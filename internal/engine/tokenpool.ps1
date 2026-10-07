$ErrorActionPreference = 'Stop'
try {
    $root = $env:AGENT_TUI_TOKEN_POOL
    $hash = [System.Security.Cryptography.SHA256]::Create()
    function Fingerprint([string]$value) {
        return [BitConverter]::ToString($hash.ComputeHash([Text.Encoding]::UTF8.GetBytes($value))).Replace('-', '').ToLowerInvariant()
    }
    $key = Fingerprint $env:AGENT_TUI_CONVERSATION
    # Global makes terminal and gateway processes in different Windows sessions
    # share the cursor. The path scopes independent pools independently.
    $mutex = New-Object Threading.Mutex($false, ('Global\AgentTuiClaudePool-' + (Fingerprint ([IO.Path]::GetFullPath($root).ToLowerInvariant()))))
    $locked = $false
    try {
        try { $locked = $mutex.WaitOne(15000) } catch [Threading.AbandonedMutexException] { $locked = $true }
        if (-not $locked) { throw 'busy' }
        $pool = @(Import-Clixml -LiteralPath (Join-Path $root 'pool.xml'))
        if ($pool.Count -eq 0) { throw 'empty pool' }
        $tokens = @(); $ids = @()
        foreach ($entry in $pool) {
            $token = $entry.GetNetworkCredential().Password
            if (-not $token.StartsWith('sk-ant-oat') -or $token.Contains("`n") -or $token.Contains("`r")) { throw 'invalid token' }
            $id = Fingerprint $token
            if ($ids -notcontains $id) { $ids += $id; $tokens += $token }
        }
        $path = Join-Path $root 'agent-tui-state.json'
        $state = @{version=1; next=0; assignments=@{}}
        if (Test-Path -LiteralPath $path) {
            $saved = Get-Content -Raw -LiteralPath $path | ConvertFrom-Json
            if ($saved.version -ne 1 -or $null -eq $saved.next -or $saved.next -lt 0 -or $null -eq $saved.assignments) { throw 'invalid state' }
            $state.next = [long]$saved.next
            foreach ($p in $saved.assignments.PSObject.Properties) { $state.assignments[$p.Name] = [string]$p.Value }
        }
        $selected = -1
        if ($state.assignments.ContainsKey($key)) {
            $selected = [Array]::IndexOf($ids, $state.assignments[$key])
            if ($selected -lt 0) { throw 'assigned token removed' }
        } else {
            $selected = [int]($state.next % $tokens.Count)
            $state.next = ($selected + 1) % $tokens.Count
            $state.assignments[$key] = $ids[$selected]
            $tmp = $path + '.' + [Guid]::NewGuid().ToString('N') + '.tmp'
            try {
                $state | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath $tmp -Encoding UTF8
                if (Test-Path -LiteralPath $path) { [IO.File]::Replace($tmp, $path, [NullString]::Value) }
                else { [IO.File]::Move($tmp, $path) }
            } finally { if ([IO.File]::Exists($tmp)) { [IO.File]::Delete($tmp) } }
        }
        [Console]::Out.Write($tokens[$selected])
    } finally {
        if ($locked) { $mutex.ReleaseMutex() }
        $mutex.Dispose()
        $hash.Dispose()
    }
} catch { exit 1 }
