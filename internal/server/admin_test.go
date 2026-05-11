package server

import (
	"context"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #558 phase 2: doctor / rebuild_fts / self_test as MCP tools, exposed
// via the dynamic /v1/<tool> HTTP dispatcher. These tests cover the
// JSON-shape contracts; HTTP wire-up is covered by the parity test.

func TestHandleDoctor_HealthyEmptyDB(t *testing.T) {
	srv, _, _ := newTestServer(t)
	srv.version = "0.21.0-test"

	result, err := srv.handleDoctor(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleDoctor: %v", err)
	}
	body := decode(t, result)

	for _, k := range []string{
		"generated_at", "binary_version", "lookback_hours",
		"schema_version", "db_size_bytes", "wal_size_bytes",
		"projects", "extraction_failures", "slow_queries",
	} {
		if _, ok := body[k]; !ok {
			t.Errorf("doctor response missing field %q", k)
		}
	}
	if got := body["binary_version"]; got != "0.21.0-test" {
		t.Errorf("binary_version: got %v want 0.21.0-test", got)
	}
	// Empty DB → empty slices, never nil. (#328 invariant)
	for _, k := range []string{"projects", "extraction_failures", "slow_queries"} {
		if v, ok := body[k].([]any); !ok || v == nil {
			t.Errorf("%s should be [] not nil; got %T %v", k, body[k], body[k])
		}
	}
}

func TestHandleDoctor_WithProject(t *testing.T) {
	srv, store, _ := newTestServer(t)
	store.UpsertProject(db.Project{
		ID: "p1", Path: "/tmp/p1", Name: "p1",
		IndexedAt: time.Now(), BinaryVersion: "0.21.0",
		FileCount: 3, SymCount: 42, EdgeCount: 17,
	})

	result, err := srv.handleDoctor(context.Background(), makeReq(map[string]any{"top": 5}))
	if err != nil {
		t.Fatalf("handleDoctor: %v", err)
	}
	body := decode(t, result)
	projects, ok := body["projects"].([]any)
	if !ok || len(projects) != 1 {
		t.Fatalf("expected 1 project, got %v", body["projects"])
	}
	p := projects[0].(map[string]any)
	if p["name"] != "p1" || p["symbols"].(float64) != 42 {
		t.Errorf("project shape wrong: %v", p)
	}
}

func TestHandleRebuildFTS_DryRunByDefault(t *testing.T) {
	srv, _, _ := newTestServer(t)
	result, err := srv.handleRebuildFTS(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleRebuildFTS: %v", err)
	}
	body := decode(t, result)
	if body["dry_run"] != true {
		t.Errorf("default call must be dry_run=true; got %v", body["dry_run"])
	}
	if _, ok := body["would_reindex_symbols"]; !ok {
		t.Errorf("dry-run response must include would_reindex_symbols; got %v", body)
	}
}

func TestHandleRebuildFTS_ConfirmedRebuilds(t *testing.T) {
	srv, _, _ := newTestServer(t)
	result, err := srv.handleRebuildFTS(context.Background(), makeReq(map[string]any{"confirm": true}))
	if err != nil {
		t.Fatalf("handleRebuildFTS confirmed: %v", err)
	}
	body := decode(t, result)
	if body["dry_run"] != false {
		t.Errorf("confirmed call must report dry_run=false; got %v", body["dry_run"])
	}
	if _, ok := body["rebuilt_rows"]; !ok {
		t.Errorf("confirmed response must include rebuilt_rows; got %v", body)
	}
	if _, ok := body["duration_ms"]; !ok {
		t.Errorf("confirmed response must include duration_ms; got %v", body)
	}
}

func TestHandleSelfTest_HealthyInstall(t *testing.T) {
	srv, _, _ := newTestServer(t)
	result, err := srv.handleSelfTest(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleSelfTest: %v", err)
	}
	body := decode(t, result)
	if body["ok"] != true {
		t.Errorf("self_test on a clean install must report ok=true; got %v\nfull body: %v", body["ok"], body)
	}
	steps, ok := body["steps"].([]any)
	if !ok || len(steps) != 5 {
		t.Fatalf("expected 5 steps, got %v", body["steps"])
	}
	for i, raw := range steps {
		step := raw.(map[string]any)
		if step["ok"] != true {
			t.Errorf("step %d (%v) failed: %v", i, step["label"], step["error"])
		}
	}
}
