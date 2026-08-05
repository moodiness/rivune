$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repositoryRoot = Split-Path -Parent $PSScriptRoot
$testRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("rivune-create-env-" + [Guid]::NewGuid().ToString('N'))

function New-TestRoot([string] $Name) {
    $caseRoot = Join-Path $testRoot $Name
    $scriptsDirectory = Join-Path $caseRoot 'scripts'
    New-Item -ItemType Directory -Path $scriptsDirectory -Force | Out-Null
    Copy-Item -LiteralPath (Join-Path $repositoryRoot 'scripts/create-env.ps1') -Destination $scriptsDirectory
    Copy-Item -LiteralPath (Join-Path $repositoryRoot '.env.example') -Destination $caseRoot
    return $caseRoot
}

try {
    $creationRoot = New-TestRoot 'creation'
    & (Join-Path $creationRoot 'scripts/create-env.ps1') | Out-Null

    $environmentFile = Join-Path $creationRoot '.env'
    $exampleFile = Join-Path $creationRoot '.env.example'
    if ((Get-Content -LiteralPath $environmentFile -Raw) -cne (Get-Content -LiteralPath $exampleFile -Raw)) {
        throw 'create-env.ps1 did not copy .env.example'
    }

    $currentSid = [Security.Principal.WindowsIdentity]::GetCurrent().User
    $acl = Get-Acl -LiteralPath $environmentFile
    if (-not $acl.AreAccessRulesProtected) {
        throw 'create-env.ps1 left DACL inheritance enabled'
    }

    $rules = @($acl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier]))
    if ($rules.Count -ne 1) {
        throw "create-env.ps1 left an unexpected number of DACL rules: $($rules.Count)"
    }
    $rule = $rules[0]
    if ($rule.IsInherited -or
        $rule.AccessControlType -ne [Security.AccessControl.AccessControlType]::Allow -or
        $rule.IdentityReference -ne $currentSid -or
        (($rule.FileSystemRights -band [Security.AccessControl.FileSystemRights]::FullControl) -ne [Security.AccessControl.FileSystemRights]::FullControl)) {
        throw 'create-env.ps1 did not grant only the current identity full control'
    }

    $existingRoot = New-TestRoot 'existing'
    $existingFile = Join-Path $existingRoot '.env'
    [System.IO.File]::WriteAllText($existingFile, "existing-secret`n")
    try {
        & (Join-Path $existingRoot 'scripts/create-env.ps1') *> $null
        throw 'create-env.ps1 overwrote an existing .env'
    }
    catch {
        if ($_.Exception.Message -eq 'create-env.ps1 overwrote an existing .env') {
            throw
        }
    }
    if ([System.IO.File]::ReadAllText($existingFile) -cne "existing-secret`n") {
        throw 'create-env.ps1 changed an existing .env'
    }
}
finally {
    Remove-Item -LiteralPath $testRoot -Recurse -Force -ErrorAction SilentlyContinue
}
