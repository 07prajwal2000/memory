package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"memory/internal/engine"
	"memory/internal/search"
	"memory/internal/store"
)

type ApiErrorDetail struct {
	CurrentVersion *int `json:"current_version,omitempty"`
}

type ApiErrorBody struct {
	Kind    string          `json:"kind"`
	Message string          `json:"message"`
	Details *ApiErrorDetail `json:"details,omitempty"`
}

type ApiErrorResponse struct {
	Error ApiErrorBody `json:"error"`
}

func writeApiError(w http.ResponseWriter, status int, kind, message string, details *ApiErrorDetail) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ApiErrorResponse{
		Error: ApiErrorBody{
			Kind:    kind,
			Message: message,
			Details: details,
		},
	})
}

var versionPattern = regexp.MustCompile(`version\s+(\d+)`)

func writeStoreOrEngineError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	var conf *store.ConflictError
	if errors.As(err, &conf) {
		ver := conf.CurrentVersion
		writeApiError(w, http.StatusConflict, "conflict", err.Error(), &ApiErrorDetail{CurrentVersion: &ver})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeApiError(w, http.StatusNotFound, "not_found", err.Error(), nil)
		return
	}
	if errors.Is(err, store.ErrConflict) {
		var detail *ApiErrorDetail
		if m := versionPattern.FindStringSubmatch(err.Error()); len(m) > 1 {
			if v, convErr := strconv.Atoi(m[1]); convErr == nil {
				detail = &ApiErrorDetail{CurrentVersion: &v}
			}
		}
		writeApiError(w, http.StatusConflict, "conflict", err.Error(), detail)
		return
	}
	if errors.Is(err, store.ErrExists) {
		writeApiError(w, http.StatusConflict, "exists", err.Error(), nil)
		return
	}
	if errors.Is(err, store.ErrReserved) {
		writeApiError(w, http.StatusForbidden, "reserved", err.Error(), nil)
		return
	}
	if errors.Is(err, store.ErrInvalid) || errors.Is(err, engine.ErrEmptyQuery) {
		writeApiError(w, http.StatusBadRequest, "invalid", err.Error(), nil)
		return
	}
	writeApiError(w, http.StatusInternalServerError, "internal", err.Error(), nil)
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

type ContextResponse struct {
	Slug        string  `json:"slug"`
	IsGlobal    bool    `json:"is_global"`
	CreatedAt   string  `json:"created_at"`
	ExpiresAt   *string `json:"expires_at"`
	MemoryCount int     `json:"memory_count"`
}

func toContextResponse(c store.Context) ContextResponse {
	res := ContextResponse{
		Slug:        c.Slug,
		IsGlobal:    c.Slug == store.GlobalContext,
		CreatedAt:   c.CreatedAt.UTC().Format(time.RFC3339),
		MemoryCount: c.MemoryCount,
	}
	if c.ExpiresAt != nil {
		exp := c.ExpiresAt.UTC().Format(time.RFC3339)
		res.ExpiresAt = &exp
	}
	return res
}

type MemorySummaryResponse struct {
	ID        string   `json:"id"`
	Context   string   `json:"context"`
	Name      string   `json:"name"`
	HasName   bool     `json:"has_name"`
	Version   int      `json:"version"`
	UpdatedAt string   `json:"updated_at"`
	UpdatedBy string   `json:"updated_by"`
	Tags      []string `json:"tags"`
	Size      int      `json:"size"`
}

func toMemorySummaryResponse(m store.MemorySummary) MemorySummaryResponse {
	tags := m.Tags
	if tags == nil {
		tags = []string{}
	}
	return MemorySummaryResponse{
		ID:        m.ID,
		Context:   m.Context,
		Name:      m.Name,
		HasName:   m.HasName,
		Version:   m.Version,
		UpdatedAt: m.UpdatedAt.UTC().Format(time.RFC3339),
		UpdatedBy: m.UpdatedBy,
		Tags:      tags,
		Size:      m.Size,
	}
}

type MemoryResponse struct {
	ID           string   `json:"id"`
	Context      string   `json:"context"`
	Name         string   `json:"name"`
	HasName      bool     `json:"has_name"`
	Version      int      `json:"version"`
	UpdatedAt    string   `json:"updated_at"`
	UpdatedBy    string   `json:"updated_by"`
	Tags         []string `json:"tags"`
	Size         int      `json:"size"`
	Body         string   `json:"body"`
	IndexWarning *string  `json:"index_warning,omitempty"`
}

func toMemoryResponse(m store.Memory, indexWarning *string) MemoryResponse {
	tags := m.Tags
	if tags == nil {
		tags = []string{}
	}
	return MemoryResponse{
		ID:           m.ID,
		Context:      m.Context,
		Name:         m.Name,
		HasName:      m.HasName,
		Version:      m.Version,
		UpdatedAt:    m.UpdatedAt.UTC().Format(time.RFC3339),
		UpdatedBy:    m.UpdatedBy,
		Tags:         tags,
		Size:         m.Size,
		Body:         m.Body,
		IndexWarning: indexWarning,
	}
}

type ListMemoriesResponse struct {
	Items []MemorySummaryResponse `json:"items"`
	Total int                     `json:"total"`
}

type SearchResponse struct {
	Hits     []search.Hit `json:"hits"`
	Degraded []string     `json:"degraded"`
	TookMs   float64      `json:"took_ms"`
}

type SyncResponse struct {
	Indexed int      `json:"indexed"`
	Removed int      `json:"removed"`
	Errors  []string `json:"errors"`
}

type ServerConfigStatus struct {
	DefaultTempExpiryDays int  `json:"default_temp_expiry_days"`
	Rerank                bool `json:"rerank"`
}

type ServerIndexStatus struct {
	Docs    int  `json:"docs"`
	Chunks  int  `json:"chunks"`
	InSync  bool `json:"in_sync"`
	Pending int  `json:"pending"`
}

type ServerStatusResponse struct {
	Version string              `json:"version"`
	DataDir string              `json:"data_dir"`
	UptimeS int                 `json:"uptime_s"`
	Config  ServerConfigStatus  `json:"config"`
	Models  engine.ModelsStatus `json:"models"`
	Index   ServerIndexStatus   `json:"index"`
}
