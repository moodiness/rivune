using System.Text.Json;
using System.Text.Json.Serialization;
using Rivune.Windows;
using Xunit;

namespace Rivune.Windows.Tests;

public sealed class TranscodingModelsTests
{
    private static readonly JsonSerializerOptions JsonOptions = new(JsonSerializerDefaults.Web)
    {
        DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
        UnmappedMemberHandling = JsonUnmappedMemberHandling.Skip,
    };

    [Fact]
    public void CapabilitiesEncodeServerOutputDeclarations()
    {
        var capabilities = new PlaybackCapabilities
        {
            StreamingProtocols = ["hls"],
            Containers = ["mp4"],
            ProcessingModes = [PlaybackProcessingMode.Remux, PlaybackProcessingMode.TranscodeAudio, PlaybackProcessingMode.Transcode],
            MaximumHeight = 2160,
            MaximumVideoBitrateKbps = 12_000,
            MaximumAudioChannels = 6,
            SubtitleModes = [PlaybackSubtitleDelivery.External, PlaybackSubtitleDelivery.Burn],
            MediaProfiles = [new PlaybackMediaProfile { Container = "mp4", VideoCodec = "h265", AudioCodec = "aac", MaximumVideoBitDepth = 10 }],
        };

        using var encoded = JsonDocument.Parse(JsonSerializer.Serialize(capabilities, JsonOptions));
        Assert.Equal(new[] { "remux", "transcode_audio", "transcode" }, encoded.RootElement.GetProperty("processingModes").EnumerateArray().Select(item => item.GetString()).ToArray());
        Assert.Equal(2160, encoded.RootElement.GetProperty("maximumHeight").GetInt32());
        Assert.Equal(12_000, encoded.RootElement.GetProperty("maximumVideoBitrateKbps").GetInt32());
        Assert.Equal(6, encoded.RootElement.GetProperty("maximumAudioChannels").GetInt32());
        Assert.Equal(new[] { "external", "burn" }, encoded.RootElement.GetProperty("subtitleModes").EnumerateArray().Select(item => item.GetString()).ToArray());
        Assert.Equal(10, encoded.RootElement.GetProperty("mediaProfiles")[0].GetProperty("maximumVideoBitDepth").GetInt32());
    }

    [Fact]
    public void SourceListDecodesOptionalAddonNameAndDefaultsOmittedSourceIdentity()
    {
        const string json = """
        {
          "sources":[
            {"id":"source-1","sourceRef":"ref-1","stableIdentity":"stable-1","addonId":"66666666-6666-4666-8666-666666666666","addonName":"Test Addon","manifestId":"org.test","streamIndex":0,"name":"Primary","protocol":"hls","expiresAt":"2026-08-03T12:00:00Z"},
            {"id":"source-2","sourceRef":"ref-2","addonId":"77777777-7777-4777-8777-777777777777","manifestId":"org.other","streamIndex":1,"name":"Fallback","protocol":"dash","expiresAt":"2026-08-03T12:00:00Z"}
          ],
          "providerErrors":[]
        }
        """;

        var sourceList = JsonSerializer.Deserialize<PlaybackSourceList>(json, JsonOptions)!;
        Assert.Equal("Test Addon", sourceList.Sources[0].AddonName);
        Assert.Equal(Guid.Parse("66666666-6666-4666-8666-666666666666"), sourceList.Sources[0].AddonId);
        Assert.Equal("org.test", sourceList.Sources[0].ManifestId);
        Assert.Equal("ref-1", sourceList.Sources[0].SourceRef);
        Assert.Equal("stable-1", sourceList.Sources[0].StableIdentity);
        Assert.Null(sourceList.Sources[1].AddonName);
        Assert.Equal(Guid.Parse("77777777-7777-4777-8777-777777777777"), sourceList.Sources[1].AddonId);
        Assert.Equal("org.other", sourceList.Sources[1].ManifestId);
        Assert.Equal("ref-2", sourceList.Sources[1].SourceRef);
        Assert.Equal(string.Empty, sourceList.Sources[1].StableIdentity);
    }

    [Fact]
    public void SessionDecodesDecisionBurnSubtitleSelectionsAndUnknownProperties()
    {
        const string json = """
        {
          "id":"22222222-2222-4222-8222-222222222222",
          "selectedSourceId":"source-1","selectedAudioTrack":2,"selectedSubtitleId":"subtitle-1",
          "sources":[{"id":"source-1","addonId":"66666666-6666-4666-8666-666666666666","manifestId":"org.test","mode":"transcode","protocol":"hls","mediaTimeline":"relative","compatible":true,"decision":{"reason":"subtitle_burn_required","reasons":["video_codec_not_supported","resolution_limit","hdr_not_supported"],"videoAction":"transcode","audioAction":"copy","subtitleAction":"burn","toneMapping":false,"source":{"container":"matroska","videoCodec":"hevc","height":2160,"videoBitrateKbps":24000,"hdrFormat":"dolby_vision"},"target":{"protocol":"hls","container":"mpegts","videoCodec":"h264","audioCodec":"aac","height":1080,"videoBitDepth":8,"videoBitrateKbps":12000},"futureDecisionField":true}}],
          "subtitles":[{"id":"subtitle-1","addonId":"66666666-6666-4666-8666-666666666666","manifestId":"org.test","default":true,"delivery":"burn","futureSubtitleField":"ignored"}],
          "providerErrors":[{"addonId":"66666666-6666-4666-8666-666666666666","manifestId":"org.test","code":"future_provider_code","message":"future"}],
          "expiresAt":"2026-08-03T12:00:00Z","futureSessionField":"ignored"
        }
        """;

        var session = JsonSerializer.Deserialize<PlaybackSession>(json, JsonOptions)!;
        Assert.Equal(2, session.SelectedAudioTrack);
        Assert.Equal("subtitle-1", session.SelectedSubtitleId);
        Assert.Equal(PlaybackDecisionReason.SubtitleBurnRequired, session.Sources[0].Decision?.Reason);
        Assert.Equal("dolby_vision", session.Sources[0].Decision?.Source?.HdrFormat);
        Assert.Equal(12_000, session.Sources[0].Decision?.Target?.VideoBitrateKbps);
        Assert.Equal(8, session.Sources[0].Decision?.Target?.VideoBitDepth);
        Assert.Equal(PlaybackSubtitleDelivery.Burn, session.Subtitles[0].Delivery);
        Assert.Equal(PlaybackMediaTimeline.Relative, session.Sources[0].MediaTimeline);
        Assert.Null(session.Subtitles[0].Url);
        Assert.Equal("future_provider_code", session.ProviderErrors[0].Code);
    }

