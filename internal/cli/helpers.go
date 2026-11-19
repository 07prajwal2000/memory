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

// searchRequest maps CLI flags onto an engine request.
func searchRequest(f *searchFlags, query string) engine.SearchRequest {
	return engine.SearchRequest{
		Query:    query,
		Context:  f.context,
		NoGlobal: f.noGlobal,
		Limit:    f.limit,
		PerDoc:   f.perDoc,
	}
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
