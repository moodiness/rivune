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
import java.util.UUID
import kotlinx.coroutines.CoroutineScope
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertContentEquals
import kotlin.test.assertFalse
import kotlin.test.assertIs
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue
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
        viewModels.forEach { it.viewModelScope.cancel() }
        viewModels.clear()
        Dispatchers.resetMain()
    }

    @Test
    fun normalizesRemoteAndLoopbackServerAddresses() {
        assertEquals("https://media.example.com", normalizeServerUrl("media.example.com/"))
        assertEquals("http://localhost:8080", normalizeServerUrl("localhost:8080"))
        assertEquals("http://127.0.0.1:8080", normalizeServerUrl("127.0.0.1:8080/"))
        assertNull(normalizeServerUrl("https://media example.com"))
        assertNull(normalizeServerUrl("  "))
    }

    @Test
    fun rejectsUnsupportedCleartextLoopbackAliases() {
        assertEquals("https://127.0.0.2:8080", normalizeServerUrl("127.0.0.2:8080"))
        assertEquals("https://[::1]:8080", normalizeServerUrl("[::1]:8080"))
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
        assertEquals("https://media.example.com", store.value)
        assertEquals("Family server", viewModel.state.value.serverName)
        assertFalse(viewModel.state.value.isBusy)

        gateway.pairingPending = false
        advanceTimeBy(1_000)
        runCurrent()
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
                subtitleLanguage = "en",
            ),
            sources = io.rivune.api.EffectiveSettingsSources(),
        )
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()

        viewModel.openProfilePreferences()
        advanceUntilIdle()
        val loaded = assertNotNull(viewModel.state.value.viewer.preferences)
        assertTrue(loaded.canEdit)
        assertEquals("1080p", loaded.settings?.maximumResolution)

        viewModel.updateProfilePreferences(
            io.rivune.api.ProfileSettingsUpdate(maximumResolution = io.rivune.api.PatchField.Value("2160p")),
        )
        advanceUntilIdle()
        assertEquals(
            listOf(io.rivune.api.ProfileSettingsUpdate(maximumResolution = io.rivune.api.PatchField.Value("2160p"))),
            gateway.profileSettingsUpdates,
        )
        assertEquals("2160p", viewModel.state.value.viewer.preferences?.settings?.maximumResolution)

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
            collections = listOf(collection(folder)),
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
    fun missingFolderArtworkIsResolvedForHome() = runTest(dispatcher) {
        val profile = profile()
        val unresolved = folder().copy(coverImageUrl = null)
        val resolved = unresolved.copy(coverImageUrl = "/api/v1/artwork/resolved-folder")
        val gateway = FakeGateway(
            restored = true,
            account = account(profile, active = true),
            collections = listOf(collection(unresolved)),
            resolvedFolders = mapOf(
                requireNotNull(unresolved.id) to listOf(resolvedFolder(resolved, page = 1, hasMore = false, items = emptyList())),
            ),
        )

        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()

        assertEquals("/api/v1/artwork/resolved-folder", viewModel.state.value.collections.single().folders.single().coverImageUrl)
        assertEquals(listOf(1), gateway.resolvedPages)
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
        gateway.progress = io.rivune.api.PlaybackProgress(targetId, io.rivune.api.PlaybackProgressMediaType.MOVIE, 120, 3600, false, 3, "2026-08-12T00:00:00Z", "2026-08-12T00:00:00Z")
        gateway.libraryPages = mapOf(1 to io.rivune.api.LibraryPage(emptyList(), 1, 1, 0))
        gateway.sourceList = io.rivune.api.PlaybackSourceList(
            listOf(io.rivune.api.PlaybackSourceOption("source", "ref", addonId, manifestId = "addon", streamIndex = 0, name = "Direct", protocol = "http", expiresAt = "2099-01-01T00:00:00Z")),
            emptyList(),
        )
        gateway.preparation = io.rivune.api.PlaybackPreparation("ref", io.rivune.api.PlaybackMode.DIRECT, "http", subtitleCount = 0, expiresAt = "2099-01-01T00:00:00Z")
        gateway.playbackSession = io.rivune.api.PlaybackSession(
            playbackId,
            "source",
            selectedSubtitleId = "subtitle",
            sources = listOf(io.rivune.api.PlaybackSource("source", addonId, "addon", mode = io.rivune.api.PlaybackMode.DIRECT, url = "/stream.m3u8", protocol = "hls", compatible = true)),
            subtitles = listOf(io.rivune.api.PlaybackSubtitle("subtitle", addonId, "addon", language = "fr", url = "/subtitle.vtt")),
            providerErrors = emptyList(),
            expiresAt = "2099-01-01T00:00:00Z",
        )
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway)
        advanceUntilIdle()

        viewModel.openMedia(MediaTarget("tt1234567", "movie", "Film"))
        advanceUntilIdle()
        assertEquals(targetId, viewModel.state.value.viewer.detail?.titleId)
        assertEquals("Film", viewModel.state.value.viewer.detail?.movie?.title)

        viewModel.playMedia()
        advanceUntilIdle()
        assertEquals(playbackId, viewModel.state.value.viewer.player?.sessionId)
        assertEquals(120_000L, viewModel.state.value.viewer.player?.startPositionMs)
        assertEquals("hls", viewModel.state.value.viewer.player?.protocol)
        assertEquals(true, viewModel.state.value.viewer.player?.subtitles?.single()?.selected)

        viewModel.reportPlayerProgress(180, 3600, false)
        advanceUntilIdle()
        assertEquals(180, gateway.progressUpdates.single().positionSeconds)
        assertEquals(4L, viewModel.state.value.viewer.player?.expectedProgressVersion)

        viewModel.closePlayer()
        advanceUntilIdle()
        assertEquals(playbackId, gateway.stoppedPlayback)
        assertEquals(1, gateway.stopPlaybackCalls)

        viewModel.playMedia()
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
        val viewModel = viewModel(FakeServerStore("https://saved.example.com"), gateway, externalPlaybackSupport = support)
        advanceUntilIdle()

        viewModel.openMedia(MediaTarget("tt1234567", "movie", "Film"))
        advanceUntilIdle()
        viewModel.playMedia()
        advanceUntilIdle()

        assertEquals(listOf(EXTERNAL_VIDEO_CAPABILITY, EXTERNAL_MAGNET_CAPABILITY), gateway.lastPlaybackCapabilities?.externalPlayers)
        assertEquals(listOf("org.example.player", "org.example.torrent"), viewModel.state.value.externalPlayers.map { it.packageName })
        assertNotNull(viewModel.state.value.viewer.sourcePicker)
        assertNull(viewModel.state.value.viewer.player)

        viewModel.choosePlaybackSource(source, externalPlayer)
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
        viewModel.choosePlaybackSource(source, externalPlayer)
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
        viewModel.choosePlaybackSource(source, externalPlayer)
        advanceUntilIdle()

        viewModel.externalPlaybackFinished(null)
        viewModel.viewModelScope.cancel()
        advanceUntilIdle()

        assertEquals(lifecyclePlaybackId, gateway.stoppedPlayback)
        assertEquals(3, gateway.stopPlaybackCalls)
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
            listOf(io.rivune.api.PlaybackSourceOption("source", "ref", addonId, manifestId = "addon", streamIndex = 0, name = "Broken", protocol = "http", expiresAt = "2099-01-01T00:00:00Z")),
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
    }

    private fun viewModel(
        store: FakeServerStore,
        gateway: FakeGateway,
        isTv: Boolean = false,
        externalPlaybackSupport: ExternalPlaybackSupport = ExternalPlaybackSupport(),
    ) = RivuneViewModel(
        store,
        RivuneGatewayFactory { gateway },
        isTv,
        "Test device",
        CoroutineScope(dispatcher),
        externalPlaybackSupportProvider = { externalPlaybackSupport },
    ).also(viewModels::add)
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
    private val collections: List<Collection> = emptyList(),
    private val resolvedFolders: Map<UUID, List<ResolvedCollectionFolder>> = emptyMap(),
    private val accountFailure: Throwable? = null,
    private val collectionFailure: Throwable? = null,
    private val selectionFailure: Throwable? = null,
    var pairingPending: Boolean = false,
    private val logoutResult: LogoutResult = LogoutResult(localCredentialsCleared = true, serverSessionClosed = true),
) : RivuneGateway {
    var selectedPin: String? = null
    var exchangeCount = 0
    var clearSelectionCount = 0
    var authorizationPlatform: String? = null
    var loggedOut = false
    val resolvedPages = mutableListOf<Int>()
    var catalogDescriptors = emptyList<io.rivune.api.AddonCatalogDescriptor>()
    var searchPages = emptyMap<Int, io.rivune.api.AddonResourceBatch>()
    var libraryPages = emptyMap<Int, io.rivune.api.LibraryPage>()
    var resolvedTitle: io.rivune.api.TitleReference? = null
    var movieResult: io.rivune.api.Movie? = null
    var seriesResult: io.rivune.api.Series? = null
    var seasons = emptyMap<String, io.rivune.api.Season>()
    var progress: io.rivune.api.PlaybackProgress? = null
    var sourceList: io.rivune.api.PlaybackSourceList? = null
    val playbackEvents = mutableListOf<String>()
    var lastPlaybackCapabilities: io.rivune.api.PlaybackCapabilities? = null
    var preparedForExternalPlayer = false
    var resolvedForExternalPlayer = false
    var preparation: io.rivune.api.PlaybackPreparation? = null
    var playbackSession: io.rivune.api.PlaybackSession? = null
    var calendarEvents = emptyList<io.rivune.api.CalendarEvent>()
    var libraryAdded: UUID? = null
    var libraryRemoved: UUID? = null
    var stoppedPlayback: UUID? = null
    var stopPlaybackCalls = 0
    var progressUpdates = mutableListOf<io.rivune.api.UpdatePlaybackProgressRequest>()
    var progressByTitle = mutableMapOf<UUID, io.rivune.api.PlaybackProgress>()
    val watchedBatchRequests = mutableListOf<List<io.rivune.api.SetWatchedBatchItem>>()
    var watchedBatchFailureAtRequest: Int? = null
    val watchedRequests = mutableListOf<Pair<UUID, Long>>()
    var seasonDelayMillis: Long = 0
    var profileAvatars = emptyMap<UUID, ByteArray>()
    var effectiveSettingsResult = io.rivune.api.EffectiveSettings(
        schemaVersion = 1,
        settings = io.rivune.api.SettingsValues(),
        sources = io.rivune.api.EffectiveSettingsSources(),
    )
    val profileSettingsUpdates = mutableListOf<io.rivune.api.ProfileSettingsUpdate>()

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
    override suspend fun resolveCollectionFolderArtwork(collectionId: UUID, folderId: UUID) =
        resolveCollectionFolder(collectionId, folderId)
    override suspend fun resolveCollectionFolder(collectionId: UUID, folderId: UUID, page: Int?): ResolvedCollectionFolder {
        val requestedPage = page ?: 1
        resolvedPages += requestedPage
        return resolvedFolders.getValue(folderId).first { it.page == requestedPage }
    }
    override suspend fun addonCatalogs() = catalogDescriptors
    override suspend fun searchAddonCatalogs(type: String, search: String, skip: Int?, limit: Int?) = searchPages.getValue(skip ?: 0)
    override suspend fun resolveTitle(input: io.rivune.api.TitleResolveInput) = requireNotNull(resolvedTitle)
    override suspend fun movie(id: UUID) = requireNotNull(movieResult)
    override suspend fun series(id: UUID, mappingProvider: io.rivune.api.SeriesMappingProvider) = requireNotNull(seriesResult)
    override suspend fun season(id: String, mappingProvider: io.rivune.api.SeriesMappingProvider): io.rivune.api.Season {
        if (seasonDelayMillis > 0) delay(seasonDelayMillis)
        return seasons.getValue(id)
    }
    override suspend fun library(mediaType: io.rivune.api.TitleMediaType?, page: Int?, pageSize: Int?) = libraryPages[page ?: 1]
        ?: io.rivune.api.LibraryPage(emptyList(), page ?: 1, page ?: 1, 0)
    override suspend fun addLibraryTitle(titleId: UUID): io.rivune.api.LibraryItem {
        libraryAdded = titleId
        return libraryPages.values.flatMap { it.items }.first { it.titleId == titleId }
    }
    override suspend fun removeLibraryTitle(titleId: UUID) { libraryRemoved = titleId }
    override suspend fun continueWatching(limit: Int?) = io.rivune.api.ContinueWatchingPage(emptyList())
    override suspend fun playbackProgress(titleId: UUID) = progressByTitle[titleId] ?: progress?.takeIf { it.titleId == titleId }
    override suspend fun playbackProgressBatch(titleIds: List<UUID>) = io.rivune.api.PlaybackProgressBatch(
        titleIds.map { titleId -> io.rivune.api.PlaybackProgressBatchItem(titleId, progressByTitle[titleId] ?: progress?.takeIf { it.titleId == titleId }) },
    )
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
            maximumResolution = input.maximumResolution.applyTo(current.maximumResolution),
            preferDirectPlay = input.preferDirectPlay.applyTo(current.preferDirectPlay),
            audioLanguage = input.audioLanguage.applyTo(current.audioLanguage),
            subtitleLanguage = input.subtitleLanguage.applyTo(current.subtitleLanguage),
            transcoding = input.transcoding.applyTo(current.transcoding),
        )
        effectiveSettingsResult = effectiveSettingsResult.copy(settings = updated)
        return io.rivune.api.SettingsLayer(1, updated)
    }
    override suspend fun playbackSources(mediaType: String, resourceId: String, capabilities: io.rivune.api.PlaybackCapabilities, addonId: UUID?): io.rivune.api.PlaybackSourceList {
        lastPlaybackCapabilities = capabilities
        return requireNotNull(sourceList)
    }
    override suspend fun preparePlayback(sourceRef: String, startSeconds: Int?, externalPlayer: Boolean): io.rivune.api.PlaybackPreparation {
        preparedForExternalPlayer = externalPlayer
        return requireNotNull(preparation)
    }
    override suspend fun resolvePlayback(sourceRef: String, titleId: String?, startSeconds: Int?, externalPlayer: Boolean): io.rivune.api.PlaybackSession {
        resolvedForExternalPlayer = externalPlayer
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
    override suspend fun calendar(from: String, to: String, language: String?) = calendarEvents
    override suspend fun beginDeviceAuthorization(deviceName: String, platform: String): DeviceAuthorizationResponse {
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
    override fun resolveArtworkUrl(value: String) = if (value.startsWith("/")) "https://media.example.com$value" else null
    override fun resolveResourceUrl(value: String) = if (value.startsWith("https://")) value else "https://media.example.com$value"
}
private fun <T> io.rivune.api.PatchField<T>.applyTo(current: T?): T? = when (this) {
    io.rivune.api.PatchField.Omitted -> current
    io.rivune.api.PatchField.Null -> null
    is io.rivune.api.PatchField.Value -> value
}

private fun discovery(setupRequired: Boolean = false) = Discovery(
    name = "Family server",
    serverVersion = "20.0.0",
    protocolVersion = 20,
    apiBaseUrl = "/api/v1",
    setupRequired = setupRequired,
    setupCompleted = !setupRequired,
    demoAvailable = false,
    timezone = "UTC",
    interfaceLanguage = "fr-FR",
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
private fun series(id: UUID) = io.rivune.api.Series(
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
    seasons = listOf(
        io.rivune.api.SeasonSummary(
            id = "season-1",
            mediaType = io.rivune.api.MediaType.SEASON,
            seriesId = id,
            name = "Season 1",
            overview = "",
            seasonNumber = 1,
            episodeCount = 2,
            voteAverage = 8.0,
            externalIds = emptyMap(),
        ),
    ),
    aliases = emptyList(),
    episodeOrders = emptyList(),
    mappingProvider = io.rivune.api.SeriesMappingProvider.TMDB,
    externalIds = mapOf("tmdb" to "42"),
)

private fun season(seriesId: UUID, episodes: List<io.rivune.api.Episode>) = io.rivune.api.Season(
    id = "season-1",
    mediaType = io.rivune.api.MediaType.SEASON,
    seriesId = seriesId,
    name = "Season 1",
    overview = "",
    seasonNumber = 1,
    voteAverage = 8.0,
    episodes = episodes,
    externalIds = emptyMap(),
)

private fun episode(id: UUID, seriesId: UUID, number: Int) = io.rivune.api.Episode(
    id = id,
    mediaType = io.rivune.api.MediaType.EPISODE,
    seasonId = "season-1",
    name = "Episode $number",
    overview = "",
    seasonNumber = 1,
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

private val USER_ID = UUID.fromString("11111111-1111-4111-8111-111111111111")
private val SESSION_ID = UUID.fromString("22222222-2222-4222-8222-222222222222")
private val DEVICE_ID = UUID.fromString("33333333-3333-4333-8333-333333333333")
private val PROFILE_ID = UUID.fromString("44444444-4444-4444-8444-444444444444")
private val CATEGORY_ID = UUID.fromString("55555555-5555-4555-8555-555555555555")
private val COLLECTION_ID = UUID.fromString("66666666-6666-4666-8666-666666666666")
private val FOLDER_ID = UUID.fromString("77777777-7777-4777-8777-777777777777")
