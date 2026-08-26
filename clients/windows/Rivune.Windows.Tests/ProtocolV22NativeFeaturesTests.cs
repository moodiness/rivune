using System.Net;
using System.Text;
using System.Text.Json;
using Rivune.App.ViewModels;
using Rivune.Windows;
using Xunit;

namespace Rivune.Windows.Tests;

public sealed class ProtocolV22NativeFeaturesTests
{
    private static readonly JsonSerializerOptions JsonOptions = new(JsonSerializerDefaults.Web);
    private static readonly Guid ProfileId = Guid.Parse("11111111-1111-4111-8111-111111111111");
    private static readonly Guid ItemId = Guid.Parse("22222222-2222-4222-8222-222222222222");
    private static readonly Guid OperationId = Guid.Parse("33333333-3333-4333-8333-333333333333");

    [Theory]
    [InlineData("source_failed", PlaybackFailoverError.SourceFailed)]
    [InlineData("source_timeout", PlaybackFailoverError.SourceTimeout)]
    [InlineData("ended_early", PlaybackFailoverError.EndedEarly)]
    [InlineData("decode_failed", PlaybackFailoverError.DecodeFailed)]
    [InlineData("access_denied", PlaybackFailoverError.AccessDenied)]
    [InlineData("user_cancelled", PlaybackFailoverError.UserCancelled)]
    public void FailoverErrorsAreClosedWireValues(string wire, PlaybackFailoverError expected) =>
        Assert.Equal(expected, JsonSerializer.Deserialize<PlaybackFailoverError>($"\"{wire}\"", JsonOptions));

    [Fact]
    public void UnknownEnumsAndFreeFormSmartRulesAreRejected()
    {
        Assert.Throws<JsonException>(() => JsonSerializer.Deserialize<MediaNotificationKind>("\"other\"", JsonOptions));
        Assert.Throws<JsonException>(() => JsonSerializer.Deserialize<SmartRule>("""{"type":"sql","value":"select * from titles"}""", JsonOptions));
        var rule = JsonSerializer.Deserialize<SmartRule>("""{"type":"genre","operator":"equals","value":"Drama"}""", JsonOptions);
        Assert.Equal(new SmartGenreRule(SmartTextOperator.Equals, "Drama"), rule);
    }

    [Fact]
    public void AccessibilityDocumentRejectsUnsupportedScale()
    {
        Assert.Throws<JsonException>(() => JsonSerializer.Deserialize<AccessibilityPreferencesDocument>("""
            {"revision":1,"reducedMotion":"system","highContrast":"system","textScale":125,"captions":"system","audioDescription":false,"focusIndicators":"standard"}
            """, JsonOptions));
    }

    [Fact]
    public async Task QueueUsesProfileRouteCasAndStableOperationId()
    {
        var handler = new FeatureHandler();
        using var client = CreateClient(handler);
        var operation = new ReadingQueueOperation(OperationId, 7);
        var controller = new ReadingQueueController();

        await controller.RemoveAsync(client, ProfileId, ItemId, operation, TestContext.Current.CancellationToken);
        await controller.RemoveAsync(client, ProfileId, ItemId, operation, TestContext.Current.CancellationToken);

        Assert.Equal(2, handler.Requests.Count);
        Assert.All(handler.Requests, request =>
        {
            Assert.Equal(HttpMethod.Delete, request.Method);
            Assert.Equal($"/api/v1/profiles/{ProfileId:D}/queue/items/{ItemId:D}", request.Path);
            using var json = JsonDocument.Parse(request.Body!);
            Assert.Equal(OperationId, json.RootElement.GetProperty("operationId").GetGuid());
            Assert.Equal(7, json.RootElement.GetProperty("expectedRevision").GetInt64());
            Assert.Equal("profile-context", request.ProfileContext);
        });
    }

