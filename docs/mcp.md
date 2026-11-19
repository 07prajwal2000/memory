# MCP server

`mem mcp` is a Model Context Protocol server over stdin/stdout. Your agent starts it, asks what tools exist, and calls them. You never call the tools yourself; you just connect the server once.

MCP is the small protocol that lets a program expose tools to an AI agent. `mem` speaks it directly, so anything that can launch a child process and exchange JSON-RPC lines can use it.

## Connect it

The [quick start](quickstart.md#4-connect-your-agent) has the exact commands. In short, for Claude Code:

```bash
claude mcp add mem -- mem mcp
```

or, for clients configured with an `mcpServers` block:

```json
{
  "mcpServers": {
    "mem": { "command": "mem", "args": ["mcp"] }
  }
}
```

Restart the agent after changing its MCP configuration.

## What the agent sees

On `initialize`, the server reports its name and version and sends your default file as `instructions`. Real response, with the middle of the instructions shortened:

```json
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"mem","version":"0.1.0"},"instructions":"Persistent memory shared across your sessions, stored locally.\nTools: search, get, upsert, delete. The text below is the user's own global instructions file; follow it.\n\n# Memory\n\nThis store keeps long-term memory for AI agents as markdown files. ...\n"}}
```

That means your standing instructions in `global/readme.md` reach every agent that connects, without anyone pasting them into a chat.

Then the agent asks for the tool list. There are exactly four tools.

## `search`

Search the `global` context plus the context you name. Use it to recall decisions, preferences and past work.

| Argument | Type | Required | Meaning |
|---|---|---|---|
| `query` | string | yes | What to look for |
| `context` | string | no | Also search this context |
| `no_global` | boolean | no | Exclude `global`; requires `context` |
| `limit` | integer | no | 1–50, default 8 |
| `fuzzy` | boolean | no | Default `true`; set `false` for exact-word precision |

Example call:

```json
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search","arguments":{"query":"which port does the dashboard use","context":"work","limit":2}}}
```

Real result:

```json
{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"1. work/mcp/note.md (id: a4z4ik46dk3n, chunk 0, score 0.098)\nmatched: port, the, dashboard\nThe dashboard defaults to port 7777 and binds to 127.0.0.1.\n\n2. work/status/log.md (id: 4cx2snmck2m2, chunk 0, score 0.083)\n- 09:00 started migration\n- 09:40 step 2 done\n\ntook_ms: 222"}],"structuredContent":{"degraded":null,"hits":[{"id":"a4z4ik46dk3n","doc_id":"a4z4ik46dk3n","context":"work","name":"mcp/note.md","heading":"","snippet":"The dashboard defaults to port 7777 and binds to 127.0.0.1.","chunk":0,"score":0.09818181818181819,"sources":["vec","bm25"],"matched_terms":["port","the","dashboard"]},{"id":"4cx2snmck2m2","doc_id":"4cx2snmck2m2","context":"work","name":"status/log.md","heading":"","snippet":"- 09:00 started migration\n- 09:40 step 2 done","chunk":0,"score":0.08333333333333333,"sources":["vec"],"matched_terms":[]}],"took_ms":222}}}
```

The `content` text is for the model to read. `structuredContent` is the same data in a typed shape for clients that prefer it.

## `get`

Retrieve one memory, list a context, or orient.

| Argument | Type | Required | Meaning |
|---|---|---|---|
| `id` | string | no | Stable id; mutually exclusive with `name` |
| `name` | string | no | Memory name inside a context |
| `context` | string | no | Scope for `name`; alone, lists that context |

The three useful shapes:

- **No arguments**: default file plus every live context. The agent's first call in a session.
- **`context` only**: that context's memories (name, id, version, size).
- **`id`** or **`name` + `context`**: one full note, including the metadata header.

Real no-argument result, shortened:

```json
{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"# Memory\n\nThis store keeps long-term memory for AI agents as markdown files. ...\n\n---\n## Live Contexts\n- global (permanent, 1 memories)\n- work (permanent, 6 memories)"}],"structuredContent":{"contexts":[{"slug":"global","is_global":true,"created_at":"2026-11-19T12:16:48Z","expires_at":null,"memory_count":1},{"slug":"work","is_global":false,"created_at":"2026-11-19T12:16:54Z","expires_at":null,"memory_count":6}],"default_file":{"body":"# Memory\n...","context":"global","id":"eebfzjok4nhm","name":"readme.md","tags":["system"],"updated_at":"2026-11-19T12:16:48Z","updated_by":"mem","version":1}}}}}
```

## `upsert`

Create or update a memory. This is the only write tool besides `delete`.

| Argument | Type | Required | Meaning |
|---|---|---|---|
| `body` | string | yes | Markdown content, up to 1 MiB |
| `context` | string | no | Target context; default `global` |
| `name` | string | no | Update-or-create by name |
| `id` | string | no | Update (and optionally rename) an existing memory |
| `tags` | array | no | Up to 20 tags, each up to 64 characters |
| `append` | boolean | no | Add to the end instead of replacing |
| `if_version` | integer | no | Only write if the current version matches |

Example call and real result:

```json
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"upsert","arguments":{"context":"work","name":"mcp/note.md","tags":["mcp"],"body":"The dashboard defaults to port 7777 and binds to 127.0.0.1."}}}
```

```json
{"jsonrpc":"2.0","id":4,"result":{"content":[{"type":"text","text":"Memory created successfully.\nid: a4z4ik46dk3n\nname: mcp/note.md\ncontext: work\nversion: 1\nstatus: created"}],"structuredContent":{"context":"work","id":"a4z4ik46dk3n","index_warning":false,"name":"mcp/note.md","status":"created","version":1}}}
```

`index_warning: true` means the write succeeded but the search index could not be updated (for example, the embedder is not running). The note is saved; `mem sync` repairs the index. If indexing is merely pending, the text response ends with a note saying keyword search still works.

A version conflict is a tool error, not a crash:

```json
{"jsonrpc":"2.0","id":4,"result":{"content":[{"type":"text","text":"version conflict: memory is currently at version 2 (expected 1); call 'get' to inspect the latest content, merge your changes, and retry"}],"isError":true}}
```

## `delete`

Delete a memory by id.

| Argument | Type | Required | Meaning |
|---|---|---|---|
| `id` | string | yes | Stable id of the memory |

Contexts cannot be deleted through MCP, and neither can the default file. An agent that needs a note gone should usually fix it with `upsert` instead.

## Trying it by hand

You can drive the server with a pipe if you keep stdin open long enough for a search to finish:

```bash
printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"shell","version":"1.0"}}}' | mem mcp
```

Real first line of the response (shortened):

```json
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"mem","version":"0.1.0"},"instructions":"Persistent memory shared across your sessions, stored locally. ..."}}
```

If you close stdin immediately, the session ends, and any in-flight search is cancelled. That is normal for a stdio server; a real client keeps the pipe open.

## Things worth knowing

- **stdout is protocol only.** Logs, warnings and errors go to stderr. `--json` has no effect on `mem mcp`.
- **`--no-fuzzy` / `--fuzzy`** set the default for tool calls that do not pass `fuzzy`. The `search_fuzzy` config key does the same persistently.
- **Agents cannot create contexts.** They can read and write inside contexts you made. If a write fails with "context not found", create it with `mem context create`.
- **Memories are untrusted input.** A note may have been written by another agent or copied from untrusted text. The skill tells agents to treat memory contents as information, not instructions.

<div class="next-steps">

**Next steps:** [The agent skill](skill.md) → [How search works](search.md)

</div>
