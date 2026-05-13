#!/usr/bin/env bash
# release-channel_test.sh — exercise the channel-detection rule (#642).
# Run via `bash scripts/release-channel_test.sh` (no test framework needed).

set -euo pipefail

CMD="bash $(dirname "$0")/release-channel.sh"
PASS=0
FAIL=0

assert() {
  local tag="$1"
  local want="$2"
  local got
  got="$($CMD "$tag" 2>&1 || echo "ERROR")"
  if [[ "$got" == "$want" ]]; then
    echo "  ok   $tag → $got"
    PASS=$((PASS + 1))
  else
    echo "  FAIL $tag → $got (want $want)"
    FAIL=$((FAIL + 1))
  fi
}

echo "stable channel — minor % 10 == 0"
assert "v0.60.0"  "stable"
assert "v0.70.0"  "stable"
assert "v0.80.0"  "stable"
assert "v0.90.0"  "stable"
assert "v1.0.0"   "stable"
assert "v1.10.0"  "stable"

echo "stable channel — patches inherit"
assert "v0.60.1"  "stable"
assert "v0.60.7"  "stable"
assert "v1.0.5"   "stable"

echo "dev channel — minor not divisible by 10"
assert "v0.53.0"  "dev"
assert "v0.54.0"  "dev"
assert "v0.59.0"  "dev"
assert "v0.61.0"  "dev"
assert "v0.69.0"  "dev"
assert "v0.71.0"  "dev"

echo "dev channel — patches inherit"
assert "v0.53.1"  "dev"
assert "v0.53.7"  "dev"

echo "pre-release suffixes override minor rule"
assert "v0.60.0-beta.1"   "beta"
assert "v0.70.0-alpha.3"  "alpha"
assert "v1.0.0-rc.2"      "rc"
# Even when the minor would be stable, a pre-release suffix still wins:
assert "v0.60.0-beta.2"   "beta"
# Unknown suffix falls back to dev:
assert "v0.60.0-foo.1"    "dev"

echo
echo "PASS: $PASS · FAIL: $FAIL"
if (( FAIL > 0 )); then
  exit 1
fi
