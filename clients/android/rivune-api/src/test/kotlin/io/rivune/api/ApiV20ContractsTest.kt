package io.rivune.api

import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer

class ApiV20ContractsTest {
    private val json = Json { ignoreUnknownKeys = true }
    private val titleId = UUID.fromString("11111111-1111-4111-8111-111111111111")
    private val seriesId = UUID.fromString("22222222-2222-4222-8222-222222222222")
    private val addonId = UUID.fromString("33333333-3333-4333-8333-333333333333")

    @Test
    fun discoveryMetadataAndMarkersExposeV20Fields() = runBlocking {
        val movie = json.decodeFromString<Movie>(
            """{"id":"11111111-1111-4111-8111-111111111111","mediaType":"movie","title":"Movie","originalTitle":"Movie","originalLanguage":"en","overview":"Overview","genres":[],"cast":[{"id":"42","name":"Actor","character":"Lead","profileUrl":"/artwork/cast"}],"voteAverage":8.5,"voteCount":10,"externalIds":{"imdb":"tt1234567"}}""",
        )
        assertEquals("Actor", movie.cast.single().name)
        assertEquals("Lead", movie.cast.single().character)

        val series = json.decodeFromString<Series>(seriesFixture())
        assertEquals("84", series.selectedEpisodeOrderId)
        assertEquals("Actor", series.cast.single().name)

        val server = MockWebServer()
        server.enqueue(discoveryResponse(setupCompleted = true, demoAvailable = false))
        server.enqueue(jsonResponse(seriesFixture()))
        server.enqueue(jsonResponse("""{"markers":[{"type":"intro","startSeconds":12.25,"endSeconds":91.75,"confidence":0.9,"submissionCount":8}]}"""))
        server.enqueue(jsonResponse("""{"sources":[],"providerErrors":[]}"""))
        server.start()
        try {
            val serverUrl = server.loopbackUrl("/").toString()
            val client = RivuneApiClient(serverUrl, V20CredentialStore(serverUrl, tokenPair()))
            val discovery = client.discover()
            assertEquals(true, discovery.setupCompleted)
            assertEquals(false, discovery.demoAvailable)

            client.series(seriesId, language = "fr-FR", mappingProvider = SeriesMappingProvider.TVDB, episodeOrder = "84")
            val markers = client.playbackMarkers("tt1234567", season = 2, episode = 3)
            client.playbackSources(
                mediaType = "episode",
                resourceId = "resource:episode",
                capabilities = PlaybackCapabilities(listOf("hls"), listOf("mp4")),
                addonId = addonId,
            )

            val discoveryRequest = server.takeRequest()
            assertEquals("/.well-known/rivune", discoveryRequest.path)
            val seriesRequest = server.takeRequest()
            assertEquals("/api/v1/metadata/series/$seriesId?language=fr-FR&mappingProvider=tvdb&episodeOrder=84", seriesRequest.path)
            val markerRequest = server.takeRequest()
            assertEquals("/api/v1/playback/markers?imdbId=tt1234567&season=2&episode=3", markerRequest.path)
            assertEquals(12.25, markers.markers.single().startSeconds)
            assertEquals(PlaybackMarkerType.INTRO, markers.markers.single().type)
            val sourcesRequest = server.takeRequest()
            assertEquals("POST", sourcesRequest.method)
            assertEquals("/api/v1/playback/sources", sourcesRequest.path)
            val sourceBody = json.parseToJsonElement(sourcesRequest.body.readUtf8()).jsonObject
            assertEquals(addonId.toString(), sourceBody.getValue("addonId").jsonPrimitive.content)
            assertEquals("resource:episode", sourceBody.getValue("resourceId").jsonPrimitive.content)
        } finally {
            server.shutdown()
        }
    }

