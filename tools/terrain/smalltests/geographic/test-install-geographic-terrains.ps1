$ErrorActionPreference = 'Stop'

. (Join-Path $PSScriptRoot 'FixtureTools.ps1')

$testRoot = Join-Path $PSScriptRoot 'runtime'
$testRootFull = [IO.Path]::GetFullPath($testRoot)
$testPrefix = [IO.Path]::GetFullPath($PSScriptRoot) + [IO.Path]::DirectorySeparatorChar
if (-not $testRootFull.StartsWith($testPrefix, [StringComparison]::OrdinalIgnoreCase)) {
    throw "Refusing test root outside installer-tests: $testRootFull"
}
if (Test-Path -LiteralPath $testRootFull) { Remove-Item -LiteralPath $testRootFull -Recurse -Force }
New-Item -ItemType Directory -Path $testRootFull | Out-Null

$fixtureRoot = Join-Path $PSScriptRoot 'fixture'
$geoScript = [IO.Path]::GetFullPath((Join-Path (Join-Path $PSScriptRoot '..\..') 'Install-Geographic-Terrains.ps1'))
$mainScript = [IO.Path]::GetFullPath((Join-Path (Join-Path $PSScriptRoot '..\..') 'Install-All-Terrains.ps1'))

function Assert-Condition {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) { throw "ASSERT: $Message" }
}

function Run-Tool {
    param([string]$Path, [string[]]$Arguments)

    $old = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $textOutput = & powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File $Path @Arguments 2>&1 | Out-String
        $code = $LASTEXITCODE
    }
    finally { $ErrorActionPreference = $old }
    return [pscustomobject]@{ ExitCode = $code; Output = $textOutput; FlatOutput = ($textOutput -replace '\s+', ' ') }
}

function Read-Manifest {
    param([string]$Destination)
    return Get-Content -LiteralPath (Join-Path $Destination 'WarThunderMaps.install.json') -Raw -Encoding UTF8 | ConvertFrom-Json
}

function Get-ManifestMap {
    param([object]$Manifest, [string]$Id)
    $matches = @($Manifest.maps | Where-Object { $_.id -eq $Id })
    Assert-Condition ($matches.Count -eq 1) "manifest entry count for $Id is $($matches.Count)"
    return $matches[0]
}

function Read-HeightmapNode {
    param([string]$Path)
    [xml]$document = Get-Content -LiteralPath $Path -Raw -Encoding UTF8
    return $document.Resources.CustomHeightmapList.CustomHeightmap
}

function Get-GeoExpectedCorners {
    param([double[]]$Local, [double[]]$Geo, [double[]]$Sample)

    $lonSpan = $Geo[2] - $Geo[0]
    $latSpan = $Geo[3] - $Geo[1]
    $xSpan = $Local[2] - $Local[0]
    $zSpan = $Local[3] - $Local[1]
    $definitions = @(
        @('BottomLeft', $Sample[0], $Sample[1]),
        @('BottomRight', $Sample[2], $Sample[1]),
        @('TopRight', $Sample[2], $Sample[3]),
        @('TopLeft', $Sample[0], $Sample[3])
    )
    $result = @()
    foreach ($definition in $definitions) {
        $result += [pscustomobject]@{
            Name = [string]$definition[0]
            Longitude = $Geo[0] + (([double]$definition[1] - $Local[0]) / $xSpan) * $lonSpan
            Latitude = $Geo[1] + (([double]$definition[2] - $Local[1]) / $zSpan) * $latSpan
        }
    }
    return $result
}

function Get-AtlasExpectedCorners {
    param([double[]]$Sample, [double]$AnchorLatitude, [double]$AnchorLongitude)

    $definitions = @(
        @('BottomLeft', $Sample[0], $Sample[1]),
        @('BottomRight', $Sample[2], $Sample[1]),
        @('TopRight', $Sample[2], $Sample[3]),
        @('TopLeft', $Sample[0], $Sample[3])
    )
    $result = @()
    foreach ($definition in $definitions) {
        $latitude = $AnchorLatitude + ([double]$definition[2] / 111320.0)
        $longitude = $AnchorLongitude + ([double]$definition[1] / (111320.0 * [Math]::Cos($latitude * [Math]::PI / 180.0)))
        $result += [pscustomobject]@{ Name = [string]$definition[0]; Longitude = $longitude; Latitude = $latitude }
    }
    return $result
}

