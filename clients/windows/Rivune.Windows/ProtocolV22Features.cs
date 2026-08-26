using System.Text.Json;
using System.Text.Json.Serialization;

namespace Rivune.Windows;

[JsonConverter(typeof(JsonStringEnumConverter<QueueMediaType>))]
public enum QueueMediaType
{
    [JsonStringEnumMemberName("movie")] Movie,
    [JsonStringEnumMemberName("series")] Series,
    [JsonStringEnumMemberName("episode")] Episode,
    [JsonStringEnumMemberName("tv")] Tv,
}

public sealed record ReadingQueue(long Revision, IReadOnlyList<ReadingQueueItem> Items);
public sealed record ReadingQueueItem(Guid Id, QueueMediaType MediaType, string ResourceId, Guid? SourceAddonId, Guid? TitleId, string Title, string? PosterUrl, int Position, string CreatedAt, string UpdatedAt);
public sealed record ReadingQueueMutation(long Revision, Guid? AffectedItemId, bool? Duplicate);
public sealed record ReadingQueueMutationInput(Guid OperationId, long ExpectedRevision);
public sealed record ReadingQueueAddInput(Guid OperationId, long ExpectedRevision, QueueMediaType MediaType, string ResourceId, string Title, Guid? SourceAddonId = null, Guid? TitleId = null, string? PosterUrl = null);
public sealed record ReadingQueueUpdateInput(Guid OperationId, long ExpectedRevision, string Title, string? PosterUrl = null);
public sealed record ReadingQueueReorderInput(Guid OperationId, long ExpectedRevision, IReadOnlyList<Guid> ItemIds);

[JsonConverter(typeof(JsonStringEnumConverter<SavedSearchSort>))]
public enum SavedSearchSort
{
    [JsonStringEnumMemberName("relevance")] Relevance,
    [JsonStringEnumMemberName("title")] Title,
    [JsonStringEnumMemberName("year")] Year,
    [JsonStringEnumMemberName("rating")] Rating,
    [JsonStringEnumMemberName("added")] Added,
}

[JsonConverter(typeof(JsonStringEnumConverter<SavedSearchMediaType>))]
public enum SavedSearchMediaType
{
    [JsonStringEnumMemberName("movie")] Movie,
    [JsonStringEnumMemberName("series")] Series,
    [JsonStringEnumMemberName("season")] Season,
    [JsonStringEnumMemberName("episode")] Episode,
    [JsonStringEnumMemberName("video")] Video,
    [JsonStringEnumMemberName("tv")] Tv,
}

public sealed record SavedSearch(Guid Id, string Name, string Query, SavedSearchMediaType? MediaType, SavedSearchSort Sort, long Revision, string CreatedAt, string UpdatedAt);
public sealed record SavedSearchInput(string Name, string Query, SavedSearchSort Sort, SavedSearchMediaType? MediaType = null);
public sealed record SavedSearchUpdateInput(string Name, string Query, SavedSearchSort Sort, long ExpectedRevision, SavedSearchMediaType? MediaType = null);
public sealed record SavedSearchList(IReadOnlyList<SavedSearch> SavedSearches);

[JsonPolymorphic(TypeDiscriminatorPropertyName = "type")]
[JsonDerivedType(typeof(SmartAllRule), "all")]
[JsonDerivedType(typeof(SmartAnyRule), "any")]
[JsonDerivedType(typeof(SmartMediaTypeRule), "media_type")]
[JsonDerivedType(typeof(SmartYearRule), "year")]
[JsonDerivedType(typeof(SmartRatingRule), "rating")]
[JsonDerivedType(typeof(SmartGenreRule), "genre")]
[JsonDerivedType(typeof(SmartStatusRule), "status")]
[JsonDerivedType(typeof(SmartSourceRule), "source")]
public abstract record SmartRule;
[JsonConverter(typeof(JsonStringEnumConverter<SmartMediaTypeOperator>))]
public enum SmartMediaTypeOperator { [JsonStringEnumMemberName("one_of")] OneOf }
[JsonConverter(typeof(JsonStringEnumConverter<SmartNumericOperator>))]
public enum SmartNumericOperator
{
    [JsonStringEnumMemberName("equals")] Equals,
    [JsonStringEnumMemberName("gte")] GreaterThanOrEqual,
    [JsonStringEnumMemberName("lte")] LessThanOrEqual,
}
[JsonConverter(typeof(JsonStringEnumConverter<SmartTextOperator>))]
public enum SmartTextOperator
{
    [JsonStringEnumMemberName("equals")] Equals,
    [JsonStringEnumMemberName("not_equals")] NotEquals,
}
public sealed record SmartAllRule(IReadOnlyList<SmartRule> Rules) : SmartRule;
public sealed record SmartAnyRule(IReadOnlyList<SmartRule> Rules) : SmartRule;
public sealed record SmartMediaTypeRule(SmartMediaTypeOperator Operator, IReadOnlyList<SavedSearchMediaType> Values) : SmartRule;
public sealed record SmartYearRule(SmartNumericOperator Operator, decimal Number) : SmartRule;
public sealed record SmartRatingRule(SmartNumericOperator Operator, decimal Number) : SmartRule;
public sealed record SmartGenreRule(SmartTextOperator Operator, string Value) : SmartRule;
public sealed record SmartStatusRule(SmartTextOperator Operator, string Value) : SmartRule;
public sealed record SmartSourceRule(SmartTextOperator Operator, string Value) : SmartRule;
[JsonConverter(typeof(JsonStringEnumConverter<SmartCollectionSort>))]
public enum SmartCollectionSort
{
    [JsonStringEnumMemberName("title")] Title,
    [JsonStringEnumMemberName("year")] Year,
    [JsonStringEnumMemberName("rating")] Rating,
    [JsonStringEnumMemberName("added")] Added,
}

