using System.Collections.ObjectModel;
using Rivune.Windows;

namespace Rivune.App.ViewModels;

public enum FeatureLoadState { Idle, Loading, Ready, Empty, Offline, Conflict, Error }

public sealed class ProtocolV22WorkspaceViewModel
{
    public ObservableCollection<ReadingQueueItem> Queue { get; } = [];
    public ObservableCollection<SavedSearch> SavedSearches { get; } = [];
    public ObservableCollection<SmartCollection> SmartCollections { get; } = [];
    public ObservableCollection<MediaNotification> Notifications { get; } = [];
    public ObservableCollection<AddonIncident> Incidents { get; } = [];
    public IReadOnlyList<MediaNotificationSubscription> NotificationSubscriptions { get; private set; } = [];
    public AccessibilityPreferencesDocument? Accessibility { get; private set; }
    public long QueueRevision { get; private set; }
    public FeatureLoadState State { get; private set; }
    public string? Failure { get; private set; }

    public async Task LoadAsync(RivuneApiClient client, Guid profileId, CancellationToken cancellationToken, bool includeIncidents = true)
    {
        State = FeatureLoadState.Loading;
        Failure = null;
        try
        {
            var queueTask = client.GetReadingQueueAsync(profileId, cancellationToken);
            var savedTask = client.GetSavedSearchesAsync(cancellationToken);
            var smartTask = client.GetSmartCollectionsAsync(cancellationToken);
            var notificationTask = client.GetMediaNotificationsAsync(cancellationToken: cancellationToken);
            var subscriptionTask = client.GetMediaNotificationSubscriptionsAsync(cancellationToken);
            var incidentsTask = includeIncidents
                ? client.GetAddonIncidentsAsync(cancellationToken)
                : Task.FromResult<IReadOnlyList<AddonIncident>>([]);
            var accessibilityTask = client.GetProfileAccessibilityPreferencesAsync(profileId, cancellationToken);
            await Task.WhenAll(queueTask, savedTask, smartTask, notificationTask, subscriptionTask, incidentsTask, accessibilityTask).ConfigureAwait(false);

            Replace(Queue, queueTask.Result.Items);
            QueueRevision = queueTask.Result.Revision;
            Replace(SavedSearches, savedTask.Result);
            Replace(SmartCollections, smartTask.Result);
            Replace(Notifications, notificationTask.Result.Notifications);
            NotificationSubscriptions = subscriptionTask.Result;
            Replace(Incidents, incidentsTask.Result.Take(500));
            Accessibility = accessibilityTask.Result;
            State = Queue.Count + SavedSearches.Count + SmartCollections.Count + Notifications.Count + Incidents.Count == 0
                ? FeatureLoadState.Empty
                : FeatureLoadState.Ready;
        }
        catch (OperationCanceledException) { throw; }
        catch (RivuneServerException exception) when (exception.StatusCode == 409)
        {
            State = FeatureLoadState.Conflict;
            Failure = "The profile changed elsewhere. Reload before trying again.";
        }
        catch (HttpRequestException)
        {
            State = FeatureLoadState.Offline;
            Failure = "Rivune is offline. Reconnect to synchronize these profile features.";
        }
        catch (Exception)
        {
            State = FeatureLoadState.Error;
            Failure = "These profile features could not be loaded.";
        }
    }

    public void ApplyQueue(ReadingQueue queue)
    {
        Replace(Queue, queue.Items);
        QueueRevision = queue.Revision;
        Failure = null;
        State = Queue.Count == 0 ? FeatureLoadState.Empty : FeatureLoadState.Ready;
    }

    public void ApplyAccessibility(AccessibilityPreferencesDocument preferences)
    {
        Accessibility = preferences;
        Failure = null;
        State = FeatureLoadState.Ready;
    }

    public void MarkConflict(string message)
    {
        State = FeatureLoadState.Conflict;
        Failure = message;
    }

    private static void Replace<T>(Collection<T> destination, IEnumerable<T> source)
    {
        destination.Clear();
        foreach (var item in source) destination.Add(item);
    }
}

public sealed class ReadingQueueOperation
{
    public ReadingQueueOperation(long expectedRevision) : this(Guid.NewGuid(), expectedRevision) { }
    internal ReadingQueueOperation(Guid operationId, long expectedRevision)
    {
        OperationId = operationId;
        ExpectedRevision = expectedRevision;
    }
    public Guid OperationId { get; }
    public long ExpectedRevision { get; }
}

