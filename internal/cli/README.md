# `mem` CLI — machine-readable output

Every command accepts `--json`. When it is on:

- **stdout** carries exactly one JSON value (an object or an array), so it can be
  piped straight into `jq` or a program.
- **stderr** carries the human-readable notes, progress and warnings.
- **errors** still exit non-zero and print one JSON object on stdout:
  `{"error": "<message>", "code": <exit code>, "hint": "<fix>"}`. The same
  message goes to stderr as `mem: <message>`.

Exit codes: `0` ok, `1` error, `2` usage, `3` not found, `4` conflict / version
mismatch.

Timestamps are RFC 3339 in UTC (`2026-11-18T19:50:00Z`). `null` means absent.
Booleans are real JSON booleans; numbers are numbers (not strings).

## Shapes

### `mem version`
```json
{"name":"mem","version":"0.1.0","go":"go1.27.0","os":"windows","arch":"amd64"}
```

### `mem context create`
```json
{"slug":"work","created_at":"2026-11-18T19:50:00Z","expires_at":null,"expires":false}
```
`expires_at` is a timestamp for a temp context and `null` for a permanent one.

### `mem context ls`
```json
[{"slug":"global","memories":1,"created_at":"2026-11-18T19:50:00Z","expires_at":null}]
```

### `mem context rm`
```json
{"deleted":"work","memories":3}
```

### `mem context expiry`
```json
{"slug":"scratch","expires_at":"2026-11-25T19:50:00Z"}
```
`expires_at` is `null` after setting `none`.

### `mem ls`
```json
[{"id":"v3yb2aenoosh","context":"work","name":"auth/token.md","version":1,
  "updated_at":"2026-11-18T19:50:00Z","updated_by":"alice","tags":["auth"]}]
```

### `mem get <name-or-id>`
```json
{"id":"v3yb2aenoosh","context":"work","name":"auth/token.md","body":"...",
 "version":1,"updated_at":"2026-11-18T19:50:00Z","updated_by":"alice","tags":["auth"]}
```

### `mem get` (no arguments)
Mirrors the MCP no-argument get: the default file, the current context's memory
list and every context.
```json
{"default_file":{"id":"...","context":"global","name":"readme.md","body":"...","version":1,"updated_at":"...","updated_by":"mem","tags":["system"]},
 "context":"global",
 "contexts":[{"slug":"global","memories":1,"created_at":"...","expires_at":null}],
 "memories":[{"id":"...","context":"global","name":"readme.md","version":1,"updated_at":"...","updated_by":"mem","tags":["system"]}]}
```
The `memories` entries omit `body` (a list, not a read).

### `mem upsert`
```json
{"id":"v3yb2aenoosh","context":"work","name":"auth/token.md","version":2,
 "updated_at":"2026-11-18T19:50:00Z","updated_by":"alice","tags":["auth"],"warnings":[]}
```
`warnings` lists non-fatal indexing problems (for example a model that is not
running). The write itself succeeded either way; `mem sync` repairs the index.

### `mem delete`
```json
{"deleted":"v3yb2aenoosh"}
```

### `mem search`
```json
{"hits":[{"id":"v3yb2aenoosh","context":"work","name":"auth/token.md",
          "heading":"Token refresh","snippet":"...","chunk":0,
          "score":0.0312,"sources":["vec","bm25"],"matched_terms":["token","refresh"]}],
 "degraded":["embedder unavailable: ..."],
 "took_ms":18.4}
```
`degraded` is absent when both search legs worked. `sources` entries are `bm25`,
`vec`, `rerank`, `fuzzy`, and `name`. Flags include `--no-fuzzy` to disable fuzzy
matching, or `--fuzzy` to force it on. Human output marks fuzzy-only hits with `~`
and shows matched terms.

### `mem sync`
```json
{"total":12,"added":1,"updated":0,"removed":0,"unchanged":11,"indexed":1,"filled":0,"took_ms":42.0}
```
`filled` counts chunks whose vectors were pending (a write landed while the
embedder was down) and were embedded by this pass.

### `mem status`
```json
{"version":"0.1.0","config_path":"...","data_dir":"...","models_dir":"...",
 "bin_dir":"...","port":7777,"embed_model":"...","embed_present":true,
 "embed_problem":"","rerank":false,"rerank_model":"...","rerank_present":false,
 "rerank_problem":"","sidecar":"...","sidecar_ok":true,"sidecar_problem":"",
 "sidecar_asset":"...","sidecar_size":12345678,"needs_reinstall":false,
 "contexts":2,"memories":4,"indexed_docs":4,"in_sync":true,"warnings":[],"error":""}
```
`*_present`/`sidecar_ok` are true only when the file is present **and** passes
the integrity check (sidecar: at least 1 MiB, an executable header for this
OS/arch, and a hash matching the install marker; models: the pinned size and
SHA-256). `*_problem` explains a failure, and `needs_reinstall` is true when
`mem init --force` is the fix.

### `mem config`
All keys: an object of `key -> value` where every value is a string (the same
text `mem config <key>` prints). One key:
```json
{"port":"7777"}
```
Set:
```json
{"key":"port","value":"9090"}
```

### `mem init`
```json
{"config_path":"...","data_dir":"...","models_dir":"...","bin_dir":"...",
 "port":7777,"expiry_days":7,"idle_timeout_seconds":300,"rerank":false,
 "embed_model":"bge-small-en-v1.5-q8_0.gguf","rerank_model":"...",
 "sidecar_path":"...","downloaded":["..."],"skipped":["..."],
 "verified":true,"verify_note":"384-dimensional embedding",
 "next_steps":["..."],"config_saved":true}
```
`--force` reinstalls the sidecar and models even when they look valid.

## Stability

These shapes are the contract the dashboard and MCP layer consume. Fields may be
added; existing names and types do not change without a version bump.

## `mem mcp` is different

`mem mcp` speaks JSON-RPC 2.0 on stdio, not this CLI JSON envelope. stdout
carries protocol messages only, one JSON object per line; logs and errors go to
stderr. `--json` has no effect on it.
