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
import io.rivune.api.EffectiveSettings
import io.rivune.api.DeviceAuthorizationResponse
import io.rivune.api.LibraryItem
import io.rivune.api.LibraryPage
import io.rivune.api.Movie
import io.rivune.api.PlaybackCapabilities
import io.rivune.api.PlaybackPreparation
import io.rivune.api.PlaybackProgress
import io.rivune.api.PlaybackProgressBatch
import io.rivune.api.PlaybackSession
import io.rivune.api.PlaybackSourceList
import io.rivune.api.Profile
import io.rivune.api.ProfileSettingsUpdate
import io.rivune.api.ProfileSelection
import io.rivune.api.ResolvedCollectionFolder
import io.rivune.api.RivuneApiClient
import io.rivune.api.RivuneApiException
import io.rivune.api.Season
import io.rivune.api.Series
import io.rivune.api.SeriesMappingProvider
import io.rivune.api.TitleMediaType
import io.rivune.api.SetWatchedBatchItem
import io.rivune.api.SetWatchedBatchResult
import io.rivune.api.SettingsLayer
import io.rivune.api.TitleReference
import io.rivune.api.TitleResolveInput
import io.rivune.api.UpdatePlaybackProgressRequest
import java.io.IOException
import java.time.Instant
import java.time.YearMonth
import java.time.format.DateTimeParseException
import java.util.UUID
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.Job
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit
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
data class PairingInfo(
    val userCode: String,
)

