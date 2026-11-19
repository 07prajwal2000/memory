package search

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"memory/internal/llm"
)

type fakeEmbedder struct {
	mu         sync.Mutex
	embedCount int
	fail       bool
}

func (f *fakeEmbedder) Embed(ctx context.Context, kind llm.Kind, texts []string) ([][]float32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return nil, errors.New("fake embedder failure")
	}
	f.embedCount += len(texts)
	res := make([][]float32, len(texts))
	for i, t := range texts {
		vec := make([]float32, ModelDim)
		h := sha256.Sum256([]byte(t))
		for d := 0; d < ModelDim; d++ {
			vec[d] = float32(int8(h[d%len(h)])) / 128.0
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

type fakeReranker struct {
	fail bool
}

func (f *fakeReranker) Rerank(ctx context.Context, query string, docs []string) ([]float64, error) {
	if f.fail {
		return nil, errors.New("fake reranker failure")
	}
	scores := make([]float64, len(docs))
	for i := range docs {
		scores[i] = float64(len(docs)-i) * 0.1
	}
	return scores, nil
}

func TestIndex_LifecycleAndCRUD(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "search.db")

	fe := &fakeEmbedder{}
	fr := &fakeReranker{}

	ix, err := OpenIndex(dbPath, fe, fr)
	if err != nil {
		t.Fatalf("OpenIndex failed: %v", err)
	}
	defer ix.Close()

	ctx := context.Background()

	// 1. Upsert Doc
	doc1 := Doc{
		ID:      "doc-1",
		Context: "work",
		Name:    "notes.md",
		Body:    "# Notes\n\nThis is a test note about OAuth and JWT authentication.",
		Version: 1,
	}
	if err := ix.Upsert(ctx, doc1); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	// Verify State
	state, err := ix.State()
	if err != nil {
		t.Fatalf("State failed: %v", err)
	}
	if len(state) != 1 || state["doc-1"].Version != 1 || state["doc-1"].Context != "work" {
		t.Errorf("unexpected state: %+v", state)
	}

	// 2. Hash-skip optimization: upserting identical doc should be a no-op (0 new embed calls)
	embedCountBefore := fe.embedCount
	if err := ix.Upsert(ctx, doc1); err != nil {
		t.Fatalf("second Upsert failed: %v", err)
	}
	if fe.embedCount != embedCountBefore {
		t.Errorf("expected 0 new embed calls on identical upsert, got %d", fe.embedCount-embedCountBefore)
	}

	// 3. Vector reuse: modifying doc to add a new chunk but keeping old chunk
	doc1Mod := Doc{
		ID:      "doc-1",
		Context: "work",
		Name:    "notes.md",
		Body:    "# Notes\n\nThis is a test note about OAuth and JWT authentication.\n\n## New Section\n\nBrand new unique content that needs embedding.",
		Version: 2,
	}
	embedCountBefore = fe.embedCount
	if err := ix.Upsert(ctx, doc1Mod); err != nil {
		t.Fatalf("upsert modified doc failed: %v", err)
	}
	// Total chunks = 2, but 1 was reused, so only 1 was embedded!
	newEmbeds := fe.embedCount - embedCountBefore
	if newEmbeds != 1 {
		t.Errorf("expected exactly 1 chunk embedded with 1 reused, got %d", newEmbeds)
	}

	// 4. Search
	res, err := ix.Search(ctx, Query{
		Text:     "OAuth JWT",
		Contexts: []string{"work"},
		Limit:    5,
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(res.Hits) == 0 {
		t.Fatal("expected hits, got 0")
	}
	if res.Hits[0].DocID != "doc-1" {
		t.Errorf("expected hit doc-1, got %s", res.Hits[0].DocID)
	}

	// 5. Delete Doc
	if err := ix.Delete(ctx, "doc-1"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	state, err = ix.State()
	if err != nil {
		t.Fatalf("State after delete failed: %v", err)
	}
	if len(state) != 0 {
		t.Errorf("expected empty state after delete, got %v", state)
	}

	// 6. Delete Context
	doc2 := Doc{ID: "doc-2", Context: "temp-ctx", Name: "temp.md", Body: "temp body", Version: 1}
	_ = ix.Upsert(ctx, doc2)
	if err := ix.DeleteContext(ctx, "temp-ctx"); err != nil {
		t.Fatalf("DeleteContext failed: %v", err)
	}
	state, _ = ix.State()
	if len(state) != 0 {
		t.Errorf("expected empty state after DeleteContext, got %v", state)
	}
}

func TestIndex_ReindexDetection(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "search.db")

	fe := &fakeEmbedder{}

	// Create index
	ix, err := OpenIndex(dbPath, fe, nil)
	if err != nil {
		t.Fatalf("initial OpenIndex failed: %v", err)
	}
	ix.Close()

	// Manually alter meta table to simulate different model_id
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	_, err = db.Exec("UPDATE meta SET value = 'old-model' WHERE key = 'model_id'")
	if err != nil {
		t.Fatalf("update meta: %v", err)
	}
	db.Close()

	// Re-opening without WithRebuild() should return ErrNeedsReindex
	_, err = OpenIndex(dbPath, fe, nil)
	if !errors.Is(err, ErrNeedsReindex) {
		t.Fatalf("expected ErrNeedsReindex, got %v", err)
	}

	// Re-opening WithRebuild() should succeed and reset meta
	ixRebuilt, err := OpenIndex(dbPath, fe, nil, WithRebuild())
	if err != nil {
		t.Fatalf("rebuild OpenIndex failed: %v", err)
	}
	ixRebuilt.Close()
}

func TestIndex_DegradedSearch(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "search.db")

	fe := &fakeEmbedder{}
	fr := &fakeReranker{}

	ix, err := OpenIndex(dbPath, fe, fr)
	if err != nil {
		t.Fatalf("OpenIndex failed: %v", err)
	}
	defer ix.Close()

	ctx := context.Background()
	_ = ix.Upsert(ctx, Doc{
		ID:      "d1",
		Context: "global",
		Name:    "guide.md",
		Body:    "# Guide\n\nDeployment procedures for Kubernetes clusters.",
		Version: 1,
	})

	// 1. Reranker fails -> Degraded note, RRF order preserved
	fr.fail = true
	res, err := ix.Search(ctx, Query{
		Text:     "Kubernetes deployment",
		Contexts: []string{"global"},
	})
	if err != nil {
		t.Fatalf("Search with failed reranker should not fail: %v", err)
	}
	if len(res.Hits) == 0 {
		t.Error("expected hits from RRF fallback")
	}
	if len(res.Degraded) == 0 {
		t.Error("expected Degraded note for reranker failure")
	}

	// 2. Embedder fails -> Degraded note, BM25 fallback
	fr.fail = false
	fe.fail = true
	res, err = ix.Search(ctx, Query{
		Text:     "Kubernetes deployment",
		Contexts: []string{"global"},
	})
	if err != nil {
		t.Fatalf("Search with failed embedder should not fail: %v", err)
	}
	if len(res.Hits) == 0 {
		t.Error("expected hits from BM25 fallback")
	}
	if len(res.Degraded) == 0 {
		t.Error("expected Degraded note for embedder failure")
	}

	// 3. Both fail -> error
	ctxCanceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = ix.Search(ctxCanceled, Query{
		Text:     "Kubernetes deployment",
		Contexts: []string{"global"},
	})
	if err == nil {
		t.Fatal("expected error when both legs fail, got nil")
	}
}

func TestSanitizeFTS5_SecurityAndSyntax(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		contexts []string
		expected string
	}{
		{
			name:     "empty input",
			input:    "",
			contexts: []string{"global"},
			expected: "scope: c" + ScopeToken("global")[1:],
		},
		{
			name:     "punctuation injection",
			input:    `"::*--NOT NEAR/2 OR ()`,
			contexts: []string{"global"},
		},
		{
			name:     "multiple contexts",
			input:    "search terms",
			contexts: []string{"global", "dev"},
		},
		{
			name:     "unicode tokens",
			input:    "测试 搜索 query",
			contexts: []string{"global"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeFTS5(tc.input, tc.contexts)
			// Ensure it doesn't break SQLite FTS5 query parser
			db, err := sql.Open("sqlite3", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			_, err = db.Exec("CREATE VIRTUAL TABLE t USING fts5(scope, heading, text);")
			if err != nil {
				t.Fatal(err)
			}
			if got != "" {
				// Query should never return a syntax error
				_, err = db.Exec("SELECT rowid FROM t WHERE t MATCH ?", got)
				if err != nil {
					t.Fatalf("SanitizeFTS5 generated invalid syntax %q: %v", got, err)
				}
			}
		})
	}
}

func TestCrossProcessCoherence_DataVersion(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "coherence_test.db")

	embedder := &fakeEmbedder{}

	ixA, err := OpenIndex(dbPath, embedder, nil, WithRebuild())
	if err != nil {
		t.Fatalf("OpenIndex A failed: %v", err)
	}
	defer ixA.Close()

	ixB, err := OpenIndex(dbPath, embedder, nil)
	if err != nil {
		t.Fatalf("OpenIndex B failed: %v", err)
	}
	defer ixB.Close()

	var vA1, vB1 int64
	if err := ixA.db.QueryRow("PRAGMA data_version").Scan(&vA1); err != nil {
		t.Fatalf("PRAGMA data_version on A failed: %v", err)
	}
	if err := ixB.db.QueryRow("PRAGMA data_version").Scan(&vB1); err != nil {
		t.Fatalf("PRAGMA data_version on B failed: %v", err)
	}

	t.Logf("Initial data_version: A=%d, B=%d", vA1, vB1)

	// Write via B
	ctx := context.Background()
	err = ixB.Upsert(ctx, Doc{
		ID:      "doc-1",
		Context: "global",
		Name:    "Doc 1",
		Body:    "Alpha beta gamma unique technical identifier CVE-9999-1234 content.",
		Version: 1,
	})
	if err != nil {
		t.Fatalf("Upsert on B failed: %v", err)
	}

	var vA2, vB2 int64
	if err := ixA.db.QueryRow("PRAGMA data_version").Scan(&vA2); err != nil {
		t.Fatalf("PRAGMA data_version on A failed: %v", err)
	}
	if err := ixB.db.QueryRow("PRAGMA data_version").Scan(&vB2); err != nil {
		t.Fatalf("PRAGMA data_version on B failed: %v", err)
	}

	t.Logf("After B write: A data_version=%d, B data_version=%d", vA2, vB2)
	if vA2 == vA1 {
		t.Errorf("Expected A data_version to change after B wrote, but remained %d", vA2)
	}
}

func TestCrossProcessCoherence_Full(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "coherence_full_test.db")

	embedder := &fakeEmbedder{}

	ixA, err := OpenIndex(dbPath, embedder, nil, WithRebuild(), WithResidentVectors(true), WithResidentVectorsSync(true))
	if err != nil {
		t.Fatalf("OpenIndex A failed: %v", err)
	}
	defer ixA.Close()

	ixB, err := OpenIndex(dbPath, embedder, nil, WithResidentVectors(true), WithResidentVectorsSync(true))
	if err != nil {
		t.Fatalf("OpenIndex B failed: %v", err)
	}
	defer ixB.Close()

	ctx := context.Background()

	// 1. Initial search on A -> should be empty
	resA0, err := ixA.Search(ctx, Query{Text: "identifier CVE-9999-1234", Contexts: []string{"global"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resA0.Hits) != 0 {
		t.Fatalf("expected 0 hits on empty DB, got %d", len(resA0.Hits))
	}

	// 2. Write in B
	err = ixB.Upsert(ctx, Doc{
		ID:      "doc-1",
		Context: "global",
		Name:    "Doc 1",
		Body:    "Alpha beta gamma unique technical identifier CVE-9999-1234 content.",
		Version: 1,
	})
	if err != nil {
		t.Fatalf("Upsert on B failed: %v", err)
	}

	// 3. Search in A -> must detect staleness, incrementally refresh, and find doc-1
	resA1, err := ixA.Search(ctx, Query{Text: "identifier CVE-9999-1234", Contexts: []string{"global"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resA1.Hits) == 0 {
		t.Fatalf("expected to find doc-1 in A after B wrote it, got 0 hits")
	}
	if resA1.Hits[0].DocID != "doc-1" {
		t.Errorf("expected doc-1, got %s", resA1.Hits[0].DocID)
	}

	// 4. Delete doc-1 in B
	err = ixB.Delete(ctx, "doc-1")
	if err != nil {
		t.Fatalf("Delete on B failed: %v", err)
	}

	// 5. Search in A -> must detect deletion and expect doc-1 gone
	resA2, err := ixA.Search(ctx, Query{Text: "identifier CVE-9999-1234", Contexts: []string{"global"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resA2.Hits) != 0 {
		t.Fatalf("expected 0 hits in A after B deleted doc-1, got %d", len(resA2.Hits))
	}
}

func TestVecStore_InProcessImmediateUpdates(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "immediate_updates.db")

	embedder := &fakeEmbedder{}

	ix, err := OpenIndex(dbPath, embedder, nil, WithRebuild(), WithResidentVectors(true), WithResidentVectorsSync(true))
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()

	ctx := context.Background()

	// 1. Upsert
	err = ix.Upsert(ctx, Doc{
		ID:      "doc-alpha",
		Context: "tenant-a",
		Name:    "Doc Alpha",
		Body:    "Important secret security vulnerability CVE-2026-0001 patch details.",
		Version: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify immediately present in vecStore
	if ix.vecStore.Count() == 0 {
		t.Fatalf("expected vecStore to have chunks immediately after Upsert")
	}

	res1, err := ix.Search(ctx, Query{Text: "vulnerability CVE-2026-0001", Contexts: []string{"tenant-a"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res1.Hits) == 0 || res1.Hits[0].DocID != "doc-alpha" {
		t.Fatalf("expected doc-alpha to be found immediately")
	}

	// 2. Delete doc
	if err := ix.Delete(ctx, "doc-alpha"); err != nil {
		t.Fatal(err)
	}
	if ix.vecStore.Count() != 0 {
		t.Fatalf("expected vecStore count 0 after delete, got %d", ix.vecStore.Count())
	}
	res2, err := ix.Search(ctx, Query{Text: "vulnerability CVE-2026-0001", Contexts: []string{"tenant-a"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Hits) != 0 {
		t.Fatalf("expected 0 hits after delete, got %d", len(res2.Hits))
	}

	// 3. Upsert into tenant-b and DeleteContext
	err = ix.Upsert(ctx, Doc{
		ID:      "doc-beta",
		Context: "tenant-b",
		Name:    "Doc Beta",
		Body:    "Another document in tenant-b for context deletion test.",
		Version: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ix.vecStore.Count() == 0 {
		t.Fatalf("expected chunks in tenant-b")
	}

	if err := ix.DeleteContext(ctx, "tenant-b"); err != nil {
		t.Fatal(err)
	}
	if ix.vecStore.Count() != 0 {
		t.Fatalf("expected 0 chunks after DeleteContext, got %d", ix.vecStore.Count())
	}
}

func TestResidentVectorCap(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cap_test.db")

	embedder := &fakeEmbedder{}

	// Set cap to 2 chunks
	ix, err := OpenIndex(dbPath, embedder, nil, WithRebuild(), WithResidentVectors(true), WithResidentVectorCap(2), WithResidentVectorsSync(true))
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()

	ctx := context.Background()

	// Insert 4 separate docs to exceed cap of 2
	for i := 1; i <= 4; i++ {
		err = ix.Upsert(ctx, Doc{
			ID:      fmt.Sprintf("doc-cap-%d", i),
			Context: "global",
			Name:    fmt.Sprintf("Doc %d", i),
			Body:    fmt.Sprintf("Content for document %d in resident cap test.", i),
			Version: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// Reopen with cap=2 -> should detect cap exceeded and stay on SQLite path
	ix2, err := OpenIndex(dbPath, embedder, nil, WithResidentVectors(true), WithResidentVectorCap(2), WithResidentVectorsSync(true))
	if err != nil {
		t.Fatal(err)
	}
	defer ix2.Close()

	if !ix2.capExceeded {
		t.Errorf("expected capExceeded to be true")
	}
	if ix2.vecStore != nil {
		t.Errorf("expected vecStore to be nil when cap exceeded")
	}

	res, err := ix2.Search(ctx, Query{Text: "resident cap test", Contexts: []string{"global"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) == 0 {
		t.Fatalf("expected hits via SQLite fallback path")
	}
	foundCapDegraded := false
	for _, d := range res.Degraded {
		if strings.Contains(d, "resident vector cap exceeded") {
			foundCapDegraded = true
		}
	}
	if !foundCapDegraded {
		t.Errorf("expected degraded warning about resident vector cap, got %v", res.Degraded)
	}
}

func TestIndex_UpsertMany(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "upsert_many_test.db")

	embedder := &fakeEmbedder{}

	ix, err := OpenIndex(dbPath, embedder, nil, WithRebuild(), WithResidentVectors(true), WithResidentVectorsSync(true), WithUpsertBatchSize(4), WithUpsertConcurrency(2))
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()

	ctx := context.Background()

	docs := []Doc{
		{ID: "doc-1", Context: "global", Name: "Doc 1", Body: "Kubernetes CFS throttling prevention using GOMAXPROCS.", Version: 1},
		{ID: "doc-2", Context: "global", Name: "Doc 2", Body: "Database maintenance executing VACUUM ANALYZE regularly.", Version: 1},
		{ID: "doc-3", Context: "tenant-x", Name: "Doc 3", Body: "Security vulnerability patch for CVE-2024-1029.", Version: 1},
	}

	if err := ix.UpsertMany(ctx, docs); err != nil {
		t.Fatalf("UpsertMany failed: %v", err)
	}

	res1, err := ix.Search(ctx, Query{Text: "GOMAXPROCS Kubernetes", Contexts: []string{"global"}})
	if err != nil || len(res1.Hits) == 0 || res1.Hits[0].DocID != "doc-1" {
		t.Fatalf("expected doc-1 to match, err=%v, hits=%+v", err, res1.Hits)
	}

	res2, err := ix.Search(ctx, Query{Text: "CVE-2024-1029 vulnerability", Contexts: []string{"tenant-x"}})
	if err != nil || len(res2.Hits) == 0 || res2.Hits[0].DocID != "doc-3" {
		t.Fatalf("expected doc-3 to match, err=%v, hits=%+v", err, res2.Hits)
	}

	// Update version of doc-1
	updatedDocs := []Doc{
		{ID: "doc-1", Context: "global", Name: "Doc 1", Body: "Updated Kubernetes CFS throttling prevention with automated container quotas and GOMAXPROCS setting.", Version: 2},
	}
	if err := ix.UpsertMany(ctx, updatedDocs); err != nil {
		t.Fatalf("UpsertMany update failed: %v", err)
	}

	res3, err := ix.Search(ctx, Query{Text: "GOMAXPROCS container quotas", Contexts: []string{"global"}})
	if err != nil || len(res3.Hits) == 0 || res3.Hits[0].DocID != "doc-1" {
		t.Fatalf("expected updated doc-1 to match, err=%v, hits=%+v", err, res3.Hits)
	}
}

func TestIndex_OfflineEmbedder_KeywordHitAndRecovery(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "offline_embedder_test.db")

	embedder := &fakeEmbedder{fail: true} // Start offline

	ix, err := OpenIndex(dbPath, embedder, nil, WithRebuild(), WithResidentVectors(true), WithResidentVectorsSync(true))
	if err != nil {
		t.Fatalf("OpenIndex failed: %v", err)
	}
	defer ix.Close()

	ctx := context.Background()

	// 1. Upsert document while embedder is failing/offline
	doc := Doc{
		ID:      "mem-token-refresh",
		Context: "global",
		Name:    "Auth Token Doc",
		Body:    "Tokens refresh every 15 minutes automatically in the background.",
		Version: 1,
	}

	err = ix.Upsert(ctx, doc)
	if !errors.Is(err, ErrVectorsPending) {
		t.Fatalf("expected ErrVectorsPending when embedder is unavailable, got: %v", err)
	}
	var pve *PendingVectorsError
	if !errors.As(err, &pve) || pve.Count != 1 {
		t.Fatalf("expected *PendingVectorsError with Count=1, got: %v", err)
	}

	// Verify pending counts
	if ix.PendingVectors() != 1 {
		t.Fatalf("expected PendingVectors() == 1, got %d", ix.PendingVectors())
	}
	if ix.PendingCount() != 1 || ix.Pending() != 1 {
		t.Fatalf("expected PendingCount() == 1, got %d", ix.PendingCount())
	}
	if ix.ContextPendingVectors("global") != 1 {
		t.Fatalf("expected ContextPendingVectors('global') == 1, got %d", ix.ContextPendingVectors("global"))
	}

	state, err := ix.State()
	if err != nil {
		t.Fatalf("State() error: %v", err)
	}
	ds, ok := state["mem-token-refresh"]
	if !ok || ds.PendingVectors != 1 || ds.Pending != 1 {
		t.Fatalf("expected doc state to have PendingVectors=1, got %+v", ds)
	}

	// 2. Keyword search must find the document immediately!
	res, err := ix.Search(ctx, Query{Text: "token refresh", Contexts: []string{"global"}})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(res.Hits) == 0 {
		t.Fatalf("expected keyword search to find hit while embedder offline, got 0 hits")
	}
	if res.Hits[0].DocID != "mem-token-refresh" {
		t.Fatalf("expected hit mem-token-refresh, got %s", res.Hits[0].DocID)
	}
	// Verify degraded warning includes embedder notice
	hasDegradedEmbed := false
	for _, d := range res.Degraded {
		if strings.Contains(d, "embedder unavailable") {
			hasDegradedEmbed = true
		}
	}
	if !hasDegradedEmbed {
		t.Errorf("expected degraded warning about embedder unavailable, got %v", res.Degraded)
	}

	// 3. Embedder recovers!
	embedder.mu.Lock()
	embedder.fail = false
	embedder.mu.Unlock()

	// 4. FillPendingVectors processes pending chunks
	nFilled, err := ix.FillPendingVectors(ctx)
	if err != nil {
		t.Fatalf("FillPendingVectors failed: %v", err)
	}
	if nFilled != 1 {
		t.Fatalf("expected FillPendingVectors to fill 1 chunk, filled %d", nFilled)
	}

	if ix.PendingVectors() != 0 {
		t.Fatalf("expected PendingVectors() == 0 after fill, got %d", ix.PendingVectors())
	}

	stateAfter, err := ix.State()
	if err != nil {
		t.Fatalf("State() error: %v", err)
	}
	if stateAfter["mem-token-refresh"].PendingVectors != 0 {
		t.Fatalf("expected DocState.PendingVectors == 0 after fill, got %d", stateAfter["mem-token-refresh"].PendingVectors)
	}

	// Verify no duplicate chunks in chunks table
	var chunkCount int
	err = ix.db.QueryRowContext(ctx, "SELECT count(*) FROM chunks WHERE doc_id = 'mem-token-refresh'").Scan(&chunkCount)
	if err != nil || chunkCount != 1 {
		t.Fatalf("expected exactly 1 chunk row, got %d (err: %v)", chunkCount, err)
	}
	var vecCount int
	err = ix.db.QueryRowContext(ctx, "SELECT count(*) FROM chunks_vec").Scan(&vecCount)
	if err != nil || vecCount != 1 {
		t.Fatalf("expected exactly 1 chunks_vec row, got %d (err: %v)", vecCount, err)
	}

	// 5. Search now succeeds with vector leg healthy!
	resRecovered, err := ix.Search(ctx, Query{Text: "token refresh", Contexts: []string{"global"}})
	if err != nil {
		t.Fatalf("Search recovered failed: %v", err)
	}
	if len(resRecovered.Hits) == 0 || resRecovered.Hits[0].DocID != "mem-token-refresh" {
		t.Fatalf("expected hit after recovery, got %+v", resRecovered.Hits)
	}
	if len(resRecovered.Degraded) != 0 {
		t.Errorf("expected no degraded notices after recovery, got %v", resRecovered.Degraded)
	}

	// 6. Test Upsert re-run when embedder is online: should update without duplicates
	err = ix.Upsert(ctx, doc)
	if err != nil {
		t.Fatalf("re-Upsert failed: %v", err)
	}
	if ix.PendingVectors() != 0 {
		t.Fatalf("expected PendingVectors() == 0 after re-Upsert, got %d", ix.PendingVectors())
	}
	err = ix.db.QueryRowContext(ctx, "SELECT count(*) FROM chunks WHERE doc_id = 'mem-token-refresh'").Scan(&chunkCount)
	if err != nil || chunkCount != 1 {
		t.Fatalf("expected still exactly 1 chunk row, got %d", chunkCount)
	}
}

func TestSchemaMigration_V2toV3(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "migration_v2_v3.db")

	// Open raw SQLite DB and construct Schema Version 2
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
			('schema_version', '2'),
			('model_id', '` + ModelID + `'),
			('dim', '384'),
			('quant_version', '1');

		CREATE TABLE docs (
			id TEXT PRIMARY KEY,
			context TEXT NOT NULL,
			name TEXT NOT NULL,
			version INTEGER NOT NULL DEFAULT 1,
			hash TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
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
			end_line INTEGER
		);

		CREATE TABLE chunk_deletions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chunk_id INTEGER NOT NULL
		);

		CREATE TRIGGER trg_chunks_delete
		AFTER DELETE ON chunks
		BEGIN
			INSERT INTO chunk_deletions(chunk_id) VALUES (OLD.id);
		END;

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
		t.Fatalf("failed to create v2 schema: %v", err)
	}
	db.Close()

	// Open with OpenIndex (without WithRebuild) -> must perform in-place migration to v3
	embedder := &fakeEmbedder{}
	ix, err := OpenIndex(dbPath, embedder, nil, WithResidentVectors(true), WithResidentVectorsSync(true))
	if err != nil {
		t.Fatalf("OpenIndex on v2 db failed: %v", err)
	}
	defer ix.Close()

	// Verify schema_version was bumped to CurrentSchemaVersion in meta table
	var vStr string
	err = ix.db.QueryRow("SELECT value FROM meta WHERE key = 'schema_version'").Scan(&vStr)
	if err != nil || vStr != strconv.Itoa(CurrentSchemaVersion) {
		t.Fatalf("expected schema_version '%d', got %s (err: %v)", CurrentSchemaVersion, vStr, err)
	}

	// Verify vec_pending column works
	ctx := context.Background()
	err = ix.Upsert(ctx, Doc{
		ID:      "migrated-doc",
		Context: "global",
		Name:    "Migrated Doc",
		Body:    "Testing in-place migration from schema version 2 to 3.",
		Version: 1,
	})
	if err != nil {
		t.Fatalf("Upsert on migrated index failed: %v", err)
	}

	res, err := ix.Search(ctx, Query{Text: "in-place migration", Contexts: []string{"global"}})
	if err != nil || len(res.Hits) == 0 || res.Hits[0].DocID != "migrated-doc" {
		t.Fatalf("Search failed on migrated index: %v, hits: %+v", err, res.Hits)
	}
}

func TestChunkDeletions_PruningAndFullReload(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "pruning_reload_test.db")

	embedder := &fakeEmbedder{}

	// Handle A and B with maxRetainedDeletions = 5
	ixA, err := OpenIndex(dbPath, embedder, nil, WithRebuild(), WithResidentVectors(true), WithResidentVectorsSync(true), WithMaxRetainedDeletions(5))
	if err != nil {
		t.Fatal(err)
	}
	defer ixA.Close()

	ixB, err := OpenIndex(dbPath, embedder, nil, WithResidentVectors(true), WithResidentVectorsSync(true), WithMaxRetainedDeletions(5))
	if err != nil {
		t.Fatal(err)
	}
	defer ixB.Close()

	ctx := context.Background()

	// 1. Insert 10 documents via B
	for i := 1; i <= 10; i++ {
		err := ixB.Upsert(ctx, Doc{
			ID:      fmt.Sprintf("doc-del-%d", i),
			Context: "global",
			Name:    fmt.Sprintf("Doc %d", i),
			Body:    fmt.Sprintf("UniqueTokenAlpha%d document content for pruning reload test.", i),
			Version: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// 2. Search in A so A catches up and has all 10 in resident memory
	resA, err := ixA.Search(ctx, Query{Text: "UniqueTokenAlpha1", Contexts: []string{"global"}})
	if err != nil || len(resA.Hits) == 0 {
		t.Fatalf("expected A to find doc-del-1 initially, err: %v, hits: %d", err, len(resA.Hits))
	}
	if ixA.vecStore.Count() != 10 {
		t.Fatalf("expected A vecStore to have 10 items, got %d", ixA.vecStore.Count())
	}

	// 3. Delete 8 documents one by one in B
	// Since WithMaxRetainedDeletions(5) is set, chunk_deletions should be pruned to keep only last 5 rows.
	for i := 1; i <= 8; i++ {
		if err := ixB.Delete(ctx, fmt.Sprintf("doc-del-%d", i)); err != nil {
			t.Fatal(err)
		}
	}

	// Check chunk_deletions table count in DB
	var delCount int
	_ = ixB.db.QueryRowContext(ctx, "SELECT count(*) FROM chunk_deletions").Scan(&delCount)
	if delCount > 5 {
		t.Fatalf("expected chunk_deletions to be pruned to <= 5 rows, got %d", delCount)
	}

	// 4. Now search on A!
	// A's lastSeenDeletionID is behind the pruned deletion records (IDs 1..3 are pruned).
	// A's checkCrossProcessSync must detect ErrPrunedDeletions, fall back to LoadFromDB,
	// and accurately reflect that doc-del-1 through doc-del-8 are gone while doc-del-9 and doc-del-10 remain.
	resDel1, err := ixA.Search(ctx, Query{Text: "UniqueTokenAlpha1", Contexts: []string{"global"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range resDel1.Hits {
		if h.DocID == "doc-del-1" {
			t.Fatalf("expected doc-del-1 to be deleted in A after pruning sync, but found in hits: %+v", resDel1.Hits)
		}
	}

	resDel9, err := ixA.Search(ctx, Query{Text: "UniqueTokenAlpha9", Contexts: []string{"global"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resDel9.Hits) == 0 || resDel9.Hits[0].DocID != "doc-del-9" {
		t.Fatalf("expected doc-del-9 to still be present in A after pruning sync")
	}

	if ixA.vecStore.Count() != 2 {
		t.Fatalf("expected A vecStore to have 2 items remaining after full reload, got %d", ixA.vecStore.Count())
	}
}
