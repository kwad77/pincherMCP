package server

import (
	"context"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #289: FTS5 raises "syntax error near '.'" when the query is a bare
// dotted identifier. The sanitizer auto-quotes the dotted token so
// `search query="os.Stat"` just works without the caller learning
// FTS5 phrase syntax.
func TestSanitizeFTS5Query_DottedIdentifier(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		// Bare dotted identifiers get wrapped.
		{"os.Stat", `"os.Stat"`},
		{"fmt.Errorf", `"fmt.Errorf"`},
		{"pkg.sub.Func", `"pkg.sub.Func"`},

		// Bare hyphenated identifiers get wrapped (FTS5 treats `-` as NOT).
		{"my-component", `"my-component"`},
		{"a-b", `"a-b"`},

		// Prefix wildcard preserved across the quoting.
		{"os.Stat*", `"os.Stat"*`},
		{"my-comp*", `"my-comp"*`},

		// Multiple tokens — each evaluated independently.
		{"os.Stat foo.Bar", `"os.Stat" "foo.Bar"`},
		{"plain os.Stat", `plain "os.Stat"`},

		// Already-quoted: pass through unchanged. The user knows what they're doing.
		{`"os.Stat"`, `"os.Stat"`},
		{`"login flow"`, `"login flow"`},

		// Plain identifier: no special chars, no wrapping.
		{"flushBuffers", "flushBuffers"},
		{"auth*", "auth*"},

		// Boolean operators: no `.` or `-`, untouched.
		{"foo OR bar", "foo OR bar"},
		{"NOT foo", "NOT foo"},

		// Column-prefix syntax (`:`) preserved — FTS5-legitimate.
		// The sanitizer only acts on `.` and `-`, so `kind:Function` flows through.
		{"kind:Function", "kind:Function"},

		// Edge cases: leading/trailing dot or hyphen don't trigger wrap
		// (a token like `.foo` or `-foo` isn't a normal identifier; if it's
		// a real query it almost certainly came from FTS5 syntax).
		{".foo", ".foo"},
		{"-foo", "-foo"},
		{"foo.", "foo."},
		{"foo-", "foo-"},

		// Empty query passes through.
		{"", ""},

		// Whitespace-only stays effectively empty.
		{"   ", ""},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := sanitizeFTS5Query(c.in)
			if got != c.want {
				t.Errorf("sanitizeFTS5Query(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// End-to-end: an unsanitized FTS5 search on `os.Stat` would error
// with "fts5: syntax error near '.'". After the fix, the call reaches
// the index without error and finds the seeded match.
func TestHandleSearch_DottedIdentifier_DoesNotError(t *testing.T) {
	srv, store, _ := newTestServer(t)
	srv.sessionID = "proj1"
	store.UpsertProject(db.Project{ID: "proj1", Path: "/tmp/proj1", Name: "proj1", IndexedAt: time.Now()})
	store.BulkUpsertSymbols([]db.Symbol{
		// Seed a symbol whose qualified name contains the dotted token —
		// FTS5 should match it once the query is properly quoted.
		{ID: "s1", ProjectID: "proj1", FilePath: "a.go", Name: "Stat",
			QualifiedName: "os.Stat", Kind: "Function", Language: "Go",
			StartByte: 0, EndByte: 100, StartLine: 1, EndLine: 5,
			ExtractionConfidence: 1.0},
	})

	result, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query":   "os.Stat",
		"project": "proj1",
	}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	if result.IsError {
		t.Fatalf("search returned error for dotted identifier: %v", decode(t, result))
	}
}

// `my-component`-style hyphenated tokens used to error with
// "no such column: component" because FTS5 reads `-` as NOT.
func TestHandleSearch_HyphenatedIdentifier_DoesNotError(t *testing.T) {
	srv, store, _ := newTestServer(t)
	srv.sessionID = "proj1"
	store.UpsertProject(db.Project{ID: "proj1", Path: "/tmp/proj1", Name: "proj1", IndexedAt: time.Now()})

	result, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query":   "my-component",
		"project": "proj1",
	}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	if result.IsError {
		t.Fatalf("search returned error for hyphenated identifier: %v", decode(t, result))
	}
}
