package io.rivune.app

import android.content.Context
import android.os.Build
import androidx.core.content.edit
import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.viewModelScope
import io.rivune.api.Account
import io.rivune.api.AddonCatalogDescriptor
import io.rivune.api.AddonResourceBatch
import io.rivune.api.AuthorizationScope
import io.rivune.api.CalendarEvent
import io.rivune.api.Collection
import io.rivune.api.CollectionFolder
import io.rivune.api.CollectionItem
import io.rivune.api.ContinueWatchingPage
import io.rivune.api.CredentialStoreException
import io.rivune.api.Discovery
import io.rivune.api.DiscoveryCapability
import io.rivune.api.EffectiveSettings
import io.rivune.api.DeviceAuthorizationResponse
import io.rivune.api.LibraryItem
import io.rivune.api.LibraryPage
import io.rivune.api.Movie
import io.rivune.api.PlaybackCapabilities
import io.rivune.api.PlaybackPreparation
import io.rivune.api.PlaybackProgress
import io.rivune.api.PlaybackProgressMediaType
import io.rivune.api.PlaybackMarkerList
import io.rivune.api.PlaybackProgressBatch
import io.rivune.api.CoordinatedPlaybackItem
import io.rivune.api.LocalRecommendationPage
import io.rivune.api.RecommendationArtworkShape
import io.rivune.api.PlaybackCommandInput
import io.rivune.api.PlaybackCommandMode
import io.rivune.api.PlaybackCommandResultCode
import io.rivune.api.PlaybackCommandResultInput
import io.rivune.api.PlaybackCommandStatus
import io.rivune.api.PlaybackCommandType
import io.rivune.api.PlaybackCommandList
import io.rivune.api.PlaybackDevice
import io.rivune.api.PlaybackDeviceHeartbeatInput
import io.rivune.api.PlaybackDeviceList
import io.rivune.api.PlaybackDeviceState
import io.rivune.api.PlaybackRoom
import io.rivune.api.PlaybackRoomCreateInput
import io.rivune.api.PlaybackRoomUpdateInput
import io.rivune.api.PlaybackSession
import io.rivune.api.ProfileArchiveImportReport
import kotlinx.serialization.json.JsonObject
import io.rivune.api.PlaybackSourceList
import io.rivune.api.Profile
import io.rivune.api.ProfileSettingsUpdate
import io.rivune.api.ProfileSelection
import io.rivune.api.ResolvedCollectionFolder
import io.rivune.api.RivuneApiClient
import io.rivune.api.RivuneApiException
import io.rivune.api.normalizeServerUrl
import io.rivune.api.isKnownLocalNetworkServerUrl
import io.rivune.api.Season
import io.rivune.api.Series
import io.rivune.api.SeriesMappingProvider
import io.rivune.api.TitleMediaType
import io.rivune.api.SetWatchedBatchItem
import io.rivune.api.SetWatchedBatchResult
import io.rivune.api.SettingsLayer
import io.rivune.api.SettingsValues
import io.rivune.api.TitleReference
import io.rivune.api.SemanticSearchPage
import io.rivune.api.SemanticSearchRequest
import io.rivune.api.TitleResolveInput
import io.rivune.api.UpdatePlaybackProgressRequest
import java.io.IOException
import java.time.Instant
import java.time.YearMonth
import java.time.format.DateTimeParseException
import java.util.Locale
import java.util.UUID
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.Job
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.withContext
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.coroutines.sync.withLock

sealed interface AppDestination {
    data object Loading : AppDestination
    data object Server : AppDestination
    data object Pairing : AppDestination
    data object Profiles : AppDestination
    data object Viewer : AppDestination
}


private data class PlayerProgressSnapshot(
    val sessionId: UUID,
    val positionSeconds: Int,
    val durationSeconds: Int,
    val completed: Boolean,
)
internal fun boundedStableSearchTypes(types: List<String>, limit: Int = 16): Pair<List<String>, Boolean> {
    val stable = types.asSequence().map { it.trim().lowercase() }.filter(String::isNotEmpty).distinct().toList()
    return stable.take(limit) to (stable.size > limit)
}

private class SearchFanoutBudget(private val limit: Int) {
    private var used = 0
    @Volatile var truncated: Boolean = false
        private set

    @Synchronized fun tryAcquire(): Boolean {
        if (used >= limit) {
            truncated = true
            return false
        }
        used += 1
        return true
    }
}

private data class HomeResolution(
    val collections: List<Collection>,
    val heroSlides: List<HomeHeroSlide>,
)
private data class SemanticSearchOutcome(
    val page: SemanticSearchPage?,
    val failed: Boolean,
)


private data class EpisodePlaybackContext(
    val nextEpisode: MediaTarget? = null,
    val markerRequest: PlaybackMarkerRequest? = null,
)
data class PairingInfo(
    val userCode: String,
)

internal fun controllablePlaybackDevices(devices: List<PlaybackDevice>): List<PlaybackDevice> =
    devices.filter { !it.current && "remote-control" in it.capabilities }

internal fun offlineStartPositionMs(item: OfflineMediaItem): Long =
    if (item.completed) 0 else item.positionMs.coerceAtLeast(0)
internal fun coordinatedHostRoomState(ending: Boolean, playing: Boolean): String =
    if (ending) "ended" else if (playing) "playing" else "paused"
internal fun shouldPublishHostRoomProgress(ending: Boolean, ended: Boolean): Boolean = !ending && !ended
private val NAMESPACED_ID = Regex("^([a-z0-9._-]+):(.+)$", RegexOption.IGNORE_CASE)
private val CANONICAL_SEARCH_PROVIDERS = setOf("tmdb", "imdb", "tvdb", "trakt")

internal fun searchMediaTargetKey(target: MediaTarget): String =
    "${target.mediaType.lowercase()}:${searchMediaTargetIdentities(target).minOrNull()}"

private fun searchMediaTargetIdentities(target: MediaTarget): Set<String> {
    val identities = target.externalIds.mapNotNullTo(mutableSetOf()) { (provider, value) ->
        value.trim().takeIf(String::isNotEmpty)?.let { "${provider.lowercase()}:${it.lowercase()}" }
    }
    val namespaced = NAMESPACED_ID.matchEntire(target.id.trim())
    val namespace = namespaced?.groupValues?.get(1)?.lowercase()
    if (namespace in CANONICAL_SEARCH_PROVIDERS) {
        identities += "$namespace:${namespaced!!.groupValues[2].lowercase()}"
    } else if (target.id.lowercase().startsWith("tt")) {
        identities += "imdb:${target.id.lowercase()}"
    }
    if (identities.isEmpty()) {
        identities += "${target.sourceAddonId?.toString()?.lowercase() ?: "native"}:" +
            "${target.sourceCatalogId?.lowercase() ?: "none"}:${target.mediaType.lowercase()}:${target.id.lowercase()}"
    }
    return identities
}

enum class UiFailure {
    SERVER_INVALID,
    SERVER_UNREACHABLE,
    LOCAL_NETWORK_PERMISSION,
    PROTOCOL_INCOMPATIBLE,
    SETUP_REQUIRED,
    DEVICE_LIMIT,
    PAIRING_START,
    PAIRING_LIMIT,
    PAIRING_EXPIRED,
    PAIRING_FAILED,
    PROFILE_PIN_INVALID,
    PROFILE_PIN_RATE_LIMITED,
    PROFILE_UNAVAILABLE,
    CONTENT_LOAD,
    PLAYBACK,
    ACTION,
    SESSION_EXPIRED,
    NO_PROFILES,
    LOGOUT_FAILED,
    UNKNOWN,
}

data class RivuneUiState(
    val destination: AppDestination = AppDestination.Loading,
    val serverInput: String = "",
    val serverName: String = "",
    val serverVersion: String? = null,
    val protocolVersion: Int? = null,
    val isTv: Boolean = false,
    val isBusy: Boolean = false,
    val failure: UiFailure? = null,
    val profiles: List<Profile> = emptyList(),
    val profileAvatarData: Map<UUID, ByteArray> = emptyMap(),
    val pendingProfile: Profile? = null,
    val offlineProfiles: List<OfflineProfileGate> = emptyList(),
    val pendingOfflineProfile: OfflineProfileGate? = null,
    val effectiveSettings: EffectiveSettings? = null,
    val activeProfile: Profile? = null,
    val collections: List<Collection> = emptyList(),
    val selectedCollectionId: UUID? = null,
    val openedCollectionId: UUID? = null,
    val resolvedFolder: ResolvedCollectionFolder? = null,
    val pairing: PairingInfo? = null,
    val pairingAccepted: Boolean = false,
    val viewer: ViewerState = ViewerState(),
    val calendarEvents: List<CalendarEvent> = emptyList(),
    val calendarMonth: java.time.YearMonth = java.time.YearMonth.now(),
    val externalPlayers: List<ExternalPlayerApp> = emptyList(),
    val archiveReport: ProfileArchiveImportReport? = null,
    val archiveBusy: Boolean = false,
)
internal data class LogoutResult(
    val localCredentialsCleared: Boolean,
    val serverSessionClosed: Boolean,
)

internal interface RivuneGateway {
    suspend fun discover(): Discovery
    suspend fun clearProfileSelection()
    suspend fun restoreSession(): Boolean
    suspend fun currentAccount(): Account
    suspend fun selectProfile(id: UUID, pin: String?): ProfileSelection
    suspend fun profileAvatar(profileId: UUID): ByteArray
    suspend fun collections(): List<Collection>
    suspend fun resolveCollectionFolder(collectionId: UUID, folderId: UUID, page: Int? = null, language: String? = null): ResolvedCollectionFolder
    suspend fun addonCatalogs(): List<AddonCatalogDescriptor>
    suspend fun resolveCollectionFolderArtwork(collectionId: UUID, folderId: UUID, language: String? = null): ResolvedCollectionFolder
    suspend fun searchAddonCatalogs(type: String, search: String, skip: Int? = null, limit: Int? = null, language: String? = null): AddonResourceBatch
    suspend fun semanticSearch(input: SemanticSearchRequest): SemanticSearchPage
    suspend fun resolveTitle(input: TitleResolveInput): TitleReference
    suspend fun movie(id: UUID, language: String? = null): Movie
    suspend fun series(
        id: UUID,
        mappingProvider: SeriesMappingProvider,
        language: String? = null,
        episodeOrder: String? = null,
    ): Series
    suspend fun season(id: String, mappingProvider: SeriesMappingProvider, language: String? = null): Season
    suspend fun trailers(titleId: UUID, seasonNumber: Int? = null, language: String? = null): List<io.rivune.api.Trailer>
    suspend fun library(mediaType: TitleMediaType? = null, page: Int? = null, pageSize: Int? = null): LibraryPage
    suspend fun addLibraryTitle(titleId: UUID): LibraryItem
    suspend fun removeLibraryTitle(titleId: UUID)
    suspend fun continueWatching(limit: Int? = null): ContinueWatchingPage
    suspend fun playbackProgress(titleId: UUID): PlaybackProgress?
    suspend fun playbackProgressBatch(titleIds: List<UUID>): PlaybackProgressBatch
    suspend fun setTitlesWatchedBatch(items: List<SetWatchedBatchItem>): SetWatchedBatchResult
    suspend fun markTitleWatched(titleId: UUID, expectedVersion: Long): PlaybackProgress
    suspend fun markTitleUnwatched(titleId: UUID, expectedVersion: Long): PlaybackProgress
    suspend fun effectiveProfileSettings(id: UUID): EffectiveSettings
    suspend fun updateProfileSettings(id: UUID, input: ProfileSettingsUpdate): SettingsLayer
    suspend fun exportProfileArchive(profileId: UUID): JsonObject
    suspend fun importProfileArchive(profileId: UUID, archive: JsonObject): ProfileArchiveImportReport
    suspend fun createProfileFromArchive(categoryId: UUID, archive: JsonObject): ProfileArchiveImportReport
    suspend fun playbackSources(mediaType: String, resourceId: String, capabilities: PlaybackCapabilities, addonId: UUID? = null): PlaybackSourceList
    suspend fun preparePlayback(sourceRef: String, startSeconds: Int? = null, externalPlayer: Boolean = false): PlaybackPreparation
    suspend fun playbackMarkers(imdbId: String, season: Int, episode: Int): PlaybackMarkerList
    suspend fun resolvePlayback(sourceRef: String, titleId: String? = null, startSeconds: Int? = null, externalPlayer: Boolean = false): PlaybackSession
    suspend fun resolvePlaybackAccessible(
        sourceRef: String,
        titleId: String?,
        startSeconds: Int?,
        externalPlayer: Boolean,
        preferredAudioTrack: Int?,
    ): PlaybackSession = resolvePlayback(sourceRef, titleId, startSeconds, externalPlayer)
    suspend fun stopPlayback(sessionId: UUID)
    suspend fun updatePlaybackProgress(titleId: UUID, input: UpdatePlaybackProgressRequest): PlaybackProgress
    suspend fun updatePlaybackDevice(input: PlaybackDeviceHeartbeatInput): PlaybackDevice
    suspend fun playbackDevices(): PlaybackDeviceList
    suspend fun sendPlaybackCommand(sessionId: UUID, input: PlaybackCommandInput): io.rivune.api.PlaybackCommand
    suspend fun playbackCommands(after: UUID? = null): PlaybackCommandList
    suspend fun reportPlaybackCommandResult(operationId: UUID, input: PlaybackCommandResultInput): io.rivune.api.PlaybackCommand
    suspend fun outgoingPlaybackCommand(operationId: UUID): io.rivune.api.PlaybackCommand
    suspend fun createPlaybackRoom(input: PlaybackRoomCreateInput): PlaybackRoom
    suspend fun joinPlaybackRoom(code: String): PlaybackRoom
    suspend fun playbackRoom(id: UUID): PlaybackRoom
    suspend fun updatePlaybackRoom(id: UUID, input: PlaybackRoomUpdateInput): PlaybackRoom
    suspend fun leavePlaybackRoom(id: UUID)
    suspend fun localRecommendations(limit: Int = 20, artworkShape: RecommendationArtworkShape? = null): LocalRecommendationPage
    suspend fun calendar(from: String, to: String, language: String? = null): List<CalendarEvent>
    suspend fun beginDeviceAuthorization(installationId: String, deviceName: String, platform: String): DeviceAuthorizationResponse
    suspend fun exchangeDeviceAuthorization(deviceCode: String)
    suspend fun logout(): LogoutResult
    suspend fun readingQueue(profileId: UUID): io.rivune.api.ReadingQueue = unsupportedV22()
    suspend fun addReadingQueueItem(profileId: UUID, input: io.rivune.api.ReadingQueueAddInput): io.rivune.api.ReadingQueueMutation = unsupportedV22()
    suspend fun removeReadingQueueItem(profileId: UUID, itemId: UUID, input: io.rivune.api.ReadingQueueMutationInput): io.rivune.api.ReadingQueueMutation = unsupportedV22()
    suspend fun consumeReadingQueueItem(profileId: UUID, itemId: UUID, input: io.rivune.api.ReadingQueueMutationInput): io.rivune.api.ReadingQueueMutation = unsupportedV22()
    suspend fun createPlaybackFailover(input: io.rivune.api.PlaybackFailoverCreateInput): io.rivune.api.PlaybackFailoverState = unsupportedV22()
    suspend fun playbackFailover(id: UUID): io.rivune.api.PlaybackFailoverState = unsupportedV22()
    suspend fun advancePlaybackFailover(id: UUID, input: io.rivune.api.PlaybackFailoverAdvanceInput): io.rivune.api.PlaybackFailoverState = unsupportedV22()
    suspend fun cancelPlaybackFailover(id: UUID): Unit = unsupportedV22()
    suspend fun savedSearches(): List<io.rivune.api.SavedSearch> = unsupportedV22()
    suspend fun createSavedSearch(input: io.rivune.api.SavedSearchInput): io.rivune.api.SavedSearch = unsupportedV22()
    suspend fun deleteSavedSearch(id: UUID, expectedRevision: Long): Unit = unsupportedV22()
    suspend fun smartCollections(): List<io.rivune.api.SmartCollection> = unsupportedV22()
    suspend fun createSmartCollection(input: io.rivune.api.SmartCollectionInput): io.rivune.api.SmartCollection = unsupportedV22()
    suspend fun deleteSmartCollection(id: UUID, expectedRevision: Long): Unit = unsupportedV22()
    suspend fun evaluateSmartCollection(id: UUID, page: Int? = null, pageSize: Int? = null): io.rivune.api.SmartCollectionPage = unsupportedV22()
    suspend fun extensionIncidents(): List<io.rivune.api.AddonIncident> = unsupportedV22()
    suspend fun acknowledgeExtensionIncident(id: UUID): io.rivune.api.AddonIncident = unsupportedV22()
    suspend fun mediaNotificationSubscriptions(): List<io.rivune.api.MediaNotificationSubscription> = unsupportedV22()
    suspend fun followMediaNotifications(titleId: UUID, input: io.rivune.api.MediaNotificationFollowInput): io.rivune.api.MediaNotificationSubscription = unsupportedV22()
    suspend fun unfollowMediaNotifications(titleId: UUID): Unit = unsupportedV22()
    suspend fun mediaNotifications(cursor: String? = null, limit: Int? = null): io.rivune.api.MediaNotificationPage = unsupportedV22()
    suspend fun acknowledgeMediaNotification(id: String, state: io.rivune.api.MediaNotificationAcknowledgementState): Unit = unsupportedV22()
    suspend fun profileAccessibilityPreferences(profileId: UUID): io.rivune.api.AccessibilityPreferencesDocument = unsupportedV22()
    suspend fun updateProfileAccessibilityPreferences(profileId: UUID, input: io.rivune.api.AccessibilityPreferencesDocument): io.rivune.api.AccessibilityPreferencesDocument = unsupportedV22()
    fun resolveResourceUrl(value: String): String?
    fun resolveArtworkUrl(value: String): String?
}
private fun unsupportedV22(): Nothing = throw UnsupportedOperationException("v22 feature unavailable")
private suspend fun RivuneGateway.canonicalSeries(id: UUID, language: String?): Series {
    val canonicalFailure = try {
        return series(
            id,
            mappingProvider = SeriesMappingProvider.TMDB,
            language = language,
            episodeOrder = null,
        )
    } catch (cause: CancellationException) {
        throw cause
    } catch (cause: Throwable) {
        cause
    }
    val fallback = series(
        id,
        mappingProvider = SeriesMappingProvider.TVDB,
        language = language,
        episodeOrder = null,
    )
    if (fallback.mappingProvider != SeriesMappingProvider.TVDB) return fallback
    val selectedOrderId = fallback.selectedEpisodeOrderId?.trim()?.takeIf(String::isNotBlank)
    val officialOrderId = fallback.episodeOrders.firstOrNull {
        it.type.trim().equals("official", ignoreCase = true)
    }?.id?.trim()?.takeIf(String::isNotBlank)
        ?: throw IllegalStateException("TVDB did not expose an official episode order", canonicalFailure)
    if (selectedOrderId == officialOrderId) return fallback
    val official = series(
        id,
        mappingProvider = SeriesMappingProvider.TVDB,
        language = language,
        episodeOrder = officialOrderId,
    )
    if (
        official.mappingProvider != SeriesMappingProvider.TVDB ||
        official.selectedEpisodeOrderId?.trim() != officialOrderId
    ) {
        throw IllegalStateException("TVDB did not select the requested official episode order", canonicalFailure)
    }
    return official
}

internal fun interface RivuneGatewayFactory {
    fun create(serverUrl: String): RivuneGateway
}

internal interface ServerAddressStore {
    fun load(): String?
    fun save(value: String)
    fun clear()
}

private class DefaultRivuneGateway(
    private val client: RivuneApiClient,
) : RivuneGateway {
    override suspend fun clearProfileSelection() = client.clearProfileSelection()
    override suspend fun discover() = client.discover()
    override suspend fun restoreSession() = client.restoreSession()
    override suspend fun currentAccount() = client.currentAccount()
    override suspend fun selectProfile(id: UUID, pin: String?) = client.selectProfile(id, pin)
    override suspend fun collections() = client.collections()
    override suspend fun profileAvatar(profileId: UUID) = client.profileAvatar(profileId)
    override suspend fun resolveCollectionFolder(collectionId: UUID, folderId: UUID, page: Int?, language: String?) =
        client.resolveCollectionFolder(collectionId, folderId, page = page, language = language)
    override suspend fun addonCatalogs() = client.addonCatalogs()
    override suspend fun resolveCollectionFolderArtwork(collectionId: UUID, folderId: UUID, language: String?) =
        client.resolveCollectionFolderArtwork(collectionId, folderId, language)
    override suspend fun searchAddonCatalogs(type: String, search: String, skip: Int?, limit: Int?, language: String?) =
        client.searchAddonCatalogs(type, search, skip, limit, language = language)
    override suspend fun semanticSearch(input: SemanticSearchRequest) = client.semanticSearch(input)
    override suspend fun resolveTitle(input: TitleResolveInput) = client.resolveTitle(input)
    override suspend fun movie(id: UUID, language: String?) = client.movie(id, language)
    override suspend fun series(id: UUID, mappingProvider: SeriesMappingProvider, language: String?, episodeOrder: String?) =
        client.series(id, language = language, mappingProvider = mappingProvider, episodeOrder = episodeOrder)
    override suspend fun season(id: String, mappingProvider: SeriesMappingProvider, language: String?) =
        client.season(id, language = language, mappingProvider = mappingProvider)
    override suspend fun trailers(titleId: UUID, seasonNumber: Int?, language: String?) =
        client.trailers(titleId, language = language, seasonNumber = seasonNumber).trailers
    override suspend fun library(mediaType: TitleMediaType?, page: Int?, pageSize: Int?) =
        client.library(mediaType, page, pageSize)
    override suspend fun addLibraryTitle(titleId: UUID) = client.addLibraryTitle(titleId)
    override suspend fun removeLibraryTitle(titleId: UUID) = client.removeLibraryTitle(titleId)
    override suspend fun continueWatching(limit: Int?) = client.continueWatching(limit)
    override suspend fun playbackProgress(titleId: UUID) = client.playbackProgress(titleId)
    override suspend fun playbackProgressBatch(titleIds: List<UUID>) = client.playbackProgressBatch(titleIds)
    override suspend fun setTitlesWatchedBatch(items: List<SetWatchedBatchItem>) = client.setTitlesWatchedBatch(items)
    override suspend fun markTitleWatched(titleId: UUID, expectedVersion: Long) =
        client.markTitleWatched(titleId, expectedVersion)
    override suspend fun markTitleUnwatched(titleId: UUID, expectedVersion: Long) =
        client.markTitleUnwatched(titleId, expectedVersion)
    override suspend fun effectiveProfileSettings(id: UUID) = client.effectiveProfileSettings(id)
    override suspend fun updateProfileSettings(id: UUID, input: ProfileSettingsUpdate) = client.updateProfileSettings(id, input)
    override suspend fun playbackSources(mediaType: String, resourceId: String, capabilities: PlaybackCapabilities, addonId: UUID?) =
        client.playbackSources(mediaType, resourceId, capabilities, addonId)
    override suspend fun preparePlayback(sourceRef: String, startSeconds: Int?, externalPlayer: Boolean) =
        client.preparePlayback(sourceRef, startSeconds, externalPlayer)
    override suspend fun playbackMarkers(imdbId: String, season: Int, episode: Int) =
        client.playbackMarkers(imdbId, season, episode)
    override suspend fun resolvePlayback(sourceRef: String, titleId: String?, startSeconds: Int?, externalPlayer: Boolean) =
        client.resolvePlayback(sourceRef, titleId = titleId, startSeconds = startSeconds, externalPlayer = externalPlayer)
    override suspend fun resolvePlaybackAccessible(sourceRef: String, titleId: String?, startSeconds: Int?, externalPlayer: Boolean, preferredAudioTrack: Int?) =
        client.resolvePlayback(sourceRef, titleId = titleId, startSeconds = startSeconds, externalPlayer = externalPlayer, preferredAudioTrack = preferredAudioTrack)
    override suspend fun stopPlayback(sessionId: UUID) = client.stopPlayback(sessionId)
    override suspend fun updatePlaybackProgress(titleId: UUID, input: UpdatePlaybackProgressRequest) =
        client.updatePlaybackProgress(titleId, input)
    override suspend fun exportProfileArchive(profileId: UUID) = client.exportProfileArchive(profileId)
    override suspend fun importProfileArchive(profileId: UUID, archive: JsonObject) = client.importProfileArchive(profileId, archive)
    override suspend fun createProfileFromArchive(categoryId: UUID, archive: JsonObject) = client.createProfileFromArchive(categoryId, archive)
    override suspend fun updatePlaybackDevice(input: PlaybackDeviceHeartbeatInput) = client.updatePlaybackDevice(input)
    override suspend fun playbackDevices() = client.playbackDevices()
    override suspend fun sendPlaybackCommand(sessionId: UUID, input: PlaybackCommandInput) = client.sendPlaybackCommand(sessionId, input)
    override suspend fun playbackCommands(after: UUID?) = client.playbackCommands(after)
    override suspend fun reportPlaybackCommandResult(operationId: UUID, input: PlaybackCommandResultInput) =
        client.reportPlaybackCommandResult(operationId, input)
    override suspend fun outgoingPlaybackCommand(operationId: UUID) = client.outgoingPlaybackCommand(operationId)
    override suspend fun createPlaybackRoom(input: PlaybackRoomCreateInput) = client.createPlaybackRoom(input)
    override suspend fun joinPlaybackRoom(code: String) = client.joinPlaybackRoom(code)
    override suspend fun playbackRoom(id: UUID) = client.playbackRoom(id)
    override suspend fun updatePlaybackRoom(id: UUID, input: PlaybackRoomUpdateInput) = client.updatePlaybackRoom(id, input)
    override suspend fun leavePlaybackRoom(id: UUID) = client.leavePlaybackRoom(id)
    override suspend fun localRecommendations(limit: Int, artworkShape: RecommendationArtworkShape?) = client.localRecommendations(limit, artworkShape)
    override suspend fun calendar(from: String, to: String, language: String?) = client.calendar(from, to, language)
    override suspend fun beginDeviceAuthorization(installationId: String, deviceName: String, platform: String) =
        client.beginDeviceAuthorization(installationId, deviceName, platform)
    override suspend fun exchangeDeviceAuthorization(deviceCode: String) {
        client.exchangeDeviceAuthorization(deviceCode)
    }
    override fun resolveArtworkUrl(value: String) = client.resolveResponseArtworkUrl(value)?.toString()
    override suspend fun logout(): LogoutResult = try {
        client.logout()
        LogoutResult(localCredentialsCleared = true, serverSessionClosed = true)
    } catch (cause: CancellationException) {
        throw cause
    } catch (cause: CredentialStoreException) {
        LogoutResult(
            localCredentialsCleared = false,
            serverSessionClosed = cause.suppressed.isEmpty(),
        )
    } catch (_: Exception) {
        LogoutResult(localCredentialsCleared = true, serverSessionClosed = false)
    }
    override fun resolveResourceUrl(value: String) = client.resolveResponseResourceUrl(value)?.toString()
    override suspend fun readingQueue(profileId: UUID) = client.readingQueue(profileId)
    override suspend fun addReadingQueueItem(profileId: UUID, input: io.rivune.api.ReadingQueueAddInput) = client.addReadingQueueItem(profileId, input)
    override suspend fun removeReadingQueueItem(profileId: UUID, itemId: UUID, input: io.rivune.api.ReadingQueueMutationInput) = client.removeReadingQueueItem(profileId, itemId, input)
    override suspend fun consumeReadingQueueItem(profileId: UUID, itemId: UUID, input: io.rivune.api.ReadingQueueMutationInput) = client.consumeReadingQueueItem(profileId, itemId, input)
    override suspend fun createPlaybackFailover(input: io.rivune.api.PlaybackFailoverCreateInput) = client.createPlaybackFailover(input)
    override suspend fun playbackFailover(id: UUID) = client.playbackFailover(id)
    override suspend fun advancePlaybackFailover(id: UUID, input: io.rivune.api.PlaybackFailoverAdvanceInput) = client.advancePlaybackFailover(id, input)
    override suspend fun cancelPlaybackFailover(id: UUID) = client.cancelPlaybackFailover(id)
    override suspend fun savedSearches() = client.savedSearches()
    override suspend fun createSavedSearch(input: io.rivune.api.SavedSearchInput) = client.createSavedSearch(input)
    override suspend fun deleteSavedSearch(id: UUID, expectedRevision: Long) = client.deleteSavedSearch(id, expectedRevision)
    override suspend fun smartCollections() = client.smartCollections()
    override suspend fun createSmartCollection(input: io.rivune.api.SmartCollectionInput) = client.createSmartCollection(input)
    override suspend fun deleteSmartCollection(id: UUID, expectedRevision: Long) = client.deleteSmartCollection(id, expectedRevision)
    override suspend fun evaluateSmartCollection(id: UUID, page: Int?, pageSize: Int?) = client.evaluateSmartCollection(id, page, pageSize)
    override suspend fun extensionIncidents() = client.extensionIncidents()
    override suspend fun acknowledgeExtensionIncident(id: UUID) = client.acknowledgeExtensionIncident(id)
    override suspend fun mediaNotificationSubscriptions() = client.mediaNotificationSubscriptions()
    override suspend fun followMediaNotifications(titleId: UUID, input: io.rivune.api.MediaNotificationFollowInput) = client.followMediaNotifications(titleId, input)
    override suspend fun unfollowMediaNotifications(titleId: UUID) = client.unfollowMediaNotifications(titleId)
    override suspend fun mediaNotifications(cursor: String?, limit: Int?) = client.mediaNotifications(cursor, limit)
    override suspend fun acknowledgeMediaNotification(id: String, state: io.rivune.api.MediaNotificationAcknowledgementState) = client.acknowledgeMediaNotification(id, state)
    override suspend fun profileAccessibilityPreferences(profileId: UUID) = client.profileAccessibilityPreferences(profileId)
    override suspend fun updateProfileAccessibilityPreferences(profileId: UUID, input: io.rivune.api.AccessibilityPreferencesDocument) = client.updateProfileAccessibilityPreferences(profileId, input)
}

