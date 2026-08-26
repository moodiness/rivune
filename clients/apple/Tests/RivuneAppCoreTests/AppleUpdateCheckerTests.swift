import Foundation
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif
import XCTest
@testable import RivuneAppCore
@testable import RivuneAPI

@MainActor
final class AppleUpdateCheckerTests: XCTestCase {
    func testManifestSelectsAndValidatesEveryApplePackage() throws {
        for platform in RivuneAppleUpdatePlatform.allCases {
            let result = try RivuneAppleUpdateManifestParser.parse(
                manifestData(version: "1.11.4"),
                currentVersion: "1.11.3",
                platform: platform
            )
            guard case .available(let update) = result else {
                return XCTFail("Expected an update for \(platform)")
            }
            XCTAssertEqual(update.currentVersion, "1.11.3")
            XCTAssertEqual(update.latestVersion, "1.11.4")
            XCTAssertEqual(update.releaseURL.absoluteString, "https://github.com/moodiness/rivune/releases/tag/v1.11.4")
            XCTAssertEqual(update.packageFileName, platform.fileName)
            XCTAssertEqual(
                update.packageURL.absoluteString,
                "https://github.com/moodiness/rivune/releases/download/v1.11.4/\(platform.fileName)"
            )
            XCTAssertEqual(update.packageSHA256, String(repeating: "a", count: 64))
        }
    }

    func testManifestReportsCurrentAndNewerInstalledVersionsAsUpToDate() throws {
        for current in ["1.11.4", "1.12.0", "2.0.0"] {
            XCTAssertEqual(
                try RivuneAppleUpdateManifestParser.parse(
                    manifestData(version: "1.11.4"),
                    currentVersion: current,
                    platform: .ios
                ),
                .upToDate(currentVersion: current, latestVersion: "1.11.4")
            )
        }
    }

    func testManifestRejectsUntrustedOrMismatchedAppleMetadata() throws {
        var manifest = manifestObject(version: "1.11.4")
        var packages = try XCTUnwrap(manifest["packages"] as? [String: Any])
        var ios = try XCTUnwrap(packages["ios"] as? [String: Any])

        ios["url"] = "https://evil.example/Rivune-iOS-unsigned.ipa"
        packages["ios"] = ios
        manifest["packages"] = packages
        XCTAssertThrowsError(try parse(manifest, platform: .ios)) { error in
            XCTAssertEqual(error as? RivuneAppleUpdateError, .invalidManifest)
        }

        ios = try XCTUnwrap((manifestObject(version: "1.11.4")["packages"] as? [String: Any])?["ios"] as? [String: Any])
        ios["bundleIdentifier"] = "io.example.copy"
        packages = try XCTUnwrap(manifestObject(version: "1.11.4")["packages"] as? [String: Any])
        packages["ios"] = ios
        manifest = manifestObject(version: "1.11.4")
        manifest["packages"] = packages
        XCTAssertThrowsError(try parse(manifest, platform: .ios)) { error in
            XCTAssertEqual(error as? RivuneAppleUpdateError, .invalidManifest)
        }

        manifest = manifestObject(version: "1.11.4")
        manifest["tagName"] = "v1.11.5"
        XCTAssertThrowsError(try parse(manifest, platform: .ios)) { error in
            XCTAssertEqual(error as? RivuneAppleUpdateError, .invalidManifest)
        }

        manifest = manifestObject(version: "1.11.4")
        manifest["publishedAt"] = "2026-8-22T17:40:37Zjunk"
        XCTAssertThrowsError(try parse(manifest, platform: .ios)) { error in
            XCTAssertEqual(error as? RivuneAppleUpdateError, .invalidManifest)
        }
    }

