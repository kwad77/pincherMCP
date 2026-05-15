# Pinned corpus: `python-app`

A small synthetic Python project used by the corpus-snapshot tooling (#33) to
lock in pincher's behaviour on a Python AST extractor. Hand-crafted so:

- The expected snapshot stays stable across pincher upgrades (no upstream
  drift).
- Symbol counts are small enough to eyeball-verify when the snapshot diff
  reports a change.
- Cross-file CALLS / IMPORTS shapes are deliberate — every edge in
  `python-app.snapshot.json` traces back to a specific construction here.

If pincher learns a new Python feature (decorators, async, dataclasses,
type aliases, etc.) and the symbol count for this corpus changes,
regenerate the snapshot via `make corpus-snapshot-update` AND review the
diff in the same PR — the review IS the rationale for the change.

## Layout

- `pyproject.toml` — establishes the project root so `python_resolve.go`
  picks `app/` as a source root (flat layout, no `src/`).
- `app/__init__.py` — package marker for `app`.
- `app/auth.py` — `Session` class with `__init__` method + helper
  `Method`, plus module-level `open_session` function.
- `app/main.py` — `main()` entry point that imports `app.auth` and calls
  `open_session(...)`. Async function `run_async()` exercises the
  AsyncFunctionDef path. Cross-file IMPORTS + CALLS edges are the
  canonical regression test for Python cross-file resolution.

## Why this matters

Pre-#1057 the only Python tests against the live extractor were
in-process Go tests with synthetic input strings. No end-to-end snapshot
gate existed for Python — the kind/symbol counts could drift silently
through any Python extractor change. This corpus closes that gap:
the snapshot pins one project's worth of Python extraction so the
review diff catches any unexpected delta.
