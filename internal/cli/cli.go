// Package cli implements the mem command line: argument parsing, exit codes,
// human and JSON output, and the commands themselves. It is the only package
// that turns store and engine errors into user-facing messages.
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"memory/internal/config"
	"memory/internal/engine"
	"memory/internal/setup"
	"memory/internal/store"
)

// Exit codes used by every command.
const (
	ExitOK       = 0
	ExitError    = 1
	ExitUsage    = 2
	ExitNotFound = 3
	ExitConflict = 4
)

// Version is the release version reported by `mem version` and the HTTP
// User-Agent used by `mem init`.
var Version = "0.1.0"

// IO carries the streams a command may use, so tests can drive commands in
// process without touching the real terminal.
type IO struct {
	In       io.Reader
	Out      io.Writer
	Err      io.Writer
	InIsTTY  bool
	OutIsTTY bool
}

// DefaultIO wires the process streams.
func DefaultIO() IO {
	return IO{
		In:       os.Stdin,
		Out:      os.Stdout,
		Err:      os.Stderr,
		InIsTTY:  isTerminal(os.Stdin),
		OutIsTTY: isTerminal(os.Stdout),
	}
}

// Env carries injectable dependencies.
type Env struct {
	// OpenEngine opens the engine. Tests substitute a fake embedder here.
	OpenEngine func(cfg config.Config, opts ...engine.Option) (*engine.Engine, error)
}

// DefaultEnv uses the real engine.
func DefaultEnv() Env {
	return Env{OpenEngine: engine.Open}
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// app is the per-invocation state shared by all commands.
type app struct {
	io      IO
	env     Env
	globals globals

	cfg     config.Config
	cfgPath string
	cfgSet  bool

	// state holds the per-invocation flag values a parse function produced.
	state any

	hinted bool
}

type globals struct {
	json    bool
	quiet   bool
	verbose bool
	dataDir string
	cfgPath string
}

func (a *app) addGlobalFlags(fs *flag.FlagSet) {
	fs.BoolVar(&a.globals.json, "json", a.globals.json, "machine-readable JSON on stdout")
	fs.BoolVar(&a.globals.quiet, "q", a.globals.quiet, "quiet: suppress hints and progress")
	fs.BoolVar(&a.globals.verbose, "v", a.globals.verbose, "verbose diagnostics")
	fs.StringVar(&a.globals.dataDir, "data-dir", a.globals.dataDir, "override the data directory")
	fs.StringVar(&a.globals.cfgPath, "config", a.globals.cfgPath, "override the config file path")
}

// exitError tags an error with an exit code.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

func usagef(format string, args ...any) error {
	return &exitError{code: ExitUsage, err: fmt.Errorf(format, args...)}
}

func notFoundf(format string, args ...any) error {
	return &exitError{code: ExitNotFound, err: fmt.Errorf(format, args...)}
}

func conflictf(format string, args ...any) error {
	return &exitError{code: ExitConflict, err: fmt.Errorf(format, args...)}
}

// Run executes one command line and returns the process exit code.
func Run(args []string, cio IO, env Env) int {
	enableSQLiteCache()
	a := &app{io: cio, env: env}
	code, err := a.dispatch(args)
	if err != nil {
		a.reportError(err, code)
		return exitCodeFor(err, code)
	}
	return ExitOK
}

func exitCodeFor(err error, fallback int) int {
	if err == nil {
		return ExitOK
	}
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		return ExitNotFound
	case errors.Is(err, store.ErrConflict), errors.Is(err, store.ErrExists), errors.Is(err, store.ErrReserved):
		return ExitConflict
	case errors.Is(err, store.ErrInvalid):
		return ExitUsage
	}
	if fallback != 0 {
		return fallback
	}
	return ExitError
}

