param([switch]$Kubernetes)

$ErrorActionPreference = "Stop"
$names = @("AI_GATEWAY_TOKEN", "AI_INGEST_TOKEN", "MCP_TOKEN", "ARTICLE_USER_RPC_TOKEN", "COMMENT_USER_RPC_TOKEN")

function New-InternalToken {
  $bytes = New-Object byte[] 32
  $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
  try { $rng.GetBytes($bytes) }
  finally { $rng.Dispose() }
  return [Convert]::ToBase64String($bytes)
}

if ($Kubernetes) {
  $secretName = "roaming-world-internal-tokens"
  $namespace = "roaming-world"
  $existing = kubectl get secret $secretName -n $namespace --ignore-not-found -o json
  if ($LASTEXITCODE -ne 0) { throw "Unable to check Kubernetes internal tokens" }
  if ($existing) {
    $data = ($existing -join [Environment]::NewLine | ConvertFrom-Json).data
    foreach ($name in $names) {
      if (-not $data.$name) { throw "$secretName is missing $name" }
    }
    return
  }

  $tokens = @{}
  foreach ($name in $names) { $tokens[$name] = New-InternalToken }
  @{
    apiVersion = "v1"
    kind = "Secret"
    metadata = @{ name = $secretName; namespace = $namespace }
    stringData = $tokens
  } | ConvertTo-Json -Depth 4 | kubectl create -f -
  if ($LASTEXITCODE -ne 0) { throw "Unable to create Kubernetes internal tokens" }
  return
}

$project = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
Push-Location $project
try {
  $configured = docker compose config --environment
  if ($LASTEXITCODE -ne 0) { throw "Unable to read Compose environment" }
  $missing = @($names | Where-Object { -not ($configured -match "^$_=.+") })
  if ($missing.Count -gt 0) {
    $envPath = Join-Path $project ".env"
    $newline = [Environment]::NewLine
    $contents = if (Test-Path -LiteralPath $envPath) { [IO.File]::ReadAllText($envPath) } else { "" }
    foreach ($name in $missing) {
      $entry = "$name=$(New-InternalToken)"
      $existing = [regex]::Matches($contents, "(?m)^[ \t]*(?:export[ \t]+)?$name[ \t]*=[^\r\n]*") | Select-Object -Last 1
      if ($existing) {
        $contents = $contents.Remove($existing.Index, $existing.Length).Insert($existing.Index, $entry)
      } else {
        if ($contents -and -not $contents.EndsWith("`n")) { $contents += $newline }
        $contents += $entry + $newline
      }
    }
    [IO.File]::WriteAllText($envPath, $contents)
    Write-Host "Configured missing internal tokens in .env"
  }
} finally {
  Pop-Location
}
