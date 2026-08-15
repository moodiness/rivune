package io.rivune.app

import io.rivune.api.PlaybackMode
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

class ExternalPlaybackTest {
    @Test
    fun supportAdvertisesGenericCapabilitiesAndFiltersPlayersByMimeType() {
        val support = ExternalPlaybackSupport(
            listOf(
                ExternalPlayerApp("org.example.video", "Video", setOf("video/*"), supportsMagnet = false),
                ExternalPlayerApp("org.example.hls", "HLS", setOf("application/vnd.apple.mpegurl"), supportsMagnet = false),
                ExternalPlayerApp("org.example.mkv", "MKV", setOf("video/x-matroska"), supportsMagnet = false),
                ExternalPlayerApp("org.example.torrent", "Torrent", emptySet(), supportsMagnet = true),
            ),
        )

        assertEquals(listOf(EXTERNAL_VIDEO_CAPABILITY, EXTERNAL_MAGNET_CAPABILITY), support.capabilityIds)
        assertEquals(
            listOf("org.example.video", "org.example.hls"),
            support.playersFor(PlaybackMode.DIRECT, "hls", "ts").map { it.packageName },
        )
        assertEquals(
            listOf("org.example.video", "org.example.mkv"),
            support.playersFor(PlaybackMode.DIRECT, "http", "mkv").map { it.packageName },
        )
        assertEquals(emptyList(), support.playersFor(PlaybackMode.YOUTUBE, "youtube", null))
        assertEquals(
            listOf("org.example.torrent"),
            support.playersFor(PlaybackMode.EXTERNAL, "external", null).map { it.packageName },
        )
        assertFalse(support.capabilityIds.any { it.contains("org.example") })
    }

    @Test
    fun preferredTargetFallsBackToAskWithoutMutatingPreference() {
        val installed = ExternalPlayerApp(
            "org.example.installed",
            "Installed",
            setOf("video/*"),
            supportsMagnet = false,
        )
        val support = ExternalPlaybackSupport(listOf(installed))
        val source = io.rivune.api.PlaybackSourceOption(
            id = "source",
            sourceRef = "ref",
            addonId = java.util.UUID.randomUUID(),
            manifestId = "addon",
            streamIndex = 0,
            name = "Direct",
            protocol = "http",
            mode = PlaybackMode.DIRECT,
            expiresAt = "2099-01-01T00:00:00Z",
        )

        assertEquals(
            PreferredPlaybackTarget.Ask,
            preferredPlaybackTarget(PreferredPlayer.Ask, source, support),
        )
        assertEquals(
            PreferredPlaybackTarget.Rivune,
            preferredPlaybackTarget(PreferredPlayer.Rivune, source, support),
        )
        assertEquals(
            PreferredPlaybackTarget.External(installed),
            preferredPlaybackTarget(PreferredPlayer.External(installed.packageName), source, support),
        )
        assertEquals(
            PreferredPlaybackTarget.Ask,
            preferredPlaybackTarget(PreferredPlayer.External("org.example.missing"), source, support),
        )
    }

    @Test
    fun externalOnlySourceNeverFallsBackToInternalPlayer() {
        val source = io.rivune.api.PlaybackSourceOption(
            id = "external",
            sourceRef = "ref",
            addonId = java.util.UUID.randomUUID(),
            manifestId = "addon",
            streamIndex = 0,
            name = "Torrent",
            protocol = "external",
            mode = PlaybackMode.EXTERNAL,
            expiresAt = "2099-01-01T00:00:00Z",
        )

        assertEquals(
            PreferredPlaybackTarget.Ask,
            preferredPlaybackTarget(PreferredPlayer.Rivune, source, ExternalPlaybackSupport()),
        )
    }

    @Test
    fun externalResultParsesSupportedDialectsAndRejectsInvalidData() {
        assertEquals(
            ExternalPlaybackResult(61_000L, 120_000L, completed = false),
            parseExternalPlaybackResult(
                "org.videolan.vlc",
                mapOf("extra_position" to 61_000L, "extra_duration" to 120_000L),
            ),
        )
        assertEquals(
            ExternalPlaybackResult(null, 120_000L, completed = true),
            parseExternalPlaybackResult(
                "com.mxtech.videoplayer.ad",
                mapOf("duration" to 120_000, "end_by" to "playback_completion"),
            ),
        )
        assertEquals(
            ExternalPlaybackResult(null, null, completed = true),
            parseExternalPlaybackResult(
                "com.brouken.player",
                mapOf("end_by" to "playback_completion"),
            ),
        )
        assertNull(parseExternalPlaybackResult("org.unknown.player", mapOf("position" to 1_000L)))
        assertNull(
            parseExternalPlaybackResult(
                "org.videolan.vlc",
                mapOf("extra_position" to 121_000L, "extra_duration" to 120_000L),
            ),
        )
        assertNull(parseExternalPlaybackResult("org.videolan.vlc", mapOf("extra_position" to -1L)))
        assertNull(
            parseExternalPlaybackResult(
                "org.videolan.vlc",
                mapOf("extra_position" to Double.NaN, "extra_duration" to 120_000L),
            ),
        )
    }

    @Test
    fun mimeAndMagnetValuesAreConstrained() {
        assertEquals("application/vnd.apple.mpegurl", externalPlaybackMimeType("hls", "ts"))
        assertEquals("application/dash+xml", externalPlaybackMimeType("dash", "mp4"))
        assertEquals("video/x-matroska", externalPlaybackMimeType("http", "mkv"))
        assertEquals("video/*", externalPlaybackMimeType("http", null))

        val magnet = magnetUrl("0123456789abcdef0123456789abcdef01234567", "A film")
        assertTrue(magnet?.startsWith("magnet:?xt=urn%3Abtih%3A0123456789abcdef0123456789abcdef01234567") == true)
        assertTrue(magnet?.contains("dn=A%20film") == true)
        assertNull(magnetUrl("../../not-a-hash", "A film"))
    }
}
