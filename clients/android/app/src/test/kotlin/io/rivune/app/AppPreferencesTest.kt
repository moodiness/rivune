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
                embeddedPlayerPreference = "unsupported",
            ),
        )
    }

    @Test
    fun parsesSelectionsAndForcesOpaqueAccent() {
        assertEquals(
            AppPreferencesState(
                startupTab = ViewerTab.CALENDAR,
                preferredPlayer = PreferredPlayer.External("org.example.player"),
                embeddedPlayerPreference = EmbeddedPlayerPreference.MPV,
                animationPreference = AnimationPreference.REDUCED,
                accentColor = 0xFF123456.toInt(),
                frameRateMatching = FrameRateMatchingPreference.ENABLED,
                videoAspect = VideoAspectPreference.ZOOM,
                wifiQuality = NetworkQualityPreference.BALANCED,
                mobileQuality = NetworkQualityPreference.ECONOMY,
                automaticallyShowStreams = false,
                autoSkipIntro = true,
                autoSkipRecap = false,
                autoSkipOutro = true,
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
                automaticallyShowStreams = false,
                autoSkipIntro = true,
                autoSkipRecap = false,
                autoSkipOutro = true,
                embeddedPlayerPreference = "mpv",
            ),
        )
    }

    @Test
    fun embeddedPlayerPreferenceRoundTripsStableValues() {
        EmbeddedPlayerPreference.entries.forEach { preference ->
            assertEquals(preference, embeddedPlayerPreference(preference.toPreferenceValue()))
        }
        assertEquals(EmbeddedPlayerPreference.AUTOMATIC, embeddedPlayerPreference(null))
        assertEquals(EmbeddedPlayerPreference.AUTOMATIC, embeddedPlayerPreference(""))
    }

    @Test
    fun legacyRivuneSelectionRemainsValidAndDefaultsEngineToAutomatic() {
        val state = appPreferencesState(
            startupTab = null,
            preferredPlayer = "rivune",
            preferredPlayerPackage = null,
            animationPreference = null,
            accentColor = DEFAULT_ACCENT_COLOR,
        )
        assertEquals(PreferredPlayer.Rivune, state.preferredPlayer)
        assertEquals(EmbeddedPlayerPreference.AUTOMATIC, state.embeddedPlayerPreference)
    }
}
