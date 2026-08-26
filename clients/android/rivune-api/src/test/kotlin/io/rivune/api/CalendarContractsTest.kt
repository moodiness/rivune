package io.rivune.api

import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlinx.coroutines.runBlocking
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer

class CalendarContractsTest {
    private val movieTitleId = UUID.fromString("11111111-1111-4111-8111-111111111111")
    private val episodeTitleId = UUID.fromString("22222222-2222-4222-8222-222222222222")
    private val seriesId = UUID.fromString("33333333-3333-4333-8333-333333333333")
    private val seasonId = UUID.fromString("44444444-4444-4444-8444-444444444444")

    @Test
    fun calendarUsesAuthenticatedProfileContextAndDecodesMovieAndEpisodeEvents() = runBlocking {
        val server = MockWebServer()
        server.enqueue(discoveryResponse())
        server.enqueue(jsonResponse(calendarFixture()))
        server.start()
        try {
            val client = RivuneApiClient(
                serverUrl = server.loopbackUrl("/").newBuilder().host("127.0.0.1").build().toString(),
                credentialStore = CalendarCredentialStore(tokenPair(), "profile-context"),
            )

            val events = client.calendar("2026-09-01", "2026-09-30", "fr-CA")

            assertEquals(
                CalendarEvent(
                    id = "movie:$movieTitleId:2026-09-05",
                    titleId = movieTitleId,
                    mediaType = CalendarEventMediaType.MOVIE,
                    title = "Le Film",
                    releaseDate = "2026-09-05",
                    posterUrl = "/api/v1/artwork/movie",
                    resourceId = "movie-resource",
                    resourceProvider = "tmdb",
                ),
                events[0],
            )
            assertNull(events[0].seriesTitle)
            assertNull(events[0].seriesId)
            assertNull(events[0].seasonId)
            assertNull(events[0].seasonNumber)
            assertNull(events[0].episodeNumber)
            assertEquals(
                CalendarEvent(
                    id = "episode:$episodeTitleId:2026-09-12",
                    titleId = episodeTitleId,
                    mediaType = CalendarEventMediaType.EPISODE,
                    title = "L'épisode",
                    releaseDate = "2026-09-12",
                    posterUrl = "/api/v1/artwork/episode",
                    resourceId = "episode-resource",
                    resourceProvider = "tvdb",
                    seriesTitle = "La Série",
                    seriesId = seriesId,
                    seasonId = seasonId,
                    seasonNumber = 2,
                    episodeNumber = 3,
                ),
                events[1],
            )

            assertEquals("/.well-known/rivune", server.takeRequest().path)
            val request = server.takeRequest()
            assertEquals("GET", request.method)
            assertEquals(
                "/api/v1/calendar?from=2026-09-01&to=2026-09-30&language=fr-CA",
                request.path,
            )
            assertEquals("Bearer calendar-access", request.getHeader("Authorization"))
            assertEquals("profile-context", request.getHeader("X-Rivune-Profile-Context"))
            assertEquals(2, server.requestCount)
        } finally {
            server.shutdown()
        }
    }

    private fun discoveryResponse() = jsonResponse(
        """{"name":"Rivune","serverVersion":"test","protocolVersion":22,"apiBaseUrl":"/api/v1","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en"}""",
    )

    private fun calendarFixture() =
        """{"events":[{"id":"movie:$movieTitleId:2026-09-05","titleId":"$movieTitleId","mediaType":"movie","title":"Le Film","releaseDate":"2026-09-05","posterUrl":"/api/v1/artwork/movie","resourceId":"movie-resource","resourceProvider":"tmdb"},{"id":"episode:$episodeTitleId:2026-09-12","titleId":"$episodeTitleId","mediaType":"episode","title":"L'épisode","releaseDate":"2026-09-12","posterUrl":"/api/v1/artwork/episode","resourceId":"episode-resource","resourceProvider":"tvdb","seriesTitle":"La Série","seriesId":"$seriesId","seasonId":"$seasonId","seasonNumber":2,"episodeNumber":3}]}"""

    private fun jsonResponse(body: String) = MockResponse()
        .setHeader("Content-Type", "application/json")
        .setBody(body)

    private fun tokenPair() = TokenPair(
        tokenType = "Bearer",
        accessToken = "calendar-access",
        accessTokenExpiresAt = "2026-09-01T12:00:00Z",
        refreshToken = "calendar-refresh",
        refreshTokenExpiresAt = "2026-10-01T12:00:00Z",
        sessionId = UUID.fromString("55555555-5555-4555-8555-555555555555"),
        deviceId = UUID.fromString("66666666-6666-4666-8666-666666666666"),
        authorizationScope = AuthorizationScope.GLOBAL_ADMIN,
        category = null,
    )
}

private class CalendarCredentialStore(
    private val tokens: TokenPair,
    private val profileContext: String,
) : CredentialStore {
    override suspend fun load(issuer: String): StoredCredentials = StoredCredentials(issuer, tokens, profileContext)
    override suspend fun save(credentials: StoredCredentials) = Unit
    override suspend fun clear(issuer: String) = Unit
}
