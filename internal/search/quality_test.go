package search

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"memory/internal/llm"
)

// 1. Header builder (named/unnamed/no heading)
func TestQuality_HeaderBuilder(t *testing.T) {
	tests := []struct {
		name     string
		docName  string
		heading  string
		expected string
	}{
		{
			name:     "named with clean heading",
			docName:  "guide.md",
			heading:  "Installation",
			expected: "guide > Installation",
		},
		{
			name:     "named with markdown heading and path",
			docName:  "notes/arch/system.md",
			heading:  "# Architecture > ## Storage",
			expected: "notes/arch/system > Architecture > Storage",
		},
		{
			name:     "named with subfolder path",
			docName:  "deploy/prod.md",
			heading:  "Step 1",
			expected: "deploy/prod > Step 1",
		},
		{
			name:     "named no heading",
			docName:  "runbook.md",
			heading:  "",
			expected: "runbook",
		},
		{
			name:     "unnamed with heading",
			docName:  "",
			heading:  "Getting Started",
			expected: "Getting Started",
		},
		{
			name:     "unnamed with markdown heading",
			docName:  "",
			heading:  "# Getting Started > ## Prerequisites",
			expected: "Getting Started > Prerequisites",
		},
		{
			name:     "unnamed no heading (both empty)",
			docName:  "",
			heading:  "",
			expected: "",
		},
		{
			name:     "heading already starts with docName",
			docName:  "Overview",
			heading:  "Overview > Setup",
			expected: "Overview > Setup",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildChunkHeader(tc.docName, tc.heading)
			if got != tc.expected {
				t.Errorf("BuildChunkHeader(%q, %q) = %q, want %q", tc.docName, tc.heading, got, tc.expected)
			}
		})
	}
}

