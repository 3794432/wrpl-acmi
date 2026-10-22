$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2.0

$script:TerrainFormatOwner = 'wrpl-acmi-terrain-format-v1'
$script:TerrainCacheOwner = 'wrpl-acmi-terrain-uint16-cache-v1'
$script:TerrainCacheEncoding = 'uint16_le_bias32768'
$script:TerrainInstallerName = 'war-thunder-air-terrain'
$script:TerrainStateName = 'WarThunderMaps.format.json'
$script:TerrainNativeName = 'CustomHeightmapList.native195'
$script:TerrainXmlName = 'CustomHeightmapList.xml'
$script:TerrainBackupName = 'CustomHeightmapList.xml.before-terrain-format.bak'
$script:TerrainUtf8 = New-Object Text.UTF8Encoding($false)
$script:TerrainInvariant = [Globalization.CultureInfo]::InvariantCulture

$encodingModule = Join-Path $PSScriptRoot 'Terrain-194-Encoding.ps1'
if (-not (Test-Path -LiteralPath $encodingModule -PathType Leaf)) {
    throw "Terrain format dependency is missing: $encodingModule"
}
. $encodingModule

function Get-TerrainProperty {
    param([object]$Object, [string]$Name)
    if ($null -eq $Object) { return $null }
    if ($Object -is [System.Collections.IDictionary]) {
        if ($Object.Contains($Name)) { return $Object[$Name] }
        return $null
    }
    $property = $Object.PSObject.Properties[$Name]
    if ($null -eq $property) { return $null }
    return $property.Value
}

function Assert-TerrainMapId {
    param([string]$Id)
    if ($Id -cnotmatch '^[a-z0-9_]+$') { throw "Unsafe terrain map ID: $Id" }
}

function Test-TerrainReparsePoint {
    param([string]$Path, [string]$Label)
    if (-not (Test-Path -LiteralPath $Path)) { return }
    $item = Get-Item -LiteralPath $Path -Force
    if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw "$Label must not be a reparse point: $Path"
    }
}

function Assert-TerrainDirectory {
    param([string]$Path, [string]$Label)
    if (-not (Test-Path -LiteralPath $Path)) { return }
    $item = Get-Item -LiteralPath $Path -Force
    if (-not $item.PSIsContainer) { throw "$Label is not a directory: $Path" }
    Test-TerrainReparsePoint $Path $Label
}

function Get-TerrainSha256 {
    param([string]$Path)
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

function Get-TerrainTextSha256 {
    param([string]$Text)
    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        $bytes = $script:TerrainUtf8.GetBytes($Text)
        return [BitConverter]::ToString($sha.ComputeHash($bytes)).Replace('-', '').ToLowerInvariant()
    }
    finally { $sha.Dispose() }
}

function Write-TerrainTextAtomic {
    param([string]$Path, [string]$Text)
    $directory = [IO.Path]::GetDirectoryName($Path)
    $temp = Join-Path $directory ('.terrain-' + [Guid]::NewGuid().ToString('N') + '.tmp')
    try {
        [IO.File]::WriteAllText($temp, $Text, $script:TerrainUtf8)
        if (Test-Path -LiteralPath $Path) {
            Test-TerrainReparsePoint $Path 'Terrain output file'
            $backup = $temp + '.bak'
            try { [IO.File]::Replace($temp, $Path, $backup, $true) }
            finally { if (Test-Path -LiteralPath $backup -PathType Leaf) { [IO.File]::Delete($backup) } }
        }
        else { [IO.File]::Move($temp, $Path) }
    }
    finally {
        if (Test-Path -LiteralPath $temp -PathType Leaf) { [IO.File]::Delete($temp) }
    }
}

function Copy-TerrainFileAtomic {
    param([string]$Source, [string]$Destination)
    $directory = [IO.Path]::GetDirectoryName($Destination)
    $temp = Join-Path $directory ('.terrain-' + [Guid]::NewGuid().ToString('N') + '.tmp')
    try {
        [IO.File]::Copy($Source, $temp, $false)
        [IO.File]::Move($temp, $Destination)
    }
    finally {
        if (Test-Path -LiteralPath $temp -PathType Leaf) { [IO.File]::Delete($temp) }
    }
}

function Write-TerrainXmlAtomic {
    param([System.Xml.XmlDocument]$Document, [string]$Path)
    $directory = [IO.Path]::GetDirectoryName($Path)
    $temp = Join-Path $directory ('.terrain-' + [Guid]::NewGuid().ToString('N') + '.xml')
    $settings = New-Object System.Xml.XmlWriterSettings
    $settings.Indent = $true
    $settings.IndentChars = '  '
    $settings.NewLineChars = "`r`n"
    $settings.Encoding = $script:TerrainUtf8
    try {
        $writer = [System.Xml.XmlWriter]::Create($temp, $settings)
        try { $Document.Save($writer) } finally { $writer.Dispose() }
        [void](Read-TerrainXml $temp 'Temporary terrain aggregate XML')
        if (Test-Path -LiteralPath $Path) {
            Test-TerrainReparsePoint $Path 'Terrain aggregate XML'
            $backup = $temp + '.bak'
            try { [IO.File]::Replace($temp, $Path, $backup, $true) }
            finally { if (Test-Path -LiteralPath $backup -PathType Leaf) { [IO.File]::Delete($backup) } }
        }
        else { [IO.File]::Move($temp, $Path) }
    }
    finally {
        if (Test-Path -LiteralPath $temp -PathType Leaf) { [IO.File]::Delete($temp) }
    }
}

