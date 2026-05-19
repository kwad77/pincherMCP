# Security threat model (v0.87+)

Pincher is a local code-intelligence MCP server. It runs on developer laptops, in CI containers, and (rarely) as a long-lived HTTP-gateway service. This doc enumerates the attacker surfaces pincher exposes, the current mitigation status of each, and what's deferred to v1.x or out of scope.

**Status:** First draft. Maintained inline with each release. Re-audited at the v0.84 API freeze checkpoint and at v0.90 stable promotion.

**Scope:** This is pincher's own threat model — what an attacker can do to a pincher install. It is NOT a comprehensive security review of the host language ecosystems pincher analyzes. A malicious file pincher indexes can't escape to the host; a malicious host calling pincher's MCP surface can.

## Trust boundary

| Trusted | Untrusted |
|---|---|
| The developer who launched pincher | The agent (LLM) calling MCP tools |
| The local filesystem under the project root | The contents of any indexed file (treated as data, never executed) |
| The OS process pincher runs as | Anyone reaching the HTTP gateway port |
| The pincher binary itself | Network responses to the `fetch` tool |

Pincher's threat model assumes the developer running it is trusted. A malicious developer can do anything pincher can — including delete indexed projects, modify the SQLite store directly, or alter the binary. That's not in scope.

## Surfaces

### 1. HTTP gateway (`--http :PORT`)

**What it does:** Serves the REST API mirroring every MCP tool plus the dashboard at `/dashboard`. Optional bearer-token auth via `--http-key`.

**Attacker model:** Anyone who can reach the port pincher's HTTP gateway listens on. Default bind is loopback only (`127.0.0.1:PORT`); a misconfiguration could expose it to a LAN or to the public Internet.

