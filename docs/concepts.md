# Concepts

Four ideas explain almost everything `mem` does: **memories**, **contexts**, the **global context**, and the **default file**.

## Memory

A memory is one markdown note. It has:

- a stable **id** (for example `fxpkh7wubztw`), generated once and never reused;
- an optional **name** such as `deploy/staging.md`, which is how you and your agent refer to it;
- a **version** that goes up on every write;
- optional **tags**;
- a **body**, up to 1 MiB of markdown or plain text;
- `updated_at` and `updated_by` metadata.

A memory is not a chat log. It is a small, specific note: a decision and its reason, a preference, a gotcha, a runbook. One topic per note works best.

### Names and unnamed memories

Names are optional, lowercase, and use `/` as a folder separator:

```text
deploy/staging.md
auth/token-refresh.md
prefs/code-style.md
```

If you omit the extension, `.md` is added. If you omit the name entirely, the memory is stored under its id (`fxpkh7wubztw.md`) with `has_name=false`. Named memories are much easier to find by browsing, so give them names when you can.

## Context

A context is a folder of memories. Use one per project, or one per topic:

```bash
mem context create work
```

Real output:

```text
created context work
```

Contexts are created, expired and removed **only from the CLI, by you**. Agents using the MCP server can read and write memories, but they cannot create contexts. If an agent writes to a context that does not exist, the write fails; that is deliberate, so an agent cannot scatter folders across your disk.

List them:

```bash
mem context ls
```

```text
SLUG                     MEMORIES  CREATED           EXPIRES
global                          1  2026-11-19 12:18  never
handoff                         0  2026-11-19 12:18  2026-11-21 12:18
work                            4  2026-11-19 12:16  never
```

Each context has a slug. A slug is 1–32 characters, lowercase letters, digits and single hyphens (`^[a-z0-9]+(-[a-z0-9]+)*$`). `global` is reserved.

## The global context

`global` always exists, never expires, and cannot be removed or expired:

```bash
mem context rm global --yes
```

```text
mem: reserved: the global context cannot be removed
hint: the global context always exists and cannot be removed or expired
```

Search always includes `global` as well as the context you ask for, unless you pass `--no-global`. That is what lets a preference saved once apply everywhere.

Use `global` for knowledge about you or about all your work. Use a project context for knowledge about that project.

To make a project's folder default to its own context instead of `global`, put an `.agent-memory` file in it (`context = "proj"`). An explicit `--context` still wins. See [Project file](configuration.md#project-file-agent-memory).

## Temp contexts

A temp context is an ordinary context with an expiry date. It is made for handing work between agents: one agent writes its working notes, the next agent reads them, and the context disappears when it is no longer needed.

```bash
mem context create handoff --expiry 2   # expires in 2 days
mem context create scratch --temp       # uses the default expiry (7 days)
mem context create                      # auto-named temp context, e.g. tmp-e8ob
```

Real output:

```text
created context handoff (expires 2026-11-21 12:18)
created context scratch (expires 2026-11-26 12:18)
created context tmp-e8ob (expires 2026-11-26 12:15)
```

Change or remove the expiry:

```bash
mem context expiry handoff 14
mem context expiry handoff none
```

```text
context handoff now expires 2026-12-03 12:15
context handoff now expires never
```

An expired context behaves as if it does not exist: reads return "not found" and its memories are removed. Expiry is swept when commands and the dashboard run. If a note must survive, move it to a permanent context first.

## The default file

Every context has a default file, `readme.md`. The one in `global` is special: it is your standing instructions to agents, the same idea as an `AGENTS.md` or `CLAUDE.md`, but delivered through the MCP server.

- When an MCP client connects, the server's `instructions` field contains this file.
- `mem get` with no arguments prints this file, then the contexts, then the current context's memories.
- The dashboard shows it as `global/readme.md`.

Edit it like any other memory:

```bash
mem upsert --name readme.md --context global "Prefer small diffs. Ask before deleting files."
```

The default file cannot be deleted (`delete` refuses it), but you can overwrite it.

## Versions and conflicts

Every write bumps the version. That gives you optimistic concurrency: read a note, then write it back only if nobody changed it in between.

```bash
mem upsert --context work --name auth/token.md --if-version 2 "attempt"
```

```text
mem: conflict: memory "t4epatooy5gv" is at version 1, not 2
hint: fetch the memory again, then retry with the current --if-version
```

Exit code `4` means conflict. The fix is always the same: `get` the note again, merge your change, and retry with the version you just read. Never retry blindly with the old version.

The same rule applies to agents through the MCP `upsert` tool's `if_version` argument.

## Where files live

Everything is under the data directory, which defaults to `~/.mem/data`:

```text
~/.mem/data/
  contexts/
    global/
      readme.md
    work/
      deploy/staging.md
      auth/token.md
  index.db     # metadata: contexts, ids, versions, tags
  search.db    # full-text and vector index, derived
```

The markdown files are the source of truth. `index.db` records the metadata, and `search.db` is a derived index you can rebuild with `mem sync`. Back up the whole data directory if you want everything; copying just `contexts/` gives you the notes themselves.

## Limits worth knowing

| Thing | Limit |
|---|---|
| Context slug | 1–32 characters, lowercase letters, digits, hyphens |
| Memory name | up to 200 characters, `[a-z0-9._/-]`, lowercase |
| Body | 1 MiB |
| Tags | up to 20, each up to 64 characters |
| Search `limit` (CLI/MCP/REST) | up to 50; default 8 |
| Search `per-doc` (REST/CLI) | default 1 chunk per document |

<div class="next-steps">

**Next steps:** [How search works](search.md) → [Command line](cli.md)

</div>
