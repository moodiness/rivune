package io.rivune.app

import android.view.ViewGroup
import androidx.annotation.OptIn
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.core.net.toUri
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.MimeTypes
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.ui.PlayerView
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import io.rivune.app.ui.components.RivuneFocusSurface
import io.rivune.app.ui.theme.RivuneDimensions
import io.rivune.app.ui.theme.RivuneElevation
import io.rivune.app.ui.theme.RivuneShapes
import io.rivune.app.ui.theme.RivuneSpacing
import io.rivune.app.ui.theme.RivuneSurfaceRaised
import io.rivune.app.ui.theme.RivuneTextSoft
import java.net.URI

private const val PROGRESS_REPORT_INTERVAL_MS = 15_000L
private const val COMPLETION_PERCENT = 90L

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
    onProgress: (Int, Int, Boolean) -> Unit,
    onClose: () -> Unit,
    onPlaybackError: () -> Unit,
) {
    val context = LocalContext.current
    val lifecycleOwner = LocalLifecycleOwner.current
    val coroutineScope = rememberCoroutineScope()
    val currentOnProgress by rememberUpdatedState(onProgress)
    val currentOnClose by rememberUpdatedState(onClose)
    val currentOnPlaybackError by rememberUpdatedState(onPlaybackError)
    val closeFocusRequester = remember { FocusRequester() }
    var closeRequested by remember(presentation.key) { mutableStateOf(false) }
    var finalProgressReporter by remember(presentation.key) { mutableStateOf<(() -> Unit)?>(null) }
    var visualState by remember(presentation.key) { mutableStateOf(PlayerVisualState.Preparing) }

    val player = remember(context, presentation.key) {
        ExoPlayer.Builder(context).build().also { newPlayer ->
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

    LaunchedEffect(isTv, presentation.key) {
        if (isTv) closeFocusRequester.requestFocus()
    }

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
                    useController = true
                    controllerAutoShow = true
                    setShowBuffering(PlayerView.SHOW_BUFFERING_NEVER)
                    isFocusable = true
                    isFocusableInTouchMode = true
                    this.player = player
                }
            },
            update = { playerView ->
                playerView.player = player
                playerView.controllerShowTimeoutMs = if (isTv) 7_000 else 5_000
            },
            modifier = Modifier.fillMaxSize().windowInsetsPadding(WindowInsets.safeDrawing),
        )

        if (visualState != PlayerVisualState.Ready) {
            val status = stringResource(
                if (visualState == PlayerVisualState.Preparing) {
                    R.string.player_preparing
                } else {
                    R.string.player_buffering
                },
            )
            Surface(
                modifier = Modifier
                    .align(Alignment.TopStart)
                    .windowInsetsPadding(WindowInsets.safeDrawing)
                    .padding(RivuneSpacing.sm)
                    .semantics(mergeDescendants = true) {
                        liveRegion = LiveRegionMode.Polite
                    },
                shape = RivuneShapes.pill,
                color = RivuneSurfaceRaised,
                contentColor = RivuneTextSoft,
                shadowElevation = RivuneElevation.overlay,
            ) {
                Row(
                    modifier = Modifier.padding(
                        horizontal = RivuneSpacing.md,
                        vertical = RivuneSpacing.sm,
                    ),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    CircularProgressIndicator(
                        modifier = Modifier
                            .size(RivuneSpacing.md)
                            .clearAndSetSemantics { },
                        color = MaterialTheme.colorScheme.primary,
                        strokeWidth = 2.dp,
                    )
                    Spacer(Modifier.width(RivuneSpacing.sm))
                    Text(text = status, style = MaterialTheme.typography.labelLarge)
                }
            }
        }

        RivuneFocusSurface(
            onClick = { requestClose() },
            enabled = !closeRequested,
            isTv = isTv,
            shape = RivuneShapes.pill,
            modifier = Modifier
                .align(Alignment.TopEnd)
                .windowInsetsPadding(WindowInsets.safeDrawing)
                .padding(RivuneSpacing.sm)
                .size(if (isTv) RivuneDimensions.touchTargetTv else RivuneDimensions.touchTarget)
                .focusRequester(closeFocusRequester),
        ) {
            Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                Icon(
                    imageVector = Icons.Default.Close,
                    contentDescription = stringResource(R.string.player_close),
                    tint = MaterialTheme.colorScheme.onSurface,
                )
            }
        }
    }
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
