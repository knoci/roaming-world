$ErrorActionPreference = "Stop"

Write-Host "Applying PostgreSQL migrations..."

$migrateScript = @'
set -eu
psql -U roaming -d roaming_world -v ON_ERROR_STOP=1 <<'SQL'
CREATE TABLE IF NOT EXISTS schema_migrations (
  version text PRIMARY KEY,
  applied_at timestamptz NOT NULL DEFAULT now()
);
SQL

for f in $(for p in /migrations/*.sql; do [ -f "$p" ] && printf '%s\n' "$p"; done | LC_ALL=C sort); do
  version=$(basename "$f")
  applied=$(printf '%s\n' "SELECT 1 FROM schema_migrations WHERE version = :'version';" | \
    psql -U roaming -d roaming_world -v ON_ERROR_STOP=1 -v version="$version" -tA)
  if [ "$applied" = "1" ]; then
    echo "Skipping already applied migration: $version"
    continue
  fi
  echo "Applying migration: $version"
  {
    printf '%s %s\n' '\i' "$f"
    printf '%s\n' "INSERT INTO schema_migrations (version) VALUES (:'version') ON CONFLICT (version) DO NOTHING;"
  } | psql -U roaming -d roaming_world -v ON_ERROR_STOP=1 -v version="$version"
  echo "Applied migration: $version"
done
'@ -replace "`r`n", "`n"

docker compose exec -T postgres sh -eu -c $migrateScript
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
Write-Host "Migrations applied."
