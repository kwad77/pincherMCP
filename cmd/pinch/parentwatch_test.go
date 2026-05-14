package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// #724: parentWatchLoop reaps the process when the parent goes away.
// When the parent stays alive, it must never fire onGone/hardExit.
func TestParentWatchLoop_ParentDies_ReapsGracefullyThenHard(t *testing.T) {
	t.Parallel()
	var onGone, hardExit atomic.Bool
	// Parent is "dead" from the first poll.
	aliveFn := func(int) bool { return false }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		parentWatchLoop(ctx, 4242, 5*time.Millisecond, 10*time.Millisecond, aliveFn,
			func() { onGone.Store(true) },
			func() { hardExit.Store(true) })
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("parentWatchLoop did not return after parent death")
	}
	if !onGone.Load() {
		t.Error("onGone was not called when parent died")
	}
	if !hardExit.Load() {
		t.Error("hardExit backstop was not called")
	}
}

// When onGone cancels the context (the production wiring), the loop
// must NOT wait the full hard-exit grace — it returns as soon as ctx
// is done, but still calls hardExit as the backstop.
func TestParentWatchLoop_GracefulCancelShortCircuitsGrace(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var hardExit atomic.Bool

	const grace = 2 * time.Second
	start := time.Now()
	parentWatchLoop(ctx, 4242, 5*time.Millisecond, grace,
		func(int) bool { return false },
		cancel, // onGone cancels ctx — the real wiring
		func() { hardExit.Store(true) })
	elapsed := time.Since(start)

	if elapsed >= grace {
		t.Errorf("loop waited the full grace (%v) despite ctx cancel; took %v", grace, elapsed)
	}
	if !hardExit.Load() {
		t.Error("hardExit backstop should still fire after a graceful cancel")
	}
}

// A live parent → the loop just keeps polling and exits cleanly on
// ctx cancel, never reaping.
func TestParentWatchLoop_ParentAlive_NeverReaps(t *testing.T) {
	t.Parallel()
	var reaped atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		parentWatchLoop(ctx, 4242, 5*time.Millisecond, 10*time.Millisecond,
			func(int) bool { return true }, // parent always alive
			func() { reaped.Store(true) },
			func() { reaped.Store(true) })
		close(done)
	}()

	time.Sleep(50 * time.Millisecond) // ~10 poll ticks
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("parentWatchLoop did not return on ctx cancel")
	}
	if reaped.Load() {
		t.Error("a live parent must never trigger reaping")
	}
}
