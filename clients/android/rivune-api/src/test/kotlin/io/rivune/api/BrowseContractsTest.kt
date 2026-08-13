package io.rivune.api

import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer

class BrowseContractsTest {
    private val json = Json { ignoreUnknownKeys = true }
    private val collectionId = UUID.fromString("11111111-1111-4111-8111-111111111111")
    private val folderId = UUID.fromString("22222222-2222-4222-8222-222222222222")
    private val addonId = UUID.fromString("33333333-3333-4333-8333-333333333333")
    private val sourceId = UUID.fromString("44444444-4444-4444-8444-444444444444")
    private val titleId = UUID.fromString("55555555-5555-4555-8555-555555555555")

    @Test
    fun collectionsAndResolvedFoldersDecodeTheFullContract() = runBlocking {
        val server = MockWebServer()
        server.enqueue(discoveryResponse())
        server.enqueue(jsonResponse("""{"collections":[${collectionFixture()}]}"""))
        server.enqueue(jsonResponse(folderFixture()))
        server.start()
        try {
            val client = client(server)
            val collection = client.collections().single()
            assertEquals(CollectionViewMode.TABBED_GRID, collection.viewMode)
            assertEquals(CollectionTileShape.LANDSCAPE, collection.folderCoverShape)
            assertEquals(listOf(CollectionSourceKind.ADDON_CATALOG, CollectionSourceKind.TMDB, CollectionSourceKind.TRAKT, CollectionSourceKind.MDBLIST), collection.folders.single().sources.map { it.kind })
            assertEquals(9_223_372_036_854_775_000L, collection.folders.single().sources[1].tmdb?.tmdbId)
            assertEquals(listOf(11L, 12L), collection.folders.single().sources[1].tmdb?.filters?.genres)
            assertEquals(8_888_888_888L, collection.folders.single().sources[2].trakt?.listId)

            val resolved = client.resolveCollectionFolder(collectionId, folderId, page = 2, limit = 50, language = "fr-FR", region = "CA")
            assertEquals(2, resolved.page)
            assertTrue(resolved.hasMore)
            assertEquals("/api/v1/artwork/source", resolved.sourcePosterUrls?.get(sourceId.toString()))
            val item = resolved.items.single()
            assertEquals("nested", item.raw?.jsonObject?.get("provider")?.jsonObject?.get("name")?.jsonPrimitive?.content)
            assertEquals(true, item.raw?.jsonObject?.get("flags")?.jsonArray?.get(0)?.jsonPrimitive?.boolean)
            assertEquals(CollectionSourceFailureCode.COLLECTION_SOURCE_TIMEOUT, resolved.errors.single().code)

            assertEquals("/.well-known/rivune", server.takeRequest().path)
            assertEquals("/api/v1/collections", server.takeRequest().path)
            assertEquals("/api/v1/collections/$collectionId/folders/$folderId/items?page=2&limit=50&language=fr-FR&region=CA", server.takeRequest().path)
        } finally {
            server.shutdown()
        }
    }

