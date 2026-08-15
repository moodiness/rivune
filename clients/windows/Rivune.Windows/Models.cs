using System.Text.Json;
using System.Text.Json.Serialization;

namespace Rivune.Windows;

public static class RivuneProtocol
{
    public const int Version = 20;
}

public static class DiscoveryCapabilityIdentifiers
{
    public const string BoundedAggregateResources = "bounded-aggregate-resources";
    public const string ProfileArchivesV1 = "profile-archives-v1";
    public const string RequestCorrelation = "request-correlation";
}

public enum DiscoveryCapability
{
    BoundedAggregateResources,
    ProfileArchivesV1,
    RequestCorrelation,
}

internal sealed class DiscoveryCapabilitiesJsonConverter : JsonConverter<IReadOnlyList<string>>
{
    private const int MaximumCapabilities = 64;
    private const int MaximumCapabilityLength = 64;

    public DiscoveryCapabilitiesJsonConverter()
    {
    }

    public override bool HandleNull => true;

    public override IReadOnlyList<string> Read(
        ref Utf8JsonReader reader,
        Type typeToConvert,
        JsonSerializerOptions options)
    {
        if (reader.TokenType == JsonTokenType.Null)
        {
            return [];
        }
        if (reader.TokenType != JsonTokenType.StartArray)
        {
            reader.Skip();
            return [];
        }

        List<string>? normalized = null;
        HashSet<string>? seen = null;
        while (reader.Read() && reader.TokenType != JsonTokenType.EndArray)
        {
            if (reader.TokenType != JsonTokenType.String || normalized?.Count == MaximumCapabilities)
            {
                reader.Skip();
                continue;
            }

            var identifier = reader.GetString();
            if (identifier is null || !IsSafeIdentifier(identifier))
            {
                continue;
            }

            seen ??= new HashSet<string>(StringComparer.Ordinal);
            if (!seen.Add(identifier))
            {
                continue;
            }

            normalized ??= new List<string>(MaximumCapabilities);
            normalized.Add(identifier);
        }
        return normalized ?? [];
    }

    public override void Write(
        Utf8JsonWriter writer,
        IReadOnlyList<string> value,
        JsonSerializerOptions options)
    {
        writer.WriteStartArray();
        foreach (var identifier in value)
        {
            writer.WriteStringValue(identifier);
        }
        writer.WriteEndArray();
    }

    private static bool IsSafeIdentifier(string identifier)
    {
        if (identifier.Length is < 1 or > MaximumCapabilityLength)
        {
            return false;
        }

        var previousWasHyphen = true;
        foreach (var character in identifier)
        {
            if (character == '-')
            {
                if (previousWasHyphen)
                {
                    return false;
                }
                previousWasHyphen = true;
                continue;
            }
            if (character is not (>= 'a' and <= 'z') and not (>= '0' and <= '9'))
            {
                return false;
            }
            previousWasHyphen = false;
        }
        return !previousWasHyphen;
    }
}

public sealed record Discovery
{
    public required string Name { get; init; }
    public required string ServerVersion { get; init; }
    public required int ProtocolVersion { get; init; }
    public required string ApiBaseUrl { get; init; }
    public required bool SetupRequired { get; init; }
    public bool? SetupCompleted { get; init; }
    public bool? DemoAvailable { get; init; }
    public required string Timezone { get; init; }
    public required string InterfaceLanguage { get; init; }
    [JsonConverter(typeof(DiscoveryCapabilitiesJsonConverter))]
    public IReadOnlyList<string> Capabilities { get; init; } = [];

    public bool Supports(DiscoveryCapability capability)
    {
        var identifier = capability switch
        {
            DiscoveryCapability.BoundedAggregateResources => DiscoveryCapabilityIdentifiers.BoundedAggregateResources,
            DiscoveryCapability.ProfileArchivesV1 => DiscoveryCapabilityIdentifiers.ProfileArchivesV1,
            DiscoveryCapability.RequestCorrelation => DiscoveryCapabilityIdentifiers.RequestCorrelation,
            _ => null,
        };
        if (identifier is null || Capabilities is null)
        {
            return false;
        }

        foreach (var advertised in Capabilities)
        {
            if (StringComparer.Ordinal.Equals(advertised, identifier))
            {
                return true;
            }
        }
        return false;
    }

    public bool SupportsProfileArchivesV1 => Supports(DiscoveryCapability.ProfileArchivesV1);
}

[JsonConverter(typeof(JsonStringEnumConverter<AuthorizationScope>))]
public enum AuthorizationScope
{
    [JsonStringEnumMemberName("global_admin")]
    GlobalAdministrator,
    [JsonStringEnumMemberName("category")]
    Category,
}

public sealed record CategoryRef
{
    public required Guid Id { get; init; }
    public required string Name { get; init; }
    public required string? Color { get; init; }
    public required string? Icon { get; init; }
}

internal interface IAuthorizationContext : IJsonOnDeserialized
{
    AuthorizationScope AuthorizationScope { get; }
    CategoryRef? Category { get; }

