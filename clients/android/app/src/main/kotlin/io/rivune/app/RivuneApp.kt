package io.rivune.app

import android.animation.ValueAnimator
import android.app.Activity
import android.content.ClipData
import android.content.ClipboardManager
import android.os.Build
import android.view.HapticFeedbackConstants
import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.togetherWith
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.BorderStroke
import androidx.compose.animation.AnimatedVisibility
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.rounded.ContentCopy
import androidx.compose.material.icons.automirrored.rounded.Logout
import androidx.compose.material.icons.rounded.Check
import androidx.compose.material.icons.rounded.Dns
import androidx.compose.material.icons.rounded.ErrorOutline
import androidx.compose.material.icons.rounded.Lock
import androidx.compose.material.icons.rounded.Person
import androidx.compose.material.icons.rounded.Refresh
import androidx.compose.material.icons.rounded.SystemUpdate
import androidx.compose.material.icons.rounded.Sync
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
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
import androidx.compose.ui.focus.focusProperties
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import io.rivune.api.Profile
import io.rivune.app.ui.components.RivuneArtwork
import io.rivune.app.ui.components.RivuneBrandLockup
import io.rivune.app.ui.components.RivuneBrandMark
import io.rivune.app.ui.components.RivuneFocusSurface
import io.rivune.app.ui.components.RivunePrimaryButton
import io.rivune.app.ui.components.RivuneScreenHeading
import io.rivune.app.ui.components.RivuneSecondaryButton
import io.rivune.app.ui.components.RivuneSkeleton
import io.rivune.app.ui.components.RivuneTextButton
import io.rivune.app.ui.components.RivuneTextField
import io.rivune.app.ui.components.RivuneTestTags
import io.rivune.app.ui.theme.RivuneBreakpoints
import io.rivune.app.ui.theme.RivuneDimensions
import io.rivune.app.ui.theme.RivuneElevation
import io.rivune.app.ui.theme.RivuneMotion
import io.rivune.app.ui.theme.RivuneShapes
import io.rivune.app.ui.theme.RivuneSuccess
import io.rivune.app.ui.theme.RivuneSpacing
import io.rivune.app.ui.theme.RivuneTheme
import java.util.Locale
import kotlinx.coroutines.delay

@Composable
internal fun RivuneRoot(viewModel: RivuneViewModel, updates: AppUpdateCoordinator, activity: Activity) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val updateState by updates.state.collectAsStateWithLifecycle()
    val inlineFailure = state.destination == AppDestination.Server || state.destination == AppDestination.Pairing

    RivuneTheme {
        Surface(
            modifier = Modifier.fillMaxSize(),
            color = MaterialTheme.colorScheme.background,
        ) {
            Box {
                AnimatedContent(
                    targetState = state.destination,
                    transitionSpec = {
                        fadeIn(tween(RivuneMotion.normal)) togetherWith fadeOut(tween(RivuneMotion.fast))
                    },
                    label = "rivune-destination",
                ) { destination ->
                    when (destination) {
                        AppDestination.Loading -> LoadingScreen()
                        AppDestination.Server -> ServerScreen(
                            serverInput = state.serverInput,
                            updateState = updateState,
                            onCheckForUpdates = updates::checkManually,
                            isBusy = state.isBusy,
                            failure = state.failure,
                            isTv = state.isTv,
                            onConnect = viewModel::connect,
                            onClearFailure = viewModel::clearFailure,
                        )
                        AppDestination.Pairing -> PairingScreen(
                            pairing = state.pairing,
                            pairingAccepted = state.pairingAccepted,
                            isBusy = state.isBusy,
                            failure = state.failure,
                            isTv = state.isTv,
                            onRestart = viewModel::startPairing,
                            onDisconnect = viewModel::disconnectServer,
                        )
                        AppDestination.Profiles -> ProfilesScreen(
                            profiles = state.profiles,
                            isBusy = state.isBusy,
                            isTv = state.isTv,
                            resourceUrl = viewModel::artworkUrl,
                            avatarData = state.profileAvatarData,
                            onSelect = viewModel::selectProfile,
                            onLogout = viewModel::logout,
                            onRefresh = viewModel::refresh,
                        )
                        AppDestination.Viewer -> ViewerShell(
                            state = state,
                            viewModel = viewModel,
                            updateState = updateState,
                            onCheckForUpdates = updates::checkManually,
                        )
                    }
                }

                AnimatedVisibility(
                    visible = state.failure != null && !inlineFailure,
                    modifier = Modifier
                        .align(Alignment.TopCenter)
                        .statusBarsPadding()
                        .padding(horizontal = RivuneSpacing.md, vertical = RivuneSpacing.sm),
                ) {
                    state.failure?.let { failure ->
                        FailureBanner(
                            message = failureMessage(failure),
                            onDismiss = viewModel::clearFailure,
                            isTv = state.isTv,
                        )
                    }
                }

                if (
                    state.isBusy &&
                    state.destination != AppDestination.Loading &&
                    state.destination != AppDestination.Server &&
                    state.destination != AppDestination.Pairing
                ) {
                    BusyIndicator(
                        modifier = Modifier
                            .align(Alignment.BottomEnd)
                            .navigationBarsPadding()
                            .padding(RivuneSpacing.lg),
                    )
                }
            }

            state.pendingProfile?.let { profile ->
                PinDialog(
                    profile = profile,
                    isBusy = state.isBusy,
                    isTv = state.isTv,
                    onSubmit = viewModel::submitPin,
                    onDismiss = viewModel::dismissPin,
                )
            }

            AppUpdateDialog(
                state = updateState,
                isTv = state.isTv,
                onDownload = updates::download,
                onInstall = { updates.install(activity) },
                onDismiss = updates::dismiss,
            )
        }
    }
}

