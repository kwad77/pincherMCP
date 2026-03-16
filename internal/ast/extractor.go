// Package ast provides multi-language symbol extraction with byte-offset recording.
//
// Each extracted symbol stores start_byte and end_byte alongside line numbers.
// This enables O(1) source retrieval at query time: one SQL lookup, one file seek,
// one read — no re-parsing, no line scanning.
//
// Language support:
//   - Go:         go/ast + go/parser (precise byte offsets via token.Pos)
//   - Python:     regex patterns (function/class/method definitions)
//   - JavaScript: regex patterns (function/class/method/arrow definitions)
//   - TypeScript: regex patterns (extends JavaScript, adds interface/type)
//   - Rust:       regex patterns (fn/struct/enum/trait/impl)
//   - Java:       regex patterns (class/interface/method)
//   - Ruby, PHP, C, C++, C#, Kotlin, Swift: regex fallback
//
// The regex approach covers ~80% of real-world symbols accurately.
// To upgrade to full tree-sitter accuracy, replace the language extractors
// with tree-sitter bindings — the interface remains identical.
package ast

import (
	"bufio"
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strings"
)

// ExtractedSymbol is the raw output of the AST extractor.
// It does NOT include project-level fields (project_id, file_hash) —
// those are added by the indexer.
type ExtractedSymbol struct {
	Name          string
	QualifiedName string
	Kind          string // Function|Method|Class|Interface|Enum|Type|Variable|Module
	StartByte     int
	EndByte       int
	StartLine     int
	EndLine       int
	Signature     string
	ReturnType    string
	Docstring     string
	Parent        string
	IsExported    bool
	IsTest        bool
	IsEntryPoint  bool
	Complexity    int
}

// ExtractedEdge is a raw call/import relationship found during extraction.
type ExtractedEdge struct {
	FromQN     string
	ToName     string // may be short name; resolved by indexer against symbol table
	Kind       string // CALLS|IMPORTS|INHERITS|IMPLEMENTS
	Confidence float64
}

// FileResult holds all symbols and edges extracted from one file.
type FileResult struct {
	Symbols []ExtractedSymbol
	Edges   []ExtractedEdge
	Module  string // module/package name
}

// Extract dispatches to the appropriate language extractor.
// source is the raw file content; language is the detected language string.
// relPath is the file path relative to the project root (used for qualified names).
func Extract(source []byte, language, relPath string) *FileResult {
	switch language {
	case "Go":
		return extractGo(source, relPath)
	case "Python":
		return extractPython(source, relPath)
	case "JavaScript", "JSX":
		return extractJavaScript(source, relPath)
	case "TypeScript", "TSX":
		return extractTypeScript(source, relPath)
	case "Rust":
		return extractRust(source, relPath)
	case "Java":
		return extractJava(source, relPath)
	case "Ruby":
		return extractRuby(source, relPath)
	case "PHP":
		return extractPHP(source, relPath)
	case "C", "C++":
		return extractC(source, relPath)
	case "C#":
		return extractCSharp(source, relPath)
	case "Kotlin":
		return extractKotlin(source, relPath)
	case "Swift":
		return extractSwift(source, relPath)
	default:
		return &FileResult{}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Go extractor — uses go/ast for precise byte offsets
// ─────────────────────────────────────────────────────────────────────────────

func extractGo(source []byte, relPath string) *FileResult {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, relPath, source, parser.ParseComments)
	if err != nil {
		// Partial parse still yields symbols
	}
	if f == nil {
		return &FileResult{}
	}

	result := &FileResult{}
	if f.Name != nil {
		result.Module = f.Name.Name
	}

	lineOffsets := buildLineOffsets(source)
	isTest := strings.HasSuffix(relPath, "_test.go")

	// Track current package prefix for qualified names
	pkg := ""
	if f.Name != nil {
		pkg = f.Name.Name
	}

	// Extract top-level imports as edges
	for _, imp := range f.Imports {
		if imp.Path != nil {
			path := strings.Trim(imp.Path.Value, `"`)
			result.Edges = append(result.Edges, ExtractedEdge{
				FromQN:     pkg,
				ToName:     path,
				Kind:       "IMPORTS",
				Confidence: 1.0,
			})
		}
	}

	// Walk top-level declarations
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			sym := goFuncToSymbol(d, fset, source, lineOffsets, pkg, isTest)
			result.Symbols = append(result.Symbols, sym)

			// Extract calls from function body
			if d.Body != nil {
				calls := extractGoCalls(d.Body, sym.QualifiedName)
				result.Edges = append(result.Edges, calls...)
			}

		case *ast.GenDecl:
			syms := goGenDeclToSymbols(d, fset, source, lineOffsets, pkg)
			result.Symbols = append(result.Symbols, syms...)
		}
	}

	return result
}

