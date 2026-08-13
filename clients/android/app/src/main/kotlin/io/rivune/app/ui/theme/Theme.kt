package io.rivune.app.ui.theme

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.Shapes
import androidx.compose.material3.Typography
import androidx.compose.runtime.Composable
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp

private val RivuneDarkColors = darkColorScheme(
    primary = RivuneAccent,
    onPrimary = RivuneAccentInk,
    primaryContainer = RivuneAccentSubtle,
    onPrimaryContainer = RivuneAccentStrong,
    secondary = RivuneTextSoft,
    onSecondary = RivuneBackground,
    background = RivuneBackground,
    onBackground = RivuneText,
    surface = RivuneSurface,
    onSurface = RivuneText,
    surfaceVariant = RivuneSurfaceRaised,
    onSurfaceVariant = RivuneTextSoft,
    surfaceContainerLowest = RivuneBackgroundSoft,
    surfaceContainerLow = RivuneSurface,
    surfaceContainer = RivuneSurfaceRaised,
    surfaceContainerHigh = RivuneSurfaceInteractive,
    surfaceContainerHighest = RivuneSurfaceSelected,
    outline = RivuneBorderStrong,
    outlineVariant = RivuneBorder,
    error = RivuneDanger,
    onError = RivuneBackground,
    errorContainer = RivuneDangerContainer,
    onErrorContainer = RivuneDangerText,
)

private val RivuneTypography = Typography(
    displayLarge = TextStyle(
        fontFamily = FontFamily.Serif,
        fontWeight = FontWeight.Medium,
        fontSize = 54.sp,
        lineHeight = 58.sp,
        letterSpacing = (-1.1).sp,
    ),
    displayMedium = TextStyle(
        fontFamily = FontFamily.Serif,
        fontWeight = FontWeight.Medium,
        fontSize = 46.sp,
        lineHeight = 50.sp,
        letterSpacing = (-0.8).sp,
    ),
    headlineLarge = TextStyle(
        fontFamily = FontFamily.Serif,
        fontWeight = FontWeight.Medium,
        fontSize = 38.sp,
        lineHeight = 42.sp,
        letterSpacing = (-0.5).sp,
    ),
    headlineMedium = TextStyle(
        fontFamily = FontFamily.Serif,
        fontWeight = FontWeight.Medium,
        fontSize = 30.sp,
        lineHeight = 36.sp,
        letterSpacing = (-0.2).sp,
    ),
    headlineSmall = TextStyle(
        fontFamily = FontFamily.Serif,
        fontWeight = FontWeight.Medium,
        fontSize = 25.sp,
        lineHeight = 31.sp,
    ),
    titleLarge = TextStyle(fontWeight = FontWeight.SemiBold, fontSize = 21.sp, lineHeight = 27.sp),
    titleMedium = TextStyle(fontWeight = FontWeight.SemiBold, fontSize = 16.sp, lineHeight = 22.sp),
    titleSmall = TextStyle(fontWeight = FontWeight.SemiBold, fontSize = 14.sp, lineHeight = 20.sp),
    bodyLarge = TextStyle(fontSize = 16.sp, lineHeight = 25.sp),
    bodyMedium = TextStyle(fontSize = 14.sp, lineHeight = 21.sp),
    bodySmall = TextStyle(fontSize = 12.sp, lineHeight = 18.sp),
    labelLarge = TextStyle(fontWeight = FontWeight.SemiBold, fontSize = 14.sp, lineHeight = 20.sp, letterSpacing = 0.1.sp),
    labelMedium = TextStyle(fontWeight = FontWeight.Bold, fontSize = 12.sp, lineHeight = 17.sp, letterSpacing = 0.8.sp),
    labelSmall = TextStyle(fontWeight = FontWeight.SemiBold, fontSize = 11.sp, lineHeight = 16.sp, letterSpacing = 0.5.sp),
)

private val MaterialShapes = Shapes(
    extraSmall = RivuneShapes.small,
    small = RivuneShapes.small,
    medium = RivuneShapes.medium,
    large = RivuneShapes.large,
    extraLarge = RivuneShapes.extraLarge,
)

@Composable
fun RivuneTheme(content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = RivuneDarkColors,
        typography = RivuneTypography,
        shapes = MaterialShapes,
        content = content,
    )
}
