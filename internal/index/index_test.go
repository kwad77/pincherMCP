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

// ─────────────────────────────────────────────────────────────────────────────
// flushBatch
// ─────────────────────────────────────────────────────────────────────────────

func TestFlushBatch_Empty(t *testing.T) {
	idx, store := newTestIndexer(t)
	pid := "flush-empty"
	store.UpsertProject(db.Project{ID: pid, Path: "/tmp/fe", Name: "fe", IndexedAt: time.Now()})
	if err := idx.flushBatch(pid, nil, nil); err != nil {
		t.Fatalf("flushBatch(nil, nil): %v", err)
	}
}

func TestFlushBatch_WithData(t *testing.T) {
	idx, store := newTestIndexer(t)
	pid := "flush-data"
	store.UpsertProject(db.Project{ID: pid, Path: "/tmp/fd", Name: "fd", IndexedAt: time.Now()})

	syms := []db.Symbol{
		{ID: "fb1", ProjectID: pid, FilePath: "a.go", Name: "FnA", QualifiedName: "pkg.FnA",
			Kind: "Function", Language: "Go", StartByte: 0, EndByte: 30, StartLine: 1, EndLine: 3},
	}
	edges := []db.Edge{
		{ProjectID: pid, FromID: "fb1", ToID: "fb1", Kind: "CALLS", Confidence: 1.0},
	}
	if err := idx.flushBatch(pid, syms, edges); err != nil {
		t.Fatalf("flushBatch: %v", err)
	}

	got, err := store.GetSymbolsForFile(pid, "a.go")
	if err != nil {
		t.Fatalf("GetSymbolsForFile: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 symbol, got %d", len(got))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ReadSymbolSource edge cases
// ─────────────────────────────────────────────────────────────────────────────

func TestReadSymbolSource_EqualStartEnd(t *testing.T) {
	sym := db.Symbol{StartByte: 10, EndByte: 10}
	src, err := ReadSymbolSource("/any/root", sym)
	if err != nil {
		t.Fatalf("ReadSymbolSource with equal bytes: %v", err)
	}
	if src != "" {
		t.Errorf("expected empty string when StartByte == EndByte, got %q", src)
	}
}

func TestReadSymbolSource_ValidFile(t *testing.T) {
	dir := t.TempDir()
	content := "package main\n\nfunc Hello() {}\n"
	goFile := filepath.Join(dir, "hello.go")
	os.WriteFile(goFile, []byte(content), 0o644)

	sym := db.Symbol{FilePath: "hello.go", StartByte: 14, EndByte: 14 + len("func Hello() {}")}
	src, err := ReadSymbolSource(dir, sym)
	if err != nil {
		t.Fatalf("ReadSymbolSource valid: %v", err)
	}
	if src != "func Hello() {}" {
		t.Errorf("expected %q, got %q", "func Hello() {}", src)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Index: empty directory (no source files)
// ─────────────────────────────────────────────────────────────────────────────

func TestIndex_EmptyDirectory(t *testing.T) {
	idx, _ := newTestIndexer(t)
	emptyDir := t.TempDir()

	result, err := idx.Index(context.Background(), emptyDir, false)
	if err != nil {
		t.Fatalf("Index empty dir: %v", err)
	}
	if result.Files != 0 {
		t.Errorf("expected 0 files in empty dir, got %d", result.Files)
	}
}

func TestIndex_OnlyNonSourceFiles(t *testing.T) {
	idx, _ := newTestIndexer(t)
	dir := t.TempDir()
	// Only README and binary — no source files
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Hello"), 0o644)
	os.WriteFile(filepath.Join(dir, "data.json"), []byte("{}"), 0o644)

	result, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("Index non-source dir: %v", err)
	}
	if result.Files != 0 {
		t.Logf("unexpected source files detected: %d (may include .json if language detected)", result.Files)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Trace: "both" direction
// ─────────────────────────────────────────────────────────────────────────────

func TestTrace_BothDirections(t *testing.T) {
	idx, store := newTestIndexer(t)
	pid := "trace-both"
	store.UpsertProject(db.Project{ID: pid, Path: "/tmp/tb", Name: "tb", IndexedAt: time.Now()})
	store.BulkUpsertSymbols([]db.Symbol{
		{ID: "tb1", ProjectID: pid, FilePath: "a.go", Name: "Root", QualifiedName: "pkg.Root",
			Kind: "Function", Language: "Go", StartByte: 0, EndByte: 10, StartLine: 1, EndLine: 2},
		{ID: "tb2", ProjectID: pid, FilePath: "b.go", Name: "Caller", QualifiedName: "pkg.Caller",
			Kind: "Function", Language: "Go", StartByte: 0, EndByte: 10, StartLine: 1, EndLine: 2},
		{ID: "tb3", ProjectID: pid, FilePath: "c.go", Name: "Callee", QualifiedName: "pkg.Callee",
			Kind: "Function", Language: "Go", StartByte: 0, EndByte: 10, StartLine: 1, EndLine: 2},
	})
	store.BulkUpsertEdges([]db.Edge{
		{ProjectID: pid, FromID: "tb2", ToID: "tb1", Kind: "CALLS", Confidence: 1.0},
		{ProjectID: pid, FromID: "tb1", ToID: "tb3", Kind: "CALLS", Confidence: 1.0},
	})

	// "both" direction should find both Caller (inbound) and Callee (outbound)
	hops, err := idx.Trace(context.Background(), pid, "Root", "both", 2, true)
	if err != nil {
		t.Fatalf("Trace both: %v", err)
	}
	names := map[string]bool{}
	for _, h := range hops {
		names[h.Symbol.Name] = true
	}
	if !names["Caller"] {
		t.Error("expected Caller in both-direction trace")
	}
	if !names["Callee"] {
		t.Error("expected Callee in both-direction trace")
	}
}

func TestTrace_DepthClamp(t *testing.T) {
	idx, store := newTestIndexer(t)
	pid := "trace-clamp"
	store.UpsertProject(db.Project{ID: pid, Path: "/tmp/tc", Name: "tc", IndexedAt: time.Now()})
	store.BulkUpsertSymbols([]db.Symbol{
		{ID: "tc1", ProjectID: pid, FilePath: "a.go", Name: "Fn", QualifiedName: "pkg.Fn",
			Kind: "Function", Language: "Go", StartByte: 0, EndByte: 10, StartLine: 1, EndLine: 2},
	})

	// maxDepth=0 should be clamped to 3
	hops, err := idx.Trace(context.Background(), pid, "Fn", "outbound", 0, false)
	if err != nil {
		t.Fatalf("Trace with 0 depth: %v", err)
	}
	_ = hops // no panic
}

func TestTrace_UnknownSymbol(t *testing.T) {
	idx, store := newTestIndexer(t)
	pid := "trace-unknown"
	store.UpsertProject(db.Project{ID: pid, Path: "/tmp/tu", Name: "tu", IndexedAt: time.Now()})

	_, err := idx.Trace(context.Background(), pid, "NonExistentFn", "outbound", 2, false)
	if err == nil {
		t.Error("expected error for unknown symbol in trace")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// flushBatch: error paths via closed store
// ─────────────────────────────────────────────────────────────────────────────

func TestFlushBatch_SymbolsError(t *testing.T) {
	dir := t.TempDir()
	store, err := db.Open(dir)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	idx := New(store)
	// Close the store first to force errors in BulkUpsertSymbols
	store.Close()

	syms := []db.Symbol{{ID: "s1", ProjectID: "p1", Name: "Fn", Kind: "Function"}}
	err = idx.flushBatch("p1", syms, nil)
	if err == nil {
		t.Error("expected error when store is closed")
	}
}

func TestFlushBatch_EdgesError(t *testing.T) {
	dir := t.TempDir()
	store, err := db.Open(dir)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	idx := New(store)

	pid := "flush-p2"
	store.UpsertProject(db.Project{ID: pid, Path: dir, Name: "p2", IndexedAt: time.Now()})

	// First close so BulkUpsertEdges will fail (after symbols succeed — but with closed db both fail)
	store.Close()

	edges := []db.Edge{{FromID: "s1", ToID: "s2", Kind: "CALLS", ProjectID: pid}}
	err = idx.flushBatch(pid, nil, edges)
	if err == nil {
		t.Error("expected error when store is closed")
	}
}
