package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// inProject writes an .agent-memory file in a temp project, chdirs into a
// subfolder of it for the test, and returns the marker path.
func inProject(t *testing.T, marker string) string {
	t.Helper()
	root := t.TempDir()
	sub := filepath.Join(root, "pkg", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".agent-memory")
	if err := os.WriteFile(path, []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(sub); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	return path
}

func memoryNames(t *testing.T, h *harness, ctx string) string {
	t.Helper()
	code, out, errOut := h.run("ls", "--context", ctx)
	if code != ExitOK {
		t.Fatalf("ls --context %s: %d %s", ctx, code, errOut)
	}
	return out
}

func TestProjectDefaultPrecedence(t *testing.T) {
	h := newHarness(t)
	for _, c := range []string{"proj", "other"} {
		if code, _, e := h.run("context", "create", c); code != ExitOK {
			t.Fatalf("create %s: %s", c, e)
		}
	}
	inProject(t, "context = \"proj\"\n")

	// marker beats global
	if code, _, e := h.run("upsert", "--name", "a.md", "from marker"); code != ExitOK {
		t.Fatalf("upsert: %s", e)
	}
	if !strings.Contains(memoryNames(t, h, "proj"), "a.md") || strings.Contains(memoryNames(t, h, "global"), "a.md") {
		t.Fatal("upsert should land in proj, not global")
	}
	// explicit beats marker, including global
	if code, _, e := h.run("upsert", "--context", "global", "--name", "g.md", "x"); code != ExitOK {
		t.Fatalf("upsert global: %s", e)
	}
	if code, _, e := h.run("upsert", "--context", "other", "--name", "o.md", "x"); code != ExitOK {
		t.Fatalf("upsert other: %s", e)
	}
	if !strings.Contains(memoryNames(t, h, "global"), "g.md") || !strings.Contains(memoryNames(t, h, "other"), "o.md") {
		t.Fatal("explicit context ignored")
	}

	// ls / get / search use the marker
	if _, out, _ := h.run("ls"); !strings.Contains(out, "a.md") || strings.Contains(out, "g.md") {
		t.Fatalf("ls should list proj: %q", out)
	}
	if code, out, e := h.run("get", "a.md"); code != ExitOK || !strings.Contains(out, "from marker") {
		t.Fatalf("get by name in marker context: %d %s %s", code, out, e)
	}
	if _, out, _ := h.run("ls", "--context", "global"); strings.Contains(out, "a.md") {
		t.Fatalf("explicit global ls leaked proj: %q", out)
	}
	if code, out, e := h.run("search", "marker", "--no-global"); code != ExitOK || !strings.Contains(out, "a.md") {
		t.Fatalf("search --no-global should satisfy context rule via marker: %d %s %s", code, out, e)
	}
	if code, out, _ := h.run("search", "marker", "--context", "other", "--no-global"); code != ExitOK || strings.Contains(out, "a.md") {
		t.Fatalf("explicit context should win in search: %d %q", code, out)
	}

	// no-arg get keeps its output and adds the detected default
	if _, out, _ := h.run("get"); !strings.Contains(out, "Default context: \"proj\" (from ") {
		t.Fatalf("get default missing marker line: %q", out)
	}
	var def map[string]any
	if code, e := h.runJSON(&def, "get"); code != ExitOK || def["project_default"] == nil {
		t.Fatalf("get --json: %d %s %v", code, e, def)
	}
}

func TestProjectDefaultMissingContext(t *testing.T) {
	h := newHarness(t)
	path := inProject(t, "context = \"nope\"\n")

	code, _, errOut := h.run("upsert", "--name", "a.md", "x")
	if code != ExitNotFound {
		t.Fatalf("want exit %d, got %d: %s", ExitNotFound, code, errOut)
	}
	for _, want := range []string{path, `"nope"`, "mem context create nope"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("error missing %q: %s", want, errOut)
		}
	}
	if strings.Contains(memoryNames(t, h, "global"), "a.md") {
		t.Fatal("write must not fall back to global")
	}

	// reads fall back to global with a note, silenced by -q
	h.run("upsert", "--context", "global", "--name", "g.md", "findme")
	code, out, errOut := h.run("search", "findme")
	if code != ExitOK || !strings.Contains(out, "g.md") || !strings.Contains(errOut, "using global") {
		t.Fatalf("search fallback: %d %q %q", code, out, errOut)
	}
	if _, _, errOut := h.run("-q", "ls"); strings.Contains(errOut, "using global") {
		t.Fatalf("-q should silence the note: %q", errOut)
	}
}

func TestProjectDefaultUnusableFile(t *testing.T) {
	h := newHarness(t)
	inProject(t, "context = \n")
	if code, _, errOut := h.run("upsert", "--name", "a.md", "x"); code == ExitOK || !strings.Contains(errOut, ".agent-memory") {
		t.Fatalf("upsert with bad marker: %d %s", code, errOut)
	}
	if code, _, errOut := h.run("ls"); code != ExitOK || !strings.Contains(errOut, "using global") {
		t.Fatalf("ls with bad marker: %d %s", code, errOut)
	}
}

func TestStatusShowsProjectDefault(t *testing.T) {
	h := newHarness(t)
	var none map[string]any
	if code, e := h.runJSON(&none, "status"); code != ExitOK {
		t.Fatal(e)
	}
	if pd, _ := none["project_default"].(map[string]any); pd == nil || pd["path"] != "" {
		t.Fatalf("status without marker: %v", none["project_default"])
	}

	path := inProject(t, "context = \"proj\"\n")
	var st map[string]any
	if code, e := h.runJSON(&st, "status"); code != ExitOK {
		t.Fatal(e)
	}
	pd, _ := st["project_default"].(map[string]any)
	if pd["path"] != path || pd["context"] != "proj" || pd["error"] != "" {
		t.Fatalf("status project_default: %v", pd)
	}
	if _, out, _ := h.run("status"); !strings.Contains(out, "default ctx") || !strings.Contains(out, path) {
		t.Fatalf("status text: %q", out)
	}
}