// dispatch parses leading global flags, then hands the rest to a command.
func (a *app) dispatch(args []string) (int, error) {
	if len(args) == 0 {
		a.printHelp("")
		return ExitOK, nil
	}

	gfs := flag.NewFlagSet("mem", flag.ContinueOnError)
	gfs.SetOutput(io.Discard)
	a.addGlobalFlags(gfs)
	idx, err := consumeLeadingFlags(gfs, args)
	if err != nil {
		return ExitUsage, err
	}
	if idx >= len(args) {
		a.printHelp("")
		return ExitOK, nil
	}

	name := args[idx]
	rest := args[idx+1:]
	cmd, ok := commandTable()[name]
	if !ok {
		if name == "help" {
			a.printHelp(strings.Join(rest, " "))
			return ExitOK, nil
		}
		return ExitUsage, usagef("unknown command %q; run `mem help` for the command list", name)
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	// Global flags are also accepted after the command name, matching the
	// documented usage lines.
	a.addGlobalFlags(fs)
	pos, err := cmd.parse(fs, a, rest)
	if err != nil {
		return ExitUsage, err
	}
	return cmd.run(a, pos)
}

// reportError writes one clear line plus a hint.
func (a *app) reportError(err error, fallback int) {
	code := exitCodeFor(err, fallback)
	msg := withSidecarHint(err.Error())
	hint := hintFor(err, msg)
	if a.globals.json {
		payload := map[string]any{"error": msg, "code": code}
		if hint != "" {
			payload["hint"] = hint
		}
		_ = a.writeJSON(payload)
		fmt.Fprintf(a.io.Err, "mem: %s\n", msg)
	} else {
		fmt.Fprintf(a.io.Err, "mem: %s\n", msg)
	}
	if hint != "" && !a.globals.json {
		fmt.Fprintf(a.io.Err, "hint: %s\n", hint)
	}
}

func hintFor(err error, msg string) string {
	if strings.Contains(msg, "does not exist; create it with") {
		return ""
	}
	switch {
	case errors.Is(err, store.ErrNotFound) && strings.Contains(msg, "memory"):
		return "run `mem ls` to list the memories in that context"
	case errors.Is(err, store.ErrNotFound):
		return "run `mem context ls` to see the contexts that exist"
	case errors.Is(err, store.ErrConflict):
		return "fetch the memory again, then retry with the current --if-version"
	case errors.Is(err, store.ErrReserved):
		return "the global context always exists and cannot be removed or expired"
	case errors.Is(err, store.ErrInvalid):
		return "run `mem help <command>` for the accepted arguments"
	}
	return ""
}

// consumeLeadingFlags parses flags until the first positional argument and
// returns its index.
func consumeLeadingFlags(fs *flag.FlagSet, args []string) (int, error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return i + 1, nil
		}
		if len(arg) < 2 || arg[0] != '-' {
			return i, nil
		}
		consumed, err := setFlagArg(fs, arg, args[i:])
		if err != nil {
			return 0, err
		}
		i += consumed - 1
	}
	return len(args), nil
}

// setFlagArg applies one "--name=value" or "--name value" occurrence. It
// returns how many argv entries were consumed.
func setFlagArg(fs *flag.FlagSet, arg string, rest []string) (int, error) {
	name := strings.TrimLeft(arg, "-")
	value := ""
	hasValue := false
	if i := strings.IndexByte(name, '='); i >= 0 {
		name, value = name[:i], name[i+1:]
		hasValue = true
	}
	if name == "" || name == "-" {
		return 0, usagef("invalid flag %q", arg)
	}
	f := fs.Lookup(name)
	if f == nil {
		return 0, usagef("unknown flag %q; run `mem help` for usage", arg)
	}
	if isBoolFlag(f) {
		if !hasValue {
			value = "true"
		}
		if err := fs.Set(name, value); err != nil {
			return 0, usagef("invalid value for --%s: %v", name, err)
		}
		return 1, nil
	}
	if !hasValue {
		if len(rest) < 2 {
			return 0, usagef("flag --%s needs a value", name)
		}
		value = rest[1]
	}
	if err := fs.Set(name, value); err != nil {
		return 0, usagef("invalid value for --%s: %v", name, err)
	}
	if hasValue {
		return 1, nil
	}
	return 2, nil
}

func isBoolFlag(f *flag.Flag) bool {
	bf, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && bf.IsBoolFlag()
}

// collectArgs parses flags anywhere in args, returning the positional ones.
func collectArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			pos = append(pos, args[i+1:]...)
			return pos, nil
		}
		if len(arg) < 2 || arg[0] != '-' {
			pos = append(pos, arg)
			continue
		}
		consumed, err := setFlagArg(fs, arg, args[i:])
		if err != nil {
			return nil, err
		}
		i += consumed - 1
	}
	return pos, nil
}

