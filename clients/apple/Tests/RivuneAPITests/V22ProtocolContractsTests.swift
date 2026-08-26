import Foundation
import XCTest
@testable import RivuneAPI

#if canImport(FoundationNetworking)
import FoundationNetworking
#endif

final class V22ProtocolContractsTests: XCTestCase {
    func testProfileArchiveExportMergeAndCreateRoutes() async throws {
        let transport = V22ContractTransport()
        let client = try RivuneAPIClient(
            serverURL: URL(string: "https://example.test")!,
            transport: transport,
            credentialStore: V22CredentialStore())
        _ = try await client.discover()
        let profileId = UUID(uuidString: "11111111-1111-4111-8111-111111111111")!
        let categoryId = UUID(uuidString: "22222222-2222-4222-8222-222222222222")!

        let archive = try await client.exportProfileArchive(profileId: profileId)
        let merge = try await client.mergeProfileArchive(profileId: profileId, archive: archive)
        let create = try await client.createProfileFromArchive(categoryId: categoryId, archive: archive)

        XCTAssertEqual(merge.mode, .merge)
        XCTAssertEqual(create.mode, .create)
        let requests = transport.requests()
        XCTAssertEqual(requests.map { $0.url?.path }, [
            "/api/v1/profiles/\(profileId.uuidString.lowercased())/archive",
            "/api/v1/profiles/\(profileId.uuidString.lowercased())/archive/import",
            "/api/v1/profiles/archive",
        ])
        XCTAssertEqual(requests.map(\.httpMethod), ["GET", "POST", "POST"])
        let createBody = try XCTUnwrap(
            JSONSerialization.jsonObject(with: try XCTUnwrap(requests[2].httpBody)) as? [String: Any])
        XCTAssertEqual((createBody["categoryId"] as? String)?.lowercased(), categoryId.uuidString.lowercased())
        XCTAssertEqual((createBody["archive"] as? [String: Any])?["version"] as? Int, 2)
        XCTAssertFalse(String(data: try XCTUnwrap(requests[2].httpBody), encoding: .utf8)?.contains("accessToken") == true)
    }

    func testCommandModelsDecodeFlatClosedResultAndRejectUnknownStatus() throws {
        let data = Data(#"{"operationId":"99999999-9999-4999-8999-999999999999","command":"load","mode":"handoff","targetRevision":7,"senderDeviceName":"Living Room","status":"failed","resultCode":"stale_target","createdAt":"2026-08-26T00:00:00Z","expiresAt":"2026-08-26T00:02:00Z"}"#.utf8)
        let command = try JSONDecoder().decode(PlaybackCommand.self, from: data)
        XCTAssertEqual(command.status, .failed)
        XCTAssertEqual(command.resultCode, .staleTarget)
        XCTAssertEqual(command.targetRevision, 7)

        let unknown = Data(#"{"operationId":"99999999-9999-4999-8999-999999999999","command":"play","senderDeviceName":"Living Room","status":"accepted","createdAt":"2026-08-26T00:00:00Z","expiresAt":"2026-08-26T00:02:00Z"}"#.utf8)
        XCTAssertThrowsError(try JSONDecoder().decode(PlaybackCommand.self, from: unknown))
    }

    func testCommandKindAcceptsFiveValuesAndRejectsUnknown() throws {
        for kind in PlaybackCommandKind.allCases {
            let encoded = try JSONEncoder().encode(kind)
            XCTAssertEqual(try JSONDecoder().decode(PlaybackCommandKind.self, from: encoded), kind)
        }
        XCTAssertEqual(PlaybackCommandKind.allCases, [.load, .play, .pause, .seek, .stop])
        XCTAssertThrowsError(
            try JSONDecoder().decode(PlaybackCommandKind.self, from: Data("\"skip\"".utf8)))
    }
}

private struct V22CredentialStore: CredentialStore {
    func load(for issuer: URL) async throws -> StoredCredentials? {
        StoredCredentials(tokens: TokenPair(
            tokenType: "Bearer",
            accessToken: "access",
            accessTokenExpiresAt: "2099-01-01T00:00:00Z",
            refreshToken: "refresh",
            refreshTokenExpiresAt: "2099-02-01T00:00:00Z",
            sessionId: UUID(uuidString: "33333333-3333-4333-8333-333333333333")!,
            deviceId: UUID(uuidString: "44444444-4444-4444-8444-444444444444")!,
            authorizationScope: .globalAdministrator,
            category: nil), profileContext: nil)
    }
    func save(_ credentials: StoredCredentials, for issuer: URL) async throws {}
    func clear(for issuer: URL) async throws {}
}

private final class V22ContractTransport: HTTPTransport, @unchecked Sendable {
    private let lock = NSLock()
    private var recorded: [URLRequest] = []

    func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        let path = request.url!.path
        let body: Data
        let status: Int
        if path == "/.well-known/rivune" {
            status = 200
            body = Data(#"{"name":"Rivune","serverVersion":"22","protocolVersion":22,"apiBaseUrl":"/api/v1","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en","capabilities":["profile-archives-v2"]}"#.utf8)
        } else {
            lock.withLock { recorded.append(request) }
            if path.hasSuffix("/archive") && request.httpMethod == "GET" {
                status = 200
                body = archive
            } else if path.hasSuffix("/archive/import") {
                status = 200
                body = report(mode: "merge", profile: "11111111-1111-4111-8111-111111111111")
            } else {
                status = 201
                body = report(mode: "create", profile: "55555555-5555-4555-8555-555555555555")
            }
        }
        return (body, HTTPURLResponse(
            url: request.url!, statusCode: status, httpVersion: nil,
            headerFields: ["Content-Type": "application/json"])!)
    }

    func requests() -> [URLRequest] { lock.withLock { recorded } }

    private var archive: Data {
        Data(#"{"version":2,"identity":{"name":"Viewer","isChild":false,"avatar":{"kind":"preset","presetId":"ember"}},"addons":[],"continueDismissals":[]}"#.utf8)
    }

    private func report(mode: String, profile: String) -> Data {
        Data("{\"mode\":\"\(mode)\",\"profileId\":\"\(profile)\",\"sections\":[{\"section\":\"settings\",\"created\":0,\"updated\":1,\"unchanged\":0}],\"trackingAccountsUpdated\":0}".utf8)
    }
}
