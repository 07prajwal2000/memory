# FAQ

## Is my data private?

Yes. Memories are plain markdown files in a folder you choose. Search, embedding and reranking all run locally. `mem` does not send your notes anywhere.

The only network access is during `mem init`, which downloads the pinned `llama-server` build and the embedding model (and the reranker if you ask for it). After that, `mem` works offline.

## Does it work offline?

Yes, after setup. To install without network access, download the assets on another machine and use:

```bash
mem init --offline-dir /path/to/folder
```

The folder must contain the pinned archive (or an unpacked `llama-server`) and the model files. `mem` still verifies their hashes.

## How big can the store get?

A single memory is capped at 1 MiB, and that is far more than a useful note needs. In benchmarks, the search pipeline handled **200,000 chunks** at about 150 ms p95, so a large store is practical on a laptop. Disk usage is the markdown files plus the derived index; the index is roughly the size of the text plus one small vector per chunk.

Practical guidance: keep notes short and one-topic. Ten focused notes search better than one long document.

## Can several agents use it at once?

Yes. Every agent runs its own `mem mcp` process against the same data directory. Writes are synchronous, versions prevent silent overwrites, and the index notices changes made by other processes. If two writers hit the same note at the same moment, one gets a conflict and merges, exactly as a human editor would.

## How is this different from `AGENTS.md` or `CLAUDE.md`?

`AGENTS.md` and `CLAUDE.md` are files an agent reads when it works in a repository. They are great for project instructions, but they are per-repo and loaded wholesale.

`mem` is a searchable store that spans sessions, projects and agents:

- the default file (`global/readme.md`) is the global equivalent of those files, delivered as MCP `instructions`;
- contexts separate projects without copying files into every repository;
- search lets an agent pull only the relevant note instead of reading everything;
- versions, tags and temp contexts cover edits and handoffs, which a plain file cannot.

Most people use both: repo files for build commands and layout, `mem` for decisions, preferences and history that should follow the agent everywhere.

## Which operating systems are supported?

Prebuilt binaries exist for Windows, macOS and Linux, on x64 and arm64. They are static Go binaries (`CGO_ENABLED=0`), so Linux builds do not depend on glibc or musl.

## Which agents work with it?

Any MCP client that can launch a stdio server. Claude Code has a one-line command; clients that use `mcpServers` JSON use the same command or block. The skill installer knows `claude-code`, `command-code`, the universal `.agents` format, `antigravity`, `opencode`, `cursor`, `gemini-cli` and `github-copilot`.

## Can an agent create a context?

No, by design. Contexts are a user decision, made from the CLI. Agents can read and write memories inside the contexts you created. If an agent needs a scratch space, create a temp context and tell it the slug.

## Do I need the reranker?

No. Reranking is off by default. The tiny reranker we tested did not improve results enough to justify the extra model, memory and latency. Enable it with `mem init --with-rerank` and `mem config rerank true` if you want to experiment.

## What language does search understand?

The bundled embedding model is English-focused (`bge-small-en-v1.5`). Keyword search works in any language that the tokenizer indexes, but semantic matching for non-English text will be weaker.

## What happens if I delete the data directory?

You lose the memories. The markdown files are not stored anywhere else. Back up the data directory; see [Back up or move your data](recipes.md#5-back-up-or-move-your-data).

## Can I edit the markdown by hand?

Yes. The files are the source of truth. After editing, run `mem sync` so the index matches. If you delete a file by hand, `mem sync` removes it from the index; `mem status` tells you when the index is out of sync.

## Is it a note-taking app for me?

Not really. The dashboard is for inspecting and correcting memories, not for long-form writing. The product is built around what an agent needs to recall: short, specific, searchable notes.

## What license is it under?

MIT. See the [repository](https://github.com/07prajwal2000/memory).

<div class="next-steps">

**Next steps:** [Contributing](contributing.md) → [Changelog](changelog.md)

</div>