    void IJsonOnDeserialized.OnDeserialized()
    {
        switch (AuthorizationScope)
        {
            case global::Rivune.Windows.AuthorizationScope.GlobalAdministrator when Category is null:
            case global::Rivune.Windows.AuthorizationScope.Category when Category is not null:
                return;
            case global::Rivune.Windows.AuthorizationScope.GlobalAdministrator:
                throw new JsonException("A global_admin authorization context cannot include a category.");
            case global::Rivune.Windows.AuthorizationScope.Category:
                throw new JsonException("A category authorization context requires a category.");
            default:
                throw new JsonException($"Unsupported authorization scope '{AuthorizationScope}'.");
        }
    }
}

public sealed record Category
{
    public required Guid Id { get; init; }
    public required string Name { get; init; }
    public required string? Description { get; init; }
    public required string? Color { get; init; }
    public required string? Icon { get; init; }
    public required int Position { get; init; }
    public required bool IsDefault { get; init; }
    public required long ProfileCount { get; init; }
    public required long DeviceCount { get; init; }
    public required string CreatedAt { get; init; }
    public required string UpdatedAt { get; init; }
}

public sealed record CategoryList
{
    public required IReadOnlyList<Category> Categories { get; init; }
}

public readonly record struct PatchField<T>
{
    private PatchField(bool isSpecified, T? value)
    {
        IsSpecified = isSpecified;
        Value = value;
    }

    public bool IsSpecified { get; }
    public T? Value { get; }
    public static PatchField<T> Omitted => default;
    public static PatchField<T> Null => new(true, default);
    public static PatchField<T> FromValue(T value) => new(true, value);
}

public sealed record CategoryCreateRequest
{
    public required string Name { get; init; }
    public string? Description { get; init; }
    public string? Color { get; init; }
    public string? Icon { get; init; }
}

public sealed record CategoryUpdateRequest
{
    public string? Name { get; init; }
    public PatchField<string> Description { get; init; }
    public PatchField<string> Color { get; init; }
    public PatchField<string> Icon { get; init; }
    public bool? IsDefault { get; init; }
}

public sealed record LoginDevice
{
    public Guid? Id { get; init; }
    public required string Name { get; init; }
    public required string Platform { get; init; }
}

public sealed record Device
{
    public required Guid Id { get; init; }
    public required string Name { get; init; }
    public required string Platform { get; init; }
    public required Guid CategoryId { get; init; }
    public required CategoryRef Category { get; init; }
    public required string? InternalNote { get; init; }
    public required string? ApprovedAt { get; init; }
    public required string? LastSeenAt { get; init; }
    public required string CreatedAt { get; init; }
    public required string UpdatedAt { get; init; }
}

public sealed record DeviceList
{
    public required IReadOnlyList<Device> Devices { get; init; }
}

public sealed record DeviceUpdateRequest
{
    public string? Name { get; init; }
    public Guid? CategoryId { get; init; }
    public PatchField<string> InternalNote { get; init; }
}

public sealed record TokenPair : IAuthorizationContext
{
    public required string TokenType { get; init; }
    public required string AccessToken { get; init; }
    public required string AccessTokenExpiresAt { get; init; }
    public required string RefreshToken { get; init; }
    public required string RefreshTokenExpiresAt { get; init; }
    public required Guid SessionId { get; init; }
    public required Guid DeviceId { get; init; }
    public required AuthorizationScope AuthorizationScope { get; init; }
    public required CategoryRef? Category { get; init; }
}

public sealed record Account
{
    public required AccountUser User { get; init; }
    public required AccountSession Session { get; init; }
    public required IReadOnlyList<Profile> Profiles { get; init; }
    public required MaintenanceSettings Maintenance { get; init; }
}

public sealed record MaintenanceSettings
{
    public required bool Enabled { get; init; }
    public required string? Message { get; init; }
}

public sealed record AccountUser
{
    public required Guid Id { get; init; }
    public required string Username { get; init; }
    public required string Role { get; init; }
}

public sealed record AccountSession : IAuthorizationContext
{
    public required Guid Id { get; init; }
    public required Guid DeviceId { get; init; }
    public required ActiveProfileGrant? ActiveProfile { get; init; }
    public required AuthorizationScope AuthorizationScope { get; init; }
    public required CategoryRef? Category { get; init; }
}

public sealed record ActiveProfileGrant
{
    public required Guid Id { get; init; }
    public required string ExpiresAt { get; init; }
}

public sealed record SessionList
{
    public required IReadOnlyList<Session> Sessions { get; init; }
}

public sealed record Session : IAuthorizationContext
{
    public required Guid Id { get; init; }
    public required Guid DeviceId { get; init; }
    public required string DeviceName { get; init; }
    public required string Platform { get; init; }
    public required string? IpAddress { get; init; }
    public required string CreatedAt { get; init; }
    public required string LastSeenAt { get; init; }
    public required bool Current { get; init; }
    public required AuthorizationScope AuthorizationScope { get; init; }
    public required CategoryRef? Category { get; init; }
}

public sealed record ProfileSessionList
{
    public required IReadOnlyList<ProfileSession> Sessions { get; init; }
}

