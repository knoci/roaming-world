$ErrorActionPreference = "Stop"

& "$PSScriptRoot\preflight.ps1"
if ($LASTEXITCODE -ne 0) { exit 1 }

Write-Host "Pulling base images..."
foreach ($img in @("golang:1.24", "debian:bookworm-slim")) {
  $pulled = $false
  for ($i = 1; $i -le 5; $i++) {
    Write-Host ("Pulling {0} ({1}/5)..." -f $img, $i)
    docker pull $img
    if ($LASTEXITCODE -eq 0) { $pulled = $true; break }
    Start-Sleep -Seconds 5
  }
  if (-not $pulled) {
    Write-Host ("[FAIL] 无法拉取基础镜像 {0}，请检查 Docker Hub 网络后重试" -f $img)
    exit 1
  }
}

Write-Host "Starting Roaming World local stack..."
docker compose up -d --build
if ($LASTEXITCODE -ne 0) {
  Write-Host "[FAIL] docker compose up 失败，请查看上面的构建日志"
  docker compose ps
  exit 1
}

Write-Host "Waiting for services..."
$targets = @(
  @{ Name = "user";       Url = "http://localhost:18000/healthz" },
  @{ Name = "scene";      Url = "http://localhost:18001/healthz" },
  @{ Name = "food";       Url = "http://localhost:18002/healthz" },
  @{ Name = "comment";    Url = "http://localhost:18003/healthz" },
  @{ Name = "article";    Url = "http://localhost:18004/healthz" },
  @{ Name = "audiobook";  Url = "http://localhost:18005/healthz" },
  @{ Name = "plan";       Url = "http://localhost:18006/healthz" }
)

$deadline = (Get-Date).AddMinutes(3)
foreach ($t in $targets) {
  $ok = $false
  while ((Get-Date) -lt $deadline) {
    try {
      $resp = Invoke-WebRequest -UseBasicParsing -Uri $t.Url -TimeoutSec 3
      if ($resp.StatusCode -eq 200) {
        Write-Host ("[OK] {0} {1}" -f $t.Name, $t.Url)
        $ok = $true
        break
      }
    } catch {
      Start-Sleep -Seconds 3
    }
  }
  if (-not $ok) {
    Write-Host ("[FAIL] {0} {1}" -f $t.Name, $t.Url)
    docker compose ps
    docker compose logs --tail=200
    exit 1
  }
}

Write-Host "All health checks passed."
Write-Host "Nacos: http://localhost:8848/nacos"
Write-Host "Meilisearch: http://localhost:7700"
