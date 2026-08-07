<#
.SYNOPSIS
Builds and pushes the Rivune development image for AMD64 and ARM64 to GHCR.

.DESCRIPTION
Requires a clean Git working tree, Docker Desktop with Buildx, and an active
GitHub CLI login whose token can write packages. Publishes the mutable `dev`
tag and an immutable `dev-sha-<commit>` tag with provenance and an SBOM.

.EXAMPLE
./scripts/publish-dev-image.ps1

.EXAMPLE
./scripts/publish-dev-image.ps1 -DryRun

.EXAMPLE
./scripts/publish-dev-image.ps1 -SkipLogin -Repository owner/repository
#>

[CmdletBinding()]
param(
    [ValidatePattern('^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$')]
    [string] $Repository = 'moodiness/rivune',

    [ValidatePattern('^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$')]
    [string] $Tag = 'dev',

    [ValidateNotNullOrEmpty()]
    [string[]] $Platforms = @('linux/amd64', 'linux/arm64'),

    [switch] $SkipLogin,
    [switch] $DryRun
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

function Assert-Command([string] $Name) {
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "Required command is unavailable: $Name"
    }
}

function Invoke-Native([string] $FilePath, [string[]] $ArgumentList) {
    & $FilePath @ArgumentList
    if ($LASTEXITCODE -ne 0) {
        throw "Command failed with exit code ${LASTEXITCODE}: $FilePath $($ArgumentList -join ' ')"
    }
}

Assert-Command 'docker'
Assert-Command 'git'
if (-not $SkipLogin -and -not $DryRun) {
    Assert-Command 'gh'
}

$repositoryRoot = Split-Path -Parent $PSScriptRoot
Push-Location $repositoryRoot
try {
    $commitSha = (& git rev-parse HEAD).Trim()
    if ($LASTEXITCODE -ne 0 -or $commitSha -notmatch '^[0-9a-f]{40}$') {
        throw 'Unable to resolve the current Git commit.'
    }

    if (-not $DryRun) {
        $changes = @(& git status --porcelain --untracked-files=normal)
        if ($LASTEXITCODE -ne 0) {
            throw 'Unable to inspect the Git working tree.'
        }
        if ($changes.Count -ne 0) {
            throw 'Refusing to publish from a dirty working tree. Commit or stash the changes first.'
        }
    }

    $registry = 'ghcr.io'
    $normalizedRepository = $Repository.ToLowerInvariant()
    $owner = $normalizedRepository.Split('/')[0]
    $shortSha = $commitSha.Substring(0, 7)
    $image = "$registry/$normalizedRepository"
    $devTag = "${image}:${Tag}"
    $shaTag = "${image}:${Tag}-sha-${shortSha}"
    $platformList = $Platforms -join ','

    $buildArguments = @(
        'buildx', 'build',
        '--platform', $platformList,
        '--file', 'server/Dockerfile',
        '--build-arg', "VERSION=dev-$commitSha",
        '--tag', $devTag,
        '--tag', $shaTag,
        '--provenance=mode=max',
        '--sbom=true',
        '--push',
        '.'
    )

    if ($DryRun) {
        Write-Output "Repository root: $repositoryRoot"
        Write-Output "Commit: $commitSha"
        Write-Output "Platforms: $platformList"
        Write-Output "Tags: $devTag, $shaTag"
        Write-Output ("Command: docker " + ($buildArguments -join ' '))
        return
    }

    Invoke-Native 'docker' @('buildx', 'inspect', '--bootstrap')

    if (-not $SkipLogin) {
        & gh auth token | & docker login $registry --username $owner --password-stdin
        if ($LASTEXITCODE -ne 0) {
            throw "Unable to authenticate Docker to $registry with the active GitHub CLI account."
        }
    }

    Invoke-Native 'docker' $buildArguments
    Invoke-Native 'docker' @('buildx', 'imagetools', 'inspect', $devTag)

    Write-Output "Published multiarchitecture image: $devTag"
    Write-Output "Immutable development tag: $shaTag"
}
finally {
    Pop-Location
}
