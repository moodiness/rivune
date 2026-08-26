package io.rivune.api

import android.content.Context
import java.io.IOException
import java.nio.charset.StandardCharsets
import java.util.UUID
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.Job
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
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.ResponseBody
import okio.Buffer

/**
 * Normalizes a user-entered Rivune server address. Local-network hosts default
 * to HTTP; public hosts default to HTTPS.
 */
fun normalizeServerUrl(value: String): String? {
    val trimmed = value.trim()
    if (trimmed.isEmpty() || trimmed.any(Char::isWhitespace)) return null
    val withScheme = if ("://" in trimmed) {
        trimmed
    } else {
        val host = "http://$trimmed".toHttpUrlOrNull()?.host
        val scheme = if (host != null && isLocalNetworkHost(host)) "http" else "https"
        "$scheme://$trimmed"
    }
    return withScheme.trimEnd('/').takeIf(String::isNotBlank)
}

/** Returns whether a supported server URL is known to require local-network access. */
fun isKnownLocalNetworkServerUrl(value: String): Boolean {
    if (value.isBlank() || value.any(Char::isWhitespace)) return false
    val url = value.toHttpUrlOrNull() ?: return false
    if (hasExplicitUserInfo(value) || url.encodedUsername.isNotEmpty() || url.encodedPassword.isNotEmpty()) return false
    return when (url.scheme) {
        "http" -> isPrivateLanLiteralHost(url.host)
        "https" -> isPrivateLanLiteralHost(url.host) || isLocalHostname(url.host)
        else -> false
    }
}

private fun isLocalHostname(rawHost: String): Boolean {
    val host = rawHost.lowercase().removeSuffix(".")
    return host.length > ".local".length && host.endsWith(".local")
}

private fun hasExplicitUserInfo(value: String): Boolean {
    val authorityStart = value.indexOf("://").takeIf { it >= 0 }?.plus(3) ?: return false
    val authorityEnd = value.indexOfAny(charArrayOf('/', '\\', '?', '#'), authorityStart)
        .takeIf { it >= 0 }
        ?: value.length
    return value.indexOf('@', authorityStart) in authorityStart until authorityEnd
}

private fun validatedServerUrl(value: String): HttpUrl {
    val url = value.toHttpUrlOrNull()?.takeIf(::isCredentialTransportAllowed)
    return url ?: throw RivuneApiException.InvalidServerUrl(value)
}

private fun isCredentialTransportAllowed(url: HttpUrl): Boolean =
    url.encodedUsername.isEmpty() &&
        url.encodedPassword.isEmpty() &&
        (url.scheme == "https" || (url.scheme == "http" && isLocalNetworkHost(url.host)))

private fun isLocalNetworkHost(rawHost: String): Boolean {
    val host = rawHost.trim('[', ']').lowercase()
    if (host == "localhost" || host == "::1") return true
    if (!host.contains(':')) {
        val octets = host.split('.', limit = 5).map { it.toIntOrNull() ?: return false }
        if (octets.size == 4 && octets.all { it in 0..255 } && octets[0] == 127) return true
    }
    return isPrivateLanLiteralHost(host)
}

private fun isPrivateLanLiteralHost(rawHost: String): Boolean {
    val host = rawHost.trim('[', ']').lowercase()
    if (host.isEmpty()) return false
    if (host.contains(':')) {
        val separator = host.indexOf(':')
        val prefix = host.substring(0, separator).toIntOrNull(16) ?: return false
        return prefix and 0xfe00 == 0xfc00
    }
    val octets = host.split('.', limit = 5).map { it.toIntOrNull() ?: return false }
    if (octets.size != 4 || octets.any { it !in 0..255 }) return false
    return octets[0] == 10 ||
        (octets[0] == 172 && octets[1] in 16..31) ||
        (octets[0] == 192 && octets[1] == 168)
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
    class Server(val status: Int, val code: String, override val message: String, val retryAfterSeconds: Long? = null) : RivuneApiException(message)
    class ResponseTooLarge(limit: String = "16 MiB") : RivuneApiException("The Rivune server response exceeds the $limit limit")
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
    @Serializable(with = DiscoveryCapabilitiesSerializer::class)
    val capabilities: List<String> = emptyList(),
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
    val externalPlayer: Boolean = false,
)

