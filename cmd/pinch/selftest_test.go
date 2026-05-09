package main

import (
	"os"
	"testing"
)

// Smoke test for the self-test runtime steps. Mirrors `pincher self-test`
// but runs in-process so we can catch regressions in the harness itself
// before they ship as silent self-test failures.
func TestSelfTestSteps_AllPass(t *testing.T) {
	rt := &selfTestRuntime{dataDir: t.TempDir()}
	t.Cleanup(func() {
		if rt.store != nil {
			_ = rt.store.Close()
		}
		if rt.projectDir != "" {
			_ = os.RemoveAll(rt.projectDir)
		}
	})

	steps := []selfTestStep{
		{"open database", openDB},
		{"create synthetic project", createSynthetic},
		{"index the project", indexSynthetic},
		{"search for known symbol", searchSynthetic},
		{"retrieve symbol source", retrieveSynthetic},
	}
	for _, step := range steps {
		if err := step.fn(rt); err != nil {
			t.Fatalf("step %q failed: %v", step.label, err)
		}
	}

	// Post-conditions: rt should carry state forward as each step runs.
	if rt.store == nil {
		t.Error("openDB should populate rt.store")
	}
	if rt.indexer == nil {
		t.Error("openDB should populate rt.indexer")
	}
	if rt.projectDir == "" {
		t.Error("createSynthetic should set rt.projectDir")
	}
	if rt.projectID == "" {
		t.Error("indexSynthetic should set rt.projectID")
	}
	if rt.symbolID == "" {
		t.Error("searchSynthetic should set rt.symbolID")
	}
}

// TestSelfTestStep_SearchFailsOnEmptyIndex ensures the search step fails
// loudly when there's nothing to find — catches a future indexer regression
// that silently produces 0 symbols (the symptom self-test exists to surface).
func TestSelfTestStep_SearchFailsOnEmptyIndex(t *testing.T) {
	rt := &selfTestRuntime{dataDir: t.TempDir()}
	t.Cleanup(func() {
		if rt.store != nil {
			_ = rt.store.Close()
		}
	})
	if err := openDB(rt); err != nil {
		t.Fatalf("openDB: %v", err)
	}
	rt.projectID = "nonexistent-project"

	if err := searchSynthetic(rt); err == nil {
		t.Error("search should fail when project has no indexed symbols")
	}
}
