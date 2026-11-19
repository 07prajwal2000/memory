package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// ServerOptions configures llama-server invocation.
type ServerOptions struct {
	BinaryPath  string
	ModelPath   string
	Port        int
	Embedding   bool
	Rerank      bool
	ContextSize int
	BatchSize   int
	UbatchSize  int
	Parallel    int
	Threads     int
	Pooling     string // "cls", "mean", "none", or "" (auto)
}

// LlamaServer controls a background llama-server process.
type LlamaServer struct {
	opts   ServerOptions
	cmd    *exec.Cmd
	client *http.Client
	port   int
}

// StartLlamaServer starts a llama-server process and waits until it is healthy.
func StartLlamaServer(opts ServerOptions) (*LlamaServer, error) {
	if opts.BinaryPath == "" {
		opts.BinaryPath = "spike/bin/llama-server.exe"
	}
	if opts.Port <= 0 {
		opts.Port = 8089
	}
	if opts.ContextSize <= 0 {
		opts.ContextSize = 2048
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 2048
	}
	if opts.UbatchSize <= 0 {
		opts.UbatchSize = 2048
	}
	if opts.Parallel <= 0 {
		opts.Parallel = 2
	}
	if opts.Threads <= 0 {
		opts.Threads = 8
	}

	args := []string{
		"-m", opts.ModelPath,
		"-c", fmt.Sprintf("%d", opts.ContextSize),
		"-b", fmt.Sprintf("%d", opts.BatchSize),
		"-ub", fmt.Sprintf("%d", opts.UbatchSize),
		"--parallel", fmt.Sprintf("%d", opts.Parallel),
		"-t", fmt.Sprintf("%d", opts.Threads),
		"--port", fmt.Sprintf("%d", opts.Port),
	}

	if opts.Embedding {
		args = append(args, "--embedding")
	}
	if opts.Rerank {
		args = append(args, "--rerank")
	}
	if opts.Pooling != "" {
		args = append(args, "--pooling", opts.Pooling)
	}

	cmd := exec.Command(opts.BinaryPath, args...)
	// Set working directory to the directory of the binary so DLLs load properly
	absBin, err := filepath.Abs(opts.BinaryPath)
	if err == nil {
		cmd.Dir = filepath.Dir(absBin)
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start llama-server: %w", err)
	}

	srv := &LlamaServer{
		opts: opts,
		cmd:  cmd,
		client: &http.Client{
			Timeout: 180 * time.Second,
		},
		port: opts.Port,
	}

	// Poll /health
	healthURL := fmt.Sprintf("http://127.0.0.1:%d/health", opts.Port)
	healthy := false
	start := time.Now()
	for time.Since(start) < 45*time.Second {
		// Check if process died
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return nil, fmt.Errorf("llama-server exited prematurely (code %d): %s\n%s",
				cmd.ProcessState.ExitCode(), stdoutBuf.String(), stderrBuf.String())
		}

		resp, err := http.Get(healthURL)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				healthy = true
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}

	if !healthy {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("llama-server health check timed out. Output:\n%s\n%s",
			stdoutBuf.String(), stderrBuf.String())
	}

	return srv, nil
}

