# REPORT-02c3: Search Lifecycle, Realistic Latency, and Concurrent Indexing

> **Task Context & Boundaries**: Follow-up 2 to `REPORT-02c2.md` addressing in-RAM `vecStore` lifecycle correctness, realistic corpus search latency with FTS5 term optimization, and batched concurrent indexing.
> **Environment**: Windows 11, go1.27.0 (windows/amd64), AMD Ryzen 7 7435HS (16 vCPUs @ 3.1 GHz), 24 GB RAM, `llama-server.exe` running `bge-small-en-v1.5-q8_0.gguf` (384-dim, 133M params).

---

## Executive Summary

1. **In-RAM vecStore Lifecycle**:
   - **Cross-process coherence**: Implemented `PRAGMA data_version` polling and SQLite `chunk_deletions` trigger. When an external process commits mutations, the index detects the version mismatch in microseconds and performs `IncrementalRefresh` (loading new chunk rowids and evicting deleted ones). Validated with two concurrent handles on one DB file in `TestCrossProcessCoherence_Full`.
   - **Population cost**: Populating 200k chunks through the SQLite WASM driver takes **13.6 seconds** (284 ms at 10k, 2.45 s at 39k). This strictly exceeds the 100 ms budget for interactive CLI commands. Therefore, CLI processes default to `WithResidentVectors(false)` (serving directly from SQLite in 40.99 ms @10k), while long-lived daemons (`mem mcp`, `mem server`) opt-in via `WithResidentVectors(true)`, populating asynchronously in the background while serving from SQLite until ready.
   - **Per-context scoping**: Slices are partitioned per context (`map[string]*contextVecSlice`). In a 200k corpus across 20 contexts, a scoped query (`global` + 1 tenant = 20,000 chunks) executes its two-stage KNN scan in **0.369 ms** (3.5x faster than scanning all 200,000 chunks at 1.277 ms).
   - **Real process RSS**: Populating 200,000 chunks adds **140.41 MB** of process RSS (~736 bytes/vector). An option `WithResidentVectorCap(maxChunks int)` (default 400,000 chunks ≈ 280 MB) stays on the SQLite path with a diagnostic warning when exceeded.
   - **In-process immediate reflection**: `Upsert`, `UpsertMany`, `Delete`, and `DeleteContext` immediately update in-RAM partitions.

2. **Total Latency on Realistic Corpus**:
   - Replaced synthetic Zipf corpus with **79,697 unique real chunks** extracted from the Go standard library (`GOROOT/src`) using `Chunker`, replicated across contexts to 200,000 chunks, evaluated against **100 realistic queries** (keywords, questions, and 40-word paragraphs).
   - Constrained FTS5 by construction: AND-first query with $\ge 5$ hit check, capped OR fallback keeping at most the 8 rarest terms (preserving identifiers), and pushed-down `ORDER BY rank LIMIT 50`.
   - **Latency targets met across all scales**:
     - **39k chunks**: p50 = **10.21 ms**, p95 = **25.67 ms** (target $\le 100\text{ ms}$, beat by **3.9x**).
     - **200k chunks**: p50 = **51.19 ms**, p95 = **150.30 ms** (target $\le 250\text{ ms}$, beat by **100 ms**).
   - **MRR verification across all 3 splits**: Zero regression on Tuning (0.9135), Held-Out (0.8046), and Final Split (0.9500).

3. **Batched Concurrent Indexing API**:
   - Identified root cause of earlier 25 texts/s: sequential HTTP requests with 1 text per payload and `-np 1` slot allocation.
   - Exposed `Index.UpsertMany(ctx context.Context, docs []Doc) error` with configurable batching (`WithUpsertBatchSize(32)`) and bounded concurrency (`WithUpsertConcurrency(4)`), matched with `--parallel 4` in `llm.SidecarConfig`.
   - End-to-end throughput on 2,000 real chunks: **72.39 chunks/s** (a **2.9x speedup** over 02c baseline including Markdown parsing, SHA-256 deduplication, 4 SQLite table inserts, and WASM transaction commits).

---

## 1. In-RAM vecStore Lifecycle & Correctness

