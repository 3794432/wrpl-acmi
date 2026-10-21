$ErrorActionPreference = 'Stop'

. (Join-Path $PSScriptRoot 'FixtureTools.ps1')

$fixtureRoot = Join-Path $PSScriptRoot 'fixture'
$fixtureRootFull = [IO.Path]::GetFullPath($fixtureRoot)
$testPrefix = [IO.Path]::GetFullPath($PSScriptRoot) + [IO.Path]::DirectorySeparatorChar
if (-not $fixtureRootFull.StartsWith($testPrefix, [StringComparison]::OrdinalIgnoreCase)) {
    throw "Refusing fixture root outside installer-tests: $fixtureRootFull"
}
if (Test-Path -LiteralPath $fixtureRootFull) { Remove-Item -LiteralPath $fixtureRootFull -Recurse -Force }
New-Item -ItemType Directory -Path $fixtureRootFull | Out-Null

$alpha = Write-FixtureMap $fixtureRootFull 'geo_alpha' @(-65536.0, -65536.0, 65536.0, 65536.0) @(-65536.0, -65536.0, 65504.0, 65504.0)
$beta = Write-FixtureMap $fixtureRootFull 'geo_beta' @(-32768.0, -32768.0, 32768.0, 32768.0) @(-32768.0, -32768.0, 32736.0, 32736.0)

$positions = [ordered]@{}
$positions['geo_alpha'] = [ordered]@{ latitude = -60.0; longitude = -140.0 }
$positions['geo_beta'] = [ordered]@{ latitude = -60.0; longitude = -100.0 }
Write-AtlasFile (Join-Path $fixtureRootFull 'AtlasReferences.json') $positions

$maps = [ordered]@{}
$maps['geo_alpha'] = New-GeoEntry $true @(-65536.0, -65536.0, 65536.0, 65536.0) @(68.5, 34.2, 70.1, 35.5) 1.0
$maps['geo_beta'] = New-GeoEntry $true @(-32768.0, -32768.0, 32768.0, 32768.0) @(74.0, 36.0, 75.5, 37.2) 2.0
Write-GeoFile (Join-Path $fixtureRootFull 'GeoReferences.json') $maps

Write-Host "Wrote geographic fixture to $fixtureRootFull"
Write-Host ("geo_alpha: {0} bytes, {1}" -f $alpha.DataBytes, $alpha.DataHash)
Write-Host ("geo_beta:  {0} bytes, {1}" -f $beta.DataBytes, $beta.DataHash)
