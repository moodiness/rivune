using System.Net;
using System.Text;
using Rivune.Windows;
using Xunit;

namespace Rivune.Windows.Tests;

public sealed class RedirectSecurityTests
{
    private const string DiscoveryBody = """
        {"name":"Rivune","serverVersion":"test","protocolVersion":22,"apiBaseUrl":"/api/v1/","setupRequired":false,"setupCompleted":true,"demoAvailable":false,"timezone":"UTC","interfaceLanguage":"en"}
        """;

    [Theory]
    [InlineData(302, false)]
    [InlineData(302, true)]
    [InlineData(307, false)]
    [InlineData(307, true)]
    [InlineData(308, false)]
    [InlineData(308, true)]
    public async Task DiscoveryRedirectIsRejectedWithoutSecondHop(int statusCode, bool crossOrigin)
    {
        var redirectTarget = new Uri(crossOrigin
            ? "https://redirect.test/api/v1/redirected"
            : "https://rivune.test/api/v1/redirected");
        var handler = new RedirectScenarioHandler(
            (HttpStatusCode)statusCode,
            redirectTarget,
            redirectDiscovery: true);
        using var client = new RivuneApiClient(
            "https://rivune.test",
            handler,
            new NullCredentialStore());

        var exception = await Assert.ThrowsAsync<RivuneServerException>(() =>
            client.DiscoverAsync(TestContext.Current.CancellationToken));

        Assert.Equal(statusCode, exception.StatusCode);
        Assert.Equal("redirect_not_allowed", exception.Code);
        Assert.Equal("Rivune server redirects are not allowed.", exception.Message);
        var request = Assert.Single(handler.PrimaryRequests);
        Assert.Equal(HttpMethod.Get, request.Method);
        Assert.Equal("/.well-known/rivune", request.Uri.AbsolutePath);
        Assert.Empty(handler.SecondHopRequests);
    }

    [Theory]
    [InlineData(302, false)]
    [InlineData(302, true)]
    [InlineData(307, false)]
    [InlineData(307, true)]
    [InlineData(308, false)]
    [InlineData(308, true)]
    public async Task LoginPostRedirectIsNotReplayed(int statusCode, bool crossOrigin)
    {
        var redirectTarget = new Uri(crossOrigin
            ? "https://redirect.test/api/v1/redirected"
            : "https://rivune.test/api/v1/redirected");
        var handler = new RedirectScenarioHandler(
            (HttpStatusCode)statusCode,
            redirectTarget,
            redirectDiscovery: false);
        using var client = new RivuneApiClient(
            "https://rivune.test",
            handler,
            new NullCredentialStore());

        var exception = await Assert.ThrowsAsync<RivuneServerException>(() =>
            client.LoginAsync(
                "alice",
                "password",
                new LoginDevice { Name = "Windows", Platform = "windows" },
                TestContext.Current.CancellationToken));

        Assert.Equal(statusCode, exception.StatusCode);
        Assert.Equal("redirect_not_allowed", exception.Code);
        Assert.Equal("Rivune server redirects are not allowed.", exception.Message);
        Assert.Equal(2, handler.PrimaryRequests.Count);
        var login = Assert.Single(
            handler.PrimaryRequests,
            request => request.Uri.AbsolutePath == "/api/v1/auth/login");
        Assert.Equal(HttpMethod.Post, login.Method);
        Assert.Contains("\"username\":\"alice\"", login.Body, StringComparison.Ordinal);
        Assert.Empty(handler.SecondHopRequests);
    }

    [Fact]
    public async Task FinalRequestUriOutsideIssuerIsRejected()
    {
        var finalUri = new Uri("https://redirect.test/api/v1/redirected");
        var handler = new FinalRequestUriHandler(finalUri);
        using var client = new RivuneApiClient(
            "https://rivune.test",
            handler,
            new NullCredentialStore());

        var exception = await Assert.ThrowsAsync<InvalidServerUrlException>(() =>
            client.DiscoverAsync(TestContext.Current.CancellationToken));

        Assert.Equal(finalUri.ToString(), exception.Value);
        Assert.Equal(1, handler.RequestCount);
    }

    [Fact]
    public void InjectedHttpClientHandlerHasAutomaticRedirectsDisabled()
    {
        var handler = new HttpClientHandler { AllowAutoRedirect = true };
        using var client = new RivuneApiClient(
            "https://rivune.test",
            handler,
            new NullCredentialStore());

        Assert.False(handler.AllowAutoRedirect);
    }