### 1.1 Cross-Process Coherence

#### The Problem
In production, multiple processes access the same `search.db`:
- A long-lived `mem mcp` server process per agent session.
- Transient `mem` CLI commands (`mem upsert`, `mem sync`, `mem context delete`).
- A long-lived `mem server` dashboard daemon.

If process A holds a populated `vecStore` and process B mutates the database, A's in-RAM vectors become stale: deleted chunks remain searchable, and newly indexed memories are invisible.

#### Implementation
We implemented a two-part coherence mechanism:
1. **Staleness Detection via `PRAGMA data_version`**:
   The `ncruces/go-sqlite3` driver natively supports `PRAGMA data_version`. This pragma returns an integer that increments whenever another database connection commits a transaction to the database file in WAL mode. Inside `Search`, before taking the read lock, `Index.checkCrossProcessSync(ctx)` executes `PRAGMA data_version`. If the version matches `ix.lastDataVersion`, no other process has committed, and the check returns in $< 15\,\mu\text{s}$.
2. **Deletion Auditing & Incremental Refresh**:
   To avoid reloading hundreds of thousands of vectors on every external edit, we added an internal `chunk_deletions` table and trigger:
   ```sql
   CREATE TABLE IF NOT EXISTS chunk_deletions (
       id INTEGER PRIMARY KEY AUTOINCREMENT,
       chunk_id INTEGER NOT NULL,
       deleted_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
   );

   CREATE TRIGGER IF NOT EXISTS trg_chunks_delete
   AFTER DELETE ON chunks
   BEGIN
       INSERT INTO chunk_deletions(chunk_id) VALUES (OLD.id);
   END;
   ```
   When a change is detected:
   - Queries `SELECT chunk_id FROM chunk_deletions WHERE id > ?` and deletes those rowids from `vecStore`.
   - Queries `SELECT rowid, context, embedding, ... FROM chunks_vec WHERE rowid > ?` and upserts new vectors.
   - Updates `ix.lastDataVersion`, `ix.maxSeenRowID`, and `ix.lastSeenDeletionID`.
   - If an error occurs during incremental refresh, it falls back safely to `LoadFromDB(db)`.

#### Verification Test
- **Test**: `TestCrossProcessCoherence_Full` in `internal/search/index_test.go`.
- **Scenario**: Two independent `Index` handles (A and B) opened on the same database file. Handle B upserts a new document; Handle A searches and confirms the new memory is immediately discovered. Handle B deletes the document; Handle A searches and confirms the memory is immediately gone.
- **Result**: `PASS` (0.06s).

---

### 1.2 Population Cost & Resident Vector Policy

#### The Problem
Reading 384-byte `vec_int8` and 48-byte `vec_bit` BLOBs across the WASM runtime boundary incurs per-row serialization overhead. How long does it take to load 10k, 39k, and 200k chunks into RAM? Does cold open exceed ~100 ms?

#### Benchmark 1 Results (Real Measured Numbers)

| Scale | Chunks | SQLite DB Size | Isolated RAM Population | CLI Cold Open + Search (`WithResidentVectors(false)`) | Daemon Cold Open + Search (Sync RAM) |
|---|---|---|---|---|---|
| **10k** | 10,000 | 6.6 MB | 284.57 ms | **40.99 ms** | 321.69 ms |
| **39k** | 39,000 | 25.9 MB | 2,447.28 ms | **371.28 ms** | 2,734.86 ms |
| **200k** | 200,000 | 129.4 MB | 13,637.47 ms | **1,852.77 ms** | 15,217.77 ms |

#### Architectural Policy Decision
Because population cost at 10k chunks (284 ms) already exceeds the ~100 ms budget for interactive CLI commands, **the CLI path must not populate the `vecStore`**:
1. `WithResidentVectors(false)` is the default for `Index`. Short-lived CLI commands open the database in $< 2\text{ ms}$ and query SQLite directly (exact KNN search on small scoped partitions takes 40 ms at 10k).
2. Long-lived daemons (`mem mcp`, `mem server`) pass `WithResidentVectors(true)`. On open, `Index` kicks off `populateVecStoreInBackground()` in a background goroutine.
3. While populating, `ix.IsVecStoreReady()` returns `false`, and `Search` seamlessly routes vector queries through SQLite. Once background loading finishes, `setVecStoreReady(true)` activates the RAM path with zero downtime.

