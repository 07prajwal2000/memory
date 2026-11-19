package store

import (
	"errors"
	"testing"
)

func TestValidateSlug(t *testing.T) {
	tests := []struct {
		name string
		slug string
		ok   bool
	}{
		{"simple", "work", true},
		{"dashed", "client-a-2026", true},
		{"single char", "a", true},
		{"digits", "123", true},
		{"global is a valid shape", "global", true},
		{"32 chars", "abcdefghijklmnopqrstuvwxyz012345", true},
		{"33 chars", "abcdefghijklmnopqrstuvwxyz0123456", false},
		{"empty", "", false},
		{"leading dash", "-work", false},
		{"trailing dash", "work-", false},
		{"double dash", "a--b", false},
		{"uppercase", "Work", false},
		{"underscore", "work_1", false},
		{"space", "my work", false},
		{"slash", "a/b", false},
		{"dot", "a.b", false},
		{"traversal", "../x", false},
		{"unicode", "wörk", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSlug(tc.slug)
			if tc.ok && err != nil {
				t.Fatalf("ValidateSlug(%q) = %v, want nil", tc.slug, err)
			}
			if !tc.ok && !errors.Is(err, ErrInvalid) {
				t.Fatalf("ValidateSlug(%q) = %v, want ErrInvalid", tc.slug, err)
			}
		})
	}
}

func TestNormalizeName(t *testing.T) {
	long := ""
	for len(long) < 210 {
		long += "abcdefghij"
	}
	tests := []struct {
		name  string
		input string
		want  string
		ok    bool
	}{
		{"empty means unnamed", "", "", true},
		{"blank means unnamed", "   ", "", true},
		{"adds md", "notes", "notes.md", true},
		{"keeps extension", "notes.txt", "notes.txt", true},
		{"lowercases", "Notes", "notes.md", true},
		{"subfolders", "auth/tokens.md", "auth/tokens.md", true},
		{"trims segments", "  auth / tokens.md  ", "auth/tokens.md", true},
		{"leading slash", "/notes.md", "", false},
		{"parent traversal", "../x", "", false},
		{"nested traversal", "a/../../b", "", false},
		{"dot segment", "a/./b.md", "", false},
		{"windows drive", `C:\x`, "", false},
		{"backslash separator", `auth\tokens.md`, "", false},
		{"reserved con", "con", "", false},
		{"reserved nul with ext", "nul.md", "", false},
		{"reserved com1", "com1.md", "", false},
		{"trailing dot", "notes.", "", false},
		{"trailing dot in segment", "a.b./c.md", "", false},
		{"trailing space trimmed", "notes.md   ", "notes.md", true},
		{"inner space", "notes .md", "", false},
		{"hidden segment", ".git/config.md", "", false},
		{"unicode", "café.md", "", false},
		{"double slash", "a//b.md", "", false},
		{"too long", long + ".md", "", false},
		{"200 chars ok", repeat("a", 197) + ".md", repeat("a", 197) + ".md", true},
		{"201 chars", repeat("a", 198) + ".md", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeName(tc.input)
			if !tc.ok {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("NormalizeName(%q) = (%q, %v), want ErrInvalid", tc.input, got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeName(%q) = %v, want nil", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("NormalizeName(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}

func TestNewID(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 500; i++ {
		id, err := newID()
		if err != nil {
			t.Fatalf("newID: %v", err)
		}
		if len(id) != 12 {
			t.Fatalf("newID() = %q, want 12 chars", id)
		}
		if err := validateID(id); err != nil {
			t.Fatalf("validateID(%q) = %v", id, err)
		}
		if seen[id] {
			t.Fatalf("newID() produced duplicate %q", id)
		}
		seen[id] = true
	}
}

func TestFrontmatterRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		data string
		want fileMeta
		body string
	}{
		{
			name: "full block",
			data: "---\nid: abcdefghijkl\nversion: 4\nupdated_at: 2026-11-17T10:22:31Z\nupdated_by: claude\ntags: [auth, decision]\n---\nhello\n",
			want: fileMeta{present: true, ID: "abcdefghijkl", Version: 4, UpdatedBy: "claude", Tags: []string{"auth", "decision"}},
			body: "hello\n",
		},
		{
			name: "no frontmatter",
			data: "just text\n",
			want: fileMeta{},
			body: "just text\n",
		},
		{
			name: "unterminated block is plain text",
			data: "---\nid: abc\nno closing",
			want: fileMeta{},
			body: "---\nid: abc\nno closing",
		},
		{
			name: "empty tags",
			data: "---\nid: abcdefghijkl\nversion: 1\ntags: []\n---\n",
			want: fileMeta{present: true, ID: "abcdefghijkl", Version: 1},
			body: "",
		},
		{
			name: "quoted values",
			data: "---\nupdated_by: \"claude bot\"\ntags: [\"a, b\", plain]\n---\nx",
			want: fileMeta{present: true, UpdatedBy: "claude bot", Tags: []string{"a, b", "plain"}},
			body: "x",
		},
		{
			name: "unknown keys ignored",
			data: "---\nid: abcdefghijkl\nfuture: 1\n---\nbody",
			want: fileMeta{present: true, ID: "abcdefghijkl"},
			body: "body",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			meta, body := splitFrontmatter([]byte(tc.data))
			if meta.present != tc.want.present || meta.ID != tc.want.ID || meta.Version != tc.want.Version ||
				meta.UpdatedBy != tc.want.UpdatedBy {
				t.Fatalf("meta = %+v, want %+v", meta, tc.want)
			}
			if len(meta.Tags) != len(tc.want.Tags) {
				t.Fatalf("tags = %v, want %v", meta.Tags, tc.want.Tags)
			}
			for i := range meta.Tags {
				if meta.Tags[i] != tc.want.Tags[i] {
					t.Fatalf("tags = %v, want %v", meta.Tags, tc.want.Tags)
				}
			}
			if string(body) != tc.body {
				t.Fatalf("body = %q, want %q", body, tc.body)
			}
		})
	}
}

func TestFormatFileRoundTrip(t *testing.T) {
	m := Memory{
		ID:        "abcdefghijkl",
		Version:   3,
		UpdatedBy: "claude\nwith newline",
		Tags:      []string{"auth", "a, b"},
		Body:      "# title\n\nbody\n",
	}
	m.UpdatedAt = m.UpdatedAt.Add(0)
	data := formatFile(m)
	meta, body := splitFrontmatter(data)
	if meta.ID != m.ID || meta.Version != m.Version {
		t.Fatalf("meta = %+v", meta)
	}
	if meta.UpdatedBy != "claude with newline" {
		t.Fatalf("UpdatedBy = %q", meta.UpdatedBy)
	}
	if string(body) != m.Body {
		t.Fatalf("body = %q, want %q", body, m.Body)
	}
	if len(meta.Tags) != 2 || meta.Tags[0] != "auth" || meta.Tags[1] != "a, b" {
		t.Fatalf("tags = %v", meta.Tags)
	}
}
