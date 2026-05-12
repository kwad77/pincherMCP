package db

import (
	"testing"
	"time"
)

// #626: hook_invocations data layer. These tests live in the internal/db
// package so the coverage tool attributes the hits correctly — the
// cmd/pinch hook_check_test.go exercises the same helpers but goes
// through the binary boundary which doesn't credit internal/db.

func TestLogHookInvocation_AndConversionRate(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UnixNano()

	// Three redirects, two taken. Conversion = 2/3 = 66.67%.
	if err := store.LogHookInvocation(HookInvocation{
		TS: now, SessionID: "s1", ToolName: "Read",
		FilePath: "f.go", FileBytes: 50000,
		Decision: "redirect", SuggestedTool: "context",
		SuggestedArgs: `{"id":"x"}`,
	}); err != nil {
		t.Fatalf("log 1: %v", err)
	}
	if err := store.LogHookInvocation(HookInvocation{
		TS: now + 1, SessionID: "s1", ToolName: "Grep",
		FilePath: "", FileBytes: 0,
		Decision: "redirect", SuggestedTool: "search",
		SuggestedArgs: `{"query":"Foo"}`,
	}); err != nil {
		t.Fatalf("log 2: %v", err)
	}
	if err := store.LogHookInvocation(HookInvocation{
		TS: now + 2, SessionID: "s1", ToolName: "Read",
		FilePath: "g.go", FileBytes: 60000,
		Decision: "redirect", SuggestedTool: "context",
		SuggestedArgs: `{"id":"y"}`,
	}); err != nil {
		t.Fatalf("log 3: %v", err)
	}
	// Pass-through (shouldn't count as a redirect).
	if err := store.LogHookInvocation(HookInvocation{
		TS: now + 3, SessionID: "s1", ToolName: "Read",
		FilePath: "tiny.txt", FileBytes: 100,
		Decision: "pass_through",
	}); err != nil {
		t.Fatalf("log 4: %v", err)
	}

	// Resolve. Two of the three redirects suggested `context`; the
	// third suggested `search`. The agent's next-3 calls include both
	// context AND search, so all three redirects resolve as taken.
	// (The metric is "did the agent end up calling the suggested tool"
	// — it doesn't try to attribute each individual call to a single
	// redirect.)
	calls := []HookSessionCall{
		{TS: now + 10, ToolName: "context"},
		{TS: now + 11, ToolName: "search"},
		{TS: now + 12, ToolName: "Edit"},
	}
	updated, err := store.ResolveHookInvocationsForSession("s1", calls)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if updated != 3 {
		t.Errorf("updated rows = %d, want 3", updated)
	}

	pct, redirects, taken, err := store.HookConversionRate7d()
	if err != nil {
		t.Fatalf("conversion rate: %v", err)
	}
	if redirects != 3 {
		t.Errorf("redirects = %d, want 3", redirects)
	}
	if taken != 3 {
		t.Errorf("taken = %d, want 3", taken)
	}
	if pct < 99.9 || pct > 100.1 {
		t.Errorf("pct = %.2f, want ~100", pct)
	}
}

