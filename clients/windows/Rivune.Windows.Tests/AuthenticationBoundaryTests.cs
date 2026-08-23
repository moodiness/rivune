using System.Net;
using System.Text;
using Rivune.Windows;
using Xunit;

namespace Rivune.Windows.Tests;

public sealed class AuthenticationBoundaryTests
{
    private const int MaximumResponseBodyBytes = 16 * 1024 * 1024;
    private const string DiscoveryBody = """
        {"name":"Rivune","serverVersion":"test","protocolVersion":20,"apiBaseUrl":"/api/v1/","setupRequired":false,"setupCompleted":true,"demoAvailable":false,"timezone":"UTC","interfaceLanguage":"en"}
        """;
    private const string TokenBody = """
        {"tokenType":"Bearer","accessToken":"new-access","accessTokenExpiresAt":"2026-08-05T12:15:00Z","refreshToken":"new-refresh","refreshTokenExpiresAt":"2026-09-05T12:00:00Z","sessionId":"22222222-2222-4222-8222-222222222222","deviceId":"33333333-3333-4333-8333-333333333333","authorizationScope":"global_admin","category":null}
        """;

    [Theory]
    [InlineData(false)]
    [InlineData(true)]
    public async Task LogoutRejectsDelayedAuthenticationWrite(bool deviceExchange)
    {
        var authenticationSeen = NewSignal();
        var authenticationResponse = new TaskCompletionSource<HttpResponseMessage>(
            TaskCreationOptions.RunContinuationsAsynchronously);
        var store = new MemoryCredentialStore(null);
        var handler = new DelegateHandler(async (request, cancellationToken) =>
        {
            if (request.RequestUri!.AbsolutePath == "/.well-known/rivune")
            {
                return JsonResponse(HttpStatusCode.OK, DiscoveryBody);
            }
        
            authenticationSeen.TrySetResult();
            return await authenticationResponse.Task.WaitAsync(cancellationToken);
        });
        using var client = CreateClient(handler, store);

        Task<TokenPair> authentication = deviceExchange
            ? client.ExchangeDeviceAuthorizationAsync("device-code", TestContext.Current.CancellationToken)
            : client.LoginAsync(
                "admin",
                "password",
                new LoginDevice { Name = "Windows", Platform = "windows" },
                TestContext.Current.CancellationToken);
        await authenticationSeen.Task.WaitAsync(TestContext.Current.CancellationToken);

        await client.LogoutAsync(TestContext.Current.CancellationToken);
        authenticationResponse.SetResult(JsonResponse(HttpStatusCode.OK, TokenBody));

        await Assert.ThrowsAsync<NotAuthenticatedException>(() => authentication);
        Assert.Null(store.Credentials);
        Assert.Equal(0, store.SaveCount);
    }

    [Fact]
    public async Task LogoutWaitsForRestoreThenClearsItsResult()
    {
        var store = new BlockingLoadCredentialStore(Stored());
        var handler = new DelegateHandler((request, _) => Task.FromResult(
            request.RequestUri!.AbsolutePath == "/.well-known/rivune"
                ? JsonResponse(HttpStatusCode.OK, DiscoveryBody)
                : new HttpResponseMessage(HttpStatusCode.NoContent)));
        using var client = CreateClient(handler, store);

        var restore = client.RestoreSessionAsync(TestContext.Current.CancellationToken);
        await store.LoadStarted.Task.WaitAsync(TestContext.Current.CancellationToken);
        var logout = client.LogoutAsync(TestContext.Current.CancellationToken);

        Assert.False(store.ClearStarted.Task.IsCompleted);
        store.AllowLoad.TrySetResult();

        Assert.True(await restore);
        await logout;
        Assert.Null(store.Credentials);
        Assert.True(store.ClearStarted.Task.IsCompletedSuccessfully);
    }

