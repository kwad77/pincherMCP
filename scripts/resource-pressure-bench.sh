#!/usr/bin/env bash
# scripts/resource-pressure-bench.sh — FILE-I #1528 v0.85.
#
# Measures pincher's peak RSS while indexing synthetic corpora at four
# file-count tiers (1k / 10k / 50k / 100k). Output is JSON consumed by
# CI for budget enforcement and by the methodology doc for the
# "minimum recommended RAM per indexed-file tier" published budget.
#
# Each tier:
#   1. Generates the synthetic corpus.
#   2. Runs `pincher index` under `/usr/bin/time -v` (Linux) or
#      `/usr/bin/time -l` (macOS) to capture Maximum Resident Set Size.
#   3. Records peak RSS + wall time + file count.
#
# Linux-only for v0.85 (CI runs on ubuntu-latest). macOS path is
# scaffolded but the parser differs and falls through to a not-supported
# warning. Windows is not in scope — `pincher index` perf on Windows
# is dominated by NTFS overhead, which the linux-only number won't tell
# you anything about. We publish "Linux/macOS only" alongside the budget.
#
# Usage:
#   PINCHER_BIN=/abs/path scripts/resource-pressure-bench.sh \
#       [tier1,tier2,...] [output-json]
#
# Defaults:
#   tiers       — "1000,10000"  (full 50k/100k available via env override)
#   output-json — out/resource-pressure.json

set -euo pipefail

PINCHER_BIN="${PINCHER_BIN:-$(command -v pincher 2>/dev/null || echo "")}"
if [ -z "${PINCHER_BIN}" ] || [ ! -x "${PINCHER_BIN}" ]; then
  echo "::error::PINCHER_BIN not set and no pincher on PATH" >&2
  exit 2
fi

TIERS="${1:-1000,10000}"
OUT="${2:-out/resource-pressure.json}"
mkdir -p "$(dirname "${OUT}")"

# Detect /usr/bin/time. macOS ships `time` as a shell builtin; we need
# the binary at /usr/bin/time for the -v / -l verbose mode.
TIME_BIN=""
TIME_FLAGS=""
TIME_PARSE=""
if [ -x /usr/bin/time ]; then
  TIME_BIN=/usr/bin/time
  if [ "$(uname)" = "Linux" ]; then
    TIME_FLAGS="-v"
    TIME_PARSE="linux"
  elif [ "$(uname)" = "Darwin" ]; then
    TIME_FLAGS="-l"
    TIME_PARSE="darwin"
  else
    echo "::warning::unsupported OS $(uname) — only Linux/macOS RSS reporting works." >&2
    exit 0
  fi
else
  echo "::warning::/usr/bin/time not found — install GNU time (Linux) or use macOS default."
  exit 0
fi

results='[]'

IFS=',' read -ra TIER_LIST <<< "${TIERS}"
for tier in "${TIER_LIST[@]}"; do
  work=$(mktemp -d -t respres-XXXXXX)
  trap 'rm -rf "$work"' EXIT
  echo "── tier: ${tier} files ─────────────────────────────"
  bash scripts/generate-synthetic-corpus.sh "${tier}" "${work}/corpus" >/dev/null

  log="${work}/time.log"
  pin_data="${work}/data"

  # Capture wall time too. `time` writes its output to stderr.
  start_s=$(date +%s)
  "${TIME_BIN}" ${TIME_FLAGS} \
    "${PINCHER_BIN}" --data-dir "${pin_data}" index "${work}/corpus" \
    >"${work}/idx.out" 2>"${log}" || {
      echo "::error::tier ${tier}: pincher index failed; stderr below"
      tail -20 "${log}" >&2
      exit 1
    }
  end_s=$(date +%s)
  wall_s=$(( end_s - start_s ))

  # Parse peak RSS.
  if [ "${TIME_PARSE}" = "linux" ]; then
    # GNU time reports "Maximum resident set size (kbytes): N".
    peak_kb=$(awk '/Maximum resident set size/ {print $NF}' "${log}")
  else
    # macOS time -l reports "  N  maximum resident set size" (bytes).
    peak_b=$(awk '/maximum resident set size/ {print $1}' "${log}")
    peak_kb=$(( peak_b / 1024 ))
  fi

  if [ -z "${peak_kb}" ]; then
    echo "::error::tier ${tier}: could not parse peak RSS from time output"
    tail -20 "${log}" >&2
    exit 1
  fi
  peak_mb=$(( peak_kb / 1024 ))

  echo "  peak_rss=${peak_mb} MiB,  wall=${wall_s}s,  files=${tier}"

  results=$(jq --arg tier "$tier" --arg mb "$peak_mb" --arg wall "$wall_s" \
    '. + [{file_count: ($tier|tonumber), peak_rss_mib: ($mb|tonumber), wall_s: ($wall|tonumber)}]' \
    <<< "${results}")
done

jq --argjson r "${results}" --arg ts "$(date -u +%FT%TZ)" --arg os "$(uname -sr)" \
  '{schema_version: 1, captured_at: $ts, host_os: $os, tiers: $r}' \
  <<< '{}' > "${OUT}"

echo
echo "Wrote ${OUT}:"
cat "${OUT}"
