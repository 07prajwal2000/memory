package bm25

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// DefaultStopwords contains the exact stopword list from internal/search/schema.go.
var DefaultStopwords = map[string]bool{
	"a": true, "about": true, "above": true, "after": true, "again": true, "against": true,
	"all": true, "am": true, "an": true, "and": true, "any": true, "are": true, "aren't": true,
	"as": true, "at": true, "be": true, "because": true, "been": true, "before": true,
	"being": true, "below": true, "between": true, "both": true, "but": true, "by": true,
	"can": true, "can't": true, "cannot": true, "could": true, "couldn't": true, "did": true,
	"didn't": true, "do": true, "does": true, "doesn't": true, "doing": true, "don't": true,
	"down": true, "during": true, "each": true, "few": true, "for": true, "from": true,
	"further": true, "had": true, "hadn't": true, "has": true, "hasn't": true, "have": true,
	"haven't": true, "having": true, "he": true, "her": true, "here": true, "hers": true,
	"herself": true, "him": true, "himself": true, "his": true, "how": true, "i": true,
	"if": true, "in": true, "into": true, "is": true, "isn't": true, "it": true, "it's": true,
	"its": true, "itself": true, "let's": true, "me": true, "more": true, "most": true,
	"mustn't": true, "my": true, "myself": true, "no": true, "nor": true, "not": true,
	"of": true, "off": true, "on": true, "once": true, "only": true, "or": true, "other": true,
	"ought": true, "our": true, "ours": true, "ourselves": true, "out": true, "over": true,
	"own": true, "same": true, "shan't": true, "she": true, "should": true, "shouldn't": true,
	"so": true, "some": true, "such": true, "than": true, "that": true, "the": true, "their": true,
	"theirs": true, "them": true, "themselves": true, "then": true, "there": true, "these": true,
	"they": true, "this": true, "those": true, "through": true, "to": true, "too": true,
	"under": true, "until": true, "up": true, "very": true, "was": true, "wasn't": true,
	"we": true, "were": true, "weren't": true, "what": true, "when": true, "where": true,
	"which": true, "while": true, "who": true, "whom": true, "why": true, "with": true,
	"won't": true, "would": true, "wouldn't": true, "you": true, "your": true, "yours": true,
	"yourself": true, "yourselves": true,
}

// IsIdentifier matches identifiers (flags, paths, snake_case, camelCase, versions, acronyms).
func IsIdentifier(s string) bool {
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

// Tokenize splits text into terms following SanitizeFTS5 rules.
func Tokenize(text string) []string {
	fields := strings.Fields(text)
	var terms []string

	for _, raw := range fields {
		trimmed := strings.TrimFunc(raw, func(r rune) bool {
			return r == '(' || r == ')' || r == '[' || r == ']' || r == '{' || r == '}' ||
				r == '"' || r == '\'' || r == ',' || r == ';' || r == ':' || r == '?' || r == '!'
		})
		if trimmed == "" {
			continue
		}

		if IsIdentifier(trimmed) {
			clean := strings.ToLower(trimmed)
			clean = strings.ReplaceAll(clean, `"`, "")
			clean = strings.ReplaceAll(clean, `*`, "")
			if clean != "" {
				terms = append(terms, clean)
			}
			continue
		}

		clean := strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return unicode.ToLower(r)
			}
			return -1
		}, trimmed)

		if len(clean) == 0 {
			continue
		}

		if DefaultStopwords[clean] {
			continue
		}
		if len(clean) < 3 {
			continue
		}

		terms = append(terms, clean)
	}

	// Fallback if all terms were stripped
	if len(terms) == 0 {
		for _, raw := range fields {
			clean := strings.ToLower(strings.Map(func(r rune) rune {
				if unicode.IsLetter(r) || unicode.IsDigit(r) {
					return r
				}
				return -1
			}, raw))
			if clean != "" {
				terms = append(terms, clean)
			}
		}
	}

	return terms
}

// Document represents an indexed document for BM25.
type Document struct {
	ID    string
	Text  string
	DocID string // underlying doc ID if this is a chunk
}

// Hit represents a BM25 search result.
type Hit struct {
	ID    string
	DocID string
	Score float64
	Rank  int // 1-based rank
}

// Index represents an in-memory BM25 index with k1=1.2, b=0.75.
type Index struct {
	k1        float64
	b         float64
	numDocs   int
	totalLen  int
	avgDocLen float64

	docIDs     []string
	docMeta    []Document
	docLengths []int

	// term -> docIndex -> termFrequency
	postings map[string]map[int]int
	// term -> document frequency (number of docs containing term)
	df map[string]int
}

// NewIndex creates a new BM25 index with k1=1.2, b=0.75.
func NewIndex(k1, b float64) *Index {
	if k1 <= 0 {
		k1 = 1.2
	}
	if b <= 0 {
		b = 0.75
	}
	return &Index{
		k1:       k1,
		b:        b,
		postings: make(map[string]map[int]int),
		df:       make(map[string]int),
	}
}

// AddDocument adds a document to the index.
func (idx *Index) AddDocument(doc Document) {
	docIdx := len(idx.docIDs)
	idx.docIDs = append(idx.docIDs, doc.ID)
	idx.docMeta = append(idx.docMeta, doc)

	terms := Tokenize(doc.Text)
	docLen := len(terms)
	idx.docLengths = append(idx.docLengths, docLen)
	idx.totalLen += docLen
	idx.numDocs++
	idx.avgDocLen = float64(idx.totalLen) / float64(idx.numDocs)

	tfMap := make(map[string]int)
	for _, t := range terms {
		tfMap[t]++
	}

	for t, count := range tfMap {
		p, exists := idx.postings[t]
		if !exists {
			p = make(map[int]int)
			idx.postings[t] = p
		}
		p[docIdx] = count
		idx.df[t]++
	}
}

// Search searches the BM25 index and returns top-k hits.
func (idx *Index) Search(query string, topK int) []Hit {
	if topK <= 0 {
		topK = 50
	}
	qTerms := Tokenize(query)
	if len(qTerms) == 0 {
		return nil
	}

	scores := make(map[int]float64)
	N := float64(idx.numDocs)

	for _, term := range qTerms {
		df := idx.df[term]
		if df == 0 {
			continue
		}

		// Robertson-Spärck Jones IDF with smoothing
		idf := math.Log(1.0 + (N-float64(df)+0.5)/(float64(df)+0.5))
		if idf < 0 {
			idf = 0
		}

		posting := idx.postings[term]
		for docIdx, tf := range posting {
			docLen := float64(idx.docLengths[docIdx])
			num := float64(tf) * (idx.k1 + 1.0)
			denom := float64(tf) + idx.k1*(1.0-idx.b+idx.b*(docLen/idx.avgDocLen))
			scores[docIdx] += idf * (num / denom)
		}
	}

	if len(scores) == 0 {
		return nil
	}

	type docScore struct {
		docIdx int
		score  float64
	}
	var ranked []docScore
	for docIdx, sc := range scores {
		ranked = append(ranked, docScore{docIdx: docIdx, score: sc})
	}

	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].docIdx < ranked[j].docIdx
	})

	if len(ranked) > topK {
		ranked = ranked[:topK]
	}

	hits := make([]Hit, len(ranked))
	for r, item := range ranked {
		meta := idx.docMeta[item.docIdx]
		hits[r] = Hit{
			ID:    meta.ID,
			DocID: meta.DocID,
			Score: item.score,
			Rank:  r + 1,
		}
	}

	return hits
}
