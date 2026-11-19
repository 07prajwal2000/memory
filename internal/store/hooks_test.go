package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"memory/internal/config"
)

// recorder collects hook calls in order.
type recorder struct {
	mu      sync.Mutex
	upserts []Memory
	deletes []string
	ctxDels []string
}

func (r *recorder) hooks() Hooks {
	return Hooks{
		OnUpsert: func(m Memory) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.upserts = append(r.upserts, m)
		},
		OnDelete: func(id, context string) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.deletes = append(r.deletes, id+"/"+context)
		},
		OnContextDelete: func(slug string) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.ctxDels = append(r.ctxDels, slug)
		},
	}
}

func (r *recorder) upsertCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.upserts)
}

func TestHooks(t *testing.T) {
	rec := &recorder{}
	s, _, _ := newTestStore(t, WithHooks(rec.hooks()))
	mustCreateContext(t, s, "work", nil)

	// The seed write during Open fires an upsert hook as well, so measure the
	// delta from here on.
	base := rec.upsertCount()

	m := mustUpsert(t, s, UpsertInput{Context: "work", Name: "hooked", Body: "v1"})
	if got := rec.upsertCount(); got != base+1 {
		t.Fatalf("upsert hooks = %d, want %d", got, base+1)
	}
	rec.mu.Lock()
	last := rec.upserts[len(rec.upserts)-1]
	rec.mu.Unlock()
	if last.ID != m.ID || last.Body != "v1" {
		t.Fatalf("hook payload = %+v", last)
	}

	// A no-op write must not fire.
	mustUpsert(t, s, UpsertInput{Context: "work", Name: "hooked", Body: "v1"})
	if got := rec.upsertCount(); got != base+1 {
		t.Fatalf("no-op fired a hook: %d upserts", got-base)
	}

	// A failed write must not fire.
	if _, err := s.Upsert(UpsertInput{Context: "work", Name: "hooked", Body: "v2", IfVersion: intPtr(99)}); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if got := rec.upsertCount(); got != base+1 {
		t.Fatalf("failed upsert fired a hook: %d upserts", got-base)
	}

	if err := s.Delete(m.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	rec.mu.Lock()
	if len(rec.deletes) != 1 || rec.deletes[0] != m.ID+"/work" {
		t.Fatalf("delete hooks = %v", rec.deletes)
	}
	rec.mu.Unlock()

	// Deleting a missing memory fires nothing.
	if err := s.Delete(m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	rec.mu.Lock()
	if len(rec.deletes) != 1 {
		t.Fatalf("failed delete fired a hook: %v", rec.deletes)
	}
	rec.mu.Unlock()

	if err := s.DeleteContext("work"); err != nil {
		t.Fatalf("DeleteContext: %v", err)
	}
	rec.mu.Lock()
	if len(rec.ctxDels) != 1 || rec.ctxDels[0] != "work" {
		t.Fatalf("context delete hooks = %v", rec.ctxDels)
	}
	rec.mu.Unlock()
}

func TestHooksOnReconcile(t *testing.T) {
	rec := &recorder{}
	dir := t.TempDir()
	s, err := Open(config.Config{DataDir: dir, DefaultTempExpiryDays: 7}, WithClock(func() time.Time { return testEpoch }), WithHooks(rec.hooks()))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	writeRaw(t, filepath.Join(dir, "contexts", "manual", "note.md"), "adopted\n")
	rec.mu.Lock()
	rec.upserts = nil
	rec.mu.Unlock()

	if _, err := s.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.upserts) != 1 || rec.upserts[0].Body != "adopted\n" {
		t.Fatalf("reconcile upsert hooks = %+v", rec.upserts)
	}
}

func TestConcurrentStoresSameDataDir(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{DataDir: dir, DefaultTempExpiryDays: 7}
	open := func() *Store {
		s, err := Open(cfg, WithClock(func() time.Time { return testEpoch }))
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		return s
	}
	a, b := open(), open()
	defer a.Close()
	defer b.Close()

	if _, err := a.CreateContext("shared", nil); err != nil {
		t.Fatalf("CreateContext: %v", err)
	}

	const (
		workers    = 8
		perWorker  = 10
		namePrefix = "counter"
	)
	var (
		wg   sync.WaitGroup
		errs = make(chan error, workers*2)
	)
	for w := 0; w < workers; w++ {
		store := a
		if w%2 == 1 {
			store = b
		}
		wg.Add(1)
		go func(w int, s *Store) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				name := fmt.Sprintf("%s-%d", namePrefix, w%2)
				if _, err := s.Upsert(UpsertInput{
					Context: "shared",
					Name:    name,
					Body:    fmt.Sprintf("worker %d iteration %d", w, i),
					Append:  true,
				}); err != nil {
					errs <- err
					return
				}
			}
		}(w, store)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	// Both writers target the same two names, so versions must exactly match
	// the number of committed writes, with nothing lost.
	list, err := a.List("shared")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	byName := map[string]Memory{}
	for _, m := range list {
		byName[m.Name] = m
	}
	total := 0
	for name, m := range byName {
		total += m.Version
		got, err := a.Get("shared", name)
		if err != nil {
			t.Fatalf("Get %s: %v", name, err)
		}
		lines := 0
		for _, line := range splitLines(got.Body) {
			if line != "" {
				lines++
			}
		}
		if lines != m.Version {
			t.Fatalf("%s has %d lines but version %d", name, lines, m.Version)
		}
	}
	if total != workers*perWorker {
		t.Fatalf("total versions = %d, want %d", total, workers*perWorker)
	}

	// Nothing may be left behind as a stray file.
	names := []string{}
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) != 2 {
		t.Fatalf("names = %v, want exactly two", names)
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func TestConcurrentIfVersionConflicts(t *testing.T) {
	s, _, _ := newTestStore(t)
	mustCreateContext(t, s, "work", nil)
	m := mustUpsert(t, s, UpsertInput{Context: "work", Name: "race", Body: "start"})

	const workers = 8
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		conflicts int
		wins      int
	)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v := m.Version
			_, err := s.Upsert(UpsertInput{
				Context:   "work",
				Name:      "race",
				Body:      fmt.Sprintf("writer %d", i),
				IfVersion: &v,
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case errors.Is(err, ErrConflict):
				conflicts++
			case err == nil:
				wins++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if wins != 1 {
		t.Fatalf("wins = %d, want exactly 1", wins)
	}
	if conflicts != workers-1 {
		t.Fatalf("conflicts = %d, want %d", conflicts, workers-1)
	}
	got, err := s.Get("work", "race")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Version != m.Version+1 {
		t.Fatalf("version = %d, want %d", got.Version, m.Version+1)
	}
}
