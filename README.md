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

Every tool response includes a `_meta` envelope so agents can track exactly how many tokens they consumed and how many they saved:

```json
{
  "result": { "...": "..." },
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
│                     (64 MB page cache)                      │
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
│  │  1 Read       │  │  BFS traversal│  │  results      │  │
│  │  O(1)         │  │               │  │               │  │
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

e.g.  "internal/db/db.go::db.Open#Function"
      "src/auth/jwt.ts::AuthService.verify#Method"
```

Agents can persist these IDs in their context and look up source code instantly — even after the file changes (as long as the symbol name doesn't change).

---

## Tools (13 total)

### Indexing & Discovery

| Tool | What it does |
|---|---|
| `index` | Index a repo. One AST pass populates all three layers. Incremental by default (xxh3 content hash skips unchanged files). |
| `list` | List all indexed projects with stats (files, symbols, edges, last indexed). |
| `changes` | Map `git diff` to affected symbols and compute blast radius via BFS. Returns changed symbols + impacted callers with CRITICAL/HIGH/MEDIUM/LOW risk labels. |

### Symbol Retrieval

| Tool | What it does | Token savings |
|---|---|---|
| `symbol` | Retrieve source for one symbol by ID. O(1): 1 SQL lookup + 1 seek + 1 read. No re-parsing. | ~95% vs. reading whole file |
| `symbols` | Batch retrieve multiple symbols in one call. Use instead of calling `symbol` in a loop. | ~95% per symbol |
| `context` | Symbol + all its direct imports as a minimal bundle. ~90% token reduction vs. reading files. | ~90% |

### Search

| Tool | What it does |
|---|---|
| `search` | FTS5 BM25 full-text search across names, signatures, and docstrings. Supports wildcards (`auth*`), phrases (`"process order"`), AND/OR. Filter by kind or language. |
| `query` | Execute Cypher-like graph queries. Sub-ms for single-hop patterns via SQL JOIN fusion. |
| `trace` | BFS call-path trace — who calls this function, or what does it call. Returns hops with risk labels. |

### Architecture & Knowledge

| Tool | What it does |
|---|---|
| `architecture` | High-level orientation: languages, entry points, hotspot functions (most-called), graph stats. Call this first on an unfamiliar project. |
| `schema` | Knowledge graph schema: node kind counts, edge kind counts. Use before `query` to understand what's indexed. |
| `adr` | Architecture Decision Records — persistent key/value store per project. `get`, `set`, `list`, `delete`. Record stack decisions, patterns, conventions. |
| `stats` | Session savings summary: cumulative tokens used/saved, cost avoided, call count, and avg latency since server start. Also shows the current project's index size (files, symbols, edges). |

---

## Cypher Query Examples

pincherMCP supports a lightweight Cypher subset translated to SQL at query time.

```cypher
-- Find all functions whose name contains "Handler"
MATCH (f:Function) WHERE f.name =~ '.*Handler.*' RETURN f.name, f.file_path

-- Find what main() calls (one hop)
MATCH (f:Function)-[:CALLS]->(g) WHERE f.name = 'main' RETURN g.name, g.file_path LIMIT 20

-- Find call chains up to 3 hops deep
MATCH (a)-[:CALLS*1..3]->(b) WHERE a.name = 'ProcessOrder' RETURN b.name, b.kind

-- Count functions by language
MATCH (f:Function) RETURN COUNT(f) AS total

-- Find all exported Go functions
MATCH (f:Function) WHERE f.language = 'Go' AND f.is_exported = 'true' RETURN f.name LIMIT 50
```

**Supported operators:** `=`, `<>`, `>`, `<`, `>=`, `<=`, `=~` (regex), `CONTAINS`, `STARTS WITH`

---

## Installation

### Requirements

- Go 1.21+ (pure Go — no CGO, no C compiler needed)
- Git (for the `changes` blast-radius tool)

### Build from source

```bash
git clone https://github.com/yourorg/pincherMCP
cd pincherMCP
go build -o pinch ./cmd/pinch

# Windows
go build -o pinch.exe ./cmd/pinch
```

### Add to Claude Code

Edit `~/.claude/mcp.json`:

```json
{
  "mcpServers": {
    "pincher": {
      "type": "stdio",
      "command": "/path/to/pinch"
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
      "command": "C:\\path\\to\\pinch.exe"
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
  "files": 142,
  "symbols": 3847,
  "edges": 12094,
  "skipped": 0,
  "duration_ms": 340
}
```

### Find a function

```
Use pincher search with query "processPayment"
```

Returns ranked results with stable IDs. Copy an ID, then:

```
Use pincher symbol with id "src/payments/processor.go::payments.processPayment#Function"
```

Returns source code, signature, byte offsets — instantly, without re-parsing.

### Understand call chains

```
Use pincher trace with name "processPayment" and direction "inbound"
```

Returns every function that (transitively) calls `processPayment`, labeled:
- **CRITICAL** — direct callers (depth 1)
- **HIGH** — callers of callers (depth 2)
- **MEDIUM** — depth 3
- **LOW** — depth 4+

### Assess impact of a change

```
Use pincher changes
```

Runs `git diff`, finds all symbols in changed files, BFS-traces inbound callers, returns a blast radius report with risk labels and counts.

---

## Data Storage

pincherMCP stores its database in a platform-appropriate directory — no root permissions required:

| Platform | Location |
|---|---|
| Windows | `%APPDATA%\pincherMCP\pincher.db` |
| macOS | `~/Library/Application Support/pincherMCP/pincher.db` |
| Linux | `~/.local/share/pincherMCP/pincher.db` |

The database is a single SQLite WAL file. Back it up with any file copy tool. Delete it to reset all indexes.

---

## Language Support

pincherMCP extracts symbols from 20+ languages:

| Language | Extraction method | Byte offsets |
|---|---|---|
| Go | `go/ast` (full AST) | Exact (token.Pos) |
| Python | Regex | Approximate |
| TypeScript / TSX | Regex | Approximate |
| JavaScript / JSX | Regex | Approximate |
| Rust | Regex | Approximate |
| Java | Regex | Approximate |
| C / C++ | Regex | Approximate |
| C# | Regex | Approximate |
| Ruby | Regex | Approximate |
| PHP | Regex | Approximate |
| Kotlin | Regex | Approximate |
| Swift | Regex | Approximate |
| Scala, Lua, Zig, Elixir, Haskell, Dart, Bash, R | Language detected | — |

Go uses the standard library's `go/ast` parser for exact byte offsets. All other languages use regex patterns covering the most common symbol forms (~80% accuracy). To upgrade any language to full accuracy, replace its extractor with tree-sitter bindings — the interface is unchanged.

---

## Performance

All numbers on a ~5,000-file Go monorepo (MacBook M2):

| Operation | Time | Notes |
|---|---|---|
| Full index (cold) | ~800ms | Concurrent per-file, xxh3 hashing |
| Incremental re-index (1 file changed) | ~15ms | Hash check skips unchanged files |
| Symbol retrieval (`symbol` tool) | <1ms | 1 SQL + 1 seek + 1 read |
| FTS5 search (`search` tool) | <5ms | BM25 ranking via SQLite FTS5 |
| Single-hop Cypher query | <2ms | JOIN-fused SQL |
| Multi-hop BFS query (depth 3) | <20ms | Graph BFS in Go |

---

## Token Efficiency

Every response includes a `_meta` envelope showing token consumption:

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
Use pincher adr with action "set", key "STACK", value "Go 1.21 + SQLite + MCP stdio"
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
│   ├── db/db.go                 # SQLite store: schema, CRUD, FTS5, graph ops
│   ├── ast/
│   │   ├── extractor.go         # Multi-language symbol extraction with byte offsets
│   │   └── languages.go         # Extension → language detection
│   ├── cypher/engine.go         # Cypher-to-SQL translation (tokenizer → parser → executor)
│   ├── index/indexer.go         # Indexing pipeline: walk → hash → extract → store → watch
│   └── server/server.go         # MCP server: 12 tools + _meta envelope
└── go.mod
```

### CLI flags

```
pinch --help
pinch --version
pinch --data-dir /custom/path    # override database directory
pinch --verbose                  # enable verbose logging to stderr
```

---

## License

MIT
