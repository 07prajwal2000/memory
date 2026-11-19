package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"memory/internal/llm"
)

type quickEvalDataset struct {
	Name    string
	Docs    []Doc
	Queries []quickEvalQuery
}

type quickEvalQuery struct {
	ID    string
	Text  string
	Qrels map[string]float64
}

type quickEvalMetrics struct {
	NDCG10   float64
	MRR10    float64
	Recall10 float64
}

func computeMetrics(hits []Hit, qrels map[string]float64) (float64, float64, float64) {
	if len(qrels) == 0 {
		return 0, 0, 0
	}

	totalRelevant := 0
	for _, g := range qrels {
		if g > 0 {
			totalRelevant++
		}
	}
	if totalRelevant == 0 {
		return 0, 0, 0
	}

	retrievedRelevant := 0
	firstRank := 0
	dcg := 0.0

	for i, h := range hits {
		if i >= 10 {
			break
		}
		rank := i + 1
		gain := qrels[h.DocID]
		if gain > 0 {
			retrievedRelevant++
			if firstRank == 0 {
				firstRank = rank
			}
			dcg += (math.Pow(2, gain) - 1.0) / math.Log2(float64(rank+1))
		}
	}

	// Compute IDCG@10
	var idealGains []float64
	for _, g := range qrels {
		if g > 0 {
			idealGains = append(idealGains, g)
		}
	}
	sort.Slice(idealGains, func(i, j int) bool {
		return idealGains[i] > idealGains[j]
	})

	idcg := 0.0
	for i := 0; i < len(idealGains) && i < 10; i++ {
		idcg += (math.Pow(2, idealGains[i]) - 1.0) / math.Log2(float64(i+2))
	}

	ndcg := 0.0
	if idcg > 0 {
		ndcg = dcg / idcg
	}

	mrr := 0.0
	if firstRank > 0 {
		mrr = 1.0 / float64(firstRank)
	}

	recall := float64(retrievedRelevant) / float64(totalRelevant)
	return ndcg, mrr, recall
}

func runEvalOnDataset(t *testing.T, ctx context.Context, embedder llm.Embedder, ds quickEvalDataset, opts ...Option) quickEvalMetrics {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "eval_"+ds.Name+".db")

	allOpts := append([]Option{
		WithRebuild(),
		WithResidentVectors(true),
		WithResidentVectorsSync(true),
		WithUpsertConcurrency(2),
		WithUpsertBatchSize(16),
	}, opts...)

	ix, err := OpenIndex(dbPath, embedder, nil, allOpts...)
	if err != nil {
		t.Fatalf("OpenIndex for %s: %v", ds.Name, err)
	}
	defer ix.Close()

	// Ingest all documents
	if err := ix.UpsertMany(ctx, ds.Docs); err != nil {
		if errors.Is(err, ErrVectorsPending) {
			for ix.PendingVectors() > 0 {
				filled, fErr := ix.FillPendingVectors(ctx)
				if fErr != nil || filled == 0 {
					break
				}
			}
		} else {
			t.Fatalf("UpsertMany for %s: %v", ds.Name, err)
		}
	}

	var sumNDCG, sumMRR, sumRecall float64
	for _, q := range ds.Queries {
		res, err := ix.Search(ctx, Query{
			Text:     q.Text,
			Contexts: []string{"global"},
			Limit:    10,
		})
		if err != nil {
			t.Fatalf("Search failed for query %s: %v", q.ID, err)
		}
		ndcg, mrr, recall := computeMetrics(res.Hits, q.Qrels)
		sumNDCG += ndcg
		sumMRR += mrr
		sumRecall += recall
	}

	n := float64(len(ds.Queries))
	if n == 0 {
		return quickEvalMetrics{}
	}
	return quickEvalMetrics{
		NDCG10:   sumNDCG / n,
		MRR10:    sumMRR / n,
		Recall10: sumRecall / n,
	}
}

