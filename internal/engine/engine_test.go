package engine

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"memory/internal/config"
	"memory/internal/llm"
	"memory/internal/search"
	"memory/internal/store"
)

var testEpoch = time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type fakeEmbedder struct {
	mu    sync.Mutex
	fail  bool
	calls int
}

func (f *fakeEmbedder) setFail(fail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = fail
}

func (f *fakeEmbedder) Embed(ctx context.Context, kind llm.Kind, texts []string) ([][]float32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return nil, errors.New("fake embedder failure")
	}
	f.calls += len(texts)
	out := make([][]float32, len(texts))
	for i, text := range texts {
		vec := make([]float32, search.ModelDim)
		h := sha256.Sum256([]byte(text))
		for d := range vec {
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
		out[i] = vec
	}
	return out, nil
}

type fakeReranker struct{ fail bool }

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

type testEngine struct {
	*Engine
	dir   string
	cfg   config.Config
	embed *fakeEmbedder
	clock *fakeClock
}

func newTestEngine(t *testing.T, opts ...Option) *testEngine {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Config{
		DataDir:               dir,
		ModelsDir:             filepath.Join(dir, "models"),
		Port:                  7777,
		DefaultTempExpiryDays: 7,
	}
	embed := &fakeEmbedder{}
	clock := &fakeClock{now: testEpoch}
	base := []Option{
		WithEmbedder(embed),
		WithClock(clock.Now),
		WithLogger(log.New(io.Discard, "", 0)),
	}
	e, err := Open(cfg, append(base, opts...)...)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return &testEngine{Engine: e, dir: dir, cfg: cfg, embed: embed, clock: clock}
}

func (te *testEngine) search(t *testing.T, r SearchRequest) search.Result {
	t.Helper()
	res, err := te.Search(context.Background(), r)
	if err != nil {
		t.Fatalf("Search(%+v): %v", r, err)
	}
	return res
}

func hasHit(res search.Result, docID string) bool {
	for _, h := range res.Hits {
		if h.DocID == docID {
			return true
		}
	}
	return false
}

func hasHitWithSource(res search.Result, docID string, source string) bool {
	for _, h := range res.Hits {
		if h.DocID == docID {
			for _, s := range h.Sources {
				if s == source {
					return true
				}
			}
		}
	}
	return false
}

