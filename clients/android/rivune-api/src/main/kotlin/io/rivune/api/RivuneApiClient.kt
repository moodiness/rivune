package io.rivune.api

import android.content.Context
import java.io.IOException
import java.nio.charset.StandardCharsets
import java.util.UUID
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.serialization.Serializable
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.ResponseBody
import okio.Buffer

private fun validatedServerUrl(value: String): HttpUrl {
    val url = value.toHttpUrlOrNull()?.takeIf(::isCredentialTransportAllowed)
    return url ?: throw RivuneApiException.InvalidServerUrl(value)
}

private fun isCredentialTransportAllowed(url: HttpUrl): Boolean =
    url.encodedUsername.isEmpty() &&
        url.encodedPassword.isEmpty() &&
        (url.scheme == "https" || (url.scheme == "http" && isLoopbackHost(url.host)))

private fun isLoopbackHost(host: String): Boolean {
    if (host == "localhost" || host == "::1") return true
    val octets = host.split('.')
    return octets.size == 4 &&
        octets.first() == "127" &&
        octets.all { it.toIntOrNull() in 0..255 }
}

private fun canonicalOrigin(url: HttpUrl): String = HttpUrl.Builder()
    .scheme(url.scheme)
    .host(url.host)
    .port(url.port)
    .build()
    .toString()

sealed class RivuneApiException(message: String, cause: Throwable? = null) : Exception(message, cause) {
    class InvalidServerUrl(val value: String) : RivuneApiException("Invalid Rivune server URL: $value")
    class IncompatibleProtocol(val expected: Int, val actual: Int) : RivuneApiException("Rivune protocol $actual is incompatible; this client requires $expected")
    class InvalidResponse(cause: Throwable? = null) : RivuneApiException("The Rivune server returned an invalid response", cause)
    class NotAuthenticated : RivuneApiException("Authentication is required")
    class Server(val status: Int, val code: String, override val message: String) : RivuneApiException(message)
    class ResponseTooLarge : RivuneApiException("The Rivune server response exceeds the 16 MiB limit")
}

@Serializable
private data class ErrorEnvelope(val error: ServerError)

@Serializable
private data class DiscoveryEnvelope(
    val name: String,
    val serverVersion: String,
    val protocolVersion: Int,
    val apiBaseUrl: String,
    val setupRequired: Boolean,
    val setupCompleted: Boolean? = null,
    val demoAvailable: Boolean? = null,
    val timezone: String,
    val interfaceLanguage: String? = null,
)

@Serializable
data class ServerError(val code: String, val message: String)

@Serializable
private data class RefreshRequest(val refreshToken: String)

@Serializable
private data class SelectProfileRequest(val pin: String? = null)

@Serializable
private data class PlaybackSourcesRequest(
    val mediaType: String,
    @Serializable(with = UUIDSerializer::class) val addonId: UUID? = null,
    val resourceId: String,
    val capabilities: PlaybackCapabilities,
)

@Serializable
private data class PlaybackPrepareRequest(
    val sourceRef: String,
    val startSeconds: Int? = null,
)

@Serializable
private data class PlaybackResolveRequest(
    val sourceRef: String,
    val titleId: String? = null,
    val preferredAudioTrack: Int? = null,
    val preferredSubtitleId: String? = null,
    val startSeconds: Int? = null,
)