function Assert-XmlCorners {
    param([object]$Node, [object[]]$Corners, [string]$Label)

    foreach ($corner in $Corners) {
        $element = $Node.SelectSingleNode($corner.Name)
        Assert-Condition ($null -ne $element) "$Label is missing $($corner.Name)"
        $longitude = [double]::Parse([string]$element.Longitude, [Globalization.NumberStyles]::Float, [Globalization.CultureInfo]::InvariantCulture)
        $latitude = [double]::Parse([string]$element.Latitude, [Globalization.NumberStyles]::Float, [Globalization.CultureInfo]::InvariantCulture)
        Assert-Condition ([Math]::Abs($longitude - $corner.Longitude) -le 1e-9) "$Label $($corner.Name) longitude"
        Assert-Condition ([Math]::Abs($latitude - $corner.Latitude) -le 1e-9) "$Label $($corner.Name) latitude"
    }
}

function Assert-GeoXml {
    param([string]$Path, [string]$Id, [double[]]$Local, [double[]]$Geo, [double[]]$Sample)

    $node = Read-HeightmapNode $Path
    Assert-Condition ([string]$node.Layer -eq 'Real World') "Layer for $Id"
    Assert-Condition ([string]$node.ID -eq ('WarThunder.Geo1.' + $Id)) "Geo ID for $Id"
    Assert-Condition ([string]$node.File -eq ($Id + '.data')) "File for $Id"
    Assert-Condition ([string]$node.Visible -eq 'true') "Visible for $Id"
    Assert-Condition ([string]$node.BigEndian -eq '0' -and [string]$node.Signed -eq 'true') "int16le flags for $Id"
    Assert-Condition ([string]$node.AltitudeFactor -eq '1' -and [string]$node.AltitudeOffset -eq '0') "altitude for $Id"
    Assert-Condition ([string]$node.Projection -eq 'Quad') "Projection for $Id"
    Assert-XmlCorners $node (Get-GeoExpectedCorners $Local $Geo $Sample) "geo $Id"
}

function Assert-AtlasXml {
    param([string]$Path, [string]$Id, [double[]]$Sample, [double]$AnchorLatitude, [double]$AnchorLongitude)

    $node = Read-HeightmapNode $Path
    Assert-Condition ([string]$node.ID -eq ('WarThunder.Atlas1.' + $Id)) "Atlas ID for $Id"
    Assert-Condition ([string]$node.Visible -eq 'true') "Atlas Visible for $Id"
    Assert-Condition ([string]$node.Projection -eq 'Quad') "Atlas Projection for $Id"
    Assert-XmlCorners $node (Get-AtlasExpectedCorners $Sample $AnchorLatitude $AnchorLongitude) "atlas $Id"
}

function Get-DataSnapshot {
    param([string]$Destination)

    $snapshot = @{}
    foreach ($directory in @(Get-ChildItem -LiteralPath $Destination -Directory -Force)) {
        $dataPath = Join-Path $directory.FullName ($directory.Name + '.data')
        if (-not (Test-Path -LiteralPath $dataPath)) { continue }
        $item = Get-Item -LiteralPath $dataPath
        $snapshot[$directory.Name] = [pscustomobject]@{
            Hash = (Get-FileHash -LiteralPath $dataPath -Algorithm SHA256).Hash
            Length = $item.Length
            LastWriteUtc = $item.LastWriteTimeUtc
        }
    }
    return $snapshot
}

function Assert-DataUnchanged {
    param([hashtable]$Before, [hashtable]$After, [string]$Label)

    Assert-Condition ($Before.Count -eq $After.Count) "$Label data count"
    foreach ($id in $Before.Keys) {
        Assert-Condition ($After.ContainsKey($id)) "$Label data $id missing"
        Assert-Condition ($Before[$id].Hash -eq $After[$id].Hash) "$Label data hash changed for $id"
        Assert-Condition ($Before[$id].Length -eq $After[$id].Length) "$Label data size changed for $id"
        Assert-Condition ($Before[$id].LastWriteUtc -eq $After[$id].LastWriteUtc) "$Label data was rewritten for $id"
    }
}

function Write-GeoFixture {
    param(
        [string]$Root,
        [bool]$AlphaEnabled = $true,
        [bool]$BetaEnabled = $true,
        [double[]]$AlphaLocal = @(-65536.0, -65536.0, 65536.0, 65536.0),
        [double[]]$BetaLocal = @(-32768.0, -32768.0, 32768.0, 32768.0),
        [double[]]$AlphaGeo = @(68.5, 34.2, 70.1, 35.5),
        [double[]]$BetaGeo = @(74.0, 36.0, 75.5, 37.2),
        [double]$AlphaUncertainty = 1.0,
        [double]$BetaUncertainty = 2.0
    )

    $maps = [ordered]@{}
    $maps['geo_alpha'] = New-GeoEntry $AlphaEnabled $AlphaLocal $AlphaGeo $AlphaUncertainty
    $maps['geo_beta'] = New-GeoEntry $BetaEnabled $BetaLocal $BetaGeo $BetaUncertainty
    Write-GeoFile (Join-Path $Root 'GeoReferences.json') $maps
}

