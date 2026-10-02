# Installs agent-tui globally on this Windows machine so that `tui` (or
# `agent-tui`) opens it in whatever directory the terminal is in.
# Safe to run again: every step checks before it changes anything.
param(
    [string]$Repo = (Resolve-Path "$PSScriptRoot\..\..\..").Path,
    [string]$Alias = 'tui'
)
$ErrorActionPreference = 'Stop'

# 1. Find go. The installer puts it under LocalAppData without always adding
#    it to PATH, so a missing `go` command does not mean Go is missing.
$go = (Get-Command go -ErrorAction SilentlyContinue).Source
if (-not $go) {
    $candidates = @(
        "$env:LOCALAPPDATA\Programs\Go\bin\go.exe",
        "C:\Program Files\Go\bin\go.exe",
        "C:\Go\bin\go.exe",
        "$env:USERPROFILE\scoop\shims\go.exe"
    )
    $go = $candidates | Where-Object { Test-Path $_ } | Select-Object -First 1
}
if (-not $go) { throw "Go not found. Install it: winget install GoLang.Go" }
$goDir = Split-Path $go
Write-Host "go     : $go ($(& $go version))"

# 2. Build and install. GOBIN wins if set; otherwise GOPATH\bin.
$gobin = (& $go env GOBIN).Trim()
if (-not $gobin) { $gobin = Join-Path (& $go env GOPATH).Trim() 'bin' }
Push-Location $Repo
try {
    $commit = (git rev-parse --short HEAD 2>$null)
    & $go install -trimpath -ldflags="-s -w" ./cmd/agent-tui
    if ($LASTEXITCODE -ne 0) { throw "go install failed" }
} finally { Pop-Location }
$exe = Join-Path $gobin 'agent-tui.exe'
Write-Host "binary : $exe (commit $commit)"

# 3. The short alias: a .cmd for PowerShell/cmd/Warp, an extensionless sh
#    script for Git Bash, which does not resolve .cmd on its own.
if ($Alias -and $Alias -ne 'agent-tui') {
    Set-Content -Path (Join-Path $gobin "$Alias.cmd") -Encoding ascii -Value '@"%~dp0agent-tui.exe" %*'
    $sh = "#!/bin/sh`nexec `"`$(dirname `"`$0`")/agent-tui.exe`" `"`$@`"`n"
    [IO.File]::WriteAllText((Join-Path $gobin $Alias), $sh)
    Write-Host "alias  : $Alias -> agent-tui.exe"
}

# 4. User PATH (not Machine: no admin needed, and nothing system-wide moves).
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$parts = @($userPath -split ';' | Where-Object { $_ })
$added = @()
foreach ($p in @($goDir, $gobin)) {
    if ($parts -notcontains $p -and $parts -notcontains "$p\") { $parts += $p; $added += $p }
}
if ($added) {
    [Environment]::SetEnvironmentVariable('Path', ($parts -join ';'), 'User')
    Write-Host "PATH   : added $($added -join ', ') (open a new terminal)"
} else {
    Write-Host "PATH   : already set"
}

# 5. Verify from a PATH built the way a new terminal would build it.
$env:Path = [Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' +
            [Environment]::GetEnvironmentVariable('Path', 'User')
$name = if ($Alias) { $Alias } else { 'agent-tui' }
$resolved = (Get-Command $name -ErrorAction SilentlyContinue).Source
if (-not $resolved) { throw "'$name' does not resolve on the new PATH" }
# -h prints usage to stderr, which PowerShell 5.1 under 'Stop' turns into a
# terminating error; judge it by the exit code instead.
$ErrorActionPreference = 'Continue'
$null = & $exe -h 2>&1
if ($LASTEXITCODE -ne 0) { throw "agent-tui.exe -h exited $LASTEXITCODE" }
Write-Host "verify : '$name' -> $resolved  OK"
