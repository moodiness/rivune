package io.rivune.app

import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.Column
import androidx.compose.material3.Text
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.fetchSemanticsNode
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.unit.dp
import io.rivune.app.ui.components.RivuneFocusSurface
import io.rivune.app.ui.theme.RivuneTheme
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test

class RivuneFocusSurfaceUiTest {
    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun ordinaryButtonDoesNotExposeUnselectedState() {
        composeRule.setContent {
            RivuneTheme {
                RivuneFocusSurface(
                    onClick = {},
                    modifier = Modifier.testTag("ordinary-action"),
                ) {
                    Text("Ordinary action", modifier = Modifier.padding(8.dp))
                }
            }
        }

        val semantics = composeRule.onNodeWithTag("ordinary-action").fetchSemanticsNode().config
        assertFalse(semantics.contains(SemanticsProperties.Selected))
    }

    @Test
    fun selectableControlsExposeBothSelectedStates() {
        composeRule.setContent {
            RivuneTheme {
                Column {
                    RivuneFocusSurface(
                        onClick = {},
                        selected = false,
                        modifier = Modifier.testTag("unselected-filter"),
                    ) {
                        Text("All", modifier = Modifier.padding(8.dp))
                    }
                    RivuneFocusSurface(
                        onClick = {},
                        selected = true,
                        modifier = Modifier.testTag("selected-filter"),
                    ) {
                        Text("Movies", modifier = Modifier.padding(8.dp))
                    }
                }
            }
        }

        val unselected = composeRule.onNodeWithTag("unselected-filter").fetchSemanticsNode().config
        val selected = composeRule.onNodeWithTag("selected-filter").fetchSemanticsNode().config
        assertTrue(unselected.contains(SemanticsProperties.Selected))
        assertTrue(selected.contains(SemanticsProperties.Selected))
        assertEquals(false, unselected[SemanticsProperties.Selected])
        assertEquals(true, selected[SemanticsProperties.Selected])
    }
}
