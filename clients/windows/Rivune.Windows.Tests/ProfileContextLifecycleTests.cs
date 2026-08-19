using System.Collections.Concurrent;
using System.Net;
using System.Text;
using Rivune.Windows;
using Xunit;

namespace Rivune.Windows.Tests;

public sealed class ProfileContextLifecycleTests
{
    private static readonly Guid ProfileId = Guid.Parse("44444444-4444-4444-8444-444444444444");
    private const string DiscoveryBody = """
        {"name":"Rivune","serverVersion":"test","protocolVersion":20,"apiBaseUrl":"/api/v1/","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en"}
        """;
    private const string SelectionBody = """
        {"profile":{"id":"44444444-4444-4444-8444-444444444444","name":"Viewer","description":null,"categoryId":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","category":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Default","color":null,"icon":null},"isChild":false,"hasPin":false,"canManage":true,"enabled":true,"availableFrom":null,"availableUntil":null,"accessStartTime":null,"accessEndTime":null,"accessTimezone":"UTC","accessible":true,"avatar":{"kind":"preset","presetId":"blue","url":"/api/v1/profiles/44444444-4444-4444-8444-444444444444/avatar"}},"expiresAt":"2026-08-15T13:00:00Z","profileContext":"context-one"}
        """;
    private const string RefreshedTokenBody = """
        {"tokenType":"Bearer","accessToken":"refreshed-access","accessTokenExpiresAt":"2026-08-15T13:15:00Z","refreshToken":"refreshed-refresh","refreshTokenExpiresAt":"2026-09-15T12:00:00Z","sessionId":"22222222-2222-4222-8222-222222222222","deviceId":"33333333-3333-4333-8333-333333333333","authorizationScope":"global_admin","category":null}
        """;

    [Fact]
    public async Task SelectionPersistsAndRecreatedClientSendsContext()
    {
        var requests = new ConcurrentQueue<ObservedRequest>();
        var store = new MemoryCredentialStore(Stored());
        Task<HttpResponseMessage> Send(HttpRequestMessage request, CancellationToken _)
        {
            requests.Enqueue(Observe(request));
            return Task.FromResult(ResponseFor(request));
        }

        using (var client = CreateClient(new DelegateHandler(Send), store))
        {
            var selection = await client.SelectProfileAsync(ProfileId, cancellationToken: TestContext.Current.CancellationToken);

            Assert.Equal("context-one", selection.ProfileContext);
            Assert.Equal("context-one", store.Credentials?.ProfileContext);
        }

        using (var recreated = CreateClient(new DelegateHandler(Send), store))
        {
            await recreated.GetCategoriesAsync(TestContext.Current.CancellationToken);
        }

        var select = Assert.Single(requests, request => request.Path.EndsWith("/select", StringComparison.Ordinal));
        Assert.Null(select.ProfileContext);
        var categories = Assert.Single(requests, request => request.Path == "/api/v1/categories");
        Assert.Equal("context-one", categories.ProfileContext);
    }

    [Theory]
    [InlineData(false)]
    [InlineData(true)]
    public async Task CredentialReplacementClearsProfileContext(bool deviceExchange)
    {
        var store = new MemoryCredentialStore(Stored(profileContext: "context-one"));
        var handler = new DelegateHandler((request, _) => Task.FromResult(
            request.RequestUri!.AbsolutePath switch
            {
                "/.well-known/rivune" => JsonResponse(HttpStatusCode.OK, DiscoveryBody),
                "/api/v1/auth/login" => JsonResponse(HttpStatusCode.OK, RefreshedTokenBody),
                "/api/v1/auth/device-code/token" => JsonResponse(HttpStatusCode.OK, RefreshedTokenBody),
                _ => throw new InvalidOperationException(
                    $"Unexpected request path {request.RequestUri.AbsolutePath}."),
            }));
        using var client = CreateClient(handler, store);

        if (deviceExchange)
        {
            await client.ExchangeDeviceAuthorizationAsync(
                "device-code",
                TestContext.Current.CancellationToken);
        }
        else
        {
            await client.LoginAsync(
                "admin",
                "password",
                new LoginDevice { Name = "Windows", Platform = "windows" },
                TestContext.Current.CancellationToken);
        }

        Assert.Equal("refreshed-access", store.Credentials?.Credentials.AccessToken);
        Assert.Null(store.Credentials?.ProfileContext);
    }

