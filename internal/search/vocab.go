package search

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type typoCandidate struct {
	term     string
	distance int
	docCount int
}

// VocabCache caches the vocabulary terms and document frequencies from chunks_fts_vocab.
// It partitions terms by first character and by length for sub-millisecond typo candidate lookups.
type VocabCache struct {
	mu            sync.RWMutex
	loaded        bool
	loadedBuckets map[byte]bool
	dataVersion   int64
	terms         map[string]int        // term -> doc count
	byFirstChar   map[byte][]string     // first character -> list of terms
	byLen         map[int][]string      // length -> list of terms
}

func NewVocabCache() *VocabCache {
	return &VocabCache{
		loadedBuckets: make(map[byte]bool),
		terms:         make(map[string]int),
		byFirstChar:   make(map[byte][]string),
		byLen:         make(map[int][]string),
	}
}

// Invalidate marks the cache as stale so next access will reload from DB.
func (vc *VocabCache) Invalidate() {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	vc.loaded = false
	vc.loadedBuckets = make(map[byte]bool)
	vc.terms = make(map[string]int)
	vc.byFirstChar = make(map[byte][]string)
	vc.byLen = make(map[int][]string)
}

func (vc *VocabCache) ensureBucketLoadedLocked(db *sql.DB, b byte) error {
	if vc.loaded || vc.loadedBuckets[b] {
		return nil
	}
	low := string([]byte{b})
	hi := string([]byte{b + 1})
	if b == 0xFF {
		hi = string([]byte{b, 0xFF})
	}
	rows, err := db.Query("SELECT term, doc FROM chunks_fts_vocab WHERE term >= ? AND term < ?", low, hi)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var term string
		var doc int
		if err := rows.Scan(&term, &doc); err == nil && len(term) > 0 {
			vc.terms[term] = doc
			firstChar := term[0]
			vc.byFirstChar[firstChar] = append(vc.byFirstChar[firstChar], term)
			vc.byLen[len(term)] = append(vc.byLen[len(term)], term)
		}
	}
	vc.loadedBuckets[b] = true
	return nil
}

// EnsureLoaded checks if the full cache is loaded. If not, it loads all terms from chunks_fts_vocab.
func (vc *VocabCache) EnsureLoaded(db *sql.DB) error {
	vc.mu.RLock()
	if vc.loaded {
		vc.mu.RUnlock()
		return nil
	}
	vc.mu.RUnlock()

	vc.mu.Lock()
	defer vc.mu.Unlock()
	if vc.loaded {
		return nil
	}

	rows, err := db.Query("SELECT term, doc FROM chunks_fts_vocab")
	if err != nil {
		return fmt.Errorf("query chunks_fts_vocab: %w", err)
	}
	defer rows.Close()

	newTerms := make(map[string]int)
	newByFirst := make(map[byte][]string)
	newByLen := make(map[int][]string)

	for rows.Next() {
		var term string
		var doc int
		if err := rows.Scan(&term, &doc); err == nil && len(term) > 0 {
			newTerms[term] = doc
			firstChar := term[0]
			newByFirst[firstChar] = append(newByFirst[firstChar], term)
			newByLen[len(term)] = append(newByLen[len(term)], term)
		}
	}

	vc.terms = newTerms
	vc.byFirstChar = newByFirst
	vc.byLen = newByLen
	vc.loaded = true
	return nil
}

// HasTerm reports whether term exists in the vocabulary.
func (vc *VocabCache) HasTerm(db *sql.DB, term string) bool {
	term = strings.ToLower(term)
	if len(term) == 0 {
		return false
	}

	vc.mu.RLock()
	if vc.loaded || vc.loadedBuckets[term[0]] {
		_, ok := vc.terms[term]
		vc.mu.RUnlock()
		return ok
	}
	if _, ok := vc.terms[term]; ok {
		vc.mu.RUnlock()
		return true
	}
	vc.mu.RUnlock()

	// Fast single index seek in chunks_fts_vocab
	var dummy int
	err := db.QueryRow("SELECT 1 FROM chunks_fts_vocab WHERE term = ? LIMIT 1", term).Scan(&dummy)
	if err == nil {
		vc.mu.Lock()
		vc.terms[term] = 1
		vc.mu.Unlock()
		return true
	}
	return false
}

// TermCount returns the total number of unique terms in the vocabulary.
func (vc *VocabCache) TermCount(db *sql.DB) int {
	if err := vc.EnsureLoaded(db); err != nil {
		return 0
	}
	vc.mu.RLock()
	defer vc.mu.RUnlock()
	return len(vc.terms)
}

// prefixUpperBound returns the smallest string strictly greater than any string with the given prefix.
func prefixUpperBound(prefix string) string {
	b := []byte(prefix)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] < 0xFF {
			b[i]++
			return string(b[:i+1])
		}
	}
	return prefix + "\xFF"
}

