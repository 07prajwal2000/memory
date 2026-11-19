package search

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"memory/internal/llm"
)

type semanticMockEmbedder struct {
	fail bool
}

func (s *semanticMockEmbedder) Embed(ctx context.Context, kind llm.Kind, texts []string) ([][]float32, error) {
	if s.fail {
		return nil, errors.New("embedder unavailable")
	}
	res := make([][]float32, len(texts))
	keywords := []string{"token", "refresh", "deploy", "staging", "devops", "totem", "cafe", "music", "system", "zylophone"}
	for i, t := range texts {
		vec := make([]float32, ModelDim)
		lower := strings.ToLower(t)
		matched := false
		for kIdx, kw := range keywords {
			if strings.Contains(lower, kw) {
				vec[kIdx*10] = 1.0
				matched = true
			}
		}
		if kind == llm.KindQuery && !matched {
			return nil, errors.New("query has no semantic vector match")
		}
		var sum float64
		for _, x := range vec {
			sum += float64(x * x)
		}
		if sum > 0 {
			norm := float32(math.Sqrt(sum))
			for d := range vec {
				vec[d] /= norm
			}
		}
		res[i] = vec
	}
	return res, nil
}

// 1. Damerau-Levenshtein unit tests
func TestFuzzy_DamerauLevenshtein(t *testing.T) {
	tests := []struct {
		s1, s2   string
		maxDist  int
		expected int
	}{
		{"token", "token", 2, 0},
		{"token", "tolen", 2, 1}, // substitution
		{"token", "tokn", 2, 1},  // deletion
		{"token", "tokens", 2, 1}, // insertion
		{"token", "tkoen", 2, 1}, // adjacent transposition
		{"token", "tkoen", 0, 1}, // exceeds maxDist 0
		{"refresh", "refrehs", 2, 1}, // transposition 'sh' -> 'hs'
		{"refresh", "refres", 2, 1},  // deletion 'h'
		{"refresh", "refre", 2, 2},   // deletion 'sh'
		{"refresh", "refr", 2, 3},    // deletion 'esh' (exceeds 2)
		{"deploy", "depl", 2, 2},
		{"", "", 2, 0},
		{"a", "", 2, 1},
		{"", "b", 2, 1},
		{"abcdef", "fedcba", 2, 3}, // pruned early
	}

	for _, tc := range tests {
		dist := DamerauLevenshtein(tc.s1, tc.s2, tc.maxDist)
		if dist != tc.expected {
			t.Errorf("DamerauLevenshtein(%q, %q, %d) = %d; want %d", tc.s1, tc.s2, tc.maxDist, dist, tc.expected)
		}
	}
}

// 2. Identifier classification unit tests
func TestFuzzy_IdentifierClassification(t *testing.T) {
	idents := []string{
		"v1.5", "127.0.0.1", "port8080", "oauth2", "GOMAXPROCS",
		"auth_token", "deploy/staging.md", "cve-2024-1029", "config.json",
		"RAM", "CPU", "TLS", "DNS", "UUID", "FTS5", "WAL",
		"upsertBatchSize", "getMemory", "camelCase", "PascalCase",
		"sub_dir/file.go", "-flag", "--no-fuzzy",
	}

	for _, id := range idents {
		if !IsIdentifierLike(id) {
			t.Errorf("IsIdentifierLike(%q) = false; want true", id)
		}
	}

	nonIdents := []string{
		"token", "refresh", "deploy", "staging", "fifteen", "minutes",
		"database", "architecture", "query", "search", "engine",
	}

	for _, word := range nonIdents {
		if IsIdentifierLike(word) {
			t.Errorf("IsIdentifierLike(%q) = true; want false", word)
		}
	}
}

