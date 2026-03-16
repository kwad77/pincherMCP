package index

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pincherMCP/pincher/internal/db"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func newTestIndexer(t *testing.T) (*Indexer, *db.Store) {
	t.Helper()
	dir := t.TempDir()
	store, err := db.Open(dir)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return New(store), store
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// ─────────────────────────────────────────────────────────────────────────────
// ReadSymbolSource
// ─────────────────────────────────────────────────────────────────────────────

func TestReadSymbolSource(t *testing.T) {
	dir := t.TempDir()
	content := "package main\n\nfunc Hello() string {\n\treturn \"hello\"\n}\n"
	writeFile(t, dir, "main.go", content)

	// Byte offsets for "func Hello..." — byte 14 to end
	startByte := 14
	endByte := len(content)

	sym := db.Symbol{
		FilePath:  "main.go",
		StartByte: startByte,
		EndByte:   endByte,
	}

	got, err := ReadSymbolSource(dir, sym)
	if err != nil {
		t.Fatalf("ReadSymbolSource: %v", err)
	}
	if got == "" {
		t.Error("expected non-empty source")
	}
	if got != content[startByte:endByte] {
		t.Errorf("source mismatch:\ngot:  %q\nwant: %q", got, content[startByte:endByte])
	}
}

func TestReadSymbolSource_ZeroBytes(t *testing.T) {
	sym := db.Symbol{FilePath: "x.go", StartByte: 5, EndByte: 5}
	got, err := ReadSymbolSource("/tmp", sym)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty for zero-length symbol, got %q", got)
	}
}

func TestReadSymbolSource_FileNotFound(t *testing.T) {
	sym := db.Symbol{FilePath: "nonexistent.go", StartByte: 0, EndByte: 10}
	_, err := ReadSymbolSource("/tmp/does_not_exist_12345", sym)
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Index
// ─────────────────────────────────────────────────────────────────────────────

const goSrc = `package mypackage

// Add adds two integers.
func Add(a, b int) int {
	return a + b
}

type Server struct {
	Port int
}

func (s *Server) Start() {}
`

func TestIndex_GoFile(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "mypackage/myfile.go", goSrc)

	result, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}

	if result.Files == 0 {
		t.Error("expected at least 1 file indexed")
	}
	if result.Symbols == 0 {
		t.Error("expected at least 1 symbol")
	}
	_ = store
}

func TestIndex_Incremental(t *testing.T) {
	idx, _ := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "main.go", goSrc)

	// First index
	r1, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("first index: %v", err)
	}

	// Second index — file unchanged, should skip
	r2, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("second index: %v", err)
	}

	if r2.Skipped == 0 {
		t.Errorf("expected files skipped on second run, got %d skipped (first indexed %d)", r2.Skipped, r1.Files)
	}
}

func TestIndex_Force(t *testing.T) {
	idx, _ := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "main.go", goSrc)

	// First index
	idx.Index(context.Background(), dir, false)

	// Second index with force — should re-parse
	r2, err := idx.Index(context.Background(), dir, true)
	if err != nil {
		t.Fatalf("force index: %v", err)
	}
	if r2.Skipped != 0 {
		t.Errorf("force index should skip 0 files, got %d skipped", r2.Skipped)
	}
}

func TestIndex_SymbolsIndexed(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "pkg/service.go", goSrc)

	_, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}

	// Find the Add function
	projectID := db.ProjectIDFromPath(dir)
	results, err := store.GetSymbolsByName(projectID, "Add", 5)
	if err != nil {
		t.Fatalf("GetSymbolsByName: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected 'Add' function to be indexed")
	}
}

func TestIndex_MultipleFiles(t *testing.T) {
	idx, _ := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "a/a.go", "package a\nfunc A() {}\n")
	writeFile(t, dir, "b/b.go", "package b\nfunc B() {}\n")
	writeFile(t, dir, "c/c.go", "package c\nfunc C() {}\n")

	result, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if result.Files < 3 {
		t.Errorf("expected at least 3 files indexed, got %d", result.Files)
	}
}

