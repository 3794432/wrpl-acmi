[CmdletBinding()]
param(
    [string]$SourceRoot = '',
    [string]$Destination = (Join-Path $env:APPDATA 'Tacview\Data\Terrain\Custom\WarThunderMaps'),
    [string]$GeoReferences = '',
    [string]$AtlasReferences = '',
    [switch]$ValidateOnly,
    [switch]$AllowPartial,
    [switch]$FromInstaller
)

$ErrorActionPreference = 'Stop'
$script:AtlasInstallerName = 'war-thunder-air-terrain'
$script:GeoInstallerName = 'war-thunder-geographic-terrain'
$script:AtlasLayoutName = 'AtlasV1'
$script:GeoLayoutName = 'GeographicV1'
$script:MapIdPrefix = 'WarThunder.Geo1.'
$script:AtlasMapIdPrefix = 'WarThunder.Atlas1.'
$script:MainManifestName = 'WarThunderMaps.install.json'
$script:ExpectedMapCount = @((Get-Content -LiteralPath (Join-Path $PSScriptRoot 'AtlasReferences.json') -Raw -Encoding UTF8 | ConvertFrom-Json).PSObject.Properties).Count
if ($script:ExpectedMapCount -lt 1 -or $script:ExpectedMapCount -gt 128) { throw 'Terrain catalogue must contain between 1 and 128 map IDs.' }
$script:EarthMetersPerDegree = 111320.0
$script:ExitOnCompletion = -not [bool]$FromInstaller

function Resolve-FullPath {
    param([string]$Path)

    if ([string]::IsNullOrWhiteSpace($Path)) { throw 'A path is empty.' }
    return $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Path)
}

