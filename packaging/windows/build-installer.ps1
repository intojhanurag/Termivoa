# Build pterm.exe (windows/amd64, CGO_ENABLED=0) and the per-user Inno Setup
# installer into artifacts/. Unsigned unless -Sign is passed, which requires
# the owner's certificate thumbprint (see sign.ps1).
param(
    [string]$Version = $(if ($env:PTERM_VERSION) { $env:PTERM_VERSION } else { '0.0.0-dev' }),
    [switch]$Sign
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

if ($Version -notmatch '^(\d+\.\d+\.\d+)(-[0-9A-Za-z.-]+)?$') {
    throw 'Version must look like 1.2.3 or 1.2.3-rc.1'
}
$numeric = $Matches[1]
$repo = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
Set-Location $repo

$buildId = "$Version+$((git rev-parse --short=12 HEAD).Trim())"
git diff --quiet HEAD --
if ($LASTEXITCODE -ne 0) { $buildId = "$buildId.dirty" }

$dist = Join-Path $repo 'dist\windows'
$artifacts = Join-Path $repo 'artifacts'
New-Item -ItemType Directory -Force $dist, $artifacts | Out-Null
$exe = Join-Path $dist 'pterm.exe'

pnpm build:web
if ($LASTEXITCODE -ne 0) { throw 'web build failed' }
$embedded = Join-Path $repo 'internal\webassets\dist'
if (Test-Path $embedded) { Remove-Item -Recurse -Force $embedded }
Copy-Item -Recurse (Join-Path $repo 'web\dist') $embedded

$env:CGO_ENABLED = '0'; $env:GOOS = 'windows'; $env:GOARCH = 'amd64'
go build -trimpath -tags webdist -ldflags "-X main.buildID=$buildId" -o $exe ./cmd/pterm
if ($LASTEXITCODE -ne 0) { throw 'go build failed' }

$iscc = if ($env:ISCC) { $env:ISCC } else { Join-Path ${env:ProgramFiles(x86)} 'Inno Setup 6\ISCC.exe' }
if (-not (Test-Path $iscc)) { throw "Inno Setup compiler not found at $iscc (set ISCC)" }

$arguments = @("/DAppVersion=$Version", "/DNumericVersion=$numeric", "/DSourceExe=$exe", "/DOutputDir=$artifacts")
if ($Sign) {
    $signScript = Join-Path $PSScriptRoot 'sign.ps1'
    & $signScript $exe
    if ($LASTEXITCODE -ne 0) { throw 'signing pterm.exe failed' }
    # Inno replaces $q with a quote and $f with the quoted file to sign.
    $arguments += '/DSign', "/Spterm=powershell.exe -NoProfile -ExecutionPolicy Bypass -File `$q$signScript`$q `$f"
}
& $iscc @arguments (Join-Path $PSScriptRoot 'phone-terminal.iss')
if ($LASTEXITCODE -ne 0) { throw 'ISCC failed' }

Get-ChildItem $artifacts -Filter "Phone-Terminal-$Version-windows-x64*-setup.exe" | ForEach-Object {
    $hash = (Get-FileHash $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
    Set-Content -Path "$($_.FullName).sha256" -Value "$hash  $($_.Name)" -Encoding ascii
    Write-Host "Built $($_.FullName) ($buildId)"
}
