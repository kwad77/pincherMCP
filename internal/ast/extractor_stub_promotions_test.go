package ast

import (
	"strings"
	"testing"
)

// #1161 v0.63: Lua / Elixir / Zig promoted from stub-tier (0.0,
// always-empty FileResult) to regex-tier (0.70). Tests follow the
// table-from-the-start shape (#1152): positive (function symbol
// extracted), positive (CALLS edges emitted from body), control
// (Scala/Haskell/Dart/R remain stub — deliberate deferral).

// LUA --------------------------------------------------------------

const luaSrc = `local function bootstrap()
  load_config()
  parse_config()
  render()
end

function module.helper()
  return 42
end

local function private_helper()
  return 1
end
`

func TestExtractLua_PromotedToRegex_ExtractsFunctions(t *testing.T) {
	r := Extract([]byte(luaSrc), "Lua", "src/main.lua")
	if r == nil {
		t.Fatal("nil result")
	}
	want := map[string]bool{
		"bootstrap":      false,
		"helper":         false,
		"private_helper": false,
	}
	for _, s := range r.Symbols {
		if s.Kind != "Function" {
			continue
		}
		if _, expected := want[s.Name]; expected {
			want[s.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("Lua function %q not extracted", name)
		}
	}
}

func TestExtractLua_PerFileCalls_EmitsEdges(t *testing.T) {
	r := Extract([]byte(luaSrc), "Lua", "src/main.lua")
	if r == nil {
		t.Fatal("nil result")
	}
	wantTargets := map[string]bool{
		"load_config":  false,
		"parse_config": false,
		"render":       false,
	}
	for _, e := range r.Edges {
		if e.Kind != "CALLS" || !strings.HasSuffix(e.FromQN, "bootstrap") {
			continue
		}
		if _, expected := wantTargets[e.ToName]; expected {
			wantTargets[e.ToName] = true
		}
	}
	for target, found := range wantTargets {
		if !found {
			t.Errorf("Lua: expected CALLS edge bootstrap → %q; missing", target)
		}
	}
}

// ZIG --------------------------------------------------------------

const zigSrc = `pub fn bootstrap() void {
    load_config();
    const c = parse_config();
    render(c);
}

fn private_helper() i32 {
    return 1;
}

export fn entry_point() void {
    bootstrap();
}
`

func TestExtractZig_PromotedToRegex_ExtractsFunctions(t *testing.T) {
	r := Extract([]byte(zigSrc), "Zig", "src/main.zig")
	if r == nil {
		t.Fatal("nil result")
	}
	want := map[string]bool{
		"bootstrap":      false,
		"private_helper": false,
		"entry_point":    false,
	}
	for _, s := range r.Symbols {
		if s.Kind != "Function" {
			continue
		}
		if _, expected := want[s.Name]; expected {
			want[s.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("Zig function %q not extracted", name)
		}
	}
}

func TestExtractZig_PerFileCalls_EmitsEdges(t *testing.T) {
	r := Extract([]byte(zigSrc), "Zig", "src/main.zig")
	if r == nil {
		t.Fatal("nil result")
	}
	wantTargets := map[string]bool{
		"load_config":  false,
		"parse_config": false,
		"render":       false,
	}
	for _, e := range r.Edges {
		if e.Kind != "CALLS" || !strings.HasSuffix(e.FromQN, "bootstrap") {
			continue
		}
		if _, expected := wantTargets[e.ToName]; expected {
			wantTargets[e.ToName] = true
		}
	}
	for target, found := range wantTargets {
		if !found {
			t.Errorf("Zig: expected CALLS edge bootstrap → %q; missing", target)
		}
	}
}

// ELIXIR -----------------------------------------------------------

const elixirSrc = `defmodule Bootstrap do
  def run do
    load_config()
    parse_config()
    render()
  end

  defp private_helper do
    42
  end

  defmacro guard_macro do
    quote do: nil
  end
end
`

func TestExtractElixir_PromotedToRegex_ExtractsFunctions(t *testing.T) {
	r := Extract([]byte(elixirSrc), "Elixir", "lib/bootstrap.ex")
	if r == nil {
		t.Fatal("nil result")
	}
	want := map[string]bool{
		"run":            false,
		"private_helper": false,
		"guard_macro":    false,
	}
	// Inside `defmodule Bootstrap do ... end` the regex extractor's
	// currentClass tracker scopes def/defp/defmacro as Methods.
	// Either kind is acceptable — the assertion is "the symbol
	// surfaces at all"; the kind delineation matches Elixir's
	// module-as-class shape.
	for _, s := range r.Symbols {
		if s.Kind != "Function" && s.Kind != "Method" {
			continue
		}
		if _, expected := want[s.Name]; expected {
			want[s.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("Elixir def %q not extracted", name)
		}
	}
}

// Cross-check: defmodule produces a Class symbol so callers can
// scope queries by module.
func TestExtractElixir_DefmoduleEmitsClass(t *testing.T) {
	r := Extract([]byte(elixirSrc), "Elixir", "lib/bootstrap.ex")
	if r == nil {
		t.Fatal("nil result")
	}
	for _, s := range r.Symbols {
		if s.Kind == "Class" && s.Name == "Bootstrap" {
			return
		}
	}
	t.Error("defmodule Bootstrap not surfaced as a Class symbol")
}

func TestExtractElixir_PerFileCalls_EmitsEdges(t *testing.T) {
	r := Extract([]byte(elixirSrc), "Elixir", "lib/bootstrap.ex")
	if r == nil {
		t.Fatal("nil result")
	}
	wantTargets := map[string]bool{
		"load_config":  false,
		"parse_config": false,
		"render":       false,
	}
	for _, e := range r.Edges {
		if e.Kind != "CALLS" || !strings.HasSuffix(e.FromQN, "run") {
			continue
		}
		if _, expected := wantTargets[e.ToName]; expected {
			wantTargets[e.ToName] = true
		}
	}
	for target, found := range wantTargets {
		if !found {
			t.Errorf("Elixir: expected CALLS edge run → %q; missing", target)
		}
	}
}

// Control: Scala / Haskell / Dart / R remain stub-tier — extractor
// returns an empty FileResult. Pins the v0.63 deferral decision so
// a future regex extractor for any of these has to opt in to a
// non-stub registration and this test then fails loudly, prompting
// proper test coverage for the new extractor.
func TestExtractStubTier_RemainsEmpty(t *testing.T) {
	cases := []struct{ lang, src, path string }{
		{"Scala", "class Foo { def bar() = 1 }", "src/Foo.scala"},
		{"Haskell", "module M where\nfoo :: Int\nfoo = 42\n", "src/M.hs"},
		{"Dart", "void main() { print('hi'); }", "src/main.dart"},
		{"R", "foo <- function(x) { x + 1 }", "src/foo.r"},
	}
	for _, c := range cases {
		r := Extract([]byte(c.src), c.lang, c.path)
		if r == nil {
			continue // also acceptable as "stub"
		}
		if len(r.Symbols) > 0 {
			t.Errorf("%s should still be stub-tier in v0.63; got %d symbols. If you implemented an extractor, update this test to cover it.",
				c.lang, len(r.Symbols))
		}
	}
}
