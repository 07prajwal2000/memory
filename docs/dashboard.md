# Dashboard

`mem server` starts a local web app for browsing and editing your memories. It is the same data the CLI and agents use, with a markdown editor and a search box.

```bash
mem server
```

```text
mem dashboard listening on http://127.0.0.1:7777
```

Open that address. Use `--open` to launch your browser automatically, and `--port N` to choose another port (the config default is `7777`).

## A short tour

- **Contexts sidebar.** Every live context, with its memory count and expiry. `global` is always first. Selecting a context shows its default file and memories.
- **Memory list.** Name, version, size and last update. You can open a memory or create a new one.
- **Markdown editor.** Edit the body and save with `Ctrl+S` (`Cmd+S` on macOS). The editor tracks the version you loaded, so two editors cannot silently overwrite each other.
- **Version conflict dialog.** If someone else saved first, the save is rejected with a conflict, and the dialog offers to copy your text, reload theirs, or overwrite with yours.
- **Search.** `Ctrl+K` (`Cmd+K`) opens the search spotlight. The full search page shows timings and any degraded-search notice.
- **Status page.** Version, data directory, index sync state, and whether the embedding and rerank models are stopped, starting, ready, disabled, or in error.

Real response from the status page's API:

```json
{"version":"0.1.0","data_dir":"...\\demo\\data","uptime_s":3,"config":{"default_temp_expiry_days":7,"rerank":false},"models":{"embed":{"state":"stopped"},"rerank":{"enabled":false,"state":"disabled"}},"index":{"docs":7,"chunks":9,"in_sync":true,"pending":0}}
```

`state: "stopped"` is normal until something needs an embedding. The dashboard does not start the model just to show status.

The dashboard is mobile-friendly: below tablet width the sidebar becomes a navigation drawer.

## Security model

The server is safe by default because it is meant to run on your machine, not on the internet.

- **Loopback only by default.** It binds `127.0.0.1`, so other machines cannot reach it.
- **Host checking.** Requests must carry a `Host` header for a local host (`127.0.0.1`, `localhost`, `[::1]`) or a host you explicitly allow. This blocks DNS-rebinding tricks.
- **Origin checking.** State-changing requests with an `Origin` header must be same-origin. A page on another site cannot make your browser delete memories.
- **No cookies.** Authentication, when enabled, is a bearer token.
- **Content Security Policy.** The app only loads its own scripts and styles; `connect-src 'self'` means the page cannot call other origins.

## Setting a token

If you want a password, pass one on the command line or in a file:

```bash
mem server --token "a-long-random-string"
mem server --token-file ./token.txt
```

The startup line then includes the token so you can click it:

```text
mem dashboard listening on http://127.0.0.1:7777/?token=a-long-random-string
```

The app keeps the token for the session and sends it on every API call. Static files still load without a token so the page can appear; the API refuses without one. Real behaviour:

```text
GET /api/status  (no token)      -> 401
GET /api/status  (Bearer token)  -> 200
GET /               (no token)   -> 200
```

## Running behind a tunnel

A tunnel changes the host name, so you must allow it and you must set a token. The server refuses to start with a non-loopback allowed host and no token:

```bash
mem server --port 7777 --allowed-host my-tunnel.ngrok-free.app
```

```text
mem: refusing to start with non-loopback allowed host "my-tunnel.ngrok-free.app" without an authentication token (pass --token or --token-file)
```

The working form:

```bash
mem server --port 7777 --allowed-host my-tunnel.ngrok-free.app --token "a-long-random-string"
```

`--allowed-host` is repeatable. The same list can live in the config as `allowed_hosts`. Binding to a non-loopback address (for example `--host 0.0.0.0`) also requires a token:

```text
mem: refusing to bind non-loopback host "0.0.0.0" without an authentication token (pass --token or --token-file)
```

Treat a tunnelled dashboard as a public service: anyone with the URL and token can read and edit every memory.

## Port already in use

If another process owns the port:

```text
mem: port 7815 is already in use; specify a different port with --port <port> or stop the existing process
```

Exit code is `1`. Pick another port or stop the other process.

## Stopping it

`Ctrl+C` shuts the server down gracefully: it stops accepting connections, gives in-flight requests up to five seconds, closes the database, and releases the port. Expired contexts are swept on startup and every ten minutes while it runs.

<div class="next-steps">

**Next steps:** [The agent skill](skill.md) → [Configuration](configuration.md)

</div>
