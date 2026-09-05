package io.rivune.app
import androidx.lifecycle.viewModelScope

import io.rivune.api.Account
import io.rivune.api.AccountSession
import io.rivune.api.AccountUser
import io.rivune.api.ActiveProfileGrant
import io.rivune.api.AuthorizationScope
import io.rivune.api.CategoryRef
import io.rivune.api.Collection
import io.rivune.api.CollectionTileShape
import io.rivune.api.CollectionFolder
import io.rivune.api.CollectionItem
import io.rivune.api.CollectionSourceView
import io.rivune.api.ResolvedCollectionFolder
import io.rivune.api.CollectionViewMode
import io.rivune.api.DeviceAuthorizationResponse
import io.rivune.api.Discovery
import io.rivune.api.MaintenanceSettings
import io.rivune.api.Profile
import io.rivune.api.ProfileAvatar
import io.rivune.api.ProfileSelection
import io.rivune.api.RivuneApiException
import io.rivune.api.normalizeServerUrl
import java.time.Instant
import java.util.UUID
import kotlinx.coroutines.CoroutineScope
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertContentEquals
import kotlin.test.assertFalse
import kotlin.test.assertIs
import kotlin.test.assertFailsWith
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue
import kotlinx.coroutines.Job
import kotlinx.coroutines.cancel
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain

@OptIn(ExperimentalCoroutinesApi::class)
class RivuneViewModelTest {
    private val dispatcher = StandardTestDispatcher()
    private val viewModels = mutableListOf<RivuneViewModel>()

    @BeforeTest
    fun setUp() {
        Dispatchers.setMain(dispatcher)
    }

    @AfterTest
    fun tearDown() {
        val jobs = viewModels.mapNotNull { it.viewModelScope.coroutineContext[Job] }
        jobs.forEach(Job::cancel)
        var attempts = 0
        while (jobs.any { !it.isCompleted } && attempts < 1_000) {
            dispatcher.scheduler.runCurrent()
            if (jobs.any { !it.isCompleted }) Thread.sleep(1)
            attempts += 1
        }
        check(jobs.all(Job::isCompleted)) { "ViewModel coroutines did not stop during test teardown" }
        viewModels.clear()
        Dispatchers.resetMain()
    }

    @Test
    fun normalizesRemoteAndLocalNetworkServerAddresses() {
        assertEquals("https://media.example.com", normalizeServerUrl("media.example.com/"))
        assertEquals("http://localhost:8080", normalizeServerUrl("localhost:8080"))
        assertEquals("http://127.0.0.1:8080", normalizeServerUrl("127.0.0.1:8080/"))
        assertEquals("http://192.168.1.20:8080", normalizeServerUrl("192.168.1.20:8080"))
        assertEquals("http://10.0.0.20:8080", normalizeServerUrl("10.0.0.20:8080"))
        assertEquals("http://172.16.0.20:8080", normalizeServerUrl("172.16.0.20:8080"))
        assertEquals("http://[fd00::20]:8080", normalizeServerUrl("[fd00::20]:8080"))
        assertEquals("https://rivune.local:8080", normalizeServerUrl("rivune.local:8080"))
        assertNull(normalizeServerUrl("https://media example.com"))
        assertNull(normalizeServerUrl("  "))
    }

    @Test
    fun defaultsPublicAddressesToHttps() {
        assertEquals("https://192.0.2.10:8080", normalizeServerUrl("192.0.2.10:8080"))
        assertEquals("https://[2001:db8::20]:8080", normalizeServerUrl("[2001:db8::20]:8080"))
    }

    @Test
    fun localNetworkPermissionTargetsOnlyApi37SupportedKnownLanDestinations() {
        val knownLan = listOf(
            "http://10.0.2.2:8080",
            "http://172.31.255.254:8080",
            "http://192.168.1.20:8080",
            "http://[fd00::20]:8080",
            "https://10.0.2.2:8080",
            "https://172.31.255.254:8080",
            "https://192.168.1.20:8080",
            "https://[fd00::20]:8080",
            "https://rivune.local:8080",
            "https://RIVUNE.LOCAL:8080",
            "https://rivune.local.:8080",
        )
        knownLan.forEach { url ->
            assertTrue(requiresLocalNetworkPermission(url, sdkInt = 37, targetSdk = 37, permissionGranted = false))
            assertFalse(requiresLocalNetworkPermission(url, sdkInt = 36, targetSdk = 37, permissionGranted = false))
            assertFalse(requiresLocalNetworkPermission(url, sdkInt = 37, targetSdk = 36, permissionGranted = false))
            assertFalse(requiresLocalNetworkPermission(url, sdkInt = 37, targetSdk = 37, permissionGranted = true))
        }

        listOf(
            "http://localhost:8080",
            "https://localhost:8080",
            "http://rivune.local:8080",
            "http://rivune.local.:8080",
            "https://local:8080",
            "https://rivune.example:8080",
            "http://169.254.1.1:8080",
            "https://100.64.1.1:8080",
            "http://127.0.0.1:8080",
            "https://127.0.0.1:8080",
            "http://[::1]:8080",
            "https://[::1]:8080",
            "https://192.0.2.10:8080",
            "https://[fe80::1]:8080",
            "https://user@10.0.2.2:8080",
            "https://@10.0.2.2:8080",
            "https://:@rivune.local:8080",
            "not a URL",
        ).forEach { url ->
            assertFalse(requiresLocalNetworkPermission(url, sdkInt = 37, targetSdk = 37, permissionGranted = false))
        }
    }

    @Test
    fun deniedLocalNetworkPermissionKeepsConnectionRetryableWithoutCreatingGateway() {
        val store = FakeServerStore()
        val gateway = FakeGateway()
        val viewModel = viewModel(store, gateway, serverConnectionAllowed = { false })

        viewModel.connect("10.0.2.2:8080")

        assertEquals(UiFailure.LOCAL_NETWORK_PERMISSION, viewModel.state.value.failure)
        assertEquals("http://10.0.2.2:8080", viewModel.state.value.serverInput)
        assertFalse(viewModel.state.value.isBusy)
        assertNull(store.value)
    }

    @Test
    fun successfulConnectionPersistsServerAndStartsMobilePairing() = runTest(dispatcher) {
        val store = FakeServerStore()
        val gateway = FakeGateway(pairingPending = true)
        val viewModel = viewModel(store, gateway)

        assertIs<AppDestination.Server>(viewModel.state.value.destination)
        viewModel.connect("media.example.com")
        runCurrent()

        assertIs<AppDestination.Pairing>(viewModel.state.value.destination)
        assertEquals("ABCD-EFGH", viewModel.state.value.pairing?.userCode)
        assertEquals("android", gateway.authorizationPlatform)
        assertTrue(runCatching { UUID.fromString(gateway.authorizationInstallationId) }.isSuccess)
        assertEquals("https://media.example.com", store.value)
        assertEquals("Family server", viewModel.state.value.serverName)
        assertEquals("20.0.0", viewModel.state.value.serverVersion)
        assertFalse(viewModel.state.value.isBusy)

        gateway.pairingPending = false
        advanceTimeBy(1_000)
        runCurrent()
    }

    @Test
    fun unsupportedOptionalCapabilitiesDoNotStartTheirRequests() = runTest(dispatcher) {
        val gateway = FakeGateway(
            discovery = discovery(capabilities = emptyList()),
            restored = true,
        )
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)

        advanceUntilIdle()

