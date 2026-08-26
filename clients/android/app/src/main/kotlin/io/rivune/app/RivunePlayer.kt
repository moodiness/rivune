@file:kotlin.OptIn(androidx.compose.material3.ExperimentalMaterial3Api::class)

package io.rivune.app

import android.app.Activity
import android.content.Context
import android.content.ContextWrapper
import android.content.pm.ActivityInfo
import android.os.Build
import android.view.KeyEvent as AndroidKeyEvent
import android.view.Surface
import android.view.SurfaceHolder
import android.view.SurfaceView
import android.view.View
import android.view.ViewGroup
import androidx.activity.compose.BackHandler
import androidx.annotation.OptIn
import androidx.annotation.StringRes
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.foundation.background
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.focusGroup
import androidx.compose.foundation.focusable
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.rounded.Check
import androidx.compose.material.icons.rounded.ClosedCaption
import androidx.compose.material.icons.rounded.Audiotrack
import androidx.compose.material.icons.rounded.FitScreen
import androidx.compose.material.icons.rounded.WidthWide
import androidx.compose.material.icons.rounded.ZoomInMap
import androidx.compose.material.icons.rounded.ErrorOutline
import androidx.compose.material.icons.rounded.Forward10
import androidx.compose.material.icons.rounded.Pause
import androidx.compose.material.icons.rounded.Lock
import androidx.compose.material.icons.rounded.LockOpen
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material.icons.rounded.Replay
import androidx.compose.material.icons.rounded.Replay10
import androidx.compose.material.icons.rounded.SkipNext
import androidx.compose.material.icons.rounded.Speed
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Slider
import androidx.compose.material3.SliderDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.withFrameNanos
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusProperties
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.DpSize
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.viewinterop.AndroidView
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.core.net.toUri
import androidx.core.view.ViewCompat
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.media3.common.C
import androidx.media3.common.Format
import androidx.media3.common.MediaItem
import androidx.media3.common.MimeTypes
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.TrackSelectionOverride
import androidx.media3.common.Tracks
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.DecoderReuseEvaluation
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.analytics.AnalyticsListener
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.ui.AspectRatioFrameLayout
import androidx.media3.ui.PlayerView
import io.rivune.api.PlaybackMarker
import io.rivune.api.PlaybackMarkerType
import io.rivune.api.PlaybackMediaTimeline
import io.rivune.app.ui.components.RivuneFocusSurface
import io.rivune.app.ui.components.RivuneFunctionalSurface
import io.rivune.app.ui.components.RivunePrimaryButton
import io.rivune.app.ui.components.RivuneTestTags
import io.rivune.app.ui.components.RivuneSecondaryButton
import io.rivune.app.ui.components.RivuneTextButton
import io.rivune.app.ui.theme.LocalRivuneMotionPolicy
import io.rivune.app.ui.theme.RivuneDimensions
import io.rivune.app.ui.theme.RivuneMotion
import io.rivune.app.ui.theme.RivuneScrim
import io.rivune.app.ui.theme.RivuneShapes
import io.rivune.app.ui.theme.RivuneSpacing
import io.rivune.app.ui.theme.finiteAnimationSpec
import java.util.Locale
import java.net.URI
import kotlin.math.roundToLong
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch

private const val PROGRESS_REPORT_INTERVAL_MS = 15_000L
private const val PLAYER_POSITION_INTERVAL_MS = 250L
private const val CONTROLS_HIDE_PHONE_MS = 5_000L
private const val CONTROLS_HIDE_TV_MS = 7_000L
private const val SEEK_INCREMENT_MS = 10_000L
private const val COMPLETION_PERCENT = 90L
private const val TABLET_SMALLEST_WIDTH_DP = 600

private enum class PlayerVisualState {
    Preparing,
    Buffering,
    Ready,
}

internal enum class PlayerOptionsMenu {
    Audio,
    Subtitles,
    Speed,
}
internal fun coordinatedLifecycleResumeIntent(current: Boolean, state: String): Boolean = when (state) {
    "play", "playing" -> true
    "pause", "paused", "stop", "ended" -> false
    else -> current
}

internal data class PlayerTrackKey(
    val groupIndex: Int,
    val trackIndex: Int,
    val groupId: String,
)

internal data class PlayerTrackLabelSource(
    val label: String?,
    val language: String?,
)

private data class PlayerTrackOption(
    val key: PlayerTrackKey,
    val label: String,
    val selected: Boolean,
)

private data class PlayerMenuChoice(
    val label: String,
    val selected: Boolean,
    val onClick: () -> Unit,
)

internal enum class PlaybackFailoverUiState { ADVANCING, SUCCEEDED, EXHAUSTED }

internal fun playbackFailoverUiState(presentation: PlayerPresentation): PlaybackFailoverUiState? = when {
    presentation.failoverAdvancing -> PlaybackFailoverUiState.ADVANCING
    presentation.failover?.status == io.rivune.api.PlaybackFailoverStatus.EXHAUSTED -> PlaybackFailoverUiState.EXHAUSTED
    presentation.failover?.status == io.rivune.api.PlaybackFailoverStatus.ACTIVE && presentation.failover.attemptCount > 0 ->
        PlaybackFailoverUiState.SUCCEEDED
    else -> null
}

@Composable
internal fun RivunePlayerScreen(
    presentation: PlayerPresentation,
    failure: PlayerEngineFailure?,
    remoteCommand: io.rivune.api.PlaybackCommand?,
    playbackRoom: io.rivune.api.PlaybackRoom?,
    onCommandConsumed: () -> Unit,
    onPlaybackState: (Long, Long, Boolean) -> Unit,
    isTv: Boolean,
    frameRateMatching: FrameRateMatchingPreference,
    videoAspect: VideoAspectPreference,
    autoSkipIntro: Boolean = false,
    autoSkipRecap: Boolean = false,
    autoSkipOutro: Boolean = false,
    onProgress: (Int, Int, Boolean) -> Unit,
    onPlaybackEnded: () -> Unit,
    onNext: () -> Unit,
    onClose: () -> Unit,
    onPlaybackError: (PlayerEngineFailure) -> Unit,
    onRetry: () -> Unit,
    onStartOver: () -> Unit,
    onChooseSource: () -> Unit,
) {
    Box(Modifier.fillMaxSize().background(Color.Black)) {
        if (failure == null) {
            when (presentation.engine) {
                EmbeddedPlayerEngine.MEDIA3 -> Media3PlayerScreen(
                    presentation = presentation,
                    remoteCommand = remoteCommand,
                    playbackRoom = playbackRoom,
                    onCommandConsumed = onCommandConsumed,
                    onPlaybackState = onPlaybackState,
                    isTv = isTv,
                    frameRateMatching = frameRateMatching,
                    videoAspect = videoAspect,
                    autoSkipIntro = autoSkipIntro,
                    autoSkipRecap = autoSkipRecap,
                    autoSkipOutro = autoSkipOutro,
                    onProgress = onProgress,
                    onPlaybackEnded = onPlaybackEnded,
                    onNext = onNext,
                    onClose = onClose,
                    onPlaybackError = onPlaybackError,
                )
                EmbeddedPlayerEngine.MPV -> MpvPlayerScreen(
                    presentation = presentation,
                    remoteCommand = remoteCommand,
                    playbackRoom = playbackRoom,
                    onCommandConsumed = onCommandConsumed,
                    onPlaybackState = onPlaybackState,
                    isTv = isTv,
                    videoAspect = videoAspect,
                    autoSkipIntro = autoSkipIntro,
                    autoSkipRecap = autoSkipRecap,
                    autoSkipOutro = autoSkipOutro,
                    onProgress = onProgress,
                    onPlaybackEnded = onPlaybackEnded,
                    onNext = onNext,
                    onClose = onClose,
                    onPlaybackError = onPlaybackError,
                )
            }
            if (presentation.decisionReasons.isNotEmpty()) {
                val reasonLabels = presentation.decisionReasons.map { stringResource(decisionReasonResource(it)) }
                androidx.compose.material3.Surface(
                    color = Color.Black.copy(alpha = 0.72f),
                    shape = MaterialTheme.shapes.medium,
                    modifier = Modifier.align(Alignment.TopCenter).windowInsetsPadding(WindowInsets.safeDrawing).padding(RivuneSpacing.md),
                ) {
                    Text(
                        text = stringResource(
                            R.string.player_decision_reasons,
                            reasonLabels.joinToString(", "),
                        ),
                        color = Color.White,
                        style = MaterialTheme.typography.bodyMedium,
                        modifier = Modifier.padding(horizontal = RivuneSpacing.md, vertical = RivuneSpacing.sm),
                    )
                }
            }
        } else {
            PlayerRecoveryOverlay(
                isTv = isTv,
                failure = failure,
                onRetry = onRetry,
                onStartOver = onStartOver,
                onChooseSource = onChooseSource,
            )
        }
        PlayerFailoverStatusOverlay(presentation)
    }
}

@Composable
private fun BoxScope.PlayerFailoverStatusOverlay(presentation: PlayerPresentation) {
    val status = playbackFailoverUiState(presentation) ?: return
    var showSuccess by remember(presentation.failover?.id, presentation.failover?.attemptCount) { mutableStateOf(true) }
    LaunchedEffect(status) {
        if (status == PlaybackFailoverUiState.SUCCEEDED) {
            delay(4_000)
            showSuccess = false
        }
    }
    if (status == PlaybackFailoverUiState.SUCCEEDED && !showSuccess) return
    val message = stringResource(
        when (status) {
            PlaybackFailoverUiState.ADVANCING -> R.string.player_failover_advancing
            PlaybackFailoverUiState.SUCCEEDED -> R.string.player_failover_succeeded
            PlaybackFailoverUiState.EXHAUSTED -> R.string.player_failover_exhausted
        },
    )
    androidx.compose.material3.Surface(
        color = if (status == PlaybackFailoverUiState.EXHAUSTED) MaterialTheme.colorScheme.errorContainer else Color.Black.copy(alpha = 0.86f),
        contentColor = if (status == PlaybackFailoverUiState.EXHAUSTED) MaterialTheme.colorScheme.onErrorContainer else Color.White,
        shape = MaterialTheme.shapes.medium,
        modifier = Modifier
            .align(Alignment.TopCenter)
            .windowInsetsPadding(WindowInsets.safeDrawing)
            .padding(RivuneSpacing.md)
            .semantics(mergeDescendants = true) { liveRegion = LiveRegionMode.Assertive },
    ) {
        Text(message, modifier = Modifier.padding(horizontal = RivuneSpacing.lg, vertical = RivuneSpacing.sm))
    }
}

@StringRes
internal fun decisionReasonResource(reason: io.rivune.api.PlaybackDecisionReason): Int = when (reason) {
    io.rivune.api.PlaybackDecisionReason.CONTAINER_NOT_SUPPORTED -> R.string.player_reason_container
    io.rivune.api.PlaybackDecisionReason.VIDEO_CODEC_NOT_SUPPORTED -> R.string.player_reason_video_codec
    io.rivune.api.PlaybackDecisionReason.AUDIO_CODEC_NOT_SUPPORTED -> R.string.player_reason_audio_codec
    io.rivune.api.PlaybackDecisionReason.RESOLUTION_LIMIT -> R.string.player_reason_resolution
    io.rivune.api.PlaybackDecisionReason.BITRATE_LIMIT -> R.string.player_reason_bitrate
    io.rivune.api.PlaybackDecisionReason.HDR_NOT_SUPPORTED -> R.string.player_reason_hdr
    io.rivune.api.PlaybackDecisionReason.SUBTITLE_BURN_REQUIRED -> R.string.player_reason_subtitle
}

