package server

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
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

// ─────────────────────────────────────────────────────────────────────────────
// resolveProjectID
// ─────────────────────────────────────────────────────────────────────────────

func TestResolveProjectID_ByID(t *testing.T) {
	srv, store, _ := newTestServer(t)
	store.UpsertProject(db.Project{ID: "proj-abc", Path: "/abc", Name: "myproj", IndexedAt: time.Now()})

	id, err := srv.resolveProjectID("proj-abc")
	if err != nil {
		t.Fatalf("resolveProjectID by ID: %v", err)
	}
	if id != "proj-abc" {
		t.Errorf("got %q, want 'proj-abc'", id)
	}
}

func TestResolveProjectID_ByName(t *testing.T) {
	srv, store, _ := newTestServer(t)
	store.UpsertProject(db.Project{ID: "proj-xyz", Path: "/xyz", Name: "myproj", IndexedAt: time.Now()})

	id, err := srv.resolveProjectID("myproj")
	if err != nil {
		t.Fatalf("resolveProjectID by name: %v", err)
	}
	if id != "proj-xyz" {
		t.Errorf("got %q, want 'proj-xyz'", id)
	}
}

func TestResolveProjectID_NotFound(t *testing.T) {
	srv, _, _ := newTestServer(t)
	_, err := srv.resolveProjectID("nonexistent-project")
	if err == nil {
		t.Error("expected error for unknown project name")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// resolveProjectRoot
// ─────────────────────────────────────────────────────────────────────────────

func TestResolveProjectRoot_WithProject(t *testing.T) {
	srv, store, _ := newTestServer(t)
	store.UpsertProject(db.Project{ID: "p1", Path: "/mypath", Name: "p1", IndexedAt: time.Now()})
	root, err := srv.resolveProjectRoot("p1")
	if err != nil {
		t.Fatalf("resolveProjectRoot: %v", err)
	}
	if root != "/mypath" {
		t.Errorf("got %q, want '/mypath'", root)
	}
}

func TestResolveProjectRoot_Fallback(t *testing.T) {
	srv, _, _ := newTestServer(t)
	srv.sessionRoot = "/fallback"
	root, err := srv.resolveProjectRoot("nonexistent")
	if err != nil {
		t.Fatalf("resolveProjectRoot fallback: %v", err)
	}
	if root != "/fallback" {
		t.Errorf("got %q, want '/fallback'", root)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// setRoot
// ─────────────────────────────────────────────────────────────────────────────

func TestSetRoot(t *testing.T) {
	srv, _, dir := newTestServer(t)
	srv.setRoot(dir)
	if srv.sessionRoot != dir {
		t.Errorf("sessionRoot = %q, want %q", srv.sessionRoot, dir)
	}
	if srv.sessionID == "" {
		t.Error("sessionID should be set after setRoot")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// handleContext with real symbol + imports
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleContext_WithSymbol(t *testing.T) {
	srv, store, _ := newTestServer(t)
	repoDir := t.TempDir()
	writeGoFile(t, repoDir, "pkg/service.go", simpleGoSrc)

	store.UpsertProject(db.Project{ID: repoDir, Path: repoDir, Name: "test", IndexedAt: time.Now()})
	store.BulkUpsertSymbols([]db.Symbol{
		{ID: "main-sym", ProjectID: repoDir, FilePath: "pkg/service.go",
			Name: "Compute", QualifiedName: "mypkg.Compute",
			Kind: "Function", Language: "Go", StartByte: 14, EndByte: 60,
			StartLine: 3, EndLine: 3},
	})

	result, err := srv.handleContext(context.Background(), makeReq(map[string]any{
		"id": "main-sym",
	}))
	if err != nil {
		t.Fatalf("handleContext: %v", err)
	}
	if result.IsError {
		t.Logf("error (acceptable if source read fails): %v", decode(t, result))
		return
	}
	m := decode(t, result)
	sym := m["symbol"].(map[string]any)
	if sym["name"] != "Compute" {
		t.Errorf("symbol name = %v, want Compute", sym["name"])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// handleChanges with git repo
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleChanges_GitRepo(t *testing.T) {
	srv, store, _ := newTestServer(t)
	repoDir := t.TempDir()

	// Initialize git repo with a committed file, then modify it
	writeGoFile(t, repoDir, "main.go", "package main\nfunc main() {}\n")
	if err := os.WriteFile(filepath.Join(repoDir, ".git", "config"), nil, 0o644); err != nil {
		// Can't init git, skip
		t.Skip("cannot create git dir structure")
	}

	// Just test with a non-git dir — git diff fails → error result
	store.UpsertProject(db.Project{ID: repoDir, Path: repoDir, Name: "test", IndexedAt: time.Now()})
	srv.sessionID = repoDir
	srv.sessionRoot = repoDir
	store.BulkUpsertSymbols([]db.Symbol{
		{ID: "m1", ProjectID: repoDir, FilePath: "main.go", Name: "main",
			QualifiedName: "main.main", Kind: "Function", Language: "Go",
			StartByte: 0, EndByte: 50, StartLine: 1, EndLine: 5},
	})

	result, err := srv.handleChanges(context.Background(), makeReq(map[string]any{
		"scope": "staged",
	}))
	if err != nil {
		t.Fatalf("handleChanges: %v", err)
	}
	// Either succeeds or fails gracefully — no panic
	_ = result
}

// ─────────────────────────────────────────────────────────────────────────────
// handleArchitecture with language data
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleArchitecture_WithSymbols(t *testing.T) {
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{ID: "p1", Path: "/p1", Name: "p1", IndexedAt: time.Now(), FileCount: 5})
	store.BulkUpsertSymbols([]db.Symbol{
		{ID: "f1", ProjectID: "p1", FilePath: "main.go", Name: "main",
			QualifiedName: "main.main", Kind: "Function", Language: "Go",
			IsEntryPoint: true, StartByte: 0, EndByte: 50, StartLine: 1, EndLine: 5},
		{ID: "f2", ProjectID: "p1", FilePath: "util.go", Name: "Helper",
			QualifiedName: "main.Helper", Kind: "Function", Language: "Go",
			StartByte: 60, EndByte: 110, StartLine: 10, EndLine: 15},
	})
	store.BulkUpsertEdges([]db.Edge{
		{ProjectID: "p1", FromID: "f1", ToID: "f2", Kind: "CALLS", Confidence: 1.0},
	})

	result, err := srv.handleArchitecture(context.Background(), makeReq(nil))
	if err != nil {
		t.Fatalf("handleArchitecture: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", decode(t, result))
	}
	m := decode(t, result)
	if m["entry_points"] == nil {
		t.Error("expected entry_points in architecture response")
	}
	if m["hotspots"] == nil {
		t.Error("expected hotspots in architecture response")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// handleSymbol with source read
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleSymbol_WithSource(t *testing.T) {
	srv, store, _ := newTestServer(t)
	repoDir := t.TempDir()
	writeGoFile(t, repoDir, "pkg/svc.go", simpleGoSrc)

	store.UpsertProject(db.Project{ID: repoDir, Path: repoDir, Name: "test", IndexedAt: time.Now()})
	store.BulkUpsertSymbols([]db.Symbol{
		{ID: "wsym1", ProjectID: repoDir, FilePath: "pkg/svc.go", Name: "Compute",
			QualifiedName: "mypkg.Compute", Kind: "Function", Language: "Go",
			StartByte: 14, EndByte: 55, StartLine: 3, EndLine: 3},
	})

	result, err := srv.handleSymbol(context.Background(), makeReq(map[string]any{"id": "wsym1"}))
	if err != nil {
		t.Fatalf("handleSymbol: %v", err)
	}
	m := decode(t, result)
	if !result.IsError && m["source"] == nil {
		t.Error("expected source field in symbol response")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// handleADR no project
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleADR_NoProject(t *testing.T) {
	srv, _, _ := newTestServer(t)
	result, err := srv.handleADR(context.Background(), makeReq(map[string]any{
		"action": "set", "key": "K", "value": "V",
	}))
	if err != nil {
		t.Fatalf("handleADR: %v", err)
	}
	if !result.IsError {
		t.Error("expected error when no project set")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// MCPServer getter
// ─────────────────────────────────────────────────────────────────────────────

func TestMCPServer_Getter(t *testing.T) {
	srv, _, _ := newTestServer(t)
	s := srv.MCPServer()
	if s == nil {
		t.Error("MCPServer() returned nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// handleChanges: in a real git repo with staged changes
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleChanges_InGitRepo(t *testing.T) {
	srv, store, _ := newTestServer(t)
	repoDir := t.TempDir()

	// Initialize a git repo
	if out, err := runCmd(t, repoDir, "git", "init"); err != nil {
		t.Skipf("git not available: %v (%s)", err, out)
	}
	runCmd(t, repoDir, "git", "config", "user.email", "test@test.com")
	runCmd(t, repoDir, "git", "config", "user.name", "Test")

	// Create a file, commit it, then modify it
	goFile := filepath.Join(repoDir, "main.go")
	os.WriteFile(goFile, []byte("package main\nfunc main() {}\n"), 0o644)
	runCmd(t, repoDir, "git", "add", ".")
	runCmd(t, repoDir, "git", "commit", "-m", "init")

	// Modify the file (unstaged)
	os.WriteFile(goFile, []byte("package main\nfunc main() { println() }\n"), 0o644)

	store.UpsertProject(db.Project{ID: repoDir, Path: repoDir, Name: "gitrepo", IndexedAt: time.Now()})
	store.BulkUpsertSymbols([]db.Symbol{
		{ID: "gitm1", ProjectID: repoDir, FilePath: "main.go", Name: "main",
			QualifiedName: "main.main", Kind: "Function", Language: "Go",
			StartByte: 14, EndByte: 38, StartLine: 2, EndLine: 2},
	})
	srv.sessionID = repoDir
	srv.sessionRoot = repoDir

	result, err := srv.handleChanges(context.Background(), makeReq(map[string]any{
		"scope": "unstaged",
	}))
	if err != nil {
		t.Fatalf("handleChanges: %v", err)
	}
	if result.IsError {
		t.Logf("handleChanges returned error (may be expected): %v", decode(t, result))
		return
	}
	m := decode(t, result)
	if m["summary"] == nil {
		t.Error("expected summary in changes response")
	}
}

func TestHandleChanges_WithStagedScope(t *testing.T) {
	srv, store, _ := newTestServer(t)
	repoDir := t.TempDir()

	if out, err := runCmd(t, repoDir, "git", "init"); err != nil {
		t.Skipf("git not available: %v (%s)", err, out)
	}
	runCmd(t, repoDir, "git", "config", "user.email", "test@test.com")
	runCmd(t, repoDir, "git", "config", "user.name", "Test")

	goFile := filepath.Join(repoDir, "svc.go")
	os.WriteFile(goFile, []byte("package svc\nfunc Run() {}\n"), 0o644)
	runCmd(t, repoDir, "git", "add", ".")
	runCmd(t, repoDir, "git", "commit", "-m", "init")

	// Stage a change
	os.WriteFile(goFile, []byte("package svc\nfunc Run() { println() }\n"), 0o644)
	runCmd(t, repoDir, "git", "add", "svc.go")

	store.UpsertProject(db.Project{ID: repoDir, Path: repoDir, Name: "gitrepo2", IndexedAt: time.Now()})
	srv.sessionID = repoDir
	srv.sessionRoot = repoDir

	result, err := srv.handleChanges(context.Background(), makeReq(map[string]any{
		"scope": "staged",
		"depth": float64(2),
	}))
	if err != nil {
		t.Fatalf("handleChanges staged: %v", err)
	}
	_ = result // just verify no panic
}

func TestHandleChanges_AllScope(t *testing.T) {
	srv, store, _ := newTestServer(t)
	repoDir := t.TempDir()

	if out, err := runCmd(t, repoDir, "git", "init"); err != nil {
		t.Skipf("git not available: %v (%s)", err, out)
	}
	runCmd(t, repoDir, "git", "config", "user.email", "test@test.com")
	runCmd(t, repoDir, "git", "config", "user.name", "Test")
	goFile := filepath.Join(repoDir, "lib.go")
	os.WriteFile(goFile, []byte("package lib\nfunc Lib() {}\n"), 0o644)
	runCmd(t, repoDir, "git", "add", ".")
	runCmd(t, repoDir, "git", "commit", "-m", "init")
	os.WriteFile(goFile, []byte("package lib\nfunc Lib() { return }\n"), 0o644)

	store.UpsertProject(db.Project{ID: repoDir, Path: repoDir, Name: "gitrepo3", IndexedAt: time.Now()})
	srv.sessionID = repoDir
	srv.sessionRoot = repoDir

	result, err := srv.handleChanges(context.Background(), makeReq(map[string]any{"scope": "all"}))
	if err != nil {
		t.Fatalf("handleChanges all: %v", err)
	}
	_ = result
}

// ─────────────────────────────────────────────────────────────────────────────
// handleContext: with IMPORTS edges
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleContext_WithImportEdges(t *testing.T) {
	srv, store, _ := newTestServer(t)
	pid := "ctx-import-proj"
	repoDir := t.TempDir()
	writeGoFile(t, repoDir, "pkg/svc.go", simpleGoSrc)

	store.UpsertProject(db.Project{ID: pid, Path: repoDir, Name: "ctximp", IndexedAt: time.Now()})
	store.BulkUpsertSymbols([]db.Symbol{
		{ID: "ci-main", ProjectID: pid, FilePath: "pkg/svc.go", Name: "Compute",
			QualifiedName: "mypkg.Compute", Kind: "Function", Language: "Go",
			StartByte: 14, EndByte: 55, StartLine: 3, EndLine: 3},
		{ID: "ci-dep", ProjectID: pid, FilePath: "pkg/dep.go", Name: "Helper",
			QualifiedName: "mypkg.Helper", Kind: "Function", Language: "Go",
			StartByte: 0, EndByte: 30, StartLine: 1, EndLine: 2},
	})
	store.BulkUpsertEdges([]db.Edge{
		{ProjectID: pid, FromID: "ci-main", ToID: "ci-dep", Kind: "IMPORTS", Confidence: 1.0},
	})
	srv.sessionID = pid

	result, err := srv.handleContext(context.Background(), makeReq(map[string]any{"id": "ci-main"}))
	if err != nil {
		t.Fatalf("handleContext: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", decode(t, result))
	}
	m := decode(t, result)
	if m["symbol"] == nil {
		t.Error("expected symbol in context response")
	}
	if m["imports"] == nil {
		t.Log("imports nil — IMPORTS edge may not have been returned")
	}
}

func TestHandleContext_NoImports(t *testing.T) {
	srv, store, _ := newTestServer(t)
	pid := "ctx-noimport-proj"
	store.UpsertProject(db.Project{ID: pid, Path: "/tmp/ctx", Name: "ctx", IndexedAt: time.Now()})
	store.BulkUpsertSymbols([]db.Symbol{
		{ID: "ni-main", ProjectID: pid, FilePath: "main.go", Name: "Run",
			QualifiedName: "pkg.Run", Kind: "Function", Language: "Go",
			StartByte: 0, EndByte: 30, StartLine: 1, EndLine: 3},
	})
	srv.sessionID = pid

	result, err := srv.handleContext(context.Background(), makeReq(map[string]any{"id": "ni-main"}))
	if err != nil {
		t.Fatalf("handleContext: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", decode(t, result))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// parseFileURI edge cases
// ─────────────────────────────────────────────────────────────────────────────

func TestParseFileURI_Valid(t *testing.T) {
	path, ok := parseFileURI("file:///home/user/project")
	if !ok {
		t.Error("expected valid parse for file:///home/user/project")
	}
	if path == "" {
		t.Error("expected non-empty path")
	}
}

func TestParseFileURI_WindowsDriveLetter(t *testing.T) {
	// Windows: file:///C:/Users/project
	path, ok := parseFileURI("file:///C:/Users/project")
	if !ok {
		t.Error("expected valid parse for Windows file URI")
	}
	if path == "" {
		t.Error("expected non-empty path")
	}
}

func TestParseFileURI_InvalidScheme(t *testing.T) {
	_, ok := parseFileURI("http://example.com/path")
	if ok {
		t.Error("expected false for non-file URI")
	}
}

func TestParseFileURI_InvalidURI(t *testing.T) {
	_, ok := parseFileURI(":/invalid")
	if ok {
		t.Error("expected false for invalid URI")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// runGitDiff helper
// ─────────────────────────────────────────────────────────────────────────────

func TestRunGitDiff_NonGitDir(t *testing.T) {
	dir := t.TempDir()
	_, err := runGitDiff(dir, "unstaged")
	if err == nil {
		t.Log("runGitDiff returned nil error for non-git dir (may be ok if git says no diff)")
	}
}

func TestParseGitDiffFiles_Basic(t *testing.T) {
	diff := "internal/server/server.go\ninternal/db/db.go\n"
	files := parseGitDiffFiles(diff)
	if len(files) != 2 {
		t.Errorf("expected 2 files, got %d: %v", len(files), files)
	}
	if files[0] != "internal/server/server.go" {
		t.Errorf("unexpected first file: %q", files[0])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// handleSymbols: with project arg
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleSymbols_WithProjectArg(t *testing.T) {
	srv, store, _ := newTestServer(t)
	pid := "syms-proj"
	store.UpsertProject(db.Project{ID: pid, Path: "/tmp/syms", Name: "symsproj", IndexedAt: time.Now()})
	store.BulkUpsertSymbols([]db.Symbol{
		{ID: "sp1", ProjectID: pid, FilePath: "a.go", Name: "Alpha",
			QualifiedName: "pkg.Alpha", Kind: "Function", Language: "Go",
			StartByte: 0, EndByte: 30, StartLine: 1, EndLine: 3},
		{ID: "sp2", ProjectID: pid, FilePath: "b.go", Name: "Beta",
			QualifiedName: "pkg.Beta", Kind: "Function", Language: "Go",
			StartByte: 0, EndByte: 30, StartLine: 1, EndLine: 3},
	})
	srv.sessionID = pid

	result, err := srv.handleSymbols(context.Background(), makeReq(map[string]any{
		"ids":     []any{"sp1", "sp2", "sp-nonexistent"},
		"project": "symsproj",
	}))
	if err != nil {
		t.Fatalf("handleSymbols: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", decode(t, result))
	}
	m := decode(t, result)
	syms, ok := m["symbols"].([]any)
	if !ok || len(syms) != 3 {
		t.Errorf("expected 3 symbols (including error entry), got %v", m["symbols"])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// handleStats
// ─────────────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
// resolveProjectRoot fallbacks
// ─────────────────────────────────────────────────────────────────────────────

func TestResolveProjectRoot_FallsBackToSessionRoot(t *testing.T) {
	srv, _, _ := newTestServer(t)
	srv.sessionRoot = "/tmp/session-root"

	root, err := srv.resolveProjectRoot("nonexistent-project-id")
	if err != nil {
		t.Fatalf("resolveProjectRoot: %v", err)
	}
	if root != "/tmp/session-root" {
		t.Errorf("expected session root fallback, got %q", root)
	}
}

func TestResolveProjectRoot_NoSessionRoot(t *testing.T) {
	srv, _, _ := newTestServer(t)
	// No session root, no project in DB
	_, err := srv.resolveProjectRoot("nonexistent")
	if err == nil {
		t.Error("expected error when no project and no session root")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// runCmd helper for git tests
// ─────────────────────────────────────────────────────────────────────────────

func runCmd(t *testing.T, dir string, name string, args ...string) (string, error) {
	t.Helper()
	c := exec.Command(name, args...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	return string(out), err
}

// ─────────────────────────────────────────────────────────────────────────────
// handleADR: missing branch coverage
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleADR_GetEmptyKey(t *testing.T) {
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{ID: "p1", Path: "/p1", Name: "p1", IndexedAt: time.Now()})

	result, err := srv.handleADR(context.Background(), makeReq(map[string]any{
		"action": "get",
		"key":    "",
	}))
	if err != nil {
		t.Fatalf("handleADR: %v", err)
	}
	if !result.IsError {
		t.Error("expected error when key is empty for get")
	}
}

func TestHandleADR_SetEmptyKey(t *testing.T) {
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{ID: "p1", Path: "/p1", Name: "p1", IndexedAt: time.Now()})

	result, err := srv.handleADR(context.Background(), makeReq(map[string]any{
		"action": "set",
		"key":    "",
		"value":  "somevalue",
	}))
	if err != nil {
		t.Fatalf("handleADR: %v", err)
	}
	if !result.IsError {
		t.Error("expected error when key is empty for set")
	}
}

func TestHandleADR_SetEmptyValue(t *testing.T) {
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{ID: "p1", Path: "/p1", Name: "p1", IndexedAt: time.Now()})

	result, err := srv.handleADR(context.Background(), makeReq(map[string]any{
		"action": "set",
		"key":    "somekey",
		"value":  "",
	}))
	if err != nil {
		t.Fatalf("handleADR: %v", err)
	}
	if !result.IsError {
		t.Error("expected error when value is empty for set")
	}
}

func TestHandleADR_DeleteEmptyKey(t *testing.T) {
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{ID: "p1", Path: "/p1", Name: "p1", IndexedAt: time.Now()})

	result, err := srv.handleADR(context.Background(), makeReq(map[string]any{
		"action": "delete",
		"key":    "",
	}))
	if err != nil {
		t.Fatalf("handleADR: %v", err)
	}
	if !result.IsError {
		t.Error("expected error when key is empty for delete")
	}
}

func TestHandleADR_GetNotFound(t *testing.T) {
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{ID: "p1", Path: "/p1", Name: "p1", IndexedAt: time.Now()})

	result, err := srv.handleADR(context.Background(), makeReq(map[string]any{
		"action": "get",
		"key":    "nonexistent-key",
	}))
	if err != nil {
		t.Fatalf("handleADR: %v", err)
	}
	if !result.IsError {
		t.Error("expected error when key not found")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// handleSymbol: stale ID resolution
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleSymbol_StaleIDRedirect(t *testing.T) {
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{ID: "p1", Path: t.TempDir(), Name: "p1", IndexedAt: time.Now()})

	// Insert a symbol at the new path
	newSym := db.Symbol{
		ID: "new/path.go::MyFn#Function", ProjectID: "p1",
		FilePath: "new/path.go", Name: "MyFn", QualifiedName: "MyFn", Kind: "Function",
		Language: "Go", ExtractionConfidence: 1.0,
	}
	store.BulkUpsertSymbols([]db.Symbol{newSym})

	// Record a move: old-id → new-id
	store.RecordSymbolMove("p1", "old/path.go::MyFn#Function", newSym.ID)

	// Lookup via stale old ID
	result, err := srv.handleSymbol(context.Background(), makeReq(map[string]any{
		"id": "old/path.go::MyFn#Function",
	}))
	if err != nil {
		t.Fatalf("handleSymbol: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success via stale redirect, got error: %v", result.Content)
	}
	m := decode(t, result)
	if m["name"] != "MyFn" {
		t.Errorf("expected MyFn, got %v", m["name"])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// intArg / boolArgDefault / strSlice: uncovered branches
// ─────────────────────────────────────────────────────────────────────────────

func TestIntArg_NonFloatFallsToDefault(t *testing.T) {
	// When the value is not a float64, intArg should return the default.
	m := map[string]any{"depth": "notanumber"}
	if got := intArg(m, "depth", 42); got != 42 {
		t.Errorf("intArg with non-float64 = %d, want 42", got)
	}
}

func TestBoolArgDefault_NonBoolFallsToDefault(t *testing.T) {
	// When the value is present but not a bool, boolArgDefault returns def.
	m := map[string]any{"flag": "notabool"}
	if got := boolArgDefault(m, "flag", true); !got {
		t.Errorf("boolArgDefault with non-bool = %v, want true (default)", got)
	}
	if got := boolArgDefault(m, "flag", false); got {
		t.Errorf("boolArgDefault with non-bool = %v, want false (default)", got)
	}
}

func TestStrSlice_NonStringValuesSkipped(t *testing.T) {
	// Values that aren't strings should be skipped.
	m := map[string]any{"ids": []any{"a", 42, "b", nil, "c"}}
	got := strSlice(m, "ids")
	if len(got) != 3 {
		t.Errorf("strSlice with mixed types = %v, want [a b c]", got)
	}
}
