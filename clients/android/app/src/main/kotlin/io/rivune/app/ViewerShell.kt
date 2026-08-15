package io.rivune.app

import android.os.Build
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.automirrored.rounded.Logout
import androidx.compose.material.icons.automirrored.rounded.OpenInNew
import androidx.compose.material.icons.rounded.CalendarMonth
import androidx.compose.material.icons.rounded.Bookmark
import androidx.compose.material.icons.rounded.Check
import androidx.compose.material.icons.rounded.ChevronLeft
import androidx.compose.material.icons.rounded.ChevronRight
import androidx.compose.material.icons.rounded.ErrorOutline
import androidx.compose.material.icons.rounded.Visibility
import androidx.compose.material.icons.rounded.VisibilityOff
import androidx.compose.material.icons.rounded.Headphones
import androidx.compose.material.icons.rounded.HighQuality
import androidx.compose.material.icons.rounded.Info
import androidx.compose.material.icons.rounded.SystemUpdate
import androidx.compose.material.icons.rounded.ContentCopy
import androidx.compose.material.icons.rounded.Dns
import androidx.compose.material.icons.rounded.FileUpload
import androidx.compose.material.icons.rounded.Language
import androidx.compose.material.icons.rounded.Palette
import androidx.compose.material.icons.rounded.LiveTv
import androidx.compose.material.icons.rounded.Movie
import androidx.compose.material.icons.rounded.MovieCreation
import androidx.compose.material.icons.rounded.SmartDisplay
import androidx.compose.material.icons.rounded.Theaters
import androidx.compose.material.icons.rounded.Translate
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material.icons.rounded.Home
import androidx.compose.material.icons.rounded.LibraryAdd
import androidx.compose.material.icons.rounded.Person
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material.icons.rounded.Refresh
import androidx.compose.material.icons.rounded.Search
import androidx.compose.material.icons.rounded.Settings
import androidx.compose.material.icons.rounded.Tv
import androidx.compose.material.icons.rounded.VideoLibrary
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.withFrameNanos
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.foundation.focusGroup
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import coil.compose.AsyncImage
import androidx.compose.ui.window.DialogProperties
import io.rivune.api.CalendarEvent
import io.rivune.api.CollectionSource
import io.rivune.api.Collection
import io.rivune.api.CollectionFolder
import io.rivune.api.CollectionSourceView
import io.rivune.api.CollectionTMDBMediaType
import io.rivune.api.CollectionTileShape
import io.rivune.api.CollectionViewMode
import io.rivune.api.CollectionItem
import io.rivune.api.LibraryItem
import io.rivune.api.PlaybackProgress
import io.rivune.api.PlaybackSourceOption
import io.rivune.api.PatchField
import io.rivune.api.ProfileSettingsUpdate
import io.rivune.api.ResolvedCollectionFolder
import io.rivune.app.ui.components.RivuneCinematicBackground
import io.rivune.app.ui.components.RivuneFunctionalSurface
import io.rivune.app.ui.components.RivuneArtwork
import io.rivune.app.ui.components.RivuneFocusSurface
import io.rivune.app.ui.components.RivunePrimaryButton
import io.rivune.app.ui.components.RivuneSecondaryButton
import io.rivune.app.ui.components.RivuneSkeleton
import io.rivune.app.ui.components.RivuneSectionHeading
import io.rivune.app.ui.components.RivuneTextButton
import io.rivune.app.ui.components.RivuneTextField
import io.rivune.app.ui.theme.LocalRivuneMotionPolicy
import io.rivune.app.ui.theme.RivuneBreakpoints
import io.rivune.app.ui.theme.RivuneDimensions
import io.rivune.app.ui.theme.RivuneElevation
import io.rivune.app.ui.theme.RivuneShapes
import io.rivune.app.ui.theme.RivuneSpacing
import io.rivune.app.ui.theme.rivuneAccentHasReadableContrast
import java.time.LocalDate
import java.time.YearMonth
import java.time.format.DateTimeFormatter
import java.time.format.DateTimeParseException
import java.time.format.FormatStyle
import java.util.Locale
import kotlin.math.ceil

private val ViewerPhoneTarget = RivuneDimensions.touchTarget
private val ViewerTvTarget = RivuneDimensions.touchTargetTv
private val ViewerCardGap = RivuneSpacing.md
private val ViewerMediaRowGap = RivuneSpacing.sm
private const val ViewerHeroScrimAlpha = 0.96f
private const val ViewerLogoWidthFraction = 0.72f
private const val ViewerSkeletonTitleFraction = 0.84f
private val ViewerAccountDialogWidthTv = 400.dp
private const val ViewerSkeletonMetadataFraction = 0.56f
private val ViewerPreferencesMaxWidth = RivuneDimensions.preferencesMax
private val ViewerTabletDockContentInset = 80.dp
private val ViewerTvDockCollapsedWidth = 72.dp
private val ViewerTvDockContentInset = 96.dp
private val ViewerTvPosterTarget = 140.dp
private val ViewerTvLandscapeTarget = 180.dp
private fun viewerHorizontalPadding(width: Dp, isTv: Boolean): Dp = when {
    isTv && width >= RivuneBreakpoints.wide -> RivuneSpacing.huge
    isTv -> RivuneSpacing.xxl
    width >= RivuneBreakpoints.wide -> RivuneSpacing.huge
    width >= RivuneBreakpoints.expanded -> RivuneSpacing.xxl
    width >= RivuneBreakpoints.medium -> RivuneSpacing.xl
    else -> RivuneSpacing.md
}

private fun viewerUsesTabletDock(width: Dp, isTv: Boolean): Boolean = !isTv && width >= RivuneBreakpoints.medium

private fun viewerGridColumns(
    width: Dp,
    targetWidth: Dp,
    minColumns: Int,
    maxColumns: Int,
): Int = ((width + ViewerCardGap) / (targetWidth + ViewerCardGap))
    .toInt()
    .coerceIn(minColumns, maxColumns)

private fun viewerGridCells(width: Dp, landscape: Boolean, isTv: Boolean): GridCells.Fixed {
    val target = when {
        isTv && landscape -> ViewerTvLandscapeTarget
        isTv -> ViewerTvPosterTarget
        landscape -> RivuneDimensions.landscapeCardWidth
        else -> RivuneDimensions.posterWidth
    }
    val minimum = when {
        isTv && !landscape -> 3
        else -> 2
    }
    val maximum = when {
        isTv && landscape -> 6
        isTv -> 8
        landscape -> 6
        else -> 8
    }
    return GridCells.Fixed(viewerGridColumns(width, target, minimum, maximum))
}

private fun viewerRowCardWidth(
    width: Dp,
    targetWidth: Dp,
    phoneVisibleCards: Float,
    isTv: Boolean,
): Dp {
    if (!isTv && width < RivuneBreakpoints.medium) {
        return ((width - ViewerCardGap * (phoneVisibleCards - 1f)) / phoneVisibleCards)
            .coerceAtMost(targetWidth)
    }
    return targetWidth
}
private val CanonicalMovieCategoryTitles = setOf("movie", "movies", "film", "films")
private val CanonicalSeriesCategoryTitles = setOf("series", "tv", "show", "shows", "tv show", "tv shows", "série", "séries")
private const val PreferredPlayerAskKey = "ask"
private const val PreferredPlayerRivuneKey = "rivune"
private const val PreferredPlayerExternalPrefix = "external:"
private const val RivuneSourceUrl = "https://github.com/moodiness/rivune"
private const val RivuneReleasesUrl = "$RivuneSourceUrl/releases/latest"
private const val RivuneIssuesUrl = "$RivuneSourceUrl/issues/new/choose"
private const val RivuneLicenseUrl = "$RivuneSourceUrl/blob/main/LICENSE"
private const val RivuneNoticeUrl = "$RivuneSourceUrl/blob/main/NOTICE"
private val AccentHexPattern = Regex("^#[0-9A-Fa-f]{6}$")

@Composable
internal fun ViewerShell(
    state: RivuneUiState,
    viewModel: RivuneViewModel,
    updateState: AppUpdateState,
    onCheckForUpdates: () -> Unit,
    appLanguage: AppLanguage,
    onAppLanguage: (AppLanguage) -> Unit,
    appPreferences: AppPreferencesState,
    onStartupTab: (ViewerTab) -> Unit,
    onPreferredPlayer: (PreferredPlayer) -> Unit,
    onAnimationPreference: (AnimationPreference) -> Unit,
    onAccentColor: (Int) -> Unit,
    onFrameRateMatching: (FrameRateMatchingPreference) -> Unit,
    onVideoAspect: (VideoAspectPreference) -> Unit,
    onWifiQuality: (NetworkQualityPreference) -> Unit,
    onMobileQuality: (NetworkQualityPreference) -> Unit,
    onChangeServer: () -> Unit,
    onOpenExternalUrl: (String) -> Unit,
    onCopyDiagnostics: () -> Unit,
    onExportLogs: () -> Unit,
) {
    val viewer = state.viewer
    val openedCollection = state.openedCollectionId?.let { id -> state.collections.firstOrNull { it.id == id } }
    when {
        viewer.player != null -> {
            BackHandler(onBack = viewModel::closePlayer)
            if (viewer.player.externalPlayer == null) {
                RivunePlayerScreen(
                    presentation = viewer.player,
                    isTv = state.isTv,
                    onProgress = viewModel::reportPlayerProgress,
                    onClose = viewModel::closePlayer,
                    onPlaybackError = viewModel::playerFailed,
                    frameRateMatching = appPreferences.frameRateMatching,
                    videoAspect = appPreferences.videoAspect,
                )
            } else {
                RivuneExternalPlayerScreen(
                    presentation = viewer.player,
                    isTv = state.isTv,
                    onResult = viewModel::externalPlaybackFinished,
                    onClose = viewModel::closePlayer,
                    onLaunchFailure = viewModel::playerFailed,
                )
            }
        }
        viewer.preferences != null -> {
            BackHandler(onBack = viewModel::closeProfilePreferences)
            ProfilePreferencesScreen(
                state = viewer.preferences,
                loading = viewer.loading == ViewerLoading.PREFERENCES,
                failure = viewer.inlineFailure,
                isTv = state.isTv,
                onBack = viewModel::closeProfilePreferences,
                onRetry = viewModel::openProfilePreferences,
                onUpdate = viewModel::updateProfilePreferences,
                updateState = updateState,
                onCheckForUpdates = onCheckForUpdates,
                appLanguage = appLanguage,
                onAppLanguage = onAppLanguage,
                deviceSettings = appPreferences,
                externalPlayers = state.externalPlayers,
                onStartupTab = onStartupTab,
                onPreferredPlayer = onPreferredPlayer,
                onAnimationPreference = onAnimationPreference,
                onAccentColor = onAccentColor,
                onFrameRateMatching = onFrameRateMatching,
                onVideoAspect = onVideoAspect,
                onWifiQuality = onWifiQuality,
                onMobileQuality = onMobileQuality,
                serverName = state.serverName,
                serverAddress = state.serverInput,
                serverVersion = state.serverVersion,
                serverProtocol = state.protocolVersion,
                onChangeServer = onChangeServer,
                onOpenExternalUrl = onOpenExternalUrl,
                onCopyDiagnostics = onCopyDiagnostics,
                onExportLogs = onExportLogs,
            )
        }
        viewer.detail != null -> {
            BackHandler(onBack = viewModel::backViewer)
            DetailScreen(
                state = viewer,
                isTv = state.isTv,
                artworkUrl = viewModel::artworkUrl,
                onOpenExternalUrl = onOpenExternalUrl,
                onBack = viewModel::backViewer,
                onSeason = viewModel::selectSeason,
                onEpisode = viewModel::playMedia,
                onPlay = { viewModel.playMedia() },
                onToggleLibrary = viewModel::toggleLibrary,
                onToggleWatched = viewModel::toggleWatched,
                externalPlayers = state.externalPlayers,
                onSelectSource = viewModel::selectPlaybackSource,
                onChooseTarget = viewModel::choosePlaybackTarget,
                onDismissTarget = viewModel::dismissPlaybackTarget,
                onDismissSources = viewModel::dismissSourcePicker,
                onRetry = viewModel::refreshViewer,
            )
        }
        state.resolvedFolder != null -> {
            BackHandler(onBack = viewModel::backViewer)
            FolderRoot(
                folder = state.resolvedFolder,
                collectionTitle = state.collections.firstOrNull { it.id == state.resolvedFolder.collectionId }?.title,
                loading = viewer.loading,
                failure = viewer.inlineFailure,
                isTv = state.isTv,
                artworkUrl = viewModel::artworkUrl,
                onBack = viewModel::backViewer,
                onItem = viewModel::openCollectionItem,
                onLoadMore = viewModel::loadMoreFolderItems,
                onRetry = viewModel::refreshViewer,
            )
        }
        openedCollection != null -> {
            BackHandler(onBack = viewModel::closeCollection)
            CollectionRoot(
                collection = openedCollection,
                isTv = state.isTv,
                artworkUrl = viewModel::artworkUrl,
                onBack = viewModel::closeCollection,
                onOpenFolder = viewModel::openFolder,
            )
        }
        else -> ViewerRoot(
            state = state,
            onTab = viewModel::selectViewerTab,
            onOpenFolder = viewModel::openFolder,
            onOpenCollection = viewModel::selectCollection,
            onMedia = viewModel::openMedia,
            onSearch = viewModel::search,
            onLoadMoreSearch = viewModel::loadMoreSearch,
            onLibraryItem = viewModel::openLibraryItem,
            onLibraryType = viewModel::setLibraryType,
            onLoadMoreLibrary = viewModel::loadMoreLibrary,
            onPreviousMonth = viewModel::previousCalendarMonth,
            onNextMonth = viewModel::nextCalendarMonth,
            onCalendarEvent = viewModel::openCalendarEvent,
            onRefresh = viewModel::refreshViewer,
            onPreferences = viewModel::openProfilePreferences,
            onChangeProfile = viewModel::changeProfile,
            onLogout = viewModel::logout,
            artworkUrl = viewModel::artworkUrl,
            profileAvatarModel = viewModel.profileAvatar(state.activeProfile),
        )
    }
}

@Composable
private fun ViewerRoot(
    state: RivuneUiState,
    onTab: (ViewerTab) -> Unit,
    onOpenFolder: (java.util.UUID, CollectionFolder) -> Unit,
    onOpenCollection: (java.util.UUID) -> Unit,
    onMedia: (MediaTarget) -> Unit,
    onSearch: (String) -> Unit,
    onLoadMoreSearch: () -> Unit,
    onLibraryItem: (LibraryItem) -> Unit,
    onLibraryType: (String?) -> Unit,
    onLoadMoreLibrary: () -> Unit,
    onPreviousMonth: () -> Unit,
    onNextMonth: () -> Unit,
    onCalendarEvent: (CalendarEvent) -> Unit,
    onRefresh: () -> Unit,
    onChangeProfile: () -> Unit,
    onPreferences: () -> Unit,
    onLogout: () -> Unit,
    profileAvatarModel: Any?,
    artworkUrl: (String?) -> String?,
) {
    var showAccount by remember { mutableStateOf(false) }
    BoxWithConstraints(modifier = Modifier.fillMaxSize()) {
        val useTabletDock = viewerUsesTabletDock(maxWidth, state.isTv)
        val usePhoneNavigation = !state.isTv && !useTabletDock
        RivuneCinematicBackground {
            Column(
                modifier = Modifier
                    .fillMaxSize()
                    .statusBarsPadding(),
            ) {
                if (usePhoneNavigation) {
                    ViewerHeader(
                        tab = state.viewer.selectedTab,
                        profileName = state.activeProfile?.name,
                        profileAvatarModel = profileAvatarModel,
                        onAccount = { showAccount = true },
                    )
                }
                Box(modifier = Modifier.weight(1f).fillMaxWidth()) {
                    Box(
                        modifier = Modifier
                            .fillMaxSize()
                            .then(
                                when {
                                    state.isTv -> Modifier
                                        .navigationBarsPadding()
                                        .padding(
                                            start = ViewerTvDockContentInset,
                                            top = RivuneSpacing.xl,
                                            bottom = RivuneSpacing.xl,
                                        )
                                    useTabletDock -> Modifier
                                        .navigationBarsPadding()
                                        .padding(top = ViewerTabletDockContentInset)
                                    else -> Modifier
                                        .navigationBarsPadding()
                                        .padding(bottom = RivuneDimensions.bottomBar)
                                },
                            ),
                    ) {
                        when (state.viewer.selectedTab) {
                            ViewerTab.HOME -> HomeRoot(
                                collections = state.collections,
                                continueWatching = state.viewer.continueWatching,
                                loading = state.viewer.loading,
                                failure = state.viewer.inlineFailure,
                                isTv = state.isTv,
                                artworkUrl = artworkUrl,
                                onOpenFolder = onOpenFolder,
                                onOpenCollection = onOpenCollection,
                                onMedia = onMedia,
                                onRetry = onRefresh,
                            )
                            ViewerTab.SEARCH -> SearchRoot(
                                state = state.viewer.search,
                                loading = state.viewer.loading,
                                failure = state.viewer.inlineFailure,
                                isTv = state.isTv,
                                artworkUrl = artworkUrl,
                                onSearch = onSearch,
                                onLoadMore = onLoadMoreSearch,
                                onMedia = onMedia,
                                onRetry = onRefresh,
                            )
                            ViewerTab.LIBRARY -> LibraryRoot(
                                state = state.viewer.library,
                                loading = state.viewer.loading,
                                failure = state.viewer.inlineFailure,
                                isTv = state.isTv,
                                artworkUrl = artworkUrl,
                                onType = onLibraryType,
                                onItem = onLibraryItem,
                                onLoadMore = onLoadMoreLibrary,
                                onRetry = onRefresh,
                            )
                            ViewerTab.CALENDAR -> CalendarRoot(
                                events = state.calendarEvents,
                                month = state.calendarMonth,
                                loading = state.viewer.loading,
                                failure = state.viewer.inlineFailure,
                                isTv = state.isTv,
                                artworkUrl = artworkUrl,
                                onPreviousMonth = onPreviousMonth,
                                onNextMonth = onNextMonth,
                                onEvent = onCalendarEvent,
                                onRetry = onRefresh,
                            )
                        }
                    }
                    if (useTabletDock) {
                        ViewerTabletDock(
                            selected = state.viewer.selectedTab,
                            profileName = state.activeProfile?.name,
                            profileAvatarModel = profileAvatarModel,
                            onSelect = onTab,
                            onAccount = { showAccount = true },
                            modifier = Modifier
                                .align(Alignment.TopCenter)
                                .padding(top = RivuneSpacing.sm),
                        )
                    }
                    if (state.isTv) {
                        ViewerTvDock(
                            selected = state.viewer.selectedTab,
                            profileName = state.activeProfile?.name,
                            profileAvatarModel = profileAvatarModel,
                            onSelect = onTab,
                            onAccount = { showAccount = true },
                            modifier = Modifier
                                .align(Alignment.CenterStart)
                                .padding(start = RivuneSpacing.sm),
                        )
                    }
                }
            }
            if (usePhoneNavigation) {
                ViewerBottomBar(
                    selected = state.viewer.selectedTab,
                    onSelect = onTab,
                    modifier = Modifier.align(Alignment.BottomCenter),
                )
            }
        }
        if (showAccount) {
            AccountDialog(
                isTv = state.isTv,
                onDismiss = { showAccount = false },
                onRefresh = {
                    showAccount = false
                    onRefresh()
                },
                onChangeProfile = {
                    showAccount = false
                    onChangeProfile()
                },
                onPreferences = {
                    showAccount = false
                    onPreferences()
                },
                onLogout = {
                    showAccount = false
                    onLogout()
                },
            )
        }
    }
}
private enum class PreferenceCategory(
    val titleResource: Int,
    val descriptionResource: Int,
    val icon: ImageVector,
    val requiresProfileSettings: Boolean,
) {
    GENERAL(
        R.string.preferences_category_general,
        R.string.preferences_category_general_body,
        Icons.Rounded.Translate,
        false,
    ),
    VIDEO(
        R.string.preferences_category_video,
        R.string.preferences_category_video_body,
        Icons.Rounded.HighQuality,
        true,
    ),
    AUDIO(
        R.string.preferences_category_audio,
        R.string.preferences_category_audio_body,
        Icons.Rounded.Headphones,
        true,
    ),
    METADATA(
        R.string.preferences_category_metadata,
        R.string.preferences_category_metadata_body,
        Icons.Rounded.Movie,
        true,
    ),
    ABOUT(
        R.string.preferences_category_about,
        R.string.preferences_category_about_body,
        Icons.Rounded.Info,
        false,
    ),
}

