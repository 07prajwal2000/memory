# Changelog

Notable changes. This project follows semantic versioning.

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

- The npm packages are not published yet. Build from source until they are.
- The help table still labels `skill` as "not available yet"; the command works.

<div class="next-steps">

**Next steps:** back to [What is mem?](README.md)

</div>
