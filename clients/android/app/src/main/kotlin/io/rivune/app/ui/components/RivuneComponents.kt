package io.rivune.app.ui.components

import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.focusable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.defaultMinSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.requiredSize
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CornerBasedShape
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.OutlinedTextFieldDefaults
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Shape
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.disabled
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.zIndex
import coil.compose.AsyncImage
import coil.request.ImageRequest
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import io.rivune.app.R
import io.rivune.app.ui.theme.RivuneArtworkPlaceholder
import io.rivune.app.ui.theme.RivuneBackground
import io.rivune.app.ui.theme.RivuneBackgroundSoft
import io.rivune.app.ui.theme.RivuneBorder
import io.rivune.app.ui.theme.RivuneBorderStrong
import io.rivune.app.ui.theme.RivuneDanger
import io.rivune.app.ui.theme.RivuneDimensions
import io.rivune.app.ui.theme.RivuneElevation
import io.rivune.app.ui.theme.LocalRivuneMotionPolicy
import io.rivune.app.ui.theme.RivuneMotion
import io.rivune.app.ui.theme.finiteAnimationSpec
import io.rivune.app.ui.theme.RivuneFunctionalLayer
import io.rivune.app.ui.theme.RivuneShapes
import io.rivune.app.ui.theme.RivuneSpacing
import io.rivune.app.ui.theme.RivuneSurface
import io.rivune.app.ui.theme.RivuneSurfaceInteractive
import io.rivune.app.ui.theme.RivuneSurfaceSelected
import io.rivune.app.ui.theme.RivuneSurfaceRaised
import io.rivune.app.ui.theme.RivuneText
import io.rivune.app.ui.theme.RivuneTextMuted
import io.rivune.app.ui.theme.RivuneTextSoft

@Composable
internal fun RivuneCinematicBackground(
    modifier: Modifier = Modifier,
    content: @Composable BoxScope.() -> Unit,
) {
    Box(
        modifier = modifier
            .fillMaxSize()
            .background(RivuneBackground),
        content = content,
    )
}

@Composable
internal fun RivuneFunctionalSurface(
    modifier: Modifier = Modifier,
    shape: Shape = RivuneShapes.large,
    contentPadding: PaddingValues = PaddingValues(RivuneSpacing.md),
    color: Color = RivuneFunctionalLayer,
    content: @Composable BoxScope.() -> Unit,
) {
    Surface(
        modifier = modifier,
        shape = shape,
        color = color,
        contentColor = MaterialTheme.colorScheme.onSurface,
        border = BorderStroke(RivuneDimensions.hairline, RivuneBorder.copy(alpha = 0.8f)),
        tonalElevation = RivuneElevation.flat,
        shadowElevation = RivuneElevation.flat,
    ) {
        Box(modifier = Modifier.padding(contentPadding), content = content)
    }
}

@Composable
internal fun RivuneSectionHeading(
    title: String,
    modifier: Modifier = Modifier,
    trailingAction: (@Composable () -> Unit)? = null,
) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .heightIn(min = RivuneDimensions.touchTarget),
        horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = title,
            modifier = Modifier
                .weight(1f)
                .semantics { heading() },
            style = MaterialTheme.typography.titleLarge,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
        )
        trailingAction?.invoke()
    }
}

