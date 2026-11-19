package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"memory/internal/engine"
	"memory/internal/search"
	"memory/internal/store"
)

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	// ModelsStatus never calls URL() or starts sidecars
	models := s.eng.ModelsStatus()

	var (
		docs    int
		chunks  int
		inSync  bool
		pending int
	)

	state, err := s.eng.Index.State()
	if err == nil {
		docs = len(state)
	}

	contexts, err := s.eng.Store.ListContexts()
	if err == nil {
		seen := 0
		for _, c := range contexts {
			chunks += s.eng.Index.ContextChunkCount(c.Slug)
			list, lerr := s.eng.Store.List(c.Slug)
			if lerr == nil {
				for _, m := range list {
					seen++
					st, ok := state[m.ID]
					if !ok || st.Version != m.Version || st.Context != m.Context {
						pending++
					} else {
						delete(state, m.ID)
					}
				}
			}
		}
		// Any remaining docs in state are orphans in the index
		pending += len(state)
		inSync = (pending == 0 && seen > 0) || (seen == 0 && docs == 0)
	}

	uptimeS := int(time.Since(s.opts.StartedAt).Seconds())
	if uptimeS < 0 {
		uptimeS = 0
	}

	expiryDays := s.opts.Config.DefaultTempExpiryDays
	if expiryDays <= 0 {
		expiryDays = 7
	}

	res := ServerStatusResponse{
		Version: s.opts.Version,
		DataDir: s.opts.DataDir,
		UptimeS: uptimeS,
		Config: ServerConfigStatus{
			DefaultTempExpiryDays: expiryDays,
			Rerank:                s.opts.Config.Rerank,
		},
		Models: models,
		Index: ServerIndexStatus{
			Docs:    docs,
			Chunks:  chunks,
			InSync:  inSync,
			Pending: pending,
		},
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleListContexts(w http.ResponseWriter, r *http.Request) {
	list, err := s.eng.Store.ListContexts()
	if err != nil {
		writeStoreOrEngineError(w, err)
		return
	}

	out := make([]ContextResponse, 0, len(list))
	var global *ContextResponse

	for _, c := range list {
		resp := toContextResponse(c)
		if c.Slug == store.GlobalContext {
			global = &resp
		} else {
			out = append(out, resp)
		}
	}

	if global != nil {
		out = append([]ContextResponse{*global}, out...)
	}

	writeJSON(w, http.StatusOK, out)
}

type postContextPayload struct {
	Slug       *string `json:"slug"`
	Temp       *bool   `json:"temp"`
	ExpiryDays *int    `json:"expiry_days"`
}

func (s *Server) handleCreateContext(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyOverhead)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	var req postContextPayload
	if err := dec.Decode(&req); err != nil {
		writeApiError(w, http.StatusBadRequest, "invalid", fmt.Sprintf("invalid json payload: %v", err), nil)
		return
	}

	var slug string
	if req.Slug != nil {
		slug = strings.TrimSpace(*req.Slug)
	}

	if slug == store.GlobalContext {
		writeApiError(w, http.StatusForbidden, "reserved", "context \"global\" always exists and cannot be created", nil)
		return
	}
	if slug != "" {
		if err := store.ValidateSlug(slug); err != nil {
			writeApiError(w, http.StatusBadRequest, "invalid", err.Error(), nil)
			return
		}
	}

	if req.ExpiryDays != nil && *req.ExpiryDays <= 0 {
		writeApiError(w, http.StatusBadRequest, "invalid", fmt.Sprintf("expiry days must be positive, got %d", *req.ExpiryDays), nil)
		return
	}

	var expiryDays *int
	if slug == "" {
		// auto tmp-xxxx, implies temp
		if req.ExpiryDays != nil {
			expiryDays = req.ExpiryDays
		} else {
			d := s.opts.Config.DefaultTempExpiryDays
			if d <= 0 {
				d = 7
			}
			expiryDays = &d
		}
	} else {
		// Named slug
		if req.Temp != nil && *req.Temp {
			if req.ExpiryDays != nil {
				expiryDays = req.ExpiryDays
			} else {
				d := s.opts.Config.DefaultTempExpiryDays
				if d <= 0 {
					d = 7
				}
				expiryDays = &d
			}
		} else {
			expiryDays = req.ExpiryDays
		}
	}

	ctx, err := s.eng.Store.CreateContext(slug, expiryDays)
	if err != nil {
		writeStoreOrEngineError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, toContextResponse(ctx))
}

func (s *Server) handleGetContext(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	ctx, err := s.eng.Store.GetContext(slug)
	if err != nil {
		writeStoreOrEngineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toContextResponse(ctx))
}

type patchContextPayload struct {
	ExpiryDays *int `json:"expiry_days"`
}

