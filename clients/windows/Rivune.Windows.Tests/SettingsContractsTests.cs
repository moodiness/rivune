using System.Net;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;
using Rivune.Windows;
using Xunit;

namespace Rivune.Windows.Tests;

public sealed class SettingsContractsTests
{
    private static readonly JsonSerializerOptions JsonOptions = new(JsonSerializerDefaults.Web)
    {
        DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
        UnmappedMemberHandling = JsonUnmappedMemberHandling.Skip,
    };

    private static readonly Guid ProfileId = Guid.Parse("44444444-4444-4444-8444-444444444444");

    [Fact]
    public void CurrentProfileAndEffectiveSettingsRoundTripWithProvenance()
    {
        const string layerJson = """
            {
              "schemaVersion":1,
              "settings":{
                "interfaceLanguage":"fr-CA","theme":"dark","maximumResolution":"2160p","maximumCastMembers":24,"maximumDirectTitles":18,
                "allowTranscoding":true,"transcoding":"enabled","preferDirectPlay":true,"hideUnreleased":false,
                "metadataLanguage":"fr-CA","metadataRegion":"CA","seriesMappingProvider":"tvdb","audioLanguage":"fr","subtitleLanguage":"en","forcedSubtitleLanguage":"off",
                "autoplayNextEpisode":true,"skipIntroEnabled":true,"skipRecapEnabled":false,"skipOutroEnabled":true,
                "cardDensity":"compact","animationsEnabled":false,"subtitleSizePercent":125,"subtitleTextColor":"#AABBCC","subtitleBackgroundOpacityPercent":70,
                "notificationsEnabled":true,"notificationDurationSeconds":8,"notificationPollIntervalSeconds":30,
                "futureSetting":{"ignored":true}
              },
              "updatedAt":"2026-08-17T10:00:00Z",
              "futureLayerField":true
            }
            """;

        var layer = JsonSerializer.Deserialize<SettingsLayer>(layerJson, JsonOptions)!;
        Assert.Equal("fr-CA", layer.Settings.InterfaceLanguage);
        Assert.Equal(24, layer.Settings.MaximumCastMembers);
        Assert.True(layer.Settings.SkipIntroEnabled);
        Assert.Equal("#AABBCC", layer.Settings.SubtitleTextColor);
        Assert.Equal(30, layer.Settings.NotificationPollIntervalSeconds);

        using var document = JsonDocument.Parse(layerJson);
        var effectiveJson = $$"""
            {
              "schemaVersion":1,
              "settings":{{document.RootElement.GetProperty("settings").GetRawText()}},
              "sources":{
                "interfaceLanguage":"profile","theme":"profile","maximumResolution":"instance","maximumCastMembers":"profile","maximumDirectTitles":"instance",
                "allowTranscoding":"instance","transcoding":"profile","preferDirectPlay":"profile","hideUnreleased":"default",
                "metadataLanguage":"profile","metadataRegion":"profile","seriesMappingProvider":"instance","audioLanguage":"profile","subtitleLanguage":"profile","forcedSubtitleLanguage":"default",
                "autoplayNextEpisode":"profile","skipIntroEnabled":"profile","skipRecapEnabled":"default","skipOutroEnabled":"profile",
                "cardDensity":"profile","animationsEnabled":"profile","subtitleSizePercent":"profile","subtitleTextColor":"profile","subtitleBackgroundOpacityPercent":"profile",
                "notificationsEnabled":"instance","notificationDurationSeconds":"instance","notificationPollIntervalSeconds":"default",
                "futureSource":"device"
              },
              "futureEffectiveField":true
            }
            """;

        var effective = JsonSerializer.Deserialize<EffectiveSettings>(effectiveJson, JsonOptions)!;
        Assert.Equal("2160p", effective.Settings.MaximumResolution);
        Assert.Equal(18, effective.Settings.MaximumDirectTitles);
        Assert.False(effective.Settings.SkipRecapEnabled);
        Assert.Equal(SettingSource.Profile, effective.Sources.InterfaceLanguage);
        Assert.Equal(SettingSource.Instance, effective.Sources.NotificationsEnabled);
        Assert.Equal(SettingSource.Default, effective.Sources.NotificationPollIntervalSeconds);
    }

