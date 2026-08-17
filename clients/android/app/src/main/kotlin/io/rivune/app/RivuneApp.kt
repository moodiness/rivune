package io.rivune.app

import android.content.ActivityNotFoundException
import android.app.Activity
import android.content.ClipData
import android.content.ClipboardManager
import android.content.ClipDescription
import android.content.Intent
import android.os.Build
import android.net.Uri
import android.os.PersistableBundle
import android.widget.Toast
import android.view.HapticFeedbackConstants
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.togetherWith
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
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
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.itemsIndexed
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.ContentCopy
import androidx.compose.material.icons.automirrored.rounded.Logout
import androidx.compose.material.icons.rounded.Check
import androidx.compose.material.icons.rounded.Dns
import androidx.compose.material.icons.rounded.ErrorOutline
import androidx.compose.material.icons.rounded.Lock
import androidx.compose.material.icons.rounded.Refresh
import androidx.compose.material.icons.rounded.SystemUpdate
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
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.withFrameNanos
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusProperties
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.liveRegion
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
import io.rivune.app.ui.components.RivuneCinematicBackground
import io.rivune.app.ui.components.RivuneFunctionalSurface
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
import io.rivune.app.ui.theme.LocalRivuneMotionPolicy
import io.rivune.app.ui.theme.RivuneBreakpoints
import io.rivune.app.ui.theme.RivuneDimensions
import io.rivune.app.ui.theme.RivuneElevation
import io.rivune.app.ui.theme.RivuneMotion
import io.rivune.app.ui.theme.finiteAnimationSpec
import io.rivune.app.ui.theme.RivuneShapes
import io.rivune.app.ui.theme.RivuneSuccess
import io.rivune.app.ui.theme.RivuneSpacing
import io.rivune.app.ui.theme.RivuneTheme
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

