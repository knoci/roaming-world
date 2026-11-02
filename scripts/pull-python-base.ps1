$ErrorActionPreference = "Continue"

$mirrors = @(
  "docker.m.daocloud.io",
  "docker.1ms.run",
  "dockerproxy.net",
  "docker.1panel.live"
)

foreach ($m in $mirrors) {
  $image = "$m/library/python:3.12-slim"
  Write-Host "Trying $image ..."
  docker pull $image
  if ($LASTEXITCODE -eq 0) {
    docker tag $image python:3.12-slim
    if ($LASTEXITCODE -eq 0) {
      Write-Host "OK: tagged $image -> python:3.12-slim"
      exit 0
    }
  }
}

Write-Host "All mirrors failed. Configure Docker Desktop registry mirror or use a proxy."
exit 1