    [Fact]
    public async Task InstanceEndpointsDecodeSchemaThreeRuntimeAndSendCompleteSparsePatch()
    {
        var handler = new SettingsHandler();
        using var client = CreateClient(handler);

        var layer = await client.GetInstanceSettingsAsync(TestContext.Current.CancellationToken);
        Assert.Equal(3, layer.SchemaVersion);
        Assert.Equal(42, layer.Revision);
        Assert.Equal("UTC", layer.Settings.Timezone);
        Assert.Equal("nvenc", layer.Settings.HardwareAcceleration);
        Assert.Equal(4, layer.Runtime.Active.TranscodeConcurrency);
        Assert.Equal(6, layer.Runtime.Requested.TranscodeConcurrency);
        Assert.Equal(new[] { "transcodeConcurrency" }, layer.Runtime.PendingRestart);

        var updated = await client.UpdateInstanceSettingsAsync(
            new InstanceSettingsPatch
            {
                InterfaceLanguage = PatchField<string>.Null,
                MaximumCastMembers = PatchField<int>.FromValue(30),
                AllowTranscoding = PatchField<bool>.FromValue(false),
                NotificationsEnabled = PatchField<bool>.FromValue(true),
                Timezone = "Europe/Paris",
                JellyfinEnabled = true,
                JellyfinDebug = false,
                HardwareAcceleration = "qsv",
                PreferredTranscodeVideoCodec = "hevc",
                TranscodeQualityPreset = "quality",
                TranscodeConcurrency = 8,
                TranscodeMaxBitrateKbps = 24_000,
                MediaMaxStorageMB = 40_960,
                ArtworkMaxStorageMB = 10_240,
            },
            TestContext.Current.CancellationToken);
        Assert.Equal(42, updated.Revision);

        var request = handler.ApiRequests.Single(item => item.Method == HttpMethod.Patch && item.Path == "/api/v1/settings");
        using var body = JsonDocument.Parse(request.Body!);
        Assert.Equal(JsonValueKind.Null, body.RootElement.GetProperty("interfaceLanguage").ValueKind);
        Assert.Equal(30, body.RootElement.GetProperty("maximumCastMembers").GetInt32());
        Assert.False(body.RootElement.GetProperty("allowTranscoding").GetBoolean());
        Assert.True(body.RootElement.GetProperty("notificationsEnabled").GetBoolean());
        Assert.Equal("Europe/Paris", body.RootElement.GetProperty("timezone").GetString());
        Assert.True(body.RootElement.GetProperty("jellyfinEnabled").GetBoolean());
        Assert.False(body.RootElement.GetProperty("jellyfinDebug").GetBoolean());
        Assert.Equal("qsv", body.RootElement.GetProperty("hardwareAcceleration").GetString());
        Assert.Equal("hevc", body.RootElement.GetProperty("preferredTranscodeVideoCodec").GetString());
        Assert.Equal("quality", body.RootElement.GetProperty("transcodeQualityPreset").GetString());
        Assert.Equal(8, body.RootElement.GetProperty("transcodeConcurrency").GetInt32());
        Assert.Equal(24_000, body.RootElement.GetProperty("transcodeMaxBitrateKbps").GetInt32());
        Assert.Equal(40_960, body.RootElement.GetProperty("mediaMaxStorageMB").GetInt32());
        Assert.Equal(10_240, body.RootElement.GetProperty("artworkMaxStorageMB").GetInt32());
        Assert.False(body.RootElement.TryGetProperty("theme", out _));
    }

