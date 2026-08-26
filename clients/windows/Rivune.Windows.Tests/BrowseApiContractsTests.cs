using System.Net;
using System.Net.Http.Headers;
using System.Text;
using System.Text.Json;
using Rivune.Windows;
using Xunit;

namespace Rivune.Windows.Tests;

public sealed class BrowseApiContractsTests
{
    private static readonly Guid CollectionId = Guid.Parse("11111111-1111-4111-8111-111111111111");
    private static readonly Guid FolderId = Guid.Parse("22222222-2222-4222-8222-222222222222");
    private static readonly Guid AddonId = Guid.Parse("44444444-4444-4444-8444-444444444444");

    [Fact]
    public async Task BrowseMethodsUseAuthenticatedProfilePathsQueryAndBody()
    {
        var handler = new BrowseHandler();
        using var client = CreateClient(handler);
        var cancellationToken = TestContext.Current.CancellationToken;

        var collections = await client.GetCollectionsAsync(cancellationToken);
        var resolved = await client.ResolveCollectionFolderAsync(
            CollectionId,
            FolderId,
            page: 3,
            limit: 40,
            language: "fr-CA",
            region: "CA",
            cancellationToken: cancellationToken);
        var title = await client.ResolveTitleAsync(
            new TitleResolveInput
            {
                MediaType = TitleResolveMediaType.Tv,
                Provider = "addon",
                ResourceId = "series/season/episode",
                Title = "Episode",
                PosterUrl = "/poster.jpg",
                SourceAddonId = AddonId,
                SourceCatalogId = "catalog",
            },
            cancellationToken);

        Assert.Equal("Featured", Assert.Single(collections).Title);
        Assert.Equal(CollectionViewMode.Rows, collections[0].ViewMode);
        Assert.Equal(CollectionSourceFailureCode.SourceTimeout, Assert.Single(resolved.Errors).Code);
        Assert.Equal("Overview", Assert.Single(resolved.Items).Description);
        Assert.Equal(CollectionSourceKind.AddonCatalog, resolved.Items[0].Sources[0].Kind);
        Assert.Equal(TitleResolveMediaType.Tv, title.MediaType);
        Assert.Equal("ignored safely", resolved.Items[0].Raw!.Value.GetProperty("future").GetString());

        AssertRequest(handler, HttpMethod.Get, "/api/v1/collections", "");
        AssertRequest(
            handler,
            HttpMethod.Get,
            "/api/v1/collections/11111111-1111-4111-8111-111111111111/folders/22222222-2222-4222-8222-222222222222/items",
            "page=3&limit=40&language=fr-CA&region=CA");
        var resolveRequest = AssertRequest(handler, HttpMethod.Post, "/api/v1/titles/resolve", "");
        using var body = JsonDocument.Parse(resolveRequest.Body!);
        Assert.Equal("tv", body.RootElement.GetProperty("mediaType").GetString());
        Assert.Equal("addon", body.RootElement.GetProperty("provider").GetString());
        Assert.Equal("series/season/episode", body.RootElement.GetProperty("resourceId").GetString());
        Assert.Equal(AddonId, body.RootElement.GetProperty("sourceAddonId").GetGuid());
        Assert.False(body.RootElement.TryGetProperty("externalId", out _));

        Assert.All(handler.Requests, request =>
        {
            Assert.Equal("Bearer access", request.Authorization);
            Assert.Equal("profile-context", request.ProfileContext);
        });
    }

    [Theory]
    [InlineData("/images/poster.jpg", "https://rivune.test/images/poster.jpg")]
    [InlineData("https://rivune.test/poster.jpg", "https://rivune.test/poster.jpg")]
    public void ResourceUrlResolverAcceptsOnlySameOriginSafeForms(string value, string expected)
    {
        using var client = CreateClient(new BrowseHandler());

        Assert.Equal(expected, client.ResolveResponseResourceUrl(value).AbsoluteUri);
    }