@Composable
private fun ProfilePreferencesScreen(
    state: ProfilePreferencesState,
    loading: Boolean,
    failure: UiFailure?,
    isTv: Boolean,
    onBack: () -> Unit,
    onRetry: () -> Unit,
    onUpdate: (ProfileSettingsUpdate) -> Unit,
    updateState: AppUpdateState,
    onCheckForUpdates: () -> Unit,
    appLanguage: AppLanguage,
    onAppLanguage: (AppLanguage) -> Unit,
    deviceSettings: AppPreferencesState,
    externalPlayers: List<ExternalPlayerApp>,
    onStartupTab: (ViewerTab) -> Unit,
    onPreferredPlayer: (PreferredPlayer) -> Unit,
    onAnimationPreference: (AnimationPreference) -> Unit,
    onAccentColor: (Int) -> Unit,
    onFrameRateMatching: (FrameRateMatchingPreference) -> Unit,
    onVideoAspect: (VideoAspectPreference) -> Unit,
    onWifiQuality: (NetworkQualityPreference) -> Unit,
    onMobileQuality: (NetworkQualityPreference) -> Unit,
    serverName: String,
    serverAddress: String,
    serverVersion: String?,
    serverProtocol: Int?,
    onChangeServer: () -> Unit,
    onOpenExternalUrl: (String) -> Unit,
    onCopyDiagnostics: () -> Unit,
    onExportLogs: () -> Unit,
) {
    val settings = state.settings
    val backFocus = remember { FocusRequester() }
    var selectedCategory by remember { mutableStateOf<PreferenceCategory?>(null) }
    var returningCategory by remember { mutableStateOf<PreferenceCategory?>(null) }
    val categoryFocus = remember { PreferenceCategory.entries.associateWith { FocusRequester() } }
    var confirmChangeServer by remember { mutableStateOf(false) }
    val cancelServerChangeFocus = remember { FocusRequester() }
    var showAppLanguageChoices by remember { mutableStateOf(false) }
    var showAccentPicker by remember { mutableStateOf(false) }
    val disabledDescription = stringResource(
        if (loading && settings != null) R.string.viewer_saving_change else R.string.viewer_preferences_read_only,
    )
    val navigateBack = {
        if (selectedCategory == null) {
            onBack()
        } else {
            returningCategory = selectedCategory
            selectedCategory = null
        }
    }
    BackHandler(onBack = navigateBack)
    LaunchedEffect(isTv, selectedCategory, returningCategory) {
        if (isTv && selectedCategory == null) {
            returningCategory?.let { categoryFocus.getValue(it).requestFocus() } ?: backFocus.requestFocus()
        }
    }
    BoxWithConstraints(modifier = Modifier.fillMaxSize().background(Color.Black)) {
        val padding = viewerHorizontalPadding(maxWidth, isTv)
        Column(
            modifier = Modifier
                .fillMaxSize()
                .statusBarsPadding()
                .navigationBarsPadding()
                .imePadding(),
        ) {
            ScreenToolbar(
                title = selectedCategory?.let { stringResource(it.titleResource) }
                    ?: stringResource(R.string.viewer_preferences),
                onBack = navigateBack,
                isTv = isTv,
                backModifier = Modifier.focusRequester(backFocus),
            )
            LazyColumn(
                modifier = Modifier.fillMaxSize(),
                contentPadding = PaddingValues(
                    start = padding,
                    top = if (isTv) RivuneSpacing.xl else RivuneSpacing.lg,
                    end = padding,
                    bottom = RivuneSpacing.huge,
                ),
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.spacedBy(if (isTv) RivuneSpacing.lg else RivuneSpacing.md),
            ) {
                val category = selectedCategory
                if (category == null) {
                    item {
                        Column(
                            modifier = Modifier.widthIn(max = ViewerPreferencesMaxWidth).fillMaxWidth(),
                            verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
                        ) {
                            Text(
                                text = stringResource(R.string.viewer_preferences_title),
                                modifier = Modifier.semantics { heading() },
                                style = if (isTv) MaterialTheme.typography.displayLarge else MaterialTheme.typography.headlineMedium,
                            )
                            Text(
                                text = stringResource(R.string.viewer_preferences_body),
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                style = if (isTv) MaterialTheme.typography.bodyLarge else MaterialTheme.typography.bodyMedium,
                            )
                        }
                    }
                    items(PreferenceCategory.entries, key = { it.name }) { preferenceCategory ->
                        PreferenceCategoryCard(
                            category = preferenceCategory,
                            isTv = isTv,
                            modifier = Modifier.focusRequester(categoryFocus.getValue(preferenceCategory)),
                            onClick = {
                                returningCategory = null
                                selectedCategory = preferenceCategory
                            },
                        )
                    }
                } else {
                    if (category.requiresProfileSettings) {
                        item {
                            Column(modifier = Modifier.widthIn(max = ViewerPreferencesMaxWidth).fillMaxWidth()) {
                                InlineStatus(
                                    loading = loading,
                                    failure = failure,
                                    onRetry = onRetry,
                                    isTv = isTv,
                                    loadingLabel = stringResource(
                                        if (settings == null) R.string.viewer_loading_preferences else R.string.viewer_saving_change,
                                    ),
                                )
                            }
                        }
                        if (settings == null && !loading) {
                            item {
                                InlineEmpty(
                                    title = stringResource(R.string.viewer_preferences_unavailable_title),
                                    body = stringResource(R.string.viewer_preferences_unavailable_body),
                                    modifier = Modifier.widthIn(max = ViewerPreferencesMaxWidth).fillMaxWidth(),
                                )
                            }
                        }
                    }
                    when (category) {
                        PreferenceCategory.GENERAL -> {
                            item {
                                AppLanguagePreferenceRow(
                                    selected = appLanguage,
                                    isTv = isTv,
                                    onClick = { showAppLanguageChoices = true },
                                )
                            }
                            item {
                                StartupPagePreference(
                                    selected = deviceSettings.startupTab,
                                    isTv = isTv,
                                    onSelect = onStartupTab,
                                )
                            }
                            item {
                                PreferredPlayerPreference(
                                    selected = deviceSettings.preferredPlayer,
                                    externalPlayers = externalPlayers,
                                    isTv = isTv,
                                    onSelect = onPreferredPlayer,
                                )
                            }
                            item {
                                AnimationPreferenceRow(
                                    selected = deviceSettings.animationPreference,
                                    isTv = isTv,
                                    onSelect = onAnimationPreference,
                                )
                            }
                            item {
                                AccentColorPreference(
                                    selected = deviceSettings.accentColor,
                                    isTv = isTv,
                                    onCustom = { showAccentPicker = true },
                                    onSelect = onAccentColor,
                                )
                            }
                        }
                        PreferenceCategory.VIDEO -> {
                            settings?.let { currentSettings ->
                                item {
                                    PreferenceChoiceCard(
                                        title = stringResource(R.string.viewer_maximum_resolution),
                                        description = stringResource(R.string.viewer_maximum_resolution_body),
                                        selected = currentSettings.maximumResolution ?: "auto",
                                        options = listOf(
                                            "auto" to stringResource(R.string.viewer_resolution_auto),
                                            "2160p" to stringResource(R.string.viewer_resolution_2160p),
                                            "1080p" to stringResource(R.string.viewer_resolution_1080p),
                                            "720p" to stringResource(R.string.viewer_resolution_720p),
                                            "480p" to stringResource(R.string.viewer_resolution_480p),
                                        ),
                                        enabled = state.canEdit && !loading,
                                        disabledDescription = disabledDescription,
                                        isTv = isTv,
                                        onSelect = { onUpdate(ProfileSettingsUpdate(maximumResolution = PatchField.Value(it))) },
                                    )
                                }
                            }
                            item {
                                FrameRatePreference(
                                    selected = deviceSettings.frameRateMatching,
                                    isTv = isTv,
                                    onSelect = onFrameRateMatching,
                                )
                            }
                            item {
                                VideoAspectPreferenceRow(
                                    selected = deviceSettings.videoAspect,
                                    isTv = isTv,
                                    onSelect = onVideoAspect,
                                )
                            }
                            item {
                                NetworkQualityPreferenceRow(
                                    title = stringResource(R.string.preferences_wifi_quality),
                                    description = stringResource(R.string.preferences_wifi_quality_body),
                                    selected = deviceSettings.wifiQuality,
                                    isTv = isTv,
                                    onSelect = onWifiQuality,
                                )
                            }
                            item {
                                NetworkQualityPreferenceRow(
                                    title = stringResource(R.string.preferences_mobile_quality),
                                    description = stringResource(R.string.preferences_mobile_quality_body),
                                    selected = deviceSettings.mobileQuality,
                                    isTv = isTv,
                                    onSelect = onMobileQuality,
                                )
                            }
                        }
                        PreferenceCategory.AUDIO -> settings?.let { currentSettings ->
                            item {
                                LanguagePreferenceCard(
                                    title = stringResource(R.string.viewer_audio_language),
                                    description = stringResource(R.string.viewer_language_body),
                                    selected = currentSettings.audioLanguage ?: "auto",
                                    enabled = state.canEdit && !loading,
                                    disabledDescription = disabledDescription,
                                    isTv = isTv,
                                    onSelect = { onUpdate(ProfileSettingsUpdate(audioLanguage = PatchField.Value(it))) },
                                )
                            }
                            item {
                                LanguagePreferenceCard(
                                    title = stringResource(R.string.viewer_subtitle_language),
                                    description = stringResource(R.string.viewer_language_body),
                                    selected = currentSettings.subtitleLanguage ?: "auto",
                                    enabled = state.canEdit && !loading,
                                    disabledDescription = disabledDescription,
                                    isTv = isTv,
                                    onSelect = { onUpdate(ProfileSettingsUpdate(subtitleLanguage = PatchField.Value(it))) },
                                )
                            }
                        }
                        PreferenceCategory.METADATA -> settings?.let { currentSettings ->
                            item {
                                LanguagePreferenceCard(
                                    title = stringResource(R.string.viewer_metadata_language),
                                    description = stringResource(R.string.viewer_metadata_language_body),
                                    selected = currentSettings.metadataLanguage ?: "auto",
                                    enabled = state.canEdit && !loading,
                                    disabledDescription = disabledDescription,
                                    isTv = isTv,
                                    onSelect = { onUpdate(ProfileSettingsUpdate(metadataLanguage = PatchField.Value(it))) },
                                )
                            }
                        }
                        PreferenceCategory.ABOUT -> item {
                            AboutPreferencesPanel(
                                updateState = updateState,
                                isTv = isTv,
                                onCheckForUpdates = onCheckForUpdates,
                                serverName = serverName,
                                serverAddress = serverAddress,
                                serverVersion = serverVersion,
                                serverProtocol = serverProtocol,
                                onChangeServer = { confirmChangeServer = true },
                                onOpenExternalUrl = onOpenExternalUrl,
                                onCopyDiagnostics = onCopyDiagnostics,
                                onExportLogs = onExportLogs,
                            )
                        }
                    }
                }
            }
        }
    }
    if (showAppLanguageChoices) {
        AppLanguageChoiceDialog(
            selected = appLanguage,
            isTv = isTv,
            onDismiss = { showAppLanguageChoices = false },
            onSelect = { language ->
                showAppLanguageChoices = false
                onAppLanguage(language)
            },
        )
    }
    if (showAccentPicker) {
        AccentColorDialog(
            selected = deviceSettings.accentColor,
            isTv = isTv,
            onDismiss = { showAccentPicker = false },
            onSelect = { color ->
                showAccentPicker = false
                onAccentColor(color)
            },
        )
    }
    if (confirmChangeServer) {
        LaunchedEffect(isTv) {
            if (isTv) cancelServerChangeFocus.requestFocus()
        }
        AlertDialog(
            onDismissRequest = { confirmChangeServer = false },
            title = { Text(stringResource(R.string.preferences_change_server_confirm_title)) },
            text = { Text(stringResource(R.string.preferences_change_server_confirm_body)) },
            confirmButton = {
                RivuneTextButton(
                    label = stringResource(R.string.preferences_change_server_confirm_action),
                    onClick = {
                        confirmChangeServer = false
                        onChangeServer()
                    },
                    isTv = isTv,
                    destructive = true,
                )
            },
            dismissButton = {
                RivuneTextButton(
                    label = stringResource(R.string.pin_cancel),
                    onClick = { confirmChangeServer = false },
                    modifier = Modifier.focusRequester(cancelServerChangeFocus),
                    isTv = isTv,
                )
            },
        )
    }
}

@Composable
private fun AppLanguagePreferenceRow(
    selected: AppLanguage,
    isTv: Boolean,
    onClick: () -> Unit,
) {
    val selectedLabel = when (selected) {
        AppLanguage.ENGLISH -> stringResource(R.string.viewer_language_english)
        AppLanguage.FRENCH -> stringResource(R.string.viewer_language_french)
        AppLanguage.SYSTEM -> stringResource(R.string.preferences_language_system)
    }
    RivuneFocusSurface(
        onClick = onClick,
        isTv = isTv,
        idleColor = Color.Transparent,
        shape = RivuneShapes.medium,
        modifier = Modifier.widthIn(max = ViewerPreferencesMaxWidth).fillMaxWidth(),
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .heightIn(min = if (isTv) ViewerTvTarget else ViewerPhoneTarget)
                .padding(
                    horizontal = if (isTv) RivuneSpacing.xl else RivuneSpacing.md,
                    vertical = RivuneSpacing.sm,
                ),
            horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Column(
                modifier = Modifier.weight(1f),
                verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xxs),
            ) {
                Text(
                    text = stringResource(R.string.preferences_app_language),
                    style = if (isTv) MaterialTheme.typography.titleLarge else MaterialTheme.typography.titleMedium,
                )
                Text(
                    text = stringResource(R.string.preferences_app_language_body),
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    style = if (isTv) MaterialTheme.typography.bodyLarge else MaterialTheme.typography.bodyMedium,
                )
            }
            Row(
                horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(
                    text = selectedLabel,
                    color = MaterialTheme.colorScheme.primary,
                    style = MaterialTheme.typography.labelLarge,
                )
                Icon(
                    imageVector = Icons.Rounded.ChevronRight,
                    contentDescription = null,
                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun AppLanguageChoiceDialog(
    selected: AppLanguage,
    isTv: Boolean,
    onDismiss: () -> Unit,
    onSelect: (AppLanguage) -> Unit,
) {
    val selectedFocus = remember { FocusRequester() }
    val options = listOf(
        AppLanguage.ENGLISH to stringResource(R.string.viewer_language_english),
        AppLanguage.FRENCH to stringResource(R.string.viewer_language_french),
        AppLanguage.SYSTEM to stringResource(R.string.preferences_language_system),
    )
    LaunchedEffect(isTv, selected) {
        if (isTv) selectedFocus.requestFocus()
    }
    BackHandler(onBack = onDismiss)
    val content: @Composable (Modifier) -> Unit = { modifier ->
        Column(modifier = modifier) {
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = if (isTv) RivuneSpacing.xl else RivuneSpacing.md),
                horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(
                    text = stringResource(R.string.preferences_app_language),
                    modifier = Modifier.weight(1f).semantics { heading() },
                    style = if (isTv) MaterialTheme.typography.headlineMedium else MaterialTheme.typography.titleLarge,
                )
                RivuneTextButton(
                    label = stringResource(R.string.pin_cancel),
                    onClick = onDismiss,
                    isTv = isTv,
                )
            }
            options.forEachIndexed { index, (language, label) ->
                val isSelected = language == selected
                if (index > 0) {
                    HorizontalDivider(
                        modifier = Modifier.padding(horizontal = if (isTv) RivuneSpacing.xl else RivuneSpacing.md),
                        color = MaterialTheme.colorScheme.outlineVariant,
                    )
                }
                RivuneFocusSurface(
                    onClick = { if (!isSelected) onSelect(language) },
                    selected = isSelected,
                    selectedColor = if (isTv) MaterialTheme.colorScheme.surfaceContainerHighest else Color.Transparent,
                    showSelectionBorder = isTv,
                    idleColor = Color.Transparent,
                    isTv = isTv,
                    shape = RivuneShapes.small,
                    modifier = Modifier
                        .fillMaxWidth()
                        .heightIn(min = if (isTv) ViewerTvTarget else ViewerPhoneTarget)
                        .then(if (isSelected) Modifier.focusRequester(selectedFocus) else Modifier),
                ) {
                    Row(
                        modifier = Modifier
                            .fillMaxWidth()
                            .padding(
                                horizontal = if (isTv) RivuneSpacing.xl else RivuneSpacing.md,
                                vertical = RivuneSpacing.sm,
                            ),
                        horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Text(
                            text = label,
                            modifier = Modifier.weight(1f),
                            color = if (isSelected) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurface,
                            style = MaterialTheme.typography.bodyLarge,
                        )
                        if (isSelected) {
                            Icon(
                                imageVector = Icons.Rounded.Check,
                                contentDescription = null,
                                modifier = Modifier.size(RivuneDimensions.iconMedium),
                                tint = MaterialTheme.colorScheme.primary,
                            )
                        }
                    }
                }
            }
        }
    }
    BoxWithConstraints(modifier = Modifier.fillMaxSize()) {
        if (!isTv && maxWidth < RivuneBreakpoints.medium) {
            ModalBottomSheet(
                onDismissRequest = onDismiss,
                containerColor = MaterialTheme.colorScheme.background,
                contentColor = MaterialTheme.colorScheme.onSurface,
                shape = RivuneShapes.extraLarge,
                tonalElevation = 0.dp,
            ) {
                content(
                    Modifier
                        .fillMaxWidth()
                        .navigationBarsPadding()
                        .padding(bottom = RivuneSpacing.md),
                )
            }
        } else {
            Dialog(
                onDismissRequest = onDismiss,
                properties = DialogProperties(usePlatformDefaultWidth = false),
            ) {
                Box(
                    modifier = Modifier
                        .fillMaxSize()
                        .padding(
                            horizontal = if (isTv) RivuneSpacing.huge else RivuneSpacing.xl,
                            vertical = RivuneSpacing.xl,
                        ),
                    contentAlignment = Alignment.Center,
                ) {
                    RivuneFunctionalSurface(
                        modifier = Modifier.widthIn(max = RivuneDimensions.dialogMax).fillMaxWidth(),
                        shape = RivuneShapes.large,
                        contentPadding = PaddingValues(vertical = RivuneSpacing.md),
                    ) {
                        content(Modifier.fillMaxWidth())
                    }
                }
            }
        }
    }
}

@Composable
private fun StartupPagePreference(
    selected: ViewerTab,
    isTv: Boolean,
    onSelect: (ViewerTab) -> Unit,
) {
    PreferenceChoiceCard(
        title = stringResource(R.string.preferences_startup_page),
        description = stringResource(R.string.preferences_startup_page_body),
        selected = selected.name,
        options = ViewerTab.entries.map { tab -> tab.name to stringResource(tab.titleResource()) },
        enabled = true,
        disabledDescription = "",
        isTv = isTv,
        onSelect = { value -> ViewerTab.entries.firstOrNull { it.name == value }?.let(onSelect) },
    )
}

@Composable
private fun PreferredPlayerPreference(
    selected: PreferredPlayer,
    externalPlayers: List<ExternalPlayerApp>,
    isTv: Boolean,
    onSelect: (PreferredPlayer) -> Unit,
) {
    val selectedKey = preferredPlayerKey(selected)
    val installedOptions = externalPlayers
        .distinctBy(ExternalPlayerApp::packageName)
        .map { player -> preferredPlayerKey(PreferredPlayer.External(player.packageName)) to player.label }
    val missingSelection = (selected as? PreferredPlayer.External)
        ?.takeIf { preferred -> externalPlayers.none { it.packageName == preferred.packageName } }
        ?.let { preferred ->
            preferredPlayerKey(preferred) to stringResource(
                R.string.preferences_player_unavailable,
                preferred.packageName,
            )
        }
    val options = buildList {
        add(preferredPlayerKey(PreferredPlayer.Ask) to stringResource(R.string.preferences_player_ask))
        add(preferredPlayerKey(PreferredPlayer.Rivune) to stringResource(R.string.viewer_player_rivune))
        addAll(installedOptions)
        missingSelection?.let { add(it) }
    }
    PreferenceChoiceCard(
        title = stringResource(R.string.preferences_preferred_player),
        description = stringResource(R.string.preferences_preferred_player_body),
        selected = selectedKey,
        options = options,
        enabled = true,
        disabledDescription = "",
        isTv = isTv,
        onSelect = { value ->
            when (value) {
                PreferredPlayerAskKey -> onSelect(PreferredPlayer.Ask)
                PreferredPlayerRivuneKey -> onSelect(PreferredPlayer.Rivune)
                else -> value.removePrefix(PreferredPlayerExternalPrefix)
                    .takeIf { value.startsWith(PreferredPlayerExternalPrefix) && it.isNotBlank() }
                    ?.let { onSelect(PreferredPlayer.External(it)) }
            }
        },
    )
}

private fun preferredPlayerKey(player: PreferredPlayer): String = when (player) {
    PreferredPlayer.Ask -> PreferredPlayerAskKey
    PreferredPlayer.Rivune -> PreferredPlayerRivuneKey
    is PreferredPlayer.External -> "$PreferredPlayerExternalPrefix${player.packageName}"
}

@Composable
private fun AnimationPreferenceRow(
    selected: AnimationPreference,
    isTv: Boolean,
    onSelect: (AnimationPreference) -> Unit,
) {
    val options = listOf(
        AnimationPreference.SYSTEM to stringResource(R.string.preferences_animations_system),
        AnimationPreference.FULL to stringResource(R.string.preferences_animations_full),
        AnimationPreference.REDUCED to stringResource(R.string.preferences_animations_reduced),
    )
    PreferenceChoiceCard(
        title = stringResource(R.string.preferences_animations),
        description = stringResource(R.string.preferences_animations_body),
        selected = selected.name,
        options = options.map { it.first.name to it.second },
        enabled = true,
        disabledDescription = "",
        isTv = isTv,
        onSelect = { value -> options.firstOrNull { it.first.name == value }?.first?.let(onSelect) },
    )
}

@Composable
private fun FrameRatePreference(
    selected: FrameRateMatchingPreference,
    isTv: Boolean,
    onSelect: (FrameRateMatchingPreference) -> Unit,
) {
    val options = buildList {
        add(FrameRateMatchingPreference.SYSTEM to stringResource(R.string.preferences_frame_rate_system))
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            add(FrameRateMatchingPreference.ENABLED to stringResource(R.string.preferences_option_on))
        }
        add(FrameRateMatchingPreference.DISABLED to stringResource(R.string.preferences_option_off))
    }
    PreferenceChoiceCard(
        title = stringResource(R.string.preferences_frame_rate),
        description = stringResource(R.string.preferences_frame_rate_body),
        selected = selected.preferenceValue,
        options = options.map { it.first.preferenceValue to it.second },
        enabled = true,
        disabledDescription = "",
        isTv = isTv,
        onSelect = { value -> options.firstOrNull { it.first.preferenceValue == value }?.first?.let(onSelect) },
    )
}

@Composable
private fun VideoAspectPreferenceRow(
    selected: VideoAspectPreference,
    isTv: Boolean,
    onSelect: (VideoAspectPreference) -> Unit,
) {
    val options = listOf(
        VideoAspectPreference.FIT to stringResource(R.string.preferences_aspect_fit),
        VideoAspectPreference.FILL to stringResource(R.string.preferences_aspect_fill),
        VideoAspectPreference.ZOOM to stringResource(R.string.preferences_aspect_zoom),
    )
    PreferenceChoiceCard(
        title = stringResource(R.string.preferences_aspect),
        description = stringResource(R.string.preferences_aspect_body),
        selected = selected.preferenceValue,
        options = options.map { it.first.preferenceValue to it.second },
        enabled = true,
        disabledDescription = "",
        isTv = isTv,
        onSelect = { value -> options.firstOrNull { it.first.preferenceValue == value }?.first?.let(onSelect) },
    )
}

@Composable
private fun NetworkQualityPreferenceRow(
    title: String,
    description: String,
    selected: NetworkQualityPreference,
    isTv: Boolean,
    onSelect: (NetworkQualityPreference) -> Unit,
) {
    val options = listOf(
        NetworkQualityPreference.AUTOMATIC to stringResource(R.string.preferences_quality_automatic),
        NetworkQualityPreference.ECONOMY to stringResource(R.string.preferences_quality_economy),
        NetworkQualityPreference.BALANCED to stringResource(R.string.preferences_quality_balanced),
        NetworkQualityPreference.MAXIMUM to stringResource(R.string.preferences_quality_maximum),
    )
    PreferenceChoiceCard(
        title = title,
        description = description,
        selected = selected.preferenceValue,
        options = options.map { it.first.preferenceValue to it.second },
        enabled = true,
        disabledDescription = "",
        isTv = isTv,
        onSelect = { value -> options.firstOrNull { it.first.preferenceValue == value }?.first?.let(onSelect) },
    )
}

private data class AccentPreset(val color: Int, val labelResource: Int)

private val AccentPresets = listOf(
    AccentPreset(DEFAULT_ACCENT_COLOR, R.string.preferences_accent_blue),
    AccentPreset(0xFFFF8F70.toInt(), R.string.preferences_accent_coral),
    AccentPreset(0xFF71C99A.toInt(), R.string.preferences_accent_green),
    AccentPreset(0xFFC29AFF.toInt(), R.string.preferences_accent_violet),
    AccentPreset(0xFFFF7D8C.toInt(), R.string.preferences_accent_rose),
)

