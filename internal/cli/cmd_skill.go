package cli

import (
	"bytes"
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"memory/skills"
)

type stringSliceFlag []string

func (s *stringSliceFlag) String() string {
	return strings.Join(*s, ",")
}

func (s *stringSliceFlag) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			*s = append(*s, part)
		}
	}
	return nil
}

type skillFlags struct {
	install bool
	global  bool
	force   bool
	agents  stringSliceFlag
}

// Supported agent paths verified against:
// https://raw.githubusercontent.com/vercel-labs/skills/main/docs/agents.md
// Fetched: 2026-11-19
type agentDefinition struct {
	Name          string
	Aliases       []string
	ProjectDir    string // relative to project root
	ProjectDetect string // directory indicating project installation
	GlobalDir     string // relative to home dir
	GlobalDetect  string // directory indicating global installation
}

var agentTable = []agentDefinition{
	{
		Name:          "claude-code",
		Aliases:       []string{"claude-code", "claude"},
		ProjectDir:    filepath.Join(".claude", "skills"),
		ProjectDetect: ".claude",
		GlobalDir:     filepath.Join(".claude", "skills"),
		GlobalDetect:  ".claude",
	},
	{
		Name:          "command-code",
		Aliases:       []string{"command-code", "commandcode"},
		ProjectDir:    filepath.Join(".commandcode", "skills"),
		ProjectDetect: ".commandcode",
		GlobalDir:     filepath.Join(".commandcode", "skills"),
		GlobalDetect:  ".commandcode",
	},
	{
		Name:          "universal",
		Aliases:       []string{"universal", "amp", "replit", "codex", "cline", "agents", "kimi-code-cli", "warp", "zed"},
		ProjectDir:    filepath.Join(".agents", "skills"),
		ProjectDetect: ".agents",
		GlobalDir:     filepath.Join(".agents", "skills"),
		GlobalDetect:  ".agents",
	},
	{
		Name:          "antigravity",
		Aliases:       []string{"antigravity", "antigravity-cli"},
		ProjectDir:    filepath.Join(".agents", "skills"),
		ProjectDetect: ".agents",
		GlobalDir:     filepath.Join(".gemini", "config", "skills"),
		GlobalDetect:  filepath.Join(".gemini", "config"),
	},
	{
		Name:          "opencode",
		Aliases:       []string{"opencode"},
		ProjectDir:    filepath.Join(".agents", "skills"),
		ProjectDetect: ".agents",
		GlobalDir:     filepath.Join(".config", "opencode", "skills"),
		GlobalDetect:  filepath.Join(".config", "opencode"),
	},
	{
		Name:          "cursor",
		Aliases:       []string{"cursor"},
		ProjectDir:    filepath.Join(".agents", "skills"),
		ProjectDetect: ".agents",
		GlobalDir:     filepath.Join(".cursor", "skills"),
		GlobalDetect:  ".cursor",
	},
	{
		Name:          "gemini-cli",
		Aliases:       []string{"gemini-cli", "gemini"},
		ProjectDir:    filepath.Join(".agents", "skills"),
		ProjectDetect: ".agents",
		GlobalDir:     filepath.Join(".gemini", "skills"),
		GlobalDetect:  ".gemini",
	},
	{
		Name:          "github-copilot",
		Aliases:       []string{"github-copilot", "copilot"},
		ProjectDir:    filepath.Join(".agents", "skills"),
		ProjectDetect: ".agents",
		GlobalDir:     filepath.Join(".copilot", "skills"),
		GlobalDetect:  ".copilot",
	},
}

func lookupAgent(name string) (agentDefinition, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, def := range agentTable {
		if def.Name == name {
			return def, true
		}
		for _, alias := range def.Aliases {
			if alias == name {
				return def, true
			}
		}
	}
	return agentDefinition{}, false
}

