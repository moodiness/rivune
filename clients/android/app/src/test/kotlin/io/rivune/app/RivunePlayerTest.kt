package io.rivune.app

import androidx.media3.common.C
import androidx.media3.common.MimeTypes
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

class RivunePlayerTest {
    @Test
    fun adaptiveProtocolsSelectMediaSourceMimeType() {
        assertEquals(MimeTypes.APPLICATION_M3U8, playbackMimeType("hls"))
        assertEquals(MimeTypes.APPLICATION_MPD, playbackMimeType("DASH"))
        assertNull(playbackMimeType("http"))
    }

    @Test
    fun onlyServerSelectedSubtitleIsDefault() {
        assertEquals(0, subtitleSelectionFlags(selected = false))
        assertEquals(C.SELECTION_FLAG_DEFAULT, subtitleSelectionFlags(selected = true))
    }

    @Test
    fun subtitleMimeTypeUsesOnlyPublishedAssetExtension() {
        assertEquals(MimeTypes.TEXT_VTT, subtitleMimeType("https://media.example/assets/subtitle-1.vtt?token=opaque"))
        assertEquals(MimeTypes.APPLICATION_TTML, subtitleMimeType("https://media.example/assets/subtitle-2.ttml?token=opaque"))
        assertNull(subtitleMimeType("https://media.example/assets/subtitle-3?token=opaque"))
    }

    @Test
    fun advertisedDeviceCapabilitiesHaveConservativeGlobalBounds() {
        val capabilities = DevicePlaybackCapabilities.value
        val maximumHeight = assertNotNull(capabilities.maximumHeight)

        assertTrue(capabilities.mediaProfiles.orEmpty().isNotEmpty())
        assertTrue(capabilities.mediaProfiles.orEmpty().all { it.maximumVideoBitDepth == 8 })
        assertNotNull(capabilities.maximumVideoBitrateKbps)
        assertTrue(maximumHeight in 144..4320)
        assertEquals(2, capabilities.maximumAudioChannels)
    }

    @Test
    fun playerDisplayPreferencesMapToMedia3Modes() {
        assertEquals(C.VIDEO_CHANGE_FRAME_RATE_STRATEGY_ONLY_IF_SEAMLESS, FrameRateMatchingPreference.SYSTEM.media3Strategy(36))
        assertEquals(C.VIDEO_CHANGE_FRAME_RATE_STRATEGY_OFF, FrameRateMatchingPreference.ENABLED.media3Strategy(36))
        assertEquals(C.VIDEO_CHANGE_FRAME_RATE_STRATEGY_ONLY_IF_SEAMLESS, FrameRateMatchingPreference.ENABLED.media3Strategy(30))
        assertEquals(C.VIDEO_CHANGE_FRAME_RATE_STRATEGY_OFF, FrameRateMatchingPreference.DISABLED.media3Strategy(36))
        assertEquals(androidx.media3.ui.AspectRatioFrameLayout.RESIZE_MODE_FIT, VideoAspectPreference.FIT.resizeMode())
        assertEquals(androidx.media3.ui.AspectRatioFrameLayout.RESIZE_MODE_FILL, VideoAspectPreference.FILL.resizeMode())
        assertEquals(androidx.media3.ui.AspectRatioFrameLayout.RESIZE_MODE_ZOOM, VideoAspectPreference.ZOOM.resizeMode())
    }

    @Test
    fun networkQualityPresetsClampDeviceCapabilities() {
        val device = DevicePlaybackCapabilities.value
        val economy = device.withQualityLimit(
            playbackQualityLimit(NetworkQualityPreference.ECONOMY, PlaybackNetwork.UNMETERED),
        )
        val balanced = device.withQualityLimit(
            playbackQualityLimit(NetworkQualityPreference.BALANCED, PlaybackNetwork.UNMETERED),
        )
        val automaticMobile = device.withQualityLimit(
            playbackQualityLimit(NetworkQualityPreference.AUTOMATIC, PlaybackNetwork.METERED),
        )

        assertTrue(assertNotNull(economy.maximumHeight) <= 480)
        assertTrue(assertNotNull(economy.maximumVideoBitrateKbps) <= 2_000)
        assertTrue(assertNotNull(balanced.maximumHeight) <= 1_080)
        assertTrue(assertNotNull(balanced.maximumVideoBitrateKbps) <= 8_000)
        assertTrue(assertNotNull(automaticMobile.maximumHeight) <= 720)
        assertTrue(assertNotNull(automaticMobile.maximumVideoBitrateKbps) <= 5_000)
        assertEquals(device, device.withQualityLimit(playbackQualityLimit(NetworkQualityPreference.MAXIMUM, PlaybackNetwork.METERED)))
        assertEquals(device, device.withQualityLimit(playbackQualityLimit(NetworkQualityPreference.AUTOMATIC, PlaybackNetwork.UNMETERED)))
    }

    @Test
    fun mediaProfilesRestrictAudioCodecsToContainerExtractors() {
        val available = listOf("aac", "ac3", "eac3", "opus", "vorbis", "mp3")

        assertEquals(listOf("aac", "ac3", "eac3"), profileAudioCodecs("mp4", available))
        assertEquals(available, profileAudioCodecs("matroska", available))
        assertEquals(listOf("opus", "vorbis"), profileAudioCodecs("webm", available))
        assertEquals(listOf("aac", "ac3", "eac3", "mp3"), profileAudioCodecs("mpegts", available))
        assertEquals(emptyList(), profileAudioCodecs("unknown", available))
    }
}
