package server

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #400: parseFieldsArg unit-pins. Empty / whitespace / single / multi /
// trailing-comma / trim-internal cases.
func TestParseFieldsArg(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want []string // sorted for stable check
	}{
		{"empty", "", nil},
		{"whitespace_only", "   ", nil},
		{"single", "id", []string{"id"}},
		{"multi", "id,name,kind", []string{"id", "kind", "name"}},
		{"trim_internal", " id , name , kind ", []string{"id", "kind", "name"}},
		{"trailing_comma", "id,name,", []string{"id", "name"}},
		{"empty_segments", "id,,,name", []string{"id", "name"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseFieldsArg(c.in)
			if c.want == nil {
				if got != nil {
					t.Errorf("parseFieldsArg(%q) = %v, want nil", c.in, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("parseFieldsArg(%q) = nil, want %v", c.in, c.want)
			}
			for _, k := range c.want {
				if !got[k] {
					t.Errorf("missing key %q in %v", k, got)
				}
			}
			if len(got) != len(c.want) {
				t.Errorf("size = %d, want %d (got %v)", len(got), len(c.want), got)
			}
		})
	}
}

// projectFields with nil allow returns the input unchanged. With
// non-nil allow, only allowed keys + _meta survive.
func TestProjectFields(t *testing.T) {
	t.Parallel()
	in := map[string]any{
		"id":     "x",
		"name":   "Foo",
		"kind":   "Function",
		"_meta":  map[string]any{"savings": "..."},
		"source": "func Foo() {}",
	}

	// nil allow → untouched.
	if got := projectFields(in, nil); !sameKeys(got, []string{"id", "name", "kind", "_meta", "source"}) {
		t.Errorf("nil allow should return all keys; got %v", got)
	}

	// allow={id,name} → only id, name, _meta.
	allow := map[string]bool{"id": true, "name": true}
	got := projectFields(in, allow)
	if !sameKeys(got, []string{"id", "name", "_meta"}) {
		t.Errorf("allow {id,name} should keep id+name+_meta only; got %v", got)
	}

	// Unknown key in allow → silently skipped.
	allow = map[string]bool{"id": true, "nonexistent": true}
	got = projectFields(in, allow)
	if !sameKeys(got, []string{"id", "_meta"}) {
		t.Errorf("unknown key in allow should be skipped; got %v", got)
	}

	// _meta absent → not added.
	in2 := map[string]any{"id": "x", "name": "Foo"}
	got = projectFields(in2, map[string]bool{"id": true})
	if !sameKeys(got, []string{"id"}) {
		t.Errorf("absent _meta should stay absent; got %v", got)
	}
}

func sameKeys(m map[string]any, want []string) bool {
	if len(m) != len(want) {
		return false
	}
	for _, k := range want {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}

// handleSymbols with fields=id,name returns only those keys + _meta;
// source field is omitted AND the per-symbol disk read is skipped.
func TestHandleSymbols_FieldsProjection(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{ID: "p1", Path: "/tmp/p1", Name: "p1", IndexedAt: time.Now()})

	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: "p1::pkg.Foo#Function", ProjectID: "p1", FilePath: "main.go",
			Name: "Foo", QualifiedName: "pkg.Foo", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
		{ID: "p1::pkg.Bar#Function", ProjectID: "p1", FilePath: "main.go",
			Name: "Bar", QualifiedName: "pkg.Bar", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
	})

	result, err := srv.handleSymbols(context.Background(), makeReq(map[string]any{
		"ids":    []string{"p1::pkg.Foo#Function", "p1::pkg.Bar#Function"},
		"fields": "id,name",
	}))
	if err != nil {
		t.Fatal(err)
	}
	body := decode(t, result)
	syms, _ := body["symbols"].([]any)
	if len(syms) != 2 {
		t.Fatalf("expected 2 symbols; got %d", len(syms))
	}
	for _, s := range syms {
		entry, _ := s.(map[string]any)
		// Only id, name, possibly _meta should be present.
		for k := range entry {
			if k != "id" && k != "name" && k != "_meta" {
				t.Errorf("unexpected field %q in projected entry %v", k, entry)
			}
		}
		if _, ok := entry["source"]; ok {
			t.Errorf("source should be omitted by projection; got %v", entry)
		}
	}
}

