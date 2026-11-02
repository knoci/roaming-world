$ErrorActionPreference = "Stop"
$src = Join-Path $PSScriptRoot "..\deploy\postgres\migrations\001_init.sql"
$dstDir = Join-Path $PSScriptRoot "..\k8s\postgres-migrations"
New-Item -ItemType Directory -Force -Path $dstDir | Out-Null
Copy-Item -Force $src (Join-Path $dstDir "001_init.sql")
Copy-Item -Force (Join-Path $PSScriptRoot "..\deploy\postgres\reconcile_counts.sql") (Join-Path $PSScriptRoot "..\k8s\reconcile_counts.sql")
Write-Host "Synced deploy/postgres/migrations/001_init.sql -> k8s/postgres-migrations/001_init.sql"
