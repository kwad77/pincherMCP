# Release artifact signing

Pincher release artifacts (`pincher-*.tar.gz`, `pincher-*.zip`, `SHA256SUMS`)
are signed with [cosign](https://docs.sigstore.dev/cosign/overview) when the
repository's `COSIGN_PRIVATE_KEY` secret is configured. Each artifact ships
with a `.sig` sidecar attached to the GitHub release.

Tracked by [#1524](https://github.com/kwad77/pincher/issues/1524) (FILE-E,
v1.0 supply-chain hardening). Until the secret is configured, releases ship
with checksums only and the release workflow logs a `::warning::` line.

## End-user verification

Download the artifact, its `.sig`, and the published public key:

```bash
VERSION=v0.88.0        # the release tag you downloaded
ARCH=linux-amd64       # or darwin-arm64, windows-amd64, etc.

# Fetch the public key (one-time).
curl -fsSLO https://raw.githubusercontent.com/kwad77/pincher/master/cosign.pub

# Verify the release tarball.
cosign verify-blob \
  --key cosign.pub \
  --signature pincher-${VERSION}-${ARCH}.tar.gz.sig \
  pincher-${VERSION}-${ARCH}.tar.gz
```

A successful verification prints `Verified OK`. A failure means either the
artifact, the signature, or the public key was tampered with — do not
install the binary in that case.

The same procedure verifies `SHA256SUMS`; doing both means a single trust
anchor (`cosign.pub`) covers every artifact in the release.

## Maintainer setup (one-time)

1. Generate a cosign keypair locally:

   ```bash
   cosign generate-key-pair
   ```

   This writes `cosign.key` (private) and `cosign.pub` (public) in the
   current directory and prompts for a passphrase.

2. Configure two repository secrets at
   https://github.com/kwad77/pincher/settings/secrets/actions:
   - `COSIGN_PRIVATE_KEY` — the full contents of `cosign.key`, including
     the `-----BEGIN ENCRYPTED COSIGN PRIVATE KEY-----` header and
     trailing newline.
   - `COSIGN_PASSWORD` — the passphrase you chose at keygen time.

3. Commit `cosign.pub` to the repository root and push to `master`:

   ```bash
   git add cosign.pub
   git commit -m "ops: publish cosign public key for release verification"
   git push origin master
   ```

4. Delete the local `cosign.key` after the secret is configured. The
   passphrase plus the secret value are the only copies that need to
   exist; treat them like any other long-lived production credential.

5. Cut a test release (or push a `vX.Y.Z` tag); confirm the release page
   now shows `*.sig` files alongside each artifact.

## Key rotation

If `cosign.key` is ever exposed (or simply on a periodic rotation
schedule), regenerate per the steps above and overwrite both secrets
plus `cosign.pub`. Old releases stay verifiable against the old key in
the git history (the `cosign.pub` at the commit that produced each
release is the one that signed it), so rotation does not invalidate
historical signatures.

## CI behaviour

The signing steps in `.github/workflows/release.yml` are guarded by
`if: env.COSIGN_PRIVATE_KEY != ''`. The workflow stays green on a fresh
fork or before the secret is configured — it just logs a warning and
ships unsigned artifacts plus checksums.
