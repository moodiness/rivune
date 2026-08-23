package io.rivune.api

import java.io.Closeable
import java.io.IOException
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.async
import kotlinx.coroutines.runBlocking
import okhttp3.Interceptor
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.Response
import okhttp3.ResponseBody.Companion.toResponseBody
import okhttp3.mockwebserver.MockWebServer

class AuthenticationLifecycleTest {
    @Test
    fun logoutDuringRefreshRejectsLateCredentials() = runBlocking {
        lifecycleFixture(initialTokens = tokenPair("old")).use { fixture ->
            fixture.client.discover()
            assertTrue(fixture.client.restoreSession())
            val refreshGate = fixture.transport.blockNext("/api/v1/auth/refresh")
            val refresh = async(start = CoroutineStart.UNDISPATCHED) { fixture.client.refreshSession() }
            refreshGate.awaitRequest()

            fixture.client.logout()
            assertNull(fixture.store.credentials)
            refreshGate.release()

            assertFailsWith<CancellationException> { refresh.await() }
            assertNull(fixture.store.credentials)
            assertFalse(fixture.client.restoreSession())
            assertFalse(fixture.store.savedAccessTokens.contains("refreshed-access"))
        }
    }

    @Test
    fun transientRefreshFailurePreservesCredentialsForRelaunch() = runBlocking {
        lifecycleFixture(initialTokens = tokenPair("old")).use { fixture ->
            fixture.client.discover()
            assertTrue(fixture.client.restoreSession())
            fixture.transport.refreshResponse = 503 to
                """{"error":{"code":"unavailable","message":"Unavailable"}}"""

            val failure = assertFailsWith<RivuneApiException.Server> { fixture.client.refreshSession() }

            assertEquals(503, failure.status)
            assertEquals("old-access", fixture.store.credentials?.tokens?.accessToken)
            assertTrue(fixture.reconstructedClient().restoreSession())
        }
    }

    @Test
    fun invalidRefreshTokenClearsCredentialsForRelaunch() = runBlocking {
        lifecycleFixture(initialTokens = tokenPair("old")).use { fixture ->
            fixture.client.discover()
            assertTrue(fixture.client.restoreSession())
            fixture.transport.refreshResponse = 401 to
                """{"error":{"code":"invalid_refresh_token","message":"Expired"}}"""

            val failure = assertFailsWith<RivuneApiException.Server> { fixture.client.refreshSession() }

            assertEquals("invalid_refresh_token", failure.code)
            assertNull(fixture.store.credentials)
            assertFalse(fixture.reconstructedClient().restoreSession())
        }
    }

    @Test
    fun logoutDuringLoginRejectsLateResponseAndAllowsNewAuthentication() = runBlocking {
        lifecycleFixture().use { fixture ->
            fixture.client.discover()
            val loginGate = fixture.transport.blockNext("/api/v1/auth/login")
            val staleLogin = async(start = CoroutineStart.UNDISPATCHED) {
                fixture.client.login("old-user", "password", testDevice())
            }
            loginGate.awaitRequest()

            fixture.client.logout()
            loginGate.release()

            assertFailsWith<CancellationException> { staleLogin.await() }
            assertNull(fixture.store.credentials)
            val current = fixture.client.login("new-user", "password", testDevice())
            assertEquals("login-2-access", current.accessToken)
            assertEquals("login-2-access", fixture.store.credentials?.tokens?.accessToken)
            assertEquals(listOf("login-2-access"), fixture.store.savedAccessTokens)
        }
    }

    @Test
    fun logoutDuringDiscoveryStopsThePendingLoginBeforeCredentialsAreIssued() = runBlocking {
        lifecycleFixture().use { fixture ->
            val discoveryGate = fixture.transport.blockNext("/.well-known/rivune")
            val staleLogin = async(start = CoroutineStart.UNDISPATCHED) {
                fixture.client.login("old-user", "password", testDevice())
            }
            discoveryGate.awaitRequest()

            fixture.client.logout()
            discoveryGate.release()

            assertFailsWith<CancellationException> { staleLogin.await() }
            assertEquals(0, fixture.transport.requestCount("/api/v1/auth/login"))
            assertNull(fixture.store.credentials)

            val current = fixture.client.login("new-user", "password", testDevice())
            assertEquals("login-1-access", current.accessToken)
            assertEquals("login-1-access", fixture.store.credentials?.tokens?.accessToken)
        }
    }

