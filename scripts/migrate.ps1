$ErrorActionPreference = "Stop"

Write-Host "Applying PostgreSQL migrations..."
docker compose exec -T postgres psql -U roaming -d roaming_world -f /migrations/001_init.sql
Write-Host "Migration applied."