func TestIndex_NoDotGit(t *testing.T) {
	idx, _ := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\nfunc main() {}\n")
	// Create a .git dir with a Go file — should be skipped
	writeFile(t, dir, ".git/hook.go", "package hook\nfunc Hook() {}\n")

	result, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if result.Files != 1 {
		t.Errorf("expected exactly 1 file (main.go), got %d (hook.go in .git should be excluded)", result.Files)
	}
}

func TestIndex_AlreadyIndexing(t *testing.T) {
	idx, _ := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "main.go", goSrc)

	// Simulate concurrent index by setting active flag
	projectID := db.ProjectIDFromPath(dir)
	idx.mu.Lock()
	idx.active[projectID] = true
	idx.mu.Unlock()

	_, err := idx.Index(context.Background(), dir, false)
	if err == nil {
		t.Error("expected error when project is already being indexed")
	}

	// Clean up
	idx.mu.Lock()
	delete(idx.active, projectID)
	idx.mu.Unlock()
}

// ─────────────────────────────────────────────────────────────────────────────
// Trace
// ─────────────────────────────────────────────────────────────────────────────

func TestTrace_Outbound(t *testing.T) {
	_, store := newTestIndexer(t)
	idx := New(store)
	projectID := "test-proj"

	store.UpsertProject(db.Project{ID: projectID, Path: "/tmp/test", Name: "test"})
	store.BulkUpsertSymbols([]db.Symbol{
		{ID: "main_fn", ProjectID: projectID, FilePath: "main.go", Name: "main", QualifiedName: "main.main", Kind: "Function", Language: "Go", StartByte: 0, EndByte: 50, StartLine: 1, EndLine: 5},
		{ID: "run_fn", ProjectID: projectID, FilePath: "main.go", Name: "run", QualifiedName: "main.run", Kind: "Function", Language: "Go", StartByte: 60, EndByte: 110, StartLine: 10, EndLine: 15},
	})
	store.BulkUpsertEdges([]db.Edge{
		{ProjectID: projectID, FromID: "main_fn", ToID: "run_fn", Kind: "CALLS", Confidence: 1.0},
	})

	hops, err := idx.Trace(context.Background(), projectID, "main", "outbound", 3, true)
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}
	if len(hops) == 0 {
		t.Error("expected at least 1 hop")
	}
	if hops[0].Symbol.Name != "run" {
		t.Errorf("first hop = %q, want run", hops[0].Symbol.Name)
	}
	if hops[0].Risk != "CRITICAL" {
		t.Errorf("depth-1 hop risk = %q, want CRITICAL", hops[0].Risk)
	}
}

func TestTrace_Inbound(t *testing.T) {
	_, store := newTestIndexer(t)
	idx := New(store)
	projectID := "test-proj2"

	store.UpsertProject(db.Project{ID: projectID, Path: "/tmp/test2", Name: "test2"})
	store.BulkUpsertSymbols([]db.Symbol{
		{ID: "caller_fn", ProjectID: projectID, FilePath: "a.go", Name: "caller", QualifiedName: "pkg.caller", Kind: "Function", Language: "Go", StartByte: 0, EndByte: 50, StartLine: 1, EndLine: 5},
		{ID: "target_fn", ProjectID: projectID, FilePath: "b.go", Name: "target", QualifiedName: "pkg.target", Kind: "Function", Language: "Go", StartByte: 0, EndByte: 50, StartLine: 1, EndLine: 5},
	})
	store.BulkUpsertEdges([]db.Edge{
		{ProjectID: projectID, FromID: "caller_fn", ToID: "target_fn", Kind: "CALLS", Confidence: 1.0},
	})

	hops, err := idx.Trace(context.Background(), projectID, "target", "inbound", 3, true)
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}
	if len(hops) == 0 {
		t.Error("expected at least 1 inbound hop")
	}
	if hops[0].Symbol.Name != "caller" {
		t.Errorf("inbound hop = %q, want caller", hops[0].Symbol.Name)
	}
}

