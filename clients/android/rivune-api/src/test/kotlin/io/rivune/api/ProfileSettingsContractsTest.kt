package io.rivune.api
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertContentEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNull
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.async
import kotlinx.coroutines.runBlocking
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.mockwebserver.MockResponse
import okhttp3.Interceptor
import okhttp3.Protocol
import okhttp3.Response
import okhttp3.ResponseBody.Companion.toResponseBody
import okhttp3.mockwebserver.MockWebServer

class ProfileSettingsContractsTest {
    private val profileId = UUID.fromString("11111111-1111-4111-8111-111111111111")

    @Test
    fun effectiveSettingsDecodePreferencesAndUpdateSendsExactAuthenticatedProfilePatch() = runBlocking {
        val server = MockWebServer()
        server.enqueue(jsonResponse(
            """{"name":"Rivune","serverVersion":"test","protocolVersion":22,"apiBaseUrl":"/api/v1","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en"}""",
        ))
        server.enqueue(jsonResponse(effectiveSettingsFixture()))
        server.enqueue(jsonResponse(settingsLayerFixture()))
        server.enqueue(jsonResponse(settingsLayerFixture()))
        server.enqueue(jsonResponse(settingsLayerFixture()))
        server.enqueue(jsonResponse(settingsLayerFixture()))
        server.start()
        try {
            val client = RivuneApiClient(
                serverUrl = server.loopbackUrl("/").newBuilder().host("127.0.0.1").build().toString(),
                credentialStore = ProfileSettingsCredentialStore(tokenPair(), "profile-context"),
            )

            val effective = client.effectiveProfileSettings(profileId)
            assertEquals("1080p", effective.settings.maximumResolution)
            assertEquals(true, effective.settings.preferDirectPlay)
            assertEquals("fr-FR", effective.settings.metadataLanguage)
            assertEquals("profile", effective.sources.metadataLanguage)
            assertEquals("fr", effective.settings.audioLanguage)
            assertEquals("en", effective.settings.subtitleLanguage)
            assertEquals("es", effective.settings.forcedSubtitleLanguage)
            assertEquals(true, effective.settings.autoplayNextEpisode)
            assertEquals(true, effective.settings.skipIntroEnabled)
            assertEquals(false, effective.settings.skipRecapEnabled)
            assertEquals(true, effective.settings.skipOutroEnabled)
            assertEquals("instance", effective.sources.allowTranscoding)
            assertEquals("instance", effective.sources.transcoding)
            assertEquals("instance", effective.sources.maximumCastMembers)
            assertEquals("profile", effective.sources.maximumResolution)
            assertEquals("profile", effective.sources.preferDirectPlay)
            assertEquals("profile", effective.sources.audioLanguage)
            assertEquals("profile", effective.sources.subtitleLanguage)
            assertEquals("profile", effective.sources.forcedSubtitleLanguage)
            assertEquals("profile", effective.sources.autoplayNextEpisode)
            assertEquals("profile", effective.sources.skipIntroEnabled)
            assertEquals("instance", effective.sources.skipRecapEnabled)
            assertEquals("default", effective.sources.skipOutroEnabled)

            val updated = client.updateProfileSettings(
                profileId,
                ProfileSettingsUpdate(
                    maximumResolution = PatchField.Value("2160p"),
                    preferDirectPlay = PatchField.Value(false),
                    audioLanguage = PatchField.Value("en"),
                    metadataLanguage = PatchField.Value("de-DE"),
                    subtitleLanguage = PatchField.Value("fr"),
                    forcedSubtitleLanguage = PatchField.Value("es"),
                    autoplayNextEpisode = PatchField.Value(false),
                    skipIntroEnabled = PatchField.Value(false),
                    skipRecapEnabled = PatchField.Value(true),
                    skipOutroEnabled = PatchField.Value(false),
                    transcoding = PatchField.Value("disabled"),
                ),
            )
            assertEquals("2160p", updated.settings.maximumResolution)
            assertEquals(false, updated.settings.preferDirectPlay)
            assertEquals("de-DE", updated.settings.metadataLanguage)
            assertEquals("en", updated.settings.audioLanguage)
            assertEquals("fr", updated.settings.subtitleLanguage)
            assertEquals("es", updated.settings.forcedSubtitleLanguage)
            assertEquals(false, updated.settings.autoplayNextEpisode)
            assertEquals(false, updated.settings.skipIntroEnabled)
            assertEquals(true, updated.settings.skipRecapEnabled)
            assertEquals(false, updated.settings.skipOutroEnabled)
            assertEquals("disabled", updated.settings.transcoding)

            client.updateProfileSettings(
                profileId,
                ProfileSettingsUpdate(
                    maximumResolution = PatchField.Null,
                    preferDirectPlay = PatchField.Null,
                    audioLanguage = PatchField.Null,
                    metadataLanguage = PatchField.Null,
                    subtitleLanguage = PatchField.Null,
                    forcedSubtitleLanguage = PatchField.Null,
                    autoplayNextEpisode = PatchField.Null,
                    skipIntroEnabled = PatchField.Null,
                    skipRecapEnabled = PatchField.Null,
                    skipOutroEnabled = PatchField.Null,
                    transcoding = PatchField.Null,
                ),
            )
            client.updateProfileSettings(
                profileId,
                ProfileSettingsUpdate(autoplayNextEpisode = PatchField.Value(true)),
            )
            client.updateProfileSettings(
                profileId,
                ProfileSettingsUpdate(metadataLanguage = PatchField.Value("es-ES")),
            )

            assertEquals("/.well-known/rivune", server.takeRequest().path)
            val get = server.takeRequest()
            assertEquals("GET", get.method)
            assertEquals("/api/v1/profiles/$profileId/settings/effective", get.path)
            assertEquals("Bearer settings-access", get.getHeader("Authorization"))
            assertEquals("profile-context", get.getHeader("X-Rivune-Profile-Context"))

            val patch = server.takeRequest()
            assertEquals("PATCH", patch.method)
            assertEquals("/api/v1/profiles/$profileId/settings", patch.path)
            assertEquals("Bearer settings-access", patch.getHeader("Authorization"))
            assertEquals("profile-context", patch.getHeader("X-Rivune-Profile-Context"))
            assertEquals(
                """{"maximumResolution":"2160p","preferDirectPlay":false,"audioLanguage":"en","metadataLanguage":"de-DE","subtitleLanguage":"fr","forcedSubtitleLanguage":"es","autoplayNextEpisode":false,"skipIntroEnabled":false,"skipRecapEnabled":true,"skipOutroEnabled":false,"transcoding":"disabled"}""",
                patch.body.readUtf8(),
            )
            val resetPatch = server.takeRequest()
            assertEquals("PATCH", resetPatch.method)
            assertEquals(
                """{"maximumResolution":null,"preferDirectPlay":null,"audioLanguage":null,"metadataLanguage":null,"subtitleLanguage":null,"forcedSubtitleLanguage":null,"autoplayNextEpisode":null,"skipIntroEnabled":null,"skipRecapEnabled":null,"skipOutroEnabled":null,"transcoding":null}""",
                resetPatch.body.readUtf8(),
            )
            val autoplayPatch = server.takeRequest()
            assertEquals("PATCH", autoplayPatch.method)
            assertEquals("""{"autoplayNextEpisode":true}""", autoplayPatch.body.readUtf8())
            val omittedPatch = server.takeRequest()
            assertEquals("PATCH", omittedPatch.method)
            assertEquals("""{"metadataLanguage":"es-ES"}""", omittedPatch.body.readUtf8())
            assertEquals(6, server.requestCount)
        } finally {
            server.shutdown()
        }
    }
    @Test
    fun customProfileAvatarUsesAuthenticatedBoundedRequest() = runBlocking {
        val server = MockWebServer()
        val avatar = byteArrayOf(0x52, 0x49, 0x56, 0x55, 0x4e, 0x45)
        server.enqueue(jsonResponse(
            """{"name":"Rivune","serverVersion":"test","protocolVersion":22,"apiBaseUrl":"/api/v1","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en"}""",
        ))
        server.enqueue(MockResponse().setHeader("Content-Type", "image/png").setBody(okio.Buffer().write(avatar)))
        server.start()
        try {
            val client = RivuneApiClient(
                serverUrl = server.loopbackUrl("/").newBuilder().host("127.0.0.1").build().toString(),
                credentialStore = ProfileSettingsCredentialStore(tokenPair(), "profile-context"),
            )

            assertContentEquals(avatar, client.profileAvatar(profileId))

            server.takeRequest()
            val request = server.takeRequest()
            assertEquals("GET", request.method)
            assertEquals("/api/v1/profiles/$profileId/avatar", request.path)
            assertEquals("Bearer settings-access", request.getHeader("Authorization"))
            assertEquals("image/*", request.getHeader("Accept"))
            assertNull(request.getHeader("X-Rivune-Profile-Context"))
        } finally {
            server.shutdown()
        }
    }
    @Test
    fun customProfileAvatarRejectsOversizedBodies() = runBlocking {
        val server = MockWebServer()
        server.enqueue(jsonResponse(
            """{"name":"Rivune","serverVersion":"test","protocolVersion":22,"apiBaseUrl":"/api/v1","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en"}""",
        ))
        server.enqueue(MockResponse().setHeader("Content-Type", "image/png").setBody(
            okio.Buffer().write(ByteArray(2 * 1024 * 1024 + 1)),
        ))
        server.start()
        try {
            val client = RivuneApiClient(
                serverUrl = server.loopbackUrl("/").newBuilder().host("127.0.0.1").build().toString(),
                credentialStore = ProfileSettingsCredentialStore(tokenPair(), "profile-context"),
            )

            assertFailsWith<RivuneApiException.ResponseTooLarge> { client.profileAvatar(profileId) }
        } finally {
            server.shutdown()
        }
        Unit
    }
    @Test
    fun customAvatarResponseSurvivesConcurrentProfileSelection() = runBlocking {
        val avatar = byteArrayOf(1, 2, 3, 4)
        val entered = CountDownLatch(1)
        val release = CountDownLatch(1)
        val discovery = """{"name":"Rivune","serverVersion":"test","protocolVersion":22,"apiBaseUrl":"/api/v1","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en"}"""
        val selection = """{"profile":{"id":"$profileId","name":"Viewer","description":null,"categoryId":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","category":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Default","color":null,"icon":null},"isChild":false,"hasPin":false,"canManage":true,"enabled":true,"availableFrom":null,"availableUntil":null,"accessStartTime":null,"accessEndTime":null,"accessTimezone":"UTC","accessible":true,"avatar":{"kind":"custom","presetId":null,"url":"/api/v1/profiles/$profileId/avatar"}},"expiresAt":"2099-01-01T00:00:00Z","profileContext":"new-context"}"""
        val client = RivuneApiClient(
            serverUrl = "http://127.0.0.1/",
            credentialStore = ProfileSettingsCredentialStore(tokenPair(), "profile-context"),
            httpClient = okhttp3.OkHttpClient.Builder().addInterceptor(Interceptor { chain ->
                val request = chain.request()
                val body = when {
                    request.url.encodedPath == "/.well-known/rivune" -> discovery.toResponseBody("application/json".toMediaType())
                    request.url.encodedPath.endsWith("/avatar") -> {
                        entered.countDown()
                        check(release.await(5, TimeUnit.SECONDS))
                        avatar.toResponseBody("image/png".toMediaType())
                    }
                    else -> selection.toResponseBody("application/json".toMediaType())
                }
                Response.Builder().request(request).protocol(Protocol.HTTP_1_1).code(200).message("OK").body(body).build()
            }).build(),
        )

        client.discover()
        val pendingAvatar = async(start = CoroutineStart.UNDISPATCHED) { client.profileAvatar(profileId) }
        check(entered.await(5, TimeUnit.SECONDS))
        client.selectProfile(profileId)
        release.countDown()

        assertContentEquals(avatar, pendingAvatar.await())
    }




