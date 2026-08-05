import Foundation
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif
import XCTest
@testable import RivuneAPI

final class CategoryContractsTests: XCTestCase {
    private let categoryId = UUID(uuidString: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")!

    func testRequiredCategoryAndScopeFieldsDecode() throws {
        let account = try JSONDecoder().decode(Account.self, from: Data("""
        {
          "user":{"id":"11111111-1111-4111-8111-111111111111","username":"admin","role":"admin"},
          "session":{"id":"22222222-2222-4222-8222-222222222222","deviceId":"33333333-3333-4333-8333-333333333333","activeProfile":null,"authorizationScope":"category","category":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Studio","color":null,"icon":"briefcase"}},
          "profiles":[{"id":"44444444-4444-4444-8444-444444444444","name":"Editor","description":"Editing profile","categoryId":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","category":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Studio","color":null,"icon":"briefcase"},"isChild":false,"hasPin":false,"canManage":true,"enabled":true,"availableFrom":null,"availableUntil":null,"accessStartTime":null,"accessEndTime":null,"accessTimezone":"UTC","accessible":true,"avatar":{"kind":"preset","presetId":"aurora","url":"/avatar.svg"}}],
          "maintenance":{"enabled":false}
        }
        """.utf8))

        XCTAssertEqual(account.session.authorizationScope, .category)
        XCTAssertEqual(account.session.category?.id, categoryId)
        XCTAssertEqual(account.profiles.first?.category.name, "Studio")
        XCTAssertEqual(account.profiles.first?.description, "Editing profile")

        let token = try JSONDecoder().decode(TokenPair.self, from: Data("""
        {"tokenType":"Bearer","accessToken":"access","accessTokenExpiresAt":"2026-08-03T12:15:00Z","refreshToken":"refresh","refreshTokenExpiresAt":"2026-09-03T12:00:00Z","sessionId":"22222222-2222-4222-8222-222222222222","deviceId":"33333333-3333-4333-8333-333333333333","authorizationScope":"global_admin","category":null}
        """.utf8))
        XCTAssertEqual(token.authorizationScope, .globalAdministrator)
        XCTAssertNil(token.category)

        let session = try JSONDecoder().decode(Session.self, from: Data("""
        {"id":"22222222-2222-4222-8222-222222222222","deviceId":"33333333-3333-4333-8333-333333333333","deviceName":"Editing Mac","platform":"macos","ipAddress":null,"createdAt":"2026-08-03T10:00:00Z","lastSeenAt":"2026-08-03T11:00:00Z","current":true,"authorizationScope":"category","category":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Studio","color":null,"icon":"briefcase"}}
        """.utf8))
        XCTAssertEqual(session.authorizationScope, .category)
        XCTAssertEqual(session.category?.id, categoryId)
    }

    func testRequiredNullableResponseFieldsCannotBeOmitted() {
        XCTAssertThrowsError(try JSONDecoder().decode(TokenPair.self, from: Data("""
        {"tokenType":"Bearer","accessToken":"access","accessTokenExpiresAt":"2026-08-03T12:15:00Z","refreshToken":"refresh","refreshTokenExpiresAt":"2026-09-03T12:00:00Z","sessionId":"22222222-2222-4222-8222-222222222222","deviceId":"33333333-3333-4333-8333-333333333333","authorizationScope":"global_admin"}
        """.utf8)))
        XCTAssertThrowsError(try JSONDecoder().decode(Device.self, from: Data("""
        {"id":"33333333-3333-4333-8333-333333333333","name":"Editing Mac","platform":"macos","categoryId":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","category":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Studio","color":null,"icon":"briefcase"},"approvedAt":null,"lastSeenAt":null,"createdAt":"2026-08-03T10:00:00Z","updatedAt":"2026-08-03T11:00:00Z"}
        """.utf8)))
        XCTAssertThrowsError(try JSONDecoder().decode(TokenPair.self, from: Data("""
        {"tokenType":"Bearer","accessToken":"access","accessTokenExpiresAt":"2026-08-03T12:15:00Z","refreshToken":"refresh","refreshTokenExpiresAt":"2026-09-03T12:00:00Z","sessionId":"22222222-2222-4222-8222-222222222222","deviceId":"33333333-3333-4333-8333-333333333333","authorizationScope":"category","category":null}
        """.utf8)))
    }

    func testCategoryAndDeviceContractsDecode() throws {
        let category = try JSONDecoder().decode(Category.self, from: Data("""
        {"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Studio","description":"Production","color":"#123ABC","icon":"briefcase","position":2,"isDefault":false,"profileCount":3,"deviceCount":4,"createdAt":"2026-08-03T10:00:00Z","updatedAt":"2026-08-03T11:00:00Z"}
        """.utf8))
        XCTAssertEqual(category.deviceCount, 4)

        let device = try JSONDecoder().decode(Device.self, from: Data("""
        {"id":"33333333-3333-4333-8333-333333333333","name":"Editing Mac","platform":"macos","categoryId":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","category":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Studio","color":"#123ABC","icon":"briefcase"},"internalNote":null,"approvedAt":"2026-08-03T10:00:00Z","lastSeenAt":null,"createdAt":"2026-08-03T10:00:00Z","updatedAt":"2026-08-03T11:00:00Z"}
        """.utf8))
        XCTAssertEqual(device.categoryId, categoryId)
        XCTAssertNil(device.internalNote)
    }

    func testPatchEncodingDistinguishesOmittedAndNull() throws {
        let patch = CategoryUpdateRequest(name: "Post", description: .null, color: .value("#ABCDEF"))
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: JSONEncoder().encode(patch)) as? [String: Any])
        XCTAssertEqual(object["name"] as? String, "Post")
        XCTAssertTrue(object["description"] is NSNull)
        XCTAssertEqual(object["color"] as? String, "#ABCDEF")
        XCTAssertNil(object["icon"])

