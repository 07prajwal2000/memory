# mem

> `mem` is local long-term memory for AI coding agents: notes stored as plain markdown on your own disk, searchable by keyword and by meaning. It is for developers who keep re-explaining project context to agents. It gives you four interfaces over one store — a command-line tool (`mem`), an MCP server over stdio, a local browser dashboard, and an agent skill that teaches an agent when to use it. Everything runs on your machine and works offline after `mem init`.

The store is made of **memories** (one markdown note each) organized into **contexts** (folders, one per project or topic). The reserved `global` context always exists, never expires, and is searched by default. Every context has a default file `readme.md`; `global/readme.md` is delivered to MCP clients as their `instructions`. Contexts are created, expired, and removed only from the CLI — agents can read and write memories, but cannot create contexts.

The commands `mem`, `memory`, and `agent-memory` are aliases. Data lives under `~/.mem` (`data/`, `models/`, `bin/`), and the markdown files are the source of truth while the SQLite indexes are derived and rebuildable with `mem sync`. Search is hybrid — BM25 keywords plus `bge-small-en-v1.5` vectors merged by reciprocal rank fusion — with fuzzy matching (prefix expansion, typo tolerance, stemming) on by default. The optional reranker is off by default.

The npm packages are not published yet; build from source until they are. These pages are the Markdown sources served at https://07prajwal2000.github.io/memory/.
