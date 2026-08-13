package io.rivune.app

import android.app.UiModeManager
import android.graphics.Color
import android.content.Context
import android.content.res.Configuration
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.viewModels

class MainActivity : ComponentActivity() {
    private val isTelevision: Boolean by lazy {
        val uiModeManager = getSystemService(Context.UI_MODE_SERVICE) as UiModeManager
        uiModeManager.currentModeType == Configuration.UI_MODE_TYPE_TELEVISION
    }

    private val viewModel: RivuneViewModel by viewModels {
        RivuneViewModel.factory(this, isTelevision)
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge(
            statusBarStyle = SystemBarStyle.dark(Color.TRANSPARENT),
            navigationBarStyle = SystemBarStyle.dark(Color.TRANSPARENT),
        )
        setContent {
            RivuneRoot(viewModel)
        }
    }

    override fun onDestroy() {
        val terminal = isFinishing
        if (terminal) {
            viewModel.beginTerminalOwnerDestruction()
        }
        try {
            super.onDestroy()
        } finally {
            if (terminal) {
                viewModel.stopPlaybackForTerminalOwner()
            }
        }
    }
}
