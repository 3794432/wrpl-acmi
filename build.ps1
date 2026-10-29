[CmdletBinding()]
param(
    [string]$OutputRoot = (Join-Path $PSScriptRoot 'dist/wrpl-acmi'),
    [switch]$WithTerrain,
    [switch]$ResumeTerrain,
    [string]$TerrainSource,
    [string]$GameRoot,
    [string]$DatamineRoot,
    [string]$Python = 'python',
    [string]$PythonRuntime,
    [string]$RuntimeCache = (Join-Path $env:LOCALAPPDATA 'wrpl-acmi-build/terrain-runtime')
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
if ($WithTerrain -or $TerrainSource -or $ResumeTerrain -or $GameRoot -or $DatamineRoot) {
    throw 'Game terrain payload packaging has been removed. Build normally, then place wrpl-terrain-installer.exe in the game root to convert local assets. Use tools/terrain/build-terrain.ps1 only for a separate developer conversion.'
}
if ((Test-Path -LiteralPath (Join-Path $output 'terrain')) -or
    @((Get-ChildItem -LiteralPath $output -File -Filter '*.data' -Recurse -ErrorAction SilentlyContinue) | Select-Object -First 1).Count -gt 0) {
    throw 'OutputRoot contains old terrain data. Choose a new output directory; public packages must not include game rasters.'
}
if ($PythonRuntime) { $PythonRuntime = (Resolve-Path -LiteralPath $PythonRuntime).Path }
$RuntimeCache = [IO.Path]::GetFullPath($RuntimeCache)
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
    Copy-PackageTree (Join-Path $repoRoot 'tools/sam') (Join-Path $tools 'sam')
    Copy-PackageTree (Join-Path $repoRoot 'tools/agm') (Join-Path $tools 'agm')
    Copy-PackageTree (Join-Path $repoRoot 'tools/bombs') (Join-Path $tools 'bombs')
    foreach ($name in @('README.md', 'README.zh-CN.md', 'LICENSE')) {
        Copy-Item -LiteralPath (Join-Path $repoRoot $name) -Destination $output -Force
    }
    Copy-Item -LiteralPath (Join-Path $repoRoot 'tools/run.bat') -Destination (Join-Path $output 'run.bat') -Force
    $installer = Join-Path $output 'wrpl-terrain-installer.exe'
    if (-not $PythonRuntime) {
        $runtimeOutput = & (Join-Path $repoRoot 'tools/terrain/build-runtime.ps1') -CacheRoot $RuntimeCache
        if (-not $?) { throw 'Portable runtime build failed.' }
        $PythonRuntime = [string](@($runtimeOutput)[-1])
    }
    & $go run ./cmd/terrain-runtime-pack -stub $installerStub -runtime $PythonRuntime -out $installerPacked
    if ($LASTEXITCODE -ne 0) { throw "Portable-runtime packaging failed (exit $LASTEXITCODE)." }
    Move-Item -LiteralPath $installerPacked -Destination $installer -Force
    $binaries = @{
        'wrpl-acmi.exe' = (Get-FileHash -LiteralPath $binary -Algorithm SHA256).Hash.ToLowerInvariant()
        'wrpl-terrain-installer.exe' = (Get-FileHash -LiteralPath $installer -Algorithm SHA256).Hash.ToLowerInvariant()
    }
    $manifest = [ordered]@{
        schema_version = 1
        built_utc = [DateTime]::UtcNow.ToString('o')
        terrain_built = $false
        terrain_installer_includes_data = $false
        terrain_installer_local_conversion = $true
        terrain_installer_includes_python_runtime = $true
        binaries_sha256 = $binaries
    }
    $manifest | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $output 'build-manifest.json') -Encoding UTF8
    Remove-Item -LiteralPath $incomplete -Force
    Write-Host "Runtime package ready: $output"
    Write-Host 'wrpl-acmi.exe runs independently; no adjacent data files or installed terrain are required.'
    Write-Host 'Terrain installer includes conversion code and its open-source runtime, with no game heights.'
    Write-Host 'Place wrpl-terrain-installer.exe in the War Thunder root (beside levels/ and aces.vromfs.bin), then run it.'
} finally {
    if (Test-Path -LiteralPath $installerStub -PathType Leaf) { Remove-Item -LiteralPath $installerStub -Force }
    if (Test-Path -LiteralPath $installerPacked -PathType Leaf) { Remove-Item -LiteralPath $installerPacked -Force }
    $env:CGO_ENABLED = $oldCGO
    Pop-Location
}
