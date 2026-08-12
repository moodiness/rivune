using System.Net;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;
using Rivune.Windows;
using Xunit;

namespace Rivune.Windows.Tests;

public sealed class PlaybackProgressContractsTests
{
    private static readonly Guid TitleId = Guid.Parse("11111111-1111-4111-8111-111111111111");
    private static readonly Guid AddonId = Guid.Parse("22222222-2222-4222-8222-222222222222");
    private static readonly JsonSerializerOptions JsonOptions = new(JsonSerializerDefaults.Web)
    {
        DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
        UnmappedMemberHandling = JsonUnmappedMemberHandling.Skip,
    };

    [Fact]
    public void MetadataAndMarkerModelsDecodeRequiredV20Fields()
    {
        const string movieJson = """
        {"id":"11111111-1111-4111-8111-111111111111","mediaType":"movie","title":"Movie","originalTitle":"Movie","originalLanguage":"en","overview":"Overview","genres":[],"cast":[{"id":"42","name":"Actor","character":"Lead","profileUrl":"/actor.jpg"}],"voteAverage":8.5,"voteCount":10,"externalIds":{}}
        """;
        const string seriesJson = """
        {"id":"11111111-1111-4111-8111-111111111111","mediaType":"series","name":"Series","originalName":"Series","originalLanguage":"en","overview":"Overview","genres":[],"cast":[{"id":"43","name":"Actor"}],"voteAverage":8.0,"voteCount":20,"seasons":[],"aliases":[],"episodeOrders":[],"selectedEpisodeOrderId":"9876543210","mappingProvider":"tvdb","externalIds":{}}
        """;
        const string markerJson = """
        {"markers":[{"type":"intro","startSeconds":12.25,"endSeconds":93.75,"confidence":0.95,"submissionCount":7}]}
        """;

        var movie = JsonSerializer.Deserialize<Movie>(movieJson, JsonOptions)!;
        var series = JsonSerializer.Deserialize<Series>(seriesJson, JsonOptions)!;
        var markers = JsonSerializer.Deserialize<PlaybackMarkerList>(markerJson, JsonOptions)!;

        Assert.Equal("Actor", Assert.Single(movie.Cast).Name);
        Assert.Equal("Lead", movie.Cast[0].Character);
        Assert.Equal("9876543210", series.SelectedEpisodeOrderId);
        Assert.Equal("43", Assert.Single(series.Cast).Id);
        Assert.Equal(PlaybackMarkerType.Intro, Assert.Single(markers.Markers).Type);
        Assert.Equal(12.25, markers.Markers[0].StartSeconds);
        Assert.Equal(93.75, markers.Markers[0].EndSeconds);
    }

    [Fact]
    public async Task ProgressNoContentReturnsNullWithoutDecodingEmptyBody()
    {
        var handler = new ContractHandler((request, _) =>
            request.RequestUri!.AbsolutePath == "/api/v1/progress/11111111-1111-4111-8111-111111111111"
                ? new HttpResponseMessage(HttpStatusCode.NoContent)
                : null);
        using var client = CreateClient(handler);

        var progress = await client.GetPlaybackProgressAsync(TitleId, TestContext.Current.CancellationToken);

        Assert.Null(progress);
    }

