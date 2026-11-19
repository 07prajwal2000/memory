# mem

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
