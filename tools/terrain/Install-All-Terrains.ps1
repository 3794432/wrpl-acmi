[CmdletBinding()]
param(
    [string]$SourceRoot = '',
    [string]$Destination = (Join-Path $env:APPDATA 'Tacview\Data\Terrain\Custom\WarThunderMaps'),
    [string]$AtlasReferences = '',
    [double]$Latitude = 0,
    [double]$Longitude = 0,
    [double]$CenterX = 0,
    [double]$CenterZ = 0,
    [switch]$LegacyCoordinates,
    [switch]$LegacyDoubleReference,
    [switch]$ValidateOnly,
    [switch]$AllowPartial
)

$ErrorActionPreference = 'Stop'
$script:InvocationParameters = @{}
foreach ($parameterName in $PSBoundParameters.Keys) {
    $script:InvocationParameters[$parameterName] = $PSBoundParameters[$parameterName]
}
$script:InstallerName = 'war-thunder-air-terrain'
$script:ExpectedMapCount = 68
$script:MapIdPrefix = 'WarThunder.'
$script:AtlasMapIdPrefix = 'WarThunder.Atlas1.'
$script:AtlasLayout = 'AtlasV1'
$script:LegacyLayout = 'LegacyCoordinates'
$script:AtlasLongitudeSlot = 40.0
$script:AtlasLatitudeSlot = 15.0
$script:LegacyActiveUnresolved = $false

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
    $settings.NewLineChars = "`r`n"
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

function Copy-FileAtomic {
    param(
        [string]$Source,
        [string]$Destination,
        [string]$ExpectedSha256,
        [long]$ExpectedBytes
    )

    if (Test-Path -LiteralPath $Destination) {
        $existing = Get-Item -LiteralPath $Destination
        if ($existing.PSIsContainer) { throw "Destination is a directory: $Destination" }
        if ($existing.Length -eq $ExpectedBytes -and (Get-Sha256Hex $Destination) -eq $ExpectedSha256) {
            return 'reused'
        }
    }

    $temp = $Destination + '.tmp-' + $PID + '-' + [Guid]::NewGuid().ToString('N')
    try {
        Copy-Item -LiteralPath $Source -Destination $temp -Force
        $copy = Get-Item -LiteralPath $temp
        if ($copy.Length -ne $ExpectedBytes) { throw "Size changed while copying $Source" }
        if ((Get-Sha256Hex $temp) -ne $ExpectedSha256) { throw "SHA256 changed while copying $Source" }
        Move-Item -LiteralPath $temp -Destination $Destination -Force
        return 'copied'
    }
    finally {
        if (Test-Path -LiteralPath $temp) { Remove-Item -LiteralPath $temp -Force }
    }
}

function Get-CornerCoordinates {
    param(
        [double[]]$Bounds,
        [double]$AnchorLatitude,
        [double]$AnchorLongitude,
        [double]$CenterXValue,
        [double]$CenterZValue,
        [bool]$Legacy
    )

    if ($Bounds.Count -ne 4) { throw 'sample_bounds_m must contain four values.' }
    $definitions = @(
        @('BottomLeft', $Bounds[0], $Bounds[1]),
        @('BottomRight', $Bounds[2], $Bounds[1]),
        @('TopRight', $Bounds[2], $Bounds[3]),
        @('TopLeft', $Bounds[0], $Bounds[3])
    )
    $result = @()
    foreach ($corner in $definitions) {
        $x = [double]$corner[1]
        $z = [double]$corner[2]
        $mapLatitude = $AnchorLatitude + ($z - $CenterZValue) / 111320.0
        if ([Math]::Abs($mapLatitude) -ge 85.0) { throw 'Choose a reference away from the poles.' }
        $mapLongitude = $AnchorLongitude + ($x - $CenterXValue) / (111320.0 * [Math]::Cos($mapLatitude * [Math]::PI / 180.0))
        if ($Legacy) {
            $mapLatitude += $AnchorLatitude
            $mapLongitude += $AnchorLongitude
        }
        if ([Math]::Abs($mapLongitude) -ge 180.0 -or [Math]::Abs($mapLatitude) -ge 90.0) {
            throw 'Reference crosses supported longitude/latitude bounds.'
        }
        $result += [pscustomobject]@{
            Name = [string]$corner[0]
            Longitude = $mapLongitude
            Latitude = $mapLatitude
        }
    }
    return $result
}

function Get-MapCorners {
    param(
        [object]$Record,
        [double]$CenterXValue = 0,
        [double]$CenterZValue = 0,
        [bool]$Legacy = $false
    )

    return @(Get-CornerCoordinates -Bounds $Record.Bounds -AnchorLatitude ([double]$Record.AnchorLatitude) -AnchorLongitude ([double]$Record.AnchorLongitude) -CenterXValue $CenterXValue -CenterZValue $CenterZValue -Legacy $Legacy)
}

function Add-TextElement {
    param([System.Xml.XmlDocument]$Document, [System.Xml.XmlNode]$Parent, [string]$Name, [string]$Value)

    $element = $Document.CreateElement($Name)
    $element.InnerText = $Value
    [void]$Parent.AppendChild($element)
    return $element
}

