using System.Net;
using System.IO.Compression;
using System.Text;
using System.Security.Cryptography;
using System.Runtime.InteropServices;
using Rivune.App;
using Xunit;

namespace Rivune.Windows.Tests;

public sealed class AppUpdateCheckerTests
{
    private const string PackageSha256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
    private const string X64ExecutableSha256 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb";
    private const string Arm64ExecutableSha256 = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc";
    private static readonly Uri ManifestUri = new("https://github.com/moodiness/rivune/releases/latest/download/rivune-update.json");

    [Theory]
    [InlineData(null, true)]
    [InlineData("2026-08-18T12:00:00Z", true)]
    [InlineData("2026-08-18T12:00:01Z", false)]
    [InlineData("2026-08-19T11:59:59Z", false)]
    [InlineData("2026-08-19T12:00:01Z", true)]
    public void AutomaticCheckRunsAtMostOncePerDay(string? lastCheck, bool expected)
    {
        var now = DateTimeOffset.Parse("2026-08-19T12:00:00Z");
        var previous = lastCheck is null ? (DateTimeOffset?)null : DateTimeOffset.Parse(lastCheck);

        Assert.Equal(expected, AppUpdateChecker.AutomaticCheckIsDue(previous, now));
    }

    [Theory]
    [InlineData("1.7.2", "1.7.2", 0)]
    [InlineData("1.7.1", "1.7.2", -1)]
    [InlineData("2.0.0", "1.99.99", 1)]
    [InlineData("1.7.2-beta.2", "1.7.2-beta.10", -1)]
    [InlineData("1.7.2-beta.10", "1.7.2", -1)]
    [InlineData("1.7.2+local", "1.7.2+release", 0)]
    public void ComparesSemanticVersions(string left, string right, int expectedSign)
    {
        Assert.Equal(expectedSign, Math.Sign(AppUpdateChecker.CompareSemanticVersions(left, right)));
    }

    [Fact]
    public void UpdateNoticeOnlyPresentsStrictlyNewerVersions()
    {
        Assert.True(AppUpdateNotificationPolicy.ShouldPresent(null, "1.7.2"));
        Assert.False(AppUpdateNotificationPolicy.ShouldPresent("1.7.2", "1.7.2"));
        Assert.True(AppUpdateNotificationPolicy.ShouldPresent("1.7.2", "1.8.0"));
        Assert.False(AppUpdateNotificationPolicy.ShouldPresent("1.8.0", "1.7.2"));
        Assert.False(AppUpdateNotificationPolicy.ShouldPresent("invalid", "1.8.0"));
    }

    [Theory]
    [InlineData("1.7.1", true)]
    [InlineData("1.7.2", false)]
    [InlineData("1.8.0", false)]
    public async Task ResolvesStableGlobalManifest(string currentVersion, bool available)
    {
        var handler = new SequenceHandler(Response(HttpStatusCode.OK, Manifest()));
        using var client = new HttpClient(handler);

        var result = await AppUpdateChecker.CheckUnsignedForTestingAsync(client,
        currentVersion,
        Architecture.X64,
        TestContext.Current.CancellationToken);

        Assert.Equal(currentVersion, result.CurrentVersion);
        Assert.Equal("1.7.2", result.LatestVersion);
        Assert.Equal(DateTimeOffset.Parse("2026-08-14T12:34:56Z"), result.PublishedAt);
        Assert.Equal(new Uri("https://github.com/moodiness/rivune/releases/tag/v1.7.2"), result.ReleaseUri);
        Assert.Equal(new Uri("https://github.com/moodiness/rivune/releases/download/v1.7.2/Rivune-Windows.exe"), result.Package.Uri);
        Assert.Equal("exe", result.Package.Format);
        Assert.Equal(new[] { "arm64", "x64" }, result.Package.Architectures);
        Assert.Equal("10.0.19041.0", result.Package.MinimumOsVersion);
        Assert.Equal("Rivune-Windows.exe", result.Package.FileName);
        Assert.Equal(123456L, result.Package.Size);
        Assert.Equal(PackageSha256, result.Package.Sha256);
        Assert.Equal("Rivune-x64.exe", result.Package.ExecutableFileName);
        Assert.Equal(345678L, result.Package.ExecutableSize);
        Assert.Equal(X64ExecutableSha256, result.Package.ExecutableSha256);
        Assert.Equal(available, result.IsUpdateAvailable);
        Assert.Equal(new[] { ManifestUri }, handler.RequestUris);
        Assert.Equal("application/json", handler.Accepts.Single());
    }

