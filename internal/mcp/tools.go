package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"memory/internal/engine"
	"memory/internal/store"
)

const (
	maxBodyBytes = 1 << 20      // 1 MiB
	maxGetBody   = 200 * 1024   // 200 KiB
	maxNameLen   = 200
	maxTagsCount = 20
	maxTagLen    = 64
	maxListItems = 200
	maxSnippet   = 400
)

const (
	searchDescription = "Search memories using hybrid search (BM25 keyword + vector similarity). Searches the 'global' context plus the specified context (unless no_global is true, which requires context). Fuzzy matching is on by default, set fuzzy:false for exact-word precision. Use to discover relevant prior knowledge, architectural decisions, user preferences, or solutions across sessions. Do NOT use search when you already have a memory's id or name (use 'get' instead). Contexts group memories; contexts cannot be created or deleted by agents (the user creates them via the CLI). Example: search(query=\"JWT auth middleware setup\", context=\"backend\")."

	getDescription = "Retrieve memories or context information. Call with NO arguments to orient yourself: returns the user's global instructions file (readme.md) and all active contexts (permanent/expiring) with memory counts. Call with only 'context' to list that context's memories (name, id, version, size). Call with 'id' or 'name'+'context' (defaults to 'global') to retrieve the full markdown note with metadata headers (id, version, tags). Use 'version' for subsequent safe upserts. Do not pass both 'id' and 'name'. Example: get(context=\"backend\", name=\"auth/jwt\")."

	upsertDescription = "Create or update a markdown memory note. Use to persist knowledge, decisions, or session logs across sessions. IDs are stable identifiers. Resolution: pass 'id' to update/rename an existing memory; pass 'name' to update-or-create; omit both to create an unnamed memory (<id>.md). Context defaults to 'global'; agents cannot create contexts (user creates them via CLI). Set 'if_version' to avoid overwriting concurrent edits (fetch version first with 'get'). Set 'append: true' to add lines to the end of a log. Example: upsert(context=\"backend\", name=\"auth/jwt\", body=\"Use RS256 for tokens.\", tags=[\"auth\",\"jwt\"], if_version=2)."

	deleteDescription = "Delete a memory permanently by its stable 'id'. Use only when the user explicitly requests deletion or when a memory is completely obsolete. Prefer 'upsert' to correct, update, or supersede information rather than deleting. Contexts and the default instructions file ('global/readme.md') cannot be deleted. Memory IDs can be found via 'search' or 'get'. Example: delete(id=\"mem_01h2x3y4z5\")."
)

func intPtr(i int) *int {
	return &i
}

