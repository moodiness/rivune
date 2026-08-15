package io.rivune.app.ui.theme

import io.rivune.app.AnimationPreference
import io.rivune.app.DEFAULT_ACCENT_COLOR
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class RivuneThemeTest {
    @Test
    fun motionPolicyFollowsPreferenceAndSystemState() {
        assertTrue(motionPolicy(AnimationPreference.SYSTEM, systemEnabled = true).finiteAnimations)
        assertFalse(motionPolicy(AnimationPreference.SYSTEM, systemEnabled = false).finiteAnimations)
        assertTrue(motionPolicy(AnimationPreference.FULL, systemEnabled = false).ambientAnimations)
        assertFalse(motionPolicy(AnimationPreference.REDUCED, systemEnabled = true).finiteAnimations)
        assertFalse(motionPolicy(AnimationPreference.REDUCED, systemEnabled = true).ambientAnimations)
        assertFalse(motionPolicy(AnimationPreference.REDUCED, systemEnabled = true).imageCrossfade)
        assertFalse(motionPolicy(AnimationPreference.REDUCED, systemEnabled = true).playerControllerAnimations)
    }

    @Test
    fun defaultAccentPreservesBluePalette() {
        assertEquals(0xFF77A7FF.toInt(), DEFAULT_ACCENT_COLOR)
        val palette = rivuneAccentColors(DEFAULT_ACCENT_COLOR)

        assertEquals(RivuneAccent, palette.primary)
        assertEquals(RivuneAccentInk, palette.onPrimary)
        assertEquals(RivuneAccentSubtle, palette.primaryContainer)
        assertEquals(RivuneAccentStrong, palette.onPrimaryContainer)
        assertEquals(RivuneAccentPressed, palette.pressed)
    }

    @Test
    fun customAccentIsOpaqueAndUsesReadableForeground() {
        val light = rivuneAccentColors(0x00123456)
        val dark = rivuneAccentColors(0x00000000)

        assertEquals(androidx.compose.ui.graphics.Color(0xFF123456), light.primary)
        assertEquals(androidx.compose.ui.graphics.Color.White, dark.onPrimary)
        assertEquals(1f, light.primary.alpha)
    }

    @Test
    fun customAccentMustRemainReadableOnInteractiveSurfaces() {
        assertTrue(rivuneAccentHasReadableContrast(DEFAULT_ACCENT_COLOR))
        assertTrue(rivuneAccentHasReadableContrast(0xFF77A7FF.toInt()))
        assertFalse(rivuneAccentHasReadableContrast(0xFF5A5A5A.toInt()))
        assertFalse(rivuneAccentHasReadableContrast(0xFF000000.toInt()))
    }
}
