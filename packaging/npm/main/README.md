# mem

Local long-term memory store for AI agents (Claude, Antigravity, OpenAI-compatible tools).

## Features

- **CLI**: manage contexts and memories directly (`mem context`, `mem get`, `mem upsert`, `mem search`).
- **Model Context Protocol (MCP)**: stdio server (`mem mcp`) with tools for search, retrieval, upsert, and deletion.
- **Embedded Web UI**: built-in dashboard for browsing memory graphs.
- **Single Static Binary**: zero external dependencies, powered by embedded SQLite with vector extensions.

## Installation

Run directly with `npx`:

```bash
npx @CHANGE-ME/mem version
```

Or install globally:

```bash
npm install -g @CHANGE-ME/mem
```

## Platform Support

Precompiled standalone binaries are delivered via platform-specific optional dependencies:
- Linux (x64, arm64)
- macOS (Apple Silicon arm64, Intel x64)
- Windows (x64, arm64)

If installing in an environment where optional dependencies were disabled, install the platform package directly or set `MEM_BINARY_PATH=/path/to/mem`.

## License

MIT
