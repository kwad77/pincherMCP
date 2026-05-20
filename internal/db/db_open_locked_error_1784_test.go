package db

import (
	"errors"
	"strings"
	"testing"
)

// #1784: db.Open's migrate() step takes a write lock; when another
// pincher process holds the writer past the busy_timeout the open
// fails with a raw SQLITE_BUSY. isDBLockedErr classifies that error so
// Open can substitute a friendly, actionable message.

func TestIsDBLockedErr_Classification_1784(t *testing.T) {
	t.Parallel()
	locked := []error{
		errors.New("migrate: init schema_version: database is locked (5) (SQLITE_BUSY)"),
		errors.New("database is locked (5)"),
		errors.New("SQLITE_BUSY"),
		errors.New("database table is locked"),
	}
	for _, e := range locked {
		if !isDBLockedErr(e) {
			t.Errorf("isDBLockedErr(%q) = false; want true", e)
		}
	}
	notLocked := []error{
		nil,
		errors.New("no such table: symbols"),
		errors.New("disk I/O error"),
		errors.New("migrate: init schema_version: out of memory (14)"),
	}
	for _, e := range notLocked {
		if isDBLockedErr(e) {
			t.Errorf("isDBLockedErr(%v) = true; want false", e)
		}
	}
}

// The friendly message must lead with actionable guidance while the raw
// cause stays wrapped (errors.Unwrap-able) for debugging.
func TestOpen_LockedErrorMessage_IsActionable_1784(t *testing.T) {
	t.Parallel()
	raw := errors.New("init schema_version: database is locked (5) (SQLITE_BUSY)")
	// Mirror the exact wrap Open applies on the isDBLockedErr branch.
	wrapped := errorsLockedWrap(raw)
	msg := wrapped.Error()
	if !strings.HasPrefix(msg, "database is locked — another pincher process is writing it") {
		t.Errorf("locked-open message must lead with the friendly guidance; got %q", msg)
	}
	if !strings.Contains(msg, "retry in a few seconds") {
		t.Errorf("message must tell the user to retry; got %q", msg)
	}
	if !errors.Is(wrapped, raw) {
		t.Errorf("wrapped error must keep the raw cause inspectable via errors.Is")
	}
}

// A normal Open against a fresh data dir still succeeds — the new
// classification branch must not perturb the happy path.
func TestOpen_FreshDir_StillSucceeds_1784(t *testing.T) {
	t.Parallel()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open on a fresh dir: %v", err)
	}
	store.Close()
}