    @Test
    fun logoutDuringDeviceExchangeRejectsLateCredentials() = runBlocking {
        lifecycleFixture().use { fixture ->
            fixture.client.discover()
            val exchangeGate = fixture.transport.blockNext("/api/v1/auth/device-code/token")
            val exchange = async(start = CoroutineStart.UNDISPATCHED) { fixture.client.exchangeDeviceAuthorization("device-code") }
            exchangeGate.awaitRequest()

            fixture.client.logout()
            exchangeGate.release()

            assertFailsWith<CancellationException> { exchange.await() }
            assertNull(fixture.store.credentials)
            assertFalse(fixture.store.savedAccessTokens.contains("device-access"))
        }
    }

    @Test
    fun logoutNetworkFailureDoesNotRestoreClearedCredentials() = runBlocking {
        lifecycleFixture(initialTokens = tokenPair("old")).use { fixture ->
            fixture.client.discover()
            assertTrue(fixture.client.restoreSession())
            fixture.transport.logoutFailure = IOException("logout unavailable")

            assertFailsWith<IOException> { fixture.client.logout() }

            assertNull(fixture.store.credentials)
            assertFalse(fixture.client.restoreSession())
            val logout = fixture.transport.singleRequest("/api/v1/auth/logout")
            assertEquals("Bearer old-access", logout.authorization)
        }
    }

    @Test
    fun cancelledLogoutCompletesLocalClearBeforeTheBlockedNetworkCallEnds() = runBlocking {
        lifecycleFixture(initialTokens = tokenPair("old")).use { fixture ->
            fixture.client.discover()
            assertTrue(fixture.client.restoreSession())
            val logoutGate = fixture.transport.blockNext("/api/v1/auth/logout")
            val logout = async(start = CoroutineStart.UNDISPATCHED) { fixture.client.logout() }
            logoutGate.awaitRequest()

            assertNull(fixture.store.credentials)
            logout.cancel()
            logoutGate.release()

            assertFailsWith<CancellationException> { logout.await() }
            assertNull(fixture.store.credentials)
            assertFalse(fixture.client.restoreSession())
        }
    }

    @Test
    fun failedReplacementLoginLeavesNoMixedAuthenticationState() = runBlocking {
        lifecycleFixture(initialTokens = tokenPair("old")).use { fixture ->
            fixture.client.discover()
            assertTrue(fixture.client.restoreSession())
            fixture.transport.loginFailure = IOException("login unavailable")

            assertFailsWith<IOException> {
                fixture.client.login("new-user", "password", testDevice())
            }

            assertNull(fixture.store.credentials)
            assertFalse(fixture.client.restoreSession())
        }
    }

    @Test
    fun authenticatedResponseCannotCrossProfileClear() = runBlocking {
        lifecycleFixture(initialTokens = tokenPair("old")).use { fixture ->
            val profileId = UUID.fromString("44444444-4444-4444-8444-444444444444")
            fixture.client.discover()
            assertTrue(fixture.client.restoreSession())
            fixture.client.selectProfile(profileId)
            fixture.transport.failNextCollectionsWithUnauthorized()
            val collectionGate = fixture.transport.blockNext("/api/v1/collections")
            val staleRequest = async(start = CoroutineStart.UNDISPATCHED) { fixture.client.collections() }
            collectionGate.awaitRequest()

            fixture.client.clearProfileSelection()
            collectionGate.release()

            assertFailsWith<CancellationException> { staleRequest.await() }
            assertEquals(0, fixture.transport.requestCount("/api/v1/auth/refresh"))
        }
    }

    @Test
    fun cancellationDuringProfileRequestPreparationDoesNotSendMutation() = runBlocking {
        lifecycleFixture(initialTokens = tokenPair("old")).use { fixture ->
            val profileId = UUID.fromString("44444444-4444-4444-8444-444444444444")
            val discoveryGate = fixture.transport.blockNext("/.well-known/rivune")
            val selection = async(start = CoroutineStart.UNDISPATCHED) { fixture.client.selectProfile(profileId) }
            discoveryGate.awaitRequest()

            selection.cancel()
            discoveryGate.release()

            assertFailsWith<CancellationException> { selection.await() }
            assertEquals(0, fixture.transport.requestCount("/api/v1/profiles/$profileId/select"))
            assertNull(fixture.store.credentials?.profileContext)
        }
    }