func TestQuickSanityEval_All(t *testing.T) {
	if os.Getenv("MEM_QUICK_EVAL") != "1" {
		t.Skip("Skipping quick sanity eval (set MEM_QUICK_EVAL=1 to run)")
	}

	llamaServer := `C:\Users\manua\.mem\bin\llama-server.exe`
	if _, err := os.Stat(llamaServer); err != nil {
		llamaServer = `D:\ProgrammingFiles\OSS\memory\spike\bin\llama-server.exe`
	}
	embedModel := `C:\Users\manua\.mem\models\bge-small-en-v1.5-q8_0.gguf`
	if _, err := os.Stat(embedModel); err != nil {
		embedModel = `D:\ProgrammingFiles\OSS\memory\spike\models\bge-small-en-v1.5-q8_0.gguf`
	}

	t.Logf("Using llama-server: %s", llamaServer)
	t.Logf("Using embedModel: %s", embedModel)

	sidecar := llm.NewEmbedSidecar(llm.SidecarConfig{
		Binary:       llamaServer,
		Model:        embedModel,
		Parallel:     4,
		Args:         []string{"-c", "2048", "-b", "2048", "-ub", "2048"},
		StartTimeout: 30 * time.Second,
		IdleTimeout:  15 * time.Minute,
	})
	defer sidecar.Close()
	embedder := sidecar.AsEmbedder()

	ctx := context.Background()

	// 1. Load Memlike dataset
	memlikePath := filepath.Join("..", "..", "spike", "eval", "data", "memlike.json")
	memlikeBytes, err := os.ReadFile(memlikePath)
	if err != nil {
		t.Fatalf("read memlike.json: %v", err)
	}
	var rawMemlike struct {
		Name   string `json:"name"`
		Corpus []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Heading string `json:"heading"`
			Text    string `json:"text"`
		} `json:"corpus"`
		Queries []struct {
			ID    string `json:"id"`
			Query string `json:"query"`
		} `json:"queries"`
		Qrels map[string]map[string]float64 `json:"qrels"`
	}
	if err := json.Unmarshal(memlikeBytes, &rawMemlike); err != nil {
		t.Fatalf("unmarshal memlike: %v", err)
	}

	var memlikeDocs []Doc
	for _, c := range rawMemlike.Corpus {
		memlikeDocs = append(memlikeDocs, Doc{
			ID:      c.ID,
			Context: "global",
			Name:    c.Name,
			Body:    c.Text,
			Version: 1,
		})
	}
	var memlikeQueries []quickEvalQuery
	for _, q := range rawMemlike.Queries {
		memlikeQueries = append(memlikeQueries, quickEvalQuery{
			ID:    q.ID,
			Text:  q.Query,
			Qrels: rawMemlike.Qrels[q.ID],
		})
	}
	dsMemlike := quickEvalDataset{Name: "memlike", Docs: memlikeDocs, Queries: memlikeQueries}

	// 2. Load testdata corpus from eval_dataset.json
	testdataPath := filepath.Join("testdata", "eval_dataset.json")
	testdataBytes, err := os.ReadFile(testdataPath)
	if err != nil {
		t.Fatalf("read eval_dataset.json: %v", err)
	}
	var rawTestdata struct {
		Corpus []struct {
			ID      string `json:"id"`
			Context string `json:"context"`
			Name    string `json:"name"`
			Heading string `json:"heading"`
			Text    string `json:"text"`
		} `json:"corpus"`
		Queries []struct {
			ID          string   `json:"id"`
			Query       string   `json:"query"`
			RelevantIDs []string `json:"relevant_ids"`
		} `json:"queries"`
	}
	if err := json.Unmarshal(testdataBytes, &rawTestdata); err != nil {
		t.Fatalf("unmarshal eval_dataset: %v", err)
	}

	var testdataDocs []Doc
	for _, c := range rawTestdata.Corpus {
		testdataDocs = append(testdataDocs, Doc{
			ID:      c.ID,
			Context: "global",
			Name:    c.Name,
			Body:    c.Text,
			Version: 1,
		})
	}

	var tuningQueries []quickEvalQuery
	for _, q := range rawTestdata.Queries {
		qrels := make(map[string]float64)
		for _, rid := range q.RelevantIDs {
			qrels[rid] = 1.0
		}
		tuningQueries = append(tuningQueries, quickEvalQuery{
			ID:    q.ID,
			Text:  q.Query,
			Qrels: qrels,
		})
	}
	dsTuning := quickEvalDataset{Name: "tuning", Docs: testdataDocs, Queries: tuningQueries}

	// 3. Load Held-Out queries
	loadSplitQueries := func(filename, splitName string) quickEvalDataset {
		p := filepath.Join("testdata", filename)
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", filename, err)
		}
		var raw struct {
			Queries []struct {
				ID          string   `json:"id"`
				Query       string   `json:"query"`
				RelevantIDs []string `json:"relevant_ids"`
			} `json:"queries"`
		}
		if err := json.Unmarshal(b, &raw); err != nil {
			t.Fatalf("unmarshal %s: %v", filename, err)
		}
		var queries []quickEvalQuery
		for _, q := range raw.Queries {
			qrels := make(map[string]float64)
			for _, rid := range q.RelevantIDs {
				qrels[rid] = 1.0
			}
			queries = append(queries, quickEvalQuery{
				ID:    q.ID,
				Text:  q.Query,
				Qrels: qrels,
			})
		}
		return quickEvalDataset{Name: splitName, Docs: testdataDocs, Queries: queries}
	}

	dsHeldOut := loadSplitQueries("eval_held_out.json", "held_out")
	dsFinal := loadSplitQueries("eval_final.json", "final")

	datasets := []quickEvalDataset{dsMemlike, dsTuning, dsHeldOut, dsFinal}

	type configCase struct {
		name string
		opts []Option
	}
	configs := []configCase{
		{
			name: "1. Baseline",
			opts: []Option{WithContextualEmbed(false), WithTier0FTS(false), WithAppendKeywordsToEmbed(false)},
		},
		{
			name: "2. +contextual chunk text",
			opts: []Option{WithContextualEmbed(true), WithTier0FTS(false), WithAppendKeywordsToEmbed(false)},
		},
		{
			name: "3. +Tier-0 FTS",
			opts: []Option{WithContextualEmbed(true), WithTier0FTS(true), WithAppendKeywordsToEmbed(false)},
		},
		{
			name: "4. +keywords in embed text",
			opts: []Option{WithContextualEmbed(true), WithTier0FTS(true), WithAppendKeywordsToEmbed(true)},
		},
	}

	fmt.Println("\n=========================================================================================")
	fmt.Println("QUICK SANITY EVALUATION (REPORT-10)")
	fmt.Println("=========================================================================================")

	type summaryRow struct {
		Dataset string
		Config  string
		NDCG10  float64
		MRR10   float64
		Recall10 float64
	}
	var results []summaryRow

	for _, ds := range datasets {
		fmt.Printf("\n--- Dataset: %s (Docs: %d, Queries: %d) ---\n", ds.Name, len(ds.Docs), len(ds.Queries))
		for _, cfg := range configs {
			t0 := time.Now()
			m := runEvalOnDataset(t, ctx, embedder, ds, cfg.opts...)
			dur := time.Since(t0)
			fmt.Printf("  %-30s | nDCG@10: %.4f | MRR@10: %.4f | Recall@10: %.4f | Took: %v\n",
				cfg.name, m.NDCG10, m.MRR10, m.Recall10, dur)
			results = append(results, summaryRow{
				Dataset:  ds.Name,
				Config:   cfg.name,
				NDCG10:   m.NDCG10,
				MRR10:    m.MRR10,
				Recall10: m.Recall10,
			})
		}
	}

	fmt.Println("\n=========================================================================================")
	fmt.Println("FINAL RESULTS TABLE")
	fmt.Println("=========================================================================================")
	fmt.Printf("| %-10s | %-28s | %-8s | %-8s | %-9s |\n", "Dataset", "Configuration", "nDCG@10", "MRR@10", "Recall@10")
	fmt.Println("|------------|------------------------------|----------|----------|-----------|")
	for _, r := range results {
		fmt.Printf("| %-10s | %-28s | %8.4f | %8.4f | %9.4f |\n", r.Dataset, r.Config, r.NDCG10, r.MRR10, r.Recall10)
	}
	fmt.Println("=========================================================================================")
}

