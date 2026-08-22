$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repositoryRoot = Split-Path -Parent $PSScriptRoot
$script = Join-Path $PSScriptRoot 'windows-discovery.ps1'
$taskName = 'Rivune LAN Discovery'
$stateDirectory = Join-Path ([Environment]::GetFolderPath([Environment+SpecialFolder]::LocalApplicationData)) 'Rivune\discovery'
$installedScript = Join-Path $stateDirectory 'windows-discovery.ps1'
$configFile = Join-Path $stateDirectory 'config.json'
$statusFile = Join-Path $stateDirectory 'status.json'
$started = $false

function Assert-Fails([scriptblock] $Operation, [string] $Label) {
    $failed = $false
    try {
        & $Operation *> $null
    }
    catch {
        $failed = $true
    }
    if (-not $failed) {
        throw "$Label unexpectedly succeeded."
    }
}

if ($null -ne (Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue)) {
    throw "Refusing to disturb an existing '$taskName' scheduled task."
}

try {
    foreach ($invalidOrigin in @(
        'http://localhost:8080',
        'http://127.0.0.1:8080',
        'http://198.51.100.20:8080',
        'http://media.example.com',
        'https://user:secret@media.example.com',
        'https://media.example.com/path',
        'https://media.example.com?token=secret',
        'ftp://media.example.com'
    )) {
        Assert-Fails {
            & $script validate -Origin $invalidOrigin -Name 'Rivune' -Version '1.11.3'
        } "Unsafe discovery origin $invalidOrigin"
    }
    Assert-Fails {
        & $script validate -Origin 'https://media.example.com' -Name '' -Version '1.11.3'
    } 'Empty discovery name'
    Assert-Fails {
        & $script validate -Origin 'https://media.example.com' -Name 'Rivune' -Version 'latest'
    } 'Mutable discovery version'

    & $script validate -Origin 'https://media.example.com/' -Name 'Rivune Windows CI' -Version '1.11.3'
    & $script validate -Origin 'http://192.168.1.20:8080' -Name 'Rivune Windows CI' -Version '1.11.3'

    $startOutput = & $script start `
        -Origin 'https://media.example.com/' `
        -Name 'Rivune Windows CI' `
        -Version '1.11.3'
    $started = $true
    if ($startOutput -notmatch "Rivune LAN discovery is active as 'Rivune Windows CI' at https://media.example.com") {
        throw "Unexpected discovery start output: $startOutput"
    }

    $statusOutput = & $script status
    if ($statusOutput -notmatch "active as 'Rivune Windows CI' at https://media.example.com \(version 1.11.3, PID [0-9]+\)") {
        throw "Unexpected discovery status output: $statusOutput"
    }

    $task = Get-ScheduledTask -TaskName $taskName
    if ($task.State -ne 'Running' -or $task.Principal.RunLevel -ne 'Limited' -or $task.Principal.LogonType -ne 'Interactive') {
        throw "Discovery task is not a running, limited, interactive-user task: state=$($task.State), runLevel=$($task.Principal.RunLevel), logonType=$($task.Principal.LogonType)."
    }
    $actions = @($task.Actions)
    $expectedArguments = "-File `"$installedScript`" run"
    if ($actions.Count -ne 1 -or -not ([string] $actions[0].Arguments).Contains($expectedArguments)) {
        throw 'Discovery task does not execute the private installed publisher.'
    }

    $config = Get-Content -LiteralPath $configFile -Raw | ConvertFrom-Json
    if ($config.origin -cne 'https://media.example.com' -or $config.name -cne 'Rivune Windows CI' -or
        $config.version -cne '1.11.3' -or [int] $config.port -ne 443) {
        throw "Installed discovery configuration is invalid: $($config | ConvertTo-Json -Compress)"
    }
    $publisherStatus = Get-Content -LiteralPath $statusFile -Raw | ConvertFrom-Json
    if ($publisherStatus.origin -cne $config.origin -or $publisherStatus.name -cne $config.name -or
        $publisherStatus.version -cne $config.version -or [int] $publisherStatus.pid -le 0) {
        throw 'Publisher status does not match the private discovery configuration.'
    }

    $currentSid = [Security.Principal.WindowsIdentity]::GetCurrent().User
    $acl = Get-Acl -LiteralPath $stateDirectory
    $rules = @($acl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier]))
    if (-not $acl.AreAccessRulesProtected -or $rules.Count -ne 1 -or $rules[0].IsInherited -or
        $rules[0].IdentityReference -ne $currentSid -or
        (($rules[0].FileSystemRights -band [Security.AccessControl.FileSystemRights]::FullControl) -ne [Security.AccessControl.FileSystemRights]::FullControl)) {
        throw 'Discovery state directory is not private to the current Windows identity.'
    }

    & dotnet run `
        --project (Join-Path $repositoryRoot 'clients/windows/Rivune.DiscoveryProbe/Rivune.DiscoveryProbe.csproj') `
        --configuration Release `
        -- 'https://media.example.com' '00:00:20'
    if ($LASTEXITCODE -ne 0) {
        throw "The Windows mDNS probe failed with exit code $LASTEXITCODE."
    }

    $logOutput = & $script logs
    if ($logOutput -notmatch 'active name=Rivune Windows CI origin=https://media.example.com version=1.11.3') {
        throw 'Discovery log did not record the active bounded contract.'
    }

    & $script stop | Out-Null
    $started = $false
    if ($null -ne (Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue) -or
        (Test-Path -LiteralPath $installedScript) -or (Test-Path -LiteralPath $configFile) -or
        (Test-Path -LiteralPath $statusFile)) {
        throw 'Discovery stop left a task, publisher, configuration, or active status behind.'
    }
}
finally {
    if ($started -or $null -ne (Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue)) {
        try { & $script stop *> $null } catch {}
    }
    Remove-Item -LiteralPath $stateDirectory -Recurse -Force -ErrorAction SilentlyContinue
}
