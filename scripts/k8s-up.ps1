param([switch]$NoCache)

$ErrorActionPreference = "Continue"
Set-Location (Join-Path $PSScriptRoot "..")

function Fail($msg) { Write-Host "[FAIL] $msg"; exit 1 }

$imageTag = Get-Date -Format "yyyyMMddHHmmss"

if (-not (Get-Command minikube -ErrorAction SilentlyContinue)) { Fail "minikube not found" }
if (-not (Get-Command kubectl -ErrorAction SilentlyContinue)) { Fail "kubectl not found" }

Write-Host "Checking Docker engine..."
docker version --format '{{.Server.Version}}' 2>&1 | Out-Null
if ($LASTEXITCODE -ne 0) { Fail "Docker Desktop engine is not running. Start Docker Desktop, wait for Engine running, then rerun." }

Write-Host "Stopping Docker Compose stack to free resources/ports..."
docker compose -f docker-compose.yml down --remove-orphans 2>&1 | Out-Null

Write-Host "Starting single-node minikube..."
minikube start --driver=docker --cpus=4 --memory=6144
if ($LASTEXITCODE -ne 0) { Fail "minikube start failed" }
kubectl config use-context minikube | Out-Null
if ($LASTEXITCODE -ne 0) { Fail "kubectl context minikube failed" }

$images = @(
  @{ Name = "roaming-world/user:latest";      Context = "."; Dockerfile = "user/Dockerfile" },
  @{ Name = "roaming-world/scene:latest";     Context = "."; Dockerfile = "scene/Dockerfile" },
  @{ Name = "roaming-world/food:latest";      Context = "."; Dockerfile = "food/Dockerfile" },
  @{ Name = "roaming-world/comment:latest";   Context = "."; Dockerfile = "comment/Dockerfile" },
  @{ Name = "roaming-world/article:latest";   Context = "."; Dockerfile = "article/Dockerfile" },
  @{ Name = "roaming-world/audiobook:latest"; Context = "."; Dockerfile = "audiobook/Dockerfile" },
  @{ Name = "roaming-world/plan:latest";      Context = "."; Dockerfile = "plan/Dockerfile" },
  @{ Name = "roaming-world/ai:latest";        Context = "ai" },
  @{ Name = "roaming-world/mcp:latest";       Context = "mcp" },
  @{ Name = "roaming-world/gateway:latest";   Context = "."; Dockerfile = "gateway/Dockerfile" }
)

