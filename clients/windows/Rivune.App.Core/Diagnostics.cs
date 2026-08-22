using System.Globalization;
using System.Text;

namespace Rivune.App;

internal static class DiagnosticLimits
{
    internal const int MaximumEventBytes = 64 * 1_024;
    internal const int MaximumReportBytes = 64 * 1_024;
    internal const string ReportFileName = "rivune-diagnostics.txt";
}

internal enum DiagnosticEventCode
{
    AppStarted,
    ServerConnectionStarted,
    ServerConnectionSucceeded,
    ServerConnectionFailed,
    CatalogRefreshStarted,
    CatalogRefreshSucceeded,
    CatalogRefreshFailed,
    PlaybackStarted,
    PlaybackStopped,
    PlaybackFailed,
    UpdateCheckStarted,
    UpdateAvailable,
    UpdateUpToDate,
    UpdateCheckFailed,
    DiagnosticExportSucceeded,
    DiagnosticExportFailed,
}

internal sealed record DiagnosticEvent(DateTimeOffset Timestamp, DiagnosticEventCode Code);

internal sealed class DiagnosticsBuffer
{
    private sealed record BufferedEvent(DiagnosticEvent Event, int Bytes);

    private readonly int _maximumBytes;
    private readonly Func<DateTimeOffset> _now;
    private readonly object _sync = new();
    private readonly Queue<BufferedEvent> _events = new();
    private int _bytes;

    internal DiagnosticsBuffer(
        int maximumBytes = DiagnosticLimits.MaximumEventBytes,
        Func<DateTimeOffset>? now = null)
    {
        ArgumentOutOfRangeException.ThrowIfLessThan(maximumBytes, 1);
        ArgumentOutOfRangeException.ThrowIfGreaterThan(maximumBytes, DiagnosticLimits.MaximumEventBytes);
        _maximumBytes = maximumBytes;
        _now = now ?? (() => DateTimeOffset.UtcNow);
    }

    internal void Record(DiagnosticEventCode code) => Record(code, _now());

    internal void Record(DiagnosticEventCode code, DateTimeOffset timestamp)
    {
        var diagnosticEvent = new DiagnosticEvent(timestamp, code);
        var buffered = new BufferedEvent(
            diagnosticEvent,
            Encoding.UTF8.GetByteCount(DiagnosticsReport.SerializedEventLine(diagnosticEvent)));
        if (buffered.Bytes > _maximumBytes) return;
        lock (_sync)
        {
            while (_events.Count > 0 && _bytes + buffered.Bytes > _maximumBytes)
                _bytes -= _events.Dequeue().Bytes;
            _events.Enqueue(buffered);
            _bytes += buffered.Bytes;
        }
    }

    internal IReadOnlyList<DiagnosticEvent> Snapshot()
    {
        lock (_sync) return _events.Select(value => value.Event).ToArray();
    }

    internal void Clear()
    {
        lock (_sync)
        {
            _events.Clear();
            _bytes = 0;
        }
    }
}

internal sealed record DiagnosticReportInput
{
    internal required DateTimeOffset GeneratedAt { get; init; }
    internal required string AppVersion { get; init; }
    internal required string AppBuild { get; init; }
    internal required string Platform { get; init; }
    internal required string OperatingSystemVersion { get; init; }
    internal required string Architecture { get; init; }
    internal required bool IsTelevision { get; init; }
    internal string? ServerAddress { get; init; }
    internal string? ServerDisplayName { get; init; }
    internal string? ServerVersion { get; init; }
    internal int? ServerProtocolVersion { get; init; }
    internal string? StartupTab { get; init; }
    internal string? PreferredPlayer { get; init; }
    internal string? AnimationPreference { get; init; }
    internal string? AccentColor { get; init; }
    internal string? VideoAspect { get; init; }
    internal IReadOnlyList<DiagnosticEvent> Events { get; init; } = [];
}

internal static class DiagnosticsReport
{
    private const int MaximumScalarBytes = 512;
    private const int MaximumServerUrlLength = 4_096;
    private const string Unavailable = "unavailable";

    internal static string Build(DiagnosticReportInput input)
    {
        var report = new StringBuilder(2_048);
        report.Append("Rivune Windows diagnostics\n");
        report.Append("Report format: 1\n");
        AppendField(report, "Generated at", FormatTimestamp(input.GeneratedAt));
        AppendField(report, "App version", SafeScalar(input.AppVersion));
        AppendField(report, "App build", SafeScalar(input.AppBuild));
        AppendField(report, "Platform", SafeScalar(input.Platform));
        AppendField(report, "OS version", SafeScalar(input.OperatingSystemVersion));
        AppendField(report, "Architecture", SafeScalar(input.Architecture));
        AppendField(report, "TV device", input.IsTelevision ? "yes" : "no");
        AppendField(report, "Server origin", SanitizeServerOrigin(input.ServerAddress) ?? Unavailable);
        AppendField(report, "Server name", SafeScalar(input.ServerDisplayName));
        AppendField(report, "Server version", SafeScalar(input.ServerVersion));
        AppendField(report, "Server protocol", input.ServerProtocolVersion?.ToString(CultureInfo.InvariantCulture) ?? Unavailable);
        AppendField(report, "Startup tab", SafeScalar(input.StartupTab));
        AppendField(report, "Preferred player", SafeScalar(input.PreferredPlayer));
        AppendField(report, "Animations", SafeScalar(input.AnimationPreference));
        AppendField(report, "Accent color", SafeScalar(input.AccentColor));
        AppendField(report, "Video aspect", SafeScalar(input.VideoAspect));
        report.Append("Events:\n");

        var remainingBytes = Math.Max(DiagnosticLimits.MaximumReportBytes - Encoding.UTF8.GetByteCount(report.ToString()), 0);
        var retainedLines = new List<string>();
        for (var index = input.Events.Count - 1; index >= 0; index--)
        {
            var line = SerializedEventLine(input.Events[index]);
            var lineBytes = Encoding.UTF8.GetByteCount(line);
            if (lineBytes > remainingBytes) break;
            retainedLines.Add(line);
            remainingBytes -= lineBytes;
        }
        for (var index = retainedLines.Count - 1; index >= 0; index--) report.Append(retainedLines[index]);
        return report.ToString();
    }