public sealed class ReadingQueueController
{
    public ReadingQueueOperation Begin(long expectedRevision) => new(expectedRevision);

    public Task<ReadingQueueMutation> AddAsync(RivuneApiClient client, Guid profileId, ReadingQueueOperation operation, QueueMediaType mediaType, string resourceId, string title, Guid? sourceAddonId, Guid? titleId, string? posterUrl, CancellationToken cancellationToken) =>
        client.AddReadingQueueItemAsync(profileId, new ReadingQueueAddInput(operation.OperationId, operation.ExpectedRevision, mediaType, resourceId, title, sourceAddonId, titleId, posterUrl), cancellationToken);

    public Task<ReadingQueueMutation> RemoveAsync(RivuneApiClient client, Guid profileId, Guid itemId, ReadingQueueOperation operation, CancellationToken cancellationToken) =>
        client.RemoveReadingQueueItemAsync(profileId, itemId, new ReadingQueueMutationInput(operation.OperationId, operation.ExpectedRevision), cancellationToken);

    public Task<ReadingQueueMutation> ConsumeAsync(RivuneApiClient client, Guid profileId, Guid itemId, ReadingQueueOperation operation, CancellationToken cancellationToken) =>
        client.ConsumeReadingQueueItemAsync(profileId, itemId, new ReadingQueueMutationInput(operation.OperationId, operation.ExpectedRevision), cancellationToken);

    public Task<ReadingQueueMutation> ReorderAsync(RivuneApiClient client, Guid profileId, ReadingQueueOperation operation, IReadOnlyList<Guid> itemIds, CancellationToken cancellationToken) =>
        client.ReorderReadingQueueAsync(profileId, new ReadingQueueReorderInput(operation.OperationId, operation.ExpectedRevision, itemIds), cancellationToken);
}

public readonly record struct PlaybackFailoverLogEntry(DateTimeOffset At, PlaybackFailoverError Error, PlaybackFailoverStatus Status, int AttemptCount, int MaximumAttempts);

public sealed class PlaybackFailoverJournal
{
    private readonly Queue<PlaybackFailoverLogEntry> _entries;
    public PlaybackFailoverJournal(int capacity = 32)
    {
        if (capacity is < 1 or > 256) throw new ArgumentOutOfRangeException(nameof(capacity));
        Capacity = capacity;
        _entries = new Queue<PlaybackFailoverLogEntry>(capacity);
    }
    public int Capacity { get; }
    public IReadOnlyList<PlaybackFailoverLogEntry> Entries => _entries.ToArray();
    public void Record(PlaybackFailoverError error, PlaybackFailoverState state)
    {
        if (_entries.Count == Capacity) _entries.Dequeue();
        _entries.Enqueue(new PlaybackFailoverLogEntry(DateTimeOffset.UtcNow, error, state.Status, state.AttemptCount, state.MaximumAttempts));
    }
}

public sealed class PlaybackFailoverController : IDisposable
{
    private CancellationTokenSource? _operation;
    private PlaybackFailoverState? _state;
    public PlaybackFailoverController(PlaybackFailoverJournal? journal = null) => Journal = journal ?? new PlaybackFailoverJournal();
    public PlaybackFailoverJournal Journal { get; }
    public PlaybackFailoverState? State => _state;
    public bool IsTransitioning => _operation is not null;

    public static bool CanAdvance(PlaybackFailoverError error) => error is
        PlaybackFailoverError.SourceFailed or PlaybackFailoverError.SourceTimeout or PlaybackFailoverError.EndedEarly;

    public async Task<PlaybackFailoverState> StartAsync(RivuneApiClient client, IReadOnlyList<string> candidates, string selected, int maximumAttempts, CancellationToken cancellationToken)
    {
        if (candidates.Count is < 2 or > 8 || candidates.Distinct(StringComparer.Ordinal).Count() != candidates.Count)
            throw new ArgumentException("Failover requires two to eight unique candidates.", nameof(candidates));
        if (maximumAttempts is < 1 or > 3) throw new ArgumentOutOfRangeException(nameof(maximumAttempts));
        CancelLocalOperation();
        _operation = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        try
        {
            _state = await client.CreatePlaybackFailoverAsync(new PlaybackFailoverCreateInput(candidates, selected, maximumAttempts), _operation.Token).ConfigureAwait(false);
            return _state;
        }
        finally { FinishOperation(); }
    }

