package io.rivune.app

import android.content.ClipData
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.content.pm.ResolveInfo
import android.net.Uri
import android.os.Build
import io.rivune.api.PlaybackMode
import java.net.URLEncoder
import java.nio.charset.StandardCharsets

private const val VLC_PACKAGE = "org.videolan.vlc"
private const val MPV_PACKAGE = "is.xyz.mpv"
private const val JUST_PLAYER_PACKAGE = "com.brouken.player"
private const val MX_PLAYER_FREE_PACKAGE = "com.mxtech.videoplayer.ad"
private const val MX_PLAYER_PRO_PACKAGE = "com.mxtech.videoplayer.pro"
private const val MX_PLAYER_FREE_ACTIVITY = "com.mxtech.videoplayer.ad.ActivityScreen"
private const val MX_PLAYER_PRO_ACTIVITY = "com.mxtech.videoplayer.ActivityScreen"
private val SUPPORTED_EXTERNAL_PLAYER_PACKAGES = setOf(
    VLC_PACKAGE,
    MPV_PACKAGE,
    JUST_PLAYER_PACKAGE,
    MX_PLAYER_FREE_PACKAGE,
    MX_PLAYER_PRO_PACKAGE,
)
private const val MAX_EXTERNAL_PLAYBACK_MS = Int.MAX_VALUE.toLong() * 1_000L

internal const val EXTERNAL_VIDEO_CAPABILITY = "android_intent"
internal const val EXTERNAL_MAGNET_CAPABILITY = "android_magnet"

private val EXTERNAL_VIDEO_MIME_TYPES = listOf(
    "video/*",
    "application/vnd.apple.mpegurl",
    "application/dash+xml",
    "video/x-matroska",
    "video/webm",
)

data class ExternalPlayerApp(
    val packageName: String,
    val label: String,
    val videoMimeTypes: Set<String>,
    val supportsMagnet: Boolean,
) {
    val supportsVideo: Boolean
        get() = videoMimeTypes.isNotEmpty()

    fun supports(protocol: String, container: String?): Boolean {
        val mimeType = externalPlaybackMimeType(protocol, container)
        return mimeType in videoMimeTypes || "video/*" in videoMimeTypes
    }
}

data class ExternalPlaybackSupport(
    val players: List<ExternalPlayerApp> = emptyList(),
) {
    val capabilityIds: List<String>
        get() = buildList {
            if (players.any(ExternalPlayerApp::supportsVideo)) add(EXTERNAL_VIDEO_CAPABILITY)
            if (players.any(ExternalPlayerApp::supportsMagnet)) add(EXTERNAL_MAGNET_CAPABILITY)
        }

    fun playersFor(mode: PlaybackMode?, protocol: String, container: String?): List<ExternalPlayerApp> = when {
        mode == PlaybackMode.EXTERNAL && protocol.equals("external", ignoreCase = true) ->
            players.filter(ExternalPlayerApp::supportsMagnet)
        mode == PlaybackMode.YOUTUBE || protocol.equals("youtube", ignoreCase = true) -> emptyList()
        else -> players.filter { it.supports(protocol, container) }
    }
}

internal data class EmbeddedPlayerSelection(
    val engine: EmbeddedPlayerEngine,
    val fallbackAllowed: Boolean,
)

internal fun embeddedPlayerSelection(preference: EmbeddedPlayerPreference): EmbeddedPlayerSelection = when (preference) {
    EmbeddedPlayerPreference.AUTOMATIC -> EmbeddedPlayerSelection(EmbeddedPlayerEngine.MEDIA3, fallbackAllowed = true)
    EmbeddedPlayerPreference.MEDIA3 -> EmbeddedPlayerSelection(EmbeddedPlayerEngine.MEDIA3, fallbackAllowed = false)
    EmbeddedPlayerPreference.MPV -> EmbeddedPlayerSelection(EmbeddedPlayerEngine.MPV, fallbackAllowed = false)
}

sealed interface PlaybackTargetSelection {
    data class Embedded(val preference: EmbeddedPlayerPreference) : PlaybackTargetSelection
    data class External(val player: ExternalPlayerApp) : PlaybackTargetSelection
}

