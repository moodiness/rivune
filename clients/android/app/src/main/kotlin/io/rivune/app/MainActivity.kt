package io.rivune.app

import android.animation.ValueAnimator
import android.app.UiModeManager
import android.content.ClipData
import android.content.ClipboardManager
import android.content.ClipDescription
import android.content.Context
import android.content.res.Configuration
import android.graphics.Color
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.os.PersistableBundle
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
    private val diagnosticClipboard by lazy { getSystemService(ClipboardManager::class.java) }
    private val diagnosticClipboardHandler = Handler(Looper.getMainLooper())
    private var diagnosticClipboardReport: String? = null
    private var diagnosticClipboardGeneration = 0L
    private val diagnosticClipboardListener = ClipboardManager.OnPrimaryClipChangedListener {
        val ownedReport = diagnosticClipboardReport ?: return@OnPrimaryClipChangedListener
        if (!hasWindowFocus()) return@OnPrimaryClipChangedListener
        val current = runCatching { diagnosticClipboard.primaryClip }.getOrNull()
        if (current?.itemCount != 1 || current.getItemAt(0).text?.toString() != ownedReport) {
            releaseDiagnosticClipboardOwnership()
        }
    }

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

    internal fun copyDiagnosticReport(report: String): Boolean = try {
        clearOwnedDiagnosticClipboard()
        val clip = ClipData.newPlainText("Rivune diagnostics", report)
        clip.description.extras = PersistableBundle().apply {
            val sensitiveKey = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                ClipDescription.EXTRA_IS_SENSITIVE
            } else {
                DIAGNOSTIC_CLIPBOARD_SENSITIVE_COMPAT_KEY
            }
            putBoolean(sensitiveKey, true)
        }
        diagnosticClipboard.setPrimaryClip(clip)
        diagnosticClipboardReport = report
        val generation = ++diagnosticClipboardGeneration
        diagnosticClipboard.addPrimaryClipChangedListener(diagnosticClipboardListener)
        scheduleDiagnosticClipboardClear(generation, DIAGNOSTIC_CLIPBOARD_LIFETIME_MILLIS)
        true
    } catch (_: SecurityException) {
        clearOwnedDiagnosticClipboard()
        false
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
            clearOwnedDiagnosticClipboard()
            (application as RivuneApplication).appUpdates.activityPaused(this)
        }
        super.onPause()
    }

    override fun onStop() {
        if (!showPlayerRecoveryPreview) viewModel.lockOfflineAccessOnBackground()
        super.onStop()
    }

    override fun onDestroy() {
        clearOwnedDiagnosticClipboard()
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
    private fun clearOwnedDiagnosticClipboard() {
        if (diagnosticClipboardReport == null) return
        try {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
                diagnosticClipboard.clearPrimaryClip()
            } else {
                diagnosticClipboard.setPrimaryClip(ClipData.newPlainText("", ""))
            }
            releaseDiagnosticClipboardOwnership()
        } catch (_: SecurityException) {
            scheduleDiagnosticClipboardClear(diagnosticClipboardGeneration, DIAGNOSTIC_CLIPBOARD_RETRY_MILLIS)
        }
    }

    private fun scheduleDiagnosticClipboardClear(generation: Long, delayMillis: Long) {
        diagnosticClipboardHandler.removeCallbacksAndMessages(null)
        diagnosticClipboardHandler.postDelayed({
            if (diagnosticClipboardReport != null && diagnosticClipboardGeneration == generation) {
                clearOwnedDiagnosticClipboard()
            }
        }, delayMillis)
    }

    private fun releaseDiagnosticClipboardOwnership() {
        diagnosticClipboardReport = null
        diagnosticClipboardGeneration += 1
        diagnosticClipboardHandler.removeCallbacksAndMessages(null)
        runCatching { diagnosticClipboard.removePrimaryClipChangedListener(diagnosticClipboardListener) }
    }
}


private const val DIAGNOSTIC_CLIPBOARD_LIFETIME_MILLIS = 60_000L
private const val DIAGNOSTIC_CLIPBOARD_RETRY_MILLIS = 250L
private const val DIAGNOSTIC_CLIPBOARD_SENSITIVE_COMPAT_KEY = "android.content.extra.IS_SENSITIVE"
