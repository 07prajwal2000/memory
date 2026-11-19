package search

import (
	"context"
	"database/sql"
	"math"
	"sort"
	"strings"
	"unicode"
)

// Tier0Enrichment stores the extractive enrichment metadata for a document.
type Tier0Enrichment struct {
	Title    string   `json:"title"`
	Keywords []string `json:"keywords"`
	Lead     string   `json:"lead"`
}

// ExtractTitle extracts the document title:
// Uses name (without .md) if present; otherwise first heading in body; otherwise empty.
func ExtractTitle(name, body string) string {
	name = strings.TrimSpace(name)
	if name != "" {
		cleaned := strings.TrimSuffix(name, ".md")
		if cleaned != "" {
			return cleaned
		}
	}
	return extractFirstHeading(body)
}

func extractFirstHeading(body string) string {
	lines := strings.Split(body, "\n")
	inCodeFence := false
	fenceMarker := ""

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			marker := trimmed[:3]
			if !inCodeFence {
				inCodeFence = true
				fenceMarker = marker
			} else if strings.HasPrefix(trimmed, fenceMarker) {
				inCodeFence = false
				fenceMarker = ""
			}
			continue
		}
		if inCodeFence {
			continue
		}

		if strings.HasPrefix(trimmed, "#") {
			level := 0
			for level < len(trimmed) && trimmed[level] == '#' {
				level++
			}
			if level <= 6 && level < len(trimmed) && (trimmed[level] == ' ' || trimmed[level] == '\t') {
				heading := strings.TrimSpace(trimmed[level:])
				if heading != "" {
					return heading
				}
			}
		}
	}
	return ""
}

// ExtractLead extracts the first 2 sentences (up to maxChars, default 300) from body text.
// Skips markdown headings and code fences.
func ExtractLead(body string, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 300
	}

	lines := strings.Split(body, "\n")
	var proseLines []string
	inCodeFence := false
	fenceMarker := ""

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			marker := trimmed[:3]
			if !inCodeFence {
				inCodeFence = true
				fenceMarker = marker
			} else if strings.HasPrefix(trimmed, fenceMarker) {
				inCodeFence = false
				fenceMarker = ""
			}
			continue
		}
		if inCodeFence {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if trimmed == "" {
			continue
		}
		proseLines = append(proseLines, trimmed)
	}

	if len(proseLines) == 0 {
		return ""
	}

	fullText := strings.Join(proseLines, " ")
	sentences := splitSentences(fullText, 2)
	lead := strings.TrimSpace(strings.Join(sentences, " "))

	if len(lead) > maxChars {
		// Truncate to maxChars cleanly
		runes := []rune(lead)
		if len(runes) > maxChars {
			lead = string(runes[:maxChars])
			// Trim to last word boundary if possible
			lastSpace := strings.LastIndex(lead, " ")
			if lastSpace > maxChars/2 {
				lead = lead[:lastSpace]
			}
			lead = strings.TrimSpace(lead) + "..."
		}
	}
	return lead
}

func splitSentences(text string, maxSentences int) []string {
	var sentences []string
	runes := []rune(text)
	n := len(runes)
	start := 0

	for i := 0; i < n; i++ {
		r := runes[i]
		if r == '.' || r == '?' || r == '!' {
			// Check if this punctuation ends a sentence:
			// followed by whitespace or end of string, and preceding character is not a digit/period.
			isBoundary := false
			if i+1 == n {
				isBoundary = true
			} else if unicode.IsSpace(runes[i+1]) {
				// Don't split on abbreviations like "e.g." or numbers like "1. 2"
				if i > 0 && !unicode.IsDigit(runes[i-1]) {
					isBoundary = true
				}
			}
			if isBoundary {
				sentence := strings.TrimSpace(string(runes[start : i+1]))
				if len(sentence) > 0 {
					sentences = append(sentences, sentence)
					if len(sentences) >= maxSentences {
						return sentences
					}
				}
				// Skip spaces for next sentence
				start = i + 1
				for start < n && unicode.IsSpace(runes[start]) {
					start++
				}
				i = start - 1
			}
		}
	}

	if start < n && len(sentences) < maxSentences {
		rem := strings.TrimSpace(string(runes[start:]))
		if len(rem) > 0 {
			sentences = append(sentences, rem)
		}
	}

	return sentences
}