    [Fact]
    public void InjectedSocketsHttpHandlerHasAutomaticRedirectsDisabledThroughDelegatingHandler()
    {
        var transport = new SocketsHttpHandler { AllowAutoRedirect = true };
        var handler = new PassthroughHandler { InnerHandler = transport };
        using var client = new RivuneApiClient(
            "https://rivune.test",
            handler,
            new NullCredentialStore());

        Assert.False(transport.AllowAutoRedirect);
    }

    [Fact]
    public async Task ArtworkRedirectIsRejectedWithoutSecondHop()
    {
        var target = new Uri("https://provider.test/poster.jpg");
        var handler = new ResourceRedirectHandler(target);
        using var client = new RivuneApiClient(
            "https://rivune.test",
            handler,
            new NullCredentialStore());

        var exception = await Assert.ThrowsAsync<RivuneServerException>(() =>
            client.DownloadSameOriginResourceAsync(
                "/api/v1/artwork/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
                TestContext.Current.CancellationToken));

        Assert.Equal("redirect_not_allowed", exception.Code);
        Assert.Equal(1, handler.PrimaryRequests);
        Assert.Equal(0, handler.SecondHopRequests);
    }

    private sealed class RedirectScenarioHandler(
        HttpStatusCode redirectStatus,
        Uri redirectTarget,
        bool redirectDiscovery) : HttpMessageHandler
    {
        public List<CapturedRequest> PrimaryRequests { get; } = [];
        public List<CapturedRequest> SecondHopRequests { get; } = [];

        protected override async Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken)
        {
            var captured = new CapturedRequest(
                request.Method,
                request.RequestUri!,
                request.Content is null
                    ? null
                    : await request.Content.ReadAsStringAsync(cancellationToken));
            if (request.RequestUri == redirectTarget)
            {
                SecondHopRequests.Add(captured);
                return JsonResponse(HttpStatusCode.OK, DiscoveryBody);
            }

            PrimaryRequests.Add(captured);
            var isDiscovery = request.RequestUri!.AbsolutePath == "/.well-known/rivune";
            if (isDiscovery != redirectDiscovery)
            {
                return JsonResponse(HttpStatusCode.OK, DiscoveryBody);
            }

            var response = JsonResponse(
                redirectStatus,
                """{"error":{"code":"untrusted","message":"Untrusted redirect body"}}""");
            response.Headers.Location = redirectTarget;
            return response;
        }
    }

    private sealed class ResourceRedirectHandler(Uri redirectTarget) : HttpMessageHandler
    {
        public int PrimaryRequests { get; private set; }
        public int SecondHopRequests { get; private set; }

        protected override Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken)
        {
            if (request.RequestUri == redirectTarget)
            {
                SecondHopRequests++;
                return Task.FromResult(new HttpResponseMessage(HttpStatusCode.OK));
            }

            PrimaryRequests++;
            if (request.RequestUri!.AbsolutePath == "/.well-known/rivune")
                return Task.FromResult(JsonResponse(HttpStatusCode.OK, DiscoveryBody));

            var response = new HttpResponseMessage(HttpStatusCode.TemporaryRedirect);
            response.Headers.Location = redirectTarget;
            return Task.FromResult(response);
        }
    }

    private sealed class FinalRequestUriHandler(Uri finalUri) : HttpMessageHandler
    {
        public int RequestCount { get; private set; }

        protected override Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken)
        {
            RequestCount++;
            return Task.FromResult(new HttpResponseMessage(HttpStatusCode.OK)
            {
                Content = new StringContent(DiscoveryBody, Encoding.UTF8, "application/json"),
                RequestMessage = new HttpRequestMessage(HttpMethod.Get, finalUri),
            });
        }
    }

    private sealed class PassthroughHandler : DelegatingHandler
    {
    }

    private sealed class NullCredentialStore : ICredentialStore
    {
        public ValueTask<StoredCredentials?> LoadAsync(CancellationToken cancellationToken = default) =>
            ValueTask.FromResult<StoredCredentials?>(null);

        public ValueTask SaveAsync(StoredCredentials credentials, CancellationToken cancellationToken = default) =>
            ValueTask.CompletedTask;

        public ValueTask ClearAsync(CancellationToken cancellationToken = default) =>
            ValueTask.CompletedTask;
    }

    private static HttpResponseMessage JsonResponse(HttpStatusCode statusCode, string body) => new(statusCode)
    {
        Content = new StringContent(body, Encoding.UTF8, "application/json"),
    };

    private sealed record CapturedRequest(HttpMethod Method, Uri Uri, string? Body);
}
