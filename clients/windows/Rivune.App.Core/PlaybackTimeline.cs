using Rivune.Windows;

namespace Rivune.App;

internal sealed class PlaybackTimeline
{
    public double OffsetSeconds { get; private set; }
    public double DurationSeconds { get; private set; } = 1;

    public void Reset(PlaybackMediaTimeline? timeline, double requestedStartSeconds, double? reportedDurationSeconds)
    {
        var start = Math.Max(0, requestedStartSeconds);
        OffsetSeconds = timeline == PlaybackMediaTimeline.Relative ? start : 0;
        DurationSeconds = Math.Max(1, Math.Max(start, reportedDurationSeconds ?? 0));
    }

    public double ToAbsolutePosition(TimeSpan mediaPosition) =>
        Math.Max(0, mediaPosition.TotalSeconds + OffsetSeconds);

    public TimeSpan ToMediaPosition(double absolutePosition) =>
        TimeSpan.FromSeconds(Math.Max(0, absolutePosition - OffsetSeconds));

    public double UpdateDuration(TimeSpan naturalDuration)
    {
        DurationSeconds = Math.Max(DurationSeconds, OffsetSeconds + Math.Max(0, naturalDuration.TotalSeconds));
        return DurationSeconds;
    }
}