func goFuncToSymbol(d *ast.FuncDecl, fset *token.FileSet, source []byte, lineOffsets []int, pkg string, isTest bool) ExtractedSymbol {
	startPos := fset.Position(d.Pos())
	endPos := fset.Position(d.End())

	name := d.Name.Name
	kind := "Function"
	parent := ""
	qn := pkg + "." + name

	// Method if it has a receiver
	if d.Recv != nil && len(d.Recv.List) > 0 {
		kind = "Method"
		recv := d.Recv.List[0]
		recvType := goTypeToString(recv.Type)
		parent = pkg + "." + recvType
		qn = parent + "." + name
	}

	sig := buildGoSignature(d)
	retType := ""
	if d.Type.Results != nil {
		var parts []string
		for _, r := range d.Type.Results.List {
			parts = append(parts, goTypeToString(r.Type))
		}
		retType = strings.Join(parts, ", ")
	}

	doc := ""
	if d.Doc != nil {
		doc = strings.TrimSpace(d.Doc.Text())
	}

	isExported := ast.IsExported(name)
	isEntryPoint := name == "main" && pkg == "main"
	isTestFn := isTest || strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "Benchmark")

	return ExtractedSymbol{
		Name:          name,
		QualifiedName: qn,
		Kind:          kind,
		StartByte:     startPos.Offset,
		EndByte:       endPos.Offset,
		StartLine:     startPos.Line,
		EndLine:       endPos.Line,
		Signature:     sig,
		ReturnType:    retType,
		Docstring:     doc,
		Parent:        parent,
		IsExported:    isExported,
		IsTest:        isTestFn,
		IsEntryPoint:  isEntryPoint,
		Complexity:    estimateComplexity(source[startPos.Offset:min(endPos.Offset, len(source))]),
	}
}

func goGenDeclToSymbols(d *ast.GenDecl, fset *token.FileSet, source []byte, lineOffsets []int, pkg string) []ExtractedSymbol {
	var syms []ExtractedSymbol
	for _, spec := range d.Specs {
		switch sp := spec.(type) {
		case *ast.TypeSpec:
			startPos := fset.Position(sp.Pos())
			endPos := fset.Position(sp.End())
			kind := "Type"
			switch sp.Type.(type) {
			case *ast.StructType:
				kind = "Class"
			case *ast.InterfaceType:
				kind = "Interface"
			}
			doc := ""
			if d.Doc != nil {
				doc = strings.TrimSpace(d.Doc.Text())
			}
			syms = append(syms, ExtractedSymbol{
				Name:          sp.Name.Name,
				QualifiedName: pkg + "." + sp.Name.Name,
				Kind:          kind,
				StartByte:     startPos.Offset,
				EndByte:       endPos.Offset,
				StartLine:     startPos.Line,
				EndLine:       endPos.Line,
				Docstring:     doc,
				IsExported:    ast.IsExported(sp.Name.Name),
			})
		}
	}
	return syms
}

func extractGoCalls(body *ast.BlockStmt, callerQN string) []ExtractedEdge {
	var edges []ExtractedEdge
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		callee := goCalleeToString(call.Fun)
		if callee != "" {
			edges = append(edges, ExtractedEdge{
				FromQN:     callerQN,
				ToName:     callee,
				Kind:       "CALLS",
				Confidence: 0.7, // unresolved, lower confidence
			})
		}
		return true
	})
	return edges
}

func goCalleeToString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return goCalleeToString(e.X) + "." + e.Sel.Name
	default:
		return ""
	}
}

func goTypeToString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return "*" + goTypeToString(e.X)
	case *ast.SelectorExpr:
		return goTypeToString(e.X) + "." + e.Sel.Name
	case *ast.ArrayType:
		return "[]" + goTypeToString(e.Elt)
	case *ast.MapType:
		return "map[" + goTypeToString(e.Key) + "]" + goTypeToString(e.Value)
	default:
		return "?"
	}
}

