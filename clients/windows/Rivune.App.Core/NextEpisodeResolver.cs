using Rivune.Windows;

namespace Rivune.App;

internal static class NextEpisodeResolver
{
    public static async Task<Episode?> ResolveAsync(
        Series series,
        Season currentSeason,
        Guid currentEpisodeId,
        Func<string, Task<Season>> loadSeason)
    {
        var orderedSeasons = series.Seasons
            .OrderBy(summary => summary.SeasonNumber)
            .ThenBy(summary => summary.Id, StringComparer.Ordinal)
            .ToArray();
        var currentSeasonIndex = Array.FindIndex(
            orderedSeasons,
            summary => string.Equals(summary.Id, currentSeason.Id, StringComparison.Ordinal));
        if (currentSeasonIndex < 0) return null;

        var currentEpisodeIndex = -1;
        for (var index = 0; index < currentSeason.Episodes.Count; index++)
        {
            if (currentSeason.Episodes[index].Id != currentEpisodeId) continue;
            currentEpisodeIndex = index;
            break;
        }
        if (currentEpisodeIndex < 0) return null;
        if (currentEpisodeIndex + 1 < currentSeason.Episodes.Count)
            return currentSeason.Episodes[currentEpisodeIndex + 1];

        for (var index = currentSeasonIndex + 1; index < orderedSeasons.Length; index++)
        {
            var summary = orderedSeasons[index];
            if (summary.EpisodeCount <= 0) continue;
            var season = await loadSeason(summary.Id);
            if (season.Episodes.Count > 0) return season.Episodes[0];
        }

        return null;
    }
}
