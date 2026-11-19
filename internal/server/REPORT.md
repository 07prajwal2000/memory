# 05b — Dashboard Server & `mem server` Implementation Report

## Executive Summary

This report documents the implementation of the `internal/server` package, `web/embed.go`, and `internal/cli/cmd_server.go` for the local memory service (`mem`), adhering to `.prompts/05b-server.md`, `.prompts/_design.md`, and the REST API specification in `.prompts/_api.md`.

All REST API endpoints, SPA asset serving, security middleware, contract tests, cross-compilation targets (`CGO_ENABLED=0`), and end-to-end browser workflows (visual verification using Chrome DevTools MCP) have been completed and verified.

---

## 1. REST API Endpoints Status Table

All endpoints strictly adhere to `.prompts/_api.md` (including amendments). All request payloads enforce `dec.DisallowUnknownFields()` and a 1 MiB body overhead ceiling. Error responses follow the unified `ApiError` format (`{"error":{"kind":"...","message":"...","details":{...}}}`).

| Method | Endpoint | Status | Description & Implementation Details |
| :--- | :--- | :--- | :--- |
| `GET` | `/api/status` | **200 OK** | Reports binary version, uptime, data directory, `default_temp_expiry_days`, document & vector counts, `sync_status` (`in_sync` or `pending`), and model statuses (`stopped`, `starting`, `ready`, `error`, `disabled`). Verified **never** starts or spawns sidecars. |
| `GET` | `/api/contexts` | **200 OK** | Lists all active contexts (`global`, persistent, and temporary) with expiration timestamps, memory counts, and default file metadata. Expired contexts are excluded. |
| `POST` | `/api/contexts` | **201 Created** | Creates a named or auto-generated temporary context (`tmp-xxxx`). Supports custom `expiry_days`. Rejects reserved names or duplicates (`409 conflict`/`exists`). |
| `GET` | `/api/contexts/{slug}` | **200 OK** | Retrieves metadata for a specific context. Expired or nonexistent contexts return `404 not_found`. |
| `PATCH`| `/api/contexts/{slug}` | **200 OK** | Updates expiry duration (`expiry_days: null` for permanent, or integer > 0). Rejects attempts to modify `global` (`403 reserved`). |
| `DELETE`| `/api/contexts/{slug}` | **204 No Content** | Deletes context and all contained memories. Rejects attempts to delete `global` (`403 reserved`). |
| `GET` | `/api/contexts/{slug}/memories` | **200 OK** | Paged, filtered listing. Supports `q` (case-insensitive substring in SQL), `sort` (`name` or `updated`), `limit` (default 200, max 1000), `offset`. Returns `total` and array of `MemorySummary` items with `size` in bytes. Excludes bodies. |
| `GET` | `/api/memory` | **200 OK** | Retrieves a single memory by `id` (cross-context lookup across live contexts) or `(context, name)`. Returns `404 not_found` if expired or missing. |
| `PUT` | `/api/memory` | **201 / 200** | Creates (201) or updates (200) a memory. Supports `if_version` optimistic locking (returns `409 conflict` with `details.current_version`). Surfaces engine index warnings in top-level `index_warning` string. |
| `DELETE`| `/api/memory` | **204 No Content** | Deletes a memory by `id` or `(context, name)`. Protected default files (e.g. `global/readme.md`) reject deletion with `403 reserved`. |
| `GET` | `/api/default` | **200 OK** | Retrieves the context default file (`readme.md`). Supports `context` query param. Returns `404 not_found` if not yet created. |
| `GET` | `/api/search` | **200 OK** | Hybrid keyword + vector search via `engine.Search`. Includes hit scores, snippets, query timing `took_ms`, and `degraded` array when models are unavailable. |
| `POST` | `/api/sync` | **200 OK** | Runs `engine.Sync()` with concurrency locking (`syncMutex`). Concurrent callers receive `503 unavailable` ("sync already running"). Returns `indexed` count and `duration_ms`. |
| `*` | `/api/*` (unknown) | **404 Not Found** | Returns structured JSON `ApiError` (`kind: "not_found"`). |
| `GET` | `/` and non-API paths | **200 OK / 404** | Serves static assets from embedded FS with SPA fallback to `index.html` for client routes; real missing static files return 404. |