    [Fact]
    public async Task Arm64ProcessSelectsArm64Package()
    {
        var handler = new SequenceHandler(Response(HttpStatusCode.OK, Manifest()));
        using var client = new HttpClient(handler);

        var result = await AppUpdateChecker.CheckUnsignedForTestingAsync(client,
        "1.7.1",
        Architecture.Arm64,
        TestContext.Current.CancellationToken);

        Assert.Equal(new Uri("https://github.com/moodiness/rivune/releases/download/v1.7.2/Rivune-Windows.exe"), result.Package.Uri);
        Assert.Equal(new[] { "arm64", "x64" }, result.Package.Architectures);
        Assert.Equal("Rivune-arm64.exe", result.Package.ExecutableFileName);
        Assert.Equal(456789L, result.Package.ExecutableSize);
        Assert.Equal(Arm64ExecutableSha256, result.Package.ExecutableSha256);
    }

    [Theory]
    [InlineData(Architecture.X86)]
    [InlineData(Architecture.Arm)]
    [InlineData(Architecture.Wasm)]
    public async Task RejectsUnsupportedProcessArchitectureBeforeNetworkRequest(Architecture architecture)
    {
        var handler = new SequenceHandler();
        using var client = new HttpClient(handler);

        var exception = await Assert.ThrowsAsync<InvalidOperationException>(() => AppUpdateChecker.CheckUnsignedForTestingAsync(client,
        "1.7.1",
        architecture,
        TestContext.Current.CancellationToken));

        Assert.Contains("architecture", exception.Message, StringComparison.OrdinalIgnoreCase);
        Assert.Empty(handler.RequestUris);
    }

    [Theory]
    [InlineData("\"fileName\":\"Rivune-arm64.exe\"", "\"fileName\":\"Rivune-x64.exe\"")]
    [InlineData("\"size\":456789", "\"size\":0")]
    [InlineData(Arm64ExecutableSha256, "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC")]
    public async Task RejectsWrongArm64ExecutableMetadata(string expected, string replacement)
    {
        var armPackageStart = Manifest().IndexOf("\"arm64\":{", StringComparison.Ordinal);
        var invalid = Manifest();
        var metadataIndex = invalid.IndexOf(expected, armPackageStart, StringComparison.Ordinal);
        Assert.True(metadataIndex >= armPackageStart);
        invalid = invalid.Remove(metadataIndex, expected.Length).Insert(metadataIndex, replacement);
        using var client = new HttpClient(new SequenceHandler(Response(HttpStatusCode.OK, invalid)));

        await Assert.ThrowsAsync<InvalidOperationException>(() => AppUpdateChecker.CheckUnsignedForTestingAsync(client,
        "1.7.1",
        Architecture.Arm64,
        TestContext.Current.CancellationToken));
    }

    [Fact]
    public async Task FollowsOnlyExpectedGitHubManifestRedirects()
    {
        var releaseUri = new Uri("https://github.com/moodiness/rivune/releases/download/v1.7.2/rivune-update.json");
        var assetUri = new Uri("https://release-assets.githubusercontent.com/github-production-release-asset/123/manifest?token=signed");
        var handler = new SequenceHandler(
            Response(HttpStatusCode.Redirect, location: releaseUri),
            Response(HttpStatusCode.Redirect, location: assetUri),
            Response(HttpStatusCode.OK, Manifest()));
        using var client = new HttpClient(handler);

        var result = await AppUpdateChecker.CheckUnsignedForTestingAsync(client, "1.7.1", Architecture.X64, TestContext.Current.CancellationToken);

        Assert.True(result.IsUpdateAvailable);
        Assert.Equal(new[] { ManifestUri, releaseUri, assetUri }, handler.RequestUris);
    }

    [Theory]
    [InlineData("http://github.com/moodiness/rivune/releases/download/v1.7.2/rivune-update.json")]
    [InlineData("https://evil.example/rivune-update.json")]
    [InlineData("https://github.com/other/repository/releases/download/v1.7.2/rivune-update.json")]
    [InlineData("https://release-assets.githubusercontent.com.evil.example/manifest")]
    public async Task RejectsUnsafeManifestRedirect(string location)
    {
        var handler = new SequenceHandler(Response(HttpStatusCode.Redirect, location: new Uri(location)));
        using var client = new HttpClient(handler);

        var exception = await Assert.ThrowsAsync<InvalidOperationException>(() =>
            AppUpdateChecker.CheckUnsignedForTestingAsync(client, "1.7.1", Architecture.X64, TestContext.Current.CancellationToken));

        Assert.Contains("untrusted host", exception.Message);
        Assert.Single(handler.RequestUris);
    }

    [Fact]
    public async Task RejectsTooManyRedirects()
    {
        var location = new Uri("https://github.com/moodiness/rivune/releases/download/v1.7.2/rivune-update.json");
        var handler = new SequenceHandler(Enumerable.Range(0, 6)
            .Select(_ => Response(HttpStatusCode.Redirect, location: location))
            .ToArray());
        using var client = new HttpClient(handler);

        await Assert.ThrowsAsync<InvalidOperationException>(() =>
            AppUpdateChecker.CheckUnsignedForTestingAsync(client, "1.7.1", Architecture.X64, TestContext.Current.CancellationToken));
    }

