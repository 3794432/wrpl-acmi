[CmdletBinding()]
param([Parameter(Mandatory)][string]$PackageRoot)
$ErrorActionPreference = 'Stop'
$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../../..'))
$package = (Resolve-Path -LiteralPath $PackageRoot).Path
$runtimeParent = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot 'runtime'))
$runtime = Join-Path $runtimeParent ('standalone-' + [Guid]::NewGuid().ToString('N'))
$runtimeFull = [IO.Path]::GetFullPath($runtime)
if (-not $runtimeFull.StartsWith($runtimeParent + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) { throw 'Invalid test runtime.' }
$oldTemp = $env:TEMP
$oldTmp = $env:TMP
New-Item -ItemType Directory -Path $runtimeFull -Force | Out-Null
try {
    $env:TEMP = $runtimeFull
    $env:TMP = $runtimeFull
    $fixture = Join-Path $PSScriptRoot 'geographic/fixture'
    if (-not (Test-Path -LiteralPath (Join-Path $fixture 'AtlasReferences.json'))) { throw 'Generate the geographic fixtures first.' }
    $single = Join-Path $runtimeFull 'only-one-file'
    New-Item -ItemType Directory -Path $single | Out-Null
    $installer = Join-Path $single 'wrpl-terrain-installer.exe'
    $go = (Get-Command go -CommandType Application -ErrorAction Stop | Select-Object -First 1).Source
    Push-Location $repoRoot
    try {
        & $go run ./cmd/terrain-pack -stub (Join-Path $package 'wrpl-terrain-installer.exe') -source $fixture -out $installer
        if ($LASTEXITCODE -ne 0) { throw 'Synthetic terrain packaging failed.' }
    } finally { Pop-Location }
    $destination = Join-Path $runtimeFull 'Tacview/Data/Terrain/Custom/WarThunderMaps'
    Push-Location $single
    try {
        & $installer -destination $destination -profile 1.9.4 -validate-only -allow-partial
        if ($LASTEXITCODE -ne 0) { throw 'Standalone validation failed.' }
        if (Test-Path -LiteralPath $destination) { throw 'ValidateOnly wrote the destination.' }
        & $installer -destination $destination -profile 1.9.4 -allow-partial
        if ($LASTEXITCODE -ne 0) { throw 'Standalone 1.9.4 installation failed.' }
        $state = Get-Content -LiteralPath (Join-Path $destination 'WarThunderMaps.format.json') -Raw | ConvertFrom-Json
        if ($state.profile -ne '1.9.4') { throw 'Wrong installed profile.' }
        foreach ($id in @('geo_alpha', 'geo_beta')) {
            $original = Join-Path $fixture "maps/$id/$id.data"
            $installed = Join-Path $destination "$id/$id.data"
            if ((Get-FileHash -LiteralPath $original).Hash -ne (Get-FileHash -LiteralPath $installed).Hash) { throw "Canonical raster changed: $id" }
            if (-not (Test-Path -LiteralPath (Join-Path $destination "_uint16/$id.data"))) { throw "Unsigned cache missing: $id" }
        }
        & $installer -destination $destination -profile 1.9.4 -allow-partial
        if ($LASTEXITCODE -ne 0) { throw 'Standalone repeat installation failed.' }
        # Reproduce an older index left behind after its state/cache were lost.
        foreach ($id in @('geo_alpha', 'geo_beta')) {
            Copy-Item -LiteralPath (Join-Path $destination "$id/CustomHeightmapList.native195") -Destination (Join-Path $destination "$id/CustomHeightmapList.xml") -Force
        }
        [IO.File]::Delete((Join-Path $destination 'WarThunderMaps.format.json'))
        $cache = Join-Path $destination '_uint16'
        foreach ($name in @('geo_alpha.data', 'geo_beta.data', 'cache.json')) {
            [IO.File]::Delete((Join-Path $cache $name))
        }
        [IO.Directory]::Delete($cache, $false)
        & $installer -destination $destination -profile 1.9.4 -allow-partial
        if ($LASTEXITCODE -ne 0) { throw 'Legacy index without state/cache could not be repaired.' }
        $state = Get-Content -LiteralPath (Join-Path $destination 'WarThunderMaps.format.json') -Raw | ConvertFrom-Json
        if ($state.profile -ne '1.9.4') { throw 'Legacy repair did not restore the 1.9.4 state.' }
        foreach ($id in @('geo_alpha', 'geo_beta')) {
            if (-not (Test-Path -LiteralPath (Join-Path $destination "_uint16/$id.data"))) { throw "Legacy cache repair missing: $id" }
        }
        & $installer -destination $destination -profile 1.9.5 -allow-partial
        if ($LASTEXITCODE -ne 0) { throw 'Standalone 1.9.5 installation failed.' }
        $state = Get-Content -LiteralPath (Join-Path $destination 'WarThunderMaps.format.json') -Raw | ConvertFrom-Json
        if ($state.profile -ne '1.9.5') { throw 'Profile switch did not complete.' }
        if (@(Get-ChildItem -LiteralPath $single -Force).Count -ne 1) { throw 'Installer left dependencies beside the executable.' }
    } finally { Pop-Location }
    Write-Host 'PASS: single-file payload validation, offline installation, repeat installation, legacy index repair, both profiles, and canonical raster hashes.'
} finally {
    $env:TEMP = $oldTemp
    $env:TMP = $oldTmp
    if (Test-Path -LiteralPath $runtimeFull) {
        if (-not $runtimeFull.StartsWith($runtimeParent + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) { throw 'Unsafe cleanup target.' }
        Remove-Item -LiteralPath $runtimeFull -Recurse -Force
    }
}
