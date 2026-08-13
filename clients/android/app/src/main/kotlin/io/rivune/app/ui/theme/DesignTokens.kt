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
    val buttonHeight = 54.dp
    val buttonHeightTv = 64.dp
    val fieldHeight = 60.dp
    val fieldHeightTv = 68.dp
    val contentMax = 560.dp
    val contentMaxTablet = 720.dp
    val contentMaxWide = 1120.dp
    val preferencesMax = 920.dp
    val navigationRail = 88.dp
    val navigationRailTv = 104.dp
    val posterWidth = 152.dp
    val posterWidthTv = 200.dp
    val landscapeCardWidth = 224.dp
    val landscapeCardWidthTv = 280.dp
}

internal object RivuneBreakpoints {
    val compact = 720.dp
    val expanded = 840.dp
    val wide = 1200.dp
}

internal object RivuneShapes {
    val small = RoundedCornerShape(10.dp)
    val medium = RoundedCornerShape(16.dp)
    val large = RoundedCornerShape(24.dp)
    val extraLarge = RoundedCornerShape(32.dp)
    val pill = RoundedCornerShape(percent = 50)
}

internal object RivuneElevation {
    val flat = 0.dp
    val raised = 3.dp
    val overlay = 8.dp
}

internal object RivuneMotion {
    const val fast = 120
    const val normal = 220
    const val slow = 360
    const val successHold = 550L
}
