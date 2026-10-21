[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$GameRoot,
    [Parameter(Mandatory)][string]$DatamineRoot,
    [Parameter(Mandatory)][string]$OutputRoot,
    [string]$Python = 'python',
    [ValidateRange(2, 8192)][int]$Resolution = 4096,
    [ValidateRange(60, 7200)][int]$TimeoutSeconds = 1200,
    [switch]$Resume,
    [switch]$SkipVerify,
    [switch]$ValidateOnly
)

$ErrorActionPreference = 'Stop'
$referenceCount = 69
$terrainRoot = [IO.Path]::GetFullPath($PSScriptRoot)
$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$converter = Join-Path $terrainRoot 'converter'
$references = Join-Path $terrainRoot 'AtlasReferences.json'
$geoReferences = Join-Path $terrainRoot 'GeoReferences.json'

function Resolve-RequiredPath([string]$Value, [string]$Label, [ValidateSet('Leaf', 'Container')][string]$Kind = 'Container') {
    if ([string]::IsNullOrWhiteSpace($Value)) { throw "$Label is empty." }
    $full = [IO.Path]::GetFullPath($Value)
    $exists = if ($Kind -eq 'Leaf') { Test-Path -LiteralPath $full -PathType Leaf } else { Test-Path -LiteralPath $full -PathType Container }
    if (-not $exists) { throw "$Label not found: $full" }
    return $full
}

