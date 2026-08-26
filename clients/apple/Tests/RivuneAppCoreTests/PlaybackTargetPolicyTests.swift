import Foundation
import XCTest
import RivuneAPI
@testable import RivuneAppCore

final class PlaybackTargetPolicyTests: XCTestCase {
    func testOnlyLiveTVPlaybackStaysScopedToItsSourceAddon() {
        let addonId = UUID(uuidString: "44444444-4444-4444-8444-444444444444")!

        for mediaType in ["movie", "series", "episode"] {
            XCTAssertNil(target(mediaType: mediaType, sourceAddonId: addonId).playbackAddonId)
        }
        XCTAssertEqual(target(mediaType: "tv", sourceAddonId: addonId).playbackAddonId, addonId)
    }

    func testAutomaticEngineUsesApplePlayerOnlyForNativeContainers() {
        XCTAssertEqual(
            RivunePlaybackEnginePolicy.selection(for: .automatic, protocol: "hls", container: "mpegts"),
            RivunePlaybackEngineSelection(engine: .native, fallbackAllowed: true)
        )
        XCTAssertEqual(
            RivunePlaybackEnginePolicy.selection(for: .automatic, protocol: "http", container: "mp4"),
            RivunePlaybackEngineSelection(engine: .native, fallbackAllowed: true)
        )
        XCTAssertEqual(
            RivunePlaybackEnginePolicy.selection(for: .automatic, protocol: "http", container: "mkv"),
            RivunePlaybackEngineSelection(engine: .mpv, fallbackAllowed: true)
        )
        XCTAssertEqual(
            RivunePlaybackEnginePolicy.selection(for: .automatic, protocol: "dash", container: "webm"),
            RivunePlaybackEngineSelection(engine: .mpv, fallbackAllowed: true)
        )
    }

    func testExplicitEnginePreferenceDisablesAutomaticFallback() {
        XCTAssertEqual(
            RivunePlaybackEnginePolicy.selection(for: .native, protocol: "http", container: "mkv"),
            RivunePlaybackEngineSelection(engine: .native, fallbackAllowed: false)
        )
        XCTAssertEqual(
            RivunePlaybackEnginePolicy.selection(for: .mpv, protocol: "hls", container: "mp4"),
            RivunePlaybackEngineSelection(engine: .mpv, fallbackAllowed: false)
        )
    }

    func testMPVAndAutomaticFallbackPreserveOriginalSource() {
        XCTAssertTrue(RivunePlaybackEnginePolicy.preservesOriginalSource(
            for: RivunePlaybackEngineSelection(engine: .mpv, fallbackAllowed: false),
            externally: false
        ))
        XCTAssertTrue(RivunePlaybackEnginePolicy.preservesOriginalSource(
            for: RivunePlaybackEngineSelection(engine: .native, fallbackAllowed: true),
            externally: false
        ))
        XCTAssertFalse(RivunePlaybackEnginePolicy.preservesOriginalSource(
            for: RivunePlaybackEngineSelection(engine: .native, fallbackAllowed: false),
            externally: false
        ))
        XCTAssertTrue(RivunePlaybackEnginePolicy.preservesOriginalSource(
            for: RivunePlaybackEngineSelection(engine: .native, fallbackAllowed: false),
            externally: true
        ))
    }

    func testMPVCapabilitiesAddContainersAndAudioWithoutOverclaimingVideoCodecs() {
        let native = RivuneAppModel.playbackCapabilities(for: .balanced, player: .rivune, embedded: .native)
        let mpv = RivuneAppModel.playbackCapabilities(for: .balanced, player: .rivune, embedded: .mpv)

        XCTAssertFalse(native.containers.contains("mkv"))
        XCTAssertEqual(native.streamingProtocols, ["hls"])
        XCTAssertTrue(mpv.streamingProtocols.contains("http"))
        XCTAssertTrue(mpv.streamingProtocols.contains("dash"))
        XCTAssertTrue(mpv.containers.contains("mkv"))
        XCTAssertTrue(mpv.audioCodecs?.contains("truehd") == true)
        XCTAssertEqual(mpv.videoCodecs, native.videoCodecs)
        XCTAssertEqual(native.hlsSegmentContainer, "mp4")
        XCTAssertEqual(native.mediaProfiles?.count, 2)
        XCTAssertEqual(native.mediaProfiles?.first?.maximumVideoBitDepth, 8)
        XCTAssertNil(mpv.mediaProfiles)
        XCTAssertNil(mpv.hlsSegmentContainer)
        XCTAssertEqual(mpv.maximumHeight, 1080)
        XCTAssertEqual(mpv.maximumVideoBitrateKbps, 8_000)
    }

