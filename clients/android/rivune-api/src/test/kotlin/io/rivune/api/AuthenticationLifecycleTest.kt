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

    private fun lifecycleFixture(initialTokens: TokenPair? = null): LifecycleFixture {
        val server = MockWebServer().apply { start() }
        val issuer = server.url("/").toString()
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
)

private class BlockingAuthTransport : Interceptor {
    private val gates = ConcurrentHashMap<String, TransportGate>()
    private val requests = mutableListOf<RecordedAuthRequest>()
    private val loginCount = AtomicInteger()

    @Volatile
    var logoutFailure: IOException? = null

    fun blockNext(path: String): TransportGate = TransportGate().also { gate ->
        check(gates.putIfAbsent(path, gate) == null) { "A gate already exists for $path" }
    }

    fun requestCount(path: String): Int = synchronized(requests) {
        requests.count { it.path == path }
    }

    fun singleRequest(path: String): RecordedAuthRequest = synchronized(requests) {
        requests.single { it.path == path }
    }

    fun releaseAll() {
        gates.values.forEach(TransportGate::release)
        gates.clear()
    }

    override fun intercept(chain: Interceptor.Chain): Response {
        val request = chain.request()
        val path = request.url.encodedPath
        synchronized(requests) {
            requests += RecordedAuthRequest(path, request.header("Authorization"))
        }
        gates.remove(path)?.block()
        if (path == "/api/v1/auth/logout") logoutFailure?.let { throw it }

        val body = when (path) {
            "/.well-known/rivune" -> DISCOVERY_JSON
            "/api/v1/auth/login" -> tokenJson("login-${loginCount.incrementAndGet()}")
            "/api/v1/auth/device-code/token" -> tokenJson("device")
            "/api/v1/auth/refresh" -> tokenJson("refreshed")
            else -> "{}"
        }
        return Response.Builder()
            .request(request)
            .protocol(Protocol.HTTP_1_1)
            .code(200)
            .message("OK")
            .header("Content-Type", "application/json")
            .body(body.toResponseBody(JSON_MEDIA_TYPE))
            .build()
    }

    private companion object {
        val JSON_MEDIA_TYPE = "application/json".toMediaType()
        const val DISCOVERY_JSON = """{"name":"Rivune","serverVersion":"test","protocolVersion":20,"apiBaseUrl":"/api/v1","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en"}"""
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
