package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"spike/eval/internal/bm25"
	"spike/eval/internal/metrics"
)

type evalFile struct {
	Corpus  []docItem   `json:"corpus"`
	Queries []queryItem `json:"queries"`
}

type docItem struct {
	ID      string `json:"id"`
	DocID   string `json:"doc_id"`
	Name    string `json:"name"`
	Heading string `json:"heading"`
	Text    string `json:"text"`
}

type queryItem struct {
	ID          string   `json:"id"`
	Class       string   `json:"class"`
	Query       string   `json:"query"`
	RelevantIDs []string `json:"relevant_ids"`
}

func findPath(rel string) string {
	if _, err := os.Stat(rel); err == nil {
		return rel
	}
	up := filepath.Join("..", rel)
	if _, err := os.Stat(up); err == nil {
		return up
	}
	return rel
}

func main() {
	// 1. Read eval_dataset for corpus
	data, err := os.ReadFile(findPath("internal/search/testdata/eval_dataset.json"))
	if err != nil {
		panic(err)
	}
	var ef evalFile
	if err := json.Unmarshal(data, &ef); err != nil {
		panic(err)
	}

	idx := bm25.NewIndex(1.2, 0.75)
	for _, doc := range ef.Corpus {
		idx.AddDocument(bm25.Document{
			ID:    doc.ID,
			DocID: doc.DocID,
			Text:  doc.Text,
		})
	}

	runQueries("Tuning Split (80 queries)", idx, ef.Queries)

	// Held out
	dataHO, err := os.ReadFile(findPath("internal/search/testdata/eval_held_out.json"))
	if err != nil {
		panic(err)
	}
	var efHO evalFile
	if err := json.Unmarshal(dataHO, &efHO); err != nil {
		panic(err)
	}
	runQueries("Held-Out Split (40 queries)", idx, efHO.Queries)

	// Final
	dataF, err := os.ReadFile(findPath("internal/search/testdata/eval_final.json"))
	if err != nil {
		panic(err)
	}
	var efF evalFile
	if err := json.Unmarshal(dataF, &efF); err != nil {
		panic(err)
	}
	runQueries("Final Split (40 queries)", idx, efF.Queries)
}

func runQueries(splitName string, idx *bm25.Index, queries []queryItem) {
	var qms []metrics.QueryMetrics
	classMap := make(map[string][]metrics.QueryMetrics)

	for _, q := range queries {
		hits := idx.Search(q.Query, 50)
		retrieved := make([]string, len(hits))
		for i, h := range hits {
			retrieved[i] = h.ID
		}
		relMap := make(map[string]float64)
		for _, rid := range q.RelevantIDs {
			relMap[rid] = 1.0
		}
		m := metrics.QueryMetrics{
			QueryID:  q.ID,
			NDCG10:   metrics.ComputeNDCG(retrieved, relMap, 10),
			MRR10:    metrics.ComputeMRR(retrieved, relMap, 10),
			Recall10: metrics.ComputeRecall(retrieved, relMap, 10),
			Recall50: metrics.ComputeRecall(retrieved, relMap, 50),
		}
		qms = append(qms, m)
		if q.Class != "" {
			classMap[q.Class] = append(classMap[q.Class], m)
		}
	}

	agg := metrics.ComputeAggregate(qms)
	fmt.Printf("=== %s ===\n", splitName)
	fmt.Printf("Overall MRR@10: %.4f | Recall@10: %.4f | Recall@50: %.4f | NDCG@10: %.4f\n",
		agg.MeanMRR10, agg.MeanRecall10, agg.MeanRecall50, agg.MeanNDCG10)

	for cls, list := range classMap {
		cAgg := metrics.ComputeAggregate(list)
		fmt.Printf("  Class %-12s (n=%d) MRR@10: %.4f | Recall@10: %.4f\n", cls, len(list), cAgg.MeanMRR10, cAgg.MeanRecall10)
	}
}
