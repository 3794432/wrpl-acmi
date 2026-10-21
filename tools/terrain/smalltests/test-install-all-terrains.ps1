$ErrorActionPreference = 'Stop'

$testRoot = Join-Path $PSScriptRoot 'runtime'
$testRootFull = [IO.Path]::GetFullPath($testRoot)
$testPrefix = [IO.Path]::GetFullPath($PSScriptRoot) + [IO.Path]::DirectorySeparatorChar
if (-not $testRootFull.StartsWith($testPrefix, [StringComparison]::OrdinalIgnoreCase)) {
    throw "Refusing test root outside smalltests: $testRootFull"
}
if (Test-Path -LiteralPath $testRootFull) { Remove-Item -LiteralPath $testRootFull -Recurse -Force }
New-Item -ItemType Directory -Path $testRootFull | Out-Null

$installer = Join-Path $PSScriptRoot '..\Install-All-Terrains.ps1'
$installerCmd = Join-Path $PSScriptRoot '..\Install-All-Terrains.cmd'
$invariant = [Globalization.CultureInfo]::InvariantCulture

function Format-Number {
    param([double]$Value)
    return $Value.ToString('F12', $invariant)
}

function Add-XmlText {
    param([System.Xml.XmlDocument]$Document, [System.Xml.XmlNode]$Parent, [string]$Name, [string]$Value)

    $element = $Document.CreateElement($Name)
    $element.InnerText = $Value
    [void]$Parent.AppendChild($element)
}

function Write-FixtureMap {
    param(
        [string]$Root,
        [string]$Id,
        [double[]]$Bounds,
        [int]$Width = 2,
        [int]$Height = 2
    )

    $mapRoot = Join-Path $Root 'maps'
    $mapDir = Join-Path $mapRoot $Id
    New-Item -ItemType Directory -Path $mapDir -Force | Out-Null

    $data = New-Object byte[] ($Width * $Height * 2)
    for ($index = 0; $index -lt $data.Length; $index++) {
        $data[$index] = [byte](($index * 37 + $Id.Length * 11 + 17) % 251)
    }
    $dataPath = Join-Path $mapDir ($Id + '.data')
    [IO.File]::WriteAllBytes($dataPath, $data)
    $dataHash = (Get-FileHash -LiteralPath $dataPath -Algorithm SHA256).Hash.ToLowerInvariant()

    $metadata = [ordered]@{
        schema_version = 1
        id = $Id
        status = 'ok'
        output = [ordered]@{
            sample_bounds_m = @([double]$Bounds[0], [double]$Bounds[1], [double]$Bounds[2], [double]$Bounds[3])
            id = $Id
            width = $Width
            height = $Height
            data_file = ($Id + '.data')
            data_bytes = [long]$data.Length
            data_sha256 = $dataHash
            altitude_factor = 1
            altitude_offset = 0
        }
    }
    $metadataPath = Join-Path $mapDir 'metadata.json'
    [IO.File]::WriteAllText($metadataPath, ($metadata | ConvertTo-Json -Depth 8), (New-Object Text.UTF8Encoding($false)))

    $document = New-Object System.Xml.XmlDocument
    [void]$document.AppendChild($document.CreateXmlDeclaration('1.0', 'utf-8', $null))
    $resources = $document.CreateElement('Resources')
    [void]$document.AppendChild($resources)
    $list = $document.CreateElement('CustomHeightmapList')
    [void]$resources.AppendChild($list)
    $node = $document.CreateElement('CustomHeightmap')
    $node.SetAttribute('Layer', 'Real World')
    $node.SetAttribute('ID', ('WarThunder.' + $Id))
    [void]$list.AppendChild($node)
    Add-XmlText $document $node 'File' ($Id + '.data')
    Add-XmlText $document $node 'Visible' 'true'
    Add-XmlText $document $node 'BigEndian' '0'
    Add-XmlText $document $node 'Signed' 'true'
    Add-XmlText $document $node 'Width' ([string]$Width)
    Add-XmlText $document $node 'Height' ([string]$Height)
    Add-XmlText $document $node 'AltitudeFactor' '1'
    Add-XmlText $document $node 'AltitudeOffset' '0'
    Add-XmlText $document $node 'Projection' 'Quad'
    $definitions = @(
        @('BottomLeft', $Bounds[0], $Bounds[1]),
        @('BottomRight', $Bounds[2], $Bounds[1]),
        @('TopRight', $Bounds[2], $Bounds[3]),
        @('TopLeft', $Bounds[0], $Bounds[3])
    )
    foreach ($corner in $definitions) {
        $latitude = [double]$corner[2] / 111320.0
        $longitude = [double]$corner[1] / (111320.0 * [Math]::Cos($latitude * [Math]::PI / 180.0))
        $cornerNode = $document.CreateElement([string]$corner[0])
        Add-XmlText $document $cornerNode 'Longitude' (Format-Number $longitude)
        Add-XmlText $document $cornerNode 'Latitude' (Format-Number $latitude)
        [void]$node.AppendChild($cornerNode)
    }
    $xmlPath = Join-Path $mapDir 'CustomHeightmapList.xml'
    $document.Save($xmlPath)

    return [pscustomobject]@{
        Id = $Id
        Folder = $mapDir
        DataPath = $dataPath
        DataHash = $dataHash
        Metadata = $metadata
        Bounds = $Bounds
    }
}

