package index

import (
	"context"
	"os/exec"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// Python IMPORTS edge resolution end-to-end test. Builds a src-layout
// project on disk, indexes it, and asserts that:
//  1. Each .py file gets a Module symbol whose QN is the file's
//     dotted relpath (src.myproj.config etc.).
//  2. The `from myproj.config import ServerSpec` in main.py resolves
//     into an IMPORTS edge to src.myproj.config — i.e. the src-prefix
//     gap from the user's report is closed.
//
// Skips when python3 is unavailable; the AST extractor is the only
// path that emits Module symbols for Python, so the resolver can't
// match either side without it.
func TestIndex_PythonImportsResolveAcrossSrcLayout(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH; Python AST resolution test skipped")
	}

	idx, store := newTestIndexer(t)
	dir := t.TempDir()

	writeFile(t, dir, "pyproject.toml", `
[tool.setuptools.packages.find]
where = ["src"]
`)
	writeFile(t, dir, "src/myproj/__init__.py", "")
	writeFile(t, dir, "src/myproj/config.py", `class ServerSpec:
    pass
`)
	writeFile(t, dir, "src/myproj/main.py", `from myproj.config import ServerSpec

def run():
    return ServerSpec()
`)

	if _, err := idx.Index(context.Background(), dir, true); err != nil {
		t.Fatalf("Index: %v", err)
	}
	projectID := db.ProjectIDFromPath(dir)

	mainMods, err := store.GetSymbolsByQN(projectID, "src.myproj.main")
	if err != nil || len(mainMods) == 0 {
		t.Fatalf("expected Module symbol src.myproj.main, got %d (err=%v)", len(mainMods), err)
	}
	if mainMods[0].Kind != "Module" {
		t.Errorf("main kind = %q, want Module", mainMods[0].Kind)
	}

	// The import target was written as `myproj.config.ServerSpec`; the
	// resolver should prepend the `src` source-root prefix and find the
	// class symbol.
	targetSyms, err := store.GetSymbolsByQN(projectID, "src.myproj.config.ServerSpec")
	if err != nil || len(targetSyms) == 0 {
		t.Fatalf("expected ServerSpec class symbol, got %d (err=%v)", len(targetSyms), err)
	}

	edges, err := store.EdgesFrom(mainMods[0].ID, nil)
	if err != nil {
		t.Fatalf("EdgesFrom: %v", err)
	}
	var found bool
	for _, e := range edges {
		if e.Kind == "IMPORTS" && e.ToID == targetSyms[0].ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected IMPORTS edge from src.myproj.main to src.myproj.config.ServerSpec; edges=%+v", edges)
	}
}

// Without source-root awareness, the resolver can't bridge "myproj.config"
// (Python import path) and "src.myproj.config" (pincher's file-path QN).
// Setting PINCHER_DISABLE_PY_AST=1 forces the regex path, which doesn't
// emit Module symbols — IMPORTS stays unresolved. This is the negative
// baseline that proves the AST path is doing the resolution work.
func TestIndex_PythonImportsUnresolvedWithoutAST(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
	t.Setenv("PINCHER_DISABLE_PY_AST", "1")

	idx, store := newTestIndexer(t)
	dir := t.TempDir()

	writeFile(t, dir, "src/myproj/__init__.py", "")
	writeFile(t, dir, "src/myproj/config.py", `class ServerSpec:
    pass
`)
	writeFile(t, dir, "src/myproj/main.py", `from myproj.config import ServerSpec
`)

	if _, err := idx.Index(context.Background(), dir, true); err != nil {
		t.Fatalf("Index: %v", err)
	}
	projectID := db.ProjectIDFromPath(dir)

	// Regex extractor doesn't emit Module symbols → from-side has nothing
	// to resolve against → no IMPORTS edges land. This is the bug the AST
	// path closes; the test pins the baseline so we notice if regex starts
	// emitting Modules in the future.
	mods, err := store.GetSymbolsByQN(projectID, "src.myproj.main")
	if err != nil {
		t.Fatalf("GetSymbolsByQN: %v", err)
	}
	for _, m := range mods {
		if m.Kind == "Module" {
			t.Errorf("regex path should not emit Module symbols for Python, got %+v", m)
		}
	}
}
