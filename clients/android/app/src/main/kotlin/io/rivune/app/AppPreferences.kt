package io.rivune.app

import android.content.Context
import android.content.SharedPreferences
import androidx.core.content.edit
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

internal const val DEFAULT_ACCENT_COLOR = 0xFF77A7FF.toInt()

internal enum class AnimationPreference(val preferenceValue: String) {
    SYSTEM("system"),
    FULL("full"),
    REDUCED("reduced"),
    ;

    companion object {
        fun fromPreference(value: String?): AnimationPreference =
            entries.firstOrNull { it.preferenceValue == value } ?: SYSTEM
    }
}

internal enum class FrameRateMatchingPreference(val preferenceValue: String) {
    SYSTEM("system"),
    ENABLED("on"),
    DISABLED("off"),
    ;

    companion object {
        fun fromPreference(value: String?): FrameRateMatchingPreference =
            entries.firstOrNull { it.preferenceValue == value } ?: SYSTEM
    }
}

internal enum class VideoAspectPreference(val preferenceValue: String) {
    FIT("fit"),
    FILL("fill"),
    ZOOM("zoom"),
    ;

    companion object {
        fun fromPreference(value: String?): VideoAspectPreference =
            entries.firstOrNull { it.preferenceValue == value } ?: FIT
    }
}

internal enum class NetworkQualityPreference(val preferenceValue: String) {
    AUTOMATIC("automatic"),
    ECONOMY("economy"),
    BALANCED("balanced"),
    MAXIMUM("maximum"),
    ;

    companion object {
        fun fromPreference(value: String?): NetworkQualityPreference =
            entries.firstOrNull { it.preferenceValue == value } ?: AUTOMATIC
    }
}

internal sealed interface PreferredPlayer {
    data object Ask : PreferredPlayer
    data object Rivune : PreferredPlayer
    data class External(val packageName: String) : PreferredPlayer
}

internal fun EmbeddedPlayerPreference.toPreferenceValue(): String = name.lowercase()

internal fun embeddedPlayerPreference(value: String?): EmbeddedPlayerPreference =
    EmbeddedPlayerPreference.entries.firstOrNull { it.name.equals(value, ignoreCase = true) }
        ?: EmbeddedPlayerPreference.AUTOMATIC

internal data class AppPreferencesState(
    val startupTab: ViewerTab = ViewerTab.HOME,
    val preferredPlayer: PreferredPlayer = PreferredPlayer.Ask,
    val embeddedPlayerPreference: EmbeddedPlayerPreference = EmbeddedPlayerPreference.AUTOMATIC,
    val animationPreference: AnimationPreference = AnimationPreference.SYSTEM,
    val accentColor: Int = DEFAULT_ACCENT_COLOR,
    val frameRateMatching: FrameRateMatchingPreference = FrameRateMatchingPreference.SYSTEM,
    val videoAspect: VideoAspectPreference = VideoAspectPreference.FIT,
    val wifiQuality: NetworkQualityPreference = NetworkQualityPreference.AUTOMATIC,
    val mobileQuality: NetworkQualityPreference = NetworkQualityPreference.AUTOMATIC,
    val autoSkipIntro: Boolean = false,
    val autoSkipRecap: Boolean = false,
    val autoSkipOutro: Boolean = false,
)

internal fun interface AppPreferencesReader {
    fun snapshot(): AppPreferencesState
}

