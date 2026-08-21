using System.Globalization;
using System.Text.Json;
using Rivune.Windows;

namespace Rivune.App;

internal enum ViewerTab
{
    Home,
    Search,
    Library,
    Calendar,
}

internal sealed record MediaTarget
{
    public required string Id { get; init; }
    public required string ResourceId { get; init; }
    public required string MediaType { get; init; }
    public required string Title { get; init; }
    public Guid? TitleId { get; init; }
    public string? Provider { get; init; }
    public string? ExternalId { get; init; }
    public IReadOnlyDictionary<string, string> ExternalIds { get; init; } = new Dictionary<string, string>();
    public Guid? SourceAddonId { get; init; }
    public string? SourceCatalogId { get; init; }
    public string? SourceName { get; init; }
    public string? PosterUrl { get; init; }
    public string? BackgroundUrl { get; init; }
    public string? LogoUrl { get; init; }
    public string? Description { get; init; }
    public string? ReleaseInfo { get; init; }
    public string? Released { get; init; }
    public string? Country { get; init; }
    public string? Language { get; init; }
    public string? Category { get; init; }
    public bool Available { get; init; } = true;
    public Guid? SeriesId { get; init; }
    public string? SeasonId { get; init; }
    public int? SeasonNumber { get; init; }
    public int? EpisodeNumber { get; init; }
    public string? SeriesImdbId { get; init; }
    public int? RuntimeMinutes { get; init; }
    public double? Rating { get; init; }
    public int ResumePositionSeconds { get; init; }
    public int DurationSeconds { get; init; }
}

internal static class PlaybackCoordinationMapping
{
    public static CoordinatedPlaybackItem CoordinatedItem(this MediaTarget target, Guid titleId, string? title = null) => new()
    {
        TitleId = titleId,
        MediaType = target.MediaType,
        ResourceId = target.ResourceId,
        SourceAddonId = target.SourceAddonId,
        Title = title ?? target.Title,
        PosterUrl = target.PosterUrl,
    };

    public static MediaTarget MediaTarget(this CoordinatedPlaybackItem item) => new()
    {
        Id = item.ResourceId,
        ResourceId = item.ResourceId,
        MediaType = item.MediaType,
        Title = item.Title,
        TitleId = item.TitleId,
        SourceAddonId = item.SourceAddonId,
        PosterUrl = item.PosterUrl,
    };
}

internal static class MediaTargetMapping
{
    private static readonly HashSet<string> GlobalMediaTypes = new(StringComparer.OrdinalIgnoreCase)
    {
        "movie", "series", "episode",
    };

    public static MediaTarget ToMediaTarget(this CollectionItem item)
    {
        var addonSource = item.Sources.FirstOrDefault(source => source.AddonId is not null);
        return new MediaTarget
        {
            Id = item.Id,
            ResourceId = item.Id,
            MediaType = item.MediaType,
            Title = item.Title,
            ExternalIds = item.ExternalIds,
            SourceAddonId = addonSource?.AddonId,
            SourceCatalogId = addonSource?.CatalogId,
            SourceName = addonSource?.Title,
            PosterUrl = item.PosterUrl,
            BackgroundUrl = item.BackgroundUrl,
            LogoUrl = item.LogoUrl,
            Description = item.Description,
            ReleaseInfo = item.ReleaseInfo,
            Released = item.Released,
            Rating = item.VoteAverage,
        };
    }