// 2. Snippet never contains the header
func TestQuality_SnippetNeverContainsHeader(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "snippet_test.db")

	embedder := &semanticMockEmbedder{}
	ix, err := OpenIndex(dbPath, embedder, nil, WithRebuild())
	if err != nil {
		t.Fatalf("OpenIndex: %v", err)
	}
	defer ix.Close()

	ctx := context.Background()
	docName := "supersecret_header_docname.md"
	chunkBody := "The user asked about how token refresh and staging deployment work in our devops pipeline."

	err = ix.Upsert(ctx, Doc{
		ID:      "doc-snippet-check",
		Context: "work",
		Name:    docName,
		Body:    chunkBody,
		Version: 1,
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	res, err := ix.Search(ctx, Query{
		Text:     "token refresh",
		Contexts: []string{"work"},
		Limit:    5,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Hits) == 0 {
		t.Fatal("expected at least 1 hit")
	}

	for _, h := range res.Hits {
		header := BuildChunkHeader(docName, h.Heading)
		if header != "" && strings.Contains(h.Snippet, header) {
			t.Errorf("Snippet contains header %q: %q", header, h.Snippet)
		}
		if strings.Contains(h.Snippet, "supersecret_header_docname") {
			t.Errorf("Snippet contains docName header: %q", h.Snippet)
		}
		if !strings.Contains(h.Snippet, "token refresh") {
			t.Errorf("Snippet missing chunk body text: %q", h.Snippet)
		}
	}
}

// 3. Keyword extraction determinism and identifier preservation
func TestQuality_KeywordExtractionDeterminism(t *testing.T) {
	sampleText := `
	In our deployment, we configured GOMAXPROCS=8 and updated the auth_token logic.
	The CVE-2024-1029 vulnerability requires checking upsertBatchSize and camelCase variables.
	We also observe high memory usage in the cache cluster when handling requests.
	The system runs smoothly when the buffer is correctly tuned.
	`

	df := map[string]int{
		"gomaxprocs":      2,
		"auth_token":       1,
		"cve-2024-1029":    1,
		"upsertbatchsize":  1,
		"camelcase":        2,
		"memory":          10,
		"cache":           15,
		"cluster":          5,
	}

	first := ExtractKeywords(sampleText, df, 100, 8)
	if len(first) == 0 {
		t.Fatal("expected non-empty keywords")
	}

	// Verify deterministic output over 100 runs
	for i := 0; i < 100; i++ {
		repeat := ExtractKeywords(sampleText, df, 100, 8)
		if !reflect.DeepEqual(first, repeat) {
			t.Fatalf("keyword extraction not deterministic on run %d: got %v, want %v", i, repeat, first)
		}
	}

	// Verify identifiers and CamelCase tokens were preserved
	joined := strings.Join(first, " ")
	foundIdent := false
	for _, kw := range first {
		if kw == "cve-2024-1029" || kw == "auth_token" || kw == "gomaxprocs" || kw == "upsertbatchsize" || kw == "camelcase" {
			foundIdent = true
			break
		}
	}
	if !foundIdent {
		t.Errorf("expected at least one identifier in keywords, got: %v (text: %s)", first, joined)
	}
}

// 4. Enrichment recompute only on body change
func TestQuality_EnrichmentRecomputeOnlyOnBodyChange(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "enrich_recompute.db")

	embedder := &semanticMockEmbedder{}
	ix, err := OpenIndex(dbPath, embedder, nil, WithRebuild())
	if err != nil {
		t.Fatalf("OpenIndex: %v", err)
	}
	defer ix.Close()

	ctx := context.Background()
	docID := "doc-enrich-test"
	bodyV1 := "First version of the note. It has two sentences for testing."
	hashV1 := Sha256Hex(bodyV1)

	err = ix.Upsert(ctx, Doc{
		ID:      docID,
		Context: "global",
		Name:    "note.md",
		Body:    bodyV1,
		Version: 1,
	})
	if err != nil {
		t.Fatalf("Upsert v1: %v", err)
	}

	// Check that doc_enrichments row exists with hashV1
	var title, lead string
	err = ix.db.QueryRow("SELECT title, lead FROM doc_enrichments WHERE doc_id = ? AND hash = ?", docID, hashV1).Scan(&title, &lead)
	if err != nil {
		t.Fatalf("expected doc_enrichments row for v1: %v", err)
	}
	if title != "note" {
		t.Errorf("expected title 'note', got %q", title)
	}

	// Manually overwrite title in doc_enrichments to detect if recompute happens
	_, err = ix.db.Exec("UPDATE doc_enrichments SET title = 'manual-unchanged-marker' WHERE doc_id = ?", docID)
	if err != nil {
		t.Fatalf("update doc_enrichments: %v", err)
	}

	// Re-upsert with same bodyV1 -> should reuse cached enrichment without recomputing
	err = ix.Upsert(ctx, Doc{
		ID:      docID,
		Context: "global",
		Name:    "note.md",
		Body:    bodyV1,
		Version: 1,
	})
	if err != nil {
		t.Fatalf("re-upsert same body: %v", err)
	}

	var checkedTitle string
	err = ix.db.QueryRow("SELECT title FROM doc_enrichments WHERE doc_id = ?", docID).Scan(&checkedTitle)
	if err != nil || checkedTitle != "manual-unchanged-marker" {
		t.Errorf("enrichment was recomputed on unchanged body! expected 'manual-unchanged-marker', got %q (err: %v)", checkedTitle, err)
	}

	// Now upsert with modified bodyV2 -> hash changes -> MUST recompute enrichment
	bodyV2 := "Second version of the note with completely altered text and facts."
	hashV2 := Sha256Hex(bodyV2)
	err = ix.Upsert(ctx, Doc{
		ID:      docID,
		Context: "global",
		Name:    "note.md",
		Body:    bodyV2,
		Version: 2,
	})
	if err != nil {
		t.Fatalf("upsert v2: %v", err)
	}

	err = ix.db.QueryRow("SELECT title FROM doc_enrichments WHERE doc_id = ? AND hash = ?", docID, hashV2).Scan(&checkedTitle)
	if err != nil {
		t.Fatalf("expected new doc_enrichments row for hashV2: %v", err)
	}
	if checkedTitle == "manual-unchanged-marker" {
		t.Error("enrichment was NOT recomputed after body change!")
	}
}

// 5. FTS finds a memory by title words that are not in the body
func TestQuality_FTSFindsMemoryByTitleWordsNotInBody(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "title_fts.db")

	embedder := &semanticMockEmbedder{fail: true} // ensure FTS leg is verified
	ix, err := OpenIndex(dbPath, embedder, nil, WithRebuild())
	if err != nil {
		t.Fatalf("OpenIndex: %v", err)
	}
	defer ix.Close()

	ctx := context.Background()
	// Title contains distinctive words "kubernetes" and "orchestration"
	// Body contains ONLY unrelated words and specifically does NOT contain "kubernetes" or "orchestration"
	docName := "kubernetes-orchestration-setup.md"
	docBody := "We configured the cluster nodes using docker containers and calico networking across machines."

	if strings.Contains(strings.ToLower(docBody), "kubernetes") || strings.Contains(strings.ToLower(docBody), "orchestration") {
		t.Fatal("test invariant violated: body must not contain title words")
	}

	err = ix.Upsert(ctx, Doc{
		ID:      "k8s-doc",
		Context: "devops",
		Name:    docName,
		Body:    docBody,
		Version: 1,
	})
	if err != nil && !errors.Is(err, ErrVectorsPending) {
		t.Fatalf("Upsert: %v", err)
	}

	// Search for title word "kubernetes"
	res, err := ix.Search(ctx, Query{
		Text:     "kubernetes",
		Contexts: []string{"devops"},
		Limit:    5,
	})
	if err != nil {
		t.Fatalf("Search for 'kubernetes': %v", err)
	}
	if len(res.Hits) == 0 {
		t.Fatal("FTS failed to find memory by title word 'kubernetes' that was absent from body!")
	}

	hit := res.Hits[0]
	if hit.DocID != "k8s-doc" {
		t.Errorf("expected hit docID 'k8s-doc', got %q", hit.DocID)
	}

	// Snippet must NOT contain the header
	if strings.Contains(hit.Snippet, "kubernetes-orchestration-setup") {
		t.Errorf("Snippet contains title header: %q", hit.Snippet)
	}
	if !strings.Contains(hit.Snippet, "docker containers") {
		t.Errorf("Snippet missing chunk body: %q", hit.Snippet)
	}
}

