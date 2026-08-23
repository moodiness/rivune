using System.Diagnostics;
using System.IO.Compression;
using System.Reflection;
using System.Runtime.InteropServices;
using Microsoft.Win32;

namespace Rivune.Bootstrapper;

internal enum DeploymentMode
{
    Install,
    Portable,
}

internal sealed record DeploymentRequest(DeploymentMode Mode, string Destination, bool CreateDesktopShortcut);
internal sealed record DeploymentResult(string ApplicationPath, string? Warning);

internal static class Deployment
{
    private const string X64PayloadName = "Rivune-x64.exe";
    private const string Arm64PayloadName = "Rivune-arm64.exe";
    private const string UninstallerPayloadName = "Rivune-Uninstall.exe";
    private const string UninstallRegistryPath = @"Software\Microsoft\Windows\CurrentVersion\Uninstall\Rivune";
    private const string DesktopShortcutMarkerName = ".rivune-desktop-shortcut";
    private const long MaximumPayloadBytes = 2L * 1024 * 1024 * 1024 - 1;

    internal static async Task<DeploymentResult> ApplyAsync(DeploymentRequest request, CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(request);
        var destination = Path.GetFullPath(request.Destination);
        if (string.IsNullOrWhiteSpace(destination) || Path.GetPathRoot(destination) == destination)
            throw new InvalidOperationException("Choose a dedicated folder for Rivune.");
        Directory.CreateDirectory(destination);

        var bundlePath = Environment.ProcessPath ?? throw new InvalidOperationException("Windows did not report the setup executable path.");
        using var archive = ZipFile.OpenRead(bundlePath);
        var expected = new HashSet<string>([X64PayloadName, Arm64PayloadName, UninstallerPayloadName], StringComparer.Ordinal);
        if (archive.Entries.Count != expected.Count)
            throw new InvalidOperationException("The Rivune setup payload is incomplete.");
        foreach (var entry in archive.Entries)
        {
            if (entry.FullName != entry.Name || !expected.Remove(entry.FullName) || entry.Length <= 0 || entry.Length > MaximumPayloadBytes)
                throw new InvalidOperationException("The Rivune setup payload is invalid.");
        }
        if (expected.Count != 0)
            throw new InvalidOperationException("The Rivune setup payload is incomplete.");

        var architecturePayload = RuntimeInformation.OSArchitecture switch
        {
            Architecture.X64 => X64PayloadName,
            Architecture.Arm64 => Arm64PayloadName,
            _ => throw new PlatformNotSupportedException($"Rivune does not support {RuntimeInformation.OSArchitecture} Windows."),
        };
        var appEntry = archive.GetEntry(architecturePayload)!;
        var applicationPath = Path.Combine(destination, "Rivune.exe");
        await ExtractAtomicallyAsync(appEntry, applicationPath, cancellationToken).ConfigureAwait(true);
        string? warning = null;
        var integrationStarted = false;
        var hadRegisteredInstall = false;

        try
        {
            if (request.Mode == DeploymentMode.Install)
            {
                hadRegisteredInstall = HasRegisteredInstall();
                var uninstallerPath = Path.Combine(destination, UninstallerPayloadName);
                integrationStarted = true;
                await ExtractAtomicallyAsync(archive.GetEntry(UninstallerPayloadName)!, uninstallerPath, cancellationToken).ConfigureAwait(true);
                RegisterUninstaller(destination, applicationPath, uninstallerPath, appEntry.Length + archive.GetEntry(UninstallerPayloadName)!.Length);
                CreateShortcut(StartMenuShortcutPath(), applicationPath);

                var desktopShortcut = DesktopShortcutPath();
                if (request.CreateDesktopShortcut)
                {
                    File.WriteAllText(Path.Combine(destination, DesktopShortcutMarkerName), string.Empty);
                    CreateShortcut(desktopShortcut, applicationPath);
                }
                else if (File.Exists(Path.Combine(destination, DesktopShortcutMarkerName)))
                {
                    if (File.Exists(desktopShortcut)) File.Delete(desktopShortcut);
                    File.Delete(Path.Combine(destination, DesktopShortcutMarkerName));
                }
            }
            else if (request.CreateDesktopShortcut)
            {
                CreateShortcut(DesktopShortcutPath(), applicationPath);
            }
        }
        catch (Exception exception)
        {
            if (integrationStarted && !hadRegisteredInstall)
                CleanupPartialIntegration(destination);
            warning = $"Rivune was deployed, but Windows integration did not complete: {exception.Message}";
        }

        return new DeploymentResult(applicationPath, warning);
    }
    private static bool HasRegisteredInstall()
    {
        using var key = Registry.CurrentUser.OpenSubKey(UninstallRegistryPath, writable: false);
        return key is not null;
    }


    private static void CleanupPartialIntegration(string destination)
    {
        TryDelete(StartMenuShortcutPath());
        if (File.Exists(Path.Combine(destination, DesktopShortcutMarkerName)))
            TryDelete(DesktopShortcutPath());
        TryDelete(Path.Combine(destination, DesktopShortcutMarkerName));
        try
        {
            Registry.CurrentUser.DeleteSubKeyTree(UninstallRegistryPath, throwOnMissingSubKey: false);
        }
        catch
        {
            // Best-effort rollback; the deployed application remains directly runnable.
        }
        TryDelete(Path.Combine(destination, UninstallerPayloadName));
    }


