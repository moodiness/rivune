package io.rivune.app

import androidx.media3.common.C
import androidx.media3.common.MimeTypes
import io.rivune.api.PlaybackMarker
import io.rivune.api.PlaybackMarkerType
import io.rivune.api.PlaybackMediaTimeline
import java.util.Locale
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertNotNull
import kotlin.test.assertTrue
import kotlin.test.assertIs

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
    fun watchedThresholdDoesNotImplyNaturalPlaybackEnd() {
        val durationMs = 10_000L
        assertEquals(9_000L, completionThreshold(durationMs))
        assertTrue(9_000L >= completionThreshold(durationMs))
        assertTrue(isNaturalPlaybackEnd(androidx.media3.common.Player.STATE_ENDED))
        assertFalse(isNaturalPlaybackEnd(androidx.media3.common.Player.STATE_READY))
    }

    @Test
    fun automaticMpvFallbackAcceptsOnlyUnsupportedMediaClassifications() {
        val eligible = setOf(
            androidx.media3.common.PlaybackException.ERROR_CODE_PARSING_CONTAINER_UNSUPPORTED,
            androidx.media3.common.PlaybackException.ERROR_CODE_PARSING_MANIFEST_UNSUPPORTED,
            androidx.media3.common.PlaybackException.ERROR_CODE_DECODING_FORMAT_EXCEEDS_CAPABILITIES,
            androidx.media3.common.PlaybackException.ERROR_CODE_DECODING_FORMAT_UNSUPPORTED,
        )
        eligible.forEach { assertTrue(media3FallbackEligible(it)) }

        val corruptOrRuntimeFailures = setOf(
            androidx.media3.common.PlaybackException.ERROR_CODE_PARSING_CONTAINER_MALFORMED,
            androidx.media3.common.PlaybackException.ERROR_CODE_PARSING_MANIFEST_MALFORMED,
            androidx.media3.common.PlaybackException.ERROR_CODE_DECODER_INIT_FAILED,
            androidx.media3.common.PlaybackException.ERROR_CODE_DECODER_QUERY_FAILED,
            androidx.media3.common.PlaybackException.ERROR_CODE_DECODING_FAILED,
            androidx.media3.common.PlaybackException.ERROR_CODE_IO_NETWORK_CONNECTION_FAILED,
        )
        corruptOrRuntimeFailures.forEach { assertFalse(media3FallbackEligible(it)) }
    }
    @Test
    fun mpvStartupWatchdogRequiresRequestedPlaybackAndLiveSurface() {
        assertTrue(shouldArmMpvStartupWatchdog(true, true, startupSucceeded = false, terminal = false))
        assertFalse(shouldArmMpvStartupWatchdog(false, true, startupSucceeded = false, terminal = false))
        assertFalse(shouldArmMpvStartupWatchdog(true, false, startupSucceeded = false, terminal = false))
        assertFalse(shouldArmMpvStartupWatchdog(true, true, startupSucceeded = true, terminal = false))
        assertFalse(shouldArmMpvStartupWatchdog(true, true, startupSucceeded = false, terminal = true))
        assertEquals(45_000L, MPV_STARTUP_TIMEOUT_MS)
    }

    @Test
    fun mpvTerminalPolicyRejectsPlayAndFocusResume() {
        assertTrue(shouldResumeMpvPlayback(playbackRequested = true, terminal = false))
        assertFalse(shouldResumeMpvPlayback(playbackRequested = false, terminal = false))
        assertFalse(shouldResumeMpvPlayback(playbackRequested = true, terminal = true))
        assertFalse(shouldResumeMpvPlayback(playbackRequested = false, terminal = true))
    }

    @Test
    fun mpvReplayResetsAndResumesOnlyNaturalEnd() {
        val replayState = assertNotNull(resetMpvNaturalEndForReplay(MpvTerminalState.NATURAL_END))

        assertEquals(MpvTerminalState.ACTIVE, replayState)
        assertTrue(
            shouldResumeMpvPlayback(
                playbackRequested = true,
                terminal = replayState != MpvTerminalState.ACTIVE,
            ),
        )
        assertNull(resetMpvNaturalEndForReplay(MpvTerminalState.ACTIVE))
        assertNull(resetMpvNaturalEndForReplay(MpvTerminalState.FAILURE))
        assertNull(resetMpvNaturalEndForReplay(MpvTerminalState.RELEASED))
    }

    @Test
    fun coordinatedPauseCancelsDeferredLifecycleResumeForBothEngines() {
        assertFalse(coordinatedLifecycleResumeIntent(current = true, state = "pause"))
        assertFalse(coordinatedLifecycleResumeIntent(current = true, state = "paused"))
        assertFalse(coordinatedLifecycleResumeIntent(current = true, state = "ended"))
        assertTrue(coordinatedLifecycleResumeIntent(current = false, state = "play"))
        assertTrue(coordinatedLifecycleResumeIntent(current = false, state = "playing"))
    }

    @Test
    fun mpvRemainsPreparingUntilPlaybackRestartAndBuffersOnlyAfterStartup() {
        assertEquals(MpvPlaybackState.PREPARING, mpvPlaybackState(startupSucceeded = false, pausedForCache = false))
        assertEquals(MpvPlaybackState.PREPARING, mpvPlaybackState(startupSucceeded = false, pausedForCache = true))
        assertEquals(MpvPlaybackState.READY, mpvPlaybackState(startupSucceeded = true, pausedForCache = false))
        assertEquals(MpvPlaybackState.BUFFERING, mpvPlaybackState(startupSucceeded = true, pausedForCache = true))
    }

    @Test
    fun onlyEligibleAutomaticMedia3FailuresBypassRecoveryOverlay() {
        val presentation = playerPresentationForPolicy(
            engine = EmbeddedPlayerEngine.MEDIA3,
            fallbackAllowed = true,
        )
        assertTrue(shouldAutomaticallyFallbackToMpv(presentation, PlayerEngineFailure(10L, fallbackEligible = true)))
        assertFalse(shouldAutomaticallyFallbackToMpv(presentation, PlayerEngineFailure(10L, fallbackEligible = false)))
        assertFalse(
            shouldAutomaticallyFallbackToMpv(
                presentation.copy(engine = EmbeddedPlayerEngine.MPV, fallbackAllowed = false),
                PlayerEngineFailure(10L, fallbackEligible = true),
            ),
        )
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
    fun pictureFormatCyclesFitFillZoomAndBackToFit() {
        assertEquals(VideoAspectPreference.FILL, VideoAspectPreference.FIT.nextVideoAspect())
        assertEquals(VideoAspectPreference.ZOOM, VideoAspectPreference.FILL.nextVideoAspect())
        assertEquals(VideoAspectPreference.FIT, VideoAspectPreference.ZOOM.nextVideoAspect())
    }

    @Test
    fun pictureFormatCaptionTracksTheCurrentModeAndNextAction() {
        assertEquals(R.string.player_aspect_fit_to_fill, videoAspectActionResource(VideoAspectPreference.FIT))
        assertEquals(R.string.player_aspect_fill_to_zoom, videoAspectActionResource(VideoAspectPreference.FILL))
        assertEquals(R.string.player_aspect_zoom_to_fit, videoAspectActionResource(VideoAspectPreference.ZOOM))
    }

    @Test
    fun networkQualityPresetsClampDeviceCapabilities() {
        val device = DevicePlaybackCapabilities.value
        val economy = device.withQualityLimit(
            playbackQualityLimit(NetworkQualityPreference.ECONOMY, PlaybackNetwork.WIFI_OR_ETHERNET),
        )
        val balanced = device.withQualityLimit(
            playbackQualityLimit(NetworkQualityPreference.BALANCED, PlaybackNetwork.WIFI_OR_ETHERNET),
        )
        val automaticMobile = device.withQualityLimit(
            playbackQualityLimit(NetworkQualityPreference.AUTOMATIC, PlaybackNetwork.MOBILE_OR_METERED),
        )

        assertTrue(assertNotNull(economy.maximumHeight) <= 480)
        assertTrue(assertNotNull(economy.maximumVideoBitrateKbps) <= 2_000)
        assertTrue(assertNotNull(balanced.maximumHeight) <= 1_080)
        assertTrue(assertNotNull(balanced.maximumVideoBitrateKbps) <= 8_000)
        assertTrue(assertNotNull(automaticMobile.maximumHeight) <= 720)
        assertTrue(assertNotNull(automaticMobile.maximumVideoBitrateKbps) <= 5_000)
        assertEquals(device, device.withQualityLimit(playbackQualityLimit(NetworkQualityPreference.MAXIMUM, PlaybackNetwork.MOBILE_OR_METERED)))
        assertEquals(device, device.withQualityLimit(playbackQualityLimit(NetworkQualityPreference.AUTOMATIC, PlaybackNetwork.WIFI_OR_ETHERNET)))
    }

    @Test
    fun networkTransportTakesPrecedenceOverMetering() {
        assertEquals(
            PlaybackNetwork.WIFI_OR_ETHERNET,
            classifyPlaybackNetwork(hasWifi = true, hasEthernet = false, hasCellular = false, isMetered = true),
        )
        assertEquals(
            PlaybackNetwork.WIFI_OR_ETHERNET,
            classifyPlaybackNetwork(hasWifi = false, hasEthernet = true, hasCellular = false, isMetered = true),
        )
        assertEquals(
            PlaybackNetwork.MOBILE_OR_METERED,
            classifyPlaybackNetwork(hasWifi = false, hasEthernet = false, hasCellular = true, isMetered = false),
        )
        assertEquals(
            PlaybackNetwork.WIFI_OR_ETHERNET,
            classifyPlaybackNetwork(hasWifi = false, hasEthernet = false, hasCellular = false, isMetered = false),
        )
        assertEquals(
            PlaybackNetwork.MOBILE_OR_METERED,
            classifyPlaybackNetwork(hasWifi = false, hasEthernet = false, hasCellular = false, isMetered = true),
        )
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

    @Test
    fun playbackMarkersUseStartInclusiveEndExclusiveBoundaries() {
        val intro = marker(PlaybackMarkerType.INTRO, startSeconds = 10.0, endSeconds = 20.0)

        assertNull(activePlaybackMarker(listOf(intro), 9_999L))
        assertEquals(intro, activePlaybackMarker(listOf(intro), 10_000L))
        assertEquals(intro, activePlaybackMarker(listOf(intro), 19_999L))
        assertNull(activePlaybackMarker(listOf(intro), 20_000L))
    }

    @Test
    fun invalidPlaybackMarkerRangesAreIgnored() {
        val invalid = listOf(
            marker(PlaybackMarkerType.INTRO, Double.NaN, 10.0),
            marker(PlaybackMarkerType.RECAP, 1.0, Double.POSITIVE_INFINITY),
            marker(PlaybackMarkerType.OUTRO, -1.0, 10.0),
            marker(PlaybackMarkerType.INTRO, 10.0, 10.0),
            marker(PlaybackMarkerType.RECAP, 20.0, 10.0),
            marker(PlaybackMarkerType.OUTRO, 0.0, Double.MAX_VALUE),
        )

        assertTrue(invalid.none(::isValidPlaybackMarker))
        assertNull(activePlaybackMarker(invalid, 5_000L))
        invalid.forEach { assertNull(playbackMarkerSeekTargetMs(it)) }
    }

    @Test
    fun markerSeekTargetIsPublishedEndInMilliseconds() {
        val recap = marker(PlaybackMarkerType.RECAP, startSeconds = 3.5, endSeconds = 12.345)

        assertEquals(12_345L, playbackMarkerSeekTargetMs(recap))
    }

    @Test
    fun overlappingValidMarkersRespectPublishedOrder() {
        val recap = marker(PlaybackMarkerType.RECAP, startSeconds = 5.0, endSeconds = 15.0)
        val intro = marker(PlaybackMarkerType.INTRO, startSeconds = 10.0, endSeconds = 20.0)

        assertEquals(recap, activePlaybackMarker(listOf(recap, intro), 12_000L))
        assertEquals(intro, activePlaybackMarker(listOf(intro, recap), 12_000L))
    }

    @Test
    fun automaticSkipGatesEachPublishedMarkerTypeIndependently() {
        assertTrue(
            shouldAutoSkipPlaybackMarker(
                PlaybackMarkerType.INTRO,
                autoSkipIntro = true,
                autoSkipRecap = false,
                autoSkipOutro = false,
            ),
        )
        assertFalse(
            shouldAutoSkipPlaybackMarker(
                PlaybackMarkerType.RECAP,
                autoSkipIntro = true,
                autoSkipRecap = false,
                autoSkipOutro = true,
            ),
        )
        assertTrue(
            shouldAutoSkipPlaybackMarker(
                PlaybackMarkerType.OUTRO,
                autoSkipIntro = false,
                autoSkipRecap = false,
                autoSkipOutro = true,
            ),
        )
    }

    @Test
    fun automaticSkipIsConsumedPerMarkerEntry() {
        val repeated = marker(PlaybackMarkerType.INTRO, startSeconds = 10.0, endSeconds = 20.0)
        val entries = listOf(repeated, repeated)

        assertEquals(0, activePlaybackMarkerEntry(entries, 10_000L)?.index)
        assertEquals(setOf(0, 1), autoSkipConsumedAfterUserSeek(entries, setOf(0, 1), 10_000L))
    }

    @Test
    fun userMustSeekStrictlyBeforeMarkerStartToRearmAutomaticSkip() {
        val markers = listOf(
            marker(PlaybackMarkerType.INTRO, startSeconds = 10.0, endSeconds = 20.0),
            marker(PlaybackMarkerType.OUTRO, startSeconds = 50.0, endSeconds = 60.0),
        )

        assertEquals(setOf(0, 1), autoSkipConsumedAfterUserSeek(markers, setOf(0, 1), 50_000L))
        assertEquals(setOf(0), autoSkipConsumedAfterUserSeek(markers, setOf(0, 1), 49_999L))
        assertEquals(emptySet(), autoSkipConsumedAfterUserSeek(markers, setOf(0, 1), 9_999L))
    }

    @Test
    fun playbackTimeFormattingIsStableAtMinuteAndHourBoundaries() {
        assertEquals("0:00", formatPlaybackTime(-1L))
        assertEquals("0:59", formatPlaybackTime(59_999L))
        assertEquals("1:00", formatPlaybackTime(60_000L))
        assertEquals("59:59", formatPlaybackTime(3_599_999L))
        assertEquals("1:00:00", formatPlaybackTime(3_600_000L))
        assertEquals("12:34:56", formatPlaybackTime(45_296_000L))
    }

    @Test
    fun relativeProcessedTimelineUsesInspectedTotalDuration() {
        val requestedStartMs = 120_000L

        assertEquals(
            0L,
            mediaPlaybackPositionMs(requestedStartMs, requestedStartMs, PlaybackMediaTimeline.RELATIVE),
        )
        assertEquals(
            138_000L,
            absolutePlaybackPositionMs(18_000L, requestedStartMs, PlaybackMediaTimeline.RELATIVE),
        )
        assertEquals(
            5_947_000L,
            resolvedPlaybackDurationMs(5_947_000L, 27_000L, requestedStartMs, PlaybackMediaTimeline.RELATIVE),
        )
        assertEquals(
            147_000L,
            resolvedPlaybackDurationMs(0L, 27_000L, requestedStartMs, PlaybackMediaTimeline.RELATIVE),
        )
    }

    @Test
    fun mpvInitialResumeIsPartOfLoadCommand() {
        assertEquals(
            listOf("loadfile", "https://media.example/movie.mkv", "replace"),
            mpvLoadFileCommand("https://media.example/movie.mkv", 0L).toList(),
        )
        assertEquals(
            listOf("loadfile", "https://media.example/movie.mkv", "replace", "-1", "start=+100.250"),
            mpvLoadFileCommand("https://media.example/movie.mkv", 100_250L).toList(),
        )
    }

    @Test
    fun absoluteTimelineKeepsMediaPositionsUnshifted() {
        assertEquals(
            120_000L,
            mediaPlaybackPositionMs(120_000L, 120_000L, PlaybackMediaTimeline.ABSOLUTE),
        )
        assertEquals(
            138_000L,
            absolutePlaybackPositionMs(138_000L, 120_000L, PlaybackMediaTimeline.ABSOLUTE),
        )
    }

    @Test
    fun trackLabelsPreferPublishedLabelsThenLocalizedLanguagesAndDisambiguateDuplicates() {
        val labels = playerTrackLabels(
            sources = listOf(
                PlayerTrackLabelSource(label = "Director commentary", language = "en"),
                PlayerTrackLabelSource(label = null, language = "fr"),
                PlayerTrackLabelSource(label = null, language = "fr"),
                PlayerTrackLabelSource(label = null, language = null),
            ),
            locale = Locale.ENGLISH,
            fallbackLabel = "Track",
        )

        assertEquals(listOf("Director commentary", "French · 1", "French · 2", "Track 4"), labels)
    }

    @Test
    fun publishedIsoLanguageLabelsAreHumanizedWithoutHidingGenericTracks() {
        assertEquals(
            listOf("French", "English", "Spanish", "Subtitle 1"),
            playerTrackLabels(
                sources = listOf(
                    PlayerTrackLabelSource(label = "fre", language = null),
                    PlayerTrackLabelSource(label = "eng", language = null),
                    PlayerTrackLabelSource(label = "Subtitle 1", language = "spa"),
                    PlayerTrackLabelSource(label = "Subtitle 1", language = null),
                ),
                locale = Locale.ENGLISH,
                fallbackLabel = "Subtitle",
            ),
        )
    }

    @Test
    fun isoLanguageCodesAreHumanizedInTheRequestedLocale() {
        assertEquals("Anglais", localizedLanguageName("en", Locale.FRENCH))
        assertEquals("English", localizedLanguageName("eng", Locale.ENGLISH))
        assertEquals("Français", localizedLanguageName("fr-FR", Locale.FRENCH))
        assertNull(localizedLanguageName(null, Locale.ENGLISH))
        assertNull(localizedLanguageName("zz", Locale.ENGLISH))
    }

    @Test
    fun playerOptionMenuToggleClosesCurrentMenuAndSwitchesDirectly() {
        assertEquals(PlayerOptionsMenu.Audio, toggledPlayerOptionsMenu(null, PlayerOptionsMenu.Audio))
        assertNull(toggledPlayerOptionsMenu(PlayerOptionsMenu.Audio, PlayerOptionsMenu.Audio))
        assertEquals(
            PlayerOptionsMenu.Speed,
            toggledPlayerOptionsMenu(PlayerOptionsMenu.Subtitles, PlayerOptionsMenu.Speed),
        )
    }

    @Test
    fun playbackSpeedLabelsCoverEverySessionOptionWithoutTrailingZeros() {
        assertEquals(
            listOf("0.5×", "0.75×", "1×", "1.25×", "1.5×", "2×"),
            PLAYER_PLAYBACK_SPEEDS.map(::formatPlaybackSpeed),
        )
    }

    @Test
    fun trackKeysPreserveExactGroupAndTrackCoordinates() {
        assertEquals(PlayerTrackKey(groupIndex = 3, trackIndex = 1, groupId = "audio-3"), PlayerTrackKey(3, 1, "audio-3"))
        assertFalse(PlayerTrackKey(3, 1, "audio-3") == PlayerTrackKey(1, 3, "audio-1"))
        assertFalse(PlayerTrackKey(3, 1, "audio-3") == PlayerTrackKey(3, 1, "replacement"))
    }

    @Test
    fun mpvEndRemainsNaturalWhenEofPropertyCallbackArrivesBeforeOrAfterEndEvent() {
        assertTrue(isMpvNaturalEnd(observedEof = true, currentEof = false))
        assertTrue(isMpvNaturalEnd(observedEof = false, currentEof = true))
        assertFalse(isMpvNaturalEnd(observedEof = false, currentEof = false))
        assertFalse(isMpvNaturalEnd(observedEof = false, currentEof = null))
    }

    @Test
    fun mpvStartupAddsNoUnselectedExternalSubtitlesAndOnlyOneSelectedSubtitle() {
        val unselected = (0 until 1_024).map { index -> subtitle(index) }
        assertTrue(initialMpvExternalSubtitles(unselected).isEmpty())

        val withSelection = unselected.mapIndexed { index, subtitle -> subtitle.copy(selected = index == 731) }
        assertEquals(listOf(withSelection[731]), initialMpvExternalSubtitles(withSelection))
    }

    @Test
    fun mpvProjectsEveryUnloadedExternalSubtitleWithStableUniqueIdentity() {
        val advertised = (0 until 1_024).map { index -> subtitle(index) }

        val first = projectMpvSubtitleTracks(advertised, emptyList(), selectedIdentity = null)
        val second = projectMpvSubtitleTracks(advertised, emptyList(), selectedIdentity = null)

        assertEquals(1_024, first.size)
        assertTrue(first.all { it.nativeId == null })
        assertEquals(1_024, first.map(MpvTrack::identity).toSet().size)
        assertEquals(first.map(MpvTrack::identity), second.map(MpvTrack::identity))
    }

    @Test
    fun mpvMapsLoadedExternalFilenameToNativeSidWithoutDuplicateRow() {
        val external = subtitle(7)
        val embedded = MpvNativeTrack(
            id = 2,
            type = "sub",
            title = "Signs",
            language = "en",
            selected = false,
        )
        val loadedExternal = MpvNativeTrack(
            id = 9,
            type = "sub",
            title = "downloaded",
            language = null,
            selected = true,
            externalFilename = external.url,
        )

        val projected = projectMpvSubtitleTracks(
            advertised = listOf(external),
            nativeTracks = listOf(embedded, loadedExternal),
            selectedIdentity = mpvExternalSubtitleIdentity(external),
        )

        assertEquals(2, projected.size)
        assertEquals(9, projected.single { it.identity == mpvExternalSubtitleIdentity(external) }.nativeId)
        assertEquals("English 7", projected.single { it.nativeId == 9 }.title)
        assertEquals(2, projected.single { it.identity == "native:sub:2" }.nativeId)
        assertEquals(1, projected.count { it.nativeId == 9 })
    }

    @Test
    fun mpvSubtitleSelectionSuppressesInFlightAddsThenRetriesAndReusesNativeSid() {
        val external = subtitle(4)
        val identity = mpvExternalSubtitleIdentity(external)
        val unloaded = projectMpvSubtitleTracks(listOf(external), emptyList(), selectedIdentity = null)

        assertEquals(
            MpvSubtitleSelectionAction.AddExternal(external),
            resolveMpvSubtitleSelection(identity, unloaded, listOf(external), emptySet()),
        )
        assertEquals(
            MpvSubtitleSelectionAction.AwaitExternal,
            resolveMpvSubtitleSelection(identity, unloaded, listOf(external), setOf(identity)),
        )
        assertTrue(isMpvSubtitleRequestPending(deadlineMs = 35_100L, nowMs = 100L))
        assertFalse(isMpvSubtitleRequestPending(deadlineMs = 35_100L, nowMs = 35_100L))
        assertFalse(isMpvSubtitleRequestPending(deadlineMs = null, nowMs = 100L))
        assertEquals(
            MpvSubtitleSelectionAction.AddExternal(external),
            resolveMpvSubtitleSelection(identity, unloaded, listOf(external), emptySet()),
        )

        val loaded = projectMpvSubtitleTracks(
            advertised = listOf(external),
            nativeTracks = listOf(MpvNativeTrack(12, "sub", null, "en", true, external.url)),
            selectedIdentity = identity,
        )
        assertEquals(
            MpvSubtitleSelectionAction.SelectNative(12),
            resolveMpvSubtitleSelection(identity, loaded, listOf(external), emptySet()),
        )
        assertIs<MpvSubtitleSelectionAction.Disable>(
            resolveMpvSubtitleSelection(null, loaded, listOf(external), emptySet()),
        )

        val embedded = projectMpvSubtitleTracks(
            advertised = emptyList(),
            nativeTracks = listOf(MpvNativeTrack(3, "sub", "Embedded", null, false)),
            selectedIdentity = null,
        )
        assertEquals(
            MpvSubtitleSelectionAction.SelectNative(3),
            resolveMpvSubtitleSelection("native:sub:3", embedded, emptyList(), emptySet()),
        )
    }

    private fun playerPresentationForPolicy(
        engine: EmbeddedPlayerEngine,
        fallbackAllowed: Boolean,
    ) = PlayerPresentation(
        key = "policy",
        sessionId = java.util.UUID.randomUUID(),
        titleId = java.util.UUID.randomUUID(),
        title = "Title",
        mediaType = "movie",
        resourceId = "title",
        mediaUrl = "https://media.example/video",
        protocol = "http",
        container = "mkv",
        mediaTimeline = null,
        startPositionMs = 0L,
        timelineStartPositionMs = 0L,
        durationSeconds = 100,
        expectedProgressVersion = 0L,
        engine = engine,
        fallbackAllowed = fallbackAllowed,
    )

    private fun subtitle(index: Int) = PlayerSubtitlePresentation(
        id = "subtitle-$index",
        label = "English $index",
        language = "en",
        url = "https://media.example/subtitles/$index.vtt",
    )

    private fun marker(
        type: PlaybackMarkerType,
        startSeconds: Double,
        endSeconds: Double,
    ) = PlaybackMarker(
        type = type,
        startSeconds = startSeconds,
        endSeconds = endSeconds,
        confidence = 1.0,
        submissionCount = 1,
    )
}
