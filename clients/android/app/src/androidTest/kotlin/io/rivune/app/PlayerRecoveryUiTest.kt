package io.rivune.app

import android.view.KeyEvent

import androidx.annotation.StringRes
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.requiredSize
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsFocused
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.dp
import androidx.test.platform.app.InstrumentationRegistry
import io.rivune.app.ui.components.RivuneTestTags
import io.rivune.app.ui.theme.RivuneDimensions
import io.rivune.app.ui.theme.RivuneSpacing
import io.rivune.app.ui.theme.RivuneTheme
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test

class PlayerRecoveryUiTest {
    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun wideRecoveryIsBoundedAndAllActionsRemainAvailable() {
        var retries = 0
        var restarts = 0
        var sourceChanges = 0
        setRecoveryContent(
            isTv = false,
            widthDp = 720,
            heightDp = 480,
            onRetry = { retries += 1 },
            onStartOver = { restarts += 1 },
            onChooseSource = { sourceChanges += 1 },
        )

        val expectedMaxWidth = (RivuneDimensions.dialogMax - RivuneSpacing.huge).value
        val cardWidth = recoveryCardWidth()
        assertEquals(expectedMaxWidth, cardWidth, 1f)
        composeRule.onNodeWithText(string(R.string.player_recovery_title)).assertIsDisplayed()
        composeRule.onNodeWithText(string(R.string.player_recovery_body)).assertIsDisplayed()

        val retryBounds = composeRule.onNodeWithText(string(R.string.player_recovery_retry))
            .fetchSemanticsNode()
            .boundsInRoot
        val startOverBounds = composeRule.onNodeWithText(string(R.string.player_recovery_start_over))
            .fetchSemanticsNode()
            .boundsInRoot
        val chooseSourceBounds = composeRule.onNodeWithText(string(R.string.player_recovery_choose_source))
            .fetchSemanticsNode()
            .boundsInRoot
        assertEquals(retryBounds.top, startOverBounds.top, 1f)
        assertEquals(startOverBounds.top, chooseSourceBounds.top, 1f)
        val cardBounds = composeRule.onNodeWithTag(RivuneTestTags.PlayerRecoveryCard)
            .fetchSemanticsNode()
            .boundsInRoot
        val actionGroupCenter = (retryBounds.left + chooseSourceBounds.right) / 2f
        assertEquals(cardBounds.center.x, actionGroupCenter, 1f)

        composeRule.onNodeWithText(string(R.string.player_recovery_retry)).performClick()
        composeRule.onNodeWithText(string(R.string.player_recovery_start_over)).performClick()
        composeRule.onNodeWithText(string(R.string.player_recovery_choose_source)).performClick()
        composeRule.runOnIdle {
            assertEquals(1, retries)
            assertEquals(1, restarts)
            assertEquals(1, sourceChanges)
        }
    }

    @Test
    fun narrowRecoveryStacksSecondaryActions() {
        setRecoveryContent(isTv = false, widthDp = 360, heightDp = 640)

        assertTrue(recoveryCardWidth() < (RivuneDimensions.dialogMax - RivuneSpacing.huge).value)
        val retryBounds = composeRule.onNodeWithText(string(R.string.player_recovery_retry))
            .fetchSemanticsNode()
            .boundsInRoot
        val startOverBounds = composeRule.onNodeWithText(string(R.string.player_recovery_start_over))
            .fetchSemanticsNode()
            .boundsInRoot
        val chooseSourceBounds = composeRule.onNodeWithText(string(R.string.player_recovery_choose_source))
            .fetchSemanticsNode()
            .boundsInRoot
        assertTrue(startOverBounds.top >= retryBounds.bottom)
        assertTrue(chooseSourceBounds.top >= startOverBounds.bottom)
    }

    @Test
    fun tvRecoveryIsBoundedAndInitiallyFocusesRetry() {
        InstrumentationRegistry.getInstrumentation().sendKeyDownUpSync(KeyEvent.KEYCODE_DPAD_DOWN)
        setRecoveryContent(isTv = true, widthDp = 720, heightDp = 480)

        assertEquals(RivuneDimensions.dialogMax.value, recoveryCardWidth(), 1f)
        composeRule.onNodeWithText(string(R.string.player_recovery_retry))
            .assertIsDisplayed()
            .assertIsFocused()
    }

    private fun setRecoveryContent(
        isTv: Boolean,
        widthDp: Int,
        heightDp: Int,
        onRetry: () -> Unit = {},
        onStartOver: () -> Unit = {},
        onChooseSource: () -> Unit = {},
    ) {
        composeRule.setContent {
            RivuneTheme {
                Surface(
                    modifier = Modifier.fillMaxSize(),
                    color = MaterialTheme.colorScheme.background,
                ) {
                    CompositionLocalProvider(LocalDensity provides Density(density = 1f, fontScale = 1f)) {
                        Box(Modifier.requiredSize(widthDp.dp, heightDp.dp)) {
                            PlayerRecoveryOverlayContent(
                                isTv = isTv,
                                failure = PlayerEngineFailure(positionMs = 30_000L, fallbackEligible = false),
                                onRetry = onRetry,
                                onStartOver = onStartOver,
                                onChooseSource = onChooseSource,
                            )
                        }
                    }
                }
            }
        }
    }

    private fun recoveryCardWidth(): Float =
        composeRule.onNodeWithTag(RivuneTestTags.PlayerRecoveryCard)
            .assertIsDisplayed()
            .fetchSemanticsNode()
            .boundsInRoot
            .width

    private fun string(@StringRes resource: Int): String =
        InstrumentationRegistry.getInstrumentation().targetContext.getString(resource)
}
