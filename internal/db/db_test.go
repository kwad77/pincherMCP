package db

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helper: in-memory store
// ─────────────────────────────────────────────────────────────────────────────

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func testProject(id string) Project {
	return Project{
		ID:        id,
		Path:      "/tmp/" + id,
		Name:      id,
		IndexedAt: time.Now().Truncate(time.Second),
	}
}

func testSymbol(id, name, kind, projectID, filePath string) Symbol {
	return Symbol{
		ID:            id,
		ProjectID:     projectID,
		FilePath:      filePath,
		Name:          name,
		QualifiedName: name,
		Kind:          kind,
		Language:      "Go",
		StartByte:     0,
		EndByte:       100,
		StartLine:     1,
		EndLine:       10,
		IsExported:    true,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// DataDir
// ─────────────────────────────────────────────────────────────────────────────

func TestDataDir(t *testing.T) {
	dir, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir: %v", err)
	}
	if dir == "" {
		t.Error("DataDir returned empty string")
	}
	// Should be a valid directory
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("DataDir %q does not exist: %v", dir, err)
	}
	if !info.IsDir() {
		t.Errorf("DataDir %q is not a directory", dir)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Open / migrate
// ─────────────────────────────────────────────────────────────────────────────

func TestOpen(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if s.Path != filepath.Join(dir, "pincher.db") {
		t.Errorf("Path = %q, want %q", s.Path, filepath.Join(dir, "pincher.db"))
	}
}

func TestOpen_Idempotent(t *testing.T) {
	dir := t.TempDir()
	s1, err := Open(dir)
	if err != nil {
		t.Fatalf("Open 1: %v", err)
	}
	s1.Close()

	// Second open should succeed (migrate is idempotent)
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("Open 2: %v", err)
	}
	s2.Close()
}

// ─────────────────────────────────────────────────────────────────────────────
// Project CRUD
// ─────────────────────────────────────────────────────────────────────────────

func TestUpsertProject(t *testing.T) {
	s := newTestStore(t)
	p := testProject("proj1")
	if err := s.UpsertProject(p); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	got, err := s.GetProject("proj1")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got == nil {
		t.Fatal("GetProject returned nil")
	}
	if got.Name != "proj1" {
		t.Errorf("Name = %q, want proj1", got.Name)
	}
}

func TestUpsertProject_Update(t *testing.T) {
	s := newTestStore(t)
	p := testProject("proj1")
	s.UpsertProject(p)

	p.FileCount = 42
	p.SymCount = 100
	if err := s.UpsertProject(p); err != nil {
		t.Fatalf("UpsertProject update: %v", err)
	}

	got, _ := s.GetProject("proj1")
	if got.FileCount != 42 {
		t.Errorf("FileCount = %d, want 42", got.FileCount)
	}
}

func TestGetProject_NotFound(t *testing.T) {
	s := newTestStore(t)
	got, err := s.GetProject("nonexistent")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got != nil {
		t.Error("expected nil for nonexistent project")
	}
}

func TestListProjects(t *testing.T) {
	s := newTestStore(t)
	s.UpsertProject(testProject("a"))
	s.UpsertProject(testProject("b"))
	s.UpsertProject(testProject("c"))

	projects, err := s.ListProjects()
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 3 {
		t.Errorf("expected 3 projects, got %d", len(projects))
	}
}

func TestDeleteProject(t *testing.T) {
	s := newTestStore(t)
	s.UpsertProject(testProject("p1"))
	sym := testSymbol("s1", "Foo", "Function", "p1", "foo.go")
	s.BulkUpsertSymbols([]Symbol{sym})

	if err := s.DeleteProject("p1"); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	got, _ := s.GetProject("p1")
	if got != nil {
		t.Error("project should be deleted")
	}
	// Symbols should also be deleted
	fetched, _ := s.GetSymbol("s1")
	if fetched != nil {
		t.Error("symbols should be deleted with project")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Symbol CRUD
// ─────────────────────────────────────────────────────────────────────────────

func TestBulkUpsertSymbols(t *testing.T) {
	s := newTestStore(t)
	s.UpsertProject(testProject("proj1"))

	syms := []Symbol{
		testSymbol("s1", "Foo", "Function", "proj1", "foo.go"),
		testSymbol("s2", "Bar", "Function", "proj1", "foo.go"),
		testSymbol("s3", "Baz", "Class", "proj1", "baz.go"),
	}
	if err := s.BulkUpsertSymbols(syms); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}

	got, err := s.GetSymbol("s1")
	if err != nil {
		t.Fatalf("GetSymbol: %v", err)
	}
	if got == nil {
		t.Fatal("symbol not found after upsert")
	}
	if got.Name != "Foo" {
		t.Errorf("Name = %q, want Foo", got.Name)
	}
}

func TestGetSymbol_NotFound(t *testing.T) {
	s := newTestStore(t)
	got, err := s.GetSymbol("nonexistent")
	if err != nil {
		t.Fatalf("GetSymbol: %v", err)
	}
	if got != nil {
		t.Error("expected nil for nonexistent symbol")
	}
}

func TestGetSymbolsByName(t *testing.T) {
	s := newTestStore(t)
	s.UpsertProject(testProject("p1"))
	s.BulkUpsertSymbols([]Symbol{
		testSymbol("s1", "Process", "Function", "p1", "a.go"),
		testSymbol("s2", "Process", "Method", "p1", "b.go"),
		testSymbol("s3", "Other", "Function", "p1", "c.go"),
	})

	results, err := s.GetSymbolsByName("p1", "Process", 10)
	if err != nil {
		t.Fatalf("GetSymbolsByName: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

func TestGetSymbolsForFile(t *testing.T) {
	s := newTestStore(t)
	s.UpsertProject(testProject("p1"))
	s.BulkUpsertSymbols([]Symbol{
		testSymbol("s1", "A", "Function", "p1", "target.go"),
		testSymbol("s2", "B", "Function", "p1", "target.go"),
		testSymbol("s3", "C", "Function", "p1", "other.go"),
	})

	results, err := s.GetSymbolsForFile("p1", "target.go")
	if err != nil {
		t.Fatalf("GetSymbolsForFile: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 symbols in target.go, got %d", len(results))
	}
}

func TestDeleteSymbolsForFile(t *testing.T) {
	s := newTestStore(t)
	s.UpsertProject(testProject("p1"))
	s.BulkUpsertSymbols([]Symbol{
		testSymbol("s1", "A", "Function", "p1", "target.go"),
		testSymbol("s2", "B", "Function", "p1", "other.go"),
	})
	s.BulkUpsertEdges([]Edge{{ProjectID: "p1", FromID: "s1", ToID: "s2", Kind: "CALLS", Confidence: 1.0}})

	if err := s.DeleteSymbolsForFile("p1", "target.go"); err != nil {
		t.Fatalf("DeleteSymbolsForFile: %v", err)
	}

	got, _ := s.GetSymbol("s1")
	if got != nil {
		t.Error("s1 should be deleted")
	}
	got, _ = s.GetSymbol("s2")
	if got == nil {
		t.Error("s2 in other.go should survive")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// FTS5 search
// ─────────────────────────────────────────────────────────────────────────────

func TestSearchSymbols(t *testing.T) {
	s := newTestStore(t)
	s.UpsertProject(testProject("p1"))

	syms := []Symbol{
		{ID: "s1", ProjectID: "p1", FilePath: "auth.go", Name: "AuthService",
			QualifiedName: "auth.AuthService", Kind: "Class", Language: "Go",
			StartByte: 0, EndByte: 100, StartLine: 1, EndLine: 20},
		{ID: "s2", ProjectID: "p1", FilePath: "user.go", Name: "UserService",
			QualifiedName: "user.UserService", Kind: "Class", Language: "Go",
			StartByte: 0, EndByte: 100, StartLine: 1, EndLine: 20},
		{ID: "s3", ProjectID: "p1", FilePath: "auth.go", Name: "Login",
			QualifiedName: "auth.Login", Kind: "Function", Language: "Go",
			StartByte: 200, EndByte: 300, StartLine: 30, EndLine: 45},
	}
	s.BulkUpsertSymbols(syms)

	results, err := s.SearchSymbols("p1", "auth*", "", "", 10)
	if err != nil {
		t.Fatalf("SearchSymbols: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected results for 'auth*'")
	}
}

func TestSearchSymbols_KindFilter(t *testing.T) {
	s := newTestStore(t)
	s.UpsertProject(testProject("p1"))
	s.BulkUpsertSymbols([]Symbol{
		{ID: "f1", ProjectID: "p1", FilePath: "a.go", Name: "processOrder",
			QualifiedName: "pkg.processOrder", Kind: "Function", Language: "Go",
			StartByte: 0, EndByte: 50, StartLine: 1, EndLine: 5},
		{ID: "c1", ProjectID: "p1", FilePath: "a.go", Name: "processOrder",
			QualifiedName: "pkg.OrderProcessor", Kind: "Class", Language: "Go",
			StartByte: 60, EndByte: 200, StartLine: 10, EndLine: 30},
	})

	results, err := s.SearchSymbols("p1", "process*", "Function", "", 10)
	if err != nil {
		t.Fatalf("SearchSymbols: %v", err)
	}
	for _, r := range results {
		if r.Symbol.Kind != "Function" {
			t.Errorf("kind filter failed: got %q", r.Symbol.Kind)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Edge operations
// ─────────────────────────────────────────────────────────────────────────────

func TestBulkUpsertEdges(t *testing.T) {
	s := newTestStore(t)
	s.UpsertProject(testProject("p1"))
	s.BulkUpsertSymbols([]Symbol{
		testSymbol("a", "A", "Function", "p1", "a.go"),
		testSymbol("b", "B", "Function", "p1", "b.go"),
	})

	edges := []Edge{
		{ProjectID: "p1", FromID: "a", ToID: "b", Kind: "CALLS", Confidence: 1.0},
	}
	if err := s.BulkUpsertEdges(edges); err != nil {
		t.Fatalf("BulkUpsertEdges: %v", err)
	}

	from, err := s.EdgesFrom("a", nil)
	if err != nil {
		t.Fatalf("EdgesFrom: %v", err)
	}
	if len(from) != 1 {
		t.Errorf("expected 1 edge from a, got %d", len(from))
	}

	to, err := s.EdgesTo("b", nil)
	if err != nil {
		t.Fatalf("EdgesTo: %v", err)
	}
	if len(to) != 1 {
		t.Errorf("expected 1 edge to b, got %d", len(to))
	}
}

func TestEdgesFrom_KindFilter(t *testing.T) {
	s := newTestStore(t)
	s.UpsertProject(testProject("p1"))
	s.BulkUpsertSymbols([]Symbol{
		testSymbol("a", "A", "Function", "p1", "a.go"),
		testSymbol("b", "B", "Function", "p1", "b.go"),
		testSymbol("c", "C", "Class", "p1", "c.go"),
	})
	s.BulkUpsertEdges([]Edge{
		{ProjectID: "p1", FromID: "a", ToID: "b", Kind: "CALLS", Confidence: 1.0},
		{ProjectID: "p1", FromID: "a", ToID: "c", Kind: "IMPORTS", Confidence: 1.0},
	})

	calls, _ := s.EdgesFrom("a", []string{"CALLS"})
	if len(calls) != 1 {
		t.Errorf("expected 1 CALLS edge, got %d", len(calls))
	}

	all, _ := s.EdgesFrom("a", nil)
	if len(all) != 2 {
		t.Errorf("expected 2 total edges, got %d", len(all))
	}
}

func TestBulkUpsertEdges_Idempotent(t *testing.T) {
	s := newTestStore(t)
	s.UpsertProject(testProject("p1"))
	s.BulkUpsertSymbols([]Symbol{
		testSymbol("a", "A", "Function", "p1", "a.go"),
		testSymbol("b", "B", "Function", "p1", "b.go"),
	})

	edge := []Edge{{ProjectID: "p1", FromID: "a", ToID: "b", Kind: "CALLS", Confidence: 1.0}}
	s.BulkUpsertEdges(edge)
	s.BulkUpsertEdges(edge) // second insert should be ignored

	from, _ := s.EdgesFrom("a", nil)
	if len(from) != 1 {
		t.Errorf("duplicate insert should be ignored, got %d edges", len(from))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// File hash operations
// ─────────────────────────────────────────────────────────────────────────────

func TestFileHash(t *testing.T) {
	s := newTestStore(t)
	s.UpsertProject(testProject("p1"))

	// Initially no hash
	h := s.GetFileHash("p1", "file.go")
	if h != "" {
		t.Errorf("expected empty hash, got %q", h)
	}

	if err := s.SetFileHash("p1", "file.go", "abc123"); err != nil {
		t.Fatalf("SetFileHash: %v", err)
	}

	h = s.GetFileHash("p1", "file.go")
	if h != "abc123" {
		t.Errorf("GetFileHash = %q, want abc123", h)
	}
}

func TestDeleteFileHash(t *testing.T) {
	s := newTestStore(t)
	s.UpsertProject(testProject("p1"))
	s.SetFileHash("p1", "file.go", "abc123")

	if err := s.DeleteFileHash("p1", "file.go"); err != nil {
		t.Fatalf("DeleteFileHash: %v", err)
	}

	h := s.GetFileHash("p1", "file.go")
	if h != "" {
		t.Errorf("expected empty hash after delete, got %q", h)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ADR operations
// ─────────────────────────────────────────────────────────────────────────────

func TestADR_SetGet(t *testing.T) {
	s := newTestStore(t)
	s.UpsertProject(testProject("p1"))

	if err := s.SetADR("p1", "STACK", "Go + SQLite"); err != nil {
		t.Fatalf("SetADR: %v", err)
	}

	val, ok, err := s.GetADR("p1", "STACK")
	if err != nil {
		t.Fatalf("GetADR: %v", err)
	}
	if !ok {
		t.Error("expected ADR to exist")
	}
	if val != "Go + SQLite" {
		t.Errorf("value = %q, want 'Go + SQLite'", val)
	}
}

func TestADR_NotFound(t *testing.T) {
	s := newTestStore(t)
	s.UpsertProject(testProject("p1"))

	_, ok, err := s.GetADR("p1", "NONEXISTENT")
	if err != nil {
		t.Fatalf("GetADR: %v", err)
	}
	if ok {
		t.Error("expected ADR not to exist")
	}
}

func TestADR_List(t *testing.T) {
	s := newTestStore(t)
	s.UpsertProject(testProject("p1"))
	s.SetADR("p1", "A", "val-a")
	s.SetADR("p1", "B", "val-b")

	entries, err := s.ListADRs("p1")
	if err != nil {
		t.Fatalf("ListADRs: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 ADR entries, got %d", len(entries))
	}
	if entries["A"] != "val-a" || entries["B"] != "val-b" {
		t.Errorf("unexpected entries: %v", entries)
	}
}

func TestADR_Delete(t *testing.T) {
	s := newTestStore(t)
	s.UpsertProject(testProject("p1"))
	s.SetADR("p1", "KEY", "value")

	if err := s.DeleteADR("p1", "KEY"); err != nil {
		t.Fatalf("DeleteADR: %v", err)
	}

	_, ok, _ := s.GetADR("p1", "KEY")
	if ok {
		t.Error("ADR should be deleted")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Graph stats
// ─────────────────────────────────────────────────────────────────────────────

func TestGraphStats(t *testing.T) {
	s := newTestStore(t)
	s.UpsertProject(testProject("p1"))
	s.BulkUpsertSymbols([]Symbol{
		testSymbol("f1", "Foo", "Function", "p1", "a.go"),
		testSymbol("f2", "Bar", "Function", "p1", "a.go"),
		testSymbol("c1", "MyClass", "Class", "p1", "b.go"),
	})
	s.BulkUpsertEdges([]Edge{
		{ProjectID: "p1", FromID: "f1", ToID: "f2", Kind: "CALLS", Confidence: 1.0},
	})

	symCount, edgeCount, kindCounts, edgeKindCounts, err := s.GraphStats("p1")
	if err != nil {
		t.Fatalf("GraphStats: %v", err)
	}
	if symCount != 3 {
		t.Errorf("symCount = %d, want 3", symCount)
	}
	if edgeCount != 1 {
		t.Errorf("edgeCount = %d, want 1", edgeCount)
	}
	if kindCounts["Function"] != 2 {
		t.Errorf("Function count = %d, want 2", kindCounts["Function"])
	}
	if edgeKindCounts["CALLS"] != 1 {
		t.Errorf("CALLS edge count = %d, want 1", edgeKindCounts["CALLS"])
	}
}

func TestGetHotspots(t *testing.T) {
	s := newTestStore(t)
	s.UpsertProject(testProject("p1"))
	s.BulkUpsertSymbols([]Symbol{
		testSymbol("a", "A", "Function", "p1", "a.go"),
		testSymbol("b", "B", "Function", "p1", "b.go"),
		testSymbol("c", "C", "Function", "p1", "c.go"),
	})
	// B is called by A and C → hotspot
	s.BulkUpsertEdges([]Edge{
		{ProjectID: "p1", FromID: "a", ToID: "b", Kind: "CALLS", Confidence: 1.0},
		{ProjectID: "p1", FromID: "c", ToID: "b", Kind: "CALLS", Confidence: 1.0},
	})

	hotspots, err := s.GetHotspots("p1", 5)
	if err != nil {
		t.Fatalf("GetHotspots: %v", err)
	}
	if len(hotspots) == 0 {
		t.Error("expected at least 1 hotspot")
	}
	if hotspots[0].Name != "B" {
		t.Errorf("top hotspot = %q, want B", hotspots[0].Name)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Utility functions
// ─────────────────────────────────────────────────────────────────────────────

func TestMakeSymbolID(t *testing.T) {
	id := MakeSymbolID("internal/db/db.go", "db.Open", "Function")
	want := "internal/db/db.go::db.Open#Function"
	if id != want {
		t.Errorf("MakeSymbolID = %q, want %q", id, want)
	}
}

func TestProjectNameFromPath(t *testing.T) {
	cases := []struct {
		path, want string
	}{
		{"/home/user/myproject", "myproject"},
		{"/home/user/myproject/", "myproject"},
		{"C:\\Users\\foo\\bar", "bar"},
	}
	for _, c := range cases {
		got := ProjectNameFromPath(c.path)
		if got != c.want {
			t.Errorf("ProjectNameFromPath(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestApproxTokens(t *testing.T) {
	cases := []struct {
		s    string
		want int
	}{
		{"", 0},
		{"abcd", 1},     // 4 chars = 1 token
		{"abcde", 2},    // 5 chars = 2 tokens
		{"abcdefgh", 2}, // 8 chars = 2 tokens
	}
	for _, c := range cases {
		got := ApproxTokens(c.s)
		if got != c.want {
			t.Errorf("ApproxTokens(%q) = %d, want %d", c.s, got, c.want)
		}
	}
}

func TestFormatSize(t *testing.T) {
	cases := []struct {
		bytes int
		want  string
	}{
		{500, "500 B"},
		{1500, "1.5 KB"},
		{2000000, "1.9 MB"},
	}
	for _, c := range cases {
		got := FormatSize(c.bytes)
		if got != c.want {
			t.Errorf("FormatSize(%d) = %q, want %q", c.bytes, got, c.want)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// DB accessor
// ─────────────────────────────────────────────────────────────────────────────

func TestDB_Accessor(t *testing.T) {
	store := newTestStore(t)
	if store.DB() == nil {
		t.Error("DB() should return non-nil *sql.DB")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// GetSymbolsByQN
// ─────────────────────────────────────────────────────────────────────────────

func TestGetSymbolsByQN(t *testing.T) {
	store := newTestStore(t)
	pid := "proj-qn"
	store.UpsertProject(Project{ID: pid, Path: "/tmp/qn", Name: "qn"})
	store.BulkUpsertSymbols([]Symbol{
		{ID: "qn1", ProjectID: pid, FilePath: "a.go", Name: "Foo",
			QualifiedName: "pkg.Foo", Kind: "Function", Language: "Go",
			StartByte: 0, EndByte: 50, StartLine: 1, EndLine: 5},
	})
	results, err := store.GetSymbolsByQN(pid, "pkg.Foo")
	if err != nil {
		t.Fatalf("GetSymbolsByQN: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected to find symbol by qualified name")
	}
}

func TestGetSymbolsByQN_NotFound(t *testing.T) {
	store := newTestStore(t)
	pid := "proj-qn2"
	store.UpsertProject(Project{ID: pid, Path: "/tmp/qn2", Name: "qn2"})
	results, err := store.GetSymbolsByQN(pid, "pkg.NonExistent")
	if err != nil {
		t.Fatalf("GetSymbolsByQN: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ProjectIDFromPath
// ─────────────────────────────────────────────────────────────────────────────

func TestProjectIDFromPath(t *testing.T) {
	id := ProjectIDFromPath("/home/user/myproject")
	if id == "" {
		t.Error("ProjectIDFromPath returned empty string")
	}
	id2 := ProjectIDFromPath("/home/user/myproject")
	if id != id2 {
		t.Error("ProjectIDFromPath should be deterministic")
	}
	id3 := ProjectIDFromPath("/home/user/otherproject")
	if id == id3 {
		t.Error("different paths should give different IDs")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// DataDir
// ─────────────────────────────────────────────────────────────────────────────

func TestDataDir_ReturnsPath(t *testing.T) {
	dir, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir: %v", err)
	}
	if dir == "" {
		t.Error("DataDir returned empty string")
	}
	if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
		t.Errorf("DataDir %q does not exist after call", dir)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// BulkUpsertEdges
// ─────────────────────────────────────────────────────────────────────────────

func TestBulkUpsertEdges_WithProperties(t *testing.T) {
	store := newTestStore(t)
	pid := "proj-edge-props"
	store.UpsertProject(testProject(pid))
	store.BulkUpsertSymbols([]Symbol{
		{ID: "ep1", ProjectID: pid, FilePath: "a.go", Name: "A", QualifiedName: "pkg.A", Kind: "Function", Language: "Go", StartByte: 0, EndByte: 10, StartLine: 1, EndLine: 2},
		{ID: "ep2", ProjectID: pid, FilePath: "b.go", Name: "B", QualifiedName: "pkg.B", Kind: "Function", Language: "Go", StartByte: 0, EndByte: 10, StartLine: 1, EndLine: 2},
	})
	edges := []Edge{
		{ProjectID: pid, FromID: "ep1", ToID: "ep2", Kind: "CALLS", Confidence: 0.9,
			Properties: map[string]any{"line": 5}},
	}
	if err := store.BulkUpsertEdges(edges); err != nil {
		t.Fatalf("BulkUpsertEdges with properties: %v", err)
	}
	got, err := store.EdgesFrom("ep1", []string{"CALLS"})
	if err != nil {
		t.Fatalf("EdgesFrom: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(got))
	}
}

func TestBulkUpsertEdges_Empty(t *testing.T) {
	store := newTestStore(t)
	if err := store.BulkUpsertEdges(nil); err != nil {
		t.Fatalf("BulkUpsertEdges(nil): %v", err)
	}
	if err := store.BulkUpsertEdges([]Edge{}); err != nil {
		t.Fatalf("BulkUpsertEdges([]): %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// DeleteProject
// ─────────────────────────────────────────────────────────────────────────────

func TestDeleteProject_RemovesAll(t *testing.T) {
	store := newTestStore(t)
	pid := "proj-del"
	store.UpsertProject(testProject(pid))
	store.BulkUpsertSymbols([]Symbol{
		{ID: "dp1", ProjectID: pid, FilePath: "a.go", Name: "A", QualifiedName: "pkg.A", Kind: "Function", Language: "Go", StartByte: 0, EndByte: 10, StartLine: 1, EndLine: 2},
		{ID: "dp2", ProjectID: pid, FilePath: "b.go", Name: "B", QualifiedName: "pkg.B", Kind: "Function", Language: "Go", StartByte: 0, EndByte: 10, StartLine: 1, EndLine: 2},
	})
	store.BulkUpsertEdges([]Edge{
		{ProjectID: pid, FromID: "dp1", ToID: "dp2", Kind: "CALLS", Confidence: 1.0},
	})
	if err := store.DeleteProject(pid); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	p, err := store.GetProject(pid)
	if err != nil {
		t.Fatalf("GetProject after delete: %v", err)
	}
	if p != nil {
		t.Error("project should be nil after deletion")
	}
	syms, _ := store.GetSymbolsForFile(pid, "a.go")
	if len(syms) != 0 {
		t.Errorf("expected 0 symbols after deletion, got %d", len(syms))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// DeleteSymbolsForFile
// ─────────────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
// GraphStats
// ─────────────────────────────────────────────────────────────────────────────

func TestGraphStats_WithData(t *testing.T) {
	store := newTestStore(t)
	pid := "proj-gs"
	store.UpsertProject(testProject(pid))
	store.BulkUpsertSymbols([]Symbol{
		{ID: "gs1", ProjectID: pid, FilePath: "a.go", Name: "Fn1", QualifiedName: "p.Fn1", Kind: "Function", Language: "Go", StartByte: 0, EndByte: 10, StartLine: 1, EndLine: 2},
		{ID: "gs2", ProjectID: pid, FilePath: "a.go", Name: "T1", QualifiedName: "p.T1", Kind: "Class", Language: "Go", StartByte: 20, EndByte: 50, StartLine: 5, EndLine: 10},
		{ID: "gs3", ProjectID: pid, FilePath: "b.go", Name: "Fn2", QualifiedName: "p.Fn2", Kind: "Function", Language: "Go", StartByte: 0, EndByte: 10, StartLine: 1, EndLine: 2},
	})
	store.BulkUpsertEdges([]Edge{
		{ProjectID: pid, FromID: "gs1", ToID: "gs3", Kind: "CALLS", Confidence: 1.0},
		{ProjectID: pid, FromID: "gs2", ToID: "gs1", Kind: "CALLS", Confidence: 1.0},
	})
	symCount, edgeCount, kindCounts, edgeKindCounts, err := store.GraphStats(pid)
	if err != nil {
		t.Fatalf("GraphStats: %v", err)
	}
	if symCount != 3 {
		t.Errorf("expected 3 symbols, got %d", symCount)
	}
	if edgeCount != 2 {
		t.Errorf("expected 2 edges, got %d", edgeCount)
	}
	if kindCounts["Function"] != 2 {
		t.Errorf("expected 2 Function kinds, got %d", kindCounts["Function"])
	}
	if kindCounts["Class"] != 1 {
		t.Errorf("expected 1 Class kind, got %d", kindCounts["Class"])
	}
	if edgeKindCounts["CALLS"] != 2 {
		t.Errorf("expected 2 CALLS edges, got %d", edgeKindCounts["CALLS"])
	}
}

func TestGraphStats_EmptyProject(t *testing.T) {
	store := newTestStore(t)
	pid := "proj-gs-empty"
	store.UpsertProject(testProject(pid))
	symCount, edgeCount, kindCounts, edgeKindCounts, err := store.GraphStats(pid)
	if err != nil {
		t.Fatalf("GraphStats: %v", err)
	}
	if symCount != 0 || edgeCount != 0 {
		t.Errorf("expected 0 counts, got sym=%d edge=%d", symCount, edgeCount)
	}
	if len(kindCounts) != 0 || len(edgeKindCounts) != 0 {
		t.Error("expected empty kind maps for empty project")
	}
}