func TestSearchFindsWriteImmediately(t *testing.T) {
	te := newTestEngine(t)
	if _, err := te.Store.CreateContext("work", nil); err != nil {
		t.Fatalf("CreateContext: %v", err)
	}
	m, err := te.Store.Upsert(store.UpsertInput{
		Context: "work",
		Name:    "auth/token.md",
		Body:    "# Token refresh\n\nThe zephyrmarker procedure refreshes tokens.",
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if warnings := te.Warnings(); len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}

	res := te.search(t, SearchRequest{Query: "zephyrmarker", Context: "work", Limit: 5})
	if !hasHit(res, m.ID) {
		t.Fatalf("search did not find freshly written memory: %+v", res.Hits)
	}
}

func TestUpdateChangesResults(t *testing.T) {
	te := newTestEngine(t)
	if _, err := te.Store.CreateContext("work", nil); err != nil {
		t.Fatalf("CreateContext: %v", err)
	}
	m, err := te.Store.Upsert(store.UpsertInput{Context: "work", Name: "note.md", Body: "alphamarker"})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if _, err := te.Store.Upsert(store.UpsertInput{Context: "work", ID: m.ID, Body: "quokkamarker"}); err != nil {
		t.Fatalf("update: %v", err)
	}

	res := te.search(t, SearchRequest{Query: "quokkamarker", Context: "work", Limit: 5})
	if !hasHit(res, m.ID) {
		t.Fatalf("updated content not searchable: %+v", res.Hits)
	}
	state, err := te.Index.State()
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if st := state[m.ID]; st.Version != 2 {
		t.Fatalf("indexed version = %d, want 2", st.Version)
	}
}

func TestDeleteRemovesFromSearch(t *testing.T) {
	te := newTestEngine(t)
	if _, err := te.Store.CreateContext("work", nil); err != nil {
		t.Fatalf("CreateContext: %v", err)
	}
	m, err := te.Store.Upsert(store.UpsertInput{Context: "work", Name: "gone.md", Body: "evanescentmarker"})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := te.Store.Delete(m.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	res := te.search(t, SearchRequest{Query: "evanescentmarker", Context: "work", Limit: 5})
	if hasHit(res, m.ID) {
		t.Fatalf("deleted memory still searchable: %+v", res.Hits)
	}
	state, _ := te.Index.State()
	if _, ok := state[m.ID]; ok {
		t.Fatal("deleted memory still indexed")
	}
}

func TestDeleteContextRemovesFromSearch(t *testing.T) {
	te := newTestEngine(t)
	if _, err := te.Store.CreateContext("scratch", nil); err != nil {
		t.Fatalf("CreateContext: %v", err)
	}
	m, err := te.Store.Upsert(store.UpsertInput{Context: "scratch", Name: "note.md", Body: "contextmarker"})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := te.Store.DeleteContext("scratch"); err != nil {
		t.Fatalf("DeleteContext: %v", err)
	}
	state, _ := te.Index.State()
	if _, ok := state[m.ID]; ok {
		t.Fatal("context delete left its document indexed")
	}
}

func TestExpiredContextIsNotSearchable(t *testing.T) {
	te := newTestEngine(t)
	one := 1
	c, err := te.Store.CreateContext("", &one)
	if err != nil {
		t.Fatalf("CreateContext: %v", err)
	}
	m, err := te.Store.Upsert(store.UpsertInput{Context: c.Slug, Name: "temp.md", Body: "ephemeralmarker"})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	te.clock.Advance(48 * time.Hour)

	_, err = te.Search(context.Background(), SearchRequest{Query: "ephemeralmarker", Context: c.Slug})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Search on expired context err = %v, want ErrNotFound", err)
	}
	state, _ := te.Index.State()
	if _, ok := state[m.ID]; ok {
		t.Fatal("expired context document left in the index")
	}
}

func TestGlobalAlwaysSearched(t *testing.T) {
	te := newTestEngine(t)
	if _, err := te.Store.CreateContext("work", nil); err != nil {
		t.Fatalf("CreateContext: %v", err)
	}
	global, err := te.Store.Upsert(store.UpsertInput{Context: store.GlobalContext, Name: "globalnote.md", Body: "globalmarker"})
	if err != nil {
		t.Fatalf("Upsert global: %v", err)
	}
	if _, err := te.Store.Upsert(store.UpsertInput{Context: "work", Name: "worknote.md", Body: "workmarker"}); err != nil {
		t.Fatalf("Upsert work: %v", err)
	}

	res := te.search(t, SearchRequest{Query: "globalmarker", Context: "work"})
	if !hasHit(res, global.ID) {
		t.Fatalf("global memory missing from scoped search: %+v", res.Hits)
	}

	res = te.search(t, SearchRequest{Query: "globalmarker", Context: "work", NoGlobal: true})
	if hasHit(res, global.ID) {
		t.Fatalf("NoGlobal search returned a global memory: %+v", res.Hits)
	}

	if _, err := te.Search(context.Background(), SearchRequest{Query: "x", NoGlobal: true}); err == nil {
		t.Fatal("NoGlobal without a context must error")
	}
}

func TestSearchContextValidation(t *testing.T) {
	te := newTestEngine(t)

	if _, err := te.Search(context.Background(), SearchRequest{Query: "  "}); !errors.Is(err, ErrEmptyQuery) {
		t.Fatalf("empty query err = %v, want ErrEmptyQuery", err)
	}
	if _, err := te.Search(context.Background(), SearchRequest{Query: "x", Context: "nope"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown context err = %v, want ErrNotFound", err)
	}
}

func TestHandEditedFileIsPickedUp(t *testing.T) {
	te := newTestEngine(t)
	if _, err := te.Store.CreateContext("work", nil); err != nil {
		t.Fatalf("CreateContext: %v", err)
	}
	path := filepath.Join(te.dir, "contexts", "work", "manual.md")
	if err := os.WriteFile(path, []byte("# Manual\n\nhandeditedmarker content\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := te.Store.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if _, err := te.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	m, err := te.Store.Get("work", "manual.md")
	if err != nil {
		t.Fatalf("Get adopted file: %v", err)
	}
	res := te.search(t, SearchRequest{Query: "handeditedmarker", Context: "work"})
	if !hasHit(res, m.ID) {
		t.Fatalf("hand-edited file not searchable: %+v", res.Hits)
	}
}

// TestIndexFailureKeepsStoreAndSyncRepairs covers the degraded path: an
// embedder failure no longer loses the document. The keyword rows commit with
// pending vectors, keyword search still finds it, and Sync fills the vector once
// the embedder is back.
func TestIndexFailureKeepsStoreAndSyncRepairs(t *testing.T) {
	te := newTestEngine(t)
	if _, err := te.Store.CreateContext("work", nil); err != nil {
		t.Fatalf("CreateContext: %v", err)
	}

	te.embed.setFail(true)
	m, err := te.Store.Upsert(store.UpsertInput{Context: "work", Name: "later.md", Body: "repairmarker"})
	if err != nil {
		t.Fatalf("store write must still succeed: %v", err)
	}
	state, _ := te.Index.State()
	if _, ok := state[m.ID]; !ok {
		t.Fatal("a failed embed must still commit the keyword rows")
	}
	if pending := te.Index.PendingVectors(); pending == 0 {
		t.Fatal("expected pending vectors after the failed embed")
	}
	// Keyword search already finds it while the vector is missing.
	if res := te.search(t, SearchRequest{Query: "repairmarker", Context: "work"}); !hasHit(res, m.ID) {
		t.Fatalf("keyword search missed the degraded write: %+v", res.Hits)
	}

	te.embed.setFail(false)
	rep, err := te.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if rep.Filled == 0 {
		t.Fatalf("Sync filled no pending vectors: %+v", rep)
	}
	if got := te.Index.PendingVectors(); got != 0 {
		t.Fatalf("PendingVectors = %d after Sync, want 0", got)
	}
	res := te.search(t, SearchRequest{Query: "repairmarker", Context: "work"})
	if !hasHit(res, m.ID) {
		t.Fatalf("repaired document not searchable: %+v", res.Hits)
	}
}

func TestOpenDoesNotStartModelsWhenIndexIsCurrent(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{DataDir: dir, ModelsDir: filepath.Join(dir, "models"), Port: 7777, DefaultTempExpiryDays: 7}
	embed := &fakeEmbedder{}
	logger := log.New(io.Discard, "", 0)

	first, err := Open(cfg, WithEmbedder(embed), WithLogger(logger))
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if _, err := first.Store.CreateContext("work", nil); err != nil {
		t.Fatalf("CreateContext: %v", err)
	}
	if _, err := first.Store.Upsert(store.UpsertInput{Context: "work", Name: "note.md", Body: "persistentmarker"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// A sidecar pointing at a binary that does not exist: if Open touched it,
	// starting it would fail and leave a warning.
	second, err := Open(cfg, WithSidecarBinary("mem-no-such-llama-server"), WithLogger(logger))
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer second.Close()
	if warnings := second.Warnings(); len(warnings) != 0 {
		t.Fatalf("opening an up-to-date index touched the model: %v", warnings)
	}

	res, err := second.Search(context.Background(), SearchRequest{Query: "persistentmarker", Context: "work"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Degraded) == 0 {
		t.Fatal("expected the vector leg to report a degraded model on first use")
	}
	if len(res.Hits) == 0 {
		t.Fatal("expected BM25 hits while the model is unavailable")
	}
}

func TestNeedsReindexRebuildsFromStore(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{DataDir: dir, ModelsDir: filepath.Join(dir, "models"), Port: 7777, DefaultTempExpiryDays: 7}
	embed := &fakeEmbedder{}
	logger := log.New(io.Discard, "", 0)

	first, err := Open(cfg, WithEmbedder(embed), WithLogger(logger))
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if _, err := first.Store.CreateContext("work", nil); err != nil {
		t.Fatalf("CreateContext: %v", err)
	}
	if _, err := first.Store.Upsert(store.UpsertInput{Context: "work", Name: "note.md", Body: "rebuildmarker"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db, err := sql.Open("sqlite3", filepath.Join(dir, SearchDBFile))
	if err != nil {
		t.Fatalf("open search db: %v", err)
	}
	if _, err := db.Exec(`UPDATE meta SET value = 'old-model' WHERE key = 'model_id'`); err != nil {
		t.Fatalf("mutate meta: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close search db: %v", err)
	}

	second, err := Open(cfg, WithEmbedder(embed), WithLogger(logger))
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer second.Close()
	rebuilt := false
	for _, w := range second.Warnings() {
		if strings.Contains(w, "needs a rebuild") {
			rebuilt = true
		}
	}
	if !rebuilt {
		t.Fatalf("expected a rebuild warning, got %v", second.Warnings())
	}
	res, err := second.Search(context.Background(), SearchRequest{Query: "rebuildmarker", Context: "work"})
	if err != nil {
		t.Fatalf("Search after rebuild: %v", err)
	}
	if len(res.Hits) == 0 {
		t.Fatal("rebuilt index has no hits")
	}
}

func TestSyncRepairsIndex(t *testing.T) {
	te := newTestEngine(t)
	if _, err := te.Store.CreateContext("work", nil); err != nil {
		t.Fatalf("CreateContext: %v", err)
	}
	m, err := te.Store.Upsert(store.UpsertInput{Context: "work", Name: "note.md", Body: "syncrepairmarker"})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	ctx := context.Background()

	if err := te.Index.Delete(ctx, m.ID); err != nil {
		t.Fatalf("Index.Delete: %v", err)
	}
	rep, err := te.Sync(ctx)
	if err != nil {
		t.Fatalf("Sync after delete: %v", err)
	}
	if rep.Added != 1 {
		t.Fatalf("Added = %d, want 1: %+v", rep.Added, rep)
	}

	if err := te.Index.Upsert(ctx, search.Doc{ID: m.ID, Context: "work", Name: "note.md", Body: "stale body", Version: 999}); err != nil {
		t.Fatalf("Index.Upsert stale: %v", err)
	}
	rep, err = te.Sync(ctx)
	if err != nil {
		t.Fatalf("Sync after stale: %v", err)
	}
	if rep.Updated != 1 {
		t.Fatalf("Updated = %d, want 1: %+v", rep.Updated, rep)
	}

	if err := te.Index.Upsert(ctx, search.Doc{ID: "orphan-doc", Context: "work", Name: "orphan.md", Body: "orphan", Version: 1}); err != nil {
		t.Fatalf("Index.Upsert orphan: %v", err)
	}
	rep, err = te.Sync(ctx)
	if err != nil {
		t.Fatalf("Sync after orphan: %v", err)
	}
	if rep.Removed != 1 {
		t.Fatalf("Removed = %d, want 1: %+v", rep.Removed, rep)
	}
	state, _ := te.Index.State()
	if _, ok := state["orphan-doc"]; ok {
		t.Fatal("orphan survived Sync")
	}
}

func TestSyncProgress(t *testing.T) {
	var (
		mu   sync.Mutex
		seen []SyncProgress
	)
	te := newTestEngine(t, WithSyncProgress(func(p SyncProgress) {
		mu.Lock()
		seen = append(seen, p)
		mu.Unlock()
	}))
	if _, err := te.Store.CreateContext("work", nil); err != nil {
		t.Fatalf("CreateContext: %v", err)
	}
	if _, err := te.Store.Upsert(store.UpsertInput{Context: "work", Name: "p.md", Body: "progressmarker"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	mu.Lock()
	base := len(seen)
	mu.Unlock()
	rep, err := te.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if rep.Total == 0 || len(seen)-base != rep.Total {
		t.Fatalf("progress calls = %d, want total %d", len(seen)-base, rep.Total)
	}
	if last := seen[len(seen)-1]; last.Done != rep.Total {
		t.Fatalf("final progress Done = %d, want %d", last.Done, rep.Total)
	}
}

func TestTwoEnginesSameDataDir(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{DataDir: dir, ModelsDir: filepath.Join(dir, "models"), Port: 7777, DefaultTempExpiryDays: 7}
	logger := log.New(io.Discard, "", 0)
	open := func() *Engine {
		e, err := Open(cfg, WithEmbedder(&fakeEmbedder{}), WithLogger(logger))
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		t.Cleanup(func() { _ = e.Close() })
		return e
	}
	a, b := open(), open()
	if _, err := a.Store.CreateContext("shared", nil); err != nil {
		t.Fatalf("CreateContext: %v", err)
	}

	const perEngine = 10
	var wg sync.WaitGroup
	errs := make(chan error, perEngine*2)
	for _, pair := range []struct {
		e     *Engine
		label string
	}{{a, "a"}, {b, "b"}} {
		wg.Add(1)
		go func(e *Engine, label string) {
			defer wg.Done()
			for i := 0; i < perEngine; i++ {
				if _, err := e.Store.Upsert(store.UpsertInput{
					Context: "shared",
					Name:    label + "-" + strconv.Itoa(i) + ".md",
					Body:    label + " document number " + strconv.Itoa(i),
				}); err != nil {
					errs <- err
					return
				}
			}
		}(pair.e, pair.label)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent write: %v", err)
	}

	list, err := a.Store.List("shared")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != perEngine*2 {
		t.Fatalf("memories = %d, want %d", len(list), perEngine*2)
	}

	if _, err := a.Sync(context.Background()); err != nil {
		t.Fatalf("Sync a: %v", err)
	}
	if _, err := b.Sync(context.Background()); err != nil {
		t.Fatalf("Sync b: %v", err)
	}
	state, err := a.Index.State()
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	for _, m := range list {
		if _, ok := state[m.ID]; !ok {
			t.Fatalf("memory %s missing from index after Sync", m.Name)
		}
	}
}

func TestConfigSidecarPathWinsOverDefaultBinary(t *testing.T) {
	dir := t.TempDir()
	custom := filepath.Join(dir, "custom-bin", "llama-server-custom")
	cfg := config.Config{
		DataDir:               filepath.Join(dir, "data"),
		ModelsDir:             filepath.Join(dir, "models"),
		Port:                  7777,
		DefaultTempExpiryDays: 7,
		SidecarPath:           custom,
	}
	// The initial sync tries to embed the seeded readme and fails, because the
	// binary does not exist. The failure must name the configured path, not the
	// bare "llama-server" from PATH.
	e, err := Open(cfg, WithLogger(log.New(io.Discard, "", 0)))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer e.Close()
	warnings := strings.Join(e.Warnings(), "\n")
	if warnings == "" {
		t.Fatal("expected an indexing warning from the missing binary")
	}
	if !strings.Contains(warnings, "custom-bin") {
		t.Fatalf("config sidecar_path was ignored; warnings:\n%s", warnings)
	}
}

func TestDefaultFile(t *testing.T) {
	te := newTestEngine(t)
	m, err := te.DefaultFile()
	if err != nil {
		t.Fatalf("DefaultFile: %v", err)
	}
	if m.Context != store.GlobalContext || m.Name != store.DefaultFileName || m.Body == "" {
		t.Fatalf("unexpected default file: %+v", m)
	}
}

func TestRerankIsOptIn(t *testing.T) {
	te := newTestEngine(t)
	if te.reranker != nil {
		t.Fatal("reranking must be off by default")
	}

	dir := t.TempDir()
	cfg := config.Config{
		DataDir:     dir,
		ModelsDir:   filepath.Join(dir, "models"),
		Rerank:      true,
		RerankModel: "jina-reranker-v1-tiny-en-q8_0.gguf",
	}
	e, err := Open(cfg, WithEmbedder(&fakeEmbedder{}), WithLogger(log.New(io.Discard, "", 0)))
	if err != nil {
		t.Fatalf("Open with rerank: %v", err)
	}
	defer e.Close()
	if e.reranker == nil {
		t.Fatal("rerank enabled with a model must build a reranker")
	}
}

func TestOpenWorksWithoutModels(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{DataDir: dir, ModelsDir: filepath.Join(dir, "models"), Port: 7777, DefaultTempExpiryDays: 7}
	logger := log.New(io.Discard, "", 0)
	e, err := Open(cfg, WithSidecarBinary("mem-no-such-llama-server"), WithLogger(logger))
	if err != nil {
		t.Fatalf("Open must not require a model: %v", err)
	}
	defer e.Close()
	if len(e.Warnings()) == 0 {
		t.Fatal("expected the initial indexing to report a warning")
	}

	if _, err := e.Store.CreateContext("work", nil); err != nil {
		t.Fatalf("CreateContext: %v", err)
	}
	m, err := e.Store.Upsert(store.UpsertInput{Context: "work", Name: "note.md", Body: "nomodelmarker"})
	if err != nil {
		t.Fatalf("store write must succeed without a model: %v", err)
	}
	res, err := e.Search(context.Background(), SearchRequest{Query: "nomodelmarker", Context: "work"})
	if err != nil {
		t.Fatalf("Search must degrade instead of failing: %v", err)
	}
	if len(res.Degraded) == 0 {
		t.Fatal("expected a degraded vector leg while the model is unavailable")
	}
	if _, err := e.Store.Get("work", m.ID); err != nil {
		t.Fatalf("store lost a write when indexing failed: %v", err)
	}
	state, err := e.Index.State()
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if _, ok := state[m.ID]; !ok {
		t.Fatal("the keyword rows must be committed even with no model")
	}
	if pending := e.Index.PendingVectors(); pending == 0 {
		t.Fatal("expected the vector to be pending while the model is unavailable")
	}
}

func TestSingleWasmBuildSupportsVec0(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{DataDir: dir, ModelsDir: filepath.Join(dir, "models"), Port: 7777, DefaultTempExpiryDays: 7}

	s, err := store.Open(cfg)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()

	ix, err := search.OpenIndex(filepath.Join(dir, SearchDBFile), &fakeEmbedder{}, nil)
	if err != nil {
		t.Fatalf("search.OpenIndex: %v", err)
	}
	defer ix.Close()

	db, err := sql.Open("sqlite3", filepath.Join(dir, "vec0_probe.db"))
	if err != nil {
		t.Fatalf("open probe db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE VIRTUAL TABLE probe_vec USING vec0(context text partition key, embedding int8[384])`); err != nil {
		t.Fatalf("vec0 is unavailable in a process that links both store and search: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO probe_vec(rowid, context, embedding) VALUES (1, 'global', vec_int8(?))`,
		search.Quantize(make([]float32, search.ModelDim))); err != nil {
		t.Fatalf("insert into vec0: %v", err)
	}
	var rowid int64
	if err := db.QueryRow(`SELECT rowid FROM probe_vec WHERE embedding MATCH vec_int8(?) AND k = 1 AND context IN ('global')`,
		search.Quantize(make([]float32, search.ModelDim))).Scan(&rowid); err != nil {
		t.Fatalf("knn query on vec0: %v", err)
	}
	if rowid != 1 {
		t.Fatalf("knn returned rowid %d, want 1", rowid)
	}
}

func TestE2EWithRealModels(t *testing.T) {
	if os.Getenv("MEM_E2E") != "1" {
		t.Skip("set MEM_E2E=1 with MEM_MODELS_DIR and MEM_LLAMA_BIN to run")
	}
	modelsDir := os.Getenv("MEM_MODELS_DIR")
	if modelsDir == "" {
		t.Fatal("MEM_MODELS_DIR must point at the directory holding the GGUF models")
	}
	binary := os.Getenv("MEM_LLAMA_BIN")
	if binary == "" {
		binary = DefaultLlamaBinary
	}
	dir := t.TempDir()
	cfg := config.Config{DataDir: dir, ModelsDir: modelsDir, Port: 7777, DefaultTempExpiryDays: 7}
	e, err := Open(cfg, WithSidecarBinary(binary))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer e.Close()
	if _, err := e.Store.CreateContext("e2e", nil); err != nil {
		t.Fatalf("CreateContext: %v", err)
	}
	m, err := e.Store.Upsert(store.UpsertInput{
		Context: "e2e",
		Name:    "deploy.md",
		Body:    "# Deployment\n\nThe staging cluster reads credentials from the vault sidecar.",
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	res, err := e.Search(context.Background(), SearchRequest{Query: "where do staging credentials come from", Context: "e2e"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !hasHit(res, m.ID) {
		t.Fatalf("real-model search did not find the memory: degraded=%v hits=%+v", res.Degraded, res.Hits)
	}
}

func TestEngineModelStatus(t *testing.T) {
	te := newTestEngine(t)
	initialCalls := te.embed.calls

	st := te.ModelsStatus()
	if te.embed.calls != initialCalls {
		t.Fatalf("ModelsStatus called Embed: calls went from %d to %d", initialCalls, te.embed.calls)
	}
	if st.Embed.State != "ready" {
		t.Fatalf("Embed.State = %q, want ready", st.Embed.State)
	}
	if st.Rerank.State != "disabled" || st.Rerank.Enabled {
		t.Fatalf("Rerank unexpected: %+v", st.Rerank)
	}
}

func TestEngine_FuzzySearch(t *testing.T) {
	te := newTestEngine(t)
	_, _ = te.Store.CreateContext("work", nil)
	m, err := te.Store.Upsert(store.UpsertInput{
		Context: "work",
		Name:    "auth-refresh.md",
		Body:    "Tokens refresh every fifteen minutes automatically.",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Default (Fuzzy == nil): fuzzy search is on by default -> prefix "refre" finds memory via fuzzy leg
	res := te.search(t, SearchRequest{Query: "refre", Context: "work"})
	if !hasHitWithSource(res, m.ID, "fuzzy") {
		t.Fatalf("default fuzzy search did not find memory with fuzzy source for prefix 'refre': %+v", res.Hits)
	}

	// 2. Explicit Fuzzy == false -> prefix "refre" must NOT have fuzzy or name source
	noFuzzy := false
	resOff := te.search(t, SearchRequest{Query: "refre", Context: "work", Fuzzy: &noFuzzy})
	if hasHitWithSource(resOff, m.ID, "fuzzy") || hasHitWithSource(resOff, m.ID, "name") {
		t.Fatalf("search with Fuzzy=false produced fuzzy/name source for partial word 'refre': %+v", resOff.Hits)
	}

	// 3. Engine with WithFuzzyDefault(false) -> default becomes off
	teOff := newTestEngine(t, WithFuzzyDefault(false))
	_, _ = teOff.Store.CreateContext("work", nil)
	m2, err := teOff.Store.Upsert(store.UpsertInput{
		Context: "work",
		Name:    "auth-refresh.md",
		Body:    "Tokens refresh every fifteen minutes automatically.",
	})
	if err != nil {
		t.Fatal(err)
	}
	resEngineOff := teOff.search(t, SearchRequest{Query: "refre", Context: "work"})
	if hasHitWithSource(resEngineOff, m2.ID, "fuzzy") || hasHitWithSource(resEngineOff, m2.ID, "name") {
		t.Fatalf("engine with WithFuzzyDefault(false) produced fuzzy/name source: %+v", resEngineOff.Hits)
	}

	// Force on with Fuzzy: true on engine that has default off
	forceOn := true
	resForceOn := teOff.search(t, SearchRequest{Query: "refre", Context: "work", Fuzzy: &forceOn})
	if !hasHitWithSource(resForceOn, m2.ID, "fuzzy") {
		t.Fatalf("engine with Fuzzy=true override did not find memory with fuzzy source: %+v", resForceOn.Hits)
	}
}
