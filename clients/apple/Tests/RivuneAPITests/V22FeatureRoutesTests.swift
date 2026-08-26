import Foundation
import XCTest
@testable import RivuneAPI

#if canImport(FoundationNetworking)
import FoundationNetworking
#endif

final class V22FeatureRoutesTests: XCTestCase {
  func testProfileQueueUsesCASAndStableOperationIdentifier() async throws {
    let transport = FeatureTransport()
    let client = try makeClient(transport)
    let profile = UUID(uuidString: "11111111-1111-4111-8111-111111111111")!
    let operation = UUID(uuidString: "22222222-2222-4222-8222-222222222222")!
    _ = try await client.addReadingQueueItem(
      profileId: profile,
      input: ReadingQueueAddInput(
        operationId: operation, expectedRevision: 7, mediaType: .movie,
        resourceId: "movie:42", title: "Example"))

    let request = try XCTUnwrap(transport.lastRequest)
    XCTAssertEqual(request.url?.path, "/api/v1/profiles/\(profile.uuidString.lowercased())/queue/items")
    XCTAssertEqual(request.httpMethod, "POST")
    let body = try JSONSerialization.jsonObject(with: try XCTUnwrap(request.httpBody)) as? [String: Any]
    XCTAssertEqual(body?["operationId"] as? String, operation.uuidString.lowercased())
    XCTAssertEqual(body?["expectedRevision"] as? Int, 7)
    XCTAssertNil(body?["sourceRef"])
  }

  func testSavedSearchSmartCollectionIncidentNotificationAndAccessibilityRoutes() async throws {
    let transport = FeatureTransport()
    let client = try makeClient(transport)
    let profile = UUID(uuidString: "11111111-1111-4111-8111-111111111111")!
    let resource = UUID(uuidString: "33333333-3333-4333-8333-333333333333")!

    _ = try await client.savedSearches()
    _ = try await client.smartCollections()
    _ = try await client.extensionIncident(id: resource)
    _ = try await client.mediaNotifications(cursor: "9", limit: 30)
    _ = try await client.profileAccessibilityPreferences(profileId: profile)

    XCTAssertEqual(transport.requests.map { $0.url?.path }, [
      "/api/v1/saved-searches",
      "/api/v1/smart-collections",
      "/api/v1/operations/extension-incidents/\(resource.uuidString.lowercased())",
      "/api/v1/media-notifications",
      "/api/v1/profiles/\(profile.uuidString.lowercased())/accessibility-preferences",
    ])
    XCTAssertEqual(transport.requests[3].url?.query, "cursor=9&limit=30")
    XCTAssertTrue(transport.requests.allSatisfy {
      $0.value(forHTTPHeaderField: "X-Rivune-Profile-Context") == "profile-context"
    })
  }

  func testFailoverAdvanceCarriesOnlyClosedErrorPositionAndRevision() async throws {
    let transport = FeatureTransport()
    let client = try makeClient(transport)
    let id = UUID(uuidString: "44444444-4444-4444-8444-444444444444")!
    let state = try await client.advancePlaybackFailover(
      id: id,
      input: PlaybackFailoverAdvanceInput(
        error: .sourceTimeout, positionSeconds: 18.5, expectedRevision: 3))

    XCTAssertEqual(state.status, .active)
    XCTAssertEqual(state.currentSourceRef, "opaque-source-ref-2")
    let request = try XCTUnwrap(transport.lastRequest)
    XCTAssertEqual(request.url?.path, "/api/v1/playback/failovers/\(id.uuidString.lowercased())/advance")
    let encoded = String(data: try XCTUnwrap(request.httpBody), encoding: .utf8) ?? ""
    XCTAssertTrue(encoded.contains("source_timeout"))
    XCTAssertFalse(encoded.contains("http"))
    XCTAssertFalse(encoded.contains("token"))
  }

