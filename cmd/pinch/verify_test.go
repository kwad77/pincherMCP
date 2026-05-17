package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
	"github.com/zeebo/xxh3"
)

// #1399. verify subcommand re-hashes every indexed file's on-disk
// content against the stored files.hash and surfaces three drift
// classes: drifted (file changed since last index), missing (file
// deleted on disk but symbols persist), unreadable (permission /
// other I/O error).

// makeXXH3 mirrors the hash shape the indexer writes
// (internal/index/indexer.go:516).
func makeXXH3(t *testing.T, content []byte) string {
	t.Helper()
	return fmt.Sprintf("%x", xxh3.Hash(content))
}

// TestVerify_AllInSync — every file's on-disk hash matches the
// stored hash; report is clean.
func TestVerify_AllInSync(t *testing.T) {
	root := t.TempDir()
	dbDir := filepath.Join(root, "db")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatalf("mkdir db: %v", err)
	}
	store, err := db.Open(dbDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	projectRoot := filepath.Join(root, "proj")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	a := []byte("package main\n")
	if err := os.WriteFile(filepath.Join(projectRoot, "main.go"), a, 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	if err := store.UpsertProject(db.Project{ID: "p1", Path: projectRoot, Name: "p1", IndexedAt: time.Now()}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := store.SetFileHash("p1", "main.go", makeXXH3(t, a)); err != nil {
		t.Fatalf("SetFileHash: %v", err)
	}

	report, err := buildVerifyReport(store, "")
	if err != nil {
		t.Fatalf("buildVerifyReport: %v", err)
	}
	if report.FilesChecked != 1 {
		t.Errorf("files_checked = %d, want 1", report.FilesChecked)
	}
	if report.FilesInSync != 1 {
		t.Errorf("files_in_sync = %d, want 1", report.FilesInSync)
	}
	if report.FilesDrifted != 0 || report.FilesMissing != 0 || report.FilesUnreadable != 0 {
		t.Errorf("expected zero failures; got drifted=%d missing=%d unreadable=%d",
			report.FilesDrifted, report.FilesMissing, report.FilesUnreadable)
	}
}

// TestVerify_DriftDetected — file modified out-of-band since last
// index; verify reports drift.
func TestVerify_DriftDetected(t *testing.T) {
	root := t.TempDir()
	dbDir := filepath.Join(root, "db")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatalf("mkdir db: %v", err)
	}
	store, err := db.Open(dbDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	projectRoot := filepath.Join(root, "proj")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	original := []byte("package main\n\nfunc Old() {}\n")
	if err := os.WriteFile(filepath.Join(projectRoot, "lib.go"), original, 0o644); err != nil {
		t.Fatalf("write lib.go: %v", err)
	}

	if err := store.UpsertProject(db.Project{ID: "p1", Path: projectRoot, Name: "p1", IndexedAt: time.Now()}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Stamp the hash of the ORIGINAL content, then overwrite the file
	// (simulating an out-of-band edit between index passes).
	if err := store.SetFileHash("p1", "lib.go", makeXXH3(t, original)); err != nil {
		t.Fatalf("SetFileHash: %v", err)
	}
	modified := []byte("package main\n\nfunc New() {}\n")
	if err := os.WriteFile(filepath.Join(projectRoot, "lib.go"), modified, 0o644); err != nil {
		t.Fatalf("modify lib.go: %v", err)
	}

	report, err := buildVerifyReport(store, "")
	if err != nil {
		t.Fatalf("buildVerifyReport: %v", err)
	}
	if report.FilesDrifted != 1 {
		t.Errorf("files_drifted = %d, want 1", report.FilesDrifted)
	}
	if len(report.Projects) != 1 || len(report.Projects[0].Drifted) != 1 ||
		report.Projects[0].Drifted[0] != "lib.go" {
		t.Errorf("expected lib.go in drifted list; got %+v", report.Projects)
	}
}

// TestVerify_MissingFileDetected — file deleted on disk between index
// and verify; report distinguishes missing from drifted.
func TestVerify_MissingFileDetected(t *testing.T) {
	root := t.TempDir()
	dbDir := filepath.Join(root, "db")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatalf("mkdir db: %v", err)
	}
	store, err := db.Open(dbDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	projectRoot := filepath.Join(root, "proj")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	// Stamp a hash for a file that doesn't exist on disk.
	if err := store.UpsertProject(db.Project{ID: "p1", Path: projectRoot, Name: "p1", IndexedAt: time.Now()}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := store.SetFileHash("p1", "ghost.go", "abcdef"); err != nil {
		t.Fatalf("SetFileHash: %v", err)
	}

	report, err := buildVerifyReport(store, "")
	if err != nil {
		t.Fatalf("buildVerifyReport: %v", err)
	}
	if report.FilesMissing != 1 {
		t.Errorf("files_missing = %d, want 1", report.FilesMissing)
	}
	if report.FilesDrifted != 0 {
		t.Errorf("missing-file case should not count as drifted; got drifted=%d", report.FilesDrifted)
	}
}

// TestVerify_ProjectFilter — --project NAME restricts the sweep.
// Two projects seeded; filter matches only one; the other's drift
// stays invisible to this verify pass.
func TestVerify_ProjectFilter(t *testing.T) {
	root := t.TempDir()
	dbDir := filepath.Join(root, "db")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatalf("mkdir db: %v", err)
	}
	store, err := db.Open(dbDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	// Two projects, each with a drifted file.
	for _, p := range []struct{ id, name string }{{"alpha", "alpha-proj"}, {"beta", "beta-proj"}} {
		dir := filepath.Join(root, p.id)
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, "f.go"), []byte("real"), 0o644)
		_ = store.UpsertProject(db.Project{ID: p.id, Path: dir, Name: p.name, IndexedAt: time.Now()})
		_ = store.SetFileHash(p.id, "f.go", "fake-stored-hash") // guaranteed drift
	}

	report, err := buildVerifyReport(store, "alpha")
	if err != nil {
		t.Fatalf("buildVerifyReport: %v", err)
	}
	if len(report.Projects) != 1 {
		t.Errorf("filter alpha matched %d projects; want 1", len(report.Projects))
	}
	if report.Projects[0].Name != "alpha-proj" {
		t.Errorf("matched project = %q, want alpha-proj", report.Projects[0].Name)
	}
}

// Cross-check: empty slices marshal as `[]`, not `null`. JSON consumers
// (CI scripts, dashboards) iterate without a null-check. Pincher's #328
// invariant applied to verify.
func TestVerify_EmptySlicesInJSONShape(t *testing.T) {
	root := t.TempDir()
	dbDir := filepath.Join(root, "db")
	_ = os.MkdirAll(dbDir, 0o755)
	store, _ := db.Open(dbDir)
	defer store.Close()

	report, err := buildVerifyReport(store, "")
	if err != nil {
		t.Fatalf("buildVerifyReport: %v", err)
	}
	if report.Projects == nil {
		t.Error("report.Projects is nil; must be empty slice")
	}
	// Per-project slices initialized on the buildVerifyReport path —
	// no projects in this case, so nothing to assert beyond the
	// top-level Projects shape.
}
