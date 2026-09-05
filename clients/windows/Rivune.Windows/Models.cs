using System.Text.Json;
using System.Text.Json.Serialization;

namespace Rivune.Windows;

public static class RivuneProtocol
{
    public const int Version = 22;
}

public static class DiscoveryCapabilityIdentifiers
{
    public const string BoundedAggregateResources = "bounded-aggregate-resources";
    public const string ProfileArchivesV2 = "profile-archives-v2";
    public const string RequestCorrelation = "request-correlation";
    public const string LocalRecommendations = "local-recommendations";
    public const string SemanticSearch = "semantic-search";
    public const string PlaybackCoordination = "playback-coordination";
    public const string PlaybackCommandResults = "playback-command-results";
}

public enum DiscoveryCapability
{
    BoundedAggregateResources,
    ProfileArchivesV2,
    RequestCorrelation,
    LocalRecommendations,
    SemanticSearch,
    PlaybackCoordination,
    PlaybackCommandResults,
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
            DiscoveryCapability.ProfileArchivesV2 => DiscoveryCapabilityIdentifiers.ProfileArchivesV2,
            DiscoveryCapability.RequestCorrelation => DiscoveryCapabilityIdentifiers.RequestCorrelation,
            DiscoveryCapability.LocalRecommendations => DiscoveryCapabilityIdentifiers.LocalRecommendations,
            DiscoveryCapability.SemanticSearch => DiscoveryCapabilityIdentifiers.SemanticSearch,
            DiscoveryCapability.PlaybackCoordination => DiscoveryCapabilityIdentifiers.PlaybackCoordination,
            DiscoveryCapability.PlaybackCommandResults => DiscoveryCapabilityIdentifiers.PlaybackCommandResults,
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

    public bool SupportsProfileArchivesV2 => Supports(DiscoveryCapability.ProfileArchivesV2);
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

    void IJsonOnDeserialized.OnDeserialized() => Validate(this);

