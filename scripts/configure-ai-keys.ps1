param([switch]$Kubernetes)

$ErrorActionPreference = "Stop"

$keys = @("QWEN_API_KEY", "DEEPSEEK_API_KEY", "XIAOMI_API_KEY")

function Unwrap([Security.SecureString]$value) {
  if (-not $value) { return "" }
  $ptr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($value)
  try { [Runtime.InteropServices.Marshal]::PtrToStringBSTR($ptr) }
  finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($ptr) }
}

if ($Kubernetes) {
  $namespace = "roaming-world"
  $secretName = "ai-provider-keys"

  $existingJson = kubectl get secret $secretName -n $namespace --ignore-not-found -o json
  if ($LASTEXITCODE -ne 0) { throw "Unable to read Kubernetes Secret $secretName" }

  $values = @{}
  $exists = $false
  if ($existingJson) {
    $exists = $true
    $parsed = ($existingJson -join [Environment]::NewLine) | ConvertFrom-Json
    if ($parsed.data) {
      foreach ($prop in $parsed.data.PSObject.Properties) {
        if ($prop.Value) {
          $values[$prop.Name] = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String([string]$prop.Value))
        } else {
          $values[$prop.Name] = ""
        }
      }
    }
  }

  Write-Host "Input is hidden. Keys are written only to the local K8s Secret, not to this repo."
  Write-Host "Press Enter on any key to keep its existing value."
  foreach ($key in $keys) {
    $hint = if ($values.ContainsKey($key) -and $values[$key]) { " [configured, Enter to keep]" } else { "" }
    $sec = Read-Host "$key$hint" -AsSecureString
    $val = Unwrap $sec
    if ($val) {
      $values[$key] = $val
    } elseif (-not $values.ContainsKey($key)) {
      $values[$key] = ""
    }
  }

  $payload = @{
    apiVersion = "v1"
    kind = "Secret"
    metadata = @{
      name = $secretName
      namespace = $namespace
    }
    type = "Opaque"
    stringData = $values
  } | ConvertTo-Json -Depth 4

  if ($exists) {
    $payload | kubectl replace -f -
  } else {
    $payload | kubectl create -f -
  }
  if ($LASTEXITCODE -ne 0) { Write-Host "Failed to write secret"; exit 1 }

  Write-Host "Secret $secretName updated. Restart AI:"
  Write-Host "kubectl rollout restart deployment/ai-service -n $namespace"
  return
}

$project = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$envPath = Join-Path $project ".env"
$newline = [Environment]::NewLine
$contents = if (Test-Path -LiteralPath $envPath) { [IO.File]::ReadAllText($envPath) } else { "" }

Write-Host "Input is hidden. Keys are written only to local .env (gitignored), not to this repo."
Write-Host "Press Enter on any key to keep its existing value."

$updated = 0
foreach ($key in $keys) {
  $match = [regex]::Matches($contents, "(?m)^[ \t]*(?:export[ \t]+)?$key[ \t]*=([^\r\n]*)") | Select-Object -Last 1
  $hasExisting = $match -and ($match.Groups[1].Value.Trim().Length -gt 0)
  $hint = if ($hasExisting) { " [configured, Enter to keep]" } else { "" }
  $sec = Read-Host "$key$hint" -AsSecureString
  $val = Unwrap $sec
  if (-not $val) { continue }

  $entry = "$key=$val"
  if ($match) {
    $contents = $contents.Remove($match.Index, $match.Length).Insert($match.Index, $entry)
  } else {
    if ($contents -and -not $contents.EndsWith("`n")) { $contents += $newline }
    $contents += $entry + $newline
  }
  $updated++
}

if ($updated -gt 0) {
  [IO.File]::WriteAllText($envPath, $contents)
  Write-Host "Updated $updated AI provider key(s) in .env. Restart AI:"
  Write-Host "docker compose up -d ai"
} else {
  Write-Host "No keys changed."
}