function Backup-TerrainRootOnce {
    param([string]$RootXml, [string]$Backup)
    if (-not (Test-Path -LiteralPath $RootXml -PathType Leaf)) { return }
    Test-TerrainReparsePoint $RootXml 'Terrain root XML'
    Test-TerrainReparsePoint $Backup 'Terrain root backup'
    if (-not (Test-Path -LiteralPath $Backup)) { Copy-TerrainFileAtomic $RootXml $Backup }
}

function Read-TerrainXml {
    param([string]$Path, [string]$Label)
    Test-TerrainReparsePoint $Path $Label
    $settings = New-Object System.Xml.XmlReaderSettings
    $settings.DtdProcessing = [System.Xml.DtdProcessing]::Prohibit
    $settings.XmlResolver = $null
    $settings.IgnoreWhitespace = $true
    $reader = [System.Xml.XmlReader]::Create($Path, $settings)
    try {
        $document = New-Object System.Xml.XmlDocument
        $document.XmlResolver = $null
        $document.Load($reader)
    }
    finally { $reader.Dispose() }
    if ($null -eq $document.DocumentElement -or $document.DocumentElement.NamespaceURI -ne '' -or
        $document.DocumentElement.LocalName -ne 'Resources') {
        throw "Unexpected terrain XML root in $Path"
    }
    return $document
}

function Get-TerrainHeightmapNode {
    param([System.Xml.XmlDocument]$Document, [string]$Path)
    $nodes = $Document.SelectNodes('/Resources/CustomHeightmapList/CustomHeightmap')
    if ($nodes.Count -ne 1) { throw "Expected one CustomHeightmap node in $Path" }
    return $nodes.Item(0)
}

function Get-TerrainNodeText {
    param([System.Xml.XmlNode]$Node, [string]$Name, [string]$Path)
    $items = $Node.SelectNodes($Name)
    if ($items.Count -ne 1) { throw "Expected one $Name element in $Path" }
    return [string]$items.Item(0).InnerText
}

function Set-TerrainNodeText {
    param([System.Xml.XmlDocument]$Document, [System.Xml.XmlNode]$Node, [string]$Name, [string]$Value)
    $items = $Node.SelectNodes($Name)
    if ($items.Count -eq 0) {
        $element = $Document.CreateElement($Name)
        $element.InnerText = $Value
        [void]$Node.AppendChild($element)
        return
    }
    if ($items.Count -ne 1) { throw "Unexpected duplicate $Name element" }
    $items.Item(0).InnerText = $Value
}

function Get-TerrainNodeHash {
    param([System.Xml.XmlNode]$Node)
    return Get-TerrainTextSha256 $Node.OuterXml
}

function Get-TerrainMapNodeId {
    param([System.Xml.XmlNode]$Node)
    if ($null -eq $Node.Attributes) { return '' }
    $attribute = $Node.Attributes.GetNamedItem('ID')
    if ($null -eq $attribute) { return '' }
    return [string]$attribute.Value
}

function Read-TerrainState {
    param([string]$Path)
    if (-not (Test-Path -LiteralPath $Path)) { return $null }
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { throw "Terrain format state is not a file: $Path" }
    Test-TerrainReparsePoint $Path 'Terrain format state'
    try { $state = Get-Content -LiteralPath $Path -Raw -Encoding UTF8 | ConvertFrom-Json }
    catch { throw "Invalid terrain format state: $Path" }
    if ($null -eq $state -or [string](Get-TerrainProperty $state 'owner') -cne $script:TerrainFormatOwner) {
        throw "Unknown terrain format state owner: $Path"
    }
    $profile = [string](Get-TerrainProperty $state 'profile')
    if ($profile -notin @('1.9.4', '1.9.5')) { throw "Unknown terrain format profile in state: $profile" }
    return $state
}

function Read-TerrainCache {
    param([string]$Path)
    if (-not (Test-Path -LiteralPath $Path)) {
        return [pscustomobject]@{ Exists = $false; Maps = @{} }
    }
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { throw "Terrain cache is not a file: $Path" }
    Test-TerrainReparsePoint $Path 'Terrain cache'
    try { $cache = Get-Content -LiteralPath $Path -Raw -Encoding UTF8 | ConvertFrom-Json }
    catch { throw "Invalid terrain cache: $Path" }
    if ($null -eq $cache -or [string](Get-TerrainProperty $cache 'owner') -cne $script:TerrainCacheOwner) {
        throw "Unknown terrain cache owner: $Path"
    }
    if ([string](Get-TerrainProperty $cache 'encoding') -cne $script:TerrainCacheEncoding) {
        throw "Unknown terrain cache encoding: $Path"
    }
    $maps = @{}
    $mapObject = Get-TerrainProperty $cache 'maps'
    if ($null -ne $mapObject) {
        if ($mapObject -is [System.Collections.IDictionary]) {
            foreach ($key in $mapObject.Keys) { $maps[[string]$key] = $mapObject[$key] }
        }
        else {
            foreach ($property in $mapObject.PSObject.Properties) { $maps[[string]$property.Name] = $property.Value }
        }
    }
    return [pscustomobject]@{ Exists = $true; Maps = $maps }
}

