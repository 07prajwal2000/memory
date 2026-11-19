package store

import (
	"os"
	"path/filepath"
	"testing"
)

func writeMarker(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, ProjectFileName)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFindProjectDefault(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	if p := FindProjectDefault(sub); p.Found() {
		// A stray marker above the temp dir would make this test machine-dependent.
		t.Skipf("marker above temp dir: %s", p.Path)
	}

	top := writeMarker(t, root, "context = \"top\"\nunknown = 1\n")
	if p := FindProjectDefault(sub); p.Path != top || p.Context != "top" || p.Err != nil {
		t.Fatalf("parent: %+v", p)
	}
	if p := FindProjectDefault(root); p.Path != top || p.Context != "top" {
		t.Fatalf("cwd: %+v", p)
	}

	near := writeMarker(t, filepath.Join(root, "a"), "context = \"near\"\n")
	if p := FindProjectDefault(sub); p.Path != near || p.Context != "near" {
		t.Fatalf("nearest: %+v", p)
	}
}

func TestFindProjectDefaultUnusable(t *testing.T) {
	for name, body := range map[string]string{
		"invalid toml": "context = ",
		"empty":        "context = \"\"\n",
		"missing key":  "other = \"x\"\n",
		"bad slug":     "context = \"Not A Slug\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeMarker(t, dir, body)
			p := FindProjectDefault(dir)
			if p.Path != path || p.Context != "" || p.Err == nil {
				t.Fatalf("want unusable marker, got %+v", p)
			}
		})
	}
}