    public static MediaTarget ToMediaTarget(this LibraryItem item) => new()
    {
        Id = item.ResourceId ?? item.ExternalId ?? item.TitleId.ToString("D"),
        ResourceId = item.ResourceId ?? item.ExternalId ?? item.TitleId.ToString("D"),
        MediaType = item.MediaType switch
        {
            TitleMediaType.Movie => "movie",
            TitleMediaType.Series => "series",
            _ => "tv",
        },
        Title = string.IsNullOrWhiteSpace(item.Title) ? "Untitled" : item.Title,
        TitleId = item.TitleId,
        Provider = item.Provider,
        ExternalId = item.ExternalId,
        ExternalIds = item.Provider is not null && item.ExternalId is not null
            ? new Dictionary<string, string> { [item.Provider] = item.ExternalId }
            : new Dictionary<string, string>(),
        SourceAddonId = item.SourceAddonId,
        SourceCatalogId = item.SourceCatalogId,
        SourceName = item.SourceName,
        PosterUrl = item.PosterUrl,
        BackgroundUrl = item.BackgroundUrl,
        ReleaseInfo = item.ReleaseInfo,
        Country = item.Country,
        Language = item.Language,
        Category = item.Category,
        Available = item.Available,
    };

    public static MediaTarget ToMediaTarget(this LocalRecommendation recommendation)
    {
        var item = recommendation.Item;
        var resourceId = string.IsNullOrWhiteSpace(item.ResourceId) ? item.Id.ToString("D") : item.ResourceId;
        return new MediaTarget
        {
            Id = resourceId,
            ResourceId = resourceId,
            MediaType = item.MediaType,
            Title = string.IsNullOrWhiteSpace(item.Title) ? "Untitled" : item.Title,
            TitleId = item.Id,
            Provider = item.ResourceProvider,
            ExternalId = item.ResourceProvider is null ? null : resourceId,
            ExternalIds = item.ProviderIds,
            SourceAddonId = item.SourceAddonId,
            PosterUrl = item.PosterUrl,
            BackgroundUrl = item.BackgroundUrl,
            Description = recommendation.Reason,
            ReleaseInfo = item.ReleaseInfo,
        };
    }

    public static MediaTarget ToMediaTarget(this ContinueWatchingItem item)
    {
        var titleId = item.TitleId.ToString("D");
        var snapshotResourceId = NonEmpty(item.ResourceId);
        var resourceId = snapshotResourceId ?? titleId;
        var provider = NonEmpty(item.ResourceProvider);
        var posterUrl = NonEmpty(item.PosterUrl);
        var backgroundUrl = NonEmpty(item.BackgroundUrl);
        var releaseInfo = NonEmpty(item.ReleaseInfo);
        var episode = item.MediaType == PlaybackProgressMediaType.Episode;
        var title = episode
            ? $"{NonEmpty(item.Title) ?? SeriesFallback(item.SeriesId)} · {NonEmpty(item.EpisodeTitle) ?? EpisodeFallback(item.SeasonNumber, item.EpisodeNumber, item.TitleId)}"
            : NonEmpty(item.Title) ?? $"Movie {resourceId}";
        var episodeStillUrl = NonEmpty(item.EpisodeStillUrl);
        var episodeAirDate = NonEmpty(item.EpisodeAirDate);

        return new MediaTarget
        {
            Id = resourceId,
            ResourceId = resourceId,
            MediaType = episode ? "episode" : "movie",
            Title = title,
            TitleId = item.TitleId,
            Provider = provider,
            ExternalId = provider is null ? null : snapshotResourceId,
            ExternalIds = provider is null || snapshotResourceId is null
                ? new Dictionary<string, string>()
                : new Dictionary<string, string> { [provider] = snapshotResourceId },
            PosterUrl = episode ? episodeStillUrl ?? posterUrl : posterUrl,
            BackgroundUrl = episode ? episodeStillUrl ?? backgroundUrl ?? posterUrl : backgroundUrl,
            ReleaseInfo = episode ? episodeAirDate ?? releaseInfo : releaseInfo,
            Released = episode ? episodeAirDate : null,
            SeriesId = item.SeriesId,
            SeasonId = item.SeasonId?.ToString("D"),
            SeasonNumber = item.SeasonNumber,
            EpisodeNumber = item.EpisodeNumber,
            ResumePositionSeconds = item.PositionSeconds,
            DurationSeconds = item.DurationSeconds,
        };
    }

    private static string SeriesFallback(Guid? seriesId) =>
        seriesId is Guid value ? $"Series {value:D}" : "Series";

