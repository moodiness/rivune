$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$rootDirectory = Split-Path -Parent $PSScriptRoot
$sourceFile = Join-Path $rootDirectory '.env.example'
$destinationFile = Join-Path $rootDirectory '.env'
$created = $false

try {
    if (Test-Path -LiteralPath $destinationFile) {
        throw "Refusing to overwrite existing .env path: $destinationFile"
    }

    $stream = [System.IO.File]::Open(
        $destinationFile,
        [System.IO.FileMode]::CreateNew,
        [System.IO.FileAccess]::Write,
        [System.IO.FileShare]::None
    )
    $stream.Dispose()
    $created = $true

    Copy-Item -LiteralPath $sourceFile -Destination $destinationFile

    $currentSid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $aclOutput = & icacls.exe $destinationFile '/inheritance:r' '/grant:r' "*$($currentSid):(F)" 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to restrict .env permissions: $($aclOutput -join [Environment]::NewLine)"
    }

    $created = $false
    Write-Output "Created private environment file: $destinationFile"
}
finally {
    if ($created) {
        Remove-Item -LiteralPath $destinationFile -Force -ErrorAction Stop
    }
}
