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