    private static string EpisodeFallback(int? seasonNumber, int? episodeNumber, Guid titleId) =>
        (seasonNumber, episodeNumber) switch
        {
            (int season, int episode) => $"S{season:00}E{episode:00}",
            (_, int episode) => $"Episode {episode}",
            _ => $"Episode {titleId:D}",
        };

    private static string? NonEmpty(string? value) =>
        string.IsNullOrWhiteSpace(value) ? null : value.Trim();

    public static MediaTarget ToMediaTarget(this Episode episode, Series series, string fallbackResourceId)
    {
        var resourceId = MediaIdentity.EpisodeResourceId(series, episode, fallbackResourceId);
        return new MediaTarget
        {
            Id = resourceId,
            ResourceId = resourceId,
            MediaType = "episode",
            Title = episode.Name,
            TitleId = episode.Id,
            Provider = series.MappingProvider == SeriesMappingProvider.Tvdb ? "tvdb" : "tmdb",
            ExternalIds = episode.ExternalIds,
            PosterUrl = episode.StillUrl ?? series.PosterUrl,
            BackgroundUrl = episode.BackdropUrl ?? episode.StillUrl ?? series.BackdropUrl,
            Description = episode.Overview,
            ReleaseInfo = episode.AirDate,
            SeriesId = series.Id,
            SeasonId = episode.SeasonId,
            SeasonNumber = episode.SeasonNumber,
            EpisodeNumber = episode.EpisodeNumber,
            SeriesImdbId = series.ExternalIds.GetValueOrDefault("imdb"),
            RuntimeMinutes = episode.RuntimeMinutes,
            Rating = episode.VoteAverage,
        };
    }

    public static IReadOnlyList<MediaTarget> ToMediaTargets(
        this AddonResourceBatch batch,
        IReadOnlyList<AddonCatalogDescriptor> descriptors)
    {
        var output = new List<MediaTarget>();
        var seen = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
        foreach (var result in batch.Results)
        {
            var descriptor = descriptors.FirstOrDefault(candidate =>
                candidate.AddonId == result.AddonId &&
                StringComparer.Ordinal.Equals(candidate.ManifestId, result.ManifestId) &&
                StringComparer.Ordinal.Equals(candidate.Catalog.Type, result.Type) &&
                StringComparer.Ordinal.Equals(candidate.Catalog.Id, result.Id));
            if (!result.Payload.TryGetProperty("metas", out var metas) || metas.ValueKind != JsonValueKind.Array) continue;
            foreach (var meta in metas.EnumerateArray())
            {
                var rawId = String(meta, "id");
                if (rawId is null) continue;
                var mediaType = String(meta, "type") ?? result.Type;
                var resourceId = String(meta, "resourceId") ?? rawId;
                var addonScoped = !GlobalMediaTypes.Contains(mediaType) || mediaType.Equals("tv", StringComparison.OrdinalIgnoreCase);
                var target = new MediaTarget
                {
                    Id = resourceId,
                    ResourceId = resourceId,
                    MediaType = mediaType,
                    Title = String(meta, "name", "title") ?? "Untitled",
                    SourceAddonId = addonScoped && Guid.TryParse(String(meta, "sourceAddonId"), out var scopedAddonId)
                        ? scopedAddonId
                        : result.AddonId,
                    SourceCatalogId = String(meta, "sourceCatalogId", "catalogId") ?? result.Id,
                    SourceName = String(meta, "sourceName", "source") ?? descriptor?.AddonName,
                    PosterUrl = String(meta, "poster", "posterUrl") ?? (mediaType == "tv" ? String(meta, "background", "backgroundUrl", "backdrop", "logo", "logoUrl") : null),
                    BackgroundUrl = String(meta, "background", "backgroundUrl", "backdrop") ?? (mediaType == "tv" ? String(meta, "poster", "posterUrl", "logo", "logoUrl") : null),
                    LogoUrl = String(meta, "logo", "logoUrl"),
                    Description = String(meta, "description", "overview"),
                    ReleaseInfo = String(meta, "releaseInfo"),
                    Released = String(meta, "released"),
                    Country = mediaType == "tv" ? String(meta, "country", "countryCode") : null,
                    Language = mediaType == "tv" ? String(meta, "language", "lang") : null,
                    Category = mediaType == "tv" ? String(meta, "category", "genre") : null,
                    Available = Boolean(meta, "available") ?? true,
                };
                var key = GlobalMediaTypes.Contains(mediaType)
                    ? $"{mediaType}:{target.Id}"
                    : $"{mediaType}:{target.SourceAddonId}:{target.ResourceId}";
                if (seen.Add(key)) output.Add(target);
            }
        }
        return output;
    }