@Composable
private fun AppUpdateDialog(
    state: AppUpdateState,
    isTv: Boolean,
    onDownload: () -> Unit,
    onInstall: () -> Unit,
    onDismiss: () -> Unit,
) {
    val visible = state is AppUpdateState.Available || state is AppUpdateState.Downloading ||
        state is AppUpdateState.ReadyToInstall || state is AppUpdateState.NeedsPermission ||
        state is AppUpdateState.Installing || state is AppUpdateState.Error || state is AppUpdateState.UpToDate
    val confirmFocus = remember { FocusRequester() }
    LaunchedEffect(state, isTv) {
        if (isTv && visible && state !is AppUpdateState.Downloading && state !is AppUpdateState.Installing) {
            confirmFocus.requestFocus()
        }
    }
    if (!visible) return
    val title = when (state) {
        is AppUpdateState.UpToDate -> stringResource(R.string.update_up_to_date_title)
        is AppUpdateState.Available -> stringResource(R.string.update_available_title)
        is AppUpdateState.ReadyToInstall, is AppUpdateState.NeedsPermission -> stringResource(R.string.update_ready_title)
        is AppUpdateState.Error -> stringResource(R.string.update_error_title)
        else -> stringResource(R.string.update_working_title)
    }
    val body = when (state) {
        is AppUpdateState.UpToDate -> stringResource(R.string.update_up_to_date, state.currentVersion)
        is AppUpdateState.Available -> stringResource(R.string.update_available_body, state.manifest.version)
        is AppUpdateState.Downloading -> stringResource(R.string.update_downloading)
        is AppUpdateState.ReadyToInstall -> stringResource(R.string.update_ready_body)
        is AppUpdateState.NeedsPermission -> stringResource(R.string.update_permission_body)
        is AppUpdateState.Installing -> stringResource(R.string.update_installing)
        is AppUpdateState.Error -> stringResource(R.string.update_error_body)
        else -> ""
    }
    AlertDialog(
        onDismissRequest = {
            if (state !is AppUpdateState.Downloading && state !is AppUpdateState.Installing) onDismiss()
        },
        icon = { Icon(Icons.Rounded.SystemUpdate, contentDescription = null) },
        title = { Text(title) },
        text = {
            Row(horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.md), verticalAlignment = Alignment.CenterVertically) {
                if (state is AppUpdateState.Downloading || state is AppUpdateState.Installing) {
                    CircularProgressIndicator(modifier = Modifier.size(24.dp), strokeWidth = 2.dp)
                }
                Text(body)
            }
        },
        confirmButton = {
            when (state) {
                is AppUpdateState.Available -> RivunePrimaryButton(
                    label = stringResource(R.string.update_download), onClick = onDownload, isTv = isTv,
                    modifier = Modifier.focusRequester(confirmFocus),
                )
                is AppUpdateState.ReadyToInstall, is AppUpdateState.NeedsPermission -> RivunePrimaryButton(
                    label = stringResource(R.string.update_install), onClick = onInstall, isTv = isTv,
                    modifier = Modifier.focusRequester(confirmFocus),
                )
                is AppUpdateState.Error, is AppUpdateState.UpToDate -> RivunePrimaryButton(
                    label = stringResource(R.string.error_dismiss), onClick = onDismiss, isTv = isTv,
                    modifier = Modifier.focusRequester(confirmFocus),
                )
                else -> Unit
            }
        },
        dismissButton = {
            if (state is AppUpdateState.Available || state is AppUpdateState.ReadyToInstall || state is AppUpdateState.NeedsPermission) {
                RivuneTextButton(label = stringResource(R.string.update_later), onClick = onDismiss, isTv = isTv)
            }
        },
    )
}

@Composable
private fun LoadingScreen() {
    val animationsEnabled = remember { ValueAnimator.areAnimatorsEnabled() }
    val transition = rememberInfiniteTransition(label = "opening-rivune")
    val pulse by transition.animateFloat(
        initialValue = 0.45f,
        targetValue = 1f,
        animationSpec = infiniteRepeatable(
            animation = tween(900),
            repeatMode = RepeatMode.Reverse,
        ),
        label = "opening-rivune-pulse",
    )
    Box(
        modifier = Modifier
            .fillMaxSize()
            .windowInsetsPadding(WindowInsets.safeDrawing)
            .padding(RivuneSpacing.xl),
        contentAlignment = Alignment.Center,
    ) {
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            RivuneBrandMark(size = 64.dp, mark = stringResource(R.string.brand_mark))
            Spacer(Modifier.height(RivuneSpacing.xl))
            Text(
                text = stringResource(R.string.loading),
                style = MaterialTheme.typography.titleLarge,
            )
            Spacer(Modifier.height(RivuneSpacing.md))
            Row(
                modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
                horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.sm),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Box(
                    Modifier
                        .size(8.dp)
                        .graphicsLayer { alpha = if (animationsEnabled) pulse else 1f }
                        .clip(CircleShape)
                        .background(MaterialTheme.colorScheme.primary),
                )
                Text(
                    text = stringResource(R.string.loading_in_progress),
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    style = MaterialTheme.typography.bodyMedium,
                )
            }
        }
    }
}

