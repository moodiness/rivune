package io.rivune.app.ui.components

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Dns
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import io.rivune.app.ui.theme.RivuneTheme

@Preview(name = "Rivune actions", widthDp = 390, backgroundColor = 0xFF08090C, showBackground = true)
@Composable
private fun RivuneActionsPreview() {
    PreviewSurface {
        RivunePrimaryButton("Continue", onClick = {}, modifier = Modifier.fillMaxWidth())
        RivunePrimaryButton("Connecting…", onClick = {}, modifier = Modifier.fillMaxWidth(), loading = true)
        RivunePrimaryButton("Continue", onClick = {}, modifier = Modifier.fillMaxWidth(), enabled = false)
        RivuneSecondaryButton("Get a new code", onClick = {}, modifier = Modifier.fillMaxWidth())
        RivuneTextButton("Disconnect from server", onClick = {}, destructive = true)
    }
}

@Preview(name = "Rivune fields", widthDp = 390, backgroundColor = 0xFF08090C, showBackground = true)
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
    }
}

@Preview(name = "Rivune heading large text", widthDp = 360, fontScale = 2f, backgroundColor = 0xFF08090C, showBackground = true)
@Composable
private fun RivuneHeadingLargeTextPreview() {
    PreviewSurface {
        RivuneScreenHeading(
            eyebrow = "First connection",
            title = "Where does your Rivune live?",
            body = "Enter the address of your Rivune server. Your library stays on your server.",
            isTv = false,
        )
    }
}

@Composable
private fun PreviewSurface(content: @Composable () -> Unit) {
    RivuneTheme {
        Column(
            modifier = Modifier
                .background(androidx.compose.material3.MaterialTheme.colorScheme.background)
                .padding(24.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            content()
        }
    }
}
