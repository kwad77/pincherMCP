// tracelatencybench measures trace-query latency with and without the
// closure-table fast-path (#1162). The gating measurement for the
// closure-tables-default-on decision: confirm the trace-latency
// improvement holds at scale before flipping the default.
//
// Approach:
//   - Open the pincher DB at -db (default per-OS pincher path).
//   - Pick -n random Function/Method symbols from -project that have
//     at least one inbound or outbound edge (degenerate roots skew
//     the measurement toward zero work).
//   - For each, time:
//       * CTE-only path:     TraceViaCTEScoped
//       * Closure-fast-path: TraceViaClosure (after building closure)
//   - Report p50 / p95 / max / mean latency in microseconds for both
//     directions ("outbound" and "inbound") plus the ratio of CTE to
//     closure, which is the headline number for #1162.
//
// Usage:
//
//	tracelatencybench [-db <path>] [-project <id>] [-n 200] [-depth 3]
//	  [-direction outbound|inbound|both] [-md]
//
// Defaults: db=$HOME/.pincher/pincher.db, project=largest by symbol
// count, n=200, depth=3, direction=both.
//
// The -md flag prints one Markdown table row suitable for pasting
// into the #1162 acceptance comment.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

// run is main's testable body — never calls os.Exit.
func run(args []string, errOut io.Writer) int {
	fs := flag.NewFlagSet("tracelatencybench", flag.ContinueOnError)
	fs.SetOutput(errOut)
	dbPath := fs.String("db", defaultDBDir(), "Path to the pincher data dir containing pincher.db (default: ~/.pincher; Windows users with the supervised install pass -db ~/AppData/Roaming/pincherMCP)")
	projectID := fs.String("project", "", "Project ID to measure (default: largest by symbol count)")
	n := fs.Int("n", 200, "Number of sample symbols to time")
	depth := fs.Int("depth", 3, "Max trace depth")
	direction := fs.String("direction", "both", "Trace direction: outbound | inbound | both")
	mdRow := fs.Bool("md", false, "Print one Markdown table row instead of multi-line summary")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *direction != "outbound" && *direction != "inbound" && *direction != "both" {
		fmt.Fprintf(errOut, "tracelatencybench: invalid direction %q (want outbound|inbound|both)\n", *direction)
		return 2
	}

	store, err := db.Open(*dbPath)
	if err != nil {
		fmt.Fprintf(errOut, "tracelatencybench: open %s: %v\n", *dbPath, err)
		return 1
	}
	defer store.Close()

	pid := *projectID
	if pid == "" {
		largest, err := largestProjectID(store)
		if err != nil {
			fmt.Fprintf(errOut, "tracelatencybench: pick largest project: %v\n", err)
			return 1
		}
		pid = largest
	}

	// Sample symbols with at least one edge — degenerate roots
	// (orphans) give every trace 0ms which floors the measurement.
	sample, err := sampleSymbolsWithEdges(store, pid, *n)
	if err != nil {
		fmt.Fprintf(errOut, "tracelatencybench: sample symbols: %v\n", err)
		return 1
	}
	if len(sample) == 0 {
		fmt.Fprintf(errOut, "tracelatencybench: no symbols with edges in project %q\n", pid)
		return 1
	}

	// Build closure if not present.
	if err := store.BuildClosure(context.Background(), pid, *depth); err != nil {
		fmt.Fprintf(errOut, "tracelatencybench: build closure: %v\n", err)
		return 1
	}

	cteLats := []time.Duration{}
	closLats := []time.Duration{}
	for _, sid := range sample {
		// Warmup pair: skip first iteration timing for each path so
		// SQLite page cache effects don't favor the second-run path.
		_, _ = store.TraceViaCTEScoped(pid, sid, *direction, nil, *depth)
		_, _ = store.TraceViaClosure(pid, sid, *direction, *depth)

		t0 := time.Now()
		_, _ = store.TraceViaCTEScoped(pid, sid, *direction, nil, *depth)
		cteLats = append(cteLats, time.Since(t0))

		t1 := time.Now()
		_, _ = store.TraceViaClosure(pid, sid, *direction, *depth)
		closLats = append(closLats, time.Since(t1))
	}

	ctP50, ctP95, ctMax, ctMean := stats(cteLats)
	clP50, clP95, clMax, clMean := stats(closLats)
	ratio := 0.0
	if clP50 > 0 {
		ratio = float64(ctP50) / float64(clP50)
	}

	if *mdRow {
		fmt.Fprintf(os.Stdout,
			"| %s | %d | %d | %s | %s | %s | %s | %s | %s | %.1f× |\n",
			pid, *n, *depth,
			fmtDur(ctP50), fmtDur(ctP95), fmtDur(ctMean),
			fmtDur(clP50), fmtDur(clP95), fmtDur(clMean),
			ratio)
		return 0
	}

	fmt.Fprintf(os.Stdout, "tracelatencybench — project=%q n=%d depth=%d direction=%s\n", pid, *n, *depth, *direction)
	fmt.Fprintf(os.Stdout, "  CTE path     p50=%s  p95=%s  max=%s  mean=%s\n", fmtDur(ctP50), fmtDur(ctP95), fmtDur(ctMax), fmtDur(ctMean))
	fmt.Fprintf(os.Stdout, "  Closure path p50=%s  p95=%s  max=%s  mean=%s\n", fmtDur(clP50), fmtDur(clP95), fmtDur(clMax), fmtDur(clMean))
	fmt.Fprintf(os.Stdout, "  Ratio        %.1f× p50 improvement (closure vs CTE)\n", ratio)
	fmt.Fprintf(os.Stdout, "\nAcceptance gate for #1162: p50 ratio ≥ 10× at 10k+ files.\n")
	return 0
}

func defaultDBDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	// db.Open takes a directory and appends pincher.db. Matches the
	// closurebench tool's expectation. Windows users running supervised
	// pincher (which uses %APPDATA%\pincherMCP) should pass -db
	// explicitly — the default points at the dev / local-CLI path.
	return filepath.Join(home, ".pincher")
}

func largestProjectID(store *db.Store) (string, error) {
	projects, err := store.ListProjects()
	if err != nil {
		return "", err
	}
	if len(projects) == 0 {
		return "", fmt.Errorf("no indexed projects")
	}
	best := projects[0]
	for _, p := range projects[1:] {
		if p.SymCount > best.SymCount {
			best = p
		}
	}
	return best.ID, nil
}

// sampleSymbolsWithEdges returns up to n symbol IDs from the project
// that have at least one inbound or outbound edge. Random selection
// without replacement (a Fisher-Yates shuffle on the candidate pool).
func sampleSymbolsWithEdges(store *db.Store, projectID string, n int) ([]string, error) {
	rows, err := store.RO().Query(`
		SELECT s.id
		  FROM symbols s
		 WHERE s.project_id = ?
		   AND s.kind IN ('Function', 'Method')
		   AND EXISTS (
			   SELECT 1 FROM edges e
				WHERE e.project_id = s.project_id
				  AND (e.from_id = s.id OR e.to_id = s.id)
				LIMIT 1
		   )
		 LIMIT 5000`,
		projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var all []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		all = append(all, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Fisher-Yates shuffle truncated to n.
	for i := len(all) - 1; i > 0; i-- {
		j := rand.IntN(i + 1)
		all[i], all[j] = all[j], all[i]
	}
	if len(all) > n {
		all = all[:n]
	}
	return all, nil
}

// stats returns p50, p95, max, mean over the passed durations. Sorts
// the input slice in-place — callers don't need the original order.
func stats(ds []time.Duration) (p50, p95, max, mean time.Duration) {
	if len(ds) == 0 {
		return
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	p50 = ds[len(ds)/2]
	p95 = ds[(len(ds)*95)/100]
	max = ds[len(ds)-1]
	var sum time.Duration
	for _, d := range ds {
		sum += d
	}
	mean = sum / time.Duration(len(ds))
	return
}

func fmtDur(d time.Duration) string {
	switch {
	case d < time.Microsecond:
		return fmt.Sprintf("%dns", d.Nanoseconds())
	case d < time.Millisecond:
		return fmt.Sprintf("%.1fμs", float64(d.Microseconds())+float64(d.Nanoseconds()%1000)/1000.0)
	default:
		return fmt.Sprintf("%.2fms", float64(d.Milliseconds())+float64(d.Microseconds()%1000)/1000.0)
	}
}