public sealed record ProfileSession : IAuthorizationContext
{
    public required Guid Id { get; init; }
    public required Guid UserId { get; init; }
    public required string Username { get; init; }
    public required Guid DeviceId { get; init; }
    public required string DeviceName { get; init; }
    public required string Platform { get; init; }
    public required string? IpAddress { get; init; }
    public required string CreatedAt { get; init; }
    public required string LastSeenAt { get; init; }
    public required string ProfileGrantExpiresAt { get; init; }
    public required bool Current { get; init; }
    public required AuthorizationScope AuthorizationScope { get; init; }
    public required CategoryRef? Category { get; init; }
}

public sealed record ProfileList
{
    public required IReadOnlyList<Profile> Profiles { get; init; }
}

public sealed record Profile
{
    public required Guid Id { get; init; }
    public required string Name { get; init; }
    public required string? Description { get; init; }
    public required Guid CategoryId { get; init; }
    public required CategoryRef Category { get; init; }
    public required bool IsChild { get; init; }
    public required bool HasPin { get; init; }
    public required bool CanManage { get; init; }
    public required bool Enabled { get; init; }
    public required string? AvailableFrom { get; init; }
    public required string? AvailableUntil { get; init; }
    public required string? AccessStartTime { get; init; }
    public required string? AccessEndTime { get; init; }
    public required string AccessTimezone { get; init; }
    public required bool Accessible { get; init; }
    public required ProfileAvatar Avatar { get; init; }
}

public sealed record ProfileAvatar
{
    public required string Kind { get; init; }
    public string? PresetId { get; init; }
    public required string Url { get; init; }
}

public sealed record ProfileSelection
{
    public required Profile Profile { get; init; }
    public required string ExpiresAt { get; init; }
    public required string ProfileContext { get; init; }
}

public sealed record CategoryOrderRequest
{
    public required IReadOnlyList<Guid> CategoryIds { get; init; }
}

public sealed record ProfileCategoryMoveRequest
{
    public required IReadOnlyList<Guid> ProfileIds { get; init; }
    public required Guid CategoryId { get; init; }
}

public sealed record DeviceCategoryMoveRequest
{
    public required IReadOnlyList<Guid> DeviceIds { get; init; }
    public required Guid CategoryId { get; init; }
}

public sealed record DeviceAuthorizationRequest
{
    public required string DeviceName { get; init; }
    public required string Platform { get; init; }
}

public sealed record DeviceAuthorizationResponse
{
    public required string DeviceCode { get; init; }
    public required string UserCode { get; init; }
    public required string VerificationUri { get; init; }
    public required string VerificationUriComplete { get; init; }
    public required string ExpiresAt { get; init; }
    public required int IntervalSeconds { get; init; }
}

public sealed record DeviceCodeApprovalRequest
{
    public required string UserCode { get; init; }
    public required Guid CategoryId { get; init; }
    public string? DeviceName { get; init; }
    public string? InternalNote { get; init; }
}

public sealed record DeviceCodeTokenRequest
{
    public required string DeviceCode { get; init; }
}

public sealed record SettingsValues
{
    public bool? AllowTranscoding { get; init; }
    public string? Transcoding { get; init; }
    public int? MaximumCastMembers { get; init; }
}

public sealed record SettingsLayer
{
    public required int SchemaVersion { get; init; }
    public required SettingsValues Settings { get; init; }
    public string? UpdatedAt { get; init; }
}

public sealed record EffectiveSettingsSources
{
    public string? AllowTranscoding { get; init; }
    public string? Transcoding { get; init; }
    public string? MaximumCastMembers { get; init; }
}

public sealed record EffectiveSettings
{
    public required int SchemaVersion { get; init; }
    public required SettingsValues Settings { get; init; }
    public required EffectiveSettingsSources Sources { get; init; }
}

[JsonConverter(typeof(JsonStringEnumConverter<MediaType>))]
public enum MediaType
{
    [JsonStringEnumMemberName("movie")]
    Movie,
    [JsonStringEnumMemberName("series")]
    Series,
    [JsonStringEnumMemberName("season")]
    Season,
    [JsonStringEnumMemberName("episode")]
    Episode,
}

[JsonConverter(typeof(JsonStringEnumConverter<SeriesMappingProvider>))]
public enum SeriesMappingProvider
{
    [JsonStringEnumMemberName("tmdb")]
    Tmdb,
    [JsonStringEnumMemberName("tvdb")]
    Tvdb,
}

public sealed record Genre
{
    public required int Id { get; init; }
    public required string Name { get; init; }
}
public sealed record CastMember
{
    public required string Id { get; init; }
    public required string Name { get; init; }
    public string? Character { get; init; }
    public string? ProfileUrl { get; init; }
}


