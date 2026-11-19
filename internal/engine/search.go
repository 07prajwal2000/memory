package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"memory/internal/search"
	"memory/internal/store"
)

// ErrEmptyQuery is returned when Search is called without a query.
var ErrEmptyQuery = errors.New("engine: search query is empty")

// SearchRequest is the high-level search used by the CLI, MCP and dashboard.
type SearchRequest struct {
	Query    string
	Context  string
	NoGlobal bool
	Limit    int
	PerDoc   int
	Fuzzy    *bool
}

// WithFuzzyDefault sets the default fuzzy search behavior when SearchRequest.Fuzzy is nil.
func WithFuzzyDefault(enabled bool) Option {
	return func(o *options) {
		o.fuzzyDefault = &enabled
	}
}

// Search resolves the context scope (global plus the requested context unless
// NoGlobal) and runs the hybrid search.
func (e *Engine) Search(ctx context.Context, r SearchRequest) (search.Result, error) {
	if strings.TrimSpace(r.Query) == "" {
		return search.Result{}, ErrEmptyQuery
	}

	contexts := make([]string, 0, 2)
	if !r.NoGlobal {
		contexts = append(contexts, store.GlobalContext)
	}
	if r.Context != "" {
		if _, err := e.Store.GetContext(r.Context); err != nil {
			return search.Result{}, fmt.Errorf("engine: search context %q: %w", r.Context, err)
		}
		if !contains(contexts, r.Context) {
			contexts = append(contexts, r.Context)
		}
	}
	if len(contexts) == 0 {
		return search.Result{}, errors.New("engine: no contexts to search; a context is required when global is excluded")
	}

	var fuzzy *bool
	if r.Fuzzy != nil {
		fuzzy = r.Fuzzy
	} else if e.fuzzyDefault != nil {
		fuzzy = e.fuzzyDefault
	}

	return e.Index.Search(ctx, search.Query{
		Text:     r.Query,
		Contexts: contexts,
		Limit:    r.Limit,
		PerDoc:   r.PerDoc,
		NoRerank: e.reranker == nil,
		Fuzzy:    fuzzy,
	})
}

// DefaultFile returns global/readme.md, the agent-facing instructions file.
func (e *Engine) DefaultFile() (store.Memory, error) {
	return e.Store.Get(store.GlobalContext, store.DefaultFileName)
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
