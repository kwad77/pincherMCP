<div align="center">
  <img src="assets/banner.png" alt="pincherMCP — pixel-art mascot Pinchy the crab holding a copper penny, wordmark, and tagline" width="900"/>
</div>

<div align="center">

[![CI](https://github.com/kwad77/pincher/actions/workflows/ci.yml/badge.svg)](https://github.com/kwad77/pincher/actions/workflows/ci.yml)
[![Go 1.25](https://img.shields.io/badge/go-1.25-00ADD8?logo=go&logoColor=white)](https://golang.org)
[![License: MIT](https://img.shields.io/badge/license-MIT-22c55e.svg)](LICENSE)
[![Coverage](https://img.shields.io/badge/coverage-84%25-22c55e.svg)](docs/REFERENCE.md#test-coverage)

**Codebase intelligence server for LLM agents.**
Single binary · No cloud dependencies · Any LLM · MCP stdio or HTTP REST

</div>

---

## What it does

pincherMCP is a single Go binary that indexes a codebase into three co-located layers — byte-offset symbol store, knowledge graph, and FTS5 full-text search — and exposes all three through **16 MCP tools** or an HTTP REST API.

Every tool response includes a `_meta` envelope with real BPE token counts (cl100k_base — exact for Claude and OpenAI families, approximate for Gemini/Llama), latency, and cost avoided:

```json
{
  "name": "processPayment",
  "source": "func processPayment(amount float64) error { ... }",
  "_meta": {
    "tokens_used":  312,
    "tokens_saved": 14500,
    "latency_ms":   2,
    "cost_avoided": "$0.0435"
  }
}
```

Token savings accumulate in SQLite across sessions — every reconnect adds to a running all-time total. All three indexes are populated in a **single AST parse pass** from one shared `symbols` table; no duplication, no sync overhead.

> **Looking for the manual?** → [`docs/REFERENCE.md`](docs/REFERENCE.md) is the long-form reference: every tool, every flag, every endpoint, schema history, performance numbers, project layout. This README sticks to pitch + quickstart.

---

## Quick Start

```bash
# 1. Install
go install github.com/kwad77/pincher/cmd/pinch@latest      # if Go 1.25+ on PATH
# or download a release binary:
#   https://github.com/kwad77/pincher/releases/latest
# or build from source:
#   git clone https://github.com/kwad77/pincher && cd pincher
#   go build -o pincher ./cmd/pinch/      # or pincher.exe on Windows

# 2. Drop the policy block into your project's CLAUDE.md (one-time)
pincher init                             # writes ./CLAUDE.md
pincher init --global                    # writes ~/.claude/CLAUDE.md

# 3. Index your project
pincher index /path/to/your/project

# 4. Point your MCP client at the binary (Claude Code / Cursor / Zed examples below)
#    Or open the dashboard: pincher web
```

### Client configuration

pincher speaks the standard JSON-RPC 2.0 MCP protocol over stdio. The `command` field is the same everywhere — only the file location and key name change.

<details>
<summary><b>Claude Code</b> — <code>~/.claude/mcp.json</code></summary>

```json
{
  "mcpServers": {
    "pincher": { "type": "stdio", "command": "/path/to/pincher" }
  }
}
```
</details>

<details>
<summary><b>Cursor</b> — <code>~/.cursor/mcp.json</code></summary>

```json
{
  "mcpServers": {
    "pincher": { "command": "/path/to/pincher" }
  }
}
```
</details>

<details>
<summary><b>Zed</b> — <code>settings.json</code> under <code>context_servers</code></summary>

```json
{
  "context_servers": {
    "pincher": {
      "command": { "path": "/path/to/pincher", "args": [] }
    }
  }
}
```
</details>

Continue, Windsurf, and any MCP-compatible client follow the same pattern. For editors without MCP, use the [HTTP REST API](docs/REFERENCE.md#http-rest-api).

For managed installs (Homebrew, systemd, launchd, Windows service, Docker), see [`packaging/README.md`](packaging/README.md).

### Tutorials

End-to-end walkthroughs (~10 min each):

- **[Claude Code](docs/tutorials/claude-code.md)** — install → index → `pincher init` → wire MCP → first query.
- **[Cursor](docs/tutorials/cursor.md)** — same flow with `pincher init --target=cursor` and Cursor's `.mdc` rules format.
- **[HTTP dashboard](docs/tutorials/http-dashboard.md)** — `pincher --http`, dashboard panels, REST API with `curl`, reverse-proxy notes.

---

## Why it's fast

**Three indexes, one AST pass.** A single `ast.Extract()` call per file populates all three layers. No background sync. No drift between graph and search.

```
   Source File                 ┌───────────────┐    ┌──────────────┐    ┌─────────────────┐
        │                      │  Layer 1      │    │  Layer 2     │    │  Layer 3 — FTS5 │
   ast.Extract()  ─────────►   │  Byte-offset  │    │  Knowledge   │    │  BM25 search    │
        │                      │  symbol store │    │  graph       │    │  per-corpus     │
        ▼                      │  O(1), <1 ms  │    │  <2 ms       │    │  routing        │
   one symbols row             └───────────────┘    └──────────────┘    └─────────────────┘
```

**Per-corpus FTS5.** Source-code identifiers, config keys, and Markdown sections live in three separate BM25 indexes (`symbols_{code,config,docs}_fts`). The `search` tool defaults to `corpus=code` so identifier searches aren't diluted by lockfile keys.

**Per-symbol confidence.** Lockfile keys score ~0.4–0.6, real config ~0.95–1.0. `search` defaults to `min_confidence=0.7` so noise drops out automatically.

**Reader pool.** SQLite WAL gives concurrent readers; pincher uses a separate read-only connection pool (`--db-readers`, capped at 32) so a busy MCP session can't block the writer.

Measured on this codebase (13 files, 618 symbols, 5,785 edges): cold index 190 ms, single-hop Cypher 2 ms, BFS depth 3 <5 ms, FTS5 search 1 ms. Full benchmark + methodology in [REFERENCE.md → Performance](docs/REFERENCE.md#performance).

---

## Token savings

The `stats` tool renders a session summary directly in chat:

```
┌────────────────────────────────────────────┐
│                  SESSION                   │
│  Tool calls:          5                    │
│  Without pincher:   ~45,200 tokens         │
│  With pincher:        1,200 tokens         │
│  Saved:             ~44,000 tokens   37x   │
│  Cost avoided:        $0.1320              │
│  Avg latency:         2 ms                 │
└────────────────────────────────────────────┘
```

**Without pincher** is the estimated baseline (whole file reads). **With pincher** is the actual BPE token count of what was returned. Savings persist in SQLite across reconnects, process restarts, and binary upgrades — the dashboard at `/v1/dashboard` shows the all-time total.

Typical per-call savings: `symbol` ~95%, `context` ~90%, `search` ~98%, `trace` ~99%. (`architecture` returns metadata only — no file-read alternative — so its `tokens_saved` is reported as 0 rather than fabricated, see [#219](https://github.com/kwad77/pincher/issues/219).)

---

## Staying current

Three subcommands keep pincher fresh and discoverable on the same machine:

```bash
# Auto-update in place — git pull + rebuild from this checkout, or fetch the
# latest GitHub release asset when run from outside the source tree.
./pincher update                  # apply if behind
./pincher update --check          # report status only

# Print the running HTTP dashboard URL; auto-spawn one if none is bound.
./pincher web                     # prints http://localhost:7777/v1/dashboard
./pincher web --json              # {url, base, pid, started_by}

# Inject the pincher usage policy into CLAUDE.md (idempotent — re-runs replace
# the marker block in place, never duplicating).
./pincher init                    # ./CLAUDE.md
./pincher init --global           # ~/.claude/CLAUDE.md
```

Other CLI subcommands ([`pincher index`](docs/REFERENCE.md#pincher-index), [`pincher doctor`](docs/REFERENCE.md#pincher-doctor), [`pincher rebuild-fts`](docs/REFERENCE.md#pincher-rebuild-fts), [`pincher self-test`](docs/REFERENCE.md#pincher-self-test)) and the full HTTP surface live in [REFERENCE.md](docs/REFERENCE.md).

---

## Roadmap

| Release | Theme | Status |
|---|---|---|
| **v0.2** | Index quality at scale (Bash, HCL, Markdown, Jinja2 extractors; per-corpus FTS5 split; pinned-corpus snapshot tests) | ✅ shipped |
| **v0.3** | Trust + observability (security audit, `pincher doctor`, dashboard CSP tightening, FTS5 escape hatch, per-symbol confidence) | ✅ shipped |
| **v0.4** | Capture-what-shipped (schema v11, four new CLI subcommands `update`/`web`/`init`/`stats`, HCL REFERENCES edges, plugin SessionStart hook, README split, Terraform pinned corpus) | ✅ shipped |
| **v0.5** | Trustworthy single-binary release (`go install` fix, default-deny remote HTTP, legacy `symbols_fts` removed, case-insensitive `project_id` fix, release artifact pipeline) | ✅ shipped |
| **v0.6** | Multi-client adoption (`pincher init` for Cursor / Windsurf / Aider / Continue, three end-to-end tutorials, `pincher project rm` CLI, coverage gate restored to 84%) | ✅ shipped |
| **v0.7** | Language + polish (HTML / XML extractors, stats reconciliation, Cypher polish vs rename, bench gate verdict) | planned |
| **v1.0** | Freeze + announce (tool schemas frozen, schema attestation, migration guide, public launch) | planned |

Live milestone burndown: <https://github.com/kwad77/pincher/milestones>. Full punch lists per release: [#193](https://github.com/kwad77/pincher/issues/193).

---

## Known limitations

- **Sequence-rename ID instability in YAML.** Inserting an item at index 0 of a YAML sequence renames every downstream symbol's qualified name (`tasks.0` → `tasks.1`). Move detection catches some cases but not deterministically. Verdict (fix vs document-as-won't-fix) tracked at [#205](https://github.com/kwad77/pincher/issues/205) for v0.7.
- **Single-user SQLite.** Cross-process indexing is safe (filesystem lockfile). Team / enterprise shared indexes need a server mode — explicitly out of v1.0 scope.
- **~7 languages without extractors.** Scala, Lua, Zig, Elixir, Haskell, Dart, R are detected as source but emit zero symbols. Adding any of them = implement one Go interface.
- **HTML / XML extractors not yet shipped.** Markup-heavy projects (component libraries, .NET csproj, Maven pom.xml) currently fall through to no extraction. Both planned for [v0.7](https://github.com/kwad77/pincher/milestone/5) ([#100](https://github.com/kwad77/pincher/issues/100), [#101](https://github.com/kwad77/pincher/issues/101)).

Full known-limitations list, with severity and tracking issue: [REFERENCE.md → Known Limitations](docs/REFERENCE.md#known-limitations).

---

## License

MIT

<div align="center">
  <img src="docs/assets/crab.png" width="32" alt="Pinchy"/>
</div>
