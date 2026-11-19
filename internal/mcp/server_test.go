package mcp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"memory/internal/config"
	"memory/internal/engine"
	"memory/internal/llm"
	"memory/internal/search"
	"memory/internal/store"
)

var updateGolden = flag.Bool("update", false, "update golden files")

type fakeEmbedder struct {
	mu   sync.Mutex
	fail bool
}

func (f *fakeEmbedder) setFail(fail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = fail
}

func (f *fakeEmbedder) Embed(ctx context.Context, kind llm.Kind, texts []string) ([][]float32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return nil, errors.New("fake embedder failure")
	}
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

func newTestEngine(t *testing.T, embed *fakeEmbedder, opts ...engine.Option) *engine.Engine {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Config{
		DataDir:               dir,
		ModelsDir:             filepath.Join(dir, "models"),
		Port:                  7777,
		DefaultTempExpiryDays: 7,
	}
	if embed == nil {
		embed = &fakeEmbedder{}
	}
	allOpts := []engine.Option{
		engine.WithEmbedder(embed),
		engine.WithLogger(log.New(io.Discard, "", 0)),
	}
	allOpts = append(allOpts, opts...)
	eng, err := engine.Open(cfg, allOpts...)
	if err != nil {
		t.Fatalf("open test engine: %v", err)
	}
	t.Cleanup(func() {
		eng.Close()
	})
	return eng
}

type testClient struct {
	clientOut *io.PipeWriter
	clientIn  *bufio.Scanner
	serveErr  chan error
	nextID    int
	mu        sync.Mutex
	t         *testing.T
}

func newTestClient(t *testing.T, eng *engine.Engine, opts ...Option) *testClient {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()

	scanner := bufio.NewScanner(outR)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, maxScanBufferSize)

	serveErr := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go func() {
		err := Serve(ctx, inR, outW, eng, opts...)
		serveErr <- err
		_ = outW.Close()
	}()

	return &testClient{
		clientOut: inW,
		clientIn:  scanner,
		serveErr:  serveErr,
		nextID:    1,
		t:         t,
	}
}

func (c *testClient) call(method string, params any) (Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	id := c.nextID
	c.nextID++

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		req["params"] = params
	}

	data, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	data = append(data, '\n')
	if _, err := c.clientOut.Write(data); err != nil {
		return Response{}, err
	}

	if !c.clientIn.Scan() {
		if err := c.clientIn.Err(); err != nil {
			return Response{}, err
		}
		return Response{}, io.EOF
	}

	var resp Response
	if err := json.Unmarshal(c.clientIn.Bytes(), &resp); err != nil {
		return Response{}, fmt.Errorf("decode response %q: %w", string(c.clientIn.Bytes()), err)
	}
	return resp, nil
}

func (c *testClient) notify(method string, params any) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	req := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		req["params"] = params
	}

	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = c.clientOut.Write(data)
	return err
}

func (c *testClient) closeInput() {
	_ = c.clientOut.Close()
}

func TestProtocol_Initialize_Versions_And_Timing(t *testing.T) {
	eng := newTestEngine(t, nil)

	versions := []struct {
		requested string
		expected  string
	}{
		{"2024-11-05", "2024-11-05"},
		{"2025-03-26", "2025-03-26"},
		{"2025-06-18", "2025-06-18"},
		{"2029-01-01", "2025-06-18"}, // unsupported -> newest
		{"", "2025-06-18"},
	}

	for _, tc := range versions {
		t.Run("ver_"+tc.requested, func(t *testing.T) {
			client := newTestClient(t, eng, WithServerVersion("1.2.3"))
			defer client.closeInput()

			start := time.Now()
			resp, err := client.call("initialize", InitializeParams{
				ProtocolVersion: tc.requested,
				ClientInfo: ClientInfo{
					Name:    "test-runner",
					Version: "0.0.1",
				},
			})
			elapsed := time.Since(start)

			if err != nil {
				t.Fatalf("initialize call failed: %v", err)
			}
			if resp.Error != nil {
				t.Fatalf("unexpected error: %v", resp.Error)
			}

			// Verify initialize responds in < 300 ms
			if elapsed > 300*time.Millisecond {
				t.Errorf("initialize took %v, must be < 300ms", elapsed)
			}
			t.Logf("initialize with %q took %v", tc.requested, elapsed)

			var initRes InitializeResult
			raw, _ := json.Marshal(resp.Result)
			if err := json.Unmarshal(raw, &initRes); err != nil {
				t.Fatalf("unmarshal init result: %v", err)
			}

			if initRes.ProtocolVersion != tc.expected {
				t.Errorf("expected protocol version %q, got %q", tc.expected, initRes.ProtocolVersion)
			}
			if initRes.ServerInfo.Name != "mem" {
				t.Errorf("expected server name 'mem', got %q", initRes.ServerInfo.Name)
			}
			if initRes.ServerInfo.Version != "1.2.3" {
				t.Errorf("expected server version '1.2.3', got %q", initRes.ServerInfo.Version)
			}
			if !strings.Contains(initRes.Instructions, "Persistent memory shared across your sessions") {
				t.Errorf("instructions missing expected header: %q", initRes.Instructions)
			}
		})
	}
}

