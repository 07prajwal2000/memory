# 02b — Search integration (`internal/engine`)

## What was built

`internal/engine` is the single integration layer between `internal/store` (files + metadata index) and `internal/search` (hybrid SQLite FTS5 + sqlite-vec index). It provides:

- `engine.Open` — opens the store, builds the model clients, opens `<data_dir>/search.db`, installs store hooks, runs `Reconcile` + `SweepExpired` + `Sync`.
- `Engine.Store` / `Engine.Index` — exposed for all non-search operations; nothing is re-wrapped.
- `Engine.Search` — one high-level entry point (context resolution + hybrid query) for the CLI, MCP and dashboard.
- `Engine.Sync` — repairs the index from the store, with progress reporting.
- `Engine.DefaultFile` — `global/readme.md` for MCP instructions and `get` with no args.
- `Engine.Warnings` — non-fatal indexing failures for callers that care.

`internal/config` gained exactly two additive fields: `rerank bool` (default `false`) and `rerank_model string` (default empty). No other package was edited (`internal/search`, `internal/llm`, `internal/store`, `spike/`, `.prompts/` untouched).

## API as built

```go
package engine

const (
    EmbedModelFile      = "bge-small-en-v1.5-q8_0.gguf"
    SearchDBFile        = "search.db"
    DefaultLlamaBinary  = "llama-server"
    DefaultIndexTimeout = 30 * time.Second
)

type Engine struct {
    Store *store.Store
    Index *search.Index
}

type Option func(*options)
func WithEmbedder(llm.Embedder) Option
func WithReranker(llm.Reranker) Option
func WithEmbedModel(path string) Option
func WithRerankModel(path string) Option
func WithSidecarBinary(path string) Option
func WithClock(func() time.Time) Option
func WithLogger(*log.Logger) Option
func WithSyncProgress(SyncProgressFunc) Option
func WithIndexTimeout(time.Duration) Option
func WithoutInitialSync() Option
func WithResidentVectors(bool) Option      // RAM vectors; long-lived daemons only
func WithSyncBatchSize(int) Option          // documents per UpsertMany call, default 32
func WithSidecarParallel(int) Option        // embed sidecar slots, default 1

func Open(cfg config.Config, opts ...Option) (*Engine, error)
func (e *Engine) Close() error
func (e *Engine) DefaultFile() (store.Memory, error)
func (e *Engine) Warnings() []string

type SearchRequest struct {
    Query    string
    Context  string
    NoGlobal bool
    Limit    int
    PerDoc   int
}
func (e *Engine) Search(ctx context.Context, r SearchRequest) (search.Result, error)

type SyncReport struct {
    Total, Added, Updated, Removed, Unchanged, Indexed int
    Duration time.Duration
}
type SyncProgress struct {
    Phase   string // "delete" | "index"
    DocID   string
    Context string
    Done    int
    Total   int
}
type SyncProgressFunc func(SyncProgress)
func (e *Engine) Sync(ctx context.Context) (SyncReport, error)
```

## Behavior

### Writes → index (synchronous)

Hooks are registered before `store.Open`, so every committed `Upsert`/`Delete`/`DeleteContext` (and every change discovered by `Reconcile`) calls `Index.Upsert`/`Index.Delete`/`Index.DeleteContext` synchronously before the store call returns. A write followed immediately by a search sees the new document (read-your-writes).

### Warning surface for indexing failures

`store.Hooks` cannot return errors, and the prompt says not to re-wrap store operations. So an indexing failure is:

1. logged through the `WithLogger` logger (default `log.Default()`),
2. appended to a bounded, thread-safe ring exposed by `Engine.Warnings()` (oldest first, last 32), and
3. repaired later by `Sync` because the store row and the index `docs` row disagree.

The `Upsert` caller always gets its `store.Memory` back; no store write is rolled back. Call sites may ignore `Warnings()` or show it to the user. Each hook write is bounded by `WithIndexTimeout` (default 30 s) so a dead/slow model cannot block a store write indefinitely.

### Sync

`Sync` compares `Index.State()` (id, context, version, hash) with every live context from `Store.ListContexts()` + `Store.List` + `Store.Get` (bodies are needed for the hash):