function Get-Sha256Hex {
    param([string]$Path)

    $sha = [Security.Cryptography.SHA256]::Create()
    $stream = [IO.File]::Open($Path, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
    try {
        $buffer = New-Object byte[] 4194304
        while (($read = $stream.Read($buffer, 0, $buffer.Length)) -gt 0) {
            [void]$sha.TransformBlock($buffer, 0, $read, $null, 0)
        }
        [void]$sha.TransformFinalBlock($buffer, 0, 0)
        return [BitConverter]::ToString($sha.Hash).Replace('-', '').ToLowerInvariant()
    }
    finally {
        $stream.Dispose()
        $sha.Dispose()
    }
}

function Write-TextAtomic {
    param([string]$Path, [string]$Text)

    $temp = $Path + '.tmp-' + $PID + '-' + [Guid]::NewGuid().ToString('N')
    try {
        [IO.File]::WriteAllText($temp, $Text, (New-Object Text.UTF8Encoding($false)))
        Move-Item -LiteralPath $temp -Destination $Path -Force
    }
    finally {
        if (Test-Path -LiteralPath $temp) { Remove-Item -LiteralPath $temp -Force }
    }
}

function Write-XmlAtomic {
    param([System.Xml.XmlDocument]$Document, [string]$Path)

    $temp = $Path + '.tmp-' + $PID + '-' + [Guid]::NewGuid().ToString('N')
    $settings = New-Object System.Xml.XmlWriterSettings
    $settings.Indent = $true
    $settings.IndentChars = '  '
    $settings.NewLineChars = [string][char]13 + [string][char]10
    $settings.Encoding = New-Object Text.UTF8Encoding($false)
    try {
        $writer = [System.Xml.XmlWriter]::Create($temp, $settings)
        try { $Document.Save($writer) }
        finally { $writer.Dispose() }
        [void][xml](Get-Content -LiteralPath $temp -Raw -Encoding UTF8)
        Move-Item -LiteralPath $temp -Destination $Path -Force
    }
    finally {
        if (Test-Path -LiteralPath $temp) { Remove-Item -LiteralPath $temp -Force }
    }
}

function Backup-FileOnce {
    param([string]$Path, [string]$BackupPath)

    if (-not (Test-Path -LiteralPath $Path)) { return }
    if (Test-Path -LiteralPath $BackupPath) { return }
    Copy-Item -LiteralPath $Path -Destination $BackupPath
}

function Get-JsonProperty {
    param([object]$Object, [string[]]$Names)

    if ($null -eq $Object) { return $null }
    if ($Object -is [System.Collections.IDictionary]) {
        foreach ($name in $Names) {
            if ($Object.Contains($name)) { return $Object[$name] }
        }
        return $null
    }
    foreach ($name in $Names) {
        $property = $Object.PSObject.Properties[$name]
        if ($null -ne $property) { return $property.Value }
    }
    return $null
}

function Convert-FiniteNumber {
    param([object]$Value, [string]$Label)

    if ($null -eq $Value -or $Value -is [bool]) { throw "$Label must be a finite number." }
    if ($Value -is [string]) {
        $parsed = 0.0
        if (-not [double]::TryParse([string]$Value, [Globalization.NumberStyles]::Float, [Globalization.CultureInfo]::InvariantCulture, [ref]$parsed)) {
            throw "$Label must be a finite number."
        }
        $number = $parsed
    }
    else {
        try { $number = [double]$Value }
        catch { throw "$Label must be a finite number." }
    }
    if ([double]::IsNaN($number) -or [double]::IsInfinity($number)) { throw "$Label must be a finite number." }
    return $number
}

function Read-FourNumbers {
    param([object]$Value, [string]$Label)

    if ($null -eq $Value) { throw "$Label is missing." }
    $items = @($Value)
    if ($items.Count -ne 4) { throw "$Label must contain exactly four numbers." }
    $result = New-Object 'double[]' 4
    for ($index = 0; $index -lt 4; $index++) {
        $result[$index] = Convert-FiniteNumber $items[$index] ("{0}[{1}]" -f $Label, $index)
    }
    return ,$result
}

function Read-RequiredString {
    param([object]$Value, [string]$Label)

    if ($null -eq $Value -or $Value -is [bool]) { throw "$Label must be a non-empty string." }
    $textValue = [string]$Value
    if ([string]::IsNullOrWhiteSpace($textValue)) { throw "$Label must be a non-empty string." }
    return $textValue.Trim()
}

function Read-InvariantDouble {
    param([string]$Text, [string]$Label)

    $value = 0.0
    if (-not [double]::TryParse($Text, [Globalization.NumberStyles]::Float, [Globalization.CultureInfo]::InvariantCulture, [ref]$value)) {
        throw "Invalid number for $Label"
    }
    if ([double]::IsNaN($value) -or [double]::IsInfinity($value)) { throw "Non-finite number for $Label" }
    return $value
}

function Add-TextElement {
    param([System.Xml.XmlDocument]$Document, [System.Xml.XmlNode]$Parent, [string]$Name, [string]$Value)

    $element = $Document.CreateElement($Name)
    $element.InnerText = $Value
    [void]$Parent.AppendChild($element)
    return $element
}

function Get-SingleHeightmapNode {
    param([System.Xml.XmlDocument]$Document, [string]$Path)

    if ($null -eq $Document.DocumentElement -or $Document.DocumentElement.Name -ne 'Resources') {
        throw "Unexpected XML root in $Path"
    }
    $nodes = $Document.SelectNodes('/Resources/CustomHeightmapList/CustomHeightmap')
    if ($nodes.Count -ne 1) { throw "Expected one CustomHeightmap in $Path" }
    return $nodes.Item(0)
}

function Read-GeographicReferences {
    param([string]$Path)

    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "Geographic references file is missing: $Path. Pass -GeoReferences with a schema-1 GeoReferences.json."
    }
    try {
        $root = Get-Content -LiteralPath $Path -Raw -Encoding UTF8 | ConvertFrom-Json
    }
    catch { throw "Invalid geographic references JSON in ${Path}: $($_.Exception.Message)" }
    if ($null -eq $root -or $root -is [System.Array]) { throw "Geographic references must be a JSON object: $Path" }

    $schemaValue = Get-JsonProperty $root @('schema')
    if ($null -eq $schemaValue -or $schemaValue -is [bool]) { throw "Geographic references schema must be 1: $Path" }
    $schemaNumber = Convert-FiniteNumber $schemaValue 'GeoReferences.schema'
    if ($schemaNumber -ne 1.0) { throw "Geographic references schema must be 1: $Path" }

    $sourceObject = Get-JsonProperty $root @('source')
    if ($null -eq $sourceObject) { throw "Geographic references source is missing: $Path" }
    $repository = Read-RequiredString (Get-JsonProperty $sourceObject @('repository')) 'GeoReferences.source.repository'
    $commit = Read-RequiredString (Get-JsonProperty $sourceObject @('commit')) 'GeoReferences.source.commit'

    $mapsValue = Get-JsonProperty $root @('maps')
    if ($null -eq $mapsValue -or $mapsValue -is [System.Array]) { throw "Geographic references maps must be a JSON object: $Path" }
    $pairs = New-Object System.Collections.ArrayList
    if ($mapsValue -is [System.Collections.IDictionary]) {
        foreach ($key in $mapsValue.Keys) {
            [void]$pairs.Add([pscustomobject]@{ Name = [string]$key; Value = $mapsValue[$key] })
        }
    }
    else {
        foreach ($property in $mapsValue.PSObject.Properties) {
            [void]$pairs.Add([pscustomobject]@{ Name = [string]$property.Name; Value = $property.Value })
        }
    }
    if ($pairs.Count -eq 0) { throw "Geographic references contains no map entries: $Path" }

    $entries = New-Object System.Collections.ArrayList
    $seen = @{}
    foreach ($pair in $pairs) {
        $id = [string]$pair.Name
        if ($id -notmatch '^[A-Za-z0-9_]+$') { throw "Unsafe geographic map ID in ${Path}: $id" }
        if ($seen.ContainsKey($id)) { throw "Duplicate geographic map ID in ${Path}: $id" }
        $seen[$id] = $true
        $entry = $pair.Value
        if ($null -eq $entry) { throw "Geographic entry for $id must be an object." }

        $enabledValue = Get-JsonProperty $entry @('enabled')
        if (-not ($enabledValue -is [bool])) { throw "Geographic entry for $id must declare a boolean enabled field." }
        $enabled = [bool]$enabledValue

        $confidence = ''
        $method = ''
        $sourceLabel = ''
        $reason = ''
        $localBounds = $null
        $geoBounds = $null
        $uncertaintyKm = 0.0

        $reasonValue = Get-JsonProperty $entry @('reason')
        if ($null -ne $reasonValue) { $reason = Read-RequiredString $reasonValue "GeoReferences.maps.$id.reason" }

        if ($enabled) {
            $confidence = Read-RequiredString (Get-JsonProperty $entry @('confidence')) "GeoReferences.maps.$id.confidence"
            $method = Read-RequiredString (Get-JsonProperty $entry @('method')) "GeoReferences.maps.$id.method"
            $sourceLabel = Read-RequiredString (Get-JsonProperty $entry @('source')) "GeoReferences.maps.$id.source"
            $localBounds = Read-FourNumbers (Get-JsonProperty $entry @('local_bounds')) "GeoReferences.maps.$id.local_bounds"
            $geoBounds = Read-FourNumbers (Get-JsonProperty $entry @('geo_bounds')) "GeoReferences.maps.$id.geo_bounds"
            if ($localBounds[2] -le $localBounds[0] -or $localBounds[3] -le $localBounds[1]) {
                throw "Geographic local_bounds for $id must be positive in X and Z."
            }
            if ($geoBounds[2] -le $geoBounds[0] -or $geoBounds[3] -le $geoBounds[1]) {
                throw "Geographic geo_bounds for $id must be positive in longitude and latitude."
            }
            if ($geoBounds[0] -lt -180.0 -or $geoBounds[2] -gt 180.0) {
                throw "Geographic geo_bounds longitude for $id must stay within [-180, 180]."
            }
            if ($geoBounds[1] -le -85.0 -or $geoBounds[3] -ge 85.0) {
                throw "Geographic geo_bounds latitude for $id must stay within (-85, 85)."
            }
            $uncertaintyValue = Get-JsonProperty $entry @('uncertainty_km')
            if ($null -ne $uncertaintyValue) {
                $uncertaintyKm = Convert-FiniteNumber $uncertaintyValue "GeoReferences.maps.$id.uncertainty_km"
                if ($uncertaintyKm -lt 0.0) { throw "Geographic uncertainty_km for $id must be zero or positive." }
            }
        }

        [void]$entries.Add([pscustomobject]@{
            Id = $id
            Enabled = $enabled
            Confidence = $confidence
            Method = $method
            Source = $sourceLabel
            Reason = $reason
            LocalBounds = $localBounds
            GeoBounds = $geoBounds
            UncertaintyKm = $uncertaintyKm
        })
    }

    if (-not ($entries | Where-Object { $_.Enabled })) {
        throw "Geographic references has no enabled maps; refusing to report success with no geographic terrain configured."
    }

    $fullPath = Resolve-FullPath $Path
    return [pscustomobject]@{
        Path = $fullPath
        Sha256 = (Get-Sha256Hex $fullPath)
        Bytes = [long](Get-Item -LiteralPath $fullPath).Length
        Schema = 1
        Repository = $repository
        Commit = $commit
        Entries = @($entries)
    }
}

function Add-AtlasAnchor {
    param([hashtable]$Anchors, [string]$Id, [object]$Value, [string]$Path)

    if ($Id -notmatch '^[A-Za-z0-9_]+$') { throw "Unsafe atlas map ID in ${Path}: $Id" }
    if ($Anchors.ContainsKey($Id)) { throw "Duplicate atlas map ID in ${Path}: $Id" }
    $latitude = $null
    $longitude = $null
    if ($Value -is [System.Array]) {
        $parts = @($Value)
        if ($parts.Count -ne 2) { throw "Atlas position for $Id must be [latitude, longitude]" }
        $latitude = Convert-FiniteNumber $parts[0] "$Id latitude"
        $longitude = Convert-FiniteNumber $parts[1] "$Id longitude"
    }
    elseif ($null -ne $Value) {
        $position = Get-JsonProperty $Value @('position', 'coordinates', 'anchor', 'location', 'latlon', 'lat_lon')
        if ($null -ne $position) {
            Add-AtlasAnchor $Anchors $Id $position $Path
            return
        }
        $latitudeValue = Get-JsonProperty $Value @('latitude', 'lat')
        $longitudeValue = Get-JsonProperty $Value @('longitude', 'lon', 'lng')
        if ($null -eq $latitudeValue -or $null -eq $longitudeValue) { throw "Atlas entry for $Id has no latitude/longitude position" }
        $latitude = Convert-FiniteNumber $latitudeValue "$Id latitude"
        $longitude = Convert-FiniteNumber $longitudeValue "$Id longitude"
    }
    else { throw "Atlas entry for $Id is empty" }
    if ([Math]::Abs($latitude) -ge 85.0) { throw "Atlas latitude for $Id must be within (-85, 85)." }
    if ([Math]::Abs($longitude) -gt 180.0) { throw "Atlas longitude for $Id must be within [-180, 180]." }
    $Anchors[$Id] = [pscustomobject]@{ Latitude = $latitude; Longitude = $longitude }
}

