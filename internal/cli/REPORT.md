# 03 — CLI (`mem`) and `mem init`

## What was built

- `cmd/mem` — the binary. Thin: `setup.SetVersion`, `cli.Run(os.Args[1:], cli.DefaultIO(), cli.DefaultEnv())`, `os.Exit`.
- `internal/cli` — every command, the flag dispatcher, exit codes, JSON output, error/hint mapping, the `mem init` command that drives `internal/setup`, and the `mem mcp` command that serves `internal/mcp` over stdio.
- `internal/setup` — platform asset table, resumable verified downloader, archive extraction, and the interactive wizard.
- Additive fields in `internal/config` (see below), three additive options in `internal/engine`, and one engine precedence bug fix found by the real run.

Not in `go.mod`: no new dependencies. stdlib `flag`, `net/http`, `archive/zip`, `archive/tar`, `compress/gzip`, `crypto/sha256`, plus what the repo already required.

## Command table as built

Global flags (`--json`, `--data-dir`, `--config`, `-q`/`--quiet`, `-v`/`--verbose`) are accepted before *or* after the command word.

| Command | Flags | Exit codes | Notes |
|---|---|---|---|
| `mem init` | `--yes --data-dir --models-dir --port --expiry --idle-timeout --with-rerank --no-download --offline-dir` | 0/1 | Wizard; see below |
| `mem context create [slug]` | `--temp --expiry DAYS` | 0/1/2/4 | No slug ⇒ auto `tmp-xxxx` and temp; `--temp` alone ⇒ config default; `--expiry` alone ⇒ temp; explicit `--expiry 0/-n` ⇒ usage |
| `mem context ls` (alias `list`) | — | 0/1 | Table or JSON array |
| `mem context rm <slug>` | `--yes` | 0/1/2/3/4 | Typed-slug confirmation on a TTY, `--yes` otherwise; `global` ⇒ 4 |
| `mem context expiry <slug> <days\|none>` | — | 0/1/2/3/4 | `global` ⇒ 4 |
| `mem ls` | `--context C --limit N` | 0/1/2/3 | Default `global` |
| `mem get [name-or-id]` | `--context C` | 0/1/2/3 | No args ⇒ default file + contexts + that context's memories (mirrors MCP get) |
| `mem upsert [body\|-]` | `--context --name --id --tag (repeat) --append --if-version --by` | 0/1/2/3/4 | Body from the arg, `-`, or piped stdin |
| `mem delete <id>` | — | 0/1/2/3 | Protected default file ⇒ 4 |
| `mem search <query...>` | `--context --no-global --limit --per-doc` | 0/1/2/3 | Prints `note:` lines for engine `Degraded` |
| `mem sync` | — | 0/1 | Progress on stderr, summary on stdout |
| `mem status` | — | 0/1 | Never starts a model |
| `mem config [key] [value]` | — | 0/1/2 | Validated set, atomic save |
| `mem server [--port N]` | `--port --host --token --token-file --open` | 0/1/2 | Implemented by prompt 05 in this file (see note below) |
| `mem mcp` | — | 0/1 | JSON-RPC 2.0 over stdio; stdout is protocol only |
| `mem skill [--install]` (stub) | `--install` | 1 | "not available in this build yet" |
| `mem version` | — | 0 | Human line or JSON |
| `mem help [cmd]` | — | 0 | File `internal/cli/help.go` |

Exit codes are enforced centrally in `exitCodeFor`: `*exitError` carries 2/3/4 directly, and unwrapped store sentinels map `ErrNotFound`→3, `ErrConflict`/`ErrExists`/`ErrReserved`→4, `ErrInvalid`→2. Every error prints one line plus a `hint:` (suppressed when the message already contains the fix, e.g. "create it with `mem context create`").

`mem server` was a stub when 03 shipped; the prompt-05 agent has since replaced `internal/cli/cmd_server.go` with the real dashboard command (`internal/server` + `web.Dist()`), so it is no longer a stub and my stub test was narrowed accordingly. I left their implementation untouched.

Two commands avoid the model by design: `delete` and `status` open the engine with `engine.WithoutInitialSync()`. The rest of the non-embedding surface (`context *`, `ls`, `get`, `config`) never opens the engine at all — only `store.Open`.

## `mem init`

