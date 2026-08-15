package io.rivune.app.ui.theme

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.compositeOver
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.graphics.lerp

private const val MINIMUM_ACCENT_TEXT_CONTRAST = 4.5f
internal val RivuneBackground = Color(0xFF050505)
internal val RivuneBackgroundSoft = Color(0xFF080808)
internal val RivuneSurface = Color(0xFF0D0D0D)
internal val RivuneSurfaceRaised = Color(0xFF141414)
internal val RivuneSurfaceInteractive = Color(0xFF1B1B1B)
internal val RivuneFunctionalLayer = Color(0xF20C0C0C)
internal val RivuneArtworkPlaceholder = Color(0xFF151515)
internal val RivuneCinematicTop = Color(0xFF050505)
internal val RivuneSurfaceSelected = Color(0xFF202020)
internal val RivuneText = Color(0xFFF5F3EF)
internal val RivuneTextSoft = Color(0xFFC9C6C1)
internal val RivuneTextMuted = Color(0xFF929298)
internal val RivuneAccent = Color(0xFF77A7FF)
internal val RivuneAccentPressed = Color(0xFF5F8FEA)
internal val RivuneBrandHairline = Color(0x7377A7FF)
internal val RivuneAccentSubtle = Color(0xFF17243D)
internal val RivuneAccentStrong = Color(0xFFA9C5FF)
internal val RivuneAccentInk = Color(0xFF07152E)

internal data class RivuneAccentColors(
    val primary: Color,
    val onPrimary: Color,
    val primaryContainer: Color,
    val onPrimaryContainer: Color,
    val pressed: Color,
)

internal fun rivuneAccentColors(accentColor: Int): RivuneAccentColors {
    val primary = Color(accentColor or 0xFF000000.toInt())
    val container = if (primary == RivuneAccent) {
        RivuneAccentSubtle
    } else {
        primary.copy(alpha = 0.20f).compositeOver(RivuneSurface)
    }
    val pressed = if (primary == RivuneAccent) {
        RivuneAccentPressed
    } else {
        lerp(primary, Color.Black, 0.10f)
    }
    return RivuneAccentColors(
        primary = primary,
        onPrimary = if (primary == RivuneAccent) RivuneAccentInk else readableForeground(primary),
        primaryContainer = container,
        onPrimaryContainer = if (primary == RivuneAccent) RivuneAccentStrong else readableForeground(container),
        pressed = pressed,
    )
}

internal fun rivuneAccentHasReadableContrast(accentColor: Int): Boolean {
    val foregroundLuminance = Color(accentColor or 0xFF000000.toInt()).luminance()
    val backgroundLuminance = RivuneSurfaceSelected.luminance()
    val lighter = maxOf(foregroundLuminance, backgroundLuminance)
    val darker = minOf(foregroundLuminance, backgroundLuminance)
    return (lighter + 0.05f) / (darker + 0.05f) >= MINIMUM_ACCENT_TEXT_CONTRAST
}

internal fun rivuneAccentHairline(primary: Color): Color =
    if (primary == RivuneAccent) RivuneBrandHairline else lerp(primary, Color.White, 0.26f).copy(alpha = 0.45f)

private fun readableForeground(background: Color): Color {
    val luminance = background.luminance()
    val blackContrast = (luminance + 0.05f) / 0.05f
    val whiteContrast = 1.05f / (luminance + 0.05f)
    return if (blackContrast >= whiteContrast) Color.Black else Color.White
}
internal val RivuneBorder = Color(0xFF242424)
internal val RivuneBorderStrong = Color(0xFF393939)
internal val RivuneDanger = Color(0xFFFF8D92)
internal val RivuneDangerContainer = Color(0xFF3B1C23)
internal val RivuneDangerText = Color(0xFFFFDADD)
internal val RivuneScrim = Color(0xCC000000)
internal val RivuneSuccess = Color(0xFF79D5AE)
internal val RivuneWarning = Color(0xFFE8B870)
