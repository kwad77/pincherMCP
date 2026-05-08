# pincherMCP — Ideation

> Status: **brainstorm**, not scheduled. None of these are committed to the roadmap. Captured here so they don't get lost while we work through the in-flight queue (#32, #34, etc.).

Author: kwad77

Outside-the-box features for an AST-aware, SQLite-backed graph serving LLM agents.

---

## 1. Human-AI Visual Bridging (the `render_subgraph` tool)

Right now, the agent understands the graph perfectly, but the human is stuck reading text descriptions of it. Since pincherMCP already runs an HTTP REST server, it would be incredible if the agent had a tool to generate interactive visuals.

**Concept:** The agent calls `render_subgraph(start_node="auth.ts", depth=3)`. Pincher dynamically generates a lightweight, interactive D3.js or HTML/canvas graph and serves it locally.

**UX impact:** The agent can reply to the user: "I've mapped out the auth flow. Click http://localhost:8080/v1/render/xyz to view the visual architecture."

---

## 2. "Blast Radius" & Codebase PageRank

Standard graph queries show connections, but we could pre-compute graph algorithms in SQLite to give agents superhuman intuition about code risk.

**Concept:** Run a PageRank or Betweenness Centrality algorithm over the `edges` table during indexing to give every node a "load-bearing score." Alternatively, provide a recursive CTE tool for agents to calculate the "blast radius" of a specific function.

**UX impact:** Agents could proactively warn users: "You asked me to modify `utils.formatDate`, but pincher indicates this is in the 99th percentile of centrality. This is a high-risk change."

---

## 3. Agentic "Scratchpads" (persistent working memory)

We already have the highly useful `adr` tool for permanent notes. Expanding this into a short-term memory system could solve the context-window reset problem during deep debugging sessions.

**Concept:** A `scratchpad` tool where agents can write structured hypotheses to SQLite (e.g., `status: investigating`, `dead_ends: [fileA, fileB]`, `current_theory: race condition`).

**UX impact:** If the context window fills up or the IDE is restarted, the agent can query its own scratchpad to pick up the investigation exactly where it left off yesterday.

---

## 4. Structural AST Diffing

Code reviews currently rely on text/line diffs, which LLMs often misinterpret. pincherMCP is uniquely positioned to do "AST diffs."

**Concept:** Allow pincher to index the working tree against the main branch and expose a `structural_diff` tool.

**UX impact:** Instead of guessing the impact of a `git diff`, the agent is told exactly what structural edges were added or destroyed: "This PR removes the call edge from `Checkout` to `InventoryCheck`."

---

## 5. Dead Code & "Code Rot" sweeper

Since pincher knows every symbol and edge, it inherently knows which code is disconnected.

**Concept:** Expose a `find_anomalies` or `find_isolated_subgraphs` tool.

**UX impact:** Users could instruct their agents to "spend the next 10 minutes cleaning up the codebase," allowing the agent to autonomously hunt down and remove truly dead code with mathematical certainty.

---

# LOE + Sanity Check (review pass)

Per-feature: level of effort, architecture fit, risks, and refinement notes.

## 1. `render_subgraph` (interactive D3.js)

- **LOE:** Medium-large (~1-2 weeks). Vendored D3 (~80KB), new `/v1/render/<token>` endpoint with token lifecycle, frontend code we now have to maintain.
- **Fit:** HTTP server + Cypher BFS already exist. Mostly UI plumbing.
- **Risks:**
  - Frontend code in a CLI-first project ages badly; D3 is a real maintenance surface.
  - Token lifecycle — when does a rendered URL expire? Eviction policy is non-obvious.
  - Just CSP-hardened the dashboard; D3 needs more permissive script-src.
- **Refinement:** ship **Mermaid output** first (couple hundred LOC, no new endpoint — just emit Mermaid syntax inline; Claude Code/Cursor render it natively). Covers ~80% of the value. Graduate to interactive D3 only if signal warrants.

