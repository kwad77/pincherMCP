package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// #936: file-hash dedup skipped re-extraction when the file's content
// hadn't changed even though the extractor HAD changed across binary
// versions. Canonical case: python_extract.py was indexed by the
// pre-#856 regex path and its content hasn't changed since, so the
// new Python-AST extractor never ran on it — it stayed at the regex
// path's symbol shape (no Module symbol for nested-package files).
//
// Fix: when the project's stored binary_version differs from the
// running binary, treat the run as force=true so the new extractor
// path actually runs.

func TestIndex_BinaryDrift_ForcesReindex(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\nfunc main(){}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Initial index with version 0.9.0.
	idx1 := New(store)
	idx1.SetBinaryVersion("0.9.0")
	r1, err := idx1.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("first Index: %v", err)
	}
	if r1.Files == 0 {
		t.Fatal("first run extracted 0 files")
	}

	// Re-index with the SAME binary version — should be a no-op (hash skip).
	idx2 := New(store)
	idx2.SetBinaryVersion("0.9.0")
	r2, err := idx2.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("same-version reindex: %v", err)
	}
	if r2.Files != 0 {
		t.Errorf("same-version reindex extracted %d files; want 0 (hash skip)", r2.Files)
	}
	if r2.Skipped == 0 {
		t.Errorf("same-version reindex skipped %d files; expected non-zero (hash matched)", r2.Skipped)
	}

	// Re-index with a NEW binary version — should force re-extract
	// even though content hash hasn't changed.
	idx3 := New(store)
	idx3.SetBinaryVersion("0.10.0")
	r3, err := idx3.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("new-version reindex: %v", err)
	}
	if r3.Files == 0 {
		t.Errorf("new-version reindex extracted 0 files; expected re-extraction triggered by binary-drift force")
	}
}

// Empty binary_version on either side opts out of the binary-drift
// force. Without this guard, every CLI run with `--version=dev`
// (legitimate dev builds) would nuke the project's hash cache every
// time — defeating the dedup performance win. We accept that legacy
// rows pre-v18 won't auto-recover; explicit `force=true` is the
// workaround there.
func TestIndex_EmptyBinaryVersion_NoForce(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\nfunc main(){}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Initial index with a version stamp.
	idx1 := New(store)
	idx1.SetBinaryVersion("0.9.0")
	if _, err := idx1.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("first Index: %v", err)
	}

	// Re-index without a version stamp (legacy / dev) — should respect
	// the hash skip, NOT force a re-extract.
	idx2 := New(store)
	// SetBinaryVersion intentionally not called.
	r, err := idx2.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("unstamped reindex: %v", err)
	}
	if r.Files != 0 {
		t.Errorf("unstamped reindex extracted %d files; expected 0 (no binary-drift force when version is empty)", r.Files)
	}
}
