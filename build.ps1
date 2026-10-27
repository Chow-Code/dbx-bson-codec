param([ValidateSet('windows-x64','darwin-arm64','darwin-x64','linux-x64','linux-arm64')][string]$Target='windows-x64',[switch]$SkipTests)
$ErrorActionPreference='Stop'
Push-Location $PSScriptRoot
try {
    if(-not $SkipTests){go test ./...; if($LASTEXITCODE -ne 0){throw 'Go tests failed'}}
    go run ./cmd/package -target $Target
    if($LASTEXITCODE -ne 0){throw 'Package build failed'}
} finally {Pop-Location}
