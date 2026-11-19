# REPORT-09: Fuzzy / Partial-Word Search Surfaces (Part 2)

**Date**: 2026-11-19  
**Machine**: AMD Ryzen 7 7435HS (8C/16T, up to 4.5 GHz), 24 GB RAM, Windows 11  
**Go Version**: `go version go1.27.0 windows/amd64`  
**Toolchain**: Pure Go SQLite (`ncruces/go-sqlite3` WASM, `CGO_ENABLED=0`), Node.js v22.13.0, Mantine 9 / React 19  
**Scope**: Part 2 — Surfaces (Config, Engine Default, CLI, MCP Server, REST API, Dashboard, Skills/Docs)

---

## 1. Executive Summary

Part 2 delivers all user-facing and agent-facing surfaces for the fuzzy / partial-word search engine implemented in Part 1 (`internal/search/REPORT-09.md`). Fuzzy matching is enabled by default across all surfaces, providing prefix matching and typo tolerance, with full opt-out controls (`--no-fuzzy`, `fuzzy: false`, `search_fuzzy = false`, or the dashboard toggle).

All contract and unit tests pass across Go packages (`internal/config`, `internal/engine`, `internal/cli`, `internal/mcp`, `internal/server`, `skills`) and the web client (54 vitest tests, typecheck, lint, real and mock builds). Real-binary smoke testing confirmed end-to-end functionality using temporary isolated `MEM_CONFIG` configurations.

---

## 2. Changes per Surface

### 2.1 Config + Engine
- **Key**: Added `search_fuzzy` (bool, default `true`) in `internal/config/config.go`.
- Validated via `config.Set` / `config.Get`, included in `Keys()`, persisted in TOML config.
- **Engine Default**: `internal/engine/engine.go` accepts `fuzzyDefault` via `WithFuzzyDefault(bool)`. When `SearchRequest.Fuzzy` is nil, falls back to engine default (configured from `cfg.Fuzzy()`).

### 2.2 CLI (`cmd_memory.go`, `cmd_mcp.go`, `help.go`, `README.md`)
- `mem search --no-fuzzy` & `mem search --fuzzy`: Flags added with mutual exclusion check.
- `--json` output: Non-nil `sources` (`[]string`) and `matched_terms` (`[]string`).
- Human text output: Fuzzy-only hits marked with `~` prefix, matched terms printed as `matched: term1, term2`.
- `mem mcp --no-fuzzy` / `--fuzzy`: Sets the MCP server's default fuzzy mode.
- Help text and CLI README updated with examples and flag documentation.

### 2.3 MCP (`internal/mcp/`)
- `search` tool: Optional `fuzzy` boolean property added to input schema.
- Tool description updated: *"Fuzzy matching is on by default, set fuzzy:false for exact-word precision."*
- Golden schema regenerated: `internal/mcp/testdata/tools.golden.json`.
- Result formatting: Hits annotated with `[fuzzy]` and `[name]` badges alongside `[bm25]` and `[vec]`, and `matched: ...` line.
- Tests: Added `fuzzy_argument_and_marking` in `internal/mcp/server_test.go`.

### 2.4 REST API (`internal/server/`)
- Endpoint: `GET /api/search?fuzzy=0|1` (default 1).
- Non-0/1 values return HTTP 400 with `{ "kind": "invalid", "message": "fuzzy must be 0 or 1" }`.
- Response contract: `Hit.sources` includes `"fuzzy"` and `"name"`, `matched_terms` is always present as a string array.
- Contract tests updated and passing in `internal/server/contract_test.go`.

### 2.5 Dashboard (`web/`)
- **Fuzzy Switch**: Added next to scope controls in `SearchResultsView.tsx`. Default on, persisted in `localStorage` under `mem_search_fuzzy` with try/catch guard.
- **Spotlight Search**: `SearchSpotlight.tsx` reads `mem_search_fuzzy` preference from `localStorage`.
- **Badges & Highlights**: `SearchResultCard.tsx` renders `fuzzy` (orange) and `name` (violet) chips, and uses Mantine `<Highlight highlight={matchedTerms}>` for snippets and document names.
- **MSW Mock**: `mockDb.search` updated in `web/src/mocks/db.ts` to simulate exact token matching, name segment prefix matching, vocabulary typo tolerance (DL distance), and `matched_terms` generation.
- **Tests**: `web/src/features/search/search.test.tsx` verified for prefix match, typo match, fuzzy switch off removing fuzzy-only hits, and matched terms highlighting.

### 2.6 Skills & Docs
- `skills/mem/SKILL.md`: Updated search tool description and CLI reference notes with default fuzzy matching and `fuzzy:false` / `--no-fuzzy` guidance.
- Drift checker: `skills/drift_checker_test.go` updated to recognize the new `fuzzy` argument; passes 100%.