1. Prompts (skipped with `--yes` or a non-TTY stdin) for data folder, models folder, port, temp expiry days, sidecar idle seconds, then the reranker question, which explains that it defaults to off because the tiny model measured worse than plain fusion.
2. Saves the config, creates both dirs, opens the store once (seeds `global` + `global/readme.md`).
3. Installs the sidecar: downloads the pinned llama.cpp asset for the current platform, verifies size + SHA-256, extracts only `llama-server` and the `ggml*`/`llama*`/`mtmd*` libraries plus `libomp.dll`, flattens them into `<models_dir>/../bin`, and writes a `.mem-release` marker so a re-run skips the work. `--offline-dir` copies from a local folder instead (either the archive or an already-unpacked `llama-server`).
4. Installs the models into `models_dir`, skipping any whose size and hash already match.
5. Verifies by running `llama-server --version`, then one embedding round-trip through the engine (90 s timeout); prints the precise failure if either step fails. A failed verification is a warning, not a failed init — everything else is already installed. On the real E2E this step passed, reporting `384-dimensional embedding`.
6. Prints next steps: `claude mcp add mem -- mem mcp`, the generic `mcpServers` JSON, `mem server --port N`, and the not-yet-available `mem skill --install`.
7. Idempotent: existing config values are the defaults, valid files are skipped, never deletes data. Changing `--data-dir` writes the new path and states plainly that existing data is not moved (the old files stay where they were).

`sidecar_path` is written to the config when the sidecar is installed, so later commands find it through `config.SidecarPath`; `DetectSidecar` also checks `<models_dir>/../bin` and `PATH`.

## Download pins

Asset names were checked against the GitHub releases API for tag `b12503`, and every SHA-256 below was **computed locally from my own download** — they are not upstream-published checksums.

| Target | Asset | Size | SHA-256 |
|---|---|---|---|
| windows/amd64 | `llama-b12503-bin-win-cpu-x64.zip` | 25,521,102 | `64bee9b6fece8e66c665c40290cbb84706af23c670041d024b56a71a39859d77` |
| windows/arm64 | `llama-b12503-bin-win-cpu-arm64.zip` | 13,838,966 | `56df5e64f4763ff9a8ca5e1a5b54a5839b4becb99e0e2f1ba8ad80dbf50faf05` |
| linux/amd64 | `llama-b12503-bin-ubuntu-x64.tar.gz` | 23,784,463 | `0d585f8a78ffdfdc4f6303a089aaa1379c15323b33483b6548c85c774a376a9d` |
| linux/arm64 | `llama-b12503-bin-ubuntu-arm64.tar.gz` | 19,377,874 | `e2b566c5368e2e781b73e5c6222f9512c9e6bc563cf4863a42b675356fb6df6a` |
| darwin/amd64 | `llama-b12503-bin-macos-x64.tar.gz` | 13,055,174 | `4c42388726490b1ed7864dc621dd29526f0ced3d617a96c5ac111287a7a68ba5` |
| darwin/arm64 | `llama-b12503-bin-macos-arm64.tar.gz` | 17,282,183 | `e88d7e91198d0de6ceb3d33711ac01ee0d5b519cd9ba4e81854e3bba84f22214` |

Base URL: `https://github.com/ggml-org/llama.cpp/releases/download/b12503/<asset>`.

| Model | URL | Size | SHA-256 |
|---|---|---|---|
| `bge-small-en-v1.5-q8_0.gguf` | `https://huggingface.co/CompendiumLabs/bge-small-en-v1.5-gguf/resolve/main/bge-small-en-v1.5-q8_0.gguf` | 36,806,944 | `ec38e8da142596baa913124ae50550de284b6916bf59577ef2f0cb9660c2f514` |
| `jina-reranker-v1-tiny-en-Q8_0.gguf` | `https://huggingface.co/gpustack/jina-reranker-v1-tiny-en-GGUF/resolve/main/jina-reranker-v1-tiny-en-Q8_0.gguf` | 36,676,960 | `0defc1f8a1f4dd22183124a2a25a97765603e5a9e42258046c9b2c8a26d1f553` |

The embedding file matches the one already in `spike/models` byte for byte (same size).

Downloader behavior: `Range`-based resume into `<dest>.part`, SHA-256 + size verification, atomic rename, `.part` deleted on a hash mismatch so the retry is clean, 3 attempts with linear backoff, a progress line on stderr (percent + speed), `HTTPS_PROXY` honored by stdlib, `User-Agent: mem/<version>`. No other CLI code touches the network.

## Config changes (all additive)

`internal/config` gained `idle_timeout_seconds` (default 300), `sidecar_path` (default empty), `embed_model` (default `bge-small-en-v1.5-q8_0.gguf`), and a default for `rerank_model` (`jina-reranker-v1-tiny-en-Q8_0.gguf`). Also added: `Defaults` fills the new fields, `Load` back-fills them when absent from the file, `BinDir()`, `Keys()`, `Set`/`Get` (validated), `LoadFile`/`SaveFile` for `--config`, and the extended `String()`. Defaults preserve prior behavior: rerank stays off, and an old config file loads with the model names filled in.

## Additive changes outside my packages

Four, all listable:

1. `engine.WithIdleTimeout(d)` — the config's `idle_timeout_seconds` has no effect otherwise (`llm.SidecarConfig.IdleTimeout` is per-sidecar). Reason: `mem init` asks for it, so it must reach the sidecar.
2. `engine.WithoutInitialSync()` — `delete`, `status` and `mcp` must not start a model on open. `Open` runs `Sync`, which embeds on a fresh store; this option lets the caller skip that while keeping hooks attached.
3. `engine` now reads `cfg.SidecarPath` and `cfg.EmbedModel` in `buildClients` (previously only options decided), so `mem init` can persist them. Option values still win.
4. **Bug fix in `engine`:** `defaultOptions()` used to set `binary: "llama-server"`, which shadowed a configured `cfg.SidecarPath` in `buildClients`. The default is now empty, so precedence is `WithSidecarBinary` › `config.SidecarPath` › PATH. Found by the real E2E; regression test `TestConfigSidecarPathWinsOverDefaultBinary`.

No changes to `internal/search`, `internal/llm`, `internal/mcp`, `web/`, or `spike/`.

## Startup time (measured)

Warm: 11 runs each, median of 11, after `mem init --no-download` on a temp dir.

| Command | median | min | max |
|---|---|---|---|
| `mem version` | 12.8 ms | 11.6 | 17.4 |
| `mem config` | 13.1 ms | 12.2 | 21.9 |
| `mem context ls` | 42.1 ms | 40.3 | 48.7 |
| `mem ls` | 43.4 ms | 42.6 | 46.6 |
| `mem get a.md --context work` | 44.9 ms | 42.0 | 58.5 |
| `mem status` | 67.6 ms | 65.3 | 75.6 |

All under the 150 ms budget. The one-time cost on a machine's first ever run is higher: compiling the 1.5 MB SQLite Wasm module takes ~750 ms. `internal/cli/runtime_cache.go` points wazero at a persistent compilation cache (`<UserCacheDir>/mem/wazero`), which drops that to ~40 ms on every later process. Without the cache, `context ls` measured ~760 ms every time; that is why the cache exists. If the cache directory cannot be created, startup falls back to the in-memory compile and still works.

## Manual transcripts (real binary, temp data dir)

```
$ mem init --yes --no-download --data-dir .../data --models-dir .../models

mem is set up.
  data    C:\Users\...\memshow\data
  models  C:\Users\...\memshow\models
  config  C:\Users\...\memshow\config.toml

next steps:
  Claude Code:  claude mcp add mem -- mem mcp
  generic MCP:  {"mcpServers":{"mem":{"command":"mem","args":["mcp"]}}}
  dashboard:    mem server --port 7777
  agent skill:  mem skill --install (coming in a later build)
  restart your agent so it picks up the MCP server
```
(stderr: `warning: llama-server was not installed; indexed search will stay unavailable until `mem init` succeeds` — expected with `--no-download`)

```
$ mem context create work
created context work

$ mem context create
created context tmp-kln7 (expires 2026-11-25 19:50)

$ mem context ls
SLUG                     MEMORIES  CREATED           EXPIRES
global                          1  2026-11-18 19:50  never
tmp-kln7                        0  2026-11-18 19:50  2026-11-25 19:50
work                            0  2026-11-18 19:50  never
```

```
$ mem upsert --context work --name auth/token.md --tag auth --by alice "Tokens refresh every 15 minutes via the zephyr flow."
v3yb2aenoosh v1 (work/auth/token.md)

$ "Deploys run from the release branch only." | mem upsert --context work --name deploy.md --tag ops
rugp522yuzc3 v1 (work/deploy.md)

$ mem ls --context work
NAME                                     ID           VERSION  UPDATED
deploy.md                                rugp522yuzc3       1  2026-11-18 19:50
auth/token.md                            v3yb2aenoosh       1  2026-11-18 19:50

$ mem get auth/token.md --context work
---
# auth/token.md (v3yb2aenoosh, v1, updated 2026-11-18 19:50 by alice)
# tags: auth
---
Tokens refresh every 15 minutes via the zephyr flow.
```

```
$ mem upsert --context work --name auth/token.md --if-version 2 "attempt"
mem: conflict: memory "v3yb2aenoosh" is at version 1, not 2
hint: fetch the memory again, then retry with the current --if-version
exit=4

$ mem status
mem 0.1.0
config      C:\Users\...\memshow\config.toml
data dir    C:\Users\...\memshow\data
contexts    3
memories    3
indexed     0 documents (out of sync, run `mem sync`)
missing      embedding   ...\models\bge-small-en-v1.5-q8_0.gguf
missing      llama-server (not found)
reranker    off (opt-in)
```

```
$ mem search tokens --context work --json
{"degraded":["embedder unavailable: sidecar embed url: sidecar start llama-server: exec: \"llama-server\": executable file not found in %PATH%"],"hits":[],"took_ms":14.608}
```
With no model installed the write still succeeded and search degrades honestly: keyword-only, empty here because the index has no documents yet (`mem sync` fills it once a model exists). The one-line `indexed search is not set up yet; run `mem init`` note appears once per command.

```
$ mem context rm global --yes
mem: reserved: context "global" cannot be removed
hint: the global context always exists and cannot be removed or expired
exit=4
```