    @Test
    fun cancelledSelectResponseReconcilesContextBeforeReportingCancellation() = runBlocking {
        lifecycleFixture(initialTokens = tokenPair("old")).use { fixture ->
            val profileId = UUID.fromString("44444444-4444-4444-8444-444444444444")
            fixture.client.discover()
            assertTrue(fixture.client.restoreSession())

            lateinit var selection: kotlinx.coroutines.Deferred<ProfileSelection>
            fixture.transport.onNextResponse("/api/v1/profiles/$profileId/select") { selection.cancel() }
            selection = async(start = CoroutineStart.LAZY) { fixture.client.selectProfile(profileId) }
            selection.start()
            selection.join()

            assertFailsWith<CancellationException> { selection.await() }
            assertEquals("context-one", fixture.store.credentials?.profileContext)
            fixture.client.collections()
            assertEquals("context-one", fixture.transport.lastRequest("/api/v1/collections").profileContext)
        }
    }

    @Test
    fun cancelledClearResponseReconcilesContextBeforeReportingCancellation() = runBlocking {
        lifecycleFixture(initialTokens = tokenPair("old")).use { fixture ->
            val profileId = UUID.fromString("44444444-4444-4444-8444-444444444444")
            fixture.client.discover()
            assertTrue(fixture.client.restoreSession())
            fixture.client.selectProfile(profileId)

            lateinit var clear: kotlinx.coroutines.Deferred<Unit>
            fixture.transport.onNextResponse("/api/v1/profiles/selection") { clear.cancel() }
            clear = async(start = CoroutineStart.LAZY) { fixture.client.clearProfileSelection() }
            clear.start()
            clear.join()

            assertFailsWith<CancellationException> { clear.await() }
            assertNull(fixture.store.credentials?.profileContext)
            fixture.client.collections()
            assertNull(fixture.transport.lastRequest("/api/v1/collections").profileContext)
        }
    }

    @Test
    fun credentialReplacementRejectsProfileResponseFromEarlierEpoch() = runBlocking {
        lifecycleFixture(initialTokens = tokenPair("old")).use { fixture ->
            val profileId = UUID.fromString("44444444-4444-4444-8444-444444444444")
            fixture.client.discover()
            assertTrue(fixture.client.restoreSession())
            val selectionGate = fixture.transport.blockNext("/api/v1/profiles/$profileId/select")
            val staleSelection = async(start = CoroutineStart.UNDISPATCHED) { fixture.client.selectProfile(profileId) }
            selectionGate.awaitRequest()

            fixture.client.logout()
            fixture.client.login("new-user", "password", testDevice())
            selectionGate.release()

            assertFailsWith<CancellationException> { staleSelection.await() }
            assertEquals("login-1-access", fixture.store.credentials?.tokens?.accessToken)
            assertNull(fixture.store.credentials?.profileContext)
            fixture.client.collections()
            assertNull(fixture.transport.lastRequest("/api/v1/collections").profileContext)
        }
    }

    @Test
    fun profileMutationsReachServerInCallOrder() = runBlocking {
        lifecycleFixture(initialTokens = tokenPair("old")).use { fixture ->
            val profileId = UUID.fromString("44444444-4444-4444-8444-444444444444")
            fixture.client.discover()
            assertTrue(fixture.client.restoreSession())
            fixture.client.selectProfile(profileId)
            val clearGate = fixture.transport.blockNext("/api/v1/profiles/selection")
            val clear = async(start = CoroutineStart.UNDISPATCHED) { fixture.client.clearProfileSelection() }
            clearGate.awaitRequest()

            val reselection = async(start = CoroutineStart.UNDISPATCHED) { fixture.client.selectProfile(profileId) }
            assertEquals(1, fixture.transport.requestCount("/api/v1/profiles/$profileId/select"))
            clearGate.release()

            clear.await()
            reselection.await()
            assertEquals(2, fixture.transport.requestCount("/api/v1/profiles/$profileId/select"))
        }
    }

    @Test
    fun browseRequestDoesNotStartDuringProfileMutation() = runBlocking {
        lifecycleFixture(initialTokens = tokenPair("old")).use { fixture ->
            val profileId = UUID.fromString("44444444-4444-4444-8444-444444444444")
            fixture.client.discover()
            assertTrue(fixture.client.restoreSession())
            fixture.client.selectProfile(profileId)
            val clearGate = fixture.transport.blockNext("/api/v1/profiles/selection")
            val clear = async(start = CoroutineStart.UNDISPATCHED) { fixture.client.clearProfileSelection() }
            clearGate.awaitRequest()

            assertFailsWith<CancellationException> { fixture.client.collections() }
            assertEquals(0, fixture.transport.requestCount("/api/v1/collections"))
            clearGate.release()
            clear.await()
        }
    }

