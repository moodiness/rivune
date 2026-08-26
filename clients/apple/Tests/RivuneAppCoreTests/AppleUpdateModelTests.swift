import Foundation
import XCTest
@testable import RivuneAppCore

@MainActor
final class AppleUpdateModelTests: XCTestCase {
    func testManualCheckPublishesAvailableStateAndPersistsCache() async throws {
        let defaults = isolatedDefaults()
        let update = fixtureUpdate()
        let checker = StubAppleUpdateChecker(result: .success(.available(update)))
        let model = RivuneAppModel(
            defaults: defaults,
            updateChecker: checker,
            applicationVersion: "1.11.3"
        )

        model.checkForUpdates()
        try await waitUntil { model.updateState == .available(update) }

        XCTAssertEqual(model.updateNotice, update)
        XCTAssertNotNil(defaults.object(forKey: "rivune.update.last-successful-check") as? Date)
        XCTAssertEqual(defaults.string(forKey: "rivune.update.last-successful-version"), "1.11.3")
        XCTAssertNotNil(defaults.data(forKey: "rivune.update.cached-result"))
        let manualCallCount = await checker.callCount()
        XCTAssertEqual(manualCallCount, 1)
    }

    func testAutomaticAvailableUpdateNotifiesOnceThenOnlyForNewerVersion() async throws {
        let defaults = isolatedDefaults()
        let first = fixtureUpdate()
        let checker = StubAppleUpdateChecker(result: .success(.available(first)))
        let notifier = StubAppleUpdateNotifier(delivered: true)
        let model = RivuneAppModel(
            defaults: defaults,
            updateChecker: checker,
            applicationVersion: "1.11.3",
            updateNotifier: notifier
        )

        model.checkForUpdates(manual: false)
        try await waitUntil { model.updateState == .available(first) }
        XCTAssertNil(model.updateNotice)
        XCTAssertEqual(notifier.deliveredVersions(), ["1.11.4"])
        defaults.removeObject(forKey: "rivune.update.last-successful-check")
        defaults.removeObject(forKey: "rivune.update.last-successful-version")

        model.checkForUpdates(manual: false)
        for _ in 0..<20 { await Task.yield() }
        let duplicateCallCount = await checker.callCount()
        XCTAssertEqual(duplicateCallCount, 2)
        XCTAssertEqual(notifier.deliveredVersions(), ["1.11.4"])
        defaults.removeObject(forKey: "rivune.update.last-successful-check")
        defaults.removeObject(forKey: "rivune.update.last-successful-version")

        let newer = fixtureUpdate(version: "1.11.5")
        await checker.setResult(.success(.available(newer)))
        model.checkForUpdates(manual: false)
        try await waitUntil { model.updateState == .available(newer) }
        XCTAssertEqual(notifier.deliveredVersions(), ["1.11.4", "1.11.5"])
    }

    func testDeniedNotificationPermissionKeepsInAppNotice() async throws {
        let defaults = isolatedDefaults()
        let update = fixtureUpdate()
        let notifier = StubAppleUpdateNotifier(delivered: false)
        let model = RivuneAppModel(
            defaults: defaults,
            updateChecker: StubAppleUpdateChecker(result: .success(.available(update))),
            applicationVersion: "1.11.3",
            updateNotifier: notifier
        )

        model.checkForUpdates(manual: false)
        try await waitUntil { model.updateNotice == update }
        XCTAssertEqual(notifier.deliveredVersions(), ["1.11.4"])
    }

    func testUpdateNoticePolicyRejectsSameOlderAndMalformedVersions() {
        XCTAssertTrue(rivuneShouldPresentUpdateNotice(lastVersion: nil, candidateVersion: "1.11.4"))
        XCTAssertFalse(rivuneShouldPresentUpdateNotice(lastVersion: "1.11.4", candidateVersion: "1.11.4"))
        XCTAssertTrue(rivuneShouldPresentUpdateNotice(lastVersion: "1.11.4", candidateVersion: "1.11.5"))
        XCTAssertFalse(rivuneShouldPresentUpdateNotice(lastVersion: "1.11.5", candidateVersion: "1.11.4"))
        XCTAssertFalse(rivuneShouldPresentUpdateNotice(lastVersion: "invalid", candidateVersion: "1.11.5"))
    }

    func testAutomaticCheckUsesDailyThrottleButManualCheckBypassesIt() async throws {
        let defaults = isolatedDefaults()
        defaults.set(Date(), forKey: "rivune.update.last-successful-check")
        defaults.set("1.11.3", forKey: "rivune.update.last-successful-version")
        try seedUpToDateCache("1.11.3", defaults: defaults)
        let checker = StubAppleUpdateChecker(result: .success(.upToDate(currentVersion: "1.11.3", latestVersion: "1.11.3")))
        let model = RivuneAppModel(
            defaults: defaults,
            updateChecker: checker,
            applicationVersion: "1.11.3"
        )

        model.start()
        for _ in 0..<20 { await Task.yield() }
        let throttledCallCount = await checker.callCount()
        XCTAssertEqual(throttledCallCount, 0)

        model.checkForUpdates()
        try await waitUntil {
            model.updateState == .upToDate(currentVersion: "1.11.3", latestVersion: "1.11.3")
        }
        let bypassedCallCount = await checker.callCount()
        XCTAssertEqual(bypassedCallCount, 1)
    }

