package index

import (
	"context"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// Integration test for #247 #3: package-level vars/consts extracted as
// Variable symbols + cross-file READS edges resolved against the
// project's symbol table.
//
// Shape of the test corpus:
//   limits.go : const MaxRetries = 3
//   config.go : var Cache map[string]int
//   handler.go: func Foo() { _ = Cache; _ = MaxRetries; helper() }
//               func helper() { return }
//
// Assertions:
//   - Cache and MaxRetries surface as Variable symbols.
//   - Foo has READS edges to Cache and MaxRetries (cross-file).
//   - Foo's call to helper() produces a CALLS edge, NOT a READS edge,
//     even though `helper` is also an Ident in Foo's body.
//   - helper itself doesn't get spurious READS to MaxRetries (it
//     doesn't reference it; only Foo does).

const fixtureLimits = `package svc

// MaxRetries caps retry attempts per request.
const MaxRetries = 3
`

const fixtureConfig = `package svc

// Cache holds in-memory entries.
var Cache map[string]int
`

const fixtureHandler = `package svc

// Foo reads both Cache and MaxRetries and calls helper.
func Foo() {
	_ = Cache
	_ = MaxRetries
	helper()
}

func helper() {
	return
}
`

func TestIndex_ReadsEdges_VariablesExtractedAsSymbols(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "svc/limits.go", fixtureLimits)
	writeFile(t, dir, "svc/config.go", fixtureConfig)
	writeFile(t, dir, "svc/handler.go", fixtureHandler)
	pid := db.ProjectIDFromPath(dir)

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	for _, name := range []string{"Cache", "MaxRetries"} {
		syms, err := store.GetSymbolsByName(pid, name, 5)
		if err != nil {
			t.Fatalf("GetSymbolsByName(%q): %v", name, err)
		}
		if len(syms) == 0 {
			t.Errorf("Variable symbol %q not extracted", name)
			continue
		}
		if syms[0].Kind != "Variable" {
			t.Errorf("%s.Kind = %q, want Variable (#247 #3 ValueSpec extraction)", name, syms[0].Kind)
		}
	}
}

func TestIndex_ReadsEdges_FooReadsCacheAndMaxRetries(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "svc/limits.go", fixtureLimits)
	writeFile(t, dir, "svc/config.go", fixtureConfig)
	writeFile(t, dir, "svc/handler.go", fixtureHandler)
	_ = db.ProjectIDFromPath(dir) // referenced indirectly via the indexer

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	fooID := db.MakeSymbolID("svc/handler.go", "svc.Foo", "Function")
	cacheID := db.MakeSymbolID("svc/config.go", "svc.Cache", "Variable")
	maxID := db.MakeSymbolID("svc/limits.go", "svc.MaxRetries", "Variable")

	// Inbound edges to Cache must include Foo with READS.
	inboundCache, err := store.EdgesTo(cacheID, nil)
	if err != nil {
		t.Fatalf("EdgesTo Cache: %v", err)
	}
	if !hasEdge(inboundCache, fooID, "READS") {
		t.Errorf("expected READS edge Foo → Cache:\n  inbound: %v", inboundCache)
	}

	// Inbound to MaxRetries similarly.
	inboundMax, err := store.EdgesTo(maxID, nil)
	if err != nil {
		t.Fatalf("EdgesTo MaxRetries: %v", err)
	}
	if !hasEdge(inboundMax, fooID, "READS") {
		t.Errorf("expected READS edge Foo → MaxRetries:\n  inbound: %v", inboundMax)
	}
}

// helper() is called from Foo; the Ident `helper` shouldn't produce
// a spurious READS edge to itself or to anything else. Only the CALLS
// edge resolves (helper is a Function, not a Variable).
func TestIndex_ReadsEdges_FunctionCallNotResolvedAsRead(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "svc/limits.go", fixtureLimits)
	writeFile(t, dir, "svc/config.go", fixtureConfig)
	writeFile(t, dir, "svc/handler.go", fixtureHandler)
	_ = db.ProjectIDFromPath(dir)

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	helperID := db.MakeSymbolID("svc/handler.go", "svc.helper", "Function")
	inboundHelper, err := store.EdgesTo(helperID, nil)
	if err != nil {
		t.Fatalf("EdgesTo helper: %v", err)
	}
	for _, e := range inboundHelper {
		if e.Kind == "READS" {
			t.Errorf("Function call surfaced as READS edge (should be CALLS only): %v", e)
		}
	}
	// At least the CALLS edge from Foo should still be present —
	// READS extraction must not regress CALLS resolution.
	fooID := db.MakeSymbolID("svc/handler.go", "svc.Foo", "Function")
	if !hasEdge(inboundHelper, fooID, "CALLS") {
		t.Errorf("CALLS edge Foo → helper missing; READS extraction regressed CALLS resolution:\n  inbound: %v", inboundHelper)
	}
}

// helper() doesn't reference Cache/MaxRetries, so neither should
// surface in inbound edges from helper. Pin so a future regression
// in dedupe / scope / over-emission doesn't add cross-function
// false positives.
func TestIndex_ReadsEdges_HelperHasNoSpuriousReads(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "svc/limits.go", fixtureLimits)
	writeFile(t, dir, "svc/config.go", fixtureConfig)
	writeFile(t, dir, "svc/handler.go", fixtureHandler)
	_ = db.ProjectIDFromPath(dir)

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	helperID := db.MakeSymbolID("svc/handler.go", "svc.helper", "Function")
	outbound, err := store.EdgesFrom(helperID, nil)
	if err != nil {
		t.Fatalf("EdgesFrom helper: %v", err)
	}
	for _, e := range outbound {
		if e.Kind == "READS" {
			t.Errorf("helper has unexpected outbound READS edge: %v", e)
		}
	}
}

// Repeated references to the same Variable from the same function
// must produce ONE READS edge, not N. This pins the seen[key] dedupe
// path in resolveReads — without it, a function with `_ = Cache; _ =
// Cache` would emit two duplicate edges and inflate trace fan-in.
// Also exercises the QN/name lookup-cache hit paths (second lookup of
// the same identifier returns the cached entry instead of re-querying).
func TestIndex_ReadsEdges_DedupesRepeatedReadsToSameTarget(t *testing.T) {
	const repeated = `package svc

// FooRepeats reads Cache three times — should still produce one
// READS edge to Cache.
func FooRepeats() {
	_ = Cache
	_ = Cache
	_ = Cache
}
`
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "svc/config.go", fixtureConfig)
	writeFile(t, dir, "svc/repeats.go", repeated)

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	cacheID := db.MakeSymbolID("svc/config.go", "svc.Cache", "Variable")
	inbound, err := store.EdgesTo(cacheID, nil)
	if err != nil {
		t.Fatalf("EdgesTo Cache: %v", err)
	}
	fooID := db.MakeSymbolID("svc/repeats.go", "svc.FooRepeats", "Function")
	count := 0
	for _, e := range inbound {
		if e.Kind == "READS" && e.FromID == fooID {
			count++
		}
	}
	if count != 1 {
		t.Errorf("READS edge count from FooRepeats → Cache = %d, want 1 (dedup must collapse N references to one edge)", count)
	}
}

// hasEdge reports whether the slice contains an edge with the given
// FromID and Kind. Edges-to / edges-from queries return both endpoints
// in a uniform shape; the caller already knows the target side, so we
// only need to verify the other end + the kind.
func hasEdge(edges []db.Edge, otherEndID, kind string) bool {
	for _, e := range edges {
		if e.Kind == kind && (e.FromID == otherEndID || e.ToID == otherEndID) {
			return true
		}
	}
	return false
}