function New-FixtureCopy {
    param([string]$Name)

    $destination = Join-Path $testRootFull $Name
    Copy-Item -LiteralPath $fixtureRoot -Destination $destination -Recurse
    return $destination
}

try {
    Assert-Condition (Test-Path -LiteralPath $geoScript) "geographic installer exists"
    Assert-Condition (Test-Path -LiteralPath $mainScript) "main installer exists"
    Assert-Condition (Test-Path -LiteralPath (Join-Path $fixtureRoot 'GeoReferences.json')) "fixture GeoReferences exists"

    $validateDestination = Join-Path $testRootFull 'A\Data\Terrain\Custom\WarThunderMaps'
    $result = Run-Tool $geoScript @('-SourceRoot', $fixtureRoot, '-Destination', $validateDestination, '-ValidateOnly', '-AllowPartial')
    Assert-Condition ($result.ExitCode -eq 0) "standalone ValidateOnly failed: $($result.Output)"
    Assert-Condition ($result.FlatOutput -match 'no files were written') 'standalone ValidateOnly did not report a read-only run'
    Assert-Condition (-not (Test-Path -LiteralPath $validateDestination)) 'standalone ValidateOnly created the destination'

    $destination = Join-Path $testRootFull 'B\Data\Terrain\Custom\WarThunderMaps'
    $result = Run-Tool $mainScript @('-SourceRoot', $fixtureRoot, '-Destination', $destination, '-AllowPartial', '-AtlasOnly')
    Assert-Condition ($result.ExitCode -eq 0) "AtlasOnly baseline failed: $($result.Output)"
    $atlasManifest = Read-Manifest $destination
    Assert-Condition ([string]$atlasManifest.layout -eq 'AtlasV1') 'AtlasOnly layout'
    Assert-Condition ($null -eq $atlasManifest.reference.geo_references) 'AtlasOnly manifest has geo reference'
    $atlasAlphaHash = (Get-FileHash -LiteralPath (Join-Path $destination 'geo_alpha\CustomHeightmapList.xml') -Algorithm SHA256).Hash
    $atlasBetaHash = (Get-FileHash -LiteralPath (Join-Path $destination 'geo_beta\CustomHeightmapList.xml') -Algorithm SHA256).Hash
    $baselineData = Get-DataSnapshot $destination

    $result = Run-Tool $mainScript @('-SourceRoot', $fixtureRoot, '-Destination', $destination, '-AllowPartial')
    Assert-Condition ($result.ExitCode -eq 0) "full geographic install failed: $($result.Output)"
    Assert-Condition ($result.FlatOutput -match 'Applied 2 geographic maps') 'full install did not report two geographic maps'
    $manifest = Read-Manifest $destination
    Assert-Condition ([string]$manifest.layout -eq 'GeographicV1') 'manifest layout is not GeographicV1'
    Assert-Condition ([string]$manifest.reference.geo_references -eq (Join-Path $fixtureRoot 'GeoReferences.json')) 'manifest geo config path'
    $geoHash = (Get-FileHash -LiteralPath (Join-Path $fixtureRoot 'GeoReferences.json') -Algorithm SHA256).Hash.ToLowerInvariant()
    Assert-Condition ([string]$manifest.reference.geo_references_sha256 -eq $geoHash) 'manifest geo config SHA256'
    Assert-Condition ([string]$manifest.reference.geo_commit -eq '1111111111111111111111111111111111111111') 'manifest geo commit'

    $alphaLocal = @(-65536.0, -65536.0, 65536.0, 65536.0)
    $betaLocal = @(-32768.0, -32768.0, 32768.0, 32768.0)
    $alphaSample = @(-65536.0, -65536.0, 65504.0, 65504.0)
    $betaSample = @(-32768.0, -32768.0, 32736.0, 32736.0)
    $alphaGeo = @(68.5, 34.2, 70.1, 35.5)
    $betaGeo = @(74.0, 36.0, 75.5, 37.2)
    Assert-GeoXml (Join-Path $destination 'geo_alpha\CustomHeightmapList.xml') 'geo_alpha' $alphaLocal $alphaGeo $alphaSample
    Assert-GeoXml (Join-Path $destination 'geo_beta\CustomHeightmapList.xml') 'geo_beta' $betaLocal $betaGeo $betaSample

    $alphaEntry = Get-ManifestMap $manifest 'geo_alpha'
    $betaEntry = Get-ManifestMap $manifest 'geo_beta'
    Assert-Condition ([string]$alphaEntry.installed_map_id -eq 'WarThunder.Geo1.geo_alpha') 'alpha installed_map_id'
    Assert-Condition ([string]$alphaEntry.new_map_id -eq 'WarThunder.Geo1.geo_alpha') 'alpha new_map_id'
    $alphaCorners = Get-GeoExpectedCorners $alphaLocal $alphaGeo $alphaSample
    $alphaCenterLat = (@($alphaCorners | ForEach-Object { $_.Latitude }) | Measure-Object -Minimum).Minimum
    $alphaCenterLat = ($alphaCenterLat + (@($alphaCorners | ForEach-Object { $_.Latitude }) | Measure-Object -Maximum).Maximum) / 2.0
    $alphaCenterLon = ((@($alphaCorners | ForEach-Object { $_.Longitude }) | Measure-Object -Minimum).Minimum + (@($alphaCorners | ForEach-Object { $_.Longitude }) | Measure-Object -Maximum).Maximum) / 2.0
    Assert-Condition ([Math]::Abs([double]$alphaEntry.anchor.latitude - $alphaCenterLat) -le 1e-9) 'alpha anchor latitude'
    Assert-Condition ([Math]::Abs([double]$alphaEntry.anchor.longitude - $alphaCenterLon) -le 1e-9) 'alpha anchor longitude'
    Assert-Condition ([string]$alphaEntry.xml_sha256 -eq (Get-FileHash -LiteralPath (Join-Path $destination 'geo_alpha\CustomHeightmapList.xml') -Algorithm SHA256).Hash.ToLowerInvariant()) 'alpha XML hash'
    Assert-Condition ($null -ne $alphaEntry.geographic -and [bool]$alphaEntry.geographic.enabled) 'alpha geographic metadata'
    Assert-Condition ([string]$betaEntry.installed_map_id -eq 'WarThunder.Geo1.geo_beta') 'beta installed_map_id'
    Assert-Condition ($null -ne $betaEntry.geographic -and [bool]$betaEntry.geographic.enabled) 'beta geographic metadata'

    $afterData = Get-DataSnapshot $destination
    Assert-DataUnchanged $baselineData $afterData 'full install'
    $alphaBackup = Join-Path $destination 'geo_alpha\CustomHeightmapList.xml.before-geographic.bak'
    Assert-Condition (Test-Path -LiteralPath $alphaBackup) 'alpha pre-geographic XML backup is missing'
    Assert-Condition ((Read-HeightmapNode $alphaBackup).ID -eq 'WarThunder.Atlas1.geo_alpha') 'alpha backup is not the Atlas XML'
    Assert-Condition ((Get-FileHash -LiteralPath $alphaBackup -Algorithm SHA256).Hash -eq $atlasAlphaHash) 'alpha backup does not match the Atlas baseline'

    $disabledFixture = New-FixtureCopy 'fixture-disabled'
    Write-GeoFixture $disabledFixture $true $false
    $result = Run-Tool $mainScript @('-SourceRoot', $disabledFixture, '-Destination', $destination, '-AllowPartial')
    Assert-Condition ($result.ExitCode -eq 0) "disabled fallback install failed: $($result.Output)"
    Assert-GeoXml (Join-Path $destination 'geo_alpha\CustomHeightmapList.xml') 'geo_alpha' $alphaLocal $alphaGeo $alphaSample
    Assert-AtlasXml (Join-Path $destination 'geo_beta\CustomHeightmapList.xml') 'geo_beta' $betaSample -60.0 -100.0
    Assert-Condition ((Get-FileHash -LiteralPath (Join-Path $destination 'geo_beta\CustomHeightmapList.xml') -Algorithm SHA256).Hash -eq $atlasBetaHash) 'beta fallback XML is not byte-identical to the Atlas baseline'
    $manifest = Read-Manifest $destination
    $betaEntry = Get-ManifestMap $manifest 'geo_beta'
    Assert-Condition ([string]$betaEntry.installed_map_id -eq 'WarThunder.Atlas1.geo_beta') 'beta fallback installed_map_id'
    Assert-Condition ([Math]::Abs([double]$betaEntry.anchor.latitude - -60.0) -le 1e-9) 'beta fallback anchor latitude'
    Assert-Condition ([Math]::Abs([double]$betaEntry.anchor.longitude - -100.0) -le 1e-9) 'beta fallback anchor longitude'
    Assert-Condition ($null -ne $betaEntry.geographic -and -not [bool]$betaEntry.geographic.enabled) 'beta fallback metadata'
    Assert-DataUnchanged $baselineData (Get-DataSnapshot $destination) 'disabled fallback'

    $enableFixture = New-FixtureCopy 'fixture-enable'
    Write-GeoFixture $enableFixture $true $true
    $result = Run-Tool $geoScript @('-SourceRoot', $enableFixture, '-Destination', $destination, '-AllowPartial')
    Assert-Condition ($result.ExitCode -eq 0) "standalone re-enable failed: $($result.Output)"
    Assert-GeoXml (Join-Path $destination 'geo_beta\CustomHeightmapList.xml') 'geo_beta' $betaLocal $betaGeo $betaSample
    $result = Run-Tool $geoScript @('-SourceRoot', $disabledFixture, '-Destination', $destination, '-AllowPartial')
    Assert-Condition ($result.ExitCode -eq 0) "standalone fallback restore failed: $($result.Output)"
    Assert-AtlasXml (Join-Path $destination 'geo_beta\CustomHeightmapList.xml') 'geo_beta' $betaSample -60.0 -100.0
    Assert-Condition (Test-Path -LiteralPath (Join-Path $destination 'geo_beta\CustomHeightmapList.xml.before-geographic-restore.bak')) 'restored beta backup is missing'
    Assert-Condition (Test-Path -LiteralPath (Join-Path $destination 'geo_beta\geo_beta.data')) 'beta data was removed by fallback restore'
    Assert-DataUnchanged $baselineData (Get-DataSnapshot $destination) 'standalone restore'

    $missingManifestDestination = Join-Path $testRootFull 'F\WarThunderMaps'
    New-Item -ItemType Directory -Path $missingManifestDestination -Force | Out-Null
    Set-Content -LiteralPath (Join-Path $missingManifestDestination 'sentinel.txt') -Value 'keep me' -Encoding ASCII
    $result = Run-Tool $geoScript @('-SourceRoot', $fixtureRoot, '-Destination', $missingManifestDestination, '-AllowPartial')
    Assert-Condition ($result.ExitCode -ne 0) 'standalone install without a main manifest succeeded'
    Assert-Condition ($result.FlatOutput -match 'Run Install-All-Terrains') "missing-manifest error is not explicit: $($result.Output)"
    Assert-Condition ((Get-Content -LiteralPath (Join-Path $missingManifestDestination 'sentinel.txt') -Raw).Trim() -eq 'keep me') 'missing-manifest run changed a sentinel'
    $hashDestination = Join-Path $testRootFull 'G\WarThunderMaps'
    Copy-Item -LiteralPath $destination -Destination $hashDestination -Recurse
    $corruptData = Join-Path $hashDestination 'geo_alpha\geo_alpha.data'
    $bytes = [IO.File]::ReadAllBytes($corruptData)
    $bytes[0] = $bytes[0] -bxor 255
    [IO.File]::WriteAllBytes($corruptData, $bytes)
    $alphaXmlHashBefore = (Get-FileHash -LiteralPath (Join-Path $hashDestination 'geo_alpha\CustomHeightmapList.xml') -Algorithm SHA256).Hash
    $manifestHashBefore = (Get-FileHash -LiteralPath (Join-Path $hashDestination 'WarThunderMaps.install.json') -Algorithm SHA256).Hash
    $result = Run-Tool $geoScript @('-SourceRoot', $disabledFixture, '-Destination', $hashDestination, '-AllowPartial')
    Assert-Condition ($result.ExitCode -ne 0) 'corrupted installed data was accepted'
    Assert-Condition ($result.FlatOutput -match 'Installed data SHA256 mismatch') 'corrupted data error is not explicit'
    Assert-Condition ((Get-FileHash -LiteralPath (Join-Path $hashDestination 'geo_alpha\CustomHeightmapList.xml') -Algorithm SHA256).Hash -eq $alphaXmlHashBefore) 'corrupted-data failure changed an XML'
    Assert-Condition ((Get-FileHash -LiteralPath (Join-Path $hashDestination 'WarThunderMaps.install.json') -Algorithm SHA256).Hash -eq $manifestHashBefore) 'corrupted-data failure changed the manifest'

    $badSource = New-FixtureCopy 'fixture-bad-source'
    $badSourceData = Join-Path $badSource 'maps\geo_alpha\geo_alpha.data'
    $sourceBytes = [IO.File]::ReadAllBytes($badSourceData)
    $sourceBytes[0] = $sourceBytes[0] -bxor 255
    [IO.File]::WriteAllBytes($badSourceData, $sourceBytes)
    $result = Run-Tool $geoScript @('-SourceRoot', $badSource, '-Destination', (Join-Path $testRootFull 'G2\WarThunderMaps'), '-ValidateOnly', '-AllowPartial')
    Assert-Condition ($result.ExitCode -ne 0) 'corrupted source data was accepted'
    Assert-Condition ($result.FlatOutput -match 'Data SHA256 mismatch') 'corrupted source error is not explicit'

    $overlapFixture = New-FixtureCopy 'fixture-overlap'
    Write-GeoFixture $overlapFixture $true $true -BetaGeo @(69.0, 34.5, 70.0, 35.0)
    $result = Run-Tool $geoScript @('-SourceRoot', $overlapFixture, '-Destination', (Join-Path $testRootFull 'H1\WarThunderMaps'), '-ValidateOnly', '-AllowPartial')
    Assert-Condition ($result.ExitCode -ne 0) 'overlapping geographic maps were accepted'
    Assert-Condition ($result.FlatOutput -match 'overlap') 'geographic overlap error is not explicit'
    Assert-Condition (-not (Test-Path -LiteralPath (Join-Path $testRootFull 'H1'))) 'geographic overlap wrote a destination'

    $atlasOverlapFixture = New-FixtureCopy 'fixture-atlas-overlap'
    Write-GeoFixture $atlasOverlapFixture $true $true -BetaGeo @(-140.2, -60.2, -139.8, -59.8)
    $result = Run-Tool $geoScript @('-SourceRoot', $atlasOverlapFixture, '-Destination', (Join-Path $testRootFull 'H2\WarThunderMaps'), '-ValidateOnly', '-AllowPartial')
    Assert-Condition ($result.ExitCode -ne 0) 'Atlas overlapping geographic map was accepted'
    Assert-Condition ($result.FlatOutput -match 'Atlas map') 'Atlas overlap error is not explicit'

    $coverageFixture = New-FixtureCopy 'fixture-coverage'
    Write-GeoFixture $coverageFixture $true $true -AlphaLocal @(-60000.0, -60000.0, 60000.0, 60000.0)
    $result = Run-Tool $geoScript @('-SourceRoot', $coverageFixture, '-Destination', (Join-Path $testRootFull 'H3\WarThunderMaps'), '-ValidateOnly', '-AllowPartial')
    Assert-Condition ($result.ExitCode -ne 0) 'texture-sized local_bounds were accepted'
    Assert-Condition ($result.FlatOutput -match 'cover|world square|texture') 'world coverage error is not explicit'

    $latitudeFixture = New-FixtureCopy 'fixture-latitude'
    Write-GeoFixture $latitudeFixture $true $true -BetaGeo @(74.0, 36.0, 75.5, 90.0)
    $result = Run-Tool $geoScript @('-SourceRoot', $latitudeFixture, '-Destination', (Join-Path $testRootFull 'H4\WarThunderMaps'), '-ValidateOnly', '-AllowPartial')
    Assert-Condition ($result.ExitCode -ne 0) 'a pole-crossing latitude was accepted'
    Assert-Condition ($result.FlatOutput -match 'latitude') 'latitude error is not explicit'

    $reversedFixture = New-FixtureCopy 'fixture-reversed'
    Write-GeoFixture $reversedFixture $true $true -AlphaLocal @(65536.0, -65536.0, -65536.0, 65536.0)
    $result = Run-Tool $geoScript @('-SourceRoot', $reversedFixture, '-Destination', (Join-Path $testRootFull 'H5\WarThunderMaps'), '-ValidateOnly', '-AllowPartial')
    Assert-Condition ($result.ExitCode -ne 0) 'reversed local_bounds were accepted'
    Assert-Condition ($result.FlatOutput -match 'positive') 'reversed-bounds error is not explicit'

    $uncertaintyFixture = New-FixtureCopy 'fixture-uncertainty'
    Write-GeoFixture $uncertaintyFixture $true $true -AlphaUncertainty -1.0
    $result = Run-Tool $geoScript @('-SourceRoot', $uncertaintyFixture, '-Destination', (Join-Path $testRootFull 'H6\WarThunderMaps'), '-ValidateOnly', '-AllowPartial')
    Assert-Condition ($result.ExitCode -ne 0) 'negative uncertainty_km was accepted'
    Assert-Condition ($result.FlatOutput -match 'uncertainty') 'uncertainty error is not explicit'

    $mainValidateDestination = Join-Path $testRootFull 'I1\WarThunderMaps'
    $result = Run-Tool $mainScript @('-SourceRoot', $overlapFixture, '-Destination', $mainValidateDestination, '-ValidateOnly', '-AllowPartial')
    Assert-Condition ($result.ExitCode -ne 0) 'main ValidateOnly accepted overlapping geographic maps'
    Assert-Condition ($result.FlatOutput -match 'overlap') 'main ValidateOnly geo error is not explicit'
    Assert-Condition (-not (Test-Path -LiteralPath $mainValidateDestination)) 'main ValidateOnly wrote a destination'

    $result = Run-Tool $mainScript @('-SourceRoot', $overlapFixture, '-Destination', $mainValidateDestination, '-ValidateOnly', '-AllowPartial', '-AtlasOnly')
    Assert-Condition ($result.ExitCode -eq 0) "AtlasOnly ValidateOnly failed on a bad geo config: $($result.Output)"
    Assert-Condition (-not (Test-Path -LiteralPath $mainValidateDestination)) 'AtlasOnly ValidateOnly wrote a destination'

    $goodValidateDestination = Join-Path $testRootFull 'I2\WarThunderMaps'
    $result = Run-Tool $mainScript @('-SourceRoot', $fixtureRoot, '-Destination', $goodValidateDestination, '-ValidateOnly', '-AllowPartial')
    Assert-Condition ($result.ExitCode -eq 0) "main ValidateOnly with a good geo config failed: $($result.Output)"
    Assert-Condition ($result.FlatOutput -match 'Validated 2 geographic maps') 'main ValidateOnly did not run the geographic check'
    Assert-Condition (-not (Test-Path -LiteralPath $goodValidateDestination)) 'main ValidateOnly created a destination'

    $oldFixture = New-FixtureCopy 'fixture-old'
    Remove-Item -LiteralPath (Join-Path $oldFixture 'GeoReferences.json') -Force
    $oldDestination = Join-Path $testRootFull 'J\WarThunderMaps'
    $result = Run-Tool $mainScript @('-SourceRoot', $oldFixture, '-Destination', $oldDestination, '-AllowPartial')
    Assert-Condition ($result.ExitCode -eq 0) "old partial fixture without Geo failed: $($result.Output)"
    Assert-Condition ([string](Read-Manifest $oldDestination).layout -eq 'AtlasV1') 'old partial fixture did not stay AtlasV1'

    $fixture69 = Join-Path $testRootFull 'fixture69'
    New-Item -ItemType Directory -Path $fixture69 | Out-Null
    $positions69 = [ordered]@{}
    for ($row = 0; $row -lt 9; $row++) {
        $columnCount = if ($row -lt 8) { 8 } else { 5 }
        for ($column = 0; $column -lt $columnCount; $column++) {
            $mapId = 'g' + ($row * 8 + $column).ToString('00')
            Write-FixtureMap $fixture69 $mapId @(-65536.0, -65536.0, 65536.0, 65536.0) @(-65536.0, -65536.0, 65504.0, 65504.0) 2 2 | Out-Null
            $positions69[$mapId] = [ordered]@{ latitude = (-60.0 + 15.0 * $row); longitude = (-140.0 + 40.0 * $column) }
        }
    }
    Write-AtlasFile (Join-Path $fixture69 'AtlasReferences.json') $positions69
    $maps69 = [ordered]@{}
    $maps69['g00'] = New-GeoEntry $true @(-65536.0, -65536.0, 65536.0, 65536.0) @(68.5, 34.2, 70.1, 35.5) 1.0
    $maps69['g01'] = New-GeoEntry $true @(-65536.0, -65536.0, 65536.0, 65536.0) @(74.0, 36.0, 75.5, 37.2) 2.0
    Write-GeoFile (Join-Path $fixture69 'GeoReferences.json') $maps69

    $destination69 = Join-Path $testRootFull 'K\WarThunderMaps'
    $result = Run-Tool $mainScript @('-SourceRoot', $fixture69, '-Destination', $destination69)
    Assert-Condition ($result.ExitCode -eq 0) "69-map geographic install failed: $($result.Output)"
    $manifest69 = Read-Manifest $destination69
    Assert-Condition ([int]$manifest69.map_count -eq 69) '69-map manifest map_count'
    Assert-Condition ([string]$manifest69.layout -eq 'GeographicV1') '69-map manifest layout'
    $geoCount = @($manifest69.maps | Where-Object { $_.installed_map_id -like 'WarThunder.Geo1.*' }).Count
    $atlasCount = @($manifest69.maps | Where-Object { $_.installed_map_id -like 'WarThunder.Atlas1.*' }).Count
    Assert-Condition ($geoCount -eq 2) "69-map geo count is $geoCount"
    Assert-Condition ($atlasCount -eq 67) "69-map Atlas fallback count is $atlasCount"
    Assert-GeoXml (Join-Path $destination69 'g00\CustomHeightmapList.xml') 'g00' @(-65536.0, -65536.0, 65536.0, 65536.0) @(68.5, 34.2, 70.1, 35.5) @(-65536.0, -65536.0, 65504.0, 65504.0)
    Assert-AtlasXml (Join-Path $destination69 'g02\CustomHeightmapList.xml') 'g02' @(-65536.0, -65536.0, 65504.0, 65504.0) -60.0 -60.0
    foreach ($mapDirectory in @(Get-ChildItem -LiteralPath $destination69 -Directory -Force)) {
        $sourceMetadata = Get-Content -LiteralPath (Join-Path (Join-Path $fixture69 'maps') (Join-Path $mapDirectory.Name 'metadata.json')) -Raw -Encoding UTF8 | ConvertFrom-Json
        $installedData = Join-Path $mapDirectory.FullName ($mapDirectory.Name + '.data')
        Assert-Condition ((Get-FileHash -LiteralPath $installedData -Algorithm SHA256).Hash.ToLowerInvariant() -eq [string]$sourceMetadata.output.data_sha256) "69-map data hash changed for $($mapDirectory.Name)"
    }

    $fixture69NoGeo = Join-Path $testRootFull 'fixture69-nogeo'
    Copy-Item -LiteralPath $fixture69 -Destination $fixture69NoGeo -Recurse
    Remove-Item -LiteralPath (Join-Path $fixture69NoGeo 'GeoReferences.json') -Force
    $destination69NoGeo = Join-Path $testRootFull 'K2\WarThunderMaps'
    $result = Run-Tool $mainScript @('-SourceRoot', $fixture69NoGeo, '-Destination', $destination69NoGeo)
    Assert-Condition ($result.ExitCode -ne 0) 'non-partial install without GeoReferences succeeded'
    Assert-Condition ($result.FlatOutput -match 'Geographic terrain configuration is required') 'missing GeoReferences error is not explicit'
    Assert-Condition (-not (Test-Path -LiteralPath $destination69NoGeo)) 'missing GeoReferences failure wrote the destination'

    $wrapper = Join-Path $testRootFull 'from-installer-wrapper.ps1'
    $wrapperText = @'
$ErrorActionPreference = 'Stop'
try {
    & '__GEO_SCRIPT__' -SourceRoot '__FIXTURE__' -Destination '__DESTINATION__' -AllowPartial -FromInstaller
}
catch {
    exit 42
}
exit 0
'@
    $wrapperText = $wrapperText.Replace('__GEO_SCRIPT__', $geoScript).Replace('__FIXTURE__', $overlapFixture).Replace('__DESTINATION__', (Join-Path $testRootFull 'L\WarThunderMaps'))
    [IO.File]::WriteAllText($wrapper, $wrapperText, (New-Object Text.UTF8Encoding($false)))
    $result = Run-Tool $wrapper @()
    Assert-Condition ($result.ExitCode -eq 42) "FromInstaller failure changed the caller exit code to $($result.ExitCode)"

    Write-Host 'PASS: geographic corners, ID/Visible/Quad, Atlas fallback, manifest layout/geo hash/commit/per-map IDs and anchors/XML hashes, data reuse, stale fallback restore, source and installed hash rejection, overlap rejection (geo and Atlas), world-bound coverage rejection, invalid latitude/bounds/uncertainty rejection, missing main manifest, ValidateOnly no-write, AtlasOnly bypass, old partial fixture compatibility, 69-map non-partial install, missing GeoReferences rejection before writes, and FromInstaller exit-code propagation.'
}
finally {
    if (Test-Path -LiteralPath $testRootFull) {
        for ($attempt = 0; $attempt -lt 3; $attempt++) {
            try {
                Remove-Item -LiteralPath $testRootFull -Recurse -Force -ErrorAction Stop
                break
            }
            catch {
                Start-Sleep -Milliseconds 250
            }
        }
    }
}
