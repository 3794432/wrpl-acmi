[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$InstallScript,
    [Parameter(Mandatory = $true)][string]$FormatScript,
    [Parameter(Mandatory = $true)][string]$SourceRoot,
    [Parameter(Mandatory = $true)][string]$Destination,
    [Parameter(Mandatory = $true)][ValidateSet('1.9.4', '1.9.5')][string]$Profile,
    [switch]$ValidateOnly,
    [switch]$AtlasOnly,
    [switch]$AllowPartial,
    [switch]$RestoreOnly
)

$ErrorActionPreference = 'Stop'

# Hidden, non-console hosts can fail to auto-load Utility cmdlets used by the
# format script (notably Get-FileHash). Windows PowerShell always ships it.
Import-Module (Join-Path $PSHOME 'Modules/Microsoft.PowerShell.Utility/Microsoft.PowerShell.Utility.psd1') -ErrorAction Stop

function Write-WrapperError {
    param([string]$Message)
    [Console]::Error.WriteLine($Message)
}

try {
    if ($RestoreOnly) {
        $restoreArguments = @{
            TerrainRoot = $Destination
            Profile = '1.9.5'
            AllowPartial = $AllowPartial.IsPresent
        }
        & $FormatScript @restoreArguments
        if ($LASTEXITCODE -ne 0) {
            Write-WrapperError "Terrain layout restoration failed with exit code $LASTEXITCODE."
            exit 1
        }
        exit 0
    }

    $installArguments = @{
        SourceRoot = $SourceRoot
        Destination = $Destination
        ValidateOnly = $ValidateOnly.IsPresent
        AtlasOnly = $AtlasOnly.IsPresent
        AllowPartial = $AllowPartial.IsPresent
    }
    & $InstallScript @installArguments
    if ($LASTEXITCODE -ne 0) {
        Write-WrapperError "Terrain installation failed with exit code $LASTEXITCODE."
        exit 1
    }

    if ($ValidateOnly) {
        Write-Host 'Source validation only; destination files and the Tacview format profile were not changed.'
        exit 0
    }

    $formatArguments = @{
        TerrainRoot = $Destination
        Profile = $Profile
        AllowPartial = $AllowPartial.IsPresent
    }
    & $FormatScript @formatArguments
    if ($LASTEXITCODE -ne 0) {
        Write-WrapperError "Terrain format selection failed with exit code $LASTEXITCODE."
        exit 1
    }
    exit 0
}
catch {
    Write-WrapperError ($_.Exception.Message + [Environment]::NewLine + $_.ScriptStackTrace)
    exit 1
}
