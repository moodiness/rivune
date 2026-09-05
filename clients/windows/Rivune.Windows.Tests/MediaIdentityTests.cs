using System.Net;
using System.Text;
using Rivune.App;
using Rivune.Windows;
using Xunit;

namespace Rivune.Windows.Tests;

public sealed class MediaIdentityTests
{
    private static readonly Guid SeriesId = Guid.Parse("11111111-1111-4111-8111-111111111111");
    private const string MetadataSeasonId = "tvdb:11111111-1111-4111-8111-111111111111:2112814";

    [Fact]
    public async Task OrderedContinuationDetailRequestUsesExactTvdbOrderAndOpaqueSeason()
    {
        var target = VariantTarget();
        var handler = new RecordingHandler();
        using var client = new RivuneApiClient("https://rivune.test", handler, new FixedCredentialStore());
        var context = Assert.IsType<MediaVariantContext>(MediaIdentity.VariantContext(target));

        var series = await client.GetSeriesAsync(
            SeriesId,
            context.MappingProvider,
            episodeOrder: context.EpisodeOrderId,
            cancellationToken: TestContext.Current.CancellationToken);
        var seasonId = MediaIdentity.DetailSeasonId(target, series);
        await client.GetSeasonAsync(
            Assert.IsType<string>(seasonId),
            context.MappingProvider,
            cancellationToken: TestContext.Current.CancellationToken);

        Assert.Equal(SeriesMappingProvider.Tvdb, context.MappingProvider);
        Assert.Equal("2", context.EpisodeOrderId);
        Assert.Equal(MetadataSeasonId, seasonId);
        Assert.Contains(handler.Requests, request =>
            request.Path == "/api/v1/metadata/series/11111111-1111-4111-8111-111111111111" &&
            request.Query == "mappingProvider=tvdb&episodeOrder=2");
        Assert.Contains(handler.Requests, request =>
            request.Path == $"/api/v1/metadata/seasons/{Uri.EscapeDataString(MetadataSeasonId)}" &&
            request.Query == "mappingProvider=tvdb");
    }

    [Fact]
    public void VariantEpisodeIdentityUsesRawTvdbResourceAndSuppressesCanonicalMarkers()
    {
        var series = Series(selectedOrderId: "2", orderType: "dvd", mappingProvider: SeriesMappingProvider.Tvdb);
        var episode = Episode("10357450");
        var target = VariantTarget();

        Assert.Equal("tvdb:10357450", MediaIdentity.EpisodeResourceId(series, episode, SeriesId.ToString("D"), "2"));
        Assert.False(MediaIdentity.CanLoadCanonicalMarkers(target));
    }

    [Fact]
    public void CanonicalEpisodeIdentityAndMarkersRemainUnchanged()
    {
        var series = Series(selectedOrderId: "official", orderType: "official", mappingProvider: SeriesMappingProvider.Tmdb);
        var episode = Episode("10357450");
        var target = new MediaTarget
        {
            Id = episode.Id.ToString("D"),
            ResourceId = "tt12345678:1:2",
            MediaType = "episode",
            Title = episode.Name,
            TitleId = episode.Id,
            SeriesId = SeriesId,
            SeasonId = "canonical-season-1",
            SeasonNumber = 1,
            EpisodeNumber = 2,
            SeriesImdbId = "tt12345678",
        };

        Assert.Equal("tt12345678:1:2", MediaIdentity.EpisodeResourceId(series, episode, SeriesId.ToString("D")));
        Assert.True(MediaIdentity.CanLoadCanonicalMarkers(target));
    }

    [Theory]
    [InlineData(null, "2", "tvdb:series:2112814")]
    [InlineData("tvdb", null, "tvdb:series:2112814")]
    [InlineData("tvdb", "2", null)]
    public void PartialVariantContextIsNotRecognized(
        string? mappingProvider,
        string? episodeOrderId,
        string? metadataSeasonId)
    {
        var target = VariantTarget() with
        {
            MappingProvider = mappingProvider == "tvdb" ? SeriesMappingProvider.Tvdb : null,
            EpisodeOrderId = episodeOrderId,
            MetadataSeasonId = metadataSeasonId,
        };

        Assert.Null(MediaIdentity.VariantContext(target));
    }

    private static MediaTarget VariantTarget() => new()
    {
        Id = "tvdb:10357450",
        ResourceId = "tvdb:10357450",
        MediaType = "episode",
        Title = "DVD Episode 2",
        TitleId = Guid.Parse("22222222-2222-4222-8222-222222222222"),
        SeriesId = SeriesId,
        MappingProvider = SeriesMappingProvider.Tvdb,
        EpisodeOrderId = "2",
        MetadataSeasonId = MetadataSeasonId,
        SeasonId = Guid.Parse("33333333-3333-4333-8333-333333333333").ToString("D"),
        SeasonNumber = 1,
        EpisodeNumber = 2,
        SeriesImdbId = "tt12345678",
    };