func TestProtocol_Instructions(t *testing.T) {
	t.Run("content", func(t *testing.T) {
		eng := newTestEngine(t, nil)
		client := newTestClient(t, eng)
		defer client.closeInput()

		resp, err := client.call("initialize", InitializeParams{
			ProtocolVersion: "2025-06-18",
		})
		if err != nil {
			t.Fatalf("call initialize: %v", err)
		}
		var res InitializeResult
		raw, _ := json.Marshal(resp.Result)
		_ = json.Unmarshal(raw, &res)

		lines := strings.Split(res.Instructions, "\n")
		if len(lines) < 2 {
			t.Fatalf("expected at least 2 lines in instructions")
		}
		if lines[0] != "Persistent memory shared across your sessions, stored locally." {
			t.Errorf("unexpected line 0: %q", lines[0])
		}
		if lines[1] != "Tools: search, get, upsert, delete. The text below is the user's own global instructions file; follow it." {
			t.Errorf("unexpected line 1: %q", lines[1])
		}
		if !strings.Contains(res.Instructions, "# Memory") {
			t.Errorf("instructions missing default readme body")
		}
	})

	t.Run("truncation", func(t *testing.T) {
		eng := newTestEngine(t, nil)
		// Update default file with large content > 16 KiB
		largeBody := strings.Repeat("Line of instructions to expand the file content.\n", 500)
		_, err := eng.Store.Upsert(store.UpsertInput{
			Context: store.GlobalContext,
			Name:    store.DefaultFileName,
			Body:    largeBody,
		})
		if err != nil {
			t.Fatalf("upsert large readme: %v", err)
		}

		client := newTestClient(t, eng)
		defer client.closeInput()

		resp, err := client.call("initialize", InitializeParams{ProtocolVersion: "2025-06-18"})
		if err != nil {
			t.Fatalf("call initialize: %v", err)
		}
		var res InitializeResult
		raw, _ := json.Marshal(resp.Result)
		_ = json.Unmarshal(raw, &res)

		if len(res.Instructions) > 16*1024 {
			t.Errorf("instructions size %d > 16 KiB", len(res.Instructions))
		}
		if !strings.HasSuffix(res.Instructions, "…truncated; call `get` with no arguments for the full file.") {
			t.Errorf("missing truncation suffix: %q", res.Instructions[len(res.Instructions)-80:])
		}
	})

	t.Run("fallback_when_default_file_fails", func(t *testing.T) {
		// Test FormatInstructions with nil engine
		instr := FormatInstructions(nil)
		if instr != InstructionHeader {
			t.Errorf("expected fallback header only, got %q", instr)
		}
	})
}

func TestProtocol_ToolsList_Golden(t *testing.T) {
	eng := newTestEngine(t, nil)
	client := newTestClient(t, eng)
	defer client.closeInput()

	resp, err := client.call("tools/list", nil)
	if err != nil {
		t.Fatalf("tools/list call failed: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(resp.Result); err != nil {
		t.Fatalf("encode tools list: %v", err)
	}
	rawResult := bytes.TrimSpace(buf.Bytes())
	goldenPath := filepath.Join("testdata", "tools.golden.json")

	if *updateGolden {
		if err := os.WriteFile(goldenPath, append(rawResult, '\n'), 0o644); err != nil {
			t.Fatalf("failed to update golden file: %v", err)
		}
		t.Logf("updated %s", goldenPath)
	}

	goldenData, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden file %s: %v (run go test -update ./internal/mcp to create)", goldenPath, err)
	}

	goldenStr := strings.TrimSpace(string(goldenData))
	gotStr := strings.TrimSpace(string(rawResult))

	if goldenStr != gotStr {
		t.Errorf("tools/list does not match golden file %s.\nGot:\n%s\nWant:\n%s", goldenPath, gotStr, goldenStr)
	}
}

