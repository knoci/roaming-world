param([switch]$Kubernetes = $true, [switch]$Multi)

$ErrorActionPreference = "Continue"
$forwards = @()
$failed = $false

if ($Kubernetes) {
  $forwards += Start-Process kubectl -ArgumentList @("port-forward","-n","roaming-world","svc/user-service","18000:8000","--address","127.0.0.1") -PassThru -WindowStyle Hidden
  $forwards += Start-Process kubectl -ArgumentList @("port-forward","-n","roaming-world","svc/gateway-service","18009:8000","--address","127.0.0.1") -PassThru -WindowStyle Hidden
  Start-Sleep -Seconds 3
}

try {
  $stage = "login"
  $loginJson = '{"email":"ai-demo@example.com","password":"demo123456"}'
  $loginBytes = [Text.Encoding]::UTF8.GetBytes($loginJson)
  $login = Invoke-RestMethod -Method Post -Uri "http://127.0.0.1:18000/v1/user/login" -ContentType "application/json" -UserAgent "ai-demo-agent" -Body $loginBytes -TimeoutSec 10
  if (-not $login.token) { throw "login returned no token" }
  Write-Host "[OK] login"

  $stage = "ingest"
  $ingestScript = @'
import os
import urllib.request

req = urllib.request.Request(
    "http://127.0.0.1:8000/v1/ai/ingest", method="POST",
    headers={"X-Ingest-Token": os.environ["AI_INGEST_TOKEN"]},
)
with urllib.request.urlopen(req, timeout=120) as response:
    print(response.read().decode("utf-8"))
'@
  $ingest = if ($Kubernetes) {
    $ingestScript | kubectl exec -i -n roaming-world deployment/ai-service -- python -
  } else {
    $ingestScript | docker compose --project-directory (Join-Path $PSScriptRoot "..") exec -T ai python -
  }
  if ($LASTEXITCODE -ne 0) { throw "AI ingest failed" }
  Write-Host ("[OK] ingest: " + $ingest)

  $queryJson = '{"query":"成都3天美食行程"}'
  $queryBytes = [Text.Encoding]::UTF8.GetBytes($queryJson)
  $stage = "multi"
  $endpoint = if ($Multi) { "/v1/ai/multi" } else { "/v1/ai/graph" }
  $web = Invoke-WebRequest -UseBasicParsing -Method Post -Uri ("http://127.0.0.1:18009" + $endpoint) -ContentType "application/json" -UserAgent "ai-demo-agent" -Headers @{Authorization="Bearer $($login.token)"} -Body $queryBytes -TimeoutSec 300
  $outerText = [Text.Encoding]::UTF8.GetString($web.RawContentStream.ToArray())
  $outer = $outerText | ConvertFrom-Json
  $inner = $outer.rawJson | ConvertFrom-Json
  $inner | ConvertTo-Json -Depth 10 | Out-File -Encoding utf8 "ai-demo-result.json"
  Write-Host ("[OK] gateway " + $endpoint + " -> ai-demo-result.json")
} catch {
  Write-Host "[FAIL] $stage : $($_.Exception.Message)"
  if ($_.ErrorDetails.Message) { Write-Host ("BODY: " + $_.ErrorDetails.Message) }
  $failed = $true
} finally {
  foreach ($p in $forwards) { Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue }
}

if ($failed) { exit 1 }
Write-Host "AI demo passed."