---

## 2. Security Decisions & Content Security Policy (CSP)

### 2.1 Host Header Validation (DNS Rebinding Defense)
- **Rules**: Binds to `127.0.0.1` by default. Requests must present a `Host` header matching `127.0.0.1`, `localhost`, `[::1]` (with or without port), or an entry in `Options.AllowedHosts`.
- **Enforcement**: Middleware validates before routing. Violations return HTTP `403 Forbidden` with `ApiError` (`kind: "invalid"`).

### 2.2 Same-Origin Validation (CSRF Defense)
- **Rules**: State-changing requests (`POST`, `PUT`, `PATCH`, `DELETE`) with an `Origin` header must match the request `Host`.
- **Enforcement**: Cross-origin requests are rejected with HTTP `403 Forbidden` (`cross-origin request rejected`). Missing `Origin` headers (e.g. CLI or curl) are permitted.

### 2.3 Bearer Token Authentication
- **Rules**: If configured (`--token` or `--token-file`), all `/api/*` endpoints require `Authorization: Bearer <token>`.
- **Static Asset Exemption**: Static dashboard assets and `index.html` are served without authentication so the browser can load the SPA. The SPA extracts `?token=` from the initial URL, stores it in `sessionStorage`/`localStorage`, and sends `Authorization: Bearer <token>` on all API requests.
- **Constant-Time Comparison**: Token checks use `crypto/subtle.ConstantTimeCompare` to defend against timing attacks. Unauthorized requests receive `401 Unauthorized` with `WWW-Authenticate: Bearer realm="mem"`.

### 2.4 Content Security Policy (CSP) & Headers
- **Headers Sent on All Responses**:
  - `X-Content-Type-Options: nosniff`
  - `Referrer-Policy: no-referrer`
- **API Cache Control**:
  - `Cache-Control: no-store` on all `/api/*` responses.
- **Static Asset Cache Control**:
  - `Cache-Control: no-cache` for `index.html` and SPA fallback responses.
  - `Cache-Control: public, max-age=31536000, immutable` for `/assets/*` (Vite cache-busted hashed assets).
- **Exact CSP Applied to HTML / SPA Responses**:
  ```http
  Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self' data:; img-src 'self' data: blob:; connect-src 'self'
  ```
  - `default-src 'self'`: Restricts default sources to the same origin.
  - `script-src 'self'`: Only allows bundled JavaScript from the server; forbids `unsafe-eval` and remote scripts.
  - `style-src 'self' 'unsafe-inline'`: Required for Mantine UI and CodeMirror dynamic stylesheet injection and inline styles.
  - `font-src 'self' data:`: Allows embedded icon and web fonts.
  - `img-src 'self' data: blob:`: Allows UI avatars, SVG icons, and rendered preview blobs.
  - `connect-src 'self'`: Restricts all fetch/XHR network requests strictly to the dashboard origin.

### 2.5 Panic Recovery & Logging Safeguards
- Any unhandled panic in HTTP handlers is recovered by `panicRecoveryMiddleware`, logging the stack to the server logger and returning HTTP `500 Internal Server Error` with `{"error":{"kind":"internal","message":"internal server error"}}` without exposing stack traces.
- HTTP log lines never log request bodies or authorization tokens.

---

## 3. Store and Engine Additions

To fulfill requirements without breaking package isolation or modifying `_design.md` constraints, minimal additive methods were introduced:

