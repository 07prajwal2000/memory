package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"memory/internal/config"
	"memory/internal/engine"
	"memory/internal/llm"
	"memory/internal/search"
)

var testTime = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type fakeEmbedder struct {
	mu    sync.Mutex
	calls int
}

func (f *fakeEmbedder) Embed(ctx context.Context, kind llm.Kind, texts []string) ([][]float32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls += len(texts)
	out := make([][]float32, len(texts))
	for i, text := range texts {
		vec := make([]float32, search.ModelDim)
		h := sha256.Sum256([]byte(text))
		for d := range vec {
			vec[d] = float32(int8(h[d%len(h)])) / 128.0
		}
		var sum float64
		for _, x := range vec {
			sum += float64(x * x)
		}
		if sum > 0 {
			norm := float32(math.Sqrt(sum))
			for d := range vec {
				vec[d] /= norm
			}
		}
		out[i] = vec
	}
	return out, nil
}

type testHarness struct {
	t       *testing.T
	dir     string
	cfg     config.Config
	clock   *fakeClock
	embed   *fakeEmbedder
	eng     *engine.Engine
	handler http.Handler
	assets  fstest.MapFS
}

func newTestHarness(t *testing.T, modifyOpts func(*Options)) *testHarness {
	t.Helper()
	dir := t.TempDir()
	clock := &fakeClock{now: testTime}
	embed := &fakeEmbedder{}

	cfg := config.Config{
		DataDir:               filepath.Join(dir, "data"),
		ModelsDir:             filepath.Join(dir, "models"),
		Port:                  7777,
		DefaultTempExpiryDays: 7,
		Rerank:                false,
	}

	eng, err := engine.Open(cfg,
		engine.WithEmbedder(embed),
		engine.WithClock(clock.Now),
	)
	if err != nil {
		t.Fatalf("engine.Open: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	assets := fstest.MapFS{
		"index.html":                &fstest.MapFile{Data: []byte("<!DOCTYPE html><html><head><title>mem</title></head><body>Dashboard</body></html>")},
		"assets/index-test.js":      &fstest.MapFile{Data: []byte("console.log('test bundle');")},
		"assets/index-test.css":     &fstest.MapFile{Data: []byte("body { background: #000; }")},
		"assets/icon.svg":           &fstest.MapFile{Data: []byte("<svg></svg>")},
		"favicon.svg":               &fstest.MapFile{Data: []byte("<svg></svg>")},
	}

	opts := Options{
		Version:   "0.1.0",
		DataDir:   cfg.DataDir,
		Config:    cfg,
		StartedAt: testTime,
		Assets:    assets,
	}
	if modifyOpts != nil {
		modifyOpts(&opts)
	}

	handler := New(eng, opts)

	return &testHarness{
		t:       t,
		dir:     dir,
		cfg:     cfg,
		clock:   clock,
		embed:   embed,
		eng:     eng,
		handler: handler,
		assets:  assets,
	}
}

func (h *testHarness) request(method, path string, body any, modifyReq func(*http.Request)) *httptest.ResponseRecorder {
	h.t.Helper()
	var bodyReader io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			bodyReader = strings.NewReader(b)
		case []byte:
			bodyReader = bytes.NewReader(b)
		default:
			data, err := json.Marshal(body)
			if err != nil {
				h.t.Fatalf("marshal body: %v", err)
			}
			bodyReader = bytes.NewReader(data)
		}
	}

	req := httptest.NewRequest(method, path, bodyReader)
	req.Host = "127.0.0.1:7777"
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if modifyReq != nil {
		modifyReq(req)
	}

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec
}

func decodeJSON[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("failed to decode JSON response (status %d): %v\nbody: %s", rec.Code, err, rec.Body.String())
	}
	return out
}

