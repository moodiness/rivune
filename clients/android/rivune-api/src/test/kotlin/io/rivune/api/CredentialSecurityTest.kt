package io.rivune.api

import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNull
import kotlin.test.assertFalse
import kotlin.test.assertTrue
import kotlinx.coroutines.runBlocking
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.Request
import okhttp3.Response
import okhttp3.ResponseBody.Companion.toResponseBody
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer

class CredentialSecurityTest {
    @Test
    fun accessTokenFromAnotherServerIsNotAttached() = runBlocking {
        val issuerServer = MockWebServer()
        val destinationServer = MockWebServer()
        issuerServer.start()
        destinationServer.start()
        try {
            destinationServer.enqueue(discoveryResponse())
            destinationServer.enqueue(MockResponse().setHeader("Content-Type", "application/json").setBody("{}"))
            val issuer = issuerServer.loopbackUrl("/").toString()
            val store = LeakyCredentialStore(StoredCredentials(issuer, tokenPair()))
            val client = RivuneApiClient(destinationServer.loopbackUrl("/").toString(), store)

            assertFailsWith<RivuneApiException.NotAuthenticated> { client.currentAccount() }

            assertEquals(1, destinationServer.requestCount)
            assertNull(destinationServer.takeRequest().getHeader("Authorization"))
        } finally {
            issuerServer.shutdown()
            destinationServer.shutdown()
        }
    }

    @Test
    fun refreshTokenFromAnotherServerIsNotPosted() = runBlocking {
        val issuerServer = MockWebServer()
        val destinationServer = MockWebServer()
        issuerServer.start()
        destinationServer.start()
        try {
            destinationServer.enqueue(discoveryResponse())
            destinationServer.enqueue(MockResponse().setHeader("Content-Type", "application/json").setBody("{}"))
            val issuer = issuerServer.loopbackUrl("/").toString()
            val store = LeakyCredentialStore(StoredCredentials(issuer, tokenPair()))
            val client = RivuneApiClient(destinationServer.loopbackUrl("/").toString(), store)

            assertFailsWith<RivuneApiException.NotAuthenticated> { client.refreshSession() }

            assertEquals(0, destinationServer.requestCount)
        } finally {
            issuerServer.shutdown()
            destinationServer.shutdown()
        }
    }

    @Test
    fun publicHttpServersAreRejected() {
        listOf(
            "http://rivune.example",
            "http://192.0.2.10",
            "http://169.254.10.20",
            "http://203.0.113.20",
            "http://rivune.local",
            "http://RIVUNE.LOCAL",
            "http://rivune.local.",
            "http://[2001:db8::20]",
            "http://[fe80::20]",
        ).forEach { serverUrl ->
            assertFailsWith<RivuneApiException.InvalidServerUrl>(serverUrl) {
                RivuneApiClient(serverUrl, LeakyCredentialStore(null))
            }
        }
    }

    @Test
    fun privateNetworkHttpServersAreAccepted() {
        listOf(
            "http://localhost:8080",
            "http://127.42.7.9:8080",
            "http://10.0.0.20:8080",
            "http://172.31.255.254:8080",
            "http://192.168.1.20:8080",
            "http://[::1]:8080",
            "http://[fd00::20]:8080",
        ).forEach { serverUrl ->
            RivuneApiClient(serverUrl, LeakyCredentialStore(null))
        }
    }

    @Test
    fun localNetworkPermissionClassifierIncludesSupportedKnownLanDestinations() {
        listOf(
            "http://10.0.0.20:8080",
            "http://172.31.255.254:8080",
            "http://192.168.1.20:8080",
            "http://[fd00::20]:8080",
            "https://10.0.0.20:8080",
            "https://172.16.0.1:8080",
            "https://192.168.1.20:8080",
            "https://[fd00::20]:8080",
            "https://rivune.local:8080",
            "https://RIVUNE.LOCAL:8080",
            "https://rivune.local.:8080",
        ).forEach { assertTrue(isKnownLocalNetworkServerUrl(it), it) }

        listOf(
            "http://localhost:8080",
            "https://localhost:8080",
            "http://127.42.7.9:8080",
            "https://127.42.7.9:8080",
            "http://[::1]:8080",
            "https://[::1]:8080",
            "http://rivune.local:8080",
            "http://rivune.local.:8080",
            "https://local:8080",
            "https://rivune.example:8080",
            "https://169.254.10.20:8080",
            "https://100.64.0.1:8080",
            "https://192.0.2.10:8080",
            "https://[fe80::20]:8080",
            "https://user@10.0.0.20:8080",
            "https://user:password@rivune.local:8080",
            "https://@10.0.0.20:8080",
            "https://:@rivune.local:8080",
            "not a URL",
        ).forEach { assertFalse(isKnownLocalNetworkServerUrl(it), it) }
    }

