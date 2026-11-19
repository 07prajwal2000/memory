package runner

import (
	"testing"
)

func TestRunnerMock(t *testing.T) {
	ds := &Dataset{
		Name: "test_ds",
		Corpus: []CorpusDoc{
			{ID: "c1", DocID: "d1", Text: "Go concurrency primitives context and sync mutex."},
			{ID: "c2", DocID: "d2", Text: "SQLite vector database and full-text search indexing."},
		},
		Queries: []QueryDoc{
			{ID: "q1", Query: "concurrency context", Class: "keyword", RelevantIDs: []string{"d1"}},
			{ID: "q2", Query: "sqlite index", Class: "keyword", RelevantIDs: []string{"d2"}},
		},
		Qrels: map[string]map[string]float64{
			"q1": {"d1": 1.0},
			"q2": {"d2": 1.0},
		},
	}

	bm25Idx := BuildBM25Index(ds, nil)
	pe := &PreparedEmbeddings{
		CorpusIDs: []string{"c1", "c2"},
		DocIDMap: map[string]string{
			"c1": "d1",
			"c2": "d2",
		},
		CorpusVecs: [][]float32{
			{1.0, 0.0},
			{0.0, 1.0},
		},
		QueryIDs: []string{"q1", "q2"},
		QueryVecs: [][]float32{
			{0.9, 0.1},
			{0.1, 0.9},
		},
	}

	resBM25 := Evaluate(ds, pe, bm25Idx, "bm25", 0, false, 0)
	if resBM25.Overall.MeanMRR10 != 1.0 {
		t.Errorf("expected BM25 MRR 1.0, got %f", resBM25.Overall.MeanMRR10)
	}

	resVec := Evaluate(ds, pe, bm25Idx, "vec_f32", 0, false, 0)
	if resVec.Overall.MeanMRR10 != 1.0 {
		t.Errorf("expected Vec MRR 1.0, got %f", resVec.Overall.MeanMRR10)
	}

	resHybrid := Evaluate(ds, pe, bm25Idx, "hybrid_confidence", 0, false, 0)
	if resHybrid.Overall.MeanMRR10 != 1.0 {
		t.Errorf("expected Hybrid MRR 1.0, got %f", resHybrid.Overall.MeanMRR10)
	}
}
