import CryptoKit
import Foundation
import XCTest
@testable import RivuneAppCore

final class OfflineMediaStoreTests: XCTestCase {
    func testEncryptedArchiveRoundTripsAcrossChunkBoundaryAndRejectsTamperedHeader() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let archive = directory.appendingPathComponent("media.rvn")
        let key = SymmetricKey(size: .bits256)
        let plaintext = Data((0..<(1_048_576 + 257)).map { UInt8($0 % 251) })

        let writer = try RivuneEncryptedMediaWriter(url: archive, key: key, maximumBytes: Int64(plaintext.count))
        try writer.append(plaintext.prefix(700_000))
        try writer.append(plaintext.dropFirst(700_000))
        XCTAssertEqual(try writer.finish(), Int64(plaintext.count))

        let reader = try RivuneEncryptedMediaReader(url: archive, key: key)
        XCTAssertEqual(try reader.read(offset: 1_048_500, count: 300), plaintext[1_048_500..<1_048_800])

        var bytes = try Data(contentsOf: archive)
        let validArchive = bytes
        bytes[12] ^= 0x01
        try bytes.write(to: archive, options: .atomic)
        XCTAssertThrowsError(try RivuneEncryptedMediaReader(url: archive, key: key))

        try validArchive.dropLast().write(to: archive, options: .atomic)
        XCTAssertThrowsError(try RivuneEncryptedMediaReader(url: archive, key: key))
    }

    func testEncryptedWriterEnforcesQuotaDuringStreaming() throws {
        let archive = FileManager.default.temporaryDirectory.appendingPathComponent("\(UUID().uuidString).rvn")
        defer { try? FileManager.default.removeItem(at: archive) }
        let writer = try RivuneEncryptedMediaWriter(url: archive, key: SymmetricKey(size: .bits256), maximumBytes: 4)
        try writer.append(Data([1, 2, 3, 4]))
        XCTAssertThrowsError(try writer.append(Data([5])))
        try writer.cancel()
    }

    func testScopeIsStableForNormalizedOriginAndIsolatedByOriginAndProfile() throws {
        let profile = UUID()
        let normalized = try XCTUnwrap(RivuneOfflineMediaScope(serverOrigin: XCTUnwrap(URL(string: "https://EXAMPLE.com:443/path?token=secret")), profileID: profile))
        let equivalent = try XCTUnwrap(RivuneOfflineMediaScope(serverOrigin: XCTUnwrap(URL(string: "https://example.com")), profileID: profile))
        let otherOrigin = try XCTUnwrap(RivuneOfflineMediaScope(serverOrigin: XCTUnwrap(URL(string: "https://other.example.com")), profileID: profile))
        let otherProfile = try XCTUnwrap(RivuneOfflineMediaScope(serverOrigin: XCTUnwrap(URL(string: "https://example.com")), profileID: UUID()))

        XCTAssertEqual(normalized, equivalent)
        XCTAssertNotEqual(normalized, otherOrigin)
        XCTAssertNotEqual(normalized, otherProfile)
        XCTAssertEqual(normalized.identifier.count, 64)
        XCTAssertFalse(normalized.identifier.contains("example"))
        XCTAssertFalse(normalized.identifier.contains(profile.uuidString.lowercased()))
        XCTAssertEqual(RivuneOfflineMediaScope(identifier: normalized.identifier), normalized)
        XCTAssertNil(RivuneOfflineMediaScope(identifier: "../other-scope"))
    }

    func testArchiveMetadataCannotCrossScopes() async throws {
        let profile = UUID()
        let firstScope = try XCTUnwrap(RivuneOfflineMediaScope(serverOrigin: URL(string: "https://first.example")!, profileID: profile))
        let secondScope = try XCTUnwrap(RivuneOfflineMediaScope(serverOrigin: URL(string: "https://second.example")!, profileID: profile))
        let store = RivuneOfflineMediaStore()
        let firstDirectory = try await store.storageDirectory(for: firstScope)
        let secondDirectory = try await store.storageDirectory(for: secondScope)
        defer {
            try? FileManager.default.removeItem(at: firstDirectory)
            try? FileManager.default.removeItem(at: secondDirectory)
        }
        let item = RivuneOfflineMediaItem(
            id: UUID(), titleId: UUID(), title: "Scoped title", fileName: "scoped.rvn",
            container: "mp4", sizeBytes: 4, createdAt: Date(), posterURL: nil
        )
        try Data([1, 2, 3, 4]).write(to: firstDirectory.appendingPathComponent(item.fileName))
        try await store.saveManifest([item], in: firstScope)

        let firstItems = await store.items(in: firstScope)
        let secondItems = await store.items(in: secondScope)
        XCTAssertEqual(firstItems.map(\.id), [item.id])
        XCTAssertEqual(firstItems.first?.expirationPolicyDays, 30)
        XCTAssertTrue(secondItems.isEmpty)
        do {
            _ = try await store.playbackURL(for: item, in: secondScope)
            XCTFail("An item from another scope was accepted")
        } catch RivuneOfflineMediaError.invalidArchive {
        }
        do {
            try await store.remove(item, in: secondScope)
            XCTFail("An item from another scope was removable")
        } catch RivuneOfflineMediaError.invalidArchive {
        }
    }

    func testReconciliationRemovesMissingQuotaAndOrphanFinalBeforeDownload() async throws {
        let scope = try XCTUnwrap(RivuneOfflineMediaScope(serverOrigin: URL(string: "https://reconcile.example")!, profileID: UUID()))
        let store = RivuneOfflineMediaStore()
        let directory = try await store.storageDirectory(for: scope)
        defer { try? FileManager.default.removeItem(at: directory) }
        let missing = RivuneOfflineMediaItem(
            id: UUID(), titleId: UUID(), title: "Missing", fileName: "missing.rvn",
            container: "mp4", sizeBytes: 20 * 1024 * 1024 * 1024, createdAt: Date(), posterURL: nil
        )
        try await store.saveManifest([missing], in: scope)
        let orphan = directory.appendingPathComponent("orphan.rvn")
        try Data([9, 9, 9]).write(to: orphan)
        let orphanPartial = directory.appendingPathComponent(".orphan.partial")
        try Data([8, 8]).write(to: orphanPartial)

        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [OfflineDownloadURLProtocol.self]
        let downloaded = try await store.download(
            from: URL(string: "https://download.test/complete")!,
            titleId: UUID(),
            title: "Downloaded",
            container: "mp4",
            posterURL: nil,
            in: scope,
            configuration: configuration,
            progress: { _ in }
        )
        let items = await store.items(in: scope)

        XCTAssertEqual(items, [downloaded])
        XCTAssertFalse(FileManager.default.fileExists(atPath: orphan.path))
        XCTAssertFalse(FileManager.default.fileExists(atPath: orphanPartial.path))
        XCTAssertFalse(items.contains(missing))
    }

    @MainActor
    func testRememberedProtectedScopeStaysLockedUntilLocalPINVerification() async throws {
        let suite = "OfflineStartup.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let scope = try XCTUnwrap(RivuneOfflineMediaScope(serverOrigin: URL(string: "https://offline.example")!, profileID: UUID()))
        let access = try RivuneOfflineProfileAccess(name: "Protected", scope: scope, pin: "4826")
        let encoded = try JSONEncoder().encode([access])
        defaults.set(encoded, forKey: "rivune.offline.profiles")
        XCTAssertFalse(String(decoding: encoded, as: UTF8.self).contains("4826"))
        let item = RivuneOfflineMediaItem(
            id: UUID(), titleId: UUID(), title: "Available offline", fileName: "startup.rvn",
            container: "mp4", sizeBytes: 12, createdAt: Date(), posterURL: nil
        )
        let directory = try await RivuneOfflineMediaStore.shared.storageDirectory(for: scope)
        defer { try? FileManager.default.removeItem(at: directory) }
        try await RivuneOfflineMediaStore.shared.saveManifest([item], in: scope)
        try Data(repeating: 7, count: 12).write(to: directory.appendingPathComponent(item.fileName))

        let model = offlineTestModel(defaults: defaults)
        model.start()
        for _ in 0..<100 where model.offlineProfiles.isEmpty {
            try await Task.sleep(nanoseconds: 1_000_000)
        }
        XCTAssertEqual(model.destination, .server)
        XCTAssertEqual(model.offlineProfiles, [access])
        XCTAssertTrue(model.offlineItems.isEmpty)
        XCTAssertFalse(model.offlineAccessUnlocked)

        model.requestOfflineUnlock(access)
        XCTAssertEqual(model.pendingOfflineProfile, access)
        model.dismissOfflineUnlock()
        XCTAssertNil(model.pendingOfflineProfile)
        XCTAssertFalse(model.offlineAccessUnlocked)

        model.requestOfflineUnlock(access)
        model.unlockOfflineProfile(access, pin: "0000")
        XCTAssertTrue(model.offlineItems.isEmpty)
        XCTAssertFalse(model.offlineAccessUnlocked)
        XCTAssertEqual(model.pendingOfflineProfile, access)
        XCTAssertEqual(model.offlineUnlockFailure, .invalidPin)

        model.unlockOfflineProfile(access, pin: "4826")
        for _ in 0..<100 where model.offlineItems.isEmpty {
            try await Task.sleep(nanoseconds: 1_000_000)
        }
        XCTAssertTrue(model.offlineAccessUnlocked)
        XCTAssertEqual(model.offlineItems.map(\.id), [item.id])
        XCTAssertEqual(model.offlineItems.first?.expirationPolicyDays, 30)

        model.handleSceneBackground()
        XCTAssertFalse(model.offlineAccessUnlocked)
        XCTAssertTrue(model.offlineItems.isEmpty)
        XCTAssertEqual(model.pendingOfflineProfile, access)
        XCTAssertEqual(model.destination, .server)
        model.dismissOfflineUnlock()
        XCTAssertNil(model.pendingOfflineProfile)
    }

    @MainActor
    func testBackgroundLeavesUnprotectedOfflineScopeUnlocked() async throws {
        let suite = "OfflineUnprotected.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let scope = try XCTUnwrap(RivuneOfflineMediaScope(serverOrigin: URL(string: "https://open.example")!, profileID: UUID()))
        let access = try RivuneOfflineProfileAccess(name: "Open", scope: scope, pin: nil)
        let item = RivuneOfflineMediaItem(
            id: UUID(), titleId: UUID(), title: "Open media", fileName: "open.rvn",
            container: "mp4", sizeBytes: 4, createdAt: Date(), posterURL: nil
        )
        let directory = try await RivuneOfflineMediaStore.shared.storageDirectory(for: scope)
        defer { try? FileManager.default.removeItem(at: directory) }
        try Data([1, 2, 3, 4]).write(to: directory.appendingPathComponent(item.fileName))
        try await RivuneOfflineMediaStore.shared.saveManifest([item], in: scope)

        let model = offlineTestModel(defaults: defaults)
        model.unlockOfflineProfile(access)
        for _ in 0..<100 where model.offlineItems.isEmpty { try await Task.sleep(nanoseconds: 1_000_000) }
        model.handleSceneBackground()

        XCTAssertTrue(model.offlineAccessUnlocked)
        XCTAssertEqual(model.offlineItems.map(\.id), [item.id])
        XCTAssertEqual(model.offlineItems.first?.expirationPolicyDays, 30)
        XCTAssertNil(model.pendingOfflineProfile)
    }

    @MainActor
    func testBackgroundRelocksTransientProtectedAccessBeforeFirstDownload() throws {
        let suite = "OfflineTransient.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let scope = try XCTUnwrap(RivuneOfflineMediaScope(serverOrigin: URL(string: "https://transient.example")!, profileID: UUID()))
        let access = try RivuneOfflineProfileAccess(name: "Transient", scope: scope, pin: "2468")
        let model = offlineTestModel(defaults: defaults)

        model.unlockOfflineProfile(access, pin: "2468")
        XCTAssertTrue(model.offlineAccessUnlocked)
        model.handleSceneBackground()

        XCTAssertFalse(model.offlineAccessUnlocked)
        XCTAssertTrue(model.offlineItems.isEmpty)
        XCTAssertNil(model.pendingOfflineProfile)
    }

    @MainActor
    func testRememberedProfileWithoutReadableArchiveIsNotOffered() async throws {
        let suite = "OfflineEmpty.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let scope = try XCTUnwrap(RivuneOfflineMediaScope(serverOrigin: URL(string: "https://empty.example")!, profileID: UUID()))
        let access = try RivuneOfflineProfileAccess(name: "Empty", scope: scope, pin: "1234")
        defaults.set(try JSONEncoder().encode([access]), forKey: "rivune.offline.profiles")
        let item = RivuneOfflineMediaItem(
            id: UUID(), titleId: UUID(), title: "Missing", fileName: "missing.rvn",
            container: "mp4", sizeBytes: 4, createdAt: Date(), posterURL: nil
        )
        let directory = try await RivuneOfflineMediaStore.shared.storageDirectory(for: scope)
        defer { try? FileManager.default.removeItem(at: directory) }
        try await RivuneOfflineMediaStore.shared.saveManifest([item], in: scope)

        let model = offlineTestModel(defaults: defaults)
        model.start()
        for _ in 0..<20 { await Task.yield() }

        XCTAssertTrue(model.offlineProfiles.isEmpty)
        XCTAssertTrue(model.offlineItems.isEmpty)
    }

    func testPINVerifierUsesSaltAndConstantTimeComparison() throws {
        let first = try RivuneOfflinePINVerifier.randomSalt()
        let second = try RivuneOfflinePINVerifier.randomSalt()
        let firstVerifier = RivuneOfflinePINVerifier.derive(pin: "1234", salt: first)
        let secondVerifier = RivuneOfflinePINVerifier.derive(pin: "1234", salt: second)

        XCTAssertNotEqual(first, second)
        XCTAssertNotEqual(firstVerifier, secondVerifier)
        XCTAssertTrue(RivuneOfflinePINVerifier.matches(pin: "1234", salt: first, verifier: firstVerifier))
        XCTAssertFalse(RivuneOfflinePINVerifier.matches(pin: "1235", salt: first, verifier: firstVerifier))
        XCTAssertFalse(RivuneOfflinePINVerifier.constantTimeEqual(Data([1]), Data([1, 0])))
    }

    func testUnprotectedOfflineProfileRequiresExplicitSelectionWithoutPIN() throws {
        let scope = try XCTUnwrap(RivuneOfflineMediaScope(serverOrigin: URL(string: "https://offline.example")!, profileID: UUID()))
        let access = try RivuneOfflineProfileAccess(name: "Open", scope: scope, pin: nil)

        XCTAssertFalse(access.requiresPIN)
        XCTAssertTrue(access.permits(pin: nil))
        XCTAssertFalse(access.permits(pin: "1234"))
    }

    func testStreamingDownloaderCancellationCancelsTransportAndAllowsNextDownload() async throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }

        let started = expectation(description: "underlying URL loading started")
        let cancelled = expectation(description: "underlying URL loading cancelled")
        OfflineDownloadURLProtocol.startHandler = { started.fulfill() }
        OfflineDownloadURLProtocol.stopHandler = { cancelled.fulfill() }
        let configuration = URLSessionConfiguration.ephemeral
        defer {
            OfflineDownloadURLProtocol.startHandler = nil
            OfflineDownloadURLProtocol.stopHandler = nil
        }
        configuration.protocolClasses = [OfflineDownloadURLProtocol.self]
        let firstWriter = try RivuneEncryptedMediaWriter(
            url: directory.appendingPathComponent("cancelled.rvn"),
            key: SymmetricKey(size: .bits256),
            maximumBytes: 1_024
        )
        let first = Task {
            try await RivuneStreamingDownloader.download(
                url: URL(string: "https://download.test/slow")!,
                writer: firstWriter,
                configuration: configuration,
                progress: { _ in }
            )
        }
        await fulfillment(of: [started], timeout: 1)
        first.cancel()
        do {
            try await first.value
            XCTFail("The cancelled download unexpectedly completed")
        } catch is CancellationError {
        }
        await fulfillment(of: [cancelled], timeout: 1)
        try? firstWriter.cancel()

        OfflineDownloadURLProtocol.startHandler = nil
        OfflineDownloadURLProtocol.stopHandler = nil
        let secondWriter = try RivuneEncryptedMediaWriter(
            url: directory.appendingPathComponent("next.rvn"),
            key: SymmetricKey(size: .bits256),
            maximumBytes: 1_024
        )
        try await RivuneStreamingDownloader.download(
            url: URL(string: "https://download.test/complete")!,
            writer: secondWriter,
            configuration: configuration,
            progress: { _ in }
        )
        XCTAssertEqual(try secondWriter.finish(), 4)
    }
    func testOfflinePlaybackServerStartsBeforeAcceptingConnectionsAndServesRanges() async throws {
        let scope = try XCTUnwrap(RivuneOfflineMediaScope(
            serverOrigin: URL(string: "https://playback.example")!,
            profileID: UUID()
        ))
        let store = RivuneOfflineMediaStore()
        let directory = try await store.storageDirectory(for: scope)
        defer { try? FileManager.default.removeItem(at: directory) }
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [OfflineDownloadURLProtocol.self]
        let item = try await store.download(
            from: URL(string: "https://download.test/complete")!,
            titleId: UUID(),
            title: "Playable",
            container: "mp4",
            posterURL: nil,
            in: scope,
            configuration: configuration,
            progress: { _ in }
        )

        let playbackURL = try await store.playbackURL(for: item, in: scope)
        var request = URLRequest(url: playbackURL)
        request.setValue("bytes=1-2", forHTTPHeaderField: "Range")
        let (data, response) = try await URLSession.shared.data(for: request)
        await store.stopPlayback()

        XCTAssertEqual((response as? HTTPURLResponse)?.statusCode, 206)
        XCTAssertEqual(data, Data([2, 3]))
    }
}