    [Fact]
    public async Task LogoutCancelsAndRetiresDelayedRefresh()
    {
        var refreshSeen = NewSignal();
        var refreshCancelled = NewSignal();
        var allowRefreshResponse = NewSignal();
        var store = new MemoryCredentialStore(Stored());
        var handler = new DelegateHandler(async (request, cancellationToken) =>
        {
            switch (request.RequestUri!.AbsolutePath)
            {
                case "/.well-known/rivune":
                    return JsonResponse(HttpStatusCode.OK, DiscoveryBody);
                case "/api/v1/auth/refresh":
                    refreshSeen.TrySetResult();
                    try
                    {
                        await Task.Delay(Timeout.InfiniteTimeSpan, cancellationToken);
                    }
                    catch (OperationCanceledException)
                    {
                        refreshCancelled.TrySetResult();
                    }
                    await allowRefreshResponse.Task.WaitAsync(TestContext.Current.CancellationToken);
                    return JsonResponse(HttpStatusCode.OK, TokenBody);
                case "/api/v1/auth/logout":
                    return new HttpResponseMessage(HttpStatusCode.NoContent);
                default:
                    throw new InvalidOperationException("Unexpected request path.");
            }
        });
        using var client = CreateClient(handler, store);

        var refresh = client.RefreshSessionAsync(TestContext.Current.CancellationToken);
        await refreshSeen.Task.WaitAsync(TestContext.Current.CancellationToken);

        await client.LogoutAsync(TestContext.Current.CancellationToken);
        await refreshCancelled.Task.WaitAsync(TestContext.Current.CancellationToken);
        allowRefreshResponse.TrySetResult();

        await Assert.ThrowsAnyAsync<OperationCanceledException>(() => refresh);
        Assert.Null(store.Credentials);
        Assert.Equal(0, store.SaveCount);
    }

    [Fact]
    public async Task TransientRefreshFailurePreservesCredentials()
    {
        var store = new MemoryCredentialStore(Stored());
        var handler = new DelegateHandler((request, _) => Task.FromResult(
            request.RequestUri!.AbsolutePath == "/.well-known/rivune"
                ? JsonResponse(HttpStatusCode.OK, DiscoveryBody)
                : JsonResponse(
                    HttpStatusCode.ServiceUnavailable,
                    """{"error":{"code":"unavailable","message":"Unavailable"}}""")));
        using var client = CreateClient(handler, store);

        var exception = await Assert.ThrowsAsync<RivuneServerException>(() =>
            client.RefreshSessionAsync(TestContext.Current.CancellationToken));

        Assert.Equal((int)HttpStatusCode.ServiceUnavailable, exception.StatusCode);
        Assert.Equal("old-access", store.Credentials?.Credentials.AccessToken);
        using var relaunched = CreateClient(handler, store);
        Assert.True(await relaunched.RestoreSessionAsync(TestContext.Current.CancellationToken));
    }

    [Fact]
    public async Task InvalidRefreshTokenClearsCredentials()
    {
        var store = new MemoryCredentialStore(Stored());
        var handler = new DelegateHandler((request, _) => Task.FromResult(
            request.RequestUri!.AbsolutePath == "/.well-known/rivune"
                ? JsonResponse(HttpStatusCode.OK, DiscoveryBody)
                : JsonResponse(
                    HttpStatusCode.Unauthorized,
                    """{"error":{"code":"invalid_refresh_token","message":"The refresh token is invalid or expired"}}""")));
        using var client = CreateClient(handler, store);

        var exception = await Assert.ThrowsAsync<RivuneServerException>(() =>
            client.RefreshSessionAsync(TestContext.Current.CancellationToken));

        Assert.Equal("invalid_refresh_token", exception.Code);
        Assert.Null(store.Credentials);
        using var relaunched = CreateClient(handler, store);
        Assert.False(await relaunched.RestoreSessionAsync(TestContext.Current.CancellationToken));
    }

