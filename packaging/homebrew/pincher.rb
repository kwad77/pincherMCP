# Homebrew formula for pincherMCP.
#
# Pinned to v0.22.0. The SHA256 values below are from the authoritative
# SHA256SUMS file published with that release:
# https://github.com/kwad77/pincher/releases/download/v0.22.0/SHA256SUMS
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
  version "0.22.0"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/kwad77/pincher/releases/download/v#{version}/pincher-v#{version}-darwin-arm64.tar.gz"
      sha256 "ef3ced0fe83d5e4d932148f8ce5d186e0cb805e073d3aadcca28dd0b9a3c9299"
    end
    on_intel do
      url "https://github.com/kwad77/pincher/releases/download/v#{version}/pincher-v#{version}-darwin-amd64.tar.gz"
      sha256 "48c671204304e2f1005707d333e9a41a9d438da9cafb64ac8e18b788bcd9ac14"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/kwad77/pincher/releases/download/v#{version}/pincher-v#{version}-linux-arm64.tar.gz"
      sha256 "4b833abe2e0f6f550b7253a2bf52f6030aabb44318280e7d5bc5e854419c95f4"
    end
    on_intel do
      url "https://github.com/kwad77/pincher/releases/download/v#{version}/pincher-v#{version}-linux-amd64.tar.gz"
      sha256 "0324aecba3e0eb933c3802fa99063777aef9db88a40c2a49d99e960b85627dbd"
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
