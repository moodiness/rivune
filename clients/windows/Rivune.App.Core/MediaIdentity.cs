using System.Globalization;
using System.Security.Cryptography;
using System.Text;
using Rivune.Windows;

namespace Rivune.App;

internal sealed record MediaVariantContext(
    SeriesMappingProvider MappingProvider,
    string EpisodeOrderId,
    string MetadataSeasonId);

internal static class MediaIdentity
{
    public static string InferProvider(string resourceId, bool hasAddonSource)
    {
        var separator = resourceId.IndexOf(':');
        if (separator > 0) return resourceId[..separator].ToLowerInvariant();
        if (resourceId.StartsWith("tt", StringComparison.OrdinalIgnoreCase)) return "imdb";
        return hasAddonSource ? "addon" : "tmdb";
    }

    public static string InferExternalId(string resourceId)
    {
        var separator = resourceId.IndexOf(':');
        return separator >= 0 && separator + 1 < resourceId.Length ? resourceId[(separator + 1)..] : resourceId;
    }

    public static string AddonExternalId(Guid addonId, string mediaType, string resourceId)
    {
        var scoped = Encoding.UTF8.GetBytes($"{addonId:D}\0{mediaType}\0{resourceId}");
        return $"sha256:{Convert.ToHexStringLower(SHA256.HashData(scoped))}";
    }

    public static string? NormalizeReleaseDate(string? value)
    {
        if (string.IsNullOrWhiteSpace(value)) return null;
        if (DateOnly.TryParseExact(value, "yyyy-MM-dd", CultureInfo.InvariantCulture, DateTimeStyles.None, out var date))
            return date.ToString("yyyy-MM-dd", CultureInfo.InvariantCulture);
        return DateTimeOffset.TryParse(value, CultureInfo.InvariantCulture, DateTimeStyles.AllowWhiteSpaces, out var timestamp)
            ? timestamp.ToString("yyyy-MM-dd", CultureInfo.InvariantCulture)
            : null;
    }
    public static MediaVariantContext? VariantContext(MediaTarget target)
    {
        var episodeOrderId = NonEmpty(target.EpisodeOrderId);
        var metadataSeasonId = NonEmpty(target.MetadataSeasonId);
        return target.MappingProvider == SeriesMappingProvider.Tvdb &&
               episodeOrderId is not null &&
               metadataSeasonId is not null
            ? new MediaVariantContext(SeriesMappingProvider.Tvdb, episodeOrderId, metadataSeasonId)
            : null;
    }

    public static MediaVariantContext? EpisodeVariantContext(
        Series series,
        Episode episode,
        MediaTarget? fallback)
    {
        var selectedOrderId = NonEmpty(series.SelectedEpisodeOrderId);
        var selectedOrder = selectedOrderId is null
            ? null
            : series.EpisodeOrders.FirstOrDefault(order =>
                string.Equals(order.Id, selectedOrderId, StringComparison.Ordinal));
        if (selectedOrder is not null)
        {
            if (string.Equals(selectedOrder.Type.Trim(), "official", StringComparison.OrdinalIgnoreCase))
                return null;
            return new MediaVariantContext(
                SeriesMappingProvider.Tvdb,
                selectedOrderId!,
                episode.SeasonId);
        }

        var inherited = fallback is null ? null : VariantContext(fallback);
        return inherited is null
            ? null
            : inherited with { MetadataSeasonId = episode.SeasonId };
    }

    public static string? DetailSeasonId(MediaTarget target, Series series)
    {
        var variant = VariantContext(target);
        if (variant is not null) return variant.MetadataSeasonId;
        return series.Seasons.FirstOrDefault(season =>
                   string.Equals(season.Id, target.SeasonId, StringComparison.Ordinal))?.Id
               ?? series.Seasons.FirstOrDefault(season =>
                   season.SeasonNumber == target.SeasonNumber)?.Id;
    }

    public static bool CanLoadCanonicalMarkers(MediaTarget target) =>
        VariantContext(target) is null;


    public static string EpisodeResourceId(
        Series series,
        Episode episode,
        string fallbackResourceId,
        string? episodeOrderId = null)
    {
        if (!string.IsNullOrWhiteSpace(episodeOrderId) &&
            episode.ExternalIds.TryGetValue("tvdb", out var orderedEpisodeTvdb) &&
            !string.IsNullOrWhiteSpace(orderedEpisodeTvdb))
            return $"tvdb:{orderedEpisodeTvdb}";
        if (series.ExternalIds.TryGetValue("imdb", out var seriesImdb) && !string.IsNullOrWhiteSpace(seriesImdb))
            return $"{seriesImdb}:{episode.SeasonNumber}:{episode.EpisodeNumber}";
        if (episode.ExternalIds.TryGetValue("imdb", out var episodeImdb) && !string.IsNullOrWhiteSpace(episodeImdb))
            return episodeImdb;
        if (episode.ExternalIds.TryGetValue("tvdb", out var episodeTvdb) && !string.IsNullOrWhiteSpace(episodeTvdb))
            return $"tvdb:{episodeTvdb}";
        if (series.ExternalIds.TryGetValue("tmdb", out var seriesTmdb) && !string.IsNullOrWhiteSpace(seriesTmdb))
            return $"tmdb:{seriesTmdb}:{episode.SeasonNumber}:{episode.EpisodeNumber}";
        return $"{fallbackResourceId}:{episode.SeasonNumber}:{episode.EpisodeNumber}";
    }

    private static string? NonEmpty(string? value) =>
        string.IsNullOrWhiteSpace(value) ? null : value.Trim();
}
