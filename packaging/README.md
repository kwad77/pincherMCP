# Packaging

Drop-in installers and service templates for running pincherMCP as a
managed process on each major platform. Everything here is optional — the
binary is a single static file and works with nothing but `pincher --http :8080`
at a shell.

## Layout

| Path | Target |
|---|---|
| `homebrew/pincher.rb` | Homebrew formula template. Drop into a `homebrew-pincher` tap repo at `Formula/pincher.rb` after a release. |
| `systemd/pincher.service` | Systemd user unit for Linux. Installs to `~/.config/systemd/user/`. |
| `launchd/com.pinchermcp.pincher.plist` | LaunchAgent for macOS. Installs to `~/Library/LaunchAgents/`. |
| `windows/install-service.ps1` | PowerShell installer that wraps the binary with `sc.exe` as a Windows service. |

## Per-platform quick start

**macOS (Homebrew tap, once a release is cut):**

```bash
brew tap kwad77/pincher https://github.com/kwad77/homebrew-pincher
brew install pincher
brew services start pincher
```

Without a tap (manual LaunchAgent install):

```bash
cp packaging/launchd/com.pinchermcp.pincher.plist ~/Library/LaunchAgents/
launchctl load ~/Library/LaunchAgents/com.pinchermcp.pincher.plist
```

**Linux (systemd user service):**

```bash
mkdir -p ~/.config/systemd/user
cp packaging/systemd/pincher.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now pincher
journalctl --user -u pincher -f      # tail logs
```

For a system-wide install, move the file to `/etc/systemd/system/` and drop
the `--user` flags.

**Windows (service via sc.exe):**

```powershell
# In an elevated PowerShell:
cd packaging\windows
.\install-service.ps1 -BinaryPath "C:\tools\pincher.exe"
Start-Service pincher
```

Alternative: Task Scheduler with a logon trigger — no admin required, but
the server only runs while you're signed in.

**Docker (unchanged from repo root):**

```bash
docker run -d --name pincher \
  -v pincher-data:/data \
  -p 8080:8080 \
  -e PINCHER_HTTP_ADDR=:8080 \
  -e PINCHER_HTTP_KEY=$(openssl rand -hex 16) \
  ghcr.io/kwad77/pinchermcp:latest
```

## Configuration knobs

All three native-service templates accept the same environment variables
so config stays consistent across platforms:

| Variable | Purpose |
|---|---|
| `PINCHER_HTTP_ADDR` | HTTP listen address (default `:8080`; use `:0` for OS-picked). |
| `PINCHER_HTTP_KEY` | Bearer token required on every HTTP request. Recommended for non-localhost. |
| `--data-dir` (flag) | Override the SQLite directory (default is platform-appropriate). |

## Adding a package manager

Other package-manager formulae are welcome — AUR for Arch, Scoop for
Windows, Nix overlays, etc. They all follow the same shape: download the
matching binary archive from the GitHub release, unpack, install to a
PATH location, register a service if the OS has one. Open a PR with the
new manifest and a note in this README.