@MainActor
private func offlineTestModel(defaults: UserDefaults) -> RivuneAppModel {
    RivuneAppModel(
        defaults: defaults,
        updateChecker: OfflineTestUpdateChecker(),
        applicationVersion: "1.11.4"
    )
}

private struct OfflineTestUpdateChecker: RivuneAppleUpdateChecking {
    func check(currentVersion: String) async throws -> RivuneAppleUpdateCheckResult {
        .upToDate(currentVersion: currentVersion, latestVersion: currentVersion)
    }
}

private final class OfflineDownloadURLProtocol: URLProtocol {
    static var startHandler: (() -> Void)?
    static var stopHandler: (() -> Void)?
    private var workItem: DispatchWorkItem?

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        Self.startHandler?()
        let response = HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: nil)!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        if request.url?.path == "/complete" {
            client?.urlProtocol(self, didLoad: Data([1, 2, 3, 4]))
            client?.urlProtocolDidFinishLoading(self)
            return
        }
        let workItem = DispatchWorkItem { [weak self] in
            guard let self else { return }
            self.client?.urlProtocol(self, didLoad: Data(repeating: 1, count: 16))
        }
        self.workItem = workItem
        DispatchQueue.global().asyncAfter(deadline: .now() + 2, execute: workItem)
    }

    override func stopLoading() {
        workItem?.cancel()
        Self.stopHandler?()
    }
}