    internal static byte[] BuildUtf8(DiagnosticReportInput input) => Encoding.UTF8.GetBytes(Build(input));

    internal static string? SanitizeServerOrigin(string? value)
    {
        var candidate = value?.Trim();
        if (string.IsNullOrEmpty(candidate) || Encoding.UTF8.GetByteCount(candidate) > MaximumServerUrlLength ||
            ContainsUnsafeScalar(candidate) || !Uri.TryCreate(candidate, UriKind.Absolute, out var uri) ||
            string.IsNullOrEmpty(uri.Host)) return null;
        var scheme = uri.Scheme.ToLowerInvariant();
        if (scheme is not ("http" or "https")) return null;
        var host = uri.Host.Trim('[', ']').ToLowerInvariant();
        if (string.IsNullOrEmpty(host) || host.Contains('%') || ContainsUnsafeScalar(host)) return null;
        var renderedHost = host.Contains(':') ? $"[{host}]" : host;
        var origin = $"{scheme}://{renderedHost}{(uri.IsDefaultPort ? string.Empty : $":{uri.Port}")}";
        return Encoding.UTF8.GetByteCount(origin) <= MaximumServerUrlLength ? origin : null;
    }

    internal static string SerializedEventLine(DiagnosticEvent diagnosticEvent) =>
        $"{FormatTimestamp(diagnosticEvent.Timestamp)} {StableCode(diagnosticEvent.Code)}\n";

    private static string StableCode(DiagnosticEventCode code) => code switch
    {
        DiagnosticEventCode.AppStarted => "APP_STARTED",
        DiagnosticEventCode.ServerConnectionStarted => "SERVER_CONNECTION_STARTED",
        DiagnosticEventCode.ServerConnectionSucceeded => "SERVER_CONNECTION_SUCCEEDED",
        DiagnosticEventCode.ServerConnectionFailed => "SERVER_CONNECTION_FAILED",
        DiagnosticEventCode.CatalogRefreshStarted => "CATALOG_REFRESH_STARTED",
        DiagnosticEventCode.CatalogRefreshSucceeded => "CATALOG_REFRESH_SUCCEEDED",
        DiagnosticEventCode.CatalogRefreshFailed => "CATALOG_REFRESH_FAILED",
        DiagnosticEventCode.PlaybackStarted => "PLAYBACK_STARTED",
        DiagnosticEventCode.PlaybackStopped => "PLAYBACK_STOPPED",
        DiagnosticEventCode.PlaybackFailed => "PLAYBACK_FAILED",
        DiagnosticEventCode.UpdateCheckStarted => "UPDATE_CHECK_STARTED",
        DiagnosticEventCode.UpdateAvailable => "UPDATE_AVAILABLE",
        DiagnosticEventCode.UpdateUpToDate => "UPDATE_UP_TO_DATE",
        DiagnosticEventCode.UpdateCheckFailed => "UPDATE_CHECK_FAILED",
        DiagnosticEventCode.DiagnosticExportSucceeded => "DIAGNOSTIC_EXPORT_SUCCEEDED",
        DiagnosticEventCode.DiagnosticExportFailed => "DIAGNOSTIC_EXPORT_FAILED",
        _ => throw new ArgumentOutOfRangeException(nameof(code)),
    };

    private static void AppendField(StringBuilder report, string label, string? value) =>
        report.Append(label).Append(": ").Append(value ?? Unavailable).Append('\n');

    private static string SafeScalar(string? value)
    {
        if (value is null) return Unavailable;
        var output = new StringBuilder(Math.Min(value.Length, MaximumScalarBytes));
        var usedBytes = 0;
        foreach (var rune in value.EnumerateRunes())
        {
            if (IsUnsafeScalar(rune)) continue;
            if (usedBytes + rune.Utf8SequenceLength > MaximumScalarBytes) break;
            output.Append(rune.ToString());
            usedBytes += rune.Utf8SequenceLength;
        }
        var result = output.ToString().Trim();
        return result.Length == 0 ? Unavailable : result;
    }

    private static bool ContainsUnsafeScalar(string value) => value.EnumerateRunes().Any(IsUnsafeScalar);

    private static bool IsUnsafeScalar(Rune rune) => Rune.GetUnicodeCategory(rune) is
        UnicodeCategory.Control or
        UnicodeCategory.Format or
        UnicodeCategory.LineSeparator or
        UnicodeCategory.ParagraphSeparator or
        UnicodeCategory.Surrogate;

    private static string FormatTimestamp(DateTimeOffset value)
    {
        var utc = value.ToUniversalTime();
        utc = utc.AddTicks(-(utc.Ticks % TimeSpan.TicksPerMillisecond));
        return utc.Millisecond == 0
            ? utc.ToString("yyyy-MM-dd'T'HH:mm:ss'Z'", CultureInfo.InvariantCulture)
            : utc.ToString("yyyy-MM-dd'T'HH:mm:ss.fff'Z'", CultureInfo.InvariantCulture);
    }
}