    [Fact]
    public async Task V20MethodsUseRequiredPathsQueriesAndBodies()
    {
        var handler = new ContractHandler();
        using var client = CreateClient(handler);
        var cancellationToken = TestContext.Current.CancellationToken;
        var capabilities = new PlaybackCapabilities
        {
            StreamingProtocols = ["hls"],
            Containers = ["mp4"],
        };

        await client.GetSeriesAsync(TitleId, SeriesMappingProvider.Tvdb, "fr", "9876543210", cancellationToken);
        await client.GetPlaybackMarkersAsync("tt1234567", 2, 9, cancellationToken);
        await client.GetPlaybackSourcesAsync("movie", "resource/1", capabilities, AddonId, cancellationToken);
        await client.GetPlaybackProgressAsync(TitleId, cancellationToken);
        await client.GetPlaybackProgressBatchAsync([TitleId], cancellationToken);
        await client.UpdatePlaybackProgressAsync(
            TitleId,
            new UpdatePlaybackProgressRequest
            {
                PositionSeconds = 125,
                DurationSeconds = 7200,
                Completed = false,
                ExpectedVersion = 4_000_000_000,
            },
            cancellationToken);
        await client.ClearPlaybackProgressAsync(TitleId, 4_000_000_001, cancellationToken);
        await client.SetTitlesWatchedBatchAsync(
            new SetWatchedBatchRequest
            {
                Items =
                [
                    new SetWatchedBatchItem
                    {
                        TitleId = TitleId,
                        Completed = true,
                        ExpectedVersion = 4_000_000_002,
                    },
                ],
            },
            cancellationToken);
        await client.MarkTitleWatchedAsync(TitleId, 4_000_000_003, cancellationToken);
        await client.MarkTitleUnwatchedAsync(TitleId, 4_000_000_004, cancellationToken);
        await client.DismissContinueWatchingTitleAsync(TitleId, cancellationToken);
        await client.GetContinueWatchingAsync(35, cancellationToken);

        AssertRequest(handler, HttpMethod.Get, "/api/v1/metadata/series/11111111-1111-4111-8111-111111111111", "language=fr&mappingProvider=tvdb&episodeOrder=9876543210");
        AssertRequest(handler, HttpMethod.Get, "/api/v1/playback/markers", "imdbId=tt1234567&season=2&episode=9");
        var sources = AssertRequest(handler, HttpMethod.Post, "/api/v1/playback/sources", "");
        using (var body = JsonDocument.Parse(sources.Body!))
        {
            Assert.Equal(AddonId, body.RootElement.GetProperty("addonId").GetGuid());
            Assert.Equal("resource/1", body.RootElement.GetProperty("resourceId").GetString());
        }
        AssertRequest(handler, HttpMethod.Get, "/api/v1/progress/11111111-1111-4111-8111-111111111111", "");
        var batch = AssertRequest(handler, HttpMethod.Post, "/api/v1/progress/batch", "");
        using (var body = JsonDocument.Parse(batch.Body!))
        {
            Assert.Equal(TitleId, body.RootElement.GetProperty("titleIds")[0].GetGuid());
        }
        var update = AssertRequest(handler, HttpMethod.Put, "/api/v1/progress/11111111-1111-4111-8111-111111111111", "");
        using (var body = JsonDocument.Parse(update.Body!))
        {
            Assert.Equal(4_000_000_000, body.RootElement.GetProperty("expectedVersion").GetInt64());
        }
        AssertRequest(handler, HttpMethod.Delete, "/api/v1/progress/11111111-1111-4111-8111-111111111111", "expectedVersion=4000000001");
        var watchedBatch = AssertRequest(handler, HttpMethod.Put, "/api/v1/titles/watched/batch", "");
        using (var body = JsonDocument.Parse(watchedBatch.Body!))
        {
            Assert.Equal(4_000_000_002, body.RootElement.GetProperty("items")[0].GetProperty("expectedVersion").GetInt64());
        }
        var watched = AssertRequest(handler, HttpMethod.Post, "/api/v1/titles/11111111-1111-4111-8111-111111111111/watched", "");
        using (var body = JsonDocument.Parse(watched.Body!))
        {
            Assert.Equal(4_000_000_003, body.RootElement.GetProperty("expectedVersion").GetInt64());
        }
        AssertRequest(handler, HttpMethod.Delete, "/api/v1/titles/11111111-1111-4111-8111-111111111111/watched", "expectedVersion=4000000004");
        AssertRequest(handler, HttpMethod.Get, "/api/v1/continue-watching", "limit=35");
        AssertRequest(handler, HttpMethod.Delete, "/api/v1/continue-watching/11111111-1111-4111-8111-111111111111", "");
    }