    internal static string DefaultInstallDirectory() =>
        Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "Programs", "Rivune");

    internal static string DefaultPortableDirectory()
    {
        var setupPath = Environment.ProcessPath;
        var parent = setupPath is null ? null : Path.GetDirectoryName(setupPath);
        return Path.Combine(string.IsNullOrWhiteSpace(parent) ? Environment.GetFolderPath(Environment.SpecialFolder.UserProfile) : parent, "Rivune Portable");
    }

    private static async Task ExtractAtomicallyAsync(ZipArchiveEntry entry, string destination, CancellationToken cancellationToken)
    {
        var temporary = Path.Combine(Path.GetDirectoryName(destination)!, $".rivune-{Guid.NewGuid():N}.tmp");
        try
        {
            await using (var input = entry.Open())
            await using (var output = new FileStream(
                             temporary,
                             FileMode.CreateNew,
                             FileAccess.Write,
                             FileShare.None,
                             128 * 1024,
                             FileOptions.Asynchronous | FileOptions.SequentialScan))
            {
                await input.CopyToAsync(output, cancellationToken).ConfigureAwait(false);
                await output.FlushAsync(cancellationToken).ConfigureAwait(false);
            }
            if (new FileInfo(temporary).Length != entry.Length)
                throw new InvalidOperationException($"The embedded file {entry.Name} was not extracted completely.");
            File.Move(temporary, destination, overwrite: true);
        }
        catch (IOException exception) when (File.Exists(destination))
        {
            throw new InvalidOperationException("Close the running Rivune application, then try again.", exception);
        }
        finally
        {
            TryDelete(temporary);
        }
    }

    private static void RegisterUninstaller(string installDirectory, string applicationPath, string uninstallerPath, long installedBytes)
    {
        var version = FileVersionInfo.GetVersionInfo(Environment.ProcessPath!).ProductVersion;
        using var key = Registry.CurrentUser.CreateSubKey(UninstallRegistryPath, writable: true)
            ?? throw new InvalidOperationException("Windows could not register the Rivune uninstaller.");
        key.SetValue("DisplayName", "Rivune");
        key.SetValue("DisplayVersion", string.IsNullOrWhiteSpace(version) ? "0.0.0" : version);
        key.SetValue("DisplayIcon", $"{applicationPath},0");
        key.SetValue("Publisher", "Rivune");
        key.SetValue("InstallLocation", installDirectory);
        key.SetValue("UninstallString", $"\"{uninstallerPath}\"");
        key.SetValue("QuietUninstallString", $"\"{uninstallerPath}\" --quiet");
        key.SetValue("URLInfoAbout", "https://github.com/moodiness/rivune");
        key.SetValue("InstallDate", DateTime.UtcNow.ToString("yyyyMMdd", System.Globalization.CultureInfo.InvariantCulture));
        key.SetValue("EstimatedSize", checked((int)Math.Max(1, installedBytes / 1024)), RegistryValueKind.DWord);
        key.SetValue("NoModify", 1, RegistryValueKind.DWord);
        key.SetValue("NoRepair", 1, RegistryValueKind.DWord);
    }

    private static string StartMenuShortcutPath() =>
        Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.Programs), "Rivune.lnk");

    private static string DesktopShortcutPath() =>
        Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.DesktopDirectory), "Rivune.lnk");

    private static void CreateShortcut(string shortcutPath, string applicationPath)
    {
        Directory.CreateDirectory(Path.GetDirectoryName(shortcutPath)!);
        var shellType = Type.GetTypeFromProgID("WScript.Shell", throwOnError: true)
            ?? throw new InvalidOperationException("Windows Shell automation is unavailable.");
        object? shell = null;
        object? shortcut = null;
        try
        {
            shell = Activator.CreateInstance(shellType) ?? throw new InvalidOperationException("Windows Shell automation is unavailable.");
            shortcut = shellType.InvokeMember(
                "CreateShortcut",
                BindingFlags.InvokeMethod,
                null,
                shell,
                [shortcutPath]);
            if (shortcut is null) throw new InvalidOperationException("Windows could not create the shortcut.");
            var shortcutType = shortcut.GetType();
            shortcutType.InvokeMember("TargetPath", BindingFlags.SetProperty, null, shortcut, [applicationPath]);
            shortcutType.InvokeMember("WorkingDirectory", BindingFlags.SetProperty, null, shortcut, [Path.GetDirectoryName(applicationPath)!]);
            shortcutType.InvokeMember("Description", BindingFlags.SetProperty, null, shortcut, ["Rivune"]);
            shortcutType.InvokeMember("IconLocation", BindingFlags.SetProperty, null, shortcut, [$"{applicationPath},0"]);
            shortcutType.InvokeMember("Save", BindingFlags.InvokeMethod, null, shortcut, null);
        }
        finally
        {
            if (shortcut is not null && Marshal.IsComObject(shortcut)) Marshal.FinalReleaseComObject(shortcut);
            if (shell is not null && Marshal.IsComObject(shell)) Marshal.FinalReleaseComObject(shell);
        }
    }
    private static bool TryDelete(string path)
    {
        try
        {
            if (File.Exists(path)) File.Delete(path);
            return true;
        }
        catch
        {
            // A stale optional shortcut or temporary file must not invalidate a successful deployment.
            return false;
        }
    }
}
