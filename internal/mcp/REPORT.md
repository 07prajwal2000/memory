# 04 — MCP Server (stdio) Implementation Report

## Summary

Implemented the stdio MCP server for `mem` inside `internal/mcp/` conforming fully to JSON-RPC 2.0 and Model Context Protocol specifications (negotiated versions `2025-06-18`, `2025-03-26`, and `2024-11-05`). It exposes the entrypoint `Serve(ctx, in, out, eng, opts...)` with fast initialization, concurrency safety, strict JSON schemas, structured + human-readable tool outputs, panic recovery, context cancellation, and zero external dependencies.

---

## Decision: Hand-written Protocol vs Go SDK

**Decision:** Hand-written JSON-RPC 2.0 over newline-delimited stdio transport (~300 lines in pure Go standard library).

**Reasons:**
1. **Module & Toolchain Incompatibility:** The official SDK (`github.com/modelcontextprotocol/go-sdk` v1.8.0) declares `go 1.25.0` in its `go.mod`, whereas `memory` is on `go 1.23`.
2. **Heavy & Platform-Dependent Dependency Tree:** The SDK pulls in 12+ dependencies including `golang-jwt/jwt/v5`, `golang.org/x/oauth2`, `google/jsonschema-go`, `uritemplate`, and `segmentio/asm` (which uses assembly routines that risk portability on some targets).
3. **Pure Go / Zero-CGO Cross-Compilation:** Hand-writing ensures `CGO_ENABLED=0` builds cleanly across all 6 required target platforms (`linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, `windows/arm64`) with 0 new dependencies in `go.mod`.
4. **Exact Specification Match:** Hand-writing provides exact control over:
   - Fast startup (< 1.5 ms initialize time, beating the < 300 ms ceiling by 200x)
   - Dynamic instruction formatting and exact 16 KiB line-boundary truncation
   - Clean stdin EOF detection without leaking goroutines
   - Cancellation tracking via `notifications/cancelled`
   - Panic recovery converting unexpected panics into `isError: true` tool results
   - `stdout` isolation (protocol messages only; logs directed to `os.Stderr`)

---

## Tool Descriptions

Below are the final tool descriptions exposed to LLM agents in `tools/list`:

### 1. `search`
> Search memories using hybrid search (BM25 keyword + vector similarity). Searches the 'global' context plus the specified context (unless no_global is true, which requires context). Use to discover relevant prior knowledge, architectural decisions, user preferences, or solutions across sessions. Do NOT use search when you already have a memory's id or name (use 'get' instead). Contexts group memories; contexts cannot be created or deleted by agents (the user creates them via the CLI). Example: search(query="JWT auth middleware setup", context="backend").

### 2. `get`
> Retrieve memories or context information. Call with NO arguments to orient yourself: returns the user's global instructions file (readme.md) and all active contexts (permanent/expiring) with memory counts. Call with only 'context' to list that context's memories (name, id, version, size). Call with 'id' or 'name'+'context' (defaults to 'global') to retrieve the full markdown note with metadata headers (id, version, tags). Use 'version' for subsequent safe upserts. Do not pass both 'id' and 'name'. Example: get(context="backend", name="auth/jwt").

### 3. `upsert`
> Create or update a markdown memory note. Use to persist knowledge, decisions, or session logs across sessions. IDs are stable identifiers. Resolution: pass 'id' to update/rename an existing memory; pass 'name' to update-or-create; omit both to create an unnamed memory (<id>.md). Context defaults to 'global'; agents cannot create contexts (user creates them via CLI). Set 'if_version' to avoid overwriting concurrent edits (fetch version first with 'get'). Set 'append: true' to add lines to the end of a log. Example: upsert(context="backend", name="auth/jwt", body="Use RS256 for tokens.", tags=["auth","jwt"], if_version=2).

### 4. `delete`
> Delete a memory permanently by its stable 'id'. Use only when the user explicitly requests deletion or when a memory is completely obsolete. Prefer 'upsert' to correct, update, or supersede information rather than deleting. Contexts and the default instructions file ('global/readme.md') cannot be deleted. Memory IDs can be found via 'search' or 'get'. Example: delete(id="mem_01h2x3y4z5").

---

## Example Transcripts (Raw JSON Lines)

### 1. `initialize`
**Request:**
```json
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"claude-code","version":"1.0.0"}}}
```
**Response:**
```json
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{"listChanged":false}},"serverInfo":{"name":"mem","version":"0.1.0"},"instructions":"Persistent memory shared across your sessions, stored locally.\nTools: search, get, upsert, delete. The text below is the user's own global instructions file; follow it.\n\n# Memory\n\nThis store keeps long-term memory for AI agents as markdown files. Every\nmemory lives in a context; the reserved context `global` is always\navailable and always searched.\n\n## Using it\n\n- `search` finds memories by meaning and by keyword. It searches the\n  current context plus `global` unless you opt out.\n- `get` with no arguments returns this file and the list of contexts.\n- `get` with a context lists that context's memories; with an id or name\n  it returns one memory.\n- `upsert` creates or updates a memory and returns its id. Pass `append`\n  to add to the end instead of replacing, and `if_version` to avoid\n  overwriting someone else's change.\n- `delete` removes a memory by id.\n\n## Conventions\n\n- One topic per memory, short and specific: a decision, a preference, a fact\n  about a codebase.\n- Name memories by subject (`auth/token-refresh.md`) so they are easy to\n  find; unnamed memories are stored under their id.\n- Record *why* something is true, not just what.\n- Update an existing memory instead of adding a near-duplicate.\n"}}
```

### 2. `search`
**Request:**
```json
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search","arguments":{"query":"RS256 JWT tokens","context":"backend"}}}
```
**Response:**
```json
{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"1. backend/auth.md (id: mem_01h2x, chunk 0, score 0.892)\n# JWT Authentication\nUse RS256 algorithm for signing tokens. Verify bearer headers on incoming requests.\n\ntook_ms: 12"}],"structuredContent":{"degraded":[],"hits":[{"chunk":0,"context":"backend","doc_id":"mem_01h2x","heading":"# JWT Authentication","id":"mem_01h2x","name":"auth.md","score":0.892,"snippet":"Use RS256 algorithm for signing tokens. Verify bearer headers on incoming requests.","sources":["bm25","vec"]}],"took_ms":12}}}
```

### 3. `get` (no arguments: orient call)
**Request:**
```json
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get","arguments":{}}}
```
**Response:**
```json
{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"# Memory\n\nThis store keeps long-term memory for AI agents as markdown files...\n\n---\n## Live Contexts\n- global (permanent, 1 memories)\n- backend (permanent, 4 memories)\n- tmp-auth (expires in 6d 23h, 2 memories)"}],"structuredContent":{"contexts":[{"created_at":"2026-11-18T19:00:00Z","expires_at":null,"is_global":true,"memory_count":1,"slug":"global"},{"created_at":"2026-11-18T19:05:00Z","expires_at":null,"is_global":false,"memory_count":4,"slug":"backend"},{"created_at":"2026-11-18T19:10:00Z","expires_at":"2026-11-25T19:10:00Z","is_global":false,"memory_count":2,"slug":"tmp-auth"}],"default_file":{"body":"# Memory\n...","context":"global","id":"ygl5oqvxc54l","name":"readme.md","tags":["system"],"updated_at":"2026-11-18T19:00:00Z","updated_by":"mem","version":1}}}}
```

### 4. `upsert` (version conflict)
**Request:**
```json
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"upsert","arguments":{"name":"auth.md","body":"new conflicting body","if_version":1}}}
```
**Response:**
```json
{"jsonrpc":"2.0","id":4,"result":{"content":[{"type":"text","text":"version conflict: memory is currently at version 2 (expected 1); call 'get' to inspect the latest content, merge your changes, and retry"}],"isError":true}}
```

---

## `initialize` Startup Timing

Measured under `go test ./internal/mcp`:
- `initialize` with `"2024-11-05"`: **1.05 ms**
- `initialize` with `"2025-03-26"`: **0.52 ms**
- `initialize` with `"2025-06-18"`: **0.58 ms**
- `initialize` fallback: **0.53 ms**

All measured startup response latencies are **< 1.5 ms**, well under the **< 300 ms** threshold. The engine is lazily initialized and never spawns sidecar processes for initialization or tool listing.

---

## CLI Wiring Instructions (for Prompt 03 / CLI Agent)

To wire the `mem mcp` command in `internal/cli` / `cmd/`, the CLI agent adds:

```go
package cli

