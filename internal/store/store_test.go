package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"memory/internal/config"
)

// fakeClock is an injectable clock for expiry tests.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock(t time.Time) *fakeClock { return &fakeClock{now: t} }

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

var testEpoch = time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)

// newTestStore opens a store in a temp dir with a fake clock.
func newTestStore(t *testing.T, opts ...Option) (*Store, *fakeClock, string) {
	t.Helper()
	dir := t.TempDir()
	clock := newClock(testEpoch)
	cfg := config.Config{
		DataDir:               dir,
		ModelsDir:             filepath.Join(dir, "models"),
		Port:                  7777,
		DefaultTempExpiryDays: 7,
	}
	all := append([]Option{WithClock(clock.Now)}, opts...)
	s, err := Open(cfg, all...)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s, clock, dir
}

func mustCreateContext(t *testing.T, s *Store, slug string, expiryDays *int) Context {
	t.Helper()
	c, err := s.CreateContext(slug, expiryDays)
	if err != nil {
		t.Fatalf("CreateContext(%q): %v", slug, err)
	}
	return c
}

func mustUpsert(t *testing.T, s *Store, in UpsertInput) Memory {
	t.Helper()
	m, err := s.Upsert(in)
	if err != nil {
		t.Fatalf("Upsert(%+v): %v", in, err)
	}
	return m
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestOpenSeedsGlobalAndDefaultFile(t *testing.T) {
	s, _, dir := newTestStore(t)

	global, err := s.GetContext(GlobalContext)
	if err != nil {
		t.Fatalf("GetContext(global): %v", err)
	}
	if global.ExpiresAt != nil {
		t.Fatalf("global context must not expire, got %v", global.ExpiresAt)
	}
	if !global.CreatedAt.Equal(testEpoch) {
		t.Fatalf("global CreatedAt = %v, want %v", global.CreatedAt, testEpoch)
	}

	m, err := s.Get("", DefaultFileName)
	if err != nil {
		t.Fatalf("Get default file: %v", err)
	}
	if m.Context != GlobalContext {
		t.Fatalf("default file context = %q, want global", m.Context)
	}
	if !strings.Contains(m.Body, "Memory") {
		t.Fatalf("default file body looks wrong: %q", m.Body)
	}
	if m.Version != 1 {
		t.Fatalf("default file version = %d, want 1", m.Version)
	}

	path := filepath.Join(dir, "contexts", GlobalContext, DefaultFileName)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("default file missing on disk: %v", err)
	}

	// Reopening must not duplicate or bump the default file.
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	again, err := Open(config.Config{DataDir: dir, DefaultTempExpiryDays: 7}, WithClock(func() time.Time { return testEpoch }))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer again.Close()
	got, err := again.Get("", DefaultFileName)
	if err != nil {
		t.Fatalf("Get default file after reopen: %v", err)
	}
	if got.ID != m.ID || got.Version != 1 {
		t.Fatalf("default file changed on reopen: id %q v%d, want %q v1", got.ID, got.Version, m.ID)
	}
}

func TestOpenAdoptsHandWrittenDefaultFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "contexts", GlobalContext, DefaultFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	const mine = "my own instructions\n"
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s, err := Open(config.Config{DataDir: dir, DefaultTempExpiryDays: 7}, WithClock(func() time.Time { return testEpoch }))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	got, err := s.Get("", DefaultFileName)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Body != mine {
		t.Fatalf("body = %q; the hand-written file must not be overwritten", got.Body)
	}
}

