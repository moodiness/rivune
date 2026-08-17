package io.rivune.app

import android.os.Build
import android.widget.ImageView
import androidx.annotation.DrawableRes
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.clickable
import androidx.compose.foundation.border
import androidx.compose.foundation.focusable
import androidx.compose.foundation.gestures.scrollBy
import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.rememberPagerState
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
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.requiredWidth
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.itemsIndexed
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
import androidx.compose.material.icons.rounded.SkipNext
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
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.withFrameNanos
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.rememberCoroutineScope
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
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import coil.compose.AsyncImage
import androidx.compose.ui.window.DialogProperties
import androidx.compose.ui.viewinterop.AndroidView
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
import io.rivune.api.SeasonSummary
import io.rivune.app.ui.components.RivuneCinematicBackground
import io.rivune.app.ui.components.RivuneFunctionalSurface
import io.rivune.app.ui.components.RivuneArtwork
import io.rivune.app.ui.components.RivuneFocusSurface
import io.rivune.app.ui.components.RivunePrimaryButton
import io.rivune.app.ui.components.RivuneSecondaryButton
import io.rivune.app.ui.components.RivuneSkeleton
import io.rivune.app.ui.components.RivuneSectionHeading
import io.rivune.app.ui.components.RivuneTextButton
import io.rivune.app.ui.components.RivuneTestTags
import io.rivune.app.ui.components.RivuneTextField
import io.rivune.app.ui.theme.LocalRivuneMotionPolicy
import io.rivune.app.ui.theme.RivuneBreakpoints
import io.rivune.app.ui.theme.RivuneDimensions
import io.rivune.app.ui.theme.RivuneElevation
import io.rivune.app.ui.theme.RivuneShapes
import io.rivune.app.ui.theme.RivuneSpacing
import io.rivune.app.ui.theme.RivuneMotion
import io.rivune.app.ui.theme.finiteAnimationSpec
import io.rivune.app.ui.theme.rivuneAccentHasReadableContrast
import java.time.LocalDate
import java.time.YearMonth
import java.time.format.DateTimeFormatter
import java.time.format.DateTimeParseException
import java.time.format.FormatStyle
import java.util.Locale
import java.util.UUID
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlin.math.ceil

