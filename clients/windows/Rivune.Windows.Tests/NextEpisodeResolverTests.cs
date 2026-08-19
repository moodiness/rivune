using Rivune.App;
using Rivune.Windows;
using Xunit;

namespace Rivune.Windows.Tests;

public sealed class NextEpisodeResolverTests
{
    private static readonly Guid SeriesId = Guid.Parse("11111111-1111-4111-8111-111111111111");
    private static readonly Guid EpisodeOneId = Guid.Parse("22222222-2222-4222-8222-222222222221");
    private static readonly Guid EpisodeTwoId = Guid.Parse("22222222-2222-4222-8222-222222222222");
    private static readonly Guid EpisodeThreeId = Guid.Parse("22222222-2222-4222-8222-222222222223");

    [Fact]
    public async Task ReturnsNextEpisodeInCurrentSeasonWithoutLoadingAnotherSeason()
    {
        var currentSeason = CreateSeason("season-1", 1, EpisodeOneId, EpisodeTwoId);
        var loadCount = 0;

        var result = await NextEpisodeResolver.ResolveAsync(
            CreateSeries(CreateSummary("season-1", 1, 2)),
            currentSeason,
            EpisodeOneId,
            _ =>
            {
                loadCount++;
                return Task.FromResult(CreateSeason("unused", 2));
            });

        Assert.Equal(EpisodeTwoId, result?.Id);
        Assert.Equal(0, loadCount);
    }

    [Fact]
    public async Task ReturnsFirstEpisodeFromNextSeasonInDeterministicSeasonOrder()
    {
        var currentSeason = CreateSeason("season-1", 1, EpisodeOneId);
        var series = CreateSeries(
            CreateSummary("season-3", 3, 1),
            CreateSummary("season-2-b", 2, 1),
            CreateSummary("season-1", 1, 1),
            CreateSummary("season-2-a", 2, 1));
        var loadedIds = new List<string>();

        var result = await NextEpisodeResolver.ResolveAsync(series, currentSeason, EpisodeOneId, id =>
        {
            loadedIds.Add(id);
            return Task.FromResult(CreateSeason(id, 2, EpisodeThreeId));
        });

        Assert.Equal(EpisodeThreeId, result?.Id);
        Assert.Equal(["season-2-a"], loadedIds);
    }

    [Fact]
    public async Task SkipsEmptySeasonSummaries()
    {
        var currentSeason = CreateSeason("season-1", 1, EpisodeOneId);
        var series = CreateSeries(
            CreateSummary("season-4", 4, 1),
            CreateSummary("season-empty", 2, 0),
            CreateSummary("season-stale", 3, 1),
            CreateSummary("season-1", 1, 1));
        var loadedIds = new List<string>();
        var result = await NextEpisodeResolver.ResolveAsync(series, currentSeason, EpisodeOneId, id =>
        {
            loadedIds.Add(id);
            return Task.FromResult(id == "season-stale"
                ? CreateSeason(id, 3)
                : CreateSeason(id, 4, EpisodeThreeId));
        });

        Assert.Equal(EpisodeThreeId, result?.Id);
        Assert.Equal(["season-stale", "season-4"], loadedIds);
    }

    [Fact]
    public async Task ReturnsNullForTerminalEpisode()
    {
        var currentSeason = CreateSeason("season-1", 1, EpisodeOneId);

        var result = await NextEpisodeResolver.ResolveAsync(
            CreateSeries(CreateSummary("season-1", 1, 1)),
            currentSeason,
            EpisodeOneId,
            _ => throw new InvalidOperationException("No later season should be loaded."));

        Assert.Null(result);
    }

    [Fact]
    public async Task ReturnsNullForUnknownCurrentEpisode()
    {
        var currentSeason = CreateSeason("season-1", 1, EpisodeOneId);

        var result = await NextEpisodeResolver.ResolveAsync(
            CreateSeries(
                CreateSummary("season-1", 1, 1),
                CreateSummary("season-2", 2, 1)),
            currentSeason,
            Guid.Parse("99999999-9999-4999-8999-999999999999"),
            _ => throw new InvalidOperationException("Unknown episodes must not advance."));

        Assert.Null(result);
    }

    [Fact]
    public async Task ReturnsNullForUnknownCurrentSeason()
    {
        var currentSeason = CreateSeason("season-unknown", 1, EpisodeOneId);

        var result = await NextEpisodeResolver.ResolveAsync(
            CreateSeries(CreateSummary("season-1", 1, 1)),
            currentSeason,
            EpisodeOneId,
            _ => throw new InvalidOperationException("Unknown seasons must not advance."));

        Assert.Null(result);
    }

    private static Series CreateSeries(params SeasonSummary[] seasons) => new()
    {
        Id = SeriesId,
        MediaType = MediaType.Series,
        Name = "Series",
        OriginalName = "Series",
        OriginalLanguage = "en",
        Overview = string.Empty,
        Genres = [],
        Cast = [],
        VoteAverage = 0,
        VoteCount = 0,
        Seasons = seasons,
        Aliases = [],
        EpisodeOrders = [],
        MappingProvider = SeriesMappingProvider.Tmdb,
        ExternalIds = new Dictionary<string, string>(),
    };

    private static SeasonSummary CreateSummary(string id, int seasonNumber, int episodeCount) => new()
    {
        Id = id,
        MediaType = MediaType.Season,
        SeriesId = SeriesId,
        Name = $"Season {seasonNumber}",
        Overview = string.Empty,
        SeasonNumber = seasonNumber,
        EpisodeCount = episodeCount,
        VoteAverage = 0,
        ExternalIds = new Dictionary<string, string>(),
    };

    private static Season CreateSeason(string id, int seasonNumber, params Guid[] episodeIds) => new()
    {
        Id = id,
        MediaType = MediaType.Season,
        SeriesId = SeriesId,
        Name = $"Season {seasonNumber}",
        Overview = string.Empty,
        SeasonNumber = seasonNumber,
        VoteAverage = 0,
        Episodes = episodeIds.Select((episodeId, index) => CreateEpisode(episodeId, id, seasonNumber, index + 1)).ToArray(),
        ExternalIds = new Dictionary<string, string>(),
    };

    private static Episode CreateEpisode(Guid id, string seasonId, int seasonNumber, int episodeNumber) => new()
    {
        Id = id,
        MediaType = MediaType.Episode,
        SeasonId = seasonId,
        Name = $"Episode {episodeNumber}",
        Overview = string.Empty,
        SeasonNumber = seasonNumber,
        EpisodeNumber = episodeNumber,
        VoteAverage = 0,
        VoteCount = 0,
        ExternalIds = new Dictionary<string, string>(),
    };
}