@Composable
private fun PlayerRecoveryOverlay(
    isTv: Boolean,
    failure: PlayerEngineFailure,
    onRetry: () -> Unit,
    onStartOver: () -> Unit,
    onChooseSource: () -> Unit,
) {
    Dialog(
        onDismissRequest = onChooseSource,
        properties = DialogProperties(
            usePlatformDefaultWidth = false,
            dismissOnClickOutside = false,
            decorFitsSystemWindows = false,
        ),
    ) {
        PlayerRecoveryOverlayContent(
            isTv = isTv,
            failure = failure,
            onRetry = onRetry,
            onStartOver = onStartOver,
            onChooseSource = onChooseSource,
        )
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
internal fun PlayerRecoveryOverlayContent(
    isTv: Boolean,
    failure: PlayerEngineFailure,
    onRetry: () -> Unit,
    onStartOver: () -> Unit,
    onChooseSource: () -> Unit,
) {
    val retryFocus = remember { FocusRequester() }
    LaunchedEffect(isTv) {
        if (isTv) {
            withFrameNanos { }
            retryFocus.requestFocus()
        }
    }
    BoxWithConstraints(
        modifier = Modifier
            .fillMaxSize()
            .background(
                Brush.radialGradient(
                    colors = listOf(
                        MaterialTheme.colorScheme.primaryContainer.copy(alpha = 0.28f),
                        RivuneScrim,
                    ),
                ),
            )
            .pointerInput(Unit) { detectTapGestures { } }
            .focusGroup()
            .windowInsetsPadding(WindowInsets.safeDrawing)
            .padding(if (isTv) RivuneSpacing.display else RivuneSpacing.lg)
            .semantics { liveRegion = LiveRegionMode.Assertive },
        contentAlignment = Alignment.Center,
    ) {
        val cardMaxWidth = if (isTv) {
            RivuneDimensions.dialogMax
        } else {
            RivuneDimensions.dialogMax - RivuneSpacing.huge
        }
        val compactActions = maxWidth < cardMaxWidth
        RivuneFunctionalSurface(
            modifier = Modifier
                .widthIn(max = cardMaxWidth)
                .fillMaxWidth()
                .testTag(RivuneTestTags.PlayerRecoveryCard),
            shape = RivuneShapes.extraLarge,
            contentPadding = PaddingValues(if (isTv) RivuneSpacing.xl else RivuneSpacing.lg),
        ) {
            Column(verticalArrangement = Arrangement.spacedBy(if (isTv) RivuneSpacing.xl else RivuneSpacing.lg)) {
                Row(
                    horizontalArrangement = Arrangement.spacedBy(if (isTv) RivuneSpacing.lg else RivuneSpacing.md),
                    verticalAlignment = Alignment.Top,
                ) {
                    Box(
                        modifier = Modifier
                            .size(if (isTv) RivuneSpacing.display else RivuneSpacing.huge)
                            .background(MaterialTheme.colorScheme.errorContainer, RivuneShapes.pill)
                            .clearAndSetSemantics { },
                        contentAlignment = Alignment.Center,
                    ) {
                        Icon(
                            imageVector = Icons.Rounded.ErrorOutline,
                            contentDescription = null,
                            modifier = Modifier.size(
                                if (isTv) RivuneSpacing.xxl else RivuneDimensions.iconMedium,
                            ),
                            tint = MaterialTheme.colorScheme.onErrorContainer,
                        )
                    }
                    Column(
                        modifier = Modifier.weight(1f),
                        verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
                    ) {
                        Text(
                            text = stringResource(R.string.player_recovery_title),
                            modifier = Modifier.semantics { heading() },
                            style = if (isTv) {
                                MaterialTheme.typography.headlineMedium
                            } else {
                                MaterialTheme.typography.headlineSmall
                            },
                        )
                        Text(
                            text = stringResource(
                                if (failure.reason == PlayerEngineFailureReason.STARTUP_TIMEOUT) {
                                    R.string.player_recovery_startup_body
                                } else {
                                    R.string.player_recovery_body
                                },
                            ),
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            style = if (isTv) MaterialTheme.typography.bodyLarge else MaterialTheme.typography.bodyMedium,
                        )
                    }
                }
                FlowRow(
                    modifier = Modifier.fillMaxWidth(),
                    maxItemsInEachRow = if (compactActions) 1 else 3,
                    horizontalArrangement = Arrangement.spacedBy(
                        space = RivuneSpacing.sm,
                        alignment = Alignment.CenterHorizontally,
                    ),
                    verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
                ) {
                    val actionModifier = if (compactActions) Modifier.fillMaxWidth() else Modifier
                    PlayerRecoveryAction(
                        label = stringResource(R.string.player_recovery_retry),
                        onClick = onRetry,
                        isTv = isTv,
                        icon = Icons.Rounded.Replay,
                        prominent = true,
                        modifier = actionModifier.focusRequester(retryFocus),
                    )
                    PlayerRecoveryAction(
                        label = stringResource(R.string.player_recovery_start_over),
                        onClick = onStartOver,
                        isTv = isTv,
                        modifier = actionModifier,
                    )
                    PlayerRecoveryAction(
                        label = stringResource(R.string.player_recovery_choose_source),
                        onClick = onChooseSource,
                        isTv = isTv,
                        modifier = actionModifier,
                    )
                }
            }
        }
    }
}

@Composable
private fun PlayerRecoveryAction(
    label: String,
    onClick: () -> Unit,
    isTv: Boolean,
    modifier: Modifier = Modifier,
    icon: androidx.compose.ui.graphics.vector.ImageVector? = null,
    prominent: Boolean = false,
) {
    RivuneFocusSurface(
        onClick = onClick,
        isTv = isTv,
        idleColor = Color.Transparent,
        focusedColor = Color.Transparent,
        pressedColor = Color.Transparent,
        selectedColor = Color.Transparent,
        focusScale = RivuneMotion.tvButtonFocusScale,
        showFocusBorder = false,
        shape = RivuneShapes.pill,
        modifier = modifier.heightIn(
            min = if (isTv) RivuneDimensions.buttonHeightTv else RivuneDimensions.touchTarget,
        ),
    ) {
        Row(
            modifier = Modifier.padding(
                horizontal = if (isTv) RivuneSpacing.lg else RivuneSpacing.md,
                vertical = RivuneSpacing.xxs,
            ),
            horizontalArrangement = Arrangement.Center,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            if (icon != null) {
                Icon(
                    imageVector = icon,
                    contentDescription = null,
                    modifier = Modifier.size(
                        if (isTv && prominent) RivuneSpacing.xl else RivuneDimensions.iconMedium,
                    ),
                    tint = MaterialTheme.colorScheme.primary,
                )
                Spacer(Modifier.width(RivuneSpacing.sm))
            }
            Text(
                text = label,
                color = MaterialTheme.colorScheme.onSurface,
                style = when {
                    prominent && isTv -> MaterialTheme.typography.titleMedium
                    prominent -> MaterialTheme.typography.titleSmall
                    isTv -> MaterialTheme.typography.titleSmall
                    else -> MaterialTheme.typography.labelLarge
                },
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

@OptIn(UnstableApi::class)
@Composable
private fun Media3PlayerScreen(
    presentation: PlayerPresentation,
    isTv: Boolean,
    frameRateMatching: FrameRateMatchingPreference,
    videoAspect: VideoAspectPreference,
    autoSkipIntro: Boolean = false,
    autoSkipRecap: Boolean = false,
    autoSkipOutro: Boolean = false,
    onProgress: (Int, Int, Boolean) -> Unit,
    onPlaybackEnded: () -> Unit,
    onNext: () -> Unit,
    onClose: () -> Unit,
    onPlaybackError: (PlayerEngineFailure) -> Unit,
    remoteCommand: io.rivune.api.PlaybackCommand?,
    playbackRoom: io.rivune.api.PlaybackRoom?,
    onCommandConsumed: () -> Unit,
    onPlaybackState: (Long, Long, Boolean) -> Unit,
) {
    val context = LocalContext.current
    val lifecycleOwner = LocalLifecycleOwner.current
    val configuration = LocalConfiguration.current
    val activity = remember(context) { context.findActivity() }
    val usePhonePresentation = !isTv &&
        configuration.smallestScreenWidthDp in 1 until TABLET_SMALLEST_WIDTH_DP
    val isWide = !isTv && configuration.smallestScreenWidthDp >= TABLET_SMALLEST_WIDTH_DP
    val coroutineScope = rememberCoroutineScope()
    val playerLocale = configuration.locales[0] ?: Locale.getDefault()
    val audioTrackFallback = stringResource(R.string.player_audio_track)
    val subtitleTrackFallback = stringResource(R.string.player_subtitle_track)
    val currentOnProgress by rememberUpdatedState(onProgress)
    val currentOnPlaybackEnded by rememberUpdatedState(onPlaybackEnded)
    val currentOnNext by rememberUpdatedState(onNext)
    val currentOnClose by rememberUpdatedState(onClose)
    val currentOnPlaybackError by rememberUpdatedState(onPlaybackError)
    var closeRequested by remember(presentation.key) { mutableStateOf(false) }
    var finalProgressReporter by remember(presentation.key) { mutableStateOf<(() -> Unit)?>(null) }
    var visualState by remember(presentation.key) { mutableStateOf(PlayerVisualState.Preparing) }
    var controlsVisible by remember(presentation.key) { mutableStateOf(true) }
    var optionsMenu by remember(presentation.key) { mutableStateOf<PlayerOptionsMenu?>(null) }
    var audioTracks by remember(presentation.key) { mutableStateOf(emptyList<PlayerTrackOption>()) }
    var subtitleTracks by remember(presentation.key) { mutableStateOf(emptyList<PlayerTrackOption>()) }
    var activePlayerView by remember(presentation.key) { mutableStateOf<PlayerView?>(null) }
    var sessionAspect by remember(presentation.key) { mutableStateOf(videoAspect) }
    var playbackSpeed by remember(presentation.key) { mutableStateOf(1f) }
    var controlsLocked by remember(presentation.key) { mutableStateOf(false) }
    var unlockVisible by remember(presentation.key) { mutableStateOf(false) }
    var playbackEnded by remember(presentation.key) { mutableStateOf(false) }
    var isPlaying by remember(presentation.key) { mutableStateOf(false) }
    var resumeAfterLifecyclePause by remember(presentation.key) { mutableStateOf(true) }
    val inspectedDurationMs = presentation.durationSeconds.coerceAtLeast(0).toLong() * 1_000L
    var positionMs by remember(presentation.key) { mutableLongStateOf(presentation.startPositionMs.coerceAtLeast(0L)) }
    var durationMs by remember(presentation.key) { mutableLongStateOf(inspectedDurationMs) }
    var requestTransportFocusWhenVisible by remember(presentation.key) { mutableStateOf(false) }
    var interactionGeneration by remember(presentation.key) { mutableLongStateOf(0L) }
    val currentPlayerLocale by rememberUpdatedState(playerLocale)
    val currentAudioTrackFallback by rememberUpdatedState(audioTrackFallback)
    val currentSubtitleTrackFallback by rememberUpdatedState(subtitleTrackFallback)
    var markerWasVisible by remember(presentation.key) { mutableStateOf(false) }
    var autoSkippedMarkerEntries by remember(presentation.key) { mutableStateOf(emptySet<Int>()) }

    val closeFocus = remember { FocusRequester() }
    val rewindFocus = remember { FocusRequester() }
    val playFocus = remember { FocusRequester() }
    val forwardFocus = remember { FocusRequester() }
    val seekFocus = remember { FocusRequester() }
    val audioFocus = remember { FocusRequester() }
    val subtitleFocus = remember { FocusRequester() }
    val aspectFocus = remember { FocusRequester() }
    val speedFocus = remember { FocusRequester() }
    val lockFocus = remember { FocusRequester() }
    val playerRootFocus = remember { FocusRequester() }
    val markerFocus = remember { FocusRequester() }
    val replayFocus = remember { FocusRequester() }
    val nextFocus = remember { FocusRequester() }
    val firstMenuChoiceFocus = remember { FocusRequester() }
    val unlockFocus = remember { FocusRequester() }

    DisposableEffect(activity, lifecycleOwner, usePhonePresentation) {
        if (activity == null || !usePhonePresentation) {
            onDispose { }
        } else {
            val window = activity.window
            val decorView = window.decorView
            val insetsController = WindowCompat.getInsetsController(window, decorView)
            val initialOrientation = activity.requestedOrientation
            val initialInsets = ViewCompat.getRootWindowInsets(decorView)
            val statusBarsWereVisible = initialInsets?.isVisible(WindowInsetsCompat.Type.statusBars()) ?: true
            val navigationBarsWereVisible = initialInsets?.isVisible(WindowInsetsCompat.Type.navigationBars()) ?: true
            val initialSystemBarsBehavior = insetsController.systemBarsBehavior
            val lightStatusBarsWereEnabled = insetsController.isAppearanceLightStatusBars
            val lightNavigationBarsWereEnabled = insetsController.isAppearanceLightNavigationBars

            fun enterPhonePresentation() {
                activity.requestedOrientation = ActivityInfo.SCREEN_ORIENTATION_SENSOR_LANDSCAPE
                insetsController.isAppearanceLightStatusBars = false
                insetsController.isAppearanceLightNavigationBars = false
                insetsController.systemBarsBehavior =
                    WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
                insetsController.hide(WindowInsetsCompat.Type.systemBars())
            }
            val immersiveLifecycleObserver = LifecycleEventObserver { _, event ->
                if (event == Lifecycle.Event.ON_RESUME) enterPhonePresentation()
            }
            lifecycleOwner.lifecycle.addObserver(immersiveLifecycleObserver)
            enterPhonePresentation()

            onDispose {
                lifecycleOwner.lifecycle.removeObserver(immersiveLifecycleObserver)
                activity.requestedOrientation = initialOrientation
                insetsController.systemBarsBehavior = initialSystemBarsBehavior
                insetsController.isAppearanceLightStatusBars = lightStatusBarsWereEnabled
                insetsController.isAppearanceLightNavigationBars = lightNavigationBarsWereEnabled
                if (statusBarsWereVisible) {
                    insetsController.show(WindowInsetsCompat.Type.statusBars())
                } else {
                    insetsController.hide(WindowInsetsCompat.Type.statusBars())
                }
                if (navigationBarsWereVisible) {
                    insetsController.show(WindowInsetsCompat.Type.navigationBars())
                } else {
                    insetsController.hide(WindowInsetsCompat.Type.navigationBars())
                }
            }
        }
    }

    val player = remember(context, presentation.key, frameRateMatching) {
        ExoPlayer.Builder(context)
            .setMediaSourceFactory(DefaultMediaSourceFactory(RivuneDataSourceFactory(context)))
            .setVideoChangeFrameRateStrategy(frameRateMatching.media3Strategy(Build.VERSION.SDK_INT))
            .build()
            .also { newPlayer ->
                try {
                    newPlayer.setMediaItem(
                        presentation.toMediaItem(),
                        mediaPlaybackPositionMs(
                            presentation.startPositionMs,
                            presentation.timelineStartPositionMs,
                            presentation.mediaTimeline,
                        ),
                    )
                    newPlayer.playWhenReady = true
                    newPlayer.prepare()
                } catch (failure: Throwable) {
                    newPlayer.release()
                    throw failure
                }
            }
    }

    fun refreshTrackOptions(tracks: Tracks) {
        audioTracks = playerTrackOptions(
            tracks = tracks,
            trackType = C.TRACK_TYPE_AUDIO,
            locale = currentPlayerLocale,
            fallbackLabel = currentAudioTrackFallback,
        )
        subtitleTracks = playerTrackOptions(
            tracks = tracks,
            trackType = C.TRACK_TYPE_TEXT,
            locale = currentPlayerLocale,
            fallbackLabel = currentSubtitleTrackFallback,
        )
    }

    fun noteInteraction(requestTransportFocus: Boolean = false) {
        if (controlsLocked) return
        controlsVisible = true
        interactionGeneration += 1L
        if (requestTransportFocus && isTv) requestTransportFocusWhenVisible = true
    }

    fun requestClose() {
        if (!closeRequested) {
            finalProgressReporter?.invoke()
            closeRequested = true
            currentOnClose()
        }
    }

    fun requestNext() {
        if (!closeRequested && presentation.nextEpisode != null) {
            finalProgressReporter?.invoke()
            closeRequested = true
            currentOnNext()
        }
    }
    fun resetAutoSkipAfterUserSeek(targetMs: Long) {
        autoSkippedMarkerEntries = autoSkipConsumedAfterUserSeek(
            markers = presentation.markers,
            consumedEntries = autoSkippedMarkerEntries,
            userSeekPositionMs = targetMs,
        )
    }

    fun togglePlayback() {
        if (controlsLocked) return
        if (player.playbackState == Player.STATE_ENDED) {
            val replayPositionMs = absolutePlaybackPositionMs(0L, presentation.timelineStartPositionMs, presentation.mediaTimeline)
            resetAutoSkipAfterUserSeek(replayPositionMs)
            playbackEnded = false
            player.seekTo(0L)
            positionMs = replayPositionMs
            player.play()
        } else if (player.isPlaying) {
            player.pause()
        } else {
            player.play()
        }
        noteInteraction()
    }




    fun seekBy(deltaMs: Long) {
        if (controlsLocked) return
        val targetMs = (positionMs + deltaMs).coerceIn(0L, durationMs.takeIf { it > 0L } ?: Long.MAX_VALUE)
        resetAutoSkipAfterUserSeek(targetMs)
        player.seekTo(mediaPlaybackPositionMs(targetMs, presentation.timelineStartPositionMs, presentation.mediaTimeline))
        positionMs = targetMs
        noteInteraction()
    }

    fun dismissChromeOrClose() {
        when {
            controlsLocked -> {
                controlsLocked = false
                unlockVisible = false
                noteInteraction(requestTransportFocus = true)
            }
            controlsVisible || optionsMenu != null -> {
                optionsMenu = null
                controlsVisible = false
                if (isTv) playerRootFocus.requestFocus()
            }
            else -> requestClose()
        }
    }

    BackHandler(enabled = !closeRequested, onBack = ::dismissChromeOrClose)

    DisposableEffect(player, lifecycleOwner) {
        var finished = false
        var errorDelivered = false
        var endDelivered = false
        resumeAfterLifecyclePause = player.playWhenReady
        var reportingJob: Job? = null

        fun reportProgress(naturalEnd: Boolean = false) {
            val reportedDurationMs = resolvedPlaybackDurationMs(
                inspectedDurationMs,
                player.duration,
                presentation.timelineStartPositionMs,
                presentation.mediaTimeline,
            )
            val reportedPositionMs = absolutePlaybackPositionMs(
                player.currentPosition,
                presentation.timelineStartPositionMs,
                presentation.mediaTimeline,
            ).let { position ->
                if (reportedDurationMs > 0L) position.coerceAtMost(reportedDurationMs) else position
            }
            val positionSeconds = (reportedPositionMs / 1_000L).coerceAtMost(Int.MAX_VALUE.toLong()).toInt()
            val durationSeconds = if (reportedDurationMs > 0L) {
                ((reportedDurationMs + 999L) / 1_000L).coerceAtMost(Int.MAX_VALUE.toLong()).toInt()
            } else 0
            val completed = naturalEnd ||
                reportedDurationMs > 0L && reportedPositionMs >= completionThreshold(reportedDurationMs)
            currentOnProgress(positionSeconds, durationSeconds, completed)
        }
        finalProgressReporter = ::reportProgress

        var hasBeenReady = player.playbackState == Player.STATE_READY

        fun updateVisualState(playbackState: Int) {
            visualState = when (playbackState) {
                Player.STATE_IDLE -> PlayerVisualState.Preparing
                Player.STATE_BUFFERING -> if (hasBeenReady) PlayerVisualState.Buffering else PlayerVisualState.Preparing
                Player.STATE_READY -> {
                    hasBeenReady = true
                    PlayerVisualState.Ready
                }
                else -> PlayerVisualState.Ready
            }
        }

        val playerListener = object : Player.Listener {
            override fun onPlaybackStateChanged(playbackState: Int) {
                updateVisualState(playbackState)
                if (playbackState == Player.STATE_ENDED) {
                    playbackEnded = true
                    controlsVisible = true
                    optionsMenu = null
                }
                if (isNaturalPlaybackEnd(playbackState) && !endDelivered && !finished) {
                    endDelivered = true
                    reportProgress(naturalEnd = true)
                    currentOnPlaybackEnded()
                }
            }

            override fun onIsPlayingChanged(playing: Boolean) {
                isPlaying = playing
                activePlayerView?.let { updateMedia3KeepScreenOn(it, playing) }
            }

            override fun onTracksChanged(tracks: Tracks) {
                refreshTrackOptions(tracks)
            }

            override fun onPlayerError(error: PlaybackException) {
                visualState = PlayerVisualState.Ready
                if (!errorDelivered && !finished) {
                    errorDelivered = true
                    reportProgress()
                    currentOnPlaybackError(
                        PlayerEngineFailure(
                            positionMs = absolutePlaybackPositionMs(
                                player.currentPosition,
                                presentation.timelineStartPositionMs,
                                presentation.mediaTimeline,
                            ),
                            fallbackEligible = presentation.fallbackAllowed && error.isMedia3FallbackEligible(),
                        ),
                    )
                }
            }
        }
        player.addListener(playerListener)
        updateVisualState(player.playbackState)
        isPlaying = player.isPlaying
        refreshTrackOptions(player.currentTracks)

        fun finish() {
            if (finished) return
            finished = true
            reportingJob?.cancel()
            activePlayerView?.let { updateMedia3KeepScreenOn(it, isPlaying = false) }
            try {
                reportProgress()
            } finally {
                player.removeListener(playerListener)
                player.release()
            }
        }

        val lifecycleObserver = LifecycleEventObserver { _, event ->
            when (event) {
                Lifecycle.Event.ON_RESUME -> if (resumeAfterLifecyclePause && !finished) player.play()
                Lifecycle.Event.ON_PAUSE -> {
                    resumeAfterLifecyclePause = player.playWhenReady
                    player.pause()
                }
                Lifecycle.Event.ON_DESTROY -> finish()
                else -> Unit
            }
        }
        lifecycleOwner.lifecycle.addObserver(lifecycleObserver)
        if (!lifecycleOwner.lifecycle.currentState.isAtLeast(Lifecycle.State.RESUMED)) player.pause()

        reportingJob = coroutineScope.launch {
            while (isActive) {
                delay(PROGRESS_REPORT_INTERVAL_MS)
                reportProgress()
            }
        }

        onDispose {
            finalProgressReporter = null
            lifecycleOwner.lifecycle.removeObserver(lifecycleObserver)
            finish()
        }
    }

    LaunchedEffect(player, presentation.key) {
        while (isActive) {
            positionMs = absolutePlaybackPositionMs(
                player.currentPosition,
                presentation.timelineStartPositionMs,
                presentation.mediaTimeline,
            )
            durationMs = resolvedPlaybackDurationMs(
                inspectedDurationMs,
                player.duration,
                presentation.timelineStartPositionMs,
                presentation.mediaTimeline,
            )
            onPlaybackState(positionMs, durationMs, player.isPlaying)
            delay(PLAYER_POSITION_INTERVAL_MS)
        }
    }
    LaunchedEffect(remoteCommand?.operationId) {
        val command = remoteCommand ?: return@LaunchedEffect
        command.positionMilliseconds?.let { target ->
            player.seekTo(mediaPlaybackPositionMs(target, presentation.timelineStartPositionMs, presentation.mediaTimeline))
        }
        resumeAfterLifecyclePause = coordinatedLifecycleResumeIntent(resumeAfterLifecyclePause, command.command.name.lowercase())
        when (command.command) {
            io.rivune.api.PlaybackCommandType.PLAY -> player.play()
            io.rivune.api.PlaybackCommandType.PAUSE -> player.pause()
            io.rivune.api.PlaybackCommandType.SEEK -> Unit
            io.rivune.api.PlaybackCommandType.STOP -> currentOnClose()
            io.rivune.api.PlaybackCommandType.LOAD -> Unit
        }
        onCommandConsumed()
    }

    LaunchedEffect(playbackRoom?.version) {
        val room = playbackRoom ?: return@LaunchedEffect
        if (room.currentMemberIsHost) return@LaunchedEffect
        val target = room.positionMilliseconds
        if (kotlin.math.abs(positionMs - target) > 1_500) {
            player.seekTo(mediaPlaybackPositionMs(target, presentation.timelineStartPositionMs, presentation.mediaTimeline))
        }
        resumeAfterLifecyclePause = coordinatedLifecycleResumeIntent(resumeAfterLifecyclePause, room.state)
        when (room.state) {
            "playing" -> player.play()
            "paused" -> player.pause()
            "ended" -> onClose()
        }
    }


    LaunchedEffect(controlsVisible, isPlaying, interactionGeneration, playbackEnded, optionsMenu, controlsLocked) {
        if (controlsVisible && isPlaying && !playbackEnded && optionsMenu == null && !controlsLocked) {
            delay(if (isTv) CONTROLS_HIDE_TV_MS else CONTROLS_HIDE_PHONE_MS)
            controlsVisible = false
            if (isTv) playerRootFocus.requestFocus()
        }
    }

    LaunchedEffect(isTv, presentation.key) {
        if (isTv) playFocus.requestFocus()
    }

    LaunchedEffect(controlsVisible, requestTransportFocusWhenVisible, isTv) {
        if (controlsVisible && requestTransportFocusWhenVisible && isTv) {
            withFrameNanos { }
            playFocus.requestFocus()
            requestTransportFocusWhenVisible = false
        }
    }

    LaunchedEffect(optionsMenu, isTv) {
        if (optionsMenu != null && isTv) {
            withFrameNanos { }
            firstMenuChoiceFocus.requestFocus()
        }
    }

    LaunchedEffect(controlsLocked, isTv) {
        if (controlsLocked && isTv) playerRootFocus.requestFocus()
    }

    LaunchedEffect(controlsLocked, unlockVisible, isTv) {
        if (controlsLocked && unlockVisible && isTv) {
            withFrameNanos { }
            unlockFocus.requestFocus()
        }
    }

    val forcedFrameRateController = remember(player, frameRateMatching) {
        player.takeIf {
            frameRateMatching == FrameRateMatchingPreference.ENABLED && Build.VERSION.SDK_INT >= Build.VERSION_CODES.S
        }?.let(::ForcedFrameRateController)
    }
    DisposableEffect(forcedFrameRateController) {
        onDispose { forcedFrameRateController?.release() }
    }

    val activeMarkerEntry = activePlaybackMarkerEntry(presentation.markers, positionMs)
    val activeMarker = activeMarkerEntry?.marker
    val activeMarkerAutoSkipEnabled = activeMarker?.let {
        shouldAutoSkipPlaybackMarker(it.type, autoSkipIntro, autoSkipRecap, autoSkipOutro)
    } == true
    val manualActiveMarker = activeMarker.takeUnless { activeMarkerAutoSkipEnabled }
    LaunchedEffect(manualActiveMarker, isTv) {
        if (manualActiveMarker == null && markerWasVisible && isTv) playerRootFocus.requestFocus()
        markerWasVisible = manualActiveMarker != null
    }
    LaunchedEffect(activeMarkerEntry, activeMarkerAutoSkipEnabled, autoSkippedMarkerEntries) {
        val entry = activeMarkerEntry ?: return@LaunchedEffect
        if (!activeMarkerAutoSkipEnabled || entry.index in autoSkippedMarkerEntries) return@LaunchedEffect
        autoSkippedMarkerEntries = autoSkippedMarkerEntries + entry.index
        player.seekTo(mediaPlaybackPositionMs(entry.endMs, presentation.timelineStartPositionMs, presentation.mediaTimeline))
        positionMs = entry.endMs
    }
    val motionPolicy = LocalRivuneMotionPolicy.current
    val markerBottomPadding = if (controlsVisible) {
        if (isTv) RivuneSpacing.display + RivuneSpacing.display else RivuneSpacing.display + RivuneSpacing.huge
    } else {
        if (isTv) RivuneSpacing.huge else RivuneSpacing.xl
    }

    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(Color.Black)
            .focusGroup()
            .focusRequester(playerRootFocus)
            .focusable()
            .onPreviewKeyEvent { event ->
                if (event.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
                if (controlsLocked) {
                    return@onPreviewKeyEvent when (event.nativeKeyEvent.keyCode) {
                        AndroidKeyEvent.KEYCODE_DPAD_CENTER,
                        AndroidKeyEvent.KEYCODE_ENTER,
                        AndroidKeyEvent.KEYCODE_NUMPAD_ENTER,
                        -> {
                            if (unlockVisible) {
                                controlsLocked = false
                                unlockVisible = false
                                noteInteraction(requestTransportFocus = true)
                            } else {
                                unlockVisible = true
                            }
                            true
                        }
                        AndroidKeyEvent.KEYCODE_DPAD_UP,
                        AndroidKeyEvent.KEYCODE_DPAD_DOWN,
                        AndroidKeyEvent.KEYCODE_DPAD_LEFT,
                        AndroidKeyEvent.KEYCODE_DPAD_RIGHT,
                        -> {
                            unlockVisible = true
                            true
                        }
                        AndroidKeyEvent.KEYCODE_MEDIA_PLAY_PAUSE,
                        AndroidKeyEvent.KEYCODE_MEDIA_PLAY,
                        AndroidKeyEvent.KEYCODE_MEDIA_PAUSE,
                        AndroidKeyEvent.KEYCODE_MEDIA_REWIND,
                        AndroidKeyEvent.KEYCODE_MEDIA_FAST_FORWARD,
                        -> true
                        else -> false
                    }
                }
                when (event.nativeKeyEvent.keyCode) {
                    AndroidKeyEvent.KEYCODE_MEDIA_PLAY_PAUSE -> {
                        togglePlayback()
                        true
                    }
                    AndroidKeyEvent.KEYCODE_MEDIA_PLAY -> {
                        player.play()
                        noteInteraction()
                        true
                    }
                    AndroidKeyEvent.KEYCODE_MEDIA_PAUSE -> {
                        player.pause()
                        noteInteraction()
                        true
                    }
                    AndroidKeyEvent.KEYCODE_MEDIA_REWIND -> {
                        seekBy(-SEEK_INCREMENT_MS)
                        true
                    }
                    AndroidKeyEvent.KEYCODE_MEDIA_FAST_FORWARD -> {
                        seekBy(SEEK_INCREMENT_MS)
                        true
                    }
                    AndroidKeyEvent.KEYCODE_DPAD_CENTER,
                    AndroidKeyEvent.KEYCODE_ENTER,
                    AndroidKeyEvent.KEYCODE_NUMPAD_ENTER,
                    AndroidKeyEvent.KEYCODE_DPAD_UP,
                    AndroidKeyEvent.KEYCODE_DPAD_DOWN,
                    AndroidKeyEvent.KEYCODE_DPAD_LEFT,
                    AndroidKeyEvent.KEYCODE_DPAD_RIGHT,
                    -> if (!controlsVisible) {
                        noteInteraction(requestTransportFocus = true)
                        true
                    } else {
                        interactionGeneration += 1L
                        false
                    }
                    else -> false
                }
            },
    ) {
        AndroidView(
            factory = { viewContext ->
                PlayerView(viewContext).apply {
                    layoutParams = ViewGroup.LayoutParams(
                        ViewGroup.LayoutParams.MATCH_PARENT,
                        ViewGroup.LayoutParams.MATCH_PARENT,
                    )
                    resizeMode = sessionAspect.resizeMode()
                    useController = false
                    setShowBuffering(PlayerView.SHOW_BUFFERING_NEVER)
                    setShutterBackgroundColor(android.graphics.Color.BLACK)
                    isFocusable = false
                    isFocusableInTouchMode = false
                    this.player = player
                    forcedFrameRateController?.attach(videoSurfaceView)
                    activePlayerView = this
                    updateMedia3KeepScreenOn(this, player.isPlaying)
                }
            },
            update = { playerView ->
                activePlayerView = playerView
                playerView.player = player
                updateMedia3KeepScreenOn(playerView, isPlaying)
                playerView.resizeMode = sessionAspect.resizeMode()
                playerView.useController = false
                forcedFrameRateController?.attach(playerView.videoSurfaceView)
            },
            onRelease = { playerView ->
                forcedFrameRateController?.attach(null)
                releaseMedia3PlayerView(playerView)
                if (activePlayerView === playerView) activePlayerView = null
            },
            modifier = Modifier.fillMaxSize(),
        )

        Box(
            modifier = Modifier
                .fillMaxSize()
                .pointerInput(presentation.key, controlsLocked, controlsVisible) {
                    detectTapGestures {
                        when {
                            controlsLocked -> unlockVisible = true
                            controlsVisible -> {
                                controlsVisible = false
                                optionsMenu = null
                            }
                            else -> noteInteraction()
                        }
                    }
                },
        )


        AnimatedVisibility(
            visible = controlsVisible && !controlsLocked,
            enter = fadeIn(motionPolicy.finiteAnimationSpec(RivuneMotion.fast)),
            exit = fadeOut(motionPolicy.finiteAnimationSpec(RivuneMotion.fast)),
            modifier = Modifier.fillMaxSize(),
        ) {
            PlayerChrome(
                title = presentation.title,
                isTv = isTv,
                isWide = isWide,
                enabled = !closeRequested,
                isPlaying = isPlaying,
                playbackEnded = playbackEnded,
                durationMs = durationMs,
                positionMs = positionMs,
                audioTracks = audioTracks,
                subtitleTracks = subtitleTracks,
                sessionAspect = sessionAspect,
                playbackSpeed = playbackSpeed,
                optionsMenu = optionsMenu,
                hasNext = presentation.nextEpisode != null,
                activeMarker = manualActiveMarker,
                closeFocus = closeFocus,
                rewindFocus = rewindFocus,
                playFocus = playFocus,
                forwardFocus = forwardFocus,
                seekFocus = seekFocus,
                audioFocus = audioFocus,
                subtitleFocus = subtitleFocus,
                aspectFocus = aspectFocus,
                speedFocus = speedFocus,
                lockFocus = lockFocus,
                markerFocus = markerFocus,
                replayFocus = replayFocus,
                nextFocus = nextFocus,
                firstMenuChoiceFocus = firstMenuChoiceFocus,
                onClose = ::requestClose,
                onTogglePlayback = ::togglePlayback,
                onSeekBack = { seekBy(-SEEK_INCREMENT_MS) },
                onSeekForward = { seekBy(SEEK_INCREMENT_MS) },
                onSeek = { target ->
                    val boundedTarget = target.coerceIn(0L, durationMs.takeIf { it > 0L } ?: Long.MAX_VALUE)
                    resetAutoSkipAfterUserSeek(boundedTarget)
                    player.seekTo(
                        mediaPlaybackPositionMs(boundedTarget, presentation.timelineStartPositionMs, presentation.mediaTimeline),
                    )
                    positionMs = boundedTarget
                    noteInteraction()
                },
                onToggleMenu = { requested ->
                    optionsMenu = toggledPlayerOptionsMenu(optionsMenu, requested)
                    noteInteraction()
                },
                onAudioSelected = { key ->
                    selectPlayerTrack(player, C.TRACK_TYPE_AUDIO, key)
                    audioTracks = audioTracks.map { it.copy(selected = it.key == key) }
                    optionsMenu = null
                    noteInteraction()
                    if (isTv) audioFocus.requestFocus()
                },
                onSubtitleSelected = { key ->
                    selectPlayerTrack(player, C.TRACK_TYPE_TEXT, key)
                    subtitleTracks = subtitleTracks.map { it.copy(selected = key != null && it.key == key) }
                    optionsMenu = null
                    noteInteraction()
                    if (isTv) subtitleFocus.requestFocus()
                },
                onCycleAspect = {
                    val nextAspect = sessionAspect.nextVideoAspect()
                    sessionAspect = nextAspect
                    optionsMenu = null
                    activePlayerView?.resizeMode = nextAspect.resizeMode()
                    noteInteraction()
                    if (isTv) aspectFocus.requestFocus()
                },
                onSpeedSelected = { speed ->
                    playbackSpeed = speed
                    player.setPlaybackSpeed(speed)
                    optionsMenu = null
                    noteInteraction()
                    if (isTv) speedFocus.requestFocus()
                },
                onLock = {
                    controlsLocked = true
                    unlockVisible = false
                    controlsVisible = false
                    optionsMenu = null
                },
                onReplay = {
                    val replayPositionMs = absolutePlaybackPositionMs(0L, presentation.timelineStartPositionMs, presentation.mediaTimeline)
                    resetAutoSkipAfterUserSeek(replayPositionMs)
                    playbackEnded = false
                    player.seekTo(0L)
                    positionMs = replayPositionMs
                    player.play()
                    noteInteraction()
                },
                onNext = ::requestNext,
            )
        }

        if (visualState != PlayerVisualState.Ready) {
            PlayerStatus(
                state = visualState,
                isTv = isTv,
                modifier = Modifier
                    .align(Alignment.TopCenter)
                    .windowInsetsPadding(WindowInsets.safeDrawing)
                    .padding(
                        top = if (isTv) {
                            RivuneSpacing.display + RivuneSpacing.huge
                        } else {
                            RivuneSpacing.display + RivuneSpacing.md
                        },
                    ),
            )
        }

        if (controlsLocked && unlockVisible) {
            PlayerIconAction(
                icon = Icons.Rounded.LockOpen,
                label = stringResource(R.string.player_unlock_controls),
                isTv = isTv,
                enabled = !closeRequested,
                prominent = true,
                onClick = {
                    controlsLocked = false
                    unlockVisible = false
                    noteInteraction(requestTransportFocus = true)
                },
                modifier = Modifier.align(Alignment.Center).focusRequester(unlockFocus),
            )
        }

        if (!controlsLocked) {
            MarkerSkipAction(
                marker = manualActiveMarker,
                isTv = isTv,
                focusRequester = markerFocus,
                modifier = Modifier
                    .align(Alignment.BottomEnd)
                    .windowInsetsPadding(WindowInsets.safeDrawing)
                    .padding(
                        end = if (isTv) RivuneSpacing.display else if (isWide) RivuneSpacing.xxl else RivuneSpacing.md,
                        bottom = markerBottomPadding,
                    )
                    .focusProperties {
                        left = playerRootFocus
                        up = playerRootFocus
                    },
                onClick = { marker ->
                    playbackMarkerSeekTargetMs(marker)?.let { targetMs ->
                        player.seekTo(mediaPlaybackPositionMs(targetMs, presentation.timelineStartPositionMs, presentation.mediaTimeline))
                        positionMs = targetMs
                    }
                    noteInteraction()
                    if (isTv) playerRootFocus.requestFocus()
                },
            )
        }
    }
}

internal fun updateMedia3KeepScreenOn(playerView: PlayerView, isPlaying: Boolean) {
    playerView.keepScreenOn = isPlaying
}

internal fun releaseMedia3PlayerView(playerView: PlayerView) {
    playerView.keepScreenOn = false
    playerView.player = null
}

@Composable
private fun MpvPlayerScreen(
    presentation: PlayerPresentation,
    isTv: Boolean,
    videoAspect: VideoAspectPreference,
    autoSkipIntro: Boolean,
    autoSkipRecap: Boolean,
    autoSkipOutro: Boolean,
    onProgress: (Int, Int, Boolean) -> Unit,
    onPlaybackEnded: () -> Unit,
    onNext: () -> Unit,
    onClose: () -> Unit,
    onPlaybackError: (PlayerEngineFailure) -> Unit,
    remoteCommand: io.rivune.api.PlaybackCommand?,
    playbackRoom: io.rivune.api.PlaybackRoom?,
    onCommandConsumed: () -> Unit,
    onPlaybackState: (Long, Long, Boolean) -> Unit,
) {
    val context = LocalContext.current
    val lifecycleOwner = LocalLifecycleOwner.current
    val configuration = LocalConfiguration.current
    val coroutineScope = rememberCoroutineScope()
    val isWide = !isTv && configuration.smallestScreenWidthDp >= TABLET_SMALLEST_WIDTH_DP
    val locale = configuration.locales[0] ?: Locale.getDefault()
    val activity = remember(context) { context.findActivity() }
    val usePhonePresentation = !isTv &&
        configuration.smallestScreenWidthDp in 1 until TABLET_SMALLEST_WIDTH_DP
    PlayerImmersivePresentationEffect(activity, lifecycleOwner, usePhonePresentation)
    val audioFallback = stringResource(R.string.player_audio_track)
    val subtitleFallback = stringResource(R.string.player_subtitle_track)
    val currentOnProgress by rememberUpdatedState(onProgress)
    val currentOnPlaybackEnded by rememberUpdatedState(onPlaybackEnded)
    val currentOnNext by rememberUpdatedState(onNext)
    val currentOnClose by rememberUpdatedState(onClose)
    val currentOnPlaybackError by rememberUpdatedState(onPlaybackError)
    var closeRequested by remember(presentation.key) { mutableStateOf(false) }
    var visualState by remember(presentation.key) { mutableStateOf(PlayerVisualState.Preparing) }
    var controlsVisible by remember(presentation.key) { mutableStateOf(true) }
    var optionsMenu by remember(presentation.key) { mutableStateOf<PlayerOptionsMenu?>(null) }
    var audioTracks by remember(presentation.key) { mutableStateOf(emptyList<PlayerTrackOption>()) }
    var subtitleTracks by remember(presentation.key) { mutableStateOf(emptyList<PlayerTrackOption>()) }
    var sessionAspect by remember(presentation.key) { mutableStateOf(videoAspect) }
    var playbackSpeed by remember(presentation.key) { mutableStateOf(1f) }
    var controlsLocked by remember(presentation.key) { mutableStateOf(false) }
    var unlockVisible by remember(presentation.key) { mutableStateOf(false) }
    var playbackEnded by remember(presentation.key) { mutableStateOf(false) }
    var playbackRequested by remember(presentation.key) { mutableStateOf(true) }
    var resumeAfterPause by remember(presentation.key) { mutableStateOf(true) }
    var playbackIsPlaying by remember(presentation.key) { mutableStateOf(false) }
    var playbackPositionMs by remember(presentation.key) { mutableLongStateOf(presentation.startPositionMs.coerceAtLeast(0L)) }
    var playbackDurationMs by remember(presentation.key) { mutableLongStateOf(presentation.durationSeconds.coerceAtLeast(0).toLong() * 1_000L) }
    var interactionGeneration by remember(presentation.key) { mutableLongStateOf(0L) }
    var requestTransportFocusWhenVisible by remember(presentation.key) { mutableStateOf(false) }
    var markerWasVisible by remember(presentation.key) { mutableStateOf(false) }
    var autoSkippedMarkerEntries by remember(presentation.key) { mutableStateOf(emptySet<Int>()) }
    var terminalDelivered by remember(presentation.key) { mutableStateOf(false) }
    var finalProgressReporter by remember(presentation.key) { mutableStateOf<(() -> Unit)?>(null) }

    val closeFocus = remember { FocusRequester() }
    val rewindFocus = remember { FocusRequester() }
    val playFocus = remember { FocusRequester() }
    val forwardFocus = remember { FocusRequester() }
    val seekFocus = remember { FocusRequester() }
    val audioFocus = remember { FocusRequester() }
    val subtitleFocus = remember { FocusRequester() }
    val aspectFocus = remember { FocusRequester() }
    val speedFocus = remember { FocusRequester() }
    val lockFocus = remember { FocusRequester() }
    val playerRootFocus = remember { FocusRequester() }
    val markerFocus = remember { FocusRequester() }
    val replayFocus = remember { FocusRequester() }
    val nextFocus = remember { FocusRequester() }
    val firstMenuChoiceFocus = remember { FocusRequester() }
    val unlockFocus = remember { FocusRequester() }

    fun trackOptions(tracks: List<MpvTrack>, fallback: String): List<PlayerTrackOption> {
        val labels = playerTrackLabels(tracks.map { PlayerTrackLabelSource(it.title, it.language) }, locale, fallback)
        return tracks.mapIndexed { index, track ->
            PlayerTrackOption(
                PlayerTrackKey(track.nativeId ?: -1, index, track.identity),
                labels[index],
                track.selected,
            )
        }
    }
    val controller = remember(context, presentation.key) {
        MpvPlaybackController(context, presentation, object : MpvPlaybackListener {
            override fun onStateChanged(state: MpvPlaybackState) {
                visualState = when (state) {
                    MpvPlaybackState.PREPARING -> PlayerVisualState.Preparing
                    MpvPlaybackState.BUFFERING -> PlayerVisualState.Buffering
                    MpvPlaybackState.READY -> PlayerVisualState.Ready
                }
            }
            override fun onPlayingChanged(isPlaying: Boolean) { playbackIsPlaying = isPlaying }
            override fun onPositionChanged(positionMs: Long, durationMs: Long) {
                playbackPositionMs = positionMs
                playbackDurationMs = durationMs
            }
            override fun onTracksChanged(audio: List<MpvTrack>, subtitles: List<MpvTrack>) {
                audioTracks = trackOptions(audio, audioFallback)
                subtitleTracks = trackOptions(subtitles, subtitleFallback)
            }
            override fun onPlaybackEnded() {
                if (terminalDelivered || closeRequested) return
                terminalDelivered = true; playbackEnded = true; controlsVisible = true; optionsMenu = null
                finalProgressReporter?.invoke(); currentOnPlaybackEnded()
            }
            override fun onPlaybackFailed(positionMs: Long, reason: PlayerEngineFailureReason) {
                if (terminalDelivered || closeRequested) return
                terminalDelivered = true; visualState = PlayerVisualState.Ready; finalProgressReporter?.invoke()
                currentOnPlaybackError(PlayerEngineFailure(positionMs, fallbackEligible = false, reason = reason))
            }
        })
    }

    fun reportProgress(naturalEnd: Boolean = false) {
        val safeDuration = playbackDurationMs.coerceAtLeast(0L)
        val safePosition = playbackPositionMs.coerceAtLeast(0L).let { if (safeDuration > 0L) it.coerceAtMost(safeDuration) else it }
        currentOnProgress(
            (safePosition / 1_000L).coerceAtMost(Int.MAX_VALUE.toLong()).toInt(),
            if (safeDuration > 0L) ((safeDuration + 999L) / 1_000L).coerceAtMost(Int.MAX_VALUE.toLong()).toInt() else 0,
            naturalEnd || safeDuration > 0L && safePosition >= completionThreshold(safeDuration),
        )
    }
    finalProgressReporter = { reportProgress(playbackEnded) }
    fun noteInteraction(requestFocus: Boolean = false) {
        if (controlsLocked) return
        controlsVisible = true; interactionGeneration += 1L
        if (requestFocus && isTv) requestTransportFocusWhenVisible = true
    }
    fun seekTo(targetMs: Long) {
        val bounded = targetMs.coerceIn(0L, playbackDurationMs.takeIf { it > 0L } ?: Long.MAX_VALUE)
        autoSkippedMarkerEntries = autoSkipConsumedAfterUserSeek(presentation.markers, autoSkippedMarkerEntries, bounded)
        controller.seekTo(bounded); playbackPositionMs = bounded
    }
    fun replayFromStart() {
        if (!controller.replayFromStart()) return
        val replayPositionMs = absolutePlaybackPositionMs(
            0L,
            presentation.timelineStartPositionMs,
            presentation.mediaTimeline,
        )
        autoSkippedMarkerEntries = autoSkipConsumedAfterUserSeek(
            presentation.markers,
            autoSkippedMarkerEntries,
            replayPositionMs,
        )
        terminalDelivered = false
        playbackEnded = false
        playbackRequested = true
        playbackPositionMs = replayPositionMs
    }
    fun seekBy(deltaMs: Long) { if (!controlsLocked) { seekTo(playbackPositionMs + deltaMs); noteInteraction() } }
    fun togglePlayback() {
        if (controlsLocked) return
        if (playbackEnded) {
            replayFromStart()
        } else if (playbackRequested) {
            playbackRequested = false
            controller.pause()
        } else {
            playbackRequested = true
            controller.play()
        }
        noteInteraction()
    }
    fun requestClose() { if (!closeRequested) { reportProgress(); closeRequested = true; currentOnClose() } }
    fun requestNext() {
        if (!closeRequested && presentation.nextEpisode != null) { reportProgress(); closeRequested = true; currentOnNext() }
    }
    fun dismissChromeOrClose() {
        when {
            controlsLocked -> { controlsLocked = false; unlockVisible = false; noteInteraction(true) }
            controlsVisible || optionsMenu != null -> { optionsMenu = null; controlsVisible = false; if (isTv) playerRootFocus.requestFocus() }
            else -> requestClose()
        }
    }

    BackHandler(enabled = !closeRequested, onBack = ::dismissChromeOrClose)
    DisposableEffect(controller, lifecycleOwner) {
        resumeAfterPause = playbackRequested
        var finished = false
        val lifecycleObserver = LifecycleEventObserver { _, event ->
            when (event) {
                Lifecycle.Event.ON_RESUME -> if (resumeAfterPause && !finished) controller.play()
                Lifecycle.Event.ON_PAUSE -> { resumeAfterPause = playbackRequested; controller.pause() }
                Lifecycle.Event.ON_DESTROY -> if (!finished) { finished = true; reportProgress(); controller.release() }
                else -> Unit
            }
        }
        lifecycleOwner.lifecycle.addObserver(lifecycleObserver)
        if (!lifecycleOwner.lifecycle.currentState.isAtLeast(Lifecycle.State.RESUMED)) controller.pause()
        val reportingJob = coroutineScope.launch { while (isActive) { delay(PROGRESS_REPORT_INTERVAL_MS); reportProgress() } }
        onDispose {
            reportingJob.cancel(); lifecycleOwner.lifecycle.removeObserver(lifecycleObserver); finalProgressReporter = null
            if (!finished) { finished = true; reportProgress(); controller.release() }
        }
    }
    LaunchedEffect(remoteCommand?.operationId) {
        val command = remoteCommand ?: return@LaunchedEffect
        command.positionMilliseconds?.let(::seekTo)
        resumeAfterPause = coordinatedLifecycleResumeIntent(resumeAfterPause, command.command.name.lowercase())
        when (command.command) {
            io.rivune.api.PlaybackCommandType.PLAY -> { playbackRequested = true; controller.play() }
            io.rivune.api.PlaybackCommandType.PAUSE -> { playbackRequested = false; controller.pause() }
            io.rivune.api.PlaybackCommandType.SEEK -> Unit
            io.rivune.api.PlaybackCommandType.STOP -> currentOnClose()
            io.rivune.api.PlaybackCommandType.LOAD -> Unit
        }
        onCommandConsumed()
    }
    LaunchedEffect(playbackRoom?.version) {
        val room = playbackRoom ?: return@LaunchedEffect
        if (room.currentMemberIsHost) return@LaunchedEffect
        if (kotlin.math.abs(playbackPositionMs - room.positionMilliseconds) > 1_500) seekTo(room.positionMilliseconds)
        playbackRequested = room.state == "playing"
        resumeAfterPause = coordinatedLifecycleResumeIntent(resumeAfterPause, room.state)
        when (room.state) {
            "playing" -> controller.play()
            "paused" -> controller.pause()
            "ended" -> onClose()
        }
    }
    LaunchedEffect(playbackPositionMs, playbackDurationMs, playbackIsPlaying) {
        onPlaybackState(playbackPositionMs, playbackDurationMs, playbackIsPlaying)
    }
    LaunchedEffect(controller, sessionAspect) { controller.setAspect(sessionAspect) }
    LaunchedEffect(controlsVisible, playbackIsPlaying, interactionGeneration, playbackEnded, optionsMenu, controlsLocked) {
        if (controlsVisible && playbackIsPlaying && !playbackEnded && optionsMenu == null && !controlsLocked) {
            delay(if (isTv) CONTROLS_HIDE_TV_MS else CONTROLS_HIDE_PHONE_MS); controlsVisible = false
            if (isTv) playerRootFocus.requestFocus()
        }
    }
    LaunchedEffect(isTv, presentation.key) { if (isTv) playFocus.requestFocus() }
    LaunchedEffect(controlsVisible, requestTransportFocusWhenVisible, isTv) {
        if (controlsVisible && requestTransportFocusWhenVisible && isTv) {
            withFrameNanos { }; playFocus.requestFocus(); requestTransportFocusWhenVisible = false
        }
    }
    LaunchedEffect(optionsMenu, isTv) { if (optionsMenu != null && isTv) { withFrameNanos { }; firstMenuChoiceFocus.requestFocus() } }
    LaunchedEffect(controlsLocked, unlockVisible, isTv) {
        if (controlsLocked && unlockVisible && isTv) { withFrameNanos { }; unlockFocus.requestFocus() }
    }

    val activeMarkerEntry = activePlaybackMarkerEntry(presentation.markers, playbackPositionMs)
    val activeMarker = activeMarkerEntry?.marker
    val autoSkipEnabled = activeMarker?.let { shouldAutoSkipPlaybackMarker(it.type, autoSkipIntro, autoSkipRecap, autoSkipOutro) } == true
    val manualActiveMarker = activeMarker.takeUnless { autoSkipEnabled }
    LaunchedEffect(manualActiveMarker, isTv) {
        if (manualActiveMarker == null && markerWasVisible && isTv) playerRootFocus.requestFocus()
        markerWasVisible = manualActiveMarker != null
    }
    LaunchedEffect(activeMarkerEntry, autoSkipEnabled, autoSkippedMarkerEntries) {
        val entry = activeMarkerEntry ?: return@LaunchedEffect
        if (!autoSkipEnabled || entry.index in autoSkippedMarkerEntries) return@LaunchedEffect
        autoSkippedMarkerEntries = autoSkippedMarkerEntries + entry.index; controller.seekTo(entry.endMs); playbackPositionMs = entry.endMs
    }
    val motionPolicy = LocalRivuneMotionPolicy.current
    val markerBottomPadding = if (controlsVisible) {
        if (isTv) RivuneSpacing.display + RivuneSpacing.display else RivuneSpacing.display + RivuneSpacing.huge
    } else if (isTv) RivuneSpacing.huge else RivuneSpacing.xl

    Box(
        modifier = Modifier.fillMaxSize().background(Color.Black).focusGroup().focusRequester(playerRootFocus).focusable()
            .onPreviewKeyEvent { event ->
                if (event.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
                if (controlsLocked) {
                    return@onPreviewKeyEvent when (event.nativeKeyEvent.keyCode) {
                        AndroidKeyEvent.KEYCODE_DPAD_CENTER, AndroidKeyEvent.KEYCODE_ENTER, AndroidKeyEvent.KEYCODE_NUMPAD_ENTER -> {
                            if (unlockVisible) { controlsLocked = false; unlockVisible = false; noteInteraction(true) } else unlockVisible = true
                            true
                        }
                        AndroidKeyEvent.KEYCODE_DPAD_UP, AndroidKeyEvent.KEYCODE_DPAD_DOWN,
                        AndroidKeyEvent.KEYCODE_DPAD_LEFT, AndroidKeyEvent.KEYCODE_DPAD_RIGHT -> { unlockVisible = true; true }
                        else -> false
                    }
                }
                when (event.nativeKeyEvent.keyCode) {
                    AndroidKeyEvent.KEYCODE_MEDIA_PLAY_PAUSE -> { togglePlayback(); true }
                    AndroidKeyEvent.KEYCODE_MEDIA_PLAY -> { playbackRequested = true; controller.play(); noteInteraction(); true }
                    AndroidKeyEvent.KEYCODE_MEDIA_PAUSE -> { playbackRequested = false; controller.pause(); noteInteraction(); true }
                    AndroidKeyEvent.KEYCODE_MEDIA_REWIND -> { seekBy(-SEEK_INCREMENT_MS); true }
                    AndroidKeyEvent.KEYCODE_MEDIA_FAST_FORWARD -> { seekBy(SEEK_INCREMENT_MS); true }
                    AndroidKeyEvent.KEYCODE_DPAD_CENTER, AndroidKeyEvent.KEYCODE_ENTER, AndroidKeyEvent.KEYCODE_NUMPAD_ENTER,
                    AndroidKeyEvent.KEYCODE_DPAD_UP, AndroidKeyEvent.KEYCODE_DPAD_DOWN,
                    AndroidKeyEvent.KEYCODE_DPAD_LEFT, AndroidKeyEvent.KEYCODE_DPAD_RIGHT -> if (!controlsVisible) {
                        noteInteraction(true); true
                    } else { interactionGeneration += 1L; false }
                    else -> false
                }
            },
    ) {
        AndroidView(
            factory = { controller.createSurfaceView(it).apply {
                layoutParams = ViewGroup.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT)
                isFocusable = false; isFocusableInTouchMode = false
            } },
            onRelease = controller::releaseSurfaceView,
            modifier = Modifier.fillMaxSize(),
        )
        Box(modifier = Modifier.fillMaxSize().pointerInput(presentation.key, controlsLocked, controlsVisible) {
            detectTapGestures {
                when {
                    controlsLocked -> unlockVisible = true
                    controlsVisible -> { controlsVisible = false; optionsMenu = null }
                    else -> noteInteraction()
                }
            }
        })
        AnimatedVisibility(
            visible = controlsVisible && !controlsLocked,
            enter = fadeIn(motionPolicy.finiteAnimationSpec(RivuneMotion.fast)),
            exit = fadeOut(motionPolicy.finiteAnimationSpec(RivuneMotion.fast)),
            modifier = Modifier.fillMaxSize(),
        ) {
            PlayerChrome(
                title = presentation.title, isTv = isTv, isWide = isWide, enabled = !closeRequested,
                isPlaying = playbackIsPlaying, playbackEnded = playbackEnded, durationMs = playbackDurationMs, positionMs = playbackPositionMs,
                audioTracks = audioTracks, subtitleTracks = subtitleTracks, sessionAspect = sessionAspect,
                playbackSpeed = playbackSpeed, optionsMenu = optionsMenu, hasNext = presentation.nextEpisode != null,
                activeMarker = manualActiveMarker, closeFocus = closeFocus, rewindFocus = rewindFocus,
                playFocus = playFocus, forwardFocus = forwardFocus, seekFocus = seekFocus, audioFocus = audioFocus,
                subtitleFocus = subtitleFocus, aspectFocus = aspectFocus, speedFocus = speedFocus, lockFocus = lockFocus,
                markerFocus = markerFocus, replayFocus = replayFocus, nextFocus = nextFocus,
                firstMenuChoiceFocus = firstMenuChoiceFocus, onClose = ::requestClose, onTogglePlayback = ::togglePlayback,
                onSeekBack = { seekBy(-SEEK_INCREMENT_MS) }, onSeekForward = { seekBy(SEEK_INCREMENT_MS) },
                onSeek = { seekTo(it); noteInteraction() },
                onToggleMenu = { optionsMenu = toggledPlayerOptionsMenu(optionsMenu, it); noteInteraction() },
                onAudioSelected = { key ->
                    controller.selectAudio(key.groupIndex); audioTracks = audioTracks.map { it.copy(selected = it.key == key) }
                    optionsMenu = null; noteInteraction(); if (isTv) audioFocus.requestFocus()
                },
                onSubtitleSelected = { key ->
                    controller.selectSubtitle(key?.groupId)
                    subtitleTracks = subtitleTracks.map { it.copy(selected = key != null && it.key == key) }
                    optionsMenu = null; noteInteraction(); if (isTv) subtitleFocus.requestFocus()
                },
                onCycleAspect = {
                    sessionAspect = sessionAspect.nextVideoAspect(); optionsMenu = null; noteInteraction()
                    if (isTv) aspectFocus.requestFocus()
                },
                onSpeedSelected = {
                    playbackSpeed = it; controller.setSpeed(it); optionsMenu = null; noteInteraction()
                    if (isTv) speedFocus.requestFocus()
                },
                onLock = { controlsLocked = true; unlockVisible = false; controlsVisible = false; optionsMenu = null },
                onReplay = {
                    replayFromStart()
                    noteInteraction()
                },
                onNext = ::requestNext,
            )
        }
        if (visualState != PlayerVisualState.Ready) PlayerStatus(
            state = visualState, isTv = isTv,
            modifier = Modifier.align(Alignment.TopCenter).windowInsetsPadding(WindowInsets.safeDrawing)
                .padding(top = if (isTv) RivuneSpacing.display + RivuneSpacing.huge else RivuneSpacing.display + RivuneSpacing.md),
        )
        if (controlsLocked && unlockVisible) PlayerIconAction(
            icon = Icons.Rounded.LockOpen, label = stringResource(R.string.player_unlock_controls), isTv = isTv,
            enabled = !closeRequested, prominent = true,
            onClick = { controlsLocked = false; unlockVisible = false; noteInteraction(true) },
            modifier = Modifier.align(Alignment.Center).focusRequester(unlockFocus),
        )
        if (!controlsLocked) MarkerSkipAction(
            marker = manualActiveMarker, isTv = isTv, focusRequester = markerFocus,
            modifier = Modifier.align(Alignment.BottomEnd).windowInsetsPadding(WindowInsets.safeDrawing)
                .padding(
                    end = if (isTv) RivuneSpacing.display else if (isWide) RivuneSpacing.xxl else RivuneSpacing.md,
                    bottom = markerBottomPadding,
                ).focusProperties { left = playerRootFocus; up = playerRootFocus },
            onClick = { marker ->
                playbackMarkerSeekTargetMs(marker)?.let(::seekTo); noteInteraction()
                if (isTv) playerRootFocus.requestFocus()
            },
        )
    }
}

@Composable
private fun PlayerImmersivePresentationEffect(
    activity: Activity?,
    lifecycleOwner: androidx.lifecycle.LifecycleOwner,
    enabled: Boolean,
) {
    DisposableEffect(activity, lifecycleOwner, enabled) {
        if (activity == null || !enabled) {
            onDispose { }
        } else {
            val window = activity.window
            val decorView = window.decorView
            val insetsController = WindowCompat.getInsetsController(window, decorView)
            val initialOrientation = activity.requestedOrientation
            val initialInsets = ViewCompat.getRootWindowInsets(decorView)
            val statusBarsWereVisible = initialInsets?.isVisible(WindowInsetsCompat.Type.statusBars()) ?: true
            val navigationBarsWereVisible = initialInsets?.isVisible(WindowInsetsCompat.Type.navigationBars()) ?: true
            val initialSystemBarsBehavior = insetsController.systemBarsBehavior
            val lightStatusBarsWereEnabled = insetsController.isAppearanceLightStatusBars
            val lightNavigationBarsWereEnabled = insetsController.isAppearanceLightNavigationBars
            fun enter() {
                activity.requestedOrientation = ActivityInfo.SCREEN_ORIENTATION_SENSOR_LANDSCAPE
                insetsController.isAppearanceLightStatusBars = false
                insetsController.isAppearanceLightNavigationBars = false
                insetsController.systemBarsBehavior = WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
                insetsController.hide(WindowInsetsCompat.Type.systemBars())
            }
            val observer = LifecycleEventObserver { _, event -> if (event == Lifecycle.Event.ON_RESUME) enter() }
            lifecycleOwner.lifecycle.addObserver(observer)
            enter()
            onDispose {
                lifecycleOwner.lifecycle.removeObserver(observer)
                activity.requestedOrientation = initialOrientation
                insetsController.systemBarsBehavior = initialSystemBarsBehavior
                insetsController.isAppearanceLightStatusBars = lightStatusBarsWereEnabled
                insetsController.isAppearanceLightNavigationBars = lightNavigationBarsWereEnabled
                if (statusBarsWereVisible) insetsController.show(WindowInsetsCompat.Type.statusBars())
                else insetsController.hide(WindowInsetsCompat.Type.statusBars())
                if (navigationBarsWereVisible) insetsController.show(WindowInsetsCompat.Type.navigationBars())
                else insetsController.hide(WindowInsetsCompat.Type.navigationBars())
            }
        }
    }
}

private fun PlaybackException.isMedia3FallbackEligible(): Boolean = media3FallbackEligible(errorCode)

internal fun media3FallbackEligible(errorCode: Int): Boolean = errorCode in setOf(
    PlaybackException.ERROR_CODE_PARSING_CONTAINER_UNSUPPORTED,
    PlaybackException.ERROR_CODE_PARSING_MANIFEST_UNSUPPORTED,
    PlaybackException.ERROR_CODE_DECODING_FORMAT_EXCEEDS_CAPABILITIES,
    PlaybackException.ERROR_CODE_DECODING_FORMAT_UNSUPPORTED,
)

@Composable
private fun PlayerStatus(
    state: PlayerVisualState,
    isTv: Boolean,
    modifier: Modifier = Modifier,
) {
    val motionPolicy = LocalRivuneMotionPolicy.current
    val status = stringResource(
        if (state == PlayerVisualState.Preparing) R.string.player_preparing else R.string.player_buffering,
    )
    RivuneFunctionalSurface(
        modifier = modifier.semantics(mergeDescendants = true) { liveRegion = LiveRegionMode.Polite },
        shape = RivuneShapes.pill,
        contentPadding = PaddingValues(
            horizontal = if (isTv) RivuneSpacing.lg else RivuneSpacing.md,
            vertical = RivuneSpacing.xs,
        ),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            if (motionPolicy.ambientAnimations) {
                CircularProgressIndicator(
                    modifier = Modifier.size(RivuneDimensions.iconSmall).clearAndSetSemantics { },
                    color = MaterialTheme.colorScheme.primary,
                    strokeWidth = RivuneDimensions.hairline,
                )
                Spacer(Modifier.width(RivuneSpacing.xs))
            }
            Text(
                text = status,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                style = if (isTv) MaterialTheme.typography.labelLarge else MaterialTheme.typography.labelMedium,
            )
        }
    }
}

@Composable
private fun PlayerChrome(
    title: String,
    isTv: Boolean,
    isWide: Boolean,
    enabled: Boolean,
    isPlaying: Boolean,
    playbackEnded: Boolean,
    durationMs: Long,
    positionMs: Long,
    audioTracks: List<PlayerTrackOption>,
    subtitleTracks: List<PlayerTrackOption>,
    sessionAspect: VideoAspectPreference,
    playbackSpeed: Float,
    optionsMenu: PlayerOptionsMenu?,
    hasNext: Boolean,
    activeMarker: PlaybackMarker?,
    closeFocus: FocusRequester,
    rewindFocus: FocusRequester,
    playFocus: FocusRequester,
    forwardFocus: FocusRequester,
    seekFocus: FocusRequester,
    audioFocus: FocusRequester,
    subtitleFocus: FocusRequester,
    aspectFocus: FocusRequester,
    speedFocus: FocusRequester,
    lockFocus: FocusRequester,
    markerFocus: FocusRequester,
    replayFocus: FocusRequester,
    nextFocus: FocusRequester,
    firstMenuChoiceFocus: FocusRequester,
    onClose: () -> Unit,
    onTogglePlayback: () -> Unit,
    onSeekBack: () -> Unit,
    onSeekForward: () -> Unit,
    onSeek: (Long) -> Unit,
    onToggleMenu: (PlayerOptionsMenu) -> Unit,
    onAudioSelected: (PlayerTrackKey) -> Unit,
    onSubtitleSelected: (PlayerTrackKey?) -> Unit,
    onCycleAspect: () -> Unit,
    onSpeedSelected: (Float) -> Unit,
    onLock: () -> Unit,
    onReplay: () -> Unit,
    onNext: () -> Unit,
) {
    val menuTitle: String?
    val menuChoices: List<PlayerMenuChoice>
    when (optionsMenu) {
        PlayerOptionsMenu.Audio -> {
            menuTitle = stringResource(R.string.player_audio)
            menuChoices = audioTracks.map { track ->
                PlayerMenuChoice(track.label, track.selected) { onAudioSelected(track.key) }
            }
        }
        PlayerOptionsMenu.Subtitles -> {
            menuTitle = stringResource(R.string.player_subtitles)
            menuChoices = listOf(
                PlayerMenuChoice(
                    label = stringResource(R.string.player_subtitles_off),
                    selected = subtitleTracks.none(PlayerTrackOption::selected),
                    onClick = { onSubtitleSelected(null) },
                ),
            ) + subtitleTracks.map { track ->
                PlayerMenuChoice(track.label, track.selected) { onSubtitleSelected(track.key) }
            }
        }
        PlayerOptionsMenu.Speed -> {
            menuTitle = stringResource(R.string.player_speed)
            menuChoices = PLAYER_PLAYBACK_SPEEDS.map { speed ->
                PlayerMenuChoice(formatPlaybackSpeed(speed), speed == playbackSpeed) { onSpeedSelected(speed) }
            }
        }
        null -> {
            menuTitle = null
            menuChoices = emptyList()
        }
    }

    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(RivuneScrim.copy(alpha = 0.38f)),
    ) {
        RivunePlayerTopBar(
            title = title,
            isTv = isTv,
            onClose = onClose,
            hasNext = false,
            enabled = enabled,
            closeModifier = Modifier
                .focusRequester(closeFocus)
                .focusProperties { down = if (playbackEnded) replayFocus else playFocus },
        )

        if (playbackEnded) {
            EndedActions(
                isTv = isTv,
                hasNext = hasNext,
                replayFocus = replayFocus,
                nextFocus = nextFocus,
                closeFocus = closeFocus,
                enabled = enabled,
                onReplay = onReplay,
                onNext = onNext,
                modifier = Modifier.align(Alignment.Center).windowInsetsPadding(WindowInsets.safeDrawing),
            )
        } else {
            TransportControls(
                isTv = isTv,
                isPlaying = isPlaying,
                enabled = enabled,
                rewindFocus = rewindFocus,
                playFocus = playFocus,
                forwardFocus = forwardFocus,
                closeFocus = closeFocus,
                seekFocus = seekFocus,
                markerFocus = markerFocus,
                markerAvailable = activeMarker != null,
                onSeekBack = onSeekBack,
                onTogglePlayback = onTogglePlayback,
                onSeekForward = onSeekForward,
                modifier = Modifier.align(Alignment.Center),
            )
            PlaybackTimeline(
                isTv = isTv,
                isWide = isWide,
                durationMs = durationMs,
                positionMs = positionMs,
                audioAvailable = audioTracks.isNotEmpty(),
                sessionAspect = sessionAspect,
                optionsMenu = optionsMenu,
                seekFocus = seekFocus,
                playFocus = playFocus,
                audioFocus = audioFocus,
                subtitleFocus = subtitleFocus,
                aspectFocus = aspectFocus,
                speedFocus = speedFocus,
                lockFocus = lockFocus,
                markerFocus = markerFocus,
                markerAvailable = activeMarker != null,
                onSeek = onSeek,
                onToggleMenu = onToggleMenu,
                onCycleAspect = onCycleAspect,
                onLock = onLock,
                modifier = Modifier.align(Alignment.BottomCenter).windowInsetsPadding(WindowInsets.safeDrawing),
            )
        }

        if (menuTitle != null) {
            PlayerOptionsMenuSheet(
                title = menuTitle,
                choices = menuChoices,
                isTv = isTv,
                firstFocus = firstMenuChoiceFocus,
                returnFocus = when (optionsMenu) {
                    PlayerOptionsMenu.Audio -> audioFocus
                    PlayerOptionsMenu.Subtitles -> subtitleFocus
                    PlayerOptionsMenu.Speed -> speedFocus
                    null -> seekFocus
                },
                modifier = Modifier
                    .align(Alignment.BottomEnd)
                    .windowInsetsPadding(WindowInsets.safeDrawing)
                    .padding(
                        end = if (isTv) RivuneSpacing.display else RivuneSpacing.md,
                        bottom = if (isTv) RivuneSpacing.display + RivuneSpacing.display else RivuneSpacing.display + RivuneSpacing.huge,
                    ),
            )
        }
    }
}

@Composable
private fun TransportControls(
    isTv: Boolean,
    isPlaying: Boolean,
    enabled: Boolean,
    rewindFocus: FocusRequester,
    playFocus: FocusRequester,
    forwardFocus: FocusRequester,
    closeFocus: FocusRequester,
    seekFocus: FocusRequester,
    markerFocus: FocusRequester,
    markerAvailable: Boolean,
    onSeekBack: () -> Unit,
    onTogglePlayback: () -> Unit,
    onSeekForward: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier,
        horizontalArrangement = Arrangement.spacedBy(if (isTv) RivuneSpacing.xl else RivuneSpacing.md),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        PlayerIconAction(
            icon = Icons.Rounded.Replay10,
            label = stringResource(R.string.player_rewind_10),
            isTv = isTv,
            enabled = enabled,
            onClick = onSeekBack,
            modifier = Modifier
                .focusRequester(rewindFocus)
                .focusProperties {
                    right = playFocus
                    up = closeFocus
                    down = seekFocus
                },
        )
        PlayerIconAction(
            icon = if (isPlaying) Icons.Rounded.Pause else Icons.Rounded.PlayArrow,
            label = stringResource(if (isPlaying) R.string.player_pause else R.string.player_play),
            isTv = isTv,
            prominent = true,
            enabled = enabled,
            onClick = onTogglePlayback,
            modifier = Modifier
                .focusRequester(playFocus)
                .focusProperties {
                    left = rewindFocus
                    right = forwardFocus
                    up = closeFocus
                    down = seekFocus
                },
        )
        PlayerIconAction(
            icon = Icons.Rounded.Forward10,
            label = stringResource(R.string.player_forward_10),
            isTv = isTv,
            enabled = enabled,
            onClick = onSeekForward,
            modifier = Modifier
                .focusRequester(forwardFocus)
                .focusProperties {
                    left = playFocus
                    up = closeFocus
                    down = if (markerAvailable) markerFocus else seekFocus
                },
        )
    }
}

@Composable
private fun PlayerIconAction(
    icon: androidx.compose.ui.graphics.vector.ImageVector,
    label: String,
    isTv: Boolean,
    enabled: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    prominent: Boolean = false,
) {
    val target = if (isTv) {
        if (prominent) RivuneSpacing.display else RivuneDimensions.touchTargetTv
    } else {
        if (prominent) RivuneDimensions.buttonHeight else RivuneDimensions.touchTarget
    }
    RivuneFocusSurface(
        onClick = onClick,
        enabled = enabled,
        isTv = isTv,
        idleColor = if (prominent) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.surface.copy(alpha = 0.74f),
        focusedColor = if (prominent) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.surfaceContainerHighest,
        pressedColor = MaterialTheme.colorScheme.primaryContainer,
        shape = RivuneShapes.pill,
        modifier = modifier.size(target),
    ) {
        Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
            Icon(
                imageVector = icon,
                contentDescription = label,
                modifier = Modifier.size(if (prominent) RivuneSpacing.xxl else RivuneDimensions.iconMedium),
                tint = if (prominent) MaterialTheme.colorScheme.onPrimary else MaterialTheme.colorScheme.onSurface,
            )
        }
    }
}

@Composable
private fun PlaybackTimeline(
    isTv: Boolean,
    isWide: Boolean,
    durationMs: Long,
    positionMs: Long,
    audioAvailable: Boolean,
    sessionAspect: VideoAspectPreference,
    optionsMenu: PlayerOptionsMenu?,
    seekFocus: FocusRequester,
    playFocus: FocusRequester,
    audioFocus: FocusRequester,
    subtitleFocus: FocusRequester,
    aspectFocus: FocusRequester,
    speedFocus: FocusRequester,
    lockFocus: FocusRequester,
    markerFocus: FocusRequester,
    markerAvailable: Boolean,
    onSeek: (Long) -> Unit,
    onToggleMenu: (PlayerOptionsMenu) -> Unit,
    onCycleAspect: () -> Unit,
    onLock: () -> Unit,
    modifier: Modifier = Modifier,
) {
    var scrubPositionMs by remember { mutableStateOf<Long?>(null) }
    val boundedDuration = durationMs.coerceAtLeast(0L)
    val displayedPosition = scrubPositionMs ?: positionMs.coerceIn(0L, boundedDuration.takeIf { it > 0L } ?: Long.MAX_VALUE)
    val maxWidth = when {
        isTv -> RivuneDimensions.contentMaxWide
        isWide -> RivuneDimensions.contentMaxTablet
        else -> RivuneDimensions.contentMax
    }
    val sliderColors = SliderDefaults.colors(
        thumbColor = MaterialTheme.colorScheme.primary,
        activeTrackColor = MaterialTheme.colorScheme.primary,
        inactiveTrackColor = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.28f),
    )
    val sliderInteraction = remember { MutableInteractionSource() }
    val aspectIcon = when (sessionAspect) {
        VideoAspectPreference.FIT -> Icons.Rounded.FitScreen
        VideoAspectPreference.FILL -> Icons.Rounded.WidthWide
        VideoAspectPreference.ZOOM -> Icons.Rounded.ZoomInMap
    }
    val aspectDescription = stringResource(videoAspectActionResource(sessionAspect))
    Column(
        modifier = modifier
            .fillMaxWidth()
            .padding(
                start = if (isTv) RivuneSpacing.display else if (isWide) RivuneSpacing.xxl else RivuneSpacing.md,
                top = if (isTv) RivuneSpacing.huge else RivuneSpacing.xxl,
                end = if (isTv) RivuneSpacing.display else if (isWide) RivuneSpacing.xxl else RivuneSpacing.md,
                bottom = if (isTv) RivuneSpacing.xl else RivuneSpacing.sm,
            ),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Column(modifier = Modifier.fillMaxWidth().widthIn(max = maxWidth)) {
            Slider(
                value = displayedPosition.toFloat(),
                onValueChange = { scrubPositionMs = it.roundToLong() },
                onValueChangeFinished = {
                    scrubPositionMs?.let(onSeek)
                    scrubPositionMs = null
                },
                valueRange = 0f..boundedDuration.coerceAtLeast(1L).toFloat(),
                enabled = boundedDuration > 0L,
                interactionSource = sliderInteraction,
                modifier = Modifier
                    .fillMaxWidth()
                    .heightIn(min = if (isTv) RivuneDimensions.touchTargetTv else RivuneDimensions.touchTarget)
                    .focusRequester(seekFocus)
                    .focusProperties {
                        up = playFocus
                        down = if (audioAvailable) audioFocus else subtitleFocus
                    }
                    .semantics {
                        stateDescription = "${formatPlaybackTime(displayedPosition)} / ${formatPlaybackTime(boundedDuration)}"
                    },
                colors = sliderColors,
                thumb = {
                    SliderDefaults.Thumb(
                        interactionSource = sliderInteraction,
                        colors = sliderColors,
                        enabled = boundedDuration > 0L,
                        thumbSize = DpSize(RivuneSpacing.sm, RivuneSpacing.sm),
                    )
                },
                track = { sliderState ->
                    SliderDefaults.Track(
                        sliderState = sliderState,
                        modifier = Modifier.height(RivuneSpacing.xxs),
                        colors = sliderColors,
                        enabled = boundedDuration > 0L,
                    )
                },
            )
            Row(
                modifier = Modifier.fillMaxWidth().padding(horizontal = RivuneSpacing.sm / 2),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(
                    text = "${formatPlaybackTime(displayedPosition)} / ${formatPlaybackTime(boundedDuration)}",
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    style = if (isTv) MaterialTheme.typography.labelLarge else MaterialTheme.typography.labelMedium,
                )
                Row(
                    horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.xxs),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    PlayerOptionAction(
                        icon = Icons.Rounded.Audiotrack,
                        label = stringResource(R.string.player_audio),
                        isTv = isTv,
                        selected = optionsMenu == PlayerOptionsMenu.Audio,
                        focus = audioFocus,
                        upFocus = seekFocus,
                        leftFocus = seekFocus,
                        rightFocus = subtitleFocus,
                        enabled = audioAvailable,
                    ) { onToggleMenu(PlayerOptionsMenu.Audio) }
                    PlayerOptionAction(
                        icon = Icons.Rounded.ClosedCaption,
                        label = stringResource(R.string.player_subtitles),
                        isTv = isTv,
                        selected = optionsMenu == PlayerOptionsMenu.Subtitles,
                        focus = subtitleFocus,
                        upFocus = seekFocus,
                        leftFocus = if (audioAvailable) audioFocus else seekFocus,
                        rightFocus = aspectFocus,
                        enabled = true,
                    ) { onToggleMenu(PlayerOptionsMenu.Subtitles) }
                    PlayerOptionAction(
                        icon = aspectIcon,
                        label = aspectDescription,
                        isTv = isTv,
                        focus = aspectFocus,
                        upFocus = seekFocus,
                        leftFocus = subtitleFocus,
                        rightFocus = speedFocus,
                        enabled = true,
                        onClick = onCycleAspect,
                    )
                    PlayerOptionAction(
                        icon = Icons.Rounded.Speed,
                        label = stringResource(R.string.player_speed),
                        isTv = isTv,
                        selected = optionsMenu == PlayerOptionsMenu.Speed,
                        focus = speedFocus,
                        upFocus = seekFocus,
                        leftFocus = aspectFocus,
                        rightFocus = lockFocus,
                        enabled = true,
                    ) { onToggleMenu(PlayerOptionsMenu.Speed) }
                    PlayerOptionAction(
                        icon = Icons.Rounded.Lock,
                        label = stringResource(R.string.player_lock_controls),
                        isTv = isTv,
                        focus = lockFocus,
                        upFocus = seekFocus,
                        leftFocus = speedFocus,
                        rightFocus = if (markerAvailable) markerFocus else seekFocus,
                        enabled = true,
                        onClick = onLock,
                    )
                }
            }
        }
    }
}

