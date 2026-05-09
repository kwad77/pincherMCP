package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const samplePolicy = "## Pincher Usage Policy\n\nPrefer pincher tools over Read/Grep/Glob.\n"

// withCWD switches into dir for the duration of the test, restoring on
// cleanup. Required for target pathFn calls that resolve from cwd.
func withCWD(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

// withHome overrides HOME / USERPROFILE so UserHomeDir() returns dir.
// Continue and claude --global both look up the user's home directory;
// this lets tests target a temp directory.
func withHome(t *testing.T, dir string) {
	t.Helper()
	if v, ok := os.LookupEnv("HOME"); ok {
		t.Setenv("HOME", v)
	}
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

func TestInitTargets_RegistryShape(t *testing.T) {
	want := []string{"claude", "cursor", "cursor-legacy", "windsurf", "aider", "continue"}
	if len(allInitTargets) != len(want) {
		t.Fatalf("registry has %d targets, want %d", len(allInitTargets), len(want))
	}
	for i, t0 := range allInitTargets {
		if t0.name != want[i] {
			t.Errorf("registry[%d].name = %q, want %q", i, t0.name, want[i])
		}
		if t0.pathFn == nil || t0.writeFn == nil {
			t.Errorf("target %q missing pathFn or writeFn", t0.name)
		}
	}
	names := initTargetNames()
	if !strings.Contains(strings.Join(names, ","), "detect") {
		t.Error("initTargetNames should include 'detect'")
	}
	if !strings.Contains(strings.Join(names, ","), "all") {
		t.Error("initTargetNames should include 'all'")
	}
}

func TestFindInitTarget(t *testing.T) {
	if _, ok := findInitTarget("cursor"); !ok {
		t.Error("expected to find 'cursor'")
	}
	if _, ok := findInitTarget("nonexistent"); ok {
		t.Error("unexpected hit for 'nonexistent'")
	}
}

// ─── cursor (modern .mdc) ────────────────────────────────────────────────────

func TestCursor_FreshWriteEmitsFrontmatter(t *testing.T) {
	out, action := cursorMDCWriter("", samplePolicy)
	if action != "wrote" {
		t.Errorf("action=%q, want wrote", action)
	}
	if !strings.HasPrefix(out, "---\n") {
		t.Error("cursor .mdc must start with YAML frontmatter delimiter")
	}
	if !strings.Contains(out, "alwaysApply: true") {
		t.Error("expected alwaysApply: true in frontmatter")
	}
	if !strings.Contains(out, pincherInitMarkerStart) {
		t.Error("expected pincher start marker in body")
	}
}

func TestCursor_PreservesUserEditedFrontmatter(t *testing.T) {
	custom := "---\n" +
		"description: my custom rules\n" +
		"globs:\n  - \"src/**\"\n" +
		"alwaysApply: false\n" +
		"---\n\n" +
		"# Some prose I wrote\n\n" +
		pincherInitMarkerStart + "\n" +
		"OLD POLICY\n" +
		pincherInitMarkerEnd + "\n"
	out, action := cursorMDCWriter(custom, samplePolicy)
	if action != "updated" {
		t.Errorf("action=%q, want updated", action)
	}
	if !strings.Contains(out, "my custom rules") {
		t.Error("custom frontmatter description should be preserved")
	}
	if !strings.Contains(out, `globs:`+"\n  - \"src/**\"") {
		t.Error("custom globs should be preserved")
	}
	if !strings.Contains(out, "alwaysApply: false") {
		t.Error("custom alwaysApply should be preserved")
	}
	if !strings.Contains(out, "# Some prose I wrote") {
		t.Error("user prose should be preserved")
	}
	if strings.Contains(out, "OLD POLICY") {
		t.Error("old policy body should have been replaced")
	}
	if !strings.Contains(out, "Pincher Usage Policy") {
		t.Error("new policy body should be present")
	}
}

func TestCursor_IdempotentRewrite(t *testing.T) {
	first, _ := cursorMDCWriter("", samplePolicy)
	second, action := cursorMDCWriter(first, samplePolicy)
	if action != "updated" {
		t.Errorf("second write action=%q, want updated", action)
	}
	if first != second {
		t.Errorf("non-idempotent rewrite:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestSplitMDXFrontmatter(t *testing.T) {
	cases := []struct {
		name           string
		input          string
		wantFM, wantBd string
	}{
		{
			name:   "no frontmatter",
			input:  "just a body\nwith two lines\n",
			wantFM: "",
			wantBd: "just a body\nwith two lines\n",
		},
		{
			name:   "simple frontmatter",
			input:  "---\nfoo: bar\n---\nbody\n",
			wantFM: "---\nfoo: bar\n---\n",
			wantBd: "body\n",
		},
		{
			name:   "frontmatter with blank line before body",
			input:  "---\nfoo: bar\n---\n\nbody\n",
			wantFM: "---\nfoo: bar\n---\n\n",
			wantBd: "body\n",
		},
		{
			name:   "malformed (no close)",
			input:  "---\nfoo: bar\nbody\n",
			wantFM: "",
			wantBd: "---\nfoo: bar\nbody\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fm, bd := splitMDXFrontmatter(c.input)
			if fm != c.wantFM {
				t.Errorf("frontmatter=%q, want %q", fm, c.wantFM)
			}
			if bd != c.wantBd {
				t.Errorf("body=%q, want %q", bd, c.wantBd)
			}
		})
	}
}

// ─── cursor-legacy / windsurf / aider (plain mergePolicyBlock) ───────────────

func TestCursorLegacy_FreshAndIdempotent(t *testing.T) {
	first, action := cursorLegacyInitTarget.writeFn("", samplePolicy)
	if action != "wrote" {
		t.Errorf("first action=%q, want wrote", action)
	}
	if !strings.Contains(first, "Pincher Usage Policy") {
		t.Error("expected policy body")
	}
	second, action := cursorLegacyInitTarget.writeFn(first, samplePolicy)
	if action != "updated" {
		t.Errorf("second action=%q, want updated", action)
	}
	if first != second {
		t.Error("cursor-legacy rewrite was not idempotent")
	}
}

func TestWindsurf_PreservesExistingContent(t *testing.T) {
	existing := "# My Windsurf Rules\n\nUse strict types.\n"
	out, action := windsurfInitTarget.writeFn(existing, samplePolicy)
	if action != "appended" {
		t.Errorf("action=%q, want appended", action)
	}
	if !strings.Contains(out, "Use strict types.") {
		t.Error("existing windsurf content should be preserved")
	}
	if !strings.Contains(out, pincherInitMarkerStart) {
		t.Error("expected pincher block to be appended")
	}
}

func TestAider_GlobalIsRejected(t *testing.T) {
	if _, err := aiderInitTarget.pathFn(true); err == nil {
		t.Error("aider --global should error (not yet implemented)")
	}
}

// ─── continue (~/.continue/config.json JSON merge) ───────────────────────────

func TestContinue_FreshWriteProducesValidJSON(t *testing.T) {
	out, action := continueJSONWriter("", samplePolicy)
	if action != "wrote" {
		t.Errorf("action=%q, want wrote", action)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(out), &cfg); err != nil {
		t.Fatalf("output is not valid JSON: %v\n--- output ---\n%s", err, out)
	}
	msg, ok := cfg["systemMessage"].(string)
	if !ok {
		t.Fatal("expected systemMessage to be a string")
	}
	if !strings.Contains(msg, "// pincher:start") || !strings.Contains(msg, "// pincher:end") {
		t.Error("expected line-prefixed pincher markers in systemMessage")
	}
	if !strings.Contains(msg, "Pincher Usage Policy") {
		t.Error("expected policy body in systemMessage")
	}
}

func TestContinue_PreservesUnknownKeysAndExistingMessage(t *testing.T) {
	prior, _ := json.Marshal(map[string]any{
		"models":        []map[string]string{{"title": "claude", "provider": "anthropic"}},
		"systemMessage": "Be concise.",
	})
	out, action := continueJSONWriter(string(prior), samplePolicy)
	if action != "appended" {
		t.Errorf("action=%q, want appended", action)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(out), &cfg); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if _, ok := cfg["models"]; !ok {
		t.Error("models key should be preserved")
	}
	msg, _ := cfg["systemMessage"].(string)
	if !strings.Contains(msg, "Be concise.") {
		t.Error("prior systemMessage should be preserved")
	}
	if !strings.Contains(msg, "// pincher:start") {
		t.Error("expected pincher markers")
	}
}

func TestContinue_IdempotentReplace(t *testing.T) {
	first, _ := continueJSONWriter("", samplePolicy)
	second, action := continueJSONWriter(first, samplePolicy)
	if action != "updated" {
		t.Errorf("action=%q, want updated", action)
	}
	if first != second {
		t.Errorf("continue rewrite not idempotent\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestContinue_MalformedJSONReturnsError(t *testing.T) {
	out, action := continueJSONWriter("{this is not valid", samplePolicy)
	if action != "error" {
		t.Errorf("action=%q, want error", action)
	}
	if out != "{this is not valid" {
		t.Error("malformed JSON should be returned unchanged")
	}
}

// ─── pathFn / detection wired to a temp cwd ──────────────────────────────────

func TestCursorPathFn_RelativeToCwd(t *testing.T) {
	tmp := t.TempDir()
	withCWD(t, tmp)
	got, err := cursorInitTarget.pathFn(false)
	if err != nil {
		t.Fatalf("pathFn: %v", err)
	}
	// Resolve symlinks (macOS /private/tmp vs /tmp) before comparing the
	// directory portion so the test isn't fooled by the indirection.
	gotAbs, _ := filepath.EvalSymlinks(filepath.Dir(filepath.Dir(filepath.Dir(got))))
	wantAbs, _ := filepath.EvalSymlinks(tmp)
	if gotAbs != wantAbs {
		t.Errorf("cursor path = %q (resolved dir %q), want under %q", got, gotAbs, wantAbs)
	}
	if !strings.HasSuffix(got, filepath.Join(".cursor", "rules", "pincher.mdc")) {
		t.Errorf("cursor path %q lacks expected suffix", got)
	}
}

func TestDetectInitTargets_FindsCursorAndWindsurf(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".windsurfrules"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	hits := detectInitTargets(tmp)
	names := make([]string, 0, len(hits))
	for _, h := range hits {
		names = append(names, h.name)
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "cursor") {
		t.Errorf("expected cursor in detection (.cursor/ exists), got %q", joined)
	}
	if !strings.Contains(joined, "windsurf") {
		t.Errorf("expected windsurf in detection (.windsurfrules exists), got %q", joined)
	}
}

func TestDetectInitTargets_EmptyDirFallsBackToClaude(t *testing.T) {
	tmp := t.TempDir()
	// Override home so the continue target's home-based detection
	// doesn't accidentally fire on a developer machine that has
	// ~/.continue/.
	withHome(t, tmp)
	hits := detectInitTargets(tmp)
	if len(hits) != 1 || hits[0].name != "claude" {
		names := []string{}
		for _, h := range hits {
			names = append(names, h.name)
		}
		t.Errorf("empty dir detection = %v, want [claude]", names)
	}
}

// ─── resolveTargets (the --target flag dispatcher) ───────────────────────────

func TestResolveTargets(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		wantLen  int
		wantErr  bool
		wantName string // if wantLen==1
	}{
		{name: "single", input: "windsurf", wantLen: 1, wantName: "windsurf"},
		{name: "all", input: "all", wantLen: len(allInitTargets)},
		{name: "unknown", input: "vim", wantErr: true},
		{name: "empty", input: "", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveTargets(c.input)
			if c.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != c.wantLen {
				t.Errorf("got %d targets, want %d", len(got), c.wantLen)
			}
			if c.wantName != "" && got[0].name != c.wantName {
				t.Errorf("got name %q, want %q", got[0].name, c.wantName)
			}
		})
	}
}

// ─── runInitTarget end-to-end against a temp dir ─────────────────────────────

func TestRunInitTarget_WritesAndIsIdempotent(t *testing.T) {
	tmp := t.TempDir()
	withCWD(t, tmp)

	var buf strings.Builder
	if err := runInitTarget(&buf, cursorInitTarget, false, false); err != nil {
		t.Fatalf("first write: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(tmp, ".cursor", "rules", "pincher.mdc"))
	if err != nil {
		t.Fatalf("expected file written: %v", err)
	}
	if !strings.HasPrefix(string(got), "---\n") {
		t.Error("expected MDC frontmatter on first write")
	}
	first := string(got)

	// Re-run; content should match exactly.
	if err := runInitTarget(&buf, cursorInitTarget, false, false); err != nil {
		t.Fatalf("second write: %v", err)
	}
	got2, _ := os.ReadFile(filepath.Join(tmp, ".cursor", "rules", "pincher.mdc"))
	if string(got2) != first {
		t.Error("re-running runInitTarget changed file contents (not idempotent)")
	}
}

func TestRunInitTarget_DryRunDoesNotWrite(t *testing.T) {
	tmp := t.TempDir()
	withCWD(t, tmp)

	var buf strings.Builder
	if err := runInitTarget(&buf, windsurfInitTarget, false, true); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".windsurfrules")); !os.IsNotExist(err) {
		t.Errorf("dry run should NOT create file; stat err=%v", err)
	}
	if !strings.Contains(buf.String(), "would wrote") {
		t.Errorf("expected 'would wrote' in dry-run output, got: %s", buf.String())
	}
}

func TestRunInitTarget_GlobalIgnoredForUnsupportedTargets(t *testing.T) {
	tmp := t.TempDir()
	withCWD(t, tmp)
	withHome(t, tmp)

	var buf strings.Builder
	// windsurf has supportsGlobal=false; passing global=true should
	// silently fall back to project-local rather than erroring.
	if err := runInitTarget(&buf, windsurfInitTarget, true, false); err != nil {
		t.Fatalf("windsurf with --global should not error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".windsurfrules")); err != nil {
		t.Errorf("expected project-local windsurf file: %v", err)
	}
}

func TestRunInitTarget_ContinueAlwaysGlobal(t *testing.T) {
	tmp := t.TempDir()
	withHome(t, tmp)

	var buf strings.Builder
	if err := runInitTarget(&buf, continueInitTarget, false, false); err != nil {
		t.Fatalf("continue: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(tmp, ".continue", "config.json"))
	if err != nil {
		t.Fatalf("expected ~/.continue/config.json: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(got, &cfg); err != nil {
		t.Fatalf("written config.json is not valid JSON: %v", err)
	}
	if _, ok := cfg["systemMessage"]; !ok {
		t.Error("expected systemMessage key")
	}
}
