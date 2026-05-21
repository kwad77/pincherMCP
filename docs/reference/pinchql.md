# pinchQL query reference

[Back to reference index](README.md)

pincher's graph-query language is **pinchQL** — a Cypher-shaped pragmatic subset that translates to SQL at query time. The grammar below is the contract; anything outside it is unsupported. All queries are scoped to one project.

> **Why "pinchQL" and not "Cypher"?** Real Cypher (the Neo4j query language) is a moving target with hundreds of features pincher doesn't implement and won't. Calling our subset "Cypher-like" set a maintenance backlog of forever-pending features. pinchQL is honest about scope: what's documented below is what works, full stop. The MCP `query` tool's `pinchql` parameter is the new canonical name; the `cypher` parameter name is still accepted as a soft alias for one release to ease transition. Decided in #206.

```pinchql
-- Node scan: all functions matching a regex
MATCH (f:Function) WHERE f.name =~ '.*Handler.*' RETURN f.name, f.file_path

-- Single-hop JOIN: what does main() call? (sub-ms)
MATCH (f:Function)-[:CALLS]->(g) WHERE f.name = 'main' RETURN g.name, g.file_path LIMIT 20

-- Variable-length BFS: call chains up to 3 hops
MATCH (a)-[:CALLS*1..3]->(b) WHERE a.name = 'ProcessOrder' RETURN b.name

-- Aggregation
MATCH (f:Function) RETURN COUNT(f) AS total

-- Named edge variables (access confidence, kind)
MATCH (a:Function)-[r:CALLS]->(b:Function) WHERE a.name = 'main'
RETURN a.name, r.kind, r.confidence, b.name

-- Ordering
MATCH (f:Function) WHERE f.file_path STARTS WITH 'internal/'
RETURN f.name, f.start_line ORDER BY f.start_line ASC

-- Filter by exported status
MATCH (f:Function) WHERE f.language = 'Go' AND f.is_exported = true
RETURN f.name, f.file_path LIMIT 50
```

**Supported operators:** `=`, `<>`, `>`, `<`, `>=`, `<=`, `=~` (regex), `CONTAINS`, `STARTS WITH`

**Supported clauses:** `WHERE`, `RETURN`, `ORDER BY`, `LIMIT`, `SKIP`, `COUNT()`

**Edge kinds indexed:** `CALLS`, `IMPORTS`, `REFERENCES` (for HCL `var.NAME` references). For Go, `CALLS` and `IMPORTS` are resolved **across files** via a deferred project-wide pass — `Bar()` calling `Foo()` from a different file in the same module produces a real `CALLS` edge. `IMPORTS` is resolved against `Module` symbols using `go.mod` to rewrite intra-module paths. For other languages, `CALLS`/`IMPORTS` are scoped to a single file (the per-file regex name table can't safely match across files without false positives).

**Node kinds indexed:** `Function`, `Method`, `Class` (and per-language subtypes: `Interface`, `Struct`, `Trait`, `Type`), `Module` (one per Go file or Terraform `module` block), `Variable` (also covers Terraform `variable` blocks as `var.NAME`), `Setting` (one per YAML/JSON/HCL `.tfvars`/TOML key, dotted-path qualified), Terraform-specific `Resource` / `DataSource` / `Output` / `Local` / `Provider`, `Block` (nested HCL blocks of any depth), `Section` (Markdown headings), `Document` (URLs stored by the `fetch` tool).
