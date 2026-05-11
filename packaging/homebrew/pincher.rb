# Homebrew formula for pincherMCP.
#
# Pinned to v0.18.0. The SHA256 values below are from the authoritative
# SHA256SUMS file published with that release:
# https://github.com/kwad77/pincher/releases/download/v0.18.0/SHA256SUMS
#
# Usage:
#   brew tap kwad77/pincher https://github.com/kwad77/homebrew-pincher
#   brew install pincher
#
# To host the tap yourself, create a repo named "homebrew-pincher" under
# your GitHub account and drop this file in at Formula/pincher.rb.
#
# On each new release: bump `version`, refetch the release's SHA256SUMS,
# and paste the four new Darwin/Linux (arm64/amd64) hashes into the
# sha256 lines below.
class Pincher < Formula
  desc "Codebase intelligence server for LLM agents (MCP stdio + HTTP REST)"
  homepage "https://github.com/kwad77/pincher"
  version "0.18.0"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/kwad77/pincher/releases/download/v#{version}/pincher-v#{version}-darwin-arm64.tar.gz"
      sha256 "dd48e48cf0fdf9c16b809d0e8b9d8c03efa4dd1b9381764f890cd2b7fcc6e36c"
    end
    on_intel do
      url "https://github.com/kwad77/pincher/releases/download/v#{version}/pincher-v#{version}-darwin-amd64.tar.gz"
      sha256 "d12a4065d4f1aa141a6a9d5de2c2bb7e20d40903a5a6717a37414891ff92ad75"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/kwad77/pincher/releases/download/v#{version}/pincher-v#{version}-linux-arm64.tar.gz"
      sha256 "c4797e0ff137ad3cc7d8097c3f07675873c4fe558dda9003a56d4959ad2a695e"
    end
    on_intel do
      url "https://github.com/kwad77/pincher/releases/download/v#{version}/pincher-v#{version}-linux-amd64.tar.gz"
      sha256 "7bde73c9ee39ec16588f74f31f36147f096d65c48aef581dd6157d9efe94d2b5"
    end
  end

  def install
    # Archives contain one file: pincher-v<version>-<os>-<arch>[.exe]
    bin_src = Dir["pincher-*"].first
    bin.install bin_src => "pincher"
  end

  test do
    assert_match "pincherMCP", shell_output("#{bin}/pincher --version")
  end

  service do
    run [opt_bin/"pincher", "--http", ":8080"]
    keep_alive true
    log_path var/"log/pincher.log"
    error_log_path var/"log/pincher.err.log"
    environment_variables PINCHER_HTTP_ADDR: ":8080"
  end
end
