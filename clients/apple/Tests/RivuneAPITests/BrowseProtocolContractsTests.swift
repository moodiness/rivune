import Foundation
import XCTest

@testable import RivuneAPI

#if canImport(FoundationNetworking)
  import FoundationNetworking
#endif

final class BrowseProtocolContractsTests: XCTestCase { private let collectionId = UUID(uuidString: "11111111-1111-4111-8111-111111111111")!
private let folderId = UUID(uuidString: "22222222-2222-4222-8222-222222222222")!
private let addonId = UUID(uuidString: "33333333-3333-4333-8333-333333333333")!
private let titleId = UUID(uuidString: "44444444-4444-4444-8444-444444444444")!

func testFullCollectionFolderAndDynamicResourceDecode() throws {
  let collection = try JSONDecoder().decode(
    CollectionList.self, from: Data(Self.collectionJSON.utf8)
  ).collections[0]
  XCTAssertEqual(collection.viewMode, .tabbedGrid)
  XCTAssertEqual(collection.folders[0].sources[0].addonCatalog?.extra?[1].name, "genre")

  let folder = try JSONDecoder().decode(
    ResolvedCollectionFolder.self, from: Data(Self.folderJSON.utf8))
  XCTAssertEqual(folder.page, 2)
  XCTAssertTrue(folder.hasMore)
  XCTAssertEqual(folder.errors[0].code, .sourceTimeout)
  XCTAssertEqual(
    folder.items[0].raw,
    .object([
      "flag": .boolean(true), "signed": .signedInteger(-7), "unsigned": .signedInteger(9),
      "fraction": .floatingPoint(1.25), "nested": .array([.null, .string("ok")]),
    ]))

  let resource = try JSONDecoder().decode(
    AddonResourceResult.self, from: Data(Self.resourceJSON.utf8))
  XCTAssertEqual(resource.cache.maxAgeSeconds, 9_223_372_036_854_775_000)
  XCTAssertEqual(resource.extra?.map(\.name), ["genre", "genre"])
  XCTAssertEqual(resource.payload["active"], .boolean(true))
  XCTAssertEqual(resource.payload["minimum"], .signedInteger(Int64.min))
  XCTAssertEqual(resource.payload["maximum"], .unsignedInteger(UInt64.max))
  XCTAssertEqual(resource.payload["items"], .array([.floatingPoint(1.5), .null]))
  XCTAssertEqual(
    try JSONDecoder().decode(JSONValue.self, from: Data("18446744073709551615".utf8)),
    .unsignedInteger(UInt64.max))
}

func testSemanticSearchRequestAndResponseContract() async throws {
  let transport = BrowseTransport()
  let client = try makeClient(transport)

  let page = try await client.semanticSearch(
    .init(
      query: "film Dune de guerre",
      mediaType: "movie",
      language: "fr-FR",
      region: "FR",
      page: 2,
      limit: 40,
      excludedIntentIds: ["genre:war"]
    ))

  XCTAssertEqual(page.intents.map(\.id), ["media_type:movie"])
  XCTAssertEqual(page.titleQuery, "Dune guerre")
  XCTAssertEqual(page.items.first?.externalIds["tmdb"], "42")
  let request = try XCTUnwrap(transport.apiRequests().last)
  XCTAssertEqual(request.httpMethod, "POST")
  XCTAssertEqual(request.url?.path, "/api/v1/search/semantic")
  let body = try XCTUnwrap(request.httpBody)
  let encoded = try JSONDecoder().decode(SemanticSearchRequest.self, from: body)
  XCTAssertEqual(encoded.excludedIntentIds, ["genre:war"])
  XCTAssertEqual(encoded.page, 2)
  XCTAssertEqual(encoded.limit, 40)
}

func testBrowseRequestsPreserveRoutesQueriesBodiesAndMutations() async throws {
  let transport = BrowseTransport()
  let client = try makeClient(transport)
  _ = try await client.resolveCollectionFolder(
    collectionId: collectionId, folderId: folderId, page: 2, limit: 50, language: "fr-FR",
    region: "CA")
  _ = try await client.searchAddonCatalogs(
    type: "tv anime/été", search: "café & tea", skip: 4, limit: 24,
    extras: [.init(name: "genre", value: "A/B"), .init(name: "genre", value: "日本")])
  _ = try await client.addonResource(
    addonId: addonId, resource: "meta/x", type: "tv anime", id: "id/日本?x=1", skip: 3, limit: 10,
    extras: [.init(name: "genre", value: "Drama"), .init(name: "genre", value: "Comedy")])
  _ = try await client.addonResources(
    resource: "catalog/x", type: "movie tv", id: "opaque/id",
    extras: [.init(name: "x", value: "1"), .init(name: "x", value: "2")])
  _ = try await client.resolveTitle(
    .init(
      mediaType: .tv, provider: "addon", resourceId: "channel/1", title: "News",
      sourceAddonId: addonId, sourceCatalogId: "live"))
  _ = try await client.library(mediaType: .tv, page: 3, pageSize: 100)
  _ = try await client.tvLibraryMembership([
    .init(sourceAddonId: addonId, resourceId: "channel/1")
  ])
  _ = try await client.addLibraryTitle(id: titleId)
  try await client.removeLibraryTitle(id: titleId)
  _ = try await client.sessionNotifications(after: "9223372036854775807")
  try await client.acknowledgeSessionNotification(id: "9007199254740993")

  let requests = transport.apiRequests()
  XCTAssertEqual(
    requests.map(\.httpMethod),
    ["GET", "GET", "GET", "GET", "POST", "GET", "POST", "PUT", "DELETE", "GET", "DELETE"])
  XCTAssertEqual(
    requests[0].url?.path,
    "/api/v1/collections/\(collectionId.uuidString.lowercased())/folders/\(folderId.uuidString.lowercased())/items"
  )
  XCTAssertEqual(
    URLComponents(url: requests[1].url!, resolvingAgainstBaseURL: false)?.percentEncodedPath,
    "/api/v1/addons/catalogs/search/tv%20anime%2F%C3%A9t%C3%A9")
  XCTAssertEqual(
    URLComponents(url: requests[2].url!, resolvingAgainstBaseURL: false)?.percentEncodedPath,
    "/api/v1/addons/\(addonId.uuidString.lowercased())/resource/meta%2Fx/tv%20anime/id%2F%E6%97%A5%E6%9C%AC%3Fx=1"
  )
  XCTAssertEqual(
    URLComponents(url: requests[3].url!, resolvingAgainstBaseURL: false)?.percentEncodedPath,
    "/api/v1/addons/resources/catalog%2Fx/movie%20tv/opaque%2Fid")
  XCTAssertEqual(queryPairs(requests[0]), ["page=2", "limit=50", "language=fr-FR", "region=CA"])
  XCTAssertEqual(
    queryPairs(requests[1]), ["search=café & tea", "skip=4", "limit=24", "genre=A/B", "genre=日本"])
  XCTAssertEqual(queryPairs(requests[2]), ["skip=3", "limit=10", "genre=Drama", "genre=Comedy"])
  XCTAssertEqual(queryPairs(requests[3]), ["x=1", "x=2"])
  let titleBody = try XCTUnwrap(
    JSONSerialization.jsonObject(with: try XCTUnwrap(requests[4].httpBody)) as? [String: Any])
  XCTAssertEqual(titleBody["mediaType"] as? String, "tv")
  XCTAssertEqual(titleBody["provider"] as? String, "addon")
  XCTAssertEqual(
    (titleBody["sourceAddonId"] as? String)?.lowercased(), addonId.uuidString.lowercased())
  XCTAssertEqual(queryPairs(requests[5]), ["mediaType=tv", "page=3", "pageSize=100"])
  XCTAssertNil(requests[7].httpBody)
  XCTAssertEqual(requests[8].url?.path, "/api/v1/library/\(titleId.uuidString.lowercased())")
  XCTAssertEqual(queryPairs(requests[9]), ["after=9223372036854775807"])
  XCTAssertEqual(requests[10].url?.path, "/api/v1/auth/notifications/9007199254740993")
}

func testResourceURLAndProfileContextLifecycleIncludingRefresh() async throws {
  let transport = BrowseTransport()
  let client = try makeClient(transport)
  let artworkURL = try await client.resolveResponseResourceURL("/api/v1/artwork/key")
  let externalURL = try await client.resolveResponseResourceURL(
    "https://cdn.example.test/image.jpg")
  XCTAssertEqual(artworkURL.absoluteString, "https://example.test/api/v1/artwork/key")
  XCTAssertEqual(externalURL.absoluteString, "https://cdn.example.test/image.jpg")
  do {
    _ = try await client.resolveResponseResourceURL("http://cdn.example.test/image.jpg")
    XCTFail("Insecure absolute public resource URLs must be rejected")
  } catch RivuneAPIError.invalidServerURL {
    // Expected.
  }

  _ = try await client.selectProfile(id: folderId, pin: "1234")
  _ = try await client.sessionNotifications(after: "0")
  _ = try await client.collections()
  transport.failNextCollectionsWithUnauthorized()
  _ = try await client.collections()
  try await client.clearProfileSelection()
  _ = try await client.collections()

  let requests = transport.apiRequests()
  let select = try XCTUnwrap(requests.first { $0.url?.path.hasSuffix("/select") == true })
  XCTAssertNil(select.value(forHTTPHeaderField: "X-Rivune-Profile-Context"))
  let notification = try XCTUnwrap(
    requests.first { $0.url?.path.hasSuffix("/auth/notifications") == true })
  XCTAssertEqual(
    notification.value(forHTTPHeaderField: "X-Rivune-Profile-Context"), "opaque-profile-context")
  let clear = try XCTUnwrap(
    requests.first { $0.url?.path.hasSuffix("/profiles/selection") == true })
  XCTAssertNil(clear.value(forHTTPHeaderField: "X-Rivune-Profile-Context"))
  let collectionRequests = requests.filter { $0.url?.path.hasSuffix("/collections") == true }
  XCTAssertEqual(
    collectionRequests[0].value(forHTTPHeaderField: "X-Rivune-Profile-Context"),
    "opaque-profile-context")
  XCTAssertEqual(
    collectionRequests[1].value(forHTTPHeaderField: "X-Rivune-Profile-Context"),
    "opaque-profile-context")
  XCTAssertEqual(
    collectionRequests[2].value(forHTTPHeaderField: "X-Rivune-Profile-Context"),
    "opaque-profile-context")
  XCTAssertNil(collectionRequests[3].value(forHTTPHeaderField: "X-Rivune-Profile-Context"))
  XCTAssertTrue(requests.contains { $0.url?.path.hasSuffix("/auth/refresh") == true })
}

func testProfileContextIsRestoredByRecreatedClient() async throws {
  let server = URL(string: "https://example.test")!
  let store = BrowseCredentials(token: BrowseTransport.token(access: "access"))
  let selectionTransport = BrowseTransport()
  let selectingClient = try RivuneAPIClient(
    serverURL: server, transport: selectionTransport, credentialStore: store)

  _ = try await selectingClient.selectProfile(id: folderId)

  let restoredTransport = BrowseTransport()
  let restoredClient = try RivuneAPIClient(
    serverURL: server, transport: restoredTransport, credentialStore: store)
  let didRestoreSelection = try await restoredClient.restoreSession()
  XCTAssertTrue(didRestoreSelection)
  restoredTransport.failNextCollectionsWithUnauthorized()
  _ = try await restoredClient.collections()

  let restoredRequest = try XCTUnwrap(
    restoredTransport.apiRequests().first { $0.url?.path.hasSuffix("/collections") == true })
  XCTAssertEqual(
    restoredRequest.value(forHTTPHeaderField: "X-Rivune-Profile-Context"),
    "opaque-profile-context")

  let refreshedTransport = BrowseTransport()
  let refreshedClient = try RivuneAPIClient(
    serverURL: server, transport: refreshedTransport, credentialStore: store)
  let didRestoreRefresh = try await refreshedClient.restoreSession()
  XCTAssertTrue(didRestoreRefresh)
  _ = try await refreshedClient.collections()
  let refreshedRequest = try XCTUnwrap(
    refreshedTransport.apiRequests().first { $0.url?.path.hasSuffix("/collections") == true })
  XCTAssertEqual(
    refreshedRequest.value(forHTTPHeaderField: "X-Rivune-Profile-Context"),
    "opaque-profile-context")

  try await refreshedClient.clearProfileSelection()
  let clearedTransport = BrowseTransport()
  let clearedClient = try RivuneAPIClient(
    serverURL: server, transport: clearedTransport, credentialStore: store)
  let didRestoreClear = try await clearedClient.restoreSession()
  XCTAssertTrue(didRestoreClear)
  _ = try await clearedClient.collections()
  let clearedRequest = try XCTUnwrap(
    clearedTransport.apiRequests().first { $0.url?.path.hasSuffix("/collections") == true })
  XCTAssertNil(clearedRequest.value(forHTTPHeaderField: "X-Rivune-Profile-Context"))
}

func testProfileMutationCancelledBeforeTransportDoesNotReachServer() async throws {
  let transport = BrowseTransport()
  let client = try makeClient(transport)

  let select = Task { try await client.selectProfile(id: folderId) }
  select.cancel()
  do {
    _ = try await select.value
    XCTFail("A select cancelled before transport must not be sent")
  } catch is CancellationError {
    // Expected.
  }

  XCTAssertFalse(transport.apiRequests().contains { $0.url?.path.hasSuffix("/select") == true })
}

func testCancelledSelectReconcilesProfileContextAfterSuccessfulResponse() async throws {
  let server = URL(string: "https://example.test")!
  let store = BrowseCredentials(token: BrowseTransport.token(access: "access"))
  let transport = BrowseTransport()
  let client = try RivuneAPIClient(
    serverURL: server, transport: transport, credentialStore: store)
  transport.blockNextResponse(pathSuffix: "/select")

  let select = Task { try await client.selectProfile(id: folderId) }
  await transport.waitUntilResponseBlocked()
  select.cancel()
  transport.releaseBlockedResponse()

  do {
    _ = try await select.value
    XCTFail("Cancellation after a successful select response must be surfaced")
  } catch is CancellationError {
    // Expected after the committed profile context is reconciled.
  }

  let persistedSelection = try await store.load(for: server)
  XCTAssertEqual(persistedSelection?.profileContext, "opaque-profile-context")
  _ = try await client.collections()
  let nextRequest = try XCTUnwrap(
    transport.apiRequests().last { $0.url?.path.hasSuffix("/collections") == true })
  XCTAssertEqual(
    nextRequest.value(forHTTPHeaderField: "X-Rivune-Profile-Context"), "opaque-profile-context")

  let restoredTransport = BrowseTransport()
  let restoredClient = try RivuneAPIClient(
    serverURL: server, transport: restoredTransport, credentialStore: store)
  let didRestoreSelection = try await restoredClient.restoreSession()
  XCTAssertTrue(didRestoreSelection)
  _ = try await restoredClient.collections()
  let restoredRequest = try XCTUnwrap(
    restoredTransport.apiRequests().last { $0.url?.path.hasSuffix("/collections") == true })
  XCTAssertEqual(
    restoredRequest.value(forHTTPHeaderField: "X-Rivune-Profile-Context"),
    "opaque-profile-context")
}

func testCancelledClearReconcilesProfileContextAfterSuccessfulResponse() async throws {
  let server = URL(string: "https://example.test")!
  let store = BrowseCredentials(token: BrowseTransport.token(access: "access"))
  let transport = BrowseTransport()
  let client = try RivuneAPIClient(
    serverURL: server, transport: transport, credentialStore: store)
  _ = try await client.selectProfile(id: folderId)
  transport.blockNextResponse(pathSuffix: "/profiles/selection")

  let clear = Task { try await client.clearProfileSelection() }
  await transport.waitUntilResponseBlocked()
  clear.cancel()
  transport.releaseBlockedResponse()

  do {
    try await clear.value
    XCTFail("Cancellation after a successful clear response must be surfaced")
  } catch is CancellationError {
    // Expected after the cleared profile context is reconciled.
  }

  let persistedClear = try await store.load(for: server)
  XCTAssertNil(persistedClear?.profileContext)
  _ = try await client.collections()
  let nextRequest = try XCTUnwrap(
    transport.apiRequests().last { $0.url?.path.hasSuffix("/collections") == true })
  XCTAssertNil(nextRequest.value(forHTTPHeaderField: "X-Rivune-Profile-Context"))

  let restoredTransport = BrowseTransport()
  let restoredClient = try RivuneAPIClient(
    serverURL: server, transport: restoredTransport, credentialStore: store)
  let didRestoreClear = try await restoredClient.restoreSession()
  XCTAssertTrue(didRestoreClear)
  _ = try await restoredClient.collections()
  let restoredRequest = try XCTUnwrap(
    restoredTransport.apiRequests().last { $0.url?.path.hasSuffix("/collections") == true })
  XCTAssertNil(restoredRequest.value(forHTTPHeaderField: "X-Rivune-Profile-Context"))
}

func testAuthenticatedResponseCannotCrossProfileClear() async throws {
  let transport = BrowseTransport()
  let client = try makeClient(transport)
  _ = try await client.selectProfile(id: folderId)
  transport.failNextCollectionsWithUnauthorized()
  transport.blockNextRequest(pathSuffix: "/collections")

  let staleRequest = Task { try await client.collections() }
  await transport.waitUntilRequestBlocked()
  try await client.clearProfileSelection()
  transport.releaseBlockedRequest()

  do {
    _ = try await staleRequest.value
    XCTFail("A response issued under the previous profile must be cancelled")
  } catch is CancellationError {
    // Expected.
  }
  XCTAssertFalse(
    transport.apiRequests().contains { $0.url?.path.hasSuffix("/auth/refresh") == true })
}

func testConcurrentProfileMutationsCannotReachTheServer() async throws {
  let transport = BrowseTransport()
  let client = try makeClient(transport)
  _ = try await client.selectProfile(id: folderId)
  transport.blockNextRequest(pathSuffix: "/profiles/selection")

  let clear = Task { try await client.clearProfileSelection() }
  await transport.waitUntilRequestBlocked()
  do {
    _ = try await client.selectProfile(id: folderId)
    XCTFail("Concurrent profile mutations must not be sent")
  } catch is CancellationError {
    // Expected.
  }
  transport.releaseBlockedRequest()
  try await clear.value

  let selectRequests = transport.apiRequests().filter {
    $0.url?.path.hasSuffix("/select") == true
  }
  XCTAssertEqual(selectRequests.count, 1)
}

func testBrowseRequestDoesNotStartDuringProfileMutation() async throws {
  let transport = BrowseTransport()
  let client = try makeClient(transport)
  _ = try await client.selectProfile(id: folderId)
  transport.blockNextRequest(pathSuffix: "/profiles/selection")

  let clear = Task { try await client.clearProfileSelection() }
  await transport.waitUntilRequestBlocked()
  do {
    _ = try await client.collections()
    XCTFail("Browse must not start while profile state is changing")
  } catch is CancellationError {
    // Expected.
  }
  XCTAssertFalse(
    transport.apiRequests().contains { $0.url?.path.hasSuffix("/collections") == true })
  transport.releaseBlockedRequest()
  try await clear.value
}

private func makeClient(_ transport: BrowseTransport) throws -> RivuneAPIClient {
  try RivuneAPIClient(
    serverURL: URL(string: "https://example.test")!, transport: transport,
    credentialStore: BrowseCredentials(token: BrowseTransport.token(access: "access")))
}

private func queryPairs(_ request: URLRequest) -> [String] {
  (URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.queryItems ?? []).map {
    "\($0.name)=\($0.value ?? "")"
  }
}

fileprivate static let collectionJSON = """
  {"collections":[{"id":"11111111-1111-4111-8111-111111111111","title":"Home","backdropImageUrl":"/api/v1/artwork/a","heroEnabled":true,"pinToTop":false,"focusGlowEnabled":true,"viewMode":"tabbed_grid","folderCoverShape":"poster","folders":[{"id":"22222222-2222-4222-8222-222222222222","title":"Shows","tileShape":"landscape","sourceView":"merged","coverImageUrl":"/api/v1/artwork/b","coverEmoji":"🎬","titleLogoUrl":"/api/v1/artwork/c","heroBackdropUrl":"/api/v1/artwork/d","heroVideoUrl":"https://media.example/video.mp4","focusGifUrl":"https://media.example/focus.gif","focusGifEnabled":true,"hideTitle":false,"sources":[{"id":"55555555-5555-4555-8555-555555555555","kind":"addon_catalog","title":"Catalog","addonCatalog":{"addonId":"33333333-3333-4333-8333-333333333333","manifestId":"com.example","type":"series","catalogId":"top","extra":[{"name":"genre","value":"Drama"},{"name":"genre","value":"Comedy"}]}}]}],"profileIds":["66666666-6666-4666-8666-666666666666"],"categoryIds":["77777777-7777-4777-8777-777777777777"],"position":0,"version":922337203685477500,"createdAt":"2026-08-12T00:00:00Z","updatedAt":"2026-08-12T01:00:00Z"}]}
  """
fileprivate static let folderJSON = """
  {"collectionId":"11111111-1111-4111-8111-111111111111","folder":{"id":"22222222-2222-4222-8222-222222222222","title":"Shows","tileShape":"poster","sourceView":"folders","focusGifEnabled":false,"hideTitle":false,"sources":[{"id":"55555555-5555-4555-8555-555555555555","kind":"tmdb","title":"Popular","tmdb":{"sourceType":"discover","mediaType":"both","sort":"vote_average.desc","filters":{"genres":[18],"voteAverageMin":7.5,"companies":[42]}}}]},"sourcePosterUrls":{"55555555-5555-4555-8555-555555555555":"/api/v1/artwork/poster"},"items":[{"id":"opaque","mediaType":"series","title":"Show","posterUrl":"/api/v1/artwork/item","externalIds":{"imdb":"tt1"},"sources":[{"id":"55555555-5555-4555-8555-555555555555","kind":"tmdb","title":"Popular"}],"raw":{"flag":true,"signed":-7,"unsigned":9,"fraction":1.25,"nested":[null,"ok"]}}],"page":2,"hasMore":true,"errors":[{"sourceId":"55555555-5555-4555-8555-555555555555","kind":"tmdb","code":"collection_source_timeout","message":"timed out"}]}
  """
fileprivate static let resourceJSON = """
  {"addonId":"33333333-3333-4333-8333-333333333333","manifestId":"com.example","resource":"catalog","type":"series","id":"top","payload":{"active":true,"minimum":-9223372036854775808,"maximum":18446744073709551615,"items":[1.5,null]},"cache":{"maxAgeSeconds":9223372036854775000,"staleWhileRevalidateSeconds":60,"staleIfErrorSeconds":120},"extra":[{"name":"genre","value":"Drama"},{"name":"genre","value":"Comedy"}]}
  """ }