internal sealed interface PreferredPlaybackTarget {
    data object Ask : PreferredPlaybackTarget
    data class Embedded(val preference: EmbeddedPlayerPreference) : PreferredPlaybackTarget
    data class External(val player: ExternalPlayerApp) : PreferredPlaybackTarget
}

internal fun preferredPlaybackTarget(
    preference: PreferredPlayer,
    embeddedPreference: EmbeddedPlayerPreference,
    source: io.rivune.api.PlaybackSourceOption,
    support: ExternalPlaybackSupport,
): PreferredPlaybackTarget {
    val players = support.playersFor(source.mode, source.protocol, source.container)
    return when (preference) {
        PreferredPlayer.Ask -> PreferredPlaybackTarget.Ask
        PreferredPlayer.Rivune -> if (source.mode == PlaybackMode.EXTERNAL) {
            PreferredPlaybackTarget.Ask
        } else {
            PreferredPlaybackTarget.Embedded(embeddedPreference)
        }
        is PreferredPlayer.External -> players.firstOrNull { it.packageName == preference.packageName }
            ?.let(PreferredPlaybackTarget::External)
            ?: PreferredPlaybackTarget.Ask
    }
}

data class ExternalPlaybackResult(
    val positionMs: Long?,
    val durationMs: Long?,
    val completed: Boolean,
)

internal fun detectExternalPlaybackSupport(context: Context): ExternalPlaybackSupport {
    val packageManager = context.packageManager
    val ownPackage = context.packageName
    val videoMimeTypesByPackage = mutableMapOf<String, MutableSet<String>>()
    externalVideoProbeIntents().forEach { (mimeType, intent) ->
        packageManager.queryExternalActivities(intent)
            .mapNotNull { it.activityInfo?.packageName }
            .filter { it != ownPackage && isSupportedExternalPlayerPackage(it) }
            .forEach { packageName ->
                videoMimeTypesByPackage.getOrPut(packageName, ::mutableSetOf).add(mimeType)
            }
    }
    val magnetPackages = packageManager.queryExternalActivities(
        Intent(Intent.ACTION_VIEW, Uri.parse("magnet:?xt=urn:btih:0000000000000000000000000000000000000000"))
            .addCategory(Intent.CATEGORY_DEFAULT),
    )
        .mapNotNull { it.activityInfo?.packageName }
        .filter { it != ownPackage && isSupportedExternalPlayerPackage(it) }
        .toSet()
    val packages = (videoMimeTypesByPackage.keys + magnetPackages).sorted()
    val players = packages.map { packageName ->
        val label = runCatching {
            packageManager.getApplicationLabel(packageManager.getApplicationInfoCompat(packageName)).toString().trim()
        }.getOrNull().orEmpty().ifBlank { packageName }
        ExternalPlayerApp(
            packageName = packageName,
            label = label,
            videoMimeTypes = videoMimeTypesByPackage[packageName].orEmpty(),
            supportsMagnet = packageName in magnetPackages,
        )
    }.sortedWith(compareBy(String.CASE_INSENSITIVE_ORDER, ExternalPlayerApp::label).thenBy(ExternalPlayerApp::packageName))
    return ExternalPlaybackSupport(players)
}

internal fun isSupportedExternalPlayerPackage(packageName: String): Boolean =
    packageName in SUPPORTED_EXTERNAL_PLAYER_PACKAGES

internal fun buildExternalPlaybackIntent(
    context: Context,
    presentation: PlayerPresentation,
): Intent? {
    val player = presentation.externalPlayer ?: return null
    val uri = Uri.parse(presentation.mediaUrl)
    val base = if (uri.scheme.equals("magnet", ignoreCase = true)) {
        Intent(Intent.ACTION_VIEW, uri).setPackage(player.packageName)
    } else {
        val exactMime = externalPlaybackMimeType(presentation.protocol, presentation.container)
        val exact = externalPlaybackIntent(uri, exactMime, player.packageName)
        when {
            context.packageManager.canResolveExternal(exact) -> exact
            exactMime != "video/*" -> externalPlaybackIntent(uri, "video/*", player.packageName)
            else -> exact
        }
    }.addCategory(Intent.CATEGORY_DEFAULT)

    val resultAware = Intent(base).also { applyExternalPlayerComponent(it, player.packageName) }
    val launchIntent = if (resultAware.component != null && context.packageManager.canResolveExternal(resultAware)) resultAware else base
    if (!context.packageManager.canResolveExternal(launchIntent)) return null
    addExternalPlayerExtras(launchIntent, presentation)
    return launchIntent
}

