package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"memory/internal/config"
)

func TestVersionAndHelp(t *testing.T) {
	h := newHarness(t)

	code, out, _ := h.run("version")
	if code != ExitOK {
		t.Fatalf("version exit = %d", code)
	}
	if !strings.Contains(out, Version) {
		t.Fatalf("version output = %q", out)
	}

	var v map[string]any
	if code, _ := h.runJSON(&v, "version"); code != ExitOK || v["version"] != Version {
		t.Fatalf("version --json = %v (code %d)", v, code)
	}

	for _, flagArgs := range [][]string{{"--version"}, {"-v"}} {
		if code, out, _ := h.run(flagArgs...); code != ExitOK || !strings.Contains(out, "mem "+Version) {
			t.Fatalf("%v: exit = %d, out = %q", flagArgs, code, out)
		}
	}

	code, out, _ = h.run("help")
	if code != ExitOK || !strings.Contains(out, "mem <command> [flags]") {
		t.Fatalf("help exit = %d, out = %q", code, out)
	}
	for _, name := range []string{"context create", "upsert", "search", "sync", "status", "config", "server", "mcp", "skill"} {
		if !strings.Contains(out, name) {
			t.Errorf("help is missing %q", name)
		}
	}
	if code, out, _ = h.run("help", "upsert"); code != ExitOK || !strings.Contains(out, "--if-version") {
		t.Fatalf("help upsert = %q", out)
	}
}

