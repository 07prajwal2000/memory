package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const memoryColumns = `id, context, name, has_name, version, updated_at, updated_by, tags, size`

func scanMemoryRow(scan func(...any) error) (Memory, error) {
	var (
		m       Memory
		hasName int
		updated int64
		tags    string
	)
	if err := scan(&m.ID, &m.Context, &m.Name, &hasName, &m.Version, &updated, &m.UpdatedBy, &tags, &m.Size); err != nil {
		return Memory{}, err
	}
	m.UpdatedAt = time.Unix(updated, 0).UTC()
	m.Tags = decodeTags(tags)
	m.HasName = (hasName != 0)
	return m, nil
}

// loadIndexed loads a memory row by id, or by name when id is empty.
func loadIndexed(q querier, slug, id, name string) (Memory, error) {
	var row *sql.Row
	if id != "" {
		row = q.QueryRow(`SELECT `+memoryColumns+` FROM memories WHERE context = ? AND id = ?`, slug, id)
	} else {
		row = q.QueryRow(`SELECT `+memoryColumns+` FROM memories WHERE context = ? AND name = ?`, slug, name)
	}
	m, err := scanMemoryRow(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Memory{}, ErrNotFound
	}
	if err != nil {
		return Memory{}, fmt.Errorf("load memory row: %w", err)
	}
	return m, nil
}

// readBody returns the body of the file backing a memory. A missing file yields
// an empty body: the markdown file is the source of truth, so a vanished file
// means the content is gone and the next write recreates it.
func (s *Store) readBody(slug, name string) (string, error) {
	data, err := os.ReadFile(s.memoryPath(slug, name))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read memory file: %w", err)
	}
	_, body := splitFrontmatter(data)
	return string(body), nil
}

