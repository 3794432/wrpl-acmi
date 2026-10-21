[CmdletBinding()]
param(
    [string]$OutputRoot = (Join-Path $PSScriptRoot 'dist/wrpl-acmi'),
    [switch]$WithTerrain,
    [switch]$ResumeTerrain,
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
if ($ResumeTerrain -and -not $WithTerrain) { throw '-ResumeTerrain requires -WithTerrain.' }
if ($WithTerrain) {
    # Resolve inputs before entering the repository directory.
    $GameRoot = (Resolve-Path -LiteralPath $GameRoot).Path
    $DatamineRoot = (Resolve-Path -LiteralPath $DatamineRoot).Path
    $Python = (Get-Command $Python -CommandType Application -ErrorAction Stop).Source
}
$go = (Get-Command go -CommandType Application -ErrorAction Stop).Source
New-Item -ItemType Directory -Path $output -Force | Out-Null
$incomplete = Join-Path $output 'BUILD-INCOMPLETE.txt'
Set-Content -LiteralPath $incomplete -Encoding ASCII -Value 'Build in progress or failed. Run build.ps1 again to finish this package.'
$oldCGO = $env:CGO_ENABLED
Push-Location $repoRoot
try {
    $env:CGO_ENABLED = '0'
    foreach ($target in @(
        @{ Name = 'wrpl-acmi'; Package = './cmd/acmi' },
        @{ Name = 'acmi-mapid'; Package = './cmd/acmi-mapid' },
        @{ Name = 'acmi-atlas'; Package = './cmd/acmi-atlas' }
    )) {
        $binary = Join-Path $output ($target.Name + '.exe')
        & $go build -trimpath -o $binary $target.Package
        if ($LASTEXITCODE -ne 0) { throw "Go build failed for $($target.Name) (exit $LASTEXITCODE)." }
    }
    foreach ($folder in @('data', 'docs')) {
        Copy-PackageTree (Join-Path $repoRoot $folder) (Join-Path $output $folder)
    }
    $tools = Join-Path $output 'tools'
    New-Item -ItemType Directory -Path $tools -Force | Out-Null
    Copy-PackageTree (Join-Path $repoRoot 'tools/terrain') (Join-Path $tools 'terrain')
    foreach ($name in @('README.md', 'README.zh-CN.md', 'LICENSE')) {
        Copy-Item -LiteralPath (Join-Path $repoRoot $name) -Destination $output -Force
    }
    Copy-Item -LiteralPath (Join-Path $repoRoot 'tools/run.bat') -Destination (Join-Path $output 'run.bat') -Force
    if ($WithTerrain) {
        & (Join-Path $repoRoot 'tools/terrain/build-terrain.ps1') -GameRoot $GameRoot -DatamineRoot $DatamineRoot -OutputRoot (Join-Path $output 'terrain') -Python $Python -Resume:$ResumeTerrain
        if (-not $?) { throw 'Terrain build failed.' }
    }
    $binaries = @{}
    foreach ($name in @('wrpl-acmi.exe', 'acmi-mapid.exe', 'acmi-atlas.exe')) {
        $binaries[$name] = (Get-FileHash -LiteralPath (Join-Path $output $name) -Algorithm SHA256).Hash.ToLowerInvariant()
    }
    $manifest = [ordered]@{
        schema_version = 1
        built_utc = [DateTime]::UtcNow.ToString('o')
        terrain_built = [bool]$WithTerrain
        binaries_sha256 = $binaries
    }
    $manifest | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $output 'build-manifest.json') -Encoding UTF8
    Remove-Item -LiteralPath $incomplete -Force
    Write-Host "Runtime package ready: $output"
    if ($WithTerrain) {
        Write-Host 'Install terrain using tools/terrain/Install-All-Terrains.cmd -SourceRoot <package>/terrain.'
    }
} finally {
    $env:CGO_ENABLED = $oldCGO
    Pop-Location
}