    [Fact]
    public void ProgressModelsDecodeLongVersionsNullableBatchAndClosedEnums()
    {
        const string json = """
        {"items":[{"titleId":"11111111-1111-4111-8111-111111111111","progress":{"titleId":"11111111-1111-4111-8111-111111111111","mediaType":"episode","positionSeconds":10,"durationSeconds":20,"completed":false,"version":4000000000,"lastWatchedAt":"2026-08-01T00:00:00Z","updatedAt":"2026-08-01T00:00:01Z"}},{"titleId":"33333333-3333-4333-8333-333333333333","progress":null}]}
        """;
        const string continueJson = """
        {"items":[{"titleId":"11111111-1111-4111-8111-111111111111","mediaType":"episode","seriesId":"44444444-4444-4444-8444-444444444444","seasonId":"55555555-5555-4555-8555-555555555555","seasonNumber":2,"episodeNumber":3,"positionSeconds":0,"durationSeconds":1800,"version":4000000001,"reason":"next_episode","lastWatchedAt":"2026-08-01T00:00:00Z"}]}
        """;

        var batch = JsonSerializer.Deserialize<PlaybackProgressBatch>(json, JsonOptions)!;
        var page = JsonSerializer.Deserialize<ContinueWatchingPage>(continueJson, JsonOptions)!;

        Assert.Equal(4_000_000_000, batch.Items[0].Progress!.Version);
        Assert.Equal(PlaybackProgressMediaType.Episode, batch.Items[0].Progress!.MediaType);
        Assert.Null(batch.Items[1].Progress);
        Assert.Equal(ContinueWatchingReason.NextEpisode, Assert.Single(page.Items).Reason);
        Assert.Equal(4_000_000_001, page.Items[0].Version);
    }

    [Fact]
    public void PlaybackEnumsRejectUnknownWireValues()
    {
        Assert.Throws<JsonException>(() =>
            JsonSerializer.Deserialize<PlaybackMarker>(
                """{"type":"credits","startSeconds":1,"endSeconds":2,"confidence":1,"submissionCount":1}""",
                JsonOptions));
        Assert.Throws<JsonException>(() =>
            JsonSerializer.Deserialize<PlaybackProgress>(
                """{"titleId":"11111111-1111-4111-8111-111111111111","mediaType":"series","positionSeconds":1,"durationSeconds":2,"completed":false,"version":1,"lastWatchedAt":"2026-08-01T00:00:00Z","updatedAt":"2026-08-01T00:00:01Z"}""",
                JsonOptions));
    }

    private static CapturedRequest AssertRequest(
        ContractHandler handler,
        HttpMethod method,
        string path,
        string query)
    {
        var request = Assert.Single(
            handler.Requests,
            item => item.Method == method && item.Path == path && item.Query == query);
        return request;
    }

    private static RivuneApiClient CreateClient(HttpMessageHandler handler) =>
        new("https://rivune.test", handler, new FixedCredentialStore());

