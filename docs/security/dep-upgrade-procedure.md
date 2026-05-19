# Dependency upgrade procedure

This doc covers the operational mechanics of keeping pincher's dependencies current and the special-case path for upgrading the MCP SDK (which has a load-bearing wire-contract relationship with every MCP host).

## Cadence

- **Weekly:** the `govulncheck` workflow runs every Monday at 09:00 UTC against `master`. Failures page nobody — it's an advisory gate — but they should be addressed in a same-week PR. If a critical CVE drops, the workflow's normal PR/push triggers catch it before the Monday schedule.
- **Per-PR:** every PR runs the same `govulncheck` job. Advisory phase per FILE-F acceptance — failure doesn't block merge until one full release window of green runs proves the gate is stable.
- **Monthly:** maintainer runs `go list -m -u all` to survey non-CVE upgrades. Patch + minor upgrades that are non-breaking, well-tested, and reduce attack surface land in the next minor.

## Routine non-MCP-SDK upgrades

When `govulncheck` fires on a regular dep (gocodewalker, xxh3, etc.):

1. Identify the affected module from the workflow output. The `-show traces` flag prints the call path, so you can verify pincher actually reaches the vulnerable symbol.
2. If a fixed version exists upstream: `go get -u <module>@<fixed-version>` + `go mod tidy`.
3. Run `go test ./...` locally. Run `make corpus-test` to confirm extraction snapshots don't drift.
4. Open a PR titled `chore(deps): bump <module> to <version> for CVE-XXXX-YYYYY (#NNN)`.
5. Merge after CI green.

When no fixed version exists upstream AND the affected symbol IS reachable from pincher: file a SECURITY.md exception entry pinning the unfixed dep + a tracking issue. Re-check at every release.

When no fixed version exists AND the affected symbol is NOT reachable: same exception entry, but note the unreachability evidence (the `-show traces` output proves it).

## MCP SDK upgrade — special path

The MCP SDK (`github.com/modelcontextprotocol/go-sdk`) sits on the wire contract pincher publishes to MCP hosts (Claude Code, Cursor, Codex, JetBrains, vscode-copilot, zed). An SDK upgrade can change:

- The JSON-RPC envelope shape (rare but possible across major SDK versions)
- The `Tool` struct fields (`Icons`, `Annotations`, etc. arrive in SDK minors)
- The MCP capabilities the server advertises during the initialize handshake
- The transport behavior (stdio, streamable HTTP — both wired through the SDK)

A naive `go get -u github.com/modelcontextprotocol/go-sdk` is therefore higher-risk than a normal dep bump. The procedure:

1. **Read the SDK changelog and migration guide.** The SDK ships CHANGELOG-style release notes; the major-version entries call out breaking changes explicitly.
2. **Test against every supported host.** Run the per-host canonical workflows from FILE-M (#1532 v0.87) — if those pass, the SDK upgrade preserves wire compatibility for the surface pincher promises.
3. **Bump in a PR by itself.** Do not bundle an SDK upgrade with other changes; if a wire-shape regression slips, the bisect must point at the SDK, not at a sibling commit.
4. **Soak in dev for one full minor.** Tag a `.x9` hardening release with the new SDK, dogfood for the full hardening window, then promote to `.x0` stable.
5. **Document the upgrade in CHANGELOG.** Users of pincher-as-a-library (the planned plugin surface in FILE-V) need to know.

The current SDK pin is in `go.mod`. As of v0.85: `github.com/modelcontextprotocol/go-sdk v1.4.0`.

## Exception management

`SECURITY.md` (forthcoming with FILE-D #1523 v0.87) is the registry for unfixed vulnerabilities pincher has triaged. Each entry carries:

- The CVE ID
- The affected dep + version
- Whether the affected symbol is reachable (with `-show traces` evidence)
- The accepted-risk rationale OR the planned upgrade window
- A re-check date

Exceptions are reviewed at every release-prep audit. An exception that's been "accepted" for more than one minor without progress gets escalated to a hardening-release slot.

## Refs

- Issue: [#1525 (FILE-F)](https://github.com/kwad77/pincher/issues/1525)
- Sibling: [#1524 (FILE-E)](https://github.com/kwad77/pincher/issues/1524) artifact signing
- Workflow: `.github/workflows/govulncheck.yml`
- FILE-V (#1540) plugin surface decision — plugin authors need to know SDK upgrade cadence too
