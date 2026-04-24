# Installer for the pincher binary that the Claude Code plugin wraps.
#
# Runs from the plugin's SessionStart hook on Windows. Idempotent - fast
# exit if the correct binary is already in place.
#
# Pulls the pincher release matching plugin.json's version, verifies
# its SHA256 against the release's SHA256SUMS file, and installs it at
# $env:CLAUDE_PLUGIN_ROOT\bin\pincher.exe.
#
# The .exe is also copied (not linked - symlinks need admin on Windows)
# to bin\pincher without extension so the same .mcp.json path works
# on every OS. Both files share a PE header; either invocation path
# will run.

param(
    [string]$PluginRoot = $env:CLAUDE_PLUGIN_ROOT
)

$ErrorActionPreference = 'Stop'

function Log($msg) { Write-Host "pincher-plugin: $msg" -ForegroundColor DarkGray }
function Debug($msg) { if ($env:PINCHER_PLUGIN_DEBUG -eq '1') { Log $msg } }

if (-not $PluginRoot) {
    Log 'CLAUDE_PLUGIN_ROOT is unset - pass -PluginRoot when running standalone'
    exit 1
}

$pluginJson = Join-Path $PluginRoot '.claude-plugin/plugin.json'
$binDir     = Join-Path $PluginRoot 'bin'
$binExe     = Join-Path $binDir 'pincher.exe'
$binBare    = Join-Path $binDir 'pincher'

if (-not (Test-Path $pluginJson)) {
    Log "plugin.json not found at $pluginJson - aborting"
    exit 1
}

# Parse version without pulling in a JSON library - regex is sufficient
# for the build-system-generated file, which always puts version on one
# line. ConvertFrom-Json would also work but adds a dep on the full
# parser just to read one string.
$versionMatch = Select-String -Path $pluginJson -Pattern '^\s*"version"\s*:\s*"([^"]+)"' | Select-Object -First 1
if (-not $versionMatch) {
    Log "could not parse version from $pluginJson"
    exit 1
}
$version = $versionMatch.Matches[0].Groups[1].Value

# Fast path: binary already present at the right version -> exit.
if (Test-Path $binExe) {
    try {
        $current = (& $binExe --version 2>$null) -replace '^pincherMCP v',''
        if ($current -eq $version) {
            Debug "pincher v$version already installed at $binExe"
            exit 0
        }
        Log "upgrading pincher $current -> $version"
    } catch {
        Debug 'existing bin/pincher.exe failed --version; will reinstall'
    }
}

# Also fast-path if pincher is already on PATH at the right version.
$onPath = Get-Command pincher -ErrorAction SilentlyContinue
if ($onPath) {
    try {
        $onPathVer = (& $onPath.Source --version 2>$null) -replace '^pincherMCP v',''
        if ($onPathVer -eq $version) {
            New-Item -ItemType Directory -Force -Path $binDir | Out-Null
            Copy-Item -Force -Path $onPath.Source -Destination $binExe
            Copy-Item -Force -Path $onPath.Source -Destination $binBare
            Debug "copied existing pincher v$version from $($onPath.Source)"
            exit 0
        }
    } catch { }
}

# - Platform detection -
# Windows plugin supports amd64 and arm64. Runtime architecture check
# avoids pulling an x64 binary onto a native arm64 host.
$arch = if ([Environment]::Is64BitOperatingSystem) {
    if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64' -or $env:PROCESSOR_ARCHITEW6432 -eq 'ARM64') { 'arm64' } else { 'amd64' }
} else {
    Log 'pincher requires a 64-bit OS'
    exit 1
}

$archive   = "pincher-v$version-windows-$arch.zip"
$binary    = "pincher-v$version-windows-$arch.exe"
$baseUrl   = "https://github.com/kwad77/pincherMCP/releases/download/v$version"
$archiveUrl = "$baseUrl/$archive"
$sumsUrl   = "$baseUrl/SHA256SUMS"

Log "downloading pincher v$version for windows/$arch"

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("pincher-plugin-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path $tmp | Out-Null
try {
    $archivePath = Join-Path $tmp $archive
    $sumsPath    = Join-Path $tmp 'SHA256SUMS'
    Invoke-WebRequest -UseBasicParsing -Uri $archiveUrl -OutFile $archivePath
    Invoke-WebRequest -UseBasicParsing -Uri $sumsUrl    -OutFile $sumsPath

    # Verify SHA256 against the release manifest.
    $expectedLine = (Get-Content $sumsPath | Where-Object { $_ -match "  $([regex]::Escape($archive))$" }) | Select-Object -First 1
    if (-not $expectedLine) {
        Log "no SHA256 line for $archive in SHA256SUMS - refusing to install"
        exit 1
    }
    $expected = ($expectedLine -split '\s+')[0]
    $actual = (Get-FileHash -Algorithm SHA256 -Path $archivePath).Hash.ToLower()
    if ($expected.ToLower() -ne $actual) {
        Log "SHA256 mismatch: expected $expected, got $actual - refusing to install"
        exit 1
    }

    # Extract the zip. The single file inside is pincher-v<version>-windows-<arch>.exe.
    Expand-Archive -Force -Path $archivePath -DestinationPath $tmp
    $extracted = Join-Path $tmp $binary
    if (-not (Test-Path $extracted)) {
        Log "expected binary not found in archive: $extracted"
        exit 1
    }

    New-Item -ItemType Directory -Force -Path $binDir | Out-Null
    # Remove any running copy first - Windows won't overwrite a running .exe.
    if (Test-Path $binExe)  { Remove-Item -Force $binExe }
    if (Test-Path $binBare) { Remove-Item -Force $binBare }
    Move-Item -Force -Path $extracted -Destination $binExe
    # Second copy without extension so a single .mcp.json command path
    # works on macOS, Linux, and Windows identically. Both share a PE
    # header; CreateProcess on Windows executes either via a full path.
    Copy-Item -Force -Path $binExe -Destination $binBare

    Log "installed pincher v$version at $binExe"
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