    [Theory]
    [InlineData("{\"sources\":null,\"providerErrors\":[]}", typeof(PlaybackSourceList))]
    [InlineData("{\"id\":\"22222222-2222-4222-8222-222222222222\",\"selectedSourceId\":\"source-1\",\"sources\":null,\"subtitles\":[],\"providerErrors\":[],\"expiresAt\":\"2026-08-03T12:00:00Z\"}", typeof(PlaybackSession))]
    public void NullRequiredPlaybackCollectionsAreRejected(string json, Type modelType)
    {
        Assert.Throws<JsonException>(() => JsonSerializer.Deserialize(json, modelType, JsonOptions));
    }

    [Fact]
    public void ActivityDecodesDecisionDetails()
    {
        const string json = """
        {
          "summary":{"activeSessions":1,"activeJobs":2,"processingSlots":1,"processingLimit":2,"storageBytes":1024,"storageLimitBytes":1048576},
          "diagnostics":{"ffmpegVersion":"7.1","ffprobeVersion":"7.1","hardwareAcceleration":"nvenc","videoEncoder":"h264_nvenc","preferredVideoCodec":"h264","encodeCodecs":["h264","hevc"],"decodeCodecs":["h264","hevc","av1"],"hevcMain10":true,"qualityPreset":"balanced","hardwareToneMap":false,"toneMapBackend":"software","transcodeThreads":4,"maximumReadRate":2.5,"totals":{"started":12,"succeeded":10,"failed":2,"softwareFallbacks":1},"pools":{"process":{"active":1,"limit":2},"probe":{"active":1,"limit":4},"subtitle":{"active":0,"limit":2},"trickplay":{"active":0,"limit":1}}},
          "sessions":[{
            "id":"22222222-2222-4222-8222-222222222222","title":"Contract Movie","mediaType":"movie","mode":"transcode",
            "decision":{"reason":"video_transcode_required","reasons":["video_codec_not_supported","bitrate_limit"],"videoAction":"transcode","audioAction":"transcode","subtitleAction":"none","toneMapping":true,"target":{"videoCodec":"h264","height":1080,"videoBitrateKbps":12000}},
            "username":"admin","profileId":"44444444-4444-4444-8444-444444444444","profile":"Admin","device":"Windows PC","platform":"windows",
            "processing":true,"positionSeconds":120,"durationSeconds":7200,
            "createdAt":"2026-08-03T10:00:00Z","lastSeenAt":"2026-08-03T10:01:00Z","expiresAt":"2026-08-03T12:00:00Z"
          }],
          "jobs":[
            {"sessionId":"22222222-2222-4222-8222-222222222222","assetId":"asset-1","mode":"transcode","state":"processing","prewarming":false,"progressPercent":37.5,"speed":1.25,"startupDurationSeconds":4.5,"createdAt":"2026-08-03T10:00:00Z","lastSeenAt":"2026-08-03T10:01:00Z"},
            {"assetId":"asset-2","mode":"transcode","state":"failed","errorClass":"capacity","prewarming":true,"createdAt":"2026-08-03T10:00:00Z","lastSeenAt":"2026-08-03T10:01:00Z"}
          ],
          "sessionsTruncated":false,
          "jobsTruncated":true,
          "futureActivityField":"ignored"
        }
        """;

        var activity = JsonSerializer.Deserialize<PlaybackActivity>(json, JsonOptions)!;
        Assert.Equal(1080, activity.Sessions[0].Decision?.Target?.Height);
        Assert.True(activity.Sessions[0].Decision?.ToneMapping == true);
        Assert.Equal(37.5, activity.Jobs[0].ProgressPercent);
        Assert.Equal(1.25, activity.Jobs[0].Speed);
        Assert.Null(activity.Jobs[1].ProgressPercent);
        Assert.Null(activity.Jobs[1].Speed);
        Assert.Equal(PlaybackHardwareAcceleration.Nvenc, activity.Diagnostics.HardwareAcceleration);
        Assert.Equal(12, activity.Diagnostics.Totals.Started);
        Assert.Equal(4, activity.Diagnostics.Pools.Probe.Limit);
        Assert.Equal(4.5, activity.Jobs[0].StartupDurationSeconds);
        Assert.Equal(PlaybackMediaJobErrorClass.Capacity, activity.Jobs[1].ErrorClass);
        Assert.False(activity.SessionsTruncated);
        Assert.True(activity.JobsTruncated);
    }

    [Fact]
    public void UnknownErrorCodesAreTolerated()
    {
        var error = JsonSerializer.Deserialize<ServerError>("{\"code\":\"future_error_code\",\"message\":\"future\",\"futureField\":true}", JsonOptions)!;
        Assert.Equal("future_error_code", error.Code);
    }
}
