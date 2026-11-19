package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"memory/internal/config"
	"memory/internal/engine"
)

const maxBodyOverhead = 1<<20 + 64*1024 // 1 MiB body limit + 64 KiB overhead

// Options configures the server.
type Options struct {
	Version      string
	Token        string
	AllowedHosts []string
	DataDir      string
	Config       config.Config
	StartedAt    time.Time
	Assets       fs.FS
	Logger       *log.Logger
}

type Server struct {
	eng     *engine.Engine
	opts    Options
	syncMu  sync.Mutex
	syncing bool
}

// New constructs an http.Handler for the dashboard and API.
func New(eng *engine.Engine, opts Options) http.Handler {
	if opts.Logger == nil {
		opts.Logger = log.Default()
	}
	if opts.StartedAt.IsZero() {
		opts.StartedAt = time.Now()
	}

	s := &Server{
		eng:  eng,
		opts: opts,
	}

	mux := http.NewServeMux()

	// API endpoints
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/contexts", s.handleListContexts)
	mux.HandleFunc("POST /api/contexts", s.handleCreateContext)
	mux.HandleFunc("GET /api/contexts/{slug}", s.handleGetContext)
	mux.HandleFunc("PATCH /api/contexts/{slug}", s.handlePatchContext)
	mux.HandleFunc("DELETE /api/contexts/{slug}", s.handleDeleteContext)
	mux.HandleFunc("GET /api/contexts/{slug}/memories", s.handleListMemories)
	mux.HandleFunc("GET /api/memory", s.handleGetMemory)
	mux.HandleFunc("PUT /api/memory", s.handlePutMemory)
	mux.HandleFunc("DELETE /api/memory", s.handleDeleteMemory)
	mux.HandleFunc("GET /api/default", s.handleDefaultMemory)
	mux.HandleFunc("GET /api/search", s.handleSearch)
	mux.HandleFunc("POST /api/sync", s.handleSync)

	// Catch-all for unknown /api/ endpoints -> 404 JSON
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeApiError(w, http.StatusNotFound, "not_found", fmt.Sprintf("endpoint %q not found", r.URL.Path), nil)
	})
	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		writeApiError(w, http.StatusNotFound, "not_found", "endpoint \"/api\" not found", nil)
	})

	// Non-API paths: SPA or 404
	mux.HandleFunc("/", s.handleSPA)

	// Apply middleware stack
	handler := http.Handler(mux)
	handler = s.originValidationMiddleware(handler)
	handler = s.bearerAuthMiddleware(handler)
	handler = s.securityHeadersMiddleware(handler)
	handler = s.hostValidationMiddleware(handler)
	handler = s.panicRecoveryMiddleware(handler)

	return handler
}

