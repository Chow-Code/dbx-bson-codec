param([string]$BuildDirectory, [switch]$SkipTests)
$ErrorActionPreference = 'Stop'
if (-not $BuildDirectory) { $BuildDirectory = Join-Path $PSScriptRoot 'work' }
$buildRoot = [IO.Path]::GetFullPath($BuildDirectory)
$stage = Join-Path $buildRoot ('package-' + [Guid]::NewGuid().ToString('N'))
$dist = Join-Path $PSScriptRoot 'dist'
New-Item -ItemType Directory -Force $stage, $dist, (Join-Path $stage 'bin/windows-x64') | Out-Null
Push-Location $PSScriptRoot
$previousGoOS = $env:GOOS; $previousGoArch = $env:GOARCH; $previousCGO = $env:CGO_ENABLED
try {
    if (-not $SkipTests) { go test ./...; if ($LASTEXITCODE -ne 0) { throw 'Go tests failed' } }
    $env:GOOS = 'windows'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
    go build -buildvcs=false -trimpath -ldflags '-s -w' -o (Join-Path $stage 'bin/windows-x64/backend.exe') ./backend
    if ($LASTEXITCODE -ne 0) { throw 'Go build failed' }
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'manifest.json') -Destination $stage
    foreach ($folder in @('assets', 'ui', 'THIRD_PARTY_LICENSES', 'LICENSE', 'NOTICE')) { Copy-Item -LiteralPath (Join-Path $PSScriptRoot $folder) -Destination $stage -Recurse }
    $checksums = [ordered]@{}
    foreach ($file in Get-ChildItem -LiteralPath $stage -Recurse -File | Sort-Object FullName) {
        $relative = [IO.Path]::GetRelativePath($stage, $file.FullName).Replace('\', '/')
        $checksums[$relative] = (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
    }
    $checksumJson = [ordered]@{ algorithm = 'sha256'; files = $checksums } | ConvertTo-Json -Depth 10
    [IO.File]::WriteAllText((Join-Path $stage 'checksums.json'), $checksumJson, [Text.UTF8Encoding]::new($false))
    $manifest = Get-Content -LiteralPath (Join-Path $stage 'manifest.json') -Raw | ConvertFrom-Json
    $filename = "$($manifest.id)-$($manifest.version)-windows-x64.dbxp"
    $destination = Join-Path $dist $filename
    $temporaryPackage = Join-Path $buildRoot ([Guid]::NewGuid().ToString('N') + '.dbxp')
    $stream = [IO.File]::Create($temporaryPackage)
    $archive = [IO.Compression.ZipArchive]::new($stream, [IO.Compression.ZipArchiveMode]::Create, $false)
    try {
        foreach ($file in Get-ChildItem -LiteralPath $stage -Recurse -File | Sort-Object FullName) {
            $relative = [IO.Path]::GetRelativePath($stage, $file.FullName).Replace('\', '/')
            $entry = $archive.CreateEntry($relative, [IO.Compression.CompressionLevel]::Optimal)
            $entry.LastWriteTime = [DateTimeOffset]::new(1980, 1, 1, 0, 0, 0, [TimeSpan]::Zero)
            $entry.ExternalAttributes = 33188 -shl 16
            $inputStream = [IO.File]::OpenRead($file.FullName)
            $entryStream = $entry.Open()
            try { $inputStream.CopyTo($entryStream) } finally { $entryStream.Dispose(); $inputStream.Dispose() }
        }
    } finally { $archive.Dispose() }
    Move-Item -LiteralPath $temporaryPackage -Destination $destination -Force
    [IO.File]::WriteAllText(($destination + '.sha256'), (Get-FileHash -LiteralPath $destination -Algorithm SHA256).Hash.ToLowerInvariant() + "  $filename`n")
    Write-Output $destination
} finally {
    $env:GOOS = $previousGoOS; $env:GOARCH = $previousGoArch; $env:CGO_ENABLED = $previousCGO
    Pop-Location
}
