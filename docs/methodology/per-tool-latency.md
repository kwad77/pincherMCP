# Per-tool latency budget

Per-tool p50 + p99 latency gate that catches single-tool regressions that the aggregate `make corpus-bench` gate would miss. Tracked by [#1522](https://github.com/kwad77/pincher/issues/1522) (FILE-C, v0.91 phase-1 advisory; v1.0 phase-2 required).

## Why a per-tool gate

`make corpus-bench` gates the `internal/index` + `internal/server` packages as a whole. A regression in one tool (say, `trace` latency) can land if the aggregate stays within ±20%. The per-tool budget enforces a tighter contract: every advertised tool stays within its declared p99 budget, individually.

This matters at v1.0 because the published latency claims (in `docs/REFERENCE.md` Performance table) are per-tool numbers. An aggregate-only gate lets the README claim "search 1ms" while the actual p99 has crept to 8ms across a quarter of releases.

## The budget file

`testdata/bench/per-tool-latency-budget.json` holds the committed p50/p99 per tool. Tools NOT in the JSON are exempt (admin/init/diagnostic tools whose latency isn't a user-facing claim).

Each tool's entry is the **target**, not the current measurement. The gate measures and compares; the budget moves only when a deliberate perf-affecting refactor justifies a new target.

## What the runner does

`scripts/per-tool-latency.sh`:

1. Indexes `testdata/corpus/go-project` (the smallest pinned corpus — keeps the latency contributions from the corpus stable across releases).
2. Starts pincher's HTTP gateway on a free port.
3. For each tool in the budget JSON, invokes the gateway endpoint `ITERATIONS` times (default 100) with a minimal body.
4. Sorts the per-invocation timings, picks p50 (index 50) and p99 (index 99).
5. Compares p99 against `budget_p99 × (1 + THRESHOLD_PCT/100)`. Default threshold 20%.

Why HTTP gateway (not stdio MCP): the gateway is the consumer-facing path; stdio overhead is structurally different and is gated separately by the existing benchmarks.

## Phase 1 (v0.91-v0.99): advisory

The workflow runs weekly + on dispatch. `continue-on-error: true` and the script exits zero even when tools breach the threshold — failures surface as `::warning::` log lines visible in the workflow run summary.

This is the soak-test window. Real regressions across a release cycle show as recurring warnings; flaky noise from runner variance shows as inconsistent ones. The phase-1 data informs whether the budget numbers are right before we promote.

## Phase 2 (v1.0+): required

At v1.0 promotion, the FILE-C acceptance criterion says the gate becomes required. Two edits:

1. `.github/workflows/per-tool-latency.yml`: `continue-on-error: true` → `false`.
2. `scripts/per-tool-latency.sh`: change the final `exit 0` (under the phase-1 comment) to `exit 1`.

The CLAUDE.md required-gates list also picks up `per-tool latency` at that point.

## When to update the budget

The budget is pinned to **CI hardware** — running locally on a different machine produces meaningless deltas. Same constraint as the `make corpus-bench` baseline.

Refresh the budget when:

- A deliberate perf-affecting refactor changes a tool's latency shape and the new numbers ARE the rationale (then re-baseline against that release).
- New tools are added (extend the JSON before they exit experimental status).

Do NOT refresh when:

- A tool's measured p99 is drifting up without a known cause. That IS the regression the gate exists to catch — fix the regression rather than absorb it into a new baseline.

## Related

- [#1522](https://github.com/kwad77/pincher/issues/1522) FILE-C — this gate.
- [`docs/REFERENCE.md`](../REFERENCE.md) Performance table — the budget numbers should match per-tool claims in the table when phase-2 lands.
- `make corpus-bench` — package-aggregate gate (catches in-process regressions; complementary).
- `.github/workflows/time-to-first-success.yml` — user-path latency budget (orthogonal: end-to-end clone→answer wall-clock, not per-tool).
