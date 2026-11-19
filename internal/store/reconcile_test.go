package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReconcileHandAddedFile(t *testing.T) {
	s, _, dir := newTestStore(t)
	mustCreateContext(t, s, "work", nil)

	body := "hand written note\n"
	path := filepath.Join(dir, "contexts", "work", "hand.md")
	writeRaw(t, path, body)

	res, err := s.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Added != 1 || res.Adopted != 1 {
		t.Fatalf("res = %+v, want 1 added and 1 adopted", res)
	}

	m, err := s.Get("work", "hand.md")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if m.Body != body {
		t.Fatalf("body = %q, want %q", m.Body, body)
	}
	if len(m.ID) != 12 {
		t.Fatalf("id = %q", m.ID)
	}
	if m.Version != 1 {
		t.Fatalf("version = %d, want 1", m.Version)
	}

	// Frontmatter must have been stamped back into the file.
	raw := readFile(t, path)
	if !strings.Contains(raw, "id: "+m.ID) || !strings.Contains(raw, "version: 1") {
		t.Fatalf("file was not adopted:\n%s", raw)
	}

	// Reconcile is idempotent.
	res, err = s.Reconcile()
	if err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	if res.Added != 0 || res.Updated != 0 || res.Removed != 0 || res.Adopted != 0 {
		t.Fatalf("second reconcile was not a no-op: %+v", res)
	}
}

