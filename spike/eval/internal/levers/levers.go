package levers

import (
	"math"
	"regexp"
	"sort"
	"strings"

	"spike/eval/internal/bm25"
	"spike/eval/internal/runner"
)

var sentenceSplitter = regexp.MustCompile(`(?m)[.!?]\s+`)

// ExtractTier0Enrichment computes extractive title, top-8 TF-IDF keywords, and lead 2 sentences.
type Tier0Enrichment struct {
	Title       string
	Keywords    []string
	LeadSummary string
}

// CorpusIDF computes corpus-wide IDF table for keyword extraction.
type CorpusIDF struct {
	numDocs int
	df      map[string]int
}

func BuildCorpusIDF(corpus []runner.CorpusDoc) *CorpusIDF {
	df := make(map[string]int)
	for _, doc := range corpus {
		terms := bm25.Tokenize(doc.Text)
		seen := make(map[string]bool)
		for _, t := range terms {
			if !seen[t] {
				seen[t] = true
				df[t]++
			}
		}
	}
	return &CorpusIDF{
		numDocs: len(corpus),
		df:      df,
	}
}

// ExtractEnrichment extracts title, top-8 keywords, and lead summary.
func (c *CorpusIDF) ExtractEnrichment(doc runner.CorpusDoc) Tier0Enrichment {
	// 1. Title
	title := doc.Title
	if title == "" {
		if doc.Heading != "" {
			title = doc.Heading
		} else if doc.Name != "" {
			title = doc.Name
		}
	}

	// 2. Lead summary (first 2 sentences)
	sentences := sentenceSplitter.Split(doc.Text, 4)
	lead := ""
	if len(sentences) > 0 {
		lead = strings.TrimSpace(sentences[0])
		if len(sentences) > 1 {
			lead += ". " + strings.TrimSpace(sentences[1])
		}
		if !strings.HasSuffix(lead, ".") {
			lead += "."
		}
	}
	if len(lead) > 250 {
		lead = lead[:250] + "..."
	}

	// 3. Keywords via TF-IDF (top 8)
	terms := bm25.Tokenize(doc.Text)
	tf := make(map[string]int)
	for _, t := range terms {
		tf[t]++
	}

	type termScore struct {
		term  string
		score float64
	}
	var scored []termScore
	N := float64(c.numDocs)
	if N <= 0 {
		N = 1
	}

	for term, count := range tf {
		if bm25.DefaultStopwords[term] || len(term) < 3 {
			continue
		}
		dfVal := float64(c.df[term])
		if dfVal == 0 {
			dfVal = 1
		}
		idf := math.Log(1.0 + (N / dfVal))
		tfidf := float64(count) * idf
		scored = append(scored, termScore{term: term, score: tfidf})
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	var keywords []string
	topK := 8
	if topK > len(scored) {
		topK = len(scored)
	}
	for i := 0; i < topK; i++ {
		keywords = append(keywords, scored[i].term)
	}

	return Tier0Enrichment{
		Title:       title,
		Keywords:    keywords,
		LeadSummary: lead,
	}
}

// BuildContextualText creates "<context> / <name> / <heading path>\n\n<chunk>".
func BuildContextualText(doc runner.CorpusDoc) string {
	var parts []string
	if doc.Category != "" {
		parts = append(parts, doc.Category)
	}
	if doc.Name != "" {
		parts = append(parts, doc.Name)
	}
	if doc.Heading != "" {
		parts = append(parts, doc.Heading)
	}
	header := strings.Join(parts, " / ")
	if header != "" {
		return header + "\n\n" + doc.Text
	}
	return doc.Text
}

// BuildTier0EnrichedText appends title, keywords, and summary to text.
func BuildTier0EnrichedText(doc runner.CorpusDoc, enr Tier0Enrichment) string {
	var sb strings.Builder
	if enr.Title != "" {
		sb.WriteString("Title: ")
		sb.WriteString(enr.Title)
		sb.WriteString("\n")
	}
	if len(enr.Keywords) > 0 {
		sb.WriteString("Keywords: ")
		sb.WriteString(strings.Join(enr.Keywords, ", "))
		sb.WriteString("\n")
	}
	if enr.LeadSummary != "" {
		sb.WriteString("Summary: ")
		sb.WriteString(enr.LeadSummary)
		sb.WriteString("\n\n")
	}
	sb.WriteString(doc.Text)
	return sb.String()
}
