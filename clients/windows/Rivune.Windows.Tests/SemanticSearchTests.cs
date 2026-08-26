using System.Diagnostics;
using System.Net;
using System.Text;
using System.Text.Json;
using Rivune.App;
using Rivune.Windows;
using Xunit;

namespace Rivune.Windows.Tests;

public sealed class SemanticSearchTests
{
    [Fact]
    public async Task ApiContractUsesAuthenticatedSemanticSearchRouteAndPreservesResponse()
    {
        var handler = new SemanticHandler();
        using var client = new RivuneApiClient("https://rivune.test", handler, new FixedCredentialStore());

        var page = await client.SemanticSearchAsync(new SemanticSearchRequest
        {
            Query = "film Dune de guerre",
            MediaType = "movie",
            Language = "fr-CA",
            Region = "CA",
            Page = 2,
            Limit = 40,
            ExcludedIntentIds = ["genre:war"],
        }, TestContext.Current.CancellationToken);

        Assert.Equal("Dune guerre", page.TitleQuery);
        Assert.Equal("media_type:movie", Assert.Single(page.Intents).Id);
        Assert.Equal("42", Assert.Single(page.Items).ExternalIds["tmdb"]);
        Assert.Equal(2, page.Page);
        Assert.False(page.HasMore);
        Assert.True(page.Partial);

        var request = Assert.Single(handler.Requests);
        Assert.Equal(HttpMethod.Post, request.Method);
        Assert.Equal("/api/v1/search/semantic", request.Path);
        Assert.Equal("Bearer access", request.Authorization);
        Assert.Equal("profile-context", request.ProfileContext);
        using var body = JsonDocument.Parse(request.Body);
        Assert.Equal("film Dune de guerre", body.RootElement.GetProperty("query").GetString());
        Assert.Equal("movie", body.RootElement.GetProperty("mediaType").GetString());
        Assert.Equal("fr-CA", body.RootElement.GetProperty("language").GetString());
        Assert.Equal("CA", body.RootElement.GetProperty("region").GetString());
        Assert.Equal(2, body.RootElement.GetProperty("page").GetInt32());
        Assert.Equal(40, body.RootElement.GetProperty("limit").GetInt32());
        Assert.Equal("genre:war", Assert.Single(body.RootElement.GetProperty("excludedIntentIds").EnumerateArray()).GetString());
    }

    [Fact]
    public async Task SemanticDeadlineCancelsAssistanceAndReturnsFallbackOutcome()
    {
        var cancellationObserved = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var neverCompletes = new TaskCompletionSource<SemanticSearchPage>(TaskCreationOptions.RunContinuationsAsynchronously);
        var stopwatch = Stopwatch.StartNew();

        var outcome = await SemanticSearchPolicy.FetchAsync(
            enabled: true,
            token =>
            {
                token.Register(() => cancellationObserved.TrySetResult());
                return neverCompletes.Task;
            },
            TestContext.Current.CancellationToken,
            TimeSpan.FromMilliseconds(30));

        stopwatch.Stop();
        Assert.Null(outcome.Page);
        Assert.True(outcome.Failed);
        Assert.True(stopwatch.Elapsed < TimeSpan.FromSeconds(1));
        await cancellationObserved.Task.WaitAsync(TestContext.Current.CancellationToken);
        Assert.Equal(TimeSpan.FromSeconds(12), SemanticSearchPolicy.DefaultDeadline);
    }

