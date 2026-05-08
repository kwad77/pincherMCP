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