function Write-AtlasFile {
    param([string]$Path, [object]$Positions)

    $maps = New-Object System.Collections.Specialized.OrderedDictionary
    foreach ($key in $Positions.Keys) { $maps[$key] = $Positions[$key] }
    $document = [ordered]@{
        schema_version = 1
        layout = 'AtlasV1'
        columns = 8
        rows = 9
        longitude_step = 40
        latitude_step = 15
        maps = $maps
    }
    [IO.File]::WriteAllText($Path, ($document | ConvertTo-Json -Depth 8), (New-Object Text.UTF8Encoding($false)))
}

function Run-InstallerProcess {
    param([string[]]$Arguments)

    $oldEap = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $text = & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $installer @Arguments 2>&1 | Out-String
        $exitCode = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $oldEap
    }
    return [pscustomobject]@{ ExitCode = $exitCode; Output = $text }
}

function Assert-XmlReference {
    param(
        [string]$Path,
        [object]$Metadata,
        [string]$ExpectedId,
        [string]$ExpectedVisible,
        [double]$AnchorLatitude,
        [double]$AnchorLongitude
    )

    [xml]$xml = Get-Content -LiteralPath $Path -Raw -Encoding UTF8
    $node = $xml.Resources.CustomHeightmapList.CustomHeightmap
    if ([string]$node.ID -ne $ExpectedId) { throw "Expected XML ID $ExpectedId, got $($node.ID)" }
    if ([string]$node.Visible -ne $ExpectedVisible) { throw "Expected Visible=$ExpectedVisible for $ExpectedId" }
    $bounds = @($Metadata.output.sample_bounds_m | ForEach-Object { [double]$_ })
    $definitions = @(
        @('BottomLeft', $bounds[0], $bounds[1]),
        @('BottomRight', $bounds[2], $bounds[1]),
        @('TopRight', $bounds[2], $bounds[3]),
        @('TopLeft', $bounds[0], $bounds[3])
    )
    foreach ($corner in $definitions) {
        $expectedLatitude = $AnchorLatitude + ([double]$corner[2] / 111320.0)
        $expectedLongitude = $AnchorLongitude + ([double]$corner[1] / (111320.0 * [Math]::Cos($expectedLatitude * [Math]::PI / 180.0)))
        $element = $node.SelectSingleNode([string]$corner[0])
        if ($null -eq $element) { throw "Missing $($corner[0]) in $Path" }
        $actualLatitude = [double]::Parse([string]$element.Latitude, [Globalization.NumberStyles]::Float, $invariant)
        $actualLongitude = [double]::Parse([string]$element.Longitude, [Globalization.NumberStyles]::Float, $invariant)
        if ([Math]::Abs($actualLatitude - $expectedLatitude) -gt 1e-9 -or [Math]::Abs($actualLongitude - $expectedLongitude) -gt 1e-9) {
            throw "Coordinate mismatch for $($corner[0]) in $Path"
        }
    }
}

function Assert-NoOldCoordinateManifestFields {
    param([object]$Manifest, [string]$Label)

    $forbidden = @('latitude', 'longitude', 'center_x', 'center_z', 'legacy_double_reference')
    foreach ($name in $forbidden) {
        if ($null -ne $Manifest.reference.PSObject.Properties[$name]) {
            throw "$Label manifest still contains reference.$name"
        }
    }
}

function Get-GeoExpectedCorners {
    param([object]$Entry, [double[]]$Sample)

    $local = @($Entry.local_bounds | ForEach-Object { [double]$_ })
    $geo = @($Entry.geo_bounds | ForEach-Object { [double]$_ })
    $definitions = @(
        @('BottomLeft', $Sample[0], $Sample[1]),
        @('BottomRight', $Sample[2], $Sample[1]),
        @('TopRight', $Sample[2], $Sample[3]),
        @('TopLeft', $Sample[0], $Sample[3])
    )
    $result = @()
    foreach ($corner in $definitions) {
        $result += [pscustomobject]@{
            Name = [string]$corner[0]
            Longitude = $geo[0] + (([double]$corner[1] - $local[0]) / ($local[2] - $local[0])) * ($geo[2] - $geo[0])
            Latitude = $geo[1] + (([double]$corner[2] - $local[1]) / ($local[3] - $local[1])) * ($geo[3] - $geo[1])
        }
    }
    return $result
}

