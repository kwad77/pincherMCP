package db

import (
	"path/filepath"
	"testing"
)

// #1231 ROOT CAUSE: symbols.id is the primary key without project_id
// included. MakeSymbolID returns "{file_path}::{qualified_name}#{kind}"
// — no project scope. So two projects with the same relative file path
// containing the same symbol collide on PK, and INSERT OR REPLACE
// silently flips the row's project_id to the latest writer.
//
// In the live shared-DB environment, pincher-repo and sniffer (an older
// mirror of pincher) both have `internal/server/server.go` with
// `server.handleSearch#Method`. They produce identical IDs. Whichever
// project indexes last owns every row whose id collides — the older
// project's symbols vanish from queries scoped to its project_id.
//
// This file pins the bug as a failing test (RED) so the fix can be
// validated by re-running it (GREEN). The PR that closes #1231 must
// flip this from t.Fatal to t.Log of the still-correct count.

// Positive: two projects, same relative file, same symbol, both must
// survive in their own project scope.
//
// CURRENTLY SKIPPED — this test demonstrates the #1231 bug as a failing
// assertion. Remove the t.Skip below when the structural fix lands
// (composite PK on symbols, or project_id-prefixed MakeSymbolID). The
// fix PR's CI run flipping this from SKIP to PASS is the close-gate.
func TestSymbol_CrossProjectIDCollision_BothMustSurvive(t *testing.T) {
	t.Skip("#1231: documents the silent cross-project ID collision. Reproduces today; remove this skip in the PR that ships the fix (composite PK or project-scoped MakeSymbolID).")
	t.Parallel()
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	// Two distinct projects.
	if err := store.UpsertProject(Project{ID: "proj-a", Name: "proj-a", Path: "/abs/path/a"}); err != nil {
		t.Fatalf("UpsertProject a: %v", err)
	}
	if err := store.UpsertProject(Project{ID: "proj-b", Name: "proj-b", Path: "/abs/path/b"}); err != nil {
		t.Fatalf("UpsertProject b: %v", err)
	}

	// Same relative file, same qualified name + kind in both projects.
	// Both projects' indexers will produce the SAME id via MakeSymbolID.
	id := MakeSymbolID("internal/server/server.go", "server.handleSearch", "Method")

	symA := Symbol{
		ID:                   id,
		ProjectID:            "proj-a",
		FilePath:             "internal/server/server.go",
		Name:                 "handleSearch",
		QualifiedName:        "server.handleSearch",
		Kind:                 "Method",
		Language:             "Go",
		StartByte:            100, EndByte: 200,
		StartLine: 10, EndLine: 20,
		ExtractionConfidence: 1.0,
	}
	symB := symA
	symB.ProjectID = "proj-b"
	symB.StartByte = 300 // different byte range — distinct symbol in proj-b's file
	symB.EndByte = 400

	// Project A indexes first.
	if err := store.BulkUpsertSymbols([]Symbol{symA}); err != nil {
		t.Fatalf("BulkUpsertSymbols a: %v", err)
	}

	// Project B indexes second — this is where the silent collision
	// fires today. INSERT OR REPLACE replaces the row entirely, flipping
	// project_id from proj-a to proj-b.
	if err := store.BulkUpsertSymbols([]Symbol{symB}); err != nil {
		t.Fatalf("BulkUpsertSymbols b: %v", err)
	}

	// Each project's scoped query must return exactly its own row.
	countA, err := countSymbolsForProject(store, "proj-a")
	if err != nil {
		t.Fatalf("count proj-a: %v", err)
	}
	countB, err := countSymbolsForProject(store, "proj-b")
	if err != nil {
		t.Fatalf("count proj-b: %v", err)
	}

	// CONTRACT (post-#1231-fix): both projects' rows survive.
	// PRE-FIX BEHAVIOUR (the bug): countA == 0, countB == 1 because
	// INSERT OR REPLACE clobbered proj-a's row.
	if countA != 1 {
		t.Errorf("proj-a lost its symbol after proj-b indexed the same id — got %d, want 1 (THIS IS THE #1231 BUG)", countA)
	}
	if countB != 1 {
		t.Errorf("proj-b symbol count = %d; want 1", countB)
	}
}

// Negative: distinct symbols in distinct projects should never trigger
// any collision regardless of fix shape.
func TestSymbol_CrossProjectNoCollision_BothSurvive(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	if err := store.UpsertProject(Project{ID: "proj-a", Name: "proj-a", Path: "/abs/a"}); err != nil {
		t.Fatalf("UpsertProject a: %v", err)
	}
	if err := store.UpsertProject(Project{ID: "proj-b", Name: "proj-b", Path: "/abs/b"}); err != nil {
		t.Fatalf("UpsertProject b: %v", err)
	}

	symA := Symbol{
		ID:        MakeSymbolID("a.go", "pkgA.fn", "Function"),
		ProjectID: "proj-a", FilePath: "a.go", Name: "fn", QualifiedName: "pkgA.fn",
		Kind: "Function", Language: "Go", ExtractionConfidence: 1.0,
	}
	symB := Symbol{
		ID:        MakeSymbolID("b.go", "pkgB.fn", "Function"),
		ProjectID: "proj-b", FilePath: "b.go", Name: "fn", QualifiedName: "pkgB.fn",
		Kind: "Function", Language: "Go", ExtractionConfidence: 1.0,
	}
	if err := store.BulkUpsertSymbols([]Symbol{symA, symB}); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}

	countA, _ := countSymbolsForProject(store, "proj-a")
	countB, _ := countSymbolsForProject(store, "proj-b")
	if countA != 1 || countB != 1 {
		t.Errorf("distinct-id baseline failed: proj-a=%d proj-b=%d; want 1,1", countA, countB)
	}
}

// Control: re-upserting the same symbol in the same project is idempotent.
// INSERT OR REPLACE inside one project's scope is correct.
func TestSymbol_SameProjectReUpsert_Idempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	if err := store.UpsertProject(Project{ID: "proj", Name: "proj", Path: "/abs"}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	sym := Symbol{
		ID:        MakeSymbolID("x.go", "fn", "Function"),
		ProjectID: "proj", FilePath: "x.go", Name: "fn", QualifiedName: "fn",
		Kind: "Function", Language: "Go", ExtractionConfidence: 1.0,
	}

	for i := 0; i < 3; i++ {
		if err := store.BulkUpsertSymbols([]Symbol{sym}); err != nil {
			t.Fatalf("upsert pass %d: %v", i, err)
		}
	}

	count, _ := countSymbolsForProject(store, "proj")
	if count != 1 {
		t.Errorf("3x re-upsert of same id produced count=%d; want 1 (idempotent)", count)
	}
}

func countSymbolsForProject(store *Store, projectID string) (int, error) {
	var n int
	err := store.db.QueryRow(`SELECT COUNT(*) FROM symbols WHERE project_id=?`, projectID).Scan(&n)
	return n, err
}
