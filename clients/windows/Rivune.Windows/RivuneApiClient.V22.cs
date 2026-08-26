using System.Globalization;

namespace Rivune.Windows;

public sealed partial class RivuneApiClient
{
    public Task<ReadingQueue> GetReadingQueueAsync(Guid profileId, CancellationToken cancellationToken = default) =>
        RequestJsonAsync<ReadingQueue>(HttpMethod.Get, ["profiles", profileId.ToString("D"), "queue"], null, null, true, cancellationToken);

    public Task<ReadingQueueMutation> AddReadingQueueItemAsync(Guid profileId, ReadingQueueAddInput input, CancellationToken cancellationToken = default) =>
        RequestJsonAsync<ReadingQueueMutation>(HttpMethod.Post, ["profiles", profileId.ToString("D"), "queue", "items"], null, input, true, cancellationToken);

    public Task<ReadingQueueMutation> ReorderReadingQueueAsync(Guid profileId, ReadingQueueReorderInput input, CancellationToken cancellationToken = default) =>
        RequestJsonAsync<ReadingQueueMutation>(HttpMethod.Put, ["profiles", profileId.ToString("D"), "queue", "order"], null, input, true, cancellationToken);

    public Task<ReadingQueueMutation> UpdateReadingQueueItemAsync(Guid profileId, Guid itemId, ReadingQueueUpdateInput input, CancellationToken cancellationToken = default) =>
        RequestJsonAsync<ReadingQueueMutation>(HttpMethod.Patch, ["profiles", profileId.ToString("D"), "queue", "items", itemId.ToString("D")], null, input, true, cancellationToken);

    public Task<ReadingQueueMutation> RemoveReadingQueueItemAsync(Guid profileId, Guid itemId, ReadingQueueMutationInput input, CancellationToken cancellationToken = default) =>
        RequestJsonAsync<ReadingQueueMutation>(HttpMethod.Delete, ["profiles", profileId.ToString("D"), "queue", "items", itemId.ToString("D")], null, input, true, cancellationToken);

    public Task<ReadingQueueMutation> ConsumeReadingQueueItemAsync(Guid profileId, Guid itemId, ReadingQueueMutationInput input, CancellationToken cancellationToken = default) =>
        RequestJsonAsync<ReadingQueueMutation>(HttpMethod.Post, ["profiles", profileId.ToString("D"), "queue", "items", itemId.ToString("D"), "consume"], null, input, true, cancellationToken);

    public async Task<IReadOnlyList<SavedSearch>> GetSavedSearchesAsync(CancellationToken cancellationToken = default) =>
        (await RequestJsonAsync<SavedSearchList>(HttpMethod.Get, ["saved-searches"], null, null, true, cancellationToken).ConfigureAwait(false)).SavedSearches;

    public Task<SavedSearch> CreateSavedSearchAsync(SavedSearchInput input, CancellationToken cancellationToken = default) =>
        RequestJsonAsync<SavedSearch>(HttpMethod.Post, ["saved-searches"], null, input, true, cancellationToken);

    public Task<SavedSearch> UpdateSavedSearchAsync(Guid id, SavedSearchUpdateInput input, CancellationToken cancellationToken = default) =>
        RequestJsonAsync<SavedSearch>(HttpMethod.Put, ["saved-searches", id.ToString("D")], null, input, true, cancellationToken);

    public Task DeleteSavedSearchAsync(Guid id, long expectedRevision, CancellationToken cancellationToken = default) =>
        RequestEmptyAsync(HttpMethod.Delete, ["saved-searches", id.ToString("D")], Query(("expectedRevision", expectedRevision.ToString(CultureInfo.InvariantCulture))), true, cancellationToken);

    public async Task<IReadOnlyList<SmartCollection>> GetSmartCollectionsAsync(CancellationToken cancellationToken = default) =>
        (await RequestJsonAsync<SmartCollectionList>(HttpMethod.Get, ["smart-collections"], null, null, true, cancellationToken).ConfigureAwait(false)).SmartCollections;

    public Task<SmartCollection> CreateSmartCollectionAsync(SmartCollectionInput input, CancellationToken cancellationToken = default) =>
        RequestJsonAsync<SmartCollection>(HttpMethod.Post, ["smart-collections"], null, input, true, cancellationToken);

    public Task<SmartCollection> UpdateSmartCollectionAsync(Guid id, SmartCollectionUpdateInput input, CancellationToken cancellationToken = default) =>
        RequestJsonAsync<SmartCollection>(HttpMethod.Put, ["smart-collections", id.ToString("D")], null, input, true, cancellationToken);

    public Task DeleteSmartCollectionAsync(Guid id, long expectedRevision, CancellationToken cancellationToken = default) =>
        RequestEmptyAsync(HttpMethod.Delete, ["smart-collections", id.ToString("D")], Query(("expectedRevision", expectedRevision.ToString(CultureInfo.InvariantCulture))), true, cancellationToken);

    public Task<SmartCollectionPage> EvaluateSmartCollectionAsync(Guid id, int page = 1, int pageSize = 30, CancellationToken cancellationToken = default) =>
        RequestJsonAsync<SmartCollectionPage>(HttpMethod.Get, ["smart-collections", id.ToString("D"), "items"], Query(("page", page.ToString(CultureInfo.InvariantCulture)), ("pageSize", pageSize.ToString(CultureInfo.InvariantCulture))), null, true, cancellationToken);

