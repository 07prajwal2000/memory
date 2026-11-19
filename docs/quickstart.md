# Quick start

From an empty machine to an agent that remembers, in five steps.

**You need:** a terminal. For the npm route, Node.js 18 or newer. For the source route, Go 1.23 or newer and Node.js 18 or newer.

## 1. Install

> [!NOTE]
> The packages are **not published yet**. The npm commands below are the names they will use. Until publication, use [the source route](#from-source).

Global install (the commands `mem`, `memory` and `agent-memory` are aliases):

```bash
npm i -g @07prajwal2000/agent-memory
```

Or run one-off without a global install:

```bash
npx @07prajwal2000/agent-memory init
```

If a global install fails with `EEXIST` because another package owns one of those command names, use `npm i -g --force @07prajwal2000/agent-memory`, or stay on `npx`.

### From source

Build the dashboard assets first, then the binary:

```bash
cd web
npm ci
npm run build:real
cd ..
go build -o mem ./cmd/mem
```

Put the `mem` binary somewhere on your `PATH`.

## 2. Run `mem init`

```bash
mem init
```

`mem init` asks a few questions:
- where to keep data, models and the `llama-server` binary (defaults under `~/.mem`);
- which port the dashboard should use (default `7777`);
- how many days a temporary context lasts by default (default `7`);
- how long the local model server may sit idle before it stops (default `300` seconds);
- whether to install the optional reranker (default **no**; see [Configuration](configuration.md#rerank)).

Then it downloads two small files:
- **llama.cpp's `llama-server`** for your OS and CPU (about 24 MB), verified against a pinned size and SHA-256;
- **the `bge-small-en-v1.5` embedding model** (about 35 MB), which turns text into vectors for semantic search.

Real output from `mem init` on Windows (paths abbreviated):

```text
  ok  model server responds (384-dimensional embedding)

mem is set up.
  data    ...\demo\data
  models  ...\full\models
  server  ...\full\bin\llama-server.exe
  config  ...\demo\config.toml

next steps:
  Claude Code:  claude mcp add mem -- mem mcp
  generic MCP:  {"mcpServers":{"mem":{"command":"mem","args":["mcp"]}}}
  dashboard:    mem server --port 7811
  agent skill:  npx skills add <owner>/<repo> (or `mem skill --install`)
  restart your agent so it picks up the MCP server
```

Useful variants:

```bash
mem init --yes                      # accept defaults and skip the questions
mem init --force                    # reinstall the model server and models
mem init --no-download              # set up config and data only, no network
mem init --offline-dir ./usb-stick  # install from a folder instead of the network
```

`--no-download` gives you a usable CLI and dashboard, but indexed search stays unavailable until a real `mem init` succeeds. You will see this warning:

```text
  warning: llama-server was not installed; indexed search will stay unavailable until `mem init` succeeds
```

The config file is written to your platform's user config directory:

| OS | Config file |
|---|---|
| Windows | `%APPDATA%\mem\config.toml` |
| Linux | `~/.config/mem/config.toml` (or `$XDG_CONFIG_HOME/mem/config.toml`) |
| macOS | `~/Library/Application Support/mem/config.toml` |

Data defaults to `~/.mem/data`, models to `~/.mem/models`, and `llama-server` to `~/.mem/bin`. See [Configuration](configuration.md) for every key and environment variable.

## 3. Write a memory and search for it

Create a context for a project, write one note, and search:

```bash
mem context create work
mem upsert --context work --name deploy/staging.md --tag deploy \
  "Deploys run from the release branch only. Staging lives at https://staging.example.internal."
mem search "where do deploys run" --context work --no-global
```

Real output:

```text
1. deploy/staging.md (work/fxpkh7wubztw) score=0.1013 [vec,bm25,name]
   matched: deploys, run, deploy
   Deploys run from the release branch only. Staging lives at https://staging.example.internal.
```

Read it back by name:

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

`mem get` with no arguments is the orientation call: it prints the default file (`global/readme.md`) and the list of contexts. Try it once.

## 4. Connect your agent

For Claude Code with the binary on your `PATH`:

```bash
claude mcp add mem -- mem mcp
```

With `npx`:

```bash
claude mcp add mem -- npx -y @07prajwal2000/agent-memory mcp
```

For any client that reads an `mcpServers` JSON block (Claude Desktop, for example):

```json
{
  "mcpServers": {
    "mem": {
      "command": "mem",
      "args": ["mcp"]
    }
  }
}
```

Or, through `npx`:

```json
{
  "mcpServers": {
    "mem": {
      "command": "npx",
      "args": ["-y", "@07prajwal2000/agent-memory", "mcp"]
    }
  }
}
```

Restart the agent after adding the server. Cursor, Codex, Gemini CLI and others that read `mcpServers` JSON use the same command or block; check your agent's MCP documentation for where that file lives.

You do not need to run `mem mcp` yourself. The agent starts it and speaks JSON-RPC over stdin and stdout. If you want to check it by hand, see [MCP server](mcp.md#trying-it-by-hand).

## 5. Teach your agent to use it

Connecting the server gives the agent the four tools, but not the habits. Install the skill:

```bash
npx skills add 07prajwal2000/memory
```

If you do not have Node, `mem` ships the same text and can install it for you:

```bash
mem skill --install --agent claude-code
```

Real output from a project directory:

```text
installed ...\.claude\skills\mem\SKILL.md
```

Run it again and it says `already up to date`. Use `--global` for your home directory instead of the current project. [The agent skill](skill.md) explains what the text teaches.

## Where to go next

- [Concepts](concepts.md): what a context is, why `global` is special, what the default file does.
- [Search](search.md): how hybrid and fuzzy search behave, and how to write better queries.
- [Recipes](recipes.md): project contexts, handoffs, logs, backups, CI.

<div class="next-steps">

**Next steps:** [Concepts](concepts.md) → [Command line](cli.md)

</div>
