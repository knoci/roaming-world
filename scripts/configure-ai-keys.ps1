$ErrorActionPreference = "Stop"

function Unwrap([Security.SecureString]$value) {
  $ptr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($value)
  try { [Runtime.InteropServices.Marshal]::PtrToStringBSTR($ptr) }
  finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($ptr) }
}

Write-Host "Input is hidden. Keys are written only to the local K8s Secret, not to this repo."
$qwen = Read-Host "QWEN_API_KEY" -AsSecureString
$deepseek = Read-Host "DEEPSEEK_API_KEY" -AsSecureString
$xiaomi = Read-Host "XIAOMI_API_KEY" -AsSecureString

$q = Unwrap $qwen
$d = Unwrap $deepseek
$x = Unwrap $xiaomi

kubectl create secret generic ai-provider-keys -n roaming-world `
  --from-literal=QWEN_API_KEY="$q" `
  --from-literal=DEEPSEEK_API_KEY="$d" `
  --from-literal=XIAOMI_API_KEY="$x" `
  --dry-run=client -o yaml | kubectl apply -f -

if ($LASTEXITCODE -ne 0) { Write-Host "Failed to write secret"; exit 1 }
Write-Host "Secret ai-provider-keys applied. Restart AI:"
Write-Host "kubectl rollout restart deployment/ai-service -n roaming-world"