@Composable
internal fun ServerScreen(
    serverInput: String,
    isBusy: Boolean,
    failure: UiFailure?,
    isTv: Boolean,
    updateState: AppUpdateState = AppUpdateState.Idle,
    onConnect: (String) -> Unit,
    onClearFailure: () -> Unit,
    onCheckForUpdates: () -> Unit = {},
) {
    var server by remember(serverInput) { mutableStateOf(serverInput) }
    val view = LocalView.current
    val failureText = failure?.let { failureMessage(it) }
    val submit = { if (server.isNotBlank() && !isBusy) onConnect(server.trim()) }
    val inputFocus = remember { FocusRequester() }
    val submitFocus = remember { FocusRequester() }
    val updateFocus = remember { FocusRequester() }

    LaunchedEffect(failure) {
        if (failure != null) performRejectHaptic(view)
    }

    AuthFrame(isTv = isTv) {
        RivuneScreenHeading(
            eyebrow = stringResource(R.string.server_eyebrow),
            title = stringResource(R.string.server_title),
            body = stringResource(R.string.server_body),
            isTv = isTv,
        )
        Spacer(Modifier.height(if (isTv) RivuneSpacing.xxl else RivuneSpacing.xl))
        RivuneTextField(
            value = server,
            onValueChange = {
                server = it
                if (failure != null) onClearFailure()
            },
            label = stringResource(R.string.server_label),
            modifier = Modifier
                .fillMaxWidth()
                .focusRequester(inputFocus)
                .focusProperties { down = if (server.isNotBlank()) submitFocus else updateFocus }
                .onPreviewKeyEvent { event ->
                    if (isTv && event.type == KeyEventType.KeyDown && event.key == Key.DirectionDown) {
                        if (server.isNotBlank()) submitFocus.requestFocus() else updateFocus.requestFocus()
                        true
                    } else {
                        false
                    }
                }
                .testTag(RivuneTestTags.ServerInput),
            placeholder = stringResource(R.string.server_placeholder),
            supportingText = failureText ?: stringResource(R.string.server_supporting),
            enabled = !isBusy,
            isError = failure != null,
            isTv = isTv,
            leadingIcon = Icons.Rounded.Dns,
            keyboardOptions = KeyboardOptions(
                keyboardType = KeyboardType.Uri,
                imeAction = ImeAction.Go,
            ),
            keyboardActions = KeyboardActions(onGo = { submit() }),
        )
        Spacer(Modifier.height(RivuneSpacing.md))
        RivunePrimaryButton(
            label = stringResource(
                when {
                    isBusy -> R.string.server_connecting
                    failure != null -> R.string.server_retry
                    else -> R.string.server_connect
                },
            ),
            onClick = submit,
            modifier = Modifier
                .fillMaxWidth()
                .focusRequester(submitFocus)
                .focusProperties { up = inputFocus; down = updateFocus }
                .testTag(RivuneTestTags.ServerSubmit),
            enabled = server.isNotBlank(),
            isTv = isTv,
            loading = isBusy,
            loadingDescription = stringResource(R.string.server_loading_description),
        )
        Spacer(Modifier.height(RivuneSpacing.sm))
        RivuneTextButton(
            label = stringResource(R.string.update_check),
            onClick = onCheckForUpdates,
            enabled = updateState !is AppUpdateState.Checking && updateState !is AppUpdateState.Downloading &&
                updateState !is AppUpdateState.Installing,
            isTv = isTv,
            icon = Icons.Rounded.SystemUpdate,
            modifier = Modifier
                .fillMaxWidth()
                .focusRequester(updateFocus)
                .focusProperties { up = if (server.isNotBlank()) submitFocus else inputFocus },
        )
    }
}


