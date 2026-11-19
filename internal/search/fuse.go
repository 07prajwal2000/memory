package search

import (
	"sort"
	"strings"
	"unicode"
)

type candidateHit struct {
	chunkID      int64
	docID        string
	context      string
	name         string
	heading      string
	text         string
	chunk        int
	score        float64
	sources      []string
	matchedTerms []string
}

type FusionMode int

const (
	// FusionModeConfidence uses confidence-aware reciprocal rank fusion (default).
	// Boosts BM25 when rare identifiers or high FTS5 confidence gaps are present;
	// relies primarily on vector search for paraphrase queries.
	FusionModeConfidence FusionMode = iota

	// FusionModeWeightedRRF uses classic Weighted Reciprocal Rank Fusion.
	FusionModeWeightedRRF

	// FusionModeBoost uses vector-first ranking with BM25 boost.
	FusionModeBoost
)

// IsRareIdentifier detects whether a token in a query represents a code/system identifier:
// paths (contains /, \, ~), error/flag prefixes (ERR_, FLAG_, CVE-, ADR-, UUID, PORT_, etc.),
// CLI flags (- or +), dots in code (React.lazy, context.Background), underscores (snake_case),
// alphanumeric mixture (v1.5, GOMAXPROCS), or uppercase acronyms >= 3 letters (DDL, TLS, DNS, RAM, CPU).
func IsRareIdentifier(s string) bool {
	s = strings.Trim(s, `"'(),:;?[]{}!`)
	if len(s) < 2 {
		return false
	}
	// Path
	if strings.ContainsAny(s, `/\~`) {
		return true
	}
	// Prefixes
	for _, p := range []string{"ERR_", "FLAG_", "UUID", "PORT_", "SEC_", "ENV_", "CRON_", "CVE-", "ADR-"} {
		if strings.HasPrefix(strings.ToUpper(s), p) {
			return true
		}
	}
	// CLI flags
	if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") {
		return true
	}
	// Dots: code identifiers like React.lazy, context.Background
	if strings.Contains(s, ".") && !strings.HasSuffix(s, ".") {
		return true
	}
	// Underscores: snake_case
	if strings.Contains(s, "_") {
		return true
	}
	// Mixture of letters and digits: e.g. GOMAXPROCS, RS256, v1.5, OAuth2
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

	// All-caps acronyms (3+ letters): e.g. DDL, TLS, DNS, NLB, CFS, RAM, CPU
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

// HasRareIdentifier returns true if the query contains at least one rare identifier.
func HasRareIdentifier(query string) bool {
	for _, w := range strings.Fields(query) {
		if IsRareIdentifier(w) {
			return true
		}
	}
	return false
}

// ConfidenceRRF fuses vector and BM25 rankings with confidence awareness.
// Vector search provides the semantic baseline (1.0 / (10 + vRank)).
// BM25 hits are boosted strongly if:
// 1) The query contains a rare identifier and the hit is rank <= 3, OR
// 2) The hit is rank 1 and has a strong FTS5 score (score <= -13.0 or score <= -9.0 with rank1-rank2 gap >= 2.0).
// Otherwise, BM25 hits are weighted gently (lowWeight / (10 + bRank)), preventing BM25 distractors from polluting paraphrase queries.
func ConfidenceRRF(
	vecRanks map[int64]int,
	bm25Ranks map[int64]int,
	bm25Scores map[int64]float64,
	query string,
	boostAmount float64,
	lowWeight float64,
) map[int64]*candidateHit {
	return ConfidenceRRFWithFuzzy(vecRanks, bm25Ranks, bm25Scores, nil, nil, query, boostAmount, lowWeight, 0.04, 0.05)
}