internal fun appPreferencesState(
    startupTab: String?,
    preferredPlayer: String?,
    preferredPlayerPackage: String?,
    animationPreference: String?,
    accentColor: Int,
    frameRateMatching: String? = null,
    videoAspect: String? = null,
    wifiQuality: String? = null,
    mobileQuality: String? = null,
    autoSkipIntro: Boolean = false,
    autoSkipRecap: Boolean = false,
    autoSkipOutro: Boolean = false,
    embeddedPlayerPreference: String? = null,
): AppPreferencesState = AppPreferencesState(
    startupTab = startupTab
        ?.let { stored -> ViewerTab.entries.firstOrNull { it.name.equals(stored, ignoreCase = true) } }
        ?: ViewerTab.HOME,
    preferredPlayer = when (preferredPlayer) {
        "rivune" -> PreferredPlayer.Rivune
        "external" -> preferredPlayerPackage
            ?.trim()
            ?.takeIf { it.length in 1..255 }
            ?.let(PreferredPlayer::External)
            ?: PreferredPlayer.Ask
        else -> PreferredPlayer.Ask
    },
    embeddedPlayerPreference = embeddedPlayerPreference(embeddedPlayerPreference),
    animationPreference = AnimationPreference.fromPreference(animationPreference),
    accentColor = opaque(accentColor),
    frameRateMatching = FrameRateMatchingPreference.fromPreference(frameRateMatching),
    videoAspect = VideoAspectPreference.fromPreference(videoAspect),
    wifiQuality = NetworkQualityPreference.fromPreference(wifiQuality),
    mobileQuality = NetworkQualityPreference.fromPreference(mobileQuality),
    autoSkipIntro = autoSkipIntro,
    autoSkipRecap = autoSkipRecap,
    autoSkipOutro = autoSkipOutro,
)

internal class AppPreferencesStore(context: Context) : AppPreferencesReader, SharedPreferences.OnSharedPreferenceChangeListener {
    private val preferences = context.getSharedPreferences(PREFERENCES_NAME, Context.MODE_PRIVATE)
    private val mutableState = MutableStateFlow(readState())
    val state: StateFlow<AppPreferencesState> = mutableState.asStateFlow()

    init {
        preferences.registerOnSharedPreferenceChangeListener(this)
    }

    override fun snapshot(): AppPreferencesState = mutableState.value

    fun setStartupTab(value: ViewerTab) {
        preferences.edit { putString(STARTUP_TAB_KEY, value.name.lowercase()) }
    }

    fun setPreferredPlayer(value: PreferredPlayer) {
        preferences.edit {
            when (value) {
                PreferredPlayer.Ask -> {
                    putString(PREFERRED_PLAYER_KEY, PLAYER_ASK)
                    remove(PREFERRED_PLAYER_PACKAGE_KEY)
                }
                PreferredPlayer.Rivune -> {
                    putString(PREFERRED_PLAYER_KEY, PLAYER_RIVUNE)
                    remove(PREFERRED_PLAYER_PACKAGE_KEY)
                }
                is PreferredPlayer.External -> {
                    putString(PREFERRED_PLAYER_KEY, PLAYER_EXTERNAL)
                    putString(PREFERRED_PLAYER_PACKAGE_KEY, value.packageName)
                }
            }
        }
    }

    fun setPreferredEmbeddedPlayer(value: EmbeddedPlayerPreference) {
        preferences.edit {
            putString(PREFERRED_PLAYER_KEY, PLAYER_RIVUNE)
            remove(PREFERRED_PLAYER_PACKAGE_KEY)
            putString(EMBEDDED_PLAYER_KEY, value.toPreferenceValue())
        }
    }

    fun setAnimationPreference(value: AnimationPreference) {
        preferences.edit { putString(ANIMATION_KEY, value.preferenceValue) }
    }

    fun setAccentColor(value: Int) {
        preferences.edit { putInt(ACCENT_COLOR_KEY, opaque(value)) }
    }

    fun setFrameRateMatching(value: FrameRateMatchingPreference) {
        preferences.edit { putString(FRAME_RATE_MATCHING_KEY, value.preferenceValue) }
    }

    fun setVideoAspect(value: VideoAspectPreference) {
        preferences.edit { putString(VIDEO_ASPECT_KEY, value.preferenceValue) }
    }

    fun setWifiQuality(value: NetworkQualityPreference) {
        preferences.edit { putString(WIFI_QUALITY_KEY, value.preferenceValue) }
    }

    fun setMobileQuality(value: NetworkQualityPreference) {
        preferences.edit { putString(MOBILE_QUALITY_KEY, value.preferenceValue) }
    }

