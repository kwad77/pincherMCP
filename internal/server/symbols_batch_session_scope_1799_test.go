package server

import (
	"context"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #1799: the symbols batch tool resolved IDs unscoped — a symbol ID is
// path+QN+kind, so an indexed mirror of the session repo carries
// identical IDs and an unscoped lookup could return the wrong project's
// row. symbols now session-scopes by default, like symbol / context /
// trace / neighborhood (#1232 / #1408).

func seed1799TwoProjects(t *testing.T, srv *Server, store *db.Store) (sessionID, mirrorID string) {
	t.Helper()
	sessionID, mirrorID = "p-session-1799", "p-mirror-1799"
	for _, pid := range []string{sessionID, mirrorID} {
		store.UpsertProject(db.Project{
			ID: pid, Path: t.TempDir(), Name: pid, IndexedAt: time.Now(),
			FileCount: 1, SymCount: 1, EdgeCount: 1,
		})
	}
	srv.sessionID = sessionID
	// Same ID in both projects, distinguishable by signature.
	collidingID := "pkg/x.go::pkg.Foo#Function"
	mustUpsertSymbols(t, store, []db.Symbol{
		{
			ID: collidingID, ProjectID: sessionID, FilePath: "pkg/x.go",
			Name: "Foo", QualifiedName: "pkg.Foo", Kind: "Function",
			Language: "Go", Signature: "func Foo() // SESSION", ExtractionConfidence: 1.0,
		},
		{
			ID: collidingID, ProjectID: mirrorID, FilePath: "pkg/x.go",
			Name: "Foo", QualifiedName: "pkg.Foo", Kind: "Function",
			Language: "Go", Signature: "func Foo() // MIRROR", ExtractionConfidence: 1.0,
		},
		{
			ID: "pkg/y.go::pkg.MirrorOnly#Function", ProjectID: mirrorID, FilePath: "pkg/y.go",
			Name: "MirrorOnly", QualifiedName: "pkg.MirrorOnly", Kind: "Function",
			Language: "Go", Signature: "func MirrorOnly()", ExtractionConfidence: 1.0,
		},
	})
	return sessionID, mirrorID
}

func TestHandleSymbols_SessionScopedByDefault_1799(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	seed1799TwoProjects(t, srv, store)

	// No `project` arg — must resolve the colliding ID from the SESSION
	// project, not the mirror.
	res, err := srv.handleSymbols(context.Background(), makeReq(map[string]any{
		"ids":    []any{"pkg/x.go::pkg.Foo#Function"},
		"fields": "id,signature",
	}))
	if err != nil {
		t.Fatalf("handleSymbols: %v", err)
	}
	syms, _ := decode(t, res)["symbols"].([]any)
	if len(syms) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(syms))
	}
	got, _ := syms[0].(map[string]any)["signature"].(string)
	if got != "func Foo() // SESSION" {
		t.Errorf("#1799: batch resolved the colliding ID from the wrong project — signature = %q, want the SESSION row", got)
	}
}

func TestHandleSymbols_MirrorOnlyID_NotFoundWithoutCrossProject_1799(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	seed1799TwoProjects(t, srv, store)

	// An ID present only in the mirror must surface as not_found —
	// not silently served from the mirror.
	res, err := srv.handleSymbols(context.Background(), makeReq(map[string]any{
		"ids": []any{"pkg/y.go::pkg.MirrorOnly#Function"},
	}))
	if err != nil {
		t.Fatalf("handleSymbols: %v", err)
	}
	body := decode(t, res)
	nf, _ := body["not_found_ids"].([]any)
	if len(nf) != 1 {
		t.Errorf("#1799: a mirror-only ID must be not_found under default session scope; not_found_ids=%v", body["not_found_ids"])
	}
}

func TestHandleSymbols_CrossProjectOptIn_ResolvesMirrorID_1799(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	seed1799TwoProjects(t, srv, store)

	// cross_project=true falls back to the unscoped lookup for the
	// session-missed ID.
	res, err := srv.handleSymbols(context.Background(), makeReq(map[string]any{
		"ids":           []any{"pkg/y.go::pkg.MirrorOnly#Function"},
		"cross_project": true,
		"fields":        "id,signature",
	}))
	if err != nil {
		t.Fatalf("handleSymbols: %v", err)
	}
	syms, _ := decode(t, res)["symbols"].([]any)
	if len(syms) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(syms))
	}
	if errStr, _ := syms[0].(map[string]any)["error"].(string); errStr != "" {
		t.Errorf("cross_project=true must resolve the mirror-only ID; got error %q", errStr)
	}
}