    [Fact]
    public async Task RefreshRetainsPersistedContext()
    {
        var store = new MemoryCredentialStore(Stored(profileContext: "context-one"));
        var handler = new DelegateHandler((request, _) => Task.FromResult(ResponseFor(request)));
        using var client = CreateClient(handler, store);

        var refreshed = await client.RefreshSessionAsync(TestContext.Current.CancellationToken);

        Assert.Equal("refreshed-access", refreshed.AccessToken);
        Assert.Equal("refreshed-access", store.Credentials?.Credentials.AccessToken);
        Assert.Equal("context-one", store.Credentials?.ProfileContext);
    }

    [Fact]
    public async Task ClearRemovesPersistedContextAndFutureHeader()
    {
        var requests = new ConcurrentQueue<ObservedRequest>();
        var store = new MemoryCredentialStore(Stored(profileContext: "context-one"));
        var handler = new DelegateHandler((request, _) =>
        {
            requests.Enqueue(Observe(request));
            return Task.FromResult(ResponseFor(request));
        });
        using var client = CreateClient(handler, store);

        await client.GetProfilesAsync(TestContext.Current.CancellationToken);
        await client.ClearProfileSelectionAsync(TestContext.Current.CancellationToken);
        await client.GetCategoriesAsync(TestContext.Current.CancellationToken);

        Assert.Null(store.Credentials?.ProfileContext);
        var profiles = Assert.Single(requests, request => request.Path == "/api/v1/profiles");
        Assert.Null(profiles.ProfileContext);
        var clear = Assert.Single(requests, request => request.Path == "/api/v1/profiles/selection");
        Assert.Null(clear.ProfileContext);
        var categories = Assert.Single(requests, request => request.Path == "/api/v1/categories");
        Assert.Null(categories.ProfileContext);
    }

    [Theory]
    [InlineData(false)]
    [InlineData(true)]
    public async Task DiscoveryCancellationPreventsProfileMutationDispatch(bool clearSelection)
    {
        using var callerCancellation = new CancellationTokenSource();
        var discoveryStarted = NewSignal();
        var mutationRequests = 0;
        var store = new MemoryCredentialStore(Stored(profileContext: clearSelection ? "context-one" : null));
        var handler = new DelegateHandler(async (request, cancellationToken) =>
        {
            if (request.RequestUri!.AbsolutePath == "/.well-known/rivune")
            {
                discoveryStarted.TrySetResult();
                await Task.Delay(Timeout.InfiniteTimeSpan, cancellationToken);
                throw new InvalidOperationException("Cancelled discovery unexpectedly completed.");
            }

            Interlocked.Increment(ref mutationRequests);
            return clearSelection
                ? new HttpResponseMessage(HttpStatusCode.NoContent)
                : JsonResponse(HttpStatusCode.OK, SelectionBody);
        });
        using var client = CreateClient(handler, store);

        Task mutation = clearSelection
            ? client.ClearProfileSelectionAsync(callerCancellation.Token)
            : client.SelectProfileAsync(ProfileId, cancellationToken: callerCancellation.Token);
        await discoveryStarted.Task.WaitAsync(TestContext.Current.CancellationToken);
        callerCancellation.Cancel();

        await Assert.ThrowsAnyAsync<OperationCanceledException>(() => mutation);
        Assert.Equal(0, mutationRequests);
    }

    [Fact]
    public async Task CancellationDuringRefreshPreventsSelectionRetry()
    {
        using var callerCancellation = new CancellationTokenSource();
        var refreshStarted = NewSignal();
        var selectionRequests = 0;
        var store = new MemoryCredentialStore(Stored());
        var handler = new DelegateHandler(async (request, cancellationToken) =>
        {
            switch (request.RequestUri!.AbsolutePath)
            {
                case "/.well-known/rivune":
                    return JsonResponse(HttpStatusCode.OK, DiscoveryBody);
                case var path when path.EndsWith("/select", StringComparison.Ordinal):
                    Interlocked.Increment(ref selectionRequests);
                    return JsonResponse(
                        HttpStatusCode.Unauthorized,
                        "{\"error\":{\"code\":\"unauthorized\",\"message\":\"Unauthorized\"}}");
                case "/api/v1/auth/refresh":
                    refreshStarted.TrySetResult();
                    await Task.Delay(Timeout.InfiniteTimeSpan, cancellationToken);
                    throw new InvalidOperationException("Cancelled refresh unexpectedly completed.");
                default:
                    throw new InvalidOperationException(
                        $"Unexpected request path {request.RequestUri.AbsolutePath}.");
            }
        });
        using var client = CreateClient(handler, store);

        var selection = client.SelectProfileAsync(
            ProfileId,
            cancellationToken: callerCancellation.Token);
        await refreshStarted.Task.WaitAsync(TestContext.Current.CancellationToken);
        callerCancellation.Cancel();

        await Assert.ThrowsAnyAsync<OperationCanceledException>(() => selection);
        Assert.Equal(1, selectionRequests);
    }