function Write-TerrainCache {
    param([string]$Path, [hashtable]$Maps)
    $output = New-Object System.Collections.Specialized.OrderedDictionary
    foreach ($id in @($Maps.Keys | Sort-Object)) {
        $item = $Maps[$id]
        $output[$id] = [ordered]@{
            source_sha256 = [string]$item.source_sha256
            source_bytes = [long]$item.source_bytes
            data_sha256 = [string]$item.data_sha256
            data_bytes = [long]$item.data_bytes
            encoding = $script:TerrainCacheEncoding
        }
    }
    $document = [ordered]@{
        schema_version = 1
        owner = $script:TerrainCacheOwner
        encoding = $script:TerrainCacheEncoding
        maps = $output
    }
    Write-TerrainTextAtomic $Path (($document | ConvertTo-Json -Depth 8) + [Environment]::NewLine)
}

function Test-TerrainAllowedXmlId {
    param([string]$XmlId, [string]$MapId)
    return ($XmlId -ceq ('WarThunder.Geo1.' + $MapId)) -or ($XmlId -ceq ('WarThunder.Atlas1.' + $MapId))
}

function Get-TerrainMapRecord {
    param([object]$ManifestItem, [string]$TerrainRoot, [string]$ManifestPath)
    $id = [string](Get-TerrainProperty $ManifestItem 'id')
    Assert-TerrainMapId $id
    $dataFile = [string](Get-TerrainProperty $ManifestItem 'data_file')
    if ($dataFile -cne ($id + '.data') -or [IO.Path]::GetFileName($dataFile) -cne $dataFile) {
        throw "Unsafe or unexpected data_file for ${id}: $dataFile"
    }
    $mapDir = Join-Path $TerrainRoot $id
    Assert-TerrainDirectory $mapDir "Terrain map directory $id"
    $canonical = Join-Path $mapDir $dataFile
    if (-not (Test-Path -LiteralPath $canonical -PathType Leaf)) { throw "Canonical terrain data is missing: $canonical" }
    Test-TerrainReparsePoint $canonical 'Canonical terrain data'
    $bytes = [long](Get-TerrainProperty $ManifestItem 'data_bytes')
    $sha = ([string](Get-TerrainProperty $ManifestItem 'data_sha256')).ToLowerInvariant()
    $xmlSha = ([string](Get-TerrainProperty $ManifestItem 'xml_sha256')).ToLowerInvariant()
    $installedId = [string](Get-TerrainProperty $ManifestItem 'installed_map_id')
    if ($bytes -le 0 -or ($bytes % 2) -ne 0) { throw "Invalid canonical byte count for $id" }
    if ($sha -cnotmatch '^[0-9a-f]{64}$') { throw "Invalid canonical hash record for $id" }
    if ($xmlSha -cnotmatch '^[0-9a-f]{64}$') { throw "Invalid native XML hash record for $id" }
    $item = Get-Item -LiteralPath $canonical
    if ($item.Length -ne $bytes) { throw "Canonical byte count mismatch for $id" }
    if ((Get-TerrainSha256 $canonical) -cne $sha) { throw "Canonical SHA256 mismatch for $id" }

    $xmlPath = Join-Path $mapDir $script:TerrainXmlName
    $nativePath = Join-Path $mapDir $script:TerrainNativeName
    if (Test-Path -LiteralPath $xmlPath) { Test-TerrainReparsePoint $xmlPath "Terrain XML $id" }
    if (Test-Path -LiteralPath $nativePath) { Test-TerrainReparsePoint $nativePath "Native terrain XML $id" }
    $nativeSource = if (Test-Path -LiteralPath $xmlPath -PathType Leaf) { $xmlPath } elseif (Test-Path -LiteralPath $nativePath -PathType Leaf) { $nativePath } else { $null }
    if ($null -eq $nativeSource) { throw "No native XML or $($script:TerrainNativeName) for $id" }
    if ((Get-TerrainSha256 $nativeSource) -cne $xmlSha) { throw "Native XML SHA256 mismatch for $id" }
    if ((Test-Path -LiteralPath $xmlPath -PathType Leaf) -and (Test-Path -LiteralPath $nativePath -PathType Leaf)) {
        if ((Get-TerrainSha256 $nativePath) -cne $xmlSha) { throw "Native XML backup mismatch for $id" }
    }
    $document = Read-TerrainXml $nativeSource "Native terrain XML $id"
    $node = Get-TerrainHeightmapNode $document $nativeSource
    $xmlId = Get-TerrainMapNodeId $node
    if (-not (Test-TerrainAllowedXmlId $xmlId $id)) { throw "Unexpected terrain XML ID for ${id}: $xmlId" }
    if ($installedId -and $installedId -cne $xmlId) { throw "Installed map ID does not match XML for $id" }
    $width = 0
    $height = 0
    if (-not [int]::TryParse((Get-TerrainNodeText $node 'Width' $nativeSource), [Globalization.NumberStyles]::Integer, $script:TerrainInvariant, [ref]$width) -or
        -not [int]::TryParse((Get-TerrainNodeText $node 'Height' $nativeSource), [Globalization.NumberStyles]::Integer, $script:TerrainInvariant, [ref]$height)) {
        throw "Invalid terrain dimensions for $id"
    }
    if ($width -le 0 -or $height -le 0 -or $width -gt 8192 -or $height -gt 8192) {
        throw "Terrain dimensions out of range for $id"
    }
    if ([long]$width * [long]$height * 2L -ne $bytes) { throw "Terrain dimensions do not match canonical bytes for $id" }
    foreach ($name in @('BottomLeft', 'BottomRight', 'TopRight', 'TopLeft')) {
        $corner = $node.SelectSingleNode($name)
        if ($null -eq $corner) { throw "Missing $name corner for $id" }
        foreach ($coordinate in @('Longitude', 'Latitude')) {
            $value = 0.0
            $coordinateText = Get-TerrainNodeText $corner $coordinate $nativeSource
            if (-not [double]::TryParse($coordinateText, [Globalization.NumberStyles]::Float, $script:TerrainInvariant, [ref]$value) -or
                [double]::IsNaN($value) -or [double]::IsInfinity($value)) {
                throw "Invalid $name $coordinate for $id"
            }
        }
    }
    return [pscustomobject]@{
        Id = $id
        Directory = $mapDir
        CanonicalPath = $canonical
        CanonicalBytes = $bytes
        CanonicalSha256 = $sha
        XmlSha256 = $xmlSha
        XmlPath = $xmlPath
        NativePath = $nativePath
        NativeSource = $nativeSource
        NativeDocument = $document
        NativeNode = $node
        XmlId = $xmlId
        Width = $width
        Height = $height
    }
}

