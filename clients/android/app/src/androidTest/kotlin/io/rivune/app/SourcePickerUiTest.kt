package io.rivune.app

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.requiredSize
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Text
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.click
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.test.swipeUp
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.dp
import androidx.test.espresso.Espresso.pressBack
import io.rivune.api.PlaybackMode
import io.rivune.api.PlaybackSourceOption
import io.rivune.app.ui.components.RivuneTestTags
import io.rivune.app.ui.theme.RivuneTheme
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import java.util.UUID

private const val BackgroundListTag = "rivune.source-picker.background-list"

class SourcePickerUiTest {
    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun tabletSourceRailAndDetailBothAcceptInput() {
        var selectedSource: PlaybackSourceOption? = null
        var selectedDetailOption: Int? = null
        setPicker(
            picker = testSourcePicker(12),
            onSelectSource = { selectedSource = it },
            onSelectDetailOption = { selectedDetailOption = it },
        )

        composeRule.onNodeWithText("Detail option 1")
            .assertIsDisplayed()
            .performTouchInput { click() }
        composeRule.runOnIdle { assertEquals(1, selectedDetailOption) }
        repeat(8) {
            composeRule.onNodeWithTag(BackgroundListTag).performTouchInput { swipeUp() }
        }
        composeRule.onNodeWithText("Detail option 20").assertIsDisplayed()

        composeRule.onNodeWithText("Stream 1").assertIsDisplayed()
        composeRule.onNodeWithText("Stream 12").assertDoesNotExist()
        repeat(4) {
            composeRule.onNodeWithTag(RivuneTestTags.SourcePickerList)
                .performTouchInput { swipeUp() }
        }
        composeRule.onNodeWithText("Stream 12")
            .assertIsDisplayed()
            .performClick()
        composeRule.runOnIdle { assertEquals("source-12", selectedSource?.id) }
    }

    @Test
    fun backInvokesOwningDetailNavigationWhileSourcesLoad() {
        var backInvocations = 0
        setPicker(
            picker = testSourcePicker(0),
            loading = true,
            onBack = { backInvocations += 1 },
        )

        pressBack()
        composeRule.runOnIdle { assertEquals(1, backInvocations) }
    }

    private fun setPicker(
        picker: SourcePickerState,
        loading: Boolean = false,
        onBack: () -> Unit = {},
        onSelectSource: (PlaybackSourceOption) -> Unit = {},
        onSelectDetailOption: (Int) -> Unit = {},
    ) {
        composeRule.setContent {
            RivuneTheme {
                CompositionLocalProvider(LocalDensity provides Density(density = 1f, fontScale = 1f)) {
                    Box(Modifier.requiredSize(width = 720.dp, height = 480.dp)) {
                        LazyColumn(
                            modifier = Modifier
                                .align(Alignment.CenterStart)
                                .width(280.dp)
                                .fillMaxHeight()
                                .testTag(BackgroundListTag),
                        ) {
                            items((1..20).toList()) { index ->
                                Text(
                                    text = "Detail option $index",
                                    modifier = Modifier
                                        .fillMaxWidth()
                                        .height(72.dp)
                                        .clickable { onSelectDetailOption(index) },
                                )
                            }
                        }
                        SourcePickerOverlay(
                            picker = picker,
                            isTv = false,
                            externalPlayers = emptyList(),
                            enabled = true,
                            loading = loading,
                            failure = null,
                            onBack = onBack,
                            onRefresh = {},
                            onSelectSource = onSelectSource,
                            onChooseTarget = {},
                            onDismissTarget = {},
                            modifier = Modifier.fillMaxSize(),
                        )
                    }
                }
            }
        }
    }
}

private fun testSourcePicker(sourceCount: Int): SourcePickerState {
    val titleId = UUID(0, 1)
    val addonId = UUID(0, 2)
    return SourcePickerState(
        target = MediaTarget(
            id = "picker-test",
            mediaType = "movie",
            title = "Picker test",
            titleId = titleId,
        ),
        titleId = titleId,
        progress = null,
        options = (1..sourceCount).map { index ->
            PlaybackSourceOption(
                id = "source-$index",
                sourceRef = "ref-$index",
                addonId = addonId,
                addonName = "Test addon",
                manifestId = "test-addon",
                streamIndex = index,
                name = "Stream $index",
                protocol = "http",
                mode = PlaybackMode.DIRECT,
                container = "mkv",
                expiresAt = "2099-01-01T00:00:00Z",
                stableIdentity = "stable-$index",
            )
        },
        partial = false,
    )
}
