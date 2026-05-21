#!/usr/bin/env bash
# scripts/reproduce-savings.sh — reproduce pincher's published token-savings numbers.
#
# See docs/methodology/token-savings.md for the methodology + interpretation.
# This script is the "run this on a fresh clone, see the numbers" path.
#
# Three phases:
#   1. Setup     — build pincher from HEAD, index this repo
#   2. Measure   — pincher bench with exact-BPE token accounting + N=50 samples
#   3. Compare   — diff against testdata/savings-baseline.json (committed
#                  per-release reference figures)
#
# Exit 0: results within ±10% of baseline.
# Exit 1: drift beyond band — investigate or update baseline.
# Exit 2: build / index / bench failure.
#
# Issue: #1520 (FILE-A v0.85)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

BIN=${PINCHER_BIN:-./pincher.test-savings}
DATA_DIR=$(mktemp -d -t pincher-savings-XXXXXX)
N_SAMPLES=${N_SAMPLES:-50}
TOLERANCE_PCT=${TOLERANCE_PCT:-10}
BASELINE_FILE=${BASELINE_FILE:-testdata/savings-baseline.json}

cleanup() {
  rm -rf "$DATA_DIR" "$BIN" 2>/dev/null || true
}
trap cleanup EXIT

# ── Phase 1: build + index ────────────────────────────────────────────────
echo "=== Token-savings reproducer ==="
echo "Building pincher from HEAD..."
if ! go build -o "$BIN" ./cmd/pinch/ 2>&1; then
  echo "ERROR: build failed" >&2
  exit 2
fi

GIT_DESC=$(git describe --tags --dirty --always 2>/dev/null || echo "unknown")
echo "Binary:    $GIT_DESC ($("$BIN" --version 2>&1 | head -1))"
echo "Project:   $REPO_ROOT"
echo "Tokenizer: cl100k_base BPE (exact)"
echo "Samples:   $N_SAMPLES per tool shape"
echo "Tolerance: ±${TOLERANCE_PCT}%"
echo

echo "Indexing repo..."
if ! "$BIN" --data-dir "$DATA_DIR" index "$REPO_ROOT" >/tmp/.pincher-index.log 2>&1; then
  echo "ERROR: index failed; see /tmp/.pincher-index.log" >&2
  exit 2
fi

# ── Phase 2: bench with exact accounting ──────────────────────────────────
echo "Running pincher bench (this takes ~30-60s)..."
BENCH_JSON=$(mktemp)
if ! PINCHER_TOKEN_ACCOUNTING=exact \
     "$BIN" --data-dir "$DATA_DIR" bench --n "$N_SAMPLES" --json --seed 42 \
     > "$BENCH_JSON" 2>/tmp/.pincher-bench.log; then
  echo "ERROR: bench failed; see /tmp/.pincher-bench.log" >&2
  cat /tmp/.pincher-bench.log >&2 || true
  exit 2
fi

# ── Phase 3: compare against baseline ─────────────────────────────────────
echo
echo "=== Results ==="
if command -v python3 >/dev/null 2>&1; then
  python3 - "$BENCH_JSON" "$BASELINE_FILE" "$TOLERANCE_PCT" <<'PY'
import json
import sys

bench_path, baseline_path, tolerance_pct = sys.argv[1:4]
tol = float(tolerance_pct) / 100.0

with open(bench_path) as fh:
    bench = json.load(fh)

try:
    with open(baseline_path) as fh:
        baseline = json.load(fh)
except FileNotFoundError:
    baseline = None
    print(f"NOTE: no baseline at {baseline_path}; printing run only.")

# Schema: pincher bench --json emits {"tools": [{"tool":"search","actual_tokens_avg":...,"baseline_tokens_avg":...,"savings_pct_avg":...,"p50_ms":...,"p95_ms":...}, ...]}.
# The exact shape is documented at docs/reference/cli.md > pincher bench. Adjust the
# field names below if the bench shape evolves (the test
# TestBenchReproducerSchemaPin pins this).
tools = bench.get("tools", [])

print(f"{'Tool':<12} | {'Avg actual':>10} | {'Avg baseline':>13} | {'Savings %':>9} | {'p50 (ms)':>9} | {'p95 (ms)':>9}")
print("-" * 80)
drift_found = False
for row in tools:
    name = row.get("tool", "?")
    actual = row.get("actual_tokens_avg", 0)
    base = row.get("baseline_tokens_avg", 0)
    pct = row.get("savings_pct_avg", 0)
    p50 = row.get("p50_ms", 0)
    p95 = row.get("p95_ms", 0)
    print(f"{name:<12} | {actual:>10.0f} | {base:>13.0f} | {pct:>8.1f}% | {p50:>9.1f} | {p95:>9.1f}")

    if baseline and name in baseline.get("tools", {}):
        ref = baseline["tools"][name]
        ref_pct = ref.get("savings_pct_avg", pct)
        if abs(pct - ref_pct) > tol * 100:
            drift_found = True
            print(f"  ⚠  drift vs baseline: {pct:.1f}% vs {ref_pct:.1f}% (tolerance ±{tol*100:.0f}%)")

if baseline:
    print()
    if drift_found:
        print("DRIFT DETECTED — investigate or regenerate baseline.")
        sys.exit(1)
    else:
        print("✅ within tolerance of committed baseline.")
sys.exit(0)
PY
else
  # Fallback: no python3 available — just dump the JSON.
  echo "python3 not available — dumping raw bench JSON:"
  cat "$BENCH_JSON"
  echo
  echo "(install python3 for tabular output + baseline comparison)"
fi

rm -f "$BENCH_JSON"
