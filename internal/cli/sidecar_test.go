package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"memory/internal/config"
	"memory/internal/setup"
)

// fakeExecutable writes a file with a real executable header for this platform
// and a size above setup's minimum, so it passes the structural check.
func fakeExecutable(t *testing.T, path string, archOS string) {
	t.Helper()
	size := 2 << 20 // 2 MiB
	buf := make([]byte, size)
	switch {
	case strings.HasPrefix(archOS, "windows"):
		buf[0], buf[1] = 'M', 'Z'
		peOff := 0x80
		buf[0x3c] = byte(peOff)
		copy(buf[peOff:], []byte("PE\x00\x00"))
		machine := uint16(0x8664)
		if strings.HasSuffix(archOS, "arm64") {
			machine = 0xaa64
		}
		buf[peOff+4] = byte(machine)
		buf[peOff+5] = byte(machine >> 8)
	case strings.HasPrefix(archOS, "darwin"):
		copy(buf, []byte{0xcf, 0xfa, 0xed, 0xfe})
		cpu := uint32(0x01000007)
		if strings.HasSuffix(archOS, "arm64") {
			cpu = 0x0100000c
		}
		buf[4], buf[5], buf[6], buf[7] = byte(cpu), byte(cpu>>8), byte(cpu>>16), byte(cpu>>24)
	default:
		copy(buf, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0})
		if strings.HasSuffix(archOS, "arm64") {
			buf[18] = 183
		} else {
			buf[18] = 62
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf, 0o755); err != nil {
		t.Fatal(err)
	}
	var lib string
	switch runtime.GOOS {
	case "windows":
		lib = "llama.dll"
	case "darwin":
		lib = "libllama.dylib"
	default:
		lib = "libllama.so"
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), lib), []byte("lib"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sidecarName() string {
	if runtime.GOOS == "windows" {
		return "llama-server.exe"
	}
	return "llama-server"
}

func TestStatusDetectsStubSidecar(t *testing.T) {
	h := newHarness(t)
	binDir := h.cfg.BinDir()
	binPath := filepath.Join(binDir, sidecarName())
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The exact failure from the owner's machine: an 8-byte placeholder.
	if err := os.WriteFile(binPath, []byte("fake exe"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFile(h.cfgF)
	if err != nil {
		t.Fatal(err)
	}
	cfg.SidecarPath = binPath
	if err := cfg.SaveFile(h.cfgF); err != nil {
		t.Fatal(err)
	}

	var st map[string]any
	if code, errOut := h.runJSON(&st, "status"); code != ExitOK {
		t.Fatalf("status failed: %s", errOut)
	}
	if st["sidecar_ok"] != false {
		t.Fatalf("status reported the stub as ok: %v", st["sidecar_ok"])
	}
	problem, _ := st["sidecar_problem"].(string)
	if !strings.Contains(problem, "not a recognised executable") {
		t.Fatalf("problem = %q", problem)
	}
	if st["needs_reinstall"] != true {
		t.Fatalf("needs_reinstall = %v, want true", st["needs_reinstall"])
	}

	// The human output says "broken" and points at --force.
	code, out, _ := h.run("status")
	if code != ExitOK {
		t.Fatalf("status exit = %d", code)
	}
	if !strings.Contains(out, "broken") || !strings.Contains(out, "mem init --force") {
		t.Fatalf("human status = %q", out)
	}
}

func TestStatusAcceptsHealthySidecar(t *testing.T) {
	h := newHarness(t)
	binPath := filepath.Join(h.cfg.BinDir(), sidecarName())
	fakeExecutable(t, binPath, runtime.GOOS+"/"+runtime.GOARCH)

	cfg, _ := config.LoadFile(h.cfgF)
	cfg.SidecarPath = binPath
	if err := cfg.SaveFile(h.cfgF); err != nil {
		t.Fatal(err)
	}

	var st map[string]any
	if code, errOut := h.runJSON(&st, "status"); code != ExitOK {
		t.Fatalf("status failed: %s", errOut)
	}
	if st["sidecar_ok"] != true {
		t.Fatalf("healthy sidecar reported not ok: %v (%v)", st["sidecar_ok"], st["sidecar_problem"])
	}
	if st["needs_reinstall"] != false {
		t.Fatalf("needs_reinstall = %v", st["needs_reinstall"])
	}
}

// TestUpsertWarnsAboutStubSidecar covers the CLI's own detection: with a stub
// binary present, the "not set up" note becomes a precise repair instruction
// instead of silently degrading forever. (The engine here uses an injected
// embedder, so the message comes from the integrity check, not from a failed
// process start; the OS-error wording is covered by TestSidecarHintOnlyForOSFailures.)
func TestUpsertWarnsAboutStubSidecar(t *testing.T) {
	h := newHarness(t)
	if code, _, _ := h.run("context", "create", "work"); code != ExitOK {
		t.Fatal("create failed")
	}
	binPath := filepath.Join(h.cfg.BinDir(), sidecarName())
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binPath, []byte("fake exe"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.LoadFile(h.cfgF)
	cfg.SidecarPath = binPath
	if err := cfg.SaveFile(h.cfgF); err != nil {
		t.Fatal(err)
	}
	// Also place a model file so the code reaches the "pieces present" branch.
	modelPath := filepath.Join(h.cfg.ModelsDir, setup.EmbeddingModel.File)
	if err := os.MkdirAll(h.cfg.ModelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modelPath, []byte("placeholder"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, _, errOut := h.run("upsert", "--context", "work", "--name", "a.md", "body")
	if code != ExitOK {
		t.Fatalf("upsert exit = %d: %s", code, errOut)
	}
	if !strings.Contains(errOut, "unusable") || !strings.Contains(errOut, "mem init --force") {
		t.Fatalf("stderr = %q, want an unusable-binary note with the repair hint", errOut)
	}
}

func TestSidecarHintOnlyForOSFailures(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// The owner's exact message.
		{"sidecar start C:\\x\\llama-server.exe: fork/exec C:\\x\\llama-server.exe: This version of %1 is not compatible with the version of Windows you're running.",
			"mem init --force"},
		{"exec format error", "mem init --force"},
		{"exec: \"llama-server\": executable file not found in %PATH%", ""},
		{"connection refused", ""},
		{"already mentions `mem init --force`", "mem init --force"},
	}
	for _, c := range cases {
		got := withSidecarHint(c.in)
		if c.want == "" {
			if got != c.in {
				t.Errorf("unrelated error was modified: %q", got)
			}
			continue
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("hint missing for %q: %q", c.in, got)
		}
		if strings.Count(got, "mem init --force") > 1 {
			t.Errorf("hint duplicated: %q", got)
		}
	}
}

func TestStatusModelIntegrity(t *testing.T) {
	h := newHarness(t)
	// A model file of the wrong size must be reported as broken.
	modelPath := filepath.Join(h.cfg.ModelsDir, setup.EmbeddingModel.File)
	if err := os.MkdirAll(h.cfg.ModelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modelPath, []byte("too small"), 0o644); err != nil {
		t.Fatal(err)
	}
	var st map[string]any
	if code, _ := h.runJSON(&st, "status"); code != ExitOK {
		t.Fatal("status failed")
	}
	if st["embed_present"] != false {
		t.Fatalf("a truncated model was accepted: %v", st)
	}
	problem, _ := st["embed_problem"].(string)
	if !strings.Contains(problem, "expected") {
		t.Fatalf("problem = %q", problem)
	}
	if st["needs_reinstall"] != true {
		t.Fatalf("a broken model should need a reinstall")
	}
}

func TestConfigJSONStillParses(t *testing.T) {
	h := newHarness(t)
	var all map[string]string
	code, _ := h.runJSON(&all, "config")
	if code != ExitOK {
		t.Fatalf("config exit = %d", code)
	}
	raw, _ := json.Marshal(all)
	if !strings.Contains(string(raw), "port") {
		t.Fatalf("config JSON = %s", raw)
	}
}