    [Fact]
    public async Task NewFeatureRoutesMatchProtocol()
    {
        var handler = new FeatureHandler();
        using var client = CreateClient(handler);
        var token = TestContext.Current.CancellationToken;

        await client.GetSavedSearchesAsync(token);
        await client.GetSmartCollectionsAsync(token);
        await client.GetAddonIncidentsAsync(token);
        await client.GetMediaNotificationsAsync(limit: 30, cancellationToken: token);
        await client.GetProfileAccessibilityPreferencesAsync(ProfileId, token);
        await client.AdvancePlaybackFailoverAsync(ItemId, new PlaybackFailoverAdvanceInput(PlaybackFailoverError.SourceTimeout, 42.5, 3), token);
        await client.CreateSavedSearchAsync(new SavedSearchInput("Drama", "quiet drama", SavedSearchSort.Relevance, SavedSearchMediaType.Movie), token);
        await client.CreateSmartCollectionAsync(new SmartCollectionInput("Drama", new SmartGenreRule(SmartTextOperator.Equals, "Drama"), SmartCollectionSort.Title), token);
        await client.AcknowledgeMediaNotificationAsync("42", MediaNotificationAcknowledgementState.Dismissed, token);
        await client.UpdateProfileAccessibilityPreferencesAsync(ProfileId, Preferences(), token);
        await client.CancelPlaybackFailoverAsync(ItemId, token);

        Assert.Contains(handler.Requests, request => request.Method == HttpMethod.Get && request.Path == "/api/v1/saved-searches");
        Assert.Contains(handler.Requests, request => request.Method == HttpMethod.Get && request.Path == "/api/v1/smart-collections");
        Assert.Contains(handler.Requests, request => request.Method == HttpMethod.Get && request.Path == "/api/v1/operations/extension-incidents");
        Assert.Contains(handler.Requests, request => request.Method == HttpMethod.Get && request.Path == "/api/v1/media-notifications" && request.Query == "limit=30");
        Assert.Contains(handler.Requests, request => request.Method == HttpMethod.Get && request.Path == $"/api/v1/profiles/{ProfileId:D}/accessibility-preferences");
        var advance = Assert.Single(handler.Requests, request => request.Path.EndsWith("/playback/failovers/22222222-2222-4222-8222-222222222222/advance", StringComparison.Ordinal));
        using var body = JsonDocument.Parse(advance.Body!);
        Assert.Equal("source_timeout", body.RootElement.GetProperty("error").GetString());
        Assert.Equal(42.5, body.RootElement.GetProperty("positionSeconds").GetDouble());
        Assert.Equal(3, body.RootElement.GetProperty("expectedRevision").GetInt64());
        Assert.Contains(handler.Requests, request => request.Method == HttpMethod.Post && request.Path == "/api/v1/saved-searches");
        Assert.Contains(handler.Requests, request => request.Method == HttpMethod.Post && request.Path == "/api/v1/smart-collections");
        Assert.Contains(handler.Requests, request => request.Method == HttpMethod.Post && request.Path == "/api/v1/media-notifications/42/acknowledgement");
        Assert.Contains(handler.Requests, request => request.Method == HttpMethod.Put && request.Path == $"/api/v1/profiles/{ProfileId:D}/accessibility-preferences");
        Assert.Contains(handler.Requests, request => request.Method == HttpMethod.Delete && request.Path == $"/api/v1/playback/failovers/{ItemId:D}");
        var smart = Assert.Single(handler.Requests, request => request.Method == HttpMethod.Post && request.Path == "/api/v1/smart-collections");
        Assert.DoesNotContain("sql", smart.Body!, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public void FailoverPolicyRestrictsAdvancesAndBoundsJournal()
    {
        Assert.True(PlaybackFailoverController.CanAdvance(PlaybackFailoverError.SourceFailed));
        Assert.True(PlaybackFailoverController.CanAdvance(PlaybackFailoverError.SourceTimeout));
        Assert.True(PlaybackFailoverController.CanAdvance(PlaybackFailoverError.EndedEarly));
        Assert.False(PlaybackFailoverController.CanAdvance(PlaybackFailoverError.DecodeFailed));
        Assert.False(PlaybackFailoverController.CanAdvance(PlaybackFailoverError.AccessDenied));
        Assert.False(PlaybackFailoverController.CanAdvance(PlaybackFailoverError.UserCancelled));

        var journal = new PlaybackFailoverJournal(2);
        var state = FailoverState(attemptCount: 1);
        journal.Record(PlaybackFailoverError.SourceFailed, state);
        journal.Record(PlaybackFailoverError.SourceTimeout, state with { AttemptCount = 2 });
        journal.Record(PlaybackFailoverError.EndedEarly, state with { AttemptCount = 3, Status = PlaybackFailoverStatus.Exhausted });
        Assert.Equal(2, journal.Entries.Count);
        Assert.Equal(PlaybackFailoverError.SourceTimeout, journal.Entries[0].Error);
        Assert.DoesNotContain("http", string.Join(' ', journal.Entries), StringComparison.OrdinalIgnoreCase);
        Assert.DoesNotContain("token", string.Join(' ', journal.Entries), StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public void WorkspaceTransitionsExposeEmptyReadyAndConflict()
    {
        var workspace = new ProtocolV22WorkspaceViewModel();
        workspace.ApplyQueue(new ReadingQueue(1, []));
        Assert.Equal(FeatureLoadState.Empty, workspace.State);

        var item = new ReadingQueueItem(ItemId, QueueMediaType.Movie, "resource", null, null, "Movie", null, 0, "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z");
        workspace.ApplyQueue(new ReadingQueue(2, [item]));
        Assert.Equal(FeatureLoadState.Ready, workspace.State);
        Assert.Equal(2, workspace.QueueRevision);

        workspace.MarkConflict("Reload before retrying.");
        Assert.Equal(FeatureLoadState.Conflict, workspace.State);
        Assert.Equal("Reload before retrying.", workspace.Failure);
    }

    [Fact]
    public async Task IneligibleFailoverNeverAdvancesOrConsumesBudget()
    {
        var handler = new FeatureHandler();
        using var client = CreateClient(handler);
        using var controller = new PlaybackFailoverController();
        controller.Restore(FailoverState(attemptCount: 1));

        var result = await controller.AdvanceAsync(client, PlaybackFailoverError.DecodeFailed, 42, TestContext.Current.CancellationToken);

        Assert.Null(result);
        Assert.Empty(handler.Requests);
        Assert.Equal(1, controller.State!.AttemptCount);
    }



    [Fact]
    public void VisualPolicyAppliesToControlsCreatedAfterPreferenceLoad()
    {
        var effective = AccessibilityPreferencesPolicy.Resolve(Preferences() with
        {
            TextScale = 130,
            FocusIndicators = FocusIndicatorsPreference.Enhanced,
        }, new TestSystemAccessibility(false, false, false));

        var policy = AccessibilityVisualPolicy.From(effective);
        var lateTextOriginalSize = 16d;

        Assert.Equal(20.8, policy.ScaleFont(lateTextOriginalSize), 3);
        Assert.Equal(3, policy.FocusPrimaryThickness);
        Assert.Equal(1, policy.FocusSecondaryThickness);
    }

    [Fact]
    public void OpenedComboBoxItemUsesProfileScaleAndEnhancedFocus()
    {
        var effective = AccessibilityPreferencesPolicy.Resolve(Preferences() with
        {
            TextScale = 130,
            FocusIndicators = FocusIndicatorsPreference.Enhanced,
        }, new TestSystemAccessibility(false, false, false));
        var popupPolicy = AccessibilityVisualPolicy.From(effective);

        Assert.Equal(18.2, popupPolicy.ScaleFont(14), 3);
        Assert.Equal(3, popupPolicy.FocusPrimaryThickness);
        Assert.Equal(1, popupPolicy.FocusSecondaryThickness);
    }

    [Fact]
    public void AccessibilityPolicyRespectsSystemAndExplicitOverrides()
    {
        var system = new TestSystemAccessibility(true, true, false);
        var inherited = AccessibilityPreferencesPolicy.Resolve(Preferences(), system);
        Assert.True(inherited.ReducedMotion);
        Assert.True(inherited.HighContrast);
        Assert.False(inherited.CaptionsEnabled);

        var explicitPreferences = Preferences() with
        {
            ReducedMotion = ReducedMotionPreference.NoPreference,
            HighContrast = HighContrastPreference.Standard,
            Captions = CaptionsPreference.On,
            TextScale = 130,
            AudioDescription = true,
            FocusIndicators = FocusIndicatorsPreference.Enhanced,
        };
        var explicitSettings = AccessibilityPreferencesPolicy.Resolve(explicitPreferences, system);
        Assert.False(explicitSettings.ReducedMotion);
        Assert.False(explicitSettings.HighContrast);
        Assert.True(explicitSettings.CaptionsEnabled);
        Assert.Equal(1.3, explicitSettings.TextScale, 3);
        Assert.True(explicitSettings.AudioDescription);
        Assert.True(explicitSettings.EnhancedFocusIndicators);
    }

    [Theory]
    [InlineData("https://provider.test/secret?token=abc")]
    [InlineData("Authorization bearer-secret")]
    public void IncidentLabelsNeverExposeUrlOrToken(string addonName)
    {
        var label = SafeIncidentPresentation.Label(Incident(addonName));
        Assert.Equal("Add-on: timed out (open)", label);
        Assert.DoesNotContain("http", label, StringComparison.OrdinalIgnoreCase);
        Assert.DoesNotContain("token", label, StringComparison.OrdinalIgnoreCase);
        Assert.DoesNotContain("secret", label, StringComparison.OrdinalIgnoreCase);
    }

    private static PlaybackFailoverState FailoverState(int attemptCount) => new(ItemId, "opaque-source-ref-0001", 0, 42, attemptCount, 3, attemptCount + 1, PlaybackFailoverStatus.Active, null, null,
        [new(0, PlaybackFailoverCandidateStatus.Current, null), new(1, PlaybackFailoverCandidateStatus.Available, null)], "2099-01-01T00:00:00Z");

    private static AccessibilityPreferencesDocument Preferences() => new(1, ReducedMotionPreference.System, HighContrastPreference.System, 100, CaptionsPreference.System, false, FocusIndicatorsPreference.Standard);

    private static AddonIncident Incident(string addonName) => new(ItemId, ProfileId, OperationId, addonName, AddonIncidentCode.Timeout, AddonIncidentState.Open, AddonIncidentImpact.Availability, 1,
        "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z", null, null, null, null, null, "2026-01-01T00:00:00Z");

    private static RivuneApiClient CreateClient(HttpMessageHandler handler) => new("https://rivune.test", handler, new FixedCredentialStore());

    private sealed record TestSystemAccessibility(bool ReducedMotion, bool HighContrast, bool CaptionsEnabled) : ISystemAccessibilitySettings;
    private sealed record CapturedRequest(HttpMethod Method, string Path, string Query, string? Body, string? ProfileContext);

    private sealed class FeatureHandler : HttpMessageHandler
    {
        public List<CapturedRequest> Requests { get; } = [];
        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            if (request.RequestUri!.AbsolutePath == "/.well-known/rivune") return Json(DiscoveryJson);
            Requests.Add(new(request.Method, request.RequestUri.AbsolutePath, request.RequestUri.Query.TrimStart('?'), request.Content is null ? null : await request.Content.ReadAsStringAsync(cancellationToken), Header(request, "X-Rivune-Profile-Context")));
            var path = request.RequestUri.AbsolutePath;
            if (request.Method == HttpMethod.Delete && path.Contains("/queue/items/", StringComparison.Ordinal)) return Json("""{"revision":8,"affectedItemId":"22222222-2222-4222-8222-222222222222"}""");
            if (path == "/api/v1/saved-searches") return request.Method == HttpMethod.Post
                ? Json("""{"id":"22222222-2222-4222-8222-222222222222","name":"Drama","query":"quiet drama","mediaType":"movie","sort":"relevance","revision":1,"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"}""")
                : Json("""{"savedSearches":[]}""");
            if (path == "/api/v1/smart-collections") return request.Method == HttpMethod.Post
                ? Json("""{"id":"22222222-2222-4222-8222-222222222222","name":"Drama","rules":{"type":"genre","operator":"equals","value":"Drama"},"sort":"title","revision":1,"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"}""")
                : Json("""{"smartCollections":[]}""");
            if (path == "/api/v1/operations/extension-incidents") return Json("""{"incidents":[]}""");
            if (path == "/api/v1/media-notifications") return Json("""{"notifications":[]}""");
            if (request.Method == HttpMethod.Post && path == "/api/v1/media-notifications/42/acknowledgement") return new HttpResponseMessage(HttpStatusCode.NoContent);
            if (path.EndsWith("/accessibility-preferences", StringComparison.Ordinal)) return Json("""{"revision":1,"reducedMotion":"system","highContrast":"system","textScale":100,"captions":"system","audioDescription":false,"focusIndicators":"standard"}""");
            if (path.EndsWith("/advance", StringComparison.Ordinal)) return Json(FailoverJson);
            if (request.Method == HttpMethod.Delete && path.EndsWith($"/playback/failovers/{ItemId:D}", StringComparison.Ordinal)) return new HttpResponseMessage(HttpStatusCode.NoContent);
            throw new InvalidOperationException($"Unexpected request {request.Method} {path}");
        }
    }

    private sealed class FixedCredentialStore : ICredentialStore
    {
        public ValueTask<StoredCredentials?> LoadAsync(CancellationToken cancellationToken = default) => ValueTask.FromResult<StoredCredentials?>(new StoredCredentials
        {
            Issuer = "https://rivune.test/",
            ProfileContext = "profile-context",
            Credentials = new TokenPair
            {
                TokenType = "Bearer", AccessToken = "access", AccessTokenExpiresAt = "2099-01-01T00:00:00Z", RefreshToken = "refresh", RefreshTokenExpiresAt = "2099-02-01T00:00:00Z",
                SessionId = Guid.NewGuid(), DeviceId = Guid.NewGuid(), AuthorizationScope = AuthorizationScope.GlobalAdministrator, Category = null,
            },
        });
        public ValueTask SaveAsync(StoredCredentials credentials, CancellationToken cancellationToken = default) => ValueTask.CompletedTask;
        public ValueTask ClearAsync(CancellationToken cancellationToken = default) => ValueTask.CompletedTask;
        public void Dispose() { }
    }

    private static string? Header(HttpRequestMessage request, string name) => request.Headers.TryGetValues(name, out var values) ? values.SingleOrDefault() : null;
    private static HttpResponseMessage Json(string json) => new(HttpStatusCode.OK) { Content = new StringContent(json, Encoding.UTF8, "application/json") };

    private const string DiscoveryJson = """{"name":"Rivune","serverVersion":"1.0.0","protocolVersion":22,"apiBaseUrl":"https://rivune.test/api/v1","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en"}""";
    private const string FailoverJson = """{"id":"22222222-2222-4222-8222-222222222222","currentSourceRef":"opaque-source-ref-0002","currentPosition":1,"positionSeconds":42.5,"attemptCount":1,"maximumAttempts":3,"revision":4,"status":"active","candidateHealth":[{"position":0,"status":"cooling_down"},{"position":1,"status":"current"}],"expiresAt":"2099-01-01T00:00:00Z"}""";
}
