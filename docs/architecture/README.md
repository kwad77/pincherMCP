# Architecture diagrams

Visual reference for pincher's main subsystems. Each diagram is Mermaid so GitHub renders it inline — no SVG build step, no toolchain dependency. Update the source block here and the diagram updates everywhere it's embedded.

## Storage layers (the single-table three-index design)

```mermaid
flowchart TB
    subgraph ast["AST extraction (one pass per file)"]
      Walker[gocodewalker walks repo] -->|.gitignore-respecting| Goroutine[per-file goroutine]
      Goroutine -->|ast.ExtractWithModule| Extractor[language extractor]
      Extractor -->|ExtractedSymbol| Buffer[flushBuffers batches symbols+edges]
    end

    subgraph storage["Storage — symbols table serves 3 query paths"]
      Buffer --> Symbols[(symbols table)]
      Buffer --> Edges[(edges table)]
      Symbols -.->|byte offsets| ByteRetrieval[Layer 1 — O(1) byte seek<br/>GetSymbol → start_byte..end_byte]
      Symbols -.->|graph nodes| KnowledgeGraph[Layer 2 — knowledge graph<br/>pinchQL → SQL via cypher/engine]
      Symbols -.->|FTS5 mirror| FTS[Layer 3 — full-text search<br/>BM25 via symbols_fts virtual table]
      Edges -.-> KnowledgeGraph
    end

    style ByteRetrieval fill:#1f3a52,color:#fff
    style KnowledgeGraph fill:#1f3a52,color:#fff
    style FTS fill:#1f3a52,color:#fff
```

**Key invariants**: all three indexes are populated in a single `ast.Extract()` call. FTS5 triggers auto-sync the virtual table; never sync manually. `db.SetMaxOpenConns(1)` keeps the SQLite single-writer invariant; the reader pool handles SELECTs.

## Indexer pipeline (Index() flow)

```mermaid
flowchart LR
    Start([pincher index]) --> Walk[gocodewalker walk]
    Walk --> PerFile[per-file goroutine fan-out]
    PerFile -->|hash match| Skip[skip file]
    PerFile -->|new/changed| Extract[ast.Extract]
    Extract --> Flush[flushBuffers — 500 syms or 1000 edges]
    Flush --> Symbols[(symbols + edges + pending_edges)]
    Skip --> Wait[wg.Wait]
    Flush --> Wait
    Wait --> Gate{totalFiles &gt; 0<br/>or force?}
    Gate -->|no| FastExit[Skip resolve passes — #1314]
    Gate -->|yes| Resolve[resolveImports / Calls / Reads]
    Resolve -->|threshold| Preload[LoadAllSymbolsByQN bulk — #1338]
    Resolve --> EdgeWrites[INSERT OR IGNORE into edges]
    FastExit --> GC[Tail GC for files removed from disk]
    EdgeWrites --> GC
    GC --> Checkpoint[CheckpointTruncate WAL]
    Checkpoint --> Done([IndexResult])

    style Gate fill:#3a5240,color:#fff
    style FastExit fill:#523a40,color:#fff
    style Preload fill:#523a40,color:#fff
```

**Key invariants**: per-project mutex + cross-process `acquireProjectLock` serialise concurrent indexers. Tail GC removes orphan symbols + file_hash rows for files no longer on disk (#326).

## MCP stack (request → response flow)

```mermaid
flowchart TB
    subgraph entry["Entry points"]
      Stdin[stdin/stdout JSON-RPC<br/>MCP protocol]
      HTTP[HTTP REST gateway<br/>--http :PORT]
    end
    Stdin --> Handler[handler dispatch — registerTools]
    HTTP -->|/v1/&lt;tool&gt;| Handler
    Handler --> Store[db.Store query]
    Store --> Response[jsonResultWithMeta]
    Response --> Envelope[wrap result in _meta envelope]
    Envelope --> SessionStats[atomic increment session stats]
    SessionStats --> Tokens[ApproxTokens — char/4 default<br/>opt-in BPE via env knob — #1320]
    Tokens --> Marshal[marshal to JSON]
    Marshal --> ClientReply([client reply])

    SessionFlusher[StartSessionFlusher<br/>every 10s] -.->|flush| SessionTable[(sessions table)]
    SessionStats -.-> SessionFlusher

    style Tokens fill:#523a40,color:#fff
    style Envelope fill:#1f3a52,color:#fff
```

**Key invariants**: every handler ends in `jsonResultWithMeta` which atomically increments session stats. `_meta.capabilities` carries the per-server capability slice (opt-out via `PINCHER_META_CAPABILITIES=off`, #1087). HTTP gateway preserves the same handler set; no diverged code path.

## Watcher lifecycle (background re-index loop)

```mermaid
sequenceDiagram
    participant W as Indexer.Watch()
    participant P as Project file tree
    participant DB as SQLite store
    participant Drift as Drift detector

    loop every 2s (active) / 30s (idle)
      W->>P: scan for changed files
      alt no files changed
        W->>W: skip resolve passes — #1314 + #1317
        Note over W: no-change tick → ~716 allocs<br/>(pre-fix: 4806)
      else files changed
        W->>P: per-file extract
        W->>DB: flush symbols + edges
        W->>DB: resolve passes (optional QN preload — #1338)
      end
      W->>Drift: check binary checksum vs running version
      alt drift detected (PINCHER_AUTO_RESTART_ON_DRIFT)
        Drift->>W: signal restart
        W->>W: exit(0) for supervisor to relaunch
      end
    end
```

**Key invariants**: 2s active / 30s idle polling cadence. Drift detection compares the on-disk binary checksum against the running process; supervised mode (#705) re-launches automatically without manual `/mcp` reconnect. Auto-restart-on-drift requires `PINCHER_AUTO_RESTART_ON_DRIFT=1` in the MCP child's env.

## Authoring notes

- Source format: GitHub Markdown with Mermaid code fences. No build step.
- If a diagram drifts from the code, update the diagram in the same PR as the code change (per the v0.69 inline-update discipline).
- For ASCII fallback (e.g., terminal-only diff viewers), GitHub renders Mermaid as a `mermaid` code block. Readers without Mermaid support see the raw text, which is still readable as a flowchart definition.
