package io.rivune.app

import androidx.activity.compose.BackHandler
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
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.automirrored.rounded.Logout
import androidx.compose.material.icons.rounded.AccountCircle
import androidx.compose.material.icons.rounded.Add
import androidx.compose.material.icons.rounded.CalendarMonth
import androidx.compose.material.icons.rounded.Check
import androidx.compose.material.icons.rounded.ChevronLeft
import androidx.compose.material.icons.rounded.ChevronRight
import androidx.compose.material.icons.rounded.ErrorOutline
import androidx.compose.material.icons.rounded.Home
import androidx.compose.material.icons.rounded.LibraryAdd
import androidx.compose.material.icons.rounded.Person
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material.icons.rounded.Refresh
import androidx.compose.material.icons.rounded.Search
import androidx.compose.material.icons.rounded.Settings
import androidx.compose.material.icons.rounded.VideoLibrary
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationBarItemDefaults
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.NavigationRailItemDefaults
import androidx.compose.material3.NavigationRail
import androidx.compose.material3.NavigationRailItem
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import io.rivune.api.CalendarEvent
import io.rivune.api.Collection
import io.rivune.api.CollectionFolder
import io.rivune.api.CollectionItem
import io.rivune.api.LibraryItem
import io.rivune.api.PlaybackProgress
import io.rivune.api.PlaybackSourceOption
import io.rivune.api.PatchField
import io.rivune.api.ProfileSettingsUpdate
import io.rivune.api.ResolvedCollectionFolder
import io.rivune.app.ui.components.RivuneArtwork
import io.rivune.app.ui.components.RivuneBrandMark
import io.rivune.app.ui.components.RivuneFocusSurface
import io.rivune.app.ui.components.RivunePrimaryButton
import io.rivune.app.ui.components.RivuneSecondaryButton
import io.rivune.app.ui.components.RivuneSkeleton
import io.rivune.app.ui.components.RivuneTextButton
import io.rivune.app.ui.components.RivuneTextField
import io.rivune.app.ui.theme.RivuneBreakpoints
import io.rivune.app.ui.theme.RivuneDimensions
import io.rivune.app.ui.theme.RivuneElevation
import io.rivune.app.ui.theme.RivuneShapes
import io.rivune.app.ui.theme.RivuneSpacing
import java.time.LocalDate
import java.time.YearMonth
import java.time.format.DateTimeFormatter
import java.time.format.DateTimeParseException
import java.time.format.FormatStyle
import java.util.Locale

private val ViewerCompactBreakpoint = RivuneBreakpoints.compact
private val ViewerPhonePadding = RivuneSpacing.md
private val ViewerTvPadding = RivuneSpacing.huge
private val ViewerPhoneTarget = RivuneDimensions.touchTarget
private val ViewerTvTarget = RivuneDimensions.touchTargetTv
private val ViewerCardGap = RivuneSpacing.md
private val ViewerPreferencesMaxWidth = RivuneDimensions.preferencesMax

