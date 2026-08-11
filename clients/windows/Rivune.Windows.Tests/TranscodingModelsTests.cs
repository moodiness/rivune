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
            ProcessingModes = ["remux", "transcode_audio", "transcode"],
            MaximumHeight = 2160,
            MaximumVideoBitrateKbps = 12_000,
            MaximumAudioChannels = 6,
            SubtitleModes = ["external", "burn"],
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
    public void SourceListDecodesOptionalAddonNameAndSourceIdentity()
    {
        const string json = """
        {
          "sources":[
            {"id":"source-1","sourceRef":"ref-1","addonId":"66666666-6666-4666-8666-666666666666","addonName":"Test Addon","manifestId":"org.test","streamIndex":0,"name":"Primary","protocol":"hls","expiresAt":"2026-08-03T12:00:00Z"},
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
        Assert.Null(sourceList.Sources[1].AddonName);
        Assert.Equal(Guid.Parse("77777777-7777-4777-8777-777777777777"), sourceList.Sources[1].AddonId);
        Assert.Equal("org.other", sourceList.Sources[1].ManifestId);
        Assert.Equal("ref-2", sourceList.Sources[1].SourceRef);
    }

    [Fact]
    public void SessionDecodesDecisionBurnSubtitleSelectionsAndUnknownProperties()
    {
        const string json = """
        {
          "id":"22222222-2222-4222-8222-222222222222",
          "selectedSourceId":"source-1","selectedAudioTrack":2,"selectedSubtitleId":"subtitle-1",
          "sources":[{"id":"source-1","addonId":"66666666-6666-4666-8666-666666666666","manifestId":"org.test","mode":"transcode","protocol":"hls","compatible":true,"decision":{"reason":"subtitle_burn_required","videoAction":"transcode","audioAction":"copy","subtitleAction":"burn","toneMapping":false,"source":{"container":"matroska","videoCodec":"hevc","height":2160,"videoBitrateKbps":24000,"hdrFormat":"dolby_vision"},"target":{"protocol":"hls","container":"mpegts","videoCodec":"h264","audioCodec":"aac","height":1080,"videoBitDepth":8,"videoBitrateKbps":12000},"futureDecisionField":true}}],
          "subtitles":[{"id":"subtitle-1","addonId":"66666666-6666-4666-8666-666666666666","manifestId":"org.test","default":true,"delivery":"burn","futureSubtitleField":"ignored"}],
          "providerErrors":[{"addonId":"66666666-6666-4666-8666-666666666666","manifestId":"org.test","code":"future_provider_code","message":"future"}],
          "expiresAt":"2026-08-03T12:00:00Z","futureSessionField":"ignored"
        }
        """;

        var session = JsonSerializer.Deserialize<PlaybackSession>(json, JsonOptions)!;
        Assert.Equal(2, session.SelectedAudioTrack);
        Assert.Equal("subtitle-1", session.SelectedSubtitleId);
        Assert.Equal("subtitle_burn_required", session.Sources[0].Decision?.Reason);
        Assert.Equal("dolby_vision", session.Sources[0].Decision?.Source?.HdrFormat);
        Assert.Equal(12_000, session.Sources[0].Decision?.Target?.VideoBitrateKbps);
        Assert.Equal(8, session.Sources[0].Decision?.Target?.VideoBitDepth);
        Assert.Equal("burn", session.Subtitles[0].Delivery);
        Assert.Null(session.Subtitles[0].Url);
        Assert.Equal("future_provider_code", session.ProviderErrors[0].Code);
    }

    [Fact]
    public void ActivityDecodesDecisionDetails()
    {
        const string json = """
        {
          "summary":{"activeSessions":1,"activeJobs":2,"processingSlots":1,"processingLimit":2,"storageBytes":1024,"storageLimitBytes":1048576},
          "diagnostics":{"videoEncoder":"libx264","hardwareToneMap":false},
          "sessions":[{
            "id":"22222222-2222-4222-8222-222222222222","title":"Contract Movie","mediaType":"movie","mode":"transcode",
            "decision":{"reason":"video_transcode_required","videoAction":"transcode","audioAction":"transcode","subtitleAction":"none","toneMapping":true,"target":{"videoCodec":"h264","height":1080,"videoBitrateKbps":12000}},
            "username":"admin","profileId":"44444444-4444-4444-8444-444444444444","profile":"Admin","device":"Windows PC","platform":"windows",
            "processing":true,"positionSeconds":120,"durationSeconds":7200,
            "createdAt":"2026-08-03T10:00:00Z","lastSeenAt":"2026-08-03T10:01:00Z","expiresAt":"2026-08-03T12:00:00Z"
          }],
          "jobs":[
            {"sessionId":"22222222-2222-4222-8222-222222222222","assetId":"asset-1","mode":"transcode","state":"running","prewarming":false,"progressPercent":37.5,"speed":1.25,"createdAt":"2026-08-03T10:00:00Z","lastSeenAt":"2026-08-03T10:01:00Z"},
            {"assetId":"asset-2","mode":"transcode","state":"running","prewarming":true,"createdAt":"2026-08-03T10:00:00Z","lastSeenAt":"2026-08-03T10:01:00Z"}
          ],
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
    }

    [Fact]
    public void MissingNewSettingsAndUnknownErrorCodesAreTolerated()
    {
        var values = JsonSerializer.Deserialize<SettingsValues>("{\"futureSetting\":true}", JsonOptions)!;
        Assert.Null(values.AllowTranscoding);
        Assert.Null(values.Transcoding);
        Assert.Null(values.MaximumCastMembers);

        var inherited = JsonSerializer.Deserialize<SettingsValues>("{\"maximumCastMembers\":null}", JsonOptions)!;
        Assert.Null(inherited.MaximumCastMembers);

        var effective = JsonSerializer.Deserialize<EffectiveSettings>(
            """{"schemaVersion":1,"settings":{"allowTranscoding":false,"transcoding":"enabled","maximumCastMembers":20,"futureSetting":true},"sources":{"allowTranscoding":"instance","transcoding":"profile","maximumCastMembers":"instance","theme":"default"}}""",
            JsonOptions)!;
        Assert.Equal(false, effective.Settings.AllowTranscoding);
        Assert.Equal("enabled", effective.Settings.Transcoding);
        Assert.Equal(20, effective.Settings.MaximumCastMembers);
        Assert.Equal("instance", effective.Sources.AllowTranscoding);
        Assert.Equal("instance", effective.Sources.MaximumCastMembers);

        var error = JsonSerializer.Deserialize<ServerError>("{\"code\":\"future_error_code\",\"message\":\"future\",\"futureField\":true}", JsonOptions)!;
        Assert.Equal("future_error_code", error.Code);
    }
}
