using System.Net;
using System.Text;
using Rivune.Windows;
using Xunit;

namespace Rivune.Windows.Tests;

public sealed class SubtitleDownloadTests
{
    [Fact]
    public async Task DownloadsBoundedSameOriginSubtitleFromPrivateHttpServer()
    {
        var handler = new SubtitleHandler();
        using var client = new RivuneApiClient(
            "http://192.168.10.20:8080",
            handler,
            new EmptyCredentialStore());

        var contents = await client.DownloadSameOriginSubtitleAsync(
            "/subtitles/episode.vtt",
            TestContext.Current.CancellationToken);

        Assert.Equal("WEBVTT\n\n00:00.000 --> 00:01.000\nHello\n", Encoding.UTF8.GetString(contents));
        Assert.Equal(new Uri("http://192.168.10.20:8080/subtitles/episode.vtt"), Assert.Single(handler.Requests));
    }

    [Theory]
    [InlineData("/subtitles/same-redirect.vtt")]
    [InlineData("/subtitles/cross-redirect.vtt")]
    public async Task RejectsSubtitleRedirects(string path)
    {
        using var client = new RivuneApiClient(
            "http://192.168.10.20:8080",
            new SubtitleHandler(),
            new EmptyCredentialStore());

        var exception = await Assert.ThrowsAsync<RivuneServerException>(() =>
            client.DownloadSameOriginSubtitleAsync(path, TestContext.Current.CancellationToken));

        Assert.Equal("redirect_not_allowed", exception.Code);
    }

    [Fact]
    public async Task RejectsOversizedSubtitleBody()
    {
        using var client = new RivuneApiClient(
            "http://192.168.10.20:8080",
            new SubtitleHandler(),
            new EmptyCredentialStore());

        await Assert.ThrowsAsync<ResponseTooLargeException>(() =>
            client.DownloadSameOriginSubtitleAsync(
                "/subtitles/oversized.vtt",
                TestContext.Current.CancellationToken));
    }

    [Fact]
    public void PrivateHttpResourceMustKeepExactOrigin()
    {
        using var client = new RivuneApiClient(
            "http://192.168.10.20:8080",
            new SubtitleHandler(),
            new EmptyCredentialStore());

        Assert.Equal(
            "http://192.168.10.20:8080/subtitles/episode.vtt",
            client.ResolveResponseResourceUrl("/subtitles/episode.vtt").AbsoluteUri);
        Assert.Throws<InvalidServerUrlException>(() =>
            client.ResolveResponseResourceUrl("http://192.168.10.20:8081/subtitles/episode.vtt"));
        Assert.Throws<InvalidServerUrlException>(() =>
            client.ResolveResponseResourceUrl("http://192.168.10.21:8080/subtitles/episode.vtt"));
    }

    private sealed class SubtitleHandler : HttpMessageHandler
    {
        public List<Uri> Requests { get; } = [];

        protected override Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken)
        {
            Requests.Add(request.RequestUri!);
            HttpResponseMessage response = request.RequestUri!.AbsolutePath switch
            {
                "/subtitles/episode.vtt" => new(HttpStatusCode.OK)
                {
                    Content = new StringContent(
                        "WEBVTT\n\n00:00.000 --> 00:01.000\nHello\n",
                        Encoding.UTF8,
                        "text/vtt"),
                },
                "/subtitles/same-redirect.vtt" => Redirect("/subtitles/episode.vtt"),
                "/subtitles/cross-redirect.vtt" => Redirect("https://evil.example/subtitles/episode.vtt"),
                "/subtitles/oversized.vtt" => Oversized(),
                _ => throw new InvalidOperationException($"Unexpected request {request.RequestUri}."),
            };
            response.RequestMessage = request;
            return Task.FromResult(response);
        }

        private static HttpResponseMessage Redirect(string location)
        {
            var response = new HttpResponseMessage(HttpStatusCode.TemporaryRedirect);
            response.Headers.Location = new Uri(location, UriKind.RelativeOrAbsolute);
            return response;
        }

        private static HttpResponseMessage Oversized()
        {
            var response = new HttpResponseMessage(HttpStatusCode.OK)
            {
                Content = new ByteArrayContent([]),
            };
            response.Content.Headers.ContentLength = 2 * 1024 * 1024 + 1;
            return response;
        }
    }

    private sealed class EmptyCredentialStore : ICredentialStore
    {
        public ValueTask<StoredCredentials?> LoadAsync(CancellationToken cancellationToken = default) =>
            ValueTask.FromResult<StoredCredentials?>(null);

        public ValueTask SaveAsync(StoredCredentials credentials, CancellationToken cancellationToken = default) =>
            ValueTask.CompletedTask;

        public ValueTask ClearAsync(CancellationToken cancellationToken = default) => ValueTask.CompletedTask;
    }
}
