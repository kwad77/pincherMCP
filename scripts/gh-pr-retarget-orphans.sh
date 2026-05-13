#!/usr/bin/env bash
# gh-pr-retarget-orphans.sh — retarget stacked PRs whose base branch was deleted (#681 Bucket E1).
#
# Pain point this fixes:
#
#   PR #B is stacked on PR #A (base = feat-A). When #A merges with
#   --delete-branch, GitHub closes #B with "feat-A was deleted" or
#   silently retargets it depending on the merge method. In the silent-
#   retarget case, the PR diff suddenly includes #A's commits — confusing
#   reviewers. In the close case, #B is dead and you have to manually
#   reopen + retarget. Either way the operator pays attention cost during
#   exactly the moment they're trying to land a stack.
#
# This script: list open PRs whose base ref no longer exists on origin,
# print the rebase + retarget plan, and (with --apply) execute it.
#
# Usage:
#   scripts/gh-pr-retarget-orphans.sh                  # dry-run, list orphans
#   scripts/gh-pr-retarget-orphans.sh --apply          # rebase + retarget to master
#   scripts/gh-pr-retarget-orphans.sh --apply --base main   # retarget to a non-master default
#
# Exit codes:
#   0 — clean (no orphans, or --apply succeeded)
#   1 — orphans found in dry-run mode (operator should re-run with --apply)
#   2 — usage / git / gh error

set -euo pipefail

APPLY=0
TARGET_BASE="master"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --apply) APPLY=1; shift ;;
    --base)  TARGET_BASE="$2"; shift 2 ;;
    -h|--help)
      sed -n '2,24p' "$0"
      exit 0
      ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

# Need gh + git + jq. Fail fast with a useful message.
for cmd in gh git jq; do
  command -v "$cmd" >/dev/null 2>&1 || { echo "$cmd not found in PATH" >&2; exit 2; }
done

# Fetch the current set of remote branches so the orphan check uses
# fresh data — a rebase that just landed on origin shouldn't show as
# missing because our local tracking is stale.
git fetch --prune origin >/dev/null 2>&1 || {
  echo "git fetch failed; proceeding with stale data" >&2
}

# List every open PR with its number, head, and base. gh's default page
# size is 30; bump to 100 for repos that batch lots of stacked work.
PRS_JSON=$(gh pr list --state open --limit 100 --json number,headRefName,baseRefName 2>/dev/null) || {
  echo "gh pr list failed (auth? rate limit?)" >&2
  exit 2
}

ORPHANS=$(echo "$PRS_JSON" | jq -r --arg target "$TARGET_BASE" '
  .[] | select(.baseRefName != $target) |
  "\(.number)\t\(.headRefName)\t\(.baseRefName)"
')

if [[ -z "$ORPHANS" ]]; then
  echo "gh-pr-retarget-orphans: no PRs with non-$TARGET_BASE base — clean"
  exit 0
fi

# Filter to PRs whose base no longer exists on origin.
ORPHAN_LIST=""
while IFS=$'\t' read -r num head base; do
  if [[ -z "$num" ]]; then continue; fi
  # Check whether origin still has the base ref. `git ls-remote` returns
  # empty (and exit 0) for missing refs; non-empty for present refs.
  if ! git ls-remote --exit-code --heads origin "$base" >/dev/null 2>&1; then
    ORPHAN_LIST+="$num"$'\t'"$head"$'\t'"$base"$'\n'
  fi
done <<< "$ORPHANS"

if [[ -z "$ORPHAN_LIST" ]]; then
  echo "gh-pr-retarget-orphans: every non-$TARGET_BASE base still exists on origin — no orphans"
  exit 0
fi

echo "gh-pr-retarget-orphans: orphan PRs (base ref deleted from origin):"
echo ""
printf "%-6s  %-40s  %s\n" "PR" "head" "missing base"
printf "%-6s  %-40s  %s\n" "------" "----------------------------------------" "----------------------------------------"
while IFS=$'\t' read -r num head base; do
  if [[ -z "$num" ]]; then continue; fi
  printf "#%-5s  %-40s  %s\n" "$num" "$head" "$base"
done <<< "$ORPHAN_LIST"
echo ""

if [[ $APPLY -eq 0 ]]; then
  echo "Re-run with --apply to rebase + retarget each orphan onto $TARGET_BASE"
  exit 1
fi

# Apply path: for each orphan, retarget the PR base to $TARGET_BASE.
# We do NOT rebase the head branch ourselves — that needs the operator
# to confirm conflict resolution. Operator re-runs `gh pr checkout <N>`
# + `git rebase $TARGET_BASE` per PR after retarget if needed.
FAILED=0
while IFS=$'\t' read -r num head base; do
  if [[ -z "$num" ]]; then continue; fi
  echo "  retargeting PR #$num: $base → $TARGET_BASE"
  if ! gh pr edit "$num" --base "$TARGET_BASE" >/dev/null 2>&1; then
    echo "    FAILED — check PR state manually" >&2
    FAILED=$((FAILED + 1))
    continue
  fi
done <<< "$ORPHAN_LIST"

if [[ $FAILED -gt 0 ]]; then
  echo "gh-pr-retarget-orphans: $FAILED retarget(s) failed; see messages above" >&2
  exit 2
fi

echo ""
echo "gh-pr-retarget-orphans: done. Now per orphan:"
echo "  1. gh pr checkout <N>"
echo "  2. git rebase $TARGET_BASE   # resolve conflicts if any"
echo "  3. git push --force-with-lease"
exit 0