func TestTrace_SymbolNotFound(t *testing.T) {
	_, store := newTestIndexer(t)
	idx := New(store)
	projectID := "test-proj3"
	store.UpsertProject(db.Project{ID: projectID, Path: "/tmp/test3", Name: "test3"})

	_, err := idx.Trace(context.Background(), projectID, "nonexistent", "both", 3, false)
	if err == nil {
		t.Error("expected error for nonexistent symbol")
	}
}

func TestTrace_DepthLimit(t *testing.T) {
	_, store := newTestIndexer(t)
	idx := New(store)
	projectID := "depth-proj"

	store.UpsertProject(db.Project{ID: projectID, Path: "/tmp/depth", Name: "depth"})
	// Build a chain: a -> b -> c -> d -> e (depth 4)
	syms := []db.Symbol{}
	for i, name := range []string{"a", "b", "c", "d", "e"} {
		syms = append(syms, db.Symbol{
			ID: name + "_id", ProjectID: projectID, FilePath: "f.go",
			Name: name, QualifiedName: name, Kind: "Function", Language: "Go",
			StartByte: i * 100, EndByte: i*100 + 50, StartLine: i + 1, EndLine: i + 5,
		})
	}
	store.BulkUpsertSymbols(syms)
	store.BulkUpsertEdges([]db.Edge{
		{ProjectID: projectID, FromID: "a_id", ToID: "b_id", Kind: "CALLS", Confidence: 1.0},
		{ProjectID: projectID, FromID: "b_id", ToID: "c_id", Kind: "CALLS", Confidence: 1.0},
		{ProjectID: projectID, FromID: "c_id", ToID: "d_id", Kind: "CALLS", Confidence: 1.0},
		{ProjectID: projectID, FromID: "d_id", ToID: "e_id", Kind: "CALLS", Confidence: 1.0},
	})

	hops2, _ := idx.Trace(context.Background(), projectID, "a", "outbound", 2, false)
	hops3, _ := idx.Trace(context.Background(), projectID, "a", "outbound", 3, false)

	if len(hops2) != 2 {
		t.Errorf("depth=2 should yield 2 hops, got %d", len(hops2))
	}
	if len(hops3) != 3 {
		t.Errorf("depth=3 should yield 3 hops, got %d", len(hops3))
	}
}

