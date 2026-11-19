# mem

A local long-term memory store for AI agents (Claude, Antigravity, ChatGPT).

`mem` delivers a fast, single-binary memory daemon with a command-line interface, a Model Context Protocol (MCP) server over stdio, an embedded web UI, and hybrid local search combining BM25 keyword matching and vector search with embedded SQLite and wazero WASM runtime.

## Key Capabilities

- **Single Static Executable**: No external C runtime or database daemons required. SQLite and sqlite-vec run entirely in-process.
- **Agent Interoperability**: Implements the Model Context Protocol (MCP) over stdio (`mem mcp`) for Claude Desktop, Antigravity, and AI IDEs.
- **Context Management**: Scoped memory contexts, permanent workspaces, and self-expiring temporary contexts (`mem context create`).
- **Markdown Source of Truth**: Records are stored directly as plain Markdown files on disk, ensuring user data remains fully transparent and portable.
- **npm / npx Distribution**: Easy installation without compiling from source via native precompiled platform packages.

## Quick Start

### Run Instantly with npx
```bash
npx mem version
npx mem help
```

### Initial Configuration
```bash
npx mem init
```
The interactive setup configures data directories, model assets, and local endpoints.

## CLI Commands

| Command | Description |
|---|---|
| `mem init` | Interactive initialization wizard |
| `mem context create [slug]` | Create a new permanent or temporary context |
| `mem context ls` | List available contexts and expiration statuses |
| `mem context rm <slug>` | Remove a context and associated records |
| `mem get [id-or-name]` | Retrieve memory records or default context guide |
| `mem upsert [content]` | Store or update a memory record |
| `mem delete <id>` | Delete a memory record |
| `mem search <query>` | Hybrid keyword and semantic vector search |
| `mem sync` | Synchronize file records with the vector search index |
| `mem status` | Report store and index health |
| `mem mcp` | Launch Model Context Protocol server over stdio |
| `mem version` | Print binary version and runtime environment |

## Architecture

- **Primary Binary**: Built from `./cmd/mem` using Go standard libraries and pure-Go/WASM SQLite bindings (`ncruces/go-sqlite3` + `sqlite-vec`).
- **Storage Subsystem**: Markdown files backed by transactional metadata in SQLite with FTS5 and vector indexing.
- **Packaging**: Multi-platform distribution using a thin Node.js wrapper with platform-specific native binary dependencies.

## Building from Source

Prerequisites: Go 1.24+ and Node.js 18+ (for packaging).

```bash
# Build local executable
go build -trimpath -ldflags "-s -w" ./cmd/mem

# Run tests
go test -v ./...

# Build npm packages
node packaging/scripts/build.mjs
```

## License

MIT
