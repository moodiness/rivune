using System.Net;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;
using Rivune.Windows;
using Xunit;

namespace Rivune.Windows.Tests;

public sealed class CategoryContractsTests
{
    private static readonly Guid CategoryId = Guid.Parse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa");
    private static readonly JsonSerializerOptions JsonOptions = new(JsonSerializerDefaults.Web)
    {
        DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
        UnmappedMemberHandling = JsonUnmappedMemberHandling.Skip,
    };

    private const string CategoryReferenceJson = """
        {"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Studio","color":null,"icon":"briefcase"}
        """;

    public static TheoryData<Type, string> AuthorizationRecordPayloads => new()
    {
        {
            typeof(TokenPair),
            """
            {"tokenType":"Bearer","accessToken":"access","accessTokenExpiresAt":"2026-08-03T12:15:00Z","refreshToken":"refresh","refreshTokenExpiresAt":"2026-09-03T12:00:00Z","sessionId":"22222222-2222-4222-8222-222222222222","deviceId":"33333333-3333-4333-8333-333333333333","authorizationScope":"SCOPE_PLACEHOLDER","category":"CATEGORY_PLACEHOLDER"}
            """
        },
        {
            typeof(AccountSession),
            """
            {"id":"22222222-2222-4222-8222-222222222222","deviceId":"33333333-3333-4333-8333-333333333333","activeProfile":null,"authorizationScope":"SCOPE_PLACEHOLDER","category":"CATEGORY_PLACEHOLDER"}
            """
        },
        {
            typeof(Session),
            """
            {"id":"22222222-2222-4222-8222-222222222222","deviceId":"33333333-3333-4333-8333-333333333333","deviceName":"Editing PC","platform":"windows","ipAddress":null,"createdAt":"2026-08-03T10:00:00Z","lastSeenAt":"2026-08-03T11:00:00Z","current":true,"authorizationScope":"SCOPE_PLACEHOLDER","category":"CATEGORY_PLACEHOLDER"}
            """
        },
        {
            typeof(ProfileSession),
            """
            {"id":"22222222-2222-4222-8222-222222222222","userId":"11111111-1111-4111-8111-111111111111","username":"admin","deviceId":"33333333-3333-4333-8333-333333333333","deviceName":"Editing PC","platform":"windows","ipAddress":null,"createdAt":"2026-08-03T10:00:00Z","lastSeenAt":"2026-08-03T11:00:00Z","profileGrantExpiresAt":"2026-08-03T12:00:00Z","current":true,"authorizationScope":"SCOPE_PLACEHOLDER","category":"CATEGORY_PLACEHOLDER"}
            """
        },
    };

    [Fact]
    public void RequiredCategoryAndScopeFieldsDecode()
    {
        const string accountJson = """
        {
          "user":{"id":"11111111-1111-4111-8111-111111111111","username":"admin","role":"admin"},
          "session":{"id":"22222222-2222-4222-8222-222222222222","deviceId":"33333333-3333-4333-8333-333333333333","activeProfile":null,"authorizationScope":"category","category":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Studio","color":null,"icon":"briefcase"}},
          "profiles":[{"id":"44444444-4444-4444-8444-444444444444","name":"Editor","description":"Editing profile","categoryId":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","category":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Studio","color":null,"icon":"briefcase"},"isChild":false,"hasPin":false,"canManage":true,"enabled":true,"availableFrom":null,"availableUntil":null,"accessStartTime":null,"accessEndTime":null,"accessTimezone":"UTC","accessible":true,"avatar":{"kind":"preset","presetId":"aurora","url":"/avatar.svg"}}],
          "maintenance":{"enabled":true,"message":null}
        }
        """;
        var account = JsonSerializer.Deserialize<Account>(accountJson, JsonOptions)!;

        Assert.Equal(AuthorizationScope.Category, account.Session.AuthorizationScope);
        Assert.Equal(CategoryId, account.Session.Category?.Id);
        Assert.Equal("Studio", account.Profiles[0].Category.Name);
        Assert.Equal("Editing profile", account.Profiles[0].Description);
        Assert.True(account.Maintenance.Enabled);
        Assert.Null(account.Maintenance.Message);

        const string tokenJson = """
        {"tokenType":"Bearer","accessToken":"access","accessTokenExpiresAt":"2026-08-03T12:15:00Z","refreshToken":"refresh","refreshTokenExpiresAt":"2026-09-03T12:00:00Z","sessionId":"22222222-2222-4222-8222-222222222222","deviceId":"33333333-3333-4333-8333-333333333333","authorizationScope":"global_admin","category":null}
        """;
        var token = JsonSerializer.Deserialize<TokenPair>(tokenJson, JsonOptions)!;
        Assert.Equal(AuthorizationScope.GlobalAdministrator, token.AuthorizationScope);
        Assert.Null(token.Category);

        const string sessionJson = """
        {"id":"22222222-2222-4222-8222-222222222222","deviceId":"33333333-3333-4333-8333-333333333333","deviceName":"Editing PC","platform":"windows","ipAddress":null,"createdAt":"2026-08-03T10:00:00Z","lastSeenAt":"2026-08-03T11:00:00Z","current":true,"authorizationScope":"category","category":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Studio","color":null,"icon":"briefcase"}}
        """;
        var session = JsonSerializer.Deserialize<Session>(sessionJson, JsonOptions)!;
        Assert.Equal(AuthorizationScope.Category, session.AuthorizationScope);
        Assert.Equal(CategoryId, session.Category?.Id);
    }

