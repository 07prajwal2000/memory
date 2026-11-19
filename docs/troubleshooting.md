# Troubleshooting

Real errors, what they mean, and the fix. Start with `mem status`; it answers most of these questions directly.

## "embedder unavailable" or "keyword-only results"

You will see this when the local embedding model is not installed or not running:

```text
note: embedder unavailable: sidecar embed url: sidecar start llama-server: exec: "llama-server": executable file not found in %PATH%
1. smoke.md (global/yc2nxix4mt2a) score=0.0073 [bm25]
   matched: release, branch, releas
   Deploys run from the release branch only.
```

Search still works by keyword. Two things to do:

1. Run `mem init` to install the model server and the embedding model.
2. If they are installed but the index is behind, run `mem sync` and check `mem status`.

While the embedder is down, write with words you would actually search for; exact identifiers work best.

## "indexed search is not set up yet"

You set up without downloading models (`mem init --no-download`), or the install failed:

```text
indexed search is not set up yet; run `mem init`
```

Everything else works. Run `mem init` (with network) or `mem init --offline-dir <folder>` when you have the files.

## `mem status` says the model server is broken

```text
missing     embedding ...\models\bge-small-en-v1.5-q8_0.gguf
broken      llama-server (not found)  (llama-server : path is empty)
fix         run `mem init --force` to reinstall the llama-server and models
```

Run the suggested fix:

```bash
mem init --force
```

`--force` reinstalls `llama-server` and the models even when they look valid. Use it after a failed download, a partial install, a moved `models_dir`, or antivirus interference.

## Conflict (exit code 4)

```text
mem: conflict: memory "t4epatooy5gv" is at version 1, not 2
hint: fetch the memory again, then retry with the current --if-version
```

Someone (or something) wrote the note after you read it. Do what the hint says:

```bash
mem get auth/token.md --context work   # read the current version
```

Merge the changes, then write with the version you just read. Do not retry with the old number; it will fail again.

## Not found (exit code 3)

```text
mem: not found: memory "missing.md" does not exist in context "work"
hint: run `mem ls` to list the memories in that context
```

Causes, in order of likelihood:

- the name or id is wrong;
- you are in the wrong context (the default for reads and writes is `global`);
- the context is a temp context that expired.

```bash
mem context ls
mem ls --context work
```

An id that is malformed is a usage error (exit `2`), not "not found".

## Port already in use

```text
mem: port 7815 is already in use; specify a different port with --port <port> or stop the existing process
```

Exit code `1`. Either stop the process using the port or pick another:

```bash
mem server --port 7816
```

## `npm i -g` fails with `EEXIST`

Another global package already owns `mem`, `memory` or `agent-memory`. Either overwrite:

```bash
npm i -g --force @07prajwal2000/agent-memory
```

or avoid the global install and use `npx @07prajwal2000/agent-memory ...`.

## Windows SmartScreen or antivirus blocks `llama-server`

`mem init` downloads a prebuilt `llama-server` binary. Windows may warn about it or quarantine it.

- If SmartScreen asks, choose to keep the file only if you trust the source you configured.
- If antivirus removed it, `mem status` shows it missing or broken. Run `mem init --force` after adding an exclusion for `~/.mem/bin`, or install from an `--offline-dir` you prepared.

Search stays keyword-only until the binary is back.

## A body that starts with `-` is treated as a flag

```text
mem: unknown flag "- 09:00 started migration"; run `mem help` for usage
exit=2
```

The body was passed as an argument beginning with `-`, so the flag parser claimed it. Put the body on stdin and use `-` as the argument:

```bash
echo "- 09:00 started migration" | mem upsert --context work --name status/log.md --append -
```

This is the reliable form for logs and any multi-line text.

## Search results look unrelated

A result with `sources: ["vec"]` and a very low score means only the semantic leg matched, weakly. Common causes:

- the model server is not actually running (check `mem status`);
- the index is out of sync (`mem sync`);
- the query is too generic (`the`, `fix`, `error`);
- the store is large and the useful note is below the result limit (raise `--limit`).

If you want exact behavior, add `--no-fuzzy` and use the words the note contains.

## `mem skill --install` finds no agents

```text
no supported agents detected; use --agent <name> to install anyway or run `npx skills add <owner>/<repo>`
```

You are in a directory without a known agent config folder. Name the agent explicitly:

```bash
mem skill --install --agent claude-code
```

Or use `--global` to install under your home directory.

## Dashboard returns 401

You started the server with `--token` or `--token-file`, but the browser does not have the token. Open the URL printed at startup, which includes `?token=...`:

```text
mem dashboard listening on http://127.0.0.1:7777/?token=secret123
```

If you opened a bare `http://127.0.0.1:7777/`, add `?token=` once. The app keeps it for the session.

<div class="next-steps">

**Next steps:** [FAQ](faq.md) → [Contributing](contributing.md)

</div>
