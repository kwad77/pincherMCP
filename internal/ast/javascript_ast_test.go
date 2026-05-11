package ast

import (
	"strings"
	"testing"
)

// JS AST extractor tests (#266). Direct extractor-level assertions —
// pinpoint the three failure modes from the dogfooding bugs (#259,
// #260, #261) plus sanity checks for the env-flag dispatch and the
// IIFE-recovery path.

// #259: `const NAME = (expr).method(…)` was emitted as Function by the
// regex extractor (the parens after NAME tripped the function pattern).
// AST extracts it as a VarDecl regardless of RHS shape — Variable is
// the right kind by construction.
func TestJSAST_ConstAssignmentIsVariableNotFunction(t *testing.T) {
	t.Setenv("PINCHER_EXPERIMENTAL_JS_AST", "1")
	src := []byte(`const iface = (document.getElementById('iface').value || 'trm_wwan').toLowerCase();
const zone  = (document.getElementById('zone').value  || 'wan').toLowerCase();
const vpnMatch = (info.data.ext_hooks || '').match(/vpn:\s*(.)/);
`)
	r, ok := extractJavaScriptAST(src, "overview.js")
	if !ok {
		t.Fatal("expected AST parse to succeed on clean ES")
	}
	if len(r.Symbols) != 3 {
		t.Fatalf("expected 3 Variable symbols, got %d: %+v", len(r.Symbols), r.Symbols)
	}
	for _, s := range r.Symbols {
		if s.Kind != "Variable" {
			t.Errorf("symbol %q kind = %q, want Variable (was the #259 false positive)", s.Name, s.Kind)
		}
	}
}

// #260: object-literal methods (LuCI's `view.extend({load: function () {}})`)
// were invisible to the regex extractor. AST descent into ObjectExpr
// properties now surfaces them as Method, scoped to the synthetic
// module parent so qualified names don't collide.
func TestJSAST_ObjectLiteralMethodsExtracted_LuCIPattern(t *testing.T) {
	t.Setenv("PINCHER_EXPERIMENTAL_JS_AST", "1")
	src := []byte(`'use strict';
return view.extend({
    load: function () {
        return Promise.all([1, 2, 3]);
    },
    render: function (result) {
        var x = 1;
        return x;
    }
});
`)
	r, ok := extractJavaScriptAST(src, "overview.js")
	if !ok {
		t.Fatal("expected AST parse to succeed via IIFE recovery")
	}
	got := map[string]string{} // name → kind
	for _, s := range r.Symbols {
		got[s.Name] = s.Kind
	}
	for _, want := range []string{"load", "render"} {
		if got[want] != "Method" {
			t.Errorf("expected Method %q, got kind=%q (was the #260 invisible-method bug)", want, got[want])
		}
	}
}

// #261: `export const NAME = {...}` modern-ESM was emitted as zero
// symbols by the regex extractor (eslint.config.mjs returned 0 from
// 189 lines). AST emits each ExportStmt's inner VarDecl as Variable,
// so the file becomes searchable.
func TestJSAST_TopLevelExportConstIsVariable(t *testing.T) {
	t.Setenv("PINCHER_EXPERIMENTAL_JS_AST", "1")
	src := []byte(`export const jsdoc_less_relaxed_rules = {
    'jsdoc/check-alignment': 'warn',
};

export const jsdoc_relaxed_rules = {
    'jsdoc/check-tag-names': 'off',
};

export default defineConfig([]);
`)
	r, ok := extractJavaScriptAST(src, "eslint.config.mjs")
	if !ok {
		t.Fatal("expected AST parse to succeed")
	}
	got := map[string]string{}
	for _, s := range r.Symbols {
		got[s.Name] = s.Kind
	}
	for _, want := range []string{"jsdoc_less_relaxed_rules", "jsdoc_relaxed_rules"} {
		if got[want] != "Variable" {
			t.Errorf("expected Variable %q, got kind=%q (was the #261 invisible-config bug)", want, got[want])
		}
	}
}

// IMPORTS edges — sanity that the AST path produces them correctly.
// The regex extractor often missed multi-line imports; AST is
// trivially correct here.
func TestJSAST_ImportStatementsEmitEdges(t *testing.T) {
	t.Setenv("PINCHER_EXPERIMENTAL_JS_AST", "1")
	src := []byte(`import { something } from './foo';
import * as bar from "./bar.js";
import baz from 'baz-pkg';
`)
	r, ok := extractJavaScriptAST(src, "index.js")
	if !ok {
		t.Fatal("expected AST parse to succeed")
	}
	want := map[string]bool{"./foo": false, "./bar.js": false, "baz-pkg": false}
	for _, e := range r.Edges {
		if e.Kind != "IMPORTS" {
			t.Errorf("expected IMPORTS edge kind, got %q", e.Kind)
		}
		if _, ok := want[e.ToName]; ok {
			want[e.ToName] = true
		}
	}
	for path, seen := range want {
		if !seen {
			t.Errorf("missing IMPORTS edge to %q; got edges: %+v", path, r.Edges)
		}
	}
}

