package io.rivune.app

import androidx.annotation.StringRes
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertTextContains
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performImeAction
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.test.performTextInput
import androidx.test.platform.app.InstrumentationRegistry
import io.rivune.app.ui.components.RivuneTestTags
import io.rivune.app.ui.theme.RivuneTheme
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test

class OnboardingUiTest {
    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun serverAddressEnablesContinueAndSubmits() {
        var submitted: String? = null
        setRivuneContent {
            ServerScreen(
                serverInput = "",
                isBusy = false,
                failure = null,
                isTv = false,
                onConnect = { submitted = it },
                onClearFailure = {},
            )
        }

        composeRule.onNodeWithTag(RivuneTestTags.ServerInput)
            .performTextInput("media.example.com")
        composeRule.onNodeWithTag(RivuneTestTags.ServerSubmit)
            .assertIsEnabled()
        composeRule.onNodeWithTag(RivuneTestTags.ServerInput).performImeAction()

        composeRule.runOnIdle { assertEquals("media.example.com", submitted) }
    }

    @Test
    fun connectionErrorStaysInFormAndRetryRemainsActionable() {
        var retried = false
        setRivuneContent {
            ServerScreen(
                serverInput = "media.example.com",
                isBusy = false,
                failure = UiFailure.SERVER_UNREACHABLE,
                isTv = false,
                onConnect = { retried = true },
                onClearFailure = {},
            )
        }

        composeRule.onNodeWithText(string(R.string.error_network)).assertIsDisplayed()
        composeRule.onNodeWithText(string(R.string.server_retry)).assertIsDisplayed()
        composeRule.onNodeWithTag(RivuneTestTags.ServerSubmit).performClick()

        composeRule.runOnIdle { assertTrue(retried) }
    }

    @Test
    fun connectionLoadingKeepsAddressVisibleAndReportsProgress() {
        setRivuneContent {
            ServerScreen(
                serverInput = "media.example.com",
                isBusy = true,
                failure = null,
                isTv = false,
                onConnect = {},
                onClearFailure = {},
            )
        }

        composeRule.onNodeWithTag(RivuneTestTags.ServerInput)
            .assertTextContains("media.example.com")
        composeRule.onNodeWithText(string(R.string.server_connecting)).assertIsDisplayed()
    }

    @Test
    fun pairingCodeCanBeCopiedAndDisconnectRequiresConfirmation() {
        var disconnected = false
        setRivuneContent {
            PairingScreen(
                pairing = PairingInfo("ABCD-EFGH"),
                pairingAccepted = false,
                isBusy = false,
                failure = null,
                isTv = false,
                onRestart = {},
                onDisconnect = { disconnected = true },
            )
        }

        composeRule.onNodeWithTag(RivuneTestTags.PairingCode)
            .assertIsDisplayed()
            .performClick()
        composeRule.onAllNodesWithText(string(R.string.pairing_copied))[0].assertIsDisplayed()
        composeRule.onNodeWithTag(RivuneTestTags.PairingDisconnect)
            .performScrollTo()
            .performClick()
        composeRule.onNodeWithText(string(R.string.pairing_disconnect_confirm_title)).assertIsDisplayed()
        composeRule.onNodeWithTag(RivuneTestTags.PairingDisconnectConfirm).performClick()

        composeRule.runOnIdle { assertTrue(disconnected) }
    }

    @Test
    fun expiredPairingPromotesCreatingANewCode() {
        var restarted = false
        setRivuneContent {
            PairingScreen(
                pairing = null,
                pairingAccepted = false,
                isBusy = false,
                failure = UiFailure.PAIRING_EXPIRED,
                isTv = false,
                onRestart = { restarted = true },
                onDisconnect = {},
            )
        }

        composeRule.onNodeWithTag(RivuneTestTags.PairingIssue).assertIsDisplayed()
        composeRule.onNodeWithText(string(R.string.pairing_expired_title)).assertIsDisplayed()
        composeRule.onNodeWithTag(RivuneTestTags.PairingRestart)
            .performScrollTo()
            .performClick()

        composeRule.runOnIdle { assertTrue(restarted) }
    }

    @Test
    fun acceptedPairingShowsShortSuccessState() {
        setRivuneContent {
            PairingScreen(
                pairing = PairingInfo("ABCD-EFGH"),
                pairingAccepted = true,
                isBusy = false,
                failure = null,
                isTv = false,
                onRestart = {},
                onDisconnect = {},
            )
        }

        composeRule.onNodeWithTag(RivuneTestTags.PairingSuccess).assertIsDisplayed()
        composeRule.onNodeWithText(string(R.string.pairing_success_title)).assertIsDisplayed()
    }

    @Test
    fun acceptedPairingTransitionKeepsItsOutgoingCode() {
        var pairing by mutableStateOf<PairingInfo?>(PairingInfo("ABCD-EFGH"))
        var accepted by mutableStateOf(false)
        setRivuneContent {
            PairingScreen(
                pairing = pairing,
                pairingAccepted = accepted,
                isBusy = false,
                failure = null,
                isTv = true,
                onRestart = {},
                onDisconnect = {},
            )
        }

        composeRule.onNodeWithTag(RivuneTestTags.PairingCode).assertIsDisplayed()
        composeRule.runOnIdle {
            pairing = null
            accepted = true
        }

        composeRule.onNodeWithTag(RivuneTestTags.PairingSuccess).assertIsDisplayed()
    }

    private fun setRivuneContent(content: @androidx.compose.runtime.Composable () -> Unit) {
        composeRule.setContent {
            RivuneTheme {
                Surface(
                    modifier = Modifier.fillMaxSize(),
                    color = MaterialTheme.colorScheme.background,
                ) {
                    content()
                }
            }
        }
    }

    private fun string(@StringRes resource: Int): String =
        InstrumentationRegistry.getInstrumentation().targetContext.getString(resource)
}