private val ViewerPhoneTarget = RivuneDimensions.touchTarget
private val ViewerTvTarget = RivuneDimensions.touchTargetTv
private val ViewerCardGap = RivuneSpacing.md
private val ViewerMediaRowGap = RivuneSpacing.sm
private const val ViewerHeroScrimAlpha = 0.96f
private const val ViewerLogoWidthFraction = 0.62f
private const val ViewerHeroLogoAspectRatio = 2.6f
private const val ViewerHeroTopScrimAlpha = 0.50f
private const val ViewerHeroLandscapeMidScrimAlpha = 0.72f
private const val ViewerHeroControlAlpha = 0.84f
private const val ViewerDetailBackdropScrimAlpha = 0.72f
private val ViewerSourceRailMinWidth = 360.dp
private val ViewerSourceRailMaxWidth = 440.dp
private const val ViewerSourceRailWidthFraction = 0.365f
private val ViewerSourceRailMaxHeight = 680.dp
private val ViewerSourceScrollbarWidth = RivuneSpacing.xxs
private val ViewerSourceScrollbarMinimumThumb = RivuneSpacing.xl
private const val ViewerPhoneHeroHeightFraction = 0.82f
private const val ViewerHeroAutoplayMillis = 8_000L
private val ViewerPhoneHeroBottomFade = 220.dp
private val ViewerPhoneHeroMinHeight = 360.dp
private val ViewerPhoneHeroMaxHeight = 760.dp
private val ViewerLandscapeHeroMinHeight = 400.dp
private val ViewerLandscapeHeroMaxHeight = 480.dp
private val ViewerDetailOverviewMaxHeight = 96.dp
private val ViewerPhoneDetailOverviewMaxHeight = 144.dp
private val ViewerEpisodeCopyHeight = RivuneSpacing.display + RivuneSpacing.display + RivuneSpacing.md
private val ViewerAccountDialogWidthTv = 400.dp
private const val ViewerSkeletonTitleFraction = 0.84f
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
private const val PreferredPlayerMedia3Key = "rivune:media3"
private const val PreferredPlayerMpvKey = "rivune:mpv"
private const val RivuneSourceUrl = "https://github.com/moodiness/rivune"
private const val RivuneReleasesUrl = "$RivuneSourceUrl/releases/latest"
private const val RivuneIssuesUrl = "$RivuneSourceUrl/issues/new/choose"
private const val RivuneLicenseUrl = "$RivuneSourceUrl/blob/main/clients/android/app/src/main/assets/legal/LICENSE.txt"
private const val RivuneNoticeUrl = "$RivuneSourceUrl/blob/main/clients/android/app/src/main/assets/legal/THIRD_PARTY_NOTICES.txt"
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
    onPreferredEmbeddedPlayer: (EmbeddedPlayerPreference) -> Unit,
    onAnimationPreference: (AnimationPreference) -> Unit,
    onAccentColor: (Int) -> Unit,
    onFrameRateMatching: (FrameRateMatchingPreference) -> Unit,
    onVideoAspect: (VideoAspectPreference) -> Unit,
    onWifiQuality: (NetworkQualityPreference) -> Unit,
    onMobileQuality: (NetworkQualityPreference) -> Unit,
    onAutomaticallyShowStreams: (Boolean) -> Unit,
    onAutoSkipIntro: (Boolean) -> Unit,
    onAutoSkipRecap: (Boolean) -> Unit,
    onAutoSkipOutro: (Boolean) -> Unit,
    onChangeServer: () -> Unit,
    onOpenExternalUrl: (String) -> Unit,
    onCopyDiagnostics: () -> Unit,
    onExportLogs: () -> Unit,
) {
    val viewer = state.viewer
    val openedCollection = state.openedCollectionId?.let { id -> state.collections.firstOrNull { it.id == id } }
    when {
        viewer.player != null -> {
            val playerFailure = viewer.playerFailure
                ?.takeIf { it.matches(viewer.player) }
                ?.failure
            BackHandler(onBack = viewModel::closePlayer)
            if (viewer.player.externalPlayer == null || playerFailure != null) {
                RivunePlayerScreen(
                    presentation = viewer.player,
                    failure = playerFailure,
                    isTv = state.isTv,
                    onProgress = viewModel::reportPlayerProgress,
                    onPlaybackEnded = viewModel::playerPlaybackEnded,
                    onNext = viewModel::playNextEpisode,
                    onClose = viewModel::closePlayer,
                    onPlaybackError = { failure ->
                        viewModel.playerFailed(viewer.player.key, viewer.player.sessionId, failure)
                    },
                    onRetry = viewModel::retryFailedPlayer,
                    onStartOver = viewModel::restartFailedPlayer,
                    onChooseSource = viewModel::chooseAnotherPlaybackSource,
                    frameRateMatching = appPreferences.frameRateMatching,
                    videoAspect = appPreferences.videoAspect,
                    autoSkipIntro = appPreferences.autoSkipIntro,
                    autoSkipRecap = appPreferences.autoSkipRecap,
                    autoSkipOutro = appPreferences.autoSkipOutro,
                )
            } else {
                RivuneExternalPlayerScreen(
                    presentation = viewer.player,
                    isTv = state.isTv,
                    onResult = viewModel::externalPlaybackFinished,
                    onClose = viewModel::closePlayer,
                    onLaunchFailure = {
                        viewModel.playerFailed(
                            viewer.player.key,
                            viewer.player.sessionId,
                            PlayerEngineFailure(0L, fallbackEligible = false),
                        )
                    },
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
                onPreferredEmbeddedPlayer = onPreferredEmbeddedPlayer,
                onAnimationPreference = onAnimationPreference,
                onAccentColor = onAccentColor,
                onFrameRateMatching = onFrameRateMatching,
                onVideoAspect = onVideoAspect,
                onWifiQuality = onWifiQuality,
                onMobileQuality = onMobileQuality,
                onAutomaticallyShowStreams = onAutomaticallyShowStreams,
                onAutoSkipIntro = onAutoSkipIntro,
                onAutoSkipRecap = onAutoSkipRecap,
                onAutoSkipOutro = onAutoSkipOutro,
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
                automaticallyShowStreams = appPreferences.automaticallyShowStreams,
                isTv = state.isTv,
                artworkUrl = viewModel::artworkUrl,
                onOpenExternalUrl = onOpenExternalUrl,
                onBack = viewModel::backDetail,
                onSeason = viewModel::selectSeason,
                onEpisode = viewModel::openEpisode,
                onPlay = { viewModel.playMedia() },
                onToggleLibrary = viewModel::toggleLibrary,
                onToggleWatched = viewModel::toggleWatched,
                externalPlayers = state.externalPlayers,
                onSelectSource = viewModel::selectPlaybackSource,
                onChooseTarget = viewModel::choosePlaybackTarget,
                onDismissTarget = viewModel::dismissPlaybackTarget,
                onDismissSources = viewModel::dismissSourcePicker,
                onRefreshSources = viewModel::refreshPlaybackSources,
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
            onPlayMedia = viewModel::openAndPlayMedia,
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
    onPlayMedia: (MediaTarget) -> Unit,
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
                                    useTabletDock && state.viewer.selectedTab != ViewerTab.HOME -> Modifier
                                        .navigationBarsPadding()
                                        .padding(top = ViewerTabletDockContentInset)
                                    useTabletDock -> Modifier.navigationBarsPadding()
                                    else -> Modifier
                                        .navigationBarsPadding()
                                        .padding(bottom = RivuneDimensions.bottomBar)
                                },
                            ),
                    ) {
                        when (state.viewer.selectedTab) {
                            ViewerTab.HOME -> HomeRoot(
                                collections = state.collections,
                                heroSlides = state.viewer.heroSlides,
                                continueWatching = state.viewer.continueWatching,
                                loading = state.viewer.loading,
                                failure = state.viewer.inlineFailure,
                                isTv = state.isTv,
                                artworkUrl = artworkUrl,
                                onOpenFolder = onOpenFolder,
                                onOpenCollection = onOpenCollection,
                                onMedia = onMedia,
                                onPlayMedia = onPlayMedia,
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
    INTRODB(
        R.string.preferences_category_introdb,
        R.string.preferences_category_introdb_body,
        Icons.Rounded.SkipNext,
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
    onPreferredEmbeddedPlayer: (EmbeddedPlayerPreference) -> Unit,
    onAnimationPreference: (AnimationPreference) -> Unit,
    onAccentColor: (Int) -> Unit,
    onFrameRateMatching: (FrameRateMatchingPreference) -> Unit,
    onVideoAspect: (VideoAspectPreference) -> Unit,
    onWifiQuality: (NetworkQualityPreference) -> Unit,
    onMobileQuality: (NetworkQualityPreference) -> Unit,
    onAutomaticallyShowStreams: (Boolean) -> Unit,
    onAutoSkipIntro: (Boolean) -> Unit,
    onAutoSkipRecap: (Boolean) -> Unit,
    onAutoSkipOutro: (Boolean) -> Unit,
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
                                    selectedEmbedded = deviceSettings.embeddedPlayerPreference,
                                    externalPlayers = externalPlayers,
                                    isTv = isTv,
                                    onSelect = onPreferredPlayer,
                                    onSelectEmbedded = onPreferredEmbeddedPlayer,
                                )
                            }
                            item {
                                DeviceBooleanPreferenceCard(
                                    title = stringResource(R.string.preferences_auto_show_streams),
                                    description = stringResource(R.string.preferences_auto_show_streams_body),
                                    value = deviceSettings.automaticallyShowStreams,
                                    isTv = isTv,
                                    onSelect = onAutomaticallyShowStreams,
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
                        PreferenceCategory.VIDEO -> settings?.let { currentSettings ->
                            item {
                                val value = currentSettings.maximumResolution ?: "auto"
                                val choices = listOf(
                                    "auto" to stringResource(R.string.viewer_resolution_auto),
                                    "2160p" to stringResource(R.string.viewer_resolution_2160p),
                                    "1080p" to stringResource(R.string.viewer_resolution_1080p),
                                    "720p" to stringResource(R.string.viewer_resolution_720p),
                                    "480p" to stringResource(R.string.viewer_resolution_480p),
                                )
                                PreferenceChoiceCard(
                                    title = stringResource(R.string.viewer_maximum_resolution),
                                    description = serverPreferenceDescription(
                                        stringResource(R.string.viewer_maximum_resolution_body),
                                        state.sources?.maximumResolution,
                                        choices.first { it.first == value }.second,
                                    ),
                                    selected = serverPreferenceSelection(value, state.sources?.maximumResolution),
                                    options = listOf(ServerPreferenceInheritKey to stringResource(R.string.preferences_use_server_value)) + choices,
                                    enabled = state.canEdit && !loading,
                                    disabledDescription = disabledDescription,
                                    isTv = isTv,
                                    onSelect = {
                                        onUpdate(ProfileSettingsUpdate(maximumResolution = stringPreferencePatch(it)))
                                    },
                                )
                            }
                            item {
                                val value = currentSettings.preferDirectPlay ?: true
                                val choices = listOf(
                                    "true" to stringResource(R.string.preferences_option_on),
                                    "false" to stringResource(R.string.preferences_option_off),
                                )
                                PreferenceChoiceCard(
                                    title = stringResource(R.string.viewer_prefer_direct_play),
                                    description = serverPreferenceDescription(
                                        stringResource(R.string.viewer_prefer_direct_play_body),
                                        state.sources?.preferDirectPlay,
                                        choices.first { it.first == value.toString() }.second,
                                    ),
                                    selected = serverPreferenceSelection(value.toString(), state.sources?.preferDirectPlay),
                                    options = listOf(ServerPreferenceInheritKey to stringResource(R.string.preferences_use_server_value)) + choices,
                                    enabled = state.canEdit && !loading,
                                    disabledDescription = disabledDescription,
                                    isTv = isTv,
                                    onSelect = {
                                        onUpdate(ProfileSettingsUpdate(preferDirectPlay = booleanPreferencePatch(it)))
                                    },
                                )
                            }
                            item {
                                val available = currentSettings.allowTranscoding == true
                                val status = stringResource(
                                    if (available) R.string.viewer_transcoding_available else R.string.viewer_transcoding_unavailable,
                                )
                                PreferenceChoiceCard(
                                    title = stringResource(R.string.viewer_transcoding),
                                    description = serverPreferenceDescription(
                                        stringResource(R.string.viewer_transcoding_body),
                                        state.sources?.allowTranscoding,
                                        status,
                                    ),
                                    selected = available.toString(),
                                    options = listOf(available.toString() to status),
                                    enabled = false,
                                    disabledDescription = stringResource(R.string.viewer_transcoding_body),
                                    isTv = isTv,
                                    onSelect = {},
                                )
                            }
                            item {
                                val value = currentSettings.autoplayNextEpisode ?: true
                                val choices = listOf(
                                    "true" to stringResource(R.string.preferences_option_on),
                                    "false" to stringResource(R.string.preferences_option_off),
                                )
                                PreferenceChoiceCard(
                                    title = stringResource(R.string.viewer_autoplay_next_episode),
                                    description = serverPreferenceDescription(
                                        stringResource(R.string.viewer_autoplay_next_episode_body),
                                        state.sources?.autoplayNextEpisode,
                                        choices.first { it.first == value.toString() }.second,
                                    ),
                                    selected = serverPreferenceSelection(value.toString(), state.sources?.autoplayNextEpisode),
                                    options = listOf(ServerPreferenceInheritKey to stringResource(R.string.preferences_use_server_value)) + choices,
                                    enabled = state.canEdit && !loading,
                                    disabledDescription = disabledDescription,
                                    isTv = isTv,
                                    onSelect = {
                                        onUpdate(ProfileSettingsUpdate(autoplayNextEpisode = booleanPreferencePatch(it)))
                                    },
                                )
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
                        PreferenceCategory.INTRODB -> {
                            settings?.let { currentSettings ->
                            item {
                                ServerBooleanPreferenceCard(
                                    title = stringResource(R.string.preferences_skip_intro),
                                    description = stringResource(R.string.preferences_skip_intro_body),
                                    value = currentSettings.skipIntroEnabled ?: true,
                                    source = state.sources?.skipIntroEnabled,
                                    enabled = state.canEdit && !loading,
                                    disabledDescription = disabledDescription,
                                    isTv = isTv,
                                    onSelect = {
                                        onUpdate(ProfileSettingsUpdate(skipIntroEnabled = booleanPreferencePatch(it)))
                                    },
                                )
                            }
                            item {
                                ServerBooleanPreferenceCard(
                                    title = stringResource(R.string.preferences_skip_recap),
                                    description = stringResource(R.string.preferences_skip_recap_body),
                                    value = currentSettings.skipRecapEnabled ?: true,
                                    source = state.sources?.skipRecapEnabled,
                                    enabled = state.canEdit && !loading,
                                    disabledDescription = disabledDescription,
                                    isTv = isTv,
                                    onSelect = {
                                        onUpdate(ProfileSettingsUpdate(skipRecapEnabled = booleanPreferencePatch(it)))
                                    },
                                )
                            }
                            item {
                                ServerBooleanPreferenceCard(
                                    title = stringResource(R.string.preferences_skip_outro),
                                    description = stringResource(R.string.preferences_skip_outro_body),
                                    value = currentSettings.skipOutroEnabled ?: true,
                                    source = state.sources?.skipOutroEnabled,
                                    enabled = state.canEdit && !loading,
                                    disabledDescription = disabledDescription,
                                    isTv = isTv,
                                    onSelect = {
                                        onUpdate(ProfileSettingsUpdate(skipOutroEnabled = booleanPreferencePatch(it)))
                                    },
                                )
                            }
                            }
                            item {
                                DeviceBooleanPreferenceCard(
                                    title = stringResource(R.string.preferences_auto_skip_intro),
                                    description = stringResource(R.string.preferences_auto_skip_intro_body),
                                    value = deviceSettings.autoSkipIntro,
                                    isTv = isTv,
                                    onSelect = onAutoSkipIntro,
                                )
                            }
                            item {
                                DeviceBooleanPreferenceCard(
                                    title = stringResource(R.string.preferences_auto_skip_recap),
                                    description = stringResource(R.string.preferences_auto_skip_recap_body),
                                    value = deviceSettings.autoSkipRecap,
                                    isTv = isTv,
                                    onSelect = onAutoSkipRecap,
                                )
                            }
                            item {
                                DeviceBooleanPreferenceCard(
                                    title = stringResource(R.string.preferences_auto_skip_outro),
                                    description = stringResource(R.string.preferences_auto_skip_outro_body),
                                    value = deviceSettings.autoSkipOutro,
                                    isTv = isTv,
                                    onSelect = onAutoSkipOutro,
                                )
                            }
                        }
                        PreferenceCategory.AUDIO -> settings?.let { currentSettings ->
                            item {
                                LanguagePreferenceCard(
                                    title = stringResource(R.string.viewer_audio_language),
                                    description = stringResource(R.string.viewer_language_body),
                                    selected = currentSettings.audioLanguage ?: "auto",
                                    source = state.sources?.audioLanguage,
                                    automatic = true,
                                    enabled = state.canEdit && !loading,
                                    disabledDescription = disabledDescription,
                                    isTv = isTv,
                                    onSelect = {
                                        onUpdate(ProfileSettingsUpdate(audioLanguage = stringPreferencePatch(it)))
                                    },
                                )
                            }
                            item {
                                LanguagePreferenceCard(
                                    title = stringResource(R.string.viewer_subtitle_language),
                                    description = stringResource(R.string.viewer_language_body),
                                    selected = currentSettings.subtitleLanguage ?: "auto",
                                    source = state.sources?.subtitleLanguage,
                                    automatic = true,
                                    enabled = state.canEdit && !loading,
                                    disabledDescription = disabledDescription,
                                    isTv = isTv,
                                    onSelect = {
                                        onUpdate(ProfileSettingsUpdate(subtitleLanguage = stringPreferencePatch(it)))
                                    },
                                )
                            }
                            item {
                                LanguagePreferenceCard(
                                    title = stringResource(R.string.viewer_forced_subtitle_language),
                                    description = stringResource(R.string.viewer_forced_subtitle_language_body),
                                    selected = currentSettings.forcedSubtitleLanguage ?: "off",
                                    source = state.sources?.forcedSubtitleLanguage,
                                    automatic = false,
                                    enabled = state.canEdit && !loading,
                                    disabledDescription = disabledDescription,
                                    isTv = isTv,
                                    onSelect = {
                                        onUpdate(ProfileSettingsUpdate(forcedSubtitleLanguage = stringPreferencePatch(it)))
                                    },
                                )
                            }
                        }
                        PreferenceCategory.METADATA -> settings?.let { currentSettings ->
                            item {
                                LanguagePreferenceCard(
                                    title = stringResource(R.string.viewer_metadata_language),
                                    description = stringResource(R.string.viewer_metadata_language_body),
                                    selected = currentSettings.metadataLanguage ?: "auto",
                                    source = state.sources?.metadataLanguage,
                                    automatic = true,
                                    enabled = state.canEdit && !loading,
                                    disabledDescription = disabledDescription,
                                    isTv = isTv,
                                    onSelect = {
                                        onUpdate(ProfileSettingsUpdate(metadataLanguage = stringPreferencePatch(it)))
                                    },
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
    selectedEmbedded: EmbeddedPlayerPreference,
    externalPlayers: List<ExternalPlayerApp>,
    isTv: Boolean,
    onSelect: (PreferredPlayer) -> Unit,
    onSelectEmbedded: (EmbeddedPlayerPreference) -> Unit,
) {
    val selectedKey = preferredPlayerKey(selected, selectedEmbedded)
    val installedOptions = externalPlayers
        .distinctBy(ExternalPlayerApp::packageName)
        .map { player -> preferredPlayerKey(PreferredPlayer.External(player.packageName), selectedEmbedded) to player.label }
    val missingSelection = (selected as? PreferredPlayer.External)
        ?.takeIf { preferred -> externalPlayers.none { it.packageName == preferred.packageName } }
        ?.let { preferred ->
            preferredPlayerKey(preferred, selectedEmbedded) to stringResource(
                R.string.preferences_player_unavailable,
                preferred.packageName,
            )
        }
    val options = buildList {
        add(preferredPlayerKey(PreferredPlayer.Ask, selectedEmbedded) to stringResource(R.string.preferences_player_ask))
        add(preferredPlayerKey(PreferredPlayer.Rivune, EmbeddedPlayerPreference.AUTOMATIC) to stringResource(R.string.viewer_player_rivune))
        add(PreferredPlayerMedia3Key to stringResource(R.string.viewer_player_media3))
        add(PreferredPlayerMpvKey to stringResource(R.string.viewer_player_mpv))
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
                PreferredPlayerRivuneKey -> onSelectEmbedded(EmbeddedPlayerPreference.AUTOMATIC)
                PreferredPlayerMedia3Key -> onSelectEmbedded(EmbeddedPlayerPreference.MEDIA3)
                PreferredPlayerMpvKey -> onSelectEmbedded(EmbeddedPlayerPreference.MPV)
                else -> value.removePrefix(PreferredPlayerExternalPrefix)
                    .takeIf { value.startsWith(PreferredPlayerExternalPrefix) && it.isNotBlank() }
                    ?.let { onSelect(PreferredPlayer.External(it)) }
            }
        },
    )
}

private fun preferredPlayerKey(
    player: PreferredPlayer,
    embedded: EmbeddedPlayerPreference,
): String = when (player) {
    PreferredPlayer.Ask -> PreferredPlayerAskKey
    PreferredPlayer.Rivune -> when (embedded) {
        EmbeddedPlayerPreference.AUTOMATIC -> PreferredPlayerRivuneKey
        EmbeddedPlayerPreference.MEDIA3 -> PreferredPlayerMedia3Key
        EmbeddedPlayerPreference.MPV -> PreferredPlayerMpvKey
    }
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

private const val ServerPreferenceInheritKey = "__inherit__"

private fun serverPreferenceSelection(value: String, source: String?): String =
    if (source == "profile") value else ServerPreferenceInheritKey

private fun stringPreferencePatch(value: String): PatchField<String> =
    if (value == ServerPreferenceInheritKey) PatchField.Null else PatchField.Value(value)

private fun booleanPreferencePatch(value: String): PatchField<Boolean> = when (value) {
    ServerPreferenceInheritKey -> PatchField.Null
    "true" -> PatchField.Value(true)
    "false" -> PatchField.Value(false)
    else -> PatchField.Omitted
}

@Composable
private fun serverPreferenceDescription(description: String, source: String?, effectiveValue: String): String {
    val sourceLabel = stringResource(
        when (source) {
            "profile" -> R.string.preferences_source_profile
            "instance" -> R.string.preferences_source_instance
            "device" -> R.string.preferences_source_device
            else -> R.string.preferences_source_default
        },
    )
    return "$description\n$sourceLabel · $effectiveValue"
}

@Composable
private fun ServerBooleanPreferenceCard(
    title: String,
    description: String,
    value: Boolean,
    source: String?,
    enabled: Boolean,
    disabledDescription: String,
    isTv: Boolean,
    onSelect: (String) -> Unit,
) {
    val choices = listOf(
        "true" to stringResource(R.string.preferences_option_on),
        "false" to stringResource(R.string.preferences_option_off),
    )
    PreferenceChoiceCard(
        title = title,
        description = serverPreferenceDescription(
            description,
            source,
            choices.first { it.first == value.toString() }.second,
        ),
        selected = serverPreferenceSelection(value.toString(), source),
        options = listOf(ServerPreferenceInheritKey to stringResource(R.string.preferences_use_server_value)) + choices,
        enabled = enabled,
        disabledDescription = disabledDescription,
        isTv = isTv,
        onSelect = onSelect,
    )
}

@Composable
private fun DeviceBooleanPreferenceCard(
    title: String,
    description: String,
    value: Boolean,
    isTv: Boolean,
    onSelect: (Boolean) -> Unit,
) {
    val choices = listOf(
        "true" to stringResource(R.string.preferences_option_on),
        "false" to stringResource(R.string.preferences_option_off),
    )
    PreferenceChoiceCard(
        title = title,
        description = description,
        selected = value.toString(),
        options = choices,
        enabled = true,
        disabledDescription = "",
        isTv = isTv,
        onSelect = { onSelect(it == "true") },
    )
}

@Composable
private fun LanguagePreferenceCard(
    title: String,
    description: String,
    selected: String,
    source: String?,
    automatic: Boolean,
    enabled: Boolean,
    disabledDescription: String,
    isTv: Boolean,
    onSelect: (String) -> Unit,
) {
    val keyboardController = LocalSoftwareKeyboardController.current
    val standardOptions = buildList {
        add(ServerPreferenceInheritKey to stringResource(R.string.preferences_use_server_value))
        add(
            if (automatic) {
                "auto" to stringResource(R.string.viewer_language_auto)
            } else {
                "off" to stringResource(R.string.viewer_language_off)
            },
        )
        add("en" to stringResource(R.string.viewer_language_english))
        add("fr" to stringResource(R.string.viewer_language_french))
        add("es" to stringResource(R.string.viewer_language_spanish))
        add("de" to stringResource(R.string.viewer_language_german))
        add("it" to stringResource(R.string.viewer_language_italian))
        add("pt" to stringResource(R.string.viewer_language_portuguese))
        add("ja" to stringResource(R.string.viewer_language_japanese))
    }
    val options = if (standardOptions.none { it.first == selected }) {
        standardOptions + (selected to selected)
    } else {
        standardOptions
    }
    val effectiveLabel = options.firstOrNull { it.first == selected }?.second ?: selected
    val selectedKey = serverPreferenceSelection(selected, source)
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
        description = serverPreferenceDescription(description, source, effectiveLabel),
        selected = selectedKey,
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
        color = MaterialTheme.colorScheme.surfaceContainer.copy(alpha = 0.64f),
        contentColor = MaterialTheme.colorScheme.onSurface,
        tonalElevation = RivuneElevation.flat,
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
    heroSlides: List<HomeHeroSlide>,
    continueWatching: List<MediaTarget>,
    loading: ViewerLoading?,
    failure: UiFailure?,
    isTv: Boolean,
    artworkUrl: (String?) -> String?,
    onOpenFolder: (java.util.UUID, CollectionFolder) -> Unit,
    onOpenCollection: (java.util.UUID) -> Unit,
    onMedia: (MediaTarget) -> Unit,
    onPlayMedia: (MediaTarget) -> Unit,
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

    BoxWithConstraints(modifier = Modifier.fillMaxSize()) {
        val padding = viewerHorizontalPadding(maxWidth, isTv)
        val isPhoneHero = !isTv && maxWidth < RivuneBreakpoints.medium
        val heroHeight = if (isPhoneHero) {
            (maxHeight * ViewerPhoneHeroHeightFraction).coerceIn(
                ViewerPhoneHeroMinHeight,
                ViewerPhoneHeroMaxHeight,
            )
        } else {
            ((maxWidth - padding * 2) / (21f / 9f)).coerceIn(
                ViewerLandscapeHeroMinHeight,
                ViewerLandscapeHeroMaxHeight,
            )
        }
    LazyColumn(
        modifier = Modifier.fillMaxSize(),
        contentPadding = PaddingValues(
            start = padding,
            end = padding,
            top = if (viewerUsesTabletDock(maxWidth, isTv)) {
                ViewerTabletDockContentInset + RivuneSpacing.xs
            } else {
                RivuneSpacing.xs
            },
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
        if (heroSlides.isNotEmpty()) {
            item(key = "home-hero") {
                HomeHeroCarousel(
                    slides = heroSlides,
                    isPhone = isPhoneHero,
                    isTv = isTv,
                    artworkUrl = artworkUrl,
                    onMedia = onMedia,
                    onPlayMedia = onPlayMedia,
                    modifier = if (isPhoneHero) {
                        Modifier
                            .requiredWidth(maxWidth)
                            .offset(x = -padding)
                    } else {
                        Modifier.fillMaxWidth()
                    },
                    height = heroHeight,
                )
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
            val visibleFolders = collection.folders
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
private fun HomeHeroCarousel(
    slides: List<HomeHeroSlide>,
    isPhone: Boolean,
    isTv: Boolean,
    artworkUrl: (String?) -> String?,
    onMedia: (MediaTarget) -> Unit,
    onPlayMedia: (MediaTarget) -> Unit,
    height: Dp,
    modifier: Modifier = Modifier,
) {
    val slideKeys = remember(slides) { slides.map { "${it.item.mediaType}:${it.item.id}" } }
    val pageCount = if (slides.size > 1) Int.MAX_VALUE else 1
    val initialPage = if (slides.size > 1) {
        val midpoint = Int.MAX_VALUE / 2
        midpoint - midpoint.mod(slides.size)
    } else {
        0
    }
    val pagerState = rememberPagerState(initialPage = initialPage) { pageCount }
    val motionPolicy = LocalRivuneMotionPolicy.current
    val scope = rememberCoroutineScope()
    var navigationGeneration by remember { mutableStateOf(0) }
    val currentIndex = pagerState.currentPage.mod(slides.size)
    val currentSlide = slides[currentIndex]
    val carouselDescription = stringResource(
        R.string.home_hero_carousel_description,
        currentSlide.item.title,
        currentIndex + 1,
        slides.size,
    )
    val pageDescription = stringResource(
        R.string.home_hero_slide_position,
        currentSlide.item.title,
        currentIndex + 1,
        slides.size,
    )

    LaunchedEffect(slideKeys) {
        pagerState.scrollToPage(initialPage)
    }
    LaunchedEffect(
        motionPolicy.ambientAnimations,
        slideKeys,
        pagerState.settledPage,
        navigationGeneration,
    ) {
        if (!motionPolicy.ambientAnimations || slides.size <= 1) return@LaunchedEffect
        delay(ViewerHeroAutoplayMillis)
        if (!pagerState.isScrollInProgress) {
            pagerState.animateScrollToPage(pagerState.currentPage + 1)
        }
    }

    fun navigateTo(page: Int) {
        navigationGeneration += 1
        scope.launch { pagerState.animateScrollToPage(page) }
    }

    Box(
        modifier = modifier
            .height(height)
            .then(if (isPhone) Modifier else Modifier.clip(RivuneShapes.extraLarge))
            .semantics {
                contentDescription = carouselDescription
                stateDescription = pageDescription
            },
    ) {
        HorizontalPager(
            state = pagerState,
            key = { page -> "$page:${slideKeys[page.mod(slides.size)]}" },
            modifier = Modifier.fillMaxSize(),
        ) { page ->
            HomeHeroSlideContent(
                slide = slides[page.mod(slides.size)],
                isPhone = isPhone,
                isTv = isTv,
                artworkUrl = artworkUrl,
                onMedia = onMedia,
                onPlayMedia = onPlayMedia,
            )
        }
        if (slides.size > 1) {
            if (isPhone) {
                HomeHeroPhoneIndicators(
                    count = slides.size,
                    selectedIndex = currentIndex,
                    modifier = Modifier
                        .align(Alignment.BottomCenter)
                        .padding(bottom = RivuneSpacing.md),
                )
            } else {
                HomeHeroLandscapeControls(
                    slides = slides,
                    currentIndex = currentIndex,
                    isTv = isTv,
                    onPrevious = { navigateTo(pagerState.currentPage - 1) },
                    onNext = { navigateTo(pagerState.currentPage + 1) },
                    onSelect = { index -> navigateTo(pagerState.currentPage - currentIndex + index) },
                    modifier = Modifier
                        .align(Alignment.BottomEnd)
                        .padding(if (isTv) RivuneSpacing.xxl else RivuneSpacing.xl),
                )
            }
        }
    }
}

private fun heroFirstGenre(item: CollectionItem): String? {
    val raw = item.raw as? kotlinx.serialization.json.JsonObject ?: return null
    return heroGenreLabel(raw["genres"]) ?: heroGenreLabel(raw["genre"])
}

private fun heroGenreLabel(value: kotlinx.serialization.json.JsonElement?): String? = when (value) {
    is kotlinx.serialization.json.JsonPrimitive -> value.content
        .substringBefore(',')
        .trim()
        .takeIf { it.isNotEmpty() && !it.equals("null", ignoreCase = true) }
    is kotlinx.serialization.json.JsonArray -> value.firstNotNullOfOrNull(::heroGenreLabel)
    is kotlinx.serialization.json.JsonObject -> heroGenreLabel(value["name"])
        ?: heroGenreLabel(value["title"])
    else -> null
}

@Composable
private fun HomeHeroSlideContent(
    slide: HomeHeroSlide,
    isPhone: Boolean,
    isTv: Boolean,
    artworkUrl: (String?) -> String?,
    onMedia: (MediaTarget) -> Unit,
    onPlayMedia: (MediaTarget) -> Unit,
) {
    val item = slide.item
    val target = remember(item) { item.toMediaTarget() }
    val backdrop = sequenceOf(item.backgroundUrl, slide.fallbackBackdropUrl, item.posterUrl)
        .firstOrNull { !it.isNullOrBlank() }
    val logo = sequenceOf(item.logoUrl, slide.fallbackLogoUrl)
        .firstOrNull { !it.isNullOrBlank() }
    val resolvedLogo = artworkUrl(logo)
    var logoFailed by remember(resolvedLogo) { mutableStateOf(false) }
    val release = item.releaseInfo?.takeIf(String::isNotBlank)
        ?: item.released?.takeIf(String::isNotBlank)
    val description = item.description?.takeIf(String::isNotBlank)
    val mediaType = mediaTypeLabel(item.mediaType)
    val rating = item.voteAverage
        ?.takeIf { it > 0.0 }
        ?.let { String.format(Locale.getDefault(), "★ %.1f", it) }
    val metadata = if (isPhone) {
        listOfNotNull(mediaType, heroFirstGenre(item), release)
    } else {
        listOfNotNull(release, rating, mediaType)
    }.joinToString(" · ")

    Box(modifier = Modifier.fillMaxSize()) {
        RivuneArtwork(
            model = artworkUrl(backdrop),
            fallback = item.title,
            contentDescription = null,
            modifier = Modifier.fillMaxSize(),
        )
        if (isPhone) {
            Box(
                modifier = Modifier
                    .fillMaxSize()
                    .background(
                        Brush.verticalGradient(
                            colors = listOf(
                                MaterialTheme.colorScheme.background.copy(alpha = ViewerHeroTopScrimAlpha),
                                Color.Transparent,
                            ),
                        ),
                    ),
            )
            Box(
                modifier = Modifier
                    .align(Alignment.BottomCenter)
                    .fillMaxWidth()
                    .height(ViewerPhoneHeroBottomFade)
                    .background(
                        Brush.verticalGradient(
                            colors = listOf(
                                Color.Transparent,
                                MaterialTheme.colorScheme.background.copy(alpha = ViewerHeroScrimAlpha),
                            ),
                        ),
                    ),
            )
        } else {
            Box(
                modifier = Modifier
                    .fillMaxSize()
                    .background(
                        Brush.horizontalGradient(
                            colors = listOf(
                                MaterialTheme.colorScheme.background.copy(alpha = ViewerHeroScrimAlpha),
                                MaterialTheme.colorScheme.background.copy(alpha = ViewerHeroLandscapeMidScrimAlpha),
                                Color.Transparent,
                            ),
                        ),
                    )
                    .background(
                        Brush.verticalGradient(
                            colors = listOf(
                                Color.Transparent,
                                MaterialTheme.colorScheme.background.copy(alpha = ViewerHeroLandscapeMidScrimAlpha),
                            ),
                        ),
                    ),
            )
        }

        Column(
            modifier = if (isPhone) {
                Modifier
                    .align(Alignment.BottomCenter)
                    .fillMaxWidth()
                    .padding(
                        start = RivuneSpacing.xl,
                        end = RivuneSpacing.xl,
                        bottom = RivuneSpacing.display,
                    )
            } else {
                Modifier
                    .align(Alignment.BottomStart)
                    .widthIn(max = RivuneDimensions.contentMax)
                    .padding(
                        start = if (isTv) RivuneSpacing.xxl else RivuneSpacing.xl,
                        end = if (isTv) RivuneSpacing.xxl else RivuneSpacing.xl,
                        bottom = if (isTv) RivuneSpacing.xxl else RivuneSpacing.xl,
                    )
            },
            horizontalAlignment = if (isPhone) Alignment.CenterHorizontally else Alignment.Start,
            verticalArrangement = Arrangement.spacedBy(
                if (isPhone || !isTv) RivuneSpacing.sm else RivuneSpacing.md,
            ),
        ) {
            if (resolvedLogo != null && !logoFailed) {
                AsyncImage(
                    model = resolvedLogo,
                    contentDescription = null,
                    contentScale = ContentScale.Fit,
                    onError = { logoFailed = true },
                    modifier = if (isPhone) {
                        Modifier
                            .fillMaxWidth(ViewerLogoWidthFraction)
                            .aspectRatio(ViewerHeroLogoAspectRatio)
                    } else {
                        Modifier
                            .fillMaxWidth(ViewerLogoWidthFraction)
                            .height(if (isTv) RivuneDimensions.buttonHeightTv else RivuneDimensions.buttonHeight)
                    },
                )
            } else {
                Text(
                    text = item.title,
                    modifier = Modifier.semantics { heading() },
                    color = MaterialTheme.colorScheme.onBackground,
                    style = if (isPhone) {
                        MaterialTheme.typography.headlineMedium
                    } else {
                        MaterialTheme.typography.headlineLarge
                    },
                    textAlign = if (isPhone) TextAlign.Center else TextAlign.Start,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            if (metadata.isNotBlank()) {
                Text(
                    text = metadata,
                    color = MaterialTheme.colorScheme.onBackground,
                    style = if (isTv) MaterialTheme.typography.bodyLarge else MaterialTheme.typography.bodyMedium,
                    textAlign = if (isPhone) TextAlign.Center else TextAlign.Start,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            if (!isPhone && description != null) {
                Text(
                    text = description,
                    color = MaterialTheme.colorScheme.onBackground,
                    style = if (isTv) MaterialTheme.typography.bodyLarge else MaterialTheme.typography.bodyMedium,
                    maxLines = 3,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            if (isPhone) {
                RivunePrimaryButton(
                    label = stringResource(R.string.home_hero_view_details),
                    onClick = { onMedia(target) },
                    icon = Icons.Rounded.Info,
                    compact = true,
                )
            } else {
                Row(horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.sm)) {
                    if (target.mediaType != "series") {
                        DetailLabeledAction(
                            label = stringResource(R.string.viewer_play),
                            icon = Icons.Rounded.PlayArrow,
                            onClick = { onPlayMedia(target) },
                            enabled = true,
                            isTv = isTv,
                            progressDescription = null,
                        )
                    }
                    DetailLabeledAction(
                        label = stringResource(R.string.home_hero_more_info),
                        icon = Icons.Rounded.Info,
                        onClick = { onMedia(target) },
                        enabled = true,
                        isTv = isTv,
                        progressDescription = null,
                    )
                }
            }
        }
    }
}

@Composable
private fun HomeHeroPhoneIndicators(
    count: Int,
    selectedIndex: Int,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier.clearAndSetSemantics { },
        horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        repeat(count) { index ->
            Box(
                modifier = Modifier
                    .width(if (index == selectedIndex) RivuneSpacing.xxl else RivuneSpacing.xs)
                    .height(RivuneSpacing.xs)
                    .clip(RivuneShapes.pill)
                    .background(
                        if (index == selectedIndex) MaterialTheme.colorScheme.primary
                        else MaterialTheme.colorScheme.onBackground.copy(alpha = ViewerHeroTopScrimAlpha),
                    ),
            )
        }
    }
}

@Composable
private fun HomeHeroLandscapeControls(
    slides: List<HomeHeroSlide>,
    currentIndex: Int,
    isTv: Boolean,
    onPrevious: () -> Unit,
    onNext: () -> Unit,
    onSelect: (Int) -> Unit,
    modifier: Modifier = Modifier,
) {
    val previousIndex = (currentIndex - 1 + slides.size).mod(slides.size)
    val indicatorTarget = if (isTv) ViewerTvTarget else ViewerPhoneTarget
    val indicatorViewportWidth = minOf(
        RivuneSpacing.xxxl + RivuneSpacing.md * (slides.size - 1),
        RivuneDimensions.contentMax / 2,
    )
    val nextIndex = (currentIndex + 1).mod(slides.size)
    Row(
        modifier = modifier,
        horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        HomeHeroArrowControl(
            icon = Icons.Rounded.ChevronLeft,
            description = stringResource(R.string.home_hero_previous, slides[previousIndex].item.title),
            isTv = isTv,
            onClick = onPrevious,
        )
        LazyRow(
            modifier = Modifier.width(indicatorViewportWidth),
            horizontalArrangement = Arrangement.Center,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            itemsIndexed(
                items = slides,
                key = { index, slide -> "hero-indicator:$index:${slide.item.mediaType}:${slide.item.id}" },
            ) { index, slide ->
                val selected = index == currentIndex
                val selectDescription = stringResource(
                    R.string.home_hero_select,
                    slide.item.title,
                    index + 1,
                    slides.size,
                )
                RivuneFocusSurface(
                    onClick = { onSelect(index) },
                    selected = selected,
                    isTv = isTv,
                    idleColor = Color.Transparent,
                    selectedColor = Color.Transparent,
                    focusedColor = MaterialTheme.colorScheme.primaryContainer,
                    showSelectionBorder = false,
                    shape = RivuneShapes.pill,
                    modifier = Modifier
                        .width(if (selected) RivuneSpacing.xxxl else RivuneSpacing.md)
                        .height(indicatorTarget)
                        .semantics {
                            contentDescription = selectDescription
                            this.selected = selected
                        },
                ) {
                    Box(
                        modifier = Modifier.fillMaxSize(),
                        contentAlignment = Alignment.Center,
                    ) {
                        Box(
                            modifier = Modifier
                                .width(if (selected) RivuneSpacing.xxl else RivuneSpacing.xs)
                                .height(RivuneSpacing.xs)
                                .clip(RivuneShapes.pill)
                                .background(
                                    if (selected) MaterialTheme.colorScheme.primary
                                    else MaterialTheme.colorScheme.onBackground.copy(alpha = ViewerHeroTopScrimAlpha),
                                ),
                        )
                    }
                }
            }
        }
        HomeHeroArrowControl(
            icon = Icons.Rounded.ChevronRight,
            description = stringResource(R.string.home_hero_next, slides[nextIndex].item.title),
            isTv = isTv,
            onClick = onNext,
        )
    }
}

@Composable
private fun HomeHeroArrowControl(
    icon: ImageVector,
    description: String,
    isTv: Boolean,
    onClick: () -> Unit,
) {
    RivuneFocusSurface(
        onClick = onClick,
        isTv = isTv,
        idleColor = MaterialTheme.colorScheme.background.copy(alpha = ViewerHeroControlAlpha),
        focusedColor = MaterialTheme.colorScheme.primaryContainer,
        shape = CircleShape,
        modifier = Modifier
            .size(if (isTv) ViewerTvTarget else ViewerPhoneTarget)
            .semantics { contentDescription = description },
    ) {
        Icon(
            imageVector = icon,
            contentDescription = null,
            tint = MaterialTheme.colorScheme.onBackground,
            modifier = Modifier.size(RivuneDimensions.iconMedium),
        )
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


internal fun displayableSeasons(seasons: List<SeasonSummary>): List<SeasonSummary> =
    seasons.filter { it.episodeCount > 0 }.sortedBy { it.seasonNumber }

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun DetailScreen(
    state: ViewerState,
    automaticallyShowStreams: Boolean,
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
    onChooseTarget: (PlaybackTargetSelection) -> Unit,
    onDismissTarget: () -> Unit,
    onDismissSources: () -> Unit,
    onRefreshSources: () -> Unit,
    onRetry: () -> Unit,
) {
    val detail = checkNotNull(state.detail)
    val movie = detail.movie
    val series = detail.series
    val season = detail.season
    val title = when {
        movie != null -> movie.title
        season != null && series != null -> "${series.name} · ${season.name}"
        season != null -> season.name
        series != null -> series.name
        else -> detail.target.title
    }
    val overview = movie?.overview ?: season?.overview ?: series?.overview ?: detail.target.description
    val backdrop = artworkUrl(
        movie?.backdropUrl ?: season?.backdropUrl ?: series?.backdropUrl ?: detail.target.backgroundUrl ?: detail.target.posterUrl,
    )
    val cast = detail.cast
    val orderedSeasons = remember(series?.seasons) { displayableSeasons(series?.seasons.orEmpty()) }
    val trailer = if (season == null) detail.trailers.firstOrNull()
    else detail.seasonTrailers.firstOrNull() ?: detail.trailers.firstOrNull()
    val trailerUrl = trailer?.youtubeId?.takeIf(String::isNotBlank)?.let { youtubeId ->
        "https://www.youtube.com/watch?v=${android.net.Uri.encode(youtubeId)}"
    }
    val onTrailer = trailerUrl?.let { url -> { onOpenExternalUrl(url) } }
    val backFocus = remember { FocusRequester() }
    val playFocus = remember { FocusRequester() }
    val detailListState = rememberLazyListState()
    val hasPlayAction = detail.target.mediaType != "series" && !automaticallyShowStreams
    val showStatus = (!state.sourcePickerVisible && state.inlineFailure != null) ||
        state.loading == ViewerLoading.DETAIL || state.loading == ViewerLoading.SEASON ||
        state.loading == ViewerLoading.ACTION
    val backLabel = stringResource(R.string.viewer_back)
    LaunchedEffect(isTv, detail.target.id, season?.id, state.inlineFailure, state.sourcePickerVisible) {
        if (isTv && !state.sourcePickerVisible) {
            if (hasPlayAction && state.inlineFailure == null) playFocus.requestFocus() else backFocus.requestFocus()
            withFrameNanos { }
        }
    }
    LaunchedEffect(detail.target.id, season?.id) {
        detailListState.scrollToItem(0)
    }
    val detailLoadingLabel = stringResource(
        when (state.loading) {
            ViewerLoading.SEASON -> R.string.viewer_loading_season
            ViewerLoading.ACTION -> R.string.viewer_saving_change
            else -> R.string.viewer_loading_detail
        },
    )
    val motionPolicy = LocalRivuneMotionPolicy.current
    RivuneCinematicBackground {
        BoxWithConstraints(modifier = Modifier.fillMaxSize()) {
            val padding = viewerHorizontalPadding(maxWidth, isTv)
            val wideHero = isTv || maxWidth >= RivuneBreakpoints.expanded
            val mediumLayout = !isTv && maxWidth >= RivuneBreakpoints.medium
            RivuneArtwork(
                model = backdrop,
                fallback = title,
                contentDescription = null,
                modifier = Modifier.fillMaxSize(),
                alignment = Alignment.TopCenter,
            )
            Box(
                modifier = Modifier
                    .fillMaxSize()
                    .background(
                        MaterialTheme.colorScheme.background.copy(alpha = ViewerDetailBackdropScrimAlpha),
                    ),
            )
            LazyColumn(
                state = detailListState,
                modifier = Modifier.fillMaxSize().navigationBarsPadding(),
                contentPadding = PaddingValues(bottom = RivuneSpacing.huge),
                verticalArrangement = Arrangement.spacedBy(if (isTv) RivuneSpacing.xl else RivuneSpacing.lg),
            ) {
                item(key = "hero") {
                    Box(modifier = Modifier.fillMaxWidth()) {
                        DetailSummary(
                            detail = detail,
                            title = title,
                            overview = overview,
                            isTv = isTv,
                            isWide = wideHero,
                            actionsEnabled = state.loading == null,
                            showPlayAction = hasPlayAction,
                            actionLoading = state.loading == ViewerLoading.ACTION,
                            onPlay = onPlay,
                            playEnabled = state.sourcePicker != null || state.loading != ViewerLoading.SOURCES,
                            onToggleLibrary = onToggleLibrary,
                            onToggleWatched = onToggleWatched,
                            onTrailer = onTrailer,
                            playModifier = Modifier.focusRequester(playFocus),
                            modifier = Modifier
                                .padding(
                                    start = padding,
                                    end = padding,
                                    top = when {
                                        isTv -> RivuneSpacing.display + RivuneSpacing.md
                                        mediumLayout -> RivuneSpacing.display * 2 + RivuneSpacing.huge
                                        else -> RivuneSpacing.display + RivuneSpacing.huge
                                    },
                                    bottom = if (isTv) RivuneSpacing.xxxl else RivuneSpacing.lg,
                                )
                                .widthIn(max = RivuneDimensions.contentMaxTablet),
                        )
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
                                    state.loading == ViewerLoading.ACTION,
                                onRetry = onRetry,
                                isTv = isTv,
                                loadingLabel = detailLoadingLabel,
                            )
                        }
                    }
                }
                if (season == null && orderedSeasons.isNotEmpty()) {
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
                                val episodeAndRating = listOfNotNull(
                                    pluralStringResource(
                                        R.plurals.viewer_episode_count,
                                        summary.episodeCount,
                                        summary.episodeCount,
                                    ),
                                    summary.voteAverage.takeIf { it > 0.0 }
                                        ?.let { String.format(Locale.getDefault(), "★ %.1f/10", it) },
                                ).joinToString(" · ")
                                SeasonTile(
                                    title = summary.name,
                                    metadata = episodeAndRating,
                                    releaseDate = summary.airDate?.let { localizedDate(it, Locale.getDefault()) }.orEmpty(),
                                    imageUrl = artworkUrl(summary.posterUrl ?: summary.backdropUrl),
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
    AnimatedVisibility(
        visible = state.sourcePickerVisible,
        modifier = Modifier.fillMaxSize(),
        enter = fadeIn(
            animationSpec = motionPolicy.finiteAnimationSpec(RivuneMotion.fast),
        ),
        exit = fadeOut(
            animationSpec = motionPolicy.finiteAnimationSpec(RivuneMotion.fast),
        ),
    ) {
        state.sourcePicker?.let { picker ->
            SourcePickerOverlay(
                picker = picker,
                isTv = isTv,
                externalPlayers = externalPlayers,
                loading = state.loading == ViewerLoading.SOURCES || state.loading == ViewerLoading.PLAYER,
                enabled = state.loading == null,
                failure = state.inlineFailure,
                onBack = if (automaticallyShowStreams) onBack else onDismissSources,
                onRefresh = onRefreshSources,
                onSelectSource = onSelectSource,
                onChooseTarget = onChooseTarget,
                onDismissTarget = onDismissTarget,
                modifier = Modifier.fillMaxSize(),
            )
        }
    }
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
    showPlayAction: Boolean,
    playEnabled: Boolean,
    actionsEnabled: Boolean,
    actionLoading: Boolean,
    onPlay: () -> Unit,
    onToggleLibrary: () -> Unit,
    onToggleWatched: () -> Unit,
    onTrailer: (() -> Unit)?,
    playModifier: Modifier = Modifier,
    modifier: Modifier = Modifier,
) {
    val locale = Locale.getDefault()
    val movie = detail.movie
    val series = detail.series
    val season = detail.season
    val target = detail.target
    val rating = movie?.voteAverage ?: season?.voteAverage ?: series?.voteAverage ?: target.rating
    val episodeCoordinates = target.takeIf { it.mediaType == "episode" }?.let {
        listOfNotNull(
            it.seasonNumber?.let { number -> stringResource(R.string.viewer_season_number, number) },
            it.episodeNumber?.let { number -> stringResource(R.string.viewer_episode_label, number) },
        ).joinToString(" · ").takeIf(String::isNotBlank)
    }
    val primaryMetadata = listOfNotNull(
        (movie?.releaseDate ?: season?.airDate ?: series?.firstAirDate ?: target.releaseInfo)
            ?.let { localizedDate(it, locale) },
        (movie?.runtimeMinutes ?: target.runtimeMinutes)
            ?.takeIf { it > 0 }
            ?.let { stringResource(R.string.viewer_minutes, it) },
        rating?.takeIf { it > 0.0 }?.let { String.format(locale, "★ %.1f/10", it) },
        series?.status?.takeIf(String::isNotBlank),
    )
    val genres = (movie?.genres ?: series?.genres.orEmpty())
        .takeIf { it.isNotEmpty() }
        ?.joinToString(" · ") { it.name }
    val secondaryMetadata = when {
        season != null -> listOfNotNull(
            pluralStringResource(R.plurals.viewer_episode_count, season.episodes.size, season.episodes.size),
            genres,
        )
        else -> listOfNotNull(
            series?.numberOfSeasons?.takeIf { it > 0 }
                ?.let { pluralStringResource(R.plurals.viewer_season_count, it, it) },
            series?.numberOfEpisodes?.takeIf { it > 0 }
                ?.let { pluralStringResource(R.plurals.viewer_episode_count, it, it) },
            genres,
        )
    }
    val tagline = movie?.tagline ?: series?.tagline
    val seasonWatched = season?.episodes
        ?.takeIf { it.isNotEmpty() }
        ?.all { detail.episodeProgress[it.id]?.completed == true } == true
    val watched = if (season != null) seasonWatched else detail.progress?.completed == true
    val resumeProgress = detail.progress?.takeIf {
        detail.target.mediaType != "series" && it.positionSeconds > 0 && it.durationSeconds > 0 && !it.completed
    }
    val resumeProgressDescription = resumeProgress?.let { progress ->
        val fraction = (progress.positionSeconds.toFloat() / progress.durationSeconds).coerceIn(0f, 1f)
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
        episodeCoordinates?.let {
            Text(
                text = it,
                color = MaterialTheme.colorScheme.onSurface,
                style = if (isTv) MaterialTheme.typography.titleMedium else MaterialTheme.typography.titleSmall,
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
            if (showPlayAction) {
                DetailLabeledAction(
                    label = stringResource(if (resumeProgress != null) R.string.viewer_resume else R.string.viewer_play),
                    icon = Icons.Rounded.PlayArrow,
                    onClick = onPlay,
                    enabled = actionsEnabled && playEnabled,
                    isTv = isTv,
                    modifier = playModifier,
                    progressDescription = resumeProgressDescription,
                )
            }
            onTrailer?.let {
                DetailLabeledAction(
                    label = stringResource(R.string.viewer_trailer),
                    icon = Icons.Rounded.Theaters,
                    onClick = it,
                    enabled = actionsEnabled,
                    isTv = isTv,
                    progressDescription = null,
                )
            }
            if (detail.target.mediaType != "series" || season != null) {
                DetailLabeledAction(
                    icon = if (watched) Icons.Rounded.Visibility else Icons.Rounded.VisibilityOff,
                    label = stringResource(if (watched) R.string.viewer_mark_unwatched else R.string.viewer_mark_watched),
                    selected = watched,
                    enabled = actionsEnabled,
                    loading = actionLoading,
                    isTv = isTv,
                    onClick = onToggleWatched,
                    progressDescription = null,
                )
            }
            if (detail.target.mediaType != "episode") {
                DetailLabeledAction(
                    icon = if (detail.inLibrary) Icons.Rounded.Check else Icons.Rounded.LibraryAdd,
                    label = stringResource(if (detail.inLibrary) R.string.viewer_in_library else R.string.viewer_add_library),
                    selected = detail.inLibrary,
                    enabled = actionsEnabled,
                    loading = actionLoading,
                    isTv = isTv,
                    onClick = onToggleLibrary,
                    progressDescription = null,
                )
            }
        }
        if (!overview.isNullOrBlank()) {
            DetailOverview(
                overview = overview,
                isTv = isTv,
                isWide = isWide,
            )
        }
    }
}

@Composable
private fun DetailOverview(
    overview: String,
    isTv: Boolean,
    isWide: Boolean,
) {
    val scrollState = rememberScrollState()
    val scope = rememberCoroutineScope()
    val density = LocalDensity.current
    val scrollableDescription = stringResource(R.string.viewer_description_scrollable)
    val viewportHeight = if (isWide) ViewerDetailOverviewMaxHeight else ViewerPhoneDetailOverviewMaxHeight
    val scrollStep = with(density) { RivuneSpacing.display.toPx() }
    var focused by remember { mutableStateOf(false) }
    val scrollable = scrollState.maxValue > 0

    LaunchedEffect(overview) {
        scrollState.scrollTo(0)
    }

    BoxWithConstraints(
        modifier = Modifier
            .padding(top = if (isTv) RivuneSpacing.sm else RivuneSpacing.xs)
            .widthIn(max = RivuneDimensions.contentMax)
            .heightIn(max = viewportHeight)
            .then(
                if (isTv && scrollable) {
                    Modifier
                        .onFocusChanged { focused = it.isFocused }
                        .onPreviewKeyEvent { event ->
                            if (event.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
                            when (event.key) {
                                Key.DirectionUp -> if (scrollState.canScrollBackward) {
                                    scope.launch { scrollState.scrollBy(-scrollStep) }
                                    true
                                } else {
                                    false
                                }
                                Key.DirectionDown -> if (scrollState.canScrollForward) {
                                    scope.launch { scrollState.scrollBy(scrollStep) }
                                    true
                                } else {
                                    false
                                }
                                else -> false
                            }
                        }
                        .focusable()
                } else {
                    Modifier
                },
            )
            .then(
                if (focused) Modifier.border(RivuneDimensions.focusRing, MaterialTheme.colorScheme.primary, RivuneShapes.small)
                else Modifier,
            )
            .clip(RivuneShapes.small)
            .semantics(mergeDescendants = true) {
                if (scrollable) stateDescription = scrollableDescription
            },
    ) {
        Text(
            text = overview,
            modifier = Modifier
                .fillMaxWidth()
                .padding(end = if (scrollable) RivuneSpacing.sm else 0.dp)
                .verticalScroll(scrollState),
            color = MaterialTheme.colorScheme.onSurface,
            style = MaterialTheme.typography.bodyLarge,
        )
        if (scrollable) {
            val thumbHeight = RivuneSpacing.xl
            val travel = (maxHeight - thumbHeight).coerceAtLeast(0.dp)
            val progress = scrollState.value.toFloat() / scrollState.maxValue.toFloat()
            Box(
                modifier = Modifier
                    .align(Alignment.TopEnd)
                    .offset(y = travel * progress)
                    .width(2.dp)
                    .height(thumbHeight)
                    .clip(RivuneShapes.pill)
                    .background(MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.72f)),
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
    selected: Boolean = false,
    loading: Boolean = false,
    progressDescription: String?,
    modifier: Modifier = Modifier,
) {
    RivuneFocusSurface(
        onClick = onClick,
        enabled = enabled && !loading,
        isTv = isTv,
        selected = selected,
        idleColor = Color.Transparent,
        focusedColor = Color.Transparent,
        pressedColor = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.12f),
        showSelectionBorder = false,
        selectedColor = Color.Transparent,
        shape = RivuneShapes.pill,
        modifier = modifier
            .heightIn(min = if (isTv) ViewerTvTarget else ViewerPhoneTarget)
            .semantics {
                this.selected = selected
                if (loading) stateDescription = label
                else if (progressDescription != null) stateDescription = progressDescription
            },
    ) {
        Row(
            modifier = Modifier.padding(horizontal = if (isTv) RivuneSpacing.md else RivuneSpacing.sm),
            horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
            verticalAlignment = Alignment.CenterVertically,
        ) {
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
            Text(
                text = label,
                color = MaterialTheme.colorScheme.onSurface,
                style = if (isTv) MaterialTheme.typography.titleMedium else MaterialTheme.typography.labelLarge,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

@Composable
internal fun SourcePickerOverlay(
    picker: SourcePickerState,
    isTv: Boolean,
    externalPlayers: List<ExternalPlayerApp>,
    enabled: Boolean,
    loading: Boolean,
    failure: UiFailure?,
    onBack: () -> Unit,
    onRefresh: () -> Unit,
    onSelectSource: (PlaybackSourceOption) -> Unit,
    onChooseTarget: (PlaybackTargetSelection) -> Unit,
    onDismissTarget: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val firstSourceFocus = remember { FocusRequester() }
    val targetSource = picker.playerSource
    val targetPlayers = remember(targetSource, externalPlayers) {
        targetSource?.let { source ->
            ExternalPlaybackSupport(externalPlayers).playersFor(source.mode, source.protocol, source.container)
        }.orEmpty()
    }
    BackHandler(enabled = targetSource == null, onBack = onBack)
    BoxWithConstraints(
        modifier = modifier
            .fillMaxSize()
            .statusBarsPadding()
            .navigationBarsPadding()
            .imePadding(),
    ) {
        val compact = !isTv && maxWidth < RivuneBreakpoints.medium
        val horizontalMargin = if (compact) RivuneSpacing.sm else RivuneSpacing.huge
        val verticalMargin = if (compact) RivuneSpacing.sm else RivuneSpacing.xxl
        val availableWidth = maxWidth - horizontalMargin * 2
        val availableHeight = maxHeight - verticalMargin * 2
        val railWidth = if (compact) {
            availableWidth
        } else {
            (availableWidth * ViewerSourceRailWidthFraction)
                .coerceAtLeast(ViewerSourceRailMinWidth)
                .coerceAtMost(ViewerSourceRailMaxWidth)
                .coerceAtMost(availableWidth)
        }
        val railHeight = availableHeight.coerceAtMost(ViewerSourceRailMaxHeight)
        SourcePickerContent(
            picker = picker,
            isTv = isTv,
            enabled = enabled,
            loading = loading,
            failure = failure,
            firstSourceFocus = firstSourceFocus,
            onRefresh = onRefresh,
            onSelect = onSelectSource,
            modifier = Modifier
                .align(Alignment.CenterEnd)
                .padding(end = horizontalMargin)
                .width(railWidth)
                .height(railHeight),
        )
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
    enabled: Boolean,
    loading: Boolean,
    failure: UiFailure?,
    firstSourceFocus: FocusRequester,
    onRefresh: () -> Unit,
    onSelect: (PlaybackSourceOption) -> Unit,
    modifier: Modifier = Modifier,
) {
    var selectedAddonId by remember(picker.target.id, picker.target.resourceId, picker.titleId) {
        mutableStateOf<UUID?>(null)
    }
    val addonFilters = remember(picker.options) { playbackSourceAddonFilters(picker.options) }
    val filteredOptions = remember(picker.options, selectedAddonId) {
        selectedAddonId?.let { addonId -> picker.options.filter { it.addonId == addonId } } ?: picker.options
    }
    val sourceFooters = remember(picker.options) {
        picker.options.associate { option -> option.id to playbackSourceFooter(option) }
    }
    val listState = rememberLazyListState()
    val refreshFocus = remember { FocusRequester() }

    LaunchedEffect(loading, selectedAddonId, addonFilters) {
        if (!loading && selectedAddonId != null && addonFilters.none { (addonId, _) -> addonId == selectedAddonId }) {
            selectedAddonId = null
        }
    }


    LaunchedEffect(isTv, loading, picker.target.id, picker.target.resourceId, picker.titleId, selectedAddonId, filteredOptions.firstOrNull()?.id) {
        if (filteredOptions.isNotEmpty()) listState.scrollToItem(0)
        if (isTv && !loading) {
            withFrameNanos { }
            if (filteredOptions.isNotEmpty()) firstSourceFocus.requestFocus() else refreshFocus.requestFocus()
        }
    }

    Column(modifier = modifier) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
        ) {
            LazyRow(
                modifier = Modifier.weight(1f),
                horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
                contentPadding = PaddingValues(vertical = RivuneSpacing.xxs),
            ) {
                item(key = "all") {
                    SourceAddonFilterButton(
                        label = stringResource(R.string.viewer_library_all),
                        selected = selectedAddonId == null,
                        isTv = isTv,
                        enabled = enabled && !loading,
                        onClick = { selectedAddonId = null },
                    )
                }
                items(addonFilters, key = { (addonId, _) -> addonId }) { (addonId, label) ->
                    SourceAddonFilterButton(
                        label = label,
                        selected = selectedAddonId == addonId,
                        isTv = isTv,
                        enabled = enabled && !loading,
                        onClick = { selectedAddonId = addonId },
                    )
                }
            }
            SourceRefreshAction(
                enabled = enabled && !loading,
                loading = loading,
                isTv = isTv,
                onClick = onRefresh,
                modifier = Modifier.focusRequester(refreshFocus),
            )
        }
        InlineStatus(
            loading = loading,
            failure = failure,
            onRetry = onRefresh,
            isTv = isTv,
            loadingLabel = stringResource(R.string.viewer_loading_sources),
        )
        if (picker.partial) {
            InlineWarning(stringResource(R.string.viewer_sources_partial))
            Spacer(Modifier.height(RivuneSpacing.xs))
        }
        if (picker.options.isEmpty() && !loading && failure == null) {
            InlineEmpty(
                title = stringResource(R.string.viewer_sources_empty_title),
                body = stringResource(R.string.viewer_sources_empty_body),
            )
        } else if (picker.options.isNotEmpty()) {
            Box(modifier = Modifier.fillMaxWidth().weight(1f)) {
                LazyColumn(
                    state = listState,
                    modifier = Modifier
                        .fillMaxSize()
                        .padding(end = RivuneSpacing.sm)
                        .testTag(RivuneTestTags.SourcePickerList),
                    contentPadding = PaddingValues(vertical = RivuneSpacing.xs),
                    verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
                ) {
                    items(filteredOptions, key = PlaybackSourceOption::id) { option ->
                        RivuneFocusSurface(
                            onClick = { onSelect(option) },
                            enabled = enabled && !loading,
                            idleColor = MaterialTheme.colorScheme.surface.copy(alpha = 0.72f),
                            selectedColor = MaterialTheme.colorScheme.surfaceContainerHigh.copy(alpha = 0.82f),
                            focusedColor = MaterialTheme.colorScheme.surfaceContainerHighest.copy(alpha = 0.88f),
                            pressedColor = MaterialTheme.colorScheme.surfaceContainerHigh.copy(alpha = 0.92f),
                            restingBorderColor = MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.92f),
                            shape = RivuneShapes.small,
                            modifier = Modifier
                                .fillMaxWidth()
                                .then(
                                    if (option.id == filteredOptions.first().id) Modifier.focusRequester(firstSourceFocus)
                                    else Modifier,
                                ),
                        ) {
                            Row(
                                modifier = Modifier.padding(horizontal = RivuneSpacing.sm, vertical = RivuneSpacing.xs),
                                horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
                                verticalAlignment = Alignment.CenterVertically,
                            ) {
                                Column(modifier = Modifier.weight(1f)) {
                                    Text(
                                        text = option.name,
                                        style = MaterialTheme.typography.titleSmall,
                                    )
                                    val description = option.description?.takeIf(String::isNotBlank)
                                        ?: option.filename
                                            ?.takeIf(String::isNotBlank)
                                            ?.takeUnless { it == option.name }
                                    description?.let {
                                        Spacer(Modifier.height(RivuneSpacing.xs))
                                        Text(
                                            text = it,
                                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                                            style = MaterialTheme.typography.bodySmall,
                                        )
                                    }
                                    Spacer(Modifier.height(RivuneSpacing.xxs))
                                    Text(
                                        text = sourceFooters.getValue(option.id),
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                        style = MaterialTheme.typography.labelSmall,
                                    )
                                }
                                Icon(
                                    Icons.Rounded.ChevronRight,
                                    contentDescription = null,
                                    modifier = Modifier.size(RivuneDimensions.iconSmall),
                                    tint = MaterialTheme.colorScheme.primary,
                                )
                            }
                        }
                    }
                }
                SourceListScrollbar(
                    state = listState,
                    modifier = Modifier
                        .align(Alignment.CenterEnd)
                        .fillMaxHeight()
                        .width(ViewerSourceScrollbarWidth),
                )
            }
        }
    }
}

internal fun playbackSourceAddonFilters(options: List<PlaybackSourceOption>): List<Pair<UUID, String>> {
    val representativeOptions = LinkedHashMap<UUID, PlaybackSourceOption>()
    options.forEach { option ->
        val current = representativeOptions[option.addonId]
        if (current == null || (current.addonName.isNullOrBlank() && !option.addonName.isNullOrBlank())) {
            representativeOptions[option.addonId] = option
        }
    }
    val representatives = representativeOptions.values.toList()
    val baseLabels = representatives.map(::playbackSourceAddonLabel)
    val baseCounts = baseLabels.groupingBy(String::lowercase).eachCount()
    val candidateLabels = representatives.mapIndexed { index, option ->
        val label = baseLabels[index]
        if (baseCounts.getValue(label.lowercase()) == 1) {
            label
        } else {
            val manifest = option.manifestId.trim().takeIf {
                option.addonName.isNullOrBlank() && it.isNotEmpty() && !it.equals(label, ignoreCase = true)
            }
            "$label · ${manifest ?: option.addonId}"
        }
    }
    val candidateCounts = candidateLabels.groupingBy(String::lowercase).eachCount()
    return representatives.mapIndexed { index, option ->
        val candidate = candidateLabels[index]
        option.addonId to if (candidateCounts.getValue(candidate.lowercase()) == 1) {
            candidate
        } else {
            "$candidate · ${option.addonId}"
        }
    }
}

internal fun playbackSourceAddonLabel(option: PlaybackSourceOption): String =
    option.addonName?.trim()?.takeIf(String::isNotEmpty)
        ?: option.manifestId.trim().takeIf(String::isNotEmpty)
        ?: option.addonId.toString()

internal fun playbackSourceFooter(option: PlaybackSourceOption): String = buildString {
    fun appendPart(value: String?) {
        val part = value?.trim()?.takeIf(String::isNotEmpty) ?: return
        if (isNotEmpty()) append(" · ")
        append(part)
    }

    appendPart(playbackSourceAddonLabel(option))
    appendPart(option.mode?.name?.lowercase(Locale.ROOT)?.replace('_', ' '))
    appendPart(option.protocol.uppercase(Locale.ROOT))
    if (!option.container.equals(option.protocol, ignoreCase = true)) {
        appendPart(option.container?.uppercase(Locale.ROOT))
    }
}

@Composable
private fun SourceRefreshAction(
    enabled: Boolean,
    loading: Boolean,
    isTv: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val label = stringResource(R.string.home_refresh)
    val loadingLabel = stringResource(R.string.viewer_loading_sources)
    val target = if (isTv) ViewerTvTarget else ViewerPhoneTarget
    RivuneFocusSurface(
        onClick = onClick,
        enabled = enabled,
        isTv = isTv,
        idleColor = Color.Transparent,
        focusedColor = MaterialTheme.colorScheme.surfaceContainerHighest.copy(alpha = 0.88f),
        pressedColor = MaterialTheme.colorScheme.surfaceContainerHigh.copy(alpha = 0.92f),
        showSelectionBorder = false,
        shape = RivuneShapes.pill,
        modifier = modifier
            .size(target)
            .semantics {
                contentDescription = label
                if (loading) stateDescription = loadingLabel
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
                    imageVector = Icons.Rounded.Refresh,
                    contentDescription = null,
                    modifier = Modifier.size(RivuneDimensions.iconMedium),
                    tint = MaterialTheme.colorScheme.onSurface,
                )
            }
        }
    }
}

@Composable
private fun SourceAddonFilterButton(
    label: String,
    selected: Boolean,
    isTv: Boolean,
    enabled: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    var focused by remember { mutableStateOf(false) }
    val target = if (isTv) ViewerTvTarget else ViewerPhoneTarget
    val containerColor = when {
        focused -> MaterialTheme.colorScheme.surfaceContainerHighest.copy(alpha = 0.88f)
        selected -> MaterialTheme.colorScheme.primaryContainer.copy(alpha = 0.86f)
        else -> MaterialTheme.colorScheme.surface.copy(alpha = 0.64f)
    }
    val borderColor = if (focused) {
        MaterialTheme.colorScheme.primary
    } else {
        MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.92f)
    }
    RivuneFocusSurface(
        onClick = onClick,
        selected = selected,
        isTv = isTv,
        enabled = enabled,
        idleColor = Color.Transparent,
        selectedColor = Color.Transparent,
        focusedColor = Color.Transparent,
        pressedColor = Color.Transparent,
        showSelectionBorder = false,
        showFocusBorder = false,
        shape = RivuneShapes.pill,
        modifier = modifier
            .height(target)
            .widthIn(min = target)
            .onFocusChanged { focused = it.isFocused },
    ) {
        Box(
            modifier = Modifier.height(target),
            contentAlignment = Alignment.Center,
        ) {
            Surface(
                modifier = Modifier
                    .height(28.dp)
                    .widthIn(min = RivuneSpacing.xxxl),
                color = containerColor,
                contentColor = if (selected) MaterialTheme.colorScheme.onPrimaryContainer else MaterialTheme.colorScheme.onSurface,
                border = BorderStroke(
                    if (focused) RivuneDimensions.focusRing else RivuneDimensions.hairline,
                    borderColor,
                ),
                shape = RivuneShapes.pill,
            ) {
                Box(
                    modifier = Modifier.padding(horizontal = RivuneSpacing.xs),
                    contentAlignment = Alignment.Center,
                ) {
                    Text(
                        text = label,
                        maxLines = 1,
                        style = MaterialTheme.typography.labelSmall,
                    )
                }
            }
        }
    }
}

@Composable
private fun SourceListScrollbar(
    state: LazyListState,
    modifier: Modifier = Modifier,
) {
    val scrollable by remember(state) {
        derivedStateOf { state.canScrollBackward || state.canScrollForward }
    }
    if (!scrollable) return

    BoxWithConstraints(
        modifier = modifier
            .clip(RivuneShapes.pill)
            .background(MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.68f)),
    ) {
        val layoutInfo = state.layoutInfo
        val visibleItems = layoutInfo.visibleItemsInfo
        if (visibleItems.isNotEmpty() && layoutInfo.totalItemsCount > 0) {
            val firstItem = visibleItems.first()
            val lastItem = visibleItems.last()
            val itemSpan = lastItem.index - firstItem.index
            val averageItemExtent = if (itemSpan > 0) {
                (lastItem.offset - firstItem.offset).toFloat() / itemSpan.toFloat()
            } else {
                firstItem.size.toFloat()
            }.coerceAtLeast(1f)
            val viewportExtent = (layoutInfo.viewportEndOffset - layoutInfo.viewportStartOffset).toFloat()
            val estimatedContentExtent = averageItemExtent * layoutInfo.totalItemsCount
            val thumbFraction = (viewportExtent / estimatedContentExtent).coerceIn(0f, 1f)
            val thumbHeight = (maxHeight * thumbFraction)
                .coerceAtLeast(ViewerSourceScrollbarMinimumThumb)
                .coerceAtMost(maxHeight)
            val scrollExtent = (estimatedContentExtent - viewportExtent).coerceAtLeast(1f)
            val scrollPosition = state.firstVisibleItemIndex * averageItemExtent + state.firstVisibleItemScrollOffset
            val progress = when {
                !state.canScrollBackward -> 0f
                !state.canScrollForward -> 1f
                else -> (scrollPosition / scrollExtent).coerceIn(0f, 1f)
            }
            Box(
                modifier = Modifier
                    .align(Alignment.TopCenter)
                    .offset(y = (maxHeight - thumbHeight) * progress)
                    .fillMaxWidth()
                    .height(thumbHeight)
                    .clip(RivuneShapes.pill)
                    .background(MaterialTheme.colorScheme.primary.copy(alpha = 0.92f)),
            )
        }
    }
}

@Composable
private fun PickerHeading(
    title: String,
    isTv: Boolean,
    onDismiss: () -> Unit,
    dismissFocus: FocusRequester? = null,
) {
    val closeLabel = stringResource(R.string.viewer_close)
    val target = if (isTv) ViewerTvTarget else ViewerPhoneTarget
    Row(
        modifier = Modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
    ) {
        Text(
            text = title,
            modifier = Modifier
                .weight(1f)
                .semantics { heading() },
            style = if (isTv) MaterialTheme.typography.headlineMedium else MaterialTheme.typography.titleLarge,
        )
        RivuneFocusSurface(
            onClick = onDismiss,
            isTv = isTv,
            idleColor = Color.Transparent,
            focusedColor = MaterialTheme.colorScheme.surfaceContainerHighest,
            pressedColor = MaterialTheme.colorScheme.surfaceContainerHigh,
            showSelectionBorder = false,
            shape = RivuneShapes.pill,
            modifier = (dismissFocus?.let { Modifier.focusRequester(it) } ?: Modifier)
                .size(target)
                .semantics { contentDescription = closeLabel },
        ) {
            Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                Icon(
                    imageVector = Icons.Rounded.Close,
                    contentDescription = null,
                    modifier = Modifier.size(RivuneDimensions.iconMedium),
                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun PlaybackTargetDialog(
    source: PlaybackSourceOption,
    players: List<ExternalPlayerApp>,
    isTv: Boolean,
    onDismiss: () -> Unit,
    onChoose: (PlaybackTargetSelection) -> Unit,
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
        Column(modifier = modifier, verticalArrangement = Arrangement.spacedBy(RivuneSpacing.md)) {
            PickerHeading(
                title = stringResource(R.string.viewer_choose_player),
                isTv = isTv,
                onDismiss = onDismiss,
                dismissFocus = dismissFocus,
            )
            LazyColumn(
                modifier = Modifier.fillMaxWidth().weight(1f, fill = false),
                verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
            ) {
                if (hasRivuneTarget) {
                    item(key = "automatic") {
                        PlaybackTargetRow(
                            label = stringResource(R.string.viewer_player_automatic),
                            supporting = stringResource(R.string.viewer_player_automatic_body),
                            packageName = null,
                            embeddedIcon = EmbeddedPlaybackIcon.RIVUNE,
                            isTv = isTv,
                            onClick = { onChoose(PlaybackTargetSelection.Embedded(EmbeddedPlayerPreference.AUTOMATIC)) },
                            modifier = Modifier.focusRequester(firstTargetFocus),
                        )
                    }
                    item(key = "media3") {
                        PlaybackTargetRow(
                            label = stringResource(R.string.viewer_player_media3),
                            supporting = stringResource(R.string.viewer_player_media3_body),
                            packageName = null,
                            embeddedIcon = EmbeddedPlaybackIcon.MEDIA3,
                            isTv = isTv,
                            onClick = { onChoose(PlaybackTargetSelection.Embedded(EmbeddedPlayerPreference.MEDIA3)) },
                        )
                    }
                    item(key = "mpv") {
                        PlaybackTargetRow(
                            label = stringResource(R.string.viewer_player_mpv),
                            supporting = stringResource(R.string.viewer_player_mpv_body),
                            packageName = null,
                            embeddedIcon = EmbeddedPlaybackIcon.MPV,
                            isTv = isTv,
                            onClick = { onChoose(PlaybackTargetSelection.Embedded(EmbeddedPlayerPreference.MPV)) },
                        )
                    }
                }
                items(players, key = { it.packageName }) { player ->
                    PlaybackTargetRow(
                        label = player.label,
                        supporting = stringResource(R.string.viewer_player_external_body),
                        packageName = player.packageName,
                        embeddedIcon = null,
                        isTv = isTv,
                        onClick = { onChoose(PlaybackTargetSelection.External(player)) },
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
                containerColor = MaterialTheme.colorScheme.surfaceContainer,
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
                            .widthIn(max = 440.dp)
                            .fillMaxWidth()
                            .heightIn(max = contentHeight),
                        shape = RivuneShapes.extraLarge,
                        contentPadding = PaddingValues(if (isTv) RivuneSpacing.xl else RivuneSpacing.lg),
                    ) {
                        content(Modifier.fillMaxWidth())
                    }
                }
            }
        }
    }
}
private enum class EmbeddedPlaybackIcon(@DrawableRes val resourceId: Int) {
    RIVUNE(R.mipmap.ic_launcher),
    MEDIA3(R.drawable.media3_mark),
    MPV(R.drawable.mpv_mark),
}

@Composable
private fun PlaybackTargetRow(
    label: String,
    supporting: String,
    packageName: String?,
    embeddedIcon: EmbeddedPlaybackIcon?,
    isTv: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val iconTarget = if (isTv) RivuneSpacing.huge else RivuneSpacing.xxxl
    RivuneFocusSurface(
        onClick = onClick,
        isTv = isTv,
        idleColor = MaterialTheme.colorScheme.surfaceContainer,
        focusedColor = MaterialTheme.colorScheme.surfaceContainerHighest,
        pressedColor = MaterialTheme.colorScheme.surfaceContainerHigh,
        restingBorderColor = MaterialTheme.colorScheme.outlineVariant,
        shape = RivuneShapes.large,
        modifier = modifier.fillMaxWidth(),
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = RivuneSpacing.md, vertical = RivuneSpacing.sm),
            horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            PlayerApplicationIcon(
                packageName = packageName,
                embeddedIconResource = embeddedIcon?.resourceId,
                size = iconTarget,
            )
            Column(
                modifier = Modifier.weight(1f),
                verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xxs),
            ) {
                Text(label, style = MaterialTheme.typography.titleMedium)
                Text(
                    text = supporting,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    style = MaterialTheme.typography.bodyMedium,
                )
            }
            Icon(
                imageVector = Icons.Rounded.ChevronRight,
                contentDescription = null,
                modifier = Modifier.size(RivuneDimensions.iconMedium),
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun PlayerApplicationIcon(
    packageName: String?,
    @DrawableRes embeddedIconResource: Int?,
    size: Dp,
) {
    val context = LocalContext.current
    val externalIcon = remember(context.packageManager, packageName) {
        packageName?.let { name ->
            runCatching { context.packageManager.getApplicationIcon(name) }.getOrNull()
        }
    }
    AndroidView(
        factory = { imageContext ->
            ImageView(imageContext).apply {
                scaleType = ImageView.ScaleType.FIT_CENTER
                contentDescription = null
            }
        },
        modifier = Modifier
            .size(size)
            .clip(RivuneShapes.medium)
            .clearAndSetSemantics { },
        update = { imageView ->
            if (embeddedIconResource != null) {
                imageView.setImageResource(embeddedIconResource)
            } else {
                imageView.setImageDrawable(externalIcon)
            }
        },
    )
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
    metadata: String,
    releaseDate: String,
    imageUrl: String?,
    isTv: Boolean,
    onClick: () -> Unit,
) {
    val width = if (isTv) 176.dp else 148.dp
    RivuneFocusSurface(
        onClick = onClick,
        isTv = isTv,
        idleColor = Color.Transparent,
        shape = RivuneShapes.medium,
        modifier = Modifier.width(width),
    ) {
        Column(verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xs)) {
            RivuneArtwork(
                model = imageUrl,
                fallback = title,
                contentDescription = null,
                modifier = Modifier
                    .fillMaxWidth()
                    .aspectRatio(2f / 3f)
                    .clip(RivuneShapes.medium),
            )
            Column(
                modifier = Modifier.padding(horizontal = RivuneSpacing.xs, vertical = RivuneSpacing.xxs),
                verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xxs),
            ) {
                Text(title, maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.titleSmall)
                Text(
                    metadata,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    style = MaterialTheme.typography.bodySmall,
                )
                Text(
                    releaseDate,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    minLines = 1,
                    maxLines = 1,
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
    val primaryMetadata = listOfNotNull(
        runtimeMinutes?.takeIf { it > 0 }?.let { stringResource(R.string.viewer_minutes, it) },
        rating?.let { String.format(locale, "★ %.1f/10", it) },
    ).joinToString(" · ")
    val releaseDate = target.releaseInfo
        ?.takeIf(String::isNotBlank)
        ?.let { localizedDate(it, locale) }
        .orEmpty()
    val completed = progress?.completed == true
    val partialProgress = progress
        ?.takeIf { !it.completed && it.durationSeconds > 0 }
        ?.let { (it.positionSeconds.toFloat() / it.durationSeconds).coerceIn(0f, 1f) }
    val progressDescription = when {
        completed -> stringResource(R.string.viewer_watched)
        partialProgress != null -> stringResource(
            R.string.viewer_progress_percent,
            (partialProgress * 100).toInt(),
        )
        else -> null
    }

    RivuneFocusSurface(
        onClick = onClick,
        isTv = isTv,
        shape = RivuneShapes.medium,
        modifier = modifier.semantics(mergeDescendants = true) {
            progressDescription?.let { stateDescription = it }
        },
    ) {
        if (isTv) {
            Row(
                modifier = Modifier.fillMaxWidth().padding(RivuneSpacing.sm),
                horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                EpisodeArtwork(
                    title = target.title,
                    imageUrl = imageUrl,
                    completed = completed,
                    progress = partialProgress,
                    modifier = Modifier
                        .width(RivuneDimensions.landscapeCardWidthTv)
                        .aspectRatio(16f / 9f),
                )
                EpisodeCopy(
                    target = target,
                    primaryMetadata = primaryMetadata,
                    releaseDate = releaseDate,
                    isTv = true,
                    modifier = Modifier.weight(1f),
                )
                Icon(
                    Icons.Rounded.ChevronRight,
                    contentDescription = null,
                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        } else {
            Column {
                EpisodeArtwork(
                    title = target.title,
                    imageUrl = imageUrl,
                    completed = completed,
                    progress = partialProgress,
                    modifier = Modifier.fillMaxWidth().aspectRatio(16f / 9f),
                )
                EpisodeCopy(
                    target = target,
                    primaryMetadata = primaryMetadata,
                    releaseDate = releaseDate,
                    isTv = false,
                    modifier = Modifier
                        .fillMaxWidth()
                        .height(ViewerEpisodeCopyHeight)
                        .padding(RivuneSpacing.sm),
                )
            }
        }
    }
}

@Composable
private fun EpisodeArtwork(
    title: String,
    imageUrl: String?,
    completed: Boolean,
    progress: Float?,
    modifier: Modifier = Modifier,
) {
    Box(modifier = modifier.clip(RivuneShapes.small)) {
        RivuneArtwork(
            model = imageUrl,
            fallback = title,
            contentDescription = null,
            modifier = Modifier.fillMaxSize(),
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
        if (completed) {
            Icon(
                imageVector = Icons.Rounded.Check,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onPrimary,
                modifier = Modifier
                    .align(Alignment.TopEnd)
                    .padding(RivuneSpacing.xs)
                    .clip(CircleShape)
                    .background(MaterialTheme.colorScheme.primary)
                    .padding(RivuneSpacing.xxs)
                    .size(RivuneDimensions.iconSmall),
            )
        }
    }
}

@Composable
private fun EpisodeCopy(
    target: MediaTarget,
    primaryMetadata: String,
    releaseDate: String,
    isTv: Boolean,
    modifier: Modifier = Modifier,
) {
    Column(modifier = modifier) {
        Text(
            text = stringResource(R.string.viewer_episode_number, target.episodeNumber ?: 0, target.title),
            minLines = 2,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
            style = if (isTv) MaterialTheme.typography.titleLarge else MaterialTheme.typography.titleMedium,
        )
        Spacer(Modifier.weight(1f))
        Text(
            text = primaryMetadata,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            style = MaterialTheme.typography.labelLarge,
        )
        Spacer(Modifier.height(RivuneSpacing.xs))
        Text(
            text = releaseDate,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            style = MaterialTheme.typography.bodyMedium,
        )
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
        val mediumLayout = !isTv && maxWidth >= RivuneBreakpoints.medium
        val startPadding = if (isTv) {
            ViewerTvDockContentInset
        } else {
            viewerHorizontalPadding(maxWidth, false)
        }
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .heightIn(min = if (isTv) ViewerTvTarget else ViewerPhoneTarget)
                .padding(
                    start = startPadding,
                    end = viewerHorizontalPadding(maxWidth, isTv),
                    top = if (isTv || mediumLayout) RivuneSpacing.xxl else RivuneSpacing.md,
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