// writeJSON emits one machine-readable payload on stdout.
func (a *app) writeJSON(v any) error {
	enc := json.NewEncoder(a.io.Out)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// emit writes v as JSON when --json is on.
func (a *app) emit(v any) error {
	if !a.globals.json {
		return nil
	}
	return a.writeJSON(v)
}

// say prints human text on stdout, or on stderr when JSON owns stdout.
func (a *app) say(format string, args ...any) {
	w := a.io.Out
	if a.globals.json {
		w = a.io.Err
	}
	fmt.Fprintf(w, format, args...)
}

// hint prints a non-fatal note to stderr.
func (a *app) hint(format string, args ...any) {
	if a.globals.quiet {
		return
	}
	fmt.Fprintf(a.io.Err, format, args...)
}

// loadConfig loads the config honoring --config and --data-dir.
func (a *app) loadConfig() error {
	if a.cfgSet {
		return nil
	}
	var (
		cfg config.Config
		err error
	)
	path := a.globals.cfgPath
	if path == "" {
		path, err = config.Path()
		if err != nil {
			return err
		}
	}
	cfg, err = config.LoadFile(path)
	if err != nil {
		return err
	}
	if a.globals.dataDir != "" {
		cfg.DataDir = a.globals.dataDir
	}
	a.cfg = cfg
	a.cfgPath = path
	a.cfgSet = true
	return nil
}

// openStore opens only the on-disk store: no model, no downloads.
func (a *app) openStore() (*store.Store, error) {
	if err := a.loadConfig(); err != nil {
		return nil, err
	}
	if a.cfg.DataDir == "" {
		return nil, fmt.Errorf("data dir is not configured; run `mem init`")
	}
	return store.Open(a.cfg)
}

// openEngine opens the full engine. Sidecars stay lazy, but the initial sync
// may start one when documents are missing from the index.
func (a *app) openEngine() (*engine.Engine, error) {
	return a.openEngineWith()
}

// openEngineReadOnly opens the engine without the initial sync, so commands that
// only read or delete never start a model.
func (a *app) openEngineReadOnly() (*engine.Engine, error) {
	return a.openEngineWith(engine.WithoutInitialSync())
}

func (a *app) openEngineWith(extra ...engine.Option) (*engine.Engine, error) {
	if err := a.loadConfig(); err != nil {
		return nil, err
	}
	cfg := a.cfg
	if cfg.SidecarPath == "" {
		cfg.SidecarPath = DetectSidecar(cfg)
	}
	// Indexing warnings are summarized by warnSetupNeeded; the full chatter is
	// only useful with -v.
	logOut := io.Discard
	if a.globals.verbose {
		logOut = a.io.Err
	}
	opts := []engine.Option{
		engine.WithLogger(log.New(logOut, "", 0)),
	}
	if cfg.IdleTimeoutSeconds > 0 {
		opts = append(opts, engine.WithIdleTimeout(time.Duration(cfg.IdleTimeoutSeconds)*time.Second))
	}
	opts = append(opts, extra...)
	if a.env.OpenEngine != nil {
		return a.env.OpenEngine(cfg, opts...)
	}
	return engine.Open(cfg, opts...)
}

// DetectSidecar finds the llama-server executable: an explicit config path, the
// bin directory installed next to models_dir, then PATH.
func DetectSidecar(cfg config.Config) string {
	name := "llama-server"
	if isWindows() {
		name += ".exe"
	}
	if cfg.SidecarPath != "" {
		if fileExists(cfg.SidecarPath) {
			return cfg.SidecarPath
		}
	}
	if dir := cfg.BinDir(); dir != "" {
		candidate := filepath.Join(dir, name)
		if fileExists(candidate) {
			return candidate
		}
	}
	if p, err := lookPath("llama-server"); err == nil {
		return p
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// warnSetupNeeded prints the single "not set up" note the spec requires, and a
// precise repair hint when the pieces are present but unusable.
func (a *app) warnSetupNeeded() {
	if a.hinted || a.globals.quiet {
		if a.globals.json && !a.hinted {
			a.hinted = true
		}
		return
	}
	modelPath := filepath.Join(a.cfg.ModelsDir, firstNonEmpty(a.cfg.EmbedModel, config.DefaultEmbedModel))
	sidecar := DetectSidecar(a.cfg)
	a.hinted = true
	if fileExists(modelPath) && sidecar != "" {
		// The pieces are present; only speak up if the binary is unusable, which
		// is what a stub or wrong-architecture download looks like.
		if st := setup.CheckSidecar(sidecar); !st.OK {
			fmt.Fprintf(a.io.Err, "the llama-server at %s is unusable: %s\nrun `mem init --force` to reinstall it\n", sidecar, st.Problem)
		}
		return
	}
	fmt.Fprintln(a.io.Err, "indexed search is not set up yet; run `mem init`")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// formatTime renders a timestamp for humans.
func formatTime(t time.Time) string {
	return t.Local().Format("2006-01-02 15:04")
}

func formatExpiry(t *time.Time) string {
	if t == nil {
		return "never"
	}
	return formatTime(*t)
}
