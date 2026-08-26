using System.Net;
using System.Text;
using Rivune.Windows;
using Xunit;

namespace Rivune.Windows.Tests;

public sealed class ProtocolV22BrowseContractsTests
{
    private static readonly Guid CollectionId = Guid.Parse("11111111-1111-4111-8111-111111111111");
    private static readonly Guid ProfileId = Guid.Parse("22222222-2222-4222-8222-222222222222");
    private static readonly Guid AddonId = Guid.Parse("33333333-3333-4333-8333-333333333333");
    private static readonly Guid TitleId = Guid.Parse("44444444-4444-4444-8444-444444444444");

    [Fact]
    public async Task CatalogSearchEscapesPathAndOrderedRepeatedQueryAndAuthenticates()
    {
        var handler = new ContractHandler();
        using var client = CreateClient(handler);
        var cancellationToken = TestContext.Current.CancellationToken;

        var catalogs = await client.GetAddonCatalogsAsync(cancellationToken);
        var search = await client.SearchAddonCatalogsAsync(
            "tv specials",
            "star & moon",
            skip: 5,
            limit: 17,
            extras:
            [
                KeyValuePair.Create("genre", "sci fi"),
                KeyValuePair.Create("genre", "crime/action"),
            ],
            language: "zh-Hant",
            cancellationToken: cancellationToken);

        Assert.Equal("TV", Assert.Single(catalogs).Catalog.Name);
        Assert.True(catalogs[0].Searchable);
        Assert.Equal("star & moon", Assert.Single(search.Results).Payload.GetProperty("query").GetString());
        Assert.Empty(search.Errors);

        AssertRequest(handler, HttpMethod.Get, "/api/v1/addons/catalogs", "", body: null);
        AssertRequest(
            handler,
            HttpMethod.Get,
            "/api/v1/addons/catalogs/search/tv%20specials",
            "search=star%20%26%20moon&skip=5&limit=17&language=zh-Hant&genre=sci%20fi&genre=crime%2Faction",
            body: null);
        AssertAuthenticated(handler);
    }

    [Fact]
    public async Task LibraryUsesTypedFiltersPagingAndBodylessPutDelete()
    {
        var handler = new ContractHandler();
        using var client = CreateClient(handler);
        var cancellationToken = TestContext.Current.CancellationToken;

        var library = await client.GetLibraryAsync(
            TitleMediaType.Tv,
            page: 3,
            pageSize: 25,
            cancellationToken: cancellationToken);
        var added = await client.AddLibraryTitleAsync(TitleId, cancellationToken);
        await client.RemoveLibraryTitleAsync(TitleId, cancellationToken);

        Assert.Equal(TitleMediaType.Tv, Assert.Single(library.Items).MediaType);
        Assert.Equal(3, library.Page);
        Assert.Equal(TitleId, added.TitleId);

        AssertRequest(handler, HttpMethod.Get, "/api/v1/library", "mediaType=tv&page=3&pageSize=25", body: null);
        AssertRequest(handler, HttpMethod.Put, $"/api/v1/library/{TitleId:D}", "", body: null);
        AssertRequest(handler, HttpMethod.Delete, $"/api/v1/library/{TitleId:D}", "", body: null);
        AssertAuthenticated(handler);
    }

    [Fact]
    public async Task CalendarCollectionAndProfileAvatarUseFixedAuthenticatedBoundedEndpoints()
    {
        var handler = new ContractHandler();
        using var client = CreateClient(handler);
        var cancellationToken = TestContext.Current.CancellationToken;

        var collection = await client.GetCollectionAsync(CollectionId, cancellationToken);
        var avatar = await client.GetProfileAvatarAsync(ProfileId, cancellationToken);
        var calendar = await client.GetCalendarAsync(
            "2026-08-01",
            "2026-08-31",
            "pt-BR",
            cancellationToken);

        Assert.Equal(CollectionId, collection.Id);
        Assert.Equal([0x89, 0x50, 0x4e, 0x47], avatar);
        Assert.Equal(CalendarEventMediaType.Episode, Assert.Single(calendar).MediaType);

        AssertRequest(handler, HttpMethod.Get, $"/api/v1/collections/{CollectionId:D}", "", body: null);
        AssertRequest(handler, HttpMethod.Get, $"/api/v1/profiles/{ProfileId:D}/avatar", "", body: null);
        AssertRequest(
            handler,
            HttpMethod.Get,
            "/api/v1/calendar",
            "from=2026-08-01&to=2026-08-31&language=pt-BR",
            body: null);
        AssertAuthenticated(handler);
    }

    private static RivuneApiClient CreateClient(HttpMessageHandler handler) =>
        new("https://rivune.test", handler, new FixedCredentialStore());

    private static CapturedRequest AssertRequest(
        ContractHandler handler,
        HttpMethod method,
        string path,
        string query,
        string? body) =>
        Assert.Single(handler.Requests, request =>
            request.Method == method &&
            request.EscapedPath == path &&
            request.Query == query &&
            request.Body == body);