func equalTags(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Upsert creates or updates a memory and returns it, including its id.
//
// Resolution order inside the resolved context:
//  1. ID set: that memory must exist; a differing Name renames it.
//  2. Name set: the memory with that name is updated, or created.
//  3. Neither: an unnamed memory is created under "<id>.md".
func (s *Store) Upsert(in UpsertInput) (Memory, error) {
	m, _, err := s.UpsertEx(in)
	return m, err
}

// UpsertEx is like Upsert but also reports whether the memory was created.
func (s *Store) UpsertEx(in UpsertInput) (Memory, bool, error) {
	slug := s.resolveContext(in.Context)
	if err := ValidateSlug(slug); err != nil {
		return Memory{}, false, err
	}
	if in.ID != "" {
		if err := validateID(in.ID); err != nil {
			return Memory{}, false, err
		}
	}
	var name string
	if in.Name != "" {
		n, err := NormalizeName(in.Name)
		if err != nil {
			return Memory{}, false, err
		}
		name = n
	}
	if len(in.Body) > maxBodySize {
		return Memory{}, false, fmt.Errorf("%w: body is %d bytes; the limit is %d", ErrInvalid, len(in.Body), maxBodySize)
	}
	tags := normalizeTags(in.Tags)

	var (
		out       Memory
		committed bool
		created   bool
	)
	err := s.withTx(func(tx *sql.Tx) error {
		if _, err := s.liveContext(tx, slug); err != nil {
			return err
		}

		cur, err := s.findCurrent(tx, slug, in.ID, name)
		if err != nil {
			if errors.Is(err, ErrNotFound) && in.ID != "" {
				return fmt.Errorf("%w: memory %q does not exist in context %q", ErrNotFound, in.ID, slug)
			}
			return err
		}
		if cur.ID == "" {
			m, err := s.insertMemory(tx, slug, name, in, tags)
			if err != nil {
				return err
			}
			out, committed, created = m, true, true
			return nil
		}

		if in.IfVersion != nil && *in.IfVersion != cur.Version {
			return &ConflictError{
				ID:             cur.ID,
				CurrentVersion: cur.Version,
				TargetVersion:  *in.IfVersion,
			}
		}

		newName := cur.Name
		if name != "" && name != cur.Name {
			if err := s.ensureNameFree(tx, slug, name, cur.ID); err != nil {
				return err
			}
			newName = name
		}

		newBody := in.Body
		if in.Append {
			newBody = cur.Body
			if newBody != "" && !strings.HasSuffix(newBody, "\n") {
				newBody += "\n"
			}
			newBody += in.Body
			if len(newBody) > maxBodySize {
				return fmt.Errorf("%w: body after append is %d bytes; the limit is %d", ErrInvalid, len(newBody), maxBodySize)
			}
		}
		newTags := cur.Tags
		if in.Tags != nil {
			newTags = tags
		}
		newBy := cur.UpdatedBy
		if in.UpdatedBy != "" {
			newBy = in.UpdatedBy
		}

		if newBody == cur.Body && equalTags(newTags, cur.Tags) && newName == cur.Name {
			out = cur
			out.Body = cur.Body
			out.Size = len(cur.Body)
			out.HasName = hasName(out.Name, out.ID)
			return nil
		}

		next := Memory{
			ID:        cur.ID,
			Context:   slug,
			Name:      newName,
			Body:      newBody,
			Version:   cur.Version + 1,
			UpdatedAt: s.now().UTC(),
			UpdatedBy: newBy,
			Tags:      newTags,
			Size:      len(newBody),
			HasName:   hasName(newName, cur.ID),
		}
		if err := writeFileAtomic(s.memoryPath(slug, newName), formatFile(next)); err != nil {
			return err
		}
		if newName != cur.Name {
			if err := os.Remove(s.memoryPath(slug, cur.Name)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove old memory file: %w", err)
			}
		}
		if _, err := tx.Exec(`UPDATE memories SET name = ?, has_name = ?, hash = ?, version = ?, updated_at = ?, updated_by = ?, tags = ?, size = ? WHERE id = ?`,
			next.Name, boolInt(next.HasName), hashBody(next.Body), next.Version,
			next.UpdatedAt.Unix(), next.UpdatedBy, encodeTags(next.Tags), next.Size, next.ID); err != nil {
			return fmt.Errorf("update memory row: %w", err)
		}
		out, committed = next, true
		return nil
	})
	if err != nil {
		return Memory{}, false, err
	}
	if committed && s.hooks.OnUpsert != nil {
		s.hooks.OnUpsert(out)
	}
	return out, created, nil
}

// findCurrent returns the memory an upsert targets, or a zero Memory with a nil
// error when one should be created. An unindexed file at the target name is
// adopted rather than overwritten, so losing the index never loses content.
func (s *Store) findCurrent(tx *sql.Tx, slug, id, name string) (Memory, error) {
	if id == "" && name == "" {
		return Memory{}, nil
	}
	m, err := loadIndexed(tx, slug, id, name)
	if err == nil {
		body, err := s.readBody(slug, m.Name)
		if err != nil {
			return Memory{}, err
		}
		m.Body = body
		return m, nil
	}
	if !errors.Is(err, ErrNotFound) || id != "" {
		return Memory{}, err
	}
	return s.adoptFile(tx, slug, name)
}

// adoptFile indexes an existing file that has no row yet.
func (s *Store) adoptFile(tx *sql.Tx, slug, name string) (Memory, error) {
	path := s.memoryPath(slug, name)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Memory{}, nil
	}
	if err != nil {
		return Memory{}, fmt.Errorf("read memory file: %w", err)
	}
	meta, body := splitFrontmatter(data)
	id := meta.ID
	if validateID(id) != nil {
		if id, err = s.freeID(tx); err != nil {
			return Memory{}, err
		}
	}
	version := meta.Version
	if version < 1 {
		version = 1
	}
	updated := meta.UpdatedAt
	if updated.IsZero() {
		if info, statErr := os.Stat(path); statErr == nil {
			updated = info.ModTime().UTC()
		} else {
			updated = s.now().UTC()
		}
	}
	m := Memory{
		ID:        id,
		Context:   slug,
		Name:      name,
		Body:      string(body),
		Version:   version,
		UpdatedAt: updated,
		UpdatedBy: meta.UpdatedBy,
		Tags:      normalizeTags(meta.Tags),
		Size:      len(body),
		HasName:   hasName(name, id),
	}
	if _, err := tx.Exec(`INSERT INTO memories(id, context, name, has_name, hash, version, updated_at, updated_by, tags, size)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, slug, name, boolInt(m.HasName), hashBody(m.Body), version, updated.Unix(),
		m.UpdatedBy, encodeTags(m.Tags), m.Size); err != nil {
		return Memory{}, fmt.Errorf("adopt memory file %q: %w", name, err)
	}
	return m, nil
}

// insertMemory writes a brand new memory file and row.
func (s *Store) insertMemory(tx *sql.Tx, slug, name string, in UpsertInput, tags []string) (Memory, error) {
	id := in.ID
	if id == "" {
		var err error
		if id, err = s.freeID(tx); err != nil {
			return Memory{}, err
		}
	}
	fileName := name
	if fileName == "" {
		fileName = id + ".md"
	}
	by := in.UpdatedBy
	if by == "" {
		by = "unknown"
	}
	m := Memory{
		ID:        id,
		Context:   slug,
		Name:      fileName,
		Body:      in.Body,
		Version:   1,
		UpdatedAt: s.now().UTC(),
		UpdatedBy: by,
		Tags:      tags,
		Size:      len(in.Body),
		HasName:   hasName(fileName, id),
	}
	if err := writeFileAtomic(s.memoryPath(slug, fileName), formatFile(m)); err != nil {
		return Memory{}, err
	}
	if _, err := tx.Exec(`INSERT INTO memories(id, context, name, has_name, hash, version, updated_at, updated_by, tags, size)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, slug, fileName, boolInt(m.HasName), hashBody(m.Body), m.Version,
		m.UpdatedAt.Unix(), m.UpdatedBy, encodeTags(m.Tags), m.Size); err != nil {
		return Memory{}, fmt.Errorf("insert memory row: %w", err)
	}
	return m, nil
}