@Composable
private fun AccentColorPreference(
    selected: Int,
    isTv: Boolean,
    onCustom: () -> Unit,
    onSelect: (Int) -> Unit,
) {
    val selectedHex = accentHex(selected)
    val isPreset = AccentPresets.any { accentHex(it.color) == selectedHex }
    PreferenceChoiceCard(
        title = stringResource(R.string.preferences_accent_color),
        description = stringResource(R.string.preferences_accent_color_body),
        selected = selectedHex,
        options = buildList {
            addAll(AccentPresets.map { preset -> accentHex(preset.color) to stringResource(preset.labelResource) })
            if (!isPreset) add(selectedHex to stringResource(R.string.preferences_accent_custom_value, selectedHex))
        },
        enabled = true,
        disabledDescription = "",
        isTv = isTv,
        onSelect = { value -> parseAccentHex(value)?.let(onSelect) },
        optionLeading = { value ->
            Box(
                modifier = Modifier
                    .size(RivuneDimensions.iconMedium)
                    .clip(CircleShape)
                    .background(Color(parseAccentHex(value) ?: selected)),
            )
        },
        extraContent = {
            RivuneSecondaryButton(
                label = stringResource(R.string.preferences_accent_custom),
                onClick = onCustom,
                isTv = isTv,
                icon = Icons.Rounded.Palette,
                modifier = Modifier.fillMaxWidth(),
            )
        },
    )
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun AccentColorDialog(
    selected: Int,
    isTv: Boolean,
    onDismiss: () -> Unit,
    onSelect: (Int) -> Unit,
) {
    var hex by remember(selected) { mutableStateOf(accentHex(selected)) }
    var red by remember(selected) { mutableStateOf(((selected ushr 16) and 0xFF).toString()) }
    var green by remember(selected) { mutableStateOf(((selected ushr 8) and 0xFF).toString()) }
    var blue by remember(selected) { mutableStateOf((selected and 0xFF).toString()) }
    val parsedHex = parseAccentHex(hex)
    val rgbValid = listOf(red, green, blue).all { channel -> channel.toIntOrNull()?.let { it in 0..255 } == true }
    val readable = parsedHex?.let(::rivuneAccentHasReadableContrast) == true
    val parsed = parsedHex.takeIf { rgbValid && readable }
    val firstFocus = remember { FocusRequester() }
    LaunchedEffect(isTv) {
        if (isTv) firstFocus.requestFocus()
    }
    BackHandler(onBack = onDismiss)
    val content: @Composable (Modifier) -> Unit = { modifier ->
        Column(
            modifier = modifier
                .verticalScroll(rememberScrollState())
                .padding(horizontal = if (isTv) RivuneSpacing.xl else RivuneSpacing.md),
            verticalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
        ) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(
                    text = stringResource(R.string.preferences_accent_custom),
                    modifier = Modifier.weight(1f).semantics { heading() },
                    style = if (isTv) MaterialTheme.typography.headlineMedium else MaterialTheme.typography.titleLarge,
                )
                RivuneTextButton(
                    label = stringResource(R.string.pin_cancel),
                    onClick = onDismiss,
                    isTv = isTv,
                )
            }
            RivuneTextField(
                value = hex,
                onValueChange = { value ->
                    val normalized = value.trim().uppercase(Locale.US).let { if (it.startsWith("#")) it else "#$it" }
                    if (normalized.length <= 7) {
                        hex = normalized
                        parseAccentHex(normalized)?.let { color ->
                            red = ((color ushr 16) and 0xFF).toString()
                            green = ((color ushr 8) and 0xFF).toString()
                            blue = (color and 0xFF).toString()
                        }
                    }
                },
                label = stringResource(R.string.preferences_accent_hex),
                placeholder = stringResource(R.string.preferences_accent_hex_hint),
                modifier = Modifier.fillMaxWidth().focusRequester(firstFocus),
                isError = parsedHex == null || !readable,
                supportingText = when {
                    parsedHex == null -> stringResource(R.string.preferences_accent_invalid)
                    !readable -> stringResource(R.string.preferences_accent_too_dark)
                    else -> null
                },
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Ascii, imeAction = ImeAction.Next),
                isTv = isTv,
            )
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.sm),
            ) {
                listOf(
                    Triple(red, R.string.preferences_accent_red, { value: String -> red = value }),
                    Triple(green, R.string.preferences_accent_green_channel, { value: String -> green = value }),
                    Triple(blue, R.string.preferences_accent_blue_channel, { value: String -> blue = value }),
                ).forEachIndexed { index, (channel, label, setChannel) ->
                    RivuneTextField(
                        value = channel,
                        onValueChange = { value ->
                            if (value.length <= 3 && value.all(Char::isDigit)) {
                                setChannel(value)
                                val channels = listOf(red, green, blue).toMutableList().also { it[index] = value }
                                rgbHex(channels[0], channels[1], channels[2])?.let { hex = it }
                            }
                        },
                        label = stringResource(label),
                        modifier = Modifier.weight(1f),
                        isError = channel.toIntOrNull()?.let { it in 0..255 } != true,
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number, imeAction = ImeAction.Next),
                        isTv = isTv,
                    )
                }
            }
            Text(
                text = stringResource(R.string.preferences_accent_rgb_hint),
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                style = MaterialTheme.typography.bodySmall,
            )
            RivunePrimaryButton(
                label = stringResource(R.string.viewer_apply),
                onClick = { parsed?.let(onSelect) },
                enabled = parsed != null,
                isTv = isTv,
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
    BoxWithConstraints(modifier = Modifier.fillMaxSize()) {
        if (!isTv && maxWidth < RivuneBreakpoints.medium) {
            ModalBottomSheet(
                onDismissRequest = onDismiss,
                containerColor = MaterialTheme.colorScheme.surface,
                contentColor = MaterialTheme.colorScheme.onSurface,
                shape = RivuneShapes.extraLarge,
            ) {
                content(Modifier.fillMaxWidth().navigationBarsPadding().padding(bottom = RivuneSpacing.md))
            }
        } else {
            Dialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false)) {
                Box(
                    modifier = Modifier.fillMaxSize().padding(
                        horizontal = if (isTv) RivuneSpacing.huge else RivuneSpacing.xl,
                        vertical = RivuneSpacing.xl,
                    ),
                    contentAlignment = Alignment.Center,
                ) {
                    RivuneFunctionalSurface(
                        modifier = Modifier.widthIn(max = RivuneDimensions.dialogMax).fillMaxWidth(),
                        shape = RivuneShapes.large,
                        contentPadding = PaddingValues(vertical = RivuneSpacing.md),
                    ) {
                        content(Modifier.fillMaxWidth())
                    }
                }
            }
        }
    }
}

private fun accentHex(color: Int): String = String.format(Locale.US, "#%06X", color and 0xFFFFFF)

private fun parseAccentHex(value: String): Int? = value
    .takeIf(AccentHexPattern::matches)
    ?.substring(1)
    ?.toLongOrNull(16)
    ?.let { (0xFF000000L or it).toInt() }


private fun rgbHex(red: String, green: String, blue: String): String? {
    val channels = listOf(red, green, blue).map { it.toIntOrNull() ?: return null }
    if (channels.any { it !in 0..255 }) return null
    return String.format(Locale.US, "#%02X%02X%02X", channels[0], channels[1], channels[2])
}

@Composable
private fun PreferenceCategoryCard(
    category: PreferenceCategory,
    isTv: Boolean,
    modifier: Modifier = Modifier,
    onClick: () -> Unit,
) {
    RivuneFocusSurface(
        onClick = onClick,
        isTv = isTv,
        idleColor = Color.Transparent,
        shape = RivuneShapes.medium,
        modifier = modifier.widthIn(max = ViewerPreferencesMaxWidth).fillMaxWidth(),
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = if (isTv) RivuneSpacing.xl else RivuneSpacing.md, vertical = RivuneSpacing.md),
            horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Box(
                modifier = Modifier
                    .size(if (isTv) ViewerTvTarget else ViewerPhoneTarget)
                    .clip(CircleShape)
                    .background(MaterialTheme.colorScheme.primary.copy(alpha = 0.12f)),
                contentAlignment = Alignment.Center,
            ) {
                Icon(
                    imageVector = category.icon,
                    contentDescription = null,
                    tint = MaterialTheme.colorScheme.primary,
                    modifier = Modifier.size(RivuneDimensions.iconMedium),
                )
            }
            Column(
                modifier = Modifier.weight(1f),
                verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xxs),
            ) {
                Text(
                    text = stringResource(category.titleResource),
                    style = if (isTv) MaterialTheme.typography.titleLarge else MaterialTheme.typography.titleMedium,
                )
                Text(
                    text = stringResource(category.descriptionResource),
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                    style = if (isTv) MaterialTheme.typography.bodyLarge else MaterialTheme.typography.bodyMedium,
                )
            }
            Icon(
                imageVector = Icons.Rounded.ChevronRight,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun AboutPreferencesPanel(
    updateState: AppUpdateState,
    isTv: Boolean,
    onCheckForUpdates: () -> Unit,
    serverName: String,
    serverAddress: String,
    serverVersion: String?,
    serverProtocol: Int?,
    onChangeServer: () -> Unit,
    onOpenExternalUrl: (String) -> Unit,
    onCopyDiagnostics: () -> Unit,
    onExportLogs: () -> Unit,
) {
    val unavailable = stringResource(R.string.viewer_version_unavailable)
    val displayedServerName = safeDiagnosticField(serverName) ?: unavailable
    val displayedServerAddress = remember(serverAddress, unavailable) {
        sanitizeServerOrigin(serverAddress) ?: unavailable
    }
    val displayedServerVersion = safeDiagnosticField(serverVersion) ?: unavailable
    val displayedProtocol = serverProtocol?.toString() ?: unavailable
    Column(
        modifier = Modifier.widthIn(max = ViewerPreferencesMaxWidth).fillMaxWidth(),
        verticalArrangement = Arrangement.spacedBy(if (isTv) RivuneSpacing.lg else RivuneSpacing.md),
    ) {
        RivuneFunctionalSurface(
            modifier = Modifier.fillMaxWidth(),
            shape = RivuneShapes.large,
            contentPadding = PaddingValues(if (isTv) RivuneSpacing.xl else RivuneSpacing.md),
            color = Color.Transparent,
        ) {
            Column(verticalArrangement = Arrangement.spacedBy(RivuneSpacing.md)) {
                SettingsPanelTitle(
                    icon = Icons.Rounded.SmartDisplay,
                    title = stringResource(R.string.preferences_about_rivune),
                    isTv = isTv,
                )
                DiagnosticValue(
                    label = stringResource(R.string.preferences_app_build),
                    value = stringResource(
                        R.string.preferences_app_build_value,
                        BuildConfig.VERSION_NAME,
                        BuildConfig.VERSION_CODE,
                    ),
                    isTv = isTv,
                )
                Text(
                    text = updatePreferenceStatus(updateState),
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    style = if (isTv) MaterialTheme.typography.bodyLarge else MaterialTheme.typography.bodyMedium,
                )
                if (updateState !is AppUpdateState.Unavailable) {
                    RivuneSecondaryButton(
                        label = stringResource(R.string.update_check),
                        onClick = onCheckForUpdates,
                        enabled = updateState !is AppUpdateState.Checking &&
                            updateState !is AppUpdateState.Downloading && updateState !is AppUpdateState.Installing,
                        loading = updateState is AppUpdateState.Checking,
                        isTv = isTv,
                        icon = Icons.Rounded.SystemUpdate,
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
            }
        }
        RivuneFunctionalSurface(
            modifier = Modifier.fillMaxWidth(),
            shape = RivuneShapes.large,
            contentPadding = PaddingValues(if (isTv) RivuneSpacing.xl else RivuneSpacing.md),
            color = Color.Transparent,
        ) {
            Column(verticalArrangement = Arrangement.spacedBy(RivuneSpacing.md)) {
                SettingsPanelTitle(
                    icon = Icons.Rounded.Dns,
                    title = stringResource(R.string.preferences_connected_server),
                    isTv = isTv,
                )
                DiagnosticValue(stringResource(R.string.preferences_server_name), displayedServerName, isTv)
                DiagnosticValue(stringResource(R.string.preferences_server_address), displayedServerAddress, isTv)
                DiagnosticValue(stringResource(R.string.preferences_server_version), displayedServerVersion, isTv)
                DiagnosticValue(stringResource(R.string.preferences_server_protocol), displayedProtocol, isTv)
                RivuneSecondaryButton(
                    label = stringResource(R.string.preferences_change_server),
                    onClick = onChangeServer,
                    isTv = isTv,
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        }
        RivuneFunctionalSurface(
            modifier = Modifier.fillMaxWidth(),
            shape = RivuneShapes.large,
            contentPadding = PaddingValues(vertical = RivuneSpacing.xs),
            color = Color.Transparent,
        ) {
            Column {
                AboutActionRow(
                    label = stringResource(R.string.preferences_latest_release),
                    icon = Icons.Rounded.SystemUpdate,
                    isTv = isTv,
                    onClick = { onOpenExternalUrl(RivuneReleasesUrl) },
                )
                AboutActionRow(
                    label = stringResource(R.string.preferences_source_code),
                    icon = Icons.Rounded.Language,
                    isTv = isTv,
                    onClick = { onOpenExternalUrl(RivuneSourceUrl) },
                )
                AboutActionRow(
                    label = stringResource(R.string.preferences_report_problem),
                    icon = Icons.Rounded.ErrorOutline,
                    isTv = isTv,
                    onClick = { onOpenExternalUrl(RivuneIssuesUrl) },
                )
                AboutActionRow(
                    label = stringResource(R.string.preferences_license),
                    icon = Icons.Rounded.Info,
                    isTv = isTv,
                    onClick = { onOpenExternalUrl(RivuneLicenseUrl) },
                )
                AboutActionRow(
                    label = stringResource(R.string.preferences_notice),
                    icon = Icons.Rounded.Info,
                    isTv = isTv,
                    onClick = { onOpenExternalUrl(RivuneNoticeUrl) },
                )
            }
        }
        RivuneFunctionalSurface(
            modifier = Modifier.fillMaxWidth(),
            shape = RivuneShapes.large,
            contentPadding = PaddingValues(if (isTv) RivuneSpacing.xl else RivuneSpacing.md),
            color = Color.Transparent,
        ) {
            Column(verticalArrangement = Arrangement.spacedBy(RivuneSpacing.md)) {
                SettingsPanelTitle(
                    icon = Icons.Rounded.ContentCopy,
                    title = stringResource(R.string.preferences_diagnostics),
                    isTv = isTv,
                )
                Text(
                    text = stringResource(R.string.preferences_diagnostics_body),
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    style = if (isTv) MaterialTheme.typography.bodyLarge else MaterialTheme.typography.bodyMedium,
                )
                RivuneSecondaryButton(
                    label = stringResource(R.string.preferences_copy_diagnostics),
                    onClick = onCopyDiagnostics,
                    isTv = isTv,
                    icon = Icons.Rounded.ContentCopy,
                    modifier = Modifier.fillMaxWidth(),
                )
                RivuneSecondaryButton(
                    label = stringResource(R.string.preferences_export_logs),
                    onClick = onExportLogs,
                    isTv = isTv,
                    icon = Icons.Rounded.FileUpload,
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        }
    }
}

@Composable
private fun SettingsPanelTitle(icon: ImageVector, title: String, isTv: Boolean) {
    Row(
        horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(
            modifier = Modifier
                .size(if (isTv) ViewerTvTarget else ViewerPhoneTarget)
                .clip(CircleShape)
                .background(MaterialTheme.colorScheme.primary.copy(alpha = 0.12f)),
            contentAlignment = Alignment.Center,
        ) {
            Icon(icon, contentDescription = null, tint = MaterialTheme.colorScheme.primary)
        }
        Text(
            text = title,
            modifier = Modifier.weight(1f),
            style = if (isTv) MaterialTheme.typography.titleLarge else MaterialTheme.typography.titleMedium,
        )
    }
}

@Composable
private fun DiagnosticValue(label: String, value: String, isTv: Boolean) {
    Column(verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xxs)) {
        Text(
            text = label,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            style = MaterialTheme.typography.labelLarge,
        )
        Text(
            text = value,
            style = if (isTv) MaterialTheme.typography.bodyLarge else MaterialTheme.typography.bodyMedium,
        )
    }
}

@Composable
private fun AboutActionRow(label: String, icon: ImageVector, isTv: Boolean, onClick: () -> Unit) {
    RivuneFocusSurface(
        onClick = onClick,
        isTv = isTv,
        idleColor = Color.Transparent,
        shape = RivuneShapes.small,
        modifier = Modifier.fillMaxWidth(),
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .heightIn(min = if (isTv) ViewerTvTarget else ViewerPhoneTarget)
                .padding(horizontal = if (isTv) RivuneSpacing.xl else RivuneSpacing.md, vertical = RivuneSpacing.sm),
            horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(icon, contentDescription = null, tint = MaterialTheme.colorScheme.primary)
            Text(label, modifier = Modifier.weight(1f), style = MaterialTheme.typography.bodyLarge)
            Icon(Icons.AutoMirrored.Rounded.OpenInNew, contentDescription = null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}


private fun safeDiagnosticField(value: String?): String? = sanitizeDiagnosticDisplayField(value)

@Composable
private fun updatePreferenceStatus(state: AppUpdateState): String = when (state) {
    is AppUpdateState.Checking -> stringResource(R.string.update_checking)
    is AppUpdateState.UpToDate -> stringResource(R.string.update_up_to_date, state.currentVersion)
    is AppUpdateState.Available -> stringResource(R.string.update_available_status, state.manifest.version)
    is AppUpdateState.Downloading -> stringResource(R.string.update_downloading)
    is AppUpdateState.ReadyToInstall, is AppUpdateState.NeedsPermission -> stringResource(R.string.update_ready_status)
    is AppUpdateState.Installing -> stringResource(R.string.update_installing)
    is AppUpdateState.Error -> stringResource(R.string.update_failed_status)
    AppUpdateState.Unavailable -> stringResource(R.string.update_unavailable)
    AppUpdateState.Idle -> stringResource(R.string.update_idle)
}

@Composable
private fun LanguagePreferenceCard(
    title: String,
    description: String,
    selected: String,
    enabled: Boolean,
    disabledDescription: String,
    isTv: Boolean,
    onSelect: (String) -> Unit,
) {
    val keyboardController = LocalSoftwareKeyboardController.current
    val standardOptions = listOf(
        "auto" to stringResource(R.string.viewer_language_auto),
        "en" to stringResource(R.string.viewer_language_english),
        "fr" to stringResource(R.string.viewer_language_french),
        "es" to stringResource(R.string.viewer_language_spanish),
        "de" to stringResource(R.string.viewer_language_german),
        "it" to stringResource(R.string.viewer_language_italian),
        "pt" to stringResource(R.string.viewer_language_portuguese),
        "ja" to stringResource(R.string.viewer_language_japanese),
    )
    val options = if (standardOptions.none { it.first == selected }) {
        standardOptions + (selected to selected)
    } else {
        standardOptions
    }
    var customValue by remember(selected) {
        mutableStateOf(selected.takeUnless { current -> standardOptions.any { it.first == current } }.orEmpty())
    }
    val submitCustom = {
        customValue.trim().takeIf { it.isNotEmpty() && it != selected }?.let(onSelect)
        keyboardController?.hide()
        Unit
    }
    PreferenceChoiceCard(
        title = title,
        description = description,
        selected = selected,
        options = options,
        enabled = enabled,
        disabledDescription = disabledDescription,
        isTv = isTv,
        onSelect = onSelect,
        extraContent = {
            RivuneTextField(
                value = customValue,
                onValueChange = { customValue = it },
                modifier = Modifier.fillMaxWidth(),
                enabled = enabled,
                isTv = isTv,
                label = stringResource(R.string.viewer_language_custom),
                placeholder = stringResource(R.string.viewer_language_custom_hint),
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Done),
                keyboardActions = KeyboardActions(onDone = { submitCustom() }),
                trailingContent = {
                    RivuneTextButton(
                        label = stringResource(R.string.viewer_apply),
                        onClick = submitCustom,
                        enabled = enabled && customValue.isNotBlank() && customValue.trim() != selected,
                        isTv = isTv,
                    )
                },
            )
        },
    )
}

@Composable
private fun PreferenceChoiceCard(
    title: String,
    description: String,
    selected: String,
    options: List<Pair<String, String>>,
    enabled: Boolean,
    disabledDescription: String,
    isTv: Boolean,
    onSelect: (String) -> Unit,
    optionLeading: (@Composable (String) -> Unit)? = null,
    extraContent: (@Composable () -> Unit)? = null,
) {
    RivuneFunctionalSurface(
        modifier = Modifier.widthIn(max = ViewerPreferencesMaxWidth).fillMaxWidth(),
        contentPadding = PaddingValues(vertical = RivuneSpacing.xs),
        color = Color.Transparent,
    ) {
        Column {
            Column(
                modifier = Modifier.padding(
                    horizontal = if (isTv) RivuneSpacing.xl else RivuneSpacing.md,
                    vertical = RivuneSpacing.sm,
                ),
                verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xxs),
            ) {
                Text(
                    title,
                    style = if (isTv) MaterialTheme.typography.titleLarge else MaterialTheme.typography.titleMedium,
                )
                Text(
                    text = description,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    style = if (isTv) MaterialTheme.typography.bodyLarge else MaterialTheme.typography.bodyMedium,
                )
            }
            options.forEachIndexed { index, (value, label) ->
                val isSelected = selected == value
                if (index > 0) {
                    HorizontalDivider(
                        modifier = Modifier.padding(horizontal = if (isTv) RivuneSpacing.xl else RivuneSpacing.md),
                        color = MaterialTheme.colorScheme.outlineVariant,
                    )
                }
                RivuneFocusSurface(
                    onClick = { if (!isSelected) onSelect(value) },
                    enabled = enabled,
                    readOnly = isTv && !enabled,
                    selected = isSelected,
                    selectedColor = if (isTv) MaterialTheme.colorScheme.surfaceContainerHighest else Color.Transparent,
                    showSelectionBorder = isTv,
                    idleColor = Color.Transparent,
                    isTv = isTv,
                    shape = RivuneShapes.small,
                    modifier = Modifier
                        .fillMaxWidth()
                        .heightIn(min = if (isTv) ViewerTvTarget else ViewerPhoneTarget)
                        .semantics {
                            if (!enabled) stateDescription = disabledDescription
                        },
                ) {
                    Row(
                        modifier = Modifier
                            .fillMaxWidth()
                            .padding(
                                horizontal = if (isTv) RivuneSpacing.xl else RivuneSpacing.md,
                                vertical = RivuneSpacing.sm,
                            ),
                        horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        optionLeading?.invoke(value)
                        Text(
                            text = label,
                            modifier = Modifier.weight(1f),
                            color = if (isSelected) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurface,
                            style = MaterialTheme.typography.bodyLarge,
                        )
                        if (isSelected) {
                            Icon(
                                Icons.Rounded.Check,
                                contentDescription = null,
                                modifier = Modifier.size(RivuneDimensions.iconMedium),
                                tint = MaterialTheme.colorScheme.primary,
                            )
                        }
                    }
                }
            }
            extraContent?.let { content ->
                HorizontalDivider(
                    modifier = Modifier.padding(horizontal = if (isTv) RivuneSpacing.xl else RivuneSpacing.md),
                    color = MaterialTheme.colorScheme.outlineVariant,
                )
                Box(
                    modifier = Modifier.padding(
                        horizontal = if (isTv) RivuneSpacing.xl else RivuneSpacing.md,
                        vertical = RivuneSpacing.md,
                    ),
                ) {
                    content()
                }
            }
        }
    }
}

@Composable
private fun ViewerHeader(
    tab: ViewerTab,
    profileName: String?,
    profileAvatarModel: Any?,
    onAccount: () -> Unit,
) {
    val displayedName = profileName ?: stringResource(R.string.viewer_unknown_profile)
    val fallback = displayedName.trim().take(1).takeIf { it.isNotBlank() }
        ?.uppercase(Locale.getDefault()) ?: stringResource(R.string.viewer_profile_fallback)
    val accountDescription = stringResource(R.string.viewer_account_for, displayedName)
    BoxWithConstraints(modifier = Modifier.fillMaxWidth()) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .heightIn(min = ViewerPhoneTarget)
                .padding(horizontal = viewerHorizontalPadding(maxWidth, false)),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.sm),
        ) {
            Text(
                text = stringResource(tab.titleResource()),
                modifier = Modifier
                    .weight(1f)
                    .semantics { heading() },
                color = MaterialTheme.colorScheme.onBackground,
                style = MaterialTheme.typography.titleLarge,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            RivuneFocusSurface(
                onClick = onAccount,
                shape = CircleShape,
                idleColor = Color.Transparent,
                modifier = Modifier
                    .size(ViewerPhoneTarget)
                    .semantics { contentDescription = accountDescription },
            ) {
                Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    RivuneArtwork(
                        model = profileAvatarModel,
                        fallback = fallback,
                        contentDescription = null,
                        modifier = Modifier.size(RivuneSpacing.xxl).clip(CircleShape),
                    )
                }
            }
        }
    }
}

@Composable
private fun ViewerTabletDock(
    selected: ViewerTab,
    profileName: String?,
    profileAvatarModel: Any?,
    onSelect: (ViewerTab) -> Unit,
    onAccount: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val displayedName = profileName ?: stringResource(R.string.viewer_unknown_profile)
    val accountFallback = displayedName.trim().take(1).takeIf(String::isNotBlank)
        ?.uppercase(Locale.getDefault()) ?: stringResource(R.string.viewer_profile_fallback)
    val accountDescription = stringResource(R.string.viewer_account_for, displayedName)
    Surface(
        modifier = modifier,
        shape = RivuneShapes.extraLarge,
        color = MaterialTheme.colorScheme.surfaceContainer.copy(alpha = 0.84f),
        contentColor = MaterialTheme.colorScheme.onSurface,
        border = BorderStroke(
            RivuneDimensions.hairline,
            MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.70f),
        ),
        tonalElevation = RivuneElevation.flat,
        shadowElevation = RivuneElevation.overlay,
    ) {
        Row(
            modifier = Modifier
                .height(ViewerTvTarget)
                .padding(RivuneSpacing.xxs),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            ViewerTab.entries.forEach { tab ->
                ViewerDockIcon(
                    icon = tab.icon(),
                    label = stringResource(tab.titleResource()),
                    selected = tab == selected,
                    isTv = false,
                    onClick = { if (tab != selected) onSelect(tab) },
                )
            }
            Spacer(Modifier.width(RivuneSpacing.xs))
            Box(
                modifier = Modifier
                    .width(RivuneDimensions.hairline)
                    .height(RivuneSpacing.xxl)
                    .background(MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.70f)),
            )
            Spacer(Modifier.width(RivuneSpacing.xs))
            RivuneFocusSurface(
                onClick = onAccount,
                shape = CircleShape,
                idleColor = Color.Transparent,
                focusedColor = MaterialTheme.colorScheme.surfaceContainerHighest,
                modifier = Modifier
                    .size(ViewerPhoneTarget)
                    .semantics { contentDescription = accountDescription },
            ) {
                Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    RivuneArtwork(
                        model = profileAvatarModel,
                        fallback = accountFallback,
                        contentDescription = null,
                        modifier = Modifier.size(RivuneSpacing.xxl).clip(CircleShape),
                    )
                }
            }
        }
    }
}

