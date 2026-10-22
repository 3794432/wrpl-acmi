[CmdletBinding()]
param(
    [string]$OutputRoot = (Join-Path $PSScriptRoot 'dist/wrpl-acmi'),
    [switch]$WithTerrain,
    [switch]$ResumeTerrain,
    [string]$TerrainSource,
    [string]$GameRoot,
    [string]$DatamineRoot,
    [string]$Python = 'python'
)

$ErrorActionPreference = 'Stop'
$repoRoot = $PSScriptRoot
$output = [IO.Path]::GetFullPath($OutputRoot)
if ($output -eq $repoRoot -or $repoRoot.StartsWith($output.TrimEnd('\', '/') + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
    throw 'OutputRoot must not be the source directory or one of its parents.'
}
foreach ($name in @('data', 'docs', 'tools')) {
    $source = Join-Path $repoRoot $name
    if ($output -eq $source -or $output.StartsWith($source + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
        throw 'OutputRoot must not be inside data, docs, or tools.'
    }
}

function Copy-PackageTree([string]$Source, [string]$Destination) {
    foreach ($file in Get-ChildItem -LiteralPath $Source -File -Recurse) {
        $relative = $file.FullName.Substring($Source.Length).TrimStart('\', '/')
        if ($relative -match '(^|[\\/])(__pycache__|runtime|fixture|\.venv)([\\/]|$)' -or $relative -match '\.pyc$') { continue }
        $target = Join-Path $Destination $relative
        New-Item -ItemType Directory -Path (Split-Path -Parent $target) -Force | Out-Null
        Copy-Item -LiteralPath $file.FullName -Destination $target -Force
    }
}
if ($WithTerrain -and (-not $GameRoot -or -not $DatamineRoot)) {
    throw '-WithTerrain requires -GameRoot and -DatamineRoot.'
}
if ($WithTerrain -and $TerrainSource) { throw 'Use either -WithTerrain or -TerrainSource.' }
if ($TerrainSource) {
    $TerrainSource = (Resolve-Path -LiteralPath $TerrainSource).Path
    if (-not (Test-Path -LiteralPath $TerrainSource -PathType Container)) { throw '-TerrainSource must be a converted terrain directory.' }
}
if ($ResumeTerrain -and -not $WithTerrain) { throw '-ResumeTerrain requires -WithTerrain.' }
if ($WithTerrain) {
    # Resolve inputs before entering the repository directory.
    $GameRoot = (Resolve-Path -LiteralPath $GameRoot).Path
    $DatamineRoot = (Resolve-Path -LiteralPath $DatamineRoot).Path
    $Python = (Get-Command $Python -CommandType Application -ErrorAction Stop | Select-Object -First 1).Source
}
# Hosted runners can expose several Go installations. Respect PATH precedence.
$go = (Get-Command go -CommandType Application -ErrorAction Stop | Select-Object -First 1).Source
New-Item -ItemType Directory -Path $output -Force | Out-Null
$incomplete = Join-Path $output 'BUILD-INCOMPLETE.txt'
Set-Content -LiteralPath $incomplete -Encoding ASCII -Value 'Build in progress or failed. Run build.ps1 again to finish this package.'
$oldCGO = $env:CGO_ENABLED
$installerStub = Join-Path $output ('.terrain-installer-' + [Guid]::NewGuid().ToString('N') + '.exe')
$installerPacked = Join-Path $output ('.terrain-packed-' + [Guid]::NewGuid().ToString('N') + '.exe')
Push-Location $repoRoot
try {
    $env:CGO_ENABLED = '0'
    $binary = Join-Path $output 'wrpl-acmi.exe'
    & $go build -trimpath -o $binary ./cmd/acmi
    if ($LASTEXITCODE -ne 0) { throw "Go build failed for wrpl-acmi (exit $LASTEXITCODE)." }
    & $go build -trimpath -o $installerStub ./cmd/terrain-install
    if ($LASTEXITCODE -ne 0) { throw "Go build failed for terrain installer (exit $LASTEXITCODE)." }
    foreach ($name in @('acmi-mapid.exe', 'acmi-atlas.exe')) {
        $obsolete = Join-Path $output $name
        if (Test-Path -LiteralPath $obsolete -PathType Leaf) {
            Remove-Item -LiteralPath $obsolete -Force
        }
    }
    foreach ($folder in @('data', 'docs')) {
        Copy-PackageTree (Join-Path $repoRoot $folder) (Join-Path $output $folder)
    }
    $tools = Join-Path $output 'tools'
    New-Item -ItemType Directory -Path $tools -Force | Out-Null
    Copy-PackageTree (Join-Path $repoRoot 'tools/terrain') (Join-Path $tools 'terrain')
    Copy-PackageTree (Join-Path $repoRoot 'tools/airfields') (Join-Path $tools 'airfields')
    Copy-PackageTree (Join-Path $repoRoot 'tools/countermeasures') (Join-Path $tools 'countermeasures')
    foreach ($name in @('README.md', 'README.zh-CN.md', 'LICENSE')) {
        Copy-Item -LiteralPath (Join-Path $repoRoot $name) -Destination $output -Force
    }
    Copy-Item -LiteralPath (Join-Path $repoRoot 'tools/run.bat') -Destination (Join-Path $output 'run.bat') -Force
    if ($WithTerrain) {
        & (Join-Path $repoRoot 'tools/terrain/build-terrain.ps1') -GameRoot $GameRoot -DatamineRoot $DatamineRoot -OutputRoot (Join-Path $output 'terrain') -Python $Python -Resume:$ResumeTerrain
        if (-not $?) { throw 'Terrain build failed.' }
        $TerrainSource = Join-Path $output 'terrain'
    }
    $installer = Join-Path $output 'wrpl-terrain-installer.exe'
    if ($TerrainSource) {
        & $go run ./cmd/terrain-pack -stub $installerStub -source $TerrainSource -out $installerPacked
        if ($LASTEXITCODE -ne 0) { throw "Terrain installer packaging failed (exit $LASTEXITCODE)." }
        Move-Item -LiteralPath $installerPacked -Destination $installer -Force
    } else {
        Move-Item -LiteralPath $installerStub -Destination $installer -Force
    }
    $binaries = @{
        'wrpl-acmi.exe' = (Get-FileHash -LiteralPath $binary -Algorithm SHA256).Hash.ToLowerInvariant()
        'wrpl-terrain-installer.exe' = (Get-FileHash -LiteralPath $installer -Algorithm SHA256).Hash.ToLowerInvariant()
    }
    $manifest = [ordered]@{
        schema_version = 1
        built_utc = [DateTime]::UtcNow.ToString('o')
        terrain_built = [bool]$WithTerrain
        terrain_installer_includes_data = [bool]$TerrainSource
        binaries_sha256 = $binaries
    }
    $manifest | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $output 'build-manifest.json') -Encoding UTF8
    Remove-Item -LiteralPath $incomplete -Force
    Write-Host "Runtime package ready: $output"
    Write-Host 'wrpl-acmi.exe runs independently; no adjacent data files or installed terrain are required.'
    if ($TerrainSource) {
        Write-Host 'wrpl-terrain-installer.exe contains the terrain data and can install it on its own.'
    } else {
        Write-Host 'Terrain installer contains no game heights: pass -source <converted-terrain>, or rebuild with -TerrainSource / -WithTerrain.'
    }
} finally {
    if (Test-Path -LiteralPath $installerStub -PathType Leaf) { Remove-Item -LiteralPath $installerStub -Force }
    if (Test-Path -LiteralPath $installerPacked -PathType Leaf) { Remove-Item -LiteralPath $installerPacked -Force }
    $env:CGO_ENABLED = $oldCGO
    Pop-Location
}