@Composable
private fun PlayerOptionAction(
    icon: androidx.compose.ui.graphics.vector.ImageVector,
    label: String,
    isTv: Boolean,
    selected: Boolean? = null,
    focus: FocusRequester,
    upFocus: FocusRequester,
    leftFocus: FocusRequester,
    rightFocus: FocusRequester,
    enabled: Boolean,
    onClick: () -> Unit,
) {
    RivuneFocusSurface(
        onClick = onClick,
        selected = selected,
        enabled = enabled,
        isTv = isTv,
        idleColor = Color.Transparent,
        selectedColor = MaterialTheme.colorScheme.primaryContainer,
        shape = RivuneShapes.pill,
        modifier = Modifier
            .size(if (isTv) RivuneDimensions.touchTargetTv else RivuneDimensions.touchTarget)
            .focusRequester(focus)
            .focusProperties {
                up = upFocus
                left = leftFocus
                right = rightFocus
            },
    ) {
        Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
            Icon(icon, contentDescription = label, modifier = Modifier.size(RivuneDimensions.iconMedium))
        }
    }
}

@Composable
private fun PlayerOptionsMenuSheet(
    title: String,
    choices: List<PlayerMenuChoice>,
    isTv: Boolean,
    firstFocus: FocusRequester,
    returnFocus: FocusRequester,
    modifier: Modifier = Modifier,
) {
    val choiceFocus = remember(firstFocus, choices.size) {
        listOf(firstFocus) + List((choices.size - 1).coerceAtLeast(0)) { FocusRequester() }
    }
    RivuneFunctionalSurface(
        modifier = modifier.width(
            if (isTv) RivuneDimensions.landscapeCardWidthTv else RivuneDimensions.landscapeCardWidth + RivuneSpacing.display,
        ),
        shape = RivuneShapes.medium,
        contentPadding = PaddingValues(vertical = RivuneSpacing.xs),
    ) {
        Column(
            modifier = Modifier
                .heightIn(max = if (isTv) RivuneDimensions.contentMax else RivuneDimensions.landscapeCardWidthTv)
                .verticalScroll(rememberScrollState()),
        ) {
            Text(
                text = title,
                modifier = Modifier.padding(horizontal = RivuneSpacing.md, vertical = RivuneSpacing.xs).semantics { heading() },
                style = if (isTv) MaterialTheme.typography.titleLarge else MaterialTheme.typography.titleMedium,
            )
            choices.forEachIndexed { index, choice ->
                PlayerMenuChoiceRow(
                    choice = choice,
                    isTv = isTv,
                    modifier = Modifier
                        .focusRequester(choiceFocus[index])
                        .focusProperties {
                            left = returnFocus
                            up = choiceFocus.getOrElse(index - 1) { returnFocus }
                            down = choiceFocus.getOrElse(index + 1) { returnFocus }
                        },
                )
            }
        }
    }
}

