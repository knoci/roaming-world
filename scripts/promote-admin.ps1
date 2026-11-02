param(
  [Parameter(Mandatory = $true)]
  [string]$Email,
  [switch]$Kubernetes
)

$ErrorActionPreference = "Continue"

if ($Email -notmatch '^[^'']+@[^'']+$') {
  Write-Host "Invalid email: $Email"
  exit 1
}

$sql = "UPDATE users SET role='admin', status='active' WHERE email='$Email';"
if ($Kubernetes) {
  $output = kubectl exec -n roaming-world deployment/postgres -- psql -U roaming -d roaming_world -c $sql 2>&1
} else {
  $output = docker compose exec -T postgres psql -U roaming -d roaming_world -c $sql 2>&1
}
$code = $LASTEXITCODE
$output | ForEach-Object { Write-Host $_ }

if ($code -ne 0) {
  Write-Host "Promote admin failed. Is the database running?"
  exit 1
}
if ($output -match 'UPDATE 0') {
  Write-Host "User not found: $Email"
  exit 1
}

Write-Host "Promoted $Email to admin. Log out and log in again to get a new JWT."
