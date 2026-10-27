param([Parameter(Mandatory)][string]$Package, [Parameter(Mandatory)][string]$ExtractDirectory)
$ErrorActionPreference = 'Stop'
$archive = [IO.Compression.ZipFile]::OpenRead([IO.Path]::GetFullPath($Package))
try {
    $entry = $archive.GetEntry('checksums.json')
    $reader = [IO.StreamReader]::new($entry.Open())
    try { $checksums = $reader.ReadToEnd() | ConvertFrom-Json -AsHashtable } finally { $reader.Dispose() }
    if ($checksums.algorithm -ne 'sha256' -or $archive.Entries.Count -ne $checksums.files.Count + 1) { throw 'Unexpected package checksum coverage' }
    foreach ($name in $checksums.files.Keys) {
        if ($name.Contains('..') -or $name.StartsWith('/') -or $name.Contains('\')) { throw 'Unsafe package path' }
        $file = $archive.GetEntry($name)
        if (-not $file) { throw "Missing package file: $name" }
        $stream = $file.Open(); $hash = [Security.Cryptography.SHA256]::Create()
        try { $actual = [Convert]::ToHexString($hash.ComputeHash($stream)).ToLowerInvariant() } finally { $hash.Dispose(); $stream.Dispose() }
        if ($actual -ne $checksums.files[$name]) { throw "Checksum mismatch: $name" }
    }
    [IO.Compression.ZipFileExtensions]::ExtractToDirectory($archive, [IO.Path]::GetFullPath($ExtractDirectory))
    Write-Output "PASS: all $($checksums.files.Count) package files verified and extracted"
} finally { $archive.Dispose() }