@Composable
private fun PlayerMenuChoiceRow(choice: PlayerMenuChoice, isTv: Boolean, modifier: Modifier = Modifier) {
    RivuneFocusSurface(
        onClick = choice.onClick,
        selected = choice.selected,
        isTv = isTv,
        idleColor = Color.Transparent,
        selectedColor = Color.Transparent,
        focusedColor = MaterialTheme.colorScheme.surfaceContainerHighest,
        pressedColor = MaterialTheme.colorScheme.surfaceContainerHighest,
        showSelectionBorder = false,
        shape = RivuneShapes.small,
        modifier = modifier.fillMaxWidth(),
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .heightIn(min = if (isTv) RivuneDimensions.touchTargetTv else RivuneDimensions.touchTarget)
                .padding(horizontal = RivuneSpacing.md, vertical = RivuneSpacing.xs),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = choice.label,
                color = MaterialTheme.colorScheme.onSurface,
                style = if (isTv) MaterialTheme.typography.bodyLarge else MaterialTheme.typography.bodyMedium,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f),
            )
            Spacer(Modifier.width(RivuneSpacing.sm))
            if (choice.selected) {
                Icon(
                    imageVector = Icons.Rounded.Check,
                    contentDescription = null,
                    tint = MaterialTheme.colorScheme.primary,
                    modifier = Modifier.size(RivuneDimensions.iconMedium),
                )
            } else {
                Spacer(Modifier.size(RivuneDimensions.iconMedium))
            }
        }
    }
}