private class PreferencesServerAddressStore(context: Context) : ServerAddressStore {
    private val preferences = context.getSharedPreferences(PREFERENCES_NAME, Context.MODE_PRIVATE)

    override fun load(): String? = preferences.getString(SERVER_URL_KEY, null)?.takeIf(String::isNotBlank)
    override fun save(value: String) = preferences.edit { putString(SERVER_URL_KEY, value) }
    override fun clear() = preferences.edit { remove(SERVER_URL_KEY) }

    private companion object {
        const val PREFERENCES_NAME = "rivune_app"
        const val SERVER_URL_KEY = "server_url"
    }
}

private class PreferencesInstallationIdStore(context: Context) {
    private val preferences = context.getSharedPreferences(PREFERENCES_NAME, Context.MODE_PRIVATE)

    fun loadOrCreate(): String = synchronized(lock) {
        preferences.getString(INSTALLATION_ID_KEY, null)?.let { stored ->
            runCatching { UUID.fromString(stored).toString() }.getOrNull()
        } ?: UUID.randomUUID().toString().also {
            preferences.edit().putString(INSTALLATION_ID_KEY, it).commit()
        }
    }


    private companion object {
        val lock = Any()
        const val PREFERENCES_NAME = "rivune_app"
        const val INSTALLATION_ID_KEY = "installation_id"
    }
}
internal data class StoredPlaybackOperation(
    val status: PlaybackCommandStatus?,
    val code: PlaybackCommandResultCode?,
)
internal interface PlaybackOperationStore {
    fun get(operationId: UUID): StoredPlaybackOperation?
    fun put(operationId: UUID, operation: StoredPlaybackOperation)
}
internal class MemoryPlaybackOperationStore : PlaybackOperationStore {
    private val values = LinkedHashMap<UUID, StoredPlaybackOperation>()
    override fun get(operationId: UUID): StoredPlaybackOperation? = synchronized(values) { values[operationId] }
    override fun put(operationId: UUID, operation: StoredPlaybackOperation) = synchronized(values) {
        values.remove(operationId)
        values[operationId] = operation
        while (values.size > MAX_PLAYBACK_OPERATION_RECORDS) values.remove(values.keys.first())
    }
}

internal class PreferencesPlaybackOperationStore(context: Context) : PlaybackOperationStore {
    private val preferences = context.getSharedPreferences("playback_operations_v22", Context.MODE_PRIVATE)

    override fun get(operationId: UUID): StoredPlaybackOperation? = synchronized(this) {
        read().firstOrNull { it.first == operationId }?.second
    }

    override fun put(operationId: UUID, operation: StoredPlaybackOperation) = synchronized(this) {
        val records = read().filterNot { it.first == operationId }.toMutableList()
        records += operationId to operation
        while (records.size > MAX_PLAYBACK_OPERATION_RECORDS) records.removeAt(0)
        check(preferences.edit().putStringSet("records", records.mapTo(linkedSetOf()) { (id, value) ->
            listOf(id, value.status?.name.orEmpty(), value.code?.name.orEmpty()).joinToString("|")
        }).commit()) { "Could not persist playback operation result" }
    }

    private fun read(): List<Pair<UUID, StoredPlaybackOperation>> = preferences.getStringSet("records", emptySet()).orEmpty()
        .mapNotNull { encoded ->
            val parts = encoded.split('|')
            if (parts.size != 3) return@mapNotNull null
            val id = runCatching { UUID.fromString(parts[0]) }.getOrNull() ?: return@mapNotNull null
            val status = parts[1].takeIf(String::isNotEmpty)?.let { runCatching { PlaybackCommandStatus.valueOf(it) }.getOrNull() }
            val code = parts[2].takeIf(String::isNotEmpty)?.let { runCatching { PlaybackCommandResultCode.valueOf(it) }.getOrNull() }
            id to StoredPlaybackOperation(status, code)
        }
        .sortedBy { it.first.toString() }
}

private const val MAX_PLAYBACK_OPERATION_RECORDS = 256

