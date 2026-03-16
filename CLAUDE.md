# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test Commands

```bash
# Build the binary
go build -o pinch.exe ./cmd/pinch        # Windows
go build -o pinch ./cmd/pinch            # Linux/macOS

# Rebuild pincher.exe (used by MCP config — must stay in sync with cmd/pinch)
go build -o pincher.exe ./cmd/pinch/

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
  → db.Open()        open/migrate SQLite
  → index.New()      create indexer (holds *db.Store)
  → server.New()     create MCP server (holds *db.Store + *index.Indexer)
  → idx.Watch()      background goroutine: polls projects for file changes
  → mcp.StdioTransport  JSON-RPC 2.0 over stdin/stdout
```

### Three-layer storage (single `symbols` table serves all three)

| Layer | Mechanism | Query path |
|---|---|---|
| 1 — Byte-offset retrieval | `start_byte` / `end_byte` on every symbol | `GetSymbol` → `ReadSymbolSource` = 1 SQL + 1 `os.File.Seek` + 1 `Read` |
| 2 — Knowledge graph | `symbols` rows + `edges` table | Cypher → SQL via `cypher/engine.go` |
| 3 — FTS5 full-text search | `symbols_fts` virtual table + 3 triggers | `SearchSymbols` via BM25 |

All three indexes are populated in a single `ast.Extract()` call per file during indexing.

### Package responsibilities

- **`internal/db/db.go`** — SQLite store. Schema lives here as a `schema` const. Schema migrations live in `schemaMigrations` (a `[]string` slice — append to add a migration; version is auto-derived from slice length). `symSelectFrom` is the canonical SELECT column list used by all symbol queries; update it and all scan functions together when adding columns.

- **`internal/ast/extractor.go`** — Multi-language symbol extraction. `Extract(source, language, relPath)` dispatches to per-language extractors and sets `ExtractionConfidence` on each symbol (1.0 for Go/AST, 0.85 for stable regex languages, 0.70 for approximate ones). Go uses `go/ast`; all other languages use regex. `extractionConfidence` map controls per-language scores.

- **`internal/ast/languages.go`** — File extension → language detection and `IsSourceFile` filter.

- **`internal/cypher/engine.go`** — Cypher-to-SQL translation. Pipeline: `tokenize` → `parseQuery` → `run`. Three query paths: `runNodeScan` (no edge), `runJoinQuery` (single-hop, SQL JOIN), `runBFS` (variable-length, Go BFS loop). `symRow` struct and all SELECT queries must stay in sync with `db.go`'s `Symbol` fields — both have `extraction_confidence`.

- **`internal/index/indexer.go`** — Indexing pipeline. `Index()` walks files concurrently (goroutine per file, `sync.WaitGroup`), hashes with xxh3, skips unchanged files, calls `ast.Extract`, converts to `db.Symbol`/`db.Edge`, flushes in batches. Per-project mutex (`idx.active` map + `idx.mu`) prevents concurrent index of the same project. `Watch()` polls all projects every 2s (active) or 30s (idle).

- **`internal/server/server.go`** — MCP server. All 13 tools are registered in `registerTools()`. Every handler calls `jsonResultWithMeta()` which wraps the result in a `_meta` envelope and atomically increments session stats (`statsCalls`, `statsTokensUsed`, `statsTokensSaved`). `sessionID`/`sessionRoot` are set once via `sessionOnce` from the MCP roots list on connection.

### Symbol ID format

```
"{file_path}::{qualified_name}#{kind}"
e.g. "internal/db/db.go::db.Open#Function"
```

IDs are stable across re-indexing as long as file path and qualified name don't change. The ID is built by `db.MakeSymbolID()`.

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

- **Regex gap**: 19 non-Go languages use regex extraction (~80% accuracy). `extraction_confidence` field surfaces this to callers. Full fix = tree-sitter bindings (future sprint).
- **ID fragility on file moves**: Symbol IDs encode file path; moving `internal/auth/` → `pkg/identity/` breaks all IDs for that file. A `symbol_moves` tracking table is planned.
- **BFS N×depth SQL queries**: `runBFS` in `cypher/engine.go` issues one SQL query per node per depth level. Planned fix: replace with a single recursive CTE query.