// Asymmetric case: redirect suggests context, but agent never calls
// context within the next 3 tool calls. Resolves as taken=0.
func TestResolveHookInvocations_NotTakenWhenSuggestedToolMissing(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UnixNano()
	if err := store.LogHookInvocation(HookInvocation{
		TS: now, SessionID: "skip", ToolName: "Read",
		Decision: "redirect", SuggestedTool: "context",
	}); err != nil {
		t.Fatalf("log: %v", err)
	}
	// Next 3 calls: agent ignored the redirect.
	updated, err := store.ResolveHookInvocationsForSession("skip", []HookSessionCall{
		{TS: now + 1, ToolName: "Read"},
		{TS: now + 2, ToolName: "Edit"},
		{TS: now + 3, ToolName: "Bash"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if updated != 1 {
		t.Errorf("updated = %d, want 1", updated)
	}
	_, _, taken, err := store.HookConversionRate7d()
	if err != nil {
		t.Fatalf("conversion rate: %v", err)
	}
	if taken != 0 {
		t.Errorf("taken = %d, want 0 (agent ignored the redirect)", taken)
	}
}

func TestResolveHookInvocations_SkipsAlreadyResolved(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UnixNano()
	if err := store.LogHookInvocation(HookInvocation{
		TS: now, SessionID: "s2", ToolName: "Read",
		Decision: "redirect", SuggestedTool: "context",
	}); err != nil {
		t.Fatalf("log: %v", err)
	}
	// First resolve: agent took it.
	if _, err := store.ResolveHookInvocationsForSession("s2",
		[]HookSessionCall{{TS: now + 1, ToolName: "context"}}); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	// Second resolve with no new calls: should not re-process the
	// already-resolved row (took_recommendation IS NOT NULL filter).
	updated, err := store.ResolveHookInvocationsForSession("s2",
		[]HookSessionCall{{TS: now + 100, ToolName: "Edit"}})
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if updated != 0 {
		t.Errorf("second resolve updated %d rows; resolved rows must not be re-processed", updated)
	}
}

func TestResolveHookInvocations_SkipsWhenNoSubsequentCalls(t *testing.T) {
	// If the joiner runs before the agent has issued any next calls,
	// took_recommendation stays NULL — we don't have evidence yet.
	store := newTestStore(t)
	now := time.Now().UnixNano()
	if err := store.LogHookInvocation(HookInvocation{
		TS: now, SessionID: "s3", ToolName: "Read",
		Decision: "redirect", SuggestedTool: "context",
	}); err != nil {
		t.Fatalf("log: %v", err)
	}
	// Empty calls list: nothing strictly after `now` to inspect.
	updated, err := store.ResolveHookInvocationsForSession("s3", nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if updated != 0 {
		t.Errorf("no subsequent calls → no resolution; got updated=%d", updated)
	}
}

func TestIsFileIndexed_FileHashesPresence(t *testing.T) {
	store := newTestStore(t)
	if err := store.UpsertProject(Project{ID: "p1", Path: "/tmp/p1", Name: "p1"}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	if store.IsFileIndexed("p1", "f.go") {
		t.Error("file not indexed yet — should report false")
	}
	if err := store.SetFileHash("p1", "f.go", "abc"); err != nil {
		t.Fatalf("set hash: %v", err)
	}
	if !store.IsFileIndexed("p1", "f.go") {
		t.Error("after SetFileHash, IsFileIndexed should return true")
	}
}

func TestCountSymbolsInFile(t *testing.T) {
	store := newTestStore(t)
	if err := store.UpsertProject(Project{ID: "p1", Path: "/tmp/p1", Name: "p1"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	syms := []Symbol{
		{ID: "p1::a#Function", ProjectID: "p1", FilePath: "f.go", Name: "a", QualifiedName: "a", Kind: "Function", Language: "Go", ExtractionConfidence: 1.0},
		{ID: "p1::b#Function", ProjectID: "p1", FilePath: "f.go", Name: "b", QualifiedName: "b", Kind: "Function", Language: "Go", ExtractionConfidence: 1.0},
		{ID: "p1::c#Function", ProjectID: "p1", FilePath: "g.go", Name: "c", QualifiedName: "c", Kind: "Function", Language: "Go", ExtractionConfidence: 1.0},
	}
	if err := store.BulkUpsertSymbols(syms); err != nil {
		t.Fatalf("upsert syms: %v", err)
	}

	n, err := store.CountSymbolsInFile("p1", "f.go")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("count(f.go) = %d, want 2", n)
	}
	n, err = store.CountSymbolsInFile("p1", "g.go")
	if err != nil || n != 1 {
		t.Errorf("count(g.go) = %d (err=%v), want 1", n, err)
	}
	n, err = store.CountSymbolsInFile("p1", "missing.go")
	if err != nil || n != 0 {
		t.Errorf("count(missing.go) = %d (err=%v), want 0", n, err)
	}
}

func TestLargestSymbolInFile(t *testing.T) {
	store := newTestStore(t)
	if err := store.UpsertProject(Project{ID: "p1", Path: "/tmp/p1", Name: "p1"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	syms := []Symbol{
		{ID: "p1::small#Function", ProjectID: "p1", FilePath: "f.go", Name: "small", QualifiedName: "small", Kind: "Function", Language: "Go", StartByte: 0, EndByte: 50, ExtractionConfidence: 1.0},
		{ID: "p1::big#Function", ProjectID: "p1", FilePath: "f.go", Name: "big", QualifiedName: "big", Kind: "Function", Language: "Go", StartByte: 100, EndByte: 5000, ExtractionConfidence: 1.0},
		{ID: "p1::medium#Function", ProjectID: "p1", FilePath: "f.go", Name: "medium", QualifiedName: "medium", Kind: "Function", Language: "Go", StartByte: 6000, EndByte: 6500, ExtractionConfidence: 1.0},
	}
	if err := store.BulkUpsertSymbols(syms); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	id, err := store.LargestSymbolInFile("p1", "f.go")
	if err != nil {
		t.Fatalf("largest: %v", err)
	}
	if id != "p1::big#Function" {
		t.Errorf("largest = %q, want p1::big#Function", id)
	}

	// Empty file: returns ("", nil) — best-effort, not a hard error.
	id, err = store.LargestSymbolInFile("p1", "missing.go")
	if err != nil {
		t.Errorf("missing file should not error; got %v", err)
	}
	if id != "" {
		t.Errorf("missing file should return empty id; got %q", id)
	}
}