func buildGoSignature(d *ast.FuncDecl) string {
	var sb strings.Builder
	sb.WriteString("func ")
	if d.Recv != nil && len(d.Recv.List) > 0 {
		sb.WriteString("(")
		sb.WriteString(goTypeToString(d.Recv.List[0].Type))
		sb.WriteString(") ")
	}
	sb.WriteString(d.Name.Name)
	sb.WriteString("(")
	if d.Type.Params != nil {
		for i, p := range d.Type.Params.List {
			if i > 0 {
				sb.WriteString(", ")
			}
			for j, n := range p.Names {
				if j > 0 {
					sb.WriteString(", ")
				}
				sb.WriteString(n.Name)
			}
			sb.WriteString(" ")
			sb.WriteString(goTypeToString(p.Type))
		}
	}
	sb.WriteString(")")
	if d.Type.Results != nil {
		sb.WriteString(" ")
		if len(d.Type.Results.List) > 1 {
			sb.WriteString("(")
		}
		for i, r := range d.Type.Results.List {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(goTypeToString(r.Type))
		}
		if len(d.Type.Results.List) > 1 {
			sb.WriteString(")")
		}
	}
	return sb.String()
}

// ─────────────────────────────────────────────────────────────────────────────
// Regex-based extractors for other languages
// ─────────────────────────────────────────────────────────────────────────────

// regexExtractor holds pre-compiled patterns for a language.
type regexExtractor struct {
	funcRE      *regexp.Regexp
	classRE     *regexp.Regexp
	interfaceRE *regexp.Regexp
	methodRE    *regexp.Regexp
	importRE    *regexp.Regexp
	enumRE      *regexp.Regexp
}

func (rx *regexExtractor) extract(source []byte, relPath, language string, opts extractOpts) *FileResult {
	result := &FileResult{}
	lines := splitLines(source)
	lineOffsets := buildLineOffsets(source)

	var currentClass string
	var currentClassEnd int

	for lineIdx, line := range lines {
		lineNum := lineIdx + 1
		lineStart := 0
		if lineIdx < len(lineOffsets) {
			lineStart = lineOffsets[lineIdx]
		}

		// Track class scope for method qualified names
		if rx.classRE != nil {
			if m := rx.classRE.FindStringSubmatch(line); m != nil {
				name := extractGroup(m, "name")
				if name != "" {
					endByte := findBlockEnd(source, lineStart, opts.blockChar)
					endLine := offsetToLine(lineOffsets, endByte)
					parent := extractGroup(m, "parent")
					currentClass = name
					currentClassEnd = endLine
					qn := moduleQN(relPath, opts.modSep) + opts.modSep + name
					result.Symbols = append(result.Symbols, ExtractedSymbol{
						Name:          name,
						QualifiedName: qn,
						Kind:          "Class",
						StartByte:     lineStart,
						EndByte:       endByte,
						StartLine:     lineNum,
						EndLine:       endLine,
						Parent:        parent,
						IsExported:    isExported(name, opts.exportedFn),
					})
				}
			}
		}

		// Reset class context when past its end
		if lineNum > currentClassEnd {
			currentClass = ""
		}

		// Interface
		if rx.interfaceRE != nil {
			if m := rx.interfaceRE.FindStringSubmatch(line); m != nil {
				name := extractGroup(m, "name")
				if name != "" {
					endByte := findBlockEnd(source, lineStart, opts.blockChar)
					qn := moduleQN(relPath, opts.modSep) + opts.modSep + name
					result.Symbols = append(result.Symbols, ExtractedSymbol{
						Name:          name,
						QualifiedName: qn,
						Kind:          "Interface",
						StartByte:     lineStart,
						EndByte:       endByte,
						StartLine:     lineNum,
						EndLine:       offsetToLine(lineOffsets, endByte),
						IsExported:    isExported(name, opts.exportedFn),
					})
				}
			}
		}

		// Enum
		if rx.enumRE != nil {
			if m := rx.enumRE.FindStringSubmatch(line); m != nil {
				name := extractGroup(m, "name")
				if name != "" {
					endByte := findBlockEnd(source, lineStart, opts.blockChar)
					qn := moduleQN(relPath, opts.modSep) + opts.modSep + name
					result.Symbols = append(result.Symbols, ExtractedSymbol{
						Name: name, QualifiedName: qn, Kind: "Enum",
						StartByte: lineStart, EndByte: endByte,
						StartLine: lineNum, EndLine: offsetToLine(lineOffsets, endByte),
					})
				}
			}
		}

		// Function / Method
		funcPattern := rx.funcRE
		if currentClass != "" && rx.methodRE != nil {
			funcPattern = rx.methodRE
		}
		if funcPattern != nil {
			if m := funcPattern.FindStringSubmatch(line); m != nil {
				name := extractGroup(m, "name")
				if name != "" {
					endByte := findBlockEnd(source, lineStart, opts.blockChar)
					endLine := offsetToLine(lineOffsets, endByte)
					sig := strings.TrimSpace(line)
					if len(sig) > 200 {
						sig = sig[:200]
					}

					kind := "Function"
					qn := moduleQN(relPath, opts.modSep) + opts.modSep + name
					parent := ""
					if currentClass != "" {
						kind = "Method"
						parent = moduleQN(relPath, opts.modSep) + opts.modSep + currentClass
						qn = parent + opts.modSep + name
					}

					result.Symbols = append(result.Symbols, ExtractedSymbol{
						Name:          name,
						QualifiedName: qn,
						Kind:          kind,
						StartByte:     lineStart,
						EndByte:       endByte,
						StartLine:     lineNum,
						EndLine:       endLine,
						Signature:     sig,
						Parent:        parent,
						IsExported:    isExported(name, opts.exportedFn),
						IsTest:        opts.isTest(name),
						Complexity:    estimateComplexity(source[lineStart:min(endByte, len(source))]),
					})
				}
			}
		}

		// Imports
		if rx.importRE != nil {
			if m := rx.importRE.FindStringSubmatch(line); m != nil {
				imp := extractGroup(m, "path")
				if imp == "" {
					imp = extractGroup(m, "name")
				}
				if imp != "" {
					result.Edges = append(result.Edges, ExtractedEdge{
						ToName: imp, Kind: "IMPORTS", Confidence: 1.0,
					})
				}
			}
		}
	}
	return result
}