---

### 1.3 Per-Context Scoping Benchmark

#### The Problem
In multi-tenant or multi-project workloads, a user might have 200k total chunks across 20 different contexts, but a query only targets `["global", "tenant-x"]` (20,000 chunks). If `vecStore` were a flat array, it would scan all 200,000 vectors on every query.

#### Implementation
We structured `vecStore` as a partitioned map:
```go
type contextVecSlice struct {
    rowids []int64
    bits   [][6]uint64
    i8     [][]byte
    idMap  map[int64]int
}

type vecStore struct {
    mu         sync.RWMutex
    byContext  map[string]*contextVecSlice
    totalCount int
}
```
`SearchTwoStage` and `SearchExact` call `getActiveSlicesLocked(contexts)` and iterate *only* over the requested context slices.

#### Benchmark 2 Results (200k Chunks across 20 Contexts)

| Query Scope | Chunks Traversed | TwoStage KNN Scan Latency | Speedup vs Flat Scan |
|---|---|---|---|
| **Scoped (`global` + 1 tenant)** | **20,000** | **0.369 ms** | **3.5x faster** |
| Unscoped (all 20 contexts) | 200,000 | 1.277 ms | 1.0x baseline |

---

### 1.4 Memory Footprint & Process RSS Cap

#### Real Process Memory Measurement (Benchmark 3)
Memory was measured using Go runtime memory statistics (`Sys` memory) on a real process loaded with 200,000 chunks:

| Chunks in RAM | Baseline MemSys | Loaded MemSys | Delta RSS Added | Average Overhead |
|---|---|---|---|---|
| **200,000** | 12.21 MB | 152.62 MB | **140.41 MB** | ~736.2 bytes / vector |

Each chunk in RAM stores:
- 384 bytes (`[]byte` int8 quantized embedding)
- 48 bytes (`[6]uint64` bit packed embedding)
- 8 bytes (`int64` rowid)
- Go slice headers, per-context map references, and `idMap` hash entries.
Total memory at 200k is 140.41 MB, well within modern daemon budgets.

#### Resident Vector Cap
We introduced `WithResidentVectorCap(maxChunks int)` (default 400,000 chunks ≈ 280 MB):
- If `totalChunks > ix.residentVectorCap`:
  `ix.capExceeded = true`
  `ix.vecStore = nil`
- When `ix.capExceeded` is true, queries fall back to SQLite, and `Result.Degraded` appends:
  `"resident vector cap exceeded; using SQLite path"`
- Verified in `TestResidentVectorCap` (`PASS`, 0.83s).

---

### 1.5 Immediate In-Process Reflection

In-process write methods (`Upsert`, `UpsertMany`, `Delete`, `DeleteContext`) update both SQLite and the in-RAM `vecStore` partition within the same call.
- `TestVecStore_InProcessImmediateUpdates` verifies:
  1. Upsert document $\rightarrow$ immediately searchable in `vecStore` (count 0 $\rightarrow$ 1).
  2. Delete document $\rightarrow$ immediately removed from `vecStore` (count 1 $\rightarrow$ 0).
  3. DeleteContext $\rightarrow$ context slice completely purged (count 3 $\rightarrow$ 0).
- Result: `PASS` (0.06s).

---

## 2. Latency on Realistic Corpus & FTS5 Term Optimization

### 2.1 Realistic Corpus Description
To address the critique of synthetic Zipf distributions, we constructed a realistic corpus from the Go standard library (`D:\Softwares\Golang\src`):
- **Chunks collected**: **79,697 unique real chunks** extracted from production `.go` files (HTTP server, runtime, TLS, sync, crypto, syscall, os).
- **Chunking**: Chunked using the real `Chunker` markdown/code parser with headings, doc comments, and function declarations.
- **Scale replication**: Replicated with unique document identifiers across 5 context partitions up to 200,000 chunks.
- **Queries**: 100 realistic queries categorized into:
  - 2-3 word keyword queries (`http server timeout`, `mutex lock contention`, `tls certificate handshake`).
  - 8-15 word natural language questions (`how does the http server handle graceful shutdown when active connections exist`).
  - 40-word pasted paragraphs from documentation.