    private fun effectiveSettingsFixture() =
        """{"schemaVersion":1,"settings":{"allowTranscoding":true,"transcoding":"enabled","maximumCastMembers":5,"maximumResolution":"1080p","preferDirectPlay":true,"audioLanguage":"fr","subtitleLanguage":"en","forcedSubtitleLanguage":"es","autoplayNextEpisode":true,"skipIntroEnabled":true,"skipRecapEnabled":false,"skipOutroEnabled":true,"metadataLanguage":"fr-FR"},"sources":{"allowTranscoding":"instance","transcoding":"instance","maximumCastMembers":"instance","maximumResolution":"profile","preferDirectPlay":"profile","audioLanguage":"profile","subtitleLanguage":"profile","forcedSubtitleLanguage":"profile","autoplayNextEpisode":"profile","skipIntroEnabled":"profile","skipRecapEnabled":"instance","skipOutroEnabled":"default","metadataLanguage":"profile"}}"""

    private fun settingsLayerFixture() =
        """{"schemaVersion":1,"settings":{"maximumResolution":"2160p","preferDirectPlay":false,"audioLanguage":"en","subtitleLanguage":"fr","forcedSubtitleLanguage":"es","autoplayNextEpisode":false,"skipIntroEnabled":false,"skipRecapEnabled":true,"skipOutroEnabled":false,"metadataLanguage":"de-DE","transcoding":"disabled"},"updatedAt":"2026-08-13T12:00:00Z"}"""

    private fun jsonResponse(body: String) = MockResponse()
        .setHeader("Content-Type", "application/json")
        .setBody(body)

    private fun tokenPair() = TokenPair(
        tokenType = "Bearer",
        accessToken = "settings-access",
        accessTokenExpiresAt = "2099-08-13T13:00:00Z",
        refreshToken = "settings-refresh",
        refreshTokenExpiresAt = "2099-09-13T12:00:00Z",
        sessionId = UUID.fromString("22222222-2222-4222-8222-222222222222"),
        deviceId = UUID.fromString("33333333-3333-4333-8333-333333333333"),
        authorizationScope = AuthorizationScope.GLOBAL_ADMIN,
        category = null,
    )
}

private class ProfileSettingsCredentialStore(
    private val tokens: TokenPair,
    private val profileContext: String,
) : CredentialStore {
    override suspend fun load(issuer: String): StoredCredentials = StoredCredentials(issuer, tokens, profileContext)
    override suspend fun save(credentials: StoredCredentials) = Unit
    override suspend fun clear(issuer: String) = Unit
}
