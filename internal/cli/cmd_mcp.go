package cli

import (
	"flag"
	"fmt"
	"log"

	"memory/internal/engine"
	"memory/internal/mcp"
)

// mcpCommand serves the Model Context Protocol over stdio. Nothing except
// protocol messages may reach stdout: the engine logger and the MCP logger both
// write to stderr.
func mcpCommand() command {
	return command{
		name:  "mcp",
		usage: "mem mcp",
		parse: func(fs *flag.FlagSet, a *app, args []string) ([]string, error) {
			return collectArgs(fs, args)
		},
		run: runMCP,
	}
}

func runMCP(a *app, pos []string) (int, error) {
	if len(pos) > 0 {
		return ExitUsage, usagef("mem mcp takes no arguments")
	}
	// WithoutInitialSync keeps startup instant and guarantees that initialize
	// and tools/list never spawn a model. Resident vectors fit a long-lived
	// process: they populate in the background while SQLite serves queries.
	eng, err := a.openEngineWith(engine.WithoutInitialSync(), engine.WithResidentVectors(true))
	if err != nil {
		return ExitError, fmt.Errorf("mem mcp: %w", err)
	}
	defer eng.Close()

	if err := mcp.Serve(
		a.context(),
		a.io.In,
		a.io.Out,
		eng,
		mcp.WithServerVersion(Version),
		mcp.WithLogger(log.New(a.io.Err, "[mcp] ", log.LstdFlags)),
	); err != nil {
		fmt.Fprintf(a.io.Err, "mem mcp: %v\n", err)
		return ExitError, nil
	}
	return ExitOK, nil
}
