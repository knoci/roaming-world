$ErrorActionPreference = "Stop"

$srcDir = Join-Path $PSScriptRoot "..\deploy\postgres\migrations"
$dstDir = Join-Path $PSScriptRoot "..\k8s\postgres-migrations"
New-Item -ItemType Directory -Force -Path $dstDir | Out-Null

$sqlFiles = Get-ChildItem -Path $srcDir -Filter "*.sql" -File | Sort-Object Name
if ($sqlFiles.Count -eq 0) {
  throw "No migration SQL files found in $srcDir"
}

foreach ($file in $sqlFiles) {
  Copy-Item -Force $file.FullName (Join-Path $dstDir $file.Name)
  Write-Host ("Synced deploy/postgres/migrations/{0} -> k8s/postgres-migrations/{0}" -f $file.Name)
}

$reconcileSrc = Join-Path $PSScriptRoot "..\deploy\postgres\reconcile_counts.sql"
$reconcileDst = Join-Path $PSScriptRoot "..\k8s\reconcile_counts.sql"
Copy-Item -Force $reconcileSrc $reconcileDst
Write-Host "Synced deploy/postgres/reconcile_counts.sql -> k8s/reconcile_counts.sql"