function Get-TerrainContext {
    param([string]$TerrainRoot, [string[]]$MapIds, [switch]$AllowPartial)
    if ([string]::IsNullOrWhiteSpace($TerrainRoot)) { throw 'TerrainRoot is required.' }
    $rootFull = [IO.Path]::GetFullPath($TerrainRoot)
    $requested = @($MapIds | ForEach-Object { [string]$_ })
    if ($requested.Count -eq 0) { throw 'MapIds must contain at least one map ID.' }
    foreach ($id in $requested) { Assert-TerrainMapId $id }
    if (@($requested | Select-Object -Unique).Count -ne $requested.Count) { throw 'MapIds contains duplicates.' }
    Assert-TerrainDirectory $rootFull 'TerrainRoot'
    $parent = [IO.Directory]::GetParent($rootFull)
    if ($null -eq $parent) { throw "TerrainRoot has no parent: $rootFull" }
    Assert-TerrainDirectory $parent.FullName 'TerrainRoot parent'
    $manifestPath = Join-Path $rootFull 'WarThunderMaps.install.json'
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) { throw "Owned install manifest is missing: $manifestPath" }
    Test-TerrainReparsePoint $manifestPath 'Terrain install manifest'
    try { $manifest = Get-Content -LiteralPath $manifestPath -Raw -Encoding UTF8 | ConvertFrom-Json }
    catch { throw "Invalid terrain install manifest: $manifestPath" }
    if ([string](Get-TerrainProperty $manifest 'installer') -cne $script:TerrainInstallerName) {
        throw "Terrain install manifest is not owned by $($script:TerrainInstallerName)"
    }
    $manifestMaps = @(Get-TerrainProperty $manifest 'maps')
    if ($manifestMaps.Count -eq 0) { throw "Terrain install manifest has no maps: $manifestPath" }
    $all = @{}
    foreach ($item in $manifestMaps) {
        $id = [string](Get-TerrainProperty $item 'id')
        Assert-TerrainMapId $id
        if ($all.ContainsKey($id)) { throw "Duplicate map in install manifest: $id" }
        $all[$id] = $item
    }
    foreach ($id in $requested) {
        if (-not $all.ContainsKey($id)) { throw "Map is not owned by the install manifest: $id" }
    }
    $records = @{}
    foreach ($id in ($requested | Sort-Object)) {
        $records[$id] = Get-TerrainMapRecord $all[$id] $rootFull $manifestPath
    }
    $statePath = Join-Path $rootFull $script:TerrainStateName
    $cacheDir = Join-Path $rootFull '_uint16'
    $cachePath = Join-Path $cacheDir 'cache.json'
    Assert-TerrainDirectory $cacheDir 'Terrain output cache directory'
    if (Test-Path -LiteralPath $cachePath) { Test-TerrainReparsePoint $cachePath 'Terrain cache file' }
    $state = Read-TerrainState $statePath
    $cache = Read-TerrainCache $cachePath
    $rootXml = Join-Path $parent.FullName $script:TerrainXmlName
    if (Test-Path -LiteralPath $rootXml) { Test-TerrainReparsePoint $rootXml 'Terrain aggregate XML' }
    return [pscustomobject]@{
        TerrainRoot = $rootFull
        TerrainName = (Get-Item -LiteralPath $rootFull -Force).Name
        Parent = $parent.FullName
        ManifestPath = $manifestPath
        Manifest = $manifest
        AllMapIds = @($all.Keys | Sort-Object)
        RequestedIds = @($requested)
        Maps = $records
        StatePath = $statePath
        State = $state
        CacheDir = $cacheDir
        CachePath = $cachePath
        Cache = $cache
        RootXml = $rootXml
        RootBackup = Join-Path $parent.FullName $script:TerrainBackupName
    }
}

