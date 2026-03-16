package ast

import (
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// DetectLanguage / IsSourceFile
// ─────────────────────────────────────────────────────────────────────────────

func TestDetectLanguage(t *testing.T) {
	cases := []struct {
		file string
		want string
	}{
		{"main.go", "Go"},
		{"handler.go", "Go"},
		{"script.py", "Python"},
		{"app.js", "JavaScript"},
		{"component.jsx", "JSX"},
		{"types.ts", "TypeScript"},
		{"page.tsx", "TSX"},
		{"lib.rs", "Rust"},
		{"Main.java", "Java"},
		{"helper.rb", "Ruby"},
		{"index.php", "PHP"},
		{"util.c", "C"},
		{"util.cpp", "C++"},
		{"service.cs", "C#"},
		{"app.kt", "Kotlin"},
		{"view.swift", "Swift"},
		{"unknown.xyz", ""},
		{"noext", ""},
		{"README.md", ""},
	}
	for _, c := range cases {
		got := DetectLanguage(c.file)
		if got != c.want {
			t.Errorf("DetectLanguage(%q) = %q, want %q", c.file, got, c.want)
		}
	}
}

func TestIsSourceFile(t *testing.T) {
	if !IsSourceFile("main.go") {
		t.Error("main.go should be a source file")
	}
	if IsSourceFile("README.md") {
		t.Error("README.md should not be a source file")
	}
	if IsSourceFile("data.json") {
		t.Error("data.json should not be a source file")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Go extractor
// ─────────────────────────────────────────────────────────────────────────────

const goSrc = `package mypackage

import "fmt"

// Add adds two ints.
func Add(a, b int) int {
	return a + b
}

type Server struct {
	port int
}

func (s *Server) Start() error {
	fmt.Println("start")
	return nil
}

type Handler interface {
	Handle() error
}
`

func TestExtractGo(t *testing.T) {
	result := Extract([]byte(goSrc), "Go", "mypackage/myfile.go")
	if result == nil {
		t.Fatal("Extract returned nil")
	}

	// Should have extracted: Add (Function), Server (Class), Start (Method), Handler (Interface)
	byName := make(map[string]ExtractedSymbol)
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}

	if _, ok := byName["Add"]; !ok {
		t.Error("expected symbol 'Add'")
	}
	if byName["Add"].Kind != "Function" {
		t.Errorf("Add.Kind = %q, want Function", byName["Add"].Kind)
	}
	if !byName["Add"].IsExported {
		t.Error("Add should be exported")
	}

	if _, ok := byName["Server"]; !ok {
		t.Error("expected symbol 'Server'")
	}
	if byName["Server"].Kind != "Class" {
		t.Errorf("Server.Kind = %q, want Class", byName["Server"].Kind)
	}

	if _, ok := byName["Start"]; !ok {
		t.Error("expected symbol 'Start'")
	}
	if byName["Start"].Kind != "Method" {
		t.Errorf("Start.Kind = %q, want Method", byName["Start"].Kind)
	}

	if _, ok := byName["Handler"]; !ok {
		t.Error("expected symbol 'Handler'")
	}
	if byName["Handler"].Kind != "Interface" {
		t.Errorf("Handler.Kind = %q, want Interface", byName["Handler"].Kind)
	}
}

func TestExtractGo_ByteOffsets(t *testing.T) {
	result := Extract([]byte(goSrc), "Go", "mypackage/myfile.go")
	if result == nil {
		t.Fatal("Extract returned nil")
	}
	for _, s := range result.Symbols {
		if s.StartByte >= s.EndByte {
			t.Errorf("symbol %q has start_byte(%d) >= end_byte(%d)", s.Name, s.StartByte, s.EndByte)
		}
		if s.StartLine <= 0 {
			t.Errorf("symbol %q has invalid start_line %d", s.Name, s.StartLine)
		}
	}
}

func TestExtractGo_DocstringCapture(t *testing.T) {
	result := Extract([]byte(goSrc), "Go", "mypackage/myfile.go")
	byName := make(map[string]ExtractedSymbol)
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	if !strings.Contains(byName["Add"].Docstring, "adds two ints") {
		t.Errorf("Add docstring = %q, want to contain 'adds two ints'", byName["Add"].Docstring)
	}
}

func TestExtractGo_CALLS_edges(t *testing.T) {
	result := Extract([]byte(goSrc), "Go", "mypackage/myfile.go")
	if result == nil {
		t.Fatal("nil result")
	}
	hasCallEdge := false
	for _, e := range result.Edges {
		if e.Kind == "CALLS" {
			hasCallEdge = true
			break
		}
	}
	// Start() calls fmt.Println — should produce a CALLS edge
	if !hasCallEdge {
		t.Error("expected at least one CALLS edge")
	}
}

func TestExtractGo_MainIsEntryPoint(t *testing.T) {
	src := []byte(`package main
func main() {}
`)
	result := Extract(src, "Go", "main.go")
	for _, s := range result.Symbols {
		if s.Name == "main" && !s.IsEntryPoint {
			t.Error("main() should be marked IsEntryPoint")
		}
	}
}

func TestExtractGo_TestFuncDetection(t *testing.T) {
	src := []byte(`package mypackage
import "testing"
func TestFoo(t *testing.T) {}
func BenchmarkBar(b *testing.B) {}
func normalFunc() {}
`)
	result := Extract(src, "Go", "mypackage/myfile_test.go")
	byName := make(map[string]ExtractedSymbol)
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	if !byName["TestFoo"].IsTest {
		t.Error("TestFoo should be IsTest")
	}
	if !byName["BenchmarkBar"].IsTest {
		t.Error("BenchmarkBar should be IsTest")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Python extractor
// ─────────────────────────────────────────────────────────────────────────────

const pySrc = `import os
from pathlib import Path

class MyClass:
    def method(self):
        pass

def standalone(x, y):
    return x + y
`

func TestExtractPython(t *testing.T) {
	result := Extract([]byte(pySrc), "Python", "mymod/myfile.py")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := make(map[string]ExtractedSymbol)
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	if _, ok := byName["MyClass"]; !ok {
		t.Error("expected symbol 'MyClass'")
	}
	if byName["MyClass"].Kind != "Class" {
		t.Errorf("MyClass.Kind = %q, want Class", byName["MyClass"].Kind)
	}
	if _, ok := byName["standalone"]; !ok {
		t.Error("expected symbol 'standalone'")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TypeScript extractor
// ─────────────────────────────────────────────────────────────────────────────

const tsSrc = `import { foo } from './foo';

export interface Greeter {
  greet(): string;
}

export class GreeterImpl implements Greeter {
  greet() { return 'hello'; }
}

export function createGreeter(): Greeter {
  return new GreeterImpl();
}
`

func TestExtractTypeScript(t *testing.T) {
	result := Extract([]byte(tsSrc), "TypeScript", "src/greeter.ts")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := make(map[string]ExtractedSymbol)
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	if _, ok := byName["Greeter"]; !ok {
		t.Error("expected symbol 'Greeter' (interface)")
	}
	if _, ok := byName["GreeterImpl"]; !ok {
		t.Error("expected symbol 'GreeterImpl' (class)")
	}
	if _, ok := byName["createGreeter"]; !ok {
		t.Error("expected symbol 'createGreeter' (function)")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Utility functions
// ─────────────────────────────────────────────────────────────────────────────

func TestBuildLineOffsets(t *testing.T) {
	src := []byte("line1\nline2\nline3")
	offsets := buildLineOffsets(src)
	if len(offsets) < 3 {
		t.Errorf("expected at least 3 offsets, got %d", len(offsets))
	}
	if offsets[0] != 0 {
		t.Errorf("first offset should be 0, got %d", offsets[0])
	}
	if offsets[1] != 6 {
		t.Errorf("second offset should be 6, got %d", offsets[1])
	}
}

func TestEstimateComplexity(t *testing.T) {
	simple := []byte("func f() { return 1 }")
	complex := []byte("func f() { if x { for i { if y { } } } }")
	sc := estimateComplexity(simple)
	cc := estimateComplexity(complex)
	if sc >= cc {
		t.Errorf("complex function should have higher complexity: simple=%d complex=%d", sc, cc)
	}
}

func TestExtractNilForEmpty(t *testing.T) {
	result := Extract([]byte{}, "Go", "empty.go")
	if result == nil {
		t.Error("Extract should never return nil")
	}
}

func TestExtractUnknownLanguage(t *testing.T) {
	result := Extract([]byte("some content"), "Zig", "file.zig")
	if result == nil {
		t.Error("Extract should return empty FileResult for unsupported language")
	}
	if len(result.Symbols) != 0 {
		t.Errorf("expected 0 symbols for unsupported language, got %d", len(result.Symbols))
	}
}
