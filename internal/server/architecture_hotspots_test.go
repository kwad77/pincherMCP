package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #305: hotspots default to production code only. Test helpers
// (newTestServer, makeReq, decode) have huge in-degree because every
// test imports them, but they're not signal for orientation.

// isTestFile is a pure helper — pin its decisions across the
// languages pincher indexes so a future tweak to one extension
// doesn't silently change the cross-language filter.
func TestIsTestFile_RecognisedConventions(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		// Go
		{"server_test.go", true},
		{"internal/server/server_test.go", true},
		{`internal\server\server_test.go`, true}, // Windows-style
		{"server.go", false},
		{"internal/server/server.go", false},

		// JS / TS
		{"foo.test.ts", true},
		{"foo.test.tsx", true},
		{"foo.spec.js", true},
		{"foo.spec.jsx", true},
		{"foo.test.mjs", true},
		{"foo.test.cjs", true},
		{"foo.ts", false},
		{"foo.spec.unrelated", false},

		// Python
		{"test_foo.py", true},
		{"foo_test.py", true},
		{"tests/foo.py", true},
		{"src/foo.py", false},

		// Ruby
		{"foo_spec.rb", true},
		{"foo_test.rb", true},
		{"spec/foo.rb", true},

		// Java / Kotlin / Scala
		{"FooTest.java", true},
		{"FooTest.kt", true},
		{"FooSpec.scala", true},
		{"Foo.java", false},

		// Directory conventions
		{"src/__tests__/foo.js", true},
		{"src/test/Foo.java", true},
		{"tests/foo.py", true},
		{"test/integration/foo.go", true},

		// Negatives — paths that look testy but aren't.
		{"src/featuretest/main.go", false},   // `featuretest` is part of a name, not a directory
		{"specfile.txt", false},              // no recognised extension
		{"testdata/corpus/foo.go", false},    // testdata is fixtures, not tests
		{"contest_winner.go", false},         // `contest` is not `_test`
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			if got := isTestFile(c.path); got != c.want {
				t.Errorf("isTestFile(%q) = %v, want %v", c.path, got, c.want)
			}
		})
	}
}

// End-to-end: seed a project with a mix of production and test
// hotspots; default architecture call drops the test ones.
func TestHandleArchitecture_HotspotsExcludeTestFilesByDefault(t *testing.T) {
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{ID: "p1", Path: "/tmp/p1", Name: "p1", IndexedAt: time.Now()})

	// Seed 4 callers + 2 callees: one production callee (Compute),
	// one test-helper callee (newTestServer). Each gets the same
	// number of inbound CALLS edges.
	syms := []db.Symbol{
		{ID: "s::pkg.Compute#Function", ProjectID: "p1", FilePath: "internal/svc/svc.go",
			Name: "Compute", QualifiedName: "pkg.Compute", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1},
		{ID: "s::pkg.newTestServer#Function", ProjectID: "p1", FilePath: "internal/server/server_test.go",
			Name: "newTestServer", QualifiedName: "pkg.newTestServer", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1},
	}
	for i := 0; i < 5; i++ {
		syms = append(syms,
			db.Symbol{ID: "s::caller_p" + string(rune('0'+i)) + "#Function",
				ProjectID: "p1", FilePath: "internal/svc/svc.go",
				Name: "callerP" + string(rune('0'+i)), QualifiedName: "pkg.callerP" + string(rune('0'+i)),
				Kind: "Function", Language: "Go", ExtractionConfidence: 1},
			db.Symbol{ID: "s::caller_t" + string(rune('0'+i)) + "#Function",
				ProjectID: "p1", FilePath: "internal/server/server_test.go",
				Name: "callerT" + string(rune('0'+i)), QualifiedName: "pkg.callerT" + string(rune('0'+i)),
				Kind: "Function", Language: "Go", ExtractionConfidence: 1},
		)
	}
	if err := store.BulkUpsertSymbols(syms); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}
	// Each caller calls both callees so they tie on count; only the
	// file_path distinguishes them.
	var edges []db.Edge
	for i := 0; i < 5; i++ {
		edges = append(edges,
			db.Edge{ProjectID: "p1",
				FromID: "s::caller_p" + string(rune('0'+i)) + "#Function",
				ToID:   "s::pkg.Compute#Function", Kind: "CALLS", Confidence: 1},
			db.Edge{ProjectID: "p1",
				FromID: "s::caller_p" + string(rune('0'+i)) + "#Function",
				ToID:   "s::pkg.newTestServer#Function", Kind: "CALLS", Confidence: 1},
		)
	}
	if err := store.BulkUpsertEdges(edges); err != nil {
		t.Fatalf("BulkUpsertEdges: %v", err)
	}

	result, err := srv.handleArchitecture(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleArchitecture: %v", err)
	}
	body := decode(t, result)
	hotspots, _ := body["hotspots"].([]any)
	for _, h := range hotspots {
		entry, _ := h.(map[string]any)
		fp, _ := entry["file_path"].(string)
		if isTestFile(fp) {
			t.Errorf("hotspot from test file leaked through default filter: %v", entry)
		}
	}
}

// include_tests=true restores the legacy mixed list.
func TestHandleArchitecture_IncludeTestsTrue_SurfacesTestHotspots(t *testing.T) {
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{ID: "p1", Path: "/tmp/p1", Name: "p1", IndexedAt: time.Now()})

	syms := []db.Symbol{
		{ID: "s::pkg.helper#Function", ProjectID: "p1", FilePath: "internal/server/server_test.go",
			Name: "helper", QualifiedName: "pkg.helper", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1},
		{ID: "s::caller#Function", ProjectID: "p1", FilePath: "internal/server/server_test.go",
			Name: "caller", QualifiedName: "pkg.caller", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1},
	}
	if err := store.BulkUpsertSymbols(syms); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}
	if err := store.BulkUpsertEdges([]db.Edge{{
		ProjectID: "p1", FromID: "s::caller#Function", ToID: "s::pkg.helper#Function",
		Kind: "CALLS", Confidence: 1,
	}}); err != nil {
		t.Fatalf("BulkUpsertEdges: %v", err)
	}

	result, err := srv.handleArchitecture(context.Background(), makeReq(map[string]any{
		"include_tests": true,
	}))
	if err != nil {
		t.Fatalf("handleArchitecture: %v", err)
	}
	body := decode(t, result)
	hotspots, _ := body["hotspots"].([]any)
	if len(hotspots) == 0 {
		t.Error("include_tests=true should surface test-file hotspots; got none")
	}
}

// #306: Project struct uses snake_case JSON keys.
func TestProject_JSONUsesSnakeCase(t *testing.T) {
	p := db.Project{
		ID: "x", Path: "/p", Name: "p",
		IndexedAt: time.Now(), FileCount: 1, SymCount: 2, EdgeCount: 3,
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	wantKeys := []string{`"id":`, `"path":`, `"name":`, `"indexed_at":`, `"file_count":`, `"symbol_count":`, `"edge_count":`}
	for _, k := range wantKeys {
		if !strings.Contains(got, k) {
			t.Errorf("Project JSON missing %q; got %s", k, got)
		}
	}
	// Ensure the legacy PascalCase keys are NOT present.
	for _, k := range []string{`"ID":`, `"Path":`, `"Name":`, `"FileCount":`, `"IndexedAt":`} {
		if strings.Contains(got, k) {
			t.Errorf("Project JSON still has PascalCase key %q; got %s", k, got)
		}
	}
}