func TestDiagnoseMemlike(t *testing.T) {
	if os.Getenv("MEM_QUICK_EVAL") != "1" {
		t.Skip("Skipping diagnose (set MEM_QUICK_EVAL=1 to run)")
	}

	llamaServer := `C:\Users\manua\.mem\bin\llama-server.exe`
	if _, err := os.Stat(llamaServer); err != nil {
		llamaServer = `D:\ProgrammingFiles\OSS\memory\spike\bin\llama-server.exe`
	}
	embedModel := `C:\Users\manua\.mem\models\bge-small-en-v1.5-q8_0.gguf`
	if _, err := os.Stat(embedModel); err != nil {
		embedModel = `D:\ProgrammingFiles\OSS\memory\spike\models\bge-small-en-v1.5-q8_0.gguf`
	}

	sidecar := llm.NewEmbedSidecar(llm.SidecarConfig{
		Binary:       llamaServer,
		Model:        embedModel,
		Parallel:     4,
		Args:         []string{"-c", "2048", "-b", "2048", "-ub", "2048"},
		StartTimeout: 30 * time.Second,
		IdleTimeout:  15 * time.Minute,
	})
	defer sidecar.Close()
	embedder := sidecar.AsEmbedder()
	ctx := context.Background()

	testVecs, testErr := embedder.Embed(ctx, llm.KindDocument, []string{"test"})
	fmt.Printf("TEST EMBEDDER: err=%v, len=%d\n", testErr, len(testVecs))


	// Load memlike
	memlikePath := filepath.Join("..", "..", "spike", "eval", "data", "memlike.json")
	memlikeBytes, err := os.ReadFile(memlikePath)
	if err != nil {
		t.Fatalf("read memlike.json: %v", err)
	}
	var rawMemlike struct {
		Name   string `json:"name"`
		Corpus []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Heading string `json:"heading"`
			Text    string `json:"text"`
		} `json:"corpus"`
		Queries []struct {
			ID    string `json:"id"`
			Class string `json:"class"`
			Query string `json:"query"`
		} `json:"queries"`
		Qrels map[string]map[string]float64 `json:"qrels"`
	}
	if err := json.Unmarshal(memlikeBytes, &rawMemlike); err != nil {
		t.Fatalf("unmarshal memlike: %v", err)
	}

	var memlikeDocs []Doc
	for _, c := range rawMemlike.Corpus {
		memlikeDocs = append(memlikeDocs, Doc{
			ID:      c.ID,
			Context: "global",
			Name:    c.Name,
			Body:    c.Text,
			Version: 1,
		})
	}
	var memlikeQueries []quickEvalQuery
	for _, q := range rawMemlike.Queries {
		memlikeQueries = append(memlikeQueries, quickEvalQuery{
			ID:    q.ID,
			Text:  q.Query,
			Qrels: rawMemlike.Qrels[q.ID],
		})
	}
	dsMemlike := quickEvalDataset{Name: "memlike", Docs: memlikeDocs, Queries: memlikeQueries}

	// Also load tuning, held_out, final for regression checks
	testdataPath := filepath.Join("testdata", "eval_dataset.json")
	testdataBytes, _ := os.ReadFile(testdataPath)
	var rawTestdata struct {
		Corpus []struct {
			ID      string `json:"id"`
			Context string `json:"context"`
			Name    string `json:"name"`
			Heading string `json:"heading"`
			Text    string `json:"text"`
		} `json:"corpus"`
		Queries []struct {
			ID          string   `json:"id"`
			Query       string   `json:"query"`
			RelevantIDs []string `json:"relevant_ids"`
		} `json:"queries"`
	}
	_ = json.Unmarshal(testdataBytes, &rawTestdata)
	var testdataDocs []Doc
	for _, c := range rawTestdata.Corpus {
		testdataDocs = append(testdataDocs, Doc{
			ID:      c.ID,
			Context: "global",
			Name:    c.Name,
			Body:    c.Text,
			Version: 1,
		})
	}
	loadSplit := func(filename, name string) quickEvalDataset {
		b, _ := os.ReadFile(filepath.Join("testdata", filename))
		var raw struct {
			Queries []struct {
				ID          string   `json:"id"`
				Query       string   `json:"query"`
				RelevantIDs []string `json:"relevant_ids"`
			} `json:"queries"`
		}
		_ = json.Unmarshal(b, &raw)
		var qs []quickEvalQuery
		for _, q := range raw.Queries {
			qrels := make(map[string]float64)
			for _, rid := range q.RelevantIDs {
				qrels[rid] = 1.0
			}
			qs = append(qs, quickEvalQuery{ID: q.ID, Text: q.Query, Qrels: qrels})
		}
		return quickEvalDataset{Name: name, Docs: testdataDocs, Queries: qs}
	}
	var tuningQueries []quickEvalQuery
	for _, q := range rawTestdata.Queries {
		qrels := make(map[string]float64)
		for _, rid := range q.RelevantIDs {
			qrels[rid] = 1.0
		}
		tuningQueries = append(tuningQueries, quickEvalQuery{ID: q.ID, Text: q.Query, Qrels: qrels})
	}
	dsTuning := quickEvalDataset{Name: "tuning", Docs: testdataDocs, Queries: tuningQueries}
	dsHeldOut := loadSplit("eval_held_out.json", "held_out")
	dsFinal := loadSplit("eval_final.json", "final")
	allDatasets := []quickEvalDataset{dsMemlike, dsTuning, dsHeldOut, dsFinal}

	type runConfig struct {
		Name       string
		IndexOpts  []Option
		QueryFuzzy *bool
		PerDoc     int
	}

	boolTrue := true
	boolFalse := false

	configs := []runConfig{
		{
			Name:       "A. Current Default (ConfidenceRRF, Fuzzy ON)",
			IndexOpts:  []Option{WithContextualEmbed(true), WithTier0FTS(true)},
			QueryFuzzy: &boolTrue,
		},
		{
			Name:       "B. Fuzzy OFF (ConfidenceRRF, Fuzzy OFF)",
			IndexOpts:  []Option{WithContextualEmbed(true), WithTier0FTS(true)},
			QueryFuzzy: &boolFalse,
		},
		{
			Name:       "C. Vec-Only (wVec=1, wBM25=0, Fuzzy OFF)",
			IndexOpts:  []Option{WithContextualEmbed(true), WithTier0FTS(true), WithFusionWeights(1.0, 0.0, 10.0)},
			QueryFuzzy: &boolFalse,
		},
		{
			Name:       "D. BM25-Only (wVec=0, wBM25=1, Fuzzy OFF)",
			IndexOpts:  []Option{WithContextualEmbed(true), WithTier0FTS(true), WithFusionWeights(0.0, 1.0, 10.0)},
			QueryFuzzy: &boolFalse,
		},
		{
			Name:       "E. Plain RRF 60 (wVec=1, wBM25=1, k=60, Fuzzy OFF)",
			IndexOpts:  []Option{WithContextualEmbed(true), WithTier0FTS(true), WithFusionWeights(1.0, 1.0, 60.0)},
			QueryFuzzy: &boolFalse,
		},
		{
			Name:       "F. Weighted RRF (wVec=1, wBM25=0.1, k=10, Fuzzy OFF)",
			IndexOpts:  []Option{WithContextualEmbed(true), WithTier0FTS(true), WithFusionWeights(1.0, 0.1, 10.0)},
			QueryFuzzy: &boolFalse,
		},
	}

	fmt.Println("\n=========================================================================================")
	fmt.Println("DIAGNOSTIC RUN")
	fmt.Println("=========================================================================================")

	for _, ds := range allDatasets {
		fmt.Printf("\n=== DATASET: %s ===\n", ds.Name)

		// Create index once for this dataset
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "diag_"+ds.Name+".db")
		allOpts := []Option{
			WithRebuild(),
			WithResidentVectors(true),
			WithResidentVectorsSync(true),
			WithUpsertConcurrency(2),
			WithUpsertBatchSize(16),
			WithContextualEmbed(true),
			WithTier0FTS(true),
		}
		ix, err := OpenIndex(dbPath, embedder, nil, allOpts...)
		if err != nil {
			t.Fatalf("OpenIndex: %v", err)
		}

		if err := ix.UpsertMany(ctx, ds.Docs); err != nil {
			if errors.Is(err, ErrVectorsPending) {
				for ix.PendingVectors() > 0 {
					filled, fErr := ix.FillPendingVectors(ctx)
					if fErr != nil || filled == 0 {
						break
					}
				}
			}
		}
		fmt.Printf("DATASET %s: Docs=%d, VecStore Count=%d, Pending=%d\n", ds.Name, len(ds.Docs), ix.vecStore.Count(), ix.PendingVectors())


		for _, cfg := range configs {
			// Apply cfg opts to ix
			for _, opt := range cfg.IndexOpts {
				opt(ix)
			}

			var sumNDCG, sumMRR, sumRecall float64
			for _, q := range ds.Queries {
				res, err := ix.Search(ctx, Query{
					Text:     q.Text,
					Contexts: []string{"global"},
					Limit:    10,
					Fuzzy:    cfg.QueryFuzzy,
					PerDoc:   cfg.PerDoc,
				})
				if err != nil {
					t.Fatalf("Search failed: %v", err)
				}
				ndcg, mrr, recall := computeMetrics(res.Hits, q.Qrels)
				sumNDCG += ndcg
				sumMRR += mrr
				sumRecall += recall
			}
			n := float64(len(ds.Queries))
			fmt.Printf("  %-45s | nDCG@10: %.4f | MRR@10: %.4f | Recall@10: %.4f\n",
				cfg.Name, sumNDCG/n, sumMRR/n, sumRecall/n)
		}
		ix.Close()
	}
}

