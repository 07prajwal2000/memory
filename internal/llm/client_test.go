package llm

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPEmbedder_BatchingAndNormalization(t *testing.T) {
	var requestCount int32
	var receivedInputs [][]string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			http.NotFound(w, r)
			return
		}
		atomic.AddInt32(&requestCount, 1)

		var req embeddingReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		receivedInputs = append(receivedInputs, req.Input)

		data := make([]embeddingItem, len(req.Input))
		for i := range req.Input {
			vec := make([]float32, 384)
			// Non-normalized vector: constant 2.0 everywhere
			for d := range vec {
				vec[d] = 2.0
			}
			data[i] = embeddingItem{
				Index:     i,
				Embedding: vec,
			}
		}

		resp := embeddingResp{Data: data}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	embedder := &HTTPEmbedder{
		BaseURL:   server.URL,
		BatchSize: 2,
		Dim:       384,
	}

	texts := []string{"chunk 1", "chunk 2", "chunk 3", "chunk 4", "chunk 5"}
	embeddings, err := embedder.Embed(context.Background(), KindDocument, texts)
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}

	if len(embeddings) != len(texts) {
		t.Fatalf("expected %d embeddings, got %d", len(texts), len(embeddings))
	}

	// 5 texts with batch size 2 => 3 requests
	if atomic.LoadInt32(&requestCount) != 3 {
		t.Errorf("expected 3 requests, got %d", requestCount)
	}

	// Verify L2 normalization: each vector should have unit length
	for i, vec := range embeddings {
		var sum float64
		for _, v := range vec {
			sum += float64(v * v)
		}
		norm := math.Sqrt(sum)
		if math.Abs(norm-1.0) > 1e-5 {
			t.Errorf("vector %d norm is %f, expected 1.0", i, norm)
		}
	}
}

func TestHTTPEmbedder_QueryPrefix(t *testing.T) {
	var lastReceived []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embeddingReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		lastReceived = req.Input

		data := make([]embeddingItem, len(req.Input))
		for i := range req.Input {
			data[i] = embeddingItem{
				Index:     i,
				Embedding: make([]float32, 384),
			}
		}
		_ = json.NewEncoder(w).Encode(embeddingResp{Data: data})
	}))
	defer server.Close()

	embedder := &HTTPEmbedder{
		BaseURL:     server.URL,
		QueryPrefix: "PREFIX: ",
		Dim:         384,
	}

	// Test KindQuery: must have prefix
	_, err := embedder.Embed(context.Background(), KindQuery, []string{"search query"})
	if err != nil {
		t.Fatalf("Embed query failed: %v", err)
	}
	if len(lastReceived) != 1 || lastReceived[0] != "PREFIX: search query" {
		t.Errorf("unexpected query input: %v", lastReceived)
	}

	// Test KindDocument: must NOT have prefix
	_, err = embedder.Embed(context.Background(), KindDocument, []string{"document text"})
	if err != nil {
		t.Fatalf("Embed doc failed: %v", err)
	}
	if len(lastReceived) != 1 || lastReceived[0] != "document text" {
		t.Errorf("unexpected document input: %v", lastReceived)
	}
}

func TestHTTPEmbedder_DimValidation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := []embeddingItem{
			{Index: 0, Embedding: make([]float32, 128)}, // wrong dim (128 vs 384)
		}
		_ = json.NewEncoder(w).Encode(embeddingResp{Data: data})
	}))
	defer server.Close()

	embedder := &HTTPEmbedder{
		BaseURL: server.URL,
		Dim:     384,
	}

	_, err := embedder.Embed(context.Background(), KindDocument, []string{"test"})
	if err == nil {
		t.Fatal("expected error on dimension mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "invalid embedding dimension 128, expected 384") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestHTTPReranker_SuccessAndFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/rerank" {
			// Simulate 404 to test fallback to /rerank
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/rerank" {
			var req rerankReq
			_ = json.NewDecoder(r.Body).Decode(&req)

			results := make([]rerankItem, len(req.Documents))
			for i := range req.Documents {
				results[i] = rerankItem{
					Index:          i,
					RelevanceScore: float64(len(req.Documents)-i) * 0.1,
				}
			}
			_ = json.NewEncoder(w).Encode(rerankResp{Results: results})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	reranker := &HTTPReranker{
		BaseURL:   server.URL,
		MaxDocs:   5,
		MaxDocLen: 20,
	}

	docs := []string{"doc 1", "doc 2", "very long document that exceeds max doc len"}
	scores, err := reranker.Rerank(context.Background(), "query", docs)
	if err != nil {
		t.Fatalf("Rerank failed: %v", err)
	}

	if len(scores) != len(docs) {
		t.Fatalf("expected %d scores, got %d", len(docs), len(scores))
	}
	if scores[0] <= scores[1] {
		t.Errorf("expected descending scores, got %v", scores)
	}
}

func TestHTTPReranker_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	reranker := &HTTPReranker{
		BaseURL: server.URL,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := reranker.Rerank(ctx, "query", []string{"doc"})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}