public sealed record Movie
{
    public required Guid Id { get; init; }
    public required MediaType MediaType { get; init; }
    public required string Title { get; init; }
    public required string OriginalTitle { get; init; }
    public required string OriginalLanguage { get; init; }
    public required string Overview { get; init; }
    public string? ReleaseDate { get; init; }
    public string? PosterUrl { get; init; }
    public string? BackdropUrl { get; init; }
    public string? LogoUrl { get; init; }
    public string? Tagline { get; init; }
    public int? RuntimeMinutes { get; init; }
    public required IReadOnlyList<Genre> Genres { get; init; }
    public required IReadOnlyList<CastMember> Cast { get; init; }
    public required double VoteAverage { get; init; }
    public required int VoteCount { get; init; }
    public required IReadOnlyDictionary<string, string> ExternalIds { get; init; }
}

public sealed record Series
{
    public required Guid Id { get; init; }
    public required MediaType MediaType { get; init; }
    public required string Name { get; init; }
    public required string OriginalName { get; init; }
    public required string OriginalLanguage { get; init; }
    public required string Overview { get; init; }
    public string? FirstAirDate { get; init; }
    public string? LastAirDate { get; init; }
    public string? PosterUrl { get; init; }
    public string? BackdropUrl { get; init; }
    public string? LogoUrl { get; init; }
    public string? Tagline { get; init; }
    public string? Status { get; init; }
    public int? NumberOfSeasons { get; init; }
    public int? NumberOfEpisodes { get; init; }
    public required IReadOnlyList<Genre> Genres { get; init; }
    public required IReadOnlyList<CastMember> Cast { get; init; }
    public required double VoteAverage { get; init; }
    public required int VoteCount { get; init; }
    public required IReadOnlyList<SeasonSummary> Seasons { get; init; }
    public required IReadOnlyList<SeriesAlias> Aliases { get; init; }
    public required IReadOnlyList<EpisodeOrder> EpisodeOrders { get; init; }
    public string? SelectedEpisodeOrderId { get; init; }
    public required SeriesMappingProvider MappingProvider { get; init; }
    public required IReadOnlyDictionary<string, string> ExternalIds { get; init; }
}

public sealed record SeriesAlias
{
    public required string Language { get; init; }
    public required string Name { get; init; }
}

public sealed record EpisodeOrder
{
    public required string Id { get; init; }
    public required string Name { get; init; }
    public required string Type { get; init; }
    public required bool IsDefault { get; init; }
}

public sealed record SeasonSummary
{
    public required string Id { get; init; }
    public required MediaType MediaType { get; init; }
    public required Guid SeriesId { get; init; }
    public required string Name { get; init; }
    public required string Overview { get; init; }
    public required int SeasonNumber { get; init; }
    public required int EpisodeCount { get; init; }
    public string? AirDate { get; init; }
    public string? PosterUrl { get; init; }
    public string? BackdropUrl { get; init; }
    public required double VoteAverage { get; init; }
    public required IReadOnlyDictionary<string, string> ExternalIds { get; init; }
}

public sealed record Season
{
    public required string Id { get; init; }
    public required MediaType MediaType { get; init; }
    public required Guid SeriesId { get; init; }
    public required string Name { get; init; }
    public required string Overview { get; init; }
    public required int SeasonNumber { get; init; }
    public string? AirDate { get; init; }
    public string? PosterUrl { get; init; }
    public string? BackdropUrl { get; init; }
    public required double VoteAverage { get; init; }
    public required IReadOnlyList<Episode> Episodes { get; init; }
    public required IReadOnlyDictionary<string, string> ExternalIds { get; init; }
}

public sealed record Episode
{
    public required Guid Id { get; init; }
    public required MediaType MediaType { get; init; }
    public required string SeasonId { get; init; }
    public required string Name { get; init; }
    public required string Overview { get; init; }
    public required int SeasonNumber { get; init; }
    public required int EpisodeNumber { get; init; }
    public string? AirDate { get; init; }
    public string? StillUrl { get; init; }
    public string? BackdropUrl { get; init; }
    public int? RuntimeMinutes { get; init; }
    public required double VoteAverage { get; init; }
    public required int VoteCount { get; init; }
    public required IReadOnlyDictionary<string, string> ExternalIds { get; init; }
}

public sealed record TrailerList
{
    public required IReadOnlyList<Trailer> Trailers { get; init; }
}

public sealed record Trailer
{
    public required string YoutubeId { get; init; }
    public required string Name { get; init; }
    public required string Language { get; init; }
    public required bool IsFallback { get; init; }
    public string? CaptionPreference { get; init; }
}

public sealed record PlaybackMediaProfile
{
    public required string Container { get; init; }
    public required string VideoCodec { get; init; }
    public string? AudioCodec { get; init; }
    public int? MaximumVideoBitDepth { get; init; }
}

[JsonConverter(typeof(JsonStringEnumConverter<PlaybackProcessingMode>))]
public enum PlaybackProcessingMode
{
    [JsonStringEnumMemberName("remux")] Remux,
    [JsonStringEnumMemberName("transcode_audio")] TranscodeAudio,
    [JsonStringEnumMemberName("transcode")] Transcode,
}

[JsonConverter(typeof(JsonStringEnumConverter<PlaybackSubtitleDelivery>))]
public enum PlaybackSubtitleDelivery
{
    [JsonStringEnumMemberName("external")] External,
    [JsonStringEnumMemberName("burn")] Burn,
}