// LookupPrefix finds terms in the vocabulary that start with prefix, sorted by document count descending.
func (vc *VocabCache) LookupPrefix(db *sql.DB, prefix string, limit int) []string {
	if len(prefix) < 2 {
		return nil
	}
	if limit <= 0 {
		limit = 8
	}

	prefix = strings.ToLower(prefix)
	first := prefix[0]

	// If cache is loaded or bucket is loaded, scan in RAM
	vc.mu.RLock()
	if vc.loaded || vc.loadedBuckets[first] {
		defer vc.mu.RUnlock()
		cands := vc.byFirstChar[first]
		type pHit struct {
			term string
			doc  int
		}
		var matched []pHit
		for _, c := range cands {
			if strings.HasPrefix(c, prefix) {
				matched = append(matched, pHit{term: c, doc: vc.terms[c]})
			}
		}
		sort.Slice(matched, func(i, j int) bool {
			if matched[i].doc != matched[j].doc {
				return matched[i].doc > matched[j].doc
			}
			return len(matched[i].term) < len(matched[j].term)
		})
		res := make([]string, 0, limit)
		for i := 0; i < len(matched) && i < limit; i++ {
			res = append(res, matched[i].term)
		}
		return res
	}
	vc.mu.RUnlock()

	// Direct SQLite range scan (fast index scan on chunks_fts_vocab)
	hi := prefixUpperBound(prefix)
	rows, err := db.Query("SELECT term, doc FROM chunks_fts_vocab WHERE term >= ? AND term < ? ORDER BY doc DESC, length(term) ASC LIMIT ?", prefix, hi, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var results []string
	for rows.Next() {
		var term string
		var doc int
		if err := rows.Scan(&term, &doc); err == nil {
			results = append(results, term)
		}
	}
	return results
}

// FindTypoCandidates searches the vocabulary for terms close to token within Damerau-Levenshtein distance.
// Tokens of length 4-7 use max distance 1 (first character must match).
// Tokens of length >= 8 use max distance 2 (first character may differ for distance 2).
func (vc *VocabCache) FindTypoCandidates(db *sql.DB, token string, limit int) []string {
	token = strings.ToLower(CleanQueryToken(token))
	tLen := len(token)
	if tLen < 4 {
		return nil
	}
	if limit <= 0 {
		limit = 8
	}

	firstChar := token[0]

	// Ensure bucket for firstChar is loaded
	vc.mu.Lock()
	if !vc.loaded && !vc.loadedBuckets[firstChar] {
		_ = vc.ensureBucketLoadedLocked(db, firstChar)
	}
	vc.mu.Unlock()

	vc.mu.RLock()
	defer vc.mu.RUnlock()

	maxDist := 1
	if tLen >= 8 {
		maxDist = 2
	}

	var candidates []typoCandidate
	hasDist1 := false

	// Same first character pool
	pool := vc.byFirstChar[firstChar]
	for _, cand := range pool {
		cLen := len(cand)
		if cLen < tLen-maxDist || cLen > tLen+maxDist {
			continue
		}
		d := DamerauLevenshtein(token, cand, maxDist)
		if d <= maxDist {
			if d <= 1 {
				hasDist1 = true
			}
			candidates = append(candidates, typoCandidate{
				term:     cand,
				distance: d,
				docCount: vc.terms[cand],
			})
		}
	}

	// For length >= 8 (maxDist == 2): first character may differ ONLY for distance 2 cases.
	// Pruning: if we already have a distance-1 candidate or >= limit candidates, differing first character
	// is skipped because same-first-character candidates strictly outrank differing distance-2.
	if maxDist == 2 && !hasDist1 && len(candidates) < limit {
		for l := tLen - 2; l <= tLen+2; l++ {
			for _, cand := range vc.byLen[l] {
				if len(cand) < 2 || cand[0] == firstChar {
					continue
				}
				// Candidate must share at least one character in the prefix
				if cand[0] != token[1] && cand[1] != token[0] && cand[1] != token[1] {
					continue
				}
				d := DamerauLevenshtein(token, cand, 2)
				if d == 2 {
					candidates = append(candidates, typoCandidate{
						term:     cand,
						distance: d,
						docCount: vc.terms[cand],
					})
				}
			}
		}
	}

	// Sort by:
	// 1. Edit distance ascending (dist 1 before dist 2)
	// 2. Document count descending (popularity/frequency)
	// 3. Length closeness to original token
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].distance != candidates[j].distance {
			return candidates[i].distance < candidates[j].distance
		}
		if candidates[i].docCount != candidates[j].docCount {
			return candidates[i].docCount > candidates[j].docCount
		}
		diffI := len(candidates[i].term) - tLen
		if diffI < 0 {
			diffI = -diffI
		}
		diffJ := len(candidates[j].term) - tLen
		if diffJ < 0 {
			diffJ = -diffJ
		}
		return diffI < diffJ
	})

	results := make([]string, 0, limit)
	for i := 0; i < len(candidates) && i < limit; i++ {
		results = append(results, candidates[i].term)
	}
	return results
}