class RivuneApiClient(
    serverUrl: String,
    credentialStore: CredentialStore,
    httpClient: OkHttpClient = OkHttpClient(),
) {
    constructor(serverUrl: String, context: Context, httpClient: OkHttpClient = OkHttpClient()) : this(
        serverUrl = serverUrl,
        credentialStore = AndroidKeystoreCredentialStore(context),
        httpClient = httpClient,
    )

    private val serverUrl: HttpUrl = validatedServerUrl(serverUrl)
    private val credentialIssuer = canonicalOrigin(this.serverUrl)
    private val json = Json {
        ignoreUnknownKeys = true
    }
    private val requestJson = Json {
        explicitNulls = false
    }
    private val authenticationMutex = Mutex()
    private val discoveryMutex = Mutex()
    private val credentialStore = OrderedCredentialStore(credentialStore)
    private val refreshMutex = Mutex()
    private val httpClient = httpClient.newBuilder()
        .followRedirects(false)
        .followSslRedirects(false)
        .build()
    private var apiBaseUrl: HttpUrl? = null
    private var credentials: TokenPair? = null
    private var credentialsLoaded = false
    private var authenticationGeneration = 0L

    suspend fun discover(): Discovery = discoveryMutex.withLock {
        val generation = currentAuthenticationGeneration()
        val url = serverUrl.resolve("/.well-known/rivune") ?: throw RivuneApiException.InvalidServerUrl(serverUrl.toString())
        val response: DiscoveryEnvelope = execute(url, method = "GET", body = null, authenticated = false, retryAfterRefresh = false)
        if (response.protocolVersion != RivuneProtocol.VERSION) {
            throw RivuneApiException.IncompatibleProtocol(RivuneProtocol.VERSION, response.protocolVersion)
        }
        val interfaceLanguage = response.interfaceLanguage ?: throw RivuneApiException.InvalidResponse()
        val discovery = Discovery(
            response.name,
            response.serverVersion,
            response.protocolVersion,
            response.apiBaseUrl,
            response.setupRequired,
            response.setupCompleted,
            response.demoAvailable,
            response.timezone,
            interfaceLanguage,
        )
        val resolved = serverUrl.resolve(discovery.apiBaseUrl)
            ?.takeIf(::isCredentialTransportAllowed)
            ?.takeIf { canonicalOrigin(it) == credentialIssuer }
            ?: throw RivuneApiException.InvalidServerUrl(discovery.apiBaseUrl)
        authenticationMutex.withLock {
            requireAuthenticationGeneration(generation)
            apiBaseUrl = resolved
        }
        discovery
    }

    suspend fun restoreSession(): Boolean {
        loadCredentialsIfNeeded()
        return authenticationMutex.withLock { credentials != null }
    }

    suspend fun login(username: String, password: String, device: LoginDevice): TokenPair {
        val generation = currentAuthenticationGeneration()
        val result: TokenPair = request(
            path = "auth/login",
            method = "POST",
            body = requestJson.encodeToString(LoginRequest(username, password, device)),
            authenticated = false,
        )
        setCredentials(result, generation)
        return result
    }

    suspend fun refreshSession(): TokenPair {
        loadCredentialsIfNeeded()
        val snapshot = authenticationSnapshot()
        return refreshCredentials(snapshot.accessToken, snapshot.generation)
    }

    suspend fun logout() {
        val cleanup = withContext(NonCancellable) {
            authenticationMutex.withLock {
                authenticationGeneration += 1
                val generation = authenticationGeneration
                val capturedCredentials = credentials
                credentials = null
                credentialsLoaded = true
                credentialStore.invalidateAndClear(
                    issuer = credentialIssuer,
                    newGeneration = generation,
                    capturedCredentials = capturedCredentials,
                )
            }
        }

        var remoteError: Exception? = null
        cleanup.credentials?.accessToken?.let { accessToken ->
            try {
                val url = endpoint("auth/logout", emptyMap())
                executeData(
                    url = url,
                    method = "POST",
                    body = null,
                    authenticated = false,
                    retryAfterRefresh = false,
                    explicitAccessToken = accessToken,
                )
            } catch (cause: Exception) {
                remoteError = cause
            }
        }
        cleanup.error?.let { localError ->
            remoteError?.let(localError::addSuppressed)
            throw localError
        }
        remoteError?.let { throw it }
    }

    suspend fun currentAccount(): Account = request("auth/me", authenticated = true)

    suspend fun sessions(): List<Session> = request<SessionList>("auth/sessions", authenticated = true).sessions

    suspend fun categories(): List<Category> = request<CategoryList>("categories", authenticated = true).categories

    suspend fun createCategory(input: CategoryCreateRequest): Category = request(
        path = "categories",
        method = "POST",
        body = requestJson.encodeToString(input),
        authenticated = true,
    )

    suspend fun updateCategory(id: UUID, input: CategoryUpdateRequest): Category = request(
        path = "categories/$id",
        method = "PATCH",
        body = categoryUpdateBody(input),
        authenticated = true,
    )

    suspend fun deleteCategory(id: UUID, reassignToCategoryId: UUID? = null) = requestUnit(
        path = "categories/$id",
        method = "DELETE",
        body = buildJsonObject {
            if (reassignToCategoryId == null) put("reassignToCategoryId", JsonNull)
            else put("reassignToCategoryId", reassignToCategoryId.toString())
        }.toString(),
        authenticated = true,
    )

    suspend fun reorderCategories(categoryIds: List<UUID>): List<Category> = request<CategoryList>(
        path = "categories/order",
        method = "PUT",
        body = requestJson.encodeToString(CategoryOrderRequest(categoryIds)),
        authenticated = true,
    ).categories

    suspend fun devices(categoryId: UUID? = null): List<Device> = request<DeviceList>(
        path = "devices",
        query = mapOf("categoryId" to categoryId?.toString()),
        authenticated = true,
    ).devices

    suspend fun updateDevice(id: UUID, input: DeviceUpdateRequest): Device = request(
        path = "devices/$id",
        method = "PATCH",
        body = deviceUpdateBody(input),
        authenticated = true,
    )

    suspend fun moveProfiles(profileIds: List<UUID>, categoryId: UUID) = requestUnit(
        path = "profiles/category-moves",
        method = "POST",
        body = requestJson.encodeToString(ProfileCategoryMoveRequest(profileIds, categoryId)),
        authenticated = true,
    )

    suspend fun moveDevices(deviceIds: List<UUID>, categoryId: UUID) = requestUnit(
        path = "devices/category-moves",
        method = "POST",
        body = requestJson.encodeToString(DeviceCategoryMoveRequest(deviceIds, categoryId)),
        authenticated = true,
    )

    suspend fun beginDeviceAuthorization(deviceName: String, platform: String): DeviceAuthorizationResponse = request(
        path = "auth/device-code",
        method = "POST",
        body = requestJson.encodeToString(DeviceAuthorizationRequest(deviceName, platform)),
        authenticated = false,
    )

    suspend fun exchangeDeviceAuthorization(deviceCode: String): TokenPair {
        val generation = currentAuthenticationGeneration()
        val result: TokenPair = request(
            path = "auth/device-code/token",
            method = "POST",
            body = requestJson.encodeToString(DeviceCodeTokenRequest(deviceCode)),
            authenticated = false,
        )
        setCredentials(result, generation)
        return result
    }

    suspend fun approveDeviceAuthorization(input: DeviceCodeApprovalRequest) = requestUnit(
        path = "auth/device-code/approve",
        method = "POST",
        body = requestJson.encodeToString(input),
        authenticated = true,
    )

    suspend fun profiles(): List<Profile> = request<ProfileList>("profiles", authenticated = true).profiles

    suspend fun selectProfile(id: UUID, pin: String? = null): ProfileSelection = request(
        path = "profiles/$id/select",
        method = "POST",
        body = requestJson.encodeToString(SelectProfileRequest(pin)),
        authenticated = true,
    )

    suspend fun clearProfileSelection() = requestUnit("profiles/selection", "DELETE", authenticated = true)

    suspend fun instanceSettings(): SettingsLayer = request("settings", authenticated = true)

    suspend fun updateInstanceSettings(allowTranscoding: Boolean?): SettingsLayer = request(
        path = "settings",
        method = "PATCH",
        body = buildJsonObject {
            if (allowTranscoding == null) put("allowTranscoding", JsonNull) else put("allowTranscoding", allowTranscoding)
        }.toString(),
        authenticated = true,
    )

    suspend fun profileSettings(id: UUID): SettingsLayer = request("profiles/$id/settings", authenticated = true)

    suspend fun updateProfileSettings(id: UUID, transcoding: String?): SettingsLayer = request(
        path = "profiles/$id/settings",
        method = "PATCH",
        body = buildJsonObject {
            if (transcoding == null) put("transcoding", JsonNull) else put("transcoding", transcoding)
        }.toString(),
        authenticated = true,
    )

    suspend fun effectiveProfileSettings(id: UUID): EffectiveSettings = request(
        "profiles/$id/settings/effective",
        authenticated = true,
    )

    suspend fun movie(id: UUID, language: String? = null): Movie = request(
        path = "metadata/titles/$id",
        query = mapOf("language" to language),
        authenticated = true,
    )

    suspend fun series(
        id: UUID,
        language: String? = null,
        mappingProvider: SeriesMappingProvider,
        episodeOrder: String? = null,
    ): Series = request(
        path = "metadata/series/$id",
        query = mapOf(
            "language" to language,
            "mappingProvider" to mappingProvider.wireValue,
            "episodeOrder" to episodeOrder,
        ),
        authenticated = true,
    )

    suspend fun season(id: String, language: String? = null, mappingProvider: SeriesMappingProvider): Season = request(
        path = "metadata/seasons/${encodePathSegment(id)}",
        query = mapOf("language" to language, "mappingProvider" to mappingProvider.wireValue),
        authenticated = true,
    )

    suspend fun trailers(titleId: UUID, language: String? = null, captionLanguage: String? = null, seasonNumber: Int? = null): TrailerList = request(
        path = "metadata/titles/$titleId/trailers",
        query = mapOf(
            "language" to language,
            "captionLanguage" to captionLanguage,
            "seasonNumber" to seasonNumber?.toString(),
        ),
        authenticated = true,
    )

    suspend fun playbackSources(
        mediaType: String,
        resourceId: String,
        capabilities: PlaybackCapabilities,
        addonId: UUID? = null,
    ): PlaybackSourceList = request(
        path = "playback/sources",
        method = "POST",
        body = requestJson.encodeToString(PlaybackSourcesRequest(mediaType, addonId, resourceId, capabilities)),
        authenticated = true,
    )

    suspend fun playbackMarkers(imdbId: String, season: Int, episode: Int): PlaybackMarkerList = request(
        path = "playback/markers",
        query = mapOf(
            "imdbId" to imdbId,
            "season" to season.toString(),
            "episode" to episode.toString(),
        ),
        authenticated = true,
    )

    suspend fun preparePlayback(sourceRef: String, startSeconds: Int? = null): PlaybackPreparation = request(
        path = "playback/prepare",
        method = "POST",
        body = requestJson.encodeToString(PlaybackPrepareRequest(sourceRef, startSeconds)),
        authenticated = true,
    )

    suspend fun resolvePlayback(
        sourceRef: String,
        titleId: String? = null,
        preferredAudioTrack: Int? = null,
        preferredSubtitleId: String? = null,
        startSeconds: Int? = null,
    ): PlaybackSession = request(
        path = "playback/resolve",
        method = "POST",
        body = requestJson.encodeToString(PlaybackResolveRequest(sourceRef, titleId, preferredAudioTrack, preferredSubtitleId, startSeconds)),
        authenticated = true,
    )

    suspend fun stopPlayback(sessionId: UUID) = requestUnit("playback/sessions/$sessionId", "DELETE", authenticated = true)

    suspend fun playbackActivity(): PlaybackActivity = request("playback/activity", authenticated = true)

    suspend fun playbackProgress(titleId: UUID): PlaybackProgress? = requestNullable(
        path = "progress/$titleId",
        authenticated = true,
    )

    suspend fun playbackProgressBatch(titleIds: List<UUID>): PlaybackProgressBatch = request(
        path = "progress/batch",
        method = "POST",
        body = requestJson.encodeToString(PlaybackProgressBatchRequest(titleIds)),
        authenticated = true,
    )

    suspend fun updatePlaybackProgress(titleId: UUID, input: UpdatePlaybackProgressRequest): PlaybackProgress = request(
        path = "progress/$titleId",
        method = "PUT",
        body = requestJson.encodeToString(input),
        authenticated = true,
    )

    suspend fun clearPlaybackProgress(titleId: UUID, expectedVersion: Long) = requestUnit(
        path = "progress/$titleId",
        method = "DELETE",
        query = mapOf("expectedVersion" to expectedVersion.toString()),
        authenticated = true,
    )

    suspend fun setTitlesWatchedBatch(items: List<SetWatchedBatchItem>): SetWatchedBatchResult = request(
        path = "titles/watched/batch",
        method = "PUT",
        body = requestJson.encodeToString(SetWatchedBatchRequest(items)),
        authenticated = true,
    )

    suspend fun markTitleWatched(titleId: UUID, expectedVersion: Long): PlaybackProgress = request(
        path = "titles/$titleId/watched",
        method = "POST",
        body = requestJson.encodeToString(CompletionRequest(expectedVersion)),
        authenticated = true,
    )

    suspend fun markTitleUnwatched(titleId: UUID, expectedVersion: Long): PlaybackProgress = request(
        path = "titles/$titleId/watched",
        method = "DELETE",
        query = mapOf("expectedVersion" to expectedVersion.toString()),
        authenticated = true,
    )

    suspend fun continueWatching(limit: Int? = null): ContinueWatchingPage = request(
        path = "continue-watching",
        query = mapOf("limit" to limit?.toString()),
        authenticated = true,
    )

    suspend fun dismissContinueWatchingTitle(titleId: UUID) = requestUnit(
        path = "continue-watching/$titleId",
        method = "DELETE",
        authenticated = true,
    )

    private suspend inline fun <reified Response> request(
        path: String,
        method: String = "GET",
        query: Map<String, String?> = emptyMap(),
        body: String? = null,
        authenticated: Boolean,
    ): Response {
        val url = endpoint(path, query)
        return execute(url, method, body, authenticated, retryAfterRefresh = authenticated)
    }

    private suspend inline fun <reified Response> requestNullable(
        path: String,
        method: String = "GET",
        query: Map<String, String?> = emptyMap(),
        body: String? = null,
        authenticated: Boolean,
    ): Response? {
        val url = endpoint(path, query)
        val data = executeData(url, method, body, authenticated, retryAfterRefresh = authenticated)
        if (data.status == 204) return null
        return decodeResponse(data.body)
    }

    private suspend fun requestUnit(
        path: String,
        method: String,
        query: Map<String, String?> = emptyMap(),
        body: String? = null,
        authenticated: Boolean,
    ) {
        val url = endpoint(path, query)
        executeData(url, method, body, authenticated, retryAfterRefresh = authenticated)
    }

    private suspend fun endpoint(path: String, query: Map<String, String?>): HttpUrl {
        var base = authenticationMutex.withLock { apiBaseUrl }
        if (base == null) {
            discover()
            base = authenticationMutex.withLock { apiBaseUrl }
        }
        val resolvedBase = base ?: throw RivuneApiException.InvalidResponse()
        return resolvedBase.newBuilder().apply {
            path.split('/').filter { it.isNotEmpty() }.forEach(::addEncodedPathSegment)
            query.forEach { (name, value) -> if (value != null) addQueryParameter(name, value) }
        }.build()
    }

    private suspend inline fun <reified Response> execute(
        url: HttpUrl,
        method: String,
        body: String?,
        authenticated: Boolean,
        retryAfterRefresh: Boolean,
    ): Response {
        val data = executeData(url, method, body, authenticated, retryAfterRefresh)
        return decodeResponse(data.body)
    }

    private inline fun <reified Response> decodeResponse(body: String): Response = try {
        json.decodeFromString<Response>(body)
    } catch (cause: Exception) {
        throw RivuneApiException.InvalidResponse(cause)
    }

    private suspend fun executeData(
        url: HttpUrl,
        method: String,
        body: String?,
        authenticated: Boolean,
        retryAfterRefresh: Boolean,
        explicitAccessToken: String? = null,
    ): ResponseData {
        requireServerDestination(url)
        val authentication = if (authenticated) {
            loadCredentialsIfNeeded()
            authenticationSnapshot()
        } else {
            null
        }
        val accessToken = explicitAccessToken ?: authentication?.accessToken
        val request = Request.Builder()
            .url(url)
            .header("Accept", "application/json")
            .apply {
                if (accessToken != null) header("Authorization", "Bearer $accessToken")
                val requestBody = body?.toRequestBody(JSON_MEDIA_TYPE)
                    ?: if (method == "POST" || method == "PUT" || method == "PATCH") ByteArray(0).toRequestBody(JSON_MEDIA_TYPE) else null
                method(method, requestBody)
            }
            .build()

        val response = try {
            withContext(Dispatchers.IO) { httpClient.newCall(request).execute() }
        } catch (cause: IOException) {
            throw cause
        }
        val responseCode = response.code
        val responseSuccessful = response.isSuccessful
        val responseBody = response.use {
            requireServerDestination(it.request.url)
            if (responseCode in 300..399) throw decodeServerError(responseCode, "")
            readResponseBody(it.body)
        }
        if (responseCode == 401 && authentication != null && retryAfterRefresh) {
            refreshCredentials(accessToken, authentication.generation)
            return executeData(url, method, body, authenticated = true, retryAfterRefresh = false)
        }
        if (!responseSuccessful) throw decodeServerError(responseCode, responseBody)
        return ResponseData(responseCode, responseBody)
    }

    private data class ResponseData(val status: Int, val body: String)

    private fun readResponseBody(body: ResponseBody?): String {
        if (body == null) return ""
        if (body.contentLength() > MAX_RESPONSE_BODY_BYTES) {
            throw RivuneApiException.ResponseTooLarge()
        }

        val source = body.source()
        val bufferedBody = Buffer()
        var remaining = MAX_RESPONSE_BODY_BYTES
        while (remaining > 0L) {
            val read = source.read(bufferedBody, remaining)
            if (read == -1L) return bufferedBody.readString(body.contentType()?.charset(StandardCharsets.UTF_8) ?: StandardCharsets.UTF_8)
            remaining -= read
        }
        if (!source.exhausted()) throw RivuneApiException.ResponseTooLarge()
        return bufferedBody.readString(body.contentType()?.charset(StandardCharsets.UTF_8) ?: StandardCharsets.UTF_8)
    }

    private suspend fun refreshCredentials(
        failedAccessToken: String?,
        expectedGeneration: Long,
    ): TokenPair = refreshMutex.withLock {
        val snapshot = authenticationMutex.withLock {
            requireAuthenticationGeneration(expectedGeneration)
            AuthenticationSnapshot(
                expectedGeneration,
                credentials ?: throw RivuneApiException.NotAuthenticated(),
            )
        }
        if (failedAccessToken != null && snapshot.accessToken != failedAccessToken) {
            return@withLock snapshot.tokens
        }
        val refreshToken = snapshot.tokens.refreshToken
        val url = endpoint("auth/refresh", emptyMap())
        authenticationMutex.withLock { requireAuthenticationGeneration(expectedGeneration) }
        requireServerDestination(url)
        try {
            val result: TokenPair = execute(
                url = url,
                method = "POST",
                body = requestJson.encodeToString(RefreshRequest(refreshToken)),
                authenticated = false,
                retryAfterRefresh = false,
            )
            setCredentials(result, expectedGeneration)
            result
        } catch (cause: Exception) {
            clearCredentialsAfterRefreshFailure(expectedGeneration, refreshToken)
            throw cause
        }
    }

    private suspend fun setCredentials(value: TokenPair, expectedGeneration: Long) {
        authenticationMutex.withLock { requireAuthenticationGeneration(expectedGeneration) }
        val saved = credentialStore.save(
            StoredCredentials(credentialIssuer, value),
            expectedGeneration,
        )
        if (!saved) throw staleAuthentication()
        authenticationMutex.withLock {
            requireAuthenticationGeneration(expectedGeneration)
            credentials = value
            credentialsLoaded = true
        }
    }

    private suspend fun loadCredentialsIfNeeded() {
        val generation = authenticationMutex.withLock {
            if (credentialsLoaded) return
            authenticationGeneration
        }
        val stored = credentialStore.load(credentialIssuer, generation)
        val restored = stored?.takeIf { it.issuer == credentialIssuer }?.tokens
        if (stored != null && restored == null) {
            runCatching { credentialStore.clear(credentialIssuer, generation) }
        }
        authenticationMutex.withLock {
            requireAuthenticationGeneration(generation)
            if (!credentialsLoaded) {
                credentials = restored
                credentialsLoaded = true
            }
        }
    }

    private suspend fun clearCredentialsAfterRefreshFailure(
        expectedGeneration: Long,
        refreshToken: String,
    ) {
        val shouldClear = authenticationMutex.withLock {
            if (authenticationGeneration != expectedGeneration ||
                credentials?.refreshToken != refreshToken
            ) {
                false
            } else {
                credentials = null
                credentialsLoaded = true
                true
            }
        }
        if (shouldClear) runCatching { credentialStore.clear(credentialIssuer, expectedGeneration) }
    }

    private suspend fun currentAuthenticationGeneration(): Long =
        authenticationMutex.withLock { authenticationGeneration }

    private suspend fun authenticationSnapshot(): AuthenticationSnapshot =
        authenticationMutex.withLock {
            AuthenticationSnapshot(
                generation = authenticationGeneration,
                tokens = credentials ?: throw RivuneApiException.NotAuthenticated(),
            )
        }

    private fun requireAuthenticationGeneration(expectedGeneration: Long) {
        if (authenticationGeneration != expectedGeneration) throw staleAuthentication()
    }

    private fun staleAuthentication() =
        CancellationException("Authentication state changed")

    private data class AuthenticationSnapshot(
        val generation: Long,
        val tokens: TokenPair,
    ) {
        val accessToken: String
            get() = tokens.accessToken
    }

    private fun requireServerDestination(url: HttpUrl) {
        if (!isCredentialTransportAllowed(url) || canonicalOrigin(url) != credentialIssuer) {
            throw RivuneApiException.InvalidServerUrl(url.toString())
        }
    }

    private fun decodeServerError(status: Int, body: String): RivuneApiException.Server {
        val error = runCatching { json.decodeFromString<ErrorEnvelope>(body).error }.getOrNull()
        return RivuneApiException.Server(
            status = status,
            code = error?.code ?: "http_$status",
            message = error?.message ?: "Rivune server returned HTTP $status",
        )
    }

    private fun encodePathSegment(value: String): String = HttpUrl.Builder()
        .scheme("https")
        .host("localhost")
        .addPathSegment(value)
        .build()
        .encodedPathSegments
        .last()


    private fun categoryUpdateBody(input: CategoryUpdateRequest): String = buildJsonObject {
        input.name?.let { put("name", it) }
        putPatch("description", input.description)
        putPatch("color", input.color)
        putPatch("icon", input.icon)
        input.isDefault?.let { put("isDefault", it) }
    }.toString()

    private fun deviceUpdateBody(input: DeviceUpdateRequest): String = buildJsonObject {
        input.name?.let { put("name", it) }
        input.categoryId?.let { put("categoryId", it.toString()) }
        putPatch("internalNote", input.internalNote)
    }.toString()

    private fun kotlinx.serialization.json.JsonObjectBuilder.putPatch(name: String, field: PatchField<String>) {
        when (field) {
            PatchField.Omitted -> Unit
            PatchField.Null -> put(name, JsonNull)
            is PatchField.Value -> put(name, field.value)
        }
    }
    private companion object {
        val JSON_MEDIA_TYPE = "application/json; charset=utf-8".toMediaType()
        const val MAX_RESPONSE_BODY_BYTES = 16L * 1024L * 1024L
    }
}
