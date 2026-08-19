using System.Text.Json;
using Rivune.App;
using Rivune.Windows;
using Xunit;

namespace Rivune.Windows.Tests;

public sealed class ViewerPresentationTests
{
    private static readonly Guid AddonId = Guid.Parse("11111111-1111-4111-8111-111111111111");

    [Fact]
    public void CollectionMappingPreservesPlaybackIdentityAndArtwork()
    {
        var item = new CollectionItem
        {
            Id = "tt1234567",
            MediaType = "movie",
            Title = "Example",
            PosterUrl = "https://rivune.test/poster.jpg",
            BackgroundUrl = "https://rivune.test/backdrop.jpg",
            LogoUrl = "https://rivune.test/logo.png",
            Description = "Overview",
            ReleaseInfo = "2026",
            Released = "2026-01-01",
            VoteAverage = 8.5,
            ExternalIds = new Dictionary<string, string> { ["imdb"] = "tt1234567" },
            Sources =
            [
                new CollectionSourceReference
                {
                    Id = Guid.NewGuid(),
                    Kind = CollectionSourceKind.AddonCatalog,
                    Title = "Catalog",
                    AddonId = AddonId,
                    ManifestId = "manifest",
                    CatalogId = "popular",
                },
            ],
        };

        var target = item.ToMediaTarget();

        Assert.Equal("tt1234567", target.ResourceId);
        Assert.Equal(AddonId, target.SourceAddonId);
        Assert.Equal("popular", target.SourceCatalogId);
        Assert.Equal("https://rivune.test/logo.png", target.LogoUrl);
        Assert.Equal(8.5, target.Rating);
    }

    [Fact]
    public void EpisodeMappingUsesCanonicalSeriesIdentityWithoutCatalogScope()
    {
        var series = new Series
        {
            Id = Guid.Parse("37ff320d-0e03-4baf-8fdf-b7e6843f4da8"),
            MediaType = MediaType.Series,
            Name = "Silo",
            OriginalName = "Silo",
            OriginalLanguage = "en",
            Overview = string.Empty,
            Genres = [],
            Cast = [],
            VoteAverage = 0,
            VoteCount = 0,
            Seasons = [],
            Aliases = [],
            EpisodeOrders = [],
            MappingProvider = SeriesMappingProvider.Tmdb,
            ExternalIds = new Dictionary<string, string> { ["imdb"] = "tt14688458", ["tmdb"] = "125988" },
        };
        var episode = new Episode
        {
            Id = Guid.Parse("3da37ffd-6174-44bc-9ba4-45c227ed3882"),
            MediaType = MediaType.Episode,
            SeasonId = "512220",
            Name = "Qui êtes-vous ?",
            Overview = string.Empty,
            SeasonNumber = 3,
            EpisodeNumber = 1,
            VoteAverage = 0,
            VoteCount = 0,
            ExternalIds = new Dictionary<string, string> { ["tmdb"] = "7173957" },
        };

        var target = episode.ToMediaTarget(series, episode.Id.ToString("D"));

        Assert.Equal("tt14688458:3:1", target.ResourceId);
        Assert.Equal(episode.Id, target.TitleId);
        Assert.Null(target.SourceAddonId);
    }

    [Fact]
    public void LibraryMappingPreservesCanonicalTitleWithoutProviderIdentity()
    {
        var titleId = Guid.Parse("22222222-2222-4222-8222-222222222222");
        var target = new LibraryItem
        {
            TitleId = titleId,
            MediaType = TitleMediaType.Tv,
            Title = "Live",
            Available = true,
            AddedAt = "2026-08-01T00:00:00Z",
            UpdatedAt = "2026-08-01T00:00:00Z",
        }.ToMediaTarget();

        Assert.Equal(titleId, target.TitleId);
        Assert.Equal("tv", target.MediaType);
        Assert.Equal(titleId.ToString("D"), target.ResourceId);
        Assert.Null(target.Provider);
        Assert.Empty(target.ExternalIds);
    }

