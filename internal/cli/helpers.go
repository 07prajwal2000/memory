package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"memory/internal/engine"
	"memory/internal/store"
)

// context is the background context every command uses.
func (a *app) context() context.Context {
	return context.Background()
}

// projectDefault finds the .agent-memory marker once per invocation.
func (a *app) projectDefault() store.ProjectDefault {
	if !a.projectSet {
		a.projectSet = true
		if wd, err := os.Getwd(); err == nil {
			a.project = store.FindProjectDefault(wd)
		}
	}
	return a.project
}

// resolveContext applies explicit > .agent-memory > global for one command.
// For reads a marker problem is a warning, printed to stderr unless -q.
func (a *app) resolveContext(s *store.Store, explicit string, write bool) (string, error) {
	slug, warn, err := a.projectDefault().Resolve(s, explicit, write)
	if warn != "" {
		a.hint("note: %s\n", warn)
	}
	return slug, err
}

func projectJSON(p store.ProjectDefault) map[string]any {
	out := map[string]any{"path": p.Path, "context": p.Context, "error": ""}
	if p.Err != nil {
		out["error"] = p.Err.Error()
	}
	return out
}

// searchRequest maps CLI flags onto an engine request.
func searchRequest(f *searchFlags, query string) engine.SearchRequest {
	req := engine.SearchRequest{
		Query:    query,
		Context:  f.context,
		NoGlobal: f.noGlobal,
		Limit:    f.limit,
		PerDoc:   f.perDoc,
	}
	if f.noFuzzySet {
		b := false
		req.Fuzzy = &b
	} else if f.fuzzySet {
		b := true
		req.Fuzzy = &b
	}
	return req
}

// indexInSync reports whether every store memory is in the index at the same
// version. It only reads, so it never embeds or spawns a model.
func indexInSync(e *engine.Engine) bool {
	state, err := e.Index.State()
	if err != nil {
		return false
	}
	contexts, err := e.Store.ListContexts()
	if err != nil {
		return false
	}
	seen := 0
	for _, c := range contexts {
		list, err := e.Store.List(c.Slug)
		if err != nil {
			return false
		}
		for _, m := range list {
			seen++
			st, ok := state[m.ID]
			if !ok || st.Version != m.Version || st.Context != m.Context {
				return false
			}
			delete(state, m.ID)
		}
	}
	return len(state) == 0 && seen > 0
}

func memoryJSON(m store.Memory) map[string]any {
	return map[string]any{
		"id":         m.ID,
		"context":    m.Context,
		"name":       m.Name,
		"body":       m.Body,
		"version":    m.Version,
		"updated_at": m.UpdatedAt.UTC(),
		"updated_by": m.UpdatedBy,
		"tags":       m.Tags,
	}
}

func memoriesJSON(list []store.Memory) []map[string]any {
	out := make([]map[string]any, 0, len(list))
	for _, m := range list {
		out = append(out, map[string]any{
			"id":         m.ID,
			"context":    m.Context,
			"name":       m.Name,
			"version":    m.Version,
			"updated_at": m.UpdatedAt.UTC(),
			"updated_by": m.UpdatedBy,
			"tags":       m.Tags,
		})
	}
	return out
}

func contextsJSON(list []store.Context) []map[string]any {
	out := make([]map[string]any, 0, len(list))
	for _, c := range list {
		out = append(out, map[string]any{
			"slug":       c.Slug,
			"memories":   c.MemoryCount,
			"created_at": c.CreatedAt.UTC(),
			"expires_at": c.ExpiresAt,
		})
	}
	return out
}

// readAll drains a reader (stdin or a test buffer).
func readAll(r io.Reader) ([]byte, error) {
	if f, ok := r.(*os.File); ok {
		// Piped stdin is closed by the writer; reading to EOF is correct.
		_ = f
	}
	return io.ReadAll(r)
}

func filepathJoin(dir, name string) string {
	if dir == "" {
		return name
	}
	return filepath.Join(dir, name)
}
