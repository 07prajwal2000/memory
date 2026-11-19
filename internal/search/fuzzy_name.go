package search

import (
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
	"sync"
)

type docNameItem struct {
	docID        string
	context      string
	name         string
	nameTokens   []string
	headings     []string
	tags         []string
	firstChunkID int64
}

// DocNameStore maintains an in-RAM index of document names, headings, and tags
// partitioned per context for ultra-fast title and metadata matching.
type DocNameStore struct {
	mu        sync.RWMutex
	byContext map[string][]docNameItem
	byDocID   map[string]int // docID -> index in byContext slice
}

func NewDocNameStore() *DocNameStore {
	return &DocNameStore{
		byContext: make(map[string][]docNameItem),
		byDocID:   make(map[string]int),
	}
}

// SplitNameTokens splits a file name or path on delimiters '/', '.', '_', '-' and spaces.
func SplitNameTokens(name string) []string {
	f := func(c rune) bool {
		return c == '/' || c == '.' || c == '_' || c == '-' || c == '\\' || c == ' '
	}
	parts := strings.FieldsFunc(strings.ToLower(name), f)
	var tokens []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if len(p) > 0 {
			tokens = append(tokens, p)
		}
	}
	return tokens
}

// LoadFromDB populates the docNameStore from the docs and chunks tables.
func (dns *DocNameStore) LoadFromDB(db *sql.DB) error {
	dns.mu.Lock()
	defer dns.mu.Unlock()

	rows, err := db.Query(`
		SELECT d.id, d.context, d.name, COALESCE(d.tags, '[]'),
		       COALESCE((SELECT c.id FROM chunks c WHERE c.doc_id = d.id ORDER BY c.ord ASC LIMIT 1), 0) as first_chunk_id
		FROM docs d
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	byCtx := make(map[string][]docNameItem)
	for rows.Next() {
		var item docNameItem
		var rawTags string
		if err := rows.Scan(&item.docID, &item.context, &item.name, &rawTags, &item.firstChunkID); err == nil {
			item.nameTokens = SplitNameTokens(item.name)
			if rawTags != "" && rawTags != "[]" {
				var tg []string
				if json.Unmarshal([]byte(rawTags), &tg) == nil {
					item.tags = tg
				}
			}
			byCtx[item.context] = append(byCtx[item.context], item)
		}
	}

	// Also fetch distinct headings per doc
	hRows, err := db.Query("SELECT doc_id, heading FROM chunks WHERE heading != '' GROUP BY doc_id, heading")
	if err == nil {
		defer hRows.Close()
		headingMap := make(map[string][]string)
		for hRows.Next() {
			var docID, h string
			if err := hRows.Scan(&docID, &h); err == nil && len(h) > 0 {
				headingMap[docID] = append(headingMap[docID], strings.ToLower(h))
			}
		}
		for ctxName := range byCtx {
			items := byCtx[ctxName]
			for i := range items {
				items[i].headings = headingMap[items[i].docID]
			}
		}
	}

	dns.byContext = byCtx
	return nil
}

// Upsert adds or updates a document in the docNameStore.
func (dns *DocNameStore) Upsert(docID, context, name string, firstChunkID int64, headings []string, tags []string) {
	dns.mu.Lock()
	defer dns.mu.Unlock()

	items := dns.byContext[context]
	item := docNameItem{
		docID:        docID,
		context:      context,
		name:         name,
		nameTokens:   SplitNameTokens(name),
		headings:     headings,
		tags:         tags,
		firstChunkID: firstChunkID,
	}

	found := false
	for i := range items {
		if items[i].docID == docID {
			items[i] = item
			found = true
			break
		}
	}
	if !found {
		items = append(items, item)
	}
	dns.byContext[context] = items
}

// Delete removes a document by docID.
func (dns *DocNameStore) Delete(docID string) {
	dns.mu.Lock()
	defer dns.mu.Unlock()

	for ctx, items := range dns.byContext {
		newItems := items[:0]
		for _, it := range items {
			if it.docID != docID {
				newItems = append(newItems, it)
			}
		}
		dns.byContext[ctx] = newItems
	}
}

// DeleteContext removes all documents in a context.
func (dns *DocNameStore) DeleteContext(ctxName string) {
	dns.mu.Lock()
	defer dns.mu.Unlock()
	delete(dns.byContext, ctxName)
}

type nameMatchResult struct {
	chunkID      int64
	docID        string
	context      string
	name         string
	score        float64
	matchedTerms []string
}

// Search matches query tokens against in-RAM document names and headings.
func (dns *DocNameStore) Search(query string, contexts []string, limit int) []nameMatchResult {
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		return nil
	}
	qTokens := strings.Fields(query)
	if len(qTokens) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 50
	}

	dns.mu.RLock()
	defer dns.mu.RUnlock()

	var matches []nameMatchResult

	for _, ctx := range contexts {
		items, ok := dns.byContext[ctx]
		if !ok {
			continue
		}

		for _, item := range items {
			if item.firstChunkID <= 0 {
				continue
			}

			matchedTermSet := make(map[string]bool)
			totalDocScore := 0.0

			for _, qTok := range qTokens {
				qTokClean := CleanQueryToken(qTok)
				if len(qTokClean) < 2 {
					continue
				}

				bestTokScore := 0.0
				var bestMatchedTerm string

				// 1. Exact match against name tokens
				for _, nTok := range item.nameTokens {
					if nTok == qTokClean {
						bestTokScore = 1.0
						bestMatchedTerm = nTok
						break
					}
				}

				// 1b. Exact or prefix match against tags
				if bestTokScore < 1.0 {
					for _, tag := range item.tags {
						tagClean := CleanQueryToken(tag)
						if tagClean == qTokClean {
							bestTokScore = 1.0
							bestMatchedTerm = tag
							break
						} else if strings.HasPrefix(tagClean, qTokClean) && bestTokScore < 0.8 {
							bestTokScore = 0.8
							bestMatchedTerm = tag
						}
					}
				}

				// 2. Prefix match against name tokens
				if bestTokScore < 0.8 {
					for _, nTok := range item.nameTokens {
						if strings.HasPrefix(nTok, qTokClean) {
							bestTokScore = 0.8
							bestMatchedTerm = nTok
							break
						}
					}
				}

				// 3. Substring match in full name
				if bestTokScore < 0.6 {
					if strings.Contains(strings.ToLower(item.name), qTokClean) {
						bestTokScore = 0.6
						bestMatchedTerm = qTokClean
					}
				}

				// 4. Heading match
				if bestTokScore < 0.7 {
					for _, h := range item.headings {
						if strings.Contains(h, qTokClean) {
							bestTokScore = 0.7
							bestMatchedTerm = qTokClean
							break
						}
					}
				}

				// 5. Typo tolerance on name tokens (DL distance <= 1, len >= 4)
				if bestTokScore < 0.5 && len(qTokClean) >= 4 && !IsIdentifierLike(qTokClean) {
					for _, nTok := range item.nameTokens {
						if len(nTok) >= 3 && DamerauLevenshtein(qTokClean, nTok, 1) <= 1 {
							bestTokScore = 0.5
							bestMatchedTerm = nTok
							break
						}
					}
				}

				if bestTokScore > 0 {
					totalDocScore += bestTokScore
					if bestMatchedTerm != "" {
						matchedTermSet[bestMatchedTerm] = true
					}
				}
			}

			if totalDocScore > 0 {
				var mTerms []string
				for t := range matchedTermSet {
					mTerms = append(mTerms, t)
				}
				matches = append(matches, nameMatchResult{
					chunkID:      item.firstChunkID,
					docID:        item.docID,
					context:      item.context,
					name:         item.name,
					score:        totalDocScore,
					matchedTerms: mTerms,
				})
			}
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		return matches[i].score > matches[j].score
	})

	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}
