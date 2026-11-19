package cli

import (
	"flag"
	"fmt"
	"strconv"
	"strings"

	"memory/internal/store"
)

// contextCommand dispatches the four context operations. Contexts are created,
// removed and expired only here; agents cannot do it through MCP.
func contextCommand() command {
	return command{
		name:  "context",
		usage: "mem context create [slug] [--temp] [--expiry DAYS] | ls | rm <slug> [--yes] | expiry <slug> <days|none>",
		parse: parseContext,
		run:   runContext,
	}
}

type contextFlags struct {
	sub       string
	temp      bool
	expiry    int
	expirySet bool
	yes       bool
	pos       []string
}

func parseContext(fs *flag.FlagSet, a *app, args []string) ([]string, error) {
	if len(args) == 0 {
		return nil, usagef("mem context needs a subcommand: create, ls, rm or expiry")
	}
	cf := &contextFlags{sub: args[0]}
	switch cf.sub {
	case "create":
		fs.BoolVar(&cf.temp, "temp", false, "create a temp context that expires")
		fs.IntVar(&cf.expiry, "expiry", 0, "expiry in days")
	case "ls", "list":
	case "rm", "delete":
		fs.BoolVar(&cf.yes, "yes", false, "skip the confirmation prompt")
	case "expiry":
	default:
		return nil, usagef("unknown context subcommand %q; use create, ls, rm or expiry", cf.sub)
	}
	pos, err := collectArgs(fs, args[1:])
	if err != nil {
		return nil, err
	}
	cf.expirySet = flagWasSet(fs, "expiry")
	cf.pos = pos
	a.state = cf
	return nil, nil
}

// flagWasSet reports whether the user explicitly passed a flag.
func flagWasSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

func runContext(a *app, _ []string) (int, error) {
	cf := a.state.(*contextFlags)
	switch cf.sub {
	case "create":
		return runContextCreate(a, cf)
	case "ls", "list":
		return runContextList(a, cf)
	case "rm", "delete":
		return runContextRemove(a, cf)
	default:
		return runContextExpiry(a, cf)
	}
}

func runContextCreate(a *app, cf *contextFlags) (int, error) {
	if len(cf.pos) > 1 {
		return ExitUsage, usagef("mem context create takes at most one slug")
	}
	slug := ""
	if len(cf.pos) == 1 {
		slug = cf.pos[0]
	}
	if cf.expirySet && cf.expiry <= 0 {
		return ExitUsage, usagef("--expiry must be a positive number of days, got %d", cf.expiry)
	}
	if err := a.loadConfig(); err != nil {
		return ExitError, err
	}

	// Temp semantics: no slug implies temp; --temp without --expiry uses the
	// configured default; --expiry alone implies temp.
	var days *int
	switch {
	case cf.expiry > 0:
		d := cf.expiry
		days = &d
	case cf.temp || slug == "":
		d := a.cfg.DefaultTempExpiryDays
		days = &d
	}

	s, err := a.openStore()
	if err != nil {
		return ExitError, err
	}
	defer s.Close()
	if _, err := s.SweepExpired(); err != nil {
		return ExitError, err
	}

	c, err := s.CreateContext(slug, days)
	if err != nil {
		return ExitError, err
	}
	if err := a.emit(map[string]any{
		"slug":       c.Slug,
		"created_at": c.CreatedAt.UTC(),
		"expires_at": c.ExpiresAt,
		"expires":    c.ExpiresAt != nil,
	}); err != nil {
		return ExitError, err
	}
	if c.ExpiresAt != nil {
		a.say("created context %s (expires %s)\n", c.Slug, formatExpiry(c.ExpiresAt))
	} else {
		a.say("created context %s\n", c.Slug)
	}
	return ExitOK, nil
}