    internal static void Validate(IAuthorizationContext context)
    {
        switch (context.AuthorizationScope)
        {
            case global::Rivune.Windows.AuthorizationScope.GlobalAdministrator when context.Category is null:
            case global::Rivune.Windows.AuthorizationScope.Category when context.Category is not null:
                return;
            case global::Rivune.Windows.AuthorizationScope.GlobalAdministrator:
                throw new JsonException("A global_admin authorization context cannot include a category.");
            case global::Rivune.Windows.AuthorizationScope.Category:
                throw new JsonException("A category authorization context requires a category.");
            default:
                throw new JsonException($"Unsupported authorization scope '{context.AuthorizationScope}'.");
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
    private PatchField(bool isSpecified, bool isNull, T? value)
    {
        IsSpecified = isSpecified;
        IsNull = isNull;
        Value = value;
    }

    public bool IsSpecified { get; }
    public bool IsNull { get; }
    public T? Value { get; }
    public static PatchField<T> Omitted => default;
    public static PatchField<T> Null => new(true, true, default);
    public static PatchField<T> FromValue(T value) => new(true, false, value);
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

public sealed record TokenPair : IAuthorizationContext, IJsonOnDeserialized
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

    void IJsonOnDeserialized.OnDeserialized()
    {
        if (!StringComparer.Ordinal.Equals(TokenType, "Bearer") ||
            string.IsNullOrEmpty(AccessToken) ||
            AccessTokenExpiresAt is null ||
            string.IsNullOrEmpty(RefreshToken) ||
            RefreshTokenExpiresAt is null)
        {
            throw new JsonException("The token response contains invalid required fields.");
        }
        IAuthorizationContext.Validate(this);
    }
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
    public required string InstallationId { get; init; }
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
    public string? InterfaceLanguage { get; init; }
    public string? Theme { get; init; }
    public string? MaximumResolution { get; init; }
    public int? MaximumCastMembers { get; init; }
    public int? MaximumDirectTitles { get; init; }
    public bool? AllowTranscoding { get; init; }
    public string? Transcoding { get; init; }
    public bool? PreferDirectPlay { get; init; }
    public bool? HideUnreleased { get; init; }
    public string? MetadataLanguage { get; init; }
    public string? MetadataRegion { get; init; }
    public string? SeriesMappingProvider { get; init; }
    public string? AudioLanguage { get; init; }
    public string? SubtitleLanguage { get; init; }
    public string? ForcedSubtitleLanguage { get; init; }
    public bool? AutoplayNextEpisode { get; init; }
    public bool? SkipIntroEnabled { get; init; }
    public bool? SkipRecapEnabled { get; init; }
    public bool? SkipOutroEnabled { get; init; }
    public string? CardDensity { get; init; }
    public bool? AnimationsEnabled { get; init; }
    public int? SubtitleSizePercent { get; init; }
    public string? SubtitleTextColor { get; init; }
    public int? SubtitleBackgroundOpacityPercent { get; init; }
    public bool? NotificationsEnabled { get; init; }
    public int? NotificationDurationSeconds { get; init; }
    public int? NotificationPollIntervalSeconds { get; init; }
}

public record CommonSettingsPatch
{
    public PatchField<string> InterfaceLanguage { get; init; }
    public PatchField<string> Theme { get; init; }
    public PatchField<string> MaximumResolution { get; init; }
    public PatchField<int> MaximumCastMembers { get; init; }
    public PatchField<int> MaximumDirectTitles { get; init; }
    public PatchField<bool> PreferDirectPlay { get; init; }
    public PatchField<bool> HideUnreleased { get; init; }
    public PatchField<string> MetadataLanguage { get; init; }
    public PatchField<string> MetadataRegion { get; init; }
    public PatchField<string> SeriesMappingProvider { get; init; }
    public PatchField<string> AudioLanguage { get; init; }
    public PatchField<string> SubtitleLanguage { get; init; }
    public PatchField<string> ForcedSubtitleLanguage { get; init; }
    public PatchField<bool> AutoplayNextEpisode { get; init; }
    public PatchField<bool> SkipIntroEnabled { get; init; }
    public PatchField<bool> SkipRecapEnabled { get; init; }
    public PatchField<bool> SkipOutroEnabled { get; init; }
    public PatchField<string> CardDensity { get; init; }
    public PatchField<bool> AnimationsEnabled { get; init; }
    public PatchField<int> SubtitleSizePercent { get; init; }
    public PatchField<string> SubtitleTextColor { get; init; }
    public PatchField<int> SubtitleBackgroundOpacityPercent { get; init; }
}

public sealed record SettingsPatch : CommonSettingsPatch
{
    public PatchField<string> Transcoding { get; init; }
}

public sealed record InstanceSettingsPatch : CommonSettingsPatch
{
    public PatchField<bool> AllowTranscoding { get; init; }
    public PatchField<bool> NotificationsEnabled { get; init; }
    public PatchField<int> NotificationDurationSeconds { get; init; }
    public PatchField<int> NotificationPollIntervalSeconds { get; init; }
    public string? Timezone { get; init; }
    public bool? JellyfinEnabled { get; init; }
    public bool? JellyfinDebug { get; init; }
    public string? HardwareAcceleration { get; init; }
    public string? PreferredTranscodeVideoCodec { get; init; }
    public string? TranscodeQualityPreset { get; init; }
    public int? TranscodeConcurrency { get; init; }
    public int? TranscodeMaxBitrateKbps { get; init; }
    public int? MediaMaxStorageMB { get; init; }
    public int? ArtworkMaxStorageMB { get; init; }
}

public sealed record InstanceSettingsValues
{
    public string? InterfaceLanguage { get; init; }
    public string? Theme { get; init; }
    public string? MaximumResolution { get; init; }
    public int? MaximumCastMembers { get; init; }
    public int? MaximumDirectTitles { get; init; }
    public required bool AllowTranscoding { get; init; }
    public string? Transcoding { get; init; }
    public bool? PreferDirectPlay { get; init; }
    public bool? HideUnreleased { get; init; }
    public string? MetadataLanguage { get; init; }
    public string? MetadataRegion { get; init; }
    public string? SeriesMappingProvider { get; init; }
    public string? AudioLanguage { get; init; }
    public string? SubtitleLanguage { get; init; }
    public string? ForcedSubtitleLanguage { get; init; }
    public bool? AutoplayNextEpisode { get; init; }
    public bool? SkipIntroEnabled { get; init; }
    public bool? SkipRecapEnabled { get; init; }
    public bool? SkipOutroEnabled { get; init; }
    public string? CardDensity { get; init; }
    public bool? AnimationsEnabled { get; init; }
    public int? SubtitleSizePercent { get; init; }
    public string? SubtitleTextColor { get; init; }
    public int? SubtitleBackgroundOpacityPercent { get; init; }
    public bool? NotificationsEnabled { get; init; }
    public int? NotificationDurationSeconds { get; init; }
    public int? NotificationPollIntervalSeconds { get; init; }
    public required string Timezone { get; init; }
    public required bool JellyfinEnabled { get; init; }
    public required bool JellyfinDebug { get; init; }
    public required string HardwareAcceleration { get; init; }
    public required string PreferredTranscodeVideoCodec { get; init; }
    public required string TranscodeQualityPreset { get; init; }
    public required int TranscodeConcurrency { get; init; }
    public required int TranscodeMaxBitrateKbps { get; init; }
    public required int MediaMaxStorageMB { get; init; }
    public required int ArtworkMaxStorageMB { get; init; }
}

public sealed record RuntimeSettingsValues
{
    public required string Timezone { get; init; }
    public required bool JellyfinEnabled { get; init; }
    public required bool JellyfinDebug { get; init; }
    public required string HardwareAcceleration { get; init; }
    public required string PreferredTranscodeVideoCodec { get; init; }
    public required string TranscodeQualityPreset { get; init; }
    public required int TranscodeConcurrency { get; init; }
    public required int TranscodeMaxBitrateKbps { get; init; }
    public required int MediaMaxStorageMB { get; init; }
    public required int ArtworkMaxStorageMB { get; init; }
    public required bool AllowTranscoding { get; init; }
}

public sealed record RuntimeSettingsApplication
{
    public required RuntimeSettingsValues Active { get; init; }
    public required RuntimeSettingsValues Requested { get; init; }
    public required IReadOnlyList<string> PendingRestart { get; init; }
}

public sealed record InstanceSettingsLayer
{
    public required int SchemaVersion { get; init; }
    public required long Revision { get; init; }
    public required InstanceSettingsValues Settings { get; init; }
    public required RuntimeSettingsApplication Runtime { get; init; }
    public required string? UpdatedAt { get; init; }
}

public sealed record SettingsLayer
{
    public required int SchemaVersion { get; init; }
    public required SettingsValues Settings { get; init; }
    public required string? UpdatedAt { get; init; }
}

public sealed record EffectiveSettingsValues
{
    public required string InterfaceLanguage { get; init; }
    public required string Theme { get; init; }
    public required string MaximumResolution { get; init; }
    public required int? MaximumCastMembers { get; init; }
    public required int? MaximumDirectTitles { get; init; }
    public bool? AllowTranscoding { get; init; }
    public string? Transcoding { get; init; }
    public required bool PreferDirectPlay { get; init; }
    public required bool HideUnreleased { get; init; }
    public required string MetadataLanguage { get; init; }
    public required string MetadataRegion { get; init; }
    public required string SeriesMappingProvider { get; init; }
    public required string AudioLanguage { get; init; }
    public required string SubtitleLanguage { get; init; }
    public required string ForcedSubtitleLanguage { get; init; }
    public required bool AutoplayNextEpisode { get; init; }
    public required bool SkipIntroEnabled { get; init; }
    public required bool SkipRecapEnabled { get; init; }
    public required bool SkipOutroEnabled { get; init; }
    public required string CardDensity { get; init; }
    public required bool AnimationsEnabled { get; init; }
    public required int SubtitleSizePercent { get; init; }
    public required string SubtitleTextColor { get; init; }
    public required int SubtitleBackgroundOpacityPercent { get; init; }
    public required bool NotificationsEnabled { get; init; }
    public required int NotificationDurationSeconds { get; init; }
    public required int NotificationPollIntervalSeconds { get; init; }
}

[JsonConverter(typeof(JsonStringEnumConverter<SettingSource>))]
public enum SettingSource
{
    [JsonStringEnumMemberName("default")] Default,
    [JsonStringEnumMemberName("instance")] Instance,
    [JsonStringEnumMemberName("profile")] Profile,
    [JsonStringEnumMemberName("device")] Device,
}

public sealed record EffectiveSettingsSources
{
    public required SettingSource InterfaceLanguage { get; init; }
    public required SettingSource Theme { get; init; }
    public required SettingSource MaximumResolution { get; init; }
    public required SettingSource MaximumCastMembers { get; init; }
    public required SettingSource MaximumDirectTitles { get; init; }
    public SettingSource? AllowTranscoding { get; init; }
    public SettingSource? Transcoding { get; init; }
    public required SettingSource PreferDirectPlay { get; init; }
    public required SettingSource HideUnreleased { get; init; }
    public required SettingSource MetadataLanguage { get; init; }
    public required SettingSource MetadataRegion { get; init; }
    public required SettingSource SeriesMappingProvider { get; init; }
    public required SettingSource AudioLanguage { get; init; }
    public required SettingSource SubtitleLanguage { get; init; }
    public required SettingSource ForcedSubtitleLanguage { get; init; }
    public required SettingSource AutoplayNextEpisode { get; init; }
    public required SettingSource SkipIntroEnabled { get; init; }
    public required SettingSource SkipRecapEnabled { get; init; }
    public required SettingSource SkipOutroEnabled { get; init; }
    public required SettingSource CardDensity { get; init; }
    public required SettingSource AnimationsEnabled { get; init; }
    public required SettingSource SubtitleSizePercent { get; init; }
    public required SettingSource SubtitleTextColor { get; init; }
    public required SettingSource SubtitleBackgroundOpacityPercent { get; init; }
    public required SettingSource NotificationsEnabled { get; init; }
    public required SettingSource NotificationDurationSeconds { get; init; }
    public required SettingSource NotificationPollIntervalSeconds { get; init; }
}

public sealed record EffectiveSettings
{
    public required int SchemaVersion { get; init; }
    public required EffectiveSettingsValues Settings { get; init; }
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

[JsonConverter(typeof(JsonStringEnumConverter<CollectionViewMode>))]
public enum CollectionViewMode
{
    [JsonStringEnumMemberName("tabbed_grid")]
    TabbedGrid,
    [JsonStringEnumMemberName("rows")]
    Rows,
    [JsonStringEnumMemberName("follow_layout")]
    FollowLayout,
}

[JsonConverter(typeof(JsonStringEnumConverter<CollectionTileShape>))]
public enum CollectionTileShape
{
    [JsonStringEnumMemberName("poster")]
    Poster,
    [JsonStringEnumMemberName("landscape")]
    Landscape,
    [JsonStringEnumMemberName("square")]
    Square,
}

[JsonConverter(typeof(JsonStringEnumConverter<CollectionSourceView>))]
public enum CollectionSourceView
{
    [JsonStringEnumMemberName("merged")]
    Merged,
    [JsonStringEnumMemberName("categories")]
    Categories,
    [JsonStringEnumMemberName("folders")]
    Folders,
}

[JsonConverter(typeof(JsonStringEnumConverter<CollectionSourceKind>))]
public enum CollectionSourceKind
{
    [JsonStringEnumMemberName("addon_catalog")]
    AddonCatalog,
    [JsonStringEnumMemberName("tmdb")]
    Tmdb,
    [JsonStringEnumMemberName("trakt")]
    Trakt,
    [JsonStringEnumMemberName("mdblist")]
    Mdblist,
}

[JsonConverter(typeof(JsonStringEnumConverter<CollectionSourceFailureCode>))]
public enum CollectionSourceFailureCode
{
    [JsonStringEnumMemberName("collection_provider_unavailable")]
    ProviderUnavailable,
    [JsonStringEnumMemberName("collection_addon_not_found")]
    AddonNotFound,
    [JsonStringEnumMemberName("collection_source_unsupported")]
    SourceUnsupported,
    [JsonStringEnumMemberName("collection_source_timeout")]
    SourceTimeout,
    [JsonStringEnumMemberName("collection_source_failed")]
    SourceFailed,
}

[JsonConverter(typeof(JsonStringEnumConverter<TitleResolveMediaType>))]
public enum TitleResolveMediaType
{
    [JsonStringEnumMemberName("movie")]
    Movie,
    [JsonStringEnumMemberName("series")]
    Series,
    [JsonStringEnumMemberName("tv")]
    Tv,
}

public sealed record CollectionList
{
    public required IReadOnlyList<Collection> Collections { get; init; }
}

public sealed record Collection
{
    public required Guid Id { get; init; }
    public required string Title { get; init; }
    public string? BackdropImageUrl { get; init; }
    public required bool HeroEnabled { get; init; }
    public required bool PinToTop { get; init; }
    public required bool FocusGlowEnabled { get; init; }
    public required CollectionViewMode ViewMode { get; init; }
    public required CollectionTileShape FolderCoverShape { get; init; }
    public required IReadOnlyList<CollectionFolder> Folders { get; init; }
    public required IReadOnlyList<Guid> ProfileIds { get; init; }
    public required IReadOnlyList<Guid> CategoryIds { get; init; }
    public required int Position { get; init; }
    public required long Version { get; init; }
    public required string CreatedAt { get; init; }
    public required string UpdatedAt { get; init; }
}

public sealed record CollectionFolder
{
    public Guid? Id { get; init; }
    public required string Title { get; init; }
    public required CollectionTileShape TileShape { get; init; }
    public CollectionSourceView? SourceView { get; init; }
    public string? CoverImageUrl { get; init; }
    public string? CoverEmoji { get; init; }
    public string? TitleLogoUrl { get; init; }
    public string? HeroBackdropUrl { get; init; }
    public string? HeroVideoUrl { get; init; }
    public string? FocusGifUrl { get; init; }
    public required bool FocusGifEnabled { get; init; }
    public required bool HideTitle { get; init; }
    public required IReadOnlyList<CollectionSource> Sources { get; init; }
}

public sealed record CollectionSource
{
    public Guid? Id { get; init; }
    public required CollectionSourceKind Kind { get; init; }
    public required string Title { get; init; }
    public CollectionAddonCatalogSource? AddonCatalog { get; init; }
    public JsonElement? Tmdb { get; init; }
    public JsonElement? Trakt { get; init; }
    public JsonElement? Mdblist { get; init; }
}

public sealed record CollectionAddonCatalogSource
{
    public required Guid AddonId { get; init; }
    public string? ManifestId { get; init; }
    public required string Type { get; init; }
    public required string CatalogId { get; init; }
    public IReadOnlyList<CollectionExtraValue>? Extra { get; init; }
}

public sealed record CollectionExtraValue
{
    public required string Name { get; init; }
    public required string Value { get; init; }
}

public sealed record ResolvedCollectionFolder
{
    public required Guid CollectionId { get; init; }
    public required CollectionFolder Folder { get; init; }
    public IReadOnlyDictionary<Guid, string>? SourcePosterUrls { get; init; }
    public required IReadOnlyList<CollectionItem> Items { get; init; }
    public required int Page { get; init; }
    public required bool HasMore { get; init; }
    public required IReadOnlyList<CollectionSourceFailure> Errors { get; init; }
}

public sealed record CollectionItem
{
    public required string Id { get; init; }
    public required string MediaType { get; init; }
    public required string Title { get; init; }
    public string? PosterUrl { get; init; }
    public string? BackgroundUrl { get; init; }
    public string? LogoUrl { get; init; }
    public string? Description { get; init; }
    public string? ReleaseInfo { get; init; }
    public string? Released { get; init; }
    public double? VoteAverage { get; init; }
    public long? VoteCount { get; init; }
    public double? Popularity { get; init; }
    public required IReadOnlyDictionary<string, string> ExternalIds { get; init; }
    public required IReadOnlyList<CollectionSourceReference> Sources { get; init; }
    public JsonElement? Raw { get; init; }
}

public sealed record SemanticSearchRequest
{
    public required string Query { get; init; }
    public string? MediaType { get; init; }
    public string? Language { get; init; }
    public string? Region { get; init; }
    public int Page { get; init; } = 1;
    public int Limit { get; init; } = 24;
    public IReadOnlyList<string> ExcludedIntentIds { get; init; } = [];
}

public sealed record SemanticSearchIntent
{
    public required string Id { get; init; }
    public required string Kind { get; init; }
    public required string Value { get; init; }
    public required string Label { get; init; }
}

public sealed record SemanticSearchPage
{
    public required IReadOnlyList<SemanticSearchIntent> Intents { get; init; }
    public required string TitleQuery { get; init; }
    public required IReadOnlyList<string> MediaTypes { get; init; }
    public required IReadOnlyList<CollectionItem> Items { get; init; }
    public required int Page { get; init; }
    public required bool HasMore { get; init; }
    public required bool Partial { get; init; }
}

public sealed record CollectionSourceReference
{
    public required Guid Id { get; init; }
    public required CollectionSourceKind Kind { get; init; }
    public required string Title { get; init; }
    public Guid? AddonId { get; init; }
    public string? ManifestId { get; init; }
    public string? CatalogId { get; init; }
}

public sealed record CollectionSourceFailure
{
    public required Guid SourceId { get; init; }
    public required CollectionSourceKind Kind { get; init; }
    public required CollectionSourceFailureCode Code { get; init; }
    public required string Message { get; init; }
}
[JsonConverter(typeof(JsonStringEnumConverter<CalendarEventMediaType>))]
public enum CalendarEventMediaType
{
    [JsonStringEnumMemberName("movie")]
    Movie,
    [JsonStringEnumMemberName("episode")]
    Episode,
}

public sealed record CalendarEventList
{
    public required IReadOnlyList<CalendarEvent> Events { get; init; }
}

public sealed record CalendarEvent
{
    public required string Id { get; init; }
    public required Guid TitleId { get; init; }
    public required CalendarEventMediaType MediaType { get; init; }
    public required string Title { get; init; }
    public required string ReleaseDate { get; init; }
    public string? PosterUrl { get; init; }
    public string? ResourceId { get; init; }
    public string? ResourceProvider { get; init; }
    public string? SeriesTitle { get; init; }
    public Guid? SeriesId { get; init; }
    public Guid? SeasonId { get; init; }
    public int? SeasonNumber { get; init; }
    public int? EpisodeNumber { get; init; }
}

public sealed record StremioExtraProperty
{
    public required string Name { get; init; }
    public bool? IsRequired { get; init; }
    public string? Default { get; init; }
    public IReadOnlyList<string>? Options { get; init; }
    public int? OptionsLimit { get; init; }
}

public sealed record StremioManifestCatalog
{
    public required string Type { get; init; }
    public required string Id { get; init; }
    public string? Name { get; init; }
    public IReadOnlyList<string>? Genres { get; init; }
    public IReadOnlyList<StremioExtraProperty>? Extra { get; init; }
    public IReadOnlyList<string>? ExtraRequired { get; init; }
    public IReadOnlyList<string>? ExtraSupported { get; init; }
}

public sealed record AddonCatalogDescriptorList
{
    public required IReadOnlyList<AddonCatalogDescriptor> Catalogs { get; init; }
}

public sealed record AddonCatalogDescriptor
{
    public required Guid AddonId { get; init; }
    public string? AddonName { get; init; }
    public string? AddonLogoUrl { get; init; }
    public required string ManifestId { get; init; }
    public required int Position { get; init; }
    public required StremioManifestCatalog Catalog { get; init; }
    public required bool AddonCatalog { get; init; }
    public required bool Searchable { get; init; }
}

public sealed record AddonCachePolicy
{
    public long? MaxAgeSeconds { get; init; }
    public long? StaleWhileRevalidateSeconds { get; init; }
    public long? StaleIfErrorSeconds { get; init; }
}

public sealed record AddonExtraValue
{
    public required string Name { get; init; }
    public required string Value { get; init; }
}

public sealed record AddonResourceResult
{
    public required Guid AddonId { get; init; }
    public required string ManifestId { get; init; }
    public required string Resource { get; init; }
    public required string Type { get; init; }
    public required string Id { get; init; }
    public required JsonElement Payload { get; init; }
    public required AddonCachePolicy Cache { get; init; }
    public IReadOnlyList<AddonExtraValue>? Extra { get; init; }
}

public sealed record AddonResourceFailure
{
    public required Guid AddonId { get; init; }
    public required string ManifestId { get; init; }
    public required string Code { get; init; }
    public required string Message { get; init; }
}

public sealed record AddonResourceBatch
{
    public required IReadOnlyList<AddonResourceResult> Results { get; init; }
    public required IReadOnlyList<AddonResourceFailure> Errors { get; init; }
}

[JsonConverter(typeof(JsonStringEnumConverter<TitleMediaType>))]
public enum TitleMediaType
{
    [JsonStringEnumMemberName("movie")]
    Movie,
    [JsonStringEnumMemberName("series")]
    Series,
    [JsonStringEnumMemberName("tv")]
    Tv,
}

public sealed record LibraryItem
{
    public required Guid TitleId { get; init; }
    public required TitleMediaType MediaType { get; init; }
    public string? Provider { get; init; }
    public string? ExternalId { get; init; }
    public string? ResourceId { get; init; }
    public string? Title { get; init; }
    public string? PosterUrl { get; init; }
    public string? BackgroundUrl { get; init; }
    public string? ReleaseInfo { get; init; }
    public Guid? SourceAddonId { get; init; }
    public string? SourceCatalogId { get; init; }
    public string? SourceName { get; init; }
    public string? Country { get; init; }
    public string? Language { get; init; }
    public string? Category { get; init; }
    public required bool Available { get; init; }
    public required string AddedAt { get; init; }
    public required string UpdatedAt { get; init; }
}

public sealed record LibraryPage
{
    public required IReadOnlyList<LibraryItem> Items { get; init; }
    public required int Page { get; init; }
    public required int TotalPages { get; init; }
    public required int TotalResults { get; init; }
}


public sealed record TitleResolveInput
{
    public required TitleResolveMediaType MediaType { get; init; }
    public required string Provider { get; init; }
    public string? ExternalId { get; init; }
    public required string ResourceId { get; init; }
    public required string Title { get; init; }
    public string? PosterUrl { get; init; }
    public string? BackgroundUrl { get; init; }
    public string? ReleaseInfo { get; init; }
    public string? Released { get; init; }
    public Guid? SourceAddonId { get; init; }
    public string? SourceCatalogId { get; init; }
    public string? SourceName { get; init; }
    public string? Country { get; init; }
    public string? Language { get; init; }
    public string? Category { get; init; }
}

public sealed record TitleReference
{
    public required Guid TitleId { get; init; }
    public required TitleResolveMediaType MediaType { get; init; }
    public required string Provider { get; init; }
    public required string ExternalId { get; init; }
    public required string ResourceId { get; init; }
    public required string Title { get; init; }
    public string? PosterUrl { get; init; }
    public string? BackgroundUrl { get; init; }
    public string? ReleaseInfo { get; init; }
    public Guid? SourceAddonId { get; init; }
    public string? SourceCatalogId { get; init; }
    public string? SourceName { get; init; }
    public string? Country { get; init; }
    public string? Language { get; init; }
    public string? Category { get; init; }
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

public sealed record PlaybackSourceList : IJsonOnDeserialized
{
    public required IReadOnlyList<PlaybackSourceOption> Sources { get; init; }
    public required IReadOnlyList<PlaybackProviderError> ProviderErrors { get; init; }

    void IJsonOnDeserialized.OnDeserialized()
    {
        if (Sources is null || ProviderErrors is null || Sources.Any(source => source is null))
            throw new JsonException("The playback source response contains null required collections.");
    }
}

public sealed record PlaybackSourceOption : IJsonOnDeserialized
{
    public required string Id { get; init; }
    public required string SourceRef { get; init; }
    public string StableIdentity { get; init; } = string.Empty;
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

    void IJsonOnDeserialized.OnDeserialized()
    {
        if (Id is null ||
            SourceRef is null ||
            StableIdentity is null ||
            ManifestId is null ||
            StreamIndex < 0 ||
            Name is null ||
            Protocol is null ||
            ExpiresAt is null)
        {
            throw new JsonException("The playback source contains invalid required fields.");
        }
    }
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

[JsonConverter(typeof(JsonStringEnumConverter<PlaybackDecisionDetailReason>))]
public enum PlaybackDecisionDetailReason
{
    [JsonStringEnumMemberName("container_not_supported")] ContainerNotSupported,
    [JsonStringEnumMemberName("video_codec_not_supported")] VideoCodecNotSupported,
    [JsonStringEnumMemberName("audio_codec_not_supported")] AudioCodecNotSupported,
    [JsonStringEnumMemberName("resolution_limit")] ResolutionLimit,
    [JsonStringEnumMemberName("bitrate_limit")] BitrateLimit,
    [JsonStringEnumMemberName("hdr_not_supported")] HdrNotSupported,
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

public sealed record PlaybackPreparation : IJsonOnDeserialized
{
    public required string SourceRef { get; init; }
    public required PlaybackMode Mode { get; init; }
    public required string Protocol { get; init; }
    public string? Container { get; init; }
    public PlaybackMediaInspection? Media { get; init; }
    public required int SubtitleCount { get; init; }
    public required string ExpiresAt { get; init; }
    public PlaybackDecision? Decision { get; init; }

    void IJsonOnDeserialized.OnDeserialized()
    {
        if (SourceRef is null ||
            Protocol is null ||
            SubtitleCount < 0 ||
            ExpiresAt is null)
        {
            throw new JsonException("The playback preparation contains invalid required fields.");
        }
    }
}

public sealed record PlaybackSession : IJsonOnDeserialized
{
    public required Guid Id { get; init; }
    public required string SelectedSourceId { get; init; }
    public int? SelectedAudioTrack { get; init; }
    public string? SelectedSubtitleId { get; init; }
    public required IReadOnlyList<PlaybackSource> Sources { get; init; }
    public required IReadOnlyList<PlaybackSubtitle> Subtitles { get; init; }
    public required IReadOnlyList<PlaybackProviderError> ProviderErrors { get; init; }
    public required string ExpiresAt { get; init; }

    void IJsonOnDeserialized.OnDeserialized()
    {
        if (SelectedSourceId is null ||
            Sources is null ||
            Subtitles is null ||
            ProviderErrors is null ||
            Sources.Any(source => source is null) ||
            Subtitles.Any(subtitle => subtitle is null) ||
            ExpiresAt is null)
        {
            throw new JsonException("The playback session contains invalid required fields.");
        }
    }
}
public sealed record CoordinatedPlaybackItem
{
    public required Guid TitleId { get; init; }
    public string MediaType { get; init; } = string.Empty;
    public string ResourceId { get; init; } = string.Empty;
    public Guid? SourceAddonId { get; init; }
    public string Title { get; init; } = string.Empty;
    public string? PosterUrl { get; init; }
}

public sealed record PlaybackDeviceState
{
    public required string Status { get; init; }
    public CoordinatedPlaybackItem? Item { get; init; }
    public long PositionMilliseconds { get; init; }
    public long DurationMilliseconds { get; init; }
    public string? UpdatedAt { get; init; }
}

public sealed record PlaybackDeviceHeartbeatInput
{
    public required IReadOnlyList<string> Capabilities { get; init; }
    public required PlaybackDeviceState State { get; init; }
}

public sealed record PlaybackDevice
{
    public required Guid SessionId { get; init; }
    public required Guid DeviceId { get; init; }
    public required string Name { get; init; }
    public required string Platform { get; init; }
    public required IReadOnlyList<string> Capabilities { get; init; }
    public required PlaybackDeviceState State { get; init; }
    public required long Revision { get; init; }
    public required bool Current { get; init; }
    public required string LastSeenAt { get; init; }
}

public sealed record PlaybackDeviceList { public required IReadOnlyList<PlaybackDevice> Devices { get; init; } }

[JsonConverter(typeof(JsonStringEnumConverter<PlaybackLoadMode>))]
public enum PlaybackLoadMode
{
    [JsonStringEnumMemberName("handoff")] Handoff,
    [JsonStringEnumMemberName("play-copy")] PlayCopy,
}

[JsonConverter(typeof(JsonStringEnumConverter<PlaybackCommandStatus>))]
public enum PlaybackCommandStatus
{
    [JsonStringEnumMemberName("pending")] Pending,
    [JsonStringEnumMemberName("applied")] Applied,
    [JsonStringEnumMemberName("failed")] Failed,
    [JsonStringEnumMemberName("expired")] Expired,
}

[JsonConverter(typeof(JsonStringEnumConverter<PlaybackOperationStatus>))]
public enum PlaybackOperationStatus
{
    [JsonStringEnumMemberName("applied")] Applied,
    [JsonStringEnumMemberName("failed")] Failed,
    [JsonStringEnumMemberName("expired")] Expired,
}

[JsonConverter(typeof(JsonStringEnumConverter<PlaybackOperationCode>))]
public enum PlaybackOperationCode
{
    [JsonStringEnumMemberName("applied")] Applied,
    [JsonStringEnumMemberName("unsupported")] Unsupported,
    [JsonStringEnumMemberName("invalid_state")] InvalidState,
    [JsonStringEnumMemberName("stale_target")] StaleTarget,
    [JsonStringEnumMemberName("expired")] Expired,
    [JsonStringEnumMemberName("execution_failed")] ExecutionFailed,
}

[JsonConverter(typeof(JsonStringEnumConverter<PlaybackCommandKind>))]
public enum PlaybackCommandKind
{
    [JsonStringEnumMemberName("load")] Load,
    [JsonStringEnumMemberName("play")] Play,
    [JsonStringEnumMemberName("pause")] Pause,
    [JsonStringEnumMemberName("seek")] Seek,
    [JsonStringEnumMemberName("stop")] Stop,
}

public sealed record PlaybackCommandInput
{
    public required Guid OperationId { get; init; }
    public required PlaybackCommandKind Command { get; init; }
    public CoordinatedPlaybackItem? Item { get; init; }
    public long? PositionMilliseconds { get; init; }
    public PlaybackLoadMode? Mode { get; init; }
    public long? TargetRevision { get; init; }
}

public sealed record PlaybackCommand
{
    public required Guid OperationId { get; init; }
    public required PlaybackCommandKind Command { get; init; }
    public CoordinatedPlaybackItem? Item { get; init; }
    public long? PositionMilliseconds { get; init; }
    public PlaybackLoadMode? Mode { get; init; }
    public required PlaybackCommandStatus Status { get; init; }
    public PlaybackOperationCode? ResultCode { get; init; }
    public required string SenderDeviceName { get; init; }
    public required string CreatedAt { get; init; }
    public required string ExpiresAt { get; init; }
}

public sealed record PlaybackCommandList { public required IReadOnlyList<PlaybackCommand> Commands { get; init; } }

public sealed record PlaybackOperationResultInput
{
    public required PlaybackOperationStatus Status { get; init; }
    public required PlaybackOperationCode Code { get; init; }
}


public sealed record PlaybackRoomCreateInput
{
    public required CoordinatedPlaybackItem Item { get; init; }
    public required string State { get; init; }
    public required long PositionMilliseconds { get; init; }
    public required long DurationMilliseconds { get; init; }
}

public sealed record PlaybackRoomJoinInput { public required string Code { get; init; } }

public sealed record PlaybackRoomUpdateInput
{
    public required string State { get; init; }
    public required long PositionMilliseconds { get; init; }
    public required long DurationMilliseconds { get; init; }
    public required long ExpectedVersion { get; init; }
}

public sealed record PlaybackRoomMember
{
    public required string MemberId { get; init; }
    public required string Profile { get; init; }
    public required string DeviceName { get; init; }
    public required string Platform { get; init; }
    public required string Role { get; init; }
    public required bool Current { get; init; }
    public required string JoinedAt { get; init; }
    public required string LastSeenAt { get; init; }
}

public sealed record PlaybackRoom
{
    public required Guid Id { get; init; }
    public string? JoinCode { get; init; }
    public required CoordinatedPlaybackItem Item { get; init; }
    public required string State { get; init; }
    public required long PositionMilliseconds { get; init; }
    public required long DurationMilliseconds { get; init; }
    public required long Version { get; init; }
    public required string UpdatedAt { get; init; }
    public required string ExpiresAt { get; init; }
    public required IReadOnlyList<PlaybackRoomMember> Members { get; init; }

    public bool CurrentMemberIsHost => Members.FirstOrDefault(member => member.Current)?.Role == "host";

    public PlaybackRoom PreservingJoinCodeFrom(PlaybackRoom previous) =>
        Id == previous.Id && JoinCode is null && previous.JoinCode is not null
            ? this with { JoinCode = previous.JoinCode }
            : this;
}

public sealed record LocalRecommendationTitle
{
    public required Guid Id { get; init; }
    public required string MediaType { get; init; }
    public string? Title { get; init; }
    public string? PosterUrl { get; init; }
    public string? BackgroundUrl { get; init; }
    public string? ReleaseInfo { get; init; }
    public string? ResourceId { get; init; }
    public string? ResourceProvider { get; init; }
    public Guid? SourceAddonId { get; init; }
    public required IReadOnlyDictionary<string, string> ProviderIds { get; init; }
}

public sealed record LocalRecommendation
{
    public required LocalRecommendationTitle Item { get; init; }
    public required string Reason { get; init; }
    public required double Score { get; init; }
}

public sealed record LocalRecommendationPage { public required IReadOnlyList<LocalRecommendation> Items { get; init; } }


public sealed record PlaybackSource : IJsonOnDeserialized
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

    void IJsonOnDeserialized.OnDeserialized()
    {
        if (Id is null ||
            ManifestId is null ||
            Protocol is null)
        {
            throw new JsonException("The resolved playback source contains invalid required fields.");
        }
    }
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

public sealed record PlaybackDecision : IJsonOnDeserialized
{
    public required PlaybackDecisionReason Reason { get; init; }
    public required IReadOnlyList<PlaybackDecisionDetailReason> Reasons { get; init; }
    public required PlaybackTrackAction VideoAction { get; init; }
    public required PlaybackTrackAction AudioAction { get; init; }
    public required PlaybackSubtitleAction SubtitleAction { get; init; }
    public required bool ToneMapping { get; init; }
    public PlaybackDecisionSource? Source { get; init; }
    public PlaybackDecisionTarget? Target { get; init; }

    void IJsonOnDeserialized.OnDeserialized()
    {
        if (Reasons is null || Reasons.Count > 7 || Reasons.Distinct().Count() != Reasons.Count)
            throw new JsonException("Playback decision reasons are invalid.");
        if (Reason == PlaybackDecisionReason.DirectSupported && Reasons.Count != 0)
            throw new JsonException("Direct playback cannot contain incompatibility reasons.");
    }
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

public sealed record ProfileArchiveDocument : IJsonOnDeserialized
{
    public required int Version { get; init; }
    public required string ExportedAt { get; init; }
    public required ProfileArchiveIdentity Identity { get; init; }
    public required JsonElement Settings { get; init; }
    public required IReadOnlyList<JsonElement> Addons { get; init; }
    public required IReadOnlyList<JsonElement> Collections { get; init; }
    public required IReadOnlyList<JsonElement> Titles { get; init; }
    public required IReadOnlyList<JsonElement> Library { get; init; }
    public required IReadOnlyList<JsonElement> Progress { get; init; }
    public required IReadOnlyList<JsonElement> Favorites { get; init; }
    public required IReadOnlyList<JsonElement> UserData { get; init; }
    public required IReadOnlyList<JsonElement> ContinueDismissals { get; init; }
    public required IReadOnlyList<JsonElement> TrackingPreferences { get; init; }
    [JsonExtensionData] public IDictionary<string, JsonElement>? AdditionalProperties { get; init; }

    void IJsonOnDeserialized.OnDeserialized()
    {
        if (Version != 2 || Identity is null || Settings.ValueKind != JsonValueKind.Object ||
            Addons is null || Collections is null || Titles is null || Library is null || Progress is null ||
            Favorites is null || UserData is null || ContinueDismissals is null || TrackingPreferences is null ||
            AdditionalProperties is { Count: > 0 })
            throw new JsonException("The profile archive is not a strict version 2 document.");
    }
}

public sealed record ProfileArchiveIdentity
{
    public required string Name { get; init; }
    public string? Description { get; init; }
    public required bool IsChild { get; init; }
    public required JsonElement Avatar { get; init; }
}

public sealed record ProfileArchiveCreateInput
{
    public required Guid CategoryId { get; init; }
    public required ProfileArchiveDocument Archive { get; init; }
}

public sealed record ProfileArchiveSectionReport
{
    public required string Section { get; init; }
    public required int Created { get; init; }
    public required int Updated { get; init; }
    public required int Unchanged { get; init; }
}

public sealed record ProfileArchiveImportReport
{
    public required string Mode { get; init; }
    public required Guid ProfileId { get; init; }
    public required IReadOnlyList<ProfileArchiveSectionReport> Sections { get; init; }
    public required int TrackingAccountsUpdated { get; init; }
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
    public string? MappingProvider { get; init; }
    public string? EpisodeOrderId { get; init; }
    public string? MetadataSeasonId { get; init; }
    public string? Title { get; init; }
    public string? PosterUrl { get; init; }
    public string? BackgroundUrl { get; init; }
    public string? ReleaseInfo { get; init; }
    public string? ResourceId { get; init; }
    public string? ResourceProvider { get; init; }
    public string? EpisodeTitle { get; init; }
    public string? EpisodeStillUrl { get; init; }
    public string? EpisodeAirDate { get; init; }
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