    [Theory]
    [InlineData("poster.jpg")]
    [InlineData("//evil.example/poster.jpg")]
    [InlineData("https://cdn.rivune.example/poster.jpg")]
    [InlineData("http://localhost:8080/poster.jpg")]
    [InlineData("http://127.0.0.1:9000/poster.jpg")]
    [InlineData("\\\\evil.example\\poster.jpg")]
    [InlineData("http://evil.example/poster.jpg")]
    [InlineData("ftp://rivune.test/poster.jpg")]
    [InlineData("javascript:alert(1)")]
    [InlineData("https:///poster.jpg")]
    [InlineData("https://user:secret@rivune.test/poster.jpg")]
    [InlineData("https://rivune.test/poster.jpg#fragment")]
    [InlineData("/poster.jpg#fragment")]
    public void ResourceUrlResolverRejectsUnsafeForms(string value)
    {
        using var client = CreateClient(new BrowseHandler());

        Assert.Throws<InvalidServerUrlException>(() => client.ResolveResponseResourceUrl(value));
    }

    [Fact]
    public async Task RetryAfterDeltaSurvivesDecodedServerError()
    {
        var response = ErrorResponse();
        response.Headers.RetryAfter = new RetryConditionHeaderValue(TimeSpan.FromSeconds(42));
        using var client = CreateClient(new ErrorHandler(response));

        var exception = await Assert.ThrowsAsync<RivuneServerException>(
            () => client.GetCollectionsAsync(TestContext.Current.CancellationToken));

        Assert.Equal("browse_busy", exception.Code);
        Assert.Equal(TimeSpan.FromSeconds(42), exception.RetryAfter);
    }

    [Fact]
    public async Task RetryAfterDateIsConvertedToRemainingDelay()
    {
        var response = ErrorResponse();
        var retryAt = DateTimeOffset.UtcNow.AddMinutes(2);
        response.Headers.TryAddWithoutValidation("Retry-After", retryAt.ToString("r"));
        using var client = CreateClient(new ErrorHandler(response));

        var exception = await Assert.ThrowsAsync<RivuneServerException>(
            () => client.GetCollectionsAsync(TestContext.Current.CancellationToken));

        Assert.NotNull(exception.RetryAfter);
        Assert.InRange(exception.RetryAfter.Value, TimeSpan.FromSeconds(100), TimeSpan.FromSeconds(121));
    }

    [Fact]
    public async Task InvalidRetryAfterIsIgnored()
    {
        var response = ErrorResponse();
        response.Headers.TryAddWithoutValidation("Retry-After", "later-ish");
        using var client = CreateClient(new ErrorHandler(response));

        var exception = await Assert.ThrowsAsync<RivuneServerException>(
            () => client.GetCollectionsAsync(TestContext.Current.CancellationToken));

        Assert.Null(exception.RetryAfter);
    }

    private static CapturedRequest AssertRequest(
        BrowseHandler handler,
        HttpMethod method,
        string path,
        string query) =>
        Assert.Single(handler.Requests, request =>
            request.Method == method && request.Path == path && request.Query == query);

    private static RivuneApiClient CreateClient(HttpMessageHandler handler) =>
        new("https://rivune.test", handler, new FixedCredentialStore());

    private static HttpResponseMessage ErrorResponse() => new(HttpStatusCode.TooManyRequests)
    {
        Content = new StringContent(
            """{"error":{"code":"browse_busy","message":"Try again later."}}""",
            Encoding.UTF8,
            "application/json"),
    };

    private sealed class BrowseHandler : HttpMessageHandler
    {
        public List<CapturedRequest> Requests { get; } = [];