    @Test
    fun playbackMediaInspectionTreatsLegacyNullTrackArraysAsEmpty() {
        val inspection = json.decodeFromString<PlaybackMediaInspection>(
            """{"container":"mp4","videoTracks":[{"index":0,"type":"video","codec":"h264"}],"audioTracks":[{"index":1,"type":"audio","codec":"aac"}],"subtitleTracks":null}""",
        )

        assertEquals(1, inspection.videoTracks.size)
        assertEquals(1, inspection.audioTracks.size)
        assertEquals(emptyList(), inspection.subtitleTracks)

        val session = json.decodeFromString<PlaybackSession>(
            """{"id":"44444444-4444-4444-8444-444444444444","selectedSourceId":"source","sources":[{"id":"source","addonId":"33333333-3333-4333-8333-333333333333","manifestId":"addon","mode":"direct","url":"https://media.example/movie.mp4","protocol":"http","compatible":true}],"subtitles":null,"providerErrors":null,"expiresAt":"2099-01-01T00:00:00Z"}""",
        )
        assertEquals(emptyList(), session.subtitles)
        assertEquals(emptyList(), session.providerErrors)
    }

    @Test
    fun progressionRoutesSerializeVersionsAndNoContentReturnsNull() = runBlocking {
        val progress = progressFixture(version = 9)
        val server = MockWebServer()
        server.enqueue(discoveryResponse())
        server.enqueue(MockResponse().setResponseCode(204))
        server.enqueue(jsonResponse("""{"items":[{"titleId":"$titleId","progress":null}]}"""))
        server.enqueue(jsonResponse(progress))
        server.enqueue(MockResponse().setResponseCode(204))
        server.enqueue(jsonResponse("""{"items":[{"titleId":"$titleId","progress":$progress}]}"""))
        server.enqueue(jsonResponse(progress))
        server.enqueue(jsonResponse(progress))
        server.enqueue(jsonResponse("""{"items":[{"titleId":"$titleId","mediaType":"movie","positionSeconds":120,"durationSeconds":7200,"version":9,"reason":"resume","lastWatchedAt":"2026-08-12T10:00:00Z"}]}"""))
        server.enqueue(MockResponse().setResponseCode(204))
        server.start()
        try {
            val serverUrl = server.loopbackUrl("/").toString()
            val client = RivuneApiClient(serverUrl, V20CredentialStore(serverUrl, tokenPair()))

            assertNull(client.playbackProgress(titleId))
            val batch = client.playbackProgressBatch(listOf(titleId))
            assertNull(batch.items.single().progress)
            val updated = client.updatePlaybackProgress(
                titleId,
                UpdatePlaybackProgressRequest(positionSeconds = 120, durationSeconds = 7200, completed = false, expectedVersion = 8L),
            )
            assertEquals(9L, updated.version)
            client.clearPlaybackProgress(titleId, expectedVersion = 9L)
            val watchedBatch = client.setTitlesWatchedBatch(listOf(SetWatchedBatchItem(titleId, completed = true, expectedVersion = 9L)))
            assertEquals(9L, watchedBatch.items.single().progress.version)
            client.markTitleWatched(titleId, expectedVersion = 9L)
            client.markTitleUnwatched(titleId, expectedVersion = 10L)
            val page = client.continueWatching(limit = 25)
            assertEquals(ContinueWatchingReason.RESUME, page.items.single().reason)
            client.dismissContinueWatchingTitle(titleId)

            server.takeRequest()
            assertEquals("GET" to "/api/v1/progress/$titleId", server.takeRequest().let { it.method to it.path })

            val batchRequest = server.takeRequest()
            assertEquals("POST" to "/api/v1/progress/batch", batchRequest.method to batchRequest.path)
            val batchBody = json.parseToJsonElement(batchRequest.body.readUtf8()).jsonObject
            assertEquals(titleId.toString(), batchBody.getValue("titleIds").jsonArray.single().jsonPrimitive.content)

            val updateRequest = server.takeRequest()
            assertEquals("PUT" to "/api/v1/progress/$titleId", updateRequest.method to updateRequest.path)
            val updateBody = json.parseToJsonElement(updateRequest.body.readUtf8()).jsonObject
            assertEquals("8", updateBody.getValue("expectedVersion").jsonPrimitive.content)
            assertEquals("120", updateBody.getValue("positionSeconds").jsonPrimitive.content)

            assertEquals("DELETE" to "/api/v1/progress/$titleId?expectedVersion=9", server.takeRequest().let { it.method to it.path })
            val watchedBatchRequest = server.takeRequest()
            assertEquals("PUT" to "/api/v1/titles/watched/batch", watchedBatchRequest.method to watchedBatchRequest.path)
            val watchedItem = json.parseToJsonElement(watchedBatchRequest.body.readUtf8()).jsonObject
                .getValue("items").jsonArray.single().jsonObject
            assertEquals("9", watchedItem.getValue("expectedVersion").jsonPrimitive.content)
            assertEquals(true, watchedItem.getValue("completed").jsonPrimitive.content.toBoolean())

            val watchedRequest = server.takeRequest()
            assertEquals("POST" to "/api/v1/titles/$titleId/watched", watchedRequest.method to watchedRequest.path)
            assertEquals("9", json.parseToJsonElement(watchedRequest.body.readUtf8()).jsonObject.getValue("expectedVersion").jsonPrimitive.content)
            assertEquals("DELETE" to "/api/v1/titles/$titleId/watched?expectedVersion=10", server.takeRequest().let { it.method to it.path })
            assertEquals("GET" to "/api/v1/continue-watching?limit=25", server.takeRequest().let { it.method to it.path })
            assertEquals("DELETE" to "/api/v1/continue-watching/$titleId", server.takeRequest().let { it.method to it.path })
        } finally {
            server.shutdown()
        }
    }