func TestUpsertCreateUnnamed(t *testing.T) {
	s, _, dir := newTestStore(t)
	mustCreateContext(t, s, "work", nil)

	m := mustUpsert(t, s, UpsertInput{Context: "work", Body: "hello", UpdatedBy: "claude"})
	if len(m.ID) != 12 {
		t.Fatalf("id = %q, want 12 chars", m.ID)
	}
	if m.Name != m.ID+".md" {
		t.Fatalf("unnamed memory name = %q, want %q", m.Name, m.ID+".md")
	}
	if m.Version != 1 {
		t.Fatalf("version = %d, want 1", m.Version)
	}
	if !m.UpdatedAt.Equal(testEpoch) {
		t.Fatalf("UpdatedAt = %v, want %v", m.UpdatedAt, testEpoch)
	}
	if m.UpdatedBy != "claude" {
		t.Fatalf("UpdatedBy = %q", m.UpdatedBy)
	}

	// The file must be readable and carry frontmatter.
	raw := readFile(t, filepath.Join(dir, "contexts", "work", m.Name))
	if !strings.HasPrefix(raw, "---\n") || !strings.Contains(raw, "id: "+m.ID) || !strings.HasSuffix(raw, "hello") {
		t.Fatalf("file content unexpected:\n%s", raw)
	}

	// And it must be retrievable by name and by id.
	byName, err := s.Get("work", m.Name)
	if err != nil {
		t.Fatalf("Get by name: %v", err)
	}
	if byName.ID != m.ID || byName.Body != "hello" {
		t.Fatalf("Get by name = %+v", byName)
	}
	byID, err := s.Get("work", m.ID)
	if err != nil {
		t.Fatalf("Get by id: %v", err)
	}
	if byID.ID != m.ID {
		t.Fatalf("Get by id = %+v", byID)
	}
}