@Composable
internal fun PairingScreen(
    pairing: PairingInfo?,
    pairingAccepted: Boolean,
    isBusy: Boolean,
    failure: UiFailure?,
    isTv: Boolean,
    onRestart: () -> Unit,
    onDisconnect: () -> Unit,
) {
    val view = LocalView.current
    val clipboard = remember(view) { view.context.getSystemService(ClipboardManager::class.java) }
    var copied by remember(pairing?.userCode) { mutableStateOf(false) }
    var confirmDisconnect by remember { mutableStateOf(false) }
    val expired = failure == UiFailure.PAIRING_EXPIRED
    val visualState = when {
        pairingAccepted -> PairingVisualState.SUCCESS
        pairing != null -> PairingVisualState.CODE
        isBusy -> PairingVisualState.LOADING
        failure != null -> PairingVisualState.ERROR
        else -> PairingVisualState.LOADING
    }

    LaunchedEffect(copied) {
        if (copied) {
            delay(1_500)
            copied = false
        }
    }
    LaunchedEffect(pairingAccepted) {
        if (pairingAccepted) performConfirmHaptic(view)
    }
    LaunchedEffect(failure) {
        if (failure != null) performRejectHaptic(view)
    }

    BoxWithConstraints(
        modifier = Modifier
            .fillMaxSize()
            .windowInsetsPadding(WindowInsets.safeDrawing),
        contentAlignment = Alignment.TopCenter,
    ) {
        val tablet = maxWidth >= RivuneBreakpoints.compact
        val horizontalPadding = when {
            isTv -> RivuneSpacing.display
            tablet -> RivuneSpacing.huge
            else -> RivuneSpacing.xl
        }
        val contentMaxWidth = when {
            isTv -> 880.dp
            tablet -> RivuneDimensions.contentMaxTablet
            else -> RivuneDimensions.contentMax
        }
        val compactHeight = maxHeight < 720.dp
        val sectionGap = when {
            compactHeight -> RivuneSpacing.lg
            tablet -> RivuneSpacing.xxl
            else -> RivuneSpacing.xl
        }

        Column(
            modifier = Modifier
                .widthIn(max = contentMaxWidth)
                .fillMaxWidth()
                .heightIn(min = maxHeight)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = horizontalPadding, vertical = if (compactHeight) RivuneSpacing.md else RivuneSpacing.xl),
            verticalArrangement = Arrangement.Center,
        ) {
            RivuneBrandLockup(
                name = stringResource(R.string.app_name),
                mark = stringResource(R.string.brand_mark),
                tagline = if (tablet || isTv) stringResource(R.string.brand_tagline) else null,
                markSize = if (isTv) 64.dp else 48.dp,
            )
            Spacer(Modifier.height(if (compactHeight) RivuneSpacing.lg else RivuneSpacing.xl))
            RivuneScreenHeading(
                eyebrow = stringResource(R.string.pairing_eyebrow),
                title = stringResource(R.string.pairing_title),
                body = stringResource(R.string.pairing_body),
                isTv = isTv,
            )
            Spacer(Modifier.height(sectionGap))

            AnimatedContent(
                targetState = visualState,
                transitionSpec = {
                    fadeIn(tween(RivuneMotion.normal)) togetherWith fadeOut(tween(RivuneMotion.fast))
                },
                label = "pairing-state",
            ) { state ->
                when (state) {
                    PairingVisualState.SUCCESS -> PairingSuccessState(isTv)
                    PairingVisualState.LOADING -> PairingLoadingState(isTv)
                    PairingVisualState.ERROR -> PairingIssueState(
                        title = stringResource(if (expired) R.string.pairing_expired_title else R.string.error_title),
                        body = if (expired) {
                            stringResource(R.string.pairing_expired_body)
                        } else {
                            failure?.let { failureMessage(it) }.orEmpty()
                        },
                        isTv = isTv,
                    )
                    PairingVisualState.CODE -> Column(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalAlignment = Alignment.CenterHorizontally,
                    ) {
                        PairingValue(
                            label = stringResource(R.string.pairing_code_label),
                            value = checkNotNull(pairing).userCode,
                            isTv = isTv,
                            copied = copied,
                            onCopy = if (isTv) null else {
                                {
                                    clipboard.setPrimaryClip(ClipData.newPlainText("Rivune pairing code", pairing.userCode))
                                    copied = true
                                    performClickHaptic(view)
                                }
                            },
                        )
                        Spacer(Modifier.height(RivuneSpacing.lg))
                        if (failure == null) {
                            PairingWaitingState(copied = copied, isTv = isTv)
                        } else {
                            PairingInlineIssue(message = failureMessage(failure), isTv = isTv)
                        }
                    }
                }
            }

            Spacer(Modifier.height(sectionGap))
            AnimatedVisibility(visible = !pairingAccepted) {
                Column(modifier = Modifier.fillMaxWidth()) {
                    if (pairing == null && failure != null) {
                        RivunePrimaryButton(
                            label = stringResource(if (isBusy) R.string.pairing_restart_loading else R.string.pairing_restart),
                            onClick = onRestart,
                            modifier = Modifier.fillMaxWidth().testTag(RivuneTestTags.PairingRestart),
                            enabled = !isBusy,
                            loading = isBusy,
                            isTv = isTv,
                            icon = Icons.Rounded.Refresh,
                        )
                    } else {
                        RivuneSecondaryButton(
                            label = stringResource(if (isBusy) R.string.pairing_restart_loading else R.string.pairing_restart),
                            onClick = onRestart,
                            modifier = Modifier.fillMaxWidth().testTag(RivuneTestTags.PairingRestart),
                            enabled = !isBusy,
                            loading = isBusy,
                            isTv = isTv,
                            icon = Icons.Rounded.Refresh,
                        )
                    }
                    Spacer(Modifier.height(RivuneSpacing.xs))
                    RivuneTextButton(
                        label = stringResource(R.string.pairing_disconnect),
                        onClick = { confirmDisconnect = true },
                        modifier = Modifier.fillMaxWidth().testTag(RivuneTestTags.PairingDisconnect),
                        enabled = !isBusy,
                        isTv = isTv,
                        destructive = true,
                    )
                }
            }
        }
    }

    if (confirmDisconnect) {
        AlertDialog(
            onDismissRequest = { confirmDisconnect = false },
            title = { Text(stringResource(R.string.pairing_disconnect_confirm_title)) },
            text = { Text(stringResource(R.string.pairing_disconnect_confirm_body)) },
            confirmButton = {
                RivuneTextButton(
                    modifier = Modifier.testTag(RivuneTestTags.PairingDisconnectConfirm),
                    label = stringResource(R.string.pairing_disconnect_confirm),
                    onClick = {
                        confirmDisconnect = false
                        onDisconnect()
                    },
                    destructive = true,
                    isTv = isTv,
                )
            },
            dismissButton = {
                RivuneTextButton(
                    label = stringResource(R.string.pin_cancel),
                    onClick = { confirmDisconnect = false },
                    isTv = isTv,
                )
            },
            containerColor = MaterialTheme.colorScheme.surfaceContainerHigh,
            shape = RivuneShapes.large,
        )
    }
}