`mem config port 9090` followed by `mem config port` prints `9090`, and the file reloads to 9090 (verified in tests).

## Real end-to-end run with downloads and real models

`hc get t23` showed the other agent idle (no measurable CPU), so the previously skipped E2E was run. Everything below used a throwaway directory under the OS temp folder (`MEM_CONFIG` pointed at it, `--data-dir`/`--models-dir` explicit); `~/.mem` was never touched.

### `mem init --yes` (real downloads)

The assets were fetched over the network and hash-verified against the pins in `internal/setup/assets.go`. Total wall time **5.8 s** (24.3 MB llama.cpp zip at ~39 MB/s, 35.1 MB GGUF at ~7–11 MB/s), then the verification ran:

```
  ok  model server responds (384-dimensional embedding)

mem is set up.
  data    C:\Users\...\meme2e\data
  models  C:\Users\...\meme2e\models
  server  C:\Users\...\meme2e\bin\llama-server.exe
  config  C:\Users\...\meme2e\config.toml

next steps:
  Claude Code:  claude mcp add mem -- mem mcp
  generic MCP:  {"mcpServers":{"mem":{"command":"mem","args":["mcp"]}}}
  dashboard:    mem server --port 7777
  agent skill:  mem skill --install (coming in a later build)
  restart your agent so it picks up the MCP server
```

This validates the pinned sizes/hashes end to end (a mismatch would have aborted before extraction), the archive extraction, `.mem-release` marker, and the `llama-server --version` + embedding round-trip.

### Workload and timings (real sidecar)

| Step | Result | Time |
|---|---|---|
| `mem context create` (auto temp) | `tmp-0ph4`, expires in 7 days | — |
| 20 varied memories (first batch) | all written; this run exposed the bug below | 1.96 s, not indexed |
| 10 genuinely new memories, one process each | all indexed and searchable | **3.67 s total, 367 ms/doc** (cold sidecar per process) |
| 2 more fresh documents in parallel-ish sequence | immediately searchable | 643 ms total (~320 ms each) |
| Bulk reindex: delete `search.db`, one `mem sync` | 34 documents | **844 ms** (one sidecar start, all embeds) |
| `mem sync` when already in sync | 21/34 unchanged | 752 ms (Open did the work) |
| `mem search` (cold process) | 5/5 correct top hit | **293–306 ms** each, mostly model load |
| `mem search` inside the MCP process | correct top hit | **9 ms warm**, **1.35 s cold** (model start inside the call) |
| `mem status` | `34 documents (in sync)` | ~68 ms |

Every CLI invocation is its own process, and 02a's sidecar is per process and lazy, so a single write or search pays the model start (~250–300 ms for the 36 MB Q8 model). Within one long-lived process (MCP) the sidecar is reused: the search took 9 ms.

Search quality on the real corpus (20 hand-written technical memories plus 14 more), keyword and paraphrase, all top-1:

```
 298 ms | RS256
        => 1. auth/rs256.md (tmp-0ph4/6xx2wbhxoslt) score=0.3409 [vec,bm25_boost]
 293 ms | how long do access tokens last before I need to renew them
        => 1. auth/token-refresh.md (tmp-0ph4/qzq5bdfxjnsf) score=0.0982 [vec,bm25]
 296 ms | where are database schema changes applied
        => 1. db/migrations.md (tmp-0ph4/s3g2jq7qr42h) score=0.0982 [vec,bm25]
 294 ms | how long are audit logs kept
        => 1. fresh/n3.md (tmp-0ph4/ohouyu67ooeh) score=0.3409 [vec,bm25_boost]
 306 ms | what happens when a background job keeps failing
        => 1. fresh/n4.md (tmp-0ph4/k4lk3qqwheyz) score=0.0982 [vec,bm25]
```

Final status:

```
mem 0.1.0
config      C:\Users\...\meme2e\config.toml
data dir    C:\Users\...\meme2e\data
contexts    2
memories    34
indexed     34 documents (in sync)
ok           embedding   C:\Users\...\meme2e\models\bge-small-en-v1.5-q8_0.gguf
ok           llama-server C:\Users\...\meme2e\bin\llama-server.exe
reranker    off (opt-in)
```

### Bug found and fixed by this run

The first E2E attempt wrote 20 memories but indexed none: `status` showed `1 documents`, and `search` reported `exec: "llama-server": executable file not found in %PATH%`. Root cause was in `internal/engine`: `defaultOptions()` set `binary: "llama-server"`, and `buildClients` used `firstNonEmpty(o.binary, cfg.SidecarPath, DefaultLlamaBinary)`, so the option default shadowed the `sidecar_path` that `mem init` had written. The option default is now empty, so the precedence is `WithSidecarBinary` › `config.SidecarPath` › PATH. Regression test: `engine.TestConfigSidecarPathWinsOverDefaultBinary` (a configured nonexistent path must be what the failure names). After the fix, the same store synced to 34/34 in sync and every query hit.

