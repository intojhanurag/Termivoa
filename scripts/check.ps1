$ErrorActionPreference = 'Stop'
$RepoDir = Resolve-Path (Join-Path $PSScriptRoot '..')
Set-Location $RepoDir

# $ErrorActionPreference does not stop on a native command that exits non-zero
# (Windows PowerShell 5.1 never does), so check $LASTEXITCODE after each one.
function Invoke-Checked([scriptblock]$Command) {
    & $Command
    if ($LASTEXITCODE -ne 0) { throw "$($Command.ToString().Trim()) failed with exit code $LASTEXITCODE in $((Get-Location).Path)." }
}

$NodeMajor = node -p "process.versions.node.split('.')[0]"
if ($NodeMajor -ne '24') {
    throw "Termivoa checks require Node 24.11.0."
}

Invoke-Checked { pnpm protocol:check }

$Unformatted = gofmt -l cmd internal tests third_party/ansi third_party/vt third_party/xpty third_party/ultraviolet
if ($LASTEXITCODE -ne 0) { throw 'gofmt failed.' }
if ($Unformatted) { throw "Go files require formatting: $($Unformatted -join ', ')" }

$env:GOTOOLCHAIN = 'go1.27.1'
Invoke-Checked { go vet ./... }
Invoke-Checked { go test ./... }
Push-Location third_party/ansi
try { Invoke-Checked { go test ./... } } finally { Pop-Location }
Push-Location third_party/vt
try { Invoke-Checked { go test ./... } } finally { Pop-Location }
Push-Location third_party/xpty
try { Invoke-Checked { go test ./... } } finally { Pop-Location }
Invoke-Checked { go test github.com/charmbracelet/ultraviolet/... }
Invoke-Checked { pnpm typecheck }
Invoke-Checked { pnpm test:web }
Invoke-Checked { pnpm build:web }
