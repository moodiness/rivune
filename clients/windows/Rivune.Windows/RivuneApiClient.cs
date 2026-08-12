using System.Buffers;
using System.Runtime.ExceptionServices;
using System.Net;
using System.Net.Http.Headers;
using System.Security.Cryptography;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace Rivune.Windows;

public sealed class RivuneApiClient : IDisposable
{
    private static readonly JsonSerializerOptions JsonOptions = new(JsonSerializerDefaults.Web)
    {
        DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
        UnmappedMemberHandling = JsonUnmappedMemberHandling.Skip,
    };

    private const int MaximumResponseBodyBytes = 16 * 1024 * 1024;

    private sealed record DiscoveryEnvelope
    {
        public required string Name { get; init; }
        public required string ServerVersion { get; init; }
        public required int ProtocolVersion { get; init; }
        public required string ApiBaseUrl { get; init; }
        public required bool SetupRequired { get; init; }
        public bool? SetupCompleted { get; init; }
        public bool? DemoAvailable { get; init; }
        public required string Timezone { get; init; }
        public string? InterfaceLanguage { get; init; }
    }
    private readonly record struct HttpResponsePayload(HttpStatusCode StatusCode, byte[] Body);

    private readonly record struct AuthenticationSnapshot(TokenPair Credentials, long Epoch);

    private readonly Uri _serverUrl;
    private readonly string _credentialIssuer;
    private readonly HttpClient _httpClient;
    private readonly ICredentialStore _credentialStore;
    private readonly bool _ownsCredentialStore;
    private readonly SemaphoreSlim _discoveryGate = new(1, 1);
    private readonly SemaphoreSlim _credentialGate = new(1, 1);
    private readonly SemaphoreSlim _refreshGate = new(1, 1);

    private Uri? _apiBaseUrl;
    private Discovery? _discovery;
    private TokenPair? _credentials;
    private bool _credentialsLoaded;
    private long _authenticationEpoch;
    private CancellationTokenSource? _refreshCancellationSource;
    private bool _disposed;

    public RivuneApiClient(string serverUrl, ICredentialStore credentialStore)
        : this(
            ParseServerUrl(serverUrl),
            CreateHttpClient(CreateDefaultHandler()),
            credentialStore,
            ownsCredentialStore: false)
    {
    }

    public RivuneApiClient(Uri serverUrl, ICredentialStore credentialStore)
        : this(
            ValidateServerUrl(serverUrl),
            CreateHttpClient(CreateDefaultHandler()),
            credentialStore,
            ownsCredentialStore: false)
    {
    }

    public RivuneApiClient(
        string serverUrl,
        HttpMessageHandler handler,
        ICredentialStore credentialStore)
        : this(
            ParseServerUrl(serverUrl),
            CreateHttpClient(handler),
            credentialStore,
            ownsCredentialStore: false)
    {
    }

    public RivuneApiClient(
        Uri serverUrl,
        HttpMessageHandler handler,
        ICredentialStore credentialStore)
        : this(
            ValidateServerUrl(serverUrl),
            CreateHttpClient(handler),
            credentialStore,
            ownsCredentialStore: false)
    {
    }

    public RivuneApiClient(string serverUrl)
        : this(
            ParseServerUrl(serverUrl),
            CreateHttpClient(CreateDefaultHandler()),
            credentialStore: null,
            ownsCredentialStore: true)
    {
    }

    public RivuneApiClient(Uri serverUrl)
        : this(
            ValidateServerUrl(serverUrl),
            CreateHttpClient(CreateDefaultHandler()),
            credentialStore: null,
            ownsCredentialStore: true)
    {
    }

    public RivuneApiClient(string serverUrl, HttpMessageHandler handler)
        : this(
            ParseServerUrl(serverUrl),
            CreateHttpClient(handler),
            credentialStore: null,
            ownsCredentialStore: true)
    {
    }

    public RivuneApiClient(Uri serverUrl, HttpMessageHandler handler)
        : this(
            ValidateServerUrl(serverUrl),
            CreateHttpClient(handler),
            credentialStore: null,
            ownsCredentialStore: true)
    {
    }

    private RivuneApiClient(
        Uri serverUrl,
        HttpClient httpClient,
        ICredentialStore? credentialStore,
        bool ownsCredentialStore)
    {
        _serverUrl = serverUrl;
        _credentialIssuer = CredentialIssuer.Canonicalize(serverUrl);
        _httpClient = httpClient;
        try
        {
            _credentialStore = ownsCredentialStore
                ? new DpapiCredentialStore(serverUrl)
                : credentialStore ?? throw new ArgumentNullException(nameof(credentialStore));
        }
        catch
        {
            _httpClient.Dispose();
            throw;
        }
        _ownsCredentialStore = ownsCredentialStore;
    }

    public Task<Discovery> DiscoverAsync(CancellationToken cancellationToken = default) =>
        DiscoverCoreAsync(force: true, cancellationToken);

