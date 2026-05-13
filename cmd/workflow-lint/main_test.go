package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeWorkflow writes a temporary workflow YAML and returns the path.
func writeWorkflow(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "wf.yml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

// TestLint_CatchesV054Beta1Failure replays the exact bug shape that
// failed the v0.54.0-beta.1 release run: a job that references
// `bash scripts/...` in a `run:` step with no earlier checkout.
func TestLint_CatchesV054Beta1Failure(t *testing.T) {
	wf := `name: Release
on: { push: { tags: ['v*'] } }
jobs:
  checksums:
    name: Checksums + GitHub Release
    runs-on: ubuntu-latest
    steps:
      - uses: actions/download-artifact@v4
        with: { path: dist }
      - name: Determine release channel
        run: |
          CHANNEL="$(bash scripts/release-channel.sh "${GITHUB_REF_NAME}")"
          echo "channel=$CHANNEL"
`
	violations, err := lintFile(writeWorkflow(t, wf))
	if err != nil {
		t.Fatalf("lintFile: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(violations))
	}
	if violations[0].JobID != "checksums" {
		t.Errorf("violation in wrong job: %s", violations[0].JobID)
	}
	if !strings.Contains(violations[0].Snippet, "release-channel.sh") {
		t.Errorf("snippet missing script ref: %q", violations[0].Snippet)
	}
}

func TestLint_PassesWhenCheckoutPresent(t *testing.T) {
	wf := `name: CI
on: { push: { branches: [master] } }
jobs:
  release-channel:
    name: Release channel rule
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Verify channel detection
        run: bash scripts/release-channel_test.sh
`
	violations, err := lintFile(writeWorkflow(t, wf))
	if err != nil {
		t.Fatalf("lintFile: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("expected 0 violations, got %d: %+v", len(violations), violations)
	}
}

func TestLint_DetectsMakeReference(t *testing.T) {
	wf := `name: CI
on: { push: { branches: [master] } }
jobs:
  corpus:
    name: Corpus snapshot
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-go@v5
      - run: make corpus-test
`
	violations, err := lintFile(writeWorkflow(t, wf))
	if err != nil {
		t.Fatalf("lintFile: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation (make ref before checkout), got %d", len(violations))
	}
}

func TestLint_DetectsGoRunRepoLocal(t *testing.T) {
	wf := `name: CI
on: { push: { branches: [master] } }
jobs:
  bench:
    name: Bench
    runs-on: ubuntu-latest
    steps:
      - run: go run ./cmd/closurebench -db /tmp/x.db
`
	violations, err := lintFile(writeWorkflow(t, wf))
	if err != nil {
		t.Fatalf("lintFile: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation (go run ./... before checkout), got %d", len(violations))
	}
}

func TestLint_DetectsRelativeShellScript(t *testing.T) {
	wf := `name: Release
on: { push: { tags: ['v*'] } }
jobs:
  publish:
    name: Publish
    runs-on: ubuntu-latest
    steps:
      - run: ./scripts/publish.sh
`
	violations, err := lintFile(writeWorkflow(t, wf))
	if err != nil {
		t.Fatalf("lintFile: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation (./scripts/ ref), got %d", len(violations))
	}
}

func TestLint_OtherJobCheckoutDoesNotHelp(t *testing.T) {
	// Two jobs: one has checkout, the other references scripts/ without
	// checkout. Per-job isolation — each job runs on a fresh runner.
	wf := `name: CI
on: { push: { branches: [master] } }
jobs:
  with-checkout:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: bash scripts/release-channel_test.sh
  no-checkout:
    runs-on: ubuntu-latest
    steps:
      - run: bash scripts/release-channel.sh v1.0.0
`
	violations, err := lintFile(writeWorkflow(t, wf))
	if err != nil {
		t.Fatalf("lintFile: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation (no-checkout job), got %d", len(violations))
	}
	if violations[0].JobID != "no-checkout" {
		t.Errorf("wrong job flagged: %s", violations[0].JobID)
	}
}

func TestLint_IgnoresInlineBashWithoutScriptRef(t *testing.T) {
	wf := `name: CI
on: { push: { branches: [master] } }
jobs:
  shell:
    runs-on: ubuntu-latest
    steps:
      - name: Just an inline command
        run: |
          echo "hello"
          mkdir -p dist
          ls -la
`
	violations, err := lintFile(writeWorkflow(t, wf))
	if err != nil {
		t.Fatalf("lintFile: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("expected 0 violations (no script refs), got %d", len(violations))
	}
}

func TestLint_HonorsCheckoutVersionAgnostic(t *testing.T) {
	wf := `name: CI
on: { push: { branches: [master] } }
jobs:
  v3:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - run: bash scripts/release-channel.sh v1.0.0
  v5:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - run: bash scripts/release-channel.sh v1.0.0
`
	violations, err := lintFile(writeWorkflow(t, wf))
	if err != nil {
		t.Fatalf("lintFile: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("checkout@v3 and v5 should both count; got %d violations", len(violations))
	}
}

func TestLint_RealRepoWorkflowsClean(t *testing.T) {
	// Belt-and-braces: the actual .github/workflows/ tree must lint clean.
	// If this fails, a real workflow regressed; the CI job will fail
	// independently but this puts the assertion in the lint package's
	// own test suite too.
	violations, err := lintTree("../../.github/workflows")
	if err != nil {
		t.Fatalf("lintTree: %v", err)
	}
	if len(violations) != 0 {
		for _, v := range violations {
			t.Errorf("real-workflow violation: %s :: %s/%d :: %s", v.File, v.JobID, v.StepIdx, v.Snippet)
		}
	}
}

func TestRun_CleanScan(t *testing.T) {
	dir := t.TempDir()
	// Empty workflow dir → no violations.
	var stdout, stderr strings.Builder
	code := run([]string{dir}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("expected exit 0 on empty dir; got %d", code)
	}
	if !strings.Contains(stdout.String(), "clean") {
		t.Errorf("expected 'clean' in stdout; got %q", stdout.String())
	}
}

func TestRun_ReportsViolations(t *testing.T) {
	dir := t.TempDir()
	wf := `name: bad
on: { push: { branches: [master] } }
jobs:
  no-checkout:
    runs-on: ubuntu-latest
    steps:
      - run: bash scripts/release-channel.sh v1.0.0
`
	if err := os.WriteFile(filepath.Join(dir, "bad.yml"), []byte(wf), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	var stdout, stderr strings.Builder
	code := run([]string{dir}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected exit 1 with violation; got %d", code)
	}
	if !strings.Contains(stderr.String(), "violation") {
		t.Errorf("expected violation report on stderr; got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "actions/checkout") {
		t.Errorf("expected fix hint mentioning checkout; got %q", stderr.String())
	}
}

func TestRun_WalkErrorReturns2(t *testing.T) {
	// Nonexistent path → walk fails → exit 2.
	var stdout, stderr strings.Builder
	code := run([]string{"/path/that/should/not/exist/anywhere"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("expected exit 2 on walk error; got %d", code)
	}
	if !strings.Contains(stderr.String(), "workflow-lint:") {
		t.Errorf("expected error tag in stderr; got %q", stderr.String())
	}
}

func TestLint_BadYAMLReturnsError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.yml"), []byte("this: is: not: valid: yaml: ::"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := lintTree(dir); err == nil {
		t.Errorf("expected lintTree to surface YAML parse error")
	}
}

func TestFirstNonEmptyLine_TruncatesLongLines(t *testing.T) {
	long := strings.Repeat("x", 200)
	got := firstNonEmptyLine(long)
	if len(got) > 120 {
		t.Errorf("expected truncation; got len=%d", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("expected '...' suffix; got %q", got)
	}
}

func TestFirstNonEmptyLine_SkipsBlanks(t *testing.T) {
	in := "\n\n  \n  echo hi  \n  next line"
	got := firstNonEmptyLine(in)
	if got != "echo hi" {
		t.Errorf("got %q", got)
	}
}