function Get-TerrainManifestMapIds {
    param([string]$TerrainRoot)
    $rootFull = [IO.Path]::GetFullPath($TerrainRoot)
    Assert-TerrainDirectory $rootFull 'TerrainRoot'
    $manifestPath = Join-Path $rootFull 'WarThunderMaps.install.json'
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) { throw "Owned install manifest is missing: $manifestPath" }
    Test-TerrainReparsePoint $manifestPath 'Terrain install manifest'
    try { $manifest = Get-Content -LiteralPath $manifestPath -Raw -Encoding UTF8 | ConvertFrom-Json }
    catch { throw "Invalid terrain install manifest: $manifestPath" }
    if ([string](Get-TerrainProperty $manifest 'installer') -cne $script:TerrainInstallerName) {
        throw "Terrain install manifest is not owned by $($script:TerrainInstallerName)"
    }
    $ids = New-Object System.Collections.ArrayList
    foreach ($item in @(Get-TerrainProperty $manifest 'maps')) {
        $id = [string](Get-TerrainProperty $item 'id')
        Assert-TerrainMapId $id
        if ($ids -contains $id) { throw "Duplicate map in install manifest: $id" }
        [void]$ids.Add($id)
    }
    if ($ids.Count -eq 0) { throw "Terrain install manifest has no maps: $manifestPath" }
    return @($ids | Sort-Object)
}

function Get-TerrainStateIdSet {
    param([object]$State)
    $set = @{}
    if ($null -ne $State) {
        foreach ($id in @(Get-TerrainProperty $State 'index_owned_ids')) {
            $textId = [string]$id
            if ($textId) { Assert-TerrainMapId $textId; $set[$textId] = $true }
        }
    }
    return $set
}

function Get-TerrainStateNodeHashes {
    param([object]$State)
    $result = @{}
    $object = if ($null -eq $State) { $null } else { Get-TerrainProperty $State 'individual_node_hashes' }
    if ($null -ne $object) {
        if ($object -is [System.Collections.IDictionary]) {
            foreach ($key in $object.Keys) { $result[[string]$key] = [string]$object[$key] }
        }
        else {
            foreach ($property in $object.PSObject.Properties) { $result[[string]$property.Name] = [string]$property.Value }
        }
    }
    return $result
}

function Get-TerrainNativeHashes {
    param([object]$State)
    $result = @{}
    $object = if ($null -eq $State) { $null } else { Get-TerrainProperty $State 'native_hashes' }
    if ($null -ne $object) {
        if ($object -is [System.Collections.IDictionary]) {
            foreach ($key in $object.Keys) { $result[[string]$key] = [string]$object[$key] }
        }
        else {
            foreach ($property in $object.PSObject.Properties) { $result[[string]$property.Name] = [string]$property.Value }
        }
    }
    return $result
}

function Get-TerrainRootDocument {
    param([object]$Context)
    if (Test-Path -LiteralPath $Context.RootXml -PathType Leaf) {
        return Read-TerrainXml $Context.RootXml 'Terrain aggregate XML'
    }
    $document = New-Object System.Xml.XmlDocument
    $document.XmlResolver = $null
    [void]$document.AppendChild($document.CreateXmlDeclaration('1.0', 'utf-8', $null))
    $resources = $document.CreateElement('Resources')
    [void]$document.AppendChild($resources)
    $list = $document.CreateElement('CustomHeightmapList')
    [void]$resources.AppendChild($list)
    return $document
}

function Get-TerrainRootList {
    param([System.Xml.XmlDocument]$Document, [string]$Path)
    $lists = $Document.SelectNodes('/Resources/CustomHeightmapList')
    if ($lists.Count -ne 1) { throw "Expected one CustomHeightmapList in aggregate XML: $Path" }
    return $lists.Item(0)
}

function Get-TerrainRootNodeMap {
    param([System.Xml.XmlNode]$List)
    $result = @{}
    $nodes = $List.SelectNodes('CustomHeightmap')
    for ($index = 0; $index -lt $nodes.Count; $index++) {
        $node = $nodes.Item($index)
        $id = Get-TerrainMapNodeId $node
        if (-not $result.ContainsKey($id)) { $result[$id] = New-Object System.Collections.ArrayList }
        [void]$result[$id].Add($node)
    }
    return $result
}

function New-TerrainAggregateNode {
    param([object]$Context, [object]$Record)
    $node = $Context.RootDocument.ImportNode($Record.NativeNode, $true)
    Set-TerrainNodeText $Context.RootDocument $node 'File' ($Context.TerrainName + '/_uint16/' + $Record.Id + '.data')
    Set-TerrainNodeText $Context.RootDocument $node 'AltitudeFactor' '1'
    Set-TerrainNodeText $Context.RootDocument $node 'AltitudeOffset' '-32768'
    $signed = $node.SelectNodes('Signed')
    for ($index = $signed.Count - 1; $index -ge 0; $index--) { [void]$node.RemoveChild($signed.Item($index)) }
    return $node
}