public sealed record PlaybackCapabilities
{
    public required IReadOnlyList<string> StreamingProtocols { get; init; }
    public required IReadOnlyList<string> Containers { get; init; }
    public IReadOnlyList<string>? VideoCodecs { get; init; }
    public IReadOnlyList<string>? AudioCodecs { get; init; }
    public IReadOnlyList<string>? HdrFormats { get; init; }
    public IReadOnlyList<string>? ExternalPlayers { get; init; }
    public IReadOnlyList<PlaybackProcessingMode>? ProcessingModes { get; init; }
    public int? MaximumHeight { get; init; }
    public int? MaximumVideoBitrateKbps { get; init; }
    public int? MaximumAudioChannels { get; init; }
    public IReadOnlyList<PlaybackSubtitleDelivery>? SubtitleModes { get; init; }
    public IReadOnlyList<PlaybackMediaProfile>? MediaProfiles { get; init; }
}

public sealed record PlaybackSourceList
{
    public required IReadOnlyList<PlaybackSourceOption> Sources { get; init; }
    public required IReadOnlyList<PlaybackProviderError> ProviderErrors { get; init; }
}

public sealed record PlaybackSourceOption
{
    public required string Id { get; init; }
    public required string SourceRef { get; init; }
    public required Guid AddonId { get; init; }
    public string? AddonName { get; init; }
    public required string ManifestId { get; init; }
    public required int StreamIndex { get; init; }
    public required string Name { get; init; }
    public string? Description { get; init; }
    public string? Filename { get; init; }
    public required string Protocol { get; init; }
    public PlaybackMode? Mode { get; init; }
    public string? Container { get; init; }
    public required string ExpiresAt { get; init; }
}

[JsonConverter(typeof(JsonStringEnumConverter<PlaybackMode>))]
public enum PlaybackMode
{
    [JsonStringEnumMemberName("direct")] Direct,
    [JsonStringEnumMemberName("remux")] Remux,
    [JsonStringEnumMemberName("transcode_audio")] TranscodeAudio,
    [JsonStringEnumMemberName("transcode")] Transcode,
    [JsonStringEnumMemberName("youtube")] Youtube,
    [JsonStringEnumMemberName("external")] External,
}

[JsonConverter(typeof(JsonStringEnumConverter<PlaybackActivityMode>))]
public enum PlaybackActivityMode
{
    [JsonStringEnumMemberName("direct")] Direct,
    [JsonStringEnumMemberName("remux")] Remux,
    [JsonStringEnumMemberName("transcode_audio")] TranscodeAudio,
    [JsonStringEnumMemberName("transcode")] Transcode,
    [JsonStringEnumMemberName("unknown")] Unknown,
}

[JsonConverter(typeof(JsonStringEnumConverter<PlaybackMediaTimeline>))]
public enum PlaybackMediaTimeline
{
    [JsonStringEnumMemberName("absolute")] Absolute,
    [JsonStringEnumMemberName("relative")] Relative,
}

[JsonConverter(typeof(JsonStringEnumConverter<PlaybackDecisionReason>))]
public enum PlaybackDecisionReason
{
    [JsonStringEnumMemberName("direct_supported")] DirectSupported,
    [JsonStringEnumMemberName("remux_required")] RemuxRequired,
    [JsonStringEnumMemberName("audio_transcode_required")] AudioTranscodeRequired,
    [JsonStringEnumMemberName("video_transcode_required")] VideoTranscodeRequired,
    [JsonStringEnumMemberName("subtitle_burn_required")] SubtitleBurnRequired,
}

[JsonConverter(typeof(JsonStringEnumConverter<PlaybackTrackAction>))]
public enum PlaybackTrackAction
{
    [JsonStringEnumMemberName("copy")] Copy,
    [JsonStringEnumMemberName("transcode")] Transcode,
}

[JsonConverter(typeof(JsonStringEnumConverter<PlaybackSubtitleAction>))]
public enum PlaybackSubtitleAction
{
    [JsonStringEnumMemberName("none")] None,
    [JsonStringEnumMemberName("external")] External,
    [JsonStringEnumMemberName("copy")] Copy,
    [JsonStringEnumMemberName("burn")] Burn,
}

[JsonConverter(typeof(JsonStringEnumConverter<PlaybackTrackType>))]
public enum PlaybackTrackType
{
    [JsonStringEnumMemberName("video")] Video,
    [JsonStringEnumMemberName("audio")] Audio,
    [JsonStringEnumMemberName("subtitle")] Subtitle,
}

public sealed record PlaybackPreparation
{
    public required string SourceRef { get; init; }
    public required PlaybackMode Mode { get; init; }
    public required string Protocol { get; init; }
    public string? Container { get; init; }
    public PlaybackMediaInspection? Media { get; init; }
    public required int SubtitleCount { get; init; }
    public required string ExpiresAt { get; init; }
    public PlaybackDecision? Decision { get; init; }
}