func TestProtocol_Ping(t *testing.T) {
	eng := newTestEngine(t, nil)
	client := newTestClient(t, eng)
	defer client.closeInput()

	resp, err := client.call("ping", nil)
	if err != nil {
		t.Fatalf("ping call failed: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("ping returned error: %v", resp.Error)
	}
}

func TestProtocol_UnknownMethod(t *testing.T) {
	eng := newTestEngine(t, nil)
	client := newTestClient(t, eng)
	defer client.closeInput()

	resp, err := client.call("nonexistent/method", nil)
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if resp.Error == nil {
		t.Fatalf("expected error for unknown method, got nil")
	}
	if resp.Error.Code != CodeMethodNotFound {
		t.Errorf("expected code %d, got %d", CodeMethodNotFound, resp.Error.Code)
	}
}

func TestProtocol_MalformedJSONLine(t *testing.T) {
	eng := newTestEngine(t, nil)
	client := newTestClient(t, eng)
	defer client.closeInput()

	// Send broken JSON
	_, err := client.clientOut.Write([]byte("{broken json syntax\n"))
	if err != nil {
		t.Fatalf("write broken line: %v", err)
	}

	if !client.clientIn.Scan() {
		t.Fatalf("expected error response from server, scan returned false")
	}

	var resp Response
	if err := json.Unmarshal(client.clientIn.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse server error response: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != CodeParseError {
		t.Fatalf("expected CodeParseError (-32700), got %v", resp.Error)
	}

	// Verify server is STILL alive
	pingResp, err := client.call("ping", nil)
	if err != nil {
		t.Fatalf("ping after parse error failed: %v", err)
	}
	if pingResp.Error != nil {
		t.Fatalf("ping error: %v", pingResp.Error)
	}
}

func TestProtocol_CancelNotification(t *testing.T) {
	eng := newTestEngine(t, nil)
	client := newTestClient(t, eng)
	defer client.closeInput()

	// Test notifications/cancelled does not error or produce unexpected output
	err := client.notify("notifications/cancelled", CancelledParams{
		RequestID: 999,
		Reason:    "client aborted",
	})
	if err != nil {
		t.Fatalf("send cancelled notification: %v", err)
	}

	// Ping to verify server continues operating normally
	resp, err := client.call("ping", nil)
	if err != nil || resp.Error != nil {
		t.Fatalf("ping after cancel notification failed: %v, resp: %v", err, resp)
	}
}

func TestProtocol_CleanEOFExit(t *testing.T) {
	eng := newTestEngine(t, nil)
	client := newTestClient(t, eng)

	// Close client out (EOF to server)
	client.closeInput()

	select {
	case err := <-client.serveErr:
		if err != nil {
			t.Errorf("expected nil error on stdin EOF, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Errorf("server did not exit within 2 seconds of stdin EOF")
	}
}

func TestProtocol_PanicRecovery(t *testing.T) {
	eng := newTestEngine(t, nil)
	client := newTestClient(t, eng)
	defer client.closeInput()

	// Call invalid tools/call that causes unknown tool (safe fallback)
	resp, err := client.call("tools/call", map[string]any{
		"name": "nonexistent_tool",
	})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	raw, _ := json.Marshal(resp.Result)
	var toolRes CallToolResult
	_ = json.Unmarshal(raw, &toolRes)

	if !toolRes.IsError {
		t.Errorf("expected isError: true for unknown tool")
	}

	// Verify server stays alive with ping
	pingResp, err := client.call("ping", nil)
	if err != nil || pingResp.Error != nil {
		t.Fatalf("ping after tool error failed: %v", err)
	}
}

func TestProtocol_NoStrayStdoutWrites(t *testing.T) {
	eng := newTestEngine(t, nil)

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()

	scanner := bufio.NewScanner(outR)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, maxScanBufferSize)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- Serve(ctx, inR, outW, eng)
	}()

	// Send initialize, ping, tools/list, get
	messages := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"get","arguments":{}}}`,
	}

	for _, msg := range messages {
		_, _ = inW.Write([]byte(msg + "\n"))
		if !scanner.Scan() {
			t.Fatalf("expected response line")
		}
		line := scanner.Bytes()
		// Validate line is strictly valid JSON-RPC
		var r Response
		if err := json.Unmarshal(line, &r); err != nil {
			t.Fatalf("stray non-JSON written to stdout: %q: %v", string(line), err)
		}
		if r.JSONRPC != "2.0" {
			t.Fatalf("invalid jsonrpc field: %q", r.JSONRPC)
		}
	}

	_ = inW.Close()
	select {
	case err := <-serveErr:
		if err != nil {
			t.Errorf("serve err: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Errorf("timeout waiting for serve exit")
	}
}

func TestProtocol_ConcurrentCalls(t *testing.T) {
	eng := newTestEngine(t, nil)

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()

	scanner := bufio.NewScanner(outR)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, maxScanBufferSize)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = Serve(ctx, inR, outW, eng)
	}()

	const numCalls = 20
	var writeMu sync.Mutex

	// Goroutine reading responses
	responses := make(map[int]Response)
	var respMu sync.Mutex
	done := make(chan struct{})

	go func() {
		for i := 0; i < numCalls; i++ {
			if !scanner.Scan() {
				break
			}
			var resp Response
			if err := json.Unmarshal(scanner.Bytes(), &resp); err == nil {
				var id int
				_ = json.Unmarshal(resp.ID, &id)
				respMu.Lock()
				responses[id] = resp
				respMu.Unlock()
			}
		}
		close(done)
	}()

	var wg sync.WaitGroup
	for i := 1; i <= numCalls; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			req := map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"method":  "ping",
			}
			data, _ := json.Marshal(req)
			writeMu.Lock()
			_, _ = inW.Write(append(data, '\n'))
			writeMu.Unlock()
		}(i)
	}

	wg.Wait()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for concurrent responses")
	}

	respMu.Lock()
	defer respMu.Unlock()
	if len(responses) != numCalls {
		t.Errorf("expected %d responses, got %d", numCalls, len(responses))
	}
	_ = inW.Close()
}

func TestTools_Search(t *testing.T) {
	embed := &fakeEmbedder{}
	eng := newTestEngine(t, embed)
	client := newTestClient(t, eng)
	defer client.closeInput()

	// Seed memories
	_, err := eng.Store.Upsert(store.UpsertInput{
		Context: store.GlobalContext,
		Name:    "jwt-auth.md",
		Body:    "# JWT Authentication\n\nUse RS256 algorithm for signing tokens. Verify bearer headers on incoming requests.",
	})
	if err != nil {
		t.Fatalf("upsert seed: %v", err)
	}

	t.Run("happy_path", func(t *testing.T) {
		resp, err := client.call("tools/call", map[string]any{
			"name": "search",
			"arguments": map[string]any{
				"query": "jwt authentication",
			},
		})
		if err != nil {
			t.Fatalf("call search: %v", err)
		}
		raw, _ := json.Marshal(resp.Result)
		var res CallToolResult
		_ = json.Unmarshal(raw, &res)
		if res.IsError {
			t.Fatalf("search returned error: %s", res.Content[0].Text)
		}
		if !strings.Contains(res.Content[0].Text, "jwt-auth.md") {
			t.Errorf("search result missing hit: %s", res.Content[0].Text)
		}
	})

	t.Run("zero_hits", func(t *testing.T) {
		_, _ = eng.Store.CreateContext("empty-ctx", nil)
		resp, err := client.call("tools/call", map[string]any{
			"name": "search",
			"arguments": map[string]any{
				"query":     "xyz123nonexistentwordxyz",
				"context":   "empty-ctx",
				"no_global": true,
			},
		})
		if err != nil {
			t.Fatalf("call search: %v", err)
		}
		raw, _ := json.Marshal(resp.Result)
		var res CallToolResult
		_ = json.Unmarshal(raw, &res)
		if res.IsError {
			t.Fatalf("search zero hits returned isError: %v", res)
		}
		if !strings.Contains(res.Content[0].Text, "No memories matched your query") {
			t.Errorf("missing zero hits suggestion: %s", res.Content[0].Text)
		}
	})

	t.Run("degraded_notice", func(t *testing.T) {
		embed.setFail(true)
		defer embed.setFail(false)

		resp, err := client.call("tools/call", map[string]any{
			"name": "search",
			"arguments": map[string]any{
				"query": "jwt",
			},
		})
		if err != nil {
			t.Fatalf("call search: %v", err)
		}
		raw, _ := json.Marshal(resp.Result)
		var res CallToolResult
		_ = json.Unmarshal(raw, &res)
		if !strings.Contains(res.Content[0].Text, "note: ") {
			t.Errorf("missing degraded notice: %s", res.Content[0].Text)
		}
	})

	t.Run("no_global_without_context_error", func(t *testing.T) {
		resp, err := client.call("tools/call", map[string]any{
			"name": "search",
			"arguments": map[string]any{
				"query":     "test",
				"no_global": true,
			},
		})
		if err != nil {
			t.Fatalf("call search: %v", err)
		}
		raw, _ := json.Marshal(resp.Result)
		var res CallToolResult
		_ = json.Unmarshal(raw, &res)
		if !res.IsError {
			t.Errorf("expected isError: true when no_global has no context")
		}
		if !strings.Contains(res.Content[0].Text, "no_global cannot be used without specifying a context") {
			t.Errorf("unexpected error text: %s", res.Content[0].Text)
		}
	})

	t.Run("limit_validation", func(t *testing.T) {
		resp, err := client.call("tools/call", map[string]any{
			"name": "search",
			"arguments": map[string]any{
				"query": "test",
				"limit": 100,
			},
		})
		if err != nil {
			t.Fatalf("call search: %v", err)
		}
		raw, _ := json.Marshal(resp.Result)
		var res CallToolResult
		_ = json.Unmarshal(raw, &res)
		if !res.IsError {
			t.Errorf("expected isError: true for limit > 50")
		}
	})

	t.Run("fuzzy_argument_and_marking", func(t *testing.T) {
		embed.setFail(true)
		defer embed.setFail(false)

		// Fuzzy ON (default): typo "tokn" finds "jwt-auth.md"
		respFuzzy, err := client.call("tools/call", map[string]any{
			"name": "search",
			"arguments": map[string]any{
				"query": "tokn",
			},
		})
		if err != nil {
			t.Fatalf("call search fuzzy on: %v", err)
		}
		rawFuzzy, _ := json.Marshal(respFuzzy.Result)
		var resFuzzy CallToolResult
		_ = json.Unmarshal(rawFuzzy, &resFuzzy)
		if resFuzzy.IsError {
			t.Fatalf("search with typo returned error: %s", resFuzzy.Content[0].Text)
		}
		if !strings.Contains(resFuzzy.Content[0].Text, "jwt-auth.md") {
			t.Errorf("expected typo 'tokn' to find jwt-auth.md with fuzzy on; got: %s", resFuzzy.Content[0].Text)
		}
		if !strings.Contains(resFuzzy.Content[0].Text, "[fuzzy]") {
			t.Errorf("expected [fuzzy] mark in hit output; got: %s", resFuzzy.Content[0].Text)
		}

		// Fuzzy OFF: "tokn" should not find "jwt-auth.md"
		respExact, err := client.call("tools/call", map[string]any{
			"name": "search",
			"arguments": map[string]any{
				"query": "tokn",
				"fuzzy": false,
			},
		})
		if err != nil {
			t.Fatalf("call search fuzzy off: %v", err)
		}
		rawExact, _ := json.Marshal(respExact.Result)
		var resExact CallToolResult
		_ = json.Unmarshal(rawExact, &resExact)
		if strings.Contains(resExact.Content[0].Text, "jwt-auth.md") {
			t.Errorf("expected typo 'tokn' with fuzzy:false NOT to find jwt-auth.md; got: %s", resExact.Content[0].Text)
		}
	})
}

func TestTools_Get(t *testing.T) {
	eng := newTestEngine(t, nil)
	client := newTestClient(t, eng)
	defer client.closeInput()

	// Seed context and memory
	_, _ = eng.Store.CreateContext("project", nil)
	mem, err := eng.Store.Upsert(store.UpsertInput{
		Context: "project",
		Name:    "api-spec.md",
		Body:    "# API Spec\n\nREST API definition.",
		Tags:    []string{"api", "v1"},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	t.Run("no_args_shape", func(t *testing.T) {
		resp, err := client.call("tools/call", map[string]any{
			"name":      "get",
			"arguments": map[string]any{},
		})
		if err != nil {
			t.Fatalf("call get: %v", err)
		}
		raw, _ := json.Marshal(resp.Result)
		var res CallToolResult
		_ = json.Unmarshal(raw, &res)
		if res.IsError {
			t.Fatalf("get no-args error: %s", res.Content[0].Text)
		}
		if !strings.Contains(res.Content[0].Text, "Live Contexts") {
			t.Errorf("missing Live Contexts in text: %s", res.Content[0].Text)
		}
		if !strings.Contains(res.Content[0].Text, "project") {
			t.Errorf("missing context 'project' in listing: %s", res.Content[0].Text)
		}
	})

	t.Run("only_context", func(t *testing.T) {
		resp, err := client.call("tools/call", map[string]any{
			"name": "get",
			"arguments": map[string]any{
				"context": "project",
			},
		})
		if err != nil {
			t.Fatalf("call get: %v", err)
		}
		raw, _ := json.Marshal(resp.Result)
		var res CallToolResult
		_ = json.Unmarshal(raw, &res)
		if res.IsError {
			t.Fatalf("get context error: %s", res.Content[0].Text)
		}
		if !strings.Contains(res.Content[0].Text, "api-spec.md") {
			t.Errorf("missing api-spec.md in context list: %s", res.Content[0].Text)
		}
	})

	t.Run("listing_truncation", func(t *testing.T) {
		_, _ = eng.Store.CreateContext("bulk-ctx", nil)
		for i := 0; i < 205; i++ {
			_, err := eng.Store.Upsert(store.UpsertInput{
				Context: "bulk-ctx",
				Name:    fmt.Sprintf("item-%03d.md", i),
				Body:    fmt.Sprintf("Item %d content", i),
			})
			if err != nil {
				t.Fatalf("upsert bulk %d: %v", i, err)
			}
		}

		resp, err := client.call("tools/call", map[string]any{
			"name": "get",
			"arguments": map[string]any{
				"context": "bulk-ctx",
			},
		})
		if err != nil {
			t.Fatalf("call get: %v", err)
		}
		raw, _ := json.Marshal(resp.Result)
		var res CallToolResult
		_ = json.Unmarshal(raw, &res)
		if res.IsError {
			t.Fatalf("get bulk context error: %s", res.Content[0].Text)
		}
		if !strings.Contains(res.Content[0].Text, "truncated at 200 memories") {
			t.Errorf("missing truncation notice in text: %s", res.Content[0].Text)
		}
	})

	t.Run("by_id_and_by_name", func(t *testing.T) {
		// By ID
		respID, err := client.call("tools/call", map[string]any{
			"name": "get",
			"arguments": map[string]any{
				"id": mem.ID,
			},
		})
		if err != nil {
			t.Fatalf("call get id: %v", err)
		}
		var resID CallToolResult
		rawID, _ := json.Marshal(respID.Result)
		_ = json.Unmarshal(rawID, &resID)
		if resID.IsError {
			t.Fatalf("get by id error: %s", resID.Content[0].Text)
		}
		if !strings.Contains(resID.Content[0].Text, "REST API definition.") {
			t.Errorf("missing body in get by id: %s", resID.Content[0].Text)
		}

		// By Name + context
		respName, err := client.call("tools/call", map[string]any{
			"name": "get",
			"arguments": map[string]any{
				"context": "project",
				"name":    "api-spec.md",
			},
		})
		if err != nil {
			t.Fatalf("call get name: %v", err)
		}
		var resName CallToolResult
		rawName, _ := json.Marshal(respName.Result)
		_ = json.Unmarshal(rawName, &resName)
		if resName.IsError {
			t.Fatalf("get by name error: %s", resName.Content[0].Text)
		}
		if !strings.Contains(resName.Content[0].Text, "REST API definition.") {
			t.Errorf("missing body in get by name: %s", resName.Content[0].Text)
		}
	})

	t.Run("id_and_name_together_error", func(t *testing.T) {
		resp, err := client.call("tools/call", map[string]any{
			"name": "get",
			"arguments": map[string]any{
				"id":   mem.ID,
				"name": "api-spec.md",
			},
		})
		if err != nil {
			t.Fatalf("call get: %v", err)
		}
		var res CallToolResult
		raw, _ := json.Marshal(resp.Result)
		_ = json.Unmarshal(raw, &res)
		if !res.IsError {
			t.Errorf("expected isError: true when both id and name provided")
		}
	})

	t.Run("large_body_truncation", func(t *testing.T) {
		bigBody := strings.Repeat("A", 250*1024) // 250 KiB
		bigMem, err := eng.Store.Upsert(store.UpsertInput{
			Context: "project",
			Name:    "big-note.md",
			Body:    bigBody,
		})
		if err != nil {
			t.Fatalf("upsert big note: %v", err)
		}

		resp, err := client.call("tools/call", map[string]any{
			"name": "get",
			"arguments": map[string]any{
				"id": bigMem.ID,
			},
		})
		if err != nil {
			t.Fatalf("call get: %v", err)
		}
		var res CallToolResult
		raw, _ := json.Marshal(resp.Result)
		_ = json.Unmarshal(raw, &res)
		if res.IsError {
			t.Fatalf("get big note error: %s", res.Content[0].Text)
		}
		if !strings.Contains(res.Content[0].Text, "…truncated at 200 KiB") {
			t.Errorf("missing 200 KiB truncation notice: %s", res.Content[0].Text[len(res.Content[0].Text)-80:])
		}
	})
}

func TestTools_Upsert(t *testing.T) {
	eng := newTestEngine(t, nil)
	client := newTestClient(t, eng)
	defer client.closeInput()

	// Initialize with custom client name
	_, err := client.call("initialize", InitializeParams{
		ProtocolVersion: "2025-06-18",
		ClientInfo: ClientInfo{
			Name: "my-custom-client",
		},
	})
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}

	t.Run("create_unnamed", func(t *testing.T) {
		resp, err := client.call("tools/call", map[string]any{
			"name": "upsert",
			"arguments": map[string]any{
				"body": "Unnamed note content.",
			},
		})
		if err != nil {
			t.Fatalf("call upsert: %v", err)
		}
		var res CallToolResult
		raw, _ := json.Marshal(resp.Result)
		_ = json.Unmarshal(raw, &res)
		if res.IsError {
			t.Fatalf("upsert error: %s", res.Content[0].Text)
		}
		if !strings.Contains(res.Content[0].Text, "status: created") {
			t.Errorf("expected created status: %s", res.Content[0].Text)
		}
	})

	t.Run("create_and_update_named", func(t *testing.T) {
		// Create named
		resp, err := client.call("tools/call", map[string]any{
			"name": "upsert",
			"arguments": map[string]any{
				"name": "config/db.md",
				"body": "host: localhost",
			},
		})
		if err != nil {
			t.Fatalf("call upsert: %v", err)
		}
		var res CallToolResult
		raw, _ := json.Marshal(resp.Result)
		_ = json.Unmarshal(raw, &res)
		if res.IsError {
			t.Fatalf("upsert error: %s", res.Content[0].Text)
		}
		if !strings.Contains(res.Content[0].Text, "status: created") {
			t.Errorf("expected status created: %s", res.Content[0].Text)
		}

		// Update named
		resp2, err := client.call("tools/call", map[string]any{
			"name": "upsert",
			"arguments": map[string]any{
				"name": "config/db.md",
				"body": "host: db.example.com",
			},
		})
		if err != nil {
			t.Fatalf("call upsert: %v", err)
		}
		var res2 CallToolResult
		raw2, _ := json.Marshal(resp2.Result)
		_ = json.Unmarshal(raw2, &res2)
		if res2.IsError {
			t.Fatalf("upsert update error: %s", res2.Content[0].Text)
		}
		if !strings.Contains(res2.Content[0].Text, "status: updated") {
			t.Errorf("expected status updated: %s", res2.Content[0].Text)
		}
		if !strings.Contains(res2.Content[0].Text, "version: 2") {
			t.Errorf("expected version 2: %s", res2.Content[0].Text)
		}

		// Check updated_by set from clientInfo
		mem, _ := eng.Store.Get(store.GlobalContext, "config/db.md")
		if mem.UpdatedBy != "my-custom-client" {
			t.Errorf("expected updated_by 'my-custom-client', got %q", mem.UpdatedBy)
		}
	})

	t.Run("append_mode", func(t *testing.T) {
		resp, err := client.call("tools/call", map[string]any{
			"name": "upsert",
			"arguments": map[string]any{
				"name":   "config/db.md",
				"body":   "port: 5432",
				"append": true,
			},
		})
		if err != nil {
			t.Fatalf("call upsert append: %v", err)
		}
		var res CallToolResult
		raw, _ := json.Marshal(resp.Result)
		_ = json.Unmarshal(raw, &res)
		if res.IsError {
			t.Fatalf("upsert append error: %s", res.Content[0].Text)
		}

		mem, _ := eng.Store.Get(store.GlobalContext, "config/db.md")
		if !strings.Contains(mem.Body, "host: db.example.com") || !strings.Contains(mem.Body, "port: 5432") {
			t.Errorf("append failed, got body: %q", mem.Body)
		}
	})

	t.Run("if_version_conflict", func(t *testing.T) {
		resp, err := client.call("tools/call", map[string]any{
			"name": "upsert",
			"arguments": map[string]any{
				"name":       "config/db.md",
				"body":       "host: conflict.com",
				"if_version": 1, // current version is 3
			},
		})
		if err != nil {
			t.Fatalf("call upsert conflict: %v", err)
		}
		var res CallToolResult
		raw, _ := json.Marshal(resp.Result)
		_ = json.Unmarshal(raw, &res)
		if !res.IsError {
			t.Errorf("expected isError: true on version conflict")
		}
		if !strings.Contains(res.Content[0].Text, "version conflict") {
			t.Errorf("expected version conflict in text: %s", res.Content[0].Text)
		}
	})

	t.Run("unknown_context_error", func(t *testing.T) {
		resp, err := client.call("tools/call", map[string]any{
			"name": "upsert",
			"arguments": map[string]any{
				"context": "nonexistent-context",
				"body":    "content",
			},
		})
		if err != nil {
			t.Fatalf("call upsert: %v", err)
		}
		var res CallToolResult
		raw, _ := json.Marshal(resp.Result)
		_ = json.Unmarshal(raw, &res)
		if !res.IsError {
			t.Errorf("expected isError: true on unknown context")
		}
		expectedMsg := "context 'nonexistent-context' does not exist; contexts are created by the user with `mem context create`; use 'global' or an existing context (call get with no arguments to list them)"
		if res.Content[0].Text != expectedMsg {
			t.Errorf("got %q, want %q", res.Content[0].Text, expectedMsg)
		}
	})

	t.Run("oversize_body", func(t *testing.T) {
		bigBody := strings.Repeat("x", maxBodyBytes+1)
		resp, err := client.call("tools/call", map[string]any{
			"name": "upsert",
			"arguments": map[string]any{
				"body": bigBody,
			},
		})
		if err != nil {
			t.Fatalf("call upsert: %v", err)
		}
		var res CallToolResult
		raw, _ := json.Marshal(resp.Result)
		_ = json.Unmarshal(raw, &res)
		if !res.IsError {
			t.Errorf("expected isError: true for body > 1 MiB")
		}
		if !strings.Contains(res.Content[0].Text, "1 MiB") {
			t.Errorf("expected 1 MiB in error: %s", res.Content[0].Text)
		}
	})

	t.Run("rename_by_id", func(t *testing.T) {
		mem, err := eng.Store.Get(store.GlobalContext, "config/db.md")
		if err != nil {
			t.Fatalf("get db.md: %v", err)
		}

		resp, err := client.call("tools/call", map[string]any{
			"name": "upsert",
			"arguments": map[string]any{
				"id":   mem.ID,
				"name": "config/renamed-database.md",
				"body": mem.Body,
			},
		})
		if err != nil {
			t.Fatalf("call upsert rename: %v", err)
		}
		var res CallToolResult
		raw, _ := json.Marshal(resp.Result)
		_ = json.Unmarshal(raw, &res)
		if res.IsError {
			t.Fatalf("upsert rename error: %s", res.Content[0].Text)
		}
		if !strings.Contains(res.Content[0].Text, "status: updated") {
			t.Errorf("expected updated status: %s", res.Content[0].Text)
		}

		// Verify renamed in store
		renamed, err := eng.Store.Get(store.GlobalContext, "config/renamed-database.md")
		if err != nil {
			t.Fatalf("failed to find renamed memory: %v", err)
		}
		if renamed.ID != mem.ID {
			t.Errorf("id mismatch after rename: got %q, want %q", renamed.ID, mem.ID)
		}
	})

	t.Run("indexing_warning_appended", func(t *testing.T) {
		// Creating an engine with an embedder that fails
		failingEmbed := &fakeEmbedder{fail: true}
		warnEng := newTestEngine(t, failingEmbed)
		warnClient := newTestClient(t, warnEng)
		defer warnClient.closeInput()

		resp, err := warnClient.call("tools/call", map[string]any{
			"name": "upsert",
			"arguments": map[string]any{
				"name": "failed-index.md",
				"body": "this memory will fail indexing",
			},
		})
		if err != nil {
			t.Fatalf("call upsert: %v", err)
		}
		var res CallToolResult
		raw, _ := json.Marshal(resp.Result)
		_ = json.Unmarshal(raw, &res)
		if res.IsError {
			t.Fatalf("upsert unexpectedly failed: %s", res.Content[0].Text)
		}
		expectedNote := "note: semantic indexing pending (embedder unavailable); keyword search works; run `mem sync`"
		if !strings.Contains(res.Content[0].Text, expectedNote) {
			t.Errorf("expected warning note %q in result: %s", expectedNote, res.Content[0].Text)
		}
	})
}

func TestTools_Delete(t *testing.T) {
	eng := newTestEngine(t, nil)
	client := newTestClient(t, eng)
	defer client.closeInput()

	mem, err := eng.Store.Upsert(store.UpsertInput{
		Context: store.GlobalContext,
		Name:    "to-delete.md",
		Body:    "Temporary file.",
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	t.Run("happy_path", func(t *testing.T) {
		resp, err := client.call("tools/call", map[string]any{
			"name": "delete",
			"arguments": map[string]any{
				"id": mem.ID,
			},
		})
		if err != nil {
			t.Fatalf("call delete: %v", err)
		}
		var res CallToolResult
		raw, _ := json.Marshal(resp.Result)
		_ = json.Unmarshal(raw, &res)
		if res.IsError {
			t.Fatalf("delete error: %s", res.Content[0].Text)
		}

		// Verify deletion
		_, err = eng.Store.Get(store.GlobalContext, mem.ID)
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("expected ErrNotFound after delete, got %v", err)
		}
	})

	t.Run("delete_default_file_protected", func(t *testing.T) {
		defaultMem, _ := eng.DefaultFile()
		resp, err := client.call("tools/call", map[string]any{
			"name": "delete",
			"arguments": map[string]any{
				"id": defaultMem.ID,
			},
		})
		if err != nil {
			t.Fatalf("call delete: %v", err)
		}
		var res CallToolResult
		raw, _ := json.Marshal(resp.Result)
		_ = json.Unmarshal(raw, &res)
		if !res.IsError {
			t.Errorf("expected isError: true when deleting default file")
		}
		if !strings.Contains(res.Content[0].Text, "the default file") {
			t.Errorf("expected default file protection message: %s", res.Content[0].Text)
		}
	})

	t.Run("delete_not_found", func(t *testing.T) {
		resp, err := client.call("tools/call", map[string]any{
			"name": "delete",
			"arguments": map[string]any{
				"id": "nonexistent_id",
			},
		})
		if err != nil {
			t.Fatalf("call delete: %v", err)
		}
		var res CallToolResult
		raw, _ := json.Marshal(resp.Result)
		_ = json.Unmarshal(raw, &res)
		if !res.IsError {
			t.Errorf("expected isError: true when deleting nonexistent ID")
		}
	})
}

func TestTools_RoundTrip(t *testing.T) {
	eng := newTestEngine(t, nil)
	client := newTestClient(t, eng)
	defer client.closeInput()

	// 1. Upsert a memory
	upsertResp, err := client.call("tools/call", map[string]any{
		"name": "upsert",
		"arguments": map[string]any{
			"name": "architecture/microservices.md",
			"body": "# Microservices Architecture\n\nWe use gRPC for internal service communication and REST for client gateways.",
			"tags": []string{"architecture", "grpc"},
		},
	})
	if err != nil {
		t.Fatalf("upsert call failed: %v", err)
	}
	var upsertRes CallToolResult
	rawU, _ := json.Marshal(upsertResp.Result)
	_ = json.Unmarshal(rawU, &upsertRes)
	if upsertRes.IsError {
		t.Fatalf("upsert error: %s", upsertRes.Content[0].Text)
	}

	// 2. Search finds it
	searchResp, err := client.call("tools/call", map[string]any{
		"name": "search",
		"arguments": map[string]any{
			"query": "gRPC internal service communication",
		},
	})
	if err != nil {
		t.Fatalf("search call failed: %v", err)
	}
	var searchRes CallToolResult
	rawS, _ := json.Marshal(searchResp.Result)
	_ = json.Unmarshal(rawS, &searchRes)
	if searchRes.IsError {
		t.Fatalf("search error: %s", searchRes.Content[0].Text)
	}
	if !strings.Contains(searchRes.Content[0].Text, "architecture/microservices.md") {
		t.Errorf("roundtrip search did not find memory: %s", searchRes.Content[0].Text)
	}
}
