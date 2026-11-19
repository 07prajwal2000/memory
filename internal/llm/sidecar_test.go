package llm

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var mockBinaryPath string

func TestMain(m *testing.M) {
	// Build mock binary
	tmpDir, err := os.MkdirTemp("", "mock_server_*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	mockSrc := filepath.Join(tmpDir, "main.go")
	mockCode := `package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"
)

func main() {
	mode := os.Getenv("HELPER_MODE")
	crashFile := os.Getenv("HELPER_CRASH_FILE")

	if mode == "hang" {
		time.Sleep(1 * time.Hour)
	}

	var port int
	for i := 0; i < len(os.Args)-1; i++ {
		if os.Args[i] == "--port" {
			port, _ = strconv.Atoi(os.Args[i+1])
		}
	}
	if port == 0 {
		fmt.Fprintf(os.Stderr, "no --port flag provided\n")
		os.Exit(2)
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen error: %v\n", err)
		os.Exit(3)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{\"status\":\"ok\"}"))
	})
	mux.HandleFunc("/crash", func(w http.ResponseWriter, r *http.Request) {
		if crashFile != "" {
			_ = os.WriteFile(crashFile, []byte("crashed"), 0644)
		}
		os.Exit(42)
	})

	_ = http.Serve(listener, mux)
}
`
	if err := os.WriteFile(mockSrc, []byte(mockCode), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write mock server source: %v\n", err)
		os.Exit(1)
	}

	mockBin := filepath.Join(tmpDir, "mock_server.exe")
	buildCmd := exec.Command("go", "build", "-o", mockBin, mockSrc)
	if out, err := buildCmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to build mock server: %v, out: %s\n", err, string(out))
		os.Exit(1)
	}
	mockBinaryPath = mockBin

	os.Exit(m.Run())
}

func TestSidecar_LazyStartAndClose(t *testing.T) {
	cfg := SidecarConfig{
		Binary:       mockBinaryPath,
		Model:        "test-model",
		StartTimeout: 5 * time.Second,
		IdleTimeout:  10 * time.Second,
	}

	t.Setenv("HELPER_MODE", "normal")

	sidecar := NewEmbedSidecar(cfg)
	defer sidecar.Close()

	// Should not have started yet
	if sidecar.cmd != nil {
		t.Fatal("sidecar should not start before URL() is called")
	}

	ctx := context.Background()
	url, err := sidecar.URL(ctx)
	if err != nil {
		t.Fatalf("sidecar.URL failed: %v", err)
	}

	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Errorf("unexpected url: %s", url)
	}

	// Calling URL again should return the same URL
	url2, err := sidecar.URL(ctx)
	if err != nil {
		t.Fatalf("second sidecar.URL failed: %v", err)
	}
	if url2 != url {
		t.Errorf("expected same URL %s, got %s", url, url2)
	}

	// Close must terminate cleanly
	if err := sidecar.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Subsequent URL call should return ErrClosed
	_, err = sidecar.URL(ctx)
	if err != ErrClosed {
		t.Errorf("expected ErrClosed, got %v", err)
	}

	// Idempotent Close
	if err := sidecar.Close(); err != nil {
		t.Errorf("second Close failed: %v", err)
	}
}

func TestSidecar_IdleTimeout(t *testing.T) {
	cfg := SidecarConfig{
		Binary:       mockBinaryPath,
		Model:        "test-model",
		StartTimeout: 5 * time.Second,
		IdleTimeout:  200 * time.Millisecond,
	}

	t.Setenv("HELPER_MODE", "normal")

	sidecar := NewEmbedSidecar(cfg)
	defer sidecar.Close()

	ctx := context.Background()
	url1, err := sidecar.URL(ctx)
	if err != nil {
		t.Fatalf("first URL failed: %v", err)
	}
	if url1 == "" {
		t.Fatal("expected non-empty URL")
	}

	// Wait for idle timeout to fire (>200ms)
	time.Sleep(400 * time.Millisecond)

	sidecar.mu.Lock()
	alive := sidecar.isAliveLocked()
	sidecar.mu.Unlock()

	if alive {
		t.Error("expected sidecar to be killed after idle timeout")
	}

	// Next URL call should transparently restart
	url2, err := sidecar.URL(ctx)
	if err != nil {
		t.Fatalf("restart URL failed: %v", err)
	}
	if url2 == "" {
		t.Error("expected valid URL on restart")
	}
}

func TestSidecar_CrashRecovery(t *testing.T) {
	tmpDir := t.TempDir()
	crashFile := filepath.Join(tmpDir, "crash.txt")

	cfg := SidecarConfig{
		Binary:       mockBinaryPath,
		Model:        "test-model",
		StartTimeout: 5 * time.Second,
		IdleTimeout:  5 * time.Minute,
	}

	t.Setenv("HELPER_MODE", "normal")
	t.Setenv("HELPER_CRASH_FILE", crashFile)

	sidecar := NewEmbedSidecar(cfg)
	defer sidecar.Close()

	ctx := context.Background()
	url, err := sidecar.URL(ctx)
	if err != nil {
		t.Fatalf("URL failed: %v", err)
	}

	// Trigger crash
	req, _ := http.NewRequest(http.MethodPost, url+"/crash", nil)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	_, _ = client.Do(req)

	// Wait for process to exit
	time.Sleep(200 * time.Millisecond)

	// URL should transparently restart once
	newURL, err := sidecar.URL(ctx)
	if err != nil {
		t.Fatalf("expected transparent restart once, got error: %v", err)
	}
	if newURL == "" {
		t.Fatal("expected new valid URL")
	}
}

func TestSidecar_HealthTimeout(t *testing.T) {
	cfg := SidecarConfig{
		Binary:       mockBinaryPath,
		Model:        "test-model",
		StartTimeout: 300 * time.Millisecond,
		IdleTimeout:  5 * time.Minute,
	}

	t.Setenv("HELPER_MODE", "hang")

	sidecar := NewEmbedSidecar(cfg)
	defer sidecar.Close()

	ctx := context.Background()
	_, err := sidecar.URL(ctx)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "sidecar failed to start within timeout") {
		t.Errorf("unexpected error: %v", err)
	}
}