public sealed record PlaybackSession
{
    public required Guid Id { get; init; }
    public required string SelectedSourceId { get; init; }
    public int? SelectedAudioTrack { get; init; }
    public string? SelectedSubtitleId { get; init; }
    public required IReadOnlyList<PlaybackSource> Sources { get; init; }
    public required IReadOnlyList<PlaybackSubtitle> Subtitles { get; init; }
    public required IReadOnlyList<PlaybackProviderError> ProviderErrors { get; init; }
    public required string ExpiresAt { get; init; }
}

public sealed record PlaybackSource
{
    public required string Id { get; init; }
    public required Guid AddonId { get; init; }
    public required string ManifestId { get; init; }
    public string? Name { get; init; }
    public string? Title { get; init; }
    public required PlaybackMode Mode { get; init; }
    public string? Url { get; init; }
    public string? YtId { get; init; }
    public string? InfoHash { get; init; }
    public int? FileIndex { get; init; }
    public required string Protocol { get; init; }
    public string? Container { get; init; }
    public PlaybackMediaTimeline? MediaTimeline { get; init; }
    public required bool Compatible { get; init; }
    public PlaybackMediaInspection? Media { get; init; }
    public PlaybackDecision? Decision { get; init; }
}

public sealed record PlaybackMediaInspection
{
    public string? Container { get; init; }
    public double? DurationSeconds { get; init; }
    public string? HdrFormat { get; init; }
    public required IReadOnlyList<PlaybackMediaTrack> VideoTracks { get; init; }
    public required IReadOnlyList<PlaybackMediaTrack> AudioTracks { get; init; }
    public required IReadOnlyList<PlaybackMediaTrack> SubtitleTracks { get; init; }
}

public sealed record PlaybackDecision
{
    public required PlaybackDecisionReason Reason { get; init; }
    public required PlaybackTrackAction VideoAction { get; init; }
    public required PlaybackTrackAction AudioAction { get; init; }
    public required PlaybackSubtitleAction SubtitleAction { get; init; }
    public required bool ToneMapping { get; init; }
    public PlaybackDecisionSource? Source { get; init; }
    public PlaybackDecisionTarget? Target { get; init; }
}

public sealed record PlaybackDecisionSource
{
    public string? Container { get; init; }
    public string? VideoCodec { get; init; }
    public string? AudioCodec { get; init; }
    public int? Height { get; init; }
    public int? VideoBitrateKbps { get; init; }
    public string? HdrFormat { get; init; }
}

public sealed record PlaybackDecisionTarget
{
    public string? Protocol { get; init; }
    public string? Container { get; init; }
    public string? VideoCodec { get; init; }
    public string? AudioCodec { get; init; }
    public int? Height { get; init; }
    public int? VideoBitDepth { get; init; }
    public int? VideoBitrateKbps { get; init; }
}

public sealed record PlaybackMediaTrack
{
    public required int Index { get; init; }
    public required PlaybackTrackType Type { get; init; }
    public required string Codec { get; init; }
    public string? Profile { get; init; }
    public string? Language { get; init; }
    public bool? Forced { get; init; }
    public string? Title { get; init; }
    public int? Width { get; init; }
    public int? Height { get; init; }
    public int? Channels { get; init; }
}

public sealed record PlaybackSubtitle
{
    public required string Id { get; init; }
    public required Guid AddonId { get; init; }
    public required string ManifestId { get; init; }
    public string? Language { get; init; }
    public bool? Forced { get; init; }
    public bool? Default { get; init; }
    public PlaybackSubtitleDelivery? Delivery { get; init; }
    public string? Url { get; init; }
}

public sealed record PlaybackProviderError
{
    public required Guid AddonId { get; init; }
    public required string ManifestId { get; init; }
    public required string Code { get; init; }
    public required string Message { get; init; }
}

[JsonConverter(typeof(JsonStringEnumConverter<PlaybackMarkerType>))]
public enum PlaybackMarkerType
{
    [JsonStringEnumMemberName("intro")] Intro,
    [JsonStringEnumMemberName("recap")] Recap,
    [JsonStringEnumMemberName("outro")] Outro,
}

public sealed record PlaybackMarkerList
{
    public required IReadOnlyList<PlaybackMarker> Markers { get; init; }
}

public sealed record PlaybackMarker
{
    public required PlaybackMarkerType Type { get; init; }
    public required double StartSeconds { get; init; }
    public required double EndSeconds { get; init; }
    public required double Confidence { get; init; }
    public required int SubmissionCount { get; init; }
}

public sealed record PlaybackActivity
{
    public required PlaybackActivitySummary Summary { get; init; }
    public required PlaybackMediaDiagnostics Diagnostics { get; init; }
    public required IReadOnlyList<PlaybackActivitySession> Sessions { get; init; }
    public required IReadOnlyList<PlaybackMediaJob> Jobs { get; init; }
    public required bool SessionsTruncated { get; init; }
    public required bool JobsTruncated { get; init; }
}