    private fun discoveryResponse(setupCompleted: Boolean = true, demoAvailable: Boolean = true) = jsonResponse(
        """{"name":"Rivune","serverVersion":"test","protocolVersion":20,"apiBaseUrl":"/api/v1","setupRequired":false,"setupCompleted":$setupCompleted,"demoAvailable":$demoAvailable,"timezone":"UTC","interfaceLanguage":"en"}""",
    )

    private fun seriesFixture() =
        """{"id":"$seriesId","mediaType":"series","name":"Series","originalName":"Series","originalLanguage":"en","overview":"Overview","genres":[],"cast":[{"id":"42","name":"Actor"}],"voteAverage":8.0,"voteCount":10,"seasons":[],"aliases":[],"episodeOrders":[{"id":"84","name":"Aired","type":"official","isDefault":true}],"selectedEpisodeOrderId":"84","mappingProvider":"tvdb","externalIds":{"tvdb":"123"}}"""

    private fun progressFixture(version: Long) =
        """{"titleId":"$titleId","mediaType":"movie","positionSeconds":120,"durationSeconds":7200,"completed":false,"version":$version,"lastWatchedAt":"2026-08-12T10:00:00Z","updatedAt":"2026-08-12T10:01:00Z"}"""

    private fun jsonResponse(body: String) = MockResponse()
        .setHeader("Content-Type", "application/json")
        .setBody(body)

    private fun tokenPair() = TokenPair(
        tokenType = "Bearer",
        accessToken = "access",
        accessTokenExpiresAt = "2026-08-12T12:00:00Z",
        refreshToken = "refresh",
        refreshTokenExpiresAt = "2026-09-12T12:00:00Z",
        sessionId = UUID.fromString("44444444-4444-4444-8444-444444444444"),
        deviceId = UUID.fromString("55555555-5555-4555-8555-555555555555"),
        authorizationScope = AuthorizationScope.GLOBAL_ADMIN,
        category = null,
    )
}

private class V20CredentialStore(
    private val issuer: String,
    private val tokens: TokenPair,
) : CredentialStore {
    override suspend fun load(issuer: String): StoredCredentials? =
        if (issuer == this.issuer) StoredCredentials(issuer, tokens) else null

    override suspend fun save(credentials: StoredCredentials) = Unit

    override suspend fun clear(issuer: String) = Unit
}