func TestUpsertSemantics(t *testing.T) {
	version := func(v int) *int { return &v }

	t.Run("create named", func(t *testing.T) {
		s, _, dir := newTestStore(t)
		mustCreateContext(t, s, "work", nil)
		m := mustUpsert(t, s, UpsertInput{Context: "work", Name: "auth/tokens", Body: "a"})
		if m.Name != "auth/tokens.md" {
			t.Fatalf("name = %q", m.Name)
		}
		if _, err := os.Stat(filepath.Join(dir, "contexts", "work", "auth", "tokens.md")); err != nil {
			t.Fatalf("nested file missing: %v", err)
		}
	})

	t.Run("update by name", func(t *testing.T) {
		s, _, _ := newTestStore(t)
		mustCreateContext(t, s, "work", nil)
		first := mustUpsert(t, s, UpsertInput{Context: "work", Name: "notes", Body: "v1"})
		second := mustUpsert(t, s, UpsertInput{Context: "work", Name: "notes", Body: "v2"})
		if second.ID != first.ID {
			t.Fatalf("update changed id: %q -> %q", first.ID, second.ID)
		}
		if second.Version != 2 {
			t.Fatalf("version = %d, want 2", second.Version)
		}
		got, err := s.Get("work", "notes.md")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Body != "v2" {
			t.Fatalf("body = %q, want v2", got.Body)
		}
	})

	t.Run("update by id", func(t *testing.T) {
		s, _, _ := newTestStore(t)
		mustCreateContext(t, s, "work", nil)
		first := mustUpsert(t, s, UpsertInput{Context: "work", Body: "unnamed"})
		second := mustUpsert(t, s, UpsertInput{Context: "work", ID: first.ID, Body: "changed"})
		if second.ID != first.ID || second.Version != 2 || second.Body != "changed" {
			t.Fatalf("second = %+v", second)
		}
		if second.Name != first.Name {
			t.Fatalf("name changed without a Name: %q -> %q", first.Name, second.Name)
		}
	})

	t.Run("renamed by id", func(t *testing.T) {
		s, _, dir := newTestStore(t)
		mustCreateContext(t, s, "work", nil)
		first := mustUpsert(t, s, UpsertInput{Context: "work", Name: "old", Body: "x"})
		second := mustUpsert(t, s, UpsertInput{Context: "work", ID: first.ID, Name: "new", Body: "x"})
		if second.Name != "new.md" {
			t.Fatalf("name = %q, want new.md", second.Name)
		}
		if _, err := os.Stat(filepath.Join(dir, "contexts", "work", "old.md")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("old file still present: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "contexts", "work", "new.md")); err != nil {
			t.Fatalf("new file missing: %v", err)
		}
		// The rename alone counts as a change, so the version bumps.
		if second.Version != 2 {
			t.Fatalf("version = %d, want 2", second.Version)
		}
	})

	t.Run("rename onto taken name conflicts", func(t *testing.T) {
		s, _, _ := newTestStore(t)
		mustCreateContext(t, s, "work", nil)
		a := mustUpsert(t, s, UpsertInput{Context: "work", Name: "a", Body: "a"})
		mustUpsert(t, s, UpsertInput{Context: "work", Name: "b", Body: "b"})
		_, err := s.Upsert(UpsertInput{Context: "work", ID: a.ID, Name: "b", Body: "a"})
		if !errors.Is(err, ErrExists) {
			t.Fatalf("err = %v, want ErrExists", err)
		}
	})

	t.Run("rename onto unindexed file conflicts", func(t *testing.T) {
		s, _, dir := newTestStore(t)
		mustCreateContext(t, s, "work", nil)
		a := mustUpsert(t, s, UpsertInput{Context: "work", Name: "a", Body: "a"})
		writeRaw(t, filepath.Join(dir, "contexts", "work", "taken.md"), "someone else's file\n")

		_, err := s.Upsert(UpsertInput{Context: "work", ID: a.ID, Name: "taken", Body: "a"})
		if !errors.Is(err, ErrExists) {
			t.Fatalf("err = %v, want ErrExists", err)
		}
		if got := readFile(t, filepath.Join(dir, "contexts", "work", "taken.md")); got != "someone else's file\n" {
			t.Fatalf("unindexed file was overwritten: %q", got)
		}
	})

	t.Run("append adds newline", func(t *testing.T) {
		s, _, _ := newTestStore(t)
		mustCreateContext(t, s, "work", nil)
		mustUpsert(t, s, UpsertInput{Context: "work", Name: "log", Body: "line1"})
		m := mustUpsert(t, s, UpsertInput{Context: "work", Name: "log", Body: "line2", Append: true})
		if m.Body != "line1\nline2" {
			t.Fatalf("body = %q, want %q", m.Body, "line1\nline2")
		}
		m = mustUpsert(t, s, UpsertInput{Context: "work", Name: "log", Body: "line3\n", Append: true})
		if m.Body != "line1\nline2\nline3\n" {
			t.Fatalf("body = %q", m.Body)
		}
		if m.Version != 3 {
			t.Fatalf("version = %d, want 3", m.Version)
		}
	})

	t.Run("append on create is a replace", func(t *testing.T) {
		s, _, _ := newTestStore(t)
		mustCreateContext(t, s, "work", nil)
		m := mustUpsert(t, s, UpsertInput{Context: "work", Name: "fresh", Body: "only", Append: true})
		if m.Body != "only" || m.Version != 1 {
			t.Fatalf("m = %+v", m)
		}
	})

	t.Run("no-op does not bump", func(t *testing.T) {
		s, _, dir := newTestStore(t)
		mustCreateContext(t, s, "work", nil)
		first := mustUpsert(t, s, UpsertInput{Context: "work", Name: "same", Body: "body", Tags: []string{"a", "b"}})
		before := readFile(t, filepath.Join(dir, "contexts", "work", "same.md"))
		second := mustUpsert(t, s, UpsertInput{Context: "work", Name: "same", Body: "body", Tags: []string{"a", "b"}})
		if second.Version != first.Version {
			t.Fatalf("no-op bumped version: %d -> %d", first.Version, second.Version)
		}
		if after := readFile(t, filepath.Join(dir, "contexts", "work", "same.md")); after != before {
			t.Fatalf("no-op rewrote the file:\nbefore %q\nafter  %q", before, after)
		}
	})

	t.Run("IfVersion mismatch fails", func(t *testing.T) {
		s, _, _ := newTestStore(t)
		mustCreateContext(t, s, "work", nil)
		m := mustUpsert(t, s, UpsertInput{Context: "work", Name: "counter", Body: "one"})
		_, err := s.Upsert(UpsertInput{Context: "work", Name: "counter", Body: "two", IfVersion: version(99)})
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
		ok, err := s.Upsert(UpsertInput{Context: "work", Name: "counter", Body: "two", IfVersion: version(m.Version)})
		if err != nil {
			t.Fatalf("matching IfVersion failed: %v", err)
		}
		if ok.Version != 2 {
			t.Fatalf("version = %d, want 2", ok.Version)
		}
	})

	t.Run("missing context", func(t *testing.T) {
		s, _, _ := newTestStore(t)
		_, err := s.Upsert(UpsertInput{Context: "nope", Body: "x"})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if !strings.Contains(err.Error(), "mem context create nope") {
			t.Fatalf("error should tell the user how to fix it: %v", err)
		}
	})

	t.Run("empty context means global", func(t *testing.T) {
		s, _, _ := newTestStore(t)
		m := mustUpsert(t, s, UpsertInput{Body: "in global"})
		if m.Context != GlobalContext {
			t.Fatalf("context = %q, want global", m.Context)
		}
		got, err := s.Get("", m.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Body != "in global" {
			t.Fatalf("body = %q", got.Body)
		}
	})

	t.Run("oversized body", func(t *testing.T) {
		s, _, _ := newTestStore(t)
		mustCreateContext(t, s, "work", nil)
		big := strings.Repeat("x", maxBodySize+1)
		_, err := s.Upsert(UpsertInput{Context: "work", Body: big})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
		if _, err := s.Upsert(UpsertInput{Context: "work", Body: strings.Repeat("x", maxBodySize)}); err != nil {
			t.Fatalf("body at the limit must be accepted: %v", err)
		}
	})

	t.Run("append over the limit", func(t *testing.T) {
		s, _, _ := newTestStore(t)
		mustCreateContext(t, s, "work", nil)
		mustUpsert(t, s, UpsertInput{Context: "work", Name: "big", Body: strings.Repeat("x", maxBodySize)})
		_, err := s.Upsert(UpsertInput{Context: "work", Name: "big", Body: "y", Append: true})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})

	t.Run("id from another context is not found", func(t *testing.T) {
		s, _, _ := newTestStore(t)
		mustCreateContext(t, s, "a", nil)
		mustCreateContext(t, s, "b", nil)
		m := mustUpsert(t, s, UpsertInput{Context: "a", Body: "x"})
		_, err := s.Upsert(UpsertInput{Context: "b", ID: m.ID, Body: "y"})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

func TestDeleteMemory(t *testing.T) {
	s, _, dir := newTestStore(t)
	mustCreateContext(t, s, "work", nil)

	m := mustUpsert(t, s, UpsertInput{Context: "work", Name: "gone", Body: "x"})
	if err := s.Delete(m.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get("work", m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after delete = %v, want ErrNotFound", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "contexts", "work", "gone.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file still exists: %v", err)
	}
	if err := s.Delete(m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Delete = %v, want ErrNotFound", err)
	}

	def, err := s.Get("", DefaultFileName)
	if err != nil {
		t.Fatalf("Get default: %v", err)
	}
	if err := s.Delete(def.ID); !errors.Is(err, ErrReserved) {
		t.Fatalf("deleting default file = %v, want ErrReserved", err)
	}
	if err := s.Delete("../x"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Delete bad id = %v, want ErrInvalid", err)
	}
}

func TestGetByID(t *testing.T) {
	s, clock, _ := newTestStore(t)
	mustCreateContext(t, s, "alpha", nil)
	days := 5
	mustCreateContext(t, s, "beta", &days)

	m1 := mustUpsert(t, s, UpsertInput{Context: "alpha", Name: "doc1.md", Body: "hello alpha"})
	m2 := mustUpsert(t, s, UpsertInput{Context: "beta", Name: "doc2.md", Body: "hello beta"})

	got1, err := s.GetByID(m1.ID)
	if err != nil {
		t.Fatalf("GetByID m1: %v", err)
	}
	if got1.Context != "alpha" || got1.Body != "hello alpha" || got1.Size != len("hello alpha") {
		t.Fatalf("unexpected got1: %+v", got1)
	}

	got2, err := s.GetByID(m2.ID)
	if err != nil {
		t.Fatalf("GetByID m2: %v", err)
	}
	if got2.Context != "beta" {
		t.Fatalf("unexpected got2 context: %s", got2.Context)
	}

	// Unknown ID
	if _, err := s.GetByID("000000000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByID unknown = %v, want ErrNotFound", err)
	}

	// Advance clock past beta's expiry: GetByID should return ErrNotFound
	clock.Advance(6 * 24 * time.Hour)
	if _, err := s.GetByID(m2.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByID expired = %v, want ErrNotFound", err)
	}
}

func TestConflictErrorDetails(t *testing.T) {
	s, _, _ := newTestStore(t)
	mustCreateContext(t, s, "work", nil)
	m := mustUpsert(t, s, UpsertInput{Context: "work", Name: "doc.md", Body: "v1"})
	if m.Version != 1 {
		t.Fatalf("version = %d, want 1", m.Version)
	}

	badVer := 2
	_, err := s.Upsert(UpsertInput{Context: "work", ID: m.ID, IfVersion: &badVer, Body: "v2"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict, got: %v", err)
	}
	var conf *ConflictError
	if !errors.As(err, &conf) {
		t.Fatalf("expected *ConflictError, got: %T", err)
	}
	if conf.CurrentVersion != 1 || conf.TargetVersion != 2 {
		t.Fatalf("unexpected conf: %+v", conf)
	}
}

func TestListPageAnd5000Memories(t *testing.T) {
	s, clock, _ := newTestStore(t)
	mustCreateContext(t, s, "work", nil)

	// Create 5 memories with known names and times
	m1 := mustUpsert(t, s, UpsertInput{Context: "work", Name: "apple.md", Body: "fruit"})
	clock.Advance(time.Minute)
	m2 := mustUpsert(t, s, UpsertInput{Context: "work", Name: "banana.md", Body: "yellow fruit"})
	clock.Advance(time.Minute)
	m3 := mustUpsert(t, s, UpsertInput{Context: "work", Name: "cherry.md", Body: "red fruit"})

	page, err := s.ListPage("work", ListPageOptions{Limit: 2, Offset: 0, Sort: "name"})
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if page.Total != 3 || len(page.Items) != 2 {
		t.Fatalf("page.Total=%d, len(page.Items)=%d", page.Total, len(page.Items))
	}
	if page.Items[0].Name != "apple.md" || page.Items[1].Name != "banana.md" {
		t.Fatalf("unexpected page items: %+v", page.Items)
	}
	if page.Items[0].Size != len("fruit") {
		t.Fatalf("size = %d, want %d", page.Items[0].Size, len("fruit"))
	}

	// Sort updated (newest first)
	pageUp, err := s.ListPage("work", ListPageOptions{Sort: "updated"})
	if err != nil {
		t.Fatalf("ListPage updated: %v", err)
	}
	if pageUp.Items[0].ID != m3.ID || pageUp.Items[1].ID != m2.ID || pageUp.Items[2].ID != m1.ID {
		t.Fatalf("unexpected order for updated sort: %+v", pageUp.Items)
	}

	// Query filter (case-insensitive substring)
	pageQ, err := s.ListPage("work", ListPageOptions{Query: "ERR"})
	if err != nil {
		t.Fatalf("ListPage query: %v", err)
	}
	if pageQ.Total != 1 || len(pageQ.Items) != 1 || pageQ.Items[0].Name != "cherry.md" {
		t.Fatalf("unexpected query result: %+v", pageQ)
	}

	// 5,000 memories test
	mustCreateContext(t, s, "big", nil)
	start := time.Now()
	// Insert 5000 memories in batches of 500
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatalf("Begin tx: %v", err)
	}
	stmt, err := tx.Prepare(`INSERT INTO memories(id, context, name, has_name, hash, version, updated_at, updated_by, tags, size) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		t.Fatalf("Prepare stmt: %v", err)
	}
	defer stmt.Close()
	nowUnix := time.Now().Unix()
	for i := 0; i < 5000; i++ {
		id := fmt.Sprintf("%012d", i)
		name := fmt.Sprintf("doc-%04d.md", i)
		bodyLen := (i % 100) + 10
		if _, err := stmt.Exec(id, "big", name, 1, "dummyhash", 1, nowUnix+int64(i), "test", "[]", bodyLen); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	t.Logf("Inserted 5000 memories in %v", time.Since(start))

	queryStart := time.Now()
	page5000, err := s.ListPage("big", ListPageOptions{
		Query:  "doc-04",
		Limit:  50,
		Offset: 0,
		Sort:   "name",
	})
	elapsed := time.Since(queryStart)
	if err != nil {
		t.Fatalf("ListPage 5000: %v", err)
	}
	t.Logf("ListPage across 5000 memories took %v (total=%d, returned=%d)", elapsed, page5000.Total, len(page5000.Items))
	// "doc-04" matches doc-0400 through doc-0499 => 100 items
	if page5000.Total != 100 {
		t.Fatalf("Total = %d, want 100", page5000.Total)
	}
	if len(page5000.Items) != 50 {
		t.Fatalf("Items = %d, want 50", len(page5000.Items))
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("ListPage took too long: %v, want < 100ms", elapsed)
	}
}
