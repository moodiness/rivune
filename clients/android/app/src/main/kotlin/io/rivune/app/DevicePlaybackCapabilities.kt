package io.rivune.app

import android.content.Context
import android.net.ConnectivityManager
import android.net.NetworkCapabilities

import android.media.MediaCodecInfo
import android.media.MediaCodecList
import io.rivune.api.PlaybackCapabilities
import io.rivune.api.PlaybackMediaProfile
import io.rivune.api.PlaybackProcessingMode
import io.rivune.api.PlaybackSubtitleMode

private const val FALLBACK_MAXIMUM_VIDEO_HEIGHT = 720
private const val FALLBACK_MAXIMUM_VIDEO_BITRATE_KBPS = 5_000
private const val AUTOMATIC_METERED_MAXIMUM_HEIGHT = 720
private const val AUTOMATIC_METERED_MAXIMUM_BITRATE_KBPS = 5_000
private const val ECONOMY_MAXIMUM_HEIGHT = 480
private const val ECONOMY_MAXIMUM_BITRATE_KBPS = 2_000
private const val BALANCED_MAXIMUM_HEIGHT = 1_080
private const val BALANCED_MAXIMUM_BITRATE_KBPS = 8_000

private val STANDARD_VIDEO_SIZES = listOf(
    7680 to 4320,
    3840 to 2160,
    1920 to 1080,
    1280 to 720,
    854 to 480,
    640 to 360,
    256 to 144,
)

private val VIDEO_CODEC_TYPES = listOf(
    "video/avc" to "h264",
    "video/hevc" to "hevc",
    "video/x-vnd.on2.vp9" to "vp9",
    "video/av01" to "av1",
)

private data class VideoSupport(
    val codec: String,
    val maximumHeight: Int,
    val maximumBitrateKbps: Int,
)

internal enum class PlaybackNetwork {
    UNMETERED,
    METERED,
}

internal data class PlaybackQualityLimit(
    val maximumHeight: Int?,
    val maximumVideoBitrateKbps: Int?,
)

internal object DevicePlaybackCapabilities {
    val value: PlaybackCapabilities by lazy(LazyThreadSafetyMode.PUBLICATION) {
        val decoderInfos = runCatching {
            MediaCodecList(MediaCodecList.ALL_CODECS).codecInfos
                .filterNot(MediaCodecInfo::isEncoder)
        }.getOrDefault(emptyList())
        val supportedTypes = decoderInfos
            .asSequence()
            .flatMap { it.supportedTypes.asSequence() }
            .map(String::lowercase)
            .toSet()
        val videoSupport = VIDEO_CODEC_TYPES.mapNotNull { (mimeType, codec) ->
            maximumDecoderSupport(decoderInfos, mimeType)?.let { support -> support.copy(codec = codec) }
        }.ifEmpty { listOf(VideoSupport("h264", FALLBACK_MAXIMUM_VIDEO_HEIGHT, FALLBACK_MAXIMUM_VIDEO_BITRATE_KBPS)) }
        val audioCodecs = buildList {
            if ("audio/mp4a-latm" in supportedTypes) add("aac")
            if ("audio/ac3" in supportedTypes) add("ac3")
            if ("audio/eac3" in supportedTypes || "audio/eac3-joc" in supportedTypes) add("eac3")
            if ("audio/opus" in supportedTypes) add("opus")
            if ("audio/vorbis" in supportedTypes) add("vorbis")
            if ("audio/mpeg" in supportedTypes) add("mp3")
        }.ifEmpty { listOf("aac") }
        val mediaProfiles = videoSupport.flatMap { support ->
            profileContainers(support.codec).map { container ->
                PlaybackMediaProfile(
                    container = container,
                    videoCodec = support.codec,
                    audioCodec = profileAudioCodecs(container, audioCodecs).joinToString(","),
                    maximumVideoBitDepth = 8,
                )
            }
        }
        PlaybackCapabilities(
            streamingProtocols = listOf("http", "hls", "dash"),
            containers = listOf("mp4", "mkv", "matroska", "webm", "mpegts"),
            videoCodecs = videoSupport.map(VideoSupport::codec),
            audioCodecs = audioCodecs,
            hdrFormats = listOf("sdr"),
            processingModes = listOf(
                PlaybackProcessingMode.REMUX,
                PlaybackProcessingMode.TRANSCODE_AUDIO,
                PlaybackProcessingMode.TRANSCODE,
            ),
            maximumHeight = videoSupport.minOf(VideoSupport::maximumHeight),
            maximumVideoBitrateKbps = videoSupport.minOf(VideoSupport::maximumBitrateKbps),
            maximumAudioChannels = 2,
            subtitleModes = listOf(PlaybackSubtitleMode.EXTERNAL, PlaybackSubtitleMode.BURN),
            mediaProfiles = mediaProfiles,
        )
    }
}