    [Fact]
    public async Task InvalidRefreshTokenSurfacesCredentialDeletionFailure()
    {
        var store = new FailingClearCredentialStore(Stored());
        var handler = new DelegateHandler((request, _) => Task.FromResult(
            request.RequestUri!.AbsolutePath == "/.well-known/rivune"
                ? JsonResponse(HttpStatusCode.OK, DiscoveryBody)
                : JsonResponse(
                    HttpStatusCode.Unauthorized,
                    """{"error":{"code":"invalid_refresh_token","message":"Expired"}}""")));
        using var client = CreateClient(handler, store);

        var exception = await Assert.ThrowsAsync<CredentialStoreException>(() =>
            client.RefreshSessionAsync(TestContext.Current.CancellationToken));

        Assert.Equal("Local credential deletion failed.", exception.Message);
        Assert.Equal("old-access", store.Credentials?.Credentials.AccessToken);
    }

    [Fact]
    public async Task LoginRejectsNullRequiredTokenFieldsBeforePersistence()
    {
        const string invalidToken = """
            {"tokenType":"Bearer","accessToken":null,"accessTokenExpiresAt":"2026-08-05T12:15:00Z","refreshToken":"refresh","refreshTokenExpiresAt":"2026-09-05T12:00:00Z","sessionId":"22222222-2222-4222-8222-222222222222","deviceId":"33333333-3333-4333-8333-333333333333","authorizationScope":"global_admin","category":null}
            """;
        var store = new MemoryCredentialStore(null);
        var handler = new DelegateHandler((request, _) => Task.FromResult(
            request.RequestUri!.AbsolutePath == "/.well-known/rivune"
                ? JsonResponse(HttpStatusCode.OK, DiscoveryBody)
                : JsonResponse(HttpStatusCode.OK, invalidToken)));
        using var client = CreateClient(handler, store);

        await Assert.ThrowsAsync<InvalidResponseException>(() => client.LoginAsync(
            "admin",
            "password",
            new LoginDevice { Name = "Windows", Platform = "windows" },
            TestContext.Current.CancellationToken));

        Assert.Null(store.Credentials);
        Assert.Equal(0, store.SaveCount);
    }

    [Fact]
    public async Task RemoteLogoutFailureCannotVetoLocalClearOrTriggerRefresh()
    {
        var refreshRequests = 0;
        var logoutObservedClearedStore = false;
        var store = new MemoryCredentialStore(Stored());
        var handler = new DelegateHandler((request, _) =>
        {
            if (request.RequestUri!.AbsolutePath == "/.well-known/rivune")
            {
                return Task.FromResult(JsonResponse(HttpStatusCode.OK, DiscoveryBody));
            }
            if (request.RequestUri.AbsolutePath == "/api/v1/auth/refresh")
            {
                Interlocked.Increment(ref refreshRequests);
            }
            else
            {
                logoutObservedClearedStore = store.Credentials is null;
            }
            return Task.FromResult(JsonResponse(
                HttpStatusCode.Unauthorized,
                """{"error":{"code":"unauthorized","message":"Unauthorized"}}"""));
        });
        using var client = CreateClient(handler, store);

        await Assert.ThrowsAsync<RivuneServerException>(() =>
            client.LogoutAsync(TestContext.Current.CancellationToken));

        Assert.Null(store.Credentials);
        Assert.Equal(0, refreshRequests);
        Assert.True(logoutObservedClearedStore);
        await Assert.ThrowsAsync<NotAuthenticatedException>(() =>
            client.GetCurrentAccountAsync(TestContext.Current.CancellationToken));
    }

    [Fact]
    public async Task LocalLogoutFailureRemainsDistinctAndTakesPriorityAfterRemoteAttempt()
    {
        var remoteAttempted = NewSignal();
        var store = new FailingClearCredentialStore(Stored());
        var handler = new DelegateHandler((request, _) =>
        {
            if (request.RequestUri!.AbsolutePath == "/.well-known/rivune")
            {
                return Task.FromResult(JsonResponse(HttpStatusCode.OK, DiscoveryBody));
            }
        
            remoteAttempted.TrySetResult();
            return Task.FromResult(JsonResponse(
                HttpStatusCode.ServiceUnavailable,
                """{"error":{"code":"unavailable","message":"Unavailable"}}"""));
        });
        using var client = CreateClient(handler, store);

        var exception = await Assert.ThrowsAsync<CredentialStoreException>(() =>
            client.LogoutAsync(TestContext.Current.CancellationToken));

        Assert.Equal("Local credential deletion failed.", exception.Message);
        Assert.True(remoteAttempted.Task.IsCompletedSuccessfully);
        await Assert.ThrowsAsync<NotAuthenticatedException>(() =>
            client.GetCurrentAccountAsync(TestContext.Current.CancellationToken));
    }

