using System.Diagnostics;
using System.Security.Cryptography;
using System.Text.RegularExpressions;

namespace Rivune.App;

internal sealed record PortableUpdateApplyRequest(
    string SourcePath,
    string TargetPath,
    int ParentProcessId,
    long Size,
    string Sha256,
    string ExpectedVersion);

internal abstract record PortableUpdateStartupCommand
{
    private PortableUpdateStartupCommand() { }

    internal sealed record Apply(PortableUpdateApplyRequest Request) : PortableUpdateStartupCommand;
    internal sealed record Cleanup(string SourcePath) : PortableUpdateStartupCommand;
    internal sealed record ReportError(string Message) : PortableUpdateStartupCommand;
}
internal static partial class PortableAppUpdate
{
    internal const string ApplySwitch = "--rivune-apply-update";
    internal const string CleanupSwitch = "--rivune-cleanup-update";
    internal const string ErrorSwitch = "--rivune-update-error";
    private const int ParentExitTimeoutSeconds = 90;

    internal static PortableUpdateStartupCommand? ParseStartupCommand(
        IReadOnlyList<string> arguments,
        string processPath)
    {
        ArgumentNullException.ThrowIfNull(arguments);
        var currentPath = NormalizeAbsolutePath(processPath, "The running Rivune executable path is invalid.");
        if (arguments.Count == 0) return null;

        if (arguments[0].Equals(CleanupSwitch, StringComparison.Ordinal))
        {
            if (arguments.Count != 2)
                throw new InvalidOperationException("The update cleanup arguments are invalid.");
            var cleanupSourcePath = NormalizeAbsolutePath(arguments[1], "The update cleanup path is invalid.");
            if (PathsEqual(cleanupSourcePath, currentPath) || !IsPathWithinDirectory(cleanupSourcePath, Path.GetTempPath()))
                throw new InvalidOperationException("The update cleanup path is not trusted.");
            return new PortableUpdateStartupCommand.Cleanup(cleanupSourcePath);
        }

        if (arguments[0].Equals(ErrorSwitch, StringComparison.Ordinal))
        {
            if (arguments.Count != 2 || string.IsNullOrWhiteSpace(arguments[1]) || arguments[1].Length > 2_000)
                throw new InvalidOperationException("The update error arguments are invalid.");
            return new PortableUpdateStartupCommand.ReportError(arguments[1]);
        }

        if (!arguments[0].Equals(ApplySwitch, StringComparison.Ordinal)) return null;
        if (arguments.Count != 13 ||
            arguments[1] != "--source" || arguments[3] != "--target" ||
            arguments[5] != "--wait-pid" || arguments[7] != "--size" ||
            arguments[9] != "--sha256" || arguments[11] != "--expected-version")
        {
            throw new InvalidOperationException("The portable update arguments are invalid.");
        }

        var sourcePath = NormalizeAbsolutePath(arguments[2], "The portable update source path is invalid.");
        var targetPath = NormalizeAbsolutePath(arguments[4], "The portable update target path is invalid.");
        if (!PathsEqual(sourcePath, currentPath) || PathsEqual(sourcePath, targetPath))
            throw new InvalidOperationException("The portable update paths are invalid.");
        if (!HaveMatchingAllowedFileNames(sourcePath, targetPath))
            throw new InvalidOperationException(
                "The portable update source and target must have the same supported Rivune executable name.");
        if (!int.TryParse(arguments[6], out var parentProcessId) || parentProcessId <= 0 || parentProcessId == Environment.ProcessId)
            throw new InvalidOperationException("The portable update process identifier is invalid.");
        if (!long.TryParse(arguments[8], out var size) || size is <= 0 or > int.MaxValue)
            throw new InvalidOperationException("The portable update size is invalid.");
        if (!Sha256Pattern().IsMatch(arguments[10]))
            throw new InvalidOperationException("The portable update verification metadata is invalid.");
        var expectedVersion = ValidateExpectedVersion(arguments[12]);

        return new PortableUpdateStartupCommand.Apply(new(
            sourcePath,
            targetPath,
            parentProcessId,
            size,
            arguments[10],
            expectedVersion));
    }