        protected override async Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken)
        {
            if (request.RequestUri!.AbsolutePath == "/.well-known/rivune")
            {
                return DiscoveryResponse();
            }

            Requests.Add(new CapturedRequest(
                request.Method,
                request.RequestUri.AbsolutePath,
                request.RequestUri.Query.TrimStart('?'),
                request.Content is null ? null : await request.Content.ReadAsStringAsync(cancellationToken),
                request.Headers.Authorization?.ToString(),
                Header(request, "X-Rivune-Profile-Context")));

            return request.RequestUri.AbsolutePath switch
            {
                "/api/v1/collections" => JsonResponse(CollectionsJson),
                "/api/v1/collections/11111111-1111-4111-8111-111111111111/folders/22222222-2222-4222-8222-222222222222/items" => JsonResponse(ResolvedFolderJson),
                "/api/v1/titles/resolve" => JsonResponse(TitleReferenceJson),
                _ => throw new InvalidOperationException($"Unexpected path {request.RequestUri.AbsolutePath}."),
            };
        }
    }

    private sealed class ErrorHandler(HttpResponseMessage errorResponse) : HttpMessageHandler
    {
        protected override Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken) =>
            Task.FromResult(request.RequestUri!.AbsolutePath == "/.well-known/rivune"
                ? DiscoveryResponse()
                : errorResponse);
    }

    private sealed class FixedCredentialStore : ICredentialStore
    {
        public ValueTask<StoredCredentials?> LoadAsync(CancellationToken cancellationToken = default) =>
            ValueTask.FromResult<StoredCredentials?>(new StoredCredentials
            {
                Issuer = "https://rivune.test/",
                ProfileContext = "profile-context",
                Credentials = new TokenPair
                {
                    TokenType = "Bearer",
                    AccessToken = "access",
                    AccessTokenExpiresAt = "2099-01-01T00:00:00Z",
                    RefreshToken = "refresh",
                    RefreshTokenExpiresAt = "2099-02-01T00:00:00Z",
                    SessionId = Guid.Parse("66666666-6666-4666-8666-666666666666"),
                    DeviceId = Guid.Parse("77777777-7777-4777-8777-777777777777"),
                    AuthorizationScope = AuthorizationScope.GlobalAdministrator,
                    Category = null,
                },
            });

        public ValueTask SaveAsync(StoredCredentials credentials, CancellationToken cancellationToken = default) =>
            ValueTask.CompletedTask;

        public ValueTask ClearAsync(CancellationToken cancellationToken = default) =>
            ValueTask.CompletedTask;
    }

    private static string? Header(HttpRequestMessage request, string name) =>
        request.Headers.TryGetValues(name, out var values) ? Assert.Single(values) : null;

    private static HttpResponseMessage DiscoveryResponse() => JsonResponse(
        """{"name":"Rivune","serverVersion":"test","protocolVersion":22,"apiBaseUrl":"/api/v1/","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en"}""");

    private static HttpResponseMessage JsonResponse(string json) => new(HttpStatusCode.OK)
    {
        Content = new StringContent(json, Encoding.UTF8, "application/json"),
    };

    private sealed record CapturedRequest(
        HttpMethod Method,
        string Path,
        string Query,
        string? Body,
        string? Authorization,
        string? ProfileContext);

    private const string CollectionsJson = """
        {"collections":[{"id":"11111111-1111-4111-8111-111111111111","title":"Featured","heroEnabled":true,"pinToTop":false,"focusGlowEnabled":true,"viewMode":"rows","folderCoverShape":"landscape","folders":[{"id":"22222222-2222-4222-8222-222222222222","title":"New","tileShape":"poster","focusGifEnabled":false,"hideTitle":false,"sources":[{"id":"33333333-3333-4333-8333-333333333333","kind":"addon_catalog","title":"Movies","futureSourceField":true}],"futureFolderField":true}],"profileIds":[],"categoryIds":[],"position":0,"version":1,"createdAt":"2026-08-17T00:00:00Z","updatedAt":"2026-08-17T00:00:00Z","futureCollectionField":true}],"futureListField":true}
        """;

    private const string ResolvedFolderJson = """
        {"collectionId":"11111111-1111-4111-8111-111111111111","folder":{"id":"22222222-2222-4222-8222-222222222222","title":"New","tileShape":"poster","focusGifEnabled":false,"hideTitle":false,"sources":[{"id":"33333333-3333-4333-8333-333333333333","kind":"addon_catalog","title":"Movies"}]},"items":[{"id":"series/season/episode","mediaType":"tv","title":"Episode","posterUrl":"/poster.jpg","description":"Overview","externalIds":{},"sources":[{"id":"33333333-3333-4333-8333-333333333333","kind":"addon_catalog","title":"Movies","addonId":"44444444-4444-4444-8444-444444444444","manifestId":"org.example","catalogId":"catalog"}],"raw":{"future":"ignored safely"},"futureItemField":true}],"page":3,"hasMore":false,"errors":[{"sourceId":"33333333-3333-4333-8333-333333333333","kind":"addon_catalog","code":"collection_source_timeout","message":"Timed out"}],"futureResolvedField":true}
        """;

    private const string TitleReferenceJson = """
        {"titleId":"55555555-5555-4555-8555-555555555555","mediaType":"tv","provider":"addon","externalId":"derived-id","resourceId":"series/season/episode","title":"Episode","posterUrl":"/poster.jpg","sourceAddonId":"44444444-4444-4444-8444-444444444444","sourceCatalogId":"catalog","futureTitleField":true}
        """;
}