@Serializable
private data class PlaybackResolveRequest(
    val sourceRef: String,
    val titleId: String? = null,
    val preferredAudioTrack: Int? = null,
    val preferredSubtitleId: String? = null,
    val startSeconds: Int? = null,
    val externalPlayer: Boolean = false,
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
    private val strictV22Json = Json { ignoreUnknownKeys = false }
    private val requestJson = Json {
        explicitNulls = false
    }
    private val authenticationMutex = Mutex()
    private val discoveryMutex = Mutex()
    private val credentialStore = OrderedCredentialStore(credentialStore)
    private val refreshMutex = Mutex()
    private val profileSelectionMutex = Mutex()
    private val httpClient = httpClient.secureBuilder().build()
    private val collectionArtworkHttpClient = httpClient.secureBuilder()
        .callTimeout(COLLECTION_ARTWORK_TIMEOUT_SECONDS, TimeUnit.SECONDS)
        .readTimeout(COLLECTION_ARTWORK_TIMEOUT_SECONDS, TimeUnit.SECONDS)
        .writeTimeout(COLLECTION_ARTWORK_TIMEOUT_SECONDS, TimeUnit.SECONDS)
        .build()
    private val mediaPreparationHttpClient = httpClient.secureBuilder()
        .callTimeout(MEDIA_PREPARATION_TIMEOUT_SECONDS, TimeUnit.SECONDS)
        .readTimeout(MEDIA_PREPARATION_TIMEOUT_SECONDS, TimeUnit.SECONDS)
        .writeTimeout(MEDIA_PREPARATION_TIMEOUT_SECONDS, TimeUnit.SECONDS)
        .build()
    private val semanticSearchHttpClient = httpClient.secureBuilder()
        .callTimeout(SEMANTIC_SEARCH_TIMEOUT_MILLISECONDS, TimeUnit.MILLISECONDS)
        .build()
    private var apiBaseUrl: HttpUrl? = null
    private var credentials: TokenPair? = null
    private var credentialsLoaded = false
    private var authenticationGeneration = 0L

    private var profileContext: String? = null
    private var profileContextGeneration = 0L
    private var profileContextMutationInFlight = false
    suspend fun discover(): Discovery = discoveryMutex.withLock {
        val generation = currentAuthenticationGeneration()
        val url = serverUrl.resolve("/.well-known/rivune") ?: throw RivuneApiException.InvalidServerUrl(serverUrl.toString())
        val response: DiscoveryEnvelope = execute(url, method = "GET", body = null, authenticated = false, retryAfterRefresh = false)
        if (response.protocolVersion != RivuneProtocol.VERSION) {
            throw RivuneApiException.IncompatibleProtocol(RivuneProtocol.VERSION, response.protocolVersion)
        }
        val interfaceLanguage = response.interfaceLanguage ?: throw RivuneApiException.InvalidResponse()
        val discovery = Discovery(
            name = response.name,
            serverVersion = response.serverVersion,
            protocolVersion = response.protocolVersion,
            apiBaseUrl = response.apiBaseUrl,
            setupRequired = response.setupRequired,
            setupCompleted = response.setupCompleted,
            demoAvailable = response.demoAvailable,
            timezone = response.timezone,
            interfaceLanguage = interfaceLanguage,
            capabilities = response.capabilities,
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
        val generation = beginCredentialReplacement()
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
        return refreshCredentials(snapshot.accessToken, snapshot)
    }

    suspend fun logout() {
        val cleanup = withContext(NonCancellable) {
            authenticationMutex.withLock {
                authenticationGeneration += 1
                val generation = authenticationGeneration
                val capturedCredentials = credentials
                credentials = null
                credentialsLoaded = true
                profileContext = null
                profileContextGeneration += 1
                profileContextMutationInFlight = false
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

    suspend fun beginDeviceAuthorization(installationId: String, deviceName: String, platform: String): DeviceAuthorizationResponse = request(
        path = "auth/device-code",
        method = "POST",
        body = requestJson.encodeToString(DeviceAuthorizationRequest(installationId, deviceName, platform)),
        authenticated = false,
    )

    suspend fun exchangeDeviceAuthorization(deviceCode: String): TokenPair {
        val generation = beginCredentialReplacement()
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

    suspend fun selectProfile(id: UUID, pin: String? = null): ProfileSelection = profileSelectionMutex.withLock {
        val state = reserveProfileMutation()
        try {
            val url = endpoint("profiles/$id/select", emptyMap())
            val body = requestJson.encodeToString(SelectProfileRequest(pin))
            loadCredentialsIfNeeded()
            val authentication = authenticationSnapshot(state)
            val requestCancellationJob = currentCoroutineContext()[Job]
            currentCoroutineContext().ensureActive()
            val selection = withContext(NonCancellable) {
                val responseData = executeData(
                    url = url,
                    method = "POST",
                    body = body,
                    authenticated = true,
                    retryAfterRefresh = true,
                    expectedAuthentication = authentication,
                    expectedProfileMutation = state,
                    requestCancellationJob = requestCancellationJob,
                )
                val response: ProfileSelection = decodeResponse(responseData.body)
                commitProfileContext(response.profileContext, state)
                response
            }
            currentCoroutineContext().ensureActive()
            selection
        } finally {
            finishProfileMutation()
        }
    }

    suspend fun clearProfileSelection() = profileSelectionMutex.withLock {
        val state = reserveProfileMutation()
        try {
            val url = endpoint("profiles/selection", emptyMap())
            loadCredentialsIfNeeded()
            val authentication = authenticationSnapshot(state)
            val requestCancellationJob = currentCoroutineContext()[Job]
            currentCoroutineContext().ensureActive()
            withContext(NonCancellable) {
                executeData(
                    url = url,
                    method = "DELETE",
                    body = null,
                    authenticated = true,
                    retryAfterRefresh = true,
                    expectedAuthentication = authentication,
                    expectedProfileMutation = state,
                    requestCancellationJob = requestCancellationJob,
                )
                commitProfileContext(null, state)
            }
            currentCoroutineContext().ensureActive()
        } finally {
            finishProfileMutation()
        }
    }

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

    suspend fun updateProfileSettings(id: UUID, input: ProfileSettingsUpdate): SettingsLayer = request(
        path = "profiles/$id/settings",
        method = "PATCH",
        body = profileSettingsUpdateBody(input),
        authenticated = true,
    )

    suspend fun effectiveProfileSettings(id: UUID): EffectiveSettings = request(
        "profiles/$id/settings/effective",
        authenticated = true,
    )
    suspend fun exportProfileArchive(profileId: UUID): kotlinx.serialization.json.JsonObject =
        validateProfileArchive(request("profiles/$profileId/archive", authenticated = true))

    suspend fun importProfileArchive(
        profileId: UUID,
        archive: kotlinx.serialization.json.JsonObject,
    ): ProfileArchiveImportReport = request(
        path = "profiles/$profileId/archive/import",
        method = "POST",
        body = boundedProfileArchiveBody(archive),
        authenticated = true,
    )

    suspend fun createProfileFromArchive(
        categoryId: UUID,
        archive: kotlinx.serialization.json.JsonObject,
    ): ProfileArchiveImportReport = request(
        path = "profiles/archive",
        method = "POST",
        body = buildJsonObject {
            put("categoryId", categoryId.toString())
            put("archive", validateProfileArchive(archive))
        }.toString().also(::requireProfileArchiveSize),
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

    suspend fun calendar(from: String, to: String, language: String? = null): List<CalendarEvent> = request<CalendarEventList>(
        path = "calendar",
        query = mapOf("from" to from, "to" to to, "language" to language),
        authenticated = true,
    ).events

    suspend fun collections(): List<Collection> = request<CollectionList>(
        path = "collections",
        authenticated = true,
    ).collections

    suspend fun collection(id: UUID): Collection = request(
        path = "collections/$id",
        authenticated = true,
    )
    suspend fun profileAvatar(profileId: UUID): ByteArray {
        val url = endpoint("profiles/$profileId/avatar", emptyMap())
        return executeBytes(url, authenticated = true, retryAfterRefresh = true)
    }


    suspend fun resolveCollectionFolder(
        collectionId: UUID,
        folderId: UUID,
        page: Int? = null,
        limit: Int? = null,
        language: String? = null,
        region: String? = null,
    ): ResolvedCollectionFolder = request(
        path = "collections/$collectionId/folders/$folderId/items",
        query = mapOf(
            "page" to page?.toString(),
            "limit" to limit?.toString(),
            "language" to language,
            "region" to region,
        ),
        authenticated = true,
    )
    suspend fun resolveCollectionFolderArtwork(
        collectionId: UUID,
        folderId: UUID,
        language: String? = null,
    ): ResolvedCollectionFolder = request(
        path = "collections/$collectionId/folders/$folderId/items",
        query = mapOf("page" to "1", "limit" to "1", "language" to language),
        authenticated = true,
        client = collectionArtworkHttpClient,
    )

    suspend fun semanticSearch(input: SemanticSearchRequest): SemanticSearchPage = request(
        path = "search/semantic",
        method = "POST",
        body = requestJson.encodeToString(input),
        authenticated = true,
        client = semanticSearchHttpClient,
    )


    suspend fun addonCatalogs(): List<AddonCatalogDescriptor> = request<AddonCatalogDescriptorList>(
        path = "addons/catalogs",
        authenticated = true,
    ).catalogs

    suspend fun searchAddonCatalogs(
        type: String,
        search: String,
        skip: Int? = null,
        limit: Int? = null,
        extras: List<Pair<String, String>> = emptyList(),
        language: String? = null,
    ): AddonResourceBatch = requestWithQueryItems(
        path = "addons/catalogs/search/${encodePathSegment(type)}",
        query = listOfNotNull(
            "search" to search,
            skip?.let { "skip" to it.toString() },
            limit?.let { "limit" to it.toString() },
            language?.let { "language" to it },
        ) + extras,
        authenticated = true,
    )

    suspend fun addonResource(
        addonId: UUID,
        resource: String,
        type: String,
        id: String,
        skip: Int? = null,
        limit: Int? = null,
        extras: List<Pair<String, String>> = emptyList(),
    ): AddonResourceResult = requestWithQueryItems(
        path = "addons/$addonId/resource/${encodePathSegment(resource)}/${encodePathSegment(type)}/${encodePathSegment(id)}",
        query = listOfNotNull(
            skip?.let { "skip" to it.toString() },
            limit?.let { "limit" to it.toString() },
        ) + extras,
        authenticated = true,
    )

    suspend fun addonResources(
        resource: String,
        type: String,
        id: String,
        extras: List<Pair<String, String>> = emptyList(),
    ): AddonResourceBatch = requestWithQueryItems(
        path = "addons/resources/${encodePathSegment(resource)}/${encodePathSegment(type)}/${encodePathSegment(id)}",
        query = extras,
        authenticated = true,
    )

    suspend fun resolveTitle(input: TitleResolveInput): TitleReference = request(
        path = "titles/resolve",
        method = "POST",
        body = requestJson.encodeToString(input),
        authenticated = true,
    )

    suspend fun resolveCustomSeries(input: CustomSeriesResolveInput): CustomSeriesResolveResult = request(
        path = "titles/custom-series/resolve",
        method = "POST",
        body = requestJson.encodeToString(input),
        authenticated = true,
    )

    suspend fun library(
        mediaType: TitleMediaType? = null,
        page: Int? = null,
        pageSize: Int? = null,
    ): LibraryPage = request(
        path = "library",
        query = mapOf(
            "mediaType" to mediaType?.wireValue,
            "page" to page?.toString(),
            "pageSize" to pageSize?.toString(),
        ),
        authenticated = true,
    )

    suspend fun tvLibraryMembership(identities: List<TVLibraryIdentity>): TVLibraryMembershipResult = request(
        path = "library/membership",
        method = "POST",
        body = requestJson.encodeToString(TVLibraryMembershipRequest(identities)),
        authenticated = true,
    )

    suspend fun addLibraryTitle(titleId: UUID): LibraryItem = request(
        path = "library/$titleId",
        method = "PUT",
        authenticated = true,
    )

    suspend fun removeLibraryTitle(titleId: UUID) = requestUnit(
        path = "library/$titleId",
        method = "DELETE",
        authenticated = true,
    )

    suspend fun sessionNotifications(after: String? = null): List<SessionNotification> = request<SessionNotificationList>(
        path = "auth/notifications",
        query = mapOf("after" to after),
        authenticated = true,
    ).notifications

    suspend fun acknowledgeSessionNotification(notificationId: String) = requestUnit(
        path = "auth/notifications/${encodePathSegment(notificationId)}",
        method = "DELETE",
        authenticated = true,
    )

    fun resolveResponseResourceUrl(value: String): HttpUrl? {
        value.toHttpUrlOrNull()?.let { absolute ->
            return absolute.takeIf(::isCredentialTransportAllowed)
        }
        return credentialIssuer.toHttpUrlOrNull()
            ?.resolve(value)
            ?.takeIf { canonicalOrigin(it) == credentialIssuer }
    }

    fun resolveResponseArtworkUrl(value: String): HttpUrl? {
        if (value.startsWith("//")) return null
        val issuer = credentialIssuer.toHttpUrlOrNull() ?: return null
        val resolved = value.toHttpUrlOrNull() ?: issuer.resolve(value) ?: return null
        return resolved.takeIf {
            it.username.isEmpty() &&
                it.password.isEmpty() &&
                canonicalOrigin(it) == credentialIssuer &&
                isCredentialTransportAllowed(it)
        }
    }

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

    suspend fun preparePlayback(
        sourceRef: String,
        startSeconds: Int? = null,
        externalPlayer: Boolean = false,
    ): PlaybackPreparation = request(
        path = "playback/prepare",
        method = "POST",
        body = requestJson.encodeToString(PlaybackPrepareRequest(sourceRef, startSeconds, externalPlayer)),
        authenticated = true,
        client = mediaPreparationHttpClient,
    )

    suspend fun resolvePlayback(
        sourceRef: String,
        titleId: String? = null,
        preferredAudioTrack: Int? = null,
        preferredSubtitleId: String? = null,
        startSeconds: Int? = null,
        externalPlayer: Boolean = false,
    ): PlaybackSession = request(
        path = "playback/resolve",
        method = "POST",
        body = requestJson.encodeToString(PlaybackResolveRequest(sourceRef, titleId, preferredAudioTrack, preferredSubtitleId, startSeconds, externalPlayer)),
        authenticated = true,
        client = mediaPreparationHttpClient,
    )

    suspend fun stopPlayback(sessionId: UUID) = requestUnit("playback/sessions/$sessionId", "DELETE", authenticated = true)
    suspend fun updatePlaybackDevice(input: PlaybackDeviceHeartbeatInput): PlaybackDevice = request(
        path = "playback/device", method = "PUT", body = requestJson.encodeToString(input), authenticated = true,
    )

    suspend fun playbackDevices(): PlaybackDeviceList = request("playback/devices", authenticated = true)

    suspend fun sendPlaybackCommand(sessionId: UUID, input: PlaybackCommandInput): PlaybackCommand = request(
        path = "playback/devices/$sessionId/commands", method = "POST", body = requestJson.encodeToString(input), authenticated = true,
    )

    suspend fun playbackCommands(after: UUID? = null): PlaybackCommandList = request(
        path = "playback/commands", query = mapOf("after" to after?.toString()), authenticated = true,
    )

    suspend fun reportPlaybackCommandResult(operationId: UUID, input: PlaybackCommandResultInput): PlaybackCommand = request(
        path = "playback/commands/incoming/$operationId/result", method = "PUT", body = requestJson.encodeToString(input), authenticated = true,
    )

    suspend fun outgoingPlaybackCommand(operationId: UUID): PlaybackCommand = request(
        path = "playback/commands/outgoing/$operationId", authenticated = true,
    )

    suspend fun createPlaybackRoom(input: PlaybackRoomCreateInput): PlaybackRoom = request(
        path = "playback/rooms", method = "POST", body = requestJson.encodeToString(input), authenticated = true,
    )

    suspend fun joinPlaybackRoom(code: String): PlaybackRoom = request(
        path = "playback/rooms/join", method = "POST", body = requestJson.encodeToString(PlaybackRoomJoinInput(code)), authenticated = true,
    )

    suspend fun playbackRoom(id: UUID): PlaybackRoom = request("playback/rooms/$id", authenticated = true)

    suspend fun updatePlaybackRoom(id: UUID, input: PlaybackRoomUpdateInput): PlaybackRoom = request(
        path = "playback/rooms/$id", method = "PUT", body = requestJson.encodeToString(input), authenticated = true,
    )

    suspend fun leavePlaybackRoom(id: UUID) = requestUnit("playback/rooms/$id", "DELETE", authenticated = true)

    suspend fun localRecommendations(
        limit: Int = 20,
        artworkShape: RecommendationArtworkShape? = null,
    ): LocalRecommendationPage = request(
        path = "recommendations",
        query = buildMap {
            put("limit", limit.toString())
            artworkShape?.let { put("artworkShape", it.name.lowercase()) }
        },
        authenticated = true,
    )


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

    suspend fun readingQueue(profileId: UUID): ReadingQueue = requestStrict(
        path = "profiles/$profileId/queue", authenticated = true,
    )

    suspend fun addReadingQueueItem(profileId: UUID, input: ReadingQueueAddInput): ReadingQueueMutation = requestStrict(
        path = "profiles/$profileId/queue/items", method = "POST", body = requestJson.encodeToString(input), authenticated = true,
    )

    suspend fun reorderReadingQueue(profileId: UUID, input: ReadingQueueReorderInput): ReadingQueueMutation = requestStrict(
        path = "profiles/$profileId/queue/order", method = "PUT", body = requestJson.encodeToString(input), authenticated = true,
    )

    suspend fun updateReadingQueueItem(profileId: UUID, itemId: UUID, input: ReadingQueueUpdateInput): ReadingQueueMutation = requestStrict(
        path = "profiles/$profileId/queue/items/$itemId", method = "PATCH", body = requestJson.encodeToString(input), authenticated = true,
    )

    suspend fun removeReadingQueueItem(profileId: UUID, itemId: UUID, input: ReadingQueueMutationInput): ReadingQueueMutation = requestStrict(
        path = "profiles/$profileId/queue/items/$itemId", method = "DELETE", body = requestJson.encodeToString(input), authenticated = true,
    )

    suspend fun consumeReadingQueueItem(profileId: UUID, itemId: UUID, input: ReadingQueueMutationInput): ReadingQueueMutation = requestStrict(
        path = "profiles/$profileId/queue/items/$itemId/consume", method = "POST", body = requestJson.encodeToString(input), authenticated = true,
    )

    suspend fun createPlaybackFailover(input: PlaybackFailoverCreateInput): PlaybackFailoverState = requestStrict(
        path = "playback/failovers", method = "POST", body = requestJson.encodeToString(input), authenticated = true,
    )

    suspend fun playbackFailover(id: UUID): PlaybackFailoverState = requestStrict("playback/failovers/$id", authenticated = true)

    suspend fun cancelPlaybackFailover(id: UUID) = requestUnit("playback/failovers/$id", "DELETE", authenticated = true)

    suspend fun advancePlaybackFailover(id: UUID, input: PlaybackFailoverAdvanceInput): PlaybackFailoverState = requestStrict(
        path = "playback/failovers/$id/advance", method = "POST", body = requestJson.encodeToString(input), authenticated = true,
    )

    suspend fun savedSearches(): List<SavedSearch> = requestStrict<SavedSearchList>("saved-searches", authenticated = true).savedSearches
    suspend fun createSavedSearch(input: SavedSearchInput): SavedSearch = requestStrict("saved-searches", "POST", body = requestJson.encodeToString(input), authenticated = true)
    suspend fun updateSavedSearch(id: UUID, input: SavedSearchUpdateInput): SavedSearch = requestStrict("saved-searches/$id", "PUT", body = requestJson.encodeToString(input), authenticated = true)
    suspend fun deleteSavedSearch(id: UUID, expectedRevision: Long) = requestUnit("saved-searches/$id", "DELETE", query = mapOf("expectedRevision" to expectedRevision.toString()), authenticated = true)

    suspend fun smartCollections(): List<SmartCollection> = requestStrict<SmartCollectionList>("smart-collections", authenticated = true).smartCollections
    suspend fun createSmartCollection(input: SmartCollectionInput): SmartCollection = requestStrict("smart-collections", "POST", body = requestJson.encodeToString(input), authenticated = true)
    suspend fun updateSmartCollection(id: UUID, input: SmartCollectionUpdateInput): SmartCollection = requestStrict("smart-collections/$id", "PUT", body = requestJson.encodeToString(input), authenticated = true)
    suspend fun deleteSmartCollection(id: UUID, expectedRevision: Long) = requestUnit("smart-collections/$id", "DELETE", query = mapOf("expectedRevision" to expectedRevision.toString()), authenticated = true)
    suspend fun evaluateSmartCollection(id: UUID, page: Int? = null, pageSize: Int? = null): SmartCollectionPage = request(
        "smart-collections/$id/items", query = mapOf("page" to page?.toString(), "pageSize" to pageSize?.toString()), authenticated = true,
    )

    suspend fun extensionIncidents(): List<AddonIncident> = requestStrict<AddonIncidentList>("operations/extension-incidents", authenticated = true).incidents
    suspend fun extensionIncident(id: UUID): AddonIncidentDetail = requestStrict("operations/extension-incidents/$id", authenticated = true)
    suspend fun acknowledgeExtensionIncident(id: UUID): AddonIncident = requestStrict("operations/extension-incidents/$id/acknowledgement", "POST", authenticated = true)

    suspend fun mediaNotificationSubscriptions(): List<MediaNotificationSubscription> = requestStrict<MediaNotificationSubscriptions>("media-notification-subscriptions", authenticated = true).subscriptions
    suspend fun followMediaNotifications(titleId: UUID, input: MediaNotificationFollowInput): MediaNotificationSubscription = requestStrict(
        "media-notification-subscriptions/$titleId", "PUT", body = requestJson.encodeToString(input), authenticated = true,
    )
    suspend fun unfollowMediaNotifications(titleId: UUID) = requestUnit("media-notification-subscriptions/$titleId", "DELETE", authenticated = true)
    suspend fun mediaNotifications(cursor: String? = null, limit: Int? = null): MediaNotificationPage = requestStrict(
        "media-notifications", query = mapOf("cursor" to cursor, "limit" to limit?.toString()), authenticated = true,
    )
    suspend fun acknowledgeMediaNotification(id: String, state: MediaNotificationAcknowledgementState) = requestUnit(
        "media-notifications/${encodePathSegment(id)}/acknowledgement", "POST",
        body = requestJson.encodeToString(MediaNotificationAcknowledgement(state)), authenticated = true,
    )

    suspend fun profileAccessibilityPreferences(profileId: UUID): AccessibilityPreferencesDocument = requestStrict(
        "profiles/$profileId/accessibility-preferences", authenticated = true,
    )
    suspend fun updateProfileAccessibilityPreferences(profileId: UUID, input: AccessibilityPreferencesDocument): AccessibilityPreferencesDocument = requestStrict(
        "profiles/$profileId/accessibility-preferences", "PUT", body = requestJson.encodeToString(input), authenticated = true,
    )

    private suspend inline fun <reified Response> request(
        path: String,
        method: String = "GET",
        query: Map<String, String?> = emptyMap(),
        body: String? = null,
        authenticated: Boolean,
        expectedProfileMutation: AuthenticationState? = null,
        client: OkHttpClient = httpClient,
    ): Response {
        val url = endpoint(path, query)
        return execute(url, method, body, authenticated, retryAfterRefresh = authenticated, expectedProfileMutation = expectedProfileMutation, client = client)
    }

    private suspend inline fun <reified Response : StrictV22Response> requestStrict(
        path: String,
        method: String = "GET",
        query: Map<String, String?> = emptyMap(),
        body: String? = null,
        authenticated: Boolean,
    ): Response {
        val data = executeData(endpoint(path, query), method, body, authenticated, retryAfterRefresh = authenticated)
        return try {
            strictV22Json.decodeFromString<Response>(data.body)
        } catch (cause: Exception) {
            throw RivuneApiException.InvalidResponse(cause)
        }
    }

    private suspend inline fun <reified Response> requestWithQueryItems(
        path: String,
        method: String = "GET",
        query: List<Pair<String, String>> = emptyList(),
        body: String? = null,
        authenticated: Boolean,
        client: OkHttpClient = httpClient,
    ): Response {
        val url = endpoint(path, query)
        return execute(url, method, body, authenticated, retryAfterRefresh = authenticated, client = client)
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
        expectedProfileMutation: AuthenticationState? = null,
    ) {
        val url = endpoint(path, query)
        executeData(url, method, body, authenticated, retryAfterRefresh = authenticated, expectedProfileMutation = expectedProfileMutation)
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

    private suspend fun endpoint(path: String, query: List<Pair<String, String>>): HttpUrl {
        var base = authenticationMutex.withLock { apiBaseUrl }
        if (base == null) {
            discover()
            base = authenticationMutex.withLock { apiBaseUrl }
        }
        val resolvedBase = base ?: throw RivuneApiException.InvalidResponse()
        return resolvedBase.newBuilder().apply {
            path.split('/').filter { it.isNotEmpty() }.forEach(::addEncodedPathSegment)
            query.forEach { (name, value) -> addQueryParameter(name, value) }
        }.build()
    }

    private suspend inline fun <reified Response> execute(
        url: HttpUrl,
        method: String,
        body: String?,
        authenticated: Boolean,
        retryAfterRefresh: Boolean,
        expectedProfileMutation: AuthenticationState? = null,
        client: OkHttpClient = httpClient,
    ): Response {
        val data = executeData(url, method, body, authenticated, retryAfterRefresh, expectedProfileMutation = expectedProfileMutation, client = client)
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
        expectedAuthentication: AuthenticationSnapshot? = null,
        expectedProfileMutation: AuthenticationState? = null,
        requestCancellationJob: Job? = null,
        client: OkHttpClient = httpClient,
    ): ResponseData {
        requireServerDestination(url)
        val authentication = expectedAuthentication ?: if (authenticated) {
            loadCredentialsIfNeeded()
            authenticationSnapshot(expectedProfileMutation)
        } else {
            null
        }
        if (authenticated && expectedAuthentication != null) requireProfileMutationState(expectedProfileMutation)
        val accessToken = explicitAccessToken ?: authentication?.accessToken
        val request = Request.Builder()
            .url(url)
            .header("Accept", "application/json")
            .apply {
                if (accessToken != null) header("Authorization", "Bearer $accessToken")
                authentication?.profileContext?.takeIf { usesProfileContext(url, method) }
                    ?.let { header("X-Rivune-Profile-Context", it) }
                val requestBody = body?.toRequestBody(JSON_MEDIA_TYPE)
                    ?: if (method == "POST" || method == "PUT" || method == "PATCH") ByteArray(0).toRequestBody(JSON_MEDIA_TYPE) else null
                method(method, requestBody)
            }
            .build()

        val responseData = try {
            withContext(Dispatchers.IO) {
                requestCancellationJob?.ensureActive()
                client.newCall(request).execute().use { response ->
                    requireServerDestination(response.request.url)
                    if (response.code in 300..399) throw decodeServerError(response.code, "", response.header("Retry-After"))
                    ResponseData(response.code, readResponseBody(response.body), response.header("Retry-After"))
                }
            }
        } catch (cause: IOException) {
            authentication?.let { requireAuthenticationState(it) }
            throw cause
        }
        val responseCode = responseData.status
        val responseSuccessful = responseCode in 200..299
        val responseBody = responseData.body
        authentication?.let { requireAuthenticationState(it) }
        if (responseCode == 401 && authentication != null && retryAfterRefresh) {
            val refreshed = refreshCredentials(accessToken, authentication)
            requireAuthenticationState(authentication)
            return executeData(
                url,
                method,
                body,
                authenticated = true,
                retryAfterRefresh = false,
                expectedAuthentication = authentication.copy(tokens = refreshed),
                expectedProfileMutation = expectedProfileMutation,
                requestCancellationJob = requestCancellationJob,
                client = client,
            )
        }
        if (!responseSuccessful) throw decodeServerError(responseCode, responseBody, responseData.retryAfter)
        return responseData
    }

    private suspend fun executeBytes(
        url: HttpUrl,
        authenticated: Boolean,
        retryAfterRefresh: Boolean,
        expectedAuthentication: AuthenticationSnapshot? = null,
    ): ByteArray {
        requireServerDestination(url)
        val authentication = expectedAuthentication ?: if (authenticated) {
            loadCredentialsIfNeeded()
            authenticationSnapshot()
        } else {
            null
        }
        val request = Request.Builder()
            .url(url)
            .header("Accept", "image/*")
            .apply {
                authentication?.accessToken?.let { header("Authorization", "Bearer $it") }
            }
            .build()
        val responseData: Pair<Int, ByteArray> = try {
            withContext(Dispatchers.IO) {
                httpClient.newCall(request).execute().use { response ->
                    requireServerDestination(response.request.url)
                    if (response.code in 300..399) throw decodeServerError(response.code, "")
                    val body = response.body
                    if (body.contentLength() > MAX_PROFILE_AVATAR_BYTES) {
                        throw RivuneApiException.ResponseTooLarge("2 MiB")
                    }
                    val bytes = readBoundedBytes(body, MAX_PROFILE_AVATAR_BYTES)
                    response.code to bytes
                }
            }
        } catch (cause: IOException) {
            authentication?.let { requireCurrentAuthenticationGeneration(it.generation) }
            throw cause
        }
        authentication?.let { requireCurrentAuthenticationGeneration(it.generation) }
        if (responseData.first == 401 && authentication != null && retryAfterRefresh) {
            val refreshed = refreshCredentials(authentication.accessToken, authentication, requireProfileContext = false)
            requireCurrentAuthenticationGeneration(authentication.generation)
            return executeBytes(
                url,
                authenticated = true,
                retryAfterRefresh = false,
                expectedAuthentication = authentication.copy(tokens = refreshed),
            )
        }
        if (responseData.first !in 200..299) {
            throw decodeServerError(responseData.first, responseData.second.toString(StandardCharsets.UTF_8))
        }
        return responseData.second
    }

    private fun readBoundedBytes(body: ResponseBody, maximumBytes: Long): ByteArray {
        val source = body.source()
        val bufferedBody = Buffer()
        var remaining = maximumBytes
        while (remaining > 0L) {
            val read = source.read(bufferedBody, remaining)
            if (read == -1L) return bufferedBody.readByteArray()
            remaining -= read
        }
        if (!source.exhausted()) throw RivuneApiException.ResponseTooLarge("2 MiB")
        return bufferedBody.readByteArray()
    }

    private data class ResponseData(val status: Int, val body: String, val retryAfter: String?)

    private fun readResponseBody(body: ResponseBody): String {
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
        expectedAuthentication: AuthenticationSnapshot,
        requireProfileContext: Boolean = true,
    ): TokenPair = refreshMutex.withLock {
        val expectedGeneration = expectedAuthentication.generation
        if (requireProfileContext) {
            requireAuthenticationState(expectedAuthentication)
        } else {
            requireCurrentAuthenticationGeneration(expectedGeneration)
        }
        val snapshot = authenticationMutex.withLock {
            requireAuthenticationGeneration(expectedGeneration)
            if (requireProfileContext) {
                requireProfileContextGeneration(expectedAuthentication.profileContextGeneration)
            }
            AuthenticationSnapshot(
                generation = expectedGeneration,
                tokens = credentials ?: throw RivuneApiException.NotAuthenticated(),
                profileContext = profileContext,
                profileContextGeneration = profileContextGeneration,
            )
        }
        if (failedAccessToken != null && snapshot.accessToken != failedAccessToken) {
            return@withLock snapshot.tokens
        }
        val refreshToken = snapshot.tokens.refreshToken
        val url = endpoint("auth/refresh", emptyMap())
        if (requireProfileContext) {
            requireAuthenticationState(expectedAuthentication)
        } else {
            requireCurrentAuthenticationGeneration(expectedGeneration)
        }
        requireServerDestination(url)
        val result = try {
            val refreshed: TokenPair = execute(
                url = url,
                method = "POST",
                body = requestJson.encodeToString(RefreshRequest(refreshToken)),
                authenticated = false,
                retryAfterRefresh = false,
            )
            setCredentials(refreshed, expectedGeneration)
            refreshed
        } catch (cause: Exception) {
            if (cause is RivuneApiException.Server &&
                cause.status == 401 && cause.code == "invalid_refresh_token"
            ) {
                clearCredentialsAfterRefreshFailure(expectedGeneration, refreshToken)
            }
            throw cause
        }
        if (requireProfileContext) {
            requireAuthenticationState(expectedAuthentication)
        } else {
            requireCurrentAuthenticationGeneration(expectedGeneration)
        }
        result
    }


    private suspend fun setCredentials(
        value: TokenPair,
        expectedGeneration: Long,
    ) = authenticationMutex.withLock {
        requireAuthenticationGeneration(expectedGeneration)
        val saved = credentialStore.save(
            StoredCredentials(credentialIssuer, value, profileContext),
            expectedGeneration,
        )
        if (!saved) throw staleAuthentication()
        requireAuthenticationGeneration(expectedGeneration)
        credentials = value
        credentialsLoaded = true
    }

    private suspend fun commitProfileContext(value: String?, expectedState: AuthenticationState) =
        withContext(NonCancellable) {
            authenticationMutex.withLock {
                requireAuthenticationState(expectedState)
                val currentCredentials = credentials ?: throw RivuneApiException.NotAuthenticated()
                val saved = credentialStore.save(
                    StoredCredentials(credentialIssuer, currentCredentials, value),
                    expectedState.authenticationGeneration,
                )
                if (!saved) throw staleAuthentication()
                requireAuthenticationState(expectedState)
                profileContext = value
                profileContextGeneration += 1
            }
        }

    private suspend fun loadCredentialsIfNeeded() {
        val generation = authenticationMutex.withLock {
            if (credentialsLoaded) return
            authenticationGeneration
        }
        val stored = credentialStore.load(credentialIssuer, generation)
        val matching = stored?.takeIf { it.issuer == credentialIssuer }
        val restored = matching?.tokens
        if (stored != null && matching == null) {
            runCatching { credentialStore.clear(credentialIssuer, generation) }
        }
        authenticationMutex.withLock {
            requireAuthenticationGeneration(generation)
            if (!credentialsLoaded) {
                credentials = restored
                credentialsLoaded = true
                if (profileContext != matching?.profileContext) {
                    profileContext = matching?.profileContext
                    profileContextGeneration += 1
                }
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
                profileContext = null
                profileContextGeneration += 1
                profileContextMutationInFlight = false
                true
            }
        }
        if (shouldClear) runCatching { credentialStore.clear(credentialIssuer, expectedGeneration) }
    }
    private suspend fun beginCredentialReplacement(): Long = withContext(NonCancellable) {
        authenticationMutex.withLock {
            authenticationGeneration += 1
            val generation = authenticationGeneration
            val capturedCredentials = credentials
            credentials = null
            credentialsLoaded = true
            profileContext = null
            profileContextGeneration += 1
            profileContextMutationInFlight = false
            val cleanup = credentialStore.invalidateAndClear(
                issuer = credentialIssuer,
                newGeneration = generation,
                capturedCredentials = capturedCredentials,
            )
            cleanup.error?.let { throw it }
            generation
        }
    }


    private suspend fun currentAuthenticationGeneration(): Long =
        authenticationMutex.withLock { authenticationGeneration }

    private suspend fun authenticationSnapshot(expectedProfileMutation: AuthenticationState? = null): AuthenticationSnapshot =
        authenticationMutex.withLock {
            requireProfileMutationStateLocked(expectedProfileMutation)
            AuthenticationSnapshot(
                generation = authenticationGeneration,
                tokens = credentials ?: throw RivuneApiException.NotAuthenticated(),
                profileContext = profileContext,
                profileContextGeneration = profileContextGeneration,
            )
        }


    private suspend fun reserveProfileMutation(): AuthenticationState =
        authenticationMutex.withLock {
            profileContextGeneration += 1
            profileContextMutationInFlight = true
            AuthenticationState(authenticationGeneration, profileContextGeneration, true)
        }

    private suspend fun finishProfileMutation() = withContext(NonCancellable) {
        authenticationMutex.withLock { profileContextMutationInFlight = false }
    }
    private suspend fun requireCurrentAuthenticationGeneration(expectedGeneration: Long) {
        authenticationMutex.withLock { requireAuthenticationGeneration(expectedGeneration) }
    }


    private suspend fun requireProfileMutationState(expected: AuthenticationState?) {
        authenticationMutex.withLock { requireProfileMutationStateLocked(expected) }
    }

    private fun requireProfileMutationStateLocked(expected: AuthenticationState?) {
        val state = AuthenticationState(authenticationGeneration, profileContextGeneration, profileContextMutationInFlight)
        if (state.profileContextMutationInFlight && state != expected) throw staleAuthentication()
        if (expected != null && state != expected) throw staleAuthentication()
    }

    private suspend fun requireAuthenticationState(expected: AuthenticationSnapshot) {
        authenticationMutex.withLock {
            if (authenticationGeneration != expected.generation ||
                profileContextGeneration != expected.profileContextGeneration
            ) throw staleAuthentication()
        }
    }

    private fun requireAuthenticationState(expected: AuthenticationState) {
        requireAuthenticationGeneration(expected.authenticationGeneration)
        requireProfileContextGeneration(expected.profileContextGeneration)
    }

    private fun requireProfileContextGeneration(expectedGeneration: Long) {
        if (profileContextGeneration != expectedGeneration) throw staleAuthentication()
    }

    private fun requireAuthenticationGeneration(expectedGeneration: Long) {
        if (authenticationGeneration != expectedGeneration) throw staleAuthentication()
    }

    private fun staleAuthentication() =
        CancellationException("Authentication state changed")

    private data class AuthenticationSnapshot(
        val generation: Long,
        val tokens: TokenPair,
        val profileContext: String?,
        val profileContextGeneration: Long,
    ) {
        val accessToken: String
            get() = tokens.accessToken
    }

    private data class AuthenticationState(
        val authenticationGeneration: Long,
        val profileContextGeneration: Long,
        val profileContextMutationInFlight: Boolean = false,
    )

    private fun usesProfileContext(url: HttpUrl, method: String): Boolean {
        val path = url.encodedPath
        if (path.endsWith("/auth/logout") || path.endsWith("/auth/me")) return false
        if (method == "GET" && path.endsWith("/profiles")) return false
        if (method == "GET" && path.contains("/profiles/") && path.endsWith("/avatar")) return false
        if (method == "DELETE" && path.endsWith("/profiles/selection")) return false
        if (method == "POST" && path.contains("/profiles/") && path.endsWith("/select")) return false
        return true
    }

    private fun requireServerDestination(url: HttpUrl) {
        if (!isCredentialTransportAllowed(url) || canonicalOrigin(url) != credentialIssuer) {
            throw RivuneApiException.InvalidServerUrl(url.toString())
        }
    }

    private fun decodeServerError(status: Int, body: String, retryAfter: String? = null): RivuneApiException.Server {
        val error = runCatching { json.decodeFromString<ErrorEnvelope>(body).error }.getOrNull()
        return RivuneApiException.Server(
            status = status,
            code = error?.code ?: "http_$status",
            message = error?.message ?: "Rivune server returned HTTP $status",
            retryAfterSeconds = retryAfter?.trim()?.toLongOrNull()?.takeIf { it > 0 },
        )
    }

    private fun encodePathSegment(value: String): String = HttpUrl.Builder()
        .scheme("https")
        .host("localhost")
        .addPathSegment(value)
        .build()
        .encodedPathSegments
        .last()


    private fun boundedProfileArchiveBody(archive: kotlinx.serialization.json.JsonObject): String =
        validateProfileArchive(archive).toString().also(::requireProfileArchiveSize)

    private fun validateProfileArchive(archive: kotlinx.serialization.json.JsonObject): kotlinx.serialization.json.JsonObject {
        require(archive["version"]?.jsonPrimitive?.intOrNull == 2) { "Unsupported profile archive version" }
        require(archive["identity"] is kotlinx.serialization.json.JsonObject) { "Profile archive identity is missing" }
        require(archive["continueDismissals"] is kotlinx.serialization.json.JsonArray) { "Profile archive continue dismissals are missing" }
        return archive
    }

    private fun requireProfileArchiveSize(value: String) {
        require(value.toByteArray(StandardCharsets.UTF_8).size <= MAX_RESPONSE_BODY_BYTES) { "Profile archive exceeds 16 MiB" }
    }
    private fun profileSettingsUpdateBody(input: ProfileSettingsUpdate): String = buildJsonObject {
        putPatch("maximumResolution", input.maximumResolution) { name, value -> put(name, value) }
        putPatch("preferDirectPlay", input.preferDirectPlay) { name, value -> put(name, value) }
        putPatch("audioLanguage", input.audioLanguage) { name, value -> put(name, value) }
        putPatch("metadataLanguage", input.metadataLanguage) { name, value -> put(name, value) }
        putPatch("subtitleLanguage", input.subtitleLanguage) { name, value -> put(name, value) }
        putPatch("forcedSubtitleLanguage", input.forcedSubtitleLanguage) { name, value -> put(name, value) }
        putPatch("autoplayNextEpisode", input.autoplayNextEpisode) { name, value -> put(name, value) }
        putPatch("skipIntroEnabled", input.skipIntroEnabled) { name, value -> put(name, value) }
        putPatch("skipRecapEnabled", input.skipRecapEnabled) { name, value -> put(name, value) }
        putPatch("skipOutroEnabled", input.skipOutroEnabled) { name, value -> put(name, value) }
        putPatch("transcoding", input.transcoding) { name, value -> put(name, value) }
    }.toString()

    private fun categoryUpdateBody(input: CategoryUpdateRequest): String = buildJsonObject {
        input.name?.let { put("name", it) }
        putPatch("description", input.description) { name, value -> put(name, value) }
        putPatch("color", input.color) { name, value -> put(name, value) }
        putPatch("icon", input.icon) { name, value -> put(name, value) }
        input.isDefault?.let { put("isDefault", it) }
    }.toString()

    private fun deviceUpdateBody(input: DeviceUpdateRequest): String = buildJsonObject {
        input.name?.let { put("name", it) }
        input.categoryId?.let { put("categoryId", it.toString()) }
        putPatch("internalNote", input.internalNote) { name, value -> put(name, value) }
    }.toString()

    private inline fun <T> kotlinx.serialization.json.JsonObjectBuilder.putPatch(
        name: String,
        field: PatchField<T>,
        putValue: kotlinx.serialization.json.JsonObjectBuilder.(String, T) -> Unit,
    ) {
        when (field) {
            PatchField.Omitted -> Unit
            PatchField.Null -> put(name, JsonNull)
            is PatchField.Value -> putValue(name, field.value)
        }
    }
    private fun OkHttpClient.secureBuilder() = newBuilder()
        .followRedirects(false)
        .followSslRedirects(false)

    private companion object {
        val JSON_MEDIA_TYPE = "application/json; charset=utf-8".toMediaType()
        const val MAX_RESPONSE_BODY_BYTES = 16L * 1024L * 1024L
        const val MAX_PROFILE_AVATAR_BYTES = 2L * 1024L * 1024L
        const val COLLECTION_ARTWORK_TIMEOUT_SECONDS = 10L
        const val MEDIA_PREPARATION_TIMEOUT_SECONDS = 180L
        const val SEMANTIC_SEARCH_TIMEOUT_MILLISECONDS = 12_000L
    }
}
