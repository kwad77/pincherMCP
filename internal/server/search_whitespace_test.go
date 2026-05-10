package server

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// #344: search must reject whitespace-only queries with a clear
// input-validation error, not leak FTS5 / SQLite internals to the
// caller. Pre-fix, " " (single space) returned:
//   "search error: SQL logic error: fts5: syntax error near \"\""

func TestHandleSearch_WhitespaceOnlyQuery_RejectedWithFriendlyError(t *testing.T) {
	srv, _, _ := newTestServer(t)
	srv.sessionID = "ws-test"
	srv.sessionRoot = "/tmp/ws-test"

	cases := []string{" ", "  ", "\t", "\n", " \t \n "}
	for _, q := range cases {
		t.Run(q, func(t *testing.T) {
			result, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
				"query": q,
			}))
			if err != nil {
				t.Fatalf("handleSearch: %v", err)
			}
			if !result.IsError {
				t.Fatalf("expected IsError=true for whitespace-only query")
			}
			body := errorBody(result)
			if !strings.Contains(body, "query is required") {
				t.Errorf("expected friendly 'query is required' error; got %q", body)
			}
			// Leaky low-level error string must NOT appear.
			if strings.Contains(body, "fts5") || strings.Contains(body, "SQL logic error") {
				t.Errorf("leaky low-level error in response: %q", body)
			}
		})
	}
}

// errorBody extracts the first text block of an errResult. Errors are
// returned as a CallToolResult with IsError=true and a single text
// Content block; tests just want the message string back.
func errorBody(r *mcp.CallToolResult) string {
	if r == nil {
		return ""
	}
	for _, c := range r.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}