### 3.1 Store Additions (`internal/store`)
- **Schema Migration 2** ([schema.go](file:///D:/ProgrammingFiles/OSS/memory/internal/store/schema.go)):
  - Added `size INTEGER NOT NULL DEFAULT 0` column to `memories` table.
  - Backfilled existing memories on reconcile and updated upsert statements.
- **`store.ConflictError`** ([store.go](file:///D:/ProgrammingFiles/OSS/memory/internal/store/store.go)):
  - Struct carrying `ID`, `CurrentVersion`, and `TargetVersion`.
  - Implements `Is(ErrConflict)` so existing CLI and engine error handling remains backwards-compatible while enabling the server to extract `current_version`.
- **`store.MemorySummary` & `store.MemoryPage`** ([store.go](file:///D:/ProgrammingFiles/OSS/memory/internal/store/store.go)):
  - `MemorySummary` includes `ID`, `Context`, `Name`, `HasName`, `Version`, `UpdatedAt`, `UpdatedBy`, `Tags`, and `Size`.
  - Excludes `Body` to minimize memory allocation during large listings.
- **`store.ListPage(contextSlug, opts ListPageOptions)`** ([store.go](file:///D:/ProgrammingFiles/OSS/memory/internal/store/store.go)):
  - Performs filtering (`instr(lower(name), lower(?)) > 0`), sorting (`name ASC` or `updated_at DESC`), and pagination (`LIMIT ? OFFSET ?`) entirely in SQLite.
  - Computes `COUNT(*)` in the same transaction.
  - Benchmark tested with 5,000 memories in `internal/store/store_test.go` (`TestListPageLargeDataset5000`), executing in ~3.1 ms.
- **`store.GetByID(id string)`** ([store.go](file:///D:/ProgrammingFiles/OSS/memory/internal/store/store.go)):
  - Resolves a memory across all live contexts. Rejects expired contexts (`ErrNotFound`).
- **`store.UpsertEx(in UpsertInput) (Memory, bool, error)`** ([store.go](file:///D:/ProgrammingFiles/OSS/memory/internal/store/store.go)):
  - Returns a `created bool` indicating whether the memory was newly inserted (201) or updated (200).

### 3.2 Engine Additions (`internal/engine`)
- **`engine.ModelsStatus` & `ModelInfo`** ([status.go](file:///D:/ProgrammingFiles/OSS/memory/internal/engine/status.go)):
  - Added `ModelsStatus()` method returning the current status of the embedding sidecar (`ready`, `starting`, `error`, `stopped`) and reranker (`disabled` or status).
  - Uses tracking embedder/reranker wrappers without invoking `Embed()` or starting sidecars, preventing cold-start latency when opening the `/status` page.

---

## 4. Web Dashboard Changes & Embedding

### 4.1 Asset Embedding (`web/embed.go`)
- Created [web/embed.go](file:///D:/ProgrammingFiles/OSS/memory/web/embed.go) using Go 1.16+ `//go:embed all:dist`.
- Exports `Dist() fs.FS` returning the sub-filesystem `dist`.
- Configured `.gitignore` with `!web/dist/index.html` containing a static fallback page so `go build ./...` succeeds on a fresh checkout without Node.

### 4.2 Production Real-Mode Build (`build:real`)
- Updated [web/package.json](file:///D:/ProgrammingFiles/OSS/memory/web/package.json):
  ```json
  "build:real": "tsc -b && vite build --mode real && node -e \"const fs=require('fs');const p='dist/mockServiceWorker.js';if(fs.existsSync(p))fs.unlinkSync(p);\""
  ```
- In [web/src/main.tsx](file:///D:/ProgrammingFiles/OSS/memory/web/src/main.tsx), conditionalized MSW initialization:
  ```ts
  if (import.meta.env.MODE === 'mock') {
    const { worker } = await import('./mocks/browser');
    await worker.start();
  }
  ```
  Vite/Rollup dead-code elimination completely removes `msw` and browser worker scripts from the production bundle.
- Verified that `dist/mockServiceWorker.js` is absent and production JS bundle contains no MSW runtime.

---

## 5. CLI Command Implementation (`internal/cli/cmd_server.go`)

Replaced the stub in [cmd_server.go](file:///D:/ProgrammingFiles/OSS/memory/internal/cli/cmd_server.go) with the full server command:
- **Flags**:
  - `--port N`: Port number (default 7777 or from config).
  - `--host H`: Listen host (default `127.0.0.1`).
  - `--token T`: Bearer token string.
  - `--token-file F`: File containing bearer token.
  - `--open`: Automatically opens the dashboard URL in default browser upon startup.
- **Safety**: Refuses to bind non-loopback host (e.g. `0.0.0.0`) without a token.
- **Port Conflict Handling**: Detects `bind: address already in use` or Windows `WSAEADDRINUSE` (10048), printing a helpful message and alternative port suggestion.
- **Context Sweeping**: Runs `Store.SweepExpired()` immediately on startup and schedules a background ticker every 10 minutes.
- **Graceful Shutdown**: Listens for `os.Interrupt` / `SIGTERM`, shuts down HTTP listener with a 5-second timeout, closes the engine, and exits with code 0.

---

## 6. Test Suite & API Contract Verification

### 6.1 Unit & Integration Tests (`internal/server/server_test.go`)
- `TestContextsEndpoints`: Happy path, listing, deletion, global protection, expiry updating.
- `TestMemoriesCRUDAndConflict`: Version increments, `if_version` optimistic concurrency, `409 conflict` format with `current_version`, default `updated_by: "dashboard"`.
- `TestDefaultFileAndProtection`: `GET /api/default`, deletion protection on `global/readme.md` (`403 reserved`).
- `TestSearchEndpoint`: Query execution, snippet rendering, took_ms timing, degraded status.
- `TestSyncEndpointAndSerialization`: Concurrent `POST /api/sync` serialization and `503 unavailable` rejection.
- `TestStatusEndpointDoesNotStartModel`: Verifies `/api/status` does not invoke model sidecars.
- `TestSecurityHostAndOriginMatrix`: Allowed vs forbidden `Host`, valid vs invalid `Origin` on state-changing methods.
- `TestBearerTokenAuthentication`: Constant-time comparison, header validation, 401 response with `WWW-Authenticate`.
- `TestSecurityHeadersAndCSP`: CSP header validation, `no-store` on API, `nosniff`, `no-referrer`.
- `TestSPAFallbackRouting`: Client route fallback to `index.html`, missing file 404, `/api/` 404 JSON.
- `TestGzipCompression`: Gzip compression of compressible assets with `Accept-Encoding: gzip`.
- `TestBodySizeLimitAndUnknownFields`: 1 MiB payload rejection and `DisallowUnknownFields` enforcement.
- `TestPanicRecovery`: Unhandled panic caught and converted to 500 JSON `internal`.
- `TestGracefulShutdown`: Clean listener termination upon context cancellation.

### 6.2 API Contract Tests (`internal/server/contract_test.go`)
Table-driven assertions validating every endpoint against TypeScript interface definitions in [web/src/api/types.ts](file:///D:/ProgrammingFiles/OSS/memory/web/src/api/types.ts):
- `GET /api/status` $\rightarrow$ `ServerStatus`
- `GET /api/contexts` $\rightarrow$ `Context[]`
- `POST /api/contexts` $\rightarrow$ `Context`
- `GET /api/contexts/{slug}` $\rightarrow$ `Context`
- `PATCH /api/contexts/{slug}` $\rightarrow$ `Context`
- `PUT /api/memory` (create) $\rightarrow$ `Memory`
- `GET /api/contexts/{slug}/memories` $\rightarrow$ `ListMemoriesResponse`
- `GET /api/memory` $\rightarrow$ `Memory`
- `GET /api/default` $\rightarrow$ `Memory`
- `GET /api/search` $\rightarrow$ `SearchResponse`
- `POST /api/sync` $\rightarrow$ `SyncResponse`
- `DELETE /api/memory` $\rightarrow$ HTTP 204
- `DELETE /api/contexts/{slug}` $\rightarrow$ HTTP 204
- `ApiError` format $\rightarrow$ `ApiError`

### 6.3 Test Run Results
```
=== RUN   TestApiContract
--- PASS: TestApiContract (0.88s)
=== RUN   TestContextsEndpoints
--- PASS: TestContextsEndpoints (0.06s)
=== RUN   TestMemoriesCRUDAndConflict
--- PASS: TestMemoriesCRUDAndConflict (0.10s)
=== RUN   TestDefaultFileAndProtection
--- PASS: TestDefaultFileAndProtection (0.06s)
=== RUN   TestSearchEndpoint
--- PASS: TestSearchEndpoint (0.09s)
=== RUN   TestSyncEndpointAndSerialization
--- PASS: TestSyncEndpointAndSerialization (0.06s)
=== RUN   TestStatusEndpointDoesNotStartModel
--- PASS: TestStatusEndpointDoesNotStartModel (0.06s)
=== RUN   TestSecurityHostAndOriginMatrix
--- PASS: TestSecurityHostAndOriginMatrix (0.06s)
=== RUN   TestBearerTokenAuthentication
--- PASS: TestBearerTokenAuthentication (0.10s)
=== RUN   TestSecurityHeadersAndCSP
--- PASS: TestSecurityHeadersAndCSP (0.06s)
=== RUN   TestSPAFallbackRouting
--- PASS: TestSPAFallbackRouting (0.06s)
=== RUN   TestGzipCompression
--- PASS: TestGzipCompression (0.06s)
=== RUN   TestBodySizeLimitAndUnknownFields
--- PASS: TestBodySizeLimitAndUnknownFields (0.06s)
=== RUN   TestPanicRecovery
--- PASS: TestPanicRecovery (0.00s)
=== RUN   TestGracefulShutdown
--- PASS: TestGracefulShutdown (0.06s)
PASS
ok  	memory/internal/server	2.008s
```

---

## 7. End-to-End Real Browser Verification

The real compiled binary (`mem.exe`) and production dashboard bundle (`npm run build:real`) were launched on port 8765 with `--token secret123` and driven through Chrome DevTools MCP.

### Verification Flow & Results
1. **Dashboard Load**:
   - Navigated to `http://127.0.0.1:8765/?token=secret123`.
   - Verified that SPA loaded without console errors.
   - Verified that token from URL was recognized and subsequent API requests passed `Authorization: Bearer secret123`.
2. **Context Creation**:
   - Created context `demo-ctx` with 7 days TTL via UI modal.
   - Verified HTTP `POST /api/contexts` returned 201 Created and page transitioned to `/c/demo-ctx`.
3. **Memory Creation**:
   - Navigated to `/c/demo-ctx/new`, entered name `guide.md`, typed markdown content, and saved with `Ctrl+S`.
   - Verified `PUT /api/memory` returned 201 Created with generated Crockford Base32 ID `wetxqcbdgmqc` (Version 1).
4. **Memory Editing & Version Increment**:
   - Modified document in editor and saved with `Ctrl+S`.
   - Verified version bumped from V1 to V2 (`size: 84 B`).
5. **Provoked Version Conflict**:
   - Kept editor open with uncommitted edits on Version 2.
   - Out-of-band client executed `PUT /api/memory` with `if_version: 2`, bumping database to Version 3.
   - In UI, pressed `Ctrl+S` with stale base version.
   - Received HTTP `409 Conflict` from server with `current_version: 3`.
   - **Conflict Modal Triggered**: UI displayed "Version Conflict Detected" comparing server Version 3 against local draft, with options "Copy my text", "Reload theirs", and "Overwrite with mine".
   - Selected "Reload theirs" and verified server version loaded into editor.
6. **Search Spotlight & Full Search**:
   - Opened search spotlight (`Cmd+K` / search button).
   - Searched for `guide`; verified query dispatch and display of search results.
   - Navigated to full `/search?q=guide` page; verified timing and degraded embedder notice.
7. **System Status Page**:
   - Navigated to `/status`; verified binary version `0.1.0`, data directory, sync status, and model sidecar indicators rendered correctly.
8. **Mobile Viewport (390x844)**:
   - Emulated 390x844 viewport.
   - Verified desktop sidebar collapsed into navigation drawer hamburger button.
   - Verified drawer toggling and responsive document list view.
9. **Clean Server Shutdown**:
   - Sent SIGINT/cancellation to daemon; verified port 8765 released.

### Visual Evidence Artifacts
Screenshots saved under [internal/server/docs/screenshots/](file:///D:/ProgrammingFiles/OSS/memory/internal/server/docs/screenshots/):
- **Desktop System Status**: [desktop_status.png](file:///D:/ProgrammingFiles/OSS/memory/internal/server/docs/screenshots/desktop_status.png)
- **Desktop Markdown Editor (Split View)**: [desktop_editor.png](file:///D:/ProgrammingFiles/OSS/memory/internal/server/docs/screenshots/desktop_editor.png)
- **Version Conflict Modal**: [conflict_dialog.png](file:///D:/ProgrammingFiles/OSS/memory/internal/server/docs/screenshots/conflict_dialog.png)
- **Mobile Responsive Document List (390 px)**: [mobile_list.png](file:///D:/ProgrammingFiles/OSS/memory/internal/server/docs/screenshots/mobile_list.png)
- **Mobile Responsive Drawer Open (390 px)**: [mobile_drawer.png](file:///D:/ProgrammingFiles/OSS/memory/internal/server/docs/screenshots/mobile_drawer.png)

---

## 8. Binary Size Measurements

Measured on Windows `amd64` using `go build ./cmd/mem`:

| Configuration | Binary Size (Bytes) | Binary Size (MiB) | Delta |
| :--- | :--- | :--- | :--- |
| **Without dashboard assets** (dummy empty FS) | 18,929,664 bytes | 18.05 MiB | Baseline |
| **With dashboard assets embedded** (`all:dist`) | 20,718,592 bytes | 19.76 MiB | **+1,788,928 bytes (+1.71 MiB, +9.45%)** |

The embedded web assets (Vite production bundle including HTML, CSS, JS, and Mantine icons) add approximately 1.71 MiB to the standalone executable.

---

## 9. Cross-Compilation Verification (6 Targets)

All 6 required targets cross-compiled cleanly with `CGO_ENABLED=0`:

| Target OS / Arch | Build Command | Output Binary Size | Status |
| :--- | :--- | :--- | :--- |
| `windows/amd64` | `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./cmd/mem` | 20,717,568 bytes | **PASS** |
| `windows/arm64` | `CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build ./cmd/mem` | 19,317,248 bytes | **PASS** |
| `linux/amd64` | `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./cmd/mem` | 20,409,961 bytes | **PASS** |
| `linux/arm64` | `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./cmd/mem` | 19,217,223 bytes | **PASS** |
| `darwin/amd64` | `CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build ./cmd/mem` | 20,675,776 bytes | **PASS** |
| `darwin/arm64` | `CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build ./cmd/mem` | 19,628,434 bytes | **PASS** |

---

## 10. Decisions Made & Notes for Concurrent Agents

1. **`internal/cli/cli_test.go` (`TestStubsExitNonZero`)**:
   - `cli_test.go` was created in prompt 03 with `TestStubsExitNonZero`, which specifically asserts that `server` exits non-zero because it was previously a stub. Now that `mem server` is implemented and functional, calling `cli.Run([]string{"server"})` starts a real server listener.
   - Per prompt boundaries ("other packages may be mid-change by other agents: report failures outside your packages without fixing them"), `cli_test.go` belongs to `cmdc` t24. All other 22 tests in `internal/cli` pass 100% when run with `-skip TestStubsExitNonZero`.
   - **Recommendation for `cmdc` (t24)**: Update `TestStubsExitNonZero` in `internal/cli/cli_test.go` to remove `server` from the stub list now that prompt 05b is complete.
2. **`help.go` server text**:
   - `help.go` currently displays `server [--port N] dashboard server (not available yet)`. The text can be updated to `server [--port N] dashboard server` by the CLI agent (`cmdc` t24).
3. **Empty Fallback for Fresh Checkouts**:
   - `web/dist/index.html` is tracked via `.gitignore` exception `!dist/index.html`, ensuring `go build ./...` works immediately on a clone without running `npm run build:real`.

---

## 11. Follow-up: Tunnel / Proxy Support & Build Isolation

### 11.1 Reverse Proxy & Tunnel Host Support
- **Repeatable `--allowed-host HOST`**:
  - Added repeatable `--allowed-host` flag to `mem server` in [cmd_server.go](file:///D:/ProgrammingFiles/OSS/memory/internal/cli/cmd_server.go).
  - Extended [config.Config](file:///D:/ProgrammingFiles/OSS/memory/internal/config/config.go) with key `allowed_hosts` (`AllowedHosts []string`).
  - Supports setting via `mem config allowed_hosts <host1,host2>` or directly in `config.toml` (`allowed_hosts = ["..."]`).
  - Merges the bind host, config `allowed_hosts`, and CLI `--allowed-host` entries into `server.Options.AllowedHosts`.
- **Token Requirement for Non-Loopback Hosts**:
  - Implemented `isLoopbackHost` checking IPv4/IPv6 loopback addresses (`127.0.0.1`, `localhost`, `::1`, `[::1]`).
  - When the bind host or **any** configured allowed host is non-loopback (e.g. `*.ngrok-free.app`, public IP, reverse proxy domain), `mem server` **refuses to start** without an authentication token (`--token` or `--token-file`), reporting:
    `refusing to start with non-loopback allowed host "my-tunnel.ngrok-free.app" without an authentication token (pass --token or --token-file)`.
- **Tunnel Origin Checking**:
  - Added `sameOriginHost(originHost, reqHost)` in [server.go](file:///D:/ProgrammingFiles/OSS/memory/internal/server/server.go) to compare origin against the request host, transparently handling standard implicit ports (HTTP 80 and HTTPS 443).
  - Allows same-origin state-changing requests through tunnel URLs (e.g. `Host: my-tunnel.ngrok-free.app` with `Origin: https://my-tunnel.ngrok-free.app`).
  - Disallows cross-origin state-changing requests through the tunnel (`403 Forbidden`).
- **Test Matrix (`server_test.go:TestTunnelHostMatrix` & `cmd_server_test.go:TestServerAllowedHostRequiresToken`)**:
  - Allowed tunnel host with token $\rightarrow$ HTTP 200 OK.
  - Allowed tunnel host without token $\rightarrow$ HTTP 401 Unauthorized.
  - Wrong / unauthorized host header $\rightarrow$ HTTP 403 Forbidden (`kind: "invalid"`).
  - Same-origin POST via tunnel host $\rightarrow$ HTTP 201 Created.
  - Cross-origin POST via tunnel host $\rightarrow$ HTTP 403 Forbidden (`cross-origin request rejected`).
  - CLI startup refusal on non-loopback `--allowed-host` or config `allowed_hosts` without token $\rightarrow$ Exits `ExitUsage` (2).

### 11.2 Frontend Build Isolation (`dist` vs `dist-mock`)
- **OutDir Separation**:
  - Updated [web/vite.config.ts](file:///D:/ProgrammingFiles/OSS/memory/web/vite.config.ts):
    `outDir: mode === 'mock' ? 'dist-mock' : 'dist'` for both `build` and `preview`.
  - Updated [web/package.json](file:///D:/ProgrammingFiles/OSS/memory/web/package.json):
    - `build:mock`: `"tsc -b && vite build --mode mock"` (outputs to `web/dist-mock/`).
    - `preview:mock`: `"vite preview --mode mock --outDir dist-mock"`.
    - `build:real`: `"tsc -b && vite build --mode real && node -e \"const fs=require('fs');const p='dist/mockServiceWorker.js';if(fs.existsSync(p))fs.unlinkSync(p);\""` (outputs to `web/dist/`).
    - `preview`: `"vite preview"` (serves `web/dist/`).
  - Added `dist-mock/` to [web/.gitignore](file:///D:/ProgrammingFiles/OSS/memory/web/.gitignore).
- **Verification**:
  - Verified `npm run build:mock` writes to `dist-mock/` and retains `mockServiceWorker.js`.
  - Verified `npm run build:real` writes to `dist/` without `mockServiceWorker.js`.
  - Running mock previews or builds no longer clobbers embedded dashboard assets.
  - Verified `npm run test` in `web/` (all 6 test suites, 50 tests pass).

### 11.3 Concurrent Agent Observation
- During follow-up testing, `internal/search/vec_store.go` was actively edited by concurrent agent `agy t23` (missing `"errors"` import at line 245 for `ErrPrunedDeletions`).
- Following prompt boundaries, `internal/search` was not modified and is reported here for `agy t23`.

