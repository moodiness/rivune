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
    val localQuality: NetworkQualityPreference = NetworkQualityPreference.AUTOMATIC,
    val remoteWifiQuality: NetworkQualityPreference = NetworkQualityPreference.AUTOMATIC,
    val mobileQuality: NetworkQualityPreference = NetworkQualityPreference.AUTOMATIC,
    val offlineQuotaBytes: Long = 20L * 1024L * 1024L * 1024L,
    val offlineExpirationDays: Int = 30,
    val downloadOnMobile: Boolean = false,
    val automaticallyShowStreams: Boolean = true,
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
    localQuality: String? = null,
    remoteWifiQuality: String? = null,
    mobileQuality: String? = null,
    offlineQuotaBytes: Long = 20L * 1024L * 1024L * 1024L,
    offlineExpirationDays: Int = 30,
    downloadOnMobile: Boolean = false,
    automaticallyShowStreams: Boolean = true,
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
    localQuality = NetworkQualityPreference.fromPreference(localQuality),
    remoteWifiQuality = NetworkQualityPreference.fromPreference(remoteWifiQuality),
    mobileQuality = NetworkQualityPreference.fromPreference(mobileQuality),
    offlineQuotaBytes = offlineQuotaBytes.coerceAtLeast(1L),
    offlineExpirationDays = offlineExpirationDays.coerceAtLeast(0),
    downloadOnMobile = downloadOnMobile,
    automaticallyShowStreams = automaticallyShowStreams,
    autoSkipIntro = autoSkipIntro,
    autoSkipRecap = autoSkipRecap,
    autoSkipOutro = autoSkipOutro,
)

internal class AppPreferencesStore(context: Context) : AppPreferencesReader, SharedPreferences.OnSharedPreferenceChangeListener {
    private val preferences = context.getSharedPreferences(PREFERENCES_NAME, Context.MODE_PRIVATE)
    private val mutableState = MutableStateFlow(migrateAndReadState())
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

    fun setLocalQuality(value: NetworkQualityPreference) {
        preferences.edit { putString(LOCAL_QUALITY_KEY, value.preferenceValue) }
    }

    fun setRemoteWifiQuality(value: NetworkQualityPreference) {
        preferences.edit { putString(REMOTE_WIFI_QUALITY_KEY, value.preferenceValue) }
    }

    fun setMobileQuality(value: NetworkQualityPreference) {
        preferences.edit { putString(MOBILE_QUALITY_KEY, value.preferenceValue) }
    }

    fun setOfflineQuotaBytes(value: Long) {
        preferences.edit { putLong(OFFLINE_QUOTA_BYTES_KEY, value.coerceAtLeast(1L)) }
    }

    fun setOfflineExpirationDays(value: Int) {
        preferences.edit { putInt(OFFLINE_EXPIRATION_DAYS_KEY, value.coerceAtLeast(0)) }
    }

    fun setDownloadOnMobile(value: Boolean) {
        preferences.edit { putBoolean(DOWNLOAD_ON_MOBILE_KEY, value) }
    }
    fun setAutomaticallyShowStreams(value: Boolean) {
        preferences.edit { putBoolean(AUTO_SHOW_STREAMS_KEY, value) }
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

    private fun migrateAndReadState(): AppPreferencesState {
        if (preferences.contains(LEGACY_WIFI_QUALITY_KEY)) {
            val legacy = preferences.getString(LEGACY_WIFI_QUALITY_KEY, null)
            check(preferences.edit()
                .apply {
                    if (!preferences.contains(LOCAL_QUALITY_KEY)) putString(LOCAL_QUALITY_KEY, legacy)
                    if (!preferences.contains(REMOTE_WIFI_QUALITY_KEY)) putString(REMOTE_WIFI_QUALITY_KEY, legacy)
                    remove(LEGACY_WIFI_QUALITY_KEY)
                }
                .commit()) { "Could not migrate network quality preferences" }
        }
        return readState()
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
        localQuality = preferences.getString(LOCAL_QUALITY_KEY, null),
        remoteWifiQuality = preferences.getString(REMOTE_WIFI_QUALITY_KEY, null),
        mobileQuality = preferences.getString(MOBILE_QUALITY_KEY, null),
        offlineQuotaBytes = preferences.getLong(OFFLINE_QUOTA_BYTES_KEY, 20L * 1024L * 1024L * 1024L),
        offlineExpirationDays = preferences.getInt(OFFLINE_EXPIRATION_DAYS_KEY, 30),
        downloadOnMobile = preferences.getBoolean(DOWNLOAD_ON_MOBILE_KEY, false),
        automaticallyShowStreams = preferences.getBoolean(AUTO_SHOW_STREAMS_KEY, true),
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
        const val LEGACY_WIFI_QUALITY_KEY = "wifi_quality"
        const val LOCAL_QUALITY_KEY = "local_quality"
        const val REMOTE_WIFI_QUALITY_KEY = "remote_wifi_quality"
        const val MOBILE_QUALITY_KEY = "mobile_quality"
        const val OFFLINE_QUOTA_BYTES_KEY = "offline_quota_bytes"
        const val OFFLINE_EXPIRATION_DAYS_KEY = "offline_expiration_days"
        const val DOWNLOAD_ON_MOBILE_KEY = "download_on_mobile"
        const val AUTO_SHOW_STREAMS_KEY = "auto_show_streams"
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
            LOCAL_QUALITY_KEY,
            REMOTE_WIFI_QUALITY_KEY,
            MOBILE_QUALITY_KEY,
            OFFLINE_QUOTA_BYTES_KEY,
            OFFLINE_EXPIRATION_DAYS_KEY,
            DOWNLOAD_ON_MOBILE_KEY,
            AUTO_SHOW_STREAMS_KEY,
            AUTO_SKIP_INTRO_KEY,
            AUTO_SKIP_RECAP_KEY,
            AUTO_SKIP_OUTRO_KEY,
        )
    }
}

private fun opaque(color: Int): Int = color or 0xFF000000.toInt()