    @Test
    fun loopbackHttpCanCarryLoginCredentials() = runBlocking {
        val server = MockWebServer()
        server.enqueue(discoveryResponse())
        server.enqueue(
            MockResponse()
                .setHeader("Content-Type", "application/json")
                .setBody(
                    """{"tokenType":"Bearer","accessToken":"loopback-access","accessTokenExpiresAt":"2026-08-04T12:15:00Z","refreshToken":"loopback-refresh","refreshTokenExpiresAt":"2026-09-04T12:00:00Z","sessionId":"22222222-2222-4222-8222-222222222222","deviceId":"33333333-3333-4333-8333-333333333333","authorizationScope":"global_admin","category":null}""",
                ),
        )
        server.start()
        try {
            val client = RivuneApiClient(server.loopbackUrl("/").toString(), LeakyCredentialStore(null))

            client.login("alice", "password", LoginDevice(name = "Test phone", platform = "android"))

            assertEquals("/.well-known/rivune", server.takeRequest().path)
            val login = server.takeRequest()
            assertEquals("/api/v1/auth/login", login.path)
            assertEquals("POST", login.method)
        } finally {
            server.shutdown()
        }
    }

    @Test
    fun privateNetworkHttpCanCarryLoginCredentials() = runBlocking {
        val requests = mutableListOf<Request>()
        val client = RivuneApiClient(
            "http://192.168.1.20:8080",
            LeakyCredentialStore(null),
            OkHttpClient.Builder()
                .addInterceptor { chain ->
                    val request = chain.request()
                    requests += request
                    val body = if (request.url.encodedPath == "/.well-known/rivune") {
                        DISCOVERY_RESPONSE
                    } else {
                        LOGIN_RESPONSE
                    }
                    Response.Builder()
                        .request(request)
                        .protocol(Protocol.HTTP_1_1)
                        .code(200)
                        .message("OK")
                        .header("Content-Type", "application/json")
                        .body(body.toResponseBody(JSON_MEDIA_TYPE))
                        .build()
                }
                .build(),
        )

        client.login("alice", "password", LoginDevice(name = "Test phone", platform = "android"))

        assertEquals(
            listOf("/.well-known/rivune", "/api/v1/auth/login"),
            requests.map { it.url.encodedPath },
        )
        assertEquals(listOf("192.168.1.20", "192.168.1.20"), requests.map { it.url.host })
        assertNull(requests.first().header("Authorization"))
    }

    @Test
    fun discoveryCannotRedirectToAnotherOrigin() = runBlocking {
        val server = MockWebServer()
        server.enqueue(discoveryResponse(apiBaseUrl = "http://127.0.0.2/api/v1"))
        server.start()
        try {
            val client = RivuneApiClient(server.loopbackUrl("/").toString(), LeakyCredentialStore(null))

            assertFailsWith<RivuneApiException.InvalidServerUrl> { client.discover() }
            Unit
        } finally {
            server.shutdown()
        }
    }

    @Test
    fun sameOriginRedirectsAreRejectedWithoutReplayingAuthenticatedPost() = runBlocking {
        for (status in REDIRECT_STATUSES) {
            val server = MockWebServer()
            server.start()
            try {
                server.enqueue(discoveryResponse())
                server.enqueue(redirectResponse(status, server.loopbackUrl("/redirect-target").toString()))
                server.enqueue(MockResponse().setHeader("Content-Type", "application/json").setBody("{}"))
                var interceptorCalls = 0
                val injectedClient = OkHttpClient.Builder()
                    .followRedirects(true)
                    .followSslRedirects(true)
                    .addInterceptor { chain ->
                        interceptorCalls += 1
                        chain.proceed(chain.request())
                    }
                    .build()
                val issuer = server.loopbackUrl("/").toString()
                val client = RivuneApiClient(
                    issuer,
                    LeakyCredentialStore(StoredCredentials(issuer, tokenPair())),
                    injectedClient,
                )

                val error = assertFailsWith<RivuneApiException.Server> {
                    client.approveDeviceAuthorization(approvalRequest(status))
                }

                assertEquals(status, error.status)
                assertEquals("http_$status", error.code)
                assertEquals(2, interceptorCalls)
                assertEquals(2, server.requestCount)
                assertEquals("/.well-known/rivune", server.takeRequest().path)
                val post = server.takeRequest()
                assertEquals("/api/v1/auth/device-code/approve", post.path)
                assertEquals("POST", post.method)
                assertEquals("Bearer server-a-access", post.getHeader("Authorization"))
                assertEquals(true, post.body.readUtf8().contains("\"userCode\":\"CODE-$status\""))
            } finally {
                server.shutdown()
            }
        }
    }