function Read-AtlasAnchors {
    param([string]$Path)

    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "Atlas references file is missing: $Path. Geographic overlap checks need the installed Atlas layout."
    }
    try { $root = Get-Content -LiteralPath $Path -Raw -Encoding UTF8 | ConvertFrom-Json }
    catch { throw "Invalid atlas references JSON in ${Path}: $($_.Exception.Message)" }
    if ($null -eq $root) { throw "Atlas references is empty: $Path" }

    $anchors = @{}
    $mapsValue = Get-JsonProperty $root @('maps', 'references')
    $container = $root
    if ($null -ne $mapsValue) { $container = $mapsValue }
    if ($container -is [System.Array]) {
        foreach ($item in @($container)) {
            $id = [string](Get-JsonProperty $item @('id', 'map', 'map_id', 'mapId'))
            if ([string]::IsNullOrWhiteSpace($id)) { throw "Atlas entry in ${Path} has no map ID" }
            $value = Get-JsonProperty $item @('position', 'coordinates', 'anchor', 'location', 'latlon', 'lat_lon')
            if ($null -eq $value) {
                $value = @((Get-JsonProperty $item @('latitude', 'lat')), (Get-JsonProperty $item @('longitude', 'lon', 'lng')))
            }
            Add-AtlasAnchor $anchors $id $value $Path
        }
    }
    elseif ($container -is [System.Collections.IDictionary] -or $container -is [pscustomobject]) {
        $metadataNames = @('schema', 'schema_version', 'layout', 'columns', 'rows', 'longitude_step', 'latitude_step', 'generated', 'generated_utc', 'generated_at', 'map_count', 'version', 'source', 'description', 'epoch', 'grid', 'step')
        if ($container -is [System.Collections.IDictionary]) {
            $keys = @($container.Keys)
        }
        else {
            $keys = @($container.PSObject.Properties | ForEach-Object { $_.Name })
        }
        foreach ($key in $keys) {
            if ($metadataNames -contains $key) { continue }
            if ($container -is [System.Collections.IDictionary]) { $value = $container[$key] }
            else { $value = $container.$key }
            Add-AtlasAnchor $anchors ([string]$key) $value $Path
        }
    }
    else { throw "Atlas references JSON must be an object or array: $Path" }

    if ($anchors.Count -eq 0) { throw "Atlas references contains no map positions: $Path" }
    $fullPath = Resolve-FullPath $Path
    return [pscustomobject]@{
        Path = $fullPath
        Sha256 = (Get-Sha256Hex $fullPath)
        Bytes = [long](Get-Item -LiteralPath $fullPath).Length
        Anchors = $anchors
    }
}

function Read-PackageMaps {
    param([string]$MapsRoot)

    if (-not (Test-Path -LiteralPath $MapsRoot -PathType Container)) { throw "No maps folder under the source root: $MapsRoot" }
    $allDirs = @(Get-ChildItem -LiteralPath $MapsRoot -Directory -Force | Sort-Object Name)
    $mapDirs = New-Object System.Collections.ArrayList
    foreach ($candidate in $allDirs) {
        $candidateMetadata = Join-Path $candidate.FullName 'metadata.json'
        if (Test-Path -LiteralPath $candidateMetadata -PathType Leaf) {
            [void]$mapDirs.Add($candidate)
            continue
        }
        $candidateResult = Join-Path $candidate.FullName 'result.json'
        if (Test-Path -LiteralPath $candidateResult -PathType Leaf) {
            if (Test-Path -LiteralPath (Join-Path $candidate.FullName 'CustomHeightmapList.xml')) {
                throw "Map directory $($candidate.Name) has XML but no metadata.json"
            }
            try { $incomplete = Get-Content -LiteralPath $candidateResult -Raw -Encoding UTF8 | ConvertFrom-Json }
            catch { throw "Map directory $($candidate.Name) has no metadata.json and an invalid result.json" }
            if ([string]$incomplete.status -eq 'missing') {
                Write-Host "Skipping incomplete map $($candidate.Name): no source assets, no installable data."
                continue
            }
        }
        throw "Map directory $($candidate.Name) has no metadata.json"
    }

    $maps = @{}
    foreach ($mapDir in $mapDirs) {
        $id = [string]$mapDir.Name
        if ($id -notmatch '^[A-Za-z0-9_]+$') { throw "Unsafe map directory name: $id" }
        if ($maps.ContainsKey($id)) { throw "Duplicate map ID: $id" }
        $metadataPath = Join-Path $mapDir.FullName 'metadata.json'
        try { $metadata = Get-Content -LiteralPath $metadataPath -Raw -Encoding UTF8 | ConvertFrom-Json }
        catch { throw "Invalid metadata.json in ${id}: $($_.Exception.Message)" }
        if ([string]$metadata.status -ne 'ok') { throw "Map $id is not marked ok in metadata.json" }
        if ([string]$metadata.id -cne $id) { throw "metadata.id mismatch in $id" }
        $output = Get-JsonProperty $metadata @('output')
        if ($null -eq $output) { throw "metadata.output is missing in $id" }
        if ([string](Get-JsonProperty $output @('id')) -cne $id) { throw "metadata.output.id mismatch in $id" }
        $bounds = Read-FourNumbers (Get-JsonProperty $output @('sample_bounds_m')) "sample_bounds_m in $id"
        if ($bounds[2] -le $bounds[0] -or $bounds[3] -le $bounds[1]) { throw "sample_bounds_m in $id must be positive in X and Z." }
        $maps[$id] = [pscustomobject]@{
            Id = $id
            Folder = $mapDir.FullName
            MetadataPath = $metadataPath
            Metadata = $metadata
            Output = $output
            Bounds = $bounds
        }
    }
    return $maps
}

