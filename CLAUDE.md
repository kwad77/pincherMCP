# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test Commands

```bash
# Build the binary
go build -o pincher.exe ./cmd/pinch/     # Windows
go build -o pincher ./cmd/pinch/         # Linux/macOS

# Run all tests
go test ./...

# Run tests with coverage
go test ./... -coverprofile=cover.out
go tool cover -func=cover.out | grep "^total"

# Run a single test
go test ./internal/db/ -run TestGraphStats_WithData -v

# Run tests for one package
go test ./internal/server/ -v

# View per-function coverage gaps
go tool cover -func=cover.out | grep -v "100.0%" | sort -t'%' -k1 -n
```

**After any schema change** (adding a column to `db.go`), rebuild `pincher.exe` and reconnect via `/mcp` in Claude Code so the binary serving MCP requests picks up the new schema.

## Architecture

### Data flow

```
cmd/pinch/main.go
  → db.Open()              open/migrate SQLite (schema v4)
  → index.New()            create indexer (holds *db.Store)
  → server.New()           create MCP server (holds *db.Store + *index.Indexer)
  → srv.StartSessionFlusher() background goroutine: flushes session stats to DB every 10s
  → idx.Watch()            background goroutine: polls projects for file changes
  → [--http :PORT]         optional HTTP server for platform-agnostic REST access
  → mcp.StdioTransport     JSON-RPC 2.0 over stdin/stdout (Claude Code)
```

### Three-layer storage (single `symbols` table serves all three)

| Layer | Mechanism | Query path |
|---|---|---|
| 1 — Byte-offset retrieval | `start_byte` / `end_byte` on every symbol | `GetSymbol` → `ReadSymbolSource` = 1 SQL + 1 `os.File.Seek` + 1 `Read` |
| 2 — Knowledge graph | `symbols` rows + `edges` table | Cypher → SQL via `cypher/engine.go` |
| 3 — FTS5 full-text search | `symbols_fts` virtual table + 3 triggers | `SearchSymbols` via BM25 |

All three indexes are populated in a single `ast.Extract()` call per file during indexing.

### Package responsibilities

- **`internal/db/db.go`** — SQLite store. Schema lives here as a `schema` const. Schema migrations live in `schemaMigrations` (a `[]string` slice — append to add a migration; version is auto-derived from slice length). Current schema: **v4** (added `sessions` table for persistent savings tracking). `symSelectFrom` is the canonical SELECT column list used by all symbol queries; update it and all scan functions together when adding columns.

- **`internal/ast/extractor.go`** — Multi-language symbol extraction. `Extract(source, language, relPath)` dispatches to per-language extractors and sets `ExtractionConfidence` on each symbol (1.0 for Go/AST, 0.85 for stable regex languages, 0.70 for approximate ones). Go uses `go/ast`; all other languages use regex. `extractionConfidence` map controls per-language scores.

- **`internal/ast/languages.go`** — File extension → language detection and `IsSourceFile` filter.

- **`internal/cypher/engine.go`** — Cypher-to-SQL translation. Pipeline: `tokenize` → `parseQuery` → `run`. Three query paths: `runNodeScan` (no edge), `runJoinQuery` (single-hop, SQL JOIN), `runBFS` (variable-length, Go BFS loop). `symRow` struct and all SELECT queries must stay in sync with `db.go`'s `Symbol` fields — both have `extraction_confidence`.

- **`internal/index/indexer.go`** — Indexing pipeline. `Index()` walks files concurrently (goroutine per file, `sync.WaitGroup`), hashes with xxh3, skips unchanged files, calls `ast.Extract`, converts to `db.Symbol`/`db.Edge`, flushes in batches. Per-project mutex (`idx.active` map + `idx.mu`) prevents concurrent index of the same project. `Watch()` polls all projects every 2s (active) or 30s (idle). On re-index, detects file moves by matching `(qualified_name, kind)` across projects and records redirects in `symbol_moves`.

- **`internal/server/server.go`** — MCP server + HTTP REST gateway. All 14 tools registered in `registerTools()`. Every handler calls `jsonResultWithMeta()` which wraps the result in a `_meta` envelope and atomically increments session stats. `StartSessionFlusher()` flushes those stats to the `sessions` table every 10s. Token savings are estimated via `savedVsFileSizes()` (uses real `os.Stat` file sizes for search/trace) and `savedVsFullRead()` (uses `avgFileSize=20000` for other tools) — baseline is "agent reads whole file", not just the symbol. The HTTP stats endpoint falls back to DB totals when no live MCP session exists (e.g. HTTP-only dashboard process). `ServeHTTP` / `ListenAndServeHTTP` expose all tools as `POST /v1/{tool}` plus `DELETE /v1/projects` for project removal. `sessionID`/`sessionRoot` are set once via `sessionOnce` from the MCP roots list. The `cypher.Executor` is initialised with `ProjectID` so all three query paths (node scan, JOIN, BFS) are scoped to the resolved project.