// ensureNameFree reports ErrExists when a rename target is already taken, either
// in the index or by a file that Reconcile has not adopted yet. Checking the
// disk too means a rename can never silently overwrite someone's file.
func (s *Store) ensureNameFree(tx *sql.Tx, slug, name, selfID string) error {
	var taken int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM memories WHERE context = ? AND name = ? AND id <> ?`, slug, name, selfID).Scan(&taken); err != nil {
		return fmt.Errorf("check target name: %w", err)
	}
	if taken > 0 {
		return fmt.Errorf("%w: memory name %q is already used in context %q", ErrExists, name, slug)
	}
	if _, err := os.Stat(s.memoryPath(slug, name)); err == nil {
		return fmt.Errorf("%w: file %q already exists in context %q", ErrExists, name, slug)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("check target file: %w", err)
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// hasName reports whether the file name was chosen by the caller rather than
// derived from the id.
func hasName(name, id string) bool {
	return name != id+".md"
}

// Get returns a memory by id (checked first) or by name, within a context.
func (s *Store) Get(contextSlug, idOrName string) (Memory, error) {
	slug := s.resolveContext(contextSlug)
	if err := ValidateSlug(slug); err != nil {
		return Memory{}, err
	}
	if idOrName == "" {
		return Memory{}, fmt.Errorf("%w: memory id or name is required", ErrInvalid)
	}
	if _, err := s.liveContext(s.db, slug); err != nil {
		return Memory{}, err
	}

	m, err := Memory{}, error(ErrNotFound)
	if idPattern.MatchString(idOrName) {
		m, err = loadIndexed(s.db, slug, idOrName, "")
	}
	if errors.Is(err, ErrNotFound) {
		name, nameErr := NormalizeName(idOrName)
		if nameErr != nil {
			return Memory{}, fmt.Errorf("%w: memory %q does not exist in context %q", ErrNotFound, idOrName, slug)
		}
		m, err = loadIndexed(s.db, slug, "", name)
	}
	if errors.Is(err, ErrNotFound) {
		return Memory{}, fmt.Errorf("%w: memory %q does not exist in context %q", ErrNotFound, idOrName, slug)
	}
	if err != nil {
		return Memory{}, err
	}
	body, err := s.readBody(slug, m.Name)
	if err != nil {
		return Memory{}, err
	}
	m.Body = body
	m.Size = len(body)
	m.HasName = hasName(m.Name, m.ID)
	return m, nil
}

// GetByID returns a memory by its unique id across all live contexts.
func (s *Store) GetByID(id string) (Memory, error) {
	if err := validateID(id); err != nil {
		return Memory{}, err
	}
	var slug string
	err := s.db.QueryRow(`SELECT context FROM memories WHERE id = ?`, id).Scan(&slug)
	if errors.Is(err, sql.ErrNoRows) {
		return Memory{}, fmt.Errorf("%w: memory %q does not exist", ErrNotFound, id)
	}
	if err != nil {
		return Memory{}, fmt.Errorf("lookup memory %q context: %w", id, err)
	}
	if _, err := s.liveContext(s.db, slug); err != nil {
		return Memory{}, err
	}
	return s.Get(slug, id)
}

// List returns the metadata of a context's memories without bodies, newest first.
func (s *Store) List(contextSlug string) ([]Memory, error) {
	slug := s.resolveContext(contextSlug)
	if err := ValidateSlug(slug); err != nil {
		return nil, err
	}
	if _, err := s.liveContext(s.db, slug); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT `+memoryColumns+` FROM memories WHERE context = ? ORDER BY updated_at DESC, name ASC`, slug)
	if err != nil {
		return nil, fmt.Errorf("list memories in %q: %w", slug, err)
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		m, err := scanMemoryRow(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan memory row: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list memories in %q: %w", slug, err)
	}
	return out, nil
}