// handleContext with fields=symbol drops imports + callees from the
// response.
func TestHandleContext_FieldsProjection(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p2"
	store.UpsertProject(db.Project{ID: "p2", Path: "/tmp/p2", Name: "p2", IndexedAt: time.Now()})

	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: "p2::pkg.Main#Function", ProjectID: "p2", FilePath: "main.go",
			Name: "Main", QualifiedName: "pkg.Main", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
	})

	result, err := srv.handleContext(context.Background(), makeReq(map[string]any{
		"id":     "p2::pkg.Main#Function",
		"fields": "symbol",
	}))
	if err != nil {
		t.Fatal(err)
	}
	body := decode(t, result)
	for _, key := range []string{"imports", "callees"} {
		if _, ok := body[key]; ok {
			t.Errorf("fields=symbol should drop %q; got %v", key, body)
		}
	}
	if _, ok := body["symbol"]; !ok {
		t.Errorf("symbol must be present; got %v", body)
	}
}

// handleTrace with fields=hops drops risk_summary from the
// top-level response.
func TestHandleTrace_FieldsProjection(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p3"
	store.UpsertProject(db.Project{ID: "p3", Path: "/tmp/p3", Name: "p3", IndexedAt: time.Now()})

	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: "p3::pkg.Tgt#Function", ProjectID: "p3", FilePath: "svc.go",
			Name: "Tgt", QualifiedName: "pkg.Tgt", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
		{ID: "p3::pkg.Caller#Function", ProjectID: "p3", FilePath: "svc.go",
			Name: "Caller", QualifiedName: "pkg.Caller", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
	})
	store.BulkUpsertEdges([]db.Edge{
		{ProjectID: "p3", FromID: "p3::pkg.Caller#Function",
			ToID: "p3::pkg.Tgt#Function", Kind: "CALLS", Confidence: 1},
	})

	result, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"name":      "Tgt",
		"direction": "inbound",
		"fields":    "hops,total",
	}))
	if err != nil {
		t.Fatal(err)
	}
	body := decode(t, result)
	if _, ok := body["risk_summary"]; ok {
		t.Errorf("fields=hops,total should drop risk_summary; got %v", body)
	}
	if _, ok := body["hops"]; !ok {
		t.Errorf("hops should be present; got %v", body)
	}
}

// handleChanges with fields=summary,tests_to_run drops changed_symbols
// and impacted lists. Requires a real git repo for `git diff` to succeed.
func TestHandleChanges_FieldsProjection(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	srv, store, _ := newTestServer(t)
	repoDir := t.TempDir()
	gitDo := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = repoDir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitDo("init", "-b", "main")
	gitDo("config", "user.email", "t@t")
	gitDo("config", "user.name", "t")
	gitDo("commit", "--allow-empty", "-m", "init")

	store.UpsertProject(db.Project{ID: repoDir, Path: repoDir, Name: "test", IndexedAt: time.Now()})
	srv.sessionID = repoDir
	srv.sessionRoot = repoDir

	result, err := srv.handleChanges(context.Background(), makeReq(map[string]any{
		"fields": "summary,tests_to_run",
	}))
	if err != nil {
		t.Fatal(err)
	}
	body := decode(t, result)
	for _, dropped := range []string{"changed_files", "changed_symbols", "impacted"} {
		if _, ok := body[dropped]; ok {
			t.Errorf("fields=summary,tests_to_run should drop %q; got keys %v", dropped, mapKeys(body))
		}
	}
	if _, ok := body["summary"]; !ok {
		t.Errorf("summary should be present; got keys %v", mapKeys(body))
	}
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// _meta is always preserved by projection — even when the caller
// passed a fields list that doesn't include it.
func TestProjectFields_MetaAlwaysPreserved(t *testing.T) {
	t.Parallel()
	in := map[string]any{
		"id":    "x",
		"_meta": map[string]any{"k": "v"},
	}
	got := projectFields(in, map[string]bool{"id": true})
	meta, ok := got["_meta"].(map[string]any)
	if !ok {
		t.Fatalf("_meta missing after projection; got %v", got)
	}
	if v, _ := meta["k"].(string); v != "v" {
		t.Errorf("_meta value wrong; got %v", meta)
	}
	// Sanity: must NOT have _meta listed in the allow set for it to
	// survive — this is the surprising behaviour caller might trip on.
	if !strings.Contains(toString(got), "_meta") {
		t.Errorf("_meta must survive projection unconditionally")
	}
}

func toString(m map[string]any) string {
	out := ""
	for k := range m {
		out += k + ","
	}
	return out
}
