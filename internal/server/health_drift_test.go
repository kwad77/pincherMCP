package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #304: health surfaces index_drift when the project's stored
// binary_version doesn't match the running server's version. Pre-fix
// (binary_version absent) drift was indistinguishable from up-to-date
// — agents trusted stale CALLS edges and got wrong "0 callers" results.

func TestHandleHealth_VersionMatch_NoDrift(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.version = "0.9.0"
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{
		ID: "p1", Path: "/tmp/p1", Name: "p1",
		IndexedAt: time.Now(), BinaryVersion: "0.9.0",
	})

	result, err := srv.handleHealth(context.Background(), makeReq(map[string]any{"project": "p1"}))
	if err != nil {
		t.Fatalf("handleHealth: %v", err)
	}
	body := decode(t, result)
	if drift, ok := body["index_drift"]; ok {
		t.Errorf("matching versions should NOT surface index_drift, got %v", drift)
	}
}

func TestHandleHealth_VersionMismatch_SurfacesDrift(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.version = "0.9.0"
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{
		ID: "p1", Path: "/tmp/p1", Name: "p1",
		IndexedAt: time.Now(), BinaryVersion: "0.7.0",
	})

	result, err := srv.handleHealth(context.Background(), makeReq(map[string]any{"project": "p1"}))
	if err != nil {
		t.Fatalf("handleHealth: %v", err)
	}
	body := decode(t, result)
	drift, _ := body["index_drift"].(bool)
	if !drift {
		t.Errorf("0.9.0 server vs 0.7.0 indexed should surface index_drift=true; got %v", body["index_drift"])
	}
	msg, _ := body["index_drift_message"].(string)
	if !strings.Contains(msg, "0.7.0") || !strings.Contains(msg, "0.9.0") {
		t.Errorf("drift message should name both versions; got %q", msg)
	}
	// Drift step should be in next_steps.
	meta, _ := body["_meta"].(map[string]any)
	steps, _ := meta["next_steps"].([]any)
	hasReindex := false
	for _, s := range steps {
		step, _ := s.(map[string]any)
		if step["tool"] == "index" {
			args, _ := step["args"].(string)
			if strings.Contains(args, `"force":true`) {
				hasReindex = true
			}
		}
	}
	if !hasReindex {
		t.Errorf("expected an index force=true next_step on drift; got %v", steps)
	}
}

// Empty stored binary_version (row pre-dates v18 migration) is
// rendered as "indexed before v18 migration ... unknown".
func TestHandleHealth_EmptyBinaryVersion_RendersAsUnknown(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.version = "0.9.0"
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{
		ID: "p1", Path: "/tmp/p1", Name: "p1",
		IndexedAt: time.Now(), BinaryVersion: "", // pre-v18 row
	})

	result, err := srv.handleHealth(context.Background(), makeReq(map[string]any{"project": "p1"}))
	if err != nil {
		t.Fatalf("handleHealth: %v", err)
	}
	body := decode(t, result)
	drift, _ := body["index_drift"].(bool)
	if !drift {
		t.Error("empty stored version vs running version should still surface drift")
	}
	msg, _ := body["index_drift_message"].(string)
	if !strings.Contains(strings.ToLower(msg), "unknown") &&
		!strings.Contains(msg, "v18") {
		t.Errorf("empty-version drift message should mention 'unknown' or 'v18'; got %q", msg)
	}
}

// project field includes binary_version verbatim.
func TestHandleHealth_BinaryVersionInProject(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.version = "0.9.0"
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{
		ID: "p1", Path: "/tmp/p1", Name: "p1",
		IndexedAt: time.Now(), BinaryVersion: "0.9.0",
	})

	result, err := srv.handleHealth(context.Background(), makeReq(map[string]any{"project": "p1"}))
	if err != nil {
		t.Fatalf("handleHealth: %v", err)
	}
	body := decode(t, result)
	proj, _ := body["project"].(map[string]any)
	if got, _ := proj["binary_version"].(string); got != "0.9.0" {
		t.Errorf("project.binary_version = %q, want 0.9.0", got)
	}
}

// TestHandleHealth_ProjectShape_AlignedWithArchitecture (#1410) — the
// health.project field has the same field names as architecture.project
// for everything they both surface. id, schema_version_at_index, and
// last_indexed_branch were silently missing pre-fix because health
// hand-rolled the map literal and #1388's rename never reached this
// surface.
func TestHandleHealth_ProjectShape_AlignedWithArchitecture(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p1"
	schemaV := 32
	store.UpsertProject(db.Project{
		ID: "p1", Path: "/tmp/p1", Name: "demo",
		IndexedAt:            time.Now(),
		BinaryVersion:        "0.71.0",
		SchemaVersionAtIndex: &schemaV,
		CurrentBranch:        "master",
		FileCount:            10, SymCount: 100, EdgeCount: 50,
	})

	result, err := srv.handleHealth(context.Background(), makeReq(map[string]any{"project": "p1"}))
	if err != nil {
		t.Fatalf("handleHealth: %v", err)
	}
	body := decode(t, result)
	proj, _ := body["project"].(map[string]any)
	if proj == nil {
		t.Fatalf("health.project missing: %v", body)
	}

	for _, want := range []string{"id", "name", "path", "files", "symbols", "edges",
		"indexed_at", "staleness_human", "staleness_seconds",
		"binary_version", "schema_version_at_index", "last_indexed_branch"} {
		if _, ok := proj[want]; !ok {
			t.Errorf("health.project missing %q (post-#1410); got fields: %v", want, projFieldNames(proj))
		}
	}

	if got, _ := proj["id"].(string); got != "p1" {
		t.Errorf("project.id = %q, want p1", got)
	}
	if got, _ := proj["last_indexed_branch"].(string); got != "master" {
		t.Errorf("project.last_indexed_branch = %q, want master (#1388 rename must reach health too)", got)
	}
}

// TestHandleHealth_ProjectShape_EmptyBranchOmits — last_indexed_branch
// is omitempty per the Project struct's JSON tag; pre-v32 projects
// with empty CurrentBranch should not surface the field at all.
func TestHandleHealth_ProjectShape_EmptyBranchOmits(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{
		ID: "p1", Path: "/tmp/p1", Name: "demo",
		IndexedAt:     time.Now(),
		BinaryVersion: "0.71.0",
		// CurrentBranch deliberately empty — pre-v32 project shape.
	})

	result, err := srv.handleHealth(context.Background(), makeReq(map[string]any{"project": "p1"}))
	if err != nil {
		t.Fatalf("handleHealth: %v", err)
	}
	body := decode(t, result)
	proj, _ := body["project"].(map[string]any)
	if _, present := proj["last_indexed_branch"]; present {
		t.Errorf("empty CurrentBranch must omit last_indexed_branch (omitempty); got %v", proj["last_indexed_branch"])
	}
}

func projFieldNames(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