function New-TerrainXmlDocument {
    param([object]$Record, [object[]]$Corners)

    $document = New-Object System.Xml.XmlDocument
    [void]$document.AppendChild($document.CreateXmlDeclaration('1.0', 'utf-8', $null))
    $resources = $document.CreateElement('Resources')
    [void]$document.AppendChild($resources)
    $list = $document.CreateElement('CustomHeightmapList')
    [void]$resources.AppendChild($list)
    $node = $document.CreateElement('CustomHeightmap')
    $node.SetAttribute('Layer', 'Real World')
    $node.SetAttribute('ID', [string]$Record.InstalledMapId)
    [void]$list.AppendChild($node)
    [void](Add-TextElement $document $node 'File' $Record.DataFile)
    $visible = if ([bool]$Record.InstalledVisible) { 'true' } else { 'false' }
    [void](Add-TextElement $document $node 'Visible' $visible)
    [void](Add-TextElement $document $node 'BigEndian' '0')
    [void](Add-TextElement $document $node 'Signed' 'true')
    [void](Add-TextElement $document $node 'Width' ([string]$Record.Width))
    [void](Add-TextElement $document $node 'Height' ([string]$Record.Height))
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

function Get-SingleHeightmapNode {
    param([System.Xml.XmlDocument]$Document, [string]$Path)

    if ($null -eq $Document.DocumentElement -or $Document.DocumentElement.Name -ne 'Resources') {
        throw "Unexpected XML root in $Path"
    }
    $nodes = $Document.SelectNodes('/Resources/CustomHeightmapList/CustomHeightmap')
    if ($nodes.Count -ne 1) { throw "Expected one CustomHeightmap in $Path" }
    return $nodes.Item(0)
}

function Read-InvariantDouble {
    param([string]$Text, [string]$Label)

    $value = 0.0
    if (-not [double]::TryParse($Text, [Globalization.NumberStyles]::Float, [Globalization.CultureInfo]::InvariantCulture, [ref]$value)) {
        throw "Invalid number for $Label"
    }
    return $value
}

function Convert-AtlasNumber {
    param([object]$Value, [string]$Label)

    if ($null -eq $Value -or $Value -is [bool]) { throw "Missing or invalid atlas number for $Label" }
    if ($Value -is [string]) {
        return Read-InvariantDouble $Value $Label
    }
    try { $number = [double]$Value }
    catch { throw "Invalid atlas number for $Label" }
    if ([double]::IsNaN($number) -or [double]::IsInfinity($number)) { throw "Non-finite atlas number for $Label" }
    return $number
}

function Get-ObjectPropertyValue {
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

function Convert-AtlasCoordinatePair {
    param([object]$Value, [string]$Id)

    if ($Value -is [System.Array]) {
        $parts = @($Value)
        if ($parts.Count -ne 2) { throw "Atlas position for $Id must be [latitude, longitude]" }
        return [pscustomobject]@{
            Latitude = Convert-AtlasNumber $parts[0] "$Id latitude"
            Longitude = Convert-AtlasNumber $parts[1] "$Id longitude"
        }
    }
    if ($Value -is [pscustomobject] -or $Value -is [System.Collections.IDictionary]) {
        $position = Get-ObjectPropertyValue $Value @('position', 'coordinates', 'anchor', 'location', 'latlon', 'lat_lon')
        if ($null -ne $position) { return Convert-AtlasCoordinatePair $position $Id }
        $latitude = Get-ObjectPropertyValue $Value @('latitude', 'lat')
        $longitude = Get-ObjectPropertyValue $Value @('longitude', 'lon', 'lng')
        if ($null -eq $latitude -or $null -eq $longitude) {
            throw "Atlas entry for $Id has no latitude/longitude position"
        }
        return [pscustomobject]@{
            Latitude = Convert-AtlasNumber $latitude "$Id latitude"
            Longitude = Convert-AtlasNumber $longitude "$Id longitude"
        }
    }
    throw "Unsupported atlas position for $Id"
}

function Add-AtlasEntry {
    param([hashtable]$Entries, [string]$Id, [object]$Value, [string]$Path)

    if ($Id -notmatch '^[A-Za-z0-9_]+$') { throw "Unsafe atlas map ID in ${Path}: $Id" }
    if ($Entries.ContainsKey($Id)) { throw "Duplicate atlas map ID in ${Path}: $Id" }
    $anchor = Convert-AtlasCoordinatePair $Value $Id
    if ([Math]::Abs($anchor.Latitude) -ge 85.0) { throw "Atlas latitude for $Id must be within (-85, 85)." }
    if ([Math]::Abs($anchor.Longitude) -gt 180.0) { throw "Atlas longitude for $Id must be within [-180, 180]." }
    $Entries[$Id] = $anchor
}

function Read-AtlasReferences {
    param([string]$Path)

    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "Atlas references file is missing: $Path. Pass -AtlasReferences or use -LegacyCoordinates."
    }
    try {
        $rawText = Get-Content -LiteralPath $Path -Raw -Encoding UTF8
        $root = $rawText | ConvertFrom-Json
    }
    catch { throw "Invalid atlas references JSON in ${Path}: $($_.Exception.Message)" }

    $container = $root
    $mapsValue = Get-ObjectPropertyValue $root @('maps', 'references')
    $topLevelArray = $rawText.TrimStart().StartsWith('[')
    $singleRootEntry = $false
    if ($null -eq $mapsValue -and -not ($root -is [System.Array])) {
        $rootId = Get-ObjectPropertyValue $root @('id', 'map', 'map_id', 'mapId')
        $rootPosition = Get-ObjectPropertyValue $root @('position', 'coordinates', 'anchor', 'location', 'latlon', 'lat_lon')
        $rootLatitude = Get-ObjectPropertyValue $root @('latitude', 'lat')
        $rootLongitude = Get-ObjectPropertyValue $root @('longitude', 'lon', 'lng')
        if (-not [string]::IsNullOrWhiteSpace([string]$rootId) -and ($null -ne $rootPosition -or ($null -ne $rootLatitude -and $null -ne $rootLongitude))) {
            $singleRootEntry = $true
        }
    }
    $usingRootProperties = $null -eq $mapsValue -and -not $topLevelArray -and -not $singleRootEntry
    if ($topLevelArray -and -not ($root -is [System.Array])) {
        $container = @($root)
    }
    elseif ($singleRootEntry) {
        $container = @($root)
    }
    elseif (-not $usingRootProperties) {
        $container = $mapsValue
        $entriesValue = Get-ObjectPropertyValue $container @('entries', 'items')
        if ($null -ne $entriesValue) { $container = $entriesValue }
    }

    $entries = @{}
    if ($container -is [System.Array]) {
        foreach ($item in @($container)) {
            $id = [string](Get-ObjectPropertyValue $item @('id', 'map', 'map_id', 'mapId'))
            if ([string]::IsNullOrWhiteSpace($id)) { throw "Atlas entry in ${Path} has no map ID" }
            $value = Get-ObjectPropertyValue $item @('position', 'coordinates', 'anchor', 'location', 'latlon', 'lat_lon')
            if ($null -eq $value) {
                $latitude = Get-ObjectPropertyValue $item @('latitude', 'lat')
                $longitude = Get-ObjectPropertyValue $item @('longitude', 'lon', 'lng')
                if ($null -eq $latitude -or $null -eq $longitude) {
                    throw "Atlas entry for $id has no latitude/longitude position"
                }
                $value = @($latitude, $longitude)
            }
            Add-AtlasEntry $entries $id $value $Path
        }
    }
    elseif ($container -is [pscustomobject] -or $container -is [System.Collections.IDictionary]) {
        $metadataNames = @('schema', 'schema_version', 'layout', 'columns', 'rows', 'generated', 'generated_utc', 'generated_at', 'map_count', 'version', 'source', 'description', 'epoch', 'grid', 'step')
        if ($container -is [System.Collections.IDictionary]) {
            $pairs = @($container.GetEnumerator())
        }
        else {
            $pairs = @($container.PSObject.Properties | ForEach-Object { [pscustomobject]@{ Key = $_.Name; Value = $_.Value } })
        }
        foreach ($pair in $pairs) {
            $id = [string]$pair.Key
            if ($usingRootProperties -and ($metadataNames -contains $id)) { continue }
            Add-AtlasEntry $entries $id $pair.Value $Path
        }
    }
    else {
        throw "Atlas references JSON must be an object or array: $Path"
    }

    if ($entries.Count -eq 0) { throw "Atlas references contains no map positions: $Path" }
    $fullPath = Resolve-FullPath $Path
    return [pscustomobject]@{
        Entries = $entries
        Path = $fullPath
        Sha256 = (Get-Sha256Hex $fullPath)
        Bytes = [long](Get-Item -LiteralPath $fullPath).Length
    }
}

function Assert-SourceXml {
    param([object]$Record, [System.Xml.XmlNode]$Node)

    if ([string]$Node.ID -cne ($script:MapIdPrefix + $Record.Id)) { throw "XML ID mismatch for $($Record.Id)" }
    if ([string]$Node.File -cne $Record.DataFile) { throw "XML File mismatch for $($Record.Id)" }
    if ([int]$Node.Width -ne $Record.Width -or [int]$Node.Height -ne $Record.Height) { throw "XML dimensions mismatch for $($Record.Id)" }
    if ([string]$Node.BigEndian -ne '0' -or [string]$Node.Signed -ne 'true') { throw "XML byte order/sign mismatch for $($Record.Id)" }
    if ([string]$Node.AltitudeFactor -ne '1' -or [string]$Node.AltitudeOffset -ne '0') { throw "XML altitude conversion mismatch for $($Record.Id)" }
    if ([string]$Node.Projection -ne 'Quad') { throw "XML projection mismatch for $($Record.Id)" }

    $expected = Get-CornerCoordinates -Bounds $Record.Bounds -AnchorLatitude 0 -AnchorLongitude 0 -CenterXValue 0 -CenterZValue 0 -Legacy $false
    foreach ($corner in $expected) {
        $element = $Node.SelectSingleNode($corner.Name)
        if ($null -eq $element) { throw "XML is missing $($corner.Name) for $($Record.Id)" }
        $actualLongitude = Read-InvariantDouble ([string]$element.Longitude) "$($Record.Id) $($corner.Name) Longitude"
        $actualLatitude = Read-InvariantDouble ([string]$element.Latitude) "$($Record.Id) $($corner.Name) Latitude"
        if ([Math]::Abs($actualLongitude - $corner.Longitude) -gt 1e-8 -or [Math]::Abs($actualLatitude - $corner.Latitude) -gt 1e-8) {
            throw "Source coordinates are not the expected 0/0 reference for $($Record.Id) $($corner.Name)"
        }
    }
}

function Assert-InstalledXml {
    param([string]$Path, [object]$Record, [object[]]$ExpectedCorners)

    [xml]$document = Get-Content -LiteralPath $Path -Raw -Encoding UTF8
    $node = Get-SingleHeightmapNode $document $Path
    if ([string]$node.Layer -ne 'Real World') { throw "Installed XML Layer mismatch for $($Record.Id)" }
    if ([string]$node.ID -cne ([string]$Record.InstalledMapId)) { throw "Installed XML ID mismatch for $($Record.Id)" }
    if ([string]$node.File -cne $Record.DataFile) { throw "Installed XML File mismatch for $($Record.Id)" }
    $expectedVisible = if ([bool]$Record.InstalledVisible) { 'true' } else { 'false' }
    if ([string]$node.Visible -ne $expectedVisible) { throw "Installed XML Visible is not $expectedVisible for $($Record.Id)" }
    if ([string]$node.BigEndian -ne '0' -or [string]$node.Signed -ne 'true') { throw "Installed XML byte order/sign mismatch for $($Record.Id)" }
    if ([string]$node.AltitudeFactor -ne '1' -or [string]$node.AltitudeOffset -ne '0') { throw "Installed XML altitude conversion mismatch for $($Record.Id)" }
    if ([string]$node.Projection -ne 'Quad') { throw "Installed XML projection mismatch for $($Record.Id)" }
    if ([int]$node.Width -ne $Record.Width -or [int]$node.Height -ne $Record.Height) { throw "Installed XML dimensions mismatch for $($Record.Id)" }
    foreach ($corner in $ExpectedCorners) {
        $element = $node.SelectSingleNode($corner.Name)
        if ($null -eq $element) { throw "Installed XML is missing $($corner.Name) for $($Record.Id)" }
        $longitude = Read-InvariantDouble ([string]$element.Longitude) "$($Record.Id) installed Longitude"
        $latitude = Read-InvariantDouble ([string]$element.Latitude) "$($Record.Id) installed Latitude"
        if ([Math]::Abs($longitude - $corner.Longitude) -gt 1e-9 -or [Math]::Abs($latitude - $corner.Latitude) -gt 1e-9) {
            throw "Installed coordinates do not match requested reference for $($Record.Id) $($corner.Name)"
        }
    }
}

function Test-InstalledXmlMatches {
    param([string]$Path, [object]$Record, [object[]]$ExpectedCorners)

    try {
        Assert-InstalledXml $Path $Record $ExpectedCorners
        return $true
    }
    catch {
        return $false
    }
}

function Assert-AtlasGeometry {
    param([object[]]$Records)

    $boxes = New-Object System.Collections.ArrayList
    foreach ($record in $Records) {
        $corners = @(Get-MapCorners -Record $record)
        $minimumLatitude = [double]::PositiveInfinity
        $maximumLatitude = [double]::NegativeInfinity
        $minimumLongitude = [double]::PositiveInfinity
        $maximumLongitude = [double]::NegativeInfinity
        foreach ($corner in $corners) {
            $minimumLatitude = [Math]::Min($minimumLatitude, [double]$corner.Latitude)
            $maximumLatitude = [Math]::Max($maximumLatitude, [double]$corner.Latitude)
            $minimumLongitude = [Math]::Min($minimumLongitude, [double]$corner.Longitude)
            $maximumLongitude = [Math]::Max($maximumLongitude, [double]$corner.Longitude)
        }
        $longitudeSpan = $maximumLongitude - $minimumLongitude
        $latitudeSpan = $maximumLatitude - $minimumLatitude
        if ($longitudeSpan -ge $script:AtlasLongitudeSlot) {
            throw ("Atlas longitude span for {0} is {1:F6} degrees; it must be less than the {2:F1}-degree slot interval." -f $record.Id, $longitudeSpan, $script:AtlasLongitudeSlot)
        }
        if ($latitudeSpan -ge $script:AtlasLatitudeSlot) {
            throw ("Atlas latitude span for {0} is {1:F6} degrees; it must be less than the {2:F1}-degree slot interval." -f $record.Id, $latitudeSpan, $script:AtlasLatitudeSlot)
        }
        if ($minimumLongitude -le -180.0 -or $maximumLongitude -ge 180.0 -or $minimumLatitude -le -85.0 -or $maximumLatitude -ge 85.0) {
            throw "Atlas corners for $($record.Id) cross a supported geographic boundary."
        }
        $box = [pscustomobject]@{
            Id = $record.Id
            MinLatitude = $minimumLatitude
            MaxLatitude = $maximumLatitude
            MinLongitude = $minimumLongitude
            MaxLongitude = $maximumLongitude
        }
        $record | Add-Member -NotePropertyName CornerBBox -NotePropertyValue $box -Force
        [void]$boxes.Add($box)
    }

    for ($first = 0; $first -lt $boxes.Count; $first++) {
        for ($second = $first + 1; $second -lt $boxes.Count; $second++) {
            $a = $boxes[$first]
            $b = $boxes[$second]
            $overlaps = $a.MinLongitude -lt $b.MaxLongitude -and $b.MinLongitude -lt $a.MaxLongitude -and
                $a.MinLatitude -lt $b.MaxLatitude -and $b.MinLatitude -lt $a.MaxLatitude
            if ($overlaps) {
                throw "Atlas corner bounding boxes overlap: $($a.Id) and $($b.Id)"
            }
        }
    }
}

function Backup-FileOnce {
    param([string]$Path, [string]$BackupPath)

    if (-not (Test-Path -LiteralPath $Path)) { return }
    if (Test-Path -LiteralPath $BackupPath) { return }
    Copy-Item -LiteralPath $Path -Destination $BackupPath
}

function Disable-OwnedLegacyActive {
    param([string]$DestinationRoot, [object[]]$Records)

    $parent = Split-Path -Path $DestinationRoot -Parent
    $legacyRoot = Join-Path $parent 'WarThunderActive'
    if (-not (Test-Path -LiteralPath $legacyRoot)) { return }
    if ((Resolve-FullPath $legacyRoot) -eq $DestinationRoot) { return }

    $xmlPath = Join-Path $legacyRoot 'CustomHeightmapList.xml'
    $jsonPath = Join-Path $legacyRoot 'active.json'
    $dataPath = Join-Path $legacyRoot 'active.data'
    if (-not (Test-Path -LiteralPath $jsonPath) -or -not (Test-Path -LiteralPath $dataPath)) {
        $script:LegacyActiveUnresolved = $true
        Write-Warning "WarThunderActive exists but is not recognized as this package's legacy active map; left untouched: $legacyRoot"
        return
    }

    try {
        $active = Get-Content -LiteralPath $jsonPath -Raw -Encoding UTF8 | ConvertFrom-Json
    }
    catch {
        $script:LegacyActiveUnresolved = $true
        Write-Warning "WarThunderActive active.json is invalid; left untouched: $legacyRoot"
        return
    }
    $legacyId = [string]$active.map
    $legacyHash = ([string]$active.sha256).ToLowerInvariant()
    if ($legacyId -notmatch '^[A-Za-z0-9_]+$' -or $legacyHash -notmatch '^[0-9a-f]{64}$') {
        $script:LegacyActiveUnresolved = $true
        Write-Warning "WarThunderActive does not contain a recognized map/id/hash; left untouched: $legacyRoot"
        return
    }
    $record = $Records | Where-Object { $_.Id -ceq $legacyId } | Select-Object -First 1
    if ($null -eq $record) {
        $script:LegacyActiveUnresolved = $true
        Write-Warning "WarThunderActive references $legacyId, which is not in this package; left untouched: $legacyRoot"
        return
    }

    $dataItem = Get-Item -LiteralPath $dataPath
    if ($dataItem.PSIsContainer) {
        $script:LegacyActiveUnresolved = $true
        Write-Warning "WarThunderActive active.data is not a file; left untouched: $legacyRoot"
        return
    }
    if ((Get-Sha256Hex $dataPath) -ne $legacyHash) {
        $script:LegacyActiveUnresolved = $true
        Write-Warning "WarThunderActive active.data hash does not match active.json; left untouched: $legacyRoot"
        return
    }
    if (-not (Test-Path -LiteralPath $xmlPath)) {
        Write-Host "Legacy WarThunderActive is already disabled: $legacyRoot"
        return
    }

    try {
        [xml]$legacyXml = Get-Content -LiteralPath $xmlPath -Raw -Encoding UTF8
        $legacyNode = Get-SingleHeightmapNode $legacyXml $xmlPath
    }
    catch {
        $script:LegacyActiveUnresolved = $true
        Write-Warning "WarThunderActive XML is invalid; left untouched: $legacyRoot"
        return
    }
    if ([string]$legacyNode.ID -cne ($script:MapIdPrefix + $legacyId) -or [string]$legacyNode.File -cne 'active.data') {
        $script:LegacyActiveUnresolved = $true
        Write-Warning "WarThunderActive XML is not recognized as this package's legacy map; left untouched: $legacyRoot"
        return
    }

    $jsonBackup = $jsonPath + '.warthundermaps-backup'
    $xmlBackup = $xmlPath + '.warthundermaps-backup'
    if (-not (Test-Path -LiteralPath $jsonBackup)) { Copy-Item -LiteralPath $jsonPath -Destination $jsonBackup }
    if (-not (Test-Path -LiteralPath $xmlBackup)) { Copy-Item -LiteralPath $xmlPath -Destination $xmlBackup }

    foreach ($node in $legacyXml.SelectNodes('/Resources/CustomHeightmapList/CustomHeightmap')) {
        $visible = $node.SelectSingleNode('Visible')
        if ($null -eq $visible) { $visible = Add-TextElement $legacyXml $node 'Visible' 'false' }
        else { $visible.InnerText = 'false' }
    }
    Write-XmlAtomic $legacyXml $xmlPath

    $disabledPath = Join-Path $legacyRoot 'CustomHeightmapList.xml.disabled-warthundermaps'
    if (Test-Path -LiteralPath $disabledPath) {
        $disabledPath = Join-Path $legacyRoot ('CustomHeightmapList.xml.disabled-warthundermaps-' + [Guid]::NewGuid().ToString('N'))
    }
    Move-Item -LiteralPath $xmlPath -Destination $disabledPath
    Write-Host "Disabled legacy WarThunderActive XML for $legacyId (backups and active.data preserved)."
}

function Assert-NoExplicitLegacyCoordinates {
    $coordinateNames = @('Latitude', 'Longitude', 'CenterX', 'CenterZ')
    foreach ($name in $coordinateNames) {
        if ($script:InvocationParameters.ContainsKey($name)) {
            throw "-$name requires -LegacyCoordinates. The default Atlas layout takes each anchor from -AtlasReferences."
        }
    }
    if ([bool]$LegacyDoubleReference) {
        throw '-LegacyDoubleReference requires -LegacyCoordinates.'
    }
}

function Invoke-Installer {
    $legacyMode = [bool]$LegacyCoordinates
    if (-not $legacyMode) { Assert-NoExplicitLegacyCoordinates }

    if ([string]::IsNullOrWhiteSpace($SourceRoot)) { $script:SourceRoot = $PSScriptRoot }
    $sourceRootFull = Resolve-FullPath $script:SourceRoot
    $mapsRoot = Join-Path $sourceRootFull 'maps'
    if (-not (Test-Path -LiteralPath $mapsRoot)) {
        throw "No maps folder under $sourceRootFull. Pass -SourceRoot for a workspace test."
    }
    if (-not (Test-Path -LiteralPath $mapsRoot -PathType Container)) { throw "maps is not a directory: $mapsRoot" }

    if ([double]::IsNaN($Latitude) -or [double]::IsInfinity($Latitude) -or [math]::Abs($Latitude) -ge 85.0) { throw 'Latitude must be finite and within (-85, 85).' }
    if ([double]::IsNaN($Longitude) -or [double]::IsInfinity($Longitude) -or [math]::Abs($Longitude) -gt 180.0) { throw 'Longitude must be finite and within [-180, 180].' }
    if ([double]::IsNaN($CenterX) -or [double]::IsInfinity($CenterX) -or [double]::IsNaN($CenterZ) -or [double]::IsInfinity($CenterZ)) { throw 'CenterX and CenterZ must be finite.' }

    $atlas = $null
    if (-not $legacyMode) {
        $atlasPathValue = $AtlasReferences
        if ([string]::IsNullOrWhiteSpace($atlasPathValue)) { $atlasPathValue = Join-Path $sourceRootFull 'AtlasReferences.json' }
        $atlas = Read-AtlasReferences $atlasPathValue
    }

    $allDirs = @(Get-ChildItem -LiteralPath $mapsRoot -Directory -Force | Sort-Object Name)
    $mapDirs = New-Object System.Collections.ArrayList
    foreach ($candidate in $allDirs) {
        $candidateMetadata = Join-Path $candidate.FullName 'metadata.json'
        if (Test-Path -LiteralPath $candidateMetadata) {
            [void]$mapDirs.Add($candidate)
            continue
        }
        $candidateResult = Join-Path $candidate.FullName 'result.json'
        if (Test-Path -LiteralPath $candidateResult) {
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
    $mapDirs = @($mapDirs)
    if ($mapDirs.Count -eq 0) { throw "No map directories found under $mapsRoot" }
    if ($mapDirs.Count -ne $script:ExpectedMapCount -and -not $AllowPartial) {
        throw "Expected $($script:ExpectedMapCount) map directories, found $($mapDirs.Count). Use -AllowPartial only for a test fixture."
    }
    if ($null -ne $atlas -and -not $AllowPartial) {
        if ($atlas.Entries.Count -ne $script:ExpectedMapCount) {
            throw "Atlas references has $($atlas.Entries.Count) map IDs; expected $($script:ExpectedMapCount): $($atlas.Path)"
        }
        $directoryIds = @{}
        foreach ($mapDir in $mapDirs) {
            $directoryId = [string]$mapDir.Name
            if ($directoryId -notmatch '^[A-Za-z0-9_]+$') { throw "Unsafe map directory name: $directoryId" }
            if ($directoryIds.ContainsKey($directoryId)) { throw "Duplicate map ID: $directoryId" }
            $directoryIds[$directoryId] = $true
        }
        foreach ($atlasId in $atlas.Entries.Keys) {
            if (-not $directoryIds.ContainsKey($atlasId)) {
                throw "Atlas references contains unknown map ID ${atlasId}: $($atlas.Path)"
            }
        }
        foreach ($directoryId in $directoryIds.Keys) {
            if (-not $atlas.Entries.ContainsKey($directoryId)) {
                throw "Atlas references has no anchor for map ${directoryId}: $($atlas.Path)"
            }
        }
    }

    $records = New-Object System.Collections.ArrayList
    $seenIds = @{}
    $totalBytes = [long]0
    $position = 0
    foreach ($mapDir in $mapDirs) {
        $position++
        $id = [string]$mapDir.Name
        if ($id -notmatch '^[A-Za-z0-9_]+$') { throw "Unsafe map directory name: $id" }
        if ($seenIds.ContainsKey($id)) { throw "Duplicate map ID: $id" }
        $seenIds[$id] = $true

        $metadataPath = Join-Path $mapDir.FullName 'metadata.json'
        $xmlPath = Join-Path $mapDir.FullName 'CustomHeightmapList.xml'
        if (-not (Test-Path -LiteralPath $metadataPath)) { throw "Missing metadata.json in $id" }
        if (-not (Test-Path -LiteralPath $xmlPath)) { throw "Missing CustomHeightmapList.xml in $id" }

        try { $metadata = Get-Content -LiteralPath $metadataPath -Raw -Encoding UTF8 | ConvertFrom-Json }
        catch { throw "Invalid metadata.json in ${id}: $($_.Exception.Message)" }
        if ([string]$metadata.status -ne 'ok') { throw "Map $id is not marked ok in metadata.json" }
        if ([string]$metadata.id -cne $id) { throw "metadata.id mismatch in $id" }
        $output = $metadata.output
        if ($null -eq $output) { throw "metadata.output is missing in $id" }
        if ([string]$output.id -cne $id) { throw "metadata.output.id mismatch in $id" }
        $dataFile = [string]$output.data_file
        if ($dataFile -cne ($id + '.data')) { throw "Unexpected data_file in $id" }
        $width = [int]$output.width
        $height = [int]$output.height
        if ($width -le 0 -or $height -le 0) { throw "Invalid dimensions in $id" }
        $dataBytes = [long]$output.data_bytes
        if ($dataBytes -ne ([long]$width * [long]$height * 2)) { throw "data_bytes does not match dimensions in $id" }
        $expectedHash = ([string]$output.data_sha256).ToLowerInvariant()
        if ($expectedHash -notmatch '^[0-9a-f]{64}$') { throw "Invalid data_sha256 in $id" }
        $bounds = @($output.sample_bounds_m | ForEach-Object { [double]$_ })
        if ($bounds.Count -ne 4) { throw "Invalid sample_bounds_m in $id" }

        $dataPath = Join-Path $mapDir.FullName $dataFile
        if (-not (Test-Path -LiteralPath $dataPath)) { throw "Missing data file in $id" }
        $dataItem = Get-Item -LiteralPath $dataPath
        if ($dataItem.PSIsContainer -or $dataItem.Length -ne $dataBytes) { throw "Data size mismatch in $id" }

        try { [xml]$sourceXml = Get-Content -LiteralPath $xmlPath -Raw -Encoding UTF8 }
        catch { throw "Invalid XML in ${id}: $($_.Exception.Message)" }
        $sourceNode = Get-SingleHeightmapNode $sourceXml $xmlPath
        $record = [pscustomobject]@{
            Id = $id
            Folder = $mapDir.FullName
            DataFile = $dataFile
            DataPath = $dataPath
            DataBytes = $dataBytes
            DataHash = $expectedHash
            MetadataPath = $metadataPath
            XmlPath = $xmlPath
            Width = $width
            Height = $height
            Bounds = $bounds
        }
        Assert-SourceXml $record $sourceNode

        $actualHash = Get-Sha256Hex $dataPath
        if ($actualHash -ne $expectedHash) { throw "Data SHA256 mismatch in $id" }
        $metadataHash = Get-Sha256Hex $metadataPath
        $record | Add-Member -NotePropertyName MetadataHash -NotePropertyValue $metadataHash
        $record | Add-Member -NotePropertyName MetadataBytes -NotePropertyValue ([long](Get-Item -LiteralPath $metadataPath).Length)
        [void]$records.Add($record)
        $totalBytes += $dataBytes
        Write-Host ("[{0}/{1}] verified {2} ({3:N1} MiB)" -f $position, $mapDirs.Count, $id, ($dataBytes / 1MB))
    }

    if ($records.Count -ne $script:ExpectedMapCount -and -not $AllowPartial) {
        throw "Verified $($records.Count) maps; expected $($script:ExpectedMapCount)."
    }
    Write-Host ("Verified {0} maps, {1:N2} GiB of elevation data." -f $records.Count, ($totalBytes / 1GB))

    if ($null -ne $atlas) {
        foreach ($record in $records) {
            if (-not $atlas.Entries.ContainsKey($record.Id)) {
                throw "Atlas references has no anchor for map $($record.Id): $($atlas.Path)"
            }
            $anchor = $atlas.Entries[$record.Id]
            $record | Add-Member -NotePropertyName AnchorLatitude -NotePropertyValue ([double]$anchor.Latitude)
            $record | Add-Member -NotePropertyName AnchorLongitude -NotePropertyValue ([double]$anchor.Longitude)
            $record | Add-Member -NotePropertyName InstalledMapId -NotePropertyValue ($script:AtlasMapIdPrefix + $record.Id)
            $record | Add-Member -NotePropertyName InstalledVisible -NotePropertyValue $true
        }
        if (-not $AllowPartial) {
            foreach ($atlasId in $atlas.Entries.Keys) {
                if (-not $seenIds.ContainsKey($atlasId)) {
                    throw "Atlas references contains unknown map ID ${atlasId}: $($atlas.Path)"
                }
            }
            if ($atlas.Entries.Count -ne $records.Count) {
                throw "Atlas references has $($atlas.Entries.Count) map IDs; verified $($records.Count)."
            }
        }
        Assert-AtlasGeometry $records
    }
    else {
        foreach ($record in $records) {
            $record | Add-Member -NotePropertyName AnchorLatitude -NotePropertyValue $Latitude
            $record | Add-Member -NotePropertyName AnchorLongitude -NotePropertyValue $Longitude
            $record | Add-Member -NotePropertyName InstalledMapId -NotePropertyValue ($script:MapIdPrefix + $record.Id)
            $record | Add-Member -NotePropertyName InstalledVisible -NotePropertyValue $false
        }
    }

    if ($ValidateOnly) {
        Write-Host 'Validation-only run complete; no files were written.'
        return
    }

    $destinationFull = Resolve-FullPath $Destination
    if (Test-Path -LiteralPath $destinationFull) {
        if (-not (Get-Item -LiteralPath $destinationFull).PSIsContainer) { throw "Destination is not a directory: $destinationFull" }
        $manifestPath = Join-Path $destinationFull 'WarThunderMaps.install.json'
        if (Test-Path -LiteralPath $manifestPath) {
            try { $oldManifest = Get-Content -LiteralPath $manifestPath -Raw -Encoding UTF8 | ConvertFrom-Json }
            catch { throw "Existing install manifest is invalid: $manifestPath" }
            if ([string]$oldManifest.installer -ne $script:InstallerName) {
                throw "Destination contains an unknown WarThunderMaps.install.json; refusing to take it over: $destinationFull"
            }
        }
    }
    else {
        New-Item -ItemType Directory -Path $destinationFull -Force | Out-Null
    }

    foreach ($record in $records) {
        $targetDir = Join-Path $destinationFull $record.Id
        if (Test-Path -LiteralPath $targetDir) {
            if (-not (Get-Item -LiteralPath $targetDir).PSIsContainer) { throw "Map target is not a directory: $targetDir" }
        }
    }

    Disable-OwnedLegacyActive $destinationFull $records

    $manifestMaps = New-Object System.Collections.ArrayList
    $copied = 0
    $reused = 0
    $installedBytes = [long]0
    $position = 0
    foreach ($record in $records) {
        $position++
        $targetDir = Join-Path $destinationFull $record.Id
        if (-not (Test-Path -LiteralPath $targetDir)) { New-Item -ItemType Directory -Path $targetDir | Out-Null }
        $targetData = Join-Path $targetDir $record.DataFile
        $state = Copy-FileAtomic $record.DataPath $targetData $record.DataHash $record.DataBytes
        if ($state -eq 'reused') { $reused++ } else { $copied++; $installedBytes += $record.DataBytes }

        $targetMetadata = Join-Path $targetDir 'metadata.json'
        [void](Copy-FileAtomic $record.MetadataPath $targetMetadata $record.MetadataHash $record.MetadataBytes)

        $corners = Get-MapCorners -Record $record -CenterXValue $CenterX -CenterZValue $CenterZ -Legacy $legacyMode
        $targetXml = Join-Path $targetDir 'CustomHeightmapList.xml'
        if (Test-Path -LiteralPath $targetXml -PathType Container) { throw "Map target XML is a directory: $targetXml" }
        if (-not (Test-Path -LiteralPath $targetXml) -or -not (Test-InstalledXmlMatches $targetXml $record $corners)) {
            if (Test-Path -LiteralPath $targetXml) {
                Backup-FileOnce $targetXml ($targetXml + '.before-atlas.bak')
            }
            $xml = New-TerrainXmlDocument $record $corners
            Write-XmlAtomic $xml $targetXml
        }
        Assert-InstalledXml $targetXml $record $corners

        [void]$manifestMaps.Add([ordered]@{
            id = $record.Id
            map_id = ($script:MapIdPrefix + $record.Id)
            new_map_id = $record.InstalledMapId
            installed_map_id = $record.InstalledMapId
            anchor = [ordered]@{
                latitude = $record.AnchorLatitude
                longitude = $record.AnchorLongitude
            }
            anchor_latitude = $record.AnchorLatitude
            anchor_longitude = $record.AnchorLongitude
            data_file = $record.DataFile
            data_bytes = $record.DataBytes
            data_sha256 = $record.DataHash
            xml_sha256 = (Get-Sha256Hex $targetXml)
        })
        Write-Host ("[{0}/{1}] installed {2} as {3} (data {4})" -f $position, $records.Count, $record.Id, $record.InstalledMapId, $state)
    }

    $layout = if ($legacyMode) { $script:LegacyLayout } else { $script:AtlasLayout }
    $reference = [ordered]@{
        layout = $layout
        latitude = $Latitude
        longitude = $Longitude
        center_x = $CenterX
        center_z = $CenterZ
        legacy_double_reference = [bool]$LegacyDoubleReference
    }
    if ($null -ne $atlas) {
        $reference.atlas_references = $atlas.Path
        $reference.atlas_references_sha256 = $atlas.Sha256
        $reference.atlas_references_bytes = $atlas.Bytes
    }
    $manifest = [ordered]@{
        schema_version = 2
        installer = $script:InstallerName
        layout = $layout
        installed_utc = [DateTime]::UtcNow.ToString('o', [Globalization.CultureInfo]::InvariantCulture)
        source_root = $sourceRootFull
        destination = $destinationFull
        reference = $reference
        map_count = $records.Count
        total_data_bytes = $totalBytes
        maps = @($manifestMaps)
    }
    $manifestPath = Join-Path $destinationFull 'WarThunderMaps.install.json'
    $manifestText = $manifest | ConvertTo-Json -Depth 8
    if (Test-Path -LiteralPath $manifestPath) {
        $existingManifestText = [IO.File]::ReadAllText($manifestPath)
        if ($existingManifestText.TrimEnd() -cne $manifestText.TrimEnd()) {
            Backup-FileOnce $manifestPath ($manifestPath + '.bak')
        }
    }
    Write-TextAtomic $manifestPath $manifestText

    Write-Host ("Installed {0} maps to {1}" -f $records.Count, $destinationFull)
    Write-Host ("Data files copied: {0}; reused by matching size/SHA256: {1}; new bytes: {2:N2} GiB" -f $copied, $reused, ($installedBytes / 1GB))
    if (-not $legacyMode) {
        Write-Host ("Layout: AtlasV1; anchors from {0}" -f $atlas.Path)
        Write-Host 'Every XML declares Visible=true with a unique WarThunder.Atlas1 ID. No add-on or MapId switch is required.'
    }
    else {
        Write-Host ("Layout: LegacyCoordinates; latitude={0} longitude={1} center=({2},{3}) legacy_double_reference={4}" -f $Latitude, $Longitude, $CenterX, $CenterZ, [bool]$LegacyDoubleReference)
        Write-Host 'Every XML declares Visible=false with the old WarThunder ID.'
        Write-Host 'Restart Tacview, then enable Advanced Options > Auto Terrain Switch once.'
    }
    Write-Host 'Restart Tacview after changing the installed terrain set.'
    if ($script:LegacyActiveUnresolved) {
        Write-Warning 'WarThunderActive could not be proven to belong to this package. It was NOT modified and may still be enabled, so it can overlap or override the new WarThunderMaps terrain. Manually inspect and disable its CustomHeightmapList.xml before loading maps.'
    }
}

try {
    Invoke-Installer
    exit 0
}
catch {
    Write-Error ($_.Exception.Message + "`n" + $_.ScriptStackTrace)
    exit 1
}