private fun externalPlaybackIntent(uri: Uri, mimeType: String, packageName: String): Intent =
    Intent(Intent.ACTION_VIEW).apply {
        setDataAndType(uri, mimeType)
        setPackage(packageName)
    }

private fun applyExternalPlayerComponent(intent: Intent, packageName: String) {
    when (packageName) {
        MX_PLAYER_FREE_PACKAGE -> intent.setClassName(packageName, MX_PLAYER_FREE_ACTIVITY)
        MX_PLAYER_PRO_PACKAGE -> intent.setClassName(packageName, MX_PLAYER_PRO_ACTIVITY)
    }
}

private fun addExternalPlayerExtras(intent: Intent, presentation: PlayerPresentation) {
    val player = presentation.externalPlayer ?: return
    intent.putExtra(Intent.EXTRA_TITLE, presentation.title)
    intent.putExtra("title", presentation.title)
    when (player.packageName) {
        VLC_PACKAGE -> {
            if (presentation.startPositionMs > 0L) {
                intent.putExtra("position", presentation.startPositionMs)
                intent.putExtra("from_start", false)
            }
            presentation.subtitles.firstOrNull { it.selected }?.let { subtitle ->
                intent.putExtra("subtitles_location", subtitle.url)
            }
        }
        MPV_PACKAGE -> {
            putIntResumePosition(intent, presentation.startPositionMs)
            addArraySubtitleExtras(intent, presentation.subtitles)
        }
        JUST_PLAYER_PACKAGE, MX_PLAYER_FREE_PACKAGE, MX_PLAYER_PRO_PACKAGE -> {
            putIntResumePosition(intent, presentation.startPositionMs)
            intent.putExtra("return_result", true)
            addArraySubtitleExtras(intent, presentation.subtitles)
        }
    }
}

private fun putIntResumePosition(intent: Intent, positionMs: Long) {
    if (positionMs <= 0L) return
    intent.putExtra("position", positionMs.coerceAtMost(Int.MAX_VALUE.toLong()).toInt())
}

private fun addArraySubtitleExtras(intent: Intent, subtitles: List<PlayerSubtitlePresentation>) {
    if (subtitles.isEmpty()) return
    val uris = subtitles.map { Uri.parse(it.url) }.toTypedArray()
    val selected = subtitles.filter(PlayerSubtitlePresentation::selected).map { Uri.parse(it.url) }.toTypedArray()
    intent.putExtra("subs", uris)
    intent.putExtra("subs.enable", selected)
    intent.putExtra("subs.name", subtitles.map(PlayerSubtitlePresentation::label).toTypedArray())

    val contentUris = uris.filter { it.scheme.equals("content", ignoreCase = true) }
    if (contentUris.isNotEmpty()) {
        intent.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
        intent.clipData = ClipData(
            "subtitles",
            arrayOf("application/x-subrip", "text/vtt"),
            ClipData.Item(contentUris.first()),
        ).apply {
            contentUris.drop(1).forEach { addItem(ClipData.Item(it)) }
        }
    }
}