@Composable
private fun EndedActions(
    isTv: Boolean,
    hasNext: Boolean,
    replayFocus: FocusRequester,
    nextFocus: FocusRequester,
    closeFocus: FocusRequester,
    enabled: Boolean,
    onReplay: () -> Unit,
    onNext: () -> Unit,
    modifier: Modifier = Modifier,
) {
    RivuneFunctionalSurface(
        modifier = modifier.widthIn(max = RivuneDimensions.contentMaxTablet),
        shape = RivuneShapes.large,
        contentPadding = PaddingValues(if (isTv) RivuneSpacing.xl else RivuneSpacing.lg),
    ) {
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            Text(
                text = stringResource(R.string.player_ended),
                modifier = Modifier.semantics { heading() },
                style = if (isTv) MaterialTheme.typography.headlineMedium else MaterialTheme.typography.titleLarge,
            )
            Spacer(Modifier.height(if (isTv) RivuneSpacing.lg else RivuneSpacing.md))
            Row(
                horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                RivuneSecondaryButton(
                    label = stringResource(R.string.player_replay),
                    onClick = onReplay,
                    enabled = enabled,
                    isTv = isTv,
                    icon = Icons.Rounded.Replay,
                    modifier = Modifier
                        .focusRequester(replayFocus)
                        .focusProperties {
                            right = if (hasNext) nextFocus else replayFocus
                            up = closeFocus
                        },
                )
                if (hasNext) {
                    RivunePrimaryButton(
                        label = stringResource(R.string.player_next_episode),
                        onClick = onNext,
                        enabled = enabled,
                        isTv = isTv,
                        icon = Icons.Rounded.SkipNext,
                        modifier = Modifier
                            .focusRequester(nextFocus)
                            .focusProperties {
                                left = replayFocus
                                up = closeFocus
                            },
                    )
                }
            }
        }
    }
}

