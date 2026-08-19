using System.Globalization;
using System.Security.Cryptography;
using System.Text;
using Rivune.Windows;

namespace Rivune.App;

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

    public static string EpisodeResourceId(Series series, Episode episode, string fallbackResourceId)
    {
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
}