// BuiltinTools returns the four defined MCP tools.
func BuiltinTools() []Tool {
	return []Tool{
		{
			Name:        "search",
			Description: searchDescription,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"query": {
						Type:        "string",
						Description: "Search query text to find relevant memories.",
					},
					"context": {
						Type:        "string",
						Description: "Optional context slug to search in addition to 'global'.",
					},
					"no_global": {
						Type:        "boolean",
						Description: "Exclude the 'global' context. Requires 'context' to be specified.",
					},
					"limit": {
						Type:        "integer",
						Description: "Maximum number of hits to return (1-50, default 8).",
						Minimum:     intPtr(1),
						Maximum:     intPtr(50),
					},
					"fuzzy": {
						Type:        "boolean",
						Description: "Enable fuzzy matching (default true). Set to false for exact-word precision.",
					},
				},
				Required:             []string{"query"},
				AdditionalProperties: false,
			},
		},
		{
			Name:        "get",
			Description: getDescription,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"context": {
						Type:        "string",
						Description: "Context slug. If used alone, lists that context's memories. If used with name, scopes the memory lookup (defaults to 'global').",
					},
					"name": {
						Type:        "string",
						Description: "Memory name to fetch within a context. Mutually exclusive with id.",
					},
					"id": {
						Type:        "string",
						Description: "Stable memory ID to fetch. Mutually exclusive with name.",
					},
				},
				AdditionalProperties: false,
			},
		},
		{
			Name:        "upsert",
			Description: upsertDescription,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"body": {
						Type:        "string",
						Description: "Markdown content of the memory (max 1 MiB).",
					},
					"context": {
						Type:        "string",
						Description: "Target context slug (defaults to 'global'). Context must already exist.",
					},
					"name": {
						Type:        "string",
						Description: "Optional memory name (max 200 chars). If id is omitted, updates or creates by name.",
					},
					"id": {
						Type:        "string",
						Description: "Optional memory ID to update or rename.",
					},
					"tags": {
						Type:        "array",
						Items:       &Property{Type: "string"},
						Description: "Optional tags (max 20 tags, each up to 64 chars).",
					},
					"append": {
						Type:        "boolean",
						Description: "If true, append body to existing content instead of overwriting.",
					},
					"if_version": {
						Type:        "integer",
						Description: "Perform write only if current memory version equals this number.",
					},
				},
				Required:             []string{"body"},
				AdditionalProperties: false,
			},
		},
		{
			Name:        "delete",
			Description: deleteDescription,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"id": {
						Type:        "string",
						Description: "Stable memory ID to delete.",
					},
				},
				Required:             []string{"id"},
				AdditionalProperties: false,
			},
		},
	}
}

func toolError(format string, args ...any) CallToolResult {
	return CallToolResult{
		IsError: true,
		Content: []ContentItem{
			{
				Type: "text",
				Text: fmt.Sprintf(format, args...),
			},
		},
	}
}

func toolSuccess(text string, structured any) CallToolResult {
	return CallToolResult{
		IsError: false,
		Content: []ContentItem{
			{
				Type: "text",
				Text: text,
			},
		},
		StructuredContent: structured,
	}
}

// executeSearch handles the search tool.
type searchArgs struct {
	Query    string `json:"query"`
	Context  string `json:"context,omitempty"`
	NoGlobal bool   `json:"no_global,omitempty"`
	Limit    *int   `json:"limit,omitempty"`
	Fuzzy    *bool  `json:"fuzzy,omitempty"`
}