@Composable
internal fun RivuneRoot(
    viewModel: RivuneViewModel,
    updates: AppUpdateCoordinator,
    activity: Activity,
    systemAnimationsEnabled: Boolean,
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val updateState by updates.state.collectAsStateWithLifecycle()
    val application = remember(activity) { activity.application as RivuneApplication }
    val appPreferences by application.appPreferences.state.collectAsStateWithLifecycle()
    val appLanguage = remember(activity) { currentAppLanguage(activity) }
    val activityCoroutineScope = rememberCoroutineScope()
    val diagnosticsCopied = stringResource(R.string.diagnostics_copied)
    val diagnosticsCopyFailed = stringResource(R.string.diagnostics_copy_failed)
    val diagnosticsExported = stringResource(R.string.diagnostics_exported)
    val diagnosticsExportFailed = stringResource(R.string.diagnostics_export_failed)
    val externalActionFailed = stringResource(R.string.external_action_failed)
    var diagnosticExportRequested by rememberSaveable { mutableStateOf(false) }
    val exportLauncher = rememberLauncherForActivityResult(diagnosticReportDocumentContract()) { uri ->
        val shouldExport = diagnosticExportRequested
        diagnosticExportRequested = false
        if (uri != null && shouldExport) {
            val input = diagnosticReportInput(
                activity = activity,
                state = state,
                preferences = appPreferences,
                events = application.diagnostics.snapshot(),
            )
            activityCoroutineScope.launch {
                val exported = exportDiagnosticReport(activity.contentResolver, uri, input)
                application.diagnostics.record(
                    if (exported) DiagnosticEventCode.DIAGNOSTIC_EXPORT_SUCCEEDED
                    else DiagnosticEventCode.DIAGNOSTIC_EXPORT_FAILED,
                )
                Toast.makeText(
                    activity,
                    if (exported) diagnosticsExported else diagnosticsExportFailed,
                    Toast.LENGTH_SHORT,
                ).show()
            }
        }
    }
    val inlineFailure = state.destination == AppDestination.Server ||
        state.destination == AppDestination.Pairing ||
        state.destination == AppDestination.Profiles ||
        state.pendingProfile != null

    fun currentDiagnosticReport(): DiagnosticReportInput = diagnosticReportInput(
        activity = activity,
        state = state,
        preferences = appPreferences,
        events = application.diagnostics.snapshot(),
    )

    RivuneTheme(
        accentColor = appPreferences.accentColor,
        animationPreference = appPreferences.animationPreference,
        systemAnimationsEnabled = systemAnimationsEnabled,
    ) {
        val motionPolicy = LocalRivuneMotionPolicy.current
        Surface(
            modifier = Modifier.fillMaxSize(),
            color = MaterialTheme.colorScheme.background,
        ) {
            Box {
                AnimatedContent(
                    targetState = state.destination,
                    transitionSpec = {
                        fadeIn(motionPolicy.finiteAnimationSpec(RivuneMotion.normal)) togetherWith
                            fadeOut(motionPolicy.finiteAnimationSpec(RivuneMotion.fast))
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
                            failure = state.failure,
                            onClearFailure = viewModel::clearFailure,
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
                            appLanguage = appLanguage,
                            onAppLanguage = { language ->
                                if (saveAppLanguage(activity, language)) activity.recreate()
                            },
                            appPreferences = appPreferences,
                            onStartupTab = application.appPreferences::setStartupTab,
                            onPreferredPlayer = application.appPreferences::setPreferredPlayer,
                            onPreferredEmbeddedPlayer = application.appPreferences::setPreferredEmbeddedPlayer,
                            onAnimationPreference = application.appPreferences::setAnimationPreference,
                            onAccentColor = application.appPreferences::setAccentColor,
                            onFrameRateMatching = application.appPreferences::setFrameRateMatching,
                            onVideoAspect = application.appPreferences::setVideoAspect,
                            onWifiQuality = application.appPreferences::setWifiQuality,
                            onMobileQuality = application.appPreferences::setMobileQuality,
                            onAutomaticallyShowStreams = application.appPreferences::setAutomaticallyShowStreams,
                            onAutoSkipIntro = application.appPreferences::setAutoSkipIntro,
                            onAutoSkipRecap = application.appPreferences::setAutoSkipRecap,
                            onAutoSkipOutro = application.appPreferences::setAutoSkipOutro,
                            onChangeServer = viewModel::disconnectServer,
                            onOpenExternalUrl = { url ->
                                try {
                                    activity.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url)))
                                } catch (_: ActivityNotFoundException) {
                                    Toast.makeText(activity, externalActionFailed, Toast.LENGTH_SHORT).show()
                                } catch (_: SecurityException) {
                                    Toast.makeText(activity, externalActionFailed, Toast.LENGTH_SHORT).show()
                                }
                            },
                            onCopyDiagnostics = {
                                val report = buildDiagnosticReport(currentDiagnosticReport())
                                try {
                                    val clipboard = activity.getSystemService(ClipboardManager::class.java)
                                    val clip = ClipData.newPlainText("Rivune diagnostics", report)
                                    clip.description.extras = PersistableBundle().apply {
                                        val sensitiveKey = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                                            ClipDescription.EXTRA_IS_SENSITIVE
                                        } else {
                                            CLIPBOARD_SENSITIVE_COMPAT_KEY
                                        }
                                        putBoolean(sensitiveKey, true)
                                    }
                                    clipboard.setPrimaryClip(clip)
                                    Toast.makeText(activity, diagnosticsCopied, Toast.LENGTH_SHORT).show()
                                    application.applicationScope.launch {
                                        delay(DIAGNOSTIC_CLIPBOARD_LIFETIME_MS)
                                        runCatching {
                                            val current = clipboard.primaryClip
                                            if (current?.itemCount == 1 && current.getItemAt(0).text?.toString() == report) {
                                                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
                                                    clipboard.clearPrimaryClip()
                                                } else {
                                                    clipboard.setPrimaryClip(ClipData.newPlainText("", ""))
                                                }
                                            }
                                        }
                                    }
                                } catch (_: SecurityException) {
                                    Toast.makeText(activity, diagnosticsCopyFailed, Toast.LENGTH_SHORT).show()
                                }
                            },
                            onExportLogs = {
                                diagnosticExportRequested = true
                                try {
                                    exportLauncher.launch(DIAGNOSTIC_REPORT_FILE_NAME)
                                } catch (_: ActivityNotFoundException) {
                                    diagnosticExportRequested = false
                                    application.diagnostics.record(DiagnosticEventCode.DIAGNOSTIC_EXPORT_FAILED)
                                    Toast.makeText(activity, diagnosticsExportFailed, Toast.LENGTH_SHORT).show()
                                } catch (_: SecurityException) {
                                    diagnosticExportRequested = false
                                    application.diagnostics.record(DiagnosticEventCode.DIAGNOSTIC_EXPORT_FAILED)
                                    Toast.makeText(activity, diagnosticsExportFailed, Toast.LENGTH_SHORT).show()
                                }
                            },
                        )
                    }
                }

                AnimatedVisibility(
                    visible = state.failure != null && !inlineFailure,
                    enter = fadeIn(motionPolicy.finiteAnimationSpec(RivuneMotion.fast)),
                    exit = fadeOut(motionPolicy.finiteAnimationSpec(RivuneMotion.fast)),
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
                            .padding(if (state.isTv) RivuneSpacing.xxl else RivuneSpacing.lg),
                    )
                }
            }

            state.pendingProfile?.let { profile ->
                PinDialog(
                    profile = profile,
                    isBusy = state.isBusy,
                    failure = state.failure,
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

private const val DIAGNOSTIC_CLIPBOARD_LIFETIME_MS = 60_000L
private const val CLIPBOARD_SENSITIVE_COMPAT_KEY = "android.content.extra.IS_SENSITIVE"

private fun diagnosticReportInput(
    activity: Activity,
    state: RivuneUiState,
    preferences: AppPreferencesState,
    events: List<DiagnosticEvent>,
): DiagnosticReportInput {
    val metadata = collectAndroidDiagnosticMetadata(activity)
    return DiagnosticReportInput(
        generatedAtEpochMillis = System.currentTimeMillis(),
        appVersionName = metadata.appVersionName,
        appVersionCode = metadata.appVersionCode,
        buildType = metadata.buildType,
        serverUrl = state.serverInput,
        serverDisplayName = state.serverName,
        serverVersion = state.serverVersion,
        serverProtocolVersion = state.protocolVersion,
        sdkInt = metadata.sdkInt,
        deviceModel = metadata.deviceModel,
        isTelevision = metadata.isTelevision,
        startupTab = when (preferences.startupTab) {
            ViewerTab.HOME -> DiagnosticStartupTab.HOME
            ViewerTab.SEARCH -> DiagnosticStartupTab.SEARCH
            ViewerTab.LIBRARY -> DiagnosticStartupTab.LIBRARY
            ViewerTab.CALENDAR -> DiagnosticStartupTab.CALENDAR
        },
        preferredPlayer = when (preferences.preferredPlayer) {
            PreferredPlayer.Ask -> DiagnosticPreferredPlayer.ASK
            PreferredPlayer.Rivune -> DiagnosticPreferredPlayer.RIVUNE
            is PreferredPlayer.External -> DiagnosticPreferredPlayer.EXTERNAL
        },
        animationPreference = when (preferences.animationPreference) {
            AnimationPreference.SYSTEM -> DiagnosticAnimationPreference.SYSTEM
            AnimationPreference.FULL -> DiagnosticAnimationPreference.FULL
            AnimationPreference.REDUCED -> DiagnosticAnimationPreference.REDUCED
        },
        accentColor = preferences.accentColor,
        frameRateMatching = preferences.frameRateMatching.preferenceValue,
        videoAspect = preferences.videoAspect.preferenceValue,
        wifiQuality = preferences.wifiQuality.preferenceValue,
        mobileQuality = preferences.mobileQuality.preferenceValue,
        events = events,
    )
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
    val motionPolicy = LocalRivuneMotionPolicy.current
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
        icon = { Icon(Icons.Rounded.SystemUpdate, contentDescription = null, tint = MaterialTheme.colorScheme.primary) },
        title = { Text(title) },
        text = {
            Row(horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.md), verticalAlignment = Alignment.CenterVertically) {
                if (
                    motionPolicy.ambientAnimations &&
                    (state is AppUpdateState.Downloading || state is AppUpdateState.Installing)
                ) {
                    CircularProgressIndicator(
                        modifier = Modifier.size(RivuneSpacing.xl),
                        strokeWidth = RivuneDimensions.focusRing,
                    )
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
        containerColor = MaterialTheme.colorScheme.surfaceContainerHigh,
        shape = RivuneShapes.extraLarge,
    )
}

@Composable
private fun LoadingScreen() {
    RivuneCinematicBackground {
        Column(
            modifier = Modifier
                .align(Alignment.Center)
                .windowInsetsPadding(WindowInsets.safeDrawing)
                .padding(RivuneSpacing.xl),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            RivuneBrandMark(size = RivuneDimensions.touchTarget)
            Spacer(Modifier.height(RivuneSpacing.md))
            Text(
                text = stringResource(R.string.loading),
                style = MaterialTheme.typography.titleLarge,
            )
            Spacer(Modifier.height(RivuneSpacing.xs))
            Row(
                modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
                horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.sm),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Box(
                    Modifier
                        .size(RivuneSpacing.xs)
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
    val keyboardController = LocalSoftwareKeyboardController.current
    var server by remember(serverInput) { mutableStateOf(serverInput) }
    var editingServerOnTv by remember { mutableStateOf(false) }
    val view = LocalView.current
    val failureText = failure?.let { failureMessage(it) }
    val submit = { if (server.isNotBlank() && !isBusy) onConnect(server.trim()) }
    val inputFocus = remember { FocusRequester() }
    val submitFocus = remember { FocusRequester() }
    val updateFocus = remember { FocusRequester() }
    LaunchedEffect(isTv) {
        if (isTv) {
            keyboardController?.hide()
            withFrameNanos { }
            if (server.isBlank()) inputFocus.requestFocus() else submitFocus.requestFocus()
        }
    }
    LaunchedEffect(editingServerOnTv) {
        if (isTv && editingServerOnTv) {
            inputFocus.requestFocus()
            withFrameNanos { }
            keyboardController?.show()
        }
    }
    LaunchedEffect(failure) {
        if (failure != null) performRejectHaptic(view)
    }

    AuthFrame(
        isTv = isTv,
        heading = {
            RivuneScreenHeading(
                eyebrow = stringResource(R.string.server_eyebrow),
                title = stringResource(R.string.server_title),
                body = stringResource(R.string.server_body),
                isTv = isTv,
            )
        },
    ) {
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
                    if (
                        isTv &&
                        !editingServerOnTv &&
                        event.type == KeyEventType.KeyDown &&
                        (
                            event.nativeKeyEvent.keyCode == android.view.KeyEvent.KEYCODE_DPAD_CENTER ||
                                event.nativeKeyEvent.keyCode == android.view.KeyEvent.KEYCODE_ENTER ||
                                event.nativeKeyEvent.keyCode == android.view.KeyEvent.KEYCODE_NUMPAD_ENTER
                            )
                    ) {
                        editingServerOnTv = true
                        true
                    } else if (isTv && event.type == KeyEventType.KeyDown && event.key == Key.DirectionDown) {
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
            readOnly = isTv && !editingServerOnTv,
            leadingIcon = Icons.Rounded.Dns,
            keyboardOptions = KeyboardOptions(
                keyboardType = KeyboardType.Uri,
                imeAction = ImeAction.Go,
            ),
            keyboardActions = KeyboardActions(onGo = {
                editingServerOnTv = false
                keyboardController?.hide()
                submit()
            }),
        )
        Spacer(Modifier.height(RivuneSpacing.sm))
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
        Spacer(Modifier.height(RivuneSpacing.xxs))
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
    val pairingActionFocus = remember { FocusRequester() }
    val cancelDisconnectFocus = remember { FocusRequester() }
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
    LaunchedEffect(isTv, visualState, isBusy) {
        if (isTv && !isBusy && (visualState == PairingVisualState.CODE || visualState == PairingVisualState.ERROR)) {
            delay(RivuneMotion.normal.toLong())
            pairingActionFocus.requestFocus()
        }
    }
    LaunchedEffect(isTv, confirmDisconnect) {
        if (isTv && confirmDisconnect) cancelDisconnectFocus.requestFocus()
    }
    val copyCode: (() -> Unit)? = if (isTv || pairing == null) {
        null
    } else {
        {
            clipboard.setPrimaryClip(ClipData.newPlainText("Rivune pairing code", pairing.userCode))
            copied = true
            performClickHaptic(view)
        }
    }

    RivuneCinematicBackground {
        BoxWithConstraints(
            modifier = Modifier
                .fillMaxSize()
                .windowInsetsPadding(WindowInsets.safeDrawing),
            contentAlignment = Alignment.TopCenter,
        ) {
            val tablet = maxWidth >= RivuneBreakpoints.medium
            val fontScale = LocalDensity.current.fontScale
            val tvLandscape = isTv &&
                maxWidth >= RivuneBreakpoints.expanded &&
                maxHeight >= 600.dp &&
                fontScale < 1.5f

            if (tvLandscape) {
                Box(
                    modifier = Modifier
                        .fillMaxSize()
                        .padding(horizontal = RivuneSpacing.display, vertical = RivuneSpacing.xxl),
                ) {
                    RivuneBrandLockup(
                        name = stringResource(R.string.app_name),
                        modifier = Modifier.align(Alignment.TopCenter),
                        markSize = RivuneSpacing.xxxl,
                    )

                    Column(
                        modifier = Modifier
                            .align(Alignment.Center)
                            .widthIn(max = 620.dp)
                            .fillMaxWidth(),
                        horizontalAlignment = Alignment.CenterHorizontally,
                    ) {
                        RivuneScreenHeading(
                            eyebrow = stringResource(R.string.pairing_eyebrow),
                            title = stringResource(R.string.pairing_title),
                            body = null,
                            isTv = true,
                            compactTitle = true,
                            modifier = Modifier.widthIn(max = 600.dp),
                            textAlign = TextAlign.Center,
                        )
                        Spacer(Modifier.height(RivuneSpacing.lg))
                        Box(
                            modifier = Modifier
                                .widthIn(max = 440.dp)
                                .fillMaxWidth(),
                        ) {
                            PairingStateContent(
                                visualState = visualState,
                                pairing = pairing,
                                expired = expired,
                                failure = failure,
                                isTv = true,
                                compactTv = true,
                                copied = copied,
                                onCopy = null,
                            )
                        }
                        Spacer(Modifier.height(RivuneSpacing.md))
                        Box(
                            modifier = Modifier
                                .widthIn(max = 440.dp)
                                .fillMaxWidth(),
                        ) {
                            PairingActions(
                                pairing = pairing,
                                pairingAccepted = pairingAccepted,
                                isBusy = isBusy,
                                failure = failure,
                                isTv = true,
                                horizontal = true,
                                onRestart = onRestart,
                                onDisconnectRequest = { confirmDisconnect = true },
                                focusRequester = pairingActionFocus,
                            )
                        }
                    }
                }
            } else {
                val horizontalPadding = responsiveHorizontalPadding(maxWidth, isTv)
                val contentMaxWidth = when {
                    isTv -> RivuneDimensions.preferencesMax
                    tablet -> RivuneDimensions.contentMaxTablet
                    else -> RivuneDimensions.contentMax
                }
                val compactHeight = maxHeight < 720.dp
                val sectionGap = when {
                    compactHeight -> RivuneSpacing.md
                    tablet -> RivuneSpacing.xxl
                    else -> RivuneSpacing.lg
                }

                Column(
                    modifier = Modifier
                        .widthIn(max = contentMaxWidth)
                        .fillMaxWidth()
                        .heightIn(min = maxHeight)
                        .verticalScroll(rememberScrollState())
                        .padding(
                            horizontal = horizontalPadding,
                            vertical = if (compactHeight) RivuneSpacing.sm else RivuneSpacing.lg,
                        ),
                    verticalArrangement = if (compactHeight) Arrangement.Top else Arrangement.Center,
                ) {
                    RivuneBrandLockup(
                        name = stringResource(R.string.app_name),
                        tagline = if (tablet || isTv) stringResource(R.string.brand_tagline) else null,
                        markSize = if (isTv) RivuneSpacing.display else RivuneSpacing.xxxl,
                    )
                    Spacer(Modifier.height(if (compactHeight) RivuneSpacing.md else RivuneSpacing.lg))
                    RivuneScreenHeading(
                        eyebrow = stringResource(R.string.pairing_eyebrow),
                        title = stringResource(R.string.pairing_title),
                        body = stringResource(R.string.pairing_body),
                        isTv = isTv,
                    )
                    Spacer(Modifier.height(sectionGap))
                    PairingStateContent(
                        visualState = visualState,
                        pairing = pairing,
                        expired = expired,
                        failure = failure,
                        isTv = isTv,
                        compactTv = false,
                        copied = copied,
                        onCopy = copyCode,
                    )
                    Spacer(Modifier.height(sectionGap))
                    PairingActions(
                        pairing = pairing,
                        pairingAccepted = pairingAccepted,
                        isBusy = isBusy,
                        failure = failure,
                        isTv = isTv,
                        onRestart = onRestart,
                        onDisconnectRequest = { confirmDisconnect = true },
                        focusRequester = pairingActionFocus,
                    )
                }
            }
        }
    }

    if (confirmDisconnect) {
        DisconnectConfirmationDialog(
            isTv = isTv,
            cancelFocus = cancelDisconnectFocus,
            onDismiss = { confirmDisconnect = false },
            onConfirm = {
                confirmDisconnect = false
                onDisconnect()
            },
        )
    }
}

@Composable
private fun DisconnectConfirmationDialog(
    isTv: Boolean,
    cancelFocus: FocusRequester,
    onDismiss: () -> Unit,
    onConfirm: () -> Unit,
) {
    val title = stringResource(R.string.pairing_disconnect_confirm_title)
    val body = stringResource(R.string.pairing_disconnect_confirm_body)
    val disconnectLabel = stringResource(R.string.pairing_disconnect_confirm)
    val cancelLabel = stringResource(R.string.pin_cancel)

    if (!isTv) {
        AlertDialog(
            onDismissRequest = onDismiss,
            icon = {
                Icon(
                    imageVector = Icons.AutoMirrored.Rounded.Logout,
                    contentDescription = null,
                    tint = MaterialTheme.colorScheme.error,
                )
            },
            title = { Text(title) },
            text = { Text(body) },
            confirmButton = {
                RivuneTextButton(
                    modifier = Modifier.testTag(RivuneTestTags.PairingDisconnectConfirm),
                    label = disconnectLabel,
                    onClick = onConfirm,
                    destructive = true,
                )
            },
            dismissButton = {
                RivuneTextButton(
                    label = cancelLabel,
                    onClick = onDismiss,
                )
            },
            containerColor = MaterialTheme.colorScheme.surfaceContainerHigh,
            shape = RivuneShapes.large,
        )
        return
    }

    Dialog(
        onDismissRequest = onDismiss,
        properties = DialogProperties(usePlatformDefaultWidth = false),
    ) {
        Box(
            modifier = Modifier
                .fillMaxSize()
                .padding(horizontal = RivuneSpacing.display, vertical = RivuneSpacing.xl),
            contentAlignment = Alignment.Center,
        ) {
            RivuneFunctionalSurface(
                modifier = Modifier
                    .widthIn(max = RivuneDimensions.contentMax)
                    .fillMaxWidth(),
                shape = RivuneShapes.extraLarge,
                contentPadding = PaddingValues(RivuneSpacing.xxl),
                color = MaterialTheme.colorScheme.surface,
            ) {
                Column(modifier = Modifier.fillMaxWidth()) {
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.lg),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Surface(
                            modifier = Modifier.size(RivuneDimensions.buttonHeight),
                            shape = CircleShape,
                            color = MaterialTheme.colorScheme.errorContainer,
                            contentColor = MaterialTheme.colorScheme.error,
                            tonalElevation = RivuneElevation.flat,
                        ) {
                            Box(contentAlignment = Alignment.Center) {
                                Icon(
                                    imageVector = Icons.AutoMirrored.Rounded.Logout,
                                    contentDescription = null,
                                    modifier = Modifier.size(RivuneSpacing.xl),
                                )
                            }
                        }
                        Column(
                            modifier = Modifier.weight(1f),
                            verticalArrangement = Arrangement.Center,
                        ) {
                            Text(
                                text = title,
                                style = MaterialTheme.typography.headlineMedium,
                            )
                            Spacer(Modifier.height(RivuneSpacing.xs))
                            Text(
                                text = body,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                style = MaterialTheme.typography.bodyLarge,
                            )
                        }
                    }
                    Spacer(Modifier.height(RivuneSpacing.xxl))
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.xl),
                    ) {
                        RivuneSecondaryButton(
                            label = cancelLabel,
                            onClick = onDismiss,
                            modifier = Modifier
                                .weight(1f)
                                .focusRequester(cancelFocus),
                            isTv = true,
                            transparent = true,
                            neutralContent = true,
                        )
                        RivuneSecondaryButton(
                            label = disconnectLabel,
                            onClick = onConfirm,
                            modifier = Modifier
                                .weight(1f)
                                .testTag(RivuneTestTags.PairingDisconnectConfirm),
                            isTv = true,
                            icon = Icons.AutoMirrored.Rounded.Logout,
                            destructive = true,
                            transparent = true,
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun PairingStateContent(
    visualState: PairingVisualState,
    pairing: PairingInfo?,
    expired: Boolean,
    failure: UiFailure?,
    isTv: Boolean,
    compactTv: Boolean,
    copied: Boolean,
    onCopy: (() -> Unit)?,
) {
    val motionPolicy = LocalRivuneMotionPolicy.current
    AnimatedContent(
        targetState = visualState to pairing,
        transitionSpec = {
            fadeIn(motionPolicy.finiteAnimationSpec(RivuneMotion.normal)) togetherWith
                fadeOut(motionPolicy.finiteAnimationSpec(RivuneMotion.fast))
        },
        label = "pairing-state",
    ) { contentState ->
        val state = contentState.first
        val statePairing = contentState.second
        when (state) {
            PairingVisualState.SUCCESS -> PairingSuccessState(isTv)
            PairingVisualState.LOADING -> PairingLoadingState(isTv = isTv, compactTv = compactTv)
            PairingVisualState.ERROR -> PairingIssueState(
                title = stringResource(if (expired) R.string.pairing_expired_title else R.string.error_title),
                body = if (expired) {
                    stringResource(R.string.pairing_expired_body)
                } else {
                    failure?.let { failureMessage(it) }.orEmpty()
                },
                isTv = isTv,
            )
            PairingVisualState.CODE -> if (statePairing == null) {
                PairingLoadingState(isTv = isTv, compactTv = compactTv)
            } else {
                Column(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalAlignment = Alignment.CenterHorizontally,
                ) {
                    PairingValue(
                        label = stringResource(R.string.pairing_code_label),
                        value = statePairing.userCode,
                        isTv = isTv,
                        compactTv = compactTv,
                        copied = copied,
                        onCopy = onCopy,
                    )
                    Spacer(Modifier.height(if (compactTv) RivuneSpacing.md else RivuneSpacing.lg))
                    if (failure == null) {
                        PairingWaitingState(copied = copied, isTv = isTv)
                    } else {
                        PairingInlineIssue(message = failureMessage(failure), isTv = isTv)
                    }
                }
            }
        }
    }
}

@Composable
private fun PairingActions(
    pairing: PairingInfo?,
    pairingAccepted: Boolean,
    isBusy: Boolean,
    failure: UiFailure?,
    isTv: Boolean,
    horizontal: Boolean = false,
    onRestart: () -> Unit,
    onDisconnectRequest: () -> Unit,
    focusRequester: FocusRequester,
) {
    val motionPolicy = LocalRivuneMotionPolicy.current
    val restartLabel = stringResource(if (isBusy) R.string.pairing_restart_loading else R.string.pairing_restart)
    val restartProminent = !isTv && pairing == null && failure != null

    @Composable
    fun RestartButton(modifier: Modifier) {
        if (restartProminent) {
            RivunePrimaryButton(
                label = restartLabel,
                onClick = onRestart,
                modifier = modifier.focusRequester(focusRequester).testTag(RivuneTestTags.PairingRestart),
                enabled = !isBusy,
                loading = isBusy,
                isTv = isTv,
                icon = Icons.Rounded.Refresh,
            )
        } else {
            RivuneSecondaryButton(
                label = restartLabel,
                onClick = onRestart,
                modifier = modifier.focusRequester(focusRequester).testTag(RivuneTestTags.PairingRestart),
                enabled = !isBusy,
                loading = isBusy,
                isTv = isTv,
                icon = Icons.Rounded.Refresh,
                transparent = isTv,
                neutralContent = horizontal,
            )
        }
    }

    @Composable
    fun DisconnectButton(modifier: Modifier) {
        val actionModifier = modifier
            .then(if (pairing == null && failure == null) Modifier.focusRequester(focusRequester) else Modifier)
            .testTag(RivuneTestTags.PairingDisconnect)
        if (isTv) {
            RivuneSecondaryButton(
                label = stringResource(R.string.pairing_disconnect_short),
                onClick = onDisconnectRequest,
                modifier = actionModifier,
                enabled = !isBusy,
                isTv = true,
                destructive = true,
                transparent = true,
                icon = Icons.AutoMirrored.Rounded.Logout,
            )
        } else {
            RivuneTextButton(
                label = stringResource(R.string.pairing_disconnect),
                onClick = onDisconnectRequest,
                modifier = actionModifier,
                enabled = !isBusy,
                destructive = true,
                icon = Icons.AutoMirrored.Rounded.Logout,
            )
        }
    }

    AnimatedVisibility(
        visible = !pairingAccepted,
        enter = fadeIn(motionPolicy.finiteAnimationSpec(RivuneMotion.fast)),
        exit = fadeOut(motionPolicy.finiteAnimationSpec(RivuneMotion.fast)),
    ) {
        if (horizontal) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.xl),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                RestartButton(Modifier.weight(1f))
                DisconnectButton(Modifier.weight(1f))
            }
        } else {
            Column(modifier = Modifier.fillMaxWidth()) {
                RestartButton(Modifier.fillMaxWidth())
                Spacer(Modifier.height(RivuneSpacing.xs))
                DisconnectButton(Modifier.fillMaxWidth())
            }
        }
    }
}

@Composable
internal fun ProfilesScreen(
    profiles: List<Profile>,
    isBusy: Boolean,
    isTv: Boolean,
    failure: UiFailure?,
    resourceUrl: (String?) -> String?,
    avatarData: Map<java.util.UUID, ByteArray>,
    onSelect: (Profile) -> Unit,
    onLogout: () -> Unit,
    onRefresh: () -> Unit,
    onClearFailure: () -> Unit,
) {
    val refreshFocus = remember { FocusRequester() }
    val firstProfileFocus = remember { FocusRequester() }
    val firstFocusableIndex = profiles.indexOfFirst { it.enabled && it.accessible }
    LaunchedEffect(isTv, profiles, isBusy) {
        if (isTv && !isBusy) {
            withFrameNanos { }
            if (firstFocusableIndex >= 0) firstProfileFocus.requestFocus() else refreshFocus.requestFocus()
        }
    }
    RivuneCinematicBackground {
        BoxWithConstraints(
            modifier = Modifier
                .fillMaxSize()
                .statusBarsPadding()
                .navigationBarsPadding(),
        ) {
            val horizontalPadding = responsiveHorizontalPadding(maxWidth, isTv)
            val profileSpacing = if (isTv) RivuneSpacing.md else RivuneSpacing.sm
            val availableGridWidth = (maxWidth - horizontalPadding * 2).coerceAtLeast(0.dp)
            val nonTvGridWidth = availableGridWidth.coerceAtMost(RivuneDimensions.preferencesMax)
            val profileColumns = if (isTv) {
                (((availableGridWidth + profileSpacing) / (120.dp + profileSpacing)).toInt()).coerceIn(4, 8)
            } else {
                (((nonTvGridWidth + profileSpacing) / (160.dp + profileSpacing)).toInt()).coerceIn(2, 5)
            }
            val gridWidth = if (isTv) {
                RivuneDimensions.profileCardWidthTv * profileColumns + profileSpacing * (profileColumns - 1)
            } else {
                nonTvGridWidth
            }
            val profileSlots = remember(profiles, profileColumns, isTv) {
                if (isTv) centeredProfileSlots(profiles, profileColumns) else emptyList()
            }
            Column(modifier = Modifier.fillMaxSize()) {
                TopBar(
                    isTv = isTv,
                    horizontalPadding = horizontalPadding,
                    compactTvHeight = isTv && this@BoxWithConstraints.maxHeight <= 540.dp,
                    actions = {
                        ToolbarAction(
                            label = stringResource(R.string.home_refresh),
                            icon = Icons.Rounded.Refresh,
                            onClick = onRefresh,
                            enabled = !isBusy,
                            isTv = isTv,
                            modifier = Modifier.focusRequester(refreshFocus),
                            neutralContent = true,
                        )
                        ToolbarAction(
                            label = stringResource(R.string.logout),
                            icon = Icons.AutoMirrored.Rounded.Logout,
                            onClick = onLogout,
                            enabled = !isBusy,
                            isTv = isTv,
                            destructive = true,
                        )
                    },
                )
                if (failure != null) {
                    FailureBanner(
                        message = failureMessage(failure),
                        onDismiss = onClearFailure,
                        isTv = isTv,
                        modifier = Modifier
                            .align(Alignment.CenterHorizontally)
                            .padding(horizontal = horizontalPadding),
                    )
                }
                LazyVerticalGrid(
                    columns = GridCells.Fixed(profileColumns),
                    modifier = Modifier
                        .width(gridWidth)
                        .weight(1f)
                        .align(Alignment.CenterHorizontally),
                    contentPadding = PaddingValues(
                        top = if (isTv) RivuneSpacing.xxl else RivuneSpacing.sm,
                        bottom = RivuneSpacing.lg,
                    ),
                    horizontalArrangement = Arrangement.spacedBy(profileSpacing),
                    verticalArrangement = Arrangement.spacedBy(if (isTv) RivuneSpacing.md else RivuneSpacing.lg),
                ) {
                    item(span = { GridItemSpan(maxLineSpan) }) {
                        RivuneScreenHeading(
                            eyebrow = stringResource(R.string.profiles_eyebrow),
                            title = stringResource(R.string.profiles_title),
                            body = stringResource(R.string.profiles_body),
                            isTv = isTv,
                            modifier = Modifier.fillMaxWidth(),
                            textAlign = if (isTv) TextAlign.Center else TextAlign.Start,
                        )
                        Spacer(Modifier.height(if (isTv) RivuneSpacing.xl else RivuneSpacing.lg))
                    }
                    if (profiles.isEmpty()) {
                        item(span = { GridItemSpan(maxLineSpan) }) {
                            EmptyState(
                                title = stringResource(R.string.profiles_empty_title),
                                body = stringResource(R.string.profiles_empty_body),
                                modifier = Modifier.fillMaxWidth(),
                            )
                        }
                    } else if (isTv) {
                        itemsIndexed(
                            profileSlots,
                            key = { slotIndex, slot -> slot?.value?.id ?: "empty-profile-slot-$slotIndex" },
                        ) { _, slot ->
                            if (slot != null) {
                                val profile = slot.value
                                ProfileCard(
                                    profile = profile,
                                    imageModel = avatarData[profile.id]
                                        ?: profile.avatar.url.takeIf { profile.avatar.kind == "preset" }?.let(resourceUrl),
                                    enabled = profile.enabled && profile.accessible && !isBusy,
                                    modifier = if (slot.index == firstFocusableIndex) {
                                        Modifier.focusRequester(firstProfileFocus)
                                    } else {
                                        Modifier
                                    },
                                    isTv = true,
                                    onClick = { onSelect(profile) },
                                )
                            }
                        }
                    } else {
                        itemsIndexed(profiles, key = { _, profile -> profile.id }) { index, profile ->
                            ProfileCard(
                                profile = profile,
                                imageModel = avatarData[profile.id]
                                    ?: profile.avatar.url.takeIf { profile.avatar.kind == "preset" }?.let(resourceUrl),
                                enabled = profile.enabled && profile.accessible && !isBusy,
                                modifier = if (index == firstFocusableIndex) {
                                    Modifier.focusRequester(firstProfileFocus)
                                } else {
                                    Modifier
                                },
                                isTv = false,
                                onClick = { onSelect(profile) },
                            )
                        }
                    }
                }
            }
        }
    }
}

private fun centeredProfileSlots(profiles: List<Profile>, columns: Int): List<IndexedValue<Profile>?> {
    require(columns > 0)
    return profiles.withIndex().chunked(columns).flatMap { row ->
        val emptySlots = columns - row.size
        val leadingSlots = emptySlots / 2
        buildList<IndexedValue<Profile>?>(columns) {
            repeat(leadingSlots) { add(null) }
            addAll(row)
            repeat(emptySlots - leadingSlots) { add(null) }
        }
    }
}




@Composable
private fun AuthFrame(
    isTv: Boolean,
    contentMaxWidth: Dp = RivuneDimensions.contentMax,
    heading: @Composable ColumnScope.() -> Unit,
    content: @Composable ColumnScope.() -> Unit,
) {
    val fontScale = LocalDensity.current.fontScale
    RivuneCinematicBackground {
        BoxWithConstraints(
            modifier = Modifier
                .fillMaxSize()
                .windowInsetsPadding(WindowInsets.safeDrawing)
                .imePadding(),
        ) {
            val tvLandscape = isTv &&
                maxWidth >= RivuneBreakpoints.expanded &&
                maxHeight >= 600.dp &&
                fontScale < 1.5f

            if (tvLandscape) {
                Row(
                    modifier = Modifier
                        .fillMaxSize()
                        .padding(horizontal = RivuneSpacing.display, vertical = RivuneSpacing.xxl),
                    horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.xxxl),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Column(
                        modifier = Modifier
                            .width(360.dp)
                            .fillMaxHeight(),
                        verticalArrangement = Arrangement.Center,
                    ) {
                        RivuneBrandLockup(
                            name = stringResource(R.string.app_name),
                            tagline = stringResource(R.string.brand_tagline),
                            markSize = RivuneDimensions.touchTarget,
                        )
                        Spacer(Modifier.height(RivuneSpacing.xxl))
                        heading()
                    }

                    Column(
                        modifier = Modifier
                            .weight(1f)
                            .widthIn(max = RivuneDimensions.contentMaxTablet),
                        verticalArrangement = Arrangement.Center,
                        content = content,
                    )
                }
            } else {
                val wide = !isTv &&
                    maxWidth >= RivuneBreakpoints.expanded &&
                    maxHeight >= 600.dp &&
                    fontScale < 1.5f
                Row(modifier = Modifier.fillMaxSize()) {
                    if (wide) {
                        Box(
                            modifier = Modifier
                                .weight(0.4f)
                                .fillMaxHeight(),
                            contentAlignment = Alignment.Center,
                        ) {
                            RivuneBrandLockup(
                                name = stringResource(R.string.app_name),
                                tagline = stringResource(R.string.brand_tagline),
                                modifier = Modifier.padding(RivuneSpacing.xxxl),
                                markSize = RivuneSpacing.huge,
                            )
                        }
                    }
                    Box(
                        modifier = Modifier
                            .weight(if (wide) 0.6f else 1f)
                            .fillMaxHeight()
                            .verticalScroll(rememberScrollState())
                            .padding(
                                horizontal = responsiveHorizontalPadding(this@BoxWithConstraints.maxWidth, isTv),
                                vertical = if (isTv) RivuneSpacing.huge else RivuneSpacing.lg,
                            ),
                        contentAlignment = if (wide) Alignment.Center else Alignment.TopCenter,
                    ) {
                        if (wide) {
                            RivuneFunctionalSurface(
                                modifier = Modifier
                                    .widthIn(max = contentMaxWidth)
                                    .fillMaxWidth(),
                                contentPadding = PaddingValues(RivuneSpacing.xl),
                            ) {
                                Column(modifier = Modifier.fillMaxWidth()) {
                                    heading()
                                    Spacer(Modifier.height(RivuneSpacing.lg))
                                    content()
                                }
                            }
                        } else {
                            Column(
                                modifier = Modifier
                                    .widthIn(max = contentMaxWidth)
                                    .fillMaxWidth()
                                    .padding(vertical = RivuneSpacing.xxs),
                            ) {
                                RivuneBrandLockup(
                                    name = stringResource(R.string.app_name),
                                    tagline = if (this@BoxWithConstraints.maxWidth >= RivuneBreakpoints.medium) {
                                        stringResource(R.string.brand_tagline)
                                    } else {
                                        null
                                    },
                                    markSize = if (isTv) RivuneSpacing.display else RivuneSpacing.xxxl,
                                )
                                Spacer(Modifier.height(if (isTv) RivuneSpacing.xxxl else RivuneSpacing.xl))
                                heading()
                                Spacer(Modifier.height(if (isTv) RivuneSpacing.xxl else RivuneSpacing.lg))
                                content()
                            }
                        }
                    }
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
    compactTv: Boolean,
    copied: Boolean,
    onCopy: (() -> Unit)?,
) {
    val copyDescription = stringResource(if (copied) R.string.pairing_copied else R.string.pairing_copy)
    val semanticsModifier = Modifier.semantics {
        contentDescription = "$label, $value"
        if (copied) stateDescription = copyDescription
    }
    val interactionModifier = if (onCopy == null) {
        semanticsModifier
    } else {
        semanticsModifier.clickable(
            onClickLabel = copyDescription,
            role = Role.Button,
            onClick = onCopy,
        )
    }
    RivuneFunctionalSurface(
        modifier = Modifier
            .fillMaxWidth()
            .testTag(RivuneTestTags.PairingCode)
            .then(interactionModifier),
        shape = if (compactTv) RivuneShapes.medium else RivuneShapes.large,
        contentPadding = PaddingValues(
            horizontal = when {
                compactTv -> RivuneSpacing.xl
                isTv -> RivuneSpacing.xxl
                else -> RivuneSpacing.lg
            },
            vertical = when {
                compactTv -> RivuneSpacing.lg
                isTv -> RivuneSpacing.xxxl
                else -> RivuneSpacing.xl
            },
        ),
    ) {
        Column(
            modifier = Modifier.fillMaxWidth(),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Text(
                text = label,
                maxLines = 1,
                softWrap = false,
                color = MaterialTheme.colorScheme.primary,
                style = MaterialTheme.typography.labelMedium,
                textAlign = TextAlign.Center,
            )
            Spacer(Modifier.height(if (isTv && !compactTv) RivuneSpacing.md else RivuneSpacing.xs))
            Text(
                text = value.replace("-", "-\u200B"),
                softWrap = true,
                modifier = Modifier.fillMaxWidth(),
                color = MaterialTheme.colorScheme.onSurface,
                fontFamily = FontFamily.Monospace,
                fontWeight = FontWeight.SemiBold,
                fontSize = when {
                    compactTv -> 46.sp
                    isTv -> 72.sp
                    else -> MaterialTheme.typography.displayMedium.fontSize
                },
                lineHeight = when {
                    compactTv -> 54.sp
                    isTv -> 80.sp
                    else -> MaterialTheme.typography.displayMedium.lineHeight
                },
                letterSpacing = (if (compactTv) 2.5f else if (isTv) 5f else 2f).sp,
                textAlign = TextAlign.Center,
            )
            if (onCopy != null) {
                Spacer(Modifier.height(RivuneSpacing.sm))
                Row(
                    horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Icon(
                        imageVector = if (copied) Icons.Rounded.Check else Icons.Rounded.ContentCopy,
                        contentDescription = null,
                        modifier = Modifier.size(RivuneDimensions.iconSmall),
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

private enum class PairingVisualState {
    LOADING,
    CODE,
    SUCCESS,
    ERROR,
}

@Composable
private fun PairingLoadingState(isTv: Boolean, compactTv: Boolean) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .semantics { liveRegion = LiveRegionMode.Polite },
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        RivuneSkeleton(
            modifier = Modifier
                .fillMaxWidth()
                .height(if (compactTv) 160.dp else if (isTv) 210.dp else 148.dp),
            shape = RivuneShapes.large,
        )
        Spacer(Modifier.height(RivuneSpacing.md))
        Text(
            text = stringResource(R.string.pairing_restart_loading),
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            style = if (isTv) MaterialTheme.typography.bodyLarge else MaterialTheme.typography.bodyMedium,
        )
    }
}

@Composable
private fun PairingWaitingState(copied: Boolean, isTv: Boolean) {
    val motionPolicy = LocalRivuneMotionPolicy.current
    AnimatedContent(
        targetState = copied,
        transitionSpec = {
            fadeIn(motionPolicy.finiteAnimationSpec(RivuneMotion.fast)) togetherWith
                fadeOut(motionPolicy.finiteAnimationSpec(RivuneMotion.fast))
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
                        .size(if (isTv) RivuneSpacing.sm else RivuneSpacing.xs)
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
    RivuneFunctionalSurface(
        modifier = Modifier
            .fillMaxWidth()
            .testTag(RivuneTestTags.PairingSuccess)
            .semantics(mergeDescendants = true) { liveRegion = LiveRegionMode.Assertive },
        shape = RivuneShapes.large,
        contentPadding = PaddingValues(if (isTv) RivuneSpacing.huge else RivuneSpacing.xl),
    ) {
        Column(
            modifier = Modifier.fillMaxWidth(),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(RivuneSpacing.sm),
        ) {
            Icon(
                imageVector = Icons.Rounded.Check,
                contentDescription = null,
                modifier = Modifier.size(if (isTv) RivuneSpacing.xxxl else RivuneSpacing.xxl),
                tint = RivuneSuccess,
            )
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
    modifier: Modifier = Modifier,
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
        idleColor = MaterialTheme.colorScheme.background.copy(alpha = 0f),
        modifier = modifier
            .then(if (isTv) Modifier.widthIn(max = RivuneDimensions.profileCardWidthTv) else Modifier.fillMaxWidth())
            .semantics {
                contentDescription = listOfNotNull(profile.name, status).joinToString(", ")
                if (status != null) stateDescription = status
            },
        shape = RivuneShapes.medium,
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(if (isTv) RivuneSpacing.sm else RivuneSpacing.xs),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            RivuneArtwork(
                model = imageModel,
                fallback = profile.name.initial(),
                modifier = if (isTv) {
                    Modifier
                        .size(RivuneDimensions.profileAvatarTv)
                        .clip(RivuneShapes.medium)
                } else {
                    Modifier
                        .fillMaxWidth()
                        .aspectRatio(1f)
                        .clip(RivuneShapes.medium)
                },
                contentDescription = null,
            )
            Spacer(Modifier.height(RivuneSpacing.sm))
            Text(
                text = profile.name,
                modifier = Modifier.fillMaxWidth(),
                maxLines = if (isTv) 1 else 2,
                overflow = TextOverflow.Ellipsis,
                textAlign = TextAlign.Center,
                style = MaterialTheme.typography.titleMedium,
            )
            status?.let {
                Spacer(Modifier.height(RivuneSpacing.xxs))
                Text(
                    text = it,
                    modifier = Modifier.fillMaxWidth(),
                    color = when {
                        !profile.enabled || !profile.accessible -> MaterialTheme.colorScheme.error
                        profile.hasPin -> MaterialTheme.colorScheme.primary
                        else -> MaterialTheme.colorScheme.onSurfaceVariant
                    },
                    maxLines = if (isTv) 2 else 1,
                    overflow = TextOverflow.Ellipsis,
                    textAlign = TextAlign.Center,
                    style = MaterialTheme.typography.labelSmall,
                )
            }
        }
    }
}



@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun TopBar(
    isTv: Boolean,
    horizontalPadding: Dp,
    compactTvHeight: Boolean,
    actions: @Composable () -> Unit,
) {
    Column(modifier = Modifier.fillMaxWidth()) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(
                    horizontal = horizontalPadding,
                    vertical = when {
                        compactTvHeight -> RivuneSpacing.xs
                        isTv -> RivuneSpacing.md
                        else -> RivuneSpacing.xs
                    },
                ),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            RivuneBrandLockup(
                name = stringResource(R.string.app_name),
                modifier = Modifier.weight(1f),
                markSize = if (isTv) RivuneDimensions.touchTargetTv else RivuneSpacing.xxxl,
            )
            FlowRow(
                horizontalArrangement = Arrangement.spacedBy(if (isTv) RivuneSpacing.md else RivuneSpacing.xxs),
                verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xxs),
            ) {
                actions()
            }
        }
        Box(
            Modifier
                .fillMaxWidth()
                .height(RivuneDimensions.hairline)
                .background(MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.7f)),
        )
    }
}

@Composable
private fun ToolbarAction(
    label: String,
    icon: ImageVector,
    onClick: () -> Unit,
    enabled: Boolean,
    isTv: Boolean,
    modifier: Modifier = Modifier,
    neutralContent: Boolean = false,
    destructive: Boolean = false,
) {
    if (isTv) {
        val transparent = MaterialTheme.colorScheme.background.copy(alpha = 0f)
        val contentColor = when {
            !enabled -> MaterialTheme.colorScheme.onSurface.copy(alpha = 0.38f)
            destructive -> MaterialTheme.colorScheme.error
            neutralContent -> MaterialTheme.colorScheme.onSurface
            else -> MaterialTheme.colorScheme.primary
        }
        RivuneFocusSurface(
            onClick = onClick,
            enabled = enabled,
            isTv = true,
            idleColor = transparent,
            focusedColor = transparent,
            pressedColor = transparent,
            showFocusBorder = false,
            focusScale = RivuneMotion.tvButtonFocusScale,
            shape = RivuneShapes.small,
            modifier = modifier,
        ) {
            Row(
                modifier = Modifier.padding(horizontal = RivuneSpacing.sm, vertical = RivuneSpacing.xs),
                horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
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
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    style = MaterialTheme.typography.labelLarge,
                )
            }
        }
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
                Icon(
                    imageVector = icon,
                    contentDescription = null,
                    tint = if (destructive) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurface,
                )
            }
        }
    }
}

@Composable
private fun FailureBanner(
    message: String,
    onDismiss: () -> Unit,
    isTv: Boolean,
    modifier: Modifier = Modifier,
) {
    Surface(
        modifier = modifier
            .widthIn(max = RivuneDimensions.contentMaxTablet)
            .fillMaxWidth()
            .semantics {
                liveRegion = LiveRegionMode.Assertive
                contentDescription = message
            },
        color = MaterialTheme.colorScheme.errorContainer,
        contentColor = MaterialTheme.colorScheme.onErrorContainer,
        shape = RivuneShapes.medium,
        tonalElevation = RivuneElevation.flat,
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
    failure: UiFailure?,
    isTv: Boolean,
    onSubmit: (String) -> Unit,
    onDismiss: () -> Unit,
) {
    val inputFocus = remember { FocusRequester() }
    val pinFailure = failure == UiFailure.PROFILE_PIN_INVALID || failure == UiFailure.PROFILE_PIN_RATE_LIMITED
    LaunchedEffect(isTv, profile.id, isBusy) {
        if (isTv && !isBusy) inputFocus.requestFocus()
    }
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
                    modifier = Modifier.focusRequester(inputFocus),
                    isError = pinFailure,
                    supportingText = if (pinFailure) failureMessage(checkNotNull(failure)) else null,
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
        shape = RivuneShapes.extraLarge,
    )
}

@Composable
private fun EmptyState(title: String, body: String, modifier: Modifier = Modifier) {
    RivuneFunctionalSurface(modifier = modifier) {
        Column {
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
private fun BusyIndicator(modifier: Modifier = Modifier) {
    val description = stringResource(R.string.loading_in_progress)
    val motionPolicy = LocalRivuneMotionPolicy.current
    Surface(
        modifier = modifier.semantics {
            liveRegion = LiveRegionMode.Polite
            contentDescription = description
        },
        shape = CircleShape,
        color = MaterialTheme.colorScheme.surfaceVariant,
        tonalElevation = RivuneElevation.raised,
    ) {
        if (motionPolicy.ambientAnimations) {
            CircularProgressIndicator(
                modifier = Modifier.padding(RivuneSpacing.sm).size(RivuneSpacing.xl),
                color = MaterialTheme.colorScheme.primary,
                strokeWidth = 2.dp,
            )
        }
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
        UiFailure.PAIRING_LIMIT -> R.string.error_pairing_limit
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
    width >= RivuneBreakpoints.wide -> RivuneSpacing.huge
    width >= RivuneBreakpoints.expanded -> RivuneSpacing.xxl
    width >= RivuneBreakpoints.medium -> RivuneSpacing.xl
    else -> RivuneSpacing.md
}

private fun String.initial(): String = trim().firstOrNull()?.uppercase() ?: ""