    public async Task<IReadOnlyList<AddonIncident>> GetAddonIncidentsAsync(CancellationToken cancellationToken = default) =>
        (await RequestJsonAsync<AddonIncidentList>(HttpMethod.Get, ["operations", "extension-incidents"], null, null, true, cancellationToken).ConfigureAwait(false)).Incidents;

    public Task<AddonIncidentDetail> GetAddonIncidentAsync(Guid id, CancellationToken cancellationToken = default) =>
        RequestJsonAsync<AddonIncidentDetail>(HttpMethod.Get, ["operations", "extension-incidents", id.ToString("D")], null, null, true, cancellationToken);

    public Task<AddonIncident> AcknowledgeAddonIncidentAsync(Guid id, CancellationToken cancellationToken = default) =>
        RequestJsonAsync<AddonIncident>(HttpMethod.Post, ["operations", "extension-incidents", id.ToString("D"), "acknowledgement"], null, null, true, cancellationToken);

    public async Task<IReadOnlyList<MediaNotificationSubscription>> GetMediaNotificationSubscriptionsAsync(CancellationToken cancellationToken = default) =>
        (await RequestJsonAsync<MediaNotificationSubscriptions>(HttpMethod.Get, ["media-notification-subscriptions"], null, null, true, cancellationToken).ConfigureAwait(false)).Subscriptions;

    public Task<MediaNotificationSubscription> FollowMediaNotificationsAsync(Guid titleId, MediaNotificationFollowInput input, CancellationToken cancellationToken = default) =>
        RequestJsonAsync<MediaNotificationSubscription>(HttpMethod.Put, ["media-notification-subscriptions", titleId.ToString("D")], null, input, true, cancellationToken);

    public Task UnfollowMediaNotificationsAsync(Guid titleId, CancellationToken cancellationToken = default) =>
        RequestEmptyAsync(HttpMethod.Delete, ["media-notification-subscriptions", titleId.ToString("D")], true, cancellationToken);

    public Task<MediaNotificationPage> GetMediaNotificationsAsync(string? cursor = null, int limit = 30, CancellationToken cancellationToken = default)
    {
        if (cursor is not null && (cursor.Length is < 1 or > 19 || cursor[0] == '0' || cursor.Any(character => character is < '0' or > '9')))
            throw new ArgumentException("Cursor must be a positive decimal int64 identifier.", nameof(cursor));
        if (limit is < 1 or > 100) throw new ArgumentOutOfRangeException(nameof(limit));
        return RequestJsonAsync<MediaNotificationPage>(HttpMethod.Get, ["media-notifications"], Query(("cursor", cursor), ("limit", limit.ToString(CultureInfo.InvariantCulture))), null, true, cancellationToken);
    }

    public Task AcknowledgeMediaNotificationAsync(string notificationId, MediaNotificationAcknowledgementState state, CancellationToken cancellationToken = default)
    {
        if (notificationId.Length is < 1 or > 19 || notificationId[0] == '0' || notificationId.Any(character => character is < '0' or > '9'))
            throw new ArgumentException("Notification identifier must be a positive decimal int64 identifier.", nameof(notificationId));
        return RequestEmptyWithBodyAsync(HttpMethod.Post, ["media-notifications", notificationId, "acknowledgement"], new MediaNotificationAcknowledgement(state), true, cancellationToken);
    }

    public Task<PlaybackFailoverState> CreatePlaybackFailoverAsync(PlaybackFailoverCreateInput input, CancellationToken cancellationToken = default) =>
        RequestJsonAsync<PlaybackFailoverState>(HttpMethod.Post, ["playback", "failovers"], null, input, true, cancellationToken);

    public Task<PlaybackFailoverState> GetPlaybackFailoverAsync(Guid id, CancellationToken cancellationToken = default) =>
        RequestJsonAsync<PlaybackFailoverState>(HttpMethod.Get, ["playback", "failovers", id.ToString("D")], null, null, true, cancellationToken);

    public Task CancelPlaybackFailoverAsync(Guid id, CancellationToken cancellationToken = default) =>
        RequestEmptyAsync(HttpMethod.Delete, ["playback", "failovers", id.ToString("D")], true, cancellationToken);

    public Task<PlaybackFailoverState> AdvancePlaybackFailoverAsync(Guid id, PlaybackFailoverAdvanceInput input, CancellationToken cancellationToken = default) =>
        RequestJsonAsync<PlaybackFailoverState>(HttpMethod.Post, ["playback", "failovers", id.ToString("D"), "advance"], null, input, true, cancellationToken);

    public Task<AccessibilityPreferencesDocument> GetProfileAccessibilityPreferencesAsync(Guid profileId, CancellationToken cancellationToken = default) =>
        RequestJsonAsync<AccessibilityPreferencesDocument>(HttpMethod.Get, ["profiles", profileId.ToString("D"), "accessibility-preferences"], null, null, true, cancellationToken);

    public Task<AccessibilityPreferencesDocument> UpdateProfileAccessibilityPreferencesAsync(Guid profileId, AccessibilityPreferencesDocument preferences, CancellationToken cancellationToken = default) =>
        RequestJsonAsync<AccessibilityPreferencesDocument>(HttpMethod.Put, ["profiles", profileId.ToString("D"), "accessibility-preferences"], null, preferences, true, cancellationToken);
}
