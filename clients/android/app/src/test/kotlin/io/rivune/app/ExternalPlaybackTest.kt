package io.rivune.app

import io.rivune.api.PlaybackCapabilities
import io.rivune.api.PlaybackMediaProfile
import io.rivune.api.PlaybackMode
import io.rivune.api.PlaybackProcessingMode
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

class ExternalPlaybackTest {
    @Test
    fun discoveryAcceptsOnlySupportedMediaPlayers() {
        assertTrue(isSupportedExternalPlayerPackage("org.videolan.vlc"))
        assertTrue(isSupportedExternalPlayerPackage("is.xyz.mpv"))
        assertTrue(isSupportedExternalPlayerPackage("com.brouken.player"))
        assertTrue(isSupportedExternalPlayerPackage("com.mxtech.videoplayer.ad"))
        assertTrue(isSupportedExternalPlayerPackage("com.mxtech.videoplayer.pro"))
        assertFalse(isSupportedExternalPlayerPackage("com.google.android.apps.photos"))
        assertFalse(isSupportedExternalPlayerPackage("org.example.video-handler"))
    }

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
            stableIdentity = "stable-direct",
        )

        assertEquals(
            PreferredPlaybackTarget.Ask,
            preferredPlaybackTarget(PreferredPlayer.Ask, EmbeddedPlayerPreference.AUTOMATIC, source, support),
        )
        assertEquals(
            PreferredPlaybackTarget.Embedded(EmbeddedPlayerPreference.AUTOMATIC),
            preferredPlaybackTarget(PreferredPlayer.Rivune, EmbeddedPlayerPreference.AUTOMATIC, source, support),
        )
        assertEquals(
            PreferredPlaybackTarget.External(installed),
            preferredPlaybackTarget(PreferredPlayer.External(installed.packageName), EmbeddedPlayerPreference.AUTOMATIC, source, support),
        )
        assertEquals(
            PreferredPlaybackTarget.Ask,
            preferredPlaybackTarget(PreferredPlayer.External("org.example.missing"), EmbeddedPlayerPreference.AUTOMATIC, source, support),
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
            stableIdentity = "stable-torrent",
        )

        assertEquals(
            PreferredPlaybackTarget.Ask,
            preferredPlaybackTarget(PreferredPlayer.Rivune, EmbeddedPlayerPreference.AUTOMATIC, source, ExternalPlaybackSupport()),
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
        assertTrue(magnet.contains("dn=A%20film"))
        assertNull(magnetUrl("../../not-a-hash", "A film"))
    }

    @Test
    fun embeddedPreferencesSelectExpectedEngineAndFallbackPolicy() {
        assertEquals(
            EmbeddedPlayerSelection(EmbeddedPlayerEngine.MEDIA3, fallbackAllowed = true),
            embeddedPlayerSelection(EmbeddedPlayerPreference.AUTOMATIC),
        )
        assertEquals(
            EmbeddedPlayerSelection(EmbeddedPlayerEngine.MEDIA3, fallbackAllowed = false),
            embeddedPlayerSelection(EmbeddedPlayerPreference.MEDIA3),
        )
        assertEquals(
            EmbeddedPlayerSelection(EmbeddedPlayerEngine.MPV, fallbackAllowed = false),
            embeddedPlayerSelection(EmbeddedPlayerPreference.MPV),
        )
    }

    @Test
    fun playbackPresentationRetainsCanonicalCoordinationIdentity() {
        val titleId = java.util.UUID.randomUUID()
        val addonId = java.util.UUID.randomUUID()
        val presentation = PlayerPresentation(
            key = "coordinated",
            sessionId = java.util.UUID.randomUUID(),
            titleId = titleId,
            title = "Film",
            mediaType = "movie",
            resourceId = "opaque-resource",
            sourceAddonId = addonId,
            posterUrl = "/poster.jpg",
            mediaUrl = "https://media.example/film.mkv",
            protocol = "http",
            container = "mkv",
            mediaTimeline = null,
            startPositionMs = 0,
            timelineStartPositionMs = 0,
            durationSeconds = 0,
            expectedProgressVersion = 0,
            engine = EmbeddedPlayerEngine.MEDIA3,
            fallbackAllowed = false,
        )

        assertEquals(
            io.rivune.api.CoordinatedPlaybackItem(
                titleId = titleId,
                mediaType = "movie",
                resourceId = "opaque-resource",
                sourceAddonId = addonId,
                title = "Film",
                posterUrl = "/poster.jpg",
            ),
            presentation.coordinatedItem(),
        )
    }

    @Test
    fun refreshedRoomRetainsHostOnlyJoinCode() {
        val room = playbackRoom(joinCode = "23456789AB")
        assertEquals("23456789AB", room.copy(joinCode = null, version = 2).preservingJoinCode(room).joinCode)
        assertEquals("ABCDEFGHJK", room.copy(joinCode = "ABCDEFGHJK", version = 2).preservingJoinCode(room).joinCode)
        assertEquals(null, playbackRoom(joinCode = null).preservingJoinCode(room).joinCode)
    }

    private fun playbackRoom(joinCode: String?) = io.rivune.api.PlaybackRoom(
        id = java.util.UUID.randomUUID(),
        joinCode = joinCode,
        item = io.rivune.api.CoordinatedPlaybackItem(java.util.UUID.randomUUID()),
        state = "paused",
        positionMilliseconds = 0,
        durationMilliseconds = 0,
        version = 1,
        updatedAt = "2026-08-21T00:00:00Z",
        expiresAt = "2026-08-22T00:00:00Z",
        members = emptyList(),
    )

    @Test
    fun eligibleAutomaticMedia3FailureTransitionsSameSessionToMpv() {
        val sessionId = java.util.UUID.randomUUID()
        val presentation = PlayerPresentation(
            key = "media3",
            sessionId = sessionId,
            titleId = java.util.UUID.randomUUID(),
            title = "Film",
            mediaType = "movie",
            resourceId = "film",
            mediaUrl = "https://media.example/film.mkv",
            protocol = "http",
            container = "mkv",
            mediaTimeline = io.rivune.api.PlaybackMediaTimeline.RELATIVE,
            startPositionMs = 120_000L,
            timelineStartPositionMs = 120_000L,
            durationSeconds = 3_600,
            expectedProgressVersion = 2,
            engine = EmbeddedPlayerEngine.MEDIA3,
            fallbackAllowed = true,
        )

        val fallback = presentation.fallbackToMpv(PlayerEngineFailure(145_000L, fallbackEligible = true), "mpv")
        assertEquals(sessionId, fallback?.sessionId)
        assertEquals("mpv", fallback?.key)
        assertEquals(145_000L, fallback?.startPositionMs)
        assertEquals(120_000L, fallback?.timelineStartPositionMs)
        assertEquals(
            25_000L,
            mediaPlaybackPositionMs(
                requireNotNull(fallback).startPositionMs,
                fallback.timelineStartPositionMs,
                fallback.mediaTimeline,
            ),
        )
        assertEquals(
            145_000L,
            absolutePlaybackPositionMs(25_000L, fallback.timelineStartPositionMs, fallback.mediaTimeline),
        )
        val replayAbsoluteMs = absolutePlaybackPositionMs(0L, fallback.timelineStartPositionMs, fallback.mediaTimeline)
        assertEquals(120_000L, replayAbsoluteMs)
        assertEquals(
            0L,
            mediaPlaybackPositionMs(replayAbsoluteMs, fallback.timelineStartPositionMs, fallback.mediaTimeline),
        )
        assertEquals(EmbeddedPlayerEngine.MPV, fallback.engine)
        assertFalse(requireNotNull(fallback).fallbackAllowed)
        assertEquals(EmbeddedPlayerPreference.MPV, fallback.recoveryEmbeddedPreference())
        assertEquals(EmbeddedPlayerPreference.AUTOMATIC, presentation.recoveryEmbeddedPreference())
        assertEquals(
            EmbeddedPlayerPreference.MEDIA3,
            presentation.copy(fallbackAllowed = false).recoveryEmbeddedPreference(),
        )
        assertNull(fallback.externalPlayer)
        assertNull(presentation.fallbackToMpv(PlayerEngineFailure(42_500L, fallbackEligible = false), "ignored"))
        assertNull(fallback.fallbackToMpv(PlayerEngineFailure(43_000L, fallbackEligible = true), "ignored"))
        assertNull(
            presentation.copy(fallbackAllowed = false)
                .fallbackToMpv(PlayerEngineFailure(42_500L, fallbackEligible = true), "ignored"),
        )
    }

    @Test
    fun capabilitySelectorKeepsExplicitOrOfferedMedia3SafeAndUsesMpvWhenAvailable() {
        val media3 = PlaybackCapabilities(
            streamingProtocols = listOf("http"),
            containers = listOf("mp4"),
            videoCodecs = listOf("h264"),
        )
        assertEquals(
            media3,
            playbackCapabilitiesFor(PreferredPlayer.Rivune, EmbeddedPlayerPreference.MEDIA3, media3),
        )
        assertEquals(
            MpvPlaybackCapabilities,
            playbackCapabilitiesFor(PreferredPlayer.Rivune, EmbeddedPlayerPreference.AUTOMATIC, media3),
        )
        assertEquals(
            MpvPlaybackCapabilities,
            playbackCapabilitiesFor(PreferredPlayer.Rivune, EmbeddedPlayerPreference.MPV, media3),
        )
        assertEquals(
            media3,
            playbackCapabilitiesFor(PreferredPlayer.Ask, EmbeddedPlayerPreference.MPV, media3),
        )
        val capped = MpvPlaybackCapabilities.withQualityLimit(PlaybackQualityLimit(720, 2_000))
        assertTrue(assertNotNull(capped.maximumHeight) <= 720)
        assertTrue(assertNotNull(capped.maximumVideoBitrateKbps) <= 2_000)
    }

    @Test
    fun mpvCapabilitiesKeepSoftwareBreadthWithoutOverclaimingDeviceVideoSupport() {
        val device = PlaybackCapabilities(
            streamingProtocols = listOf("http"),
            containers = listOf("mp4", "mkv"),
            videoCodecs = listOf("h264"),
            audioCodecs = listOf("aac"),
            hdrFormats = listOf("sdr"),
            maximumHeight = 1_080,
            maximumVideoBitrateKbps = 12_000,
            mediaProfiles = listOf(
                PlaybackMediaProfile("mkv", "h264", "aac", maximumVideoBitDepth = 8),
                PlaybackMediaProfile("mp4", "hevc", "aac", maximumVideoBitDepth = 10),
            ),
        )

        val capabilities = mpvPlaybackCapabilities(device)

        assertEquals(listOf("h264"), capabilities.videoCodecs)
        assertFalse(capabilities.videoCodecs.orEmpty().any { it == "hevc" || it == "h265" })
        assertFalse(capabilities.mediaProfiles.orEmpty().any { it.videoCodec == "hevc" || it.videoCodec == "h265" })
        assertEquals(listOf("sdr"), capabilities.hdrFormats)
        assertEquals(1_080, capabilities.maximumHeight)
        assertEquals(12_000, capabilities.maximumVideoBitrateKbps)
        assertTrue("avi" in capabilities.containers)
        assertTrue("truehd" in capabilities.audioCodecs.orEmpty())
        assertTrue("truehd" in capabilities.mediaProfiles.orEmpty().single().audioCodec.orEmpty().split(','))
        assertTrue(PlaybackProcessingMode.TRANSCODE in capabilities.processingModes.orEmpty())

        val capped = capabilities.withQualityLimit(PlaybackQualityLimit(720, 2_000))
        assertEquals(720, capped.maximumHeight)
        assertEquals(2_000, capped.maximumVideoBitrateKbps)
    }

    @Test
    fun mpvCapabilitiesRetainHardwareHevcAndItsDeviceLimits() {
        val device = PlaybackCapabilities(
            streamingProtocols = listOf("http"),
            containers = listOf("mp4"),
            videoCodecs = listOf("h264", "hevc"),
            audioCodecs = listOf("aac"),
            hdrFormats = listOf("sdr", "hdr10", "hlg"),
            maximumHeight = 2_160,
            maximumVideoBitrateKbps = 30_000,
            mediaProfiles = listOf(
                PlaybackMediaProfile("mp4", "h264", "aac", maximumVideoBitDepth = 8),
                PlaybackMediaProfile("mp4", "hevc", "aac", maximumVideoBitDepth = 10),
            ),
        )

        val capabilities = mpvPlaybackCapabilities(device)
        val hevcProfile = capabilities.mediaProfiles.orEmpty().single { it.videoCodec == "hevc" }

        assertTrue("hevc" in capabilities.videoCodecs.orEmpty())
        assertEquals(10, hevcProfile.maximumVideoBitDepth)
        assertEquals(listOf("sdr", "hdr10", "hlg"), capabilities.hdrFormats)
        assertEquals(2_160, capabilities.maximumHeight)
        assertEquals(30_000, capabilities.maximumVideoBitrateKbps)
    }

}