    public async Task<PlaybackFailoverState?> AdvanceAsync(RivuneApiClient client, PlaybackFailoverError error, double positionSeconds, CancellationToken cancellationToken)
    {
        if (_state is null || _state.Status != PlaybackFailoverStatus.Active || !CanAdvance(error)) return null;
        if (!double.IsFinite(positionSeconds)) throw new ArgumentOutOfRangeException(nameof(positionSeconds));
        CancelLocalOperation();
        _operation = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        try
        {
            var next = await client.AdvancePlaybackFailoverAsync(_state.Id, new PlaybackFailoverAdvanceInput(error, Math.Clamp(positionSeconds, 0, 86400), _state.Revision), _operation.Token).ConfigureAwait(false);
            _state = next;
            Journal.Record(error, next);
            return next;
        }
        finally { FinishOperation(); }
    }

    public async Task CancelAsync(RivuneApiClient client, CancellationToken cancellationToken)
    {
        var state = _state;
        CancelLocalOperation();
        if (state is not null && state.Status == PlaybackFailoverStatus.Active)
            await client.CancelPlaybackFailoverAsync(state.Id, cancellationToken).ConfigureAwait(false);
        _state = null;
    }

    public void Restore(PlaybackFailoverState state) => _state = state;
    public void Dispose() { CancelLocalOperation(); GC.SuppressFinalize(this); }
    private void CancelLocalOperation() { _operation?.Cancel(); _operation?.Dispose(); _operation = null; }
    private void FinishOperation() { _operation?.Dispose(); _operation = null; }
}

public interface ISystemAccessibilitySettings
{
    bool ReducedMotion { get; }
    bool HighContrast { get; }
    bool CaptionsEnabled { get; }
}

public readonly record struct EffectiveAccessibilitySettings(bool ReducedMotion, bool HighContrast, double TextScale, bool CaptionsEnabled, bool AudioDescription, bool EnhancedFocusIndicators);

public static class AccessibilityPreferencesPolicy
{
    public static EffectiveAccessibilitySettings Resolve(AccessibilityPreferencesDocument preferences, ISystemAccessibilitySettings system) => new(
        preferences.ReducedMotion switch { ReducedMotionPreference.System => system.ReducedMotion, ReducedMotionPreference.Reduce => true, ReducedMotionPreference.NoPreference => false, _ => system.ReducedMotion },
        preferences.HighContrast switch { HighContrastPreference.System => system.HighContrast, HighContrastPreference.More => true, HighContrastPreference.Standard => false, _ => system.HighContrast },
        preferences.TextScale / 100d,
        preferences.Captions switch { CaptionsPreference.System => system.CaptionsEnabled, CaptionsPreference.On => true, CaptionsPreference.Off => false, _ => system.CaptionsEnabled },
        preferences.AudioDescription,
        preferences.FocusIndicators == FocusIndicatorsPreference.Enhanced);
}

public static class SafeIncidentPresentation
{
    public static string Label(AddonIncident incident)
    {
        var addon = SafeText(incident.AddonName);
        var code = incident.Code switch { AddonIncidentCode.Timeout => "timed out", AddonIncidentCode.Unavailable => "unavailable", AddonIncidentCode.InvalidResponse => "returned an invalid response", _ => "is unhealthy" };
        return $"{addon}: {code} ({incident.State.ToString().ToLowerInvariant()})";
    }

    private static string SafeText(string value)
    {
        var trimmed = value.Trim();
        if (trimmed.Contains("://", StringComparison.Ordinal) || trimmed.Contains("token", StringComparison.OrdinalIgnoreCase) || trimmed.Contains("authorization", StringComparison.OrdinalIgnoreCase))
            return "Add-on";
        return trimmed.Length switch { 0 => "Add-on", > 80 => trimmed[..80], _ => trimmed };
    }
}

public readonly record struct AccessibilityVisualPolicy(
    double TextScale,
    double FocusPrimaryThickness,
    double FocusSecondaryThickness)
{
    public static AccessibilityVisualPolicy From(EffectiveAccessibilitySettings settings) => new(
        settings.TextScale,
        settings.EnhancedFocusIndicators || settings.HighContrast ? 3 : 2,
        settings.EnhancedFocusIndicators || settings.HighContrast ? 1 : 0);

    public double ScaleFont(double unscaledFontSize)
    {
        if (!double.IsFinite(unscaledFontSize) || unscaledFontSize < 0)
            throw new ArgumentOutOfRangeException(nameof(unscaledFontSize));
        return unscaledFontSize * TextScale;
    }
}
