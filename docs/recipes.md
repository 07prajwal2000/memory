# Recipes

Practical setups you can copy. Each one uses only real commands.

## 1. One context per repository

Keep project knowledge separate from personal preferences:

```bash
mem context create acme-api
mem upsert --context acme-api --name deploy/staging.md --tag deploy \
  "Staging deploys run from the release branch. https://staging.example.internal"
mem upsert --context acme-api --name conventions/errors.md \
  "Return wrapped errors with %w; log once at the handler, not in every layer."
```

Search only that project when you mean only that project:

```bash
mem search "staging deploy branch" --context acme-api --no-global
```

Tell agents which context belongs to the repo by adding a line to your default file:

```bash
mem upsert --name readme.md --context global \
  "In the acme-api repo, use memory context 'acme-api'."
```

The default file is delivered as MCP `instructions`, so every connected agent sees it.

## 2. Hand off work between two agents

Create a temp context, write the state, pass the slug to the next agent:

```bash
mem context create handoff --expiry 2
```

```text
created context handoff (expires 2026-11-21 12:18)
```

```bash
mem upsert --context handoff --name state/progress.md \
  "Migration is done through 0042. Next: run 0043, then verify the backfill. Failing test: TestOrderTotals."
```

Tell the next agent: *search context `handoff` and read `state/progress.md`*. It can continue while the notes exist. Before the context expires, move anything durable:

```bash
mem get state/progress.md --context handoff
mem upsert --context acme-api --name migrations/backfill.md "..."   # durable copy
```

Demo agents that use the skill are told not to delete contexts or change expiries on their own.

## 3. Keep a daily log with `--append`

`--append` adds to the end instead of replacing. Pipe the new line from stdin:

```bash
echo "- 09:00 started migration" | mem upsert --context work --name status/log.md --append -
echo "- 09:40 step 2 done" | mem upsert --context work --name status/log.md --append -
```

```text
4cx2snmck2m2 v1 (work/status/log.md)
4cx2snmck2m2 v2 (work/status/log.md)
```

Read it back:

```bash
mem get status/log.md --context work
```

```text
---
# status/log.md (4cx2snmck2m2, v2, updated 2026-11-19 12:17 by cli)
---
- 09:00 started migration
- 09:40 step 2 done
```

Appending still takes the write lock and bumps the version, so two agents appending at the same time do not lose lines.

## 4. Edit safely with `--if-version`

Read the note, note its version, write back only if nobody changed it:

```bash
mem get auth/token.md --context work
```

The header shows `(t4epatooy5gv, v1, ...)`. Now:

```bash
mem upsert --context work --name auth/token.md --if-version 1 "Access tokens expire after 20 minutes."
```

If the version is no longer 1, the write is refused:

```text
mem: conflict: memory "t4epatooy5gv" is at version 1, not 2
hint: fetch the memory again, then retry with the current --if-version
```

The recovery is always: `get` again, merge both changes by hand, retry with the version you just read. The same pattern works through MCP with `if_version`, and through the dashboard's conflict dialog.

## 5. Back up or move your data

Everything that matters is under the data directory (default `~/.mem/data`):

```text
contexts/       the markdown memories
index.db        contexts, ids, versions, tags
search.db       derived search index
```

Back up the whole directory while `mem` is not writing:

```bash
# macOS / Linux
cp -a ~/.mem/data ~/backups/mem-data-$(date +%F)

# Windows PowerShell
Copy-Item -Recurse "$env:USERPROFILE\.mem\data" "$env:USERPROFILE\backups\mem-data"
```

To move to another machine: install `mem`, run `mem init`, then copy the data directory into place (or set `data_dir` to the copied folder). If you copy only `contexts/`, rebuild the metadata and index with `mem sync` and check `mem status`.

To test a restore without touching your real store, point a throwaway config at the copy:

```bash
MEM_CONFIG=/tmp/mem-restore.toml mem --data-dir /tmp/mem-restore-data status
```

## 6. Deal with an expired context

An expired temp context behaves as if it never existed, and its memories are removed. There is no undo button. Two options:

**If you have a backup:** recreate the context and restore its folder, then rebuild the index:

```bash
mem context create recovered
# copy the backed-up contexts/recovered/* files into the data dir
mem sync
mem status
```

**If you do not:** recreate the slug and re-add what you remember. Search the rest of the store first; useful notes often exist in a permanent context:

```bash
mem search "the thing you lost" --limit 20
```

To stop a handoff from expiring while you still need it:

```bash
mem context expiry handoff none
```

## 7. Use it from CI or a script

Set a throwaway config and data directory, use `--json`, and check the exit code:

```bash
export MEM_CONFIG="$PWD/.mem-ci.toml"
export MEM_DATA_DIR="$PWD/.mem-ci-data"
mem init --yes --no-download
mem context create build
mem upsert --context build --name last-run.md "Build 1842 passed on main."

hits=$(mem search "last build" --context build --json)
echo "$hits"
```

A search with no hits is still success and returns `{"hits":[]...}`; do not treat empty output as failure. Errors exit non-zero and print one JSON object, for example:

```json
{"code":3,"error":"not found: memory \"missing.md\" does not exist in context \"work\"","hint":"run `mem ls` to list the memories in that context"}
```

`--no-download` is deliberate in CI: it avoids the 60 MB of models, and keyword search still works for exact identifiers.

## 8. Share one store between several agents

Point every agent at the same `mem` binary and config. Each agent runs its own `mem mcp` process, but they all read and write the same files.

A simple split that works well:

- `global` for preferences and cross-project knowledge;
- one permanent context per repository;
- a fresh temp context per handoff.

Writes are synchronous and versioned, and the index detects changes made by other processes, so an agent does not need a lock. If you see a conflict, it means two writers raced on the same note; the fix is the same `get`-and-merge from recipe 4.

<div class="next-steps">

**Next steps:** [Troubleshooting](troubleshooting.md) → [FAQ](faq.md)

</div>