func (s *session) executeSearch(ctx context.Context, raw json.RawMessage) CallToolResult {
	var args searchArgs
	if len(raw) > 0 && string(raw) != "null" {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&args); err != nil {
			return toolError("invalid search arguments: %v", err)
		}
	}

	if strings.TrimSpace(args.Query) == "" {
		return toolError("query is required and cannot be empty")
	}

	if args.NoGlobal && args.Context == "" {
		return toolError("no_global cannot be used without specifying a context; global is the default search space")
	}

	limit := 8
	if args.Limit != nil {
		if *args.Limit < 1 || *args.Limit > 50 {
			return toolError("limit must be between 1 and 50, got %d", *args.Limit)
		}
		limit = *args.Limit
	}

	res, err := s.eng.Search(ctx, engine.SearchRequest{
		Query:    args.Query,
		Context:  args.Context,
		NoGlobal: args.NoGlobal,
		Limit:    limit,
		PerDoc:   1,
		Fuzzy:    args.Fuzzy,
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return toolError("context '%s' does not exist; contexts are created by the user with `mem context create`; use 'global' or an existing context (call get with no arguments to list them)", args.Context)
		}
		return toolError("search failed: %v", err)
	}

	type HitJSON struct {
		ID           string   `json:"id"`
		DocID        string   `json:"doc_id"`
		Context      string   `json:"context"`
		Name         string   `json:"name"`
		Heading      string   `json:"heading"`
		Snippet      string   `json:"snippet"`
		Chunk        int      `json:"chunk"`
		Score        float64  `json:"score"`
		Sources      []string `json:"sources"`
		MatchedTerms []string `json:"matched_terms"`
	}

	hitsJSON := make([]HitJSON, 0, len(res.Hits))
	var sb strings.Builder

	if len(res.Degraded) > 0 {
		for _, d := range res.Degraded {
			sb.WriteString("note: ")
			sb.WriteString(d)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	if len(res.Hits) == 0 {
		sb.WriteString(fmt.Sprintf("No memories matched your query %q. Try a broader search query or call 'get' with no arguments to see available contexts.", args.Query))
	} else {
		for i, h := range res.Hits {
			snippet := h.Snippet
			if len(snippet) > maxSnippet {
				snippet = snippet[:maxSnippet] + "…"
			}
			matchedTerms := h.MatchedTerms
			if matchedTerms == nil {
				matchedTerms = []string{}
			}
			sources := h.Sources
			if sources == nil {
				sources = []string{}
			}
			hitsJSON = append(hitsJSON, HitJSON{
				ID:           h.DocID,
				DocID:        h.DocID,
				Context:      h.Context,
				Name:         h.Name,
				Heading:      h.Heading,
				Snippet:      snippet,
				Chunk:        h.Chunk,
				Score:        h.Score,
				Sources:      sources,
				MatchedTerms: matchedTerms,
			})

			tag := ""
			var marks []string
			for _, src := range h.Sources {
				if src == "fuzzy" || src == "name" {
					marks = append(marks, src)
				}
			}
			if len(marks) > 0 {
				tag = fmt.Sprintf(" [%s]", strings.Join(marks, ", "))
			}

			sb.WriteString(fmt.Sprintf("%d. %s/%s (id: %s, chunk %d, score %.3f)%s\n", i+1, h.Context, h.Name, h.DocID, h.Chunk, h.Score, tag))
			if len(h.MatchedTerms) > 0 {
				sb.WriteString(fmt.Sprintf("matched: %s\n", strings.Join(h.MatchedTerms, ", ")))
			}
			if h.Heading != "" {
				sb.WriteString(h.Heading)
				sb.WriteString("\n")
			}
			sb.WriteString(snippet)
			sb.WriteString("\n\n")
		}
		sb.WriteString(fmt.Sprintf("took_ms: %d", res.Took.Milliseconds()))
	}

	structured := map[string]any{
		"hits":     hitsJSON,
		"degraded": res.Degraded,
		"took_ms":  res.Took.Milliseconds(),
	}

	return toolSuccess(strings.TrimSpace(sb.String()), structured)
}

// executeGet handles the get tool.
type getArgs struct {
	Context string `json:"context,omitempty"`
	Name    string `json:"name,omitempty"`
	ID      string `json:"id,omitempty"`
}

func (s *session) executeGet(_ context.Context, raw json.RawMessage) CallToolResult {
	var args getArgs
	if len(raw) > 0 && string(raw) != "null" {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&args); err != nil {
			return toolError("invalid get arguments: %v", err)
		}
	}

	if args.ID != "" && args.Name != "" {
		return toolError("cannot specify both 'id' and 'name'; specify exactly one")
	}

	// 1. No arguments: default file + context list
	if args.Context == "" && args.Name == "" && args.ID == "" {
		df, err := s.eng.DefaultFile()
		if err != nil {
			return toolError("read default file: %v", err)
		}
		contexts, err := s.eng.Store.ListContexts()
		if err != nil {
			return toolError("list contexts: %v", err)
		}

		type ctxJSON struct {
			Slug        string     `json:"slug"`
			IsGlobal    bool       `json:"is_global"`
			CreatedAt   string     `json:"created_at"`
			ExpiresAt   *time.Time `json:"expires_at"`
			MemoryCount int        `json:"memory_count"`
		}

		ctxList := make([]ctxJSON, 0, len(contexts))
		var sb strings.Builder
		sb.WriteString(df.Body)
		sb.WriteString("\n\n---\n## Live Contexts\n")

		for _, c := range contexts {
			status := "permanent"
			if c.ExpiresAt != nil {
				d := time.Until(*c.ExpiresAt)
				if d <= 0 {
					status = "expired"
				} else {
					days := int(d.Hours()) / 24
					hours := int(d.Hours()) % 24
					status = fmt.Sprintf("expires in %dd %dh", days, hours)
				}
			}
			sb.WriteString(fmt.Sprintf("- %s (%s, %d memories)\n", c.Slug, status, c.MemoryCount))

			ctxList = append(ctxList, ctxJSON{
				Slug:        c.Slug,
				IsGlobal:    c.Slug == store.GlobalContext,
				CreatedAt:   c.CreatedAt.Format(time.RFC3339),
				ExpiresAt:   c.ExpiresAt,
				MemoryCount: c.MemoryCount,
			})
		}

		structured := map[string]any{
			"default_file": map[string]any{
				"id":         df.ID,
				"context":    df.Context,
				"name":       df.Name,
				"version":    df.Version,
				"updated_at": df.UpdatedAt.Format(time.RFC3339),
				"updated_by": df.UpdatedBy,
				"tags":       df.Tags,
				"body":       df.Body,
			},
			"contexts": ctxList,
		}
		return toolSuccess(strings.TrimSpace(sb.String()), structured)
	}

	// 2. Only context: list memories in that context
	if args.Context != "" && args.Name == "" && args.ID == "" {
		list, err := s.eng.Store.List(args.Context)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return toolError("context '%s' does not exist; contexts are created by the user with `mem context create`; use 'global' or an existing context (call get with no arguments to list them)", args.Context)
			}
			return toolError("list context memories: %v", err)
		}

		total := len(list)
		truncated := false
		if len(list) > maxListItems {
			list = list[:maxListItems]
			truncated = true
		}

		type memSummary struct {
			ID        string   `json:"id"`
			Name      string   `json:"name"`
			Version   int      `json:"version"`
			UpdatedAt string   `json:"updated_at"`
			UpdatedBy string   `json:"updated_by"`
			Tags      []string `json:"tags"`
			Size      int      `json:"size"`
		}

		summaries := make([]memSummary, 0, len(list))
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Memories in context %q (%d total):\n", args.Context, total))

		for _, item := range list {
			size := 0
			if full, err := s.eng.Store.Get(args.Context, item.ID); err == nil {
				size = len(full.Body)
			}
			summaries = append(summaries, memSummary{
				ID:        item.ID,
				Name:      item.Name,
				Version:   item.Version,
				UpdatedAt: item.UpdatedAt.Format(time.RFC3339),
				UpdatedBy: item.UpdatedBy,
				Tags:      item.Tags,
				Size:      size,
			})
			sb.WriteString(fmt.Sprintf("- %s (id: %s, version: %d, updated_at: %s, size: %d bytes)\n",
				item.Name, item.ID, item.Version, item.UpdatedAt.Format(time.RFC3339), size))
		}

		if truncated {
			sb.WriteString(fmt.Sprintf("\n…truncated at %d memories; use 'search' to find specific memories.", maxListItems))
		}

		structured := map[string]any{
			"context":   args.Context,
			"total":     total,
			"truncated": truncated,
			"memories":  summaries,
		}
		return toolSuccess(strings.TrimSpace(sb.String()), structured)
	}

	// 3. ID or Name (+ optional context)
	var mem store.Memory
	var err error
	if args.ID != "" {
		if args.Context != "" {
			mem, err = s.eng.Store.Get(args.Context, args.ID)
		} else {
			// Search global first, then fallback across all contexts
			mem, err = s.eng.Store.Get(store.GlobalContext, args.ID)
			if errors.Is(err, store.ErrNotFound) {
				contexts, listErr := s.eng.Store.ListContexts()
				if listErr == nil {
					for _, c := range contexts {
						if c.Slug == store.GlobalContext {
							continue
						}
						if found, findErr := s.eng.Store.Get(c.Slug, args.ID); findErr == nil {
							mem = found
							err = nil
							break
						}
					}
				}
			}
		}
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				if args.Context != "" {
					return toolError("memory %q does not exist in context %q", args.ID, args.Context)
				}
				return toolError("memory %q does not exist", args.ID)
			}
			return toolError("get memory: %v", err)
		}
	} else if args.Name != "" {
		ctxSlug := args.Context
		if ctxSlug == "" {
			ctxSlug = store.GlobalContext
		}
		mem, err = s.eng.Store.Get(ctxSlug, args.Name)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return toolError("memory %q does not exist in context %q", args.Name, ctxSlug)
			}
			return toolError("get memory: %v", err)
		}
	}

	body := mem.Body
	truncated := false
	if len(body) > maxGetBody {
		body = body[:maxGetBody] + "\n\n…truncated at 200 KiB; this memory is larger than 200 KiB (no pagination in v1)."
		truncated = true
	}

	tagsStr := "[]"
	if len(mem.Tags) > 0 {
		tagsStr = "[" + strings.Join(mem.Tags, ", ") + "]"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("id: %s\n", mem.ID))
	sb.WriteString(fmt.Sprintf("name: %s\n", mem.Name))
	sb.WriteString(fmt.Sprintf("context: %s\n", mem.Context))
	sb.WriteString(fmt.Sprintf("version: %d\n", mem.Version))
	sb.WriteString(fmt.Sprintf("updated_at: %s\n", mem.UpdatedAt.Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("updated_by: %s\n", mem.UpdatedBy))
	sb.WriteString(fmt.Sprintf("tags: %s\n\n", tagsStr))
	sb.WriteString(body)

	structured := map[string]any{
		"id":         mem.ID,
		"context":    mem.Context,
		"name":       mem.Name,
		"version":    mem.Version,
		"updated_at": mem.UpdatedAt.Format(time.RFC3339),
		"updated_by": mem.UpdatedBy,
		"tags":       mem.Tags,
		"body":       body,
		"truncated":  truncated,
	}
	return toolSuccess(strings.TrimSpace(sb.String()), structured)
}