    private static Series Series(string? selectedOrderId, string orderType, SeriesMappingProvider mappingProvider) => new()
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
        Seasons =
        [
            new SeasonSummary
            {
                Id = selectedOrderId == "2" ? MetadataSeasonId : "canonical-season-1",
                MediaType = MediaType.Season,
                SeriesId = SeriesId,
                Name = "Season 1",
                Overview = string.Empty,
                SeasonNumber = 1,
                EpisodeCount = 1,
                VoteAverage = 0,
                ExternalIds = new Dictionary<string, string>(),
            },
        ],
        Aliases = [],
        EpisodeOrders =
        [
            new EpisodeOrder
            {
                Id = selectedOrderId ?? "official",
                Name = "Selected",
                Type = orderType,
                IsDefault = true,
            },
        ],
        SelectedEpisodeOrderId = selectedOrderId,
        MappingProvider = mappingProvider,
        ExternalIds = new Dictionary<string, string> { ["imdb"] = "tt12345678" },
    };

    private static Episode Episode(string tvdbId) => new()
    {
        Id = Guid.Parse("22222222-2222-4222-8222-222222222222"),
        MediaType = MediaType.Episode,
        SeasonId = MetadataSeasonId,
        Name = "DVD Episode 2",
        Overview = string.Empty,
        SeasonNumber = 1,
        EpisodeNumber = 2,
        VoteAverage = 0,
        VoteCount = 0,
        ExternalIds = new Dictionary<string, string> { ["tvdb"] = tvdbId },
    };

    private sealed class RecordingHandler : HttpMessageHandler
    {
        public List<CapturedRequest> Requests { get; } = [];

        protected override Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken)
        {
            if (request.RequestUri!.AbsolutePath == "/.well-known/rivune")
            {
                return Task.FromResult(JsonResponse("""{"name":"Rivune","serverVersion":"test","protocolVersion":22,"apiBaseUrl":"/api/v1/","setupRequired":false,"setupCompleted":true,"demoAvailable":false,"timezone":"UTC","interfaceLanguage":"en"}"""));
            }

            Requests.Add(new CapturedRequest(
                request.RequestUri.AbsolutePath,
                request.RequestUri.Query.TrimStart('?')));
            var body = request.RequestUri.AbsolutePath.Contains("/metadata/series/", StringComparison.Ordinal)
                ? $$$"""{"id":"{{{SeriesId:D}}}","mediaType":"series","name":"Series","originalName":"Series","originalLanguage":"en","overview":"","genres":[],"cast":[],"voteAverage":0,"voteCount":0,"seasons":[{"id":"{{{MetadataSeasonId}}}","mediaType":"season","seriesId":"{{{SeriesId:D}}}","name":"DVD Season 1","overview":"","seasonNumber":1,"episodeCount":1,"voteAverage":0,"externalIds":{}}],"aliases":[],"episodeOrders":[{"id":"2","name":"DVD","type":"dvd","isDefault":false}],"selectedEpisodeOrderId":"2","mappingProvider":"tvdb","externalIds":{"imdb":"tt12345678"}}"""
                : $$$"""{"id":"{{{MetadataSeasonId}}}","mediaType":"season","seriesId":"{{{SeriesId:D}}}","name":"DVD Season 1","overview":"","seasonNumber":1,"voteAverage":0,"episodes":[],"externalIds":{}}""";
            return Task.FromResult(JsonResponse(body));
        }
    }

    private sealed record CapturedRequest(string Path, string Query);

    private static HttpResponseMessage JsonResponse(string body) => new(HttpStatusCode.OK)
    {
        Content = new StringContent(body, Encoding.UTF8, "application/json"),
    };

    private sealed class FixedCredentialStore : ICredentialStore
    {
        private readonly StoredCredentials _stored = new()
        {
            Issuer = "https://rivune.test/",
            Credentials = new TokenPair
            {
                TokenType = "Bearer",
                AccessToken = "access",
                AccessTokenExpiresAt = "2099-08-01T01:00:00Z",
                RefreshToken = "refresh",
                RefreshTokenExpiresAt = "2099-09-01T00:00:00Z",
                SessionId = Guid.Parse("44444444-4444-4444-8444-444444444444"),
                DeviceId = Guid.Parse("55555555-5555-4555-8555-555555555555"),
                AuthorizationScope = AuthorizationScope.GlobalAdministrator,
                Category = null,
            },
        };

        public ValueTask<StoredCredentials?> LoadAsync(CancellationToken cancellationToken = default) =>
            ValueTask.FromResult<StoredCredentials?>(_stored);

        public ValueTask SaveAsync(StoredCredentials credentials, CancellationToken cancellationToken = default) =>
            ValueTask.CompletedTask;

        public ValueTask ClearAsync(CancellationToken cancellationToken = default) => ValueTask.CompletedTask;
    }
}
