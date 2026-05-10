package server

import (
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

func TestNormalizeVersion(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		// Canonical forms — leading 'v' is added if missing, dropped from output? No: semver.IsValid requires the 'v' prefix.
		{"0.10.0", "v0.10.0"},
		{"v0.10.0", "v0.10.0"},
		// Dirty/git-describe noise stripped.
		{"0.10.0-dirty", "v0.10.0"},
		{"v0.10.0-3-gabcdef", "v0.10.0"},
		{"v0.10.0-3-gabcdef-dirty", "v0.10.0"},
		// Unparseable / dev sentinels return "" so callers skip the comparison.
		{"", ""},
		{"dev", ""},
		{"not-a-version", ""},
		{"v0.10.0.0", ""}, // semver doesn't allow four parts
	}
	for _, c := range cases {
		got := normalizeVersion(c.in)
		if got != c.want {
			t.Errorf("normalizeVersion(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// driftFor + checkDriftForWrite + attachDriftWarning all key on the
// project's BinaryVersion vs the server's version. Tests use
// newTestServer (which already wires a real *db.Store) and seed a
// project row directly so we control the BinaryVersion field.

func seedProject(t *testing.T, s *Server, name, binaryVersion string) string {
	t.Helper()
	pid := db.ProjectIDFromPath(t.TempDir())
	if err := s.store.UpsertProject(db.Project{
		ID:            pid,
		Path:          t.TempDir(),
		Name:          name,
		BinaryVersion: binaryVersion,
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return pid
}

func TestDriftFor_SelfNewer_NoDrift(t *testing.T) {
	srv, _, _ := newTestServer(t)
	srv.version = "0.11.0"
	pid := seedProject(t, srv, "p", "0.10.0")

	msg, action := srv.driftFor(pid)
	if action != driftNone || msg != "" {
		t.Errorf("self newer than project: got action=%v msg=%q, want driftNone", action, msg)
	}
}

func TestDriftFor_Equal_NoDrift(t *testing.T) {
	srv, _, _ := newTestServer(t)
	srv.version = "0.10.0"
	pid := seedProject(t, srv, "p", "0.10.0")

	_, action := srv.driftFor(pid)
	if action != driftNone {
		t.Errorf("equal versions: got action=%v, want driftNone", action)
	}
}

func TestDriftFor_SelfOlder_Warns(t *testing.T) {
	srv, _, _ := newTestServer(t)
	srv.version = "0.9.0"
	pid := seedProject(t, srv, "p", "0.10.0")

	msg, action := srv.driftFor(pid)
	if action != driftActionWarn {
		t.Errorf("got action=%v, want driftActionWarn", action)
	}
	if !strings.Contains(msg, "0.10.0") || !strings.Contains(msg, "0.9.0") {
		t.Errorf("warning should mention both versions; got: %s", msg)
	}
	if !strings.Contains(msg, "writes are blocked") {
		t.Errorf("warning should explain write-block behavior; got: %s", msg)
	}
}

func TestDriftFor_DevOnEitherSide_Skips(t *testing.T) {
	srv, _, _ := newTestServer(t)

	// Self is dev, project is real
	srv.version = "dev"
	pid := seedProject(t, srv, "p", "0.10.0")
	if _, action := srv.driftFor(pid); action != driftNone {
		t.Errorf("dev self: got action=%v, want driftNone (can't compare dev)", action)
	}

	// Self is real, project is unstamped
	srv.version = "0.10.0"
	pid2 := seedProject(t, srv, "q", "")
	if _, action := srv.driftFor(pid2); action != driftNone {
		t.Errorf("unstamped project: got action=%v, want driftNone", action)
	}
}

func TestDriftFor_NormalizesGitDescribeAndDirty(t *testing.T) {
	srv, _, _ := newTestServer(t)
	// Self is a dirty build of v0.10.0; project was indexed by clean v0.10.0.
	// Without normalization, semver pre-release ordering would put dirty
	// BELOW clean and falsely flag drift. Normalization strips the suffix
	// and they compare equal.
	srv.version = "0.10.0-dirty"
	pid := seedProject(t, srv, "p", "0.10.0")

	if _, action := srv.driftFor(pid); action != driftNone {
		t.Errorf("dirty self vs clean release: got action=%v, want driftNone (normalize should equate)", action)
	}
}

func TestCheckDriftForWrite_RefusesOnDrift(t *testing.T) {
	srv, _, _ := newTestServer(t)
	srv.version = "0.9.0"
	pid := seedProject(t, srv, "p", "0.10.0")

	err := srv.checkDriftForWrite(pid)
	if err == nil {
		t.Fatal("expected refusal error for older self on newer project")
	}
	if !strings.Contains(err.Error(), "writes are blocked") {
		t.Errorf("error should explain why writes are blocked; got: %v", err)
	}
}

func TestCheckDriftForWrite_NoDrift_ReturnsNil(t *testing.T) {
	srv, _, _ := newTestServer(t)
	srv.version = "0.10.0"
	pid := seedProject(t, srv, "p", "0.10.0")

	if err := srv.checkDriftForWrite(pid); err != nil {
		t.Errorf("equal versions should not refuse: %v", err)
	}
}

func TestAttachDriftWarning_AttachesToMeta(t *testing.T) {
	srv, _, _ := newTestServer(t)
	srv.version = "0.9.0"
	pid := seedProject(t, srv, "p", "0.10.0")

	data := map[string]any{}
	srv.attachDriftWarning(data, pid)

	meta, ok := data["_meta"].(map[string]any)
	if !ok {
		t.Fatal("_meta map not allocated")
	}
	w, ok := meta["binary_version_warning"].(string)
	if !ok || w == "" {
		t.Fatal("binary_version_warning not set")
	}
}

func TestAttachDriftWarning_NoOpOnMatch(t *testing.T) {
	srv, _, _ := newTestServer(t)
	srv.version = "0.10.0"
	pid := seedProject(t, srv, "p", "0.10.0")

	data := map[string]any{"some": "key"}
	srv.attachDriftWarning(data, pid)

	if _, ok := data["_meta"]; ok {
		t.Error("_meta should not be allocated when no drift")
	}
}

func TestAttachDriftWarning_PreservesExistingMeta(t *testing.T) {
	srv, _, _ := newTestServer(t)
	srv.version = "0.9.0"
	pid := seedProject(t, srv, "p", "0.10.0")

	data := map[string]any{
		"_meta": map[string]any{"existing": "value"},
	}
	srv.attachDriftWarning(data, pid)

	meta := data["_meta"].(map[string]any)
	if meta["existing"] != "value" {
		t.Error("existing _meta entry was clobbered")
	}
	if meta["binary_version_warning"] == nil {
		t.Error("warning was not added")
	}
}