@Composable
private fun MarkerSkipAction(
    marker: PlaybackMarker?,
    isTv: Boolean,
    focusRequester: FocusRequester,
    onClick: (PlaybackMarker) -> Unit,
    modifier: Modifier = Modifier,
) {
    val motionPolicy = LocalRivuneMotionPolicy.current
    var announcedMarkerKey by remember { mutableStateOf<String?>(null) }
    var announcedMarkerKeys by remember { mutableStateOf(emptySet<String>()) }
    val markerKey = marker?.let { "${it.type}:${it.startSeconds}:${it.endSeconds}" }
    LaunchedEffect(markerKey) {
        if (markerKey != null && markerKey !in announcedMarkerKeys) {
            announcedMarkerKeys = announcedMarkerKeys + markerKey
            announcedMarkerKey = markerKey
            delay(1_000L)
            if (announcedMarkerKey == markerKey) announcedMarkerKey = null
        } else if (markerKey == null) {
            announcedMarkerKey = null
        }
    }
    AnimatedVisibility(
        visible = marker != null,
        modifier = modifier,
        enter = fadeIn(motionPolicy.finiteAnimationSpec(RivuneMotion.fast)),
        exit = fadeOut(motionPolicy.finiteAnimationSpec(RivuneMotion.fast)),
    ) {
        marker?.let { visibleMarker ->
            val label = markerLabel(visibleMarker.type)
            RivuneTextButton(
                label = label,
                onClick = { onClick(visibleMarker) },
                isTv = isTv,
                modifier = Modifier
                    .focusRequester(focusRequester)
                    .semantics {
                        if (announcedMarkerKey == markerKey) liveRegion = LiveRegionMode.Polite
                    },
            )
        }
    }
}

