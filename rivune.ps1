[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [string] $Command = 'help',

    [Parameter(Position = 1, ValueFromRemainingArguments = $true)]
    [string[]] $CommandArguments = @()
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$rootDirectory = Split-Path -Parent $PSCommandPath
$environmentFile = Join-Path $rootDirectory '.env'
$composeFile = Join-Path $rootDirectory 'compose.yaml'
$discoveryScript = Join-Path $rootDirectory 'scripts\windows-discovery.ps1'

function Show-Usage {
    @'
Usage: .\rivune.ps1 COMMAND [ARGUMENTS]

Commands:
  up
  down
  restart
  pull
  status
  logs [rivune|postgres|discovery]
  help
'@
}

function Assert-NoArguments([string] $Name) {
    if ($CommandArguments.Count -ne 0) {
        throw "$Name does not accept arguments."
    }
}

function Assert-EnvironmentFile {
    if (-not (Test-Path -LiteralPath $environmentFile -PathType Leaf)) {
        throw 'A regular .env file is required; run .\scripts\create-env.ps1 first.'
    }
    $item = Get-Item -LiteralPath $environmentFile -Force
    if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -or $item.Length -gt 1048576) {
        throw 'The .env path must be a regular file no larger than 1 MiB.'
    }
}

function Get-LiteralEnvironmentValue([string] $Key, [string] $Default = '') {
    $prefix = "$Key="
    $values = [Collections.Generic.List[string]]::new()
    foreach ($line in [IO.File]::ReadLines($environmentFile)) {
        if ($line.StartsWith($prefix, [StringComparison]::Ordinal)) {
            $null = $values.Add($line.Substring($prefix.Length).TrimEnd("`r"))
        }
    }
    if ($values.Count -gt 1) {
        throw "The .env file defines $Key more than once."
    }
    if ($values.Count -eq 0) {
        return $Default
    }
    $value = $values[0]
    if ($value.Length -ge 2 -and
        (($value[0] -eq '"' -and $value[$value.Length - 1] -eq '"') -or
         ($value[0] -eq "'" -and $value[$value.Length - 1] -eq "'"))) {
        $value = $value.Substring(1, $value.Length - 2)
    }
    return $value
}

function Get-DiscoveryConfiguration {
    return [pscustomobject]@{
        Origin = Get-LiteralEnvironmentValue 'RIVUNE_DISCOVERY_URL'
        Name = Get-LiteralEnvironmentValue 'RIVUNE_DISCOVERY_NAME' 'Rivune'
        Version = Get-LiteralEnvironmentValue 'RIVUNE_VERSION'
    }
}

function Invoke-Compose([string[]] $Arguments) {
    $controlledVariables = @(
        'COMPOSE_FILE',
        'COMPOSE_ENV_FILES',
        'COMPOSE_PROJECT_NAME',
        'COMPOSE_PROFILES',
        'COMPOSE_PATH_SEPARATOR'
    )
    $saved = @{}
    foreach ($name in $controlledVariables) {
        $saved[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
        [Environment]::SetEnvironmentVariable($name, $null, 'Process')
    }
    Push-Location $rootDirectory
    try {
        & docker compose --env-file .env -f compose.yaml @Arguments
        if ($LASTEXITCODE -ne 0) {
            throw "Docker Compose failed with exit code $LASTEXITCODE."
        }
    }
    finally {
        Pop-Location
        foreach ($name in $controlledVariables) {
            [Environment]::SetEnvironmentVariable($name, $saved[$name], 'Process')
        }
    }
}

function Assert-DiscoveryConfiguration($Configuration) {
    if ([string]::IsNullOrEmpty($Configuration.Origin)) {
        return
    }
    & $discoveryScript validate `
        -Origin $Configuration.Origin `
        -Name $Configuration.Name `
        -Version $Configuration.Version
}

function Start-HostDiscovery($Configuration) {
    if ([string]::IsNullOrEmpty($Configuration.Origin)) {
        & $discoveryScript stop | Out-Null
        return
    }
    & $discoveryScript start `
        -Origin $Configuration.Origin `
        -Name $Configuration.Name `
        -Version $Configuration.Version
}

switch ($Command.ToLowerInvariant()) {
    'up' {
        Assert-NoArguments 'up'
        Assert-EnvironmentFile
        $discovery = Get-DiscoveryConfiguration
        Assert-DiscoveryConfiguration $discovery
        Invoke-Compose @('up', '-d')
        try {
            Start-HostDiscovery $discovery
        }
        catch {
            try { Invoke-Compose @('down') } catch {}
            throw
        }
    }
    'down' {
        Assert-NoArguments 'down'
        Assert-EnvironmentFile
        Invoke-Compose @('down')
        & $discoveryScript stop
    }
    'restart' {
        Assert-NoArguments 'restart'
        Assert-EnvironmentFile
        $discovery = Get-DiscoveryConfiguration
        Assert-DiscoveryConfiguration $discovery
        Invoke-Compose @('restart')
        Start-HostDiscovery $discovery
    }
    'pull' {
        Assert-NoArguments 'pull'
        Assert-EnvironmentFile
        Invoke-Compose @('pull')
    }
    'status' {
        Assert-NoArguments 'status'
        Assert-EnvironmentFile
        Invoke-Compose @('ps')
        try {
            & $discoveryScript status
        }
        catch {
            Write-Output $_.Exception.Message
        }
    }
    'logs' {
        if ($CommandArguments.Count -gt 1) {
            throw 'logs accepts at most one service.'
        }
        $service = if ($CommandArguments.Count -eq 0) { '' } else { $CommandArguments[0] }
        if ($service -notin @('', 'rivune', 'postgres', 'discovery')) {
            throw 'logs service must be rivune, postgres, or discovery.'
        }
        Assert-EnvironmentFile
        if ($service -eq 'discovery') {
            & $discoveryScript logs
        }
        elseif ($service -eq '') {
            Invoke-Compose @('logs')
        }
        else {
            Invoke-Compose @('logs', $service)
        }
    }
    { $_ -in @('help', '-h', '--help') } {
        Assert-NoArguments 'help'
        Show-Usage
    }
    default {
        throw "Unknown command: $Command. Run .\rivune.ps1 help for usage."
    }
}