    @Test
    fun crossOriginRedirectsAreRejectedBeforeSendingCredentialsOrPostBody() = runBlocking {
        for (status in REDIRECT_STATUSES) {
            val sourceServer = MockWebServer()
            val destinationServer = MockWebServer()
            sourceServer.start()
            destinationServer.start()
            try {
                sourceServer.enqueue(discoveryResponse())
                sourceServer.enqueue(redirectResponse(status, destinationServer.loopbackUrl("/redirect-target").toString()))
                destinationServer.enqueue(MockResponse().setHeader("Content-Type", "application/json").setBody("{}"))
                val issuer = sourceServer.loopbackUrl("/").toString()
                val client = RivuneApiClient(
                    issuer,
                    LeakyCredentialStore(StoredCredentials(issuer, tokenPair())),
                    OkHttpClient.Builder()
                        .followRedirects(true)
                        .followSslRedirects(true)
                        .build(),
                )

                val error = assertFailsWith<RivuneApiException.Server> {
                    client.approveDeviceAuthorization(approvalRequest(status))
                }

                assertEquals(status, error.status)
                assertEquals("http_$status", error.code)
                assertEquals(2, sourceServer.requestCount)
                assertEquals(0, destinationServer.requestCount)
                assertEquals("/.well-known/rivune", sourceServer.takeRequest().path)
                val post = sourceServer.takeRequest()
                assertEquals("/api/v1/auth/device-code/approve", post.path)
                assertEquals("POST", post.method)
                assertEquals("Bearer server-a-access", post.getHeader("Authorization"))
                assertEquals(true, post.body.readUtf8().contains("\"userCode\":\"CODE-$status\""))
            } finally {
                sourceServer.shutdown()
                destinationServer.shutdown()
            }
        }
    }

    private fun redirectResponse(status: Int, location: String) = MockResponse()
        .setResponseCode(status)
        .setHeader("Location", location)

    private fun approvalRequest(status: Int) = DeviceCodeApprovalRequest(
        userCode = "CODE-$status",
        categoryId = UUID.fromString("44444444-4444-4444-8444-444444444444"),
        deviceName = "Redirect guard",
    )

    private fun discoveryResponse(apiBaseUrl: String = "/api/v1") = MockResponse()
        .setHeader("Content-Type", "application/json")
        .setBody(
            """{"name":"Rivune","serverVersion":"test","protocolVersion":20,"apiBaseUrl":"$apiBaseUrl","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en"}""",
        )

    private companion object {
        val JSON_MEDIA_TYPE = "application/json".toMediaType()
        const val DISCOVERY_RESPONSE =
            """{"name":"Rivune","serverVersion":"test","protocolVersion":20,"apiBaseUrl":"/api/v1","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en"}"""
        const val LOGIN_RESPONSE =
            """{"tokenType":"Bearer","accessToken":"lan-access","accessTokenExpiresAt":"2026-08-04T12:15:00Z","refreshToken":"lan-refresh","refreshTokenExpiresAt":"2026-09-04T12:00:00Z","sessionId":"22222222-2222-4222-8222-222222222222","deviceId":"33333333-3333-4333-8333-333333333333","authorizationScope":"global_admin","category":null}"""
        val REDIRECT_STATUSES = listOf(302, 307, 308)
    }

    private fun tokenPair() = TokenPair(
        tokenType = "Bearer",
        accessToken = "server-a-access",
        accessTokenExpiresAt = "2026-08-04T12:15:00Z",
        refreshToken = "server-a-refresh",
        refreshTokenExpiresAt = "2026-09-04T12:00:00Z",
        sessionId = UUID.fromString("22222222-2222-4222-8222-222222222222"),
        deviceId = UUID.fromString("33333333-3333-4333-8333-333333333333"),
        authorizationScope = AuthorizationScope.GLOBAL_ADMIN,
        category = null,
    )
}

private class LeakyCredentialStore(
    private var credentials: StoredCredentials?,
) : CredentialStore {
    override suspend fun load(issuer: String): StoredCredentials? = credentials

    override suspend fun save(credentials: StoredCredentials) {
        this.credentials = credentials
    }

    override suspend fun clear(issuer: String) = Unit
}