@Composable
internal fun RivunePrimaryButton(
    label: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    loading: Boolean = false,
    isTv: Boolean = false,
    icon: ImageVector? = null,
    loadingDescription: String = label,
    compact: Boolean = false,
) {
    val motionPolicy = LocalRivuneMotionPolicy.current

    @Composable
    fun Content(contentColor: Color) {
        AnimatedContent(
            targetState = loading,
            transitionSpec = {
                fadeIn(motionPolicy.finiteAnimationSpec(RivuneMotion.fast)) togetherWith
                    fadeOut(motionPolicy.finiteAnimationSpec(RivuneMotion.fast))
            },
            label = "primary-button-content",
        ) { busy ->
            Row(
                horizontalArrangement = Arrangement.Center,
                verticalAlignment = Alignment.CenterVertically,
            ) {
                if (busy && motionPolicy.ambientAnimations) {
                    CircularProgressIndicator(
                        modifier = Modifier.size(RivuneDimensions.iconSmall),
                        strokeWidth = RivuneDimensions.hairline,
                        color = contentColor,
                    )
                } else if (!busy && icon != null) {
                    Icon(
                        imageVector = icon,
                        contentDescription = null,
                        modifier = Modifier.size(RivuneDimensions.iconMedium),
                        tint = contentColor,
                    )
                }
                if ((busy && motionPolicy.ambientAnimations) || (!busy && icon != null)) {
                    Spacer(Modifier.width(RivuneSpacing.sm))
                }
                Text(label, color = contentColor, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
        }
    }

    if (isTv) {
        val active = enabled && !loading
        val containerColor = if (active) MaterialTheme.colorScheme.primary else RivuneSurfaceInteractive
        val contentColor = if (active) MaterialTheme.colorScheme.onPrimary else RivuneTextMuted
        RivuneFocusSurface(
            onClick = onClick,
            enabled = active,
            isTv = true,
            idleColor = containerColor,
            focusedColor = containerColor,
            pressedColor = containerColor,
            showFocusBorder = false,
            focusScale = RivuneMotion.tvButtonFocusScale,
            shape = RivuneShapes.medium,
            modifier = modifier
                .heightIn(min = RivuneDimensions.buttonHeightTv)
                .semantics {
                    if (loading) stateDescription = loadingDescription
                },
        ) {
            Box(
                modifier = Modifier.padding(
                    horizontal = if (compact) RivuneSpacing.md else RivuneSpacing.lg,
                    vertical = RivuneSpacing.xxs,
                ),
                contentAlignment = Alignment.Center,
            ) {
                Content(contentColor)
            }
        }
        return
    }

    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    val pressed by interaction.collectIsPressedAsState()
    val container by animateColorAsState(
        targetValue = if (pressed) MaterialTheme.colorScheme.inversePrimary else MaterialTheme.colorScheme.primary,
        animationSpec = motionPolicy.finiteAnimationSpec(RivuneMotion.fast),
        label = "primary-button-color",
    )
    Button(
        onClick = onClick,
        modifier = modifier
            .heightIn(min = if (compact) RivuneDimensions.touchTarget else RivuneDimensions.buttonHeight)
            .rivuneFocusRing(focused, RivuneShapes.medium)
            .semantics {
                if (loading) stateDescription = loadingDescription
            },
        enabled = enabled && !loading,
        interactionSource = interaction,
        shape = RivuneShapes.medium,
        colors = ButtonDefaults.buttonColors(
            containerColor = container,
            contentColor = MaterialTheme.colorScheme.onPrimary,
            disabledContainerColor = RivuneSurfaceInteractive,
            disabledContentColor = RivuneTextMuted,
        ),
        contentPadding = PaddingValues(
            horizontal = if (compact) RivuneSpacing.md else RivuneSpacing.lg,
            vertical = RivuneSpacing.xxs,
        ),
    ) {
        Content(if (enabled && !loading) MaterialTheme.colorScheme.onPrimary else RivuneTextMuted)
    }
}

@Composable
internal fun RivuneSecondaryButton(
    label: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    loading: Boolean = false,
    isTv: Boolean = false,
    icon: ImageVector? = null,
    compact: Boolean = false,
    transparent: Boolean = false,
    neutralContent: Boolean = false,
    destructive: Boolean = false,
) {
    val motionPolicy = LocalRivuneMotionPolicy.current
    val focusColor = if (destructive) RivuneDanger else MaterialTheme.colorScheme.primary
    val contentColor = when {
        destructive -> RivuneDanger
        neutralContent -> MaterialTheme.colorScheme.onSurface
        else -> MaterialTheme.colorScheme.primary
    }

    @Composable
    fun Content(resolvedContentColor: Color) {
        if (loading && motionPolicy.ambientAnimations) {
            CircularProgressIndicator(
                modifier = Modifier.size(RivuneDimensions.iconSmall),
                strokeWidth = RivuneDimensions.hairline,
                color = resolvedContentColor,
            )
        } else if (!loading && icon != null) {
            Icon(
                imageVector = icon,
                contentDescription = null,
                modifier = Modifier.size(RivuneDimensions.iconMedium),
                tint = resolvedContentColor,
            )
        }
        if ((loading && motionPolicy.ambientAnimations) || (!loading && icon != null)) {
            Spacer(Modifier.width(RivuneSpacing.sm))
        }
        Text(label, color = resolvedContentColor, maxLines = 1, overflow = TextOverflow.Ellipsis)
    }

    if (isTv) {
        val active = enabled && !loading
        val containerColor = when {
            transparent -> Color.Transparent
            active -> RivuneSurfaceRaised
            else -> RivuneSurfaceInteractive
        }
        val resolvedContentColor = if (active) contentColor else RivuneTextMuted
        RivuneFocusSurface(
            onClick = onClick,
            enabled = active,
            isTv = true,
            idleColor = containerColor,
            focusedColor = containerColor,
            pressedColor = containerColor,
            restingBorderColor = if (transparent) null else RivuneBorder,
            showFocusBorder = false,
            focusScale = RivuneMotion.tvButtonFocusScale,
            shape = RivuneShapes.medium,
            modifier = modifier
                .heightIn(min = RivuneDimensions.buttonHeightTv)
                .semantics {
                    if (loading) stateDescription = label
                },
        ) {
            Row(
                modifier = Modifier.padding(
                    horizontal = if (compact) RivuneSpacing.md else RivuneSpacing.lg,
                    vertical = RivuneSpacing.xxs,
                ),
                horizontalArrangement = Arrangement.Center,
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Content(resolvedContentColor)
            }
        }
        return
    }

    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    val pressed by interaction.collectIsPressedAsState()
    val container by animateColorAsState(
        targetValue = when {
            destructive && (pressed || focused) -> MaterialTheme.colorScheme.errorContainer
            pressed -> RivuneSurfaceInteractive
            transparent && focused -> MaterialTheme.colorScheme.primaryContainer
            transparent -> Color.Transparent
            else -> RivuneSurfaceRaised
        },
        animationSpec = motionPolicy.finiteAnimationSpec(RivuneMotion.fast),
        label = "secondary-button-color",
    )
    OutlinedButton(
        onClick = onClick,
        modifier = modifier
            .heightIn(min = if (compact) RivuneDimensions.touchTarget else RivuneDimensions.buttonHeight)
            .rivuneFocusRing(focused, RivuneShapes.medium, focusColor)
            .semantics {
                if (loading) stateDescription = label
            },
        enabled = enabled && !loading,
        interactionSource = interaction,
        shape = RivuneShapes.medium,
        border = if (transparent) {
            null
        } else {
            BorderStroke(RivuneDimensions.hairline, if (focused) focusColor else RivuneBorder)
        },
        colors = ButtonDefaults.outlinedButtonColors(
            containerColor = container,
            contentColor = contentColor,
            disabledContainerColor = if (transparent) Color.Transparent else RivuneSurfaceInteractive,
            disabledContentColor = RivuneTextMuted,
        ),
        contentPadding = PaddingValues(
            horizontal = if (compact) RivuneSpacing.md else RivuneSpacing.lg,
            vertical = RivuneSpacing.xxs,
        ),
    ) {
        Content(if (enabled && !loading) contentColor else RivuneTextMuted)
    }
}

@Composable
internal fun RivuneTextButton(
    label: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    isTv: Boolean = false,
    destructive: Boolean = false,
    icon: ImageVector? = null,
    neutralContent: Boolean = false,
) {
    val contentColor = when {
        !enabled -> RivuneTextMuted
        destructive -> RivuneDanger
        neutralContent -> MaterialTheme.colorScheme.onSurface
        else -> MaterialTheme.colorScheme.primary
    }

    @Composable
    fun Content() {
        if (icon != null) {
            Icon(
                imageVector = icon,
                contentDescription = null,
                modifier = Modifier.size(RivuneDimensions.iconMedium),
                tint = contentColor,
            )
            Spacer(Modifier.width(RivuneSpacing.xs))
        }
        Text(label, color = contentColor, maxLines = 1, overflow = TextOverflow.Ellipsis)
    }

    if (isTv) {
        val transparent = MaterialTheme.colorScheme.background.copy(alpha = 0f)
        RivuneFocusSurface(
            onClick = onClick,
            enabled = enabled,
            isTv = true,
            idleColor = transparent,
            focusedColor = transparent,
            pressedColor = transparent,
            showFocusBorder = false,
            focusScale = RivuneMotion.tvButtonFocusScale,
            shape = RivuneShapes.small,
            modifier = modifier.heightIn(min = RivuneDimensions.touchTargetTv),
        ) {
            Row(
                modifier = Modifier.padding(horizontal = RivuneSpacing.sm, vertical = RivuneSpacing.xs),
                horizontalArrangement = Arrangement.Center,
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Content()
            }
        }
        return
    }

    val motionPolicy = LocalRivuneMotionPolicy.current
    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    val pressed by interaction.collectIsPressedAsState()
    val container by animateColorAsState(
        targetValue = if (pressed) RivuneSurfaceInteractive else Color.Transparent,
        animationSpec = motionPolicy.finiteAnimationSpec(RivuneMotion.fast),
        label = "text-button-color",
    )
    TextButton(
        onClick = onClick,
        modifier = modifier
            .heightIn(min = RivuneDimensions.touchTarget)
            .rivuneFocusRing(focused, RivuneShapes.small, if (destructive) RivuneDanger else MaterialTheme.colorScheme.primary),
        enabled = enabled,
        interactionSource = interaction,
        shape = RivuneShapes.small,
        colors = ButtonDefaults.textButtonColors(
            containerColor = container,
            contentColor = contentColor,
            disabledContentColor = RivuneTextMuted,
        ),
        contentPadding = PaddingValues(horizontal = RivuneSpacing.sm, vertical = RivuneSpacing.xs),
    ) {
        Content()
    }
}

@Composable
internal fun RivuneTextField(
    value: String,
    onValueChange: (String) -> Unit,
    label: String,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    isTv: Boolean = false,
    readOnly: Boolean = false,
    isError: Boolean = false,
    supportingText: String? = null,
    placeholder: String? = null,
    leadingIcon: ImageVector? = null,
    trailingContent: (@Composable () -> Unit)? = null,
    visualTransformation: VisualTransformation = VisualTransformation.None,
    keyboardOptions: KeyboardOptions = KeyboardOptions.Default,
    keyboardActions: KeyboardActions = KeyboardActions.Default,
) {
    OutlinedTextField(
        value = value,
        onValueChange = onValueChange,
        modifier = modifier.heightIn(min = if (isTv) RivuneDimensions.fieldHeightTv else RivuneDimensions.fieldHeight),
        enabled = enabled,
        readOnly = readOnly,
        singleLine = true,
        isError = isError,
        label = { Text(label) },
        placeholder = placeholder?.let { { Text(it) } },
        leadingIcon = leadingIcon?.let { { Icon(it, contentDescription = null) } },
        trailingIcon = trailingContent,
        supportingText = supportingText?.let { { Text(it) } },
        visualTransformation = visualTransformation,
        keyboardOptions = keyboardOptions,
        keyboardActions = keyboardActions,
        shape = RivuneShapes.medium,
        colors = OutlinedTextFieldDefaults.colors(
            focusedTextColor = RivuneText,
            unfocusedTextColor = RivuneText,
            disabledTextColor = RivuneTextMuted,
            focusedBorderColor = MaterialTheme.colorScheme.primary,
            unfocusedBorderColor = RivuneBorder,
            disabledBorderColor = RivuneBorder,
            errorBorderColor = MaterialTheme.colorScheme.error,
            focusedLabelColor = MaterialTheme.colorScheme.primary,
            unfocusedLabelColor = RivuneTextSoft,
            disabledLabelColor = RivuneTextMuted,
            errorLabelColor = MaterialTheme.colorScheme.error,
            focusedContainerColor = RivuneSurfaceRaised,
            unfocusedContainerColor = RivuneSurface.copy(alpha = 0.72f),
            disabledContainerColor = RivuneSurface.copy(alpha = 0.5f),
            cursorColor = MaterialTheme.colorScheme.primary,
            errorCursorColor = MaterialTheme.colorScheme.error,
        ),
    )
}

@Composable
internal fun RivuneBrandMark(size: Dp) {
    val shape = if (size >= RivuneSpacing.display) RivuneShapes.medium else RivuneShapes.small
    Box(
        modifier = Modifier
            .requiredSize(size)
            .clip(shape)
            .background(Color.Black)
            .border(RivuneDimensions.hairline, RivuneBorderStrong, shape)
            .clearAndSetSemantics { },
        contentAlignment = Alignment.Center,
    ) {
        Icon(
            painter = painterResource(R.drawable.rivune_mark),
            contentDescription = null,
            modifier = Modifier.fillMaxSize(),
            tint = Color.Unspecified,
        )
    }
}

@Composable
internal fun RivuneBrandLockup(
    name: String,
    modifier: Modifier = Modifier,
    tagline: String? = null,
    markSize: Dp = RivuneDimensions.touchTarget,
) {
    Row(
        modifier = modifier.semantics(mergeDescendants = true) { },
        verticalAlignment = Alignment.CenterVertically,
    ) {
        RivuneBrandMark(size = markSize)
        Spacer(Modifier.width(RivuneSpacing.sm))
        Column(verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xxs)) {
            Text(name, style = MaterialTheme.typography.titleMedium, letterSpacing = (-0.15).sp)
            tagline?.let {
                Text(
                    text = it,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    style = MaterialTheme.typography.bodySmall,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
    }
}

@Composable
internal fun RivuneScreenHeading(
    eyebrow: String,
    title: String,
    body: String?,
    isTv: Boolean,
    compactTitle: Boolean = false,
    modifier: Modifier = Modifier,
    textAlign: TextAlign = TextAlign.Start,
) {
    Column(
        modifier = modifier,
        horizontalAlignment = if (textAlign == TextAlign.Center) Alignment.CenterHorizontally else Alignment.Start,
    ) {
        Text(
            text = eyebrow.uppercase(),
            color = MaterialTheme.colorScheme.primary,
            modifier = Modifier.fillMaxWidth(),
            style = if (isTv) MaterialTheme.typography.labelLarge else MaterialTheme.typography.labelMedium,
            textAlign = textAlign,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        Spacer(Modifier.height(RivuneSpacing.xs))
        Text(
            text = title,
            modifier = Modifier.fillMaxWidth().semantics { heading() },
            style = when {
                isTv && compactTitle -> MaterialTheme.typography.headlineMedium
                isTv -> MaterialTheme.typography.headlineLarge
                else -> MaterialTheme.typography.headlineMedium
            },
            textAlign = textAlign,
        )
        body?.let {
            Spacer(Modifier.height(if (isTv) RivuneSpacing.md else RivuneSpacing.sm))
            Text(
                text = it,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                style = if (isTv) MaterialTheme.typography.bodyLarge else MaterialTheme.typography.bodyMedium,
                textAlign = textAlign,
            )
        }
    }
}

@Composable
internal fun RivuneFocusSurface(
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    readOnly: Boolean = false,
    selected: Boolean = false,
    isTv: Boolean = false,
    idleColor: Color = RivuneSurface,
    selectedColor: Color = RivuneSurfaceSelected,
    focusedColor: Color = RivuneSurfaceSelected,
    pressedColor: Color = RivuneSurfaceInteractive,
    showSelectionBorder: Boolean = true,
    showFocusBorder: Boolean = true,
    restingBorderColor: Color? = null,
    focusScale: Float = RivuneMotion.focusScale,
    shape: CornerBasedShape = RivuneShapes.medium,
    content: @Composable () -> Unit,
) {
    val motionPolicy = LocalRivuneMotionPolicy.current
    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    val pressed by interaction.collectIsPressedAsState()
    val background by animateColorAsState(
        targetValue = when {
            pressed -> pressedColor
            focused -> focusedColor
            selected -> selectedColor
            else -> idleColor
        },
        animationSpec = motionPolicy.finiteAnimationSpec(RivuneMotion.fast),
        label = "focus-surface-color",
    )
    val scale by animateFloatAsState(
        targetValue = when {
            pressed -> RivuneMotion.pressedScale
            focused && isTv -> focusScale
            else -> 1f
        },
        animationSpec = motionPolicy.finiteAnimationSpec(RivuneMotion.fast),
        label = "focus-surface-scale",
    )
    Surface(
        modifier = modifier
            .zIndex(if (focused) 1f else 0f)
            .graphicsLayer {
                scaleX = scale
                scaleY = scale
            }
            .defaultMinSize(
                minWidth = if (isTv) RivuneDimensions.touchTargetTv else RivuneDimensions.touchTarget,
                minHeight = if (isTv) RivuneDimensions.touchTargetTv else RivuneDimensions.touchTarget,
            )
            .semantics(mergeDescendants = true) {
                if (!readOnly) role = Role.Button
                this.selected = selected
                if (!enabled) disabled()
            }
            .then(
                when {
                    focused && showFocusBorder -> Modifier.border(RivuneDimensions.focusRing, MaterialTheme.colorScheme.primary, shape)
                    selected && showSelectionBorder -> Modifier.border(RivuneDimensions.hairline, RivuneBorderStrong, shape)
                    restingBorderColor != null -> Modifier.border(RivuneDimensions.hairline, restingBorderColor, shape)
                    else -> Modifier
                },
            )
            .clip(shape)
            .then(
                if (readOnly) {
                    Modifier.focusable(enabled = isTv, interactionSource = interaction)
                } else {
                    Modifier.clickable(
                        interactionSource = interaction,
                        indication = null,
                        enabled = enabled,
                        onClick = onClick,
                    )
                },
            ),
        color = background,
        shape = shape,
        content = content,
    )
}

@Composable
internal fun RivuneArtwork(
    model: Any?,
    fallback: String,
    modifier: Modifier = Modifier,
    contentDescription: String? = null,
    contentScale: ContentScale = ContentScale.Crop,
    alignment: Alignment = Alignment.Center,
) {
    val context = LocalContext.current
    val motionPolicy = LocalRivuneMotionPolicy.current
    val request = remember(context, model, motionPolicy.imageCrossfade) {
        model?.let {
            ImageRequest.Builder(context)
                .data(it)
                .apply {
                    if (motionPolicy.imageCrossfade) crossfade(RivuneMotion.normal) else crossfade(false)
                }
                .build()
        }
    }
    var failed by remember(request) { mutableStateOf(false) }
    Box(
        modifier = modifier
            .clipToBounds()
            .background(
                Brush.verticalGradient(
                    colors = listOf(RivuneArtworkPlaceholder, RivuneBackgroundSoft),
                ),
            )
            .clearAndSetSemantics {
                contentDescription?.let { this.contentDescription = it }
            },
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = fallback.take(2).uppercase(),
            color = RivuneTextMuted,
            style = MaterialTheme.typography.headlineMedium,
        )
        if (request != null && !failed) {
            AsyncImage(
                model = request,
                contentDescription = null,
                modifier = Modifier.fillMaxSize(),
                contentScale = contentScale,
                alignment = alignment,
                onError = { failed = true },
            )
        }
    }
}

@Composable
internal fun RivuneSkeleton(
    modifier: Modifier = Modifier,
    shape: CornerBasedShape = RivuneShapes.medium,
) {
    val motionPolicy = LocalRivuneMotionPolicy.current
    val alpha = if (motionPolicy.ambientAnimations) {
        val transition = rememberInfiniteTransition(label = "skeleton")
        val animatedAlpha by transition.animateFloat(
            initialValue = RivuneMotion.skeletonRestAlpha,
            targetValue = RivuneMotion.skeletonPeakAlpha,
            animationSpec = infiniteRepeatable(
                animation = tween(RivuneMotion.ambient),
                repeatMode = RepeatMode.Reverse,
            ),
            label = "skeleton-alpha",
        )
        animatedAlpha
    } else {
        RivuneMotion.skeletonRestAlpha
    }
    Box(
        modifier = modifier.background(
            color = RivuneArtworkPlaceholder.copy(alpha = alpha),
            shape = shape,
        ),
    )
}

@Composable
private fun Modifier.rivuneFocusRing(
    focused: Boolean,
    shape: CornerBasedShape,
    color: Color = MaterialTheme.colorScheme.primary,
): Modifier = if (focused) {
    border(RivuneDimensions.focusRing, color, shape)
} else {
    this
}
