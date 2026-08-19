using System.Net;
using System.Net.Http.Headers;
using Xunit;
using Rivune.App;
using Rivune.App.ViewModels;
using Rivune.Windows;

namespace Rivune.Windows.Tests;

public sealed class WindowsApplicationTests
{
    [Fact]
    public void TransitionCancelsPreviousGenerationAndAdvancesPhase()
    {
        using var state = new MainPageViewModel();
        var previousToken = state.Token;

        var generation = state.Transition(AppPhase.Pairing);

        Assert.True(previousToken.IsCancellationRequested);
        Assert.Equal(AppPhase.Pairing, state.Phase);
        Assert.Equal(generation, state.GenerationId);
        Assert.True(state.IsCurrent(generation));
        Assert.False(state.IsCurrent(generation - 1));
    }

    [Fact]
    public void ResetServerDisposesClientAndClearsPlaybackSelection()
    {
        using var state = new MainPageViewModel
        {
            Client = new RivuneApiClient(new Uri("https://rivune.example")),
            SelectedItem = new CollectionItem
            {
                Id = "movie:1",
                MediaType = "movie",
                Title = "Example",
                ExternalIds = new Dictionary<string, string>(),
                Sources = [],
            },
        };

        state.ResetServer();

        Assert.Equal(AppPhase.Server, state.Phase);
        Assert.Null(state.Client);
        Assert.Null(state.SelectedItem);
    }

    [Fact]
    public void RelativeTimelineMapsPlayerTimeToAbsoluteProgress()
    {
        var timeline = new PlaybackTimeline();
        timeline.Reset(PlaybackMediaTimeline.Relative, requestedStartSeconds: 120, reportedDurationSeconds: 3_480);

        Assert.Equal(120, timeline.OffsetSeconds);
        Assert.Equal(130, timeline.ToAbsolutePosition(TimeSpan.FromSeconds(10)));
        Assert.Equal(TimeSpan.FromSeconds(15), timeline.ToMediaPosition(135));
        Assert.Equal(TimeSpan.Zero, timeline.ToMediaPosition(100));
        Assert.Equal(3_600, timeline.UpdateDuration(TimeSpan.FromSeconds(3_480)));
    }

    [Fact]
    public void AbsoluteTimelinePreservesPositionsAndNeverShrinksDuration()
    {
        var timeline = new PlaybackTimeline();
        timeline.Reset(PlaybackMediaTimeline.Absolute, requestedStartSeconds: 120, reportedDurationSeconds: 3_600);

        Assert.Equal(0, timeline.OffsetSeconds);
        Assert.Equal(135, timeline.ToAbsolutePosition(TimeSpan.FromSeconds(135)));
        Assert.Equal(TimeSpan.FromSeconds(135), timeline.ToMediaPosition(135));
        Assert.Equal(3_600, timeline.UpdateDuration(TimeSpan.FromSeconds(3_000)));
    }

    [Theory]
    [InlineData("imdb:tt123", false, "imdb")]
    [InlineData("tt123", false, "imdb")]
    [InlineData("opaque-resource", true, "addon")]
    [InlineData("opaque-resource", false, "tmdb")]
    public void ProviderInferenceMatchesTitleResolutionRules(string resourceId, bool hasAddonSource, string expected)
    {
        Assert.Equal(expected, MediaIdentity.InferProvider(resourceId, hasAddonSource));
    }

    [Fact]
    public void AddonIdentityUsesServerScopedHashContract()
    {
        var externalId = MediaIdentity.AddonExternalId(
            Guid.Parse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"),
            "movie",
            "addon:movie:42");

        Assert.Equal("sha256:79cf0b22062398531fbf409e3fee587595e4733377bdb074443067138e013433", externalId);
    }

    [Theory]
    [InlineData("2026-08-12", "2026-08-12")]
    [InlineData("2026-08-12T15:30:45Z", "2026-08-12")]
    [InlineData("2026-08-12T23:30:00-05:00", "2026-08-12")]
    [InlineData("not-a-date", null)]
    [InlineData(null, null)]
    public void ReleaseDatesAreNormalizedForTitleResolution(string? value, string? expected)
    {
        Assert.Equal(expected, MediaIdentity.NormalizeReleaseDate(value));
    }

    [Fact]
    public async Task DirectMediaProxyForwardsRangeOnFixedSameOriginTarget()
    {
        RangeHeaderValue? observedRange = null;
        var handler = new DelegateHandler((request, _) =>
        {
            observedRange = request.Headers.Range;
            var response = new HttpResponseMessage(HttpStatusCode.PartialContent)
            {
                Content = new ByteArrayContent("cde"u8.ToArray()),
                RequestMessage = request,
            };
            response.Content.Headers.ContentRange = new ContentRangeHeaderValue(2, 4, 8);
            response.Headers.AcceptRanges.Add("bytes");
            return Task.FromResult(response);
        });
        using var proxy = new LoopbackMediaProxy(
            new Uri("https://rivune.example/api/v1/playback/asset?token=opaque"),
            uri => uri.Scheme == "https" && uri.Host == "rivune.example",
            handler);
        using var client = new HttpClient(new SocketsHttpHandler { UseProxy = false });
        using var request = new HttpRequestMessage(HttpMethod.Get, proxy.PlaybackUri);
        request.Headers.Range = new RangeHeaderValue(2, 4);

        using var response = await client.SendAsync(request, TestContext.Current.CancellationToken);

        Assert.Equal(HttpStatusCode.PartialContent, response.StatusCode);
        Assert.Equal("bytes=2-4", observedRange?.ToString());
        Assert.Equal("cde", await response.Content.ReadAsStringAsync(TestContext.Current.CancellationToken));
        Assert.Equal("bytes 2-4/8", response.Content.Headers.ContentRange?.ToString());
    }

    [Fact]
    public async Task DirectMediaProxyConvertsUpstreamRedirectToBadGateway()
    {
        var handler = new DelegateHandler((request, _) =>
        {
            var response = new HttpResponseMessage(HttpStatusCode.TemporaryRedirect) { RequestMessage = request };
            response.Headers.Location = new Uri("http://127.0.0.1/private");
            return Task.FromResult(response);
        });
        using var proxy = new LoopbackMediaProxy(
            new Uri("https://rivune.example/api/v1/playback/asset?token=opaque"),
            uri => uri.Scheme == "https" && uri.Host == "rivune.example",
            handler);
        using var client = new HttpClient(new SocketsHttpHandler { UseProxy = false });

        using var response = await client.GetAsync(proxy.PlaybackUri, TestContext.Current.CancellationToken);

        Assert.Equal(HttpStatusCode.BadGateway, response.StatusCode);
    }

    private sealed class DelegateHandler(
        Func<HttpRequestMessage, CancellationToken, Task<HttpResponseMessage>> send) : HttpMessageHandler
    {
        protected override Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken) => send(request, cancellationToken);
    }
}
