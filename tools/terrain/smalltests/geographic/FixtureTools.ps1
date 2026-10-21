$ErrorActionPreference = 'Stop'

$script:Invariant = [Globalization.CultureInfo]::InvariantCulture
$script:EarthMetersPerDegree = 111320.0

function Format-FixtureNumber {
    param([double]$Value)
    return $Value.ToString('F12', $script:Invariant)
}

function Add-FixtureXmlText {
    param([System.Xml.XmlDocument]$Document, [System.Xml.XmlNode]$Parent, [string]$Name, [string]$Value)

    $element = $Document.CreateElement($Name)
    $element.InnerText = $Value
    [void]$Parent.AppendChild($element)
}

function Write-FixtureMap {
    param(
        [string]$Root,
        [string]$Id,
        [double[]]$WorldBounds,
        [double[]]$SampleBounds,
        [int]$Width = 4,
        [int]$Height = 4
    )

    $mapDir = Join-Path (Join-Path $Root 'maps') $Id
    New-Item -ItemType Directory -Path $mapDir -Force | Out-Null

    $values = New-Object 'int16[]' ($Width * $Height)
    for ($index = 0; $index -lt $values.Length; $index++) {
        $values[$index] = [int16]((($index * 37) + ($Id.Length * 11) + 17) % 900 - 450)
    }
    $data = New-Object byte[] ($values.Length * 2)
    [Buffer]::BlockCopy($values, 0, $data, 0, $data.Length)
    $dataPath = Join-Path $mapDir ($Id + '.data')
    [IO.File]::WriteAllBytes($dataPath, $data)
    $dataHash = (Get-FileHash -LiteralPath $dataPath -Algorithm SHA256).Hash.ToLowerInvariant()

    $metadata = [ordered]@{
        schema_version = 1
        id = $Id
        status = 'ok'
        source = [ordered]@{
            path = ('fixture://' + $Id)
            size = [long]$data.Length
            sha256 = $dataHash
        }
        catalog = [ordered]@{
            is_air = $true
            exists = $true
            map_coord0 = @([double]$WorldBounds[0], [double]$WorldBounds[1])
            map_coord1 = @([double]$WorldBounds[2], [double]$WorldBounds[3])
        }
        output = [ordered]@{
            sample_bounds_m = @([double]$SampleBounds[0], [double]$SampleBounds[1], [double]$SampleBounds[2], [double]$SampleBounds[3])
            id = $Id
            width = $Width
            height = $Height
            data_file = ($Id + '.data')
            data_bytes = [long]$data.Length
            data_sha256 = $dataHash
            output_type = 'int16_le'
            output_row_order = 'north_to_south'
            output_unit = 'meter'
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
    Add-FixtureXmlText $document $node 'File' ($Id + '.data')
    Add-FixtureXmlText $document $node 'Visible' 'true'
    Add-FixtureXmlText $document $node 'BigEndian' '0'
    Add-FixtureXmlText $document $node 'Signed' 'true'
    Add-FixtureXmlText $document $node 'Width' ([string]$Width)
    Add-FixtureXmlText $document $node 'Height' ([string]$Height)
    Add-FixtureXmlText $document $node 'AltitudeFactor' '1'
    Add-FixtureXmlText $document $node 'AltitudeOffset' '0'
    Add-FixtureXmlText $document $node 'Projection' 'Quad'
    $definitions = @(
        @('BottomLeft', $SampleBounds[0], $SampleBounds[1]),
        @('BottomRight', $SampleBounds[2], $SampleBounds[1]),
        @('TopRight', $SampleBounds[2], $SampleBounds[3]),
        @('TopLeft', $SampleBounds[0], $SampleBounds[3])
    )
    foreach ($corner in $definitions) {
        $latitude = [double]$corner[2] / $script:EarthMetersPerDegree
        $longitude = [double]$corner[1] / ($script:EarthMetersPerDegree * [Math]::Cos($latitude * [Math]::PI / 180.0))
        $cornerNode = $document.CreateElement([string]$corner[0])
        Add-FixtureXmlText $document $cornerNode 'Longitude' (Format-FixtureNumber $longitude)
        Add-FixtureXmlText $document $cornerNode 'Latitude' (Format-FixtureNumber $latitude)
        [void]$node.AppendChild($cornerNode)
    }
    $document.Save((Join-Path $mapDir 'CustomHeightmapList.xml'))

    return [pscustomobject]@{
        Id = $Id
        Folder = $mapDir
        DataPath = $dataPath
        DataHash = $dataHash
        DataBytes = [long]$data.Length
        WorldBounds = $WorldBounds
        SampleBounds = $SampleBounds
        Metadata = $metadata
    }
}

function Write-AtlasFile {
    param([string]$Path, [object]$Positions)

    $maps = [ordered]@{}
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

function Write-GeoFile {
    param(
        [string]$Path,
        [object]$Maps,
        [string]$Repository = 'https://github.com/example/wt-map-analyze',
        [string]$Commit = '1111111111111111111111111111111111111111'
    )

    $mapObject = [ordered]@{}
    foreach ($key in $Maps.Keys) { $mapObject[$key] = $Maps[$key] }
    $document = [ordered]@{
        schema = 1
        source = [ordered]@{
            repository = $Repository
            commit = $Commit
        }
        maps = $mapObject
    }
    [IO.File]::WriteAllText($Path, ($document | ConvertTo-Json -Depth 10), (New-Object Text.UTF8Encoding($false)))
}

function New-GeoEntry {
    param(
        [bool]$Enabled,
        [double[]]$LocalBounds,
        [double[]]$GeoBounds,
        [double]$UncertaintyKm = 1.0,
        [string]$Reason = '',
        [string]$Confidence = 'high',
        [string]$Method = 'fixture+dem-match',
        [string]$Source = 'fixture://geo'
    )

    $entry = [ordered]@{ enabled = $Enabled }
    if ($Enabled) {
        $entry.confidence = $Confidence
        $entry.method = $Method
        $entry.source = $Source
        $entry.local_bounds = @([double]$LocalBounds[0], [double]$LocalBounds[1], [double]$LocalBounds[2], [double]$LocalBounds[3])
        $entry.geo_bounds = @([double]$GeoBounds[0], [double]$GeoBounds[1], [double]$GeoBounds[2], [double]$GeoBounds[3])
        $entry.uncertainty_km = $UncertaintyKm
    }
    else {
        if ([string]::IsNullOrWhiteSpace($Reason)) { $Reason = 'fixture fallback to Atlas' }
        $entry.reason = $Reason
    }
    return $entry
}