@Composable
internal fun ViewerShell(
    state: RivuneUiState,
    viewModel: RivuneViewModel,
    updateState: AppUpdateState,
    onCheckForUpdates: () -> Unit,
) {
    val viewer = state.viewer
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
            )
        }
        viewer.detail != null -> {
            BackHandler(onBack = viewModel::backViewer)
            DetailScreen(
                state = viewer,
                isTv = state.isTv,
                artworkUrl = viewModel::artworkUrl,
                onBack = viewModel::backViewer,
                onSeason = viewModel::selectSeason,
                onEpisode = viewModel::playMedia,
                onPlay = { viewModel.playMedia() },
                onToggleLibrary = viewModel::toggleLibrary,
                onToggleWatched = viewModel::toggleWatched,
                externalPlayers = state.externalPlayers,
                onChooseSource = viewModel::choosePlaybackSource,
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
        else -> ViewerRoot(
            state = state,
            onTab = viewModel::selectViewerTab,
            onOpenFolder = viewModel::openFolder,
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
        val useRail = state.isTv || maxWidth >= ViewerCompactBreakpoint
        val targetSize = if (state.isTv) ViewerTvTarget else ViewerPhoneTarget
        Column(
            modifier = Modifier
                .fillMaxSize()
                .statusBarsPadding(),
        ) {
            ViewerHeader(
                tab = state.viewer.selectedTab,
                profileName = state.activeProfile?.name,
                profileAvatarModel = profileAvatarModel,
                isTv = state.isTv,
                onAccount = { showAccount = true },
            )
            Row(
                modifier = Modifier
                    .weight(1f)
                    .padding(
                        start = if (state.isTv) ViewerTvPadding else 0.dp,
                        bottom = if (state.isTv) RivuneSpacing.xl else 0.dp,
                    ),
            ) {
                if (useRail) {
                    ViewerRail(
                        selected = state.viewer.selectedTab,
                        isTv = state.isTv,
                        onSelect = onTab,
                    )
                }
                Box(modifier = Modifier.weight(1f).fillMaxHeight()) {
                    when (state.viewer.selectedTab) {
                        ViewerTab.HOME -> HomeRoot(
                            collections = state.collections,
                            selectedCollectionId = state.selectedCollectionId,
                            continueWatching = state.viewer.continueWatching,
                            loading = state.viewer.loading,
                            failure = state.viewer.inlineFailure,
                            isTv = state.isTv,
                            artworkUrl = artworkUrl,
                            onOpenFolder = onOpenFolder,
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
            }
            if (!useRail) {
                ViewerBottomBar(
                    selected = state.viewer.selectedTab,
                    onSelect = onTab,
                )
            } else {
                Spacer(Modifier.navigationBarsPadding())
            }
        }
        if (showAccount) {
            AccountDialog(
                profileName = state.activeProfile?.name,
                serverName = state.serverName,
                profileAvatarModel = profileAvatarModel,
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
) {
    val padding = if (isTv) ViewerTvPadding else ViewerPhonePadding
    val settings = state.settings
    val backFocus = remember { FocusRequester() }
    LaunchedEffect(isTv) {
        if (isTv) backFocus.requestFocus()
    }
    Column(
        modifier = Modifier
            .fillMaxSize()
            .statusBarsPadding()
            .navigationBarsPadding()
            .imePadding(),
    ) {
        ScreenToolbar(
            title = stringResource(R.string.viewer_preferences),
            onBack = onBack,
            isTv = isTv,
            backModifier = Modifier.focusRequester(backFocus),
        )
        LazyColumn(
            modifier = Modifier.fillMaxSize(),
            contentPadding = PaddingValues(start = padding, end = padding, bottom = RivuneSpacing.huge),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
        ) {
            item {
                Column(
                    modifier = Modifier.widthIn(max = ViewerPreferencesMaxWidth).fillMaxWidth(),
                    verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
                ) {
                    Text(
                        text = stringResource(R.string.viewer_preferences_title),
                        modifier = Modifier.semantics { heading() },
                        style = if (isTv) MaterialTheme.typography.displayLarge else MaterialTheme.typography.headlineLarge,
                    )
                    Text(
                        text = stringResource(
                            if (state.canEdit) R.string.viewer_preferences_body else R.string.viewer_preferences_read_only,
                        ),
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        style = MaterialTheme.typography.bodyLarge,
                    )
                }
            }
            item {
                Column(modifier = Modifier.widthIn(max = ViewerPreferencesMaxWidth).fillMaxWidth()) {
                    InlineStatus(
                        loading = loading,
                        failure = failure,
                        onRetry = onRetry,
                        isTv = isTv,
                        loadingLabel = stringResource(R.string.viewer_loading_preferences),
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
            } else if (settings != null) {
                item {
                    Column(modifier = Modifier.widthIn(max = ViewerPreferencesMaxWidth).fillMaxWidth()) {
                        SectionTitle(stringResource(R.string.viewer_playback_section), isTv)
                    }
                }
                item {
                    PreferenceChoiceCard(
                        title = stringResource(R.string.viewer_maximum_resolution),
                        description = stringResource(R.string.viewer_maximum_resolution_body),
                        selected = settings.maximumResolution ?: "auto",
                        options = listOf(
                            "auto" to stringResource(R.string.viewer_resolution_auto),
                            "2160p" to stringResource(R.string.viewer_resolution_2160p),
                            "1080p" to stringResource(R.string.viewer_resolution_1080p),
                            "720p" to stringResource(R.string.viewer_resolution_720p),
                            "480p" to stringResource(R.string.viewer_resolution_480p),
                        ),
                        enabled = state.canEdit && !loading,
                        isTv = isTv,
                        onSelect = { onUpdate(ProfileSettingsUpdate(maximumResolution = PatchField.Value(it))) },
                    )
                }
                item {
                    PreferenceChoiceCard(
                        title = stringResource(R.string.viewer_direct_play),
                        description = stringResource(R.string.viewer_direct_play_body),
                        selected = (settings.preferDirectPlay ?: false).toString(),
                        options = listOf(
                            "true" to stringResource(R.string.viewer_enabled),
                            "false" to stringResource(R.string.viewer_disabled),
                        ),
                        enabled = state.canEdit && !loading,
                        isTv = isTv,
                        onSelect = { onUpdate(ProfileSettingsUpdate(preferDirectPlay = PatchField.Value(it.toBoolean()))) },
                    )
                }
                item {
                    Column(
                        modifier = Modifier
                            .widthIn(max = ViewerPreferencesMaxWidth)
                            .fillMaxWidth()
                            .padding(top = RivuneSpacing.sm),
                    ) {
                        SectionTitle(stringResource(R.string.viewer_language_section), isTv)
                    }
                }
                item {
                    LanguagePreferenceCard(
                        title = stringResource(R.string.viewer_audio_language),
                        selected = settings.audioLanguage ?: "auto",
                        enabled = state.canEdit && !loading,
                        isTv = isTv,
                        onSelect = { onUpdate(ProfileSettingsUpdate(audioLanguage = PatchField.Value(it))) },
                    )
                }
                item {
                    LanguagePreferenceCard(
                        title = stringResource(R.string.viewer_subtitle_language),
                        selected = settings.subtitleLanguage ?: "auto",
                        enabled = state.canEdit && !loading,
                        isTv = isTv,
                        onSelect = { onUpdate(ProfileSettingsUpdate(subtitleLanguage = PatchField.Value(it))) },
                    )
                }
            }
            item {
                Column(
                    modifier = Modifier.widthIn(max = ViewerPreferencesMaxWidth).fillMaxWidth(),
                    verticalArrangement = Arrangement.spacedBy(RivuneSpacing.sm),
                ) {
                    SectionTitle(stringResource(R.string.update_section), isTv)
                    Text(
                        text = updatePreferenceStatus(updateState),
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        style = MaterialTheme.typography.bodyMedium,
                    )
                    RivuneSecondaryButton(
                        label = stringResource(R.string.update_check),
                        onClick = onCheckForUpdates,
                        enabled = updateState !is AppUpdateState.Checking &&
                            updateState !is AppUpdateState.Downloading && updateState !is AppUpdateState.Installing,
                        loading = updateState is AppUpdateState.Checking,
                        isTv = isTv,
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
            }
        }
    }
}

@Composable
private fun updatePreferenceStatus(state: AppUpdateState): String = when (state) {
    is AppUpdateState.Checking -> stringResource(R.string.update_checking)
    is AppUpdateState.UpToDate -> stringResource(R.string.update_up_to_date, state.currentVersion)
    is AppUpdateState.Available -> stringResource(R.string.update_available_status, state.manifest.version)
    is AppUpdateState.Downloading -> stringResource(R.string.update_downloading)
    is AppUpdateState.ReadyToInstall, is AppUpdateState.NeedsPermission -> stringResource(R.string.update_ready_status)
    is AppUpdateState.Installing -> stringResource(R.string.update_installing)
    is AppUpdateState.Error -> stringResource(R.string.update_failed_status)
    AppUpdateState.Idle -> stringResource(R.string.update_idle)
}

@Composable
private fun LanguagePreferenceCard(
    title: String,
    selected: String,
    enabled: Boolean,
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
        description = stringResource(R.string.viewer_language_body),
        selected = selected,
        options = options,
        enabled = enabled,
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

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun PreferenceChoiceCard(
    title: String,
    description: String,
    selected: String,
    options: List<Pair<String, String>>,
    enabled: Boolean,
    isTv: Boolean,
    onSelect: (String) -> Unit,
    extraContent: (@Composable () -> Unit)? = null,
) {
    val readOnlyDescription = stringResource(R.string.viewer_preferences_read_only)
    Surface(
        modifier = Modifier.widthIn(max = ViewerPreferencesMaxWidth).fillMaxWidth(),
        shape = RivuneShapes.large,
        color = MaterialTheme.colorScheme.surface,
    ) {
        Column(
            modifier = Modifier.padding(if (isTv) RivuneSpacing.xl else RivuneSpacing.md),
            verticalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
        ) {
            Column(verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xxs)) {
                Text(title, style = MaterialTheme.typography.titleLarge)
                Text(
                    text = description,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    style = if (isTv) MaterialTheme.typography.bodyLarge else MaterialTheme.typography.bodyMedium,
                )
            }
            FlowRow(
                horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
                verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
            ) {
                options.forEach { (value, label) ->
                    val isSelected = selected == value
                    RivuneFocusSurface(
                        onClick = { if (enabled && !isSelected) onSelect(value) },
                        enabled = enabled || isTv,
                        selected = isSelected,
                        isTv = isTv,
                        shape = RivuneShapes.pill,
                        modifier = Modifier
                            .heightIn(min = if (isTv) ViewerTvTarget else ViewerPhoneTarget)
                            .semantics {
                                if (!enabled) stateDescription = readOnlyDescription
                            },
                    ) {
                        Row(
                            modifier = Modifier.padding(horizontal = RivuneSpacing.md, vertical = RivuneSpacing.xs),
                            horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            if (isSelected) {
                                Icon(
                                    Icons.Rounded.Check,
                                    contentDescription = null,
                                    modifier = Modifier.size(RivuneSpacing.md),
                                    tint = MaterialTheme.colorScheme.primary,
                                )
                            }
                            Text(
                                text = label,
                                color = if (isSelected) {
                                    MaterialTheme.colorScheme.primary
                                } else {
                                    MaterialTheme.colorScheme.onSurface
                                },
                                style = MaterialTheme.typography.labelLarge,
                            )
                        }
                    }
                }
            }
            extraContent?.invoke()
        }
    }
}

@Composable
private fun ViewerHeader(
    tab: ViewerTab,
    profileName: String?,
    profileAvatarModel: Any?,
    isTv: Boolean,
    onAccount: () -> Unit,
) {
    val targetSize = if (isTv) ViewerTvTarget else ViewerPhoneTarget
    val displayedName = profileName ?: stringResource(R.string.viewer_unknown_profile)
    val fallback = displayedName.trim().take(1).takeIf { it.isNotBlank() }
        ?.uppercase(Locale.getDefault()) ?: stringResource(R.string.viewer_profile_fallback)
    val accountDescription = stringResource(R.string.viewer_account_for, displayedName)
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .heightIn(min = if (isTv) RivuneDimensions.buttonHeightTv else RivuneDimensions.fieldHeight)
            .padding(
                horizontal = if (isTv) ViewerTvPadding else ViewerPhonePadding,
                vertical = if (isTv) RivuneSpacing.xxl else 0.dp,
            ),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
    ) {
        RivuneBrandMark(
            size = if (isTv) ViewerTvTarget else ViewerPhoneTarget,
            mark = stringResource(R.string.brand_mark),
        )
        Text(
            text = stringResource(tab.titleResource()),
            modifier = Modifier.weight(1f).semantics { heading() },
            style = if (isTv) MaterialTheme.typography.headlineLarge else MaterialTheme.typography.headlineMedium,
        )
        RivuneFocusSurface(
            onClick = onAccount,
            isTv = isTv,
            shape = CircleShape,
            modifier = Modifier
                .size(targetSize)
                .semantics { contentDescription = accountDescription },
        ) {
            RivuneArtwork(
                model = profileAvatarModel,
                fallback = fallback,
                modifier = Modifier.fillMaxSize(),
            )
        }
    }
}

@Composable
private fun ViewerRail(
    selected: ViewerTab,
    isTv: Boolean,
    onSelect: (ViewerTab) -> Unit,
) {
    val selectedFocus = remember { FocusRequester() }
    LaunchedEffect(isTv, selected) {
        if (isTv) selectedFocus.requestFocus()
    }
    NavigationRail(
        modifier = Modifier
            .fillMaxHeight()
            .width(if (isTv) RivuneDimensions.navigationRailTv else RivuneDimensions.navigationRail),
        containerColor = MaterialTheme.colorScheme.background,
    ) {
        Spacer(Modifier.height(if (isTv) RivuneSpacing.md else RivuneSpacing.xs))
        ViewerTab.entries.forEach { tab ->
            val label = stringResource(tab.titleResource())
            NavigationRailItem(
                selected = tab == selected,
                onClick = { onSelect(tab) },
                icon = { Icon(tab.icon(), contentDescription = null) },
                label = { Text(label, maxLines = 1) },
                colors = NavigationRailItemDefaults.colors(
                    selectedIconColor = MaterialTheme.colorScheme.primary,
                    selectedTextColor = MaterialTheme.colorScheme.onBackground,
                    indicatorColor = MaterialTheme.colorScheme.primaryContainer,
                    unselectedIconColor = MaterialTheme.colorScheme.onSurfaceVariant,
                    unselectedTextColor = MaterialTheme.colorScheme.onSurfaceVariant,
                ),
                modifier = Modifier
                    .heightIn(min = if (isTv) ViewerTvTarget else ViewerPhoneTarget)
                    .then(if (tab == selected) Modifier.focusRequester(selectedFocus) else Modifier)
                    .semantics { contentDescription = label },
            )
            Spacer(Modifier.height(if (isTv) RivuneSpacing.sm else RivuneSpacing.xxs))
        }
    }
}

@Composable
private fun ViewerBottomBar(
    selected: ViewerTab,
    onSelect: (ViewerTab) -> Unit,
) {
    NavigationBar(
        modifier = Modifier.navigationBarsPadding(),
        containerColor = MaterialTheme.colorScheme.background,
    ) {
        ViewerTab.entries.forEach { tab ->
            val label = stringResource(tab.titleResource())
            NavigationBarItem(
                selected = tab == selected,
                onClick = { onSelect(tab) },
                icon = { Icon(tab.icon(), contentDescription = null) },
                label = { Text(label, maxLines = 1) },
                colors = NavigationBarItemDefaults.colors(
                    selectedIconColor = MaterialTheme.colorScheme.primary,
                    selectedTextColor = MaterialTheme.colorScheme.onBackground,
                    indicatorColor = MaterialTheme.colorScheme.primaryContainer,
                    unselectedIconColor = MaterialTheme.colorScheme.onSurfaceVariant,
                    unselectedTextColor = MaterialTheme.colorScheme.onSurfaceVariant,
                ),
                modifier = Modifier
                    .heightIn(min = ViewerPhoneTarget)
                    .semantics { contentDescription = label },
            )
        }
    }
}

@Composable
private fun HomeRoot(
    collections: List<Collection>,
    selectedCollectionId: java.util.UUID?,
    continueWatching: List<MediaTarget>,
    loading: ViewerLoading?,
    failure: UiFailure?,
    isTv: Boolean,
    artworkUrl: (String?) -> String?,
    onOpenFolder: (java.util.UUID, CollectionFolder) -> Unit,
    onMedia: (MediaTarget) -> Unit,
    onRetry: () -> Unit,
) {
    val padding = if (isTv) ViewerTvPadding else ViewerPhonePadding
    val loadingLabel = stringResource(R.string.viewer_loading_home)
    LazyColumn(
        modifier = Modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = padding, end = padding, top = RivuneSpacing.xs, bottom = RivuneSpacing.xxxl),
        verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xl),
    ) {
        inlineStatusItems(
            loading = loading == ViewerLoading.HOME,
            failure = failure,
            onRetry = onRetry,
            isTv = isTv,
            loadingLabel = loadingLabel,
        )
        if (continueWatching.isNotEmpty()) {
            item {
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
            item { MediaRowSkeleton(isTv = isTv) }
        }
        if (collections.isEmpty() && loading != ViewerLoading.HOME && failure == null) {
            item {
                InlineEmpty(
                    title = stringResource(R.string.home_empty_collections_title),
                    body = stringResource(R.string.home_empty_collections_body),
                )
            }
        }
        collections.forEach { collection ->
            item(key = collection.id) {
                Column(verticalArrangement = Arrangement.spacedBy(RivuneSpacing.sm)) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        if (collection.id == selectedCollectionId) {
                            Surface(
                                modifier = Modifier.width(RivuneSpacing.xxs).height(RivuneSpacing.xl),
                                color = MaterialTheme.colorScheme.primary,
                                shape = RivuneShapes.pill,
                            ) {}
                            Spacer(Modifier.width(RivuneSpacing.xs))
                        }
                        Text(
                            text = collection.title,
                            modifier = Modifier.weight(1f).semantics { heading() },
                            color = MaterialTheme.colorScheme.onBackground,
                            style = if (isTv) MaterialTheme.typography.headlineMedium else MaterialTheme.typography.titleLarge,
                        )
                    }
                    if (collection.folders.isEmpty()) {
                        InlineEmpty(
                            title = stringResource(R.string.home_empty_folders_title),
                            body = stringResource(R.string.home_empty_folders_body),
                        )
                    } else {
                        LazyRow(
                            horizontalArrangement = Arrangement.spacedBy(ViewerCardGap),
                            contentPadding = PaddingValues(start = RivuneSpacing.xxs, end = padding),
                        ) {
                            items(collection.folders, key = { it.id ?: "${collection.id}:${it.title}" }) { folder ->
                                FolderTile(
                                    folder = folder,
                                    imageUrl = artworkUrl(folder.coverImageUrl),
                                    isTv = isTv,
                                    enabled = folder.id != null,
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
    val padding = if (isTv) ViewerTvPadding else ViewerPhonePadding
    Column(
        modifier = Modifier
            .fillMaxSize()
            .statusBarsPadding()
            .navigationBarsPadding(),
    ) {
        val backFocus = remember { FocusRequester() }
        LaunchedEffect(isTv, folder.folder.id) {
            if (isTv) backFocus.requestFocus()
        }
        ScreenToolbar(
            title = collectionTitle ?: stringResource(R.string.home_folders),
            onBack = onBack,
            isTv = isTv,
            backModifier = Modifier.focusRequester(backFocus),
        )
        Column(modifier = Modifier.padding(horizontal = padding)) {
            Text(
                text = listOfNotNull(folder.folder.coverEmoji, folder.folder.title).joinToString(" "),
                modifier = Modifier.semantics { heading() },
                style = if (isTv) MaterialTheme.typography.displayLarge else MaterialTheme.typography.headlineLarge,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
            Spacer(Modifier.height(RivuneSpacing.xs))
            Text(
                text = pluralStringResource(R.plurals.viewer_result_count, folder.items.size, folder.items.size),
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(RivuneSpacing.md))
            InlineStatus(
                loading = loading == ViewerLoading.FOLDER,
                failure = failure,
                onRetry = onRetry,
                isTv = isTv,
                loadingLabel = stringResource(R.string.viewer_loading_folder),
            )
            if (folder.errors.isNotEmpty()) {
                InlineWarning(stringResource(R.string.folder_partial_warning))
                Spacer(Modifier.height(RivuneSpacing.md))
            }
        }
        if (folder.items.isEmpty() && loading != ViewerLoading.FOLDER && failure == null) {
            InlineEmpty(
                title = stringResource(R.string.folder_empty_title),
                body = stringResource(R.string.folder_empty_body),
                modifier = Modifier.padding(horizontal = padding),
            )
        } else {
            LazyVerticalGrid(
                columns = GridCells.Adaptive(RivuneDimensions.posterWidth),
                modifier = Modifier.weight(1f),
                contentPadding = PaddingValues(start = padding, end = padding, bottom = RivuneSpacing.xxxl),
                horizontalArrangement = Arrangement.spacedBy(ViewerCardGap),
                verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xl),
            ) {
                items(folder.items, key = { "${it.mediaType}:${it.id}" }) { item ->
                    MediaTile(
                        target = item.toMediaTarget(),
                        imageUrl = artworkUrl(item.posterUrl ?: item.backgroundUrl),
                        isTv = isTv,
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
    val padding = if (isTv) ViewerTvPadding else ViewerPhonePadding
    LaunchedEffect(isTv, state.query, state.items.firstOrNull()?.id) {
        if (isTv && state.items.isNotEmpty()) firstResultFocus.requestFocus()
    }
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(horizontal = padding)
            .imePadding(),
    ) {
        RivuneTextField(
            value = query,
            onValueChange = { query = it },
            modifier = Modifier
                .fillMaxWidth()
                .widthIn(max = RivuneDimensions.contentMaxTablet),
            label = stringResource(R.string.viewer_search_label),
            placeholder = stringResource(R.string.viewer_search_hint),
            leadingIcon = Icons.Rounded.Search,
            isTv = isTv,
            trailingContent = {
                val submitDescription = stringResource(R.string.viewer_search_submit)
                IconButton(
                    onClick = submit,
                    enabled = trimmed.length >= 2 && loading != ViewerLoading.SEARCH,
                    modifier = Modifier
                        .size(if (isTv) ViewerTvTarget else ViewerPhoneTarget)
                        .semantics { contentDescription = submitDescription },
                ) {
                    Icon(Icons.Rounded.Search, contentDescription = null)
                }
            },
            keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
            keyboardActions = KeyboardActions(onSearch = { submit() }),
        )
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
        when {
            state.items.isNotEmpty() -> LazyVerticalGrid(
                columns = GridCells.Adaptive(RivuneDimensions.posterWidth),
                modifier = Modifier.weight(1f),
                contentPadding = PaddingValues(bottom = RivuneSpacing.xxxl),
                horizontalArrangement = Arrangement.spacedBy(ViewerCardGap),
                verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xl),
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
                title = stringResource(if (state.query.length >= 2) R.string.viewer_search_empty_title else R.string.viewer_search_start_title),
                body = stringResource(if (state.query.length >= 2) R.string.viewer_search_empty_body else R.string.viewer_search_start_body),
            )
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
    val padding = if (isTv) ViewerTvPadding else ViewerPhonePadding
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(horizontal = padding),
    ) {
        LazyRow(horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.xs)) {
            item {
                FilterChip(
                    selected = state.mediaType == null,
                    onClick = { onType(null) },
                    label = { Text(stringResource(R.string.viewer_library_all)) },
                    leadingIcon = if (state.mediaType == null) ({ Icon(Icons.Rounded.Check, contentDescription = null) }) else null,
                    modifier = Modifier.heightIn(min = if (isTv) ViewerTvTarget else ViewerPhoneTarget),
                )
            }
            item {
                FilterChip(
                    selected = state.mediaType == "movie",
                    onClick = { onType("movie") },
                    label = { Text(stringResource(R.string.viewer_movies)) },
                    leadingIcon = if (state.mediaType == "movie") ({ Icon(Icons.Rounded.Check, contentDescription = null) }) else null,
                    modifier = Modifier.heightIn(min = if (isTv) ViewerTvTarget else ViewerPhoneTarget),
                )
            }
            item {
                FilterChip(
                    selected = state.mediaType == "series",
                    onClick = { onType("series") },
                    label = { Text(stringResource(R.string.viewer_series)) },
                    leadingIcon = if (state.mediaType == "series") ({ Icon(Icons.Rounded.Check, contentDescription = null) }) else null,
                    modifier = Modifier.heightIn(min = if (isTv) ViewerTvTarget else ViewerPhoneTarget),
                )
            }
        }
        Text(
            text = pluralStringResource(R.plurals.viewer_library_count, state.totalResults, state.totalResults),
            modifier = Modifier.padding(vertical = RivuneSpacing.sm),
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            style = MaterialTheme.typography.bodyMedium,
        )
        InlineStatus(
            loading = loading == ViewerLoading.LIBRARY,
            failure = failure,
            onRetry = onRetry,
            isTv = isTv,
            loadingLabel = stringResource(R.string.viewer_loading_library),
        )
        if (state.items.isEmpty() && loading != ViewerLoading.LIBRARY && failure == null) {
            InlineEmpty(
                title = stringResource(R.string.viewer_library_empty_title),
                body = stringResource(R.string.viewer_library_empty_body),
            )
        } else {
            LazyVerticalGrid(
                columns = GridCells.Adaptive(RivuneDimensions.posterWidth),
                modifier = Modifier.weight(1f),
                contentPadding = PaddingValues(bottom = RivuneSpacing.xxxl),
                horizontalArrangement = Arrangement.spacedBy(ViewerCardGap),
                verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xl),
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
        }
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
    val padding = if (isTv) ViewerTvPadding else ViewerPhonePadding
    val locale = Locale.getDefault()
    val monthLabel = remember(month, locale) {
        month.atDay(1).format(DateTimeFormatter.ofPattern("MMMM yyyy", locale))
            .replaceFirstChar { if (it.isLowerCase()) it.titlecase(locale) else it.toString() }
    }
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(horizontal = padding),
    ) {
        Row(
            modifier = Modifier.fillMaxWidth(),
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
                style = if (isTv) MaterialTheme.typography.headlineMedium else MaterialTheme.typography.titleLarge,
            )
            CalendarMonthButton(
                icon = Icons.Rounded.ChevronRight,
                label = stringResource(R.string.viewer_calendar_next),
                isTv = isTv,
                onClick = onNextMonth,
            )
        }
        Spacer(Modifier.height(RivuneSpacing.md))
        InlineStatus(
            loading = loading == ViewerLoading.CALENDAR,
            failure = failure,
            onRetry = onRetry,
            isTv = isTv,
            loadingLabel = stringResource(R.string.viewer_loading_calendar),
        )
        if (events.isEmpty() && loading != ViewerLoading.CALENDAR && failure == null) {
            InlineEmpty(
                title = stringResource(R.string.viewer_calendar_empty_title),
                body = stringResource(R.string.viewer_calendar_empty_body),
            )
        } else {
            LazyColumn(
                modifier = Modifier.weight(1f),
                contentPadding = PaddingValues(bottom = RivuneSpacing.xxxl),
                verticalArrangement = Arrangement.spacedBy(RivuneSpacing.sm),
            ) {
                events.groupBy { it.releaseDate }.forEach { (releaseDate, dayEvents) ->
                    item(key = "date:$releaseDate") {
                        Text(
                            text = localizedDate(releaseDate, locale),
                            modifier = Modifier
                                .padding(top = RivuneSpacing.sm, bottom = RivuneSpacing.xxs)
                                .semantics { heading() },
                            color = MaterialTheme.colorScheme.onSurface,
                            style = MaterialTheme.typography.titleSmall,
                        )
                    }
                    items(dayEvents, key = { it.id }) { event ->
                        CalendarEventRow(
                            event = event,
                            imageUrl = artworkUrl(event.posterUrl),
                            isTv = isTv,
                            onClick = { onEvent(event) },
                        )
                    }
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
    onBack: () -> Unit,
    onSeason: (String) -> Unit,
    onEpisode: (MediaTarget) -> Unit,
    onPlay: () -> Unit,
    onToggleLibrary: () -> Unit,
    onToggleWatched: () -> Unit,
    externalPlayers: List<ExternalPlayerApp>,
    onChooseSource: (PlaybackSourceOption, ExternalPlayerApp?) -> Unit,
    onDismissSources: () -> Unit,
    onRetry: () -> Unit,
) {
    val detail = checkNotNull(state.detail)
    val movie = detail.movie
    val series = detail.series
    val season = detail.season
    val title = movie?.title ?: series?.name ?: season?.name ?: detail.target.title
    val overview = movie?.overview ?: series?.overview ?: season?.overview ?: detail.target.description
    val artwork = artworkUrl(
        movie?.backdropUrl ?: series?.backdropUrl ?: season?.backdropUrl ?: detail.target.backgroundUrl ?: detail.target.posterUrl,
    )
    val cast = movie?.cast ?: series?.cast.orEmpty()
    val padding = if (isTv) ViewerTvPadding else ViewerPhonePadding
    val backFocus = remember { FocusRequester() }
    val playFocus = remember { FocusRequester() }
    val hasPlayAction = detail.target.mediaType != "series"
    val showStatus = state.inlineFailure != null || state.loading == ViewerLoading.DETAIL ||
        state.loading == ViewerLoading.SEASON || state.loading == ViewerLoading.SOURCES ||
        state.loading == ViewerLoading.ACTION
    LaunchedEffect(isTv, detail.target.id, state.inlineFailure) {
        if (isTv && state.sourcePicker == null) {
            if (hasPlayAction && state.inlineFailure == null) playFocus.requestFocus() else backFocus.requestFocus()
        }
    }
    val detailLoadingLabel = stringResource(
        when (state.loading) {
            ViewerLoading.SEASON -> R.string.viewer_loading_season
            ViewerLoading.SOURCES -> R.string.viewer_loading_sources
            ViewerLoading.ACTION -> R.string.viewer_saving_change
            else -> R.string.viewer_loading_detail
        },
    )
    Column(
        modifier = Modifier
            .fillMaxSize()
            .statusBarsPadding()
            .navigationBarsPadding(),
    ) {
        ScreenToolbar(title = title, onBack = onBack, isTv = isTv, backModifier = Modifier.focusRequester(backFocus))
        LazyColumn(
            modifier = Modifier.fillMaxSize(),
            contentPadding = PaddingValues(start = padding, end = padding, bottom = RivuneSpacing.huge),
            verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xl),
        ) {
            if (showStatus) {
                item {
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
            item {
                BoxWithConstraints(modifier = Modifier.fillMaxWidth()) {
                    val wide = maxWidth >= RivuneBreakpoints.expanded
                    if (wide) {
                        Row(horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.xxl)) {
                            DetailArtwork(artwork, title, Modifier.weight(0.46f))
                            DetailSummary(
                                detail = detail,
                                title = title,
                                overview = overview,
                                isTv = isTv,
                                actionsEnabled = state.loading == null,
                                onPlay = onPlay,
                                onToggleLibrary = onToggleLibrary,
                                onToggleWatched = onToggleWatched,
                                playModifier = Modifier.focusRequester(playFocus),
                                modifier = Modifier.weight(0.54f),
                            )
                        }
                    } else {
                        Column(verticalArrangement = Arrangement.spacedBy(RivuneSpacing.lg)) {
                            DetailArtwork(artwork, title, Modifier.fillMaxWidth())
                            DetailSummary(
                                detail = detail,
                                title = title,
                                overview = overview,
                                isTv = isTv,
                                actionsEnabled = state.loading == null,
                                onPlay = onPlay,
                                onToggleLibrary = onToggleLibrary,
                                onToggleWatched = onToggleWatched,
                                playModifier = Modifier.focusRequester(playFocus),
                            )
                        }
                    }
                }
            }
            if (cast.isNotEmpty()) {
                item {
                    Column(verticalArrangement = Arrangement.spacedBy(RivuneSpacing.sm)) {
                        SectionTitle(stringResource(R.string.viewer_cast), isTv)
                        FlowRow(
                            horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
                            verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
                        ) {
                            cast.forEach { member ->
                                Surface(
                                    color = MaterialTheme.colorScheme.surface,
                                    shape = RivuneShapes.small,
                                ) {
                                    Text(
                                        text = listOfNotNull(member.name, member.character?.takeIf(String::isNotBlank)).joinToString(" · "),
                                        modifier = Modifier.padding(horizontal = RivuneSpacing.md, vertical = RivuneSpacing.sm),
                                        style = MaterialTheme.typography.bodyMedium,
                                    )
                                }
                            }
                        }
                    }
                }
            }
            if (series != null && series.seasons.isNotEmpty()) {
                item {
                    Column(verticalArrangement = Arrangement.spacedBy(RivuneSpacing.sm)) {
                        SectionTitle(stringResource(R.string.viewer_seasons), isTv)
                        LazyRow(
                            contentPadding = PaddingValues(horizontal = RivuneSpacing.xxs),
                            horizontalArrangement = Arrangement.spacedBy(ViewerCardGap),
                        ) {
                            items(series.seasons, key = { it.id }) { summary ->
                                SeasonTile(
                                    title = summary.name,
                                    subtitle = pluralStringResource(R.plurals.viewer_episode_count, summary.episodeCount, summary.episodeCount),
                                    imageUrl = artworkUrl(summary.posterUrl ?: summary.backdropUrl),
                                    selected = season?.id == summary.id,
                                    isTv = isTv,
                                    onClick = { onSeason(summary.id) },
                                )
                            }
                        }
                    }
                }
            }
            if (season != null && series != null) {
                item { SectionTitle(stringResource(R.string.viewer_episodes), isTv) }
                items(season.episodes, key = { it.id }) { episode ->
                    val target = episode.toMediaTarget(series, detail.target)
                    EpisodeRow(
                        target = target,
                        progress = detail.episodeProgress[episode.id],
                        imageUrl = artworkUrl(episode.stillUrl ?: episode.backdropUrl),
                        isTv = isTv,
                        onClick = { onEpisode(target) },
                    )
                }
            }
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
            onChoose = onChooseSource,
        )
    }
}

@Composable
private fun DetailArtwork(imageUrl: String?, title: String, modifier: Modifier = Modifier) {
    RivuneArtwork(
        model = imageUrl,
        fallback = title,
        contentDescription = stringResource(R.string.media_image_description, title),
        modifier = modifier
            .aspectRatio(16f / 9f)
            .clip(RivuneShapes.large),
    )
}

@Composable
private fun DetailSummary(
    detail: MediaDetailState,
    title: String,
    overview: String?,
    isTv: Boolean,
    actionsEnabled: Boolean,
    onPlay: () -> Unit,
    onToggleLibrary: () -> Unit,
    onToggleWatched: () -> Unit,
    playModifier: Modifier = Modifier,
    modifier: Modifier = Modifier,
) {
    val locale = Locale.getDefault()
    val metadata = listOfNotNull(
        (detail.movie?.releaseDate ?: detail.series?.firstAirDate ?: detail.season?.airDate ?: detail.target.releaseInfo)
            ?.let { localizedDate(it, locale) },
        detail.movie?.runtimeMinutes?.let { stringResource(R.string.viewer_minutes, it) },
        (detail.movie?.genres ?: detail.series?.genres.orEmpty()).takeIf { it.isNotEmpty() }?.joinToString(" · ") { it.name },
    )
    Column(modifier = modifier, verticalArrangement = Arrangement.spacedBy(RivuneSpacing.md)) {
        Text(
            text = title,
            modifier = Modifier.semantics { heading() },
            style = if (isTv) MaterialTheme.typography.displayLarge else MaterialTheme.typography.headlineLarge,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
        )
        if (metadata.isNotEmpty()) {
            Text(
                text = metadata.joinToString("  •  "),
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                style = MaterialTheme.typography.bodyMedium,
            )
        }
        if (detail.target.mediaType != "series") {
            RivunePrimaryButton(
                label = stringResource(R.string.viewer_play),
                onClick = onPlay,
                enabled = actionsEnabled,
                isTv = isTv,
                icon = Icons.Rounded.PlayArrow,
                modifier = playModifier.fillMaxWidth(),
            )
        }
        if (detail.target.mediaType != "episode") {
            RivuneSecondaryButton(
                label = stringResource(if (detail.inLibrary) R.string.viewer_in_library else R.string.viewer_add_library),
                onClick = onToggleLibrary,
                enabled = actionsEnabled,
                isTv = isTv,
                icon = if (detail.inLibrary) Icons.Rounded.Check else Icons.Rounded.LibraryAdd,
                modifier = Modifier.fillMaxWidth(),
            )
        }
        val seasonWatched = detail.season?.episodes
            ?.takeIf { it.isNotEmpty() }
            ?.all { detail.episodeProgress[it.id]?.completed == true } == true
        val watched = if (detail.season != null) seasonWatched else detail.progress?.completed == true
        if (detail.target.mediaType != "series" || detail.season != null) {
            RivuneSecondaryButton(
                label = stringResource(if (watched) R.string.viewer_mark_unwatched else R.string.viewer_mark_watched),
                onClick = onToggleWatched,
                enabled = actionsEnabled,
                isTv = isTv,
                icon = if (watched) Icons.Rounded.Check else Icons.Rounded.Add,
                modifier = Modifier.fillMaxWidth(),
            )
        }
        if (!overview.isNullOrBlank()) {
            Text(text = overview, style = MaterialTheme.typography.bodyLarge)
        }
        detail.progress?.takeIf { it.durationSeconds > 0 && !it.completed }?.let { progress ->
            PlaybackProgressSummary(progress.positionSeconds, progress.durationSeconds)
        }
    }
}

@Composable
private fun SourcePickerDialog(
    picker: SourcePickerState,
    isTv: Boolean,
    externalPlayers: List<ExternalPlayerApp>,
    loading: Boolean,
    failure: UiFailure?,
    onDismiss: () -> Unit,
    onRetry: () -> Unit,
    onChoose: (PlaybackSourceOption, ExternalPlayerApp?) -> Unit,
) {
    val firstSourceFocus = remember { FocusRequester() }
    var targetSource by remember(picker.options) { mutableStateOf<PlaybackSourceOption?>(null) }
    val targetPlayers = remember(targetSource, externalPlayers) {
        targetSource?.let { source ->
            ExternalPlaybackSupport(externalPlayers)
                .playersFor(source.mode, source.protocol, source.container)
        }.orEmpty()
    }
    LaunchedEffect(isTv, picker.options.firstOrNull()?.id) {
        if (isTv && picker.options.isNotEmpty()) firstSourceFocus.requestFocus()
    }
    BackHandler(onBack = onDismiss)
    Dialog(
        onDismissRequest = onDismiss,
        properties = DialogProperties(usePlatformDefaultWidth = false),
    ) {
        Box(
            modifier = Modifier
                .fillMaxSize()
                .padding(
                    horizontal = if (isTv) RivuneSpacing.huge else RivuneSpacing.md,
                    vertical = if (isTv) RivuneSpacing.xl else RivuneSpacing.md,
                ),
            contentAlignment = Alignment.Center,
        ) {
            Surface(
                modifier = Modifier
                    .fillMaxWidth(if (isTv) 0.72f else 1f)
                    .widthIn(max = RivuneDimensions.contentMaxTablet)
                    .fillMaxHeight(),
                shape = RivuneShapes.large,
                color = MaterialTheme.colorScheme.surface,
                tonalElevation = RivuneElevation.overlay,
            ) {
                Column(modifier = Modifier.padding(if (isTv) RivuneSpacing.xxl else RivuneSpacing.lg)) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Column(modifier = Modifier.weight(1f)) {
                            Text(
                                stringResource(R.string.viewer_choose_source),
                                modifier = Modifier.semantics { heading() },
                                style = MaterialTheme.typography.headlineMedium,
                            )
                            Text(
                                picker.target.title,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                maxLines = 2,
                                overflow = TextOverflow.Ellipsis,
                                style = MaterialTheme.typography.bodyMedium,
                            )
                        }
                        RivuneTextButton(label = stringResource(R.string.pin_cancel), onClick = onDismiss, isTv = isTv)
                    }
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
                    if (picker.options.isEmpty() && failure == null) {
                        InlineEmpty(
                            title = stringResource(R.string.viewer_sources_empty_title),
                            body = stringResource(R.string.viewer_sources_empty_body),
                        )
                    } else {
                        LazyColumn(
                            modifier = Modifier.weight(1f, fill = false),
                            contentPadding = PaddingValues(vertical = RivuneSpacing.xxs),
                            verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
                        ) {
                            items(picker.options, key = { it.id }) { option ->
                                RivuneFocusSurface(
                                    onClick = {
                                        val players = ExternalPlaybackSupport(externalPlayers)
                                            .playersFor(option.mode, option.protocol, option.container)
                                        if (players.isEmpty()) onChoose(option, null) else targetSource = option
                                    },
                                    isTv = isTv,
                                    enabled = !loading,
                                    modifier = Modifier
                                        .fillMaxWidth()
                                        .then(
                                            if (option.id == picker.options.first().id) Modifier.focusRequester(firstSourceFocus)
                                            else Modifier,
                                        ),
                                ) {
                                    Column(modifier = Modifier.padding(RivuneSpacing.md)) {
                                        Text(
                                            option.name,
                                            maxLines = 2,
                                            overflow = TextOverflow.Ellipsis,
                                            style = MaterialTheme.typography.titleMedium,
                                        )
                                        Text(
                                            text = listOfNotNull(
                                                option.addonName,
                                                option.description,
                                                option.container?.uppercase(Locale.getDefault()),
                                                option.protocol.uppercase(Locale.getDefault()),
                                            ).joinToString(" · "),
                                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                                            maxLines = 2,
                                            overflow = TextOverflow.Ellipsis,
                                            style = MaterialTheme.typography.bodyMedium,
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
    targetSource?.let { source ->
        PlaybackTargetDialog(
            source = source,
            players = targetPlayers,
            isTv = isTv,
            onDismiss = { targetSource = null },
            onChoose = { player ->
                targetSource = null
                onChoose(source, player)
            },
        )
    }
}

@Composable
private fun PlaybackTargetDialog(
    source: PlaybackSourceOption,
    players: List<ExternalPlayerApp>,
    isTv: Boolean,
    onDismiss: () -> Unit,
    onChoose: (ExternalPlayerApp?) -> Unit,
) {
    val firstTargetFocus = remember { FocusRequester() }
    LaunchedEffect(isTv, source.id, players.size) {
        if (isTv && (source.mode != io.rivune.api.PlaybackMode.EXTERNAL || players.isNotEmpty())) {
            firstTargetFocus.requestFocus()
        }
    }
    BackHandler(onBack = onDismiss)
    Dialog(
        onDismissRequest = onDismiss,
        properties = DialogProperties(usePlatformDefaultWidth = false),
    ) {
        Box(
            modifier = Modifier
                .fillMaxSize()
                .padding(
                    horizontal = if (isTv) RivuneSpacing.huge else RivuneSpacing.md,
                    vertical = if (isTv) RivuneSpacing.xl else RivuneSpacing.md,
                ),
            contentAlignment = Alignment.Center,
        ) {
            Surface(
                modifier = Modifier
                    .fillMaxWidth(if (isTv) 0.62f else 1f)
                    .widthIn(max = RivuneDimensions.contentMax)
                    .fillMaxHeight(),
                shape = RivuneShapes.large,
                color = MaterialTheme.colorScheme.surface,
                tonalElevation = RivuneElevation.overlay,
            ) {
                Column(
                    modifier = Modifier.padding(if (isTv) RivuneSpacing.xxl else RivuneSpacing.lg),
                    verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
                ) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Column(modifier = Modifier.weight(1f)) {
                            Text(
                                stringResource(R.string.viewer_choose_player),
                                modifier = Modifier.semantics { heading() },
                                style = MaterialTheme.typography.headlineMedium,
                            )
                            Text(
                                source.name,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                maxLines = 2,
                                overflow = TextOverflow.Ellipsis,
                                style = MaterialTheme.typography.bodyMedium,
                            )
                        }
                        RivuneTextButton(label = stringResource(R.string.pin_cancel), onClick = onDismiss, isTv = isTv)
                    }
                    Spacer(Modifier.height(RivuneSpacing.sm))
                    LazyColumn(
                        modifier = Modifier.weight(1f, fill = false),
                        contentPadding = PaddingValues(vertical = RivuneSpacing.xxs),
                        verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
                    ) {
                        if (source.mode != io.rivune.api.PlaybackMode.EXTERNAL) {
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
                                modifier = if (
                                    source.mode == io.rivune.api.PlaybackMode.EXTERNAL && player == players.first()
                                ) {
                                    Modifier.focusRequester(firstTargetFocus)
                                } else {
                                    Modifier
                                },
                            )
                        }
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
    RivuneFocusSurface(
        onClick = onClick,
        isTv = isTv,
        modifier = modifier.fillMaxWidth(),
    ) {
        Column(modifier = Modifier.padding(RivuneSpacing.md)) {
            Text(label, maxLines = 2, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.titleMedium)
            Text(
                supporting,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
                style = MaterialTheme.typography.bodyMedium,
            )
        }
    }
}

@Composable
private fun AccountDialog(
    profileName: String?,
    serverName: String,
    profileAvatarModel: Any?,
    isTv: Boolean,
    onDismiss: () -> Unit,
    onRefresh: () -> Unit,
    onChangeProfile: () -> Unit,
    onPreferences: () -> Unit,
    onLogout: () -> Unit,
) {
    val displayedName = profileName ?: stringResource(R.string.viewer_unknown_profile)
    val targetSize = if (isTv) ViewerTvTarget else ViewerPhoneTarget
    val firstActionFocus = remember { FocusRequester() }
    LaunchedEffect(isTv) {
        if (isTv) firstActionFocus.requestFocus()
    }
    BackHandler(onBack = onDismiss)
    Dialog(
        onDismissRequest = onDismiss,
        properties = DialogProperties(usePlatformDefaultWidth = false),
    ) {
        Box(
            modifier = Modifier
                .fillMaxSize()
                .padding(
                    horizontal = if (isTv) RivuneSpacing.huge else RivuneSpacing.md,
                    vertical = if (isTv) RivuneSpacing.xl else RivuneSpacing.md,
                ),
            contentAlignment = Alignment.Center,
        ) {
            Surface(
                modifier = Modifier
                    .fillMaxWidth(if (isTv) 0.62f else 1f)
                    .widthIn(max = RivuneDimensions.contentMax)
                    .fillMaxHeight(),
                shape = RivuneShapes.large,
                color = MaterialTheme.colorScheme.surface,
                tonalElevation = RivuneElevation.overlay,
            ) {
                Column(
                    modifier = Modifier.padding(if (isTv) RivuneSpacing.xxl else RivuneSpacing.lg),
                    verticalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
                ) {
                    Row(
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.sm),
                    ) {
                        Icon(Icons.Rounded.AccountCircle, contentDescription = null)
                        Text(
                            stringResource(R.string.viewer_account_title),
                            modifier = Modifier.weight(1f).semantics { heading() },
                            style = MaterialTheme.typography.headlineMedium,
                        )
                    }
                    LazyColumn(
                        modifier = Modifier.weight(1f),
                        contentPadding = PaddingValues(vertical = RivuneSpacing.xxs),
                        verticalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
                    ) {
                        item {
                            Row(
                                verticalAlignment = Alignment.CenterVertically,
                                horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
                            ) {
                                RivuneArtwork(
                                    model = profileAvatarModel,
                                    fallback = displayedName.trim().take(1).takeIf { it.isNotBlank() }
                                        ?.uppercase(Locale.getDefault()) ?: stringResource(R.string.viewer_profile_fallback),
                                    modifier = Modifier.size(targetSize).clip(CircleShape),
                                )
                                Column(
                                    modifier = Modifier.weight(1f),
                                    verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xxs),
                                ) {
                                    Text(
                                        text = stringResource(R.string.viewer_profile).uppercase(Locale.getDefault()),
                                        color = MaterialTheme.colorScheme.primary,
                                        style = MaterialTheme.typography.labelMedium,
                                    )
                                    Text(
                                        text = displayedName,
                                        maxLines = 1,
                                        overflow = TextOverflow.Ellipsis,
                                        style = MaterialTheme.typography.titleLarge,
                                    )
                                    Text(
                                        text = stringResource(R.string.viewer_server).uppercase(Locale.getDefault()),
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                        style = MaterialTheme.typography.labelSmall,
                                    )
                                    Text(
                                        text = serverName,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                        maxLines = 1,
                                        overflow = TextOverflow.Ellipsis,
                                        style = MaterialTheme.typography.bodyMedium,
                                    )
                                }
                            }
                        }
                        item { HorizontalDivider() }
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
                            AccountAction(
                                icon = Icons.Rounded.Person,
                                label = stringResource(R.string.home_change_profile),
                                isTv = isTv,
                                onClick = onChangeProfile,
                            )
                        }
                        item {
                            AccountAction(
                                icon = Icons.Rounded.Refresh,
                                label = stringResource(R.string.home_refresh),
                                isTv = isTv,
                                onClick = onRefresh,
                            )
                        }
                        item { HorizontalDivider() }
                        item {
                            RivuneTextButton(
                                label = stringResource(R.string.logout),
                                onClick = onLogout,
                                icon = Icons.AutoMirrored.Rounded.Logout,
                                destructive = true,
                                isTv = isTv,
                                modifier = Modifier.fillMaxWidth(),
                            )
                        }
                    }
                    RivuneTextButton(
                        label = stringResource(R.string.viewer_close),
                        onClick = onDismiss,
                        isTv = isTv,
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
            }
        }
    }
}

@Composable
private fun AccountAction(
    icon: ImageVector,
    label: String,
    isTv: Boolean,
    modifier: Modifier = Modifier,
    onClick: () -> Unit,
) {
    RivuneSecondaryButton(
        label = label,
        onClick = onClick,
        icon = icon,
        isTv = isTv,
        modifier = modifier.fillMaxWidth(),
    )
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
        SectionTitle(title, isTv)
        LazyRow(
            contentPadding = PaddingValues(horizontal = RivuneSpacing.xxs),
            horizontalArrangement = Arrangement.spacedBy(ViewerCardGap),
        ) {
            items(items, key = { "${it.mediaType}:${it.id}" }) { item ->
                MediaTile(
                    target = item,
                    imageUrl = artworkUrl(item.backgroundUrl ?: item.posterUrl),
                    isTv = isTv,
                    modifier = Modifier.width(if (isTv) RivuneDimensions.landscapeCardWidthTv else RivuneDimensions.landscapeCardWidth),
                    landscape = true,
                    onClick = { onMedia(item) },
                )
            }
        }
    }
}

@Composable
private fun MediaRowSkeleton(isTv: Boolean) {
    Column(verticalArrangement = Arrangement.spacedBy(RivuneSpacing.sm)) {
        RivuneSkeleton(
            modifier = Modifier
                .width(if (isTv) RivuneDimensions.landscapeCardWidthTv else RivuneDimensions.landscapeCardWidth)
                .height(RivuneSpacing.xl),
            shape = RivuneShapes.small,
        )
        LazyRow(horizontalArrangement = Arrangement.spacedBy(ViewerCardGap)) {
            items(listOf(0, 1, 2)) {
                RivuneSkeleton(
                    modifier = Modifier
                        .width(if (isTv) RivuneDimensions.landscapeCardWidthTv else RivuneDimensions.landscapeCardWidth)
                        .aspectRatio(16f / 9f),
                )
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
    Column(modifier = modifier, verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xs)) {
        RivuneFocusSurface(
            onClick = onClick,
            isTv = isTv,
            modifier = Modifier
                .fillMaxWidth()
                .semantics {
                    contentDescription = target.title
                    progressDescription?.let { stateDescription = it }
                },
        ) {
            Box {
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
                        modifier = Modifier.align(Alignment.BottomStart).padding(RivuneSpacing.xs),
                        color = MaterialTheme.colorScheme.errorContainer,
                        shape = RivuneShapes.small,
                    ) {
                        Text(
                            text = stringResource(R.string.viewer_unavailable),
                            modifier = Modifier.padding(horizontal = RivuneSpacing.xs, vertical = RivuneSpacing.xxs),
                            color = MaterialTheme.colorScheme.onErrorContainer,
                            style = MaterialTheme.typography.labelMedium,
                        )
                    }
                }
            }
        }
        Text(
            text = target.title,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
            style = if (isTv) MaterialTheme.typography.titleLarge else MaterialTheme.typography.titleMedium,
        )
        Text(
            text = progressDescription
                ?: target.releaseInfo?.takeIf(String::isNotBlank)?.let { localizedDate(it, Locale.getDefault()) }
                ?: mediaTypeLabel(target.mediaType),
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            style = MaterialTheme.typography.bodyMedium,
        )
    }
}

@Composable
private fun FolderTile(
    folder: CollectionFolder,
    imageUrl: String?,
    isTv: Boolean,
    enabled: Boolean,
    onClick: () -> Unit,
) {
    val initial = folder.title.trim().take(1).uppercase(Locale.getDefault())
    val fallback = folder.coverEmoji?.takeIf { it.isNotBlank() }
        ?: initial.takeIf { it.isNotBlank() }
        ?: stringResource(R.string.folder_fallback)
    RivuneFocusSurface(
        onClick = onClick,
        isTv = isTv,
        enabled = enabled,
        modifier = Modifier
            .width(if (isTv) RivuneDimensions.landscapeCardWidthTv else RivuneDimensions.landscapeCardWidth)
            .semantics { contentDescription = folder.title },
    ) {
        Column {
            RivuneArtwork(
                model = imageUrl,
                fallback = fallback,
                contentDescription = stringResource(R.string.folder_image_description, folder.title),
                modifier = Modifier.fillMaxWidth().aspectRatio(16f / 9f),
            )
            if (!folder.hideTitle) {
                Text(
                    text = folder.title,
                    modifier = Modifier.padding(RivuneSpacing.sm),
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                    style = if (isTv) MaterialTheme.typography.titleLarge else MaterialTheme.typography.titleMedium,
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
    Column(modifier = Modifier.width(if (isTv) RivuneDimensions.posterWidthTv else RivuneDimensions.posterWidth)) {
        RivuneFocusSurface(
            onClick = onClick,
            isTv = isTv,
            selected = selected,
        ) {
            RivuneArtwork(
                model = imageUrl,
                fallback = title,
                contentDescription = stringResource(R.string.media_image_description, title),
                modifier = Modifier.fillMaxWidth().aspectRatio(2f / 3f),
            )
        }
        Spacer(Modifier.height(RivuneSpacing.xs))
        Text(title, maxLines = 2, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.titleMedium)
        Text(subtitle, color = MaterialTheme.colorScheme.onSurfaceVariant, style = MaterialTheme.typography.bodyMedium)
    }
}

@Composable
private fun EpisodeRow(
    target: MediaTarget,
    progress: PlaybackProgress?,
    imageUrl: String?,
    isTv: Boolean,
    onClick: () -> Unit,
) {
    RivuneFocusSurface(
        onClick = onClick,
        isTv = isTv,
        modifier = Modifier.fillMaxWidth(),
    ) {
        Row(
            modifier = Modifier.padding(RivuneSpacing.sm),
            horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            RivuneArtwork(
                model = imageUrl,
                fallback = target.title,
                contentDescription = stringResource(R.string.media_image_description, target.title),
                modifier = Modifier
                    .width(if (isTv) RivuneDimensions.posterWidthTv else RivuneDimensions.posterWidth)
                    .aspectRatio(16f / 9f)
                    .clip(RivuneShapes.small),
            )
            Column(modifier = Modifier.weight(1f)) {
                Text(
                    text = stringResource(R.string.viewer_episode_number, target.episodeNumber ?: 0, target.title),
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                    style = if (isTv) MaterialTheme.typography.titleLarge else MaterialTheme.typography.titleMedium,
                )
                if (!target.description.isNullOrBlank()) {
                    Text(
                        text = target.description,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 2,
                        overflow = TextOverflow.Ellipsis,
                        style = MaterialTheme.typography.bodyMedium,
                    )
                }
                when {
                    progress?.completed == true -> Text(stringResource(R.string.viewer_watched), color = MaterialTheme.colorScheme.primary, style = MaterialTheme.typography.labelMedium)
                    progress != null && progress.durationSeconds > 0 -> PlaybackProgressSummary(progress.positionSeconds, progress.durationSeconds)
                }
            }
            Icon(Icons.Rounded.PlayArrow, contentDescription = null, tint = MaterialTheme.colorScheme.primary)
        }
    }
}

@Composable
private fun CalendarEventRow(
    event: CalendarEvent,
    imageUrl: String?,
    isTv: Boolean,
    onClick: () -> Unit,
) {
    RivuneFocusSurface(onClick = onClick, isTv = isTv, modifier = Modifier.fillMaxWidth()) {
        Row(
            modifier = Modifier.padding(RivuneSpacing.sm),
            horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            RivuneArtwork(
                model = imageUrl,
                fallback = event.title,
                contentDescription = stringResource(R.string.media_image_description, event.title),
                modifier = Modifier
                    .width(if (isTv) RivuneDimensions.navigationRail else RivuneSpacing.display)
                    .aspectRatio(2f / 3f)
                    .clip(RivuneShapes.small),
            )
            Column(modifier = Modifier.weight(1f)) {
                Text(event.title, maxLines = 2, overflow = TextOverflow.Ellipsis, style = if (isTv) MaterialTheme.typography.titleLarge else MaterialTheme.typography.titleMedium)
                val context = listOfNotNull(
                    event.seriesTitle,
                    event.seasonNumber?.let { stringResource(R.string.viewer_season_short, it) },
                    event.episodeNumber?.let { stringResource(R.string.viewer_episode_short, it) },
                ).joinToString(" · ")
                Text(
                    text = context.ifBlank { mediaTypeLabel(event.mediaType.name.lowercase(Locale.ROOT)) },
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    style = MaterialTheme.typography.bodyMedium,
                )
            }
            Icon(Icons.Rounded.ChevronRight, contentDescription = null)
        }
    }
}

@Composable
private fun PlaybackProgressSummary(positionSeconds: Int, durationSeconds: Int) {
    if (durationSeconds <= 0) return
    val percent = ((positionSeconds.toFloat() / durationSeconds) * 100).toInt().coerceIn(0, 100)
    val progressDescription = stringResource(R.string.viewer_progress_percent, percent)
    Column(
        modifier = Modifier.fillMaxWidth().semantics { stateDescription = progressDescription },
        verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xxs),
    ) {
        LinearProgressIndicator(
            progress = { (positionSeconds.toFloat() / durationSeconds).coerceIn(0f, 1f) },
            modifier = Modifier.fillMaxWidth().height(RivuneSpacing.xxs),
        )
        Text(
            text = stringResource(R.string.viewer_resume_progress, positionSeconds / 60, durationSeconds / 60),
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            style = MaterialTheme.typography.bodySmall,
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
            Icon(icon, contentDescription = null)
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
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .heightIn(min = if (isTv) RivuneDimensions.buttonHeightTv else RivuneDimensions.fieldHeight)
            .padding(
                horizontal = if (isTv) ViewerTvPadding else ViewerPhonePadding,
                vertical = if (isTv) RivuneSpacing.xl else 0.dp,
            ),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.sm),
    ) {
        val backDescription = stringResource(R.string.viewer_back)
        IconButton(
            onClick = onBack,
            modifier = backModifier
                .size(if (isTv) ViewerTvTarget else ViewerPhoneTarget)
                .semantics { contentDescription = backDescription },
        ) {
            Icon(Icons.AutoMirrored.Rounded.ArrowBack, contentDescription = null)
        }
        Text(
            title,
            modifier = Modifier.weight(1f).semantics { heading() },
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
            style = if (isTv) MaterialTheme.typography.headlineMedium else MaterialTheme.typography.titleLarge,
        )
    }
}

@Composable
private fun SectionTitle(title: String, isTv: Boolean) {
    Text(
        text = title,
        modifier = Modifier.semantics { heading() },
        style = if (isTv) MaterialTheme.typography.headlineMedium else MaterialTheme.typography.titleLarge,
    )
}

@Composable
private fun LoadMoreButton(loading: Boolean, isTv: Boolean, onClick: () -> Unit) {
    RivuneSecondaryButton(
        label = stringResource(R.string.folder_load_more),
        onClick = onClick,
        loading = loading,
        isTv = isTv,
    )
}

@Composable
private fun InlineStatus(
    loading: Boolean,
    failure: UiFailure?,
    onRetry: () -> Unit,
    isTv: Boolean,
    loadingLabel: String,
) {
    if (loading) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(vertical = RivuneSpacing.md)
                .semantics { liveRegion = LiveRegionMode.Polite },
            horizontalArrangement = Arrangement.Center,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            CircularProgressIndicator(modifier = Modifier.size(RivuneSpacing.xl), strokeWidth = 2.dp)
            Spacer(Modifier.width(RivuneSpacing.sm))
            Text(loadingLabel)
        }
    }
    else if (failure != null) {
        Surface(
            modifier = Modifier.fillMaxWidth().semantics { liveRegion = LiveRegionMode.Assertive },
            color = MaterialTheme.colorScheme.errorContainer,
            shape = MaterialTheme.shapes.small,
        ) {
            Row(
                modifier = Modifier.padding(RivuneSpacing.md),
                horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.sm),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Icon(Icons.Rounded.ErrorOutline, contentDescription = null, tint = MaterialTheme.colorScheme.onErrorContainer)
                Text(
                    text = viewerFailureMessage(failure),
                    modifier = Modifier.weight(1f),
                    color = MaterialTheme.colorScheme.onErrorContainer,
                    style = MaterialTheme.typography.bodyMedium,
                )
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

@Composable
private fun InlineWarning(message: String) {
    Surface(
        modifier = Modifier.fillMaxWidth().semantics { liveRegion = LiveRegionMode.Polite },
        color = MaterialTheme.colorScheme.surface,
        shape = RivuneShapes.small,
    ) {
        Text(
            text = message,
            modifier = Modifier.padding(RivuneSpacing.md),
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
            .padding(vertical = RivuneSpacing.xxl),
        horizontalAlignment = Alignment.Start,
        verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
    ) {
        Text(title, modifier = Modifier.semantics { heading() }, style = MaterialTheme.typography.titleLarge)
        Text(body, color = MaterialTheme.colorScheme.onSurfaceVariant, style = MaterialTheme.typography.bodyLarge)
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
