package server

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #302: list prune_dead=true physically deletes projects whose
// on-disk path no longer exists. Defaults are unchanged — pruning
// is opt-in.

// Default behaviour: dead-on-disk projects are HIDDEN but not deleted.
func TestHandleList_DefaultDoesNotPrune(t *testing.T) {
	srv, store, _ := newTestServer(t)
	store.UpsertProject(db.Project{
		ID: "ghost", Path: filepath.Join(t.TempDir(), "no-such-dir"),
		Name: "ghost", IndexedAt: time.Now(),
	})

	result, err := srv.handleList(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	body := decode(t, result)
	if body["count"].(float64) != 0 {
		t.Errorf("count = %v, want 0 (ghost project hidden)", body["count"])
	}
	if _, ok := body["pruned"]; ok {
		t.Errorf("default call must not include `pruned` field; got %v", body["pruned"])
	}
	// Confirm the row is still in the DB.
	projects, _ := store.ListProjects()
	if len(projects) != 1 {
		t.Errorf("expected ghost project still in DB; got %d projects", len(projects))
	}
}

// prune_dead=true physically removes the missing-path projects and
// returns their ids in `pruned`.
func TestHandleList_PruneDeadDeletesMissingProjects(t *testing.T) {
	srv, store, _ := newTestServer(t)
	deadDir := filepath.Join(t.TempDir(), "dead-dir-doesnt-exist")
	store.UpsertProject(db.Project{
		ID: "ghost", Path: deadDir, Name: "ghost", IndexedAt: time.Now(),
	})
	aliveDir := t.TempDir()
	store.UpsertProject(db.Project{
		ID: "alive", Path: aliveDir, Name: "alive", IndexedAt: time.Now(),
	})

	result, err := srv.handleList(context.Background(), makeReq(map[string]any{
		"prune_dead": true,
	}))
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	body := decode(t, result)
	pruned, _ := body["pruned"].([]any)
	if len(pruned) != 1 || pruned[0] != "ghost" {
		t.Errorf("pruned = %v, want [ghost]", pruned)
	}
	if body["count"].(float64) != 1 {
		t.Errorf("count = %v, want 1 (alive only)", body["count"])
	}

	// Confirm the ghost row is GONE from the DB.
	projects, _ := store.ListProjects()
	if len(projects) != 1 {
		t.Errorf("expected 1 project after prune; got %d", len(projects))
	}
	for _, p := range projects {
		if p.ID == "ghost" {
			t.Errorf("ghost project still in DB after prune_dead=true: %v", p)
		}
	}
}

// prune_dead=true with no dead projects returns an empty `pruned`
// array (not nil) — distinguishes "I tried to prune and there was
// nothing" from "I never tried to prune".
func TestHandleList_PruneDeadEmptyArrayWhenNothingDead(t *testing.T) {
	srv, store, _ := newTestServer(t)
	aliveDir := t.TempDir()
	store.UpsertProject(db.Project{
		ID: "alive", Path: aliveDir, Name: "alive", IndexedAt: time.Now(),
	})

	result, err := srv.handleList(context.Background(), makeReq(map[string]any{
		"prune_dead": true,
	}))
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	body := decode(t, result)
	pruned, ok := body["pruned"].([]any)
	if !ok {
		t.Fatalf("pruned field missing despite prune_dead=true; body: %v", body)
	}
	if len(pruned) != 0 {
		t.Errorf("pruned = %v, want empty array", pruned)
	}
}

// include_dead=true short-circuits the prune — caller wanted to see
// dead rows, not delete them.
func TestHandleList_IncludeDeadDoesNotPruneEvenIfFlagSet(t *testing.T) {
	srv, store, _ := newTestServer(t)
	deadDir := filepath.Join(t.TempDir(), "dead-dir-doesnt-exist")
	store.UpsertProject(db.Project{
		ID: "ghost", Path: deadDir, Name: "ghost", IndexedAt: time.Now(),
	})

	// include_dead=true means "show me dead rows" — DO NOT delete them
	// even if prune_dead=true is also set. The prune branch only fires
	// inside the "drop dead" filter, and include_dead=true skips that
	// filter entirely.
	result, err := srv.handleList(context.Background(), makeReq(map[string]any{
		"include_dead": true,
		"prune_dead":   true,
	}))
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	body := decode(t, result)
	if body["count"].(float64) != 1 {
		t.Errorf("count = %v, want 1 (ghost surfaced via include_dead)", body["count"])
	}
	// Ghost still in DB.
	projects, _ := store.ListProjects()
	if len(projects) != 1 {
		t.Errorf("ghost should not have been pruned when include_dead=true; got %d projects", len(projects))
	}
}