## 2. PageRank / Blast Radius — actually two features

- **LOE:**
  - **Centrality column (PageRank):** ~200 LOC + snapshot updates. Compute once at `Index()` tail via `gonum/graph`. ~3 days.
  - **Blast radius:** ~50 LOC. `trace` already does BFS — add reverse direction + clearer name. ~1 day.
- **Fit:** Excellent — graph + edges already there.
- **Risks:**
  - PageRank rewards **popularity**, not **fragility**. `utils.Format` ranks high but isn't risky to change. Centrality and blast radius are **complementary**, not interchangeable. The original pitch conflates them.
  - Determinism: PageRank converges in iteration-order-dependent ways. Need fixed seed / iteration order for snapshot stability.
- **Refinement:** ship blast-radius first (almost free); centrality second. Frame the column as "load-bearing score," not "risk score."

## 3. Scratchpad

- **LOE:** Small (~150 LOC + schema migration).
- **Fit:** We already have `adr` (project-keyed K/V). Scratchpad = same pattern + TTL.
- **Sanity check:**
  - The "short-term memory" pitch doesn't match the proposed mechanism. SQLite-persisted state is **long-term** by definition — what makes it "short-term" is just a TTL flag.
  - The structured-fields pitch (`status`, `dead_ends`, `current_theory`) is dubious — locking schema requires domain assumptions; leaving it open is just JSON-in-text.
- **Refinement:** **don't add a new tool**. Add an optional `expires_at` field to `adr`. Lower risk, no new vocabulary for agents to learn. Likely 90% achievable today by writing better prompts around `adr`.

## 4. Structural AST Diff

- **LOE:** Large (~1-2 weeks). Dual-index plumbing is real work.
- **Fit:** Reasonable. We already index trees; the challenge is two trees in one DB without `project_id` collision.
- **Risks:**
  - Need a "shadow project" concept (e.g. `project_id = "<base>::shadow_<sha>"`). Cache invalidation is real.
  - `git checkout` into a temp dir has subprocess hygiene + race conditions (dirty working tree).
- **Verdict:** **highest-value differentiator on the list** — graph-level diff is something LLMs genuinely can't do from text alone. The existing `changes` tool is the precursor. Worth doing, but **needs a design RFC before implementation**, not a casual sprint.

## 5. Dead Code / Anomalies

- **LOE:** Small (~100 LOC tool + ~150 LOC tests). It's a query, not a feature.
- **Fit:** Excellent — graph encodes inbound edges already.
- **Sanity check (this is where the original pitch over-claims):**
  - "**Mathematical certainty**" is wrong. Pincher CAN'T prove a function isn't called via reflection, generated code, build tags, dependency injection, plugin loaders, or external API consumers.
  - False-positive surfaces: public API exports, test fixtures (called via reflection by test runners), Go interface implementations, init() functions, generated code, exported vars consumed externally.
  - "Spend 10 minutes cleaning up the codebase" + agent autonomy on deletes is **dangerous**. This must be "candidates for review," not "delete with confidence."
- **Refinement:** ship as `unreferenced` or `candidates` tool with explicit caveats in the description. Always require human review. **No autonomous-deletion tool.**

---

## Ranked recommendation

| Order | Item | Effort | Value |
|---|---|---|---|
| 1 | Mermaid output for graph queries | 1-2 days | High — cheap differentiator |
| 2 | Blast-radius (reverse trace) | 1 day | High — almost free |
| 3 | `unreferenced` tool with explicit caveats | 2-3 days | Medium — useful but risky framing |
| 4 | Centrality column (PageRank) | 3-5 days | Medium-high — compounds with #2 |
| 5 | Structural diff RFC + impl | 1-2 weeks | Highest single feature; needs design first |
| — | Scratchpad → fold into `adr` w/ TTL | 1 day | Marginal vs. just better prompting |
| — | Interactive D3 render | Defer | Mermaid covers 80% of value |
