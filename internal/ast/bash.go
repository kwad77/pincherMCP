package ast

import (
	"bytes"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// bashExtractor parses Bash / sh scripts via mvdan.cc/sh/v3/syntax — a pure-Go
// shell parser used by shfmt. Emits one Function symbol per top-level
// function declaration (`name() { ... }` POSIX style and `function name { ... }`
// reserved-word style), with exact byte offsets pulled from the parser's
// position info.
//
// Confidence is 1.0 (real AST parser, not regex).
//
// Registered for .sh and .bash. Replaces the stub adapter that previously
// registered Bash as a "detected but not extracted" language.
type bashExtractor struct{}

func (b *bashExtractor) Languages() []string { return []string{"Bash"} }
func (b *bashExtractor) Extensions() map[string]string {
	return map[string]string{
		".sh":   "Bash",
		".bash": "Bash",
	}
}
func (b *bashExtractor) Confidence() float64 { return 1.0 }

func (b *bashExtractor) Extract(source []byte, _, relPath string, _ ExtractOptions) *FileResult {
	result := &FileResult{}

	base := filepath.Base(relPath)
	if ext := filepath.Ext(base); ext != "" {
		base = base[:len(base)-len(ext)]
	}
	result.Module = base

	if len(source) == 0 {
		return result
	}

	parser := syntax.NewParser(syntax.Variant(syntax.LangBash))
	file, err := parser.Parse(bytes.NewReader(source), relPath)
	if err != nil && file == nil {
		// Parse failed irrecoverably — return empty.
		return result
	}

	sourceLen := len(source)
	syntax.Walk(file, func(node syntax.Node) bool {
		if node == nil {
			return true
		}
		fn, ok := node.(*syntax.FuncDecl)
		if !ok {
			return true
		}
		if fn.Name == nil || fn.Name.Value == "" {
			return true
		}

		startByte := int(fn.Pos().Offset())
		endByte := int(fn.End().Offset())
		if endByte > sourceLen {
			endByte = sourceLen
		}
		if startByte >= endByte {
			return true
		}

		name := fn.Name.Value
		sig := name + "()"
		if fn.RsrvWord {
			sig = "function " + name
			if fn.Parens {
				sig += "()"
			}
		}

		result.Symbols = append(result.Symbols, ExtractedSymbol{
			Name:          name,
			QualifiedName: result.Module + "." + name,
			Kind:          "Function",
			StartByte:     startByte,
			EndByte:       endByte,
			StartLine:     int(fn.Pos().Line()),
			EndLine:       int(fn.End().Line()),
			Signature:     sig,
			// By Bash convention, names beginning with "_" are treated as
			// internal helpers; everything else is callable from outside.
			IsExported: !strings.HasPrefix(name, "_"),
		})
		return true
	})

	return result
}

func init() {
	Register(&bashExtractor{})
}
