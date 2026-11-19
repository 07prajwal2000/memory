package search

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

type Chunk struct {
	Ord       int
	Heading   string
	Text      string
	StartLine int
	EndLine   int
	DocName   string
}

func (c Chunk) Embed() string {
	var prefix string
	if c.DocName != "" && c.Heading != "" {
		prefix = c.DocName + " > " + c.Heading + "\n\n"
	} else if c.DocName != "" {
		prefix = c.DocName + "\n\n"
	} else if c.Heading != "" {
		prefix = c.Heading + "\n\n"
	}
	return prefix + c.Text
}

type ChunkOpts struct {
	TargetTokens  int // default ~300
	MaxTokens     int // default ~400
	OverlapTokens int // default ~40
	MaxChars      int // default ~1500
}

func DefaultChunkOpts() ChunkOpts {
	return ChunkOpts{
		TargetTokens:  300,
		MaxTokens:     400,
		OverlapTokens: 40,
		MaxChars:      1500,
	}
}

func EstimateTokens(text string) int {
	words := len(strings.Fields(text))
	chars := utf8.RuneCountInString(text)
	tChars := (chars + 3) / 4
	tWords := int(float64(words) * 1.3)
	if tChars > tWords {
		return tChars
	}
	return tWords
}

type lineInfo struct {
	text   string
	lineNo int // 1-indexed
}

type section struct {
	heading   string
	lines     []lineInfo
	startLine int
	endLine   int
}

func Chunker(name, body string, opts ChunkOpts) []Chunk {
	if opts.TargetTokens <= 0 {
		opts.TargetTokens = 300
	}
	if opts.MaxTokens <= 0 {
		opts.MaxTokens = 400
	}
	if opts.OverlapTokens < 0 {
		opts.OverlapTokens = 40
	}
	if opts.MaxChars <= 0 {
		opts.MaxChars = 1500
	}

	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")

	if strings.TrimSpace(body) == "" {
		return nil
	}

	rawLines := strings.Split(body, "\n")
	sections := parseSections(rawLines)
	if len(sections) == 0 {
		return nil
	}

	var allChunks []Chunk
	ord := 0

	for _, sec := range sections {
		secChunks := splitSection(name, sec, opts)
		for _, sc := range secChunks {
			sc.Ord = ord
			ord++
			allChunks = append(allChunks, sc)
		}
	}

	return allChunks
}

func parseSections(rawLines []string) []section {
	var sections []section
	var currentSection section
	var headingStack []struct {
		level int
		text  string
	}

	inCodeFence := false
	fenceMarker := ""

	flushCurrent := func() {
		for len(currentSection.lines) > 0 && strings.TrimSpace(currentSection.lines[len(currentSection.lines)-1].text) == "" {
			currentSection.lines = currentSection.lines[:len(currentSection.lines)-1]
		}
		if len(currentSection.lines) > 0 {
			currentSection.startLine = currentSection.lines[0].lineNo
			currentSection.endLine = currentSection.lines[len(currentSection.lines)-1].lineNo
			sections = append(sections, currentSection)
			currentSection = section{}
		}
	}

	for idx, lineStr := range rawLines {
		lineNo := idx + 1
		trimmed := strings.TrimSpace(lineStr)

		// Check code fence toggle
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			marker := trimmed[:3]
			if !inCodeFence {
				inCodeFence = true
				fenceMarker = marker
			} else if strings.HasPrefix(trimmed, fenceMarker) {
				inCodeFence = false
				fenceMarker = ""
			}
			currentSection.lines = append(currentSection.lines, lineInfo{text: lineStr, lineNo: lineNo})
			continue
		}

		if inCodeFence {
			currentSection.lines = append(currentSection.lines, lineInfo{text: lineStr, lineNo: lineNo})
			continue
		}

		// Heading detection
		if level, headingText, isHeading := parseHeading(lineStr); isHeading {
			flushCurrent()

			// Pop heading stack to level - 1
			for len(headingStack) > 0 && headingStack[len(headingStack)-1].level >= level {
				headingStack = headingStack[:len(headingStack)-1]
			}
			headingStack = append(headingStack, struct {
				level int
				text  string
			}{level: level, text: headingText})

			// Build heading path
			parts := make([]string, len(headingStack))
			for i, h := range headingStack {
				parts[i] = h.text
			}
			currentSection.heading = strings.Join(parts, " > ")
			currentSection.lines = append(currentSection.lines, lineInfo{text: lineStr, lineNo: lineNo})
			continue
		}

		// Regular line
		if len(currentSection.lines) == 0 && currentSection.heading == "" && len(headingStack) > 0 {
			parts := make([]string, len(headingStack))
			for i, h := range headingStack {
				parts[i] = h.text
			}
			currentSection.heading = strings.Join(parts, " > ")
		}
		currentSection.lines = append(currentSection.lines, lineInfo{text: lineStr, lineNo: lineNo})
	}

	flushCurrent()
	return sections
}

func parseHeading(line string) (int, string, bool) {
	trimmed := strings.TrimLeftFunc(line, unicode.IsSpace)
	if !strings.HasPrefix(trimmed, "#") {
		return 0, "", false
	}

	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level > 6 || level == len(trimmed) {
		return 0, "", false
	}
	if trimmed[level] != ' ' && trimmed[level] != '\t' {
		return 0, "", false
	}

	return level, strings.TrimSpace(trimmed), true
}