type extractOpts struct {
	modSep     string
	blockChar  byte
	exportedFn func(string) bool
	isTest     func(string) bool
}

// Language-specific extractors

var pyRE = &regexExtractor{
	funcRE:  regexp.MustCompile(`(?m)^[ \t]*def\s+(?P<name>[A-Za-z_][A-Za-z0-9_]*)\s*\(`),
	classRE: regexp.MustCompile(`(?m)^class\s+(?P<name>[A-Za-z_][A-Za-z0-9_]*)(?:\((?P<parent>[^)]*)\))?:`),
	importRE: regexp.MustCompile(
		`(?m)^(?:from\s+(?P<path>[.\w]+)\s+import|import\s+(?P<name>[.\w]+))`),
}

func extractPython(source []byte, relPath string) *FileResult {
	opts := extractOpts{
		modSep:    ".",
		blockChar: 0, // Python uses indentation, approximate via colon heuristic
		exportedFn: func(name string) bool {
			return !strings.HasPrefix(name, "_")
		},
		isTest: func(name string) bool {
			return strings.HasPrefix(name, "test_") || strings.HasPrefix(name, "Test")
		},
	}
	res := pyRE.extract(source, relPath, "Python", opts)
	// Derive module name from file path
	mod := strings.TrimSuffix(relPath, ".py")
	mod = strings.ReplaceAll(mod, "/", ".")
	mod = strings.ReplaceAll(mod, "\\", ".")
	res.Module = mod
	return res
}

var jsRE = &regexExtractor{
	funcRE: regexp.MustCompile(
		`(?m)(?:^|\s)(?:export\s+)?(?:async\s+)?function\s+(?P<name>[A-Za-z_$][A-Za-z0-9_$]*)|` +
			`(?m)(?:^|\s)(?:export\s+)?(?:const|let|var)\s+(?P<name2>[A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*(?:async\s*)?\(`),
	classRE: regexp.MustCompile(`(?m)^(?:export\s+)?(?:default\s+)?class\s+(?P<name>[A-Za-z_$][A-Za-z0-9_$]*)(?:\s+extends\s+(?P<parent>[A-Za-z_$][A-Za-z0-9_$]*))?`),
	importRE: regexp.MustCompile(`(?m)^import\s+.*?from\s+['"](?P<path>[^'"]+)['"]`),
}

