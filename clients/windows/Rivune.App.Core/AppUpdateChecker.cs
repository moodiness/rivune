using System.Buffers;
using System.Globalization;
using System.IO.Compression;
using System.Net;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using System.Text.Json;
using System.Text.RegularExpressions;

namespace Rivune.App;

internal sealed record WindowsUpdatePackage(
    Uri Uri,
    string Format,
    IReadOnlyList<string> Architectures,
    string MinimumOsVersion,
    string FileName,
    long Size,
    string Sha256,
    string ExecutableFileName,
    long ExecutableSize,
    string ExecutableSha256);

internal sealed record AppUpdateCheckResult(
    string CurrentVersion,
    string LatestVersion,
    DateTimeOffset PublishedAt,
    Uri ReleaseUri,
    WindowsUpdatePackage Package,
    bool IsUpdateAvailable);

internal static partial class AppUpdateChecker
{
    private const int MaximumManifestResponseBytes = 256 * 1024;
    private const int MaximumRedirects = 5;
    private const long MaximumPackageBytes = 2L * 1024 * 1024 * 1024 - 1;
    private static readonly TimeSpan AutomaticCheckInterval = TimeSpan.FromHours(24);
    private static readonly Uri ManifestEndpoint = new("https://github.com/moodiness/rivune/releases/latest/download/rivune-update.json");
    private static readonly HttpClient SharedPackageClient = CreatePackageClient();
    private static readonly HttpClient SharedClient = CreateClient();

    public static Task<AppUpdateCheckResult> CheckAsync(
        string currentVersion,
        CancellationToken cancellationToken = default) =>
        CheckAsync(SharedClient, currentVersion, RuntimeInformation.ProcessArchitecture, cancellationToken);

    public static Task DownloadPackageAsync(
        WindowsUpdatePackage package,
        Stream destination,
        CancellationToken cancellationToken = default) =>
        DownloadPackageAsync(SharedPackageClient, package, destination, cancellationToken);

    internal static async Task ExtractExecutableAsync(
        WindowsUpdatePackage package,
        string bundlePath,
        string destinationPath,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(package);
        ArgumentException.ThrowIfNullOrWhiteSpace(bundlePath);
        ArgumentException.ThrowIfNullOrWhiteSpace(destinationPath);
        if (File.Exists(destinationPath))
            throw new InvalidOperationException("The update extraction destination already exists.");

        await using var bundleStream = new FileStream(
            bundlePath,
            FileMode.Open,
            FileAccess.Read,
            FileShare.Read,
            81920,
            FileOptions.Asynchronous | FileOptions.SequentialScan);
        if (bundleStream.Length != package.Size)
            throw new InvalidOperationException("The downloaded Windows executable size changed before extraction.");
        var bundleDigest = Convert.ToHexStringLower(
            await SHA256.HashDataAsync(bundleStream, cancellationToken).ConfigureAwait(false));
        if (!bundleDigest.Equals(package.Sha256, StringComparison.Ordinal))
            throw new InvalidOperationException("The downloaded Windows executable SHA-256 changed before extraction.");
        bundleStream.Position = 0;

        using var archive = new ZipArchive(bundleStream, ZipArchiveMode.Read, leaveOpen: true);
        var expectedEntries = new HashSet<string>(["Rivune-x64.exe", "Rivune-arm64.exe", "Rivune-Uninstall.exe"], StringComparer.Ordinal);
        if (archive.Entries.Count != expectedEntries.Count)
            throw new InvalidOperationException("The downloaded Windows executable has an invalid embedded payload.");
        ZipArchiveEntry? selected = null;
        foreach (var entry in archive.Entries)
        {
            if (entry.FullName != entry.Name || !expectedEntries.Remove(entry.FullName) || entry.Length <= 0 || entry.Length > MaximumPackageBytes)
                throw new InvalidOperationException("The downloaded Windows executable has an invalid embedded payload.");
            if (entry.FullName.Equals(package.ExecutableFileName, StringComparison.Ordinal)) selected = entry;
        }
        if (expectedEntries.Count != 0 || selected is null || selected.Length != package.ExecutableSize)
            throw new InvalidOperationException("The downloaded Windows executable has no matching architecture payload.");

        try
        {
            await using var input = selected.Open();
            await using var output = new FileStream(
                destinationPath,
                FileMode.CreateNew,
                FileAccess.Write,
                FileShare.None,
                81920,
                FileOptions.Asynchronous | FileOptions.SequentialScan);
            using var hash = IncrementalHash.CreateHash(HashAlgorithmName.SHA256);
            var buffer = ArrayPool<byte>.Shared.Rent(81920);
            long total = 0;
            try
            {
                while (true)
                {
                    var read = await input.ReadAsync(buffer.AsMemory(0, buffer.Length), cancellationToken).ConfigureAwait(false);
                    if (read == 0) break;
                    if (total + read > package.ExecutableSize)
                        throw new InvalidOperationException("The embedded Windows executable is larger than declared.");
                    hash.AppendData(buffer, 0, read);
                    await output.WriteAsync(buffer.AsMemory(0, read), cancellationToken).ConfigureAwait(false);
                    total += read;
                }
                if (total != package.ExecutableSize ||
                    !Convert.ToHexStringLower(hash.GetHashAndReset()).Equals(package.ExecutableSha256, StringComparison.Ordinal))
                    throw new InvalidOperationException("The embedded Windows executable does not match its manifest size and SHA-256.");
                await output.FlushAsync(cancellationToken).ConfigureAwait(false);
            }
            finally
            {
                ArrayPool<byte>.Shared.Return(buffer);
            }
        }
        catch
        {
            try { File.Delete(destinationPath); } catch { }
            throw;
        }
    }

