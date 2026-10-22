$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2.0

$terrainRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$repoRoot = [IO.Path]::GetFullPath((Join-Path $terrainRoot '..\..'))
$workRoot = [IO.Path]::GetFullPath((Join-Path $repoRoot '..'))
$taskRoot = Join-Path $workRoot 'tacview-dual-version'
$logPath = Join-Path $taskRoot 'encoding-tests.log'
$modulePath = Join-Path $terrainRoot 'Terrain-194-Encoding.ps1'
$runtimeRoot = Join-Path (Join-Path $PSScriptRoot 'runtime') ('terrain-194-encoding-' + $PID + '-' + [guid]::NewGuid().ToString('N'))
$utf8NoBom = New-Object System.Text.UTF8Encoding($false)

if (-not (Test-Path -LiteralPath $taskRoot -PathType Container)) {
    New-Item -ItemType Directory -Path $taskRoot | Out-Null
}
[IO.File]::WriteAllText($logPath, '', $utf8NoBom)

function Write-TestLog {
    param([string]$Message)
    $line = '{0:yyyy-MM-dd HH:mm:ss.fff} {1}' -f (Get-Date), $Message
    Write-Host $line
    [IO.File]::AppendAllText($logPath, $line + [Environment]::NewLine, $utf8NoBom)
}

function Assert-Condition {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) { throw "ASSERT: $Message" }
}

function Assert-Equal {
    param($Actual, $Expected, [string]$Message)
    if ($Actual -ne $Expected) {
        throw "ASSERT: $Message (expected <$Expected>, actual <$Actual>)"
    }
}

