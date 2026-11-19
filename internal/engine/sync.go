package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"memory/internal/search"
	"memory/internal/store"
)

// SyncReport describes what one Sync pass changed.
type SyncReport struct {
	Total     int
	Added     int
	Updated   int
	Removed   int
	Unchanged int
	Indexed   int
	// Filled counts chunks whose vectors were pending (a write landed while the
	// embedder was down) and were embedded by this pass.
	Filled   int
	Duration time.Duration
}

// SyncProgress is delivered to a WithSyncProgress callback.
type SyncProgress struct {
	Phase   string // "delete" or "index"
	DocID   string
	Context string
	Done    int
	Total   int
}

// SyncProgressFunc observes Sync progress.
type SyncProgressFunc func(SyncProgress)

type desiredDoc struct {
	mem  store.Memory
	hash string
}

// Sync makes the search index match the store: stale or missing documents are
// indexed, orphans are deleted. Files are the source of truth, so a document
// only has to exist in the store to be indexed.
func (e *Engine) Sync(ctx context.Context) (SyncReport, error) {
	start := time.Now()
	var rep SyncReport
	if e.Index == nil {
		return rep, errors.New("engine: search index is not open")
	}

	state, err := e.Index.State()
	if err != nil {
		return rep, fmt.Errorf("engine: read index state: %w", err)
	}
	contexts, err := e.Store.ListContexts()
	if err != nil {
		return rep, fmt.Errorf("engine: list contexts: %w", err)
	}

	desired := make(map[string]desiredDoc)
	for _, c := range contexts {
		list, err := e.Store.List(c.Slug)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			return rep, fmt.Errorf("engine: list memories in %q: %w", c.Slug, err)
		}
		for _, m := range list {
			full, err := e.Store.Get(c.Slug, m.ID)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					continue
				}
				return rep, fmt.Errorf("engine: read memory %s/%s: %w", c.Slug, m.ID, err)
			}
			desired[full.ID] = desiredDoc{mem: full, hash: search.Sha256Hex(full.Body)}
		}
	}

	var orphanIDs []string
	for id := range state {
		if _, ok := desired[id]; !ok {
			orphanIDs = append(orphanIDs, id)
		}
	}
	sort.Strings(orphanIDs)
	docIDs := make([]string, 0, len(desired))
	for id := range desired {
		docIDs = append(docIDs, id)
	}
	sort.Strings(docIDs)

	rep.Total = len(orphanIDs) + len(docIDs)
	done := 0
	report := func(phase, id, ctxName string) {
		done++
		if e.progress != nil {
			e.progress(SyncProgress{Phase: phase, DocID: id, Context: ctxName, Done: done, Total: rep.Total})
		}
	}

	for _, id := range orphanIDs {
		if err := e.Index.Delete(ctx, id); err != nil {
			return rep, fmt.Errorf("engine: delete orphan %s: %w", id, err)
		}
		rep.Removed++
		report("delete", id, state[id].Context)
	}

	// Collect the documents that need (re)indexing, then hand them to
	// UpsertMany in batches. UpsertMany embeds concurrently and commits in
	// WASM-safe chunks, and because each call commits independently an
	// interrupted sync resumes at the next missing document.
	pending := make([]string, 0, len(docIDs))
	for _, id := range docIDs {
		d := desired[id]
		if st, ok := state[id]; ok &&
			st.Version == d.mem.Version &&
			st.Context == d.mem.Context &&
			st.Hash == d.hash {
			rep.Unchanged++
			report("index", id, d.mem.Context)
			continue
		}
		pending = append(pending, id)
	}

	batchSize := e.syncBatchSize
	if batchSize <= 0 {
		batchSize = defaultSyncBatchSize
	}
	for start := 0; start < len(pending); start += batchSize {
		end := start + batchSize
		if end > len(pending) {
			end = len(pending)
		}
		ids := pending[start:end]
		docs := make([]search.Doc, 0, len(ids))
		for _, id := range ids {
			d := desired[id]
			docs = append(docs, search.Doc{
				ID:      d.mem.ID,
				Context: d.mem.Context,
				Name:    d.mem.Name,
				Body:    d.mem.Body,
				Version: d.mem.Version,
			})
		}
		if err := e.Index.UpsertMany(ctx, docs); err != nil {
			if errors.Is(err, search.ErrVectorsPending) {
				e.recordWarning("semantic indexing pending (embedder unavailable); keyword search works; run `mem sync`")
			} else {
				return rep, fmt.Errorf("engine: index batch (%d documents starting at %s): %w", len(ids), ids[0], err)
			}
		}
		for _, id := range ids {
			if _, ok := state[id]; ok {
				rep.Updated++
			} else {
				rep.Added++
			}
			rep.Indexed++
			report("index", id, desired[id].mem.Context)
		}
	}

	// A write whose embedding failed still commits its keyword rows with the
	// vectors marked pending. Now that the index is otherwise in step, retry
	// those; a still-broken embedder leaves them pending for the next run.
	if pendingChunks := e.Index.PendingVectors(); pendingChunks > 0 {
		for {
			filled, err := e.Index.FillPendingVectors(ctx)
			if err != nil {
				if filled > 0 {
					rep.Filled += filled
				}
				e.recordWarning(fmt.Sprintf("engine: fill pending vectors (%d chunks still pending): %v", e.Index.PendingVectors(), err))
				break
			}
			rep.Filled += filled
			if filled == 0 {
				break
			}
		}
	}

	rep.Duration = time.Since(start)
	return rep, nil
}
