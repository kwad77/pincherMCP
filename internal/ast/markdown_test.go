package ast

import (
	"strings"
	"testing"
)

const mdSrc = `# Pincher

Tagline goes here.

## Installation

Some prose about installing.

### macOS

Brew install steps.

### Linux

Apt install steps.

## Usage

How to use it.

# Reference

API reference content.
`

func TestExtractMarkdown_HeadingHierarchy(t *testing.T) {
	result := Extract([]byte(mdSrc), "Markdown", "README.md")
	if result == nil {
		t.Fatal("Extract returned nil")
	}
	byQN := make(map[string]ExtractedSymbol)
	for _, s := range result.Symbols {
		byQN[s.QualifiedName] = s
	}
	for _, want := range []string{
		"Pincher",
		"Pincher.Installation",
		"Pincher.Installation.macOS",
		"Pincher.Installation.Linux",
		"Pincher.Usage",
		"Reference",
	} {
		if _, ok := byQN[want]; !ok {
			t.Errorf("expected qn %q in extracted symbols, got: %v", want, mapKeys(byQN))
		}
	}
}

func TestExtractMarkdown_AllSectionKind(t *testing.T) {
	result := Extract([]byte(mdSrc), "Markdown", "README.md")
	for _, s := range result.Symbols {
		if s.Kind != "Section" {
			t.Errorf("symbol %q kind = %q, want Section", s.QualifiedName, s.Kind)
		}
	}
}

func TestExtractMarkdown_ConfidenceOne(t *testing.T) {
	result := Extract([]byte(mdSrc), "Markdown", "README.md")
	if len(result.Symbols) == 0 {
		t.Fatal("no symbols extracted")
	}
	for _, s := range result.Symbols {
		if s.ExtractionConfidence != 1.0 {
			t.Errorf("symbol %q confidence = %v, want 1.0", s.QualifiedName, s.ExtractionConfidence)
			break
		}
	}
}

func TestExtractMarkdown_ByteRangeCoversFullSection(t *testing.T) {
	src := []byte(mdSrc)
	result := Extract(src, "Markdown", "README.md")
	byQN := make(map[string]ExtractedSymbol)
	for _, s := range result.Symbols {
		byQN[s.QualifiedName] = s
	}

	install := byQN["Pincher.Installation"]
	if install.StartByte == 0 || install.EndByte == 0 {
		t.Fatalf("Pincher.Installation has zero byte range: %+v", install)
	}
	if install.EndByte > len(src) {
		t.Fatalf("Pincher.Installation.EndByte=%d > source len %d", install.EndByte, len(src))
	}
	body := string(src[install.StartByte:install.EndByte])
	for _, want := range []string{"## Installation", "### macOS", "### Linux"} {
		if !strings.Contains(body, want) {
			t.Errorf("Pincher.Installation body missing %q\nbody:\n%s", want, body)
		}
	}
	// Should not include the next sibling heading "## Usage" or H1 "Reference".
	if strings.Contains(body, "## Usage") {
		t.Errorf("Pincher.Installation body leaks into ## Usage:\n%s", body)
	}
	if strings.Contains(body, "# Reference") {
		t.Errorf("Pincher.Installation body leaks into # Reference:\n%s", body)
	}
}

func TestExtractMarkdown_LeafSectionRangeStopsAtNextSibling(t *testing.T) {
	src := []byte(mdSrc)
	result := Extract(src, "Markdown", "README.md")
	byQN := make(map[string]ExtractedSymbol)
	for _, s := range result.Symbols {
		byQN[s.QualifiedName] = s
	}
	macos := byQN["Pincher.Installation.macOS"]
	if macos.StartByte == 0 {
		t.Fatal("macOS section not extracted")
	}
	body := string(src[macos.StartByte:macos.EndByte])
	if !strings.Contains(body, "Brew install steps") {
		t.Errorf("macOS body missing 'Brew install steps':\n%s", body)
	}
	if strings.Contains(body, "### Linux") {
		t.Errorf("macOS body leaks into ### Linux:\n%s", body)
	}
}

func TestExtractMarkdown_EmptySource(t *testing.T) {
	result := Extract([]byte(""), "Markdown", "empty.md")
	if result == nil {
		t.Fatal("nil result for empty source")
	}
	if len(result.Symbols) != 0 {
		t.Errorf("empty source produced %d symbols, want 0", len(result.Symbols))
	}
}

func TestExtractMarkdown_NoHeadings(t *testing.T) {
	result := Extract([]byte("Just prose, no headings.\n\nAnother paragraph."), "Markdown", "doc.md")
	if result == nil {
		t.Fatal("nil result")
	}
	if len(result.Symbols) != 0 {
		t.Errorf("no-heading source produced %d symbols, want 0", len(result.Symbols))
	}
}

func TestExtractMarkdown_TopLevelMultipleH1(t *testing.T) {
	src := `# First
content
# Second
more content
`
	result := Extract([]byte(src), "Markdown", "two.md")
	byQN := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byQN[s.QualifiedName] = s
	}
	if _, ok := byQN["First"]; !ok {
		t.Error("missing First")
	}
	if _, ok := byQN["Second"]; !ok {
		t.Error("missing Second")
	}
}

func TestExtractMarkdown_HeadingsWithSpecialChars(t *testing.T) {
	// Dots, slashes, and spaces in a heading must not break the dotted-path
	// qualified name.
	src := `# v1.2 / Quick Start
content
`
	result := Extract([]byte(src), "Markdown", "x.md")
	if len(result.Symbols) != 1 {
		t.Fatalf("got %d symbols, want 1", len(result.Symbols))
	}
	got := result.Symbols[0].QualifiedName
	if strings.Contains(got, ".") || strings.Contains(got, "/") {
		t.Errorf("qualified name %q contains separator chars; sanitize failed", got)
	}
	if result.Symbols[0].Name != "v1.2 / Quick Start" {
		t.Errorf("Name should preserve original heading text, got %q", result.Symbols[0].Name)
	}
}

func TestExtractMarkdown_MdxExtension(t *testing.T) {
	// Verify .mdx files dispatch through the Markdown extractor.
	if got := DetectLanguage("post.mdx"); got != "Markdown" {
		t.Errorf("DetectLanguage(post.mdx) = %q, want Markdown", got)
	}
}

func TestExtractMarkdown_MdcExtension(t *testing.T) {
	// Verify .mdc files (Cursor rule files) dispatch through the Markdown extractor.
	if got := DetectLanguage("rules.mdc"); got != "Markdown" {
		t.Errorf("DetectLanguage(rules.mdc) = %q, want Markdown", got)
	}
}

func TestExtractMarkdown_SignatureFormat(t *testing.T) {
	result := Extract([]byte(mdSrc), "Markdown", "README.md")
	byQN := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byQN[s.QualifiedName] = s
	}
	if got := byQN["Pincher.Installation.macOS"].Signature; got != "### macOS" {
		t.Errorf("signature = %q, want %q", got, "### macOS")
	}
	if got := byQN["Pincher"].Signature; got != "# Pincher" {
		t.Errorf("signature = %q, want %q", got, "# Pincher")
	}
}

func mapKeys(m map[string]ExtractedSymbol) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
