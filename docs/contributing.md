# Contributing

`mem` is a Go program with an embedded React dashboard, a CLI, an MCP server and a search engine. Here is how to build it and where things live.

## Requirements

- Go 1.23 or newer.
- Node.js 18 or newer, for the dashboard and the packaging scripts.

Windows is the primary development platform. Everything also builds for Linux and macOS.

## Build and run

```bash
# dashboard assets (needed for the real UI; a fallback page keeps plain builds working)
cd web
npm ci
npm run build:real
cd ..

go build -o mem ./cmd/mem
./mem version
```

`go build ./cmd/mem` works without the dashboard step, but the embedded UI will be the static fallback page.

## Tests

Run everything the CI runs:

```bash
go build ./...
go vet ./...
go test ./... -count=1
```

Tests must not touch your real `~/.mem` or real config. The `internal/testenv` package points the home, config and cache directories at a temporary tree for every test package that uses it. If you add a package that reads or writes user paths, call `testenv.Main(m)` in `TestMain`.

## Cross-builds

The project ships static binaries with `CGO_ENABLED=0` for six targets:

```bash
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./cmd/mem
CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build ./cmd/mem
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build ./cmd/mem
CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build ./cmd/mem
CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build ./cmd/mem
CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build ./cmd/mem
```

No cgo, no dynamic runtime dependencies. Keep it that way.

## Repository layout

| Path | What it is |
|---|---|
| `cmd/mem` | The binary's `main`: sets the version and calls the CLI |
| `internal/cli` | Every command, flag parsing, exit codes, JSON output |
| `internal/config` | `config.toml` loading, defaults, validation |
| `internal/store` | Markdown files plus metadata SQLite; the source of truth |
| `internal/search` | Chunking, FTS5, vectors, fuzzy expansion, fusion |
| `internal/llm` | `llama-server` sidecar: start, embed, rerank, idle stop |
| `internal/engine` | Ties store and search together for CLI, MCP and server |
| `internal/mcp` | MCP server over stdio (hand-written JSON-RPC) |
| `internal/server` | Dashboard HTTP server, REST API, security middleware |
| `internal/setup` | `mem init`: asset table, downloads, verification |
| `internal/testenv` | Test isolation for home/config/cache paths |
| `web/` | React dashboard, built into `web/dist` and embedded with `go:embed` |
| `skills/mem` | The agent skill text, embedded and printed by `mem skill` |
| `packaging/` | npm packaging, cross-builds, release scripts |
| `spike/` | Early experiments and measurements, kept for reference |
| `docs/` | This documentation site |

The design rule is: markdown files on disk are authoritative; SQLite is a derived index that can be rebuilt.

## Dashboard development

```bash
cd web
npm ci
npm run build:real   # production bundle into web/dist (what the binary embeds)
npm test             # frontend tests
```

There is also a mock mode (`npm run build:mock`) that writes to `dist-mock` and keeps the mock service worker, so experiments never overwrite the embedded assets. Rebuild `go build ./cmd/mem` after changing `web/dist`.

## Changing the docs

This site is Docsify: plain markdown in `docs/`, no build step. Preview it with any static server:

```bash
npx --yes docsify-cli serve docs
# or
python -m http.server 3000 --directory docs
```

Then open the printed URL.

## Documentation for AI tools

Two generated files let AI tools read the docs: `llms.txt` follows the [llmstxt.org](https://llmstxt.org) convention with a summary, key facts and links to the Markdown pages, and `llms-full.txt` holds every page in one file. They are built from the pages listed in `_sidebar.md` plus the hand-written intro in `llms-intro.md`, so do not edit them by hand. Regenerate after editing any page:

```bash
node docs/scripts/build-llms.mjs
```

`node docs/scripts/build-llms.mjs --check` fails when the committed files are out of date, and `node docs/scripts/check-links.mjs` also verifies that every link in `llms.txt` resolves to a real page and that the code fences in `llms-full.txt` are balanced.

## Packaging and release

Packaging lives in `packaging/`. The everyday commands are:

```bash
node packaging/scripts/build.mjs     # cross-compile and assemble packages
node packaging/scripts/verify.mjs    # structural and mapping checks
node packaging/scripts/pack.mjs      # tarballs and file audit
node packaging/scripts/version.mjs   # show or bump the version
```

Without `--publish-ready`, builds are marked `"private": true`, so a mistake cannot publish to npm. The release workflow is tag-driven; see [packaging/README.md](https://github.com/07prajwal2000/memory/blob/main/packaging/README.md) for the full flow, package names and the current publishing status.

## Style

The codebase prefers the Go standard library, small packages, table-driven tests and no speculative abstractions. Match the surrounding code. If a change affects a command, a flag or an MCP tool, update the help text, the tests and these docs together.

<div class="next-steps">

**Next steps:** [Changelog](changelog.md)

</div>