public sealed record PlaybackActivitySummary
{
    public required int ActiveSessions { get; init; }
    public required int ActiveJobs { get; init; }
    public required int ProcessingSlots { get; init; }
    public required int ProcessingLimit { get; init; }
    public required long StorageBytes { get; init; }
    public required long StorageLimitBytes { get; init; }
}

[JsonConverter(typeof(JsonStringEnumConverter<PlaybackHardwareAcceleration>))]
public enum PlaybackHardwareAcceleration
{
    [JsonStringEnumMemberName("unknown")] Unknown,
    [JsonStringEnumMemberName("auto")] Auto,
    [JsonStringEnumMemberName("software")] Software,
    [JsonStringEnumMemberName("hybrid")] Hybrid,
    [JsonStringEnumMemberName("vaapi")] Vaapi,
    [JsonStringEnumMemberName("qsv")] Qsv,
    [JsonStringEnumMemberName("nvenc")] Nvenc,
    [JsonStringEnumMemberName("amf")] Amf,
}

[JsonConverter(typeof(JsonStringEnumConverter<PlaybackVideoCodec>))]
public enum PlaybackVideoCodec
{
    [JsonStringEnumMemberName("auto")] Auto,
    [JsonStringEnumMemberName("h264")] H264,
    [JsonStringEnumMemberName("hevc")] Hevc,
    [JsonStringEnumMemberName("av1")] Av1,
}

[JsonConverter(typeof(JsonStringEnumConverter<PlaybackEncodeCodec>))]
public enum PlaybackEncodeCodec
{
    [JsonStringEnumMemberName("h264")] H264,
    [JsonStringEnumMemberName("hevc")] Hevc,
    [JsonStringEnumMemberName("av1")] Av1,
}

[JsonConverter(typeof(JsonStringEnumConverter<PlaybackQualityPreset>))]
public enum PlaybackQualityPreset
{
    [JsonStringEnumMemberName("speed")] Speed,
    [JsonStringEnumMemberName("balanced")] Balanced,
    [JsonStringEnumMemberName("quality")] Quality,
}

[JsonConverter(typeof(JsonStringEnumConverter<PlaybackToneMapBackend>))]
public enum PlaybackToneMapBackend
{
    [JsonStringEnumMemberName("vulkan")] Vulkan,
    [JsonStringEnumMemberName("vaapi")] Vaapi,
    [JsonStringEnumMemberName("hybrid")] Hybrid,
    [JsonStringEnumMemberName("software")] Software,
}

public sealed record PlaybackMediaDiagnostics
{
    public required string FfmpegVersion { get; init; }
    public required string FfprobeVersion { get; init; }
    public required PlaybackHardwareAcceleration HardwareAcceleration { get; init; }
    public required string VideoEncoder { get; init; }
    public required PlaybackVideoCodec PreferredVideoCodec { get; init; }
    public required IReadOnlyList<PlaybackEncodeCodec> EncodeCodecs { get; init; }
    public required IReadOnlyList<PlaybackEncodeCodec> DecodeCodecs { get; init; }
    public bool? HevcMain10 { get; init; }
    public required PlaybackQualityPreset QualityPreset { get; init; }
    public required bool HardwareToneMap { get; init; }
    public required PlaybackToneMapBackend ToneMapBackend { get; init; }
    public required int TranscodeThreads { get; init; }
    public required double MaximumReadRate { get; init; }
    public required PlaybackMediaProcessTotals Totals { get; init; }
    public required PlaybackMediaDiagnosticPools Pools { get; init; }
}

public sealed record PlaybackMediaProcessTotals
{
    public required long Started { get; init; }
    public required long Succeeded { get; init; }
    public required long Failed { get; init; }
    public required long SoftwareFallbacks { get; init; }
}

public sealed record PlaybackMediaDiagnosticPools
{
    public required PlaybackMediaDiagnosticPool Process { get; init; }
    public required PlaybackMediaDiagnosticPool Probe { get; init; }
    public required PlaybackMediaDiagnosticPool Subtitle { get; init; }
    public required PlaybackMediaDiagnosticPool Trickplay { get; init; }
}

public sealed record PlaybackMediaDiagnosticPool
{
    public required int Active { get; init; }
    public required int Limit { get; init; }
}

public sealed record PlaybackActivityExternalIds
{
    public string? Imdb { get; init; }
    public string? Tmdb { get; init; }
    public string? Tvdb { get; init; }
}

public sealed record PlaybackActivityExternalIdMediaTypes
{
    public MediaType? Imdb { get; init; }
    public MediaType? Tmdb { get; init; }
    public MediaType? Tvdb { get; init; }
}

public sealed record PlaybackActivitySession
{
    public required Guid Id { get; init; }
    public string? TitleId { get; init; }
    public string? ArtworkUrl { get; init; }
    public PlaybackActivityExternalIds? ExternalIds { get; init; }
    public PlaybackActivityExternalIdMediaTypes? ExternalIdMediaTypes { get; init; }
    public required string Title { get; init; }
    public required string MediaType { get; init; }
    public required PlaybackActivityMode Mode { get; init; }
    public PlaybackDecision? Decision { get; init; }
    public required string Username { get; init; }
    public required Guid ProfileId { get; init; }
    public required string Profile { get; init; }
    public required string Device { get; init; }
    public required string Platform { get; init; }
    public required bool Processing { get; init; }
    public required int PositionSeconds { get; init; }
    public required int DurationSeconds { get; init; }
    public required string CreatedAt { get; init; }
    public required string LastSeenAt { get; init; }
    public required string ExpiresAt { get; init; }
}

