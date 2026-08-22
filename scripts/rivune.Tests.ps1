$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repositoryRoot = Split-Path -Parent $PSScriptRoot
$testRoot = Join-Path ([IO.Path]::GetTempPath()) ('rivune-windows-operator-' + [Guid]::NewGuid().ToString('N'))
$caseRoot = Join-Path $testRoot 'root'
$binDirectory = Join-Path $testRoot 'bin'
$dockerLog = Join-Path $testRoot 'docker.log'
$environmentLog = Join-Path $testRoot 'docker-environment.log'
$discoveryLog = Join-Path $testRoot 'discovery.log'

function Write-Environment([string] $DiscoveryUrl = 'https://media.example.com') {
    [IO.File]::WriteAllText(
        (Join-Path $caseRoot '.env'),
        @"
RIVUNE_POSTGRES_SUPERUSER_PASSWORD=superuser-secret
RIVUNE_DATABASE_PASSWORD=database-secret
RIVUNE_RESTORE_PASSWORD=restore-secret
RIVUNE_SETUP_TOKEN=setup-secret
RIVUNE_ENCRYPTION_KEYS=1:1212121212121212121212121212121212121212121212121212121212121212
RIVUNE_PUBLIC_URL=https://media.example.com
RIVUNE_DISCOVERY_URL=$DiscoveryUrl
RIVUNE_DISCOVERY_NAME=Living room
RIVUNE_VERSION=1.11.4
"@,
        [Text.UTF8Encoding]::new($false)
    )
}

function Assert-Lines([string] $Path, [string[]] $Expected, [string] $Label) {
    $actual = if (Test-Path -LiteralPath $Path) { @([IO.File]::ReadAllLines($Path)) } else { @() }
    if (($actual -join "`n") -cne ($Expected -join "`n")) {
        throw "$Label differs. Expected [$($Expected -join ', ')], actual [$($actual -join ', ')]."
    }
}

function Reset-Logs {
    Remove-Item -LiteralPath $dockerLog, $environmentLog, $discoveryLog -Force -ErrorAction SilentlyContinue
}

function Assert-Compose([string[]] $Arguments) {
    Assert-Lines $dockerLog (@('compose', '--env-file', '.env', '-f', 'compose.yaml') + $Arguments) 'Docker Compose arguments'
    Assert-Lines $environmentLog @(
        'COMPOSE_FILE=',
        'COMPOSE_ENV_FILES=',
        'COMPOSE_PROJECT_NAME=',
        'COMPOSE_PROFILES=',
        'COMPOSE_PATH_SEPARATOR='
    ) 'Controlled Compose environment'
}

function Assert-Fails([scriptblock] $Operation, [string] $Label) {
    $failed = $false
    try { & $Operation *> $null } catch { $failed = $true }
    if (-not $failed) { throw "$Label unexpectedly succeeded." }
}

