package skills_test

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"memory/internal/cli"
	"memory/internal/config"
	"memory/internal/mcp"
	"memory/internal/store"
	"memory/skills"
)

// checkDrift parses skill markdown content and verifies all claims against the real codebase.
// Returns a slice of errors found (empty if no drift).
func checkDrift(content []byte) []error {
	var errs []error

	// 1. Frontmatter check
	fm, err := skills.ParseFrontmatter(content)
	if err != nil {
		errs = append(errs, fmt.Errorf("frontmatter syntax error: %w", err))
	} else {
		if err := fm.Validate("mem"); err != nil {
			errs = append(errs, fmt.Errorf("frontmatter validation error: %w", err))
		}
	}

	contentStr := string(content)

	// 2. CLI commands and flags check
	cliErrs := checkCLICommands(contentStr)
	errs = append(errs, cliErrs...)

	// 3. MCP tool schemas and arguments check
	mcpErrs := checkMCPToolsAndArgs(contentStr)
	errs = append(errs, mcpErrs...)

	// 4. Facts check
	factErrs := checkFacts(contentStr)
	errs = append(errs, factErrs...)

	return errs
}

var (
	fencedBlockRegex = regexp.MustCompile("(?s)```(.*?)```")
	inlineCodeRegex  = regexp.MustCompile("`([^`\r\n]+)`")
	flagRegex        = regexp.MustCompile(`--[a-z0-9-]+`)
)

func checkCLICommands(text string) []error {
	var errs []error

	// 1. Extract commands from fenced blocks
	matches := fencedBlockRegex.FindAllStringSubmatch(text, -1)
	for _, match := range matches {
		block := match[1]
		lines := strings.Split(block, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			// Remove comments
			if idx := strings.Index(line, "#"); idx != -1 {
				line = strings.TrimSpace(line[:idx])
			}
			if !strings.HasPrefix(line, "mem ") {
				continue
			}

			cmd, flags := parseCommandLine(line)
			if cmd != "" {
				if err := cli.ValidateCommandFlags(cmd, flags); err != nil {
					errs = append(errs, fmt.Errorf("CLI command in fenced block %q: %w", line, err))
				}
			}
		}
	}

	// 2. Remove fenced blocks before scanning for inline code to prevent fence backtick collisions
	textWithoutFenced := fencedBlockRegex.ReplaceAllString(text, "")
	inlines := inlineCodeRegex.FindAllStringSubmatch(textWithoutFenced, -1)
	for _, m := range inlines {
		code := strings.TrimSpace(m[1])
		if strings.HasPrefix(code, "mem ") {
			cmd, flags := parseCommandLine(code)
			if cmd != "" {
				if err := cli.ValidateCommandFlags(cmd, flags); err != nil {
					errs = append(errs, fmt.Errorf("CLI command in inline code %q: %w", code, err))
				}
			}
		}
	}

	return errs
}

func parseCommandLine(line string) (string, []string) {
	line = strings.TrimPrefix(line, "mem ")
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", nil
	}

	var cmd string
	var flags []string

	if fields[0] == "context" && len(fields) > 1 && !strings.HasPrefix(fields[1], "-") && !strings.HasPrefix(fields[1], "<") && !strings.HasPrefix(fields[1], "[") {
		cmd = "context " + fields[1]
	} else {
		cmd = fields[0]
	}

	allFlags := flagRegex.FindAllString(line, -1)
	for _, f := range allFlags {
		flags = append(flags, strings.TrimPrefix(f, "--"))
	}

	return cmd, flags
}