// Stop shuts down the llama-server.
func (s *LlamaServer) Stop() {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", s.cmd.Process.Pid)).Run()
		_ = s.cmd.Process.Kill()
		_ = s.cmd.Wait()
		healthURL := fmt.Sprintf("http://127.0.0.1:%d/health", s.port)
		for i := 0; i < 20; i++ {
			resp, err := http.Get(healthURL)
			if err != nil {
				break
			}
			resp.Body.Close()
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// EmbeddingRequest matches OpenAI-compatible format.
type EmbeddingRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model,omitempty"`
}

// EmbeddingResponse matches OpenAI-compatible format.
type EmbeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

// EmbedBatch embeds a slice of texts in a single HTTP request to /v1/embeddings.
func (s *LlamaServer) EmbedBatch(texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	reqBody, err := json.Marshal(EmbeddingRequest{Input: texts})
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/v1/embeddings", s.port)
	var resp *http.Response
	for attempt := 0; attempt < 2; attempt++ {
		resp, err = s.client.Post(url, "application/json", bytes.NewReader(reqBody))
		if err == nil && resp.StatusCode == http.StatusOK {
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		if attempt == 0 {
			time.Sleep(500 * time.Millisecond)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("POST /v1/embeddings: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("POST /v1/embeddings returned %d: %s", resp.StatusCode, string(body))
	}

	var embResp EmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&embResp); err != nil {
		return nil, fmt.Errorf("decode embeddings: %w", err)
	}

	if len(embResp.Data) != len(texts) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(texts), len(embResp.Data))
	}

	// Preserve order by Index
	out := make([][]float32, len(texts))
	for _, item := range embResp.Data {
		if item.Index >= 0 && item.Index < len(texts) {
			out[item.Index] = item.Embedding
		}
	}

	return out, nil
}

// EmbedConcurrent embeds all texts in batches of batchSize using numWorkers concurrent goroutines.
func (s *LlamaServer) EmbedConcurrent(texts []string, batchSize, numWorkers int) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if batchSize <= 0 {
		batchSize = 16
	}
	if numWorkers <= 0 {
		numWorkers = 2
	}

	results := make([][]float32, len(texts))

	type batchTask struct {
		startIdx int
		batch    []string
	}

	taskChan := make(chan batchTask, (len(texts)+batchSize-1)/batchSize)
	errChan := make(chan error, numWorkers)
	var wg sync.WaitGroup

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range taskChan {
				embs, err := s.EmbedBatch(task.batch)
				if err != nil {
					select {
					case errChan <- err:
					default:
					}
					return
				}
				for i, emb := range embs {
					results[task.startIdx+i] = emb
				}
			}
		}()
	}

	for i := 0; i < len(texts); i += batchSize {
		end := i + batchSize
		if end > len(texts) {
			end = len(texts)
		}
		taskChan <- batchTask{
			startIdx: i,
			batch:    texts[i:end],
		}
	}
	close(taskChan)

	wg.Wait()
	close(errChan)

	if err, ok := <-errChan; ok {
		return nil, err
	}

	return results, nil
}

// MeasureLatency measures single-text query latency in milliseconds.
func (s *LlamaServer) MeasureLatency(query string, runs int) (float64, error) {
	if runs <= 0 {
		runs = 5
	}
	var total time.Duration
	// 1 warmup run
	_, err := s.EmbedBatch([]string{query})
	if err != nil {
		return 0, err
	}

	for i := 0; i < runs; i++ {
		start := time.Now()
		_, err := s.EmbedBatch([]string{query})
		if err != nil {
			return 0, err
		}
		total += time.Since(start)
	}

	avgMs := float64(total.Milliseconds()) / float64(runs)
	return avgMs, nil
}

// MeasureThroughput measures texts/second with 200-token texts, batch 16, concurrency 2.
func (s *LlamaServer) MeasureThroughput(sampleTexts []string, batchSize, concurrency int) (float64, error) {
	if len(sampleTexts) == 0 {
		return 0, fmt.Errorf("no sample texts")
	}
	if batchSize <= 0 {
		batchSize = 16
	}
	if concurrency <= 0 {
		concurrency = 2
	}

	// Warmup
	_, err := s.EmbedBatch(sampleTexts[:min(batchSize, len(sampleTexts))])
	if err != nil {
		return 0, err
	}

	start := time.Now()
	_, err = s.EmbedConcurrent(sampleTexts, batchSize, concurrency)
	if err != nil {
		return 0, err
	}
	elapsed := time.Since(start).Seconds()
	if elapsed <= 0 {
		elapsed = 0.001
	}

	return float64(len(sampleTexts)) / elapsed, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