    public async Task<bool> RestoreSessionAsync(CancellationToken cancellationToken = default)
    {
        ThrowIfDisposed();
        var epoch = await CaptureAuthenticationEpochAsync(cancellationToken).ConfigureAwait(false);
        await _credentialGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            if (epoch != _authenticationEpoch)
            {
                return false;
            }

            _credentials = await LoadCredentialsFromStoreAsync(cancellationToken).ConfigureAwait(false);
            _credentialsLoaded = true;
            return _credentials is not null;
        }
        finally
        {
            _credentialGate.Release();
        }
    }

    public async Task<TokenPair> LoginAsync(
        string username,
        string password,
        LoginDevice device,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(username);
        ArgumentNullException.ThrowIfNull(password);
        ArgumentNullException.ThrowIfNull(device);

        var epoch = await CaptureAuthenticationEpochAsync(cancellationToken).ConfigureAwait(false);

        var result = await RequestJsonAsync<TokenPair>(
            HttpMethod.Post,
            ["auth", "login"],
            query: null,
            new LoginRequest(username, password, device),
            authenticated: false,
            cancellationToken).ConfigureAwait(false);
        await SetCredentialsAsync(result, epoch, cancellationToken).ConfigureAwait(false);
        return result;
    }

    public async Task<TokenPair> RefreshSessionAsync(CancellationToken cancellationToken = default)
    {
        ThrowIfDisposed();
        var current = await GetAuthenticationSnapshotAsync(cancellationToken).ConfigureAwait(false)
            ?? throw new NotAuthenticatedException();
        return await RefreshCredentialsAsync(
            current.Credentials.AccessToken,
            current.Epoch,
            cancellationToken).ConfigureAwait(false);
    }

    public async Task LogoutAsync(CancellationToken cancellationToken = default)
    {
        ThrowIfDisposed();

        string? accessToken = null;
        Exception? localFailure = null;
        await _credentialGate.WaitAsync(CancellationToken.None).ConfigureAwait(false);
        try
        {
            unchecked
            {
                _authenticationEpoch++;
            }
            try
            {
                _refreshCancellationSource?.Cancel();
            }
            catch (Exception exception)
            {
                localFailure = exception;
            }
            _refreshCancellationSource = null;

            if (_credentialsLoaded)
            {
                accessToken = _credentials?.AccessToken;
            }
            else
            {
                try
                {
                    accessToken = (await LoadCredentialsFromStoreAsync(CancellationToken.None).ConfigureAwait(false))
                        ?.AccessToken;
                }
                catch (Exception exception)
                {
                    localFailure = exception;
                }
            }

            try
            {
                await _credentialStore.ClearAsync(CancellationToken.None).ConfigureAwait(false);
            }
            catch (Exception exception)
            {
                localFailure ??= exception;
            }
            finally
            {
                _credentials = null;
                _credentialsLoaded = true;
            }
        }
        finally
        {
            _credentialGate.Release();
        }

        Exception? remoteFailure = null;
        if (accessToken is not null)
        {
            try
            {
                var uri = await BuildEndpointAsync(
                    ["auth", "logout"],
                    query: null,
                    cancellationToken).ConfigureAwait(false);
                EnsureCredentialDestination(uri);
                var response = await SendResponseWithAccessTokenAsync(
                    HttpMethod.Post,
                    uri,
                    body: null,
                    accessToken,
                    cancellationToken).ConfigureAwait(false);
                CryptographicOperations.ZeroMemory(response.Body);
            }
            catch (Exception exception)
            {
                remoteFailure = exception;
            }
        }

        if (localFailure is not null)
        {
            ExceptionDispatchInfo.Capture(localFailure).Throw();
        }
        if (remoteFailure is not null)
        {
            ExceptionDispatchInfo.Capture(remoteFailure).Throw();
        }
    }

    public Task<Account> GetCurrentAccountAsync(CancellationToken cancellationToken = default) =>
        RequestJsonAsync<Account>(HttpMethod.Get, ["auth", "me"], null, null, true, cancellationToken);

    public async Task<IReadOnlyList<Session>> GetSessionsAsync(CancellationToken cancellationToken = default) =>
        (await RequestJsonAsync<SessionList>(
            HttpMethod.Get, ["auth", "sessions"], null, null, true, cancellationToken).ConfigureAwait(false)).Sessions;

    public async Task<IReadOnlyList<Category>> GetCategoriesAsync(CancellationToken cancellationToken = default) =>
        (await RequestJsonAsync<CategoryList>(
            HttpMethod.Get, ["categories"], null, null, true, cancellationToken).ConfigureAwait(false)).Categories;

    public Task<Category> CreateCategoryAsync(
        CategoryCreateRequest input,
        CancellationToken cancellationToken = default) =>
        RequestJsonAsync<Category>(
            HttpMethod.Post, ["categories"], null, input, true, cancellationToken);

    public Task<Category> UpdateCategoryAsync(
        Guid categoryId,
        CategoryUpdateRequest input,
        CancellationToken cancellationToken = default) =>
        RequestJsonAsync<Category>(
            HttpMethod.Patch,
            ["categories", categoryId.ToString("D")],
            null,
            CategoryUpdateBody(input),
            true,
            cancellationToken);

    public Task DeleteCategoryAsync(
        Guid categoryId,
        Guid? reassignToCategoryId = null,
        CancellationToken cancellationToken = default) =>
        RequestEmptyWithBodyAsync(
            HttpMethod.Delete,
            ["categories", categoryId.ToString("D")],
            new Dictionary<string, object?> { ["reassignToCategoryId"] = reassignToCategoryId },
            true,
            cancellationToken);

    public async Task<IReadOnlyList<Category>> ReorderCategoriesAsync(
        IReadOnlyList<Guid> categoryIds,
        CancellationToken cancellationToken = default) =>
        (await RequestJsonAsync<CategoryList>(
            HttpMethod.Put,
            ["categories", "order"],
            null,
            new CategoryOrderRequest { CategoryIds = categoryIds },
            true,
            cancellationToken).ConfigureAwait(false)).Categories;

    public async Task<IReadOnlyList<Device>> GetDevicesAsync(
        Guid? categoryId = null,
        CancellationToken cancellationToken = default) =>
        (await RequestJsonAsync<DeviceList>(
            HttpMethod.Get,
            ["devices"],
            Query(("categoryId", categoryId?.ToString("D"))),
            null,
            true,
            cancellationToken).ConfigureAwait(false)).Devices;

    public Task<Device> UpdateDeviceAsync(
        Guid deviceId,
        DeviceUpdateRequest input,
        CancellationToken cancellationToken = default) =>
        RequestJsonAsync<Device>(
            HttpMethod.Patch,
            ["devices", deviceId.ToString("D")],
            null,
            DeviceUpdateBody(input),
            true,
            cancellationToken);

    public Task MoveProfilesAsync(
        IReadOnlyList<Guid> profileIds,
        Guid categoryId,
        CancellationToken cancellationToken = default) =>
        RequestEmptyWithBodyAsync(
            HttpMethod.Post,
            ["profiles", "category-moves"],
            new ProfileCategoryMoveRequest { ProfileIds = profileIds, CategoryId = categoryId },
            true,
            cancellationToken);

    public Task MoveDevicesAsync(
        IReadOnlyList<Guid> deviceIds,
        Guid categoryId,
        CancellationToken cancellationToken = default) =>
        RequestEmptyWithBodyAsync(
            HttpMethod.Post,
            ["devices", "category-moves"],
            new DeviceCategoryMoveRequest { DeviceIds = deviceIds, CategoryId = categoryId },
            true,
            cancellationToken);

    public Task<DeviceAuthorizationResponse> BeginDeviceAuthorizationAsync(
        string deviceName,
        string platform,
        CancellationToken cancellationToken = default) =>
        RequestJsonAsync<DeviceAuthorizationResponse>(
            HttpMethod.Post,
            ["auth", "device-code"],
            null,
            new DeviceAuthorizationRequest { DeviceName = deviceName, Platform = platform },
            false,
            cancellationToken);

    public async Task<TokenPair> ExchangeDeviceAuthorizationAsync(
        string deviceCode,
        CancellationToken cancellationToken = default)
    {
        var epoch = await CaptureAuthenticationEpochAsync(cancellationToken).ConfigureAwait(false);
        var tokens = await RequestJsonAsync<TokenPair>(
            HttpMethod.Post,
            ["auth", "device-code", "token"],
            null,
            new DeviceCodeTokenRequest { DeviceCode = deviceCode },
            false,
            cancellationToken).ConfigureAwait(false);
        await SetCredentialsAsync(tokens, epoch, cancellationToken).ConfigureAwait(false);
        return tokens;
    }

    public Task ApproveDeviceAuthorizationAsync(
        DeviceCodeApprovalRequest input,
        CancellationToken cancellationToken = default) =>
        RequestEmptyWithBodyAsync(
            HttpMethod.Post,
            ["auth", "device-code", "approve"],
            input,
            true,
            cancellationToken);

    public async Task<IReadOnlyList<Profile>> GetProfilesAsync(CancellationToken cancellationToken = default) =>
        (await RequestJsonAsync<ProfileList>(
            HttpMethod.Get,
            ["profiles"],
            null,
            null,
            true,
            cancellationToken).ConfigureAwait(false)).Profiles;

    public Task<ProfileSelection> SelectProfileAsync(
        Guid profileId,
        string? pin = null,
        CancellationToken cancellationToken = default) =>
        RequestJsonAsync<ProfileSelection>(
            HttpMethod.Post,
            ["profiles", profileId.ToString("D"), "select"],
            null,
            new SelectProfileRequest(pin),
            true,
            cancellationToken);

    public Task ClearProfileSelectionAsync(CancellationToken cancellationToken = default) =>
        RequestEmptyAsync(HttpMethod.Delete, ["profiles", "selection"], true, cancellationToken);

    public Task<SettingsLayer> GetInstanceSettingsAsync(CancellationToken cancellationToken = default) =>
        RequestJsonAsync<SettingsLayer>(HttpMethod.Get, ["settings"], null, null, true, cancellationToken);

    public Task<SettingsLayer> UpdateInstanceSettingsAsync(
        bool? allowTranscoding,
        CancellationToken cancellationToken = default) =>
        RequestJsonAsync<SettingsLayer>(
            HttpMethod.Patch,
            ["settings"],
            null,
            new InstanceTranscodingPatch(allowTranscoding),
            true,
            cancellationToken);

    public Task<SettingsLayer> GetProfileSettingsAsync(
        Guid profileId,
        CancellationToken cancellationToken = default) =>
        RequestJsonAsync<SettingsLayer>(
            HttpMethod.Get,
            ["profiles", profileId.ToString("D"), "settings"],
            null,
            null,
            true,
            cancellationToken);

    public Task<SettingsLayer> UpdateProfileSettingsAsync(
        Guid profileId,
        string? transcoding,
        CancellationToken cancellationToken = default) =>
        RequestJsonAsync<SettingsLayer>(
            HttpMethod.Patch,
            ["profiles", profileId.ToString("D"), "settings"],
            null,
            new ProfileTranscodingPatch(transcoding),
            true,
            cancellationToken);

    public Task<EffectiveSettings> GetEffectiveProfileSettingsAsync(
        Guid profileId,
        CancellationToken cancellationToken = default) =>
        RequestJsonAsync<EffectiveSettings>(
            HttpMethod.Get,
            ["profiles", profileId.ToString("D"), "settings", "effective"],
            null,
            null,
            true,
            cancellationToken);

    public Task<Movie> GetMovieAsync(
        Guid id,
        string? language = null,
        CancellationToken cancellationToken = default) =>
        RequestJsonAsync<Movie>(
            HttpMethod.Get,
            ["metadata", "titles", id.ToString("D")],
            Query(("language", language)),
            null,
            true,
            cancellationToken);

    public Task<Series> GetSeriesAsync(
        Guid id,
        SeriesMappingProvider mappingProvider,
        string? language = null,
        string? episodeOrder = null,
        CancellationToken cancellationToken = default) =>
        RequestJsonAsync<Series>(
            HttpMethod.Get,
            ["metadata", "series", id.ToString("D")],
            Query(
                ("language", language),
                ("mappingProvider", MappingProviderValue(mappingProvider)),
                ("episodeOrder", episodeOrder)),
            null,
            true,
            cancellationToken);

    public Task<Season> GetSeasonAsync(
        string id,
        SeriesMappingProvider mappingProvider,
        string? language = null,
        CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrEmpty(id);
        return RequestJsonAsync<Season>(
            HttpMethod.Get,
            ["metadata", "seasons", id],
            Query(
                ("language", language),
                ("mappingProvider", MappingProviderValue(mappingProvider))),
            null,
            true,
            cancellationToken);
    }

    public Task<TrailerList> GetTrailersAsync(
        Guid titleId,
        string? language = null,
        string? captionLanguage = null,
        int? seasonNumber = null,
        CancellationToken cancellationToken = default) =>
        RequestJsonAsync<TrailerList>(
            HttpMethod.Get,
            ["metadata", "titles", titleId.ToString("D"), "trailers"],
            Query(
                ("language", language),
                ("captionLanguage", captionLanguage),
                ("seasonNumber", seasonNumber?.ToString(System.Globalization.CultureInfo.InvariantCulture))),
            null,
            true,
            cancellationToken);
    public Task<PlaybackMarkerList> GetPlaybackMarkersAsync(
        string imdbId,
        int season,
        int episode,
        CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrEmpty(imdbId);
        return RequestJsonAsync<PlaybackMarkerList>(
            HttpMethod.Get,
            ["playback", "markers"],
            Query(
                ("imdbId", imdbId),
                ("season", season.ToString(System.Globalization.CultureInfo.InvariantCulture)),
                ("episode", episode.ToString(System.Globalization.CultureInfo.InvariantCulture))),
            null,
            true,
            cancellationToken);
    }


    public Task<PlaybackSourceList> GetPlaybackSourcesAsync(
        string mediaType,
        string resourceId,
        PlaybackCapabilities capabilities,
        Guid? addonId = null,
        CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrEmpty(resourceId);
        ArgumentException.ThrowIfNullOrEmpty(mediaType);
        ArgumentNullException.ThrowIfNull(capabilities);
        return RequestJsonAsync<PlaybackSourceList>(
            HttpMethod.Post,
            ["playback", "sources"],
            null,
            new PlaybackSourcesRequest(mediaType, addonId, resourceId, capabilities),
            true,
            cancellationToken);
    }

    public Task<PlaybackPreparation> PreparePlaybackAsync(
        string sourceRef,
        int? startSeconds = null,
        CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrEmpty(sourceRef);
        return RequestJsonAsync<PlaybackPreparation>(
            HttpMethod.Post,
            ["playback", "prepare"],
            null,
            new PlaybackPrepareRequest(sourceRef, startSeconds),
            true,
            cancellationToken);
    }

    public Task<PlaybackSession> ResolvePlaybackAsync(
        string sourceRef,
        string? titleId = null,
        int? preferredAudioTrack = null,
        string? preferredSubtitleId = null,
        int? startSeconds = null,
        CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrEmpty(sourceRef);
        return RequestJsonAsync<PlaybackSession>(
            HttpMethod.Post,
            ["playback", "resolve"],
            null,
            new PlaybackResolveRequest(sourceRef, titleId, preferredAudioTrack, preferredSubtitleId, startSeconds),
            true,
            cancellationToken);
    }

    public Task StopPlaybackAsync(Guid sessionId, CancellationToken cancellationToken = default) =>
        RequestEmptyAsync(
            HttpMethod.Delete,
            ["playback", "sessions", sessionId.ToString("D")],
            true,
            cancellationToken);

    public Task<PlaybackActivity> GetPlaybackActivityAsync(CancellationToken cancellationToken = default) =>
        RequestJsonAsync<PlaybackActivity>(
            HttpMethod.Get,
            ["playback", "activity"],
            null,
            null,
            true,
            cancellationToken);
    public Task<PlaybackProgress?> GetPlaybackProgressAsync(
        Guid titleId,
        CancellationToken cancellationToken = default) =>
        RequestOptionalJsonAsync<PlaybackProgress>(
            HttpMethod.Get,
            ["progress", titleId.ToString("D")],
            null,
            null,
            true,
            cancellationToken);

    public Task<PlaybackProgressBatch> GetPlaybackProgressBatchAsync(
        IReadOnlyList<Guid> titleIds,
        CancellationToken cancellationToken = default) =>
        RequestJsonAsync<PlaybackProgressBatch>(
            HttpMethod.Post,
            ["progress", "batch"],
            null,
            new PlaybackProgressBatchRequest { TitleIds = titleIds },
            true,
            cancellationToken);

    public Task<PlaybackProgress> UpdatePlaybackProgressAsync(
        Guid titleId,
        UpdatePlaybackProgressRequest input,
        CancellationToken cancellationToken = default) =>
        RequestJsonAsync<PlaybackProgress>(
            HttpMethod.Put,
            ["progress", titleId.ToString("D")],
            null,
            input,
            true,
            cancellationToken);

    public Task ClearPlaybackProgressAsync(
        Guid titleId,
        long expectedVersion,
        CancellationToken cancellationToken = default) =>
        RequestEmptyAsync(
            HttpMethod.Delete,
            ["progress", titleId.ToString("D")],
            Query(("expectedVersion", expectedVersion.ToString(System.Globalization.CultureInfo.InvariantCulture))),
            true,
            cancellationToken);

    public Task<SetWatchedBatchResult> SetTitlesWatchedBatchAsync(
        SetWatchedBatchRequest input,
        CancellationToken cancellationToken = default) =>
        RequestJsonAsync<SetWatchedBatchResult>(
            HttpMethod.Put,
            ["titles", "watched", "batch"],
            null,
            input,
            true,
            cancellationToken);

    public Task<PlaybackProgress> MarkTitleWatchedAsync(
        Guid titleId,
        long expectedVersion,
        CancellationToken cancellationToken = default) =>
        RequestJsonAsync<PlaybackProgress>(
            HttpMethod.Post,
            ["titles", titleId.ToString("D"), "watched"],
            null,
            new CompletionRequest { ExpectedVersion = expectedVersion },
            true,
            cancellationToken);

    public Task<PlaybackProgress> MarkTitleUnwatchedAsync(
        Guid titleId,
        long expectedVersion,
        CancellationToken cancellationToken = default) =>
        RequestJsonAsync<PlaybackProgress>(
            HttpMethod.Delete,
            ["titles", titleId.ToString("D"), "watched"],
            Query(("expectedVersion", expectedVersion.ToString(System.Globalization.CultureInfo.InvariantCulture))),
            null,
            true,
            cancellationToken);

    public Task<ContinueWatchingPage> GetContinueWatchingAsync(
        int? limit = null,
        CancellationToken cancellationToken = default) =>
        RequestJsonAsync<ContinueWatchingPage>(
            HttpMethod.Get,
            ["continue-watching"],
            Query(("limit", limit?.ToString(System.Globalization.CultureInfo.InvariantCulture))),
            null,
            true,
            cancellationToken);
    public Task DismissContinueWatchingTitleAsync(
        Guid titleId,
        CancellationToken cancellationToken = default) =>
        RequestEmptyAsync(
            HttpMethod.Delete,
            ["continue-watching", titleId.ToString("D")],
            true,
            cancellationToken);



    public void Dispose()
    {
        if (_disposed)
        {
            return;
        }

        _disposed = true;
        _httpClient.Dispose();
        _discoveryGate.Dispose();
        _credentialGate.Dispose();
        _refreshGate.Dispose();
        if (_ownsCredentialStore && _credentialStore is IDisposable disposableStore)
        {
            disposableStore.Dispose();
        }
    }

    private async Task<Discovery> DiscoverCoreAsync(bool force, CancellationToken cancellationToken)
    {
        ThrowIfDisposed();
        await _discoveryGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            if (!force && _discovery is not null)
            {
                return _discovery;
            }

            var discoveryUrl = new Uri(_serverUrl, "/.well-known/rivune");
            var response = await SendJsonResponseAsync<DiscoveryEnvelope>(
                HttpMethod.Get,
                discoveryUrl,
                body: null,
                authenticated: false,
                retryAfterRefresh: false,
                cancellationToken).ConfigureAwait(false);

            if (response.ProtocolVersion != RivuneProtocol.Version)
            {
                _apiBaseUrl = null;
                _discovery = null;
                throw new IncompatibleProtocolException(RivuneProtocol.Version, response.ProtocolVersion);
            }
            if (response.InterfaceLanguage is null)
            {
                _apiBaseUrl = null;
                _discovery = null;
                throw new InvalidResponseException();
            }

            var discovery = new Discovery
            {
                Name = response.Name,
                ServerVersion = response.ServerVersion,
                ProtocolVersion = response.ProtocolVersion,
                ApiBaseUrl = response.ApiBaseUrl,
                SetupRequired = response.SetupRequired,
                SetupCompleted = response.SetupCompleted,
                DemoAvailable = response.DemoAvailable,
                Timezone = response.Timezone,
                InterfaceLanguage = response.InterfaceLanguage,
            };
            if (!Uri.TryCreate(_serverUrl, discovery.ApiBaseUrl, out var apiBaseUrl) ||
                !IsAllowedServerUrl(apiBaseUrl) ||
                !StringComparer.Ordinal.Equals(CredentialIssuer.Canonicalize(apiBaseUrl), _credentialIssuer))
            {
                _apiBaseUrl = null;
                _discovery = null;
                throw new InvalidServerUrlException(discovery.ApiBaseUrl);
            }

            _discovery = discovery;
            _apiBaseUrl = EnsureTrailingSlash(apiBaseUrl);
            return discovery;
        }
        finally
        {
            _discoveryGate.Release();
        }
    }

    private async Task EnsureDiscoveredAsync(CancellationToken cancellationToken)
    {
        if (_apiBaseUrl is null)
        {
            await DiscoverCoreAsync(force: false, cancellationToken).ConfigureAwait(false);
        }
    }

    private async Task<T> RequestJsonAsync<T>(
        HttpMethod method,
        IReadOnlyList<string> pathSegments,
        IReadOnlyList<KeyValuePair<string, string>>? query,
        object? body,
        bool authenticated,
        CancellationToken cancellationToken)
    {
        var uri = await BuildEndpointAsync(pathSegments, query, cancellationToken).ConfigureAwait(false);
        return await SendJsonResponseAsync<T>(
            method,
            uri,
            SerializeBody(body),
            authenticated,
            retryAfterRefresh: authenticated,
            cancellationToken).ConfigureAwait(false);
    }
    private async Task<T?> RequestOptionalJsonAsync<T>(
        HttpMethod method,
        IReadOnlyList<string> pathSegments,
        IReadOnlyList<KeyValuePair<string, string>>? query,
        object? body,
        bool authenticated,
        CancellationToken cancellationToken) where T : class
    {
        var uri = await BuildEndpointAsync(pathSegments, query, cancellationToken).ConfigureAwait(false);
        var serializedBody = SerializeBody(body);
        HttpResponsePayload response;
        try
        {
            response = await SendResponseAsync(
                method,
                uri,
                serializedBody,
                authenticated,
                retryAfterRefresh: authenticated,
                cancellationToken).ConfigureAwait(false);
        }
        finally
        {
            if (serializedBody is not null)
            {
                CryptographicOperations.ZeroMemory(serializedBody);
            }
        }

        try
        {
            if (response.StatusCode == HttpStatusCode.NoContent)
            {
                return null;
            }

            return JsonSerializer.Deserialize<T>(response.Body, JsonOptions)
                ?? throw new InvalidResponseException();
        }
        catch (JsonException exception)
        {
            throw new InvalidResponseException(exception);
        }
        catch (NotSupportedException exception)
        {
            throw new InvalidResponseException(exception);
        }
        finally
        {
            CryptographicOperations.ZeroMemory(response.Body);
        }
    }


    private Task RequestEmptyAsync(
        HttpMethod method,
        IReadOnlyList<string> pathSegments,
        bool authenticated,
        CancellationToken cancellationToken) =>
        RequestEmptyAsync(method, pathSegments, query: null, authenticated, cancellationToken);

    private async Task RequestEmptyAsync(
        HttpMethod method,
        IReadOnlyList<string> pathSegments,
        IReadOnlyList<KeyValuePair<string, string>>? query,
        bool authenticated,
        CancellationToken cancellationToken)
    {
        var uri = await BuildEndpointAsync(pathSegments, query, cancellationToken).ConfigureAwait(false);
        var response = await SendResponseAsync(
            method,
            uri,
            body: null,
            authenticated,
            retryAfterRefresh: authenticated,
            cancellationToken).ConfigureAwait(false);
        CryptographicOperations.ZeroMemory(response.Body);
    }

    private async Task RequestEmptyWithBodyAsync(
        HttpMethod method,
        IReadOnlyList<string> pathSegments,
        object body,
        bool authenticated,
        CancellationToken cancellationToken)
    {
        var uri = await BuildEndpointAsync(pathSegments, query: null, cancellationToken).ConfigureAwait(false);
        var serializedBody = SerializeBody(body);
        HttpResponsePayload response;
        try
        {
            response = await SendResponseAsync(
                method,
                uri,
                serializedBody,
                authenticated,
                retryAfterRefresh: authenticated,
                cancellationToken).ConfigureAwait(false);
        }
        finally
        {
            if (serializedBody is not null)
            {
                CryptographicOperations.ZeroMemory(serializedBody);
            }
        }
        CryptographicOperations.ZeroMemory(response.Body);
    }

    private async Task<T> SendJsonResponseAsync<T>(
        HttpMethod method,
        Uri uri,
        byte[]? body,
        bool authenticated,
        bool retryAfterRefresh,
        CancellationToken cancellationToken)
    {
        HttpResponsePayload response;
        try
        {
            response = await SendResponseAsync(
                method,
                uri,
                body,
                authenticated,
                retryAfterRefresh,
                cancellationToken).ConfigureAwait(false);
        }
        finally
        {
            if (body is not null)
            {
                CryptographicOperations.ZeroMemory(body);
            }
        }

        try
        {
            return JsonSerializer.Deserialize<T>(response.Body, JsonOptions)
                ?? throw new InvalidResponseException();
        }
        catch (JsonException exception)
        {
            throw new InvalidResponseException(exception);
        }
        catch (NotSupportedException exception)
        {
            throw new InvalidResponseException(exception);
        }
        finally
        {
            CryptographicOperations.ZeroMemory(response.Body);
        }
    }

    private async Task<HttpResponsePayload> SendResponseAsync(
        HttpMethod method,
        Uri uri,
        byte[]? body,
        bool authenticated,
        bool retryAfterRefresh,
        CancellationToken cancellationToken)
    {
        ThrowIfDisposed();
        EnsureCredentialDestination(uri);
        AuthenticationSnapshot? authentication = null;
        if (authenticated)
        {
            authentication = await GetAuthenticationSnapshotAsync(cancellationToken).ConfigureAwait(false)
                ?? throw new NotAuthenticatedException();
        }

        return await SendResponseCoreAsync(
            method,
            uri,
            body,
            authentication?.Credentials.AccessToken,
            authentication?.Epoch,
            retryAfterRefresh,
            cancellationToken).ConfigureAwait(false);
    }

    private Task<HttpResponsePayload> SendResponseWithAccessTokenAsync(
        HttpMethod method,
        Uri uri,
        byte[]? body,
        string accessToken,
        CancellationToken cancellationToken) =>
        SendResponseCoreAsync(
            method,
            uri,
            body,
            accessToken,
            authenticationEpoch: null,
            retryAfterRefresh: false,
            cancellationToken);

    private async Task<HttpResponsePayload> SendResponseCoreAsync(
        HttpMethod method,
        Uri uri,
        byte[]? body,
        string? accessToken,
        long? authenticationEpoch,
        bool retryAfterRefresh,
        CancellationToken cancellationToken)
    {
        using var request = new HttpRequestMessage(method, uri);
        request.Headers.Accept.Add(new MediaTypeWithQualityHeaderValue("application/json"));
        if (accessToken is not null)
        {
            request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", accessToken);
        }

        if (body is not null)
        {
            request.Content = new ByteArrayContent(body);
            request.Content.Headers.ContentType = new MediaTypeHeaderValue("application/json")
            {
                CharSet = "utf-8",
            };
        }

        using var response = await _httpClient.SendAsync(
            request,
            HttpCompletionOption.ResponseHeadersRead,
            cancellationToken).ConfigureAwait(false);
        EnsureCredentialDestination(response.RequestMessage?.RequestUri ?? uri);
        var statusCode = (int)response.StatusCode;
        if (statusCode is >= 300 and <= 399)
        {
            throw new RivuneServerException(
                statusCode,
                "redirect_not_allowed",
                "Rivune server redirects are not allowed.");
        }

        var responseBody = await ReadResponseBodyAsync(response.Content, cancellationToken).ConfigureAwait(false);

        if (response.StatusCode == HttpStatusCode.Unauthorized &&
            authenticationEpoch is not null &&
            retryAfterRefresh)
        {
            CryptographicOperations.ZeroMemory(responseBody);
            await RefreshCredentialsAsync(
                accessToken ?? throw new NotAuthenticatedException(),
                authenticationEpoch.Value,
                cancellationToken).ConfigureAwait(false);
            return await SendResponseAsync(
                method,
                uri,
                body,
                authenticated: true,
                retryAfterRefresh: false,
                cancellationToken).ConfigureAwait(false);
        }

        if (!response.IsSuccessStatusCode)
        {
            var exception = DecodeServerError((int)response.StatusCode, responseBody);
            CryptographicOperations.ZeroMemory(responseBody);
            throw exception;
        }

        return new HttpResponsePayload(response.StatusCode, responseBody);
    }

    private static async Task<byte[]> ReadResponseBodyAsync(
        HttpContent content,
        CancellationToken cancellationToken)
    {
        var declaredLength = content.Headers.ContentLength;
        if (declaredLength > MaximumResponseBodyBytes)
        {
            throw new ResponseTooLargeException(MaximumResponseBodyBytes);
        }

        await using var stream = await content.ReadAsStreamAsync(cancellationToken).ConfigureAwait(false);
        using var buffer = declaredLength is >= 0
            ? new MemoryStream((int)declaredLength.Value)
            : new MemoryStream();
        var readBuffer = ArrayPool<byte>.Shared.Rent(81_920);
        try
        {
            var totalBytes = 0;
            while (true)
            {
                var remainingWithSentinel = MaximumResponseBodyBytes + 1 - totalBytes;
                var bytesRead = await stream.ReadAsync(
                    readBuffer.AsMemory(0, Math.Min(readBuffer.Length, remainingWithSentinel)),
                    cancellationToken).ConfigureAwait(false);
                if (bytesRead == 0)
                {
                    return buffer.ToArray();
                }

                totalBytes += bytesRead;
                if (totalBytes > MaximumResponseBodyBytes)
                {
                    throw new ResponseTooLargeException(MaximumResponseBodyBytes);
                }

                buffer.Write(readBuffer, 0, bytesRead);
            }
        }
        finally
        {
            CryptographicOperations.ZeroMemory(readBuffer);
            ArrayPool<byte>.Shared.Return(readBuffer);
            if (buffer.TryGetBuffer(out var bufferedBytes))
            {
                CryptographicOperations.ZeroMemory(bufferedBytes.AsSpan(0, (int)buffer.Length));
            }
        }
    }

    private async Task<TokenPair> RefreshCredentialsAsync(
        string failedAccessToken,
        long expectedEpoch,
        CancellationToken cancellationToken)
    {
        await _refreshGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        CancellationTokenSource? refreshCancellationSource = null;
        try
        {
            AuthenticationSnapshot current;
            await _credentialGate.WaitAsync(cancellationToken).ConfigureAwait(false);
            try
            {
                if (expectedEpoch != _authenticationEpoch)
                {
                    throw new NotAuthenticatedException();
                }
                if (!_credentialsLoaded)
                {
                    _credentials = await LoadCredentialsFromStoreAsync(cancellationToken).ConfigureAwait(false);
                    _credentialsLoaded = true;
                }

                current = _credentials is null
                    ? throw new NotAuthenticatedException()
                    : new AuthenticationSnapshot(_credentials, _authenticationEpoch);
                if (!StringComparer.Ordinal.Equals(current.Credentials.AccessToken, failedAccessToken))
                {
                    return current.Credentials;
                }

                refreshCancellationSource = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
                _refreshCancellationSource = refreshCancellationSource;
            }
            finally
            {
                _credentialGate.Release();
            }

            try
            {
                var refreshCancellationToken = refreshCancellationSource!.Token;
                var uri = await BuildEndpointAsync(
                    ["auth", "refresh"],
                    query: null,
                    refreshCancellationToken).ConfigureAwait(false);
                EnsureCredentialDestination(uri);
                var result = await SendJsonResponseAsync<TokenPair>(
                    HttpMethod.Post,
                    uri,
                    SerializeBody(new RefreshRequest(current.Credentials.RefreshToken)),
                    authenticated: false,
                    retryAfterRefresh: false,
                    refreshCancellationToken).ConfigureAwait(false);
                await SetCredentialsAsync(result, expectedEpoch, refreshCancellationToken).ConfigureAwait(false);
                return result;
            }
            catch
            {
                await ClearCredentialsIfEpochSuppressingStoreErrorsAsync(expectedEpoch).ConfigureAwait(false);
                throw;
            }
        }
        finally
        {
            if (refreshCancellationSource is not null)
            {
                await _credentialGate.WaitAsync(CancellationToken.None).ConfigureAwait(false);
                try
                {
                    if (ReferenceEquals(_refreshCancellationSource, refreshCancellationSource))
                    {
                        _refreshCancellationSource = null;
                    }
                }
                finally
                {
                    _credentialGate.Release();
                    refreshCancellationSource.Dispose();
                }
            }
            _refreshGate.Release();
        }
    }

    private async Task<long> CaptureAuthenticationEpochAsync(CancellationToken cancellationToken)
    {
        ThrowIfDisposed();
        await _credentialGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            return _authenticationEpoch;
        }
        finally
        {
            _credentialGate.Release();
        }
    }

    private async Task<AuthenticationSnapshot?> GetAuthenticationSnapshotAsync(
        CancellationToken cancellationToken)
    {
        await _credentialGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            if (!_credentialsLoaded)
            {
                _credentials = await LoadCredentialsFromStoreAsync(cancellationToken).ConfigureAwait(false);
                _credentialsLoaded = true;
            }

            return _credentials is null
                ? null
                : new AuthenticationSnapshot(_credentials, _authenticationEpoch);
        }
        finally
        {
            _credentialGate.Release();
        }
    }


    private async Task SetCredentialsAsync(
        TokenPair credentials,
        long expectedEpoch,
        CancellationToken cancellationToken)
    {
        await _credentialGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            if (expectedEpoch != _authenticationEpoch)
            {
                throw new NotAuthenticatedException();
            }

            await _credentialStore.SaveAsync(
                new StoredCredentials
                {
                    Issuer = _credentialIssuer,
                    Credentials = credentials,
                },
                cancellationToken).ConfigureAwait(false);
            _credentials = credentials;
            _credentialsLoaded = true;
        }
        finally
        {
            _credentialGate.Release();
        }
    }

    private async ValueTask<TokenPair?> LoadCredentialsFromStoreAsync(CancellationToken cancellationToken)
    {
        var stored = await _credentialStore.LoadAsync(cancellationToken).ConfigureAwait(false);
        if (stored is null)
        {
            return null;
        }
        if (StringComparer.Ordinal.Equals(stored.Issuer, _credentialIssuer))
        {
            return stored.Credentials;
        }

        return null;
    }

    private async Task ClearCredentialsIfEpochSuppressingStoreErrorsAsync(long expectedEpoch)
    {
        await _credentialGate.WaitAsync(CancellationToken.None).ConfigureAwait(false);
        try
        {
            if (expectedEpoch != _authenticationEpoch)
            {
                return;
            }

            try
            {
                await _credentialStore.ClearAsync(CancellationToken.None).ConfigureAwait(false);
            }
            catch
            {
            }
            finally
            {
                _credentials = null;
                _credentialsLoaded = true;
            }
        }
        finally
        {
            _credentialGate.Release();
        }
    }

    private async Task<Uri> BuildEndpointAsync(
        IReadOnlyList<string> pathSegments,
        IReadOnlyList<KeyValuePair<string, string>>? query,
        CancellationToken cancellationToken)
    {
        await EnsureDiscoveredAsync(cancellationToken).ConfigureAwait(false);
        var apiBaseUrl = _apiBaseUrl ?? throw new InvalidResponseException();
        var encodedPath = string.Join('/', pathSegments.Select(Uri.EscapeDataString));
        var uriBuilder = new UriBuilder(new Uri(apiBaseUrl, encodedPath));

        if (query is { Count: > 0 })
        {
            var encodedQuery = string.Join(
                '&',
                query.Select(item => $"{Uri.EscapeDataString(item.Key)}={Uri.EscapeDataString(item.Value)}"));
            uriBuilder.Query = string.IsNullOrEmpty(uriBuilder.Query)
                ? encodedQuery
                : $"{uriBuilder.Query.TrimStart('?')}&{encodedQuery}";
        }

        return uriBuilder.Uri;
    }

    private static byte[]? SerializeBody(object? body) =>
        body is null ? null : JsonSerializer.SerializeToUtf8Bytes(body, body.GetType(), JsonOptions);

    private static IReadOnlyList<KeyValuePair<string, string>> Query(
        params (string Name, string? Value)[] values) =>
        values
            .Where(value => value.Value is not null)
            .Select(value => KeyValuePair.Create(value.Name, value.Value!))
            .ToArray();

    private static string MappingProviderValue(SeriesMappingProvider provider) => provider switch
    {
        SeriesMappingProvider.Tmdb => "tmdb",
        SeriesMappingProvider.Tvdb => "tvdb",
        _ => throw new ArgumentOutOfRangeException(nameof(provider)),
    };

    private static Dictionary<string, object?> CategoryUpdateBody(CategoryUpdateRequest input)
    {
        var body = new Dictionary<string, object?>();
        if (input.Name is not null) body["name"] = input.Name;
        AddPatch(body, "description", input.Description);
        AddPatch(body, "color", input.Color);
        AddPatch(body, "icon", input.Icon);
        if (input.IsDefault is not null) body["isDefault"] = input.IsDefault;
        return body;
    }

    private static Dictionary<string, object?> DeviceUpdateBody(DeviceUpdateRequest input)
    {
        var body = new Dictionary<string, object?>();
        if (input.Name is not null) body["name"] = input.Name;
        if (input.CategoryId is not null) body["categoryId"] = input.CategoryId;
        AddPatch(body, "internalNote", input.InternalNote);
        return body;
    }

    private static void AddPatch(
        IDictionary<string, object?> body,
        string name,
        PatchField<string> field)
    {
        if (field.IsSpecified) body[name] = field.Value;
    }

    private static RivuneServerException DecodeServerError(int statusCode, byte[] body)
    {
        try
        {
            var envelope = JsonSerializer.Deserialize<ErrorEnvelope>(body, JsonOptions);
            if (envelope?.Error is not null)
            {
                return new RivuneServerException(statusCode, envelope.Error.Code, envelope.Error.Message);
            }
        }
        catch (JsonException)
        {
        }
        catch (NotSupportedException)
        {
        }

        return new RivuneServerException(
            statusCode,
            $"http_{statusCode}",
            $"Rivune server returned HTTP {statusCode}.");
    }

    private static HttpMessageHandler CreateDefaultHandler() =>
        new HttpClientHandler { AllowAutoRedirect = false };

    private static HttpClient CreateHttpClient(HttpMessageHandler handler)
    {
        ArgumentNullException.ThrowIfNull(handler);
        DisableAutomaticRedirects(handler);
        return new HttpClient(handler, disposeHandler: true);
    }

    private static void DisableAutomaticRedirects(HttpMessageHandler handler)
    {
        while (true)
        {
            switch (handler)
            {
                case HttpClientHandler httpClientHandler:
                    httpClientHandler.AllowAutoRedirect = false;
                    return;
                case SocketsHttpHandler socketsHttpHandler:
                    socketsHttpHandler.AllowAutoRedirect = false;
                    return;
                case DelegatingHandler { InnerHandler: not null } delegatingHandler:
                    handler = delegatingHandler.InnerHandler;
                    break;
                default:
                    return;
            }
        }
    }

    private static Uri ParseServerUrl(string value)
    {
        if (!Uri.TryCreate(value, UriKind.Absolute, out var uri))
        {
            throw new InvalidServerUrlException(value);
        }

        return ValidateServerUrl(uri);
    }

    private static Uri ValidateServerUrl(Uri value)
    {
        ArgumentNullException.ThrowIfNull(value);
        if (!value.IsAbsoluteUri || !IsAllowedServerUrl(value))
        {
            throw new InvalidServerUrlException(value.ToString());
        }

        return new Uri(CredentialIssuer.Canonicalize(value), UriKind.Absolute);
    }

    private static bool IsAllowedServerUrl(Uri value) =>
        value.Scheme.Equals(Uri.UriSchemeHttps, StringComparison.OrdinalIgnoreCase) ||
        (value.Scheme.Equals(Uri.UriSchemeHttp, StringComparison.OrdinalIgnoreCase) && value.IsLoopback);

    private void EnsureCredentialDestination(Uri destination)
    {
        if (!IsAllowedServerUrl(destination) ||
            !StringComparer.Ordinal.Equals(CredentialIssuer.Canonicalize(destination), _credentialIssuer))
        {
            throw new InvalidServerUrlException(destination.ToString());
        }
    }

    private static Uri EnsureTrailingSlash(Uri value)
    {
        var builder = new UriBuilder(value);
        if (!builder.Path.EndsWith("/", StringComparison.Ordinal))
        {
            builder.Path += "/";
        }

        return builder.Uri;
    }

    private void ThrowIfDisposed() => ObjectDisposedException.ThrowIf(_disposed, this);

    private sealed record LoginRequest(string Username, string Password, LoginDevice Device);
    private sealed record RefreshRequest(string RefreshToken);
    private sealed record SelectProfileRequest(string? Pin);
    private sealed record InstanceTranscodingPatch(
        [property: JsonIgnore(Condition = JsonIgnoreCondition.Never)] bool? AllowTranscoding);
    private sealed record ProfileTranscodingPatch(
        [property: JsonIgnore(Condition = JsonIgnoreCondition.Never)] string? Transcoding);
    private sealed record PlaybackSourcesRequest(
        string MediaType,
        Guid? AddonId,
        string ResourceId,
        PlaybackCapabilities Capabilities);
    private sealed record PlaybackPrepareRequest(string SourceRef, int? StartSeconds);
    private sealed record PlaybackResolveRequest(
        string SourceRef,
        string? TitleId,
        int? PreferredAudioTrack,
        string? PreferredSubtitleId,
        int? StartSeconds);
    private sealed record ErrorEnvelope(ServerError Error);
}