    [Fact]
    public async Task AddonSearchStartsBeforeSemanticDeadlineAndIsReusedOnTimeout()
    {
        var addonStarted = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var semanticNeverCompletes = new TaskCompletionSource<SemanticSearchPage>(TaskCreationOptions.RunContinuationsAsynchronously);
        var progressReported = new TaskCompletionSource<string>(TaskCreationOptions.RunContinuationsAsynchronously);

        var search = SemanticSearchPolicy.SearchAddonsAsync(
            semanticEnabled: true,
            configuredTypes: ["movie", "series"],
            originalQuery: "original",
            semanticFetch: _ => semanticNeverCompletes.Task,
            addonFetch: (types, query, _) =>
            {
                addonStarted.TrySetResult();
                return Task.FromResult($"{string.Join(',', types)}:{query}");
            },
            cancellationToken: TestContext.Current.CancellationToken,
            semanticDeadline: TimeSpan.FromMilliseconds(40),
            progress: new InlineProgress<string>(value => progressReported.TrySetResult(value)));

        await addonStarted.Task.WaitAsync(TestContext.Current.CancellationToken);
        Assert.Equal("movie,series:original", await progressReported.Task.WaitAsync(TestContext.Current.CancellationToken));
        Assert.False(search.IsCompleted);
        var outcome = await search;

        Assert.True(outcome.Semantic.Failed);
        Assert.Equal("movie,series:original", outcome.Addon);
    }

    [Fact]
    public async Task SemanticResultsAreReportedWhileAddonSearchContinues()
    {
        using var caller = new CancellationTokenSource();
        var semanticReported = new TaskCompletionSource<SemanticSearchOutcome>(TaskCreationOptions.RunContinuationsAsynchronously);
        var addonStarted = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);

        var search = SemanticSearchPolicy.SearchAddonsAsync(
            semanticEnabled: true,
            configuredTypes: ["movie"],
            originalQuery: "Dune",
            semanticFetch: _ => Task.FromResult(Page("Dune", ["movie"], [Item("tmdb:1", "Semantic first", new Dictionary<string, string> { ["tmdb"] = "1" })])),
            addonFetch: async (_, _, token) =>
            {
                addonStarted.TrySetResult();
                await Task.Delay(Timeout.InfiniteTimeSpan, token);
                return "unexpected";
            },
            cancellationToken: caller.Token,
            semanticProgress: new InlineProgress<SemanticSearchOutcome>(value => semanticReported.TrySetResult(value)));

