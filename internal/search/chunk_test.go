package search

import (
	"strings"
	"testing"
)

func TestChunker_Table(t *testing.T) {
	tests := []struct {
		name         string
		docName      string
		body         string
		minChunks    int
		maxChunks    int
		checkContent func(t *testing.T, chunks []Chunk)
	}{
		{
			name:      "empty body",
			docName:   "empty.md",
			body:      "",
			minChunks: 0,
			maxChunks: 0,
		},
		{
			name:      "whitespace only",
			docName:   "space.md",
			body:      "   \n\t\n   ",
			minChunks: 0,
			maxChunks: 0,
		},
		{
			name:      "crlf line endings",
			docName:   "crlf.md",
			body:      "# Title\r\n\r\nLine 1\r\nLine 2\r\n",
			minChunks: 1,
			maxChunks: 1,
			checkContent: func(t *testing.T, chunks []Chunk) {
				if chunks[0].Heading != "# Title" {
					t.Errorf("expected heading # Title, got %q", chunks[0].Heading)
				}
				if strings.Contains(chunks[0].Text, "\r") {
					t.Error("chunk text contains raw \\r")
				}
			},
		},
		{
			name:      "unicode support",
			docName:   "unicode.md",
			body:      "# 标题\n\n这是一个测试文档，包含中文字符和 Emoji 🚀。\n\n## 子标题\n\n更多内容。",
			minChunks: 2,
			maxChunks: 2,
			checkContent: func(t *testing.T, chunks []Chunk) {
				if chunks[0].Heading != "# 标题" {
					t.Errorf("expected # 标题, got %q", chunks[0].Heading)
				}
				if chunks[1].Heading != "# 标题 > ## 子标题" {
					t.Errorf("expected # 标题 > ## 子标题, got %q", chunks[1].Heading)
				}
				if !strings.Contains(chunks[0].Text, "🚀") {
					t.Error("expected emoji preserved in chunk text")
				}
			},
		},
		{
			name:      "only headings",
			docName:   "headings.md",
			body:      "# Header 1\n## Header 2\n### Header 3",
			minChunks: 1,
			maxChunks: 3,
			checkContent: func(t *testing.T, chunks []Chunk) {
				for _, c := range chunks {
					if c.Heading == "" {
						t.Error("expected non-empty heading")
					}
				}
			},
		},
		{
			name:    "nested code fences with fake markdown headings inside",
			docName: "code.md",
			body: `# Code Example

Here is some code:
` + "```python" + `
# This is a comment, NOT a markdown heading
def hello():
    print("world")
` + "```" + `

And another section:
## Section 2
Text in section 2.`,
			minChunks: 2,
			maxChunks: 2,
			checkContent: func(t *testing.T, chunks []Chunk) {
				if chunks[0].Heading != "# Code Example" {
					t.Errorf("expected # Code Example, got %q", chunks[0].Heading)
				}
				if !strings.Contains(chunks[0].Text, "def hello():") {
					t.Error("expected code block inside first chunk")
				}
				if chunks[1].Heading != "# Code Example > ## Section 2" {
					t.Errorf("expected section 2 heading path, got %q", chunks[1].Heading)
				}
			},
		},
		{
			name:      "huge single line",
			docName:   "huge.md",
			body:      strings.Repeat("word ", 1000), // ~5000 chars, no newlines
			minChunks: 3,
			maxChunks: 10,
			checkContent: func(t *testing.T, chunks []Chunk) {
				for i, c := range chunks {
					if len(c.Text) > 1500 {
						t.Errorf("chunk %d length %d exceeds MaxChars (1500)", i, len(c.Text))
					}
				}
			},
		},
		{
			name:    "heading hierarchy stack",
			docName: "hierarchy.md",
			body: `# Level 1
text 1

## Level 2A
text 2a

### Level 3
text 3

## Level 2B
text 2b`,
			minChunks: 4,
			maxChunks: 4,
			checkContent: func(t *testing.T, chunks []Chunk) {
				expected := []string{
					"# Level 1",
					"# Level 1 > ## Level 2A",
					"# Level 1 > ## Level 2A > ### Level 3",
					"# Level 1 > ## Level 2B",
				}
				for i, c := range chunks {
					if c.Heading != expected[i] {
						t.Errorf("chunk %d: expected heading %q, got %q", i, expected[i], c.Heading)
					}
				}
			},
		},
		{
			name:    "embed prefix formatting",
			docName: "auth/jwt.md",
			body:    "# Authentication\n\nJWT tokens should be signed with RS256.",
			minChunks: 1,
			maxChunks: 1,
			checkContent: func(t *testing.T, chunks []Chunk) {
				c := chunks[0]
				embedText := c.Embed()
				if !strings.HasPrefix(embedText, "auth/jwt.md > # Authentication\n\n") {
					t.Errorf("unexpected Embed() output: %q", embedText)
				}
				if !strings.Contains(embedText, "JWT tokens should be signed with RS256.") {
					t.Error("Embed() missing chunk body")
				}
			},
		},
	}

	opts := DefaultChunkOpts()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			chunks := Chunker(tc.docName, tc.body, opts)
			if len(chunks) < tc.minChunks || len(chunks) > tc.maxChunks {
				t.Fatalf("expected between %d and %d chunks, got %d", tc.minChunks, tc.maxChunks, len(chunks))
			}
			for i, c := range chunks {
				if c.Ord != i {
					t.Errorf("chunk %d has Ord=%d", i, c.Ord)
				}
			}
			if tc.checkContent != nil {
				tc.checkContent(t, chunks)
			}
		})
	}
}

func TestChunker_OverlapAndLimits(t *testing.T) {
	// Generate 10 paragraphs of 50 tokens each (total 500 tokens) in one section
	paras := make([]string, 10)
	for i := range paras {
		paras[i] = strings.Repeat("test sentence with multiple words for tokens. ", 5)
	}
	body := "# Long Section\n\n" + strings.Join(paras, "\n\n")

	opts := ChunkOpts{
		TargetTokens:  200,
		MaxTokens:     250,
		OverlapTokens: 40,
		MaxChars:      1000,
	}

	chunks := Chunker("long.md", body, opts)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}

	for i, c := range chunks {
		if len(c.Text) > opts.MaxChars {
			t.Errorf("chunk %d has %d chars, exceeds %d", i, len(c.Text), opts.MaxChars)
		}
	}
}
