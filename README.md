# pincherMCP

[![CI](https://github.com/kwad77/pincherMCP/actions/workflows/ci.yml/badge.svg)](https://github.com/kwad77/pincherMCP/actions/workflows/ci.yml)
[![Go 1.24](https://img.shields.io/badge/go-1.24-blue.svg)](https://golang.org)
[![License: MIT](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

> The fastest, most token-efficient codebase intelligence server — single binary, no cloud dependencies, works with any LLM. Docker image available.

pincherMCP fuses the best ideas from three codebases into one lean Go binary:

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
Any LLM → HTTP REST API works with GPT-4, Gemini, Copilot, Cursor, CI/CD
```

Every tool response includes a `_meta` field so agents know exactly what they spent and saved:

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

Savings are now **persisted across sessions** — every reconnect accumulates into a running all-time total, giving enterprises proof of ROI over time.

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

e.g.  "internal/db/db.go::db.Open#Function"
      "src/auth/jwt.ts::AuthService.verify#Method"
```

Agents can persist these IDs in their context and retrieve source code instantly — even after the file changes. When a file is **renamed or moved**, pincherMCP records a `symbol_moves` redirect. The `symbol` tool transparently resolves stale IDs — agents never get a "not found" just because a file moved.

### Extraction Confidence

Every symbol carries an `extraction_confidence` score:

| Value | Languages |
|---|---|
| `1.0` | Go — `go/ast` full AST, exact byte offsets |
| `0.85` | Python, JavaScript, JSX, TypeScript, TSX, Rust, Java — stable regex |
| `0.70` | Ruby, PHP, C, C++, C#, Kotlin, Swift — approximate regex |

### Schema Migrations

The schema is versioned (`schema_version` table). Upgrading the binary applies any pending migrations automatically — **no data loss, no manual steps**. Currently at **v4** (adds the `sessions` table for persistent savings tracking).

---

## Tools (14 total)

### Indexing & Discovery

| Tool | What it does |
|---|---|
| `index` | Index a repo. One AST pass populates all three layers. Incremental by default (xxh3 content hash skips unchanged files). `force=true` to re-parse everything. |
| `list` | List all indexed projects with stats: files, symbols, edges, last indexed timestamp. |
| `changes` | Map `git diff` to affected symbols and compute blast radius. Scope: `unstaged` (default), `staged`, or `all`. Returns changed symbols + impacted callers with CRITICAL/HIGH/MEDIUM/LOW risk labels. |

### Symbol Retrieval

| Tool | What it does | Token savings |
|---|---|---|
| `symbol` | Retrieve source for one symbol by ID. O(1): 1 SQL lookup + 1 seek + 1 read. No re-parsing. | ~95% vs. reading whole file |
| `symbols` | Batch retrieve multiple symbols in one call. Use instead of calling `symbol` in a loop. | ~95% per symbol |
| `context` | Symbol + all its direct imports as a minimal bundle. ~90% token reduction vs. reading files. | ~90% |

### Search & Query

| Tool | What it does |
|---|---|
| `search` | FTS5 BM25 full-text search across names, signatures, and docstrings. Supports wildcards (`auth*`), phrases (`"process order"`), AND/OR. Filter by `kind`, `language`, or `fields` (selective projection). Use `project=*` to search all indexed repos in one call. |
| `query` | Execute Cypher-like graph queries. Sub-ms for single-hop patterns via SQL JOIN fusion. Variable-length paths via recursive CTE. Scoped to a single project. |
| `trace` | Call-path trace — who calls this function, or what does it call. BFS via recursive CTE. Returns hops grouped by depth with CRITICAL/HIGH/MEDIUM/LOW risk labels. |

### Architecture & Knowledge

| Tool | What it does |
|---|---|
| `architecture` | High-level orientation: language breakdown, entry points, hotspot functions (most-called), graph stats. Start here on an unfamiliar project. |
| `schema` | Knowledge graph schema: node kind counts, edge kind counts, total symbols and edges. Use before `query` to understand what's indexed. |
| `adr` | Architecture Decision Records — persistent key/value store per project. Actions: `get`, `set`, `list`, `delete`. Record stack decisions, patterns, conventions that survive context resets. |
| `health` | Diagnostic report: schema version, index staleness (time since last index), and per-language extraction coverage. Use to detect stale indexes before trusting results. |
| `stats` | Session and **all-time** savings summary: tokens used/saved, cost avoided, call count, avg latency. `all_time` persists across reconnects — enterprises can prove ROI over weeks/months. |

---

## Platform Agnostic — HTTP REST API

pincherMCP works with any LLM, IDE, or CI/CD pipeline — not just Claude Code.

Start with the `--http` flag to expose all 14 tools over REST:

```bash
# Unauthenticated (safe for localhost-only)
pincher --http :8080

# With bearer token auth (recommended for shared/remote deployments)
pincher --http :8080 --http-key mysecrettoken
```

Then call any tool from any HTTP client:

```bash
# Index a repository
curl -s -X POST http://localhost:8080/v1/index \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer mysecrettoken" \
  -H "Accept-Encoding: gzip" \
  -d '{"path": "/path/to/your/project"}' | jq .

# Search for symbols — field projection + gzip compression
curl -s -X POST http://localhost:8080/v1/search \
  -H "Content-Type: application/json" \
  -H "Accept-Encoding: gzip" \
  -d '{"query": "processPayment", "project": "myproject", "fields": "id,name,file_path"}' | jq .

# Search across ALL indexed repos in one call
curl -s -X POST http://localhost:8080/v1/search \
  -H "Content-Type: application/json" \
  -d '{"query": "processPayment", "project": "*"}' | jq .

# Execute a Cypher graph query
curl -s -X POST http://localhost:8080/v1/query \
  -H "Content-Type: application/json" \
  -d '{"cypher": "MATCH (f:Function)-[:CALLS]->(g) WHERE f.name = '\''main'\'' RETURN g.name LIMIT 10", "project": "myproject"}' | jq .

# List all indexed projects (idiomatic GET — no body needed)
curl http://localhost:8080/v1/projects | jq .

# Auto-discover the full API spec (Postman/Cursor-importable)
curl http://localhost:8080/v1/openapi.json | jq .

# Liveness probe — no auth required (for K8s / load balancers)
curl http://localhost:8080/v1/health
```

Responses are gzip-compressed when `Accept-Encoding: gzip` is sent — typically ~65% smaller payloads.

**Works with:**
- OpenAI / GPT-4 function calling
- Google Gemini tool use
- GitHub Copilot / Cursor custom tools (import `GET /v1/openapi.json`)
- Any CI/CD pipeline (GitHub Actions, Jenkins, etc.)
- Browser-based tools (CORS headers included)
- Custom integrations — any language that can make HTTP requests

The stdio MCP transport (for Claude Code) and HTTP server run simultaneously — no either/or.

---

## Cypher Query Examples

pincherMCP supports a lightweight Cypher subset translated to SQL at query time.

```cypher
-- Find all functions whose name matches a regex
MATCH (f:Function) WHERE f.name =~ '.*Handler.*' RETURN f.name, f.file_path

-- Find what main() calls (single-hop JOIN, sub-ms)
MATCH (f:Function)-[:CALLS]->(g) WHERE f.name = 'main' RETURN g.name, g.file_path LIMIT 20

-- Find call chains up to 3 hops deep (recursive CTE)
MATCH (a)-[:CALLS*1..3]->(b) WHERE a.name = 'ProcessOrder' AND b.kind = 'Function' RETURN b.name

-- Count functions by language
MATCH (f:Function) RETURN COUNT(f) AS total

-- Find all exported Go functions
MATCH (f:Function) WHERE f.language = 'Go' RETURN f.name LIMIT 50

-- Named edge variables (access edge metadata)
MATCH (a:Function)-[r:CALLS]->(b:Function) WHERE a.name = 'main' RETURN a.name, r.kind, r.confidence, b.name

-- Sort results by line number
MATCH (f:Function) WHERE f.file_path STARTS WITH 'internal/' RETURN f.name, f.start_line ORDER BY f.start_line ASC
```

**Supported operators:** `=`, `<>`, `>`, `<`, `>=`, `<=`, `=~` (regex), `CONTAINS`, `STARTS WITH`

All `query` and `trace` calls are scoped to a single project — cross-project data leakage is impossible. Use `search` with `project=*` for intentional cross-repo discovery.

---

## Token Savings in Detail

### Per-call savings

Every response includes a `_meta` field:

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
- `search` vs. asking the model to scan files: **~98% fewer tokens**
- `trace` vs. asking the model to follow call chains manually: **~99% fewer tokens**

### Search field projection

When you only need IDs to feed into `symbol` or `context`, use the `fields` parameter to return only what you need:

```
search query="processPayment" fields="id,name,file_path"
```

Returns ~80% fewer tokens per result compared to the full 10-field default.

### All-time ROI tracking

The `stats` tool now shows cumulative savings across every session:

```json
{
  "session": {
    "calls": 47,
    "tokens_saved": 284000,
    "total_cost_avoided": "$0.8520"
  },
  "all_time": {
    "calls": 3847,
    "tokens_saved": 18400000,
    "total_cost_avoided": "$55.20"
  }
}
```

These numbers persist across reconnects, process restarts, and binary upgrades — giving you (and your stakeholders) a durable, provable measure of cost avoided.

---

## Installation

### Requirements

- Go 1.24+ (pure Go — no CGO, no C compiler needed)
- Git (for the `changes` blast-radius tool)

### Build from source

```bash
git clone https://github.com/kwad77/pincherMCP
cd pincherMCP
go build -o pincher ./cmd/pinch/         # Linux/macOS
go build -o pincher.exe ./cmd/pinch/     # Windows
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

### Use as an HTTP server (any LLM / CI/CD)

```bash
# Run with both stdio MCP and HTTP REST
pincher --http :8080

# With bearer token auth (recommended for shared deployments)
pincher --http :8080 --http-key mysecrettoken

# Or HTTP-only (no Claude Code needed)
pincher --http :8080 --data-dir /var/pincher
```

Point any OpenAI-compatible tool at `http://localhost:8080/v1/`. Import `GET /v1/openapi.json` directly into Postman or Cursor for auto-generated typed calls.

### Docker

```bash
# Pull and run
docker build -t pincher .
docker run -p 8080:8080 -v /my/data:/data pincher

# With auth key
docker run -p 8080:8080 -v /my/data:/data pincher --http :8080 --http-key mysecrettoken

# Mount a repo for indexing
docker run -p 8080:8080 \
  -v /my/data:/data \
  -v /path/to/repo:/repo:ro \
  pincher
```

---

## Usage

### First time: index a project

```
Use pincher index with path "/path/to/your/project"
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
  "duration_ms": 340
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

### Track savings over time

```
Use pincher stats
```

Returns the current session stats **and** the all-time aggregate from every previous session.

---

## Data Storage

pincherMCP stores its database in a platform-appropriate directory — no root permissions required:

| Platform | Location |
|---|---|
| Windows | `%APPDATA%\pincherMCP\pincher.db` |
| macOS | `~/Library/Application Support/pincherMCP/pincher.db` |
| Linux | `~/.local/share/pincherMCP/pincher.db` |

Override with `--data-dir /custom/path`.

The database is a single SQLite WAL file (schema v4). Back it up with any file copy tool. Delete it to reset all indexes. The schema is versioned — upgrading the binary applies any pending migrations automatically, no data loss.

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

Go uses the standard library's `go/ast` parser for exact byte offsets. All other extracting languages use regex patterns calibrated for accuracy. To upgrade any language to full accuracy, replace its extractor with tree-sitter bindings — the interface is unchanged.

---

## Performance

All numbers on a ~5,000-file Go monorepo (MacBook M2):

| Operation | Time | Notes |
|---|---|---|
| Full index (cold) | ~800ms | Concurrent per-file goroutines, xxh3 content hashing |
| Incremental re-index (1 file changed) | ~15ms | Hash check skips unchanged files |
| Symbol retrieval (`symbol` tool) | <1ms | 1 SQL + 1 seek + 1 read |
| FTS5 search (`search` tool) | <5ms | BM25 ranking via SQLite FTS5 |
| Single-hop Cypher query | <2ms | JOIN-fused SQL, no round trips |
| Multi-hop BFS query (depth 3) | <5ms | Recursive CTE — 1 SQL per start node |
| Session stats flush | every 60s | Background goroutine, non-blocking |

---

## Architecture Decision Records

Use the `adr` tool to record project-level decisions that survive context resets:

```
Use pincher adr with action "set", key "STACK", value "Go 1.24 + SQLite + MCP stdio"
Use pincher adr with action "set", key "AUTH", value "JWT, 24h expiry, RS256 keys in /etc/keys"
Use pincher adr with action "list"
```

These persist in SQLite and are retrievable in any future session, from any connected client.

---

## Development

```
pincherMCP/
├── cmd/pinch/main.go            # Entry point — flags, wires db + indexer + server
├── Dockerfile                   # Multi-stage scratch image (~10MB), CGO_ENABLED=0
├── internal/
│   ├── db/db.go                 # SQLite store: schema v4, migrations, CRUD, FTS5, graph ops, sessions
│   ├── ast/
│   │   ├── extractor.go         # Multi-language symbol extraction with byte offsets + confidence scores
│   │   └── languages.go         # Extension → language detection (20+ languages)
│   ├── cypher/engine.go         # Cypher-to-SQL translation (tokenizer → parser → 3 query paths)
│   ├── index/indexer.go         # Indexing pipeline: walk → hash → extract → store → watch → move tracking
│   └── server/server.go         # 14 MCP tools, HTTP REST gateway, gzip, OpenAPI spec, bearer auth, session persistence
└── go.mod
```

### CLI flags

```
pincher --version                    # print version and exit
pincher --data-dir /custom/path      # override database directory
pincher --verbose                    # enable verbose logging to stderr
pincher --http :8080                 # also listen for HTTP REST on :8080
pincher --http-key mysecrettoken     # require bearer token on all HTTP requests
pincher --http-rate 60               # rate limit: 60 requests/IP/minute (0 = unlimited)
```

### Test coverage

```bash
go test ./...                                         # run all tests
go test ./... -coverprofile=cover.out                 # with coverage
go tool cover -func=cover.out | grep "^total"         # total: ~90%
go test ./internal/db/ -run TestGraphStats_WithData -v  # single test
```

Current coverage: **~90%** across all packages.

---

## License

MIT
