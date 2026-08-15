package io.rivune.app

import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.layout.size
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.OpenInNew
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import io.rivune.app.ui.components.RivuneCinematicBackground
import io.rivune.app.ui.components.RivuneFunctionalSurface
import io.rivune.app.ui.theme.LocalRivuneMotionPolicy
import io.rivune.app.ui.theme.RivuneDimensions
import io.rivune.app.ui.theme.RivuneShapes
import io.rivune.app.ui.theme.RivuneSpacing

@Composable
internal fun RivuneExternalPlayerScreen(
    presentation: PlayerPresentation,
    isTv: Boolean,
    onResult: (ExternalPlaybackResult?) -> Unit,
    onClose: () -> Unit,
    onLaunchFailure: () -> Unit,
) {
    val context = LocalContext.current
    val currentOnResult by rememberUpdatedState(onResult)
    val currentOnLaunchFailure by rememberUpdatedState(onLaunchFailure)
    var launchRequested by rememberSaveable(presentation.key) { mutableStateOf(false) }
    val player = requireNotNull(presentation.externalPlayer)
    val closeFocus = remember { FocusRequester() }
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) { activityResult ->
        currentOnResult(parseExternalPlaybackResult(player.packageName, activityResult.data))
    }

    BackHandler(onBack = onClose)
    LaunchedEffect(presentation.key, player.packageName) {
        if (launchRequested) return@LaunchedEffect
        val intent = buildExternalPlaybackIntent(context, presentation)
        if (intent == null) {
            currentOnLaunchFailure()
            return@LaunchedEffect
        }
        launchRequested = true
        runCatching { launcher.launch(intent) }
            .onFailure { currentOnLaunchFailure() }
    }
    LaunchedEffect(isTv, presentation.key) {
        if (isTv) closeFocus.requestFocus()
    }


    RivuneCinematicBackground(modifier = Modifier.fillMaxSize()) {
        Box(
            modifier = Modifier
                .fillMaxSize()
                .windowInsetsPadding(WindowInsets.safeDrawing)
                .padding(
                    horizontal = if (isTv) RivuneSpacing.huge else RivuneSpacing.xl,
                    vertical = if (isTv) RivuneSpacing.huge else RivuneSpacing.xl,
                ),
            contentAlignment = Alignment.Center,
        ) {
            RivuneFunctionalSurface(
                modifier = Modifier.widthIn(max = RivuneDimensions.contentMax),
                shape = RivuneShapes.medium,
                contentPadding = PaddingValues(
                    horizontal = if (isTv) RivuneSpacing.xl else RivuneSpacing.lg,
                    vertical = if (isTv) RivuneSpacing.lg else RivuneSpacing.md,
                ),
            ) {
                Row(
                    modifier = Modifier.semantics(mergeDescendants = true) {
                        liveRegion = LiveRegionMode.Polite
                    },
                    horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    if (LocalRivuneMotionPolicy.current.ambientAnimations) {
                        CircularProgressIndicator(
                            modifier = Modifier
                                .size(RivuneDimensions.iconMedium)
                                .clearAndSetSemantics { },
                            color = MaterialTheme.colorScheme.primary,
                            strokeWidth = RivuneDimensions.hairline,
                        )
                    } else {
                        Icon(
                            imageVector = Icons.AutoMirrored.Rounded.OpenInNew,
                            contentDescription = null,
                            modifier = Modifier.size(RivuneDimensions.iconMedium),
                            tint = MaterialTheme.colorScheme.primary,
                        )
                    }
                    Column(verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xxs)) {
                        Text(
                            text = stringResource(R.string.player_external_opening, player.label),
                            color = MaterialTheme.colorScheme.onSurface,
                            style = if (isTv) MaterialTheme.typography.titleLarge else MaterialTheme.typography.titleMedium,
                            maxLines = 2,
                            overflow = TextOverflow.Ellipsis,
                        )
                        Text(
                            text = stringResource(R.string.player_external_waiting),
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            style = if (isTv) MaterialTheme.typography.bodyMedium else MaterialTheme.typography.bodySmall,
                        )
                    }
                }
            }
        }

        RivunePlayerTopBar(
            title = presentation.title,
            isTv = isTv,
            onClose = onClose,
            closeModifier = Modifier.focusRequester(closeFocus),
        )
    }
}