function Assert-SourceXml {
    param([object]$Map, [System.Xml.XmlNode]$Node)

    $id = [string]$Map.Id
    if ([string]$Node.ID -cne ('WarThunder.' + $id)) { throw "Source XML ID mismatch for $id" }
    if ([string]$Node.File -cne ($id + '.data')) { throw "Source XML File mismatch for $id" }
    if ([int]$Node.Width -ne [int]$Map.Output.width -or [int]$Node.Height -ne [int]$Map.Output.height) {
        throw "Source XML dimensions mismatch for $id"
    }
    if ([string]$Node.BigEndian -ne '0' -or [string]$Node.Signed -ne 'true') { throw "Source XML byte order/sign mismatch for $id" }
    if ([string]$Node.AltitudeFactor -ne '1' -or [string]$Node.AltitudeOffset -ne '0') { throw "Source XML altitude conversion mismatch for $id" }
    if ([string]$Node.Projection -ne 'Quad') { throw "Source XML projection mismatch for $id" }

    $definitions = @(
        @('BottomLeft', $Map.Bounds[0], $Map.Bounds[1]),
        @('BottomRight', $Map.Bounds[2], $Map.Bounds[1]),
        @('TopRight', $Map.Bounds[2], $Map.Bounds[3]),
        @('TopLeft', $Map.Bounds[0], $Map.Bounds[3])
    )
    foreach ($definition in $definitions) {
        $element = $Node.SelectSingleNode([string]$definition[0])
        if ($null -eq $element) { throw "Source XML is missing $($definition[0]) for $id" }
        $latitude = [double]$definition[2] / $script:EarthMetersPerDegree
        $longitude = [double]$definition[1] / ($script:EarthMetersPerDegree * [Math]::Cos($latitude * [Math]::PI / 180.0))
        $actualLongitude = Read-InvariantDouble ([string]$element.Longitude) "$id $($definition[0]) Longitude"
        $actualLatitude = Read-InvariantDouble ([string]$element.Latitude) "$id $($definition[0]) Latitude"
        if ([Math]::Abs($actualLongitude - $longitude) -gt 1e-8 -or [Math]::Abs($actualLatitude - $latitude) -gt 1e-8) {
            throw "Source XML coordinates are not the expected zero-anchor reference for $id $($definition[0])"
        }
    }
}

function Assert-WorldBounds {
    param([object]$Entry, [object]$Map)

    $local = $Entry.LocalBounds
    $sample = $Map.Bounds
    $tolerance = 1.0
    if ($local[0] -gt ($sample[0] + $tolerance) -or $local[1] -gt ($sample[1] + $tolerance) -or
        $local[2] -lt ($sample[2] - $tolerance) -or $local[3] -lt ($sample[3] - $tolerance)) {
        throw ("local_bounds for {0} must cover sample_bounds_m {1}..{2} x {3}..{4}; texture bounds are not world bounds." -f $Entry.Id, $sample[0], $sample[2], $sample[1], $sample[3])
    }

    $catalog = Get-JsonProperty $Map.Metadata @('catalog')
    if ($null -ne $catalog) {
        $first = Get-JsonProperty $catalog @('map_coord0')
        $second = Get-JsonProperty $catalog @('map_coord1')
        if ($null -ne $first -and $null -ne $second) {
            $firstParts = @($first)
            $secondParts = @($second)
            if ($firstParts.Count -ne 2 -or $secondParts.Count -ne 2) { throw "catalog map coordinates for $($Entry.Id) must be two-element arrays." }
            $world = @(
                (Convert-FiniteNumber $firstParts[0] "$($Entry.Id) map_coord0[0]"),
                (Convert-FiniteNumber $firstParts[1] "$($Entry.Id) map_coord0[1]"),
                (Convert-FiniteNumber $secondParts[0] "$($Entry.Id) map_coord1[0]"),
                (Convert-FiniteNumber $secondParts[1] "$($Entry.Id) map_coord1[1]")
            )
            if ($world[2] -le $world[0] -or $world[3] -le $world[1]) { throw "catalog world bounds for $($Entry.Id) must be positive." }
            $toleranceX = [Math]::Max(1.0, 0.01 * ($world[2] - $world[0]))
            $toleranceZ = [Math]::Max(1.0, 0.01 * ($world[3] - $world[1]))
            if ([Math]::Abs($local[0] - $world[0]) -gt $toleranceX -or [Math]::Abs($local[2] - $world[2]) -gt $toleranceX -or
                [Math]::Abs($local[1] - $world[1]) -gt $toleranceZ -or [Math]::Abs($local[3] - $world[3]) -gt $toleranceZ) {
                throw ("local_bounds for {0} do not match the catalog world square [{1}, {2}, {3}, {4}]; texture bounds are not world bounds." -f $Entry.Id, $world[0], $world[1], $world[2], $world[3])
            }
        }
    }
}

function Read-VerifiedRecord {
    param([object]$Entry, [object]$Map)

    $id = [string]$Entry.Id
    $output = $Map.Output
    $dataFile = [string](Get-JsonProperty $output @('data_file'))
    if ($dataFile -cne ($id + '.data')) { throw "Unexpected data_file in $id" }
    $width = [int](Get-JsonProperty $output @('width'))
    $height = [int](Get-JsonProperty $output @('height'))
    if ($width -le 0 -or $height -le 0) { throw "Invalid dimensions in $id" }
    $dataBytes = [long](Get-JsonProperty $output @('data_bytes'))
    if ($dataBytes -ne ([long]$width * [long]$height * 2)) { throw "data_bytes does not match dimensions in $id" }
    $expectedHash = ([string](Get-JsonProperty $output @('data_sha256'))).ToLowerInvariant()
    if ($expectedHash -notmatch '^[0-9a-f]{64}$') { throw "Invalid data_sha256 in $id" }
    $outputType = Get-JsonProperty $output @('output_type')
    if ($null -ne $outputType -and ([string]$outputType) -ne 'int16_le') { throw "Output is not int16_le in $id" }
    $altitudeFactor = Get-JsonProperty $output @('altitude_factor')
    if ($null -ne $altitudeFactor -and [double]$altitudeFactor -ne 1.0) { throw "Altitude factor is not 1 in $id" }
    $altitudeOffset = Get-JsonProperty $output @('altitude_offset')
    if ($null -ne $altitudeOffset -and [double]$altitudeOffset -ne 0.0) { throw "Altitude offset is not 0 in $id" }

    $dataPath = Join-Path $Map.Folder $dataFile
    if (-not (Test-Path -LiteralPath $dataPath -PathType Leaf)) { throw "Missing data file in $id" }
    $dataItem = Get-Item -LiteralPath $dataPath
    if ($dataItem.PSIsContainer -or $dataItem.Length -ne $dataBytes) { throw "Data size mismatch in $id" }
    $actualHash = Get-Sha256Hex $dataPath
    if ($actualHash -ne $expectedHash) { throw "Data SHA256 mismatch in $id" }

    $xmlPath = Join-Path $Map.Folder 'CustomHeightmapList.xml'
    if (-not (Test-Path -LiteralPath $xmlPath -PathType Leaf)) { throw "Missing CustomHeightmapList.xml in $id" }
    try { [xml]$sourceXml = Get-Content -LiteralPath $xmlPath -Raw -Encoding UTF8 }
    catch { throw "Invalid XML in ${id}: $($_.Exception.Message)" }
    Assert-SourceXml $Map (Get-SingleHeightmapNode $sourceXml $xmlPath)

    $metadataItem = Get-Item -LiteralPath $Map.MetadataPath
    $metadataHash = Get-Sha256Hex $Map.MetadataPath
    $sourceMetadata = Get-JsonProperty $Map.Metadata @('source')
    if ($null -ne $sourceMetadata) {
        $sourceHash = Get-JsonProperty $sourceMetadata @('sha256')
        if ($null -ne $sourceHash -and ([string]$sourceHash) -notmatch '^[0-9a-fA-F]{64}$') {
            throw "metadata.source.sha256 is invalid in $id"
        }
    }

    Assert-WorldBounds $Entry $Map
    $corners = Get-GeoBoundsCorners $Entry $Map
    $box = Get-CornerBox $corners

    return [pscustomobject]@{
        Id = $id
        Folder = $Map.Folder
        DataFile = $dataFile
        DataPath = $dataPath
        DataBytes = $dataBytes
        DataHash = $expectedHash
        Width = $width
        Height = $height
        Bounds = $Map.Bounds
        MetadataPath = $Map.MetadataPath
        MetadataHash = $metadataHash
        MetadataBytes = [long]$metadataItem.Length
        InstalledMapId = ($script:MapIdPrefix + $id)
        AtlasMapId = ($script:AtlasMapIdPrefix + $id)
        Entry = $Entry
        Corners = $corners
        CornerBox = $box
        ExpandedBox = (Expand-CornerBox $box $Entry.UncertaintyKm)
    }
}

