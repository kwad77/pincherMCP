package ast

import (
	"path/filepath"
	"strings"

	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// markdownExtractor parses Markdown via goldmark (CommonMark) and emits one
// Section symbol per heading. The qualified name is the dotted path of
// ancestor heading names (e.g. README -> Installation -> Build becomes
// "README.Installation.Build"). Each section's byte range covers the heading
// line through the start of the next same-or-shallower heading, so retrieving
// "README.Installation" returns the entire Installation block — same shape
// as fetching a function body.
//
// Confidence is 1.0 (real CommonMark parser, not regex).
//
// Registered for .md, .markdown, .mdx. RST is intentionally skipped (different
// grammar; would need a separate extractor).
type markdownExtractor struct {
	parser goldmark.Markdown
}

func newMarkdownExtractor() *markdownExtractor {
	return &markdownExtractor{parser: goldmark.New()}
}

func (m *markdownExtractor) Languages() []string { return []string{"Markdown"} }
func (m *markdownExtractor) Extensions() map[string]string {
	return map[string]string{
		".md":       "Markdown",
		".markdown": "Markdown",
		".mdx":      "Markdown",
		".mdc":      "Markdown", // Cursor rule files (markdown + frontmatter)
	}
}
func (m *markdownExtractor) Confidence() float64 { return 1.0 }

func (m *markdownExtractor) Extract(source []byte, _ , relPath string, _ ExtractOptions) *FileResult {
	result := &FileResult{}

	base := filepath.Base(relPath)
	if ext := filepath.Ext(base); ext != "" {
		base = base[:len(base)-len(ext)]
	}
	result.Module = base

	if len(source) == 0 {
		return result
	}

	root := m.parser.Parser().Parse(text.NewReader(source))
	lineOffsets := buildLineOffsets(source)

	type heading struct {
		level     int
		name      string // raw text, used for the symbol Name field
		sanitized string // for the qualified name
		startByte int
		endByte   int
		startLine int
	}
	var headings []heading

	_ = gast.Walk(root, func(n gast.Node, entering bool) (gast.WalkStatus, error) {
		if !entering {
			return gast.WalkContinue, nil
		}
		h, ok := n.(*gast.Heading)
		if !ok {
			return gast.WalkContinue, nil
		}
		raw := strings.TrimSpace(headingText(h, source))
		if raw == "" {
			return gast.WalkContinue, nil
		}
		startByte := headingLineStart(h, lineOffsets, len(source))
		startLine := offsetToLine(lineOffsets, startByte)
		headings = append(headings, heading{
			level:     h.Level,
			name:      raw,
			sanitized: mdSanitize(raw),
			startByte: startByte,
			startLine: startLine,
		})
		return gast.WalkContinue, nil
	})

	if len(headings) == 0 {
		return result
	}

	// Compute end byte for each heading: start of next same-or-shallower heading,
	// or end of source.
	for i := range headings {
		end := len(source)
		for j := i + 1; j < len(headings); j++ {
			if headings[j].level <= headings[i].level {
				end = headings[j].startByte
				break
			}
		}
		headings[i].endByte = end
	}

	// Build qualified names via a level-stack. Pop until the stack top has a
	// strictly shallower level than the current heading, then push.
	type stackEntry struct {
		level int
		name  string
	}
	var stack []stackEntry

	for _, h := range headings {
		for len(stack) > 0 && stack[len(stack)-1].level >= h.level {
			stack = stack[:len(stack)-1]
		}
		stack = append(stack, stackEntry{level: h.level, name: h.sanitized})

		parts := make([]string, len(stack))
		for i, e := range stack {
			parts[i] = e.name
		}
		qn := strings.Join(parts, ".")

		endLine := offsetToLine(lineOffsets, h.endByte-1)
		if endLine < h.startLine {
			endLine = h.startLine
		}

		signature := strings.Repeat("#", h.level) + " " + h.name
		if len(signature) > 200 {
			signature = signature[:200]
		}

		result.Symbols = append(result.Symbols, ExtractedSymbol{
			Name:          h.name,
			QualifiedName: qn,
			Kind:          "Section",
			StartByte:     h.startByte,
			EndByte:       h.endByte,
			StartLine:     h.startLine,
			EndLine:       endLine,
			Signature:     signature,
			IsExported:    true,
		})
	}

	return result
}

// headingText concatenates all inline text content under a heading node.
func headingText(h *gast.Heading, source []byte) string {
	var sb strings.Builder
	_ = gast.Walk(h, func(n gast.Node, entering bool) (gast.WalkStatus, error) {
		if !entering {
			return gast.WalkContinue, nil
		}
		switch t := n.(type) {
		case *gast.Text:
			sb.Write(t.Segment.Value(source))
		case *gast.String:
			sb.Write(t.Value)
		}
		return gast.WalkContinue, nil
	})
	return sb.String()
}

// headingLineStart returns the byte offset of the start of the line containing
// the heading. For ATX headings (`# foo`) this lands right at the `#`.
func headingLineStart(h *gast.Heading, lineOffsets []int, sourceLen int) int {
	if h.Lines().Len() == 0 {
		return 0
	}
	seg := h.Lines().At(0)
	if seg.Start < 0 {
		return 0
	}
	if seg.Start > sourceLen {
		return sourceLen
	}
	line := offsetToLine(lineOffsets, seg.Start)
	if line >= 1 && line-1 < len(lineOffsets) {
		return lineOffsets[line-1]
	}
	return seg.Start
}

// mdSanitize collapses a heading title into a qualified-name component:
// drops dot/slash separators (so they don't break the dotted path), maps
// runs of whitespace to single underscores, trims edges. Empty input returns
// "_" so we never emit an empty path component.
func mdSanitize(s string) string {
	s = strings.ReplaceAll(s, ".", "")
	s = strings.ReplaceAll(s, "/", "")
	s = strings.ReplaceAll(s, "\\", "")
	// Collapse whitespace to single underscores.
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' {
			if !prevSpace {
				b.WriteByte('_')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "_"
	}
	return out
}

func init() {
	Register(newMarkdownExtractor())
}