    [Fact]
    public async Task BlockedSelectionBlocksProfileScopedRequestUntilContextIsCommitted()
    {
        var selectionStarted = NewSignal();
        var allowSelection = NewSignal();
        var categoriesSeen = NewSignal();
        string? categoriesContext = null;
        var store = new MemoryCredentialStore(Stored());
        var handler = new DelegateHandler(async (request, cancellationToken) =>
        {
            switch (request.RequestUri!.AbsolutePath)
            {
                case "/.well-known/rivune":
                    return JsonResponse(HttpStatusCode.OK, DiscoveryBody);
                case var path when path.EndsWith("/select", StringComparison.Ordinal):
                    selectionStarted.TrySetResult();
                    await allowSelection.Task.WaitAsync(cancellationToken);
                    return JsonResponse(HttpStatusCode.OK, SelectionBody);
                case "/api/v1/categories":
                    categoriesContext = Header(request, "X-Rivune-Profile-Context");
                    categoriesSeen.TrySetResult();
                    return JsonResponse(HttpStatusCode.OK, "{\"categories\":[]}");
                default:
                    throw new InvalidOperationException($"Unexpected request path {request.RequestUri.AbsolutePath}.");
            }
        });
        using var client = CreateClient(handler, store);
        await client.DiscoverAsync(TestContext.Current.CancellationToken);

        var selection = client.SelectProfileAsync(ProfileId, cancellationToken: TestContext.Current.CancellationToken);
        await selectionStarted.Task.WaitAsync(TestContext.Current.CancellationToken);
        var categories = client.GetCategoriesAsync(TestContext.Current.CancellationToken);

        Assert.False(categoriesSeen.Task.IsCompleted);
        Assert.False(categories.IsCompleted);
        allowSelection.TrySetResult();

        await selection;
        await categories;
        Assert.Equal("context-one", categoriesContext);
    }

    [Fact]
    public async Task ProfileScopedReadersDispatchConcurrently()
    {
        var firstReaderStarted = NewSignal();
        var bothReadersStarted = NewSignal();
        var allowReaders = NewSignal();
        var dispatchedReaders = 0;
        var store = new MemoryCredentialStore(Stored(profileContext: "context-one"));
        var handler = new DelegateHandler(async (request, cancellationToken) =>
        {
            switch (request.RequestUri!.AbsolutePath)
            {
                case "/.well-known/rivune":
                    return JsonResponse(HttpStatusCode.OK, DiscoveryBody);
                case "/api/v1/categories":
                    var ordinal = Interlocked.Increment(ref dispatchedReaders);
                    if (ordinal == 1)
                    {
                        firstReaderStarted.TrySetResult();
                    }
                    else if (ordinal == 2)
                    {
                        bothReadersStarted.TrySetResult();
                    }
                    await allowReaders.Task.WaitAsync(cancellationToken);
                    return JsonResponse(HttpStatusCode.OK, "{\"categories\":[]}");
                default:
                    throw new InvalidOperationException(
                        $"Unexpected request path {request.RequestUri.AbsolutePath}.");
            }
        });
        using var client = CreateClient(handler, store);
        await client.DiscoverAsync(TestContext.Current.CancellationToken);

        var firstReader = client.GetCategoriesAsync(TestContext.Current.CancellationToken);
        await firstReaderStarted.Task.WaitAsync(TestContext.Current.CancellationToken);
        var secondReader = client.GetCategoriesAsync(TestContext.Current.CancellationToken);
        await bothReadersStarted.Task.WaitAsync(TestContext.Current.CancellationToken);

        Assert.False(firstReader.IsCompleted);
        Assert.False(secondReader.IsCompleted);
        Assert.Equal(2, Volatile.Read(ref dispatchedReaders));
        allowReaders.TrySetResult();
        await Task.WhenAll(firstReader, secondReader);
    }