    [Theory]
    [MemberData(nameof(AuthorizationRecordPayloads))]
    public void AuthorizationContextsRejectGlobalAdministratorWithCategory(Type recordType, string payload)
    {
        var json = WithAuthorizationContext(payload, "global_admin", CategoryReferenceJson);

        Assert.Throws<JsonException>(() => DeserializeAuthorizationContext(recordType, json));
    }

    [Theory]
    [MemberData(nameof(AuthorizationRecordPayloads))]
    public void AuthorizationContextsRejectCategoryScopeWithoutCategory(Type recordType, string payload)
    {
        var json = WithAuthorizationContext(payload, "category", "null");

        Assert.Throws<JsonException>(() => DeserializeAuthorizationContext(recordType, json));
    }

    [Theory]
    [MemberData(nameof(AuthorizationRecordPayloads))]
    public void AuthorizationContextsAcceptValidGlobalAndCategoryPayloads(Type recordType, string payload)
    {
        var globalAdministrator = DeserializeAuthorizationContext(
            recordType,
            WithAuthorizationContext(payload, "global_admin", "null"));
        var category = DeserializeAuthorizationContext(
            recordType,
            WithAuthorizationContext(payload, "category", CategoryReferenceJson));

        AssertAuthorizationContext(globalAdministrator, AuthorizationScope.GlobalAdministrator, null);
        AssertAuthorizationContext(category, AuthorizationScope.Category, CategoryId);
    }

    [Fact]
    public void CategoryAndDeviceContractsDecode()
    {
        const string categoryJson = """
        {"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Studio","description":"Production","color":"#123ABC","icon":"briefcase","position":2,"isDefault":false,"profileCount":3,"deviceCount":4,"createdAt":"2026-08-03T10:00:00Z","updatedAt":"2026-08-03T11:00:00Z"}
        """;
        var category = JsonSerializer.Deserialize<Category>(categoryJson, JsonOptions)!;
        Assert.Equal(4, category.DeviceCount);

        const string deviceJson = """
        {"id":"33333333-3333-4333-8333-333333333333","name":"Editing PC","platform":"windows","categoryId":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","category":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Studio","color":"#123ABC","icon":"briefcase"},"internalNote":null,"approvedAt":"2026-08-03T10:00:00Z","lastSeenAt":null,"createdAt":"2026-08-03T10:00:00Z","updatedAt":"2026-08-03T11:00:00Z"}
        """;
        var device = JsonSerializer.Deserialize<Device>(deviceJson, JsonOptions)!;
        Assert.Equal(CategoryId, device.CategoryId);
        Assert.Null(device.InternalNote);
    }

    [Fact]
    public void ApprovalRequestCarriesCategoryAsResourceIdentifier()
    {
        var approval = new DeviceCodeApprovalRequest
        {
            UserCode = "ABCD-EFGH",
            CategoryId = CategoryId,
            DeviceName = "Living Room",
            InternalNote = "Wall display",
        };
        using var encoded = JsonDocument.Parse(JsonSerializer.Serialize(approval, JsonOptions));
        Assert.Equal("ABCD-EFGH", encoded.RootElement.GetProperty("userCode").GetString());
        Assert.Equal(CategoryId, encoded.RootElement.GetProperty("categoryId").GetGuid());
        Assert.Equal("Living Room", encoded.RootElement.GetProperty("deviceName").GetString());
    }