    @Test
    fun catalogsAndResourcesPreserveOpaquePathsOrderedQueriesAndDynamicPayloads() = runBlocking {
        val payload = """{"metas":[{"id":"opaque","nested":{"array":[1,true,null,{"future":"field"}]}}],"sdkCache":{"etag":"abc"}}"""
        val resource = """{"addonId":"$addonId","manifestId":"org.example","resource":"meta/custom","type":"tv/雪","id":"id/雪?x","payload":$payload,"cache":{"maxAgeSeconds":9223372036854775000,"staleWhileRevalidateSeconds":30,"staleIfErrorSeconds":60},"extra":[{"name":"genre","value":"Drama"},{"name":"genre","value":"Sci Fi"}]}"""
        val server = MockWebServer()
        server.enqueue(discoveryResponse())
        server.enqueue(jsonResponse("""{"catalogs":[{"addonId":"$addonId","addonName":"Example","addonLogoUrl":"/api/v1/artwork/${"a".repeat(64)}","manifestId":"org.example","position":7,"catalog":{"type":"tv/雪","id":"featured","name":"Featured","genres":["Drama"],"extra":[{"name":"search","isRequired":true,"default":"all","options":["all"],"optionsLimit":2}],"extraRequired":["search"],"extraSupported":["skip","limit"]},"addonCatalog":false,"searchable":true}]}"""))
        server.enqueue(jsonResponse("""{"results":[$resource],"errors":[{"addonId":"$addonId","manifestId":"org.failed","code":"addon_timeout","message":"Timed out"}]}"""))
        server.enqueue(jsonResponse(resource))
        server.enqueue(jsonResponse("""{"results":[$resource],"errors":[]}"""))
        server.start()
        try {
            val client = client(server)
            assertEquals(2, client.addonCatalogs().single().catalog.extra?.single()?.optionsLimit)
            val search = client.searchAddonCatalogs("tv/雪", "café noir", 3, 24, listOf("genre" to "Drama", "genre" to "Sci Fi"))
            assertEquals("field", search.results.single().payload["metas"]?.jsonArray?.single()?.jsonObject?.get("nested")?.jsonObject?.get("array")?.jsonArray?.get(3)?.jsonObject?.get("future")?.jsonPrimitive?.content)
            assertEquals(9_223_372_036_854_775_000L, search.results.single().cache.maxAgeSeconds)
            assertEquals("addon_timeout", search.errors.single().code)
            val exact = client.addonResource(addonId, "meta/custom", "tv/雪", "id/雪?x", skip = 0, limit = 24, extras = listOf("genre" to "Drama", "genre" to "Sci Fi"))
            assertEquals(listOf("Drama", "Sci Fi"), exact.extra?.map { it.value })
            client.addonResources("meta/custom", "tv/雪", "id/雪?x", listOf("genre" to "Drama", "genre" to "Sci Fi"))

            server.takeRequest()
            assertEquals("/api/v1/addons/catalogs", server.takeRequest().path)
            assertEquals("/api/v1/addons/catalogs/search/tv%2F%E9%9B%AA?search=caf%C3%A9%20noir&skip=3&limit=24&genre=Drama&genre=Sci%20Fi", server.takeRequest().path)
            assertEquals("/api/v1/addons/$addonId/resource/meta%2Fcustom/tv%2F%E9%9B%AA/id%2F%E9%9B%AA%3Fx?skip=0&limit=24&genre=Drama&genre=Sci%20Fi", server.takeRequest().path)
            assertEquals("/api/v1/addons/resources/meta%2Fcustom/tv%2F%E9%9B%AA/id%2F%E9%9B%AA%3Fx?genre=Drama&genre=Sci%20Fi", server.takeRequest().path)
        } finally {
            server.shutdown()
        }
    }

