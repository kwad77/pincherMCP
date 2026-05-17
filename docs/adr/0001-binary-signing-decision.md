# ADR-0001: Binary signing — defer to v1.0+ certificate purchase; document bypass for v0.x

**Status:** Accepted
**Date:** 2026-05-17
**Decision-maker:** kwad77 (sole maintainer through v0.x)
**Issue:** [#1260 §2](https://github.com/kwad77/pincher/issues/1260)
**Supersedes:** none

## Context

Pre-v0.69 release artifacts (macOS / Linux tarballs, Windows zip, ghcr.io Docker image) ship unsigned. First-run security gates on user machines:

- **macOS Gatekeeper** quarantines downloaded binaries. The user sees *"pincher.app cannot be opened because the developer cannot be verified"* and must right-click → Open, or run `xattr -d com.apple.quarantine pincher` to clear the quarantine bit.
- **Windows SmartScreen** flags the binary with the *"Microsoft Defender SmartScreen prevented an unrecognized app from starting"* dialog. User clicks "More info" → "Run anyway".
- **Linux** has no equivalent gate. SHA256SUMS published with each release is sufficient.

These gates add real friction to first install but are stable across `pincher update` runs — the quarantine + SmartScreen attributes only fire on the initial download. Existing Homebrew users get the bypass for free (Homebrew clears the attribute on `brew install`). Docker users on ghcr.io never see either gate.

## Options considered

### Option A — Pay for proper code signing

- **Apple Developer ID Certificate**: $99 / year. Removes Gatekeeper friction permanently. Requires the Apple Developer enrollment, which needs an Apple ID and either a DUNS number (for organizations) or government ID (for individuals). Signing wiring slots into `.github/workflows/release.yml` via `xcrun notarytool` after the binary is built; secrets go in GitHub Actions encrypted secrets.
- **Windows Authenticode Certificate**: $200-400 / year depending on issuer (DigiCert, Sectigo, SSL.com). Removes SmartScreen friction. Some EV certs cost more but give instant reputation; standard OV certs build reputation gradually over hundreds of installs. Signing happens via `signtool.exe` (Windows runner) or `osslsigncode` (Linux runner with the .pfx in secrets).
- **Total**: ~$300-500 / year recurring, plus the one-time enrollment friction (passport scan, business verification, etc.).

### Option B — Document the curl-based bypass in install docs

- macOS: `curl -fsSL <url> | tar xz && xattr -d com.apple.quarantine ./pincher && ./pincher --version`
- Windows: install via Scoop (in-progress, #1260 §1) which avoids the quarantine path entirely. For direct download, document the SmartScreen "Run anyway" two-click flow.
- Linux: no change; SHA256SUMS already lives next to the binaries.
- **Total**: zero recurring cost, but every first-time user hits friction.

### Option C — Hybrid: defer signing decision until product hits v1.0; document bypass for v0.x

This is the path chosen. Pre-1.0 with ~2 users, the cost of certificate enrollment + secrets-management ceremony outweighs the user-acquisition value of removing the gate. Documented bypass is sufficient for the current user population (technical developers comfortable with terminal install steps). Revisit at v1.0 when:

- the user count justifies the recurring cost,
- the certificate-management overhead doesn't dominate maintainer time,
- packaging-channel coverage (Homebrew, Scoop, ghcr) means most users never see the unsigned-binary path anyway.

## Decision

**Defer the certificate purchase until v1.0+ promotion. For v0.x, document the bypass in install docs.**

Codification:

1. `packaging/README.md` already covers Homebrew (no bypass needed — brew handles quarantine) and Docker (no bypass needed — Docker pulls signed-by-registry images). This ADR triggers two more sections: macOS direct download (with `xattr` one-liner) and Windows direct download (with SmartScreen "More info" → "Run anyway" screenshot path).
2. The release-prep checklist in `CLAUDE.md` does NOT need a signing step until v1.0.
3. The decision is revisited at v0.95 (the v1.0 release-prep cycle) to give planning lead time.

## Consequences

**Positive**:
- Zero recurring cost during v0.x stabilization.
- No enrollment-friction blocker for v0.70 stable promotion.
- Reversible — purchasing certificates later doesn't break anything that's been shipped.

**Negative**:
- Every first-time macOS / Windows direct-install user hits the OS gate. Adoption friction.
- Documented bypass is a "Run anyway" instruction, which is a click-through users have to make consciously. Some will bail.
- Brand perception: "unsigned binary" reads as less professional than signed.

**Mitigations**:
- Homebrew (macOS) + Scoop (Windows, #1260 §1) + Docker (cross-platform server) cover most install paths. Direct-download users are the minority.
- The bypass instructions are clear (single-line `xattr`, or one extra click for SmartScreen).
- "Verify the SHA256 from SHA256SUMS" is the security story we already document; OS gates are convenience, not load-bearing security on top of that.

## Re-evaluation triggers

Reopen this decision when ANY of the following hold:

1. v1.0 release-prep cycle starts (currently estimated as v0.95 + 1 minor).
2. User-reported install-friction issues exceed 5 distinct reports in a single release window.
3. A free/foundation-funded code-signing program (e.g., SignPath OSS, Apple's Notary for OSS) becomes available and offers low-ceremony enrollment for individual maintainers.

## Related

- [#1260](https://github.com/kwad77/pincher/issues/1260) Distribution polish umbrella (this is §2)
- `packaging/README.md` — install path documentation
- `.github/workflows/release.yml` — would gain signing steps if Option A were chosen
