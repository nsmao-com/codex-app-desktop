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
Get-NetTCPConnection -LocalPort $port -ErrorAction SilentlyContinue |
  Where-Object { $_.OwningProcess -gt 0 } |
  Select-Object -ExpandProperty OwningProcess -Unique |
  ForEach-Object {
    Write-Output ("freeing port {0} pid={1}" -f $port, $_)
    Stop-Process -Id $_ -Force -ErrorAction SilentlyContinue
  }
Start-Sleep -Seconds 1

$root = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $root
Write-Output ('starting wails3 dev in ' + $root)
& wails3 dev -config ./build/config.yml -port 9245