func checkMCPToolsAndArgs(text string) []error {
	var errs []error

	tools := mcp.BuiltinTools()
	toolSchema := make(map[string]map[string]bool)
	allProps := make(map[string]bool)
	for _, t := range tools {
		props := make(map[string]bool)
		for p := range t.InputSchema.Properties {
			props[p] = true
			allProps[p] = true
		}
		toolSchema[t.Name] = props
	}

	// 1. Check tool names mentioned in SKILL.md
	expectedTools := []string{"search", "get", "upsert", "delete"}
	for _, exp := range expectedTools {
		if _, ok := toolSchema[exp]; !ok {
			errs = append(errs, fmt.Errorf("expected MCP tool %q not found in schema", exp))
		}
	}

	// 2. Find tool call syntax in text, e.g. upsert(name=..., context=...)
	toolCallRegex := regexp.MustCompile(`(search|get|upsert|delete)\(([^)]*)\)`)
	calls := toolCallRegex.FindAllStringSubmatch(text, -1)
	for _, call := range calls {
		toolName := call[1]
		argString := call[2]
		props, ok := toolSchema[toolName]
		if !ok {
			errs = append(errs, fmt.Errorf("unknown MCP tool %q in call: %s", toolName, call[0]))
			continue
		}

		// Find argument names: key=
		argRegex := regexp.MustCompile(`([a-z_]+)\s*=`)
		argMatches := argRegex.FindAllStringSubmatch(argString, -1)
		for _, am := range argMatches {
			argName := am[1]
			if !props[argName] {
				errs = append(errs, fmt.Errorf("unknown argument %q for MCP tool %q in call: %s", argName, toolName, call[0]))
			}
		}
	}

	// 3. Scan the MCP section (before "## CLI reference") for MCP arguments mentioned in prose/inline code
	mcpSection := text
	if idx := strings.Index(text, "## CLI reference"); idx != -1 {
		mcpSection = text[:idx]
	}

	// Check snake_case parameter names in inline code (e.g. `no_global`, `if_version`)
	snakeRegex := regexp.MustCompile("`([a-z]+_[a-z0-9_]+)`")
	snakeMatches := snakeRegex.FindAllStringSubmatch(mcpSection, -1)
	for _, sm := range snakeMatches {
		argName := sm[1]
		if !allProps[argName] {
			errs = append(errs, fmt.Errorf("unknown MCP argument %q mentioned in MCP section", argName))
		}
	}

	// Check "with `arg`" patterns specifically for upsert
	withRegex := regexp.MustCompile("`upsert`\\s+with\\s+`([a-z_]+)`")
	withMatches := withRegex.FindAllStringSubmatch(mcpSection, -1)
	for _, wm := range withMatches {
		argName := wm[1]
		if !toolSchema["upsert"][argName] {
			errs = append(errs, fmt.Errorf("unknown argument %q for upsert in prose", argName))
		}
	}

	// 4. Verify all expected core arguments exist in the MCP schemas
	expectedArgs := map[string][]string{
		"search": {"query", "context", "no_global", "limit", "fuzzy"},
		"get":    {"name", "id", "context"},
		"upsert": {"name", "id", "context", "body", "tags", "append", "if_version"},
		"delete": {"id"},
	}

	for tool, args := range expectedArgs {
		props, ok := toolSchema[tool]
		if !ok {
			errs = append(errs, fmt.Errorf("tool %q missing from MCP schemas", tool))
			continue
		}
		for _, arg := range args {
			if !props[arg] {
				errs = append(errs, fmt.Errorf("argument %q missing from MCP tool %q schema", arg, tool))
			}
		}
	}

	return errs
}

func checkFacts(text string) []error {
	var errs []error

	// 1. Expiry days fact
	expiryRegex := regexp.MustCompile(`(\d+)\s+days\s+by\s+default`)
	m := expiryRegex.FindStringSubmatch(text)
	if len(m) < 2 {
		errs = append(errs, fmt.Errorf("could not find default expiry days claim in skill text"))
	} else {
		days, _ := strconv.Atoi(m[1])
		if days != config.DefaultTempExpiryDays {
			errs = append(errs, fmt.Errorf("expiry claim %d days does not match config.DefaultTempExpiryDays (%d)", days, config.DefaultTempExpiryDays))
		}
	}

	// 2. Default dashboard port fact
	portStr := strconv.Itoa(config.DefaultPort)
	if !strings.Contains(text, "127.0.0.1:"+portStr) && !strings.Contains(text, ":"+portStr) {
		errs = append(errs, fmt.Errorf("skill does not mention default dashboard port %d", config.DefaultPort))
	}

	// 3. Exit codes fact
	expectedExitLine := fmt.Sprintf("Exit codes: %d ok, %d error, %d usage, %d not found, %d conflict.",
		cli.ExitOK, cli.ExitError, cli.ExitUsage, cli.ExitNotFound, cli.ExitConflict)
	if !strings.Contains(text, expectedExitLine) {
		errs = append(errs, fmt.Errorf("exit codes fact line %q missing or altered in skill", expectedExitLine))
	}

	// 4. Global context fact
	if !strings.Contains(text, "`"+store.GlobalContext+"`") && !strings.Contains(text, store.GlobalContext) {
		errs = append(errs, fmt.Errorf("skill does not mention global context %q", store.GlobalContext))
	}

	// 5. Default file name fact
	defaultPath := store.GlobalContext + "/" + store.DefaultFileName
	if !strings.Contains(text, defaultPath) {
		errs = append(errs, fmt.Errorf("skill does not mention default file path %q", defaultPath))
	}

	return errs
}
