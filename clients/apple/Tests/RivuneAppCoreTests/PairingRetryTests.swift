import Foundation
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif
import XCTest
@testable import RivuneAPI
@testable import RivuneAppCore

@MainActor
final class PairingRetryTests: XCTestCase {
    func testCapacityResponseRetriesAutomaticallyAfterServerDelay() async throws {
        let suite = "PairingRetryTests.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }

        let transport = PairingRetryTransport()
        let serverURL = URL(string: "https://pairing-retry.test")!
        let client = try RivuneAPIClient(
            serverURL: serverURL,
            transport: transport,
            credentialStore: PairingRetryCredentialStore()
        )
        let model = RivuneAppModel(
            defaults: defaults,
            updateChecker: PairingRetryUpdateChecker(),
            applicationVersion: "test",
            client: client,
            serverOrigin: serverURL
        )

        model.restartPairing()
        try await waitUntil {
            model.failure == .pairingCapacity && model.pairingRetrySeconds == 1
        }
        try await waitUntil(timeoutIterations: 1_500) {
            model.pairingCode == "ABCD-EFGH"
        }

        XCTAssertNil(model.failure)
        XCTAssertNil(model.pairingRetrySeconds)
        let deviceCodeRequestCount = await transport.deviceCodeRequestCount()
        XCTAssertEqual(deviceCodeRequestCount, 2)
        let installationIDs = await transport.installationIDs()
        XCTAssertEqual(installationIDs.count, 2)
        XCTAssertEqual(Set(installationIDs).count, 1)
        XCTAssertEqual(defaults.string(forKey: "rivune.installation.id"), installationIDs.first)
        model.disconnect()
    }

    func testCapacityCountdownUsesCompleteServerDelay() async throws {
        let suite = "PairingRetryTests.complete-delay.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }

        let transport = PairingRetryTransport(retryAfterSeconds: 75)
        let serverURL = URL(string: "https://pairing-retry.test")!
        let client = try RivuneAPIClient(
            serverURL: serverURL,
            transport: transport,
            credentialStore: PairingRetryCredentialStore()
        )
        let model = RivuneAppModel(
            defaults: defaults,
            updateChecker: PairingRetryUpdateChecker(),
            applicationVersion: "test",
            client: client,
            serverOrigin: serverURL
        )

        model.restartPairing()
        try await waitUntil { model.pairingRetrySeconds == 75 }
        XCTAssertEqual(model.failure, .pairingCapacity)
        model.disconnect()
    }

    func testInstallationIdentitySurvivesModelRecreation() async throws {
        let suite = "PairingRetryTests.installation.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let serverURL = URL(string: "https://pairing-retry.test")!

        let firstTransport = PairingRetryTransport(capacityResponses: 0)
        let firstClient = try RivuneAPIClient(
            serverURL: serverURL,
            transport: firstTransport,
            credentialStore: PairingRetryCredentialStore()
        )
        let firstModel = RivuneAppModel(
            defaults: defaults,
            updateChecker: PairingRetryUpdateChecker(),
            applicationVersion: "test",
            client: firstClient,
            serverOrigin: serverURL
        )
        firstModel.restartPairing()
        try await waitUntil { firstModel.pairingCode == "ABCD-EFGH" }
        let firstInstallationIDs = await firstTransport.installationIDs()
        let firstID = try XCTUnwrap(firstInstallationIDs.first)
        firstModel.disconnect()

        let secondTransport = PairingRetryTransport(capacityResponses: 0)
        let secondClient = try RivuneAPIClient(
            serverURL: serverURL,
            transport: secondTransport,
            credentialStore: PairingRetryCredentialStore()
        )
        let secondModel = RivuneAppModel(
            defaults: defaults,
            updateChecker: PairingRetryUpdateChecker(),
            applicationVersion: "test",
            client: secondClient,
            serverOrigin: serverURL
        )
        secondModel.restartPairing()
        try await waitUntil { secondModel.pairingCode == "ABCD-EFGH" }
        let secondInstallationIDs = await secondTransport.installationIDs()
        let secondID = try XCTUnwrap(secondInstallationIDs.first)

        XCTAssertEqual(secondID, firstID)
        secondModel.disconnect()
    }

    private func waitUntil(
        timeoutIterations: Int = 200,
        _ condition: @escaping @MainActor () -> Bool
    ) async throws {
        for _ in 0..<timeoutIterations {
            if condition() { return }
            try await Task.sleep(nanoseconds: 1_000_000)
        }
        XCTFail("Timed out waiting for pairing state")
    }
}

private actor PairingRetryTransport: HTTPTransport {
    private let capacityResponses: Int
    private let retryAfterSeconds: Int
    private var deviceCodeRequests = 0
    private var requestedInstallationIDs: [String] = []

    init(capacityResponses: Int = 1, retryAfterSeconds: Int = 1) {
        self.capacityResponses = capacityResponses
        self.retryAfterSeconds = retryAfterSeconds
    }
    func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        switch request.url?.path {
        case "/.well-known/rivune":
            return response(
                request,
                status: 200,
                body: #"{"name":"Rivune","serverVersion":"test","protocolVersion":22,"apiBaseUrl":"/api/v1","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en"}"#
            )
        case "/api/v1/auth/device-code":
            if let body = request.httpBody,
               let object = try? JSONSerialization.jsonObject(with: body) as? [String: Any],
               let installationID = object["installationId"] as? String {
                requestedInstallationIDs.append(installationID)
            }
            deviceCodeRequests += 1
            if deviceCodeRequests <= capacityResponses {
                return response(
                    request,
                    status: 429,
                    headers: ["Retry-After": String(retryAfterSeconds)],
                    body: #"{"error":{"code":"device_code_capacity","message":"busy"}}"#
                )
            }
            return response(
                request,
                status: 201,
                body: #"{"deviceCode":"device-secret","userCode":"ABCD-EFGH","verificationUri":"https://pairing-retry.test/pair","verificationUriComplete":"https://pairing-retry.test/pair?code=ABCD-EFGH","expiresAt":"2030-01-01T00:00:00Z","intervalSeconds":60}"#
            )
        default:
            return response(
                request,
                status: 400,
                body: #"{"error":{"code":"unexpected_request","message":"unexpected request"}}"#
            )
        }
    }

    func deviceCodeRequestCount() -> Int { deviceCodeRequests }
    func installationIDs() -> [String] { requestedInstallationIDs }

    private func response(
        _ request: URLRequest,
        status: Int,
        headers: [String: String] = [:],
        body: String
    ) -> (Data, HTTPURLResponse) {
        var fields = headers
        fields["Content-Type"] = "application/json"
        return (
            Data(body.utf8),
            HTTPURLResponse(
                url: request.url!,
                statusCode: status,
                httpVersion: nil,
                headerFields: fields
            )!
        )
    }
}

private actor PairingRetryCredentialStore: CredentialStore {
    func load(for issuer: URL) async throws -> StoredCredentials? { nil }
    func save(_ credentials: StoredCredentials, for issuer: URL) async throws {}
    func clear(for issuer: URL) async throws {}
}

private struct PairingRetryUpdateChecker: RivuneAppleUpdateChecking {
    func check(currentVersion: String) async throws -> RivuneAppleUpdateCheckResult {
        .upToDate(currentVersion: currentVersion, latestVersion: currentVersion)
    }
}
