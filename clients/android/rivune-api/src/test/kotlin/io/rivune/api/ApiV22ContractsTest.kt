package io.rivune.api

import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertIs
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.SerializationException
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer

class ApiV22ContractsTest {
    private val json = Json { explicitNulls = false }
    private val profileId = UUID.fromString("11111111-1111-4111-8111-111111111111")
    private val itemId = UUID.fromString("22222222-2222-4222-8222-222222222222")
    private val failoverId = UUID.fromString("33333333-3333-4333-8333-333333333333")

    @Test
    fun queueRoutesPreserveCasAndOperationId() = runBlocking {
        val server = MockWebServer()
        server.enqueue(discovery())
        server.enqueue(response(queueFixture()))
        server.enqueue(response("""{"revision":3,"affectedItemId":"$itemId","duplicate":false}"""))
        server.enqueue(response("""{"revision":4,"affectedItemId":"$itemId"}"""))
        server.start()
        try {
            val client = client(server)
            val queue = client.readingQueue(profileId)
            val operationId = UUID.fromString("44444444-4444-4444-8444-444444444444")
            val mutation = client.addReadingQueueItem(
                profileId,
                ReadingQueueAddInput(operationId, queue.revision, ReadingQueueMediaType.MOVIE, "tmdb:42", title = "Dune"),
            )
            client.updateReadingQueueItem(
                profileId,
                itemId,
                ReadingQueueUpdateInput(operationId, mutation.revision, "Dune: Part Two"),
            )
            assertEquals(3, mutation.revision)
            assertEquals("/.well-known/rivune", server.takeRequest().path)
            assertEquals("/api/v1/profiles/$profileId/queue", server.takeRequest().path)
            val request = server.takeRequest()
            assertEquals("POST", request.method)
            assertEquals("/api/v1/profiles/$profileId/queue/items", request.path)
            val body = json.parseToJsonElement(request.body.readUtf8()).jsonObject
            assertEquals(operationId.toString(), body.getValue("operationId").jsonPrimitive.content)
            assertEquals("2", body.getValue("expectedRevision").jsonPrimitive.content)
            assertEquals("movie", body.getValue("mediaType").jsonPrimitive.content)
            val update = server.takeRequest()
            assertEquals("PATCH", update.method)
            assertEquals("/api/v1/profiles/$profileId/queue/items/$itemId", update.path)
        } finally {
            server.shutdown()
        }
    }

    @Test
    fun v22RoutesAndClosedResponsesMatchProtocol() = runBlocking {
        val server = MockWebServer()
        server.enqueue(discovery())
        server.enqueue(response(failoverFixture()))
        server.enqueue(response("""{"savedSearches":[]}"""))
        server.enqueue(response("""{"smartCollections":[]}"""))
        server.enqueue(response("""{"incidents":[]}"""))
        server.enqueue(response("""{"notifications":[],"nextCursor":"9"}"""))
        server.enqueue(response(accessibilityFixture()))
        server.enqueue(response(queueFixture(extra = ",\"unknown\":true")))
        server.start()
        try {
            val client = client(server)
            assertEquals(PlaybackFailoverStatus.ACTIVE, client.playbackFailover(failoverId).status)
            assertEquals(emptyList(), client.savedSearches())
            assertEquals(emptyList(), client.smartCollections())
            assertEquals(emptyList(), client.extensionIncidents())
            assertEquals("9", client.mediaNotifications(limit = 30).nextCursor)
            assertEquals(ReducedMotionPreference.SYSTEM, client.profileAccessibilityPreferences(profileId).reducedMotion)
            assertFailsWith<RivuneApiException.InvalidResponse> { client.readingQueue(profileId) }

            assertEquals("/.well-known/rivune", server.takeRequest().path)
            assertEquals("/api/v1/playback/failovers/$failoverId", server.takeRequest().path)
            assertEquals("/api/v1/saved-searches", server.takeRequest().path)
            assertEquals("/api/v1/smart-collections", server.takeRequest().path)
            assertEquals("/api/v1/operations/extension-incidents", server.takeRequest().path)
            assertEquals("/api/v1/media-notifications?limit=30", server.takeRequest().path)
            assertEquals("/api/v1/profiles/$profileId/accessibility-preferences", server.takeRequest().path)
        } finally {
            server.shutdown()
        }
    }

    @Test
    fun closedEnumsAndSmartAstRejectFreeFormValues() {
        assertFailsWith<SerializationException> { json.decodeFromString<PlaybackFailoverError>("\"network_weird\"") }
        assertFailsWith<SerializationException> { json.decodeFromString<MediaNotificationKind>("\"arbitrary\"") }
        assertFailsWith<SerializationException> { json.decodeFromString<ReducedMotionPreference>("\"sometimes\"") }

        val encoded = json.parseToJsonElement(
            json.encodeToString<SmartRule>(
                SmartRule.All(
                    listOf(
                        SmartRule.MediaType(values = listOf(CatalogMediaType.MOVIE)),
                        SmartRule.Rating(SmartNumericOperator.GTE, 7.5),
                    ),
                ),
            ),
        ).jsonObject
        assertEquals("all", encoded.getValue("type").jsonPrimitive.content)
        assertIs<SmartRule.All>(json.decodeFromString<SmartRule>(encoded.toString()))
    }

    private fun client(server: MockWebServer): RivuneApiClient {
        val base = server.loopbackUrl("/").toString()
        return RivuneApiClient(base, TestCredentialStore(base))
    }

    private fun discovery() = response(
        """{"name":"Rivune","serverVersion":"test","protocolVersion":22,"apiBaseUrl":"/api/v1","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en"}""",
    )

    private fun response(body: String) = MockResponse().setHeader("Content-Type", "application/json").setBody(body)

    private fun queueFixture(extra: String = "") =
        """{"revision":2,"items":[{"id":"$itemId","mediaType":"movie","resourceId":"tmdb:42","title":"Dune","position":0,"createdAt":"2026-08-26T10:00:00Z","updatedAt":"2026-08-26T10:00:00Z"}]$extra}"""

    private fun failoverFixture() =
        """{"id":"$failoverId","currentSourceRef":"opaque-source-reference-01","currentPosition":0,"positionSeconds":12.5,"attemptCount":0,"maximumAttempts":2,"revision":1,"status":"active","candidateHealth":[{"position":0,"status":"current"},{"position":1,"status":"available"}],"expiresAt":"2026-08-26T11:00:00Z"}"""

    private fun accessibilityFixture() =
        """{"revision":4,"reducedMotion":"system","highContrast":"more","textScale":115,"captions":"on","audioDescription":true,"focusIndicators":"enhanced"}"""
}

private class TestCredentialStore(private val issuer: String) : CredentialStore {
    private val credentials = StoredCredentials(
        issuer = issuer,
        tokens = TokenPair(
            "Bearer", "access", "2099-01-01T00:00:00Z", "refresh", "2099-02-01T00:00:00Z",
            UUID.fromString("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"),
            UUID.fromString("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"),
            AuthorizationScope.GLOBAL_ADMIN,
            null,
        ),
        profileContext = "profile-context",
    )

    override suspend fun load(issuer: String): StoredCredentials? = credentials.takeIf { issuer == this.issuer }
    override suspend fun save(credentials: StoredCredentials) = Unit
    override suspend fun clear(issuer: String) = Unit
}
