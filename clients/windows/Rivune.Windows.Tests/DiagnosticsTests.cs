using Xunit;
using System.Text;
using Rivune.App;

namespace Rivune.Windows.Tests;

public sealed class DiagnosticsTests
{
    [Fact]
    public void ServerAddressIsReducedToOrigin()
    {
        Assert.Equal(
            "https://media.example:8443",
            DiagnosticsReport.SanitizeServerOrigin(
                "https://diagnostic-user:diagnostic-password@Media.Example:8443/private/profile?access_token=secret#fragment"));
        Assert.Equal("http://media.example", DiagnosticsReport.SanitizeServerOrigin("http://media.example:80/library"));
        Assert.Equal("https://[2001:db8::1]:9443", DiagnosticsReport.SanitizeServerOrigin("https://[2001:db8::1]:9443/api"));
        Assert.Null(DiagnosticsReport.SanitizeServerOrigin("file://media.example/private"));
        Assert.Null(DiagnosticsReport.SanitizeServerOrigin("https://media.example\nAuthorization: Bearer secret"));
        Assert.Equal("https://[fe80::1]", DiagnosticsReport.SanitizeServerOrigin("https://[fe80::1%25Ethernet]/private"));
        Assert.Null(DiagnosticsReport.SanitizeServerOrigin($"https://e{new string('\u0301', 4_096)}.example"));
    }

    [Fact]
    public void ReportHasStableAllowlistedMetadataAndNoUrlSecrets()
    {
        var report = DiagnosticsReport.Build(Input([
            new DiagnosticEvent(DateTimeOffset.FromUnixTimeMilliseconds(1_000), DiagnosticEventCode.AppStarted),
        ]));
        var expected = ("""
            Rivune Windows diagnostics
            Report format: 1
            Generated at: 1970-01-01T00:00:00Z
            App version: 1.2.3
            App build: 42
            Platform: windows
            OS version: 10.0.26100
            Architecture: x64
            TV device: no
            Server origin: https://media.example:8443
            Server name: Living Room
            Server version: 10.2.0
            Server protocol: 20
            Startup tab: library
            Preferred player: windows-native
            Animations: reduced
            Accent color: #FF7D8C
            Video aspect: zoom
            Events:
            1970-01-01T00:00:01Z APP_STARTED
            """).ReplaceLineEndings("\n") + "\n";

        Assert.Equal(expected, report);
        foreach (var secret in new[] { "diagnostic-user", "diagnostic-password", "private-profile", "access_token", "diagnostic-token", "diagnostic-fragment" })
            Assert.DoesNotContain(secret, report, StringComparison.Ordinal);
    }

    [Fact]
    public void ReportIsUtf8BoundedAndScalarValuesCannotInjectLines()
    {
        var oversized = string.Concat(Enumerable.Repeat("🚀", DiagnosticLimits.MaximumReportBytes));
        var events = Enumerable.Range(0, 4_000)
            .Select(value => new DiagnosticEvent(
                DateTimeOffset.FromUnixTimeMilliseconds(value),
                DiagnosticEventCode.ServerConnectionFailed))
            .ToArray();
        var report = DiagnosticsReport.Build(Input(events) with
        {
            AppVersion = $"1.2.3\nInjected: secret\0{oversized}",
            Architecture = oversized,
        });

        Assert.True(Encoding.UTF8.GetByteCount(report) <= DiagnosticLimits.MaximumReportBytes);
        Assert.DoesNotContain("\nInjected:", report, StringComparison.Ordinal);
        Assert.DoesNotContain('\0', report);
        Assert.Contains("1970-01-01T00:00:03.999Z SERVER_CONNECTION_FAILED\n", report, StringComparison.Ordinal);
        Assert.DoesNotContain("1970-01-01T00:00:00Z SERVER_CONNECTION_FAILED\n", report, StringComparison.Ordinal);
        Assert.Equal(Encoding.UTF8.GetBytes(report), DiagnosticsReport.BuildUtf8(Input(events) with
        {
            AppVersion = $"1.2.3\nInjected: secret\0{oversized}",
            Architecture = oversized,
        }));
    }

    [Fact]
    public void EventBufferEvictsOldestEntriesWithinByteLimit()
    {
        var buffer = new DiagnosticsBuffer(maximumBytes: 70);
        buffer.Record(DiagnosticEventCode.AppStarted, DateTimeOffset.FromUnixTimeMilliseconds(0));
        buffer.Record(DiagnosticEventCode.AppStarted, DateTimeOffset.FromUnixTimeMilliseconds(1_000));
        buffer.Record(DiagnosticEventCode.AppStarted, DateTimeOffset.FromUnixTimeMilliseconds(2_000));

        Assert.Equal([
            new DiagnosticEvent(DateTimeOffset.FromUnixTimeMilliseconds(1_000), DiagnosticEventCode.AppStarted),
            new DiagnosticEvent(DateTimeOffset.FromUnixTimeMilliseconds(2_000), DiagnosticEventCode.AppStarted),
        ], buffer.Snapshot());
    }

    private static DiagnosticReportInput Input(IReadOnlyList<DiagnosticEvent> events) => new()
    {
        GeneratedAt = DateTimeOffset.UnixEpoch,
        AppVersion = "1.2.3",
        AppBuild = "42",
        Platform = "windows",
        OperatingSystemVersion = "10.0.26100",
        Architecture = "x64",
        IsTelevision = false,
        ServerAddress = "https://diagnostic-user:diagnostic-password@media.example:8443/private-profile?access_token=diagnostic-token#diagnostic-fragment",
        ServerDisplayName = "Living Room",
        ServerVersion = "10.2.0",
        ServerProtocolVersion = 20,
        StartupTab = "library",
        PreferredPlayer = "windows-native",
        AnimationPreference = "reduced",
        AccentColor = "#FF7D8C",
        VideoAspect = "zoom",
        Events = events,
    };
}