func splitSection(docName string, sec section, opts ChunkOpts) []Chunk {
	// First collect text
	allText := joinLines(sec.lines)
	if strings.TrimSpace(allText) == "" {
		return nil
	}

	tokens := EstimateTokens(allText)
	if tokens <= opts.TargetTokens && len(allText) <= opts.MaxChars {
		return []Chunk{{
			Heading:   sec.heading,
			Text:      allText,
			StartLine: sec.startLine,
			EndLine:   sec.endLine,
			DocName:   docName,
		}}
	}

	// Group lines into paragraphs / code blocks
	type block struct {
		isCodeBlock bool
		lines       []lineInfo
	}
	var blocks []block
	var curBlock block
	inFence := false
	fenceMarker := ""

	for _, l := range sec.lines {
		trimmed := strings.TrimSpace(l.text)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			marker := trimmed[:3]
			if !inFence {
				if len(curBlock.lines) > 0 {
					blocks = append(blocks, curBlock)
					curBlock = block{}
				}
				inFence = true
				fenceMarker = marker
				curBlock.isCodeBlock = true
				curBlock.lines = append(curBlock.lines, l)
			} else if strings.HasPrefix(trimmed, fenceMarker) {
				curBlock.lines = append(curBlock.lines, l)
				blocks = append(blocks, curBlock)
				curBlock = block{}
				inFence = false
				fenceMarker = ""
			} else {
				curBlock.lines = append(curBlock.lines, l)
			}
			continue
		}

		if inFence {
			curBlock.lines = append(curBlock.lines, l)
			continue
		}

		if trimmed == "" {
			if len(curBlock.lines) > 0 {
				curBlock.lines = append(curBlock.lines, l)
				blocks = append(blocks, curBlock)
				curBlock = block{}
			}
			continue
		}

		curBlock.lines = append(curBlock.lines, l)
	}
	if len(curBlock.lines) > 0 {
		blocks = append(blocks, curBlock)
	}

	// Pack blocks into chunks
	var chunks []Chunk
	var currentLines []lineInfo

	flushChunk := func() {
		if len(currentLines) == 0 {
			return
		}
		txt := joinLines(currentLines)
		if strings.TrimSpace(txt) == "" {
			currentLines = nil
			return
		}
		chunks = append(chunks, Chunk{
			Heading:   sec.heading,
			Text:      txt,
			StartLine: currentLines[0].lineNo,
			EndLine:   currentLines[len(currentLines)-1].lineNo,
			DocName:   docName,
		})
		currentLines = nil
	}

	for _, b := range blocks {
		blockText := joinLines(b.lines)
		blockTokens := EstimateTokens(blockText)

		// Check if adding this block exceeds limit
		curText := joinLines(currentLines)
		candidateText := curText
		if candidateText != "" {
			candidateText += "\n"
		}
		candidateText += blockText

		candTokens := EstimateTokens(candidateText)

		if len(currentLines) > 0 && (candTokens > opts.TargetTokens || len(candidateText) > opts.MaxChars) {
			// Flush current chunk with overlap
			overlapLines := pickOverlapLines(currentLines, opts.OverlapTokens)
			flushChunk()
			currentLines = append(currentLines, overlapLines...)
		}

		// If block alone exceeds MaxChars or MaxTokens, split block line-by-line or char-by-char
		if blockTokens > opts.MaxTokens || len(blockText) > opts.MaxChars {
			for _, l := range b.lines {
				curText = joinLines(currentLines)
				cand := curText
				if cand != "" {
					cand += "\n"
				}
				cand += l.text
				if len(currentLines) > 0 && (EstimateTokens(cand) > opts.TargetTokens || len(cand) > opts.MaxChars) {
					overlapLines := pickOverlapLines(currentLines, opts.OverlapTokens)
					flushChunk()
					currentLines = append(currentLines, overlapLines...)
				}

				// If single line exceeds MaxChars, slice it
				if len(l.text) > opts.MaxChars {
					lineRunes := []rune(l.text)
					for len(lineRunes) > 0 {
						chunkSize := opts.MaxChars
						if chunkSize > len(lineRunes) {
							chunkSize = len(lineRunes)
						}
						currentLines = append(currentLines, lineInfo{text: string(lineRunes[:chunkSize]), lineNo: l.lineNo})
						flushChunk()
						lineRunes = lineRunes[chunkSize:]
					}
				} else {
					currentLines = append(currentLines, l)
				}
			}
		} else {
			currentLines = append(currentLines, b.lines...)
		}
	}

	flushChunk()

	// Merge tiny trailing chunk if < 50 tokens and combined <= MaxChars
	if len(chunks) > 1 {
		last := chunks[len(chunks)-1]
		prev := chunks[len(chunks)-2]
		if EstimateTokens(last.Text) < 50 && len(prev.Text)+len(last.Text)+1 <= opts.MaxChars {
			prev.Text = prev.Text + "\n" + last.Text
			prev.EndLine = last.EndLine
			chunks[len(chunks)-2] = prev
			chunks = chunks[:len(chunks)-1]
		}
	}

	return chunks
}

func pickOverlapLines(lines []lineInfo, targetTokens int) []lineInfo {
	if len(lines) == 0 || targetTokens <= 0 {
		return nil
	}
	var overlap []lineInfo
	var tokenCount int
	for i := len(lines) - 1; i >= 0; i-- {
		l := lines[i]
		t := EstimateTokens(l.text)
		if tokenCount+t > targetTokens && len(overlap) > 0 {
			break
		}
		tokenCount += t
		overlap = append([]lineInfo{l}, overlap...)
	}
	return overlap
}

func joinLines(lines []lineInfo) string {
	var sb strings.Builder
	for i, l := range lines {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(l.text)
	}
	return sb.String()
}