@Composable
private fun markerLabel(type: PlaybackMarkerType): String = stringResource(
    when (type) {
        PlaybackMarkerType.INTRO -> R.string.player_skip_intro
        PlaybackMarkerType.RECAP -> R.string.player_skip_recap
        PlaybackMarkerType.OUTRO -> R.string.player_skip_outro
    },
)

@OptIn(UnstableApi::class)
internal fun FrameRateMatchingPreference.media3Strategy(sdkInt: Int = Build.VERSION.SDK_INT): Int = when (this) {
    FrameRateMatchingPreference.DISABLED -> C.VIDEO_CHANGE_FRAME_RATE_STRATEGY_OFF
    FrameRateMatchingPreference.ENABLED -> if (sdkInt >= Build.VERSION_CODES.S) {
        C.VIDEO_CHANGE_FRAME_RATE_STRATEGY_OFF
    } else {
        C.VIDEO_CHANGE_FRAME_RATE_STRATEGY_ONLY_IF_SEAMLESS
    }
    FrameRateMatchingPreference.SYSTEM -> C.VIDEO_CHANGE_FRAME_RATE_STRATEGY_ONLY_IF_SEAMLESS
}

@OptIn(UnstableApi::class)
internal fun VideoAspectPreference.resizeMode(): Int = when (this) {
    VideoAspectPreference.FIT -> AspectRatioFrameLayout.RESIZE_MODE_FIT
    VideoAspectPreference.FILL -> AspectRatioFrameLayout.RESIZE_MODE_FILL
    VideoAspectPreference.ZOOM -> AspectRatioFrameLayout.RESIZE_MODE_ZOOM
}


internal fun VideoAspectPreference.nextVideoAspect(): VideoAspectPreference = when (this) {
    VideoAspectPreference.FIT -> VideoAspectPreference.FILL
    VideoAspectPreference.FILL -> VideoAspectPreference.ZOOM
    VideoAspectPreference.ZOOM -> VideoAspectPreference.FIT
}

@StringRes
internal fun videoAspectActionResource(aspect: VideoAspectPreference): Int = when (aspect) {
    VideoAspectPreference.FIT -> R.string.player_aspect_fit_to_fill
    VideoAspectPreference.FILL -> R.string.player_aspect_fill_to_zoom
    VideoAspectPreference.ZOOM -> R.string.player_aspect_zoom_to_fit
}
@OptIn(UnstableApi::class)
private class ForcedFrameRateController(private val player: ExoPlayer) : Player.Listener, AnalyticsListener, SurfaceHolder.Callback {
    private var surfaceView: SurfaceView? = null
    private var surface: Surface? = null
    private var contentFrameRate = 0f

    init {
        player.addAnalyticsListener(this)
        player.addListener(this)
    }

    fun attach(view: View?) {
        val next = view as? SurfaceView
        if (surfaceView === next) return
        clearSurfaceFrameRate()
        surfaceView?.holder?.removeCallback(this)
        surfaceView = next
        surface = null
        next?.holder?.addCallback(this)
        next?.holder?.surface?.takeIf(Surface::isValid)?.let {
            surface = it
            applyFrameRate()
        }
    }

    override fun surfaceCreated(holder: SurfaceHolder) {
        surface = holder.surface
        applyFrameRate()
    }

    override fun surfaceChanged(holder: SurfaceHolder, format: Int, width: Int, height: Int) {
        surface = holder.surface
        applyFrameRate()
    }

    override fun surfaceDestroyed(holder: SurfaceHolder) {
        if (surface === holder.surface) surface = null
    }

    override fun onVideoInputFormatChanged(
        eventTime: AnalyticsListener.EventTime,
        format: Format,
        decoderReuseEvaluation: DecoderReuseEvaluation?,
    ) {
        contentFrameRate = format.frameRate.takeIf { it.isFinite() && it > 0f } ?: 0f
        applyFrameRate()
    }

    override fun onPlaybackParametersChanged(playbackParameters: androidx.media3.common.PlaybackParameters) {
        applyFrameRate()
    }

    fun release() {
        clearSurfaceFrameRate()
        surfaceView?.holder?.removeCallback(this)
        surfaceView = null
        surface = null
        player.removeListener(this)
        player.removeAnalyticsListener(this)
    }

    private fun applyFrameRate() {
        val adjustedRate = contentFrameRate * player.playbackParameters.speed
        updateForcedFrameRate(surface, adjustedRate.takeIf { it.isFinite() && it > 0f } ?: 0f)
    }

    private fun clearSurfaceFrameRate() {
        updateForcedFrameRate(surface, 0f)
    }
}

private fun updateForcedFrameRate(surface: Surface?, frameRate: Float) {
    if (Build.VERSION.SDK_INT < Build.VERSION_CODES.S || surface == null || !surface.isValid) return
    runCatching {
        surface.setFrameRate(
            frameRate.coerceAtLeast(0f),
            if (frameRate > 0f) Surface.FRAME_RATE_COMPATIBILITY_FIXED_SOURCE else Surface.FRAME_RATE_COMPATIBILITY_DEFAULT,
            Surface.CHANGE_FRAME_RATE_ALWAYS,
        )
    }
}

