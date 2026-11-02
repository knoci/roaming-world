$ErrorActionPreference = "Continue"
$failed = $false
$dockerOK = $false
$stackRunning = $false

function Fail($msg) {
  Write-Host "[FAIL] $msg"
  $script:failed = $true
}
function Ok($msg) {
  Write-Host "[OK] $msg"
}

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
  Fail "docker 不在 PATH"
} else {
  Ok "docker 已安装"
  docker info --format "{{.ServerVersion}}" 2>$null | Out-Null
  if ($LASTEXITCODE -ne 0) {
    Fail "Docker Engine 不可用；请先启动 Docker Desktop，并确认当前用户在 docker-users 组"
  } else {
    Ok "Docker Engine 可用"
    $dockerOK = $true
  }
}

docker compose version 2>$null | Out-Null
if ($LASTEXITCODE -ne 0) {
  Fail "docker compose 不可用"
} else {
  Ok "docker compose 可用"
}

if ($dockerOK) {
  $running = docker compose ps --services --status running 2>$null
  if ($LASTEXITCODE -eq 0 -and $running) {
    $stackRunning = $true
    Ok "已有 compose 服务在运行，跳过端口占用检查"
  }
}

if (-not $stackRunning) {
foreach ($p in @(5432, 6379, 8848, 9092, 7700, 2379)) {
  $conn = Get-NetTCPConnection -LocalPort $p -State Listen -ErrorAction SilentlyContinue
  if ($conn) {
    Fail "端口 $p 已被占用，先停掉本机同名服务或改 compose 端口"
  } else {
    Ok "端口 $p 空闲"
  }
}
}

foreach ($f in @(
  "docker-compose.yml",
  "deploy/nacos/roaming-world.yaml",
  "deploy/postgres/migrations/001_init.sql",
  "k8s/postgres-migrations/001_init.sql"
)) {
  if (Test-Path $f) { Ok "文件存在：$f" } else { Fail "文件缺失：$f" }
}

if ($failed) {
  Write-Host "Preflight failed."
  exit 1
}
Write-Host "Preflight passed."