// ConfidenceRRFWithFuzzy fuses vector, exact BM25, fuzzy FTS expansions, and name match rankings.
// Fuzzy matches (w=0.04) and name matches (w=0.05) enter as separate lower-weighted lists with k=15,
// ensuring exact matches strictly outrank fuzzy-only matches.
func ConfidenceRRFWithFuzzy(
	vecRanks map[int64]int,
	bm25Ranks map[int64]int,
	bm25Scores map[int64]float64,
	fuzzyRanks map[int64]int,
	nameRanks map[int64]int,
	query string,
	boostAmount float64,
	lowWeight float64,
	fuzzyWeight float64,
	nameWeight float64,
) map[int64]*candidateHit {
	if boostAmount <= 0 {
		boostAmount = 0.25
	}
	if lowWeight <= 0 {
		lowWeight = 0.08
	}
	if fuzzyWeight <= 0 {
		fuzzyWeight = 0.04
	}
	if nameWeight <= 0 {
		nameWeight = 0.05
	}

	merged := make(map[int64]*candidateHit)

	for id, rank := range vecRanks {
		h := &candidateHit{chunkID: id}
		merged[id] = h
		h.score = 1.0 / (10.0 + float64(rank))
		h.sources = append(h.sources, "vec")
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
	scoreGap := top2Score - top1Score

	for id, bRank := range bm25Ranks {
		h, exists := merged[id]
		if !exists {
			h = &candidateHit{chunkID: id}
			merged[id] = h
		}

		score := 0.0
		if bm25Scores != nil {
			score = bm25Scores[id]
		}

		isHighConfidence := false
		if qHasIdent && bRank <= 3 {
			isHighConfidence = true
		} else if bRank == 1 && (score <= -13.0 || (score <= -9.0 && scoreGap >= 2.0)) {
			isHighConfidence = true
		}

		if isHighConfidence {
			h.score += boostAmount / float64(bRank)
			h.sources = append(h.sources, "bm25_boost")
		} else {
			h.score += lowWeight / (10.0 + float64(bRank))
			h.sources = append(h.sources, "bm25")
		}
	}

	for id, fRank := range fuzzyRanks {
		h, exists := merged[id]
		if !exists {
			h = &candidateHit{chunkID: id}
			merged[id] = h
		}
		h.score += fuzzyWeight / (15.0 + float64(fRank))
		h.sources = append(h.sources, "fuzzy")
	}

	for id, nRank := range nameRanks {
		h, exists := merged[id]
		if !exists {
			h = &candidateHit{chunkID: id}
			merged[id] = h
		}
		h.score += nameWeight / (15.0 + float64(nRank))
		h.sources = append(h.sources, "name")
	}

	return merged
}

// WeightedRRF fuses rankings from multiple sources using Weighted Reciprocal Rank Fusion:
// score = sum(w / (k + rank)) where rank is 1-based.
func WeightedRRF(vecRanks map[int64]int, bm25Ranks map[int64]int, wVec, wBM25, k float64) map[int64]*candidateHit {
	if k <= 0 {
		k = 10.0
	}
	if wVec <= 0 && wBM25 <= 0 {
		wVec = 1.0
		wBM25 = 0.10
	}

	merged := make(map[int64]*candidateHit)

	for id, rank := range vecRanks {
		h, exists := merged[id]
		if !exists {
			h = &candidateHit{chunkID: id}
			merged[id] = h
		}
		h.score += wVec / (k + float64(rank))
		h.sources = append(h.sources, "vec")
	}

	for id, rank := range bm25Ranks {
		h, exists := merged[id]
		if !exists {
			h = &candidateHit{chunkID: id}
			merged[id] = h
		}
		h.score += wBM25 / (k + float64(rank))
		h.sources = append(h.sources, "bm25")
	}

	return merged
}

// BoostFusion orders by vector rank, with a boost for high-confidence BM25 hits.
func BoostFusion(vecRanks map[int64]int, bm25Ranks map[int64]int, boostRankThreshold int, boostAmount float64) map[int64]*candidateHit {
	if boostRankThreshold <= 0 {
		boostRankThreshold = 3
	}
	if boostAmount <= 0 {
		boostAmount = 0.25
	}
	merged := make(map[int64]*candidateHit)
	for id, rank := range vecRanks {
		h := &candidateHit{chunkID: id}
		merged[id] = h
		baseScore := 1.0 / (10.0 + float64(rank))
		h.score = baseScore
		h.sources = append(h.sources, "vec")
		if bmRank, ok := bm25Ranks[id]; ok && bmRank <= boostRankThreshold {
			h.score += boostAmount / float64(bmRank)
			h.sources = append(h.sources, "bm25_boost")
		}
	}
	for id, rank := range bm25Ranks {
		if _, exists := merged[id]; !exists && rank <= boostRankThreshold {
			merged[id] = &candidateHit{
				chunkID: id,
				score:   (boostAmount * 0.5) / float64(rank),
				sources: []string{"bm25_only"},
			}
		}
	}
	return merged
}

// RRF fuses rankings from multiple sources using Reciprocal Rank Fusion:
// score = sum(w / (k + rank)). Defaults to tuned weights wVec=1.0, wBM25=0.10.
func RRF(vecRanks map[int64]int, bm25Ranks map[int64]int, k float64) map[int64]*candidateHit {
	if k <= 0 {
		k = 10.0
	}
	return WeightedRRF(vecRanks, bm25Ranks, 1.0, 0.10, k)
}

// MakeSnippet extracts a window of ~500 characters around the best query term match.
func MakeSnippet(text string, query string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = 500
	}
	text = strings.TrimSpace(text)
	if len(text) <= maxLen {
		return text
	}

	// Extract query words
	var qWords []string
	for _, f := range strings.Fields(query) {
		clean := strings.ToLower(strings.TrimFunc(f, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		}))
		if len(clean) >= 2 {
			qWords = append(qWords, clean)
		}
	}

	lowerText := strings.ToLower(text)
	bestIdx := -1
	for _, w := range qWords {
		idx := strings.Index(lowerText, w)
		if idx != -1 {
			if bestIdx == -1 || idx < bestIdx {
				bestIdx = idx
			}
		}
	}

	if bestIdx == -1 {
		if len(text) > maxLen {
			return strings.TrimSpace(text[:maxLen]) + "..."
		}
		return text
	}

	half := maxLen / 2
	start := bestIdx - half
	if start < 0 {
		start = 0
	}
	end := start + maxLen
	if end > len(text) {
		end = len(text)
		start = end - maxLen
		if start < 0 {
			start = 0
		}
	}

	if start > 0 {
		for start < len(text) && !unicode.IsSpace(rune(text[start])) {
			start++
		}
	}
	if end < len(text) {
		for end > 0 && !unicode.IsSpace(rune(text[end])) {
			end--
		}
	}

	snippet := strings.TrimSpace(text[start:end])
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(text) {
		snippet = snippet + "..."
	}
	return snippet
}
