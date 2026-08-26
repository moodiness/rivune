package io.rivune.app.ui.theme

import androidx.compose.animation.core.FiniteAnimationSpec
import androidx.compose.animation.core.snap
import androidx.compose.animation.core.tween
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.Shapes
import androidx.compose.material3.Typography
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.remember
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp
import androidx.compose.ui.platform.LocalContext
import io.rivune.app.AnimationPreference
import io.rivune.app.DEFAULT_ACCENT_COLOR
import io.rivune.api.AccessibilityPreferencesDocument
import io.rivune.api.FocusIndicatorsPreference
import io.rivune.api.HighContrastPreference
import io.rivune.api.ReducedMotionPreference

private val DefaultAccentColors = rivuneAccentColors(DEFAULT_ACCENT_COLOR)

private val RivuneDarkColors = darkColorScheme(
    primary = DefaultAccentColors.primary,
    onPrimary = DefaultAccentColors.onPrimary,
    primaryContainer = DefaultAccentColors.primaryContainer,
    onPrimaryContainer = DefaultAccentColors.onPrimaryContainer,
    secondary = RivuneTextSoft,
    onSecondary = RivuneBackground,
    tertiary = RivuneSuccess,
    onTertiary = RivuneBackground,
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
    inverseSurface = RivuneText,
    inverseOnSurface = RivuneBackground,
    inversePrimary = DefaultAccentColors.pressed,
    scrim = RivuneScrim,
    surfaceTint = DefaultAccentColors.primary,
)

@Immutable
internal data class RivuneMotionPolicy(
    val finiteAnimations: Boolean,
    val ambientAnimations: Boolean,
    val imageCrossfade: Boolean,
    val playerControllerAnimations: Boolean,
)

internal fun motionPolicy(
    preference: AnimationPreference,
    systemEnabled: Boolean,
): RivuneMotionPolicy {
    val enabled = when (preference) {
        AnimationPreference.SYSTEM -> systemEnabled
        AnimationPreference.FULL -> true
        AnimationPreference.REDUCED -> false
    }
    return RivuneMotionPolicy(
        finiteAnimations = enabled,
        ambientAnimations = enabled,
        imageCrossfade = enabled,
        playerControllerAnimations = enabled,
    )
}

internal fun <T> RivuneMotionPolicy.finiteAnimationSpec(durationMillis: Int): FiniteAnimationSpec<T> =
    if (finiteAnimations) tween(durationMillis) else snap()

internal val LocalRivuneMotionPolicy = staticCompositionLocalOf {
    motionPolicy(AnimationPreference.SYSTEM, systemEnabled = true)
}
internal val LocalRivuneEnhancedFocusIndicators = staticCompositionLocalOf { false }

private fun Typography.scaled(percent: Int): Typography {
    if (percent == 100) return this
    val factor = percent / 100f
    fun TextStyle.scale() = copy(fontSize = fontSize * factor, lineHeight = lineHeight * factor)
    return copy(
        displayLarge = displayLarge.scale(), displayMedium = displayMedium.scale(), displaySmall = displaySmall.scale(),
        headlineLarge = headlineLarge.scale(), headlineMedium = headlineMedium.scale(), headlineSmall = headlineSmall.scale(),
        titleLarge = titleLarge.scale(), titleMedium = titleMedium.scale(), titleSmall = titleSmall.scale(),
        bodyLarge = bodyLarge.scale(), bodyMedium = bodyMedium.scale(), bodySmall = bodySmall.scale(),
        labelLarge = labelLarge.scale(), labelMedium = labelMedium.scale(), labelSmall = labelSmall.scale(),
    )
}