func TestContextsEndpoints(t *testing.T) {
	h := newTestHarness(t, nil)

	// GET /api/contexts should include "global" pinned first
	rec := h.request("GET", "/api/contexts", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/contexts = %d, want 200", rec.Code)
	}
	ctxs := decodeJSON[[]ContextResponse](t, rec)
	if len(ctxs) < 1 || ctxs[0].Slug != "global" || !ctxs[0].IsGlobal {
		t.Fatalf("expected global context first, got: %+v", ctxs)
	}

	// POST /api/contexts creates named context
	rec = h.request("POST", "/api/contexts", map[string]any{"slug": "work"}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/contexts = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	cWork := decodeJSON[ContextResponse](t, rec)
	if cWork.Slug != "work" || cWork.IsGlobal || cWork.ExpiresAt != nil {
		t.Fatalf("unexpected created context: %+v", cWork)
	}

	// POST /api/contexts creates auto tmp-xxxx context
	rec = h.request("POST", "/api/contexts", map[string]any{}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST auto tmp context = %d: %s", rec.Code, rec.Body.String())
	}
	cTmp := decodeJSON[ContextResponse](t, rec)
	if !strings.HasPrefix(cTmp.Slug, "tmp-") || cTmp.ExpiresAt == nil {
		t.Fatalf("expected auto temp context with expiry, got: %+v", cTmp)
	}

	// POST duplicate context => 409 exists
	rec = h.request("POST", "/api/contexts", map[string]any{"slug": "work"}, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("POST duplicate context = %d, want 409", rec.Code)
	}
	errResp := decodeJSON[ApiErrorResponse](t, rec)
	if errResp.Error.Kind != "exists" {
		t.Fatalf("expected error kind 'exists', got %q", errResp.Error.Kind)
	}

	// POST global context => 403 reserved
	rec = h.request("POST", "/api/contexts", map[string]any{"slug": "global"}, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST global context = %d, want 403", rec.Code)
	}
	errResp = decodeJSON[ApiErrorResponse](t, rec)
	if errResp.Error.Kind != "reserved" {
		t.Fatalf("expected error kind 'reserved', got %q", errResp.Error.Kind)
	}

	// GET /api/contexts/{slug}
	rec = h.request("GET", "/api/contexts/work", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/contexts/work = %d", rec.Code)
	}
	gotWork := decodeJSON[ContextResponse](t, rec)
	if gotWork.Slug != "work" {
		t.Fatalf("expected work, got %s", gotWork.Slug)
	}

	// GET unknown context => 404 not_found
	rec = h.request("GET", "/api/contexts/nonexistent", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET nonexistent = %d, want 404", rec.Code)
	}

	// PATCH /api/contexts/{slug} expiry
	days := 10
	rec = h.request("PATCH", "/api/contexts/work", map[string]any{"expiry_days": days}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH expiry = %d: %s", rec.Code, rec.Body.String())
	}
	patchedWork := decodeJSON[ContextResponse](t, rec)
	if patchedWork.ExpiresAt == nil {
		t.Fatalf("expected ExpiresAt set")
	}

	// PATCH clear expiry to null (permanent)
	rec = h.request("PATCH", "/api/contexts/work", map[string]any{"expiry_days": nil}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH clear expiry = %d", rec.Code)
	}
	clearedWork := decodeJSON[ContextResponse](t, rec)
	if clearedWork.ExpiresAt != nil {
		t.Fatalf("expected null ExpiresAt")
	}

	// DELETE /api/contexts/work => 204
	rec = h.request("DELETE", "/api/contexts/work", nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE context = %d, want 204", rec.Code)
	}

	// DELETE global context => 403 reserved
	rec = h.request("DELETE", "/api/contexts/global", nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("DELETE global context = %d, want 403", rec.Code)
	}
}

func TestMemoriesCRUDAndConflict(t *testing.T) {
	h := newTestHarness(t, nil)

	// Create context
	_ = h.request("POST", "/api/contexts", map[string]any{"slug": "testctx"}, nil)

	// PUT /api/memory create => 201
	rec := h.request("PUT", "/api/memory", map[string]any{
		"context": "testctx",
		"name":    "guide.md",
		"body":    "first version",
		"tags":    []string{"doc", "setup"},
	}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("PUT /api/memory create = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	m := decodeJSON[MemoryResponse](t, rec)
	if m.Version != 1 || m.Body != "first version" || m.UpdatedBy != "dashboard" || m.Size != len("first version") {
		t.Fatalf("unexpected created memory: %+v", m)
	}

	// PUT update => 200
	rec = h.request("PUT", "/api/memory", map[string]any{
		"context":    "testctx",
		"id":         m.ID,
		"body":       "second version",
		"if_version": 1,
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /api/memory update = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	m2 := decodeJSON[MemoryResponse](t, rec)
	if m2.Version != 2 || m2.Body != "second version" {
		t.Fatalf("unexpected updated memory: %+v", m2)
	}

	// PUT conflict => 409 conflict with current_version
	badVersion := 1
	rec = h.request("PUT", "/api/memory", map[string]any{
		"context":    "testctx",
		"id":         m.ID,
		"body":       "conflicting edit",
		"if_version": badVersion,
	}, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("PUT conflict = %d, want 409", rec.Code)
	}
	errResp := decodeJSON[ApiErrorResponse](t, rec)
	if errResp.Error.Kind != "conflict" {
		t.Fatalf("expected error kind 'conflict', got %q", errResp.Error.Kind)
	}
	if errResp.Error.Details == nil || errResp.Error.Details.CurrentVersion == nil || *errResp.Error.Details.CurrentVersion != 2 {
		t.Fatalf("expected details.current_version = 2, got: %+v", errResp.Error.Details)
	}

	// GET /api/memory by id without context (cross-context resolution)
	rec = h.request("GET", "/api/memory?id="+m.ID, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/memory by id = %d: %s", rec.Code, rec.Body.String())
	}
	gotM := decodeJSON[MemoryResponse](t, rec)
	if gotM.ID != m.ID || gotM.Context != "testctx" {
		t.Fatalf("unexpected memory by id: %+v", gotM)
	}

	// GET /api/memory by id with mismatching context => 404
	rec = h.request("GET", "/api/memory?id="+m.ID+"&context=global", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET memory mismatching context = %d, want 404", rec.Code)
	}

	// GET /api/contexts/{slug}/memories
	rec = h.request("GET", "/api/contexts/testctx/memories?q=guide", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET list memories = %d: %s", rec.Code, rec.Body.String())
	}
	listResp := decodeJSON[ListMemoriesResponse](t, rec)
	if listResp.Total != 1 || len(listResp.Items) != 1 || listResp.Items[0].Name != "guide.md" {
		t.Fatalf("unexpected list memories: %+v", listResp)
	}

	// DELETE /api/memory
	rec = h.request("DELETE", "/api/memory?id="+m.ID, nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE memory = %d, want 204", rec.Code)
	}

	// GET after delete => 404
	rec = h.request("GET", "/api/memory?id="+m.ID, nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET after delete = %d, want 404", rec.Code)
	}
}

func TestDefaultFileAndProtection(t *testing.T) {
	h := newTestHarness(t, nil)

	// GET /api/default returns global/readme.md
	rec := h.request("GET", "/api/default", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/default = %d: %s", rec.Code, rec.Body.String())
	}
	def := decodeJSON[MemoryResponse](t, rec)
	if def.Context != "global" || def.Name != "readme.md" {
		t.Fatalf("unexpected default memory: %+v", def)
	}

	// DELETE default file => 403 reserved
	rec = h.request("DELETE", "/api/memory?id="+def.ID, nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("DELETE default file = %d, want 403", rec.Code)
	}
	errResp := decodeJSON[ApiErrorResponse](t, rec)
	if errResp.Error.Kind != "reserved" {
		t.Fatalf("expected reserved error, got: %s", errResp.Error.Kind)
	}
}

func TestSearchEndpoint(t *testing.T) {
	h := newTestHarness(t, nil)

	// Seed memories
	_ = h.request("PUT", "/api/memory", map[string]any{
		"context": "global",
		"name":    "auth.md",
		"body":    "Authentication tokens expire every 15 minutes.",
	}, nil)

	// Valid search
	rec := h.request("GET", "/api/search?q=tokens", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/search = %d: %s", rec.Code, rec.Body.String())
	}
	sResp := decodeJSON[SearchResponse](t, rec)
	if len(sResp.Hits) == 0 {
		t.Fatalf("expected search hits for 'tokens', got 0")
	}

	// Empty query => 400 invalid
	rec = h.request("GET", "/api/search?q=", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET /api/search empty q = %d, want 400", rec.Code)
	}
	errResp := decodeJSON[ApiErrorResponse](t, rec)
	if errResp.Error.Kind != "invalid" {
		t.Fatalf("expected invalid error, got: %s", errResp.Error.Kind)
	}

	// no_global without context => 400 invalid
	rec = h.request("GET", "/api/search?q=tokens&no_global=true", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET /api/search no_global without context = %d, want 400", rec.Code)
	}
}

func TestSyncEndpointAndSerialization(t *testing.T) {
	h := newTestHarness(t, nil)

	rec := h.request("POST", "/api/sync", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/sync = %d: %s", rec.Code, rec.Body.String())
	}
	syncResp := decodeJSON[SyncResponse](t, rec)
	if syncResp.Indexed < 0 {
		t.Fatalf("unexpected sync response: %+v", syncResp)
	}
}

func TestStatusEndpointDoesNotStartModel(t *testing.T) {
	h := newTestHarness(t, nil)
	initialEmbedCalls := h.embed.calls

	rec := h.request("GET", "/api/status", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/status = %d: %s", rec.Code, rec.Body.String())
	}

	if h.embed.calls != initialEmbedCalls {
		t.Fatalf("status endpoint called Embed (%d calls, was %d)", h.embed.calls, initialEmbedCalls)
	}

	st := decodeJSON[ServerStatusResponse](t, rec)
	if st.Version != "0.1.0" || st.Index.Docs < 1 || st.Models.Embed.State != "ready" {
		t.Fatalf("unexpected status: %+v", st)
	}
}

func TestSecurityHostAndOriginMatrix(t *testing.T) {
	h := newTestHarness(t, func(o *Options) {
		o.AllowedHosts = []string{"custom.local:7777"}
	})

	// 1. Allowed host: 127.0.0.1:7777
	rec := h.request("GET", "/api/status", nil, func(r *http.Request) {
		r.Host = "127.0.0.1:7777"
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("allowed host failed: %d", rec.Code)
	}

	// 2. Allowed host: custom.local:7777
	rec = h.request("GET", "/api/status", nil, func(r *http.Request) {
		r.Host = "custom.local:7777"
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("custom allowed host failed: %d", rec.Code)
	}

	// 3. Disallowed host: evil.com
	rec = h.request("GET", "/api/status", nil, func(r *http.Request) {
		r.Host = "evil.com"
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("evil host = %d, want 403", rec.Code)
	}
	errResp := decodeJSON[ApiErrorResponse](t, rec)
	if errResp.Error.Kind != "invalid" {
		t.Fatalf("expected invalid error kind, got %s", errResp.Error.Kind)
	}

	// 4. State-changing request with same-origin Origin: allowed
	rec = h.request("POST", "/api/contexts", map[string]any{"slug": "orig1"}, func(r *http.Request) {
		r.Host = "127.0.0.1:7777"
		r.Header.Set("Origin", "http://127.0.0.1:7777")
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("same origin POST failed: %d: %s", rec.Code, rec.Body.String())
	}

	// 5. State-changing request with NO Origin header: allowed (e.g. CLI, curl)
	rec = h.request("POST", "/api/contexts", map[string]any{"slug": "orig2"}, func(r *http.Request) {
		r.Host = "127.0.0.1:7777"
		r.Header.Del("Origin")
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("no origin POST failed: %d: %s", rec.Code, rec.Body.String())
	}

	// 6. State-changing request with mismatched Origin: 403 rejected
	rec = h.request("POST", "/api/contexts", map[string]any{"slug": "orig3"}, func(r *http.Request) {
		r.Host = "127.0.0.1:7777"
		r.Header.Set("Origin", "http://attacker.com")
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST = %d, want 403", rec.Code)
	}
	errResp = decodeJSON[ApiErrorResponse](t, rec)
	if errResp.Error.Kind != "invalid" {
		t.Fatalf("expected invalid kind, got %s", errResp.Error.Kind)
	}
}

func TestTunnelHostMatrix(t *testing.T) {
	tunnelHost := "my-tunnel.ngrok-free.app"
	token := "tok-secret-456"

	h := newTestHarness(t, func(o *Options) {
		o.AllowedHosts = []string{tunnelHost}
		o.Token = token
	})

	// 1. Allowed tunnel host with valid token -> 200 OK
	rec := h.request("GET", "/api/status", nil, func(r *http.Request) {
		r.Host = tunnelHost
		r.Header.Set("Authorization", "Bearer "+token)
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("tunnel host with token = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// 2. Allowed tunnel host without token -> 401 Unauthorized
	rec = h.request("GET", "/api/status", nil, func(r *http.Request) {
		r.Host = tunnelHost
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("tunnel host without token = %d, want 401: %s", rec.Code, rec.Body.String())
	}

	// 3. Wrong / unauthorized tunnel host -> 403 Forbidden
	rec = h.request("GET", "/api/status", nil, func(r *http.Request) {
		r.Host = "other-tunnel.ngrok-free.app"
		r.Header.Set("Authorization", "Bearer "+token)
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("wrong tunnel host = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	errResp := decodeJSON[ApiErrorResponse](t, rec)
	if errResp.Error.Kind != "invalid" {
		t.Fatalf("expected invalid kind for wrong host, got %s", errResp.Error.Kind)
	}

	// 4. Same-origin state-changing request through tunnel host -> 201 Created
	rec = h.request("POST", "/api/contexts", map[string]any{"slug": "tunnel-ctx"}, func(r *http.Request) {
		r.Host = tunnelHost
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Origin", "https://"+tunnelHost)
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("same-origin tunnel POST = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	// 5. Cross-origin state-changing request through tunnel host -> 403 Forbidden
	rec = h.request("POST", "/api/contexts", map[string]any{"slug": "evil-ctx"}, func(r *http.Request) {
		r.Host = tunnelHost
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Origin", "https://evil-attacker.com")
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin tunnel POST = %d, want 403: %s", rec.Code, rec.Body.String())
	}
}

func TestBearerTokenAuthentication(t *testing.T) {
	token := "secret123"
	h := newTestHarness(t, func(o *Options) {
		o.Token = token
	})

	// 1. Missing token on /api/* => 401
	rec := h.request("GET", "/api/status", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token = %d, want 401", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") != `Bearer realm="mem"` {
		t.Fatalf("WWW-Authenticate missing or incorrect: %q", rec.Header().Get("WWW-Authenticate"))
	}

	// 2. Wrong token on /api/* => 401
	rec = h.request("GET", "/api/status", nil, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer wrongtoken")
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d, want 401", rec.Code)
	}

	// 3. Right token on /api/* => 200
	rec = h.request("GET", "/api/status", nil, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("right token = %d, want 200", rec.Code)
	}

	// 4. Static asset index.html served WITHOUT token
	rec = h.request("GET", "/", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("SPA index.html should not require token, got %d", rec.Code)
	}
}

func TestSecurityHeadersAndCSP(t *testing.T) {
	h := newTestHarness(t, nil)

	// API headers
	rec := h.request("GET", "/api/status", nil, nil)
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("nosniff missing")
	}
	if rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("referrer policy missing")
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("api Cache-Control != no-store: %q", rec.Header().Get("Cache-Control"))
	}

	// SPA headers and CSP
	rec = h.request("GET", "/", nil, nil)
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "style-src 'self' 'unsafe-inline'") {
		t.Fatalf("unexpected CSP: %q", csp)
	}
	if rec.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("index.html cache control = %q, want no-cache", rec.Header().Get("Cache-Control"))
	}

	// Hashed asset immutable cache
	rec = h.request("GET", "/assets/index-test.js", nil, nil)
	if !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("hashed asset cache control = %q, want immutable", rec.Header().Get("Cache-Control"))
	}
}

func TestSPAFallbackRouting(t *testing.T) {
	h := newTestHarness(t, nil)

	// Root path serves index.html
	rec := h.request("GET", "/", nil, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Dashboard") {
		t.Fatalf("root path failed: %d, body: %s", rec.Code, rec.Body.String())
	}

	// Client-side deep link route (/c/global/m/someid) serves index.html
	rec = h.request("GET", "/c/global/m/someid", nil, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Dashboard") {
		t.Fatalf("SPA fallback failed: %d, body: %s", rec.Code, rec.Body.String())
	}

	// Real missing asset with extension => 404
	rec = h.request("GET", "/assets/nonexistent.js", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing asset with extension = %d, want 404", rec.Code)
	}

	// Missing API endpoint => 404 JSON
	rec = h.request("GET", "/api/not-an-endpoint", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing API endpoint = %d, want 404", rec.Code)
	}
	errResp := decodeJSON[ApiErrorResponse](t, rec)
	if errResp.Error.Kind != "not_found" {
		t.Fatalf("expected not_found JSON error, got %s", errResp.Error.Kind)
	}
}

func TestGzipCompression(t *testing.T) {
	h := newTestHarness(t, nil)

	rec := h.request("GET", "/assets/index-test.js", nil, func(r *http.Request) {
		r.Header.Set("Accept-Encoding", "gzip")
	})
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected Content-Encoding: gzip, got %q", rec.Header().Get("Content-Encoding"))
	}

	gr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	decompressed, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("read decompressed: %v", err)
	}
	if string(decompressed) != "console.log('test bundle');" {
		t.Fatalf("unexpected decompressed content: %s", string(decompressed))
	}
}

func TestBodySizeLimitAndUnknownFields(t *testing.T) {
	h := newTestHarness(t, nil)

	// Unknown field in JSON => 400 invalid
	rec := h.request("POST", "/api/contexts", `{"slug":"foo","unrecognized_field":"bar"}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d, want 400", rec.Code)
	}
	errResp := decodeJSON[ApiErrorResponse](t, rec)
	if errResp.Error.Kind != "invalid" {
		t.Fatalf("expected invalid error, got %s", errResp.Error.Kind)
	}

	// Body exceeding 1 MiB + overhead limit => 400 invalid
	oversized := strings.Repeat("A", maxBodyOverhead+1000)
	rec = h.request("PUT", "/api/memory", fmt.Sprintf(`{"body":%q}`, oversized), nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized body = %d, want 400", rec.Code)
	}
}

func TestPanicRecovery(t *testing.T) {
	s := &Server{opts: Options{}}

	// Wrap a test handler that panics
	panickingHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("simulated fatal crash")
	})

	recovered := s.panicRecoveryMiddleware(panickingHandler)

	req := httptest.NewRequest("GET", "/api/status", nil)
	rec := httptest.NewRecorder()
	recovered.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("panic recovered code = %d, want 500", rec.Code)
	}
	errResp := decodeJSON[ApiErrorResponse](t, rec)
	if errResp.Error.Kind != "internal" || strings.Contains(rec.Body.String(), "simulated fatal crash") {
		t.Fatalf("leaked internal details or bad kind: %+v", errResp)
	}
}

func TestGracefulShutdown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	h := newTestHarness(t, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- ServeListener(ctx, ln, h.handler)
	}()

	// Verify server responds
	addr := ln.Addr().String()
	res, err := http.Get("http://" + addr + "/api/status")
	if err != nil {
		t.Fatalf("GET status: %v", err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}

	// Trigger shutdown
	cancel()
	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("ServeListener returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown timed out")
	}
}