function Assert-GeoXmlReference {
    param([string]$Path, [string]$Id, [object]$Entry, [double[]]$Sample)

    [xml]$xml = Get-Content -LiteralPath $Path -Raw -Encoding UTF8
    $node = $xml.Resources.CustomHeightmapList.CustomHeightmap
    if ([string]$node.ID -ne ('WarThunder.Geo1.' + $Id)) { throw "Expected geographic ID for $Id, got $($node.ID)" }
    if ([string]$node.Visible -ne 'true') { throw "Expected geographic Visible=true for $Id" }
    foreach ($corner in (Get-GeoExpectedCorners $Entry $Sample)) {
        $element = $node.SelectSingleNode($corner.Name)
        if ($null -eq $element) { throw "Missing $($corner.Name) for $Id" }
        $actualLatitude = [double]::Parse([string]$element.Latitude, [Globalization.NumberStyles]::Float, $invariant)
        $actualLongitude = [double]::Parse([string]$element.Longitude, [Globalization.NumberStyles]::Float, $invariant)
        if ([Math]::Abs($actualLatitude - $corner.Latitude) -gt 1e-9 -or [Math]::Abs($actualLongitude - $corner.Longitude) -gt 1e-9) {
            throw "Geographic coordinate mismatch for $Id $($corner.Name)"
        }
    }
}

function Assert-ManifestMap {
    param([object]$Manifest, [string]$Id, [double]$Latitude, [double]$Longitude)

    $entry = @($Manifest.maps | Where-Object { $_.id -eq $Id })
    if ($entry.Count -ne 1) { throw "Manifest entry missing or duplicated for $Id" }
    if ([string]$entry[0].new_map_id -ne ('WarThunder.Atlas1.' + $Id)) { throw "Manifest new_map_id mismatch for $Id" }
    if ([string]$entry[0].installed_map_id -ne ('WarThunder.Atlas1.' + $Id)) { throw "Manifest installed_map_id mismatch for $Id" }
    if ([string]$entry[0].map_id -ne ('WarThunder.' + $Id)) { throw "Manifest source map_id mismatch for $Id" }
    if ([Math]::Abs([double]$entry[0].anchor.latitude - $Latitude) -gt 1e-9) { throw "Manifest nested anchor latitude mismatch for $Id" }
    if ([Math]::Abs([double]$entry[0].anchor.longitude - $Longitude) -gt 1e-9) { throw "Manifest nested anchor longitude mismatch for $Id" }
    if ([Math]::Abs([double]$entry[0].anchor_latitude - $Latitude) -gt 1e-9) { throw "Manifest anchor latitude mismatch for $Id" }
    if ([Math]::Abs([double]$entry[0].anchor_longitude - $Longitude) -gt 1e-9) { throw "Manifest anchor longitude mismatch for $Id" }
}

