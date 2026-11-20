$ErrorActionPreference = "Stop"

Write-Host "Applying PostgreSQL migrations..."
docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U roaming -d roaming_world -f /migrations/001_init.sql
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
Write-Host "Migration applied."