    @Test
    fun titleLibraryAndNotificationsUseExactBodiesCursorsAndMutationRoutes() = runBlocking {
        val libraryItem = libraryItemFixture()
        val server = MockWebServer()
        server.enqueue(discoveryResponse())
        server.enqueue(jsonResponse(titleReferenceFixture()))
        server.enqueue(jsonResponse(customSeriesResultFixture()))
        server.enqueue(jsonResponse("""{"items":[$libraryItem],"page":2,"totalPages":4,"totalResults":71}"""))
        server.enqueue(jsonResponse("""{"items":[{"sourceAddonId":"$addonId","resourceId":"channel/雪","titleId":"$titleId"}]}"""))
        server.enqueue(jsonResponse(libraryItem))
        server.enqueue(MockResponse().setResponseCode(204))
        server.enqueue(jsonResponse("""{"notifications":[{"id":"9007199254740993","message":"Maintenance soon","senderUsername":"admin","createdAt":"2026-08-12T10:00:00Z"}]}"""))
        server.enqueue(MockResponse().setResponseCode(204))
        server.start()
        try {
            val client = client(server)
            val title = client.resolveTitle(TitleResolveInput(TitleMediaType.TV, "addon", resourceId = "channel/雪", title = "News & More", posterUrl = "/api/v1/artwork/${"b".repeat(64)}", sourceAddonId = addonId, sourceCatalogId = "live channels", sourceName = "Example", country = "CA", language = "fr", category = "News"))
            assertEquals(titleId, title.titleId)
            val custom = client.resolveCustomSeries(CustomSeriesResolveInput(addonId, "anime/custom", CustomSeriesSnapshot("series/雪", "Series", "/api/v1/artwork/${"c".repeat(64)}"), listOf(CustomVideoSnapshot("episode/1", "Pilot", 1, 1, released = "2026-08-12"))))
            assertEquals(1, custom.videos.single().episodeNumber)
            val page = client.library(TitleMediaType.TV, 2, 20)
            assertFalse(page.items.single().available)
            assertEquals(71, page.totalResults)
            assertEquals(titleId, client.tvLibraryMembership(listOf(TVLibraryIdentity(addonId, "channel/雪"))).items.single().titleId)
            assertEquals(titleId, client.addLibraryTitle(titleId).titleId)
            client.removeLibraryTitle(titleId)
            val notification = client.sessionNotifications("9223372036854775807").single()
            assertEquals("9007199254740993", notification.id)
            client.acknowledgeSessionNotification(notification.id)

            server.takeRequest()
            val titleRequest = server.takeRequest()
            assertEquals("POST" to "/api/v1/titles/resolve", titleRequest.method to titleRequest.path)
            val titleBody = json.parseToJsonElement(titleRequest.body.readUtf8()).jsonObject
            assertEquals("tv", titleBody.getValue("mediaType").jsonPrimitive.content)
            assertEquals(addonId.toString(), titleBody.getValue("sourceAddonId").jsonPrimitive.content)
            assertNull(titleBody["externalId"])
            val customRequest = server.takeRequest()
            assertEquals("POST" to "/api/v1/titles/custom-series/resolve", customRequest.method to customRequest.path)
            assertEquals("2026-08-12", json.parseToJsonElement(customRequest.body.readUtf8()).jsonObject.getValue("videos").jsonArray.single().jsonObject.getValue("released").jsonPrimitive.content)
            assertEquals("GET" to "/api/v1/library?mediaType=tv&page=2&pageSize=20", server.takeRequest().let { it.method to it.path })
            val membershipRequest = server.takeRequest()
            assertEquals("POST" to "/api/v1/library/membership", membershipRequest.method to membershipRequest.path)
            assertEquals("channel/雪", json.parseToJsonElement(membershipRequest.body.readUtf8()).jsonObject.getValue("identities").jsonArray.single().jsonObject.getValue("resourceId").jsonPrimitive.content)
            assertEquals("PUT" to "/api/v1/library/$titleId", server.takeRequest().let { it.method to it.path })
            assertEquals("DELETE" to "/api/v1/library/$titleId", server.takeRequest().let { it.method to it.path })
            assertEquals("GET" to "/api/v1/auth/notifications?after=9223372036854775807", server.takeRequest().let { it.method to it.path })
            assertEquals("DELETE" to "/api/v1/auth/notifications/9007199254740993", server.takeRequest().let { it.method to it.path })
        } finally {
            server.shutdown()
        }
    }

    @Test
    fun profileContextIsPersistedClearedAndRetainedAcrossRefreshRetry() = runBlocking {
        val profileId = UUID.fromString("66666666-6666-4666-8666-666666666666")
        val store = MutableCredentialStore(tokenPair("initial"))
        val server = MockWebServer()
        listOf(
            discoveryResponse(),
            jsonResponse(profileSelectionFixture("context-one", profileId)),
            jsonResponse("""{"collections":[]}"""),
            MockResponse().setResponseCode(204),
            jsonResponse("""{"collections":[]}"""),
            jsonResponse(profileSelectionFixture("context-two", profileId)),
            MockResponse().setResponseCode(401).setBody("""{"error":{"code":"expired","message":"Expired"}}"""),
            jsonResponse(tokenPairJson("refreshed")),
            jsonResponse("""{"collections":[]}"""),
            jsonResponse(tokenPairJson("login")),
            jsonResponse("""{"collections":[]}"""),
            jsonResponse(profileSelectionFixture("context-three", profileId)),
            jsonResponse(tokenPairJson("device")),
            jsonResponse("""{"collections":[]}"""),
        ).forEach(server::enqueue)
        server.start()
        try {
            val client = RivuneApiClient(server.loopbackUrl("/").toString(), store)
            client.selectProfile(profileId)
            client.collections()
            client.clearProfileSelection()
            client.collections()
            client.selectProfile(profileId)
            client.collections()
            client.login("alice", "password", LoginDevice(name = "Phone", platform = "android"))
            client.collections()
            client.selectProfile(profileId)
            client.exchangeDeviceAuthorization("device-code")
            client.collections()

            server.takeRequest()
            assertNull(server.takeRequest().getHeader(PROFILE_CONTEXT_HEADER))
            assertEquals("context-one", server.takeRequest().getHeader(PROFILE_CONTEXT_HEADER))
            assertNull(server.takeRequest().getHeader(PROFILE_CONTEXT_HEADER))
            assertNull(server.takeRequest().getHeader(PROFILE_CONTEXT_HEADER))
            server.takeRequest()
            assertEquals("context-two", server.takeRequest().getHeader(PROFILE_CONTEXT_HEADER))
            val refresh = server.takeRequest()
            assertEquals("/api/v1/auth/refresh", refresh.path)
            assertNull(refresh.getHeader(PROFILE_CONTEXT_HEADER))
            val retry = server.takeRequest()
            assertEquals("Bearer refreshed-access", retry.getHeader("Authorization"))
            assertEquals("context-two", retry.getHeader(PROFILE_CONTEXT_HEADER))
            assertNull(server.takeRequest().getHeader(PROFILE_CONTEXT_HEADER))
            assertNull(server.takeRequest().getHeader(PROFILE_CONTEXT_HEADER))
            server.takeRequest()
            assertNull(server.takeRequest().getHeader(PROFILE_CONTEXT_HEADER))
            assertNull(server.takeRequest().getHeader(PROFILE_CONTEXT_HEADER))
            assertEquals("device-access", store.savedAccessTokens.last())
        } finally {
            server.shutdown()
        }
    }

