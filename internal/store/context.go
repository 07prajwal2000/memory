package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"
)

// CreateContext creates a context.
//
// An empty slug creates a temp context with an auto-generated "tmp-xxxx" slug;
// the expiry defaults to the configured value when expiryDays is nil. A named
// context with a nil expiryDays is permanent.
func (s *Store) CreateContext(slug string, expiryDays *int) (Context, error) {
	now := s.now().UTC()
	days := expiryDays
	if slug == "" {
		if days == nil {
			d := s.expiryDays
			days = &d
		}
	} else if err := ValidateSlug(slug); err != nil {
		return Context{}, err
	}
	if slug == GlobalContext {
		return Context{}, fmt.Errorf("%w: context %q always exists and cannot be created, deleted or expired", ErrReserved, GlobalContext)
	}
	if days != nil && *days <= 0 {
		return Context{}, fmt.Errorf("%w: expiry days must be positive, got %d", ErrInvalid, *days)
	}

	c := Context{CreatedAt: now}
	if days != nil {
		exp := now.AddDate(0, 0, *days)
		c.ExpiresAt = &exp
	}

	err := s.withTx(func(tx *sql.Tx) error {
		for attempt := 0; attempt < 8; attempt++ {
			candidate := slug
			if candidate == "" {
				suffix, err := randomString(4, slugAlphabet)
				if err != nil {
					return err
				}
				candidate = "tmp-" + suffix
			}
			var exists int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM contexts WHERE slug = ?`, candidate).Scan(&exists); err != nil {
				return fmt.Errorf("check context %q: %w", candidate, err)
			}
			if exists > 0 {
				if slug != "" {
					return fmt.Errorf("%w: context %q already exists", ErrExists, slug)
				}
				continue
			}
			c.Slug = candidate
			var expires any
			if c.ExpiresAt != nil {
				expires = c.ExpiresAt.Unix()
			}
			if _, err := tx.Exec(`INSERT INTO contexts(slug, created_at, expires_at) VALUES (?, ?, ?)`,
				c.Slug, c.CreatedAt.Unix(), expires); err != nil {
				return fmt.Errorf("insert context %q: %w", c.Slug, err)
			}
			return nil
		}
		return fmt.Errorf("%w: could not find a free temp context slug", ErrExists)
	})
	if err != nil {
		return Context{}, err
	}
	if err := os.MkdirAll(s.contextPath(c.Slug), 0o755); err != nil {
		return Context{}, fmt.Errorf("create context dir: %w", err)
	}
	return c, nil
}

// ListContexts returns every live context with its memory count.
func (s *Store) ListContexts() ([]Context, error) {
	if _, err := s.SweepExpired(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT c.slug, c.created_at, c.expires_at, COUNT(m.id)
		FROM contexts c LEFT JOIN memories m ON m.context = c.slug
		GROUP BY c.slug, c.created_at, c.expires_at
		ORDER BY c.slug`)
	if err != nil {
		return nil, fmt.Errorf("list contexts: %w", err)
	}
	defer rows.Close()
	var out []Context
	for rows.Next() {
		var (
			c       Context
			created int64
			expires sql.NullInt64
			count   int
		)
		if err := rows.Scan(&c.Slug, &created, &expires, &count); err != nil {
			return nil, fmt.Errorf("scan context row: %w", err)
		}
		c.CreatedAt = time.Unix(created, 0).UTC()
		if expires.Valid {
			t := time.Unix(expires.Int64, 0).UTC()
			c.ExpiresAt = &t
		}
		c.MemoryCount = count
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list contexts: %w", err)
	}
	return out, nil
}

// GetContext returns one context with its memory count.
func (s *Store) GetContext(slug string) (Context, error) {
	slug = s.resolveContext(slug)
	c, err := s.liveContext(s.db, slug)
	if err != nil {
		return Context{}, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE context = ?`, slug).Scan(&c.MemoryCount); err != nil {
		return Context{}, fmt.Errorf("count memories in %q: %w", slug, err)
	}
	return c, nil
}

// SetContextExpiry sets (days != nil) or clears (days == nil) a context's expiry.
func (s *Store) SetContextExpiry(slug string, days *int) error {
	slug = s.resolveContext(slug)
	if slug == GlobalContext {
		return fmt.Errorf("%w: context %q never expires", ErrReserved, GlobalContext)
	}
	if days != nil && *days <= 0 {
		return fmt.Errorf("%w: expiry days must be positive, got %d", ErrInvalid, *days)
	}
	return s.withTx(func(tx *sql.Tx) error {
		if _, err := s.liveContext(tx, slug); err != nil {
			return err
		}
		var expires any
		if days != nil {
			expires = s.now().UTC().AddDate(0, 0, *days).Unix()
		}
		res, err := tx.Exec(`UPDATE contexts SET expires_at = ? WHERE slug = ?`, expires, slug)
		if err != nil {
			return fmt.Errorf("set expiry for %q: %w", slug, err)
		}
		if n, err := res.RowsAffected(); err == nil && n == 0 {
			return contextMissing(slug)
		}
		return nil
	})
}

// DeleteContext removes a context with its memories and files.
func (s *Store) DeleteContext(slug string) error {
	slug = s.resolveContext(slug)
	if slug == GlobalContext {
		return fmt.Errorf("%w: context %q cannot be deleted", ErrReserved, GlobalContext)
	}
	if err := ValidateSlug(slug); err != nil {
		return err
	}
	return s.removeContext(slug)
}

// SweepExpired removes every expired context and returns the removed slugs.
func (s *Store) SweepExpired() ([]string, error) {
	now := s.now().UTC().Unix()
	rows, err := s.db.Query(`SELECT slug FROM contexts WHERE expires_at IS NOT NULL AND expires_at <= ? ORDER BY slug`, now)
	if err != nil {
		return nil, fmt.Errorf("find expired contexts: %w", err)
	}
	var slugs []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan expired context: %w", err)
		}
		slugs = append(slugs, slug)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("find expired contexts: %w", err)
	}
	removed := make([]string, 0, len(slugs))
	for _, slug := range slugs {
		err := s.removeContext(slug)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return removed, err
		}
		removed = append(removed, slug)
	}
	return removed, nil
}
