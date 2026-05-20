#!/usr/bin/env bash
# scripts/comparator-raw-readgrep.sh — FILE-B #1521 v0.86, §2 corpus #1298 v0.89.
#
# Code-intelligence comparator harness. For each canonical agent-loop
# task it runs the pincher tool call and the equivalent non-pincher
# approaches, recording what would have entered the agent's context
# window (wall-clock ms + a byte token-proxy).
#
# Comparators:
#   raw    — the Read+Grep loop: an agent with only grep / find / cat,
#            no code-intel tool at all.
#   ctags  — universal-ctags: a symbol-definition index (comparable to
#            pincher's layer 1). Has no call graph and extracts no
#            symbol bodies — so it answers find-symbol compactly but
#            cannot answer find-callers and gives no better than a
#            whole-file read for read-with-context. That asymmetry is
#            the point: it isolates what pincher's layers 2 (graph) and
#            3 (byte-offset retrieval) add over a plain tags index.
#
# Per side it captures wall-clock ms, a byte token-proxy, and an
# accuracy flag — did the tool's output actually contain the right
# answer (checked against a grep-based oracle independent of any one
# tool)? Tokens + latency say how cheap; accuracy says whether it
# worked.
#
# Output: JSON {schema_version, captured_at, corpus, tasks:[{task,
#   pincher_ms, comparator_ms, ctags_ms, pincher_bytes,
#   comparator_bytes, ctags_bytes, pincher_accurate, comparator_accurate,
#   ctags_accurate, bytes_ratio}]}. ctags_* are null when ctags is not
#   installed or cannot answer the task.
#
# #1298 v0.89: the v0.86 runner never executed a single task — four
# bugs each aborted it before any measurement (see the inline notes at
# the server-setup block). This revision fixes them, grows the corpus
# from 1 placeholder task to 3 canonical agent-loop shapes, and adds
# universal-ctags as a second comparator.

set -euo pipefail

PINCHER_BIN="${PINCHER_BIN:-$(command -v pincher 2>/dev/null || echo "")}"
if [ -z "${PINCHER_BIN}" ] || [ ! -x "${PINCHER_BIN}" ]; then
  echo "::error::PINCHER_BIN not set and no pincher on PATH" >&2
  exit 2
fi

if ! command -v jq >/dev/null 2>&1; then
  echo "::error::jq not on PATH — comparator needs jq" >&2
  exit 2
fi

CORPUS="${1:?path to corpus (a project directory to index) required: $0 <corpus> [out.json]}"
if [ ! -d "${CORPUS}" ]; then
  echo "::error::corpus '${CORPUS}' is not a directory" >&2
  exit 2
fi
OUT="${2:-out/comparator.json}"
mkdir -p "$(dirname "${OUT}")"

TARGET_SYMBOL="${TARGET_SYMBOL:-Open}"
# pincher derives a project's name from its directory basename; the
# HTTP tools scope by that name.
PROJECT="$(basename "${CORPUS}")"

# ── Server setup ─────────────────────────────────────────────────────
# Four bugs made the v0.86 runner a no-op; all four are fixed here:
#   1. The server was started as `pin_resp=$(pincher ... &)` — the `&`
#      backgrounds inside the command-substitution subshell, so `$!`
#      in this shell was unbound and `set -u` aborted immediately.
#   2. `pincher --http` also runs the MCP stdio loop; a backgrounded
#      process whose stdin EOFs at once triggers the graceful stdio
#      shutdown and the whole process (HTTP included) exits. `--no-stdio`
#      runs HTTP-only and survives.
#   3. `--http :0` binds all interfaces, which pincher default-denies
#      without --http-key (#199). `--http 127.0.0.1:0` is loopback-only
#      and needs no auth.
#   4. `pincher --data-dir D index PATH` puts `--data-dir` in argv[1],
#      so the `index` subcommand is never detected and pincher runs as
#      the MCP server, indexing nothing. The subcommand must lead:
#      `pincher index PATH --data-dir D`.
COMP_DATA="${COMP_DATA:-$(mktemp -d -t comparator-XXXXXX)}"
COMP_LOG="${COMP_DATA}/srv.log"
PIN_PID=""
cleanup() {
  if [ -n "${PIN_PID}" ]; then
    kill -TERM "${PIN_PID}" 2>/dev/null || true
    # Block until the gateway has actually exited and released its
    # SQLite handles, otherwise `rm` races the still-open DB file.
    wait "${PIN_PID}" 2>/dev/null || true
  fi
  rm -rf "${COMP_DATA}"
}
trap cleanup EXIT