    [Fact]
    public void ContinueEpisodeMappingUsesPayloadSnapshotsWithoutMetadataRequests()
    {
        var titleId = Guid.Parse("33333333-3333-4333-8333-333333333333");
        var seriesId = Guid.Parse("44444444-4444-4444-8444-444444444444");
        var seasonId = Guid.Parse("55555555-5555-4555-8555-555555555555");
        var target = new ContinueWatchingItem
        {
            TitleId = titleId,
            MediaType = PlaybackProgressMediaType.Episode,
            SeriesId = seriesId,
            SeasonId = seasonId,
            SeasonNumber = 2,
            EpisodeNumber = 3,
            Title = "Snapshot Series",
            PosterUrl = "/series-poster.jpg",
            BackgroundUrl = "/series-background.jpg",
            ReleaseInfo = "2026",
            ResourceId = "tt1234567:2:3",
            ResourceProvider = "imdb",
            EpisodeTitle = "Snapshot Episode",
            EpisodeStillUrl = "/episode-still.jpg",
            EpisodeAirDate = "2026-08-12",
            PositionSeconds = 125,
            DurationSeconds = 1800,
            Version = 1,
            Reason = ContinueWatchingReason.Resume,
            LastWatchedAt = "2026-08-12T00:00:00Z",
        }.ToMediaTarget();

        Assert.Equal("Snapshot Series · Snapshot Episode", target.Title);
        Assert.Equal("tt1234567:2:3", target.Id);
        Assert.Equal("tt1234567:2:3", target.ResourceId);
        Assert.Equal("imdb", target.Provider);
        Assert.Equal("tt1234567:2:3", target.ExternalIds["imdb"]);
        Assert.Equal("/episode-still.jpg", target.PosterUrl);
        Assert.Equal("/episode-still.jpg", target.BackgroundUrl);
        Assert.Equal("2026-08-12", target.ReleaseInfo);
        Assert.Equal("2026-08-12", target.Released);
        Assert.Equal(seriesId, target.SeriesId);
        Assert.Equal(seasonId.ToString("D"), target.SeasonId);
        Assert.Equal(125, target.ResumePositionSeconds);
        Assert.Equal(1800, target.DurationSeconds);
    }

    [Fact]
    public void ContinueMappingFallsBackToStableIdsAndEpisodeNumbers()
    {
        var titleId = Guid.Parse("66666666-6666-4666-8666-666666666666");
        var target = new ContinueWatchingItem
        {
            TitleId = titleId,
            MediaType = PlaybackProgressMediaType.Episode,
            SeasonNumber = 1,
            EpisodeNumber = 7,
            Title = " ",
            EpisodeTitle = " ",
            ResourceId = " ",
            PositionSeconds = 0,
            DurationSeconds = 0,
            Version = 1,
            Reason = ContinueWatchingReason.NextEpisode,
            LastWatchedAt = "2026-08-12T00:00:00Z",
        }.ToMediaTarget();

        Assert.Equal(titleId.ToString("D"), target.Id);
        Assert.Equal(titleId.ToString("D"), target.ResourceId);
        Assert.Equal("Series · S01E07", target.Title);
        Assert.Empty(target.ExternalIds);
    }

    [Fact]
    public void ContinueMovieMappingUsesMovieSnapshots()
    {
        var target = new ContinueWatchingItem
        {
            TitleId = Guid.Parse("77777777-7777-4777-8777-777777777777"),
            MediaType = PlaybackProgressMediaType.Movie,
            Title = "Snapshot Movie",
            PosterUrl = "/movie-poster.jpg",
            BackgroundUrl = "/movie-background.jpg",
            ReleaseInfo = "2025",
            ResourceId = "tt7654321",
            ResourceProvider = "imdb",
            PositionSeconds = 50,
            DurationSeconds = 100,
            Version = 1,
            Reason = ContinueWatchingReason.Resume,
            LastWatchedAt = "2026-08-12T00:00:00Z",
        }.ToMediaTarget();

        Assert.Equal("Snapshot Movie", target.Title);
        Assert.Equal("movie", target.MediaType);
        Assert.Equal("/movie-poster.jpg", target.PosterUrl);
        Assert.Equal("/movie-background.jpg", target.BackgroundUrl);
        Assert.Equal("2025", target.ReleaseInfo);
        Assert.Null(target.Released);
    }

