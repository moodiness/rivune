package io.rivune.app

import android.app.Activity
import android.content.Context
import android.content.ContextWrapper
import android.content.pm.ActivityInfo
import android.graphics.drawable.ColorDrawable
import android.graphics.drawable.GradientDrawable
import android.os.Build
import android.view.Surface
import android.view.SurfaceHolder
import android.view.SurfaceView
import android.view.View
import android.view.ViewGroup
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.annotation.OptIn
import androidx.media3.common.Format
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.core.net.toUri
import androidx.core.view.ViewCompat
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.MimeTypes
import androidx.media3.ui.AspectRatioFrameLayout
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.DecoderReuseEvaluation
import androidx.media3.exoplayer.analytics.AnalyticsListener
import androidx.media3.ui.DefaultTimeBar
import androidx.media3.ui.PlayerView
import androidx.media3.ui.R as Media3UiR
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import io.rivune.app.ui.components.RivuneFocusSurface
import io.rivune.app.ui.components.RivuneFunctionalSurface
import io.rivune.app.ui.theme.LocalRivuneMotionPolicy
import io.rivune.app.ui.theme.RivuneDimensions
import io.rivune.app.ui.theme.RivuneMotion
import io.rivune.app.ui.theme.finiteAnimationSpec
import io.rivune.app.ui.theme.RivuneScrim
import io.rivune.app.ui.theme.RivuneShapes
import io.rivune.app.ui.theme.RivuneSpacing
import java.net.URI

private const val PROGRESS_REPORT_INTERVAL_MS = 15_000L
private const val COMPLETION_PERCENT = 90L
private const val TABLET_SMALLEST_WIDTH_DP = 600

private enum class PlayerVisualState {
    Preparing,
    Buffering,
    Ready,
}

