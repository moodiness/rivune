namespace Rivune.App;

internal sealed class CoordinationPollingPolicy
{
    internal static readonly TimeSpan PresenceInterval = TimeSpan.FromSeconds(15);
    internal static readonly TimeSpan ActiveCommandInterval = TimeSpan.FromSeconds(2);
    internal static readonly TimeSpan IdleCommandInterval = TimeSpan.FromSeconds(15);
    internal static readonly TimeSpan RecentActivityWindow = TimeSpan.FromSeconds(30);

    private readonly TimeProvider _timeProvider;
    private DateTimeOffset? _lastCommandActivity;

    internal CoordinationPollingPolicy(TimeProvider? timeProvider = null) =>
        _timeProvider = timeProvider ?? TimeProvider.System;

    internal void MarkCommandActivity() => _lastCommandActivity = _timeProvider.GetUtcNow();

    internal static bool ShouldRun(bool foreground, bool closed, bool capabilityAvailable, bool profileActive) =>
        foreground && !closed && capabilityAvailable && profileActive;

    internal TimeSpan CommandInterval(bool playbackActive, bool roomActive, bool operationPendingOrExecuting)
    {
        var recent = _lastCommandActivity is { } last && _timeProvider.GetUtcNow() - last <= RecentActivityWindow;
        return playbackActive || roomActive || operationPendingOrExecuting || recent
            ? ActiveCommandInterval
            : IdleCommandInterval;
    }
}