    internal static bool AutomaticCheckIsDue(DateTimeOffset? lastSuccessfulCheck, DateTimeOffset now) =>
        lastSuccessfulCheck is null || lastSuccessfulCheck > now || now - lastSuccessfulCheck >= AutomaticCheckInterval;

    internal static async Task DownloadPackageAsync(
        HttpClient client,
        WindowsUpdatePackage package,
        Stream destination,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(client);
        ArgumentNullException.ThrowIfNull(package);
        ArgumentNullException.ThrowIfNull(destination);
        if (!destination.CanWrite || !destination.CanSeek)
            throw new ArgumentException("The update package destination must be writable and seekable.", nameof(destination));
        destination.SetLength(0);
        destination.Position = 0;

        using var deadline = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        deadline.CancelAfter(TimeSpan.FromMinutes(10));
        try
        {
            await DownloadPackageCoreAsync(client, package, destination, deadline.Token).ConfigureAwait(false);
        }
        catch
        {
            destination.SetLength(0);
            destination.Position = 0;
            throw;
        }
    }

    internal static Task<AppUpdateCheckResult> CheckAsync(
        HttpClient client,
        string currentVersion,
        CancellationToken cancellationToken = default) =>
        CheckAsync(client, currentVersion, RuntimeInformation.ProcessArchitecture, cancellationToken);