function Compare-NormalizedPath([string]$Left, [string]$Right) {
    $leftFull = [IO.Path]::GetFullPath($Left).TrimEnd('\', '/') + [IO.Path]::DirectorySeparatorChar
    $rightFull = [IO.Path]::GetFullPath($Right).TrimEnd('\', '/') + [IO.Path]::DirectorySeparatorChar
    return [string]::Compare($leftFull, $rightFull, [StringComparison]::OrdinalIgnoreCase)
}

function Test-SameOrWithin([string]$Child, [string]$Parent) {
    $childFull = [IO.Path]::GetFullPath($Child).TrimEnd('\', '/') + [IO.Path]::DirectorySeparatorChar
    $parentFull = [IO.Path]::GetFullPath($Parent).TrimEnd('\', '/') + [IO.Path]::DirectorySeparatorChar
    return $childFull.StartsWith($parentFull, [StringComparison]::OrdinalIgnoreCase)
}

function Assert-Disjoint([string]$First, [string]$FirstLabel, [string]$Second, [string]$SecondLabel) {
    if ((Test-SameOrWithin $First $Second) -or (Test-SameOrWithin $Second $First)) {
        throw "$FirstLabel and $SecondLabel must not be the same directory or contain each other: $First ; $Second"
    }
}

$game = Resolve-RequiredPath $GameRoot 'GameRoot'
$datamine = Resolve-RequiredPath $DatamineRoot 'DatamineRoot'
$referencesPath = Resolve-RequiredPath $references 'AtlasReferences.json' 'Leaf'
$geoPath = Resolve-RequiredPath $geoReferences 'GeoReferences.json' 'Leaf'
$output = [IO.Path]::GetFullPath($OutputRoot)
if ($output.Equals([IO.Path]::GetPathRoot($output), [StringComparison]::OrdinalIgnoreCase)) { throw "Refusing filesystem root as OutputRoot: $output" }
Assert-Disjoint $game 'GameRoot' $datamine 'DatamineRoot'
Assert-Disjoint $output 'OutputRoot' $game 'GameRoot'
Assert-Disjoint $output 'OutputRoot' $datamine 'DatamineRoot'
Assert-Disjoint $output 'OutputRoot' $terrainRoot 'terrain source directory'
if (Test-SameOrWithin $repoRoot $output) { throw "Refusing OutputRoot that is the repository root or one of its ancestors: $output" }
$levels = Join-Path $game 'levels'
if (-not (Test-Path -LiteralPath $levels -PathType Container)) { throw "GameRoot has no levels directory: $levels" }
$configLevels = Join-Path $datamine 'aces.vromfs.bin_u\levels'
if (-not (Test-Path -LiteralPath $configLevels -PathType Container)) {
    $configLevels = Join-Path $datamine 'levels'
}
if (-not (Test-Path -LiteralPath $configLevels -PathType Container)) {
    throw "DatamineRoot has no levels config directory: $datamine"
}

try {
    $targetDocument = Get-Content -LiteralPath $referencesPath -Raw -Encoding UTF8 | ConvertFrom-Json
} catch {
    throw "Cannot parse AtlasReferences.json: $($_.Exception.Message)"
}
$targetProperties = @($targetDocument.PSObject.Properties)
if ($targetProperties.Count -ne $referenceCount) {
    throw "AtlasReferences.json has $($targetProperties.Count) IDs; expected exactly $referenceCount."
}
$targetIds = @($targetProperties | ForEach-Object { [string]$_.Name })
foreach ($id in $targetIds) {
    if ($id -notmatch '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$') { throw "Invalid target map ID: $id" }
    $source = Join-Path $levels ($id + '.bin')
    if (-not (Test-Path -LiteralPath $source -PathType Leaf)) { throw "Local game source is missing: $source" }
    $config = Join-Path $configLevels ($id + '.blkx')
    if (-not (Test-Path -LiteralPath $config -PathType Leaf)) { throw "Datamine level config is missing: $config" }
}

if ([IO.Path]::IsPathRooted($Python) -or $Python.Contains('\') -or $Python.Contains('/')) {
    $pythonExe = Resolve-RequiredPath $Python 'Python' 'Leaf'
} else {
    $pythonCommand = Get-Command $Python -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($null -eq $pythonCommand) { throw "Python executable not found: $Python" }
    $pythonExe = $pythonCommand.Source
}

$versionProbe = @'
import importlib.metadata as metadata
import numpy, PIL, zstandard, ooz
expected = {'numpy':'2.3.5', 'Pillow':'12.3.0', 'pyooz':'0.0.8', 'zstandard':'0.25.0'}
actual = {name: metadata.version(name) for name in expected}
if actual != expected:
    raise SystemExit('dependency mismatch: ' + ', '.join(f'{k}={v}' for k, v in actual.items()))
print('Python dependencies:', ', '.join(f'{k}={v}' for k, v in actual.items()))
'@
$versions = & $pythonExe -B -c $versionProbe 2>&1 | Out-String
if ($LASTEXITCODE -ne 0) {
    throw "Python environment failed the pinned dependency check. Run: & '$pythonExe' -m pip install -r '$converter\requirements.txt'`n$versions"
}
Write-Host $versions.Trim()

if (Test-Path -LiteralPath $output -PathType Leaf) { throw "OutputRoot is a file: $output" }
if ((Test-Path -LiteralPath $output -PathType Container) -and -not $Resume -and -not $ValidateOnly) {
    $existing = @(Get-ChildItem -LiteralPath $output -Force | Select-Object -First 1)
    if ($existing.Count -ne 0) { throw "OutputRoot is not empty: $output. Choose a new directory or pass -Resume." }
}

Write-Host "Authoritative targets: $($targetIds.Count) (AtlasReferences.json)"
Write-Host "Resolution: $Resolution; per-map timeout: $TimeoutSeconds seconds; memory Job guard: 2 GiB"
if ($ValidateOnly) {
    Write-Host "PASS: inputs, 69 target sources/configs, and pinned Python dependencies validated; no files were written."
    return
}

New-Item -ItemType Directory -Path $output, (Join-Path $output 'maps'), (Join-Path $output 'tmp') -Force | Out-Null
Copy-Item -LiteralPath $referencesPath -Destination (Join-Path $output 'AtlasReferences.json') -Force
Copy-Item -LiteralPath $geoPath -Destination (Join-Path $output 'GeoReferences.json') -Force

function Invoke-Python([string]$Label, [string[]]$Arguments) {
    Write-Host "== $Label =="
    & $pythonExe -B @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$Label failed with exit code $LASTEXITCODE." }
}

$catalogPath = Join-Path $output 'catalog.json'
$catalogArgs = @(
    (Join-Path $converter 'catalog.py'),
    '--client', $game,
    '--datamine', $datamine,
    '--targets', $referencesPath,
    '--expected-targets', [string]$referenceCount,
    '--output', $catalogPath,
    '--air-only'
)
Invoke-Python 'Catalog authoritative level configs' $catalogArgs

$batchArgs = @(
    (Join-Path $converter 'batch.py'),
    '--catalog', $catalogPath,
    '--references', $referencesPath,
    '--expected-targets', [string]$referenceCount,
    '--output', $output,
    '--resolution', [string]$Resolution,
    '--timeout', [string]$TimeoutSeconds
)
if ($Resume) { $batchArgs += '--resume' }
Invoke-Python 'Convert authoritative map set' $batchArgs

Invoke-Python 'Summarize package timings and modes' @(
    (Join-Path $converter 'summarize.py'),
    '--root', $output,
    '--catalog', $catalogPath
)

if (-not $SkipVerify) {
    Invoke-Python 'Independently verify output rasters and memory limit' @(
        (Join-Path $converter 'verify_outputs.py'),
        '--root', $output,
        '--catalog', $catalogPath,
        '--verification-json', (Join-Path $output 'verification.json'),
        '--overview', (Join-Path $output 'overview.png')
    )
}

Invoke-Python 'Finalize installer and Sinai overlay contract' @(
    (Join-Path $converter 'finalize.py'),
    '--root', $output,
    '--references', $referencesPath,
    '--catalog', $catalogPath,
    '--expected-count', [string]$referenceCount,
    '--special', 'avg_egypt_sinai=render_mesh_hm2_overlay'
)

Write-Host "PASS: $referenceCount installer-ready terrain maps written to $output"
