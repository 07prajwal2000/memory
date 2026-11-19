# Configuration

One TOML file controls `mem`. Every setting can also be changed with `mem config`, and most have a command-line override.

## Where the config lives

`mem` uses the operating system's user config directory:

| OS | Path |
|---|---|
| Windows | `%APPDATA%\mem\config.toml` |
| Linux | `~/.config/mem/config.toml` (or `$XDG_CONFIG_HOME/mem/config.toml`) |
| macOS | `~/Library/Application Support/mem/config.toml` |

Set `MEM_CONFIG` to use a different file for one command or a whole shell:

```bash
MEM_CONFIG=/tmp/mem-test.toml mem status
```

## Environment variables

| Variable | Effect |
|---|---|
| `MEM_CONFIG` | Path to the config file (replaces the default location) |
| `MEM_DATA_DIR` | Overrides `data_dir` after the file is loaded |

A command-line `--config` or `--data-dir` overrides both. Note that `--data-dir` changes only the data directory; the models and `llama-server` still come from the config. For a fully isolated test setup, set `MEM_CONFIG` to a throwaway file and run `mem init` into it.

## Default paths

| Thing | Default |
|---|---|
| Data (`index.db`, `search.db`, `contexts/`) | `~/.mem/data` |
| Models (`bge-small`, optionally the reranker) | `~/.mem/models` |
| `llama-server` | `~/.mem/bin` (the `bin` directory next to `models`) |
| Dashboard | `http://127.0.0.1:7777` |
| Temp context lifetime | 7 days |
| Model server idle timeout | 300 seconds |

## A real config file

This is what `mem init` wrote during a test setup (paths shortened and line order preserved):

```toml
data_dir = '...\demo\data'
models_dir = '...\full\models'
port = 7811
default_temp_expiry_days = 7
rerank = false
rerank_model = 'jina-reranker-v1-tiny-en-Q8_0.gguf'
search_fuzzy = true
idle_timeout_seconds = 300
sidecar_path = '...\full\bin\llama-server.exe'
embed_model = 'bge-small-en-v1.5-q8_0.gguf'
allowed_hosts = []
```

The config stores paths as text. Use absolute paths if you run `mem` from different directories.

## Every key

| Key | Default | Meaning |
|---|---|---|
| `data_dir` | `~/.mem/data` | Memories and the two databases |
| `models_dir` | `~/.mem/models` | Model files; `llama-server` lives in the sibling `bin/` |
| `port` | `7777` | Dashboard port |
| `default_temp_expiry_days` | `7` | Default lifetime of a temp context |
| `idle_timeout_seconds` | `300` | Stop the model server after this much idle time |
| `embed_model` | `bge-small-en-v1.5-q8_0.gguf` | Embedding model file inside `models_dir` |
| `rerank` | `false` | Enable the optional reranker |
| `rerank_model` | `jina-reranker-v1-tiny-en-Q8_0.gguf` | Reranker file inside `models_dir` |
| `sidecar_path` | installed path | Explicit path to `llama-server`; wins over the default location |
| `allowed_hosts` | empty | Extra `Host` values the dashboard may serve (tunnels, reverse proxies) |
| `search_fuzzy` | `true` | Default fuzzy matching for CLI, MCP and REST search |

Read or change them with:

```bash
mem config
mem config port
mem config port 7812
```

```text
7812
port = 7812
```

`mem config` validates values before saving and writes the file atomically. The full key list from a real run:

```text
data_dir                 ...\demo\data
models_dir               ...\full\models
port                     7777
default_temp_expiry_days 7
idle_timeout_seconds     300
embed_model              bge-small-en-v1.5-q8_0.gguf
rerank                   false
rerank_model             jina-reranker-v1-tiny-en-Q8_0.gguf
sidecar_path
allowed_hosts
search_fuzzy             true
```

## Models

`mem` uses two small GGUF models, both downloaded by `mem init` and both optional in the sense that `mem` still works without them (with reduced search).

| Model | Size | Dimensions | Used for |
|---|---|---|---|
| `bge-small-en-v1.5-q8_0.gguf` | ~35 MB | 384 | Embedding memories and queries |
| `jina-reranker-v1-tiny-en-Q8_0.gguf` | ~35 MB | — | Optional reranking of search candidates |

They run through `llama-server`, a local HTTP server that `mem` starts on demand and stops after the idle timeout. Nothing is sent to a remote service.

The embedding model is required for semantic (`vec`) search. Without it, search is keyword-only and says so. The reranker is never required.

## Rerank

Reranking takes the top search candidates and asks a second model to score them against the query. It is **off by default**.

We tested the tiny reranker and it did not beat plain fusion enough to justify the extra model, memory and latency. It is there if you want to experiment:

```bash
mem init --with-rerank
mem config rerank true
```

The status surfaces show `rerank: disabled` when it is off and a normal model state when it is on. If you enable reranking but the model is missing, `mem status` says so.

## Idle timeout

`llama-server` costs a few hundred milliseconds to start. `mem` keeps it running while it is useful and stops it after `idle_timeout_seconds` (default 300) of inactivity. The next search or write starts it again.

This is why the first search in a fresh CLI process can take longer than the next one, and why a long-lived MCP server is faster than a sequence of one-shot commands: it reuses the same model process.

## Dashboard hosts and tokens

`allowed_hosts` is the config form of `mem server --allowed-host`:

```toml
allowed_hosts = ["my-tunnel.ngrok-free.app"]
```

If any configured host is non-loopback, `mem server` requires a token (`--token` or `--token-file`). See [Dashboard](dashboard.md#running-behind-a-tunnel).

## Precedence

When more than one source sets a value, the most specific wins:

1. A command-line flag (`--data-dir`, `--port`, `--config`).
2. An environment variable (`MEM_CONFIG`, `MEM_DATA_DIR`).
3. The config file.
4. Built-in defaults.

<div class="next-steps">

**Next steps:** [Dashboard](dashboard.md) → [Troubleshooting](troubleshooting.md)

</div>