@Composable
private fun ProfilesScreen(
    profiles: List<Profile>,
    isBusy: Boolean,
    isTv: Boolean,
    resourceUrl: (String?) -> String?,
    avatarData: Map<java.util.UUID, ByteArray>,
    onSelect: (Profile) -> Unit,
    onLogout: () -> Unit,
    onRefresh: () -> Unit,
) {
    BoxWithConstraints(
        modifier = Modifier
            .fillMaxSize()
            .statusBarsPadding()
            .navigationBarsPadding(),
    ) {
        val wide = maxWidth >= 720.dp
        val horizontalPadding = responsiveHorizontalPadding(maxWidth, isTv)
        val columns = when {
            isTv -> 5
            maxWidth >= 1100.dp -> 5
            wide -> 4
            else -> 2
        }
        Column(modifier = Modifier.fillMaxSize()) {
            TopBar(
                isTv = isTv,
                actions = {
                    ToolbarAction(
                        label = stringResource(R.string.home_refresh),
                        icon = Icons.Rounded.Refresh,
                        onClick = onRefresh,
                        enabled = !isBusy,
                        isTv = isTv,
                    )
                    ToolbarAction(
                        label = stringResource(R.string.logout),
                        icon = Icons.AutoMirrored.Rounded.Logout,
                        onClick = onLogout,
                        enabled = !isBusy,
                        isTv = isTv,
                    )
                },
            )
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .weight(1f)
                    .padding(horizontal = horizontalPadding),
            ) {
                RivuneScreenHeading(
                    eyebrow = stringResource(R.string.profiles_eyebrow),
                    title = stringResource(R.string.profiles_title),
                    body = stringResource(R.string.profiles_body),
                    isTv = isTv,
                )
                Spacer(Modifier.height(if (isTv) 36.dp else 24.dp))
                if (profiles.isEmpty()) {
                    EmptyState(
                        title = stringResource(R.string.profiles_empty_title),
                        body = stringResource(R.string.profiles_empty_body),
                        modifier = Modifier.fillMaxWidth(),
                    )
                } else {
                    LazyVerticalGrid(
                        columns = GridCells.Fixed(columns),
                        modifier = Modifier.fillMaxSize(),
                        horizontalArrangement = Arrangement.spacedBy(if (isTv) 24.dp else 16.dp),
                        verticalArrangement = Arrangement.spacedBy(if (isTv) 24.dp else 16.dp),
                    ) {
                        items(profiles, key = { it.id }) { profile ->
                            ProfileCard(
                                profile = profile,
                                imageModel = avatarData[profile.id]
                                    ?: profile.avatar.url.takeIf { profile.avatar.kind == "preset" }?.let(resourceUrl),
                                enabled = profile.enabled && profile.accessible && !isBusy,
                                isTv = isTv,
                                onClick = { onSelect(profile) },
                            )
                        }
                    }
                }
            }
        }
    }
}




@Composable
private fun AuthFrame(
    isTv: Boolean,
    contentMaxWidth: Dp = RivuneDimensions.contentMax,
    content: @Composable ColumnScope.() -> Unit,
) {
    val fontScale = LocalDensity.current.fontScale
    BoxWithConstraints(
        modifier = Modifier
            .fillMaxSize()
            .windowInsetsPadding(WindowInsets.safeDrawing)
            .imePadding(),
    ) {
        val wide = maxWidth >= RivuneBreakpoints.expanded && maxHeight >= 600.dp && fontScale < 1.5f
        Row(modifier = Modifier.fillMaxSize()) {
            if (wide) {
                Box(
                    modifier = Modifier
                        .weight(0.4f)
                        .fillMaxHeight()
                        .background(MaterialTheme.colorScheme.surfaceContainerLowest),
                    contentAlignment = Alignment.Center,
                ) {
                    RivuneBrandLockup(
                        name = stringResource(R.string.app_name),
                        mark = stringResource(R.string.brand_mark),
                        tagline = stringResource(R.string.brand_tagline),
                        modifier = Modifier.padding(if (isTv) RivuneSpacing.display else RivuneSpacing.xxxl),
                        markSize = if (isTv) 80.dp else 64.dp,
                    )
                }
            }
            Box(
                modifier = Modifier
                    .weight(if (wide) 0.6f else 1f)
                    .fillMaxHeight()
                    .verticalScroll(rememberScrollState())
                    .padding(
                        horizontal = if (isTv) RivuneSpacing.display else RivuneSpacing.xl,
                        vertical = if (isTv) RivuneSpacing.huge else RivuneSpacing.xxl,
                    ),
                contentAlignment = Alignment.Center,
            ) {
                Column(
                    modifier = Modifier
                        .widthIn(max = contentMaxWidth)
                        .fillMaxWidth(),
                ) {
                    if (!wide) {
                        RivuneBrandLockup(
                            name = stringResource(R.string.app_name),
                            mark = stringResource(R.string.brand_mark),
                            tagline = if (this@BoxWithConstraints.maxWidth >= RivuneBreakpoints.compact) stringResource(R.string.brand_tagline) else null,
                            markSize = if (isTv) 64.dp else 48.dp,
                        )
                        Spacer(Modifier.height(if (isTv) RivuneSpacing.xxxl else RivuneSpacing.xxl))
                    }
                    content()
                }
            }
        }
    }
}







@Composable
private fun PairingValue(
    label: String,
    value: String,
    isTv: Boolean,
    copied: Boolean,
    onCopy: (() -> Unit)?,
) {
    val fontScale = LocalDensity.current.fontScale.coerceAtLeast(1f)
    val copyDescription = stringResource(if (copied) R.string.pairing_copied else R.string.pairing_copy)
    Surface(
        modifier = Modifier
            .fillMaxWidth()
            .testTag(RivuneTestTags.PairingCode)
            .semantics {
                contentDescription = "$label, $value"
                if (copied) stateDescription = copyDescription
            }
            .clickable(
                enabled = onCopy != null,
                onClickLabel = copyDescription,
                role = Role.Button,
                onClick = { onCopy?.invoke() },
            ),
        shape = RivuneShapes.extraLarge,
        color = MaterialTheme.colorScheme.surfaceContainer,
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
    ) {
        BoxWithConstraints {
            val baseSize = when {
                isTv -> 72f
                maxWidth >= 520.dp -> 62f
                else -> 43f
            }
            val codeSize = (baseSize / fontScale).sp
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(
                        horizontal = if (isTv) RivuneSpacing.xxxl else RivuneSpacing.xl,
                        vertical = if (isTv) RivuneSpacing.xxxl else RivuneSpacing.xxl,
                    ),
                horizontalAlignment = Alignment.CenterHorizontally,
            ) {
                Box(
                    Modifier
                        .width(48.dp)
                        .height(3.dp)
                        .clip(RivuneShapes.pill)
                        .background(MaterialTheme.colorScheme.primary),
                )
                Spacer(Modifier.height(RivuneSpacing.md))
                Text(
                    text = label,
                    maxLines = 1,
                    softWrap = false,
                    color = MaterialTheme.colorScheme.primary,
                    style = MaterialTheme.typography.labelMedium,
                    textAlign = TextAlign.Center,
                )
                Spacer(Modifier.height(if (isTv) RivuneSpacing.md else RivuneSpacing.sm))
                Text(
                    text = value,
                    maxLines = 1,
                    softWrap = false,
                    modifier = Modifier.fillMaxWidth(),
                    color = MaterialTheme.colorScheme.onSurface,
                    fontFamily = FontFamily.Monospace,
                    fontWeight = FontWeight.SemiBold,
                    fontSize = codeSize,
                    lineHeight = codeSize * 1.15f,
                    letterSpacing = (if (isTv) 5f else 2f).sp,
                    textAlign = TextAlign.Center,
                )
                if (onCopy != null) {
                    Spacer(Modifier.height(RivuneSpacing.md))
                    Row(
                        horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Icon(
                            imageVector = if (copied) Icons.Rounded.Check else Icons.Rounded.ContentCopy,
                            contentDescription = null,
                            modifier = Modifier.size(17.dp),
                            tint = if (copied) RivuneSuccess else MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        Text(
                            text = copyDescription,
                            color = if (copied) RivuneSuccess else MaterialTheme.colorScheme.onSurfaceVariant,
                            style = MaterialTheme.typography.labelSmall,
                        )
                    }
                }
            }
        }
    }
}

