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

    private sealed record DiscoveryEnvelope
    {
        public required string Name { get; init; }
        public required string ServerVersion { get; init; }
        public required int ProtocolVersion { get; init; }
        public required string ApiBaseUrl { get; init; }
        public required bool SetupRequired { get; init; }
        public required string Timezone { get; init; }
        public string? InterfaceLanguage { get; init; }
    }

    private readonly Uri _serverUrl;
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
    private bool _disposed;

    public RivuneApiClient(string serverUrl, HttpClient httpClient, ICredentialStore credentialStore)
        : this(ParseServerUrl(serverUrl), httpClient, credentialStore, ownsCredentialStore: false)
    {
    }

    public RivuneApiClient(Uri serverUrl, HttpClient httpClient, ICredentialStore credentialStore)
        : this(ValidateServerUrl(serverUrl), httpClient, credentialStore, ownsCredentialStore: false)
    {
    }

    public RivuneApiClient(string serverUrl, HttpClient httpClient)
        : this(ParseServerUrl(serverUrl), httpClient, new DpapiCredentialStore(), ownsCredentialStore: true)
    {
    }

    public RivuneApiClient(Uri serverUrl, HttpClient httpClient)
        : this(ValidateServerUrl(serverUrl), httpClient, new DpapiCredentialStore(), ownsCredentialStore: true)
    {
    }

    private RivuneApiClient(
        Uri serverUrl,
        HttpClient httpClient,
        ICredentialStore credentialStore,
        bool ownsCredentialStore)
    {
        _serverUrl = serverUrl;
        _httpClient = httpClient ?? throw new ArgumentNullException(nameof(httpClient));
        _credentialStore = credentialStore ?? throw new ArgumentNullException(nameof(credentialStore));
        _ownsCredentialStore = ownsCredentialStore;
    }

    public Task<Discovery> DiscoverAsync(CancellationToken cancellationToken = default) =>
        DiscoverCoreAsync(force: true, cancellationToken);

    public async Task<bool> RestoreSessionAsync(CancellationToken cancellationToken = default)
    {
        ThrowIfDisposed();
        await _credentialGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            _credentials = await _credentialStore.LoadAsync(cancellationToken).ConfigureAwait(false);
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
        Device device,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(username);
        ArgumentNullException.ThrowIfNull(password);
        ArgumentNullException.ThrowIfNull(device);

        var result = await RequestJsonAsync<TokenPair>(
            HttpMethod.Post,
            ["auth", "login"],
            query: null,
            new LoginRequest(username, password, device),
            authenticated: false,
            cancellationToken).ConfigureAwait(false);
        await SetCredentialsAsync(result, cancellationToken).ConfigureAwait(false);
        return result;
    }

    public async Task<TokenPair> RefreshSessionAsync(CancellationToken cancellationToken = default)
    {
        ThrowIfDisposed();
        var current = await GetCredentialsAsync(cancellationToken).ConfigureAwait(false)
            ?? throw new NotAuthenticatedException();
        return await RefreshCredentialsAsync(current.AccessToken, cancellationToken).ConfigureAwait(false);
    }

    public async Task LogoutAsync(CancellationToken cancellationToken = default)
    {
        ThrowIfDisposed();
        if (await GetCredentialsAsync(cancellationToken).ConfigureAwait(false) is not null)
        {
            await RequestEmptyAsync(
                HttpMethod.Post,
                ["auth", "logout"],
                authenticated: true,
                cancellationToken).ConfigureAwait(false);
        }

        await ClearCredentialsAsync(cancellationToken).ConfigureAwait(false);
    }

    public Task<Account> GetCurrentAccountAsync(CancellationToken cancellationToken = default) =>
        RequestJsonAsync<Account>(HttpMethod.Get, ["auth", "me"], null, null, true, cancellationToken);

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
        CancellationToken cancellationToken = default) =>
        RequestJsonAsync<Series>(
            HttpMethod.Get,
            ["metadata", "series", id.ToString("D")],
            Query(
                ("language", language),
                ("mappingProvider", MappingProviderValue(mappingProvider))),
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

    public Task<PlaybackSourceList> GetPlaybackSourcesAsync(
        string mediaType,
        string resourceId,
        PlaybackCapabilities capabilities,
        CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrEmpty(resourceId);
        ArgumentException.ThrowIfNullOrEmpty(mediaType);
        ArgumentNullException.ThrowIfNull(capabilities);
        return RequestJsonAsync<PlaybackSourceList>(
            HttpMethod.Post,
            ["playback", "sources"],
            null,
            new PlaybackSourcesRequest(mediaType, resourceId, capabilities),
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

    public void Dispose()
    {
        if (_disposed)
        {
            return;
        }

        _disposed = true;
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
                Timezone = response.Timezone,
                InterfaceLanguage = response.InterfaceLanguage,
            };
            if (!Uri.TryCreate(_serverUrl, discovery.ApiBaseUrl, out var apiBaseUrl) || !IsHttpUrl(apiBaseUrl))
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

    private async Task RequestEmptyAsync(
        HttpMethod method,
        IReadOnlyList<string> pathSegments,
        bool authenticated,
        CancellationToken cancellationToken)
    {
        var uri = await BuildEndpointAsync(pathSegments, query: null, cancellationToken).ConfigureAwait(false);
        var responseBody = await SendResponseBytesAsync(
            method,
            uri,
            body: null,
            authenticated,
            retryAfterRefresh: authenticated,
            cancellationToken).ConfigureAwait(false);
        CryptographicOperations.ZeroMemory(responseBody);
    }

    private async Task<T> SendJsonResponseAsync<T>(
        HttpMethod method,
        Uri uri,
        byte[]? body,
        bool authenticated,
        bool retryAfterRefresh,
        CancellationToken cancellationToken)
    {
        byte[] responseBody;
        try
        {
            responseBody = await SendResponseBytesAsync(
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
            return JsonSerializer.Deserialize<T>(responseBody, JsonOptions)
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
            CryptographicOperations.ZeroMemory(responseBody);
        }
    }

    private async Task<byte[]> SendResponseBytesAsync(
        HttpMethod method,
        Uri uri,
        byte[]? body,
        bool authenticated,
        bool retryAfterRefresh,
        CancellationToken cancellationToken)
    {
        ThrowIfDisposed();
        var accessToken = authenticated
            ? (await GetCredentialsAsync(cancellationToken).ConfigureAwait(false))?.AccessToken
                ?? throw new NotAuthenticatedException()
            : null;

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
        var responseBody = await response.Content.ReadAsByteArrayAsync(cancellationToken).ConfigureAwait(false);

        if (response.StatusCode == HttpStatusCode.Unauthorized && authenticated && retryAfterRefresh)
        {
            CryptographicOperations.ZeroMemory(responseBody);
            await RefreshCredentialsAsync(accessToken ?? throw new NotAuthenticatedException(), cancellationToken).ConfigureAwait(false);
            return await SendResponseBytesAsync(
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

        return responseBody;
    }

    private async Task<TokenPair> RefreshCredentialsAsync(
        string failedAccessToken,
        CancellationToken cancellationToken)
    {
        await _refreshGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            var current = await GetCredentialsAsync(cancellationToken).ConfigureAwait(false)
                ?? throw new NotAuthenticatedException();
            if (!StringComparer.Ordinal.Equals(current.AccessToken, failedAccessToken))
            {
                return current;
            }

            try
            {
                var uri = await BuildEndpointAsync(["auth", "refresh"], query: null, cancellationToken).ConfigureAwait(false);
                var result = await SendJsonResponseAsync<TokenPair>(
                    HttpMethod.Post,
                    uri,
                    SerializeBody(new RefreshRequest(current.RefreshToken)),
                    authenticated: false,
                    retryAfterRefresh: false,
                    cancellationToken).ConfigureAwait(false);
                await SetCredentialsAsync(result, cancellationToken).ConfigureAwait(false);
                return result;
            }
            catch
            {
                await ClearCredentialsSuppressingStoreErrorsAsync().ConfigureAwait(false);
                throw;
            }
        }
        finally
        {
            _refreshGate.Release();
        }
    }

    private async Task<TokenPair?> GetCredentialsAsync(CancellationToken cancellationToken)
    {
        await _credentialGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            if (!_credentialsLoaded)
            {
                _credentials = await _credentialStore.LoadAsync(cancellationToken).ConfigureAwait(false);
                _credentialsLoaded = true;
            }

            return _credentials;
        }
        finally
        {
            _credentialGate.Release();
        }
    }

    private async Task SetCredentialsAsync(TokenPair credentials, CancellationToken cancellationToken)
    {
        await _credentialGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            await _credentialStore.SaveAsync(credentials, cancellationToken).ConfigureAwait(false);
            _credentials = credentials;
            _credentialsLoaded = true;
        }
        finally
        {
            _credentialGate.Release();
        }
    }

    private async Task ClearCredentialsAsync(CancellationToken cancellationToken)
    {
        await _credentialGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            try
            {
                await _credentialStore.ClearAsync(cancellationToken).ConfigureAwait(false);
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

    private async Task ClearCredentialsSuppressingStoreErrorsAsync()
    {
        try
        {
            await ClearCredentialsAsync(CancellationToken.None).ConfigureAwait(false);
        }
        catch
        {
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

    private static Uri ParseServerUrl(string value)
    {
        if (!Uri.TryCreate(value, UriKind.Absolute, out var uri) || !IsHttpUrl(uri))
        {
            throw new InvalidServerUrlException(value);
        }

        return uri;
    }

    private static Uri ValidateServerUrl(Uri value)
    {
        ArgumentNullException.ThrowIfNull(value);
        if (!value.IsAbsoluteUri || !IsHttpUrl(value))
        {
            throw new InvalidServerUrlException(value.ToString());
        }

        return value;
    }

    private static bool IsHttpUrl(Uri value) =>
        value.Scheme.Equals(Uri.UriSchemeHttps, StringComparison.OrdinalIgnoreCase) ||
        value.Scheme.Equals(Uri.UriSchemeHttp, StringComparison.OrdinalIgnoreCase);

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

    private sealed record LoginRequest(string Username, string Password, Device Device);
    private sealed record RefreshRequest(string RefreshToken);
    private sealed record SelectProfileRequest(string? Pin);
    private sealed record InstanceTranscodingPatch(
        [property: JsonIgnore(Condition = JsonIgnoreCondition.Never)] bool? AllowTranscoding);
    private sealed record ProfileTranscodingPatch(
        [property: JsonIgnore(Condition = JsonIgnoreCondition.Never)] string? Transcoding);
    private sealed record PlaybackSourcesRequest(
        string MediaType,
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
