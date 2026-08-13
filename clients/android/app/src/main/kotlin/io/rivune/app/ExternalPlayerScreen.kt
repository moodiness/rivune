package io.rivune.app

import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
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
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import io.rivune.app.ui.components.RivuneSecondaryButton
import io.rivune.app.ui.theme.RivuneDimensions
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

    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(Color.Black),
        contentAlignment = Alignment.Center,
    ) {
        Column(
            modifier = Modifier
                .widthIn(max = RivuneDimensions.contentMax)
                .padding(RivuneSpacing.xxl),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(RivuneSpacing.lg),
        ) {
            CircularProgressIndicator(color = MaterialTheme.colorScheme.primary)
            Icon(
                imageVector = Icons.AutoMirrored.Rounded.OpenInNew,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onSurface,
            )
            Text(
                text = stringResource(R.string.player_external_opening, player.label),
                color = MaterialTheme.colorScheme.onSurface,
                textAlign = TextAlign.Center,
                style = MaterialTheme.typography.titleLarge,
            )
            Text(
                text = stringResource(R.string.player_external_waiting),
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                textAlign = TextAlign.Center,
                style = MaterialTheme.typography.bodyMedium,
            )
            RivuneSecondaryButton(
                label = stringResource(R.string.player_close),
                onClick = onClose,
                isTv = isTv,
            )
        }
    }
}