[JsonConverter(typeof(JsonStringEnumConverter<PlaybackMediaJobState>))]
public enum PlaybackMediaJobState
{
    [JsonStringEnumMemberName("processing")] Processing,
    [JsonStringEnumMemberName("complete")] Complete,
    [JsonStringEnumMemberName("failed")] Failed,
}

[JsonConverter(typeof(JsonStringEnumConverter<PlaybackMediaJobErrorClass>))]
public enum PlaybackMediaJobErrorClass
{
    [JsonStringEnumMemberName("capacity")] Capacity,
    [JsonStringEnumMemberName("source")] Source,
    [JsonStringEnumMemberName("processing")] Processing,
    [JsonStringEnumMemberName("storage")] Storage,
    [JsonStringEnumMemberName("timeout")] Timeout,
    [JsonStringEnumMemberName("cancelled")] Cancelled,
    [JsonStringEnumMemberName("unknown")] Unknown,
}

public sealed record PlaybackMediaJob
{
    public Guid? SessionId { get; init; }
    public required string AssetId { get; init; }
    public required string Mode { get; init; }
    public required PlaybackMediaJobState State { get; init; }
    public PlaybackMediaJobErrorClass? ErrorClass { get; init; }
    public required bool Prewarming { get; init; }
    public double? ProgressPercent { get; init; }
    public double? Speed { get; init; }
    public double? StartupDurationSeconds { get; init; }
    public required string CreatedAt { get; init; }
    public required string LastSeenAt { get; init; }
}

[JsonConverter(typeof(JsonStringEnumConverter<PlaybackProgressMediaType>))]
public enum PlaybackProgressMediaType
{
    [JsonStringEnumMemberName("movie")] Movie,
    [JsonStringEnumMemberName("episode")] Episode,
}

public sealed record PlaybackProgress
{
    public required Guid TitleId { get; init; }
    public required PlaybackProgressMediaType MediaType { get; init; }
    public required int PositionSeconds { get; init; }
    public required int DurationSeconds { get; init; }
    public required bool Completed { get; init; }
    public required long Version { get; init; }
    public required string LastWatchedAt { get; init; }
    public required string UpdatedAt { get; init; }
}

public sealed record PlaybackProgressBatchRequest
{
    public required IReadOnlyList<Guid> TitleIds { get; init; }
}

public sealed record PlaybackProgressBatchItem
{
    public required Guid TitleId { get; init; }
    public required PlaybackProgress? Progress { get; init; }
}

public sealed record PlaybackProgressBatch
{
    public required IReadOnlyList<PlaybackProgressBatchItem> Items { get; init; }
}

public sealed record UpdatePlaybackProgressRequest
{
    public required int PositionSeconds { get; init; }
    public required int DurationSeconds { get; init; }
    public required bool Completed { get; init; }
    public required long ExpectedVersion { get; init; }
}

public sealed record SetWatchedBatchItem
{
    public required Guid TitleId { get; init; }
    public required bool Completed { get; init; }
    public required long ExpectedVersion { get; init; }
}

public sealed record SetWatchedBatchRequest
{
    public required IReadOnlyList<SetWatchedBatchItem> Items { get; init; }
}

public sealed record SetWatchedBatchResultItem
{
    public required Guid TitleId { get; init; }
    public required PlaybackProgress Progress { get; init; }
}

public sealed record SetWatchedBatchResult
{
    public required IReadOnlyList<SetWatchedBatchResultItem> Items { get; init; }
}

public sealed record CompletionRequest
{
    public required long ExpectedVersion { get; init; }
}

[JsonConverter(typeof(JsonStringEnumConverter<ContinueWatchingReason>))]
public enum ContinueWatchingReason
{
    [JsonStringEnumMemberName("resume")] Resume,
    [JsonStringEnumMemberName("next_episode")] NextEpisode,
}

public sealed record ContinueWatchingItem
{
    public required Guid TitleId { get; init; }
    public required PlaybackProgressMediaType MediaType { get; init; }
    public Guid? SeriesId { get; init; }
    public Guid? SeasonId { get; init; }
    public int? SeasonNumber { get; init; }
    public int? EpisodeNumber { get; init; }
    public required int PositionSeconds { get; init; }
    public required int DurationSeconds { get; init; }
    public required long Version { get; init; }
    public required ContinueWatchingReason Reason { get; init; }
    public required string LastWatchedAt { get; init; }
}

public sealed record ContinueWatchingPage
{
    public required IReadOnlyList<ContinueWatchingItem> Items { get; init; }
}

public sealed record ServerError
{
    public required string Code { get; init; }
    public required string Message { get; init; }
}