function Invoke-TerrainProfile194 {
    param([object]$Context, [switch]$ValidateOnly)
    $owned = Get-TerrainStateIdSet $Context.State
    $ownedHashes = Get-TerrainStateNodeHashes $Context.State
    $document = Get-TerrainRootDocument $Context
    $list = Get-TerrainRootList $document $Context.RootXml
    $existing = Get-TerrainRootNodeMap $list
    $desiredHashes = @{}
    $rootChanged = $false

    foreach ($id in $Context.RequestedIds) {
        $record = $Context.Maps[$id]
        $desired = New-TerrainAggregateNode ([pscustomobject]@{ RootDocument = $document; TerrainName = $Context.TerrainName }) $record
        $desiredHash = Get-TerrainNodeHash $desired
        $desiredHashes[$id] = $desiredHash
        if ($existing.ContainsKey($record.XmlId)) {
            $nodes = $existing[$record.XmlId]
            if ($nodes.Count -ne 1 -or -not $owned.ContainsKey($id)) {
                throw "Aggregate terrain XML contains a foreign or duplicate collision for $($record.XmlId)"
            }
            if (-not $ownedHashes.ContainsKey($id) -or $ownedHashes[$id] -cne (Get-TerrainNodeHash $nodes.Item(0))) {
                throw "Aggregate terrain XML node no longer matches format state for $id"
            }
            if ((Get-TerrainNodeHash $nodes.Item(0)) -cne $desiredHash) {
                [void]$list.ReplaceChild($desired, $nodes.Item(0))
                $rootChanged = $true
            }
        }
        else {
            $stateIs194 = $null -ne $Context.State -and [string](Get-TerrainProperty $Context.State 'profile') -ceq '1.9.4'
            if ($owned.ContainsKey($id) -and $stateIs194) {
                throw "Owned aggregate terrain XML node is missing for $id"
            }
            [void]$list.AppendChild($desired)
            $rootChanged = $true
        }
    }

    foreach ($id in $Context.RequestedIds) {
        $record = $Context.Maps[$id]
        $output = Join-Path $Context.CacheDir ($id + '.data')
        if (Test-Path -LiteralPath $output) {
            Test-TerrainReparsePoint $output "Terrain output $id"
            if (-not $Context.Cache.Maps.ContainsKey($id) -and
                -not ($null -ne $Context.State -and [string](Get-TerrainProperty $Context.State 'profile') -ceq '1.9.4' -and $owned.ContainsKey($id))) {
                throw "Foreign file collides with expected terrain output: $output"
            }
        }
    }

    if ($ValidateOnly) {
        return [pscustomobject]@{ Changed = $false; RootChanged = $rootChanged; Converted = 0; Reused = 0; Validated = $true }
    }

    Assert-TerrainDirectory $Context.CacheDir 'Terrain output cache directory'
    if (-not (Test-Path -LiteralPath $Context.CacheDir)) { New-Item -ItemType Directory -Path $Context.CacheDir | Out-Null }
    Assert-TerrainDirectory $Context.CacheDir 'Terrain output cache directory'
    $cacheMaps = @{}
    foreach ($key in $Context.Cache.Maps.Keys) { $cacheMaps[$key] = $Context.Cache.Maps[$key] }
    $convertedCount = 0
    $reusedCount = 0
    foreach ($id in $Context.RequestedIds) {
        $record = $Context.Maps[$id]
        $output = Join-Path $Context.CacheDir ($id + '.data')
        $reuse = $false
        if ((Test-Path -LiteralPath $output -PathType Leaf) -and $cacheMaps.ContainsKey($id)) {
            $entry = $cacheMaps[$id]
            if ([string](Get-TerrainProperty $entry 'source_sha256') -ceq $record.CanonicalSha256 -and
                [long](Get-TerrainProperty $entry 'source_bytes') -eq $record.CanonicalBytes -and
                [long](Get-TerrainProperty $entry 'data_bytes') -eq $record.CanonicalBytes -and
                (Get-Item -LiteralPath $output).Length -eq $record.CanonicalBytes -and
                (Get-TerrainSha256 $output) -ceq [string](Get-TerrainProperty $entry 'data_sha256')) {
                $reuse = $true
                $reusedCount++
            }
        }
        if (-not $reuse) {
            $result = Convert-WTInt16ToUInt16 -Source $record.CanonicalPath -Destination $output -ExpectedSourceSha256 $record.CanonicalSha256 -ExpectedBytes $record.CanonicalBytes
            if ([long]$result.data_bytes -ne $record.CanonicalBytes -or (Get-TerrainSha256 $output) -cne [string]$result.data_sha256) {
                throw "Converted terrain output failed verification for $id"
            }
            $cacheMaps[$id] = [pscustomobject]@{
                source_sha256 = $record.CanonicalSha256
                source_bytes = $record.CanonicalBytes
                data_sha256 = [string]$result.data_sha256
                data_bytes = [long]$result.data_bytes
            }
            Write-TerrainCache $Context.CachePath $cacheMaps
            $convertedCount++
        }
    }

    $stateWasTarget = $null -ne $Context.State -and [string](Get-TerrainProperty $Context.State 'profile') -ceq '1.9.4'
    if (-not $rootChanged -and $stateWasTarget -and $convertedCount -eq 0) {
        return [pscustomobject]@{ Changed = $false; RootChanged = $false; Converted = 0; Reused = $reusedCount; Validated = $false }
    }

    $moved = New-Object System.Collections.ArrayList
    $wroteRoot = $false
    $oldRootBytes = $null
    if (Test-Path -LiteralPath $Context.RootXml -PathType Leaf) { $oldRootBytes = [IO.File]::ReadAllBytes($Context.RootXml) }
    try {
        if ($rootChanged) { Backup-TerrainRootOnce $Context.RootXml $Context.RootBackup }
        foreach ($id in $Context.RequestedIds) {
            $record = $Context.Maps[$id]
            if (-not (Test-Path -LiteralPath $record.XmlPath -PathType Leaf)) { continue }
            if (Test-Path -LiteralPath $record.NativePath -PathType Leaf) {
                if ((Get-TerrainSha256 $record.NativePath) -cne $record.XmlSha256 -or (Get-TerrainSha256 $record.XmlPath) -cne $record.XmlSha256) {
                    throw "Native XML collision for $id"
                }
                [IO.File]::Delete($record.XmlPath)
            }
            else { [IO.File]::Move($record.XmlPath, $record.NativePath) }
            [void]$moved.Add($record)
        }
        if ($rootChanged) {
            Write-TerrainXmlAtomic $document $Context.RootXml
            $wroteRoot = $true
        }
        $nativeHashes = Get-TerrainNativeHashes $Context.State
        foreach ($id in $Context.RequestedIds) { $nativeHashes[$id] = $Context.Maps[$id].XmlSha256 }
        $stateOutput = New-Object System.Collections.Specialized.OrderedDictionary
        foreach ($id in ($Context.RequestedIds | Sort-Object)) { $stateOutput[$id] = [string]$desiredHashes[$id] }
        $nativeOutput = New-Object System.Collections.Specialized.OrderedDictionary
        foreach ($id in ($nativeHashes.Keys | Sort-Object)) { $nativeOutput[$id] = [string]$nativeHashes[$id] }
        $stateDocument = [ordered]@{
            schema_version = 1
            owner = $script:TerrainFormatOwner
            profile = '1.9.4'
            index_owned_ids = @($Context.RequestedIds | Sort-Object)
            individual_node_hashes = $stateOutput
            native_hashes = $nativeOutput
            cache_encoding = $script:TerrainCacheEncoding
        }
        Write-TerrainTextAtomic $Context.StatePath (($stateDocument | ConvertTo-Json -Depth 8) + [Environment]::NewLine)
    }
    catch {
        foreach ($record in $moved) {
            if ((-not (Test-Path -LiteralPath $record.XmlPath -PathType Leaf)) -and (Test-Path -LiteralPath $record.NativePath -PathType Leaf)) {
                Copy-TerrainFileAtomic $record.NativePath $record.XmlPath
            }
        }
        if ($null -ne $oldRootBytes) { [IO.File]::WriteAllBytes($Context.RootXml, $oldRootBytes) }
        elseif ($wroteRoot -and (Test-Path -LiteralPath $Context.RootXml -PathType Leaf)) { [IO.File]::Delete($Context.RootXml) }
        throw
    }
    return [pscustomobject]@{ Changed = $true; RootChanged = $rootChanged; Converted = $convertedCount; Reused = $reusedCount; Validated = $false }
}