  func testSmartRuleIsClosedAndNeverAcceptsFreeFormExpression() throws {
    let valid = Data(#"{"type":"all","rules":[{"type":"media_type","operator":"one_of","values":["movie"]},{"type":"rating","operator":"gte","number":7.5}]}"#.utf8)
    let decoded = try JSONDecoder().decode(SmartRule.self, from: valid)
    XCTAssertEqual(try JSONDecoder().decode(SmartRule.self, from: JSONEncoder().encode(decoded)), decoded)
    XCTAssertThrowsError(
      try JSONDecoder().decode(
        SmartRule.self,
        from: Data(#"{"type":"sql","value":"select * from titles"}"#.utf8)))
  }

  func testSavedSearchMediaTypeIsClosedAndAnimeCannotBeEncoded() throws {
    let input = SavedSearchInput(name: "Films", query: "space", mediaType: .movie, sort: .relevance)
    let body = try JSONEncoder().encode(input)
    XCTAssertTrue(String(decoding: body, as: UTF8.self).contains("\"mediaType\":\"movie\""))
    XCTAssertNil(SavedSearchMediaType(rawValue: "anime"))
    XCTAssertThrowsError(
      try JSONDecoder().decode(
        SavedSearch.self,
        from: Data(#"{"id":"11111111-1111-4111-8111-111111111111","name":"Anime","query":"anime","mediaType":"anime","sort":"relevance","revision":1,"createdAt":"2026-08-26T00:00:00Z","updatedAt":"2026-08-26T00:00:00Z"}"#.utf8)))
  }

  private func makeClient(_ transport: FeatureTransport) throws -> RivuneAPIClient {
    try RivuneAPIClient(
      serverURL: URL(string: "https://example.test")!, transport: transport,
      credentialStore: FeatureCredentials())
  }
}

private struct FeatureCredentials: CredentialStore {
  func load(for issuer: URL) async throws -> StoredCredentials? {
    StoredCredentials(
      tokens: TokenPair(
        tokenType: "Bearer", accessToken: "access", accessTokenExpiresAt: "2099-01-01T00:00:00Z",
        refreshToken: "refresh", refreshTokenExpiresAt: "2099-02-01T00:00:00Z",
        sessionId: UUID(), deviceId: UUID(), authorizationScope: .globalAdministrator, category: nil),
      profileContext: "profile-context")
  }
  func save(_ credentials: StoredCredentials, for issuer: URL) async throws {}
  func clear(for issuer: URL) async throws {}
}

private final class FeatureTransport: HTTPTransport, @unchecked Sendable {
  private let lock = NSLock()
  private var recorded: [URLRequest] = []
  var requests: [URLRequest] { lock.withLock { recorded } }
  var lastRequest: URLRequest? { requests.last }

  func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
    let path = request.url!.path
    let body: Data
    if path == "/.well-known/rivune" {
      body = Data(#"{"name":"Rivune","serverVersion":"22","protocolVersion":22,"apiBaseUrl":"/api/v1","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en","capabilities":[]}"#.utf8)
    } else {
      lock.withLock { recorded.append(request) }
      switch path {
      case let value where value.hasSuffix("/queue/items"):
        body = Data(#"{"revision":8,"affectedItemId":"55555555-5555-4555-8555-555555555555","duplicate":false}"#.utf8)
      case "/api/v1/saved-searches":
        body = Data(#"{"savedSearches":[]}"#.utf8)
      case "/api/v1/smart-collections":
        body = Data(#"{"smartCollections":[]}"#.utf8)
      case let value where value.contains("/operations/extension-incidents/"):
        body = Data(#"{"incident":{"id":"33333333-3333-4333-8333-333333333333","profileId":"11111111-1111-4111-8111-111111111111","addonId":"66666666-6666-4666-8666-666666666666","addonName":"Catalog","code":"timeout","state":"open","impact":"availability","occurrenceCount":1,"firstOccurredAt":"2026-08-26T00:00:00Z","lastOccurredAt":"2026-08-26T00:00:00Z","lastSuccessAt":null,"recoveryStartedAt":null,"resolvedAt":null,"acknowledgedAt":null,"acknowledgedByUserId":null,"updatedAt":"2026-08-26T00:00:00Z"},"events":[]}"#.utf8)
      case "/api/v1/media-notifications":
        body = Data(#"{"notifications":[]}"#.utf8)
      case let value where value.hasSuffix("/accessibility-preferences"):
        body = Data(#"{"revision":0,"reducedMotion":"system","highContrast":"system","textScale":100,"captions":"system","audioDescription":false,"focusIndicators":"standard"}"#.utf8)
      case let value where value.hasSuffix("/advance"):
        body = Data(#"{"id":"44444444-4444-4444-8444-444444444444","currentSourceRef":"opaque-source-ref-2","currentPosition":1,"positionSeconds":18.5,"attemptCount":1,"maximumAttempts":2,"revision":4,"status":"active","lastError":"source_timeout","explanation":"Trying another source.","candidateHealth":[{"position":0,"status":"cooling_down"},{"position":1,"status":"current"}],"expiresAt":"2026-08-26T01:00:00Z"}"#.utf8)
      default:
        body = Data("{}".utf8)
      }
    }
    return (
      body,
      HTTPURLResponse(
        url: request.url!, statusCode: 200, httpVersion: nil,
        headerFields: ["Content-Type": "application/json"])!)
  }
}
