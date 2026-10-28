[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$CacheRoot,
    [string]$OutputRoot = ''
)
$ErrorActionPreference = 'Stop'
$repo = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$cache = [IO.Path]::GetFullPath($CacheRoot)
$lockPath = Join-Path $PSScriptRoot 'runtime-lock.json'
$lockHash = (Get-FileHash -LiteralPath $lockPath -Algorithm SHA256).Hash.ToLowerInvariant()
$lock = Get-Content -LiteralPath $lockPath -Raw -Encoding UTF8 | ConvertFrom-Json
if ($lock.schema -ne 1 -or $lock.platform -ne 'windows-amd64') { throw 'Unsupported runtime lock.' }
if (-not $OutputRoot) { $OutputRoot = Join-Path $cache ('runtime-' + $lockHash.Substring(0,16)) }
$output = [IO.Path]::GetFullPath($OutputRoot)
$downloads = Join-Path $cache 'downloads'
New-Item -ItemType Directory -Path $downloads -Force | Out-Null
Add-Type -AssemblyName System.IO.Compression.FileSystem

function Expand-PinnedZip([string]$Archive, [string]$Destination, [bool]$Wheel) {
    $zip = [IO.Compression.ZipFile]::OpenRead($Archive)
    $prefix = [IO.Path]::GetFullPath($Destination).TrimEnd('\','/') + [IO.Path]::DirectorySeparatorChar
    [long]$total = 0
    try {
        if ($zip.Entries.Count -gt 10000) { throw 'Too many runtime archive entries.' }
        foreach ($entry in $zip.Entries) {
            $name = $entry.FullName.Replace('\','/')
            # Wheel entry-point scripts and test fixtures are not required at runtime.
            if ($Wheel -and ($name -match '\.data/' -or $name -match '(^|/)(tests|test)/')) { continue }
            if ($name.EndsWith('/')) { continue }
            if ($name -match '(^|/)\.\.(/|$)' -or $name.Contains(':') -or $name.StartsWith('/')) { throw "Unsafe archive path: $name" }
            $target = [IO.Path]::GetFullPath((Join-Path $Destination $name))
            if (-not $target.StartsWith($prefix,[StringComparison]::OrdinalIgnoreCase)) { throw "Archive path escapes destination: $name" }
            $total += $entry.Length
            if ($entry.Length -gt 268435456 -or $total -gt 1073741824) { throw 'Runtime archive exceeds size limit.' }
            New-Item -ItemType Directory -Path (Split-Path -Parent $target) -Force | Out-Null
            if (Test-Path -LiteralPath $target) { throw "Duplicate runtime archive file: $target" }
            $inputStream = $entry.Open()
            $outputStream = [IO.File]::Open($target,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write)
            try { $inputStream.CopyTo($outputStream,131072) }
            finally { $outputStream.Dispose(); $inputStream.Dispose() }
        }
    } finally { $zip.Dispose() }
}

# Always reassemble from verified cached downloads. This avoids trusting a
# pre-existing interpreter or executable in a mutable build cache.
$stage = Join-Path $cache ('stage-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $stage -Force | Out-Null
foreach ($file in $lock.files) {
    if ($file.name -cnotmatch '^[A-Za-z0-9._-]+$' -or $file.sha256 -cnotmatch '^[a-f0-9]{64}$' -or $file.url -notmatch '^https://') { throw 'Invalid runtime lock entry.' }
    $archive = Join-Path $downloads $file.name
    $valid = (Test-Path -LiteralPath $archive -PathType Leaf) -and
        ((Get-Item -LiteralPath $archive).Length -eq [long]$file.bytes) -and
        ((Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant() -ceq $file.sha256)
    if (-not $valid) {
        $temporary = $archive + '.download-' + [Guid]::NewGuid().ToString('N')
        Write-Host "Downloading pinned runtime dependency: $($file.name)"
        [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
        Invoke-WebRequest -UseBasicParsing -Uri $file.url -OutFile $temporary
        if ((Get-Item -LiteralPath $temporary).Length -ne [long]$file.bytes -or
            (Get-FileHash -LiteralPath $temporary -Algorithm SHA256).Hash.ToLowerInvariant() -cne $file.sha256) { throw "Runtime download hash/size mismatch: $($file.name)" }
        Move-Item -LiteralPath $temporary -Destination $archive -Force
    }
    switch ($file.kind) {
        'python' { Expand-PinnedZip $archive $stage $false }
        'wheel' { Expand-PinnedZip $archive (Join-Path $stage 'Lib/site-packages') $true }
        'source' {
            $sources = Join-Path $stage 'licenses/sources'
            New-Item -ItemType Directory -Path $sources -Force | Out-Null
            Copy-Item -LiteralPath $archive -Destination $sources
        }
        default { throw "Unknown runtime component kind: $($file.kind)" }
    }
}
$utf8 = New-Object Text.UTF8Encoding($false)
[IO.File]::WriteAllText((Join-Path $stage 'python312._pth'),"python312.zip`n.`nLib/site-packages`n",$utf8)
$licenses = Join-Path $stage 'licenses'
New-Item -ItemType Directory -Path $licenses -Force | Out-Null
Copy-Item -LiteralPath (Join-Path $repo 'LICENSE') -Destination (Join-Path $licenses 'wrpl-acmi-AGPL-3.0.txt')
Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'georeference/UPSTREAM-MIT.txt') -Destination (Join-Path $licenses 'WT-MAP-ANALYZE-MIT.txt')
Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'georeference/UPSTREAM-NOTICE.md') -Destination $licenses
$goRoot = (& go env GOROOT | Out-String).Trim()
if ($LASTEXITCODE -ne 0) { throw 'Cannot locate Go license.' }
Copy-Item -LiteralPath (Join-Path $goRoot 'LICENSE') -Destination (Join-Path $licenses 'Go-BSD-3-Clause.txt')
Copy-Item -LiteralPath $lockPath -Destination (Join-Path $licenses 'runtime-lock.json')
$notices = @'
WRPL-ACMI local terrain converter: AGPL-3.0; source https://github.com/3794432/wrpl-acmi
Go runtime and golang.org/x/sys: BSD-3-Clause; see Go-BSD-3-Clause.txt.
CPython: PSF license and incorporated notices; see ../LICENSE.txt.
NumPy: BSD-3-Clause and bundled component licenses; see ../Lib/site-packages/numpy-*.dist-info/licenses/.
Pillow: HPND/MIT-CMU and bundled component licenses; see ../Lib/site-packages/pillow-*.dist-info/licenses/.
zstandard: BSD-3-Clause; see ../Lib/site-packages/zstandard-*.dist-info/licenses/.
pyooz / ooz: GPL-3.0-or-later; matching source is included in sources/pyooz-0.0.8.tar.gz.
WT-MAP-ANALYZE geographic references: MIT; see WT-MAP-ANALYZE-MIT.txt and UPSTREAM-NOTICE.md.
Exact downloadable runtime dependencies, SHA-256 and source URLs: runtime-lock.json.
No War Thunder height rasters, game archives, level binaries, models or textures are included.
'@
[IO.File]::WriteAllText((Join-Path $licenses 'THIRD-PARTY-NOTICES.txt'),$notices,$utf8)
$probe = 'import sys,numpy,PIL,ooz,zstandard; print("Portable runtime",sys.version.split()[0],numpy.__version__,PIL.__version__,zstandard.__version__)'
$probeOutput = & (Join-Path $stage 'python.exe') -I -B -c $probe
if ($LASTEXITCODE -ne 0) { throw 'Portable runtime dependency check failed.' }
Write-Host ($probeOutput | Out-String).Trim()
$manifest = [ordered]@{ schema=1; python_version=[string]$lock.python_version; files=[ordered]@{} }
foreach ($entry in Get-ChildItem -LiteralPath $stage -File -Recurse | Sort-Object FullName) {
    $relative = $entry.FullName.Substring($stage.Length).TrimStart('\','/').Replace('\','/')
    $manifest.files[$relative] = [ordered]@{ size=[long]$entry.Length; sha256=(Get-FileHash -LiteralPath $entry.FullName -Algorithm SHA256).Hash.ToLowerInvariant() }
}
[IO.File]::WriteAllText((Join-Path $stage 'runtime-manifest.json'),($manifest | ConvertTo-Json -Depth 8),$utf8)
if (Test-Path -LiteralPath $output) {
    # The packer validates this directory independently before using it.
    $output = $output + '-' + [Guid]::NewGuid().ToString('N')
}
Move-Item -LiteralPath $stage -Destination $output
Write-Host "Prepared open-source Python runtime: $output"
return $output
