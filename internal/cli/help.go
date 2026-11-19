package cli

import (
	"strings"
)

// printHelp writes the command list, or one command's usage.
func (a *app) printHelp(topic string) {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		a.say("%s\n\n", usageHeader)
		return
	}
	if c, ok := commandTable()[topic]; ok {
		a.say("%s\n\n%s\n", c.usage, helpNotes[topic])
		return
	}
	a.say("no help for %q; run `mem help` for the command list\n", topic)
}

const usageHeader = `mem — local long-term memory for AI agents

usage: mem <command> [flags]

commands:
  init                     set up data, models and the local model server
  context create [slug]    create a context (--temp, --expiry DAYS)
  context ls               list contexts
  context rm <slug>        delete a context and its memories (--yes)
  context expiry <slug> <days|none>
  ls [--context C]         list memories of a context (default global)
  get [name-or-id]         read one memory, or the default file and contexts
  upsert [body|-]          create or update a memory
  delete <id>              delete a memory
  search <query...>        hybrid keyword + vector search
  sync                     re-sync files into the search index
  status                   show configuration, models and index state
  config [key] [value]     show, read or set configuration
  server [--port N]        dashboard server
  mcp                      MCP server over stdio
  skill [--install]        agent skill (not available yet)
  version                  print the version
  help [command]           this help

global flags:
  --json                   machine-readable output on stdout
  --data-dir D             override the data directory
  --config PATH            override the config file
  -q, --quiet              suppress hints and progress
  -v, --verbose            extra diagnostics

exit codes: 0 ok, 1 error, 2 usage, 3 not found, 4 conflict`

var helpNotes = map[string]string{
	"upsert": `The body comes from the argument, or from stdin when the argument is "-"
or omitted with piped input. Without --context the memory goes to global.
Writing to an unknown context fails; create it with ` + "`mem context create`" + ` first.`,
	"context": `Contexts are created, removed and expired only from the CLI. ` + "`global`" + `
always exists and can be neither removed nor expired.`,
	"search": `Searches global plus --context unless --no-global is set. Without a
context, --no-global searches nothing. Fuzzy and partial-word matching is on
by default; use --no-fuzzy for exact-word precision, or --fuzzy to force on.`,
	"init": `Interactive setup. --yes takes the defaults and any flags you pass.
--no-download skips the network; --offline-dir copies from a local folder.`,
	"mcp": `Serves the Model Context Protocol on stdin/stdout for agents such as
Claude Code. stdout carries protocol messages only; logs go to stderr. Use
--no-fuzzy to disable fuzzy search by default for agents.`,
}