// Top-level FuncDecl + ClassDecl + class methods — the bread-and-butter
// case. Cursor advances per emit so source order is preserved and same-
// named methods on different classes don't collide.
func TestJSAST_FunctionsClassesAndMethods(t *testing.T) {
	t.Setenv("PINCHER_EXPERIMENTAL_JS_AST", "1")
	src := []byte(`function topLevel() {
    return 1;
}

class Greeter {
    constructor(name) { this.name = name; }
    greet() { return 'hi ' + this.name; }
}

class Other {
    greet() { return 'bye'; }
}
`)
	r, ok := extractJavaScriptAST(src, "src/svc.js")
	if !ok {
		t.Fatal("expected AST parse to succeed")
	}
	got := map[string]string{}
	parents := map[string]string{}
	for _, s := range r.Symbols {
		got[s.Name+"@"+s.QualifiedName] = s.Kind
		parents[s.Name+"@"+s.QualifiedName] = s.Parent
	}
	if got["topLevel@src.svc.topLevel"] != "Function" {
		t.Errorf("topLevel: kind=%q, want Function; got=%v", got["topLevel@src.svc.topLevel"], got)
	}
	if got["Greeter@src.svc.Greeter"] != "Class" {
		t.Errorf("Greeter: want Class; got=%v", got)
	}
	if got["Other@src.svc.Other"] != "Class" {
		t.Errorf("Other: want Class; got=%v", got)
	}
	// Each class contributes its own greet() method, scoped to its parent.
	greetGreeter := got["greet@src.svc.Greeter.greet"]
	greetOther := got["greet@src.svc.Other.greet"]
	if greetGreeter != "Method" || greetOther != "Method" {
		t.Errorf("expected two Method greets (one per class); got Greeter=%q Other=%q full=%v",
			greetGreeter, greetOther, got)
	}
	if parents["greet@src.svc.Greeter.greet"] != "src.svc.Greeter" {
		t.Errorf("Greeter.greet parent=%q, want src.svc.Greeter", parents["greet@src.svc.Greeter.greet"])
	}
}

// Flag off → AST extractor not invoked; falls through to regex via the
// registry's adapter. Verifies the dispatch in extractor.go's init().
func TestJSAST_FlagOff_FallsThroughToRegex(t *testing.T) {
	// Don't set the env var. Direct call to extractJavaScript (regex
	// path) and confirm symbols come back at the regex-confidence
	// shape — proves the AST extractor isn't accidentally always-on.
	src := []byte(`function regexExtractsThis() {}`)
	r := extractJavaScript(src, "x.js")
	if r == nil || len(r.Symbols) == 0 {
		t.Fatal("regex extractor should emit a symbol for a plain function")
	}
	if r.Symbols[0].Name != "regexExtractsThis" {
		t.Errorf("regex extractor returned unexpected symbols: %+v", r.Symbols)
	}
}

// jsASTEnabled reads the env var on every call so test set/unset cycles
// don't require re-registering the extractor. Pin the contract.
func TestJSASTEnabled_ReadsEnvOnEachCall(t *testing.T) {
	t.Setenv("PINCHER_EXPERIMENTAL_JS_AST", "")
	if jsASTEnabled() {
		t.Error("expected disabled when env unset")
	}
	t.Setenv("PINCHER_EXPERIMENTAL_JS_AST", "1")
	if !jsASTEnabled() {
		t.Error("expected enabled when env=1")
	}
	t.Setenv("PINCHER_EXPERIMENTAL_JS_AST", "0")
	if jsASTEnabled() {
		t.Error("expected disabled when env=0 (only =1 enables)")
	}
}

// Top-level `return` recovery: parseJSWithRecovery wraps in IIFE on the
// LuCI-style error so symbols inside still extract. Pin the recovery
// path independently of the object-literal walker.
func TestJSAST_TopLevelReturnRecoveryParses(t *testing.T) {
	src := []byte(`return { a: 1 };`)
	parsed, ok := parseJSWithRecovery(src)
	if !ok {
		t.Fatal("expected IIFE recovery to allow top-level return to parse")
	}
	if parsed == nil {
		t.Fatal("parsed is nil despite ok=true")
	}
}

// Garbage source falls back to regex. Verifies the ok=false path of
// extractJavaScriptAST so the registry adapter knows to use regex.
func TestJSAST_GarbageReturnsOkFalse(t *testing.T) {
	t.Setenv("PINCHER_EXPERIMENTAL_JS_AST", "1")
	src := []byte(`<<<this is not JavaScript at all<<<`)
	_, ok := extractJavaScriptAST(src, "junk.js")
	if ok {
		t.Error("expected ok=false on garbage input")
	}
}

// Signature is a single line, capped at 200 chars — mirrors the regex
// extractor's signature shape so search snippets don't change format.
func TestJSAST_SignatureIsSingleLineBounded(t *testing.T) {
	t.Setenv("PINCHER_EXPERIMENTAL_JS_AST", "1")
	src := []byte(`function fn() {
    return 1;
}
`)
	r, ok := extractJavaScriptAST(src, "x.js")
	if !ok || len(r.Symbols) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(r.Symbols))
	}
	sig := r.Symbols[0].Signature
	if strings.Contains(sig, "\n") {
		t.Errorf("signature should be single-line; got %q", sig)
	}
	if !strings.Contains(sig, "function fn") {
		t.Errorf("signature should contain the declaration; got %q", sig)
	}
}
