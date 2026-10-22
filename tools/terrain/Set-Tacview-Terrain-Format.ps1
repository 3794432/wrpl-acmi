[CmdletBinding()]
param(
    [string]$TerrainRoot = (Join-Path $env:APPDATA 'Tacview\Data\Terrain\Custom\WarThunderMaps'),
    [Parameter(Mandatory = $true)]
    [ValidateSet('1.9.4', '1.9.5')]
    [string]$Profile,
    [switch]$ValidateOnly,
    [switch]$AllowPartial
)

$ErrorActionPreference = 'Stop'

try {
    # A Windows PowerShell child may inherit PowerShell 7's module search path.
    Import-Module (Join-Path $PSHOME 'Modules/Microsoft.PowerShell.Utility/Microsoft.PowerShell.Utility.psd1') -ErrorAction Stop
    . (Join-Path $PSScriptRoot 'Terrain-Format.ps1')
    $result = Set-WTTerrainFormat -TerrainRoot $TerrainRoot -Profile $Profile -ValidateOnly:$ValidateOnly -AllowPartial:$AllowPartial
    $result
    exit 0
}
catch {
    [Console]::Error.WriteLine($_.Exception.Message + [string][char]10 + $_.ScriptStackTrace)
    exit 1
}
