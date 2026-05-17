package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// #1091 v0.67: snippet_lines knob on search.

// Helper: seed one Function symbol so the search returns deterministic
// content for the snippet assertions.
func seedSearchTestSymbol(t *testing.T, store *db.Store) {
	t.Helper()
	pid := "p"
	if err := store.UpsertProject(db.Project{
		ID: pid, Path: t.TempDir(), Name: "p", IndexedAt: time.Now(),
	}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	if err := store.BulkUpsertSymbols([]db.Symbol{{
		ID:                   "p::handleSearch#Function",
		ProjectID:            pid,
		FilePath:             "main.go",
		Language:             "Go",
		Kind:                 "Function",
		Name:                 "handleSearch",
		QualifiedName:        "handleSearch",
		Signature:            "func handleSearch()",
		ExtractionConfidence: 1.0,
	}}); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}
}

// Positive: snippet_lines=0 returns an empty snippet field — caller
// got the result row without paying the per-hit byte-offset read.
func TestSearch_SnippetLinesZero_ReturnsEmptySnippet(t *testing.T) {
	srv, store, _ := newTestServer(t)
	seedSearchTestSymbol(t, store)

	res, err := srv.handleSearch(context.Background(), &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Name: "search", Arguments: []byte(
			`{"query":"handleSearch","project":"p","snippet_lines":0}`)},
	})
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	body := textOf(t, res)
	// snippet field present in the schema but value should be empty.
	if !strings.Contains(body, `"snippet":""`) {
		t.Errorf("expected empty snippet on snippet_lines=0; got body: %s", body)
	}
}

// Positive: snippet_lines default (omitted) keeps the historical
// shape — back-compat for callers that depend on snippet presence.
func TestSearch_SnippetLinesDefault_KeepsHistoricalShape(t *testing.T) {
	srv, store, _ := newTestServer(t)
	seedSearchTestSymbol(t, store)

	res, err := srv.handleSearch(context.Background(), &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Name: "search", Arguments: []byte(
			`{"query":"handleSearch","project":"p"}`)},
	})
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	body := textOf(t, res)
	if !strings.Contains(body, `"snippet"`) {
		t.Errorf("snippet field missing on default call; got body: %s", body)
	}
}

// Negative: out-of-range snippet_lines values are clamped with a
// warning rather than erroring.
func TestSearch_SnippetLinesClampWarnings(t *testing.T) {
	srv, store, _ := newTestServer(t)
	seedSearchTestSymbol(t, store)

	for _, tc := range []struct {
		name     string
		args     string
		wantHint string
	}{
		{"negative", `{"query":"handleSearch","project":"p","snippet_lines":-3}`, "clamped to 0"},
		{"too_large", `{"query":"handleSearch","project":"p","snippet_lines":100}`, "clamped to 20"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := srv.handleSearch(context.Background(), &mcp.CallToolRequest{
				Params: &mcp.CallToolParamsRaw{Name: "search", Arguments: []byte(tc.args)},
			})
			if err != nil {
				t.Fatalf("handleSearch: %v", err)
			}
			body := textOf(t, res)
			if !strings.Contains(body, tc.wantHint) {
				t.Errorf("expected clamp warning %q; got body: %s", tc.wantHint, body)
			}
		})
	}
}
