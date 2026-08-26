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

internal static class PlaybackDecisionPresentation
{
    public static IReadOnlyList<string> Reasons(PlaybackDecision? decision)
    {
        if (decision is null || decision.Reasons.Count == 0) return [];
        return decision.Reasons.Select(reason => reason switch
        {
            PlaybackDecisionDetailReason.ContainerNotSupported => "The source container is not supported directly.",
            PlaybackDecisionDetailReason.VideoCodecNotSupported => "The video codec is not supported directly.",
            PlaybackDecisionDetailReason.AudioCodecNotSupported => "The audio codec is not supported directly.",
            PlaybackDecisionDetailReason.ResolutionLimit => "The source exceeds this network's resolution limit.",
            PlaybackDecisionDetailReason.BitrateLimit => "The source exceeds this network's bitrate limit.",
            PlaybackDecisionDetailReason.HdrNotSupported => "HDR conversion is required for this display.",
            _ => throw new ArgumentOutOfRangeException(nameof(reason)),
        }).ToArray();
    }

    public static string Summary(PlaybackDecision? decision) => string.Join(" ", Reasons(decision));
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

internal static class PlaybackCoordinationPolicy
{
    private const long MaximumPositionMilliseconds = 7L * 24 * 60 * 60 * 1_000;

    public static string NormalizeRoomCode(string value) =>
        new string(value.Where(character => character is not (' ' or '-')).ToArray()).ToUpperInvariant();

    public static long ForwardSeekPosition(long positionMilliseconds, long durationMilliseconds)
    {
        var current = Math.Clamp(positionMilliseconds, 0, MaximumPositionMilliseconds);
        var target = Math.Min(MaximumPositionMilliseconds, current + 10_000);
        return durationMilliseconds > 0 ? Math.Max(current, Math.Min(target, durationMilliseconds)) : target;
    }