func TestReconcileHandEditedFile(t *testing.T) {
	s, _, dir := newTestStore(t)
	mustCreateContext(t, s, "work", nil)
	m := mustUpsert(t, s, UpsertInput{Context: "work", Name: "edit", Body: "before\n"})

	path := filepath.Join(dir, "contexts", "work", "edit.md")
	meta, _ := splitFrontmatter([]byte(readFile(t, path)))
	edited := "---\nid: " + m.ID + "\nversion: 1\nupdated_at: 2026-01-02T03:04:05Z\n---\nafter\n"
	writeRaw(t, path, edited)

	res, err := s.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Updated != 1 {
		t.Fatalf("res = %+v, want 1 updated", res)
	}
	_ = meta

	got, err := s.Get("work", m.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Body != "after\n" {
		t.Fatalf("body = %q, want %q", got.Body, "after\n")
	}
	if got.Version != 2 {
		t.Fatalf("version = %d, want 2", got.Version)
	}
	want := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if !got.UpdatedAt.Equal(want) {
		t.Fatalf("UpdatedAt = %v, want %v (from frontmatter)", got.UpdatedAt, want)
	}
}

func TestReconcileUsesMtimeWithoutFrontmatterDate(t *testing.T) {
	s, _, dir := newTestStore(t)
	mustCreateContext(t, s, "work", nil)
	path := filepath.Join(dir, "contexts", "work", "plain.md")
	writeRaw(t, path, "no metadata\n")
	stamp := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	if _, err := s.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	got, err := s.Get("work", "plain.md")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.UpdatedAt.Equal(stamp) {
		t.Fatalf("UpdatedAt = %v, want file mtime %v", got.UpdatedAt, stamp)
	}
}

func TestReconcileHandDeletedFile(t *testing.T) {
	s, _, dir := newTestStore(t)
	mustCreateContext(t, s, "work", nil)
	m := mustUpsert(t, s, UpsertInput{Context: "work", Name: "vanish", Body: "x"})
	if err := os.Remove(filepath.Join(dir, "contexts", "work", "vanish.md")); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	res, err := s.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Removed != 1 {
		t.Fatalf("res = %+v, want 1 removed", res)
	}
	if _, err := s.Get("work", m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get = %v, want ErrNotFound", err)
	}
	if list, _ := s.List("work"); len(list) != 0 {
		t.Fatalf("list = %+v, want empty", list)
	}
}

func TestReconcileHandMadeFolder(t *testing.T) {
	s, _, dir := newTestStore(t)
	writeRaw(t, filepath.Join(dir, "contexts", "by-hand", "note.md"), "made by hand\n")
	writeRaw(t, filepath.Join(dir, "contexts", "BAD_SLUG", "note.md"), "cannot be a slug\n")
	writeRaw(t, filepath.Join(dir, "contexts", "nested", "deeper", "note.txt"), "txt counts too\n")

	res, err := s.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Contexts != 2 {
		t.Fatalf("contexts adopted = %d, want 2", res.Contexts)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "BAD_SLUG") {
		t.Fatalf("warnings = %v, want one about BAD_SLUG", res.Warnings)
	}

	c, err := s.GetContext("by-hand")
	if err != nil {
		t.Fatalf("GetContext: %v", err)
	}
	if c.ExpiresAt != nil {
		t.Fatalf("adopted contexts must be permanent, got expiry %v", c.ExpiresAt)
	}
	if c.MemoryCount != 1 {
		t.Fatalf("count = %d, want 1", c.MemoryCount)
	}

	txt, err := s.Get("nested", "deeper/note.txt")
	if err != nil {
		t.Fatalf("Get txt: %v", err)
	}
	if txt.Body != "txt counts too\n" {
		t.Fatalf("body = %q", txt.Body)
	}

	if _, err := s.Upsert(UpsertInput{Context: "BAD_SLUG", Body: "x"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad slug upsert = %v, want ErrInvalid", err)
	}
}

func TestReconcileIgnoresOtherFiles(t *testing.T) {
	s, _, dir := newTestStore(t)
	mustCreateContext(t, s, "work", nil)
	writeRaw(t, filepath.Join(dir, "contexts", "work", "image.png"), "not text")
	writeRaw(t, filepath.Join(dir, "contexts", "work", ".hidden.md"), "hidden")

	res, err := s.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Added != 0 {
		t.Fatalf("res = %+v, want nothing added", res)
	}
	if len(res.Warnings) != 1 {
		t.Fatalf("warnings = %v, want one for the hidden file", res.Warnings)
	}
}

func TestReconcileCrashHeal(t *testing.T) {
	s, _, dir := newTestStore(t)
	mustCreateContext(t, s, "work", nil)
	m := mustUpsert(t, s, UpsertInput{Context: "work", Name: "original", Body: "original body\n"})
	oldPath := filepath.Join(dir, "contexts", "work", "original.md")
	newPath := filepath.Join(dir, "contexts", "work", "renamed.md")

	// Simulate a crash during rename: the new file is written, the old file is
	// still there, and the index still points at the old name.
	data := readFile(t, oldPath)
	if err := os.WriteFile(newPath, []byte(data), 0o644); err != nil {
		t.Fatalf("write new path: %v", err)
	}
	if err := os.Remove(oldPath); err != nil {
		t.Fatalf("remove old path: %v", err)
	}

	res, err := s.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Added != 1 || res.Removed != 1 {
		t.Fatalf("res = %+v, want 1 added and 1 removed", res)
	}

	got, err := s.Get("work", m.ID)
	if err != nil {
		t.Fatalf("Get by id after heal: %v", err)
	}
	if got.Name != "renamed.md" {
		t.Fatalf("name = %q, want renamed.md", got.Name)
	}
	if got.Body != "original body\n" {
		t.Fatalf("body = %q", got.Body)
	}
	if list, _ := s.List("work"); len(list) != 1 {
		t.Fatalf("list = %+v, want one memory", list)
	}
}

func TestReconcileDetectsDuplicatedFile(t *testing.T) {
	s, _, dir := newTestStore(t)
	mustCreateContext(t, s, "work", nil)
	src := filepath.Join(dir, "contexts", "work", "a.md")
	writeRaw(t, src, "same content\n")
	if _, err := s.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// Copy the adopted file (same frontmatter id) under a new name. The index
	// must keep one file per row, not two rows with one id.
	data := readFile(t, src)
	writeRaw(t, filepath.Join(dir, "contexts", "work", "b.md"), data)

	if _, err := s.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	a, err := s.Get("work", "a.md")
	if err != nil {
		t.Fatalf("Get a: %v", err)
	}
	b, err := s.Get("work", "b.md")
	if err != nil {
		t.Fatalf("Get b: %v", err)
	}
	if a.ID == b.ID {
		t.Fatalf("duplicated file shares id %q", a.ID)
	}
}

func writeRaw(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}