    @Test
    fun profileContextPersistsAcrossClientReconstructionAndTokenRefresh() = runBlocking {
        lifecycleFixture(initialTokens = tokenPair("old")).use { fixture ->
            val profileId = UUID.fromString("44444444-4444-4444-8444-444444444444")
            fixture.client.discover()
            assertTrue(fixture.client.restoreSession())
            fixture.client.selectProfile(profileId)
            assertEquals("context-one", fixture.store.credentials?.profileContext)

            val restored = fixture.reconstructedClient()
            restored.discover()
            assertTrue(restored.restoreSession())
            restored.collections()
            assertEquals("context-one", fixture.transport.lastRequest("/api/v1/collections").profileContext)

            restored.refreshSession()
            assertEquals("refreshed-access", fixture.store.credentials?.tokens?.accessToken)
            assertEquals("context-one", fixture.store.credentials?.profileContext)

            val refreshed = fixture.reconstructedClient()
            refreshed.discover()
            assertTrue(refreshed.restoreSession())
            refreshed.collections()
            val collection = fixture.transport.lastRequest("/api/v1/collections")
            assertEquals("Bearer refreshed-access", collection.authorization)
            assertEquals("context-one", collection.profileContext)

            refreshed.clearProfileSelection()
            assertNull(fixture.store.credentials?.profileContext)
        }
    }

    @Test
    fun storedCredentialsWithoutProfileContextRemainReadable() {
        val encoded = Json.encodeToString(StoredCredentials("https://media.example.com/", tokenPair("legacy")))
        assertFalse(encoded.contains("profileContext"))
        assertNull(Json.decodeFromString<StoredCredentials>(encoded).profileContext)
    }

    private fun lifecycleFixture(initialTokens: TokenPair? = null): LifecycleFixture {
        val server = MockWebServer().apply { start() }
        val issuer = server.loopbackUrl("/").toString()
        val store = RecordingCredentialStore(initialTokens?.let { StoredCredentials(issuer, it) })
        val transport = BlockingAuthTransport()
        val client = RivuneApiClient(
            serverUrl = issuer,
            credentialStore = store,
            httpClient = OkHttpClient.Builder().addInterceptor(transport).build(),
        )
        return LifecycleFixture(server, store, transport, client)
    }

    private fun testDevice() = LoginDevice(name = "Lifecycle test", platform = "android")
}

private class LifecycleFixture(
    private val server: MockWebServer,
    val store: RecordingCredentialStore,
    val transport: BlockingAuthTransport,
    val client: RivuneApiClient,
) : Closeable {
    override fun close() {
        transport.releaseAll()
        server.shutdown()
    }

    fun reconstructedClient() = RivuneApiClient(
        serverUrl = server.loopbackUrl("/").toString(),
        credentialStore = store,
        httpClient = OkHttpClient.Builder().addInterceptor(transport).build(),
    )
}

private class RecordingCredentialStore(
    initialCredentials: StoredCredentials?,
) : CredentialStore {
    @Volatile
    var credentials: StoredCredentials? = initialCredentials
        private set

    val savedAccessTokens = mutableListOf<String>()

    override suspend fun load(issuer: String): StoredCredentials? = credentials

    override suspend fun save(credentials: StoredCredentials) {
        synchronized(savedAccessTokens) {
            savedAccessTokens += credentials.tokens.accessToken
            this.credentials = credentials
        }
    }

    override suspend fun clear(issuer: String) {
        credentials = null
    }
}

private data class RecordedAuthRequest(
    val path: String,
    val authorization: String?,
    val profileContext: String?,
)

private class BlockingAuthTransport : Interceptor {
    private val gates = ConcurrentHashMap<String, TransportGate>()
    private val responseCallbacks = ConcurrentHashMap<String, () -> Unit>()
    private val requests = mutableListOf<RecordedAuthRequest>()
    private val loginCount = AtomicInteger()
    private val unauthorizedCollections = AtomicInteger()

    @Volatile
    var loginFailure: IOException? = null
    @Volatile
    var logoutFailure: IOException? = null

    @Volatile
    var refreshResponse: Pair<Int, String>? = null


    fun failNextCollectionsWithUnauthorized() {
        check(unauthorizedCollections.compareAndSet(0, 1)) { "A collection failure is already queued" }
    }
    fun blockNext(path: String): TransportGate = TransportGate().also { gate ->
        check(gates.putIfAbsent(path, gate) == null) { "A gate already exists for $path" }
    }

    fun onNextResponse(path: String, callback: () -> Unit) {
        check(responseCallbacks.putIfAbsent(path, callback) == null) { "A response callback already exists for $path" }
    }

    fun requestCount(path: String): Int = synchronized(requests) {
        requests.count { it.path == path }
    }