- index entries with no store doc → `Index.Delete` (orphans);
- missing docs → `Index.Upsert`, counted as `Added`;
- version/context/hash mismatch → `Index.Upsert`, counted as `Updated`;
- equal → `Unchanged`.

`SyncReport.Total` is `orphans + desired docs`; `WithSyncProgress` fires once per document with an incrementing `Done`. `Open` runs `Sync` once; its error is downgraded to a warning (the store must remain usable with no model), while an explicit `Engine.Sync` (future `mem reindex`) returns the error. `ErrNeedsReindex` from `search.OpenIndex` triggers an automatic `search.WithRebuild()` open and the rebuilt index is then repopulated by `Sync`.

### Search

`SearchRequest` resolves to `[]contexts`: `global` unless `NoGlobal`, plus `Context` when set. `Context` is validated with `Store.GetContext`, so an unknown or expired context returns a wrapped `store.ErrNotFound`; `NoGlobal` with no `Context` errors; duplicates are removed; an empty/whitespace query returns `ErrEmptyQuery`. `Limit`/`PerDoc` are passed through to `search.Query`.

### Models (lazy, opt-in rerank)

- Embedder: injected via `WithEmbedder`, otherwise a `llm.Sidecar` wrapping `llama-server` with model `<models_dir>/bge-small-en-v1.5-q8_0.gguf`. Sidecars start on first `URL()` call, never at construction.
- Reranker: only when `cfg.Rerank == true` **and** `cfg.RerankModel` resolves to a path (or a reranker is injected). Otherwise `search.OpenIndex` receives `nil`. `search.Query.NoRerank` is also set when no reranker exists.
- `--init`/CLI will later write these config values; `Engine` has no model-download logic (out of scope).

## Decisions I made

1. **One SQLite Wasm build for the whole binary (fixed).** `internal/store` used to blank-import `ncruces/go-sqlite3/embed` (stock build) while `internal/search` blank-imports `asg017/sqlite-vec-go-bindings/ncruces` (vec build). Both assign the global `sqlite3.Binary` from package `init()`, and Go's initialization order put the stock build last, so `CREATE VIRTUAL TABLE … vec0` failed with "no such module: vec0" in any binary linking both packages. The fix lives in `internal/store`, which now blank-imports `github.com/asg017/sqlite-vec-go-bindings/ncruces` instead of the stock embed package, so every package in the binary (store, search, engine) links the same vec-enabled Wasm and nothing re-assigns `sqlite3.Binary` after it. The sqlite-vec build is a superset of the stock one (same SQLite release plus the `vec0` module), so the store's FTS5/JSON/WAL behavior is unchanged. Grepping the repo confirms no package imports the stock embed package any more (only `spike/` and `internal/search` reference the vec bindings). A regression test, `TestSingleWasmBuildSupportsVec0`, opens a `store.Store` and a `search.Index` in one process and then creates, writes and KNN-queries a `vec0` table; re-adding the stock import makes it fail with the "no such module: vec0" error, so the collision cannot come back unnoticed.
2. **Synchronous indexing, async repair.** Hooks index inside the write call (read-your-writes), but failures never fail the write; the doc stays missing/stale and `Sync` repairs it. This matches the prompt and avoids a background queue.
3. **Warnings via `Engine.Warnings()` + logger**, not a wrapper return, because store operations must stay directly on `Engine.Store`.
4. **`Open` never fails because a model is unavailable.** Only store/index open and `Reconcile` are fatal; the initial `Sync` error becomes a warning. This keeps the CLI/MCP usable in BM25-only mode.
5. **Model path resolution:** `rerank_model` and `--embed-model` are absolute, or relative to `models_dir`. Embedding model name is the fixed `EmbedModelFile` constant.
6. **Sync reads every body.** `store.List` returns no body and `store` exposes no hash, so hash comparison requires `Get` for every memory. Acceptable for the first version; an exported store hash or `List` body would avoid it (noted for 01 owners).
7. **Progress callback granularity:** one event per document (including unchanged ones), `Total` known up front after the store scan.
8. **`Sync` fails fast** on the first index error and returns the partial `SyncReport`. Continuing would hide a broken model behind a long try/retry loop.
9. **Engine does not wrap `Get`/`Upsert`/`Delete`/context operations**, per the prompt. `DefaultFile` is the only convenience read.

