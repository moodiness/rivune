using System.Text.Json.Serialization;

namespace Rivune.Windows;

public static class RivuneProtocol
{
    public const int Version = 17;
}

public sealed record Discovery
{
    public required string Name { get; init; }
    public required string ServerVersion { get; init; }
    public required int ProtocolVersion { get; init; }
    public required string ApiBaseUrl { get; init; }
    public required bool SetupRequired { get; init; }
    public required string Timezone { get; init; }
    public required string InterfaceLanguage { get; init; }
}

public sealed record Device
{
    public Guid? Id { get; init; }
    public required string Name { get; init; }
    public required string Platform { get; init; }
}

public sealed record TokenPair
{
    public required string TokenType { get; init; }
    public required string AccessToken { get; init; }
    public required string AccessTokenExpiresAt { get; init; }
    public required string RefreshToken { get; init; }
    public required string RefreshTokenExpiresAt { get; init; }
    public required Guid SessionId { get; init; }
    public required Guid DeviceId { get; init; }
}

public sealed record Account
{
    public required AccountUser User { get; init; }
    public required AccountSession Session { get; init; }
    public required IReadOnlyList<Profile> Profiles { get; init; }
}

public sealed record AccountUser
{
    public required Guid Id { get; init; }
    public required string Username { get; init; }
    public required string Role { get; init; }
}

public sealed record AccountSession
{
    public required Guid Id { get; init; }
    public required Guid DeviceId { get; init; }
    public required ActiveProfileGrant? ActiveProfile { get; init; }
}

public sealed record ActiveProfileGrant
{
    public required Guid Id { get; init; }
    public required string ExpiresAt { get; init; }
}

public sealed record ProfileList
{
    public required IReadOnlyList<Profile> Profiles { get; init; }
}

public sealed record Profile
{
    public required Guid Id { get; init; }
    public required string Name { get; init; }
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
}

public sealed record TranscodingSettingsValues
{
    public bool? AllowTranscoding { get; init; }
    public string? Transcoding { get; init; }
}

public sealed record SettingsLayer
{
    public required int SchemaVersion { get; init; }
    public required TranscodingSettingsValues Settings { get; init; }
    public string? UpdatedAt { get; init; }
}

public sealed record EffectiveSettingsSources
{
    public string? AllowTranscoding { get; init; }
    public string? Transcoding { get; init; }
}

public sealed record EffectiveSettings
{
    public required int SchemaVersion { get; init; }
    public required TranscodingSettingsValues Settings { get; init; }
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
    public required double VoteAverage { get; init; }
    public required int VoteCount { get; init; }
    public required IReadOnlyList<SeasonSummary> Seasons { get; init; }
    public required IReadOnlyList<SeriesAlias> Aliases { get; init; }
    public required IReadOnlyList<EpisodeOrder> EpisodeOrders { get; init; }
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
}

public sealed record PlaybackCapabilities
{
    public required IReadOnlyList<string> StreamingProtocols { get; init; }
    public required IReadOnlyList<string> Containers { get; init; }
    public IReadOnlyList<string>? VideoCodecs { get; init; }
    public IReadOnlyList<string>? AudioCodecs { get; init; }
    public IReadOnlyList<string>? HdrFormats { get; init; }
    public IReadOnlyList<string>? ExternalPlayers { get; init; }
    public IReadOnlyList<string>? ProcessingModes { get; init; }
    public int? MaximumHeight { get; init; }
    public int? MaximumVideoBitrateKbps { get; init; }
    public int? MaximumAudioChannels { get; init; }
    public IReadOnlyList<string>? SubtitleModes { get; init; }
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
    public required string ManifestId { get; init; }
    public required int StreamIndex { get; init; }
    public required string Name { get; init; }
    public string? Description { get; init; }
    public string? Filename { get; init; }
    public required string Protocol { get; init; }
    public string? Container { get; init; }
    public required string ExpiresAt { get; init; }
}

[JsonConverter(typeof(JsonStringEnumConverter<PlaybackMode>))]
public enum PlaybackMode
{
    [JsonStringEnumMemberName("direct")]
    Direct,
    [JsonStringEnumMemberName("remux")]
    Remux,
    [JsonStringEnumMemberName("transcode_audio")]
    TranscodeAudio,
    [JsonStringEnumMemberName("transcode")]
    Transcode,
    [JsonStringEnumMemberName("youtube")]
    Youtube,
    [JsonStringEnumMemberName("external")]
    External,
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
    public required string Reason { get; init; }
    public required string VideoAction { get; init; }
    public required string AudioAction { get; init; }
    public required string SubtitleAction { get; init; }
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
    public int? VideoBitrateKbps { get; init; }
}

public sealed record PlaybackMediaTrack
{
    public required int Index { get; init; }
    public required string Type { get; init; }
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
    public string? Delivery { get; init; }
    public string? Url { get; init; }
}

public sealed record PlaybackProviderError
{
    public required Guid AddonId { get; init; }
    public required string ManifestId { get; init; }
    public required string Code { get; init; }
    public required string Message { get; init; }
}

public sealed record PlaybackActivity
{
    public required PlaybackActivitySummary Summary { get; init; }
    public required PlaybackMediaDiagnostics Diagnostics { get; init; }
    public required IReadOnlyList<PlaybackActivitySession> Sessions { get; init; }
    public required IReadOnlyList<PlaybackMediaJob> Jobs { get; init; }
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

public sealed record PlaybackMediaDiagnostics
{
    public required string VideoEncoder { get; init; }
    public required bool HardwareToneMap { get; init; }
}

public sealed record PlaybackActivitySession
{
    public required Guid Id { get; init; }
    public string? TitleId { get; init; }
    public string? ArtworkUrl { get; init; }
    public IReadOnlyDictionary<string, string>? ExternalIds { get; init; }
    public IReadOnlyDictionary<string, string>? ExternalIdMediaTypes { get; init; }
    public required string Title { get; init; }
    public required string MediaType { get; init; }
    public required string Mode { get; init; }
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

public sealed record PlaybackMediaJob
{
    public Guid? SessionId { get; init; }
    public required string AssetId { get; init; }
    public required string Mode { get; init; }
    public required string State { get; init; }
    public required bool Prewarming { get; init; }
    public double? ProgressPercent { get; init; }
    public double? Speed { get; init; }
    public required string CreatedAt { get; init; }
    public required string LastSeenAt { get; init; }
}

public sealed record ServerError
{
    public required string Code { get; init; }
    public required string Message { get; init; }
}
