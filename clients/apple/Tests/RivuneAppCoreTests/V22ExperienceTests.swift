import Foundation
import XCTest
@testable import RivuneAPI
@testable import RivuneAppCore

#if canImport(FoundationNetworking)
import FoundationNetworking
#endif

final class V22ExperienceTests: XCTestCase {
    func testQualityTableAndNetworkClassification() {
        XCTAssertEqual(
            RivuneNetworkQualityPolicy.limit(quality: .economy, networkClass: .local),
            RivuneQualityLimit(maximumHeight: 480, maximumVideoBitrateKbps: 2_000))
        XCTAssertEqual(
            RivuneNetworkQualityPolicy.limit(quality: .balanced, networkClass: .remoteWifi),
            RivuneQualityLimit(maximumHeight: 1080, maximumVideoBitrateKbps: 8_000))
        XCTAssertEqual(
            RivuneNetworkQualityPolicy.limit(quality: .automatic, networkClass: .mobile),
            RivuneQualityLimit(maximumHeight: 720, maximumVideoBitrateKbps: 5_000))
        XCTAssertEqual(
            RivuneNetworkQualityPolicy.limit(quality: .automatic, networkClass: .local),
            RivuneQualityLimit(maximumHeight: nil, maximumVideoBitrateKbps: nil))
        XCTAssertEqual(
            RivuneAppModel.classifyNetwork(
                cellular: false, expensive: false, constrained: false,
                serverOrigin: URL(string: "http://192.168.1.10")),
            .local)
        XCTAssertEqual(
            RivuneAppModel.classifyNetwork(
                cellular: false, expensive: false, constrained: false,
                serverOrigin: URL(string: "https://media.example")),
            .remoteWifi)
        XCTAssertEqual(
            RivuneAppModel.classifyNetwork(
                cellular: false, expensive: false, constrained: true,
                serverOrigin: URL(string: "http://192.168.1.10")),
            .mobile)
    }

    @MainActor
    func testLegacyQualityKeysMigrateOnceAndAreRemoved() throws {
        let suite = "V22Quality.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        defaults.set("balanced", forKey: "rivune.playback.wifi-quality")
        defaults.set("economy", forKey: "rivune.playback.mobile-quality")

        let model = RivuneAppModel(
            defaults: defaults,
            updateChecker: V22UpdateChecker(),
            applicationVersion: "test")

        XCTAssertEqual(model.localQuality, .balanced)
        XCTAssertEqual(model.remoteWifiQuality, .balanced)
        XCTAssertEqual(model.mobileQuality, .economy)
        XCTAssertNil(defaults.object(forKey: "rivune.playback.wifi-quality"))
        XCTAssertNil(defaults.object(forKey: "rivune.playback.mobile-quality"))
        XCTAssertEqual(defaults.string(forKey: "rivune.quality.local"), "balanced")
        XCTAssertEqual(defaults.string(forKey: "rivune.quality.remote-wifi"), "balanced")
        XCTAssertEqual(defaults.string(forKey: "rivune.quality.mobile"), "economy")
    }