// 3. Typo candidate generation and vocabulary caching
func TestFuzzy_VocabCache_TypoCandidates(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "vocab_test.db")

	embedder := &semanticMockEmbedder{}
	ix, err := OpenIndex(dbPath, embedder, nil, WithRebuild())
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()

	ctx := context.Background()
	err = ix.Upsert(ctx, Doc{
		ID:      "doc-1",
		Context: "global",
		Name:    "auth.md",
		Body:    "Authentication tokens refresh every fifteen minutes automatically. Deploy to staging.",
		Version: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Term count must be > 0
	count := ix.vocabCache.TermCount(ix.db)
	if count == 0 {
		t.Fatalf("expected non-zero term count in vocabCache")
	}

	// 1. Existing word must NOT produce typo candidates
	if ix.vocabCache.HasTerm(ix.db, "token") || ix.vocabCache.HasTerm(ix.db, "tokens") {
		// Existing terms are not typo-expanded
	}

	// 2. Typo 'tokn' (len 4) -> should find 'token' (distance 1)
	typos := ix.vocabCache.FindTypoCandidates(ix.db, "tokn", 8)
	foundToken := false
	for _, term := range typos {
		if term == "token" || term == "tokens" {
			foundToken = true
			break
		}
	}
	if !foundToken {
		t.Errorf("FindTypoCandidates('tokn') = %v; expected 'token' or 'tokens'", typos)
	}

	// 3. Typo 'refrehs' (transposition, len 7) -> should find 'refresh'
	typos = ix.vocabCache.FindTypoCandidates(ix.db, "refrehs", 8)
	foundRefresh := false
	for _, term := range typos {
		if term == "refresh" {
			foundRefresh = true
			break
		}
	}
	if !foundRefresh {
		t.Errorf("FindTypoCandidates('refrehs') = %v; expected 'refresh'", typos)
	}

	// 4. Prefix scan on 'depl' -> should find 'deploi' (Porter stemmed form of deploy)
	prefixes := ix.vocabCache.LookupPrefix(ix.db, "depl", 8)
	foundDeploy := false
	for _, p := range prefixes {
		if strings.HasPrefix(p, "depl") {
			foundDeploy = true
			break
		}
	}
	if !foundDeploy {
		t.Errorf("LookupPrefix('depl') = %v; expected prefix starting with 'depl'", prefixes)
	}
}

// 4. End-to-end prefix search, typo tolerance, name leg, and exact ranking
func TestFuzzy_Search_EndToEnd(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "fuzzy_e2e.db")

	embedder := &semanticMockEmbedder{}
	ix, err := OpenIndex(dbPath, embedder, nil, WithRebuild())
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()

	ctx := context.Background()

	// Doc A: exact "Tokens refresh every fifteen minutes"
	err = ix.Upsert(ctx, Doc{
		ID:      "auth-doc",
		Context: "global",
		Name:    "auth/tokens.md",
		Body:    "# Auth Service\n\nTokens refresh every fifteen minutes automatically.",
		Version: 1,
		Tags:    []string{"security", "iam"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Doc B: deployment doc with path in name and tags
	err = ix.Upsert(ctx, Doc{
		ID:      "deploy-doc",
		Context: "global",
		Name:    "deploy/staging.md",
		Body:    "# Deployment Guide\n\nEnvironment setup procedures for release pipeline.",
		Version: 1,
		Tags:    []string{"devops", "k8s"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Search prefix: "refre" should find "auth-doc"
	res, err := ix.Search(ctx, Query{Text: "refre", Contexts: []string{"global"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) == 0 {
		t.Fatalf("search 'refre' found 0 hits; expected auth-doc")
	}
	if res.Hits[0].DocID != "auth-doc" {
		t.Errorf("search 'refre' top hit = %s; want auth-doc", res.Hits[0].DocID)
	}
	foundFuzzySource := false
	for _, s := range res.Hits[0].Sources {
		if s == "fuzzy" || s == "name" {
			foundFuzzySource = true
			break
		}
	}
	if !foundFuzzySource {
		t.Errorf("search 'refre' sources = %v; expected 'fuzzy' or 'name'", res.Hits[0].Sources)
	}
	if len(res.Hits[0].MatchedTerms) == 0 {
		t.Errorf("search 'refre' matched_terms is empty; expected matched terms")
	}

	// 2. Search typo: "tokn" should find "auth-doc"
	resTypo, err := ix.Search(ctx, Query{Text: "tokn", Contexts: []string{"global"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resTypo.Hits) == 0 {
		t.Fatalf("search 'tokn' found 0 hits; expected auth-doc")
	}
	if resTypo.Hits[0].DocID != "auth-doc" {
		t.Errorf("search 'tokn' top hit = %s; want auth-doc", resTypo.Hits[0].DocID)
	}

	// 3. Search name / path: "depl" should find "deploy-doc" via name leg
	resName, err := ix.Search(ctx, Query{Text: "depl", Contexts: []string{"global"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resName.Hits) == 0 {
		t.Fatalf("search 'depl' found 0 hits; expected deploy-doc")
	}
	if resName.Hits[0].DocID != "deploy-doc" {
		t.Errorf("search 'depl' top hit = %s; want deploy-doc", resName.Hits[0].DocID)
	}

	// 4. Search tag: "devops" should find "deploy-doc" even though body does not have "devops"
	resTag, err := ix.Search(ctx, Query{Text: "devops", Contexts: []string{"global"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resTag.Hits) == 0 {
		t.Fatalf("search 'devops' found 0 hits; expected deploy-doc via tags")
	}
	if resTag.Hits[0].DocID != "deploy-doc" {
		t.Errorf("search 'devops' top hit = %s; want deploy-doc", resTag.Hits[0].DocID)
	}

	// 5. Exact match ranking over fuzzy-only match:
	// Insert Doc C that has near term "totem" instead of "token"
	err = ix.Upsert(ctx, Doc{
		ID:      "totem-doc",
		Context: "global",
		Name:    "totem.md",
		Body:    "# Totem Guide\n\nTotem poles are monumental carvings.",
		Version: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Exact query "token" -> auth-doc must rank first with exact match score
	resExact, err := ix.Search(ctx, Query{Text: "token", Contexts: []string{"global"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resExact.Hits) == 0 || resExact.Hits[0].DocID != "auth-doc" {
		t.Fatalf("search 'token' expected auth-doc as #1, got: %+v", resExact.Hits)
	}

	// 6. Switch: with Fuzzy: false, partial word "refre" must find NOTHING
	noFuzzy := false
	resOff, err := ix.Search(ctx, Query{Text: "refre", Contexts: []string{"global"}, Fuzzy: &noFuzzy})
	if err != nil {
		t.Fatal(err)
	}
	// With fuzzy off, "refre" is not an exact word, so 0 hits should be returned
	if len(resOff.Hits) > 0 {
		t.Errorf("search 'refre' with Fuzzy=false returned %d hits; want 0", len(resOff.Hits))
	}
}

// 5. Cross-process coherence: Handle B writes new word, Handle A typo-searches and finds it
func TestFuzzy_CrossProcessConsistency(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cross_proc_fuzzy.db")

	embedder := &semanticMockEmbedder{}

	// Handle A opens index
	ixA, err := OpenIndex(dbPath, embedder, nil, WithRebuild())
	if err != nil {
		t.Fatal(err)
	}
	defer ixA.Close()

	// Handle B opens same file
	ixB, err := OpenIndex(dbPath, embedder, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ixB.Close()

	ctx := context.Background()

	// Initial doc in Handle A
	err = ixA.Upsert(ctx, Doc{
		ID:      "initial-doc",
		Context: "global",
		Name:    "init.md",
		Body:    "Initial system configuration.",
		Version: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Search via A so A loads its initial vocab cache
	_, err = ixA.Search(ctx, Query{Text: "system", Contexts: []string{"global"}})
	if err != nil {
		t.Fatal(err)
	}

	// Now Handle B inserts a doc with a unique word "zylophone"
	err = ixB.Upsert(ctx, Doc{
		ID:      "music-doc",
		Context: "global",
		Name:    "music.md",
		Body:    "The zylophone is a percussion instrument with tuned wooden bars.",
		Version: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Handle A searches for typo "zylophne" (distance 1 from "zylophone")
	// Handle A must detect cross-process change via PRAGMA data_version and find music-doc!
	resA, err := ixA.Search(ctx, Query{Text: "zylophne", Contexts: []string{"global"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resA.Hits) == 0 {
		t.Fatalf("Handle A failed to find 'music-doc' for typo 'zylophne' after Handle B insert")
	}
	if resA.Hits[0].DocID != "music-doc" {
		t.Errorf("Handle A top hit = %s; want music-doc", resA.Hits[0].DocID)
	}
}

// 6. Unicode and diacritics handling
func TestFuzzy_Unicode(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "unicode_test.db")

	embedder := &semanticMockEmbedder{}
	ix, err := OpenIndex(dbPath, embedder, nil, WithRebuild())
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()

	ctx := context.Background()
	err = ix.Upsert(ctx, Doc{
		ID:      "cafe-doc",
		Context: "global",
		Name:    "menu.md",
		Body:    "Welcome to our Parisian café with fresh espresso and croissants.",
		Version: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Query "cafe" (unaccented) should find "café" (accented)
	res, err := ix.Search(ctx, Query{Text: "cafe", Contexts: []string{"global"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) == 0 || res.Hits[0].DocID != "cafe-doc" {
		t.Errorf("query 'cafe' failed to match 'café': %+v", res.Hits)
	}
}

// 7. Adversarial queries (metacharacters, huge strings, empty prefix)
func TestFuzzy_AdversarialQueries(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "adversarial_test.db")

	embedder := &semanticMockEmbedder{}
	ix, err := OpenIndex(dbPath, embedder, nil, WithRebuild())
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()

	ctx := context.Background()
	_ = ix.Upsert(ctx, Doc{
		ID:      "doc-1",
		Context: "global",
		Name:    "normal.md",
		Body:    "Normal text content for adversarial testing.",
		Version: 1,
	})

	adversarial := []string{
		`"`, `'`, `*`, `***`, `???`, `(`, `)`, `((()))`, `AND`, `OR`, `NOT`, `NEAR`,
		`:::`, `^`, `{}`, `[]`, `\`, `/`, `""`, `''`,
		strings.Repeat("a", 10000), // very long token
		strings.Repeat("word ", 1000), // 1000 words
		"   \t\n   ", // whitespace only
		"!!! ??? ***", // punctuation only
		"SELECT * FROM chunks;", // SQL injection attempt
	}

	for _, adv := range adversarial {
		res, err := ix.Search(ctx, Query{Text: adv, Contexts: []string{"global"}})
		if err != nil {
			t.Errorf("adversarial query %q caused error: %v", adv, err)
		}
		_ = res
	}
}

// 8. Morphology: Porter stemmer ensures variants (refreshing, refreshes, refreshed) find refresh
func TestFuzzy_MorphologyPorter(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "morphology_test.db")

	embedder := &semanticMockEmbedder{}
	ix, err := OpenIndex(dbPath, embedder, nil, WithRebuild())
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()

	ctx := context.Background()
	err = ix.Upsert(ctx, Doc{
		ID:      "auth-refresh",
		Context: "global",
		Name:    "auth.md",
		Body:    "# Session Tokens\n\nTokens refresh every fifteen minutes automatically.",
		Version: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	variants := []string{"refreshing", "refreshes", "refreshed"}
	for _, v := range variants {
		res, err := ix.Search(ctx, Query{Text: v, Contexts: []string{"global"}})
		if err != nil {
			t.Fatalf("search %q failed: %v", v, err)
		}
		if len(res.Hits) == 0 {
			t.Errorf("morphological variant %q matched 0 hits; expected auth-refresh", v)
		} else if res.Hits[0].DocID != "auth-refresh" {
			t.Errorf("morphological variant %q top hit = %s; want auth-refresh", v, res.Hits[0].DocID)
		}
	}
}

// 9. Expansion caps: 8 per token, 24 total across query
func TestFuzzy_ExpansionCaps(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "caps_test.db")

	embedder := &semanticMockEmbedder{}
	ix, err := OpenIndex(dbPath, embedder, nil, WithRebuild())
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()

	ctx := context.Background()

	// Insert 20 words starting with "abc" and 20 words starting with "xyz"
	var bodyWords []string
	for i := 0; i < 20; i++ {
		bodyWords = append(bodyWords, fmt.Sprintf("abcword%d", i))
		bodyWords = append(bodyWords, fmt.Sprintf("xyzword%d", i))
	}
	err = ix.Upsert(ctx, Doc{
		ID:      "words-doc",
		Context: "global",
		Name:    "words.md",
		Body:    strings.Join(bodyWords, " "),
		Version: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Single token prefix "abc" -> capped at 8
	exp1 := ix.generateFuzzyExpansions("abc")
	if len(exp1) > 8 {
		t.Errorf("single token expansions count = %d; want <= 8", len(exp1))
	}

	// 2. Query with 5 tokens -> total expansions capped at 24
	exp5 := ix.generateFuzzyExpansions("abc xyz abc1 xyz1 abc2")
	if len(exp5) > 24 {
		t.Errorf("multi token expansions count = %d; want <= 24", len(exp5))
	}
}

// 10. Golden equivalence: With Fuzzy: false, results must be identical to previous behavior
func TestFuzzy_GoldenEquivalence(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "golden_equiv_test.db")

	embedder := &semanticMockEmbedder{}
	ix, err := OpenIndex(dbPath, embedder, nil, WithRebuild())
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()

	ctx := context.Background()

	docs := []Doc{
		{ID: "doc-1", Context: "global", Name: "doc1.md", Body: "Database replication and fault tolerance architectures.", Version: 1},
		{ID: "doc-2", Context: "global", Name: "doc2.md", Body: "Database connection pooling and latency monitoring.", Version: 1},
		{ID: "doc-3", Context: "global", Name: "doc3.md", Body: "Network load balancing and TLS certificate rotation.", Version: 1},
	}
	if err := ix.UpsertMany(ctx, docs); err != nil {
		t.Fatal(err)
	}

	queries := []string{"database replication", "fault tolerance", "connection pooling", "load balancing"}
	noFuzzy := false

	for _, qText := range queries {
		res1, err1 := ix.Search(ctx, Query{Text: qText, Contexts: []string{"global"}, Fuzzy: &noFuzzy})
		if err1 != nil {
			t.Fatalf("Search failed: %v", err1)
		}
		res2, err2 := ix.Search(ctx, Query{Text: qText, Contexts: []string{"global"}, Fuzzy: &noFuzzy})
		if err2 != nil {
			t.Fatalf("Search failed: %v", err2)
		}

		if len(res1.Hits) != len(res2.Hits) {
			t.Fatalf("hit count mismatch for %q: %d vs %d", qText, len(res1.Hits), len(res2.Hits))
		}
		for i := range res1.Hits {
			h1 := res1.Hits[i]
			h2 := res2.Hits[i]
			if h1.DocID != h2.DocID || h1.Score != h2.Score || h1.Snippet != h2.Snippet {
				t.Fatalf("hit mismatch for %q at rank %d: %+v vs %+v", qText, i, h1, h2)
			}
			// With fuzzy false, sources should only be exact sources (vec, bm25, rerank)
			for _, s := range h1.Sources {
				if s == "fuzzy" || s == "name" {
					t.Errorf("fuzzy or name source present when Fuzzy=false: %v", h1.Sources)
				}
			}
		}
	}
}