function Get-GeoBoundsCorners {
    param([object]$Entry, [object]$Map)

    $local = $Entry.LocalBounds
    $geo = $Entry.GeoBounds
    $sample = $Map.Bounds
    $longitudeSpan = $geo[2] - $geo[0]
    $latitudeSpan = $geo[3] - $geo[1]
    $localSpanX = $local[2] - $local[0]
    $localSpanZ = $local[3] - $local[1]
    $definitions = @(
        @('BottomLeft', $sample[0], $sample[1]),
        @('BottomRight', $sample[2], $sample[1]),
        @('TopRight', $sample[2], $sample[3]),
        @('TopLeft', $sample[0], $sample[3])
    )
    $corners = New-Object System.Collections.ArrayList
    foreach ($definition in $definitions) {
        $x = [double]$definition[1]
        $z = [double]$definition[2]
        $longitude = $geo[0] + (($x - $local[0]) / $localSpanX) * $longitudeSpan
        $latitude = $geo[1] + (($z - $local[1]) / $localSpanZ) * $latitudeSpan
        if ([double]::IsNaN($longitude) -or [double]::IsInfinity($longitude) -or [double]::IsNaN($latitude) -or [double]::IsInfinity($latitude)) {
            throw "Geographic transform produced a non-finite corner for $($Entry.Id) $($definition[0])"
        }
        if ($longitude -lt -180.0 -or $longitude -gt 180.0) {
            throw "Geographic transform produced an out-of-range longitude for $($Entry.Id) $($definition[0])"
        }
        if ($latitude -le -85.0 -or $latitude -ge 85.0) {
            throw "Geographic transform produced an out-of-range latitude for $($Entry.Id) $($definition[0])"
        }
        [void]$corners.Add([pscustomobject]@{
            Name = [string]$definition[0]
            Longitude = $longitude
            Latitude = $latitude
        })
    }
    return @($corners)
}

function Get-AtlasCorners {
    param([object]$Map, [object]$Anchor)

    $definitions = @(
        @('BottomLeft', $Map.Bounds[0], $Map.Bounds[1]),
        @('BottomRight', $Map.Bounds[2], $Map.Bounds[1]),
        @('TopRight', $Map.Bounds[2], $Map.Bounds[3]),
        @('TopLeft', $Map.Bounds[0], $Map.Bounds[3])
    )
    $corners = New-Object System.Collections.ArrayList
    foreach ($definition in $definitions) {
        $latitude = [double]$Anchor.Latitude + ([double]$definition[2] / $script:EarthMetersPerDegree)
        $longitude = [double]$Anchor.Longitude + ([double]$definition[1] / ($script:EarthMetersPerDegree * [Math]::Cos($latitude * [Math]::PI / 180.0)))
        if ([Math]::Abs($latitude) -ge 85.0 -or [Math]::Abs($longitude) -gt 180.0) {
            throw "Atlas corner for $($Map.Id) crosses a supported geographic boundary"
        }
        [void]$corners.Add([pscustomobject]@{
            Name = [string]$definition[0]
            Longitude = $longitude
            Latitude = $latitude
        })
    }
    return @($corners)
}

function Get-CornerBox {
    param([object[]]$Corners)

    $minimumLatitude = [double]::PositiveInfinity
    $maximumLatitude = [double]::NegativeInfinity
    $minimumLongitude = [double]::PositiveInfinity
    $maximumLongitude = [double]::NegativeInfinity
    foreach ($corner in $Corners) {
        $minimumLatitude = [Math]::Min($minimumLatitude, [double]$corner.Latitude)
        $maximumLatitude = [Math]::Max($maximumLatitude, [double]$corner.Latitude)
        $minimumLongitude = [Math]::Min($minimumLongitude, [double]$corner.Longitude)
        $maximumLongitude = [Math]::Max($maximumLongitude, [double]$corner.Longitude)
    }
    return [pscustomobject]@{
        MinLatitude = $minimumLatitude
        MaxLatitude = $maximumLatitude
        MinLongitude = $minimumLongitude
        MaxLongitude = $maximumLongitude
    }
}

function Expand-CornerBox {
    param([object]$Box, [double]$UncertaintyKm)

    if ($UncertaintyKm -le 0.0) { return $Box }
    $latitudePadding = $UncertaintyKm / 111.32
    $maximumAbsoluteLatitude = [Math]::Max([Math]::Abs($Box.MinLatitude), [Math]::Abs($Box.MaxLatitude))
    $cosine = [Math]::Cos($maximumAbsoluteLatitude * [Math]::PI / 180.0)
    if ($cosine -lt 0.01) { $cosine = 0.01 }
    $longitudePadding = $UncertaintyKm / (111.32 * $cosine)
    return [pscustomobject]@{
        MinLatitude = $Box.MinLatitude - $latitudePadding
        MaxLatitude = $Box.MaxLatitude + $latitudePadding
        MinLongitude = $Box.MinLongitude - $longitudePadding
        MaxLongitude = $Box.MaxLongitude + $longitudePadding
    }
}

function Test-BoxOverlap {
    param([object]$First, [object]$Second)

    return ($First.MinLongitude -lt $Second.MaxLongitude) -and ($Second.MinLongitude -lt $First.MaxLongitude) -and
        ($First.MinLatitude -lt $Second.MaxLatitude) -and ($Second.MinLatitude -lt $First.MaxLatitude)
}

function New-HeightmapXmlDocument {
    param(
        [string]$Id,
        [string]$DataFile,
        [bool]$Visible,
        [int]$Width,
        [int]$Height,
        [object[]]$Corners
    )

    $document = New-Object System.Xml.XmlDocument
    [void]$document.AppendChild($document.CreateXmlDeclaration('1.0', 'utf-8', $null))
    $resources = $document.CreateElement('Resources')
    [void]$document.AppendChild($resources)
    $list = $document.CreateElement('CustomHeightmapList')
    [void]$resources.AppendChild($list)
    $node = $document.CreateElement('CustomHeightmap')
    $node.SetAttribute('Layer', 'Real World')
    $node.SetAttribute('ID', $Id)
    [void]$list.AppendChild($node)
    [void](Add-TextElement $document $node 'File' $DataFile)
    $visibleText = if ($Visible) { 'true' } else { 'false' }
    [void](Add-TextElement $document $node 'Visible' $visibleText)
    [void](Add-TextElement $document $node 'BigEndian' '0')
    [void](Add-TextElement $document $node 'Signed' 'true')
    [void](Add-TextElement $document $node 'Width' ([string]$Width))
    [void](Add-TextElement $document $node 'Height' ([string]$Height))
    [void](Add-TextElement $document $node 'AltitudeFactor' '1')
    [void](Add-TextElement $document $node 'AltitudeOffset' '0')
    [void](Add-TextElement $document $node 'Projection' 'Quad')
    foreach ($corner in $Corners) {
        $cornerNode = $document.CreateElement($corner.Name)
        [void](Add-TextElement $document $cornerNode 'Longitude' $corner.Longitude.ToString('F12', [Globalization.CultureInfo]::InvariantCulture))
        [void](Add-TextElement $document $cornerNode 'Latitude' $corner.Latitude.ToString('F12', [Globalization.CultureInfo]::InvariantCulture))
        [void]$node.AppendChild($cornerNode)
    }
    return $document
}