    func testManifestAndSemanticVersionInputsAreBoundedAndStrict() throws {
        let oversized = Data(repeating: 0x20, count: rivuneMaximumAppleUpdateManifestBytes + 1)
        XCTAssertThrowsError(
            try RivuneAppleUpdateManifestParser.parse(oversized, currentVersion: "1.11.3", platform: .macos)
        ) { error in
            XCTAssertEqual(error as? RivuneAppleUpdateError, .responseTooLarge)
        }

        for invalid in ["1.2", "01.2.3", "1.02.3", "1.2.03", "1.2.3-", "1.2.3-01", "1.2.3+", "v1.2.3", "1.2.3\n", "1.2.3-é"] {
            XCTAssertNil(RivuneSemanticVersion(invalid), invalid)
        }
        XCTAssertLessThan(try XCTUnwrap(RivuneSemanticVersion("1.11.4-rc.2")), try XCTUnwrap(RivuneSemanticVersion("1.11.4")))
        XCTAssertLessThan(try XCTUnwrap(RivuneSemanticVersion("1.11.4-rc.2")), try XCTUnwrap(RivuneSemanticVersion("1.11.4-rc.10")))
        XCTAssertLessThan(try XCTUnwrap(RivuneSemanticVersion("999999999999999999999.0.0")), try XCTUnwrap(RivuneSemanticVersion("1000000000000000000000.0.0")))
    }

    func testCheckerFollowsOnlyBoundedTrustedGitHubRedirects() async throws {
        let versionedManifest = URL(string: "https://github.com/moodiness/rivune/releases/download/v1.11.4/rivune-update.json")!
        let releaseAsset = URL(string: "https://release-assets.githubusercontent.com/download/rivune-update.json?signature=fixture")!
        let transport = ScriptedAppleUpdateTransport([
            .init(status: 302, headers: ["Location": versionedManifest.absoluteString]),
            .init(status: 302, headers: ["Location": releaseAsset.absoluteString]),
            .init(status: 200, data: manifestData(version: "1.11.4")),
            .init(status: 200, data: Data("fixture-sidecar".utf8)),
        ])
        let checker = RivuneAppleUpdateChecker(transport: transport, platform: .tvos, verifySignature: { _, _ in })

        guard case .available(let update) = try await checker.check(currentVersion: "1.11.3") else {
            return XCTFail("Expected a tvOS update")
        }
        XCTAssertEqual(update.packageFileName, "Rivune-tvOS-unsigned.ipa")
        let requestedURLs = await transport.requestedURLs()
        XCTAssertEqual(
            requestedURLs,
            [RivuneAppleUpdateChecker.manifestURL, versionedManifest, releaseAsset, URL(string: RivuneAppleUpdateChecker.manifestURL.absoluteString + ".sig")!]
        )

        let rejected = ScriptedAppleUpdateTransport([
            .init(status: 302, headers: ["Location": "https://evil.example/rivune-update.json"]),
        ])
        do {
            _ = try await RivuneAppleUpdateChecker(transport: rejected, platform: .ios).check(currentVersion: "1.11.3")
            XCTFail("An off-GitHub redirect must be rejected")
        } catch {
            XCTAssertEqual(error as? RivuneAppleUpdateError, .untrustedRedirect)
        }
        let rejectedRequestCount = await rejected.requestedURLs().count
        XCTAssertEqual(rejectedRequestCount, 1)
    }

    func testCheckerRejectsSixthManifestRedirect() async {
        let redirect = "https://github.com/moodiness/rivune/releases/download/v1.11.4/rivune-update.json"
        let transport = ScriptedAppleUpdateTransport(Array(
            repeating: .init(status: 302, headers: ["Location": redirect]),
            count: 6
        ))

        do {
            _ = try await RivuneAppleUpdateChecker(transport: transport, platform: .macos)
                .check(currentVersion: "1.11.3")
            XCTFail("A sixth redirect must be rejected")
        } catch {
            XCTAssertEqual(error as? RivuneAppleUpdateError, .tooManyRedirects)
        }
        let requestCount = await transport.requestedURLs().count
        XCTAssertEqual(requestCount, 6)
    }

