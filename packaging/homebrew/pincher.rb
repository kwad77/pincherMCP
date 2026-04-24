# Homebrew formula for pincherMCP.
#
# Pinned to v0.2.1. The SHA256 values below are from the authoritative
# SHA256SUMS file published with that release:
# https://github.com/kwad77/pincherMCP/releases/download/v0.2.1/SHA256SUMS
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
  homepage "https://github.com/kwad77/pincherMCP"
  version "0.2.1"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/kwad77/pincherMCP/releases/download/v#{version}/pincher-v#{version}-darwin-arm64.tar.gz"
      sha256 "79aba402c4be0fd2c18a36d5e1b5f729d45a5ba4e0f3e0516ff8b85dfaf255a6"
    end
    on_intel do
      url "https://github.com/kwad77/pincherMCP/releases/download/v#{version}/pincher-v#{version}-darwin-amd64.tar.gz"
      sha256 "306fe940e0d654bc674f86602f5bf2f2f140ab21aaf9d04d12deb221caed0c92"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/kwad77/pincherMCP/releases/download/v#{version}/pincher-v#{version}-linux-arm64.tar.gz"
      sha256 "9c80ba3ee484f9fed4de787c437011cea979b4a013e4d7dde03bf07315a38140"
    end
    on_intel do
      url "https://github.com/kwad77/pincherMCP/releases/download/v#{version}/pincher-v#{version}-linux-amd64.tar.gz"
      sha256 "832257805630b2dc57a0b9d54305f85994f899e2d3468bddbbcfa2f83fd566f4"
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