    func testAutomaticCheckRunsImmediatelyAfterInstalledVersionChanges() async throws {
        let defaults = isolatedDefaults()
        defaults.set(Date(), forKey: "rivune.update.last-successful-check")
        defaults.set("1.11.3", forKey: "rivune.update.last-successful-version")
        try seedUpToDateCache("1.11.3", defaults: defaults)
        let checker = StubAppleUpdateChecker(result: .success(.upToDate(currentVersion: "1.11.4", latestVersion: "1.11.4")))
        let model = RivuneAppModel(
            defaults: defaults,
            updateChecker: checker,
            applicationVersion: "1.11.4"
        )

        model.start()
        try await waitUntil {
            model.updateState == .upToDate(currentVersion: "1.11.4", latestVersion: "1.11.4")
        }
        let upgradedCallCount = await checker.callCount()
        XCTAssertEqual(upgradedCallCount, 1)
    }

    func testFailedCheckDoesNotAdvanceThrottleAndCanBeRetried() async throws {
        let defaults = isolatedDefaults()
        let checker = StubAppleUpdateChecker(result: .failure(RivuneAppleUpdateError.unexpectedResponse))
        let model = RivuneAppModel(
            defaults: defaults,
            updateChecker: checker,
            applicationVersion: "1.11.3"
        )

        model.checkForUpdates()
        try await waitUntil { model.updateState == .failed }
        XCTAssertNil(defaults.object(forKey: "rivune.update.last-successful-check"))

        await checker.setResult(.success(.upToDate(currentVersion: "1.11.3", latestVersion: "1.11.3")))
        model.checkForUpdates()
        try await waitUntil {
            model.updateState == .upToDate(currentVersion: "1.11.3", latestVersion: "1.11.3")
        }
        let retryCallCount = await checker.callCount()
        XCTAssertEqual(retryCallCount, 2)
    }

    func testValidatedCachedUpdateIsRestoredOnlyForInstalledVersion() throws {
        let defaults = isolatedDefaults()
        let update = fixtureUpdate()
        let cache = RivuneAppleUpdateCache(
            currentVersion: update.currentVersion,
            latestVersion: update.latestVersion,
            publishedAt: ISO8601DateFormatter().string(from: update.publishedAt),
            releaseURL: update.releaseURL.absoluteString,
            packageURL: update.packageURL.absoluteString,
            packageFileName: update.packageFileName,
            packageSize: update.packageSize,
            packageSHA256: update.packageSHA256
        )
        defaults.set(try JSONEncoder().encode(cache), forKey: "rivune.update.cached-result")
        defaults.set(Date(), forKey: "rivune.update.last-successful-check")
        defaults.set("1.11.3", forKey: "rivune.update.last-successful-version")

        let restored = RivuneAppModel(
            defaults: defaults,
            updateChecker: StubAppleUpdateChecker(result: .failure(RivuneAppleUpdateError.unexpectedResponse)),
            applicationVersion: "1.11.3"
        )
        XCTAssertEqual(restored.updateState, .available(update))

        _ = RivuneAppModel(
            defaults: defaults,
            updateChecker: StubAppleUpdateChecker(result: .failure(RivuneAppleUpdateError.unexpectedResponse)),
            applicationVersion: "1.11.4"
        )
        XCTAssertNil(defaults.data(forKey: "rivune.update.cached-result"))
        XCTAssertNil(defaults.object(forKey: "rivune.update.last-successful-check"))
        XCTAssertNil(defaults.string(forKey: "rivune.update.last-successful-version"))
    }

    private func fixtureUpdate(version: String = "1.11.4") -> RivuneAppleUpdate {
        RivuneAppleUpdate(
            currentVersion: "1.11.3",
            latestVersion: version,
            publishedAt: Date(timeIntervalSince1970: 1_787_420_437),
            releaseURL: URL(string: "https://github.com/moodiness/rivune/releases/tag/v\(version)")!,
            packageURL: URL(string: "https://github.com/moodiness/rivune/releases/download/v\(version)/Rivune-macOS.dmg")!,
            packageFileName: "Rivune-macOS.dmg",
            packageSize: 1_048_576,
            packageSHA256: String(repeating: "a", count: 64)
        )
    }

    private func seedUpToDateCache(_ version: String, defaults: UserDefaults) throws {
        let cache = RivuneAppleUpdateCache(
            currentVersion: version,
            latestVersion: version,
            publishedAt: "",
            releaseURL: "",
            packageURL: "",
            packageFileName: "",
            packageSize: 0,
            packageSHA256: ""
        )
        defaults.set(try JSONEncoder().encode(cache), forKey: "rivune.update.cached-result")
    }

    private func isolatedDefaults() -> UserDefaults {
        let suite = "AppleUpdateModelTests.\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: suite)!
        defaults.removePersistentDomain(forName: suite)
        return defaults
    }

    private func waitUntil(_ condition: @escaping @MainActor () -> Bool) async throws {
        for _ in 0..<200 {
            if condition() { return }
            try await Task.sleep(nanoseconds: 1_000_000)
        }
        XCTFail("Timed out waiting for update state")
    }
}

private actor StubAppleUpdateChecker: RivuneAppleUpdateChecking {
    private var result: Result<RivuneAppleUpdateCheckResult, RivuneAppleUpdateError>
    private var calls = 0

    init(result: Result<RivuneAppleUpdateCheckResult, RivuneAppleUpdateError>) {
        self.result = result
    }

    func check(currentVersion: String) async throws -> RivuneAppleUpdateCheckResult {
        calls += 1
        return try result.get()
    }

    func setResult(_ result: Result<RivuneAppleUpdateCheckResult, RivuneAppleUpdateError>) {
        self.result = result
    }

    func callCount() -> Int { calls }
}

@MainActor
private final class StubAppleUpdateNotifier: RivuneAppleUpdateNotifying {
    private let delivered: Bool
    private var versions: [String] = []

    init(delivered: Bool) { self.delivered = delivered }

    func deliver(_ update: RivuneAppleUpdate) async -> Bool {
        versions.append(update.latestVersion)
        return delivered
    }

    func requestPermission() async {}

    func deliveredVersions() -> [String] { versions }
}