private actor BrowseCredentials: CredentialStore {
  private var credentials: StoredCredentials?

  init(token: TokenPair) {
    credentials = StoredCredentials(tokens: token, profileContext: nil)
  }

  func load(for issuer: URL) async throws -> StoredCredentials? { credentials }
  func save(_ credentials: StoredCredentials, for issuer: URL) async throws {
    self.credentials = credentials
  }
  func clear(for issuer: URL) async throws { credentials = nil }
}

private final class BrowseTransport: HTTPTransport, @unchecked Sendable {
  private let lock = NSLock()
  private var requests: [URLRequest] = []
  private var unauthorizedCollections = false
  private var didRejectCollections = false
  private var blockedPathSuffix: String?
  private var requestIsBlocked = false
  private var blockedRequestContinuation: CheckedContinuation<Void, Never>?
  private var blockedRequestWaiters: [CheckedContinuation<Void, Never>] = []
  private var blockedResponsePathSuffix: String?
  private var responseIsBlocked = false
  private var blockedResponseContinuation: CheckedContinuation<Void, Never>?
  private var blockedResponseWaiters: [CheckedContinuation<Void, Never>] = []

  static func token(access: String) -> TokenPair {
    TokenPair(
      tokenType: "Bearer", accessToken: access, accessTokenExpiresAt: "2026-08-12T12:00:00Z",
      refreshToken: "refresh", refreshTokenExpiresAt: "2026-09-12T12:00:00Z",
      sessionId: UUID(uuidString: "88888888-8888-4888-8888-888888888888")!,
      deviceId: UUID(uuidString: "99999999-9999-4999-8999-999999999999")!,
      authorizationScope: .globalAdministrator, category: nil)
  }
  private static func tokenJSON(access: String) -> String {
    "{\"tokenType\":\"Bearer\",\"accessToken\":\"\(access)\",\"accessTokenExpiresAt\":\"2026-08-12T12:00:00Z\",\"refreshToken\":\"refresh\",\"refreshTokenExpiresAt\":\"2026-09-12T12:00:00Z\",\"sessionId\":\"88888888-8888-4888-8888-888888888888\",\"deviceId\":\"99999999-9999-4999-8999-999999999999\",\"authorizationScope\":\"global_admin\",\"category\":null}"
  }