foreach ($img in $images) {
  $buildName = $img.Name -replace ":latest$", (":" + $imageTag)
  Write-Host ("Building {0}..." -f $buildName)
  $buildArgs = @("build", "-t", $buildName)
  if ($NoCache) { $buildArgs += "--no-cache" }
  if ($img.Dockerfile) { $buildArgs += @("-f", (Join-Path $PSScriptRoot ("..\" + $img.Dockerfile))) }
  $buildArgs += (Join-Path $PSScriptRoot ("..\" + $img.Context))
  docker @buildArgs
  if ($LASTEXITCODE -ne 0) { Fail ("docker build failed: " + $img.Name) }
  Write-Host ("Loading {0} into minikube..." -f $buildName)
  minikube image load $buildName
  if ($LASTEXITCODE -ne 0) { Fail ("minikube image load failed: " + $buildName) }
}

Write-Host "Removing one-shot jobs so changed templates can be reapplied..."
kubectl delete job postgres-migrate nacos-config-init -n roaming-world --ignore-not-found=true 2>&1 | Out-Null

Write-Host "Applying single-node manifests..."
kubectl apply -f k8s/namespace.yaml
if ($LASTEXITCODE -ne 0) { Fail "namespace apply failed" }
try { & "$PSScriptRoot\configure-internal-tokens.ps1" -Kubernetes }
catch { Fail $_.Exception.Message }
kubectl delete service nacos-nodeport -n roaming-world --ignore-not-found
if ($LASTEXITCODE -ne 0) { Fail "unable to remove the legacy Nacos NodePort" }

$rendered = Join-Path $env:TEMP ("roaming-world-single-node-{0}.yaml" -f [guid]::NewGuid().ToString("N"))
try {
  $kustomizeOut = kubectl kustomize k8s-single-node --load-restrictor LoadRestrictionsNone
  if ($LASTEXITCODE -ne 0) { Fail "kustomize render failed" }
  $manifest = ($kustomizeOut -join "`n") -replace 'roaming-world/([a-z0-9-]+):latest', ('roaming-world/$1:' + $imageTag)
  [IO.File]::WriteAllText($rendered, $manifest, [Text.UTF8Encoding]::new($false))
  kubectl apply -f $rendered
  if ($LASTEXITCODE -ne 0) { Fail "kubectl apply failed" }
} finally {
  Remove-Item -LiteralPath $rendered -Force -ErrorAction SilentlyContinue
}

Write-Host "Waiting for infrastructure..."
kubectl rollout status deployment/postgres -n roaming-world --timeout=300s
if ($LASTEXITCODE -ne 0) { Fail "postgres rollout failed" }
kubectl rollout status deployment/nacos -n roaming-world --timeout=240s
if ($LASTEXITCODE -ne 0) { Fail "nacos rollout failed" }
kubectl rollout status deployment/kafka -n roaming-world --timeout=240s
if ($LASTEXITCODE -ne 0) { Fail "kafka rollout failed" }
kubectl wait --for=condition=complete job/postgres-migrate -n roaming-world --timeout=180s
if ($LASTEXITCODE -ne 0) { Fail "postgres migration job failed" }
kubectl wait --for=condition=complete job/nacos-config-init -n roaming-world --timeout=240s
if ($LASTEXITCODE -ne 0) { Fail "nacos config job failed" }

Write-Host "Restarting app services so they load the imported Nacos config..."
kubectl rollout restart deployment/user-service deployment/scene-service deployment/food-service deployment/comment-service deployment/article-service deployment/audiobook-service deployment/plan-service deployment/ai-service deployment/mcp-service deployment/gateway-service -n roaming-world
if ($LASTEXITCODE -ne 0) { Fail "rollout restart failed" }

foreach ($svc in @("user-service","scene-service","food-service","comment-service","article-service","audiobook-service","plan-service","ai-service","mcp-service","gateway-service")) {
  kubectl rollout status deployment/$svc -n roaming-world --timeout=180s
  if ($LASTEXITCODE -ne 0) { Fail ("rollout failed: " + $svc) }
}

Write-Host "Pods:"
kubectl get pods -n roaming-world

Write-Host "Starting port-forward for smoke test..."
$forwards = @()
$ports = @(
  @{ Svc = "user-service";      Local = 18000 },
  @{ Svc = "scene-service";     Local = 18001 },
  @{ Svc = "food-service";      Local = 18002 },
  @{ Svc = "comment-service";   Local = 18003 },
  @{ Svc = "article-service";   Local = 18004 },
  @{ Svc = "audiobook-service"; Local = 18005 },
  @{ Svc = "plan-service";      Local = 18006 },
  @{ Svc = "gateway-service";   Local = 18009 }
)
try {
  foreach ($p in $ports) {
    $args = @("port-forward", "-n", "roaming-world", ("svc/" + $p.Svc), ("{0}:8000" -f $p.Local), "--address", "127.0.0.1")
    $proc = Start-Process -FilePath "kubectl" -ArgumentList $args -PassThru -WindowStyle Hidden
    $forwards += $proc
  }
  Start-Sleep -Seconds 4
  & (Join-Path $PSScriptRoot "smoke.ps1") -Kubernetes
  $smokeExit = $LASTEXITCODE
} finally {
  foreach ($proc in $forwards) { Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue }
}

if ($smokeExit -ne 0) { Fail "smoke test failed" }
Write-Host "Single-node K8s is ready."
Write-Host "Nacos: kubectl port-forward -n roaming-world svc/nacos-service 8848:8848"
