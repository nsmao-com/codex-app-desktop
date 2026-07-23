$ErrorActionPreference = 'Stop'
$port = 9245

function Get-PortPids([int]$localPort) {
  Get-NetTCPConnection -LocalPort $localPort -ErrorAction SilentlyContinue |
    Where-Object { $_.OwningProcess -gt 0 } |
    Select-Object -ExpandProperty OwningProcess -Unique
}

$pids = @(Get-PortPids $port)
if (-not $pids.Count) {
  Write-Output "port $port is free"
  exit 0
}

foreach ($procId in $pids) {
  $proc = Get-Process -Id $procId -ErrorAction SilentlyContinue
  $name = if ($proc) { $proc.ProcessName } else { 'unknown' }
  Write-Output ("killing pid={0} name={1}" -f $procId, $name)
  Stop-Process -Id $procId -Force -ErrorAction SilentlyContinue
}

# Child node/vite leftovers from wails frontend.
Get-Process -Name node,vite,esbuild -ErrorAction SilentlyContinue |
  Where-Object {
    try {
      $cmd = (Get-CimInstance Win32_Process -Filter ("ProcessId={0}" -f $_.Id)).CommandLine
      $cmd -and ($cmd -match 'NiceCodex|nice_codex_desktop|9245|5173')
    } catch { $false }
  } |
  ForEach-Object {
    Write-Output ("killing related pid={0} name={1}" -f $_.Id, $_.ProcessName)
    Stop-Process -Id $_.Id -Force -ErrorAction SilentlyContinue
  }

Start-Sleep -Seconds 2
$left = @(Get-PortPids $port)
if ($left.Count) {
  Write-Output ("port still busy pids=" + ($left -join ','))
  exit 1
}
Write-Output "port $port freed"
