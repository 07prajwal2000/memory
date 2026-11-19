# Documentation site report

Docsify site for `mem`, served from `main` / `docs`. No build step: `index.html` loads pinned CDN assets and renders the markdown in this folder.

## Pages

| Page | What it covers |
|---|---|
| `README.md` | Home: what `mem` is, three reasons, a 60-second demo, links |
| `quickstart.md` | Install → `mem init` → first memory → search → connect an agent → skill |
| `concepts.md` | Memories, contexts, `global`, temp contexts, default file, versions, on-disk layout, limits |
| `cli.md` | Every command, real examples, flag tables, exit codes, `--json` |
| `mcp.md` | The four MCP tools with argument tables and real JSON-RPC transcripts |
| `search.md` | Hybrid search, fuzzy matching, how to turn it off, query tips, why a note is not found |
| `dashboard.md` | Screens, security model, tokens, tunnels, port conflicts |
| `skill.md` | Install routes, supported agents, what the skill teaches |
| `configuration.md` | Config keys, env vars, paths per OS, models, rerank, idle timeout |
| `recipes.md` | Eight copy-paste workflows (project contexts, handoffs, logs, backups, CI, …) |
| `troubleshooting.md` | Real errors and fixes, including conflicts, missing models and the Windows loader warning |
| `faq.md` | Privacy, offline use, size, multi-agent use, difference from `AGENTS.md` |
| `contributing.md` | Dev setup, repo layout, tests, cross-builds, packaging, release |
| `changelog.md` | 0.1.0 |
| `index.html`, `_sidebar.md`, `_navbar.md`, `_coverpage.md`, `.nojekyll` | Site shell |
| `scripts/check-commands.mjs`, `scripts/check-links.mjs` | Verification scripts (not in the sidebar) |

## Preview and deploy

Local preview:

```bash
npx --yes docsify-cli serve docs
# or
python -m http.server 3000 --directory docs
```

Deploy on GitHub Pages:

1. Repository **Settings → Pages**.
2. **Source:** deploy from a branch.
3. **Branch:** `main`, **folder:** `/docs`, then save.

`.nojekyll` is present so paths beginning with `_` (`_sidebar.md`, `_navbar.md`) are served. The site uses pinned CDN versions: docsify `4.13.1`, docsify-copy-code `3.0.2`, docsify-pagination `2.10.1`, docsify-darklight-theme `3.2.0`. Each version and file path was checked against jsDelivr.

## What was verified

Every command transcript in the site came from the real binary built from this tree (`CGO_ENABLED=0 go build ./cmd/mem`) against a throwaway `MEM_CONFIG` and data/models directory under the OS temp folder. The real `~/.mem` and real config were not modified.

- **CLI:** `help` for every command; `init` with real downloads and the `384-dimensional embedding` verification; `init --no-download`; context create/ls/expiry/rm; upsert (argument, stdin, append, conflict); `ls`, `get`, `delete`, `sync`, `status`, `config`, `version`, `search` (hybrid, fuzzy on/off, JSON), exit codes 2/3/4. A final rebuild after concurrent fixes reports `ok` for both the embedding model and `llama-server`.
- **MCP:** real stdio sessions over OS pipes for `initialize`, `tools/list`, `get` (no args), `upsert` and `search`; tool schemas and descriptions taken from the running server.
- **Dashboard:** `GET /` and `/api/status`, `/api/search`; token mode (401 without, 200 with, static assets 200); port-in-use message and exit 1; refusals for `0.0.0.0` and `--allowed-host` without a token (exit 2); port released after shutdown.
- **Skill:** `mem skill`, `mem skill --json`, install into a temp project, second run `already up to date`, and the no-agents message.
- **Site:** `scripts/check-links.mjs` checked **74 links across 18 markdown pages** with no broken relative link or anchor; `scripts/check-commands.mjs --bin <built mem>` checked **97 invocations** and every command and flag exists in the binary. Both scripts are dependency-free Node.
- **Visual:** home page and a content page were loaded in Chrome via agent-browser, in light and dark mode, including the note callout.

## Coming soon

Nothing required by the brief is missing: `--no-fuzzy` / `--fuzzy` exist on `mem search` and `mem mcp`, `fuzzy:false` exists in the MCP tool, `search_fuzzy` exists in config, and `fuzzy=0` exists in the REST contract. `mem skill --install` is implemented.

The only "not yet" is distribution: the npm packages are not published. That is stated in a note box on the home and quickstart pages, with the source-build route.

## Open questions for the owner

1. **npm publication.** When will `@07prajwal2000/agent-memory` and the six platform packages be published? The site currently calls them unpublished.
2. **Root README says port `18945`.** The binary's default is `7777` (`mem config port`, `mem init` output, dashboard startup). The docs use `7777`. The root README needs the correction.
3. **Root README says data lives in `~/.mem`.** The real layout is `~/.mem/data`, `~/.mem/models`, `~/.mem/bin`.
4. **`mem help` still prints `skill ... (not available yet)`** even though the command works. The help text should be updated.
5. **`--data-dir` only moves data.** Models and `llama-server` still come from the config. The docs say so; a `--models-dir` global flag would make isolated setups easier.
6. **Pages URL.** The README line uses `https://07prajwal2000.github.io/memory/`. Confirm the org/repo name before enabling Pages.