// executeUpsert handles the upsert tool.
type upsertArgs struct {
	Body      string   `json:"body"`
	Context   string   `json:"context,omitempty"`
	Name      string   `json:"name,omitempty"`
	ID        string   `json:"id,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Append    bool     `json:"append,omitempty"`
	IfVersion *int     `json:"if_version,omitempty"`
}

func (s *session) executeUpsert(_ context.Context, raw json.RawMessage) CallToolResult {
	var args upsertArgs
	if len(raw) > 0 && string(raw) != "null" {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&args); err != nil {
			return toolError("invalid upsert arguments: %v", err)
		}
	}

	if len(args.Body) > maxBodyBytes {
		return toolError("body is %d bytes; the limit is %d bytes (1 MiB)", len(args.Body), maxBodyBytes)
	}

	if len(args.Name) > maxNameLen {
		return toolError("name exceeds %d characters limit", maxNameLen)
	}

	if len(args.Tags) > maxTagsCount {
		return toolError("maximum %d tags allowed, got %d", maxTagsCount, len(args.Tags))
	}
	for _, tag := range args.Tags {
		if len(tag) > maxTagLen {
			return toolError("tag %q exceeds %d characters limit", tag, maxTagLen)
		}
	}

	targetCtx := args.Context
	if targetCtx == "" {
		targetCtx = store.GlobalContext
	}

	isUpdate := false
	if args.ID != "" {
		isUpdate = true
	} else if args.Name != "" {
		if _, err := s.eng.Store.Get(targetCtx, args.Name); err == nil {
			isUpdate = true
		}
	}

	updatedBy := s.clientName
	if updatedBy == "" {
		updatedBy = "mcp"
	}

	warnBefore := len(s.eng.Warnings())

	mem, err := s.eng.Store.Upsert(store.UpsertInput{
		Context:   targetCtx,
		ID:        args.ID,
		Name:      args.Name,
		Body:      args.Body,
		UpdatedBy: updatedBy,
		Tags:      args.Tags,
		Append:    args.Append,
		IfVersion: args.IfVersion,
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) && (strings.Contains(err.Error(), "context") || strings.Contains(err.Error(), "does not exist")) {
			return toolError("context '%s' does not exist; contexts are created by the user with `mem context create`; use 'global' or an existing context (call get with no arguments to list them)", targetCtx)
		}
		if errors.Is(err, store.ErrConflict) {
			curVer := -1
			if args.ID != "" {
				if curMem, getErr := s.eng.Store.Get(targetCtx, args.ID); getErr == nil {
					curVer = curMem.Version
				}
			} else if args.Name != "" {
				if curMem, getErr := s.eng.Store.Get(targetCtx, args.Name); getErr == nil {
					curVer = curMem.Version
				}
			}
			verMsg := "version mismatch"
			if curVer >= 0 {
				verMsg = fmt.Sprintf("memory is currently at version %d", curVer)
			}
			expMsg := ""
			if args.IfVersion != nil {
				expMsg = fmt.Sprintf(" (expected %d)", *args.IfVersion)
			}
			return toolError("version conflict: %s%s; call 'get' to inspect the latest content, merge your changes, and retry", verMsg, expMsg)
		}
		return toolError("upsert failed: %v", err)
	}

	warnAfter := len(s.eng.Warnings())
	hasWarning := warnAfter > warnBefore

	status := "created"
	if isUpdate {
		status = "updated"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Memory %s successfully.\n", status))
	sb.WriteString(fmt.Sprintf("id: %s\n", mem.ID))
	sb.WriteString(fmt.Sprintf("name: %s\n", mem.Name))
	sb.WriteString(fmt.Sprintf("context: %s\n", mem.Context))
	sb.WriteString(fmt.Sprintf("version: %d\n", mem.Version))
	sb.WriteString(fmt.Sprintf("status: %s", status))

	if hasWarning {
		note := "saved, but search indexing failed; it will be repaired by `mem sync`"
		wList := s.eng.Warnings()
		if len(wList) > 0 {
			latest := wList[len(wList)-1]
			if strings.Contains(latest, "semantic indexing pending") {
				note = "semantic indexing pending (embedder unavailable); keyword search works; run `mem sync`"
			}
		}
		sb.WriteString("\nnote: " + note)
	}

	structured := map[string]any{
		"id":            mem.ID,
		"name":          mem.Name,
		"context":       mem.Context,
		"version":       mem.Version,
		"status":        status,
		"index_warning": hasWarning,
	}

	return toolSuccess(strings.TrimSpace(sb.String()), structured)
}

// executeDelete handles the delete tool.
type deleteArgs struct {
	ID string `json:"id"`
}

func (s *session) executeDelete(_ context.Context, raw json.RawMessage) CallToolResult {
	var args deleteArgs
	if len(raw) > 0 && string(raw) != "null" {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&args); err != nil {
			return toolError("invalid delete arguments: %v", err)
		}
	}

	if strings.TrimSpace(args.ID) == "" {
		return toolError("id is required and cannot be empty")
	}

	err := s.eng.Store.Delete(args.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return toolError("memory %q does not exist", args.ID)
		}
		if errors.Is(err, store.ErrReserved) {
			return toolError("the default file %q cannot be deleted; it contains global instructions", store.DefaultFileName)
		}
		return toolError("delete failed: %v", err)
	}

	text := fmt.Sprintf("Memory %q deleted successfully.", args.ID)
	structured := map[string]any{
		"id":      args.ID,
		"deleted": true,
	}
	return toolSuccess(text, structured)
}
