# Changelog

Notable changes. This project follows semantic versioning.

## 0.2.0

### Added

- Per-project default context: an optional `.agent-memory` file (`context = "<slug>"`) in a project folder or any parent. Calls without a context use it instead of `global` for `upsert`, `search`, `get` and `ls`, in the CLI and the MCP server. An explicit context always wins; a marker naming a missing context fails writes and falls back to `global` for reads. `mem status` shows the detected file, and the MCP `initialize` instructions mention it. See [Configuration](configuration.md#project-file-agent-memory).

## 0.1.1

### Added

- `mem --version` and `mem -v` print the version, same as `mem version`.

### Fixed

- Test-suite fixes found by running CI on Linux, macOS and Windows (no behavior change for users).

## 0.1.0

The first release.

### Added

- `mem` CLI: `init`, `context create|ls|rm|expiry`, `ls`, `get`, `upsert`, `delete`, `search`, `sync`, `status`, `config`, `server`, `mcp`, `skill`, `version`, `help`.
- Markdown memories on disk with a derived SQLite index; contexts, tags, versions and optimistic concurrency (`--if-version`).
- Temp contexts with expiry (7 days by default) and a reserved, permanent `global` context.
- Hybrid search: BM25 keywords plus local `bge-small-en-v1.5` embeddings, merged with reciprocal rank fusion.
- Fuzzy search on by default: prefix expansion, typo tolerance and stemming, with per-query and config switches.
- MCP server over stdio with four tools: `search`, `get`, `upsert`, `delete`.
- Agent skill with installers for Claude Code, Command Code, Cursor, Gemini CLI and others, or the universal `.agents` layout.
- Local dashboard with markdown editor, search spotlight, status page and version-conflict handling.
- `mem init` with resumable, hash-verified downloads; offline installs with `--offline-dir`; `--force` repair.
- Safe-by-default dashboard: loopback binding, Host and Origin checks, optional bearer token, CSP.

### Known issues

- The help table still labels `skill` as "not available yet"; the command works.

<div class="next-steps">

**Next steps:** back to [What is mem?](README.md)

</div>
