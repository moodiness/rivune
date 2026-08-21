using System.Net;
using System.Text;
using System.Text.Json;
using Rivune.Windows;
using Xunit;

namespace Rivune.Windows.Tests;

public sealed class DiscoveryCapabilitiesTests
{
    private static readonly JsonSerializerOptions JsonOptions = new(JsonSerializerDefaults.Web);

    [Fact]
    public void CapabilityIdentifiersExposeStableWireValues()
    {
        Assert.Equal("bounded-aggregate-resources", DiscoveryCapabilityIdentifiers.BoundedAggregateResources);
        Assert.Equal("profile-archives-v1", DiscoveryCapabilityIdentifiers.ProfileArchivesV1);
        Assert.Equal("request-correlation", DiscoveryCapabilityIdentifiers.RequestCorrelation);
        Assert.Equal("local-recommendations", DiscoveryCapabilityIdentifiers.LocalRecommendations);
        Assert.Equal("playback-coordination", DiscoveryCapabilityIdentifiers.PlaybackCoordination);
    }

    [Fact]
    public void PublicDiscoveryDecodeNormalizesCapabilitiesAndDefaultsOmissionToEmpty()
    {
        var omitted = JsonSerializer.Deserialize<Discovery>(DiscoveryBody(null), JsonOptions)!;
        var normalized = JsonSerializer.Deserialize<Discovery>(
            DiscoveryBody("[\"profile-archives-v1\",\"future-feature\",\"profile-archives-v1\",\"UPPERCASE\",null,{}]"),
            JsonOptions)!;

        Assert.Empty(omitted.Capabilities);
        Assert.Equal(new[] { "profile-archives-v1", "future-feature" }, normalized.Capabilities);
        Assert.True(normalized.SupportsProfileArchivesV1);
    }

    [Fact]
    public async Task OmittedCapabilitiesDecodeAsEmpty()
    {
        var discovery = await DiscoverAsync();

        Assert.Empty(discovery.Capabilities);
        Assert.False(discovery.Supports(DiscoveryCapability.ProfileArchivesV1));
        Assert.False(discovery.SupportsProfileArchivesV1);
    }

    [Theory]
    [InlineData("null")]
    [InlineData("{}")]
    [InlineData("\"profile-archives-v1\"")]
    public async Task NullOrWrongShapeCapabilitiesDecodeAsEmpty(string capabilitiesJson)
    {
        var discovery = await DiscoverAsync(capabilitiesJson);

        Assert.Empty(discovery.Capabilities);
    }

    [Fact]
    public async Task CapabilitiesAreNormalizedAndRecognizedQueriesIgnoreUnknowns()
    {
        const string capabilitiesJson = """
            ["profile-archives-v1","future-feature","bounded-aggregate-resources","request-correlation","profile-archives-v1","","UPPERCASE","leading-","two--hyphens","has_underscore",null,7,{}]
            """;

        var discovery = await DiscoverAsync(capabilitiesJson);

        Assert.Equal(
            new[] { "profile-archives-v1", "future-feature", "bounded-aggregate-resources", "request-correlation" },
            discovery.Capabilities);
        Assert.True(discovery.Supports(DiscoveryCapability.ProfileArchivesV1));
        Assert.True(discovery.SupportsProfileArchivesV1);
        Assert.True(discovery.Supports(DiscoveryCapability.BoundedAggregateResources));
        Assert.True(discovery.Supports(DiscoveryCapability.RequestCorrelation));
        Assert.False(discovery.Supports((DiscoveryCapability)int.MaxValue));
    }

    [Fact]
    public async Task CapabilitiesRetainOnlyFirst64SafeUniqueIdentifiers()
    {
        var maximumLengthIdentifier = new string('a', 64);
        var advertised = Enumerable.Range(0, 70)
            .Select(index => $"future-{index}")
            .Prepend("future-0")
            .Prepend(maximumLengthIdentifier)
            .Prepend(new string('a', 65))
            .ToArray();

        var discovery = await DiscoverAsync(JsonSerializer.Serialize(advertised));

        Assert.Equal(64, discovery.Capabilities.Count);
        Assert.Equal(maximumLengthIdentifier, discovery.Capabilities[0]);
        Assert.Equal("future-62", discovery.Capabilities[^1]);
        Assert.Equal(64, discovery.Capabilities.Distinct(StringComparer.Ordinal).Count());
    }

    private static async Task<Discovery> DiscoverAsync(string? capabilitiesJson = null)
    {
        var handler = new DiscoveryHandler(DiscoveryBody(capabilitiesJson));
        using var client = new RivuneApiClient(
            new Uri("https://rivune.test"),
            handler,
            new EmptyCredentialStore());
        return await client.DiscoverAsync(TestContext.Current.CancellationToken);
    }

    private static string DiscoveryBody(string? capabilitiesJson) => $$"""
        {"name":"Rivune","serverVersion":"test","protocolVersion":20,"apiBaseUrl":"/api/v1/","setupRequired":false,"setupCompleted":true,"demoAvailable":false,"timezone":"UTC","interfaceLanguage":"en"{{(capabilitiesJson is null ? string.Empty : ",\"capabilities\":" + capabilitiesJson)}}}
        """;

    private sealed class DiscoveryHandler(string responseBody) : HttpMessageHandler
    {
        protected override Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken) => Task.FromResult(new HttpResponseMessage(HttpStatusCode.OK)
        {
            Content = new StringContent(responseBody, Encoding.UTF8, "application/json"),
        });
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
