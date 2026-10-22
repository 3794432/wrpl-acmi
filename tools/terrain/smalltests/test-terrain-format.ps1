$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2.0

$terrainModuleRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$repoRoot = [IO.Path]::GetFullPath((Join-Path $terrainModuleRoot '..\..'))
$workRoot = [IO.Path]::GetFullPath((Join-Path $repoRoot '..'))
$taskRoot = Join-Path $workRoot 'tacview-dual-version'
$logPath = Join-Path $taskRoot 'profile-tests.log'
$runtimeRoot = Join-Path (Join-Path $PSScriptRoot 'runtime') ('terrain-format-' + $PID + '-' + [Guid]::NewGuid().ToString('N'))
$utf8 = New-Object Text.UTF8Encoding($false)
$invariant = [Globalization.CultureInfo]::InvariantCulture

if (-not (Test-Path -LiteralPath $taskRoot -PathType Container)) { New-Item -ItemType Directory -Path $taskRoot | Out-Null }
[IO.File]::WriteAllText($logPath, '', $utf8)

function Write-TestLog {
    param([string]$Message)
    $line = '{0:yyyy-MM-dd HH:mm:ss.fff} {1}' -f (Get-Date), $Message
    Write-Host $line
    [IO.File]::AppendAllText($logPath, $line + [Environment]::NewLine, $utf8)
}

function Assert-Condition {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) { throw "ASSERT: $Message" }
}

function Assert-Equal {
    param($Actual, $Expected, [string]$Message)
    if ($Actual -ne $Expected) { throw "ASSERT: $Message (expected <$Expected>, actual <$Actual>)" }
}