    public static bool IsTerminalRemoteLoadFailure(Exception exception)
    {
        if (exception is RivuneServerException serverException)
            return serverException.StatusCode is >= 400 and < 500 and not 408 and not 409 and not 429;
        return exception is InvalidOperationException or RivuneApiException;
    }
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
        var provider = new[] { "tmdb", "imdb", "tvdb", "trakt" }
            .FirstOrDefault(candidate => item.ExternalIds.TryGetValue(candidate, out var value) && !string.IsNullOrWhiteSpace(value));
        Guid? titleId = Guid.TryParse(item.Id, out var parsedTitleId) ? parsedTitleId : null;
        return new MediaTarget
        {
            Id = item.Id,
            ResourceId = item.Id,
            MediaType = item.MediaType,
            Title = item.Title,
            TitleId = titleId,
            Provider = provider,
            ExternalId = provider is null ? null : item.ExternalIds[provider],
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

internal readonly record struct SemanticSearchOutcome(SemanticSearchPage? Page, bool Failed);
internal readonly record struct SemanticAddonSearchOutcome<T>(SemanticSearchOutcome Semantic, T Addon);

internal static class SemanticSearchPolicy
{
    public static readonly TimeSpan DefaultDeadline = TimeSpan.FromSeconds(12);

    public static async Task<SemanticSearchOutcome> FetchAsync(
        bool enabled,
        Func<CancellationToken, Task<SemanticSearchPage>> fetch,
        CancellationToken cancellationToken,
        TimeSpan? deadline = null)
    {
        if (!enabled) return new SemanticSearchOutcome(null, false);
        using var timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        timeout.CancelAfter(deadline ?? DefaultDeadline);
        try
        {
            var operation = fetch(timeout.Token);
            return new SemanticSearchOutcome(await operation.WaitAsync(timeout.Token).ConfigureAwait(false), false);
        }
        catch (OperationCanceledException) when (!cancellationToken.IsCancellationRequested)
        {
            return new SemanticSearchOutcome(null, true);
        }
        catch (Exception exception) when (exception is not OperationCanceledException)
        {
            cancellationToken.ThrowIfCancellationRequested();
            return new SemanticSearchOutcome(null, true);
        }
    }

    public static async Task<SemanticAddonSearchOutcome<T>> SearchAddonsAsync<T>(
        bool semanticEnabled,
        IReadOnlyList<string> configuredTypes,
        string originalQuery,
        Func<CancellationToken, Task<SemanticSearchPage>> semanticFetch,
        Func<IReadOnlyList<string>, string, CancellationToken, Task<T>> addonFetch,
        CancellationToken cancellationToken,
        TimeSpan? semanticDeadline = null,
        IProgress<T>? progress = null,
        IProgress<SemanticSearchOutcome>? semanticProgress = null)
    {
        using var speculativeCancellation = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        var speculative = addonFetch(configuredTypes, originalQuery, speculativeCancellation.Token);
        var speculativeProgress = ReportAddonWhenReadyAsync(speculative, progress, cancellationToken);
        SemanticSearchOutcome semantic;
        try
        {
            semantic = await FetchAsync(
                semanticEnabled,
                semanticFetch,
                cancellationToken,
                semanticDeadline).ConfigureAwait(false);
            semanticProgress?.Report(semantic);
        }
        catch
        {
            speculativeCancellation.Cancel();
            try
            {
                await speculative.ConfigureAwait(false);
            }
            catch
            {
            }
            await speculativeProgress.ConfigureAwait(false);
            throw;
        }

        var finalTypes = SelectTypes(configuredTypes, semantic.Page?.MediaTypes);
        var finalQuery = AddonQuery(originalQuery, semantic.Page);
        if (SamePlan(configuredTypes, originalQuery, finalTypes, finalQuery))
        {
            var addon = await speculative.ConfigureAwait(false);
            await speculativeProgress.ConfigureAwait(false);
            return new SemanticAddonSearchOutcome<T>(semantic, addon);
        }

        speculativeCancellation.Cancel();
        try
        {
            await speculative.ConfigureAwait(false);
        }
        catch (OperationCanceledException) when (!cancellationToken.IsCancellationRequested)
        {
        }
        catch (Exception exception) when (exception is not OperationCanceledException)
        {
            cancellationToken.ThrowIfCancellationRequested();
        }
        await speculativeProgress.ConfigureAwait(false);
        cancellationToken.ThrowIfCancellationRequested();
        var finalAddon = await addonFetch(finalTypes, finalQuery, cancellationToken).ConfigureAwait(false);
        cancellationToken.ThrowIfCancellationRequested();
        progress?.Report(finalAddon);
        return new SemanticAddonSearchOutcome<T>(semantic, finalAddon);
    }

    private static async Task ReportAddonWhenReadyAsync<T>(Task<T> operation, IProgress<T>? progress, CancellationToken cancellationToken)
    {
        if (progress is null) return;
        try
        {
            var value = await operation.ConfigureAwait(false);
            if (!cancellationToken.IsCancellationRequested) progress.Report(value);
        }
        catch
        {
        }
    }

    private static bool SamePlan(
        IReadOnlyList<string> leftTypes,
        string leftQuery,
        IReadOnlyList<string> rightTypes,
        string rightQuery) =>
        StringComparer.Ordinal.Equals(leftQuery, rightQuery) &&
        leftTypes.SequenceEqual(rightTypes, StringComparer.OrdinalIgnoreCase);


    public static IReadOnlyList<string> SelectTypes(
        IReadOnlyList<string> configuredTypes,
        IReadOnlyList<string>? inferredTypes)
    {
        if (inferredTypes is null || inferredTypes.Count == 0) return configuredTypes;
        var inferred = inferredTypes.ToHashSet(StringComparer.OrdinalIgnoreCase);
        var intersection = configuredTypes.Where(inferred.Contains).ToArray();
        return intersection.Length == 0 ? configuredTypes : intersection;
    }

    public static string AddonQuery(string originalQuery, SemanticSearchPage? semanticPage)
    {
        var residual = semanticPage?.TitleQuery.Trim();
        return residual is { Length: >= 2 } ? residual : originalQuery;
    }

    public static bool AccumulatePartial(bool previous, bool reset, bool current) =>
        current || !reset && previous;

    public static IReadOnlyList<MediaTarget> Merge(
        IEnumerable<MediaTarget> existing,
        IEnumerable<MediaTarget> direct,
        IEnumerable<MediaTarget> semantic)
    {
        var output = existing.ToList();
        var seen = output.SelectMany(TargetIdentity).ToHashSet(StringComparer.OrdinalIgnoreCase);
        foreach (var target in direct.Concat(semantic))
        {
            var identities = TargetIdentity(target);
            if (identities.Any(seen.Contains)) continue;
            seen.UnionWith(identities);
            output.Add(target);
        }
        return output;
    }

    public static IReadOnlyList<string> TargetIdentity(MediaTarget target)
    {
        var identities = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
        foreach (var (provider, value) in target.ExternalIds)
        {
            if (!string.IsNullOrWhiteSpace(provider) && !string.IsNullOrWhiteSpace(value))
                identities.Add($"{provider.Trim()}:{value.Trim()}");
        }
        var separator = target.Id.IndexOf(':');
        if (separator > 0 && separator < target.Id.Length - 1)
        {
            var provider = target.Id[..separator];
            if (provider.Equals("tmdb", StringComparison.OrdinalIgnoreCase) ||
                provider.Equals("imdb", StringComparison.OrdinalIgnoreCase) ||
                provider.Equals("tvdb", StringComparison.OrdinalIgnoreCase) ||
                provider.Equals("trakt", StringComparison.OrdinalIgnoreCase))
                identities.Add($"{provider}:{target.Id[(separator + 1)..]}");
        }
        else if (target.Id.StartsWith("tt", StringComparison.OrdinalIgnoreCase))
        {
            identities.Add($"imdb:{target.Id}");
        }
        if (identities.Count == 0) identities.Add(target.Identity());
        return identities.ToArray();
    }
}

internal static class ProgressiveSearchPolicy
{
    public const int MaximumTypes = 16;
    public const int MaximumConcurrency = 4;

    public static IReadOnlyList<string> NormalizeTypes(IEnumerable<string> types, out bool truncated)
    {
        ArgumentNullException.ThrowIfNull(types);
        var normalized = types
            .Where(type => !string.IsNullOrWhiteSpace(type))
            .Distinct(StringComparer.OrdinalIgnoreCase)
            .Take(MaximumTypes + 1)
            .ToArray();
        truncated = normalized.Length > MaximumTypes;
        return truncated ? normalized[..MaximumTypes] : normalized;
    }

    public static async Task<T?[]> CollectOrderedAsync<T>(
        IReadOnlyList<Func<CancellationToken, Task<T?>>> fetches,
        CancellationToken cancellationToken,
        IProgress<T?[]>? progress = null,
        int maximumConcurrency = MaximumConcurrency)
        where T : class
    {
        ArgumentOutOfRangeException.ThrowIfLessThan(maximumConcurrency, 1);
        var results = new T?[fetches.Count];
        using var concurrency = new SemaphoreSlim(Math.Min(maximumConcurrency, Math.Max(1, fetches.Count)));
        var pending = fetches
            .Select((fetch, index) => (Index: index, Task: RunBoundedAsync(fetch, concurrency, cancellationToken)))
            .ToList();

        while (pending.Count > 0)
        {
            var completedTask = await Task.WhenAny(pending.Select(entry => entry.Task)).ConfigureAwait(false);
            var completedIndex = pending.FindIndex(entry => ReferenceEquals(entry.Task, completedTask));
            var completed = pending[completedIndex];
            pending.RemoveAt(completedIndex);
            results[completed.Index] = await completed.Task.ConfigureAwait(false);
            cancellationToken.ThrowIfCancellationRequested();
            if (results[completed.Index] is not null) progress?.Report((T?[])results.Clone());
        }

        return results;
    }

    private static async Task<T?> RunBoundedAsync<T>(
        Func<CancellationToken, Task<T?>> fetch,
        SemaphoreSlim concurrency,
        CancellationToken cancellationToken) where T : class
    {
        await concurrency.WaitAsync(cancellationToken).ConfigureAwait(false);
        try { return await fetch(cancellationToken).ConfigureAwait(false); }
        finally { concurrency.Release(); }
    }
}

internal static class IncrementalViewerPresentation
{
    public static IReadOnlyList<MediaTarget> Additions<TItem>(
        IEnumerable<TItem> existingItems,
        Func<TItem, MediaTarget?> targetForItem,
        IEnumerable<MediaTarget> desiredTargets)
    {
        var identities = existingItems
            .Select(targetForItem)
            .Where(target => target is not null)
            .SelectMany(target => SemanticSearchPolicy.TargetIdentity(target!))
            .ToHashSet(StringComparer.OrdinalIgnoreCase);
        var additions = new List<MediaTarget>();
        foreach (var target in desiredTargets)
        {
            var targetIdentities = SemanticSearchPolicy.TargetIdentity(target);
            if (targetIdentities.Any(identities.Contains)) continue;
            identities.UnionWith(targetIdentities);
            additions.Add(target);
        }
        return additions;
    }
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
