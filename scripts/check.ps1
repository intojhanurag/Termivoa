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

$GoFiles = Get-ChildItem cmd, internal, tests -Recurse -Filter *.go -ErrorAction SilentlyContinue
if ($GoFiles) {
    $Unformatted = $GoFiles.FullName | ForEach-Object { gofmt -l $_ }
    if ($Unformatted) { throw "Go files require formatting: $($Unformatted -join ', ')" }
}

$env:GOTOOLCHAIN = 'go1.27.1'
go vet ./...
go test ./...
pnpm typecheck
pnpm test:web
pnpm build:web