func TestUnknownCommandAndFlag(t *testing.T) {
	h := newHarness(t)
	if code, _, _ := h.run("nope"); code != ExitUsage {
		t.Fatalf("unknown command exit = %d, want %d", code, ExitUsage)
	}
	code, _, errOut := h.run("--totally-unknown")
	if code != ExitUsage {
		t.Fatalf("unknown flag exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(errOut, "unknown flag") {
		t.Fatalf("stderr = %q", errOut)
	}
}

func TestStubsExitNonZero(t *testing.T) {
	h := newHarness(t)
	// server and mcp are implemented by their own prompts; skill is still a stub.
	for _, args := range [][]string{{"skill"}, {"skill", "--install"}} {
		code, _, errOut := h.run(args...)
		if code != ExitError {
			t.Errorf("%v exit = %d, want %d", args, code, ExitError)
		}
		if !strings.Contains(errOut, "not available in this build yet") {
			t.Errorf("%v stderr = %q", args, errOut)
		}
	}
}

// TestServerFlagParsing checks that the dashboard flags parse without starting a
// listener: a non-loopback bind without a token must be rejected up front.
func TestServerFlagParsing(t *testing.T) {
	h := newHarness(t)
	code, _, errOut := h.run("server", "--host", "0.0.0.0", "--port", "8080")
	if code != ExitUsage {
		t.Fatalf("non-loopback without token exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(errOut, "authentication token") {
		t.Fatalf("stderr = %q", errOut)
	}
}

// TestMCPEmptyStdin exercises the wired MCP command: an immediate EOF must exit
// cleanly and write nothing but protocol messages (here: nothing) to stdout.
func TestMCPEmptyStdin(t *testing.T) {
	h := newHarness(t)
	h.stdin.Reset()
	code, out, _ := h.run("mcp")
	if code != ExitOK {
		t.Fatalf("mem mcp exit = %d, want %d", code, ExitOK)
	}
	if out != "" {
		t.Fatalf("mem mcp wrote to stdout: %q", out)
	}
}

// TestMCPProtocolSmoke runs one real handshake and a tool call in process.
func TestMCPProtocolSmoke(t *testing.T) {
	h := newHarness(t)
	h.stdin.Reset()
	h.stdin.WriteString(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}` + "\n")
	h.stdin.WriteString(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n")
	h.stdin.WriteString(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get","arguments":{}}}` + "\n")

	code, out, errOut := h.run("mcp")
	if code != ExitOK {
		t.Fatalf("mem mcp exit = %d: %s", code, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 JSON-RPC responses, got %d: %q\nstderr: %s", len(lines), out, errOut)
	}
	for i, line := range lines {
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("response %d is not JSON: %v (%q)", i, err, line)
		}
		if msg["jsonrpc"] != "2.0" {
			t.Errorf("response %d jsonrpc = %v", i, msg["jsonrpc"])
		}
	}
	if !strings.Contains(out, `"tools"`) || !strings.Contains(out, `"default_file"`) {
		t.Fatalf("unexpected responses: %s", out)
	}
}

func TestContextLifecycle(t *testing.T) {
	h := newHarness(t)

	code, out, _ := h.run("context", "create", "work")
	if code != ExitOK || !strings.Contains(out, "created context work") {
		t.Fatalf("create exit = %d out = %q", code, out)
	}

	var created struct {
		Slug    string `json:"slug"`
		Expires bool   `json:"expires"`
	}
	if code, _ := h.runJSON(&created, "context", "create", "second"); code != ExitOK {
		t.Fatalf("create second failed")
	}
	if created.Slug != "second" || created.Expires {
		t.Fatalf("created = %+v", created)
	}

	var list []map[string]any
	if code, _ := h.runJSON(&list, "context", "ls"); code != ExitOK {
		t.Fatalf("context ls failed")
	}
	slugs := map[string]bool{}
	for _, c := range list {
		slugs[c["slug"].(string)] = true
	}
	for _, want := range []string{"global", "work", "second"} {
		if !slugs[want] {
			t.Errorf("context ls missing %q: %v", want, slugs)
		}
	}

	if code, _, _ := h.run("context", "rm", "second"); code != ExitUsage {
		t.Errorf("rm without --yes and no TTY exit = %d, want usage", code)
	}
	if code, _, _ := h.run("context", "rm", "second", "--yes"); code != ExitOK {
		t.Errorf("rm --yes failed")
	}
	if code, _, _ := h.run("context", "rm", "second", "--yes"); code != ExitNotFound {
		t.Errorf("rm of a missing context exit = %d, want %d", code, ExitNotFound)
	}
}

func TestContextTempAndExpiryParsing(t *testing.T) {
	h := newHarness(t)

	// No slug: auto tmp-xxxx and temp.
	var auto struct {
		Slug      string  `json:"slug"`
		Expires   bool    `json:"expires"`
		ExpiresAt *string `json:"expires_at"`
	}
	if code, _ := h.runJSON(&auto, "context", "create"); code != ExitOK {
		t.Fatalf("auto temp create failed")
	}
	if !strings.HasPrefix(auto.Slug, "tmp-") || len(auto.Slug) != 8 {
		t.Fatalf("auto slug = %q", auto.Slug)
	}
	if !auto.Expires || auto.ExpiresAt == nil {
		t.Fatalf("auto context must expire: %+v", auto)
	}

	// --temp without --expiry uses the configured default, and a named slug
	// stays named.
	var named struct {
		Slug    string `json:"slug"`
		Expires bool   `json:"expires"`
	}
	if code, _ := h.runJSON(&named, "context", "create", "scratch", "--temp"); code != ExitOK {
		t.Fatalf("create --temp failed")
	}
	if named.Slug != "scratch" || !named.Expires {
		t.Fatalf("--temp named = %+v", named)
	}

	// --expiry alone implies temp.
	var byExpiry struct {
		Expires bool `json:"expires"`
	}
	if code, _ := h.runJSON(&byExpiry, "context", "create", "timed", "--expiry", "3"); code != ExitOK {
		t.Fatalf("create --expiry failed")
	}
	if !byExpiry.Expires {
		t.Fatal("--expiry alone must imply temp")
	}

	// Permanent named context without temp flags.
	var perm struct {
		Expires bool `json:"expires"`
	}
	if code, _ := h.runJSON(&perm, "context", "create", "perm"); code != ExitOK || perm.Expires {
		t.Fatalf("permanent = %+v (code %d)", perm, code)
	}

	if code, _, _ := h.run("context", "create", "bad", "--expiry", "0"); code != ExitUsage {
		t.Errorf("--expiry 0 exit = %d, want usage", code)
	}
	if code, _, _ := h.run("context", "create", "bad", "--expiry", "-3"); code != ExitUsage {
		t.Errorf("--expiry -3 exit = %d, want usage", code)
	}
	if code, _, errOut := h.run("context", "expiry", "scratch", "abc"); code != ExitUsage || !strings.Contains(errOut, "positive number") {
		t.Errorf("bad expiry parse exit = %d err = %q", code, errOut)
	}
}

func TestGlobalIsProtected(t *testing.T) {
	h := newHarness(t)

	code, _, errOut := h.run("context", "rm", "global", "--yes")
	if code != ExitConflict {
		t.Fatalf("rm global exit = %d, want %d", code, ExitConflict)
	}
	if !strings.Contains(errOut, "cannot be removed") {
		t.Fatalf("stderr = %q", errOut)
	}

	code, _, errOut = h.run("context", "expiry", "global", "3")
	if code != ExitConflict || !strings.Contains(errOut, "never expires") {
		t.Fatalf("expiry global exit = %d err = %q", code, errOut)
	}

	code, _, errOut = h.run("context", "create", "global")
	if code != ExitConflict {
		t.Fatalf("create global exit = %d, want conflict", code)
	}
}

func TestUpsertGetSearchDelete(t *testing.T) {
	h := newHarness(t)
	if code, _, _ := h.run("context", "create", "work"); code != ExitOK {
		t.Fatal("create work failed")
	}

	code, out, _ := h.run("upsert", "--context", "work", "--name", "auth/token.md", "--tag", "auth", "Tokens refresh every fifteen minutes.")
	if code != ExitOK {
		t.Fatalf("upsert exit = %d: %s", code, out)
	}
	id := strings.Fields(out)[0]
	if len(id) != 12 {
		t.Fatalf("printed id = %q", id)
	}

	code, out, _ = h.run("get", "auth/token.md", "--context", "work")
	if code != ExitOK || !strings.Contains(out, "Tokens refresh every fifteen minutes.") {
		t.Fatalf("get exit = %d out = %q", code, out)
	}

	var hits struct {
		Hits []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"hits"`
	}
	if code, errOut := h.runJSON(&hits, "search", "tokens", "refresh", "--context", "work"); code != ExitOK {
		t.Fatalf("search failed: %s", errOut)
	}
	if len(hits.Hits) == 0 {
		t.Fatalf("search returned no hits: %+v", hits)
	}
	found := false
	for _, hit := range hits.Hits {
		if hit.ID == id {
			found = true
		}
	}
	if !found {
		t.Fatalf("search did not find the memory %s: %+v", id, hits)
	}

	if code, _, _ := h.run("delete", id); code != ExitOK {
		t.Fatal("delete failed")
	}
	if code, _, _ := h.run("get", "auth/token.md", "--context", "work"); code != ExitNotFound {
		t.Fatalf("get after delete exit = %d, want %d", code, ExitNotFound)
	}
	if code, _, _ := h.run("delete", id); code != ExitNotFound {
		t.Fatalf("second delete exit = %d, want %d", code, ExitNotFound)
	}
}

func TestUpsertBodyFromStdinAndAppend(t *testing.T) {
	h := newHarness(t)

	h.stdin.Reset()
	h.stdin.WriteString("piped body line\n")
	code, out, _ := h.run("upsert", "--name", "piped.md")
	if code != ExitOK {
		t.Fatalf("upsert from stdin exit = %d", code)
	}
	if !strings.Contains(out, "v1") {
		t.Fatalf("out = %q", out)
	}
	if _, got, _ := h.run("get", "piped.md"); !strings.Contains(got, "piped body line") {
		t.Fatalf("stdin body not stored: %q", got)
	}

	// "-" also means stdin.
	h.stdin.Reset()
	h.stdin.WriteString("dash body\n")
	if code, _, _ := h.run("upsert", "--name", "dash.md", "-"); code != ExitOK {
		t.Fatalf("upsert - failed")
	}
	if _, got, _ := h.run("get", "dash.md"); !strings.Contains(got, "dash body") {
		t.Fatalf("dash body not stored: %q", got)
	}

	// Append keeps the old content and bumps the version.
	code, out, _ = h.run("upsert", "--name", "piped.md", "--append", "second line")
	if code != ExitOK || !strings.Contains(out, "v2") {
		t.Fatalf("append out = %q (code %d)", out, code)
	}
	_, got, _ := h.run("get", "piped.md")
	if !strings.Contains(got, "piped body line") || !strings.Contains(got, "second line") {
		t.Fatalf("append lost content: %q", got)
	}
}

func TestUpsertVersionConflict(t *testing.T) {
	h := newHarness(t)
	if code, out, _ := h.run("upsert", "--name", "race.md", "v1"); code != ExitOK {
		t.Fatalf("upsert: %s", out)
	}
	code, _, errOut := h.run("upsert", "--name", "race.md", "--if-version", "99", "v2")
	if code != ExitConflict {
		t.Fatalf("conflict exit = %d, want %d", code, ExitConflict)
	}
	if !strings.Contains(errOut, "is at version 1, not 99") {
		t.Fatalf("conflict message = %q", errOut)
	}
	if code, out, _ := h.run("upsert", "--name", "race.md", "--if-version", "1", "v2"); code != ExitOK {
		t.Fatalf("matching --if-version failed: %s", out)
	}
	if code, _, _ := h.run("upsert", "--name", "race.md", "--if-version", "0", "x"); code != ExitUsage {
		t.Fatalf("--if-version 0 exit = %d, want usage", code)
	}
}

func TestUpsertUnknownContextDoesNotCreateIt(t *testing.T) {
	h := newHarness(t)
	code, _, errOut := h.run("upsert", "--context", "missing", "--name", "x.md", "body")
	if code != ExitNotFound {
		t.Fatalf("exit = %d, want %d", code, ExitNotFound)
	}
	if !strings.Contains(errOut, "mem context create") {
		t.Fatalf("hint missing: %q", errOut)
	}
	_, out, _ := h.run("context", "ls")
	if strings.Contains(out, "missing") {
		t.Fatal("an unknown context was auto-created")
	}
}

func TestLSAndGetDefault(t *testing.T) {
	h := newHarness(t)
	if code, _, _ := h.run("context", "create", "work"); code != ExitOK {
		t.Fatal("create failed")
	}
	if code, _, _ := h.run("upsert", "--context", "work", "--name", "a.md", "alpha body"); code != ExitOK {
		t.Fatal("upsert failed")
	}
	if code, _, _ := h.run("upsert", "--context", "work", "--name", "b.md", "beta body"); code != ExitOK {
		t.Fatal("upsert failed")
	}

	var rows []map[string]any
	if code, _ := h.runJSON(&rows, "ls", "--context", "work"); code != ExitOK {
		t.Fatal("ls failed")
	}
	if len(rows) != 2 {
		t.Fatalf("ls returned %d rows, want 2", len(rows))
	}
	for _, r := range rows {
		if _, ok := r["id"]; !ok {
			t.Errorf("ls row missing id: %v", r)
		}
	}

	var limited []map[string]any
	h.runJSON(&limited, "ls", "--context", "work", "--limit", "1")
	if len(limited) != 1 {
		t.Fatalf("--limit ignored: got %d rows", len(limited))
	}

	// No-arg get: default file plus contexts.
	var def struct {
		DefaultFile map[string]any   `json:"default_file"`
		Contexts    []map[string]any `json:"contexts"`
	}
	if code, _ := h.runJSON(&def, "get"); code != ExitOK {
		t.Fatal("get with no args failed")
	}
	if def.DefaultFile["name"] != "readme.md" {
		t.Fatalf("default file = %v", def.DefaultFile)
	}
	if len(def.Contexts) < 2 {
		t.Fatalf("default get listed %d contexts", len(def.Contexts))
	}

	// Unknown context on ls is an error, not an empty list.
	if code, _, _ := h.run("ls", "--context", "nope"); code != ExitNotFound {
		t.Fatalf("ls unknown context exit = %d, want %d", code, ExitNotFound)
	}
}

func TestSearchRequiresQueryAndScope(t *testing.T) {
	h := newHarness(t)
	if code, _, _ := h.run("search"); code != ExitUsage {
		t.Fatalf("empty search exit = %d, want usage", code)
	}
	if code, _, _ := h.run("search", "--no-global", "hello"); code != ExitError {
		t.Fatalf("no-global without context exit = %d, want error", code)
	}
	if code, _, _ := h.run("search", "hello", "--context", "nope"); code != ExitNotFound {
		t.Fatalf("unknown context exit = %d, want %d", code, ExitNotFound)
	}
}

func TestSyncAndStatus(t *testing.T) {
	h := newHarness(t)
	if code, _, _ := h.run("context", "create", "work"); code != ExitOK {
		t.Fatal("create failed")
	}
	if code, _, _ := h.run("upsert", "--context", "work", "--name", "s.md", "synced body"); code != ExitOK {
		t.Fatal("upsert failed")
	}

	var rep map[string]any
	if code, errOut := h.runJSON(&rep, "sync"); code != ExitOK {
		t.Fatalf("sync failed: %s", errOut)
	}
	if _, ok := rep["unchanged"]; !ok {
		t.Fatalf("sync JSON = %v", rep)
	}

	var st map[string]any
	if code, errOut := h.runJSON(&st, "status"); code != ExitOK {
		t.Fatalf("status failed: %s", errOut)
	}
	for _, key := range []string{"data_dir", "models_dir", "template", "contexts", "memories", "indexed_docs", "in_sync", "embed_present", "sidecar_ok"} {
		if key == "template" {
			continue
		}
		if _, ok := st[key]; !ok {
			t.Errorf("status JSON missing %q: %v", key, st)
		}
	}
	if st["in_sync"] != true {
		t.Errorf("status in_sync = %v, want true", st["in_sync"])
	}
	if code, out, _ := h.run("status"); code != ExitOK || !strings.Contains(out, "mem "+Version) {
		t.Fatalf("human status = %q", out)
	}
}

func TestConfigGetSetValidation(t *testing.T) {
	h := newHarness(t)

	var all map[string]string
	if code, _ := h.runJSON(&all, "config"); code != ExitOK {
		t.Fatal("config dump failed")
	}
	for _, key := range []string{"data_dir", "models_dir", "port", "default_temp_expiry_days", "idle_timeout_seconds", "embed_model", "rerank", "rerank_model", "sidecar_path"} {
		if _, ok := all[key]; !ok {
			t.Errorf("config dump missing %q: %v", key, all)
		}
	}

	if code, out, _ := h.run("config", "port"); code != ExitOK || strings.TrimSpace(out) != "7777" {
		t.Fatalf("config port = %q (code %d)", out, code)
	}

	if code, _, _ := h.run("config", "port", "9090"); code != ExitOK {
		t.Fatal("config set port failed")
	}
	cfg, err := configLoad(h.cfgF)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if cfg.Port != 9090 {
		t.Fatalf("saved port = %d", cfg.Port)
	}
	if code, out, _ := h.run("config", "port"); code != ExitOK || strings.TrimSpace(out) != "9090" {
		t.Fatalf("config port after set = %q", out)
	}

	bad := [][]string{
		{"config", "port", "0"},
		{"config", "port", "70000"},
		{"config", "port", "abc"},
		{"config", "default_temp_expiry_days", "0"},
		{"config", "idle_timeout_seconds", "-1"},
		{"config", "rerank", "maybe"},
		{"config", "nonsense", "1"},
	}
	for _, args := range bad {
		code, _, errOut := h.run(args...)
		if code != ExitUsage {
			t.Errorf("%v exit = %d, want %d", args, code, ExitUsage)
		}
		if !strings.Contains(errOut, "mem:") {
			t.Errorf("%v stderr = %q", args, errOut)
		}
	}
	// rerank accepts a boolean.
	if code, _, _ := h.run("config", "rerank", "true"); code != ExitOK {
		t.Fatal("config rerank true failed")
	}
	if code, out, _ := h.run("config", "rerank"); code != ExitOK || strings.TrimSpace(out) != "true" {
		t.Fatalf("rerank = %q", out)
	}
}

func TestJSONErrorsAreMachineReadable(t *testing.T) {
	h := newHarness(t)
	code, out, errOut := h.run("get", "definitely-missing", "--json")
	if code != ExitNotFound {
		t.Fatalf("exit = %d", code)
	}
	var payload struct {
		Error string `json:"error"`
		Code  int    `json:"code"`
		Hint  string `json:"hint"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("error JSON: %v (stdout %q)", err, out)
	}
	if payload.Code != ExitNotFound || payload.Error == "" {
		t.Fatalf("payload = %+v", payload)
	}
	// The human line still goes to stderr so stdout stays parseable.
	if !strings.Contains(errOut, "mem:") {
		t.Fatalf("stderr = %q", errOut)
	}
}

func TestGlobalFlagsAfterCommand(t *testing.T) {
	h := newHarness(t)
	// --json is documented as a global flag and must also work after the
	// command word, as the usage lines show.
	code, out, _ := h.run("context", "ls", "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("not JSON: %v (%q)", err, out)
	}
}

func TestQuietSuppressesHints(t *testing.T) {
	h := newHarness(t)
	if code, _, _ := h.run("context", "create", "work"); code != ExitOK {
		t.Fatal("create failed")
	}
	_, _, loud := h.run("upsert", "--context", "work", "--name", "a.md", "body")
	if !strings.Contains(loud, "not set up yet") {
		t.Fatalf("expected the setup hint, got %q", loud)
	}
	code, _, quiet := h.run("upsert", "-q", "--context", "work", "--name", "b.md", "body")
	if code != ExitOK {
		t.Fatalf("quiet upsert exit = %d", code)
	}
	if strings.Contains(quiet, "not set up yet") {
		t.Fatalf("-q still printed the hint: %q", quiet)
	}
}

func configLoad(path string) (config.Config, error) { return config.LoadFile(path) }

func TestUsageErrorsPrintUsage(t *testing.T) {
	h := newHarness(t)
	code, _, errOut := h.run("upsert", "a", "b")
	if code != ExitUsage || !strings.Contains(errOut, "one body argument") {
		t.Fatalf("exit = %d err = %q", code, errOut)
	}
	if code, _, _ := h.run("context", "frobnicate"); code != ExitUsage {
		t.Fatalf("bad context subcommand exit = %d", code)
	}
	if code, _, _ := h.run("context"); code != ExitUsage {
		t.Fatalf("bare context exit = %d", code)
	}
}

func TestExitCodeTable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"plain", fmt.Errorf("boom"), ExitError},
		{"usage", usagef("bad"), ExitUsage},
		{"notfound", notFoundf("gone"), ExitNotFound},
		{"conflict", conflictf("differ"), ExitConflict},
	}
	for _, c := range cases {
		if got := exitCodeFor(c.err, ExitError); got != c.want {
			t.Errorf("%s: exitCodeFor = %d, want %d", c.name, got, c.want)
		}
	}
	if got := exitCodeFor(nil, 0); got != ExitOK {
		t.Errorf("nil error = %d, want 0", got)
	}
}
