package ast

import (
	"testing"
)

// #1328 v0.71: JavaScript AST extractor went default-on in v0.20.0
// (#266), but the langAdapter still registered confidence=0.85 (the
// regex fallback's honest floor). Pre-fix, every AST-extracted JS
// symbol stamped 0.85 → leaving no way to distinguish AST output from
// regex output in min_confidence filters or `pincher health`'s parser
// label. Fix: extractJavaScriptAST returns ConfidenceOverride=1.0,
// mirroring Python's #944 pattern.

func TestJavaScriptAST_ConfidenceOverride_BoostsSymbols_1328(t *testing.T) {
	// Force the AST path (default, but be explicit for the test).
	t.Setenv("PINCHER_DISABLE_JS_AST", "")
	t.Setenv("PINCHER_EXPERIMENTAL_JS_AST", "")

	src := []byte(`
class Foo {
  bar() { return 1; }
}
function baz() { return 2; }
const Q = (x) => x + 1;
`)
	result := ExtractWithModule(src, "JavaScript", "src/mod.js", "")
	if len(result.Symbols) == 0 {
		t.Fatal("expected JS symbols; got none")
	}
	for _, s := range result.Symbols {
		// AST path stamps 1.0 baseline → Compose may add signal bumps,
		// but it should NOT come back at the regex-tier 0.975 ceiling.
		// Threshold 0.99 matches the Python AST confidence test.
		if s.ExtractionConfidence < 0.99 {
			t.Errorf("symbol %q: confidence = %v, want >=0.99 (AST-extracted)",
				s.Name, s.ExtractionConfidence)
		}
	}
}

// Negative control: with PINCHER_DISABLE_JS_AST=1, the dispatcher
// routes to the regex extractor and symbols stamp the regex-tier
// confidence (≤0.99). Pre-fix THIS was the universal behaviour even
// without the opt-out — now it's only the opt-out path.
func TestJavaScriptAST_DisableOptOut_KeepsRegexConfidence_1328(t *testing.T) {
	t.Setenv("PINCHER_DISABLE_JS_AST", "1")

	src := []byte("function foo() { return 1; }\n")
	result := ExtractWithModule(src, "JavaScript", "src/mod.js", "")
	if len(result.Symbols) == 0 {
		t.Skip("JS regex extractor returned no symbols on the fixture")
	}
	for _, s := range result.Symbols {
		if s.ExtractionConfidence > 0.99 {
			t.Errorf("opt-out path symbol %q: confidence = %v, expected ≤0.99 (regex tier)",
				s.Name, s.ExtractionConfidence)
		}
	}
}

// JavaScriptASTEnabled mirrors PythonAvailable so /internal/server can
// upgrade the parser label at runtime. With both opt-out envs cleared,
// the default is true (post-v0.20.0).
func TestJavaScriptASTEnabled_DefaultsOn_1328(t *testing.T) {
	t.Setenv("PINCHER_DISABLE_JS_AST", "")
	t.Setenv("PINCHER_EXPERIMENTAL_JS_AST", "")
	if !JavaScriptASTEnabled() {
		t.Error("JavaScriptASTEnabled() = false with both env vars cleared; want true (default-on since v0.20.0)")
	}

	t.Setenv("PINCHER_DISABLE_JS_AST", "1")
	if JavaScriptASTEnabled() {
		t.Error("JavaScriptASTEnabled() = true with PINCHER_DISABLE_JS_AST=1; want false")
	}
}
