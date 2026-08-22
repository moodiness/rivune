[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet('', 'validate', 'start', 'stop', 'status', 'logs', 'run')]
    [string] $Command = '',

    [string] $Origin,
    [string] $Name = 'Rivune',
    [string] $Version
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$taskName = 'Rivune LAN Discovery'
$stateDirectory = Join-Path ([Environment]::GetFolderPath([Environment+SpecialFolder]::LocalApplicationData)) 'Rivune\discovery'
$installedScript = Join-Path $stateDirectory 'windows-discovery.ps1'
$configFile = Join-Path $stateDirectory 'config.json'
$statusFile = Join-Path $stateDirectory 'status.json'
$logFile = Join-Path $stateDirectory 'discovery.log'

function Test-IsWindows {
    return [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT
}

function Test-PrivateAddress([Net.IPAddress] $Address) {
    $bytes = $Address.GetAddressBytes()
    if ($bytes.Length -eq 4) {
        return $bytes[0] -eq 10 -or
            ($bytes[0] -eq 172 -and $bytes[1] -ge 16 -and $bytes[1] -le 31) -or
            ($bytes[0] -eq 192 -and $bytes[1] -eq 168)
    }
    return $bytes.Length -eq 16 -and (($bytes[0] -band 0xfe) -eq 0xfc)
}

function Test-UnusableAddress([Net.IPAddress] $Address) {
    if ([Net.IPAddress]::IsLoopback($Address) -or $Address.Equals([Net.IPAddress]::Any) -or
        $Address.Equals([Net.IPAddress]::IPv6Any) -or $Address.IsIPv6LinkLocal -or
        $Address.IsIPv6Multicast) {
        return $true
    }
    $bytes = $Address.GetAddressBytes()
    return $bytes.Length -eq 4 -and
        (($bytes[0] -eq 169 -and $bytes[1] -eq 254) -or $bytes[0] -ge 224)
}

function Get-ValidatedDiscoveryConfig([string] $RawOrigin, [string] $RawName, [string] $RawVersion) {
    if ([string]::IsNullOrWhiteSpace($RawOrigin) -or $RawOrigin -match '[\x00-\x20]') {
        throw 'RIVUNE_DISCOVERY_URL is required and must not contain whitespace or control characters.'
    }

    $parsed = $null
    if (-not [Uri]::TryCreate($RawOrigin, [UriKind]::Absolute, [ref] $parsed) -or
        ($parsed.Scheme -cne 'http' -and $parsed.Scheme -cne 'https') -or
        [string]::IsNullOrEmpty($parsed.Host) -or
        -not [string]::IsNullOrEmpty($parsed.UserInfo) -or
        ($parsed.AbsolutePath -cne '' -and $parsed.AbsolutePath -cne '/') -or
        -not [string]::IsNullOrEmpty($parsed.Query) -or
        -not [string]::IsNullOrEmpty($parsed.Fragment)) {
        throw 'RIVUNE_DISCOVERY_URL must be an absolute HTTP(S) origin without credentials, path, query, or fragment.'
    }

    $serverHost = $parsed.DnsSafeHost.Trim([char[]] @('[', ']'))
    if ($serverHost -ieq 'localhost') {
        throw 'RIVUNE_DISCOVERY_URL must identify a LAN-reachable or HTTPS server, not loopback.'
    }
    $address = $null
    if ([Net.IPAddress]::TryParse($serverHost, [ref] $address)) {
        if (Test-UnusableAddress $address) {
            throw 'RIVUNE_DISCOVERY_URL must identify a LAN-reachable or HTTPS server, not loopback, unspecified, multicast, or link-local.'
        }
        if ($parsed.Scheme -ceq 'http' -and -not (Test-PrivateAddress $address)) {
            throw 'RIVUNE_DISCOVERY_URL must use HTTPS unless its host is a private-network IP address.'
        }
    }
    elseif ($parsed.Scheme -ceq 'http') {
        throw 'RIVUNE_DISCOVERY_URL must use HTTPS unless its host is a private-network IP address.'
    }

    $normalizedOrigin = $parsed.GetLeftPart([UriPartial]::Authority)
    if ([Text.Encoding]::UTF8.GetByteCount("url=$normalizedOrigin") -gt 255) {
        throw 'RIVUNE_DISCOVERY_URL is too long for a DNS-SD TXT record.'
    }
    $normalizedName = $RawName.Trim()
    if ([string]::IsNullOrWhiteSpace($normalizedName) -or $normalizedName -match '[\x00-\x1f\x7f]' -or
        [Text.Encoding]::UTF8.GetByteCount($normalizedName) -gt 63) {
        throw 'RIVUNE_DISCOVERY_NAME must be 1 to 63 UTF-8 bytes without control characters.'
    }
    if ($RawVersion -notmatch '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$') {
        throw 'RIVUNE_VERSION must be a stable numeric version such as 1.11.2.'
    }

    return [pscustomobject]@{
        origin = $normalizedOrigin
        name = $normalizedName
        version = $RawVersion
        port = $parsed.Port
    }
}

function Set-PrivateDirectory([string] $Path) {
    if (Test-Path -LiteralPath $Path) {
        $item = Get-Item -LiteralPath $Path -Force
        if (-not $item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
            throw "Refusing unsafe discovery state path: $Path"
        }
    }
    else {
        New-Item -ItemType Directory -Path $Path -Force | Out-Null
    }

    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $acl = [Security.AccessControl.DirectorySecurity]::new()
    $acl.SetOwner($identity.User)
    $acl.SetAccessRuleProtection($true, $false)
    $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new(
        $identity.User,
        [Security.AccessControl.FileSystemRights]::FullControl,
        [Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit',
        [Security.AccessControl.PropagationFlags]::None,
        [Security.AccessControl.AccessControlType]::Allow
    ))
    Set-Acl -LiteralPath $Path -AclObject $acl
}

function Write-JsonAtomically([string] $Path, [object] $Value) {
    $temporary = Join-Path $stateDirectory ('.' + [IO.Path]::GetFileName($Path) + '.' + [Guid]::NewGuid().ToString('N') + '.tmp')
    try {
        [IO.File]::WriteAllText(
            $temporary,
            ($Value | ConvertTo-Json -Compress),
            [Text.UTF8Encoding]::new($false)
        )
        Move-Item -LiteralPath $temporary -Destination $Path -Force
    }
    finally {
        Remove-Item -LiteralPath $temporary -Force -ErrorAction SilentlyContinue
    }
}

function Write-DiscoveryLog([string] $Message) {
    try {
        Set-PrivateDirectory $stateDirectory
        if ((Test-Path -LiteralPath $logFile) -and (Get-Item -LiteralPath $logFile).Length -gt 262144) {
            Remove-Item -LiteralPath $logFile -Force
        }
        $safeMessage = $Message -replace '[\r\n]+', ' '
        [IO.File]::AppendAllText(
            $logFile,
            ('{0:o} {1}{2}' -f [DateTimeOffset]::UtcNow, $safeMessage, [Environment]::NewLine),
            [Text.UTF8Encoding]::new($false)
        )
    }
    catch {
        Write-Error "Could not write the private discovery log: $($_.Exception.Message)" -ErrorAction Continue
    }
}

function Get-DiscoveryTask {
    return Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
}

function Assert-ManagedTask($Task) {
    $actions = @($Task.Actions)
    $expectedArguments = "-File `"$installedScript`" run"
    if ($actions.Count -ne 1 -or -not ([string] $actions[0].Arguments).Contains($expectedArguments)) {
        throw "Refusing to replace or remove an unmanaged scheduled task named '$taskName'."
    }
}

function Stop-DiscoveryTask([switch] $RemoveFiles) {
    $task = Get-DiscoveryTask
    if ($null -ne $task) {
        Assert-ManagedTask $task
        Stop-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
        $deadline = [DateTime]::UtcNow.AddSeconds(5)
        do {
            Start-Sleep -Milliseconds 100
            $task = Get-DiscoveryTask
        } while ($null -ne $task -and $task.State -eq 'Running' -and [DateTime]::UtcNow -lt $deadline)
        if ($null -ne $task -and $task.State -eq 'Running') {
            throw "The Windows LAN discovery publisher did not stop within five seconds."
        }
        Unregister-ScheduledTask -TaskName $taskName -Confirm:$false
    }
    Remove-Item -LiteralPath $statusFile -Force -ErrorAction SilentlyContinue
    if ($RemoveFiles) {
        Remove-Item -LiteralPath $configFile, $installedScript -Force -ErrorAction SilentlyContinue
    }
}
function Wait-DiscoveryActive {
    $deadline = [DateTime]::UtcNow.AddSeconds(20)
    $sawRunning = $false
    do {
        if (Test-Path -LiteralPath $statusFile) {
            try {
                $status = Get-Content -LiteralPath $statusFile -Raw | ConvertFrom-Json
                $process = Get-Process -Id ([int] $status.pid) -ErrorAction Stop
                if (-not $process.HasExited) {
                    return $status
                }
            }
            catch {
                # The publisher may still be replacing its status file.
            }
        }
        $task = Get-DiscoveryTask
        if ($null -eq $task) {
            break
        }
        if ($task.State -eq 'Running') {
            $sawRunning = $true
        }
        elseif ($sawRunning -or $task.State -notin @('Queued', 'Ready')) {
            break
        }
        Start-Sleep -Milliseconds 200
    } while ([DateTime]::UtcNow -lt $deadline)

    $result = $null
    try { $result = (Get-ScheduledTaskInfo -TaskName $taskName).LastTaskResult } catch {}
    $suffix = if ($null -eq $result) { '' } else { " LastTaskResult=$result." }
    throw "The Windows LAN discovery publisher did not become active.$suffix Run '.\scripts\windows-discovery.ps1 logs' for the private local log."
}

function Start-Discovery([object] $Config) {
    if (-not (Test-IsWindows)) {
        throw 'Windows LAN discovery can only be installed on Windows.'
    }
    $existing = Get-DiscoveryTask
    if ($null -ne $existing) {
        Assert-ManagedTask $existing
    }
    Stop-DiscoveryTask -RemoveFiles
    Set-PrivateDirectory $stateDirectory

    $temporaryScript = Join-Path $stateDirectory ('.windows-discovery.' + [Guid]::NewGuid().ToString('N') + '.tmp')
    try {
        Copy-Item -LiteralPath $PSCommandPath -Destination $temporaryScript
        Move-Item -LiteralPath $temporaryScript -Destination $installedScript -Force
        Write-JsonAtomically $configFile $Config
        Remove-Item -LiteralPath $statusFile -Force -ErrorAction SilentlyContinue

        $powerShellExecutable = (Get-Process -Id $PID).Path
        if ([string]::IsNullOrWhiteSpace($powerShellExecutable) -or $powerShellExecutable.Contains('"')) {
            throw 'Could not determine a safe PowerShell executable for the scheduled task.'
        }
        $arguments = "-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File `"$installedScript`" run"
        $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
        $action = New-ScheduledTaskAction -Execute $powerShellExecutable -Argument $arguments -WorkingDirectory $stateDirectory
        $trigger = New-ScheduledTaskTrigger -AtLogOn -User $identity.Name
        $principal = New-ScheduledTaskPrincipal -UserId $identity.User.Value -LogonType Interactive -RunLevel Limited
        $settings = New-ScheduledTaskSettingsSet `
            -AllowStartIfOnBatteries `
            -DontStopIfGoingOnBatteries `
            -StartWhenAvailable `
            -ExecutionTimeLimit ([TimeSpan]::Zero) `
            -RestartCount 5 `
            -RestartInterval ([TimeSpan]::FromMinutes(1))
        Register-ScheduledTask `
            -TaskName $taskName `
            -Action $action `
            -Trigger $trigger `
            -Principal $principal `
            -Settings $settings `
            -Description 'Publishes this self-hosted Rivune server through local-network DNS-SD.' `
            -Force | Out-Null
        Start-ScheduledTask -TaskName $taskName
        $status = Wait-DiscoveryActive
        Write-Output "Rivune LAN discovery is active as '$($status.name)' at $($status.origin)."
    }
    catch {
        try { Stop-DiscoveryTask -RemoveFiles } catch {}
        throw
    }
    finally {
        Remove-Item -LiteralPath $temporaryScript -Force -ErrorAction SilentlyContinue
    }
}

function Show-DiscoveryStatus {
    $task = Get-DiscoveryTask
    if ($null -eq $task) {
        throw 'Rivune LAN discovery is not installed.'
    }
    Assert-ManagedTask $task
    if ($task.State -ne 'Running' -or -not (Test-Path -LiteralPath $statusFile)) {
        $info = Get-ScheduledTaskInfo -TaskName $taskName
        throw "Rivune LAN discovery is not active. State=$($task.State); LastTaskResult=$($info.LastTaskResult)."
    }
    $status = Get-Content -LiteralPath $statusFile -Raw | ConvertFrom-Json
    $process = Get-Process -Id ([int] $status.pid) -ErrorAction SilentlyContinue
    if ($null -eq $process -or $process.HasExited) {
        throw 'Rivune LAN discovery has a stale publisher status.'
    }
    Write-Output "Rivune LAN discovery is active as '$($status.name)' at $($status.origin) (version $($status.version), PID $($status.pid))."
}

function Add-NativePublisherType {
    if ('Rivune.WindowsDiscovery.NativeRegistration' -as [type]) {
        return
    }
    Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;
using System.Threading;

namespace Rivune.WindowsDiscovery
{
    public sealed class NativeRegistration : IDisposable
    {
        private const uint DnsQueryRequestVersion1 = 1;
        private const uint DnsRequestPending = 0x2522;

        [UnmanagedFunctionPointer(CallingConvention.Winapi)]
        private delegate void Completion(uint status, IntPtr context, IntPtr instance);

        [StructLayout(LayoutKind.Sequential)]
        private struct RegisterRequest
        {
            public uint Version;
            public uint InterfaceIndex;
            public IntPtr ServiceInstance;
            public IntPtr CompletionCallback;
            public IntPtr QueryContext;
            public IntPtr Credentials;
            [MarshalAs(UnmanagedType.Bool)]
            public bool UnicastEnabled;
        }


        [DllImport("dnsapi.dll", CharSet = CharSet.Unicode, SetLastError = true)]
        private static extern IntPtr DnsServiceConstructInstance(
            string serviceName,
            string hostName,
            IntPtr ipv4Address,
            IntPtr ipv6Address,
            ushort port,
            ushort priority,
            ushort weight,
            uint propertyCount,
            [MarshalAs(UnmanagedType.LPArray, ArraySubType = UnmanagedType.LPWStr, SizeParamIndex = 7)] string[] keys,
            [MarshalAs(UnmanagedType.LPArray, ArraySubType = UnmanagedType.LPWStr, SizeParamIndex = 7)] string[] values);

        [DllImport("dnsapi.dll")]
        private static extern void DnsServiceFreeInstance(IntPtr instance);

        [DllImport("dnsapi.dll")]
        private static extern uint DnsServiceRegister(ref RegisterRequest request, IntPtr cancel);

        [DllImport("dnsapi.dll")]
        private static extern uint DnsServiceDeRegister(ref RegisterRequest request, IntPtr cancel);

        private readonly ManualResetEvent completionEvent = new ManualResetEvent(false);
        private readonly Completion completion;
        private RegisterRequest request;
        private IntPtr instance;
        private uint completionStatus = UInt32.MaxValue;
        private bool registered;
        private bool operationPending;
        private bool disposed;

        public NativeRegistration(string serviceName, string hostName, ushort port, string[] keys, string[] values)
        {
            if (keys == null || values == null || keys.Length != values.Length)
                throw new ArgumentException("DNS-SD property keys and values must have equal lengths.");
            completion = OnCompletion;
            instance = DnsServiceConstructInstance(
                serviceName, hostName, IntPtr.Zero, IntPtr.Zero, port, 0, 0,
                checked((uint)keys.Length), keys, values);
            if (instance == IntPtr.Zero)
                throw new Win32Exception(Marshal.GetLastWin32Error(), "Could not construct the Windows DNS-SD service instance.");
            request = new RegisterRequest
            {
                Version = DnsQueryRequestVersion1,
                InterfaceIndex = 0,
                ServiceInstance = instance,
                CompletionCallback = Marshal.GetFunctionPointerForDelegate(completion),
                QueryContext = IntPtr.Zero,
                Credentials = IntPtr.Zero,
                UnicastEnabled = false
            };
        }

        public void Register()
        {
            ThrowIfDisposed();
            if (registered || operationPending)
                throw new InvalidOperationException("The DNS-SD service is already registering or registered.");
            completionStatus = UInt32.MaxValue;
            completionEvent.Reset();
            operationPending = true;
            uint result = DnsServiceRegister(ref request, IntPtr.Zero);
            if (result != DnsRequestPending)
            {
                operationPending = false;
                throw new Win32Exception(unchecked((int)result), "Windows rejected the DNS-SD registration request.");
            }
            completionEvent.WaitOne();
            if (completionStatus != 0)
                throw new Win32Exception(unchecked((int)completionStatus), "Windows could not register the DNS-SD service.");
            registered = true;
        }

        private void OnCompletion(uint status, IntPtr context, IntPtr callbackInstance)
        {
            completionStatus = status;
            operationPending = false;
            if (callbackInstance != IntPtr.Zero)
                DnsServiceFreeInstance(callbackInstance);
            completionEvent.Set();
        }

        private void ThrowIfDisposed()
        {
            if (disposed)
                throw new ObjectDisposedException("NativeRegistration");
        }

        public void Dispose()
        {
            if (disposed)
                return;
            disposed = true;
            bool canFreeInstance = !operationPending;
            if (registered)
            {
                completionStatus = UInt32.MaxValue;
                completionEvent.Reset();
                operationPending = true;
                uint result = DnsServiceDeRegister(ref request, IntPtr.Zero);
                if (result == DnsRequestPending)
                {
                    canFreeInstance = completionEvent.WaitOne(5000);
                }
                else
                {
                    operationPending = false;
                    canFreeInstance = true;
                }
                registered = false;
            }
            if (canFreeInstance && instance != IntPtr.Zero)
            {
                DnsServiceFreeInstance(instance);
                instance = IntPtr.Zero;
            }
            if (!operationPending)
                completionEvent.Dispose();
            GC.KeepAlive(completion);
        }
    }
}
'@
}

function Escape-DnsServiceLabel([string] $Value) {
    return $Value.Replace('\', '\\').Replace('.', '\.')
}

function Run-Publisher {
    if (-not (Test-IsWindows)) {
        throw 'The Windows discovery publisher can only run on Windows.'
    }
    Set-PrivateDirectory $stateDirectory
    if (-not (Test-Path -LiteralPath $configFile) -or (Get-Item -LiteralPath $configFile).Length -gt 4096) {
        throw 'The private Windows discovery configuration is absent or invalid.'
    }
    $saved = Get-Content -LiteralPath $configFile -Raw | ConvertFrom-Json
    $config = Get-ValidatedDiscoveryConfig ([string] $saved.origin) ([string] $saved.name) ([string] $saved.version)
    if ([int] $saved.port -ne $config.port) {
        throw 'The private Windows discovery configuration port is inconsistent with its origin.'
    }

    Add-NativePublisherType
    $serviceName = '{0}._rivune._tcp.local' -f (Escape-DnsServiceLabel $config.name)
    $hostName = [Environment]::MachineName + '.local'
    $registration = [Rivune.WindowsDiscovery.NativeRegistration]::new(
        $serviceName,
        $hostName,
        [uint16] $config.port,
        [string[]] @('url', 'protocol', 'version'),
        [string[]] @($config.origin, '20', $config.version)
    )
    try {
        $registration.Register()
        Write-JsonAtomically $statusFile ([ordered]@{
            pid = $PID
            startedAt = [DateTimeOffset]::UtcNow.ToString('o')
            origin = $config.origin
            name = $config.name
            version = $config.version
        })
        Write-DiscoveryLog "active name=$($config.name) origin=$($config.origin) version=$($config.version)"
        while ($true) {
            Start-Sleep -Seconds 3600
        }
    }
    finally {
        Remove-Item -LiteralPath $statusFile -Force -ErrorAction SilentlyContinue
        $registration.Dispose()
    }
}

if ($Command -eq '') {
    throw 'Usage: windows-discovery.ps1 validate|start -Origin URL -Name NAME -Version X.Y.Z | stop | status | logs'
}

switch ($Command) {
    'validate' {
        $null = Get-ValidatedDiscoveryConfig $Origin $Name $Version
    }
    'start' {
        $config = Get-ValidatedDiscoveryConfig $Origin $Name $Version
        Start-Discovery $config
    }
    'stop' {
        if (-not (Test-IsWindows)) { throw 'Windows LAN discovery can only be managed on Windows.' }
        Stop-DiscoveryTask -RemoveFiles
        Write-Output 'Rivune LAN discovery is stopped.'
    }
    'status' {
        if (-not (Test-IsWindows)) { throw 'Windows LAN discovery can only be inspected on Windows.' }
        Show-DiscoveryStatus
    }
    'logs' {
        if (Test-Path -LiteralPath $logFile) {
            Get-Content -LiteralPath $logFile
        }
        else {
            Write-Output 'No Rivune LAN discovery log exists.'
        }
    }
    'run' {
        try {
            Run-Publisher
        }
        catch {
            Write-DiscoveryLog "failed error=$($_.Exception.Message)"
            throw
        }
    }
}
