package main

import (
	"context"
	"log/slog"
	"os"
	"time"
)

// parentWatchInterval is how often a long-lived pincher process polls
// its parent's liveness. 30s keeps orphan-reaping latency low while the
// poll itself (one PID liveness check) is effectively free.
const parentWatchInterval = 30 * time.Second

// parentWatchHardExitGrace is how long the watcher waits after calling
// onGone (graceful cancel) before forcing a hard exit. If the graceful
// path tears the process down first, this never fires; it only matters
// when something downstream doesn't honour context cancellation.
const parentWatchHardExitGrace = 5 * time.Second

// watchParent reaps this process when its parent dies abnormally.
//
// #724: a stdio MCP child (or the supervisor) detects a *graceful*
// parent disconnect via stdin EOF. But a parent killed with SIGKILL,
// crashed, or lost to power-off may not close our stdin pipe — leaving
// us an orphan whose Watch() loop keeps running against the shared DB
// and stomping project metadata (binary_version, schema_version_at_index)
// for every other pincher process. Three such orphans were observed on
// one machine, one of them two days old.
//
// The parent PID is captured once at startup. On Unix a re-parent to
// init moves Getppid() to 1 while the *original* PID is now dead; on
// Windows Getppid() stays stale but the captured PID itself becomes
// dead. Polling pidIsAlive on the captured PID catches both.
//
// onGone is invoked once when the parent is detected gone — pass the
// process's context cancel so shutdown runs cleanly (session-stat flush,
// Watch() teardown). The watcher then waits parentWatchHardExitGrace and
// force-exits as a backstop. A no-op when there is no meaningful parent
// (ppid <= 1) — that includes intentionally-detached servers, which
// must NOT be reaped.
func watchParent(ctx context.Context, onGone func()) {
	ppid := os.Getppid()
	if ppid <= 1 {
		// No meaningful parent (already orphaned, or detached on Unix
		// via setsid). Detached HTTP servers rely on this no-op.
		return
	}
	go parentWatchLoop(ctx, ppid, parentWatchInterval, parentWatchHardExitGrace,
		pidIsAlive, onGone, func() {
			slog.Warn("pincher.parent_gone.hard_exit", "ppid", ppid)
			os.Exit(0)
		})
}

// parentWatchLoop is the testable core of watchParent: poll aliveFn on
// ppid every interval; on the first not-alive result call onGone, wait
// up to hardExitGrace, then call hardExit. Returns early (without
// reaping) when ctx is cancelled — the normal shutdown path. Split out
// so tests can inject a fake liveness fn, short timings, and a
// non-os.Exit hardExit.
func parentWatchLoop(
	ctx context.Context,
	ppid int,
	interval time.Duration,
	hardExitGrace time.Duration,
	aliveFn func(int) bool,
	onGone func(),
	hardExit func(),
) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if aliveFn(ppid) {
				continue
			}
			slog.Warn("pincher.parent_gone",
				"ppid", ppid,
				"action", "graceful_shutdown_then_exit")
			if onGone != nil {
				onGone()
			}
			select {
			case <-ctx.Done():
				// Graceful path tore us down inside the grace window —
				// no need for the hard backstop.
			case <-time.After(hardExitGrace):
			}
			if hardExit != nil {
				hardExit()
			}
			return
		}
	}
}
