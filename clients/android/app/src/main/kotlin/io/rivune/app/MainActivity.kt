package io.rivune.app

import android.animation.ValueAnimator
import android.app.UiModeManager
import android.graphics.Color
import android.content.Context
import android.content.res.Configuration
import android.os.Bundle
import androidx.activity.compose.BackHandler
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.viewModels
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import io.rivune.app.ui.theme.RivuneTheme

private const val DebugPlayerRecoveryPreviewAction = "io.rivune.app.action.DEBUG_PLAYER_RECOVERY_PREVIEW"

class MainActivity : ComponentActivity() {
    private val isTelevision: Boolean by lazy {
        val uiModeManager = getSystemService(Context.UI_MODE_SERVICE) as UiModeManager
        uiModeManager.currentModeType == Configuration.UI_MODE_TYPE_TELEVISION
    }

    private var systemAnimationsEnabled by mutableStateOf(true)
    private var showPlayerRecoveryPreview = false

    private val viewModel: RivuneViewModel by viewModels {
        RivuneViewModel.factory(this, isTelevision)
    }

    override fun attachBaseContext(newBase: Context) {
        super.attachBaseContext(contextWithAppLanguage(newBase))
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        systemAnimationsEnabled = ValueAnimator.areAnimatorsEnabled()
        enableEdgeToEdge(
            statusBarStyle = SystemBarStyle.dark(Color.TRANSPARENT),
            navigationBarStyle = SystemBarStyle.dark(Color.TRANSPARENT),
        )
        val updates = (application as RivuneApplication).appUpdates
        showPlayerRecoveryPreview = BuildConfig.DEBUG && intent.action == DebugPlayerRecoveryPreviewAction
        setContent {
            if (showPlayerRecoveryPreview) {
                RivuneTheme {
                    BackHandler(onBack = ::finish)
                    PlayerRecoveryOverlayContent(
                        isTv = isTelevision,
                        failure = PlayerEngineFailure(positionMs = 0L, fallbackEligible = false),
                        onRetry = ::finish,
                        onStartOver = ::finish,
                        onChooseSource = ::finish,
                    )
                }
            } else {
                RivuneRoot(viewModel, updates, this, systemAnimationsEnabled)
            }
        }
        if (!showPlayerRecoveryPreview) updates.checkAutomatically()
    }

    override fun onResume() {
        super.onResume()
        systemAnimationsEnabled = ValueAnimator.areAnimatorsEnabled()
        if (!showPlayerRecoveryPreview) {
            viewModel.refreshExternalPlaybackSupport()
            val updates = (application as RivuneApplication).appUpdates
            updates.activityResumed(this)
            updates.resumeAfterPermission(this)
        }
    }

    override fun onPause() {
        if (!showPlayerRecoveryPreview) {
            (application as RivuneApplication).appUpdates.activityPaused(this)
        }
        super.onPause()
    }

    override fun onStop() {
        if (!showPlayerRecoveryPreview) viewModel.lockOfflineAccessOnBackground()
        super.onStop()
    }

    override fun onDestroy() {
        val terminal = isFinishing
        if (terminal && !showPlayerRecoveryPreview) {
            viewModel.beginTerminalOwnerDestruction()
        }
        try {
            super.onDestroy()
        } finally {
            if (terminal && !showPlayerRecoveryPreview) {
                viewModel.stopPlaybackForTerminalOwner()
            }
        }
    }
}