    internal static ProcessStartInfo PrepareHandoff(
        string downloadedExecutable,
        string currentExecutable,
        int currentProcessId,
        long size,
        string sha256,
        string expectedVersion)
    {
        var sourcePath = NormalizeAbsolutePath(downloadedExecutable, "The downloaded update path is invalid.");
        var targetPath = NormalizeAbsolutePath(currentExecutable, "The running Rivune executable path is invalid.");
        if (!File.Exists(sourcePath))
            throw new InvalidOperationException("The verified update file no longer exists. Download it again.");
        if (!File.Exists(targetPath))
            throw new InvalidOperationException("The running Rivune executable could not be found.");
        if (PathsEqual(sourcePath, targetPath))
            throw new InvalidOperationException("The update cannot replace its download source.");
        if (!HaveMatchingAllowedFileNames(sourcePath, targetPath))
        {
            throw new InvalidOperationException(
                "Portable updates require the app and downloaded file to have the same supported Rivune executable name.");
        }
        if (!IsPathWithinDirectory(sourcePath, Path.GetTempPath()))
            throw new InvalidOperationException("The verified update file is not in the trusted temporary directory.");
        if (currentProcessId <= 0)
            throw new InvalidOperationException("The running Rivune process identifier is invalid.");
        if (size is <= 0 or > int.MaxValue || !Sha256Pattern().IsMatch(sha256))
            throw new InvalidOperationException("The verified update metadata is invalid.");
        var validatedExpectedVersion = ValidateExpectedVersion(expectedVersion);

        EnsureTargetDirectoryIsWritable(targetPath);

        var startInfo = new ProcessStartInfo(sourcePath) { UseShellExecute = false };
        foreach (var argument in new[]
                 {
                     ApplySwitch,
                     "--source", sourcePath,
                     "--target", targetPath,
                     "--wait-pid", currentProcessId.ToString(System.Globalization.CultureInfo.InvariantCulture),
                     "--size", size.ToString(System.Globalization.CultureInfo.InvariantCulture),
                     "--sha256", sha256,
                     "--expected-version", validatedExpectedVersion,
                 })
        {
            startInfo.ArgumentList.Add(argument);
        }
        return startInfo;
    }

    internal static void StartHandoff(
        string downloadedExecutable,
        string currentExecutable,
        int currentProcessId,
        long size,
        string sha256,
        string expectedVersion)
    {
        var startInfo = PrepareHandoff(
            downloadedExecutable,
            currentExecutable,
            currentProcessId,
            size,
            sha256,
            expectedVersion);
        if (Process.Start(startInfo) is null)
            throw new InvalidOperationException("Windows could not start the verified update. Rivune is still unchanged; try again.");
    }

    internal static async Task ApplyAsync(
        PortableUpdateApplyRequest request,
        CancellationToken cancellationToken = default,
        Action<string, string>? versionVerifier = null)
    {
        ArgumentNullException.ThrowIfNull(request);
        versionVerifier ??= VerifyProductVersion;
        ValidateApplyRequest(request);
        await VerifyFileAsync(request.SourcePath, request.Size, request.Sha256, cancellationToken).ConfigureAwait(false);
        versionVerifier(request.SourcePath, request.ExpectedVersion);
        await WaitForParentExitAsync(request, cancellationToken).ConfigureAwait(false);

        var directory = Path.GetDirectoryName(request.TargetPath)!;
        var nonce = Guid.NewGuid().ToString("N");
        var stagedPath = Path.Combine(directory, $".rivune-update-{nonce}.exe");
        var backupPath = Path.Combine(directory, $".rivune-backup-{nonce}.tmp");
        var replaced = false;
        try
        {
            File.Copy(request.SourcePath, stagedPath, overwrite: false);
            await VerifyFileAsync(stagedPath, request.Size, request.Sha256, cancellationToken).ConfigureAwait(false);
            versionVerifier(stagedPath, request.ExpectedVersion);
            File.Replace(stagedPath, request.TargetPath, backupPath, ignoreMetadataErrors: true);
            replaced = true;

            var startInfo = new ProcessStartInfo(request.TargetPath) { UseShellExecute = false };
            startInfo.ArgumentList.Add(CleanupSwitch);
            startInfo.ArgumentList.Add(request.SourcePath);
            if (Process.Start(startInfo) is null)
                throw new InvalidOperationException("Windows did not start the updated Rivune executable.");

            TryDeleteFile(backupPath);
        }
        catch (Exception updateException)
        {
            if (replaced && File.Exists(backupPath))
            {
                try
                {
                    File.Replace(backupPath, request.TargetPath, null, ignoreMetadataErrors: true);
                    replaced = false;
                }
                catch (Exception rollbackException)
                {
                    throw new InvalidOperationException(
                        $"The update failed and Windows could not restore the previous executable. The recovery copy is at {backupPath}.",
                        new AggregateException(updateException, rollbackException));
                }
            }
            throw;
        }
        finally
        {
            TryDeleteFile(stagedPath);
            if (!replaced) TryDeleteFile(backupPath);
        }
    }