## `mem mcp` over real pipes

`mem mcp` now calls `mcp.Serve(ctx, stdin, stdout, engine, mcp.WithServerVersion(Version))` with the engine opened through `engine.WithoutInitialSync()`, so `initialize` and `tools/list` never spawn a model; the first write/search starts the shared in-process sidecar. Logs go to stderr only. The session below is a real `mem.exe mcp` process driven over OS pipes (requests verbatim; the long `instructions` text and the four tool schemas are elided where marked, nothing else changed):

```
>>> initialize
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"transcript","version":"1.0"}}}
<<< (29 ms, 1406 bytes)
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},
 "serverInfo":{"name":"mem","version":"0.1.0"},
 "instructions":"Persistent memory shared across your sessions, stored locally.\nTools: search, get, upsert, delete. ... [readme.md text elided]"}}

>>> notifications/initialized (no response expected)

>>> tools/list
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
<<< (0 ms, 4312 bytes)
{"jsonrpc":"2.0","id":2,"result":{"tools":[
 {"name":"search","description":"Search memories using hybrid search ...","inputSchema":{...}},
 {"name":"get","description":"Retrieve memories or context information ...","inputSchema":{...}},
 {"name":"upsert","description":"Create or update a markdown memory note ...","inputSchema":{...}},
 {"name":"delete","description":"Delete a memory permanently ...","inputSchema":{...}}]}}
   [all four schemas returned in full; elided here, unchanged from the 04 report]

>>> tools/call upsert
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"upsert","arguments":{"context":"tmp-0ph4","name":"mcp/written.md","tags":["mcp","transcript"],"body":"# Written over MCP\n\nThe MCP server wrote this note over a stdio JSON-RPC session, and the engine indexed it with the real llama-server sidecar."}}}
<<< (240 ms, 329 bytes)
{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"Memory created successfully.\nid: opgfe3lk4gke\nname: mcp/written.md\ncontext: tmp-0ph4\nversion: 1\nstatus: created"}],"structuredContent":{"context":"tmp-0ph4","id":"opgfe3lk4gke","index_warning":false,"name":"mcp/written.md","status":"created","version":1}}}

>>> tools/call search (paraphrase; real sidecar already warm from the upsert)
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"search","arguments":{"query":"which component stores session lookups for a few minutes","context":"tmp-0ph4","limit":3}}}
<<< (9 ms, 1878 bytes)
{"jsonrpc":"2.0","id":4,"result":{"content":[{"type":"text","text":"1. tmp-0ph4/cache/redis.md (id: d3sodixhtjfy, chunk 0, score 0.098)\nRedis caches session lookups for five minutes; the write-through path invalidates on profile updates.\n\n2. tmp-0ph4/frontend/state.md (id: 4hfxrymdxdvj, chunk 0, score 0.087)\n...\n\n3. global/readme.md (id: w4ldpt3ffd3l, chunk 0, score 0.084)\n...\n\ntook_ms: 7"}],"structuredContent":{"degraded":null,"hits":[{"id":"d3sodixhtjfy","doc_id":"d3sodixhtjfy","context":"tmp-0ph4","name":"cache/redis.md","heading":"","snippet":"Redis caches session lookups for five minutes; the write-through path invalidates on profile updates.","chunk":0,"score":0.09818181818181819,"sources":["vec","bm25"]},{"id":"4hfxrymdxdvj","doc_id":"4hfxrymdxdvj","context":"tmp-0ph4","name":"frontend/state.md","heading":"# State management","snippet":"# State management\n\nThe dashboard keeps server state in React Query ...","chunk":0,"score":0.08714285714285713,"sources":["vec","bm25"]},{"id":"w4ldpt3ffd3l","doc_id":"w4ldpt3ffd3l","context":"global","name":"readme.md","heading":"# Memory","snippet":"# Memory\n\nThis store keeps long-term memory ...","chunk":0,"score":0.0835897435897436,"sources":["vec","bm25"]}],"took_ms":7}}}

>>> tools/call get (no arguments)
{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"get","arguments":{}}}
<<< (1 ms, 2766 bytes)
{"jsonrpc":"2.0","id":5,"result":{"content":[{"type":"text","text":"# Memory\n\n... [default file elided]\n\n---\n## Live Contexts\n- global (permanent, 1 memories)\n- tmp-0ph4 (expires in 6d 23h, 23 memories)"}],"structuredContent":{"contexts":[{"slug":"global","is_global":true,"created_at":"2026-11-18T21:20:46Z","expires_at":null,"memory_count":1},{"slug":"tmp-0ph4","is_global":false,"created_at":"2026-11-18T21:20:59Z","expires_at":"2026-11-25T21:20:59Z","memory_count":23}],"default_file":{"body":"# Memory\n...","context":"global","id":"w4ldpt3ffd3l","name":"readme.md","tags":["system"],"updated_at":"2026-11-18T21:20:46Z","updated_by":"mem","version":1}}}}

--- process exit code: 0 ---
(stderr empty)
```