internal fun detectPlaybackNetwork(context: Context): PlaybackNetwork = runCatching {
    val connectivity = context.getSystemService(ConnectivityManager::class.java)
        ?: return@runCatching PlaybackNetwork.METERED
    val network = connectivity.activeNetwork ?: return@runCatching PlaybackNetwork.METERED
    val capabilities = connectivity.getNetworkCapabilities(network)
        ?: return@runCatching PlaybackNetwork.METERED
    if (capabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED)) {
        PlaybackNetwork.UNMETERED
    } else {
        PlaybackNetwork.METERED
    }
}.getOrDefault(PlaybackNetwork.METERED)

internal fun playbackQualityLimit(
    quality: NetworkQualityPreference,
    network: PlaybackNetwork,
): PlaybackQualityLimit = when (quality) {
    NetworkQualityPreference.AUTOMATIC -> if (network == PlaybackNetwork.METERED) {
        PlaybackQualityLimit(AUTOMATIC_METERED_MAXIMUM_HEIGHT, AUTOMATIC_METERED_MAXIMUM_BITRATE_KBPS)
    } else {
        PlaybackQualityLimit(null, null)
    }
    NetworkQualityPreference.ECONOMY ->
        PlaybackQualityLimit(ECONOMY_MAXIMUM_HEIGHT, ECONOMY_MAXIMUM_BITRATE_KBPS)
    NetworkQualityPreference.BALANCED ->
        PlaybackQualityLimit(BALANCED_MAXIMUM_HEIGHT, BALANCED_MAXIMUM_BITRATE_KBPS)
    NetworkQualityPreference.MAXIMUM -> PlaybackQualityLimit(null, null)
}

internal fun PlaybackCapabilities.withQualityLimit(limit: PlaybackQualityLimit): PlaybackCapabilities = copy(
    maximumHeight = maximumHeight.clampedTo(limit.maximumHeight),
    maximumVideoBitrateKbps = maximumVideoBitrateKbps.clampedTo(limit.maximumVideoBitrateKbps),
)

private fun Int?.clampedTo(limit: Int?): Int? = when {
    limit == null -> this
    this == null -> limit
    else -> minOf(this, limit)
}

private fun maximumDecoderSupport(decoderInfos: List<MediaCodecInfo>, mimeType: String): VideoSupport? =
    decoderInfos.mapNotNull { info ->
        val supportedType = info.supportedTypes.firstOrNull { it.equals(mimeType, ignoreCase = true) }
            ?: return@mapNotNull null
        runCatching {
            val videoCapabilities = info.getCapabilitiesForType(supportedType).videoCapabilities
            val maximumHeight = STANDARD_VIDEO_SIZES.firstOrNull { (width, height) ->
                videoCapabilities.isSizeSupported(width, height)
            }?.second ?: return@runCatching null
            VideoSupport(
                codec = "",
                maximumHeight = maximumHeight,
                maximumBitrateKbps = (videoCapabilities.bitrateRange.upper / 1_000).coerceIn(64, 200_000),
            )
        }.getOrNull()
    }.maxWithOrNull(compareBy<VideoSupport> { it.maximumHeight }.thenBy { it.maximumBitrateKbps })

private fun profileContainers(codec: String): List<String> = when (codec) {
    "h264", "hevc" -> listOf("mp4", "mkv", "matroska", "mpegts")
    "vp9" -> listOf("mkv", "matroska", "webm")
    "av1" -> listOf("mp4", "mkv", "matroska", "webm")
    else -> emptyList()
}

internal fun profileAudioCodecs(container: String, availableCodecs: List<String>): List<String> {
    val demuxedCodecs = when (container) {
        "mp4" -> setOf("aac", "ac3", "eac3")
        "mkv", "matroska" -> setOf("aac", "ac3", "eac3", "opus", "vorbis", "mp3")
        "webm" -> setOf("opus", "vorbis")
        "mpegts" -> setOf("aac", "ac3", "eac3", "mp3")
        else -> emptySet()
    }
    return availableCodecs.filter(demuxedCodecs::contains)
}