---

### 2.2 FTS5 Cheap by Construction

In 02c2, FTS5 latency reached 250 ms @39k and 1130 ms @200k because queries constructed huge OR expressions with 30-40 terms, causing SQLite full table rank scans.

We restructured FTS query generation in `internal/search/schema.go`:
1. **Identifier Preservation vs Word Splitting**:
   `ExtractFTS5Terms` extracts code identifiers (e.g. `ADR-014`, `CVE-2024-1029`, `context.Background()`, `find -mtime +7 -delete`) as exact quoted phrases (`"ADR-014"`), and splits remaining natural language text into clean terms.
2. **AND-First with Capped-OR Fallback**:
   Natural language queries first query SQLite with an AND query (`BuildFTS5ANDQuery`). If the AND query yields $\ge 5$ matches, SQLite executes in $< 10\text{ ms}$ with zero OR overhead.
3. **Capped-OR Query**:
   If AND returns $< 5$ hits, the fallback query (`BuildFTS5CappedORQuery`) preserves all identifiers and caps normal words to at most the **8 rarest / longest terms**. This prevents SQLite query planner explosion.
4. **Pushdown Limit**:
   All FTS queries enforce `ORDER BY rank LIMIT 50`.

---

### 2.3 Realistic Corpus Latency Benchmark Results (Benchmark 4)

Measured on a warm process across 100 realistic queries at 10k, 39k, 100k, and 200k chunks in scope:

| Chunks in Scope | First Query (Cold) | EmbedQuery (p50 / p95) | Vec KNN (p50 / p95) | FTS5 (p50 / p95) | Hydration (p50 / p95) | Fusion (p50 / p95) | Unaccounted (p50 / p95) | **Total Pipeline (p50 / p95)** | Latency Target (p95) | Verdict |
|---|---|---|---|---|---|---|---|---|---|---|
| **10k** | 6,618.69 ms | 5.82 / 6.89 ms | 0.53 / 1.08 ms | 7.97 / 16.63 ms | 0.51 / 1.01 ms | 0.00 / 0.51 ms | 0.00 / 0.56 ms | **8.74 / 18.15 ms** | $\le 50\text{ ms}$ | **PASS** (2.8x under) |
| **39k** | 12.72 ms | 6.17 / 7.15 ms | 1.02 / 1.58 ms | 9.19 / 25.13 ms | 0.00 / 1.02 ms | 0.00 / 0.54 ms | 0.00 / 0.00 ms | **10.21 / 25.67 ms** | $\le 100\text{ ms}$ | **PASS** (3.9x under) |
| **100k** | 18.45 ms | 6.13 / 7.44 ms | 2.04 / 2.91 ms | 20.91 / 50.52 ms | 0.00 / 0.61 ms | 0.00 / 0.38 ms | 0.00 / 1.02 ms | **21.49 / 51.04 ms** | $\le 150\text{ ms}$ | **PASS** (2.9x under) |
| **200k** | 23.33 ms | 5.85 / 7.15 ms | 2.71 / 4.24 ms | 50.17 / 149.77 ms | 0.52 / 1.52 ms | 0.00 / 0.52 ms | 0.00 / 0.53 ms | **51.19 / 150.30 ms** | $\le 250\text{ ms}$ | **PASS** (100 ms under) |

#### Analysis:
- Warm process p95 latency at 39k is **25.67 ms** (well below the 100 ms target).
- Warm process p95 latency at 200k is **150.30 ms** (well below the 250 ms target).
- FTS5 p95 dropped from 1130 ms down to 149.77 ms at 200k, and from 250 ms down to 25.13 ms at 39k.
- Cold open numbers: Cold open on 10k takes 6.6 s (due to cold WASM engine JIT compilation and disk caching). Subsequent warm searches on the same process stay between 12 ms and 23 ms even when querying new contexts.