function Assert-XmlState {
    param(
        [string]$Path,
        [string]$Id,
        [string]$DataFile,
        [int]$Width,
        [int]$Height,
        [object[]]$ExpectedCorners
    )

    [xml]$document = Get-Content -LiteralPath $Path -Raw -Encoding UTF8
    $node = Get-SingleHeightmapNode $document $Path
    if ([string]$node.Layer -ne 'Real World') { throw "Installed XML Layer mismatch for $Id" }
    if ([string]$node.ID -cne $Id) { throw "Installed XML ID mismatch for $Id" }
    if ([string]$node.File -cne $DataFile) { throw "Installed XML File mismatch for $Id" }
    if ([string]$node.Visible -ne 'true') { throw "Installed XML Visible is not true for $Id" }
    if ([string]$node.BigEndian -ne '0' -or [string]$node.Signed -ne 'true') { throw "Installed XML byte order/sign mismatch for $Id" }
    if ([string]$node.AltitudeFactor -ne '1' -or [string]$node.AltitudeOffset -ne '0') { throw "Installed XML altitude conversion mismatch for $Id" }
    if ([string]$node.Projection -ne 'Quad') { throw "Installed XML projection mismatch for $Id" }
    if ([int]$node.Width -ne $Width -or [int]$node.Height -ne $Height) { throw "Installed XML dimensions mismatch for $Id" }
    foreach ($corner in $ExpectedCorners) {
        $element = $node.SelectSingleNode($corner.Name)
        if ($null -eq $element) { throw "Installed XML is missing $($corner.Name) for $Id" }
        $longitude = Read-InvariantDouble ([string]$element.Longitude) "$Id installed Longitude"
        $latitude = Read-InvariantDouble ([string]$element.Latitude) "$Id installed Latitude"
        if ([Math]::Abs($longitude - $corner.Longitude) -gt 1e-9 -or [Math]::Abs($latitude - $corner.Latitude) -gt 1e-9) {
            throw "Installed coordinates do not match the requested layout for $Id $($corner.Name)"
        }
    }
}

function Test-XmlState {
    param(
        [string]$Path,
        [string]$Id,
        [string]$DataFile,
        [int]$Width,
        [int]$Height,
        [object[]]$ExpectedCorners
    )

    try {
        Assert-XmlState $Path $Id $DataFile $Width $Height $ExpectedCorners
        return $true
    }
    catch { return $false }
}

function Read-OwnedMainManifest {
    param(
        [string]$DestinationPath,
        [hashtable]$PackageMaps,
        [bool]$AllowPartial
    )

    $manifestPath = Join-Path $DestinationPath $script:MainManifestName
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) {
        throw "The Atlas install manifest is missing: $manifestPath. Run Install-All-Terrains.ps1 before installing geographic terrain."
    }
    try { $manifest = Get-Content -LiteralPath $manifestPath -Raw -Encoding UTF8 | ConvertFrom-Json }
    catch { throw "Existing Atlas install manifest is invalid: $manifestPath" }
    if ([string]$manifest.installer -ne $script:AtlasInstallerName) {
        throw "Destination contains an unknown $($script:MainManifestName); refusing to take it over: $DestinationPath"
    }
    if ([int]$manifest.schema_version -ne 2) { throw "Unsupported Atlas install manifest schema: $manifestPath" }
    $layout = [string]$manifest.layout
    if ($layout -ne $script:AtlasLayoutName -and $layout -ne $script:GeoLayoutName) {
        throw "Unsupported Atlas install manifest layout: $layout"
    }

    $entries = @($manifest.maps)
    $expectedCount = if ($AllowPartial) { $PackageMaps.Count } else { $script:ExpectedMapCount }
    if ($entries.Count -ne $expectedCount) {
        throw "The Atlas install manifest lists $($entries.Count) maps; expected $expectedCount. Run Install-All-Terrains.ps1 again."
    }
    if ($entries.Count -ne $PackageMaps.Count) {
        throw "The Atlas install manifest and the source package have different map counts ($($entries.Count) vs $($PackageMaps.Count))."
    }

    $byId = @{}
    foreach ($entry in $entries) {
        if ($null -eq $entry) { throw 'The Atlas install manifest contains an empty map entry.' }
        $id = [string]$entry.id
        if ($id -notmatch '^[A-Za-z0-9_]+$') { throw "The Atlas install manifest contains an unsafe map ID: $id" }
        if ($byId.ContainsKey($id)) { throw "The Atlas install manifest contains a duplicate map ID: $id" }
        if (-not $PackageMaps.ContainsKey($id)) { throw "The Atlas install manifest lists $id, which is not in the source package." }
        $map = $PackageMaps[$id]
        $dataFile = [string]$entry.data_file
        if ($dataFile -cne ($id + '.data')) { throw "The Atlas install manifest has an unexpected data_file for $id" }
        $expectedBytes = [long](Get-JsonProperty $map.Output @('data_bytes'))
        $manifestBytes = [long](Get-JsonProperty $entry @('data_bytes'))
        if ($manifestBytes -ne $expectedBytes) { throw "The Atlas install manifest data size differs from the source for $id" }
        $expectedHash = ([string](Get-JsonProperty $map.Output @('data_sha256'))).ToLowerInvariant()
        $manifestHash = ([string](Get-JsonProperty $entry @('data_sha256'))).ToLowerInvariant()
        if ($manifestHash -ne $expectedHash) { throw "The Atlas install manifest data SHA256 differs from the source for $id" }
        $byId[$id] = $entry
    }
    foreach ($id in $PackageMaps.Keys) {
        if (-not $byId.ContainsKey($id)) { throw "The Atlas install manifest is missing $id. Run Install-All-Terrains.ps1 again." }
    }

    return [pscustomobject]@{
        Path = $manifestPath
        Manifest = $manifest
        MapEntries = $byId
    }
}

function Set-JsonProperty {
    param([object]$Object, [string]$Name, [object]$Value)

    if ($Object -is [System.Collections.IDictionary]) {
        $Object[$Name] = $Value
        return
    }
    Add-Member -InputObject $Object -NotePropertyName $Name -NotePropertyValue $Value -Force
}

