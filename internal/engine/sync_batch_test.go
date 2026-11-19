package engine

import (
	"context"
	"errors"
	"io"
	"log"
	"math"
	"sync"
	"testing"

	"memory/internal/config"
	"memory/internal/llm"
	"memory/internal/search"
	"memory/internal/store"
)

// countingEmbedder records how many texts it embedded and can be switched off
// to simulate a dead sidecar.
type countingEmbedder struct {
	mu      sync.Mutex
	inner   llm.Embedder
	fail    bool
	texts   int
	batches int
}

func (c *countingEmbedder) Embed(ctx context.Context, kind llm.Kind, texts []string) ([][]float32, error) {
	c.mu.Lock()
	if c.fail {
		c.mu.Unlock()
		return nil, errors.New("embedder offline")
	}
	c.texts += len(texts)
	c.batches++
	inner := c.inner
	c.mu.Unlock()
	return inner.Embed(ctx, kind, texts)
}

func (c *countingEmbedder) setFail(fail bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fail = fail
}

func (c *countingEmbedder) stats() (texts, batches int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.texts, c.batches
}

func itoaTest(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// newSyncEngine opens an engine on an empty data dir with the initial sync
// skipped, so each test controls exactly what Sync has to do.
func newSyncEngine(t *testing.T, embed *countingEmbedder, opts ...Option) (*Engine, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Config{
		DataDir:               dir,
		ModelsDir:             dir + "/models",
		Port:                  7777,
		DefaultTempExpiryDays: 7,
	}
	base := []Option{
		WithEmbedder(embed),
		WithLogger(log.New(io.Discard, "", 0)),
		WithoutInitialSync(),
	}
	e, err := Open(cfg, append(base, opts...)...)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { e.Close() })
	return e, dir
}

// writeViaStore writes memories through a separate store handle so no index
// hook fires: the documents stay invisible to the index until Sync runs.
func writeViaStore(t *testing.T, dir, slug string, n int) {
	t.Helper()
	cfg := config.Config{DataDir: dir, ModelsDir: dir + "/models", Port: 7777, DefaultTempExpiryDays: 7}
	s, err := store.Open(cfg)
	if err != nil {
		t.Fatalf("open writer store: %v", err)
	}
	defer s.Close()
	if _, err := s.CreateContext(slug, nil); err != nil {
		t.Fatalf("CreateContext: %v", err)
	}
	for i := 0; i < n; i++ {
		body := "Memory number " + itoaTest(i) + " about topic " + itoaTest(i%5) + " with enough words to make a chunk."
		if _, err := s.Upsert(store.UpsertInput{Context: slug, Name: "m" + itoaTest(i) + ".md", Body: body}); err != nil {
			t.Fatalf("Upsert %d: %v", i, err)
		}
	}
}

func TestSyncBatchesThroughUpsertMany(t *testing.T) {
	embed := &countingEmbedder{inner: deterministicEmbedder{}}
	e, dir := newSyncEngine(t, embed, WithSyncBatchSize(2))
	writeViaStore(t, dir, "batch", 5)

	progressCalls := 0
	e.progress = func(SyncProgress) { progressCalls++ }

	rep, err := e.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	// 5 memories plus the seeded global readme, none indexed via hooks.
	if rep.Added != 6 || rep.Indexed != 6 {
		t.Fatalf("rep = %+v, want 6 added and indexed", rep)
	}
	if progressCalls != rep.Total {
		t.Fatalf("progress calls = %d, want Total = %d", progressCalls, rep.Total)
	}
	if _, batches := embed.stats(); batches < 2 {
		t.Fatalf("embed called %d times, want several batches", batches)
	}

	state, err := e.Index.State()
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if len(state) != 6 {
		t.Fatalf("indexed %d documents, want 6", len(state))
	}
}

func TestSyncSecondPassIsIdempotent(t *testing.T) {
	embed := &countingEmbedder{inner: deterministicEmbedder{}}
	e, dir := newSyncEngine(t, embed)
	writeViaStore(t, dir, "idem", 3)

	if _, err := e.Sync(context.Background()); err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	textsAfterFirst, _ := embed.stats()

	rep, err := e.Sync(context.Background())
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if rep.Indexed != 0 || rep.Added != 0 || rep.Updated != 0 {
		t.Fatalf("second Sync re-indexed: %+v", rep)
	}
	if rep.Total != 4 || rep.Unchanged != 4 {
		t.Fatalf("second Sync = %+v, want 4 unchanged", rep)
	}
	if textsNow, _ := embed.stats(); textsNow != textsAfterFirst {
		t.Fatalf("second Sync embedded %d more texts", textsNow-textsAfterFirst)
	}
}