    @Test
    fun responseResourceUrlsResolveAgainstServerOriginWithoutRequests() {
        val server = MockWebServer()
        server.start()
        try {
            val client = client(server)
            assertEquals(server.loopbackUrl("/api/v1/artwork/key"), client.resolveResponseResourceUrl("/api/v1/artwork/key"))
            assertEquals("https://cdn.example.test/image.jpg", client.resolveResponseResourceUrl("https://cdn.example.test/image.jpg")?.toString())
            assertNull(client.resolveResponseResourceUrl("http://cdn.example.test/image.jpg"))
            assertNull(client.resolveResponseResourceUrl("//cdn.example.test/image.jpg"))
            assertEquals(0, server.requestCount)
        } finally {
            server.shutdown()
        }
    }

    @Test
    fun responseArtworkUrlsStayOnServerOriginWithoutRequests() {
        val server = MockWebServer()
        server.start()
        try {
            val client = client(server)
            assertEquals(server.loopbackUrl("/api/v1/artwork/key"), client.resolveResponseArtworkUrl("/api/v1/artwork/key"))
            assertEquals(server.loopbackUrl("/api/v1/artwork/key"), client.resolveResponseArtworkUrl(server.loopbackUrl("/api/v1/artwork/key").toString()))
            assertNull(client.resolveResponseArtworkUrl("https://cdn.example.test/image.jpg"))
            assertNull(client.resolveResponseArtworkUrl("//127.0.0.1/private"))
            assertNull(client.resolveResponseArtworkUrl("https://user:secret@cdn.example.test/image.jpg"))
            assertEquals(0, server.requestCount)
        } finally {
            server.shutdown()
        }
    }

    private fun client(server: MockWebServer) = RivuneApiClient(server.loopbackUrl("/").toString(), MutableCredentialStore(tokenPair("access")))
    private fun discoveryResponse() = jsonResponse("""{"name":"Rivune","serverVersion":"test","protocolVersion":20,"apiBaseUrl":"/api/v1","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en"}""")