func (s *Server) handlePatchContext(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == store.GlobalContext {
		writeApiError(w, http.StatusForbidden, "reserved", "context \"global\" never expires", nil)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyOverhead)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	var req patchContextPayload
	if err := dec.Decode(&req); err != nil {
		writeApiError(w, http.StatusBadRequest, "invalid", fmt.Sprintf("invalid json payload: %v", err), nil)
		return
	}

	if req.ExpiryDays != nil && *req.ExpiryDays <= 0 {
		writeApiError(w, http.StatusBadRequest, "invalid", fmt.Sprintf("expiry days must be positive, got %d", *req.ExpiryDays), nil)
		return
	}

	if err := s.eng.Store.SetContextExpiry(slug, req.ExpiryDays); err != nil {
		writeStoreOrEngineError(w, err)
		return
	}

	ctx, err := s.eng.Store.GetContext(slug)
	if err != nil {
		writeStoreOrEngineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toContextResponse(ctx))
}

func (s *Server) handleDeleteContext(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == store.GlobalContext {
		writeApiError(w, http.StatusForbidden, "reserved", "context \"global\" cannot be deleted", nil)
		return
	}

	if err := s.eng.Store.DeleteContext(slug); err != nil {
		writeStoreOrEngineError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListMemories(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	q := r.URL.Query().Get("q")
	sortVal := r.URL.Query().Get("sort")
	if sortVal != "" && sortVal != "name" && sortVal != "updated" {
		writeApiError(w, http.StatusBadRequest, "invalid", fmt.Sprintf("invalid sort %q: must be 'name' or 'updated'", sortVal), nil)
		return
	}

	limit := 200
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		l, err := strconv.Atoi(lStr)
		if err != nil || l <= 0 {
			writeApiError(w, http.StatusBadRequest, "invalid", fmt.Sprintf("invalid limit %q: must be positive integer", lStr), nil)
			return
		}
		if l > 1000 {
			limit = 1000
		} else {
			limit = l
		}
	}

	offset := 0
	if oStr := r.URL.Query().Get("offset"); oStr != "" {
		o, err := strconv.Atoi(oStr)
		if err != nil || o < 0 {
			writeApiError(w, http.StatusBadRequest, "invalid", fmt.Sprintf("invalid offset %q: must be non-negative integer", oStr), nil)
			return
		}
		offset = o
	}

	page, err := s.eng.Store.ListPage(slug, store.ListPageOptions{
		Query:  q,
		Sort:   sortVal,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		writeStoreOrEngineError(w, err)
		return
	}

	items := make([]MemorySummaryResponse, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, toMemorySummaryResponse(item))
	}

	writeJSON(w, http.StatusOK, ListMemoriesResponse{
		Items: items,
		Total: page.Total,
	})
}

func (s *Server) handleGetMemory(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	name := r.URL.Query().Get("name")
	contextSlug := r.URL.Query().Get("context")

	if (id == "" && name == "") || (id != "" && name != "") {
		writeApiError(w, http.StatusBadRequest, "invalid", "query must specify exactly one of id or name", nil)
		return
	}

	if name != "" {
		if contextSlug == "" {
			contextSlug = store.GlobalContext
		}
		m, err := s.eng.Store.Get(contextSlug, name)
		if err != nil {
			writeStoreOrEngineError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toMemoryResponse(m, nil))
		return
	}

	// id lookup across contexts
	m, err := s.eng.Store.GetByID(id)
	if err != nil {
		writeStoreOrEngineError(w, err)
		return
	}
	if contextSlug != "" && m.Context != contextSlug {
		writeApiError(w, http.StatusNotFound, "not_found", fmt.Sprintf("memory %q does not belong to context %q", id, contextSlug), nil)
		return
	}

	writeJSON(w, http.StatusOK, toMemoryResponse(m, nil))
}

type putMemoryPayload struct {
	Context   *string  `json:"context"`
	ID        *string  `json:"id"`
	Name      *string  `json:"name"`
	Body      *string  `json:"body"`
	Tags      []string `json:"tags"`
	Append    bool     `json:"append"`
	IfVersion *int     `json:"if_version"`
	UpdatedBy *string  `json:"updated_by"`
}

func (s *Server) handlePutMemory(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyOverhead)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	var req putMemoryPayload
	if err := dec.Decode(&req); err != nil {
		writeApiError(w, http.StatusBadRequest, "invalid", fmt.Sprintf("invalid json payload: %v", err), nil)
		return
	}

	if req.Body == nil {
		writeApiError(w, http.StatusBadRequest, "invalid", "memory body is required", nil)
		return
	}

	contextSlug := store.GlobalContext
	if req.Context != nil && strings.TrimSpace(*req.Context) != "" {
		contextSlug = strings.TrimSpace(*req.Context)
	}

	var id string
	if req.ID != nil {
		id = strings.TrimSpace(*req.ID)
	}

	var name string
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
	}

	updatedBy := "dashboard"
	if req.UpdatedBy != nil && strings.TrimSpace(*req.UpdatedBy) != "" {
		updatedBy = strings.TrimSpace(*req.UpdatedBy)
	}

	in := store.UpsertInput{
		Context:   contextSlug,
		ID:        id,
		Name:      name,
		Body:      *req.Body,
		UpdatedBy: updatedBy,
		Tags:      req.Tags,
		Append:    req.Append,
		IfVersion: req.IfVersion,
	}

	warningsBefore := len(s.eng.Warnings())
	m, created, err := s.eng.Store.UpsertEx(in)
	if err != nil {
		writeStoreOrEngineError(w, err)
		return
	}

	warningsAfter := s.eng.Warnings()
	var indexWarning *string
	if len(warningsAfter) > warningsBefore {
		wMsg := warningsAfter[len(warningsAfter)-1]
		indexWarning = &wMsg
	}

	statusCode := http.StatusOK
	if created {
		statusCode = http.StatusCreated
	}

	writeJSON(w, statusCode, toMemoryResponse(m, indexWarning))
}