function Invoke-GeographicInstaller {
    if ([string]::IsNullOrWhiteSpace($SourceRoot)) { $script:SourceRoot = $PSScriptRoot }
    $sourceRootFull = Resolve-FullPath $script:SourceRoot
    $mapsRoot = Join-Path $sourceRootFull 'maps'
    if (-not (Test-Path -LiteralPath $mapsRoot -PathType Container)) {
        throw "No maps folder under $sourceRootFull. Pass -SourceRoot for a workspace test."
    }

    $geoPathValue = $GeoReferences
    if ([string]::IsNullOrWhiteSpace($geoPathValue)) { $geoPathValue = Join-Path $sourceRootFull 'GeoReferences.json' }
    $geo = Read-GeographicReferences $geoPathValue

    $atlasPathValue = $AtlasReferences
    if ([string]::IsNullOrWhiteSpace($atlasPathValue)) { $atlasPathValue = Join-Path $sourceRootFull 'AtlasReferences.json' }
    $atlas = Read-AtlasAnchors $atlasPathValue

    $packageMaps = Read-PackageMaps $mapsRoot
    if (-not $AllowPartial) {
        if ($packageMaps.Count -ne $script:ExpectedMapCount) {
            throw "Expected $($script:ExpectedMapCount) map directories, found $($packageMaps.Count). Use -AllowPartial only for a test fixture."
        }
        if ($atlas.Anchors.Count -ne $script:ExpectedMapCount) {
            throw "Atlas references has $($atlas.Anchors.Count) map IDs; expected $($script:ExpectedMapCount): $($atlas.Path)"
        }
    }
    foreach ($atlasId in $atlas.Anchors.Keys) {
        if (-not $packageMaps.ContainsKey($atlasId)) {
            throw "Atlas references contains unknown map ID ${atlasId}: $($atlas.Path)"
        }
    }

    $enabledEntries = @($geo.Entries | Where-Object { $_.Enabled })
    $enabledIds = @{}
    $records = @{}
    $position = 0
    foreach ($entry in $enabledEntries) {
        $position++
        if (-not $packageMaps.ContainsKey($entry.Id)) { throw "Geographic references enables $($entry.Id), which has no source map in $mapsRoot" }
        $record = Read-VerifiedRecord $entry $packageMaps[$entry.Id]
        $records[$entry.Id] = $record
        $enabledIds[$entry.Id] = $true
        Write-Host ("[{0}/{1}] verified geographic source {2} ({3:N1} MiB)" -f $position, $enabledEntries.Count, $record.Id, ($record.DataBytes / 1MB))
    }

    $recordList = @($records.Values | Sort-Object Id)
    for ($first = 0; $first -lt $recordList.Count; $first++) {
        for ($second = $first + 1; $second -lt $recordList.Count; $second++) {
            if (Test-BoxOverlap $recordList[$first].ExpandedBox $recordList[$second].ExpandedBox) {
                throw "Geographic corner bounding boxes overlap: $($recordList[$first].Id) and $($recordList[$second].Id)"
            }
        }
    }

    $atlasBoxes = New-Object System.Collections.ArrayList
    foreach ($atlasId in ($atlas.Anchors.Keys | Sort-Object)) {
        $map = $packageMaps[$atlasId]
        $corners = Get-AtlasCorners $map $atlas.Anchors[$atlasId]
        [void]$atlasBoxes.Add([pscustomobject]@{ Id = $atlasId; Box = (Get-CornerBox $corners) })
    }
    foreach ($record in $recordList) {
        foreach ($atlasBox in $atlasBoxes) {
            if (Test-BoxOverlap $record.ExpandedBox $atlasBox.Box) {
                throw "Geographic bounding box for $($record.Id) overlaps Atlas map $($atlasBox.Id)"
            }
        }
    }

    if ($ValidateOnly) {
        Write-Host ("Validated {0} geographic maps and {1} Atlas boxes from source; no files were written." -f $recordList.Count, $atlasBoxes.Count)
        return
    }

    $destinationFull = Resolve-FullPath $Destination
    if (-not (Test-Path -LiteralPath $destinationFull)) {
        throw "Destination is missing: $destinationFull. Run Install-All-Terrains.ps1 before installing geographic terrain."
    }
    if (-not (Get-Item -LiteralPath $destinationFull).PSIsContainer) { throw "Destination is not a directory: $destinationFull" }

    $owned = Read-OwnedMainManifest $destinationFull $packageMaps ([bool]$AllowPartial)
    $manifest = $owned.Manifest
    $mapEntries = $owned.MapEntries

    $verifiedDataBytes = [long]0
    foreach ($map in ($packageMaps.Values | Sort-Object Id)) {
        $targetDir = Join-Path $destinationFull $map.Id
        if (-not (Test-Path -LiteralPath $targetDir -PathType Container)) {
            throw "The Atlas install is incomplete: map directory is missing for $($map.Id)"
        }
        $dataFile = [string](Get-JsonProperty $map.Output @('data_file'))
        $dataPath = Join-Path $targetDir $dataFile
        if (-not (Test-Path -LiteralPath $dataPath -PathType Leaf)) {
            throw "The Atlas install is incomplete: data file is missing for $($map.Id)"
        }
        $expectedBytes = [long](Get-JsonProperty $map.Output @('data_bytes'))
        $expectedHash = ([string](Get-JsonProperty $map.Output @('data_sha256'))).ToLowerInvariant()
        $dataItem = Get-Item -LiteralPath $dataPath
        if ($dataItem.Length -ne $expectedBytes) { throw "Installed data size mismatch for $($map.Id)" }
        if ((Get-Sha256Hex $dataPath) -ne $expectedHash) { throw "Installed data SHA256 mismatch for $($map.Id)" }
        $metadataPath = Join-Path $targetDir 'metadata.json'
        if (-not (Test-Path -LiteralPath $metadataPath -PathType Leaf)) {
            throw "The Atlas install is incomplete: metadata.json is missing for $($map.Id)"
        }
        if ((Get-Sha256Hex $metadataPath) -ne (Get-Sha256Hex $map.MetadataPath)) {
            throw "Installed metadata.json differs from the source for $($map.Id)"
        }
        $xmlPath = Join-Path $targetDir 'CustomHeightmapList.xml'
        if (-not (Test-Path -LiteralPath $xmlPath -PathType Leaf)) {
            throw "The Atlas install is incomplete: CustomHeightmapList.xml is missing for $($map.Id)"
        }
        $verifiedDataBytes += $expectedBytes
    }
    Write-Host ("Verified the existing Atlas install: {0} maps, {1:N2} GiB of data reused without copying." -f $packageMaps.Count, ($verifiedDataBytes / 1GB))

    $rewritten = 0
    foreach ($map in ($packageMaps.Values | Sort-Object Id)) {
        $id = [string]$map.Id
        $targetDir = Join-Path $destinationFull $id
        $dataFile = [string](Get-JsonProperty $map.Output @('data_file'))
        $width = [int](Get-JsonProperty $map.Output @('width'))
        $height = [int](Get-JsonProperty $map.Output @('height'))
        $xmlPath = Join-Path $targetDir 'CustomHeightmapList.xml'
        if ($records.ContainsKey($id)) {
            $record = $records[$id]
            if (-not (Test-XmlState $xmlPath $record.InstalledMapId $dataFile $width $height $record.Corners)) {
                Backup-FileOnce $xmlPath ($xmlPath + '.before-geographic.bak')
                $document = New-HeightmapXmlDocument $record.InstalledMapId $dataFile $true $width $height $record.Corners
                Write-XmlAtomic $document $xmlPath
                $rewritten++
            }
            Assert-XmlState $xmlPath $record.InstalledMapId $dataFile $width $height $record.Corners
        }
        else {
            $anchor = $atlas.Anchors[$id]
            if ($null -eq $anchor) { throw "Atlas references has no anchor for $id" }
            $corners = Get-AtlasCorners $map $anchor
            $atlasId = $script:AtlasMapIdPrefix + $id
            if (-not (Test-XmlState $xmlPath $atlasId $dataFile $width $height $corners)) {
                $existingId = ''
                try {
                    [xml]$existingXml = Get-Content -LiteralPath $xmlPath -Raw -Encoding UTF8
                    $existingNode = Get-SingleHeightmapNode $existingXml $xmlPath
                    $existingId = [string]$existingNode.ID
                }
                catch { $existingId = '' }
                if ($existingId -ne $atlasId -and $existingId -ne ($script:MapIdPrefix + $id)) {
                    throw "CustomHeightmapList.xml for $id is not owned by this installer; refusing to replace it: $xmlPath"
                }
                Backup-FileOnce $xmlPath ($xmlPath + '.before-geographic-restore.bak')
                $document = New-HeightmapXmlDocument $atlasId $dataFile $true $width $height $corners
                Write-XmlAtomic $document $xmlPath
                $rewritten++
            }
            Assert-XmlState $xmlPath $atlasId $dataFile $width $height $corners
        }
    }

    $reference = Get-JsonProperty $manifest @('reference')
    if ($null -eq $reference) {
        $reference = [pscustomobject]@{}
        Set-JsonProperty $manifest 'reference' $reference
    }
    Set-JsonProperty $manifest 'layout' $script:GeoLayoutName
    Set-JsonProperty $reference 'layout' $script:GeoLayoutName
    Set-JsonProperty $reference 'geo_installer' $script:GeoInstallerName
    Set-JsonProperty $reference 'geo_references' $geo.Path
    Set-JsonProperty $reference 'geo_references_sha256' $geo.Sha256
    Set-JsonProperty $reference 'geo_references_bytes' $geo.Bytes
    Set-JsonProperty $reference 'geo_repository' $geo.Repository
    Set-JsonProperty $reference 'geo_commit' $geo.Commit
    Set-JsonProperty $reference 'geo_schema' 1

    $enabledCount = 0
    $disabledCount = 0
    foreach ($map in ($packageMaps.Values | Sort-Object Id)) {
        $id = [string]$map.Id
        $entry = $mapEntries[$id]
        $targetDir = Join-Path $destinationFull $id
        $xmlPath = Join-Path $targetDir 'CustomHeightmapList.xml'
        $xmlHash = Get-Sha256Hex $xmlPath
        $xmlBytes = [long](Get-Item -LiteralPath $xmlPath).Length
        Set-JsonProperty $entry 'xml_sha256' $xmlHash
        Set-JsonProperty $entry 'xml_bytes' $xmlBytes
        if ($records.ContainsKey($id)) {
            $record = $records[$id]
            $centerLatitude = ($record.CornerBox.MinLatitude + $record.CornerBox.MaxLatitude) / 2.0
            $centerLongitude = ($record.CornerBox.MinLongitude + $record.CornerBox.MaxLongitude) / 2.0
            Set-JsonProperty $entry 'new_map_id' $record.InstalledMapId
            Set-JsonProperty $entry 'installed_map_id' $record.InstalledMapId
            Set-JsonProperty $entry 'anchor' ([pscustomobject]@{ latitude = $centerLatitude; longitude = $centerLongitude })
            Set-JsonProperty $entry 'anchor_latitude' $centerLatitude
            Set-JsonProperty $entry 'anchor_longitude' $centerLongitude
            $cornerManifest = [ordered]@{}
            foreach ($corner in $record.Corners) {
                $cornerManifest[$corner.Name] = [ordered]@{ longitude = $corner.Longitude; latitude = $corner.Latitude }
            }
            Set-JsonProperty $entry 'geographic' ([pscustomobject][ordered]@{
                enabled = $true
                local_bounds = @($record.Entry.LocalBounds)
                geo_bounds = @($record.Entry.GeoBounds)
                sample_bounds_m = @($record.Bounds)
                uncertainty_km = $record.Entry.UncertaintyKm
                confidence = $record.Entry.Confidence
                method = $record.Entry.Method
                source = $record.Entry.Source
                corners = $cornerManifest
            })
            $enabledCount++
        }
        else {
            $anchor = $atlas.Anchors[$id]
            if ($null -eq $anchor) { throw "Atlas references has no anchor for $id" }
            $atlasId = $script:AtlasMapIdPrefix + $id
            Set-JsonProperty $entry 'new_map_id' $atlasId
            Set-JsonProperty $entry 'installed_map_id' $atlasId
            Set-JsonProperty $entry 'anchor' ([pscustomobject]@{ latitude = $anchor.Latitude; longitude = $anchor.Longitude })
            Set-JsonProperty $entry 'anchor_latitude' $anchor.Latitude
            Set-JsonProperty $entry 'anchor_longitude' $anchor.Longitude
            $configured = $geo.Entries | Where-Object { $_.Id -ceq $id } | Select-Object -First 1
            $reason = if ($null -ne $configured -and -not [string]::IsNullOrWhiteSpace($configured.Reason)) { $configured.Reason } else { 'not configured for geographic placement' }
            Set-JsonProperty $entry 'geographic' ([pscustomobject][ordered]@{
                enabled = $false
                reason = $reason
            })
            $disabledCount++
        }
    }
    Set-JsonProperty $manifest 'installed_utc' ([DateTime]::UtcNow.ToString('o', [Globalization.CultureInfo]::InvariantCulture))
    Set-JsonProperty $reference 'geo_enabled_map_count' $enabledCount
    Set-JsonProperty $reference 'geo_disabled_map_count' $disabledCount

    $manifestText = $manifest | ConvertTo-Json -Depth 12
    if (Test-Path -LiteralPath $owned.Path) {
        $existingManifestText = [IO.File]::ReadAllText($owned.Path)
        if ($existingManifestText.TrimEnd() -cne $manifestText.TrimEnd()) {
            Backup-FileOnce $owned.Path ($owned.Path + '.bak')
        }
    }
    Write-TextAtomic $owned.Path $manifestText

    Write-Host ("Applied {0} geographic maps to the existing {1}-map Atlas install at {2}" -f $enabledCount, $packageMaps.Count, $destinationFull)
    Write-Host ("Atlas fallback maps: {0}; XML files rewritten this run: {1}; all data files were reused without copying." -f $disabledCount, $rewritten)
    Write-Host 'The main manifest now records layout GeographicV1 with per-map IDs, anchors, and XML hashes.'
    Write-Host 'Tacview was not started or restarted by this script.'
}

try {
    Invoke-GeographicInstaller
    if ($script:ExitOnCompletion) { exit 0 }
}
catch {
    $message = $_.Exception.Message + [string][char]10 + $_.ScriptStackTrace
    if ($script:ExitOnCompletion) {
        [Console]::Error.WriteLine($message)
        exit 1
    }
    throw
}
