param(
  [int]$Port = 9245,
  [int]$TimeoutSeconds = 30
)

$ErrorActionPreference = 'Stop'

if ($env:WAILS_VITE_PORT) {
  $Port = [int]$env:WAILS_VITE_PORT
}

$deadline = (Get-Date).AddSeconds($TimeoutSeconds)
$urls = @("http://localhost:$Port/", "http://127.0.0.1:$Port/")

while ((Get-Date) -lt $deadline) {
  foreach ($url in $urls) {
    try {
      $response = Invoke-WebRequest -UseBasicParsing -Uri $url -TimeoutSec 2
      if ($response.StatusCode -ge 200 -and $response.StatusCode -lt 500) {
        exit 0
      }
    } catch {
    }
  }
  Start-Sleep -Milliseconds 200
}

throw "Frontend dev server did not become ready on port $Port within $TimeoutSeconds seconds."
