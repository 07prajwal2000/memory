package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"memory/internal/config"
)

func TestInitNonInteractiveThroughCLI(t *testing.T) {
	h := newHarness(t)
	cfgDir := filepath.Dir(h.cfgF)

	code, out, errOut := h.run("init", "--yes", "--no-download",
		"--data-dir", filepath.Join(cfgDir, "data2"),
		"--models-dir", filepath.Join(cfgDir, "models2"),
		"--port", "8123", "--expiry", "4", "--idle-timeout", "120")
	if code != ExitOK {
		t.Fatalf("init exit = %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "mem is set up") {
		t.Fatalf("init stdout = %q", out)
	}
	if !strings.Contains(out, "claude mcp add mem") {
		t.Error("init did not print the MCP next step")
	}

	cfg, err := config.LoadFile(h.cfgF)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if cfg.Port != 8123 || cfg.DefaultTempExpiryDays != 4 || cfg.IdleTimeoutSeconds != 120 {
		t.Fatalf("saved config = %+v", cfg)
	}
	if cfg.Rerank {
		t.Error("rerank must stay off by default")
	}
	if !strings.HasSuffix(cfg.DataDir, "data2") || !strings.HasSuffix(cfg.ModelsDir, "models2") {
		t.Fatalf("dirs = %q / %q", cfg.DataDir, cfg.ModelsDir)
	}

	// The store is bootstrapped, so the default file is readable right away.
	code, out, _ = h.run("get")
	if code != ExitOK || !strings.Contains(out, "# Memory") {
		t.Fatalf("get after init exit = %d out = %q", code, out)
	}
}

func TestInitJSONShape(t *testing.T) {
	h := newHarness(t)
	cfgDir := filepath.Dir(h.cfgF)
	var res map[string]any
	code, errOut := h.runJSON(&res, "init", "--yes", "--no-download",
		"--data-dir", filepath.Join(cfgDir, "d"), "--models-dir", filepath.Join(cfgDir, "m"))
	if code != ExitOK {
		t.Fatalf("init --json exit = %d: %s", code, errOut)
	}
	for _, key := range []string{"config_path", "data_dir", "models_dir", "port", "expiry_days", "rerank", "next_steps", "config_saved"} {
		if _, ok := res[key]; !ok {
			t.Errorf("init JSON missing %q: %v", key, res)
		}
	}
}