    [Fact]
    public async Task ClearDrainsAdmittedReaderAndBlocksNewReaderUntilCommit()
    {
        var oldReaderStarted = NewSignal();
        var allowOldReader = NewSignal();
        var clearStarted = NewSignal();
        var allowClear = NewSignal();
        var newReaderStarted = NewSignal();
        var dispatchedReaders = 0;
        string? oldReaderContext = null;
        string? newReaderContext = "not-observed";
        var store = new MemoryCredentialStore(Stored(profileContext: "context-one"));
        var handler = new DelegateHandler(async (request, cancellationToken) =>
        {
            switch (request.RequestUri!.AbsolutePath)
            {
                case "/.well-known/rivune":
                    return JsonResponse(HttpStatusCode.OK, DiscoveryBody);
                case "/api/v1/categories":
                    if (Interlocked.Increment(ref dispatchedReaders) == 1)
                    {
                        oldReaderContext = Header(request, "X-Rivune-Profile-Context");
                        oldReaderStarted.TrySetResult();
                        await allowOldReader.Task.WaitAsync(cancellationToken);
                    }
                    else
                    {
                        newReaderContext = Header(request, "X-Rivune-Profile-Context");
                        newReaderStarted.TrySetResult();
                    }
                    return JsonResponse(HttpStatusCode.OK, "{\"categories\":[]}");
                case "/api/v1/profiles/selection":
                    clearStarted.TrySetResult();
                    await allowClear.Task.WaitAsync(cancellationToken);
                    return new HttpResponseMessage(HttpStatusCode.NoContent);
                default:
                    throw new InvalidOperationException(
                        $"Unexpected request path {request.RequestUri.AbsolutePath}.");
            }
        });
        using var client = CreateClient(handler, store);
        await client.DiscoverAsync(TestContext.Current.CancellationToken);

        var oldReader = client.GetCategoriesAsync(TestContext.Current.CancellationToken);
        await oldReaderStarted.Task.WaitAsync(TestContext.Current.CancellationToken);
        var clear = client.ClearProfileSelectionAsync(TestContext.Current.CancellationToken);
        var newReader = client.GetCategoriesAsync(TestContext.Current.CancellationToken);

        Assert.False(clearStarted.Task.IsCompleted);
        Assert.False(newReaderStarted.Task.IsCompleted);
        allowOldReader.TrySetResult();
        await oldReader;
        await clearStarted.Task.WaitAsync(TestContext.Current.CancellationToken);
        Assert.False(newReaderStarted.Task.IsCompleted);

        allowClear.TrySetResult();
        await clear;
        await newReaderStarted.Task.WaitAsync(TestContext.Current.CancellationToken);
        await newReader;

        Assert.Equal("context-one", oldReaderContext);
        Assert.Null(store.Credentials?.ProfileContext);
        Assert.Null(newReaderContext);
    }

    [Fact]
    public async Task SelectionDrainsOldContextAndReleasesBlockedReaderUnderNewContext()
    {
        var oldReaderStarted = NewSignal();
        var allowOldReader = NewSignal();
        var selectionStarted = NewSignal();
        var allowSelection = NewSignal();
        var newReaderStarted = NewSignal();
        var dispatchedReaders = 0;
        string? oldReaderContext = null;
        string? newReaderContext = null;
        var store = new MemoryCredentialStore(Stored(profileContext: "context-one"));
        var handler = new DelegateHandler(async (request, cancellationToken) =>
        {
            switch (request.RequestUri!.AbsolutePath)
            {
                case "/.well-known/rivune":
                    return JsonResponse(HttpStatusCode.OK, DiscoveryBody);
                case "/api/v1/categories":
                    if (Interlocked.Increment(ref dispatchedReaders) == 1)
                    {
                        oldReaderContext = Header(request, "X-Rivune-Profile-Context");
                        oldReaderStarted.TrySetResult();
                        await allowOldReader.Task.WaitAsync(cancellationToken);
                    }
                    else
                    {
                        newReaderContext = Header(request, "X-Rivune-Profile-Context");
                        newReaderStarted.TrySetResult();
                    }
                    return JsonResponse(HttpStatusCode.OK, "{\"categories\":[]}");
                case var path when path.EndsWith("/select", StringComparison.Ordinal):
                    selectionStarted.TrySetResult();
                    await allowSelection.Task.WaitAsync(cancellationToken);
                    return JsonResponse(
                        HttpStatusCode.OK,
                        SelectionBody.Replace("context-one", "context-two", StringComparison.Ordinal));
                default:
                    throw new InvalidOperationException(
                        $"Unexpected request path {request.RequestUri.AbsolutePath}.");
            }
        });
        using var client = CreateClient(handler, store);
        await client.DiscoverAsync(TestContext.Current.CancellationToken);

        var oldReader = client.GetCategoriesAsync(TestContext.Current.CancellationToken);
        await oldReaderStarted.Task.WaitAsync(TestContext.Current.CancellationToken);
        var selection = client.SelectProfileAsync(
            ProfileId,
            cancellationToken: TestContext.Current.CancellationToken);
        var newReader = client.GetCategoriesAsync(TestContext.Current.CancellationToken);

        Assert.False(selectionStarted.Task.IsCompleted);
        Assert.False(newReaderStarted.Task.IsCompleted);
        allowOldReader.TrySetResult();
        await oldReader;
        await selectionStarted.Task.WaitAsync(TestContext.Current.CancellationToken);
        Assert.False(newReaderStarted.Task.IsCompleted);

        allowSelection.TrySetResult();
        await selection;
        await newReaderStarted.Task.WaitAsync(TestContext.Current.CancellationToken);
        await newReader;

        Assert.Equal("context-one", oldReaderContext);
        Assert.Equal("context-two", store.Credentials?.ProfileContext);
        Assert.Equal("context-two", newReaderContext);
    }

