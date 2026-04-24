# Homebrew formula for pincherMCP.
#
# Before the formula can land in a tap, cut a release (the GitHub Actions
# workflow at .github/workflows/release.yml builds tagged binaries) and
# replace the placeholder SHA256 values below with the real checksums
# published in dist/SHA256SUMS.
#
# Usage:
#   brew tap kwad77/pincher https://github.com/kwad77/homebrew-pincher
#   brew install pincher
#
# To host the tap yourself, create a repo named "homebrew-pincher" under
# your GitHub account and drop this file in at Formula/pincher.rb.
class Pincher < Formula
  desc "Codebase intelligence server for LLM agents (MCP stdio + HTTP REST)"
  homepage "https://github.com/kwad77/pincherMCP"
  version "0.1.0"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/kwad77/pincherMCP/releases/download/v#{version}/pincher-v#{version}-darwin-arm64.tar.gz"
      sha256 "REPLACE_WITH_SHA256_FROM_SHASUMS"
    end
    on_intel do
      url "https://github.com/kwad77/pincherMCP/releases/download/v#{version}/pincher-v#{version}-darwin-amd64.tar.gz"
      sha256 "REPLACE_WITH_SHA256_FROM_SHASUMS"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/kwad77/pincherMCP/releases/download/v#{version}/pincher-v#{version}-linux-arm64.tar.gz"
      sha256 "REPLACE_WITH_SHA256_FROM_SHASUMS"
    end
    on_intel do
      url "https://github.com/kwad77/pincherMCP/releases/download/v#{version}/pincher-v#{version}-linux-amd64.tar.gz"
      sha256 "REPLACE_WITH_SHA256_FROM_SHASUMS"
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
