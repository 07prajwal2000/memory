// Package engine wires the on-disk store to the hybrid search index. It is the
// only package that imports both store and search.
package engine

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"memory/internal/config"
	"memory/internal/llm"
	"memory/internal/search"
	"memory/internal/store"
)

const (
	// EmbedModelFile is the embedding model expected inside config.ModelsDir.
	EmbedModelFile = "bge-small-en-v1.5-q8_0.gguf"
	// SearchDBFile is the search index file inside config.DataDir.
	SearchDBFile = "search.db"
	// DefaultLlamaBinary is the llama-server executable looked up on PATH.
	DefaultLlamaBinary = "llama-server"
	// DefaultIndexTimeout bounds one synchronous index write made from a
	// store hook. A slow or missing model must not stall a memory write forever.
	DefaultIndexTimeout = 30 * time.Second

	defaultSyncBatchSize = 32

	// defaultSidecarParallel is 1 because four slots measured slower than one on
	// this hardware (103 vs 124 texts/s); see REPORT.md. It stays configurable
	// for machines where extra slots pay off.
	defaultSidecarParallel = 1

	// embedContextPerSlot is the llm embed sidecar's -c value (512). Scaling it
	// with --parallel keeps each slot's context at 512 tokens.
	embedContextPerSlot = 512

	maxWarnings = 32
)

// Engine bundles a Store with its derived search Index.
type Engine struct {
	Store *store.Store
	Index *search.Index

	embedder      llm.Embedder
	reranker      llm.Reranker
	sidecars      []*llm.Sidecar
	logger        *log.Logger
	indexTimeout  time.Duration
	syncBatchSize int
	progress      SyncProgressFunc

	mu       sync.Mutex
	warnings []string

	embedMu     sync.RWMutex
	embedState  string
	embedDetail string

	rerankMu     sync.RWMutex
	rerankState  string
	rerankDetail string
	fuzzyDefault *bool
}

