#!/usr/bin/env bash
# scoop-manifest_test.sh — contract checks for packaging/scoop/pincher.json.
#
# #1260 §1: the Scoop manifest lives in-repo so users can install via the
# raw URL even before a dedicated bucket repo exists. This test pins the
# manifest's required fields so a misedit doesn't ship a broken install
# path. Run via `bash scripts/scoop-manifest_test.sh` (no framework).
#
# Acceptance:
#   - parses as JSON
#   - version + license + homepage + description present
#   - architecture entries cover 64bit + arm64 with url + hash + bin
#   - bin tuple maps an arch-named exe to the canonical "pincher" alias
#   - autoupdate section mirrors the architecture entries with $version

set -euo pipefail

MANIFEST="$(dirname "$0")/../packaging/scoop/pincher.json"
PASS=0
FAIL=0

ok()   { echo "  ok   $1"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL $1"; FAIL=$((FAIL + 1)); }

require_jq() {
  if ! command -v jq >/dev/null 2>&1; then
    echo "  SKIP jq not installed; skipping manifest contract test"
    exit 0
  fi
}

require_jq

if [[ ! -f "$MANIFEST" ]]; then
  fail "manifest missing at $MANIFEST"
  exit 1
fi

if ! jq -e '.' "$MANIFEST" >/dev/null 2>&1; then
  fail "manifest is not valid JSON"
  exit 1
fi
ok "manifest is valid JSON"

# Required top-level fields. Scoop will reject the install on a missing
# version/url/hash, so each one is load-bearing.
for field in version description homepage license architecture autoupdate; do
  val="$(jq -r ".${field} // empty" "$MANIFEST")"
  if [[ -z "$val" ]]; then
    fail "top-level field .${field} missing or empty"
  else
    ok "top-level field .${field} present"
  fi
done

# Architecture coverage. Scoop selects per host arch — both 64bit and
# arm64 must have url + hash + bin or the install path is broken on
# half the Windows population.
for arch in 64bit arm64; do
  for sub in url hash bin; do
    val="$(jq -r ".architecture.\"${arch}\".${sub} // empty" "$MANIFEST")"
    if [[ -z "$val" ]]; then
      fail "architecture.${arch}.${sub} missing or empty"
    else
      ok "architecture.${arch}.${sub} present"
    fi
  done

  # bin alias must map the arch-named exe to "pincher" (so `scoop install`
  # exposes a single `pincher` command regardless of host arch).
  alias_name="$(jq -r ".architecture.\"${arch}\".bin[0][1] // empty" "$MANIFEST")"
  if [[ "$alias_name" != "pincher" ]]; then
    fail "architecture.${arch}.bin[0][1] = '${alias_name}', want 'pincher'"
  else
    ok "architecture.${arch}.bin[0] aliases to 'pincher'"
  fi
done

# Hash format: SHA256 hex strings are 64 lowercase hex chars.
for arch in 64bit arm64; do
  hash="$(jq -r ".architecture.\"${arch}\".hash" "$MANIFEST")"
  if [[ "$hash" =~ ^[a-f0-9]{64}$ ]]; then
    ok "architecture.${arch}.hash is well-formed sha256"
  else
    fail "architecture.${arch}.hash '${hash}' is not a 64-char lowercase hex sha256"
  fi
done

# Version-template consistency in autoupdate. Each arch's url + bin must
# use literal "\$version" so Scoop's autoupdater rebuilds them on a new
# release. Hash lookup must reference SHA256SUMS by the same template.
for arch in 64bit arm64; do
  url="$(jq -r ".autoupdate.architecture.\"${arch}\".url" "$MANIFEST")"
  if [[ "$url" == *'$version'* ]]; then
    ok "autoupdate.architecture.${arch}.url uses \$version template"
  else
    fail "autoupdate.architecture.${arch}.url missing \$version template: '${url}'"
  fi
  hash_url="$(jq -r ".autoupdate.architecture.\"${arch}\".hash.url" "$MANIFEST")"
  if [[ "$hash_url" == *'SHA256SUMS'* ]]; then
    ok "autoupdate.architecture.${arch}.hash.url points at SHA256SUMS"
  else
    fail "autoupdate.architecture.${arch}.hash.url should reference SHA256SUMS, got '${hash_url}'"
  fi
done

echo
echo "scoop-manifest_test.sh: $PASS pass, $FAIL fail"
if [[ $FAIL -gt 0 ]]; then
  exit 1
fi