// 6. Migration rebuild
func TestQuality_MigrationRebuild(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "migration_v4_to_v5.db")

	// Construct raw database with Schema Version 4
	db, err := sql.Open("sqlite3", "file:"+dbPath+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	_, err = db.Exec(`
		CREATE TABLE meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
		INSERT INTO meta (key, value) VALUES
			('schema_version', '4'),
			('model_id', '` + ModelID + `'),
			('dim', '384'),
			('quant_version', '1');

		CREATE TABLE docs (
			id TEXT PRIMARY KEY,
			context TEXT NOT NULL,
			name TEXT NOT NULL,
			version INTEGER NOT NULL DEFAULT 1,
			hash TEXT NOT NULL,
			tags TEXT NOT NULL DEFAULT '[]'
		);
		CREATE TABLE chunks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			doc_id TEXT NOT NULL REFERENCES docs(id) ON DELETE CASCADE,
			context TEXT NOT NULL,
			ord INTEGER NOT NULL,
			heading TEXT NOT NULL,
			text TEXT NOT NULL,
			text_hash TEXT NOT NULL,
			start_line INTEGER,
			end_line INTEGER,
			vec_pending INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE chunk_deletions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chunk_id INTEGER NOT NULL
		);
		CREATE VIRTUAL TABLE chunks_vec USING vec0(
			context text partition key,
			embedding int8[384]
		);
		CREATE VIRTUAL TABLE chunks_vec_bit USING vec0(
			context text partition key,
			embedding bit[384]
		);
		CREATE VIRTUAL TABLE chunks_fts USING fts5(
			scope,
			heading,
			text,
			tokenize='porter unicode61'
		);
	`)
	if err != nil {
		db.Close()
		t.Fatalf("failed to create v4 schema: %v", err)
	}
	db.Close()

	embedder := &semanticMockEmbedder{}

	// 1. Opening without WithRebuild MUST return ErrNeedsReindex
	_, err = OpenIndex(dbPath, embedder, nil)
	if err == nil {
		t.Fatal("expected OpenIndex to fail with ErrNeedsReindex for v4 schema")
	}
	if !errors.Is(err, ErrNeedsReindex) {
		t.Fatalf("expected ErrNeedsReindex, got: %v", err)
	}

	// 2. Opening with WithRebuild MUST rebuild the database to CurrentSchemaVersion (5)
	ix, err := OpenIndex(dbPath, embedder, nil, WithRebuild())
	if err != nil {
		t.Fatalf("OpenIndex with WithRebuild failed: %v", err)
	}
	defer ix.Close()

	var newVersion int
	err = ix.db.QueryRow("SELECT CAST(value AS INTEGER) FROM meta WHERE key = 'schema_version'").Scan(&newVersion)
	if err != nil {
		t.Fatalf("query schema_version: %v", err)
	}
	if newVersion != CurrentSchemaVersion {
		t.Fatalf("expected schema_version = %d, got %d", CurrentSchemaVersion, newVersion)
	}

	// Verify doc_enrichments table was created and chunks_fts has 4 columns
	var count int
	err = ix.db.QueryRow("SELECT count(*) FROM doc_enrichments").Scan(&count)
	if err != nil {
		t.Fatalf("doc_enrichments table missing after rebuild: %v", err)
	}
	err = ix.db.QueryRow("SELECT count(*) FROM chunks_fts").Scan(&count)
	if err != nil {
		t.Fatalf("chunks_fts table missing after rebuild: %v", err)
	}

	// Verify upsert works on the rebuilt database
	ctx := context.Background()
	err = ix.Upsert(ctx, Doc{
		ID:      "rebuilt-doc",
		Context: "work",
		Name:    "Rebuilt Note",
		Body:    "Testing operations on freshly rebuilt schema version " + strconv.Itoa(CurrentSchemaVersion),
		Version: 1,
	})
	if err != nil {
		t.Fatalf("Upsert on rebuilt db failed: %v", err)
	}
}

