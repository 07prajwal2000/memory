package mcp

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"memory/internal/store"
)

func toolCall(t *testing.T, c *testClient, name string, args map[string]any) CallToolResult {
	t.Helper()
	resp, err := c.call("tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var res CallToolResult
	raw, _ := json.Marshal(resp.Result)
	_ = json.Unmarshal(raw, &res)
	return res
}

func initInstructions(t *testing.T, c *testClient) string {
	t.Helper()
	resp, err := c.call("initialize", InitializeParams{ProtocolVersion: "2025-06-18"})
	if err != nil {
		t.Fatal(err)
	}
	var res InitializeResult
	raw, _ := json.Marshal(resp.Result)
	_ = json.Unmarshal(raw, &res)
	return res.Instructions
}

func TestProjectDefault_Instructions(t *testing.T) {
	eng := newTestEngine(t, nil)
	base := FormatInstructions(eng)

	if got := initInstructions(t, newTestClient(t, eng)); got != base {
		t.Fatalf("no marker must leave instructions unchanged:\n%q\n%q", got, base)
	}

	p := store.ProjectDefault{Path: "/work/p/.agent-memory", Context: "proj"}
	got := initInstructions(t, newTestClient(t, eng, WithProjectDefault(p)))
	want := base + "\n\nDefault context for this session: \"proj\" (from /work/p/.agent-memory). Calls without a context use it plus global; pass context=\"global\" for notes that apply across projects."
	if got != want {
		t.Fatalf("got %q", got)
	}

	bad := store.ProjectDefault{Path: "/work/p/.agent-memory", Err: errors.New("bad toml")}
	got = initInstructions(t, newTestClient(t, eng, WithProjectDefault(bad)))
	if !strings.HasPrefix(got, base+"\n\nWarning:") || !strings.Contains(got, "bad toml") {
		t.Fatalf("got %q", got)
	}
}

func TestProjectDefault_Tools(t *testing.T) {
	eng := newTestEngine(t, nil)
	for _, c := range []string{"proj", "other"} {
		if _, err := eng.Store.CreateContext(c, nil); err != nil {
			t.Fatal(err)
		}
	}
	p := store.ProjectDefault{Path: "/work/p/.agent-memory", Context: "proj"}
	c := newTestClient(t, eng, WithProjectDefault(p))
	defer c.closeInput()

	res := toolCall(t, c, "upsert", map[string]any{"name": "a", "body": "needle one"})
	if res.IsError || !strings.Contains(res.Content[0].Text, "context: proj") {
		t.Fatalf("upsert should use marker: %+v", res)
	}
	res = toolCall(t, c, "upsert", map[string]any{"name": "g", "body": "needle global", "context": "global"})
	if res.IsError || !strings.Contains(res.Content[0].Text, "context: global") {
		t.Fatalf("explicit global must win: %+v", res)
	}
	res = toolCall(t, c, "upsert", map[string]any{"name": "o", "body": "needle other", "context": "other"})
	if res.IsError || !strings.Contains(res.Content[0].Text, "context: other") {
		t.Fatalf("explicit context must win: %+v", res)
	}

	// get by name and list resolve in the marker context; no-arg get reports it
	if res = toolCall(t, c, "get", map[string]any{"name": "a"}); res.IsError || !strings.Contains(res.Content[0].Text, "context: proj") {
		t.Fatalf("get name: %+v", res)
	}
	if res = toolCall(t, c, "get", map[string]any{"name": "g"}); !res.IsError {
		t.Fatalf("g lives in global, marker context must not see it: %+v", res)
	}
	if res = toolCall(t, c, "get", map[string]any{"name": "g", "context": "global"}); res.IsError {
		t.Fatalf("explicit global get: %+v", res)
	}
	if res = toolCall(t, c, "get", nil); res.IsError || !strings.Contains(res.Content[0].Text, "Default context: \"proj\" (from /work/p/.agent-memory)") {
		t.Fatalf("get no-arg: %+v", res)
	}

	// search: marker context + global; no_global is satisfied by the marker
	res = toolCall(t, c, "search", map[string]any{"query": "needle"})
	txt := res.Content[0].Text
	if res.IsError || !strings.Contains(txt, "proj/a") || !strings.Contains(txt, "global/g") || strings.Contains(txt, "other/o") {
		t.Fatalf("search default: %s", txt)
	}
	res = toolCall(t, c, "search", map[string]any{"query": "needle", "no_global": true})
	txt = res.Content[0].Text
	if res.IsError || !strings.Contains(txt, "proj/a") || strings.Contains(txt, "global/g") {
		t.Fatalf("search no_global: %s", txt)
	}
	res = toolCall(t, c, "search", map[string]any{"query": "needle", "context": "other", "no_global": true})
	txt = res.Content[0].Text
	if !strings.Contains(txt, "other/o") || strings.Contains(txt, "proj/a") {
		t.Fatalf("explicit context should win: %s", txt)
	}
}

func TestProjectDefault_MissingContext(t *testing.T) {
	eng := newTestEngine(t, nil)
	p := store.ProjectDefault{Path: "/work/p/.agent-memory", Context: "nope"}
	c := newTestClient(t, eng, WithProjectDefault(p))
	defer c.closeInput()

	res := toolCall(t, c, "upsert", map[string]any{"name": "a", "body": "needle"})
	msg := res.Content[0].Text
	if !res.IsError || !strings.Contains(msg, "/work/p/.agent-memory") || !strings.Contains(msg, "mem context create nope") {
		t.Fatalf("upsert should fail clearly: %+v", res)
	}
	if list, _ := eng.Store.List(store.GlobalContext); len(list) > 1 { // only the default file
		t.Fatalf("nothing may land in global: %v", list)
	}

	toolCall(t, c, "upsert", map[string]any{"name": "g", "body": "needle", "context": "global"})
	res = toolCall(t, c, "search", map[string]any{"query": "needle"})
	if res.IsError || !strings.Contains(res.Content[0].Text, "using global") || !strings.Contains(res.Content[0].Text, "global/g") {
		t.Fatalf("search should fall back with a note: %+v", res)
	}
	res = toolCall(t, c, "get", map[string]any{"name": "g"})
	if res.IsError || !strings.HasPrefix(res.Content[0].Text, "note: ") {
		t.Fatalf("get should fall back with a note: %+v", res)
	}
}