    func testSignatureVerifierRejectsAlteredBytesKeyAndMalformedSignature() throws {
        let manifest = Data("fixture".utf8)
        let valid = Data("{\"schemaVersion\":1,\"algorithm\":\"ecdsa-p256-sha256\",\"keyId\":\"4e9b15a0b6aed77908f3686fbf05a0a9c322ad846662eb758f56d4e65c22796f\",\"manifestSha256\":\"f16d05ec6b29248d2c61adb1e9263f78e4f7bace1b955014a2d17872cfe4064d\",\"signature\":\"MEUCID/exybli2HXWsp9h4iFZIXCTAlvZZcaizBj+dIOfOfRAiEAuxdEPEnwG3MWFlChfZ8NfUvHp+QRoLKu4NXhyFQYNBM=\"}".utf8)
        XCTAssertNoThrow(try RivuneAppleUpdateSignatureVerifier.verify(manifest: manifest, sidecar: valid))
        XCTAssertThrowsError(try RivuneAppleUpdateSignatureVerifier.verify(manifest: manifest + Data([0x20]), sidecar: valid))
        let invalid = Data("{\"schemaVersion\":1,\"algorithm\":\"ecdsa-p256-sha256\",\"keyId\":\"0000000000000000000000000000000000000000000000000000000000000000\",\"manifestSha256\":\"0000000000000000000000000000000000000000000000000000000000000000\",\"signature\":\"%%%\"}".utf8)
        XCTAssertThrowsError(try RivuneAppleUpdateSignatureVerifier.verify(manifest: manifest, sidecar: invalid))
        XCTAssertThrowsError(try RivuneAppleUpdateSignatureVerifier.verify(
            manifest: manifest,
            sidecar: Data(repeating: 0x20, count: rivuneMaximumAppleUpdateSignatureBytes + 1)
        ))
    }

    func testAutomaticChecksAreLimitedToOncePerDay() {
        let now = Date(timeIntervalSince1970: 2_000_000)
        XCTAssertTrue(RivuneAppleUpdateChecker.automaticCheckIsDue(lastSuccessfulCheck: nil, now: now))
        XCTAssertFalse(RivuneAppleUpdateChecker.automaticCheckIsDue(lastSuccessfulCheck: now.addingTimeInterval(-86_399), now: now))
        XCTAssertTrue(RivuneAppleUpdateChecker.automaticCheckIsDue(lastSuccessfulCheck: now.addingTimeInterval(-86_400), now: now))
        XCTAssertTrue(RivuneAppleUpdateChecker.automaticCheckIsDue(lastSuccessfulCheck: now.addingTimeInterval(1), now: now))
    }

    private func parse(_ object: [String: Any], platform: RivuneAppleUpdatePlatform) throws -> RivuneAppleUpdateCheckResult {
        try RivuneAppleUpdateManifestParser.parse(
            JSONSerialization.data(withJSONObject: object, options: [.sortedKeys]),
            currentVersion: "1.11.3",
            platform: platform
        )
    }

    private func manifestData(version: String) -> Data {
        try! JSONSerialization.data(withJSONObject: manifestObject(version: version), options: [.sortedKeys])
    }

    private func manifestObject(version: String) -> [String: Any] {
        var packages: [String: Any] = [:]
        for platform in RivuneAppleUpdatePlatform.allCases {
            packages[platform.rawValue] = [
                "format": platform.format,
                "architectures": platform.architectures,
                "minimumOsVersion": platform.minimumOSVersion,
                "bundleIdentifier": platform.bundleIdentifier,
                "signature": "unsigned",
                "fileName": platform.fileName,
                "url": "https://github.com/moodiness/rivune/releases/download/v\(version)/\(platform.fileName)",
                "size": 1_048_576,
                "sha256": String(repeating: "a", count: 64),
            ]
        }
        return [
            "schemaVersion": 3,
            "channel": version.contains("-") ? "prerelease" : "stable",
            "version": version,
            "tagName": "v\(version)",
            "publishedAt": "2026-08-22T17:40:37Z",
            "releaseUrl": "https://github.com/moodiness/rivune/releases/tag/v\(version)",
            "packages": packages,
        ]
    }
}

private actor ScriptedAppleUpdateTransport: HTTPTransport {
    struct Reply: Sendable {
        let status: Int
        let headers: [String: String]
        let data: Data

        init(status: Int, headers: [String: String] = [:], data: Data = Data()) {
            self.status = status
            self.headers = headers
            self.data = data
        }
    }

    private var replies: [Reply]
    private var requests: [URL] = []

    init(_ replies: [Reply]) {
        self.replies = replies
    }

    func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        guard let url = request.url, !replies.isEmpty else { throw URLError(.badServerResponse) }
        requests.append(url)
        let reply = replies.removeFirst()
        let response = HTTPURLResponse(
            url: url,
            statusCode: reply.status,
            httpVersion: "HTTP/1.1",
            headerFields: reply.headers
        )!
        return (reply.data, response)
    }

    func requestedURLs() -> [URL] { requests }
}