    [Fact]
    public async Task ProfileEndpointsDecodeLayerAndPatchEveryAllowedFieldWithoutOmittedValues()
    {
        var handler = new SettingsHandler();
        using var client = CreateClient(handler);

        var layer = await client.GetProfileSettingsAsync(ProfileId, TestContext.Current.CancellationToken);
        Assert.Equal(1, layer.SchemaVersion);
        Assert.Equal("dark", layer.Settings.Theme);

        await client.UpdateProfileSettingsAsync(
            ProfileId,
            new SettingsPatch
            {
                InterfaceLanguage = PatchField<string>.FromValue("en"),
                Theme = PatchField<string>.Null,
                MaximumResolution = PatchField<string>.FromValue("1080p"),
                MaximumCastMembers = PatchField<int>.FromValue(20),
                MaximumDirectTitles = PatchField<int>.Null,
                Transcoding = PatchField<string>.FromValue("inherit"),
                PreferDirectPlay = PatchField<bool>.FromValue(false),
                HideUnreleased = PatchField<bool>.FromValue(true),
                MetadataLanguage = PatchField<string>.FromValue("auto"),
                MetadataRegion = PatchField<string>.FromValue("US"),
                SeriesMappingProvider = PatchField<string>.FromValue("tmdb"),
                AudioLanguage = PatchField<string>.FromValue("en"),
                SubtitleLanguage = PatchField<string>.FromValue("es"),
                ForcedSubtitleLanguage = PatchField<string>.FromValue("off"),
                AutoplayNextEpisode = PatchField<bool>.FromValue(true),
                SkipIntroEnabled = PatchField<bool>.FromValue(true),
                SkipRecapEnabled = PatchField<bool>.FromValue(false),
                SkipOutroEnabled = PatchField<bool>.FromValue(true),
                CardDensity = PatchField<string>.FromValue("comfortable"),
                AnimationsEnabled = PatchField<bool>.FromValue(false),
                SubtitleSizePercent = PatchField<int>.FromValue(110),
                SubtitleTextColor = PatchField<string>.FromValue("#FFFFFF"),
                SubtitleBackgroundOpacityPercent = PatchField<int>.FromValue(60),
            },
            TestContext.Current.CancellationToken);

        var request = handler.ApiRequests.Single(item => item.Method == HttpMethod.Patch && item.Path.EndsWith("/settings", StringComparison.Ordinal));
        using var body = JsonDocument.Parse(request.Body!);
        string[] expectedNames =
        [
            "interfaceLanguage", "theme", "maximumResolution", "maximumCastMembers", "maximumDirectTitles", "transcoding", "preferDirectPlay", "hideUnreleased",
            "metadataLanguage", "metadataRegion", "seriesMappingProvider", "audioLanguage", "subtitleLanguage", "forcedSubtitleLanguage", "autoplayNextEpisode",
            "skipIntroEnabled", "skipRecapEnabled", "skipOutroEnabled", "cardDensity", "animationsEnabled", "subtitleSizePercent", "subtitleTextColor",
            "subtitleBackgroundOpacityPercent",
        ];
        Assert.Equal(expectedNames.Order(), body.RootElement.EnumerateObject().Select(property => property.Name).Order());
        Assert.Equal(JsonValueKind.Null, body.RootElement.GetProperty("theme").ValueKind);
        Assert.Equal(JsonValueKind.Null, body.RootElement.GetProperty("maximumDirectTitles").ValueKind);
        Assert.False(body.RootElement.GetProperty("preferDirectPlay").GetBoolean());
    }

    private static RivuneApiClient CreateClient(HttpMessageHandler handler) =>
        new(new Uri("https://rivune.test"), handler, new StubCredentialStore());