// TestSyncDegradesWhenEmbedderDiesMidway documents the behavior after 02c3's
// pending-vector support: a failed embed no longer aborts the batch, it commits
// the keyword rows with vectors marked pending. Sync therefore succeeds, and the
// next Sync after recovery fills the pending vectors.
func TestSyncDegradesWhenEmbedderDiesMidway(t *testing.T) {
	embed := &countingEmbedder{inner: deterministicEmbedder{}}
	e, dir := newSyncEngine(t, embed, WithSyncBatchSize(2))
	writeViaStore(t, dir, "resume", 8)

	e.progress = func(p SyncProgress) {
		if p.Phase == "index" {
			embed.setFail(true)
		}
	}
	rep, err := e.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync must degrade instead of failing: %v", err)
	}
	if rep.Indexed == 0 {
		t.Fatalf("nothing was indexed: %+v", rep)
	}
	// Every document got keyword rows, but vectors are pending.
	state, _ := e.Index.State()
	if len(state) != rep.Total {
		t.Fatalf("index holds %d docs, want %d (keyword rows must still be written)", len(state), rep.Total)
	}
	pending := e.Index.PendingVectors()
	if pending == 0 {
		t.Fatal("expected pending vectors after the embedder died")
	}

	// Recovery: the next pass fills the pending vectors without re-adding docs.
	embed.setFail(false)
	e.progress = nil
	rep2, err := e.Sync(context.Background())
	if err != nil {
		t.Fatalf("resume Sync: %v", err)
	}
	if rep2.Added != 0 {
		t.Fatalf("resume re-added %d documents; it should only fill vectors", rep2.Added)
	}
	if rep2.Filled == 0 {
		t.Fatalf("resume filled no pending vectors: %+v", rep2)
	}
	if got := e.Index.PendingVectors(); got != 0 {
		t.Fatalf("PendingVectors = %d after resume, want 0", got)
	}
	if after, _ := embed.stats(); after == 0 {
		t.Fatal("no embeddings were produced at any point")
	}
}

func TestSyncFillsPendingVectors(t *testing.T) {
	embed := &countingEmbedder{inner: deterministicEmbedder{}}
	e, _ := newSyncEngine(t, embed, WithSyncBatchSize(2))

	// Write through the engine while the embedder is down: the store write
	// succeeds and the index keeps the keyword rows with pending vectors.
	if _, err := e.Store.CreateContext("offline", nil); err != nil {
		t.Fatalf("CreateContext: %v", err)
	}
	embed.setFail(true)
	m, err := e.Store.Upsert(store.UpsertInput{Context: "offline", Name: "offline.md", Body: "keyword only content about a circuit breaker"})
	if err != nil {
		t.Fatalf("Upsert while offline: %v", err)
	}
	if _, err := e.Store.Get("offline", m.ID); err != nil {
		t.Fatalf("store lost the offline write: %v", err)
	}
	if pending := e.Index.PendingVectors(); pending == 0 {
		t.Fatalf("offline write left no pending vectors")
	}

	// Keyword search works while the vectors are still pending.
	res, err := e.Search(context.Background(), SearchRequest{Query: "circuit breaker", Context: "offline"})
	if err != nil {
		t.Fatalf("keyword search: %v", err)
	}
	found := false
	for _, h := range res.Hits {
		if h.DocID == m.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("keyword search missed the pending document: %+v", res.Hits)
	}

	// The embedder recovers; Sync fills the pending vectors.
	embed.setFail(false)
	rep, err := e.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync after recovery: %v", err)
	}
	if rep.Filled == 0 {
		t.Fatalf("Sync filled no pending vectors: %+v", rep)
	}
	if got := e.Index.PendingVectors(); got != 0 {
		t.Fatalf("PendingVectors = %d after fill, want 0", got)
	}

	// The document now has a vector hit too.
	res2, err := e.Search(context.Background(), SearchRequest{Query: "circuit breaker", Context: "offline"})
	if err != nil {
		t.Fatalf("search after fill: %v", err)
	}
	hasVec := false
	for _, h := range res2.Hits {
		if h.DocID != m.ID {
			continue
		}
		for _, src := range h.Sources {
			if src == "vec" || src == "rerank" {
				hasVec = true
			}
		}
	}
	if !hasVec {
		t.Fatalf("filled document has no vector source: %+v", res2.Hits)
	}
}

func TestWithResidentVectorsOption(t *testing.T) {
	embed := &countingEmbedder{inner: deterministicEmbedder{}}
	e, dir := newSyncEngine(t, embed, WithResidentVectors(true))
	writeViaStore(t, dir, "res", 3)
	if _, err := e.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	res, err := e.Search(context.Background(), SearchRequest{Query: "Memory number 1", Context: "res"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Hits) == 0 {
		t.Fatal("no hits with resident vectors enabled")
	}
}

// deterministicEmbedder produces stable unit vectors without any I/O.
type deterministicEmbedder struct{}

func (deterministicEmbedder) Embed(ctx context.Context, kind llm.Kind, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, text := range texts {
		vec := make([]float32, search.ModelDim)
		h := search.Sha256Hex(text)
		for d := range vec {
			vec[d] = float32(int8(h[d%len(h)])) / 128.0
		}
		var sum float64
		for _, x := range vec {
			sum += float64(x * x)
		}
		if sum > 0 {
			inv := float32(1 / math.Sqrt(sum))
			for d := range vec {
				vec[d] *= inv
			}
		}
		out[i] = vec
	}
	return out, nil
}
