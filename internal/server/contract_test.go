package server

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

func assertKeys(t *testing.T, m map[string]any, required []string) {
	t.Helper()
	for _, k := range required {
		if _, ok := m[k]; !ok {
			t.Errorf("missing required key %q in response: %+v", k, m)
		}
	}
}

func parseMap(t *testing.T, recBody []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(recBody, &m); err != nil {
		t.Fatalf("failed to parse JSON map: %v\nbody: %s", err, string(recBody))
	}
	return m
}

func TestApiContract(t *testing.T) {
	h := newTestHarness(t, nil)

	// 1. GET /api/status
	t.Run("GET /api/status contract", func(t *testing.T) {
		rec := h.request("GET", "/api/status", nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
		m := parseMap(t, rec.Body.Bytes())
		assertKeys(t, m, []string{"version", "data_dir", "uptime_s", "config", "models", "index"})

		cfgMap, ok := m["config"].(map[string]any)
		if !ok {
			t.Fatalf("config is not a map: %T", m["config"])
		}
		assertKeys(t, cfgMap, []string{"default_temp_expiry_days", "rerank"})

		modelsMap, ok := m["models"].(map[string]any)
		if !ok {
			t.Fatalf("models is not a map: %T", m["models"])
		}
		assertKeys(t, modelsMap, []string{"embed", "rerank"})

		embedMap, ok := modelsMap["embed"].(map[string]any)
		if !ok {
			t.Fatalf("models.embed is not a map: %T", modelsMap["embed"])
		}
		assertKeys(t, embedMap, []string{"state"})

		rerankMap, ok := modelsMap["rerank"].(map[string]any)
		if !ok {
			t.Fatalf("models.rerank is not a map: %T", modelsMap["rerank"])
		}
		assertKeys(t, rerankMap, []string{"enabled", "state"})

		indexMap, ok := m["index"].(map[string]any)
		if !ok {
			t.Fatalf("index is not a map: %T", m["index"])
		}
		assertKeys(t, indexMap, []string{"docs", "chunks", "in_sync", "pending"})
	})

	// 2. GET /api/contexts
	t.Run("GET /api/contexts contract", func(t *testing.T) {
		rec := h.request("GET", "/api/contexts", nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
		var list []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatalf("parse: %v", err)
		}
		if len(list) == 0 {
			t.Fatal("empty contexts list")
		}
		assertKeys(t, list[0], []string{"slug", "is_global", "created_at", "expires_at", "memory_count"})
	})

	// 3. POST /api/contexts
	t.Run("POST /api/contexts contract", func(t *testing.T) {
		rec := h.request("POST", "/api/contexts", map[string]any{"slug": "contract-ctx"}, nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status %d", rec.Code)
		}
		m := parseMap(t, rec.Body.Bytes())
		assertKeys(t, m, []string{"slug", "is_global", "created_at", "expires_at", "memory_count"})
	})

	// 4. GET /api/contexts/{slug}
	t.Run("GET /api/contexts/{slug} contract", func(t *testing.T) {
		rec := h.request("GET", "/api/contexts/contract-ctx", nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
		m := parseMap(t, rec.Body.Bytes())
		assertKeys(t, m, []string{"slug", "is_global", "created_at", "expires_at", "memory_count"})
	})

	// 5. PATCH /api/contexts/{slug}
	t.Run("PATCH /api/contexts/{slug} contract", func(t *testing.T) {
		days := 5
		rec := h.request("PATCH", "/api/contexts/contract-ctx", map[string]any{"expiry_days": days}, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
		m := parseMap(t, rec.Body.Bytes())
		assertKeys(t, m, []string{"slug", "is_global", "created_at", "expires_at", "memory_count"})
	})

	// 6. PUT /api/memory (created)
	var createdID string
	t.Run("PUT /api/memory (create) contract", func(t *testing.T) {
		rec := h.request("PUT", "/api/memory", map[string]any{
			"context": "contract-ctx",
			"name":    "contract-test.md",
			"body":    "# Contract Body\n\nTesting exact shapes.",
			"tags":    []string{"contract"},
		}, nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		m := parseMap(t, rec.Body.Bytes())
		assertKeys(t, m, []string{"id", "context", "name", "has_name", "version", "updated_at", "updated_by", "tags", "size", "body"})
		createdID = m["id"].(string)

		// Assert types
		if _, ok := m["has_name"].(bool); !ok {
			t.Errorf("has_name not bool: %v", reflect.TypeOf(m["has_name"]))
		}
		if _, ok := m["version"].(float64); !ok {
			t.Errorf("version not number: %v", reflect.TypeOf(m["version"]))
		}
		if _, ok := m["size"].(float64); !ok {
			t.Errorf("size not number: %v", reflect.TypeOf(m["size"]))
		}
		if tags, ok := m["tags"].([]any); !ok || len(tags) != 1 {
			t.Errorf("tags not array: %+v", m["tags"])
		}
	})

	// 7. GET /api/contexts/{slug}/memories
	t.Run("GET /api/contexts/{slug}/memories contract", func(t *testing.T) {
		rec := h.request("GET", "/api/contexts/contract-ctx/memories", nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
		m := parseMap(t, rec.Body.Bytes())
		assertKeys(t, m, []string{"items", "total"})
		items, ok := m["items"].([]any)
		if !ok || len(items) == 0 {
			t.Fatalf("items not array or empty: %+v", m["items"])
		}
		item0, ok := items[0].(map[string]any)
		if !ok {
			t.Fatalf("item0 not map: %+v", items[0])
		}
		assertKeys(t, item0, []string{"id", "context", "name", "has_name", "version", "updated_at", "updated_by", "tags", "size"})
		// MemorySummary must NOT have body
		if _, hasBody := item0["body"]; hasBody {
			t.Errorf("MemorySummary in list must not have body!")
		}
	})

	// 8. GET /api/memory
	t.Run("GET /api/memory contract", func(t *testing.T) {
		rec := h.request("GET", "/api/memory?id="+createdID, nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
		m := parseMap(t, rec.Body.Bytes())
		assertKeys(t, m, []string{"id", "context", "name", "has_name", "version", "updated_at", "updated_by", "tags", "size", "body"})
	})

	// 9. GET /api/default
	t.Run("GET /api/default contract", func(t *testing.T) {
		rec := h.request("GET", "/api/default", nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
		m := parseMap(t, rec.Body.Bytes())
		assertKeys(t, m, []string{"id", "context", "name", "has_name", "version", "updated_at", "updated_by", "tags", "size", "body"})
	})

	// 10. GET /api/search
	t.Run("GET /api/search contract", func(t *testing.T) {
		rec := h.request("GET", "/api/search?q=Contract", nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
		m := parseMap(t, rec.Body.Bytes())
		assertKeys(t, m, []string{"hits", "degraded", "took_ms"})
		hits, ok := m["hits"].([]any)
		if !ok {
			t.Fatalf("hits not array: %+v", m["hits"])
		}
		if len(hits) > 0 {
			hit0 := hits[0].(map[string]any)
			assertKeys(t, hit0, []string{"doc_id", "context", "name", "heading", "snippet", "chunk", "score", "sources", "matched_terms"})
		}

		// Valid fuzzy=1
		recFuzzy1 := h.request("GET", "/api/search?q=Contract&fuzzy=1", nil, nil)
		if recFuzzy1.Code != http.StatusOK {
			t.Fatalf("fuzzy=1 status %d, want 200", recFuzzy1.Code)
		}

		// Valid fuzzy=0
		recFuzzy0 := h.request("GET", "/api/search?q=Contract&fuzzy=0", nil, nil)
		if recFuzzy0.Code != http.StatusOK {
			t.Fatalf("fuzzy=0 status %d, want 200", recFuzzy0.Code)
		}

		// Invalid fuzzy value returns invalid (400)
		recFuzzyInvalid := h.request("GET", "/api/search?q=Contract&fuzzy=invalid", nil, nil)
		if recFuzzyInvalid.Code != http.StatusBadRequest {
			t.Fatalf("fuzzy=invalid status %d, want 400", recFuzzyInvalid.Code)
		}
		errM := parseMap(t, recFuzzyInvalid.Body.Bytes())
		assertKeys(t, errM, []string{"error"})
		errMap := errM["error"].(map[string]any)
		if errMap["kind"] != "invalid" {
			t.Errorf("error.kind = %v, want invalid", errMap["kind"])
		}
	})

	// 11. POST /api/sync
	t.Run("POST /api/sync contract", func(t *testing.T) {
		rec := h.request("POST", "/api/sync", nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
		m := parseMap(t, rec.Body.Bytes())
		assertKeys(t, m, []string{"indexed", "removed", "errors"})
	})

	// 12. DELETE /api/memory
	t.Run("DELETE /api/memory contract", func(t *testing.T) {
		rec := h.request("DELETE", "/api/memory?id="+createdID, nil, nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status %d, want 204", rec.Code)
		}
	})

	// 13. DELETE /api/contexts/{slug}
	t.Run("DELETE /api/contexts/{slug} contract", func(t *testing.T) {
		rec := h.request("DELETE", "/api/contexts/contract-ctx", nil, nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status %d, want 204", rec.Code)
		}
	})

	// 14. Error response contract
	t.Run("ApiError contract", func(t *testing.T) {
		rec := h.request("GET", "/api/memory?id=000000000000", nil, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status %d", rec.Code)
		}
		m := parseMap(t, rec.Body.Bytes())
		assertKeys(t, m, []string{"error"})
		errMap, ok := m["error"].(map[string]any)
		if !ok {
			t.Fatalf("error not map: %+v", m["error"])
		}
		assertKeys(t, errMap, []string{"kind", "message"})
		if errMap["kind"] != "not_found" {
			t.Errorf("kind = %v, want not_found", errMap["kind"])
		}
	})
}