@Composable
private fun ViewerTvDock(
    selected: ViewerTab,
    profileName: String?,
    profileAvatarModel: Any?,
    onSelect: (ViewerTab) -> Unit,
    onAccount: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val selectedFocus = remember { FocusRequester() }
    val displayedName = profileName ?: stringResource(R.string.viewer_unknown_profile)
    val accountFallback = displayedName.trim().take(1).takeIf(String::isNotBlank)
        ?.uppercase(Locale.getDefault()) ?: stringResource(R.string.viewer_profile_fallback)
    val accountDescription = stringResource(R.string.viewer_account_for, displayedName)
    LaunchedEffect(Unit) {
        selectedFocus.requestFocus()
    }
    Surface(
        modifier = modifier
            .width(ViewerTvDockCollapsedWidth)
            .focusGroup(),
        shape = RivuneShapes.extraLarge,
        color = MaterialTheme.colorScheme.surfaceContainer.copy(alpha = 0.92f),
        contentColor = MaterialTheme.colorScheme.onSurface,
        border = BorderStroke(
            RivuneDimensions.hairline,
            MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.70f),
        ),
        tonalElevation = RivuneElevation.flat,
        shadowElevation = RivuneElevation.overlay,
    ) {
        Column(
            modifier = Modifier.padding(RivuneSpacing.xs),
            verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xxs),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            ViewerTab.entries.forEach { tab ->
                ViewerDockIcon(
                    icon = tab.icon(),
                    label = stringResource(tab.titleResource()),
                    selected = tab == selected,
                    isTv = true,
                    modifier = if (tab == selected) Modifier.focusRequester(selectedFocus) else Modifier,
                    onClick = { if (tab != selected) onSelect(tab) },
                )
            }
            HorizontalDivider(
                modifier = Modifier.padding(vertical = RivuneSpacing.xs),
                color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.70f),
            )
            RivuneFocusSurface(
                onClick = onAccount,
                isTv = true,
                idleColor = Color.Transparent,
                focusedColor = MaterialTheme.colorScheme.surfaceContainerHighest,
                shape = CircleShape,
                modifier = Modifier
                    .size(ViewerTvTarget)
                    .semantics { contentDescription = accountDescription },
            ) {
                Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    RivuneArtwork(
                        model = profileAvatarModel,
                        fallback = accountFallback,
                        contentDescription = null,
                        modifier = Modifier.size(RivuneSpacing.xxxl).clip(CircleShape),
                    )
                }
            }
        }
    }
}