func (s *Server) panicRecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if s.opts.Logger != nil {
					s.opts.Logger.Printf("server: panic recovered: %v", rec)
				}
				writeApiError(w, http.StatusInternalServerError, "internal", "internal server error", nil)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) hostValidationMiddleware(next http.Handler) http.Handler {
	allowedSet := make(map[string]bool)
	allowedSet["127.0.0.1"] = true
	allowedSet["localhost"] = true
	allowedSet["::1"] = true
	allowedSet["[::1]"] = true

	for _, h := range s.opts.AllowedHosts {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		hostOnly, _, err := net.SplitHostPort(h)
		if err == nil {
			allowedSet[strings.ToLower(hostOnly)] = true
		}
		allowedSet[strings.ToLower(h)] = true
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		hostOnly, _, err := net.SplitHostPort(host)
		if err != nil {
			hostOnly = host
		}
		hostLower := strings.ToLower(hostOnly)
		if !allowedSet[hostLower] && !allowedSet[strings.ToLower(host)] {
			writeApiError(w, http.StatusForbidden, "invalid", fmt.Sprintf("forbidden host %q: requests must specify an allowed host", r.Host), nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func sameOriginHost(originHost, reqHost string) bool {
	if strings.EqualFold(originHost, reqHost) {
		return true
	}
	oHost, oPort, err1 := net.SplitHostPort(originHost)
	if err1 != nil {
		oHost = originHost
		oPort = ""
	}
	rHost, rPort, err2 := net.SplitHostPort(reqHost)
	if err2 != nil {
		rHost = reqHost
		rPort = ""
	}
	if !strings.EqualFold(oHost, rHost) {
		return false
	}
	if oPort == rPort {
		return true
	}
	// Allow standard ports 80/443 when omitted on one side
	if (oPort == "" || oPort == "80" || oPort == "443") && (rPort == "" || rPort == "80" || rPort == "443") {
		return true
	}
	return false
}

func (s *Server) originValidationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			origin := r.Header.Get("Origin")
			if origin != "" {
				u, err := url.Parse(origin)
				if err != nil || !sameOriginHost(u.Host, r.Host) {
					writeApiError(w, http.StatusForbidden, "invalid", fmt.Sprintf("cross-origin request rejected: Origin %q does not match Host %q", origin, r.Host), nil)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) bearerAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.opts.Token != "" && (r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/")) {
			authHeader := r.Header.Get("Authorization")
			var providedToken string
			if strings.HasPrefix(authHeader, "Bearer ") {
				providedToken = strings.TrimPrefix(authHeader, "Bearer ")
			}
			if subtle.ConstantTimeCompare([]byte(providedToken), []byte(s.opts.Token)) != 1 {
				w.Header().Set("WWW-Authenticate", `Bearer realm="mem"`)
				writeApiError(w, http.StatusUnauthorized, "reserved", "unauthorized: valid token required", nil)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")

		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api" {
			w.Header().Set("Cache-Control", "no-store")
		} else {
			// CSP for SPA allowing same-origin scripts/styles/fonts/images plus Mantine runtime styles
			w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self' data:; img-src 'self' data: blob:; connect-src 'self'")
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	if s.opts.Assets == nil {
		writeApiError(w, http.StatusNotFound, "not_found", "not found", nil)
		return
	}

	cleanPath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if cleanPath == "" || cleanPath == "." {
		cleanPath = "index.html"
	}

	// Try reading exact file
	data, err := fs.ReadFile(s.opts.Assets, cleanPath)
	if err == nil {
		s.serveAsset(w, r, cleanPath, data)
		return
	}

	// If missing, check if it had a file extension -> 404
	if path.Ext(cleanPath) != "" {
		http.NotFound(w, r)
		return
	}

	// SPA fallback: serve index.html for client-side routes (e.g. /c/global/m/id)
	indexData, err := fs.ReadFile(s.opts.Assets, "index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.serveAsset(w, r, "index.html", indexData)
}

func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request, filename string, data []byte) {
	if filename == "index.html" {
		w.Header().Set("Cache-Control", "no-cache")
	} else if strings.HasPrefix(filename, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}

	contentType := mime.TypeByExtension(path.Ext(filename))
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	w.Header().Set("Content-Type", contentType)

	// Gzip compression for compressible assets
	if isCompressible(contentType, filename) && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		_, _ = gw.Write(data)
		_ = gw.Close()

		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Vary", "Accept-Encoding")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf.Bytes())
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func isCompressible(contentType, filename string) bool {
	ext := path.Ext(filename)
	if ext == ".html" || ext == ".js" || ext == ".css" || ext == ".json" || ext == ".svg" || ext == ".txt" || ext == ".map" {
		return true
	}
	return strings.HasPrefix(contentType, "text/") ||
		strings.Contains(contentType, "javascript") ||
		strings.Contains(contentType, "json") ||
		strings.Contains(contentType, "xml")
}

// ServeListener serves HTTP requests on an existing listener until ctx is cancelled.
func ServeListener(ctx context.Context, ln net.Listener, handler http.Handler) error {
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// Serve starts a listener on addr and serves until ctx is cancelled.
func Serve(ctx context.Context, addr string, handler http.Handler) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer ln.Close()
	return ServeListener(ctx, ln, handler)
}