## Deviations / notes

- The 02a `REPORT.md` documents `Chunk.Embed(docName string)` and `SanitizeFTS5(raw)`; the actual code uses `Chunk.Embed()` (doc name is a `Chunk.DocName` field) and `SanitizeFTS5(raw, contexts)`. I coded against the code.
- The prompt asks for "bulk reindex commits in batches". The current `search.Index.Upsert` commits per document; `search.WithBatchCommitSize` exists but is not consulted by 02a's implementation, and `Sync` may not edit that package. `Sync` therefore indexes document-by-document and reports progress; batching remains a 02c-owned improvement.
- "Opening must not start a model process": `Open` starts no process by construction, but on a *fresh* data dir the initial `Sync` must embed the seeded `global/readme.md` and therefore starts the sidecar (that is the first index call). The test verifies the strict version that matters for repeated opens: opening an already-synced index with a deliberately nonexistent `llama-server` produces zero warnings, i.e. no spawn attempt; only `Search`/index then degrades and touches the (missing) sidecar.
- Two Engine instances were simulated in one process (two independent `sql.DB` handles over the same files) as the prompt allows; true multi-process WAL behavior across separate OS processes is not exercised, and sidecars are per-process (02a documented this).

## Dependencies added

None. No new dependency and no new binary artifact: the sqlite-vec Wasm comes from the already-required `github.com/asg017/sqlite-vec-go-bindings@v0.1.6`, now also imported by `internal/store` instead of the stock embed package. No `go mod tidy` was run; `go.mod`/`go.sum` are unchanged.

## Tests

All non-E2E coverage uses the injected fake embedder/reranker, so it needs no models:

- write → immediate search hit;
- update changes indexed version/content;
- delete removes from index and search;
- context delete removes docs;
- temp expiry (injected clock) removes docs after the sweep;
- global always included; `NoGlobal` excludes it; `NoGlobal` without a context errors;
- unknown/expired context errors (`ErrNotFound`); empty query errors;
- hand-edited file adopted by `Reconcile` + `Sync` is searchable;
- index failure keeps the store write, emits a warning, and `Sync` repairs it;
- opening an up-to-date index does not touch the model, first search degrades to BM25;
- `ErrNeedsReindex` (simulated by editing `meta.model_id`) rebuilds from files;
- `Sync` repairs deleted/stale/orphaned index entries and reports correct counts;
- progress callback counts equal `SyncReport.Total`;
- two Engine instances concurrently writing different docs to the same data dir, then syncing, see every doc;
- reranking is off by default, and on with `rerank=true` + model;
- opening with no model still works and leaves the store writable;
- single-Wasm regression: a `Store` and a `search.Index` open together, then a `vec0` table is created, written and KNN-queried (fails if the stock embed import returns);
- `MEM_E2E=1` test (real `llama-server` + GGUF models) — **skipped in this run** (see Unverified).

Output (this run):

```
ok  memory/internal/config  1.172s
ok  memory/internal/engine  3.401s
ok  memory/internal/llm     8.265s
ok  memory/internal/search  1.521s
ok  memory/internal/store   5.893s
```

## How to run the checks

```sh
go build ./...
go vet ./...
go test ./... -count=1
```

Cross-builds (all passed):

```sh
# windows/amd64, windows/arm64, linux/amd64, linux/arm64, darwin/amd64, darwin/arm64
CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> go build ./...
```

Real-model E2E (opt-in):

```sh
MEM_E2E=1 MEM_MODELS_DIR=/path/to/models MEM_LLAMA_BIN=/path/to/llama-server \
  go test ./internal/engine/ -run TestE2EWithRealModels -count=1
```

## Follow-up: single SQLite Wasm build