private val RivuneTypography = Typography(
    displayLarge = TextStyle(
        fontFamily = FontFamily.SansSerif,
        fontWeight = FontWeight.Bold,
        fontSize = 52.sp,
        lineHeight = 56.sp,
        letterSpacing = (-1.4).sp,
    ),
    displayMedium = TextStyle(
        fontFamily = FontFamily.SansSerif,
        fontWeight = FontWeight.Bold,
        fontSize = 44.sp,
        lineHeight = 48.sp,
        letterSpacing = (-1.0).sp,
    ),
    headlineLarge = TextStyle(
        fontFamily = FontFamily.SansSerif,
        fontWeight = FontWeight.Bold,
        fontSize = 34.sp,
        lineHeight = 38.sp,
        letterSpacing = (-0.6).sp,
    ),
    headlineMedium = TextStyle(
        fontFamily = FontFamily.SansSerif,
        fontWeight = FontWeight.Bold,
        fontSize = 27.sp,
        lineHeight = 32.sp,
        letterSpacing = (-0.35).sp,
    ),
    headlineSmall = TextStyle(
        fontFamily = FontFamily.SansSerif,
        fontWeight = FontWeight.SemiBold,
        fontSize = 23.sp,
        lineHeight = 28.sp,
        letterSpacing = (-0.15).sp,
    ),
    titleLarge = TextStyle(fontFamily = FontFamily.SansSerif, fontWeight = FontWeight.Bold, fontSize = 20.sp, lineHeight = 25.sp, letterSpacing = (-0.15).sp),
    titleMedium = TextStyle(fontFamily = FontFamily.SansSerif, fontWeight = FontWeight.SemiBold, fontSize = 16.sp, lineHeight = 21.sp),
    titleSmall = TextStyle(fontFamily = FontFamily.SansSerif, fontWeight = FontWeight.SemiBold, fontSize = 14.sp, lineHeight = 19.sp),
    bodyLarge = TextStyle(fontFamily = FontFamily.SansSerif, fontSize = 16.sp, lineHeight = 23.sp),
    bodyMedium = TextStyle(fontFamily = FontFamily.SansSerif, fontSize = 14.sp, lineHeight = 20.sp),
    bodySmall = TextStyle(fontFamily = FontFamily.SansSerif, fontSize = 12.sp, lineHeight = 17.sp),
    labelLarge = TextStyle(fontFamily = FontFamily.SansSerif, fontWeight = FontWeight.SemiBold, fontSize = 14.sp, lineHeight = 19.sp, letterSpacing = 0.1.sp),
    labelMedium = TextStyle(fontFamily = FontFamily.SansSerif, fontWeight = FontWeight.Bold, fontSize = 12.sp, lineHeight = 16.sp, letterSpacing = 0.65.sp),
    labelSmall = TextStyle(fontFamily = FontFamily.SansSerif, fontWeight = FontWeight.SemiBold, fontSize = 11.sp, lineHeight = 15.sp, letterSpacing = 0.35.sp),
)

private val MaterialShapes = Shapes(
    extraSmall = RivuneShapes.small,
    small = RivuneShapes.small,
    medium = RivuneShapes.medium,
    large = RivuneShapes.large,
    extraLarge = RivuneShapes.extraLarge,
)

@Composable
internal fun RivuneTheme(
    accentColor: Int = DEFAULT_ACCENT_COLOR,
    animationPreference: AnimationPreference = AnimationPreference.SYSTEM,
    systemAnimationsEnabled: Boolean = true,
    accessibility: AccessibilityPreferencesDocument? = null,
    content: @Composable () -> Unit,
) {
    val context = LocalContext.current
    val systemHighContrast = runCatching {
        android.provider.Settings.Secure.getInt(
            context.contentResolver,
            "high_text_contrast_enabled",
            0,
        ) == 1
    }.getOrDefault(false)
    val colors = remember(accentColor, accessibility?.highContrast, systemHighContrast) {
        val accent = rivuneAccentColors(accentColor)
        RivuneDarkColors.copy(
            primary = accent.primary,
            onPrimary = accent.onPrimary,
            primaryContainer = accent.primaryContainer,
            onPrimaryContainer = accent.onPrimaryContainer,
            inversePrimary = accent.pressed,
            surfaceTint = accent.primary,
        ).let { scheme ->
            if (accessibility?.highContrast == HighContrastPreference.MORE ||
                accessibility?.highContrast == HighContrastPreference.SYSTEM && systemHighContrast
            ) scheme.copy(
                background = androidx.compose.ui.graphics.Color.Black,
                surface = androidx.compose.ui.graphics.Color.Black,
                onBackground = androidx.compose.ui.graphics.Color.White,
                onSurface = androidx.compose.ui.graphics.Color.White,
                onSurfaceVariant = androidx.compose.ui.graphics.Color.White,
                outline = androidx.compose.ui.graphics.Color.White,
                outlineVariant = androidx.compose.ui.graphics.Color.White,
            ) else scheme
        }
    }
    val effectiveAnimationPreference = when (accessibility?.reducedMotion) {
        ReducedMotionPreference.REDUCE -> AnimationPreference.REDUCED
        ReducedMotionPreference.NO_PREFERENCE -> AnimationPreference.FULL
        else -> animationPreference
    }
    val policy = remember(effectiveAnimationPreference, systemAnimationsEnabled) {
        motionPolicy(effectiveAnimationPreference, systemAnimationsEnabled)
    }
    val typography = remember(accessibility?.textScale) { RivuneTypography.scaled(accessibility?.textScale ?: 100) }
    CompositionLocalProvider(
        LocalRivuneMotionPolicy provides policy,
        LocalRivuneEnhancedFocusIndicators provides (accessibility?.focusIndicators == FocusIndicatorsPreference.ENHANCED),
    ) {
        MaterialTheme(
            colorScheme = colors,
            typography = typography,
            shapes = MaterialShapes,
            content = content,
        )
    }
}
