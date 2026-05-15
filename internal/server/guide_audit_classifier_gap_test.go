package server

import (
	"strings"
	"testing"
)

// #924: natural threshold/audit phrasings that #912 missed because they
// drop the "every|all|any" article ("find functions longer than 100
// lines") or use a standalone audit adjective ("find untested exported
// functions"). The latter was being silently routed to shapeTest by
// the bare "test" substring check; the former fell to shapeFind and
// got recommended as a BM25 search of the literal phrase.

func TestClassifyTaskShape_AuditLooseThreshold(t *testing.T) {
	t.Parallel()
	cases := []string{
		"find functions longer than 100 lines",
		"list functions longer than 50 lines",
		"surface methods bigger than 200 lines",
		"show classes deeper than 5 levels",
		"find handlers slower than 100ms",
		"find handlers faster than the median",
	}
	for _, task := range cases {
		t.Run(task, func(t *testing.T) {
			if got := classifyTaskShape(task); got != shapeAudit {
				t.Errorf("classifyTaskShape(%q) = %v, want shapeAudit", task, got)
			}
		})
	}
}

func TestClassifyTaskShape_AuditAdjective(t *testing.T) {
	t.Parallel()
	cases := []string{
		"find untested exported functions",
		"list untested methods",
		"surface undocumented public APIs",
		"find uncovered handlers",
		"show me untyped exports",
		"list unhandled errors",
		"find unauthenticated endpoints",
	}
	for _, task := range cases {
		t.Run(task, func(t *testing.T) {
			if got := classifyTaskShape(task); got != shapeAudit {
				t.Errorf("classifyTaskShape(%q) = %v, want shapeAudit", task, got)
			}
		})
	}
}

// Regression guards: existing routes should still claim their tasks.
// "unused" stays with shapeDeadCode (more specific); plain "test" /
// "coverage" without an audit adjective stays with shapeTest.
func TestClassifyTaskShape_AuditAdjectiveDoesNotStealNonAudits(t *testing.T) {
	t.Parallel()
	cases := map[string]guideShape{
		"find unused functions":                 shapeDeadCode,
		"write tests for the new handler":       shapeTest,
		"add test coverage for the auth module": shapeTest,
	}
	for task, want := range cases {
		t.Run(task, func(t *testing.T) {
			if got := classifyTaskShape(task); got != want {
				t.Errorf("classifyTaskShape(%q) = %v, want %v", task, got, want)
			}
		})
	}
}

// End-to-end: a loose-threshold task gets a complexity / lines query,
// an adjective task gets the right narrowed template, and none of
// them get the docstring fallback by accident.
func TestGuideRecommendations_LooseThresholdGetsLinesQuery(t *testing.T) {
	t.Parallel()
	recs := guideRecommendations(shapeAudit, "longer than 100 lines", "",
		"find functions longer than 100 lines")
	if len(recs) == 0 {
		t.Fatal("audit shape should produce at least one recommendation")
	}
	first := recs[0]
	if first["tool"] != "query" {
		t.Errorf("first tool = %q, want query", first["tool"])
	}
	if !strings.Contains(first["args"], "end_line - n.start_line") {
		t.Errorf("loose-threshold lines task must emit (end_line - start_line) query; got %q", first["args"])
	}
}

func TestGuideRecommendations_AdjectiveUntestedGetsCoverageQuery(t *testing.T) {
	t.Parallel()
	recs := guideRecommendations(shapeAudit, "untested exported functions", "",
		"find untested exported functions")
	if len(recs) == 0 {
		t.Fatal("audit shape should produce at least one recommendation")
	}
	first := recs[0]
	if first["tool"] != "query" {
		t.Errorf("first tool = %q, want query", first["tool"])
	}
	args := first["args"]
	if !strings.Contains(args, "is_exported=true AND n.is_test=false") {
		t.Errorf("untested task must emit non-test exported query; got %q", args)
	}
	// #923: must be scoped to Go to avoid regex-tier noise.
	if !strings.Contains(args, `n.language='Go'`) {
		t.Errorf("untested task must scope to Go; got %q", args)
	}
}

// #923: the docstring fallback template now scopes to Go + non-test
// so it doesn't recommend a query that returns JS/Bash regex-tier
// false positives or test functions.
func TestInferAuditPinchQL_DocstringFallbackExcludesTestsAndNonGo(t *testing.T) {
	t.Parallel()
	pinchql, _ := inferAuditPinchQL("audit exported APIs")
	if !strings.Contains(pinchql, "docstring IS NULL") {
		t.Fatalf("fallback should still emit docstring template; got %q", pinchql)
	}
	if !strings.Contains(pinchql, "n.is_test=false") {
		t.Errorf("docstring fallback must exclude test functions; got %q", pinchql)
	}
	if !strings.Contains(pinchql, `n.language='Go'`) {
		t.Errorf("docstring fallback must scope to Go; got %q", pinchql)
	}
}