try {
    New-Item -ItemType Directory -Path (Join-Path $caseRoot 'scripts'), $binDirectory -Force | Out-Null
    Copy-Item -LiteralPath (Join-Path $repositoryRoot 'rivune.ps1') -Destination $caseRoot
    [IO.File]::WriteAllText((Join-Path $caseRoot 'compose.yaml'), "services: {}`n")
    Write-Environment

    [IO.File]::WriteAllText(
        (Join-Path $caseRoot 'scripts\windows-discovery.ps1'),
        @'
param(
    [Parameter(Position = 0)] [string] $Command,
    [string] $Origin = '',
    [string] $Name = '',
    [string] $Version = ''
)
[IO.File]::AppendAllText(
    $env:RIVUNE_TEST_DISCOVERY_LOG,
    (@($Command, $Origin, $Name, $Version) -join [Environment]::NewLine) + [Environment]::NewLine
)
if ($Command -eq 'status') { Write-Output 'fake discovery status' }
'@,
        [Text.UTF8Encoding]::new($false)
    )
    [IO.File]::WriteAllText(
        (Join-Path $binDirectory 'docker.cmd'),
        @'
@echo off
for %%A in (%*) do @echo %%~A>>"%RIVUNE_TEST_DOCKER_LOG%"
(
  echo COMPOSE_FILE=%COMPOSE_FILE%
  echo COMPOSE_ENV_FILES=%COMPOSE_ENV_FILES%
  echo COMPOSE_PROJECT_NAME=%COMPOSE_PROJECT_NAME%
  echo COMPOSE_PROFILES=%COMPOSE_PROFILES%
  echo COMPOSE_PATH_SEPARATOR=%COMPOSE_PATH_SEPARATOR%
)>>"%RIVUNE_TEST_ENVIRONMENT_LOG%"
exit /b 0
'@,
        [Text.ASCIIEncoding]::new()
    )

    $oldPath = $env:PATH
    $env:PATH = "$binDirectory;$oldPath"
    $env:RIVUNE_TEST_DOCKER_LOG = $dockerLog
    $env:RIVUNE_TEST_ENVIRONMENT_LOG = $environmentLog
    $env:RIVUNE_TEST_DISCOVERY_LOG = $discoveryLog
    $env:COMPOSE_FILE = 'attacker-compose.yaml'
    $env:COMPOSE_ENV_FILES = 'attacker.env'
    $env:COMPOSE_PROJECT_NAME = 'attacker'
    $env:COMPOSE_PROFILES = 'attacker'
    $env:COMPOSE_PATH_SEPARATOR = ';'

    Reset-Logs
    & (Join-Path $caseRoot 'rivune.ps1') up | Out-Null
    Assert-Compose @('up', '-d')
    Assert-Lines $discoveryLog @(
        'validate', 'https://media.example.com', 'Living room', '1.11.4',
        'start', 'https://media.example.com', 'Living room', '1.11.4'
    ) 'Windows discovery up delegation'

    Reset-Logs
    & (Join-Path $caseRoot 'rivune.ps1') restart | Out-Null
    Assert-Compose @('restart')
    Assert-Lines $discoveryLog @(
        'validate', 'https://media.example.com', 'Living room', '1.11.4',
        'start', 'https://media.example.com', 'Living room', '1.11.4'
    ) 'Windows discovery restart delegation'

    Reset-Logs
    & (Join-Path $caseRoot 'rivune.ps1') status | Out-Null
    Assert-Compose @('ps')
    Assert-Lines $discoveryLog @('status', '', '', '') 'Windows discovery status delegation'

    Reset-Logs
    & (Join-Path $caseRoot 'rivune.ps1') logs discovery | Out-Null
    Assert-Lines $dockerLog @() 'Discovery logs Docker isolation'
    Assert-Lines $discoveryLog @('logs', '', '', '') 'Windows discovery log delegation'

    Reset-Logs
    & (Join-Path $caseRoot 'rivune.ps1') logs postgres | Out-Null
    Assert-Compose @('logs', 'postgres')
    Assert-Lines $discoveryLog @() 'Postgres logs discovery isolation'

    Reset-Logs
    & (Join-Path $caseRoot 'rivune.ps1') down | Out-Null
    Assert-Compose @('down')
    Assert-Lines $discoveryLog @('stop', '', '', '') 'Windows discovery down delegation'

    Write-Environment ''
    Reset-Logs
    & (Join-Path $caseRoot 'rivune.ps1') up | Out-Null
    Assert-Compose @('up', '-d')
    Assert-Lines $discoveryLog @('stop', '', '', '') 'Disabled Windows discovery cleanup'

    Reset-Logs
    Assert-Fails { & (Join-Path $caseRoot 'rivune.ps1') logs 'postgres;ignored' } 'Hostile log service'
    Assert-Lines $dockerLog @() 'Rejected service Docker isolation'
    Add-Content -LiteralPath (Join-Path $caseRoot '.env') -Value "`nRIVUNE_VERSION=9.9.9"
    Reset-Logs
    Assert-Fails { & (Join-Path $caseRoot 'rivune.ps1') up } 'Duplicate discovery version'
    Assert-Lines $dockerLog @() 'Duplicate environment Docker isolation'
}
finally {
    if ($null -ne (Get-Variable oldPath -ErrorAction SilentlyContinue)) { $env:PATH = $oldPath }
    Remove-Item Env:RIVUNE_TEST_DOCKER_LOG -ErrorAction SilentlyContinue
    Remove-Item Env:RIVUNE_TEST_ENVIRONMENT_LOG -ErrorAction SilentlyContinue
    Remove-Item Env:RIVUNE_TEST_DISCOVERY_LOG -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $testRoot -Recurse -Force -ErrorAction SilentlyContinue
}