**Mitigations in place:**
- Bind defaults to `127.0.0.1` unless an explicit interface is named.
- `--http-key` enables bearer-token auth for all `/v1/*` and `/dashboard` routes.
- DNS-rebinding protection in the `fetch` tool (#844) — a remote attacker can't trick pincher's `fetch` into resolving to localhost.
- Bounded per-call payload size; the per-tool payload-outlier advisory (#635) surfaces anomalous tool responses for review.
- CORS is restrictive — no cross-origin POSTs by default.

**Known gaps:**
- The default no-auth localhost mode is convenient for developers but means a local malicious process can talk to the gateway without a token. Mitigation requires `--http-key` or a unix-domain-socket transport (post-v1.0).
- Rate limiting is absent. A bug in agent code that loops infinitely against `/v1/query` will saturate the CPU. v1.x feature.

**Deferred:** TLS termination is out of scope — front pincher with nginx / Caddy / Cloudflare for production HTTPS. Documented in `docs/deployment/`.

### 2. `fetch` tool (HTTP outbound)

**What it does:** Fetches an external HTTP/S resource on behalf of the agent. Returns headers + truncated body. Backed by an opt-in allowlist or "off by default" outbound policy.

**Attacker model:** An agent that has been prompt-injected may try to use `fetch` for (a) SSRF against internal services, (b) data exfiltration of indexed code to attacker-controlled URLs, (c) reconnaissance of the developer's network.

**Mitigations in place:**
- DNS-rebinding hardening (#844, v0.56) — IP literals are checked against the loopback/private-network/link-local ranges and refused. The DNS A/AAAA lookup is performed once, and the resolved IP is the one actually dialed.
- `localhost`, `127.0.0.0/8`, `::1`, `169.254.0.0/16`, `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `fc00::/7` are blocked by default. The block list is in `internal/server/fetch.go`.
- `file://`, `ftp://`, `gopher://`, and other non-HTTP schemes are refused.
- Response size is bounded.
- Each fetch call is logged via slog so post-hoc audit is possible.

**Known gaps:**
- No per-host rate limit. A malicious agent could enumerate public hosts via repeated `fetch` calls.
- The allowlist mechanism (if needed for production: only allow `fetch` against documented APIs) is not yet wired. v1.x.

**Deferred:** SOCKS proxy support. If a developer needs all outbound traffic through a corporate proxy, configure at the OS / `HTTPS_PROXY` env var level — pincher honors stdlib `http.Transport` defaults.

### 3. Cypher query injection (`query` tool)

**What it does:** Takes a pinchQL string from the agent and runs it through `internal/cypher/engine.go`. Translates to SQLite SQL internally; the SQL is never directly user-provided.

**Attacker model:** A prompt-injected agent submits a pinchQL string designed to escape the translation layer into raw SQL — read `_meta` tables, exfiltrate stored ADRs, modify the schema_version.

**Mitigations in place:**
- pinchQL parser is hand-rolled in `internal/cypher` — it does not pass arbitrary user input to SQL. Every value extracted from the parsed query is bound as a parameter (`?`), never string-concatenated into the SQL string.
- The pinchQL grammar deliberately excludes SQL keywords (UPDATE, DELETE, ALTER, ATTACH, PRAGMA). Attempts to use them produce parse errors before reaching the SQL layer.
- The reader connection pool (`s.ro`) is the only path query takes — no writes are reachable from `query`.

**Known gaps:**
- A complex pinchQL expression that exhausts the recursive-CTE recursion limit can DoS the query process (consuming wall-clock until SQLite's stack limit fires). Mitigated indirectly by per-call timeouts; per-query depth limits are a v1.x enhancement.

**Deferred:** Read-only SQLite handle for `query` (currently the reader pool is read-only at the connection level but the handle is shared with maintenance code). Post-v1.0.

### 4. Path traversal (`init`, `index`, `fetch`)

**What it does:** Several tools take filesystem paths from the agent (`init` writes config files; `index` reads source trees; `fetch` writes downloaded blobs to a cache).

**Attacker model:** A prompt-injected agent passes a path like `../../../../etc/passwd` or `~/.ssh/id_rsa` hoping to read sensitive files OR a write-path like `../../../etc/passwd` to overwrite system files.

**Mitigations in place:**
- `index` calls `filepath.Abs(path)` + ancestor `.git`-walking — out-of-tree symlinks are not followed (audited in #41, `internal/index/symlink_safety_test.go`).
- `init`'s write surface is bounded to the documented per-target paths (`.claude/config.json`, `CLAUDE.md`, `.cursor/rules/`, etc.). Arbitrary writes are not reachable.
- `bloat_trap.go` (`internal/index/bloat_trap.go`) refuses indexing of the filesystem root, the user's home directory, and (in hook-mode) any path lacking a project marker (`.git`, `go.mod`, etc.).
- `os.ReadFile` calls in symbol byte-offset retrieval are bounded to file paths already in the indexed `symbols` table — the agent can't ask for arbitrary file contents via `symbol` or `context`.

**Known gaps:**
- The `fetch` tool writes downloaded blobs to a cache directory under the pincher data dir. Path-traversal in the cached filename has been mitigated (#844 family) but warrants a follow-up audit.

**Deferred:** A "deny-listed paths" config for `init` (so a developer can configure their pincher install to refuse writes to certain locations) is a v1.x feature.

### 5. Credential exposure (stats / health / doctor)

**What it does:** These diagnostic tools surface metadata about indexed projects, slow queries, recent extraction failures, and dashboard panel data.

**Attacker model:** A user pastes the JSON output of `pincher doctor --json` or `pincher hook-stats --export-7d` to a public issue tracker or chat channel without realizing it contained credentials.

**Mitigations in place:**
- Credential redaction in slow-query argument strings (#607). Strings matching common credential shapes (Bearer tokens, API key patterns) are replaced with `<redacted>` before logging.
- `pincher hook-stats --export-7d` is explicitly opt-in (default: emit nothing) and anonymises by default — no project paths, no hostnames, no usernames unless `--include-host` is set.
- The dashboard's slow-query panel applies the same redaction.
- File paths in the dashboard are project-relative, not absolute.

**Known gaps:**
- ADR `value` fields are stored verbatim. A developer who pastes a secret into an ADR (`adr set api-key sk-...`) will see it surface in `adr list` and in `doctor --json` if the diagnostic block is widened. Documented as a known limitation.

**Deferred:** A configurable redaction-regex list (let operators add organization-specific credential patterns). v1.x.

### 6. Watcher + indexer path traversal

**What it does:** The indexer's `Watch()` poll walks project trees on a 2s/30s rhythm.

**Attacker model:** A malicious file dropped into a project being watched — designed to crash the indexer parser, exfiltrate data via side-effect, or escape into another process.

**Mitigations in place:**
- Every extractor parses files as pure data — no `exec`, no `eval`, no template rendering against the file's content.
- The Python AST dispatcher (#856) is the only extractor that spawns a subprocess — `python -c <stdin>` is fed the user's file but the subprocess is sandboxed (no network, no fs writes beyond stdout) and bounded by a 2s timeout.
- Jinja2 parsing (gonja) has a 2s parse timeout to prevent crafted templates from hanging the indexer.
- The walker (`gocodewalker`) respects `.gitignore` AND pincher's `ast/blocklist.go` AND the bloat-trap refusal of root/home paths.

**Known gaps:**
- `os.Stat` calls during the watcher's poll cycle aren't bounded by a timeout. A malicious filesystem (e.g. a FUSE mount that hangs on stat) could DoS the watcher. Real-world risk is low.

**Deferred:** Per-extractor allowlisting (block specific extractors on a per-project basis). Not a v1.0 concern.

## Reachability and dependency vulnerabilities

When `govulncheck` (per FILE-F) fires a CVE for a transitive dep, the operator runs `govulncheck -test -show traces` to see whether the affected symbol is on a reachable code path in pincher.

- **Reachable** vulns get an upgrade PR in the next release window (or sooner if critical).
- **Unreachable** vulns get a SECURITY.md exception entry naming the unreachability evidence + a re-check date.

`SECURITY.md` is the registry — exceptions reviewed at every release-prep audit. Exceptions older than one minor without progress escalate to a hardening-release slot.

## What's not pincher's threat model

- **Malicious source files crashing the indexer.** That's a quality bug, not a security bug. Extractor robustness is tracked via the extraction_failures pipeline. A crash is filed, not CVE-tracked.
- **The host LLM's prompt injection.** Pincher doesn't reason about whether an LLM has been jailbroken. Mitigation lives at the MCP host layer (Claude Code / Cursor / etc.) — pincher provides only the tools, not the trust assessment.
- **The agent reading code it shouldn't.** Pincher indexes what the user asked it to. If a user indexed a directory containing secrets, the agent CAN read those secrets via `context` or `search` — that's expected behavior. Don't index directories containing secrets.
- **Supply chain attack on pincher's own binary.** Tracked separately via FILE-E (#1524) — release artifact signing + checksums + SLSA L1.

## Refs

- Issue: [#1523 (FILE-D)](https://github.com/kwad77/pincher/issues/1523)
- Pairs with FILE-E (#1524) artifact verification + FILE-F (#1525) govulncheck CI gate
- DNS-rebinding fix: [#844](https://github.com/kwad77/pincher/issues/844) v0.56
- Credential redaction: [#607](https://github.com/kwad77/pincher/issues/607)
- Symlink safety audit: [#41](https://github.com/kwad77/pincher/issues/41)
- Bloat-trap: `internal/index/bloat_trap.go`
- Per-target init write surface: `internal/init/`