    public static string Identity(this MediaTarget target) => GlobalMediaTypes.Contains(target.MediaType)
        ? $"{target.MediaType}\0{target.TitleId?.ToString("D") ?? target.Id}"
        : $"{target.MediaType}\0{target.SourceAddonId?.ToString("D")}\0{target.ResourceId}";

    public static bool HasFullPage(this AddonResourceBatch batch, int pageSize) =>
        batch.Results.Any(result => result.Payload.TryGetProperty("metas", out var metas) &&
                                    metas.ValueKind == JsonValueKind.Array &&
                                    metas.GetArrayLength() == pageSize);

    public static MediaTarget ToMediaTarget(this CalendarEvent item) => new()
    {
        Id = item.ResourceId ?? item.TitleId.ToString("D"),
        ResourceId = item.ResourceId ?? item.TitleId.ToString("D"),
        MediaType = item.MediaType == CalendarEventMediaType.Movie ? "movie" : "episode",
        Title = item.Title,
        TitleId = item.TitleId,
        Provider = item.ResourceProvider,
        PosterUrl = item.PosterUrl,
        ReleaseInfo = item.ReleaseDate,
        Released = item.ReleaseDate,
        SeriesId = item.SeriesId,
        SeasonId = item.SeasonId?.ToString("D"),
        SeasonNumber = item.SeasonNumber,
        EpisodeNumber = item.EpisodeNumber,
    };

    private static string? String(JsonElement element, params string[] names)
    {
        foreach (var name in names)
        {
            if (!element.TryGetProperty(name, out var value) || value.ValueKind != JsonValueKind.String) continue;
            var result = value.GetString()?.Trim();
            if (!string.IsNullOrEmpty(result)) return result;
        }
        return null;
    }

    private static bool? Boolean(JsonElement element, string name) =>
        element.TryGetProperty(name, out var value) && value.ValueKind is JsonValueKind.True or JsonValueKind.False
            ? value.GetBoolean()
            : null;
}

internal static class ViewerDatePresentation
{
    public static string ReleaseDate(string value)
    {
        if (!DateOnly.TryParseExact(value, "yyyy-MM-dd", CultureInfo.InvariantCulture, DateTimeStyles.None, out var date)) return value;
        return date.ToString("MMM d, yyyy", CultureInfo.GetCultureInfo("en-US"));
    }
}

internal readonly record struct DetailActionVisibility(
    bool Play,
    bool Trailer,
    bool Watched,
    bool Library);

internal static class DetailActionPolicy
{
    public static DetailActionVisibility For(string mediaType, bool hasSeason, bool automaticallyShowSources)
    {
        var episode = mediaType.Equals("episode", StringComparison.OrdinalIgnoreCase);
        var series = mediaType.Equals("series", StringComparison.OrdinalIgnoreCase);
        return new DetailActionVisibility(
            Play: !automaticallyShowSources && (episode || (!series && !hasSeason)),
            Trailer: !episode,
            Watched: true,
            Library: !episode && !hasSeason);
    }
}

internal sealed record MediaCardViewModel
{
    public required MediaTarget Target { get; init; }
    public required string Title { get; init; }
    public required string Metadata { get; init; }
    public required string ArtworkUrl { get; init; }
    public string AccessibilityName => Available ? Title : $"{Title}, unavailable";
    public bool Available => Target.Available;
}
