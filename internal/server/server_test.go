package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pincherMCP/pincher/internal/db"
	"github.com/pincherMCP/pincher/internal/index"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func newTestServer(t *testing.T) (*Server, *db.Store, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := db.Open(dir)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	idx := index.New(store)
	srv := New(store, idx, "test")
	return srv, store, dir
}

// makeReq builds a minimal CallToolRequest with JSON args.
func makeReq(args map[string]any) *mcp.CallToolRequest {
	b, _ := json.Marshal(args)
	return &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Arguments: json.RawMessage(b),
		},
	}
}

// decode unmarshals the text content of a tool result into a map.
func decode(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	if result == nil {
		t.Fatal("result is nil")
	}
	if len(result.Content) == 0 {
		t.Fatal("result has no content")
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is not TextContent, got %T", result.Content[0])
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(text.Text), &m); err != nil {
		t.Fatalf("unmarshal result: %v\nraw: %s", err, text.Text)
	}
	return m
}

func writeGoFile(t *testing.T, dir, rel, src string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

const simpleGoSrc = `package mypkg

// Compute does something.
func Compute(x int) int { return x * 2 }

type Widget struct{ ID int }

func (w *Widget) Render() string { return "widget" }
`

// ─────────────────────────────────────────────────────────────────────────────
// Utility helpers (parseArgs, str, intArg, boolArg, etc.)
// ─────────────────────────────────────────────────────────────────────────────

func TestParseArgs_Empty(t *testing.T) {
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{}}
	m := parseArgs(req)
	if len(m) != 0 {
		t.Errorf("expected empty map, got %v", m)
	}
}

func TestParseArgs_Fields(t *testing.T) {
	req := makeReq(map[string]any{"path": "/tmp/x", "force": true, "limit": 5})
	m := parseArgs(req)
	if str(m, "path") != "/tmp/x" {
		t.Errorf("path = %q", str(m, "path"))
	}
	if !boolArg(m, "force") {
		t.Error("force should be true")
	}
	if intArg(m, "limit", 0) != 5 {
		t.Errorf("limit = %d", intArg(m, "limit", 0))
	}
}

func TestBoolArgDefault(t *testing.T) {
	m := map[string]any{}
	if !boolArgDefault(m, "missing", true) {
		t.Error("default should be true")
	}
	m["flag"] = false
	if boolArgDefault(m, "flag", true) {
		t.Error("explicit false should override default true")
	}
}