        assertFalse(viewModel.state.value.viewer.playbackCoordinationAvailable)
        assertEquals(0, gateway.playbackHeartbeatRequests)
        assertEquals(0, gateway.playbackDeviceListRequests)
        assertEquals(0, gateway.localRecommendationRequests)
    }

    @Test
    fun recommendationsRequestLandscapeArtwork() = runTest(dispatcher) {
        val gateway = FakeGateway(
            discovery = discovery(capabilities = listOf("local-recommendations")),
            restored = true,
            account = account(profile(), active = true),
        )
        viewModel(FakeServerStore("https://saved.example.com"), gateway)

        advanceUntilIdle()

        assertEquals(io.rivune.api.RecommendationArtworkShape.LANDSCAPE, gateway.localRecommendationArtworkShape)
    }

    @Test
    fun pairingCapacityFailureExplainsWhyNoCodeWasCreated() = runTest(dispatcher) {
        val store = FakeServerStore()
        val gateway = FakeGateway(
            authorizationFailure = RivuneApiException.Server(
                429,
                "device_code_capacity",
                "Too many device authorizations are pending; retry later",
            ),
        )
        val viewModel = viewModel(store, gateway)

        viewModel.connect("media.example.com")
        advanceUntilIdle()

        val state = viewModel.state.value
        assertIs<AppDestination.Pairing>(state.destination)
        assertEquals(UiFailure.PAIRING_LIMIT, state.failure)
        assertNull(state.pairing)
        assertFalse(state.isBusy)
    }

    @Test
    fun disconnectingPendingPairingForgetsServerAndCancelsPolling() = runTest(dispatcher) {
        val store = FakeServerStore()
        val gateway = FakeGateway(pairingPending = true)
        val viewModel = viewModel(store, gateway)

        viewModel.connect("media.example.com")
        runCurrent()
        assertIs<AppDestination.Pairing>(viewModel.state.value.destination)

        viewModel.disconnectServer()
        advanceUntilIdle()

        val state = viewModel.state.value
        assertIs<AppDestination.Server>(state.destination)
        assertEquals("", state.serverInput)
        assertNull(state.serverVersion)
        assertNull(state.pairing)
        assertNull(store.value)
        assertFalse(state.isBusy)
        assertEquals(0, gateway.exchangeCount)
        assertTrue(gateway.loggedOut)
    }

    @Test
    fun disconnectKeepsServerWhenLocalCredentialsCannotBeCleared() = runTest(dispatcher) {
        val store = FakeServerStore()
        val gateway = FakeGateway(
            pairingPending = true,
            logoutResult = LogoutResult(localCredentialsCleared = false, serverSessionClosed = true),
        )
        val viewModel = viewModel(store, gateway)

        viewModel.connect("media.example.com")
        runCurrent()
        viewModel.disconnectServer()
        advanceUntilIdle()

        val state = viewModel.state.value
        assertIs<AppDestination.Pairing>(state.destination)
        assertEquals(UiFailure.LOGOUT_FAILED, state.failure)
        assertEquals("https://media.example.com", store.value)
        assertFalse(state.isBusy)
        assertTrue(gateway.loggedOut)
    }

    @Test
    fun disconnectForgetsServerAfterRemoteRevocationFailure() = runTest(dispatcher) {
        val store = FakeServerStore()
        val gateway = FakeGateway(
            pairingPending = true,
            logoutResult = LogoutResult(localCredentialsCleared = true, serverSessionClosed = false),
        )
        val viewModel = viewModel(store, gateway)

        viewModel.connect("media.example.com")
        runCurrent()
        viewModel.disconnectServer()
        advanceUntilIdle()

        val state = viewModel.state.value
        assertIs<AppDestination.Server>(state.destination)
        assertEquals(UiFailure.LOGOUT_FAILED, state.failure)
        assertNull(store.value)
        assertFalse(state.isBusy)
        assertTrue(gateway.loggedOut)
    }

    @Test
    fun setupRequiredServerIsNotRemembered() = runTest(dispatcher) {
        val store = FakeServerStore()
        val gateway = FakeGateway(discovery = discovery(setupRequired = true))
        val viewModel = viewModel(store, gateway)

        viewModel.connect("https://new.example.com")
        advanceUntilIdle()

        assertIs<AppDestination.Server>(viewModel.state.value.destination)
        assertEquals(UiFailure.SETUP_REQUIRED, viewModel.state.value.failure)
        assertNull(store.value)
        assertNull(viewModel.state.value.serverVersion)
    }

    @Test
    fun restoredActiveProfileLoadsCollections() = runTest(dispatcher) {
        val profile = profile(hasPin = false)
        val collection = collection()
        val store = FakeServerStore("https://saved.example.com")
        val gateway = FakeGateway(
            restored = true,
            account = account(profile, active = true),
            collections = listOf(collection),
        )

        val viewModel = viewModel(store, gateway)
        advanceUntilIdle()

        val state = viewModel.state.value
        assertIs<AppDestination.Viewer>(state.destination)
        assertEquals(profile, state.activeProfile)
        assertEquals(listOf(collection), state.collections)
        assertEquals(collection.id, state.selectedCollectionId)
    }

    @Test
    fun continueWatchingMapsPayloadLocallyWithoutMetadataRequests() = runTest(dispatcher) {
        val profile = profile(hasPin = false)
        val seriesId = UUID.randomUUID()
        val episodeId = UUID.randomUUID()
        val movieId = UUID.randomUUID()
        val seasonId = UUID.randomUUID()
        val gateway = FakeGateway(
            restored = true,
            account = account(profile, active = true),
            collections = listOf(collection()),
        ).apply {
            continueWatchingPage = io.rivune.api.ContinueWatchingPage(
                listOf(
                    io.rivune.api.ContinueWatchingItem(
                        titleId = episodeId,
                        mediaType = io.rivune.api.PlaybackProgressMediaType.EPISODE,
                        seriesId = seriesId,
                        seasonId = seasonId,
                        seasonNumber = 2,
                        episodeNumber = 3,
                        title = "Signal Horizon",
                        posterUrl = "/series-poster",
                        backgroundUrl = "/series-background",
                        releaseInfo = "2026",
                        resourceId = "tt9000:2:3",
                        resourceProvider = "imdb",
                        episodeTitle = "Moonrise",
                        episodeStillUrl = "/episode-still",
                        episodeAirDate = "2026-08-15",
                        positionSeconds = 120,
                        durationSeconds = 1_800,
                        version = 1,
                        reason = io.rivune.api.ContinueWatchingReason.RESUME,
                        lastWatchedAt = "2026-08-15T00:00:00Z",
                    ),
                    io.rivune.api.ContinueWatchingItem(
                        titleId = movieId,
                        mediaType = io.rivune.api.PlaybackProgressMediaType.MOVIE,
                        title = "The Film",
                        posterUrl = "/movie-poster",
                        backgroundUrl = "/movie-background",
                        releaseInfo = "2025",
                        resourceId = "movie-42",
                        resourceProvider = "tmdb",
                        positionSeconds = 600,
                        durationSeconds = 7_200,
                        version = 2,
                        reason = io.rivune.api.ContinueWatchingReason.RESUME,
                        lastWatchedAt = "2026-08-14T00:00:00Z",
                    ),
                ),
            )
        }

        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()

        val episode = viewModel.state.value.viewer.continueWatching[0]
        assertEquals("Signal Horizon · Moonrise", episode.title)
        assertEquals(episodeId.toString(), episode.id)
        assertEquals("tt9000:2:3", episode.resourceId)
        assertEquals("imdb", episode.provider)
        assertEquals(mapOf("imdb" to "tt9000:2:3"), episode.externalIds)
        assertEquals("/episode-still", episode.posterUrl)
        assertEquals("/episode-still", episode.backgroundUrl)
        assertEquals("2026-08-15", episode.releaseInfo)
        assertEquals("2026-08-15", episode.released)
        assertEquals(seriesId, episode.seriesId)
        assertEquals(seasonId.toString(), episode.seasonId)
        assertEquals(2, episode.seasonNumber)
        assertEquals(3, episode.episodeNumber)
        assertEquals(120, episode.resumePositionSeconds)
        assertEquals(1_800, episode.durationSeconds)

        val movie = viewModel.state.value.viewer.continueWatching[1]
        assertEquals("The Film", movie.title)
        assertEquals(movieId.toString(), movie.id)
        assertEquals("movie-42", movie.resourceId)
        assertEquals("tmdb", movie.provider)
        assertEquals("/movie-poster", movie.posterUrl)
        assertEquals("/movie-background", movie.backgroundUrl)
        assertEquals("2025", movie.releaseInfo)
        assertEquals(listOf<Int?>(30), gateway.continueWatchingLimits)
        assertTrue(gateway.metadataRequests.none { it.first in setOf("movie", "series", "season") })
    }

    @Test
    fun homeRevalidationKeepsRenderedStateUntilReplacementIsReady() = runTest(dispatcher) {
        fun continueMovie(id: UUID, title: String, resourceId: String) = io.rivune.api.ContinueWatchingItem(
            titleId = id,
            mediaType = io.rivune.api.PlaybackProgressMediaType.MOVIE,
            title = title,
            resourceId = resourceId,
            resourceProvider = "tmdb",
            positionSeconds = 120,
            durationSeconds = 7_200,
            version = 1,
            reason = io.rivune.api.ContinueWatchingReason.RESUME,
            lastWatchedAt = "2026-08-15T00:00:00Z",
        )
        val initialFolder = folder()
        val initialItem = mediaItem("initial", "Initial Hero")
        val initialContinue = continueMovie(UUID.randomUUID(), "Initial Continue", "initial-continue")
        val gateway = FakeGateway(
            restored = true,
            account = account(profile(hasPin = false), active = true),
            collections = listOf(collection(initialFolder)),
            resolvedFolders = mapOf(
                requireNotNull(initialFolder.id) to listOf(
                    resolvedFolder(initialFolder, page = 1, hasMore = false, items = listOf(initialItem)),
                ),
            ),
        ).apply {
            continueWatchingPage = io.rivune.api.ContinueWatchingPage(listOf(initialContinue))
        }
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()
        val renderedCollections = viewModel.state.value.collections
        val renderedHero = viewModel.state.value.viewer.heroSlides
        val renderedContinue = viewModel.state.value.viewer.continueWatching
        assertTrue(renderedHero.isNotEmpty())

        gateway.collections = listOf(collection().copy(title = "Replacement Home"))
        gateway.continueWatchingPage = io.rivune.api.ContinueWatchingPage(
            listOf(continueMovie(UUID.randomUUID(), "Replacement Continue", "replacement-continue")),
        )
        gateway.continueWatchingDelayMillis = 1_000
        viewModel.refreshViewer()
        runCurrent()

        assertEquals(renderedCollections, viewModel.state.value.collections)
        assertEquals(renderedHero, viewModel.state.value.viewer.heroSlides)
        assertEquals(renderedContinue, viewModel.state.value.viewer.continueWatching)

        advanceTimeBy(1_000)
        runCurrent()
        assertEquals("Replacement Home", viewModel.state.value.collections.single().title)
        assertEquals("Replacement Continue", viewModel.state.value.viewer.continueWatching.single().title)
    }

    @Test
    fun episodeTargetKeepsRichDetailMetadata() {
        val seriesId = UUID.randomUUID()
        val episode = episode(UUID.randomUUID(), seriesId, 3, seasonNumber = 2).copy(
            runtimeMinutes = 51,
            voteAverage = 7.4,
        )
        val target = episode.toMediaTarget(series(seriesId), MediaTarget("series", "series", "Series"))

        assertEquals(51, target.runtimeMinutes)
        assertEquals(7.4, target.rating)
        assertEquals(2, target.seasonNumber)
        assertEquals(3, target.episodeNumber)
    }

    @Test
    fun sourceAddonFiltersUseAddonIdentityAndHideManifestFromNamedFooters() {
        val aioId = UUID.fromString("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
        val cometId = UUID.fromString("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
        fun source(
            id: String,
            addonId: UUID,
            addonName: String?,
            manifestId: String,
        ) = io.rivune.api.PlaybackSourceOption(
            id = id,
            sourceRef = "ref-$id",
            addonId = addonId,
            addonName = addonName,
            manifestId = manifestId,
            streamIndex = 0,
            name = "Stream $id",
            protocol = "http",
            mode = io.rivune.api.PlaybackMode.DIRECT,
            container = "mkv",
            expiresAt = "2099-01-01T00:00:00Z",
            stableIdentity = "stable-$id",
        )
        val unnamedAio = source("aio-1", aioId, null, "aiostreams.internal.manifest")
        val namedAio = source("aio-2", aioId, "AIOStreams", "aiostreams.internal.manifest")
        val comet = source("comet", cometId, "Comet", "comet.internal.manifest")

        assertEquals(
            listOf(aioId to "AIOStreams", cometId to "Comet"),
            playbackSourceAddonFilters(listOf(unnamedAio, namedAio, comet)),
        )
        assertEquals("aiostreams.internal.manifest", playbackSourceAddonLabel(unnamedAio))
        assertEquals("AIOStreams · direct · HTTP · MKV", playbackSourceFooter(namedAio))
        assertTrue(namedAio.copy(sourceRef = "rotated", streamIndex = 7).matchesRecoverySource(namedAio))
        assertFalse(namedAio.copy(stableIdentity = "").matchesRecoverySource(namedAio))
        assertFalse(namedAio.copy(stableIdentity = "different").matchesRecoverySource(namedAio))
        assertFalse(playbackSourceFooter(namedAio).contains("internal.manifest"))
    }

    @Test
    fun nextEpisodeResolutionUsesAdjacencyCrossSeasonAndFinalBoundary() = runTest(dispatcher) {
        val seriesId = UUID.randomUUID()
        val firstId = UUID.randomUUID()
        val secondId = UUID.randomUUID()
        val thirdId = UUID.randomUUID()
        val firstSeason = season(
            seriesId,
            listOf(episode(firstId, seriesId, 1), episode(secondId, seriesId, 2)),
        )
        val secondSeason = season(
            seriesId,
            listOf(episode(thirdId, seriesId, 1, seasonId = "season-3", seasonNumber = 3)),
            id = "season-3",
            number = 3,
        )
        val emptySeason = season(seriesId, emptyList(), id = "season-empty", number = 2)
        val seriesFixture = series(seriesId).copy(
            seasons = listOf(
                seasonSummary(seriesId, "season-1", 1, 2),
                seasonSummary(seriesId, "season-empty", 2, 0),
                seasonSummary(seriesId, "season-3", 3, 1),
            ),
        )
        val fallback = MediaTarget(
            id = "current",
            mediaType = "episode",
            title = "Current",
            seriesId = seriesId,
            seasonId = "season-1",
            seasonNumber = 1,
            episodeNumber = 1,
        )
        val seasons = mapOf(secondSeason.id to secondSeason, emptySeason.id to emptySeason)

        assertEquals(
            secondId,
            resolveNextEpisodeTarget(seriesFixture, firstSeason, firstId, fallback) { seasons.getValue(it) }?.titleId,
        )
        assertEquals(
            thirdId,
            resolveNextEpisodeTarget(seriesFixture, firstSeason, secondId, fallback) { seasons.getValue(it) }?.titleId,
        )
        assertNull(resolveNextEpisodeTarget(seriesFixture, secondSeason, thirdId, fallback) { seasons.getValue(it) })
        assertNull(resolveNextEpisodeTarget(seriesFixture, firstSeason, UUID.randomUUID(), fallback) { seasons.getValue(it) })
    }

    @Test
    fun restoredProfileOpensConfiguredStartupTab() = runTest(dispatcher) {
        val profile = profile(hasPin = false)
        val gateway = FakeGateway(
            restored = true,
            account = account(profile, active = true),
            collections = listOf(collection()),
        )
        val preferences = AppPreferencesReader {
            AppPreferencesState(startupTab = ViewerTab.SEARCH)
        }

        val viewModel = viewModel(
            FakeServerStore("https://saved.example.com"),
            gateway,
            appPreferences = preferences,
        )
        advanceUntilIdle()

        assertIs<AppDestination.Viewer>(viewModel.state.value.destination)
        assertEquals(ViewerTab.SEARCH, viewModel.state.value.viewer.selectedTab)
        assertTrue(viewModel.state.value.collections.isEmpty())
        assertFalse(viewModel.state.value.isBusy)
    }

    @Test
    fun selectedProfileOpensConfiguredStartupTab() = runTest(dispatcher) {
        val profile = profile(hasPin = false)
        val gateway = FakeGateway(
            restored = true,
            account = account(profile),
        )
        val viewModel = viewModel(
            FakeServerStore("https://saved.example.com"),
            gateway,
            appPreferences = AppPreferencesReader {
                AppPreferencesState(startupTab = ViewerTab.LIBRARY)
            },
        )
        advanceUntilIdle()

        viewModel.selectProfile(profile)
        advanceUntilIdle()

        assertIs<AppDestination.Viewer>(viewModel.state.value.destination)
        assertEquals(ViewerTab.LIBRARY, viewModel.state.value.viewer.selectedTab)
        assertFalse(viewModel.state.value.isBusy)
    }

    @Test
    fun restoredAdministratorSessionIsReplacedByDevicePairing() = runTest(dispatcher) {
        val profile = profile(hasPin = false)
        val gateway = FakeGateway(
            restored = true,
            account = account(profile, authorizationScope = AuthorizationScope.GLOBAL_ADMIN),
            pairingPending = true,
        )
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        runCurrent()

        assertTrue(gateway.loggedOut)
        assertIs<AppDestination.Pairing>(viewModel.state.value.destination)
        assertEquals("ABCD-EFGH", viewModel.state.value.pairing?.userCode)
        viewModel.viewModelScope.cancel()

    }

    @Test
    fun pairingAndPinSelectionReachHomeWithoutRetainingPinInState() = runTest(dispatcher) {
        val profile = profile(hasPin = true)
        val gateway = FakeGateway(
            account = account(profile),
            collections = listOf(collection()),
            pairingPending = true,
        )
        val viewModel = viewModel(FakeServerStore(), gateway)
        viewModel.connect("https://media.example.com")
        runCurrent()

        gateway.pairingPending = false
        advanceTimeBy(1_000)
        runCurrent()
        assertIs<AppDestination.Pairing>(viewModel.state.value.destination)
        assertTrue(viewModel.state.value.pairingAccepted)
        assertEquals(1, gateway.exchangeCount)

        advanceTimeBy(550)
        runCurrent()
        assertIs<AppDestination.Profiles>(viewModel.state.value.destination)

        viewModel.selectProfile(profile)
        assertEquals(profile, viewModel.state.value.pendingProfile)
        viewModel.submitPin("1234")
        advanceUntilIdle()

        assertIs<AppDestination.Viewer>(viewModel.state.value.destination)
        assertEquals("1234", gateway.selectedPin)
        assertNull(viewModel.state.value.pendingProfile)
        assertEquals(profile, viewModel.state.value.activeProfile)
    }

    @Test
    fun changingProfileClearsSelectionWithoutLoggingOut() = runTest(dispatcher) {
        val profile = profile(hasPin = false)
        val gateway = FakeGateway(
            restored = true,
            account = account(profile, active = true),
            collections = listOf(collection()),
        )
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()

        viewModel.changeProfile()
        advanceUntilIdle()

        assertIs<AppDestination.Profiles>(viewModel.state.value.destination)
        assertNull(viewModel.state.value.activeProfile)
        assertTrue(viewModel.state.value.collections.isEmpty())
        assertEquals(1, gateway.clearSelectionCount)
        assertFalse(gateway.loggedOut)
    }

    @Test
    fun changingProfileFencesDelayedHomeBeforeClearingSelection() = runTest(dispatcher) {
        val profile = profile(hasPin = false)
        val gateway = FakeGateway(
            restored = true,
            account = account(profile, active = true),
            collections = listOf(collection()),
        ).apply {
            continueWatchingDelayMillis = 1_000
            continueWatchingPage = io.rivune.api.ContinueWatchingPage(
                listOf(
                    io.rivune.api.ContinueWatchingItem(
                        titleId = UUID.randomUUID(),
                        mediaType = io.rivune.api.PlaybackProgressMediaType.MOVIE,
                        title = "Delayed Home",
                        resourceId = "delayed-home",
                        positionSeconds = 120,
                        durationSeconds = 7_200,
                        version = 1,
                        reason = io.rivune.api.ContinueWatchingReason.RESUME,
                        lastWatchedAt = "2026-08-15T00:00:00Z",
                    ),
                ),
            )
        }
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        runCurrent()
        assertEquals(listOf<Int?>(30), gateway.continueWatchingLimits)

        viewModel.changeProfile()
        runCurrent()
        assertIs<AppDestination.Profiles>(viewModel.state.value.destination)
        assertTrue(viewModel.state.value.viewer.continueWatching.isEmpty())

        advanceTimeBy(1_000)
        runCurrent()
        assertIs<AppDestination.Profiles>(viewModel.state.value.destination)
        assertTrue(viewModel.state.value.collections.isEmpty())
        assertTrue(viewModel.state.value.viewer.continueWatching.isEmpty())
    }
    @Test
    fun profilePreferencesLoadPersistAndCloseWithoutLeavingViewer() = runTest(dispatcher) {
        val profile = profile(canManage = true)
        val gateway = FakeGateway(
            restored = true,
            account = account(profile, active = true),
            collections = listOf(collection()),
        )
        gateway.effectiveSettingsResult = io.rivune.api.EffectiveSettings(
            schemaVersion = 1,
            settings = io.rivune.api.SettingsValues(
                maximumResolution = "1080p",
                preferDirectPlay = true,
                audioLanguage = "fr",
                metadataLanguage = "fr-FR",
                subtitleLanguage = "en",
                forcedSubtitleLanguage = "es",
                autoplayNextEpisode = false,
            ),
            sources = io.rivune.api.EffectiveSettingsSources(
                maximumResolution = "instance",
                preferDirectPlay = "profile",
                audioLanguage = "profile",
                subtitleLanguage = "instance",
                forcedSubtitleLanguage = "profile",
                autoplayNextEpisode = "profile",
                metadataLanguage = "profile",
            ),
        )
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()

        viewModel.openProfilePreferences()
        advanceUntilIdle()
        val loaded = assertNotNull(viewModel.state.value.viewer.preferences)
        assertTrue(loaded.canEdit)
        assertEquals("1080p", loaded.settings?.maximumResolution)
        assertEquals("fr-FR", loaded.settings?.metadataLanguage)
        assertEquals("instance", loaded.sources?.maximumResolution)
        assertEquals("profile", loaded.sources?.forcedSubtitleLanguage)
        assertEquals(false, loaded.settings?.autoplayNextEpisode)

        viewModel.updateProfilePreferences(
            io.rivune.api.ProfileSettingsUpdate(
                metadataLanguage = io.rivune.api.PatchField.Value("de-DE"),
                forcedSubtitleLanguage = io.rivune.api.PatchField.Null,
                autoplayNextEpisode = io.rivune.api.PatchField.Value(true),
            ),
        )
        advanceUntilIdle()
        assertEquals(
            listOf(
                io.rivune.api.ProfileSettingsUpdate(
                    metadataLanguage = io.rivune.api.PatchField.Value("de-DE"),
                    forcedSubtitleLanguage = io.rivune.api.PatchField.Null,
                    autoplayNextEpisode = io.rivune.api.PatchField.Value(true),
                ),
            ),
            gateway.profileSettingsUpdates,
        )
        assertEquals("de-DE", viewModel.state.value.viewer.preferences?.settings?.metadataLanguage)
        assertEquals("es", viewModel.state.value.viewer.preferences?.settings?.forcedSubtitleLanguage)
        assertEquals("instance", viewModel.state.value.viewer.preferences?.sources?.forcedSubtitleLanguage)
        assertEquals(true, viewModel.state.value.viewer.preferences?.settings?.autoplayNextEpisode)

        viewModel.backViewer()
        assertNull(viewModel.state.value.viewer.preferences)
        assertIs<AppDestination.Viewer>(viewModel.state.value.destination)
    }

    @Test
    fun readOnlyProfilePreferencesExposeEffectiveValuesWithoutPersisting() = runTest(dispatcher) {
        val profile = profile(canManage = false)
        val gateway = FakeGateway(
            restored = true,
            account = account(profile, active = true),
            collections = listOf(collection()),
        )
        gateway.effectiveSettingsResult = io.rivune.api.EffectiveSettings(
            schemaVersion = 1,
            settings = io.rivune.api.SettingsValues(audioLanguage = "fr"),
            sources = io.rivune.api.EffectiveSettingsSources(),
        )
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()

        viewModel.openProfilePreferences()
        advanceUntilIdle()
        assertFalse(assertNotNull(viewModel.state.value.viewer.preferences).canEdit)

        viewModel.updateProfilePreferences(
            io.rivune.api.ProfileSettingsUpdate(audioLanguage = io.rivune.api.PatchField.Value("en")),
        )
        advanceUntilIdle()
        assertTrue(gateway.profileSettingsUpdates.isEmpty())
        assertEquals("fr", viewModel.state.value.viewer.preferences?.settings?.audioLanguage)
    }


    @Test
    fun logoutKeepsAuthenticatedScreenWhenLocalCredentialsRemain() = runTest(dispatcher) {
        val profile = profile(hasPin = false)
        val store = FakeServerStore("https://saved.example.com")
        val gateway = FakeGateway(
            restored = true,
            account = account(profile, active = true),
            logoutResult = LogoutResult(localCredentialsCleared = false, serverSessionClosed = false),
        )
        val viewModel = viewModel(store, gateway)
        advanceUntilIdle()

        viewModel.logout()
        advanceUntilIdle()

        assertIs<AppDestination.Viewer>(viewModel.state.value.destination)
        assertEquals(UiFailure.LOGOUT_FAILED, viewModel.state.value.failure)
        assertEquals("https://saved.example.com", store.value)
        assertTrue(gateway.loggedOut)
    }

    @Test
    fun logoutKeepsRevocationFailureDuringNewPairing() = runTest(dispatcher) {
        val profile = profile(hasPin = false)
        val gateway = FakeGateway(
            restored = true,
            account = account(profile, active = true),
            pairingPending = true,
            logoutResult = LogoutResult(localCredentialsCleared = true, serverSessionClosed = false),
        )
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()

        viewModel.logout()
        runCurrent()

        assertIs<AppDestination.Pairing>(viewModel.state.value.destination)
        assertEquals("ABCD-EFGH", viewModel.state.value.pairing?.userCode)
        assertEquals("android", gateway.authorizationPlatform)
        assertEquals(UiFailure.LOGOUT_FAILED, viewModel.state.value.failure)

        gateway.pairingPending = false
        advanceTimeBy(1_000)
        runCurrent()
    }

    @Test
    fun openingFolderLoadsAndAppendsUniquePagesThenReturnsHome() = runTest(dispatcher) {
        val profile = profile(hasPin = false)
        val folder = folder()
        val first = resolvedFolder(folder, page = 1, hasMore = true, items = listOf(mediaItem("one", "First")))
        val second = resolvedFolder(
            folder,
            page = 2,
            hasMore = false,
            items = listOf(mediaItem("one", "First"), mediaItem("two", "Second")),
        )
        val gateway = FakeGateway(
            restored = true,
            account = account(profile, active = true),
            collections = listOf(collection(folder).copy(heroEnabled = false)),
            resolvedFolders = mapOf(folder.id!! to listOf(first, second)),
        )
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()

        viewModel.openFolder(COLLECTION_ID, folder)
        advanceUntilIdle()
        assertEquals(listOf("one"), viewModel.state.value.resolvedFolder?.items?.map { it.id })

        viewModel.loadMoreFolderItems()
        advanceUntilIdle()
        assertEquals(listOf("one", "two"), viewModel.state.value.resolvedFolder?.items?.map { it.id })
        assertEquals(listOf(1, 2), gateway.resolvedPages)
        assertFalse(viewModel.state.value.resolvedFolder?.hasMore ?: true)

        viewModel.closeFolder()
        assertNull(viewModel.state.value.resolvedFolder)
        assertIs<AppDestination.Viewer>(viewModel.state.value.destination)
    }
    @Test
    fun selectingMultiFolderCollectionPreservesHierarchyUntilClosed() = runTest(dispatcher) {
        val profile = profile(hasPin = false)
        val first = folder()
        val second = folder().copy(
            id = UUID.fromString("88888888-8888-4888-8888-888888888888"),
            title = "Second",
        )
        val collection = collection(first).copy(folders = listOf(first, second))
        val gateway = FakeGateway(
            restored = true,
            account = account(profile, active = true),
            collections = listOf(collection),
            resolvedFolders = mapOf(
                requireNotNull(first.id) to listOf(resolvedFolder(first, 1, false, emptyList())),
                requireNotNull(second.id) to listOf(resolvedFolder(second, 1, false, emptyList())),
            ),
        )
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()

        viewModel.selectCollection(collection.id)

        assertEquals(collection.id, viewModel.state.value.openedCollectionId)
        assertNull(viewModel.state.value.resolvedFolder)
        viewModel.closeCollection()
        assertNull(viewModel.state.value.openedCollectionId)
    }

    @Test
    fun libraryTypesFollowAccessibleAddonCatalogs() = runTest(dispatcher) {
        val profile = profile(hasPin = false)
        val gateway = FakeGateway(
            restored = true,
            account = account(profile, active = true),
        )
        gateway.catalogDescriptors = listOf(
            io.rivune.api.AddonCatalogDescriptor(
                addonId = UUID.fromString("88888888-8888-4888-8888-888888888881"),
                manifestId = "movies",
                position = 0,
                catalog = io.rivune.api.StremioManifestCatalog(type = "movie", id = "popular"),
                addonCatalog = false,
                searchable = true,
            ),
            io.rivune.api.AddonCatalogDescriptor(
                addonId = UUID.fromString("88888888-8888-4888-8888-888888888882"),
                manifestId = "television",
                position = 1,
                catalog = io.rivune.api.StremioManifestCatalog(type = "tv", id = "live"),
                addonCatalog = false,
                searchable = false,
            ),
            io.rivune.api.AddonCatalogDescriptor(
                addonId = UUID.fromString("88888888-8888-4888-8888-888888888883"),
                manifestId = "configuration",
                position = 2,
                catalog = io.rivune.api.StremioManifestCatalog(type = "series", id = "settings"),
                addonCatalog = true,
                searchable = false,
            ),
        )
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()

        viewModel.selectViewerTab(ViewerTab.LIBRARY)
        advanceUntilIdle()

        assertEquals(setOf("movie", "tv"), viewModel.state.value.viewer.library.availableTypes)
    }
    @Test
    fun missingFolderArtworkIsResolvedForHome() = runTest(dispatcher) {
        val profile = profile()
        val unresolved = folder().copy(coverImageUrl = null)
        val resolved = unresolved.copy(coverImageUrl = "/api/v1/artwork/resolved-folder")
        val gateway = FakeGateway(
            restored = true,
            account = account(profile, active = true),
            collections = listOf(collection(unresolved).copy(heroEnabled = false)),
            resolvedFolders = mapOf(
                requireNotNull(unresolved.id) to listOf(resolvedFolder(resolved, page = 1, hasMore = false, items = emptyList())),
            ),
        )

        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()

        assertEquals("/api/v1/artwork/resolved-folder", viewModel.state.value.collections.single().folders.single().coverImageUrl)
        assertEquals(listOf(1), gateway.resolvedPages)
        assertEquals(listOf(requireNotNull(unresolved.id)), gateway.artworkFolderRequests)
        assertTrue(gateway.fullFolderRequests.isEmpty())
    }

    @Test
    fun heroArtworkIsResolvedEvenWhenFolderCoverAlreadyExists() = runTest(dispatcher) {
        val profile = profile()
        val unresolved = folder().copy(
            sources = listOf(
                io.rivune.api.CollectionSource(
                    kind = io.rivune.api.CollectionSourceKind.TMDB,
                    title = "Hero",
                ),
            ),
        )
        val resolved = unresolved.copy(
            heroBackdropUrl = "/api/v1/artwork/hero-backdrop",
            titleLogoUrl = "/api/v1/artwork/hero-logo",
        )
        val gateway = FakeGateway(
            restored = true,
            account = account(profile, active = true),
            collections = listOf(collection(unresolved).copy(heroEnabled = true)),
            resolvedFolders = mapOf(
                requireNotNull(unresolved.id) to listOf(
                    resolvedFolder(resolved, page = 1, hasMore = false, items = emptyList()),
                ),
            ),
        )

        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()

        val heroFolder = viewModel.state.value.collections.single().folders.single()
        assertEquals("/api/v1/artwork/hero-backdrop", heroFolder.heroBackdropUrl)
        assertEquals("/api/v1/artwork/hero-logo", heroFolder.titleLogoUrl)
        assertEquals(listOf(1), gateway.resolvedPages)
        assertEquals(listOf(requireNotNull(unresolved.id)), gateway.fullFolderRequests)
        assertTrue(gateway.artworkFolderRequests.isEmpty())
    }

    @Test
    fun homeHeroSlidesUseEveryEnabledFolderInOrderWithStableDedupeCapAndFallbacks() = runTest(dispatcher) {
        val profile = profile()
        val collectionOneId = UUID.fromString("81000000-0000-4000-8000-000000000001")
        val collectionTwoId = UUID.fromString("81000000-0000-4000-8000-000000000002")
        val disabledCollectionId = UUID.fromString("81000000-0000-4000-8000-000000000003")
        val firstFolderId = UUID.fromString("82000000-0000-4000-8000-000000000001")
        val secondFolderId = UUID.fromString("82000000-0000-4000-8000-000000000002")
        val failedFolderId = UUID.fromString("82000000-0000-4000-8000-000000000003")
        val disabledFolderId = UUID.fromString("82000000-0000-4000-8000-000000000004")
        val addonOne = UUID.fromString("83000000-0000-4000-8000-000000000001")
        val addonTwo = UUID.fromString("83000000-0000-4000-8000-000000000002")
        val sourceOne = UUID.fromString("84000000-0000-4000-8000-000000000001")
        val sourceTwo = UUID.fromString("84000000-0000-4000-8000-000000000002")
        fun addonItem(id: String, addonId: UUID, sourceId: UUID) = mediaItem(id, id).copy(
            mediaType = "custom",
            sources = listOf(
                io.rivune.api.CollectionSourceReference(
                    id = sourceId,
                    kind = io.rivune.api.CollectionSourceKind.ADDON_CATALOG,
                    title = "Addon",
                    addonId = addonId,
                    catalogId = "featured",
                ),
            ),
        )
        val firstFolder = folder().copy(id = firstFolderId, title = "First", coverImageUrl = "/first")
        val secondFolder = folder().copy(id = secondFolderId, title = "Second", coverImageUrl = "/second")
        val failedFolder = folder().copy(id = failedFolderId, title = "Failed")
        val disabledFolder = folder().copy(id = disabledFolderId, title = "Disabled", coverImageUrl = null)
        val resolvedFirst = firstFolder.copy(
            heroBackdropUrl = "/api/v1/artwork/first-backdrop",
            titleLogoUrl = "/api/v1/artwork/first-logo",
        )
        val firstItems = listOf(
            mediaItem("movie-one", "Movie One"),
            mediaItem("movie-one", "Duplicate Movie One"),
            addonItem("shared-resource", addonOne, sourceOne),
            addonItem("shared-resource", addonTwo, sourceTwo),
        )
        val secondItems = listOf(mediaItem("movie-one", "Duplicate Across Folders")) + (1..10).map { index ->
            mediaItem("series-$index", "Series $index").copy(mediaType = "series")
        }
        val collections = listOf(
            collection().copy(
                id = collectionOneId,
                title = "First Collection",
                backdropImageUrl = "/api/v1/artwork/collection-backdrop",
                heroEnabled = true,
                folders = listOf(firstFolder),
            ),
            collection().copy(
                id = collectionTwoId,
                title = "Second Collection",
                backdropImageUrl = "/api/v1/artwork/collection-backdrop",
                heroEnabled = true,
                folders = listOf(failedFolder, secondFolder),
            ),
            collection().copy(
                id = disabledCollectionId,
                title = "Disabled Collection",
                heroEnabled = false,
                folders = listOf(disabledFolder),
            ),
        )
        val gateway = FakeGateway(
            restored = true,
            account = account(profile, active = true),
            collections = collections,
            resolvedFolders = mapOf(
                firstFolderId to listOf(resolvedFolder(resolvedFirst, page = 1, hasMore = false, items = firstItems)),
                secondFolderId to listOf(resolvedFolder(secondFolder, page = 1, hasMore = false, items = secondItems)),
                disabledFolderId to listOf(
                    resolvedFolder(disabledFolder, page = 1, hasMore = false, items = listOf(mediaItem("disabled", "Disabled"))),
                ),
            ),
        )

        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()

        val slides = viewModel.state.value.viewer.heroSlides
        assertEquals(12, slides.size)
        assertEquals(
            listOf("movie-one", "shared-resource", "shared-resource") + (1..9).map { "series-$it" },
            slides.map { it.item.id },
        )
        assertEquals(firstItems.first(), slides.first().item)
        assertEquals(listOf(addonOne, addonTwo), slides.slice(1..2).map { it.item.sources.single().addonId })
        assertEquals("/api/v1/artwork/first-backdrop", slides.first().fallbackBackdropUrl)
        assertEquals("/api/v1/artwork/first-logo", slides.first().fallbackLogoUrl)
        assertEquals("/api/v1/artwork/collection-backdrop", slides[3].fallbackBackdropUrl)
        assertNull(slides[3].fallbackLogoUrl)
        assertFalse(slides.any { it.item.id == "disabled" })
        assertNull(viewModel.state.value.viewer.inlineFailure)
        assertNull(viewModel.state.value.viewer.loading)
        assertEquals(setOf(firstFolderId, secondFolderId, failedFolderId), gateway.fullFolderRequests.toSet())
        assertEquals(listOf(disabledFolderId), gateway.artworkFolderRequests)
        assertEquals("/api/v1/artwork/first-backdrop", viewModel.state.value.collections.first().folders.first().heroBackdropUrl)

    }
    @Test
    fun displayableSeasonsHideEmptyEntriesAndSortBySeasonNumber() {
        val seriesId = UUID.fromString("88888888-8888-4888-8888-888888888888")
        val seasonTwo = seasonSummary(seriesId, "season-2", 2, 10)
        val emptySeason = seasonSummary(seriesId, "season-0", 0, 0)
        val seasonOne = seasonSummary(seriesId, "season-1", 1, 8)

        assertEquals(listOf("season-1", "season-2"), displayableSeasons(listOf(seasonTwo, emptySeason, seasonOne)).map { it.id })
    }
    @Test
    fun episodeDetailOpensSourcesAndReturnsToItsSelectedSeason() = runTest(dispatcher) {
        val profile = profile()
        val seriesId = UUID.fromString("88888888-8888-4888-8888-888888888888")
        val episode = episode(UUID.fromString("99999999-9999-4999-8999-999999999991"), seriesId, 1)
        val season = season(seriesId, listOf(episode))
        val castMember = io.rivune.api.CastMember("person-1", "Lead Actor", "Juliette")
        val series = series(seriesId).copy(
            seasons = listOf(seasonSummary(seriesId, season.id, 1, 1)),
            cast = listOf(castMember),
        )
        val gateway = FakeGateway(
            restored = true,
            account = account(profile, active = true),
            collections = listOf(collection()),
        )
        gateway.seriesResult = series
        gateway.seasons = mapOf(season.id to season)
        gateway.configurePlayback(episode.id)
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()
        val seriesTarget = MediaTarget(id = "tmdb:42", mediaType = "series", title = "Series", titleId = seriesId)

        viewModel.openMedia(seriesTarget)
        advanceUntilIdle()
        viewModel.selectSeason(season.id)
        advanceUntilIdle()
        val seasonDetail = assertNotNull(viewModel.state.value.viewer.detail)

        viewModel.openEpisode(episode.toMediaTarget(series, seriesTarget))
        advanceUntilIdle()

        assertEquals("episode", viewModel.state.value.viewer.detail?.target?.mediaType)
        assertEquals(listOf(seasonDetail), viewModel.state.value.viewer.detailHistory)
        assertEquals("source", viewModel.state.value.viewer.sourcePicker?.options?.single()?.id)
        assertEquals(listOf(castMember), viewModel.state.value.viewer.detail?.cast)
        assertTrue(viewModel.state.value.viewer.sourcePickerVisible)

        viewModel.backDetail()

        assertNull(viewModel.state.value.viewer.sourcePicker)
        assertFalse(viewModel.state.value.viewer.sourcePickerVisible)
        assertEquals("series", viewModel.state.value.viewer.detail?.target?.mediaType)
        assertEquals(season.id, viewModel.state.value.viewer.detail?.season?.id)

        assertTrue(viewModel.state.value.viewer.detailHistory.isEmpty())

        viewModel.backViewer()

        assertEquals("series", viewModel.state.value.viewer.detail?.target?.mediaType)
        assertNull(viewModel.state.value.viewer.detail?.season)
        assertTrue(viewModel.state.value.viewer.detailHistory.isEmpty())
    }



    @Test
    fun selectedSeasonTogglesEveryEpisodeWithBatchProgress() = runTest(dispatcher) {
        val profile = profile()
        val seriesId = UUID.fromString("88888888-8888-4888-8888-888888888888")
        val episodeOne = episode(UUID.fromString("99999999-9999-4999-8999-999999999991"), seriesId, 1)
        val episodeTwo = episode(UUID.fromString("99999999-9999-4999-8999-999999999992"), seriesId, 2)
        val season = season(seriesId, listOf(episodeOne, episodeTwo))
        val gateway = FakeGateway(
            restored = true,
            account = account(profile, active = true),
            collections = listOf(collection()),
        )
        gateway.resolvedTitle = io.rivune.api.TitleReference(
            seriesId,
            io.rivune.api.TitleMediaType.SERIES,
            "tmdb",
            "42",
            "tmdb:42",
            "Series",
        )
        gateway.seriesResult = series(seriesId)
        gateway.seasons = mapOf(season.id to season)
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()

        viewModel.openMedia(MediaTarget(id = "tmdb:42", mediaType = "series", title = "Series"))
        advanceUntilIdle()
        viewModel.selectSeason(season.id)
        advanceUntilIdle()
        viewModel.toggleWatched()
        advanceUntilIdle()

        assertEquals(listOf(episodeOne.id, episodeTwo.id), gateway.watchedBatchRequests.single().map { it.titleId })
        assertTrue(gateway.watchedBatchRequests.single().all { it.completed && it.expectedVersion == 0L })
        assertTrue(viewModel.state.value.viewer.detail?.episodeProgress?.values?.all { it.completed } == true)

        viewModel.toggleWatched()
        advanceUntilIdle()
        assertTrue(gateway.watchedBatchRequests.last().all { !it.completed && it.expectedVersion == 1L })
        assertTrue(viewModel.state.value.viewer.detail?.episodeProgress?.values?.none { it.completed } == true)
    }

    @Test
    fun successfulSeasonChunksRemainVisibleAfterLaterChunkFails() = runTest(dispatcher) {
        val viewerProfile = profile()
        val seriesId = UUID.fromString("88888888-8888-4888-8888-888888888888")
        val episodes = (1..101).map { number ->
            episode(UUID.nameUUIDFromBytes("episode-$number".toByteArray()), seriesId, number)
        }
        val selectedSeason = season(seriesId, episodes)
        val gateway = FakeGateway(
            restored = true,
            account = account(viewerProfile, active = true),
            collections = listOf(collection()),
        )
        gateway.resolvedTitle = io.rivune.api.TitleReference(seriesId, io.rivune.api.TitleMediaType.SERIES, "tmdb", "42", "tmdb:42", "Series")
        gateway.seriesResult = series(seriesId)
        gateway.seasons = mapOf(selectedSeason.id to selectedSeason)
        gateway.watchedBatchFailureAtRequest = 2
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()

        viewModel.openMedia(MediaTarget(id = "tmdb:42", mediaType = "series", title = "Series"))
        advanceUntilIdle()
        viewModel.selectSeason(selectedSeason.id)
        advanceUntilIdle()
        viewModel.toggleWatched()
        advanceUntilIdle()

        val progress = requireNotNull(viewModel.state.value.viewer.detail).episodeProgress
        assertEquals(100, progress.values.count { it.completed })
        assertEquals(UiFailure.ACTION, viewModel.state.value.viewer.inlineFailure)
    }

    @Test
    fun seasonRemainsVisibleWhenProgressHydrationFails() = runTest(dispatcher) {
        val viewerProfile = profile()
        val seriesId = UUID.fromString("88888888-8888-4888-8888-888888888888")
        val selectedSeason = season(
            seriesId,
            listOf(episode(UUID.fromString("99999999-9999-4999-8999-999999999991"), seriesId, 1)),
        )
        val gateway = FakeGateway(
            restored = true,
            account = account(viewerProfile, active = true),
            collections = listOf(collection()),
        )
        gateway.resolvedTitle = io.rivune.api.TitleReference(
            seriesId,
            io.rivune.api.TitleMediaType.SERIES,
            "tmdb",
            "42",
            "tmdb:42",
            "Series",
        )
        gateway.seriesResult = series(seriesId)
        gateway.seasons = mapOf(selectedSeason.id to selectedSeason)
        gateway.progressBatchFailure = RivuneApiException.Server(404, "not_found", "failed")
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()

        viewModel.openMedia(MediaTarget(id = "tmdb:42", mediaType = "series", title = "Series"))
        advanceUntilIdle()
        viewModel.selectSeason(selectedSeason.id)
        advanceUntilIdle()

        val viewer = viewModel.state.value.viewer
        assertEquals(selectedSeason, viewer.detail?.season)
        assertTrue(viewer.detail?.episodeProgress?.isEmpty() == true)
        assertNull(viewer.loading)
        assertEquals(UiFailure.CONTENT_LOAD, viewer.inlineFailure)
    }

    @Test
    fun detailAndSeasonLoadMatchingTrailers() = runTest(dispatcher) {
        val viewerProfile = profile()
        val seriesId = UUID.fromString("88888888-8888-4888-8888-888888888888")
        val selectedSeason = season(
            seriesId,
            listOf(episode(UUID.fromString("99999999-9999-4999-8999-999999999991"), seriesId, 1)),
        )
        val titleTrailer = io.rivune.api.Trailer("title-trailer", "Title trailer", "en", false)
        val seasonTrailer = io.rivune.api.Trailer("season-trailer", "Season trailer", "en", false)
        val gateway = FakeGateway(
            restored = true,
            account = account(viewerProfile, active = true),
            collections = listOf(collection()),
        )
        gateway.resolvedTitle = io.rivune.api.TitleReference(
            seriesId,
            io.rivune.api.TitleMediaType.SERIES,
            "tmdb",
            "42",
            "tmdb:42",
            "Series",
        )
        gateway.seriesResult = series(seriesId)
        gateway.seasons = mapOf(selectedSeason.id to selectedSeason)
        gateway.trailerResults = mapOf(null to listOf(titleTrailer), selectedSeason.seasonNumber to listOf(seasonTrailer))
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()

        viewModel.openMedia(MediaTarget(id = "tmdb:42", mediaType = "series", title = "Series"))
        advanceUntilIdle()
        assertEquals(listOf(titleTrailer), viewModel.state.value.viewer.detail?.trailers)

        viewModel.selectSeason(selectedSeason.id)
        advanceUntilIdle()
        assertEquals(listOf(seasonTrailer), viewModel.state.value.viewer.detail?.seasonTrailers)
        assertEquals(listOf(seriesId to null, seriesId to selectedSeason.seasonNumber), gateway.trailerRequests)
    }

    @Test
    fun backingOutOfPendingSeasonLoadDoesNotReopenSeason() = runTest(dispatcher) {
        val viewerProfile = profile()
        val seriesId = UUID.fromString("88888888-8888-4888-8888-888888888888")
        val firstSeason = season(
            seriesId,
            listOf(episode(UUID.fromString("99999999-9999-4999-8999-999999999991"), seriesId, 1)),
        )
        val pendingSeason = firstSeason.copy(id = "season-2", name = "Season 2", seasonNumber = 2)
        val gateway = FakeGateway(
            restored = true,
            account = account(viewerProfile, active = true),
            collections = listOf(collection()),
        )
        gateway.resolvedTitle = io.rivune.api.TitleReference(seriesId, io.rivune.api.TitleMediaType.SERIES, "tmdb", "42", "tmdb:42", "Series")
        gateway.seriesResult = series(seriesId)
        gateway.seasons = mapOf(firstSeason.id to firstSeason, pendingSeason.id to pendingSeason)
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()
        viewModel.openMedia(MediaTarget(id = "tmdb:42", mediaType = "series", title = "Series"))
        advanceUntilIdle()
        viewModel.selectSeason(firstSeason.id)
        advanceUntilIdle()

        gateway.seasonDelayMillis = 1_000
        viewModel.selectSeason(pendingSeason.id)
        runCurrent()
        viewModel.backViewer()
        advanceTimeBy(1_000)
        advanceUntilIdle()

        assertNotNull(viewModel.state.value.viewer.detail)
        assertNull(viewModel.state.value.viewer.detail?.season)
    }

    @Test
    fun customProfileAvatarLoadsThroughGateway() = runTest(dispatcher) {
        val customProfile = profile(avatar = ProfileAvatar("custom", null, "/api/v1/profiles/$PROFILE_ID/avatar"))
        val avatar = byteArrayOf(1, 2, 3, 4)
        val gateway = FakeGateway(
            restored = true,
            account = account(customProfile),
        )
        gateway.profileAvatars = mapOf(customProfile.id to avatar)

        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()

        assertContentEquals(avatar, viewModel.state.value.profileAvatarData[customProfile.id])
    }


    @Test
    fun invalidRefreshTokenStartsFreshMobilePairing() = runTest(dispatcher) {
        val gateway = FakeGateway(
            restored = true,
            accountFailure = RivuneApiException.Server(401, "invalid_refresh_token", "expired"),
            pairingPending = true,
        )
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        runCurrent()

        assertIs<AppDestination.Pairing>(viewModel.state.value.destination)
        assertEquals("ABCD-EFGH", viewModel.state.value.pairing?.userCode)
        assertEquals(UiFailure.SESSION_EXPIRED, viewModel.state.value.failure)
        assertFalse(viewModel.state.value.isBusy)
        viewModel.viewModelScope.cancel()

    }

    @Test
    fun televisionSessionExpiryStartsFreshPairing() = runTest(dispatcher) {
        val profile = profile(hasPin = false)
        val gateway = FakeGateway(
            restored = true,
            account = account(profile, active = true),
            collectionFailure = RivuneApiException.Server(401, "invalid_refresh_token", "expired"),
            pairingPending = true,
        )
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway, isTv = true)
        runCurrent()

        assertIs<AppDestination.Pairing>(viewModel.state.value.destination)
        assertEquals("ABCD-EFGH", viewModel.state.value.pairing?.userCode)
        assertFalse(viewModel.state.value.isBusy)
        viewModel.viewModelScope.cancel()

    }


    @Test
    fun televisionPairingDisplaysCodeThenRoutesToProfiles() = runTest(dispatcher) {
        val profile = profile(hasPin = false)
        val gateway = FakeGateway(account = account(profile), pairingPending = true)
        val viewModel = viewModel(FakeServerStore(), gateway, isTv = true)

        viewModel.connect("https://media.example.com")
        runCurrent()

        assertIs<AppDestination.Pairing>(viewModel.state.value.destination)
        assertEquals("ABCD-EFGH", viewModel.state.value.pairing?.userCode)
        assertEquals("android_tv", gateway.authorizationPlatform)

        gateway.pairingPending = false
        advanceTimeBy(1_000)
        runCurrent()

        assertIs<AppDestination.Pairing>(viewModel.state.value.destination)
        assertTrue(viewModel.state.value.pairingAccepted)

        advanceTimeBy(550)
        runCurrent()
        assertIs<AppDestination.Profiles>(viewModel.state.value.destination)
        assertEquals(1, gateway.exchangeCount)
    }

    @Test
    fun mediaDetailsLibraryAndPlaybackProgressUseCanonicalTitle() = runTest(dispatcher) {
        val viewerProfile = profile(hasPin = false)
        val targetId = UUID.fromString("88888888-8888-4888-8888-888888888888")
        val playbackId = UUID.fromString("99999999-9999-4999-8999-999999999999")
        val addonId = UUID.fromString("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
        val gateway = FakeGateway(
            restored = true,
            account = account(viewerProfile, active = true),
            collections = listOf(collection()),
        )
        gateway.resolvedTitle = io.rivune.api.TitleReference(targetId, io.rivune.api.TitleMediaType.MOVIE, "imdb", "tt1234567", "tt1234567", "Film")
        gateway.movieResult = io.rivune.api.Movie(targetId, io.rivune.api.MediaType.MOVIE, "Film", "Film", "en", "Overview", "2026-08-12", genres = emptyList(), cast = emptyList(), voteAverage = 8.0, voteCount = 10, externalIds = mapOf("imdb" to "tt1234567"))
        gateway.effectiveSettingsResult = gateway.effectiveSettingsResult.copy(
            settings = io.rivune.api.SettingsValues(metadataLanguage = "fr-FR"),
        )
        gateway.progress = io.rivune.api.PlaybackProgress(targetId, io.rivune.api.PlaybackProgressMediaType.MOVIE, 120, 3600, false, 3, "2026-08-12T00:00:00Z", "2026-08-12T00:00:00Z")
        gateway.libraryPages = mapOf(1 to io.rivune.api.LibraryPage(emptyList(), 1, 1, 0))
        gateway.sourceList = io.rivune.api.PlaybackSourceList(
            listOf(io.rivune.api.PlaybackSourceOption("source", "ref", addonId, manifestId = "addon", streamIndex = 0, name = "Direct", protocol = "http", expiresAt = "2099-01-01T00:00:00Z", stableIdentity = "stable-direct")),
            emptyList(),
        )
        gateway.preparation = io.rivune.api.PlaybackPreparation("ref", io.rivune.api.PlaybackMode.DIRECT, "http", subtitleCount = 0, expiresAt = "2099-01-01T00:00:00Z")
        gateway.playbackSession = io.rivune.api.PlaybackSession(
            playbackId,
            "source",
            selectedSubtitleId = "subtitle",
            sources = listOf(
                io.rivune.api.PlaybackSource(
                    "source",
                    addonId,
                    "addon",
                    mode = io.rivune.api.PlaybackMode.DIRECT,
                    url = "/stream.m3u8",
                    protocol = "hls",
                    mediaTimeline = io.rivune.api.PlaybackMediaTimeline.RELATIVE,
                    compatible = true,
                ),
            ),
            subtitles = listOf(io.rivune.api.PlaybackSubtitle("subtitle", addonId, "addon", language = "fr", url = "/subtitle.vtt")),
            providerErrors = emptyList(),
            expiresAt = "2099-01-01T00:00:00Z",
        )
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()

        viewModel.openMedia(
            MediaTarget(
                id = "tt1234567",
                mediaType = "movie",
                title = "Film",
                released = "2026-08-12T15:30:45.123+02:00",
                sourceAddonId = addonId,
                sourceCatalogId = "catalog",
                sourceName = "Catalog",
                category = "Featured",
            ),
        )
        advanceUntilIdle()
        assertEquals(targetId, viewModel.state.value.viewer.detail?.titleId)
        assertEquals("Film", viewModel.state.value.viewer.detail?.movie?.title)
        assertEquals("fr-FR", gateway.metadataRequests.last { it.first == "movie" }.second)
        val resolvedInput = gateway.resolvedTitleInputs.single()
        assertEquals("2026-08-12", resolvedInput.released)
        assertEquals(addonId, resolvedInput.sourceAddonId)
        assertEquals("catalog", resolvedInput.sourceCatalogId)
        assertEquals("Catalog", resolvedInput.sourceName)
        assertEquals("Featured", resolvedInput.category)

        assertTrue(viewModel.state.value.viewer.sourcePickerVisible)
        assertEquals("source", requireNotNull(viewModel.state.value.viewer.sourcePicker).options.single().id)
        assertNull(viewModel.state.value.viewer.loading)
        assertEquals(listOf("tt1234567"), gateway.playbackSourceResources)
        assertNull(viewModel.state.value.viewer.sourcePicker?.nextEpisode)
        viewModel.selectPlaybackSource(requireNotNull(viewModel.state.value.viewer.sourcePicker).options.single())
        viewModel.choosePlaybackTarget(PlaybackTargetSelection.Embedded(EmbeddedPlayerPreference.AUTOMATIC))
        advanceUntilIdle()
        assertEquals(playbackId, viewModel.state.value.viewer.player?.sessionId)
        assertEquals(120_000L, viewModel.state.value.viewer.player?.startPositionMs)
        assertEquals("hls", viewModel.state.value.viewer.player?.protocol)
        assertEquals(io.rivune.api.PlaybackMediaTimeline.RELATIVE, viewModel.state.value.viewer.player?.mediaTimeline)
        assertEquals(true, viewModel.state.value.viewer.player?.subtitles?.single()?.selected)

        viewModel.reportPlayerProgress(180, 3600, false)
        advanceUntilIdle()
        assertEquals(180, gateway.progressUpdates.single().positionSeconds)
        assertEquals(4L, viewModel.state.value.viewer.player?.expectedProgressVersion)

        viewModel.closePlayer()
        advanceUntilIdle()
        assertEquals(playbackId, gateway.stoppedPlayback)
        assertEquals(1, gateway.stopPlaybackCalls)
        assertTrue(gateway.markerRequests.isEmpty())
        assertTrue(viewModel.state.value.viewer.sourcePickerVisible)
        assertEquals("source", requireNotNull(viewModel.state.value.viewer.sourcePicker).options.single().id)
        viewModel.selectPlaybackSource(requireNotNull(viewModel.state.value.viewer.sourcePicker).options.single())
        viewModel.choosePlaybackTarget(PlaybackTargetSelection.Embedded(EmbeddedPlayerPreference.AUTOMATIC))
        advanceUntilIdle()
        assertEquals(playbackId, viewModel.state.value.viewer.player?.sessionId)
        viewModel.beginTerminalOwnerDestruction()
        viewModel.reportPlayerProgress(240, 3600, false)
        viewModel.stopPlaybackForTerminalOwner()
        advanceUntilIdle()
        assertNull(viewModel.state.value.viewer.player)
        assertEquals(240, gateway.progressUpdates.last().positionSeconds)
        assertEquals("stop", gateway.playbackEvents.last())
        assertEquals(2, gateway.stopPlaybackCalls)
    }

    @Test
    fun featuredPlayLoadsDetailsAndOpensSources() = runTest(dispatcher) {
        val viewerProfile = profile(hasPin = false)
        val titleId = UUID.randomUUID()
        val addonId = UUID.randomUUID()
        val gateway = FakeGateway(
            restored = true,
            account = account(viewerProfile, active = true),
            collections = listOf(collection()),
        )
        gateway.resolvedTitle = io.rivune.api.TitleReference(
            titleId,
            io.rivune.api.TitleMediaType.MOVIE,
            "imdb",
            "tt1234567",
            "tt1234567",
            "Featured film",
        )
        gateway.progress = io.rivune.api.PlaybackProgress(
            titleId,
            io.rivune.api.PlaybackProgressMediaType.MOVIE,
            90,
            3600,
            false,
            1,
            "2026-08-12T00:00:00Z",
            "2026-08-12T00:00:00Z",
        )
        gateway.sourceList = io.rivune.api.PlaybackSourceList(
            listOf(
                io.rivune.api.PlaybackSourceOption(
                    "source",
                    "ref",
                    addonId,
                    manifestId = "addon",
                    streamIndex = 0,
                    name = "Direct",
                    protocol = "http",
                    expiresAt = "2099-01-01T00:00:00Z",
                    stableIdentity = "stable-direct",
                ),
            ),
            emptyList(),
        )
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()

        viewModel.openAndPlayMedia(MediaTarget("tt1234567", "movie", "Featured film"))
        assertEquals(ViewerLoading.DETAIL, viewModel.state.value.viewer.loading)
        assertNull(viewModel.state.value.viewer.detail)
        advanceUntilIdle()

        val picker = assertNotNull(viewModel.state.value.viewer.sourcePicker)
        assertEquals(titleId, viewModel.state.value.viewer.detail?.titleId)
        assertEquals(90, picker.progress?.positionSeconds)
        assertEquals("source", picker.options.single().id)
        assertEquals(titleId, viewModel.state.value.viewer.detail?.titleId)
    }

    @Test
    fun disabledAutomaticStreamsPrefetchesThenRevealsCachedSources() = runTest(dispatcher) {
        val titleId = UUID.randomUUID()
        val gateway = FakeGateway(
            restored = true,
            account = account(profile(hasPin = false), active = true),
            collections = listOf(collection()),
        ).apply {
            movieResult = io.rivune.api.Movie(
                titleId,
                io.rivune.api.MediaType.MOVIE,
                "Film",
                "Film",
                "en",
                "Overview",
                genres = emptyList(),
                cast = emptyList(),
                voteAverage = 0.0,
                voteCount = 0,
                externalIds = mapOf("imdb" to "tt1234567"),
            )
            configurePlayback(titleId)
        }
        val viewModel = viewModel(
            FakeServerStore("https://saved.example.com"),
            gateway,
            appPreferences = AppPreferencesReader {
                AppPreferencesState(automaticallyShowStreams = false)
            },
        )
        advanceUntilIdle()

        viewModel.openMedia(
            MediaTarget("movie", "movie", "Film", titleId = titleId, resourceId = "tt1234567"),
        )
        advanceUntilIdle()

        assertFalse(viewModel.state.value.viewer.sourcePickerVisible)
        assertEquals("source", viewModel.state.value.viewer.sourcePicker?.options?.single()?.id)
        assertEquals(listOf("tt1234567"), gateway.playbackSourceResources)

        viewModel.playMedia()

        assertTrue(viewModel.state.value.viewer.sourcePickerVisible)
        assertEquals(listOf("tt1234567"), gateway.playbackSourceResources)

        viewModel.backViewer()
        assertFalse(viewModel.state.value.viewer.sourcePickerVisible)
        assertNotNull(viewModel.state.value.viewer.detail)

        viewModel.playMedia()
        viewModel.backDetail()
        assertNull(viewModel.state.value.viewer.detail)
        assertNull(viewModel.state.value.viewer.sourcePicker)
    }

    @Test
    fun expiredCachedSourcesReloadBeforeReveal() = runTest(dispatcher) {
        val titleId = UUID.randomUUID()
        val gateway = FakeGateway(
            restored = true,
            account = account(profile(hasPin = false), active = true),
            collections = listOf(collection()),
        ).apply {
            movieResult = io.rivune.api.Movie(
                titleId,
                io.rivune.api.MediaType.MOVIE,
                "Film",
                "Film",
                "en",
                "Overview",
                genres = emptyList(),
                cast = emptyList(),
                voteAverage = 0.0,
                voteCount = 0,
                externalIds = mapOf("imdb" to "tt1234567"),
            )
            val expiredSource = configurePlayback(titleId).copy(expiresAt = "2000-01-01T00:00:00Z")
            sourceList = io.rivune.api.PlaybackSourceList(listOf(expiredSource), emptyList())
        }
        val viewModel = viewModel(
            FakeServerStore("https://saved.example.com"),
            gateway,
            appPreferences = AppPreferencesReader {
                AppPreferencesState(automaticallyShowStreams = false)
            },
        )
        advanceUntilIdle()

        viewModel.openMedia(
            MediaTarget("movie", "movie", "Film", titleId = titleId, resourceId = "tt1234567"),
        )
        advanceUntilIdle()

        assertEquals(listOf("tt1234567"), gateway.playbackSourceResources)
        viewModel.playMedia()
        runCurrent()

        assertEquals(listOf("tt1234567", "tt1234567"), gateway.playbackSourceResources)
        assertTrue(viewModel.state.value.viewer.sourcePickerVisible)
    }

    @Test
    fun validCachedSourcesReuseCurrentDetailProgress() = runTest(dispatcher) {
        val titleId = UUID.randomUUID()
        val initialProgress = io.rivune.api.PlaybackProgress(
            titleId,
            io.rivune.api.PlaybackProgressMediaType.MOVIE,
            120,
            3_600,
            false,
            4,
            "2026-08-12T00:00:00Z",
            "2026-08-12T00:00:00Z",
        )
        val gateway = FakeGateway(
            restored = true,
            account = account(profile(hasPin = false), active = true),
            collections = listOf(collection()),
        ).apply {
            movieResult = io.rivune.api.Movie(
                titleId,
                io.rivune.api.MediaType.MOVIE,
                "Film",
                "Film",
                "en",
                "Overview",
                genres = emptyList(),
                cast = emptyList(),
                voteAverage = 0.0,
                voteCount = 0,
                externalIds = mapOf("imdb" to "tt1234567"),
            )
            configurePlayback(titleId)
            progress = initialProgress
        }
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()

        viewModel.openMedia(
            MediaTarget("movie", "movie", "Film", titleId = titleId, resourceId = "tt1234567"),
        )
        advanceUntilIdle()
        var picker = assertNotNull(viewModel.state.value.viewer.sourcePicker)
        viewModel.selectPlaybackSource(picker.options.single())
        viewModel.choosePlaybackTarget(PlaybackTargetSelection.Embedded(EmbeddedPlayerPreference.AUTOMATIC))
        advanceUntilIdle()
        viewModel.reportPlayerProgress(600, 3_600, false)
        advanceUntilIdle()
        viewModel.closePlayer()
        advanceUntilIdle()

        val currentProgress = assertNotNull(viewModel.state.value.viewer.detail?.progress)
        assertEquals(5L, currentProgress.version)
        assertEquals(600, currentProgress.positionSeconds)
        assertTrue(viewModel.state.value.viewer.sourcePickerVisible)

        picker = assertNotNull(viewModel.state.value.viewer.sourcePicker)
        assertEquals(listOf("tt1234567"), gateway.playbackSourceResources)
        assertTrue(viewModel.state.value.viewer.sourcePickerVisible)
        viewModel.selectPlaybackSource(picker.options.single())
        viewModel.choosePlaybackTarget(PlaybackTargetSelection.Embedded(EmbeddedPlayerPreference.AUTOMATIC))
        advanceUntilIdle()
        assertEquals(currentProgress, viewModel.state.value.viewer.sourcePicker?.progress)

        assertEquals(listOf<Int?>(120, 600), gateway.preparedStartSeconds)
        assertEquals(5L, viewModel.state.value.viewer.player?.expectedProgressVersion)
    }

    @Test
    fun expiredVisibleSourceReloadsBeforePlayback() = runTest(dispatcher) {
        val titleId = UUID.randomUUID()
        var now = Instant.parse("2026-08-17T00:00:00Z")
        val gateway = FakeGateway(
            restored = true,
            account = account(profile(hasPin = false), active = true),
            collections = listOf(collection()),
        ).apply {
            movieResult = io.rivune.api.Movie(
                titleId,
                io.rivune.api.MediaType.MOVIE,
                "Film",
                "Film",
                "en",
                "Overview",
                genres = emptyList(),
                cast = emptyList(),
                voteAverage = 0.0,
                voteCount = 0,
                externalIds = mapOf("imdb" to "tt1234567"),
            )
            val expiring = configurePlayback(titleId).copy(
                sourceRef = "expiring-ref",
                expiresAt = "2026-08-17T01:00:00Z",
            )
            sourceList = io.rivune.api.PlaybackSourceList(listOf(expiring), emptyList())
        }
        val viewModel = viewModel(
            FakeServerStore("https://saved.example.com"),
            gateway,
            instantNow = { now },
        )
        advanceUntilIdle()

        viewModel.openMedia(
            MediaTarget("movie", "movie", "Film", titleId = titleId, resourceId = "tt1234567"),
        )
        advanceUntilIdle()
        var picker = assertNotNull(viewModel.state.value.viewer.sourcePicker)
        viewModel.selectPlaybackSource(picker.options.single())
        viewModel.choosePlaybackTarget(PlaybackTargetSelection.Embedded(EmbeddedPlayerPreference.AUTOMATIC))
        advanceUntilIdle()
        assertEquals(1, gateway.preparePlaybackCalls)

        viewModel.closePlayer()
        advanceUntilIdle()
        now = Instant.parse("2026-08-17T02:00:00Z")
        val fresh = picker.options.single().copy(
            sourceRef = "fresh-ref",
            expiresAt = "2026-08-17T03:00:00Z",
        )
        gateway.sourceList = io.rivune.api.PlaybackSourceList(listOf(fresh), emptyList())

        picker = assertNotNull(viewModel.state.value.viewer.sourcePicker)
        viewModel.selectPlaybackSource(picker.options.single())
        viewModel.choosePlaybackTarget(PlaybackTargetSelection.Embedded(EmbeddedPlayerPreference.AUTOMATIC))
        advanceUntilIdle()

        assertEquals(listOf("tt1234567", "tt1234567"), gateway.playbackSourceResources)
        assertEquals(1, gateway.preparePlaybackCalls)
        assertEquals("fresh-ref", viewModel.state.value.viewer.sourcePicker?.options?.single()?.sourceRef)
    }

    @Test
    fun dismissingManualSourceLoadKeepsRailClosedAndCachesResponse() = runTest(dispatcher) {
        val titleId = UUID.randomUUID()
        val gateway = FakeGateway(
            restored = true,
            account = account(profile(hasPin = false), active = true),
            collections = listOf(collection()),
        ).apply {
            movieResult = io.rivune.api.Movie(
                titleId,
                io.rivune.api.MediaType.MOVIE,
                "Film",
                "Film",
                "en",
                "Overview",
                genres = emptyList(),
                cast = emptyList(),
                voteAverage = 0.0,
                voteCount = 0,
                externalIds = mapOf("imdb" to "tt1234567"),
            )
            configurePlayback(titleId)
            playbackSourceDelayMillis = 1_000
        }
        val viewModel = viewModel(
            FakeServerStore("https://saved.example.com"),
            gateway,
            appPreferences = AppPreferencesReader {
                AppPreferencesState(automaticallyShowStreams = false)
            },
        )
        advanceUntilIdle()

        viewModel.openMedia(
            MediaTarget("movie", "movie", "Film", titleId = titleId, resourceId = "tt1234567"),
        )
        runCurrent()
        viewModel.playMedia()

        assertTrue(viewModel.state.value.viewer.sourcePickerVisible)
        assertEquals(ViewerLoading.SOURCES, viewModel.state.value.viewer.loading)

        viewModel.backViewer()
        advanceTimeBy(1_000)
        runCurrent()

        assertFalse(viewModel.state.value.viewer.sourcePickerVisible)
        assertEquals("source", viewModel.state.value.viewer.sourcePicker?.options?.single()?.id)
        assertNull(viewModel.state.value.viewer.loading)
        assertNotNull(viewModel.state.value.viewer.detail)

        viewModel.playMedia()

        assertTrue(viewModel.state.value.viewer.sourcePickerVisible)
        assertEquals(listOf("tt1234567"), gateway.playbackSourceResources)
    }

    @Test
    fun hiddenPrefetchCompletionPreservesWatchedActionState() = runTest(dispatcher) {
        val titleId = UUID.randomUUID()
        val gateway = FakeGateway(
            restored = true,
            account = account(profile(hasPin = false), active = true),
            collections = listOf(collection()),
        ).apply {
            movieResult = io.rivune.api.Movie(
                titleId,
                io.rivune.api.MediaType.MOVIE,
                "Film",
                "Film",
                "en",
                "Overview",
                genres = emptyList(),
                cast = emptyList(),
                voteAverage = 0.0,
                voteCount = 0,
                externalIds = mapOf("imdb" to "tt1234567"),
            )
            configurePlayback(titleId)
            playbackSourceDelayMillis = 500
            watchedDelayMillis = 1_000
        }
        val viewModel = viewModel(
            FakeServerStore("https://saved.example.com"),
            gateway,
            appPreferences = AppPreferencesReader {
                AppPreferencesState(automaticallyShowStreams = false)
            },
        )
        advanceUntilIdle()

        viewModel.openMedia(
            MediaTarget("movie", "movie", "Film", titleId = titleId, resourceId = "tt1234567"),
        )
        runCurrent()
        viewModel.toggleWatched()

        assertEquals(ViewerLoading.ACTION, viewModel.state.value.viewer.loading)
        advanceTimeBy(500)
        runCurrent()

        assertEquals(ViewerLoading.ACTION, viewModel.state.value.viewer.loading)
        assertEquals("source", viewModel.state.value.viewer.sourcePicker?.options?.single()?.id)
        assertFalse(viewModel.state.value.viewer.sourcePickerVisible)

        advanceTimeBy(500)
        runCurrent()
        assertNull(viewModel.state.value.viewer.loading)
        assertTrue(viewModel.state.value.viewer.detail?.progress?.completed == true)
    }

    @Test
    fun hiddenPrefetchCompletionPreservesWatchedFailure() = runTest(dispatcher) {
        val titleId = UUID.randomUUID()
        val gateway = FakeGateway(
            restored = true,
            account = account(profile(hasPin = false), active = true),
            collections = listOf(collection()),
        ).apply {
            movieResult = io.rivune.api.Movie(
                titleId,
                io.rivune.api.MediaType.MOVIE,
                "Film",
                "Film",
                "en",
                "Overview",
                genres = emptyList(),
                cast = emptyList(),
                voteAverage = 0.0,
                voteCount = 0,
                externalIds = mapOf("imdb" to "tt1234567"),
            )
            configurePlayback(titleId)
            playbackSourceDelayMillis = 1_000
            watchedFailure = RivuneApiException.Server(500, "watched_failed", "failed")
        }
        val viewModel = viewModel(
            FakeServerStore("https://saved.example.com"),
            gateway,
            appPreferences = AppPreferencesReader {
                AppPreferencesState(automaticallyShowStreams = false)
            },
        )
        advanceUntilIdle()

        viewModel.openMedia(
            MediaTarget("movie", "movie", "Film", titleId = titleId, resourceId = "tt1234567"),
        )
        runCurrent()
        viewModel.toggleWatched()
        runCurrent()

        assertEquals(UiFailure.ACTION, viewModel.state.value.viewer.inlineFailure)
        advanceTimeBy(1_000)
        runCurrent()

        assertEquals(UiFailure.ACTION, viewModel.state.value.viewer.inlineFailure)
        assertEquals("source", viewModel.state.value.viewer.sourcePicker?.options?.single()?.id)
        assertFalse(viewModel.state.value.viewer.sourcePickerVisible)
    }

    @Test
    fun refreshingPlaybackSourcesReloadsCurrentResourceAndKeepsPickerOpen() = runTest(dispatcher) {
        val viewerProfile = profile(hasPin = false)
        val titleId = UUID.randomUUID()
        val gateway = FakeGateway(
            restored = true,
            account = account(viewerProfile, active = true),
            collections = listOf(collection()),
        )
        gateway.resolvedTitle = io.rivune.api.TitleReference(
            titleId,
            io.rivune.api.TitleMediaType.MOVIE,
            "imdb",
            "tt1234567",
            "tt1234567",
            "Film",
        )
        gateway.movieResult = io.rivune.api.Movie(
            titleId,
            io.rivune.api.MediaType.MOVIE,
            "Film",
            "Film",
            "en",
            "Overview",
            "2026-08-12",
            genres = emptyList(),
            cast = emptyList(),
            voteAverage = 8.0,
            voteCount = 10,
            externalIds = mapOf("imdb" to "tt1234567"),
        )
        gateway.configurePlayback(titleId)
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()
        viewModel.openMedia(
            MediaTarget(
                id = "catalog-entry",
                mediaType = "movie",
                title = "Film",
                resourceId = "tt1234567",
            ),
        )
        advanceUntilIdle()
        viewModel.playMedia()
        advanceUntilIdle()

        val originalPicker = assertNotNull(viewModel.state.value.viewer.sourcePicker)
        val metadataRequestCount = gateway.metadataRequests.size
        assertEquals(listOf("tt1234567"), gateway.playbackSourceResources)

        viewModel.refreshPlaybackSources()

        val pendingPicker = assertNotNull(viewModel.state.value.viewer.sourcePicker)
        assertEquals(originalPicker.target, pendingPicker.target)
        assertEquals(originalPicker.titleId, pendingPicker.titleId)
        assertEquals(originalPicker.progress, pendingPicker.progress)
        assertTrue(pendingPicker.options.isEmpty())
        assertEquals(ViewerLoading.SOURCES, viewModel.state.value.viewer.loading)

        advanceUntilIdle()

        assertEquals(listOf("tt1234567", "tt1234567"), gateway.playbackSourceResources)
        assertNotNull(viewModel.state.value.viewer.sourcePicker)
        assertTrue(viewModel.state.value.viewer.sourcePicker!!.options.isNotEmpty())
        assertNull(viewModel.state.value.viewer.loading)
        assertEquals(metadataRequestCount, gateway.metadataRequests.size)
    }

    @Test
    fun episodeFromLoadedSeriesPublishesExactMarkersWithoutReloadingSeries() = runTest(dispatcher) {
        val seriesId = UUID.randomUUID()
        val episodeId = UUID.randomUUID()
        val series = series(seriesId, "tt12345678")
        val episode = episode(episodeId, seriesId, 1)
        val season = season(seriesId, listOf(episode))
        val marker = io.rivune.api.PlaybackMarker(
            io.rivune.api.PlaybackMarkerType.INTRO,
            12.5,
            87.25,
            0.98,
            14,
        )
        val gateway = FakeGateway(
            restored = true,
            account = account(profile(hasPin = false), active = true),
            collections = listOf(collection()),
        ).apply {
            resolvedTitle = io.rivune.api.TitleReference(
                seriesId,
                io.rivune.api.TitleMediaType.SERIES,
                "tmdb",
                "42",
                "tmdb:42",
                "Series",
            )
            seriesResult = series
            seasons = mapOf(season.id to season)
            markerResult = io.rivune.api.PlaybackMarkerList(listOf(marker))
            markerDelayMillis = 1_000
        }
        val source = gateway.configurePlayback(episodeId)
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()

        viewModel.openMedia(MediaTarget("tmdb:42", "series", "Series"))
        advanceUntilIdle()
        viewModel.selectSeason(season.id)
        advanceUntilIdle()
        val detail = requireNotNull(viewModel.state.value.viewer.detail)
        val target = episode.toMediaTarget(series, detail.target)
        assertEquals("tt12345678", target.seriesImdbId)
        val seriesLoads = gateway.metadataRequests.count { it.first == "series" }

        viewModel.playMedia(target)
        advanceUntilIdle()
        val picker = requireNotNull(viewModel.state.value.viewer.sourcePicker)
        assertEquals(PlaybackMarkerRequest("tt12345678", 1, 1), picker.markerRequest)
        assertEquals(listOf("tt12345678:1:1"), gateway.playbackSourceResources)
        assertEquals(seriesLoads, gateway.metadataRequests.count { it.first == "series" })

        viewModel.selectPlaybackSource(source)
        viewModel.choosePlaybackTarget(PlaybackTargetSelection.Embedded(EmbeddedPlayerPreference.AUTOMATIC))
        runCurrent()

        assertNotNull(viewModel.state.value.viewer.player)
        assertTrue(viewModel.state.value.viewer.player?.markers?.isEmpty() == true)
        assertEquals(listOf(PlaybackMarkerRequest("tt12345678", 1, 1)), gateway.markerRequests)

        advanceTimeBy(1_000)
        runCurrent()
        assertEquals(listOf(marker), viewModel.state.value.viewer.player?.markers)
    }

    @Test
    fun variantContinuationRetainsTvdbOrderAcrossPlaybackAndNextEpisode() = runTest(dispatcher) {
        val seriesId = UUID.randomUUID()
        val persistedSeasonId = UUID.randomUUID()
        val currentEpisodeId = UUID.randomUUID()
        val nextEpisodeId = UUID.randomUUID()
        val metadataSeasonId = "tvdb:$seriesId:2112814"
        val currentEpisode = episode(
            currentEpisodeId,
            seriesId,
            number = 1,
            seasonId = metadataSeasonId,
        ).copy(externalIds = mapOf("tvdb" to "10357450"))
        val nextEpisode = episode(
            nextEpisodeId,
            seriesId,
            number = 2,
            seasonId = metadataSeasonId,
        ).copy(externalIds = mapOf("tvdb" to "10357451"))
        val variantSeason = season(
            seriesId,
            listOf(currentEpisode, nextEpisode),
            id = metadataSeasonId,
        )
        val gateway = FakeGateway(
            restored = true,
            account = account(profile(hasPin = false), active = true),
            collections = listOf(collection()),
        ).apply {
            continueWatchingPage = io.rivune.api.ContinueWatchingPage(
                listOf(
                    io.rivune.api.ContinueWatchingItem(
                        titleId = currentEpisodeId,
                        mediaType = io.rivune.api.PlaybackProgressMediaType.EPISODE,
                        seriesId = seriesId,
                        seasonId = persistedSeasonId,
                        seasonNumber = 1,
                        episodeNumber = 1,
                        mappingProvider = " TVDB ",
                        episodeOrderId = "2",
                        metadataSeasonId = metadataSeasonId,
                        title = "Variant Series",
                        resourceId = "tvdb:10357450",
                        resourceProvider = "tvdb",
                        episodeTitle = "Variant Episode 1",
                        positionSeconds = 120,
                        durationSeconds = 1_800,
                        version = 1,
                        reason = io.rivune.api.ContinueWatchingReason.RESUME,
                        lastWatchedAt = "2026-09-04T00:00:00Z",
                    ),
                ),
            )
            seriesResult = series(seriesId, "tt12345678").copy(
                seasons = listOf(seasonSummary(seriesId, metadataSeasonId, 1, 2)),
                mappingProvider = io.rivune.api.SeriesMappingProvider.TVDB,
            )
            seasons = mapOf(metadataSeasonId to variantSeason)
        }
        val source = gateway.configurePlayback(currentEpisodeId)
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()

        val continuation = viewModel.state.value.viewer.continueWatching.single()
        assertEquals(io.rivune.api.SeriesMappingProvider.TVDB, continuation.mappingProvider)
        assertEquals("2", continuation.episodeOrderId)
        assertEquals(metadataSeasonId, continuation.metadataSeasonId)
        assertEquals(persistedSeasonId.toString(), continuation.seasonId)
        assertEquals(120, continuation.resumePositionSeconds)
        assertEquals(1_800, continuation.durationSeconds)
        val nestedCurrent = currentEpisode.toMediaTarget(requireNotNull(gateway.seriesResult), continuation)
        assertEquals("tvdb:10357450", nestedCurrent.resourceId)
        assertEquals(io.rivune.api.SeriesMappingProvider.TVDB, nestedCurrent.mappingProvider)
        assertEquals("2", nestedCurrent.episodeOrderId)
        assertEquals(metadataSeasonId, nestedCurrent.metadataSeasonId)
        assertEquals(persistedSeasonId.toString(), nestedCurrent.seasonId)
        assertEquals(120, nestedCurrent.resumePositionSeconds)
        assertEquals(1_800, nestedCurrent.durationSeconds)

        viewModel.openMedia(continuation)
        advanceUntilIdle()

        assertEquals(
            listOf<Triple<UUID, io.rivune.api.SeriesMappingProvider, String?>>(
                Triple(seriesId, io.rivune.api.SeriesMappingProvider.TVDB, "2"),
            ),
            gateway.seriesRequests,
        )
        assertEquals(
            listOf(metadataSeasonId to io.rivune.api.SeriesMappingProvider.TVDB),
            gateway.seasonRequests,
        )
        assertEquals(listOf("tvdb:10357450"), gateway.playbackSourceResources)
        val picker = assertNotNull(viewModel.state.value.viewer.sourcePicker)
        assertNull(picker.markerRequest)
        val next = assertNotNull(picker.nextEpisode)
        assertEquals(nextEpisodeId, next.titleId)
        assertEquals("tvdb:10357451", next.resourceId)
        assertEquals(io.rivune.api.SeriesMappingProvider.TVDB, next.mappingProvider)
        assertEquals("2", next.episodeOrderId)
        assertEquals(metadataSeasonId, next.metadataSeasonId)
        assertEquals(persistedSeasonId.toString(), next.seasonId)
        assertEquals(0, next.resumePositionSeconds)
        assertEquals(0, next.durationSeconds)

        viewModel.selectPlaybackSource(source)
        viewModel.choosePlaybackTarget(PlaybackTargetSelection.Embedded(EmbeddedPlayerPreference.AUTOMATIC))
        advanceUntilIdle()
        assertTrue(gateway.markerRequests.isEmpty())
    }

    @Test
    fun incompleteVariantContinuationClearsContextAndUsesCanonicalHierarchy() = runTest(dispatcher) {
        val seriesId = UUID.randomUUID()
        val persistedSeasonId = UUID.randomUUID()
        val episodeId = UUID.randomUUID()
        val opaqueSeasonId = "tvdb:$seriesId:2112814"
        val canonicalEpisode = episode(episodeId, seriesId, number = 1)
        val canonicalSeason = season(seriesId, listOf(canonicalEpisode))
        val gateway = FakeGateway(
            restored = true,
            account = account(profile(hasPin = false), active = true),
            collections = listOf(collection()),
        ).apply {
            continueWatchingPage = io.rivune.api.ContinueWatchingPage(
                listOf(
                    io.rivune.api.ContinueWatchingItem(
                        titleId = episodeId,
                        mediaType = io.rivune.api.PlaybackProgressMediaType.EPISODE,
                        seriesId = seriesId,
                        seasonId = persistedSeasonId,
                        seasonNumber = 1,
                        episodeNumber = 1,
                        mappingProvider = " tvdb ",
                        episodeOrderId = null,
                        metadataSeasonId = opaqueSeasonId,
                        title = "Canonical Series",
                        resourceId = "tt12345678:1:1",
                        resourceProvider = "imdb",
                        episodeTitle = "Canonical Episode 1",
                        positionSeconds = 120,
                        durationSeconds = 1_800,
                        version = 1,
                        reason = io.rivune.api.ContinueWatchingReason.RESUME,
                        lastWatchedAt = "2026-09-04T00:00:00Z",
                    ),
                ),
            )
            seriesResult = series(seriesId, "tt12345678").copy(
                seasons = listOf(seasonSummary(seriesId, canonicalSeason.id, 1, 1)),
            )
            seasons = mapOf(canonicalSeason.id to canonicalSeason)
        }
        val source = gateway.configurePlayback(episodeId)
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()

        val continuation = viewModel.state.value.viewer.continueWatching.single()
        assertNull(continuation.mappingProvider)
        assertNull(continuation.episodeOrderId)
        assertNull(continuation.metadataSeasonId)

        viewModel.openMedia(continuation)
        advanceUntilIdle()

        assertEquals(
            listOf<Triple<UUID, io.rivune.api.SeriesMappingProvider, String?>>(
                Triple(seriesId, io.rivune.api.SeriesMappingProvider.TMDB, null),
            ),
            gateway.seriesRequests,
        )
        assertEquals(
            listOf(canonicalSeason.id to io.rivune.api.SeriesMappingProvider.TMDB),
            gateway.seasonRequests,
        )
        assertTrue(gateway.seasonRequests.none { it.first == opaqueSeasonId })
        val picker = assertNotNull(viewModel.state.value.viewer.sourcePicker)
        assertEquals(PlaybackMarkerRequest("tt12345678", 1, 1), picker.markerRequest)

        viewModel.selectPlaybackSource(source)
        viewModel.choosePlaybackTarget(PlaybackTargetSelection.Embedded(EmbeddedPlayerPreference.AUTOMATIC))
        advanceUntilIdle()
        assertEquals(listOf(PlaybackMarkerRequest("tt12345678", 1, 1)), gateway.markerRequests)
    }

    @Test
    fun canonicalContinuationDiscoversOfficialTvdbFallbackOrder() = runTest(dispatcher) {
        val seriesId = UUID.randomUUID()
        val episodeId = UUID.randomUUID()
        val nextEpisodeId = UUID.randomUUID()
        val officialSeasonId = "tvdb:$seriesId:1001"
        val alternateSeasonId = "tvdb:$seriesId:2112814"
        val profileDefaultSeries = series(seriesId).copy(
            seasons = listOf(seasonSummary(seriesId, alternateSeasonId, 1, 1)),
            episodeOrders = listOf(
                io.rivune.api.EpisodeOrder("1", "Aired Order", "official", false),
                io.rivune.api.EpisodeOrder("2", "DVD Order", "dvd", true),
            ),
            selectedEpisodeOrderId = "2",
            mappingProvider = io.rivune.api.SeriesMappingProvider.TVDB,
            externalIds = mapOf("tvdb" to "42"),
        )
        val officialSeries = profileDefaultSeries.copy(
            seasons = listOf(seasonSummary(seriesId, officialSeasonId, 1, 2)),
            selectedEpisodeOrderId = "1",
        )
        val gateway = FakeGateway(
            restored = true,
            account = account(profile(hasPin = false), active = true),
            collections = listOf(collection()),
        ).apply {
            continueWatchingPage = io.rivune.api.ContinueWatchingPage(
                listOf(
                    io.rivune.api.ContinueWatchingItem(
                        titleId = episodeId,
                        mediaType = io.rivune.api.PlaybackProgressMediaType.EPISODE,
                        seriesId = seriesId,
                        seasonNumber = 1,
                        episodeNumber = 1,
                        title = "Canonical Series",
                        resourceId = "tt12345678:1:1",
                        resourceProvider = "imdb",
                        episodeTitle = "Canonical Episode 1",
                        positionSeconds = 120,
                        durationSeconds = 1_800,
                        version = 1,
                        reason = io.rivune.api.ContinueWatchingReason.RESUME,
                        lastWatchedAt = "2026-09-04T00:00:00Z",
                    ),
                ),
            )
            seriesResult = profileDefaultSeries
            seriesResults = mapOf(
                Pair(io.rivune.api.SeriesMappingProvider.TVDB, "1") to officialSeries,
            )
            seriesFailures = mapOf(
                io.rivune.api.SeriesMappingProvider.TMDB to IllegalStateException("canonical unavailable"),
            )
            seasons = mapOf(
                officialSeasonId to season(
                    seriesId,
                    listOf(
                        episode(episodeId, seriesId, 1, seasonId = officialSeasonId),
                        episode(nextEpisodeId, seriesId, 2, seasonId = officialSeasonId),
                    ),
                    id = officialSeasonId,
                ),
                alternateSeasonId to season(
                    seriesId,
                    listOf(episode(episodeId, seriesId, 1, seasonId = alternateSeasonId)),
                    id = alternateSeasonId,
                ),
            )
        }
        gateway.configurePlayback(episodeId)
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()

        val continuation = viewModel.state.value.viewer.continueWatching.single()
        assertNull(continuation.mappingProvider)
        assertNull(continuation.episodeOrderId)
        assertNull(continuation.metadataSeasonId)

        viewModel.openMedia(continuation)
        advanceUntilIdle()

        assertEquals(
            listOf<Triple<UUID, io.rivune.api.SeriesMappingProvider, String?>>(
                Triple(seriesId, io.rivune.api.SeriesMappingProvider.TMDB, null),
                Triple(seriesId, io.rivune.api.SeriesMappingProvider.TVDB, null),
                Triple(seriesId, io.rivune.api.SeriesMappingProvider.TVDB, "1"),
            ),
            gateway.seriesRequests,
        )
        assertEquals(
            listOf(officialSeasonId to io.rivune.api.SeriesMappingProvider.TVDB),
            gateway.seasonRequests,
        )
        assertTrue(gateway.seasonRequests.none { it.first == alternateSeasonId })
        val nextEpisode = assertNotNull(viewModel.state.value.viewer.sourcePicker?.nextEpisode)
        assertNull(nextEpisode.mappingProvider)
        assertNull(nextEpisode.episodeOrderId)
        assertNull(nextEpisode.metadataSeasonId)
    }


    @Test
    fun episodeEntryResolvesSeriesOnceAndMarkerFailureFailsOpen() = runTest(dispatcher) {
        val seriesId = UUID.randomUUID()
        val episodeId = UUID.randomUUID()
        val castMember = io.rivune.api.CastMember("person-direct", "Direct Lead", "Character")
        val series = series(seriesId, "tt7654321")
        val episode = episode(episodeId, seriesId, 3, seasonNumber = 2)
        val season = season(seriesId, listOf(episode), number = 2)
        val gateway = FakeGateway(
            restored = true,
            account = account(profile(hasPin = false), active = true),
            collections = listOf(collection()),
        ).apply {
            seriesResult = series.copy(
                seasons = listOf(seasonSummary(seriesId, season.id, 2, 1)),
                cast = listOf(castMember),
            )
            seasons = mapOf(season.id to season)
            markerFailure = IllegalStateException("provider unavailable")
        }
        val source = gateway.configurePlayback(episodeId)
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()
        viewModel.openMedia(
            MediaTarget(
                id = "calendar-episode",
                mediaType = "episode",
                title = "Episode 3",
                titleId = episodeId,
                seriesId = seriesId,
                seasonId = season.id,
                seasonNumber = 2,
                episodeNumber = 3,
            ),
        )
        advanceUntilIdle()

        assertEquals(1, gateway.metadataRequests.count { it.first == "series" })
        assertEquals(listOf(castMember), viewModel.state.value.viewer.detail?.cast)
        assertEquals(PlaybackMarkerRequest("tt7654321", 2, 3), viewModel.state.value.viewer.sourcePicker?.markerRequest)

        viewModel.selectPlaybackSource(source)
        viewModel.choosePlaybackTarget(PlaybackTargetSelection.Embedded(EmbeddedPlayerPreference.AUTOMATIC))
        advanceUntilIdle()

        assertNotNull(viewModel.state.value.viewer.player)
        assertTrue(viewModel.state.value.viewer.player?.markers?.isEmpty() == true)
        assertNull(viewModel.state.value.viewer.inlineFailure)
        assertEquals(listOf(PlaybackMarkerRequest("tt7654321", 2, 3)), gateway.markerRequests)
    }

    @Test
    fun externalEpisodePlaybackDoesNotRequestOrCarryMarkers() = runTest(dispatcher) {
        val episodeId = UUID.randomUUID()
        val externalPlayer = ExternalPlayerApp(
            packageName = "org.example.player",
            label = "Example Player",
            videoMimeTypes = setOf("video/*"),
            supportsMagnet = false,
        )
        val gateway = FakeGateway(
            restored = true,
            account = account(profile(hasPin = false), active = true),
            collections = listOf(collection()),
        )
        val source = gateway.configurePlayback(episodeId)
        val viewModel = viewModel(
            FakeServerStore("https://saved.example.com"),
            gateway,
            externalPlaybackSupport = ExternalPlaybackSupport(listOf(externalPlayer)),
        )
        advanceUntilIdle()
        viewModel.openMedia(
            MediaTarget(
                id = "episode",
                mediaType = "episode",
                title = "Episode",
                titleId = episodeId,
                seriesImdbId = "tt1234567",
                seasonNumber = 1,
                episodeNumber = 1,
            ),
        )
        advanceUntilIdle()
        viewModel.playMedia()
        advanceUntilIdle()
        assertNotNull(viewModel.state.value.viewer.sourcePicker?.markerRequest)

        viewModel.selectPlaybackSource(source)
        viewModel.choosePlaybackTarget(PlaybackTargetSelection.External(externalPlayer))
        advanceUntilIdle()

        assertEquals(externalPlayer, viewModel.state.value.viewer.player?.externalPlayer)
        assertTrue(viewModel.state.value.viewer.player?.markers?.isEmpty() == true)
        assertTrue(gateway.markerRequests.isEmpty())
    }

    @Test
    fun staleMarkerCompletionCannotReplaceMarkersOnNewPlayer() = runTest(dispatcher) {
        val episodeId = UUID.randomUUID()
        val oldSessionId = UUID.randomUUID()
        val newSessionId = UUID.randomUUID()
        val oldMarker = io.rivune.api.PlaybackMarker(io.rivune.api.PlaybackMarkerType.RECAP, 0.0, 45.0, 0.8, 3)
        val newMarker = io.rivune.api.PlaybackMarker(io.rivune.api.PlaybackMarkerType.OUTRO, 1_700.0, 1_795.0, 0.9, 8)
        val gateway = FakeGateway(
            restored = true,
            account = account(profile(hasPin = false), active = true),
            collections = listOf(collection()),
        ).apply {
            markerResultsByCall = listOf(
                io.rivune.api.PlaybackMarkerList(listOf(oldMarker)),
                io.rivune.api.PlaybackMarkerList(listOf(newMarker)),
            )
            markerDelaysByCall = listOf(1_000, 0)
        }
        val source = gateway.configurePlayback(episodeId, oldSessionId)
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()
        viewModel.openMedia(
            MediaTarget(
                id = "episode",
                mediaType = "episode",
                title = "Episode",
                titleId = episodeId,
                seriesImdbId = "tt1234567",
                seasonNumber = 1,
                episodeNumber = 1,
            ),
        )
        advanceUntilIdle()
        viewModel.playMedia()
        advanceUntilIdle()
        viewModel.selectPlaybackSource(source)
        viewModel.choosePlaybackTarget(PlaybackTargetSelection.Embedded(EmbeddedPlayerPreference.AUTOMATIC))
        runCurrent()
        assertEquals(oldSessionId, viewModel.state.value.viewer.player?.sessionId)

        viewModel.closePlayer()
        gateway.configurePlayback(episodeId, newSessionId)
        viewModel.playMedia()
        runCurrent()
        val replacementSource = requireNotNull(viewModel.state.value.viewer.sourcePicker).options.single()
        viewModel.selectPlaybackSource(replacementSource)
        viewModel.choosePlaybackTarget(PlaybackTargetSelection.Embedded(EmbeddedPlayerPreference.AUTOMATIC))
        runCurrent()
        assertEquals(newSessionId, viewModel.state.value.viewer.player?.sessionId)
        assertEquals(listOf(newMarker), viewModel.state.value.viewer.player?.markers)

        advanceTimeBy(1_000)
        runCurrent()
        assertEquals(newSessionId, viewModel.state.value.viewer.player?.sessionId)
        assertEquals(listOf(newMarker), viewModel.state.value.viewer.player?.markers)
    }

    @Test
    fun naturalEndHonorsAutoplayAndManualNextAlwaysAdvances() = runTest(dispatcher) {
        val profile = profile(hasPin = false)
        val seriesId = UUID.randomUUID()
        val firstEpisodeId = UUID.randomUUID()
        val secondEpisodeId = UUID.randomUUID()
        val addonId = UUID.randomUUID()
        val firstEpisode = episode(firstEpisodeId, seriesId, 1)
        val secondEpisode = episode(secondEpisodeId, seriesId, 2)
        val source = io.rivune.api.PlaybackSourceOption(
            id = "source",
            sourceRef = "fresh-per-request",
            addonId = addonId,
            manifestId = "addon",
            streamIndex = 0,
            name = "Direct",
            protocol = "http",
            expiresAt = "2099-01-01T00:00:00Z",
            stableIdentity = "stable-direct",
        )

        fun scenario(autoplay: Boolean): Pair<RivuneViewModel, FakeGateway> {
            val gateway = FakeGateway(
                restored = true,
                account = account(profile, active = true),
                collections = listOf(collection()),
            ).apply {
                seriesResult = io.rivune.app.series(seriesId)
                seasons = mapOf("season-1" to season(seriesId, listOf(firstEpisode, secondEpisode)))
                progress = io.rivune.api.PlaybackProgress(
                    firstEpisodeId,
                    io.rivune.api.PlaybackProgressMediaType.EPISODE,
                    0,
                    1_000,
                    false,
                    0,
                    "2026-08-12T00:00:00Z",
                    "2026-08-12T00:00:00Z",
                )
                sourceList = io.rivune.api.PlaybackSourceList(listOf(source), emptyList())
                preparation = io.rivune.api.PlaybackPreparation(
                    source.sourceRef,
                    io.rivune.api.PlaybackMode.DIRECT,
                    "http",
                    subtitleCount = 0,
                    expiresAt = "2099-01-01T00:00:00Z",
                )
                playbackSession = io.rivune.api.PlaybackSession(
                    UUID.randomUUID(),
                    "selected",
                    sources = listOf(
                        io.rivune.api.PlaybackSource(
                            "selected",
                            addonId,
                            "addon",
                            mode = io.rivune.api.PlaybackMode.DIRECT,
                            url = "https://media.example.com/episode.m3u8",
                            protocol = "hls",
                            compatible = true,
                            media = io.rivune.api.PlaybackMediaInspection(durationSeconds = 1_000.0),
                        ),
                    ),
                    subtitles = emptyList(),
                    providerErrors = emptyList(),
                    expiresAt = "2099-01-01T00:00:00Z",
                )
                effectiveSettingsResult = effectiveSettingsResult.copy(
                    settings = io.rivune.api.SettingsValues(autoplayNextEpisode = autoplay),
                )
            }
            return viewModel(FakeServerStore("https://saved.example.com"), gateway) to gateway
        }

        fun openFirstEpisode(viewModel: RivuneViewModel) {
            viewModel.openMedia(
                MediaTarget(
                    id = "tmdb:42:1:1",
                    resourceId = "tmdb:42:1:1",
                    mediaType = "episode",
                    title = "Episode 1",
                    titleId = firstEpisodeId,
                    seriesId = seriesId,
                    seasonId = "season-1",
                    seasonNumber = 1,
                    episodeNumber = 1,
                ),
            )
        }

        val (enabledViewModel, enabledGateway) = scenario(autoplay = true)
        advanceUntilIdle()
        openFirstEpisode(enabledViewModel)
        advanceUntilIdle()
        assertEquals(secondEpisodeId, enabledViewModel.state.value.viewer.sourcePicker?.nextEpisode?.titleId)
        enabledViewModel.selectPlaybackSource(source)
        enabledViewModel.choosePlaybackTarget(PlaybackTargetSelection.Embedded(EmbeddedPlayerPreference.AUTOMATIC))
        advanceUntilIdle()
        val enabledSession = requireNotNull(enabledViewModel.state.value.viewer.player).sessionId
        enabledViewModel.reportPlayerProgress(900, 1_000, completed = true)
        advanceUntilIdle()
        assertEquals(enabledSession, enabledViewModel.state.value.viewer.player?.sessionId)
        enabledViewModel.playerPlaybackEnded()
        enabledViewModel.playerPlaybackEnded()
        advanceUntilIdle()

        assertNotNull(enabledViewModel.state.value.viewer.player)
        assertEquals(secondEpisodeId, enabledViewModel.state.value.viewer.player?.titleId)
        assertEquals(1, enabledGateway.stopPlaybackCalls)
        assertEquals(listOf("tmdb:42:1:1", "tmdb:42:1:2"), enabledGateway.playbackSourceResources)
        assertTrue(enabledGateway.playbackEvents.indexOf("progress:900") < enabledGateway.playbackEvents.indexOf("stop"))
        assertTrue(enabledGateway.playbackEvents.indexOf("stop") < enabledGateway.playbackEvents.lastIndexOf("sources:tmdb:42:1:2"))

        val (disabledViewModel, disabledGateway) = scenario(autoplay = false)
        advanceUntilIdle()
        openFirstEpisode(disabledViewModel)
        advanceUntilIdle()
        disabledViewModel.selectPlaybackSource(source)
        disabledViewModel.choosePlaybackTarget(PlaybackTargetSelection.Embedded(EmbeddedPlayerPreference.AUTOMATIC))
        advanceUntilIdle()
        val disabledSession = requireNotNull(disabledViewModel.state.value.viewer.player).sessionId
        disabledViewModel.reportPlayerProgress(400, 1_000, completed = false)
        disabledViewModel.playerPlaybackEnded()
        advanceUntilIdle()

        assertEquals(disabledSession, disabledViewModel.state.value.viewer.player?.sessionId)
        assertEquals(0, disabledGateway.stopPlaybackCalls)

        disabledViewModel.playNextEpisode()
        disabledViewModel.playNextEpisode()
        advanceUntilIdle()

        assertNotNull(disabledViewModel.state.value.viewer.player)
        assertEquals(secondEpisodeId, disabledViewModel.state.value.viewer.player?.titleId)
        assertEquals(1, disabledGateway.stopPlaybackCalls)
    }

    @Test
    fun externalPlayerHandoffHandlesCompletionAndLifecycleCleanup() = runTest(dispatcher) {
        val viewerProfile = profile(hasPin = false)
        val titleId = UUID.fromString("88888888-8888-4888-8888-888888888888")
        val playbackId = UUID.fromString("99999999-9999-4999-8999-999999999999")
        val addonId = UUID.fromString("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
        val externalPlayer = ExternalPlayerApp(
            packageName = "org.example.player",
            label = "Example Player",
            videoMimeTypes = setOf("video/*"),
            supportsMagnet = false,
        )
        val gateway = FakeGateway(
            restored = true,
            account = account(viewerProfile, active = true),
            collections = listOf(collection()),
        )
        gateway.resolvedTitle = io.rivune.api.TitleReference(
            titleId,
            io.rivune.api.TitleMediaType.MOVIE,
            "imdb",
            "tt1234567",
            "tt1234567",
            "Film",
        )
        gateway.movieResult = io.rivune.api.Movie(
            titleId,
            io.rivune.api.MediaType.MOVIE,
            "Film",
            "Film",
            "en",
            "Overview",
            "2026-08-12",
            genres = emptyList(),
            cast = emptyList(),
            voteAverage = 8.0,
            voteCount = 10,
            externalIds = mapOf("imdb" to "tt1234567"),
        )
        gateway.progress = io.rivune.api.PlaybackProgress(
            titleId,
            io.rivune.api.PlaybackProgressMediaType.MOVIE,
            120,
            3_600,
            false,
            3,
            "2026-08-12T00:00:00Z",
            "2026-08-12T00:00:00Z",
        )
        val source = io.rivune.api.PlaybackSourceOption(
            "source",
            "ref",
            addonId,
            manifestId = "addon",
            streamIndex = 0,
            name = "Direct",
            protocol = "http",
            mode = io.rivune.api.PlaybackMode.DIRECT,
            expiresAt = "2099-01-01T00:00:00Z",
            stableIdentity = "stable-direct",
        )
        gateway.sourceList = io.rivune.api.PlaybackSourceList(listOf(source), emptyList())
        gateway.preparation = io.rivune.api.PlaybackPreparation(
            "ref",
            io.rivune.api.PlaybackMode.DIRECT,
            "http",
            subtitleCount = 0,
            expiresAt = "2099-01-01T00:00:00Z",
        )
        gateway.playbackSession = io.rivune.api.PlaybackSession(
            playbackId,
            "source",
            sources = listOf(
                io.rivune.api.PlaybackSource(
                    "source",
                    addonId,
                    "addon",
                    mode = io.rivune.api.PlaybackMode.DIRECT,
                    url = "https://media.example.com/stream.m3u8",
                    protocol = "hls",
                    container = "ts",
                    compatible = true,
                    media = io.rivune.api.PlaybackMediaInspection(durationSeconds = 3_600.0),
                ),
            ),
            subtitles = emptyList(),
            providerErrors = emptyList(),
            expiresAt = "2099-01-01T00:00:00Z",
        )
        val support = ExternalPlaybackSupport(
            listOf(
                externalPlayer,
                ExternalPlayerApp("org.example.torrent", "Torrent", emptySet(), supportsMagnet = true),
            ),
        )
        val viewModel = viewModel(
            FakeServerStore("https://saved.example.com"),
            gateway,
            externalPlaybackSupport = support,
            playbackNetwork = NetworkClass.MOBILE,
        )
        advanceUntilIdle()

        viewModel.openMedia(MediaTarget("tt1234567", "movie", "Film"))
        advanceUntilIdle()
        viewModel.playMedia()
        advanceUntilIdle()

        assertEquals(listOf(EXTERNAL_VIDEO_CAPABILITY, EXTERNAL_MAGNET_CAPABILITY), gateway.lastPlaybackCapabilities?.externalPlayers)
        assertEquals(720, gateway.lastPlaybackCapabilities?.maximumHeight)
        assertEquals(5_000, gateway.lastPlaybackCapabilities?.maximumVideoBitrateKbps)
        assertEquals(listOf("org.example.player", "org.example.torrent"), viewModel.state.value.externalPlayers.map { it.packageName })
        assertNotNull(viewModel.state.value.viewer.sourcePicker)
        assertNull(viewModel.state.value.viewer.player)

        viewModel.selectPlaybackSource(source)
        assertEquals(source, viewModel.state.value.viewer.sourcePicker?.playerSource)
        viewModel.choosePlaybackTarget(PlaybackTargetSelection.External(externalPlayer))
        advanceUntilIdle()

        assertTrue(gateway.preparedForExternalPlayer)
        assertTrue(gateway.resolvedForExternalPlayer)
        assertEquals(externalPlayer, viewModel.state.value.viewer.player?.externalPlayer)
        assertEquals("ts", viewModel.state.value.viewer.player?.container)
        assertEquals(3_600, viewModel.state.value.viewer.player?.durationSeconds)

        viewModel.externalPlaybackFinished(ExternalPlaybackResult(null, null, completed = true))
        advanceUntilIdle()

        assertNull(viewModel.state.value.viewer.player)
        assertEquals(3_600, gateway.progressUpdates.single().positionSeconds)
        assertEquals(3_600, gateway.progressUpdates.single().durationSeconds)
        assertTrue(gateway.progressUpdates.single().completed)
        assertEquals(playbackId, gateway.stoppedPlayback)
        assertEquals(3_600, viewModel.state.value.viewer.detail?.progress?.positionSeconds)
        assertTrue(viewModel.state.value.viewer.detail?.progress?.completed == true)

        val firstPlayback = requireNotNull(gateway.playbackSession)
        val unknownDurationPlaybackId = UUID.fromString("77777777-7777-4777-8777-777777777777")
        gateway.progress = null
        gateway.playbackSession = firstPlayback.copy(
            id = unknownDurationPlaybackId,
            sources = firstPlayback.sources.map { it.copy(media = null) },
        )
        viewModel.openMedia(MediaTarget("tt1234567", "movie", "Film"))
        advanceUntilIdle()
        viewModel.playMedia()
        advanceUntilIdle()
        viewModel.selectPlaybackSource(source)
        viewModel.choosePlaybackTarget(PlaybackTargetSelection.External(externalPlayer))
        advanceUntilIdle()
        assertEquals(0, viewModel.state.value.viewer.player?.durationSeconds)

        viewModel.externalPlaybackFinished(ExternalPlaybackResult(null, null, completed = true))
        advanceUntilIdle()

        assertEquals(listOf(titleId to 0L), gateway.watchedRequests)
        assertEquals(unknownDurationPlaybackId, gateway.stoppedPlayback)
        assertTrue(viewModel.state.value.viewer.detail?.progress?.completed == true)

        val lifecyclePlaybackId = UUID.fromString("66666666-6666-4666-8666-666666666666")
        gateway.playbackSession = firstPlayback.copy(id = lifecyclePlaybackId)
        viewModel.openMedia(MediaTarget("tt1234567", "movie", "Film"))
        advanceUntilIdle()
        viewModel.playMedia()
        advanceUntilIdle()
        viewModel.selectPlaybackSource(source)
        viewModel.choosePlaybackTarget(PlaybackTargetSelection.External(externalPlayer))
        advanceUntilIdle()

        viewModel.externalPlaybackFinished(null)
        viewModel.viewModelScope.cancel()
        advanceUntilIdle()

        assertEquals(lifecyclePlaybackId, gateway.stoppedPlayback)
        assertEquals(3, gateway.stopPlaybackCalls)
    }

    @Test
    fun askPreferenceOnlyOpensTargetDialogWithoutStartingExternalPlayback() = runTest(dispatcher) {
        val titleId = UUID.randomUUID()
        val externalPlayer = ExternalPlayerApp(
            packageName = "org.videolan.vlc",
            label = "VLC",
            videoMimeTypes = setOf("video/*"),
            supportsMagnet = false,
        )
        val gateway = FakeGateway(
            restored = true,
            account = account(profile(hasPin = false), active = true),
            collections = listOf(collection()),
        )
        val source = gateway.configurePlayback(titleId)
        val viewModel = viewModel(
            FakeServerStore("https://saved.example.com"),
            gateway,
            externalPlaybackSupport = ExternalPlaybackSupport(listOf(externalPlayer)),
            appPreferences = AppPreferencesReader {
                AppPreferencesState(
                    preferredPlayer = PreferredPlayer.Ask,
                    embeddedPlayerPreference = EmbeddedPlayerPreference.AUTOMATIC,
                )
            },
        )
        advanceUntilIdle()
        viewModel.openMedia(MediaTarget("episode", "episode", "Episode", titleId = titleId))
        advanceUntilIdle()

        viewModel.selectPlaybackSource(source)
        advanceUntilIdle()

        assertEquals(source, viewModel.state.value.viewer.sourcePicker?.playerSource)
        assertNull(viewModel.state.value.viewer.player)
        assertFalse(gateway.preparedForExternalPlayer)
        assertFalse(gateway.resolvedForExternalPlayer)
    }

    @Test
    fun askMpvSelectionPreservesOriginalSourceAndConsumesChoiceOnce() = runTest(dispatcher) {
        val titleId = UUID.randomUUID()
        val gateway = FakeGateway(
            restored = true,
            account = account(profile(hasPin = false), active = true),
            collections = listOf(collection()),
        )
        val source = gateway.configurePlayback(titleId)
        val viewModel = viewModel(
            FakeServerStore("https://saved.example.com"),
            gateway,
            appPreferences = AppPreferencesReader {
                AppPreferencesState(
                    preferredPlayer = PreferredPlayer.Ask,
                    embeddedPlayerPreference = EmbeddedPlayerPreference.AUTOMATIC,
                )
            },
        )
        advanceUntilIdle()
        viewModel.openMedia(MediaTarget("episode", "episode", "Episode", titleId = titleId))
        advanceUntilIdle()
        viewModel.selectPlaybackSource(source)

        val mpvTarget = PlaybackTargetSelection.Embedded(EmbeddedPlayerPreference.MPV)
        viewModel.choosePlaybackTarget(mpvTarget)
        assertNull(viewModel.state.value.viewer.sourcePicker?.playerSource)
        assertEquals(ViewerLoading.PLAYER, viewModel.state.value.viewer.loading)

        viewModel.choosePlaybackTarget(mpvTarget)
        advanceUntilIdle()

        assertTrue(gateway.preparedForExternalPlayer)
        assertTrue(gateway.resolvedForExternalPlayer)
        assertEquals(1, gateway.preparePlaybackCalls)
        assertEquals(1, gateway.resolvePlaybackCalls)
        assertEquals(EmbeddedPlayerEngine.MPV, viewModel.state.value.viewer.player?.engine)
    }

    @Test
    fun automaticFallbackIsOneShotAndTerminalFailureRemainsUntilClose() = runTest(dispatcher) {
        val titleId = UUID.randomUUID()
        val sessionId = UUID.randomUUID()
        val gateway = FakeGateway(
            restored = true,
            account = account(profile(hasPin = false), active = true),
            collections = listOf(collection()),
        )
        val source = gateway.configurePlayback(titleId, sessionId)
        val viewModel = viewModel(
            FakeServerStore("https://saved.example.com"),
            gateway,
            appPreferences = AppPreferencesReader {
                AppPreferencesState(
                    preferredPlayer = PreferredPlayer.Rivune,
                    embeddedPlayerPreference = EmbeddedPlayerPreference.AUTOMATIC,
                )
            },
        )
        advanceUntilIdle()
        viewModel.openMedia(MediaTarget("episode", "episode", "Episode", titleId = titleId))
        advanceUntilIdle()
        viewModel.selectPlaybackSource(source)
        advanceUntilIdle()

        val media3 = requireNotNull(viewModel.state.value.viewer.player)
        assertEquals(EmbeddedPlayerEngine.MEDIA3, media3.engine)
        assertTrue(media3.fallbackAllowed)
        viewModel.playerFailed(media3.key, media3.sessionId, PlayerEngineFailure(45_250L, fallbackEligible = true))

        val mpv = requireNotNull(viewModel.state.value.viewer.player)
        assertEquals(sessionId, mpv.sessionId)
        assertTrue(mpv.key != media3.key)
        assertEquals(EmbeddedPlayerEngine.MPV, mpv.engine)
        assertFalse(mpv.fallbackAllowed)
        assertEquals(45_250L, mpv.startPositionMs)
        assertEquals(0, gateway.stopPlaybackCalls)

        viewModel.playerFailed(
            media3.key,
            media3.sessionId,
            PlayerEngineFailure(46_000L, fallbackEligible = true),
        )
        assertNull(viewModel.state.value.viewer.playerFailure)

        val terminalFailure = PlayerEngineFailure(46_000L, fallbackEligible = true)
        viewModel.playerFailed(mpv.key, mpv.sessionId, terminalFailure)
        viewModel.playerFailed(mpv.key, mpv.sessionId, terminalFailure)
        assertEquals(mpv, viewModel.state.value.viewer.player)
        assertEquals(terminalFailure, viewModel.state.value.viewer.playerFailure?.failure)
        assertEquals(0, gateway.stopPlaybackCalls)

        viewModel.closePlayer()
        advanceUntilIdle()
        assertNull(viewModel.state.value.viewer.player)
        assertNull(viewModel.state.value.viewer.playerFailure)
        assertEquals(sessionId, gateway.stoppedPlayback)
        assertEquals(1, gateway.stopPlaybackCalls)
    }

    @Test
    fun failedPlayerRetryAndRestartCreateFreshSessionsAtExplicitPositions() = runTest(dispatcher) {
        val titleId = UUID.randomUUID()
        val initialSessionId = UUID.randomUUID()
        val retrySessionId = UUID.randomUUID()
        val restartSessionId = UUID.randomUUID()
        val gateway = FakeGateway(
            restored = true,
            account = account(profile(hasPin = false), active = true),
            collections = listOf(collection()),
        )
        val source = gateway.configurePlayback(titleId, initialSessionId)
        val viewModel = viewModel(
            FakeServerStore("https://saved.example.com"),
            gateway,
            appPreferences = AppPreferencesReader {
                AppPreferencesState(
                    preferredPlayer = PreferredPlayer.Rivune,
                    embeddedPlayerPreference = EmbeddedPlayerPreference.MPV,
                )
            },
        )
        advanceUntilIdle()
        viewModel.openMedia(MediaTarget("episode", "episode", "Episode", titleId = titleId))
        advanceUntilIdle()
        viewModel.selectPlaybackSource(source)
        advanceUntilIdle()

        val initialPlayer = requireNotNull(viewModel.state.value.viewer.player)
        assertEquals(initialSessionId, initialPlayer.sessionId)
        val failure = PlayerEngineFailure(
            positionMs = 46_999L,
            fallbackEligible = false,
            reason = PlayerEngineFailureReason.STARTUP_TIMEOUT,
        )
        viewModel.playerFailed(initialPlayer.key, initialPlayer.sessionId, failure)
        assertEquals(failure, viewModel.state.value.viewer.playerFailure?.failure)
        assertEquals(initialSessionId, viewModel.state.value.viewer.player?.sessionId)

        val refreshedSource = source.copy(id = "source-rotated", sourceRef = "fresh-retry-source-ref", streamIndex = 7)
        gateway.sourceList = io.rivune.api.PlaybackSourceList(listOf(refreshedSource), emptyList())
        gateway.preparation = requireNotNull(gateway.preparation).copy(sourceRef = refreshedSource.sourceRef)
        gateway.playbackSession = requireNotNull(gateway.playbackSession).copy(id = retrySessionId)
        viewModel.retryFailedPlayer()
        viewModel.retryFailedPlayer()
        advanceUntilIdle()

        assertEquals(retrySessionId, viewModel.state.value.viewer.player?.sessionId)
        assertEquals(EmbeddedPlayerEngine.MPV, viewModel.state.value.viewer.player?.engine)
        assertNull(viewModel.state.value.viewer.playerFailure)
        assertEquals(listOf<Int?>(0, 46), gateway.preparedStartSeconds)
        assertEquals(listOf<Int?>(0, 46), gateway.resolvedStartSeconds)
        assertEquals(1, gateway.stopPlaybackCalls)
        assertEquals(2, gateway.playbackSourceResources.size)
        assertEquals(listOf(source.sourceRef, refreshedSource.sourceRef), gateway.preparedSourceRefs)
        assertEquals(listOf(source.sourceRef, refreshedSource.sourceRef), gateway.resolvedSourceRefs)

        val retryPlayer = requireNotNull(viewModel.state.value.viewer.player)
        viewModel.playerFailed(retryPlayer.key, retryPlayer.sessionId, failure.copy(positionMs = 78_000L))
        assertNotNull(viewModel.state.value.viewer.playerFailure)
        gateway.playbackSession = requireNotNull(gateway.playbackSession).copy(id = restartSessionId)
        viewModel.restartFailedPlayer()
        viewModel.restartFailedPlayer()
        advanceUntilIdle()

        assertEquals(restartSessionId, viewModel.state.value.viewer.player?.sessionId)
        assertEquals(0L, viewModel.state.value.viewer.player?.startPositionMs)
        assertNull(viewModel.state.value.viewer.playerFailure)
        assertEquals(listOf<Int?>(0, 46, 0), gateway.preparedStartSeconds)
        assertEquals(listOf<Int?>(0, 46, 0), gateway.resolvedStartSeconds)
        assertEquals(2, gateway.stopPlaybackCalls)
    }

    @Test
    fun failedPlayerRetryStopsOnceAndKeepsPickerWhenRefreshCannotResume() = runTest(dispatcher) {
        for (refresh in listOf("failure", "source disappeared")) {
            val titleId = UUID.randomUUID()
            val sessionId = UUID.randomUUID()
            val gateway = FakeGateway(
                restored = true,
                account = account(profile(hasPin = false), active = true),
                collections = listOf(collection()),
            )
            val source = gateway.configurePlayback(titleId, sessionId)
            val viewModel = viewModel(
                FakeServerStore("https://saved.example.com"),
                gateway,
                appPreferences = AppPreferencesReader {
                    AppPreferencesState(
                        preferredPlayer = PreferredPlayer.Rivune,
                        embeddedPlayerPreference = EmbeddedPlayerPreference.MPV,
                    )
                },
            )
            advanceUntilIdle()
            viewModel.openMedia(MediaTarget("episode", "episode", "Episode", titleId = titleId))
            advanceUntilIdle()
            viewModel.selectPlaybackSource(source)
            advanceUntilIdle()
            val player = requireNotNull(viewModel.state.value.viewer.player)
            viewModel.playerFailed(player.key, player.sessionId, PlayerEngineFailure(10_000L, fallbackEligible = false))
            gateway.sourceList = if (refresh == "failure") {
                null
            } else {
                io.rivune.api.PlaybackSourceList(
                    listOf(source.copy(sourceRef = "different-source-ref", stableIdentity = "different-source")),
                    emptyList(),
                )
            }

            viewModel.retryFailedPlayer()
            viewModel.retryFailedPlayer()
            advanceUntilIdle()

            assertEquals(1, gateway.stopPlaybackCalls, refresh)
            assertEquals(sessionId, gateway.stoppedPlayback, refresh)
            assertNull(viewModel.state.value.viewer.player, refresh)
            assertNull(viewModel.state.value.viewer.playerFailure, refresh)
            assertNotNull(viewModel.state.value.viewer.sourcePicker, refresh)
            assertEquals(UiFailure.PLAYBACK, viewModel.state.value.viewer.inlineFailure, refresh)
            assertEquals(1, gateway.preparePlaybackCalls, refresh)
        }
    }

    @Test
    fun failedPlayerRetryDoesNotResumeIdentityThatWasOriginallyAmbiguous() = runTest(dispatcher) {
        val titleId = UUID.randomUUID()
        val sessionId = UUID.randomUUID()
        val gateway = FakeGateway(
            restored = true,
            account = account(profile(hasPin = false), active = true),
            collections = listOf(collection()),
        )
        val selected = gateway.configurePlayback(titleId, sessionId)
        val duplicate = selected.copy(id = "duplicate", sourceRef = "duplicate-source-ref", streamIndex = 1)
        gateway.sourceList = io.rivune.api.PlaybackSourceList(listOf(selected, duplicate), emptyList())
        val viewModel = viewModel(
            FakeServerStore("https://saved.example.com"),
            gateway,
            appPreferences = AppPreferencesReader {
                AppPreferencesState(
                    preferredPlayer = PreferredPlayer.Rivune,
                    embeddedPlayerPreference = EmbeddedPlayerPreference.MPV,
                )
            },
        )
        advanceUntilIdle()
        viewModel.openMedia(MediaTarget("episode", "episode", "Episode", titleId = titleId))
        advanceUntilIdle()
        viewModel.selectPlaybackSource(selected)
        advanceUntilIdle()
        val player = requireNotNull(viewModel.state.value.viewer.player)
        viewModel.playerFailed(player.key, player.sessionId, PlayerEngineFailure(10_000L, fallbackEligible = false))
        gateway.sourceList = io.rivune.api.PlaybackSourceList(
            listOf(duplicate.copy(sourceRef = "refreshed-duplicate-source-ref")),
            emptyList(),
        )

        viewModel.retryFailedPlayer()
        advanceUntilIdle()

        assertEquals(1, gateway.stopPlaybackCalls)
        assertNull(viewModel.state.value.viewer.player)
        assertNull(viewModel.state.value.viewer.playerFailure)
        assertNotNull(viewModel.state.value.viewer.sourcePicker)
        assertEquals(UiFailure.PLAYBACK, viewModel.state.value.viewer.inlineFailure)
        assertEquals(1, gateway.preparePlaybackCalls)
    }

    @Test
    fun choosingAnotherSourceStopsOnceAndPreservesPickerOptions() = runTest(dispatcher) {
        val titleId = UUID.randomUUID()
        val sessionId = UUID.randomUUID()
        val gateway = FakeGateway(
            restored = true,
            account = account(profile(hasPin = false), active = true),
            collections = listOf(collection()),
        )
        val source = gateway.configurePlayback(titleId, sessionId)
        val viewModel = viewModel(
            FakeServerStore("https://saved.example.com"),
            gateway,
            appPreferences = AppPreferencesReader {
                AppPreferencesState(
                    preferredPlayer = PreferredPlayer.Rivune,
                    embeddedPlayerPreference = EmbeddedPlayerPreference.MPV,
                )
            },
        )
        advanceUntilIdle()
        viewModel.openMedia(MediaTarget("episode", "episode", "Episode", titleId = titleId))
        advanceUntilIdle()
        viewModel.selectPlaybackSource(source)
        advanceUntilIdle()
        val options = requireNotNull(viewModel.state.value.viewer.sourcePicker).options
        val failedPlayer = requireNotNull(viewModel.state.value.viewer.player)
        viewModel.playerFailed(
            failedPlayer.key,
            failedPlayer.sessionId,
            PlayerEngineFailure(20_000L, fallbackEligible = false),
        )
        assertNotNull(viewModel.state.value.viewer.playerFailure)

        viewModel.chooseAnotherPlaybackSource()
        viewModel.chooseAnotherPlaybackSource()
        advanceUntilIdle()

        assertNull(viewModel.state.value.viewer.player)
        assertNull(viewModel.state.value.viewer.playerFailure)
        assertEquals(options, viewModel.state.value.viewer.sourcePicker?.options)
        assertNull(viewModel.state.value.viewer.sourcePicker?.playerSource)
        assertEquals(UiFailure.PLAYBACK, viewModel.state.value.viewer.inlineFailure)
        assertEquals(sessionId, gateway.stoppedPlayback)
        assertEquals(1, gateway.stopPlaybackCalls)
    }

    @Test
    fun explicitMpvStartsMpvWithoutMedia3Fallback() = runTest(dispatcher) {
        val titleId = UUID.randomUUID()
        val gateway = FakeGateway(
            restored = true,
            account = account(profile(hasPin = false), active = true),
            collections = listOf(collection()),
        )
        val source = gateway.configurePlayback(titleId)
        val viewModel = viewModel(
            FakeServerStore("https://saved.example.com"),
            gateway,
            appPreferences = AppPreferencesReader {
                AppPreferencesState(
                    preferredPlayer = PreferredPlayer.Rivune,
                    embeddedPlayerPreference = EmbeddedPlayerPreference.MPV,
                )
            },
        )
        advanceUntilIdle()
        viewModel.openMedia(MediaTarget("episode", "episode", "Episode", titleId = titleId))
        advanceUntilIdle()
        viewModel.selectPlaybackSource(source)
        advanceUntilIdle()

        val player = requireNotNull(viewModel.state.value.viewer.player)
        assertEquals(EmbeddedPlayerEngine.MPV, player.engine)
        assertFalse(player.fallbackAllowed)
        assertEquals(0, gateway.stopPlaybackCalls)
    }

    @Test
    fun invalidResolvedPlaybackSessionIsStoppedAndKeepsSourcePicker() = runTest(dispatcher) {
        val viewerProfile = profile(hasPin = false)
        val targetId = UUID.fromString("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
        val playbackId = UUID.fromString("bbbbbbbb-cccc-4ddd-8eee-ffffffffffff")
        val addonId = UUID.fromString("cccccccc-dddd-4eee-8fff-aaaaaaaaaaaa")
        val gateway = FakeGateway(
            restored = true,
            account = account(viewerProfile, active = true),
            collections = listOf(collection()),
        )
        gateway.resolvedTitle = io.rivune.api.TitleReference(targetId, io.rivune.api.TitleMediaType.MOVIE, "imdb", "tt7654321", "tt7654321", "Broken")
        gateway.movieResult = io.rivune.api.Movie(targetId, io.rivune.api.MediaType.MOVIE, "Broken", "Broken", "en", "", "2026-08-12", genres = emptyList(), cast = emptyList(), voteAverage = 0.0, voteCount = 0, externalIds = mapOf("imdb" to "tt7654321"))
        gateway.sourceList = io.rivune.api.PlaybackSourceList(
            listOf(io.rivune.api.PlaybackSourceOption("source", "ref", addonId, manifestId = "addon", streamIndex = 0, name = "Broken", protocol = "http", expiresAt = "2099-01-01T00:00:00Z", stableIdentity = "stable-broken")),
            emptyList(),
        )
        gateway.preparation = io.rivune.api.PlaybackPreparation("ref", io.rivune.api.PlaybackMode.DIRECT, "http", subtitleCount = 0, expiresAt = "2099-01-01T00:00:00Z")
        gateway.playbackSession = io.rivune.api.PlaybackSession(
            playbackId,
            "source",
            sources = listOf(io.rivune.api.PlaybackSource("source", addonId, "addon", mode = io.rivune.api.PlaybackMode.DIRECT, url = null, protocol = "http", compatible = true)),
            subtitles = emptyList(),
            providerErrors = emptyList(),
            expiresAt = "2099-01-01T00:00:00Z",
        )
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()

        viewModel.openMedia(MediaTarget("tt7654321", "movie", "Broken"))
        advanceUntilIdle()
        viewModel.playMedia()
        advanceUntilIdle()
        viewModel.selectPlaybackSource(requireNotNull(viewModel.state.value.viewer.sourcePicker).options.single())
        viewModel.choosePlaybackTarget(PlaybackTargetSelection.Embedded(EmbeddedPlayerPreference.AUTOMATIC))
        advanceUntilIdle()

        assertEquals(playbackId, gateway.stoppedPlayback)
        assertNotNull(viewModel.state.value.viewer.sourcePicker)
        assertEquals(UiFailure.PLAYBACK, viewModel.state.value.viewer.inlineFailure)
        assertNull(viewModel.state.value.viewer.player)
    }

    @Test
    fun libraryAndCalendarTabsLoadBackendContent() = runTest(dispatcher) {
        val viewerProfile = profile(hasPin = false)
        val titleId = UUID.fromString("88888888-8888-4888-8888-888888888888")
        val gateway = FakeGateway(
            restored = true,
            account = account(viewerProfile, active = true),
            collections = listOf(collection()),
        )
        gateway.libraryPages = mapOf(1 to io.rivune.api.LibraryPage(listOf(io.rivune.api.LibraryItem(titleId, io.rivune.api.TitleMediaType.MOVIE, title = "Film", available = true, addedAt = "2026-08-12T00:00:00Z", updatedAt = "2026-08-12T00:00:00Z")), 1, 1, 1))
        gateway.calendarEvents = listOf(io.rivune.api.CalendarEvent("event", titleId, io.rivune.api.CalendarEventMediaType.MOVIE, "Film", "2026-08-12"))
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()

        viewModel.selectViewerTab(ViewerTab.LIBRARY)
        advanceUntilIdle()
        assertEquals(listOf("Film"), viewModel.state.value.viewer.library.items.map { it.title })

        viewModel.selectViewerTab(ViewerTab.CALENDAR)
        advanceUntilIdle()
        assertEquals(listOf("event"), viewModel.state.value.calendarEvents.map { it.id })
    }

    @Test
    fun searchMapsOpaqueAddonResultsIntoActionableMedia() = runTest(dispatcher) {
        val viewerProfile = profile(hasPin = false)
        val addonId = UUID.fromString("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
        val gateway = FakeGateway(
            restored = true,
            account = account(viewerProfile, active = true),
            collections = listOf(collection()),
        )
        gateway.catalogDescriptors = listOf(
            io.rivune.api.AddonCatalogDescriptor(
                addonId = addonId,
                addonName = "Catalog",
                manifestId = "org.example",
                position = 0,
                catalog = io.rivune.api.StremioManifestCatalog(type = "movie", id = "search"),
                addonCatalog = false,
                searchable = true,
            ),
        )
        gateway.searchPages = mapOf(
            0 to io.rivune.api.AddonResourceBatch(
                results = listOf(
                    io.rivune.api.AddonResourceResult(
                        addonId = addonId,
                        manifestId = "org.example",
                        resource = "catalog",
                        type = "movie",
                        id = "search",
                        payload = kotlinx.serialization.json.Json.parseToJsonElement("""{"metas":[{"id":"tt1234567","name":"Film","poster":"/art.jpg"}]}""") as kotlinx.serialization.json.JsonObject,
                        cache = io.rivune.api.AddonCachePolicy(),
                    ),
                ),
                errors = emptyList(),
            ),
        )
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()

        viewModel.selectViewerTab(ViewerTab.SEARCH)
        advanceUntilIdle()
        viewModel.search("Film")
        advanceUntilIdle()

        val result = viewModel.state.value.viewer.search.items.single()
        assertEquals("tt1234567", result.resourceId)
        assertEquals("Film", result.title)
        assertEquals(addonId, result.sourceAddonId)
        assertTrue(gateway.semanticRequests.isEmpty())
    }

    @Test
    fun semanticSearchUsesResidualQueryInferredTypesAndKeepsFirstRepresentative() = runTest(dispatcher) {
        val gateway = semanticSearchGateway()
        gateway.semanticPages = mapOf(
            1 to semanticPage(
                items = listOf(
                    mediaItem("tmdb:1", "Semantic duplicate").copy(externalIds = mapOf("imdb" to "tt1234567", "tmdb" to "1")),
                    mediaItem("tmdb:2", "Semantic unique").copy(externalIds = mapOf("tmdb" to "2")),
                ),
                mediaTypes = listOf("movie"),
                hasMore = true,
            ),
        )
        gateway.searchPages = mapOf(0 to addonSearchBatch("movie", "tt1234567", "Direct title"))
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()

        viewModel.selectViewerTab(ViewerTab.SEARCH)
        advanceUntilIdle()
        viewModel.search("film Dune de guerre")
        advanceUntilIdle()

        val search = viewModel.state.value.viewer.search
        assertEquals(listOf("Semantic duplicate", "Semantic unique"), search.items.map(MediaTarget::title))
        assertEquals("tmdb", search.items.first().provider)
        assertEquals("1", search.items.first().externalId)
        assertEquals(listOf("genre:war"), search.intents.map { it.id })
        assertEquals(Triple("movie", "Dune", 0), gateway.addonSearchRequests.last())
        assertTrue(gateway.addonSearchRequests.dropLast(1).all { it.second == "film Dune de guerre" })
        assertEquals(1, gateway.semanticRequests.single().page)
        assertEquals(24, gateway.semanticRequests.single().limit)
        assertTrue(search.hasMore)
        assertFalse(search.partial)
    }

    @Test
    fun searchPublishesFirstTypeImmediatelyCoalescesLaterTypesAndKeepsLoading() = runTest(dispatcher) {
        val gateway = semanticSearchGateway(types = listOf("movie", "series", "tv"))
        gateway.semanticDelayMillis = 100
        gateway.semanticPages = mapOf(
            1 to semanticPage(emptyList(), mediaTypes = listOf("movie", "series", "tv")),
        )
        gateway.searchDelaysByType = mapOf("series" to 10, "tv" to 15)
        gateway.searchPagesByType = mapOf(
            "movie" to addonSearchBatch("movie", "movie-1", "Movie"),
            "series" to addonSearchBatch("series", "series-1", "Series"),
            "tv" to addonSearchBatch("tv", "tv-1", "Channel"),
        )
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()
        viewModel.selectViewerTab(ViewerTab.SEARCH)
        advanceUntilIdle()

        viewModel.search("Dune")
        runCurrent()

        assertEquals(listOf("Movie"), viewModel.state.value.viewer.search.items.map(MediaTarget::title))
        assertEquals(ViewerLoading.SEARCH, viewModel.state.value.viewer.loading)

        advanceTimeBy(15)
        runCurrent()
        assertEquals(listOf("Movie"), viewModel.state.value.viewer.search.items.map(MediaTarget::title))
        assertEquals(ViewerLoading.SEARCH, viewModel.state.value.viewer.loading)

        advanceTimeBy(27)
        runCurrent()
        assertEquals(
            listOf("Movie", "Series", "Channel"),
            viewModel.state.value.viewer.search.items.map(MediaTarget::title),
        )
        assertEquals(ViewerLoading.SEARCH, viewModel.state.value.viewer.loading)

        advanceUntilIdle()
        assertEquals(
            listOf("Movie", "Series", "Channel"),
            viewModel.state.value.viewer.search.items.map(MediaTarget::title),
        )
        assertNull(viewModel.state.value.viewer.loading)
    }

    @Test
    fun semanticFirstDuplicateKeepsPublishedRepresentativeOrderAndKey() = runTest(dispatcher) {
        val gateway = semanticSearchGateway(types = listOf("movie"))
        val semanticDuplicate = mediaItem("tmdb:42", "Semantic first").copy(
            externalIds = mapOf("imdb" to "tt7654321", "tmdb" to "42"),
        )
        gateway.semanticPages = mapOf(
            1 to semanticPage(
                listOf(semanticDuplicate, mediaItem("tmdb:7", "Semantic second")),
            ),
        )
        gateway.searchDelayMillis = 100
        gateway.searchPages = mapOf(0 to addonSearchBatch("movie", "tt7654321", "Direct duplicate"))
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()
        viewModel.selectViewerTab(ViewerTab.SEARCH)
        advanceUntilIdle()

        viewModel.search("Dune")
        runCurrent()

        val published = viewModel.state.value.viewer.search.items
        assertEquals(listOf("Semantic first", "Semantic second"), published.map(MediaTarget::title))
        val publishedFirst = published.first()
        val publishedKey = searchMediaTargetKey(publishedFirst)
        assertEquals(
            publishedKey,
            searchMediaTargetKey(MediaTarget(id = "tt7654321", mediaType = "movie", title = "Direct duplicate")),
        )
        assertEquals(ViewerLoading.SEARCH, viewModel.state.value.viewer.loading)

        advanceUntilIdle()

        val completed = viewModel.state.value.viewer.search.items
        assertEquals(listOf("Semantic first", "Semantic second"), completed.map(MediaTarget::title))
        assertEquals(publishedFirst, completed.first())
        assertEquals(publishedKey, searchMediaTargetKey(completed.first()))
        assertNull(viewModel.state.value.viewer.loading)
    }
    @Test
    fun opaqueResultsWithSameIdFromDifferentSourcesHaveDistinctPresentationKeys() {
        val first = MediaTarget(
            id = "shared-id",
            mediaType = "movie",
            title = "First source",
            sourceAddonId = UUID.fromString("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"),
            sourceCatalogId = "search",
        )
        val second = first.copy(
            title = "Second source",
            sourceAddonId = UUID.fromString("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"),
        )

        assertFalse(searchMediaTargetKey(first) == searchMediaTargetKey(second))
    }


    @Test
    fun coordinationCadenceUsesActiveAndIdleIntervals() {
        val viewModel = viewModel(FakeServerStore(), FakeGateway())

        assertEquals(2_000L, viewModel.coordinationDelayMilliseconds(active = true))
        assertEquals(15_000L, viewModel.coordinationDelayMilliseconds(active = false))
    }

    @Test
    fun coordinationSuspendsInBackgroundAndRestartsPresenceOnForeground() = runTest(dispatcher) {
        val gateway = FakeGateway(
            discovery = discovery(),
            restored = true,
            account = account(profile(hasPin = false), active = true),
            collections = listOf(collection()),
        )
        val delays = mutableListOf<Long>()
        val viewModel = viewModel(
            FakeServerStore("https://saved.example.com"),
            gateway,
            awaitCoordinationTick = { delayMillis ->
                delays += delayMillis
                kotlinx.coroutines.awaitCancellation()
            },
        )
        runCurrent()

        assertEquals(1, gateway.playbackHeartbeatRequests)
        assertEquals(listOf(15_000L), delays)
        viewModel.coordinationForegroundChanged(false)
        runCurrent()
        assertEquals(1, gateway.playbackHeartbeatRequests)

        viewModel.coordinationForegroundChanged(true)
        runCurrent()
        assertEquals(2, gateway.playbackHeartbeatRequests)
        assertEquals(listOf(15_000L, 15_000L), delays)
    }

    @Test
    fun searchTypeBoundingIsStableDeduplicatedAndReportsTruncation() {
        val (types, truncated) = boundedStableSearchTypes(
            listOf(" Movie ", "series", "movie") + (2..20).map { "type$it" },
        )

        assertEquals(listOf("movie", "series") + (2..15).map { "type$it" }, types)
        assertTrue(truncated)
    }

    @Test
    fun searchFanoutUsesAtMostSixteenRequestsAndFourConcurrentCalls() = runTest(dispatcher) {
        val gateway = semanticSearchGateway(types = (0 until 20).map { "type$it" })
        gateway.semanticFailure = RivuneApiException.Server(404, "not_found", "Unsupported")
        gateway.searchDelayMillis = 1_000
        gateway.searchPages = mapOf(0 to addonSearchBatch("movie", "result", "Result"))
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()
        viewModel.selectViewerTab(ViewerTab.SEARCH)
        advanceUntilIdle()

        viewModel.search("bounded query")
        advanceUntilIdle()

        assertEquals(16, gateway.addonSearchRequests.size)
        assertEquals(4, gateway.maxConcurrentAddonSearches)
        assertTrue(viewModel.state.value.viewer.search.partial)
    }

    @Test
    fun semanticSearchDeduplicatesProviderIdentitiesWithoutReplacingFirstResult() = runTest(dispatcher) {
        val gateway = semanticSearchGateway(types = listOf("movie"))
        gateway.semanticPages = mapOf(
            1 to semanticPage(
                listOf(mediaItem("tmdb:42", "Semantic duplicate").copy(externalIds = mapOf("imdb" to "tt7654321", "tmdb" to "42"))),
            ),
        )
        gateway.searchPages = mapOf(0 to addonSearchBatch("movie", "tt7654321", "Direct exact match"))
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()
        viewModel.selectViewerTab(ViewerTab.SEARCH)
        advanceUntilIdle()

        viewModel.search("film Dune de guerre")
        advanceUntilIdle()

        assertEquals(listOf("Semantic duplicate"), viewModel.state.value.viewer.search.items.map(MediaTarget::title))
    }

    @Test
    fun inferredTypesWithoutConfiguredIntersectionKeepOrdinaryCatalogTypes() = runTest(dispatcher) {
        val gateway = semanticSearchGateway()
        gateway.semanticPages = mapOf(1 to semanticPage(emptyList(), mediaTypes = listOf("anime")))
        gateway.searchPages = mapOf(
            0 to io.rivune.api.AddonResourceBatch(
                results = listOf(addonSearchBatch("movie", "movie-1", "Configured result").results.single()),
                errors = emptyList(),
            ),
        )
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()
        viewModel.selectViewerTab(ViewerTab.SEARCH)
        advanceUntilIdle()

        viewModel.search("anime Dune")
        advanceUntilIdle()

        assertEquals(listOf("movie", "series"), gateway.addonSearchRequests.map { it.first })
        assertTrue(gateway.addonSearchRequests.all { it.second == "Dune" })
        assertEquals(listOf("Configured result"), viewModel.state.value.viewer.search.items.map(MediaTarget::title))
    }

    @Test
    fun semanticSearchTimeoutReusesAddonSearchStartedBeforeDeadline() = runTest(dispatcher) {
        val gateway = semanticSearchGateway()
        gateway.semanticDelayMillis = 13_000
        gateway.searchPages = mapOf(
            0 to io.rivune.api.AddonResourceBatch(
                results = listOf(addonSearchBatch("movie", "movie-1", "Fallback").results.single()),
                errors = emptyList(),
            ),
        )
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()
        viewModel.selectViewerTab(ViewerTab.SEARCH)
        advanceUntilIdle()

        viewModel.search("slow semantic query")
        runCurrent()
        assertEquals(listOf("movie", "series"), gateway.addonSearchRequests.map { it.first })
        assertTrue(gateway.addonSearchRequests.all { it.second == "slow semantic query" })
        assertEquals(listOf("Fallback"), viewModel.state.value.viewer.search.items.map(MediaTarget::title))
        assertEquals(ViewerLoading.SEARCH, viewModel.state.value.viewer.loading)
        advanceTimeBy(11_999)
        runCurrent()
        assertEquals(ViewerLoading.SEARCH, viewModel.state.value.viewer.loading)
        advanceTimeBy(1)
        advanceUntilIdle()

        val search = viewModel.state.value.viewer.search
        assertEquals(listOf("Fallback"), search.items.map(MediaTarget::title))
        assertEquals(listOf("movie", "series"), gateway.addonSearchRequests.map { it.first })
        assertTrue(search.partial)
        assertNull(viewModel.state.value.viewer.loading)
    }
    @Test
    fun semanticSearchErrorFallsBackWithoutLosingAddonResults() = runTest(dispatcher) {
        val gateway = semanticSearchGateway(types = listOf("movie"))
        gateway.semanticFailure = RivuneApiException.Server(404, "not_found", "Unsupported")
        gateway.searchPages = mapOf(0 to addonSearchBatch("movie", "movie-fallback", "Ordinary result"))
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()
        viewModel.selectViewerTab(ViewerTab.SEARCH)
        advanceUntilIdle()

        viewModel.search("ordinary query")
        advanceUntilIdle()

        val search = viewModel.state.value.viewer.search
        assertEquals(listOf("Ordinary result"), search.items.map(MediaTarget::title))
        assertEquals(listOf(Triple("movie", "ordinary query", 0)), gateway.addonSearchRequests)
        assertTrue(search.partial)
        assertNull(viewModel.state.value.viewer.inlineFailure)
    }


    @Test
    fun semanticSearchPagingPreservesItemsIntentsAndPartialState() = runTest(dispatcher) {
        val gateway = semanticSearchGateway(types = listOf("movie"))
        gateway.semanticPages = mapOf(
            1 to semanticPage(listOf(mediaItem("tmdb:1", "First")), hasMore = true, partial = true),
            2 to semanticPage(listOf(mediaItem("tmdb:2", "Second")), page = 2, hasMore = false),
        )
        gateway.searchPages = mapOf(
            0 to addonSearchBatch("movie", "movie-direct", "Direct"),
            24 to io.rivune.api.AddonResourceBatch(emptyList(), emptyList()),
        )
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()
        viewModel.selectViewerTab(ViewerTab.SEARCH)
        advanceUntilIdle()
        viewModel.search("film Dune de guerre")
        advanceUntilIdle()

        viewModel.loadMoreSearch()
        advanceUntilIdle()

        val search = viewModel.state.value.viewer.search
        assertEquals(listOf("First", "Direct", "Second"), search.items.map(MediaTarget::title))
        assertEquals(listOf("genre:war"), search.intents.map { it.id })
        assertEquals(2, search.page)
        assertFalse(search.hasMore)
        assertEquals(listOf(0, 24), gateway.addonSearchRequests.map { it.third })
        assertEquals(listOf(1, 2), gateway.semanticRequests.map { it.page })
    }

    @Test
    fun replacingSearchCancelsSemanticAndSpeculativeAddonWork() = runTest(dispatcher) {
        val gateway = semanticSearchGateway(types = listOf("movie"))
        gateway.semanticDelayMillis = 10_000
        gateway.searchDelayMillis = 10_000
        gateway.searchPages = mapOf(0 to addonSearchBatch("movie", "result", "Result"))
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()
        viewModel.selectViewerTab(ViewerTab.SEARCH)
        advanceUntilIdle()

        viewModel.search("first query")
        runCurrent()
        viewModel.search("")
        runCurrent()

        assertTrue(gateway.semanticCancelled)
        assertEquals(1, gateway.addonSearchCancellations)
        assertEquals("", viewModel.state.value.viewer.search.query)
        assertNull(viewModel.state.value.viewer.loading)
    }

    @Test
    fun clearingSearchInvalidatesInFlightResponse() = runTest(dispatcher) {
        val viewerProfile = profile(hasPin = false)
        val addonId = UUID.fromString("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
        val gateway = FakeGateway(
            restored = true,
            account = account(viewerProfile, active = true),
            collections = listOf(collection()),
        )
        gateway.catalogDescriptors = listOf(
            io.rivune.api.AddonCatalogDescriptor(
                addonId = addonId,
                addonName = "Catalog",
                manifestId = "org.example",
                position = 0,
                catalog = io.rivune.api.StremioManifestCatalog(type = "movie", id = "search"),
                addonCatalog = false,
                searchable = true,
            ),
        )
        gateway.searchPages = mapOf(0 to addonSearchBatch("movie", "stale", "Stale result"))
        gateway.searchDelayMillis = 1_000
        gateway.searchIgnoresCancellation = true
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()

        viewModel.selectViewerTab(ViewerTab.SEARCH)
        advanceUntilIdle()
        viewModel.search("Film")
        runCurrent()
        viewModel.search("")
        advanceTimeBy(1_000)
        runCurrent()

        assertEquals("", viewModel.state.value.viewer.search.query)
        assertTrue(viewModel.state.value.viewer.search.items.isEmpty())
        assertNull(viewModel.state.value.viewer.loading)
    }

    @Test
    fun remoteTargetsExcludeCurrentAndNonControllableDevices() {
        fun device(name: String, current: Boolean, capabilities: List<String>) = io.rivune.api.PlaybackDevice(
            UUID.randomUUID(),
            UUID.randomUUID(),
            name,
            "android",
            capabilities,
            io.rivune.api.PlaybackDeviceState("idle"),
            1,
            current,
            "2099-01-01T00:00:00Z",
        )
        val controllable = device("Remote", false, listOf("remote-control", "watch-room"))

        assertEquals(
            listOf(controllable),
            controllablePlaybackDevices(
                listOf(
                    controllable,
                    device("Room only", false, listOf("watch-room")),
                    device("Current", true, listOf("remote-control")),
                ),
            ),
        )
    }
    @Test
    fun playbackCommandTtlExpiresAtExactBoundary() {
        val viewModel = viewModel(FakeServerStore(), FakeGateway())
        val expiry = "2026-08-26T12:00:00Z"
        assertFalse(viewModel.playbackCommandExpired(expiry, Instant.parse("2026-08-26T11:59:59.999Z")))
        assertTrue(viewModel.playbackCommandExpired(expiry, Instant.parse(expiry)))
        assertTrue(viewModel.playbackCommandExpired("invalid", Instant.EPOCH))
    }

    @Test
    fun playbackOperationLedgerRemainsDurableInMeaningAndBoundedBeyondTtl() {
        val store = MemoryPlaybackOperationStore()
        val ids = (0..256).map { index -> UUID(0, index.toLong() + 1) }
        ids.forEach { id ->
            store.put(id, StoredPlaybackOperation(io.rivune.api.PlaybackCommandStatus.APPLIED, io.rivune.api.PlaybackCommandResultCode.APPLIED))
        }
        assertNull(store.get(ids.first()))
        assertNotNull(store.get(ids.last()))
    }


    @Test
    fun terminalRoomIntentAlwaysPreemptsPeriodicHostProgress() {
        assertEquals("playing", coordinatedHostRoomState(ending = false, playing = true))
        assertEquals("paused", coordinatedHostRoomState(ending = false, playing = false))
        assertEquals("ended", coordinatedHostRoomState(ending = true, playing = true))
        assertEquals("ended", coordinatedHostRoomState(ending = true, playing = false))
        assertTrue(shouldPublishHostRoomProgress(ending = false, ended = false))
        assertFalse(shouldPublishHostRoomProgress(ending = true, ended = false))
        assertFalse(shouldPublishHostRoomProgress(ending = false, ended = true))
    }

    @Test
    fun offlineResumeUsesLocalPositionUnlessPlaybackCompleted() {
        val item = OfflineMediaItem(
            id = UUID.randomUUID(),
            titleId = UUID.randomUUID(),
            title = "Downloaded",
            fileName = "media.rvn",
            container = "mp4",
            sizeBytes = 100,
            createdAtEpochMs = 1,
            posterUrl = null,
            positionMs = 42_000,
            durationMs = 90_000,
        )

        assertEquals(42_000, offlineStartPositionMs(item))
        assertEquals(0, offlineStartPositionMs(item.copy(completed = true)))
    }

    @Test
    fun offlineStartupUnlocksDownloadsWithoutGatewayAndDisconnectRelocksScope() {
        val root = kotlin.io.path.createTempDirectory("rivune-offline-startup").toFile()
        try {
            val offlineStore = OfflineMediaStore(root, testing = true)
            val scope = offlineStore.registerProfile("https://offline.example", UUID.randomUUID(), "Offline", hasPin = false, pin = null)
            val mediaId = UUID.randomUUID()
            val titleId = UUID.randomUUID()
            val directory = java.io.File(root, scope)
            java.io.File(directory, "$mediaId.rvn").writeBytes(byteArrayOf(1))
            java.io.File(directory, "manifest.json").writeText(
                """[{"id":"$mediaId","titleId":"$titleId","title":"Saved","fileName":"$mediaId.rvn","container":"mp4","sizeBytes":1,"createdAtEpochMs":${System.currentTimeMillis()},"posterUrl":""}]""",
            )
            offlineStore.lock()
            val viewModel = viewModel(FakeServerStore(), FakeGateway(), offlineMediaStore = offlineStore)

            assertEquals(AppDestination.Server, viewModel.state.value.destination)
            val gate = viewModel.state.value.offlineProfiles.single()
            viewModel.selectOfflineProfile(gate)
            assertEquals(AppDestination.Viewer, viewModel.state.value.destination)
            assertEquals("Saved", viewModel.state.value.viewer.offlineItems.single().title)

            viewModel.disconnectServer()
            assertEquals(AppDestination.Server, viewModel.state.value.destination)
            assertEquals(listOf(scope), viewModel.state.value.offlineProfiles.map(OfflineProfileGate::scope))
            assertFailsWith<IllegalArgumentException> { offlineStore.items(scope) }
        } finally {
            root.deleteRecursively()
        }
    }
    @Test
    fun backgroundRelocksProtectedOfflineOnlyButLeavesUnprotectedOpen() {
        val protected = offlineFixture(hasPin = true)
        val unprotected = offlineFixture(hasPin = false)
        try {
            val protectedVm = viewModel(FakeServerStore(), FakeGateway(), offlineMediaStore = protected.store)
            protectedVm.selectOfflineProfile(protected.store.profiles().single())
            protectedVm.submitPin("1234")
            assertEquals(AppDestination.Viewer, protectedVm.state.value.destination)

            protectedVm.lockOfflineAccessOnBackground()

            assertEquals(AppDestination.Server, protectedVm.state.value.destination)
            assertTrue(protectedVm.state.value.viewer.offlineItems.isEmpty())
            assertFailsWith<IllegalArgumentException> { protected.store.items(protected.scope) }

            val unprotectedVm = viewModel(FakeServerStore(), FakeGateway(), offlineMediaStore = unprotected.store)
            unprotectedVm.selectOfflineProfile(unprotected.store.profiles().single())
            unprotectedVm.lockOfflineAccessOnBackground()
            assertEquals(AppDestination.Viewer, unprotectedVm.state.value.destination)
            assertEquals("Saved", unprotectedVm.state.value.viewer.offlineItems.single().title)
        } finally {
            protected.root.deleteRecursively()
            unprotected.root.deleteRecursively()
        }
    }

    @Test
    fun backgroundRelocksProtectedOnlineDownloadsButPreservesOnlineViewer() = runTest(dispatcher) {
        val viewerProfile = profile(hasPin = true)
        val fixture = offlineFixture(hasPin = true, profileId = viewerProfile.id, withMedia = false)
        try {
            val viewModel = viewModel(
                FakeServerStore("https://saved.example.com"),
                FakeGateway(restored = true, account = account(viewerProfile, active = true), collections = listOf(collection())),
                offlineMediaStore = fixture.store,
            )
            advanceUntilIdle()
            viewModel.selectOfflineProfile(requireNotNull(fixture.store.profileGate(fixture.scope)))
            viewModel.submitPin("1234")

            viewModel.lockOfflineAccessOnBackground()

            assertEquals(AppDestination.Viewer, viewModel.state.value.destination)
            assertEquals(viewerProfile.id, viewModel.state.value.activeProfile?.id)
            assertTrue(viewModel.state.value.viewer.offlineItems.isEmpty())
            assertEquals(fixture.scope, viewModel.state.value.pendingOfflineProfile?.scope)
            assertFailsWith<IllegalArgumentException> { fixture.store.items(fixture.scope) }
        } finally {
            fixture.root.deleteRecursively()
        }
    }

    @Test
    fun restoredProtectedOnlineProfilePromptsAndUnlocksDownloadsWithoutLosingViewer() = runTest(dispatcher) {
        val viewerProfile = profile(hasPin = true)
        val fixture = offlineFixture(hasPin = true, profileId = viewerProfile.id)
        try {
            val gateway = FakeGateway(restored = true, account = account(viewerProfile, active = true), collections = listOf(collection()))
            val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway, offlineMediaStore = fixture.store)
            advanceUntilIdle()

            assertEquals(AppDestination.Viewer, viewModel.state.value.destination)
            assertEquals(viewerProfile.id, viewModel.state.value.activeProfile?.id)
            assertNotNull(viewModel.state.value.pendingOfflineProfile)
            assertTrue(viewModel.state.value.viewer.offlineItems.isEmpty())

            viewModel.dismissPin()
            assertEquals(AppDestination.Viewer, viewModel.state.value.destination)
            assertEquals(viewerProfile.id, viewModel.state.value.activeProfile?.id)
            assertNull(viewModel.state.value.pendingOfflineProfile)
            assertNull(viewModel.requireOfflineDownloadScope())
            assertEquals(fixture.scope, viewModel.state.value.pendingOfflineProfile?.scope)


            viewModel.selectOfflineProfile(fixture.store.profiles().single())
            viewModel.submitPin("0000")
            assertEquals(UiFailure.PROFILE_PIN_INVALID, viewModel.state.value.failure)
            assertTrue(viewModel.state.value.viewer.offlineItems.isEmpty())

            viewModel.submitPin("1234")
            assertEquals(AppDestination.Viewer, viewModel.state.value.destination)
            assertEquals(viewerProfile.id, viewModel.state.value.activeProfile?.id)
            assertEquals("Saved", viewModel.state.value.viewer.offlineItems.single().title)
        } finally {
            fixture.root.deleteRecursively()
        }
    }
    @Test
    fun readingQueueMutationRetriesWithStableOperationAndAdvancesCasRevision() = runTest(dispatcher) {
        val viewerProfile = profile()
        val titleId = UUID.randomUUID()
        val gateway = FakeGateway(restored = true, account = account(viewerProfile, active = true), collections = listOf(collection()))
        gateway.v22Queue = io.rivune.api.ReadingQueue(4, emptyList())
        gateway.failFirstQueueAdd = true
        gateway.resolvedTitle = io.rivune.api.TitleReference(titleId, io.rivune.api.TitleMediaType.MOVIE, "imdb", "tt1234567", "tt1234567", "Film")
        gateway.movieResult = io.rivune.api.Movie(
            titleId, io.rivune.api.MediaType.MOVIE, "Film", "Film", "en", "Overview", "2026-08-26",
            genres = emptyList(), cast = emptyList(), voteAverage = 8.0, voteCount = 1, externalIds = mapOf("imdb" to "tt1234567"),
        )
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()
        viewModel.openMedia(MediaTarget("tt1234567", "movie", "Film"))
        advanceUntilIdle()

        viewModel.addDetailToQueue()
        advanceUntilIdle()

        assertEquals(2, gateway.queueAddInputs.size)
        assertEquals(gateway.queueAddInputs[0].operationId, gateway.queueAddInputs[1].operationId)
        assertEquals(listOf(4L, 4L), gateway.queueAddInputs.map { it.expectedRevision })
        assertEquals(5, viewModel.state.value.viewer.features.queue?.revision)
        assertFalse(viewModel.state.value.viewer.features.conflict)
    }

    @Test
    fun partialV22LoadKeepsIndependentSuccessAndErrorStates() = runTest(dispatcher) {
        val viewerProfile = profile(canManage = true)
        val gateway = FakeGateway(restored = true, account = account(viewerProfile, active = true), collections = listOf(collection()))
        gateway.readingQueueFailure = RivuneApiException.Server(503, "unavailable", "Queue unavailable")
        gateway.v22Notifications = listOf(mediaNotification("17"))
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)

        advanceUntilIdle()

        val features = viewModel.state.value.viewer.features
        assertEquals(UiFailure.CONTENT_LOAD, features.queueLoad.failure)
        assertFalse(features.queueLoad.loaded)
        assertTrue(features.inboxLoad.loaded)
        assertNull(features.inboxLoad.failure)
        assertEquals("17", features.notifications.single().id)
        assertNull(features.queue)
        gateway.readingQueueFailure = null
        viewModel.retryV22Feature(V22FeatureKind.QUEUE)
        advanceUntilIdle()
        assertTrue(viewModel.state.value.viewer.features.queueLoad.loaded)
        assertNull(viewModel.state.value.viewer.features.queueLoad.failure)
    }

    @Test
    fun v22ProfileFeaturesLoadAndAccessibilityChangesWithProfile() = runTest(dispatcher) {
        val first = profile(canManage = true)
        val gateway = FakeGateway(restored = true, account = account(first, active = true), collections = listOf(collection()))
        gateway.v22Accessibility = accessibility(revision = 7, textScale = 115)
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)

        advanceUntilIdle()

        assertEquals(7, viewModel.state.value.viewer.features.accessibility?.revision)
        assertEquals(115, viewModel.state.value.viewer.features.accessibility?.textScale)
        viewModel.updateAccessibility { it.copy(textScale = 130) }
        advanceUntilIdle()
        assertEquals(130, viewModel.state.value.viewer.features.accessibility?.textScale)
        assertEquals(7, gateway.accessibilityUpdates.single().revision)
    }

    @Test
    fun failoverBudgetAndClosedPlayerErrorsPreventLoops() {
        val active = failover(attemptCount = 1, maximumAttempts = 2)
        val exhausted = failover(attemptCount = 2, maximumAttempts = 2)
        val player = playerPresentation(failover = active)
        val eligible = PlayerEngineFailure(42_000, fallbackEligible = true, PlayerEngineFailureReason.STARTUP_TIMEOUT)

        assertEquals(io.rivune.api.PlaybackFailoverError.SOURCE_TIMEOUT, eligible.playbackFailoverError())
        assertTrue(player.canAdvancePlaybackFailover(eligible))
        assertFalse(player.copy(failover = exhausted).canAdvancePlaybackFailover(eligible))
        assertFalse(player.copy(failoverAdvancing = true).canAdvancePlaybackFailover(eligible))
        assertNull(PlayerEngineFailure(0, fallbackEligible = false).playbackFailoverError())
    }

    @Test
    fun notificationReadAndDismissTransitionsAreProfileState() = runTest(dispatcher) {
        val first = profile(canManage = true)
        val gateway = FakeGateway(restored = true, account = account(first, active = true), collections = listOf(collection()))
        val notification = mediaNotification("1")
        gateway.v22Notifications = listOf(notification)
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway, instantNow = { Instant.parse("2026-08-26T12:00:00Z") })
        advanceUntilIdle()

        viewModel.acknowledgeMediaNotification(notification, dismiss = false)
        advanceUntilIdle()
        assertEquals("2026-08-26T12:00:00Z", viewModel.state.value.viewer.features.notifications.single().readAt)
        viewModel.acknowledgeMediaNotification(notification, dismiss = true)
        advanceUntilIdle()
        assertTrue(viewModel.state.value.viewer.features.notifications.isEmpty())
        assertEquals(listOf(io.rivune.api.MediaNotificationAcknowledgementState.READ, io.rivune.api.MediaNotificationAcknowledgementState.DISMISSED), gateway.notificationAcknowledgements)
    }



    private fun viewModel(
        store: FakeServerStore,
        gateway: FakeGateway,
        isTv: Boolean = false,
        externalPlaybackSupport: ExternalPlaybackSupport = ExternalPlaybackSupport(),
        appPreferences: AppPreferencesReader = AppPreferencesReader { AppPreferencesState() },
        locale: java.util.Locale = java.util.Locale.ENGLISH,
        playbackNetwork: NetworkClass = NetworkClass.REMOTE_WIFI,
        serverConnectionAllowed: (String) -> Boolean = { true },
        instantNow: () -> Instant = Instant::now,
        offlineMediaStore: OfflineMediaStore? = null,
        awaitCoordinationTick: suspend (Long) -> Unit = { _ -> kotlinx.coroutines.awaitCancellation() },
        monotonicNowMilliseconds: () -> Long = { 0L },
    ) = RivuneViewModel(
        store,
        RivuneGatewayFactory { gateway },
        isTv,
        "Test device",
        CoroutineScope(dispatcher),
        externalPlaybackSupportProvider = { externalPlaybackSupport },
        appPreferences = appPreferences,
        localeProvider = { locale },
        playbackNetworkProvider = { playbackNetwork },
        serverConnectionAllowed = serverConnectionAllowed,
        instantNow = instantNow,
        offlineMediaStore = offlineMediaStore,
        awaitCoordinationTick = awaitCoordinationTick,
        monotonicNowMilliseconds = monotonicNowMilliseconds,
    ).also(viewModels::add)
}
private data class OfflineFixture(
    val root: java.io.File,
    val store: OfflineMediaStore,
    val scope: String,
)

private fun offlineFixture(
    hasPin: Boolean,
    profileId: UUID = UUID.randomUUID(),
    origin: String = "https://saved.example.com",
    withMedia: Boolean = true,
): OfflineFixture {
    val root = kotlin.io.path.createTempDirectory("rivune-offline-vm").toFile()
    val store = OfflineMediaStore(root, testing = true)
    val scope = store.registerProfile(origin, profileId, "Offline", hasPin, if (hasPin) "1234" else null)
    if (withMedia) {
        val mediaId = UUID.randomUUID()
        val titleId = UUID.randomUUID()
        val directory = java.io.File(root, scope)
        java.io.File(directory, "$mediaId.rvn").writeBytes(byteArrayOf(1))
        java.io.File(directory, "manifest.json").writeText(
            """[{"id":"$mediaId","titleId":"$titleId","title":"Saved","fileName":"$mediaId.rvn","container":"mp4","sizeBytes":1,"createdAtEpochMs":${System.currentTimeMillis()},"posterUrl":""}]""",
        )
    }
    store.lock()
    return OfflineFixture(root, store, scope)
}


private class FakeServerStore(initial: String? = null) : ServerAddressStore {
    var value: String? = initial
    override fun load() = value
    override fun save(value: String) {
        this.value = value
    }
    override fun clear() {
        value = null
    }
}

private class FakeGateway(
    private val discovery: Discovery = discovery(),
    private val restored: Boolean = false,
    private val account: Account = account(profile()),
    var collections: List<Collection> = emptyList(),
    private val resolvedFolders: Map<UUID, List<ResolvedCollectionFolder>> = emptyMap(),
    private val accountFailure: Throwable? = null,
    private val collectionFailure: Throwable? = null,
    private val selectionFailure: Throwable? = null,
    var pairingPending: Boolean = false,
    private val logoutResult: LogoutResult = LogoutResult(localCredentialsCleared = true, serverSessionClosed = true),
    private val authorizationFailure: Throwable? = null,
) : RivuneGateway {
    var selectedPin: String? = null
    var exchangeCount = 0
    var clearSelectionCount = 0
    var authorizationPlatform: String? = null
    var authorizationInstallationId: String = ""
    var loggedOut = false
    val resolvedPages = mutableListOf<Int>()
    val fullFolderRequests = mutableListOf<UUID>()
    val artworkFolderRequests = mutableListOf<UUID>()
    var catalogDescriptors = emptyList<io.rivune.api.AddonCatalogDescriptor>()
    var searchPages = emptyMap<Int, io.rivune.api.AddonResourceBatch>()
    var semanticPages = emptyMap<Int, io.rivune.api.SemanticSearchPage>()
    var semanticDelayMillis: Long = 0
    var semanticFailure: Throwable? = null
    val semanticRequests = mutableListOf<io.rivune.api.SemanticSearchRequest>()
    val addonSearchRequests = mutableListOf<Triple<String, String, Int>>()
    var semanticCancelled = false
    var addonSearchCancellations = 0
    var libraryPages = emptyMap<Int, io.rivune.api.LibraryPage>()
    var resolvedTitle: io.rivune.api.TitleReference? = null
    val resolvedTitleInputs = mutableListOf<io.rivune.api.TitleResolveInput>()
    var movieResult: io.rivune.api.Movie? = null
    var seriesResult: io.rivune.api.Series? = null
    var seriesFailures = emptyMap<io.rivune.api.SeriesMappingProvider, Throwable>()
    var seriesResults = emptyMap<Pair<io.rivune.api.SeriesMappingProvider, String?>, io.rivune.api.Series>()
    var seasons = emptyMap<String, io.rivune.api.Season>()
    val seriesRequests = mutableListOf<Triple<UUID, io.rivune.api.SeriesMappingProvider, String?>>()
    val seasonRequests = mutableListOf<Pair<String, io.rivune.api.SeriesMappingProvider>>()
    var progress: io.rivune.api.PlaybackProgress? = null
    var sourceList: io.rivune.api.PlaybackSourceList? = null
    val playbackEvents = mutableListOf<String>()
    val playbackSourceResources = mutableListOf<String>()
    var lastPlaybackCapabilities: io.rivune.api.PlaybackCapabilities? = null
    var playbackSourceDelayMillis: Long = 0
    var watchedDelayMillis: Long = 0
    var watchedFailure: Throwable? = null
    var preparedForExternalPlayer = false
    var resolvedForExternalPlayer = false
    var preparePlaybackCalls = 0
    var resolvePlaybackCalls = 0
    val preparedStartSeconds = mutableListOf<Int?>()
    val resolvedStartSeconds = mutableListOf<Int?>()
    val preparedSourceRefs = mutableListOf<String>()
    val resolvedSourceRefs = mutableListOf<String>()
    var preparation: io.rivune.api.PlaybackPreparation? = null
    var playbackSession: io.rivune.api.PlaybackSession? = null
    var markerResult = io.rivune.api.PlaybackMarkerList(emptyList())
    var markerFailure: Throwable? = null
    var markerDelayMillis: Long = 0
    var markerResultsByCall = emptyList<io.rivune.api.PlaybackMarkerList>()
    var markerDelaysByCall = emptyList<Long>()
    val markerRequests = mutableListOf<PlaybackMarkerRequest>()
    var calendarEvents = emptyList<io.rivune.api.CalendarEvent>()
    var libraryAdded: UUID? = null
    var libraryRemoved: UUID? = null
    var stoppedPlayback: UUID? = null
    var stopPlaybackCalls = 0
    var progressUpdates = mutableListOf<io.rivune.api.UpdatePlaybackProgressRequest>()
    var progressByTitle = mutableMapOf<UUID, io.rivune.api.PlaybackProgress>()
    var progressBatchFailure: Throwable? = null
    val watchedBatchRequests = mutableListOf<List<io.rivune.api.SetWatchedBatchItem>>()
    var watchedBatchFailureAtRequest: Int? = null
    val watchedRequests = mutableListOf<Pair<UUID, Long>>()
    var seasonDelayMillis: Long = 0
    var searchDelayMillis: Long = 0
    var searchIgnoresCancellation = false
    var searchDelaysByType = emptyMap<String, Long>()
    var activeAddonSearches = 0
    var maxConcurrentAddonSearches = 0
    var searchPagesByType = emptyMap<String, io.rivune.api.AddonResourceBatch>()
    var trailerResults = emptyMap<Int?, List<io.rivune.api.Trailer>>()
    var continueWatchingDelayMillis: Long = 0
    val continueWatchingLimits = mutableListOf<Int?>()
    var continueWatchingPage = io.rivune.api.ContinueWatchingPage(emptyList())
    val trailerRequests = mutableListOf<Pair<UUID, Int?>>()
    val metadataRequests = mutableListOf<Pair<String, String?>>()
    var profileAvatars = emptyMap<UUID, ByteArray>()
    var effectiveSettingsResult = io.rivune.api.EffectiveSettings(
        schemaVersion = 1,
        settings = io.rivune.api.SettingsValues(),
        sources = io.rivune.api.EffectiveSettingsSources(),
    )
    val profileSettingsUpdates = mutableListOf<io.rivune.api.ProfileSettingsUpdate>()
    var playbackHeartbeatRequests = 0
    var playbackDeviceListRequests = 0
    var localRecommendationRequests = 0
    var localRecommendationArtworkShape: io.rivune.api.RecommendationArtworkShape? = null
    var v22Accessibility = accessibility()
    val accessibilityUpdates = mutableListOf<io.rivune.api.AccessibilityPreferencesDocument>()
    var v22Notifications = emptyList<io.rivune.api.MediaNotification>()
    val notificationAcknowledgements = mutableListOf<io.rivune.api.MediaNotificationAcknowledgementState>()
    var v22Queue = io.rivune.api.ReadingQueue(1, emptyList())
    var failFirstQueueAdd = false
    val queueAddInputs = mutableListOf<io.rivune.api.ReadingQueueAddInput>()
    var readingQueueFailure: Throwable? = null

    override suspend fun discover() = discovery
    override suspend fun restoreSession() = restored
    override suspend fun clearProfileSelection() {
        clearSelectionCount += 1
    }
    override suspend fun currentAccount(): Account {
        accountFailure?.let { throw it }
        return account
    }
    override suspend fun selectProfile(id: UUID, pin: String?): ProfileSelection {
        selectionFailure?.let { throw it }
        selectedPin = pin
        val selected = account.profiles.single { it.id == id }
        return ProfileSelection(selected, "2099-01-01T00:00:00Z", "context")
    }
    override suspend fun profileAvatar(profileId: UUID) = profileAvatars.getValue(profileId)
    override suspend fun collections(): List<Collection> {
        collectionFailure?.let { throw it }
        return collections
    }
    override suspend fun resolveCollectionFolderArtwork(collectionId: UUID, folderId: UUID, language: String?): ResolvedCollectionFolder {
        artworkFolderRequests += folderId
        return resolvedFolderResponse(folderId, page = 1, language = language)
    }
    override suspend fun resolveCollectionFolder(collectionId: UUID, folderId: UUID, page: Int?, language: String?): ResolvedCollectionFolder {
        fullFolderRequests += folderId
        return resolvedFolderResponse(folderId, page ?: 1, language)
    }
    private fun resolvedFolderResponse(folderId: UUID, page: Int, language: String?): ResolvedCollectionFolder {
        metadataRequests += "collection" to language
        resolvedPages += page
        return resolvedFolders.getValue(folderId).first { it.page == page }
    }
    override suspend fun addonCatalogs() = catalogDescriptors
    override suspend fun searchAddonCatalogs(type: String, search: String, skip: Int?, limit: Int?, language: String?): io.rivune.api.AddonResourceBatch {
        metadataRequests += "search" to language
        addonSearchRequests += Triple(type, search, skip ?: 0)
        val response = searchPagesByType[type] ?: searchPages.getValue(skip ?: 0)
        val searchDelay = searchDelaysByType[type] ?: searchDelayMillis
        activeAddonSearches += 1
        maxConcurrentAddonSearches = maxOf(maxConcurrentAddonSearches, activeAddonSearches)
        try {
            if (searchDelay > 0) {
                if (searchIgnoresCancellation) {
                    kotlinx.coroutines.withContext(kotlinx.coroutines.NonCancellable) { delay(searchDelay) }
                } else {
                    delay(searchDelay)
                }
            }
            return response
        } catch (cause: kotlinx.coroutines.CancellationException) {
            addonSearchCancellations += 1
            throw cause
        } finally {
            activeAddonSearches -= 1
        }
    }
    override suspend fun semanticSearch(input: io.rivune.api.SemanticSearchRequest): io.rivune.api.SemanticSearchPage {
        semanticRequests += input
        if (semanticDelayMillis > 0) try {
            delay(semanticDelayMillis)
        } catch (cause: kotlinx.coroutines.CancellationException) {
            semanticCancelled = true
            throw cause
        }
        semanticFailure?.let { throw it }
        return semanticPages.getValue(input.page)
    }
    override suspend fun resolveTitle(input: io.rivune.api.TitleResolveInput): io.rivune.api.TitleReference {
        resolvedTitleInputs += input
        return requireNotNull(resolvedTitle)
    }
    override suspend fun movie(id: UUID, language: String?): io.rivune.api.Movie {
        metadataRequests += "movie" to language
        return requireNotNull(movieResult)
    }
    override suspend fun series(
        id: UUID,
        mappingProvider: io.rivune.api.SeriesMappingProvider,
        language: String?,
        episodeOrder: String?,
    ): io.rivune.api.Series {
        metadataRequests += "series" to language
        seriesRequests += Triple(id, mappingProvider, episodeOrder)
        seriesFailures[mappingProvider]?.let { throw it }
        return seriesResults[mappingProvider to episodeOrder] ?: requireNotNull(seriesResult)
    }
    override suspend fun season(id: String, mappingProvider: io.rivune.api.SeriesMappingProvider, language: String?): io.rivune.api.Season {
        metadataRequests += "season" to language
        seasonRequests += id to mappingProvider
        if (seasonDelayMillis > 0) delay(seasonDelayMillis)
        return seasons.getValue(id)
    }
    override suspend fun trailers(titleId: UUID, seasonNumber: Int?, language: String?): List<io.rivune.api.Trailer> {
        metadataRequests += "trailers" to language
        trailerRequests += titleId to seasonNumber
        return trailerResults[seasonNumber].orEmpty()
    }
    override suspend fun library(mediaType: io.rivune.api.TitleMediaType?, page: Int?, pageSize: Int?) = libraryPages[page ?: 1]
        ?: io.rivune.api.LibraryPage(emptyList(), page ?: 1, page ?: 1, 0)
    override suspend fun addLibraryTitle(titleId: UUID): io.rivune.api.LibraryItem {
        libraryAdded = titleId
        return libraryPages.values.flatMap { it.items }.first { it.titleId == titleId }
    }
    override suspend fun removeLibraryTitle(titleId: UUID) { libraryRemoved = titleId }
    override suspend fun continueWatching(limit: Int?): io.rivune.api.ContinueWatchingPage {
        continueWatchingLimits += limit
        if (continueWatchingDelayMillis > 0) delay(continueWatchingDelayMillis)
        return continueWatchingPage
    }
    override suspend fun playbackProgress(titleId: UUID) = progressByTitle[titleId] ?: progress?.takeIf { it.titleId == titleId }
    override suspend fun playbackProgressBatch(titleIds: List<UUID>): io.rivune.api.PlaybackProgressBatch {
        progressBatchFailure?.let { throw it }
        return io.rivune.api.PlaybackProgressBatch(
            titleIds.map { titleId -> io.rivune.api.PlaybackProgressBatchItem(titleId, progressByTitle[titleId] ?: progress?.takeIf { it.titleId == titleId }) },
        )
    }
    override suspend fun setTitlesWatchedBatch(items: List<io.rivune.api.SetWatchedBatchItem>): io.rivune.api.SetWatchedBatchResult {
        watchedBatchRequests += items
        if (watchedBatchFailureAtRequest == watchedBatchRequests.size) {
            watchedBatchFailureAtRequest = null
            throw RivuneApiException.Server(500, "batch_failed", "failed")
        }
        return io.rivune.api.SetWatchedBatchResult(
            items.map { item ->
                val current = progressByTitle[item.titleId]
                val updated = (current ?: io.rivune.api.PlaybackProgress(
                    titleId = item.titleId,
                    mediaType = io.rivune.api.PlaybackProgressMediaType.EPISODE,
                    positionSeconds = 0,
                    durationSeconds = 0,
                    completed = false,
                    version = 0,
                    lastWatchedAt = "2026-08-12T00:00:00Z",
                    updatedAt = "2026-08-12T00:00:00Z",
                )).copy(completed = item.completed, version = item.expectedVersion + 1)
                progressByTitle[item.titleId] = updated
                io.rivune.api.SetWatchedBatchResultItem(item.titleId, updated)
            },
        )
    }
    override suspend fun markTitleWatched(titleId: UUID, expectedVersion: Long): io.rivune.api.PlaybackProgress {
        watchedRequests += titleId to expectedVersion
        if (watchedDelayMillis > 0) delay(watchedDelayMillis)
        watchedFailure?.let { throw it }
        val current = progressByTitle[titleId] ?: progress?.takeIf { it.titleId == titleId }
        val updated = (current ?: io.rivune.api.PlaybackProgress(
            titleId = titleId,
            mediaType = io.rivune.api.PlaybackProgressMediaType.MOVIE,
            positionSeconds = 0,
            durationSeconds = 0,
            completed = false,
            version = 0,
            lastWatchedAt = "2026-08-12T00:00:00Z",
            updatedAt = "2026-08-12T00:00:00Z",
        )).copy(
            positionSeconds = current?.durationSeconds ?: 0,
            completed = true,
            version = expectedVersion + 1,
        )
        progressByTitle[titleId] = updated
        return updated
    }
    override suspend fun markTitleUnwatched(titleId: UUID, expectedVersion: Long) = requireNotNull(progress).copy(completed = false, version = expectedVersion + 1)
    override suspend fun effectiveProfileSettings(id: UUID) = effectiveSettingsResult
    override suspend fun updateProfileSettings(
        id: UUID,
        input: io.rivune.api.ProfileSettingsUpdate,
    ): io.rivune.api.SettingsLayer {
        profileSettingsUpdates += input
        val current = effectiveSettingsResult.settings
        val updated = current.copy(
            maximumResolution = input.maximumResolution.applyEffectiveTo(current.maximumResolution),
            preferDirectPlay = input.preferDirectPlay.applyEffectiveTo(current.preferDirectPlay),
            audioLanguage = input.audioLanguage.applyEffectiveTo(current.audioLanguage),
            metadataLanguage = input.metadataLanguage.applyEffectiveTo(current.metadataLanguage),
            subtitleLanguage = input.subtitleLanguage.applyEffectiveTo(current.subtitleLanguage),
            forcedSubtitleLanguage = input.forcedSubtitleLanguage.applyEffectiveTo(current.forcedSubtitleLanguage),
            autoplayNextEpisode = input.autoplayNextEpisode.applyEffectiveTo(current.autoplayNextEpisode),
            transcoding = input.transcoding.applyEffectiveTo(current.transcoding),
        )
        val currentSources = effectiveSettingsResult.sources
        val updatedSources = currentSources.copy(
            maximumResolution = input.maximumResolution.applySourceTo(currentSources.maximumResolution),
            preferDirectPlay = input.preferDirectPlay.applySourceTo(currentSources.preferDirectPlay),
            audioLanguage = input.audioLanguage.applySourceTo(currentSources.audioLanguage),
            metadataLanguage = input.metadataLanguage.applySourceTo(currentSources.metadataLanguage),
            subtitleLanguage = input.subtitleLanguage.applySourceTo(currentSources.subtitleLanguage),
            forcedSubtitleLanguage = input.forcedSubtitleLanguage.applySourceTo(currentSources.forcedSubtitleLanguage),
            autoplayNextEpisode = input.autoplayNextEpisode.applySourceTo(currentSources.autoplayNextEpisode),
            transcoding = input.transcoding.applySourceTo(currentSources.transcoding),
        )
        effectiveSettingsResult = effectiveSettingsResult.copy(settings = updated, sources = updatedSources)
        return io.rivune.api.SettingsLayer(1, updated)
    }
    override suspend fun playbackSources(mediaType: String, resourceId: String, capabilities: io.rivune.api.PlaybackCapabilities, addonId: UUID?): io.rivune.api.PlaybackSourceList {
        lastPlaybackCapabilities = capabilities
        playbackSourceResources += resourceId
        playbackEvents += "sources:$resourceId"
        if (playbackSourceDelayMillis > 0) delay(playbackSourceDelayMillis)
        return requireNotNull(sourceList)
    }
    override suspend fun preparePlayback(sourceRef: String, startSeconds: Int?, externalPlayer: Boolean): io.rivune.api.PlaybackPreparation {
        preparedForExternalPlayer = externalPlayer
        preparePlaybackCalls += 1
        preparedStartSeconds += startSeconds
        preparedSourceRefs += sourceRef
        return requireNotNull(preparation)
    }
    override suspend fun playbackMarkers(imdbId: String, season: Int, episode: Int): io.rivune.api.PlaybackMarkerList {
        val request = PlaybackMarkerRequest(imdbId, season, episode)
        val callIndex = markerRequests.size
        markerRequests += request
        val result = markerResultsByCall.getOrNull(callIndex) ?: markerResult
        val delayMillis = markerDelaysByCall.getOrNull(callIndex) ?: markerDelayMillis
        if (delayMillis > 0) delay(delayMillis)
        markerFailure?.let { throw it }
        return result
    }
    override suspend fun resolvePlayback(sourceRef: String, titleId: String?, startSeconds: Int?, externalPlayer: Boolean): io.rivune.api.PlaybackSession {
        resolvedForExternalPlayer = externalPlayer
        resolvePlaybackCalls += 1
        resolvedSourceRefs += sourceRef
        resolvedStartSeconds += startSeconds
        return requireNotNull(playbackSession)
    }
    override suspend fun stopPlayback(sessionId: UUID) {
        stoppedPlayback = sessionId
        stopPlaybackCalls += 1
        playbackEvents += "stop"
    }
    override suspend fun updatePlaybackProgress(titleId: UUID, input: io.rivune.api.UpdatePlaybackProgressRequest): io.rivune.api.PlaybackProgress {
        progressUpdates += input
        playbackEvents += "progress:${input.positionSeconds}"
        return requireNotNull(progress).copy(positionSeconds = input.positionSeconds, durationSeconds = input.durationSeconds, completed = input.completed, version = input.expectedVersion + 1)
    }
    override suspend fun updatePlaybackDevice(input: io.rivune.api.PlaybackDeviceHeartbeatInput): io.rivune.api.PlaybackDevice {
        playbackHeartbeatRequests += 1
        return io.rivune.api.PlaybackDevice(UUID.randomUUID(), UUID.randomUUID(), "Current", "android", input.capabilities, input.state, 1, true, "2099-01-01T00:00:00Z")
    }
    override suspend fun playbackDevices(): io.rivune.api.PlaybackDeviceList {
        playbackDeviceListRequests += 1
        return io.rivune.api.PlaybackDeviceList(emptyList())
    }
    override suspend fun sendPlaybackCommand(sessionId: UUID, input: io.rivune.api.PlaybackCommandInput) = fakeCommand(input)
    override suspend fun playbackCommands(after: UUID?) = io.rivune.api.PlaybackCommandList(emptyList())
    override suspend fun reportPlaybackCommandResult(operationId: UUID, input: io.rivune.api.PlaybackCommandResultInput) =
        fakeCommand(io.rivune.api.PlaybackCommandInput(operationId, io.rivune.api.PlaybackCommandType.PLAY), input.status, input.code)
    override suspend fun outgoingPlaybackCommand(operationId: UUID) =
        fakeCommand(io.rivune.api.PlaybackCommandInput(operationId, io.rivune.api.PlaybackCommandType.LOAD))
    override suspend fun exportProfileArchive(profileId: UUID) = kotlinx.serialization.json.buildJsonObject {
        put("version", kotlinx.serialization.json.JsonPrimitive(2)); put("identity", kotlinx.serialization.json.buildJsonObject {}); put("continueDismissals", kotlinx.serialization.json.buildJsonArray {})
    }
    override suspend fun importProfileArchive(profileId: UUID, archive: kotlinx.serialization.json.JsonObject) = error("unused")
    override suspend fun createProfileFromArchive(categoryId: UUID, archive: kotlinx.serialization.json.JsonObject) = error("unused")
    override suspend fun createPlaybackRoom(input: io.rivune.api.PlaybackRoomCreateInput) = fakePlaybackRoom(input.item, input.state, input.positionMilliseconds, input.durationMilliseconds)
    override suspend fun joinPlaybackRoom(code: String) = fakePlaybackRoom(io.rivune.api.CoordinatedPlaybackItem(UUID.randomUUID()), "paused", 0, 0)
    override suspend fun playbackRoom(id: UUID) = fakePlaybackRoom(io.rivune.api.CoordinatedPlaybackItem(UUID.randomUUID()), "paused", 0, 0, id)
    override suspend fun updatePlaybackRoom(id: UUID, input: io.rivune.api.PlaybackRoomUpdateInput) = fakePlaybackRoom(io.rivune.api.CoordinatedPlaybackItem(UUID.randomUUID()), input.state, input.positionMilliseconds, input.durationMilliseconds, id)
    override suspend fun leavePlaybackRoom(id: UUID) = Unit
    override suspend fun localRecommendations(limit: Int, artworkShape: io.rivune.api.RecommendationArtworkShape?): io.rivune.api.LocalRecommendationPage {
        localRecommendationRequests += 1
        localRecommendationArtworkShape = artworkShape
        return io.rivune.api.LocalRecommendationPage(emptyList())
    }
    override suspend fun calendar(from: String, to: String, language: String?): List<io.rivune.api.CalendarEvent> {
        metadataRequests += "calendar" to language
        return calendarEvents
    }
    override suspend fun beginDeviceAuthorization(installationId: String, deviceName: String, platform: String): DeviceAuthorizationResponse {
        authorizationInstallationId = installationId
        authorizationFailure?.let { throw it }
        authorizationPlatform = platform
        return DeviceAuthorizationResponse(
            deviceCode = "device-code",
            userCode = "ABCD-EFGH",
            verificationUri = "https://media.example.com/pair",
            verificationUriComplete = "https://media.example.com/pair?code=ABCD-EFGH",
            expiresAt = "2099-01-01T00:00:00Z",
            intervalSeconds = 1,
        )
    }

    override suspend fun exchangeDeviceAuthorization(deviceCode: String) {
        exchangeCount += 1
        if (pairingPending) throw RivuneApiException.Server(400, "authorization_pending", "pending")
    }
    override suspend fun logout(): LogoutResult {
        loggedOut = true
        return logoutResult
    }
    override suspend fun readingQueue(profileId: UUID): io.rivune.api.ReadingQueue {
        readingQueueFailure?.let { throw it }
        return v22Queue
    }
    override suspend fun addReadingQueueItem(profileId: UUID, input: io.rivune.api.ReadingQueueAddInput): io.rivune.api.ReadingQueueMutation {
        queueAddInputs += input
        if (failFirstQueueAdd && queueAddInputs.size == 1) throw java.io.IOException("offline")
        v22Queue = v22Queue.copy(revision = v22Queue.revision + 1)
        return io.rivune.api.ReadingQueueMutation(v22Queue.revision)
    }
    override suspend fun savedSearches() = emptyList<io.rivune.api.SavedSearch>()
    override suspend fun smartCollections() = emptyList<io.rivune.api.SmartCollection>()
    override suspend fun extensionIncidents() = emptyList<io.rivune.api.AddonIncident>()
    override suspend fun mediaNotificationSubscriptions() = emptyList<io.rivune.api.MediaNotificationSubscription>()
    override suspend fun mediaNotifications(cursor: String?, limit: Int?) = io.rivune.api.MediaNotificationPage(v22Notifications)
    override suspend fun acknowledgeMediaNotification(id: String, state: io.rivune.api.MediaNotificationAcknowledgementState) {
        notificationAcknowledgements += state
    }
    override suspend fun profileAccessibilityPreferences(profileId: UUID) = v22Accessibility
    override suspend fun updateProfileAccessibilityPreferences(profileId: UUID, input: io.rivune.api.AccessibilityPreferencesDocument): io.rivune.api.AccessibilityPreferencesDocument {
        accessibilityUpdates += input
        v22Accessibility = input.copy(revision = input.revision + 1)
        return v22Accessibility
    }
    override fun resolveArtworkUrl(value: String) = if (value.startsWith("/")) "https://media.example.com$value" else null
    override fun resolveResourceUrl(value: String) = if (value.startsWith("https://")) value else "https://media.example.com$value"
}
private fun FakeGateway.configurePlayback(
    titleId: UUID,
    sessionId: UUID = UUID.randomUUID(),
): io.rivune.api.PlaybackSourceOption {
    val addonId = UUID.randomUUID()
    val source = io.rivune.api.PlaybackSourceOption(
        id = "source",
        sourceRef = "source-ref",
        addonId = addonId,
        manifestId = "addon",
        streamIndex = 0,
        name = "Direct",
        protocol = "http",
        mode = io.rivune.api.PlaybackMode.DIRECT,
        expiresAt = "2099-01-01T00:00:00Z",
        stableIdentity = "stable-direct",
    )
    progress = io.rivune.api.PlaybackProgress(
        titleId,
        io.rivune.api.PlaybackProgressMediaType.EPISODE,
        0,
        1_800,
        false,
        0,
        "2026-08-12T00:00:00Z",
        "2026-08-12T00:00:00Z",
    )
    sourceList = io.rivune.api.PlaybackSourceList(listOf(source), emptyList())
    preparation = io.rivune.api.PlaybackPreparation(
        source.sourceRef,
        io.rivune.api.PlaybackMode.DIRECT,
        "http",
        subtitleCount = 0,
        expiresAt = "2099-01-01T00:00:00Z",
    )
    playbackSession = io.rivune.api.PlaybackSession(
        sessionId,
        "selected",
        sources = listOf(
            io.rivune.api.PlaybackSource(
                "selected",
                addonId,
                "addon",
                mode = io.rivune.api.PlaybackMode.DIRECT,
                url = "https://media.example.com/episode.m3u8",
                protocol = "hls",
                compatible = true,
                media = io.rivune.api.PlaybackMediaInspection(durationSeconds = 1_800.0),
            ),
        ),
        subtitles = emptyList(),
        providerErrors = emptyList(),
        expiresAt = "2099-01-01T00:00:00Z",
    )
    return source
}

private fun <T> io.rivune.api.PatchField<T>.applyEffectiveTo(current: T?): T? = when (this) {
    io.rivune.api.PatchField.Omitted, io.rivune.api.PatchField.Null -> current
    is io.rivune.api.PatchField.Value -> value
}

private fun <T> io.rivune.api.PatchField<T>.applySourceTo(current: String?): String? = when (this) {
    io.rivune.api.PatchField.Omitted -> current
    io.rivune.api.PatchField.Null -> "instance"
    is io.rivune.api.PatchField.Value -> "profile"
}

private fun semanticSearchGateway(types: List<String> = listOf("movie", "series")): FakeGateway {
    val gateway = FakeGateway(
        discovery = discovery(capabilities = listOf("semantic-search")),
        restored = true,
        account = account(profile(hasPin = false), active = true),
        collections = listOf(collection()),
    )
    gateway.catalogDescriptors = types.mapIndexed { index, type ->
        io.rivune.api.AddonCatalogDescriptor(
            addonId = UUID.fromString("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"),
            addonName = "Catalog",
            manifestId = "org.example",
            position = index,
            catalog = io.rivune.api.StremioManifestCatalog(type = type, id = "search-$type"),
            addonCatalog = false,
            searchable = true,
        )
    }
    return gateway
}

private fun semanticPage(
    items: List<CollectionItem>,
    mediaTypes: List<String> = listOf("movie"),
    hasMore: Boolean = false,
    page: Int = 1,
    partial: Boolean = false,
) = io.rivune.api.SemanticSearchPage(
    intents = listOf(io.rivune.api.SemanticSearchIntent("genre:war", "genre", "war", "War")),
    titleQuery = "Dune",
    mediaTypes = mediaTypes,
    items = items,
    page = page,
    hasMore = hasMore,
    partial = partial,
)

private fun addonSearchBatch(
    type: String,
    id: String,
    title: String,
) = io.rivune.api.AddonResourceBatch(
    results = listOf(
        io.rivune.api.AddonResourceResult(
            addonId = UUID.fromString("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"),
            manifestId = "org.example",
            resource = "catalog",
            type = type,
            id = "search-$type",
            payload = kotlinx.serialization.json.Json.parseToJsonElement(
                """{"metas":[{"id":"$id","type":"$type","name":"$title"}]}""",
            ) as kotlinx.serialization.json.JsonObject,
            cache = io.rivune.api.AddonCachePolicy(),
        ),
    ),

    errors = emptyList(),
)
private fun fakeCommand(
    input: io.rivune.api.PlaybackCommandInput,
    status: io.rivune.api.PlaybackCommandStatus = io.rivune.api.PlaybackCommandStatus.PENDING,
    code: io.rivune.api.PlaybackCommandResultCode? = null,
) = io.rivune.api.PlaybackCommand(
    operationId = input.operationId,
    command = input.command,
    item = input.item,
    positionMilliseconds = input.positionMilliseconds,
    mode = input.mode,
    targetRevision = input.targetRevision,
    status = status,
    resultCode = code,
    senderDeviceName = "Sender",
    createdAt = "2099-01-01T00:00:00Z",
    expiresAt = "2099-01-01T00:02:00Z",
)
private fun discovery(
    setupRequired: Boolean = false,
    capabilities: List<String> = listOf("local-recommendations", "playback-coordination", "playback-command-results"),
) = Discovery(
    name = "Family server",
    serverVersion = "20.0.0",
    protocolVersion = 22,
    apiBaseUrl = "/api/v1",
    setupRequired = setupRequired,
    setupCompleted = !setupRequired,
    demoAvailable = false,
    timezone = "UTC",
    interfaceLanguage = "fr-FR",
    capabilities = capabilities,
)

private fun profile(
    hasPin: Boolean = false,
    canManage: Boolean = false,
    avatar: ProfileAvatar = ProfileAvatar("preset", "one", "/api/v1/profile-avatars/one"),
): Profile {
    val category = CategoryRef(CATEGORY_ID, "Home", null, null)
    return Profile(
        id = PROFILE_ID,
        name = "Viewer",
        description = "Family profile",
        categoryId = CATEGORY_ID,
        category = category,
        isChild = false,
        hasPin = hasPin,
        canManage = canManage,
        enabled = true,
        availableFrom = null,
        availableUntil = null,
        accessStartTime = null,
        accessEndTime = null,
        accessTimezone = "UTC",
        accessible = true,
        avatar = avatar,
    )
}

private fun account(
    profile: Profile,
    active: Boolean = false,
    authorizationScope: AuthorizationScope = AuthorizationScope.CATEGORY,
) = Account(
    user = AccountUser(USER_ID, "alice", "user"),
    session = AccountSession(
        id = SESSION_ID,
        deviceId = DEVICE_ID,
        activeProfile = if (active) ActiveProfileGrant(profile.id, "2099-01-01T00:00:00Z") else null,
        authorizationScope = authorizationScope,
        category = profile.category.takeIf { authorizationScope == AuthorizationScope.CATEGORY },
    ),
    profiles = listOf(profile),
    maintenance = MaintenanceSettings(false, null),
)

private fun collection(folder: CollectionFolder? = null) = Collection(
    id = COLLECTION_ID,
    title = "Home",
    backdropImageUrl = null,
    heroEnabled = true,
    pinToTop = true,
    focusGlowEnabled = true,

    viewMode = CollectionViewMode.ROWS,
    folderCoverShape = CollectionTileShape.POSTER,
    folders = listOfNotNull(folder),
    profileIds = listOf(PROFILE_ID),
    categoryIds = listOf(CATEGORY_ID),
    position = 0,
    version = 1,
    createdAt = "2026-08-12T00:00:00Z",
    updatedAt = "2026-08-12T00:00:00Z",
)
private fun series(id: UUID, imdbId: String? = null) = io.rivune.api.Series(
    id = id,
    mediaType = io.rivune.api.MediaType.SERIES,
    name = "Series",
    originalName = "Series",
    originalLanguage = "en",
    overview = "Overview",
    genres = emptyList(),
    cast = emptyList(),
    voteAverage = 8.0,
    voteCount = 10,
    seasons = listOf(seasonSummary(id, "season-1", 1, 2)),
    aliases = emptyList(),
    episodeOrders = emptyList(),
    mappingProvider = io.rivune.api.SeriesMappingProvider.TMDB,
    externalIds = mapOf("tmdb" to "42") + listOfNotNull(imdbId?.let { "imdb" to it }).toMap(),
)

private fun seasonSummary(seriesId: UUID, id: String, number: Int, episodeCount: Int) =
    io.rivune.api.SeasonSummary(
        id = id,
        mediaType = io.rivune.api.MediaType.SEASON,
        seriesId = seriesId,
        name = "Season $number",
        overview = "",
        seasonNumber = number,
        episodeCount = episodeCount,
        voteAverage = 8.0,
        externalIds = emptyMap(),
    )

private fun season(
    seriesId: UUID,
    episodes: List<io.rivune.api.Episode>,
    id: String = "season-1",
    number: Int = 1,
) = io.rivune.api.Season(
    id = id,
    mediaType = io.rivune.api.MediaType.SEASON,
    seriesId = seriesId,
    name = "Season $number",
    overview = "",
    seasonNumber = number,
    voteAverage = 8.0,
    episodes = episodes,
    externalIds = emptyMap(),
)

private fun episode(
    id: UUID,
    seriesId: UUID,
    number: Int,
    seasonId: String = "season-1",
    seasonNumber: Int = 1,
) = io.rivune.api.Episode(
    id = id,
    mediaType = io.rivune.api.MediaType.EPISODE,
    seasonId = seasonId,
    name = "Episode $number",
    overview = "",
    seasonNumber = seasonNumber,
    episodeNumber = number,
    voteAverage = 8.0,
    voteCount = 10,
    externalIds = mapOf("tmdb" to "${seriesId}:$number"),
)

private fun folder() = CollectionFolder(
    id = FOLDER_ID,
    title = "Featured",
    tileShape = CollectionTileShape.POSTER,
    sourceView = CollectionSourceView.MERGED,
    coverImageUrl = "/api/v1/artwork/featured",
    coverEmoji = null,
    titleLogoUrl = null,
    heroBackdropUrl = null,
    heroVideoUrl = null,
    focusGifUrl = null,
    focusGifEnabled = false,
    hideTitle = false,
    sources = emptyList(),
)

private fun mediaItem(id: String, title: String) = CollectionItem(
    id = id,
    mediaType = "movie",
    title = title,
    posterUrl = "/api/v1/artwork/$id",
    externalIds = emptyMap(),
    sources = emptyList(),
)

private fun resolvedFolder(
    folder: CollectionFolder,
    page: Int,
    hasMore: Boolean,
    items: List<CollectionItem>,
) = ResolvedCollectionFolder(
    collectionId = COLLECTION_ID,
    folder = folder,
    sourcePosterUrls = null,
    items = items,
    page = page,
    hasMore = hasMore,
    errors = emptyList(),
)

private fun fakePlaybackRoom(
    item: io.rivune.api.CoordinatedPlaybackItem,
    state: String,
    positionMilliseconds: Long,
    durationMilliseconds: Long,
    id: UUID = UUID.randomUUID(),
) = io.rivune.api.PlaybackRoom(
    id = id,
    item = item,
    state = state,
    positionMilliseconds = positionMilliseconds,
    durationMilliseconds = durationMilliseconds,
    version = 1,
    updatedAt = "2099-01-01T00:00:00Z",
    expiresAt = "2099-01-01T01:00:00Z",
    members = emptyList(),
)

private val USER_ID = UUID.fromString("11111111-1111-4111-8111-111111111111")
private val SESSION_ID = UUID.fromString("22222222-2222-4222-8222-222222222222")
private val DEVICE_ID = UUID.fromString("33333333-3333-4333-8333-333333333333")
private val PROFILE_ID = UUID.fromString("44444444-4444-4444-8444-444444444444")
private val CATEGORY_ID = UUID.fromString("55555555-5555-4555-8555-555555555555")
private val COLLECTION_ID = UUID.fromString("66666666-6666-4666-8666-666666666666")
private val FOLDER_ID = UUID.fromString("77777777-7777-4777-8777-777777777777")

private fun accessibility(revision: Long = 1, textScale: Int = 100) = io.rivune.api.AccessibilityPreferencesDocument(
    revision,
    io.rivune.api.ReducedMotionPreference.SYSTEM,
    io.rivune.api.HighContrastPreference.SYSTEM,
    textScale,
    io.rivune.api.CaptionsPreference.SYSTEM,
    false,
    io.rivune.api.FocusIndicatorsPreference.STANDARD,
)

private fun failover(attemptCount: Int, maximumAttempts: Int) = io.rivune.api.PlaybackFailoverState(
    UUID.randomUUID(), "opaque-source-reference-01", 0, 0.0, attemptCount, maximumAttempts, 1,
    io.rivune.api.PlaybackFailoverStatus.ACTIVE,
    candidateHealth = listOf(
        io.rivune.api.PlaybackFailoverCandidateHealth(0, io.rivune.api.PlaybackFailoverCandidateStatus.CURRENT),
        io.rivune.api.PlaybackFailoverCandidateHealth(1, io.rivune.api.PlaybackFailoverCandidateStatus.AVAILABLE),
    ),
    expiresAt = "2099-01-01T00:00:00Z",
)

private fun playerPresentation(failover: io.rivune.api.PlaybackFailoverState) = PlayerPresentation(
    UUID.randomUUID().toString(), UUID.randomUUID(), UUID.randomUUID(), "Title", "https://example.invalid/media",
    "movie", "tmdb:42", protocol = "http", container = "mp4", mediaTimeline = null,
    startPositionMs = 0, timelineStartPositionMs = 0, durationSeconds = 0, expectedProgressVersion = 0,
    engine = EmbeddedPlayerEngine.MEDIA3, fallbackAllowed = false, failover = failover,
)

private fun mediaNotification(id: String) = io.rivune.api.MediaNotification(
    id,
    io.rivune.api.MediaNotificationKind.MOVIE_RELEASE,
    UUID.randomUUID(),
    title = "Release",
    availableAt = "2026-08-26T10:00:00Z",
    createdAt = "2026-08-26T09:00:00Z",
)
