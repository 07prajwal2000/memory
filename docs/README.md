# What is mem?

`mem` gives you and your AI coding agents a shared long-term memory: notes stored as plain markdown files on your disk, searchable by keyword and by meaning.

If you have ever repeated the same context to an agent — the deploy command, the naming rule, the reason you rejected a library — `mem` is for that. Your agent saves the note once. In a later session, in another project, or in a different agent, it searches for it and finds it. You can read and edit every note with a text editor.

> [!NOTE]
> The npm packages are **not published yet**. The install commands on this site show the names they will be published under. Until then, [build from source](quickstart.md#from-source).

## Three reasons to use it

- **It survives the session.** Agents forget everything when the conversation ends. A note in `mem` does not.
- **You own the data.** Memories are markdown files in a folder you choose. The search index is derived and can be rebuilt from the files.
- **It works with the agents you already use.** One command connects Claude Code, and any client that reads `mcpServers` JSON can connect the same way.

## A 60-second demo

After [installing and running `mem init`](quickstart.md), this is a whole session:

```bash
mem context create work
mem upsert --context work --name deploy/staging.md --tag deploy \
  "Deploys run from the release branch only. Staging lives at https://staging.example.internal."

mem search "where do deploys run" --context work --no-global --limit 2
```

Real output from the search:

```text
1. deploy/staging.md (work/fxpkh7wubztw) score=0.1013 [vec,bm25,name]
   matched: deploys, run, deploy
   Deploys run from the release branch only. Staging lives at https://staging.example.internal.
2. release/checklist.md (work/n37iccu7dqwn) score=0.0900 [vec,bm25]
   matched: run
   # Release checklist

   - run tests
   - tag the commit
```

The `[vec,bm25,name]` part tells you which search legs matched: vector similarity (`vec`), keyword search (`bm25`), and the memory name (`name`). You do not need it to use search, but it explains why a result appeared.

The note is also a file on disk. Here is what `mem ls` shows:

```text
NAME                     ID           VERSION  UPDATED
deploy/staging.md        fxpkh7wubztw       1  2026-11-19 12:16
```

## What you get

- **`mem`**, a command-line tool for creating contexts, writing and reading memories, and searching.
- **`mem mcp`**, a Model Context Protocol server over stdio, so an agent can search and write memories as part of its normal work.
- **`mem server`**, a local dashboard at `http://127.0.0.1:7777` for browsing and editing memories in a browser.
- **An agent skill**, a short manual that tells an agent *when* and *how* to use the store, installed with `npx skills add 07prajwal2000/memory` or `mem skill --install`.

## Where to go next

- **New here?** [Quick start](quickstart.md) takes you from zero to an agent that remembers.
- **Want the model?** [Concepts](concepts.md) explains memories, contexts and the default file.
- **Looking for a command?** [Command line](cli.md).
- **Connecting an agent?** [MCP server](mcp.md) and [The agent skill](skill.md).

AI tools can read this documentation as [llms.txt](https://07prajwal2000.github.io/memory/llms.txt) or [llms-full.txt](https://07prajwal2000.github.io/memory/llms-full.txt).

<div class="next-steps">

**Next steps:** [Quick start](quickstart.md) → [Concepts](concepts.md)

</div>