    [Theory]
    [InlineData("\"schemaVersion\":3", "\"schemaVersion\":1")]
    [InlineData("\"schemaVersion\":3", "\"schemaVersion\":3.0")]
    [InlineData("\"format\":\"exe\"", "\"format\":\"zip\"")]
    [InlineData("\"minimumOsVersion\":\"10.0.19041.0\"", "\"minimumOsVersion\":\"10.0.0.0\"")]
    [InlineData("\"architectures\":[\"arm64\",\"x64\"]", "\"architectures\":[\"x64\",\"arm64\"]")]
    [InlineData("\"size\":123456", "\"size\":0")]
    [InlineData("\"size\":123456", "\"size\":2147483648")]
    [InlineData("\"size\":123456", "\"size\":123.0")]
    [InlineData("\"channel\":\"stable\"", "\"channel\":\"prerelease\"")]
    [InlineData("\"publishedAt\":\"2026-08-14T12:34:56Z\"", "\"publishedAt\":\"not-a-timestamp\"")]
    [InlineData("\"tagName\":\"v1.7.2\"", "\"tagName\":\"v1.7.3\"")]
    [InlineData("\"fileName\":\"Rivune-Windows.exe\"", "\"fileName\":\"other.exe\"")]
    public async Task RejectsInvalidGlobalOrWindowsContract(string expected, string replacement)
    {
        await AssertInvalidManifestAsync(Manifest().Replace(expected, replacement, StringComparison.Ordinal));
    }

    [Fact]
    public async Task RejectsManifestWithoutWindowsPackage()
    {
        var invalid = Manifest().Replace("\"windows\":{", "\"other\":{", StringComparison.Ordinal);

        await AssertInvalidManifestAsync(invalid);
    }

    [Fact]
    public async Task RejectsUntrustedReleaseUrl()
    {
        var invalid = Manifest().Replace(
            "https://github.com/moodiness/rivune/releases/tag/v1.7.2",
            "https://evil.example/moodiness/rivune/releases/tag/v1.7.2",
            StringComparison.Ordinal);

        await AssertInvalidManifestAsync(invalid);
    }

    [Theory]
    [InlineData("https://evil.example/Rivune-Windows.exe")]
    [InlineData("http://github.com/moodiness/rivune/releases/download/v1.7.2/Rivune-Windows.exe")]
    [InlineData("https://github.com/moodiness/rivune/releases/download/v1.7.3/Rivune-Windows.exe")]
    [InlineData("https://github.com/moodiness/rivune/releases/download/v1.7.2/Rivune-Windows.exe?download=1")]
    public async Task RejectsUntrustedPackageUrl(string packageUrl)
    {
        var invalid = Manifest().Replace(
            "https://github.com/moodiness/rivune/releases/download/v1.7.2/Rivune-Windows.exe",
            packageUrl,
            StringComparison.Ordinal);

        await AssertInvalidManifestAsync(invalid);
    }