public sealed record SmartCollection(Guid Id, string Name, SmartRule Rules, SmartCollectionSort Sort, long Revision, string CreatedAt, string UpdatedAt);
public sealed record SmartCollectionInput(string Name, SmartRule Rules, SmartCollectionSort Sort);
public sealed record SmartCollectionUpdateInput(string Name, SmartRule Rules, SmartCollectionSort Sort, long ExpectedRevision);
public sealed record SmartCollectionCatalogItem(Guid Id, SavedSearchMediaType MediaType, string Title, IReadOnlyList<string> Genres, string? PosterUrl, string? BackgroundUrl, string? ReleaseInfo, string? Released, decimal? CommunityRating, string? Status, string? ResourceId, string? ResourceProvider, Guid? SourceAddonId, string? SourceCatalogId, string? SourceName);
public sealed record SmartCollectionList(IReadOnlyList<SmartCollection> SmartCollections);
public sealed record SmartCollectionPage(IReadOnlyList<SmartCollectionCatalogItem> Items, int Page, int PageSize, int Total, int TotalPages);

[JsonConverter(typeof(JsonStringEnumConverter<AddonIncidentCode>))]
public enum AddonIncidentCode
{
    [JsonStringEnumMemberName("timeout")] Timeout,
    [JsonStringEnumMemberName("unavailable")] Unavailable,
    [JsonStringEnumMemberName("invalid_response")] InvalidResponse,
    [JsonStringEnumMemberName("unhealthy")] Unhealthy,
}
[JsonConverter(typeof(JsonStringEnumConverter<AddonIncidentState>))]
public enum AddonIncidentState
{
    [JsonStringEnumMemberName("open")] Open,
    [JsonStringEnumMemberName("recovering")] Recovering,
    [JsonStringEnumMemberName("resolved")] Resolved,
}
[JsonConverter(typeof(JsonStringEnumConverter<AddonIncidentImpact>))]
public enum AddonIncidentImpact
{
    [JsonStringEnumMemberName("availability")] Availability,
    [JsonStringEnumMemberName("response_integrity")] ResponseIntegrity,
}
[JsonConverter(typeof(JsonStringEnumConverter<AddonIncidentEventType>))]
public enum AddonIncidentEventType
{
    [JsonStringEnumMemberName("opened")] Opened,
    [JsonStringEnumMemberName("occurred")] Occurred,
    [JsonStringEnumMemberName("recovering")] Recovering,
    [JsonStringEnumMemberName("resolved")] Resolved,
    [JsonStringEnumMemberName("acknowledged")] Acknowledged,
}
public sealed record AddonIncident(Guid Id, Guid ProfileId, Guid AddonId, string AddonName, AddonIncidentCode Code, AddonIncidentState State, AddonIncidentImpact Impact, int OccurrenceCount, string FirstOccurredAt, string LastOccurredAt, string? LastSuccessAt, string? RecoveryStartedAt, string? ResolvedAt, string? AcknowledgedAt, Guid? AcknowledgedByUserId, string UpdatedAt);
public sealed record AddonIncidentList(IReadOnlyList<AddonIncident> Incidents);
public sealed record AddonIncidentEvent(long Id, AddonIncidentEventType Type, AddonIncidentCode Code, string OccurredAt);
public sealed record AddonIncidentDetail(AddonIncident Incident, IReadOnlyList<AddonIncidentEvent> Events);

