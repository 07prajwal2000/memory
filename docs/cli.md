# Command line

Every command, with the flags the binary actually accepts. Run `mem help` or `mem help <command>` for the same text in your terminal.

```text
usage: mem <command> [flags]

commands:
  init                     set up data, models and the local model server
  context create [slug]    create a context (--temp, --expiry DAYS)
  context ls               list contexts
  context rm <slug>        delete a context and its memories (--yes)
  context expiry <slug> <days|none>
  ls [--context C]         list memories of a context (default global)
  get [name-or-id]         read one memory, or the default file and contexts
  upsert [body|-]          create or update a memory
  delete <id>              delete a memory
  search <query...>        hybrid keyword + vector search
  sync                     re-sync files into the search index
  status                   show configuration, models and index state
  config [key] [value]     show, read or set configuration
  server [--port N]        dashboard server
  mcp                      MCP server over stdio
  skill [--install]        agent skill (not available yet)
  version                  print the version
  help [command]           this help
```

The `skill` line still says "not available yet" in the help table, but the command works: `mem skill` prints the skill and `mem skill --install` installs it. See [The agent skill](skill.md).

## Global flags

These work before or after the command word:

| Flag | Meaning |
|---|---|
| `--json` | Machine-readable output on stdout; human notes go to stderr |
| `--data-dir D` | Override the data directory for this command |
| `--config PATH` | Override the config file for this command |
| `-q`, `--quiet` | Suppress hints and progress |
| `-v`, `--verbose` | Extra diagnostics |