Notes: stdout carried only one JSON object per line (the client parsed every line), stderr stayed empty, EOF on stdin shut the process down with exit 0, and the search used the engine with the real sidecar (the same process had just indexed the upsert, hence 9 ms). A second identical run, where the upsert was a no-op and therefore started nothing, showed the other side of the same behavior: the search then paid the cold model start and reported `took_ms: 1349` for the same top hit. So the 9 ms is a warm-sidecar figure, not a cold-process one.

## Tests

`go build ./... && go vet ./... && go test ./... -count=1` — all pass, whole repo including the other agents' `internal/mcp` and `internal/server`:

```
ok  memory/internal/cli     3.643s
ok  memory/internal/config  0.805s
ok  memory/internal/engine  5.590s
ok  memory/internal/llm     7.927s
ok  memory/internal/mcp     8.455s
ok  memory/internal/search  6.445s
ok  memory/internal/server  4.117s
ok  memory/internal/setup   3.642s
ok  memory/internal/store   7.538s
```

Cross-builds of `./cmd/mem` with `CGO_ENABLED=0`: windows/amd64, windows/arm64, linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 — all pass.

Two other agents were editing `internal/search` and `internal/cli/cmd_server.go` while this work landed: their build broke twice mid-run, their cache-invalidation test failed once before they fixed it, and their new `mem server` made my old stub test hang (narrowed to `mem skill`). All of that settled; the block above is the final clean run. I did not modify `internal/search`, `internal/server`, or their `cmd_server.go` implementation.

`internal/cli` tests drive `Run` in process with a temp config, temp dirs and `engine.WithEmbedder(deterministicEmbedder{})` through `Env.OpenEngine`; no test touches `~/.mem` or the network. Covered: every command's happy path and main error path, exit codes, `--json` shapes, stdin body, `-`, append, `--if-version` conflict (4), unknown context (no auto-create), `rm` confirmation refusal without a TTY, `global` protection, auto temp slug, `--expiry` parsing (0/negative/non-numeric), config set validation, `-q`, global flags after the command, JSON error envelope, `mem server`'s non-loopback-without-token refusal, and `mem mcp` (empty stdin exits 0 with a clean stdout; a real initialize/tools/list/get session returns three valid JSON-RPC responses).

`internal/setup` tests cover: the six-target selection table plus unsupported platforms, model pins, downloader happy path/atomic install/skip-valid/resume with `Range`/hash mismatch/retry-then-succeed/exhausted retries/size mismatch, zip and tar.gz extraction (kept vs dropped files, flattening), traversal rejection, unknown archive format, non-interactive init, `--no-download`, `--with-rerank`, idempotence (no re-download, data preserved, port preserved), interactive defaults and a `y` answer, bad `--offline-dir`, next-step text, and the network path against `httptest`.

## Bug fix: test leakage into the real home, and trusting a broken sidecar

An owner machine failed with `fork/exec C:\Users\...\.mem\bin\llama-server.exe: This version of %1 is not compatible`. The file at that path was the 8-byte `"fake exe"` from `internal/setup/init_test.go`: two tests set `DataDir`/`ModelsDir` to `""`, so `setup.Run` fell back to the platform defaults and wrote a fake binary, fake models, a `.mem-release` marker and a seeded data dir into the real `~/.mem` on every `go test ./...`. A real `%APPDATA%\mem\config.toml` was also rewritten by an agent E2E run that forgot `MEM_CONFIG`.

### 1. Tests can no longer reach the real home

`internal/testenv` provides `Main(m *testing.M)`, which points `USERPROFILE`, `HOMEDRIVE`, `HOMEPATH`, `HOME`, `APPDATA`, `LOCALAPPDATA`, `XDG_CONFIG_HOME`, `XDG_CACHE_HOME`, `XDG_DATA_HOME` and `MEM_CONFIG` at a fresh `<temp>/mem-test-home-*/` tree and clears `MEM_DATA_DIR`. A `TestMain` in each of `internal/setup`, `internal/cli`, `internal/engine`, `internal/mcp`, `internal/server`, `internal/config` and `internal/store` calls it. If the temp tree cannot be created the binary fails loudly instead of running against the real home. (`internal/search` and `internal/llm` belong to the other agent, so I did not add guards there.)

`TestInitWithEmptyDirsStaysUnderIsolatedHome` asserts the guard: it checks that the home and `MEM_CONFIG` are inside `mem-test-home-*`, then runs `setup.Run` with **empty** `DataDir`, `ModelsDir` and `ConfigPath` — the exact leak — and requires every resolved path (data, models, bin, config) to stay inside that root.

Two independent verifications, both against temp paths only:

- Running the suite with `USERPROFILE`/`HOME`/`APPDATA`/`LOCALAPPDATA`/`XDG_*` pointed at a scratch dir produced **no** `.mem` under it (only the Go build cache, which Go itself writes).
- `~/.mem` and `%APPDATA%\mem\config.toml` were left byte-for-byte untouched: their mtimes were identical before and after a full run (`19:46:48` and `00:30:03`, both predating this session). I never read their contents and never ran anything that writes them; every manual command in this session used an explicit temp `MEM_CONFIG`, and the owner's own repairs were left alone.

### 2. `mem init` no longer trusts whatever is at the path

The `.mem-release` marker now stores JSON: the asset name, the installed binary's size and its **SHA-256**. `mem init` re-validates on every run — the binary must be at least 1 MiB, start with an executable header for this OS/arch, and match the recorded hash. Any mismatch reinstalls. Model files are checked against their pinned size and SHA-256 as before, and `--force` reinstalls the sidecar and models regardless.

`CheckSidecar`/`CheckModel` in `internal/setup` are the shared validators; `mem status` runs them (offline, without executing anything) and reports `ok` / `missing` / `broken` plus the reason, and adds `fix  run \`mem init --force\` …` when a reinstall would help. The JSON status gained `sidecar_problem`, `sidecar_asset`, `sidecar_size`, `embed_problem`, `rerank_problem` and `needs_reinstall`.

Against the owner's exact artifact:

```
$ mem status
…
missing     embedding …/models/bge-small-en-v1.5-q8_0.gguf
broken      llama-server …/bin/llama-server.exe  (…: is only 8 bytes; a real llama-server is several megabytes (corrupt, incomplete or a placeholder))
reranker    off (opt-in)
fix         run `mem init --force` to reinstall the llama-server and models
```

### 3. OS-level start failures carry a repair hint

`withSidecarHint` wraps any message matching an OS execution failure ("is not compatible", "not a valid Win32", "exec format error", "bad CPU type", "cannot execute binary", "is not a valid application") with:

> the llama-server at PATH looks corrupt or for another CPU architecture; run `mem init --force`

It applies to command errors, `--json` error payloads, search `degraded` notices (human and JSON) and upsert `warnings`; it never duplicates an already-present hint. `warnSetupNeeded` also distinguishes "not set up" from "present but unusable", so a stub binary now says which file is bad instead of silently degrading forever.

`InspectBinary` reads only the header fields (`e_lfanew`/COFF machine for PE, `e_machine` for ELF, `cputype` for Mach-O) rather than using `debug/pe`/`elf`/`macho`, which parse the whole image and reject a damaged tail; this check is about "is this plausibly a binary for this machine", and it costs one 64-byte read.

### 4. Tests added

