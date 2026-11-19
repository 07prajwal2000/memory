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
		usage: "mem mcp [--no-fuzzy] [--fuzzy]",
		parse: parseMCP,
		run:   runMCP,
	}
}

type mcpFlags struct {
	noFuzzy    bool
	fuzzy      bool
	noFuzzySet bool
	fuzzySet   bool
}

func parseMCP(fs *flag.FlagSet, a *app, args []string) ([]string, error) {
	var f mcpFlags
	fs.BoolVar(&f.noFuzzy, "no-fuzzy", false, "disable fuzzy search by default in MCP server")
	fs.BoolVar(&f.fuzzy, "fuzzy", false, "force fuzzy search enabled by default in MCP server")
	pos, err := collectArgs(fs, args)
	if err != nil {
		return nil, err
	}
	f.noFuzzySet = flagWasSet(fs, "no-fuzzy")
	f.fuzzySet = flagWasSet(fs, "fuzzy")
	a.state = &f
	return pos, nil
}

func runMCP(a *app, pos []string) (int, error) {
	if len(pos) > 0 {
		return ExitUsage, usagef("mem mcp takes no arguments")
	}
	f, _ := a.state.(*mcpFlags)
	if f != nil && f.fuzzySet && f.noFuzzySet {
		return ExitUsage, usagef("cannot use both --fuzzy and --no-fuzzy")
	}

	opts := []engine.Option{
		engine.WithoutInitialSync(),
		engine.WithResidentVectors(true),
	}
	if f != nil && f.noFuzzySet {
		opts = append(opts, engine.WithFuzzyDefault(false))
	} else if f != nil && f.fuzzySet {
		opts = append(opts, engine.WithFuzzyDefault(true))
	}

	// WithoutInitialSync keeps startup instant and guarantees that initialize
	// and tools/list never spawn a model. Resident vectors fit a long-lived
	// process: they populate in the background while SQLite serves queries.
	eng, err := a.openEngineWith(opts...)
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
