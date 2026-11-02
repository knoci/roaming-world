$ErrorActionPreference = "Stop"

$targets = @(
  @{ Name = "user";       Url = "http://localhost:18000/healthz" },
  @{ Name = "scene";      Url = "http://localhost:18001/healthz" },
  @{ Name = "food";       Url = "http://localhost:18002/healthz" },
  @{ Name = "comment";    Url = "http://localhost:18003/healthz" },
  @{ Name = "article";    Url = "http://localhost:18004/healthz" },
  @{ Name = "audiobook";  Url = "http://localhost:18005/healthz" },
  @{ Name = "plan";       Url = "http://localhost:18006/healthz" },
  @{ Name = "ai";         Url = "http://localhost:18007/healthz" },
  @{ Name = "mcp";        Url = "http://localhost:18008/healthz" },
  @{ Name = "gateway";    Url = "http://localhost:18009/healthz" }
)

$failed = $false
foreach ($t in $targets) {
  try {
    $resp = Invoke-WebRequest -UseBasicParsing -Uri $t.Url -TimeoutSec 5
    if ($resp.StatusCode -eq 200) {
      Write-Host ("[OK] {0}" -f $t.Name)
    } else {
      Write-Host ("[FAIL] {0}: HTTP {1}" -f $t.Name, $resp.StatusCode)
      $failed = $true
    }
  } catch {
    Write-Host ("[FAIL] {0}: {1}" -f $t.Name, $_.Exception.Message)
    $failed = $true
  }
}

$routes = @(
  @{ Name = "scene list";    Url = "http://localhost:18001/v1/scene" },
  @{ Name = "food list";     Url = "http://localhost:18002/v1/food" },
  @{ Name = "article list";  Url = "http://localhost:18004/v1/article?way=time&page=1&limit=10" },
  @{ Name = "audiobook list";Url = "http://localhost:18005/v1/audiobook" },
  @{ Name = "comment list";  Url = "http://localhost:18003/v1/comment?aid=smoke-none" },
  @{ Name = "plan list";     Url = "http://localhost:18006/v1/plan?scope=public" },
  @{ Name = "article metrics"; Url = "http://localhost:18004/metrics" }
)

foreach ($r in $routes) {
  try {
    $resp = Invoke-WebRequest -UseBasicParsing -Uri $r.Url -TimeoutSec 5
    if ($resp.StatusCode -eq 200) {
      Write-Host ("[OK] {0}" -f $r.Name)
    } else {
      Write-Host ("[FAIL] {0}: HTTP {1}" -f $r.Name, $resp.StatusCode)
      $failed = $true
    }
  } catch {
    Write-Host ("[FAIL] {0}: {1}" -f $r.Name, $_.Exception.Message)
    $failed = $true
  }
}

if ($failed) { exit 1 }
Write-Host "Smoke test passed."
