// Package store implements contexts and memories on disk with a SQLite
// metadata index. Markdown files are the source of truth; the index is
// derived and can be rebuilt with Reconcile.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/asg017/sqlite-vec-go-bindings/ncruces"
	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/driver"

	"memory/internal/config"
)

// Sentinel errors. Callers match them with errors.Is.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
	ErrInvalid  = errors.New("invalid")
	ErrReserved = errors.New("reserved")
	ErrExists   = errors.New("already exists")
)

// ConflictError is returned when an optimistic locking check fails.
type ConflictError struct {
	ID             string
	CurrentVersion int
	TargetVersion  int
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("conflict: memory %q is at version %d, not %d", e.ID, e.CurrentVersion, e.TargetVersion)
}

func (e *ConflictError) Is(target error) bool {
	return target == ErrConflict
}

// GlobalContext is the reserved context that always exists and never expires.
const GlobalContext = "global"

// Context is a container of memories.
type Context struct {
	Slug        string
	CreatedAt   time.Time
	ExpiresAt   *time.Time
	MemoryCount int
}

// Memory is one record: a file in a context plus its index metadata.
type Memory struct {
	ID        string
	Context   string
	Name      string
	Body      string
	Version   int
	UpdatedAt time.Time
	UpdatedBy string
	Tags      []string
	Size      int
	HasName   bool
}

