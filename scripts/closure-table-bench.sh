#!/usr/bin/env bash
# closure-table-bench.sh — measure closure-table storage cost (#639).
#
# Decision-blocking measurement before committing to v0.54's #652 closure-tables
# phase 1 implementation: at depth=3 and depth=5, how big does the materialized
# closure get on a real corpus? If a 10k-file repo lands above ~500 MB of
# closure storage, phase 1 needs a smaller default depth or an on-demand mode.
#
# Usage:
#   scripts/closure-table-bench.sh [--repo PATH] [--db DB] [--md]
#
# - PATH: optional repo to (re-)index before measurement (default: skip indexing,
#         measure whatever's already in the source DB).
# - DB:   pincher.db path (default: $HOME/.pincher/pincher.db).
# - --md: print one Markdown table row instead of multi-line summary (paste-able
#         into the #639 results table).
#
# The actual measurement work is in cmd/closurebench/main.go — this is just
# the orchestration shell so the operator workflow is one line.

set -euo pipefail

REPO=""
DB="${HOME}/.pincher/pincher.db"
MD=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)   REPO="$2"; shift 2 ;;
    --db)     DB="$2"; shift 2 ;;
    --md)     MD="-md"; shift ;;
    -h|--help)
      sed -n '2,16p' "$0"
      exit 0
      ;;
    *)
      echo "unknown arg: $1" >&2
      exit 2
      ;;
  esac
done

# Build the bench cmd if not already built (idempotent).
go build -o ./.bin/closurebench ./cmd/closurebench

# Optional pre-index step. Indexing is a single-process operation; the bench
# happens against a side-DB so concurrent reads are safe even when not indexing.
if [[ -n "$REPO" ]]; then
  echo "indexing $REPO ..." >&2
  if ! command -v pincher >/dev/null 2>&1; then
    if [[ -x ./pincher ]]; then PIN="./pincher"
    elif [[ -x ./pincher.exe ]]; then PIN="./pincher.exe"
    else
      echo "pincher binary not found in PATH or repo root; build with 'make build' first" >&2
      exit 3
    fi
  else
    PIN="pincher"
  fi
  "$PIN" index "$REPO"
fi

./.bin/closurebench -db "$DB" $MD