private enum class PairingVisualState {
    LOADING,
    CODE,
    SUCCESS,
    ERROR,
}

@Composable
private fun PairingLoadingState(isTv: Boolean) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .semantics { liveRegion = LiveRegionMode.Polite },
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        RivuneSkeleton(
            modifier = Modifier
                .fillMaxWidth()
                .height(if (isTv) 210.dp else 174.dp),
            shape = RivuneShapes.extraLarge,
        )
        Spacer(Modifier.height(RivuneSpacing.lg))
        Text(
            text = stringResource(R.string.pairing_restart_loading),
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            style = if (isTv) MaterialTheme.typography.bodyLarge else MaterialTheme.typography.bodyMedium,
        )
    }
}

@Composable
private fun PairingWaitingState(copied: Boolean, isTv: Boolean) {
    AnimatedContent(
        targetState = copied,
        transitionSpec = {
            fadeIn(tween(RivuneMotion.fast)) togetherWith fadeOut(tween(RivuneMotion.fast))
        },
        label = "pairing-feedback",
    ) { codeCopied ->
        if (codeCopied) {
            Row(
                modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
                horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.sm),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Icon(Icons.Rounded.Check, contentDescription = null, tint = RivuneSuccess)
                Text(
                    text = stringResource(R.string.pairing_copied),
                    color = RivuneSuccess,
                    style = if (isTv) MaterialTheme.typography.bodyLarge else MaterialTheme.typography.bodyMedium,
                )
            }
        } else {
            val animationsEnabled = remember { ValueAnimator.areAnimatorsEnabled() }
            val transition = rememberInfiniteTransition(label = "pairing-wait")
            val pulse by transition.animateFloat(
                initialValue = 0.55f,
                targetValue = 1f,
                animationSpec = infiniteRepeatable(
                    animation = tween(1_100),
                    repeatMode = RepeatMode.Reverse,
                ),
                label = "pairing-wait-pulse",
            )
            val waitingDescription = stringResource(R.string.pairing_waiting_description)
            Row(
                modifier = Modifier.semantics {
                    liveRegion = LiveRegionMode.Polite
                    stateDescription = waitingDescription
                },
                horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.sm),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Box(
                    Modifier
                        .size(if (isTv) 12.dp else 10.dp)
                        .graphicsLayer {
                            val scale = if (animationsEnabled) pulse else 1f
                            scaleX = scale
                            scaleY = scale
                            alpha = if (animationsEnabled) pulse else 1f
                        }
                        .clip(CircleShape)
                        .background(MaterialTheme.colorScheme.primary),
                )
                Text(
                    text = stringResource(R.string.pairing_waiting),
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    style = if (isTv) MaterialTheme.typography.bodyLarge else MaterialTheme.typography.bodyMedium,
                )
            }
        }
    }
}

@Composable
private fun PairingSuccessState(isTv: Boolean) {
    Surface(
        modifier = Modifier
            .fillMaxWidth()
            .testTag(RivuneTestTags.PairingSuccess)
            .semantics(mergeDescendants = true) { liveRegion = LiveRegionMode.Assertive },
        color = MaterialTheme.colorScheme.surfaceContainer,
        shape = RivuneShapes.extraLarge,
        border = BorderStroke(1.dp, RivuneSuccess.copy(alpha = 0.45f)),
    ) {
        Column(
            modifier = Modifier.padding(if (isTv) RivuneSpacing.huge else RivuneSpacing.xxl),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(RivuneSpacing.sm),
        ) {
            Surface(shape = CircleShape, color = RivuneSuccess.copy(alpha = 0.14f)) {
                Icon(
                    imageVector = Icons.Rounded.Check,
                    contentDescription = null,
                    modifier = Modifier.padding(RivuneSpacing.md).size(if (isTv) 38.dp else 30.dp),
                    tint = RivuneSuccess,
                )
            }
            Text(
                text = stringResource(R.string.pairing_success_title),
                style = if (isTv) MaterialTheme.typography.headlineMedium else MaterialTheme.typography.titleLarge,
            )
            Text(
                text = stringResource(R.string.pairing_success_body),
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                style = MaterialTheme.typography.bodyMedium,
                textAlign = TextAlign.Center,
            )
        }
    }
}

