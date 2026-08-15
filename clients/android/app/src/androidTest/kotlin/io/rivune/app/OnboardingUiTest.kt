package io.rivune.app

import androidx.annotation.StringRes
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.requiredSize
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.InputMode
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalInputModeManager
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsFocused
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertTextContains
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performImeAction
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.test.performKeyInput
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.Density
import androidx.test.platform.app.InstrumentationRegistry
import io.rivune.api.CategoryRef
import io.rivune.api.Profile
import io.rivune.api.ProfileAvatar
import io.rivune.app.ui.components.RivuneTestTags
import io.rivune.app.ui.theme.RivuneTheme
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import java.util.UUID

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
    @OptIn(ExperimentalTestApi::class)
    fun tvServerAddressEntersEditingAfterRemoteConfirmation() {
        setRivuneContent {
            ServerScreen(
                serverInput = "",
                isBusy = false,
                failure = null,
                isTv = true,
                onConnect = {},
                onClearFailure = {},
            )
        }

        composeRule.onNodeWithTag(RivuneTestTags.ServerInput)
            .assertIsFocused()
            .performKeyInput {
                keyDown(Key.DirectionCenter)
                keyUp(Key.DirectionCenter)
            }
            .performTextInput("http://localhost:8080")

        composeRule.onNodeWithTag(RivuneTestTags.ServerInput)
            .assertTextContains("http://localhost:8080")
        composeRule.onNodeWithTag(RivuneTestTags.ServerSubmit).assertIsEnabled()
    }

    @Test
    @OptIn(ExperimentalTestApi::class)
    fun tvProfilesAdaptColumnsAndKeepRowsNavigable() {
        val profiles = (1..12).map(::testProfile)
        var requestKeyboardInput = { false }
        setRivuneContent {
            CompositionLocalProvider(LocalDensity provides Density(density = 1f, fontScale = 1f)) {
                val inputModeManager = LocalInputModeManager.current
                requestKeyboardInput = { inputModeManager.requestInputMode(InputMode.Keyboard) }
                Box(Modifier.requiredSize(width = 960.dp, height = 540.dp)) {
                    ProfilesScreen(
                        profiles = profiles,
                        isBusy = false,
                        isTv = true,
                        failure = null,
                        resourceUrl = { it },
                        avatarData = emptyMap(),
                        onSelect = {},
                        onLogout = {},
                        onRefresh = {},
                        onClearFailure = {},
                    )
                }
            }
        }
        composeRule.runOnIdle { assertTrue(requestKeyboardInput()) }

        (1..6).forEach { index ->
            composeRule.onNodeWithContentDescription("Profile $index").assertIsDisplayed()
        }
        composeRule.onNodeWithContentDescription("Profile 1")
            .performSemanticsAction(SemanticsActions.RequestFocus)
            .assertIsFocused()
            .performKeyInput {
                keyDown(Key.DirectionDown)
                keyUp(Key.DirectionDown)
            }
        composeRule.onNodeWithContentDescription("Profile 7")
            .assertIsFocused()
            .assertIsDisplayed()
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

private fun testProfile(index: Int): Profile {
    val categoryId = UUID(0, 100)
    return Profile(
        id = UUID(0, index.toLong()),
        name = "Profile $index",
        categoryId = categoryId,
        category = CategoryRef(categoryId, "Home", null, null),
        isChild = false,
        hasPin = false,
        canManage = false,
        enabled = true,
        availableFrom = null,
        availableUntil = null,
        accessStartTime = null,
        accessEndTime = null,
        accessTimezone = "UTC",
        accessible = true,
        avatar = ProfileAvatar(kind = "custom", url = ""),
    )
}
