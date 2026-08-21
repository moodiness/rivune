package io.rivune.app

import androidx.media3.ui.PlayerView
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class Media3ScreenOnTest {
    @Test
    fun keepScreenOnTracksPlaybackAndClearsOnRelease() {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        instrumentation.runOnMainSync {
            val playerView = PlayerView(instrumentation.targetContext)

            updateMedia3KeepScreenOn(playerView, isPlaying = true)
            assertTrue(playerView.keepScreenOn)

            updateMedia3KeepScreenOn(playerView, isPlaying = false)
            assertFalse(playerView.keepScreenOn)

            playerView.keepScreenOn = true
            releaseMedia3PlayerView(playerView)
            assertFalse(playerView.keepScreenOn)
        }
    }
}