// ExtractKeywords extracts top-k (default 8) terms by TF-IDF against document frequencies.
// Skips stopwords; keeps identifiers/CamelCase tokens; deterministic (score desc, term asc).
func ExtractKeywords(body string, df map[string]int, numDocs int, topK int) []string {
	if topK <= 0 {
		topK = 8
	}

	tokens := tokenizeForEnrichment(body)
	if len(tokens) == 0 {
		return nil
	}

	tf := make(map[string]int)
	for _, tok := range tokens {
		tf[tok]++
	}

	type termScore struct {
		term  string
		score float64
	}
	var scored []termScore

	N := float64(numDocs)
	if N <= 0 {
		N = 1.0
	}

	for term, count := range tf {
		docFreq := 1.0
		if df != nil {
			if v, ok := df[term]; ok && v > 0 {
				docFreq = float64(v)
			} else if v, ok := df[strings.ToLower(term)]; ok && v > 0 {
				docFreq = float64(v)
			}
		}

		idf := math.Log(1.0 + (N / docFreq))
		score := float64(count) * idf
		scored = append(scored, termScore{term: term, score: score})
	}

	// Deterministic sort: score desc, term asc
	sort.Slice(scored, func(i, j int) bool {
		if math.Abs(scored[i].score-scored[j].score) > 1e-9 {
			return scored[i].score > scored[j].score
		}
		return scored[i].term < scored[j].term
	})

	var keywords []string
	for i := 0; i < len(scored) && i < topK; i++ {
		keywords = append(keywords, scored[i].term)
	}

	return keywords
}

func tokenizeForEnrichment(text string) []string {
	fields := strings.Fields(text)
	var tokens []string
	seenInDoc := make(map[string]bool)

	for _, raw := range fields {
		trimmed := strings.TrimFunc(raw, func(r rune) bool {
			return r == '(' || r == ')' || r == '[' || r == ']' || r == '{' || r == '}' ||
				r == '"' || r == '\'' || r == '`' || r == ',' || r == ';' || r == ':' || r == '?' || r == '!' || r == '<' || r == '>'
		})
		if trimmed == "" {
			continue
		}

		// Keep identifiers & CamelCase
		if IsIdentifierLike(trimmed) {
			clean := strings.ToLower(trimmed)
			if !seenInDoc[clean] {
				seenInDoc[clean] = true
				tokens = append(tokens, clean)
			}
			continue
		}

		// Normal word cleanup
		clean := strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return unicode.ToLower(r)
			}
			return -1
		}, trimmed)

		if len(clean) < 3 {
			continue
		}
		if defaultStopwords[clean] {
			continue
		}

		tokens = append(tokens, clean)
	}
	return tokens
}

// ExtractEnrichment computes title, keywords (top 8), and lead summary for a document.
func ExtractEnrichment(name, body string, df map[string]int, numDocs int) Tier0Enrichment {
	title := ExtractTitle(name, body)
	lead := ExtractLead(body, 300)
	keywords := ExtractKeywords(body, df, numDocs, 8)

	return Tier0Enrichment{
		Title:    title,
		Keywords: keywords,
		Lead:     lead,
	}
}

// GetOrComputeDocEnrichment retrieves cached enrichment from doc_enrichments or computes and caches it.
func GetOrComputeDocEnrichment(ctx context.Context, tx *sql.Tx, docID, docName, docBody, docHash string, df map[string]int, numDocs int) (Tier0Enrichment, error) {
	var enr Tier0Enrichment
	var keywordsJoined string

	err := tx.QueryRowContext(ctx, `
		SELECT title, keywords, lead
		FROM doc_enrichments
		WHERE doc_id = ? AND hash = ?
	`, docID, docHash).Scan(&enr.Title, &keywordsJoined, &enr.Lead)

	if err == nil {
		if keywordsJoined != "" {
			enr.Keywords = strings.Split(keywordsJoined, "\t")
		}
		return enr, nil
	}

	// Not cached or hash changed -> compute
	enr = ExtractEnrichment(docName, docBody, df, numDocs)
	keywordsJoined = strings.Join(enr.Keywords, "\t")

	_, err = tx.ExecContext(ctx, `
		INSERT OR REPLACE INTO doc_enrichments (doc_id, hash, title, keywords, lead)
		VALUES (?, ?, ?, ?, ?)
	`, docID, docHash, enr.Title, keywordsJoined, enr.Lead)
	if err != nil {
		return enr, err
	}

	return enr, nil
}
