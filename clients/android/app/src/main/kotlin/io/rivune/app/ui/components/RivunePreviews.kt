package io.rivune.app.ui.components

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.Logout
import androidx.compose.material.icons.rounded.Dns
import androidx.compose.material.icons.rounded.ErrorOutline
import androidx.compose.material.icons.rounded.Movie
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material3.Icon
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import io.rivune.app.ui.theme.RivuneDimensions
import io.rivune.app.ui.theme.RivuneShapes
import io.rivune.app.ui.theme.RivuneSpacing
import io.rivune.app.ui.theme.RivuneTheme

@Preview(name = "Actions — compact", widthDp = 360, heightDp = 520, showBackground = true)
@Composable
private fun RivuneActionsPreview() {
    PreviewSurface {
        RivunePrimaryButton("Continue", onClick = {}, modifier = Modifier.fillMaxWidth())
        RivunePrimaryButton("Connecting…", onClick = {}, modifier = Modifier.fillMaxWidth(), loading = true)
        RivunePrimaryButton("Continue", onClick = {}, modifier = Modifier.fillMaxWidth(), enabled = false)
        RivuneSecondaryButton(
            "Play preview",
            onClick = {},
            modifier = Modifier.fillMaxWidth(),
            icon = Icons.Rounded.PlayArrow,
        )
        RivuneSecondaryButton("Get a new code", onClick = {}, modifier = Modifier.fillMaxWidth(), loading = true)
        RivuneTextButton("Disconnect", onClick = {}, destructive = true, icon = Icons.AutoMirrored.Rounded.Logout)
    }
}

@Preview(name = "Fields — reference", widthDp = 390, heightDp = 420, showBackground = true)
@Composable
private fun RivuneFieldsPreview() {
    PreviewSurface {
        RivuneTextField(
            value = "https://rivune.example",
            onValueChange = {},
            label = "Server address",
            modifier = Modifier.fillMaxWidth(),
            leadingIcon = Icons.Rounded.Dns,
        )
        RivuneTextField(
            value = "not a server",
            onValueChange = {},
            label = "Server address",
            modifier = Modifier.fillMaxWidth(),
            leadingIcon = Icons.Rounded.Dns,
            isError = true,
            supportingText = "Check the address and try again.",
        )
        RivuneTextField(
            value = "Unavailable",
            onValueChange = {},
            label = "Server address",
            modifier = Modifier.fillMaxWidth(),
            enabled = false,
        )
    }
}

@Preview(name = "Foundation — reference", widthDp = 390, heightDp = 620, showBackground = true)
@Composable
private fun RivuneFoundationPreview() {
    PreviewSurface {
        RivuneBrandLockup(name = "Rivune", tagline = "Your library, beautifully close")
        RivuneSectionHeading(
            title = "Continue watching",
            trailingAction = { RivuneTextButton("See all", onClick = {}) },
        )
        RivuneFunctionalSurface(modifier = Modifier.fillMaxWidth()) {
            Column(verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xs)) {
                Text("A quiet layer for navigation and controls", style = MaterialTheme.typography.titleMedium)
                Text(
                    "Artwork stays vivid while functional chrome remains restrained.",
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    style = MaterialTheme.typography.bodyMedium,
                )
            }
        }
        Row(
            horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            RivuneArtwork(
                model = null,
                fallback = "The Last Ember",
                contentDescription = "The Last Ember artwork",
                modifier = Modifier
                    .size(width = 96.dp, height = 136.dp)
                    .clip(RivuneShapes.medium),
            )
            Column(
                modifier = Modifier.weight(1f),
                verticalArrangement = Arrangement.spacedBy(RivuneSpacing.sm),
            ) {
                RivuneFocusSurface(onClick = {}, selected = true, modifier = Modifier.fillMaxWidth()) {
                    Text("Selected library row", modifier = Modifier.padding(RivuneSpacing.md))
                }
                RivuneSkeleton(modifier = Modifier.fillMaxWidth().height(20.dp), shape = RivuneShapes.small)
                RivuneSkeleton(modifier = Modifier.fillMaxWidth().height(12.dp), shape = RivuneShapes.small)
            }
        }
    }
}

@Preview(name = "Status states — compact", widthDp = 360, heightDp = 500, showBackground = true)
@Composable
private fun RivuneStatusStatesPreview() {
    PreviewSurface {
        RivuneFunctionalSurface(modifier = Modifier.fillMaxWidth()) {
            Row(
                horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                CircularProgressIndicator(
                    modifier = Modifier.size(RivuneDimensions.iconMedium),
                    strokeWidth = RivuneDimensions.hairline,
                )
                Text("Loading your library", style = MaterialTheme.typography.titleMedium)
            }
        }
        RivuneFunctionalSurface(modifier = Modifier.fillMaxWidth()) {
            Row(
                horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Icon(Icons.Rounded.ErrorOutline, contentDescription = null, tint = MaterialTheme.colorScheme.error)
                Column {
                    Text("Couldn’t reach your server", style = MaterialTheme.typography.titleMedium)
                    Text(
                        "Check the address and try again.",
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        style = MaterialTheme.typography.bodyMedium,
                    )
                }
            }
        }
        RivuneFunctionalSurface(modifier = Modifier.fillMaxWidth()) {
            Row(
                horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Icon(Icons.Rounded.Movie, contentDescription = null, tint = MaterialTheme.colorScheme.primary)
                Column {
                    Text("Build your watchlist", style = MaterialTheme.typography.titleMedium)
                    Text(
                        "Add a title and it will appear here.",
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        style = MaterialTheme.typography.bodyMedium,
                    )
                }
            }
        }
    }
}

@Preview(name = "Editorial type — large font", widthDp = 360, heightDp = 560, fontScale = 1.8f, showBackground = true)
@Composable
private fun RivuneLargeTextPreview() {
    PreviewSurface {
        RivuneScreenHeading(
            eyebrow = "First connection",
            title = "Where does your Rivune live?",
            body = "Enter the address of your Rivune server. Your library stays on your server.",
            isTv = false,
        )
        RivuneSectionHeading(title = "Recently added")
        RivunePrimaryButton("Continue", onClick = {}, modifier = Modifier.fillMaxWidth())
    }
}

@Composable
private fun PreviewSurface(content: @Composable ColumnScope.() -> Unit) {
    RivuneTheme {
        RivuneCinematicBackground {
            Column(
                modifier = Modifier
                    .fillMaxSize()
                    .padding(RivuneSpacing.xl),
                verticalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
                content = content,
            )
        }
    }
}
