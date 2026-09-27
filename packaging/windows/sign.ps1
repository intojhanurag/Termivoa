# Owner-authorized signing with SignTool. The certificate stays in the
# current user's certificate store; this script only receives its name-free
# SHA-1 thumbprint and the owner's RFC 3161 timestamp URL:
#   PTERM_SIGN_CERT_THUMBPRINT  40 hex characters
#   PTERM_SIGN_TIMESTAMP_URL    e.g. the certificate vendor's timestamp service
param([Parameter(Mandatory = $true, ValueFromRemainingArguments = $true)][string[]]$Path)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$thumbprint = $env:PTERM_SIGN_CERT_THUMBPRINT
$timestamp = $env:PTERM_SIGN_TIMESTAMP_URL
if (-not $thumbprint -or $thumbprint -notmatch '^[0-9A-Fa-f]{40}$' -or -not $timestamp -or $timestamp -notmatch '^https?://') {
    [Console]::Error.WriteLine('Refusing to sign: set PTERM_SIGN_CERT_THUMBPRINT and PTERM_SIGN_TIMESTAMP_URL (owner authorization required).')
    exit 1
}
$signtool = if ($env:SIGNTOOL) { $env:SIGNTOOL } else { (Get-Command signtool.exe -ErrorAction Stop).Source }

foreach ($file in $Path) {
    & $signtool sign /sha1 $thumbprint /fd SHA256 /tr $timestamp /td SHA256 /d 'Termivoa' $file
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    & $signtool verify /pa $file
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