    internal static async Task DeleteTemporarySourceAsync(string sourcePath)
    {
        for (var attempt = 0; attempt < 20; attempt++)
        {
            try
            {
                if (File.Exists(sourcePath)) File.Delete(sourcePath);
                var directory = Path.GetDirectoryName(sourcePath);
                if (directory is not null && Directory.Exists(directory) && !Directory.EnumerateFileSystemEntries(directory).Any())
                    Directory.Delete(directory);
                return;
            }
            catch when (attempt < 19)
            {
                await Task.Delay(250).ConfigureAwait(false);
            }
            catch
            {
                return;
            }
        }
    }

    private static void ValidateApplyRequest(PortableUpdateApplyRequest request)
    {
        var command = ParseStartupCommand(
            [
                ApplySwitch,
                "--source", request.SourcePath,
                "--target", request.TargetPath,
                "--wait-pid", request.ParentProcessId.ToString(System.Globalization.CultureInfo.InvariantCulture),
                "--size", request.Size.ToString(System.Globalization.CultureInfo.InvariantCulture),
                "--sha256", request.Sha256,
                "--expected-version", request.ExpectedVersion,
            ],
            request.SourcePath);
        if (command is not PortableUpdateStartupCommand.Apply)
            throw new InvalidOperationException("The portable update request is invalid.");
        if (!File.Exists(request.SourcePath) || !File.Exists(request.TargetPath))
            throw new InvalidOperationException("The portable update source or target no longer exists.");
    }

