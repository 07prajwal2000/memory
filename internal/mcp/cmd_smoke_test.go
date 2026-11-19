//go:build smoke

package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// TestCmdSmokeOverOSPipe drives Serve over real OS pipes (os.Pipe) with a minimal
// hand-written client. The CLI wiring agent (mem mcp) can reuse this pattern.
func TestCmdSmokeOverOSPipe(t *testing.T) {
	eng := newTestEngine(t, nil)

	// OS pipe 1: client -> server (stdin)
	serverInR, clientInW, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdin pipe: %v", err)
	}
	defer serverInR.Close()

	// OS pipe 2: server -> client (stdout)
	clientOutR, serverOutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	defer clientOutR.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serveErrCh := make(chan error, 1)
	go func() {
		// Server runs with the OS pipe handles as its stdio
		err := Serve(ctx, serverInR, serverOutW, eng)
		serveErrCh <- err
		_ = serverOutW.Close()
	}()

	clientScanner := bufio.NewScanner(clientOutR)
	buf := make([]byte, 64*1024)
	clientScanner.Buffer(buf, maxScanBufferSize)

	send := func(msg string) {
		if _, err := clientInW.Write([]byte(msg + "\n")); err != nil {
			t.Fatalf("write to server stdin pipe: %v", err)
		}
	}

	recv := func() Response {
		if !clientScanner.Scan() {
			if err := clientScanner.Err(); err != nil {
				t.Fatalf("scan server stdout: %v", err)
			}
			t.Fatalf("unexpected EOF reading server stdout")
		}
		var resp Response
		if err := json.Unmarshal(clientScanner.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal response %q: %v", string(clientScanner.Bytes()), err)
		}
		return resp
	}

	// 1. Initialize
	send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","clientInfo":{"name":"smoke-agent","version":"1.0.0"}}}`)
	resp1 := recv()
	if resp1.Error != nil {
		t.Fatalf("initialize error: %v", resp1.Error)
	}
	var initRes InitializeResult
	raw1, _ := json.Marshal(resp1.Result)
	_ = json.Unmarshal(raw1, &initRes)
	if initRes.ProtocolVersion != "2025-06-18" {
		t.Errorf("expected protocol version 2025-06-18, got %q", initRes.ProtocolVersion)
	}
	if initRes.ServerInfo.Name != "mem" {
		t.Errorf("expected server name 'mem', got %q", initRes.ServerInfo.Name)
	}
	t.Logf("smoke: initialize negotiated protocol %s, server %s", initRes.ProtocolVersion, initRes.ServerInfo.Name)

	// 2. Initialized notification
	send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)

	// 3. Ping
	send(`{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	resp2 := recv()
	if resp2.Error != nil {
		t.Fatalf("ping error: %v", resp2.Error)
	}
	t.Logf("smoke: ping succeeded")

	// 4. Tools list
	send(`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`)
	resp3 := recv()
	if resp3.Error != nil {
		t.Fatalf("tools/list error: %v", resp3.Error)
	}
	var listRes ToolsListResult
	raw3, _ := json.Marshal(resp3.Result)
	_ = json.Unmarshal(raw3, &listRes)
	if len(listRes.Tools) != 4 {
		t.Fatalf("expected 4 tools, got %d", len(listRes.Tools))
	}
	t.Logf("smoke: tools/list returned %d tools", len(listRes.Tools))

	// 5. Tools call: get with no args
	send(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"get","arguments":{}}}`)
	resp4 := recv()
	if resp4.Error != nil {
		t.Fatalf("get error: %v", resp4.Error)
	}
	var getRes CallToolResult
	raw4, _ := json.Marshal(resp4.Result)
	_ = json.Unmarshal(raw4, &getRes)
	if getRes.IsError || len(getRes.Content) == 0 || !strings.Contains(getRes.Content[0].Text, "Live Contexts") {
		t.Fatalf("get no-args unexpected result: %v", getRes)
	}
	t.Logf("smoke: get no-args returned default file and contexts")

	// 6. Tools call: upsert
	send(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"upsert","arguments":{"name":"smoke/test.md","body":"smoke test persistent memory payload"}}}`)
	resp5 := recv()
	if resp5.Error != nil {
		t.Fatalf("upsert error: %v", resp5.Error)
	}
	var upsertRes CallToolResult
	raw5, _ := json.Marshal(resp5.Result)
	_ = json.Unmarshal(raw5, &upsertRes)
	if upsertRes.IsError || !strings.Contains(upsertRes.Content[0].Text, "status: created") {
		t.Fatalf("upsert unexpected result: %v", upsertRes)
	}
	t.Logf("smoke: upsert created memory smoke/test.md")

	// 7. Tools call: search
	send(`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"search","arguments":{"query":"persistent memory payload"}}}`)
	resp6 := recv()
	if resp6.Error != nil {
		t.Fatalf("search error: %v", resp6.Error)
	}
	var searchRes CallToolResult
	raw6, _ := json.Marshal(resp6.Result)
	_ = json.Unmarshal(raw6, &searchRes)
	if searchRes.IsError || !strings.Contains(searchRes.Content[0].Text, "smoke/test.md") {
		t.Fatalf("search unexpected result: %v", searchRes)
	}
	t.Logf("smoke: search successfully found smoke/test.md")

	// 8. Close client pipe writer (EOF on stdin)
	if err := clientInW.Close(); err != nil {
		t.Fatalf("close client pipe writer: %v", err)
	}

	// 9. Verify server exits cleanly on EOF with nil error
	select {
	case err := <-serveErrCh:
		if err != nil {
			t.Fatalf("expected nil error on stdin EOF, got: %v", err)
		}
		t.Logf("smoke: Serve exited cleanly with nil error on EOF")
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting for Serve to exit on EOF")
	}
}