func TestRiskLabel(t *testing.T) {
	cases := []struct {
		depth int
		want  string
	}{
		{1, "CRITICAL"},
		{2, "HIGH"},
		{3, "MEDIUM"},
		{4, "LOW"},
		{10, "LOW"},
	}
	for _, c := range cases {
		got := riskLabel(c.depth)
		if got != c.want {
			t.Errorf("riskLabel(%d) = %q, want %q", c.depth, got, c.want)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
// hasChanges
// ─────────────────────────────────────────────────────────────────────────────

func TestHasChanges_NewerFile(t *testing.T) {
	_, store := newTestIndexer(t)
	idx := New(store)
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\nfunc main() {}\n")

	// IndexedAt is well in the past — file is newer
	p := db.Project{
		ID:        "proj",
		Path:      dir,
		Name:      "proj",
		IndexedAt: time.Now().Add(-24 * time.Hour),
	}
	if !idx.hasChanges(p) {
		t.Error("hasChanges should return true when source file is newer than IndexedAt")
	}
}

func TestHasChanges_OlderFile(t *testing.T) {
	_, store := newTestIndexer(t)
	idx := New(store)
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\nfunc main() {}\n")

	// IndexedAt is in the future — no files are newer
	p := db.Project{
		ID:        "proj",
		Path:      dir,
		Name:      "proj",
		IndexedAt: time.Now().Add(24 * time.Hour),
	}
	if idx.hasChanges(p) {
		t.Error("hasChanges should return false when all source files are older than IndexedAt")
	}
}

func TestHasChanges_NoSourceFiles(t *testing.T) {
	_, store := newTestIndexer(t)
	idx := New(store)
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# readme\n")
	writeFile(t, dir, "data.json", "{}\n")

	p := db.Project{
		ID:        "proj",
		Path:      dir,
		Name:      "proj",
		IndexedAt: time.Now().Add(-24 * time.Hour),
	}
	if idx.hasChanges(p) {
		t.Error("hasChanges should return false when there are no source files")
	}
}

func TestHasChanges_NonExistentDir(t *testing.T) {
	_, store := newTestIndexer(t)
	idx := New(store)

	p := db.Project{
		ID:        "proj",
		Path:      "/nonexistent/path/that/does/not/exist",
		Name:      "proj",
		IndexedAt: time.Now().Add(-24 * time.Hour),
	}
	if idx.hasChanges(p) {
		t.Error("hasChanges should return false for nonexistent directory")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Watch
// ─────────────────────────────────────────────────────────────────────────────

func TestWatch_CancelImmediately(t *testing.T) {
	_, store := newTestIndexer(t)
	idx := New(store)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	done := make(chan struct{})
	go func() {
		idx.Watch(ctx)
		close(done)
	}()

	select {
	case <-done:
		// expected: Watch exits when context is cancelled
	case <-time.After(3 * time.Second):
		t.Error("Watch did not exit when context was cancelled")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ReadSymbolSource edge cases
// ─────────────────────────────────────────────────────────────────────────────

func TestReadSymbolSource_NegativeSize(t *testing.T) {
	// StartByte > EndByte → size <= 0, should return empty (file must exist to reach that path)
	dir := t.TempDir()
	writeFile(t, dir, "x.go", "package x\nfunc X() {}\n")
	sym := db.Symbol{FilePath: "x.go", StartByte: 100, EndByte: 50}
	got, err := ReadSymbolSource(dir, sym)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty for negative-size symbol, got %q", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Index edge cases
// ─────────────────────────────────────────────────────────────────────────────

func TestIndex_NonSourceFiles(t *testing.T) {
	idx, _ := newTestIndexer(t)
	dir := t.TempDir()
	// Write only non-source files
	writeFile(t, dir, "README.md", "# readme\n")
	writeFile(t, dir, "data.json", `{"key":"value"}`)
	writeFile(t, dir, ".gitignore", "*.tmp\n")

	result, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if result.Files != 0 {
		t.Errorf("expected 0 files indexed for non-source files, got %d", result.Files)
	}
}

func TestIndex_EmptyGoFile(t *testing.T) {
	idx, _ := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "empty.go", "package empty\n")

	result, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	// File is indexed (counted), but no symbols extracted from an empty package decl
	_ = result
}

func TestIndex_LargeGoFile(t *testing.T) {
	idx, _ := newTestIndexer(t)
	dir := t.TempDir()
	// Build a Go file with many symbols to exercise the buffer flush path
	src := "package bigpkg\n\n"
	for i := 0; i < 30; i++ {
		src += fmt.Sprintf("func Fn%d() int { return %d }\n\n", i, i)
	}
	writeFile(t, dir, "big.go", src)

	result, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if result.Symbols < 30 {
		t.Errorf("expected at least 30 symbols, got %d", result.Symbols)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func TestIsSkippedDir(t *testing.T) {
	skipped := []string{".git", "node_modules", "vendor", ".cache", "dist", "build"}
	for _, d := range skipped {
		if !isSkippedDir(d) {
			t.Errorf("isSkippedDir(%q) = false, want true", d)
		}
	}
	if isSkippedDir("src") {
		t.Error("isSkippedDir('src') = true, want false")
	}
	if isSkippedDir("internal") {
		t.Error("isSkippedDir('internal') = true, want false")
	}
	// Dot-prefix dirs should be skipped
	if !isSkippedDir(".hidden") {
		t.Error("isSkippedDir('.hidden') = false, want true")
	}
}