    func testSimulatorWithoutMPVUsesNativePlaybackAndCapabilities() {
        for preference in [RivuneEmbeddedPlayerPreference.automatic, .mpv] {
            XCTAssertEqual(
                RivunePlaybackEnginePolicy.selection(
                    for: preference,
                    protocol: "http",
                    container: "mkv",
                    embeddedMPVSupported: false
                ),
                RivunePlaybackEngineSelection(engine: .native, fallbackAllowed: false)
            )
        }

        let capabilities = RivuneAppModel.playbackCapabilities(
            for: .balanced,
            player: .rivune,
            embedded: .automatic,
            embeddedMPVSupported: false
        )
        XCTAssertFalse(capabilities.containers.contains("mkv"))
        XCTAssertFalse(capabilities.audioCodecs?.contains("truehd") == true)
        XCTAssertEqual(capabilities.hlsSegmentContainer, "mp4")
        XCTAssertTrue(capabilities.processingModes?.contains(.remux) == true)
        XCTAssertTrue(capabilities.processingModes?.contains(.transcode) == true)
    }

    func testRelativeMediaTimelineKeepsAbsoluteProgressCoordinates() {
        let presentation = playbackPresentation(
            startSeconds: 145,
            timelineStartSeconds: 120,
            mediaTimeline: .relative,
            durationSeconds: 3_600
        )

        XCTAssertEqual(presentation.timelineOffsetSeconds, 120)
        XCTAssertEqual(presentation.mediaPlaybackPosition(absoluteSeconds: 145), 25)
        XCTAssertEqual(presentation.absolutePlaybackPosition(mediaSeconds: 25), 145)
        XCTAssertEqual(presentation.resolvedPlaybackDuration(mediaDurationSeconds: 3_480), 3_600)
    }

    func testAbsoluteMediaTimelineDoesNotApplyAResumeOffset() {
        let presentation = playbackPresentation(
            startSeconds: 145,
            timelineStartSeconds: 120,
            mediaTimeline: .absolute,
            durationSeconds: nil
        )

        XCTAssertEqual(presentation.timelineOffsetSeconds, 0)
        XCTAssertEqual(presentation.mediaPlaybackPosition(absoluteSeconds: 145), 145)
        XCTAssertEqual(presentation.absolutePlaybackPosition(mediaSeconds: 25), 25)
        XCTAssertEqual(presentation.resolvedPlaybackDuration(mediaDurationSeconds: 3_480), 3_480)
    }

    private func playbackPresentation(
        startSeconds: Int,
        timelineStartSeconds: Int,
        mediaTimeline: PlaybackMediaTimeline?,
        durationSeconds: Int?
    ) -> RivunePlaybackPresentation {
        RivunePlaybackPresentation(
            id: UUID(),
            sessionId: UUID(),
            sourceRef: "source-ref",
            titleId: UUID(),
            title: "Title",
            url: URL(string: "https://media.example/video.m3u8")!,
            engine: .native,
            fallbackAllowed: false,
            startSeconds: startSeconds,
            timelineStartSeconds: timelineStartSeconds,
            mediaTimeline: mediaTimeline,
            videoAspect: .fit,
            playbackSpeed: 1,
            markers: [],
            durationSeconds: durationSeconds,
            expectedVersion: 0,
            audioTracks: [],
            subtitles: [],
            selectedAudioTrack: nil,
            selectedSubtitleId: nil,
            decisionReasons: [.containerNotSupported],
            coordinatedItem: nil,
            sourceAddonId: nil,
            nextEpisode: nil
        )
    }

    private func target(mediaType: String, sourceAddonId: UUID?) -> RivuneMediaTarget {
        RivuneMediaTarget(
            id: "resource",
            resourceId: "resource",
            mediaType: mediaType,
            title: "Title",
            titleId: nil,
            provider: nil,
            externalId: nil,
            externalIds: [:],
            sourceAddonId: sourceAddonId,
            sourceCatalogId: nil,
            sourceName: nil,
            posterUrl: nil,
            backgroundUrl: nil,
            logoUrl: nil,
            overview: nil,
            releaseInfo: nil,
            released: nil,
            seriesId: nil,
            seasonId: nil,
            seasonNumber: nil,
            episodeNumber: nil,
            runtimeMinutes: nil
        )
    }
}
