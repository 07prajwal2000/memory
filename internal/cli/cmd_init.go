package cli

import (
	"flag"
	"fmt"

	"memory/internal/setup"
)

func initCommand() command {
	return command{
		name:  "init",
		usage: "mem init [--yes] [--force] [--data-dir D] [--models-dir D] [--port N] [--expiry DAYS] [--idle-timeout SECONDS] [--with-rerank] [--no-download] [--offline-dir D]",
		parse: parseInit,
		run:   runInit,
	}
}

type initFlags struct {
	yes         bool
	modelsDir   string
	port        int
	expiry      int
	idleTimeout int
	withRerank  bool
	noDownload  bool
	offlineDir  string
	force       bool
	pos         []string
}

func parseInit(fs *flag.FlagSet, a *app, args []string) ([]string, error) {
	f := &initFlags{}
	fs.BoolVar(&f.yes, "yes", false, "accept the defaults")
	fs.StringVar(&f.modelsDir, "models-dir", "", "models directory")
	fs.IntVar(&f.port, "port", 0, "dashboard port")
	fs.IntVar(&f.expiry, "expiry", 0, "default temp-context expiry in days")
	fs.IntVar(&f.idleTimeout, "idle-timeout", 0, "model server idle timeout in seconds")
	fs.BoolVar(&f.withRerank, "with-rerank", false, "download the reranker too")
	fs.BoolVar(&f.noDownload, "no-download", false, "skip all downloads")
	fs.StringVar(&f.offlineDir, "offline-dir", "", "install from a local folder instead of the network")
	fs.BoolVar(&f.force, "force", false, "reinstall the llama-server and models even if they look valid")
	pos, err := collectArgs(fs, args)
	if err != nil {
		return nil, err
	}
	f.pos = pos
	a.state = f
	return nil, nil
}

func runInit(a *app, _ []string) (int, error) {
	f := a.state.(*initFlags)
	if len(f.pos) > 0 {
		return ExitUsage, usagef("mem init takes no arguments; use flags")
	}

	interactive := a.io.InIsTTY
	// With --json, stdout is reserved for the payload, so the wizard's human
	// summary goes to stderr.
	out := a.io.Out
	if a.globals.json {
		out = a.io.Err
	}
	res, err := setup.Run(setup.Options{
		Yes:         f.yes,
		DataDir:     a.globals.dataDir,
		ModelsDir:   f.modelsDir,
		Port:        f.port,
		ExpiryDays:  f.expiry,
		IdleSeconds: f.idleTimeout,
		WithRerank:  f.withRerank,
		NoDownload:  f.noDownload,
		OfflineDir:  f.offlineDir,
		Force:       f.force,
		ConfigPath:  a.globals.cfgPath,
		In:          a.io.In,
		Out:         out,
		Err:         a.io.Err,
		InIsTTY:     interactive && !f.yes,
	})
	if err != nil {
		return ExitError, err
	}
	if a.globals.json {
		return ExitOK, a.emit(res)
	}
	return ExitOK, nil
}

var _ = fmt.Sprintf