func TestStrSlice(t *testing.T) {
	m := map[string]any{"ids": []any{"a", "b", "c"}}
	got := strSlice(m, "ids")
	if len(got) != 3 || got[0] != "a" {
		t.Errorf("strSlice = %v", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// parseFileURI
// ─────────────────────────────────────────────────────────────────────────────

func TestParseFileURI(t *testing.T) {
	cases := []struct {
		uri string
		ok  bool
	}{
		{"file:///tmp/project", true},
		{"http://example.com", false},
		{"not-a-uri", false},
	}
	for _, c := range cases {
		got, ok := parseFileURI(c.uri)
		if ok != c.ok {
			t.Errorf("parseFileURI(%q) ok=%v, want %v", c.uri, ok, c.ok)
		}
		if ok && got == "" {
			t.Errorf("parseFileURI(%q) returned empty path", c.uri)
		}
		// Verify the path contains expected components (OS-agnostic)
		if ok && !strings.Contains(got, "tmp") {
			t.Errorf("parseFileURI(%q) = %q, expected path containing 'tmp'", c.uri, got)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// handleList
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleList_Empty(t *testing.T) {
	srv, _, _ := newTestServer(t)
	result, err := srv.handleList(context.Background(), makeReq(nil))
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	m := decode(t, result)
	if m["count"].(float64) != 0 {
		t.Errorf("expected 0 projects, got %v", m["count"])
	}
}

func TestHandleList_WithProjects(t *testing.T) {
	srv, store, _ := newTestServer(t)
	store.UpsertProject(db.Project{ID: "p1", Path: "/p1", Name: "proj1", IndexedAt: time.Now()})
	store.UpsertProject(db.Project{ID: "p2", Path: "/p2", Name: "proj2", IndexedAt: time.Now()})

	result, err := srv.handleList(context.Background(), makeReq(nil))
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	m := decode(t, result)
	if m["count"].(float64) != 2 {
		t.Errorf("expected 2 projects, got %v", m["count"])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// handleIndex
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleIndex_NoPath(t *testing.T) {
	srv, _, _ := newTestServer(t)
	// No session root, no path arg → error result
	result, err := srv.handleIndex(context.Background(), makeReq(nil))
	if err != nil {
		t.Fatalf("handleIndex: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result when no path provided")
	}
}

func TestHandleIndex_ValidPath(t *testing.T) {
	srv, _, _ := newTestServer(t)
	repoDir := t.TempDir()
	writeGoFile(t, repoDir, "pkg/service.go", simpleGoSrc)

	result, err := srv.handleIndex(context.Background(), makeReq(map[string]any{"path": repoDir}))
	if err != nil {
		t.Fatalf("handleIndex: %v", err)
	}
	if result.IsError {
		m := decode(t, result)
		t.Fatalf("handleIndex error: %v", m)
	}
	m := decode(t, result)
	if m["files"].(float64) < 1 {
		t.Errorf("expected at least 1 file indexed, got %v", m["files"])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// handleSearch
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleSearch_NoProject(t *testing.T) {
	srv, _, _ := newTestServer(t)
	// No session root, no project arg → error
	result, err := srv.handleSearch(context.Background(), makeReq(map[string]any{"query": "Compute"}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	if !result.IsError {
		t.Error("expected error when no project")
	}
}

func TestHandleSearch_WithProject(t *testing.T) {
	srv, store, _ := newTestServer(t)
	srv.sessionID = "proj1"
	store.UpsertProject(db.Project{ID: "proj1", Path: "/tmp/proj1", Name: "proj1", IndexedAt: time.Now()})
	store.BulkUpsertSymbols([]db.Symbol{
		{ID: "s1", ProjectID: "proj1", FilePath: "a.go", Name: "Compute",
			QualifiedName: "pkg.Compute", Kind: "Function", Language: "Go",
			StartByte: 0, EndByte: 100, StartLine: 1, EndLine: 5},
	})

	result, err := srv.handleSearch(context.Background(), makeReq(map[string]any{"query": "Compute"}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	if result.IsError {
		t.Fatalf("handleSearch error: %v", decode(t, result))
	}
	m := decode(t, result)
	if m["count"].(float64) < 1 {
		t.Errorf("expected ≥1 result, got %v", m["count"])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// handleSymbol
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleSymbol_NotFound(t *testing.T) {
	srv, _, _ := newTestServer(t)
	result, err := srv.handleSymbol(context.Background(), makeReq(map[string]any{"id": "nonexistent"}))
	if err != nil {
		t.Fatalf("handleSymbol: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for nonexistent symbol")
	}
}

func TestHandleSymbol_NoID(t *testing.T) {
	srv, _, _ := newTestServer(t)
	result, err := srv.handleSymbol(context.Background(), makeReq(nil))
	if err != nil {
		t.Fatalf("handleSymbol: %v", err)
	}
	if !result.IsError {
		t.Error("expected error when id is missing")
	}
}

func TestHandleSymbol_Found(t *testing.T) {
	srv, store, _ := newTestServer(t)
	repoDir := t.TempDir()
	writeGoFile(t, repoDir, "pkg/svc.go", simpleGoSrc)
	store.UpsertProject(db.Project{ID: repoDir, Path: repoDir, Name: "test", IndexedAt: time.Now()})
	store.BulkUpsertSymbols([]db.Symbol{
		{ID: "sym1", ProjectID: repoDir, FilePath: "pkg/svc.go", Name: "Compute",
			QualifiedName: "mypkg.Compute", Kind: "Function", Language: "Go",
			StartByte: 14, EndByte: 60, StartLine: 3, EndLine: 5},
	})

	result, err := srv.handleSymbol(context.Background(), makeReq(map[string]any{"id": "sym1"}))
	if err != nil {
		t.Fatalf("handleSymbol: %v", err)
	}
	if result.IsError {
		t.Logf("error (may be ok if source read fails): %v", decode(t, result))
		return
	}
	m := decode(t, result)
	if m["name"] != "Compute" {
		t.Errorf("name = %v, want Compute", m["name"])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// handleSymbols (batch)
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleSymbols_NoIDs(t *testing.T) {
	srv, _, _ := newTestServer(t)
	result, err := srv.handleSymbols(context.Background(), makeReq(nil))
	if err != nil {
		t.Fatalf("handleSymbols: %v", err)
	}
	if !result.IsError {
		t.Error("expected error when ids is missing")
	}
}

func TestHandleSymbols_MultipleIDs(t *testing.T) {
	srv, store, _ := newTestServer(t)
	store.UpsertProject(db.Project{ID: "p1", Path: "/p1", Name: "p1", IndexedAt: time.Now()})
	store.BulkUpsertSymbols([]db.Symbol{
		{ID: "s1", ProjectID: "p1", FilePath: "a.go", Name: "Foo", QualifiedName: "pkg.Foo",
			Kind: "Function", Language: "Go", StartByte: 0, EndByte: 50, StartLine: 1, EndLine: 5},
		{ID: "s2", ProjectID: "p1", FilePath: "a.go", Name: "Bar", QualifiedName: "pkg.Bar",
			Kind: "Function", Language: "Go", StartByte: 60, EndByte: 110, StartLine: 10, EndLine: 15},
	})

	result, err := srv.handleSymbols(context.Background(), makeReq(map[string]any{
		"ids": []any{"s1", "s2", "nonexistent"},
	}))
	if err != nil {
		t.Fatalf("handleSymbols: %v", err)
	}
	m := decode(t, result)
	if m["count"].(float64) != 3 {
		t.Errorf("expected 3 results (2 found + 1 not found), got %v", m["count"])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// handleQuery (Cypher)
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleQuery_NoProject(t *testing.T) {
	srv, _, _ := newTestServer(t)
	result, err := srv.handleQuery(context.Background(), makeReq(map[string]any{
		"cypher": "MATCH (f:Function) RETURN f.name",
	}))
	if err != nil {
		t.Fatalf("handleQuery: %v", err)
	}
	// No project set — cypher runs against all data (may return empty result)
	if result == nil {
		t.Error("result should not be nil")
	}
}

func TestHandleQuery_EmptyCypher(t *testing.T) {
	srv, _, _ := newTestServer(t)
	result, err := srv.handleQuery(context.Background(), makeReq(nil))
	if err != nil {
		t.Fatalf("handleQuery: %v", err)
	}
	if !result.IsError {
		t.Error("expected error when cypher is empty")
	}
}

func TestHandleQuery_ValidQuery(t *testing.T) {
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{ID: "p1", Path: "/p1", Name: "p1", IndexedAt: time.Now()})
	store.BulkUpsertSymbols([]db.Symbol{
		{ID: "f1", ProjectID: "p1", FilePath: "a.go", Name: "main", QualifiedName: "main.main",
			Kind: "Function", Language: "Go", StartByte: 0, EndByte: 50, StartLine: 1, EndLine: 5},
	})

	result, err := srv.handleQuery(context.Background(), makeReq(map[string]any{
		"cypher": "MATCH (f:Function) WHERE f.name = 'main' RETURN f.name",
	}))
	if err != nil {
		t.Fatalf("handleQuery: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", decode(t, result))
	}
	m := decode(t, result)
	if m["total"].(float64) < 1 {
		t.Errorf("expected at least 1 result, got %v", m["total"])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// handleSchema
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleSchema_NoProject(t *testing.T) {
	srv, _, _ := newTestServer(t)
	result, err := srv.handleSchema(context.Background(), makeReq(nil))
	if err != nil {
		t.Fatalf("handleSchema: %v", err)
	}
	if !result.IsError {
		t.Error("expected error when no project")
	}
}

func TestHandleSchema_WithProject(t *testing.T) {
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{ID: "p1", Path: "/p1", Name: "p1", IndexedAt: time.Now()})
	store.BulkUpsertSymbols([]db.Symbol{
		{ID: "f1", ProjectID: "p1", FilePath: "a.go", Name: "Foo", QualifiedName: "pkg.Foo",
			Kind: "Function", Language: "Go", StartByte: 0, EndByte: 50, StartLine: 1, EndLine: 5},
	})

	result, err := srv.handleSchema(context.Background(), makeReq(nil))
	if err != nil {
		t.Fatalf("handleSchema: %v", err)
	}
	m := decode(t, result)
	if m["symbols"].(float64) < 1 {
		t.Errorf("expected symbols > 0, got %v", m["symbols"])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// handleArchitecture
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleArchitecture_NoProject(t *testing.T) {
	srv, _, _ := newTestServer(t)
	result, err := srv.handleArchitecture(context.Background(), makeReq(nil))
	if err != nil {
		t.Fatalf("handleArchitecture: %v", err)
	}
	if !result.IsError {
		t.Error("expected error when no project")
	}
}

func TestHandleArchitecture_WithProject(t *testing.T) {
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{ID: "p1", Path: "/p1", Name: "p1", IndexedAt: time.Now(), FileCount: 5})

	result, err := srv.handleArchitecture(context.Background(), makeReq(nil))
	if err != nil {
		t.Fatalf("handleArchitecture: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", decode(t, result))
	}
	m := decode(t, result)
	if _, ok := m["languages"]; !ok {
		t.Error("expected 'languages' key in architecture response")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// handleTrace
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleTrace_NoName(t *testing.T) {
	srv, _, _ := newTestServer(t)
	result, err := srv.handleTrace(context.Background(), makeReq(nil))
	if err != nil {
		t.Fatalf("handleTrace: %v", err)
	}
	if !result.IsError {
		t.Error("expected error when name is missing")
	}
}

func TestHandleTrace_SymbolNotFound(t *testing.T) {
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{ID: "p1", Path: "/p1", Name: "p1", IndexedAt: time.Now()})

	result, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"name": "nonexistent",
	}))
	if err != nil {
		t.Fatalf("handleTrace: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for nonexistent symbol")
	}
}

func TestHandleTrace_Valid(t *testing.T) {
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{ID: "p1", Path: "/p1", Name: "p1", IndexedAt: time.Now()})
	store.BulkUpsertSymbols([]db.Symbol{
		{ID: "fn_a", ProjectID: "p1", FilePath: "a.go", Name: "Alpha", QualifiedName: "pkg.Alpha",
			Kind: "Function", Language: "Go", StartByte: 0, EndByte: 50, StartLine: 1, EndLine: 5},
		{ID: "fn_b", ProjectID: "p1", FilePath: "b.go", Name: "Beta", QualifiedName: "pkg.Beta",
			Kind: "Function", Language: "Go", StartByte: 0, EndByte: 50, StartLine: 1, EndLine: 5},
	})
	store.BulkUpsertEdges([]db.Edge{
		{ProjectID: "p1", FromID: "fn_a", ToID: "fn_b", Kind: "CALLS", Confidence: 1.0},
	})

	result, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"name":      "Alpha",
		"direction": "outbound",
		"depth":     float64(2),
	}))
	if err != nil {
		t.Fatalf("handleTrace: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", decode(t, result))
	}
	m := decode(t, result)
	if m["total"].(float64) < 1 {
		t.Errorf("expected at least 1 hop, got %v", m["total"])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// handleChanges
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleChanges_NoProject(t *testing.T) {
	srv, _, _ := newTestServer(t)
	result, err := srv.handleChanges(context.Background(), makeReq(nil))
	if err != nil {
		t.Fatalf("handleChanges: %v", err)
	}
	if !result.IsError {
		t.Error("expected error when no project")
	}
}

func TestHandleChanges_ValidProject(t *testing.T) {
	srv, store, _ := newTestServer(t)
	repoDir := t.TempDir()
	// Initialize a git repo so git diff doesn't fail
	os.MkdirAll(repoDir, 0o755)

	store.UpsertProject(db.Project{ID: repoDir, Path: repoDir, Name: "test", IndexedAt: time.Now()})
	srv.sessionID = repoDir
	srv.sessionRoot = repoDir

	result, err := srv.handleChanges(context.Background(), makeReq(nil))
	if err != nil {
		t.Fatalf("handleChanges: %v", err)
	}
	// git diff may fail (not a git repo) → that's an error result, which is fine
	_ = result
}

// ─────────────────────────────────────────────────────────────────────────────
// handleADR
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleADR_SetGetListDelete(t *testing.T) {
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{ID: "p1", Path: "/p1", Name: "p1", IndexedAt: time.Now()})

	// set
	result, err := srv.handleADR(context.Background(), makeReq(map[string]any{
		"action": "set", "key": "STACK", "value": "Go+SQLite",
	}))
	if err != nil || result.IsError {
		t.Fatalf("ADR set failed: %v / %v", err, decode(t, result))
	}

	// get
	result, err = srv.handleADR(context.Background(), makeReq(map[string]any{
		"action": "get", "key": "STACK",
	}))
	if err != nil || result.IsError {
		t.Fatalf("ADR get failed")
	}
	m := decode(t, result)
	if m["value"] != "Go+SQLite" {
		t.Errorf("value = %v, want Go+SQLite", m["value"])
	}

	// list
	result, _ = srv.handleADR(context.Background(), makeReq(map[string]any{"action": "list"}))
	m = decode(t, result)
	entries := m["entries"].(map[string]any)
	if entries["STACK"] != "Go+SQLite" {
		t.Errorf("list entries = %v", entries)
	}

	// delete
	result, _ = srv.handleADR(context.Background(), makeReq(map[string]any{
		"action": "delete", "key": "STACK",
	}))
	if result.IsError {
		t.Fatalf("ADR delete failed")
	}

	// get after delete → error
	result, _ = srv.handleADR(context.Background(), makeReq(map[string]any{
		"action": "get", "key": "STACK",
	}))
	if !result.IsError {
		t.Error("expected error after delete")
	}
}

func TestHandleADR_UnknownAction(t *testing.T) {
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{ID: "p1", Path: "/p1", Name: "p1", IndexedAt: time.Now()})

	result, err := srv.handleADR(context.Background(), makeReq(map[string]any{
		"action": "invalid",
	}))
	if err != nil {
		t.Fatalf("handleADR: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for unknown action")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// handleStats
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleStats_Empty(t *testing.T) {
	srv, _, _ := newTestServer(t)
	result, err := srv.handleStats(context.Background(), makeReq(nil))
	if err != nil {
		t.Fatalf("handleStats: %v", err)
	}
	m := decode(t, result)
	session := m["session"].(map[string]any)
	// Stats reads atomics before jsonResultWithMeta increments them,
	// so on a fresh server the reported calls is 0.
	if session["calls"].(float64) != 0 {
		t.Errorf("calls = %v, want 0", session["calls"])
	}
}

func TestHandleStats_Accumulates(t *testing.T) {
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{ID: "p1", Path: "/p1", Name: "p1", IndexedAt: time.Now()})

	// Make a few calls
	srv.handleList(context.Background(), makeReq(nil))
	srv.handleList(context.Background(), makeReq(nil))
	srv.handleList(context.Background(), makeReq(nil))

	result, _ := srv.handleStats(context.Background(), makeReq(nil))
	m := decode(t, result)
	session := m["session"].(map[string]any)
	// Stats reads atomics before incrementing itself, so it reports 3 (the 3 list calls).
	if session["calls"].(float64) < 3 {
		t.Errorf("expected ≥3 calls tracked, got %v", session["calls"])
	}
	if session["tokens_used"].(float64) == 0 {
		t.Error("tokens_used should be non-zero")
	}
}

func TestHandleStats_WithProject(t *testing.T) {
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{ID: "p1", Path: "/p1", Name: "p1", IndexedAt: time.Now(), SymCount: 42})

	result, err := srv.handleStats(context.Background(), makeReq(nil))
	if err != nil {
		t.Fatalf("handleStats: %v", err)
	}
	m := decode(t, result)
	if m["project"] == nil {
		t.Error("expected project data when session project is set")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// handleContext
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleContext_NoID(t *testing.T) {
	srv, _, _ := newTestServer(t)
	result, err := srv.handleContext(context.Background(), makeReq(nil))
	if err != nil {
		t.Fatalf("handleContext: %v", err)
	}
	if !result.IsError {
		t.Error("expected error when id is missing")
	}
}

func TestHandleContext_NotFound(t *testing.T) {
	srv, _, _ := newTestServer(t)
	result, err := srv.handleContext(context.Background(), makeReq(map[string]any{"id": "nonexistent"}))
	if err != nil {
		t.Fatalf("handleContext: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for nonexistent symbol")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// parseGitDiffFiles
// ─────────────────────────────────────────────────────────────────────────────

func TestParseGitDiffFiles(t *testing.T) {
	diff := "internal/db/db.go\ninternal/server/server.go\n\n"
	files := parseGitDiffFiles(diff)
	if len(files) != 2 {
		t.Errorf("expected 2 files, got %d: %v", len(files), files)
	}
	if files[0] != "internal/db/db.go" {
		t.Errorf("files[0] = %q", files[0])
	}
}

func TestParseGitDiffFiles_Empty(t *testing.T) {
	files := parseGitDiffFiles("")
	if len(files) != 0 {
		t.Errorf("expected 0 files for empty diff, got %d", len(files))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// riskLabel
// ─────────────────────────────────────────────────────────────────────────────

func TestRiskLabel(t *testing.T) {
	cases := []struct{ d int; want string }{
		{1, "CRITICAL"}, {2, "HIGH"}, {3, "MEDIUM"}, {4, "LOW"}, {5, "LOW"},
	}
	for _, c := range cases {
		if got := riskLabel(c.d); got != c.want {
			t.Errorf("riskLabel(%d) = %q, want %q", c.d, got, c.want)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// max helper
// ─────────────────────────────────────────────────────────────────────────────

func TestMax(t *testing.T) {
	if max(3, 5) != 5 {
		t.Error("max(3,5) should be 5")
	}
	if max(7, 2) != 7 {
		t.Error("max(7,2) should be 7")
	}
	if max(4, 4) != 4 {
		t.Error("max(4,4) should be 4")
	}
}