    internal static async Task<AppUpdateCheckResult> CheckAsync(
        HttpClient client,
        string currentVersion,
        Architecture processArchitecture,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(client);
        var (architecture, executableFileName) = processArchitecture switch
        {
            Architecture.X64 => ("x64", "Rivune-x64.exe"),
            Architecture.Arm64 => ("arm64", "Rivune-arm64.exe"),
            _ => throw new InvalidOperationException(
                $"Rivune updates do not support the current process architecture ({processArchitecture})."),
        };
        var current = ParseSemanticVersion(currentVersion, "The installed Rivune version is invalid.");
        var payload = await FetchManifestAsync(client, cancellationToken).ConfigureAwait(false);

        JsonDocument document;
        try
        {
            document = JsonDocument.Parse(payload, new JsonDocumentOptions
            {
                AllowTrailingCommas = false,
                CommentHandling = JsonCommentHandling.Disallow,
                MaxDepth = 16,
            });
        }
        catch (JsonException exception)
        {
            throw new InvalidOperationException("The Rivune update manifest is not valid JSON.", exception);
        }

        using (document)
        {
            var root = document.RootElement;
            RejectDuplicateProperties(root, "manifest");
            RequireProperties(root, "manifest",
                "schemaVersion", "channel", "version", "tagName", "publishedAt", "releaseUrl", "packages");
            if (root.GetProperty("schemaVersion").ValueKind != JsonValueKind.Number ||
                root.GetProperty("schemaVersion").GetRawText() != "3")
                throw new InvalidOperationException("The Rivune update manifest schema is not supported.");

            var version = RequiredString(root, "version", "manifest");
            var latest = ParseSemanticVersion(version, "The Rivune update manifest version is invalid.");
            var channel = RequiredString(root, "channel", "manifest");
            var expectedChannel = latest.Prerelease.Length == 0 ? "stable" : "prerelease";
            if (!channel.Equals(expectedChannel, StringComparison.Ordinal))
                throw new InvalidOperationException("The Rivune update manifest channel does not match its version.");

            var tagName = RequiredString(root, "tagName", "manifest");
            if (!tagName.Equals($"v{version}", StringComparison.Ordinal))
                throw new InvalidOperationException("The Rivune update manifest tag does not match its version.");

            var publishedAtText = RequiredString(root, "publishedAt", "manifest");
            if (!Rfc3339Pattern().IsMatch(publishedAtText) ||
                !DateTimeOffset.TryParse(publishedAtText, CultureInfo.InvariantCulture, DateTimeStyles.None, out var publishedAt))
            {
                throw new InvalidOperationException("The Rivune update manifest publication timestamp is invalid.");
            }

            var expectedReleaseUrl = $"https://github.com/moodiness/rivune/releases/tag/{tagName}";
            var releaseUri = RequireExactUri(
                RequiredString(root, "releaseUrl", "manifest"),
                expectedReleaseUrl,
                "The Rivune update manifest release URL is not trusted.");

            var packages = root.GetProperty("packages");
            RequireProperties(packages, "manifest packages", "android", "windows");
            if (packages.GetProperty("android").ValueKind != JsonValueKind.Object)
                throw new InvalidOperationException("The Rivune update manifest Android package is invalid.");
            var package = ParseWindowsPackage(
                packages.GetProperty("windows"),
                tagName,
                architecture,
                executableFileName);

            return new AppUpdateCheckResult(
                currentVersion,
                version,
                publishedAt,
                releaseUri,
                package,
                Compare(current, latest) < 0);
        }
    }

    internal static int CompareSemanticVersions(string left, string right) =>
        Compare(
            ParseSemanticVersion(left, "The left semantic version is invalid."),
            ParseSemanticVersion(right, "The right semantic version is invalid."));

    private static WindowsUpdatePackage ParseWindowsPackage(
        JsonElement package,
        string tagName,
        string expectedArchitecture,
        string expectedExecutableFileName)
    {
        RequireProperties(package, "Windows package",
            "format", "architectures", "minimumOsVersion", "fileName", "url", "size", "sha256", "executables");
        RequireExactString(package, "format", "exe", "The Windows update package format is invalid.");
        RequireExactString(package, "minimumOsVersion", "10.0.19041.0", "The Windows update minimum OS version is invalid.");

        var architectures = package.GetProperty("architectures");
        if (architectures.ValueKind != JsonValueKind.Array ||
            architectures.GetArrayLength() != 2 ||
            architectures[0].ValueKind != JsonValueKind.String || architectures[0].GetString() != "arm64" ||
            architectures[1].ValueKind != JsonValueKind.String || architectures[1].GetString() != "x64")
        {
            throw new InvalidOperationException("The Windows update package architectures are invalid.");
        }

        const string packageFileName = "Rivune-Windows.exe";
        if (!RequiredString(package, "fileName", "Windows package").Equals(packageFileName, StringComparison.Ordinal))
            throw new InvalidOperationException("The Windows update package file name is invalid.");

        var expectedPackageUrl = $"https://github.com/moodiness/rivune/releases/download/{tagName}/{packageFileName}";
        var packageUri = RequireExactUri(
            RequiredString(package, "url", "Windows package"),
            expectedPackageUrl,
            "The Windows update package URL is not trusted.");
        var size = RequiredPositiveSize(package, "size", "Windows package");
        var sha256 = RequiredSha256(package, "sha256", "Windows package");

        var executables = package.GetProperty("executables");
        RequireProperties(executables, "Windows executables", "x64", "arm64");
        var executable = executables.GetProperty(expectedArchitecture);
        RequireProperties(executable, $"Windows {expectedArchitecture} executable", "fileName", "size", "sha256");
        if (!RequiredString(executable, "fileName", "Windows executable").Equals(expectedExecutableFileName, StringComparison.Ordinal))
            throw new InvalidOperationException("The Windows update executable file name is invalid.");
        var executableSize = RequiredPositiveSize(executable, "size", "Windows executable");
        var executableSha256 = RequiredSha256(executable, "sha256", "Windows executable");

        return new WindowsUpdatePackage(
            packageUri,
            "exe",
            ["arm64", "x64"],
            "10.0.19041.0",
            packageFileName,
            size,
            sha256,
            expectedExecutableFileName,
            executableSize,
            executableSha256);
    }