        await addonStarted.Task.WaitAsync(TestContext.Current.CancellationToken);
        var progressive = await semanticReported.Task.WaitAsync(TestContext.Current.CancellationToken);
        Assert.Equal("Semantic first", Assert.Single(progressive.Page!.Items).Title);
        Assert.False(search.IsCompleted);
        caller.Cancel();
        await Assert.ThrowsAnyAsync<OperationCanceledException>(() => search);
    }

    [Fact]
    public async Task ChangedSemanticPlanCancelsAndDrainsSpeculationBeforeResidualSearch()
    {
        var speculativeCancelled = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var allowDrain = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var residualStarted = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var invocations = 0;

        async Task<string> Addons(IReadOnlyList<string> types, string query, CancellationToken token)
        {
            if (Interlocked.Increment(ref invocations) == 1)
            {
                try
                {
                    await Task.Delay(Timeout.InfiniteTimeSpan, token);
                }
                catch (OperationCanceledException)
                {
                    speculativeCancelled.TrySetResult();
                    await allowDrain.Task;
                    throw;
                }
                throw new InvalidOperationException("Speculative search unexpectedly completed.");
            }

            residualStarted.TrySetResult();
            return $"{string.Join(',', types)}:{query}";
        }

        var search = SemanticSearchPolicy.SearchAddonsAsync(
            semanticEnabled: true,
            configuredTypes: ["movie", "series"],
            originalQuery: "film Dune de guerre",
            semanticFetch: _ => Task.FromResult(Page("Dune", ["movie"], [])),
            addonFetch: Addons,
            cancellationToken: TestContext.Current.CancellationToken);

        await speculativeCancelled.Task.WaitAsync(TestContext.Current.CancellationToken);
        Assert.False(residualStarted.Task.IsCompleted);
        Assert.Equal(1, Volatile.Read(ref invocations));
        allowDrain.TrySetResult();

        var outcome = await search;
        Assert.Equal("movie:Dune", outcome.Addon);
        Assert.True(residualStarted.Task.IsCompletedSuccessfully);
        Assert.Equal(2, invocations);
    }

    [Fact]
    public async Task CallerCancellationCancelsSemanticAndSpeculativeAddonWithoutFallback()
    {
        using var caller = new CancellationTokenSource();
        var semanticCancelled = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var addonCancelled = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var allowAddonDrain = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);

        var search = SemanticSearchPolicy.SearchAddonsAsync(
            semanticEnabled: true,
            configuredTypes: ["movie"],
            originalQuery: "Dune",
            semanticFetch: async token =>
            {
                token.Register(() => semanticCancelled.TrySetResult());
                await Task.Delay(Timeout.InfiniteTimeSpan, token);
                throw new InvalidOperationException("Semantic request unexpectedly completed.");
            },
            addonFetch: async (_, _, token) =>
            {
                try
                {
                    await Task.Delay(Timeout.InfiniteTimeSpan, token);
                }
                catch (OperationCanceledException)
                {
                    addonCancelled.TrySetResult();
                    await allowAddonDrain.Task;
                    throw;
                }
                return "unexpected";
            },
            cancellationToken: caller.Token);

        caller.Cancel();
        await semanticCancelled.Task.WaitAsync(TestContext.Current.CancellationToken);
        await addonCancelled.Task.WaitAsync(TestContext.Current.CancellationToken);
        Assert.False(search.IsCompleted);
        allowAddonDrain.TrySetResult();
        await Assert.ThrowsAnyAsync<OperationCanceledException>(() => search);
    }

    [Fact]
    public async Task SemanticFailureFallsBackButCallerCancellationPropagates()
    {
        var failed = await SemanticSearchPolicy.FetchAsync(
            enabled: true,
            _ => Task.FromException<SemanticSearchPage>(new RivuneServerException(404, "not_found", "Not found")),
            TestContext.Current.CancellationToken);

        Assert.Null(failed.Page);
        Assert.True(failed.Failed);

        using var caller = new CancellationTokenSource();
        var pending = SemanticSearchPolicy.FetchAsync(
            enabled: true,
            async token =>
            {
                await Task.Delay(Timeout.InfiniteTimeSpan, token);
                throw new InvalidOperationException("Cancelled semantic work unexpectedly completed.");
            },
            caller.Token,
            TimeSpan.FromSeconds(5));
        caller.Cancel();

        await Assert.ThrowsAnyAsync<OperationCanceledException>(() => pending);
    }

    [Fact]
    public void SuccessfulSemanticPlanRestrictsIntersectingTypesAndMergesDirectFirst()
    {
        var semanticPage = Page(
            titleQuery: "Dune",
            mediaTypes: ["movie", "unsupported"],
            items:
            [
                Item("tmdb:1", "Semantic duplicate", new Dictionary<string, string>
                {
                    ["tmdb"] = "1",
                    ["imdb"] = "tt-duplicate",
                }),
                Item("tmdb:2", "Semantic unique", new Dictionary<string, string> { ["tmdb"] = "2" }),
            ]);
        var configured = new[] { "movie", "series" };
        var types = SemanticSearchPolicy.SelectTypes(configured, semanticPage.MediaTypes);
        var direct = new[]
        {
            Target("tt-duplicate", "Direct title"),
            Target("direct-2", "Direct unique"),
        };

        var merged = SemanticSearchPolicy.Merge(
            [],
            direct,
            semanticPage.Items.Select(item => item.ToMediaTarget()));

        Assert.Equal(new[] { "movie" }, types);
        Assert.Equal("Dune", SemanticSearchPolicy.AddonQuery("film Dune de guerre", semanticPage));
        Assert.Equal(new[] { "Direct title", "Direct unique", "Semantic unique" }, merged.Select(item => item.Title));
    }

    [Fact]
    public void NoInferredTypeIntersectionAndShortResidualKeepOrdinarySearchConfiguration()
    {
        var configured = new[] { "movie", "series" };
        var page = Page(titleQuery: " ", mediaTypes: ["episode"], items: []);

        Assert.Same(configured, SemanticSearchPolicy.SelectTypes(configured, page.MediaTypes));
        Assert.Equal("original query", SemanticSearchPolicy.AddonQuery("original query", page));
    }

    [Fact]
    public async Task SemanticRequestRejectsInvalidPagingBeforeDispatch()
    {
        var handler = new SemanticHandler();
        using var client = new RivuneApiClient("https://rivune.test", handler, new FixedCredentialStore());

        await Assert.ThrowsAsync<ArgumentOutOfRangeException>(() => client.SemanticSearchAsync(new SemanticSearchRequest
        {
            Query = "Dune",
            Page = 0,
            Limit = 24,
        }, TestContext.Current.CancellationToken));
        await Assert.ThrowsAsync<ArgumentOutOfRangeException>(() => client.SemanticSearchAsync(new SemanticSearchRequest
        {
            Query = "Dune",
            Page = 1,
            Limit = 41,
        }, TestContext.Current.CancellationToken));
        Assert.Empty(handler.Requests);
    }

    private static SemanticSearchPage Page(
        string titleQuery,
        IReadOnlyList<string> mediaTypes,
        IReadOnlyList<CollectionItem> items) => new()
        {
            Intents = [new SemanticSearchIntent { Id = "media_type:movie", Kind = "media_type", Value = "movie", Label = "Movies" }],
            TitleQuery = titleQuery,
            MediaTypes = mediaTypes,
            Items = items,
            Page = 1,
            HasMore = false,
            Partial = false,
        };

    private static CollectionItem Item(string id, string title, IReadOnlyDictionary<string, string> externalIds) => new()
    {
        Id = id,
        MediaType = "movie",
        Title = title,
        ExternalIds = externalIds,
        Sources = [],
    };

    private static MediaTarget Target(string id, string title) => new()
    {
        Id = id,
        ResourceId = id,
        MediaType = "movie",
        Title = title,
    };

    private sealed class SemanticHandler : HttpMessageHandler
    {
        public List<CapturedRequest> Requests { get; } = [];

        protected override async Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken)
        {
            if (request.RequestUri!.AbsolutePath == "/.well-known/rivune")
            {
                return JsonResponse("""
                    {"name":"Rivune","serverVersion":"test","protocolVersion":22,"apiBaseUrl":"/api/v1/","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en","capabilities":["semantic-search"]}
                    """);
            }

            Requests.Add(new CapturedRequest(
                request.Method,
                request.RequestUri.AbsolutePath,
                request.Content is null ? string.Empty : await request.Content.ReadAsStringAsync(cancellationToken),
                request.Headers.Authorization?.ToString(),
                Header(request, "X-Rivune-Profile-Context")));
            return JsonResponse("""
                {"intents":[{"id":"media_type:movie","kind":"media_type","value":"movie","label":"Movies"}],"titleQuery":"Dune guerre","mediaTypes":["movie"],"items":[{"id":"tmdb:42","mediaType":"movie","title":"Dune","externalIds":{"tmdb":"42"},"sources":[]}],"page":2,"hasMore":false,"partial":true}
                """);
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
                    SessionId = Guid.Parse("66666666-6666-4666-8666-666666666666"),
                    DeviceId = Guid.Parse("77777777-7777-4777-8777-777777777777"),
                    AuthorizationScope = AuthorizationScope.GlobalAdministrator,
                    Category = null,
                },
            });

        public ValueTask SaveAsync(StoredCredentials credentials, CancellationToken cancellationToken = default) =>
            ValueTask.CompletedTask;

        public ValueTask ClearAsync(CancellationToken cancellationToken = default) => ValueTask.CompletedTask;
    }

    private static string? Header(HttpRequestMessage request, string name) =>
        request.Headers.TryGetValues(name, out var values) ? Assert.Single(values) : null;

    private static HttpResponseMessage JsonResponse(string json) => new(HttpStatusCode.OK)
    {
        Content = new StringContent(json, Encoding.UTF8, "application/json"),
    };

    private sealed class InlineProgress<T>(Action<T> report) : IProgress<T>
    {
        public void Report(T value) => report(value);
    }

    private sealed record CapturedRequest(
        HttpMethod Method,
        string Path,
        string Body,
        string? Authorization,
        string? ProfileContext);
}
