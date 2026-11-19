package llm

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ErrClosed       = errors.New("sidecar is closed")
	ErrStartTimeout = errors.New("sidecar failed to start within timeout")
)

const (
	DefaultIdleTimeout  = 5 * time.Minute
	DefaultStartTimeout = 60 * time.Second
)

type SidecarConfig struct {
	Binary       string        // paths
	Model        string        // paths
	Args         []string      // extra llama-server args (mode flags are added by the constructor)
	Parallel     int           // number of parallel request slots (llama-server --parallel)
	IdleTimeout  time.Duration // default 5m; 0 = never kill
	StartTimeout time.Duration // default 60s
	LogFile      string        // llama-server stdout/stderr appended here
	NoIdleKill   bool          // set true if IdleTimeout was explicitly 0 / never kill
}

type ringBuffer struct {
	mu    sync.Mutex
	lines []string
	max   int
}

func newRingBuffer(max int) *ringBuffer {
	return &ringBuffer{max: max}
}

func (r *ringBuffer) Write(p []byte) (n int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sc := bufio.NewScanner(bytes.NewReader(p))
	for sc.Scan() {
		line := sc.Text()
		if len(r.lines) >= r.max {
			r.lines = r.lines[1:]
		}
		r.lines = append(r.lines, line)
	}
	return len(p), nil
}

func (r *ringBuffer) LastLines() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.lines, "\n")
}

type Sidecar struct {
	cfg           SidecarConfig
	modeArgs      []string
	mu            sync.Mutex
	cmd           *exec.Cmd
	exitChan      chan struct{}
	port          int
	baseURL       string
	idleTimer     *time.Timer
	closed        bool
	restartedOnce bool
	logBuffer     *ringBuffer
	logFileHandle *os.File
}

func NewEmbedSidecar(cfg SidecarConfig) *Sidecar {
	// Mode flags: --embedding -c 512 -t 8 -b 2048 -ub 512 --flash-attn on
	modeArgs := []string{"--embedding", "-c", "512", "-t", "8", "-b", "2048", "-ub", "512", "--flash-attn", "on"}
	return newSidecar(cfg, modeArgs)
}

func NewRerankSidecar(cfg SidecarConfig) *Sidecar {
	// Mode flags: --rerank -c 8192 -b 2048 -ub 512
	modeArgs := []string{"--rerank", "-c", "8192", "-b", "2048", "-ub", "512"}
	return newSidecar(cfg, modeArgs)
}

func newSidecar(cfg SidecarConfig, modeArgs []string) *Sidecar {
	if cfg.StartTimeout <= 0 {
		cfg.StartTimeout = DefaultStartTimeout
	}
	if cfg.IdleTimeout == 0 && !cfg.NoIdleKill {
		cfg.IdleTimeout = DefaultIdleTimeout
	} else if cfg.IdleTimeout < 0 {
		cfg.IdleTimeout = 0
	}
	return &Sidecar{
		cfg:       cfg,
		modeArgs:  modeArgs,
		logBuffer: newRingBuffer(50),
	}
}

func findFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("sidecar: listen 127.0.0.1:0: %w", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func (s *Sidecar) isAliveLocked() bool {
	if s.cmd == nil || s.cmd.Process == nil || s.exitChan == nil {
		return false
	}
	select {
	case <-s.exitChan:
		return false
	default:
		return true
	}
}

func (s *Sidecar) URL(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return "", ErrClosed
	}

	if s.cmd != nil && s.cmd.Process != nil {
		if s.isAliveLocked() {
			s.resetIdleTimerLocked()
			return s.baseURL, nil
		}
		// Process died mid-session!
		_ = s.cleanupProcessLocked()
		if !s.restartedOnce {
			s.restartedOnce = true
			if err := s.startLocked(ctx); err != nil {
				return "", fmt.Errorf("sidecar restarted once but failed: %w", err)
			}
			s.resetIdleTimerLocked()
			return s.baseURL, nil
		}
		return "", fmt.Errorf("sidecar died unexpectedly and restart limit exceeded: %s", s.logBuffer.LastLines())
	}

	if err := s.startLocked(ctx); err != nil {
		return "", err
	}
	s.resetIdleTimerLocked()
	return s.baseURL, nil
}