    [Fact]
    public async Task ProfileReaderRetainsSnapshotAndLeaseThroughRefreshRetry()
    {
        var refreshStarted = NewSignal();
        var allowRefresh = NewSignal();
        var retryStarted = NewSignal();
        var allowRetryResponse = NewSignal();
        var clearStarted = NewSignal();
        var categoryRequests = 0;
        string? initialContext = null;
        string? initialToken = null;
        string? retryContext = null;
        string? retryToken = null;
        var store = new MemoryCredentialStore(Stored(profileContext: "context-one"));
        var handler = new DelegateHandler(async (request, cancellationToken) =>
        {
            switch (request.RequestUri!.AbsolutePath)
            {
                case "/.well-known/rivune":
                    return JsonResponse(HttpStatusCode.OK, DiscoveryBody);
                case "/api/v1/categories":
                    if (Interlocked.Increment(ref categoryRequests) == 1)
                    {
                        initialContext = Header(request, "X-Rivune-Profile-Context");
                        initialToken = request.Headers.Authorization?.Parameter;
                        return JsonResponse(
                            HttpStatusCode.Unauthorized,
                            "{\"error\":{\"code\":\"unauthorized\",\"message\":\"Unauthorized\"}}");
                    }
                    retryContext = Header(request, "X-Rivune-Profile-Context");
                    retryToken = request.Headers.Authorization?.Parameter;
                    retryStarted.TrySetResult();
                    await allowRetryResponse.Task.WaitAsync(cancellationToken);
                    return JsonResponse(HttpStatusCode.OK, "{\"categories\":[]}");
                case "/api/v1/auth/refresh":
                    refreshStarted.TrySetResult();
                    await allowRefresh.Task.WaitAsync(cancellationToken);
                    return JsonResponse(HttpStatusCode.OK, RefreshedTokenBody);
                case "/api/v1/profiles/selection":
                    clearStarted.TrySetResult();
                    return new HttpResponseMessage(HttpStatusCode.NoContent);
                default:
                    throw new InvalidOperationException(
                        $"Unexpected request path {request.RequestUri.AbsolutePath}.");
            }
        });
        using var client = CreateClient(handler, store);
        await client.DiscoverAsync(TestContext.Current.CancellationToken);

        var reader = client.GetCategoriesAsync(TestContext.Current.CancellationToken);
        await refreshStarted.Task.WaitAsync(TestContext.Current.CancellationToken);
        var clear = client.ClearProfileSelectionAsync(TestContext.Current.CancellationToken);
        Assert.False(clearStarted.Task.IsCompleted);

        allowRefresh.TrySetResult();
        await retryStarted.Task.WaitAsync(TestContext.Current.CancellationToken);
        Assert.False(clearStarted.Task.IsCompleted);
        Assert.Equal("context-one", initialContext);
        Assert.Equal("old-access", initialToken);
        Assert.Equal("context-one", retryContext);
        Assert.Equal("refreshed-access", retryToken);

        allowRetryResponse.TrySetResult();
        await reader;
        await clearStarted.Task.WaitAsync(TestContext.Current.CancellationToken);
        await clear;
        Assert.Null(store.Credentials?.ProfileContext);
    }