// MemorySummary contains metadata for a memory without its body.
type MemorySummary struct {
	ID        string    `json:"id"`
	Context   string    `json:"context"`
	Name      string    `json:"name"`
	HasName   bool      `json:"has_name"`
	Version   int       `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
	UpdatedBy string    `json:"updated_by"`
	Tags      []string  `json:"tags"`
	Size      int       `json:"size"`
}

// MemoryPage is the result of ListPage.
type MemoryPage struct {
	Items []MemorySummary `json:"items"`
	Total int             `json:"total"`
}

// ListPageOptions configures a paged memory query.
type ListPageOptions struct {
	Query  string // case-insensitive substring of name
	Sort   string // "name" (default) or "updated"
	Limit  int    // default 200, max 1000
	Offset int    // >= 0
}

// UpsertInput describes a create-or-update.
type UpsertInput struct {
	Context   string
	ID        string
	Name      string
	Body      string
	UpdatedBy string
	Tags      []string
	Append    bool
	IfVersion *int
}

// Hooks are called after a change is committed, so indexers can react
// without observing rolled-back work.
type Hooks struct {
	OnUpsert        func(Memory)
	OnDelete        func(id, context string)
	OnContextDelete func(slug string)
}

// Store is a handle to a data directory. It is safe for concurrent use.
type Store struct {
	dataDir     string
	contextsDir string
	db          *sql.DB
	now         func() time.Time
	hooks       Hooks
	expiryDays  int
}

// Option configures Open.
type Option func(*Store)

// WithClock injects the clock used for every timestamp decision.
func WithClock(fn func() time.Time) Option {
	return func(s *Store) {
		if fn != nil {
			s.now = fn
		}
	}
}

// WithHooks installs change hooks.
func WithHooks(h Hooks) Option {
	return func(s *Store) { s.hooks = h }
}

// Open prepares the data directory, runs migrations, seeds the global context
// and the default file, and sweeps expired contexts.
func Open(cfg config.Config, opts ...Option) (*Store, error) {
	if cfg.DataDir == "" {
		return nil, fmt.Errorf("%w: data dir is empty", ErrInvalid)
	}
	s := &Store{
		dataDir:    cfg.DataDir,
		now:        time.Now,
		expiryDays: cfg.DefaultTempExpiryDays,
	}
	if s.expiryDays <= 0 {
		s.expiryDays = config.DefaultTempExpiryDays
	}
	for _, opt := range opts {
		opt(s)
	}
	s.contextsDir = filepath.Join(s.dataDir, "contexts")
	if err := os.MkdirAll(s.contextsDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	db, err := openDB(filepath.Join(s.dataDir, "index.db"))
	if err != nil {
		return nil, err
	}
	s.db = db
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.seed(); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := s.SweepExpired(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func openDB(file string) (*sql.DB, error) {
	dsn := "file:" + filepath.ToSlash(file) + "?_txlock=immediate"
	db, err := driver.Open(dsn, func(c *sqlite3.Conn) error {
		return c.Exec(`
			PRAGMA journal_mode=WAL;
			PRAGMA busy_timeout=5000;
			PRAGMA foreign_keys=ON;
			PRAGMA synchronous=NORMAL;
		`)
	})
	if err != nil {
		return nil, fmt.Errorf("open index %s: %w", file, err)
	}
	return db, nil
}

// Close releases the database handle.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version > len(migrations) {
		return fmt.Errorf("index schema version %d is newer than this build (%d)", version, len(migrations))
	}
	for i := version; i < len(migrations); i++ {
		tx, err := s.db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %d: %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", i+1, err)
		}
	}
	return nil
}

// querier is implemented by *sql.DB and *sql.Tx.
type querier interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

func (s *Store) withTx(fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// contextPath is the directory holding a context's files.
func (s *Store) contextPath(slug string) string {
	return filepath.Join(s.contextsDir, slug)
}

// memoryPath is the file backing a memory.
func (s *Store) memoryPath(slug, name string) string {
	return filepath.Join(s.contextsDir, slug, filepath.FromSlash(name))
}

func (s *Store) resolveContext(slug string) string {
	if slug == "" {
		return GlobalContext
	}
	return slug
}

func contextMissing(slug string) error {
	return fmt.Errorf("%w: context %q does not exist; create it with `mem context create %s`", ErrNotFound, slug, slug)
}

// expired reports whether a context has passed its expiry at the store clock.
func (s *Store) expired(expiresAt *time.Time) bool {
	return expiresAt != nil && !s.now().Before(*expiresAt)
}

// liveContext loads a context, treating an expired context as missing. Outside
// a transaction an expired context is also removed on the spot; inside one the
// removal is left to the next sweep, since the caller's transaction is about to
// fail with ErrNotFound and roll back anyway.
func (s *Store) liveContext(q querier, slug string) (Context, error) {
	var (
		c       Context
		created int64
		expires sql.NullInt64
	)
	err := q.QueryRow(`SELECT slug, created_at, expires_at FROM contexts WHERE slug = ?`, slug).
		Scan(&c.Slug, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return Context{}, contextMissing(slug)
	}
	if err != nil {
		return Context{}, fmt.Errorf("load context %q: %w", slug, err)
	}
	c.CreatedAt = time.Unix(created, 0).UTC()
	if expires.Valid {
		t := time.Unix(expires.Int64, 0).UTC()
		c.ExpiresAt = &t
	}
	if s.expired(c.ExpiresAt) {
		if _, inTx := q.(*sql.Tx); !inTx {
			if err := s.removeContext(slug); err != nil {
				return Context{}, err
			}
		}
		return Context{}, contextMissing(slug)
	}
	return c, nil
}

// removeContext deletes a context's files and row. Hooks fire after commit.
func (s *Store) removeContext(slug string) error {
	err := s.withTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`DELETE FROM contexts WHERE slug = ?`, slug)
		if err != nil {
			return fmt.Errorf("delete context %q: %w", slug, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("delete context %q: %w", slug, err)
		}
		if n == 0 {
			return contextMissing(slug)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := os.RemoveAll(s.contextPath(slug)); err != nil {
		return fmt.Errorf("remove context files %q: %w", slug, err)
	}
	if fn := s.hooks.OnContextDelete; fn != nil {
		fn(slug)
	}
	return nil
}

func encodeTags(tags []string) string {
	if len(tags) == 0 {
		return "[]"
	}
	data, err := json.Marshal(tags)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func decodeTags(raw string) []string {
	var tags []string
	if raw == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return nil
	}
	if len(tags) == 0 {
		return nil
	}
	return tags
}

// seed creates the global context and the default file on first open.
func (s *Store) seed() error {
	now := s.now().Unix()
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO contexts(slug, created_at, expires_at) VALUES (?, ?, NULL)`,
		GlobalContext, now); err != nil {
		return fmt.Errorf("seed global context: %w", err)
	}
	if err := os.MkdirAll(s.contextPath(GlobalContext), 0o755); err != nil {
		return fmt.Errorf("create global context dir: %w", err)
	}
	var exists int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE context = ? AND name = ?`,
		GlobalContext, DefaultFileName).Scan(&exists); err != nil {
		return fmt.Errorf("check default file: %w", err)
	}
	if exists > 0 {
		return nil
	}
	if _, err := os.Stat(s.memoryPath(GlobalContext, DefaultFileName)); err == nil {
		// A hand-written default file already exists: index it as-is rather than
		// overwriting whatever the user wrote.
		return s.withTx(func(tx *sql.Tx) error {
			_, err := s.adoptFile(tx, GlobalContext, DefaultFileName)
			return err
		})
	}
	_, err := s.Upsert(UpsertInput{
		Context:   GlobalContext,
		Name:      DefaultFileName,
		Body:      defaultFileBody,
		UpdatedBy: "mem",
		Tags:      []string{"system"},
	})
	return err
}

const defaultFileBody = `# Memory

This store keeps long-term memory for AI agents as markdown files. Every
memory lives in a context; the reserved context ` + "`global`" + ` is always
available and always searched.

## Using it

- ` + "`search`" + ` finds memories by meaning and by keyword. It searches the
  current context plus ` + "`global`" + ` unless you opt out.
- ` + "`get`" + ` with no arguments returns this file and the list of contexts.
- ` + "`get`" + ` with a context lists that context's memories; with an id or name
  it returns one memory.
- ` + "`upsert`" + ` creates or updates a memory and returns its id. Pass ` + "`append`" + `
  to add to the end instead of replacing, and ` + "`if_version`" + ` to avoid
  overwriting someone else's change.
- ` + "`delete`" + ` removes a memory by id.

## Conventions

- One topic per memory, short and specific: a decision, a preference, a fact
  about a codebase.
- Name memories by subject (` + "`auth/token-refresh.md`" + `) so they are easy to
  find; unnamed memories are stored under their id.
- Record *why* something is true, not just what.
- Update an existing memory instead of adding a near-duplicate.
`
