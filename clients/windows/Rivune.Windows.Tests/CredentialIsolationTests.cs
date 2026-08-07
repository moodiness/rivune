using System.Net;
using System.Net.Http.Headers;
using System.Text;
using Rivune.Windows;
using Xunit;

namespace Rivune.Windows.Tests;

public sealed class CredentialIsolationTests
{
    [Fact]
    public async Task AccessTokenFromAnotherServerIsNotAttached()
    {
        var store = new MemoryCredentialStore(CredentialsFor("https://issuer.test/"));
        var handler = new RecordingHandler();
        using var client = new RivuneApiClient(new Uri("https://destination.test"), handler, store);

        await Assert.ThrowsAsync<NotAuthenticatedException>(() =>
            client.GetCurrentAccountAsync(TestContext.Current.CancellationToken));

        var request = Assert.Single(handler.Requests);
        Assert.Equal("/.well-known/rivune", request.Uri.AbsolutePath);
        Assert.Null(request.Authorization);
        Assert.DoesNotContain("access-from-issuer", request.Body ?? string.Empty);
        Assert.DoesNotContain("refresh-from-issuer", request.Body ?? string.Empty);
    }

    [Fact]
    public async Task RefreshTokenFromAnotherServerIsNotPosted()
    {
        var store = new MemoryCredentialStore(CredentialsFor("https://issuer.test/"));
        var handler = new RecordingHandler();
        using var client = new RivuneApiClient(new Uri("https://destination.test"), handler, store);

        await Assert.ThrowsAsync<NotAuthenticatedException>(() =>
            client.RefreshSessionAsync(TestContext.Current.CancellationToken));

        Assert.Empty(handler.Requests);
    }

    [Fact]
    public async Task CanonicalServerOriginRestoresItsCredentials()
    {
        var store = new MemoryCredentialStore(CredentialsFor("https://rivune.test/"));
        using var client = new RivuneApiClient(
            new Uri("HTTPS://RIVUNE.TEST:443/ignored/path?ignored=true"),
            new RecordingHandler(),
            store);

        Assert.True(await client.RestoreSessionAsync(TestContext.Current.CancellationToken));
    }

    [Theory]
    [InlineData("http://rivune.test")]
    [InlineData("http://192.0.2.10:8080")]
    public void RemoteHttpServerIsRejected(string serverUrl)
    {
        Assert.Throws<InvalidServerUrlException>(() =>
        {
            using var client = new RivuneApiClient(
                serverUrl,
                new RecordingHandler(),
                new MemoryCredentialStore(null));
        });
    }

    [Theory]
    [InlineData("http://localhost:8080")]
    [InlineData("http://127.0.0.1:8080")]
    [InlineData("http://127.42.7.9:8080")]
    [InlineData("http://[::1]:8080")]
    public async Task LoopbackHttpServerIsAccepted(string serverUrl)
    {
        var handler = new RecordingHandler();
        using var client = new RivuneApiClient(serverUrl, handler, new MemoryCredentialStore(null));

        var discovery = await client.DiscoverAsync(TestContext.Current.CancellationToken);

        Assert.Equal("Rivune", discovery.Name);
        Assert.Single(handler.Requests);
    }

    [Theory]
    [InlineData("http://rivune.test/api/v1")]
    [InlineData("https://other.test/api/v1")]
    public async Task DiscoveryCannotRedirectCredentialsToAnotherOrigin(string apiBaseUrl)
    {
        var handler = new RecordingHandler(apiBaseUrl);
        using var client = new RivuneApiClient(
            "https://rivune.test",
            handler,
            new MemoryCredentialStore(CredentialsFor("https://rivune.test/")));

        await Assert.ThrowsAsync<InvalidServerUrlException>(() =>
            client.DiscoverAsync(TestContext.Current.CancellationToken));

        var request = Assert.Single(handler.Requests);
        Assert.Null(request.Authorization);
    }

    private static StoredCredentials CredentialsFor(string issuer) => new()
    {
        Issuer = issuer,
        Credentials = new TokenPair
        {
            TokenType = "Bearer",
            AccessToken = "access-from-issuer",
            AccessTokenExpiresAt = "2026-08-04T12:15:00Z",
            RefreshToken = "refresh-from-issuer",
            RefreshTokenExpiresAt = "2026-09-04T12:00:00Z",
            SessionId = Guid.Parse("22222222-2222-4222-8222-222222222222"),
            DeviceId = Guid.Parse("33333333-3333-4333-8333-333333333333"),
            AuthorizationScope = AuthorizationScope.GlobalAdministrator,
            Category = null,
        },
    };

    private sealed class MemoryCredentialStore(StoredCredentials? credentials) : ICredentialStore
    {
        private StoredCredentials? _credentials = credentials;

        public ValueTask<StoredCredentials?> LoadAsync(CancellationToken cancellationToken = default) =>
            ValueTask.FromResult(_credentials);

        public ValueTask SaveAsync(StoredCredentials credentials, CancellationToken cancellationToken = default)
        {
            _credentials = credentials;
            return ValueTask.CompletedTask;
        }

        public ValueTask ClearAsync(CancellationToken cancellationToken = default)
        {
            _credentials = null;
            return ValueTask.CompletedTask;
        }
    }

    private sealed class RecordingHandler(string apiBaseUrl = "/api/v1") : HttpMessageHandler
    {
        public List<CapturedRequest> Requests { get; } = [];

        protected override async Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken)
        {
            Requests.Add(new CapturedRequest(
                request.RequestUri!,
                request.Headers.Authorization,
                request.Content is null
                    ? null
                    : await request.Content.ReadAsStringAsync(cancellationToken)));

            if (request.RequestUri!.AbsolutePath == "/.well-known/rivune")
            {
                var discovery = $$"""
                    {"name":"Rivune","serverVersion":"test","protocolVersion":20,"apiBaseUrl":"{{apiBaseUrl}}","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en"}
                    """;
                return JsonResponse(HttpStatusCode.OK, discovery);
            }

            return JsonResponse(HttpStatusCode.Unauthorized, """{"error":{"code":"unauthorized","message":"Unauthorized"}}""");
        }

        private static HttpResponseMessage JsonResponse(HttpStatusCode statusCode, string body) => new(statusCode)
        {
            Content = new StringContent(body, Encoding.UTF8, "application/json"),
        };
    }

    private sealed record CapturedRequest(Uri Uri, AuthenticationHeaderValue? Authorization, string? Body);
}
