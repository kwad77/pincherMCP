# Install pincherMCP as a Windows service that starts at boot.
#
# Run as Administrator:
#     Set-ExecutionPolicy -Scope Process Bypass
#     .\install-service.ps1 -BinaryPath "C:\tools\pincher.exe"
#
# Then manage it like any other Windows service:
#     Start-Service pincher
#     Stop-Service  pincher
#     Get-Service   pincher
#     Restart-Service pincher
#     # Remove entirely:
#     Stop-Service pincher ; sc.exe delete pincher
#
# For user-scope installs that run only while you're logged in, use Task
# Scheduler with a logon trigger instead of a Windows service — the
# trade-off is the process starts slightly later but doesn't need admin.
#
# Background: Go stdio processes don't implement the Windows Service Control
# Manager protocol natively. This script uses `sc.exe create` which wraps
# any binary; the HTTP server keeps the process alive. pincher's own MCP
# stdio loop will keep waiting on stdin (which gets a closed handle from
# the SCM) and eventually exit — but by then the HTTP goroutine is
# serving requests and SCM restarts the process on failure.

param(
    [Parameter(Mandatory = $true)]
    [string]$BinaryPath,

    [string]$ServiceName = "pincher",
    [string]$DisplayName = "pincherMCP codebase intelligence server",
    [string]$HttpAddr    = ":8080",
    [string]$HttpKey     = "",
    [string]$DataDir     = ""
)

if (-not (Test-Path $BinaryPath)) {
    Write-Error "Binary not found at $BinaryPath"
    exit 1
}

$BinaryPath = (Resolve-Path $BinaryPath).Path

# Build the arg string. Always pass --http so the service has something to
# serve; stdio MCP doesn't work under SCM anyway.
$binPath = '"' + $BinaryPath + '"' + ' --http ' + $HttpAddr
if ($DataDir) { $binPath += ' --data-dir "' + $DataDir + '"' }
if ($HttpKey) { $binPath += ' --http-key "' + $HttpKey + '"' }

# Create the service. start= auto = starts at boot. obj= LocalSystem by
# default; override to obj= "NT AUTHORITY\NetworkService" for least privilege.
& sc.exe create $ServiceName binPath= $binPath start= auto DisplayName= $DisplayName
if ($LASTEXITCODE -ne 0) {
    Write-Error "sc.exe create failed (exit $LASTEXITCODE)"
    exit $LASTEXITCODE
}

& sc.exe description $ServiceName "pincherMCP local codebase intelligence server. See https://github.com/kwad77/pincherMCP"
# Auto-restart on failure (3 retries 10s apart, then give up).
& sc.exe failure $ServiceName reset= 86400 actions= restart/10000/restart/10000/restart/10000

Write-Host ""
Write-Host "pincherMCP installed as service '$ServiceName'." -ForegroundColor Green
Write-Host "Start it with:  Start-Service $ServiceName"
Write-Host "Logs:           check Event Viewer → Windows Logs → Application, source '$ServiceName'"