# Index the corpus first, into a populated DB the gateway then reads.
"${PINCHER_BIN}" index "${CORPUS}" --data-dir "${COMP_DATA}" >/dev/null 2>&1

# Start the loopback HTTP-only gateway.
"${PINCHER_BIN}" --data-dir "${COMP_DATA}" --no-stdio --http 127.0.0.1:0 \
  >"${COMP_LOG}" 2>&1 &
PIN_PID=$!

addr=""
for _ in $(seq 1 50); do
  addr=$(grep -oE '127\.0\.0\.1:[0-9]+' "${COMP_LOG}" 2>/dev/null | head -1 || true)
  [ -n "${addr}" ] && break
  sleep 0.2
done
if [ -z "${addr}" ]; then
  echo "::error::pincher HTTP gateway did not bind" >&2
  cat "${COMP_LOG}" >&2 || true
  exit 1
fi
API="http://${addr}"

# Build the ctags symbol index once (mirrors pincher's index step).
# When ctags is absent the comparator still runs; its columns go null.
CTAGS_TAGS=""
if command -v ctags >/dev/null 2>&1; then
  CTAGS_TAGS="${COMP_DATA}/tags"
  if ! ctags -R -f "${CTAGS_TAGS}" "${CORPUS}" >/dev/null 2>&1; then
    CTAGS_TAGS=""
  fi
fi
if [ -z "${CTAGS_TAGS}" ]; then
  echo "::warning::universal-ctags unavailable — ctags comparator columns will be null" >&2
fi

# ── helpers ──────────────────────────────────────────────────────────
# pin_call TOOL JSON → response body on stdout.
pin_call() {
  curl -fsSL -m 10 -H 'Content-Type: application/json' -d "$2" "${API}/v1/$1"
}

# byte_len → length in bytes of stdin.
byte_len() { wc -c | tr -d ' '; }

# now_ms → wall clock in milliseconds.
now_ms() { echo $(( $(date +%s%N) / 1000000 )); }

# accurate OUTPUT EXPECT → "true" if OUTPUT contains the literal EXPECT
# token, else "false". The accuracy dimension answers "would the agent,
# from this tool's output, actually have the right answer?" — distinct
# from the byte token-proxy, which only measures how much entered
# context. EXPECT is derived per task from a grep-based oracle
# independent of any single tool, so no comparator is scored against
# its own output.
accurate() {
  if [ -n "$2" ] && printf '%s' "$1" | grep -qF -- "$2"; then
    echo true
  else
    echo false
  fi
}

# ctags_tag_lines NAME → tag lines whose first tab-field is exactly NAME.
ctags_tag_lines() {
  [ -n "${CTAGS_TAGS}" ] || return 0
  awk -F'\t' -v n="$1" '$1 == n' "${CTAGS_TAGS}" 2>/dev/null || true
}

# sum_grep_file_bytes → reads `grep -rn` output on stdin, sums `wc -c`
# of each DISTINCT matched file (an agent Reads each file once).
sum_grep_file_bytes() {
  awk -F: 'NF > 1 && !seen[$1]++ { print $1 }' \
    | while IFS= read -r fp; do [ -f "${fp}" ] && wc -c < "${fp}"; done \
    | awk '{ s += $1 } END { print s + 0 }'
}

results='[]'

