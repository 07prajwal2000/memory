package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// ProjectFileName is the marker file that names a project's default context.
const ProjectFileName = ".agent-memory"

// ProjectDefault is the result of looking for a ProjectFileName. The zero
// value means no file was found.
type ProjectDefault struct {
	Path    string // the marker file; empty when none was found
	Context string // the validated slug; empty when Err is set
	Err     error  // why the file is unusable, if it is
}

// Found reports whether a marker file exists, usable or not.
func (p ProjectDefault) Found() bool { return p.Path != "" }

// FindProjectDefault walks from startDir up to the filesystem root and parses
// the nearest ProjectFileName. It never creates anything.
func FindProjectDefault(startDir string) ProjectDefault {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return ProjectDefault{}
	}
	for {
		path := filepath.Join(dir, ProjectFileName)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return parseProjectFile(path)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ProjectDefault{}
		}
		dir = parent
	}
}

func parseProjectFile(path string) ProjectDefault {
	p := ProjectDefault{Path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		p.Err = fmt.Errorf("%w: cannot read %s: %v", ErrInvalid, path, err)
		return p
	}
	var doc struct {
		Context string `toml:"context"`
	}
	if err := toml.Unmarshal(data, &doc); err != nil {
		p.Err = fmt.Errorf("%w: %s is not valid TOML: %v", ErrInvalid, path, err)
		return p
	}
	if err := ValidateSlug(doc.Context); err != nil {
		p.Err = fmt.Errorf("%s needs context = \"<slug>\" (%w)", path, err)
		return p
	}
	p.Context = doc.Context
	return p
}

// Resolve picks the context for a call that may not name one: an explicit
// slug wins, then a usable marker whose context exists, else "" (global).
// A marker that cannot be honoured fails writes with a clear error; for reads
// it falls back to global and returns a warning instead. With no marker the
// explicit value is returned untouched.
func (p ProjectDefault) Resolve(s *Store, explicit string, write bool) (slug, warning string, err error) {
	if explicit != "" || !p.Found() {
		return explicit, "", nil
	}
	if p.Err == nil {
		if _, gerr := s.GetContext(p.Context); gerr == nil {
			return p.Context, "", nil
		} else if !errors.Is(gerr, ErrNotFound) {
			return "", "", gerr
		}
		err = fmt.Errorf("%w: %s names context %q, which does not exist; create it with `mem context create %s`", ErrNotFound, p.Path, p.Context, p.Context)
	} else {
		err = p.Err
	}
	if write {
		return "", "", err
	}
	return "", fmt.Sprintf("%v; using global", err), nil
}

// Summary is a one-line description for status and get output: the detected
// default context and its file, or why the file is unusable.
func (p ProjectDefault) Summary() string {
	switch {
	case !p.Found():
		return "none"
	case p.Err != nil:
		return "unusable: " + p.Err.Error()
	}
	return fmt.Sprintf("%q (from %s)", p.Context, p.Path)
}
