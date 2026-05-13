// workflow-lint catches a recurring class of GitHub Actions workflow bugs
// where a job's `run:` block references a repo-relative script (bash
// scripts/foo.sh, ./scripts/bar.sh, make corpus-test, go run ./cmd/baz)
// without an earlier `actions/checkout@v4` step in the same job.
//
// Symptom in the wild (#690): v0.54.0-beta.1 release run failed at the
// "Determine release channel" step with `bash: scripts/release-channel.sh:
// No such file or directory` because the checksums job ran on a fresh
// runner that downloaded artifacts but never checked out the repo. That
// failure mode is invisible to YAML linting, invisible to local test
// suites, and only surfaces at tag time — slow, public, and the failed
// tag artifacts linger.
//
// What this tool catches:
//
//   - jobs with at least one `run:` step that calls into the repo
//     (bash scripts/, ./scripts/, ./.bin/, make <target>, go run ./...)
//     but no `uses: actions/checkout@v4` step earlier in the job's
//     step list
//
// What this tool does NOT catch (file as follow-ups if needed):
//
//   - inline-divergence from canonical scripts (bucket 2 in #690)
//   - missing tag-shaped probe before merge (bucket 3 in #690)
//
// Run via: `go run ./cmd/workflow-lint`. Exit 0 = clean; exit 1 =
// at least one job has a missing-checkout violation. CI invokes this
// from the new `workflow-lint` job in .github/workflows/ci.yml.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// scriptRefPattern matches the script-reference shapes we want to gate
// against. Substring match — we don't constrain the preceding character
// because real-world scripts get wrapped in $(...), assigned to vars,
// chained after &&, etc. False positives from script-ref strings that
// only appear inside comments are vanishingly rare and not worth the
// regex complexity to suppress.
var scriptRefPattern = regexp.MustCompile(`bash\s+(?:\./)?scripts/|(?:\./)?scripts/[^\s]+\.sh|(?:\./)?\.bin/|\bmake\s+\w|\bgo\s+run\s+\./`)

// checkoutPattern matches any actions/checkout invocation. Version-agnostic
// since lint should pass for v3, v4, v5 — the bug is missing-checkout, not
// wrong-version.
var checkoutPattern = regexp.MustCompile(`actions/checkout@v\d+`)

type step struct {
	Uses string `yaml:"uses"`
	Name string `yaml:"name"`
	Run  string `yaml:"run"`
}

type job struct {
	Name  string `yaml:"name"`
	Steps []step `yaml:"steps"`
}

type workflow struct {
	Name string         `yaml:"name"`
	Jobs map[string]job `yaml:"jobs"`
}

type violation struct {
	File    string
	JobID   string
	JobName string
	StepIdx int
	Snippet string
}

func main() {
	root := ".github/workflows"
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	violations, err := lintTree(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow-lint: %v\n", err)
		os.Exit(2)
	}
	if len(violations) == 0 {
		fmt.Printf("workflow-lint: clean (scanned %s)\n", root)
		return
	}
	fmt.Fprintf(os.Stderr, "workflow-lint: %d violation(s):\n\n", len(violations))
	for _, v := range violations {
		fmt.Fprintf(os.Stderr, "  %s :: job=%s (%s) :: step %d\n    references repo without prior actions/checkout@vN:\n      %s\n\n",
			v.File, v.JobID, v.JobName, v.StepIdx, v.Snippet)
	}
	fmt.Fprintln(os.Stderr, "fix: add `- uses: actions/checkout@v4` as a step before the run-script reference.")
	os.Exit(1)
}

// lintTree walks `root` looking for *.yml/*.yaml files and lints each.
func lintTree(root string) ([]violation, error) {
	var out []violation
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if ext := filepath.Ext(p); ext != ".yml" && ext != ".yaml" {
			return nil
		}
		vs, err := lintFile(p)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		out = append(out, vs...)
		return nil
	})
	return out, err
}

// lintFile parses one workflow YAML and returns any missing-checkout
// violations across its jobs.
func lintFile(p string) ([]violation, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var wf workflow
	if err := yaml.Unmarshal(data, &wf); err != nil {
		return nil, fmt.Errorf("yaml parse: %w", err)
	}
	var out []violation
	for jobID, j := range wf.Jobs {
		out = append(out, lintJob(p, jobID, j)...)
	}
	return out, nil
}

// lintJob walks a job's step list, tracking whether a checkout has run.
// Returns one violation per script-referencing step that fires before
// any checkout step.
func lintJob(file, jobID string, j job) []violation {
	var out []violation
	checkedOut := false
	for i, s := range j.Steps {
		if checkoutPattern.MatchString(s.Uses) {
			checkedOut = true
			continue
		}
		if s.Run == "" || checkedOut {
			continue
		}
		if loc := scriptRefPattern.FindString(s.Run); loc != "" {
			out = append(out, violation{
				File:    file,
				JobID:   jobID,
				JobName: j.Name,
				StepIdx: i,
				Snippet: firstNonEmptyLine(s.Run),
			})
		}
	}
	return out
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if len(t) > 120 {
			return t[:117] + "..."
		}
		return t
	}
	return "<empty>"
}