  func failNextCollectionsWithUnauthorized() {
    lock.withLock {
      unauthorizedCollections = true
      didRejectCollections = false
    }
  }
  func apiRequests() -> [URLRequest] { lock.withLock { requests } }

  func blockNextRequest(pathSuffix: String) {
    lock.withLock { blockedPathSuffix = pathSuffix }
  }

  func waitUntilRequestBlocked() async {
    if lock.withLock({ requestIsBlocked }) { return }
    await withCheckedContinuation { continuation in
      let resumeImmediately = lock.withLock { () -> Bool in
        if requestIsBlocked { return true }
        blockedRequestWaiters.append(continuation)
        return false
      }
      if resumeImmediately { continuation.resume() }
    }
  }

  func releaseBlockedRequest() {
    let continuation = lock.withLock { () -> CheckedContinuation<Void, Never>? in
      requestIsBlocked = false
      defer { blockedRequestContinuation = nil }
      return blockedRequestContinuation
    }
    continuation?.resume()
  }
  func blockNextResponse(pathSuffix: String) {
    lock.withLock { blockedResponsePathSuffix = pathSuffix }
  }

  func waitUntilResponseBlocked() async {
    if lock.withLock({ responseIsBlocked }) { return }
    await withCheckedContinuation { continuation in
      let resumeImmediately = lock.withLock { () -> Bool in
        if responseIsBlocked { return true }
        blockedResponseWaiters.append(continuation)
        return false
      }
      if resumeImmediately { continuation.resume() }
    }
  }