`--data-dir` changes only the data directory. Models and `llama-server` still come from the config, so scripts that need full isolation should set `MEM_CONFIG` as well. See [Configuration](configuration.md#environment-variables).

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Success |
| `1` | General error |
| `2` | Usage error (bad flag, missing argument, invalid value) |
| `3` | Not found (memory or context does not exist) |
| `4` | Conflict or version mismatch, or a reserved thing was refused |

With `--json`, an error still exits non-zero and prints one object on stdout:

```json
{"code":3,"error":"not found: memory \"missing.md\" does not exist in context \"work\"","hint":"run `mem ls` to list the memories in that context"}
```

The same line goes to stderr as `mem: ...`, followed by `hint:`.

## `mem init`

```text
mem init [--yes] [--force] [--data-dir D] [--models-dir D] [--port N] [--expiry DAYS]
         [--idle-timeout SECONDS] [--with-rerank] [--no-download] [--offline-dir D]
```

| Flag | Meaning |
|---|---|
| `--yes` | Take defaults and any flags you pass; skip the questions |
| `--force` | Reinstall the model server and models even if they look valid |
| `--data-dir D` | Where memories, `index.db` and `search.db` live |
| `--models-dir D` | Where model files live; `llama-server` goes in the sibling `bin/` |
| `--port N` | Dashboard port saved in the config |
| `--expiry DAYS` | Default lifetime for temp contexts |
| `--idle-timeout SECONDS` | How long the model server may sit idle before it stops |
| `--with-rerank` | Also install the optional reranker model |
| `--no-download` | Configure and seed only; make no network requests |
| `--offline-dir D` | Install from a local folder instead of the network |

`mem init` is idempotent. It never deletes data, and it writes `sidecar_path` into the config after a successful install. [Quick start](quickstart.md#2-run-mem-init) shows the full output.

## `mem context`

```text
mem context create [slug] [--temp] [--expiry DAYS] | ls | rm <slug> [--yes] | expiry <slug> <days|none>
```

Create:

```bash
mem context create work
mem context create handoff --expiry 2
mem context create scratch --temp
mem context create
```

```text
created context work
created context handoff (expires 2026-11-21 12:18)
created context scratch (expires 2026-11-26 12:18)
created context tmp-e8ob (expires 2026-11-26 12:15)
```

No slug means an auto-named temp context (`tmp-xxxx`). `--expiry` without `--temp` also implies a temp context. `--expiry 0` or a negative value is a usage error.

List:

```bash
mem context ls
```

```text
SLUG                     MEMORIES  CREATED           EXPIRES
global                          1  2026-11-19 12:18  never
handoff                         0  2026-11-19 12:18  2026-11-21 12:18
work                            4  2026-11-19 12:16  never
```

Remove:

```bash
mem context rm handoff --yes
```

```text
deleted context handoff (0 memories)
```

Without `--yes` on a terminal, `rm` asks you to type the slug. `global` cannot be removed (exit `4`).

Change expiry:

```bash
mem context expiry work 14
mem context expiry work none
```

```text
context work now expires 2026-12-03 12:15
context work now expires never
```

## `mem ls`

```text
mem ls [--context C] [--limit N]
```

Lists memories without bodies. Default context is `global`.

```bash
mem ls --context work
```

```text
NAME                                     ID           VERSION  UPDATED
db/migrations.md                         wi67lnntnaq5       1  2026-11-19 12:16
deploy/staging.md                        fxpkh7wubztw       1  2026-11-19 12:16
auth/token.md                            t4epatooy5gv       1  2026-11-19 12:16
```

`--limit 0` or no limit lists everything (the CLI's own default).

JSON:

```json
[{"context":"work","id":"wi67lnntnaq5","name":"db/migrations.md","tags":["db"],"updated_at":"2026-11-19T12:16:19Z","updated_by":"cli","version":1}]
```

## `mem get`

```text
mem get [name-or-id] [--context C]
```

Read one memory by name or id:

```bash
mem get deploy/staging.md --context work
```

```text
---
# deploy/staging.md (fxpkh7wubztw, v1, updated 2026-11-19 12:16 by cli)
# tags: deploy
---
Deploys run from the release branch only. Staging lives at https://staging.example.internal.
```

With no arguments, `get` prints the default file, the contexts, and the current context's memories. This is what an agent calls to orient itself:

```bash
mem get
```

```text
# Memory

This store keeps long-term memory for AI agents as markdown files. ...

## Contexts

* global                      1 memories  expires never
  work                        4 memories  expires never

## Memories in global

readme.md                                eebfzjok4nhm v1
```

The `*` marks the current context.

## `mem upsert`

```text
mem upsert [--context C] [--name N] [--id ID] [--tag T]... [--append]
           [--if-version V] [--by WHO] [body | -]
```

| Flag | Meaning |
|---|---|
| `--context C` | Target context; defaults to `global` |
| `--name N` | Update-or-create by name inside the context |
| `--id ID` | Update (and optionally rename) an existing memory |
| `--tag T` | Add a tag; repeat the flag for several tags |
| `--append` | Add to the end instead of replacing |
| `--if-version V` | Write only if the current version equals `V` |
| `--by WHO` | Record who wrote it (default `cli`) |

Resolution order is **id**, then **name**, then create an unnamed memory. A body can be an argument or stdin:

```bash
mem upsert --context work --name auth/token.md --tag auth --by alice \
  "Access tokens expire after 15 minutes; refresh tokens last 30 days."
```

```text
t4epatooy5gv v1 (work/auth/token.md)
```

Multi-line body from stdin (use `-`, or omit the argument with piped input):

```bash
cat <<'EOF' | mem upsert --context work --name release/checklist.md -
# Release checklist

- run tests
- tag the commit
EOF
```

```text
n37iccu7dqwn v1 (work/release/checklist.md)
```

Append a line to a log:

```bash
echo "- 09:00 started migration" | mem upsert --context work --name status/log.md --append -
```

```text
4cx2snmck2m2 v1 (work/status/log.md)
```

Writing to an unknown context fails with exit `3` and a hint to create it.

## `mem delete`

```text
mem delete <id>
```

```bash
mem delete y5maamtampm2
```

```text
deleted y5maamtampm2
```

Takes an **id**, not a name. Find the id with `mem ls` or `mem search`. The default file `global/readme.md` cannot be deleted. A malformed id is a usage error (exit `2`); a well-formed id that does not exist is not found (exit `3`).

## `mem search`

```text
mem search <query...> [--context C] [--no-global] [--limit N] [--per-doc N] [--fuzzy] [--no-fuzzy]
```

| Flag | Meaning |
|---|---|
| `--context C` | Also search this context |
| `--no-global` | Exclude `global`; requires `--context` |
| `--limit N` | Number of results (default 8, max 50) |
| `--per-doc N` | Chunks per document (default 1) |
| `--fuzzy` / `--no-fuzzy` | Force fuzzy matching on or off for this query |

```bash
mem search "where do deploys run" --context work --no-global --limit 2
```

```text
1. deploy/staging.md (work/fxpkh7wubztw) score=0.1013 [vec,bm25,name]
   matched: deploys, run, deploy
   Deploys run from the release branch only. Staging lives at https://staging.example.internal.
2. release/checklist.md (work/n37iccu7dqwn) score=0.0900 [vec,bm25]
   matched: run
   # Release checklist

   - run tests
   - tag the commit
```

Notes printed with `note:` (for example "embedder unavailable") mean search ran in a reduced mode. [How search works](search.md) explains the brackets, the scores and fuzzy matching.

JSON:

```json
{"degraded":null,"hits":[{"chunk":2,"context":"global","heading":"# Memory > ## Conventions","id":"5py22udtiqgp","matched_terms":["token","refresh"],"name":"readme.md","score":0.09757575757575758,"snippet":"## Conventions\n\n- One topic per memory...","sources":["vec","bm25"]},{"chunk":0,"context":"work","heading":"","id":"z54l6mnsc7te","matched_terms":["token","refresh"],"name":"auth/token.md","score":0.0818262987012987,"snippet":"Access tokens expire after 15 minutes; refresh tokens last 30 days.","sources":["vec","bm25","name"]}],"took_ms":224.838}
```

`degraded` is `null` when both search legs worked, or a list of human-readable notices otherwise.

## `mem sync`

```text
mem sync
```

Rebuilds the search index from the files. Use it after an embedder outage, after editing files by hand, or when `mem status` says the index is out of sync.

```bash
mem sync
```

```text
index 1/4 global/eebfzjok4nhm
index 2/4 work/fxpkh7wubztw
index 3/4 work/t4epatooy5gv
index 4/4 work/wi67lnntnaq5

synced 4 documents: 0 added, 0 updated, 0 removed, 4 unchanged, 0 pending vectors filled (0.0s)
```

Progress goes to stderr; the summary goes to stdout. `-q` silences the progress, `--json` gives you the summary as an object.

## `mem status`

```text
mem status
```

Shows version, config path, data directory, counts, index state, and whether the models and the model server are present and valid. It never starts the model.

```bash
mem status
```

```text
mem 0.1.0
config      ...\demo\config.toml
data dir    ...\demo\data
contexts    2
memories    4
indexed     4 documents (in sync)
ok          embedding ...\full\models\bge-small-en-v1.5-q8_0.gguf
ok          llama-server ...\full\bin\llama-server.exe
reranker    off (opt-in)
```

If something is missing or damaged, the lines read `missing` or `broken` and a `fix` line tells you to run `mem init --force`. [Troubleshooting](troubleshooting.md#mem-status-says-the-model-server-is-broken) covers the repair.

## `mem config`

```text
mem config [key] [value]
```

```bash
mem config                     # every setting
mem config port                # one value
mem config port 7812           # set and save
```

```text
7812
port = 7812
```

Values are validated before they are saved. [Configuration](configuration.md) lists every key.

## `mem server`

```text
mem server [--port N] [--host H] [--allowed-host HOST] [--token T|--token-file F] [--open]
```

Starts the dashboard. Default `http://127.0.0.1:7777`. See [Dashboard](dashboard.md) for the screens and security model.

## `mem mcp`

```text
mem mcp [--no-fuzzy] [--fuzzy]
```

Serves the Model Context Protocol on stdin/stdout. Protocol messages are the only thing on stdout; logs go to stderr. See [MCP server](mcp.md).

## `mem skill`

```text
mem skill [--install] [--global] [--agent NAME]... [--force]
```

Without `--install`, prints the skill markdown. With `--install`, writes it to the agent directories it detects in the current project, or to the ones you name with `--agent`. `--global` uses your home directory. See [The agent skill](skill.md).

## `mem version` and `mem help`

```bash
mem version
mem version --json
mem --version
```

`mem --version` and a bare `mem -v` do the same as `mem version`. With a command, `-v` is still the verbose flag.

```text
mem 0.1.0 (go windows/amd64, go1.27.0)
{"arch":"amd64","go":"go1.27.0","name":"mem","os":"windows","version":"0.1.0"}
```

```bash
mem help
mem help upsert
```

`help` exits `0`, including for a command with no extra help text.

## Using `--json` in scripts

With `--json`, stdout is exactly one JSON value, so it pipes cleanly:

```bash
mem search "deploy branch" --context work --json | jq '.hits[] | {name, score}'
```

Errors keep the same contract:

```bash
mem --json get missing.md --context work
```

```json
{"code":3,"error":"not found: memory \"missing.md\" does not exist in context \"work\"","hint":"run `mem ls` to list the memories in that context"}
```

Check the exit code, not the presence of output. A search with no hits is still a success (`0`) and returns `{"hits":[],...}`.

<div class="next-steps">

**Next steps:** [MCP server](mcp.md) → [Configuration](configuration.md)

</div>
