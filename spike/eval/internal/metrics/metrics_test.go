package metrics

import (
	"math"
	"testing"
)

func TestMetricsBasic(t *testing.T) {
	retrieved := []string{"d1", "d2", "d3", "d4", "d5"}
	relevance := map[string]float64{
		"d1": 1.0,
		"d3": 2.0,
	}

	// NDCG@3:
	// DCG@3:
	// i=0 (d1): (2^1 - 1)/log2(2) = 1.0 / 1.0 = 1.0
	// i=1 (d2): rel=0 => 0
	// i=2 (d3): (2^2 - 1)/log2(4) = 3.0 / 2.0 = 1.5
	// Total DCG@3 = 2.5
	// Ideal ranking: gains=[2.0, 1.0]
	// i=0: (2^2 - 1)/log2(2) = 3.0
	// i=1: (2^1 - 1)/log2(3) = 1.0 / 1.5849625 = 0.63092975
	// Total IDCG@3 = 3.63092975
	// nDCG@3 = 2.5 / 3.63092975 = 0.68853
	ndcg := ComputeNDCG(retrieved, relevance, 3)
	expectedNDCG := 2.5 / (3.0 + 1.0/math.Log2(3.0))
	if math.Abs(ndcg-expectedNDCG) > 1e-4 {
		t.Errorf("expected nDCG %f, got %f", expectedNDCG, ndcg)
	}

	// MRR@10: first relevant is at rank 1 (i=0) -> 1.0
	mrr := ComputeMRR(retrieved, relevance, 10)
	if mrr != 1.0 {
		t.Errorf("expected MRR 1.0, got %f", mrr)
	}

	// Recall@10: 2 relevant total, both in top 5 -> 1.0
	r10 := ComputeRecall(retrieved, relevance, 10)
	if r10 != 1.0 {
		t.Errorf("expected Recall@10 1.0, got %f", r10)
	}

	// Recall@2: only d1 in top 2 -> 1/2 = 0.5
	r2 := ComputeRecall(retrieved, relevance, 2)
	if r2 != 0.5 {
		t.Errorf("expected Recall@2 0.5, got %f", r2)
	}
}

func TestPairedBootstrapCI(t *testing.T) {
	// Candidate is strictly better on every item
	cand := []float64{0.9, 0.8, 0.7, 0.85, 0.95}
	base := []float64{0.5, 0.4, 0.5, 0.55, 0.60}

	ci := PairedBootstrapCI(cand, base, 1000, 42)
	if !ci.Significant {
		t.Errorf("expected significant difference, got not significant: %v", ci)
	}
	if ci.Lower95 <= 0 {
		t.Errorf("expected lower > 0, got %f", ci.Lower95)
	}

	// Candidate and base are identical
	ciZero := PairedBootstrapCI(cand, cand, 1000, 42)
	if ciZero.Significant {
		t.Errorf("expected non-significant for identical, got %v", ciZero)
	}
	if ciZero.MeanDelta != 0.0 {
		t.Errorf("expected mean delta 0.0, got %f", ciZero.MeanDelta)
	}
}