internal fun parseExternalPlaybackResult(
    packageName: String,
    extras: Map<String, Any?>,
): ExternalPlaybackResult? {
    val dialect = when (packageName) {
        VLC_PACKAGE -> ExternalResultDialect.VLC
        MPV_PACKAGE -> ExternalResultDialect.GENERIC_POSITION
        JUST_PLAYER_PACKAGE, MX_PLAYER_FREE_PACKAGE, MX_PLAYER_PRO_PACKAGE -> ExternalResultDialect.MX_COMPATIBLE
        else -> return null
    }
    val completed = dialect == ExternalResultDialect.MX_COMPATIBLE && extras["end_by"] == "playback_completion"
    val position = when (dialect) {
        ExternalResultDialect.VLC -> extras["extra_position"].asBoundedMilliseconds()
        ExternalResultDialect.GENERIC_POSITION, ExternalResultDialect.MX_COMPATIBLE -> extras["position"].asBoundedMilliseconds()
    }
    val duration = when (dialect) {
        ExternalResultDialect.VLC -> extras["extra_duration"].asPositiveBoundedMilliseconds()
        ExternalResultDialect.GENERIC_POSITION, ExternalResultDialect.MX_COMPATIBLE -> extras["duration"].asPositiveBoundedMilliseconds()
    }
    if (position == null && !completed) return null
    if (position != null && duration != null && position > duration) return null
    return ExternalPlaybackResult(position, duration, completed)
}

internal fun parseExternalPlaybackResult(packageName: String, resultData: Intent?): ExternalPlaybackResult? {
    val bundle = resultData?.extras ?: return null
    val keys = listOf("extra_position", "extra_duration", "position", "duration", "end_by")
    return parseExternalPlaybackResult(packageName, keys.associateWith { key -> bundle.getValue(key) })
}

internal fun externalPlaybackMimeType(protocol: String, container: String?): String = when {
    protocol.equals("hls", ignoreCase = true) -> "application/vnd.apple.mpegurl"
    protocol.equals("dash", ignoreCase = true) -> "application/dash+xml"
    container.equals("mkv", ignoreCase = true) || container.equals("matroska", ignoreCase = true) -> "video/x-matroska"
    container.equals("webm", ignoreCase = true) -> "video/webm"
    else -> "video/*"
}

internal fun magnetUrl(infoHash: String, title: String): String? {
    val normalized = infoHash.trim()
    if (!normalized.matches(Regex("^[A-Fa-f0-9]{40}$|^[A-Za-z2-7]{32}$"))) return null
    val xt = encodeMagnetQueryValue("urn:btih:$normalized")
    val displayName = encodeMagnetQueryValue(title)
    return "magnet:?xt=$xt&dn=$displayName"
}

private fun encodeMagnetQueryValue(value: String): String =
    URLEncoder.encode(value, StandardCharsets.UTF_8.name()).replace("+", "%20")

private enum class ExternalResultDialect {
    VLC,
    GENERIC_POSITION,
    MX_COMPATIBLE,
}

private fun Any?.asBoundedMilliseconds(): Long? = when (this) {
    is Float -> takeIf(Float::isFinite)?.toLong()
    is Double -> takeIf(Double::isFinite)?.toLong()
    is Number -> toLong()
    else -> null
}?.takeIf { it in 0..MAX_EXTERNAL_PLAYBACK_MS }

private fun Any?.asPositiveBoundedMilliseconds(): Long? =
    asBoundedMilliseconds()?.takeIf { it > 0L }

@Suppress("DEPRECATION")
private fun PackageManager.queryExternalActivities(intent: Intent): List<ResolveInfo> =
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
        queryIntentActivities(intent, PackageManager.ResolveInfoFlags.of(PackageManager.MATCH_DEFAULT_ONLY.toLong()))
    } else {
        queryIntentActivities(intent, PackageManager.MATCH_DEFAULT_ONLY)
    }

private fun PackageManager.canResolveExternal(intent: Intent): Boolean =
    queryExternalActivities(intent).isNotEmpty()

@Suppress("DEPRECATION")
private fun PackageManager.getApplicationInfoCompat(packageName: String) =
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
        getApplicationInfo(packageName, PackageManager.ApplicationInfoFlags.of(0))
    } else {
        getApplicationInfo(packageName, 0)
    }

@Suppress("DEPRECATION")
private fun android.os.Bundle.getValue(key: String): Any? = get(key)

private fun externalVideoProbeIntents(): List<Pair<String, Intent>> =
    listOf("http", "https").flatMap { scheme ->
        EXTERNAL_VIDEO_MIME_TYPES.map { mimeType ->
            mimeType to Intent(Intent.ACTION_VIEW).apply {
                setDataAndType(Uri.parse("$scheme://example.invalid/video"), mimeType)
                addCategory(Intent.CATEGORY_DEFAULT)
            }
        }
    }