    private static long RequiredPositiveSize(JsonElement value, string name, string context)
    {
        var element = value.GetProperty(name);
        if (element.ValueKind != JsonValueKind.Number ||
            !PositiveIntegerPattern().IsMatch(element.GetRawText()) ||
            !element.TryGetInt64(out var size) ||
            size is <= 0 or > MaximumPackageBytes)
        {
            throw new InvalidOperationException($"The Rivune update {context} {name} is invalid.");
        }
        return size;
    }

    private static string RequiredSha256(JsonElement value, string name, string context)
    {
        var digest = RequiredString(value, name, context);
        if (!Sha256Pattern().IsMatch(digest))
            throw new InvalidOperationException($"The Rivune update {context} {name} is invalid.");
        return digest;
    }


    private static HttpClient CreateClient()
    {
        var handler = new SocketsHttpHandler
        {
            AllowAutoRedirect = false,
            AutomaticDecompression = DecompressionMethods.All,
            ConnectTimeout = TimeSpan.FromSeconds(10),
        };
        return new HttpClient(handler) { Timeout = TimeSpan.FromSeconds(20) };
    }

    private static HttpClient CreatePackageClient()
    {
        var handler = new SocketsHttpHandler
        {
            AllowAutoRedirect = false,
            AutomaticDecompression = DecompressionMethods.None,
            ConnectTimeout = TimeSpan.FromSeconds(10),
        };
        return new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan };
    }

    private static async Task<byte[]> FetchManifestAsync(HttpClient client, CancellationToken cancellationToken)
    {
        var uri = ManifestEndpoint;
        for (var redirectCount = 0; redirectCount <= MaximumRedirects; redirectCount++)
        {
            using var request = new HttpRequestMessage(HttpMethod.Get, uri);
            request.Headers.Accept.ParseAdd("application/json");
            request.Headers.UserAgent.ParseAdd("Rivune-Windows-Update/2.0");
            using var response = await client.SendAsync(
                request,
                HttpCompletionOption.ResponseHeadersRead,
                cancellationToken).ConfigureAwait(false);

            if (response.RequestMessage?.RequestUri is { } finalUri && finalUri != uri && !IsExpectedRedirectUri(finalUri))
                throw new InvalidOperationException("The Rivune update manifest was redirected to an untrusted host.");

            if (IsRedirect(response.StatusCode))
            {
                if (redirectCount == MaximumRedirects)
                    throw new InvalidOperationException("The Rivune update manifest was redirected too many times.");
                if (response.Headers.Location is not { } location ||
                    !Uri.TryCreate(uri, location, out var redirectUri) ||
                    !IsExpectedRedirectUri(redirectUri))
                {
                    throw new InvalidOperationException("The Rivune update manifest was redirected to an untrusted host.");
                }
                uri = redirectUri;
                continue;
            }

            if (response.StatusCode == HttpStatusCode.NotFound)
                throw new InvalidOperationException("No published Rivune update manifest was found.");
            if (!response.IsSuccessStatusCode)
                throw new HttpRequestException(
                    $"GitHub returned HTTP {(int)response.StatusCode} while checking for updates.",
                    null,
                    response.StatusCode);
            if (response.Content.Headers.ContentLength is > MaximumManifestResponseBytes)
                throw new InvalidOperationException("The Rivune update manifest is too large.");
            return await ReadBoundedAsync(response.Content, cancellationToken).ConfigureAwait(false);
        }
        throw new InvalidOperationException("The Rivune update manifest was redirected too many times.");
    }

    private static async Task DownloadPackageCoreAsync(
        HttpClient client,
        WindowsUpdatePackage package,
        Stream destination,
        CancellationToken cancellationToken)
    {
        var uri = package.Uri;
        for (var redirectCount = 0; redirectCount <= MaximumRedirects; redirectCount++)
        {
            using var request = new HttpRequestMessage(HttpMethod.Get, uri);
            request.Headers.Accept.ParseAdd("application/octet-stream");
            request.Headers.UserAgent.ParseAdd("Rivune-Windows-Update/2.0");
            using var response = await client.SendAsync(
                request,
                HttpCompletionOption.ResponseHeadersRead,
                cancellationToken).ConfigureAwait(false);

            if (response.RequestMessage?.RequestUri is { } finalUri &&
                finalUri != uri &&
                !IsExpectedPackageRedirectUri(finalUri, package.Uri))
            {
                throw new InvalidOperationException("The Rivune update package was redirected to an untrusted host.");
            }

            if (IsRedirect(response.StatusCode))
            {
                if (redirectCount == MaximumRedirects)
                    throw new InvalidOperationException("The Rivune update package was redirected too many times.");
                if (response.Headers.Location is not { } location ||
                    !Uri.TryCreate(uri, location, out var redirectUri) ||
                    !IsExpectedPackageRedirectUri(redirectUri, package.Uri))
                {
                    throw new InvalidOperationException("The Rivune update package was redirected to an untrusted host.");
                }
                uri = redirectUri;
                continue;
            }

            if (!response.IsSuccessStatusCode)
                throw new HttpRequestException(
                    $"GitHub returned HTTP {(int)response.StatusCode} while downloading the Rivune update package.",
                    null,
                    response.StatusCode);
            if (response.Content.Headers.ContentLength is { } contentLength && contentLength != package.Size)
                throw new InvalidOperationException("The downloaded Rivune update package size does not match the manifest.");

            await using var input = await response.Content.ReadAsStreamAsync(cancellationToken).ConfigureAwait(false);
            using var hash = IncrementalHash.CreateHash(HashAlgorithmName.SHA256);
            var buffer = ArrayPool<byte>.Shared.Rent(81920);
            long total = 0;
            try
            {
                while (true)
                {
                    var read = await input.ReadAsync(buffer.AsMemory(0, buffer.Length), cancellationToken).ConfigureAwait(false);
                    if (read == 0) break;
                    if (total + read > package.Size)
                        throw new InvalidOperationException("The downloaded Rivune update package is larger than the manifest size.");
                    hash.AppendData(buffer, 0, read);
                    await destination.WriteAsync(buffer.AsMemory(0, read), cancellationToken).ConfigureAwait(false);
                    total += read;
                }
                if (total != package.Size)
                    throw new InvalidOperationException("The downloaded Rivune update package size does not match the manifest.");
                var digest = Convert.ToHexStringLower(hash.GetHashAndReset());
                if (!digest.Equals(package.Sha256, StringComparison.Ordinal))
                    throw new InvalidOperationException("The downloaded Rivune update package SHA-256 does not match the manifest.");
                await destination.FlushAsync(cancellationToken).ConfigureAwait(false);
                return;
            }
            finally
            {
                ArrayPool<byte>.Shared.Return(buffer);
            }
        }
        throw new InvalidOperationException("The Rivune update package was redirected too many times.");
    }

    private static bool IsRedirect(HttpStatusCode statusCode) =>
        statusCode is HttpStatusCode.MovedPermanently or HttpStatusCode.Redirect or HttpStatusCode.SeeOther or
            HttpStatusCode.TemporaryRedirect or HttpStatusCode.PermanentRedirect;

    private static bool IsExpectedRedirectUri(Uri uri)
    {
        if (uri.Scheme != Uri.UriSchemeHttps || !uri.IsDefaultPort || !string.IsNullOrEmpty(uri.UserInfo) ||
            !string.IsNullOrEmpty(uri.Fragment))
        {
            return false;
        }
        if (IsExpectedAssetHostUri(uri))
            return true;
        return uri.Host.Equals("github.com", StringComparison.OrdinalIgnoreCase) &&
               string.IsNullOrEmpty(uri.Query) &&
               GitHubManifestRedirectPathPattern().IsMatch(uri.AbsolutePath);
    }

    private static bool IsExpectedPackageRedirectUri(Uri uri, Uri packageUri) =>
        IsExpectedAssetHostUri(uri) || uri == packageUri;

    private static bool IsExpectedAssetHostUri(Uri uri) =>
        uri.Scheme == Uri.UriSchemeHttps &&
        uri.IsDefaultPort &&
        string.IsNullOrEmpty(uri.UserInfo) &&
        string.IsNullOrEmpty(uri.Fragment) &&
        !string.IsNullOrEmpty(uri.AbsolutePath) &&
        (uri.Host.Equals("release-assets.githubusercontent.com", StringComparison.OrdinalIgnoreCase) ||
         uri.Host.Equals("objects.githubusercontent.com", StringComparison.OrdinalIgnoreCase));

    private static async Task<byte[]> ReadBoundedAsync(HttpContent content, CancellationToken cancellationToken)
    {
        await using var input = await content.ReadAsStreamAsync(cancellationToken).ConfigureAwait(false);
        using var output = new MemoryStream();
        var buffer = ArrayPool<byte>.Shared.Rent(8192);
        try
        {
            while (true)
            {
                var read = await input.ReadAsync(buffer.AsMemory(0, buffer.Length), cancellationToken).ConfigureAwait(false);
                if (read == 0) break;
                if (output.Length + read > MaximumManifestResponseBytes)
                    throw new InvalidOperationException("The Rivune update manifest is too large.");
                output.Write(buffer, 0, read);
            }
            return output.ToArray();
        }
        finally
        {
            ArrayPool<byte>.Shared.Return(buffer);
        }
    }

    private static void RejectDuplicateProperties(JsonElement element, string context)
    {
        if (element.ValueKind == JsonValueKind.Object)
        {
            var found = new HashSet<string>(StringComparer.Ordinal);
            foreach (var property in element.EnumerateObject())
            {
                if (!found.Add(property.Name))
                    throw new InvalidOperationException($"The Rivune update {context} contains duplicate fields.");
                RejectDuplicateProperties(property.Value, $"{context}.{property.Name}");
            }
        }
        else if (element.ValueKind == JsonValueKind.Array)
        {
            foreach (var item in element.EnumerateArray())
                RejectDuplicateProperties(item, context);
        }
    }

    private static void RequireProperties(JsonElement element, string context, params string[] expected)
    {
        if (element.ValueKind != JsonValueKind.Object)
            throw new InvalidOperationException($"The Rivune update {context} is not an object.");
        var found = new HashSet<string>(StringComparer.Ordinal);
        foreach (var property in element.EnumerateObject())
        {
            if (!found.Add(property.Name))
                throw new InvalidOperationException($"The Rivune update {context} contains duplicate fields.");
        }
        if (expected.Any(name => !found.Contains(name)))
            throw new InvalidOperationException($"The Rivune update {context} is missing required fields.");
    }

    private static string RequiredString(JsonElement element, string name, string context)
    {
        var property = element.GetProperty(name);
        if (property.ValueKind != JsonValueKind.String || string.IsNullOrEmpty(property.GetString()))
            throw new InvalidOperationException($"The Rivune update {context} {name} is invalid.");
        return property.GetString()!;
    }

    private static void RequireExactString(JsonElement element, string name, string expected, string errorMessage)
    {
        if (!RequiredString(element, name, "Windows package").Equals(expected, StringComparison.Ordinal))
            throw new InvalidOperationException(errorMessage);
    }

    private static Uri RequireExactUri(string value, string expected, string errorMessage)
    {
        if (!value.Equals(expected, StringComparison.Ordinal) ||
            !Uri.TryCreate(value, UriKind.Absolute, out var uri) ||
            uri.Scheme != Uri.UriSchemeHttps || !uri.IsDefaultPort || !string.IsNullOrEmpty(uri.UserInfo) ||
            !string.IsNullOrEmpty(uri.Query) || !string.IsNullOrEmpty(uri.Fragment))
        {
            throw new InvalidOperationException(errorMessage);
        }
        return uri;
    }

    private static ParsedSemanticVersion ParseSemanticVersion(string value, string errorMessage)
    {
        var match = SemanticVersionPattern().Match(value);
        if (!match.Success) throw new InvalidOperationException(errorMessage);
        var identifiers = match.Groups[4].Success ? match.Groups[4].Value.Split('.') : [];
        if (identifiers.Any(identifier =>
                identifier.All(char.IsAsciiDigit) && identifier.Length > 1 && identifier[0] == '0'))
        {
            throw new InvalidOperationException(errorMessage);
        }
        return new ParsedSemanticVersion(
            match.Groups[1].Value,
            match.Groups[2].Value,
            match.Groups[3].Value,
            identifiers);
    }

    private static int Compare(ParsedSemanticVersion left, ParsedSemanticVersion right)
    {
        foreach (var (leftPart, rightPart) in new[]
                 {
                     (left.Major, right.Major),
                     (left.Minor, right.Minor),
                     (left.Patch, right.Patch),
                 })
        {
            var result = CompareNumericIdentifiers(leftPart, rightPart);
            if (result != 0) return result;
        }

        if (left.Prerelease.Length == 0) return right.Prerelease.Length == 0 ? 0 : 1;
        if (right.Prerelease.Length == 0) return -1;
        for (var index = 0; index < Math.Max(left.Prerelease.Length, right.Prerelease.Length); index++)
        {
            if (index >= left.Prerelease.Length) return -1;
            if (index >= right.Prerelease.Length) return 1;
            var leftPart = left.Prerelease[index];
            var rightPart = right.Prerelease[index];
            var leftNumeric = leftPart.All(char.IsAsciiDigit);
            var rightNumeric = rightPart.All(char.IsAsciiDigit);
            var result = leftNumeric && rightNumeric
                ? CompareNumericIdentifiers(leftPart, rightPart)
                : leftNumeric
                    ? -1
                    : rightNumeric
                        ? 1
                        : string.Compare(leftPart, rightPart, StringComparison.Ordinal);
            if (result != 0) return result;
        }
        return 0;
    }

    private static int CompareNumericIdentifiers(string left, string right)
    {
        var length = left.Length.CompareTo(right.Length);
        return length != 0 ? length : string.Compare(left, right, StringComparison.Ordinal);
    }

    private sealed record ParsedSemanticVersion(
        string Major,
        string Minor,
        string Patch,
        string[] Prerelease);

    [GeneratedRegex("^(0|[1-9]\\d*)\\.(0|[1-9]\\d*)\\.(0|[1-9]\\d*)(?:-([0-9A-Za-z-]+(?:\\.[0-9A-Za-z-]+)*))?(?:\\+([0-9A-Za-z-]+(?:\\.[0-9A-Za-z-]+)*))?$", RegexOptions.CultureInvariant)]
    private static partial Regex SemanticVersionPattern();


    [GeneratedRegex("^[0-9a-f]{64}$", RegexOptions.CultureInvariant)]
    private static partial Regex Sha256Pattern();

    [GeneratedRegex("^[1-9][0-9]*$", RegexOptions.CultureInvariant)]
    private static partial Regex PositiveIntegerPattern();

    [GeneratedRegex("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\\.[0-9]+)?(?:Z|[+-][0-9]{2}:[0-9]{2})$", RegexOptions.CultureInvariant)]
    private static partial Regex Rfc3339Pattern();

    [GeneratedRegex("^/moodiness/rivune/releases/download/v[0-9A-Za-z.+-]+/rivune-update\\.json$", RegexOptions.CultureInvariant)]
    private static partial Regex GitHubManifestRedirectPathPattern();
}
