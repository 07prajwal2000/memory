# mem

<p align="center">
  <a href="https://www.npmjs.com/package/@07prajwal2000/agent-memory"><img alt="npm version" src="https://img.shields.io/npm/v/@07prajwal2000/agent-memory?style=for-the-badge&logo=npm&logoColor=white&color=cb3837"></a>
  <a href="https://07prajwal2000.github.io/memory/"><img alt="Documentation" src="https://img.shields.io/badge/docs-read%20the%20docs-4f46e5?style=for-the-badge&logo=readthedocs&logoColor=white"></a>
  <a href="https://github.com/07prajwal2000/memory/releases/latest"><img alt="Latest release" src="https://img.shields.io/github/v/release/07prajwal2000/memory?style=for-the-badge&logo=github&color=24292f"></a>
</p>

<p align="center">
  <a href="https://github.com/07prajwal2000/memory/actions/workflows/release.yml"><img alt="Release workflow" src="https://img.shields.io/github/actions/workflow/status/07prajwal2000/memory/release.yml?style=flat-square&logo=githubactions&logoColor=white&label=build"></a>
  <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/github/license/07prajwal2000/memory?style=flat-square&color=blue"></a>
  <a href="https://www.npmjs.com/package/@07prajwal2000/agent-memory"><img alt="Node 18 or newer" src="https://img.shields.io/node/v/@07prajwal2000/agent-memory?style=flat-square&logo=nodedotjs&logoColor=white&color=339933"></a>
  <img alt="Platforms: Windows, macOS, Linux" src="https://img.shields.io/badge/platforms-windows%20%7C%20macos%20%7C%20linux-lightgrey?style=flat-square">
  <img alt="MCP server" src="https://img.shields.io/badge/MCP-stdio%20server-8a2be2?style=flat-square">
  <img alt="Works offline" src="https://img.shields.io/badge/runs-100%25%20local-success?style=flat-square">
</p>

Local long-term memory store for AI agents (Claude, Antigravity, ChatGPT).

`mem` is a fast single-binary memory daemon with a command-line interface, a Model Context Protocol (MCP) server over stdio, an embedded web UI dashboard, and hybrid search combining BM25 keyword matching and vector search with embedded SQLite and wazero WASM runtime.

**Documentation:** https://07prajwal2000.github.io/memory/

## Installation

Install globally via npm:

```bash
npm i -g @07prajwal2000/agent-memory
```

The package provides three equivalent command aliases: `mem`, `memory`, and `agent-memory`.

> **Note on global conflicts:** A global install fails with `EEXIST` if another global package already owns one of those command names. Run `npm i -g --force @07prajwal2000/agent-memory` to overwrite or use local `npx` to avoid conflicts.

## First Run

Initialize the data directory and configuration:

```bash
mem init
```

Or run one-off without global installation:

```bash
npx @07prajwal2000/agent-memory init
```

## Model Context Protocol (MCP) Setup

`mem` provides an MCP server over stdio with tools for semantic search, memory retrieval, upsert, and deletion.

### Claude CLI

Global installation:
```bash
claude mcp add mem -- mem mcp
```

Via npx:
```bash
claude mcp add mem -- npx -y @07prajwal2000/agent-memory mcp
```

### Generic JSON Configuration

Add to your MCP client configuration (such as Claude Desktop `claude_desktop_config.json`):

**Global:**
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

**Via npx:**
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

## Agent Skill

Install the skill for agent runtimes:

```bash
npx skills add 07prajwal2000/memory
```

## Web Dashboard

Start the embedded web dashboard server:

```bash
mem server
```

Access the dashboard at `http://127.0.0.1:18945` to inspect stored memories, search indices, and daemon health.

## Contexts

Memories are organized into scoped contexts (default: `global`):

```bash
mem context ls                           # List contexts
mem context create [slug]                # Create permanent context
mem context create [slug] --temp         # Create temporary context (default 7 days)
mem context create [slug] --expiry 14    # Create context with custom expiry (days)
mem context expiry <slug> <days|none>    # Update or remove expiry
mem context rm <slug> --yes              # Delete context and its memories
```

## Where Data Lives

By default, all data (SQLite database, vector index, and Markdown files) is stored in:

```
~/.mem
```

Override the data directory with the `--data-dir` flag or the `MEM_DATA_DIR` environment variable.

## License

MIT