  func releaseBlockedResponse() {
    let continuation = lock.withLock { () -> CheckedContinuation<Void, Never>? in
      responseIsBlocked = false
      defer { blockedResponseContinuation = nil }
      return blockedResponseContinuation
    }
    continuation?.resume()
  }

  func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
    let path = request.url!.path
    if path == "/.well-known/rivune" {
      return response(
        request,
        body: Data(
          "{\"name\":\"Rivune\",\"serverVersion\":\"20\",\"protocolVersion\":22,\"apiBaseUrl\":\"/api/v1\",\"setupRequired\":false,\"timezone\":\"UTC\",\"interfaceLanguage\":\"en-US\"}"
            .utf8))
    }
    lock.withLock { requests.append(request) }
    let shouldBlock = lock.withLock { () -> Bool in
      guard let blockedPathSuffix, path.hasSuffix(blockedPathSuffix) else { return false }
      self.blockedPathSuffix = nil
      return true
    }
    if shouldBlock {
      await withCheckedContinuation { continuation in
        let waiters = lock.withLock { () -> [CheckedContinuation<Void, Never>] in
          blockedRequestContinuation = continuation
          requestIsBlocked = true
          defer { blockedRequestWaiters.removeAll() }
          return blockedRequestWaiters
        }
        waiters.forEach { $0.resume() }
      }
    }
    if path.hasSuffix("/collections") {
      let reject = lock.withLock { () -> Bool in
        if unauthorizedCollections && !didRejectCollections {
          didRejectCollections = true
          return true
        }
        return false
      }
      if reject {
        return response(
          request, status: 401,
          body: Data("{\"error\":{\"code\":\"expired\",\"message\":\"expired\"}}".utf8))
      }
      return response(request, body: Data(BrowseProtocolContractsTests.collectionJSON.utf8))
    }
    if path.hasSuffix("/auth/refresh") {
      return response(request, body: Data(Self.tokenJSON(access: "refreshed").utf8))
    }
    if path.hasSuffix("/select") {
      let result = response(request, body: Data(profileSelection.utf8))
      await holdResponseIfNeeded(path: path)
      return result
    }
    if path.hasSuffix("/selection") || request.httpMethod == "DELETE" {
      let result = response(request, status: 204, body: Data())
      await holdResponseIfNeeded(path: path)
      return result
    }
    if path.contains("/folders/") {
      return response(request, body: Data(BrowseProtocolContractsTests.folderJSON.utf8))
    }
    if path.hasSuffix("/search/semantic") {
      let body =
        "{\"intents\":[{\"id\":\"media_type:movie\",\"kind\":\"media_type\",\"value\":\"movie\",\"label\":\"Movies\"}],\"titleQuery\":\"Dune guerre\",\"mediaTypes\":[\"movie\"],\"items\":[{\"id\":\"tmdb:42\",\"mediaType\":\"movie\",\"title\":\"Dune\",\"externalIds\":{\"tmdb\":\"42\"},\"sources\":[]}],\"page\":2,\"hasMore\":false,\"partial\":false}"
      return response(request, body: Data(body.utf8))
    }
    if path.contains("/addons/") {
      return response(
        request,
        body: path.contains("/resources/") || path.contains("/search/")
          ? Data("{\"results\":[],\"errors\":[]}".utf8)
          : Data(BrowseProtocolContractsTests.resourceJSON.utf8))
    }
    if path.hasSuffix("/titles/resolve") {
      return response(
        request,
        body: Data(
          "{\"titleId\":\"44444444-4444-4444-8444-444444444444\",\"mediaType\":\"tv\",\"provider\":\"addon\",\"externalId\":\"33333333-3333-4333-8333-333333333333:channel/1\",\"resourceId\":\"channel/1\",\"title\":\"News\"}"
            .utf8))
    }
    if path.hasSuffix("/membership") { return response(request, body: Data("{\"items\":[]}".utf8)) }
    if path.hasSuffix("/library") {
      return response(
        request, body: Data("{\"items\":[],\"page\":3,\"totalPages\":0,\"totalResults\":0}".utf8))
    }
    if path.contains("/library/") {
      return response(
        request,
        body: Data(
          "{\"titleId\":\"44444444-4444-4444-8444-444444444444\",\"mediaType\":\"tv\",\"available\":true,\"addedAt\":\"2026-08-12T00:00:00Z\",\"updatedAt\":\"2026-08-12T00:00:00Z\"}"
            .utf8))
    }
    if path.hasSuffix("/notifications") {
      return response(
        request,
        body: Data(
          "{\"notifications\":[{\"id\":\"9007199254740993\",\"message\":\"hello\",\"senderUsername\":\"admin\",\"createdAt\":\"2026-08-12T00:00:00Z\"}]}"
            .utf8))
    }
    return response(request, body: Data("{\"results\":[],\"errors\":[]}".utf8))
  }

  private func holdResponseIfNeeded(path: String) async {
    let shouldBlock = lock.withLock { () -> Bool in
      guard let blockedResponsePathSuffix, path.hasSuffix(blockedResponsePathSuffix) else {
        return false
      }
      self.blockedResponsePathSuffix = nil
      return true
    }
    guard shouldBlock else { return }
    await withCheckedContinuation { continuation in
      let waiters = lock.withLock { () -> [CheckedContinuation<Void, Never>] in
        blockedResponseContinuation = continuation
        responseIsBlocked = true
        defer { blockedResponseWaiters.removeAll() }
        return blockedResponseWaiters
      }
      waiters.forEach { $0.resume() }
    }
  }

  private var profileSelection: String {
    "{\"profile\":{\"id\":\"22222222-2222-4222-8222-222222222222\",\"name\":\"Viewer\",\"description\":null,\"categoryId\":\"77777777-7777-4777-8777-777777777777\",\"category\":{\"id\":\"77777777-7777-4777-8777-777777777777\",\"name\":\"Home\",\"color\":null,\"icon\":null},\"isChild\":false,\"hasPin\":true,\"canManage\":false,\"enabled\":true,\"availableFrom\":null,\"availableUntil\":null,\"accessStartTime\":null,\"accessEndTime\":null,\"accessTimezone\":\"UTC\",\"accessible\":true,\"avatar\":{\"kind\":\"preset\",\"presetId\":\"one\",\"url\":\"/avatar\"}},\"expiresAt\":\"2026-08-12T12:00:00Z\",\"profileContext\":\"opaque-profile-context\"}"
  }

  private func response(_ request: URLRequest, status: Int = 200, body: Data) -> (
    Data, HTTPURLResponse
  ) {
    (
      body,
      HTTPURLResponse(
        url: request.url!, statusCode: status, httpVersion: nil,
        headerFields: ["Content-Type": "application/json"])!
    )
  }
}
