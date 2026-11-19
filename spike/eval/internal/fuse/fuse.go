package fuse

import (
	"sort"
	"strings"
	"unicode"
)

// IsRareIdentifier detects whether a token in a query represents a code/system identifier.
func IsRareIdentifier(s string) bool {
	s = strings.Trim(s, `"'(),:;?[]{}!`)
	if len(s) < 2 {
		return false
	}
	if strings.ContainsAny(s, `/\~`) {
		return true
	}
	for _, p := range []string{"ERR_", "FLAG_", "UUID", "PORT_", "SEC_", "ENV_", "CRON_", "CVE-", "ADR-"} {
		if strings.HasPrefix(strings.ToUpper(s), p) {
			return true
		}
	}
	if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") {
		return true
	}
	if strings.Contains(s, ".") && !strings.HasSuffix(s, ".") {
		return true
	}
	if strings.Contains(s, "_") {
		return true
	}
	hasLetter := false
	hasDigit := false
	for _, r := range s {
		if unicode.IsLetter(r) {
			hasLetter = true
		} else if unicode.IsDigit(r) {
			hasDigit = true
		}
	}
	if hasLetter && hasDigit {
		return true
	}

	allCaps := true
	letterCount := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			letterCount++
			if !unicode.IsUpper(r) {
				allCaps = false
			}
		} else {
			allCaps = false
		}
	}
	if allCaps && letterCount >= 3 {
		return true
	}

	return false
}

// HasRareIdentifier returns true if query contains at least one rare identifier.
func HasRareIdentifier(query string) bool {
	for _, w := range strings.Fields(query) {
		if IsRareIdentifier(w) {
			return true
		}
	}
	return false
}

// Candidate represents a fused document candidate.
type Candidate struct {
	ID    string
	DocID string
	Score float64
}

// ConfidenceRRF implements the product's confidence-aware fusion from internal/search/fuse.go.
// vecRanks: doc/chunk ID -> 1-based rank
// bm25Ranks: doc/chunk ID -> 1-based rank
// bm25Scores: doc/chunk ID -> BM25 score
func ConfidenceRRF(
	vecRanks map[string]int,
	bm25Ranks map[string]int,
	bm25Scores map[string]float64,
	docIDMap map[string]string, // chunk ID -> doc ID
	query string,
	boostAmount float64,
	lowWeight float64,
) []Candidate {
	if boostAmount <= 0 {
		boostAmount = 0.25
	}
	if lowWeight <= 0 {
		lowWeight = 0.08
	}

	scores := make(map[string]float64)

	for id, vRank := range vecRanks {
		scores[id] += 1.0 / (10.0 + float64(vRank))
	}

	qHasIdent := HasRareIdentifier(query)

	var top1Score, top2Score float64
	if len(bm25Scores) > 0 {
		type sc struct {
			rank  int
			score float64
		}
		var sList []sc
		for id, r := range bm25Ranks {
			sList = append(sList, sc{rank: r, score: bm25Scores[id]})
		}
		sort.Slice(sList, func(i, j int) bool {
			return sList[i].rank < sList[j].rank
		})
		if len(sList) >= 1 {
			top1Score = sList[0].score
		}
		if len(sList) >= 2 {
			top2Score = sList[1].score
		}
	}
	scoreGap := top1Score - top2Score // note: in our in-memory BM25, higher score is better

	for id, bRank := range bm25Ranks {
		score := 0.0
		if bm25Scores != nil {
			score = bm25Scores[id]
		}

		isHighConfidence := false
		if qHasIdent && bRank <= 3 {
			isHighConfidence = true
		} else if bRank == 1 && (score >= 13.0 || (score >= 9.0 && scoreGap >= 2.0)) {
			isHighConfidence = true
		}

		if isHighConfidence {
			scores[id] += boostAmount / float64(bRank)
		} else {
			scores[id] += lowWeight / (10.0 + float64(bRank))
		}
	}

	return aggregateDocLevel(scores, docIDMap)
}

// PlainRRF fuses rankings with standard 1 / (k + rank), default k=60.
func PlainRRF(vecRanks, bm25Ranks map[string]int, docIDMap map[string]string, k float64) []Candidate {
	if k <= 0 {
		k = 60.0
	}
	scores := make(map[string]float64)
	for id, r := range vecRanks {
		scores[id] += 1.0 / (k + float64(r))
	}
	for id, r := range bm25Ranks {
		scores[id] += 1.0 / (k + float64(r))
	}
	return aggregateDocLevel(scores, docIDMap)
}

// WeightedRRF fuses rankings with wVec / (k + vRank) + wBM25 / (k + bRank).
func WeightedRRF(vecRanks, bm25Ranks map[string]int, docIDMap map[string]string, wVec, wBM25, k float64) []Candidate {
	if k <= 0 {
		k = 10.0
	}
	if wVec <= 0 && wBM25 <= 0 {
		wVec = 1.0
		wBM25 = 0.10
	}
	scores := make(map[string]float64)
	for id, r := range vecRanks {
		scores[id] += wVec / (k + float64(r))
	}
	for id, r := range bm25Ranks {
		scores[id] += wBM25 / (k + float64(r))
	}
	return aggregateDocLevel(scores, docIDMap)
}

// VectorOnly returns document ranking sorted solely by vector similarity.
func VectorOnly(vecRanks map[string]int, vecScores map[string]float64, docIDMap map[string]string) []Candidate {
	scores := make(map[string]float64)
	for id, sc := range vecScores {
		scores[id] = sc
	}
	return aggregateDocLevel(scores, docIDMap)
}

// BM25Only returns document ranking sorted solely by BM25 score.
func BM25Only(bm25Ranks map[string]int, bm25Scores map[string]float64, docIDMap map[string]string) []Candidate {
	scores := make(map[string]float64)
	for id, sc := range bm25Scores {
		scores[id] = sc
	}
	return aggregateDocLevel(scores, docIDMap)
}

// aggregateDocLevel aggregates chunk-level scores to document level (max score per document).
func aggregateDocLevel(chunkScores map[string]float64, docIDMap map[string]string) []Candidate {
	docMaxScores := make(map[string]float64)
	docBestChunk := make(map[string]string)

	for id, score := range chunkScores {
		docID := id
		if docIDMap != nil {
			if d, ok := docIDMap[id]; ok && d != "" {
				docID = d
			}
		}
		if current, exists := docMaxScores[docID]; !exists || score > current {
			docMaxScores[docID] = score
			docBestChunk[docID] = id
		}
	}

	var candidates []Candidate
	for docID, score := range docMaxScores {
		candidates = append(candidates, Candidate{
			ID:    docBestChunk[docID],
			DocID: docID,
			Score: score,
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		return candidates[i].DocID < candidates[j].DocID
	})

	return candidates
}
