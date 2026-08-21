import Foundation
import XCTest
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
        XCTAssertTrue(mpv.containers.contains("mkv"))
        XCTAssertTrue(mpv.audioCodecs?.contains("truehd") == true)
        XCTAssertEqual(mpv.videoCodecs, native.videoCodecs)
        XCTAssertEqual(mpv.maximumHeight, 1080)
        XCTAssertEqual(mpv.maximumVideoBitrateKbps, 12_000)
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