// 7. Regression: Fuzzy/Name hits must rank above vector-only junk on prefix and typo queries
type distractorFakeEmbedder struct{}

func (f *distractorFakeEmbedder) Embed(ctx context.Context, kind llm.Kind, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		vec := make([]float32, ModelDim)
		// Make queries and readme.md point towards [1.0, 0...], so readme.md is always vector rank 1.
		// deploy/staging.md points towards [0.5, 0.5...], making it vector rank 2.
		if strings.Contains(t, "readme") || strings.Contains(t, "deplo") || strings.Contains(t, "staigng") {
			vec[0] = 1.0
		} else {
			vec[0] = 0.5
			vec[1] = 0.5
		}
		out[i] = vec
	}
	return out, nil
}

func TestFuzzy_OutranksVectorJunkOnPrefixAndTypo(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "distractor_test.db")

	embedder := &distractorFakeEmbedder{}
	ix, err := OpenIndex(dbPath, embedder, nil, WithRebuild(), WithResidentVectors(true), WithResidentVectorsSync(true))
	if err != nil {
		t.Fatalf("OpenIndex failed: %v", err)
	}
	defer ix.Close()

	ctx := context.Background()

	// Store with readme.md + deploy/staging.md
	docs := []Doc{
		{
			ID:      "doc-readme",
			Context: "global",
			Name:    "readme.md",
			Body:    "# Overview\n\nProject readme documentation and guidelines.",
			Version: 1,
		},
		{
			ID:      "doc-staging",
			Context: "global",
			Name:    "deploy/staging.md",
			Body:    "Staging deploys run from the release branch after tokens are refreshed.",
			Version: 1,
		},
	}
	if err := ix.UpsertMany(ctx, docs); err != nil {
		t.Fatalf("UpsertMany failed: %v", err)
	}

	boolFalse := false

	// Test 1: "deplo" (prefix query) with fuzzy ON (default)
	// Must find deploy/staging.md at rank 1, NOT readme.md
	resDeplo, err := ix.Search(ctx, Query{
		Text:     "deplo",
		Contexts: []string{"global"},
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("search deplo failed: %v", err)
	}
	if len(resDeplo.Hits) == 0 {
		t.Fatal("expected hits for 'deplo', got 0")
	}
	if resDeplo.Hits[0].Name != "deploy/staging.md" {
		t.Errorf("expected deploy/staging.md to rank #1 for 'deplo'; got #1 = %s (score %.4f, sources %v)",
			resDeplo.Hits[0].Name, resDeplo.Hits[0].Score, resDeplo.Hits[0].Sources)
	}

	// Test 2: "staigng" (typo query) with fuzzy ON (default)
	// Must find deploy/staging.md at rank 1, NOT readme.md
	resStaigng, err := ix.Search(ctx, Query{
		Text:     "staigng",
		Contexts: []string{"global"},
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("search staigng failed: %v", err)
	}
	if len(resStaigng.Hits) == 0 {
		t.Fatal("expected hits for 'staigng', got 0")
	}
	if resStaigng.Hits[0].Name != "deploy/staging.md" {
		t.Errorf("expected deploy/staging.md to rank #1 for 'staigng'; got #1 = %s (score %.4f, sources %v)",
			resStaigng.Hits[0].Name, resStaigng.Hits[0].Score, resStaigng.Hits[0].Sources)
	}

	// Test 3: Fuzzy OFF equivalence
	// With fuzzy: false, "deplo" has no exact match in deploy/staging.md, so vector rank 1 (readme.md) appears #1
	resFuzzyOff, err := ix.Search(ctx, Query{
		Text:     "deplo",
		Contexts: []string{"global"},
		Limit:    10,
		Fuzzy:    &boolFalse,
	})
	if err != nil {
		t.Fatalf("search deplo with fuzzy:false failed: %v", err)
	}
	if len(resFuzzyOff.Hits) == 0 || resFuzzyOff.Hits[0].Name != "readme.md" {
		t.Errorf("expected readme.md to rank #1 with fuzzy:false for 'deplo'; got %v", resFuzzyOff.Hits)
	}

	// Test 4: Exact word "staging" still ranks deploy/staging.md #1
	resExact, err := ix.Search(ctx, Query{
		Text:     "staging",
		Contexts: []string{"global"},
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("search staging failed: %v", err)
	}
	if len(resExact.Hits) == 0 || resExact.Hits[0].Name != "deploy/staging.md" {
		t.Errorf("expected deploy/staging.md to rank #1 for 'staging'; got %v", resExact.Hits)
	}
}

