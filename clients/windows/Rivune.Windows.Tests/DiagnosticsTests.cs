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
            Server protocol: 22
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

    [Fact]
    public void SearchEventsContainOnlyBoundedCodesAndNeverQueryText()
    {
        const string secretQuery = "private title token=never-log-this";
        var report = DiagnosticsReport.Build(Input([
            new DiagnosticEvent(DateTimeOffset.UnixEpoch, DiagnosticEventCode.SearchStarted),
            new DiagnosticEvent(DateTimeOffset.UnixEpoch.AddSeconds(1), DiagnosticEventCode.SearchPartial),
            new DiagnosticEvent(DateTimeOffset.UnixEpoch.AddSeconds(2), DiagnosticEventCode.SearchCanceled),
        ]));

        Assert.Contains("SEARCH_STARTED", report, StringComparison.Ordinal);
        Assert.Contains("SEARCH_PARTIAL", report, StringComparison.Ordinal);
        Assert.Contains("SEARCH_CANCELED", report, StringComparison.Ordinal);
        Assert.DoesNotContain(secretQuery, report, StringComparison.Ordinal);
        Assert.DoesNotContain("token=", report, StringComparison.OrdinalIgnoreCase);
    }

    [Theory]
    [InlineData(0, "UPDATE_DOWNLOAD_FAILED")]
    [InlineData(1, "UPDATE_PACKAGE_FAILED")]
    [InlineData(2, "UPDATE_APPLY_FAILED")]
    public void UpdateTerminalFailuresUseClosedSecretFreeCodes(
        int phaseValue,
        string stableCode)
    {
        const string sensitiveFailure = "https://user:password@example.test/pkg?token=secret /private/update.exe";
        var code = AppUpdateFailureDiagnostics.TerminalCode((AppUpdateFailurePhase)phaseValue);
        var report = DiagnosticsReport.Build(Input([
            new DiagnosticEvent(DateTimeOffset.UnixEpoch, code),
        ]));

        Assert.Equal(stableCode, DiagnosticsReport.SerializedEventLine(new DiagnosticEvent(DateTimeOffset.UnixEpoch, code)).Split(' ')[1].Trim());
        Assert.Contains(stableCode, report, StringComparison.Ordinal);
        Assert.DoesNotContain(sensitiveFailure, report, StringComparison.Ordinal);
        Assert.DoesNotContain("token=", report, StringComparison.OrdinalIgnoreCase);
        Assert.DoesNotContain("update.exe", report, StringComparison.OrdinalIgnoreCase);
    }

    [Theory]
    [InlineData(0, "UPDATE_DOWNLOAD_CANCELED", "UPDATE_DOWNLOAD_FAILED")]
    [InlineData(1, "UPDATE_PACKAGE_CANCELED", "UPDATE_PACKAGE_FAILED")]
    [InlineData(2, "UPDATE_APPLY_CANCELED", "UPDATE_APPLY_FAILED")]
    public void ExplicitCancellationUsesItsPhaseAndIsNotFailure(int phaseValue, string canceledCode, string failureCode)
    {
        var phase = (AppUpdateFailurePhase)phaseValue;
        var canceled = AppUpdateFailureDiagnostics.TerminalCode(phase, canceled: true);
        var failure = AppUpdateFailureDiagnostics.TerminalCode(phase, canceled: false);
        var buffer = new DiagnosticsBuffer();
        buffer.Record(canceled, DateTimeOffset.UnixEpoch);

        Assert.NotEqual(failure, canceled);
        Assert.Single(buffer.Snapshot());
        var report = DiagnosticsReport.Build(Input(buffer.Snapshot()));
        Assert.Contains(canceledCode, report, StringComparison.Ordinal);
        Assert.DoesNotContain(failureCode, report, StringComparison.Ordinal);
        if (phase != AppUpdateFailurePhase.Download)
            Assert.DoesNotContain("UPDATE_DOWNLOAD_CANCELED", report, StringComparison.Ordinal);
    }

    [Fact]
    public void SupersededSearchesRetainOpaqueStartTerminalPairing()
    {
        var ticks = 0;
        var buffer = new DiagnosticsBuffer(now: () => DateTimeOffset.UnixEpoch.AddSeconds(Interlocked.Increment(ref ticks)));
        var first = buffer.BeginOperation(1, DiagnosticEventCode.SearchStarted);
        var second = buffer.BeginOperation(2, DiagnosticEventCode.SearchStarted);

        Assert.True(first.Complete(DiagnosticEventCode.SearchCanceled));
        Assert.True(second.Complete(DiagnosticEventCode.SearchSucceeded));
        Assert.False(first.Complete(DiagnosticEventCode.SearchFailed));

        var events = buffer.Snapshot();
        Assert.Equal([
            new DiagnosticEvent(DateTimeOffset.UnixEpoch.AddSeconds(1), DiagnosticEventCode.SearchStarted, 1),
            new DiagnosticEvent(DateTimeOffset.UnixEpoch.AddSeconds(2), DiagnosticEventCode.SearchStarted, 2),
            new DiagnosticEvent(DateTimeOffset.UnixEpoch.AddSeconds(3), DiagnosticEventCode.SearchCanceled, 1),
            new DiagnosticEvent(DateTimeOffset.UnixEpoch.AddSeconds(4), DiagnosticEventCode.SearchSucceeded, 2),
        ], events);
        var report = DiagnosticsReport.Build(Input(events));
        Assert.Contains("SEARCH_STARTED op=0000000000000001", report, StringComparison.Ordinal);
        Assert.Contains("SEARCH_CANCELED op=0000000000000001", report, StringComparison.Ordinal);
        Assert.Contains("SEARCH_STARTED op=0000000000000002", report, StringComparison.Ordinal);
        Assert.Contains("SEARCH_SUCCEEDED op=0000000000000002", report, StringComparison.Ordinal);
        Assert.DoesNotContain("query", report, StringComparison.OrdinalIgnoreCase);
        Assert.DoesNotContain("token=", report, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public void TimeoutWithoutExplicitCancellationRemainsDownloadFailure()
    {
        var code = AppUpdateFailureDiagnostics.TerminalCode(AppUpdateFailurePhase.Download, canceled: false);
        var report = DiagnosticsReport.Build(Input([
            new DiagnosticEvent(DateTimeOffset.UnixEpoch, code),
        ]));

        Assert.Equal(DiagnosticEventCode.UpdateDownloadFailed, code);
        Assert.Contains("UPDATE_DOWNLOAD_FAILED", report, StringComparison.Ordinal);
        Assert.DoesNotContain("UPDATE_DOWNLOAD_CANCELED", report, StringComparison.Ordinal);
    }

    [Fact]

    public void UnknownUpdateFailurePhaseIsRejected()
    {
        Assert.Throws<ArgumentOutOfRangeException>(() =>
            AppUpdateFailureDiagnostics.TerminalCode((AppUpdateFailurePhase)99));
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
        ServerProtocolVersion = 22,
        StartupTab = "library",
        PreferredPlayer = "windows-native",
        AnimationPreference = "reduced",
        AccentColor = "#FF7D8C",
        VideoAspect = "zoom",
        Events = events,
    };
}