    private fun collectionFixture() = """{"id":"$collectionId","title":"Home","backdropImageUrl":"/api/v1/artwork/home","heroEnabled":true,"pinToTop":true,"focusGlowEnabled":false,"viewMode":"tabbed_grid","folderCoverShape":"landscape","folders":[{"id":"$folderId","title":"Featured","tileShape":"poster","sourceView":"merged","coverImageUrl":"/api/v1/artwork/cover","focusGifEnabled":true,"hideTitle":false,"sources":[{"id":"$sourceId","kind":"addon_catalog","title":"Addon","addonCatalog":{"addonId":"$addonId","manifestId":"org.example","type":"movie","catalogId":"featured","extra":[{"name":"genre","value":"Drama"}]}},{"id":"77777777-7777-4777-8777-777777777777","kind":"tmdb","title":"TMDB","tmdb":{"sourceType":"list","tmdbId":9223372036854775000,"mediaType":"movie","sort":"popularity.desc","filters":{"genres":[11,12]}}},{"id":"88888888-8888-4888-8888-888888888888","kind":"trakt","title":"Trakt","trakt":{"listId":8888888888,"mediaType":"series","sortBy":"votes","sortHow":"desc"}},{"id":"99999999-9999-4999-8999-999999999999","kind":"mdblist","title":"MDBList","mdblist":{"listId":9999999999,"mediaType":"movie","sort":"score_average","order":"asc"}}]}],"profileIds":["66666666-6666-4666-8666-666666666666"],"categoryIds":["aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"],"position":0,"version":1,"createdAt":"2026-08-12T09:00:00Z","updatedAt":"2026-08-12T10:00:00Z"}"""
    private fun folderFixture() = """{"collectionId":"$collectionId","folder":{"id":"$folderId","title":"Featured","tileShape":"poster","sourceView":"merged","focusGifEnabled":false,"hideTitle":false,"sources":[{"id":"$sourceId","kind":"addon_catalog","title":"Addon","addonCatalog":{"addonId":"$addonId","type":"movie","catalogId":"featured"}}]},"sourcePosterUrls":{"$sourceId":"/api/v1/artwork/source"},"items":[{"id":"opaque/雪","mediaType":"custom","title":"Title","externalIds":{"imdb":"tt1234567"},"sources":[{"id":"$sourceId","kind":"addon_catalog","title":"Addon","addonId":"$addonId","manifestId":"org.example","catalogId":"featured"}],"raw":{"provider":{"name":"nested"},"flags":[true,null,3]}}],"page":2,"hasMore":true,"errors":[{"sourceId":"$sourceId","kind":"addon_catalog","code":"collection_source_timeout","message":"Timed out"}]}"""
    private fun titleReferenceFixture() = """{"titleId":"$titleId","mediaType":"tv","provider":"addon","externalId":"$addonId:channel/雪","resourceId":"channel/雪","title":"News & More","sourceAddonId":"$addonId"}"""
    private fun customSeriesResultFixture() = """{"series":{"titleId":"$titleId","resourceId":"series/雪"},"seasons":[{"titleId":"77777777-7777-4777-8777-777777777777","seasonNumber":1}],"videos":[{"titleId":"88888888-8888-4888-8888-888888888888","resourceId":"episode/1","seasonTitleId":"77777777-7777-4777-8777-777777777777","seasonNumber":1,"episodeNumber":1}]}"""
    private fun libraryItemFixture() = """{"titleId":"$titleId","mediaType":"tv","provider":"addon","externalId":"$addonId:channel/雪","resourceId":"channel/雪","title":"News & More","sourceAddonId":"$addonId","available":false,"addedAt":"2026-08-12T09:00:00Z","updatedAt":"2026-08-12T10:00:00Z"}"""
    private fun profileSelectionFixture(context: String, profileId: UUID) = """{"profile":{"id":"$profileId","name":"Viewer","description":null,"categoryId":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","category":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Default","color":null,"icon":null},"isChild":false,"hasPin":false,"canManage":true,"enabled":true,"availableFrom":null,"availableUntil":null,"accessStartTime":null,"accessEndTime":null,"accessTimezone":"UTC","accessible":true,"avatar":{"kind":"preset","presetId":"one","url":"/api/v1/avatar"}},"expiresAt":"2026-08-12T12:00:00Z","profileContext":"$context"}"""

    private fun tokenPair(prefix: String) = TokenPair("Bearer", "$prefix-access", "2026-08-12T12:00:00Z", "$prefix-refresh", "2026-09-12T12:00:00Z", UUID.fromString("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"), UUID.fromString("cccccccc-cccc-4ccc-8ccc-cccccccccccc"), AuthorizationScope.GLOBAL_ADMIN, null)
    private fun tokenPairJson(prefix: String) = """{"tokenType":"Bearer","accessToken":"$prefix-access","accessTokenExpiresAt":"2026-08-12T12:00:00Z","refreshToken":"$prefix-refresh","refreshTokenExpiresAt":"2026-09-12T12:00:00Z","sessionId":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","deviceId":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","authorizationScope":"global_admin","category":null}"""
    private fun jsonResponse(body: String) = MockResponse().setHeader("Content-Type", "application/json").setBody(body)

    private companion object { const val PROFILE_CONTEXT_HEADER = "X-Rivune-Profile-Context" }
}

private class MutableCredentialStore(initialTokens: TokenPair) : CredentialStore {
    private var credentials: StoredCredentials? = null
    private val initialTokens = initialTokens
    val savedAccessTokens = mutableListOf<String>()
    override suspend fun load(issuer: String): StoredCredentials = credentials ?: StoredCredentials(issuer, initialTokens)
    override suspend fun save(credentials: StoredCredentials) { this.credentials = credentials; savedAccessTokens += credentials.tokens.accessToken }
    override suspend fun clear(issuer: String) { credentials = null }
}
