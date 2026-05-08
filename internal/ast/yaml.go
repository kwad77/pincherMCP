package ast

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// extractYAML parses a YAML or JSON document and emits a Setting symbol per key.
//
// Each key's QualifiedName is the dotted path from the root, e.g. "services.web.image".
// Sequence elements use numeric indices, e.g. "tasks.0.name". When a file contains
// multiple YAML documents, the doc index is included in the path: "doc1.services.web".
//
// The byte range of each Setting covers from the key (or sequence element) on the
// page through to the start of the next sibling-or-shallower entry, so retrieving
// the symbol returns the key plus its full nested value — the same shape as
// retrieving a function body in code.
//
// Confidence is 1.0 (real YAML parser, not regex).
func extractYAML(source []byte, relPath string) *FileResult {
	result := &FileResult{}

	base := filepath.Base(relPath)
	if ext := filepath.Ext(base); ext != "" {
		base = base[:len(base)-len(ext)]
	}
	result.Module = base

	if len(source) == 0 {
		return result
	}

	lineOffsets := buildLineOffsets(source)
	sourceLen := len(source)

	// Decode all documents (handles YAML's `---` multi-document streams).
	var docs []*yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(source))
	for {
		var doc yaml.Node
		if err := decoder.Decode(&doc); err != nil {
			break
		}
		d := doc
		docs = append(docs, &d)
	}
	if len(docs) == 0 {
		return result
	}
	multiDoc := len(docs) > 1

	// Collect entries via DFS — entries are produced in source order.
	type entry struct {
		path []string
		val  *yaml.Node
		line int
		col  int
	}
	var entries []entry

	var walk func(n *yaml.Node, path []string)
	walk = func(n *yaml.Node, path []string) {
		if n == nil {
			return
		}
		switch n.Kind {
		case yaml.DocumentNode:
			for _, child := range n.Content {
				walk(child, path)
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				k := n.Content[i]
				v := n.Content[i+1]
				if k.Kind != yaml.ScalarNode {
					continue
				}
				childPath := append(append([]string(nil), path...), yamlSanitizeKey(k.Value))
				entries = append(entries, entry{
					path: childPath,
					val:  v,
					line: k.Line,
					col:  k.Column,
				})
				walk(v, childPath)
			}
		case yaml.SequenceNode:
			for i, child := range n.Content {
				childPath := append(append([]string(nil), path...), fmt.Sprintf("%d", i))
				entries = append(entries, entry{
					path: childPath,
					val:  child,
					line: child.Line,
					col:  child.Column,
				})
				walk(child, childPath)
			}
		}
	}

	for i, doc := range docs {
		var prefix []string
		if multiDoc {
			prefix = []string{fmt.Sprintf("doc%d", i)}
			line := doc.Line
			if line < 1 {
				line = 1
			}
			col := doc.Column
			if col < 1 {
				col = 1
			}
			entries = append(entries, entry{
				path: prefix,
				val:  doc,
				line: line,
				col:  col,
			})
		}
		walk(doc, prefix)
	}

	// Convert entries to symbols. End offset = start of next entry at same-or-shallower depth.
	for i, e := range entries {
		startByte := lineColToOffset(lineOffsets, e.line, e.col, sourceLen)

		endByte := sourceLen
		for j := i + 1; j < len(entries); j++ {
			if len(entries[j].path) <= len(e.path) {
				endByte = lineColToOffset(lineOffsets, entries[j].line, 1, sourceLen)
				break
			}
		}
		if endByte <= startByte {
			if e.line < len(lineOffsets) {
				endByte = lineOffsets[e.line]
			} else {
				endByte = sourceLen
			}
		}
		endLine := offsetToLine(lineOffsets, endByte-1)
		if endLine < e.line {
			endLine = e.line
		}

		name := e.path[len(e.path)-1]
		qn := strings.Join(e.path, ".")

		sig := yamlSignature(e.val)

		result.Symbols = append(result.Symbols, ExtractedSymbol{
			Name:          name,
			QualifiedName: qn,
			Kind:          "Setting",
			StartByte:     startByte,
			EndByte:       endByte,
			StartLine:     e.line,
			EndLine:       endLine,
			Signature:     sig,
			IsExported:    true,
		})
	}

	return result
}

// yamlSanitizeKey replaces characters that would collide with the dotted-path
// qualified name format.
func yamlSanitizeKey(k string) string {
	k = strings.ReplaceAll(k, ".", "_")
	k = strings.ReplaceAll(k, "/", "_")
	k = strings.ReplaceAll(k, " ", "_")
	return k
}

// yamlSignature returns a short, FTS-friendly description of a YAML value node.
func yamlSignature(n *yaml.Node) string {
	if n == nil {
		return ""
	}
	switch n.Kind {
	case yaml.ScalarNode:
		v := n.Value
		if len(v) > 200 {
			v = v[:200]
		}
		return v
	case yaml.MappingNode:
		return fmt.Sprintf("<mapping with %d keys>", len(n.Content)/2)
	case yaml.SequenceNode:
		return fmt.Sprintf("<sequence with %d items>", len(n.Content))
	case yaml.AliasNode:
		if n.Alias != nil {
			return "*" + n.Alias.Anchor
		}
		return "<alias>"
	case yaml.DocumentNode:
		return "<document>"
	}
	return ""
}

// lineColToOffset converts a 1-based (line, col) to a byte offset, clamped to source bounds.
func lineColToOffset(lineOffsets []int, line, col, sourceLen int) int {
	if line < 1 || line-1 >= len(lineOffsets) {
		return sourceLen
	}
	off := lineOffsets[line-1] + (col - 1)
	if off > sourceLen {
		return sourceLen
	}
	if off < 0 {
		return 0
	}
	return off
}