enum class UiFailure {
    SERVER_INVALID,
    SERVER_UNREACHABLE,
    PROTOCOL_INCOMPATIBLE,
    SETUP_REQUIRED,
    DEVICE_LIMIT,
    PAIRING_START,
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
    val isTv: Boolean = false,
    val isBusy: Boolean = false,
    val failure: UiFailure? = null,
    val profiles: List<Profile> = emptyList(),
    val profileAvatarData: Map<UUID, ByteArray> = emptyMap(),
    val pendingProfile: Profile? = null,
    val activeProfile: Profile? = null,
    val collections: List<Collection> = emptyList(),
    val selectedCollectionId: UUID? = null,
    val resolvedFolder: ResolvedCollectionFolder? = null,
    val pairing: PairingInfo? = null,
    val pairingAccepted: Boolean = false,
    val viewer: ViewerState = ViewerState(),
    val calendarEvents: List<CalendarEvent> = emptyList(),
    val calendarMonth: java.time.YearMonth = java.time.YearMonth.now(),
    val externalPlayers: List<ExternalPlayerApp> = emptyList(),
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
    suspend fun resolveCollectionFolder(collectionId: UUID, folderId: UUID, page: Int? = null): ResolvedCollectionFolder
    suspend fun addonCatalogs(): List<AddonCatalogDescriptor>
    suspend fun resolveCollectionFolderArtwork(collectionId: UUID, folderId: UUID): ResolvedCollectionFolder
    suspend fun searchAddonCatalogs(type: String, search: String, skip: Int? = null, limit: Int? = null): AddonResourceBatch
    suspend fun resolveTitle(input: TitleResolveInput): TitleReference
    suspend fun movie(id: UUID): Movie
    suspend fun series(id: UUID, mappingProvider: SeriesMappingProvider = SeriesMappingProvider.TMDB): Series
    suspend fun season(id: String, mappingProvider: SeriesMappingProvider): Season
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
    suspend fun playbackSources(mediaType: String, resourceId: String, capabilities: PlaybackCapabilities, addonId: UUID? = null): PlaybackSourceList
    suspend fun preparePlayback(sourceRef: String, startSeconds: Int? = null, externalPlayer: Boolean = false): PlaybackPreparation
    suspend fun resolvePlayback(sourceRef: String, titleId: String? = null, startSeconds: Int? = null, externalPlayer: Boolean = false): PlaybackSession
    suspend fun stopPlayback(sessionId: UUID)
    suspend fun updatePlaybackProgress(titleId: UUID, input: UpdatePlaybackProgressRequest): PlaybackProgress
    suspend fun calendar(from: String, to: String, language: String? = null): List<CalendarEvent>
    suspend fun beginDeviceAuthorization(deviceName: String, platform: String): DeviceAuthorizationResponse
    suspend fun exchangeDeviceAuthorization(deviceCode: String)
    suspend fun logout(): LogoutResult
    fun resolveResourceUrl(value: String): String?
    fun resolveArtworkUrl(value: String): String?
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
    override suspend fun resolveCollectionFolder(collectionId: UUID, folderId: UUID, page: Int?) =
        client.resolveCollectionFolder(collectionId, folderId, page = page)
    override suspend fun addonCatalogs() = client.addonCatalogs()
    override suspend fun resolveCollectionFolderArtwork(collectionId: UUID, folderId: UUID) =
        client.resolveCollectionFolderArtwork(collectionId, folderId)
    override suspend fun searchAddonCatalogs(type: String, search: String, skip: Int?, limit: Int?) =
        client.searchAddonCatalogs(type, search, skip, limit)
    override suspend fun resolveTitle(input: TitleResolveInput) = client.resolveTitle(input)
    override suspend fun movie(id: UUID) = client.movie(id)
    override suspend fun series(id: UUID, mappingProvider: SeriesMappingProvider) =
        client.series(id, mappingProvider = mappingProvider)
    override suspend fun season(id: String, mappingProvider: SeriesMappingProvider) =
        client.season(id, mappingProvider = mappingProvider)
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
    override suspend fun resolvePlayback(sourceRef: String, titleId: String?, startSeconds: Int?, externalPlayer: Boolean) =
        client.resolvePlayback(sourceRef, titleId = titleId, startSeconds = startSeconds, externalPlayer = externalPlayer)
    override suspend fun stopPlayback(sessionId: UUID) = client.stopPlayback(sessionId)
    override suspend fun updatePlaybackProgress(titleId: UUID, input: UpdatePlaybackProgressRequest) =
        client.updatePlaybackProgress(titleId, input)
    override suspend fun calendar(from: String, to: String, language: String?) = client.calendar(from, to, language)
    override suspend fun beginDeviceAuthorization(deviceName: String, platform: String) =
        client.beginDeviceAuthorization(deviceName, platform)
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

class RivuneViewModel internal constructor(
    private val serverStore: ServerAddressStore,
    private val gatewayFactory: RivuneGatewayFactory,
    private val tvDevice: Boolean,
    private val deviceName: String,
    private val terminalCleanupScope: CoroutineScope = CoroutineScope(SupervisorJob() + Dispatchers.IO),
    private val externalPlaybackSupportProvider: () -> ExternalPlaybackSupport = { ExternalPlaybackSupport() },
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
    private var folderRequestGeneration = 0L
    private var viewerRequestGeneration = 0L
    private var searchDescriptors: List<AddonCatalogDescriptor> = emptyList()
    private val progressUpdateMutex = Mutex()

    init {
        val remembered = serverStore.load()
        if (remembered == null) {
            mutableState.value = mutableState.value.copy(destination = AppDestination.Server)
        } else {
            mutableState.value = mutableState.value.copy(serverInput = remembered)
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
            return
        }

        generation += 1
        val operationGeneration = generation
        pairingJob?.cancel()
        pairingJob = null
        val connectingDestination = if (mutableState.value.destination == AppDestination.Server) {
            AppDestination.Server
        } else {
            AppDestination.Loading
        }
        mutableState.value = mutableState.value.copy(
            destination = connectingDestination,
            serverInput = normalized,
            serverName = "",
            isBusy = true,
            failure = null,
            profiles = emptyList(),
            profileAvatarData = emptyMap(),
            pendingProfile = null,
            activeProfile = null,
            collections = emptyList(),
            selectedCollectionId = null,
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
                    }
                    return@launch
                }
                if (!isCurrent(operationGeneration)) return@launch
                gateway = candidate
                serverStore.save(normalized)
                mutableState.value = mutableState.value.copy(serverName = discovery.name)
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
        val currentGateway = gateway ?: return
        generation += 1
        val operationGeneration = generation
        folderRequestGeneration += 1
        viewerRequestGeneration += 1
        pairingJob?.cancel()
        pairingJob = null
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
            serverStore.clear()
            mutableState.value = RivuneUiState(
                destination = AppDestination.Server,
                isTv = tvDevice,
                failure = if (result.serverSessionClosed) null else UiFailure.LOGOUT_FAILED,
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

    fun submitPin(pin: String) {
        val profile = mutableState.value.pendingProfile ?: return
        val normalized = pin.filter(Char::isDigit)
        if (normalized.length !in 4..8) {
            mutableState.value = mutableState.value.copy(failure = UiFailure.PROFILE_PIN_INVALID)
            return
        }
        performProfileSelection(profile, normalized)
    }

    fun dismissPin() {
        if (mutableState.value.isBusy) return
        mutableState.value = mutableState.value.copy(pendingProfile = null, failure = null)
    }

    fun selectCollection(id: UUID) {
        folderRequestGeneration += 1
        if (mutableState.value.collections.none { it.id == id }) return
        mutableState.value = mutableState.value.copy(
            selectedCollectionId = id,
            resolvedFolder = null,
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
        mutableState.value = mutableState.value.copy(isBusy = true, failure = null)
        viewModelScope.launch {
            try {
                currentGateway.clearProfileSelection()
                ifCurrent(operationGeneration) {
                    mutableState.value = mutableState.value.copy(
                        destination = AppDestination.Profiles,
                        pendingProfile = null,
                        activeProfile = null,
                        collections = emptyList(),
                        selectedCollectionId = null,
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
                activeProfile = null,
                collections = emptyList(),
                selectedCollectionId = null,
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
                preferences = ProfilePreferencesState(canEdit = profile.canManage),
                detail = null,
                sourcePicker = null,
                loading = ViewerLoading.PREFERENCES,
                inlineFailure = null,
            ),
        )
        viewModelScope.launch {
            try {
                val effective = currentGateway.effectiveProfileSettings(profile.id)
                if (!viewerRequestCurrent(operationGeneration, requestGeneration)) return@launch
                mutableState.value = mutableState.value.copy(
                    viewer = mutableState.value.viewer.copy(
                        preferences = ProfilePreferencesState(settings = effective.settings, canEdit = profile.canManage),
                        loading = null,
                        inlineFailure = null,
                    ),
                )
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
                currentGateway.updateProfileSettings(profile.id, input)
                val effective = currentGateway.effectiveProfileSettings(profile.id)
                if (!viewerRequestCurrent(operationGeneration, requestGeneration)) return@launch
                mutableState.value = mutableState.value.copy(
                    viewer = mutableState.value.viewer.copy(
                        preferences = ProfilePreferencesState(settings = effective.settings, canEdit = true),
                        loading = null,
                        inlineFailure = null,
                    ),
                )
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
                sourcePicker = null,
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

    fun openMedia(target: MediaTarget) {
        if (!target.available) return
        val currentGateway = gateway ?: return
        val operationGeneration = generation
        val requestGeneration = ++viewerRequestGeneration
        mutableState.value = mutableState.value.copy(
            viewer = mutableState.value.viewer.copy(
                loading = ViewerLoading.DETAIL,
                detail = null,
                sourcePicker = null,
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
                val movie = if (target.mediaType == "movie") runCatching { currentGateway.movie(titleId) }.getOrNull() else null
                val series = if (target.mediaType == "series") {
                    runCatching { currentGateway.series(titleId) }
                        .recoverCatching { currentGateway.series(titleId, SeriesMappingProvider.TVDB) }
                        .getOrNull()
                } else null
                if (!viewerRequestCurrent(operationGeneration, requestGeneration)) return@launch
                val detail = MediaDetailState(
                    target = canonical,
                    titleId = titleId,
                    movie = movie,
                    series = series,
                    progress = progress,
                    inLibrary = library?.items?.any { it.titleId == titleId } == true,
                )
                mutableState.value = mutableState.value.copy(
                    viewer = mutableState.value.viewer.copy(detail = detail, loading = null, inlineFailure = null),
                )
            } catch (cause: CancellationException) {
                throw cause
            } catch (cause: Throwable) {
                viewerFailure(cause, operationGeneration, requestGeneration, UiFailure.CONTENT_LOAD)
            }
        }
    }

    fun backViewer() {
        val viewer = mutableState.value.viewer
        when {
            viewer.player != null -> closePlayer()
            viewer.sourcePicker != null -> dismissSourcePicker()
            viewer.preferences != null -> closeProfilePreferences()
            viewer.detail?.season != null -> {
                viewerRequestGeneration += 1
                mutableState.value = mutableState.value.copy(
                    viewer = viewer.copy(
                        detail = viewer.detail.copy(season = null),
                        loading = null,
                        inlineFailure = null,
                    ),
                )
            }
            viewer.detail != null -> {
                viewerRequestGeneration += 1
                mutableState.value = mutableState.value.copy(
                    viewer = viewer.copy(detail = null, loading = null, inlineFailure = null),
                )
            }
            mutableState.value.resolvedFolder != null -> closeFolder()
        }
    }

    fun search(query: String) {
        val normalized = query.trim()
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
        val currentGateway = gateway ?: return
        val requestGeneration = ++viewerRequestGeneration
        val operationGeneration = generation
        mutableState.value = mutableState.value.copy(viewer = mutableState.value.viewer.copy(loading = ViewerLoading.SEASON, inlineFailure = null))
        viewModelScope.launch {
            try {
                val season = currentGateway.season(seasonId, series.mappingProvider)
                val progress = season.episodes
                    .chunked(MAX_WATCHED_BATCH_SIZE)
                    .flatMap { chunk -> currentGateway.playbackProgressBatch(chunk.map { it.id }).items }
                    .mapNotNull { item -> item.progress?.let { item.titleId to it } }
                    .toMap()
                if (!viewerRequestCurrent(operationGeneration, requestGeneration)) return@launch
                mutableState.value = mutableState.value.copy(
                    viewer = mutableState.value.viewer.copy(
                        detail = detail.copy(season = season, episodeProgress = progress),
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
        val detail = mutableState.value.viewer.detail ?: return
        val resolvedTarget = target ?: detail.target
        if (resolvedTarget.mediaType == "series") return
        val titleId = resolvedTarget.titleId ?: detail.titleId.takeIf { resolvedTarget == detail.target } ?: return
        val progress = if (resolvedTarget == detail.target) detail.progress else detail.episodeProgress[titleId]
        loadPlaybackSources(resolvedTarget.copy(titleId = titleId), titleId, progress)
    }

    fun choosePlaybackSource(
        source: io.rivune.api.PlaybackSourceOption,
        externalPlayer: ExternalPlayerApp? = null,
    ) {
        val picker = mutableState.value.viewer.sourcePicker ?: return
        val currentGateway = gateway ?: return
        val selectedExternalPlayer = externalPlayer?.let { requested ->
            externalPlaybackSupport.playersFor(source.mode, source.protocol, source.container)
                .firstOrNull { it.packageName == requested.packageName }
                ?: return
        }
        val operationGeneration = generation
        val requestGeneration = ++viewerRequestGeneration
        val start = picker.progress?.takeUnless { it.completed }?.positionSeconds ?: 0
        mutableState.value = mutableState.value.copy(viewer = mutableState.value.viewer.copy(loading = ViewerLoading.PLAYER, inlineFailure = null))
        viewModelScope.launch {
            var createdSession: io.rivune.api.PlaybackSession? = null
            try {
                val external = selectedExternalPlayer != null
                currentGateway.preparePlayback(source.sourceRef, start, external)
                val session = currentGateway.resolvePlayback(source.sourceRef, picker.titleId.toString(), start, external)
                createdSession = session
                val selected = session.sources.firstOrNull { it.id == session.selectedSourceId } ?: session.sources.firstOrNull()
                    ?: throw IllegalStateException("Playback session has no selected source")
                val mediaUrl = selected.url?.let(currentGateway::resolveResourceUrl)
                    ?: selected.infoHash?.takeIf { external }?.let { magnetUrl(it, picker.target.title) }
                    ?: throw IllegalStateException("Playback session has no playable URL")
                val subtitles = session.subtitles.mapNotNull { subtitle ->
                    val url = subtitle.url?.let(currentGateway::resolveResourceUrl) ?: return@mapNotNull null
                    PlayerSubtitlePresentation(
                        id = subtitle.id,
                        label = subtitle.language ?: subtitle.id,
                        language = subtitle.language,
                        url = url,
                        selected = subtitle.id == session.selectedSubtitleId,
                    )
                }
                val durationSeconds = selected.media?.durationSeconds
                    ?.takeIf { it.isFinite() && it > 0.0 }
                    ?.coerceAtMost(Int.MAX_VALUE.toDouble())
                    ?.toInt()
                    ?: picker.progress?.durationSeconds
                    ?: 0
                lastPlayerProgress = null
                if (!viewerRequestCurrent(operationGeneration, requestGeneration)) return@launch
                mutableState.value = mutableState.value.copy(
                    viewer = mutableState.value.viewer.copy(
                        sourcePicker = null,
                        player = PlayerPresentation(
                            key = session.id.toString(),
                            sessionId = session.id,
                            titleId = picker.titleId,
                            title = picker.target.title,
                            mediaUrl = mediaUrl,
                            protocol = selected.protocol,
                            container = selected.container,
                            startPositionMs = start * 1_000L,
                            durationSeconds = durationSeconds,
                            expectedProgressVersion = picker.progress?.version ?: 0,
                            subtitles = subtitles,
                            externalPlayer = selectedExternalPlayer,
                        ),
                        loading = null,
                        inlineFailure = null,
                    ),
                )
                createdSession = null
            } catch (cause: CancellationException) {
                throw cause
            } catch (cause: Throwable) {
                viewerFailure(cause, operationGeneration, requestGeneration, UiFailure.PLAYBACK)
            } finally {
                createdSession?.let { session ->
                    kotlinx.coroutines.withContext(NonCancellable) {
                        runCatching { currentGateway.stopPlayback(session.id) }
                    }
                }
            }
        }
    }

    fun dismissSourcePicker() {
        viewerRequestGeneration += 1
        mutableState.value = mutableState.value.copy(
            viewer = mutableState.value.viewer.copy(sourcePicker = null, loading = null, inlineFailure = null),
        )
    }

    fun toggleLibrary() {
        val detail = mutableState.value.viewer.detail ?: return
        if (detail.target.mediaType == "episode") return
        val currentGateway = gateway ?: return
        if (mutableState.value.viewer.loading == ViewerLoading.ACTION) return
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

    fun closePlayer() {
        val player = mutableState.value.viewer.player ?: return
        val currentGateway = gateway
        viewerRequestGeneration += 1
        mutableState.value = mutableState.value.copy(
            viewer = mutableState.value.viewer.copy(player = null, loading = null),
        )
        terminalCleanupScope.launch { runCatching { currentGateway?.stopPlayback(player.sessionId) } }
        loadHomeContent()
    }

    fun playerFailed() {
        val player = mutableState.value.viewer.player ?: return
        val currentGateway = gateway
        mutableState.value = mutableState.value.copy(
            viewer = mutableState.value.viewer.copy(
                player = null,
                loading = null,
                inlineFailure = UiFailure.PLAYBACK,
            ),
        )
        terminalCleanupScope.launch { runCatching { currentGateway?.stopPlayback(player.sessionId) } }
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
        viewerRequestGeneration += 1
        mutableState.value = mutableState.value.copy(
            viewer = mutableState.value.viewer.copy(player = null, loading = null),
        )
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
            viewer = mutableState.value.viewer.copy(player = null, loading = null),
        )
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

    private fun loadHomeContent() {
        val currentGateway = gateway ?: return
        val operationGeneration = generation
        val requestGeneration = ++viewerRequestGeneration
        mutableState.value = mutableState.value.copy(
            isBusy = false,
            viewer = mutableState.value.viewer.copy(loading = ViewerLoading.HOME, inlineFailure = null),
        )
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
                    val collections = collectionsTask.await()
                    if (!viewerRequestCurrent(operationGeneration, requestGeneration)) {
                        continueTask.cancel()
                        return@coroutineScope
                    }
                    val selected = mutableState.value.selectedCollectionId
                        ?.takeIf { id -> collections.any { it.id == id } }
                        ?: collections.firstOrNull()?.id
                    mutableState.value = mutableState.value.copy(
                        collections = collections,
                        selectedCollectionId = selected,
                        isBusy = false,
                        viewer = mutableState.value.viewer.copy(loading = null, inlineFailure = null),
                    )
                    val artworkTask = async {
                        resolveCollectionArtwork(currentGateway, collections) { collectionId, folder ->
                            if (viewerRequestCurrent(operationGeneration, requestGeneration)) {
                                replaceCollectionFolder(collectionId, folder)
                            }
                        }
                    }

                    val continuePage = continueTask.await()
                    val continueTargets = continuePage?.let { enrichContinueWatching(currentGateway, it) }.orEmpty()
                    if (!viewerRequestCurrent(operationGeneration, requestGeneration)) {
                        artworkTask.cancel()
                        return@coroutineScope
                    }
                    mutableState.value = mutableState.value.copy(
                        viewer = mutableState.value.viewer.copy(continueWatching = continueTargets),
                    )

                    val hydratedCollections = artworkTask.await()
                    if (!viewerRequestCurrent(operationGeneration, requestGeneration)) return@coroutineScope
                    mutableState.value = mutableState.value.copy(collections = hydratedCollections)
                }
            } catch (cause: CancellationException) {
                throw cause
            } catch (cause: Throwable) {
                viewerFailure(cause, operationGeneration, requestGeneration, UiFailure.CONTENT_LOAD)
            }
        }
    }

    private suspend fun resolveCollectionArtwork(
        currentGateway: RivuneGateway,
        collections: List<Collection>,
        onResolved: ((UUID, CollectionFolder) -> Unit)? = null,
    ): List<Collection> = coroutineScope {
        val semaphore = Semaphore(COLLECTION_ARTWORK_CONCURRENCY)
        val pending = collections.map { collection ->
            collection to collection.folders.map { folder ->
                async {
                    val folderId = folder.id
                    if (!folder.coverImageUrl.isNullOrBlank() || folderId == null) {
                        folder
                    } else {
                        semaphore.withPermit {
                            val resolved = try {
                                currentGateway.resolveCollectionFolderArtwork(collection.id, folderId).folder
                            } catch (cause: CancellationException) {
                                throw cause
                            } catch (_: Throwable) {
                                null
                            }
                            resolved?.also { onResolved?.invoke(collection.id, it) } ?: folder
                        }
                    }
                }
            }
        }
        pending.map { (collection, folders) -> collection.copy(folders = folders.awaitAll()) }
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

    private suspend fun enrichContinueWatching(
        currentGateway: RivuneGateway,
        page: ContinueWatchingPage,
    ): List<MediaTarget> = coroutineScope {
        page.items.map { item ->
            async {
                val fallback = MediaTarget(
                    id = item.titleId.toString(),
                    resourceId = item.titleId.toString(),
                    mediaType = item.mediaType.name.lowercase(),
                    title = if (item.episodeNumber != null) {
                        "S${item.seasonNumber?.toString()?.padStart(2, '0') ?: "--"}E${item.episodeNumber.toString().padStart(2, '0')}"
                    } else {
                        "Continue watching"
                    },
                    titleId = item.titleId,
                    seriesId = item.seriesId,
                    seasonId = item.seasonId?.toString(),
                    seasonNumber = item.seasonNumber,
                    episodeNumber = item.episodeNumber,
                    resumePositionSeconds = item.positionSeconds,
                    durationSeconds = item.durationSeconds,
                )
                val enriched = runCatching {
                    if (item.seriesId == null) {
                        val movie = currentGateway.movie(item.titleId)
                        fallback.copy(
                            mediaType = "movie",
                            title = movie.title,
                            resourceId = movie.externalIds["imdb"] ?: movie.externalIds["tmdb"] ?: fallback.resourceId,
                            externalIds = movie.externalIds,
                            posterUrl = movie.posterUrl,
                            backgroundUrl = movie.backdropUrl,
                            description = movie.overview,
                            releaseInfo = movie.releaseDate,
                        )
                    } else {
                        val seriesId = requireNotNull(item.seriesId)
                        val series = runCatching { currentGateway.series(seriesId) }
                            .recoverCatching { currentGateway.series(seriesId, SeriesMappingProvider.TVDB) }
                            .getOrThrow()
                        val summary = series.seasons.firstOrNull { it.id == item.seasonId?.toString() }
                            ?: series.seasons.firstOrNull { it.seasonNumber == item.seasonNumber }
                        val season = summary?.let { currentGateway.season(it.id, series.mappingProvider) }
                        val episode = season?.episodes?.firstOrNull { it.id == item.titleId }
                            ?: season?.episodes?.firstOrNull { it.episodeNumber == item.episodeNumber }
                        episode?.toMediaTarget(series, fallback)?.copy(
                            title = listOfNotNull(series.name, fallback.title, episode.name).joinToString(" · "),
                        ) ?: fallback.copy(title = series.name, posterUrl = series.posterUrl, backgroundUrl = series.backdropUrl)
                    }
                }.getOrDefault(fallback)
                enriched.copy(
                    resumePositionSeconds = item.positionSeconds,
                    durationSeconds = item.durationSeconds,
                )
            }
        }.awaitAll()
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
        val requestGeneration = ++viewerRequestGeneration
        mutableState.value = mutableState.value.copy(
            viewer = mutableState.value.viewer.copy(
                loading = if (append) ViewerLoading.SEARCH_MORE else ViewerLoading.SEARCH,
                inlineFailure = null,
            ),
        )
        viewModelScope.launch {
            try {
                val descriptors = if (searchDescriptors.isEmpty()) currentGateway.addonCatalogs() else searchDescriptors
                searchDescriptors = descriptors
                val types = descriptors.asSequence().filter { it.searchable }.map { it.catalog.type }.distinct().toList()
                val results = coroutineScope {
                    types.map { type -> async { runCatching { currentGateway.searchAddonCatalogs(type, query, skip, SEARCH_PAGE_SIZE) } } }.awaitAll()
                }
                if (!viewerRequestCurrent(operationGeneration, requestGeneration)) return@launch
                val batches = results.mapNotNull { it.getOrNull() }
                val incoming = batches.flatMap { it.toMediaTargets(descriptors) }
                val current = if (append) mutableState.value.viewer.search.items else emptyList()
                val merged = mergeMediaTargets(current, incoming)
                mutableState.value = mutableState.value.copy(
                    viewer = mutableState.value.viewer.copy(
                        search = SearchState(
                            query = query,
                            items = merged,
                            page = if (append) mutableState.value.viewer.search.page + 1 else 1,
                            hasMore = batches.any { it.hasFullPage(SEARCH_PAGE_SIZE) },
                            partial = results.any { it.isFailure } || batches.any { it.errors.isNotEmpty() },
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
        val requestGeneration = ++viewerRequestGeneration
        val month = mutableState.value.calendarMonth
        mutableState.value = mutableState.value.copy(viewer = mutableState.value.viewer.copy(loading = ViewerLoading.CALENDAR, inlineFailure = null))
        viewModelScope.launch {
            try {
                val events = currentGateway.calendar(month.atDay(1).toString(), month.atEndOfMonth().toString())
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

    private fun loadPlaybackSources(target: MediaTarget, titleId: UUID, progress: PlaybackProgress?) {
        val currentGateway = gateway ?: return
        refreshExternalPlaybackSupport()
        val support = externalPlaybackSupport
        val operationGeneration = generation
        val requestGeneration = ++viewerRequestGeneration
        mutableState.value = mutableState.value.copy(viewer = mutableState.value.viewer.copy(loading = ViewerLoading.SOURCES, sourcePicker = null, inlineFailure = null))
        viewModelScope.launch {
            try {
                val sources = currentGateway.playbackSources(
                    mediaType = target.mediaType,
                    resourceId = target.resourceId,
                    capabilities = DevicePlaybackCapabilities.value.copy(
                        externalPlayers = support.capabilityIds.ifEmpty { null },
                    ),
                    addonId = target.sourceAddonId.takeIf { target.mediaType == "tv" },
                )
                if (!viewerRequestCurrent(operationGeneration, requestGeneration)) return@launch
                if (sources.sources.isEmpty()) throw IllegalStateException("No playback source")
                val picker = SourcePickerState(target, titleId, progress, sources.sources, sources.providerErrors.isNotEmpty())
                mutableState.value = mutableState.value.copy(
                    viewer = mutableState.value.viewer.copy(sourcePicker = picker, loading = null, inlineFailure = null),
                )
                val onlySource = sources.sources.singleOrNull()
                if (onlySource != null && support.playersFor(onlySource.mode, onlySource.protocol, onlySource.container).isEmpty()) {
                    choosePlaybackSource(onlySource)
                }
            } catch (cause: CancellationException) {
                throw cause
            } catch (cause: Throwable) {
                viewerFailure(cause, operationGeneration, requestGeneration, UiFailure.PLAYBACK)
            }
        }
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
                released = target.released?.take(10)?.takeIf { it.matches(DATE_ONLY) },
                sourceAddonId = target.sourceAddonId,
                sourceCatalogId = target.sourceCatalogId,
                sourceName = target.sourceName,
                country = target.country,
                language = target.language,
                category = target.category,
            ),
        ).titleId
    }

    private fun mergeMediaTargets(current: List<MediaTarget>, incoming: List<MediaTarget>): List<MediaTarget> {
        val output = current.toMutableList()
        val seen = current.mapTo(mutableSetOf(), ::mediaTargetIdentity)
        for (target in incoming) if (seen.add(mediaTargetIdentity(target))) output += target
        return output
    }

    private fun mediaTargetIdentity(target: MediaTarget): String = if (target.mediaType in setOf("movie", "series", "episode")) {
        "${target.mediaType}:${target.titleId ?: target.id}"
    } else {
        "${target.mediaType}:${target.sourceAddonId}:${target.resourceId}"
    }

    private fun viewerRequestCurrent(operationGeneration: Long, requestGeneration: Long): Boolean =
        isCurrent(operationGeneration) && viewerRequestGeneration == requestGeneration

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
                mutableState.value = mutableState.value.copy(
                    destination = AppDestination.Viewer,
                    activeProfile = profile,
                    pendingProfile = null,
                    isBusy = true,
                    failure = null,
                    resolvedFolder = null,
                )
                loadHomeContent()
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
                val resolved = currentGateway.resolveCollectionFolder(collectionId, folderId, page)
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
                resolvedFolder = null,
                isBusy = false,
                failure = null,
            )
            val hydratedCollections = resolveCollectionArtwork(currentGateway, collections) { collectionId, folder ->
                if (viewerRequestCurrent(operationGeneration, requestGeneration)) replaceCollectionFolder(collectionId, folder)
            }
            if (viewerRequestCurrent(operationGeneration, requestGeneration)) {
                mutableState.value = mutableState.value.copy(collections = hydratedCollections)
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
                activeProfile = null,
                collections = emptyList(),
                selectedCollectionId = null,
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
                activeProfile = null,
                isBusy = false,
                failure = UiFailure.NO_PROFILES,
            )
            return
        }
        val active = account.session.activeProfile?.id
            ?.takeIf { honorActiveProfile }
            ?.let { activeId -> profiles.firstOrNull { it.id == activeId && it.accessible } }
        mutableState.value = mutableState.value.copy(
            destination = if (active == null) AppDestination.Profiles else AppDestination.Viewer,
            profiles = profiles,
            profileAvatarData = emptyMap(),
            pendingProfile = null,
            activeProfile = active,
            isBusy = active != null,
            failure = null,
        )
        if (active != null) loadHomeContent()
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
                val authorization = currentGateway.beginDeviceAuthorization(deviceName, platformName())
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
                    "slow_down" -> intervalSeconds += 5
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
        mutableState.value = mutableState.value.copy(
            destination = AppDestination.Pairing,
            isBusy = false,
            failure = UiFailure.SESSION_EXPIRED,
            profiles = emptyList(),
            profileAvatarData = emptyMap(),
            pendingProfile = null,
            activeProfile = null,
            collections = emptyList(),
            selectedCollectionId = null,
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
        private const val PAIRING_SUCCESS_HOLD_MS = 550L
        private val NAMESPACED_ID = Regex("^([a-z0-9._-]+):(.+)$", RegexOption.IGNORE_CASE)
        private val IMDB_ID = Regex("^tt\\d+$", RegexOption.IGNORE_CASE)
        private val DATE_ONLY = Regex("^\\d{4}-\\d{2}-\\d{2}$")

        private const val COLLECTION_ARTWORK_CONCURRENCY = 6
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
                    val model = Build.MODEL.trim().ifBlank { "Android device" }.take(120)
                    return RivuneViewModel(
                        store,
                        gatewayFactory,
                        isTv,
                        model,
                        externalPlaybackSupportProvider = { detectExternalPlaybackSupport(applicationContext) },
                    ) as T
                }
            }
        }
    }
}
internal fun normalizeServerUrl(value: String): String? {
    val trimmed = value.trim()
    if (trimmed.isEmpty() || trimmed.any(Char::isWhitespace)) return null
    val withScheme = if ("://" in trimmed) {
        trimmed
    } else {
        val host = trimmed.substringBefore('/').substringBefore(':').lowercase()
        val scheme = if (host == "localhost" || host == "127.0.0.1") "http" else "https"
        "$scheme://$trimmed"
    }
    return withScheme.trimEnd('/').takeIf(String::isNotBlank)
}

private fun CollectionItem.identity(): String = "$mediaType\u0000$id"