    [Fact]
    public async Task DeclaredOversizedResponseIsRejectedBeforeReadingContent()
    {
        var stream = new CountingStream(MaximumResponseBodyBytes + 1L);
        var content = new StreamContent(stream);
        content.Headers.ContentLength = MaximumResponseBodyBytes + 1L;
        var handler = new DelegateHandler((_, _) => Task.FromResult(
            new HttpResponseMessage(HttpStatusCode.Unauthorized) { Content = content }));
        using var client = CreateClient(handler, new MemoryCredentialStore(null));

        var exception = await Assert.ThrowsAsync<ResponseTooLargeException>(() =>
            client.DiscoverAsync(TestContext.Current.CancellationToken));

        Assert.Equal(MaximumResponseBodyBytes, exception.MaximumBytes);
        Assert.Equal(0, stream.ReadCount);
    }

    [Fact]
    public async Task ChunkedResponseIsCountedThroughLimitPlusOneBeforeStatusParsing()
    {
        var stream = new CountingStream(MaximumResponseBodyBytes + 1L);
        var content = new StreamContent(stream);
        Assert.Null(content.Headers.ContentLength);
        var handler = new DelegateHandler((_, _) => Task.FromResult(
            new HttpResponseMessage(HttpStatusCode.Unauthorized) { Content = content }));
        using var client = CreateClient(handler, new MemoryCredentialStore(null));

        var exception = await Assert.ThrowsAsync<ResponseTooLargeException>(() =>
            client.DiscoverAsync(TestContext.Current.CancellationToken));

        Assert.Equal(MaximumResponseBodyBytes, exception.MaximumBytes);
        Assert.Equal(MaximumResponseBodyBytes + 1L, stream.BytesRead);
    }

    [Fact]
    public async Task ResponseAtExactLimitIsAccepted()
    {
        var json = Encoding.UTF8.GetBytes(DiscoveryBody);
        var body = new byte[MaximumResponseBodyBytes];
        json.CopyTo(body, 0);
        body.AsSpan(json.Length).Fill((byte)' ');
        var handler = new DelegateHandler((_, _) => Task.FromResult(
            new HttpResponseMessage(HttpStatusCode.OK) { Content = new ByteArrayContent(body) }));
        using var client = CreateClient(handler, new MemoryCredentialStore(null));

        var discovery = await client.DiscoverAsync(TestContext.Current.CancellationToken);

        Assert.Equal("Rivune", discovery.Name);
        Assert.Equal(true, discovery.SetupCompleted);
        Assert.Equal(false, discovery.DemoAvailable);
    }

    private static RivuneApiClient CreateClient(HttpMessageHandler handler, ICredentialStore store) =>
        new("https://rivune.test", handler, store);

    private static TaskCompletionSource NewSignal() =>
        new(TaskCreationOptions.RunContinuationsAsynchronously);

    private static HttpResponseMessage JsonResponse(HttpStatusCode statusCode, string body) => new(statusCode)
    {
        Content = new StringContent(body, Encoding.UTF8, "application/json"),
    };

    private static StoredCredentials Stored() => new()
    {
        Issuer = "https://rivune.test/",
        Credentials = new TokenPair
        {
            TokenType = "Bearer",
            AccessToken = "old-access",
            AccessTokenExpiresAt = "2026-08-05T12:15:00Z",
            RefreshToken = "old-refresh",
            RefreshTokenExpiresAt = "2026-09-05T12:00:00Z",
            SessionId = Guid.Parse("22222222-2222-4222-8222-222222222222"),
            DeviceId = Guid.Parse("33333333-3333-4333-8333-333333333333"),
            AuthorizationScope = AuthorizationScope.GlobalAdministrator,
            Category = null,
        },
    };

