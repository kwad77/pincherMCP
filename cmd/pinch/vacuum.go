package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
)

// runVacuumCLI implements `pincher vacuum` — runs SQLite VACUUM to
// reclaim pages freed by `pincher project rm` / `pincher project
// prune-stale` so the database file on disk actually shrinks (#732).
//
// VACUUM is deliberately a separate, explicit CLI step rather than a
// flag on an MCP tool: it rewrites the whole file and holds an
// exclusive lock for the duration, which on a multi-GB DB can take a
// while. Keeping it out of the hot MCP path means a long-running
// `pincher` server never blocks queries on a vacuum the user didn't ask
// for. Pair it with prune-stale: prune drops the rows, vacuum reclaims
// the space.
func runVacuumCLI(args []string) {
	log.SetOutput(io.Discard)

	fs := flag.NewFlagSet("vacuum", flag.ExitOnError)
	dataDir := fs.String("data-dir", "", "Override data directory")
	asJSON := fs.Bool("json", false, "Emit a structured JSON receipt")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: pincher vacuum [--json] [--data-dir DIR]")
		fmt.Fprintln(os.Stderr, "  Rewrites the database file, reclaiming space freed by project removal.")
		fmt.Fprintln(os.Stderr, "  Holds an exclusive lock for the duration — run when no agent is mid-query.")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	store, _, err := openProjectStore(*dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pincher vacuum: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	before := dbFileSize(store.Path)
	if err := store.Vacuum(); err != nil {
		fmt.Fprintf(os.Stderr, "pincher vacuum: %v\n", err)
		os.Exit(1)
	}
	after := dbFileSize(store.Path)
	reclaimed := before - after
	if reclaimed < 0 {
		reclaimed = 0
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{
			"vacuumed":             true,
			"bytes_before":         before,
			"bytes_after":          after,
			"bytes_reclaimed":      reclaimed,
			"path":                 store.Path,
		})
		return
	}
	fmt.Fprintf(os.Stdout, "Vacuumed %s\n  before:    %s\n  after:     %s\n  reclaimed: %s\n",
		store.Path, humanBytes(before), humanBytes(after), humanBytes(reclaimed))
}

// dbFileSize returns the size of the database file in bytes, or 0 if it
// can't be stat'd (a freshly-opened in-memory or missing file).
func dbFileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}
