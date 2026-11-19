package store

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ReconcileResult reports what disk -> index reconciliation changed.
type ReconcileResult struct {
	Added    int
	Updated  int
	Removed  int
	Contexts int
	Adopted  int
	Warnings []string
}

// Reconcile rebuilds the index from the files on disk. It heals a crash
// between the file rename and the index commit, and picks up files that were
// added, edited or removed by hand.
func (s *Store) Reconcile() (ReconcileResult, error) {
	var (
		res     ReconcileResult
		changed []Memory
		deleted []Memory
	)

	entries, err := os.ReadDir(s.contextsDir)
	if err != nil {
		return res, fmt.Errorf("read contexts dir: %w", err)
	}

	err = s.withTx(func(tx *sql.Tx) error {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			slug := entry.Name()
			if err := ValidateSlug(slug); err != nil {
				res.Warnings = append(res.Warnings, fmt.Sprintf("skipped directory %q: %v", slug, err))
				continue
			}
			if err := s.reconcileContext(tx, slug, &res, &changed, &deleted); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return ReconcileResult{}, err
	}

	sort.Slice(changed, func(i, j int) bool {
		if changed[i].Context != changed[j].Context {
			return changed[i].Context < changed[j].Context
		}
		return changed[i].Name < changed[j].Name
	})
	if s.hooks.OnUpsert != nil {
		for _, m := range changed {
			s.hooks.OnUpsert(m)
		}
	}
	if s.hooks.OnDelete != nil {
		for _, m := range deleted {
			s.hooks.OnDelete(m.ID, m.Context)
		}
	}
	return res, nil
}

func (s *Store) reconcileContext(tx *sql.Tx, slug string, res *ReconcileResult, changed, deleted *[]Memory) error {
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM contexts WHERE slug = ?`, slug).Scan(&exists); err != nil {
		return fmt.Errorf("load context %q: %w", slug, err)
	}
	if exists == 0 {
		if _, err := tx.Exec(`INSERT INTO contexts(slug, created_at, expires_at) VALUES (?, ?, NULL)`,
			slug, s.now().UTC().Unix()); err != nil {
			return fmt.Errorf("adopt context %q: %w", slug, err)
		}
		res.Contexts++
	}

	known := make(map[string]string)
	rows, err := tx.Query(`SELECT name, id FROM memories WHERE context = ?`, slug)
	if err != nil {
		return fmt.Errorf("list memories for %q: %w", slug, err)
	}
	for rows.Next() {
		var name, id string
		if err := rows.Scan(&name, &id); err != nil {
			rows.Close()
			return fmt.Errorf("list memories for %q: %w", slug, err)
		}
		known[name] = id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list memories for %q: %w", slug, err)
	}

	// Collect the files first: rows whose file is gone must be dropped before
	// any new row claims an id, so a crash-interrupted rename heals cleanly.
	type diskFile struct {
		name string
		path string
	}
	var files []diskFile
	seen := make(map[string]bool)
	err = filepath.WalkDir(s.contextPath(slug), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if ext != ".md" && ext != ".txt" {
			return nil
		}
		rel, err := filepath.Rel(s.contextPath(slug), p)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		if _, err := NormalizeName(name); err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("skipped %s/%s: %v", slug, name, err))
			return nil
		}
		seen[name] = true
		files = append(files, diskFile{name: name, path: p})
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk context %q: %w", slug, err)
	}

	for name, id := range known {
		if seen[name] {
			continue
		}
		if _, err := tx.Exec(`DELETE FROM memories WHERE id = ?`, id); err != nil {
			return fmt.Errorf("drop stale row %q: %w", id, err)
		}
		res.Removed++
		*deleted = append(*deleted, Memory{ID: id, Context: slug, Name: name})
	}

	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	for _, f := range files {
		if err := s.reconcileFile(tx, slug, f.name, f.path, res, changed); err != nil {
			return err
		}
	}
	return nil
}

// reconcileFile brings one file's row in line with its content.
func (s *Store) reconcileFile(tx *sql.Tx, slug, name, path string, res *ReconcileResult, changed *[]Memory) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", path, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	meta, body := splitFrontmatter(data)
	mtime := info.ModTime().UTC()
	if !meta.UpdatedAt.IsZero() {
		mtime = meta.UpdatedAt
	}
	hash := hashBody(string(body))

	var (
		curID      string
		curHash    string
		curVersion int
	)
	err = tx.QueryRow(`SELECT id, hash, version FROM memories WHERE context = ? AND name = ?`, slug, name).
		Scan(&curID, &curHash, &curVersion)
	rowExists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("load row for %s/%s: %w", slug, name, err)
	}

	if !rowExists {
		// Prefer the id in the file, but never reuse one that another index row
		// already holds (e.g. a file copied by hand): that would merge two files
		// into one memory. Fall back to a fresh id instead.
		id := meta.ID
		if validateID(id) != nil || !s.idAvailable(tx, id) {
			candidate, err := s.freeID(tx)
			if err != nil {
				return err
			}
			id = candidate
		}
		version := meta.Version
		if version < 1 {
			version = 1
		}
		m := Memory{
			ID:        id,
			Context:   slug,
			Name:      name,
			Body:      string(body),
			Version:   version,
			UpdatedAt: mtime,
			UpdatedBy: meta.UpdatedBy,
			Tags:      normalizeTags(meta.Tags),
			Size:      len(body),
			HasName:   hasName(name, id),
		}
		if _, err := tx.Exec(`INSERT INTO memories(id, context, name, has_name, hash, version, updated_at, updated_by, tags, size)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			m.ID, slug, name, boolInt(m.HasName), hash, version, mtime.Unix(), m.UpdatedBy,
			encodeTags(m.Tags), m.Size); err != nil {
			return fmt.Errorf("insert row for %s/%s: %w", slug, name, err)
		}
		if !meta.present || meta.ID != m.ID || meta.Version != version {
			if err := writeFileAtomic(path, formatFile(m)); err != nil {
				return err
			}
			res.Adopted++
		}
		res.Added++
		*changed = append(*changed, m)
		return nil
	}

	if curHash == hash {
		// Content unchanged. Ensure size is recorded (in case migrated from older index).
		_, _ = tx.Exec(`UPDATE memories SET size = ? WHERE id = ? AND size = 0`, len(body), curID)
		// Repair frontmatter that disagrees with the index
		// only when the file actually has a frontmatter block.
		if meta.present && (meta.ID != curID || meta.Version != curVersion) {
			m := Memory{ID: curID, Context: slug, Name: name, Body: string(body), Version: curVersion,
				UpdatedAt: mtime, UpdatedBy: meta.UpdatedBy, Tags: normalizeTags(meta.Tags), Size: len(body), HasName: hasName(name, curID)}
			if err := writeFileAtomic(path, formatFile(m)); err != nil {
				return err
			}
			res.Adopted++
		}
		return nil
	}

	version := curVersion + 1
	if meta.Version > version {
		version = meta.Version
	}
	m := Memory{
		ID:        curID,
		Context:   slug,
		Name:      name,
		Body:      string(body),
		Version:   version,
		UpdatedAt: mtime,
		UpdatedBy: meta.UpdatedBy,
		Tags:      normalizeTags(meta.Tags),
		Size:      len(body),
		HasName:   hasName(name, curID),
	}
	if _, err := tx.Exec(`UPDATE memories SET hash = ?, version = ?, updated_at = ?, updated_by = ?, tags = ?, size = ? WHERE id = ?`,
		hash, version, mtime.Unix(), m.UpdatedBy, encodeTags(m.Tags), m.Size, curID); err != nil {
		return fmt.Errorf("update row for %s/%s: %w", slug, name, err)
	}
	res.Updated++
	*changed = append(*changed, m)
	return nil
}

// idAvailable reports whether no index row uses this id.
func (s *Store) idAvailable(tx *sql.Tx, id string) bool {
	var used int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM memories WHERE id = ?`, id).Scan(&used); err != nil {
		return false
	}
	return used == 0
}

// freeID returns an unused memory id.
func (s *Store) freeID(tx *sql.Tx) (string, error) {
	for attempt := 0; attempt < 8; attempt++ {
		id, err := newID()
		if err != nil {
			return "", err
		}
		var used int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM memories WHERE id = ?`, id).Scan(&used); err != nil {
			return "", fmt.Errorf("check memory id: %w", err)
		}
		if used == 0 {
			return id, nil
		}
	}
	return "", fmt.Errorf("%w: could not find a free memory id", ErrExists)
}
