package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"spike/eval/internal/client"
	"spike/eval/internal/runner"
)

type EvalFileData struct {
	Corpus  []runner.CorpusDoc `json:"corpus"`
	Queries []runner.QueryDoc  `json:"queries"`
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func runHarnessCrossCheck(evaluator *runner.Evaluator, port int) {
	fmt.Printf("\n=======================================================\n")
	fmt.Printf("PART A.5 — HARNESS VS PRODUCT CROSS-CHECK\n")
	fmt.Printf("=======================================================\n")

	// Load eval_dataset (corpus + tuning queries)
	data, err := os.ReadFile(findPath("internal/search/testdata/eval_dataset.json"))
	if err != nil {
		fmt.Printf("Error reading eval_dataset: %v\n", err)
		return
	}
	var efTuning EvalFileData
	if err := json.Unmarshal(data, &efTuning); err != nil {
		fmt.Printf("Error unmarshaling eval_dataset: %v\n", err)
		return
	}

	// In eval_dataset.json, evaluation is chunk-level (matching eval_test.go).
	// Set DocID = ID for each chunk so evaluation compares chunk IDs.
	for i := range efTuning.Corpus {
		efTuning.Corpus[i].DocID = efTuning.Corpus[i].ID
	}

	// Build hashToChunks map for expanding duplicate text chunks
	hashToChunks := make(map[string][]string)
	chunkToHash := make(map[string]string)
	for _, c := range efTuning.Corpus {
		th := sha256Hex(c.Text)
		hashToChunks[th] = append(hashToChunks[th], c.ID)
		chunkToHash[c.ID] = th
	}

	expandQrels := func(queries []runner.QueryDoc) map[string]map[string]float64 {
		qrels := make(map[string]map[string]float64)
		for _, q := range queries {
			qrels[q.ID] = make(map[string]float64)
			for _, rid := range q.RelevantIDs {
				qrels[q.ID][rid] = 1.0
				if th, ok := chunkToHash[rid]; ok {
					for _, eqID := range hashToChunks[th] {
						qrels[q.ID][eqID] = 1.0
					}
				}
			}
		}
		return qrels
	}

	tuningDS := &runner.Dataset{
		Name:    "tuning",
		Corpus:  efTuning.Corpus,
		Queries: efTuning.Queries,
		Qrels:   expandQrels(efTuning.Queries),
	}

	// Load held-out queries
	dataHO, _ := os.ReadFile(findPath("internal/search/testdata/eval_held_out.json"))
	var efHO EvalFileData
	_ = json.Unmarshal(dataHO, &efHO)
	heldOutDS := &runner.Dataset{
		Name:    "held_out",
		Corpus:  efTuning.Corpus,
		Queries: efHO.Queries,
		Qrels:   expandQrels(efHO.Queries),
	}

	// Load final queries
	dataFinal, _ := os.ReadFile(findPath("internal/search/testdata/eval_final.json"))
	var efFinal EvalFileData
	_ = json.Unmarshal(dataFinal, &efFinal)
	finalDS := &runner.Dataset{
		Name:    "final",
		Corpus:  efTuning.Corpus,
		Queries: efFinal.Queries,
		Qrels:   expandQrels(efFinal.Queries),
	}

	// Start BGE-small Q8_0 server
	server, err := client.StartLlamaServer(client.ServerOptions{
		BinaryPath: findPath("spike/bin/llama-server.exe"),
		ModelPath:  findPath("spike/models/bge-small-en-v1.5-q8_0.gguf"),
		Port:       port,
		Embedding:  true,
		Pooling:    "cls",
		Parallel:   2,
	})
	if err != nil {
		fmt.Printf("Error starting server for crosscheck: %v\n", err)
		return
	}
	defer server.Stop()

	spec := runner.ModelSpec{
		ID:          "bge-small-en-v1.5-q8_0",
		Name:        "bge-small-en-v1.5",
		Path:        findPath("spike/models/bge-small-en-v1.5-q8_0.gguf"),
		SHA256:      "ec38e8da1425",
		PrefixQuery: "Represent this sentence for searching relevant passages: ",
		PrefixDoc:   "",
		Pooling:     "cls",
	}

	// Text extractors matching product eval_test.go
	bm25TextExtractor := func(d runner.CorpusDoc) string {
		if d.Heading != "" {
			return d.Heading + " " + d.Text
		}
		return d.Text
	}
	embedExtractor := func(d runner.CorpusDoc) string {
		if d.Name != "" && d.Heading != "" {
			return d.Name + " > " + d.Heading + "\n\n" + d.Text
		} else if d.Name != "" {
			return d.Name + "\n\n" + d.Text
		}
		return d.Text
	}

	bm25Idx := runner.BuildBM25Index(tuningDS, bm25TextExtractor)

	splits := []*runner.Dataset{tuningDS, heldOutDS, finalDS}

	for _, ds := range splits {
		pe, err := evaluator.PrepareCorpusEmbeddings(ds, spec, server, embedExtractor)
		if err != nil {
			fmt.Printf("Error preparing embeddings for %s: %v\n", ds.Name, err)
			continue
		}

		bm25Res := runner.Evaluate(ds, pe, bm25Idx, "bm25", 0, false, 0)
		vecF32Res := runner.Evaluate(ds, pe, bm25Idx, "vec_f32", 0, false, 0)
		vecI8Res := runner.Evaluate(ds, pe, bm25Idx, "vec_i8", 0, true, 1058.0)
		hybridRes := runner.Evaluate(ds, pe, bm25Idx, "hybrid_confidence", 0, false, 0)

		fmt.Printf("\n--- Split: %s (%d queries) ---\n", ds.Name, len(ds.Queries))
		fmt.Printf("  BM25 Alone:         MRR@10: %.4f | Recall@10: %.4f | NDCG@10: %.4f\n",
			bm25Res.Overall.MeanMRR10, bm25Res.Overall.MeanRecall10, bm25Res.Overall.MeanNDCG10)
		fmt.Printf("  Vec float32:        MRR@10: %.4f | Recall@10: %.4f | NDCG@10: %.4f\n",
			vecF32Res.Overall.MeanMRR10, vecF32Res.Overall.MeanRecall10, vecF32Res.Overall.MeanNDCG10)
		fmt.Printf("  Vec int8 (S=1058):  MRR@10: %.4f | Recall@10: %.4f | NDCG@10: %.4f\n",
			vecI8Res.Overall.MeanMRR10, vecI8Res.Overall.MeanRecall10, vecI8Res.Overall.MeanNDCG10)
		fmt.Printf("  Confidence RRF:     MRR@10: %.4f | Recall@10: %.4f | NDCG@10: %.4f\n",
			hybridRes.Overall.MeanMRR10, hybridRes.Overall.MeanRecall10, hybridRes.Overall.MeanNDCG10)

		for cls, cm := range hybridRes.PerClass {
			fmt.Printf("    Class %-12s MRR@10: %.4f | Recall@10: %.4f\n", cls, cm.MeanMRR10, cm.MeanRecall10)
		}
	}
}
