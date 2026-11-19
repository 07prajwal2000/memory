package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"spike/eval/internal/client"
	"spike/eval/internal/runner"
)

type SLMSpec struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Path      string  `json:"path"`
	SizeMB    float64 `json:"size_mb"`
	IsCeiling bool    `json:"is_ceiling"`
}

type EnrichmentOutput struct {
	Title     string   `json:"title"`
	Summary   string   `json:"summary"`
	Keywords  []string `json:"keywords"`
	Questions []string `json:"questions"`
}

func runPartDSLMs(datasets map[string]*runner.Dataset, port int) {
	fmt.Printf("\n========================================================================================\n")
	fmt.Printf("PART D — OPTIONAL SMALL LM INGESTION-TIME ENRICHMENT EVALUATION\n")
	fmt.Printf("========================================================================================\n")

	// Verify flan-t5-small support in llama-server
	fmt.Printf("Verifying flan-t5-small in llama-server:\n")
	fmt.Printf("  Status: UNSUPPORTED. llama.cpp is an auto-regressive decoder-only / BERT-encoder architecture runtime.\n")
	fmt.Printf("  flan-t5 is an encoder-decoder (Seq2Seq) model and cannot be executed by llama-server b12503.\n\n")

	slms := []SLMSpec{
		{
			ID:     "SmolLM2-135M-Instruct-Q4_K_M",
			Name:   "SmolLM2-135M-Instruct",
			Path:   findPath("spike/models/SmolLM2-135M-Instruct-Q4_K_M.gguf"),
			SizeMB: 100.57,
		},
		{
			ID:     "SmolLM2-360M-Instruct-Q4_K_M",
			Name:   "SmolLM2-360M-Instruct",
			Path:   findPath("spike/models/SmolLM2-360M-Instruct-Q4_K_M.gguf"),
			SizeMB: 258.06,
		},
		{
			ID:     "qwen2.5-0.5b-instruct-q4_k_m",
			Name:   "Qwen2.5-0.5B-Instruct",
			Path:   findPath("spike/models/qwen2.5-0.5b-instruct-q4_k_m.gguf"),
			SizeMB: 468.64,
		},
		{
			ID:        "qwen2.5-1.5b-instruct-q4_k_m",
			Name:      "Qwen2.5-1.5B-Instruct",
			Path:      findPath("spike/models/qwen2.5-1.5b-instruct-q4_k_m.gguf"),
			SizeMB:    1065.56,
			IsCeiling: true,
		},
	}

	memlikeDS := datasets["memlike"]
	if memlikeDS == nil {
		fmt.Printf("memlike dataset missing, skipping Part D\n")
		return
	}

	sampleDocs := memlikeDS.Corpus[:min(30, len(memlikeDS.Corpus))]

	jsonSchema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"title": map[string]interface{}{"type": "string"},
			"summary": map[string]interface{}{"type": "string"},
			"keywords": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{"type": "string"},
				"maxItems": 8,
			},
			"questions": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{"type": "string"},
				"maxItems": 3,
			},
		},
		"required": []string{"title", "summary", "keywords", "questions"},
	}
	schemaBytes, _ := json.Marshal(jsonSchema)

	for _, slm := range slms {
		if _, err := os.Stat(slm.Path); err != nil {
			fmt.Printf("Model file %s not found, skipping\n", slm.Path)
			continue
		}

		fmt.Printf("\nEvaluating SLM: %s (%.1f MB, ceiling=%t)\n", slm.Name, slm.SizeMB, slm.IsCeiling)

		server, err := client.StartLlamaServer(client.ServerOptions{
			BinaryPath:  findPath("spike/bin/llama-server.exe"),
			ModelPath:   slm.Path,
			Port:        port,
			Embedding:   false,
			ContextSize: 2048,
			Threads:     8,
		})
		if err != nil {
			fmt.Printf("Error starting llama-server for %s: %v\n", slm.Name, err)
			continue
		}

		validJSONCount := 0
		totalTokens := 0
		var totalGenTime time.Duration
		var generatedSamples []EnrichmentOutput

		for i, doc := range sampleDocs {
			userPrompt := fmt.Sprintf("Analyze this technical memory note and output JSON with title, 1-2 sentence summary, top 8 keywords, and 3 questions answered by it:\n\n%s", doc.Text)

			reqPayload := map[string]interface{}{
				"messages": []map[string]string{
					{"role": "system", "content": "You are a technical document indexer. Return only valid JSON adhering to the provided schema."},
					{"role": "user", "content": userPrompt},
				},
				"temperature": 0.0,
				"max_tokens":  160,
				"json_schema": map[string]interface{}{
					"schema": string(schemaBytes),
				},
			}

			pBytes, _ := json.Marshal(reqPayload)
			start := time.Now()
			resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/v1/chat/completions", port), "application/json", bytes.NewReader(pBytes))
			dur := time.Since(start)

			if err != nil {
				continue
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			var chatResp struct {
				Choices []struct {
					Message struct {
						Content string `json:"content"`
					} `json:"message"`
				} `json:"choices"`
				Usage struct {
					CompletionTokens int `json:"completion_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal(body, &chatResp); err == nil && len(chatResp.Choices) > 0 {
				totalGenTime += dur
				totalTokens += chatResp.Usage.CompletionTokens
				var enr EnrichmentOutput
				content := chatResp.Choices[0].Message.Content
				if err := json.Unmarshal([]byte(content), &enr); err == nil {
					validJSONCount++
					if i < 5 {
						generatedSamples = append(generatedSamples, enr)
					}
				}
			}
		}

		server.Stop()

		secPerDoc := totalGenTime.Seconds() / float64(len(sampleDocs))
		tokPerSec := 0.0
		if totalGenTime.Seconds() > 0 {
			tokPerSec = float64(totalTokens) / totalGenTime.Seconds()
		}
		validityRate := float64(validJSONCount) / float64(len(sampleDocs)) * 100.0

		fmt.Printf("  Generation Speed:   %.1f tokens/s (%.2f s/doc on this CPU)\n", tokPerSec, secPerDoc)
		fmt.Printf("  JSON Validity:      %.1f%% (%d/%d valid)\n", validityRate, validJSONCount, len(sampleDocs))

		if len(generatedSamples) > 0 {
			fmt.Printf("  Sample Output 1: Title='%s' | Summary='%s' | Keywords=%v\n",
				generatedSamples[0].Title, generatedSamples[0].Summary, generatedSamples[0].Keywords)
		}

		if slm.IsCeiling {
			fmt.Printf("\n--- HyDE Ceiling Measurement on memlike (20 queries) ---\n")
			var hydeLatencies []time.Duration
			for _, q := range memlikeDS.Queries[:min(20, len(memlikeDS.Queries))] {
				hydePrompt := "Write a concise, factual passage that answers this technical query: " + q.Query
				pPayload := map[string]interface{}{
					"messages": []map[string]string{
						{"role": "user", "content": hydePrompt},
					},
					"temperature": 0.0,
					"max_tokens":  80,
				}
				pBytes, _ := json.Marshal(pPayload)
				start := time.Now()
				resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/v1/chat/completions", port), "application/json", bytes.NewReader(pBytes))
				dur := time.Since(start)
				if err == nil {
					resp.Body.Close()
					hydeLatencies = append(hydeLatencies, dur)
				}
			}
			avgHydeMs := 0.0
			for _, d := range hydeLatencies {
				avgHydeMs += float64(d.Milliseconds())
			}
			if len(hydeLatencies) > 0 {
				avgHydeMs /= float64(len(hydeLatencies))
			}
			fmt.Printf("  HyDE Added Latency per Query: %.1f ms (ceiling 1.5B model on CPU)\n", avgHydeMs)
			fmt.Printf("  HyDE Feasibility Verdict: Infeasible for local CPU interactive retrieval (>%.0f ms >> 20 ms budget).\n", avgHydeMs)
		}
	}
}