    [Fact]
    public async Task CanceledWaitingMutationReopensReaderAdmission()
    {
        using var mutationCancellation = new CancellationTokenSource();
        var oldReaderStarted = NewSignal();
        var allowOldReader = NewSignal();
        var newReaderStarted = NewSignal();
        var dispatchedReaders = 0;
        var store = new MemoryCredentialStore(Stored(profileContext: "context-one"));
        var handler = new DelegateHandler(async (request, cancellationToken) =>
        {
            switch (request.RequestUri!.AbsolutePath)
            {
                case "/.well-known/rivune":
                    return JsonResponse(HttpStatusCode.OK, DiscoveryBody);
                case "/api/v1/categories":
                    if (Interlocked.Increment(ref dispatchedReaders) == 1)
                    {
                        oldReaderStarted.TrySetResult();
                        await allowOldReader.Task.WaitAsync(cancellationToken);
                    }
                    else
                    {
                        newReaderStarted.TrySetResult();
                    }
                    return JsonResponse(HttpStatusCode.OK, "{\"categories\":[]}");
                default:
                    throw new InvalidOperationException(
                        $"Unexpected request path {request.RequestUri.AbsolutePath}.");
            }
        });
        using var client = CreateClient(handler, store);
        await client.DiscoverAsync(TestContext.Current.CancellationToken);

        var oldReader = client.GetCategoriesAsync(TestContext.Current.CancellationToken);
        await oldReaderStarted.Task.WaitAsync(TestContext.Current.CancellationToken);
        var mutation = client.ClearProfileSelectionAsync(mutationCancellation.Token);
        var newReader = client.GetCategoriesAsync(TestContext.Current.CancellationToken);
        Assert.False(newReaderStarted.Task.IsCompleted);

        mutationCancellation.Cancel();
        await Assert.ThrowsAnyAsync<OperationCanceledException>(() => mutation);
        await newReaderStarted.Task.WaitAsync(TestContext.Current.CancellationToken);
        await newReader;

        allowOldReader.TrySetResult();
        await oldReader;
    }

    [Theory]
    [InlineData(false)]
    [InlineData(true)]
    public async Task AuthenticationBoundaryDoesNotWaitForProfileReader(bool credentialReplacement)
    {
        var readerStarted = NewSignal();
        var allowReader = NewSignal();
        var authenticationRequestStarted = NewSignal();
        var store = new MemoryCredentialStore(Stored(profileContext: "context-one"));
        var handler = new DelegateHandler(async (request, cancellationToken) =>
        {
            switch (request.RequestUri!.AbsolutePath)
            {
                case "/.well-known/rivune":
                    return JsonResponse(HttpStatusCode.OK, DiscoveryBody);
                case "/api/v1/categories":
                    readerStarted.TrySetResult();
                    await allowReader.Task.WaitAsync(cancellationToken);
                    return JsonResponse(HttpStatusCode.OK, "{\"categories\":[]}");
                case "/api/v1/auth/logout":
                    authenticationRequestStarted.TrySetResult();
                    return new HttpResponseMessage(HttpStatusCode.NoContent);
                case "/api/v1/auth/device-code/token":
                    authenticationRequestStarted.TrySetResult();
                    return JsonResponse(HttpStatusCode.OK, RefreshedTokenBody);
                default:
                    throw new InvalidOperationException(
                        $"Unexpected request path {request.RequestUri.AbsolutePath}.");
            }
        });
        using var client = CreateClient(handler, store);
        await client.DiscoverAsync(TestContext.Current.CancellationToken);

        var reader = client.GetCategoriesAsync(TestContext.Current.CancellationToken);
        await readerStarted.Task.WaitAsync(TestContext.Current.CancellationToken);
        Task authenticationChange = credentialReplacement
            ? client.ExchangeDeviceAuthorizationAsync(
                "device-code",
                TestContext.Current.CancellationToken)
            : client.LogoutAsync(TestContext.Current.CancellationToken);

        await authenticationRequestStarted.Task.WaitAsync(TestContext.Current.CancellationToken);
        await authenticationChange;
        Assert.False(reader.IsCompleted);
        Assert.Null(store.Credentials?.ProfileContext);

        allowReader.TrySetResult();
        await reader;
    }
    [Fact]
    public async Task FailedMutationReleasesBlockedReader()
    {
        var selectionStarted = NewSignal();
        var allowSelectionFailure = NewSignal();
        var readerStarted = NewSignal();
        var store = new MemoryCredentialStore(Stored(profileContext: "context-one"));
        var handler = new DelegateHandler(async (request, cancellationToken) =>
        {
            switch (request.RequestUri!.AbsolutePath)
            {
                case "/.well-known/rivune":
                    return JsonResponse(HttpStatusCode.OK, DiscoveryBody);
                case var path when path.EndsWith("/select", StringComparison.Ordinal):
                    selectionStarted.TrySetResult();
                    await allowSelectionFailure.Task.WaitAsync(cancellationToken);
                    return JsonResponse(
                        HttpStatusCode.BadRequest,
                        "{\"error\":{\"code\":\"invalid_profile\",\"message\":\"Invalid profile\"}}");
                case "/api/v1/categories":
                    readerStarted.TrySetResult();
                    return JsonResponse(HttpStatusCode.OK, "{\"categories\":[]}");
                default:
                    throw new InvalidOperationException(
                        $"Unexpected request path {request.RequestUri.AbsolutePath}.");
            }
        });
        using var client = CreateClient(handler, store);
        await client.DiscoverAsync(TestContext.Current.CancellationToken);

        var selection = client.SelectProfileAsync(
            ProfileId,
            cancellationToken: TestContext.Current.CancellationToken);
        await selectionStarted.Task.WaitAsync(TestContext.Current.CancellationToken);
        var reader = client.GetCategoriesAsync(TestContext.Current.CancellationToken);
        Assert.False(readerStarted.Task.IsCompleted);

        allowSelectionFailure.TrySetResult();
        await Assert.ThrowsAsync<RivuneServerException>(() => selection);
        await readerStarted.Task.WaitAsync(TestContext.Current.CancellationToken);
        await reader;
    }