// ModelInfo describes the runtime state of a sidecar or model.
type ModelInfo struct {
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// RerankModelInfo describes the state and opt-in enablement of the reranker.
type RerankModelInfo struct {
	Enabled bool   `json:"enabled"`
	State   string `json:"state"`
	Detail  string `json:"detail,omitempty"`
}

// ModelsStatus contains the status of both embedder and reranker models.
type ModelsStatus struct {
	Embed  ModelInfo       `json:"embed"`
	Rerank RerankModelInfo `json:"rerank"`
}

type trackingEmbedder struct {
	inner llm.Embedder
	eng   *Engine
}

func (t *trackingEmbedder) Embed(ctx context.Context, kind llm.Kind, texts []string) ([][]float32, error) {
	t.eng.embedMu.Lock()
	t.eng.embedState = "starting"
	t.eng.embedMu.Unlock()

	res, err := t.inner.Embed(ctx, kind, texts)

	t.eng.embedMu.Lock()
	defer t.eng.embedMu.Unlock()
	if err != nil {
		t.eng.embedState = "error"
		t.eng.embedDetail = err.Error()
	} else {
		t.eng.embedState = "ready"
		t.eng.embedDetail = ""
	}
	return res, err
}

type trackingReranker struct {
	inner llm.Reranker
	eng   *Engine
}

func (t *trackingReranker) Rerank(ctx context.Context, query string, docs []string) ([]float64, error) {
	t.eng.rerankMu.Lock()
	t.eng.rerankState = "starting"
	t.eng.rerankMu.Unlock()

	res, err := t.inner.Rerank(ctx, query, docs)

	t.eng.rerankMu.Lock()
	defer t.eng.rerankMu.Unlock()
	if err != nil {
		t.eng.rerankState = "error"
		t.eng.rerankDetail = err.Error()
	} else {
		t.eng.rerankState = "ready"
		t.eng.rerankDetail = ""
	}
	return res, err
}

// ModelsStatus returns the current model states without starting any process.
func (e *Engine) ModelsStatus() ModelsStatus {
	e.embedMu.RLock()
	emState, emDetail := e.embedState, e.embedDetail
	e.embedMu.RUnlock()

	e.rerankMu.RLock()
	rrState, rrDetail := e.rerankState, e.rerankDetail
	e.rerankMu.RUnlock()

	if emState == "" {
		emState = "stopped"
	}
	if rrState == "" {
		if e.reranker == nil {
			rrState = "disabled"
		} else {
			rrState = "stopped"
		}
	}

	return ModelsStatus{
		Embed: ModelInfo{
			State:  emState,
			Detail: emDetail,
		},
		Rerank: RerankModelInfo{
			Enabled: rrState != "disabled",
			State:   rrState,
			Detail:  rrDetail,
		},
	}
}

type options struct {
	embedder     llm.Embedder
	reranker     llm.Reranker
	embedModel   string
	rerankModel  string
	binary       string
	idleTimeout  time.Duration
	clock        func() time.Time
	logger       *log.Logger
	progress     SyncProgressFunc
	indexTimeout time.Duration
	skipSync     bool
	// residentVectors keeps quantized vectors in RAM. Long-lived daemons turn
	// it on; short-lived CLI commands keep the default off because populating
	// the store costs 284 ms at 10k chunks (search REPORT-02c3 section 1.2).
	residentVectors bool
	syncBatchSize   int
	// sidecarParallel is the number of llama-server slots used for embedding.
	// UpsertMany sends several batches concurrently, and a single slot makes
	// them queue; 4 slots match search's default upsert concurrency.
	sidecarParallel int
	fuzzyDefault    *bool
}

// Option configures Open.
type Option func(*options)

// WithEmbedder injects an embedder and skips the embedding sidecar.
func WithEmbedder(e llm.Embedder) Option {
	return func(o *options) { o.embedder = e }
}

// WithReranker injects a reranker and skips the rerank sidecar.
func WithReranker(r llm.Reranker) Option {
	return func(o *options) { o.reranker = r }
}

// WithEmbedModel overrides the embedding model path (absolute or relative to
// config.ModelsDir).
func WithEmbedModel(path string) Option {
	return func(o *options) { o.embedModel = path }
}

// WithRerankModel overrides the rerank model path (absolute or relative to
// config.ModelsDir).
func WithRerankModel(path string) Option {
	return func(o *options) { o.rerankModel = path }
}

// WithSidecarBinary sets the llama-server executable used by default sidecars.
func WithSidecarBinary(path string) Option {
	return func(o *options) { o.binary = path }
}

// WithIdleTimeout sets how long an idle model sidecar stays resident. Zero
// keeps the llm package default; a negative value disables idle killing.
func WithIdleTimeout(d time.Duration) Option {
	return func(o *options) { o.idleTimeout = d }
}

// WithClock injects the store clock (expiry handling, timestamps).
func WithClock(fn func() time.Time) Option {
	return func(o *options) { o.clock = fn }
}

// WithLogger sets where non-fatal indexing warnings are written.
func WithLogger(l *log.Logger) Option {
	return func(o *options) { o.logger = l }
}

// WithSyncProgress installs a callback invoked for every document Sync handles.
func WithSyncProgress(fn SyncProgressFunc) Option {
	return func(o *options) { o.progress = fn }
}

// WithIndexTimeout bounds each synchronous index write made by a store hook.
func WithIndexTimeout(d time.Duration) Option {
	return func(o *options) { o.indexTimeout = d }
}

// WithoutInitialSync skips the Sync that Open normally runs. Callers that only
// read or delete can then open the engine without ever touching a model; a
// later Sync (or the next indexing write) repairs any drift.
func WithoutInitialSync() Option {
	return func(o *options) { o.skipSync = true }
}

// WithResidentVectors keeps the quantized vectors in RAM. Only long-lived
// processes (mem mcp, mem server) should enable it: populating the store costs
// 284 ms at 10k chunks, which short-lived CLI commands cannot pay.
func WithResidentVectors(enabled bool) Option {
	return func(o *options) { o.residentVectors = enabled }
}

// WithSyncBatchSize sets how many documents Sync hands to Index.UpsertMany per
// call. Each batch is committed independently, so an interrupted sync resumes
// where it stopped on the next run.
func WithSyncBatchSize(n int) Option {
	return func(o *options) {
		if n > 0 {
			o.syncBatchSize = n
		}
	}
}

// WithSidecarParallel sets how many llama-server slots the embedding sidecar
// starts (default 4). More slots let UpsertMany's concurrent batches run in
// parallel instead of queueing; 1 falls back to a single slot. The per-slot
// context is kept at the llm package's 512 tokens by scaling -c with the count.
func WithSidecarParallel(n int) Option {
	return func(o *options) {
		if n > 0 {
			o.sidecarParallel = n
		}
	}
}

func defaultOptions() options {
	return options{
		// The binary stays empty unless injected: buildClients then prefers
		// config.SidecarPath, and only falls back to PATH at the end.
		binary:          "",
		logger:          log.Default(),
		indexTimeout:    DefaultIndexTimeout,
		sidecarParallel: defaultSidecarParallel,
	}
}

// Open opens the store, builds the model clients, opens the search index and
// brings it in line with the store. Model processes are lazy: the engine itself
// starts nothing, and the initial Sync touches a model only when documents are
// missing from the index. A sync failure caused by an unavailable model is
// recorded as a warning instead of failing the open, so the store stays usable.
func Open(cfg config.Config, opts ...Option) (*Engine, error) {
	if cfg.DataDir == "" {
		return nil, errors.New("engine: data dir is empty")
	}
	o := defaultOptions()
	for _, opt := range opts {
		opt(&o)
	}

	fuzzyDefault := o.fuzzyDefault
	if fuzzyDefault == nil {
		b := cfg.Fuzzy()
		fuzzyDefault = &b
	}

	e := &Engine{
		logger:        o.logger,
		indexTimeout:  o.indexTimeout,
		syncBatchSize: o.syncBatchSize,
		progress:      o.progress,
		fuzzyDefault:  fuzzyDefault,
	}

	if err := e.buildClients(cfg, o); err != nil {
		e.closeSidecars()
		return nil, err
	}

	storeOpts := []store.Option{store.WithHooks(e.hooks())}
	if o.clock != nil {
		storeOpts = append(storeOpts, store.WithClock(o.clock))
	}
	s, err := store.Open(cfg, storeOpts...)
	if err != nil {
		e.closeSidecars()
		return nil, fmt.Errorf("engine: open store: %w", err)
	}
	e.Store = s

	searchOpts := []search.Option{
		search.WithResidentVectors(o.residentVectors),
	}
	if fuzzyDefault != nil {
		searchOpts = append(searchOpts, search.WithFuzzy(*fuzzyDefault))
	}
	ix, err := search.OpenIndex(filepath.Join(cfg.DataDir, SearchDBFile), e.embedder, e.reranker, searchOpts...)
	if errors.Is(err, search.ErrNeedsReindex) {
		e.recordWarning("engine: search index needs a rebuild: " + err.Error())
		ix, err = search.OpenIndex(filepath.Join(cfg.DataDir, SearchDBFile), e.embedder, e.reranker,
			append(searchOpts, search.WithRebuild())...)
	}
	if err != nil {
		e.Close()
		return nil, fmt.Errorf("engine: open search index: %w", err)
	}
	e.Index = ix

	if _, err := s.Reconcile(); err != nil {
		e.Close()
		return nil, fmt.Errorf("engine: reconcile store: %w", err)
	}
	if _, err := s.SweepExpired(); err != nil {
		e.Close()
		return nil, fmt.Errorf("engine: sweep expired contexts: %w", err)
	}
	if !o.skipSync {
		if _, err := e.Sync(context.Background()); err != nil {
			e.recordWarning("engine: initial sync incomplete (run Sync to retry): " + err.Error())
		}
	}
	return e, nil
}

// buildClients constructs embedders and rerankers. Injection wins; otherwise a
// lazy sidecar is created, which does not spawn a process until first use.
func (e *Engine) buildClients(cfg config.Config, o options) error {
	if o.embedder != nil {
		e.embedder = &trackingEmbedder{inner: o.embedder, eng: e}
		e.embedState = "ready"
	} else {
		model := resolveModelPath(cfg.ModelsDir, firstNonEmpty(o.embedModel, cfg.EmbedModel, EmbedModelFile))
		if model == "" {
			return errors.New("engine: models_dir is empty; set it or pass WithEmbedder")
		}
		sc := llm.NewEmbedSidecar(llm.SidecarConfig{
			Binary:      firstNonEmpty(o.binary, cfg.SidecarPath, DefaultLlamaBinary),
			Model:       model,
			IdleTimeout: o.idleTimeout,
			Parallel:    o.sidecarParallel,
			// -c is the total context divided across slots; scale it so every
			// slot keeps the 512 tokens the embed mode was tuned for.
			Args: parallelContextArgs(o.sidecarParallel),
		})
		e.sidecars = append(e.sidecars, sc)
		e.embedder = &trackingEmbedder{inner: sc.AsEmbedder(), eng: e}
		if _, err := os.Stat(model); err != nil {
			e.embedState = "error"
			e.embedDetail = fmt.Sprintf("model file missing: %s", model)
		} else {
			e.embedState = "stopped"
		}
	}

	if o.reranker != nil {
		e.reranker = &trackingReranker{inner: o.reranker, eng: e}
		e.rerankState = "ready"
	} else if cfg.Rerank {
		model := resolveModelPath(cfg.ModelsDir, firstNonEmpty(o.rerankModel, cfg.RerankModel))
		if model == "" {
			e.recordWarning("engine: rerank is enabled but rerank_model is empty; reranking stays disabled")
			e.rerankState = "error"
			e.rerankDetail = "rerank is enabled but rerank_model is empty"
		} else {
			sc := llm.NewRerankSidecar(llm.SidecarConfig{
				Binary:      firstNonEmpty(o.binary, cfg.SidecarPath, DefaultLlamaBinary),
				Model:       model,
				IdleTimeout: o.idleTimeout,
			})
			e.sidecars = append(e.sidecars, sc)
			e.reranker = &trackingReranker{inner: sc.AsReranker(), eng: e}
			if _, err := os.Stat(model); err != nil {
				e.rerankState = "error"
				e.rerankDetail = fmt.Sprintf("rerank model file missing: %s", model)
			} else {
				e.rerankState = "stopped"
			}
		}
	} else {
		e.rerankState = "disabled"
	}
	return nil
}

// parallelContextArgs returns the -c override that keeps each parallel slot at
// embedContextPerSlot tokens. Below two slots the llm default (512 total) fits.
func parallelContextArgs(parallel int) []string {
	if parallel <= 1 {
		return nil
	}
	return []string{"-c", strconv.Itoa(embedContextPerSlot * parallel)}
}

func resolveModelPath(modelsDir, name string) string {
	if name == "" {
		return ""
	}
	if filepath.IsAbs(name) {
		return name
	}
	if modelsDir == "" {
		return ""
	}
	return filepath.Join(modelsDir, name)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func (e *Engine) hooks() store.Hooks {
	return store.Hooks{
		OnUpsert:        e.indexUpsert,
		OnDelete:        e.indexDelete,
		OnContextDelete: e.indexDeleteContext,
	}
}

func (e *Engine) indexUpsert(m store.Memory) {
	if e.Index == nil {
		return
	}
	ctx, cancel := e.indexContext()
	defer cancel()
	err := e.Index.Upsert(ctx, search.Doc{
		ID:      m.ID,
		Context: m.Context,
		Name:    m.Name,
		Body:    m.Body,
		Version: m.Version,
	})
	if err != nil {
		if errors.Is(err, search.ErrVectorsPending) {
			e.recordWarning("semantic indexing pending (embedder unavailable); keyword search works; run `mem sync`")
		} else {
			e.recordWarning(fmt.Sprintf("engine: index memory %s/%s: %v", m.Context, m.ID, err))
		}
	}
}

func (e *Engine) indexDelete(id, _ string) {
	if e.Index == nil {
		return
	}
	ctx, cancel := e.indexContext()
	defer cancel()
	if err := e.Index.Delete(ctx, id); err != nil {
		e.recordWarning(fmt.Sprintf("engine: unindex memory %s: %v", id, err))
	}
}

func (e *Engine) indexDeleteContext(slug string) {
	if e.Index == nil {
		return
	}
	ctx, cancel := e.indexContext()
	defer cancel()
	if err := e.Index.DeleteContext(ctx, slug); err != nil {
		e.recordWarning(fmt.Sprintf("engine: unindex context %s: %v", slug, err))
	}
}

func (e *Engine) indexContext() (context.Context, context.CancelFunc) {
	if e.indexTimeout <= 0 {
		return context.WithCancel(context.Background())
	}
	return context.WithTimeout(context.Background(), e.indexTimeout)
}

// Warnings returns the recent non-fatal indexing warnings, oldest first. A
// write whose indexing failed is still committed to the store; Sync repairs it.
func (e *Engine) Warnings() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.warnings...)
}

func (e *Engine) recordWarning(msg string) {
	if e.logger != nil {
		e.logger.Print(msg)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.warnings = append(e.warnings, msg)
	if len(e.warnings) > maxWarnings {
		e.warnings = e.warnings[len(e.warnings)-maxWarnings:]
	}
}

// Close releases the index, the store and any started sidecars.
func (e *Engine) Close() error {
	var errs []error
	if e.Index != nil {
		errs = append(errs, e.Index.Close())
		e.Index = nil
	}
	if e.Store != nil {
		errs = append(errs, e.Store.Close())
		e.Store = nil
	}
	errs = append(errs, e.closeSidecars()...)
	return errors.Join(errs...)
}

func (e *Engine) closeSidecars() []error {
	errs := make([]error, 0, len(e.sidecars))
	for _, sc := range e.sidecars {
		if err := sc.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	e.sidecars = nil
	return errs
}