    private static void AssertAuthenticated(ContractHandler handler) =>
        Assert.All(handler.Requests, request =>
        {
            Assert.Equal("Bearer access", request.Authorization);
            Assert.Equal(
                request.EscapedPath == $"/api/v1/profiles/{ProfileId:D}/avatar" ? null : "profile-context",
                request.ProfileContext);
        });

    private sealed class ContractHandler : HttpMessageHandler
    {
        public List<CapturedRequest> Requests { get; } = [];

        protected override async Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken)
        {
            if (request.RequestUri!.AbsolutePath == "/.well-known/rivune")
            {
                return JsonResponse(
                    """{"name":"Rivune","serverVersion":"test","protocolVersion":22,"apiBaseUrl":"/api/v1/","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en"}""");
            }

            var escapedPath = request.RequestUri.GetComponents(UriComponents.Path, UriFormat.UriEscaped);
            escapedPath = "/" + escapedPath.TrimStart('/');
            Requests.Add(new CapturedRequest(
                request.Method,
                escapedPath,
                request.RequestUri.Query.TrimStart('?'),
                request.Content is null ? null : await request.Content.ReadAsStringAsync(cancellationToken),
                request.Headers.Authorization?.ToString(),
                Header(request, "X-Rivune-Profile-Context")));

            return request.RequestUri.AbsolutePath switch
            {
                "/api/v1/addons/catalogs" => JsonResponse(CatalogsJson),
                "/api/v1/addons/catalogs/search/tv specials" or
                "/api/v1/addons/catalogs/search/tv%20specials" => JsonResponse(SearchJson),
                "/api/v1/library" when request.Method == HttpMethod.Get => JsonResponse(LibraryJson),
                var path when path == $"/api/v1/library/{TitleId:D}" && request.Method == HttpMethod.Put => JsonResponse(LibraryItemJson),
                var path when path == $"/api/v1/library/{TitleId:D}" && request.Method == HttpMethod.Delete => new HttpResponseMessage(HttpStatusCode.NoContent),
                var path when path == $"/api/v1/collections/{CollectionId:D}" => JsonResponse(CollectionJson),
                var path when path == $"/api/v1/profiles/{ProfileId:D}/avatar" => BytesResponse([0x89, 0x50, 0x4e, 0x47]),
                "/api/v1/calendar" => JsonResponse(CalendarJson),
                _ => throw new InvalidOperationException($"Unexpected request {request.Method} {request.RequestUri}."),
            };
        }
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
                    SessionId = Guid.Parse("55555555-5555-4555-8555-555555555555"),
                    DeviceId = Guid.Parse("66666666-6666-4666-8666-666666666666"),
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

    private static HttpResponseMessage JsonResponse(string json) => new(HttpStatusCode.OK)
    {
        Content = new StringContent(json, Encoding.UTF8, "application/json"),
    };

    private static HttpResponseMessage BytesResponse(byte[] bytes) => new(HttpStatusCode.OK)
    {
        Content = new ByteArrayContent(bytes),
    };

    private sealed record CapturedRequest(
        HttpMethod Method,
        string EscapedPath,
        string Query,
        string? Body,
        string? Authorization,
        string? ProfileContext);

    private const string CatalogsJson = """
        {"catalogs":[{"addonId":"33333333-3333-4333-8333-333333333333","addonName":"Channels","manifestId":"org.example","position":0,"catalog":{"type":"tv","id":"live","name":"TV","extra":[{"name":"search","isRequired":true}]},"addonCatalog":false,"searchable":true}]}
        """;

    private const string SearchJson = """
        {"results":[{"addonId":"33333333-3333-4333-8333-333333333333","manifestId":"org.example","resource":"catalog","type":"tv specials","id":"live","payload":{"query":"star & moon"},"cache":{"maxAgeSeconds":60},"extra":[{"name":"genre","value":"sci fi"}]}],"errors":[]}
        """;

    private const string LibraryJson = """
        {"items":[{"titleId":"44444444-4444-4444-8444-444444444444","mediaType":"tv","title":"News","available":true,"addedAt":"2026-08-01T00:00:00Z","updatedAt":"2026-08-01T00:00:00Z"}],"page":3,"totalPages":4,"totalResults":80}
        """;

    private const string LibraryItemJson = """
        {"titleId":"44444444-4444-4444-8444-444444444444","mediaType":"tv","title":"News","available":true,"addedAt":"2026-08-01T00:00:00Z","updatedAt":"2026-08-01T00:00:00Z"}
        """;

    private const string CollectionJson = """
        {"id":"11111111-1111-4111-8111-111111111111","title":"Featured","heroEnabled":true,"pinToTop":false,"focusGlowEnabled":true,"viewMode":"rows","folderCoverShape":"landscape","folders":[],"profileIds":[],"categoryIds":[],"position":0,"version":1,"createdAt":"2026-08-01T00:00:00Z","updatedAt":"2026-08-01T00:00:00Z"}
        """;

    private const string CalendarJson = """
        {"events":[{"id":"episode:1","titleId":"44444444-4444-4444-8444-444444444444","mediaType":"episode","title":"Premiere","releaseDate":"2026-08-17","seriesTitle":"Series","seasonNumber":1,"episodeNumber":1}]}
        """;
}