func (s *Server) handleDeleteMemory(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		writeApiError(w, http.StatusBadRequest, "invalid", "query parameter 'id' is required", nil)
		return
	}

	if err := s.eng.Store.Delete(id); err != nil {
		writeStoreOrEngineError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDefaultMemory(w http.ResponseWriter, r *http.Request) {
	m, err := s.eng.DefaultFile()
	if err != nil {
		writeStoreOrEngineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toMemoryResponse(m, nil))
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeApiError(w, http.StatusBadRequest, "invalid", "query parameter 'q' is required", nil)
		return
	}

	contextSlug := r.URL.Query().Get("context")
	noGlobalStr := r.URL.Query().Get("no_global")
	noGlobal := noGlobalStr == "true" || noGlobalStr == "1"

	if noGlobal && contextSlug == "" {
		writeApiError(w, http.StatusBadRequest, "invalid", "no_global requires a context to search", nil)
		return
	}

	var fuzzy *bool
	if fStr := r.URL.Query().Get("fuzzy"); fStr != "" {
		if fStr == "1" || fStr == "true" {
			v := true
			fuzzy = &v
		} else if fStr == "0" || fStr == "false" {
			v := false
			fuzzy = &v
		} else {
			writeApiError(w, http.StatusBadRequest, "invalid", fmt.Sprintf("invalid fuzzy %q: must be 0 or 1", fStr), nil)
			return
		}
	}

	limit := 8
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		l, err := strconv.Atoi(lStr)
		if err != nil || l <= 0 {
			writeApiError(w, http.StatusBadRequest, "invalid", fmt.Sprintf("invalid limit %q: must be positive integer", lStr), nil)
			return
		}
		if l > 50 {
			limit = 50
		} else {
			limit = l
		}
	}

	perDoc := 1
	if pStr := r.URL.Query().Get("per_doc"); pStr != "" {
		p, err := strconv.Atoi(pStr)
		if err != nil || p <= 0 {
			writeApiError(w, http.StatusBadRequest, "invalid", fmt.Sprintf("invalid per_doc %q: must be positive integer", pStr), nil)
			return
		}
		perDoc = p
	}

	res, err := s.eng.Search(r.Context(), engine.SearchRequest{
		Query:    q,
		Context:  contextSlug,
		NoGlobal: noGlobal,
		Limit:    limit,
		PerDoc:   perDoc,
		Fuzzy:    fuzzy,
	})
	if err != nil {
		if errors.Is(err, engine.ErrEmptyQuery) {
			writeApiError(w, http.StatusBadRequest, "invalid", err.Error(), nil)
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			writeApiError(w, http.StatusNotFound, "not_found", err.Error(), nil)
			return
		}
		if strings.Contains(err.Error(), "no contexts to search") {
			writeApiError(w, http.StatusBadRequest, "invalid", err.Error(), nil)
			return
		}
		writeApiError(w, http.StatusServiceUnavailable, "unavailable", err.Error(), nil)
		return
	}

	hits := res.Hits
	if hits == nil {
		hits = []search.Hit{}
	}
	for i := range hits {
		if hits[i].MatchedTerms == nil {
			hits[i].MatchedTerms = []string{}
		}
		if hits[i].Sources == nil {
			hits[i].Sources = []string{}
		}
	}
	degraded := res.Degraded
	if degraded == nil {
		degraded = []string{}
	}

	tookMs := float64(res.Took.Microseconds()) / 1000.0

	writeJSON(w, http.StatusOK, SearchResponse{
		Hits:     hits,
		Degraded: degraded,
		TookMs:   tookMs,
	})
}

func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	s.syncMu.Lock()
	if s.syncing {
		s.syncMu.Unlock()
		writeApiError(w, http.StatusServiceUnavailable, "unavailable", "sync already running", nil)
		return
	}
	s.syncing = true
	s.syncMu.Unlock()

	defer func() {
		s.syncMu.Lock()
		s.syncing = false
		s.syncMu.Unlock()
	}()

	rep, err := s.eng.Sync(r.Context())
	if err != nil {
		writeApiError(w, http.StatusServiceUnavailable, "unavailable", err.Error(), nil)
		return
	}

	writeJSON(w, http.StatusOK, SyncResponse{
		Indexed: rep.Indexed,
		Removed: rep.Removed,
		Errors:  []string{},
	})
}