function Invoke-TerrainProfile195 {
    param([object]$Context, [switch]$ValidateOnly)
    $owned = Get-TerrainStateIdSet $Context.State
    $ownedHashes = Get-TerrainStateNodeHashes $Context.State
    $document = Get-TerrainRootDocument $Context
    $list = Get-TerrainRootList $document $Context.RootXml
    $rootNodes = Get-TerrainRootNodeMap $list
    $remove = New-Object System.Collections.ArrayList
    foreach ($id in $owned.Keys) {
        $record = if ($Context.Maps.ContainsKey($id)) { $Context.Maps[$id] } else { $null }
        if ($null -eq $record) {
            foreach ($nodeId in $rootNodes.Keys) {
                if ($nodeId.EndsWith('.' + $id, [StringComparison]::Ordinal)) {
                    throw "Format state owns $id but it is outside the current target context."
                }
            }
            continue
        }
        if (-not $rootNodes.ContainsKey($record.XmlId)) { throw "Owned aggregate node is missing for $id" }
        $nodes = $rootNodes[$record.XmlId]
        if ($nodes.Count -ne 1 -or -not $ownedHashes.ContainsKey($id)) { throw "Owned aggregate node is ambiguous for $id" }
        $node = $nodes.Item(0)
        if ((Get-TerrainNodeHash $node) -cne $ownedHashes[$id]) { throw "Aggregate node hash does not match format state for $id" }
        [void]$remove.Add($node)
    }
    foreach ($id in $Context.RequestedIds) {
        $record = $Context.Maps[$id]
        if ($rootNodes.ContainsKey($record.XmlId) -and -not $owned.ContainsKey($id)) {
            throw "Aggregate terrain XML contains a foreign collision for $($record.XmlId)"
        }
        if ((Test-Path -LiteralPath $record.XmlPath -PathType Leaf) -and (Get-TerrainSha256 $record.XmlPath) -cne $record.XmlSha256) {
            throw "Foreign or modified terrain XML collides with $id"
        }
        if ((Test-Path -LiteralPath $record.NativePath -PathType Leaf) -and (Get-TerrainSha256 $record.NativePath) -cne $record.XmlSha256) {
            throw "Native XML hash does not match install manifest for $id"
        }
    }
    $rootChanged = $remove.Count -gt 0
    if ($ValidateOnly) { return [pscustomobject]@{ Changed = $false; RootChanged = $rootChanged; Converted = 0; Reused = 0; Validated = $true } }

    $stateWasTarget = $null -ne $Context.State -and [string](Get-TerrainProperty $Context.State 'profile') -ceq '1.9.5'
    $needsXml = @($Context.RequestedIds | Where-Object { -not (Test-Path -LiteralPath $Context.Maps[$_].XmlPath -PathType Leaf) })
    if (-not $rootChanged -and $stateWasTarget -and $needsXml.Count -eq 0) {
        return [pscustomobject]@{ Changed = $false; RootChanged = $false; Converted = 0; Reused = 0; Validated = $false }
    }

    $createdXml = New-Object System.Collections.ArrayList
    $oldRootBytes = $null
    $wroteRoot = $false
    if (Test-Path -LiteralPath $Context.RootXml -PathType Leaf) { $oldRootBytes = [IO.File]::ReadAllBytes($Context.RootXml) }
    try {
        if ($rootChanged) { Backup-TerrainRootOnce $Context.RootXml $Context.RootBackup }
        if ($rootChanged) {
            foreach ($node in $remove) { [void]$list.RemoveChild($node) }
            Write-TerrainXmlAtomic $document $Context.RootXml
            $wroteRoot = $true
        }
        foreach ($id in $needsXml) {
            $record = $Context.Maps[$id]
            Copy-TerrainFileAtomic $record.NativePath $record.XmlPath
            [void]$createdXml.Add($record)
        }
        $nativeHashes = Get-TerrainNativeHashes $Context.State
        foreach ($id in $Context.RequestedIds) { $nativeHashes[$id] = $Context.Maps[$id].XmlSha256 }
        $nativeOutput = New-Object System.Collections.Specialized.OrderedDictionary
        foreach ($id in ($nativeHashes.Keys | Sort-Object)) { $nativeOutput[$id] = [string]$nativeHashes[$id] }
        $stateDocument = [ordered]@{
            schema_version = 1
            owner = $script:TerrainFormatOwner
            profile = '1.9.5'
            index_owned_ids = @()
            individual_node_hashes = [ordered]@{}
            native_hashes = $nativeOutput
            cache_encoding = $script:TerrainCacheEncoding
        }
        Write-TerrainTextAtomic $Context.StatePath (($stateDocument | ConvertTo-Json -Depth 8) + [Environment]::NewLine)
    }
    catch {
        foreach ($record in $createdXml) {
            if (Test-Path -LiteralPath $record.XmlPath -PathType Leaf) { [IO.File]::Delete($record.XmlPath) }
        }
        if ($null -ne $oldRootBytes) { [IO.File]::WriteAllBytes($Context.RootXml, $oldRootBytes) }
        elseif ($wroteRoot -and (Test-Path -LiteralPath $Context.RootXml -PathType Leaf)) { [IO.File]::Delete($Context.RootXml) }
        throw
    }
    return [pscustomobject]@{ Changed = $true; RootChanged = $rootChanged; Converted = 0; Reused = 0; Validated = $false }
}