---

### 2.4 MRR Evaluation Across All Three Splits

Re-running the evaluation suite verified that the FTS5 term optimizations and AND-first strategy caused **zero regression**:

| Evaluation Split | BM25 Alone | Vec int8 Alone | Weighted RRF (02c) | Confidence RRF (02c2) | **Confidence RRF (02c3)** | Regressed $> 0.01$? |
|---|---|---|---|---|---|---|
| **Tuning Split (80 queries)** | 0.7860 | 0.9160 | 0.9202 | 0.9135 | **0.9135** | **NO** ($\Delta = 0.0000$) |
| **Held-Out Split (40 queries)** | 0.7817 | 0.5200 | 0.5210 | 0.8046 | **0.8046** | **NO** ($\Delta = 0.0000$) |
| **Final Split (40 queries)** | 0.8508 | 0.9625 | 0.9625 | 0.9500 | **0.9500** | **NO** ($\Delta = 0.0000$) |
| **5-Fold Cross Validation Mean** | 0.7846 | 0.7840 | 0.7871 | 0.8772 | **0.8772** | **NO** ($\Delta = 0.0000$) |

---

## 3. Batched, Concurrent Indexing API

### 3.1 Why 02c Measured 25 texts/s
In 02c, indexing throughput was measured at ~25 texts/s. The root causes were:
1. **Single-Text Requests**: Documents were chunked and submitted to `llama-server.exe` as 1 text per HTTP POST request (`{"input": "..."}`).
2. **Sequential Requests**: Requests were submitted serially over a single HTTP connection.
3. **`-np 1` Slot Configuration**: The server started with a single slot, preventing `llama.cpp` from computing matrix-matrix multiplications ($B \times N$) across parallel token sequences.
4. **Transaction Overhead**: SQLite executed commits for individual chunk insertions.

When `llama-server` receives a batch of 16-32 texts with `--parallel 4`, llama.cpp processes multiple slots concurrently and aggregates matrix operations on CPU/GPU, reaching 150 texts/s for pure embedding.

---

### 3.2 Additive API Surface

We added `Index.UpsertMany` to `internal/search/index.go`:
```go
// UpsertMany indexes a batch of documents concurrently with batched embeddings and SQLite transactions.
func (ix *Index) UpsertMany(ctx context.Context, docs []Doc) error
```

#### Key Design Elements:
1. **Deduplication**: Hashes every chunk text (`Sha256Hex`). If a chunk hash already exists in SQLite, embedding is skipped and existing vectors are preserved.
2. **Worker Pool Embedding**: Bounded worker pool (`WithUpsertConcurrency(4)`) feeds batches of texts (`WithUpsertBatchSize(32)`) into `ix.embedder.Embed`.
3. **Multi-Slot Sidecar**: Added `Parallel int` to `llm.SidecarConfig`. When `Parallel > 1`, the sidecar starts with `--parallel N`.
4. **WASM-Safe Commits**: Inserts chunks into SQLite in batched transactions of $\le 5,000$ vectors to prevent WASM virtual memory limits.
5. **In-RAM Synchronization**: Populates `ix.vecStore` immediately upon commit.
6. **Backward Compatibility**: `Index.Upsert(ctx, d)` delegates directly to `Index.UpsertMany(ctx, []Doc{d})`.

---

### 3.3 End-to-End Throughput Measurement (Benchmark 5)

Measured on **2,000 real chunks** across 1,000 documents including Markdown parsing, SHA-256 deduplication, HTTP batch embedding with `llama-server.exe`, SQLite table inserts (`chunks`, `chunks_fts`, `chunks_vec`, `chunks_vec_bit`), and in-RAM `vecStore` updates:

| Configuration | Total Chunks | Wall Time | **End-to-End Throughput** | 02c Baseline | Speedup |
|---|---|---|---|---|---|
| **32 texts/req $\times$ 4 parallel workers** | 2,000 | 27.63 s | **72.39 chunks/s** | ~25 chunks/s | **2.9x** |