func (s *Sidecar) startLocked(ctx context.Context) error {
	port, err := findFreePort()
	if err != nil {
		return err
	}

	args := append([]string{
		"-m", s.cfg.Model,
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(port),
	}, s.modeArgs...)
	if s.cfg.Parallel > 1 {
		args = append(args, "--parallel", strconv.Itoa(s.cfg.Parallel))
	}
	args = append(args, s.cfg.Args...)

	cmd := exec.Command(s.cfg.Binary, args...)
	setHideWindow(cmd)

	var writers []io.Writer
	writers = append(writers, s.logBuffer)

	if s.cfg.LogFile != "" {
		f, err := os.OpenFile(s.cfg.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("sidecar open log file: %w", err)
		}
		s.logFileHandle = f
		writers = append(writers, f)
	}

	mw := io.MultiWriter(writers...)
	cmd.Stdout = mw
	cmd.Stderr = mw

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("sidecar start %s: %w", s.cfg.Binary, err)
	}

	exitChan := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exitChan)
	}()

	s.cmd = cmd
	s.exitChan = exitChan
	s.port = port
	s.baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)

	// Wait for readiness via /health
	startTimeout := s.cfg.StartTimeout
	deadline := time.Now().Add(startTimeout)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	healthURL := fmt.Sprintf("%s/health", s.baseURL)

	for {
		if ctx.Err() != nil {
			_ = s.cleanupProcessLocked()
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			_ = s.cleanupProcessLocked()
			return fmt.Errorf("%w: waited %v, log:\n%s", ErrStartTimeout, startTimeout, s.logBuffer.LastLines())
		}
		if !s.isAliveLocked() {
			_ = s.cleanupProcessLocked()
			return fmt.Errorf("sidecar exited early with code %v, log:\n%s", cmd.ProcessState, s.logBuffer.LastLines())
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
		if err == nil {
			resp, err := client.Do(req)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
			}
		}

		time.Sleep(100 * time.Millisecond)
	}
}

func (s *Sidecar) resetIdleTimerLocked() {
	if s.cfg.IdleTimeout <= 0 {
		return
	}
	if s.idleTimer != nil {
		s.idleTimer.Stop()
	}
	s.idleTimer = time.AfterFunc(s.cfg.IdleTimeout, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closed || s.cmd == nil {
			return
		}
		_ = s.cleanupProcessLocked()
	})
}

func (s *Sidecar) cleanupProcessLocked() error {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		if s.exitChan != nil {
			<-s.exitChan
		}
		s.cmd = nil
		s.exitChan = nil
	}
	if s.logFileHandle != nil {
		_ = s.logFileHandle.Close()
		s.logFileHandle = nil
	}
	s.baseURL = ""
	s.port = 0
	return nil
}

func (s *Sidecar) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true

	if s.idleTimer != nil {
		s.idleTimer.Stop()
		s.idleTimer = nil
	}
	return s.cleanupProcessLocked()
}

type SidecarEmbedder struct {
	Sidecar *Sidecar
	Client  HTTPEmbedder
}

func (s *Sidecar) AsEmbedder(opts ...func(*HTTPEmbedder)) *SidecarEmbedder {
	c := HTTPEmbedder{
		Model:       s.cfg.Model,
		QueryPrefix: DefaultQueryPrefix,
		BatchSize:   DefaultBatchSize,
		Dim:         DefaultDim,
	}
	for _, opt := range opts {
		opt(&c)
	}
	return &SidecarEmbedder{Sidecar: s, Client: c}
}

func (se *SidecarEmbedder) Embed(ctx context.Context, kind Kind, texts []string) ([][]float32, error) {
	baseURL, err := se.Sidecar.URL(ctx)
	if err != nil {
		return nil, fmt.Errorf("sidecar embed url: %w", err)
	}
	client := se.Client
	client.BaseURL = baseURL
	return client.Embed(ctx, kind, texts)
}

type SidecarReranker struct {
	Sidecar *Sidecar
	Client  HTTPReranker
}

func (s *Sidecar) AsReranker(opts ...func(*HTTPReranker)) *SidecarReranker {
	c := HTTPReranker{
		Model:     s.cfg.Model,
		MaxDocs:   DefaultMaxRerankDocs,
		MaxDocLen: DefaultMaxDocLen,
	}
	for _, opt := range opts {
		opt(&c)
	}
	return &SidecarReranker{Sidecar: s, Client: c}
}

func (sr *SidecarReranker) Rerank(ctx context.Context, query string, docs []string) ([]float64, error) {
	baseURL, err := sr.Sidecar.URL(ctx)
	if err != nil {
		return nil, fmt.Errorf("sidecar rerank url: %w", err)
	}
	client := sr.Client
	client.BaseURL = baseURL
	return client.Rerank(ctx, query, docs)
}
