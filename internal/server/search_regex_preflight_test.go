package server

import (
	"context"
	"strings"
	"testing"
)

// #509: regex meta-patterns (.*  .+ .?) in search query must hit the
// pre-flight rather than leak FTS5 syntax errors. Narrow on purpose —
// dotted identifiers (db.Open) and prefix wildcards (auth*) must
// continue to work.
func TestHandleSearch_RegexInQuery_RejectedWithFriendlyError(t *testing.T) {
	srv, _, _ := newTestServer(t)
	srv.sessionID = "regex-test"

	cases := []string{
		"handle.*Changes", // .*
		"foo.+bar",        // .+
		"name.?",          // .?
	}
	for _, q := range cases {
		t.Run(q, func(t *testing.T) {
			req := makeReq(map[string]any{"query": q})
			req.Params.Name = "search"
			result, err := srv.handleSearch(context.Background(), req)
			if err != nil {
				t.Fatalf("handleSearch: %v", err)
			}
			if !result.IsError {
				t.Fatalf("expected IsError=true on regex meta-pattern; got success")
			}
			body := errorBody(result)
			if !strings.Contains(body, "regex sequence") {
				t.Errorf("error should mention 'regex sequence'; got %q", body)
			}
			if !strings.Contains(body, "query") || !strings.Contains(body, "=~") {
				t.Errorf("error should redirect to `query` tool with =~; got %q", body)
			}
			// Raw FTS5/SQL leaks must NOT appear.
			if strings.Contains(body, "fts5") || strings.Contains(body, "SQL logic error") {
				t.Errorf("raw SQL error must not leak; got %q", body)
			}
		})
	}
}

// Dotted identifiers (db.Open) are common search inputs and must NOT
// trigger the regex pre-flight — they're rescued by the existing
// sanitizeFTS5Query (#424).
func TestHandleSearch_DottedIdentifier_NoRegexFalsePositive(t *testing.T) {
	srv, _, _ := newTestServer(t)
	srv.sessionID = "dotted-test"

	cases := []string{
		"db.Open",
		"os.Stat",
		"a.b.c",
	}
	for _, q := range cases {
		t.Run(q, func(t *testing.T) {
			req := makeReq(map[string]any{"query": q})
			req.Params.Name = "search"
			result, err := srv.handleSearch(context.Background(), req)
			if err != nil {
				t.Fatalf("handleSearch: %v", err)
			}
			body := errorBody(result)
			if strings.Contains(body, "regex sequence") {
				t.Errorf("dotted identifier %q must not trigger regex pre-flight; got %q", q, body)
			}
		})
	}
}

// Prefix wildcards (auth*) are valid FTS5 syntax — must NOT be flagged.
func TestHandleSearch_PrefixWildcard_NoRegexFalsePositive(t *testing.T) {
	srv, _, _ := newTestServer(t)
	srv.sessionID = "wildcard-test"

	req := makeReq(map[string]any{"query": "auth*"})
	req.Params.Name = "search"
	result, err := srv.handleSearch(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	body := errorBody(result)
	if strings.Contains(body, "regex sequence") {
		t.Errorf("prefix wildcard 'auth*' must not trigger regex pre-flight; got %q", body)
	}
}

// Regex chars INSIDE a quoted phrase are literal — pre-flight skips
// them.
func TestHandleSearch_RegexInsideQuotedPhrase_AllowedThrough(t *testing.T) {
	srv, _, _ := newTestServer(t)
	srv.sessionID = "regex-q-test"

	req := makeReq(map[string]any{"query": `"handle.*Changes"`})
	req.Params.Name = "search"
	result, err := srv.handleSearch(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	body := errorBody(result)
	if strings.Contains(body, "regex sequence") {
		t.Errorf("regex inside quoted phrase must not trigger pre-flight; got %q", body)
	}
}

// Pure unit test for the helper.
func TestFirstFTS5IncompatibleRegexChar_Coverage(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"plain", ""},
		{"db.Open", ""},  // single dot — sanitizer handles
		{"auth*", ""},    // prefix wildcard — FTS5 supports
		{"a.b.c", ""},    // dotted ident
		{"handle.*X", ".*"},
		{"foo.+bar", ".+"},
		{"baz.?qux", ".?"},
		{`"a.*b"`, ""}, // inside quote
	}
	for _, tc := range cases {
		got := firstFTS5IncompatibleRegexChar(tc.in)
		if got != tc.want {
			t.Errorf("firstFTS5IncompatibleRegexChar(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}