The 02b hack is gone. `internal/store` now blank-imports `github.com/asg017/sqlite-vec-go-bindings/ncruces` instead of `github.com/ncruces/go-sqlite3/embed`, so `store`, `search` and `engine` all link the same vec-enabled Wasm; `internal/engine/sqlite3_vec.wasm` and the `init()` re-assertion were deleted. A grep for `go-sqlite3/embed` finds no remaining importer, and a grep for `sqlite3.Binary` finds no remaining assignment in the repo.

`TestSingleWasmBuildSupportsVec0` in `internal/engine` guards the fix: it opens a `store.Store` and a `search.Index` in the same process (so the two Wasm builds would collide if the old import came back), then creates a `vec0` virtual table, inserts a quantized vector and runs a KNN query. I verified the guard is real by temporarily re-adding the stock import — the test failed with `sqlite3: SQL logic error: no such module: vec0` — then removed it again and the suite went green.

E2E was skipped deliberately: `hc get t23` reported the other agent (agy) as `"state":"working"` (CPU benchmarks in progress), and running `llama-server` during its measurements would skew them. Model files are present for a later run (`spike/models/bge-small-en-v1.5-q8_0.gguf`, `spike/bin/llama-server.exe`).

## Follow-up: batched Sync, resident vectors, pending vectors

`Sync` now hands documents to `search.Index.UpsertMany` in batches instead of calling `Upsert` once per memory, long-lived processes keep vectors resident, and `Sync` fills vectors that a failed embed left pending.

### API changes (additive)

```go
func WithSyncBatchSize(n int) Option        // documents per UpsertMany call, default 32
func WithResidentVectors(enabled bool) Option // RAM vectors, default false
func WithSidecarParallel(n int) Option      // embed sidecar slots, default 1 (measured: more is slower here)
```

`SyncReport` gained `Filled int`, the number of chunks whose vectors were embedded from the pending queue during that pass.

### How Sync works now

1. Read `Index.State()` and the store, as before.
2. Delete orphans one by one.
3. Collect the documents whose version/context/hash differ, then call `UpsertMany` once per `WithSyncBatchSize` batch (default 32).
4. If the index still has pending vectors, call `FillPendingVectors` until it returns zero or errors, counting the filled chunks in `Filled`. A still-broken embedder leaves them pending for the next run and records a warning instead of failing.

Each batch commits independently (and each `UpsertMany` commits in WASM-safe chunks of at most 5,000, per 02c3), so an interrupted sync keeps the batches that finished and the next run picks up only what is missing. Progress events still fire once per document with a monotonic `Done` and a `Total` known up front.

### Resident vectors

`engine.WithResidentVectors(true)` opens the index with `search.WithResidentVectors(true)`. `mem mcp` and `mem server` pass it — they are long-lived and search's 02c3 report measured a 15.2 s populate at 200k chunks while the SQLite path costs 1.85 s cold — and the background populate serves queries from SQLite until it finishes. Every CLI command keeps the default `false`, so a one-shot `mem search` stays at the 40–70 ms startup measured in `internal/cli/REPORT.md`.

### Parallel sidecar: measured, so left at one slot

`UpsertMany` sends several batches concurrently (`WithUpsertConcurrency`, default 4). Worth testing was whether the embed sidecar should start more than one slot: it was built for the 02a single-agent workload (`--embedding -c 512`, one slot), so concurrent batches queue behind each other.

Measured directly against `llama-server b12503` with the same 64-text payloads (`internal/llm.HTTPEmbedder`, batch 16):

| Configuration | Workers | Throughput |
|---|---|---|
| `--parallel 1`, `-c 512` | 1 | **109.6 texts/s** |
| `--parallel 1`, `-c 512` | 4 | **123.8 texts/s** |
| `--parallel 4`, `-c 2048` (512/slot) | 1 | 87.2 texts/s |
| `--parallel 4`, `-c 2048` (512/slot) | 4 | 103.3 texts/s |

Four slots were **slower on this machine** even though each slot kept its 512-token context (`n_ctx_slot = 512` confirmed from the server log). Splitting the KV cache and the batch into four sequences costs more than the added concurrency buys for 384-token embedding work at this size. So `WithSidecarParallel` defaults to **1** — the 02a setting — and `parallelContextArgs` only emits a `-c` override when a caller raises it above 1 (keeping 512 tokens per slot). The option exists so hardware where more slots do help can use them without touching `internal/llm`.