@OptIn(UnstableApi::class)
@Composable
internal fun RivunePlayerScreen(
    presentation: PlayerPresentation,
    isTv: Boolean,
    frameRateMatching: FrameRateMatchingPreference,
    videoAspect: VideoAspectPreference,
    onProgress: (Int, Int, Boolean) -> Unit,
    onClose: () -> Unit,
    onPlaybackError: () -> Unit,
) {
    val context = LocalContext.current
    val lifecycleOwner = LocalLifecycleOwner.current
    val configuration = LocalConfiguration.current
    val activity = remember(context) { context.findActivity() }
    val usePhonePresentation = !isTv &&
        configuration.smallestScreenWidthDp in 1 until TABLET_SMALLEST_WIDTH_DP
    val coroutineScope = rememberCoroutineScope()
    val currentOnProgress by rememberUpdatedState(onProgress)
    val currentOnClose by rememberUpdatedState(onClose)
    val currentOnPlaybackError by rememberUpdatedState(onPlaybackError)
    var closeRequested by remember(presentation.key) { mutableStateOf(false) }
    var finalProgressReporter by remember(presentation.key) { mutableStateOf<(() -> Unit)?>(null) }
    var visualState by remember(presentation.key) { mutableStateOf(PlayerVisualState.Preparing) }
    var controllerVisible by remember(presentation.key) { mutableStateOf(true) }

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
            .setVideoChangeFrameRateStrategy(frameRateMatching.media3Strategy(Build.VERSION.SDK_INT))
            .build()
            .also { newPlayer ->
                try {
                    newPlayer.setMediaItem(
                        presentation.toMediaItem(),
                        presentation.startPositionMs.coerceAtLeast(0L),
                    )
                    newPlayer.playWhenReady = true
                    newPlayer.prepare()
                } catch (failure: Throwable) {
                    newPlayer.release()
                    throw failure
                }
            }
    }

    fun requestClose() {
        if (!closeRequested) {
            finalProgressReporter?.invoke()
            closeRequested = true
            currentOnClose()
        }
    }

    BackHandler(enabled = !closeRequested) { requestClose() }

    DisposableEffect(player, lifecycleOwner) {
        var finished = false
        var errorDelivered = false
        var resumeAfterLifecyclePause = player.playWhenReady
        var reportingJob: Job? = null

        fun reportProgress() {
            val durationMs = player.duration.takeIf { it != C.TIME_UNSET && it > 0L } ?: 0L
            val positionMs = player.currentPosition.coerceAtLeast(0L).let { position ->
                if (durationMs > 0L) position.coerceAtMost(durationMs) else position
            }
            val positionSeconds = (positionMs / 1_000L).coerceAtMost(Int.MAX_VALUE.toLong()).toInt()
            val durationSeconds = if (durationMs > 0L) {
                ((durationMs + 999L) / 1_000L).coerceAtMost(Int.MAX_VALUE.toLong()).toInt()
            } else 0
            val completed = durationMs > 0L && positionMs >= completionThreshold(durationMs)
            currentOnProgress(positionSeconds, durationSeconds, completed)
        }
        finalProgressReporter = ::reportProgress

        var hasBeenReady = player.playbackState == Player.STATE_READY

        fun updateVisualState(playbackState: Int) {
            visualState = when (playbackState) {
                Player.STATE_IDLE -> PlayerVisualState.Preparing
                Player.STATE_BUFFERING -> if (hasBeenReady) {
                    PlayerVisualState.Buffering
                } else {
                    PlayerVisualState.Preparing
                }
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
            }

            override fun onPlayerError(error: PlaybackException) {
                visualState = PlayerVisualState.Ready
                if (!errorDelivered && !finished) {
                    errorDelivered = true
                    reportProgress()
                    currentOnPlaybackError()
                }
            }
        }
        player.addListener(playerListener)
        updateVisualState(player.playbackState)

        fun finish() {
            if (finished) return
            finished = true
            reportingJob?.cancel()
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
        if (!lifecycleOwner.lifecycle.currentState.isAtLeast(Lifecycle.State.RESUMED)) {
            player.pause()
        }

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

    val forcedFrameRateController = remember(player, frameRateMatching) {
        player.takeIf {
            frameRateMatching == FrameRateMatchingPreference.ENABLED && Build.VERSION.SDK_INT >= Build.VERSION_CODES.S
        }?.let(::ForcedFrameRateController)
    }
    DisposableEffect(forcedFrameRateController) {
        onDispose { forcedFrameRateController?.release() }
    }
    val motionPolicy = LocalRivuneMotionPolicy.current
    val controllerPlayedColor = MaterialTheme.colorScheme.primary.toArgb()
    val controllerBufferedColor = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.44f).toArgb()
    val controllerUnplayedColor = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.18f).toArgb()
    val controllerScrimColor = RivuneScrim.copy(alpha = 0.88f).toArgb()
    val controllerTransparentColor = Color.Transparent.toArgb()

    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(Color.Black),
    ) {
        AndroidView(
            factory = { viewContext ->
                PlayerView(viewContext).apply {
                    layoutParams = ViewGroup.LayoutParams(
                        ViewGroup.LayoutParams.MATCH_PARENT,
                        ViewGroup.LayoutParams.MATCH_PARENT,
                    )
                    resizeMode = videoAspect.resizeMode()
                    useController = true
                    controllerAutoShow = true
                    setShowBuffering(PlayerView.SHOW_BUFFERING_NEVER)
                    applyRivuneControllerStyle(
                        playedColor = controllerPlayedColor,
                        bufferedColor = controllerBufferedColor,
                        unplayedColor = controllerUnplayedColor,
                        scrimColor = controllerScrimColor,
                        transparentColor = controllerTransparentColor,
                        animationsEnabled = motionPolicy.playerControllerAnimations,
                        onVisibilityChanged = { visibility -> controllerVisible = visibility == View.VISIBLE },
                    )
                    isFocusable = true
                    isFocusableInTouchMode = true
                    this.player = player
                    forcedFrameRateController?.attach(videoSurfaceView)
                    if (isTv) {
                        post {
                            requestFocus()
                            showController()
                        }
                    }
                }
            },
            update = { playerView ->
                playerView.player = player
                playerView.resizeMode = videoAspect.resizeMode()
                forcedFrameRateController?.attach(playerView.videoSurfaceView)
                playerView.controllerShowTimeoutMs = if (isTv) 7_000 else 5_000
                playerView.applyRivuneControllerStyle(
                    playedColor = controllerPlayedColor,
                    bufferedColor = controllerBufferedColor,
                    unplayedColor = controllerUnplayedColor,
                    scrimColor = controllerScrimColor,
                    transparentColor = controllerTransparentColor,
                    animationsEnabled = motionPolicy.playerControllerAnimations,
                    onVisibilityChanged = { visibility -> controllerVisible = visibility == View.VISIBLE },
                )
            },
            onRelease = { playerView ->
                forcedFrameRateController?.attach(null)
                playerView.player = null
            },
            modifier = Modifier
                .fillMaxSize()
                .windowInsetsPadding(WindowInsets.safeDrawing),
        )

        if (visualState != PlayerVisualState.Ready) {
            val status = stringResource(
                if (visualState == PlayerVisualState.Preparing) {
                    R.string.player_preparing
                } else {
                    R.string.player_buffering
                },
            )
            RivuneFunctionalSurface(
                modifier = Modifier
                    .align(Alignment.Center)
                    .semantics(mergeDescendants = true) {
                        liveRegion = LiveRegionMode.Polite
                    },
                shape = RivuneShapes.pill,
                contentPadding = PaddingValues(
                    horizontal = if (isTv) RivuneSpacing.lg else RivuneSpacing.md,
                    vertical = RivuneSpacing.xs,
                ),
            ) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    if (motionPolicy.ambientAnimations) {
                        CircularProgressIndicator(
                            modifier = Modifier
                                .size(RivuneDimensions.iconSmall)
                                .clearAndSetSemantics { },
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

        AnimatedVisibility(
            visible = controllerVisible || visualState != PlayerVisualState.Ready,
            enter = fadeIn(motionPolicy.finiteAnimationSpec(RivuneMotion.fast)),
            exit = fadeOut(motionPolicy.finiteAnimationSpec(RivuneMotion.fast)),
        ) {
            RivunePlayerTopBar(
                title = presentation.title,
                isTv = isTv,
                onClose = ::requestClose,
                enabled = !closeRequested,
            )
        }
    }
}

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
                        RivuneScrim.copy(alpha = 0.92f),
                        RivuneScrim.copy(alpha = 0.54f),
                        Color.Transparent,
                    ),
                ),
            )
            .windowInsetsPadding(WindowInsets.safeDrawing)
            .padding(
                start = if (isTv) RivuneSpacing.xxl else RivuneSpacing.xs,
                top = if (isTv) RivuneSpacing.md else RivuneSpacing.xs,
                end = if (isTv) RivuneSpacing.xxl else RivuneSpacing.xs,
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
            modifier = closeModifier
                .size(if (isTv) RivuneDimensions.touchTargetTv else RivuneDimensions.touchTarget),
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
            modifier = Modifier
                .weight(1f)
                .semantics { heading() },
            color = MaterialTheme.colorScheme.onSurface,
            style = if (isTv) MaterialTheme.typography.titleLarge else MaterialTheme.typography.titleMedium,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

@OptIn(UnstableApi::class)
private fun PlayerView.applyRivuneControllerStyle(
    playedColor: Int,
    bufferedColor: Int,
    unplayedColor: Int,
    scrimColor: Int,
    transparentColor: Int,
    animationsEnabled: Boolean,
    onVisibilityChanged: (Int) -> Unit,
) {
    setShutterBackgroundColor(android.graphics.Color.BLACK)
    setControllerAnimationEnabled(animationsEnabled)
    setShowPreviousButton(false)
    setShowNextButton(false)
    setShowSubtitleButton(true)
    findViewById<View>(Media3UiR.id.exo_controls_background)?.background = GradientDrawable(
        GradientDrawable.Orientation.BOTTOM_TOP,
        intArrayOf(scrimColor, transparentColor, transparentColor, transparentColor),
    )
    setControllerVisibilityListener(PlayerView.ControllerVisibilityListener(onVisibilityChanged))
    findViewById<View>(Media3UiR.id.exo_bottom_bar)?.background = ColorDrawable(transparentColor)
    (findViewById<View>(Media3UiR.id.exo_progress) as? DefaultTimeBar)?.apply {
        setPlayedColor(playedColor)
        setScrubberColor(playedColor)
        setBufferedColor(bufferedColor)
        setUnplayedColor(unplayedColor)
    }
}

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

private fun completionThreshold(durationMs: Long): Long =
    durationMs / 100L * COMPLETION_PERCENT +
        (durationMs % 100L * COMPLETION_PERCENT + 99L) / 100L