    private static async Task WaitForParentExitAsync(
        PortableUpdateApplyRequest request,
        CancellationToken cancellationToken)
    {
        Process parent;
        try
        {
            parent = Process.GetProcessById(request.ParentProcessId);
        }
        catch (ArgumentException)
        {
            return;
        }

        string? parentPath;
        try
        {
            if (parent.HasExited)
            {
                parent.Dispose();
                return;
            }
            parentPath = parent.MainModule?.FileName;
        }
        catch (InvalidOperationException)
        {
            parent.Dispose();
            return;
        }
        if (parentPath is null || !PathsEqual(parentPath, request.TargetPath))
        {
            parent.Dispose();
            throw new InvalidOperationException("The update target does not match the Rivune process being replaced.");
        }

        using (parent)
        using (var timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken))
        {
            timeout.CancelAfter(TimeSpan.FromSeconds(ParentExitTimeoutSeconds));
            try
            {
                await parent.WaitForExitAsync(timeout.Token).ConfigureAwait(false);
            }
            catch (OperationCanceledException) when (!cancellationToken.IsCancellationRequested)
            {
                throw new InvalidOperationException("Rivune did not exit in time. Close it completely, then try the update again.");
            }
        }
    }

    private static async Task VerifyFileAsync(
        string path,
        long expectedSize,
        string expectedSha256,
        CancellationToken cancellationToken)
    {
        var info = new FileInfo(path);
        if (!info.Exists || info.Length != expectedSize)
            throw new InvalidOperationException("The portable update file size changed before it could be applied.");
        await using var stream = new FileStream(
            path,
            FileMode.Open,
            FileAccess.Read,
            FileShare.Read,
            81920,
            FileOptions.Asynchronous | FileOptions.SequentialScan);
        var digest = Convert.ToHexStringLower(await SHA256.HashDataAsync(stream, cancellationToken).ConfigureAwait(false));
        if (!digest.Equals(expectedSha256, StringComparison.Ordinal))
            throw new InvalidOperationException("The portable update SHA-256 changed before it could be applied.");
    }

    internal static void VerifyProductVersion(string path, string expectedVersion) =>
        VerifyProductVersion(path, expectedVersion, static executable => FileVersionInfo.GetVersionInfo(executable).ProductVersion);

    internal static void VerifyProductVersion(
        string path,
        string expectedVersion,
        Func<string, string?> productVersionReader)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(path);
        ArgumentNullException.ThrowIfNull(productVersionReader);
        var validatedExpectedVersion = ValidateExpectedVersion(expectedVersion);
        if (!string.Equals(productVersionReader(path), validatedExpectedVersion, StringComparison.Ordinal))
            throw new InvalidOperationException("The downloaded Rivune update ProductVersion does not match the release manifest.");
    }

    private static string ValidateExpectedVersion(string? version)
    {
        if (string.IsNullOrEmpty(version))
            throw new InvalidOperationException("The expected portable update version is invalid.");
        try
        {
            _ = AppUpdateChecker.CompareSemanticVersions(version, version);
            return version;
        }
        catch (InvalidOperationException exception)
        {
            throw new InvalidOperationException("The expected portable update version is invalid.", exception);
        }
    }

    private static void EnsureTargetDirectoryIsWritable(string targetPath)
    {
        var directory = Path.GetDirectoryName(targetPath);
        if (string.IsNullOrEmpty(directory))
            throw new InvalidOperationException("The Rivune executable directory is invalid.");
        var nonce = Guid.NewGuid().ToString("N");
        var probeTarget = Path.Combine(directory, $".rivune-write-test-{nonce}.tmp");
        var probeSource = Path.Combine(directory, $".rivune-replace-test-{nonce}.tmp");
        try
        {
            File.WriteAllBytes(probeTarget, [0]);
            File.WriteAllBytes(probeSource, [1]);
            File.Replace(probeSource, probeTarget, null, ignoreMetadataErrors: true);
        }
        catch (Exception exception) when (exception is UnauthorizedAccessException or IOException or NotSupportedException)
        {
            throw new InvalidOperationException(
                $"Rivune cannot safely replace files in this folder. Move {Path.GetFileName(targetPath)} to a writable local folder, such as Downloads, then try again.",
                exception);
        }
        finally
        {
            TryDeleteFile(probeSource);
            TryDeleteFile(probeTarget);
        }
    }

    private static string NormalizeAbsolutePath(string path, string errorMessage)
    {
        if (string.IsNullOrWhiteSpace(path) || !Path.IsPathFullyQualified(path))
            throw new InvalidOperationException(errorMessage);
        try
        {
            return Path.GetFullPath(path);
        }
        catch (Exception exception) when (exception is ArgumentException or NotSupportedException or PathTooLongException)
        {
            throw new InvalidOperationException(errorMessage, exception);
        }
    }

    private static bool IsPathWithinDirectory(string path, string directory)
    {
        var normalizedDirectory = Path.TrimEndingDirectorySeparator(Path.GetFullPath(directory)) + Path.DirectorySeparatorChar;
        return path.StartsWith(normalizedDirectory, StringComparison.OrdinalIgnoreCase);
    }

    private static bool PathsEqual(string left, string right) =>
        string.Equals(Path.TrimEndingDirectorySeparator(left), Path.TrimEndingDirectorySeparator(right), StringComparison.OrdinalIgnoreCase);

    private static bool HaveMatchingAllowedFileNames(string leftPath, string rightPath)
    {
        var leftName = Path.GetFileName(leftPath);
        var rightName = Path.GetFileName(rightPath);
        return IsAllowedExecutableFileName(leftName) && leftName.Equals(rightName, StringComparison.OrdinalIgnoreCase);
    }

    private static bool IsAllowedExecutableFileName(string fileName) =>
        fileName.Equals("Rivune.exe", StringComparison.OrdinalIgnoreCase) ||
        fileName.Equals("Rivune-x64.exe", StringComparison.OrdinalIgnoreCase) ||
        fileName.Equals("Rivune-arm64.exe", StringComparison.OrdinalIgnoreCase);

    private static void TryDeleteFile(string path)
    {
        try
        {
            if (File.Exists(path)) File.Delete(path);
        }
        catch
        {
            // Update cleanup must not hide a successful update or its actionable primary error.
        }
    }

    [GeneratedRegex("^[0-9a-f]{64}$", RegexOptions.CultureInvariant)]
    private static partial Regex Sha256Pattern();
}