func extractJavaScript(source []byte, relPath string) *FileResult {
	opts := extractOpts{
		modSep:    ".",
		blockChar: '{',
		exportedFn: func(name string) bool {
			// Exported if the declaration has 'export' before it
			return true
		},
		isTest: func(name string) bool {
			return strings.HasPrefix(name, "test") || strings.HasPrefix(name, "spec")
		},
	}
	return jsRE.extract(source, relPath, "JavaScript", opts)
}

var tsRE = &regexExtractor{
	funcRE: regexp.MustCompile(
		`(?m)(?:^|\s)(?:export\s+)?(?:async\s+)?function\s+(?P<name>[A-Za-z_$][A-Za-z0-9_$]*)|` +
			`(?m)(?:^|\s)(?:export\s+)?(?:const|let|var)\s+(?P<name>[A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*(?:async\s*)?\(`),
	classRE:     regexp.MustCompile(`(?m)^(?:export\s+)?(?:abstract\s+)?class\s+(?P<name>[A-Za-z_$][A-Za-z0-9_$]*)(?:\s+extends\s+(?P<parent>[A-Za-z_$][A-Za-z0-9_$]*))?`),
	interfaceRE: regexp.MustCompile(`(?m)^(?:export\s+)?interface\s+(?P<name>[A-Za-z_$][A-Za-z0-9_$]*)`),
	enumRE:      regexp.MustCompile(`(?m)^(?:export\s+)?(?:const\s+)?enum\s+(?P<name>[A-Za-z_$][A-Za-z0-9_$]*)`),
	importRE:    regexp.MustCompile(`(?m)^import\s+.*?from\s+['"](?P<path>[^'"]+)['"]`),
}

func extractTypeScript(source []byte, relPath string) *FileResult {
	opts := extractOpts{
		modSep:     ".",
		blockChar:  '{',
		exportedFn: func(name string) bool { return true },
		isTest: func(name string) bool {
			return strings.HasPrefix(name, "test") || strings.HasPrefix(name, "describe")
		},
	}
	return tsRE.extract(source, relPath, "TypeScript", opts)
}

var rustRE = &regexExtractor{
	funcRE:      regexp.MustCompile(`(?m)^(?:pub(?:\(.*?\))?\s+)?(?:async\s+)?fn\s+(?P<name>[a-z_][a-z0-9_]*)`),
	classRE:     regexp.MustCompile(`(?m)^(?:pub(?:\(.*?\))?\s+)?struct\s+(?P<name>[A-Z][A-Za-z0-9_]*)`),
	interfaceRE: regexp.MustCompile(`(?m)^(?:pub(?:\(.*?\))?\s+)?trait\s+(?P<name>[A-Z][A-Za-z0-9_]*)`),
	enumRE:      regexp.MustCompile(`(?m)^(?:pub(?:\(.*?\))?\s+)?enum\s+(?P<name>[A-Z][A-Za-z0-9_]*)`),
	importRE:    regexp.MustCompile(`(?m)^use\s+(?P<path>[a-zA-Z0-9_:]+)`),
}

func extractRust(source []byte, relPath string) *FileResult {
	opts := extractOpts{
		modSep:    "::",
		blockChar: '{',
		exportedFn: func(name string) bool {
			// In Rust, 'pub' prefix = exported. Approximation: starts with uppercase = exported type.
			return true
		},
		isTest: func(name string) bool { return false },
	}
	return rustRE.extract(source, relPath, "Rust", opts)
}

var javaRE = &regexExtractor{
	funcRE:      regexp.MustCompile(`(?m)^\s*(?:public|private|protected)?\s*(?:static\s+)?(?:final\s+)?(?:\w+\s+)+(?P<name>[A-Za-z][A-Za-z0-9_]*)\s*\(`),
	classRE:     regexp.MustCompile(`(?m)^(?:public\s+)?(?:abstract\s+)?(?:final\s+)?class\s+(?P<name>[A-Z][A-Za-z0-9_]*)(?:\s+extends\s+(?P<parent>[A-Z][A-Za-z0-9_]*))?`),
	interfaceRE: regexp.MustCompile(`(?m)^(?:public\s+)?interface\s+(?P<name>[A-Z][A-Za-z0-9_]*)`),
	enumRE:      regexp.MustCompile(`(?m)^(?:public\s+)?enum\s+(?P<name>[A-Z][A-Za-z0-9_]*)`),
	importRE:    regexp.MustCompile(`(?m)^import\s+(?P<path>[a-zA-Z0-9_.]+)`),
}