    [Fact]
    public async Task UpdateCategoryClientUsesPatchRouteAndExactBody()
    {
        var handler = new RecordingHandler();
        using var client = new RivuneApiClient(
            new Uri("https://rivune.test"),
            handler,
            new StubCredentialStore(FixtureToken()));

        var category = await client.UpdateCategoryAsync(
            CategoryId,
            new CategoryUpdateRequest
            {
                Description = PatchField<string>.Null,
                Icon = PatchField<string>.FromValue("briefcase"),
                IsDefault = true,
            },
            TestContext.Current.CancellationToken);
        Assert.Equal(CategoryId, category.Id);

        var request = Assert.Single(handler.ApiRequests);
        Assert.Equal(HttpMethod.Patch, request.Method);
        Assert.Equal("/api/v1/categories/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", request.Path);
        using var body = JsonDocument.Parse(request.Body!);
        Assert.Equal(JsonValueKind.Null, body.RootElement.GetProperty("description").ValueKind);
        Assert.Equal("briefcase", body.RootElement.GetProperty("icon").GetString());
        Assert.True(body.RootElement.GetProperty("isDefault").GetBoolean());
        Assert.False(body.RootElement.TryGetProperty("name", out _));
    }

    private static string WithAuthorizationContext(string payload, string scope, string category) =>
        payload
            .Replace("SCOPE_PLACEHOLDER", scope, StringComparison.Ordinal)
            .Replace("\"CATEGORY_PLACEHOLDER\"", category, StringComparison.Ordinal);

    private static object DeserializeAuthorizationContext(Type recordType, string json) =>
        JsonSerializer.Deserialize(json, recordType, JsonOptions)!;

    private static void AssertAuthorizationContext(
        object value,
        AuthorizationScope expectedScope,
        Guid? expectedCategoryId)
    {
        var (scope, category) = value switch
        {
            TokenPair token => (token.AuthorizationScope, token.Category),
            AccountSession accountSession => (accountSession.AuthorizationScope, accountSession.Category),
            Session session => (session.AuthorizationScope, session.Category),
            ProfileSession profileSession => (profileSession.AuthorizationScope, profileSession.Category),
            _ => throw new ArgumentOutOfRangeException(nameof(value)),
        };

        Assert.Equal(expectedScope, scope);
        Assert.Equal(expectedCategoryId, category?.Id);
    }

    private static TokenPair FixtureToken() => new()
    {
        TokenType = "Bearer",
        AccessToken = "access",
        AccessTokenExpiresAt = "2026-08-03T12:15:00Z",
        RefreshToken = "refresh",
        RefreshTokenExpiresAt = "2026-09-03T12:00:00Z",
        SessionId = Guid.Parse("22222222-2222-4222-8222-222222222222"),
        DeviceId = Guid.Parse("33333333-3333-4333-8333-333333333333"),
        AuthorizationScope = AuthorizationScope.GlobalAdministrator,
        Category = null,
    };

    private sealed class StubCredentialStore(TokenPair token) : ICredentialStore
    {
        public ValueTask<StoredCredentials?> LoadAsync(CancellationToken cancellationToken = default) =>
            ValueTask.FromResult<StoredCredentials?>(new StoredCredentials
            {
                Issuer = "https://rivune.test/",
                Credentials = token,
            });

        public ValueTask SaveAsync(StoredCredentials credentials, CancellationToken cancellationToken = default) =>
            ValueTask.CompletedTask;

        public ValueTask ClearAsync(CancellationToken cancellationToken = default) => ValueTask.CompletedTask;
    }

    private sealed class RecordingHandler : HttpMessageHandler
    {
        public List<(HttpMethod Method, string Path, string? Body)> ApiRequests { get; } = [];

        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            string body;
            if (request.RequestUri!.AbsolutePath == "/.well-known/rivune")
            {
                body = """{"name":"Rivune","serverVersion":"test","protocolVersion":20,"apiBaseUrl":"/api/v1","setupRequired":false,"setupCompleted":true,"demoAvailable":false,"timezone":"UTC","interfaceLanguage":"en"}""";
            }
            else
            {
                var requestBody = request.Content is null ? null : await request.Content.ReadAsStringAsync(cancellationToken);
                ApiRequests.Add((request.Method, request.RequestUri.AbsolutePath, requestBody));
                body = """{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Studio","description":null,"color":null,"icon":"briefcase","position":0,"isDefault":false,"profileCount":0,"deviceCount":0,"createdAt":"2026-08-03T10:00:00Z","updatedAt":"2026-08-03T11:00:00Z"}""";
            }

            return new HttpResponseMessage(HttpStatusCode.OK)
            {
                Content = new StringContent(body, Encoding.UTF8, "application/json"),
            };
        }
    }
}