try {
    $fixtureRoot = Join-Path $testRootFull 'fixture'
    $fixtureMaps = Join-Path $fixtureRoot 'maps'
    $firstId = 'air_afghan'
    $secondId = 'air_africa_desert'
    $firstBounds = @(-30000.0, -20000.0, 30000.0, 20000.0)
    $secondBounds = @(-25000.0, -15000.0, 25000.0, 15000.0)
    $first = Write-FixtureMap $fixtureRoot $firstId $firstBounds
    $second = Write-FixtureMap $fixtureRoot $secondId $secondBounds

    $atlasPath = Join-Path $fixtureRoot 'AtlasReferences.json'
    $positions = New-Object System.Collections.Specialized.OrderedDictionary
    $positions[$firstId] = @(-60.0, -140.0)
    $positions[$secondId] = @(-60.0, -100.0)
    Write-AtlasFile $atlasPath $positions

    $activeOverlay = Join-Path $testRootFull 'Data\Terrain\Custom\WarThunderActive'
    New-Item -ItemType Directory -Path $activeOverlay -Force | Out-Null
    Copy-Item -LiteralPath $first.DataPath -Destination (Join-Path $activeOverlay 'active.data')
    @{ map = $firstId; sha256 = $first.DataHash } |
        ConvertTo-Json | Set-Content -LiteralPath (Join-Path $activeOverlay 'active.json') -Encoding UTF8
    [xml]$activeXml = Get-Content -LiteralPath (Join-Path $first.Folder 'CustomHeightmapList.xml') -Raw -Encoding UTF8
    $activeNode = $activeXml.Resources.CustomHeightmapList.CustomHeightmap
    $activeNode.File = 'active.data'
    $activeNode.Visible = 'true'
    $activeXml.Save((Join-Path $activeOverlay 'CustomHeightmapList.xml'))

    $removedParameters = @(
        @{ Name = 'LegacyCoordinates'; Arguments = @('-LegacyCoordinates') },
        @{ Name = 'LegacyDoubleReference'; Arguments = @('-LegacyDoubleReference') },
        @{ Name = 'Latitude'; Arguments = @('-Latitude', '10') },
        @{ Name = 'Longitude'; Arguments = @('-Longitude', '10') },
        @{ Name = 'CenterX'; Arguments = @('-CenterX', '10') },
        @{ Name = 'CenterZ'; Arguments = @('-CenterZ', '10') }
    )
    foreach ($removed in $removedParameters) {
        $rejectedDestination = Join-Path $testRootFull ('rejected-' + $removed.Name)
        $arguments = @('-SourceRoot', $fixtureRoot, '-Destination', $rejectedDestination, '-AllowPartial') + @($removed.Arguments)
        $result = Run-InstallerProcess $arguments
        if ($result.ExitCode -eq 0) { throw "Removed parameter $($removed.Name) was accepted." }
        if ($result.Output -notmatch [regex]::Escape([string]$removed.Name)) { throw "Removed parameter $($removed.Name) did not fail explicitly:`n$($result.Output)" }
        if (Test-Path -LiteralPath $rejectedDestination) { throw "Rejected $($removed.Name) run created a destination." }
    }

    $missingAtlasDestination = Join-Path $testRootFull 'missing-atlas-destination'
    $result = Run-InstallerProcess @('-SourceRoot', $fixtureRoot, '-Destination', $missingAtlasDestination, '-AllowPartial', '-AtlasReferences', (Join-Path $testRootFull 'missing.json'))
    if ($result.ExitCode -eq 0 -or $result.Output -notmatch 'Atlas references file is missing') { throw "Missing atlas reference was not rejected:`n$($result.Output)" }
    if (Test-Path -LiteralPath $missingAtlasDestination) { throw 'Missing-atlas run created a destination.' }

    $destination = Join-Path $testRootFull 'Data\Terrain\Custom\WarThunderMaps'
    New-Item -ItemType Directory -Path $destination -Force | Out-Null
    Set-Content -LiteralPath (Join-Path $destination 'keep-me.txt') -Value 'root sentinel' -Encoding ASCII
    foreach ($fixture in @($first, $second)) {
        New-Item -ItemType Directory -Path (Join-Path $destination $fixture.Id) -Force | Out-Null
        Set-Content -LiteralPath (Join-Path (Join-Path $destination $fixture.Id) 'keep-me-too.txt') -Value 'map sentinel' -Encoding ASCII
    }

    $result = Run-InstallerProcess @('-SourceRoot', $fixtureRoot, '-Destination', $destination, '-AllowPartial')
    if ($result.ExitCode -ne 0) { throw "Default Atlas install failed:`n$($result.Output)" }
    if ($result.Output -notmatch 'Layout: AtlasV1') { throw 'Default install did not report its Atlas layout.' }
    foreach ($fixture in @($first, $second)) {
        $installedXml = Join-Path (Join-Path $destination $fixture.Id) 'CustomHeightmapList.xml'
        $anchor = $positions[$fixture.Id]
        Assert-XmlReference $installedXml $fixture.Metadata ('WarThunder.Atlas1.' + $fixture.Id) 'true' ([double]$anchor[0]) ([double]$anchor[1])
        if (-not (Get-Content -LiteralPath (Join-Path (Join-Path $destination $fixture.Id) 'keep-me-too.txt') -Raw).Contains('map sentinel')) { throw 'Unknown map file was not preserved.' }
    }
    if (-not (Get-Content -LiteralPath (Join-Path $destination 'keep-me.txt') -Raw).Contains('root sentinel')) { throw 'Unknown root file was not preserved.' }

    $manifestPath = Join-Path $destination 'WarThunderMaps.install.json'
    $manifest = Get-Content -LiteralPath $manifestPath -Raw -Encoding UTF8 | ConvertFrom-Json
    if ([string]$manifest.layout -ne 'AtlasV1') { throw 'Atlas manifest layout mismatch.' }
    if ([int]$manifest.map_count -ne 2) { throw 'Atlas manifest map_count mismatch.' }
    if ([string]$manifest.reference.layout -ne 'AtlasV1') { throw 'Atlas manifest reference layout mismatch.' }
    if ([string]$manifest.reference.atlas_references -ne $atlasPath) { throw 'Atlas manifest reference path mismatch.' }
    if ([string]$manifest.reference.atlas_references_sha256 -ne (Get-FileHash -LiteralPath $atlasPath -Algorithm SHA256).Hash.ToLowerInvariant()) { throw 'Atlas manifest reference hash mismatch.' }
    Assert-NoOldCoordinateManifestFields $manifest 'Atlas'
    Assert-ManifestMap $manifest $firstId -60 -140
    Assert-ManifestMap $manifest $secondId -60 -100

    if (Test-Path -LiteralPath (Join-Path $activeOverlay 'CustomHeightmapList.xml')) { throw 'WarThunderActive XML was not disabled.' }
    if (-not (Test-Path -LiteralPath (Join-Path $activeOverlay 'CustomHeightmapList.xml.disabled-warthundermaps'))) { throw 'Disabled WarThunderActive XML is missing.' }
    if (-not (Test-Path -LiteralPath (Join-Path $activeOverlay 'active.data'))) { throw 'WarThunderActive active.data was removed.' }
    if (-not (Test-Path -LiteralPath (Join-Path $activeOverlay 'active.json.warthundermaps-backup'))) { throw 'WarThunderActive active.json backup is missing.' }

    $dataTimestamps = @{}
    $dataHashes = @{}
    foreach ($fixture in @($first, $second)) {
        $installedData = Join-Path (Join-Path $destination $fixture.Id) ($fixture.Id + '.data')
        $dataTimestamps[$fixture.Id] = (Get-Item -LiteralPath $installedData).LastWriteTimeUtc
        $dataHashes[$fixture.Id] = (Get-FileHash -LiteralPath $installedData -Algorithm SHA256).Hash
    }

    $staleSource = Join-Path $first.Folder 'CustomHeightmapList.xml'
    $staleTarget = Join-Path (Join-Path $destination $firstId) 'CustomHeightmapList.xml'
    Copy-Item -LiteralPath $staleSource -Destination $staleTarget -Force
    $staleHash = (Get-FileHash -LiteralPath $staleTarget -Algorithm SHA256).Hash
    $result = Run-InstallerProcess @('-SourceRoot', $fixtureRoot, '-Destination', $destination, '-AllowPartial')
    if ($result.ExitCode -ne 0) { throw "Atlas regeneration failed:`n$($result.Output)" }
    if ($result.Output -notmatch 'reused by matching size/SHA256: 2') { throw 'Atlas regeneration did not reuse both data files.' }
    $xmlBackup = $staleTarget + '.before-atlas.bak'
    if (-not (Test-Path -LiteralPath $xmlBackup)) { throw 'Atlas regeneration did not create an XML backup.' }
    if ((Get-FileHash -LiteralPath $xmlBackup -Algorithm SHA256).Hash -ne $staleHash) { throw 'XML backup does not contain the previous source-ID XML.' }
    Assert-XmlReference $staleTarget $first.Metadata ('WarThunder.Atlas1.' + $firstId) 'true' -60 -140
    foreach ($fixture in @($first, $second)) {
        $installedData = Join-Path (Join-Path $destination $fixture.Id) ($fixture.Id + '.data')
        if ((Get-Item -LiteralPath $installedData).LastWriteTimeUtc -ne $dataTimestamps[$fixture.Id]) { throw "Data file was rewritten for $($fixture.Id)." }
        if ((Get-FileHash -LiteralPath $installedData -Algorithm SHA256).Hash -ne $dataHashes[$fixture.Id]) { throw "Data hash changed for $($fixture.Id)." }
    }
    $manifestBackupPath = $manifestPath + '.bak'
    if (-not (Test-Path -LiteralPath $manifestBackupPath)) { throw 'Atlas regeneration did not create a manifest backup.' }
    $backupManifest = Get-Content -LiteralPath $manifestBackupPath -Raw -Encoding UTF8 | ConvertFrom-Json
    if ([string]$backupManifest.layout -ne 'AtlasV1') { throw 'Manifest backup does not contain the previous Atlas manifest.' }
    Assert-NoOldCoordinateManifestFields $backupManifest 'Backup'
    $xmlBackupHash = (Get-FileHash -LiteralPath $xmlBackup -Algorithm SHA256).Hash
    $manifestBackupHash = (Get-FileHash -LiteralPath $manifestBackupPath -Algorithm SHA256).Hash

    $result = Run-InstallerProcess @('-SourceRoot', $fixtureRoot, '-Destination', $destination, '-AllowPartial')
    if ($result.ExitCode -ne 0) { throw "Atlas rerun failed:`n$($result.Output)" }
    if ($result.Output -notmatch 'reused by matching size/SHA256: 2') { throw 'Atlas rerun did not reuse both data files.' }
    if ((Get-FileHash -LiteralPath $xmlBackup -Algorithm SHA256).Hash -ne $xmlBackupHash) { throw 'Atlas rerun replaced the original XML backup.' }
    if ((Get-FileHash -LiteralPath $manifestBackupPath -Algorithm SHA256).Hash -ne $manifestBackupHash) { throw 'Atlas rerun replaced the original manifest backup.' }

    $validateDestination = Join-Path $testRootFull 'validate-destination'
    $result = Run-InstallerProcess @('-SourceRoot', $fixtureRoot, '-Destination', $validateDestination, '-ValidateOnly', '-AllowPartial')
    if ($result.ExitCode -ne 0) { throw "ValidateOnly failed:`n$($result.Output)" }
    if ($result.Output -notmatch 'no files were written') { throw 'ValidateOnly did not report a read-only run.' }
    if (Test-Path -LiteralPath $validateDestination) { throw 'ValidateOnly created the destination.' }

    $unknownRoot = Join-Path $testRootFull 'unknown\Data\Terrain\Custom'
    $unknownOverlay = Join-Path $unknownRoot 'WarThunderActive'
    New-Item -ItemType Directory -Path $unknownOverlay -Force | Out-Null
    $unknownXml = Join-Path $unknownOverlay 'CustomHeightmapList.xml'
    $unknownText = '<Resources><CustomHeightmapList><CustomHeightmap Layer="Real World" ID="Other.Map"><File>other.data</File><Visible>true</Visible></CustomHeightmap></CustomHeightmapList></Resources>'
    Set-Content -LiteralPath $unknownXml -Value $unknownText -Encoding UTF8
    Set-Content -LiteralPath (Join-Path $unknownOverlay 'unknown.txt') -Value 'unknown sentinel' -Encoding ASCII
    $unknownDestination = Join-Path $unknownRoot 'WarThunderMaps'
    $result = Run-InstallerProcess @('-SourceRoot', $fixtureRoot, '-Destination', $unknownDestination, '-AllowPartial')
    if ($result.ExitCode -ne 0) { throw "Unknown overlay test failed:`n$($result.Output)" }
    if ($result.Output -notmatch 'could not be proven') { throw 'Unknown WarThunderActive did not produce the explicit warning.' }
    if ([IO.File]::ReadAllText($unknownXml).Trim() -cne $unknownText) { throw 'Unknown WarThunderActive XML was modified.' }
    if ((Get-Content -LiteralPath (Join-Path $unknownOverlay 'unknown.txt') -Raw).Trim() -ne 'unknown sentinel') { throw 'Unknown WarThunderActive file was modified.' }
    if (-not (Test-Path -LiteralPath (Join-Path $unknownDestination ($firstId + '\CustomHeightmapList.xml')))) { throw 'Install did not proceed past the unknown overlay warning.' }

    $badAtlasPath = Join-Path $testRootFull 'bad-atlas.json'
    $badPositions = New-Object System.Collections.Specialized.OrderedDictionary
    $badPositions[$firstId] = @(-60.0, -140.0)
    $badPositions[$secondId] = @(-60.0, -140.0)
    Write-AtlasFile $badAtlasPath $badPositions
    $badDestination = Join-Path $testRootFull 'bad-destination'
    New-Item -ItemType Directory -Path $badDestination -Force | Out-Null
    Set-Content -LiteralPath (Join-Path $badDestination 'sentinel.txt') -Value 'bad sentinel' -Encoding ASCII
    $result = Run-InstallerProcess @('-SourceRoot', $fixtureRoot, '-Destination', $badDestination, '-AllowPartial', '-AtlasReferences', $badAtlasPath)
    if ($result.ExitCode -eq 0) { throw 'Overlapping atlas anchors were accepted.' }
    if ($result.Output -notmatch 'overlap') { throw "Overlap failure did not identify the geometry conflict:`n$($result.Output)" }
    if (Test-Path -LiteralPath (Join-Path $badDestination $firstId)) { throw 'Overlap failure wrote a map directory.' }
    if (Test-Path -LiteralPath (Join-Path $badDestination 'WarThunderMaps.install.json')) { throw 'Overlap failure wrote a manifest.' }
    if ((Get-Content -LiteralPath (Join-Path $badDestination 'sentinel.txt') -Raw).Trim() -ne 'bad sentinel') { throw 'Overlap failure changed the destination sentinel.' }

    $bigRoot = Join-Path $testRootFull 'big-fixture'
    $bigMap = Write-FixtureMap $bigRoot $firstId @(-1400000.0, -500000.0, 1400000.0, 500000.0)
    $bigAtlasPath = Join-Path $bigRoot 'AtlasReferences.json'
    $bigPositions = New-Object System.Collections.Specialized.OrderedDictionary
    $bigPositions[$firstId] = @(-60.0, -140.0)
    Write-AtlasFile $bigAtlasPath $bigPositions
    $bigDestination = Join-Path $testRootFull 'big-destination'
    New-Item -ItemType Directory -Path $bigDestination -Force | Out-Null
    Set-Content -LiteralPath (Join-Path $bigDestination 'sentinel.txt') -Value 'big sentinel' -Encoding ASCII
    $result = Run-InstallerProcess @('-SourceRoot', $bigRoot, '-Destination', $bigDestination, '-AllowPartial')
    if ($result.ExitCode -eq 0) { throw 'A map wider than its atlas slot was accepted.' }
    if ($result.Output -notmatch 'span' -or $result.Output -notmatch 'slot') { throw "Slot-span failure was not explicit:`n$($result.Output)" }
    if (Test-Path -LiteralPath (Join-Path $bigDestination $firstId)) { throw 'Slot-span failure wrote a map directory.' }
    if ((Get-Content -LiteralPath (Join-Path $bigDestination 'sentinel.txt') -Raw).Trim() -ne 'big sentinel') { throw 'Slot-span failure changed the destination sentinel.' }

    $output = & $installerCmd -SourceRoot $fixtureRoot -ValidateOnly -AllowPartial 2>&1 | Out-String
    if ($LASTEXITCODE -ne 0) { throw ".cmd success path returned $LASTEXITCODE`n$output" }
    $oldEap = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $output = & $installerCmd -SourceRoot (Join-Path $testRootFull 'missing') -ValidateOnly 2>&1 | Out-String
    }
    finally { $ErrorActionPreference = $oldEap }
    if ($LASTEXITCODE -eq 0) { throw '.cmd failure path returned zero.' }

    $packageRoot = Join-Path $testRootFull 'package'
    New-Item -ItemType Directory -Path $packageRoot -Force | Out-Null
    Copy-Item -LiteralPath $installer -Destination (Join-Path $packageRoot 'Install-All-Terrains.ps1')
    Copy-Item -LiteralPath $installerCmd -Destination (Join-Path $packageRoot 'Install-All-Terrains.cmd')
    Copy-Item -LiteralPath $fixtureMaps -Destination (Join-Path $packageRoot 'maps') -Recurse
    Copy-Item -LiteralPath $atlasPath -Destination (Join-Path $packageRoot 'AtlasReferences.json')
    $packageDestination = Join-Path $packageRoot 'out\WarThunderMaps'
    $output = & powershell.exe -NoProfile -ExecutionPolicy Bypass -File (Join-Path $packageRoot 'Install-All-Terrains.ps1') -Destination $packageDestination -AllowPartial 2>&1 | Out-String
    if ($LASTEXITCODE -ne 0) { throw "Package-root default SourceRoot failed:`n$output" }
    if (-not (Test-Path -LiteralPath (Join-Path $packageDestination ($firstId + '\CustomHeightmapList.xml')))) { throw 'Package-root default install did not create the first map.' }
    if (-not (Test-Path -LiteralPath (Join-Path $packageDestination ($secondId + '\CustomHeightmapList.xml')))) { throw 'Package-root default install did not create the second map.' }

    $terrainRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
    $defaultFixture = Join-Path $testRootFull 'default-69-fixture'
    New-Item -ItemType Directory -Path $defaultFixture -Force | Out-Null
    Copy-Item -LiteralPath (Join-Path $terrainRoot 'AtlasReferences.json') -Destination (Join-Path $defaultFixture 'AtlasReferences.json')
    Copy-Item -LiteralPath (Join-Path $terrainRoot 'GeoReferences.json') -Destination (Join-Path $defaultFixture 'GeoReferences.json')
    $defaultAtlas = Get-Content -LiteralPath (Join-Path $defaultFixture 'AtlasReferences.json') -Raw -Encoding UTF8 | ConvertFrom-Json
    $defaultGeo = Get-Content -LiteralPath (Join-Path $defaultFixture 'GeoReferences.json') -Raw -Encoding UTF8 | ConvertFrom-Json
    $defaultSamples = @{}
    foreach ($property in @($defaultAtlas.PSObject.Properties | Sort-Object Name)) {
        $id = [string]$property.Name
        $geoEntry = $defaultGeo.maps.PSObject.Properties[$id].Value
        if ($null -ne $geoEntry -and [bool]$geoEntry.enabled) {
            $sample = @([double]$geoEntry.local_bounds[0], [double]$geoEntry.local_bounds[1], [double]$geoEntry.local_bounds[2], [double]$geoEntry.local_bounds[3])
        }
        else {
            $sample = @(-32768.0, -32768.0, 32768.0, 32768.0)
        }
        $defaultSamples[$id] = $sample
        Write-FixtureMap $defaultFixture $id $sample 2 2 | Out-Null
    }
    if ($defaultSamples.Count -ne 69) { throw "Default reference fixture created $($defaultSamples.Count) maps." }

    $defaultDestination = Join-Path $testRootFull 'Default69\WarThunderMaps'
    $result = Run-InstallerProcess @('-SourceRoot', $defaultFixture, '-Destination', $defaultDestination)
    if ($result.ExitCode -ne 0) { throw "Default 69-map geographic install failed:`n$($result.Output)" }
    if ($result.Output -notmatch 'Applied 14 geographic maps') { throw 'Default install did not apply 14 geographic maps.' }
    $defaultManifest = Get-Content -LiteralPath (Join-Path $defaultDestination 'WarThunderMaps.install.json') -Raw -Encoding UTF8 | ConvertFrom-Json
    if ([int]$defaultManifest.map_count -ne 69) { throw 'Default 69-map manifest map_count mismatch.' }
    if ([string]$defaultManifest.layout -ne 'GeographicV1') { throw 'Default 69-map manifest layout is not GeographicV1.' }
    if ([int]$defaultManifest.reference.geo_enabled_map_count -ne 14) { throw 'Default manifest geo_enabled_map_count is not 14.' }
    if ([int]$defaultManifest.reference.geo_disabled_map_count -ne 55) { throw 'Default manifest geo_disabled_map_count is not 55.' }
    $geoCount = @($defaultManifest.maps | Where-Object { $_.installed_map_id -like 'WarThunder.Geo1.*' }).Count
    $atlasCount = @($defaultManifest.maps | Where-Object { $_.installed_map_id -like 'WarThunder.Atlas1.*' }).Count
    if ($geoCount -ne 14) { throw "Default manifest geographic count is $geoCount." }
    if ($atlasCount -ne 55) { throw "Default manifest Atlas fallback count is $atlasCount." }
    Assert-NoOldCoordinateManifestFields $defaultManifest 'Default69'
    Assert-GeoXmlReference (Join-Path $defaultDestination 'air_afghan\CustomHeightmapList.xml') 'air_afghan' $defaultGeo.maps.air_afghan $defaultSamples['air_afghan']
    $grandAnchor = $defaultAtlas.air_grand_canyon
    $grandMetadata = Get-Content -LiteralPath (Join-Path $defaultFixture 'maps\air_grand_canyon\metadata.json') -Raw -Encoding UTF8 | ConvertFrom-Json
    Assert-XmlReference (Join-Path $defaultDestination 'air_grand_canyon\CustomHeightmapList.xml') $grandMetadata 'WarThunder.Atlas1.air_grand_canyon' 'true' ([double]$grandAnchor.latitude) ([double]$grandAnchor.longitude)

    $defaultData = @{}
    foreach ($directory in @(Get-ChildItem -LiteralPath $defaultDestination -Directory -Force)) {
        $dataPath = Join-Path $directory.FullName ($directory.Name + '.data')
        $defaultData[$directory.Name] = [pscustomobject]@{
            Hash = (Get-FileHash -LiteralPath $dataPath -Algorithm SHA256).Hash
            Timestamp = (Get-Item -LiteralPath $dataPath).LastWriteTimeUtc
        }
    }
    $geoStalePath = Join-Path $defaultDestination 'air_afghan\CustomHeightmapList.xml'
    $grandStalePath = Join-Path $defaultDestination 'air_grand_canyon\CustomHeightmapList.xml'
    Copy-Item -LiteralPath (Join-Path $defaultFixture 'maps\air_afghan\CustomHeightmapList.xml') -Destination $geoStalePath -Force
    Copy-Item -LiteralPath (Join-Path $defaultFixture 'maps\air_grand_canyon\CustomHeightmapList.xml') -Destination $grandStalePath -Force
    $result = Run-InstallerProcess @('-SourceRoot', $defaultFixture, '-Destination', $defaultDestination)
    if ($result.ExitCode -ne 0) { throw "Default 69-map regeneration failed:`n$($result.Output)" }
    if ($result.Output -notmatch 'reused by matching size/SHA256: 69') { throw 'Default regeneration did not reuse all 69 data files.' }
    Assert-GeoXmlReference $geoStalePath 'air_afghan' $defaultGeo.maps.air_afghan $defaultSamples['air_afghan']
    Assert-XmlReference $grandStalePath $grandMetadata 'WarThunder.Atlas1.air_grand_canyon' 'true' ([double]$grandAnchor.latitude) ([double]$grandAnchor.longitude)
    if (-not (Test-Path -LiteralPath ($geoStalePath + '.before-atlas.bak'))) { throw 'Default geographic regeneration is missing its Atlas XML backup.' }
    if (-not (Test-Path -LiteralPath ($grandStalePath + '.before-atlas.bak'))) { throw 'Default Atlas fallback regeneration is missing its XML backup.' }
    foreach ($id in $defaultData.Keys) {
        $dataPath = Join-Path (Join-Path $defaultDestination $id) ($id + '.data')
        if ((Get-FileHash -LiteralPath $dataPath -Algorithm SHA256).Hash -ne $defaultData[$id].Hash) { throw "Default data hash changed for $id." }
        if ((Get-Item -LiteralPath $dataPath).LastWriteTimeUtc -ne $defaultData[$id].Timestamp) { throw "Default data was rewritten for $id." }
    }
    $defaultBackupPath = Join-Path $defaultDestination 'WarThunderMaps.install.json.bak'
    if (-not (Test-Path -LiteralPath $defaultBackupPath)) { throw 'Default regeneration is missing its manifest backup.' }
    $result = Run-InstallerProcess @('-SourceRoot', $defaultFixture, '-Destination', $defaultDestination)
    if ($result.ExitCode -ne 0) { throw "Default 69-map no-op rerun failed:`n$($result.Output)" }
    if ($result.Output -notmatch 'reused by matching size/SHA256: 69') { throw 'Default no-op rerun did not reuse all 69 data files.' }

    $atlasOnlyDestination = Join-Path $testRootFull 'Default69-AtlasOnly\WarThunderMaps'
    $result = Run-InstallerProcess @('-SourceRoot', $defaultFixture, '-Destination', $atlasOnlyDestination, '-AtlasOnly')
    if ($result.ExitCode -ne 0) { throw "Default 69-map AtlasOnly install failed:`n$($result.Output)" }
    $atlasOnlyManifest = Get-Content -LiteralPath (Join-Path $atlasOnlyDestination 'WarThunderMaps.install.json') -Raw -Encoding UTF8 | ConvertFrom-Json
    if ([string]$atlasOnlyManifest.layout -ne 'AtlasV1') { throw 'AtlasOnly did not keep the AtlasV1 layout.' }
    if ([int]$atlasOnlyManifest.map_count -ne 69) { throw 'AtlasOnly manifest map_count mismatch.' }
    if (@($atlasOnlyManifest.maps | Where-Object { $_.installed_map_id -like 'WarThunder.Geo1.*' }).Count -ne 0) { throw 'AtlasOnly installed a geographic map.' }
    if (@($atlasOnlyManifest.maps | Where-Object { $_.installed_map_id -like 'WarThunder.Atlas1.*' }).Count -ne 69) { throw 'AtlasOnly did not install all 69 Atlas maps.' }

    Write-Host 'PASS: removed-coordinate switches reject, default 69-map 14-geo/55-Atlas geometry, Atlas fallback, XML/manifest regeneration, data reuse, WarThunderActive overlay disable, validation-only, safety backups, .cmd exit codes, package-root defaults, and explicit AtlasOnly.'
}
finally {
    if ($testRootFull.StartsWith($testPrefix, [StringComparison]::OrdinalIgnoreCase)) {
        for ($cleanupAttempt = 0; $cleanupAttempt -lt 3; $cleanupAttempt++) {
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
