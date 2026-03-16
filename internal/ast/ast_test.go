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

// ─────────────────────────────────────────────────────────────────────────────
// JavaScript extractor
// ─────────────────────────────────────────────────────────────────────────────

const jsSrc = `
function processOrder(order) {
  return order.total * 1.1;
}

class PaymentGateway {
  constructor(apiKey) {
    this.apiKey = apiKey;
  }
}

const fetchData = async (url) => {
  return fetch(url);
};

export function helper() {}
`

func TestExtractJavaScript(t *testing.T) {
	result := Extract([]byte(jsSrc), "JavaScript", "src/payments.js")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := make(map[string]ExtractedSymbol)
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	if _, ok := byName["processOrder"]; !ok {
		t.Error("expected symbol 'processOrder'")
	}
	if byName["processOrder"].Kind != "Function" {
		t.Errorf("processOrder.Kind = %q, want Function", byName["processOrder"].Kind)
	}
	if _, ok := byName["PaymentGateway"]; !ok {
		t.Error("expected symbol 'PaymentGateway'")
	}
	if byName["PaymentGateway"].Kind != "Class" {
		t.Errorf("PaymentGateway.Kind = %q, want Class", byName["PaymentGateway"].Kind)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Rust extractor
// ─────────────────────────────────────────────────────────────────────────────

const rustSrc = `use std::collections::HashMap;

pub struct Config {
    pub name: String,
}

pub trait Runnable {
    fn run(&self);
}

pub enum Status {
    Active,
    Inactive,
}

pub fn process(input: &str) -> String {
    input.to_uppercase()
}

fn helper() {}
`

func TestExtractRust(t *testing.T) {
	result := Extract([]byte(rustSrc), "Rust", "src/lib.rs")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := make(map[string]ExtractedSymbol)
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	if _, ok := byName["Config"]; !ok {
		t.Error("expected struct 'Config'")
	}
	if byName["Config"].Kind != "Class" {
		t.Errorf("Config.Kind = %q, want Class", byName["Config"].Kind)
	}
	if _, ok := byName["Runnable"]; !ok {
		t.Error("expected trait 'Runnable'")
	}
	if byName["Runnable"].Kind != "Interface" {
		t.Errorf("Runnable.Kind = %q, want Interface", byName["Runnable"].Kind)
	}
	if _, ok := byName["Status"]; !ok {
		t.Error("expected enum 'Status'")
	}
	if _, ok := byName["process"]; !ok {
		t.Error("expected function 'process'")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Java extractor
// ─────────────────────────────────────────────────────────────────────────────

const javaSrc = `import java.util.List;

public class OrderService {
    private final String name;

    public OrderService(String name) {
        this.name = name;
    }

    public List<String> getOrders() {
        return null;
    }
}

public interface Repository {
    void save(Object obj);
}

public enum OrderStatus {
    PENDING, FULFILLED
}
`

func TestExtractJava(t *testing.T) {
	result := Extract([]byte(javaSrc), "Java", "src/OrderService.java")
	if result == nil {
		t.Fatal("nil result")
	}
	// Java constructors share the class name, so iterate to find the class symbol.
	var foundClass, foundInterface, foundEnum bool
	for _, s := range result.Symbols {
		if s.Name == "OrderService" && s.Kind == "Class" {
			foundClass = true
		}
		if s.Name == "Repository" && s.Kind == "Interface" {
			foundInterface = true
		}
		if s.Name == "OrderStatus" && s.Kind == "Enum" {
			foundEnum = true
		}
	}
	if !foundClass {
		t.Error("expected class symbol 'OrderService'")
	}
	if !foundInterface {
		t.Error("expected interface symbol 'Repository'")
	}
	if !foundEnum {
		t.Error("expected enum symbol 'OrderStatus'")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Ruby extractor
// ─────────────────────────────────────────────────────────────────────────────

const rubySrc = `class Animal
  def initialize(name)
    @name = name
  end

  def speak
    "..."
  end
end

class Dog < Animal
  def speak
    "woof"
  end
end

def standalone_helper
  true
end
`

func TestExtractRuby(t *testing.T) {
	result := Extract([]byte(rubySrc), "Ruby", "lib/animal.rb")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := make(map[string]ExtractedSymbol)
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	if _, ok := byName["Animal"]; !ok {
		t.Error("expected class 'Animal'")
	}
	if byName["Animal"].Kind != "Class" {
		t.Errorf("Animal.Kind = %q, want Class", byName["Animal"].Kind)
	}
	if _, ok := byName["Dog"]; !ok {
		t.Error("expected class 'Dog'")
	}
	if _, ok := byName["speak"]; !ok {
		t.Error("expected method 'speak'")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// PHP extractor
// ─────────────────────────────────────────────────────────────────────────────

const phpSrc = `<?php

class UserController extends BaseController {
    public function index() {
        return view('users.index');
    }

    private function validate($request) {
        return true;
    }
}

function formatDate($date) {
    return date('Y-m-d', $date);
}
`

func TestExtractPHP(t *testing.T) {
	result := Extract([]byte(phpSrc), "PHP", "app/UserController.php")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := make(map[string]ExtractedSymbol)
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	if _, ok := byName["UserController"]; !ok {
		t.Error("expected class 'UserController'")
	}
	// Note: indented class methods (e.g. 'index') are not matched by the regex extractor.
	// Top-level functions without indentation are matched.
	if _, ok := byName["formatDate"]; !ok {
		t.Error("expected function 'formatDate'")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// C extractor
// ─────────────────────────────────────────────────────────────────────────────

const cSrc = `#include <stdio.h>
#include <stdlib.h>

int add(int a, int b) {
    return a + b;
}

static void helper(void) {
    printf("hello\n");
}

int main(int argc, char *argv[]) {
    return 0;
}
`

func TestExtractC(t *testing.T) {
	result := Extract([]byte(cSrc), "C", "src/main.c")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := make(map[string]ExtractedSymbol)
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	if _, ok := byName["add"]; !ok {
		t.Error("expected function 'add'")
	}
	if _, ok := byName["main"]; !ok {
		t.Error("expected function 'main'")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// C# extractor
// ─────────────────────────────────────────────────────────────────────────────

const csharpSrc = `using System;

public class OrderService : IService {
    private readonly string _name;

    public OrderService(string name) {
        _name = name;
    }

    public async Task<string> GetOrderAsync(int id) {
        return id.ToString();
    }

    private void Validate() {}
}

public interface IService {
    Task<string> GetOrderAsync(int id);
}
`

func TestExtractCSharp(t *testing.T) {
	result := Extract([]byte(csharpSrc), "C#", "Services/OrderService.cs")
	if result == nil {
		t.Fatal("nil result")
	}
	// C# constructors share the class name; iterate to find the class symbol.
	var foundClass, foundInterface bool
	for _, s := range result.Symbols {
		if s.Name == "OrderService" && s.Kind == "Class" {
			foundClass = true
		}
		if s.Name == "IService" && s.Kind == "Interface" {
			foundInterface = true
		}
	}
	if !foundClass {
		t.Error("expected class symbol 'OrderService'")
	}
	if !foundInterface {
		t.Error("expected interface symbol 'IService'")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Kotlin extractor
// ─────────────────────────────────────────────────────────────────────────────

const kotlinSrc = `import kotlinx.coroutines.*

data class User(val name: String, val age: Int)

class UserService {
    suspend fun fetchUser(id: Int): User {
        return User("Alice", 30)
    }

    fun validateUser(user: User): Boolean {
        return user.age >= 0
    }
}

fun main() {
    println("Hello")
}
`

func TestExtractKotlin(t *testing.T) {
	result := Extract([]byte(kotlinSrc), "Kotlin", "src/UserService.kt")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := make(map[string]ExtractedSymbol)
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	if _, ok := byName["User"]; !ok {
		t.Error("expected data class 'User'")
	}
	if _, ok := byName["UserService"]; !ok {
		t.Error("expected class 'UserService'")
	}
	if _, ok := byName["main"]; !ok {
		t.Error("expected function 'main'")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Swift extractor
// ─────────────────────────────────────────────────────────────────────────────

const swiftSrc = `import Foundation

protocol Drawable {
    func draw()
}

class Shape: Drawable {
    var color: String = "red"

    func draw() {
        print("drawing")
    }

    private func validate() -> Bool {
        return true
    }
}

public func createShape(color: String) -> Shape {
    return Shape()
}
`

func TestExtractSwift(t *testing.T) {
	result := Extract([]byte(swiftSrc), "Swift", "Sources/Shape.swift")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := make(map[string]ExtractedSymbol)
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	if _, ok := byName["Drawable"]; !ok {
		t.Error("expected protocol 'Drawable'")
	}
	if byName["Drawable"].Kind != "Interface" {
		t.Errorf("Drawable.Kind = %q, want Interface", byName["Drawable"].Kind)
	}
	if _, ok := byName["Shape"]; !ok {
		t.Error("expected class 'Shape'")
	}
	if _, ok := byName["createShape"]; !ok {
		t.Error("expected function 'createShape'")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Extract dispatch (all language branches)
// ─────────────────────────────────────────────────────────────────────────────

func TestExtract_JSX(t *testing.T) {
	src := []byte(`function MyComponent() { return null; }`)
	result := Extract(src, "JSX", "src/MyComponent.jsx")
	if result == nil {
		t.Fatal("nil result")
	}
}

func TestExtract_TSX(t *testing.T) {
	src := []byte(`export function Button(): JSX.Element { return null; }`)
	result := Extract(src, "TSX", "src/Button.tsx")
	if result == nil {
		t.Fatal("nil result")
	}
}

func TestExtract_CPP(t *testing.T) {
	src := []byte(`int compute(int x) { return x * 2; }`)
	result := Extract(src, "C++", "src/compute.cpp")
	if result == nil {
		t.Fatal("nil result")
	}
}