    func testPlaybackDecisionRequiresClosedUniqueReasons() throws {
        let valid = Data(#"{"reason":"video_transcode_required","reasons":["resolution_limit","bitrate_limit"],"videoAction":"transcode","audioAction":"copy","subtitleAction":"none","toneMapping":false}"#.utf8)
        XCTAssertEqual(
            try JSONDecoder().decode(PlaybackDecision.self, from: valid).reasons,
            [.resolutionLimit, .bitrateLimit])

        let missing = Data(#"{"reason":"direct_supported","videoAction":"copy","audioAction":"copy","subtitleAction":"none","toneMapping":false}"#.utf8)
        XCTAssertThrowsError(try JSONDecoder().decode(PlaybackDecision.self, from: missing))
        let duplicate = Data(#"{"reason":"video_transcode_required","reasons":["bitrate_limit","bitrate_limit"],"videoAction":"transcode","audioAction":"copy","subtitleAction":"none","toneMapping":false}"#.utf8)
        XCTAssertThrowsError(try JSONDecoder().decode(PlaybackDecision.self, from: duplicate))
        let unsafe = Data(#"{"reason":"video_transcode_required","reasons":["https://provider.example/?token=secret"],"videoAction":"transcode","audioAction":"copy","subtitleAction":"none","toneMapping":false}"#.utf8)
        XCTAssertThrowsError(try JSONDecoder().decode(PlaybackDecision.self, from: unsafe))
    }

    func testProfileArchiveV2ValidationIsBounded() throws {
        let archive = try ProfileArchiveDocument(data: Data(#"{"version":2,"identity":{"name":"Viewer","isChild":false,"avatar":{"kind":"preset","presetId":"ember"}},"addons":[],"continueDismissals":[]}"#.utf8))
        XCTAssertEqual(try archive.encodedData().isEmpty, false)
        XCTAssertThrowsError(try ProfileArchiveDocument(data: Data(#"{"version":1,"identity":{}}"#.utf8)))
        XCTAssertThrowsError(
            try ProfileArchiveDocument(data: Data(repeating: 0, count: ProfileArchiveDocument.maximumBytes + 1)))
    }

    @MainActor
    func testSearchDeduplicationIndexesTenThousandItems() throws {
        let suite = "V22Search.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let model = RivuneAppModel(
            defaults: defaults,
            updateChecker: V22UpdateChecker(),
            applicationVersion: "test")
        let items = (0..<10_000).map { index in
            searchTarget(index: index, identity: index % 9_000)
        }
        XCTAssertTrue(model.appendSearchItems(items))
        XCTAssertEqual(model.searchItems.count, 9_000)
        XCTAssertEqual(model.searchItems.first?.title, "Title 0")
    }

    func testSearchTypeFanoutStableDeduplicatesAndCapsAtSixteen() {
        let configured = [" Movie ", "series", "movie", "ANIME"]
            + (0..<20).map { "type-\($0)" }
        let bounded = RivuneAppModel.boundedSearchTypes(configured)
        XCTAssertEqual(Array(bounded.prefix(3)), ["movie", "series", "anime"])
        XCTAssertEqual(bounded.count, 16)
        XCTAssertEqual(Set(bounded).count, bounded.count)
    }

    func testCoordinationClockUsesActiveRecentAndIdleCadences() {
        XCTAssertEqual(
            RivuneAppModel.coordinationPollIntervalNanoseconds(
                hasActiveWork: true, recentActivityUntil: 0, now: 100),
            2_000_000_000)
        XCTAssertEqual(
            RivuneAppModel.coordinationPollIntervalNanoseconds(
                hasActiveWork: false, recentActivityUntil: 101, now: 100),
            2_000_000_000)
        XCTAssertEqual(
            RivuneAppModel.coordinationPollIntervalNanoseconds(
                hasActiveWork: false, recentActivityUntil: 100, now: 100),
            15_000_000_000)
        XCTAssertEqual(
            RivuneAppModel.coordinationLoopDelayNanoseconds(
                commandInterval: 2_000_000_000,
                lastPresence: 0,
                now: 14_000_000_000),
            1_000_000_000)
        XCTAssertEqual(
            RivuneAppModel.coordinationLoopDelayNanoseconds(
                commandInterval: 15_000_000_000,
                lastPresence: 15_000_000_000,
                now: 15_000_000_000),
            15_000_000_000)
    }

    @MainActor
    func testCoordinationLifecycleSuspendsOutsideActiveScene() throws {
        let suite = "V22Coordination.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let model = RivuneAppModel(
            defaults: defaults, updateChecker: V22UpdateChecker(), applicationVersion: "test")
        XCTAssertTrue(model.coordinationIsForeground)
        model.handleSceneBackground()
        XCTAssertFalse(model.coordinationIsForeground)
        XCTAssertFalse(model.coordinationPollingActive)
        model.handleSceneActive()
        XCTAssertTrue(model.coordinationIsForeground)
    }

    func testOfflineQuotaIsGlobalAcrossProfilesAndExpirationReconciles() async throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("V22Offline.\(UUID().uuidString)", isDirectory: true)
        defer { try? FileManager.default.removeItem(at: root) }
        let scopeA = try XCTUnwrap(RivuneOfflineMediaScope(
            serverOrigin: URL(string: "https://one.example")!, profileID: UUID()))
        let scopeB = try XCTUnwrap(RivuneOfflineMediaScope(
            serverOrigin: URL(string: "https://two.example")!, profileID: UUID()))
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [V22DownloadProtocol.self]
        let store = RivuneOfflineMediaStore(
            rootDirectory: root, maximumStoredBytes: 100, expirationDays: 30,
            now: { Date(timeIntervalSince1970: 1_000) })

        _ = try await store.download(
            from: URL(string: "https://download.test/media")!, titleId: UUID(), title: "First",
            container: "mp4", posterURL: nil, in: scopeA, configuration: configuration,
            progress: { _ in })
        do {
            _ = try await store.download(
                from: URL(string: "https://download.test/media")!, titleId: UUID(), title: "Second",
                container: "mp4", posterURL: nil, in: scopeB, configuration: configuration,
                progress: { _ in })
            XCTFail("The device-wide quota accepted a second archive")
        } catch RivuneOfflineMediaError.quotaExceeded {
        }

        let directory = try await store.storageDirectory(for: scopeB)
        let expired = RivuneOfflineMediaItem(
            id: UUID(), titleId: UUID(), title: "Expired", fileName: "expired.rvn",
            container: "mp4", sizeBytes: 1, createdAt: Date(timeIntervalSince1970: 0),
            expiresAt: Date(timeIntervalSince1970: 999), state: .ready, posterURL: nil)
        try Data([1]).write(to: directory.appendingPathComponent(expired.fileName))
        try await store.saveManifest([expired], in: scopeB)
        let remaining = await store.items(in: scopeB)
        XCTAssertTrue(remaining.isEmpty)
    }

    func testExplicitOfflineRepairScansLargeDeviceStoreOnce() async throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("V22OfflineRepair.\(UUID().uuidString)", isDirectory: true)
        defer { try? FileManager.default.removeItem(at: root) }
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        for index in 0..<10_000 {
            try Data([UInt8(index % 251)]).write(
                to: root.appendingPathComponent("\(index).rvn"))
        }
        let store = RivuneOfflineMediaStore(rootDirectory: root)
        let firstRepair = try await store.repairGlobalUsage()
        let secondRepair = try await store.repairGlobalUsage()
        XCTAssertEqual(firstRepair, 10_000)
        XCTAssertEqual(secondRepair, 10_000)
    }

    private func searchTarget(index: Int, identity: Int) -> RivuneSearchItem {
        RivuneMediaTarget(
            id: "item-\(index)", resourceId: "resource-\(index)", mediaType: "movie",
            title: "Title \(index)", titleId: nil, provider: "tmdb",
            externalId: String(identity), externalIds: ["tmdb": String(identity)],
            sourceAddonId: nil, sourceCatalogId: nil, sourceName: nil,
            posterUrl: nil, backgroundUrl: nil, logoUrl: nil, overview: nil,
            releaseInfo: nil, released: nil, seriesId: nil, mappingProvider: nil,
            episodeOrderId: nil, metadataSeasonId: nil, seasonId: nil,
            seasonNumber: nil, episodeNumber: nil, runtimeMinutes: nil)
    }

}

private struct V22UpdateChecker: RivuneAppleUpdateChecking {
    func check(currentVersion: String) async throws -> RivuneAppleUpdateCheckResult {
        .upToDate(currentVersion: currentVersion, latestVersion: currentVersion)
    }
}

private final class V22DownloadProtocol: URLProtocol {
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        let response = HTTPURLResponse(
            url: request.url!, statusCode: 200, httpVersion: nil,
            headerFields: ["Content-Type": "video/mp4"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data([1, 2, 3, 4]))
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}
