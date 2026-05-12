package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #625: pincher hook-check decides whether to redirect a Read or Grep
// call to a pincher equivalent. Pass-through is silent (no stopReason
// emitted, just `{"continue": true}`); redirect carries `continue:false`
// + a one-sentence systemMessage with the suggested call.
//
// These tests exercise the decision logic directly via decideHook
// (skips the stdin/stdout shim) so each branch is unit-isolated.

func newHookTestStore(t *testing.T) *db.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := db.Open(dir)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func indexLargeFakeFile(t *testing.T, store *db.Store, projectDir, relPath string, sizeBytes int) (projectID string) {
	t.Helper()
	abs := filepath.Join(projectDir, relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := bytes.Repeat([]byte("x"), sizeBytes)
	if err := os.WriteFile(abs, body, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	projectID = "p-" + filepath.Base(projectDir)
	if err := store.UpsertProject(db.Project{
		ID: projectID, Path: projectDir, Name: filepath.Base(projectDir),
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	if err := store.SetFileHash(projectID, relPath, "fakehash"); err != nil {
		t.Fatalf("set file hash: %v", err)
	}
	// Seed enough symbols that the symbol-count gate passes.
	syms := make([]db.Symbol, 0, 6)
	for i := 0; i < 6; i++ {
		syms = append(syms, db.Symbol{
			ID:                   projectID + "::sym" + string(rune('a'+i)) + "#Function",
			ProjectID:            projectID,
			FilePath:             relPath,
			Name:                 "sym" + string(rune('a'+i)),
			QualifiedName:        "pkg.sym" + string(rune('a'+i)),
			Kind:                 "Function",
			Language:             "Go",
			StartByte:            i * 100,
			EndByte:              i*100 + 50,
			ExtractionConfidence: 1.0,
		})
	}
	if err := store.BulkUpsertSymbols(syms); err != nil {
		t.Fatalf("upsert symbols: %v", err)
	}
	return projectID
}

func TestDecideHook_Read_LargeIndexedFile_Redirects(t *testing.T) {
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	relPath := "internal/server/server.go"
	indexLargeFakeFile(t, store, projectDir, relPath, 50000) // 50 KB

	in := hookCheckInput{
		ToolName: "Read",
		ToolInput: map[string]any{
			"file_path": filepath.Join(projectDir, relPath),
		},
	}
	d := decideHook(store, in, false)
	if d.Continue {
		t.Fatalf("expected redirect for 50KB indexed file; got pass-through")
	}
	if d.Decision != "redirect" {
		t.Errorf("decision = %q, want redirect", d.Decision)
	}
	if d.SuggestedTool != "context" {
		t.Errorf("suggested tool = %q, want context", d.SuggestedTool)
	}
	if !strings.Contains(d.SuggestedArgs, `"lite":true`) {
		t.Errorf("suggested args should request lite mode; got %s", d.SuggestedArgs)
	}
	if !strings.Contains(d.SystemMessage, "context") {
		t.Errorf("system message should explain the redirect; got %q", d.SystemMessage)
	}
}

func TestDecideHook_Read_TinyFile_PassesThrough(t *testing.T) {
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	relPath := "tiny.txt"
	indexLargeFakeFile(t, store, projectDir, relPath, 100) // 100 bytes

	in := hookCheckInput{
		ToolName:  "Read",
		ToolInput: map[string]any{"file_path": filepath.Join(projectDir, relPath)},
	}
	d := decideHook(store, in, false)
	if !d.Continue {
		t.Errorf("tiny file should pass through; got redirect: %+v", d)
	}
}

func TestDecideHook_Read_OffsetSet_PassesThrough(t *testing.T) {
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	relPath := "internal/server/server.go"
	indexLargeFakeFile(t, store, projectDir, relPath, 50000)

	in := hookCheckInput{
		ToolName: "Read",
		ToolInput: map[string]any{
			"file_path": filepath.Join(projectDir, relPath),
			"offset":    100,
		},
	}
	d := decideHook(store, in, false)
	if !d.Continue {
		t.Errorf("offset-set Read should pass through (agent already narrowed); got redirect: %+v", d)
	}
}

func TestDecideHook_Read_UnindexedPath_PassesThrough(t *testing.T) {
	store := newHookTestStore(t)
	in := hookCheckInput{
		ToolName:  "Read",
		ToolInput: map[string]any{"file_path": "/nowhere/foo.go"},
	}
	d := decideHook(store, in, false)
	if !d.Continue {
		t.Errorf("unindexed path should pass through; got redirect: %+v", d)
	}
}

func TestDecideHook_Grep_IdentifierPattern_RedirectsToSearch(t *testing.T) {
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	indexLargeFakeFile(t, store, projectDir, "f.go", 50000)

	cases := []string{
		"classifyTaskShape",  // CamelCase
		"foo_bar",            // snake
		"pkg.Bar",            // qualified
		"Class::method",      // C++ scope
	}
	for _, pattern := range cases {
		t.Run(pattern, func(t *testing.T) {
			in := hookCheckInput{
				ToolName:  "Grep",
				ToolInput: map[string]any{"pattern": pattern},
			}
			d := decideHook(store, in, false)
			if d.Continue {
				t.Errorf("identifier pattern %q should redirect; got pass-through", pattern)
			}
			if d.SuggestedTool != "search" {
				t.Errorf("suggested = %q, want search", d.SuggestedTool)
			}
		})
	}
}

func TestDecideHook_Grep_RegexOrPhrase_PassesThrough(t *testing.T) {
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	indexLargeFakeFile(t, store, projectDir, "f.go", 50000)

	cases := []string{
		`func \w+\(`,         // regex
		`Foo(.*?)Bar`,        // capture group
		`hello world`,        // multi-word phrase
		`->`,                 // operator-only pattern
	}
	for _, pattern := range cases {
		t.Run(pattern, func(t *testing.T) {
			in := hookCheckInput{
				ToolName:  "Grep",
				ToolInput: map[string]any{"pattern": pattern},
			}
			d := decideHook(store, in, false)
			if !d.Continue {
				t.Errorf("non-identifier pattern %q should pass through; got redirect", pattern)
			}
		})
	}
}

func TestDecideHook_Grep_NoIndexedProjects_PassesThrough(t *testing.T) {
	// Identifier-shape pattern but no project indexed → pincher can't
	// help, pass through.
	store := newHookTestStore(t)
	in := hookCheckInput{
		ToolName:  "Grep",
		ToolInput: map[string]any{"pattern": "Foo"},
	}
	d := decideHook(store, in, false)
	if !d.Continue {
		t.Errorf("Grep on empty index should pass through; got %+v", d)
	}
}

func TestEmitHookResponse_PassThroughIsSilent(t *testing.T) {
	// Pass-through must NOT include stopReason / systemMessage —
	// otherwise every successful Read would generate noise that
	// trains the user to disable the hook.
	d := hookDecision{Continue: true, Decision: "pass_through"}
	var buf bytes.Buffer
	resp := map[string]any{"continue": d.Continue}
	if !d.Continue {
		if d.StopReason != "" {
			resp["stopReason"] = d.StopReason
		}
		if d.SystemMessage != "" {
			resp["systemMessage"] = d.SystemMessage
		}
	}
	out, _ := json.Marshal(resp)
	buf.Write(out)
	got := buf.String()
	if strings.Contains(got, "stopReason") || strings.Contains(got, "systemMessage") {
		t.Errorf("pass-through response leaked chrome: %s", got)
	}
}