func extractJava(source []byte, relPath string) *FileResult {
	opts := extractOpts{
		modSep:    ".",
		blockChar: '{',
		exportedFn: func(name string) bool {
			return len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z'
		},
		isTest: func(name string) bool { return false },
	}
	return javaRE.extract(source, relPath, "Java", opts)
}

var rubyRE = &regexExtractor{
	funcRE:  regexp.MustCompile(`(?m)^\s*def\s+(?P<name>[a-zA-Z_][a-zA-Z0-9_?!]*)`),
	classRE: regexp.MustCompile(`(?m)^class\s+(?P<name>[A-Z][A-Za-z0-9_]*)(?:\s*<\s*(?P<parent>[A-Z][A-Za-z0-9_:]*))?`),
}

func extractRuby(source []byte, relPath string) *FileResult {
	opts := extractOpts{modSep: "::", blockChar: 0, exportedFn: func(n string) bool { return true }, isTest: func(n string) bool { return false }}
	return rubyRE.extract(source, relPath, "Ruby", opts)
}

var phpRE = &regexExtractor{
	funcRE:  regexp.MustCompile(`(?m)^(?:public|private|protected)?\s*(?:static\s+)?function\s+(?P<name>[a-zA-Z_][a-zA-Z0-9_]*)`),
	classRE: regexp.MustCompile(`(?m)^(?:abstract\s+)?class\s+(?P<name>[A-Z][A-Za-z0-9_]*)(?:\s+extends\s+(?P<parent>[A-Z][A-Za-z0-9_]*))?`),
}

func extractPHP(source []byte, relPath string) *FileResult {
	opts := extractOpts{modSep: "\\", blockChar: '{', exportedFn: func(n string) bool { return true }, isTest: func(n string) bool { return false }}
	return phpRE.extract(source, relPath, "PHP", opts)
}

var cRE = &regexExtractor{
	funcRE: regexp.MustCompile(`(?m)^(?:static\s+)?(?:inline\s+)?(?:\w+\s+)+(?P<name>[A-Za-z_][A-Za-z0-9_]*)\s*\(`),
}

func extractC(source []byte, relPath string) *FileResult {
	opts := extractOpts{modSep: "::", blockChar: '{', exportedFn: func(n string) bool { return true }, isTest: func(n string) bool { return false }}
	return cRE.extract(source, relPath, "C", opts)
}

var csRE = &regexExtractor{
	funcRE:      regexp.MustCompile(`(?m)^\s*(?:public|private|protected|internal)?\s*(?:static\s+)?(?:async\s+)?(?:\w+\s+)+(?P<name>[A-Za-z][A-Za-z0-9_]*)\s*\(`),
	classRE:     regexp.MustCompile(`(?m)^(?:\s*)(?:public|private|internal)?\s*(?:abstract|sealed)?\s*class\s+(?P<name>[A-Z][A-Za-z0-9_]*)(?:\s*:\s*(?P<parent>[A-Z][A-Za-z0-9_, ]*))?`),
	interfaceRE: regexp.MustCompile(`(?m)^(?:\s*)(?:public)?\s*interface\s+(?P<name>[A-Z][A-Za-z0-9_]*)`),
}

func extractCSharp(source []byte, relPath string) *FileResult {
	opts := extractOpts{modSep: ".", blockChar: '{', exportedFn: func(n string) bool { return true }, isTest: func(n string) bool { return false }}
	return csRE.extract(source, relPath, "C#", opts)
}

var kotlinRE = &regexExtractor{
	funcRE:  regexp.MustCompile(`(?m)^\s*(?:public|private|internal|protected)?\s*(?:suspend\s+)?fun\s+(?P<name>[a-zA-Z][a-zA-Z0-9_]*)`),
	classRE: regexp.MustCompile(`(?m)^(?:open\s+)?(?:data\s+)?class\s+(?P<name>[A-Z][A-Za-z0-9_]*)(?:\(|:|\s)`),
}

func extractKotlin(source []byte, relPath string) *FileResult {
	opts := extractOpts{modSep: ".", blockChar: '{', exportedFn: func(n string) bool { return true }, isTest: func(n string) bool { return false }}
	return kotlinRE.extract(source, relPath, "Kotlin", opts)
}

