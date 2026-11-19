package store

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultFileName is the memory seeded into the global context on first open.
const DefaultFileName = "readme.md"

// maxBodySize is the largest accepted memory body.
const maxBodySize = 1 << 20

var (
	slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	idPattern   = regexp.MustCompile(`^[a-z0-9]{12}$`)
)

// windowsReserved are device names Windows refuses (or silently reinterprets)
// as file names, with or without an extension.
var windowsReserved = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// ValidateSlug reports whether s is a legal context slug.
func ValidateSlug(s string) error {
	if s == "" {
		return fmt.Errorf("%w: context slug is empty", ErrInvalid)
	}
	if len(s) > 32 {
		return fmt.Errorf("%w: context slug %q is longer than 32 characters", ErrInvalid, s)
	}
	if !slugPattern.MatchString(s) {
		return fmt.Errorf("%w: context slug %q must match %s", ErrInvalid, s, slugPattern)
	}
	return nil
}

// NormalizeName canonicalizes a memory name. It returns "" for an empty input,
// which means "unnamed". The result is safe to use as a relative path.
func NormalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil
	}
	// Reject characters that never survive normalization instead of silently
	// rewriting them: a caller asking for "C:\x" or "Notes" deserves an error.
	name = strings.ToLower(name)
	if strings.ContainsAny(name, "\\:") {
		return "", fmt.Errorf("%w: memory name %q must not contain %q", ErrInvalid, name, `\:`)
	}
	segments := strings.Split(name, "/")
	for i, seg := range segments {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			return "", fmt.Errorf("%w: memory name %q has an empty path segment", ErrInvalid, name)
		}
		if seg == "." || seg == ".." {
			return "", fmt.Errorf("%w: memory name %q must not contain %q segments", ErrInvalid, name, seg)
		}
		if strings.HasPrefix(seg, ".") {
			return "", fmt.Errorf("%w: memory name %q has a segment starting with '.'", ErrInvalid, name)
		}
		if strings.HasSuffix(seg, ".") {
			return "", fmt.Errorf("%w: memory name %q has a segment ending with '.'", ErrInvalid, name)
		}
		if base, _, _ := strings.Cut(seg, "."); windowsReserved[base] {
			return "", fmt.Errorf("%w: memory name %q uses reserved device name %q", ErrInvalid, name, base)
		}
		for _, r := range seg {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			default:
				return "", fmt.Errorf("%w: memory name %q contains disallowed character %q", ErrInvalid, name, r)
			}
		}
		segments[i] = seg
	}
	name = strings.Join(segments, "/")
	if path.Ext(name) == "" {
		name += ".md"
	}
	if len(name) > 200 {
		return "", fmt.Errorf("%w: memory name is longer than 200 characters", ErrInvalid)
	}
	return name, nil
}

// validateID reports whether id has the generated identifier shape.
func validateID(id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("%w: invalid memory id %q", ErrInvalid, id)
	}
	return nil
}

var idEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func randomString(n int, alphabet string) (string, error) {
	if alphabet == "" {
		return "", errors.New("empty alphabet")
	}
	out := make([]byte, 0, n)
	buf := make([]byte, n)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("read random: %w", err)
		}
		for _, b := range buf {
			out = append(out, alphabet[int(b)%len(alphabet)])
			if len(out) == n {
				break
			}
		}
	}
	return string(out), nil
}

// newID returns a fresh 12 character lowercase identifier.
func newID() (string, error) {
	b := make([]byte, 9)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	s := strings.ToLower(idEncoding.EncodeToString(b))
	return s[:12], nil
}

const slugAlphabet = "0123456789abcdefghijklmnopqrstuvwxyz"

// fileMeta is the parsed YAML frontmatter of a memory file.
type fileMeta struct {
	present   bool
	ID        string
	Version   int
	UpdatedAt time.Time
	UpdatedBy string
	Tags      []string
}

// splitFrontmatter separates the optional frontmatter block from the body.
// Files without a well-formed block are treated as plain text.
func splitFrontmatter(data []byte) (fileMeta, []byte) {
	var meta fileMeta
	line, rest, ok := cutLine(data)
	if !ok || strings.TrimRight(string(line), "\r") != "---" {
		return meta, data
	}
	var block []string
	for {
		line, next, ok := cutLine(rest)
		if !ok {
			return fileMeta{}, data
		}
		if strings.TrimRight(string(line), "\r") == "---" {
			meta.present = true
			meta.parse(strings.Join(block, "\n"))
			return meta, next
		}
		block = append(block, string(line))
		rest = next
	}
}

func cutLine(data []byte) (line, rest []byte, ok bool) {
	i := bytes.IndexByte(data, '\n')
	if i < 0 {
		return nil, nil, false
	}
	return data[:i], data[i+1:], true
}

func (m *fileMeta) parse(block string) {
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "id":
			m.ID = unquote(value)
		case "version":
			if v, err := strconv.Atoi(value); err == nil && v > 0 {
				m.Version = v
			}
		case "updated_at":
			if t, err := time.Parse(time.RFC3339, unquote(value)); err == nil {
				m.UpdatedAt = t.UTC()
			}
		case "updated_by":
			m.UpdatedBy = unquote(value)
		case "tags":
			m.Tags = parseTags(value)
		}
	}
}

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		if v, err := strconv.Unquote(s); err == nil {
			return v
		}
		return s[1 : len(s)-1]
	}
	return s
}

func parseTags(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		value = value[1 : len(value)-1]
	}
	var (
		tags    []string
		cur     strings.Builder
		quoted  bool
		escaped bool
	)
	flush := func() {
		tag := strings.TrimSpace(cur.String())
		cur.Reset()
		if tag == "" {
			return
		}
		tags = append(tags, unquote(tag))
	}
	for _, r := range value {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\' && quoted:
			cur.WriteRune(r)
			escaped = true
		case r == '"':
			quoted = !quoted
			cur.WriteRune(r)
		case r == ',' && !quoted:
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return tags
}

func formatTags(tags []string) string {
	parts := make([]string, len(tags))
	for i, t := range tags {
		if t == "" || strings.ContainsAny(t, ",[]\"") || strings.TrimSpace(t) != t {
			parts[i] = strconv.Quote(t)
		} else {
			parts[i] = t
		}
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// formatFile renders a memory as frontmatter plus body.
func formatFile(m Memory) []byte {
	var b bytes.Buffer
	b.WriteString("---\n")
	b.WriteString("id: " + m.ID + "\n")
	b.WriteString("version: " + strconv.Itoa(m.Version) + "\n")
	b.WriteString("updated_at: " + m.UpdatedAt.UTC().Format(time.RFC3339) + "\n")
	if by := singleLine(m.UpdatedBy); by != "" {
		b.WriteString("updated_by: " + by + "\n")
	}
	if len(m.Tags) > 0 {
		b.WriteString("tags: " + formatTags(m.Tags) + "\n")
	}
	b.WriteString("---\n")
	b.WriteString(m.Body)
	return b.Bytes()
}

func singleLine(s string) string {
	s = strings.TrimSpace(s)
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}

// hashBody is the change detector stored in the index.
func hashBody(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// normalizeTags trims and de-duplicates tags while preserving order.
func normalizeTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(tags))
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// writeFileAtomic writes data to path via a temp file in the same directory and
// a rename, which is atomic on every supported platform (on Windows os.Rename
// replaces an existing file via MoveFileEx).
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, ".mem-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("write temp file %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync temp file %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp file %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}