    fun singleRequest(path: String): RecordedAuthRequest = synchronized(requests) {
        requests.single { it.path == path }
    }

    fun lastRequest(path: String): RecordedAuthRequest = synchronized(requests) {
        requests.last { it.path == path }
    }

    fun releaseAll() {
        gates.values.forEach(TransportGate::release)
        gates.clear()
    }

    override fun intercept(chain: Interceptor.Chain): Response {
        val request = chain.request()
        val path = request.url.encodedPath
        synchronized(requests) {
            requests += RecordedAuthRequest(
                path,
                request.header("Authorization"),
                request.header("X-Rivune-Profile-Context"),
            )
        }
        gates.remove(path)?.block()
        if (path == "/api/v1/auth/login") loginFailure?.let { throw it }
        if (path == "/api/v1/auth/logout") logoutFailure?.let { throw it }

        val rejectCollections = path == "/api/v1/collections" && unauthorizedCollections.compareAndSet(1, 0)
        val configuredRefresh = if (path == "/api/v1/auth/refresh") refreshResponse else null
        val status = when {
            configuredRefresh != null -> configuredRefresh.first
            path == "/api/v1/profiles/selection" -> 204
            rejectCollections -> 401
            else -> 200
        }
        val body = when {
            configuredRefresh != null -> configuredRefresh.second
            rejectCollections -> """{"error":{"code":"expired","message":"Expired"}}"""
            path == "/.well-known/rivune" -> DISCOVERY_JSON
            path == "/api/v1/auth/login" -> tokenJson("login-${loginCount.incrementAndGet()}")
            path == "/api/v1/auth/device-code/token" -> tokenJson("device")
            path == "/api/v1/auth/refresh" -> tokenJson("refreshed")
            path == "/api/v1/collections" -> """{"collections":[]}"""
            path.endsWith("/select") -> PROFILE_SELECTION_JSON
            else -> "{}"
        }
        val response = Response.Builder()
            .request(request)
            .protocol(Protocol.HTTP_1_1)
            .code(status)
            .message("OK")
            .header("Content-Type", "application/json")
            .body(body.toResponseBody(JSON_MEDIA_TYPE))
            .build()
        responseCallbacks.remove(path)?.invoke()
        return response
    }

    private companion object {
        val JSON_MEDIA_TYPE = "application/json".toMediaType()
        const val DISCOVERY_JSON = """{"name":"Rivune","serverVersion":"test","protocolVersion":20,"apiBaseUrl":"/api/v1","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en"}"""
        const val PROFILE_SELECTION_JSON = """{"profile":{"id":"44444444-4444-4444-8444-444444444444","name":"Viewer","description":null,"categoryId":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","category":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Default","color":null,"icon":null},"isChild":false,"hasPin":false,"canManage":true,"enabled":true,"availableFrom":null,"availableUntil":null,"accessStartTime":null,"accessEndTime":null,"accessTimezone":"UTC","accessible":true,"avatar":{"kind":"preset","presetId":"one","url":"/api/v1/avatar"}},"expiresAt":"2026-08-12T12:00:00Z","profileContext":"context-one"}"""
    }
}

private class TransportGate {
    private val entered = CountDownLatch(1)
    private val released = CountDownLatch(1)

    fun awaitRequest() {
        assertTrue(entered.await(5, TimeUnit.SECONDS), "Timed out waiting for the blocked request")
    }

    fun release() {
        released.countDown()
    }

    fun block() {
        entered.countDown()
        check(released.await(5, TimeUnit.SECONDS)) { "Timed out waiting to release request" }
    }
}

private fun tokenPair(prefix: String) = TokenPair(
    tokenType = "Bearer",
    accessToken = "$prefix-access",
    accessTokenExpiresAt = "2026-08-04T12:15:00Z",
    refreshToken = "$prefix-refresh",
    refreshTokenExpiresAt = "2026-09-04T12:00:00Z",
    sessionId = UUID.fromString("22222222-2222-4222-8222-222222222222"),
    deviceId = UUID.fromString("33333333-3333-4333-8333-333333333333"),
    authorizationScope = AuthorizationScope.GLOBAL_ADMIN,
    category = null,
)

private fun tokenJson(prefix: String) =
    """{"tokenType":"Bearer","accessToken":"$prefix-access","accessTokenExpiresAt":"2026-08-04T12:15:00Z","refreshToken":"$prefix-refresh","refreshTokenExpiresAt":"2026-09-04T12:00:00Z","sessionId":"22222222-2222-4222-8222-222222222222","deviceId":"33333333-3333-4333-8333-333333333333","authorizationScope":"global_admin","category":null}"""