class RivuneViewModel internal constructor(
    private val serverStore: ServerAddressStore,
    private val gatewayFactory: RivuneGatewayFactory,
    private val tvDevice: Boolean,
    private val deviceName: String,
    private val terminalCleanupScope: CoroutineScope = CoroutineScope(SupervisorJob() + Dispatchers.IO),
    private val externalPlaybackSupportProvider: () -> ExternalPlaybackSupport = { ExternalPlaybackSupport() },
    private val appPreferences: AppPreferencesReader = AppPreferencesReader { AppPreferencesState() },
    private val playbackNetworkProvider: () -> NetworkClass = { NetworkClass.REMOTE_WIFI },
    private val serverConnectionAllowed: (String) -> Boolean = { true },
    private val localeProvider: () -> Locale = Locale::getDefault,
    private val diagnostics: DiagnosticsBuffer = DiagnosticsBuffer(),
    private val instantNow: () -> Instant = Instant::now,
    private val offlineMediaStore: OfflineMediaStore? = null,
    private val awaitCoordinationTick: suspend (Long) -> Unit = { delay(it) },
    private val monotonicNowMilliseconds: () -> Long = { System.nanoTime() / 1_000_000L },
    private val installationId: String = UUID.randomUUID().toString(),
    private val playbackOperationStore: PlaybackOperationStore = MemoryPlaybackOperationStore(),
) : ViewModel() {
    private var externalPlaybackSupport = runCatching(externalPlaybackSupportProvider).getOrDefault(ExternalPlaybackSupport())
    private val mutableState = MutableStateFlow(
        RivuneUiState(isTv = tvDevice, externalPlayers = externalPlaybackSupport.players),
    )
    val state: StateFlow<RivuneUiState> = mutableState.asStateFlow()

    private var gateway: RivuneGateway? = null
    private var generation = 0L
    private var pairingJob: Job? = null

    private var terminalOwnerDestructionPending = false
    private var lastPlayerProgress: PlayerProgressSnapshot? = null
    private var lastPersistedPlayerProgress: PlayerProgressSnapshot? = null
    private var advancingPlayerSessionId: UUID? = null
    private var folderRequestGeneration = 0L
    private var viewerRequestGeneration = 0L
    private var sourceRequestGeneration = 0L
    private var searchJob: Job? = null
    private var searchDescriptors: List<AddonCatalogDescriptor> = emptyList()
    private var metadataRefreshPending = false
    private val roomEndMutex = Mutex()
    private val progressUpdateMutex = Mutex()
    private var playbackCoordinationAvailable = false
    private var localRecommendationsAvailable = false
    private var semanticSearchAvailable = false
    private var coordinationJob: Job? = null
    private var coordinationForeground = true
    private var coordinationRecentUntilMilliseconds = 0L
    private var lastPlaybackOperationId: UUID? = null
    private val pendingOutgoingHandoffs = mutableMapOf<UUID, UUID>()
    private var coordinationPositionMs = 0L
    private var coordinationDurationMs = 0L
    private var coordinationPlaying = false
    private var coordinationEndedSessionId: UUID? = null
    private var coordinationEndingSessionId: UUID? = null
    private var coordinatedPlaybackLoading = false
    private var activeOfflineScope: String? = null

    private var offlineDownloadJob: Job? = null
    init {
        offlineMediaStore?.lock()
        val offlineProfiles = offlineMediaStore?.profiles().orEmpty()
        val remembered = serverStore.load()
        if (remembered == null) {
            mutableState.value = mutableState.value.copy(destination = AppDestination.Server, offlineProfiles = offlineProfiles)
        } else {
            mutableState.value = mutableState.value.copy(serverInput = remembered, offlineProfiles = offlineProfiles)
            connect(remembered)
        }
    }

    fun refreshExternalPlaybackSupport() {
        val refreshed = runCatching(externalPlaybackSupportProvider).getOrDefault(ExternalPlaybackSupport())
        externalPlaybackSupport = refreshed
        if (mutableState.value.externalPlayers != refreshed.players) {
            mutableState.value = mutableState.value.copy(externalPlayers = refreshed.players)
        }
    }

    fun connect(rawServerUrl: String) {
        if (mutableState.value.isBusy) return
        val normalized = normalizeServerUrl(rawServerUrl)
        if (normalized == null) {
            mutableState.value = mutableState.value.copy(
                destination = AppDestination.Server,
                serverInput = rawServerUrl.trim(),
                failure = UiFailure.SERVER_INVALID,
            )
            diagnostics.record(DiagnosticEventCode.SERVER_CONNECTION_FAILED)
            return
        }
        if (!serverConnectionAllowed(normalized)) {
            mutableState.value = mutableState.value.copy(
                destination = AppDestination.Server,
                serverInput = normalized,
                isBusy = false,
                failure = UiFailure.LOCAL_NETWORK_PERMISSION,
            )
            return
        }
        diagnostics.record(DiagnosticEventCode.SERVER_CONNECTION_STARTED)
        closeOfflineScope()
        metadataRefreshPending = false

        generation += 1
        val operationGeneration = generation
        searchJob?.cancel()
        searchJob = null
        pairingJob?.cancel()
        pairingJob = null
        stopCoordination()
        playbackCoordinationAvailable = false
        localRecommendationsAvailable = false
        semanticSearchAvailable = false
        val connectingDestination = if (mutableState.value.destination == AppDestination.Server) {
            AppDestination.Server
        } else {
            AppDestination.Loading
        }
        mutableState.value = mutableState.value.copy(
            destination = connectingDestination,
            serverInput = normalized,
            serverName = "",
            serverVersion = null,
            protocolVersion = null,
            isBusy = true,
            failure = null,
            profiles = emptyList(),
            profileAvatarData = emptyMap(),
            pendingProfile = null,
            effectiveSettings = null,
            activeProfile = null,
            collections = emptyList(),
            selectedCollectionId = null,
            openedCollectionId = null,
            resolvedFolder = null,
            viewer = ViewerState(),
            calendarEvents = emptyList(),
            pairing = null,
            pairingAccepted = false,
        )

        viewModelScope.launch {
            try {
                val candidate = gatewayFactory.create(normalized)
                val discovery = candidate.discover()
                if (discovery.setupRequired) {
                    ifCurrent(operationGeneration) {
                        gateway = null
                        serverStore.clear()
                        mutableState.value = mutableState.value.copy(
                            destination = AppDestination.Server,
                            serverName = discovery.name,
                            isBusy = false,
                            failure = UiFailure.SETUP_REQUIRED,
                        )
                        diagnostics.record(DiagnosticEventCode.SERVER_CONNECTION_FAILED)
                    }
                    return@launch
                }
                if (!isCurrent(operationGeneration)) return@launch
                gateway = candidate
                playbackCoordinationAvailable = discovery.supportsCapability(DiscoveryCapability.PLAYBACK_COORDINATION) &&
                    discovery.supportsCapability(DiscoveryCapability.PLAYBACK_COMMAND_RESULTS)
                localRecommendationsAvailable = "local-recommendations" in discovery.capabilities
                semanticSearchAvailable = discovery.supportsCapability(DiscoveryCapability.SEMANTIC_SEARCH)
                serverStore.save(normalized)
                mutableState.value = mutableState.value.copy(
                    serverName = discovery.name,
                    serverVersion = discovery.serverVersion,
                    protocolVersion = discovery.protocolVersion,
                )
                diagnostics.record(DiagnosticEventCode.SERVER_CONNECTION_SUCCEEDED)
                if (candidate.restoreSession()) {
                    try {
                        routeAuthenticated(candidate, operationGeneration)
                    } catch (cause: Throwable) {
                        if (cause is CancellationException) throw cause
                        if (failureFor(cause, UiFailure.UNKNOWN) != UiFailure.SESSION_EXPIRED) throw cause
                        handleSessionExpired(operationGeneration)
                    }
                } else {
                    launchPairing(operationGeneration)
                }
            } catch (cause: CancellationException) {
                throw cause
            } catch (cause: Throwable) {
                ifCurrent(operationGeneration) {
                    gateway = null
                    mutableState.value = mutableState.value.copy(
                        destination = AppDestination.Server,
                        isBusy = false,
                        failure = failureFor(cause, UiFailure.SERVER_UNREACHABLE),
                    )
                    diagnostics.record(DiagnosticEventCode.SERVER_CONNECTION_FAILED)
                }
            }
        }
    }


    fun startPairing() {
        if (gateway == null) return
        launchPairing(generation)
    }

    fun disconnectServer() {
        if (mutableState.value.isBusy) return
        val currentGateway = gateway
        if (currentGateway == null) {
            closeOfflineScope()
            mutableState.value = RivuneUiState(
                destination = AppDestination.Server,
                isTv = tvDevice,
                offlineProfiles = offlineMediaStore?.profiles().orEmpty(),
            )
            return
        }
        generation += 1
        searchJob?.cancel()
        searchJob = null
        val operationGeneration = generation
        folderRequestGeneration += 1
        viewerRequestGeneration += 1
        pairingJob?.cancel()
        pairingJob = null
        stopCoordination()
        closeOfflineScope()
        mutableState.value = mutableState.value.copy(isBusy = true, failure = null)
        viewModelScope.launch {
            val result = currentGateway.logout()
            if (!isCurrent(operationGeneration)) return@launch
            if (!result.localCredentialsCleared) {
                mutableState.value = mutableState.value.copy(
                    isBusy = false,
                    failure = UiFailure.LOGOUT_FAILED,
                )
                return@launch
            }
            gateway = null
            searchDescriptors = emptyList()
            semanticSearchAvailable = false
            serverStore.clear()
            mutableState.value = RivuneUiState(
                destination = AppDestination.Server,
                isTv = tvDevice,
                failure = if (result.serverSessionClosed) null else UiFailure.LOGOUT_FAILED,
                offlineProfiles = offlineMediaStore?.profiles().orEmpty(),
            )
        }
    }

    fun selectProfile(profile: Profile) {
        if (mutableState.value.isBusy || !profile.accessible) return
        if (profile.hasPin) {
            mutableState.value = mutableState.value.copy(pendingProfile = profile, failure = null)
            return
        }
        performProfileSelection(profile, null)
    }

    fun selectOfflineProfile(profile: OfflineProfileGate) {
        val store = offlineMediaStore ?: return
        if (store.profileGate(profile.scope) != profile) return
        if (profile.hasPin) {
            mutableState.value = mutableState.value.copy(pendingOfflineProfile = profile, failure = null)
        } else {
            unlockOfflineProfile(profile, null)
        }
    }

    fun submitPin(pin: String) {
        val normalized = pin.filter(Char::isDigit)
        if (normalized.length !in 4..8) {
            mutableState.value = mutableState.value.copy(failure = UiFailure.PROFILE_PIN_INVALID)
            return
        }
        mutableState.value.pendingOfflineProfile?.let { offline ->
            unlockOfflineProfile(offline, normalized)
            return
        }
        val profile = mutableState.value.pendingProfile ?: return
        performProfileSelection(profile, normalized)
    }

    fun dismissPin() {
        if (mutableState.value.isBusy) return
        mutableState.value = mutableState.value.copy(pendingProfile = null, pendingOfflineProfile = null, failure = null)
    }

    private fun unlockOfflineProfile(profile: OfflineProfileGate, pin: String?) {
        val store = offlineMediaStore ?: return
        if (!store.unlock(profile.scope, pin)) {
            mutableState.value = mutableState.value.copy(failure = UiFailure.PROFILE_PIN_INVALID)
            return
        }
        activeOfflineScope = profile.scope
        val items = runCatching { store.items(profile.scope) }.getOrDefault(emptyList())
        if (mutableState.value.activeProfile != null) {
            mutableState.value = mutableState.value.copy(
                pendingOfflineProfile = null,
                failure = null,
                viewer = mutableState.value.viewer.copy(offlineItems = items),
            )
        } else {
            mutableState.value = mutableState.value.copy(
                destination = AppDestination.Viewer,
                activeProfile = null,
                pendingOfflineProfile = null,
                failure = null,
                isBusy = false,
                viewer = ViewerState(offlineItems = items),
            )
        }
    }

    private fun closeOfflineScope() {
        activeOfflineScope = null
        offlineMediaStore?.lock()
    }

    fun selectCollection(id: UUID) {
        folderRequestGeneration += 1
        val collection = mutableState.value.collections.firstOrNull { it.id == id } ?: return
        val onlyFolder = collection.folders.singleOrNull()

        if (onlyFolder?.id != null) {
            openFolder(id, onlyFolder)
            return
        }
        mutableState.value = mutableState.value.copy(
            selectedCollectionId = id,
            openedCollectionId = id,
            resolvedFolder = null,
            failure = null,
        )
    }
    fun lockOfflineAccessOnBackground() {
        val scope = activeOfflineScope ?: return
        val store = offlineMediaStore ?: return
        val gate = store.profileGate(scope) ?: return
        if (!gate.hasPin) return
        if (mutableState.value.activeProfile != null) {
            if (mutableState.value.viewer.player?.mediaType == "offline") closePlayer()
            closeOfflineScope()
            mutableState.update { state ->
                state.copy(
                    pendingOfflineProfile = gate,
                    failure = null,
                    viewer = state.viewer.copy(
                        offlineItems = emptyList(),
                        offlineDownloadActive = false,
                        offlineDownloadBytes = 0,
                    ),
                )
            }
            return
        }
        if (mutableState.value.viewer.player?.mediaType == "offline") closePlayer()
        generation += 1
        viewerRequestGeneration += 1
        sourceRequestGeneration += 1
        folderRequestGeneration += 1
        pairingJob?.cancel()
        pairingJob = null
        stopCoordination()
        closeOfflineScope()
        mutableState.value = mutableState.value.copy(
            destination = AppDestination.Server,
            offlineProfiles = store.profiles(),
            pendingOfflineProfile = null,
            viewer = ViewerState(),
            failure = null,
            isBusy = false,
        )
    }

    fun closeCollection() {
        folderRequestGeneration += 1
        mutableState.value = mutableState.value.copy(
            openedCollectionId = null,
            resolvedFolder = null,
            isBusy = false,
            failure = null,
        )
    }
    fun openFolder(collectionId: UUID, folder: CollectionFolder) {
        val folderId = folder.id
        val knownFolder = mutableState.value.collections
            .firstOrNull { it.id == collectionId }
            ?.folders
            ?.any { it.id == folderId } == true
        if (folderId == null || !knownFolder || mutableState.value.isBusy) {
            if (folderId == null) mutableState.value = mutableState.value.copy(failure = UiFailure.CONTENT_LOAD)
            return
        }
        folderRequestGeneration += 1
        loadResolvedFolder(collectionId, folderId, page = 1, append = false, folderRequestGeneration)
    }

    fun closeFolder() {
        folderRequestGeneration += 1
        mutableState.value = mutableState.value.copy(
            resolvedFolder = null,
            isBusy = false,
            failure = null,
        )
    }

    fun loadMoreFolderItems() {
        val current = mutableState.value.resolvedFolder ?: return
        val folderId = current.folder.id ?: return
        if (mutableState.value.isBusy || !current.hasMore) return
        folderRequestGeneration += 1
        loadResolvedFolder(
            collectionId = current.collectionId,
            folderId = folderId,
            page = current.page + 1,
            append = true,
            requestGeneration = folderRequestGeneration,
        )
    }

    fun refresh() {
        if (mutableState.value.isBusy) return
        val currentGateway = gateway ?: return
        val operationGeneration = generation
        when (mutableState.value.destination) {
            AppDestination.Viewer -> {
                val resolved = mutableState.value.resolvedFolder
                val folderId = resolved?.folder?.id
                if (resolved != null && folderId != null) {
                    folderRequestGeneration += 1
                    loadResolvedFolder(
                        collectionId = resolved.collectionId,
                        folderId = folderId,
                        page = 1,
                        append = false,
                        requestGeneration = folderRequestGeneration,
                    )
                } else {
                    loadCollections(currentGateway, operationGeneration, mutableState.value.activeProfile)
                }
            }
            AppDestination.Profiles -> viewModelScope.launch {
                mutableState.value = mutableState.value.copy(isBusy = true, failure = null)
                try {
                    routeAuthenticated(currentGateway, operationGeneration, honorActiveProfile = false)
                } catch (cause: CancellationException) {
                    throw cause
                } catch (cause: Throwable) {
                    if (!isCurrent(operationGeneration)) return@launch
                    val failure = failureFor(cause, UiFailure.UNKNOWN)
                    if (failure == UiFailure.SESSION_EXPIRED) {
                        handleSessionExpired(operationGeneration)
                    } else {
                        mutableState.value = mutableState.value.copy(
                            destination = AppDestination.Profiles,
                            isBusy = false,
                            failure = failure,
                        )
                    }
                }
            }
            AppDestination.Pairing -> startPairing()
            AppDestination.Server -> connect(mutableState.value.serverInput)
            else -> Unit
        }
    }

    fun changeProfile() {
        if (mutableState.value.isBusy) return
        val currentGateway = gateway ?: return
        val operationGeneration = generation
        viewerRequestGeneration += 1
        folderRequestGeneration += 1
        stopCoordination()
        mutableState.value = mutableState.value.copy(isBusy = true, failure = null)
        viewModelScope.launch {
            try {
                currentGateway.clearProfileSelection()
                ifCurrent(operationGeneration) {
                    mutableState.value = mutableState.value.copy(
                        destination = AppDestination.Profiles,
                        pendingProfile = null,
                        effectiveSettings = null,
                        activeProfile = null,
                        collections = emptyList(),
                        selectedCollectionId = null,
                        openedCollectionId = null,
                        isBusy = false,
                        resolvedFolder = null,
                        failure = null,
                        viewer = ViewerState(),
                        calendarEvents = emptyList(),
                    )
                }
            } catch (cause: CancellationException) {
                throw cause
            } catch (cause: Throwable) {
                if (!isCurrent(operationGeneration)) return@launch
                val failure = failureFor(cause, UiFailure.PROFILE_UNAVAILABLE)
                if (failure == UiFailure.SESSION_EXPIRED) {
                    handleSessionExpired(operationGeneration)
                } else {
                    mutableState.value = mutableState.value.copy(isBusy = false, failure = failure)
                }
            }
        }
    }

    fun logout() {
        if (mutableState.value.isBusy) return
        val currentGateway = gateway ?: return
        generation += 1
        val operationGeneration = generation
        folderRequestGeneration += 1
        pairingJob?.cancel()
        pairingJob = null
        stopCoordination()
        mutableState.value = mutableState.value.copy(isBusy = true, failure = null, pendingProfile = null)
        viewModelScope.launch {
            val result = currentGateway.logout()
            if (!isCurrent(operationGeneration)) return@launch
            if (!result.localCredentialsCleared) {
                mutableState.value = mutableState.value.copy(isBusy = false, failure = UiFailure.LOGOUT_FAILED)
                return@launch
            }
            mutableState.value = mutableState.value.copy(
                destination = AppDestination.Pairing,
                isBusy = false,
                failure = if (result.serverSessionClosed) null else UiFailure.LOGOUT_FAILED,
                profiles = emptyList(),
                profileAvatarData = emptyMap(),
                effectiveSettings = null,
                activeProfile = null,
                collections = emptyList(),
                selectedCollectionId = null,
                openedCollectionId = null,
                resolvedFolder = null,
                viewer = ViewerState(),
                calendarEvents = emptyList(),
                pairing = null,
                pairingAccepted = false,
            )
            launchPairing(operationGeneration, preserveFailure = !result.serverSessionClosed)
        }
    }


    fun clearFailure() {
        mutableState.value = mutableState.value.copy(failure = null)
    }

    fun resourceUrl(value: String?): String? {
        if (value.isNullOrBlank()) return null
        return runCatching { gateway?.resolveResourceUrl(value) }.getOrNull()
    }

    fun artworkUrl(value: String?): String? {
        if (value.isNullOrBlank()) return null
        return runCatching { gateway?.resolveArtworkUrl(value) }.getOrNull()
    }
    fun profileAvatar(profile: Profile?): Any? {
        profile ?: return null
        return if (profile.avatar.kind == "custom") {
            mutableState.value.profileAvatarData[profile.id]
        } else {
            artworkUrl(profile.avatar.url)
        }
    }

    fun openProfilePreferences() {
        val profile = mutableState.value.activeProfile ?: return
        val currentGateway = gateway ?: return
        val operationGeneration = generation
        val requestGeneration = ++viewerRequestGeneration
        mutableState.value = mutableState.value.copy(
            viewer = mutableState.value.viewer.copy(
                preferences = ProfilePreferencesState(
                    effective = mutableState.value.effectiveSettings,
                    canEdit = profile.canManage,
                ),
                detail = null,
                detailHistory = emptyList(),
                sourcePicker = null,
                sourcePickerVisible = false,
                loading = ViewerLoading.PREFERENCES,
                inlineFailure = null,
            ),
        )
        viewModelScope.launch {
            try {
                val previousLanguage = metadataLanguage()
                val effective = currentGateway.effectiveProfileSettings(profile.id)
                if (!viewerRequestCurrent(operationGeneration, requestGeneration)) return@launch
                updateEffectiveSettings(effective)
                mutableState.value = mutableState.value.copy(
                    viewer = mutableState.value.viewer.copy(
                        preferences = ProfilePreferencesState(effective = effective, canEdit = profile.canManage),
                        loading = null,
                        inlineFailure = null,
                    ),
                )
                if (previousLanguage != metadataLanguage(effective.settings)) invalidateMetadataContent()
            } catch (cause: CancellationException) {
                throw cause
            } catch (cause: Throwable) {
                viewerFailure(cause, operationGeneration, requestGeneration, UiFailure.ACTION)
            }
        }
    }

    fun closeProfilePreferences() {
        if (mutableState.value.viewer.preferences == null) return
        viewerRequestGeneration += 1
        mutableState.value = mutableState.value.copy(
            viewer = mutableState.value.viewer.copy(
                preferences = null,
                loading = null,
                inlineFailure = null,
            ),
        )
        if (metadataRefreshPending) refreshMetadataContent()
    }

    fun updateProfilePreferences(input: ProfileSettingsUpdate) {
        val profile = mutableState.value.activeProfile?.takeIf(Profile::canManage) ?: return
        val preferences = mutableState.value.viewer.preferences ?: return
        if (!preferences.canEdit || mutableState.value.viewer.loading == ViewerLoading.PREFERENCES) return
        val currentGateway = gateway ?: return
        val operationGeneration = generation
        val requestGeneration = viewerRequestGeneration
        mutableState.value = mutableState.value.copy(
            viewer = mutableState.value.viewer.copy(loading = ViewerLoading.PREFERENCES, inlineFailure = null),
        )
        viewModelScope.launch {
            try {
                val previousLanguage = metadataLanguage()
                currentGateway.updateProfileSettings(profile.id, input)
                val effective = currentGateway.effectiveProfileSettings(profile.id)
                if (!viewerRequestCurrent(operationGeneration, requestGeneration)) return@launch
                updateEffectiveSettings(effective)
                mutableState.value = mutableState.value.copy(
                    viewer = mutableState.value.viewer.copy(
                        preferences = ProfilePreferencesState(effective = effective, canEdit = true),
                        loading = null,
                        inlineFailure = null,
                    ),
                )
                if (previousLanguage != metadataLanguage(effective.settings)) invalidateMetadataContent()
            } catch (cause: CancellationException) {
                throw cause
            } catch (cause: Throwable) {
                viewerFailure(cause, operationGeneration, requestGeneration, UiFailure.ACTION)
            }
        }
    }


    fun selectViewerTab(tab: ViewerTab) {
        if (mutableState.value.destination != AppDestination.Viewer) return
        viewerRequestGeneration += 1
        mutableState.value = mutableState.value.copy(
            viewer = mutableState.value.viewer.copy(
                selectedTab = tab,
                detail = null,
                detailHistory = emptyList(),
                sourcePicker = null,
                sourcePickerVisible = false,
                preferences = null,
                inlineFailure = null,
            ),
        )
        when (tab) {
            ViewerTab.HOME -> loadHomeContent()
            ViewerTab.SEARCH -> if (searchDescriptors.isEmpty()) loadSearchDescriptors()
            ViewerTab.LIBRARY -> loadLibrary(reset = true)
            ViewerTab.CALENDAR -> loadCalendar()
        }
    }

    fun openCollectionItem(item: CollectionItem) = openMedia(item.toMediaTarget())

    fun openLibraryItem(item: LibraryItem) = openMedia(item.toMediaTarget())

    fun openMedia(target: MediaTarget) = loadMedia(
        requestedTarget = target,
        parentDetail = null,
    )

    fun openAndPlayMedia(target: MediaTarget, startOverrideMs: Long? = null) {
        if (target.mediaType != "series") loadMedia(target, parentDetail = null, playWhenReady = true, startOverrideMs = startOverrideMs)
    }

    private fun openCoordinatedMedia(
        target: MediaTarget,
        startOverrideMs: Long? = null,
        onResult: (Boolean) -> Unit,
    ) {
        if (target.mediaType == "series") {
            onResult(false)
            return
        }
        loadMedia(
            requestedTarget = target,
            parentDetail = null,
            playWhenReady = true,
            startOverrideMs = startOverrideMs,
            forceEmbedded = true,
            onPlaybackResult = onResult,
        )
    }
    private fun beginCoordinatedMedia(target: MediaTarget, startOverrideMs: Long?): CompletableDeferred<Boolean> {
        val result = CompletableDeferred<Boolean>()
        coordinatedPlaybackLoading = true
        openCoordinatedMedia(target, startOverrideMs) { success ->
            if (result.complete(success)) coordinatedPlaybackLoading = false
        }
        return result
    }

    private suspend fun awaitCoordinatedMedia(target: MediaTarget, startOverrideMs: Long?): Boolean =
        beginCoordinatedMedia(target, startOverrideMs).await()

    fun openEpisode(target: MediaTarget) = loadMedia(
        requestedTarget = target,
        parentDetail = mutableState.value.viewer.detail,
    )

    private fun loadMedia(
        requestedTarget: MediaTarget,
        parentDetail: MediaDetailState?,
        playWhenReady: Boolean = false,
        startOverrideMs: Long? = null,
        forceEmbedded: Boolean = false,
        onPlaybackResult: ((Boolean) -> Unit)? = null,
    ) {
        val target = requestedTarget.withAtomicVariantContext()
        if (!target.available) {
            onPlaybackResult?.invoke(false)
            return
        }
        val currentGateway = gateway ?: run {
            onPlaybackResult?.invoke(false)
            return
        }
        val operationGeneration = generation
        val language = metadataLanguage()
        val requestGeneration = ++viewerRequestGeneration
        val detailHistory = if (parentDetail == null) {
            emptyList()
        } else {
            mutableState.value.viewer.detailHistory + parentDetail
        }
        mutableState.value = mutableState.value.copy(
            viewer = mutableState.value.viewer.copy(
                loading = ViewerLoading.DETAIL,
                detail = parentDetail,
                detailHistory = if (parentDetail == null) emptyList() else mutableState.value.viewer.detailHistory,
                sourcePicker = null,
                sourcePickerVisible = false,
                preferences = null,
                inlineFailure = null,
            ),
        )
        viewModelScope.launch {
            try {
                val titleId = resolveTarget(currentGateway, target)
                val canonical = target.copy(titleId = titleId)
                val progress = runCatching { currentGateway.playbackProgress(titleId) }.getOrNull()
                val library = runCatching { currentGateway.library(page = 1, pageSize = 100) }.getOrNull()
                val movie = if (target.mediaType == "movie") runCatching { currentGateway.movie(titleId, language) }.getOrNull() else null
                val series = if (target.mediaType == "series") {
                    if (target.mappingProvider != null) {
                        runCatching {
                            currentGateway.series(
                                titleId,
                                mappingProvider = target.mappingProvider,
                                language = language,
                                episodeOrder = target.episodeOrderId,
                            )
                        }.getOrNull()
                    } else {
                        runCatching { currentGateway.canonicalSeries(titleId, language) }.getOrNull()
                    }
                } else null
                val trailers = if (target.mediaType == "movie" || target.mediaType == "series") {
                    runCatching { currentGateway.trailers(titleId, language = language) }.getOrDefault(emptyList())
                } else {
                    emptyList()
                }
                val episodeSeries = if (target.mediaType == "episode") {
                    target.seriesId?.let { seriesId ->
                        parentDetail?.series?.takeIf {
                            it.id == seriesId &&
                                parentDetail.target.mappingProvider == target.mappingProvider &&
                                parentDetail.target.episodeOrderId == target.episodeOrderId
                        } ?: if (target.mappingProvider != null) {
                            runCatching {
                                currentGateway.series(
                                    seriesId,
                                    mappingProvider = target.mappingProvider,
                                    language = language,
                                    episodeOrder = target.episodeOrderId,
                                )
                            }.getOrNull()
                        } else {
                            runCatching { currentGateway.canonicalSeries(seriesId, language) }.getOrNull()
                        }
                    }
                } else {
                    null
                }
                if (!viewerRequestCurrent(operationGeneration, requestGeneration)) {
                    onPlaybackResult?.invoke(false)
                    return@launch
                }
                val detail = MediaDetailState(
                    target = canonical,
                    titleId = titleId,
                    movie = movie,
                    series = series,
                    cast = movie?.cast ?: series?.cast ?: episodeSeries?.cast ?: parentDetail?.cast.orEmpty(),
                    progress = progress,
                    trailers = trailers,
                    inLibrary = library?.items?.any { it.titleId == titleId } == true,
                )
                mutableState.value = mutableState.value.copy(
                    viewer = mutableState.value.viewer.copy(
                        detail = detail,
                        detailHistory = detailHistory,
                        loading = null,
                        inlineFailure = null,
                    ),
                )
                if (canonical.mediaType != "series") {
                    loadPlaybackSources(
                        target = canonical,
                        titleId = titleId,
                        progress = progress,
                        showPicker = playWhenReady || shouldAutomaticallyShowStreams(canonical),
                        episodeContextDetail = (parentDetail ?: detail).let { context ->
                            if (episodeSeries == null) context else context.copy(series = episodeSeries)
                        },
                        autoStart = playWhenReady,
                        startOverrideMs = startOverrideMs,
                        forceEmbedded = forceEmbedded,
                        onResult = onPlaybackResult,
                    )
                }
            } catch (cause: CancellationException) {
                onPlaybackResult?.invoke(false)
                throw cause
            } catch (cause: Throwable) {
                viewerFailure(cause, operationGeneration, requestGeneration, UiFailure.CONTENT_LOAD)
                onPlaybackResult?.invoke(false)
            }
        }
    }
    private fun shouldAutomaticallyShowStreams(target: MediaTarget): Boolean =
        target.mediaType != "series" && appPreferences.snapshot().automaticallyShowStreams


    fun backViewer() {
        val viewer = mutableState.value.viewer
        when {
            viewer.player != null -> closePlayer()
            viewer.preferences != null -> closeProfilePreferences()
            viewer.sourcePickerVisible && !appPreferences.snapshot().automaticallyShowStreams -> dismissSourcePicker()
            else -> navigateViewerBack(viewer)
        }
    }

    fun backDetail() = navigateViewerBack(mutableState.value.viewer)

    private fun navigateViewerBack(viewer: ViewerState) {
        when {
            viewer.detailHistory.isNotEmpty() -> {
                viewerRequestGeneration += 1
                mutableState.value = mutableState.value.copy(
                    viewer = viewer.copy(
                        detail = viewer.detailHistory.last(),
                        detailHistory = viewer.detailHistory.dropLast(1),
                        sourcePicker = null,
                        sourcePickerVisible = false,
                        loading = null,
                        inlineFailure = null,
                    ),
                )
            }
            viewer.detail?.season != null -> {
                viewerRequestGeneration += 1
                mutableState.value = mutableState.value.copy(
                    viewer = viewer.copy(
                        detail = viewer.detail.copy(season = null, seasonTrailers = emptyList(), episodeProgress = emptyMap()),
                        detailHistory = emptyList(),
                        sourcePicker = null,
                        sourcePickerVisible = false,
                        loading = null,
                        inlineFailure = null,
                    ),
                )
            }
            viewer.detail != null -> {
                viewerRequestGeneration += 1
                mutableState.value = mutableState.value.copy(
                    viewer = viewer.copy(
                        detail = null,
                        detailHistory = emptyList(),
                        sourcePicker = null,
                        sourcePickerVisible = false,
                        loading = null,
                        inlineFailure = null,
                    ),
                )
            }
            mutableState.value.resolvedFolder != null -> closeFolder()
            mutableState.value.openedCollectionId != null -> closeCollection()
            activeOfflineScope != null && mutableState.value.activeProfile == null -> disconnectServer()
        }
    }

    fun search(query: String) {
        val normalized = query.trim()
        searchJob?.cancel()
        searchJob = null
        viewerRequestGeneration += 1
        mutableState.value = mutableState.value.copy(
            viewer = mutableState.value.viewer.copy(
                search = SearchState(query = normalized),
                loading = if (normalized.length >= 2) ViewerLoading.SEARCH else null,
                inlineFailure = null,
            ),
        )
        if (normalized.length < 2) return
        runSearch(normalized, skip = 0, append = false)
    }

    fun loadMoreSearch() {
        val search = mutableState.value.viewer.search
        if (!search.hasMore || mutableState.value.viewer.loading != null) return
        runSearch(search.query, skip = search.page * SEARCH_PAGE_SIZE, append = true)
    }

    fun setLibraryType(mediaType: String?) {
        val normalized = mediaType?.takeIf { it in setOf("movie", "series", "tv") }
        mutableState.value = mutableState.value.copy(
            viewer = mutableState.value.viewer.copy(
                library = mutableState.value.viewer.library.copy(mediaType = normalized),
            ),
        )
        loadLibrary(reset = true)
    }

    fun loadMoreLibrary() {
        val library = mutableState.value.viewer.library
        if (library.page >= library.totalPages || mutableState.value.viewer.loading != null) return
        loadLibrary(reset = false)
    }

    fun selectSeason(seasonId: String) {
        val detail = mutableState.value.viewer.detail ?: return
        val series = detail.series ?: return
        if (detail.season?.id == seasonId) return
        val currentGateway = gateway ?: return
        val requestGeneration = ++viewerRequestGeneration
        val operationGeneration = generation
        val language = metadataLanguage()
        mutableState.value = mutableState.value.copy(
            viewer = mutableState.value.viewer.copy(
                loading = ViewerLoading.SEASON,
                inlineFailure = null,
            ),
        )
        viewModelScope.launch {
            try {
                val season = currentGateway.season(seasonId, series.mappingProvider, language)
                if (!viewerRequestCurrent(operationGeneration, requestGeneration)) return@launch
                mutableState.value = mutableState.value.copy(
                    viewer = mutableState.value.viewer.copy(
                        detail = detail.copy(season = season, seasonTrailers = emptyList(), episodeProgress = emptyMap()),
                    ),
                )
                val seasonTrailers = runCatching {
                    currentGateway.trailers(detail.titleId, season.seasonNumber, language)
                }.getOrDefault(emptyList())
                if (!viewerRequestCurrent(operationGeneration, requestGeneration)) return@launch
                val loadedDetail = mutableState.value.viewer.detail ?: return@launch
                mutableState.value = mutableState.value.copy(
                    viewer = mutableState.value.viewer.copy(
                        detail = loadedDetail.copy(seasonTrailers = seasonTrailers),
                    ),
                )
                val progress = season.episodes
                    .chunked(MAX_WATCHED_BATCH_SIZE)
                    .flatMap { chunk -> currentGateway.playbackProgressBatch(chunk.map { it.id }).items }
                    .mapNotNull { item -> item.progress?.let { item.titleId to it } }
                    .toMap()
                if (!viewerRequestCurrent(operationGeneration, requestGeneration)) return@launch
                val currentDetail = mutableState.value.viewer.detail ?: return@launch
                mutableState.value = mutableState.value.copy(
                    viewer = mutableState.value.viewer.copy(
                        detail = currentDetail.copy(episodeProgress = progress),
                        loading = null,
                        inlineFailure = null,
                    ),
                )
            } catch (cause: CancellationException) {
                throw cause
            } catch (cause: Throwable) {
                viewerFailure(cause, operationGeneration, requestGeneration, UiFailure.CONTENT_LOAD)
            }
        }
    }

    fun playMedia(target: MediaTarget? = null) {
        if (mutableState.value.viewer.loading != null) return
        val detail = mutableState.value.viewer.detail ?: return
        val resolvedTarget = target ?: detail.target
        if (resolvedTarget.mediaType == "series") return
        val titleId = resolvedTarget.titleId ?: detail.titleId.takeIf { resolvedTarget == detail.target } ?: return
        val progress = if (resolvedTarget == detail.target) detail.progress else detail.episodeProgress[titleId]
        val now = instantNow()
        val cached = mutableState.value.viewer.sourcePicker
            ?.takeIf { it.titleId == titleId && it.target.resourceId == resolvedTarget.resourceId }
            ?.takeIf { picker ->
                picker.options.isEmpty() || picker.options.all { sourceReferenceValid(it, now) }
            }
        if (cached != null) {
            mutableState.value = mutableState.value.copy(
                viewer = mutableState.value.viewer.copy(
                    sourcePicker = cached.copy(progress = progress),
                    sourcePickerVisible = true,
                    loading = if (cached.options.isEmpty()) ViewerLoading.SOURCES else null,
                    inlineFailure = null,
                ),
            )
        } else {
            loadPlaybackSources(
                target = resolvedTarget.copy(titleId = titleId),
                titleId = titleId,
                progress = progress,
                showPicker = true,
            )
        }
    }

    fun refreshPlaybackSources() {
        if (mutableState.value.viewer.loading != null) return
        val picker = mutableState.value.viewer.sourcePicker ?: return
        loadPlaybackSources(picker.target, picker.titleId, picker.progress, showPicker = true)
    }

    fun selectPlaybackSource(source: io.rivune.api.PlaybackSourceOption) {
        if (mutableState.value.viewer.loading != null) return
        val picker = mutableState.value.viewer.sourcePicker ?: return
        val selectedSource = picker.options.firstOrNull {
            it.id == source.id && it.sourceRef == source.sourceRef
        } ?: return
        refreshExternalPlaybackSupport()
        val preferences = appPreferences.snapshot()
        when (
            val target = preferredPlaybackTarget(
                preferences.preferredPlayer,
                preferences.embeddedPlayerPreference,
                selectedSource,
                externalPlaybackSupport,
            )
        ) {
            PreferredPlaybackTarget.Ask -> {
                val compatiblePlayers = externalPlaybackSupport.playersFor(
                    selectedSource.mode,
                    selectedSource.protocol,
                    selectedSource.container,
                )
                if (selectedSource.mode == io.rivune.api.PlaybackMode.EXTERNAL && compatiblePlayers.isEmpty()) {
                    mutableState.value = mutableState.value.copy(
                        viewer = mutableState.value.viewer.copy(inlineFailure = UiFailure.PLAYBACK),
                    )
                } else {
                    requestPlaybackTarget(picker, selectedSource)
                }
            }
            is PreferredPlaybackTarget.Embedded -> startPlayback(
                picker,
                selectedSource,
                PlaybackTargetSelection.Embedded(target.preference),
            )
            is PreferredPlaybackTarget.External -> startPlayback(
                picker,
                selectedSource,
                PlaybackTargetSelection.External(target.player),
            )
        }
    }

    internal fun requireOfflineDownloadScope(): String? {
        activeOfflineScope?.let { return it }
        val profile = mutableState.value.activeProfile ?: return null
        val store = offlineMediaStore ?: return null
        val scope = offlineProfileScope(mutableState.value.serverInput, profile.id)
        val gate = store.profileGate(scope)?.takeIf(OfflineProfileGate::hasPin) ?: return null
        mutableState.value = mutableState.value.copy(pendingOfflineProfile = gate, failure = null)
        return null
    }

    fun downloadPlaybackSource(source: io.rivune.api.PlaybackSourceOption) {
        val store = offlineMediaStore ?: return
        val scope = requireOfflineDownloadScope() ?: return
        val picker = mutableState.value.viewer.sourcePicker ?: return
        val selected = picker.options.firstOrNull { it.id == source.id && it.sourceRef == source.sourceRef } ?: return
        if (selected.protocol.lowercase() in setOf("hls", "dash") || offlineDownloadJob?.isActive == true) return
        val preferences = appPreferences.snapshot()
        if (!preferences.downloadOnMobile && runCatching(playbackNetworkProvider).getOrDefault(NetworkClass.MOBILE) == NetworkClass.MOBILE) return
        val currentGateway = gateway ?: return
        mutableState.value = mutableState.value.copy(
            viewer = mutableState.value.viewer.copy(
                offlineDownloadState = OfflineMediaState.QUEUED,
                offlineDownloadActive = true,
                offlineDownloadBytes = 0,
                inlineFailure = null,
            ),
        )
        offlineDownloadJob = viewModelScope.launch {
            var session: PlaybackSession? = null
            try {
                currentGateway.preparePlayback(selected.sourceRef, externalPlayer = true)
                val resolvedSession = currentGateway.resolvePlayback(selected.sourceRef, picker.titleId.toString(), externalPlayer = true)
                session = resolvedSession
                val resolved = resolvedSession.sources.firstOrNull { it.id == resolvedSession.selectedSourceId }
                    ?: resolvedSession.sources.firstOrNull()
                    ?: error("No downloadable source")
                val url = resolved.url?.let(currentGateway::resolveResourceUrl) ?: error("No downloadable source URL")
                val item = store.download(
                    scope, url, picker.titleId, picker.target.title,
                    resolved.container ?: selected.container, picker.target.posterUrl,
                    quotaBytes = preferences.offlineQuotaBytes,
                    expirationDays = preferences.offlineExpirationDays,
                ) { bytes ->
                    if (!preferences.downloadOnMobile && runCatching(playbackNetworkProvider).getOrDefault(NetworkClass.MOBILE) == NetworkClass.MOBILE) {
                        throw CancellationException("Mobile downloads disabled")
                    }
                    if (activeOfflineScope == scope) {
                        mutableState.update { state -> state.copy(viewer = state.viewer.copy(
                            offlineDownloadState = OfflineMediaState.DOWNLOADING,
                            offlineDownloadBytes = bytes,
                        )) }
                    }
                }
                if (activeOfflineScope == scope) {
                    mutableState.update { state ->
                        state.copy(
                            offlineProfiles = store.profiles(),
                            viewer = state.viewer.copy(
                                offlineItems = listOf(item) + state.viewer.offlineItems.filterNot { it.id == item.id },
                                offlineDownloadState = OfflineMediaState.READY,
                                offlineDownloadActive = false,
                            ),
                        )
                    }
                }
            } catch (cause: CancellationException) {
                if (activeOfflineScope == scope) {
                    mutableState.update { state -> state.copy(viewer = state.viewer.copy(
                        offlineDownloadState = OfflineMediaState.FAILED,
                        offlineDownloadActive = false,
                    )) }
                }
                return@launch
            } catch (_: Throwable) {
                if (activeOfflineScope == scope) {
                    mutableState.update { state -> state.copy(viewer = state.viewer.copy(
                        offlineDownloadState = OfflineMediaState.FAILED,
                        offlineDownloadActive = false,
                        inlineFailure = UiFailure.ACTION,
                    )) }
                }
            } finally {
                session?.let { activeSession ->
                    kotlinx.coroutines.withContext(NonCancellable) { runCatching { currentGateway.stopPlayback(activeSession.id) } }
                }
                offlineDownloadJob = null
            }
        }
    }


    fun cancelOfflineDownload() {
        offlineDownloadJob?.cancel()
    }
    fun playOffline(item: OfflineMediaItem) {
        val store = offlineMediaStore ?: return
        val scope = activeOfflineScope ?: return
        if (item !in mutableState.value.viewer.offlineItems) return
        val mediaUrl = runCatching { store.mediaUri(scope, item) }.getOrNull() ?: return
        mutableState.value = mutableState.value.copy(
            viewer = mutableState.value.viewer.copy(
                player = PlayerPresentation(
                    key = "offline:${item.id}", sessionId = item.id, titleId = item.titleId, title = item.title,
                    mediaUrl = mediaUrl, protocol = "http", container = item.container,
                    mediaType = "offline", resourceId = "offline:${item.id}", posterUrl = item.posterUrl,
                    mediaTimeline = null, startPositionMs = offlineStartPositionMs(item), timelineStartPositionMs = 0,
                    durationSeconds = (item.durationMs / 1_000L).coerceAtMost(Int.MAX_VALUE.toLong()).toInt(),
                    expectedProgressVersion = 0, engine = EmbeddedPlayerEngine.MEDIA3, fallbackAllowed = false,
                ),
                sourcePicker = null, sourcePickerVisible = false, inlineFailure = null,
            ),
        )
    }

    fun removeOffline(item: OfflineMediaItem) {
        val store = offlineMediaStore ?: return
        val scope = activeOfflineScope ?: return
        viewModelScope.launch(Dispatchers.IO) {
            val removed = runCatching { store.remove(scope, item) }.getOrDefault(false)
            mutableState.update { state ->
                state.copy(viewer = if (removed) state.viewer.copy(
                    offlineItems = state.viewer.offlineItems.filterNot { it.id == item.id },
                ) else state.viewer.copy(inlineFailure = UiFailure.ACTION))
            }
        }
    }

    fun handoffPlayback(device: PlaybackDevice) = sendLoadOperation(device, PlaybackCommandMode.HANDOFF)

    fun playCopyPlayback(device: PlaybackDevice) = sendLoadOperation(device, PlaybackCommandMode.PLAY_COPY)

    private fun sendLoadOperation(device: PlaybackDevice, mode: PlaybackCommandMode) {
        val currentGateway = gateway ?: return
        val detail = mutableState.value.viewer.detail ?: return
        val operationId = UUID.randomUUID()
        val sourceSessionId = mutableState.value.viewer.player?.sessionId
        val input = PlaybackCommandInput(
            operationId = operationId,
            command = PlaybackCommandType.LOAD,
            item = detail.coordinatedItem(),
            positionMilliseconds = coordinationPositionMs.takeIf { it > 0 }
                ?: (detail.progress?.positionSeconds ?: 0) * 1_000L,
            mode = mode,
            targetRevision = device.revision,
        )
        viewModelScope.launch {
            val sent = runCatching { currentGateway.sendPlaybackCommand(device.sessionId, input) }.getOrNull()
                ?: return@launch
            if (mode == PlaybackCommandMode.HANDOFF && sourceSessionId != null) {
                pendingOutgoingHandoffs[sent.operationId] = sourceSessionId
            }
        }
    }
    fun controlPlayback(device: PlaybackDevice, command: String) {
        val currentGateway = gateway ?: return
        val type = when (command) {
            "play" -> PlaybackCommandType.PLAY
            "pause" -> PlaybackCommandType.PAUSE
            "seek" -> PlaybackCommandType.SEEK
            "stop" -> PlaybackCommandType.STOP
            else -> return
        }
        val input = PlaybackCommandInput(
            operationId = UUID.randomUUID(),
            command = type,
            positionMilliseconds = coordinationPositionMs.takeIf { type == PlaybackCommandType.SEEK },
            targetRevision = device.revision,
        )
        viewModelScope.launch { runCatching { currentGateway.sendPlaybackCommand(device.sessionId, input) } }
    }

    fun createPlaybackRoom() {
        if (!playbackCoordinationAvailable) return
        val currentGateway = gateway ?: return
        val detail = mutableState.value.viewer.detail ?: return
        viewModelScope.launch {
            val room = runCatching { currentGateway.createPlaybackRoom(PlaybackRoomCreateInput(detail.coordinatedItem(), "paused", (detail.progress?.positionSeconds ?: 0) * 1_000L, (detail.progress?.durationSeconds ?: 0) * 1_000L)) }.getOrNull() ?: return@launch
            mutableState.value = mutableState.value.copy(viewer = mutableState.value.viewer.copy(activePlaybackRoom = room))
        }
    }

    fun joinPlaybackRoom(code: String) {
        if (!playbackCoordinationAvailable) return
        val currentGateway = gateway ?: return
        val normalized = code.trim().uppercase().takeIf(String::isNotEmpty) ?: return
        viewModelScope.launch {
            val room = runCatching { currentGateway.joinPlaybackRoom(normalized) }.getOrNull() ?: return@launch
            mutableState.update { state -> state.copy(viewer = state.viewer.copy(activePlaybackRoom = room)) }
            val item = room.item
            val started = awaitCoordinatedMedia(
                MediaTarget(id = item.resourceId, resourceId = item.resourceId, mediaType = item.mediaType, title = item.title, titleId = item.titleId, sourceAddonId = item.sourceAddonId, posterUrl = item.posterUrl),
                room.positionMilliseconds,
            )
            if (!started) {
                mutableState.update { state ->
                    state.copy(viewer = state.viewer.copy(activePlaybackRoom = state.viewer.activePlaybackRoom?.takeUnless { it.id == room.id }))
                }
                runCatching { currentGateway.leavePlaybackRoom(room.id) }
            }
        }
    }
    fun exportActiveProfileArchive(deliver: (JsonObject?) -> Unit) {
        val profile = mutableState.value.activeProfile ?: return
        val currentGateway = gateway ?: return
        if (mutableState.value.archiveBusy) return
        mutableState.update { it.copy(archiveBusy = true, failure = null) }
        viewModelScope.launch {
            val archive = runCatching { currentGateway.exportProfileArchive(profile.id) }.getOrNull()
            mutableState.update { it.copy(archiveBusy = false, failure = if (archive == null) UiFailure.ACTION else null) }
            deliver(archive)
        }
    }

    fun importProfileArchive(archive: JsonObject, create: Boolean) {
        val profile = mutableState.value.activeProfile ?: return
        val currentGateway = gateway ?: return
        if (mutableState.value.archiveBusy) return
        mutableState.update { it.copy(archiveBusy = true, archiveReport = null, failure = null) }
        viewModelScope.launch {
            val result = runCatching {
                if (create) currentGateway.createProfileFromArchive(profile.categoryId, archive)
                else currentGateway.importProfileArchive(profile.id, archive)
            }.getOrNull()
            mutableState.update { it.copy(
                archiveBusy = false,
                archiveReport = result,
                failure = if (result == null) UiFailure.ACTION else null,
            ) }
            if (result != null && create) refresh()
        }
    }

    fun clearArchiveReport() {
        mutableState.update { it.copy(archiveReport = null) }
    }

    fun leavePlaybackRoom() {
        val currentGateway = gateway ?: return
        val room = mutableState.value.viewer.activePlaybackRoom ?: return
        mutableState.value = mutableState.value.copy(viewer = mutableState.value.viewer.copy(activePlaybackRoom = null))
        viewModelScope.launch { runCatching { currentGateway.leavePlaybackRoom(room.id) } }
    }

    fun consumePlaybackCommand() {
        val viewer = mutableState.value.viewer
        val command = viewer.pendingPlaybackCommands.firstOrNull() ?: return
        mutableState.value = mutableState.value.copy(
            viewer = viewer.copy(pendingPlaybackCommands = viewer.pendingPlaybackCommands.drop(1)),
        )
        val result = StoredPlaybackOperation(PlaybackCommandStatus.APPLIED, PlaybackCommandResultCode.APPLIED)
        playbackOperationStore.put(command.operationId, result)
        gateway?.let { currentGateway ->
            viewModelScope.launch { reportStoredPlaybackResult(currentGateway, command.operationId, result) }
        }
    }

    fun choosePlaybackTarget(target: PlaybackTargetSelection) {
        if (mutableState.value.viewer.loading != null) return
        val picker = mutableState.value.viewer.sourcePicker ?: return
        val source = picker.playerSource ?: return
        refreshExternalPlaybackSupport()
        val validatedTarget = when (target) {
            is PlaybackTargetSelection.Embedded -> {
                if (source.mode == io.rivune.api.PlaybackMode.EXTERNAL) return
                target
            }
            is PlaybackTargetSelection.External -> {
                val player = externalPlaybackSupport.playersFor(source.mode, source.protocol, source.container)
                    .firstOrNull { it.packageName == target.player.packageName }
                    ?: return
                PlaybackTargetSelection.External(player)
            }
        }
        startPlayback(picker, source, validatedTarget)
    }

    fun dismissPlaybackTarget() {
        if (mutableState.value.viewer.loading == ViewerLoading.PLAYER) return
        val picker = mutableState.value.viewer.sourcePicker ?: return
        mutableState.value = mutableState.value.copy(
            viewer = mutableState.value.viewer.copy(sourcePicker = picker.copy(playerSource = null)),
        )
    }

    private fun requestPlaybackTarget(
        picker: SourcePickerState,
        source: io.rivune.api.PlaybackSourceOption,
    ) {
        mutableState.value = mutableState.value.copy(
            viewer = mutableState.value.viewer.copy(
                sourcePicker = picker.copy(playerSource = source),
                inlineFailure = null,
            ),
        )
    }

    private fun startPlayback(
        requestedPicker: SourcePickerState,
        requestedSource: io.rivune.api.PlaybackSourceOption,
        target: PlaybackTargetSelection,
        startOverrideMs: Long? = null,
        onResult: ((Boolean) -> Unit)? = null,
        resumeFailover: io.rivune.api.PlaybackFailoverState? = null,
    ) {
        if (mutableState.value.viewer.loading == ViewerLoading.PLAYER) {
            onResult?.invoke(false)
            return
        }
        val currentPicker = mutableState.value.viewer.sourcePicker
            ?.takeIf {
                it.titleId == requestedPicker.titleId &&
                    it.target.resourceId == requestedPicker.target.resourceId
            }
            ?: run {
                onResult?.invoke(false)
                return
            }
        val source = currentPicker.options.firstOrNull {
            it.id == requestedSource.id && it.sourceRef == requestedSource.sourceRef
        } ?: run {
            onResult?.invoke(false)
            return
        }
        val picker = currentPicker.copy(progress = currentPlaybackProgress(currentPicker))
        if (!sourceReferenceValid(source, instantNow())) {
            loadPlaybackSources(
                picker.target,
                picker.titleId,
                picker.progress,
                showPicker = true,
                autoStart = onResult != null,
                forceEmbedded = onResult != null,
                onResult = onResult,
            )
            return
        }
        val currentGateway = gateway ?: run {
            onResult?.invoke(false)
            return
        }
        val operationGeneration = generation
        val requestGeneration = ++viewerRequestGeneration
        val start = startOverrideMs?.coerceAtLeast(0L)
            ?.div(1_000L)
            ?.coerceAtMost(Int.MAX_VALUE.toLong())
            ?.toInt()
            ?: picker.progress?.takeUnless { it.completed }?.positionSeconds
            ?: 0
        mutableState.value = mutableState.value.copy(
            viewer = mutableState.value.viewer.copy(
                sourcePicker = picker.copy(playerSource = null),
                player = mutableState.value.viewer.player,
                loading = ViewerLoading.PLAYER,
                inlineFailure = null,
                playerFailure = null,
            ),
        )
        viewModelScope.launch {
            var playbackResultDelivered = false
            fun deliverPlaybackResult(success: Boolean) {
                if (!playbackResultDelivered) {
                    playbackResultDelivered = true
                    onResult?.invoke(success)
                }
            }
            var createdSession: io.rivune.api.PlaybackSession? = null
            val selectedExternalPlayer = (target as? PlaybackTargetSelection.External)?.player
            val embedded = (target as? PlaybackTargetSelection.Embedded)
                ?.let { embeddedPlayerSelection(it.preference) }
                ?: EmbeddedPlayerSelection(EmbeddedPlayerEngine.MEDIA3, fallbackAllowed = false)
            val external = selectedExternalPlayer != null
            val preserveOriginalSource = external || embedded.engine == EmbeddedPlayerEngine.MPV
            val markerRequest = picker.markerRequest.takeUnless { external }
            val markerDeferred = markerRequest?.let { request ->
                async(start = CoroutineStart.UNDISPATCHED) {
                    try {
                        currentGateway.playbackMarkers(request.imdbId, request.season, request.episode).markers
                    } catch (cause: CancellationException) {
                        throw cause
                    } catch (_: Throwable) {
                        emptyList()
                    }
                }
            }
            try {
                val candidateRefs = buildList {
                    add(source.sourceRef)
                    picker.options.asSequence()
                        .map { it.sourceRef }
                        .filter { it != source.sourceRef && it.length in 16..128 }
                        .distinct()
                        .take(7)
                        .forEach(::add)
                }
                val failover = resumeFailover ?: if (!external && candidateRefs.size >= 2) {
                    try {
                        currentGateway.createPlaybackFailover(
                            io.rivune.api.PlaybackFailoverCreateInput(
                                candidateSourceRefs = candidateRefs,
                                selectedSourceRef = source.sourceRef,
                                maximumAttempts = minOf(3, candidateRefs.size - 1),
                            ),
                        )
                    } catch (cause: CancellationException) {
                        throw cause
                    } catch (_: Throwable) {
                        null
                    }
                } else {
                    null
                }
                val preparation = currentGateway.preparePlayback(source.sourceRef, start, preserveOriginalSource)
                val accessibility = mutableState.value.viewer.features.accessibility
                val preferredAudioTrack = accessibility?.takeIf { it.audioDescription }
                    ?.let {
                        preparation.media?.audioTracks?.firstOrNull { track ->
                            val title = track.title.orEmpty().lowercase(Locale.ROOT)
                            "description" in title || "descriptive" in title || title == "ad"
                        }?.index
                    }
                val session = currentGateway.resolvePlaybackAccessible(
                    source.sourceRef,
                    picker.titleId.toString(),
                    start,
                    preserveOriginalSource,
                    preferredAudioTrack,
                )
                createdSession = session
                val selected = session.sources.firstOrNull { it.id == session.selectedSourceId } ?: session.sources.firstOrNull()
                    ?: throw IllegalStateException("Playback session has no selected source")
                val mediaUrl = selected.url?.let(currentGateway::resolveResourceUrl)
                    ?: selected.infoHash?.takeIf { external }?.let { magnetUrl(it, picker.target.title) }
                    ?: throw IllegalStateException("Playback session has no playable URL")
                val subtitles = session.subtitles.mapNotNull { subtitle ->
                    val url = subtitle.url?.let(currentGateway::resolveResourceUrl) ?: return@mapNotNull null
                    val serverSelected = subtitle.id == session.selectedSubtitleId
                    PlayerSubtitlePresentation(
                        id = subtitle.id,
                        label = subtitle.language ?: subtitle.id,
                        language = subtitle.language,
                        url = url,
                        selected = when (accessibility?.captions) {
                            io.rivune.api.CaptionsPreference.ON -> serverSelected || session.selectedSubtitleId == null && subtitle == session.subtitles.firstOrNull { it.url != null }
                            io.rivune.api.CaptionsPreference.OFF -> false
                            else -> serverSelected
                        },
                    )
                }
                val durationSeconds = selected.media?.durationSeconds
                    ?.takeIf { it.isFinite() && it > 0.0 }
                    ?.coerceAtMost(Int.MAX_VALUE.toDouble())
                    ?.toInt()
                    ?: picker.progress?.durationSeconds
                    ?: 0
                lastPlayerProgress = null
                lastPersistedPlayerProgress = null
                advancingPlayerSessionId = null
                if (!viewerRequestCurrent(operationGeneration, requestGeneration)) {
                    markerDeferred?.cancel()
                    deliverPlaybackResult(false)
                    return@launch
                }
                mutableState.value = mutableState.value.copy(
                    viewer = mutableState.value.viewer.copy(
                        sourcePicker = picker.copy(playerSource = source),
                        player = PlayerPresentation(
                            key = session.id.toString(),
                            sessionId = session.id,
                            titleId = picker.titleId,
                            title = picker.target.title,
                            mediaType = picker.target.mediaType,
                            resourceId = picker.target.resourceId,
                            sourceAddonId = picker.target.sourceAddonId,
                            posterUrl = picker.target.posterUrl,
                            mediaUrl = mediaUrl,
                            protocol = selected.protocol,
                            container = selected.container,
                            mediaTimeline = selected.mediaTimeline,
                            startPositionMs = start * 1_000L,
                            timelineStartPositionMs = start * 1_000L,
                            durationSeconds = durationSeconds,
                            expectedProgressVersion = picker.progress?.version ?: 0,
                            engine = embedded.engine,
                            fallbackAllowed = embedded.fallbackAllowed,
                            subtitles = subtitles,
                            externalPlayer = selectedExternalPlayer,
                            nextEpisode = picker.nextEpisode,
                            decisionReasons = selected.decision?.reasons.orEmpty(),
                            queueItemId = picker.target.queueItemId
                                ?: mutableState.value.viewer.features.queue?.items?.firstOrNull {
                                    it.resourceId == picker.target.resourceId && it.sourceAddonId == picker.target.sourceAddonId
                                }?.id,
                            failover = failover,
                        ),
                        loading = null,
                        playerFailure = null,
                        inlineFailure = null,
                    ),
                )
                diagnostics.record(DiagnosticEventCode.PLAYBACK_STARTED)
                createdSession = null
                deliverPlaybackResult(true)
                consumeQueueAfterPlaybackStarted(currentGateway, picker, failover)

                val markers = markerDeferred?.await().orEmpty()
                val currentViewer = mutableState.value.viewer
                val currentPlayer = currentViewer.player
                if (
                    viewerRequestCurrent(operationGeneration, requestGeneration) &&
                    currentPlayer?.sessionId == session.id &&
                    currentViewer.sourcePicker?.markerRequest == markerRequest
                ) {
                    mutableState.value = mutableState.value.copy(
                        viewer = currentViewer.copy(player = currentPlayer.copy(markers = markers)),
                    )
                }
            } catch (cause: CancellationException) {
                markerDeferred?.cancel()
                deliverPlaybackResult(false)
                throw cause
            } catch (cause: Throwable) {
                markerDeferred?.cancel()
                diagnostics.record(DiagnosticEventCode.PLAYBACK_FAILED)
                viewerFailure(cause, operationGeneration, requestGeneration, UiFailure.PLAYBACK)
                deliverPlaybackResult(false)
            } finally {
                createdSession?.let { session ->
                    kotlinx.coroutines.withContext(NonCancellable) {
                        runCatching { currentGateway.stopPlayback(session.id) }
                    }
                }
            }
        }

    }
    private fun consumeQueueAfterPlaybackStarted(
        currentGateway: RivuneGateway,
        picker: SourcePickerState,
        failover: io.rivune.api.PlaybackFailoverState?,
    ) {
        val profile = mutableState.value.activeProfile ?: return
        val queue = mutableState.value.viewer.features.queue ?: return
        val item = picker.target.queueItemId?.let { id -> queue.items.firstOrNull { it.id == id } }
            ?: queue.items.firstOrNull { it.resourceId == picker.target.resourceId && it.sourceAddonId == picker.target.sourceAddonId }
            ?: return
        val input = io.rivune.api.ReadingQueueMutationInput(UUID.randomUUID(), queue.revision)
        viewModelScope.launch {
            try {
                val mutation = retryIdempotentMutation { currentGateway.consumeReadingQueueItem(profile.id, item.id, input) }
                if (mutableState.value.activeProfile?.id == profile.id) mutableState.update { state ->
                    val currentQueue = state.viewer.features.queue
                    if (currentQueue?.revision != queue.revision) state else state.copy(viewer = state.viewer.copy(features = state.viewer.features.copy(
                        queue = currentQueue.copy(revision = mutation.revision, items = currentQueue.items.filterNot { it.id == item.id }),
                    )))
                }
            } catch (cause: CancellationException) {
                throw cause
            } catch (cause: Throwable) {
                featureMutationFailure(cause)
            }
        }
    }

    private fun currentPlaybackProgress(picker: SourcePickerState): PlaybackProgress? {
        val detail = mutableState.value.viewer.detail ?: return picker.progress
        return when {
            detail.titleId == picker.titleId -> detail.progress
            picker.titleId in detail.episodeProgress -> detail.episodeProgress[picker.titleId]
            else -> picker.progress
        }
    }

    private fun sourceReferenceValid(
        source: io.rivune.api.PlaybackSourceOption,
        now: Instant,
    ): Boolean = runCatching { Instant.parse(source.expiresAt) }
        .getOrNull()
        ?.let(now::isBefore) == true

    fun dismissSourcePicker() {
        val currentViewer = mutableState.value.viewer
        if (currentViewer.loading == ViewerLoading.PLAYER || currentViewer.loading == ViewerLoading.ACTION) return
        mutableState.value = mutableState.value.copy(
            viewer = currentViewer.copy(
                sourcePickerVisible = false,
                loading = if (currentViewer.loading == ViewerLoading.SOURCES) null else currentViewer.loading,
                inlineFailure = if (currentViewer.inlineFailure == UiFailure.PLAYBACK) null else currentViewer.inlineFailure,
            ),
        )
    }

    fun toggleLibrary() {
        val detail = mutableState.value.viewer.detail ?: return
        if (detail.target.mediaType == "episode") return
        val currentGateway = gateway ?: return
        if (mutableState.value.viewer.loading != null) return
        val operationGeneration = generation
        val requestGeneration = viewerRequestGeneration
        mutableState.value = mutableState.value.copy(viewer = mutableState.value.viewer.copy(loading = ViewerLoading.ACTION, inlineFailure = null))
        viewModelScope.launch {
            try {
                if (detail.inLibrary) currentGateway.removeLibraryTitle(detail.titleId) else currentGateway.addLibraryTitle(detail.titleId)
                if (!viewerRequestCurrent(operationGeneration, requestGeneration)) return@launch
                mutableState.value = mutableState.value.copy(
                    viewer = mutableState.value.viewer.copy(
                        detail = detail.copy(inLibrary = !detail.inLibrary),
                        library = LibraryState(mediaType = mutableState.value.viewer.library.mediaType),
                        loading = null,
                    ),
                )
            } catch (cause: CancellationException) {
                throw cause
            } catch (cause: Throwable) {
                viewerFailure(cause, operationGeneration, requestGeneration, UiFailure.ACTION)
            }
        }
    }

    fun toggleWatched() {
        if (mutableState.value.viewer.loading != null) return
        val detail = mutableState.value.viewer.detail ?: return
        val currentGateway = gateway ?: return
        val requestGeneration = viewerRequestGeneration
        val operationGeneration = generation
        val season = detail.season
        val episodes = season?.episodes.orEmpty()
        if (season != null && episodes.isEmpty()) return
        mutableState.value = mutableState.value.copy(
            viewer = mutableState.value.viewer.copy(loading = ViewerLoading.ACTION, inlineFailure = null),
        )
        viewModelScope.launch {
            try {
                val updatedDetail = if (season != null) {
                    val completed = episodes.all { detail.episodeProgress[it.id]?.completed == true }
                    var episodeProgress = detail.episodeProgress
                    for (chunk in episodes.chunked(MAX_WATCHED_BATCH_SIZE)) {
                        val results = currentGateway.setTitlesWatchedBatch(
                            chunk.map { episode ->
                                SetWatchedBatchItem(
                                    titleId = episode.id,
                                    completed = !completed,
                                    expectedVersion = episodeProgress[episode.id]?.version ?: 0,
                                )
                            },
                        ).items
                        episodeProgress = episodeProgress + results.associate { it.titleId to it.progress }
                        if (!viewerRequestCurrent(operationGeneration, requestGeneration)) return@launch
                        mutableState.value = mutableState.value.copy(
                            viewer = mutableState.value.viewer.copy(
                                detail = detail.copy(episodeProgress = episodeProgress),
                            ),
                        )
                    }
                    detail.copy(episodeProgress = episodeProgress)
                } else {
                    val expected = detail.progress?.version ?: 0
                    val progress = if (detail.progress?.completed == true) {
                        currentGateway.markTitleUnwatched(detail.titleId, expected)
                    } else {
                        currentGateway.markTitleWatched(detail.titleId, expected)
                    }
                    detail.copy(progress = progress)
                }
                if (!viewerRequestCurrent(operationGeneration, requestGeneration)) return@launch
                mutableState.value = mutableState.value.copy(
                    viewer = mutableState.value.viewer.copy(detail = updatedDetail, loading = null),
                )
            } catch (cause: CancellationException) {
                throw cause
            } catch (cause: Throwable) {
                viewerFailure(cause, operationGeneration, requestGeneration, UiFailure.ACTION)
            }
        }
    }

    fun refreshViewer() {
        when {
            mutableState.value.viewer.detail != null -> openMedia(mutableState.value.viewer.detail!!.target)
            mutableState.value.resolvedFolder != null -> refresh()
            else -> selectViewerTab(mutableState.value.viewer.selectedTab)
        }
    }

    fun previousCalendarMonth() {
        mutableState.value = mutableState.value.copy(
            calendarMonth = mutableState.value.calendarMonth.minusMonths(1),
            calendarEvents = emptyList(),
        )
        loadCalendar()
    }

    fun nextCalendarMonth() {
        mutableState.value = mutableState.value.copy(
            calendarMonth = mutableState.value.calendarMonth.plusMonths(1),
            calendarEvents = emptyList(),
        )
        loadCalendar()
    }

    fun openCalendarEvent(event: CalendarEvent) {
        openMedia(
            MediaTarget(
                id = event.resourceId ?: event.titleId.toString(),
                resourceId = event.resourceId ?: event.titleId.toString(),
                mediaType = event.mediaType.name.lowercase(),
                title = event.title,
                titleId = event.titleId,
                provider = event.resourceProvider,
                posterUrl = event.posterUrl,
                seriesId = event.seriesId,
                seasonId = event.seasonId?.toString(),
                seasonNumber = event.seasonNumber,
                episodeNumber = event.episodeNumber,
                released = event.releaseDate,
            ),
        )
    }

    fun playerPlaybackEnded() {
        val player = mutableState.value.viewer.player ?: return
        if (mutableState.value.viewer.activePlaybackRoom != null) {
            endActivePlaybackRoom(player)
            return
        }
        if (mutableState.value.effectiveSettings?.settings?.autoplayNextEpisode != false) {
            advancePlayer(player.sessionId, completedWithoutDuration = player.durationSeconds <= 0)
        }
    }

    fun playNextEpisode() {
        val player = mutableState.value.viewer.player ?: return
        if (mutableState.value.viewer.activePlaybackRoom != null) {
            endActivePlaybackRoom(player)
            leavePlaybackRoom()
        }
        advancePlayer(player.sessionId)
    }

    private fun endActivePlaybackRoom(player: PlayerPresentation) {
        val currentGateway = gateway ?: return
        val room = mutableState.value.viewer.activePlaybackRoom?.takeIf { it.currentMemberIsHost } ?: return
        if (coordinationEndedSessionId == player.sessionId || coordinationEndingSessionId == player.sessionId) return
        coordinationEndingSessionId = player.sessionId
        coordinationPlaying = false
        terminalCleanupScope.launch {
            kotlinx.coroutines.withContext(NonCancellable) {
                roomEndMutex.withLock {
                    transitionRoomToEndedLocked(currentGateway, room, player.sessionId)?.let { endedRoom ->
                        mutableState.update { state ->
                            val active = state.viewer.activePlaybackRoom
                            if (active?.id != endedRoom.id) state else state.copy(
                                viewer = state.viewer.copy(activePlaybackRoom = endedRoom.preservingJoinCode(active)),
                            )
                        }
                    }
                }
            }
        }
    }

    private suspend fun transitionRoomToEndedLocked(
        currentGateway: RivuneGateway,
        initialRoom: PlaybackRoom,
        sessionId: UUID,
    ): PlaybackRoom? {
        var room = initialRoom
        if (initialRoom.state == "ended") {
            coordinationEndedSessionId = sessionId
            coordinationEndingSessionId = null
            return initialRoom
        }
        repeat(2) { attempt ->
            try {
                val ended = currentGateway.updatePlaybackRoom(
                    room.id,
                    PlaybackRoomUpdateInput(
                        coordinatedHostRoomState(ending = true, playing = false),
                        coordinationPositionMs,
                        coordinationDurationMs,
                        room.version,
                    ),
                ).preservingJoinCode(room)
                coordinationEndedSessionId = sessionId
                coordinationEndingSessionId = null
                return ended
            } catch (cause: RivuneApiException.Server) {
                if (cause.status != 409) return null
                val latest = runCatching { currentGateway.playbackRoom(room.id) }.getOrNull() ?: return null
                if (latest.state == "ended") {
                    coordinationEndedSessionId = sessionId
                    coordinationEndingSessionId = null
                    return latest.preservingJoinCode(room)
                }
                room = latest.preservingJoinCode(room)
                if (attempt == 1) return null
            } catch (_: Throwable) {
                return null
            }
        }
        return null
    }

    private fun advancePlayer(
        sessionId: UUID,
        suppliedProgress: PlayerProgressSnapshot? = null,
        completedWithoutDuration: Boolean = false,
        automaticallyStartNext: Boolean = true,
    ) {
        val player = mutableState.value.viewer.player?.takeIf { it.sessionId == sessionId } ?: return
        val nextEpisode = player.nextEpisode ?: return
        val nextTitleId = nextEpisode.titleId ?: return
        if (advancingPlayerSessionId != null) return
        val currentGateway = gateway ?: return
        val finalProgress = suppliedProgress
            ?: lastPlayerProgress?.takeIf { it.sessionId == player.sessionId }
        advancingPlayerSessionId = player.sessionId
        val operationGeneration = generation
        val requestGeneration = ++viewerRequestGeneration
        mutableState.value = mutableState.value.copy(
            viewer = mutableState.value.viewer.copy(
                player = null,
                playerFailure = null,
                sourcePicker = null,
                sourcePickerVisible = true,
                loading = ViewerLoading.SOURCES,
                inlineFailure = null,
            ),
        )
        diagnostics.record(DiagnosticEventCode.PLAYBACK_STOPPED)
        viewModelScope.launch {
            try {
                when {
                    finalProgress != null -> updatePlayerProgress(player, finalProgress, currentGateway)
                    completedWithoutDuration -> markPlayerWatched(player, currentGateway)
                }
            } finally {
                kotlinx.coroutines.withContext(NonCancellable) {
                    runCatching { currentGateway.stopPlayback(player.sessionId) }
                }
            }
            if (
                advancingPlayerSessionId != player.sessionId ||
                !viewerRequestCurrent(operationGeneration, requestGeneration)
            ) return@launch
            val progress = runCatching { currentGateway.playbackProgress(nextTitleId) }.getOrNull()
            if (!viewerRequestCurrent(operationGeneration, requestGeneration)) return@launch
            advancingPlayerSessionId = null
            val continuationTarget = if (!automaticallyStartNext) {
                null
            } else {
                player.externalPlayer
                    ?.let { PlaybackTargetSelection.External(it) }
                    ?: PlaybackTargetSelection.Embedded(
                        when {
                            player.engine == EmbeddedPlayerEngine.MPV -> EmbeddedPlayerPreference.MPV
                            player.fallbackAllowed -> EmbeddedPlayerPreference.AUTOMATIC
                            else -> EmbeddedPlayerPreference.MEDIA3
                        },
                    )
            }
            loadPlaybackSources(nextEpisode, nextTitleId, progress, continuationTarget)
        }
    }
    fun retryFailedPlayer() {
        val failure = currentPlayerFailure() ?: return
        recoverFailedPlayer(failure.failure.positionMs)
    }

    fun restartFailedPlayer() {
        if (currentPlayerFailure() == null) return
        recoverFailedPlayer(0L)
    }

    private fun currentPlayerFailure(): PlayerFailureState? {
        val viewer = mutableState.value.viewer
        val player = viewer.player ?: return null
        return viewer.playerFailure?.takeIf { it.matches(player) }
    }

    private fun recoverFailedPlayer(startPositionMs: Long) {
        if (mutableState.value.viewer.loading != null) return
        val failure = currentPlayerFailure() ?: return
        val player = mutableState.value.viewer.player ?: return
        val picker = mutableState.value.viewer.sourcePicker ?: return
        val source = picker.playerSource ?: return
        if (failure.sessionId != player.sessionId) return
        val target = player.externalPlayer
            ?.let { PlaybackTargetSelection.External(it) }
            ?: PlaybackTargetSelection.Embedded(player.recoveryEmbeddedPreference())
        val currentGateway = gateway ?: return
        val operationGeneration = generation
        val requestGeneration = ++viewerRequestGeneration
        mutableState.value = mutableState.value.copy(
            viewer = mutableState.value.viewer.copy(
                player = null,
                playerFailure = null,
                sourcePicker = picker,
                sourcePickerVisible = true,
                loading = ViewerLoading.SOURCES,
                inlineFailure = null,
            ),
        )
        viewModelScope.launch {
            kotlinx.coroutines.withContext(NonCancellable) { runCatching { currentGateway.stopPlayback(player.sessionId) } }
            if (!viewerRequestCurrent(operationGeneration, requestGeneration)) return@launch
            loadPlaybackSources(
                target = picker.target,
                titleId = picker.titleId,
                progress = picker.progress,
                continuationTarget = target,
                continuationSource = source,
                continuationSourceWasUnique = picker.options.count { it.matchesRecoverySource(source) } == 1,
                startOverrideMs = startPositionMs,
            )
        }
    }

    fun chooseAnotherPlaybackSource() {
        if (mutableState.value.viewer.loading == ViewerLoading.PLAYER) return
        val failure = currentPlayerFailure() ?: return
        val player = mutableState.value.viewer.player ?: return
        val picker = mutableState.value.viewer.sourcePicker ?: return
        if (failure.sessionId != player.sessionId) return
        val currentGateway = gateway
        viewerRequestGeneration += 1
        mutableState.value = mutableState.value.copy(
            viewer = mutableState.value.viewer.copy(
                player = null,
                playerFailure = null,
                sourcePicker = picker.copy(playerSource = null),
                sourcePickerVisible = true,
                loading = ViewerLoading.PLAYER,
                inlineFailure = UiFailure.PLAYBACK,
            ),
        )
        viewModelScope.launch {
            kotlinx.coroutines.withContext(NonCancellable) {
                runCatching { currentGateway?.stopPlayback(player.sessionId) }
                player.failover?.id?.let { runCatching { currentGateway?.cancelPlaybackFailover(it) } }
            }
            if (mutableState.value.viewer.player == null && mutableState.value.viewer.sourcePicker?.titleId == picker.titleId) {
                mutableState.value = mutableState.value.copy(
                    viewer = mutableState.value.viewer.copy(loading = null, inlineFailure = UiFailure.PLAYBACK),
                )
            }
        }
    }

    fun closePlayer() {
        val player = mutableState.value.viewer.player ?: return
        val currentGateway = gateway
        endActivePlaybackRoom(player)
        val picker = mutableState.value.viewer.sourcePicker?.copy(playerSource = null)
        viewerRequestGeneration += 1
        mutableState.value = mutableState.value.copy(
            viewer = mutableState.value.viewer.copy(
                player = null,
                playerFailure = null,
                sourcePicker = picker,
                sourcePickerVisible = picker != null && appPreferences.snapshot().automaticallyShowStreams,
                pendingPlaybackCommands = emptyList(),
                loading = null,
            ),
        )
        if (player.mediaType != "offline") {
            terminalCleanupScope.launch {
                kotlinx.coroutines.withContext(NonCancellable) {
                    runCatching { currentGateway?.stopPlayback(player.sessionId) }
                    player.failover?.id?.let { runCatching { currentGateway?.cancelPlaybackFailover(it) } }
                }
            }
        }
        diagnostics.record(DiagnosticEventCode.PLAYBACK_STOPPED)
        loadHomeContent()
    }

    fun playerFailed(playerKey: String, sessionId: UUID, failure: PlayerEngineFailure) {
        while (true) {
            val state = mutableState.value
            val player = state.viewer.player ?: return
            if (player.key != playerKey || player.sessionId != sessionId) return
            if (state.viewer.playerFailure?.matches(player) == true) return
            if (player.canAdvancePlaybackFailover(failure)) {
                val advancing = state.copy(viewer = state.viewer.copy(player = player.copy(failoverAdvancing = true)))
                if (!mutableState.compareAndSet(state, advancing)) continue
                advanceFailedPlaybackSource(player, failure)
                return
            }
            val fallback = player.fallbackToMpv(failure, "${player.sessionId}:mpv:${UUID.randomUUID()}")
            if (fallback != null) {
                val updated = state.copy(
                    viewer = state.viewer.copy(
                        player = fallback,
                        playerFailure = null,
                        loading = null,
                        inlineFailure = null,
                    ),
                )
                if (mutableState.compareAndSet(state, updated)) return
                continue
            }
            val failed = state.copy(
                viewer = state.viewer.copy(
                    playerFailure = PlayerFailureState(player.key, player.sessionId, failure),
                    loading = null,
                    inlineFailure = null,
                ),
            )
            if (!mutableState.compareAndSet(state, failed)) continue
            diagnostics.record(DiagnosticEventCode.PLAYBACK_FAILED)
            return
        }
    }

    private fun advanceFailedPlaybackSource(player: PlayerPresentation, failure: PlayerEngineFailure) {
        val currentGateway = gateway ?: return
        val failover = player.failover ?: return
        val error = failure.playbackFailoverError() ?: return
        val picker = mutableState.value.viewer.sourcePicker ?: return
        viewModelScope.launch {
            val inputFor: (Long) -> io.rivune.api.PlaybackFailoverAdvanceInput = { revision ->
                io.rivune.api.PlaybackFailoverAdvanceInput(
                    error = error,
                    positionSeconds = (failure.positionMs.coerceAtLeast(0L) / 1_000.0).coerceAtMost(86_400.0),
                    expectedRevision = revision,
                )
            }
            val advanced = try {
                currentGateway.advancePlaybackFailover(failover.id, inputFor(failover.revision))
            } catch (cause: RivuneApiException.Server) {
                if (cause.status != 409) null else {
                    val latest = try { currentGateway.playbackFailover(failover.id) } catch (_: Throwable) { null }
                    if (latest?.status == io.rivune.api.PlaybackFailoverStatus.ACTIVE &&
                        latest.attemptCount < latest.maximumAttempts
                    ) try {
                        currentGateway.advancePlaybackFailover(latest.id, inputFor(latest.revision))
                    } catch (_: Throwable) {
                        null
                    } else latest
                }
            } catch (cause: CancellationException) {
                throw cause
            } catch (_: Throwable) {
                null
            }
            val currentPlayer = mutableState.value.viewer.player
                ?.takeIf { it.sessionId == player.sessionId && it.failoverAdvancing }
                ?: return@launch
            val nextSource = advanced?.currentSourceRef?.let { sourceRef ->
                picker.options.firstOrNull { it.sourceRef == sourceRef && it.sourceRef != picker.playerSource?.sourceRef }
            }
            if (advanced?.status != io.rivune.api.PlaybackFailoverStatus.ACTIVE || nextSource == null) {
                mutableState.update { state -> state.copy(viewer = state.viewer.copy(
                    player = currentPlayer.copy(failover = advanced ?: failover, failoverAdvancing = false),
                    playerFailure = PlayerFailureState(currentPlayer.key, currentPlayer.sessionId, failure),
                    loading = null,
                )) }
                diagnostics.record(DiagnosticEventCode.PLAYBACK_FAILED)
                return@launch
            }
            mutableState.update { state -> state.copy(viewer = state.viewer.copy(
                player = null,
                playerFailure = null,
                sourcePicker = picker.copy(playerSource = null),
                sourcePickerVisible = true,
                loading = null,
                inlineFailure = null,
            )) }
            kotlinx.coroutines.withContext(NonCancellable) { runCatching { currentGateway.stopPlayback(player.sessionId) } }
            startPlayback(
                picker,
                nextSource,
                PlaybackTargetSelection.Embedded(player.recoveryEmbeddedPreference()),
                startOverrideMs = (advanced.positionSeconds * 1_000.0).toLong(),
                resumeFailover = advanced,
            )
        }
    }
    fun externalPlaybackFinished(result: ExternalPlaybackResult?) {
        val player = mutableState.value.viewer.player?.takeIf { it.externalPlayer != null } ?: return
        val currentGateway = gateway ?: return
        val durationSeconds = result?.durationMs
            ?.let { ((it + 999L) / 1_000L).coerceAtMost(Int.MAX_VALUE.toLong()).toInt() }
            ?.takeIf { it > 0 }
            ?: player.durationSeconds
        val progress = result?.takeIf { durationSeconds > 0 }?.let {
            val positionSeconds = when {
                it.positionMs != null -> (it.positionMs / 1_000L).coerceIn(0L, durationSeconds.toLong()).toInt()
                it.completed -> durationSeconds
                else -> return@let null
            }
            PlayerProgressSnapshot(
                sessionId = player.sessionId,
                positionSeconds = positionSeconds,
                durationSeconds = durationSeconds,
                completed = it.completed || positionSeconds.toLong() * 100L >= durationSeconds.toLong() * 90L,
            )
        }
        val completedWithoutDuration = result?.completed == true && durationSeconds <= 0
        if (result?.completed == true && player.nextEpisode != null) {
            advancePlayer(
                sessionId = player.sessionId,
                suppliedProgress = progress,
                completedWithoutDuration = completedWithoutDuration,
                automaticallyStartNext = mutableState.value.effectiveSettings?.settings?.autoplayNextEpisode != false,
            )
            return
        }
        viewerRequestGeneration += 1
        val picker = mutableState.value.viewer.sourcePicker?.copy(playerSource = null)
        mutableState.value = mutableState.value.copy(
            viewer = mutableState.value.viewer.copy(
                player = null,
                playerFailure = null,
                sourcePicker = picker,
                sourcePickerVisible = picker != null && appPreferences.snapshot().automaticallyShowStreams,
                loading = null,
            ),
        )
        diagnostics.record(DiagnosticEventCode.PLAYBACK_STOPPED)
        terminalCleanupScope.launch {
            try {
                when {
                    progress != null -> updatePlayerProgress(player, progress, currentGateway)
                    completedWithoutDuration -> markPlayerWatched(player, currentGateway)
                }
            } finally {
                kotlinx.coroutines.withContext(NonCancellable) {
                    runCatching { currentGateway.stopPlayback(player.sessionId) }
                }
            }
            viewModelScope.launch { loadHomeContent() }
        }
    }

    internal fun beginTerminalOwnerDestruction() {
        terminalOwnerDestructionPending = true
    }

    internal fun stopPlaybackForTerminalOwner() {
        terminalOwnerDestructionPending = false
        val player = mutableState.value.viewer.player ?: return
        val currentGateway = gateway ?: return
        val finalProgress = lastPlayerProgress?.takeIf { it.sessionId == player.sessionId }
        mutableState.value = mutableState.value.copy(
            viewer = mutableState.value.viewer.copy(player = null, playerFailure = null, loading = null),
        )
        if (player.mediaType == "offline") return
        terminalCleanupScope.launch {
            try {
                finalProgress?.let { updatePlayerProgress(player, it, currentGateway) }
            } finally {
                kotlinx.coroutines.withContext(NonCancellable) {
                    runCatching { currentGateway.stopPlayback(player.sessionId) }
                }
            }
        }
    }

    override fun onCleared() {
        if (!terminalOwnerDestructionPending) {
            stopPlaybackForTerminalOwner()
        }
        super.onCleared()
    }

    fun reportPlaybackState(positionMs: Long, durationMs: Long, isPlaying: Boolean) {
        val player = mutableState.value.viewer.player ?: return
        if (player.mediaType == "offline") return
        coordinationPositionMs = positionMs.coerceAtLeast(0L)
        coordinationDurationMs = durationMs.coerceAtLeast(0L)
        coordinationPlaying = isPlaying
    }

    fun reportPlayerProgress(positionSeconds: Int, durationSeconds: Int, completed: Boolean) {
        val player = mutableState.value.viewer.player ?: return
        if (durationSeconds <= 0 || positionSeconds < 0) return
        val progress = PlayerProgressSnapshot(
            sessionId = player.sessionId,
            positionSeconds = positionSeconds.coerceAtMost(durationSeconds),
            durationSeconds = durationSeconds,
            completed = completed,
        )
        lastPlayerProgress = progress
        if (player.key.startsWith("offline:")) {
            val store = offlineMediaStore ?: return
            viewModelScope.launch {
                val updated = runCatching {
                    store.updateProgress(
                        scope = activeOfflineScope ?: error("Offline profile scope is locked"),
                        id = player.sessionId,
                        positionMs = progress.positionSeconds * 1_000L,
                        durationMs = progress.durationSeconds * 1_000L,
                        completed = progress.completed,
                    )
                }.getOrNull() ?: return@launch
                mutableState.update { state ->
                    state.copy(
                        viewer = state.viewer.copy(
                            offlineItems = state.viewer.offlineItems.map { if (it.id == updated.id) updated else it },
                        ),
                    )
                }
            }
            return
        }
        if (terminalOwnerDestructionPending) return
        viewModelScope.launch { updatePlayerProgress(player, progress) }
    }

    private suspend fun updatePlayerProgress(
        player: PlayerPresentation,
        progress: PlayerProgressSnapshot,
        requestedGateway: RivuneGateway? = gateway,
    ) {
        progressUpdateMutex.withLock {
            val currentPlayer = mutableState.value.viewer.player
            if (currentPlayer != null && currentPlayer.sessionId != player.sessionId) return@withLock
            val active = currentPlayer ?: player
            val currentGateway = requestedGateway ?: return@withLock
            if (lastPersistedPlayerProgress == progress) return@withLock
            try {
                fun request(expectedVersion: Long) = UpdatePlaybackProgressRequest(
                    positionSeconds = progress.positionSeconds,
                    durationSeconds = progress.durationSeconds,
                    completed = progress.completed,
                    expectedVersion = expectedVersion,
                )
                val updated = try {
                    currentGateway.updatePlaybackProgress(active.titleId, request(active.expectedProgressVersion))
                } catch (cause: RivuneApiException.Server) {
                    if (cause.status != 409) throw cause
                    val latest = currentGateway.playbackProgress(active.titleId) ?: throw cause
                    currentGateway.updatePlaybackProgress(active.titleId, request(latest.version))
                }
                applyPlayerProgress(active, updated)
                lastPersistedPlayerProgress = progress
            } catch (cause: CancellationException) {
                throw cause
            } catch (cause: Throwable) {
                if (failureFor(cause, UiFailure.ACTION) == UiFailure.SESSION_EXPIRED) handleSessionExpired(generation)
            }
        }
    }

    private suspend fun markPlayerWatched(player: PlayerPresentation, currentGateway: RivuneGateway) {
        progressUpdateMutex.withLock {
            val currentPlayer = mutableState.value.viewer.player
            if (currentPlayer != null && currentPlayer.sessionId != player.sessionId) return@withLock
            val active = currentPlayer ?: player
            try {
                val updated = try {
                    currentGateway.markTitleWatched(active.titleId, active.expectedProgressVersion)
                } catch (cause: RivuneApiException.Server) {
                    if (cause.status != 409) throw cause
                    val latest = currentGateway.playbackProgress(active.titleId) ?: throw cause
                    if (latest.completed) latest else currentGateway.markTitleWatched(active.titleId, latest.version)
                }
                applyPlayerProgress(active, updated)
            } catch (cause: CancellationException) {
                throw cause
            } catch (cause: Throwable) {
                if (failureFor(cause, UiFailure.ACTION) == UiFailure.SESSION_EXPIRED) handleSessionExpired(generation)
            }
        }
    }

    private fun startCoordination(currentGateway: RivuneGateway) {
        if (!playbackCoordinationAvailable || !coordinationForeground) return
        coordinationJob?.cancel()
        coordinationJob = viewModelScope.launch {
            var lastPresenceAtMilliseconds: Long? = null
            while (coordinationForeground) {
                val viewer = mutableState.value.viewer
                val player = viewer.player
                val coordinatedItem = player?.takeUnless { it.mediaType == "offline" }?.coordinatedItem()
                val heartbeat = PlaybackDeviceHeartbeatInput(
                    capabilities = listOf("remote-control", "watch-room"),
                    state = PlaybackDeviceState(
                        status = if (coordinatedItem == null) "idle" else if (coordinationPlaying) "playing" else "paused",
                        item = coordinatedItem,
                        positionMilliseconds = if (coordinatedItem == null) 0 else coordinationPositionMs,
                        durationMilliseconds = if (coordinatedItem == null) 0 else coordinationDurationMs,
                    ),
                )
                val nowMilliseconds = monotonicNowMilliseconds()
                val presenceDue = lastPresenceAtMilliseconds == null ||
                    nowMilliseconds - lastPresenceAtMilliseconds >= COORDINATION_PRESENCE_INTERVAL_MILLISECONDS
                val devices = if (presenceDue) {
                    runCatching { currentGateway.updatePlaybackDevice(heartbeat) }
                    lastPresenceAtMilliseconds = nowMilliseconds
                    runCatching { controllablePlaybackDevices(currentGateway.playbackDevices().devices) }
                        .getOrDefault(controllablePlaybackDevices(viewer.playbackDevices))
                } else {
                    controllablePlaybackDevices(viewer.playbackDevices)
                }
                val commands = runCatching { currentGateway.playbackCommands(lastPlaybackOperationId).commands }
                    .getOrDefault(emptyList())
                if (commands.isNotEmpty()) {
                    coordinationRecentUntilMilliseconds = nowMilliseconds + COORDINATION_RECENT_ACTIVITY_MILLISECONDS
                }
                val queuedCommands = mutableListOf<io.rivune.api.PlaybackCommand>()
                var hasPlayback = viewer.player != null
                for (command in commands) {
                    val stored = playbackOperationStore.get(command.operationId)
                    if (stored != null) {
                        if (!reportStoredPlaybackResult(currentGateway, command.operationId, stored)) break
                        lastPlaybackOperationId = command.operationId
                        continue
                    }
                    if (playbackCommandExpired(command.expiresAt, instantNow())) {
                        val expired = StoredPlaybackOperation(PlaybackCommandStatus.EXPIRED, PlaybackCommandResultCode.EXPIRED)
                        playbackOperationStore.put(command.operationId, expired)
                        if (!reportStoredPlaybackResult(currentGateway, command.operationId, expired)) break
                        lastPlaybackOperationId = command.operationId
                        continue
                    }
                    if (command.command == PlaybackCommandType.LOAD) {
                        val item = command.item
                        val validMode = command.mode == PlaybackCommandMode.HANDOFF || command.mode == PlaybackCommandMode.PLAY_COPY
                        val loaded = if (item != null && validMode) awaitCoordinatedMedia(
                            MediaTarget(
                                id = item.resourceId,
                                resourceId = item.resourceId,
                                mediaType = item.mediaType,
                                title = item.title,
                                titleId = item.titleId,
                                sourceAddonId = item.sourceAddonId,
                                posterUrl = item.posterUrl,
                            ),
                            command.positionMilliseconds,
                        ) else false
                        val result = if (loaded) {
                            hasPlayback = true
                            StoredPlaybackOperation(PlaybackCommandStatus.APPLIED, PlaybackCommandResultCode.APPLIED)
                        } else {
                            StoredPlaybackOperation(PlaybackCommandStatus.FAILED, if (item == null || !validMode) PlaybackCommandResultCode.INVALID_STATE else PlaybackCommandResultCode.EXECUTION_FAILED)
                        }
                        playbackOperationStore.put(command.operationId, result)
                        if (!reportStoredPlaybackResult(currentGateway, command.operationId, result)) break
                        lastPlaybackOperationId = command.operationId
                    } else if (command.command != PlaybackCommandType.LOAD && (hasPlayback || coordinatedPlaybackLoading)) {
                        if (viewer.pendingPlaybackCommands.none { it.operationId == command.operationId }) queuedCommands += command
                        break
                    } else {
                        val failed = StoredPlaybackOperation(PlaybackCommandStatus.FAILED, PlaybackCommandResultCode.UNSUPPORTED)
                        playbackOperationStore.put(command.operationId, failed)
                        if (!reportStoredPlaybackResult(currentGateway, command.operationId, failed)) break
                        lastPlaybackOperationId = command.operationId
                    }
                }
                var room = viewer.activePlaybackRoom
                if (room != null) {
                    room = roomEndMutex.withLock {
                        val activeRoom = room ?: return@withLock null
                        val endingSessionId = coordinationEndingSessionId
                        val endedSessionId = coordinationEndedSessionId
                        when {
                            activeRoom.currentMemberIsHost && endingSessionId != null ->
                                transitionRoomToEndedLocked(currentGateway, activeRoom, endingSessionId)
                                    ?: refreshPlaybackRoom(currentGateway, activeRoom)
                            activeRoom.currentMemberIsHost && !shouldPublishHostRoomProgress(
                                ending = endingSessionId != null,
                                ended = endedSessionId != null,
                            ) -> refreshPlaybackRoom(currentGateway, activeRoom)
                            activeRoom.currentMemberIsHost &&
                                activeRoom.state != "ended" &&
                                coordinatedItem?.titleId == activeRoom.item.titleId -> runCatching {
                                    currentGateway.updatePlaybackRoom(
                                        activeRoom.id,
                                        PlaybackRoomUpdateInput(
                                            coordinatedHostRoomState(ending = false, playing = coordinationPlaying),
                                            coordinationPositionMs,
                                            coordinationDurationMs,
                                            activeRoom.version,
                                        ),
                                    ).preservingJoinCode(activeRoom)
                                }.getOrElse { refreshPlaybackRoom(currentGateway, activeRoom) }
                            else -> refreshPlaybackRoom(currentGateway, activeRoom)
                        }
                    }
                }
                val endedRoom = room?.takeIf { it.state == "ended" && !it.currentMemberIsHost }
                if (endedRoom != null) {
                    if (mutableState.value.viewer.player != null) closePlayer()
                    mutableState.update { state ->
                        state.copy(viewer = state.viewer.copy(activePlaybackRoom = null))
                    }
                    runCatching { currentGateway.leavePlaybackRoom(endedRoom.id) }
                    room = null
                }
                mutableState.update { state ->
                    val currentViewer = state.viewer
                    state.copy(
                        viewer = currentViewer.copy(
                            playbackDevices = devices,
                            pendingPlaybackCommands = currentViewer.pendingPlaybackCommands + queuedCommands,
                            activePlaybackRoom = room,
                        ),
                    )
                }
                refreshOutgoingHandoffs(currentGateway)
                val latestViewer = mutableState.value.viewer
                val active = latestViewer.player != null || latestViewer.activePlaybackRoom != null ||
                    latestViewer.pendingPlaybackCommands.isNotEmpty() || coordinatedPlaybackLoading ||
                    pendingOutgoingHandoffs.isNotEmpty() || monotonicNowMilliseconds() < coordinationRecentUntilMilliseconds
                awaitCoordinationTick(coordinationDelayMilliseconds(active))
            }
        }
    }

    private suspend fun reportStoredPlaybackResult(
        currentGateway: RivuneGateway,
        operationId: UUID,
        result: StoredPlaybackOperation,
    ): Boolean {
        val status = result.status ?: return false
        val code = result.code ?: return false
        return runCatching {
            currentGateway.reportPlaybackCommandResult(operationId, PlaybackCommandResultInput(status, code))
        }.isSuccess
    }

    private suspend fun refreshOutgoingHandoffs(currentGateway: RivuneGateway) {
        for ((operationId, sourceSessionId) in pendingOutgoingHandoffs.toMap()) {
            val outgoing = runCatching { currentGateway.outgoingPlaybackCommand(operationId) }.getOrNull() ?: continue
            if (outgoing.status == PlaybackCommandStatus.PENDING) continue
            pendingOutgoingHandoffs.remove(operationId)
            if (outgoing.status == PlaybackCommandStatus.APPLIED && mutableState.value.viewer.player?.sessionId == sourceSessionId) {
                closePlayer()
            }
        }
    }

    internal fun playbackCommandExpired(expiresAt: String, now: Instant): Boolean =
        runCatching { !now.isBefore(Instant.parse(expiresAt)) }.getOrDefault(true)

    private suspend fun refreshPlaybackRoom(currentGateway: RivuneGateway, room: PlaybackRoom): PlaybackRoom? = try {
        currentGateway.playbackRoom(room.id).preservingJoinCode(room)
    } catch (cause: CancellationException) {
        throw cause
    } catch (cause: RivuneApiException.Server) {
        if (cause.status == 404 || cause.status == 403) null else room
    } catch (_: Throwable) {
        room
    }

    internal fun coordinationForegroundChanged(foreground: Boolean) {
        if (coordinationForeground == foreground) return
        coordinationForeground = foreground
        if (!foreground) {
            coordinationJob?.cancel()
            coordinationJob = null
        } else {
            gateway?.let(::startCoordination)
        }
    }

    internal fun coordinationDelayMilliseconds(active: Boolean): Long =
        if (active) COORDINATION_ACTIVE_INTERVAL_MILLISECONDS else COORDINATION_IDLE_INTERVAL_MILLISECONDS

    private fun stopCoordination() {
        coordinationJob?.cancel()
        coordinationJob = null
        coordinationEndedSessionId = null
        lastPlaybackOperationId = null
        coordinationEndingSessionId = null
        pendingOutgoingHandoffs.clear()
        coordinationPositionMs = 0
        coordinationDurationMs = 0
        coordinationPlaying = false
        coordinationRecentUntilMilliseconds = 0L
    }

    private fun applyPlayerProgress(player: PlayerPresentation, updated: PlaybackProgress) {
        val currentViewer = mutableState.value.viewer
        val currentPlayer = currentViewer.player
        if (currentPlayer != null && currentPlayer.sessionId != player.sessionId) return
        mutableState.value = mutableState.value.copy(
            viewer = currentViewer.copy(
                player = currentPlayer?.copy(expectedProgressVersion = updated.version),
                detail = currentViewer.detail?.let { detail ->
                    when {
                        detail.titleId == updated.titleId -> detail.copy(progress = updated)
                        updated.titleId in detail.episodeProgress -> detail.copy(
                            episodeProgress = detail.episodeProgress + (updated.titleId to updated),
                        )
                        else -> detail
                    }
                },
            ),
        )
    }

    private fun enterViewer() {
        val startupTab = appPreferences.snapshot().startupTab
        viewerRequestGeneration += 1
        metadataRefreshPending = false
        folderRequestGeneration += 1
        searchDescriptors = emptyList()
        mutableState.value = mutableState.value.copy(
            isBusy = true,
            collections = emptyList(),
            selectedCollectionId = null,
            openedCollectionId = null,
            resolvedFolder = null,
            calendarEvents = emptyList(),
            viewer = ViewerState(
                selectedTab = startupTab,
                playbackCoordinationAvailable = playbackCoordinationAvailable,
            ),
        )
        val currentGateway = gateway ?: return
        val profile = mutableState.value.activeProfile ?: return
        val operationGeneration = generation
        val requestGeneration = viewerRequestGeneration
        loadV22Features(currentGateway, profile, operationGeneration)
        viewModelScope.launch {
            val effective = try {
                currentGateway.effectiveProfileSettings(profile.id)
            } catch (cause: CancellationException) {
                throw cause
            } catch (_: Throwable) {
                null
            }
            if (!viewerRequestCurrent(operationGeneration, requestGeneration)) return@launch
            updateEffectiveSettings(effective)
            mutableState.value = mutableState.value.copy(isBusy = false)
            when (startupTab) {
                ViewerTab.HOME -> loadHomeContent()
                ViewerTab.SEARCH -> loadSearchDescriptors()
                ViewerTab.LIBRARY -> loadLibrary(reset = true)
                ViewerTab.CALENDAR -> loadCalendar()
            }
            startCoordination(currentGateway)
        }
    }

    private fun loadHomeContent() {
        val operationGeneration = generation
        val requestGeneration = ++viewerRequestGeneration
        val scope = activeOfflineScope
        viewModelScope.launch {
            val items = if (scope == null) emptyList() else withContext(Dispatchers.IO) {
                runCatching { offlineMediaStore?.items(scope).orEmpty() }.getOrDefault(emptyList())
            }
            if (viewerRequestCurrent(operationGeneration, requestGeneration)) {
                mutableState.update { state -> state.copy(viewer = state.viewer.copy(offlineItems = items)) }
            }
        }
        val currentGateway = gateway
        if (currentGateway == null) {
            mutableState.update { state -> state.copy(isBusy = false, viewer = state.viewer.copy(loading = null)) }
            return
        }
        val language = metadataLanguage()
        val preserveRenderedHome = mutableState.value.viewer.heroSlides.isNotEmpty() || mutableState.value.viewer.continueWatching.isNotEmpty()
        mutableState.value = mutableState.value.copy(isBusy = false, viewer = mutableState.value.viewer.copy(loading = ViewerLoading.HOME, inlineFailure = null))
        diagnostics.record(DiagnosticEventCode.CATALOG_REFRESH_STARTED)
        viewModelScope.launch {
            try {
                coroutineScope {
                    val collectionsTask = async { currentGateway.collections() }
                    val continueTask = async {
                        try {
                            currentGateway.continueWatching(30)
                        } catch (cause: CancellationException) {
                            throw cause
                        } catch (_: Throwable) {

                            null
                        }
                    }
                    val recommendationsTask = async {
                        if (!localRecommendationsAvailable) return@async emptyList()
                        try {
                            currentGateway.localRecommendations(30, RecommendationArtworkShape.LANDSCAPE).items
                        } catch (cause: CancellationException) {
                            throw cause
                        } catch (_: Throwable) {
                            emptyList()
                        }
                    }
                    val collections = collectionsTask.await()
                    if (!viewerRequestCurrent(operationGeneration, requestGeneration)) return@coroutineScope
                    val selected = mutableState.value.selectedCollectionId?.takeIf { id -> collections.any { it.id == id } } ?: collections.firstOrNull()?.id
                    if (!preserveRenderedHome) mutableState.update { state -> state.copy(
                        collections = collections,
                        selectedCollectionId = selected,
                        openedCollectionId = state.openedCollectionId?.takeIf { id -> collections.any { it.id == id } },
                    ) }
                    val homeResolutionTask = async {
                        resolveHomeCollections(currentGateway, collections, language) { collectionId, folder ->
                            if (!preserveRenderedHome && viewerRequestCurrent(operationGeneration, requestGeneration)) replaceCollectionFolder(collectionId, folder)
                        }
                    }
                    val continueTargets = continueTask.await()?.let(::mapContinueWatching)
                    if (!viewerRequestCurrent(operationGeneration, requestGeneration)) return@coroutineScope
                    val homeResolution = homeResolutionTask.await()
                    val recommendations = recommendationsTask.await()
                    if (!viewerRequestCurrent(operationGeneration, requestGeneration)) return@coroutineScope
                    mutableState.update { state -> state.copy(
                        collections = homeResolution.collections,
                        selectedCollectionId = selected,
                        openedCollectionId = state.openedCollectionId?.takeIf { id -> homeResolution.collections.any { it.id == id } },
                        viewer = state.viewer.copy(
                            continueWatching = continueTargets ?: state.viewer.continueWatching,
                            heroSlides = homeResolution.heroSlides,
                            recommendations = recommendations,
                            loading = null,
                        ),
                    ) }
                    diagnostics.record(DiagnosticEventCode.CATALOG_REFRESH_SUCCEEDED)
                }
            } catch (cause: CancellationException) {
                throw cause
            } catch (cause: Throwable) {
                diagnostics.record(DiagnosticEventCode.CATALOG_REFRESH_FAILED)
                viewerFailure(cause, operationGeneration, requestGeneration, UiFailure.CONTENT_LOAD)
            }
        }
    }
    private fun loadV22Features(currentGateway: RivuneGateway, profile: Profile, operationGeneration: Long) {
        V22FeatureKind.entries.forEach { kind ->
            loadV22Feature(kind, currentGateway, profile, operationGeneration)
        }
    }

    fun retryV22Feature(kind: V22FeatureKind) {
        val currentGateway = gateway ?: return
        val profile = mutableState.value.activeProfile ?: return
        loadV22Feature(kind, currentGateway, profile, generation)
    }

    private fun loadV22Feature(
        kind: V22FeatureKind,
        currentGateway: RivuneGateway,
        profile: Profile,
        operationGeneration: Long,
    ) {
        mutableState.update { state ->
            val features = state.viewer.features
            val pending = V22LoadState(loading = true)
            state.copy(viewer = state.viewer.copy(features = when (kind) {
                V22FeatureKind.QUEUE -> features.copy(queueLoad = pending)
                V22FeatureKind.SAVED_SEARCHES -> features.copy(savedSearchLoad = pending)
                V22FeatureKind.SMART_COLLECTIONS -> features.copy(smartCollectionLoad = pending)
                V22FeatureKind.INCIDENTS -> features.copy(incidentLoad = pending)
                V22FeatureKind.INBOX -> features.copy(inboxLoad = pending)
                V22FeatureKind.ACCESSIBILITY -> features.copy(accessibilityLoad = pending)
            }))
        }
        viewModelScope.launch {
            try {
                val result: Any = when (kind) {
                    V22FeatureKind.QUEUE -> currentGateway.readingQueue(profile.id)
                    V22FeatureKind.SAVED_SEARCHES -> currentGateway.savedSearches()
                    V22FeatureKind.SMART_COLLECTIONS -> currentGateway.smartCollections()
                    V22FeatureKind.INCIDENTS -> currentGateway.extensionIncidents()
                    V22FeatureKind.INBOX -> currentGateway.mediaNotificationSubscriptions() to currentGateway.mediaNotifications(limit = 30)
                    V22FeatureKind.ACCESSIBILITY -> currentGateway.profileAccessibilityPreferences(profile.id)
                }
                if (!isCurrent(operationGeneration) || mutableState.value.activeProfile?.id != profile.id) return@launch
                mutableState.update { state ->
                    val features = state.viewer.features
                    val loaded = V22LoadState(loaded = true)
                    @Suppress("UNCHECKED_CAST")
                    state.copy(viewer = state.viewer.copy(features = when (kind) {
                        V22FeatureKind.QUEUE -> features.copy(queue = result as io.rivune.api.ReadingQueue, queueLoad = loaded)
                        V22FeatureKind.SAVED_SEARCHES -> features.copy(savedSearches = result as List<io.rivune.api.SavedSearch>, savedSearchLoad = loaded)
                        V22FeatureKind.SMART_COLLECTIONS -> features.copy(smartCollections = result as List<io.rivune.api.SmartCollection>, smartCollectionLoad = loaded)
                        V22FeatureKind.INCIDENTS -> features.copy(incidents = result as List<io.rivune.api.AddonIncident>, incidentLoad = loaded)
                        V22FeatureKind.INBOX -> {
                            val inbox = result as Pair<List<io.rivune.api.MediaNotificationSubscription>, io.rivune.api.MediaNotificationPage>
                            features.copy(notificationSubscriptions = inbox.first, notifications = inbox.second.notifications, inboxLoad = loaded)
                        }
                        V22FeatureKind.ACCESSIBILITY -> features.copy(accessibility = result as io.rivune.api.AccessibilityPreferencesDocument, accessibilityLoad = loaded)
                    }))
                }
            } catch (cause: CancellationException) {
                throw cause
            } catch (cause: Throwable) {
                if (!isCurrent(operationGeneration) || mutableState.value.activeProfile?.id != profile.id) return@launch
                val failed = V22LoadState(failure = failureFor(cause, UiFailure.CONTENT_LOAD))
                mutableState.update { state ->
                    val features = state.viewer.features
                    state.copy(viewer = state.viewer.copy(features = when (kind) {
                        V22FeatureKind.QUEUE -> features.copy(queueLoad = failed)
                        V22FeatureKind.SAVED_SEARCHES -> features.copy(savedSearchLoad = failed)
                        V22FeatureKind.SMART_COLLECTIONS -> features.copy(smartCollectionLoad = failed)
                        V22FeatureKind.INCIDENTS -> features.copy(incidentLoad = failed)
                        V22FeatureKind.INBOX -> features.copy(inboxLoad = failed)
                        V22FeatureKind.ACCESSIBILITY -> features.copy(accessibilityLoad = failed)
                    }))
                }
            }
        }
    }

    private fun setFeatureMutationState(busy: Boolean, failure: UiFailure? = null, conflict: Boolean = false) {
        mutableState.update { state ->
            state.copy(viewer = state.viewer.copy(features = state.viewer.features.copy(
                mutationInFlight = busy,
                failure = failure,
                conflict = conflict,
            )))
        }
    }

    private suspend fun <T> retryIdempotentMutation(block: suspend () -> T): T = try {
        block()
    } catch (cause: IOException) {
        block()
    }

    private fun featureMutationFailure(cause: Throwable) {
        val conflict = cause is RivuneApiException.Server && cause.status == 409
        setFeatureMutationState(false, if (conflict) UiFailure.ACTION else failureFor(cause, UiFailure.ACTION), conflict)
    }

    fun addDetailToQueue() {
        val currentGateway = gateway ?: return
        val profile = mutableState.value.activeProfile ?: return
        val detail = mutableState.value.viewer.detail ?: return
        val queue = mutableState.value.viewer.features.queue ?: return
        val mediaType = when (detail.target.mediaType.lowercase(Locale.ROOT)) {
            "movie" -> io.rivune.api.ReadingQueueMediaType.MOVIE
            "series" -> io.rivune.api.ReadingQueueMediaType.SERIES
            "episode" -> io.rivune.api.ReadingQueueMediaType.EPISODE
            "tv" -> io.rivune.api.ReadingQueueMediaType.TV
            else -> return
        }
        val operationId = UUID.randomUUID()
        val input = io.rivune.api.ReadingQueueAddInput(
            operationId = operationId,
            expectedRevision = queue.revision,
            mediaType = mediaType,
            resourceId = detail.target.resourceId,
            sourceAddonId = detail.target.sourceAddonId,
            titleId = detail.titleId,
            title = detail.target.title,
            posterUrl = detail.target.posterUrl,
        )
        setFeatureMutationState(true)
        viewModelScope.launch {
            try {
                retryIdempotentMutation { currentGateway.addReadingQueueItem(profile.id, input) }
                val updated = currentGateway.readingQueue(profile.id)
                if (mutableState.value.activeProfile?.id == profile.id) mutableState.update { state ->
                    state.copy(viewer = state.viewer.copy(features = state.viewer.features.copy(
                        queue = updated, mutationInFlight = false, failure = null, conflict = false,
                    )))
                }
            } catch (cause: CancellationException) {
                throw cause
            } catch (cause: Throwable) {
                featureMutationFailure(cause)
            }
        }
    }

    fun removeQueueItem(item: io.rivune.api.ReadingQueueItem) {
        val currentGateway = gateway ?: return
        val profile = mutableState.value.activeProfile ?: return
        val queue = mutableState.value.viewer.features.queue ?: return
        val input = io.rivune.api.ReadingQueueMutationInput(UUID.randomUUID(), queue.revision)
        setFeatureMutationState(true)
        viewModelScope.launch {
            try {
                retryIdempotentMutation { currentGateway.removeReadingQueueItem(profile.id, item.id, input) }
                val updated = currentGateway.readingQueue(profile.id)
                if (mutableState.value.activeProfile?.id == profile.id) mutableState.update { state ->
                    state.copy(viewer = state.viewer.copy(features = state.viewer.features.copy(
                        queue = updated, mutationInFlight = false, failure = null, conflict = false,
                    )))
                }
            } catch (cause: CancellationException) { throw cause } catch (cause: Throwable) { featureMutationFailure(cause) }
        }
    }

    fun saveCurrentSearch() {
        val currentGateway = gateway ?: return
        val query = mutableState.value.viewer.search.query.trim().takeIf { it.length >= 2 } ?: return
        val input = io.rivune.api.SavedSearchInput(query.take(120), query.take(256), sort = io.rivune.api.SavedSearchSort.RELEVANCE)
        setFeatureMutationState(true)
        viewModelScope.launch {
            try {
                val created = currentGateway.createSavedSearch(input)
                mutableState.update { state -> state.copy(viewer = state.viewer.copy(features = state.viewer.features.copy(
                    savedSearches = (state.viewer.features.savedSearches.filterNot { it.id == created.id } + created).sortedBy { it.name },
                    mutationInFlight = false, failure = null, conflict = false,
                ))) }
            } catch (cause: CancellationException) { throw cause } catch (cause: Throwable) { featureMutationFailure(cause) }
        }
    }

    fun runSavedSearch(saved: io.rivune.api.SavedSearch) = search(saved.query)

    fun deleteSavedSearch(saved: io.rivune.api.SavedSearch) {
        val currentGateway = gateway ?: return
        setFeatureMutationState(true)
        viewModelScope.launch {
            try {
                currentGateway.deleteSavedSearch(saved.id, saved.revision)
                mutableState.update { state -> state.copy(viewer = state.viewer.copy(features = state.viewer.features.copy(
                    savedSearches = state.viewer.features.savedSearches.filterNot { it.id == saved.id },
                    mutationInFlight = false, failure = null, conflict = false,
                ))) }
            } catch (cause: CancellationException) { throw cause } catch (cause: Throwable) { featureMutationFailure(cause) }
        }
    }

    fun createSmartCollectionForLibrary() {
        val currentGateway = gateway ?: return
        val mediaType = when (mutableState.value.viewer.library.mediaType) {
            "movie" -> io.rivune.api.CatalogMediaType.MOVIE
            "series" -> io.rivune.api.CatalogMediaType.SERIES
            "tv" -> io.rivune.api.CatalogMediaType.TV
            else -> return
        }
        val name = mediaType.name.lowercase().replaceFirstChar(Char::uppercase)
        val input = io.rivune.api.SmartCollectionInput(
            name = name,
            rules = io.rivune.api.SmartRule.MediaType(values = listOf(mediaType)),
            sort = io.rivune.api.SmartCollectionSort.TITLE,
        )
        setFeatureMutationState(true)
        viewModelScope.launch {
            try {
                val created = currentGateway.createSmartCollection(input)
                mutableState.update { state -> state.copy(viewer = state.viewer.copy(features = state.viewer.features.copy(
                    smartCollections = (state.viewer.features.smartCollections.filterNot { it.id == created.id } + created).sortedBy { it.name },
                    mutationInFlight = false, failure = null, conflict = false,
                ))) }
            } catch (cause: CancellationException) { throw cause } catch (cause: Throwable) { featureMutationFailure(cause) }
        }
    }

    fun openSmartCollection(collection: io.rivune.api.SmartCollection) {
        val currentGateway = gateway ?: return
        setFeatureMutationState(true)
        viewModelScope.launch {
            try {
                val page = currentGateway.evaluateSmartCollection(collection.id)
                mutableState.update { state -> state.copy(viewer = state.viewer.copy(features = state.viewer.features.copy(
                    smartCollectionPage = page, mutationInFlight = false, failure = null,
                ))) }
            } catch (cause: CancellationException) { throw cause } catch (cause: Throwable) { featureMutationFailure(cause) }
        }
    }

    fun deleteSmartCollection(collection: io.rivune.api.SmartCollection) {
        val currentGateway = gateway ?: return
        setFeatureMutationState(true)
        viewModelScope.launch {
            try {
                currentGateway.deleteSmartCollection(collection.id, collection.revision)
                mutableState.update { state -> state.copy(viewer = state.viewer.copy(features = state.viewer.features.copy(
                    smartCollections = state.viewer.features.smartCollections.filterNot { it.id == collection.id },
                    smartCollectionPage = null,
                    mutationInFlight = false,
                    failure = null,
                    conflict = false,
                ))) }
            } catch (cause: CancellationException) {
                throw cause
            } catch (cause: Throwable) {
                featureMutationFailure(cause)
            }
        }
    }
    fun acknowledgeIncident(incident: io.rivune.api.AddonIncident) {
        val currentGateway = gateway ?: return
        setFeatureMutationState(true)
        viewModelScope.launch {
            try {
                val updated = currentGateway.acknowledgeExtensionIncident(incident.id)
                mutableState.update { state -> state.copy(viewer = state.viewer.copy(features = state.viewer.features.copy(
                    incidents = state.viewer.features.incidents.map { if (it.id == updated.id) updated else it },
                    mutationInFlight = false, failure = null,
                ))) }
            } catch (cause: CancellationException) { throw cause } catch (cause: Throwable) { featureMutationFailure(cause) }
        }
    }

    fun acknowledgeMediaNotification(notification: io.rivune.api.MediaNotification, dismiss: Boolean) {
        val currentGateway = gateway ?: return
        val acknowledgement = if (dismiss) io.rivune.api.MediaNotificationAcknowledgementState.DISMISSED else io.rivune.api.MediaNotificationAcknowledgementState.READ
        setFeatureMutationState(true)
        viewModelScope.launch {
            try {
                currentGateway.acknowledgeMediaNotification(notification.id, acknowledgement)
                mutableState.update { state -> state.copy(viewer = state.viewer.copy(features = state.viewer.features.copy(
                    notifications = if (dismiss) state.viewer.features.notifications.filterNot { it.id == notification.id }
                    else state.viewer.features.notifications.map { if (it.id == notification.id) it.copy(readAt = instantNow().toString()) else it },
                    mutationInFlight = false, failure = null,
                ))) }
            } catch (cause: CancellationException) { throw cause } catch (cause: Throwable) { featureMutationFailure(cause) }
        }
    }

    fun toggleMediaNotifications() {
        val currentGateway = gateway ?: return
        val detail = mutableState.value.viewer.detail ?: return
        val existing = mutableState.value.viewer.features.notificationSubscriptions.firstOrNull { it.titleId == detail.titleId }
        setFeatureMutationState(true)
        viewModelScope.launch {
            try {
                if (existing == null) {
                    val followed = currentGateway.followMediaNotifications(
                        detail.titleId,
                        io.rivune.api.MediaNotificationFollowInput(java.time.ZoneId.systemDefault().id, horizonDays = 90, leadDays = 1),
                    )
                    mutableState.update { state -> state.copy(viewer = state.viewer.copy(features = state.viewer.features.copy(
                        notificationSubscriptions = state.viewer.features.notificationSubscriptions + followed,
                        mutationInFlight = false, failure = null,
                    ))) }
                } else {
                    currentGateway.unfollowMediaNotifications(detail.titleId)
                    mutableState.update { state -> state.copy(viewer = state.viewer.copy(features = state.viewer.features.copy(
                        notificationSubscriptions = state.viewer.features.notificationSubscriptions.filterNot { it.titleId == detail.titleId },
                        mutationInFlight = false, failure = null,
                    ))) }
                }
            } catch (cause: CancellationException) { throw cause } catch (cause: Throwable) { featureMutationFailure(cause) }
        }
    }

    fun updateAccessibility(transform: (io.rivune.api.AccessibilityPreferencesDocument) -> io.rivune.api.AccessibilityPreferencesDocument) {
        val currentGateway = gateway ?: return
        val profile = mutableState.value.activeProfile ?: return
        val current = mutableState.value.viewer.features.accessibility ?: return
        val input = transform(current).copy(revision = current.revision)
        setFeatureMutationState(true)
        viewModelScope.launch {
            try {
                val updated = currentGateway.updateProfileAccessibilityPreferences(profile.id, input)
                if (mutableState.value.activeProfile?.id == profile.id) mutableState.update { state -> state.copy(viewer = state.viewer.copy(features = state.viewer.features.copy(
                    accessibility = updated, mutationInFlight = false, failure = null, conflict = false,
                ))) }
            } catch (cause: CancellationException) { throw cause } catch (cause: Throwable) {
                if (cause is RivuneApiException.Server && cause.status == 409) {
                    val latest = try { currentGateway.profileAccessibilityPreferences(profile.id) } catch (_: Throwable) { null }
                    if (latest != null && mutableState.value.activeProfile?.id == profile.id) mutableState.update { state -> state.copy(viewer = state.viewer.copy(features = state.viewer.features.copy(accessibility = latest))) }
                }
                featureMutationFailure(cause)
            }
        }
    }

    private suspend fun resolveHomeCollections(
        currentGateway: RivuneGateway,
        collections: List<Collection>,
        language: String?,
        onResolved: ((UUID, CollectionFolder) -> Unit)? = null,
    ): HomeResolution = coroutineScope {
        val semaphore = Semaphore(COLLECTION_ARTWORK_CONCURRENCY)
        val pending = collections.map { collection ->
            collection to collection.folders.map { folder ->
                async {
                    val folderId = folder.id
                    val needsHeroItems = collection.heroEnabled
                    val needsArtwork = folder.coverImageUrl.isNullOrBlank()
                    if (folderId == null || (!needsHeroItems && !needsArtwork)) {
                        null
                    } else {
                        semaphore.withPermit {
                            try {
                                if (needsHeroItems) {
                                    currentGateway.resolveCollectionFolder(collection.id, folderId, page = 1, language = language)
                                } else {
                                    currentGateway.resolveCollectionFolderArtwork(collection.id, folderId, language)
                                }.also { onResolved?.invoke(collection.id, it.folder) }
                            } catch (cause: CancellationException) {
                                throw cause
                            } catch (_: Throwable) {
                                null
                            }
                        }
                    }
                }
            }
        }
        val seen = HashSet<String>()
        val heroSlides = ArrayList<HomeHeroSlide>(HOME_HERO_SLIDE_LIMIT)
        val hydratedCollections = pending.map { (collection, folders) ->
            val resolvedFolders = folders.awaitAll()
            collection.copy(folders = collection.folders.mapIndexed { index, folder ->
                resolvedFolders[index]?.folder ?: folder
            }).also {
                if (collection.heroEnabled && heroSlides.size < HOME_HERO_SLIDE_LIMIT) {
                    for (resolved in resolvedFolders) {
                        if (resolved == null || heroSlides.size == HOME_HERO_SLIDE_LIMIT) continue
                        for (item in resolved.items) {
                            if (seen.add(homeHeroIdentity(item))) {
                                heroSlides += HomeHeroSlide(
                                    item = item,
                                    fallbackBackdropUrl = resolved.folder.heroBackdropUrl ?: collection.backdropImageUrl,
                                    fallbackLogoUrl = resolved.folder.titleLogoUrl,
                                )
                                if (heroSlides.size == HOME_HERO_SLIDE_LIMIT) break
                            }
                        }
                    }
                }
            }
        }
        HomeResolution(hydratedCollections, heroSlides)
    }

    private fun homeHeroIdentity(item: CollectionItem): String = if (
        item.mediaType == "movie" || item.mediaType == "series" || item.mediaType == "episode"
    ) {
        "${item.mediaType}:${item.id}"
    } else {
        val addonId = item.sources.firstNotNullOfOrNull { it.addonId }?.toString().orEmpty()
        "${item.mediaType}:$addonId:${item.id}"
    }

    private fun replaceCollectionFolder(collectionId: UUID, folder: CollectionFolder) {
        val current = mutableState.value
        val collections = current.collections.map { collection ->
            if (collection.id != collectionId) collection else collection.copy(
                folders = collection.folders.map { existing ->
                    if (existing.id == folder.id) folder else existing
                },
            )
        }
        if (collections != current.collections) mutableState.value = current.copy(collections = collections)
    }

    private fun mapContinueWatching(page: ContinueWatchingPage): List<MediaTarget> = page.items.map { item ->
        val payloadResourceId = item.resourceId?.takeIf(String::isNotBlank)
        val resourceId = payloadResourceId ?: item.titleId.toString()
        val provider = item.resourceProvider
            ?.trim()
            ?.takeIf(String::isNotBlank)
            ?.lowercase(Locale.ROOT)
        val parsedMappingProvider = item.mappingProvider
            ?.trim()
            ?.takeIf(String::isNotBlank)
            ?.let { value ->
                SeriesMappingProvider.entries.firstOrNull { it.wireValue.equals(value, ignoreCase = true) }
            }
        val episodeOrderId = item.episodeOrderId?.trim()?.takeIf(String::isNotBlank)
        val metadataSeasonId = item.metadataSeasonId?.trim()?.takeIf(String::isNotBlank)
        val mappingProvider = parsedMappingProvider.takeIf {
            it == SeriesMappingProvider.TVDB && episodeOrderId != null && metadataSeasonId != null
        }
        val isEpisode = item.mediaType == PlaybackProgressMediaType.EPISODE
        val episodeTitle = item.episodeTitle?.takeIf(String::isNotBlank)
            ?: item.episodeNumber?.let { "Episode $it" }
        val title = if (isEpisode) {
            listOfNotNull(item.title?.takeIf(String::isNotBlank), episodeTitle)
                .joinToString(" · ")
                .ifBlank { "Episode" }
        } else {
            item.title?.takeIf(String::isNotBlank) ?: "Continue watching"
        }
        val episodeStillUrl = item.episodeStillUrl?.takeIf(String::isNotBlank)
        MediaTarget(
            id = item.titleId.toString(),
            resourceId = resourceId,
            mediaType = item.mediaType.name.lowercase(),
            title = title,
            titleId = item.titleId,
            provider = provider,
            externalIds = if (provider != null && payloadResourceId != null) {
                mapOf(provider to payloadResourceId)
            } else {
                emptyMap()
            },
            posterUrl = if (isEpisode) episodeStillUrl ?: item.posterUrl else item.posterUrl,
            backgroundUrl = if (isEpisode) episodeStillUrl ?: item.backgroundUrl ?: item.posterUrl else item.backgroundUrl,
            releaseInfo = if (isEpisode) item.episodeAirDate ?: item.releaseInfo else item.releaseInfo,
            released = if (isEpisode) item.episodeAirDate else null,
            seriesId = item.seriesId,
            mappingProvider = mappingProvider,
            episodeOrderId = episodeOrderId.takeIf { mappingProvider != null },
            metadataSeasonId = metadataSeasonId.takeIf { mappingProvider != null },
            seasonId = item.seasonId?.toString(),
            seasonNumber = item.seasonNumber,
            episodeNumber = item.episodeNumber,
            resumePositionSeconds = item.positionSeconds,
            durationSeconds = item.durationSeconds,
        )
    }

    private fun loadSearchDescriptors() {
        val currentGateway = gateway ?: return
        val operationGeneration = generation
        val requestGeneration = ++viewerRequestGeneration
        mutableState.value = mutableState.value.copy(viewer = mutableState.value.viewer.copy(loading = ViewerLoading.SEARCH, inlineFailure = null))
        viewModelScope.launch {
            try {
                val descriptors = currentGateway.addonCatalogs()
                if (!viewerRequestCurrent(operationGeneration, requestGeneration)) return@launch
                searchDescriptors = descriptors
                mutableState.value = mutableState.value.copy(viewer = mutableState.value.viewer.copy(loading = null))
            } catch (cause: CancellationException) {
                throw cause
            } catch (cause: Throwable) {
                viewerFailure(cause, operationGeneration, requestGeneration, UiFailure.CONTENT_LOAD)
            }
        }
    }
    private fun runSearch(query: String, skip: Int, append: Boolean) {
        val currentGateway = gateway ?: return
        val operationGeneration = generation
        val language = metadataLanguage()
        val region = runCatching(localeProvider).getOrNull()?.country?.trim()?.uppercase().takeUnless { it.isNullOrEmpty() }
        val requestGeneration = ++viewerRequestGeneration
        mutableState.value = mutableState.value.copy(
            viewer = mutableState.value.viewer.copy(
                loading = if (append) ViewerLoading.SEARCH_MORE else ViewerLoading.SEARCH,
                inlineFailure = null,
            ),
        )
        val diagnosticOperation = SearchDiagnosticOperation(diagnostics)
        searchJob = viewModelScope.launch {
            var hasPublishedResults = false
            val pendingItems = mutableListOf<MediaTarget>()
            var coalescedPublication: Job? = null

            fun publishItems(incoming: List<MediaTarget>): Boolean {
                if (incoming.isEmpty() || !viewerRequestCurrent(operationGeneration, requestGeneration)) return false
                val viewer = mutableState.value.viewer
                val merged = mergeSearchMediaTargets(viewer.search.items, incoming)
                if (merged == viewer.search.items) return false
                mutableState.value = mutableState.value.copy(
                    viewer = viewer.copy(search = viewer.search.copy(items = merged)),
                )
                return true
            }

            fun publishPendingItems() {
                coalescedPublication = null
                if (pendingItems.isEmpty()) return
                val incoming = pendingItems.toList()
                pendingItems.clear()
                publishItems(incoming)
            }

            fun enqueueItems(incoming: List<MediaTarget>) {
                if (incoming.isEmpty() || !viewerRequestCurrent(operationGeneration, requestGeneration)) return
                if (!hasPublishedResults) {
                    hasPublishedResults = publishItems(incoming)
                    if (hasPublishedResults) return
                }
                pendingItems += incoming
                if (coalescedPublication == null) {
                    coalescedPublication = launch {
                        delay(SEARCH_PUBLICATION_WINDOW_MILLISECONDS)
                        publishPendingItems()
                    }
                }
            }

            try {
                val semanticDeferred = async {
                    semanticSearchOutcome(
                        currentGateway,
                        SemanticSearchRequest(
                            query = query,
                            language = language,
                            region = region,
                            page = if (append) mutableState.value.viewer.search.page + 1 else 1,
                            limit = minOf(SEARCH_PAGE_SIZE, 40),
                        ),
                    )
                }
                val descriptors = if (searchDescriptors.isEmpty()) currentGateway.addonCatalogs() else searchDescriptors
                searchDescriptors = descriptors
                val configured = descriptors.asSequence()
                    .filter { it.searchable }
                    .map { it.catalog.type }
                    .toList()
                val (configuredTypes, configuredTypesTruncated) = boundedStableSearchTypes(configured, MAX_SEARCH_ADDON_TYPES)
                val fanoutBudget = SearchFanoutBudget(MAX_SEARCH_ADDON_REQUESTS)
                val publishAddonOutcome: (Result<AddonResourceBatch>) -> Unit = { outcome ->
                    enqueueItems(outcome.getOrNull()?.toMediaTargets(descriptors).orEmpty())
                }
                val speculativeAddonSearch = async {
                    searchAddonCatalogOutcomes(currentGateway, configuredTypes, query, skip, language, fanoutBudget, publishAddonOutcome)
                }
                val semanticOutcome = semanticDeferred.await()
                val semantic = semanticOutcome.page
                if (semantic != null && viewerRequestCurrent(operationGeneration, requestGeneration)) {
                    enqueueItems(semantic.items.map(CollectionItem::toSemanticMediaTarget))
                    val viewer = mutableState.value.viewer
                    if (viewerRequestCurrent(operationGeneration, requestGeneration) && viewer.search.intents != semantic.intents) {
                        mutableState.value = mutableState.value.copy(
                            viewer = viewer.copy(search = viewer.search.copy(intents = semantic.intents)),
                        )
                    }
                }
                val inferredTypes = semantic?.mediaTypes.orEmpty().map(String::lowercase).toSet()
                val inferredConfiguredTypes = configuredTypes.filter(inferredTypes::contains)
                val types = inferredConfiguredTypes.ifEmpty { configuredTypes }
                val residualQuery = semantic?.titleQuery?.trim()
                val addonQuery = residualQuery?.takeIf { it.length >= 2 } ?: query
                val results = if (types == configuredTypes && addonQuery == query) {
                    speculativeAddonSearch.await()
                } else {
                    speculativeAddonSearch.cancelAndJoin()
                    searchAddonCatalogOutcomes(currentGateway, types, addonQuery, skip, language, fanoutBudget, publishAddonOutcome)
                }
                if (!viewerRequestCurrent(operationGeneration, requestGeneration)) return@launch
                coalescedPublication?.cancelAndJoin()
                coalescedPublication = null
                val batches = results.mapNotNull { it.getOrNull() }
                if (batches.isEmpty() && results.any { it.isFailure } && semantic?.items.isNullOrEmpty() &&
                    mutableState.value.viewer.search.items.isEmpty()
                ) {
                    throw RivuneApiException.InvalidResponse()
                }
                val directItems = batches.flatMap { it.toMediaTargets(descriptors) }
                    .filter { inferredConfiguredTypes.isEmpty() || it.mediaType.lowercase() in inferredConfiguredTypes }
                val semanticItems = semantic?.items.orEmpty().map(CollectionItem::toSemanticMediaTarget)
                val viewer = mutableState.value.viewer
                val merged = mergeSearchMediaTargets(viewer.search.items, pendingItems + directItems + semanticItems)
                pendingItems.clear()
                val completedSearch = SearchState(
                    query = query,
                    items = merged,
                    intents = semantic?.intents ?: if (append) viewer.search.intents else emptyList(),
                    page = semantic?.page ?: if (append) viewer.search.page + 1 else 1,
                    hasMore = semantic?.hasMore == true || batches.any { it.hasFullPage(SEARCH_PAGE_SIZE) },
                    partial = (append && viewer.search.partial) || configuredTypesTruncated || fanoutBudget.truncated ||
                        semanticOutcome.failed || semantic?.partial == true || results.any { it.isFailure } || batches.any { it.errors.isNotEmpty() },
                )
                mutableState.value = mutableState.value.copy(
                    viewer = viewer.copy(
                        search = completedSearch,
                        loading = null,
                        inlineFailure = null,
                    ),
                )
                diagnosticOperation.finish(
                    if (completedSearch.partial) DiagnosticEventCode.SEARCH_PARTIAL
                    else DiagnosticEventCode.SEARCH_SUCCEEDED,
                )
            } catch (cause: CancellationException) {
                diagnosticOperation.finish(DiagnosticEventCode.SEARCH_CANCELED)
                throw cause
            } catch (cause: Throwable) {
                diagnosticOperation.finish(DiagnosticEventCode.SEARCH_FAILED)
                viewerFailure(cause, operationGeneration, requestGeneration, UiFailure.CONTENT_LOAD)
            }
        }
        searchJob?.invokeOnCompletion { cause ->
            if (cause is CancellationException) diagnosticOperation.finish(DiagnosticEventCode.SEARCH_CANCELED)
            else if (cause != null) diagnosticOperation.finish(DiagnosticEventCode.SEARCH_FAILED)
        }
    }
    private suspend fun searchAddonCatalogOutcomes(
        currentGateway: RivuneGateway,
        types: List<String>,
        query: String,
        skip: Int,
        language: String?,
        budget: SearchFanoutBudget,
        onOutcome: (Result<AddonResourceBatch>) -> Unit,
    ): List<Result<AddonResourceBatch>> = coroutineScope {
        val semaphore = Semaphore(MAX_SEARCH_ADDON_CONCURRENCY)
        types.map { type ->
            async {
                semaphore.withPermit {
                    if (!budget.tryAcquire()) return@withPermit null
                    val outcome = try {
                        Result.success(currentGateway.searchAddonCatalogs(type, query, skip, SEARCH_PAGE_SIZE, language))
                    } catch (cause: CancellationException) {
                        throw cause
                    } catch (cause: Throwable) {
                        Result.failure(cause)
                    }
                    onOutcome(outcome)
                    outcome
                }
            }
        }.awaitAll().filterNotNull()
    }


    private suspend fun semanticSearchOutcome(
        currentGateway: RivuneGateway,
        request: SemanticSearchRequest,
    ): SemanticSearchOutcome {
        if (!semanticSearchAvailable) return SemanticSearchOutcome(page = null, failed = false)
        return try {
            val page = withTimeoutOrNull(SEMANTIC_SEARCH_TIMEOUT_MILLISECONDS) {
                currentGateway.semanticSearch(request)
            }
            SemanticSearchOutcome(page = page, failed = page == null)
        } catch (cause: CancellationException) {
            throw cause
        } catch (_: Throwable) {
            SemanticSearchOutcome(page = null, failed = true)
        }
    }

    private fun loadLibrary(reset: Boolean) {
        val currentGateway = gateway ?: return
        val operationGeneration = generation
        val requestGeneration = ++viewerRequestGeneration
        val current = mutableState.value.viewer.library
        val type = when (current.mediaType) {
            "movie" -> TitleMediaType.MOVIE
            "series" -> TitleMediaType.SERIES
            "tv" -> TitleMediaType.TV
            else -> null
        }
        val page = if (reset) 1 else current.page + 1
        mutableState.value = mutableState.value.copy(
            viewer = mutableState.value.viewer.copy(
                loading = if (reset) ViewerLoading.LIBRARY else ViewerLoading.LIBRARY_MORE,
                inlineFailure = null,
            ),
        )
        viewModelScope.launch {
            try {
                val descriptors = if (searchDescriptors.isNotEmpty()) {
                    searchDescriptors
                } else {
                    try {
                        currentGateway.addonCatalogs().also { searchDescriptors = it }
                    } catch (cause: CancellationException) {
                        throw cause
                    } catch (_: Throwable) {
                        emptyList()
                    }
                }
                val availableTypes = current.availableTypes.toMutableSet()
                availableTypes += descriptors.asSequence()
                    .filter { !it.addonCatalog }
                    .map { it.catalog.type.trim() }
                    .filter { it in setOf("movie", "series", "tv") }
                val response = currentGateway.library(type, page, LIBRARY_PAGE_SIZE)
                if (!viewerRequestCurrent(operationGeneration, requestGeneration)) return@launch
                val existing = if (reset) emptyList() else mutableState.value.viewer.library.items
                val seen = existing.mapTo(mutableSetOf()) { it.titleId }
                val additions = response.items.filter { seen.add(it.titleId) }
                mutableState.value = mutableState.value.copy(
                    viewer = mutableState.value.viewer.copy(
                        library = LibraryState(
                            items = existing + additions,
                            page = response.page,
                            totalPages = response.totalPages,
                            totalResults = response.totalResults,
                            mediaType = current.mediaType,
                            availableTypes = availableTypes,
                        ),
                        loading = null,
                        inlineFailure = null,
                    ),
                )
            } catch (cause: CancellationException) {
                throw cause
            } catch (cause: Throwable) {
                viewerFailure(cause, operationGeneration, requestGeneration, UiFailure.CONTENT_LOAD)
            }
        }
    }

    private fun loadCalendar() {
        val currentGateway = gateway ?: return
        val operationGeneration = generation
        val language = metadataLanguage()
        val requestGeneration = ++viewerRequestGeneration
        val month = mutableState.value.calendarMonth
        mutableState.value = mutableState.value.copy(viewer = mutableState.value.viewer.copy(loading = ViewerLoading.CALENDAR, inlineFailure = null))
        viewModelScope.launch {
            try {
                val events = currentGateway.calendar(month.atDay(1).toString(), month.atEndOfMonth().toString(), language)
                if (!viewerRequestCurrent(operationGeneration, requestGeneration) || mutableState.value.calendarMonth != month) return@launch
                mutableState.value = mutableState.value.copy(
                    calendarEvents = events,
                    viewer = mutableState.value.viewer.copy(loading = null, inlineFailure = null),
                )
            } catch (cause: CancellationException) {
                throw cause
            } catch (cause: Throwable) {
                viewerFailure(cause, operationGeneration, requestGeneration, UiFailure.CONTENT_LOAD)
            }
        }
    }

    private fun loadPlaybackSources(
        target: MediaTarget,
        titleId: UUID,
        progress: PlaybackProgress?,
        continuationTarget: PlaybackTargetSelection? = null,
        continuationSource: io.rivune.api.PlaybackSourceOption? = null,
        continuationSourceWasUnique: Boolean = true,
        startOverrideMs: Long? = null,
        showPicker: Boolean = true,
        episodeContextDetail: MediaDetailState? = null,
        autoStart: Boolean = false,
        forceEmbedded: Boolean = false,
        onResult: ((Boolean) -> Unit)? = null,
    ) {
        val currentGateway = gateway ?: run {
            onResult?.invoke(false)
            return
        }
        refreshExternalPlaybackSupport()
        val support = externalPlaybackSupport
        val detail = episodeContextDetail ?: mutableState.value.viewer.detail
        val operationGeneration = generation
        val requestGeneration = viewerRequestGeneration
        val sourceGeneration = ++sourceRequestGeneration
        val pendingPicker = SourcePickerState(
            target = target,
            titleId = titleId,
            progress = progress,
            options = emptyList(),
            partial = false,
        )
        val currentPicker = mutableState.value.viewer.sourcePicker
        mutableState.value = mutableState.value.copy(
            viewer = mutableState.value.viewer.copy(
                loading = if (showPicker || continuationTarget != null) ViewerLoading.SOURCES else null,
                sourcePicker = if (continuationTarget == null) pendingPicker else currentPicker,
                sourcePickerVisible = showPicker || mutableState.value.viewer.sourcePickerVisible,
            ),
        )
        viewModelScope.launch {
            try {
                val episodeContext = try {
                    resolveEpisodePlaybackContext(currentGateway, target, titleId, detail)
                } catch (cause: CancellationException) {
                    throw cause
                } catch (_: Throwable) {
                    EpisodePlaybackContext()
                }
                val preferences = appPreferences.snapshot()
                val detectedNetwork = runCatching(playbackNetworkProvider)
                    .getOrDefault(NetworkClass.MOBILE)
                val network = if (
                    detectedNetwork == NetworkClass.REMOTE_WIFI &&
                    isKnownLocalNetworkServerUrl(mutableState.value.serverInput)
                ) NetworkClass.LOCAL else detectedNetwork
                val quality = when (network) {
                    NetworkClass.LOCAL -> preferences.localQuality
                    NetworkClass.REMOTE_WIFI -> preferences.remoteWifiQuality
                    NetworkClass.MOBILE -> preferences.mobileQuality
                }
                val capabilities = playbackCapabilitiesFor(
                    if (forceEmbedded) PreferredPlayer.Rivune else preferences.preferredPlayer,
                    preferences.embeddedPlayerPreference,
                )
                    .withQualityLimit(playbackQualityLimit(quality, network))
                    .copy(externalPlayers = if (forceEmbedded) null else support.capabilityIds.ifEmpty { null })
                val sources = currentGateway.playbackSources(
                    mediaType = target.mediaType,
                    resourceId = target.resourceId,
                    capabilities = capabilities,
                    addonId = target.sourceAddonId.takeIf { target.mediaType == "tv" },
                )
                if (!sourceRequestCurrent(operationGeneration, requestGeneration, sourceGeneration)) {
                    onResult?.invoke(false)
                    return@launch
                }
                if (sources.sources.isEmpty()) throw IllegalStateException("No playback source")
                val picker = SourcePickerState(
                    target = target,
                    titleId = titleId,
                    progress = progress,
                    options = sources.sources,
                    partial = sources.providerErrors.isNotEmpty(),
                    nextEpisode = episodeContext.nextEpisode,
                    markerRequest = episodeContext.markerRequest,
                )
                val currentViewer = mutableState.value.viewer
                val pickerVisible = currentViewer.sourcePickerVisible
                mutableState.value = mutableState.value.copy(
                    viewer = currentViewer.copy(
                        sourcePicker = picker,
                        sourcePickerVisible = pickerVisible,
                        loading = if (currentViewer.loading == ViewerLoading.SOURCES) null else currentViewer.loading,
                        inlineFailure = if (currentViewer.inlineFailure == UiFailure.PLAYBACK) null else currentViewer.inlineFailure,
                    ),
                )
                if (autoStart) {
                    val preferredTarget = if (forceEmbedded) {
                        PlaybackTargetSelection.Embedded(preferences.embeddedPlayerPreference)
                    } else {
                        when (
                            val target = preferredPlaybackTarget(
                                preferences.preferredPlayer,
                                preferences.embeddedPlayerPreference,
                                picker.options.first(),
                                support,
                            )
                        ) {
                            PreferredPlaybackTarget.Ask -> PlaybackTargetSelection.Embedded(EmbeddedPlayerPreference.AUTOMATIC)
                            is PreferredPlaybackTarget.Embedded -> PlaybackTargetSelection.Embedded(target.preference)
                            is PreferredPlaybackTarget.External -> PlaybackTargetSelection.External(target.player)
                        }
                    }
                    startPlayback(picker, picker.options.first(), preferredTarget, startOverrideMs, onResult)
                    return@launch
                }
                val continuation = continuationPlaybackSelection(continuationTarget, continuationSource, continuationSourceWasUnique, picker.options, support)
                if (continuation != null) {
                    val (source, selectedTarget) = continuation
                    startPlayback(picker, source, selectedTarget, startOverrideMs)
                } else if (continuationTarget != null) {
                    mutableState.value = mutableState.value.copy(
                        viewer = mutableState.value.viewer.copy(
                            player = null,
                            playerFailure = null,
                            sourcePicker = picker,
                            sourcePickerVisible = true,
                            loading = null,
                            inlineFailure = UiFailure.PLAYBACK,
                        ),
                    )
                    onResult?.invoke(false)
                }
            } catch (cause: CancellationException) {
                onResult?.invoke(false)
                throw cause
            } catch (cause: Throwable) {
                if (!sourceRequestCurrent(operationGeneration, requestGeneration, sourceGeneration)) {
                    onResult?.invoke(false)
                    return@launch
                }
                val currentViewer = mutableState.value.viewer
                if (currentViewer.sourcePickerVisible) {
                    viewerFailure(cause, operationGeneration, requestGeneration, UiFailure.PLAYBACK)
                } else {
                    mutableState.value = mutableState.value.copy(
                        viewer = currentViewer.copy(
                            sourcePicker = null,
                            loading = if (currentViewer.loading == ViewerLoading.SOURCES) null else currentViewer.loading,
                            inlineFailure = if (currentViewer.inlineFailure == UiFailure.PLAYBACK) null else currentViewer.inlineFailure,
                        ),
                    )
                }
                onResult?.invoke(false)
            }
        }
    }

    private fun continuationPlaybackSelection(
        target: PlaybackTargetSelection?,
        preferredSource: io.rivune.api.PlaybackSourceOption?,
        preferredSourceWasUnique: Boolean,
        sources: List<io.rivune.api.PlaybackSourceOption>,
        support: ExternalPlaybackSupport,
    ): Pair<io.rivune.api.PlaybackSourceOption, PlaybackTargetSelection>? {
        target ?: return null
        if (preferredSource != null) {
            if (!preferredSourceWasUnique) return null
            var matched: io.rivune.api.PlaybackSourceOption? = null
            for (source in sources) {
                if (!source.matchesRecoverySource(preferredSource)) continue
                if (matched != null) return null
                matched = source
            }
            val source = matched ?: return null
            val selectedTarget = continuationPlaybackTarget(target, source, support) ?: return null
            return source to selectedTarget
        }
        for (source in sources) {
            continuationPlaybackTarget(target, source, support)?.let { return source to it }
        }
        return null
    }

    private fun continuationPlaybackTarget(
        target: PlaybackTargetSelection,
        source: io.rivune.api.PlaybackSourceOption,
        support: ExternalPlaybackSupport,
    ): PlaybackTargetSelection? = when (target) {
        is PlaybackTargetSelection.Embedded -> target.takeIf { source.mode != io.rivune.api.PlaybackMode.EXTERNAL }
        is PlaybackTargetSelection.External -> support
            .playersFor(source.mode, source.protocol, source.container)
            .firstOrNull { it.packageName == target.player.packageName }
            ?.let(PlaybackTargetSelection::External)
    }

    private suspend fun resolveEpisodePlaybackContext(
        currentGateway: RivuneGateway,
        target: MediaTarget,
        titleId: UUID,
        detail: MediaDetailState?,
    ): EpisodePlaybackContext {
        if (target.mediaType != "episode") return EpisodePlaybackContext()
        val directMarkerRequest = markerRequest(
            target.seriesImdbId,
            target.seasonNumber,
            target.episodeNumber,
            target.episodeOrderId,
        )
        val seriesId = target.seriesId ?: return EpisodePlaybackContext(markerRequest = directMarkerRequest)
        val language = metadataLanguage()
        val series = try {
            detail?.series?.takeIf {
                it.id == seriesId &&
                    detail.target.mappingProvider == target.mappingProvider &&
                    detail.target.episodeOrderId == target.episodeOrderId
            } ?: target.mappingProvider?.let { mappingProvider ->
                currentGateway.series(
                    seriesId,
                    mappingProvider = mappingProvider,
                    language = language,
                    episodeOrder = target.episodeOrderId,
                )
            } ?: currentGateway.canonicalSeries(seriesId, language)
        } catch (cause: CancellationException) {
            throw cause
        } catch (_: Throwable) {
            return EpisodePlaybackContext(markerRequest = directMarkerRequest)
        }
        val markerRequest = markerRequest(
            series.externalIds["imdb"] ?: target.seriesImdbId,
            target.seasonNumber,
            target.episodeNumber,
            target.episodeOrderId,
        )
        val mappingProvider = target.mappingProvider ?: series.mappingProvider
        val metadataSeasonId = target.metadataSeasonId?.takeIf(String::isNotBlank)
        val nextEpisode = try {
            val currentSeason = detail?.season?.takeIf { season ->
                season.seriesId == seriesId &&
                    season.episodes.any { it.id == titleId } &&
                    (metadataSeasonId == null || season.id == metadataSeasonId)
            } ?: run {
                val seasonId = metadataSeasonId
                    ?: series.seasons.firstOrNull { it.id == target.seasonId }?.id
                    ?: series.seasons.firstOrNull { it.seasonNumber == target.seasonNumber }?.id
                    ?: return EpisodePlaybackContext(markerRequest = markerRequest)
                currentGateway.season(seasonId, mappingProvider, language)
            }
            resolveNextEpisodeTarget(series, currentSeason, titleId, target) { seasonId ->
                currentGateway.season(seasonId, mappingProvider, language)
            }
        } catch (cause: CancellationException) {
            throw cause
        } catch (_: Throwable) {
            null
        }
        return EpisodePlaybackContext(nextEpisode, markerRequest)
    }

    private fun markerRequest(
        imdbId: String?,
        season: Int?,
        episode: Int?,
        episodeOrderId: String? = null,
    ): PlaybackMarkerRequest? {
        if (
            !episodeOrderId.isNullOrBlank() ||
            imdbId?.matches(SERIES_IMDB_ID) != true ||
            season == null ||
            season <= 0 ||
            episode == null ||
            episode <= 0
        ) {
            return null
        }
        return PlaybackMarkerRequest(imdbId, season, episode)
    }

    private suspend fun resolveTarget(currentGateway: RivuneGateway, target: MediaTarget): UUID {
        target.titleId?.let { return it }
        val mediaType = when (target.mediaType) {
            "movie" -> TitleMediaType.MOVIE
            "series" -> TitleMediaType.SERIES
            "tv" -> TitleMediaType.TV
            else -> throw IllegalArgumentException("Unsupported media type ${target.mediaType}")
        }
        val preferredProvider = listOf("tmdb", "imdb", "tvdb", "trakt").firstOrNull { !target.externalIds[it].isNullOrBlank() }
        val namespaced = NAMESPACED_ID.matchEntire(target.id)
        val provider = target.provider
            ?: if (target.mediaType == "tv") "addon" else preferredProvider ?: namespaced?.groupValues?.get(1)?.lowercase()
            ?: if (target.id.matches(IMDB_ID)) "imdb" else "addon"
        val externalId = target.externalId
            ?: if (target.mediaType == "tv") target.resourceId else preferredProvider?.let(target.externalIds::get)
            ?: namespaced?.groupValues?.get(2)
            ?: target.id
        return currentGateway.resolveTitle(
            TitleResolveInput(
                mediaType = mediaType,
                provider = provider,
                externalId = externalId,
                resourceId = target.resourceId,
                title = target.title,
                posterUrl = target.posterUrl,
                backgroundUrl = target.backgroundUrl,
                releaseInfo = target.releaseInfo,
                released = titleReleaseDate(target.released),
                sourceAddonId = target.sourceAddonId,
                sourceCatalogId = target.sourceCatalogId,
                sourceName = target.sourceName,
                country = target.country,
                language = target.language,
                category = target.category,
            ),
        ).titleId
    }

    private fun mergeSearchMediaTargets(current: List<MediaTarget>, incoming: List<MediaTarget>): List<MediaTarget> {
        val output = current.toMutableList()
        val seen = current.flatMapTo(mutableSetOf(), ::searchMediaTargetIdentities)
        for (target in incoming) {
            val identities = searchMediaTargetIdentities(target)
            if (seen.none(identities::contains)) {
                output += target
            }
            seen += identities
        }
        return output
    }

    private fun viewerRequestCurrent(operationGeneration: Long, requestGeneration: Long): Boolean =
        isCurrent(operationGeneration) && viewerRequestGeneration == requestGeneration
    private fun sourceRequestCurrent(
        operationGeneration: Long,
        requestGeneration: Long,
        sourceGeneration: Long,
    ): Boolean = viewerRequestCurrent(operationGeneration, requestGeneration) && sourceRequestGeneration == sourceGeneration
    private fun updateEffectiveSettings(effective: EffectiveSettings?) {
        mutableState.value = mutableState.value.copy(effectiveSettings = effective)
    }

    private fun metadataLanguage(settings: SettingsValues? = mutableState.value.effectiveSettings?.settings): String? {
        val configured = settings?.metadataLanguage?.trim()
        if (!configured.isNullOrEmpty() && !configured.equals("auto", ignoreCase = true)) return configured
        val locale = runCatching(localeProvider).getOrNull() ?: return null
        return locale.toLanguageTag().takeIf { it.isNotBlank() && it != "und" }
            ?: locale.language.takeIf(String::isNotBlank)
    }

    private fun invalidateMetadataContent() {
        folderRequestGeneration += 1
        metadataRefreshPending = true
        val viewer = mutableState.value.viewer
        mutableState.value = mutableState.value.copy(
            resolvedFolder = null,
            calendarEvents = emptyList(),
            viewer = viewer.copy(
                detail = null,
                detailHistory = emptyList(),
                sourcePicker = null,
                sourcePickerVisible = false,
                search = viewer.search.copy(items = emptyList(), page = 0, hasMore = false, partial = false),
            ),
        )
    }

    private fun refreshMetadataContent() {
        metadataRefreshPending = false
        val viewer = mutableState.value.viewer
        when (viewer.selectedTab) {
            ViewerTab.HOME -> loadHomeContent()
            ViewerTab.SEARCH -> if (viewer.search.query.length >= 2) {
                runSearch(viewer.search.query, skip = 0, append = false)
            } else {
                loadSearchDescriptors()
            }
            ViewerTab.LIBRARY -> Unit
            ViewerTab.CALENDAR -> loadCalendar()
        }
    }

    private fun viewerFailure(
        cause: Throwable,
        operationGeneration: Long,
        requestGeneration: Long,
        fallback: UiFailure,
    ) {
        if (!viewerRequestCurrent(operationGeneration, requestGeneration)) return
        val failure = failureFor(cause, fallback)
        if (failure == UiFailure.SESSION_EXPIRED) {
            handleSessionExpired(operationGeneration)
        } else {
            mutableState.value = mutableState.value.copy(
                isBusy = false,
                viewer = mutableState.value.viewer.copy(loading = null, inlineFailure = failure),
            )
        }
    }

    private fun performProfileSelection(profile: Profile, pin: String?) {
        val currentGateway = gateway ?: return
        val operationGeneration = generation
        mutableState.value = mutableState.value.copy(isBusy = true, failure = null)
        viewModelScope.launch {
            try {
                currentGateway.selectProfile(profile.id, pin)
                if (!isCurrent(operationGeneration)) return@launch
                val scope = offlineMediaStore?.registerProfile(
                    normalizedOrigin = mutableState.value.serverInput,
                    profileId = profile.id,
                    name = profile.name,
                    hasPin = profile.hasPin,
                    pin = pin,
                )
                activeOfflineScope = scope
                mutableState.value = mutableState.value.copy(
                    destination = AppDestination.Viewer,
                    activeProfile = profile,
                    pendingProfile = null,
                    offlineProfiles = offlineMediaStore?.profiles().orEmpty(),
                    isBusy = true,
                    failure = null,
                    resolvedFolder = null,
                )
                enterViewer()
            } catch (cause: CancellationException) {
                throw cause
            } catch (cause: Throwable) {
                if (!isCurrent(operationGeneration)) return@launch
                val failure = failureFor(cause, UiFailure.PROFILE_UNAVAILABLE)
                if (failure == UiFailure.SESSION_EXPIRED) {
                    handleSessionExpired(operationGeneration)
                } else {
                    mutableState.value = mutableState.value.copy(
                        destination = AppDestination.Profiles,
                        isBusy = false,
                        failure = failure,
                    )
                }
            }
        }
    }

    private fun loadCollections(
        currentGateway: RivuneGateway,
        operationGeneration: Long,
        profile: Profile?,
    ) {
        val requestGeneration = ++viewerRequestGeneration
        mutableState.value = mutableState.value.copy(isBusy = true, failure = null)
        viewModelScope.launch {
            loadCollectionsNow(currentGateway, operationGeneration, requestGeneration, profile)
        }
    }

    private fun loadResolvedFolder(
        collectionId: UUID,
        folderId: UUID,
        page: Int,
        append: Boolean,
        requestGeneration: Long,
    ) {
        val currentGateway = gateway ?: return
        val operationGeneration = generation
        val language = metadataLanguage()
        mutableState.value = mutableState.value.copy(
            selectedCollectionId = collectionId,
            isBusy = true,
            failure = null,
            viewer = mutableState.value.viewer.copy(
                loading = ViewerLoading.FOLDER,
                inlineFailure = null,
            ),
        )
        viewModelScope.launch {
            try {
                val resolved = currentGateway.resolveCollectionFolder(collectionId, folderId, page, language)
                if (!isCurrent(operationGeneration) || folderRequestGeneration != requestGeneration) return@launch
                val current = mutableState.value.resolvedFolder
                val content = if (append && current != null &&
                    current.collectionId == collectionId && current.folder.id == folderId
                ) {
                    val seen = current.items.mapTo(mutableSetOf(), CollectionItem::identity)
                    val additions = resolved.items.filter { seen.add(it.identity()) }
                    resolved.copy(
                        items = current.items + additions,
                        hasMore = resolved.hasMore && additions.isNotEmpty(),
                        errors = current.errors + resolved.errors,
                    )
                } else {
                    resolved
                }
                mutableState.value = mutableState.value.copy(
                    destination = AppDestination.Viewer,
                    resolvedFolder = content,
                    isBusy = false,
                    failure = null,
                    viewer = mutableState.value.viewer.copy(loading = null, inlineFailure = null),
                )
            } catch (cause: CancellationException) {
                throw cause
            } catch (cause: Throwable) {
                if (!isCurrent(operationGeneration) || folderRequestGeneration != requestGeneration) return@launch
                val failure = failureFor(cause, UiFailure.CONTENT_LOAD)
                if (failure == UiFailure.SESSION_EXPIRED) {

                    handleSessionExpired(operationGeneration)
                } else {
                    mutableState.value = mutableState.value.copy(
                        isBusy = false,
                        viewer = mutableState.value.viewer.copy(loading = null, inlineFailure = failure),
                    )
                }
            }
        }
    }
    private suspend fun loadCustomProfileAvatars(
        currentGateway: RivuneGateway,
        profiles: List<Profile>,
    ): Map<UUID, ByteArray> = coroutineScope {
        val semaphore = Semaphore(PROFILE_AVATAR_CONCURRENCY)
        profiles.filter { it.avatar.kind == "custom" }.map { profile ->
            async {
                semaphore.withPermit {
                    val avatar = try {
                        currentGateway.profileAvatar(profile.id)
                    } catch (cause: CancellationException) {
                        throw cause
                    } catch (_: Throwable) {
                        null
                    }
                    avatar?.let { profile.id to it }
                }
            }
        }.awaitAll().filterNotNull().toMap()
    }

    private suspend fun loadCollectionsNow(
        currentGateway: RivuneGateway,
        operationGeneration: Long,
        requestGeneration: Long,
        profile: Profile?,
    ) {
        try {
            val collections = currentGateway.collections()
            if (!viewerRequestCurrent(operationGeneration, requestGeneration)) return
            val selected = mutableState.value.selectedCollectionId
                ?.takeIf { id -> collections.any { it.id == id } }
                ?: collections.firstOrNull()?.id
            mutableState.value = mutableState.value.copy(
                destination = AppDestination.Viewer,
                activeProfile = profile,
                collections = collections,
                selectedCollectionId = selected,
                openedCollectionId = null,
                resolvedFolder = null,
                isBusy = false,
                failure = null,
            )
            val homeResolution = resolveHomeCollections(currentGateway, collections, metadataLanguage()) { collectionId, folder ->
                if (viewerRequestCurrent(operationGeneration, requestGeneration)) replaceCollectionFolder(collectionId, folder)
            }
            if (viewerRequestCurrent(operationGeneration, requestGeneration)) {
                mutableState.value = mutableState.value.copy(
                    collections = homeResolution.collections,
                    viewer = mutableState.value.viewer.copy(heroSlides = homeResolution.heroSlides),
                )
            }
        } catch (cause: CancellationException) {
            throw cause
        } catch (cause: Throwable) {
            val failure = failureFor(cause, UiFailure.CONTENT_LOAD)
            if (!viewerRequestCurrent(operationGeneration, requestGeneration)) return
            if (failure == UiFailure.SESSION_EXPIRED) {
                handleSessionExpired(operationGeneration)
            } else ifCurrent(operationGeneration) {
                mutableState.value = mutableState.value.copy(
                    destination = AppDestination.Viewer,
                    isBusy = false,
                    failure = failure,
                    collections = emptyList(),
                    selectedCollectionId = null,
                    openedCollectionId = null,
                    resolvedFolder = null,
                )
            }
        }
    }

    private suspend fun routeAuthenticated(
        currentGateway: RivuneGateway,
        operationGeneration: Long,
        honorActiveProfile: Boolean = true,
    ) {
        val account = currentGateway.currentAccount()
        if (!isCurrent(operationGeneration)) return
        if (account.session.authorizationScope != AuthorizationScope.CATEGORY) {
            val result = currentGateway.logout()
            if (!isCurrent(operationGeneration)) return
            mutableState.value = mutableState.value.copy(
                destination = AppDestination.Pairing,
                isBusy = false,
                failure = if (result.serverSessionClosed && result.localCredentialsCleared) {
                    null
                } else {
                    UiFailure.LOGOUT_FAILED
                },
                profiles = emptyList(),
                profileAvatarData = emptyMap(),
                pendingProfile = null,
                effectiveSettings = null,
                activeProfile = null,
                collections = emptyList(),
                selectedCollectionId = null,
                openedCollectionId = null,
                resolvedFolder = null,
                viewer = ViewerState(),
                calendarEvents = emptyList(),
            )
            launchPairing(
                operationGeneration,
                preserveFailure = !result.serverSessionClosed || !result.localCredentialsCleared,
            )
            return
        }
        val profiles = account.profiles
        if (profiles.isEmpty()) {
            mutableState.value = mutableState.value.copy(
                destination = AppDestination.Profiles,
                profileAvatarData = emptyMap(),
                profiles = emptyList(),
                effectiveSettings = null,
                activeProfile = null,
                isBusy = false,
                failure = UiFailure.NO_PROFILES,
            )
            return
        }
        val active = account.session.activeProfile?.id
            ?.takeIf { honorActiveProfile }
            ?.let { activeId -> profiles.firstOrNull { it.id == activeId && it.accessible } }
        val expectedOfflineScope = active?.let { profile ->
            offlineProfileScope(mutableState.value.serverInput, profile.id)
        }
        activeOfflineScope = active?.let { profile ->
            offlineMediaStore?.openRestoredProfile(
                normalizedOrigin = mutableState.value.serverInput,
                profileId = profile.id,
                name = profile.name,
                hasPin = profile.hasPin,
            )
        }
        val offlineProfiles = offlineMediaStore?.profiles().orEmpty()
        val pendingOfflineProfile = if (activeOfflineScope == null) {
            offlineProfiles.firstOrNull { it.scope == expectedOfflineScope && it.hasPin }
        } else null
        mutableState.value = mutableState.value.copy(
            destination = if (active == null) AppDestination.Profiles else AppDestination.Viewer,
            profiles = profiles,
            profileAvatarData = emptyMap(),
            offlineProfiles = offlineProfiles,
            pendingOfflineProfile = pendingOfflineProfile,
            effectiveSettings = null,
            activeProfile = active,
            isBusy = active != null,
            failure = null,
        )
        if (active != null) enterViewer()
        val profileAvatarData = loadCustomProfileAvatars(currentGateway, profiles)
        if (isCurrent(operationGeneration) && mutableState.value.profiles.map(Profile::id) == profiles.map(Profile::id)) {
            mutableState.value = mutableState.value.copy(profileAvatarData = profileAvatarData)
        }
    }

    private fun launchPairing(operationGeneration: Long, preserveFailure: Boolean = false) {
        pairingJob?.cancel()
        pairingJob = viewModelScope.launch {
            val currentGateway = gateway ?: return@launch
            mutableState.value = mutableState.value.copy(
                destination = AppDestination.Pairing,
                pairing = null,
                pairingAccepted = false,
                isBusy = true,
                failure = mutableState.value.failure.takeIf { preserveFailure },
            )
            try {
                val authorization = currentGateway.beginDeviceAuthorization(installationId, deviceName, platformName())
                if (!isCurrent(operationGeneration)) return@launch
                mutableState.value = mutableState.value.copy(
                    destination = AppDestination.Pairing,
                    pairing = PairingInfo(authorization.userCode),
                    pairingAccepted = false,
                    isBusy = false,
                    failure = mutableState.value.failure.takeIf { preserveFailure },
                )
                pollDeviceAuthorization(currentGateway, authorization, operationGeneration)
            } catch (cause: CancellationException) {
                throw cause
            } catch (cause: Throwable) {
                ifCurrent(operationGeneration) {
                    mutableState.value = mutableState.value.copy(
                        destination = AppDestination.Pairing,
                        pairing = null,
                        isBusy = false,
                        failure = failureFor(cause, UiFailure.PAIRING_START),
                    )
                }
            }
        }
    }

    private suspend fun pollDeviceAuthorization(
        currentGateway: RivuneGateway,
        authorization: DeviceAuthorizationResponse,
        operationGeneration: Long,
    ) {
        var intervalSeconds = authorization.intervalSeconds.coerceAtLeast(1)
        val expiresAt = runCatching { Instant.parse(authorization.expiresAt) }.getOrNull()
        while (isCurrent(operationGeneration) && (expiresAt == null || Instant.now().isBefore(expiresAt))) {
            delay(intervalSeconds * 1_000L)
            try {
                currentGateway.exchangeDeviceAuthorization(authorization.deviceCode)
                ifCurrent(operationGeneration) {
                    mutableState.value = mutableState.value.copy(
                        pairingAccepted = true,
                        isBusy = false,
                        failure = null,
                    )
                }
                delay(PAIRING_SUCCESS_HOLD_MS)
                routeAuthenticated(currentGateway, operationGeneration)
                return
            } catch (cause: CancellationException) {
                throw cause
            } catch (cause: RivuneApiException.Server) {
                when (cause.code) {
                    "authorization_pending" -> Unit
                    "slow_down" -> intervalSeconds = cause.retryAfterSeconds?.coerceAtLeast(1)?.coerceAtMost(Int.MAX_VALUE.toLong())?.toInt() ?: intervalSeconds + 5
                    "expired_device_code" -> {
                        ifCurrent(operationGeneration) {
                            mutableState.value = mutableState.value.copy(
                                pairing = null,
                                failure = UiFailure.PAIRING_EXPIRED,
                            )
                        }
                        return
                    }
                    "device_quota_reached" -> {
                        ifCurrent(operationGeneration) {
                            mutableState.value = mutableState.value.copy(
                                pairing = null,
                                failure = UiFailure.DEVICE_LIMIT,
                            )
                        }
                        return
                    }
                    else -> {
                        ifCurrent(operationGeneration) {
                            mutableState.value = mutableState.value.copy(
                                pairing = null,
                                failure = UiFailure.PAIRING_FAILED,
                            )
                        }
                        return
                    }
                }
            } catch (_: IOException) {
                ifCurrent(operationGeneration) {
                    mutableState.value = mutableState.value.copy(failure = UiFailure.SERVER_UNREACHABLE)
                }
            } catch (_: Throwable) {
                ifCurrent(operationGeneration) {
                    mutableState.value = mutableState.value.copy(
                        pairing = null,
                        failure = UiFailure.PAIRING_FAILED,
                    )
                }
                return
            }
        }
        ifCurrent(operationGeneration) {
            mutableState.value = mutableState.value.copy(pairing = null, failure = UiFailure.PAIRING_EXPIRED)
        }
    }

    private fun platformName(): String = if (tvDevice) "android_tv" else "android"

    private fun isCurrent(operationGeneration: Long): Boolean = generation == operationGeneration
    private fun handleSessionExpired(operationGeneration: Long) {
        if (!isCurrent(operationGeneration)) return
        folderRequestGeneration += 1
        stopCoordination()
        closeOfflineScope()
        mutableState.value = mutableState.value.copy(
            destination = AppDestination.Pairing,
            isBusy = false,
            failure = UiFailure.SESSION_EXPIRED,
            profiles = emptyList(),
            profileAvatarData = emptyMap(),
            pendingProfile = null,
            effectiveSettings = null,
            activeProfile = null,
            collections = emptyList(),
            selectedCollectionId = null,
            openedCollectionId = null,
            resolvedFolder = null,
            viewer = ViewerState(),
            calendarEvents = emptyList(),
        )
        launchPairing(operationGeneration, preserveFailure = true)
    }


    private inline fun ifCurrent(operationGeneration: Long, action: () -> Unit) {
        if (isCurrent(operationGeneration)) action()
    }

    private fun failureFor(cause: Throwable, fallback: UiFailure): UiFailure = when (cause) {
        is RivuneApiException.InvalidServerUrl -> UiFailure.SERVER_INVALID
        is RivuneApiException.IncompatibleProtocol -> UiFailure.PROTOCOL_INCOMPATIBLE
        is RivuneApiException.NotAuthenticated -> UiFailure.SESSION_EXPIRED
        is IOException -> UiFailure.SERVER_UNREACHABLE
        is RivuneApiException.Server -> when (cause.code) {
            "device_quota_reached" -> UiFailure.DEVICE_LIMIT
            "device_code_capacity", "rate_limited" -> UiFailure.PAIRING_LIMIT
            "invalid_profile_pin" -> UiFailure.PROFILE_PIN_INVALID
            "profile_pin_rate_limited" -> UiFailure.PROFILE_PIN_RATE_LIMITED
            "profile_unavailable", "profile_not_found", "maintenance_mode" -> UiFailure.PROFILE_UNAVAILABLE
            "invalid_access_token", "invalid_refresh_token", "not_authenticated", "authentication_required" -> UiFailure.SESSION_EXPIRED
            else -> fallback
        }
        else -> fallback
    }

    companion object {
        private const val SEARCH_PAGE_SIZE = 24
        private const val PROFILE_AVATAR_CONCURRENCY = 4
        private const val LIBRARY_PAGE_SIZE = 100
        private const val MAX_WATCHED_BATCH_SIZE = 100
        private const val SEMANTIC_SEARCH_TIMEOUT_MILLISECONDS = 12_000L
        private const val SEARCH_PUBLICATION_WINDOW_MILLISECONDS = 32L
        private const val MAX_SEARCH_ADDON_TYPES = 16
        private const val MAX_SEARCH_ADDON_REQUESTS = 16
        private const val MAX_SEARCH_ADDON_CONCURRENCY = 4
        private const val COORDINATION_ACTIVE_INTERVAL_MILLISECONDS = 2_000L
        private const val COORDINATION_IDLE_INTERVAL_MILLISECONDS = 15_000L
        private const val COORDINATION_PRESENCE_INTERVAL_MILLISECONDS = 15_000L
        private const val COORDINATION_RECENT_ACTIVITY_MILLISECONDS = 30_000L
        private const val PAIRING_SUCCESS_HOLD_MS = 550L
        private val IMDB_ID = Regex("^tt\\d+$", RegexOption.IGNORE_CASE)
        private val SERIES_IMDB_ID = Regex("^tt[0-9]{7,8}$")
        private val DATE_ONLY = Regex("^\\d{4}-\\d{2}-\\d{2}$")

        private const val COLLECTION_ARTWORK_CONCURRENCY = 6
        private const val HOME_HERO_SLIDE_LIMIT = 12
        fun factory(context: Context, isTv: Boolean): ViewModelProvider.Factory {
            val applicationContext = context.applicationContext
            return object : ViewModelProvider.Factory {
                @Suppress("UNCHECKED_CAST")
                override fun <T : ViewModel> create(modelClass: Class<T>): T {
                    require(modelClass.isAssignableFrom(RivuneViewModel::class.java))
                    val store = PreferencesServerAddressStore(applicationContext)
                    val gatewayFactory = RivuneGatewayFactory { serverUrl ->
                        DefaultRivuneGateway(RivuneApiClient(serverUrl, applicationContext))
                    }
                    val installationId = PreferencesInstallationIdStore(applicationContext).loadOrCreate()
                    val model = Build.MODEL.trim().ifBlank { "Android device" }.take(120)
                    val application = applicationContext as? RivuneApplication
                    return RivuneViewModel(
                        store,
                        gatewayFactory,
                        isTv,
                        model,
                        installationId = installationId,
                        externalPlaybackSupportProvider = { detectExternalPlaybackSupport(applicationContext) },
                        appPreferences = application?.appPreferences ?: AppPreferencesStore(applicationContext),
                        playbackNetworkProvider = { detectPlaybackNetwork(applicationContext) },
                        serverConnectionAllowed = { serverUrl ->
                            !requiresLocalNetworkPermission(applicationContext, serverUrl)
                        },
                        diagnostics = application?.diagnostics ?: DiagnosticsBuffer(),
                        offlineMediaStore = OfflineMediaStore(applicationContext),
                        playbackOperationStore = PreferencesPlaybackOperationStore(applicationContext),
                    ) as T
                }
            }
        }
    }
}

private fun CollectionItem.identity(): String = "$mediaType\u0000$id"