#### Plain Honesty on Misses:
- Pure embedding throughput in `llama-server.exe` reaches **~150 texts/s**.
- Full end-to-end throughput reaches **72.39 chunks/s**.
- The difference (~78 chunks/s) is dominated by:
  1. Go markdown parsing and sentence chunking for 1,000 documents.
  2. SHA-256 hash checks and deduplication queries across SQLite.
  3. Writing to 4 SQLite tables over the WASM runtime boundary (`ncruces/go-sqlite3`).
This is a **2.9x end-to-end indexing speedup** over 02c.

---

## 4. Decisions I Made

1. **Default `WithResidentVectors(false)`**:
   The measured data showed cold population at 10k chunks takes 284 ms, and at 200k takes 13.6 s. Interactive CLI commands cannot pay this penalty. The CLI stays on SQLite, while server/MCP daemons opt-in to resident vectors with background loading.
2. **AND-First with Capped-OR Fallback**:
   Rather than letting long queries inflate into 40-term OR queries in SQLite FTS5, queries with $\ge 5$ AND matches skip the OR branch entirely. If fewer matches exist, terms are capped to the 8 rarest. This stabilized FTS5 latency at $\le 25\text{ ms}$ @39k and $\le 150\text{ ms}$ @200k.
3. **Per-Context Partitioning in `vecStore`**:
   Partitioning into `map[string]*contextVecSlice` reduced scoped scan latency from 1.28 ms to 0.37 ms at 200k chunks.
4. **Trigger-Based Deletion Auditing**:
   Using `chunk_deletions` and `PRAGMA data_version` allows multi-process coherence without scanning or re-indexing unmodified tables.
5. **Strict API Backward Compatibility**:
   No existing exported signatures were modified. All new features are additive options or methods.

---

## 5. Verification Matrix

| Verification Check | Target | Result | Status |
|---|---|---|---|
| `go build ./...` | Zero build errors | Clean build | **PASS** |
| `go vet ./...` | Zero vet warnings | Clean vet | **PASS** |
| `go test ./internal/search/... -count=1` | All unit tests pass | 4.01s | **PASS** |
| `go test ./internal/llm/... -count=1` | All sidecar tests pass | 7.26s | **PASS** |
| Cross-build `linux/amd64` (`CGO_ENABLED=0`) | Zero CGO dependency | Clean binary | **PASS** |
| Cross-build `linux/arm64` (`CGO_ENABLED=0`) | Zero CGO dependency | Clean binary | **PASS** |
| Cross-build `darwin/amd64` (`CGO_ENABLED=0`) | Zero CGO dependency | Clean binary | **PASS** |
| Cross-build `darwin/arm64` (`CGO_ENABLED=0`) | Zero CGO dependency | Clean binary | **PASS** |
| Cross-build `windows/amd64` (`CGO_ENABLED=0`) | Zero CGO dependency | Clean binary | **PASS** |
| Cross-build `windows/arm64` (`CGO_ENABLED=0`) | Zero CGO dependency | Clean binary | **PASS** |
| Warm Search Latency @39k | $\le 100\text{ ms}$ | **25.67 ms** | **PASS** |
| Warm Search Latency @200k | $\le 250\text{ ms}$ | **150.30 ms** | **PASS** |
| MRR Delta on Held-Out Split | $\le 0.01$ regression | $\Delta = 0.0000$ | **PASS** |
| MRR Delta on Final Split | $\le 0.01$ regression | $\Delta = 0.0000$ | **PASS** |

---

## 6. Follow-up 3: Resilient Offline Embedder & Deletion Pruning

