package cli

import (
	"flag"
	"os/exec"
	"runtime"
)

// command is one subcommand: a flag set plus a run function.
type command struct {
	name  string
	usage string
	parse func(fs *flag.FlagSet, a *app, args []string) ([]string, error)
	run   func(a *app, pos []string) (int, error)
}

// commandTable is the whole command surface. Keys are the words a user types,
// including aliases, so `mem ls` and `mem context ls` can share one entry.
func commandTable() map[string]command {
	cmds := []command{
		initCommand(),
		contextCommand(),
		lsCommand(),
		getCommand(),
		upsertCommand(),
		deleteCommand(),
		searchCommand(),
		syncCommand(),
		statusCommand(),
		configCommand(),
		serverCommand(),
		mcpCommand(),
		skillCommand(),
		versionCommand(),
	}
	out := make(map[string]command, len(cmds))
	for _, c := range cmds {
		out[c.name] = c
	}
	return out
}

func versionCommand() command {
	return command{
		name:  "version",
		usage: "mem version",
		parse: func(fs *flag.FlagSet, a *app, args []string) ([]string, error) {
			return collectArgs(fs, args)
		},
		run: func(a *app, pos []string) (int, error) {
			if a.globals.json {
				return ExitOK, a.writeJSON(map[string]any{
					"name":    "mem",
					"version": Version,
					"go":      runtime.Version(),
					"os":      runtime.GOOS,
					"arch":    runtime.GOARCH,
				})
			}
			a.say("mem %s (%s %s/%s, %s)\n", Version, "go", runtime.GOOS, runtime.GOARCH, runtime.Version())
			return ExitOK, nil
		},
	}
}

func isWindows() bool { return runtime.GOOS == "windows" }

// lookPath is exec.LookPath, indirected so tests can stay hermetic.
func lookPath(name string) (string, error) { return exec.LookPath(name) }