@Composable
private fun ViewerDockIcon(
    icon: ImageVector,
    label: String,
    selected: Boolean,
    isTv: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    RivuneFocusSurface(
        selected = selected,
        onClick = onClick,
        isTv = isTv,
        idleColor = Color.Transparent,
        selectedColor = MaterialTheme.colorScheme.primaryContainer.copy(alpha = 0.88f),
        focusedColor = if (selected) {
            MaterialTheme.colorScheme.primaryContainer
        } else {
            MaterialTheme.colorScheme.surfaceContainerHighest
        },
        showSelectionBorder = false,
        shape = RivuneShapes.medium,
        modifier = modifier
            .size(if (isTv) ViewerTvTarget else ViewerPhoneTarget)
            .semantics { contentDescription = label },
    ) {
        Box(
            modifier = Modifier.fillMaxSize().clearAndSetSemantics {},
            contentAlignment = Alignment.Center,
        ) {
            Icon(
                imageVector = icon,
                contentDescription = null,
                modifier = Modifier.size(RivuneDimensions.iconMedium),
                tint = if (selected) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}


@Composable
private fun ViewerBottomBar(
    selected: ViewerTab,
    onSelect: (ViewerTab) -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .fillMaxWidth()
            .background(MaterialTheme.colorScheme.background)
            .navigationBarsPadding(),
    ) {
        HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.42f))
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .height(RivuneDimensions.bottomBar)
                .padding(horizontal = RivuneSpacing.xs),
        ) {
            ViewerTab.entries.forEach { tab ->
                val label = stringResource(tab.titleResource())
                val isSelected = tab == selected
                RivuneFocusSurface(
                    selected = isSelected,
                    selectedColor = Color.Transparent,
                    onClick = { onSelect(tab) },
                    idleColor = Color.Transparent,
                    shape = RivuneShapes.small,
                    showSelectionBorder = false,
                    modifier = Modifier
                        .weight(1f)
                        .fillMaxHeight()
                        .semantics { contentDescription = label },
                ) {
                    Column(
                        modifier = Modifier
                            .fillMaxSize()
                            .clearAndSetSemantics {},
                        horizontalAlignment = Alignment.CenterHorizontally,
                        verticalArrangement = Arrangement.Center,
                    ) {
                        Icon(
                            tab.icon(),
                            contentDescription = null,
                            modifier = Modifier.size(RivuneDimensions.iconMedium),
                            tint = if (isSelected) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        Spacer(Modifier.height(2.dp))
                        Text(
                            text = label,
                            color = if (isSelected) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                            style = MaterialTheme.typography.labelSmall,
                        )
                        Spacer(Modifier.height(2.dp))
                        Box(
                            modifier = Modifier
                                .width(16.dp)
                                .height(2.dp)
                                .clip(RivuneShapes.pill)
                                .background(if (isSelected) MaterialTheme.colorScheme.primary else Color.Transparent),
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun HomeRoot(
    collections: List<Collection>,
    continueWatching: List<MediaTarget>,
    loading: ViewerLoading?,
    failure: UiFailure?,
    isTv: Boolean,
    artworkUrl: (String?) -> String?,
    onOpenFolder: (java.util.UUID, CollectionFolder) -> Unit,
    onOpenCollection: (java.util.UUID) -> Unit,
    onMedia: (MediaTarget) -> Unit,
    onRetry: () -> Unit,
) {
    val loadingLabel = stringResource(R.string.viewer_loading_home)
    val orderedCollections = collections.withIndex()
        .sortedWith(
            compareByDescending<IndexedValue<Collection>> { it.value.pinToTop }
                .thenBy { it.value.position }
                .thenBy { it.index },
        )
        .map { it.value }
    val hero = orderedCollections.asSequence()
        .filter(Collection::heroEnabled)
        .mapNotNull { collection ->
            collection.folders.firstOrNull { folder ->
                folder.id != null && (
                    !folder.heroBackdropUrl.isNullOrBlank() ||
                        !collection.backdropImageUrl.isNullOrBlank()
                    )
            }?.let { collection to it }
        }
        .firstOrNull()

    BoxWithConstraints(modifier = Modifier.fillMaxSize()) {
        val padding = viewerHorizontalPadding(maxWidth, isTv)
        val heroAspectRatio = if (isTv || maxWidth >= RivuneBreakpoints.expanded) 21f / 9f else 16f / 9f
        val heroHeight = minOf(
            (maxWidth - padding * 2) / heroAspectRatio,
            if (maxWidth >= RivuneBreakpoints.medium || isTv) 480.dp else 420.dp,
        )
    LazyColumn(
        modifier = Modifier.fillMaxSize(),
        contentPadding = PaddingValues(
            start = padding,
            end = padding,
            top = RivuneSpacing.xs,
            bottom = RivuneSpacing.xxxl,
        ),
        verticalArrangement = Arrangement.spacedBy(if (isTv) RivuneSpacing.xxxl else RivuneSpacing.xl),
    ) {
        inlineStatusItems(
            loading = loading == ViewerLoading.HOME,
            failure = failure,
            onRetry = onRetry,
            isTv = isTv,
            loadingLabel = loadingLabel,
        )
        hero?.let { (collection, folder) ->
            item(key = "hero:${collection.id}:${folder.id}") {
                val heroBackdrop = folder.heroBackdropUrl?.takeIf(String::isNotBlank)
                    ?: collection.backdropImageUrl
                RivuneFocusSurface(
                    onClick = { onOpenFolder(collection.id, folder) },
                    isTv = isTv,
                    shape = RivuneShapes.extraLarge,
                    modifier = Modifier
                        .fillMaxWidth()
                        .semantics { contentDescription = folder.title },
                ) {
                    Box(
                        modifier = Modifier
                            .fillMaxWidth()
                            .height(heroHeight)
                            .clearAndSetSemantics {},
                    ) {
                        RivuneArtwork(
                            model = artworkUrl(heroBackdrop),
                            fallback = folder.coverEmoji?.takeIf(String::isNotBlank) ?: folder.title,
                            contentDescription = null,
                            modifier = Modifier.fillMaxSize(),
                        )
                        Box(
                            modifier = Modifier
                                .fillMaxSize()
                                .background(
                                    Brush.verticalGradient(
                                        colors = listOf(
                                            MaterialTheme.colorScheme.background.copy(alpha = 0f),
                                            MaterialTheme.colorScheme.background.copy(alpha = ViewerHeroScrimAlpha),
                                        ),
                                    ),
                                ),
                        )
                        Column(
                            modifier = Modifier
                                .align(Alignment.BottomStart)
                                .fillMaxWidth()
                                .padding(if (isTv) RivuneSpacing.xxl else RivuneSpacing.md),
                            verticalArrangement = Arrangement.spacedBy(if (isTv) RivuneSpacing.sm else RivuneSpacing.xs),
                            horizontalAlignment = Alignment.Start,
                        ) {
                            Text(
                                text = collection.title,
                                color = MaterialTheme.colorScheme.primary,
                                style = MaterialTheme.typography.labelLarge,
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis,
                            )
                            if (!folder.titleLogoUrl.isNullOrBlank()) {
                                RivuneArtwork(
                                    model = artworkUrl(folder.titleLogoUrl),
                                    fallback = folder.title,
                                    contentDescription = null,
                                    contentScale = ContentScale.Fit,
                                    modifier = Modifier
                                        .fillMaxWidth(ViewerLogoWidthFraction)
                                        .height(if (isTv) RivuneDimensions.buttonHeightTv else RivuneDimensions.buttonHeight),
                                )
                            } else if (!folder.hideTitle) {
                                Text(
                                    text = folder.title,
                                    color = MaterialTheme.colorScheme.onBackground,
                                    style = if (isTv) MaterialTheme.typography.displayLarge else MaterialTheme.typography.headlineMedium,
                                    maxLines = 2,
                                    overflow = TextOverflow.Ellipsis,
                                )
                            }
                            Row(
                                horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
                                verticalAlignment = Alignment.CenterVertically,
                            ) {
                                Text(
                                    text = stringResource(R.string.home_folders),
                                    color = MaterialTheme.colorScheme.primary,
                                    style = MaterialTheme.typography.labelLarge,
                                )
                                Icon(
                                    Icons.Rounded.ChevronRight,
                                    contentDescription = null,
                                    modifier = Modifier.size(RivuneDimensions.iconMedium),
                                    tint = MaterialTheme.colorScheme.primary,
                                )
                            }
                        }
                    }
                }
            }
        }
        if (continueWatching.isNotEmpty()) {
            item(key = "continue-watching") {
                MediaRow(
                    title = stringResource(R.string.viewer_continue_watching),
                    items = continueWatching,
                    isTv = isTv,
                    artworkUrl = artworkUrl,
                    onMedia = onMedia,
                )
            }
        }
        if (loading == ViewerLoading.HOME && continueWatching.isEmpty() && collections.isEmpty()) {
            item(key = "home-skeleton") { MediaRowSkeleton(isTv = isTv) }
        }
        if (collections.isEmpty() && loading != ViewerLoading.HOME && failure == null) {
            item(key = "empty-collections") {
                InlineEmpty(
                    title = stringResource(R.string.home_empty_collections_title),
                    body = stringResource(R.string.home_empty_collections_body),
                )
            }
        }
        orderedCollections.forEach { collection ->
            val visibleFolders = collection.folders.filterNot { folder ->
                isTv && collection.id == hero?.first?.id && folder.id == hero?.second?.id
            }
            if (collection.folders.isNotEmpty() && visibleFolders.isEmpty()) return@forEach
            item(key = "collection:${collection.id}") {
                Column(verticalArrangement = Arrangement.spacedBy(RivuneSpacing.sm)) {
                    RivuneSectionHeading(
                        title = collection.title,
                        trailingAction = {
                            RivuneTextButton(
                                label = stringResource(R.string.viewer_view_all),
                                onClick = { onOpenCollection(collection.id) },
                                isTv = isTv,
                                icon = Icons.Rounded.ChevronRight,
                            )
                        },
                    )
                    if (visibleFolders.isEmpty()) {
                        InlineEmpty(
                            title = stringResource(R.string.home_empty_folders_title),
                            body = stringResource(R.string.home_empty_folders_body),
                        )
                    } else {
                        BoxWithConstraints(modifier = Modifier.fillMaxWidth()) {
                            val focusInset = if (isTv) RivuneSpacing.xxs else 0.dp
                            val availableWidth = maxWidth - focusInset
                            val landscapeCardWidth = viewerRowCardWidth(
                                width = availableWidth,
                                targetWidth = if (isTv) ViewerTvLandscapeTarget else RivuneDimensions.landscapeCardWidth,
                                phoneVisibleCards = 2f,
                                isTv = isTv,
                            )
                            val portraitCardWidth = viewerRowCardWidth(
                                width = availableWidth,
                                targetWidth = if (isTv) ViewerTvPosterTarget else RivuneDimensions.posterWidth,
                                phoneVisibleCards = 3.2f,
                                isTv = isTv,
                            )
                            LazyRow(
                                horizontalArrangement = Arrangement.spacedBy(ViewerCardGap),
                                contentPadding = PaddingValues(
                                    start = focusInset,
                                    top = focusInset,
                                    end = padding,
                                    bottom = focusInset,
                                ),
                            ) {
                                items(
                                    items = visibleFolders,
                                    key = { folder -> folder.id ?: "${collection.id}:${folder.title}" },
                                ) { folder ->
                                    val tileShape = if (collection.viewMode == CollectionViewMode.FOLLOW_LAYOUT) {
                                        folder.tileShape
                                    } else {
                                        collection.folderCoverShape
                                    }
                                    val cardWidth = if (tileShape == CollectionTileShape.LANDSCAPE) {
                                        landscapeCardWidth
                                    } else {
                                        portraitCardWidth
                                    }
                                    FolderTile(
                                        folder = folder,
                                        imageUrl = artworkUrl(folder.coverImageUrl),
                                        isTv = isTv,
                                        enabled = folder.id != null,
                                        tileShape = tileShape,
                                        modifier = Modifier.width(cardWidth),
                                        onClick = { onOpenFolder(collection.id, folder) },
                                    )
                                }
                            }
                        }
                    }
                }
            }
        }
    }
    }
}

@Composable
private fun CollectionRoot(
    collection: Collection,
    isTv: Boolean,
    artworkUrl: (String?) -> String?,
    onBack: () -> Unit,
    onOpenFolder: (java.util.UUID, CollectionFolder) -> Unit,
) {
    val backFocus = remember { FocusRequester() }
    LaunchedEffect(isTv, collection.id) {
        if (isTv) backFocus.requestFocus()
    }
    RivuneCinematicBackground {
        Column(
            modifier = Modifier
                .fillMaxSize()
                .statusBarsPadding()
                .navigationBarsPadding(),
        ) {
            ScreenToolbar(
                title = collection.title,
                onBack = onBack,
                isTv = isTv,
                backModifier = Modifier.focusRequester(backFocus),
            )
            if (collection.folders.isEmpty()) {
                BoxWithConstraints(modifier = Modifier.fillMaxWidth()) {
                    InlineEmpty(
                        title = stringResource(R.string.home_empty_folders_title),
                        body = stringResource(R.string.home_empty_folders_body),
                        modifier = Modifier.padding(horizontal = viewerHorizontalPadding(maxWidth, isTv)),
                    )
                }
            } else {
                BoxWithConstraints(modifier = Modifier.weight(1f).fillMaxWidth()) {
                    val padding = viewerHorizontalPadding(maxWidth, isTv)
                    val landscapeCollection = collection.folderCoverShape == CollectionTileShape.LANDSCAPE
                    val contentWidth = (maxWidth - padding * 2).coerceAtMost(
                        if (isTv) RivuneDimensions.contentMaxTv else RivuneDimensions.contentMaxWide,
                    )
                    val columns = viewerGridCells(contentWidth, landscapeCollection, isTv)
                    LazyVerticalGrid(
                        columns = columns,
                        modifier = Modifier
                            .width(contentWidth)
                            .fillMaxHeight()
                            .align(Alignment.Center),
                        contentPadding = PaddingValues(
                            top = RivuneSpacing.md,
                            bottom = RivuneSpacing.xxxl,
                        ),
                        horizontalArrangement = Arrangement.spacedBy(ViewerCardGap),
                        verticalArrangement = Arrangement.spacedBy(if (isTv) RivuneSpacing.xl else RivuneSpacing.md),
                    ) {
                        items(
                            items = collection.folders,
                            key = { folder -> folder.id ?: "${collection.id}:${folder.title}" },
                        ) { folder ->
                            val tileShape = if (collection.viewMode == CollectionViewMode.FOLLOW_LAYOUT) {
                                folder.tileShape
                            } else {
                                collection.folderCoverShape
                            }
                            FolderTile(
                                folder = folder,
                                imageUrl = artworkUrl(folder.coverImageUrl),
                                isTv = isTv,
                                enabled = folder.id != null,
                                tileShape = tileShape,
                                modifier = Modifier.fillMaxWidth(),
                                onClick = { onOpenFolder(collection.id, folder) },
                            )
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun FolderRoot(
    folder: ResolvedCollectionFolder,
    collectionTitle: String?,
    loading: ViewerLoading?,
    failure: UiFailure?,
    isTv: Boolean,
    artworkUrl: (String?) -> String?,
    onBack: () -> Unit,
    onItem: (CollectionItem) -> Unit,
    onLoadMore: () -> Unit,
    onRetry: () -> Unit,
) {
    val sources = remember(folder.folder.sources) { folder.folder.sources.filter { it.id != null } }
    val sourceView = if (sources.size > 1) {
        folder.folder.sourceView ?: CollectionSourceView.MERGED
    } else {
        CollectionSourceView.MERGED
    }
    var activeSourceId by remember(folder.folder.id, sourceView, sources) {
        mutableStateOf(if (sourceView == CollectionSourceView.CATEGORIES) sources.firstOrNull()?.id else null)
    }
    var mediaFilter by remember(folder.folder.id, activeSourceId) { mutableStateOf<String?>(null) }
    val activeSource = sources.firstOrNull { it.id == activeSourceId }
    val itemsBySource = remember(folder.items, sources) {
        sources.associate { source ->
            source.id to folder.items.filter { item -> item.sources.any { reference -> reference.id == source.id } }
        }
    }
    val browsingSourceFolders = sourceView == CollectionSourceView.FOLDERS && activeSource == null
    val scopedItems = activeSource?.let { itemsBySource[it.id].orEmpty() } ?: folder.items
    val filterSources = activeSource?.let(::listOf) ?: sources
    val supportsMediaFilter = filterSources.any { it.tmdb?.mediaType == CollectionTMDBMediaType.BOTH }
    val visibleItems = mediaFilter?.let { selected -> scopedItems.filter { it.mediaType == selected } } ?: scopedItems
    val landscape = folder.folder.tileShape == CollectionTileShape.LANDSCAPE
    val backFocus = remember { FocusRequester() }
    val sourceGridFocus = remember { FocusRequester() }
    var lastOpenedSourceId by remember(folder.folder.id) { mutableStateOf<java.util.UUID?>(null) }
    val closeSourceOrFolder = {
        if (activeSource != null && sourceView == CollectionSourceView.FOLDERS) {
            activeSourceId = null
        } else {
            onBack()
        }
    }
    BackHandler(enabled = activeSource != null && sourceView == CollectionSourceView.FOLDERS) {
        activeSourceId = null
    }
    LaunchedEffect(isTv, folder.folder.id) {
        if (isTv) backFocus.requestFocus()
    }
    LaunchedEffect(isTv, browsingSourceFolders, lastOpenedSourceId) {
        if (isTv && browsingSourceFolders && lastOpenedSourceId != null) sourceGridFocus.requestFocus()
    }
    RivuneCinematicBackground {
        Column(
            modifier = Modifier
                .fillMaxSize()
                .statusBarsPadding()
                .navigationBarsPadding(),
        ) {
            ScreenToolbar(
                title = collectionTitle ?: stringResource(R.string.home_folders),
                onBack = closeSourceOrFolder,
                isTv = isTv,
                backModifier = Modifier.focusRequester(backFocus),
            )
            BoxWithConstraints(modifier = Modifier.fillMaxWidth()) {
                Column(
                    modifier = Modifier.padding(horizontal = viewerHorizontalPadding(maxWidth, isTv)),
                    verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
                ) {
                InlineStatus(
                    loading = loading == ViewerLoading.FOLDER && folder.items.isEmpty(),
                    failure = failure,
                    onRetry = onRetry,
                    isTv = isTv,
                    loadingLabel = stringResource(R.string.viewer_loading_folder),
                )
                if (folder.errors.isNotEmpty()) {
                    InlineWarning(stringResource(R.string.folder_partial_warning))
                }
                if (sourceView == CollectionSourceView.CATEGORIES) {
                    LazyRow(horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.xs)) {
                        items(sources, key = { source -> source.id.toString() }) { source ->
                            FolderFilterButton(
                                label = collectionSourceLabel(source),
                                icon = collectionSourceIcon(source),
                                selected = source.id == activeSourceId,
                                isTv = isTv,
                                onClick = { activeSourceId = source.id },
                            )
                        }
                    }
                }
                if (supportsMediaFilter && !browsingSourceFolders) {
                    LazyRow(horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.xs)) {
                        item {
                            FolderFilterButton(
                                label = stringResource(R.string.viewer_library_all),
                                selected = mediaFilter == null,
                                isTv = isTv,
                                icon = Icons.Rounded.VideoLibrary,
                                onClick = { mediaFilter = null },
                            )
                        }
                        item {
                            FolderFilterButton(
                                label = stringResource(R.string.viewer_movies),
                                selected = mediaFilter == "movie",
                                isTv = isTv,
                                icon = Icons.Rounded.MovieCreation,
                                onClick = { mediaFilter = "movie" },
                            )
                        }
                        item {
                            FolderFilterButton(
                                label = stringResource(R.string.viewer_series),
                                selected = mediaFilter == "series",
                                isTv = isTv,
                                icon = Icons.Rounded.Tv,
                                onClick = { mediaFilter = "series" },
                            )
                        }
                    }
                }
                }
            }
            when {
                browsingSourceFolders -> BoxWithConstraints(modifier = Modifier.weight(1f).fillMaxWidth()) {
                    val padding = viewerHorizontalPadding(maxWidth, isTv)
                    val contentWidth = (maxWidth - padding * 2).coerceAtMost(
                        if (isTv) RivuneDimensions.contentMaxTv else RivuneDimensions.contentMaxWide,
                    )
                    val columns = viewerGridCells(contentWidth, landscape, isTv)
                    LazyVerticalGrid(
                        columns = columns,
                        modifier = Modifier
                            .width(contentWidth)
                            .fillMaxHeight()
                            .align(Alignment.Center),
                        contentPadding = PaddingValues(
                            top = RivuneSpacing.md,
                            bottom = RivuneSpacing.xxxl,
                        ),
                        horizontalArrangement = Arrangement.spacedBy(ViewerCardGap),
                        verticalArrangement = Arrangement.spacedBy(if (isTv) RivuneSpacing.xl else RivuneSpacing.md),
                    ) {
                        items(sources, key = { source -> source.id.toString() }) { source ->
                            val sourceItems = itemsBySource[source.id].orEmpty()
                            val sourceArtwork = folder.sourcePosterUrls?.get(source.id.toString())
                                ?: sourceItems.firstNotNullOfOrNull { item -> item.posterUrl ?: item.backgroundUrl }
                            FolderTile(
                                folder = folder.folder.copy(
                                    title = collectionSourceLabel(source),
                                    coverImageUrl = sourceArtwork,
                                    coverEmoji = null,
                                    titleLogoUrl = null,
                                    hideTitle = false,
                                    sources = listOf(source),
                                ),
                                imageUrl = artworkUrl(sourceArtwork),
                                isTv = isTv,
                                enabled = true,
                                tileShape = folder.folder.tileShape,
                                modifier = Modifier
                                    .fillMaxWidth()
                                    .then(if (source.id == lastOpenedSourceId) Modifier.focusRequester(sourceGridFocus) else Modifier),
                                onClick = {
                                    lastOpenedSourceId = source.id
                                    activeSourceId = source.id
                                },
                            )
                        }
                        if (folder.hasMore) {
                            item(span = { androidx.compose.foundation.lazy.grid.GridItemSpan(maxLineSpan) }) {
                                LoadMoreButton(
                                    loading = loading == ViewerLoading.FOLDER,
                                    isTv = isTv,
                                    onClick = onLoadMore,
                                )
                            }
                        }
                    }
                }
                visibleItems.isNotEmpty() -> BoxWithConstraints(modifier = Modifier.weight(1f).fillMaxWidth()) {
                    val padding = viewerHorizontalPadding(maxWidth, isTv)
                    val contentWidth = (maxWidth - padding * 2).coerceAtMost(
                        if (isTv) RivuneDimensions.contentMaxTv else RivuneDimensions.contentMaxWide,
                    )
                    val columns = viewerGridCells(contentWidth, landscape, isTv)
                    LazyVerticalGrid(
                        columns = columns,
                        modifier = Modifier
                            .width(contentWidth)
                            .fillMaxHeight()
                            .align(Alignment.Center),
                        contentPadding = PaddingValues(
                            top = RivuneSpacing.md,
                            bottom = RivuneSpacing.xxxl,
                        ),
                        horizontalArrangement = Arrangement.spacedBy(ViewerCardGap),
                        verticalArrangement = Arrangement.spacedBy(if (isTv) RivuneSpacing.xl else RivuneSpacing.md),
                    ) {
                        items(visibleItems, key = { "${it.mediaType}:${it.id}" }) { item ->
                            val itemLandscape = landscape || item.mediaType == "tv"
                            MediaTile(
                                target = item.toMediaTarget(),
                                imageUrl = artworkUrl(
                                    if (itemLandscape) item.backgroundUrl ?: item.posterUrl
                                    else item.posterUrl ?: item.backgroundUrl,
                                ),
                                isTv = isTv,
                                landscape = itemLandscape,
                                onClick = { onItem(item) },
                            )
                        }
                        if (folder.hasMore) {
                            item(span = { androidx.compose.foundation.lazy.grid.GridItemSpan(maxLineSpan) }) {
                                LoadMoreButton(
                                    loading = loading == ViewerLoading.FOLDER,
                                    isTv = isTv,
                                    onClick = onLoadMore,
                                )
                            }
                        }
                    }
                }
                loading != ViewerLoading.FOLDER && failure == null -> BoxWithConstraints(
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    InlineEmpty(
                        title = stringResource(R.string.folder_empty_title),
                        body = stringResource(R.string.folder_empty_body),
                        modifier = Modifier.padding(horizontal = viewerHorizontalPadding(maxWidth, isTv)),
                    )
                }
            }
        }
    }
}

@Composable
private fun collectionSourceLabel(source: CollectionSource): String {
    val mediaType = source.tmdb?.mediaType?.name
        ?: source.trakt?.mediaType?.name
        ?: source.mdblist?.mediaType?.name
        ?: source.addonCatalog?.type
    val normalizedMediaType = mediaType?.trim()?.lowercase(Locale.ROOT)
    val normalizedTitle = source.title.trim().lowercase(Locale.ROOT)
    return when {
        normalizedMediaType == "movie" && normalizedTitle in CanonicalMovieCategoryTitles ->
            stringResource(R.string.viewer_movies)
        (normalizedMediaType == "series" || normalizedMediaType == "tv") &&
            normalizedTitle in CanonicalSeriesCategoryTitles -> stringResource(R.string.viewer_series)
        else -> source.title
    }
}
private fun collectionSourceIcon(source: CollectionSource): ImageVector? {
    val mediaType = source.tmdb?.mediaType?.name
        ?: source.trakt?.mediaType?.name
        ?: source.mdblist?.mediaType?.name
        ?: source.addonCatalog?.type
    val normalizedMediaType = mediaType?.trim()?.lowercase(Locale.ROOT)
    val normalizedTitle = source.title.trim().lowercase(Locale.ROOT)
    return when {
        normalizedMediaType == "movie" || normalizedTitle in CanonicalMovieCategoryTitles -> Icons.Rounded.MovieCreation
        normalizedMediaType == "series" || normalizedMediaType == "tv" ||
            normalizedTitle in CanonicalSeriesCategoryTitles -> Icons.Rounded.Tv
        else -> null
    }
}


@Composable
private fun FolderFilterButton(
    label: String,
    selected: Boolean,
    isTv: Boolean,
    icon: ImageVector? = null,
    onClick: () -> Unit,
) {
    var focused by remember { mutableStateOf(false) }
    val contentColor = if (selected || focused) {
        MaterialTheme.colorScheme.primary
    } else {
        MaterialTheme.colorScheme.onSurfaceVariant
    }
    RivuneFocusSurface(
        onClick = onClick,
        selected = selected,
        isTv = isTv,
        shape = RivuneShapes.small,
        selectedColor = Color.Transparent,
        idleColor = Color.Transparent,
        showSelectionBorder = false,
        showFocusBorder = false,
        focusedColor = Color.Transparent,
        modifier = Modifier.onFocusChanged { focused = it.isFocused },
    ) {
        Column(
            modifier = Modifier.padding(
                horizontal = if (isTv) RivuneSpacing.xs else RivuneSpacing.md,
                vertical = if (isTv) RivuneSpacing.xxs else RivuneSpacing.xs,
            ),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xxs),
        ) {
            Row(
                horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                icon?.let {
                    Icon(
                        imageVector = it,
                        contentDescription = null,
                        modifier = Modifier.size(RivuneDimensions.iconMedium),
                        tint = contentColor,
                    )
                }
                Text(
                    text = label,
                    color = contentColor,
                    style = if (isTv) MaterialTheme.typography.titleMedium else MaterialTheme.typography.labelLarge,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            Box(
                modifier = Modifier
                    .width(24.dp)
                    .height(2.dp)
                    .clip(RivuneShapes.pill)
                    .background(if (selected) MaterialTheme.colorScheme.primary else Color.Transparent),
            )
        }
    }
}

@Composable
private fun SearchRoot(
    state: SearchState,
    loading: ViewerLoading?,
    failure: UiFailure?,
    isTv: Boolean,
    artworkUrl: (String?) -> String?,
    onSearch: (String) -> Unit,
    onLoadMore: () -> Unit,
    onMedia: (MediaTarget) -> Unit,
    onRetry: () -> Unit,
) {
    var query by remember(state.query) { mutableStateOf(state.query) }
    val trimmed = query.trim()
    val keyboardController = LocalSoftwareKeyboardController.current
    val firstResultFocus = remember { FocusRequester() }
    val submit = {
        if (trimmed.length >= 2) {
            onSearch(trimmed)
            keyboardController?.hide()
        }
    }
    LaunchedEffect(isTv, state.query, state.items.firstOrNull()?.id) {
        if (isTv && state.items.isNotEmpty()) firstResultFocus.requestFocus()
    }
    RivuneCinematicBackground {
        BoxWithConstraints(modifier = Modifier.fillMaxSize()) {
            val padding = viewerHorizontalPadding(maxWidth, isTv)
            val contentWidth = (maxWidth - padding * 2).coerceAtMost(
                if (isTv) RivuneDimensions.contentMaxTv else RivuneDimensions.contentMaxTablet,
            )
            Column(
                modifier = Modifier
                    .width(contentWidth)
                    .fillMaxHeight()
                    .align(Alignment.Center)
                    .padding(top = RivuneSpacing.md)
                    .imePadding(),
            ) {
            Box(modifier = Modifier.fillMaxWidth(), contentAlignment = Alignment.TopCenter) {
                RivuneTextField(
                    value = query,
                    onValueChange = { query = it },
                    modifier = Modifier
                        .widthIn(max = RivuneDimensions.contentMaxTablet)
                        .fillMaxWidth(),
                    label = stringResource(R.string.viewer_search_label),
                    placeholder = stringResource(R.string.viewer_search_hint),
                    leadingIcon = Icons.Rounded.Search,
                    isTv = isTv,
                    trailingContent = {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            if (query.isNotEmpty()) {
                                val clearDescription = stringResource(R.string.viewer_search_clear)
                                ViewerIconAction(
                                    icon = Icons.Rounded.Close,
                                    label = clearDescription,
                                    onClick = {
                                        query = ""
                                        onSearch("")
                                    },
                                    isTv = isTv,
                                    modifier = Modifier.size(if (isTv) ViewerTvTarget else ViewerPhoneTarget),
                                )
                            }
                            val submitDescription = stringResource(R.string.viewer_search_submit)
                            ViewerIconAction(
                                icon = Icons.Rounded.Search,
                                label = submitDescription,
                                onClick = submit,
                                isTv = isTv,
                                enabled = trimmed.length >= 2 && loading != ViewerLoading.SEARCH,
                                modifier = Modifier.size(if (isTv) ViewerTvTarget else ViewerPhoneTarget),
                            )
                        }
                    },
                    keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
                    keyboardActions = KeyboardActions(onSearch = { submit() }),
                )
            }
            Spacer(Modifier.height(RivuneSpacing.md))
            InlineStatus(
                loading = loading == ViewerLoading.SEARCH,
                failure = failure,
                onRetry = onRetry,
                isTv = isTv,
                loadingLabel = stringResource(R.string.viewer_loading_search),
            )
            if (state.partial) {
                InlineWarning(stringResource(R.string.viewer_partial_results))
                Spacer(Modifier.height(RivuneSpacing.md))
            }
            if (state.items.isNotEmpty()) {
                Text(
                    text = pluralStringResource(R.plurals.viewer_result_count, state.items.size, state.items.size),
                    modifier = Modifier.padding(bottom = RivuneSpacing.sm),
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    style = MaterialTheme.typography.bodyMedium,
                )
            }
            BoxWithConstraints(modifier = Modifier.weight(1f).fillMaxWidth()) {
                val columns = viewerGridCells(maxWidth, landscape = false, isTv = isTv)
                when {
                    state.items.isNotEmpty() -> LazyVerticalGrid(
                        columns = columns,
                        modifier = Modifier.fillMaxSize(),
                        contentPadding = PaddingValues(bottom = RivuneSpacing.xxxl),
                        horizontalArrangement = Arrangement.spacedBy(ViewerCardGap),
                        verticalArrangement = Arrangement.spacedBy(if (isTv) RivuneSpacing.xl else RivuneSpacing.md),
                    ) {
                        items(state.items, key = { "${it.mediaType}:${it.id}" }) { item ->
                            MediaTile(
                                target = item,
                                imageUrl = artworkUrl(item.posterUrl ?: item.backgroundUrl),
                                isTv = isTv,
                                modifier = if (item == state.items.first()) {
                                    Modifier.focusRequester(firstResultFocus)
                                } else {
                                    Modifier
                                },
                                onClick = { onMedia(item) },
                            )
                        }
                        if (state.hasMore) {
                            item(span = { androidx.compose.foundation.lazy.grid.GridItemSpan(maxLineSpan) }) {
                                LoadMoreButton(
                                    loading = loading == ViewerLoading.SEARCH_MORE,
                                    isTv = isTv,
                                    onClick = onLoadMore,
                                )
                            }
                        }
                    }
                    loading != ViewerLoading.SEARCH && failure == null -> InlineEmpty(
                        title = stringResource(
                            if (state.query.length >= 2) R.string.viewer_search_empty_title else R.string.viewer_search_start_title,
                        ),
                        body = stringResource(
                            if (state.query.length >= 2) R.string.viewer_search_empty_body else R.string.viewer_search_start_body,
                        ),
                    )
                }
            }
        }
    }
        }
}
@Composable
private fun LibraryRoot(
    state: LibraryState,
    loading: ViewerLoading?,
    failure: UiFailure?,
    isTv: Boolean,
    artworkUrl: (String?) -> String?,
    onType: (String?) -> Unit,
    onItem: (LibraryItem) -> Unit,
    onLoadMore: () -> Unit,
    onRetry: () -> Unit,
) {
    RivuneCinematicBackground {
        BoxWithConstraints(modifier = Modifier.fillMaxSize()) {
            val padding = viewerHorizontalPadding(maxWidth, isTv)
            val compact = !isTv && maxWidth < RivuneBreakpoints.medium
            val contentWidth = (maxWidth - padding * 2).coerceAtMost(
                if (isTv) RivuneDimensions.contentMaxTv else RivuneDimensions.contentMaxWide,
            )
            Column(
                modifier = Modifier
                    .width(contentWidth)
                    .fillMaxHeight()
                    .align(Alignment.Center),
            ) {
            val libraryFilters = buildList<Triple<String?, String, ImageVector>> {
                add(Triple(null, stringResource(R.string.viewer_library_all), Icons.Rounded.Bookmark))
                if ("movie" in state.availableTypes) {
                    add(Triple("movie", stringResource(R.string.viewer_movies), Icons.Rounded.Movie))
                }
                if ("series" in state.availableTypes) {
                    add(Triple("series", stringResource(R.string.viewer_series), Icons.Rounded.Tv))
                }
                if ("tv" in state.availableTypes) {
                    add(Triple("tv", stringResource(R.string.viewer_live_tv), Icons.Rounded.LiveTv))
                }
            }
            if (compact) {
                LazyRow(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
                ) {
                    items(libraryFilters, key = { (type, _, _) -> type ?: "all" }) { (type, label, icon) ->
                        FolderFilterButton(
                            label = label,
                            icon = icon,
                            selected = state.mediaType == type,
                            isTv = false,
                            onClick = { onType(type) },
                        )
                    }
                }
                HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.42f))
            } else {
                Box(modifier = Modifier.fillMaxWidth(), contentAlignment = Alignment.TopCenter) {
                    RivuneFunctionalSurface(
                        modifier = Modifier
                            .widthIn(max = RivuneDimensions.contentMaxTablet)
                            .fillMaxWidth(),
                        shape = RivuneShapes.pill,
                        contentPadding = PaddingValues(RivuneSpacing.xxs),
                    ) {
                        Row(modifier = Modifier.fillMaxWidth().heightIn(min = if (isTv) ViewerTvTarget else ViewerPhoneTarget)) {
                            libraryFilters.forEach { (type, label, icon) ->
                                RivuneFocusSurface(
                                    onClick = { onType(type) },
                                    selected = state.mediaType == type,
                                    isTv = isTv,
                                    shape = RivuneShapes.pill,
                                    modifier = Modifier.weight(1f).heightIn(min = if (isTv) ViewerTvTarget else ViewerPhoneTarget),
                                ) {
                                    Box(
                                        modifier = Modifier.fillMaxWidth().padding(vertical = RivuneSpacing.sm),
                                        contentAlignment = Alignment.Center,
                                    ) {
                                        LibraryFilterLabel(
                                            label = label,
                                            icon = icon,
                                            selected = state.mediaType == type,
                                        )
                                    }
                                }
                            }
                        }
                    }
                }
            }
            Spacer(Modifier.height(RivuneSpacing.md))
            InlineStatus(
                loading = loading == ViewerLoading.LIBRARY,
                failure = failure,
                onRetry = onRetry,
                isTv = isTv,
                loadingLabel = stringResource(R.string.viewer_loading_library),
            )
            BoxWithConstraints(modifier = Modifier.weight(1f).fillMaxWidth()) {
                val gridWidth = if (isTv) {
                    maxWidth
                } else {
                    val visibleColumns = state.items.size.coerceIn(1, 6)
                    minOf(
                        maxWidth,
                        RivuneDimensions.posterWidth * visibleColumns + ViewerCardGap * (visibleColumns - 1),
                    )
                }
                val columns = viewerGridCells(gridWidth, landscape = false, isTv = isTv)
                when {
                    state.items.isNotEmpty() -> LazyVerticalGrid(
                        columns = columns,
                        modifier = Modifier.width(gridWidth).fillMaxHeight().align(Alignment.TopCenter),
                        contentPadding = PaddingValues(bottom = RivuneSpacing.xxxl),
                        horizontalArrangement = Arrangement.spacedBy(ViewerCardGap),
                        verticalArrangement = Arrangement.spacedBy(if (isTv) RivuneSpacing.xl else RivuneSpacing.md),
                    ) {
                        items(state.items, key = { it.titleId }) { item ->
                            MediaTile(
                                target = item.toMediaTarget(stringResource(R.string.viewer_untitled)),
                                imageUrl = artworkUrl(item.posterUrl ?: item.backgroundUrl),
                                isTv = isTv,
                                onClick = { onItem(item) },
                            )
                        }
                        if (state.page < state.totalPages) {
                            item(span = { androidx.compose.foundation.lazy.grid.GridItemSpan(maxLineSpan) }) {
                                LoadMoreButton(
                                    loading = loading == ViewerLoading.LIBRARY_MORE,
                                    isTv = isTv,
                                    onClick = onLoadMore,
                                )
                            }
                        }
                    }
                    loading != ViewerLoading.LIBRARY && failure == null -> InlineEmpty(
                        title = stringResource(R.string.viewer_library_empty_title),
                        body = stringResource(R.string.viewer_library_empty_body),
                    )
                }
            }
        }
    }
        }
}

@Composable
private fun LibraryFilterLabel(label: String, icon: ImageVector, selected: Boolean) {
    val contentColor = if (selected) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant
    Row(
        horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.xxs),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(
            imageVector = icon,
            contentDescription = null,
            modifier = Modifier.size(RivuneDimensions.iconMedium),
            tint = contentColor,
        )
        Text(
            text = label,
            color = contentColor,
            style = MaterialTheme.typography.labelLarge,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}
@Composable
private fun CalendarRoot(
    events: List<CalendarEvent>,
    month: YearMonth,
    loading: ViewerLoading?,
    failure: UiFailure?,
    isTv: Boolean,
    artworkUrl: (String?) -> String?,
    onPreviousMonth: () -> Unit,
    onNextMonth: () -> Unit,
    onEvent: (CalendarEvent) -> Unit,
    onRetry: () -> Unit,
) {
    val locale = Locale.getDefault()
    val monthLabel = remember(month, locale) {
        month.atDay(1).format(DateTimeFormatter.ofPattern("MMMM yyyy", locale))
            .replaceFirstChar { if (it.isLowerCase()) it.titlecase(locale) else it.toString() }
    }
    RivuneCinematicBackground {
        BoxWithConstraints(modifier = Modifier.fillMaxSize()) {
            val padding = viewerHorizontalPadding(maxWidth, isTv)
            val eventArtworkHeight = if (isTv || maxWidth >= RivuneBreakpoints.medium) 88.dp else 64.dp
            Column(
                modifier = Modifier
                    .align(Alignment.TopCenter)
                    .fillMaxHeight()
                    .widthIn(max = if (isTv) RivuneDimensions.contentMaxWide else ViewerPreferencesMaxWidth)
                    .fillMaxWidth()
                    .padding(horizontal = padding)
                    .padding(top = RivuneSpacing.lg),
            ) {
                Surface(
                    modifier = Modifier.fillMaxWidth(),
                    shape = if (isTv) RivuneShapes.large else RivuneShapes.medium,
                    color = Color.Transparent,
                    contentColor = MaterialTheme.colorScheme.onBackground,
                    border = BorderStroke(
                        RivuneDimensions.hairline,
                        MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.72f),
                    ),
                ) {
                    Row(
                        modifier = Modifier.fillMaxWidth().padding(horizontal = RivuneSpacing.xs, vertical = RivuneSpacing.xxs),
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
                    ) {
                        CalendarMonthButton(
                            icon = Icons.Rounded.ChevronLeft,
                            label = stringResource(R.string.viewer_calendar_previous),
                            isTv = isTv,
                            onClick = onPreviousMonth,
                        )
                        Text(
                            text = monthLabel,
                            modifier = Modifier.weight(1f).semantics { heading() },
                            textAlign = TextAlign.Center,
                            style = if (isTv) MaterialTheme.typography.headlineLarge else MaterialTheme.typography.titleLarge,
                            maxLines = 2,
                        )
                        CalendarMonthButton(
                            icon = Icons.Rounded.ChevronRight,
                            label = stringResource(R.string.viewer_calendar_next),
                            isTv = isTv,
                            onClick = onNextMonth,
                        )
                    }
                }
                Spacer(Modifier.height(RivuneSpacing.md))
                InlineStatus(
                    loading = loading == ViewerLoading.CALENDAR,
                    failure = failure,
                    onRetry = onRetry,
                    isTv = isTv,
                    loadingLabel = stringResource(R.string.viewer_loading_calendar),
                )
                when {
                    events.isNotEmpty() -> LazyColumn(
                        modifier = Modifier.weight(1f),
                        contentPadding = PaddingValues(bottom = RivuneSpacing.xxxl),
                        verticalArrangement = Arrangement.spacedBy(RivuneSpacing.sm),
                    ) {
                        events.groupBy { it.releaseDate }.forEach { (releaseDate, dayEvents) ->
                            item(key = "date:$releaseDate") {
                                RivuneSectionHeading(
                                    title = localizedDate(releaseDate, locale),
                                    modifier = Modifier.padding(top = RivuneSpacing.xs),
                                )
                            }
                            items(dayEvents, key = { it.id }) { event ->
                                CalendarEventRow(
                                    event = event,
                                    imageUrl = artworkUrl(event.posterUrl),
                                    artworkHeight = eventArtworkHeight,
                                    isTv = isTv,
                                    onClick = { onEvent(event) },
                                )
                            }
                        }
                    }
                    loading != ViewerLoading.CALENDAR && failure == null -> InlineEmpty(
                        title = stringResource(R.string.viewer_calendar_empty_title),
                        body = stringResource(R.string.viewer_calendar_empty_body),
                    )
                }
            }
        }
    }
}


@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun DetailScreen(
    state: ViewerState,
    isTv: Boolean,
    artworkUrl: (String?) -> String?,
    onOpenExternalUrl: (String) -> Unit,
    onBack: () -> Unit,
    onSeason: (String) -> Unit,
    onEpisode: (MediaTarget) -> Unit,
    onPlay: () -> Unit,
    onToggleLibrary: () -> Unit,
    onToggleWatched: () -> Unit,
    externalPlayers: List<ExternalPlayerApp>,
    onSelectSource: (PlaybackSourceOption) -> Unit,
    onChooseTarget: (ExternalPlayerApp?) -> Unit,
    onDismissTarget: () -> Unit,
    onDismissSources: () -> Unit,
    onRetry: () -> Unit,
) {
    val detail = checkNotNull(state.detail)
    val movie = detail.movie
    val series = detail.series
    val season = detail.season
    val title = movie?.title ?: season?.name ?: series?.name ?: detail.target.title
    val overview = movie?.overview ?: season?.overview ?: series?.overview ?: detail.target.description
    val backdrop = artworkUrl(
        movie?.backdropUrl ?: season?.backdropUrl ?: series?.backdropUrl ?: detail.target.backgroundUrl ?: detail.target.posterUrl,
    )
    val cast = movie?.cast ?: series?.cast.orEmpty()
    val orderedSeasons = remember(series?.seasons) { series?.seasons.orEmpty().sortedBy { it.seasonNumber } }
    val trailer = if (season == null) detail.trailers.firstOrNull()
    else detail.seasonTrailers.firstOrNull() ?: detail.trailers.firstOrNull()
    val trailerUrl = trailer?.youtubeId?.takeIf(String::isNotBlank)?.let { youtubeId ->
        "https://www.youtube.com/watch?v=${android.net.Uri.encode(youtubeId)}"
    }
    val onTrailer = trailerUrl?.let { url -> { onOpenExternalUrl(url) } }
    val backFocus = remember { FocusRequester() }
    val playFocus = remember { FocusRequester() }
    val detailListState = rememberLazyListState()
    val hasPlayAction = detail.target.mediaType != "series"
    val showStatus = state.inlineFailure != null || state.loading == ViewerLoading.DETAIL ||
        state.loading == ViewerLoading.SEASON || state.loading == ViewerLoading.SOURCES ||
        state.loading == ViewerLoading.ACTION
    val backLabel = stringResource(R.string.viewer_back)
    LaunchedEffect(isTv, detail.target.id, state.inlineFailure, state.sourcePicker) {
        if (isTv && state.sourcePicker == null) {
            if (hasPlayAction && state.inlineFailure == null) playFocus.requestFocus() else backFocus.requestFocus()
            withFrameNanos { }
        }
        detailListState.scrollToItem(0)
    }
    val detailLoadingLabel = stringResource(
        when (state.loading) {
            ViewerLoading.SEASON -> R.string.viewer_loading_season
            ViewerLoading.SOURCES -> R.string.viewer_loading_sources
            ViewerLoading.ACTION -> R.string.viewer_saving_change
            else -> R.string.viewer_loading_detail
        },
    )
    RivuneCinematicBackground {
        BoxWithConstraints(modifier = Modifier.fillMaxSize()) {
            val padding = viewerHorizontalPadding(maxWidth, isTv)
            val wideHero = isTv || maxWidth >= RivuneBreakpoints.expanded
            val mediumLayout = !isTv && maxWidth >= RivuneBreakpoints.medium
            LazyColumn(
                state = detailListState,
                modifier = Modifier.fillMaxSize().navigationBarsPadding(),
                contentPadding = PaddingValues(bottom = RivuneSpacing.huge),
                verticalArrangement = Arrangement.spacedBy(if (isTv) RivuneSpacing.xl else RivuneSpacing.lg),
            ) {
                item(key = "hero") {
                    Box(modifier = Modifier.fillMaxWidth()) {
                        if (wideHero) {
                            DetailArtwork(
                                imageUrl = backdrop,
                                title = title,
                                modifier = if (isTv) Modifier.matchParentSize() else Modifier.fillMaxWidth(),
                                aspectRatio = if (isTv) null else 21f / 9f,
                            )
                            DetailSummary(
                                detail = detail,
                                title = title,
                                overview = overview,
                                isTv = isTv,
                                isWide = true,
                                actionsEnabled = state.loading == null,
                                actionLoading = state.loading == ViewerLoading.ACTION,
                                onPlay = onPlay,
                                onToggleLibrary = onToggleLibrary,
                                onToggleWatched = onToggleWatched,
                                onTrailer = onTrailer,
                                playModifier = Modifier.focusRequester(playFocus),
                                modifier = Modifier
                                    .align(Alignment.BottomStart)
                                    .padding(
                                        start = padding,
                                        end = padding,
                                        top = if (isTv) RivuneSpacing.display + RivuneSpacing.md else 0.dp,
                                        bottom = RivuneSpacing.xxxl,
                                    )
                                    .widthIn(max = RivuneDimensions.contentMaxTablet),
                                overviewMaxLines = 3,
                            )
                        } else {
                            Column {
                                DetailArtwork(backdrop, title, Modifier.fillMaxWidth())
                                DetailSummary(
                                    detail = detail,
                                    title = title,
                                    overview = overview,
                                    isTv = false,
                                    isWide = false,
                                    actionsEnabled = state.loading == null,
                                    actionLoading = state.loading == ViewerLoading.ACTION,
                                    onPlay = onPlay,
                                    onToggleLibrary = onToggleLibrary,
                                    onToggleWatched = onToggleWatched,
                                    onTrailer = onTrailer,
                                    playModifier = Modifier.focusRequester(playFocus),
                                    modifier = Modifier.padding(horizontal = padding, vertical = RivuneSpacing.md),
                                )
                            }
                        }
                        if (!isTv) {
                            ViewerIconAction(
                                icon = Icons.AutoMirrored.Rounded.ArrowBack,
                                label = backLabel,
                                onClick = onBack,
                                isTv = false,
                                modifier = Modifier
                                    .align(Alignment.TopStart)
                                    .statusBarsPadding()
                                    .padding(
                                        start = padding,
                                        top = if (mediumLayout) RivuneSpacing.xxl else RivuneSpacing.md,
                                    )
                                    .size(ViewerPhoneTarget)
                                    .focusRequester(backFocus),
                            )
                        }
                    }
                }
                if (showStatus) {
                    item(key = "detail-status") {
                        Box(modifier = Modifier.padding(horizontal = padding)) {
                            InlineStatus(
                                failure = state.inlineFailure,
                                loading = state.loading == ViewerLoading.DETAIL ||
                                    state.loading == ViewerLoading.SEASON ||
                                    state.loading == ViewerLoading.SOURCES ||
                                    state.loading == ViewerLoading.ACTION,
                                onRetry = onRetry,
                                isTv = isTv,
                                loadingLabel = detailLoadingLabel,
                            )
                        }
                    }
                }
                if (orderedSeasons.isNotEmpty()) {
                    item(key = "season-heading") {
                        RivuneSectionHeading(
                            title = stringResource(R.string.viewer_seasons),
                            modifier = Modifier.padding(horizontal = padding),
                        )
                    }
                    item(key = "seasons") {
                        LazyRow(
                            contentPadding = PaddingValues(horizontal = padding),
                            horizontalArrangement = Arrangement.spacedBy(ViewerCardGap),
                        ) {
                            items(orderedSeasons, key = { it.id }) { summary ->
                                val facts = listOfNotNull(
                                    pluralStringResource(R.plurals.viewer_episode_count, summary.episodeCount, summary.episodeCount),
                                    summary.airDate?.let { localizedDate(it, Locale.getDefault()) },
                                    summary.voteAverage.takeIf { it > 0.0 }?.let { String.format(Locale.getDefault(), "★ %.1f", it) },
                                )
                                SeasonTile(
                                    title = summary.name,
                                    subtitle = facts.joinToString(" · "),
                                    imageUrl = artworkUrl(summary.backdropUrl ?: summary.posterUrl),
                                    selected = season?.id == summary.id,
                                    isTv = isTv,
                                    onClick = { onSeason(summary.id) },
                                )
                            }
                        }
                    }
                }
                if (season != null && series != null) {
                    item(key = "episode-heading") {
                        RivuneSectionHeading(
                            title = stringResource(R.string.viewer_episodes),
                            modifier = Modifier.padding(horizontal = padding),
                        )
                    }
                    if (isTv) {
                        items(season.episodes, key = { it.id }) { episode ->
                            val target = episode.toMediaTarget(series, detail.target)
                            EpisodeRow(
                                target = target,
                                progress = detail.episodeProgress[episode.id],
                                imageUrl = artworkUrl(episode.stillUrl ?: episode.backdropUrl),
                                runtimeMinutes = episode.runtimeMinutes,
                                rating = episode.voteAverage.takeIf { it > 0.0 },
                                isTv = true,
                                onClick = { onEpisode(target) },
                                modifier = Modifier.padding(horizontal = padding),
                            )
                        }
                    } else {
                        item(key = "episodes") {
                            LazyRow(
                                contentPadding = PaddingValues(horizontal = padding),
                                horizontalArrangement = Arrangement.spacedBy(ViewerCardGap),
                            ) {
                                items(season.episodes, key = { it.id }) { episode ->
                                    val target = episode.toMediaTarget(series, detail.target)
                                    EpisodeRow(
                                        target = target,
                                        progress = detail.episodeProgress[episode.id],
                                        imageUrl = artworkUrl(episode.stillUrl ?: episode.backdropUrl),
                                        runtimeMinutes = episode.runtimeMinutes,
                                        rating = episode.voteAverage.takeIf { it > 0.0 },
                                        isTv = false,
                                        onClick = { onEpisode(target) },
                                        modifier = Modifier.width(RivuneDimensions.landscapeCardWidth),
                                    )
                                }
                            }
                        }
                    }
                }
                if (cast.isNotEmpty()) {
                    item(key = "cast-heading") {
                        RivuneSectionHeading(
                            title = stringResource(R.string.viewer_cast),
                            modifier = Modifier.padding(horizontal = padding),
                        )
                    }
                    item(key = "cast") {
                        LazyRow(
                            contentPadding = PaddingValues(horizontal = padding),
                            horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.sm),
                        ) {
                            items(cast, key = { it.id }) { member ->
                                Column(
                                    modifier = Modifier
                                        .width(if (isTv) RivuneDimensions.navigationRailTv else RivuneDimensions.navigationRail)
                                        .semantics(mergeDescendants = true) { },
                                    verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
                                ) {
                                    RivuneArtwork(
                                        model = artworkUrl(member.profileUrl),
                                        fallback = member.name,
                                        contentDescription = null,
                                        modifier = Modifier.fillMaxWidth().aspectRatio(2f / 3f).clip(RivuneShapes.medium),
                                    )
                                    Text(
                                        text = member.name,
                                        maxLines = 2,
                                        overflow = TextOverflow.Ellipsis,
                                        style = MaterialTheme.typography.titleSmall,
                                    )
                                    member.character?.takeIf(String::isNotBlank)?.let { character ->
                                        Text(
                                            text = character,
                                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                                            maxLines = 2,
                                            overflow = TextOverflow.Ellipsis,
                                            style = MaterialTheme.typography.bodySmall,
                                        )
                                    }
                                }
                            }
                        }
                    }
                }
            }
        }
    if (isTv) {
        ViewerIconAction(
            icon = Icons.AutoMirrored.Rounded.ArrowBack,
            label = backLabel,
            onClick = onBack,
            isTv = true,
            modifier = Modifier
                .statusBarsPadding()
                .padding(start = ViewerTvDockContentInset, top = RivuneSpacing.xxl)
                .size(ViewerTvTarget)
                .focusRequester(backFocus),
        )
    }
    }
    state.sourcePicker?.let { picker ->
        SourcePickerDialog(
            picker = picker,
            isTv = isTv,
            externalPlayers = externalPlayers,
            loading = state.loading == ViewerLoading.SOURCES || state.loading == ViewerLoading.PLAYER,
            failure = state.inlineFailure,
            onDismiss = onDismissSources,
            onRetry = onRetry,
            onSelectSource = onSelectSource,
            onChooseTarget = onChooseTarget,
            onDismissTarget = onDismissTarget,
        )
    }
}

@Composable
private fun DetailArtwork(
    imageUrl: String?,
    title: String,
    modifier: Modifier = Modifier,
    aspectRatio: Float? = 16f / 9f,
) {
    val artworkModifier = if (aspectRatio == null) modifier.fillMaxSize() else modifier.aspectRatio(aspectRatio)
    Box(modifier = artworkModifier) {
        RivuneArtwork(
            model = imageUrl,
            fallback = title,
            contentDescription = null,
            modifier = Modifier.fillMaxSize(),
        )
        Box(
            modifier = Modifier
                .fillMaxSize()
                .background(
                    Brush.verticalGradient(
                        colors = listOf(
                            MaterialTheme.colorScheme.scrim,
                            Color.Transparent,
                            MaterialTheme.colorScheme.background,
                        ),
                    ),
                ),
        )
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun DetailSummary(
    detail: MediaDetailState,
    title: String,
    overview: String?,
    isTv: Boolean,
    isWide: Boolean,
    actionsEnabled: Boolean,
    actionLoading: Boolean,
    onPlay: () -> Unit,
    onToggleLibrary: () -> Unit,
    onToggleWatched: () -> Unit,
    onTrailer: (() -> Unit)?,
    playModifier: Modifier = Modifier,
    modifier: Modifier = Modifier,
    overviewMaxLines: Int = Int.MAX_VALUE,
) {
    val locale = Locale.getDefault()
    val movie = detail.movie
    val series = detail.series
    val season = detail.season
    val rating = movie?.voteAverage ?: season?.voteAverage ?: series?.voteAverage
    val primaryMetadata = listOfNotNull(
        (movie?.releaseDate ?: season?.airDate ?: series?.firstAirDate ?: detail.target.releaseInfo)
            ?.let { localizedDate(it, locale) },
        movie?.runtimeMinutes?.let { stringResource(R.string.viewer_minutes, it) },
        rating?.takeIf { it > 0.0 }?.let { String.format(locale, "★ %.1f", it) },
        series?.status?.takeIf(String::isNotBlank),
    )
    val secondaryMetadata = listOfNotNull(
        series?.numberOfSeasons?.let { "$it · ${stringResource(R.string.viewer_seasons)}" },
        series?.numberOfEpisodes?.let { pluralStringResource(R.plurals.viewer_episode_count, it, it) },
        season?.episodes?.size?.let { pluralStringResource(R.plurals.viewer_episode_count, it, it) },
        (movie?.genres ?: series?.genres.orEmpty()).takeIf { it.isNotEmpty() }?.joinToString(" · ") { it.name },
    )
    val tagline = movie?.tagline ?: series?.tagline
    val seasonWatched = season?.episodes
        ?.takeIf { it.isNotEmpty() }
        ?.all { detail.episodeProgress[it.id]?.completed == true } == true
    val watched = if (season != null) seasonWatched else detail.progress?.completed == true
    val resumeProgress = detail.progress?.takeIf {
        detail.target.mediaType != "series" && it.positionSeconds > 0 && it.durationSeconds > 0 && !it.completed
    }
    val resumeFraction = resumeProgress?.let { progress ->
        (progress.positionSeconds.toFloat() / progress.durationSeconds).coerceIn(0f, 1f)
    }
    val resumeProgressDescription = resumeFraction?.let { fraction ->
        stringResource(R.string.viewer_progress_percent, (fraction * 100).toInt())
    }
    Column(
        modifier = modifier,
        verticalArrangement = Arrangement.spacedBy(
            when {
                isTv -> RivuneSpacing.md
                isWide -> RivuneSpacing.sm
                else -> RivuneSpacing.xs
            },
        ),
    ) {
        Text(
            text = title,
            modifier = Modifier.semantics { heading() },
            style = when {
                isTv -> MaterialTheme.typography.displayMedium
                isWide -> MaterialTheme.typography.headlineLarge
                else -> MaterialTheme.typography.headlineMedium
            },
            maxLines = if (isTv) 2 else 3,
            overflow = TextOverflow.Ellipsis,
        )
        tagline?.takeIf(String::isNotBlank)?.let {
            Text(
                text = it,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                style = if (isTv) MaterialTheme.typography.titleMedium else MaterialTheme.typography.bodyLarge,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
        }
        if (primaryMetadata.isNotEmpty()) {
            Text(
                text = primaryMetadata.joinToString("  ·  "),
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                style = MaterialTheme.typography.labelLarge,
            )
        }
        if (secondaryMetadata.isNotEmpty()) {
            Text(
                text = secondaryMetadata.joinToString("  ·  "),
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                style = MaterialTheme.typography.bodyMedium,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
        }
        FlowRow(
            modifier = Modifier.widthIn(max = RivuneDimensions.contentMaxTablet).fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(if (isTv) RivuneSpacing.sm else RivuneSpacing.xs),
            verticalArrangement = Arrangement.spacedBy(if (isTv) RivuneSpacing.sm else RivuneSpacing.xs),
        ) {
            if (detail.target.mediaType != "series") {
                DetailLabeledAction(
                    label = stringResource(if (resumeProgress != null) R.string.viewer_resume else R.string.viewer_play),
                    icon = Icons.Rounded.PlayArrow,
                    onClick = onPlay,
                    enabled = actionsEnabled,
                    isTv = isTv,
                    modifier = playModifier,
                    progress = resumeFraction,
                    progressDescription = resumeProgressDescription,
                )
            }
            onTrailer?.let {
                DetailTrailerAction(
                    label = stringResource(R.string.viewer_trailer),
                    onClick = it,
                    enabled = actionsEnabled,
                    isTv = isTv,
                )
            }
            if (detail.target.mediaType != "series" || season != null) {
                DetailIconAction(
                    icon = if (watched) Icons.Rounded.Visibility else Icons.Rounded.VisibilityOff,
                    label = stringResource(if (watched) R.string.viewer_mark_unwatched else R.string.viewer_mark_watched),
                    selected = watched,
                    enabled = actionsEnabled,
                    loading = actionLoading,
                    isTv = isTv,
                    onClick = onToggleWatched,
                )
            }
            if (detail.target.mediaType != "episode") {
                DetailIconAction(
                    icon = if (detail.inLibrary) Icons.Rounded.Check else Icons.Rounded.LibraryAdd,
                    label = stringResource(if (detail.inLibrary) R.string.viewer_in_library else R.string.viewer_add_library),
                    selected = detail.inLibrary,
                    enabled = actionsEnabled,
                    loading = actionLoading,
                    isTv = isTv,
                    onClick = onToggleLibrary,
                )
            }
        }
        if (!overview.isNullOrBlank()) {
            Text(
                text = overview,
                modifier = Modifier
                    .padding(top = if (isTv) RivuneSpacing.sm else RivuneSpacing.xs)
                    .widthIn(max = RivuneDimensions.contentMax),
                color = MaterialTheme.colorScheme.onSurface,
                maxLines = overviewMaxLines,
                overflow = TextOverflow.Ellipsis,
                style = MaterialTheme.typography.bodyLarge,
            )
        }
    }
}

@Composable
private fun DetailLabeledAction(
    label: String,
    icon: ImageVector,
    onClick: () -> Unit,
    enabled: Boolean,
    isTv: Boolean,
    progress: Float?,
    progressDescription: String?,
    modifier: Modifier = Modifier,
) {
    RivuneFocusSurface(
        onClick = onClick,
        enabled = enabled,
        isTv = isTv,
        idleColor = Color.Transparent,
        focusedColor = Color.Transparent,
        pressedColor = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.12f),
        showSelectionBorder = false,
        shape = RivuneShapes.pill,
        modifier = modifier
            .heightIn(min = if (isTv) ViewerTvTarget else ViewerPhoneTarget)
            .then(
                if (progressDescription != null) Modifier.semantics {
                    stateDescription = progressDescription
                } else Modifier,
            ),
    ) {
        Row(
            modifier = Modifier.padding(horizontal = if (isTv) RivuneSpacing.md else RivuneSpacing.sm),
            horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(
                imageVector = icon,
                contentDescription = null,
                modifier = Modifier.size(RivuneDimensions.iconMedium),
                tint = MaterialTheme.colorScheme.onSurface,
            )
            Column(verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xxs)) {
                Text(
                    text = label,
                    color = MaterialTheme.colorScheme.onSurface,
                    style = if (isTv) MaterialTheme.typography.titleMedium else MaterialTheme.typography.labelLarge,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                progress?.let { fraction ->
                    LinearProgressIndicator(
                        progress = { fraction },
                        modifier = Modifier
                            .width(80.dp)
                            .height(2.dp)
                            .clip(RivuneShapes.pill),
                        color = MaterialTheme.colorScheme.primary,
                        trackColor = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.16f),
                    )
                }
            }
        }
    }
}

@Composable
private fun DetailTrailerAction(
    label: String,
    onClick: () -> Unit,
    enabled: Boolean,
    isTv: Boolean,
) {
    val target = if (isTv) ViewerTvTarget else ViewerPhoneTarget
    RivuneFocusSurface(
        onClick = onClick,
        enabled = enabled,
        isTv = isTv,
        idleColor = Color.Transparent,
        focusedColor = Color.Transparent,
        pressedColor = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.12f),
        showSelectionBorder = false,
        shape = CircleShape,
        modifier = Modifier
            .size(target)
            .semantics { contentDescription = label },
    ) {
        Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
            Icon(
                imageVector = Icons.Rounded.Theaters,
                contentDescription = null,
                modifier = Modifier.size(RivuneDimensions.iconMedium),
                tint = MaterialTheme.colorScheme.onSurface,
            )
        }
    }
}

@Composable
private fun DetailIconAction(
    icon: ImageVector,
    label: String,
    selected: Boolean,
    enabled: Boolean,
    loading: Boolean,
    isTv: Boolean,
    onClick: () -> Unit,
) {
    val target = if (isTv) ViewerTvTarget else ViewerPhoneTarget
    RivuneFocusSurface(
        onClick = onClick,
        enabled = enabled && !loading,
        selected = selected,
        isTv = isTv,
        idleColor = Color.Transparent,
        selectedColor = Color.Transparent,
        focusedColor = Color.Transparent,
        pressedColor = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.12f),
        showSelectionBorder = false,
        shape = CircleShape,
        modifier = Modifier
            .size(target)
            .semantics {
                contentDescription = label
                this.selected = selected
                if (loading) stateDescription = label
            },
    ) {
        Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
            if (loading) {
                CircularProgressIndicator(
                    modifier = Modifier.size(RivuneDimensions.iconMedium),
                    color = MaterialTheme.colorScheme.onSurface,
                    strokeWidth = RivuneDimensions.hairline,
                )
            } else {
                Icon(
                    imageVector = icon,
                    contentDescription = null,
                    modifier = Modifier.size(RivuneDimensions.iconMedium),
                    tint = MaterialTheme.colorScheme.onSurface,
                )
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun SourcePickerDialog(
    picker: SourcePickerState,
    isTv: Boolean,
    externalPlayers: List<ExternalPlayerApp>,
    loading: Boolean,
    failure: UiFailure?,
    onDismiss: () -> Unit,
    onRetry: () -> Unit,
    onSelectSource: (PlaybackSourceOption) -> Unit,
    onChooseTarget: (ExternalPlayerApp?) -> Unit,
    onDismissTarget: () -> Unit,
) {
    val firstSourceFocus = remember { FocusRequester() }
    val dismissFocus = remember { FocusRequester() }
    val targetSource = picker.playerSource
    val targetPlayers = remember(targetSource, externalPlayers) {
        targetSource?.let { source ->
            ExternalPlaybackSupport(externalPlayers).playersFor(source.mode, source.protocol, source.container)
        }.orEmpty()
    }
    LaunchedEffect(isTv, picker.options.firstOrNull()?.id) {
        if (isTv) {
            if (picker.options.isNotEmpty()) firstSourceFocus.requestFocus() else dismissFocus.requestFocus()
        }
    }
    BackHandler(onBack = onDismiss)
    val content: @Composable (Modifier) -> Unit = { modifier ->
        SourcePickerContent(
            picker = picker,
            isTv = isTv,
            loading = loading,
            failure = failure,
            firstSourceFocus = firstSourceFocus,
            dismissFocus = dismissFocus,
            onDismiss = onDismiss,
            onRetry = onRetry,
            onSelect = onSelectSource,
            modifier = modifier,
        )
    }
    BoxWithConstraints(modifier = Modifier.fillMaxSize()) {
        val compact = !isTv && maxWidth < RivuneBreakpoints.medium
        val dialogHeight = (maxHeight - RivuneSpacing.huge)
            .coerceAtMost(RivuneDimensions.sourceDialogMax)
            .coerceAtLeast(240.dp)
        if (compact) {
            ModalBottomSheet(
                onDismissRequest = onDismiss,
                containerColor = MaterialTheme.colorScheme.surface,
                contentColor = MaterialTheme.colorScheme.onSurface,
                shape = RivuneShapes.extraLarge,
            ) {
                content(
                    Modifier
                        .fillMaxWidth()
                        .heightIn(max = RivuneDimensions.sourceDialogMax)
                        .navigationBarsPadding()
                        .imePadding()
                        .padding(horizontal = RivuneSpacing.lg, vertical = RivuneSpacing.sm),
                )
            }
        } else {
            Dialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false)) {
                Box(
                    modifier = Modifier.fillMaxSize().padding(horizontal = RivuneSpacing.huge, vertical = RivuneSpacing.xl),
                    contentAlignment = Alignment.Center,
                ) {
                    RivuneFunctionalSurface(
                        modifier = Modifier
                            .widthIn(max = ViewerPreferencesMaxWidth)
                            .fillMaxWidth()
                            .height(dialogHeight),
                        shape = RivuneShapes.large,
                        contentPadding = PaddingValues(if (isTv) RivuneSpacing.xxl else RivuneSpacing.lg),
                    ) {
                        content(Modifier.fillMaxSize())
                    }
                }
            }
        }
    }
    targetSource?.let { source ->
        PlaybackTargetDialog(
            source = source,
            players = targetPlayers,
            isTv = isTv,
            onDismiss = onDismissTarget,
            onChoose = onChooseTarget,
        )
    }
}

@Composable
private fun SourcePickerContent(
    picker: SourcePickerState,
    isTv: Boolean,
    loading: Boolean,
    failure: UiFailure?,
    firstSourceFocus: FocusRequester,
    dismissFocus: FocusRequester,
    onDismiss: () -> Unit,
    onRetry: () -> Unit,
    onSelect: (PlaybackSourceOption) -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(modifier = modifier) {
        PickerHeading(
            title = stringResource(R.string.viewer_choose_source),
            subtitle = picker.target.title,
            isTv = isTv,
            onDismiss = onDismiss,
            dismissFocus = dismissFocus,
        )
        Spacer(Modifier.height(RivuneSpacing.md))
        InlineStatus(
            loading = loading,
            failure = failure,
            onRetry = onRetry,
            isTv = isTv,
            loadingLabel = stringResource(R.string.viewer_preparing_source),
        )
        if (picker.partial) {
            InlineWarning(stringResource(R.string.viewer_sources_partial))
            Spacer(Modifier.height(RivuneSpacing.sm))
        }
        if (picker.options.isEmpty() && !loading && failure == null) {
            InlineEmpty(
                title = stringResource(R.string.viewer_sources_empty_title),
                body = stringResource(R.string.viewer_sources_empty_body),
            )
        } else if (picker.options.isNotEmpty()) {
            LazyColumn(
                modifier = Modifier.fillMaxWidth().weight(1f),
                contentPadding = PaddingValues(vertical = RivuneSpacing.xs),
                verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
            ) {
                items(picker.options, key = { it.id }) { option ->
                    RivuneFocusSurface(
                        onClick = { onSelect(option) },
                        isTv = isTv,
                        enabled = !loading,
                        shape = RivuneShapes.small,
                        modifier = Modifier
                            .fillMaxWidth()
                            .then(
                                if (option.id == picker.options.first().id) Modifier.focusRequester(firstSourceFocus)
                                else Modifier,
                            ),
                    ) {
                        Row(
                            modifier = Modifier.padding(horizontal = RivuneSpacing.md, vertical = RivuneSpacing.sm),
                            horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            Column(
                                modifier = Modifier.weight(1f),
                                verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xxs),
                            ) {
                                Text(
                                    text = option.name,
                                    maxLines = 2,
                                    overflow = TextOverflow.Ellipsis,
                                    style = MaterialTheme.typography.titleMedium,
                                )
                                val description = option.description?.takeIf(String::isNotBlank)
                                    ?: option.filename?.takeIf(String::isNotBlank)
                                description?.let {
                                    Text(
                                        text = it,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                        maxLines = 2,
                                        overflow = TextOverflow.Ellipsis,
                                        style = MaterialTheme.typography.bodyMedium,
                                    )
                                }
                                option.filename
                                    ?.takeIf(String::isNotBlank)
                                    ?.takeIf { filename -> filename != description && filename != option.name }
                                    ?.let { filename ->
                                        Text(
                                            text = stringResource(R.string.viewer_source_file, filename),
                                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                                            maxLines = 1,
                                            overflow = TextOverflow.Ellipsis,
                                            style = MaterialTheme.typography.bodySmall,
                                        )
                                    }
                                val addonName = option.addonName?.takeIf(String::isNotBlank)
                                val manifestId = option.manifestId.takeIf(String::isNotBlank)
                                val provider = when {
                                    addonName != null && manifestId != null && !addonName.equals(manifestId, ignoreCase = true) ->
                                        "$addonName · $manifestId"
                                    addonName != null -> addonName
                                    manifestId != null -> manifestId
                                    else -> option.addonId.toString()
                                }
                                val technical = listOfNotNull(
                                    option.mode?.name?.lowercase(Locale.getDefault())?.replace('_', ' '),
                                    option.protocol.takeIf(String::isNotBlank)?.uppercase(Locale.getDefault()),
                                    option.container?.takeIf(String::isNotBlank)?.uppercase(Locale.getDefault()),
                                ).distinct()
                                Text(
                                    text = (listOf(provider) + technical).joinToString(" · "),
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    maxLines = 1,
                                    overflow = TextOverflow.Ellipsis,
                                    style = MaterialTheme.typography.labelMedium,
                                )
                            }
                            Icon(
                                Icons.Rounded.ChevronRight,
                                contentDescription = null,
                                tint = MaterialTheme.colorScheme.primary,
                            )
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun PickerHeading(
    title: String,
    subtitle: String,
    isTv: Boolean,
    onDismiss: () -> Unit,
    dismissFocus: FocusRequester? = null,
) {
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.sm)) {
        Column(modifier = Modifier.weight(1f)) {
            Text(title, modifier = Modifier.semantics { heading() }, style = MaterialTheme.typography.headlineMedium)
            Text(
                subtitle,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
                style = MaterialTheme.typography.bodyMedium,
            )
        }
        RivuneTextButton(
            label = stringResource(R.string.pin_cancel),
            onClick = onDismiss,
            modifier = dismissFocus?.let { Modifier.focusRequester(it) } ?: Modifier,
            isTv = isTv,
        )
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun PlaybackTargetDialog(
    source: PlaybackSourceOption,
    players: List<ExternalPlayerApp>,
    isTv: Boolean,
    onDismiss: () -> Unit,
    onChoose: (ExternalPlayerApp?) -> Unit,
) {
    val firstTargetFocus = remember { FocusRequester() }
    val dismissFocus = remember { FocusRequester() }
    val hasRivuneTarget = source.mode != io.rivune.api.PlaybackMode.EXTERNAL
    val hasTarget = hasRivuneTarget || players.isNotEmpty()
    LaunchedEffect(isTv, source.id, players.size) {
        if (isTv) {
            if (hasTarget) firstTargetFocus.requestFocus() else dismissFocus.requestFocus()
        }
    }
    BackHandler(onBack = onDismiss)
    val content: @Composable (Modifier) -> Unit = { modifier ->
        Column(modifier = modifier, verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xs)) {
            PickerHeading(
                title = stringResource(R.string.viewer_choose_player),
                subtitle = source.name,
                isTv = isTv,
                onDismiss = onDismiss,
                dismissFocus = dismissFocus,
            )
            Spacer(Modifier.height(RivuneSpacing.sm))
            LazyColumn(
                modifier = Modifier.fillMaxWidth().weight(1f, fill = false),
                verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
            ) {
                if (hasRivuneTarget) {
                    item(key = "rivune") {
                        PlaybackTargetRow(
                            label = stringResource(R.string.viewer_player_rivune),
                            supporting = stringResource(R.string.viewer_player_rivune_body),
                            isTv = isTv,
                            onClick = { onChoose(null) },
                            modifier = Modifier.focusRequester(firstTargetFocus),
                        )
                    }
                }
                items(players, key = { it.packageName }) { player ->
                    PlaybackTargetRow(
                        label = player.label,
                        supporting = stringResource(R.string.viewer_player_external_body),
                        isTv = isTv,
                        onClick = { onChoose(player) },
                        modifier = if (!hasRivuneTarget && player == players.first()) {
                            Modifier.focusRequester(firstTargetFocus)
                        } else {
                            Modifier
                        },
                    )
                }
            }
        }
    }
    BoxWithConstraints(modifier = Modifier.fillMaxSize()) {
        val compact = !isTv && maxWidth < RivuneBreakpoints.medium
        val contentHeight = (maxHeight - RivuneSpacing.huge).coerceAtMost(RivuneDimensions.sourceDialogMax)
        if (compact) {
            ModalBottomSheet(
                onDismissRequest = onDismiss,
                containerColor = MaterialTheme.colorScheme.surface,
                contentColor = MaterialTheme.colorScheme.onSurface,
                shape = RivuneShapes.extraLarge,
            ) {
                content(
                    Modifier
                        .fillMaxWidth()
                        .heightIn(max = RivuneDimensions.sourceDialogMax)
                        .navigationBarsPadding()
                        .padding(horizontal = RivuneSpacing.lg, vertical = RivuneSpacing.sm),
                )
            }
        } else {
            Dialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false)) {
                Box(
                    modifier = Modifier.fillMaxSize().padding(horizontal = RivuneSpacing.huge, vertical = RivuneSpacing.xl),
                    contentAlignment = Alignment.Center,
                ) {
                    RivuneFunctionalSurface(
                        modifier = Modifier
                            .widthIn(max = RivuneDimensions.dialogMax)
                            .fillMaxWidth()
                            .heightIn(max = contentHeight),
                        shape = RivuneShapes.large,
                        contentPadding = PaddingValues(if (isTv) RivuneSpacing.xxl else RivuneSpacing.lg),
                    ) {
                        content(Modifier.fillMaxWidth())
                    }
                }
            }
        }
    }
}

@Composable
private fun PlaybackTargetRow(
    label: String,
    supporting: String,
    isTv: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    RivuneFocusSurface(onClick = onClick, isTv = isTv, modifier = modifier.fillMaxWidth()) {
        Row(
            modifier = Modifier.padding(horizontal = RivuneSpacing.md, vertical = RivuneSpacing.sm),
            horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(Icons.Rounded.PlayArrow, contentDescription = null, tint = MaterialTheme.colorScheme.primary)
            Column(modifier = Modifier.weight(1f)) {
                Text(label, maxLines = 2, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.titleMedium)
                Text(
                    supporting,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                    style = MaterialTheme.typography.bodyMedium,
                )
            }
            Icon(Icons.Rounded.ChevronRight, contentDescription = null)
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun AccountDialog(
    isTv: Boolean,
    onDismiss: () -> Unit,
    onRefresh: () -> Unit,
    onChangeProfile: () -> Unit,
    onPreferences: () -> Unit,
    onLogout: () -> Unit,
) {
    val firstActionFocus = remember { FocusRequester() }
    BackHandler(onBack = onDismiss)
    BoxWithConstraints(modifier = Modifier.fillMaxSize()) {
        val compactPhone = !isTv && maxWidth < RivuneBreakpoints.medium
        if (compactPhone) {
            ModalBottomSheet(
                onDismissRequest = onDismiss,
                shape = RivuneShapes.extraLarge,
                containerColor = MaterialTheme.colorScheme.surfaceContainer,
            ) {
                AccountPanelContent(
                    isTv = false,
                    firstActionFocus = firstActionFocus,
                    onRefresh = onRefresh,
                    onChangeProfile = onChangeProfile,
                    onPreferences = onPreferences,
                    onLogout = onLogout,
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        } else {
            Dialog(
                onDismissRequest = onDismiss,
                properties = DialogProperties(usePlatformDefaultWidth = false),
            ) {
                Box(
                    modifier = Modifier
                        .fillMaxSize()
                        .padding(
                            horizontal = if (isTv) RivuneSpacing.huge else RivuneSpacing.xl,
                            vertical = RivuneSpacing.xl,
                        ),
                    contentAlignment = Alignment.Center,
                ) {
                    RivuneFunctionalSurface(
                        modifier = if (isTv) {
                            Modifier.width(ViewerAccountDialogWidthTv)
                        } else {
                            Modifier
                                .widthIn(max = RivuneDimensions.dialogMax)
                                .fillMaxWidth()
                        },
                        shape = RivuneShapes.large,
                        contentPadding = PaddingValues(0.dp),
                    ) {
                        AccountPanelContent(
                            isTv = isTv,
                            firstActionFocus = firstActionFocus,
                            onRefresh = onRefresh,
                            onChangeProfile = onChangeProfile,
                            onPreferences = onPreferences,
                            onLogout = onLogout,
                            modifier = Modifier.fillMaxWidth(),
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun AccountPanelContent(
    isTv: Boolean,
    firstActionFocus: FocusRequester,
    onRefresh: () -> Unit,
    onChangeProfile: () -> Unit,
    onPreferences: () -> Unit,
    onLogout: () -> Unit,
    modifier: Modifier = Modifier,
) {
    LaunchedEffect(isTv) {
        if (isTv) firstActionFocus.requestFocus()
    }
    Column(
        modifier = modifier
            .heightIn(max = RivuneDimensions.contentMaxTablet)
            .padding(
                horizontal = if (isTv) RivuneSpacing.md else RivuneSpacing.xl,
                vertical = if (isTv) RivuneSpacing.sm else RivuneSpacing.md,
            ),
    ) {
        LazyColumn(
            modifier = Modifier.weight(1f, fill = false),
            contentPadding = PaddingValues(vertical = if (isTv) 0.dp else RivuneSpacing.xs),
        ) {
            item {
                AccountAction(
                    icon = Icons.Rounded.Settings,
                    label = stringResource(R.string.viewer_preferences),
                    isTv = isTv,
                    modifier = Modifier.focusRequester(firstActionFocus),
                    onClick = onPreferences,
                )
            }
            item {
                HorizontalDivider(
                    modifier = Modifier.padding(horizontal = RivuneSpacing.md),
                    color = MaterialTheme.colorScheme.outlineVariant,
                )
                AccountAction(
                    icon = Icons.Rounded.Person,
                    label = stringResource(R.string.home_change_profile),
                    isTv = isTv,
                    onClick = onChangeProfile,
                )
            }
            item {
                HorizontalDivider(
                    modifier = Modifier.padding(horizontal = RivuneSpacing.md),
                    color = MaterialTheme.colorScheme.outlineVariant,
                )
                AccountAction(
                    icon = Icons.Rounded.Refresh,
                    label = stringResource(R.string.home_refresh),
                    isTv = isTv,
                    onClick = onRefresh,
                )
            }
            item {
                Spacer(Modifier.height(if (isTv) RivuneSpacing.xs else RivuneSpacing.lg))
                HorizontalDivider(
                    modifier = Modifier.padding(horizontal = RivuneSpacing.sm),
                    color = MaterialTheme.colorScheme.outlineVariant,
                )
                Spacer(Modifier.height(RivuneSpacing.xxs))
                AccountAction(
                    icon = Icons.AutoMirrored.Rounded.Logout,
                    label = stringResource(R.string.logout),
                    isTv = isTv,
                    destructive = true,
                    onClick = onLogout,
                )
            }
        }
    }
}

@Composable
private fun AccountAction(
    icon: ImageVector,
    label: String,
    isTv: Boolean,
    destructive: Boolean = false,
    modifier: Modifier = Modifier,
    onClick: () -> Unit,
) {
    val contentColor = if (destructive) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurface
    RivuneFocusSurface(
        onClick = onClick,
        isTv = isTv,
        idleColor = if (isTv) MaterialTheme.colorScheme.surface else Color.Transparent,
        focusedColor = if (destructive) {
            MaterialTheme.colorScheme.error.copy(alpha = 0.12f)
        } else {
            MaterialTheme.colorScheme.surfaceContainerHighest
        },
        shape = RivuneShapes.small,
        modifier = modifier
            .fillMaxWidth()
            .heightIn(min = ViewerPhoneTarget),
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(
                    horizontal = if (isTv) RivuneSpacing.sm else RivuneSpacing.md,
                    vertical = if (isTv) RivuneSpacing.xs else RivuneSpacing.sm,
                ),
            horizontalArrangement = Arrangement.spacedBy(if (isTv) RivuneSpacing.sm else RivuneSpacing.md),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(
                imageVector = icon,
                contentDescription = null,
                modifier = Modifier.size(RivuneDimensions.iconMedium),
                tint = if (destructive) contentColor else MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Text(
                text = label,
                modifier = Modifier.weight(1f),
                color = contentColor,
                style = MaterialTheme.typography.bodyLarge,
            )
        }
    }
}

@Composable
private fun MediaRow(
    title: String,
    items: List<MediaTarget>,
    isTv: Boolean,
    artworkUrl: (String?) -> String?,
    onMedia: (MediaTarget) -> Unit,
) {
    Column(verticalArrangement = Arrangement.spacedBy(RivuneSpacing.sm)) {
        RivuneSectionHeading(title = title)
        BoxWithConstraints(modifier = Modifier.fillMaxWidth()) {
            val focusInset = if (isTv) RivuneSpacing.xxs else 0.dp
            val cardWidth = viewerRowCardWidth(
                width = maxWidth - focusInset,
                targetWidth = if (isTv) ViewerTvLandscapeTarget else RivuneDimensions.landscapeCardWidth,
                phoneVisibleCards = 2.4f,
                isTv = isTv,
            )
            LazyRow(
                contentPadding = PaddingValues(
                    start = focusInset,
                    top = focusInset,
                    end = viewerHorizontalPadding(maxWidth, isTv),
                    bottom = focusInset,
                ),
                horizontalArrangement = Arrangement.spacedBy(ViewerMediaRowGap),
            ) {
                items(items, key = { "${it.mediaType}:${it.id}" }) { item ->
                    MediaTile(
                        target = item,
                        imageUrl = artworkUrl(item.backgroundUrl ?: item.posterUrl),
                        isTv = isTv,
                        modifier = Modifier.width(cardWidth),
                        landscape = true,
                        onClick = { onMedia(item) },
                    )
                }
            }
        }
    }
}

@Composable
private fun MediaRowSkeleton(isTv: Boolean) {
    Column(verticalArrangement = Arrangement.spacedBy(RivuneSpacing.sm)) {
        RivuneSkeleton(
            modifier = Modifier
                .width(RivuneDimensions.landscapeCardWidth)
                .height(RivuneSpacing.xl),
            shape = RivuneShapes.small,
        )
        BoxWithConstraints(modifier = Modifier.fillMaxWidth()) {
            val focusInset = if (isTv) RivuneSpacing.xxs else 0.dp
            val cardWidth = viewerRowCardWidth(
                width = maxWidth - focusInset,
                targetWidth = if (isTv) ViewerTvLandscapeTarget else RivuneDimensions.landscapeCardWidth,
                phoneVisibleCards = 2.4f,
                isTv = isTv,
            )
            LazyRow(
                contentPadding = PaddingValues(
                    start = focusInset,
                    top = focusInset,
                    end = viewerHorizontalPadding(maxWidth, isTv),
                    bottom = focusInset,
                ),
                horizontalArrangement = Arrangement.spacedBy(ViewerMediaRowGap),
            ) {
                items(count = ceil((maxWidth / cardWidth).toDouble()).toInt().coerceAtLeast(1)) {
                    Column(
                        modifier = Modifier.width(cardWidth),
                        verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
                    ) {
                        RivuneSkeleton(
                            modifier = Modifier
                                .fillMaxWidth()
                                .aspectRatio(16f / 9f),
                        )
                        RivuneSkeleton(
                            modifier = Modifier
                                .fillMaxWidth(ViewerSkeletonTitleFraction)
                                .height(RivuneSpacing.md),
                            shape = RivuneShapes.small,
                        )
                        RivuneSkeleton(
                            modifier = Modifier
                                .fillMaxWidth(ViewerSkeletonMetadataFraction)
                                .height(RivuneSpacing.sm),
                            shape = RivuneShapes.small,
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun MediaTile(
    target: MediaTarget,
    imageUrl: String?,
    isTv: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    landscape: Boolean = target.mediaType == "episode",
) {
    val progress = if (landscape && target.durationSeconds > 0) {
        (target.resumePositionSeconds.toFloat() / target.durationSeconds).coerceIn(0f, 1f)
    } else {
        null
    }
    val progressDescription = progress?.let {
        stringResource(R.string.viewer_progress_percent, (it * 100).toInt())
    }
    val availabilityDescription = if (target.available) null else stringResource(R.string.viewer_unavailable)
    val episodeMetadata = listOfNotNull(
        target.seasonNumber?.let { stringResource(R.string.viewer_season_short, it) },
        target.episodeNumber?.let { stringResource(R.string.viewer_episode_short, it) },
    ).joinToString(" · ").takeIf(String::isNotBlank)
    val metadata = episodeMetadata
        ?: target.releaseInfo?.takeIf(String::isNotBlank)?.let { localizedDate(it, Locale.getDefault()) }
        ?: mediaTypeLabel(target.mediaType)

    RivuneFocusSurface(
        onClick = onClick,
        isTv = isTv,
        idleColor = Color.Transparent,
        modifier = modifier.semantics {
            contentDescription = target.title
            (progressDescription ?: availabilityDescription)?.let { stateDescription = it }
        },
    ) {
        Column(
            modifier = Modifier
                .clearAndSetSemantics {}
                .padding(if (isTv) RivuneSpacing.xxs else 0.dp),
            verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
        ) {
            Box(modifier = Modifier.clip(RivuneShapes.medium)) {
                RivuneArtwork(
                    model = imageUrl,
                    fallback = target.title,
                    contentDescription = null,
                    modifier = Modifier
                        .fillMaxWidth()
                        .aspectRatio(if (landscape) 16f / 9f else 2f / 3f),
                )
                progress?.let {
                    LinearProgressIndicator(
                        progress = { it },
                        modifier = Modifier
                            .align(Alignment.BottomCenter)
                            .fillMaxWidth()
                            .height(RivuneSpacing.xxs),
                    )
                }
                if (!target.available) {
                    Surface(
                        modifier = Modifier
                            .align(Alignment.BottomStart)
                            .padding(RivuneSpacing.xs),
                        color = MaterialTheme.colorScheme.errorContainer,
                        shape = RivuneShapes.small,
                    ) {
                        Text(
                            text = stringResource(R.string.viewer_unavailable),
                            modifier = Modifier.padding(
                                horizontal = RivuneSpacing.xs,
                                vertical = RivuneSpacing.xxs,
                            ),
                            color = MaterialTheme.colorScheme.onErrorContainer,
                            style = MaterialTheme.typography.labelMedium,
                        )
                    }
                }
            }
            Text(
                text = target.title,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
                style = if (isTv) MaterialTheme.typography.titleLarge else MaterialTheme.typography.titleSmall,
            )
            Text(
                text = metadata,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                style = if (isTv) MaterialTheme.typography.bodyMedium else MaterialTheme.typography.bodySmall,
            )
        }
    }
}

@Composable
private fun FolderTile(
    folder: CollectionFolder,
    imageUrl: String?,
    isTv: Boolean,
    enabled: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    tileShape: CollectionTileShape = folder.tileShape,
) {
    val initial = folder.title.trim().take(1).uppercase(Locale.getDefault())
    val fallback = folder.coverEmoji?.takeIf(String::isNotBlank)
        ?: initial.takeIf(String::isNotBlank)
        ?: stringResource(R.string.folder_fallback)
    val aspectRatio = when (tileShape) {
        CollectionTileShape.POSTER -> 2f / 3f
        CollectionTileShape.LANDSCAPE -> 16f / 9f
        CollectionTileShape.SQUARE -> 1f
    }

    RivuneFocusSurface(
        onClick = onClick,
        isTv = isTv,
        enabled = enabled,
        idleColor = Color.Transparent,
        modifier = modifier.semantics { contentDescription = folder.title },
    ) {
        Column(
            modifier = Modifier.clearAndSetSemantics {},
            verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
        ) {
            Box(
                modifier = Modifier
                    .fillMaxWidth()
                    .aspectRatio(aspectRatio)
                    .clip(RivuneShapes.medium),
            ) {
                RivuneArtwork(
                    model = imageUrl,
                    fallback = fallback,
                    contentDescription = null,
                    modifier = Modifier.fillMaxSize(),
                )
            }
            if (!folder.hideTitle) {
                Text(
                    text = folder.title,
                    modifier = Modifier.fillMaxWidth(),
                    textAlign = TextAlign.Center,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                    style = if (isTv) MaterialTheme.typography.titleLarge else MaterialTheme.typography.titleSmall,
                )
            }
        }
    }
}

@Composable
private fun SeasonTile(
    title: String,
    subtitle: String,
    imageUrl: String?,
    selected: Boolean,
    isTv: Boolean,
    onClick: () -> Unit,
) {
    val width = if (isTv) 176.dp else 148.dp
    RivuneFocusSurface(
        onClick = onClick,
        isTv = isTv,
        selected = selected,
        idleColor = Color.Transparent,
        selectedColor = MaterialTheme.colorScheme.primary.copy(alpha = 0.10f),
        shape = RivuneShapes.medium,
        modifier = Modifier
            .width(width)
            .semantics { this.selected = selected },
    ) {
        Column(verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xs)) {
            Box {
                RivuneArtwork(
                    model = imageUrl,
                    fallback = title,
                    contentDescription = null,
                    modifier = Modifier
                        .fillMaxWidth()
                        .aspectRatio(16f / 9f)
                        .clip(RivuneShapes.medium),
                )
                if (selected) {
                    Icon(
                        imageVector = Icons.Rounded.Check,
                        contentDescription = null,
                        tint = MaterialTheme.colorScheme.onPrimary,
                        modifier = Modifier
                            .align(Alignment.TopEnd)
                            .padding(RivuneSpacing.xs)
                            .clip(CircleShape)
                            .background(MaterialTheme.colorScheme.primary)
                            .padding(RivuneSpacing.xxs),
                    )
                }
            }
            Column(
                modifier = Modifier.padding(horizontal = RivuneSpacing.xs, vertical = RivuneSpacing.xxs),
                verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xxs),
            ) {
                Text(title, maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.titleSmall)
                Text(
                    subtitle,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                    style = MaterialTheme.typography.bodySmall,
                )
            }
        }
    }
}

@Composable
private fun EpisodeRow(
    target: MediaTarget,
    progress: PlaybackProgress?,
    imageUrl: String?,
    runtimeMinutes: Int?,
    rating: Double?,
    isTv: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val locale = Locale.getDefault()
    val facts = listOfNotNull(
        target.releaseInfo?.takeIf(String::isNotBlank)?.let { localizedDate(it, locale) },
        runtimeMinutes?.let { stringResource(R.string.viewer_minutes, it) },
        rating?.let { String.format(locale, "★ %.1f", it) },
    )
    RivuneFocusSurface(
        onClick = onClick,
        isTv = isTv,
        shape = RivuneShapes.medium,
        modifier = modifier,
    ) {
        if (isTv) {
            Row(
                modifier = Modifier.fillMaxWidth().padding(RivuneSpacing.sm),
                horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                RivuneArtwork(
                    model = imageUrl,
                    fallback = target.title,
                    contentDescription = null,
                    modifier = Modifier
                        .width(RivuneDimensions.landscapeCardWidthTv)
                        .aspectRatio(16f / 9f)
                        .clip(RivuneShapes.small),
                )
                EpisodeCopy(target, progress, facts, true, Modifier.weight(1f))
                Icon(Icons.Rounded.PlayArrow, contentDescription = null, tint = MaterialTheme.colorScheme.primary)
            }
        } else {
            Column {
                Box {
                    RivuneArtwork(
                        model = imageUrl,
                        fallback = target.title,
                        contentDescription = null,
                        modifier = Modifier.fillMaxWidth().aspectRatio(16f / 9f),
                    )
                    Icon(
                        Icons.Rounded.PlayArrow,
                        contentDescription = null,
                        tint = MaterialTheme.colorScheme.primary,
                        modifier = Modifier.align(Alignment.BottomEnd).padding(RivuneSpacing.xs),
                    )
                }
                EpisodeCopy(target, progress, facts, false, Modifier.padding(RivuneSpacing.sm))
            }
        }
    }
}

@Composable
private fun EpisodeCopy(
    target: MediaTarget,
    progress: PlaybackProgress?,
    facts: List<String>,
    isTv: Boolean,
    modifier: Modifier = Modifier,
) {
    Column(modifier = modifier, verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xs)) {
        Text(
            text = stringResource(R.string.viewer_episode_number, target.episodeNumber ?: 0, target.title),
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
            style = if (isTv) MaterialTheme.typography.titleLarge else MaterialTheme.typography.titleMedium,
        )
        if (facts.isNotEmpty()) {
            Text(
                text = facts.joinToString(" · "),
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
                style = MaterialTheme.typography.labelLarge,
            )
        }
        if (!target.description.isNullOrBlank()) {
            Text(
                text = target.description,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = if (isTv) 3 else 2,
                overflow = TextOverflow.Ellipsis,
                style = MaterialTheme.typography.bodyMedium,
            )
        }
        when {
            progress?.completed == true -> Text(
                stringResource(R.string.viewer_watched),
                color = MaterialTheme.colorScheme.primary,
                style = MaterialTheme.typography.labelMedium,
            )
            progress != null && progress.durationSeconds > 0 -> PlaybackProgressSummary(
                progress.positionSeconds,
                progress.durationSeconds,
            )
        }
    }
}

@Composable
private fun CalendarEventRow(
    event: CalendarEvent,
    imageUrl: String?,
    artworkHeight: Dp,
    isTv: Boolean,
    onClick: () -> Unit,
) {
    RivuneFocusSurface(
        onClick = onClick,
        isTv = isTv,
        idleColor = Color.Transparent,
        shape = RivuneShapes.small,
        modifier = Modifier.fillMaxWidth(),
    ) {
        Row(
            modifier = Modifier.padding(RivuneSpacing.sm),
            horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            RivuneArtwork(
                model = imageUrl,
                fallback = event.title,
                contentDescription = null,
                modifier = Modifier
                    .height(artworkHeight)
                    .aspectRatio(2f / 3f)
                    .clip(RivuneShapes.small),
            )
            Column(
                modifier = Modifier.weight(1f),
                verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xxs),
            ) {
                event.seriesTitle?.takeIf(String::isNotBlank)?.let { seriesTitle ->
                    Text(
                        text = seriesTitle,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        style = MaterialTheme.typography.labelMedium,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
                Text(
                    text = event.title,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                    style = if (isTv) MaterialTheme.typography.titleLarge else MaterialTheme.typography.titleSmall,
                )
                val episode = listOfNotNull(
                    event.seasonNumber?.let { stringResource(R.string.viewer_season_short, it) },
                    event.episodeNumber?.let { stringResource(R.string.viewer_episode_short, it) },
                ).joinToString(" · ")
                Text(
                    text = episode.ifBlank { mediaTypeLabel(event.mediaType.name.lowercase(Locale.ROOT)) },
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    style = MaterialTheme.typography.bodySmall,
                )
            }
            Icon(
                Icons.Rounded.ChevronRight,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}
@Composable
private fun ScreenToolbar(
    title: String,
    onBack: () -> Unit,
    isTv: Boolean,
    backModifier: Modifier = Modifier,
) {
    BoxWithConstraints(modifier = Modifier.fillMaxWidth()) {
        val startPadding = when {
            isTv -> ViewerTvDockContentInset
            maxWidth >= RivuneBreakpoints.medium -> RivuneSpacing.xl
            else -> RivuneSpacing.xs
        }
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .heightIn(min = if (isTv) ViewerTvTarget else ViewerPhoneTarget)
                .padding(
                    start = startPadding,
                    end = viewerHorizontalPadding(maxWidth, isTv),
                    top = if (isTv) RivuneSpacing.xxl else RivuneSpacing.md,
                    bottom = if (isTv) RivuneSpacing.sm else RivuneSpacing.xs,
                ),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
        ) {
            ViewerIconAction(
                icon = Icons.AutoMirrored.Rounded.ArrowBack,
                label = stringResource(R.string.viewer_back),
                onClick = onBack,
                isTv = isTv,
                modifier = backModifier.size(if (isTv) ViewerTvTarget else ViewerPhoneTarget),
            )
            RivuneSectionHeading(
                title = title,
                modifier = Modifier.weight(1f),
            )
        }
    }
}

@Composable
private fun ViewerIconAction(
    icon: ImageVector,
    label: String,
    onClick: () -> Unit,
    isTv: Boolean,
    enabled: Boolean = true,
    modifier: Modifier = Modifier,
) {
    val actionModifier = modifier.semantics { contentDescription = label }
    if (isTv) {
        RivuneFocusSurface(
            onClick = onClick,
            enabled = enabled,
            isTv = true,
            idleColor = Color.Transparent,
            shape = CircleShape,
            modifier = actionModifier,
        ) {
            Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                Icon(icon, contentDescription = null)
            }
        }
    } else {
        IconButton(onClick = onClick, enabled = enabled, modifier = actionModifier) {
            Icon(icon, contentDescription = null)
        }
    }
}
@Composable
private fun LoadMoreButton(loading: Boolean, isTv: Boolean, onClick: () -> Unit) {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .padding(top = RivuneSpacing.sm),
        contentAlignment = Alignment.Center,
    ) {
        RivuneSecondaryButton(
            label = stringResource(R.string.folder_load_more),
            onClick = onClick,
            loading = loading,
            isTv = isTv,
        )
    }
}
@Composable
private fun InlineStatus(
    loading: Boolean,
    failure: UiFailure?,
    onRetry: () -> Unit,
    isTv: Boolean,
    loadingLabel: String,
) {
    val motionPolicy = LocalRivuneMotionPolicy.current
    when {
        loading -> Row(
            modifier = Modifier
                .fillMaxWidth()
                .heightIn(min = if (isTv) ViewerTvTarget else ViewerPhoneTarget)
                .semantics { liveRegion = LiveRegionMode.Polite },
            horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.sm),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            if (motionPolicy.ambientAnimations) {
                CircularProgressIndicator(
                    modifier = Modifier.size(RivuneSpacing.lg),
                    strokeWidth = RivuneDimensions.focusRing,
                )
            }
            Text(
                text = loadingLabel,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                style = MaterialTheme.typography.bodyMedium,
            )
        }
        failure != null -> {
            Surface(
                modifier = Modifier
                    .fillMaxWidth()
                    .semantics { liveRegion = LiveRegionMode.Assertive },
                color = MaterialTheme.colorScheme.errorContainer,
                shape = RivuneShapes.medium,
            ) {
                Column(
                    modifier = Modifier.padding(RivuneSpacing.md),
                    verticalArrangement = Arrangement.spacedBy(RivuneSpacing.sm),
                ) {
                    Row(
                        horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.sm),
                        verticalAlignment = Alignment.Top,
                    ) {
                        Icon(
                            Icons.Rounded.ErrorOutline,
                            contentDescription = null,
                            tint = MaterialTheme.colorScheme.onErrorContainer,
                        )
                        Text(
                            text = viewerFailureMessage(failure),
                            modifier = Modifier.weight(1f),
                            color = MaterialTheme.colorScheme.onErrorContainer,
                            style = MaterialTheme.typography.bodyMedium,
                        )
                    }
                    RivuneTextButton(
                        label = stringResource(R.string.viewer_retry),
                        onClick = onRetry,
                        isTv = isTv,
                    )
                }
            }
            Spacer(Modifier.height(RivuneSpacing.md))
        }
    }
}
@Composable
private fun InlineWarning(message: String) {
    RivuneFunctionalSurface(
        modifier = Modifier
            .fillMaxWidth()
            .semantics { liveRegion = LiveRegionMode.Polite },
        shape = RivuneShapes.small,
        contentPadding = PaddingValues(RivuneSpacing.md),
    ) {
        Text(
            text = message,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            style = MaterialTheme.typography.bodyMedium,
        )
    }
}
@Composable
private fun InlineEmpty(title: String, body: String, modifier: Modifier = Modifier) {
    Column(
        modifier = modifier
            .fillMaxWidth()
            .padding(horizontal = RivuneSpacing.xs, vertical = RivuneSpacing.md),
        verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
    ) {
        Text(
            text = title,
            modifier = Modifier.semantics { heading() },
            style = MaterialTheme.typography.titleMedium,
        )
        Text(
            text = body,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            style = MaterialTheme.typography.bodyMedium,
        )
    }
}

@Composable
private fun PlaybackProgressSummary(positionSeconds: Int, durationSeconds: Int) {
    if (durationSeconds <= 0) return
    val fraction = (positionSeconds.toFloat() / durationSeconds).coerceIn(0f, 1f)
    val percent = (fraction * 100).toInt()
    val progressDescription = stringResource(R.string.viewer_progress_percent, percent)
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .semantics(mergeDescendants = true) { stateDescription = progressDescription },
        horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.sm),
    ) {
        Text(
            text = stringResource(R.string.viewer_progress_time, positionSeconds / 60, durationSeconds / 60),
            modifier = Modifier.weight(1f),
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            style = MaterialTheme.typography.bodySmall,
        )
        Text(
            text = progressDescription,
            color = MaterialTheme.colorScheme.primary,
            style = MaterialTheme.typography.labelMedium,
        )
    }
}

@Composable
private fun CalendarMonthButton(
    icon: ImageVector,
    label: String,
    isTv: Boolean,
    onClick: () -> Unit,
) {
    if (isTv) {
        RivuneTextButton(label = label, onClick = onClick, isTv = true, icon = icon)
    } else {
        IconButton(
            onClick = onClick,
            modifier = Modifier.size(RivuneDimensions.touchTarget).semantics { contentDescription = label },
        ) {
            Icon(icon, contentDescription = null, tint = MaterialTheme.colorScheme.primary)
        }
    }
}


private fun androidx.compose.foundation.lazy.LazyListScope.inlineStatusItems(
    loading: Boolean,
    failure: UiFailure?,
    onRetry: () -> Unit,
    isTv: Boolean,
    loadingLabel: String,
) {
    if (loading || failure != null) {
        item(key = "viewer-status") {
            InlineStatus(loading = loading, failure = failure, onRetry = onRetry, isTv = isTv, loadingLabel = loadingLabel)
        }
    }
}


private fun localizedDate(value: String, locale: Locale): String = try {
    LocalDate.parse(value, DateTimeFormatter.ISO_LOCAL_DATE)
        .format(DateTimeFormatter.ofLocalizedDate(FormatStyle.MEDIUM).withLocale(locale))
} catch (_: DateTimeParseException) {
    value
}

@Composable
private fun viewerFailureMessage(failure: UiFailure): String = stringResource(
    when (failure) {
        UiFailure.SERVER_INVALID -> R.string.error_invalid_server
        UiFailure.SERVER_UNREACHABLE -> R.string.error_network
        UiFailure.PROTOCOL_INCOMPATIBLE -> R.string.error_incompatible_server
        UiFailure.SETUP_REQUIRED -> R.string.error_setup_required
        UiFailure.DEVICE_LIMIT -> R.string.error_device_limit
        UiFailure.PLAYBACK -> R.string.viewer_error_playback
        UiFailure.ACTION -> R.string.viewer_error_action
        UiFailure.PAIRING_START -> R.string.error_pairing_start
        UiFailure.PAIRING_LIMIT -> R.string.error_pairing_limit
        UiFailure.PAIRING_EXPIRED -> R.string.error_pairing_expired
        UiFailure.PAIRING_FAILED -> R.string.error_pairing_failed
        UiFailure.PROFILE_PIN_INVALID -> R.string.error_pin
        UiFailure.PROFILE_PIN_RATE_LIMITED -> R.string.error_pin_rate_limited
        UiFailure.PROFILE_UNAVAILABLE -> R.string.error_profile
        UiFailure.CONTENT_LOAD -> R.string.error_content_load
        UiFailure.SESSION_EXPIRED -> R.string.error_session_expired
        UiFailure.NO_PROFILES -> R.string.error_no_profiles
        UiFailure.LOGOUT_FAILED -> R.string.error_logout_failed
        UiFailure.UNKNOWN -> R.string.error_generic
    },
)

@Composable
private fun mediaTypeLabel(mediaType: String): String = stringResource(
    when (mediaType.lowercase(Locale.ROOT)) {
        "movie" -> R.string.viewer_movie
        "series", "tv" -> R.string.viewer_series_single
        "season" -> R.string.viewer_season
        "episode" -> R.string.viewer_episode
        else -> R.string.viewer_title
    },
)

private fun ViewerTab.titleResource(): Int = when (this) {
    ViewerTab.HOME -> R.string.viewer_home
    ViewerTab.SEARCH -> R.string.viewer_search
    ViewerTab.LIBRARY -> R.string.viewer_library
    ViewerTab.CALENDAR -> R.string.viewer_calendar
}

private fun ViewerTab.icon(): ImageVector = when (this) {
    ViewerTab.HOME -> Icons.Rounded.Home
    ViewerTab.SEARCH -> Icons.Rounded.Search
    ViewerTab.LIBRARY -> Icons.Rounded.VideoLibrary
    ViewerTab.CALENDAR -> Icons.Rounded.CalendarMonth
}