---

## 3. Verification & Test Results

### 3.1 Unit & Contract Tests
- `go test ./internal/config/...`: **PASS**
- `go test ./internal/engine/...`: **PASS**
- `go test ./internal/cli/...`: **PASS**
- `go test ./internal/mcp/...`: **PASS**
- `go test ./internal/server/...`: **PASS**
- `go test ./skills/...`: **PASS** (drift test green)

### 3.2 Frontend Verification
- `npm --prefix web run typecheck`: **PASS** (0 errors)
- `npm --prefix web run lint`: **PASS** (0 errors)
- `npm --prefix web test`: **PASS** (6 files, 54 tests)
- `npm --prefix web run build:real`: **PASS** (built in 2.77s)
- `npm --prefix web run build:mock`: **PASS** (built in 2.56s)

### 3.3 Cross-Compilation (6 Platforms)
- `linux/amd64`: **PASS**
- `linux/arm64`: **PASS**
- `darwin/amd64`: **PASS**
- `darwin/arm64`: **PASS**
- `windows/amd64`: **PASS**
- `windows/arm64`: **PASS**

---

## 4. Real-Binary Smoke Run Transcript

Conducted using clean temporary directory and config (`MEM_CONFIG` pointing to temporary TOML):

```powershell
=== Config get/set ===
true
search_fuzzy = false
false
search_fuzzy = true

=== Upsert ===
amgnbzzegi4c v1 (global/refresh-tokens.md)

=== mem search refre (fuzzy on default) ===
1. readme.md (global/hmob7idtsbmw) score=0.0933 [vec,fuzzy]
   matched: refre, refresh
2. refresh-tokens.md (global/amgnbzzegi4c) score=0.0825 [vec,fuzzy,name]
   matched: refre, refresh
   # Refresh Tokens
   Refresh tokens expire after 30 days.

=== mem search --no-fuzzy refre (CLI flag override off) ===
1. readme.md (global/hmob7idtsbmw) score=0.0909 [vec]
   matched: refre
2. refresh-tokens.md (global/amgnbzzegi4c) score=0.0769 [vec]
   matched: refre
   # Refresh Tokens

=== mem config search_fuzzy false ===
search_fuzzy = false

mem search refre (default from config: false):
1. readme.md (global/hmob7idtsbmw) score=0.0909 [vec]
2. refresh-tokens.md (global/amgnbzzegi4c) score=0.0769 [vec]

mem search --fuzzy refre (CLI flag override on):
1. readme.md (global/hmob7idtsbmw) score=0.0933 [vec,fuzzy]
   matched: refre, refresh
2. refresh-tokens.md (global/amgnbzzegi4c) score=0.0825 [vec,fuzzy,name]
   matched: refre, refresh
```

---

## 5. Unverified Items
- **Live Browser Screenshots**: Skipped per instruction (*"browser screenshots optional, skip if slow"*). All frontend behavior is comprehensively covered by Vitest component and MSW tests.

---

## 6. Follow-up Additions (Items A & B)

### 6.1 Item A: Sidecar Integrity Check False-Positive Fix (`internal/setup/sidecar.go`)
- **Root Cause**: The integrity check previously enforced `info.Size() < minSidecarSize (1 MiB)`. On Windows, the official llama.cpp b12503 `llama-server.exe` launcher is a valid 9,728-byte PE image that dynamically loads the heavy shared libraries (`llama.dll`, `ggml.dll`, `llama-server-impl.dll`) placed beside it.
- **Fix**:
  1. Removed the arbitrary `minSidecarSize` size threshold.
  2. Maintained structural validation of the PE/ELF/Mach-O header for `runtime.GOOS` and `runtime.GOARCH` (rejects 8-byte text stubs like `"fake exe"` and corrupted headers).
  3. Added `HasCompanionLibraries` check to ensure required `ggml`/`llama` shared libraries (`.dll`, `.so`, `.dylib`) exist in the same directory.
  4. Preserved SHA-256 verification against `.mem-release` when a marker is present and specifies a checksum.
- **Testing**: Added `TestRealSizeLauncherPassesAndStubFails` in `internal/setup/sidecar_test.go`, verifying that an 8-byte fake fails and the 9,728-byte launcher with companion libraries passes.
- **Machine Verification**: Verified that `mem status` on this machine now prints:
  `ok          llama-server C:\Users\manua\.mem\bin\llama-server.exe`

### 6.2 Item B: Isolation & Store Integrity
- **Real Store Notice**: Existing leftover data in the user store (`~/.mem/data`) was left intact without modification or deletion per user instructions.
- **Enforcement**: Confirmed all test harnesses and manual runs strictly use temporary directories (`MEM_CONFIG` and `--data-dir`) to prevent any writes to the real store or configuration.