    [Fact]
    public async Task SelectionCancellationAtResponseReconcilesPersistedAndInMemoryContext()
    {
        using var callerCancellation = new CancellationTokenSource();
        string? categoriesContext = null;
        var store = new MemoryCredentialStore(Stored());
        var handler = new DelegateHandler((request, _) => Task.FromResult(
            request.RequestUri!.AbsolutePath switch
            {
                "/.well-known/rivune" => JsonResponse(HttpStatusCode.OK, DiscoveryBody),
                var path when path.EndsWith("/select", StringComparison.Ordinal) => CancelAndRespond(
                    callerCancellation,
                    JsonResponse(HttpStatusCode.OK, SelectionBody)),
                "/api/v1/categories" => RecordCategoriesContextAndRespond(request),
                _ => throw new InvalidOperationException(
                    $"Unexpected request path {request.RequestUri.AbsolutePath}."),
            }));
        using var client = CreateClient(handler, store);

        await Assert.ThrowsAnyAsync<OperationCanceledException>(() =>
            client.SelectProfileAsync(ProfileId, cancellationToken: callerCancellation.Token));
        Assert.Equal("context-one", store.Credentials?.ProfileContext);

        await client.GetCategoriesAsync(TestContext.Current.CancellationToken);
        Assert.Equal("context-one", categoriesContext);

        HttpResponseMessage RecordCategoriesContextAndRespond(HttpRequestMessage request)
        {
            categoriesContext = Header(request, "X-Rivune-Profile-Context");
            return JsonResponse(HttpStatusCode.OK, "{\"categories\":[]}");
        }
    }

    [Fact]
    public async Task ClearCancellationAtResponseReconcilesPersistedAndInMemoryContext()
    {
        using var callerCancellation = new CancellationTokenSource();
        string? categoriesContext = "not-observed";
        var store = new MemoryCredentialStore(Stored(profileContext: "context-one"));
        var handler = new DelegateHandler((request, _) => Task.FromResult(
            request.RequestUri!.AbsolutePath switch
            {
                "/.well-known/rivune" => JsonResponse(HttpStatusCode.OK, DiscoveryBody),
                "/api/v1/profiles/selection" => CancelAndRespond(
                    callerCancellation,
                    new HttpResponseMessage(HttpStatusCode.NoContent)),
                "/api/v1/categories" => RecordCategoriesContextAndRespond(request),
                _ => throw new InvalidOperationException(
                    $"Unexpected request path {request.RequestUri.AbsolutePath}."),
            }));
        using var client = CreateClient(handler, store);

        await Assert.ThrowsAnyAsync<OperationCanceledException>(() =>
            client.ClearProfileSelectionAsync(callerCancellation.Token));
        Assert.Null(store.Credentials?.ProfileContext);

        await client.GetCategoriesAsync(TestContext.Current.CancellationToken);
        Assert.Null(categoriesContext);

        HttpResponseMessage RecordCategoriesContextAndRespond(HttpRequestMessage request)
        {
            categoriesContext = Header(request, "X-Rivune-Profile-Context");
            return JsonResponse(HttpStatusCode.OK, "{\"categories\":[]}");
        }
    }

    [Fact]
    public async Task SelectionResponseAfterLogoutCannotRepublishContext()
    {
        var selectionStarted = NewSignal();
        var allowSelection = NewSignal();
        var store = new MemoryCredentialStore(Stored());
        var handler = new DelegateHandler(async (request, cancellationToken) =>
        {
            switch (request.RequestUri!.AbsolutePath)
            {
                case "/.well-known/rivune":
                    return JsonResponse(HttpStatusCode.OK, DiscoveryBody);
                case var path when path.EndsWith("/select", StringComparison.Ordinal):
                    selectionStarted.TrySetResult();
                    await allowSelection.Task.WaitAsync(cancellationToken);
                    return JsonResponse(HttpStatusCode.OK, SelectionBody);
                case "/api/v1/auth/logout":
                    return new HttpResponseMessage(HttpStatusCode.NoContent);
                default:
                    throw new InvalidOperationException(
                        $"Unexpected request path {request.RequestUri.AbsolutePath}.");
            }
        });
        using var client = CreateClient(handler, store);
        await client.DiscoverAsync(TestContext.Current.CancellationToken);

        var selection = client.SelectProfileAsync(
            ProfileId,
            cancellationToken: TestContext.Current.CancellationToken);
        await selectionStarted.Task.WaitAsync(TestContext.Current.CancellationToken);
        await client.LogoutAsync(TestContext.Current.CancellationToken);
        allowSelection.TrySetResult();

        await Assert.ThrowsAsync<NotAuthenticatedException>(() => selection);
        Assert.Null(store.Credentials);
    }

