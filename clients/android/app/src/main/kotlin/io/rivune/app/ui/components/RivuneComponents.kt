package io.rivune.app.ui.components

import android.animation.ValueAnimator
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
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.interaction.collectIsPressedAsState
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
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import coil.compose.AsyncImage
import coil.request.ImageRequest
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import io.rivune.app.ui.theme.RivuneAccent
import io.rivune.app.ui.theme.RivuneAccentPressed
import io.rivune.app.ui.theme.RivuneBorder
import io.rivune.app.ui.theme.RivuneBorderStrong
import io.rivune.app.ui.theme.RivuneDanger
import io.rivune.app.ui.theme.RivuneDimensions
import io.rivune.app.ui.theme.RivuneMotion
import io.rivune.app.ui.theme.RivuneShapes
import io.rivune.app.ui.theme.RivuneSpacing
import io.rivune.app.ui.theme.RivuneSurface
import io.rivune.app.ui.theme.RivuneSurfaceInteractive
import io.rivune.app.ui.theme.RivuneSurfaceSelected
import io.rivune.app.ui.theme.RivuneText
import io.rivune.app.ui.theme.RivuneTextMuted
import io.rivune.app.ui.theme.RivuneTextSoft

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
) {
    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    val pressed by interaction.collectIsPressedAsState()
    val container by animateColorAsState(
        targetValue = if (pressed) RivuneAccentPressed else RivuneAccent,
        animationSpec = tween(RivuneMotion.fast),
        label = "primary-button-color",
    )
    Button(
        onClick = onClick,
        modifier = modifier
            .heightIn(min = if (isTv) RivuneDimensions.buttonHeightTv else RivuneDimensions.buttonHeight)
            .rivuneFocusRing(focused, RivuneShapes.pill)
            .semantics {
                if (loading) stateDescription = loadingDescription
            },
        enabled = enabled && !loading,
        interactionSource = interaction,
        shape = RivuneShapes.pill,
        colors = ButtonDefaults.buttonColors(
            containerColor = container,
            contentColor = MaterialTheme.colorScheme.onPrimary,
            disabledContainerColor = RivuneSurfaceInteractive,
            disabledContentColor = RivuneTextMuted,
        ),
        contentPadding = ButtonDefaults.ContentPadding,
    ) {
        AnimatedContent(
            targetState = loading,
            transitionSpec = {
                fadeIn(tween(RivuneMotion.fast)) togetherWith fadeOut(tween(RivuneMotion.fast))
            },
            label = "primary-button-content",
        ) { busy ->
            Row(
                horizontalArrangement = Arrangement.Center,
                verticalAlignment = Alignment.CenterVertically,
            ) {
                if (busy) {
                    CircularProgressIndicator(
                        modifier = Modifier.size(17.dp),
                        strokeWidth = 2.dp,
                        color = MaterialTheme.colorScheme.onPrimary,
                    )
                } else if (icon != null) {
                    Icon(icon, contentDescription = null, modifier = Modifier.size(19.dp))
                }
                if (busy || icon != null) Spacer(Modifier.width(RivuneSpacing.sm))
                Text(label, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
        }
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
) {
    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    val pressed by interaction.collectIsPressedAsState()
    val container by animateColorAsState(
        targetValue = if (pressed) RivuneSurfaceInteractive else Color.Transparent,
        animationSpec = tween(RivuneMotion.fast),
        label = "secondary-button-color",
    )
    OutlinedButton(
        onClick = onClick,
        modifier = modifier
            .heightIn(min = if (isTv) RivuneDimensions.buttonHeightTv else RivuneDimensions.buttonHeight)
            .rivuneFocusRing(focused, RivuneShapes.pill),
        enabled = enabled && !loading,
        interactionSource = interaction,
        shape = RivuneShapes.pill,
        border = BorderStroke(1.dp, if (focused) MaterialTheme.colorScheme.primary else RivuneBorderStrong),
        colors = ButtonDefaults.outlinedButtonColors(
            containerColor = container,
            contentColor = MaterialTheme.colorScheme.primary,
            disabledContentColor = RivuneTextMuted,
        ),
    ) {
        if (loading) {
            CircularProgressIndicator(
                modifier = Modifier.size(17.dp),
                strokeWidth = 2.dp,
                color = MaterialTheme.colorScheme.primary,
            )
        } else if (icon != null) {
            Icon(icon, contentDescription = null, modifier = Modifier.size(19.dp))
        }
        if (loading || icon != null) Spacer(Modifier.width(RivuneSpacing.sm))
        Text(label, maxLines = 1, overflow = TextOverflow.Ellipsis)
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
) {
    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    TextButton(
        onClick = onClick,
        modifier = modifier
            .heightIn(min = if (isTv) RivuneDimensions.touchTargetTv else RivuneDimensions.touchTarget)
            .rivuneFocusRing(focused, RivuneShapes.pill),
        enabled = enabled,
        interactionSource = interaction,
        shape = RivuneShapes.pill,
        colors = ButtonDefaults.textButtonColors(
            contentColor = if (destructive) RivuneDanger else MaterialTheme.colorScheme.primary,
            disabledContentColor = RivuneTextMuted,
        ),
    ) {
        if (icon != null) {
            Icon(icon, contentDescription = null, modifier = Modifier.size(18.dp))
            Spacer(Modifier.width(RivuneSpacing.xs))
        }
        Text(label, maxLines = 1, overflow = TextOverflow.Ellipsis)
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
            unfocusedBorderColor = RivuneBorderStrong,
            disabledBorderColor = RivuneBorder,
            errorBorderColor = MaterialTheme.colorScheme.error,
            focusedLabelColor = MaterialTheme.colorScheme.primary,
            unfocusedLabelColor = RivuneTextSoft,
            disabledLabelColor = RivuneTextMuted,
            errorLabelColor = MaterialTheme.colorScheme.error,
            focusedContainerColor = RivuneSurfaceInteractive,
            unfocusedContainerColor = RivuneSurface,
            disabledContainerColor = RivuneSurface,
            cursorColor = MaterialTheme.colorScheme.primary,
            errorCursorColor = MaterialTheme.colorScheme.error,
        ),
    )
}

@Composable
internal fun RivuneBrandMark(size: Dp, mark: String) {
    Box(
        modifier = Modifier
            .requiredSize(size)
            .clip(if (size >= 64.dp) RivuneShapes.large else RivuneShapes.medium)
            .background(MaterialTheme.colorScheme.primary)
            .clearAndSetSemantics { },
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = mark,
            color = MaterialTheme.colorScheme.onPrimary,
            fontWeight = FontWeight.Black,
            style = if (size >= 64.dp) MaterialTheme.typography.headlineLarge else MaterialTheme.typography.titleLarge,
        )
    }
}

@Composable
internal fun RivuneBrandLockup(
    name: String,
    mark: String,
    modifier: Modifier = Modifier,
    tagline: String? = null,
    markSize: Dp = 48.dp,
) {
    Row(
        modifier = modifier.semantics(mergeDescendants = true) { },
        verticalAlignment = Alignment.CenterVertically,
    ) {
        RivuneBrandMark(size = markSize, mark = mark)
        Spacer(Modifier.width(RivuneSpacing.md))
        Column(verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xxs)) {
            Text(name, style = MaterialTheme.typography.titleLarge)
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
    body: String,
    isTv: Boolean,
    modifier: Modifier = Modifier,
) {
    Column(modifier = modifier) {
        Text(
            text = eyebrow.uppercase(),
            color = MaterialTheme.colorScheme.primary,
            style = MaterialTheme.typography.labelMedium,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        Spacer(Modifier.height(RivuneSpacing.sm))
        Text(
            text = title,
            modifier = Modifier.semantics { heading() },
            style = if (isTv) MaterialTheme.typography.displayLarge else MaterialTheme.typography.headlineLarge,
        )
        Spacer(Modifier.height(RivuneSpacing.md))
        Text(
            text = body,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            style = if (isTv) MaterialTheme.typography.bodyLarge else MaterialTheme.typography.bodyMedium,
        )
    }
}

@Composable
internal fun RivuneFocusSurface(
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    selected: Boolean = false,
    isTv: Boolean = false,
    shape: CornerBasedShape = RivuneShapes.medium,
    content: @Composable () -> Unit,
) {
    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    val pressed by interaction.collectIsPressedAsState()
    val background by animateColorAsState(
        targetValue = when {
            focused -> RivuneSurfaceSelected
            pressed -> RivuneSurfaceInteractive
            else -> RivuneSurface
        },
        animationSpec = tween(RivuneMotion.fast),
        label = "focus-surface-color",
    )
    val scale by animateFloatAsState(
        targetValue = when {
            pressed -> 0.985f
            focused && isTv -> 1.018f
            else -> 1f
        },
        animationSpec = tween(RivuneMotion.fast),
        label = "focus-surface-scale",
    )
    Surface(
        modifier = modifier
            .graphicsLayer {
                scaleX = scale
                scaleY = scale
            }
            .defaultMinSize(
                minWidth = if (isTv) RivuneDimensions.touchTargetTv else RivuneDimensions.touchTarget,
                minHeight = if (isTv) RivuneDimensions.touchTargetTv else RivuneDimensions.touchTarget,
            )
            .semantics(mergeDescendants = true) {
                role = Role.Button
                this.selected = selected
            }
            .border(
                width = if (focused) 2.dp else if (selected) 1.dp else 0.dp,
                color = if (focused) MaterialTheme.colorScheme.primary else RivuneBorderStrong,
                shape = shape,
            )
            .clip(shape)
            .clickable(
                interactionSource = interaction,
                indication = null,
                enabled = enabled,
                onClick = onClick,
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
) {
    val context = LocalContext.current
    val request = remember(context, model) {
        model?.let {
            ImageRequest.Builder(context)
                .data(it)
                .crossfade(RivuneMotion.normal)
                .build()
        }
    }
    var failed by remember(request) { mutableStateOf(false) }
    Box(
        modifier = modifier
            .clipToBounds()
            .background(RivuneSurfaceInteractive),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = fallback.take(2).uppercase(),
            color = MaterialTheme.colorScheme.primary,
            style = MaterialTheme.typography.headlineLarge,
        )
        if (request != null && !failed) {
            AsyncImage(
                model = request,
                contentDescription = contentDescription,
                modifier = Modifier.fillMaxSize(),
                contentScale = contentScale,
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
    val animationsEnabled = remember { ValueAnimator.areAnimatorsEnabled() }
    val transition = rememberInfiniteTransition(label = "skeleton")
    val animatedAlpha by transition.animateFloat(
        initialValue = 0.52f,
        targetValue = 0.82f,
        animationSpec = infiniteRepeatable(
            animation = tween(900),
            repeatMode = RepeatMode.Reverse,
        ),
        label = "skeleton-alpha",
    )
    Box(
        modifier = modifier.background(
            color = RivuneSurfaceInteractive.copy(alpha = if (animationsEnabled) animatedAlpha else 0.68f),
            shape = shape,
        ),
    )
}

@Composable
private fun Modifier.rivuneFocusRing(focused: Boolean, shape: CornerBasedShape): Modifier = if (focused) {
    border(2.dp, MaterialTheme.colorScheme.primary, shape)
} else {
    this
}