# record_task NAME PIN_MS COMP_MS PIN_BYTES COMP_BYTES CTAGS_MS CTAGS_BYTES \
#             PIN_ACC COMP_ACC CTAGS_ACC
# CTAGS_* may be the literal `null` (ctags absent, or a task ctags
# structurally cannot answer). *_ACC are `true` / `false` / `null`.
record_task() {
  local name="$1" pm="$2" cm="$3" pb="$4" cb="$5" ctm="${6:-null}" ctb="${7:-null}"
  local pa="${8:-null}" ca="${9:-null}" cta="${10:-null}"
  local ratio=0
  if [ "${cb}" -gt 0 ] && [ "${pb}" -gt 0 ]; then
    ratio=$(awk -v c="${cb}" -v p="${pb}" 'BEGIN { printf "%.2f", c / p }')
  fi
  echo "  pincher: ${pm}ms, ${pb} bytes, accurate=${pa}"
  echo "  raw:     ${cm}ms, ${cb} bytes, accurate=${ca}"
  if [ "${ctm}" = "null" ]; then
    echo "  ctags:   n/a (ctags cannot answer this task)"
  else
    echo "  ctags:   ${ctm}ms, ${ctb} bytes, accurate=${cta}"
  fi
  results=$(jq \
    --arg task "${name}" \
    --argjson pm "${pm}" --argjson cm "${cm}" \
    --argjson pb "${pb}" --argjson cb "${cb}" \
    --argjson ctm "${ctm}" --argjson ctb "${ctb}" \
    --argjson pa "${pa}" --argjson ca "${ca}" --argjson cta "${cta}" \
    --arg r "${ratio}" \
    '. + [{task: $task,
           pincher_ms: $pm, comparator_ms: $cm, ctags_ms: $ctm,
           pincher_bytes: $pb, comparator_bytes: $cb, ctags_bytes: $ctb,
           pincher_accurate: $pa, comparator_accurate: $ca, ctags_accurate: $cta,
           bytes_ratio: ($r | tonumber)}]' \
    <<< "${results}")
}

# ── Task 1: find-symbol-by-name ──────────────────────────────────────
# Pincher: search → id + signature, one targeted response.
# raw:     `grep -rn "func TARGET"` — what enters context is grep's own
#          output (the agent only needs to LOCATE the symbol here).
# ctags:   the tag line(s) for the exact symbol name — a compact record
#          equivalent to pincher's layer-1 lookup.
echo "── Task 1: find-symbol-by-name (${TARGET_SYMBOL}) ──"
t0=$(now_ms)
search_resp=$(pin_call search \
  "{\"query\":\"${TARGET_SYMBOL}\",\"project\":\"${PROJECT}\",\"limit\":5}")
t1=$(now_ms)
pin_bytes=$(printf '%s' "${search_resp}" | byte_len)

t0c=$(now_ms)
grep_out=$(grep -rn "func ${TARGET_SYMBOL}" "${CORPUS}" || true)
t1c=$(now_ms)
comp_bytes=$(printf '%s' "${grep_out}" | byte_len)

ct_ms=null ct_bytes=null ct_lines=""
if [ -n "${CTAGS_TAGS}" ]; then
  t0t=$(now_ms)
  ct_lines=$(ctags_tag_lines "${TARGET_SYMBOL}")
  t1t=$(now_ms)
  ct_ms=$((t1t - t0t))
  ct_bytes=$(printf '%s' "${ct_lines}" | byte_len)
fi

# Accuracy oracle: the file that defines `func TARGET`. A tool is
# accurate on find-symbol if its output names that file.
def_file=$(grep -rl "func ${TARGET_SYMBOL}" "${CORPUS}" 2>/dev/null | head -1 || true)
def_base=$(basename "${def_file:-}")
pin_acc=$(accurate "${search_resp}" "${def_base}")
comp_acc=$(accurate "${grep_out}" "${def_base}")
ct_acc=null
[ -n "${CTAGS_TAGS}" ] && ct_acc=$(accurate "${ct_lines}" "${def_base}")

record_task "find-symbol-by-name" "$((t1 - t0))" "$((t1c - t0c))" \
  "${pin_bytes}" "${comp_bytes}" "${ct_ms}" "${ct_bytes}" \
  "${pin_acc}" "${comp_acc}" "${ct_acc}"