    private sealed class ContractHandler(
        Func<HttpRequestMessage, CancellationToken, HttpResponseMessage?>? response = null) : HttpMessageHandler
    {
        public List<CapturedRequest> Requests { get; } = [];

        protected override async Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken)
        {
            if (request.RequestUri!.AbsolutePath == "/.well-known/rivune")
            {
                return JsonResponse("""{"name":"Rivune","serverVersion":"test","protocolVersion":20,"apiBaseUrl":"/api/v1/","setupRequired":false,"setupCompleted":true,"demoAvailable":false,"timezone":"UTC","interfaceLanguage":"en"}""");
            }

            Requests.Add(new CapturedRequest(
                request.Method,
                request.RequestUri.AbsolutePath,
                request.RequestUri.Query.TrimStart('?'),
                request.Content is null ? null : await request.Content.ReadAsStringAsync(cancellationToken)));

            var custom = response?.Invoke(request, cancellationToken);
            if (custom is not null)
            {
                return custom;
            }

            return request.RequestUri.AbsolutePath switch
            {
                "/api/v1/metadata/series/11111111-1111-4111-8111-111111111111" => JsonResponse("""{"id":"11111111-1111-4111-8111-111111111111","mediaType":"series","name":"Series","originalName":"Series","originalLanguage":"en","overview":"","genres":[],"cast":[],"voteAverage":0,"voteCount":0,"seasons":[],"aliases":[],"episodeOrders":[],"selectedEpisodeOrderId":"9876543210","mappingProvider":"tvdb","externalIds":{}}"""),
                "/api/v1/playback/markers" => JsonResponse("""{"markers":[{"type":"intro","startSeconds":1.5,"endSeconds":2.5,"confidence":1,"submissionCount":1}]}"""),
                "/api/v1/playback/sources" => JsonResponse("""{"sources":[],"providerErrors":[]}"""),
                "/api/v1/progress/batch" => JsonResponse("""{"items":[{"titleId":"11111111-1111-4111-8111-111111111111","progress":null}]}"""),
                "/api/v1/titles/watched/batch" => JsonResponse($$"""{"items":[{"titleId":"{{TitleId:D}}","progress":{{ProgressJson(5)}}}]}"""),
                "/api/v1/continue-watching" => JsonResponse("""{"items":[]}"""),
                _ when request.Method == HttpMethod.Delete && request.RequestUri.AbsolutePath.StartsWith("/api/v1/continue-watching/", StringComparison.Ordinal) => new HttpResponseMessage(HttpStatusCode.NoContent),
                _ when request.Method == HttpMethod.Delete && request.RequestUri.AbsolutePath.StartsWith("/api/v1/progress/", StringComparison.Ordinal) => new HttpResponseMessage(HttpStatusCode.NoContent),
                _ => JsonResponse(ProgressJson(5)),
            };
        }
    }

    private static string ProgressJson(long version) => $$"""
        {"titleId":"{{TitleId:D}}","mediaType":"movie","positionSeconds":125,"durationSeconds":7200,"completed":false,"version":{{version}},"lastWatchedAt":"2026-08-01T00:00:00Z","updatedAt":"2026-08-01T00:00:01Z"}
        """;

    private static HttpResponseMessage JsonResponse(string json) => new(HttpStatusCode.OK)
    {
        Content = new StringContent(json, Encoding.UTF8, "application/json"),
    };

    private sealed record CapturedRequest(HttpMethod Method, string Path, string Query, string? Body);

    private sealed class FixedCredentialStore : ICredentialStore
    {
        private readonly StoredCredentials _stored = new()
        {
            Issuer = "https://rivune.test/",
            Credentials = new TokenPair
            {
                TokenType = "Bearer",
                AccessToken = "access",
                AccessTokenExpiresAt = "2026-08-01T01:00:00Z",
                RefreshToken = "refresh",
                RefreshTokenExpiresAt = "2026-09-01T00:00:00Z",
                SessionId = Guid.Parse("66666666-6666-4666-8666-666666666666"),
                DeviceId = Guid.Parse("77777777-7777-4777-8777-777777777777"),
                AuthorizationScope = AuthorizationScope.GlobalAdministrator,
                Category = null,
            },
        };

        public ValueTask<StoredCredentials?> LoadAsync(CancellationToken cancellationToken = default) =>
            ValueTask.FromResult<StoredCredentials?>(_stored);

        public ValueTask SaveAsync(StoredCredentials credentials, CancellationToken cancellationToken = default) =>
            ValueTask.CompletedTask;

        public ValueTask ClearAsync(CancellationToken cancellationToken = default) =>
            ValueTask.CompletedTask;
    }
}
