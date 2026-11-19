package fuse

import (
	"testing"
)

func TestConfidenceRRF(t *testing.T) {
	vecRanks := map[string]int{
		"c1": 1,
		"c2": 2,
	}
	bm25Ranks := map[string]int{
		"c1": 5,
		"c2": 1,
	}
	bm25Scores := map[string]float64{
		"c1": 5.0,
		"c2": 15.0,
	}
	docMap := map[string]string{
		"c1": "d1",
		"c2": "d2",
	}

	// Query has rare identifier: c2 has bm25Rank 1 <= 3 => high confidence boost
	res := ConfidenceRRF(vecRanks, bm25Ranks, bm25Scores, docMap, "check CVE-2024-1029", 0.25, 0.08)
	if len(res) < 2 {
		t.Fatalf("expected 2 candidates, got %d", len(res))
	}
	if res[0].DocID != "d2" {
		t.Errorf("expected d2 to rank 1st with identifier boost, got %s", res[0].DocID)
	}
}
