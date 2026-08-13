package io.rivune.api

import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNotEquals
import kotlinx.coroutines.runBlocking
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.Request
import okhttp3.Response
import okhttp3.ResponseBody
import okhttp3.ResponseBody.Companion.toResponseBody
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okio.Buffer
import okio.BufferedSource
import okio.ForwardingSource
import okio.buffer

class ResponseSizeLimitTest {
    @Test
    fun responseAtLimitIsAccepted() = runBlocking {
        val server = MockWebServer()
        server.enqueue(
            MockResponse()
                .setHeader("Content-Type", "application/json")
                .setBody(paddedJson(DISCOVERY_JSON, RESPONSE_LIMIT_BYTES)),
        )
        server.start()
        try {
            val discovery = RivuneApiClient(server.loopbackUrl("/").toString(), ResponseLimitCredentialStore()).discover()

            assertEquals("Rivune", discovery.name)
        } finally {
            server.shutdown()
        }
    }

    @Test
    fun responseBodyIsReadOffCallerThread() = runBlocking {
        val callerThread = Thread.currentThread()
        lateinit var bodyReadThread: Thread
        val source = object : ForwardingSource(Buffer().writeUtf8(DISCOVERY_JSON)) {
            override fun read(sink: Buffer, byteCount: Long): Long {
                bodyReadThread = Thread.currentThread()
                return super.read(sink, byteCount)
            }
        }.buffer()
        val httpClient = OkHttpClient.Builder()
            .addInterceptor { chain ->
                Response.Builder()
                    .request(chain.request())
                    .protocol(Protocol.HTTP_1_1)
                    .code(200)
                    .message("OK")
                    .body(object : ResponseBody() {
                        override fun contentType() = JSON_MEDIA_TYPE
                        override fun contentLength() = -1L
                        override fun source(): BufferedSource = source
                    })
                    .build()
            }
            .build()

        RivuneApiClient(
            "http://127.0.0.1/",
            ResponseLimitCredentialStore(),
            httpClient,
        ).discover()

        assertNotEquals(callerThread, bodyReadThread)
    }

    @Test
    fun contentLengthOverLimitIsRejectedBeforeBodyRead() = runBlocking {
        val client = RivuneApiClient(
            "http://127.0.0.1/",
            ResponseLimitCredentialStore(),
            oversizedClient(status = 200),
        )

        val error = assertFailsWith<RivuneApiException.ResponseTooLarge> { client.discover() }

        assertEquals("The Rivune server response exceeds the 16 MiB limit", error.message)
    }

    @Test
    fun chunkedResponseOverLimitIsRejectedWhileReading() = runBlocking {
        val server = MockWebServer()
        server.enqueue(
            MockResponse()
                .setHeader("Content-Type", "application/json")
                .setChunkedBody(paddedJson(DISCOVERY_JSON, RESPONSE_LIMIT_BYTES + 1), 8192),
        )
        server.start()
        try {
            val client = RivuneApiClient(server.loopbackUrl("/").toString(), ResponseLimitCredentialStore())

            assertFailsWith<RivuneApiException.ResponseTooLarge> { client.discover() }
            Unit
        } finally {
            server.shutdown()
        }
    }

    @Test
    fun oversizedErrorResponseIsNotParsed() = runBlocking {
        val client = RivuneApiClient(
            "http://127.0.0.1/",
            ResponseLimitCredentialStore(),
            oversizedClient(status = 400),
        )

        assertFailsWith<RivuneApiException.ResponseTooLarge> { client.discover() }
        Unit
    }

    @Test
    fun oversizedUnauthorizedResponseDoesNotRefresh() = runBlocking {
        var requestCount = 0
        val httpClient = OkHttpClient.Builder()
            .addInterceptor { chain ->
                requestCount += 1
                if (chain.request().url.encodedPath == "/.well-known/rivune") {
                    Response.Builder()
                        .request(chain.request())
                        .protocol(Protocol.HTTP_1_1)
                        .code(200)
                        .message("OK")
                        .body(DISCOVERY_JSON.toResponseBody(JSON_MEDIA_TYPE))
                        .build()
                } else {
                    oversizedResponse(chain.request(), status = 401)
                }
            }
            .build()
        val client = RivuneApiClient(
            "http://127.0.0.1/",
            ResponseLimitCredentialStore(
                StoredCredentials("http://127.0.0.1/", responseLimitTokenPair()),
            ),
            httpClient,
        )

        assertFailsWith<RivuneApiException.ResponseTooLarge> { client.currentAccount() }
        assertEquals(2, requestCount)
    }

    private fun oversizedClient(status: Int): OkHttpClient = OkHttpClient.Builder()
        .addInterceptor { chain -> oversizedResponse(chain.request(), status) }
        .build()

    private fun oversizedResponse(request: Request, status: Int): Response = Response.Builder()
        .request(request)
        .protocol(Protocol.HTTP_1_1)
        .code(status)
        .message("Test response")
        .body(object : ResponseBody() {
            private val guardedSource = object : ForwardingSource(Buffer()) {
                override fun read(sink: Buffer, byteCount: Long): Long =
                    throw AssertionError("Oversized response body must not be read")
            }.buffer()

            override fun contentType() = JSON_MEDIA_TYPE

            override fun contentLength() = RESPONSE_LIMIT_BYTES.toLong() + 1L

            override fun source(): BufferedSource = guardedSource
        })
        .build()

    private fun paddedJson(json: String, size: Int): String {
        val paddingSize = size - json.toByteArray(Charsets.UTF_8).size
        require(paddingSize >= 0)
        return json + " ".repeat(paddingSize)
    }

    private companion object {
        const val RESPONSE_LIMIT_BYTES = 16 * 1024 * 1024
        val JSON_MEDIA_TYPE = "application/json; charset=utf-8".toMediaType()
        const val DISCOVERY_JSON = """{"name":"Rivune","serverVersion":"test","protocolVersion":20,"apiBaseUrl":"/api/v1","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en"}"""
    }
}

private class ResponseLimitCredentialStore(
    private var credentials: StoredCredentials? = null,
) : CredentialStore {
    override suspend fun load(issuer: String): StoredCredentials? = credentials

    override suspend fun save(credentials: StoredCredentials) {
        this.credentials = credentials
    }

    override suspend fun clear(issuer: String) {
        credentials = null
    }
}

private fun responseLimitTokenPair() = TokenPair(
    tokenType = "Bearer",
    accessToken = "access-token",
    accessTokenExpiresAt = "2026-08-05T12:15:00Z",
    refreshToken = "refresh-token",
    refreshTokenExpiresAt = "2026-09-05T12:00:00Z",
    sessionId = UUID.fromString("22222222-2222-4222-8222-222222222222"),
    deviceId = UUID.fromString("33333333-3333-4333-8333-333333333333"),
    authorizationScope = AuthorizationScope.GLOBAL_ADMIN,
    category = null,
)