        let approval = DeviceCodeApprovalRequest(
            userCode: "ABCD-EFGH",
            categoryId: categoryId,
            deviceName: "Living Room",
            internalNote: "Wall display"
        )
        let approvalObject = try XCTUnwrap(JSONSerialization.jsonObject(with: JSONEncoder().encode(approval)) as? [String: Any])
        XCTAssertEqual(approvalObject["userCode"] as? String, "ABCD-EFGH")
        XCTAssertEqual((approvalObject["categoryId"] as? String)?.lowercased(), categoryId.uuidString.lowercased())
        XCTAssertEqual(approvalObject["deviceName"] as? String, "Living Room")
    }

    func testUpdateCategoryClientUsesPatchRouteAndExactBody() async throws {
        let transport = RecordingCategoryTransport()
        let serverURL = URL(string: "https://rivune.test")!
        let client = try RivuneAPIClient(
            serverURL: serverURL,
            transport: transport,
            credentialStore: StubCredentialStore(issuer: serverURL, token: fixtureToken())
        )
        let category = try await client.updateCategory(
            id: categoryId,
            input: CategoryUpdateRequest(description: .null, icon: .value("briefcase"), isDefault: true)
        )
        XCTAssertEqual(category.id, categoryId)

        let recordedRequest = transport.lastAPIRequest()
        let request = try XCTUnwrap(recordedRequest)
        XCTAssertEqual(request.httpMethod, "PATCH")
        XCTAssertEqual(request.url?.path, "/api/v1/categories/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
        let body = try XCTUnwrap(request.httpBody)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: body) as? [String: Any])
        XCTAssertTrue(object["description"] is NSNull)
        XCTAssertEqual(object["icon"] as? String, "briefcase")
        XCTAssertEqual(object["isDefault"] as? Bool, true)
        XCTAssertNil(object["name"])
    }

    private func fixtureToken() -> TokenPair {
        TokenPair(
            tokenType: "Bearer",
            accessToken: "access",
            accessTokenExpiresAt: "2026-08-03T12:15:00Z",
            refreshToken: "refresh",
            refreshTokenExpiresAt: "2026-09-03T12:00:00Z",
            sessionId: UUID(uuidString: "22222222-2222-4222-8222-222222222222")!,
            deviceId: UUID(uuidString: "33333333-3333-4333-8333-333333333333")!,
            authorizationScope: .globalAdministrator,
            category: nil
        )
    }
}

private struct StubCredentialStore: CredentialStore {
    let issuer: URL
    let token: TokenPair

    func load(for requestedIssuer: URL) async throws -> TokenPair? {
        requestedIssuer == issuer ? token : nil
    }

    func save(_ credentials: TokenPair, for issuer: URL) async throws {}
    func clear(for issuer: URL) async throws {}
}

private final class RecordingCategoryTransport: HTTPTransport, @unchecked Sendable {
    private let lock = NSLock()
    private var requests: [URLRequest] = []

    func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        lock.withLock {
            requests.append(request)
        }
        let body: Data
        if request.url?.path == "/.well-known/rivune" {
            body = Data("""
            {"name":"Rivune","serverVersion":"test","protocolVersion":19,"apiBaseUrl":"/api/v1","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en"}
            """.utf8)
        } else {
            body = Data("""
            {"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Studio","description":null,"color":null,"icon":"briefcase","position":0,"isDefault":false,"profileCount":0,"deviceCount":0,"createdAt":"2026-08-03T10:00:00Z","updatedAt":"2026-08-03T11:00:00Z"}
            """.utf8)
        }
        let response = HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
        return (body, response)
    }

    func lastAPIRequest() -> URLRequest? {
        lock.withLock {
            requests.last(where: { $0.url?.path != "/.well-known/rivune" })
        }
    }
}