    private sealed class StubCredentialStore : ICredentialStore
    {
        public ValueTask<StoredCredentials?> LoadAsync(CancellationToken cancellationToken = default) =>
            ValueTask.FromResult<StoredCredentials?>(new StoredCredentials
            {
                Issuer = "https://rivune.test/",
                Credentials = new TokenPair
                {
                    TokenType = "Bearer",
                    AccessToken = "access",
                    AccessTokenExpiresAt = "2026-08-17T12:15:00Z",
                    RefreshToken = "refresh",
                    RefreshTokenExpiresAt = "2026-09-17T12:00:00Z",
                    SessionId = Guid.Parse("22222222-2222-4222-8222-222222222222"),
                    DeviceId = Guid.Parse("33333333-3333-4333-8333-333333333333"),
                    AuthorizationScope = AuthorizationScope.GlobalAdministrator,
                    Category = null,
                },
            });

        public ValueTask SaveAsync(StoredCredentials credentials, CancellationToken cancellationToken = default) => ValueTask.CompletedTask;
        public ValueTask ClearAsync(CancellationToken cancellationToken = default) => ValueTask.CompletedTask;
    }

    private sealed class SettingsHandler : HttpMessageHandler
    {
        public List<(HttpMethod Method, string Path, string? Body)> ApiRequests { get; } = [];

        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            if (request.RequestUri!.AbsolutePath == "/.well-known/rivune")
            {
                return JsonResponse("""{"name":"Rivune","serverVersion":"test","protocolVersion":20,"apiBaseUrl":"/api/v1","setupRequired":false,"setupCompleted":true,"demoAvailable":false,"timezone":"UTC","interfaceLanguage":"en"}""");
            }

            ApiRequests.Add((request.Method, request.RequestUri.AbsolutePath, request.Content is null ? null : await request.Content.ReadAsStringAsync(cancellationToken)));
            return request.RequestUri.AbsolutePath == "/api/v1/settings"
                ? JsonResponse(InstanceLayerJson)
                : JsonResponse(ProfileLayerJson);
        }

        private static HttpResponseMessage JsonResponse(string body) => new(HttpStatusCode.OK)
        {
            Content = new StringContent(body, Encoding.UTF8, "application/json"),
        };

        private const string ProfileLayerJson = """
            {"schemaVersion":1,"settings":{"theme":"dark","maximumCastMembers":null,"futureSetting":true},"updatedAt":null,"futureLayerField":true}
            """;

        private const string InstanceLayerJson = """
            {
              "schemaVersion":3,"revision":42,
              "settings":{"timezone":"UTC","jellyfinEnabled":false,"jellyfinDebug":false,"hardwareAcceleration":"nvenc","preferredTranscodeVideoCodec":"h264","transcodeQualityPreset":"balanced","transcodeConcurrency":6,"transcodeMaxBitrateKbps":12000,"mediaMaxStorageMB":20480,"artworkMaxStorageMB":20480,"allowTranscoding":true,"theme":"dark","futureSetting":true},
              "runtime":{
                "active":{"timezone":"UTC","jellyfinEnabled":false,"jellyfinDebug":false,"hardwareAcceleration":"nvenc","preferredTranscodeVideoCodec":"h264","transcodeQualityPreset":"balanced","transcodeConcurrency":4,"transcodeMaxBitrateKbps":12000,"mediaMaxStorageMB":20480,"artworkMaxStorageMB":20480,"allowTranscoding":true},
                "requested":{"timezone":"UTC","jellyfinEnabled":false,"jellyfinDebug":false,"hardwareAcceleration":"nvenc","preferredTranscodeVideoCodec":"h264","transcodeQualityPreset":"balanced","transcodeConcurrency":6,"transcodeMaxBitrateKbps":12000,"mediaMaxStorageMB":20480,"artworkMaxStorageMB":20480,"allowTranscoding":true},
                "pendingRestart":["transcodeConcurrency"],"futureRuntimeField":true
              },
              "updatedAt":"2026-08-17T10:00:00Z","futureLayerField":true
            }
            """;
    }
}