### The 14 MCP tools

| # | Tool | Purpose |
|---|---|---|
| 1 | `index` | Index a repo (incremental by default; `force=true` to re-parse all) |
| 2 | `symbol` | Retrieve source by stable ID via O(1) byte-offset seek |
| 3 | `symbols` | Batch retrieve multiple symbols in one call |
| 4 | `context` | Symbol + its direct imports as a minimal-token bundle |
| 5 | `search` | FTS5 BM25 full-text search (wildcards, phrases, kind/language/fields filters) |
| 6 | `query` | Cypher-like graph queries (node scan, single-hop JOIN, BFS) |
| 7 | `trace` | BFS call-path trace with CRITICAL/HIGH/MEDIUM/LOW risk labels |
| 8 | `changes` | Git diff → affected symbols → blast radius BFS |
| 9 | `architecture` | High-level orientation: languages, entry points, hotspot functions |
| 10 | `schema` | Graph schema: node/edge kind counts |
| 11 | `list` | All indexed projects with stats |
| 12 | `adr` | Architecture Decision Records: persistent key/value project knowledge |
| 13 | `health` | Schema version, index staleness, per-language extraction coverage |
| 14 | `stats` | Session savings summary: tokens used/saved, cost avoided, call count |

### Symbol ID format

```
"{file_path}::{qualified_name}#{kind}"
e.g. "internal/db/db.go::db.Open#Function"
```

IDs are stable across re-indexing as long as file path and qualified name don't change. Built by `db.MakeSymbolID()`. If a file moves, `handleSymbol` automatically resolves stale IDs via `store.ResolveStaleID()` → `symbol_moves` table.

### Schema migration pattern

To add a schema change:
1. Append a SQL string to `schemaMigrations` in `db.go`
2. Update the `Symbol` struct field, `symSelectFrom` const, and all scan functions (`scanOneSymbol`, `scanSymbolRowsRow`, `scanSymbolRow`) together
3. Update `cypher/engine.go`'s `symRow` struct and all SELECT queries there too
4. Update `ast/extractor.go`'s `ExtractedSymbol` struct and `indexer.go`'s symbol construction if the field originates in extraction

### Key invariants

- `db.SetMaxOpenConns(1)` — SQLite is single-writer; all writes are serialized at the connection pool level
- WAL mode + `_busy_timeout=5000` — readers never block writers; a 5s retry window prevents immediate failures during index
- FTS5 triggers (`sym_fts_insert`, `sym_fts_delete`, `sym_fts_update`) auto-sync the `symbols_fts` virtual table; never manually sync it
- `flushBuffers` is called when the in-memory batch reaches 500 symbols or 1000 edges to bound memory during large index runs

## Dependencies

- `github.com/modelcontextprotocol/go-sdk v1.4.0` — MCP server framework (JSON-RPC 2.0 over stdio)
- `modernc.org/sqlite` — Pure-Go SQLite (no CGO required)
- `github.com/boyter/gocodewalker` — File walker that respects `.gitignore`
- `github.com/zeebo/xxh3` — Fast content hashing for incremental indexing

## Known Architectural Limitations (tracked, not yet fixed)

- **Regex gap**: 19 non-Go languages use regex extraction (~80% accuracy). `extraction_confidence` field surfaces this to callers. Full fix = tree-sitter bindings (no CGO path; planned next sprint).
- **Single-user SQLite**: The `sessions` table and symbol store are local. For team/enterprise shared indexes, a server mode with a shared DB path or a PostgreSQL backend is needed.
- **HTTP auth**: The `--http` REST API supports optional bearer token auth via `--http-key <token>`. Without it the API is open; for production deployment put it behind a reverse proxy or always set `--http-key`.
- **Two-process stats gap**: The MCP stdio process and the HTTP dashboard process are separate. Stats are shared via the `sessions` SQLite table (flushed every 10s). The dashboard shows all-time totals from DB when it has no live MCP session.
- **`symbols` batch cap**: `maxBatchSymbols = 100` — the batch symbols tool rejects requests with more than 100 IDs.