[JsonConverter(typeof(JsonStringEnumConverter<MediaNotificationKind>))]
public enum MediaNotificationKind
{
    [JsonStringEnumMemberName("calendar-event-upcoming")] CalendarEventUpcoming,
    [JsonStringEnumMemberName("season-available")] SeasonAvailable,
    [JsonStringEnumMemberName("episode-available")] EpisodeAvailable,
    [JsonStringEnumMemberName("movie-release")] MovieRelease,
}
[JsonConverter(typeof(JsonStringEnumConverter<MediaNotificationAcknowledgementState>))]
public enum MediaNotificationAcknowledgementState
{
    [JsonStringEnumMemberName("read")] Read,
    [JsonStringEnumMemberName("dismissed")] Dismissed,
}
public sealed record MediaNotificationFollowInput(string Timezone, int HorizonDays, int LeadDays);
public sealed record MediaNotificationSubscription(Guid TitleId, string Timezone, int HorizonDays, int LeadDays, string CreatedAt, string UpdatedAt);
public sealed record MediaNotificationSubscriptions(IReadOnlyList<MediaNotificationSubscription> Subscriptions);
public sealed record MediaNotification(string Id, MediaNotificationKind Kind, Guid TitleId, Guid? SubjectTitleId, string Title, string? SeriesTitle, string? ReleaseDate, int? SeasonNumber, int? EpisodeNumber, string AvailableAt, string? ReadAt, string CreatedAt);
public sealed record MediaNotificationPage(IReadOnlyList<MediaNotification> Notifications, string? NextCursor);
public sealed record MediaNotificationAcknowledgement(MediaNotificationAcknowledgementState State);

[JsonConverter(typeof(JsonStringEnumConverter<PlaybackFailoverError>))]
public enum PlaybackFailoverError
{
    [JsonStringEnumMemberName("source_failed")] SourceFailed,
    [JsonStringEnumMemberName("source_timeout")] SourceTimeout,
    [JsonStringEnumMemberName("ended_early")] EndedEarly,
    [JsonStringEnumMemberName("decode_failed")] DecodeFailed,
    [JsonStringEnumMemberName("access_denied")] AccessDenied,
    [JsonStringEnumMemberName("user_cancelled")] UserCancelled,
}
[JsonConverter(typeof(JsonStringEnumConverter<PlaybackFailoverStatus>))]
public enum PlaybackFailoverStatus
{
    [JsonStringEnumMemberName("active")] Active,
    [JsonStringEnumMemberName("exhausted")] Exhausted,
    [JsonStringEnumMemberName("cancelled")] Cancelled,
}
[JsonConverter(typeof(JsonStringEnumConverter<PlaybackFailoverCandidateStatus>))]
public enum PlaybackFailoverCandidateStatus
{
    [JsonStringEnumMemberName("current")] Current,
    [JsonStringEnumMemberName("available")] Available,
    [JsonStringEnumMemberName("cooling_down")] CoolingDown,
}
public sealed record PlaybackFailoverCreateInput(IReadOnlyList<string> CandidateSourceRefs, string SelectedSourceRef, int? MaximumAttempts = null);
public sealed record PlaybackFailoverAdvanceInput(PlaybackFailoverError Error, double PositionSeconds, long ExpectedRevision);
public sealed record PlaybackFailoverCandidateHealth(int Position, PlaybackFailoverCandidateStatus Status, string? CooldownUntil);
public sealed record PlaybackFailoverState(Guid Id, string? CurrentSourceRef, int CurrentPosition, double PositionSeconds, int AttemptCount, int MaximumAttempts, long Revision, PlaybackFailoverStatus Status, PlaybackFailoverError? LastError, string? Explanation, IReadOnlyList<PlaybackFailoverCandidateHealth> CandidateHealth, string ExpiresAt);

[JsonConverter(typeof(JsonStringEnumConverter<ReducedMotionPreference>))]
public enum ReducedMotionPreference
{
    [JsonStringEnumMemberName("system")] System,
    [JsonStringEnumMemberName("reduce")] Reduce,
    [JsonStringEnumMemberName("no-preference")] NoPreference,
}
[JsonConverter(typeof(JsonStringEnumConverter<HighContrastPreference>))]
public enum HighContrastPreference
{
    [JsonStringEnumMemberName("system")] System,
    [JsonStringEnumMemberName("more")] More,
    [JsonStringEnumMemberName("standard")] Standard,
}
[JsonConverter(typeof(JsonStringEnumConverter<CaptionsPreference>))]
public enum CaptionsPreference
{
    [JsonStringEnumMemberName("system")] System,
    [JsonStringEnumMemberName("on")] On,
    [JsonStringEnumMemberName("off")] Off,
}
[JsonConverter(typeof(JsonStringEnumConverter<FocusIndicatorsPreference>))]
public enum FocusIndicatorsPreference
{
    [JsonStringEnumMemberName("standard")] Standard,
    [JsonStringEnumMemberName("enhanced")] Enhanced,
}
public sealed record AccessibilityPreferencesDocument(long Revision, ReducedMotionPreference ReducedMotion, HighContrastPreference HighContrast, int TextScale, CaptionsPreference Captions, bool AudioDescription, FocusIndicatorsPreference FocusIndicators) : IJsonOnDeserialized
{
    void IJsonOnDeserialized.OnDeserialized()
    {
        if (Revision < 0 || TextScale is not (100 or 115 or 130))
            throw new JsonException("Invalid accessibility preference document.");
    }
}
