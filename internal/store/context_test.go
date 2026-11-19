package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCreateContext(t *testing.T) {
	days7 := 7

	t.Run("permanent named", func(t *testing.T) {
		s, _, _ := newTestStore(t)
		c := mustCreateContext(t, s, "work", nil)
		if c.Slug != "work" {
			t.Fatalf("slug = %q", c.Slug)
		}
		if c.ExpiresAt != nil {
			t.Fatalf("ExpiresAt = %v, want nil", c.ExpiresAt)
		}
		if !c.CreatedAt.Equal(testEpoch) {
			t.Fatalf("CreatedAt = %v", c.CreatedAt)
		}
	})

	t.Run("temp with default expiry", func(t *testing.T) {
		s, _, _ := newTestStore(t)
		c := mustCreateContext(t, s, "scratch", nil)
		if c.ExpiresAt != nil {
			t.Fatalf("named context with nil days must be permanent, got %v", c.ExpiresAt)
		}
		c = mustCreateContext(t, s, "", nil)
		if !strings.HasPrefix(c.Slug, "tmp-") || len(c.Slug) != 8 {
			t.Fatalf("auto slug = %q, want tmp-xxxx", c.Slug)
		}
		if c.ExpiresAt == nil {
			t.Fatal("auto context must expire")
		}
		want := testEpoch.AddDate(0, 0, 7)
		if !c.ExpiresAt.Equal(want) {
			t.Fatalf("ExpiresAt = %v, want %v", c.ExpiresAt, want)
		}
	})

	t.Run("custom expiry", func(t *testing.T) {
		s, _, _ := newTestStore(t)
		c := mustCreateContext(t, s, "short", &days7)
		if c.ExpiresAt == nil {
			t.Fatal("expected an expiry")
		}
		if want := testEpoch.AddDate(0, 0, 7); !c.ExpiresAt.Equal(want) {
			t.Fatalf("ExpiresAt = %v, want %v", c.ExpiresAt, want)
		}
	})

	t.Run("auto slugs do not collide", func(t *testing.T) {
		s, _, _ := newTestStore(t)
		seen := map[string]bool{}
		for i := 0; i < 20; i++ {
			c := mustCreateContext(t, s, "", nil)
			if seen[c.Slug] {
				t.Fatalf("duplicate auto slug %q", c.Slug)
			}
			seen[c.Slug] = true
		}
	})

	t.Run("invalid and reserved", func(t *testing.T) {
		s, _, _ := newTestStore(t)
		tests := []struct {
			name string
			slug string
			days *int
			want error
		}{
			{"global", "global", nil, ErrReserved},
			{"bad slug", "Bad_Slug", nil, ErrInvalid},
			{"traversal", "../x", nil, ErrInvalid},
			{"zero days", "z", intPtr(0), ErrInvalid},
			{"negative days", "n", intPtr(-3), ErrInvalid},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				if _, err := s.CreateContext(tc.slug, tc.days); !errors.Is(err, tc.want) {
					t.Fatalf("err = %v, want %v", err, tc.want)
				}
			})
		}
	})

	t.Run("duplicate name", func(t *testing.T) {
		s, _, _ := newTestStore(t)
		mustCreateContext(t, s, "dup", nil)
		if _, err := s.CreateContext("dup", nil); !errors.Is(err, ErrExists) {
			t.Fatalf("err = %v, want ErrExists", err)
		}
	})
}

func intPtr(v int) *int { return &v }

func TestListAndGetContext(t *testing.T) {
	s, _, _ := newTestStore(t)
	mustCreateContext(t, s, "b-work", nil)
	mustCreateContext(t, s, "a-personal", nil)
	mustUpsert(t, s, UpsertInput{Context: "b-work", Name: "one", Body: "1"})
	mustUpsert(t, s, UpsertInput{Context: "b-work", Name: "two", Body: "2"})

	list, err := s.ListContexts()
	if err != nil {
		t.Fatalf("ListContexts: %v", err)
	}
	byName := map[string]Context{}
	for _, c := range list {
		byName[c.Slug] = c
	}
	if len(list) != 3 {
		t.Fatalf("got %d contexts, want 3 (global + 2)", len(list))
	}
	if byName["b-work"].MemoryCount != 2 {
		t.Fatalf("b-work count = %d, want 2", byName["b-work"].MemoryCount)
	}
	if byName["a-personal"].MemoryCount != 0 {
		t.Fatalf("a-personal count = %d, want 0", byName["a-personal"].MemoryCount)
	}
	if _, ok := byName[GlobalContext]; !ok {
		t.Fatal("global context missing from the list")
	}

	got, err := s.GetContext("b-work")
	if err != nil {
		t.Fatalf("GetContext: %v", err)
	}
	if got.MemoryCount != 2 {
		t.Fatalf("count = %d", got.MemoryCount)
	}

	if _, err := s.GetContext("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestSetContextExpiry(t *testing.T) {
	s, clock, _ := newTestStore(t)
	mustCreateContext(t, s, "work", nil)

	if err := s.SetContextExpiry("work", intPtr(3)); err != nil {
		t.Fatalf("SetContextExpiry: %v", err)
	}
	c, err := s.GetContext("work")
	if err != nil {
		t.Fatalf("GetContext: %v", err)
	}
	if c.ExpiresAt == nil || !c.ExpiresAt.Equal(testEpoch.AddDate(0, 0, 3)) {
		t.Fatalf("ExpiresAt = %v", c.ExpiresAt)
	}

	// Back to permanent.
	if err := s.SetContextExpiry("work", nil); err != nil {
		t.Fatalf("SetContextExpiry(nil): %v", err)
	}
	c, err = s.GetContext("work")
	if err != nil {
		t.Fatalf("GetContext: %v", err)
	}
	if c.ExpiresAt != nil {
		t.Fatalf("ExpiresAt = %v, want nil", c.ExpiresAt)
	}

	// Extending from the current time, not the creation time.
	clock.Advance(48 * time.Hour)
	if err := s.SetContextExpiry("work", intPtr(1)); err != nil {
		t.Fatalf("SetContextExpiry: %v", err)
	}
	c, _ = s.GetContext("work")
	want := clock.Now().AddDate(0, 0, 1)
	if !c.ExpiresAt.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want %v", c.ExpiresAt, want)
	}

	if err := s.SetContextExpiry(GlobalContext, intPtr(1)); !errors.Is(err, ErrReserved) {
		t.Fatalf("global expiry = %v, want ErrReserved", err)
	}
	if err := s.SetContextExpiry("missing", intPtr(1)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing context = %v, want ErrNotFound", err)
	}
	if err := s.SetContextExpiry("work", intPtr(0)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero days = %v, want ErrInvalid", err)
	}
}

