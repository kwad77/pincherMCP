# pincherMCP

> The fastest, most token-efficient MCP server for codebase intelligence — single binary, no Docker, no cloud dependencies.

pincherMCP fuses the best ideas from three codebases into one:

| Source | Innovation borrowed |
|---|---|
| [codebase-memory-mcp](https://github.com/nicholasgasior/codebase-memory-mcp) | Knowledge graph, Cypher queries, incremental re-index |
| [jcodemunch-mcp](https://github.com/jgravelle/jcodemunch-mcp) | Byte-offset O(1) symbol retrieval, `_meta` token budget envelope |
| [Code-Index-MCP](https://github.com/ViperJuice/Code-Index-MCP) | FTS5 BM25 full-text search, file-watch auto-reindex |

All three indexes — **byte-offset store**, **knowledge graph**, **FTS5 search** — are populated in a **single AST parse pass** from one shared `symbols` table. No duplication, no sync overhead.

---

## Why pincherMCP?

### The problem with existing tools

- **codebase-memory-mcp** — great graph queries, but no byte-offset retrieval (re-parses files on every read) and no full-text search
- **jcodemunch-mcp** — brilliant O(1) symbol retrieval, but Python overhead, no graph traversal, full re-parse on every change
- **Code-Index-MCP** — excellent FTS5 search, but requires Docker, Python, and optionally a paid Qdrant/OpenAI API

### What pincherMCP does differently

```
One parse → three indexes, zero overhead
One binary → no Docker, no Python, no external services
One response → always includes token cost metadata
```

Every tool response includes a `_meta` field at the top level so agents can track exactly how many tokens they consumed and how many they saved:

```json
{
  "name": "mySymbol",
  "source": "func mySymbol() { ... }",
  "_meta": {
    "tokens_used":  312,
    "tokens_saved": 14800,
    "latency_ms":   2,
    "cost_avoided": "$0.0444"
  }
}
```

---

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                        SQLite WAL                           │
│              (64 MB page cache, busy_timeout=5s)            │
│                                                             │
│  ┌───────────────┐  ┌───────────────┐  ┌───────────────┐  │
│  │  Layer 1      │  │  Layer 2      │  │  Layer 3      │  │
│  │  Byte-Offset  │  │  Knowledge    │  │  FTS5 BM25    │  │
│  │  Symbol Store │  │  Graph        │  │  Full-Text    │  │
│  │               │  │  (Cypher)     │  │  Search       │  │
│  │  start_byte   │  │  symbols +    │  │  symbols_fts  │  │
│  │  end_byte     │  │  edges        │  │  virtual      │  │
│  │  ↓            │  │  ↓            │  │  table        │  │
│  │  1 SQL lookup │  │  sub-ms JOIN  │  │  ↓            │  │
│  │  1 Seek       │  │  fusion       │  │  BM25 ranked  │  │
│  │  1 Read       │  │  Recursive    │  │  results      │  │
│  │  O(1)         │  │  CTE traversal│  │               │  │
│  └───────────────┘  └───────────────┘  └───────────────┘  │
│          ↑                  ↑                  ↑            │
│          └──────────────────┴──────────────────┘            │
│                    Single symbols row                        │
│              (populated in one AST parse pass)              │
└─────────────────────────────────────────────────────────────┘
```

### Stable Symbol IDs

Every symbol gets a stable, human-readable ID that survives re-indexing:

```
"{file_path}::{qualified_name}#{kind}"

e.g.  "internal/db/db.go::db.*Store.Open#Method"
      "src/auth/jwt.ts::AuthService.verify#Method"
```

Agents can persist these IDs in their context and look up source code instantly — even after the file changes (as long as the symbol name doesn't change).

When a file is **renamed or moved**, pincherMCP records a `symbol_moves` entry mapping the old ID to the new one. The `symbol` tool transparently redirects stale IDs — agents never get a "not found" error just because a file moved.

### Extraction Confidence

Every symbol carries an `extraction_confidence` score:

| Value | Languages |
|---|---|
| `1.0` | Go — `go/ast` full AST, exact byte offsets |
| `0.85` | Python, JavaScript, JSX, TypeScript, TSX, Rust, Java — stable regex |
| `0.70` | Ruby, PHP, C, C++, C#, Kotlin, Swift — approximate regex |

Languages in the "detected" category (Scala, Lua, Zig, Elixir, Haskell, Dart, Bash, R) are recognized for file filtering but **no symbols are extracted** from them — those files are skipped during indexing.

### Schema Migrations

The database schema is versioned. New columns and tables are added via append-only migrations tracked in a `schema_version` table — existing databases upgrade automatically on next `pinch` startup without data loss.

---

## Tools (13 total)

### Indexing & Discovery

| Tool | What it does |
|---|---|
| `index` | Index a repo. One AST pass populates all three layers. Incremental by default (xxh3 content hash skips unchanged files). |
| `list` | List all indexed projects with stats (files, symbols, edges, last indexed). |
| `changes` | Map `git diff` to affected symbols and compute blast radius. Scope: `unstaged` (default), `staged`, or `all`. Returns changed symbols + impacted callers with CRITICAL/HIGH/MEDIUM/LOW risk labels. |

### Symbol Retrieval

| Tool | What it does | Token savings |
|---|---|---|
| `symbol` | Retrieve source for one symbol by ID. O(1): 1 SQL lookup + 1 seek + 1 read. No re-parsing. | ~95% vs. reading whole file |
| `symbols` | Batch retrieve multiple symbols in one call. Use instead of calling `symbol` in a loop. | ~95% per symbol |
| `context` | Symbol + all its direct imports as a minimal bundle. ~90% token reduction vs. reading files. | ~90% |

### Search

| Tool | What it does |
|---|---|
| `search` | FTS5 BM25 full-text search across names, signatures, and docstrings. Supports wildcards (`auth*`), phrases (`"process order"`), AND/OR. Filter by `kind` or `language`. |
| `query` | Execute Cypher-like graph queries. Sub-ms for single-hop patterns via SQL JOIN fusion. Variable-length via recursive CTE. |
| `trace` | Call-path trace — who calls this function, or what does it call. Uses recursive CTE (max 2 SQL calls). Returns hops grouped by depth with risk labels. |

### Architecture & Knowledge

| Tool | What it does |
|---|---|
| `architecture` | High-level orientation: language breakdown, entry points, hotspot functions (most-called), graph stats. Call this first on an unfamiliar project. |
| `schema` | Knowledge graph schema: node kind counts, edge kind counts. Use before `query` to understand what's indexed. |
| `adr` | Architecture Decision Records — persistent key/value store per project. Actions: `get`, `set`, `list`, `delete`. Record stack decisions, patterns, conventions. |
| `stats` | Session savings summary: cumulative tokens used/saved, cost avoided, call count, avg latency since server start. Also shows the current project's index size. |

---

## Cypher Query Examples

pincherMCP supports a lightweight Cypher subset translated to SQL at query time.

```cypher
-- Find all functions whose name matches a regex
MATCH (f:Function) WHERE f.name =~ '.*Handler.*' RETURN f.name, f.file_path

-- Find what main() calls (single-hop JOIN)
MATCH (f:Function)-[:CALLS]->(g) WHERE f.name = 'main' RETURN g.name, g.file_path LIMIT 20

-- Find call chains up to 3 hops deep (recursive CTE), filter result nodes
MATCH (a)-[:CALLS*1..3]->(b) WHERE a.name = 'ProcessOrder' AND b.kind = 'Function' RETURN b.name, b.kind

-- Count functions by language
MATCH (f:Function) RETURN COUNT(f) AS total

-- Find all exported Go functions
MATCH (f:Function) WHERE f.language = 'Go' AND f.is_exported = 'true' RETURN f.name LIMIT 50

-- Named edge variables (access edge metadata)
MATCH (a:Function)-[r:CALLS]->(b:Function) WHERE a.name = 'main' RETURN a.name, r.kind, r.confidence, b.name

-- Sort results by line number
MATCH (f:Function) WHERE f.file_path STARTS WITH 'internal/' RETURN f.name, f.start_line ORDER BY f.start_line ASC
```

**Supported operators:** `=`, `<>`, `>`, `<`, `>=`, `<=`, `=~` (regex), `CONTAINS`, `STARTS WITH`

WHERE filters apply to both the start node and result nodes in all query modes (single-hop JOIN, variable-length BFS, and node-only scans).

---

## Installation

### Requirements

- Go 1.24+ (pure Go — no CGO, no C compiler needed)
- Git (for the `changes` blast-radius tool)

### Build from source

```bash
git clone https://github.com/kwad77/pincherMCP
cd pincherMCP
go build -o pincher ./cmd/pinch

# Windows
go build -o pincher.exe ./cmd/pinch
```

### Add to Claude Code

Edit `~/.claude/mcp.json`:

```json
{
  "mcpServers": {
    "pincher": {
      "type": "stdio",
      "command": "/path/to/pincher"
    }
  }
}
```

### Add to Claude Desktop

Edit `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) or `%APPDATA%\Claude\claude_desktop_config.json` (Windows):

```json
{
  "mcpServers": {
    "pincher": {
      "type": "stdio",
      "command": "C:\\path\\to\\pincher.exe"
    }
  }
}
```

---

## Usage

### First time: index a project

```
Use the pincher index tool with path "/path/to/your/project"
```

Returns:

```json
{
  "project": "myproject",
  "path": "/path/to/your/project",
  "files": 142,
  "symbols": 3847,
  "edges": 12094,
  "skipped": 0,
  "duration_ms": 340,
  "_meta": { "tokens_used": 56, "tokens_saved": 0, "latency_ms": 340, "cost_avoided": "$0.0000" }
}
```

### Find a function

```
Use pincher search with query "processPayment"
```

Returns BM25-ranked results with stable IDs. Copy an ID, then:

```
Use pincher symbol with id "src/payments/processor.go::payments.processPayment#Function"
```

Returns source code, signature, byte offsets, and `extraction_confidence` — instantly, without re-parsing.

### Understand call chains

```
Use pincher trace with name "processPayment" and direction "inbound"
```

Returns every function that (transitively) calls `processPayment`, grouped by depth:
- **CRITICAL** — direct callers (depth 1)
- **HIGH** — callers of callers (depth 2)
- **MEDIUM** — depth 3
- **LOW** — depth 4+

### Assess impact of a change

```
Use pincher changes with scope "unstaged"
```

Runs `git diff`, finds all symbols in changed files, traces inbound callers, returns a blast radius report with risk labels and counts.

---

## Data Storage

pincherMCP stores its database in a platform-appropriate directory — no root permissions required:

| Platform | Location |
|---|---|
| Windows | `%APPDATA%\pincherMCP\pincher.db` |
| macOS | `~/Library/Application Support/pincherMCP/pincher.db` |
| Linux | `~/.local/share/pincherMCP/pincher.db` |

The database is a single SQLite WAL file. Back it up with any file copy tool. Delete it to reset all indexes. The schema is versioned — upgrading the binary applies any pending migrations automatically.

---

## Language Support

pincherMCP extracts symbols from 12 languages and detects (but does not index) 8 more:

| Language | Extraction | Confidence | Symbols extracted |
|---|---|---|---|
| Go | `go/ast` full AST | 1.0 | Functions, Methods, Types, Interfaces, Constants, Variables |
| Python | Regex | 0.85 | Functions, Classes, Methods |
| TypeScript / TSX | Regex | 0.85 | Functions, Classes, Interfaces, Methods |
| JavaScript / JSX | Regex | 0.85 | Functions, Classes, Methods |
| Rust | Regex | 0.85 | Functions, Structs, Traits, Impls |
| Java | Regex | 0.85 | Classes, Methods, Interfaces |
| Ruby | Regex | 0.70 | Functions, Classes, Methods |
| PHP | Regex | 0.70 | Functions, Classes, Methods |
| C / C++ | Regex | 0.70 | Functions, Structs, Classes |
| C# | Regex | 0.70 | Classes, Methods, Interfaces |
| Kotlin | Regex | 0.70 | Functions, Classes |
| Swift | Regex | 0.70 | Functions, Classes |
| Scala, Lua, Zig, Elixir, Haskell, Dart, Bash, R | Detected only | — | None (files skipped) |

Go uses the standard library's `go/ast` parser for exact byte offsets. All other extracting languages use regex patterns. To upgrade any language to full accuracy, replace its extractor with tree-sitter bindings — the interface is unchanged.

---

## Performance

All numbers on a ~5,000-file Go monorepo (MacBook M2):

| Operation | Time | Notes |
|---|---|---|
| Full index (cold) | ~800ms | Concurrent per-file goroutines, xxh3 content hashing |
| Incremental re-index (1 file changed) | ~15ms | Hash check skips unchanged files |
| Symbol retrieval (`symbol` tool) | <1ms | 1 SQL + 1 seek + 1 read |
| FTS5 search (`search` tool) | <5ms | BM25 ranking via SQLite FTS5 |
| Single-hop Cypher query | <2ms | JOIN-fused SQL |
| Multi-hop BFS query (depth 3) | <5ms | Recursive CTE — 1 SQL per start node |
| Concurrent write safety | 5s retry | `busy_timeout=5000` prevents lock contention errors |

---

## Token Efficiency

Every response includes a `_meta` field showing token consumption:

```json
"_meta": {
  "tokens_used":  312,
  "tokens_saved": 14800,
  "latency_ms":   2,
  "cost_avoided": "$0.0444"
}
```

**Typical savings:**
- `symbol` vs. reading a whole file: **~95% fewer tokens**
- `context` vs. reading a file and its imports: **~90% fewer tokens**
- `search` vs. asking Claude to scan files: **~98% fewer tokens**
- `trace` vs. asking Claude to manually follow call chains: **~99% fewer tokens**

---

## Architecture Decision Records

Use the `adr` tool to record project-level decisions that survive context resets:

```
Use pincher adr with action "set", key "STACK", value "Go 1.24 + SQLite + MCP stdio"
Use pincher adr with action "set", key "AUTH", value "JWT, 24h expiry, RS256 keys in /etc/keys"
Use pincher adr with action "list"
```

These persist in the SQLite database and are retrievable in any future session.

---

## Development

```
pincherMCP/
├── cmd/pinch/main.go            # Entry point — wires db + indexer + server
├── internal/
│   ├── db/db.go                 # SQLite store: schema, versioned migrations, CRUD, FTS5, graph ops
│   ├── ast/
│   │   ├── extractor.go         # Multi-language symbol extraction with byte offsets + confidence scores
│   │   └── languages.go         # Extension → language detection (20+ languages)
│   ├── cypher/engine.go         # Cypher-to-SQL translation (tokenizer → parser → executor)
│   ├── index/indexer.go         # Indexing pipeline: walk → hash → extract → store → watch → move tracking
│   └── server/server.go         # MCP server: 13 tools + _meta envelope
└── go.mod
```

### CLI flags

```
pinch -version                   # print version and exit
pinch -data-dir /custom/path     # override database directory
pinch -verbose                   # enable verbose logging to stderr
```

---

## License

MIT