// ListPage returns a filtered, sorted, paginated list of memory summaries.
func (s *Store) ListPage(contextSlug string, opts ListPageOptions) (MemoryPage, error) {
	slug := s.resolveContext(contextSlug)
	if err := ValidateSlug(slug); err != nil {
		return MemoryPage{}, err
	}
	if _, err := s.liveContext(s.db, slug); err != nil {
		return MemoryPage{}, err
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 200
	} else if limit > 1000 {
		limit = 1000
	}
	offset := opts.Offset
	if offset < 0 {
		offset = 0
	}

	orderBy := "name ASC"
	if opts.Sort == "updated" {
		orderBy = "updated_at DESC, name ASC"
	}

	var (
		totalQuery string
		itemsQuery string
		args       []any
	)

	q := strings.TrimSpace(opts.Query)
	if q != "" {
		totalQuery = `SELECT COUNT(*) FROM memories WHERE context = ? AND instr(lower(name), lower(?)) > 0`
		itemsQuery = `SELECT id, context, name, has_name, version, updated_at, updated_by, tags, size
			FROM memories
			WHERE context = ? AND instr(lower(name), lower(?)) > 0
			ORDER BY ` + orderBy + `
			LIMIT ? OFFSET ?`
		args = []any{slug, q}
	} else {
		totalQuery = `SELECT COUNT(*) FROM memories WHERE context = ?`
		itemsQuery = `SELECT id, context, name, has_name, version, updated_at, updated_by, tags, size
			FROM memories
			WHERE context = ?
			ORDER BY ` + orderBy + `
			LIMIT ? OFFSET ?`
		args = []any{slug}
	}

	var total int
	if err := s.db.QueryRow(totalQuery, args...).Scan(&total); err != nil {
		return MemoryPage{}, fmt.Errorf("count memories in %q: %w", slug, err)
	}

	queryArgs := append(args, limit, offset)
	rows, err := s.db.Query(itemsQuery, queryArgs...)
	if err != nil {
		return MemoryPage{}, fmt.Errorf("list paged memories in %q: %w", slug, err)
	}
	defer rows.Close()

	items := make([]MemorySummary, 0)
	for rows.Next() {
		var (
			sum     MemorySummary
			hasName int
			updated int64
			tags    string
		)
		if err := rows.Scan(&sum.ID, &sum.Context, &sum.Name, &hasName, &sum.Version, &updated, &sum.UpdatedBy, &tags, &sum.Size); err != nil {
			return MemoryPage{}, fmt.Errorf("scan memory summary: %w", err)
		}
		sum.HasName = (hasName != 0)
		sum.UpdatedAt = time.Unix(updated, 0).UTC()
		sum.Tags = decodeTags(tags)
		items = append(items, sum)
	}
	if err := rows.Err(); err != nil {
		return MemoryPage{}, fmt.Errorf("list paged memories in %q: %w", slug, err)
	}

	return MemoryPage{
		Items: items,
		Total: total,
	}, nil
}

// Delete removes a memory by id. The default file is protected.
func (s *Store) Delete(id string) error {
	if err := validateID(id); err != nil {
		return err
	}
	var (
		slug string
		name string
	)
	err := s.withTx(func(tx *sql.Tx) error {
		err := tx.QueryRow(`SELECT context, name FROM memories WHERE id = ?`, id).Scan(&slug, &name)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: memory %q does not exist", ErrNotFound, id)
		}
		if err != nil {
			return fmt.Errorf("load memory %q: %w", id, err)
		}
		if slug == GlobalContext && name == DefaultFileName {
			return fmt.Errorf("%w: the default file %q cannot be deleted", ErrReserved, DefaultFileName)
		}
		if _, err := s.liveContext(tx, slug); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM memories WHERE id = ?`, id); err != nil {
			return fmt.Errorf("delete memory %q: %w", id, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := os.Remove(s.memoryPath(slug, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove memory file: %w", err)
	}
	if s.hooks.OnDelete != nil {
		s.hooks.OnDelete(id, slug)
	}
	return nil
}
