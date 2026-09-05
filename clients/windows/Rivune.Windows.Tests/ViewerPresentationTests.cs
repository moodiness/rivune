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
    public void TvdbContinuationMappingRetainsCompleteOrderContextAndPersistedSeason()
    {
        var titleId = Guid.Parse("88888888-8888-4888-8888-888888888888");
        var seriesId = Guid.Parse("99999999-9999-4999-8999-999999999999");
        var persistedSeasonId = Guid.Parse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa");
        var metadataSeasonId = $"tvdb:{seriesId:D}:2112814";

        var target = new ContinueWatchingItem
        {
            TitleId = titleId,
            MediaType = PlaybackProgressMediaType.Episode,
            SeriesId = seriesId,
            SeasonId = persistedSeasonId,
            SeasonNumber = 1,
            EpisodeNumber = 2,
            MappingProvider = " TVDB ",
            EpisodeOrderId = " 2 ",
            MetadataSeasonId = $" {metadataSeasonId} ",
            Title = "Variant Series",
            ResourceId = "tvdb:10357450",
            ResourceProvider = "tvdb",
            EpisodeTitle = "DVD Episode 2",
            PositionSeconds = 480,
            DurationSeconds = 1860,
            Version = 3,
            Reason = ContinueWatchingReason.Resume,
            LastWatchedAt = "2026-09-04T12:00:00Z",
        }.ToMediaTarget();

        Assert.Equal("tvdb:10357450", target.ResourceId);
        Assert.Equal(SeriesMappingProvider.Tvdb, target.MappingProvider);
        Assert.Equal("2", target.EpisodeOrderId);
        Assert.Equal(metadataSeasonId, target.MetadataSeasonId);
        Assert.Equal(persistedSeasonId.ToString("D"), target.SeasonId);
        Assert.Equal(480, target.ResumePositionSeconds);
        Assert.Equal(1860, target.DurationSeconds);
    }

    [Theory]
    [InlineData("unknown", "2", "tvdb:series:2112814")]
    [InlineData("tvdb", null, "tvdb:series:2112814")]
    [InlineData("tvdb", "2", null)]
    public void IncompleteContinuationContextIsCleared(
        string? mappingProvider,
        string? episodeOrderId,
        string? metadataSeasonId)
    {
        var target = new ContinueWatchingItem
        {
            TitleId = Guid.Parse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"),
            MediaType = PlaybackProgressMediaType.Episode,
            MappingProvider = mappingProvider,
            EpisodeOrderId = episodeOrderId,
            MetadataSeasonId = metadataSeasonId,
            PositionSeconds = 0,
            DurationSeconds = 0,
            Version = 0,
            Reason = ContinueWatchingReason.Resume,
            LastWatchedAt = "2026-09-04T12:00:00Z",
        }.ToMediaTarget();

        Assert.Null(target.MappingProvider);
        Assert.Null(target.EpisodeOrderId);
        Assert.Null(target.MetadataSeasonId);
    }

    [Fact]
    public void VariantEpisodeMappingUsesRawTvdbIdentityAndIsolatesSiblingProgress()
    {
        var seriesId = Guid.Parse("cccccccc-cccc-4ccc-8ccc-cccccccccccc");
        var persistedSeasonId = Guid.Parse("dddddddd-dddd-4ddd-8ddd-dddddddddddd");
        var metadataSeasonId = $"tvdb:{seriesId:D}:2112814";
        var series = Series(
            seriesId,
            metadataSeasonId,
            selectedOrderId: "2",
            orderType: "dvd",
            mappingProvider: SeriesMappingProvider.Tvdb);
        var current = Episode(
            Guid.Parse("eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"),
            metadataSeasonId,
            2,
            "10357450");
        var sibling = Episode(
            Guid.Parse("ffffffff-ffff-4fff-8fff-ffffffffffff"),
            metadataSeasonId,
            3,
            "10357451");
        var source = new MediaTarget
        {
            Id = "tvdb:10357450",
            ResourceId = "tvdb:10357450",
            MediaType = "episode",
            Title = "DVD Episode 2",
            TitleId = current.Id,
            SeriesId = seriesId,
            MappingProvider = SeriesMappingProvider.Tvdb,
            EpisodeOrderId = "stale-order",
            MetadataSeasonId = metadataSeasonId,
            SeasonId = persistedSeasonId.ToString("D"),
            SeasonNumber = 1,
            EpisodeNumber = 2,
            ResumePositionSeconds = 480,
            DurationSeconds = 1860,
        };

        var currentTarget = current.ToMediaTarget(series, seriesId.ToString("D"), source);
        var siblingTarget = sibling.ToMediaTarget(series, seriesId.ToString("D"), source);

        Assert.Equal("tvdb:10357450", currentTarget.ResourceId);
        Assert.Equal("tvdb:10357451", siblingTarget.ResourceId);
        Assert.Equal(SeriesMappingProvider.Tvdb, siblingTarget.MappingProvider);
        Assert.Equal("2", siblingTarget.EpisodeOrderId);
        Assert.Equal(metadataSeasonId, siblingTarget.MetadataSeasonId);
        Assert.Equal(persistedSeasonId.ToString("D"), siblingTarget.SeasonId);
        Assert.Equal(480, currentTarget.ResumePositionSeconds);
        Assert.Equal(1860, currentTarget.DurationSeconds);
        Assert.Equal(0, siblingTarget.ResumePositionSeconds);
        Assert.Equal(0, siblingTarget.DurationSeconds);
    }

    [Fact]
    public void SelectedOfficialOrderClearsStaleVariantContext()
    {
        var seriesId = Guid.Parse("12121212-1212-4212-8212-121212121212");
        var episode = Episode(
            Guid.Parse("13131313-1313-4313-8313-131313131313"),
            "canonical-season-1",
            2,
            "10357450");
        var source = new MediaTarget
        {
            Id = "tvdb:10357450",
            ResourceId = "tvdb:10357450",
            MediaType = "episode",
            Title = "Stale DVD Episode",
            TitleId = episode.Id,
            MappingProvider = SeriesMappingProvider.Tvdb,
            EpisodeOrderId = "2",
            MetadataSeasonId = "tvdb:stale:2112814",
            SeasonId = Guid.Parse("14141414-1414-4414-8414-141414141414").ToString("D"),
            ResumePositionSeconds = 10,
            DurationSeconds = 20,
        };
        var series = Series(
            seriesId,
            "canonical-season-1",
            selectedOrderId: "official",
            orderType: "official",
            mappingProvider: SeriesMappingProvider.Tmdb);

        var target = episode.ToMediaTarget(series, seriesId.ToString("D"), source);

        Assert.Equal("tt12345678:1:2", target.ResourceId);
        Assert.Null(target.MappingProvider);
        Assert.Null(target.EpisodeOrderId);
        Assert.Null(target.MetadataSeasonId);
        Assert.Equal("canonical-season-1", target.SeasonId);
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

    [Fact]
    public async Task ProgressiveCollectionReportsFastTypeBeforeSlowTypeAndReturnsPlanOrder()
    {
        var movie = new TaskCompletionSource<string?>(TaskCreationOptions.RunContinuationsAsynchronously);
        var series = new TaskCompletionSource<string?>(TaskCreationOptions.RunContinuationsAsynchronously);
        var firstReport = new TaskCompletionSource<string?[]>(TaskCreationOptions.RunContinuationsAsynchronously);
        var reports = 0;

        var collection = ProgressiveSearchPolicy.CollectOrderedAsync<string>(
            [_ => movie.Task, _ => series.Task],
            TestContext.Current.CancellationToken,
            new InlineProgress<string?[]>(values =>
            {
                if (Interlocked.Increment(ref reports) == 1) firstReport.TrySetResult(values);
            }));

        series.TrySetResult("series");
        var progressive = await firstReport.Task.WaitAsync(TestContext.Current.CancellationToken);
        Assert.Null(progressive[0]);
        Assert.Equal("series", progressive[1]);
        Assert.False(collection.IsCompleted);

        movie.TrySetResult("movie");
        var ordered = await collection;
        Assert.Equal("movie", ordered[0]);
        Assert.Equal("series", ordered[1]);
    }

    [Theory]
    [InlineData(true, false, false, true)]
    [InlineData(true, true, false, false)]
    [InlineData(false, false, true, true)]
    public void PartialStateAccumulatesAcrossDisplayedPages(
        bool previous,
        bool reset,
        bool current,
        bool expected)
    {
        Assert.Equal(expected, SemanticSearchPolicy.AccumulatePartial(previous, reset, current));
    }

    [Fact]
    public void SemanticFirstDuplicateKeepsPublishedRepresentative()
    {
        var semantic = Target("semantic:1", "Semantic", "1");
        var direct = Target("tmdb:1", "Direct", "1");

        var merged = SemanticSearchPolicy.Merge([semantic], [direct], []);

        Assert.Same(semantic, Assert.Single(merged));
    }

    [Fact]
    public void IncrementalPresentationKeepsExistingObjectAndAppendsOnlyNewIdentity()
    {
        var published = Target("tmdb:1", "Published", "1");
        var existingControl = new object();
        var controls = new Dictionary<object, MediaTarget> { [existingControl] = published };
        var duplicate = Target("semantic:1", "Semantic duplicate", "1");
        var newTarget = Target("tmdb:2", "New", "2");
        var selectedControl = existingControl;
        var focusedControl = existingControl;

        var additions = IncrementalViewerPresentation.Additions(
            controls.Keys,
            item => controls[item],
            [duplicate, newTarget]);

        Assert.Same(existingControl, Assert.Single(controls.Keys));
        Assert.Same(newTarget, Assert.Single(additions));
        Assert.Same(existingControl, selectedControl);
        Assert.Same(existingControl, focusedControl);
    }

    private static Series Series(
        Guid id,
        string seasonId,
        string? selectedOrderId,
        string orderType,
        SeriesMappingProvider mappingProvider) => new()
    {
        Id = id,
        MediaType = MediaType.Series,
        Name = "Series",
        OriginalName = "Series",
        OriginalLanguage = "en",
        Overview = string.Empty,
        Genres = [],
        Cast = [],
        VoteAverage = 0,
        VoteCount = 0,
        Seasons =
        [
            new SeasonSummary
            {
                Id = seasonId,
                MediaType = MediaType.Season,
                SeriesId = id,
                Name = "Season 1",
                Overview = string.Empty,
                SeasonNumber = 1,
                EpisodeCount = 2,
                VoteAverage = 0,
                ExternalIds = new Dictionary<string, string>(),
            },
        ],
        Aliases = [],
        EpisodeOrders =
        [
            new EpisodeOrder
            {
                Id = selectedOrderId ?? "2",
                Name = "Selected",
                Type = orderType,
                IsDefault = true,
            },
        ],
        SelectedEpisodeOrderId = selectedOrderId,
        MappingProvider = mappingProvider,
        ExternalIds = new Dictionary<string, string> { ["imdb"] = "tt12345678" },
    };

    private static Episode Episode(Guid id, string seasonId, int number, string tvdbId) => new()
    {
        Id = id,
        MediaType = MediaType.Episode,
        SeasonId = seasonId,
        Name = $"Episode {number}",
        Overview = string.Empty,
        SeasonNumber = 1,
        EpisodeNumber = number,
        VoteAverage = 0,
        VoteCount = 0,
        ExternalIds = new Dictionary<string, string> { ["tvdb"] = tvdbId },
    };

    private static MediaTarget Target(string id, string title, string tmdb) => new()
    {
        Id = id,
        ResourceId = id,
        MediaType = "movie",
        Title = title,
        ExternalIds = new Dictionary<string, string> { ["tmdb"] = tmdb },
    };

    private sealed class InlineProgress<T>(Action<T> report) : IProgress<T>
    {
        public void Report(T value) => report(value);
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

    [Fact]
    public void SearchTypesAreStableDedupedAndBounded()
    {
        var types = Enumerable.Range(0, 20).Select(index => $"type-{index}").Prepend("TYPE-0");
        var normalized = ProgressiveSearchPolicy.NormalizeTypes(types, out var truncated);

        Assert.True(truncated);
        Assert.Equal(16, normalized.Count);
        Assert.Equal("TYPE-0", normalized[0]);
        Assert.Equal(16, normalized.Distinct(StringComparer.OrdinalIgnoreCase).Count());
    }

    [Fact]
    public async Task SearchFanoutNeverExceedsFourConcurrentFetches()
    {
        var active = 0;
        var maximum = 0;
        var release = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var fetches = Enumerable.Range(0, 16).Select(index => new Func<CancellationToken, Task<string?>>(async token =>
        {
            var current = Interlocked.Increment(ref active);
            InterlockedExtensions.Max(ref maximum, current);
            try { await release.Task.WaitAsync(token); return index.ToString(); }
            finally { Interlocked.Decrement(ref active); }
        })).ToArray();

        var collection = ProgressiveSearchPolicy.CollectOrderedAsync(fetches, TestContext.Current.CancellationToken);
        await Task.Delay(50, TestContext.Current.CancellationToken);
        Assert.Equal(4, Volatile.Read(ref maximum));
        release.SetResult();
        Assert.Equal(16, (await collection).Length);
    }

    private static class InterlockedExtensions
    {
        internal static void Max(ref int location, int value)
        {
            int observed;
            do
            {
                observed = Volatile.Read(ref location);
                if (observed >= value) return;
            } while (Interlocked.CompareExchange(ref location, value, observed) != observed);
        }
    }
}
