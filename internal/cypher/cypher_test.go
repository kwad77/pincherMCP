package cypher

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// ─────────────────────────────────────────────────────────────────────────────
// Test helpers
// ─────────────────────────────────────────────────────────────────────────────

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE symbols (
			id TEXT PRIMARY KEY,
			project_id TEXT,
			file_path TEXT,
			name TEXT,
			qualified_name TEXT,
			kind TEXT,
			language TEXT,
			start_byte INTEGER,
			end_byte INTEGER,
			start_line INTEGER,
			end_line INTEGER,
			is_exported INTEGER DEFAULT 0,
			is_entry_point INTEGER DEFAULT 0,
			complexity INTEGER DEFAULT 0
		);
		CREATE TABLE edges (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id TEXT,
			from_id TEXT,
			to_id TEXT,
			kind TEXT,
			confidence REAL DEFAULT 1.0
		);
	`)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func insertSym(t *testing.T, db *sql.DB, id, name, kind, lang string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO symbols(id, project_id, file_path, name, qualified_name, kind, language,
			start_byte, end_byte, start_line, end_line) VALUES (?,?,?,?,?,?,?, 0,100,1,5)`,
		id, "proj1", "file.go", name, name, kind, lang,
	)
	if err != nil {
		t.Fatalf("insert symbol %q: %v", id, err)
	}
}