func TestDeleteContext(t *testing.T) {
	s, _, dir := newTestStore(t)
	mustCreateContext(t, s, "doomed", nil)
	mustUpsert(t, s, UpsertInput{Context: "doomed", Name: "a", Body: "1"})
	mustUpsert(t, s, UpsertInput{Context: "doomed", Name: "sub/b", Body: "2"})

	if err := s.DeleteContext("doomed"); err != nil {
		t.Fatalf("DeleteContext: %v", err)
	}
	if _, err := s.GetContext("doomed"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetContext = %v, want ErrNotFound", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "contexts", "doomed")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("context dir still exists: %v", err)
	}
	if err := s.DeleteContext("doomed"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}
	if err := s.DeleteContext(GlobalContext); !errors.Is(err, ErrReserved) {
		t.Fatalf("deleting global = %v, want ErrReserved", err)
	}
	if err := s.DeleteContext(""); !errors.Is(err, ErrReserved) {
		t.Fatalf("deleting empty (global) = %v, want ErrReserved", err)
	}

	// Memories of a deleted context are gone with it.
	if _, err := s.Get("", "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("orphaned memory = %v, want ErrNotFound", err)
	}
}

func TestExpiryLazyAndSweep(t *testing.T) {
	s, clock, dir := newTestStore(t)
	mustCreateContext(t, s, "temp", intPtr(1))
	mustUpsert(t, s, UpsertInput{Context: "temp", Name: "note", Body: "x"})
	clock.Advance(12 * time.Hour)

	if _, err := s.GetContext("temp"); err != nil {
		t.Fatalf("context should still be live: %v", err)
	}

	clock.Advance(13 * time.Hour) // 25h total, past the 24h expiry

	// Lazy removal on access.
	if _, err := s.GetContext("temp"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetContext = %v, want ErrNotFound", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "contexts", "temp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired context files remain: %v", err)
	}
	if _, err := s.Get("temp", "note"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get in expired context = %v, want ErrNotFound", err)
	}
	if _, err := s.Upsert(UpsertInput{Context: "temp", Body: "y"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Upsert in expired context = %v, want ErrNotFound", err)
	}

	// Sweep removes what access has not touched yet.
	mustCreateContext(t, s, "temp2", intPtr(1))
	clock.Advance(25 * time.Hour)
	removed, err := s.SweepExpired()
	if err != nil {
		t.Fatalf("SweepExpired: %v", err)
	}
	if len(removed) != 1 || removed[0] != "temp2" {
		t.Fatalf("removed = %v, want [temp2]", removed)
	}
	if list, _ := s.ListContexts(); len(list) != 1 || list[0].Slug != GlobalContext {
		t.Fatalf("contexts after sweep = %+v", list)
	}
}

func TestExpiredContextInsideWriteTransaction(t *testing.T) {
	s, clock, dir := newTestStore(t)
	mustCreateContext(t, s, "temp", intPtr(1))
	mustUpsert(t, s, UpsertInput{Context: "temp", Name: "note", Body: "x"})
	clock.Advance(25 * time.Hour)

	// An upsert starts a write transaction and then has to learn the context is
	// gone. It must return ErrNotFound instead of deadlocking or purging from
	// inside the transaction.
	done := make(chan error, 1)
	go func() {
		_, err := s.Upsert(UpsertInput{Context: "temp", Body: "y"})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Upsert on an expired context hung")
	}

	// The lazy purge still happens on the next non-transactional read.
	if _, err := s.GetContext("temp"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetContext = %v, want ErrNotFound", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "contexts", "temp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired context files remain: %v", err)
	}
}

func TestGlobalNeverExpires(t *testing.T) {
	s, clock, _ := newTestStore(t)
	mustUpsert(t, s, UpsertInput{Body: "kept"})
	clock.Advance(365 * 24 * time.Hour)
	if _, err := s.Get("", "readme.md"); err != nil {
		t.Fatalf("default file expired: %v", err)
	}
	removed, err := s.SweepExpired()
	if err != nil {
		t.Fatalf("SweepExpired: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("global was swept: %v", removed)
	}
}