func runContextList(a *app, cf *contextFlags) (int, error) {
	if len(cf.pos) > 0 {
		return ExitUsage, usagef("mem context ls takes no arguments")
	}
	s, err := a.openStore()
	if err != nil {
		return ExitError, err
	}
	defer s.Close()

	list, err := s.ListContexts()
	if err != nil {
		return ExitError, err
	}
	if a.globals.json {
		rows := make([]map[string]any, 0, len(list))
		for _, c := range list {
			rows = append(rows, map[string]any{
				"slug":       c.Slug,
				"memories":   c.MemoryCount,
				"created_at": c.CreatedAt.UTC(),
				"expires_at": c.ExpiresAt,
			})
		}
		return ExitOK, a.emit(rows)
	}
	if len(list) == 0 {
		a.say("no contexts\n")
		return ExitOK, nil
	}
	a.say("%-24s %8s  %-17s %s\n", "SLUG", "MEMORIES", "CREATED", "EXPIRES")
	for _, c := range list {
		a.say("%-24s %8d  %-17s %s\n", c.Slug, c.MemoryCount, formatTime(c.CreatedAt), formatExpiry(c.ExpiresAt))
	}
	return ExitOK, nil
}

func runContextRemove(a *app, cf *contextFlags) (int, error) {
	if len(cf.pos) != 1 {
		return ExitUsage, usagef("usage: mem context rm <slug> [--yes]")
	}
	slug := cf.pos[0]
	if slug == store.GlobalContext {
		return ExitConflict, fmt.Errorf("%w: the global context cannot be removed", store.ErrReserved)
	}

	s, err := a.openStore()
	if err != nil {
		return ExitError, err
	}
	defer s.Close()
	if _, err := s.SweepExpired(); err != nil {
		return ExitError, err
	}
	c, err := s.GetContext(slug)
	if err != nil {
		return ExitError, err
	}

	if !cf.yes {
		if !a.io.InIsTTY {
			return ExitUsage, usagef("refusing to delete context %q without --yes (stdin is not a terminal)", slug)
		}
		fmt.Fprintf(a.io.Err, "This permanently deletes context %q and its %d memories.\nType the slug to confirm: ", slug, c.MemoryCount)
		var typed string
		if _, err := fmt.Fscan(a.io.In, &typed); err != nil {
			return ExitError, fmt.Errorf("read confirmation: %w", err)
		}
		if strings.TrimSpace(typed) != slug {
			return ExitError, fmt.Errorf("confirmation did not match %q; nothing was deleted", slug)
		}
	}
	if err := s.DeleteContext(slug); err != nil {
		return ExitError, err
	}
	if err := a.emit(map[string]any{"deleted": slug, "memories": c.MemoryCount}); err != nil {
		return ExitError, err
	}
	a.say("deleted context %s (%d memories)\n", slug, c.MemoryCount)
	return ExitOK, nil
}

func runContextExpiry(a *app, cf *contextFlags) (int, error) {
	if len(cf.pos) != 2 {
		return ExitUsage, usagef("usage: mem context expiry <slug> <days|none>")
	}
	slug := cf.pos[0]
	if slug == store.GlobalContext {
		return ExitConflict, fmt.Errorf("%w: the global context never expires", store.ErrReserved)
	}
	var days *int
	if strings.EqualFold(cf.pos[1], "none") {
		days = nil
	} else {
		n, err := strconv.Atoi(cf.pos[1])
		if err != nil || n <= 0 {
			return ExitUsage, usagef("expiry must be a positive number of days or \"none\", got %q", cf.pos[1])
		}
		days = &n
	}

	s, err := a.openStore()
	if err != nil {
		return ExitError, err
	}
	defer s.Close()
	if err := s.SetContextExpiry(slug, days); err != nil {
		return ExitError, err
	}
	c, err := s.GetContext(slug)
	if err != nil {
		return ExitError, err
	}
	if err := a.emit(map[string]any{"slug": slug, "expires_at": c.ExpiresAt}); err != nil {
		return ExitError, err
	}
	a.say("context %s now expires %s\n", slug, formatExpiry(c.ExpiresAt))
	return ExitOK, nil
}