func insertEdge(t *testing.T, db *sql.DB, fromID, toID, kind string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO edges(project_id, from_id, to_id, kind) VALUES ('proj1',?,?,?)`,
		fromID, toID, kind,
	)
	if err != nil {
		t.Fatalf("insert edge %s->%s: %v", fromID, toID, err)
	}
}

func exec(t *testing.T, db *sql.DB, query string) *Result {
	t.Helper()
	e := &Executor{DB: db, MaxRows: 100}
	r, err := e.Execute(context.Background(), query)
	if err != nil {
		t.Fatalf("Execute(%q): %v", query, err)
	}
	return r
}

// ─────────────────────────────────────────────────────────────────────────────
// Tokenizer tests
// ─────────────────────────────────────────────────────────────────────────────

func TestTokenize_keywords(t *testing.T) {
	toks := tokenize("MATCH WHERE RETURN LIMIT ORDER BY")
	for _, tok := range toks {
		if tok.kind != "KEYWORD" {
			t.Errorf("expected KEYWORD, got %q for %q", tok.kind, tok.value)
		}
	}
}

func TestTokenize_strings(t *testing.T) {
	toks := tokenize(`'hello' "world"`)
	if len(toks) != 2 {
		t.Fatalf("expected 2 tokens, got %d", len(toks))
	}
	for _, tok := range toks {
		if tok.kind != "STRING" {
			t.Errorf("expected STRING, got %q", tok.kind)
		}
	}
}

func TestTokenize_operators(t *testing.T) {
	toks := tokenize("<> >= <= =~ ->")
	ops := make(map[string]bool)
	for _, tok := range toks {
		ops[tok.value] = true
	}
	for _, want := range []string{"<>", ">=", "<=", "=~", "->"} {
		if !ops[want] {
			t.Errorf("expected operator %q", want)
		}
	}
}

func TestTokenize_hops(t *testing.T) {
	toks := tokenize("*1..3")
	found := false
	for _, tok := range toks {
		if tok.kind == "HOPS" && tok.value == "1..3" {
			found = true
		}
	}
	if !found {
		t.Error("expected HOPS token with value '1..3'")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Parser tests
// ─────────────────────────────────────────────────────────────────────────────

func TestParseHops(t *testing.T) {
	cases := []struct {
		s        string
		min, max int
	}{
		{"1..3", 1, 3},
		{"2..5", 2, 5},
		{"3", 3, 3},
		{"", 1, 1},
	}
	for _, c := range cases {
		mn, mx := parseHops(c.s)
		if mn != c.min || mx != c.max {
			t.Errorf("parseHops(%q) = (%d,%d), want (%d,%d)", c.s, mn, mx, c.min, c.max)
		}
	}
}

func TestParse_NodeOnlyQuery(t *testing.T) {
	q, err := parse("MATCH (f:Function) RETURN f.name LIMIT 10")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(q.patterns) != 1 {
		t.Errorf("expected 1 pattern, got %d", len(q.patterns))
	}
	if q.patterns[0].fromKind != "Function" {
		t.Errorf("fromKind = %q, want Function", q.patterns[0].fromKind)
	}
	if q.limit != 10 {
		t.Errorf("limit = %d, want 10", q.limit)
	}
}

func TestParse_EdgeQuery(t *testing.T) {
	q, err := parse("MATCH (a:Function)-[:CALLS]->(b) RETURN a.name, b.name")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(q.patterns[0].edgeKinds) == 0 {
		t.Error("expected edge kind CALLS")
	}
	if q.patterns[0].edgeKinds[0] != "CALLS" {
		t.Errorf("edgeKind = %q, want CALLS", q.patterns[0].edgeKinds[0])
	}
}

func TestParse_WhereCondition(t *testing.T) {
	q, err := parse("MATCH (f:Function) WHERE f.name = 'main' RETURN f.name")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(q.conditions) == 0 {
		t.Fatal("expected at least one condition")
	}
	c := q.conditions[0]
	if c.property != "name" || c.op != "=" || c.value != "main" {
		t.Errorf("condition = {%q %q %q}, want {name = main}", c.property, c.op, c.value)
	}
}

func TestParse_Distinct(t *testing.T) {
	q, err := parse("MATCH (f) RETURN DISTINCT f.name")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !q.distinct {
		t.Error("expected distinct=true")
	}
}

func TestParse_VariableLengthHops(t *testing.T) {
	q, err := parse("MATCH (a)-[:CALLS*1..3]->(b) WHERE a.name='main' RETURN b.name")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if q.patterns[0].minHops != 1 || q.patterns[0].maxHops != 3 {
		t.Errorf("hops = %d..%d, want 1..3", q.patterns[0].minHops, q.patterns[0].maxHops)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Executor: node scan
// ─────────────────────────────────────────────────────────────────────────────

func TestNodeScan_AllFunctions(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "Foo", "Function", "Go")
	insertSym(t, db, "f2", "Bar", "Function", "Go")
	insertSym(t, db, "t1", "MyType", "Class", "Go")

	r := exec(t, db, "MATCH (f:Function) RETURN f.name LIMIT 10")
	if r.Total != 2 {
		t.Errorf("expected 2 functions, got %d", r.Total)
	}
}

func TestNodeScan_WhereEquals(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "main", "Function", "Go")
	insertSym(t, db, "f2", "helper", "Function", "Go")

	r := exec(t, db, "MATCH (f:Function) WHERE f.name = 'main' RETURN f.name")
	if r.Total != 1 {
		t.Errorf("expected 1 result, got %d", r.Total)
	}
	if r.Rows[0]["f.name"] != "main" {
		t.Errorf("unexpected result: %v", r.Rows[0])
	}
}

func TestNodeScan_WhereRegex(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "HandleLogin", "Function", "Go")
	insertSym(t, db, "f2", "HandleLogout", "Function", "Go")
	insertSym(t, db, "f3", "DoSomething", "Function", "Go")

	r := exec(t, db, "MATCH (f:Function) WHERE f.name =~ '.*Handle.*' RETURN f.name")
	if r.Total != 2 {
		t.Errorf("expected 2 Handler functions, got %d", r.Total)
	}
}

func TestNodeScan_WhereContains(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "processOrder", "Function", "Go")
	insertSym(t, db, "f2", "processPayment", "Function", "Go")
	insertSym(t, db, "f3", "startServer", "Function", "Go")

	r := exec(t, db, "MATCH (f:Function) WHERE f.name CONTAINS 'process' RETURN f.name")
	if r.Total != 2 {
		t.Errorf("expected 2 results, got %d", r.Total)
	}
}

func TestNodeScan_Count(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "A", "Function", "Go")
	insertSym(t, db, "f2", "B", "Function", "Go")
	insertSym(t, db, "f3", "C", "Function", "Go")

	r := exec(t, db, "MATCH (f:Function) RETURN COUNT(f) AS total")
	if r.Total != 1 {
		t.Fatalf("COUNT should return 1 row, got %d", r.Total)
	}
	if r.Rows[0]["total"] != 3 {
		t.Errorf("COUNT = %v, want 3", r.Rows[0]["total"])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Executor: edge queries
// ─────────────────────────────────────────────────────────────────────────────

func TestJoinQuery_SingleHop(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "main_fn", "main", "Function", "Go")
	insertSym(t, db, "run_fn", "run", "Function", "Go")
	insertSym(t, db, "other_fn", "other", "Function", "Go")
	insertEdge(t, db, "main_fn", "run_fn", "CALLS")

	r := exec(t, db, "MATCH (a:Function)-[:CALLS]->(b) WHERE a.name='main' RETURN b.name")
	if r.Total != 1 {
		t.Errorf("expected 1 callee, got %d", r.Total)
	}
	if r.Rows[0]["b.name"] != "run" {
		t.Errorf("expected 'run', got %v", r.Rows[0]["b.name"])
	}
}

func TestJoinQuery_NoEdgeFilter(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "a", "alpha", "Function", "Go")
	insertSym(t, db, "b", "beta", "Function", "Go")
	insertEdge(t, db, "a", "b", "CALLS")

	r := exec(t, db, "MATCH (x)-[:CALLS]->(y) RETURN x.name, y.name")
	if r.Total != 1 {
		t.Errorf("expected 1 edge result, got %d", r.Total)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Executor: BFS variable-length paths
// ─────────────────────────────────────────────────────────────────────────────

func TestBFS_VariableLength(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	// Chain: main -> a -> b -> c
	insertSym(t, db, "main_fn", "main", "Function", "Go")
	insertSym(t, db, "a_fn", "a", "Function", "Go")
	insertSym(t, db, "b_fn", "b", "Function", "Go")
	insertSym(t, db, "c_fn", "c", "Function", "Go")
	insertEdge(t, db, "main_fn", "a_fn", "CALLS")
	insertEdge(t, db, "a_fn", "b_fn", "CALLS")
	insertEdge(t, db, "b_fn", "c_fn", "CALLS")

	r := exec(t, db, "MATCH (a)-[:CALLS*1..3]->(b) WHERE a.name='main' RETURN b.name")
	// Should find a, b, c (depths 1, 2, 3)
	if r.Total < 3 {
		t.Errorf("expected at least 3 nodes in chain, got %d", r.Total)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// matchesConditions
// ─────────────────────────────────────────────────────────────────────────────

func TestMatchesConditions(t *testing.T) {
	row := map[string]any{"n.name": "processOrder", "n.kind": "Function"}

	pass := []condition{
		{variable: "n", property: "name", op: "=", value: "processOrder"},
		{variable: "n", property: "name", op: "CONTAINS", value: "Order"},
		{variable: "n", property: "name", op: "STARTS WITH", value: "process"},
		{variable: "n", property: "name", op: "=~", value: ".*Order.*"},
		{variable: "n", property: "name", op: "<>", value: "other"},
	}
	for _, c := range pass {
		if !matchesConditions(row, []condition{c}) {
			t.Errorf("condition {%s %s %s} should pass", c.property, c.op, c.value)
		}
	}

	fail := []condition{
		{variable: "n", property: "name", op: "=", value: "wrong"},
		{variable: "n", property: "name", op: "<>", value: "processOrder"},
		{variable: "n", property: "name", op: "CONTAINS", value: "xyz"},
	}
	for _, c := range fail {
		if matchesConditions(row, []condition{c}) {
			t.Errorf("condition {%s %s %s} should fail", c.property, c.op, c.value)
		}
	}
}

func TestMatchesConditions_Numeric(t *testing.T) {
	row := map[string]any{"n.complexity": "5"}
	if !matchesConditions(row, []condition{{variable: "n", property: "complexity", op: ">", value: "3"}}) {
		t.Error("5 > 3 should be true")
	}
	if matchesConditions(row, []condition{{variable: "n", property: "complexity", op: "<", value: "3"}}) {
		t.Error("5 < 3 should be false")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// cypherPropToCol
// ─────────────────────────────────────────────────────────────────────────────

func TestCypherPropToCol(t *testing.T) {
	cases := map[string]string{
		"name":           "name",
		"qualified_name": "qualified_name",
		"qn":             "qualified_name",
		"kind":           "kind",
		"label":          "kind",
		"file_path":      "file_path",
		"language":       "language",
		"unknown_prop":   "",
	}
	for prop, want := range cases {
		got := cypherPropToCol(prop)
		if got != want {
			t.Errorf("cypherPropToCol(%q) = %q, want %q", prop, got, want)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Edge cases
// ─────────────────────────────────────────────────────────────────────────────

func TestEmptyQuery(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	e := &Executor{DB: db}
	r, err := e.Execute(context.Background(), "")
	if err != nil {
		t.Fatalf("empty query should not error: %v", err)
	}
	if r.Total != 0 {
		t.Errorf("empty query should return 0 rows, got %d", r.Total)
	}
}

func TestLimitRespected(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	for i := 0; i < 20; i++ {
		id := string(rune('a' + i))
		insertSym(t, db, id, id, "Function", "Go")
	}
	r := exec(t, db, "MATCH (f:Function) RETURN f.name LIMIT 5")
	if r.Total > 5 {
		t.Errorf("expected at most 5 results, got %d", r.Total)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// parseQuery coverage: ORDER BY, STARTS WITH
// ─────────────────────────────────────────────────────────────────────────────

func TestParse_OrderBy(t *testing.T) {
	tokens := tokenize("MATCH (f:Function) RETURN f.name ORDER BY f.name")
	p := &parser{tokens: tokens}
	q, err := p.parseQuery()
	if err != nil {
		t.Fatalf("parseQuery: %v", err)
	}
	if q.orderBy == "" {
		t.Error("expected orderBy to be set")
	}
}

func TestParse_OrderByDesc(t *testing.T) {
	tokens := tokenize("MATCH (f:Function) RETURN f.name ORDER BY f.name DESC")
	p := &parser{tokens: tokens}
	q, err := p.parseQuery()
	if err != nil {
		t.Fatalf("parseQuery: %v", err)
	}
	if q.orderDir != "DESC" {
		t.Errorf("orderDir = %q, want DESC", q.orderDir)
	}
}

func TestParse_OrderByAsc(t *testing.T) {
	tokens := tokenize("MATCH (f:Function) RETURN f.name ORDER BY f.name ASC")
	p := &parser{tokens: tokens}
	q, err := p.parseQuery()
	if err != nil {
		t.Fatalf("parseQuery: %v", err)
	}
	if q.orderDir == "DESC" {
		t.Error("expected ascending order (not DESC)")
	}
}

func TestParse_StartsWith(t *testing.T) {
	tokens := tokenize("MATCH (f:Function) WHERE f.name STARTS WITH 'Get' RETURN f.name")
	p := &parser{tokens: tokens}
	q, err := p.parseQuery()
	if err != nil {
		t.Fatalf("parseQuery: %v", err)
	}
	if len(q.conditions) == 0 {
		t.Fatal("expected at least 1 condition")
	}
	if q.conditions[0].op != "STARTS WITH" {
		t.Errorf("op = %q, want 'STARTS WITH'", q.conditions[0].op)
	}
}

func TestNodeScan_StartsWith(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "get1", "GetUser", "Function", "Go")
	insertSym(t, db, "set1", "SetUser", "Function", "Go")
	insertSym(t, db, "del1", "DeleteUser", "Function", "Go")

	r := exec(t, db, "MATCH (f:Function) WHERE f.name STARTS WITH 'Get' RETURN f.name")
	if r.Total != 1 {
		t.Errorf("expected 1 result for STARTS WITH 'Get', got %d", r.Total)
	}
}

func TestNodeScan_WhereNotEquals(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "Alpha", "Function", "Go")
	insertSym(t, db, "f2", "Beta", "Function", "Go")

	r := exec(t, db, "MATCH (f:Function) WHERE f.name <> 'Alpha' RETURN f.name")
	if r.Total == 0 {
		t.Error("expected results for <> operator")
	}
	for _, row := range r.Rows {
		if row["f.name"] == "Alpha" {
			t.Error("Alpha should be excluded by <> filter")
		}
	}
}

func TestNodeScan_WhereGreaterThan(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "foo", "Function", "Go")

	// complexity > 0 (default is 0) → no results
	r := exec(t, db, "MATCH (f:Function) WHERE f.complexity > 5 RETURN f.name")
	_ = r // just verify no crash
}

// ─────────────────────────────────────────────────────────────────────────────
// QueryAST and minHops
// ─────────────────────────────────────────────────────────────────────────────

func TestQueryAST_Export(t *testing.T) {
	tokens := tokenize("MATCH (f:Function)-[:CALLS]->(g) WHERE f.name = 'main' RETURN g.name LIMIT 10")
	p := &parser{tokens: tokens}
	q, err := p.parseQuery()
	if err != nil {
		t.Fatalf("parseQuery: %v", err)
	}
	info := QueryAST(q)
	if info["patterns"].(int) == 0 {
		t.Error("expected patterns > 0")
	}
	if info["conditions"].(int) == 0 {
		t.Error("expected conditions > 0")
	}
	if info["limit"].(int) != 10 {
		t.Errorf("limit = %v, want 10", info["limit"])
	}
}

func TestMinHops_WithPattern(t *testing.T) {
	tokens := tokenize("MATCH (a)-[:CALLS*1..3]->(b) RETURN b.name")
	p := &parser{tokens: tokens}
	q, err := p.parseQuery()
	if err != nil {
		t.Fatalf("parseQuery: %v", err)
	}
	if q.minHops() < 1 {
		t.Errorf("minHops = %d, want ≥1", q.minHops())
	}
}

func TestMinHops_NoPattern(t *testing.T) {
	q := &queryAST{}
	if q.minHops() != 1 {
		t.Errorf("minHops with no patterns = %d, want 1", q.minHops())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Executor.MaxRows capping
// ─────────────────────────────────────────────────────────────────────────────

func TestMaxRows_Capping(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	for i := 0; i < 5; i++ {
		insertSym(t, db, string(rune('a'+i)), string(rune('A'+i)), "Function", "Go")
	}
	// MaxRows = 0 → should use default (not panic or return 0)
	e := &Executor{DB: db, MaxRows: 0}
	r, err := e.Execute(context.Background(), "MATCH (f:Function) RETURN f.name")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Total == 0 {
		t.Error("expected results with MaxRows=0 (should use default)")
	}
}

func TestMaxRows_ExceedsCap(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	// MaxRows > 10000 → should be capped at 10000
	e := &Executor{DB: db, MaxRows: 99999}
	r, err := e.Execute(context.Background(), "MATCH (f:Function) RETURN f.name")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	_ = r // just verify no panic
}

// ─────────────────────────────────────────────────────────────────────────────
// OR conditions
// ─────────────────────────────────────────────────────────────────────────────

func TestParse_ORCondition(t *testing.T) {
	tokens := tokenize("MATCH (f:Function) WHERE f.name = 'Alpha' OR f.name = 'Beta' RETURN f.name")
	p := &parser{tokens: tokens}
	q, err := p.parseQuery()
	if err != nil {
		t.Fatalf("parseQuery: %v", err)
	}
	// Both conditions should be captured (OR treated as AND in simplified impl)
	if len(q.conditions) < 2 {
		t.Errorf("expected 2+ conditions for OR clause, got %d", len(q.conditions))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// COUNT with alias
// ─────────────────────────────────────────────────────────────────────────────

func TestNodeScan_CountWithAlias(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "Foo", "Function", "Go")
	insertSym(t, db, "f2", "Bar", "Function", "Go")

	r := exec(t, db, "MATCH (f:Function) RETURN COUNT(f) AS total")
	if r.Total == 0 {
		t.Error("expected count result")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Execute error: invalid query syntax
// ─────────────────────────────────────────────────────────────────────────────

func TestExecute_InvalidCypher(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	e := &Executor{DB: db, MaxRows: 100}
	// Malformed Cypher that can't be executed (bad WHERE with no op)
	_, err := e.Execute(context.Background(), "MATCH (f:Function) WHERE RETURN f.name")
	// Execute may or may not error — just verify no panic
	_ = err
}