function Get-Sha256Hex {
    param([string]$Path)
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

function New-Int16LeBytes {
    param([int[]]$Values)
    $bytes = New-Object byte[] ($Values.Length * 2)
    for ($index = 0; $index -lt $Values.Length; $index++) {
        $value = $Values[$index]
        Assert-Condition ($value -ge -32768 -and $value -le 32767) "fixture value $value is int16"
        $bytes[2 * $index] = [byte]($value -band 0xff)
        $bytes[(2 * $index) + 1] = [byte](($value -shr 8) -band 0xff)
    }
    return ,$bytes
}

function New-BiasEncodedBytes {
    param([byte[]]$SourceBytes)
    $encoded = [byte[]]$SourceBytes.Clone()
    for ($index = 1; $index -lt $encoded.Length; $index += 2) {
        $encoded[$index] = [byte]($encoded[$index] -bxor 0x80)
    }
    return ,$encoded
}

function Invoke-Encoding {
    param([string]$SourcePath, [string]$DestinationPath, [string]$SourceHash, [long]$Bytes)
    return Convert-WTInt16ToUInt16 -Source $SourcePath -Destination $DestinationPath -ExpectedSourceSha256 $SourceHash -ExpectedBytes $Bytes
}

function Assert-NoTemporaryFiles {
    param([string]$Directory)
    $temporary = @(Get-ChildItem -LiteralPath $Directory -Filter 'wt194-*.tmp' -File -ErrorAction SilentlyContinue)
    Assert-Condition ($temporary.Count -eq 0) "temporary output leaked into $Directory"
}

function Assert-FailureIsSideEffectFree {
    param(
        [string]$Label,
        [string]$SourcePath,
        [string]$DestinationPath,
        [string]$SourceHash,
        [long]$Bytes
    )

    $sentinel = [Text.Encoding]::ASCII.GetBytes('existing-destination-' + $Label)
    [IO.File]::WriteAllBytes($DestinationPath, $sentinel)
    $sentinelHash = Get-Sha256Hex $DestinationPath
    $sourceHashBefore = Get-Sha256Hex $SourcePath

    $failed = $false
    try {
        Invoke-Encoding $SourcePath $DestinationPath $SourceHash $Bytes | Out-Null
    }
    catch {
        $failed = $true
    }
    Assert-Condition $failed "$Label should fail"
    Assert-Condition (Test-Path -LiteralPath $DestinationPath -PathType Leaf) "$Label removed the existing destination"
    Assert-Equal (Get-Sha256Hex $DestinationPath) $sentinelHash "$Label preserved the existing destination"
    Assert-Equal (Get-Sha256Hex $SourcePath) $sourceHashBefore "$Label left the source unchanged"
    Assert-NoTemporaryFiles ([IO.Path]::GetDirectoryName($DestinationPath))

    [IO.File]::Delete($DestinationPath)
    $failed = $false
    try {
        Invoke-Encoding $SourcePath $DestinationPath $SourceHash $Bytes | Out-Null
    }
    catch {
        $failed = $true
    }
    Assert-Condition $failed "$Label should also fail with an absent destination"
    Assert-Condition (-not (Test-Path -LiteralPath $DestinationPath)) "$Label created an output after failure"
    Assert-NoTemporaryFiles ([IO.Path]::GetDirectoryName($DestinationPath))

    Write-TestLog "PASS $Label failure: source unchanged, no output, existing destination preserved"
}

$runtimeSucceeded = $false
try {
    Write-TestLog 'START Terrain-194 encoding tests'
    Assert-Condition (Test-Path -LiteralPath $modulePath -PathType Leaf) "module not found: $modulePath"
    New-Item -ItemType Directory -Path $runtimeRoot | Out-Null

    $moduleText = Get-Content -LiteralPath $modulePath -Raw -Encoding UTF8
    Assert-Condition ($moduleText -match 'MaxBufferBytes\s*=\s*4\s*\*\s*1024\s*\*\s*1024') 'module does not declare a 4 MiB stream buffer'
    Assert-Condition ($moduleText -notmatch 'ReadAllBytes|ReadAllText') 'module contains a whole-file read'
    Assert-Condition ($moduleText -match 'File\.Replace' -and $moduleText -match 'File\.Move') 'module lacks atomic publish paths'

    . $modulePath
    $firstType = 'WrplTerrain194Encoding.Terrain194Encoder' -as [type]
    . $modulePath
    $secondType = 'WrplTerrain194Encoding.Terrain194Encoder' -as [type]
    Assert-Condition ($null -ne $firstType) 'encoder type was not loaded'
    Assert-Condition ([object]::ReferenceEquals($firstType, $secondType)) 'dot-sourcing compiled a second encoder type'
    Write-TestLog 'PASS embedded encoder type is reused after repeated dot-sourcing'

    # Exercise all 65,536 signed int16 values and verify the exact biased LE bytes.
    $domainSource = Join-Path $runtimeRoot 'int16-domain.bin'
    $domainDestination = Join-Path $runtimeRoot 'int16-domain.data'
    $domainValues = New-Object int[] 65536
    for ($sample = 0; $sample -lt 65536; $sample++) { $domainValues[$sample] = $sample - 32768 }
    [byte[]]$domainRaw = New-Int16LeBytes $domainValues
    [IO.File]::WriteAllBytes($domainSource, $domainRaw)
    $domainHash = Get-Sha256Hex $domainSource
    $domainResult = Invoke-Encoding $domainSource $domainDestination $domainHash $domainRaw.Length
    Assert-Equal $domainResult.encoding 'uint16_le_bias32768' 'encoding label'
    Assert-Equal $domainResult.altitude_factor 1 'altitude factor'
    Assert-Equal $domainResult.altitude_offset -32768 'altitude offset'
    Assert-Equal $domainResult.data_bytes 131072 'domain output byte count'
    Assert-Equal $domainResult.sample_count 65536 'domain sample count'
    Assert-Equal $domainResult.min_height_m -32768 'domain minimum height'
    Assert-Equal $domainResult.max_height_m 32767 'domain maximum height'
    Assert-Equal $domainResult.source_sha256 $domainHash 'domain source hash result'
    Assert-Equal (Get-Sha256Hex $domainSource) $domainHash 'domain source remained unchanged'
    Assert-Equal $domainResult.data_sha256 (Get-Sha256Hex $domainDestination) 'domain output hash result'

    [byte[]]$domainOutput = [IO.File]::ReadAllBytes($domainDestination)
    Assert-Equal $domainOutput.Length $domainRaw.Length 'domain output preserves length'
    for ($sample = 0; $sample -lt 65536; $sample++) {
        $signed = $sample - 32768
        $expectedUnsigned = $signed + 32768
        $actualUnsigned = [int]$domainOutput[2 * $sample] -bor ([int]$domainOutput[(2 * $sample) + 1] -shl 8)
        if ($actualUnsigned -ne $expectedUnsigned) {
            throw "ASSERT: signed $signed encoded as $actualUnsigned instead of $expectedUnsigned"
        }
        Assert-Equal $domainOutput[(2 * $sample) + 1] ([byte](([int]$domainRaw[(2 * $sample) + 1]) -bxor 0x80)) "high byte XOR for signed $signed"
    }
    Assert-Equal $domainOutput[0] 0x00 'LE low byte for -32768'
    Assert-Equal $domainOutput[1] 0x00 'LE high byte for -32768'
    Assert-Equal $domainOutput[131070] 0xff 'LE low byte for 32767'
    Assert-Equal $domainOutput[131071] 0xff 'LE high byte for 32767'
    Write-TestLog 'PASS complete int16 domain, little-endian bytes, unsigned bias formula, source hash, and result metadata'

    # A value whose source high byte already has bit 7 set verifies XOR and word endianness directly.
    $endianSource = Join-Path $runtimeRoot 'endian.bin'
    $endianDestination = Join-Path $runtimeRoot 'endian.data'
    [byte[]]$endianRaw = New-Int16LeBytes ([int[]]@(258))
    Assert-Equal $endianRaw[0] 2 'signed 258 low byte is LE'
    Assert-Equal $endianRaw[1] 1 'signed 258 high byte is LE'
    [IO.File]::WriteAllBytes($endianSource, $endianRaw)
    $endianResult = Invoke-Encoding $endianSource $endianDestination (Get-Sha256Hex $endianSource) 2
    [byte[]]$endianOutput = [IO.File]::ReadAllBytes($endianDestination)
    Assert-Equal $endianOutput[0] 2 'encoded 258 low byte'
    Assert-Equal $endianOutput[1] 0x81 'encoded 258 high byte'
    Assert-Equal ([int]$endianOutput[0] -bor ([int]$endianOutput[1] -shl 8)) 33026 'encoded 258 unsigned value'
    Assert-Equal $endianResult.sample_count 1 'single-sample count'
    Write-TestLog 'PASS explicit little-endian and high-byte XOR encoding for 258'

    $negativeSource = Join-Path $runtimeRoot 'negative.bin'
    $negativeDestination = Join-Path $runtimeRoot 'negative.data'
    [byte[]]$negativeRaw = New-Int16LeBytes ([int[]]@(-1, -32768, -20000, -5))
    [IO.File]::WriteAllBytes($negativeSource, $negativeRaw)
    $negativeResult = Invoke-Encoding $negativeSource $negativeDestination (Get-Sha256Hex $negativeSource) $negativeRaw.Length
    Assert-Equal $negativeResult.min_height_m -32768 'negative-only minimum'
    Assert-Equal $negativeResult.max_height_m -1 'negative-only maximum'
    [byte[]]$negativeOutput = [IO.File]::ReadAllBytes($negativeDestination)
    $negativeValues = @(-1, -32768, -20000, -5)
    for ($index = 0; $index -lt $negativeValues.Length; $index++) {
        $actual = [int]$negativeOutput[2 * $index] -bor ([int]$negativeOutput[(2 * $index) + 1] -shl 8)
        Assert-Equal $actual ($negativeValues[$index] + 32768) "negative sample $index"
    }
    Write-TestLog 'PASS negative-only ranges and decoded signed values'

    $oddSource = Join-Path $runtimeRoot 'odd.bin'
    [IO.File]::WriteAllBytes($oddSource, [byte[]]@(0x01, 0x02, 0x03))
    Assert-FailureIsSideEffectFree 'odd-length' $oddSource (Join-Path $runtimeRoot 'odd.data') (Get-Sha256Hex $oddSource) 3

    $truncatedSource = Join-Path $runtimeRoot 'truncated.bin'
    [IO.File]::WriteAllBytes($truncatedSource, [byte[]]@(0x01, 0x02, 0x03, 0x04))
    Assert-FailureIsSideEffectFree 'truncated' $truncatedSource (Join-Path $runtimeRoot 'truncated.data') (Get-Sha256Hex $truncatedSource) 6

    $wrongHashSource = Join-Path $runtimeRoot 'wrong-hash.bin'
    [IO.File]::WriteAllBytes($wrongHashSource, [byte[]]@(0x01, 0x02, 0x03, 0x84))
    $wrongHash = ('0' * 64)
    Assert-FailureIsSideEffectFree 'wrong-hash' $wrongHashSource (Join-Path $runtimeRoot 'wrong-hash.data') $wrongHash 4

    $samePathSource = Join-Path $runtimeRoot 'Same-Path.bin'
    [byte[]]$samePathRaw = New-Int16LeBytes ([int[]]@(-1234, 5678))
    [IO.File]::WriteAllBytes($samePathSource, $samePathRaw)
    $samePathHash = Get-Sha256Hex $samePathSource
    $samePathDestination = $samePathSource.ToUpperInvariant()
    $samePathFailed = $false
    try {
        Invoke-Encoding $samePathSource $samePathDestination $samePathHash $samePathRaw.Length | Out-Null
    }
    catch {
        $samePathFailed = $true
    }
    Assert-Condition $samePathFailed 'case-insensitive same source/destination path was accepted'
    Assert-Equal (Get-Sha256Hex $samePathSource) $samePathHash 'same-path rejection left source unchanged'
    Assert-NoTemporaryFiles $runtimeRoot
    Write-TestLog 'PASS case-insensitive source/destination rejection without source modification'

    # Write just over 8 MiB in repeated chunks. The file crosses two 4 MiB stream boundaries.
    $largeSource = Join-Path $runtimeRoot 'large-8mib.bin'
    $largeDestination = Join-Path $runtimeRoot 'large-8mib.data'
    $largeExpected = Join-Path $runtimeRoot 'large-8mib.expected'
    [byte[]]$largePattern = $domainRaw
    [byte[]]$largeEncodedPattern = New-BiasEncodedBytes $largePattern
    $largePatternCount = 64
    $largeTail = New-Int16LeBytes ([int[]]@(-12345))
    [byte[]]$largeEncodedTail = New-BiasEncodedBytes $largeTail
    $sourceStream = [IO.File]::Open($largeSource, [IO.FileMode]::Create, [IO.FileAccess]::Write, [IO.FileShare]::None)
    $expectedStream = [IO.File]::Open($largeExpected, [IO.FileMode]::Create, [IO.FileAccess]::Write, [IO.FileShare]::None)
    try {
        for ($copy = 0; $copy -lt $largePatternCount; $copy++) {
            $sourceStream.Write($largePattern, 0, $largePattern.Length)
            $expectedStream.Write($largeEncodedPattern, 0, $largeEncodedPattern.Length)
        }
        $sourceStream.Write($largeTail, 0, $largeTail.Length)
        $expectedStream.Write($largeEncodedTail, 0, $largeEncodedTail.Length)
        $sourceStream.Flush($true)
        $expectedStream.Flush($true)
    }
    finally {
        $sourceStream.Dispose()
        $expectedStream.Dispose()
    }
    $largeBytes = ([long]$largePattern.Length * $largePatternCount) + $largeTail.Length
    Assert-Condition ($largeBytes -gt (8L * 1024 * 1024)) 'large fixture exceeds 8 MiB'
    $largeHash = Get-Sha256Hex $largeSource
    [IO.File]::WriteAllBytes($largeDestination, [Text.Encoding]::ASCII.GetBytes('large-existing-destination'))
    $largeResult = Invoke-Encoding $largeSource $largeDestination $largeHash $largeBytes
    Assert-Equal $largeResult.data_bytes $largeBytes 'large output byte count'
    Assert-Equal $largeResult.sample_count (($largePatternCount * 65536) + 1) 'large sample count'
    Assert-Equal $largeResult.min_height_m -32768 'large minimum height'
    Assert-Equal $largeResult.max_height_m 32767 'large maximum height'
    Assert-Equal (Get-Sha256Hex $largeSource) $largeHash 'large source remained unchanged'
    Assert-Equal $largeResult.source_sha256 $largeHash 'large source hash result'
    Assert-Equal $largeResult.data_sha256 (Get-Sha256Hex $largeExpected) 'large byte-exact encoded hash'
    Assert-Equal (Get-Sha256Hex $largeDestination) (Get-Sha256Hex $largeExpected) 'large destination replaced atomically with verified data'

    $spotOffsets = New-Object long[] 3
    $fourMiB = [long](4L * 1024 * 1024)
    $spotOffsets[0] = $fourMiB - 4L
    $spotOffsets[1] = $fourMiB
    $spotOffsets[2] = $largeBytes - 4L
    [byte[]]$spot = New-Object byte[] 8
    [byte[]]$expectedSpot = New-Object byte[] 8
    $spotStream = $null
    $expectedSpotStream = $null
    try {
        $spotStream = [IO.File]::OpenRead($largeDestination)
        $expectedSpotStream = [IO.File]::OpenRead($largeExpected)
        foreach ($offset in $spotOffsets) {
            [void]$spotStream.Seek($offset, [IO.SeekOrigin]::Begin)
            [void]$expectedSpotStream.Seek($offset, [IO.SeekOrigin]::Begin)
            [void]$spotStream.Read($spot, 0, $spot.Length)
            [void]$expectedSpotStream.Read($expectedSpot, 0, $expectedSpot.Length)
            for ($byteIndex = 0; $byteIndex -lt $spot.Length; $byteIndex++) {
                Assert-Equal $spot[$byteIndex] $expectedSpot[$byteIndex] "large boundary byte at offset $($offset + $byteIndex)"
            }
        }
    }
    finally {
        if ($null -ne $spotStream) { $spotStream.Dispose() }
        if ($null -ne $expectedSpotStream) { $expectedSpotStream.Dispose() }
    }
    Write-TestLog 'PASS >8 MiB streaming conversion across 4 MiB boundaries and existing-destination replacement'
    Write-TestLog 'PASS all Terrain-194 encoding tests'
    $runtimeSucceeded = $true
}
catch {
    Write-TestLog ('FAIL Terrain-194 encoding tests: ' + $_.Exception.Message)
    throw
}
finally {
    if (Test-Path -LiteralPath $runtimeRoot -PathType Container) {
        Get-ChildItem -LiteralPath $runtimeRoot -File -Force | ForEach-Object {
            try { [IO.File]::Delete($_.FullName) }
            catch { Write-TestLog ('WARN cleanup could not delete ' + $_.FullName + ': ' + $_.Exception.Message) }
        }
        if (Test-Path -LiteralPath $runtimeRoot -PathType Container) {
            [IO.Directory]::Delete($runtimeRoot, $false)
        }
    }
}

if (-not $runtimeSucceeded) { throw 'Terrain-194 encoding tests did not complete.' }