function Test-WTTerrainFormatTarget {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$TerrainRoot,
        [Parameter(Mandatory = $true)]
        [string[]]$MapIds
    )
    $requested = @($MapIds | ForEach-Object { [string]$_ })
    if ($requested.Count -eq 0) { throw 'MapIds must contain at least one map ID.' }
    foreach ($id in $requested) { Assert-TerrainMapId $id }
    if (@($requested | Select-Object -Unique).Count -ne $requested.Count) { throw 'MapIds contains duplicates.' }
    $rootFull = [IO.Path]::GetFullPath($TerrainRoot)
    if (-not (Test-Path -LiteralPath $rootFull)) {
        return [pscustomobject]@{ IsValid = $true; Exists = $false; Status = 'Missing'; TerrainRoot = $rootFull; MapIds = $requested; Profile = $null }
    }
    Assert-TerrainDirectory $rootFull 'TerrainRoot'
    $context = Get-TerrainContext $rootFull $requested
    $profile = if ($null -eq $context.State) { $null } else { [string](Get-TerrainProperty $context.State 'profile') }
    return [pscustomobject]@{ IsValid = $true; Exists = $true; Status = 'Ready'; TerrainRoot = $context.TerrainRoot; MapIds = $requested; Profile = $profile; MapCount = $context.RequestedIds.Count }
}

function Set-WTTerrainFormat {
    [CmdletBinding()]
    param(
        [string]$TerrainRoot = (Join-Path $env:APPDATA 'Tacview\Data\Terrain\Custom\WarThunderMaps'),
        [Parameter(Mandatory = $true)]
        [ValidateSet('1.9.4', '1.9.5')]
        [string]$Profile,
        [switch]$ValidateOnly,
        [switch]$AllowPartial
    )
    if (-not (Test-Path -LiteralPath $TerrainRoot)) { throw "TerrainRoot does not exist: $TerrainRoot" }
    $rootFull = [IO.Path]::GetFullPath($TerrainRoot)
    $allIds = @(Get-TerrainManifestMapIds $rootFull)
    $context = Get-TerrainContext $rootFull $allIds -AllowPartial:$AllowPartial
    if (-not $AllowPartial -and ($context.AllMapIds.Count -ne 69 -or [int](Get-TerrainProperty $context.Manifest 'map_count') -ne 69)) {
        throw 'A complete 69-map terrain install is required; use -AllowPartial only for fixtures.'
    }
    if ($Profile -ceq '1.9.4') { $result = Invoke-TerrainProfile194 $context -ValidateOnly:$ValidateOnly }
    else { $result = Invoke-TerrainProfile195 $context -ValidateOnly:$ValidateOnly }
    return [pscustomobject]@{
        TerrainRoot = $context.TerrainRoot
        Profile = $Profile
        ValidateOnly = [bool]$ValidateOnly
        Changed = [bool]$result.Changed
        RootChanged = [bool]$result.RootChanged
        Converted = [int]$result.Converted
        Reused = [int]$result.Reused
        StatePath = $context.StatePath
        CachePath = $context.CachePath
        RootXml = $context.RootXml
    }
}