### Real Sync of ~300 memories (before vs after)

Setup: 300 generated markdown memories (10 topics × 5 predicates × 10 objects, ~90 words each) in one context, the real `bge-small-en-v1.5-q8_0.gguf`, the real `llama-server.exe`, a fresh data dir per run so the full corpus is indexed. Both binaries were built from the **same** `internal/search` revision; the "before" binary was built with `defaultSyncBatchSize = 1` and `defaultSidecarParallel = 1`, i.e. the pre-change behavior expressed in the new code.

| Run | Before | After (batch 32) |
|---|---|---|
| 1 | 34.2 s | 31.3 s |
| 2 | 33.2 s | 28.9 s |
| 3 | 28.4 s | 28.6 s |
| **best** | **28.4 s** | **28.6 s** |

**Honest conclusion: no measurable speedup on this machine under these conditions.** Batched `UpsertMany` and a wider sidecar did not move end-to-end sync of 300 small documents away from ~28–34 s. The measurement is heavily contaminated: another agent's model-eval `llama-server` was running throughout and alternating runs of the *same* binary varied from 20.5 s to 41.5 s (earlier rounds), i.e. run-to-run spread exceeds the difference between configurations. A standalone embed benchmark in the same window reached ~110–125 texts/s, so the ~4,500 chunks in this corpus should take ~40 s of pure embedding at most — the model is the bottleneck, and the remaining time is chunking, SHA-256 dedup lookups and four SQLite inserts per chunk over the WASM boundary, none of which batching changes.

What batching *does* buy, verified by tests rather than by wall clock: fewer embed round-trips (a 6-document sync with batch size 2 makes several calls instead of six), no per-document transaction overhead, and a resumable boundary at every batch.

### Pending vectors

With 02c3's change, a failed embed no longer loses the write: the keyword rows commit and the vectors are marked pending. `Sync` now retries them after the document pass, so a `mem sync` run once the model is back fills them. This replaced the earlier behavior where an offline embedder made `UpsertMany` return an error and `Sync` stop. `TestSyncDegradesWhenEmbedderDiesMidway` and `TestSyncFillsPendingVectors` cover both paths; the CLI's `mem sync` prints and reports `filled`.

One consequence worth flagging to the MCP owner: `internal/mcp/tools.go` derives its "saved, but search indexing failed" note from a delta in `eng.Warnings()`. Since 02c3, a failed embed is handled *inside* the index (pending vectors) rather than surfacing as an engine warning, so that delta is now zero and `internal/mcp.TestTools_Upsert/indexing_warning_appended` fails. I did not touch `internal/mcp`; the fix there is to report the pending state (`Index.PendingVectors()`/`DocState.PendingVectors`) instead of a warning delta. That is the only failing test in the repo after this change:

```
ok   memory/internal/engine   6.895s
ok   memory/internal/cli      3.895s
ok   memory/internal/config   0.864s
ok   memory/internal/setup    3.221s
ok   memory/internal/search   7.493s
ok   memory/internal/store    7.766s
ok   memory/internal/llm      8.700s
ok   memory/internal/server   4.164s
FAIL memory/internal/mcp      (TestTools_Upsert/indexing_warning_appended — see above)
```

`go build ./...` and `go vet ./...` are clean, and `./cmd/mem` cross-builds with `CGO_ENABLED=0` for windows/amd64, windows/arm64, linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64.

## Unverified

- **The 300-document before/after measurement is inconclusive**, not a win. Another agent's model eval ran on the same CPU throughout, single-binary variance was 20.5–41.5 s, and the before/after difference (28.4 vs 28.6 s best) is far inside that noise. The parallel-slot numbers are from a short isolated embed benchmark, not a quiet machine.
- `FillPendingVectors` is capped at 5,000 chunks per call by search; `Sync` loops until it returns zero, so a very large backlog takes several calls. Not exercised at that scale.
- Resident vectors on 200k chunks were not re-measured here; the 15.2 s / 140 MB figures are 02c3's.
- `-race` cannot run here (`-race requires cgo`).
- The E2E with real models on macOS/Linux was not run (Windows only).