function Get-Sha256Hex {
    param([string]$Path)
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

function Write-XmlDocument {
    param([System.Xml.XmlDocument]$Document, [string]$Path)
    $settings = New-Object System.Xml.XmlWriterSettings
    $settings.Indent = $true
    $settings.Encoding = $utf8
    $writer = [System.Xml.XmlWriter]::Create($Path, $settings)
    try { $Document.Save($writer) } finally { $writer.Dispose() }
}

function Add-Text {
    param([System.Xml.XmlDocument]$Document, [System.Xml.XmlNode]$Parent, [string]$Name, [string]$Value)
    $element = $Document.CreateElement($Name)
    $element.InnerText = $Value
    [void]$Parent.AppendChild($element)
}

function New-Int16LeBytes {
    param([int[]]$Values)
    $bytes = New-Object byte[] ($Values.Length * 2)
    for ($index = 0; $index -lt $Values.Length; $index++) {
        $bytes[2 * $index] = [byte]($Values[$index] -band 0xff)
        $bytes[(2 * $index) + 1] = [byte](($Values[$index] -shr 8) -band 0xff)
    }
    return ,$bytes
}

function New-NativeXml {
    param([string]$Path, [string]$Id, [int]$Offset)
    $document = New-Object System.Xml.XmlDocument
    [void]$document.AppendChild($document.CreateXmlDeclaration('1.0', 'utf-8', $null))
    $resources = $document.CreateElement('Resources')
    [void]$document.AppendChild($resources)
    $list = $document.CreateElement('CustomHeightmapList')
    [void]$resources.AppendChild($list)
    $node = $document.CreateElement('CustomHeightmap')
    $node.SetAttribute('Layer', 'Real World')
    $node.SetAttribute('ID', ('WarThunder.Geo1.' + $Id))
    [void]$list.AppendChild($node)
    Add-Text $document $node 'File' ($Id + '.data')
    Add-Text $document $node 'Visible' 'true'
    Add-Text $document $node 'BigEndian' '0'
    Add-Text $document $node 'Signed' 'true'
    Add-Text $document $node 'Width' '2'
    Add-Text $document $node 'Height' '2'
    Add-Text $document $node 'AltitudeFactor' '1'
    Add-Text $document $node 'AltitudeOffset' '0'
    Add-Text $document $node 'Projection' 'Quad'
    [void]$node.AppendChild($document.CreateComment('coordinate-ok'))
    foreach ($cornerData in @(@('BottomLeft', -10.0, -20.0), @('BottomRight', -9.0, -20.0), @('TopRight', -9.0, -19.0), @('TopLeft', -10.0, -19.0))) {
        $corner = $document.CreateElement([string]$cornerData[0])
        Add-Text $document $corner 'Longitude' ([double]$cornerData[1]).ToString('F12', $invariant)
        Add-Text $document $corner 'Latitude' (([double]$cornerData[2]) + $Offset).ToString('F12', $invariant)
        [void]$node.AppendChild($corner)
    }
    Write-XmlDocument $document $Path
}

function New-RootXml {
    param([string]$Path)
    $document = New-Object System.Xml.XmlDocument
    [void]$document.AppendChild($document.CreateXmlDeclaration('1.0', 'utf-8', $null))
    $resources = $document.CreateElement('Resources')
    [void]$document.AppendChild($resources)
    [void]$resources.AppendChild($document.CreateComment('foreign-root-comment'))
    $foreign = $document.CreateElement('Unrelated')
    $foreign.SetAttribute('Keep', 'yes')
    [void]$resources.AppendChild($foreign)
    $list = $document.CreateElement('CustomHeightmapList')
    [void]$resources.AppendChild($list)
    $node = $document.CreateElement('CustomHeightmap')
    $node.SetAttribute('Layer', 'Real World')
    $node.SetAttribute('ID', 'WarThunder.Atlas1.foreign')
    [void]$list.AppendChild($node)
    Add-Text $document $node 'File' 'foreign.data'
    Add-Text $document $node 'Visible' 'true'
    [void]$list.AppendChild($document.CreateComment('foreign-list-comment'))
    Write-XmlDocument $document $Path
}

function New-Fixture {
    param([string]$FixtureRoot)
    $default = Join-Path (Join-Path $FixtureRoot 'Custom') 'WarThunderMaps'
    New-Item -ItemType Directory -Path $default -Force | Out-Null
    $definitions = @(
        @('air_alpha', [int[]]@(-50, 0, 1234, -50), 0),
        @('air_bravo', [int[]]@(1234, -50, 0, 0), 1)
    )
    $entries = New-Object System.Collections.ArrayList
    foreach ($definition in $definitions) {
        $id = [string]$definition[0]
        $values = [int[]]$definition[1]
        $offset = [int]$definition[2]
        $mapDir = Join-Path $default $id
        New-Item -ItemType Directory -Path $mapDir | Out-Null
        $dataPath = Join-Path $mapDir ($id + '.data')
        [IO.File]::WriteAllBytes($dataPath, (New-Int16LeBytes $values))
        $xmlPath = Join-Path $mapDir 'CustomHeightmapList.xml'
        New-NativeXml $xmlPath $id $offset
        [void]$entries.Add([ordered]@{
            id = $id
            map_id = ('WarThunder.' + $id)
            new_map_id = ('WarThunder.Geo1.' + $id)
            installed_map_id = ('WarThunder.Geo1.' + $id)
            data_file = ($id + '.data')
            data_bytes = [long](8)
            data_sha256 = (Get-Sha256Hex $dataPath)
            xml_sha256 = (Get-Sha256Hex $xmlPath)
        })
    }
    $manifest = [ordered]@{
        schema_version = 2
        installer = 'war-thunder-air-terrain'
        map_count = 2
        maps = @($entries)
    }
    [IO.File]::WriteAllText((Join-Path $default 'WarThunderMaps.install.json'), (($manifest | ConvertTo-Json -Depth 8) + [Environment]::NewLine), $utf8)
    New-RootXml (Join-Path (Join-Path $FixtureRoot 'Custom') 'CustomHeightmapList.xml')
    return $default
}

function Add-LegacyAggregateNode {
    param(
        [System.Xml.XmlDocument]$Document,
        [string]$TerrainRoot,
        [string]$Id,
        [switch]$BadFile,
        [switch]$UnknownField,
        [double]$LongitudeShift = 0.0
    )
    $nativePath = Join-Path (Join-Path $TerrainRoot $Id) 'CustomHeightmapList.xml'
    [xml]$native = Get-Content -LiteralPath $nativePath -Raw -Encoding UTF8
    $sourceNode = $native.SelectSingleNode('/Resources/CustomHeightmapList/CustomHeightmap')
    $node = $Document.ImportNode($sourceNode, $true)

    # Attribute order and XML whitespace are serialization details, not node semantics.
    $attributes = @($node.Attributes | ForEach-Object { [pscustomobject]@{ Name = $_.Name; Value = $_.Value } })
    foreach ($attribute in $attributes) { [void]$node.Attributes.RemoveNamedItem($attribute.Name) }
    [array]::Reverse($attributes)
    foreach ($attribute in $attributes) { $node.SetAttribute($attribute.Name, $attribute.Value) }

    $file = if ($BadFile) { 'elsewhere/_uint16/' + $Id + '.data' } else { 'WarThunderMaps/_uint16/' + $Id + '.data' }
    $node.SelectSingleNode('File').InnerText = $file
    $node.SelectSingleNode('AltitudeFactor').InnerText = '1'
    $node.SelectSingleNode('AltitudeOffset').InnerText = '-32768'
    $signed = $node.SelectSingleNode('Signed')
    if ($null -ne $signed) { [void]$node.RemoveChild($signed) }
    if ($LongitudeShift -ne 0.0) {
        $longitude = $node.SelectSingleNode('BottomLeft/Longitude')
        $value = [double]::Parse($longitude.InnerText, $invariant)
        $longitude.InnerText = ($value + $LongitudeShift).ToString('F12', $invariant)
    }
    if ($UnknownField) { Add-Text $Document $node 'UnexpectedField' 'unknown' }
    return $node
}

function New-LegacyAggregateFixture {
    param([string]$FixtureRoot, [string]$Mode = 'Valid')
    $terrainRoot = New-Fixture $FixtureRoot
    $rootPath = Join-Path (Join-Path $FixtureRoot 'Custom') 'CustomHeightmapList.xml'
    [xml]$document = Get-Content -LiteralPath $rootPath -Raw -Encoding UTF8
    $list = $document.SelectSingleNode('/Resources/CustomHeightmapList')
    switch ($Mode) {
        'Valid' {
            [void]$list.AppendChild((Add-LegacyAggregateNode $document $terrainRoot 'air_alpha' -LongitudeShift 1e-12))
            [void]$list.AppendChild((Add-LegacyAggregateNode $document $terrainRoot 'air_bravo'))
        }
        'BadFile' {
            [void]$list.AppendChild((Add-LegacyAggregateNode $document $terrainRoot 'air_alpha' -BadFile))
            [void]$list.AppendChild((Add-LegacyAggregateNode $document $terrainRoot 'air_bravo'))
        }
        'BadGeometry' {
            [void]$list.AppendChild((Add-LegacyAggregateNode $document $terrainRoot 'air_alpha' -LongitudeShift 5e-12))
            [void]$list.AppendChild((Add-LegacyAggregateNode $document $terrainRoot 'air_bravo'))
        }
        'UnknownField' {
            [void]$list.AppendChild((Add-LegacyAggregateNode $document $terrainRoot 'air_alpha' -UnknownField))
            [void]$list.AppendChild((Add-LegacyAggregateNode $document $terrainRoot 'air_bravo'))
        }
        'Duplicate' {
            [void]$list.AppendChild((Add-LegacyAggregateNode $document $terrainRoot 'air_alpha'))
            [void]$list.AppendChild((Add-LegacyAggregateNode $document $terrainRoot 'air_alpha'))
        }
        default { throw "Unknown legacy fixture mode: $Mode" }
    }
    Write-XmlDocument $document $rootPath
    return $terrainRoot
}

function Get-TreeSnapshot {
    param([string]$Root)
    $items = @(Get-ChildItem -LiteralPath $Root -Recurse -Force | Sort-Object FullName)
    return (@($items | ForEach-Object {
        # NTFS can publish delayed directory timestamps after fixture creation.
        # Compare directory membership and file content/metadata instead.
        if ($_.PSIsContainer) { '{0}|D' -f $_.FullName.Substring($Root.Length) }
        else { '{0}|F|{1}|{2}|{3}' -f $_.FullName.Substring($Root.Length), $_.Length, $_.LastWriteTimeUtc.Ticks, (Get-Sha256Hex $_.FullName) }
    }) -join "`n")
}

function Assert-TreeUnchanged {
    param([string]$Root, [string]$Before, [string]$Message)
    $after = Get-TreeSnapshot $Root
    if ($after -cne $Before) { throw "ASSERT: $Message`nBEFORE:`n$Before`nAFTER:`n$after" }
}

function Read-EncodedSigned {
    param([string]$Path)
    [byte[]]$bytes = [IO.File]::ReadAllBytes($Path)
    $values = New-Object int[] ($bytes.Length / 2)
    for ($index = 0; $index -lt $values.Length; $index++) {
        $values[$index] = ([int]$bytes[2 * $index] -bor ([int]$bytes[(2 * $index) + 1] -shl 8)) - 32768
    }
    return ,$values
}

function Get-NodeCornerText {
    param([System.Xml.XmlNode]$Node)
    $parts = New-Object System.Collections.ArrayList
    foreach ($name in @('BottomLeft', 'BottomRight', 'TopRight', 'TopLeft')) {
        $corner = $Node.SelectSingleNode($name)
        $longitude = $corner.SelectSingleNode('Longitude').InnerText
        $latitude = $corner.SelectSingleNode('Latitude').InnerText
        [void]$parts.Add($name + ':' + $longitude + ',' + $latitude)
    }
    return ($parts -join '|')
}

function Invoke-Set {
    param([string]$Root, [string]$Profile, [switch]$ValidateOnly)
    return Set-WTTerrainFormat -TerrainRoot $Root -Profile $Profile -AllowPartial -ValidateOnly:$ValidateOnly
}

$runtimeSucceeded = $false
try {
    Write-TestLog 'START terrain format synthetic tests'
    $modulePath = Join-Path $terrainModuleRoot 'Terrain-Format.ps1'
    $wrapperPath = Join-Path $terrainModuleRoot 'Set-Tacview-Terrain-Format.ps1'
    Assert-Condition (Test-Path -LiteralPath $modulePath -PathType Leaf) 'format module is missing'
    Assert-Condition (Test-Path -LiteralPath $wrapperPath -PathType Leaf) 'format wrapper is missing'
    . $modulePath

    $missingRoot = Join-Path $runtimeRoot 'not-installed\WarThunderMaps'
    $missing = Test-WTTerrainFormatTarget -TerrainRoot $missingRoot -MapIds @('air_alpha')
    Assert-Condition (-not $missing.Exists) 'missing destination preflight should report no installation'
    Assert-Condition (-not (Test-Path -LiteralPath $runtimeRoot)) 'missing destination preflight wrote a path'
    Write-TestLog 'PASS absent target preflight validates IDs without writes'

    $fixtureRoot = Join-Path $runtimeRoot 'main'
    $terrainRoot = New-Fixture $fixtureRoot
    $parentRoot = Join-Path $fixtureRoot 'Custom'
    $rootPath = Join-Path $parentRoot 'CustomHeightmapList.xml'
    $manifestPath = Join-Path $terrainRoot 'WarThunderMaps.install.json'
    $manifestHash = Get-Sha256Hex $manifestPath
    $canonicalHash = @{}
    $xmlHash = @{}
    $cornerText = @{}
    foreach ($id in @('air_alpha', 'air_bravo')) {
        $canonicalHash[$id] = Get-Sha256Hex (Join-Path (Join-Path $terrainRoot $id) ($id + '.data'))
        $xmlPath = Join-Path (Join-Path $terrainRoot $id) 'CustomHeightmapList.xml'
        $xmlHash[$id] = Get-Sha256Hex $xmlPath
        [xml]$native = Get-Content -LiteralPath $xmlPath -Raw -Encoding UTF8
        $node = $native.Resources.CustomHeightmapList.CustomHeightmap
        $cornerText[$id] = Get-NodeCornerText $node
    }
    $target = Test-WTTerrainFormatTarget -TerrainRoot $terrainRoot -MapIds @('air_alpha', 'air_bravo')
    Assert-Condition $target.IsValid 'installed target preflight failed'
    Write-TestLog 'PASS installed target validates two canonical maps and native XML records'

    [xml]$conflict = Get-Content -LiteralPath $rootPath -Raw -Encoding UTF8
    $list = $conflict.Resources.CustomHeightmapList
    $collision = $conflict.CreateElement('CustomHeightmap')
    $collision.SetAttribute('ID', 'WarThunder.Geo1.air_alpha')
    [void]$list.AppendChild($collision)
    Write-XmlDocument $conflict $rootPath
    $beforeConflict = Get-TreeSnapshot $terrainRoot
    $conflictFailed = $false
    try { Invoke-Set $terrainRoot '1.9.4' | Out-Null } catch { $conflictFailed = $true }
    Assert-Condition $conflictFailed 'foreign aggregate ID collision was accepted'
    Assert-TreeUnchanged $terrainRoot $beforeConflict 'collision rejection changed the installed tree'
    Assert-Condition (-not (Test-Path -LiteralPath (Join-Path $terrainRoot '_uint16'))) 'collision rejection created cache output'
    Assert-Condition (-not (Test-Path -LiteralPath (Join-Path $terrainRoot 'WarThunderMaps.format.json'))) 'collision rejection created format state'
    Write-TestLog 'PASS foreign aggregate collision is rejected before writes'

    $legacyFixture = Join-Path $runtimeRoot 'legacy-no-state'
    $legacyRoot = New-LegacyAggregateFixture $legacyFixture
    $legacyParent = Join-Path $legacyFixture 'Custom'
    $legacyXmlPath = Join-Path $legacyParent 'CustomHeightmapList.xml'
    $legacyStatePath = Join-Path $legacyRoot 'WarThunderMaps.format.json'
    $legacyCacheDir = Join-Path $legacyRoot '_uint16'
    $legacyCachePath = Join-Path $legacyCacheDir 'cache.json'
    Assert-Condition (-not (Test-Path -LiteralPath $legacyStatePath)) 'legacy fixture unexpectedly has format state'
    Assert-Condition (-not (Test-Path -LiteralPath $legacyCacheDir)) 'legacy fixture unexpectedly has an encoding cache'

    $beforeLegacyValidate = Get-TreeSnapshot $legacyFixture
    $legacyValidated = Invoke-Set $legacyRoot '1.9.4' -ValidateOnly
    Assert-Condition $legacyValidated.ValidateOnly 'legacy ValidateOnly result is missing'
    Assert-Condition (-not $legacyValidated.Changed) 'legacy ValidateOnly claimed an installation change'
    Assert-Condition (-not (Test-Path -LiteralPath $legacyStatePath)) 'legacy ValidateOnly created format state'
    Assert-Condition (-not (Test-Path -LiteralPath $legacyCacheDir)) 'legacy ValidateOnly created an encoding cache'
    Assert-TreeUnchanged $legacyFixture $beforeLegacyValidate 'legacy ValidateOnly changed installed files or aggregate XML'
    Write-TestLog 'PASS state-less legacy XML validates without writes or an installed claim'

    $legacyFirst = Invoke-Set $legacyRoot '1.9.4'
    Assert-Equal $legacyFirst.Converted 2 'legacy takeover conversion count'
    Assert-Condition $legacyFirst.RootChanged 'legacy rounding normalization did not rewrite aggregate XML'
    Assert-Condition (Test-Path -LiteralPath $legacyStatePath -PathType Leaf) 'legacy takeover did not create format state'
    Assert-Condition (Test-Path -LiteralPath $legacyCachePath -PathType Leaf) 'legacy takeover did not create the encoding cache'
    $legacyState = Get-Content -LiteralPath $legacyStatePath -Raw -Encoding UTF8 | ConvertFrom-Json
    [xml]$legacyActive = Get-Content -LiteralPath $legacyXmlPath -Raw -Encoding UTF8
    Assert-Condition ($null -ne $legacyActive.SelectSingleNode('/Resources/Unrelated[@Keep="yes"]')) 'legacy takeover removed a foreign root node'
    Assert-Equal (@($legacyActive.SelectNodes('//comment()[contains(., "foreign-")]')).Count) 2 'legacy takeover removed foreign comments'
    Assert-Equal (@($legacyActive.SelectNodes('/Resources/CustomHeightmapList/CustomHeightmap')).Count) 3 'legacy takeover removed or duplicated a node'
    foreach ($id in @('air_alpha', 'air_bravo')) {
        $node = $legacyActive.SelectSingleNode('/Resources/CustomHeightmapList/CustomHeightmap[@ID="WarThunder.Geo1.' + $id + '"]')
        Assert-Condition ($null -ne $node) "legacy aggregate node was not adopted for $id"
        $recordedHash = [string]$legacyState.individual_node_hashes.PSObject.Properties[$id].Value
        Assert-Equal $recordedHash (Get-TerrainNodeHash $node) "legacy state hash differs from the adopted XML for $id"
        Assert-Equal ([string]$node.AltitudeOffset) '-32768' "legacy altitude offset changed for $id"
        Assert-Equal $node.SelectNodes('Signed').Count 0 "legacy signed declaration remained for $id"
    }
    $legacyAlphaLongitude = [double]::Parse($legacyActive.SelectSingleNode('/Resources/CustomHeightmapList/CustomHeightmap[@ID="WarThunder.Geo1.air_alpha"]/BottomLeft/Longitude').InnerText, $invariant)
    Assert-Equal $legacyAlphaLongitude -10.0 'legacy coordinate rounding was not normalized to desired geometry'

    $legacyRepeat = Invoke-Set $legacyRoot '1.9.4'
    Assert-Condition (-not $legacyRepeat.Changed) 'adopted legacy profile rerun reported a change'
    Assert-Equal $legacyRepeat.Converted 0 'adopted legacy profile rerun reconverted data'
    Assert-Equal $legacyRepeat.Reused 2 'adopted legacy profile rerun did not reuse the cache'
    $legacyBack = Invoke-Set $legacyRoot '1.9.5'
    Assert-Condition $legacyBack.Changed 'adopted legacy profile did not switch to 1.9.5'
    [xml]$legacyRestored = Get-Content -LiteralPath $legacyXmlPath -Raw -Encoding UTF8
    Assert-Equal (@($legacyRestored.SelectNodes('/Resources/CustomHeightmapList/CustomHeightmap')).Count) 1 '1.9.5 did not remove only adopted nodes'
    Assert-Condition ($null -ne $legacyRestored.SelectSingleNode('/Resources/CustomHeightmapList/CustomHeightmap[@ID="WarThunder.Atlas1.foreign"]')) '1.9.5 removed the foreign node after legacy takeover'
    foreach ($id in @('air_alpha', 'air_bravo')) {
        Assert-Condition (Test-Path -LiteralPath (Join-Path (Join-Path $legacyRoot $id) 'CustomHeightmapList.xml') -PathType Leaf) "1.9.5 did not restore native XML for $id"
    }
    Write-TestLog 'PASS state-less legacy takeover creates cache/state, reruns, and switches to 1.9.5'

    foreach ($rejectMode in @('BadFile', 'BadGeometry', 'UnknownField', 'Duplicate')) {
        $rejectFixture = Join-Path $runtimeRoot ('legacy-reject-' + $rejectMode)
        $rejectRoot = New-LegacyAggregateFixture $rejectFixture $rejectMode
        $beforeReject = Get-TreeSnapshot $rejectFixture
        $rejectFailed = $false
        try { Invoke-Set $rejectRoot '1.9.4' | Out-Null } catch { $rejectFailed = $true }
        Assert-Condition $rejectFailed "$rejectMode legacy aggregate was accepted"
        Assert-TreeUnchanged $rejectFixture $beforeReject "$rejectMode rejection changed the installed tree"
        Assert-Condition (-not (Test-Path -LiteralPath (Join-Path $rejectRoot '_uint16'))) "$rejectMode rejection created cache output"
        Assert-Condition (-not (Test-Path -LiteralPath (Join-Path $rejectRoot 'WarThunderMaps.format.json'))) "$rejectMode rejection created format state"
    }
    Write-TestLog 'PASS legacy path, geometry, unknown-field, and duplicate candidates are rejected before writes'

    [xml]$clean = Get-Content -LiteralPath $rootPath -Raw -Encoding UTF8
    $bad = $clean.SelectSingleNode('/Resources/CustomHeightmapList/CustomHeightmap[@ID="WarThunder.Geo1.air_alpha"]')
    [void]$bad.ParentNode.RemoveChild($bad)
    Write-XmlDocument $clean $rootPath
    $rootHashBefore = Get-Sha256Hex $rootPath
    $beforeValidate = Get-TreeSnapshot $terrainRoot
    $validated = Invoke-Set $terrainRoot '1.9.4' -ValidateOnly
    Assert-Condition $validated.ValidateOnly 'ValidateOnly result is missing'
    Assert-TreeUnchanged $terrainRoot $beforeValidate 'ValidateOnly changed installed files or timestamps'
    Write-TestLog 'PASS ValidateOnly performs no writes, cache creation, or logging changes'

    $first = Invoke-Set $terrainRoot '1.9.4'
    Assert-Equal $first.Converted 2 'first activation conversion count'
    Assert-Condition (Test-Path -LiteralPath (Join-Path $parentRoot 'CustomHeightmapList.xml.before-terrain-format.bak')) 'root backup was not created once'
    Assert-Equal (Get-Sha256Hex (Join-Path $parentRoot 'CustomHeightmapList.xml.before-terrain-format.bak')) $rootHashBefore 'root backup is not the pre-activation index'
    $statePath = Join-Path $terrainRoot 'WarThunderMaps.format.json'
    $cachePath = Join-Path (Join-Path $terrainRoot '_uint16') 'cache.json'
    $state = Get-Content -LiteralPath $statePath -Raw -Encoding UTF8 | ConvertFrom-Json
    $cache = Get-Content -LiteralPath $cachePath -Raw -Encoding UTF8 | ConvertFrom-Json
    Assert-Equal $state.owner 'wrpl-acmi-terrain-format-v1' 'state owner'
    Assert-Equal $state.profile '1.9.4' 'state profile'
    Assert-Equal $cache.owner 'wrpl-acmi-terrain-uint16-cache-v1' 'cache owner'
    Assert-Equal $cache.encoding 'uint16_le_bias32768' 'cache encoding'
    Assert-Equal (Get-Sha256Hex $manifestPath) $manifestHash 'activation changed install manifest'
    foreach ($id in @('air_alpha', 'air_bravo')) {
        $mapDir = Join-Path $terrainRoot $id
        Assert-Equal (Get-Sha256Hex (Join-Path $mapDir ($id + '.data'))) $canonicalHash[$id] "canonical data changed for $id"
        Assert-Condition (-not (Test-Path -LiteralPath (Join-Path $mapDir 'CustomHeightmapList.xml'))) "visible native XML remained for $id"
        Assert-Condition (Test-Path -LiteralPath (Join-Path $mapDir 'CustomHeightmapList.native195')) "native XML backup missing for $id"
        Assert-Equal (Get-Sha256Hex (Join-Path $mapDir 'CustomHeightmapList.native195')) $xmlHash[$id] "native XML backup changed for $id"
        $encoded = Join-Path (Join-Path $terrainRoot '_uint16') ($id + '.data')
        $actual = Read-EncodedSigned $encoded
        $expected = if ($id -eq 'air_alpha') { @(-50, 0, 1234, -50) } else { @(1234, -50, 0, 0) }
        for ($sample = 0; $sample -lt $actual.Length; $sample++) { Assert-Equal $actual[$sample] $expected[$sample] "$id sample $sample" }
    }
    [xml]$activeRoot = Get-Content -LiteralPath $rootPath -Raw -Encoding UTF8
    $ownedNodes = @($activeRoot.SelectNodes('/Resources/CustomHeightmapList/CustomHeightmap'))
    Assert-Equal $ownedNodes.Count 3 'aggregate node count including preserved foreign node'
    Assert-Condition ($null -ne $activeRoot.SelectSingleNode('/Resources/Unrelated[@Keep="yes"]')) 'unrelated root node was not preserved'
    Assert-Equal (@($activeRoot.SelectNodes('//comment()')).Count) 4 'root comments were not preserved'
    foreach ($id in @('air_alpha', 'air_bravo')) {
        $node = $activeRoot.SelectSingleNode('/Resources/CustomHeightmapList/CustomHeightmap[@ID="WarThunder.Geo1.' + $id + '"]')
        Assert-Condition ($null -ne $node) "aggregate node missing for $id"
        Assert-Equal ([string]$node.File) ('WarThunderMaps/_uint16/' + $id + '.data') "aggregate file for $id"
        Assert-Equal ([string]$node.AltitudeFactor) '1' "altitude factor for $id"
        Assert-Equal ([string]$node.AltitudeOffset) '-32768' "altitude offset for $id"
        Assert-Equal $node.SelectNodes('Signed').Count 0 "signed declaration remained for $id"
        Assert-Equal (Get-NodeCornerText $node) $cornerText[$id] "coordinates changed for $id"
        Assert-Equal ([string]$node.ID) ('WarThunder.Geo1.' + $id) "map ID changed for $id"
    }
    Write-TestLog 'PASS 1.9.4 conversion, exact samples, XML parking, state, cache, and root preservation'

    $cacheTicks = (Get-Item -LiteralPath $cachePath).LastWriteTimeUtc.Ticks
    $encodedTicks = (Get-Item -LiteralPath (Join-Path (Join-Path $terrainRoot '_uint16') 'air_alpha.data')).LastWriteTimeUtc.Ticks
    $nativeTicks = (Get-Item -LiteralPath (Join-Path (Join-Path $terrainRoot 'air_alpha') 'CustomHeightmapList.native195')).LastWriteTimeUtc.Ticks
    $repeat = Invoke-Set $terrainRoot '1.9.4'
    Assert-Condition (-not $repeat.Changed) 'matching 1.9.4 rerun reported a change'
    Assert-Equal $repeat.Converted 0 'matching cache rerun converted output'
    Assert-Equal $repeat.Reused 2 'matching cache rerun did not reuse both maps'
    Assert-Equal (Get-Item -LiteralPath $cachePath).LastWriteTimeUtc.Ticks $cacheTicks 'matching rerun rewrote cache'
    Assert-Equal (Get-Item -LiteralPath (Join-Path (Join-Path $terrainRoot '_uint16') 'air_alpha.data')).LastWriteTimeUtc.Ticks $encodedTicks 'matching rerun rewrote encoded data'
    Assert-Equal (Get-Item -LiteralPath (Join-Path (Join-Path $terrainRoot 'air_alpha') 'CustomHeightmapList.native195')).LastWriteTimeUtc.Ticks $nativeTicks 'matching rerun recopied native XML'
    Write-TestLog 'PASS deterministic 1.9.4 rerun and cache timestamp reuse'

    $back195 = Invoke-Set $terrainRoot '1.9.5'
    Assert-Condition $back195.Changed '1.9.5 restore did not change state'
    $state195 = Get-Content -LiteralPath $statePath -Raw -Encoding UTF8 | ConvertFrom-Json
    Assert-Equal $state195.profile '1.9.5' 'restored profile state'
    Assert-Equal @($state195.index_owned_ids).Count 0 '1.9.5 state retained owned index IDs'
    [xml]$restoredRoot = Get-Content -LiteralPath $rootPath -Raw -Encoding UTF8
    Assert-Equal (@($restoredRoot.SelectNodes('/Resources/CustomHeightmapList/CustomHeightmap')).Count) 1 '1.9.5 root did not remove only owned nodes'
    Assert-Condition ($null -ne $restoredRoot.SelectSingleNode('/Resources/CustomHeightmapList/CustomHeightmap[@ID="WarThunder.Atlas1.foreign"]')) '1.9.5 removed foreign root node'
    Assert-Equal (@($restoredRoot.SelectNodes('//comment()')).Count) 2 '1.9.5 removed root comments'
    foreach ($id in @('air_alpha', 'air_bravo')) {
        $mapDir = Join-Path $terrainRoot $id
        Assert-Equal (Get-Sha256Hex (Join-Path $mapDir 'CustomHeightmapList.xml')) $xmlHash[$id] "restored XML differs for $id"
        Assert-Condition (Test-Path -LiteralPath (Join-Path $mapDir 'CustomHeightmapList.native195')) "native backup was removed for $id"
        Assert-Equal (Get-Sha256Hex (Join-Path $mapDir ($id + '.data'))) $canonicalHash[$id] "1.9.5 changed canonical data for $id"
        $encodedPath = Join-Path (Join-Path $terrainRoot '_uint16') ($id + '.data')
        Assert-Condition (Test-Path -LiteralPath $encodedPath -PathType Leaf) "encoded cache missing for $id"
        Assert-Condition ($null -ne $cache.maps.PSObject.Properties[$id]) "cache record missing for $id"
    }
    $repeat195 = Invoke-Set $terrainRoot '1.9.5'
    Assert-Condition (-not $repeat195.Changed) 'matching 1.9.5 rerun reported a change'
    Write-TestLog 'PASS 1.9.5 restores native XML, keeps canonical data and cache, and is deterministic'

    $encodedBeforeReturn = (Get-Item -LiteralPath (Join-Path (Join-Path $terrainRoot '_uint16') 'air_alpha.data')).LastWriteTimeUtc.Ticks
    $again194 = Invoke-Set $terrainRoot '1.9.4'
    Assert-Equal $again194.Converted 0 'switch back to 1.9.4 reconverted cached data'
    Assert-Equal (Get-Item -LiteralPath (Join-Path (Join-Path $terrainRoot '_uint16') 'air_alpha.data')).LastWriteTimeUtc.Ticks $encodedBeforeReturn 'switch back rewrote cached data'
    Assert-Equal (Get-Sha256Hex (Join-Path (Join-Path $terrainRoot 'air_alpha') 'CustomHeightmapList.native195')) $xmlHash['air_alpha'] 'switch back damaged native backup'
    Write-TestLog 'PASS 1.9.4 -> 1.9.5 -> 1.9.4 reuses native hashes and encoded cache'

    $alphaOutput = Join-Path (Join-Path $terrainRoot '_uint16') 'air_alpha.data'
    [byte[]]$corrupt = [IO.File]::ReadAllBytes($alphaOutput)
    $corrupt[0] = [byte]($corrupt[0] -bxor 1)
    [IO.File]::WriteAllBytes($alphaOutput, $corrupt)
    $rebuilt = Invoke-Set $terrainRoot '1.9.4'
    Assert-Equal $rebuilt.Converted 1 'corrupt owned payload was not rebuilt'
    Assert-Equal (Read-EncodedSigned $alphaOutput)[0] -50 'rebuilt payload sample'
    Assert-Equal (Get-Sha256Hex (Join-Path (Join-Path $terrainRoot 'air_alpha') 'air_alpha.data')) $canonicalHash['air_alpha'] 'corrupt payload repair changed canonical data'
    Write-TestLog 'PASS corrupt owned cache payload is rebuilt from verified canonical data'

    $beforeWrapper = Get-TreeSnapshot $terrainRoot
    $wrapperOutput = & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $wrapperPath -TerrainRoot $terrainRoot -Profile '1.9.5' -ValidateOnly -AllowPartial 2>&1 | Out-String
    Assert-Equal $LASTEXITCODE 0 ('wrapper ValidateOnly failed: ' + $wrapperOutput)
    Assert-TreeUnchanged $terrainRoot $beforeWrapper 'wrapper ValidateOnly changed the tree'
    Write-TestLog 'PASS Set-Tacview-Terrain-Format.ps1 ValidateOnly path'

    $oddFixture = Join-Path $runtimeRoot 'odd'
    $oddRoot = New-Fixture $oddFixture
    $oddPath = Join-Path (Join-Path $oddRoot 'air_alpha') 'air_alpha.data'
    [byte[]]$oddBytes = [byte[]]@(1, 2, 3)
    [IO.File]::WriteAllBytes($oddPath, $oddBytes)
    $beforeOdd = Get-TreeSnapshot $oddRoot
    $oddFailed = $false
    try { Invoke-Set $oddRoot '1.9.4' | Out-Null } catch { $oddFailed = $true }
    Assert-Condition $oddFailed 'odd canonical source was accepted'
    Assert-TreeUnchanged $oddRoot $beforeOdd 'odd source rejection wrote output or state'
    Write-TestLog 'PASS odd canonical source is rejected without output'

    $cacheDir = Join-Path $terrainRoot '_uint16'
    $movedCache = Join-Path $terrainRoot '_uint16-real'
    $junction = $null
    try {
        [IO.Directory]::Move($cacheDir, $movedCache)
        New-Item -ItemType Junction -Path $cacheDir -Target $movedCache | Out-Null
        $junction = Get-Item -LiteralPath $cacheDir -Force
    }
    catch {
        Write-TestLog ('SKIP cache reparse-point check: ' + $_.Exception.Message)
    }
    try {
        if ($null -ne $junction) {
            $reparseFailed = $false
            try { Invoke-Set $terrainRoot '1.9.4' -ValidateOnly | Out-Null } catch { $reparseFailed = $true }
            Assert-Condition $reparseFailed 'reparse-point cache directory was accepted'
            Write-TestLog 'PASS cache-directory reparse point is rejected'
        }
    }
    finally {
        if ($null -ne $junction -and (Test-Path -LiteralPath $cacheDir)) {
            $cacheFull = [IO.Path]::GetFullPath($cacheDir)
            $runtimePrefix = [IO.Path]::GetFullPath($runtimeRoot).TrimEnd('\', '/') + [IO.Path]::DirectorySeparatorChar
            Assert-Condition ($cacheFull.StartsWith($runtimePrefix, [StringComparison]::OrdinalIgnoreCase)) 'junction escaped test runtime'
            Assert-Condition (((Get-Item -LiteralPath $cacheFull -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) 'cleanup target is not a junction'
            $targetBefore = Get-TreeSnapshot $movedCache
            # Windows PowerShell 5.1 Remove-Item can throw NullReferenceException on junctions.
            [IO.Directory]::Delete($cacheFull, $false)
            Assert-TreeUnchanged $movedCache $targetBefore 'junction cleanup changed its target'
        }
        if ((Test-Path -LiteralPath $movedCache) -and -not (Test-Path -LiteralPath $cacheDir)) { [IO.Directory]::Move($movedCache, $cacheDir) }
    }

    $runtimeSucceeded = $true
    Write-TestLog 'PASS all terrain format tests'
}
catch {
    Write-TestLog ('FAIL terrain format tests: ' + $_.Exception.Message + [Environment]::NewLine + $_.ScriptStackTrace)
    throw
}
finally {
    if (Test-Path -LiteralPath $runtimeRoot) {
        Get-ChildItem -LiteralPath $runtimeRoot -Recurse -File -Force | ForEach-Object { [IO.File]::Delete($_.FullName) }
        $directories = @(Get-ChildItem -LiteralPath $runtimeRoot -Recurse -Directory -Force | Sort-Object FullName -Descending)
        foreach ($directory in $directories) {
            [IO.Directory]::Delete($directory.FullName, $false)
        }
        [IO.Directory]::Delete($runtimeRoot, $false)
    }
}

if (-not $runtimeSucceeded) { throw 'Terrain format tests did not complete.' }