@Composable
private fun PairingIssueState(title: String, body: String, isTv: Boolean) {
    Surface(
        modifier = Modifier
            .fillMaxWidth()
            .testTag(RivuneTestTags.PairingIssue)
            .semantics(mergeDescendants = true) { liveRegion = LiveRegionMode.Assertive },
        color = MaterialTheme.colorScheme.errorContainer,
        shape = RivuneShapes.large,
    ) {
        Row(
            modifier = Modifier.padding(if (isTv) RivuneSpacing.xxl else RivuneSpacing.xl),
            horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
            verticalAlignment = Alignment.Top,
        ) {
            Icon(Icons.Rounded.ErrorOutline, contentDescription = null, tint = MaterialTheme.colorScheme.onErrorContainer)
            Column(verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xs)) {
                Text(title, color = MaterialTheme.colorScheme.onErrorContainer, style = MaterialTheme.typography.titleMedium)
                Text(body, color = MaterialTheme.colorScheme.onErrorContainer, style = MaterialTheme.typography.bodyMedium)
            }
        }
    }
}

@Composable
private fun PairingInlineIssue(message: String, isTv: Boolean) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .semantics(mergeDescendants = true) { liveRegion = LiveRegionMode.Assertive }
            .padding(horizontal = RivuneSpacing.xs),
        horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.sm),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(Icons.Rounded.ErrorOutline, contentDescription = null, tint = MaterialTheme.colorScheme.error)
        Text(
            text = message,
            color = MaterialTheme.colorScheme.onErrorContainer,
            style = if (isTv) MaterialTheme.typography.bodyLarge else MaterialTheme.typography.bodyMedium,
        )
    }
}

private fun performConfirmHaptic(view: android.view.View) {
    view.performHapticFeedback(
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) HapticFeedbackConstants.CONFIRM else HapticFeedbackConstants.VIRTUAL_KEY,
    )
}

private fun performRejectHaptic(view: android.view.View) {
    view.performHapticFeedback(
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) HapticFeedbackConstants.REJECT else HapticFeedbackConstants.LONG_PRESS,
    )
}

private fun performClickHaptic(view: android.view.View) {
    view.performHapticFeedback(HapticFeedbackConstants.CLOCK_TICK)
}

@Composable
private fun ProfileCard(
    profile: Profile,
    imageModel: Any?,
    enabled: Boolean,
    isTv: Boolean,
    onClick: () -> Unit,
) {
    val status = when {
        !profile.enabled || !profile.accessible -> stringResource(R.string.profile_unavailable)
        profile.hasPin -> stringResource(R.string.profile_pin_required)
        else -> null
    }
    RivuneFocusSurface(
        onClick = onClick,
        enabled = enabled,
        isTv = isTv,
        modifier = Modifier
            .fillMaxWidth()
            .semantics {
                contentDescription = listOfNotNull(profile.name, status).joinToString(", ")
                if (status != null) stateDescription = status
            },
        shape = RivuneShapes.large,
    ) {
        Column(
            modifier = Modifier.padding(if (isTv) RivuneSpacing.xl else RivuneSpacing.md),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            RivuneArtwork(
                model = imageModel,
                fallback = profile.name.initial(),
                modifier = Modifier
                    .fillMaxWidth()
                    .aspectRatio(1f)
                    .clip(CircleShape),
                contentDescription = null,
            )
            Spacer(Modifier.height(RivuneSpacing.md))
            Text(
                text = profile.name,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                style = if (isTv) MaterialTheme.typography.titleLarge else MaterialTheme.typography.titleMedium,
            )
            status?.let {
                Spacer(Modifier.height(RivuneSpacing.xxs))
                Text(
                    text = it,
                    color = if (enabled) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    style = MaterialTheme.typography.bodyMedium,
                )
            }
        }
    }
}



@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun TopBar(
    isTv: Boolean,
    actions: @Composable () -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(
                horizontal = if (isTv) RivuneSpacing.huge else RivuneSpacing.md,
                vertical = RivuneSpacing.sm,
            ),
        horizontalArrangement = Arrangement.SpaceBetween,
        verticalAlignment = Alignment.CenterVertically,
    ) {
        RivuneBrandLockup(
            name = stringResource(R.string.app_name),
            mark = stringResource(R.string.brand_mark),
            modifier = Modifier.weight(1f),
            markSize = if (isTv) RivuneDimensions.touchTargetTv else RivuneDimensions.touchTarget,
        )
        FlowRow(
            horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.xxs),
            verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xxs),
        ) {
            actions()
        }
    }
}

@Composable
private fun ToolbarAction(
    label: String,
    icon: ImageVector,
    onClick: () -> Unit,
    enabled: Boolean,
    isTv: Boolean,
) {
    if (isTv) {
        RivuneTextButton(
            label = label,
            onClick = onClick,
            enabled = enabled,
            isTv = true,
            icon = icon,
        )
    } else {
        RivuneFocusSurface(
            onClick = onClick,
            enabled = enabled,
            shape = CircleShape,
            modifier = Modifier
                .size(RivuneDimensions.touchTarget)
                .semantics { contentDescription = label },
        ) {
            Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                Icon(imageVector = icon, contentDescription = null)
            }
        }
    }
}

@Composable
private fun FailureBanner(message: String, onDismiss: () -> Unit, isTv: Boolean) {
    Surface(
        modifier = Modifier
            .widthIn(max = RivuneDimensions.contentMaxTablet)
            .fillMaxWidth()
            .semantics {
                liveRegion = LiveRegionMode.Assertive
                contentDescription = message
            },
        color = MaterialTheme.colorScheme.errorContainer,
        contentColor = MaterialTheme.colorScheme.onErrorContainer,
        shape = RivuneShapes.large,
        tonalElevation = RivuneElevation.raised,
    ) {
        Row(
            modifier = Modifier.padding(
                start = RivuneSpacing.md,
                top = RivuneSpacing.sm,
                end = RivuneSpacing.xs,
                bottom = RivuneSpacing.sm,
            ),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.sm),
        ) {
            Icon(Icons.Rounded.ErrorOutline, contentDescription = null)
            Column(modifier = Modifier.weight(1f)) {
                Text(stringResource(R.string.error_title), style = MaterialTheme.typography.labelLarge)
                Text(message, style = MaterialTheme.typography.bodyMedium)
            }
            RivuneTextButton(
                label = stringResource(R.string.error_dismiss),
                onClick = onDismiss,
                isTv = isTv,
            )
        }
    }
}