    [Fact]
    public void AddonSearchMappingMatchesAndroidScopeAndDeduplication()
    {
        using var document = JsonDocument.Parse("""
            {
              "metas": [
                { "id": "movie:1", "type": "movie", "name": "First", "poster": "/poster" },
                { "id": "movie:1", "type": "movie", "name": "Duplicate" },
                { "id": "channel:1", "type": "tv", "name": "Channel", "logo": "/logo", "available": false }
              ]
            }
            """);
        var descriptors = new[]
        {
            new AddonCatalogDescriptor
            {
                AddonId = AddonId,
                AddonName = "Example addon",
                ManifestId = "manifest",
                Position = 0,
                Catalog = new StremioManifestCatalog { Type = "movie", Id = "search", Name = "Search" },
                AddonCatalog = false,
                Searchable = true,
            },
        };
        var batch = new AddonResourceBatch
        {
            Results =
            [
                new AddonResourceResult
                {
                    AddonId = AddonId,
                    ManifestId = "manifest",
                    Resource = "catalog",
                    Type = "movie",
                    Id = "search",
                    Payload = document.RootElement.Clone(),
                    Cache = new AddonCachePolicy(),
                },
            ],
            Errors = [],
        };

        var targets = batch.ToMediaTargets(descriptors);

        Assert.Equal(2, targets.Count);
        Assert.Equal("First", targets[0].Title);
        Assert.Equal("/poster", targets[0].PosterUrl);
        Assert.Equal("tv", targets[1].MediaType);
        Assert.Equal("/logo", targets[1].PosterUrl);
        Assert.Equal(AddonId, targets[1].SourceAddonId);
        Assert.False(targets[1].Available);
    }

    [Fact]
    public void FullPageDetectionUsesAnyProviderPage()
    {
        using var shortPayload = JsonDocument.Parse("""{"metas":[{"id":"1"}]}""");
        using var fullPayload = JsonDocument.Parse("""{"metas":[{"id":"1"},{"id":"2"}]}""");
        var result = new AddonResourceBatch
        {
            Results =
            [
                Result(shortPayload.RootElement.Clone()),
                Result(fullPayload.RootElement.Clone()),
            ],
            Errors = [],
        };

        Assert.True(result.HasFullPage(2));
        Assert.False(result.HasFullPage(3));
    }

    [Theory]
    [InlineData("series", false, true, true, true, false)]
    [InlineData("series", true, true, true, false, false)]
    [InlineData("episode", true, false, true, false, true)]
    public void DetailActionsMatchMediaHierarchy(
        string mediaType,
        bool hasSeason,
        bool expectedTrailer,
        bool expectedWatched,
        bool expectedLibrary,
        bool expectedPlay)
    {
        var actions = DetailActionPolicy.For(mediaType, hasSeason, automaticallyShowSources: false);

        Assert.Equal(expectedTrailer, actions.Trailer);
        Assert.Equal(expectedWatched, actions.Watched);
        Assert.Equal(expectedLibrary, actions.Library);
        Assert.Equal(expectedPlay, actions.Play);
    }

    [Fact]
    public void EpisodePlayIsHiddenWhenSourcesOpenAutomatically()
    {
        var actions = DetailActionPolicy.For("episode", hasSeason: true, automaticallyShowSources: true);

        Assert.False(actions.Play);
    }

    [Theory]
    [InlineData("2007-11-27", "Nov 27, 2007")]
    [InlineData("2007", "2007")]
    public void ReleaseDatesUseAndroidStyle(string value, string expected)
    {
        Assert.Equal(expected, ViewerDatePresentation.ReleaseDate(value));
    }

    private static AddonResourceResult Result(JsonElement payload) => new()
    {
        AddonId = AddonId,
        ManifestId = "manifest",
        Resource = "catalog",
        Type = "movie",
        Id = "search",
        Payload = payload,
        Cache = new AddonCachePolicy(),
    };
}