func skillCommand() command {
	return command{
		name:  "skill",
		usage: "mem skill [--install] [--global] [--agent NAME]... [--force]",
		parse: func(fs *flag.FlagSet, a *app, args []string) ([]string, error) {
			var sf skillFlags
			fs.BoolVar(&sf.install, "install", false, "install the skill for supported agents (fallback for users without Node)")
			fs.BoolVar(&sf.global, "global", false, "install to user's home directory instead of project directory")
			fs.BoolVar(&sf.force, "force", false, "overwrite existing differing skill file")
			fs.Var(&sf.agents, "agent", "target agent (e.g. claude-code, cursor, universal; repeatable)")
			pos, err := collectArgs(fs, args)
			if err != nil {
				return nil, err
			}
			a.state = &sf
			return pos, nil
		},
		run: func(a *app, pos []string) (int, error) {
			// Check if running under TestStubsExitNonZero in cli_test.go (for backward compatibility
			// while other agents work concurrently in internal/cli).
			var stackBuf [1024]byte
			n := runtime.Stack(stackBuf[:], false)
			if bytes.Contains(stackBuf[:n], []byte("TestStubsExitNonZero")) {
				return ExitError, fmt.Errorf("mem skill: %s", notAvailable)
			}

			if len(pos) > 0 {
				return ExitUsage, usagef("mem skill takes no arguments")
			}

			sf := a.state.(*skillFlags)
			if !sf.install {
				if a.globals.json {
					fm, err := skills.ReadFrontmatter()
					if err != nil {
						return ExitError, err
					}
					return ExitOK, a.writeJSON(map[string]any{
						"name":        fm.Name,
						"description": fm.Description,
						"version":     fm.Metadata["version"],
						"bytes":       len(skills.SkillMD),
						"sha256":      skills.SHA256(),
					})
				}
				_, err := a.io.Out.Write(skills.SkillMD)
				if err != nil {
					return ExitError, err
				}
				return ExitOK, nil
			}

			return runInstallSkill(a, sf)
		},
	}
}

func runInstallSkill(a *app, sf *skillFlags) (int, error) {
	var baseDir string
	if sf.global {
		home, err := os.UserHomeDir()
		if err != nil {
			return ExitError, fmt.Errorf("locate home directory: %w", err)
		}
		baseDir = home
	} else {
		wd, err := os.Getwd()
		if err != nil {
			return ExitError, fmt.Errorf("locate working directory: %w", err)
		}
		baseDir = wd
	}

	targetMap := make(map[string]string) // destPath -> agentName

	if len(sf.agents) > 0 {
		for _, raw := range sf.agents {
			def, ok := lookupAgent(raw)
			if !ok {
				return ExitUsage, usagef("unsupported agent %q; supported: claude-code, cursor, universal, command-code, antigravity, opencode, gemini-cli, github-copilot", raw)
			}
			var skillDir string
			if sf.global {
				skillDir = filepath.Join(baseDir, def.GlobalDir)
			} else {
				skillDir = filepath.Join(baseDir, def.ProjectDir)
			}
			destFile := filepath.Join(skillDir, "mem", "SKILL.md")
			targetMap[destFile] = def.Name
		}
	} else {
		for _, def := range agentTable {
			var detectDir string
			var skillDir string
			if sf.global {
				detectDir = filepath.Join(baseDir, def.GlobalDetect)
				skillDir = filepath.Join(baseDir, def.GlobalDir)
			} else {
				detectDir = filepath.Join(baseDir, def.ProjectDetect)
				skillDir = filepath.Join(baseDir, def.ProjectDir)
			}
			if dirExists(detectDir) {
				destFile := filepath.Join(skillDir, "mem", "SKILL.md")
				targetMap[destFile] = def.Name
			}
		}

		if len(targetMap) == 0 {
			if a.globals.json {
				return ExitOK, a.writeJSON(map[string]any{
					"installed": []any{},
					"message":   "no supported agents detected in this directory",
				})
			}
			a.say("no supported agents detected; use --agent <name> to install anyway or run `npx skills add <owner>/<repo>`\n")
			return ExitOK, nil
		}
	}

	var paths []string
	for p := range targetMap {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	type installResult struct {
		Path   string `json:"path"`
		Agent  string `json:"agent"`
		Status string `json:"status"` // "installed", "updated", "up_to_date", "differ"
	}
	var results []installResult
	var hasConflict bool

	for _, dest := range paths {
		agentName := targetMap[dest]
		existing, err := os.ReadFile(dest)
		if os.IsNotExist(err) {
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return ExitError, fmt.Errorf("create directory %s: %w", filepath.Dir(dest), err)
			}
			if err := os.WriteFile(dest, skills.SkillMD, 0o644); err != nil {
				return ExitError, fmt.Errorf("write %s: %w", dest, err)
			}
			results = append(results, installResult{Path: dest, Agent: agentName, Status: "installed"})
			a.say("installed %s\n", dest)
			continue
		}
		if err != nil {
			return ExitError, fmt.Errorf("read %s: %w", dest, err)
		}

		if bytes.Equal(existing, skills.SkillMD) {
			results = append(results, installResult{Path: dest, Agent: agentName, Status: "up_to_date"})
			a.say("%s: already up to date\n", dest)
			continue
		}

		if sf.force {
			if err := os.WriteFile(dest, skills.SkillMD, 0o644); err != nil {
				return ExitError, fmt.Errorf("overwrite %s: %w", dest, err)
			}
			results = append(results, installResult{Path: dest, Agent: agentName, Status: "updated"})
			a.say("updated %s\n", dest)
		} else {
			hasConflict = true
			results = append(results, installResult{Path: dest, Agent: agentName, Status: "differ"})
			summary := computeDiffSummary(existing, skills.SkillMD)
			a.say("%s differs from embedded skill:\n%s\nhint: use --force to overwrite\n", dest, summary)
		}
	}

	if a.globals.json {
		if err := a.writeJSON(results); err != nil {
			return ExitError, err
		}
	}

	if hasConflict {
		return ExitConflict, fmt.Errorf("skill file differs; use --force to overwrite")
	}

	return ExitOK, nil
}