@Composable
internal fun RivunePlayerTopBar(
    title: String,
    isTv: Boolean,
    onClose: () -> Unit,
    onNext: () -> Unit = {},
    hasNext: Boolean = false,
    modifier: Modifier = Modifier,
    closeModifier: Modifier = Modifier,
    enabled: Boolean = true,
) {
    val closeLabel = stringResource(R.string.player_close)

    Row(
        modifier = modifier
            .fillMaxWidth()
            .background(
                Brush.verticalGradient(
                    colors = listOf(
                        RivuneScrim.copy(alpha = 0.94f),
                        RivuneScrim.copy(alpha = 0.58f),
                        Color.Transparent,
                    ),
                ),
            )
            .windowInsetsPadding(WindowInsets.safeDrawing)
            .padding(
                start = if (isTv) RivuneSpacing.display else RivuneSpacing.xs,
                top = if (isTv) RivuneSpacing.md else RivuneSpacing.xs,
                end = if (isTv) RivuneSpacing.display else RivuneSpacing.xs,
                bottom = if (isTv) RivuneSpacing.xxl else RivuneSpacing.lg,
            ),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        RivuneFocusSurface(
            onClick = onClose,
            enabled = enabled,
            isTv = isTv,
            idleColor = Color.Transparent,
            shape = RivuneShapes.pill,
            modifier = closeModifier.size(if (isTv) RivuneDimensions.touchTargetTv else RivuneDimensions.touchTarget),
        ) {
            Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                Icon(
                    imageVector = Icons.AutoMirrored.Rounded.ArrowBack,
                    contentDescription = closeLabel,
                    modifier = Modifier.size(RivuneDimensions.iconMedium),
                    tint = MaterialTheme.colorScheme.onSurface,
                )
            }
        }
        Spacer(Modifier.width(if (isTv) RivuneSpacing.md else RivuneSpacing.sm))
        Text(
            text = title,
            modifier = Modifier.weight(1f).semantics { heading() },
            color = MaterialTheme.colorScheme.onSurface,
            style = if (isTv) MaterialTheme.typography.titleLarge else MaterialTheme.typography.titleMedium,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        if (hasNext) {
            Spacer(Modifier.width(if (isTv) RivuneSpacing.md else RivuneSpacing.sm))
            RivuneFocusSurface(
                onClick = onNext,
                enabled = enabled,
                isTv = isTv,
                idleColor = Color.Transparent,
                shape = RivuneShapes.pill,
            ) {
                Row(
                    modifier = Modifier.padding(horizontal = RivuneSpacing.sm, vertical = RivuneSpacing.xs),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Icon(
                        imageVector = Icons.Rounded.SkipNext,
                        contentDescription = if (isTv) null else stringResource(R.string.player_next_episode),
                        modifier = Modifier.size(RivuneDimensions.iconMedium),
                        tint = MaterialTheme.colorScheme.onSurface,
                    )
                    if (isTv) {
                        Spacer(Modifier.width(RivuneSpacing.xs))
                        Text(
                            text = stringResource(R.string.player_next_episode),
                            color = MaterialTheme.colorScheme.onSurface,
                            style = MaterialTheme.typography.labelLarge,
                        )
                    }
                }
            }
        }
    }
}

@OptIn(UnstableApi::class)
private fun selectPlayerTrack(player: ExoPlayer, trackType: Int, key: PlayerTrackKey?) {
    if (trackType == C.TRACK_TYPE_AUDIO && key == null) return
    val builder = player.trackSelectionParameters.buildUpon()
        .clearOverridesOfType(trackType)
        .setTrackTypeDisabled(trackType, trackType == C.TRACK_TYPE_TEXT && key == null)
    key?.let { selectedKey ->
        val selectedGroup = player.currentTracks.groups.getOrNull(selectedKey.groupIndex)
            ?.takeIf {
                it.mediaTrackGroup.id == selectedKey.groupId &&
                    it.type == trackType &&
                    selectedKey.trackIndex in 0 until it.length &&
                    it.isTrackSupported(selectedKey.trackIndex)
            }
            ?: return
        builder.addOverride(TrackSelectionOverride(selectedGroup.mediaTrackGroup, listOf(selectedKey.trackIndex)))
    }
    player.trackSelectionParameters = builder.build()
}

@OptIn(UnstableApi::class)
private fun playerTrackOptions(
    tracks: Tracks,
    trackType: Int,
    locale: Locale,
    fallbackLabel: String,
): List<PlayerTrackOption> {
    val entries = tracks.groups.flatMapIndexed { groupIndex, group ->
        if (group.type != trackType) return@flatMapIndexed emptyList()
        (0 until group.length).mapNotNull { trackIndex ->
            if (!group.isTrackSupported(trackIndex)) return@mapNotNull null
            val format = group.getTrackFormat(trackIndex)
            Triple(
                PlayerTrackKey(groupIndex, trackIndex, group.mediaTrackGroup.id),
                PlayerTrackLabelSource(format.label, format.language),
                group.isTrackSelected(trackIndex),
            )
        }
    }
    val uniqueEntries = entries.distinctBy { it.first }
    val labels = playerTrackLabels(uniqueEntries.map { it.second }, locale, fallbackLabel)
    return uniqueEntries.mapIndexed { index, (key, _, selected) -> PlayerTrackOption(key, labels[index], selected) }
}

internal fun playerTrackLabels(
    sources: List<PlayerTrackLabelSource>,
    locale: Locale,
    fallbackLabel: String,
): List<String> {
    val baseLabels = sources.mapIndexed { index, source ->
        val publishedLabel = source.label?.trim()?.takeIf(String::isNotEmpty)
        val languageLabel = localizedLanguageName(source.language, locale)
        when {
            publishedLabel == null -> languageLabel ?: "$fallbackLabel ${index + 1}"
            isGenericNumberedTrackLabel(publishedLabel, fallbackLabel) && languageLabel != null -> languageLabel
            publishedLabel.isLowercaseLanguageCode() -> localizedLanguageName(publishedLabel, locale) ?: publishedLabel
            else -> publishedLabel
        }
    }
    val counts = baseLabels.groupingBy { it }.eachCount()
    val occurrences = mutableMapOf<String, Int>()
    return baseLabels.map { label ->
        if (counts[label] == 1) label else "$label · ${(occurrences[label] ?: 0) + 1}".also {
            occurrences[label] = (occurrences[label] ?: 0) + 1
        }
    }
}

private fun isGenericNumberedTrackLabel(label: String, fallbackLabel: String): Boolean {
    val digitStart = label.indexOfFirst(Char::isDigit)
    if (digitStart <= 0) return false
    val suffix = label.substring(digitStart).trim()
    if (suffix.isEmpty() || !suffix.all(Char::isDigit)) return false
    val prefix = label.substring(0, digitStart).trim().trimEnd('#').trim()
    return prefix.equals(fallbackLabel, ignoreCase = true) ||
        prefix.equals("subtitle", ignoreCase = true) ||
        prefix.equals("audio", ignoreCase = true) ||
        prefix.equals("track", ignoreCase = true)
}

private fun String.isLowercaseLanguageCode(): Boolean {
    if (this != lowercase(Locale.ROOT)) return false
    val parts = replace('_', '-').split('-')
    return parts.isNotEmpty() && parts.all { part -> part.length in 2..3 && part.all(Char::isLetter) }
}

internal fun localizedLanguageName(language: String?, locale: Locale): String? {
    val code = language?.trim()?.takeIf(String::isNotEmpty) ?: return null
    val languageTag = code.replace('_', '-')
    return runCatching {
        Locale.forLanguageTag(languageTag)
            .takeIf { it.language.isNotEmpty() }
            ?.getDisplayLanguage(locale)
            ?.trim()
    }.getOrNull()?.takeIf {
        it.isNotEmpty() &&
            !it.equals("und", ignoreCase = true) &&
            !it.equals(languageTag.substringBefore('-'), ignoreCase = true)
    }?.replaceFirstChar { it.titlecase(locale) }
}

internal val PLAYER_PLAYBACK_SPEEDS = listOf(0.5f, 0.75f, 1f, 1.25f, 1.5f, 2f)

internal fun formatPlaybackSpeed(speed: Float): String =
    if (speed % 1f == 0f) "${speed.toInt()}×" else "${speed}×"

internal fun toggledPlayerOptionsMenu(current: PlayerOptionsMenu?, requested: PlayerOptionsMenu): PlayerOptionsMenu? =
    requested.takeUnless { it == current }

private tailrec fun Context.findActivity(): Activity? = when (this) {
    is Activity -> this
    is ContextWrapper -> baseContext.findActivity()
    else -> null
}

private fun PlayerPresentation.toMediaItem(): MediaItem =
    MediaItem.Builder()
        .setUri(mediaUrl)
        .setMediaId(key)
        .apply { playbackMimeType(protocol)?.let(::setMimeType) }
        .setSubtitleConfigurations(
            subtitles.map { subtitle ->
                MediaItem.SubtitleConfiguration.Builder(subtitle.url.toUri())
                    .setId(subtitle.id)
                    .setLabel(subtitle.label)
                    .setLanguage(subtitle.language)
                    .apply { subtitleMimeType(subtitle.url)?.let(::setMimeType) }
                    .setSelectionFlags(subtitleSelectionFlags(subtitle.selected))
                    .build()
            },
        )
        .build()

internal fun subtitleSelectionFlags(selected: Boolean): Int =
    if (selected) C.SELECTION_FLAG_DEFAULT else 0

internal fun playbackMimeType(protocol: String): String? =
    when (protocol.lowercase()) {
        "hls" -> MimeTypes.APPLICATION_M3U8
        "dash" -> MimeTypes.APPLICATION_MPD
        else -> null
    }

internal fun subtitleMimeType(url: String): String? {
    val extension = runCatching { URI(url).path.substringAfterLast('.', "").lowercase() }.getOrNull()
    return when (extension) {
        "srt" -> MimeTypes.APPLICATION_SUBRIP
        "ssa", "ass" -> MimeTypes.TEXT_SSA
        "ttml", "dfxp", "xml" -> MimeTypes.APPLICATION_TTML
        "vtt", "webvtt" -> MimeTypes.TEXT_VTT
        else -> null
    }
}

internal data class PlaybackMarkerEntry(
    val index: Int,
    val marker: PlaybackMarker,
    val startMs: Long,
    val endMs: Long,
)

internal fun activePlaybackMarkerEntry(
    markers: List<PlaybackMarker>,
    positionMs: Long,
): PlaybackMarkerEntry? {
    if (positionMs < 0L) return null
    return markers.withIndex().firstNotNullOfOrNull { (index, marker) ->
        if (!isValidPlaybackMarker(marker)) return@firstNotNullOfOrNull null
        val startMs = markerSecondsToMillis(marker.startSeconds) ?: return@firstNotNullOfOrNull null
        val endMs = markerSecondsToMillis(marker.endSeconds) ?: return@firstNotNullOfOrNull null
        PlaybackMarkerEntry(index, marker, startMs, endMs).takeIf { positionMs >= startMs && positionMs < endMs }
    }
}

internal fun shouldAutoSkipPlaybackMarker(
    type: PlaybackMarkerType,
    autoSkipIntro: Boolean,
    autoSkipRecap: Boolean,
    autoSkipOutro: Boolean,
): Boolean = when (type) {
    PlaybackMarkerType.INTRO -> autoSkipIntro
    PlaybackMarkerType.RECAP -> autoSkipRecap
    PlaybackMarkerType.OUTRO -> autoSkipOutro
}

internal fun autoSkipConsumedAfterUserSeek(
    markers: List<PlaybackMarker>,
    consumedEntries: Set<Int>,
    userSeekPositionMs: Long,
): Set<Int> = consumedEntries.filterTo(mutableSetOf()) { index ->
    val marker = markers.getOrNull(index) ?: return@filterTo false
    val startMs = markerSecondsToMillis(marker.startSeconds) ?: return@filterTo false
    userSeekPositionMs >= startMs
}

internal fun isValidPlaybackMarker(marker: PlaybackMarker): Boolean =
    marker.startSeconds.isFinite() &&
        marker.endSeconds.isFinite() &&
        marker.startSeconds >= 0.0 &&
        marker.endSeconds > marker.startSeconds &&
        markerSecondsToMillis(marker.startSeconds) != null &&
        markerSecondsToMillis(marker.endSeconds) != null

internal fun activePlaybackMarker(markers: List<PlaybackMarker>, positionMs: Long): PlaybackMarker? =
    activePlaybackMarkerEntry(markers, positionMs)?.marker

internal fun playbackMarkerSeekTargetMs(marker: PlaybackMarker): Long? =
    marker.takeIf(::isValidPlaybackMarker)?.let { markerSecondsToMillis(it.endSeconds) }

private fun markerSecondsToMillis(seconds: Double): Long? {
    if (!seconds.isFinite() || seconds < 0.0 || seconds > Long.MAX_VALUE.toDouble() / 1_000.0) return null
    return (seconds * 1_000.0).roundToLong()
}

internal fun absolutePlaybackPositionMs(
    mediaPositionMs: Long,
    timelineStartPositionMs: Long,
    mediaTimeline: PlaybackMediaTimeline?,
): Long = mediaPositionMs.coerceAtLeast(0L) + playbackTimelineOffsetMs(timelineStartPositionMs, mediaTimeline)

internal fun mediaPlaybackPositionMs(
    absolutePositionMs: Long,
    timelineStartPositionMs: Long,
    mediaTimeline: PlaybackMediaTimeline?,
): Long = (absolutePositionMs.coerceAtLeast(0L) - playbackTimelineOffsetMs(timelineStartPositionMs, mediaTimeline))
    .coerceAtLeast(0L)

internal fun resolvedPlaybackDurationMs(
    inspectedDurationMs: Long,
    playerDurationMs: Long,
    timelineStartPositionMs: Long,
    mediaTimeline: PlaybackMediaTimeline?,
): Long {
    if (inspectedDurationMs > 0L) return inspectedDurationMs
    if (playerDurationMs == C.TIME_UNSET || playerDurationMs <= 0L) return 0L
    return absolutePlaybackPositionMs(playerDurationMs, timelineStartPositionMs, mediaTimeline)
}

private fun playbackTimelineOffsetMs(
    timelineStartPositionMs: Long,
    mediaTimeline: PlaybackMediaTimeline?,
): Long = if (mediaTimeline == PlaybackMediaTimeline.RELATIVE) timelineStartPositionMs.coerceAtLeast(0L) else 0L

internal fun formatPlaybackTime(timeMs: Long): String {
    val totalSeconds = timeMs.coerceAtLeast(0L) / 1_000L
    val hours = totalSeconds / 3_600L
    val minutes = totalSeconds % 3_600L / 60L
    val seconds = totalSeconds % 60L
    return if (hours > 0L) {
        "$hours:${minutes.toString().padStart(2, '0')}:${seconds.toString().padStart(2, '0')}"
    } else {
        "$minutes:${seconds.toString().padStart(2, '0')}"
    }
}

internal fun isNaturalPlaybackEnd(playbackState: Int): Boolean = playbackState == Player.STATE_ENDED

internal fun completionThreshold(durationMs: Long): Long =
    durationMs / 100L * COMPLETION_PERCENT +
        (durationMs % 100L * COMPLETION_PERCENT + 99L) / 100L
