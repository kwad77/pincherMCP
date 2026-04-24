#!/usr/bin/env sh
# Installer for the pincher binary that the Claude Code plugin wraps.
#
# Runs from the plugin's SessionStart hook on macOS and Linux. Idempotent —
# fast exit if the correct binary is already in place.
#
# Pulls the pincher release matching the plugin.json version, verifies
# its SHA256 against the release's SHA256SUMS file, and installs it at
# $CLAUDE_PLUGIN_ROOT/bin/pincher.
#
# Environment:
#   CLAUDE_PLUGIN_ROOT   required; provided by Claude Code at hook time
#   PINCHER_PLUGIN_DEBUG set to 1 to print verbose output
set -eu

log() {
  # One prefixed line per message — users usually see these in Claude Code's
  # session-start output, so keep them short and friendly.
  printf 'pincher-plugin: %s\n' "$*" >&2
}
debug() {
  [ "${PINCHER_PLUGIN_DEBUG:-0}" = "1" ] && log "$*" || true
}

root="${CLAUDE_PLUGIN_ROOT:-}"
if [ -z "$root" ]; then
  # Allow running standalone for testing by accepting the plugin root as $1.
  root="${1:-}"
  if [ -z "$root" ]; then
    log "CLAUDE_PLUGIN_ROOT is unset — pass the plugin dir as argv[1] when running standalone"
    exit 1
  fi
fi

plugin_json="$root/.claude-plugin/plugin.json"
bin_dir="$root/bin"
bin_path="$bin_dir/pincher"

if [ ! -f "$plugin_json" ]; then
  log "plugin.json not found at $plugin_json — aborting"
  exit 1
fi

# Parse version from plugin.json without needing jq. The field shape is
# always  "version": "X.Y.Z"  on one line after the build system writes it,
# so a simple grep + sed is reliable here.
version="$(grep -E '^[[:space:]]*"version"' "$plugin_json" | head -1 | sed -E 's/.*"version"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/')"
if [ -z "$version" ]; then
  log "could not parse version from $plugin_json"
  exit 1
fi

# Fast path: binary already present at the right version → exit.
if [ -x "$bin_path" ]; then
  current="$("$bin_path" --version 2>/dev/null | sed -E 's/^pincherMCP v//' || true)"
  if [ "$current" = "$version" ]; then
    debug "pincher v$version already installed at $bin_path"
    exit 0
  fi
  log "upgrading pincher $current → $version"
fi

# Also fast-path if pincher is already on PATH at the right version —
# saves bandwidth and avoids a second on-disk copy when users already
# installed pincher via Homebrew or a direct release download.
if command -v pincher >/dev/null 2>&1; then
  onpath="$(pincher --version 2>/dev/null | sed -E 's/^pincherMCP v//' || true)"
  if [ "$onpath" = "$version" ]; then
    mkdir -p "$bin_dir"
    # A symlink lets future version bumps re-evaluate what's on PATH.
    ln -sf "$(command -v pincher)" "$bin_path"
    debug "linked existing pincher v$version from $(command -v pincher)"
    exit 0
  fi
fi

# ── Platform detection ────────────────────────────────────────────────
os="$(uname -s)"
arch="$(uname -m)"
case "$os" in
  Darwin) goos="darwin" ;;
  Linux)  goos="linux" ;;
  *)      log "unsupported OS: $os"; exit 1 ;;
esac
case "$arch" in
  x86_64|amd64) goarch="amd64" ;;
  arm64|aarch64) goarch="arm64" ;;
  *) log "unsupported arch: $arch"; exit 1 ;;
esac

archive="pincher-v${version}-${goos}-${goarch}.tar.gz"
base_url="https://github.com/kwad77/pincherMCP/releases/download/v${version}"
archive_url="${base_url}/${archive}"
sums_url="${base_url}/SHA256SUMS"

log "downloading pincher v${version} for ${goos}/${goarch}"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# Fetch archive. curl is present on every mac and nearly every Linux; fall
# back to wget if that fails.
fetch() {
  if command -v curl >/dev/null 2>&1; then
    curl --fail --location --silent --show-error --output "$2" "$1"
  elif command -v wget >/dev/null 2>&1; then
    wget --quiet --output-document="$2" "$1"
  else
    log "need curl or wget to download the pincher binary"
    return 1
  fi
}

fetch "$archive_url" "$tmp/$archive"
fetch "$sums_url"    "$tmp/SHA256SUMS"

# Verify SHA256 against the published SHA256SUMS file. shasum on macOS,
# sha256sum on Linux — same output format.
expected="$(grep "  $archive\$" "$tmp/SHA256SUMS" | awk '{print $1}')"
if [ -z "$expected" ]; then
  log "no SHA256 line for $archive in SHA256SUMS — refusing to install"
  exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$tmp/$archive" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "$tmp/$archive" | awk '{print $1}')"
else
  log "need sha256sum or shasum to verify the download"
  exit 1
fi
if [ "$expected" != "$actual" ]; then
  log "SHA256 mismatch: expected $expected, got $actual — refusing to install"
  exit 1
fi

# Extract. The tarball contains a single file named after the archive
# minus the .tar.gz — e.g. pincher-v0.2.1-darwin-arm64.
tar -xzf "$tmp/$archive" -C "$tmp"
extracted="$tmp/pincher-v${version}-${goos}-${goarch}"
if [ ! -f "$extracted" ]; then
  log "expected binary not found in archive: $extracted"
  exit 1
fi

mkdir -p "$bin_dir"
# rm first so we're not overwriting a running copy on subsequent upgrades.
rm -f "$bin_path"
mv "$extracted" "$bin_path"
chmod +x "$bin_path"

log "installed pincher v${version} at $bin_path"