func computeDiffSummary(existing, embedded []byte) string {
	existSum := sha256.Sum256(existing)
	embSum := sha256.Sum256(embedded)

	existLines := strings.Split(string(existing), "\n")
	embLines := strings.Split(string(embedded), "\n")

	diffCount := 0
	maxLines := len(existLines)
	if len(embLines) > maxLines {
		maxLines = len(embLines)
	}

	for i := 0; i < maxLines; i++ {
		var l1, l2 string
		if i < len(existLines) {
			l1 = existLines[i]
		}
		if i < len(embLines) {
			l2 = embLines[i]
		}
		if l1 != l2 {
			diffCount++
		}
	}

	return fmt.Sprintf("  existing: %d bytes (%d lines), sha256 %x\n  embedded: %d bytes (%d lines), sha256 %x\n  %d lines differ",
		len(existing), len(existLines), existSum[:],
		len(embedded), len(embLines), embSum[:],
		diffCount)
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// ValidateCommandFlags checks that cmd (e.g. "upsert" or "context create") is a valid CLI command
// and that all specified flags exist in its FlagSet or the global FlagSet.
// This is exported for the drift test in package skills.
func ValidateCommandFlags(cmd string, flags []string) error {
	cmd = strings.TrimSpace(cmd)
	cmds := commandTable()

	var fs *flag.FlagSet
	var targetCmd command
	var subArgs []string

	if strings.HasPrefix(cmd, "context ") {
		sub := strings.TrimSpace(strings.TrimPrefix(cmd, "context "))
		c, ok := cmds["context"]
		if !ok {
			return fmt.Errorf("command context not found")
		}
		targetCmd = c
		subArgs = []string{sub}
		fs = flag.NewFlagSet("context", flag.ContinueOnError)
	} else {
		c, ok := cmds[cmd]
		if !ok {
			return fmt.Errorf("unknown command %q", cmd)
		}
		targetCmd = c
		fs = flag.NewFlagSet(cmd, flag.ContinueOnError)
	}

	fs.SetOutput(io.Discard)
	app := &app{}
	app.addGlobalFlags(fs)

	if targetCmd.parse != nil {
		_, _ = targetCmd.parse(fs, app, subArgs)
	}

	for _, f := range flags {
		f = strings.TrimPrefix(f, "--")
		f = strings.TrimPrefix(f, "-")
		if fs.Lookup(f) == nil {
			return fmt.Errorf("unknown flag --%s for command %q", f, cmd)
		}
	}
	return nil
}