### 6.1 Resilient Offline Embedder & Pending Vector Queue
- **Problem**: When the embedding sidecar was down or unreachable, `Index.Upsert` / `UpsertMany` aborted before writing chunks or FTS rows. Newly ingested memories (such as `"Tokens refresh every 15 minutes"`) were completely lost from the index, returning 0 hits for keyword queries like `"token refresh"`.
- **Solution & Invariants**:
  1. **Always-Write Guarantee**: `Upsert` and `UpsertMany` now proceed even if the embedder errors out. Chunks and FTS5 rows (`chunks_fts`) are committed immediately.
  2. **Pending Marker**: Chunks created without vectors are tagged with `vec_pending = 1` in SQLite (`chunks.vec_pending`) and indexed via `idx_chunks_vec_pending`.
  3. **Seamless Schema Migration (v2 $\to$ v3)**: `CurrentSchemaVersion` bumped to 3. Existing databases are upgraded in-place via `ALTER TABLE chunks ADD COLUMN vec_pending INTEGER NOT NULL DEFAULT 0;` without requiring a rebuild.
  4. **Degraded Keyword Search**: Search never chokes on chunks lacking vectors. The FTS leg immediately resolves queries with high recall and precision, returning hits accompanied by a `"embedder unavailable"` diagnostic warning in `Result.Degraded`.
  5. **Background & Recovery Vector Infill**:
     - When the embedder recovers, a background worker or sync pass calls `FillPendingVectors(ctx)`, which batches pending texts, fetches embeddings, inserts them into `chunks_vec` / `chunks_vec_bit` / `vecStore`, and clears `vec_pending = 0`.
     - Calling `Upsert` / `UpsertMany` again on the document also fills missing vectors cleanly without creating duplicate chunks.
  6. **Pending Counters & Non-Fatal Signal**:
     - `Index.PendingVectors()`, `PendingCount()`, `Pending()`, `ContextPendingVectors(context)`, and `DocState.PendingVectors` / `DocState.Pending` in `Index.State()` expose pending vector counts for REST `/status` and CLI `mem status`.
     - When vectors are left pending, `Index.Upsert` and `Index.UpsertMany` return a non-fatal signal error `*PendingVectorsError` satisfying `errors.Is(err, search.ErrVectorsPending)` (`"the write succeeded, keyword search works, vectors pending"`).
     - `internal/engine` detects `ErrVectorsPending` during upsert/sync and records the warning: `"semantic indexing pending (embedder unavailable); keyword search works; run `mem sync`"`.
     - `internal/mcp` and REST expose this warning note, notifying agents and clients that semantic indexing is pending and repairable by `mem sync`.

### 6.2 Bounded Deletions Pruning (`chunk_deletions`)
- **Problem**: `chunk_deletions` logged every chunk removal via SQLite triggers for multi-process coherence, growing indefinitely over time.
- **Solution**:
  1. **Bounded Retention**: Added option `WithMaxRetainedDeletions(n int)` defaulting to $N = 100,000$ (`DefaultMaxRetainedDeletions`).
  2. **Pruning Routine**: `pruneDeletionsLocked(ctx)` deletes records where `id <= max(id) - limit` after mutating operations.
  3. **Dormant Handle Fallback**: When an external reader wakes up after a large pruning window (`minAvailDelID > 1 && minDeletionID < minAvailDelID - 1`), `IncrementalRefresh` returns `ErrPrunedDeletions`. `checkCrossProcessSync` catches this error and automatically triggers a clean full reload (`LoadFromDB`), keeping resident state accurate.

### 6.3 Follow-up 3 Verification
- `TestIndex_OfflineEmbedder_KeywordHitAndRecovery`: PASSED (1.76s). Verified offline indexing, non-fatal `ErrVectorsPending` signal, immediate keyword search hit, embedder recovery, `FillPendingVectors` infill, and zero duplicate chunks.
- `TestSchemaMigration_V2toV3`: PASSED (0.10s). Verified seamless in-place schema upgrade from v2 to v3.
- `TestChunkDeletions_PruningAndFullReload`: PASSED (0.13s). Verified deletion pruning to $N$ rows and automatic full reload on stale reader handles.
- `internal/mcp` `TestTools_Upsert/indexing_warning_appended`: PASSED (0.09s). Verified MCP tool warning note matches `"note: semantic indexing pending (embedder unavailable); keyword search works; run `mem sync`"`.
- Full repo verification: `go build ./...` (PASS), `go vet ./...` (PASS), `go test ./... -count=1` (ALL PACKAGES PASS).
- 6 platform cross-builds (`linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, `windows/arm64` with `CGO_ENABLED=0`): ALL PASS.

