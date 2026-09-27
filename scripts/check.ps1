$ErrorActionPreference = 'Stop'
$RepoDir = Resolve-Path (Join-Path $PSScriptRoot '..')
Set-Location $RepoDir

$NodeMajor = node -p "process.versions.node.split('.')[0]"
if ($NodeMajor -ne '24') {
    throw "Phone Terminal checks require Node 24.11.0."
}

pnpm protocol:generate
git diff --exit-code -- internal/protocol/generated.go web/src/generated/protocol.ts
if ($LASTEXITCODE -ne 0) { throw 'Generated protocol files drifted.' }

$RootPlan = (Get-FileHash plan.md -Algorithm SHA256).Hash
$DocsPlan = (Get-FileHash docs/implementation-plan.md -Algorithm SHA256).Hash
if ($RootPlan -ne $DocsPlan) {
    throw 'docs/implementation-plan.md must be an exact copy of plan.md.'
}

$GoFiles = Get-ChildItem cmd, internal, tests, third_party/ansi, third_party/vt, third_party/xpty, third_party/ultraviolet -Recurse -Filter *.go -ErrorAction SilentlyContinue
if ($GoFiles) {
    $Unformatted = $GoFiles.FullName | ForEach-Object { gofmt -l $_ }
    if ($Unformatted) { throw "Go files require formatting: $($Unformatted -join ', ')" }
}

$env:GOTOOLCHAIN = 'go1.27.1'
go vet ./...
go test ./...
Push-Location third_party/ansi
try { go test ./... } finally { Pop-Location }
Push-Location third_party/vt
try { go test ./... } finally { Pop-Location }
Push-Location third_party/xpty
try { go test ./... } finally { Pop-Location }
go test github.com/charmbracelet/ultraviolet/...
pnpm typecheck
pnpm test:web
pnpm build:web
