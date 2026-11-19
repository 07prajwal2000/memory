# @07prajwal2000/agent-memory

Local long-term memory store for AI agents (Claude, Antigravity, OpenAI-compatible tools).

## Features

- **CLI**: manage contexts and memories directly (`mem context`, `mem get`, `mem upsert`, `mem search`).
- **Model Context Protocol (MCP)**: stdio server (`mem mcp`) with tools for search, retrieval, upsert, and deletion.
- **Embedded Web UI**: built-in dashboard for browsing memory graphs (`mem server`).
- **Single Static Binary**: zero external dependencies, powered by embedded SQLite with vector extensions.

## Installation

Install globally:

```bash
npm i -g @07prajwal2000/agent-memory
```

The CLI is available under three equivalent command aliases: `mem`, `memory`, and `agent-memory`.
Note: Global install fails with `EEXIST` if another global package owns one of those names; run `npm i -g --force @07prajwal2000/agent-memory` or use local `npx` to avoid it.

Run directly with `npx`:

```bash
npx @07prajwal2000/agent-memory init
```

## Model Context Protocol (MCP)

### Claude CLI
```bash
claude mcp add mem -- mem mcp
# or via npx
claude mcp add mem -- npx -y @07prajwal2000/agent-memory mcp
```

### Generic JSON Configuration
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

## Platform Support

Precompiled standalone binaries are delivered via platform-specific optional dependencies:
- Linux (x64, arm64)
- macOS (Apple Silicon arm64, Intel x64)
- Windows (x64, arm64)

If installing in an environment where optional dependencies were disabled, install the platform package directly or set `MEM_BINARY_PATH=/path/to/mem`.

## License

MIT