import (
	"context"
	"os"

	"memory/internal/engine"
	"memory/internal/mcp"
)

func runMCP(ctx context.Context, eng *engine.Engine, version string) error {
	defer eng.Close()

	return mcp.Serve(
		ctx,
		os.Stdin,
		os.Stdout,
		eng,
		mcp.WithServerVersion(version),
	)
}
```

Key requirements for the caller:
1. Pass `os.Stdin` as `in` and `os.Stdout` as `out`.
2. Do NOT write anything to `os.Stdout` (logs must go to `os.Stderr`).
3. Cleanly close the engine (`defer eng.Close()`) after `Serve` exits on EOF.

---

## Verification Summary

1. **Unit & Protocol Tests (`server_test.go`)**:
   - `TestProtocol_Initialize_Versions_And_Timing`: version negotiation + startup time < 300 ms.
   - `TestProtocol_Instructions`: header, default file, >16 KiB truncation, and fallback.
   - `TestProtocol_ToolsList_Golden`: matches golden file `testdata/tools.golden.json`.
   - `TestProtocol_Ping`: returns `{}`.
   - `TestProtocol_UnknownMethod`: returns `-32601 Method not found`.
   - `TestProtocol_MalformedJSONLine`: returns `-32700 Parse error` and keeps server running.
   - `TestProtocol_CancelNotification`: in-flight context cancellation.
   - `TestProtocol_CleanEOFExit`: exits with `nil` error on stdin EOF.
   - `TestProtocol_PanicRecovery`: converts panics into `isError: true` tool results.
   - `TestProtocol_NoStrayStdoutWrites`: verifies every written byte is valid JSON-RPC.
   - `TestProtocol_ConcurrentCalls`: 20 simultaneous calls handled concurrently.
   - `TestTools_Search`: happy path, zero hits, degraded notice, parameter validations.
   - `TestTools_Get`: no-args orientation, only context listing (>200 truncation), by ID/name, large body (>200 KiB truncation), mutual exclusion.
   - `TestTools_Upsert`: create unnamed/named, update, rename, append, version conflict, unknown context, oversize body (>1 MiB), string length checks, indexing warning note, and `updated_by` propagation.
   - `TestTools_Delete`: ID deletion, default file protection, not found error.
   - `TestTools_RoundTrip`: written memories are searchable via deterministic vector/BM25 index.

2. **Real OS Pipe Smoke Test (`cmd_smoke_test.go`, tag `smoke`)**:
   - Runs full handshake and tool sequence over real OS pipes (`os.Pipe`).
   - Passes with `go test -v -tags=smoke -run TestCmdSmokeOverOSPipe ./internal/mcp`.

3. **Cross-Compilation (`CGO_ENABLED=0`)**:
   Passed for all 6 target architectures:
   - `linux/amd64`
   - `linux/arm64`
   - `darwin/amd64`
   - `darwin/arm64`
   - `windows/amd64`
   - `windows/arm64`

4. **Dependencies**:
   - Zero additions to `go.mod` or `go.sum`. Standard library only.

---

## Unverified

1. **External Third-Party MCP Inspector**: `npx --no-install @modelcontextprotocol/inspector` was tested, but returned missing package error because the inspector is not cached/installed locally offline in this environment. As instructed, running against a third-party CLI was therefore omitted.
2. **Real Model Sidecar**: The test suite uses the injected `fakeEmbedder` per specification so as not to consume CPU resources or skew concurrent benchmark tasks run by other agents (`t23`).