# ── Task 2: read-with-context ────────────────────────────────────────
# Pincher: context → the symbol's source plus its imports/callees in
#          one response.
# raw:     the agent has located the symbol; to READ it with context it
#          must `cat` the whole file(s) — context = full file bytes.
# ctags:   the tag pinpoints the file, but ctags extracts no body, so
#          the agent still `cat`s the whole file — context = that file.
echo "── Task 2: read-with-context (${TARGET_SYMBOL}) ──"
sym_id=$(printf '%s' "${search_resp}" | jq -r '.results[0].id // empty')
if [ -n "${sym_id}" ]; then
  t0=$(now_ms)
  ctx_resp=$(pin_call context \
    "{\"id\":\"${sym_id}\",\"project\":\"${PROJECT}\"}")
  t1=$(now_ms)
  pin_bytes=$(printf '%s' "${ctx_resp}" | byte_len)

  t0c=$(now_ms)
  decl_grep=$(grep -rn "func ${TARGET_SYMBOL}" "${CORPUS}" || true)
  t1c=$(now_ms)
  comp_bytes=$(printf '%s' "${decl_grep}" | sum_grep_file_bytes)

  ct_ms=null ct_bytes=null ct_lines=""
  if [ -n "${CTAGS_TAGS}" ]; then
    t0t=$(now_ms)
    ct_lines=$(ctags_tag_lines "${TARGET_SYMBOL}")
    ct_file=$(printf '%s' "${ct_lines}" | awk -F'\t' 'NR==1 { print $2 }')
    t1t=$(now_ms)
    ct_ms=$((t1t - t0t))
    if [ -n "${ct_file}" ] && [ -f "${ct_file}" ]; then
      ct_bytes=$(wc -c < "${ct_file}" | tr -d ' ')
    else
      ct_bytes=0
    fi
  fi

  # Accuracy oracle: the symbol's declaration line is present in the
  # output. A tool is accurate on read-with-context if its output
  # actually carries `func TARGET` (the ctags tag's pattern field
  # echoes the declaration line, so the tag record satisfies this).
  expect_decl="func ${TARGET_SYMBOL}"
  pin_acc=$(accurate "${ctx_resp}" "${expect_decl}")
  comp_acc=$(accurate "${decl_grep}" "${expect_decl}")
  ct_acc=null
  [ -n "${CTAGS_TAGS}" ] && ct_acc=$(accurate "${ct_lines}" "${expect_decl}")

  record_task "read-with-context" "$((t1 - t0))" "$((t1c - t0c))" \
    "${pin_bytes}" "${comp_bytes}" "${ct_ms}" "${ct_bytes}" \
    "${pin_acc}" "${comp_acc}" "${ct_acc}"
else
  echo "::warning::task read-with-context skipped — search returned no id for ${TARGET_SYMBOL}" >&2
fi

# ── Task 3: find-callers ─────────────────────────────────────────────
# Pincher: query (pinchQL) → the resolved CALLS graph, callers only.
# raw:     `grep -rn "TARGET("` finds candidate call sites; a bare grep
#          can't tell a real call from a comment / string / shadowed
#          name, so the agent `cat`s each matched file to confirm —
#          context = full file bytes of every candidate-match file.
# ctags:   a definition index has NO call graph. ctags structurally
#          cannot answer find-callers → null. This is the layer-2 gap.
echo "── Task 3: find-callers (${TARGET_SYMBOL}) ──"
t0=$(now_ms)
query_resp=$(pin_call query \
  "{\"pinchql\":\"MATCH (caller)-[:CALLS]->(callee) WHERE callee.name = \\\"${TARGET_SYMBOL}\\\" RETURN caller.qualified_name, caller.file_path\",\"project\":\"${PROJECT}\"}")
t1=$(now_ms)
pin_bytes=$(printf '%s' "${query_resp}" | byte_len)

t0c=$(now_ms)
call_grep=$(grep -rn "${TARGET_SYMBOL}(" "${CORPUS}" || true)
t1c=$(now_ms)
comp_bytes=$(printf '%s' "${call_grep}" | sum_grep_file_bytes)

# Accuracy oracle: a real caller file — a file (other than the
# definition file) that contains a `TARGET(` call site. A tool is
# accurate on find-callers if its output names that caller.
caller_file=$(grep -rl "${TARGET_SYMBOL}(" "${CORPUS}" 2>/dev/null \
  | { [ -n "${def_file:-}" ] && grep -vF -- "${def_file}" || cat; } \
  | head -1 || true)
caller_base=$(basename "${caller_file:-}")
pin_acc=$(accurate "${query_resp}" "${caller_base}")
comp_acc=$(accurate "${call_grep}" "${caller_base}")

# ctags has no caller graph — bytes and accuracy both null.
record_task "find-callers" "$((t1 - t0))" "$((t1c - t0c))" \
  "${pin_bytes}" "${comp_bytes}" null null \
  "${pin_acc}" "${comp_acc}" null

# ── Emit ─────────────────────────────────────────────────────────────
jq \
  --argjson r "${results}" \
  --arg ts "$(date -u +%FT%TZ)" \
  --arg corp "${CORPUS}" \
  '{schema_version: 4, captured_at: $ts, corpus: $corp, tasks: $r}' \
  <<< '{}' > "${OUT}"

echo
echo "Wrote ${OUT}:"
cat "${OUT}"
