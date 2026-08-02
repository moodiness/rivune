package io.rivune.api

import android.content.Context
import java.io.IOException
import java.util.UUID
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.serialization.Serializable
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody

sealed class RivuneApiException(message: String, cause: Throwable? = null) : Exception(message, cause) {
    class InvalidServerUrl(val value: String) : RivuneApiException("Invalid Rivune server URL: $value")
    class IncompatibleProtocol(val expected: Int, val actual: Int) : RivuneApiException("Rivune protocol $actual is incompatible; this client requires $expected")
    class InvalidResponse(cause: Throwable? = null) : RivuneApiException("The Rivune server returned an invalid response", cause)
    class NotAuthenticated : RivuneApiException("Authentication is required")
    class Server(val status: Int, val code: String, override val message: String) : RivuneApiException(message)
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
    val resourceId: String,
    val capabilities: PlaybackCapabilities,
)

@Serializable
private data class PlaybackPrepareRequest(val sourceRef: String)

@Serializable
private data class PlaybackResolveRequest(
    val sourceRef: String,
    val titleId: String? = null,
    val preferredAudioTrack: Int? = null,
    val preferredSubtitleId: String? = null,
)

class RivuneApiClient(
    serverUrl: String,
    private val credentialStore: CredentialStore,
    private val httpClient: OkHttpClient = OkHttpClient(),
) {
    constructor(serverUrl: String, context: Context, httpClient: OkHttpClient = OkHttpClient()) : this(
        serverUrl = serverUrl,
        credentialStore = AndroidKeystoreCredentialStore(context),
        httpClient = httpClient,
    )

    private val serverUrl: HttpUrl = serverUrl.toHttpUrlOrNull()?.takeIf { it.scheme == "https" || it.scheme == "http" }
        ?: throw RivuneApiException.InvalidServerUrl(serverUrl)
    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
    }
    private val refreshMutex = Mutex()
    private var apiBaseUrl: HttpUrl? = null
    private var credentials: TokenPair? = null
    private var credentialsLoaded = false

    suspend fun discover(): Discovery {
        val url = serverUrl.resolve("/.well-known/rivune") ?: throw RivuneApiException.InvalidServerUrl(serverUrl.toString())
        val response: DiscoveryEnvelope = execute(url, method = "GET", body = null, authenticated = false, retryAfterRefresh = false)
        if (response.protocolVersion != RivuneProtocol.VERSION) {
            throw RivuneApiException.IncompatibleProtocol(RivuneProtocol.VERSION, response.protocolVersion)
        }
        val interfaceLanguage = response.interfaceLanguage ?: throw RivuneApiException.InvalidResponse()
        val discovery = Discovery(response.name, response.serverVersion, response.protocolVersion, response.apiBaseUrl, response.setupRequired, response.timezone, interfaceLanguage)
        val resolved = serverUrl.resolve(discovery.apiBaseUrl)?.takeIf { it.scheme == "https" || it.scheme == "http" }
            ?: throw RivuneApiException.InvalidServerUrl(discovery.apiBaseUrl)
        apiBaseUrl = resolved
        return discovery
    }

    suspend fun restoreSession(): Boolean {
        credentials = credentialStore.load()
        credentialsLoaded = true
        return credentials != null
    }

    suspend fun login(username: String, password: String, device: Device): TokenPair {
        val result: TokenPair = request(
            path = "auth/login",
            method = "POST",
            body = json.encodeToString(LoginRequest(username, password, device)),
            authenticated = false,
        )
        setCredentials(result)
        return result
    }

    suspend fun refreshSession(): TokenPair {
        loadCredentialsIfNeeded()
        return refreshCredentials(credentials?.accessToken)
    }

    suspend fun logout() {
        loadCredentialsIfNeeded()
        if (credentials != null) requestUnit("auth/logout", "POST", authenticated = true)
        credentials = null
        credentialStore.clear()
    }

    suspend fun currentAccount(): Account = request("auth/me", authenticated = true)

    suspend fun profiles(): List<Profile> = request<ProfileList>("profiles", authenticated = true).profiles

    suspend fun selectProfile(id: UUID, pin: String? = null): ProfileSelection = request(
        path = "profiles/$id/select",
        method = "POST",
        body = json.encodeToString(SelectProfileRequest(pin)),
        authenticated = true,
    )

    suspend fun clearProfileSelection() = requestUnit("profiles/selection", "DELETE", authenticated = true)

    suspend fun movie(id: UUID, language: String? = null): Movie = request(
        path = "metadata/titles/$id",
        query = mapOf("language" to language),
        authenticated = true,
    )

    suspend fun series(id: UUID, language: String? = null, mappingProvider: SeriesMappingProvider): Series = request(
        path = "metadata/series/$id",
        query = mapOf("language" to language, "mappingProvider" to mappingProvider.wireValue),
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

    suspend fun playbackSources(mediaType: String, resourceId: String, capabilities: PlaybackCapabilities): PlaybackSourceList = request(
        path = "playback/sources",
        method = "POST",
        body = json.encodeToString(PlaybackSourcesRequest(mediaType, resourceId, capabilities)),
        authenticated = true,
    )

    suspend fun preparePlayback(sourceRef: String): PlaybackPreparation = request(
        path = "playback/prepare",
        method = "POST",
        body = json.encodeToString(PlaybackPrepareRequest(sourceRef)),
        authenticated = true,
    )

    suspend fun resolvePlayback(
        sourceRef: String,
        titleId: String? = null,
        preferredAudioTrack: Int? = null,
        preferredSubtitleId: String? = null,
    ): PlaybackSession = request(
        path = "playback/resolve",
        method = "POST",
        body = json.encodeToString(PlaybackResolveRequest(sourceRef, titleId, preferredAudioTrack, preferredSubtitleId)),
        authenticated = true,
    )

    suspend fun stopPlayback(sessionId: UUID) = requestUnit("playback/sessions/$sessionId", "DELETE", authenticated = true)

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

    private suspend fun requestUnit(path: String, method: String, authenticated: Boolean) {
        val url = endpoint(path, emptyMap())
        executeData(url, method, null, authenticated, retryAfterRefresh = authenticated)
    }

    private suspend fun endpoint(path: String, query: Map<String, String?>): HttpUrl {
        if (apiBaseUrl == null) discover()
        val base = apiBaseUrl ?: throw RivuneApiException.InvalidResponse()
        return base.newBuilder().apply {
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
        return try {
            json.decodeFromString<Response>(data)
        } catch (cause: Exception) {
            throw RivuneApiException.InvalidResponse(cause)
        }
    }

    private suspend fun executeData(
        url: HttpUrl,
        method: String,
        body: String?,
        authenticated: Boolean,
        retryAfterRefresh: Boolean,
    ): String {
        if (authenticated) loadCredentialsIfNeeded()
        val accessToken = if (authenticated) credentials?.accessToken ?: throw RivuneApiException.NotAuthenticated() else null
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
        response.use {
            val responseBody = it.body?.string().orEmpty()
            if (it.code == 401 && authenticated && retryAfterRefresh) {
                refreshCredentials(accessToken)
                return executeData(url, method, body, authenticated = true, retryAfterRefresh = false)
            }
            if (!it.isSuccessful) throw decodeServerError(it.code, responseBody)
            return responseBody
        }
    }

    private suspend fun refreshCredentials(failedAccessToken: String?): TokenPair = refreshMutex.withLock {
        if (failedAccessToken != null && credentials?.accessToken != failedAccessToken) {
            return@withLock credentials ?: throw RivuneApiException.NotAuthenticated()
        }
        val refreshToken = credentials?.refreshToken ?: throw RivuneApiException.NotAuthenticated()
        if (apiBaseUrl == null) discover()
        val url = endpoint("auth/refresh", emptyMap())
        try {
            val result: TokenPair = execute(
                url = url,
                method = "POST",
                body = json.encodeToString(RefreshRequest(refreshToken)),
                authenticated = false,
                retryAfterRefresh = false,
            )
            setCredentials(result)
            result
        } catch (cause: Exception) {
            credentials = null
            runCatching { credentialStore.clear() }
            throw cause
        }
    }

    private suspend fun setCredentials(value: TokenPair) {
        credentialStore.save(value)
        credentials = value
        credentialsLoaded = true
    }

    private suspend fun loadCredentialsIfNeeded() {
        if (credentialsLoaded) return
        credentials = credentialStore.load()
        credentialsLoaded = true
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

    private companion object {
        val JSON_MEDIA_TYPE = "application/json; charset=utf-8".toMediaType()
    }
}
