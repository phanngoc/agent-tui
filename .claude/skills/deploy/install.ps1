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

# 2b. The desktop window, beside the core it runs: it looks for agent-tui.exe
#     in its own folder first. A Start menu entry makes it an app you open
#     rather than a file you find. It is a module of its own (desktop/), so the
#     core's binary carries none of its dependencies.
$desk = Join-Path $gobin 'agent-tui-desktop.exe'
if (Test-Path (Join-Path $Repo 'desktop\go.mod')) {
    & $go build -C (Join-Path $Repo 'desktop') -trimpath -ldflags="-s -w -H windowsgui" -o $desk .
    if ($LASTEXITCODE -ne 0) { throw "desktop build failed" }
    $lnk = Join-Path ([Environment]::GetFolderPath('Programs')) 'agent-tui.lnk'
    $ws = New-Object -ComObject WScript.Shell
    $sc = $ws.CreateShortcut($lnk)
    $sc.TargetPath = $desk
    $sc.WorkingDirectory = $env:USERPROFILE
    $sc.IconLocation = "$desk,0"
    $sc.Description = 'agent-tui'
    $sc.Save()
    # The web admin on its own: starts the gateway if it is not running and
    # opens the browser, with no terminal and no desktop window.
    $web = $ws.CreateShortcut((Join-Path ([Environment]::GetFolderPath('Programs')) 'agent-tui web.lnk'))
    $web.TargetPath = $desk
    $web.Arguments = '--web'
    $web.WorkingDirectory = $env:USERPROFILE
    $web.IconLocation = "$desk,0"
    $web.Description = 'agent-tui web admin (starts the gateway if needed)'
    $web.Save()
    Write-Host "desktop: $desk  (Start menu: agent-tui, agent-tui web)"
}

# 2c. The web admin, as a static build in an `admin` folder beside the binary:
#     `agent-tui serve` (which `tui` starts on its own) serves it from there.
#     Skipped without npm; the gateway then serves a page saying how to run it.
#     A gateway already running keeps the old binary and pages until it is
#     stopped, so it is stopped here and, if it was running, started again
#     from the new binary: the web stays up without waiting for a terminal.
$web = Join-Path $Repo 'web\admin'
$npm = Get-Command npm -ErrorAction SilentlyContinue
if ((Test-Path (Join-Path $web 'package.json')) -and $npm) {
    Push-Location $web
    try {
        if (-not (Test-Path 'node_modules')) { & npm install --no-audit --no-fund | Out-Null }
        & npm run build | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "admin build failed (cd web/admin; npm run build)" }
    } finally { Pop-Location }
    $dest = Join-Path $gobin 'admin'
    if (Test-Path $dest) { Remove-Item -Recurse -Force $dest }
    Copy-Item -Recurse (Join-Path $web 'out') $dest
    Write-Host "admin  : $dest"
} else {
    Write-Host "admin  : skipped (npm not found)"
}
$wasRunning = $false
Get-CimInstance Win32_Process -Filter "Name='agent-tui.exe'" |
    Where-Object { $_.CommandLine -match '\sserve(\s|$)' } |
    ForEach-Object { Stop-Process -Id $_.ProcessId -Force; $script:wasRunning = $true; Write-Host "gateway: stopped old pid $($_.ProcessId)" }
if ($wasRunning) {
    $ErrorActionPreference = 'Continue'
    $out = & $exe gateway start 2>&1
    $ErrorActionPreference = 'Stop'
    Write-Host "gateway: $out"
}

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