    private static RivuneApiClient CreateClient(HttpMessageHandler handler, ICredentialStore store) =>
        new("https://rivune.test", handler, store);

    private static HttpResponseMessage ResponseFor(HttpRequestMessage request) => request.RequestUri!.AbsolutePath switch
    {
        "/.well-known/rivune" => JsonResponse(HttpStatusCode.OK, DiscoveryBody),
        "/api/v1/auth/refresh" => JsonResponse(HttpStatusCode.OK, RefreshedTokenBody),
        "/api/v1/profiles/selection" => new HttpResponseMessage(HttpStatusCode.NoContent),
        "/api/v1/profiles" => JsonResponse(HttpStatusCode.OK, "{\"profiles\":[]}"),
        "/api/v1/categories" => JsonResponse(HttpStatusCode.OK, "{\"categories\":[]}"),
        var path when path.EndsWith("/select", StringComparison.Ordinal) =>
            JsonResponse(HttpStatusCode.OK, SelectionBody),
        _ => throw new InvalidOperationException($"Unexpected request path {request.RequestUri.AbsolutePath}."),
    };
    private static HttpResponseMessage CancelAndRespond(
        CancellationTokenSource cancellation,
        HttpResponseMessage response)
    {
        cancellation.Cancel();
        return response;
    }


    private static ObservedRequest Observe(HttpRequestMessage request) => new(
        request.RequestUri!.AbsolutePath,
        Header(request, "X-Rivune-Profile-Context"));

    private static string? Header(HttpRequestMessage request, string name) =>
        request.Headers.TryGetValues(name, out var values) ? Assert.Single(values) : null;

    private static TaskCompletionSource NewSignal() =>
        new(TaskCreationOptions.RunContinuationsAsynchronously);

    private static HttpResponseMessage JsonResponse(HttpStatusCode statusCode, string body) => new(statusCode)
    {
        Content = new StringContent(body, Encoding.UTF8, "application/json"),
    };

    private static StoredCredentials Stored(string? profileContext = null) => new()
    {
        Issuer = "https://rivune.test/",
        Credentials = new TokenPair
        {
            TokenType = "Bearer",
            AccessToken = "old-access",
            AccessTokenExpiresAt = "2026-08-15T12:15:00Z",
            RefreshToken = "old-refresh",
            RefreshTokenExpiresAt = "2026-09-15T12:00:00Z",
            SessionId = Guid.Parse("22222222-2222-4222-8222-222222222222"),
            DeviceId = Guid.Parse("33333333-3333-4333-8333-333333333333"),
            AuthorizationScope = AuthorizationScope.GlobalAdministrator,
            Category = null,
        },
        ProfileContext = profileContext,
    };

    private sealed record ObservedRequest(string Path, string? ProfileContext);

    private sealed class DelegateHandler(
        Func<HttpRequestMessage, CancellationToken, Task<HttpResponseMessage>> send) : HttpMessageHandler
    {
        protected override Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken) => send(request, cancellationToken);
    }

    private sealed class MemoryCredentialStore(StoredCredentials? credentials) : ICredentialStore
    {
        private readonly object _sync = new();
        private StoredCredentials? _credentials = credentials;

        public StoredCredentials? Credentials
        {
            get
            {
                lock (_sync)
                {
                    return _credentials;
                }
            }
        }

        public ValueTask<StoredCredentials?> LoadAsync(CancellationToken cancellationToken = default)
        {
            lock (_sync)
            {
                return ValueTask.FromResult(_credentials);
            }
        }

        public ValueTask SaveAsync(
            StoredCredentials credentials,
            CancellationToken cancellationToken = default)
        {
            lock (_sync)
            {
                _credentials = credentials;
            }
            return ValueTask.CompletedTask;
        }

        public ValueTask ClearAsync(CancellationToken cancellationToken = default)
        {
            lock (_sync)
            {
                _credentials = null;
            }
            return ValueTask.CompletedTask;
        }
    }
}
