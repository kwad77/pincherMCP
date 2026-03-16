package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
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

func TestMigrate_UpgradeFromV1(t *testing.T) {
	// Simulate a pre-versioning database that is at schema v1 (baseline only,
	// no extraction_confidence column, no symbol_moves table).
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "pincher.db")

	// Build a v1-era database using a raw connection — apply the baseline schema
	// then pin schema_version to 1 (before any migrations ran).
	raw, err := sql.Open("sqlite", "file:"+dbPath+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	if _, err := raw.Exec(schema); err != nil {
		raw.Close()
		t.Fatalf("baseline schema: %v", err)
	}
	if _, err := raw.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		raw.Close()
		t.Fatalf("create schema_version: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO schema_version(version) VALUES(1)`); err != nil {
		raw.Close()
		t.Fatalf("seed schema_version: %v", err)
	}
	raw.Close()

	// Now open via the normal path — migrate() must detect v1 and apply
	// all pending migrations (v1→v2, v2→v3, …).
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open after v1 seed: %v", err)
	}
	defer s.Close()

	// Verify the final version equals 1 + len(schemaMigrations).
	var version int
	if err := s.db.QueryRow(`SELECT version FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	want := 1 + len(schemaMigrations)
	if version != want {
		t.Errorf("version = %d, want %d", version, want)
	}

	// Spot-check: extraction_confidence column must exist (migration 0).
	if _, err := s.db.Exec(`INSERT INTO symbols(id,project_id,file_path,name,qualified_name,kind,language,start_byte,end_byte,start_line,end_line,extraction_confidence) VALUES('x','p','f.go','X','X','func','Go',0,1,1,1,0.85)`); err != nil {
		t.Errorf("extraction_confidence column missing after migration: %v", err)
	}

	// Spot-check: symbol_moves table must exist (migration 1).
	if _, err := s.db.Exec(`INSERT INTO symbol_moves(old_id,new_id,project_id,moved_at) VALUES('old','new','p',0)`); err != nil {
		t.Errorf("symbol_moves table missing after migration: %v", err)
	}
}

func TestMigrate_VersionTracked(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	var version int
	if err := s.db.QueryRow(`SELECT version FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	// Version must be 1 (baseline) + number of migrations applied.
	want := 1 + len(schemaMigrations)
	if version != want {
		t.Errorf("schema_version = %d, want %d", version, want)
	}
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


// ─────────────────────────────────────────────────────────────────────────────
// TraceViaCTE
// ─────────────────────────────────────────────────────────────────────────────

func TestTraceViaCTE_Outbound(t *testing.T) {
	s := newTestStore(t)
	s.UpsertProject(testProject("p1"))

	caller := testSymbol("s1", "Caller", "Function", "p1", "a.go")
	callee := testSymbol("s2", "Callee", "Function", "p1", "b.go")
	s.BulkUpsertSymbols([]Symbol{caller, callee})
	s.BulkUpsertEdges([]Edge{{ProjectID: "p1", FromID: "s1", ToID: "s2", Kind: "CALLS", Confidence: 1.0}})

	results, err := s.TraceViaCTE("s1", "outbound", []string{"CALLS"}, 3)
	if err != nil {
		t.Fatalf("TraceViaCTE: %v", err)
	}
	if len(results) != 1 || results[0].SymbolID != "s2" {
		t.Errorf("expected [s2], got %v", results)
	}
	if results[0].Depth != 1 {
		t.Errorf("expected depth 1, got %d", results[0].Depth)
	}
	if results[0].ViaKind != "CALLS" {
		t.Errorf("expected via CALLS, got %q", results[0].ViaKind)
	}
}

func TestTraceViaCTE_Inbound(t *testing.T) {
	s := newTestStore(t)
	s.UpsertProject(testProject("p1"))

	caller := testSymbol("s1", "Caller", "Function", "p1", "a.go")
	callee := testSymbol("s2", "Callee", "Function", "p1", "b.go")
	s.BulkUpsertSymbols([]Symbol{caller, callee})
	s.BulkUpsertEdges([]Edge{{ProjectID: "p1", FromID: "s1", ToID: "s2", Kind: "CALLS", Confidence: 1.0}})

	results, err := s.TraceViaCTE("s2", "inbound", []string{"CALLS"}, 3)
	if err != nil {
		t.Fatalf("TraceViaCTE: %v", err)
	}
	if len(results) != 1 || results[0].SymbolID != "s1" {
		t.Errorf("expected [s1], got %v", results)
	}
}

func TestTraceViaCTE_Both(t *testing.T) {
	s := newTestStore(t)
	s.UpsertProject(testProject("p1"))

	root := testSymbol("root", "Root", "Function", "p1", "root.go")
	caller := testSymbol("caller", "Caller", "Function", "p1", "caller.go")
	callee := testSymbol("callee", "Callee", "Function", "p1", "callee.go")
	s.BulkUpsertSymbols([]Symbol{root, caller, callee})
	s.BulkUpsertEdges([]Edge{
		{ProjectID: "p1", FromID: "caller", ToID: "root", Kind: "CALLS", Confidence: 1.0},
		{ProjectID: "p1", FromID: "root", ToID: "callee", Kind: "CALLS", Confidence: 1.0},
	})

	results, err := s.TraceViaCTE("root", "both", []string{"CALLS"}, 3)
	if err != nil {
		t.Fatalf("TraceViaCTE: %v", err)
	}
	ids := map[string]bool{}
	for _, r := range results {
		ids[r.SymbolID] = true
	}
	if !ids["caller"] {
		t.Error("expected caller in both-direction trace")
	}
	if !ids["callee"] {
		t.Error("expected callee in both-direction trace")
	}
}

func TestTraceViaCTE_MultiHop(t *testing.T) {
	s := newTestStore(t)
	s.UpsertProject(testProject("p1"))

	a := testSymbol("a", "A", "Function", "p1", "a.go")
	b := testSymbol("b", "B", "Function", "p1", "b.go")
	c := testSymbol("c", "C", "Function", "p1", "c.go")
	s.BulkUpsertSymbols([]Symbol{a, b, c})
	s.BulkUpsertEdges([]Edge{
		{ProjectID: "p1", FromID: "a", ToID: "b", Kind: "CALLS", Confidence: 1.0},
		{ProjectID: "p1", FromID: "b", ToID: "c", Kind: "CALLS", Confidence: 1.0},
	})

	results, err := s.TraceViaCTE("a", "outbound", []string{"CALLS"}, 3)
	if err != nil {
		t.Fatalf("TraceViaCTE: %v", err)
	}
	ids := map[string]int{}
	for _, r := range results {
		ids[r.SymbolID] = r.Depth
	}
	if ids["b"] != 1 {
		t.Errorf("expected B at depth 1, got %d", ids["b"])
	}
	if ids["c"] != 2 {
		t.Errorf("expected C at depth 2, got %d", ids["c"])
	}
}

func TestTraceViaCTE_DepthLimit(t *testing.T) {
	s := newTestStore(t)
	s.UpsertProject(testProject("p1"))

	a := testSymbol("a", "A", "Function", "p1", "a.go")
	b := testSymbol("b", "B", "Function", "p1", "b.go")
	c := testSymbol("c", "C", "Function", "p1", "c.go")
	s.BulkUpsertSymbols([]Symbol{a, b, c})
	s.BulkUpsertEdges([]Edge{
		{ProjectID: "p1", FromID: "a", ToID: "b", Kind: "CALLS", Confidence: 1.0},
		{ProjectID: "p1", FromID: "b", ToID: "c", Kind: "CALLS", Confidence: 1.0},
	})

	// maxDepth=1 should only find b, not c
	results, err := s.TraceViaCTE("a", "outbound", []string{"CALLS"}, 1)
	if err != nil {
		t.Fatalf("TraceViaCTE: %v", err)
	}
	for _, r := range results {
		if r.SymbolID == "c" {
			t.Error("c should be out of reach at maxDepth=1")
		}
	}
}

func TestTraceViaCTE_NoEdges(t *testing.T) {
	s := newTestStore(t)
	s.UpsertProject(testProject("p1"))

	sym := testSymbol("iso", "Isolated", "Function", "p1", "iso.go")
	s.BulkUpsertSymbols([]Symbol{sym})

	results, err := s.TraceViaCTE("iso", "both", []string{"CALLS"}, 3)
	if err != nil {
		t.Fatalf("TraceViaCTE: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected no results for isolated node, got %d", len(results))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Symbol move tracking
// ─────────────────────────────────────────────────────────────────────────────

func TestRecordSymbolMove_Basic(t *testing.T) {
	s := newTestStore(t)
	s.UpsertProject(testProject("p1"))
	if err := s.RecordSymbolMove("p1", "old-id", "new-id"); err != nil {
		t.Fatalf("RecordSymbolMove: %v", err)
	}
	newID, ok := s.ResolveStaleID("p1", "old-id")
	if !ok {
		t.Fatal("expected stale ID to resolve")
	}
	if newID != "new-id" {
		t.Errorf("expected new-id, got %q", newID)
	}
}

func TestResolveStaleID_NotFound(t *testing.T) {
	s := newTestStore(t)
	s.UpsertProject(testProject("p1"))
	_, ok := s.ResolveStaleID("p1", "nonexistent")
	if ok {
		t.Error("expected false for nonexistent stale ID")
	}
}

func TestRecordSymbolMove_Upsert(t *testing.T) {
	s := newTestStore(t)
	s.UpsertProject(testProject("p1"))
	s.RecordSymbolMove("p1", "old-id", "new-id-1")
	// Second call should update new_id
	if err := s.RecordSymbolMove("p1", "old-id", "new-id-2"); err != nil {
		t.Fatalf("RecordSymbolMove upsert: %v", err)
	}
	newID, _ := s.ResolveStaleID("p1", "old-id")
	if newID != "new-id-2" {
		t.Errorf("expected updated new-id-2, got %q", newID)
	}
}

func TestDetectAndRecordMoves_DetectsMove(t *testing.T) {
	s := newTestStore(t)
	s.UpsertProject(testProject("p1"))

	// Original symbol at old path
	old := testSymbol("old/path.go::MyFn#Function", "MyFn", "Function", "p1", "old/path.go")
	old.QualifiedName = "MyFn"
	s.BulkUpsertSymbols([]Symbol{old})

	// Same qualified name + kind, new path (file moved)
	newSym := testSymbol("new/path.go::MyFn#Function", "MyFn", "Function", "p1", "new/path.go")
	newSym.QualifiedName = "MyFn"

	if err := s.DetectAndRecordMoves("p1", []Symbol{newSym}); err != nil {
		t.Fatalf("DetectAndRecordMoves: %v", err)
	}

	newID, ok := s.ResolveStaleID("p1", old.ID)
	if !ok {
		t.Fatal("expected move to be recorded")
	}
	if newID != newSym.ID {
		t.Errorf("expected %q, got %q", newSym.ID, newID)
	}
}

func TestDetectAndRecordMoves_NoMove(t *testing.T) {
	s := newTestStore(t)
	s.UpsertProject(testProject("p1"))

	// Brand new symbol — no existing symbol with same QN+kind
	newSym := testSymbol("path.go::BrandNew#Function", "BrandNew", "Function", "p1", "path.go")
	newSym.QualifiedName = "BrandNew"

	if err := s.DetectAndRecordMoves("p1", []Symbol{newSym}); err != nil {
		t.Fatalf("DetectAndRecordMoves: %v", err)
	}
	_, ok := s.ResolveStaleID("p1", newSym.ID)
	if ok {
		t.Error("no move should be recorded for brand-new symbol")
	}
}

func TestDetectAndRecordMoves_Empty(t *testing.T) {
	s := newTestStore(t)
	if err := s.DetectAndRecordMoves("p1", nil); err != nil {
		t.Fatalf("DetectAndRecordMoves empty: %v", err)
	}
}