    private sealed class DelegateHandler(
        Func<HttpRequestMessage, CancellationToken, Task<HttpResponseMessage>> send) : HttpMessageHandler
    {
        protected override Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken) => send(request, cancellationToken);
    }

    private class MemoryCredentialStore(StoredCredentials? credentials) : ICredentialStore
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

        public int SaveCount { get; private set; }

        public virtual ValueTask<StoredCredentials?> LoadAsync(CancellationToken cancellationToken = default)
        {
            lock (_sync)
            {
                return ValueTask.FromResult(_credentials);
            }
        }

        public virtual ValueTask SaveAsync(
            StoredCredentials credentials,
            CancellationToken cancellationToken = default)
        {
            lock (_sync)
            {
                _credentials = credentials;
                SaveCount++;
            }
            return ValueTask.CompletedTask;
        }

        public virtual ValueTask ClearAsync(CancellationToken cancellationToken = default)
        {
            lock (_sync)
            {
                _credentials = null;
            }
            return ValueTask.CompletedTask;
        }
    }

    private sealed class BlockingLoadCredentialStore(StoredCredentials credentials)
        : MemoryCredentialStore(credentials)
    {
        public TaskCompletionSource LoadStarted { get; } = NewSignal();
        public TaskCompletionSource AllowLoad { get; } = NewSignal();
        public TaskCompletionSource ClearStarted { get; } = NewSignal();

        public override async ValueTask<StoredCredentials?> LoadAsync(
            CancellationToken cancellationToken = default)
        {
            LoadStarted.TrySetResult();
            await AllowLoad.Task.WaitAsync(cancellationToken);
            return await base.LoadAsync(cancellationToken);
        }

        public override ValueTask ClearAsync(CancellationToken cancellationToken = default)
        {
            ClearStarted.TrySetResult();
            return base.ClearAsync(cancellationToken);
        }
    }

    private sealed class FailingClearCredentialStore(StoredCredentials credentials)
        : MemoryCredentialStore(credentials)
    {
        public override ValueTask ClearAsync(CancellationToken cancellationToken = default) =>
            ValueTask.FromException(new CredentialStoreException("Local credential deletion failed."));
    }

    private sealed class CountingStream(long length) : Stream
    {
        private long _position;

        public int ReadCount { get; private set; }
        public long BytesRead => _position;
        public override bool CanRead => true;
        public override bool CanSeek => false;
        public override bool CanWrite => false;
        public override long Length => length;
        public override long Position
        {
            get => _position;
            set => throw new NotSupportedException();
        }

        public override int Read(byte[] buffer, int offset, int count)
        {
            var bytesRead = (int)Math.Min(count, length - _position);
            if (bytesRead <= 0)
            {
                return 0;
            }
            Array.Clear(buffer, offset, bytesRead);
            _position += bytesRead;
            ReadCount++;
            return bytesRead;
        }

        public override ValueTask<int> ReadAsync(
            Memory<byte> buffer,
            CancellationToken cancellationToken = default)
        {
            cancellationToken.ThrowIfCancellationRequested();
            var bytesRead = (int)Math.Min(buffer.Length, length - _position);
            if (bytesRead <= 0)
            {
                return ValueTask.FromResult(0);
            }
            buffer.Span[..bytesRead].Clear();
            _position += bytesRead;
            ReadCount++;
            return ValueTask.FromResult(bytesRead);
        }

        public override void Flush() { }
        public override long Seek(long offset, SeekOrigin origin) => throw new NotSupportedException();
        public override void SetLength(long value) => throw new NotSupportedException();
        public override void Write(byte[] buffer, int offset, int count) => throw new NotSupportedException();
    }
}
