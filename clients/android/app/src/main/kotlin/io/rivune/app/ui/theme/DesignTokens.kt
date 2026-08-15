package io.rivune.app.ui.theme

import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.ui.unit.dp

internal object RivuneSpacing {
    val xxs = 4.dp
    val xs = 8.dp
    val sm = 12.dp
    val md = 16.dp
    val lg = 20.dp
    val xl = 24.dp
    val xxl = 32.dp
    val xxxl = 40.dp
    val huge = 48.dp
    val display = 64.dp
}

internal object RivuneDimensions {
    val touchTarget = 48.dp
    val touchTargetTv = 56.dp
    val buttonHeight = 52.dp
    val buttonHeightTv = 60.dp
    val fieldHeight = 52.dp
    val fieldHeightTv = 64.dp
    val iconSmall = 16.dp
    val iconMedium = 20.dp
    val profileAvatarTv = 96.dp
    val profileCardWidthTv = 120.dp
    val contentMax = 560.dp
    val contentMaxTablet = 720.dp
    val contentMaxWide = 1120.dp
    val preferencesMax = 920.dp
    val bottomBar = 56.dp
    val dialogMax = 560.dp
    val sourceDialogMax = 720.dp
    val contentMaxTv = 1440.dp
    val navigationRail = 88.dp
    val navigationRailTv = 104.dp
    val posterWidth = 112.dp
    val posterWidthTv = 200.dp
    val landscapeCardWidth = 184.dp
    val landscapeCardWidthTv = 280.dp
    val focusRing = 2.dp
    val hairline = 1.dp
}

internal object RivuneBreakpoints {
    val medium = 600.dp
    val expanded = 840.dp
    val wide = 1200.dp
}

internal object RivuneShapes {
    val small = RoundedCornerShape(6.dp)
    val medium = RoundedCornerShape(10.dp)
    val large = RoundedCornerShape(16.dp)
    val extraLarge = RoundedCornerShape(20.dp)
    val pill = RoundedCornerShape(percent = 50)
}

internal object RivuneElevation {
    val flat = 0.dp
    val raised = 1.dp
    val overlay = 4.dp
}

internal object RivuneMotion {
    const val fast = 140
    const val normal = 240
    const val slow = 420
    const val ambient = 1_100
    const val successHold = 600L
    const val pressedScale = 0.985f
    const val focusScale = 1.015f
    const val tvButtonFocusScale = 1.06f
    const val skeletonRestAlpha = 0.62f
    const val skeletonPeakAlpha = 0.86f
}
