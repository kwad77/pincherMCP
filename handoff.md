# Handoff — v0.58 → v0.59

**Status at handoff:** `master` at `0a5329d` (release: prep v0.58.0). v0.58.0 release-prep PR (#1059) merged. **The tag itself has not been pushed yet** — see [Tag the release](#tag-the-release) below for the explicit step. Everything else is committed.

This is the artifact that v0.59 (the hardening + pre-promotion release) picks up. It tells you what changed, what's working, what's known-broken, and where to look first.

---

## What v0.58 shipped (one-paragraph version)

**Failure-as-pedagogy at the project boundary.** v0.58 is a behaviour release, not a schema release — no migration, no new tools, but every tool that takes a project arg or scopes to a single ID now tells you what's wrong instead of silently returning the wrong tree's data. Three diagnosis families closed: silent cross-project ID resolution (mirror projects on multi-project installs were returning source bytes from the wrong project), star-sentinel consistency (the `project="*"` arg now does the right thing on all 13 project-arg tools), and ghost-extraction empty-result diagnosis (projects with substantial symbols but zero edges now explain the empty result instead of advising "lower min_confidence"). Plus `doctor`'s ceiling lowered so it actually fits the MCP per-call token cap, `changes` stops returning silent zero-everything responses, and Python AST extraction picks up its first end-to-end pinned-snapshot corpus.

## What v0.58 shipped (by family)

### Cross-project leak class (the headline)

A single dogfood probe found this: `mcp__pincher__symbol id=cmd/pinch/main.go::main.main#Function` on a workspace with mirror projects returned the **sniffer mirror's** source, not pincher-repo's. No warning. An agent using the result to edit code would be editing the wrong tree. Five fixes closed the family across every retrieval-shape tool:

| PR | Tool | Behaviour |
|---|---|---|
| #1049 | `symbol` | Warns when unscoped lookup resolved outside session project |
| #1050 | `context` + `symbols` (batch) | Same warning + batch-level aggregation across mixed IDs |
| #1051 | `neighborhood` | Same warning + names the file_path's source-of-truth |
| #1052 | `trace` (id-mode) | Warns when seed lands in different project than BFS traversal scopes to |
| #1047 | `search` (project=*) | Emits `project_id` on every cross-project result row |

Backward-compatible across the board — the unscoped lookup still returns the same row it did pre-fix; the warning is purely additive in `_meta.warnings`.

### Star-sentinel consistency (3-way fix)

`*` is the documented cross-project sentinel on `search`/`query`. Other tools mishandled it:

| PR | Tool category | Behaviour |
|---|---|---|
| #1048 | per-ID retrieval (symbol/symbols/context/neighborhood) | Silent fallback to unscoped lookup |
| #1055 | aggregate (health/stats) | Silent fallback to session/global view |
| #1056 | per-project (architecture/schema/trace/dead_code/etc.) | Clear-rejection error naming the sentinel + pointing at search/query |

The clear-rejection text matters: pre-fix passing `*` to `architecture` returned `project "*" not found — use list to see available projects`, but `list` never contains `*`. The new error names `*` as the cross-project sentinel and names the two tools that accept it.

### Ghost-extraction diagnosis family

Started in earlier PRs; closed in v0.58:

| PR | Tool | Behaviour |
|---|---|---|
| #1040 | `architecture` | Diagnoses ghost when code-corpus languages exist with 0 edges |
| #1042 | `schema` | Diagnoses ghost when ≥100 callable kinds + 0 edges |
| #1043 | `query` | Diagnoses ghost on empty rows + 0 edges |
| #1044 | `dead_code` | Diagnoses ghost on populated OR empty results + 0 edges |

Plus `doctor`'s existing #1009 advisory. The remaining gap is the low-edges-per-symbol ratio extension (#1010) — currently the advisory fires on `edges == 0` strict, but warp_rc at 1.4M symbols / 247 edges (ratio 0.000135) flies under the radar. Filed for v0.59.

### `doctor` token-cap fitting (#1054)

The pre-#1016 `top=99999` blew the MCP token cap with a 506 KB response. #1016 clamped to 500 — but at top=500 the response still ran ~218 KB and STILL exceeded the cap on real installs (118 projects). #1054 drops the ceiling to 50; three sections × 50 rows × ~400-byte rows ≈ 60 KB, comfortable headroom. For deeper enumeration callers should use `list` (paginated) or pinchQL queries against the underlying tables.

### `changes` empty-state diagnosis (#1053)

`changes scope=staged` on a workspace with only-unstaged edits returned `{changed_files:[], ...}` with NO `_meta` — the agent couldn't distinguish "nothing changed" from "I asked the wrong scope." Now probes the other scopes (`staged`/`unstaged`/`all`) on empty and reports which DO have content, plus offers next_steps pointing at each. When every scope is clean, suggests `scope="base:<branch>"`.

### Python AST + YAML/Markdown pin-down (#1057, #1058)

- New `testdata/corpus/python-app/` pinned corpus exercises Class + Method + Function + Module + AsyncFunctionDef + cross-file IMPORTS + CALLS (5 files / 16 symbols / 6 edges).
- New unit tests for: Markdown headings-in-code-blocks isolation, Markdown setext headings (`===` / `---`), YAML merge-key (`<<: *anchor`) static-extraction.
- **#1058 was a hot-fix** for a CI gate that didn't fire on #1057: `TestSearchRelevance_QueriesRegistered` requires every new corpus to register a curated query set in `cmd/pinch/main.go`'s `searchRelevanceQueries` map. The PR landed without the registration and CI somehow let it through. Investigation deferred to v0.59 — see [Things to investigate](#things-to-investigate).

---

## What's in flight for v0.59

v0.59 is **hardening + stability** by policy — no new features. Six workstreams per the #664 umbrella:

1. **Bug-fix triage.** Sweep open bugs, close blockers for v0.60 promotion.
2. **Perf regression validation.** Run bench corpora vs v0.51.1 baseline; flag >5% regressions.
3. **Cross-platform smoke.** One-command install on Claude Code, Codex, Cursor, Zed (4 platforms minimum).
4. **Capability-advertisement audit.** Every entry in `_meta.capabilities` verified by a runtime probe.
5. **v0.60 stable-promotion sign-off doc.** Brief sign-off capturing what's verified + what risks remain.
6. **Phase 2 backlog build.** File per-release issues for v0.61–v0.68 while Phase 1 lessons are fresh.

Plus 4 specific bugs currently open against v0.59:

| # | Title | Shape |
|---|---|---|
| [#1010](https://github.com/kwad77/pincher/issues/1010) | doctor ghost-advisory: extend to low-edges-per-symbol ratio | Follow-on to the ghost family v0.58 just closed (currently `edges==0` strict; misses warp_rc at 0.000135 ratio) |
| [#986](https://github.com/kwad77/pincher/issues/986) | Watch-triggered reindex produces ~30% of expected symbol count after binary drift | Resolver-phase gating bug — suspect `force` flag isn't propagating to `resolveImports`/`Calls`/`Reads` on the watch path. The last visible gap in the auto-restart-on-drift workflow. |
| [#996](https://github.com/kwad77/pincher/issues/996) | Large JSON test-fixture dirs explode symbol count (1.45M syms / 247 edges on warp_rc) | Indexer needs heuristic skip for `testdata/`/`ref_tests/data/`/`fixtures/` JSON, or per-file symbol cap |
| [#640](https://github.com/kwad77/pincher/issues/640) | Hook conversion-rate field measurement (first 30 days) | Needs CLI export shim + community thread for ≥20 install reports |

Also still open on **v0.58.0** milestone (will roll to v0.59 if not closed at tag time):

- **[#858](https://github.com/kwad77/pincher/issues/858)** — non-Go corpora produce zero edges (closure tables / trace / dead_code Go-only in practice). **The biggest user-visible quality gap.** Python joined Go in v0.57; TypeScript / Rust / Ruby are next on the long-tail list.
- **[#663](https://github.com/kwad77/pincher/issues/663)** — v0.58 testing-depth umbrella
- **[#662](https://github.com/kwad77/pincher/issues/662)** — v0.57 field-data export CLI (predecessor to #640)

---

## Things to investigate

### Why `TestSearchRelevance_QueriesRegistered` didn't fail on #1057

#1057 added `"python-app"` to `cmd/pinch/snapshot_test.go`'s `corpora` slice without the matching `searchRelevanceQueries` entry. There's an explicit gate test for this (`TestSearchRelevance_QueriesRegistered`) that **does** fail locally on master after #1057 merged. But the PR's CI checks were green. Either:

- CI didn't run `./cmd/pinch` tests (workflow scope bug)
- Auto-merge merged before checks finished (auto-merge race)
- The auto-merge process bypasses required-status checks

Worth one focused dig before relying on auto-merge for v0.59's stability sweep. The hot-fix #1058 lands master green again; the question is how #1057 got there in the first place. **Files to start:** `.github/workflows/test.yml`, `.github/workflows/required-status.yml` (or wherever the branch protection lives).

### Should pincher's "savings" messaging reframe around index completeness?

Discovered during handoff: the "savings" framing on `stats` and the README is per-call (tokens saved vs reading the whole file). The dogfood-loop framing makes that backwards. The right metric is **calls avoided across the session**, and the way you get there is by indexing every file regardless of size. A 200-byte YAML omitted from the index forces 2-3× more tool calls when downstream code wants to trace into it. Pincher already indexes every small file (no min-size threshold in `ShouldSkip`) — the gap is purely in how the value gets messaged.

**Specifically:**
- `internal/server/server.go` — `savedVsFileSizesSession` is the per-call computation. Consider adding a session-level "completeness score" metric (% of files in session DB indexed by their owning project).
- `README.md` — the savings section reads in per-call frame. Consider replacing with "calls avoided across N-step workflows."
- `docs/REFERENCE.md` Savings section.

Not a v0.58 fix; logging as v0.59 framing work or post-promotion docs work.

---

## Reset state — where master sits at handoff

| Thing | State | Notes |
|---|---|---|
| `master` HEAD | `0a5329d` | release: prep v0.58.0 — failure-as-pedagogy at the project boundary (#1059) |
| Pending tag | **`v0.58.0` not pushed** | See [Tag the release](#tag-the-release) below |
| Open PRs | 0 | All merged |
| Open issues on v0.58.0 milestone | 3 (#858 / #663 / #662) | Will roll to v0.59 at tag time per the milestone-close rule |
| Schema version | v26 (unchanged) | No migration this release |
| MCP tool count | 22 (unchanged) | |
| Languages detected | ~25 (unchanged) | 10 AST-tier / 15 regex-tier / 7 stub-tier |
| Supervisor / auto-restart | Enabled, working | Binary swaps pick up on next MCP call without `/mcp` |
| Watch-triggered reindex | **Known-buggy** | #986 — ~30% of expected symbols after binary drift. Recovery: `mcp__pincher__index force=true` |

### Tag the release

```bash
git checkout master
git pull --ff-only
git tag -a v0.58.0 -m "v0.58.0 — failure-as-pedagogy at the project boundary"
git push origin v0.58.0
```

The release workflow handles Homebrew formula bump + Docker image push from there. **Don't** push the tag without confirming the CHANGELOG.md `[0.58.0]` section is in master (it is, via #1059).

---

## Standing-order context (what dogfood mode is doing)

This session ran with the "continuous probe→fix→ship" standing order: agent probes pincher live, finds a silent-confidently-wrong shape, cuts a fresh branch from master, writes the fix + regression test + CHANGELOG.d stub, opens a PR with auto-merge, returns to master, repeats. Stops only on explicit user "stop". Standard wakeups every ~270s (within prompt cache TTL).

**What that produced this session:** 16 PRs (#1044–#1059). Three diagnosis families fully closed (cross-project leak / star-sentinel / ghost-extraction). One Python corpus + edge-case tests. One release-prep PR.

**If you resume:** the loop is paused at handoff. To restart, send "continue the autonomous probe→fix→ship dogfood loop" — the agent will pick up against v0.59's open bugs.

---

## Memory updates this session

Persistent agent memory was updated through the loop (the agent's auto-memory system writes to `~/.claude/projects/.../memory/`). Highlights worth knowing about:

- The cross-project leak class is now documented as a recurring shape — future probes against indexed-mirror-heavy installs should expect mirror-vs-session disambiguation needs.
- The star-sentinel inconsistency was caught by probing both behaviors deliberately (silent fallback + clear-rejection categories) — pattern to repeat against future tool families.
- Doctor's per-section token budget is real: `top × 3 sections × per-row bytes`. The math says any future per-row payload growth needs a corresponding ceiling re-check.

---

## Files of note (uncommitted, ignore)

These are scratch files from the session, intentionally `.gitignore`'d or just untracked:

- `.planning-ci-hardening.md` — pre-release CI scratch
- `.planning-roadmap-to-v1.md` — phase planning scratch
- `.planning-zelosmcp-meeting.md`, `.study-guide-zelosmcp.md` — design-decision scratch from a prior meeting
- `srv.out` — supervisor log capture from a dogfood probe
- `scripts/enable-supervised.ps1`, `scripts/unstick-pincher.ps1` — Windows local-dev helpers

Nothing here blocks the tag. They're noted only so the v0.59 picker-upper knows what to ignore in `git status`.