    fun setAutoSkipIntro(value: Boolean) {
        preferences.edit { putBoolean(AUTO_SKIP_INTRO_KEY, value) }
    }

    fun setAutoSkipRecap(value: Boolean) {
        preferences.edit { putBoolean(AUTO_SKIP_RECAP_KEY, value) }
    }

    fun setAutoSkipOutro(value: Boolean) {
        preferences.edit { putBoolean(AUTO_SKIP_OUTRO_KEY, value) }
    }

    override fun onSharedPreferenceChanged(sharedPreferences: SharedPreferences, key: String?) {
        if (key in OBSERVED_KEYS) mutableState.value = readState()
    }

    private fun readState(): AppPreferencesState = appPreferencesState(
        startupTab = preferences.getString(STARTUP_TAB_KEY, null),
        preferredPlayer = preferences.getString(PREFERRED_PLAYER_KEY, null),
        preferredPlayerPackage = preferences.getString(PREFERRED_PLAYER_PACKAGE_KEY, null),
        animationPreference = preferences.getString(ANIMATION_KEY, null),
        embeddedPlayerPreference = preferences.getString(EMBEDDED_PLAYER_KEY, null),
        accentColor = preferences.getInt(ACCENT_COLOR_KEY, DEFAULT_ACCENT_COLOR),
        frameRateMatching = preferences.getString(FRAME_RATE_MATCHING_KEY, null),
        videoAspect = preferences.getString(VIDEO_ASPECT_KEY, null),
        wifiQuality = preferences.getString(WIFI_QUALITY_KEY, null),
        mobileQuality = preferences.getString(MOBILE_QUALITY_KEY, null),
        autoSkipIntro = preferences.getBoolean(AUTO_SKIP_INTRO_KEY, false),
        autoSkipRecap = preferences.getBoolean(AUTO_SKIP_RECAP_KEY, false),
        autoSkipOutro = preferences.getBoolean(AUTO_SKIP_OUTRO_KEY, false),
    )

    private companion object {
        const val PREFERENCES_NAME = "app_preferences"
        const val STARTUP_TAB_KEY = "startup_tab"
        const val PREFERRED_PLAYER_KEY = "preferred_player"
        const val PREFERRED_PLAYER_PACKAGE_KEY = "preferred_player_package"
        const val EMBEDDED_PLAYER_KEY = "embedded_player"
        const val ANIMATION_KEY = "interface_animations"
        const val ACCENT_COLOR_KEY = "accent_color"
        const val FRAME_RATE_MATCHING_KEY = "frame_rate_matching"
        const val VIDEO_ASPECT_KEY = "video_aspect"
        const val WIFI_QUALITY_KEY = "wifi_quality"
        const val MOBILE_QUALITY_KEY = "mobile_quality"
        const val AUTO_SKIP_INTRO_KEY = "auto_skip_intro"
        const val AUTO_SKIP_RECAP_KEY = "auto_skip_recap"
        const val AUTO_SKIP_OUTRO_KEY = "auto_skip_outro"
        const val PLAYER_ASK = "ask"
        const val PLAYER_RIVUNE = "rivune"
        const val PLAYER_EXTERNAL = "external"
        val OBSERVED_KEYS = setOf(
            STARTUP_TAB_KEY,
            PREFERRED_PLAYER_KEY,
            PREFERRED_PLAYER_PACKAGE_KEY,
            EMBEDDED_PLAYER_KEY,
            ANIMATION_KEY,
            ACCENT_COLOR_KEY,
            FRAME_RATE_MATCHING_KEY,
            VIDEO_ASPECT_KEY,
            WIFI_QUALITY_KEY,
            MOBILE_QUALITY_KEY,
            AUTO_SKIP_INTRO_KEY,
            AUTO_SKIP_RECAP_KEY,
            AUTO_SKIP_OUTRO_KEY,
        )
    }
}

private fun opaque(color: Int): Int = color or 0xFF000000.toInt()
