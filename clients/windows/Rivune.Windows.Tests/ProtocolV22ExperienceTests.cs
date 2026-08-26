using System.Text.Json;
using Rivune.App;
using Rivune.Windows;
using Xunit;

namespace Rivune.Windows.Tests;

public sealed class ProtocolV22ExperienceTests
{
    private static readonly JsonSerializerOptions JsonOptions = new(JsonSerializerDefaults.Web);

    [Theory]
    [InlineData("load", PlaybackCommandKind.Load)]
    [InlineData("play", PlaybackCommandKind.Play)]
    [InlineData("pause", PlaybackCommandKind.Pause)]
    [InlineData("seek", PlaybackCommandKind.Seek)]
    [InlineData("stop", PlaybackCommandKind.Stop)]
    public void PlaybackCommandKindsAreClosedWireValues(string wire, PlaybackCommandKind expected) =>
        Assert.Equal(expected, JsonSerializer.Deserialize<PlaybackCommandKind>($"\"{wire}\"", JsonOptions));

    [Fact]
    public void UnknownPlaybackCommandKindIsRejected() =>
        Assert.Throws<JsonException>(() => JsonSerializer.Deserialize<PlaybackCommandKind>("\"rewind\"", JsonOptions));

    [Fact]
    public void PlaybackDecisionRequiresClosedUniqueReasons()
    {
        var decision = JsonSerializer.Deserialize<PlaybackDecision>("""
            {"reason":"video_transcode_required","reasons":["video_codec_not_supported","bitrate_limit"],"videoAction":"transcode","audioAction":"copy","subtitleAction":"none","toneMapping":false}
            """, JsonOptions)!;

        Assert.Equal(new[] { PlaybackDecisionDetailReason.VideoCodecNotSupported, PlaybackDecisionDetailReason.BitrateLimit }, decision.Reasons);
        Assert.Equal("The video codec is not supported directly. The source exceeds this network's bitrate limit.", PlaybackDecisionPresentation.Summary(decision));
        Assert.DoesNotContain("http", PlaybackDecisionPresentation.Summary(decision), StringComparison.OrdinalIgnoreCase);
        Assert.DoesNotContain("token", PlaybackDecisionPresentation.Summary(decision), StringComparison.OrdinalIgnoreCase);
    }

    [Theory]
    [InlineData("{\"reason\":\"direct_supported\",\"reasons\":[\"bitrate_limit\"],\"videoAction\":\"copy\",\"audioAction\":\"copy\",\"subtitleAction\":\"none\",\"toneMapping\":false}")]
    [InlineData("{\"reason\":\"video_transcode_required\",\"reasons\":[\"bitrate_limit\",\"bitrate_limit\"],\"videoAction\":\"transcode\",\"audioAction\":\"copy\",\"subtitleAction\":\"none\",\"toneMapping\":false}")]
    [InlineData("{\"reason\":\"video_transcode_required\",\"videoAction\":\"transcode\",\"audioAction\":\"copy\",\"subtitleAction\":\"none\",\"toneMapping\":false}")]
    public void InvalidDecisionReasonsFailClosed(string json) =>
        Assert.Throws<JsonException>(() => JsonSerializer.Deserialize<PlaybackDecision>(json, JsonOptions));

    [Fact]
    public void ProfileArchiveParserRequiresStrictVersionTwoAndNoUnknownRootFields()
    {
        var valid = System.Text.Encoding.UTF8.GetBytes("""
            {"version":2,"exportedAt":"2026-08-26T00:00:00Z","identity":{"name":"Viewer","description":null,"isChild":false,"avatar":{"kind":"preset","presetId":"blue"}},"settings":{},"addons":[],"collections":[],"titles":[],"library":[],"progress":[],"favorites":[],"userData":[],"continueDismissals":[],"trackingPreferences":[]}
            """);
        var document = RivuneApiClient.ParseProfileArchive(valid);
        Assert.Equal(2, document.Version);

        var unknown = System.Text.Encoding.UTF8.GetBytes(System.Text.Encoding.UTF8.GetString(valid).Replace("\"trackingPreferences\":[]", "\"trackingPreferences\":[],\"credentials\":{}", StringComparison.Ordinal));
        Assert.Throws<InvalidResponseException>(() => RivuneApiClient.ParseProfileArchive(unknown));
    }
}