    [Theory]
    [InlineData(PackageSha256, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")]
    [InlineData(PackageSha256, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")]
    [InlineData(PackageSha256, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")]
    public async Task RejectsInvalidHashes(string expected, string replacement)
    {
        await AssertInvalidManifestAsync(Manifest().Replace(expected, replacement, StringComparison.Ordinal));
    }

    [Fact]
    public async Task RejectsDuplicateManifestFields()
    {
        await AssertInvalidManifestAsync(Manifest().Replace(
            "\"schemaVersion\":3",
            "\"schemaVersion\":3,\"schemaVersion\":3",
            StringComparison.Ordinal));
    }

    [Fact]
    public async Task AcceptsFutureFieldsAndPackages()
    {
        var manifest = Manifest()
            .Replace("\"schemaVersion\":3", "\"schemaVersion\":3,\"futureRoot\":true", StringComparison.Ordinal)
            .Replace("\"android\":{", "\"futurePlatform\":{},\"android\":{", StringComparison.Ordinal)
            .Replace("\"format\":\"exe\"", "\"format\":\"exe\",\"futureWindowsField\":true", StringComparison.Ordinal);
        using var client = new HttpClient(new SequenceHandler(Response(HttpStatusCode.OK, manifest)));

        var result = await AppUpdateChecker.CheckUnsignedForTestingAsync(client, "1.7.1", Architecture.X64, TestContext.Current.CancellationToken);

        Assert.True(result.IsUpdateAvailable);
    }

    [Fact]
    public async Task RejectsManifestLargerThanBoundWithoutContentLength()
    {
        var oversized = Encoding.UTF8.GetBytes(new string(' ', 256 * 1024 + 1));
        var handler = new SequenceHandler(new ResponseSpec(HttpStatusCode.OK, oversized, null, true));
        using var client = new HttpClient(handler);

        var exception = await Assert.ThrowsAsync<InvalidOperationException>(() =>
            AppUpdateChecker.CheckUnsignedForTestingAsync(client, "1.7.1", Architecture.X64, TestContext.Current.CancellationToken));

        Assert.Equal("The Rivune update manifest is too large.", exception.Message);
    }

    [Fact]
    public void VerifiesPinnedManifestSignatureAndRejectsTampering()
    {
        var manifest = Encoding.UTF8.GetBytes("fixture");
        var valid = Encoding.UTF8.GetBytes("{\"schemaVersion\":1,\"algorithm\":\"ecdsa-p256-sha256\",\"keyId\":\"4e9b15a0b6aed77908f3686fbf05a0a9c322ad846662eb758f56d4e65c22796f\",\"manifestSha256\":\"f16d05ec6b29248d2c61adb1e9263f78e4f7bace1b955014a2d17872cfe4064d\",\"signature\":\"MEUCID/exybli2HXWsp9h4iFZIXCTAlvZZcaizBj+dIOfOfRAiEAuxdEPEnwG3MWFlChfZ8NfUvHp+QRoLKu4NXhyFQYNBM=\"}");
        AppUpdateChecker.VerifyManifestSignature(manifest, valid);
        Assert.Throws<InvalidOperationException>(() => AppUpdateChecker.VerifyManifestSignature([.. manifest, 0x20], valid));
        Assert.Throws<InvalidOperationException>(() => AppUpdateChecker.VerifyManifestSignature(manifest, Encoding.UTF8.GetBytes("{\"schemaVersion\":1,\"algorithm\":\"ecdsa-p256-sha256\",\"keyId\":\"bad\",\"manifestSha256\":\"bad\",\"signature\":\"%%%\"}")));
        Assert.Throws<InvalidOperationException>(() => AppUpdateChecker.VerifyManifestSignature(manifest, new byte[4097]));
    }

    [Fact]
    public async Task MissingSignatureFailsClosedBeforeManifestParsing()
    {
        var handler = new SequenceHandler(Response(HttpStatusCode.OK, Manifest()), Response(HttpStatusCode.NotFound));
        using var client = new HttpClient(handler);
        await Assert.ThrowsAsync<InvalidOperationException>(() => AppUpdateChecker.CheckAsync(
            client, "1.7.1", Architecture.X64, TestContext.Current.CancellationToken));
        Assert.Equal(new[] { ManifestUri, new Uri(ManifestUri.AbsoluteUri + ".sig") }, handler.RequestUris);
    }

    [Fact]
    public async Task DownloadsPackageThroughTrustedRedirectAndVerifiesIt()
    {
        var contents = Encoding.UTF8.GetBytes("portable-exe-contents");
        var assetUri = new Uri("https://release-assets.githubusercontent.com/github-production-release-asset/123/Rivune-x64.exe?token=signed");
        var handler = new SequenceHandler(
            Response(HttpStatusCode.Redirect, location: assetUri),
            Response(HttpStatusCode.OK, contents));
        using var client = new HttpClient(handler);
        await using var destination = new MemoryStream();

        await AppUpdateChecker.DownloadPackageAsync(
            client,
            Package(contents),
            destination,
            TestContext.Current.CancellationToken);

        Assert.Equal(contents, destination.ToArray());
        Assert.Equal(new[] { PackageUri, assetUri }, handler.RequestUris);
        Assert.All(handler.Accepts, accept => Assert.Equal("application/octet-stream", accept));
    }

    [Fact]
    public async Task ExtractsOnlySelectedExecutableFromVerifiedBundle()
    {
        var x64 = Encoding.UTF8.GetBytes("x64 executable payload");
        var arm64 = Encoding.UTF8.GetBytes("arm64 executable payload");
        var bundle = Bundle(x64, arm64, Encoding.UTF8.GetBytes("uninstaller payload"));
        var root = Path.Combine(Path.GetTempPath(), "Rivune", "bundle-tests", Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(root);
        var bundlePath = Path.Combine(root, "Rivune-Windows.exe");
        var destination = Path.Combine(root, "Rivune.exe");
        await File.WriteAllBytesAsync(bundlePath, bundle, TestContext.Current.CancellationToken);
        var package = new WindowsUpdatePackage(
            PackageUri,
            "exe",
            ["arm64", "x64"],
            "10.0.19041.0",
            "Rivune-Windows.exe",
            bundle.LongLength,
            Convert.ToHexStringLower(SHA256.HashData(bundle)),
            "Rivune-x64.exe",
            x64.LongLength,
            Convert.ToHexStringLower(SHA256.HashData(x64)));
        try
        {
            await AppUpdateChecker.ExtractExecutableAsync(package, bundlePath, destination, TestContext.Current.CancellationToken);
            Assert.Equal(x64, await File.ReadAllBytesAsync(destination, TestContext.Current.CancellationToken));
        }
        finally
        {
            Directory.Delete(root, recursive: true);
        }
    }

    [Theory]
    [InlineData(-1, "size")]
    [InlineData(1, "size")]
    public async Task RejectsPackageLengthMismatchAndClearsDestination(int sizeDifference, string expectedMessage)
    {
        var contents = Encoding.UTF8.GetBytes("portable-exe-contents");
        var handler = new SequenceHandler(new ResponseSpec(HttpStatusCode.OK, contents, null, true));
        using var client = new HttpClient(handler);
        await using var destination = new MemoryStream();

        var exception = await Assert.ThrowsAsync<InvalidOperationException>(() => AppUpdateChecker.DownloadPackageAsync(
            client,
            Package(contents, contents.LongLength + sizeDifference),
            destination,
            TestContext.Current.CancellationToken));

        Assert.Contains(expectedMessage, exception.Message, StringComparison.OrdinalIgnoreCase);
        Assert.Equal(0, destination.Length);
    }

    [Fact]
    public async Task RejectsPackageHashMismatchAndClearsDestination()
    {
        var contents = Encoding.UTF8.GetBytes("portable-exe-contents");
        var handler = new SequenceHandler(new ResponseSpec(HttpStatusCode.OK, contents, null, true));
        using var client = new HttpClient(handler);
        await using var destination = new MemoryStream();

        var exception = await Assert.ThrowsAsync<InvalidOperationException>(() => AppUpdateChecker.DownloadPackageAsync(
            client,
            Package(contents, sha256: new string('0', 64)),
            destination,
            TestContext.Current.CancellationToken));

        Assert.Contains("SHA-256", exception.Message);
        Assert.Equal(0, destination.Length);
    }

    [Fact]
    public async Task RejectsUnsafePackageRedirectWithoutWriting()
    {
        var contents = Encoding.UTF8.GetBytes("portable-exe-contents");
        var handler = new SequenceHandler(Response(
            HttpStatusCode.Redirect,
            location: new Uri("https://evil.example/Rivune-x64.exe")));
        using var client = new HttpClient(handler);
        await using var destination = new MemoryStream();

        var exception = await Assert.ThrowsAsync<InvalidOperationException>(() => AppUpdateChecker.DownloadPackageAsync(
            client,
            Package(contents),
            destination,
            TestContext.Current.CancellationToken));

        Assert.Contains("untrusted host", exception.Message);
        Assert.Equal(0, destination.Length);
        Assert.Single(handler.RequestUris);
    }

    [Fact]
    public async Task ClearsPartialDestinationWhenPackageRequestFails()
    {
        var contents = Encoding.UTF8.GetBytes("portable-exe-contents");
        var handler = new SequenceHandler(Response(HttpStatusCode.ServiceUnavailable));
        using var client = new HttpClient(handler);
        await using var destination = new MemoryStream(Encoding.UTF8.GetBytes("partial"), writable: true);

        var exception = await Assert.ThrowsAsync<HttpRequestException>(() => AppUpdateChecker.DownloadPackageAsync(
            client,
            Package(contents),
            destination,
            TestContext.Current.CancellationToken));

        Assert.Equal(HttpStatusCode.ServiceUnavailable, exception.StatusCode);
        Assert.Equal(0, destination.Length);
    }

    [Fact]
    public void ExpectedUpdateVersionMustBeExactBeforeReadingProductVersion()
    {
        var readerCalled = false;

        Assert.Throws<InvalidOperationException>(() => PortableAppUpdate.VerifyProductVersion(
            "Rivune-x64.exe",
            "v1.7.2",
            _ => { readerCalled = true; return "1.7.2"; }));

        Assert.False(readerCalled);
    }

    [Fact]
    public void UpdateProductVersionMustMatchManifestExactly()
    {
        PortableAppUpdate.VerifyProductVersion(
            "Rivune-x64.exe",
            "1.7.2-rc.1+release",
            _ => "1.7.2-rc.1+release");

        Assert.Throws<InvalidOperationException>(() => PortableAppUpdate.VerifyProductVersion(
            "Rivune-x64.exe", "1.7.2", _ => null));
        var mismatch = Assert.Throws<InvalidOperationException>(() => PortableAppUpdate.VerifyProductVersion(
            "Rivune-x64.exe", "1.7.2", _ => "1.7.3"));
        Assert.Contains("ProductVersion", mismatch.Message, StringComparison.Ordinal);
    }

    [Theory]
    [InlineData("1.07.2")]
    [InlineData("v1.7.2")]
    [InlineData("1.7")]
    [InlineData("1.7.2-beta.01")]
    public void RejectsInvalidSemanticVersion(string version)
    {
        Assert.Throws<InvalidOperationException>(() =>
            AppUpdateChecker.CompareSemanticVersions(version, "1.7.2"));
    }

    [Theory]
    [InlineData("Rivune.exe")]
    [InlineData("Rivune-x64.exe")]
    [InlineData("Rivune-arm64.exe")]
    public void ParsesExactPortableApplyArguments(string fileName)
    {
        var source = Path.Combine(Path.GetTempPath(), "Rivune", "updates", "test", fileName);
        var target = Path.Combine(Path.GetTempPath(), "Rivune-installed", fileName);

        var command = PortableAppUpdate.ParseStartupCommand(
            [
                PortableAppUpdate.ApplySwitch,
                "--source", source,
                "--target", target,
                "--wait-pid", "12345",
                "--size", "42",
                "--sha256", PackageSha256,
                "--expected-version", "1.7.2-rc.1+release",
            ],
            source);

        var apply = Assert.IsType<PortableUpdateStartupCommand.Apply>(command);
        Assert.Equal(Path.GetFullPath(source), apply.Request.SourcePath);
        Assert.Equal(Path.GetFullPath(target), apply.Request.TargetPath);
        Assert.Equal(12345, apply.Request.ParentProcessId);
        Assert.Equal(42, apply.Request.Size);
        Assert.Equal(PackageSha256, apply.Request.Sha256);
        Assert.Equal("1.7.2-rc.1+release", apply.Request.ExpectedVersion);
    }

    [Theory]
    [InlineData("--wait-pid", "0")]
    [InlineData("--size", "0")]
    [InlineData("--sha256", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")]
    [InlineData("--expected-version", "v1.7.2")]
    public void RejectsInvalidPortableApplyArguments(string name, string value)
    {
        var source = Path.Combine(Path.GetTempPath(), "Rivune", "updates", "test", "Rivune-x64.exe");
        var target = Path.Combine(Path.GetTempPath(), "Rivune-installed", "Rivune-x64.exe");
        var arguments = new[]
        {
            PortableAppUpdate.ApplySwitch,
            "--source", source,
            "--target", target,
            "--wait-pid", "12345",
            "--size", "42",
            "--sha256", PackageSha256,
            "--expected-version", "1.7.2",
        };
        arguments[Array.IndexOf(arguments, name) + 1] = value;

        Assert.Throws<InvalidOperationException>(() =>
            PortableAppUpdate.ParseStartupCommand(arguments, source));
    }

    [Theory]
    [InlineData("Rivune.exe", "Rivune-x64.exe")]
    [InlineData("Rivune-x64.exe", "Rivune.exe")]
    [InlineData("Rivune-arm64.exe", "Rivune.exe")]
    [InlineData("Rivune-x64.exe", "Rivune-arm64.exe")]
    [InlineData("Rivune-arm64.exe", "Rivune-x64.exe")]
    [InlineData("Rivune-x64.exe", "other.exe")]
    [InlineData("other.exe", "Rivune-x64.exe")]
    [InlineData("other.exe", "other.exe")]
    public void RejectsMismatchedOrUnsupportedPortableHandoffNames(string sourceName, string targetName)
    {
        var source = Path.Combine(Path.GetTempPath(), "Rivune", "updates", "test", sourceName);
        var target = Path.Combine(Path.GetTempPath(), "Rivune-installed", targetName);

        Assert.Throws<InvalidOperationException>(() => PortableAppUpdate.ParseStartupCommand(
            [
                PortableAppUpdate.ApplySwitch,
                "--source", source,
                "--target", target,
                "--wait-pid", "12345",
                "--size", "42",
                "--sha256", PackageSha256,
                "--expected-version", "1.7.2",
            ],
            source));
    }

    [Fact]
    public void RejectsCleanupOutsideTemporaryDirectory()
    {
        var current = Path.Combine(Path.GetTempPath(), "Rivune-installed", "Rivune-x64.exe");
        var untrusted = Path.Combine(Path.GetPathRoot(Path.GetTempPath())!, "Rivune-x64.exe");

        Assert.Throws<InvalidOperationException>(() => PortableAppUpdate.ParseStartupCommand(
            [PortableAppUpdate.CleanupSwitch, untrusted],
            current));
    }

    [Theory]
    [InlineData("Rivune.exe")]
    [InlineData("Rivune-x64.exe")]
    [InlineData("Rivune-arm64.exe")]
    public void PreparesQuotedHandoffWithoutChangingCurrentExecutable(string fileName)
    {
        var root = Path.Combine(Path.GetTempPath(), "Rivune tests", Guid.NewGuid().ToString("N"));
        var sourceDirectory = Path.Combine(root, "updates", "source with spaces");
        var targetDirectory = Path.Combine(root, "installed with spaces");
        Directory.CreateDirectory(sourceDirectory);
        Directory.CreateDirectory(targetDirectory);
        var source = Path.Combine(sourceDirectory, fileName);
        var target = Path.Combine(targetDirectory, fileName);
        var currentContents = Encoding.UTF8.GetBytes("current executable");
        var updateContents = Encoding.UTF8.GetBytes("verified update");
        File.WriteAllBytes(source, updateContents);
        File.WriteAllBytes(target, currentContents);
        try
        {
            var digest = Convert.ToHexStringLower(SHA256.HashData(updateContents));
            var startInfo = PortableAppUpdate.PrepareHandoff(
                source,
                target,
                12345,
                updateContents.Length,
                digest,
                "1.7.2-rc.1+release");

            Assert.Equal(source, startInfo.FileName);
            Assert.False(startInfo.UseShellExecute);
            Assert.Equal(
                [
                    PortableAppUpdate.ApplySwitch,
                    "--source", source,
                    "--target", target,
                    "--wait-pid", "12345",
                    "--size", updateContents.Length.ToString(),
                    "--sha256", digest,
                    "--expected-version", "1.7.2-rc.1+release",
                ],
                startInfo.ArgumentList);
            Assert.Equal(currentContents, File.ReadAllBytes(target));
        }
        finally
        {
            Directory.Delete(root, recursive: true);
        }
    }

    [Fact]
    public async Task FailedPostHandoffLaunchRestoresCurrentExecutable()
    {
        var root = Path.Combine(Path.GetTempPath(), "Rivune", "updates", Guid.NewGuid().ToString("N"));
        var targetDirectory = Path.Combine(Path.GetTempPath(), "Rivune-installed-tests", Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(root);
        Directory.CreateDirectory(targetDirectory);
        var source = Path.Combine(root, "Rivune-x64.exe");
        var target = Path.Combine(targetDirectory, "Rivune-x64.exe");
        var currentContents = Encoding.UTF8.GetBytes("not a Windows executable: current");
        var updateContents = Encoding.UTF8.GetBytes("not a Windows executable: update");
        File.WriteAllBytes(source, updateContents);
        File.WriteAllBytes(target, currentContents);
        try
        {
            var request = new PortableUpdateApplyRequest(
                source,
                target,
                int.MaxValue,
                updateContents.Length,
                Convert.ToHexStringLower(SHA256.HashData(updateContents)),
                "1.7.2");

            await Assert.ThrowsAnyAsync<Exception>(() =>
                PortableAppUpdate.ApplyAsync(request, TestContext.Current.CancellationToken, (_, _) => { }));

            Assert.Equal(currentContents, File.ReadAllBytes(target));
        }
        finally
        {
            Directory.Delete(root, recursive: true);
            Directory.Delete(targetDirectory, recursive: true);
        }
    }

    [Fact]
    public async Task ApplyVerifiesDownloadedAndStagedProductVersionsBeforeReplacement()
    {
        var root = Path.Combine(Path.GetTempPath(), "Rivune", "updates", Guid.NewGuid().ToString("N"));
        var targetDirectory = Path.Combine(Path.GetTempPath(), "Rivune-installed-tests", Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(root);
        Directory.CreateDirectory(targetDirectory);
        var source = Path.Combine(root, "Rivune-x64.exe");
        var target = Path.Combine(targetDirectory, "Rivune-x64.exe");
        var updateContents = Encoding.UTF8.GetBytes("unsigned update fixture");
        File.WriteAllBytes(source, updateContents);
        File.WriteAllBytes(target, "current"u8.ToArray());
        var verifiedPaths = new List<string>();
        try
        {
            var request = new PortableUpdateApplyRequest(
                source,
                target,
                int.MaxValue,
                updateContents.Length,
                Convert.ToHexStringLower(SHA256.HashData(updateContents)),
                "1.7.2-rc.1+release");

            await Assert.ThrowsAnyAsync<Exception>(() => PortableAppUpdate.ApplyAsync(
                request,
                TestContext.Current.CancellationToken,
                (path, expectedVersion) =>
                {
                    Assert.Equal("1.7.2-rc.1+release", expectedVersion);
                    verifiedPaths.Add(path);
                }));

            Assert.Equal(2, verifiedPaths.Count);
            Assert.Equal(source, verifiedPaths[0]);
            Assert.StartsWith(targetDirectory, verifiedPaths[1], StringComparison.OrdinalIgnoreCase);
        }
        finally
        {
            Directory.Delete(root, recursive: true);
            Directory.Delete(targetDirectory, recursive: true);
        }
    }

    private static async Task AssertInvalidManifestAsync(string manifest)
    {
        using var client = new HttpClient(new SequenceHandler(Response(HttpStatusCode.OK, manifest)));
        await Assert.ThrowsAsync<InvalidOperationException>(() =>
            AppUpdateChecker.CheckUnsignedForTestingAsync(client, "1.7.1", Architecture.X64, TestContext.Current.CancellationToken));
    }

    private static string Manifest() => $$"""
        {
          "schemaVersion":3,
          "channel":"stable",
          "version":"1.7.2",
          "tagName":"v1.7.2",
          "publishedAt":"2026-08-14T12:34:56Z",
          "releaseUrl":"https://github.com/moodiness/rivune/releases/tag/v1.7.2",
          "packages":{
            "android":{
              "format":"apk",
              "architectures":["universal"],
              "minimumOsVersion":"8.0",
              "applicationId":"io.rivune.app",
              "buildVersion":"10702",
              "signingCertificateSha256":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
              "fileName":"Rivune-Android.apk",
              "url":"https://github.com/moodiness/rivune/releases/download/v1.7.2/Rivune-Android.apk",
              "size":654321,
              "sha256":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
            },
            "windows":{
              "format":"exe",
              "architectures":["arm64","x64"],
              "minimumOsVersion":"10.0.19041.0",
              "fileName":"Rivune-Windows.exe",
              "url":"https://github.com/moodiness/rivune/releases/download/v1.7.2/Rivune-Windows.exe",
              "size":123456,
              "sha256":"{{PackageSha256}}",
              "executables":{
                "x64":{"fileName":"Rivune-x64.exe","size":345678,"sha256":"{{X64ExecutableSha256}}"},
                "arm64":{"fileName":"Rivune-arm64.exe","size":456789,"sha256":"{{Arm64ExecutableSha256}}"}
              }
            }
          }
        }
        """;

    private static readonly Uri PackageUri = new("https://github.com/moodiness/rivune/releases/download/v1.7.2/Rivune-Windows.exe");

    private static WindowsUpdatePackage Package(byte[] contents, long? size = null, string? sha256 = null) => new(
        PackageUri,
        "exe",
        ["arm64", "x64"],
        "10.0.19041.0",
        "Rivune-Windows.exe",
        size ?? contents.LongLength,
        sha256 ?? Convert.ToHexStringLower(SHA256.HashData(contents)),
        "Rivune-x64.exe",
        1,
        X64ExecutableSha256);

    private static byte[] Bundle(byte[] x64, byte[] arm64, byte[]? uninstaller = null)
    {
        using var output = new MemoryStream();
        using (var archive = new ZipArchive(output, ZipArchiveMode.Create, leaveOpen: true))
        {
            foreach (var (name, contents) in new[]
                     {
                         ("Rivune-x64.exe", x64),
                         ("Rivune-arm64.exe", arm64),
                         ("Rivune-Uninstall.exe", uninstaller ?? Encoding.UTF8.GetBytes("uninstaller payload")),
                     })
            {
                var entry = archive.CreateEntry(name, CompressionLevel.Optimal);
                using var stream = entry.Open();
                stream.Write(contents);
            }
        }
        return output.ToArray();
    }

    private static ResponseSpec Response(HttpStatusCode statusCode, string body = "", Uri? location = null) =>
        new(statusCode, Encoding.UTF8.GetBytes(body), location, false);

    private static ResponseSpec Response(HttpStatusCode statusCode, byte[] body, Uri? location = null) =>
        new(statusCode, body, location, false);

    private sealed record ResponseSpec(
        HttpStatusCode StatusCode,
        byte[] Body,
        Uri? Location,
        bool UnknownLength);

    private sealed class SequenceHandler(params ResponseSpec[] responses) : HttpMessageHandler
    {
        private readonly Queue<ResponseSpec> _responses = new(responses);

        public List<Uri> RequestUris { get; } = [];
        public List<string?> Accepts { get; } = [];

        protected override Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken)
        {
            RequestUris.Add(request.RequestUri!);
            Accepts.Add(request.Headers.Accept.SingleOrDefault()?.MediaType);
            var specification = _responses.Dequeue();
            HttpContent content = specification.UnknownLength
                ? new UnknownLengthContent(specification.Body)
                : new ByteArrayContent(specification.Body);
            content.Headers.ContentType = new("application/json");
            var response = new HttpResponseMessage(specification.StatusCode)
            {
                Content = content,
                RequestMessage = request,
            };
            response.Headers.Location = specification.Location;
            return Task.FromResult(response);
        }
    }

    private sealed class UnknownLengthContent(byte[] body) : HttpContent
    {
        protected override Task SerializeToStreamAsync(Stream stream, TransportContext? context) =>
            stream.WriteAsync(body).AsTask();

        protected override bool TryComputeLength(out long length)
        {
            length = 0;
            return false;
        }

        protected override Task<Stream> CreateContentReadStreamAsync() =>
            Task.FromResult<Stream>(new MemoryStream(body, writable: false));
    }
}
