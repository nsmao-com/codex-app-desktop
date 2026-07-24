$ErrorActionPreference = 'Stop'

$names = @('NiceCodex', 'nice_codex_desktop', 'wails3')
$stopped = @()
foreach ($name in $names) {
  Get-Process -Name $name -ErrorAction SilentlyContinue | ForEach-Object {
    Stop-Process -Id $_.Id -Force -ErrorAction SilentlyContinue
    $stopped += ("{0}({1})" -f $_.ProcessName, $_.Id)
  }
}

# Also stop any go process hosting this app from the NiceCodex folder if present.
Get-CimInstance Win32_Process -ErrorAction SilentlyContinue |
  Where-Object {
    $_.Name -match '^(NiceCodex|wails3)\.exe$' -or
    ($_.CommandLine -and $_.CommandLine -match 'wails3\s+dev' -and $_.CommandLine -match 'nice_codex_desktop|NiceCodex')
  } |
  ForEach-Object {
    Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue
    $stopped += ("{0}({1})" -f $_.Name, $_.ProcessId)
  }

Start-Sleep -Seconds 1
Write-Output ('stopped: ' + ($(if ($stopped.Count) { $stopped -join ', ' } else { '(none)' })))

# Free wails3 control port if a stale process still holds it.
$port = 9245
try {
  $listenPids = @()
  foreach ($line in (netstat -ano | Select-String "LISTENING" | Select-String ":$port\s")) {
    if ($line.Line -match '\s+(\d+)\s*$') {
      $listenPids += [int]$Matches[1]
    }
  }
  foreach ($procId in ($listenPids | Select-Object -Unique)) {
    if ($procId -le 0) { continue }
    Write-Output ("freeing port {0} pid={1}" -f $port, $procId)
    Stop-Process -Id $procId -Force -ErrorAction SilentlyContinue
  }
} catch {
  Write-Output ("skip port free for {0}: {1}" -f $port, $_.Exception.Message)
}
Start-Sleep -Seconds 1

$root = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $root
Write-Output ('starting wails3 dev in ' + $root)
& wails3 dev -config ./build/config.yml -port 9245