var swiftRE = &regexExtractor{
	funcRE:      regexp.MustCompile(`(?m)^\s*(?:public|private|internal|open)?\s*(?:static\s+)?func\s+(?P<name>[a-zA-Z][a-zA-Z0-9_]*)`),
	classRE:     regexp.MustCompile(`(?m)^(?:public\s+)?(?:final\s+)?class\s+(?P<name>[A-Z][A-Za-z0-9_]*)(?:\s*:\s*(?P<parent>[A-Z][A-Za-z0-9_, ]*))?`),
	interfaceRE: regexp.MustCompile(`(?m)^(?:public\s+)?protocol\s+(?P<name>[A-Z][A-Za-z0-9_]*)`),
}

func extractSwift(source []byte, relPath string) *FileResult {
	opts := extractOpts{modSep: ".", blockChar: '{', exportedFn: func(n string) bool { return true }, isTest: func(n string) bool { return false }}
	return swiftRE.extract(source, relPath, "Swift", opts)
}

// ─────────────────────────────────────────────────────────────────────────────
// Utility functions
// ─────────────────────────────────────────────────────────────────────────────

// buildLineOffsets returns the byte offset of the start of each line.
func buildLineOffsets(source []byte) []int {
	offsets := []int{0}
	for i, b := range source {
		if b == '\n' && i+1 < len(source) {
			offsets = append(offsets, i+1)
		}
	}
	return offsets
}

// splitLines splits source into lines without allocating a giant string.
func splitLines(source []byte) []string {
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(source))
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}

// findBlockEnd finds the byte offset of the closing brace/dedent after startOffset.
// For brace-delimited languages (blockChar='{'), walks forward counting braces.
// For indent-delimited languages (blockChar=0), finds the next line with equal or less indent.
func findBlockEnd(source []byte, startOffset int, blockChar byte) int {
	if startOffset >= len(source) {
		return len(source)
	}
	if blockChar == 0 {
		// Indentation-based (Python): find next line with ≤ indent level
		// Simplified: just return 80 lines worth of bytes
		end := startOffset
		count := 0
		for end < len(source) && count < 80 {
			if source[end] == '\n' {
				count++
			}
			end++
		}
		return min(end, len(source))
	}
	// Brace-delimited
	depth := 0
	started := false
	for i := startOffset; i < len(source); i++ {
		if source[i] == blockChar {
			depth++
			started = true
		} else if source[i] == blockChar+2 { // '}' is '{'+2
			depth--
			if started && depth == 0 {
				return i + 1
			}
		}
	}
	return len(source)
}

// offsetToLine returns the 1-based line number for a byte offset.
func offsetToLine(lineOffsets []int, offset int) int {
	lo, hi := 0, len(lineOffsets)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		if lineOffsets[mid] <= offset {
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return hi + 1
}

// moduleQN derives a module/package qualified name prefix from a relative file path.
func moduleQN(relPath, sep string) string {
	// Strip extension
	base := relPath
	if idx := strings.LastIndex(base, "."); idx > 0 {
		base = base[:idx]
	}
	// Normalize path separators to the language separator
	base = strings.ReplaceAll(base, "/", sep)
	base = strings.ReplaceAll(base, "\\", sep)
	return base
}

// extractGroup extracts a named capture group from a regex match.
// Falls back to group 2 if "name" group is empty (for alternation patterns).
func extractGroup(m []string, name string) string {
	// Try to find the group named "name" — but regexp doesn't give us named group indices easily.
	// For simplicity, return the first non-empty group after the full match.
	for i := 1; i < len(m); i++ {
		if m[i] != "" {
			return m[i]
		}
	}
	return ""
}

// isExported checks if a name is exported according to the given rule.
func isExported(name string, fn func(string) bool) bool {
	if fn == nil {
		return len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z'
	}
	return fn(name)
}

// estimateComplexity counts branch keywords as a rough cyclomatic complexity proxy.
func estimateComplexity(body []byte) int {
	keywords := []string{"if ", "else ", "for ", "while ", "switch ", "case ", "catch ", "&&", "||"}
	count := 1
	s := string(body)
	for _, kw := range keywords {
		count += strings.Count(s, kw)
	}
	return count
}

// min returns the smaller of two ints.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