- `internal/setup`: `TestInspectBinaryRejectsTheRealWorldFailure` (an 8-byte stub is rejected with its size in the message and flagged for reinstall, plus missing/empty/junk cases), `TestInspectBinaryRejectsWrongArchitecture`, `TestMarkerHashMismatchTriggersReinstall` (tampered binary + wrong hash ⇒ re-run downloads again and refreshes the marker), `TestMarkerStubBinaryIsDetectedAsInvalid` (self-consistent marker around a stub is still rejected), `TestForceReinstallsValidFiles`, `TestCheckModelVerifiesPinnedFiles`, `TestInitWithEmptyDirsStaysUnderIsolatedHome`.
- `internal/cli`: `TestStatusDetectsStubSidecar`, `TestStatusAcceptsHealthySidecar`, `TestUpsertWarnsAboutStubSidecar`, `TestSidecarHintOnlyForOSFailures` (including the owner's literal message and two messages that must **not** be touched), `TestStatusModelIntegrity`.

Verification: `go build ./...`, `go vet ./...`, and `go test` for `internal/setup`, `internal/cli`, `internal/engine`, `internal/config`, `internal/store`, `internal/server` all pass; the six `CGO_ENABLED=0` cross-builds pass. `go.mod` is untouched.

One caveat on the full-repo run: another agent was actively editing `internal/search` throughout (its build failed several times mid-session), so a whole-repo `go test ./...` could not be captured cleanly in one shot; each package was verified in a window where the tree built, and `internal/mcp`'s previously-reported `TestTools_Upsert/indexing_warning_appended` failure is theirs to fix, unchanged by this work.

## What is unverified

- **The real end-to-end run was performed** (see above), but only on Windows/amd64: real downloads, real `llama-server`, real GGUF embedding, real sync/search/status. The macOS and Linux binaries were never unpacked on their own OS — only the asset choice and the synthetic `tar.gz` extraction are tested.
- **Download resume against the real CDN was not exercised.** The full downloads used the non-resumed path (>50 MB over a stable connection, no interruption). `Range` resume, hash mismatch, retry/backoff and size mismatch are covered against a local `httptest` server only.
- `mem skill` is the only remaining stub. `mem server` was implemented by prompt 05 and `mem mcp` is wired here (tested in-process and over real pipes).
- `/v1/rerank` and reranker quality are 02a's territory; `--with-rerank` is tested to install and record the model, not to change ranking. The E2E left reranking off.
- The MCP session's `tools/list` schemas are unchanged from prompt 04; the report shows them elided. MCP behaviour beyond the transcript (cancellation, panic recovery, truncation) is covered by 04's own tests, not re-run here.
- Concurrency in the real E2E was sequential (one CLI process at a time); the two-engine concurrent test in `internal/engine` still covers the shared-data-dir case.
- **The sidecar header check is structural, not authoritative.** It reads `e_lfanew`/COFF machine, `e_machine` or `cputype`, so it catches stubs, truncation and wrong-architecture downloads. It cannot prove a binary runs; only `runVersion`/the embed probe do that, and `mem status` deliberately executes nothing.
- **Old-format markers are tolerated.** A `.mem-release` written by an earlier build (the bare asset name) still reads, and the next successful install rewrites it as JSON with a hash. That means a binary installed by an older `mem` is hash-checked only after one more `mem init`.
- **The macOS/Linux header paths are tested with synthetic files**, not with the real release archives on their own OS; Windows/amd64 was validated against the actual failure the owner hit.
- **Test isolation covers seven packages.** `internal/search` and `internal/llm` have no `TestMain` guard because they belong to the other agent; neither writes to the home today, but a future test there could.

## Decisions I made

1. **stdlib `flag` with a small dispatcher.** `consumeLeadingFlags` parses global flags before the command; the same globals are registered on each command's set so they also work after it. Positional arguments are collected around flags, which lets `mem search foo --limit 3 bar` work. `--` ends flag parsing.
2. **`BinDir` = `<models_dir>/../bin`.** Chosen so a single `models_dir` setting locates both the models and the sidecar; `sidecar_path` overrides it, and `DetectSidecar` also falls back to `PATH`.
3. **wazero compilation cache** to meet the 150 ms budget (see timings). This is a real behavior change in `internal/cli` only; it falls back silently.
4. **`init` writes `sidecar_path` into the config** after a successful install, so no command has to guess. Re-running init keeps it.
5. **`.mem-release` marker file** records the installed release instead of probing `llama-server --version` on every run — a quarantined or blocked binary then cannot make init re-download forever, and idempotence works for tests.
6. **`--no-download` still writes config and seeds the store**, then warns that indexed search is not set up. That is the fastest way to a usable CLI.
7. **`.part` is removed on hash mismatch** (not kept for resume), because a corrupted prefix cannot be repaired by appending.
8. **`status` checks drift read-only** by comparing store versions against `Index.State()` (never indexes, never embeds), so it stays a fast, model-free command. `mem sync` is the writer.
9. **`sync` progress goes to stderr**, suppressed by `-q` and `--json`.
10. **Errors print `mem: <message>` plus `hint:`**, and with `--json` an additional `{"error","code","hint"}` object goes to stdout — the same single-line contract in both modes.
11. **`mem get` with no args prints the default file body, then a context list marked with `*`, then that context's memories** — the same content the MCP no-arg get returns, in a human layout.
12. **Unnamed memories print `<id>.md`** because the store names them that way; the CLI never invents a display name.
13. **`init` verification is a warning, not an error.** Files are already on disk; failing the whole command because a model will not start would be worse than a precise message.
14. **`--offline-dir` accepts either the pinned archive or a bare `llama-server`**, and still verifies model hashes, so offline installs are as strict as network ones.
15. **`mem mcp` opens the engine with `WithoutInitialSync`.** `initialize` and `tools/list` must answer instantly and must not spawn a model, which is what the 04 report promises. Writes are still indexed synchronously by the store hooks, and `mem sync` exists for repair. The engine logger and the MCP logger both write to stderr; stdout is protocol only and `--json` has no effect on this command.
16. **Isolation is a package-level `TestMain`, not per-test env juggling.** One helper in `internal/testenv` means a new test cannot forget to isolate itself, and a failure to create the temp root aborts the binary rather than silently running against the real home. I considered asserting "nothing was written outside what the test created" at exit, but the instruction was right that it is too strict (the Go toolchain writes its build cache under the redirected `LOCALAPPDATA`); the empty-dirs regression test covers the actual leak instead.
17. **The marker carries the hash rather than a hash file per binary**, so one small file next to `llama-server` is the whole record and `mem status` needs no extra state. Old one-line markers still parse.
18. **`--force` is a flag on `init`, not a separate command**, because every other repair path (`mem init --force`) in the hints should be one thing to remember.

## How to run the checks

```sh
go build ./...
go vet ./...
go test ./... -count=1

# six cross-builds
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./cmd/mem   # + windows/arm64,
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build ./cmd/mem   #   linux/arm64,
CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build ./cmd/mem   #   darwin/arm64
```

Real setup (network): `mem init --yes` (add `--with-rerank` for the reranker).
Offline: `mem init --yes --offline-dir /path/with/asset-or-llama-server-and-models`.
Machine-readable output contract: `internal/cli/README.md`.
