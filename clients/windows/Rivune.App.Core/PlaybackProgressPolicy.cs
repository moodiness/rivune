namespace Rivune.App;

internal static class PlaybackProgressPolicy
{
    internal readonly record struct Snapshot(
        int PositionSeconds,
        int DurationSeconds,
        bool Completed);

    internal static int StartSeconds(int positionSeconds, bool completed) =>
        completed ? 0 : Math.Max(0, positionSeconds);

    internal static bool IsCompleted(
        int positionSeconds,
        int durationSeconds,
        bool explicitlyCompleted) =>
        explicitlyCompleted ||
        durationSeconds > 0 && (long)positionSeconds * 100 >= (long)durationSeconds * 90;

    internal static Snapshot MergeConflict(Snapshot latestLocal, Snapshot remote)
    {
        var durationSeconds = latestLocal.DurationSeconds > 0
            ? latestLocal.DurationSeconds
            : remote.DurationSeconds;

        return new Snapshot(
            latestLocal.PositionSeconds,
            durationSeconds,
            IsCompleted(
                latestLocal.PositionSeconds,
                durationSeconds,
                latestLocal.Completed || remote.Completed));
    }
}
