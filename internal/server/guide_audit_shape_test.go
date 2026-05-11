package server

import (
	"strings"
	"testing"
)

// #467: `guide task="find an undocumented exported function"` previously
// returned a BM25 search recommendation for the literal phrase, which
// matches nothing. The fix recognises audit shapes and routes to
// pinchQL `query` against the docstring property (#438).

func TestClassifyTaskShape_AuditUndocumented(t *testing.T) {
	cases := []string{
		"find an undocumented exported function",
		"list functions with no docstring",
		"survey undocumented APIs",
		"every exported method missing docstring",
		"functions without docstring",
		"audit functions missing comment",
	}
	for _, task := range cases {
		t.Run(task, func(t *testing.T) {
			got := classifyTaskShape(task)
			if got != shapeAudit {
				t.Errorf("classifyTaskShape(%q) = %v, want shapeAudit", task, got)
			}
		})
	}
}

func TestClassifyTaskShape_AuditDoesNotOvercatch(t *testing.T) {
	// Generic find / understand tasks should NOT fall into shapeAudit.
	cases := map[string]guideShape{
		"find the auth middleware":           shapeFind,
		"understand how indexing works":      shapeUnderstand,
		"fix the docstring extraction bug":   shapeFix,
		"add docstring lookup hint":          shapeAdd,
	}
	for task, want := range cases {
		t.Run(task, func(t *testing.T) {
			if got := classifyTaskShape(task); got != want {
				t.Errorf("classifyTaskShape(%q) = %v, want %v", task, got, want)
			}
		})
	}
}

func TestGuideRecommendations_AuditEmitsPinchQL(t *testing.T) {
	recs := guideRecommendations(shapeAudit, "undocumented exported functions")
	if len(recs) == 0 {
		t.Fatal("audit shape should produce at least one recommendation")
	}
	first := recs[0]
	if first["tool"] != "query" {
		t.Errorf("first recommendation tool = %q, want query", first["tool"])
	}
	args := first["args"]
	if !strings.Contains(args, "docstring IS NULL") {
		t.Errorf("audit query should filter on docstring IS NULL; got args=%q", args)
	}
	if !strings.Contains(args, "is_exported=true") {
		t.Errorf("audit query should filter on is_exported=true; got args=%q", args)
	}
}
