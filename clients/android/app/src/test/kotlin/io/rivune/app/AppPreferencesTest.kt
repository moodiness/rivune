package io.rivune.app

import kotlin.test.Test
import kotlin.test.assertEquals

class AppPreferencesTest {
    @Test
    fun invalidStoredValuesUseSafeDefaults() {
        assertEquals(
            AppPreferencesState(),
            appPreferencesState(
                startupTab = "unknown",
                preferredPlayer = "external",
                preferredPlayerPackage = " ",
                animationPreference = "unknown",
                accentColor = DEFAULT_ACCENT_COLOR,
                frameRateMatching = "unknown",
                videoAspect = "unknown",
                wifiQuality = "unknown",
                mobileQuality = "unknown",
            ),
        )
    }

    @Test
    fun parsesSelectionsAndForcesOpaqueAccent() {
        assertEquals(
            AppPreferencesState(
                startupTab = ViewerTab.CALENDAR,
                preferredPlayer = PreferredPlayer.External("org.example.player"),
                animationPreference = AnimationPreference.REDUCED,
                accentColor = 0xFF123456.toInt(),
                frameRateMatching = FrameRateMatchingPreference.ENABLED,
                videoAspect = VideoAspectPreference.ZOOM,
                wifiQuality = NetworkQualityPreference.BALANCED,
                mobileQuality = NetworkQualityPreference.ECONOMY,
            ),
            appPreferencesState(
                startupTab = "calendar",
                preferredPlayer = "external",
                preferredPlayerPackage = "org.example.player",
                animationPreference = "reduced",
                accentColor = 0x00123456,
                frameRateMatching = "on",
                videoAspect = "zoom",
                wifiQuality = "balanced",
                mobileQuality = "economy",
            ),
        )
    }
}