@Composable
private fun PinDialog(
    profile: Profile,
    isBusy: Boolean,
    isTv: Boolean,
    onSubmit: (String) -> Unit,
    onDismiss: () -> Unit,
) {
    var pin by remember(profile.id) { mutableStateOf("") }
    val submit = {
        if (pin.length in 4..8 && !isBusy) {
            val submittedPin = pin
            pin = ""
            onSubmit(submittedPin)
        }
    }
    val dismiss = {
        pin = ""
        onDismiss()
    }

    AlertDialog(
        onDismissRequest = { if (!isBusy) dismiss() },
        title = { Text(stringResource(R.string.pin_title)) },
        text = {
            Column {
                Text(
                    text = stringResource(R.string.pin_body, profile.name),
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(RivuneSpacing.md))
                RivuneTextField(
                    value = pin,
                    onValueChange = { value -> pin = value.filter(Char::isDigit).take(8) },
                    label = stringResource(R.string.pin_label),
                    enabled = !isBusy,
                    isTv = isTv,
                    leadingIcon = Icons.Rounded.Lock,
                    visualTransformation = PasswordVisualTransformation(),
                    keyboardOptions = KeyboardOptions(
                        keyboardType = KeyboardType.NumberPassword,
                        imeAction = ImeAction.Done,
                    ),
                    keyboardActions = KeyboardActions(onDone = { submit() }),
                )
            }
        },
        confirmButton = {
            RivunePrimaryButton(
                label = stringResource(if (isBusy) R.string.pin_submitting else R.string.pin_submit),
                onClick = submit,
                enabled = pin.length in 4..8,
                loading = isBusy,
                isTv = isTv,
            )
        },
        dismissButton = {
            RivuneTextButton(
                label = stringResource(R.string.pin_cancel),
                onClick = dismiss,
                enabled = !isBusy,
                isTv = isTv,
            )
        },
        containerColor = MaterialTheme.colorScheme.surfaceContainer,
    )
}

@Composable
private fun EmptyState(title: String, body: String, modifier: Modifier = Modifier) {
    Surface(
        modifier = modifier,
        color = MaterialTheme.colorScheme.surface,
        shape = MaterialTheme.shapes.large,
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
    ) {
        Column(modifier = Modifier.padding(RivuneSpacing.xxl)) {
            Text(title, style = MaterialTheme.typography.titleLarge)
            Spacer(Modifier.height(RivuneSpacing.xs))
            Text(
                text = body,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                style = MaterialTheme.typography.bodyMedium,
            )
        }
    }
}

@Composable
private fun SectionLabel(label: String) {
    Text(
        text = label,
        style = MaterialTheme.typography.titleLarge,
    )
}

@Composable
private fun BusyIndicator(modifier: Modifier = Modifier) {
    val description = stringResource(R.string.loading_in_progress)
    Surface(
        modifier = modifier.semantics {
            liveRegion = LiveRegionMode.Polite
            contentDescription = description
        },
        shape = CircleShape,
        color = MaterialTheme.colorScheme.surfaceVariant,
        tonalElevation = RivuneElevation.raised,
    ) {
        CircularProgressIndicator(
            modifier = Modifier.padding(RivuneSpacing.sm).size(RivuneSpacing.xl),
            color = MaterialTheme.colorScheme.primary,
            strokeWidth = 2.dp,
        )
    }
}

@Composable
private fun failureMessage(failure: UiFailure): String {
    val resource = when (failure) {
        UiFailure.SERVER_INVALID -> R.string.error_invalid_server
        UiFailure.SERVER_UNREACHABLE -> R.string.error_network
        UiFailure.PROTOCOL_INCOMPATIBLE -> R.string.error_incompatible_server
        UiFailure.SETUP_REQUIRED -> R.string.error_setup_required
        UiFailure.DEVICE_LIMIT -> R.string.error_device_limit
        UiFailure.PAIRING_START -> R.string.error_pairing_start
        UiFailure.PAIRING_EXPIRED -> R.string.error_pairing_expired
        UiFailure.PAIRING_FAILED -> R.string.error_pairing_failed
        UiFailure.PROFILE_PIN_INVALID -> R.string.error_pin
        UiFailure.PROFILE_PIN_RATE_LIMITED -> R.string.error_pin_rate_limited
        UiFailure.PROFILE_UNAVAILABLE -> R.string.error_profile
        UiFailure.CONTENT_LOAD -> R.string.error_content_load
        UiFailure.SESSION_EXPIRED -> R.string.error_session_expired
        UiFailure.PLAYBACK -> R.string.viewer_error_playback
        UiFailure.ACTION -> R.string.viewer_error_action
        UiFailure.NO_PROFILES -> R.string.error_no_profiles
        UiFailure.LOGOUT_FAILED -> R.string.error_logout_failed
        UiFailure.UNKNOWN -> R.string.error_generic
    }
    return stringResource(resource)
}

private fun responsiveHorizontalPadding(width: Dp, isTv: Boolean): Dp = when {
    isTv -> RivuneSpacing.display
    width >= RivuneBreakpoints.wide -> RivuneSpacing.display
    width >= RivuneBreakpoints.compact -> RivuneSpacing.xxxl
    else -> RivuneSpacing.lg
}

private fun String.initial(): String = trim().firstOrNull()?.uppercase() ?: ""
