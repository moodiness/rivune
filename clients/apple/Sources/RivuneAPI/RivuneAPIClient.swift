import Foundation

#if canImport(FoundationNetworking)
  import FoundationNetworking
#endif

public protocol HTTPTransport: Sendable {
  func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse)
}

public struct URLSessionTransport: HTTPTransport {
  public static let maximumResponseBodyBytes = 16 * 1024 * 1024

  let loader: BoundedURLSessionLoader

  public init(
    session: URLSession = .shared,
    maximumResponseBodyBytes: Int = Self.maximumResponseBodyBytes
  ) {
    precondition(maximumResponseBodyBytes > 0)
    self.loader = BoundedURLSessionLoader(
      configuration: session.configuration,
      authenticationDelegate: session.delegate,
      delegateQueue: session.delegateQueue,
      maximumResponseBodyBytes: maximumResponseBodyBytes
    )
  }

  public func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
    try await loader.data(for: request)
  }
}

final class BoundedURLSessionLoader: @unchecked Sendable {
  private struct RequestState {
    let id: UUID
    let task: URLSessionDataTask
    let continuation: CheckedContinuation<(Data, HTTPURLResponse), Error>
    var response: HTTPURLResponse?
    var data = Data()
  }

  private let maximumResponseBodyBytes: Int
  let delegate: BoundedURLSessionDelegate
  private let lock = NSLock()
  private var states: [Int: RequestState] = [:]
  private var taskIdentifiers: [UUID: Int] = [:]
  private var cancelledRequests: Set<UUID> = []
  private var session: URLSession!

  init(
    configuration: URLSessionConfiguration,
    authenticationDelegate: (any URLSessionDelegate)?,
    delegateQueue: OperationQueue,
    maximumResponseBodyBytes: Int
  ) {
    self.maximumResponseBodyBytes = maximumResponseBodyBytes
    self.delegate = BoundedURLSessionDelegate(authenticationDelegate: authenticationDelegate)
    delegate.loader = self
    session = URLSession(
      configuration: configuration, delegate: delegate, delegateQueue: delegateQueue)
  }

  deinit {
    session.invalidateAndCancel()
  }

  func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
    let id = UUID()
    return try await withTaskCancellationHandler {
      try Task.checkCancellation()
      return try await withCheckedThrowingContinuation { continuation in
        let task = session.dataTask(with: request)
        let state = RequestState(id: id, task: task, continuation: continuation)

        lock.lock()
        let wasCancelled = cancelledRequests.remove(id) != nil
        if !wasCancelled {
          states[task.taskIdentifier] = state
          taskIdentifiers[id] = task.taskIdentifier
        }
        lock.unlock()

        if wasCancelled {
          task.cancel()
          continuation.resume(throwing: CancellationError())
        } else {
          task.resume()
        }
      }
    } onCancel: {
      self.cancel(id: id)
    }
  }

  func urlSession(
    _ session: URLSession,
    dataTask: URLSessionDataTask,
    didReceive response: URLResponse,
    completionHandler: @escaping (URLSession.ResponseDisposition) -> Void
  ) {
    guard let response = response as? HTTPURLResponse else {
      finish(
        taskIdentifier: dataTask.taskIdentifier, result: .failure(RivuneAPIError.invalidResponse))
      completionHandler(.cancel)
      return
    }
    let headerLength = response.value(forHTTPHeaderField: "Content-Length").flatMap { Int64($0) }
    let declaredLength = max(response.expectedContentLength, headerLength ?? -1)
    guard declaredLength < 0 || declaredLength <= Int64(maximumResponseBodyBytes) else {
      finish(
        taskIdentifier: dataTask.taskIdentifier,
        result: .failure(RivuneAPIError.responseTooLarge(maximumBytes: maximumResponseBodyBytes))
      )
      completionHandler(.cancel)
      return
    }

    lock.lock()
    if var state = states[dataTask.taskIdentifier] {
      state.response = response
      state.data.reserveCapacity(
        declaredLength >= 0
          ? min(Int(declaredLength), maximumResponseBodyBytes)
          : min(64 * 1024, maximumResponseBodyBytes)
      )
      states[dataTask.taskIdentifier] = state
    }
    lock.unlock()
    completionHandler(.allow)
  }

  func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
    var oversizedContinuation: CheckedContinuation<(Data, HTTPURLResponse), Error>?

    lock.lock()
    if var state = states[dataTask.taskIdentifier] {
      if data.count > maximumResponseBodyBytes - state.data.count {
        states.removeValue(forKey: dataTask.taskIdentifier)
        taskIdentifiers.removeValue(forKey: state.id)
        oversizedContinuation = state.continuation
      } else {
        state.data.append(data)
        states[dataTask.taskIdentifier] = state
      }
    }
    lock.unlock()

    if let oversizedContinuation {
      dataTask.cancel()
      oversizedContinuation.resume(
        throwing: RivuneAPIError.responseTooLarge(maximumBytes: maximumResponseBodyBytes)
      )
    }
  }

  func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
    if let error {
      finish(taskIdentifier: task.taskIdentifier, result: .failure(error))
      return
    }

    lock.lock()
    let state = states[task.taskIdentifier]
    lock.unlock()
    guard let state, let response = state.response else {
      finish(taskIdentifier: task.taskIdentifier, result: .failure(RivuneAPIError.invalidResponse))
      return
    }
    finish(taskIdentifier: task.taskIdentifier, result: .success((state.data, response)))
  }

  private func cancel(id: UUID) {
    var state: RequestState?
    lock.lock()
    if let taskIdentifier = taskIdentifiers.removeValue(forKey: id) {
      state = states.removeValue(forKey: taskIdentifier)
    } else {
      cancelledRequests.insert(id)
    }
    lock.unlock()

    state?.task.cancel()
    state?.continuation.resume(throwing: CancellationError())
  }

  private func finish(
    taskIdentifier: Int,
    result: Result<(Data, HTTPURLResponse), Error>
  ) {
    lock.lock()
    let state = states.removeValue(forKey: taskIdentifier)
    if let state {
      taskIdentifiers.removeValue(forKey: state.id)
    }
    lock.unlock()
    state?.continuation.resume(with: result)
  }
}

final class BoundedURLSessionDelegate: NSObject, URLSessionDataDelegate, @unchecked Sendable {
  weak var loader: BoundedURLSessionLoader?
  let authenticationDelegate: (any URLSessionDelegate)?

  init(authenticationDelegate: (any URLSessionDelegate)?) {
    self.authenticationDelegate = authenticationDelegate
  }

  func urlSession(
    _ session: URLSession,
    didReceive challenge: URLAuthenticationChallenge,
    completionHandler:
      @escaping @Sendable (URLSession.AuthChallengeDisposition, URLCredential?) -> Void
  ) {
    #if canImport(ObjectiveC)
      if let authenticationDelegate,
        authenticationDelegate.responds(
          to: #selector(URLSessionDelegate.urlSession(_:didReceive:completionHandler:))
        )
      {
        authenticationDelegate.urlSession?(
          session,
          didReceive: challenge,
          completionHandler: completionHandler
        )
        return
      }
    #else
      if let authenticationDelegate {
        authenticationDelegate.urlSession(
          session,
          didReceive: challenge,
          completionHandler: completionHandler
        )
        return
      }
    #endif
    completionHandler(.performDefaultHandling, nil)
  }

  func urlSession(
    _ session: URLSession,
    task: URLSessionTask,
    didReceive challenge: URLAuthenticationChallenge,
    completionHandler:
      @escaping @Sendable (URLSession.AuthChallengeDisposition, URLCredential?) -> Void
  ) {
    #if canImport(ObjectiveC)
      if let authenticationDelegate = authenticationDelegate as? any URLSessionTaskDelegate,
        authenticationDelegate.responds(
          to: #selector(URLSessionTaskDelegate.urlSession(_:task:didReceive:completionHandler:))
        )
      {
        authenticationDelegate.urlSession?(
          session,
          task: task,
          didReceive: challenge,
          completionHandler: completionHandler
        )
        return
      }
    #else
      if let authenticationDelegate = authenticationDelegate as? any URLSessionTaskDelegate {
        authenticationDelegate.urlSession(
          session,
          task: task,
          didReceive: challenge,
          completionHandler: completionHandler
        )
        return
      }
    #endif
    completionHandler(.performDefaultHandling, nil)
  }
  func urlSession(
    _ session: URLSession,
    task: URLSessionTask,
    willPerformHTTPRedirection response: HTTPURLResponse,
    newRequest request: URLRequest,
    completionHandler: @escaping @Sendable (URLRequest?) -> Void
  ) {
    completionHandler(nil)
  }

  func urlSession(
    _ session: URLSession,
    dataTask: URLSessionDataTask,
    didReceive response: URLResponse,
    completionHandler: @escaping (URLSession.ResponseDisposition) -> Void
  ) {
    guard let loader else {
      completionHandler(.cancel)
      return
    }
    loader.urlSession(
      session,
      dataTask: dataTask,
      didReceive: response,
      completionHandler: completionHandler
    )
  }

  func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
    loader?.urlSession(session, dataTask: dataTask, didReceive: data)
  }

  func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
    loader?.urlSession(session, task: task, didCompleteWithError: error)
  }
}

public struct ServerError: Codable, Sendable, Equatable {
  public let code: String
  public let message: String
}

private struct ErrorEnvelope: Codable {
  let error: ServerError
}

private struct DiscoveryEnvelope: Decodable {
  let name: String
  let serverVersion: String
  let protocolVersion: Int
  let apiBaseUrl: String
  let setupRequired: Bool
  let setupCompleted: Bool?
  let demoAvailable: Bool?
  let timezone: String
  let interfaceLanguage: String?
  let capabilities: [String]

  init(from decoder: Decoder) throws {
    let values = try decoder.container(keyedBy: CodingKeys.self)
    name = try values.decode(String.self, forKey: .name)
    serverVersion = try values.decode(String.self, forKey: .serverVersion)
    protocolVersion = try values.decode(Int.self, forKey: .protocolVersion)
    apiBaseUrl = try values.decode(String.self, forKey: .apiBaseUrl)
    setupRequired = try values.decode(Bool.self, forKey: .setupRequired)
    setupCompleted = try values.decodeIfPresent(Bool.self, forKey: .setupCompleted)
    demoAvailable = try values.decodeIfPresent(Bool.self, forKey: .demoAvailable)
    timezone = try values.decode(String.self, forKey: .timezone)
    interfaceLanguage = try values.decodeIfPresent(String.self, forKey: .interfaceLanguage)
    capabilities = Discovery.decodeCapabilities(from: values, forKey: .capabilities)
  }

  private enum CodingKeys: String, CodingKey {
    case name, serverVersion, protocolVersion, apiBaseUrl, setupRequired, setupCompleted
    case demoAvailable, timezone, interfaceLanguage, capabilities
  }
}

public enum RivuneAPIError: Error, LocalizedError, Sendable {
  case incompatibleProtocol(expected: Int, actual: Int)
  case invalidServerURL(String)
  case invalidResponse
  case notAuthenticated
  case responseTooLarge(maximumBytes: Int)
  case server(status: Int, code: String, message: String, retryAfterSeconds: Int?)

  public var errorDescription: String? {
    switch self {
    case .incompatibleProtocol(let expected, let actual):
      return "Rivune protocol \(actual) is incompatible; this client requires \(expected)."
    case .invalidServerURL(let value):
      return "Invalid Rivune server URL: \(value)"
    case .invalidResponse:
      return "The Rivune server returned an invalid response."
    case .notAuthenticated:
      return "Authentication is required."
    case .responseTooLarge(let maximumBytes):
      return "The Rivune server response exceeded the \(maximumBytes)-byte limit."
    case .server(_, _, let message, _):
      return message
    }
  }
}

private struct RefreshRequest: Encodable { let refreshToken: String }
private struct SelectProfileRequest: Encodable { let pin: String? }
private struct PlaybackSourcesRequest: Encodable {
  let mediaType: String
  let addonId: UUID?
  let resourceId: String
  let capabilities: PlaybackCapabilities
}
private struct PlaybackPrepareRequest: Encodable {
  let sourceRef: String
  let startSeconds: Int?
  let externalPlayer: Bool?
}
private struct PlaybackResolveRequest: Encodable {
  let sourceRef: String
  let titleId: String?
  let preferredAudioTrack: Int?
  let preferredSubtitleId: String?
  let startSeconds: Int?
  let externalPlayer: Bool?
}

private struct RefreshOperation {
  let generation: UInt64
  let refreshToken: String
  let task: Task<TokenPair, Error>
}

private struct PendingProfileContextPersistence {
  let authenticationGeneration: UInt64
  let selectionGeneration: UInt64
  let value: String?
}

private struct HTTPResult {
  let data: Data
  let response: HTTPURLResponse
}
private final class ProfileMutationCancellation: @unchecked Sendable {
  private let lock = NSLock()
  private var cancelled = false

  func cancel() {
    lock.lock()
    cancelled = true
    lock.unlock()
  }

  func check() throws {
    lock.lock()
    let isCancelled = cancelled
    lock.unlock()
    if isCancelled { throw CancellationError() }
  }
}

public actor RivuneAPIClient {
  private let serverURL: URL
  private let transport: any HTTPTransport
  private let credentialStore: OrderedCredentialStore
  private let encoder: JSONEncoder
  private let decoder: JSONDecoder
  private var apiBaseURL: URL?
  private var credentials: TokenPair?
  private var loadedCredentials = false
  private var authenticationGeneration: UInt64 = 0
  private var refreshOperation: RefreshOperation?
  private var pendingAuthenticationCancellations: [UUID: @Sendable () -> Void] = [:]
  private var profileContext: String?
  private var profileSelectionGeneration: UInt64 = 0
  private var profileSelectionMutationInFlight = false
  private var pendingProfileContextPersistence: PendingProfileContextPersistence?

  public init(
    serverURL: URL,
    transport: any HTTPTransport = URLSessionTransport(),
    credentialStore: any CredentialStore
  ) throws {
    self.serverURL = try Self.canonicalServerOrigin(serverURL)
    self.transport = transport
    self.credentialStore = OrderedCredentialStore(store: credentialStore)
    self.encoder = JSONEncoder()
    self.decoder = JSONDecoder()
  }

  #if canImport(Security)
    public init(serverURL: URL, transport: any HTTPTransport = URLSessionTransport()) throws {
      #if DEBUG && os(macOS)
        let credentialStore: any CredentialStore = DebugFileCredentialStore()
      #else
        let credentialStore: any CredentialStore = KeychainCredentialStore()
      #endif
      try self.init(serverURL: serverURL, transport: transport, credentialStore: credentialStore)
    }
  #endif

  @discardableResult
  public func discover() async throws -> Discovery {
    guard let url = URL(string: "/.well-known/rivune", relativeTo: serverURL)?.absoluteURL else {
      throw RivuneAPIError.invalidServerURL(serverURL.absoluteString)
    }
    let response: DiscoveryEnvelope = try await perform(
      url: url, method: "GET", body: Optional<Data>.none, authenticated: false,
      retryAfterRefresh: false)
    guard response.protocolVersion == RivuneProtocol.version else {
      throw RivuneAPIError.incompatibleProtocol(
        expected: RivuneProtocol.version, actual: response.protocolVersion)
    }
    guard let interfaceLanguage = response.interfaceLanguage else {
      throw RivuneAPIError.invalidResponse
    }
    let discovery = Discovery(
      name: response.name,
      serverVersion: response.serverVersion,
      protocolVersion: response.protocolVersion,
      apiBaseUrl: response.apiBaseUrl,
      setupRequired: response.setupRequired,
      setupCompleted: response.setupCompleted,
      demoAvailable: response.demoAvailable,
      timezone: response.timezone,
      interfaceLanguage: interfaceLanguage,
      capabilities: response.capabilities
    )
    guard let resolved = URL(string: discovery.apiBaseUrl, relativeTo: serverURL)?.absoluteURL,
      try Self.canonicalServerOrigin(resolved) == serverURL
    else {
      throw RivuneAPIError.invalidServerURL(discovery.apiBaseUrl)
    }
    apiBaseURL = resolved
    return discovery
  }

  @discardableResult
  public func restoreSession() async throws -> Bool {
    let generation = try await beginCredentialReplacement(preserveStoredCredentials: true)
    return try await runAuthenticationOperation {
      let restored = try await self.credentialStore.load(
        for: self.serverURL,
        generation: generation
      )
      try Task.checkCancellation()
      let currentGeneration = await self.authenticationGeneration
      guard generation == currentGeneration else { throw CancellationError() }
      await self.installRestoredCredentials(restored)
      return restored != nil
    }
  }

  @discardableResult
  public func login(username: String, password: String, device: LoginDevice) async throws
    -> TokenPair
  {
    let generation = try await beginCredentialReplacement(preserveStoredCredentials: false)
    return try await runAuthenticationOperation {
      let payload = LoginRequest(username: username, password: password, device: device)
      let tokens: TokenPair = try await self.request(
        "auth/login",
        method: "POST",
        body: payload,
        authenticated: false
      )
      try Task.checkCancellation()
      try await self.setCredentials(tokens, generation: generation)
      return tokens
    }
  }

  @discardableResult
  public func refreshSession() async throws -> TokenPair {
    try await loadCredentialsIfNeeded()
    return try await refreshCredentials()
  }

  public func logout() async throws {
    authenticationGeneration += 1
    let generation = authenticationGeneration
    let capturedCredentials = credentials.map {
      StoredCredentials(tokens: $0, profileContext: profileContext)
    }
    credentials = nil
    profileContext = nil
    profileSelectionGeneration += 1
    loadedCredentials = true
    refreshOperation?.task.cancel()
    refreshOperation = nil
    for cancel in pendingAuthenticationCancellations.values {
      cancel()
    }
    pendingAuthenticationCancellations.removeAll()

    let cleanup = await credentialStore.invalidateAndClear(
      for: serverURL,
      generation: generation,
      capturedCredentials: capturedCredentials
    )

    var remoteError: Error?
    if let accessToken = cleanup.credentials?.tokens.accessToken {
      do {
        _ = try await requestData(
          "auth/logout",
          method: "POST",
          body: Optional<Data>.none,
          authorizationToken: accessToken,
          retryAfterRefresh: false
        )
      } catch {
        remoteError = error
      }
    }

    if let localError = cleanup.error { throw localError }
    if let remoteError { throw remoteError }
  }

  public func currentAccount() async throws -> Account {
    try await request("auth/me", authenticated: true)
  }

  public func sessions() async throws -> [Session] {
    let result: SessionList = try await request("auth/sessions", authenticated: true)
    return result.sessions
  }

  public func categories() async throws -> [Category] {
    let result: CategoryList = try await request("categories", authenticated: true)
    return result.categories
  }

  public func createCategory(_ input: CategoryCreateRequest) async throws -> Category {
    try await request("categories", method: "POST", body: input, authenticated: true)
  }

  public func updateCategory(id: UUID, input: CategoryUpdateRequest) async throws -> Category {
    try await request(
      "categories/\(id.uuidString.lowercased())", method: "PATCH", body: input, authenticated: true)
  }

  public func deleteCategory(id: UUID, reassignToCategoryId: UUID? = nil) async throws {
    _ = try await requestData(
      "categories/\(id.uuidString.lowercased())",
      method: "DELETE",
      body: try encoder.encode(CategoryDeleteRequest(reassignToCategoryId: reassignToCategoryId)),
      authenticated: true
    )
  }

  public func reorderCategories(_ categoryIds: [UUID]) async throws -> [Category] {
    let result: CategoryList = try await request(
      "categories/order",
      method: "PUT",
      body: CategoryOrderRequest(categoryIds: categoryIds),
      authenticated: true
    )
    return result.categories
  }

  public func devices(categoryId: UUID? = nil) async throws -> [Device] {
    let result: DeviceList = try await request(
      "devices",
      query: queryItems(("categoryId", categoryId?.uuidString.lowercased())),
      authenticated: true
    )
    return result.devices
  }

  public func updateDevice(id: UUID, input: DeviceUpdateRequest) async throws -> Device {
    try await request(
      "devices/\(id.uuidString.lowercased())", method: "PATCH", body: input, authenticated: true)
  }

  public func moveProfiles(_ profileIds: [UUID], to categoryId: UUID) async throws {
    _ = try await requestData(
      "profiles/category-moves",
      method: "POST",
      body: try encoder.encode(
        ProfileCategoryMoveRequest(profileIds: profileIds, categoryId: categoryId)),
      authenticated: true
    )
  }

  public func moveDevices(_ deviceIds: [UUID], to categoryId: UUID) async throws {
    _ = try await requestData(
      "devices/category-moves",
      method: "POST",
      body: try encoder.encode(
        DeviceCategoryMoveRequest(deviceIds: deviceIds, categoryId: categoryId)),
      authenticated: true
    )
  }

  public func beginDeviceAuthorization(installationId: String, deviceName: String, platform: String)
    async throws -> DeviceAuthorizationResponse
  {
    try await request(
      "auth/device-code",
      method: "POST",
      body: DeviceAuthorizationRequest(
        installationId: installationId, deviceName: deviceName, platform: platform),
      authenticated: false
    )
  }

  @discardableResult
  public func exchangeDeviceAuthorization(deviceCode: String) async throws -> TokenPair {
    let generation = try await beginCredentialReplacement(preserveStoredCredentials: false)
    return try await runAuthenticationOperation {
      let tokens: TokenPair = try await self.request(
        "auth/device-code/token",
        method: "POST",
        body: DeviceCodeTokenRequest(deviceCode: deviceCode),
        authenticated: false
      )
      try Task.checkCancellation()
      try await self.setCredentials(tokens, generation: generation)
      return tokens
    }
  }

  public func approveDeviceAuthorization(_ input: DeviceCodeApprovalRequest) async throws {
    _ = try await requestData(
      "auth/device-code/approve", method: "POST", body: try encoder.encode(input),
      authenticated: true)
  }

  public func profiles() async throws -> [Profile] {
    let result: ProfileList = try await request("profiles", authenticated: true)
    return result.profiles
  }

  public func profileAvatar(id: UUID) async throws -> Data {
    try await requestData(
      "profiles/\(id.uuidString.lowercased())/avatar",
      method: "GET",
      body: nil,
      authenticated: true
    )
  }

  public func selectProfile(id: UUID, pin: String? = nil) async throws -> ProfileSelection {
    try Task.checkCancellation()
    guard !profileSelectionMutationInFlight else { throw CancellationError() }
    profileSelectionMutationInFlight = true
    defer { profileSelectionMutationInFlight = false }
    let generation = authenticationGeneration
    profileSelectionGeneration += 1
    let selectionGeneration = profileSelectionGeneration
    if apiBaseURL == nil { _ = try await discover() }
    try await loadCredentialsIfNeeded()
    guard generation == authenticationGeneration,
      selectionGeneration == profileSelectionGeneration
    else {
      throw CancellationError()
    }
    guard let authorizationToken = credentials?.accessToken else {
      throw RivuneAPIError.notAuthenticated
    }
    let url = try resolvedAPIURL(
      path: "profiles/\(id.uuidString.lowercased())/select",
      query: []
    )
    let body = try encoder.encode(SelectProfileRequest(pin: pin))
    try Task.checkCancellation()
    let callerCancellation = ProfileMutationCancellation()
    return try await withTaskCancellationHandler {
      let operation = Task.detached { [self] in
        try await self.performAndCommitProfileSelection(
          url: url,
          body: body,
          authorizationToken: authorizationToken,
          authenticationGeneration: generation,
          selectionGeneration: selectionGeneration,
          callerCancellation: callerCancellation
        )
      }
      let result = try await operation.value
      try Task.checkCancellation()
      return result
    } onCancel: {
      callerCancellation.cancel()
    }
  }

  public func clearProfileSelection() async throws {
    try Task.checkCancellation()
    guard !profileSelectionMutationInFlight else { throw CancellationError() }
    profileSelectionMutationInFlight = true
    defer { profileSelectionMutationInFlight = false }
    let generation = authenticationGeneration
    profileSelectionGeneration += 1
    let selectionGeneration = profileSelectionGeneration
    if apiBaseURL == nil { _ = try await discover() }
    try await loadCredentialsIfNeeded()
    guard generation == authenticationGeneration,
      selectionGeneration == profileSelectionGeneration
    else {
      throw CancellationError()
    }
    guard let authorizationToken = credentials?.accessToken else {
      throw RivuneAPIError.notAuthenticated
    }
    let url = try resolvedAPIURL(path: "profiles/selection", query: [])
    try Task.checkCancellation()
    let callerCancellation = ProfileMutationCancellation()
    try await withTaskCancellationHandler {
      let operation = Task.detached { [self] in
        try await self.performAndCommitProfileClear(
          url: url,
          authorizationToken: authorizationToken,
          authenticationGeneration: generation,
          selectionGeneration: selectionGeneration,
          callerCancellation: callerCancellation
        )
      }
      try await operation.value
      try Task.checkCancellation()
    } onCancel: {
      callerCancellation.cancel()
    }
  }

  private func performAndCommitProfileSelection(
    url: URL,
    body: Data,
    authorizationToken: String,
    authenticationGeneration generation: UInt64,
    selectionGeneration: UInt64,
    callerCancellation: ProfileMutationCancellation
  ) async throws -> ProfileSelection {
    let result = try await perform(
      url: url,
      method: "POST",
      body: body,
      authorizationToken: authorizationToken,
      retryAfterRefresh: true,
      expectedAuthenticationGeneration: generation,
      expectedProfileSelectionGeneration: selectionGeneration,
      profileMutationCancellation: callerCancellation
    )
    let selection: ProfileSelection
    do {
      selection = try decoder.decode(ProfileSelection.self, from: result.data)
    } catch {
      throw RivuneAPIError.invalidResponse
    }
    try await persistProfileContext(
      selection.profileContext,
      authenticationGeneration: generation,
      selectionGeneration: selectionGeneration
    )
    return selection
  }

  private func performAndCommitProfileClear(
    url: URL,
    authorizationToken: String,
    authenticationGeneration generation: UInt64,
    selectionGeneration: UInt64,
    callerCancellation: ProfileMutationCancellation
  ) async throws {
    _ = try await perform(
      url: url,
      method: "DELETE",
      body: nil,
      authorizationToken: authorizationToken,
      retryAfterRefresh: true,
      expectedAuthenticationGeneration: generation,
      expectedProfileSelectionGeneration: selectionGeneration,
      profileMutationCancellation: callerCancellation
    )
    try await persistProfileContext(
      nil,
      authenticationGeneration: generation,
      selectionGeneration: selectionGeneration
    )
  }

  private func persistProfileContext(
    _ value: String?,
    authenticationGeneration generation: UInt64,
    selectionGeneration: UInt64
  ) async throws {
    guard generation == authenticationGeneration,
      selectionGeneration == profileSelectionGeneration
    else {
      throw CancellationError()
    }
    pendingProfileContextPersistence = PendingProfileContextPersistence(
      authenticationGeneration: generation,
      selectionGeneration: selectionGeneration,
      value: value
    )
    defer {
      if pendingProfileContextPersistence?.selectionGeneration == selectionGeneration {
        pendingProfileContextPersistence = nil
      }
    }
    guard let tokens = credentials else { throw RivuneAPIError.notAuthenticated }
    let stored = StoredCredentials(tokens: tokens, profileContext: value)
    let saved = try await credentialStore.save(stored, for: serverURL, generation: generation)
    guard saved,
      generation == authenticationGeneration,
      selectionGeneration == profileSelectionGeneration
    else {
      throw CancellationError()
    }
    profileContext = value
    profileSelectionGeneration += 1
  }

  public func instanceSettings() async throws -> SettingsLayer {
    try await request("settings", authenticated: true)
  }

  public func updateInstanceSettings(_ patch: InstanceTranscodingPatch) async throws
    -> SettingsLayer
  {
    try await request("settings", method: "PATCH", body: patch, authenticated: true)
  }

  public func profileSettings(id: UUID) async throws -> SettingsLayer {
    try await request("profiles/\(id.uuidString.lowercased())/settings", authenticated: true)
  }

  public func updateProfileSettings(id: UUID, patch: ProfileSettingsPatch) async throws
    -> SettingsLayer
  {
    try await request(
      "profiles/\(id.uuidString.lowercased())/settings", method: "PATCH", body: patch,
      authenticated: true)
  }

  public func effectiveProfileSettings(id: UUID) async throws -> EffectiveSettings {
    try await request(
      "profiles/\(id.uuidString.lowercased())/settings/effective", authenticated: true)
  }

  public func movie(id: UUID, language: String? = nil) async throws -> Movie {
    try await request(
      "metadata/titles/\(id.uuidString.lowercased())", query: queryItems(("language", language)),
      authenticated: true)
  }

  public func series(
    id: UUID,
    language: String? = nil,
    mappingProvider: SeriesMappingProvider,
    episodeOrder: String? = nil
  ) async throws -> Series {
    try await request(
      "metadata/series/\(id.uuidString.lowercased())",
      query: queryItems(
        ("language", language),
        ("mappingProvider", mappingProvider.rawValue),
        ("episodeOrder", episodeOrder)
      ),
      authenticated: true
    )
  }

  public func season(id: String, language: String? = nil, mappingProvider: SeriesMappingProvider)
    async throws -> Season
  {
    try await request(
      "metadata/seasons/\(pathComponent(id))",
      query: queryItems(("language", language), ("mappingProvider", mappingProvider.rawValue)),
      authenticated: true)
  }

  public func trailers(
    titleId: UUID, language: String? = nil, captionLanguage: String? = nil, seasonNumber: Int? = nil
  ) async throws -> TrailerList {
    try await request(
      "metadata/titles/\(titleId.uuidString.lowercased())/trailers",
      query: queryItems(
        ("language", language), ("captionLanguage", captionLanguage),
        ("seasonNumber", seasonNumber.map(String.init))),
      authenticated: true
    )
  }

  public func playbackSources(
    mediaType: String,
    addonId: UUID? = nil,
    resourceId: String,
    capabilities: PlaybackCapabilities
  ) async throws -> PlaybackSourceList {
    try await request(
      "playback/sources",
      method: "POST",
      body: PlaybackSourcesRequest(
        mediaType: mediaType, addonId: addonId, resourceId: resourceId, capabilities: capabilities),
      authenticated: true
    )
  }

  public func preparePlayback(
    sourceRef: String, startSeconds: Int? = nil, externalPlayer: Bool = false
  ) async throws -> PlaybackPreparation {
    try await request(
      "playback/prepare", method: "POST",
      body: PlaybackPrepareRequest(
        sourceRef: sourceRef, startSeconds: startSeconds,
        externalPlayer: externalPlayer ? true : nil), authenticated: true)
  }

  public func resolvePlayback(
    sourceRef: String,
    titleId: String? = nil,
    preferredAudioTrack: Int? = nil,
    preferredSubtitleId: String? = nil,
    startSeconds: Int? = nil,
    externalPlayer: Bool = false
  ) async throws -> PlaybackSession {
    try await request(
      "playback/resolve",
      method: "POST",
      body: PlaybackResolveRequest(
        sourceRef: sourceRef,
        titleId: titleId,
        preferredAudioTrack: preferredAudioTrack,
        preferredSubtitleId: preferredSubtitleId,
        startSeconds: startSeconds,
        externalPlayer: externalPlayer ? true : nil
      ),
      authenticated: true
    )
  }

  public func playbackMarkers(imdbId: String, season: Int, episode: Int) async throws
    -> PlaybackMarkerList
  {
    try await request(
      "playback/markers",
      query: queryItems(
        ("imdbId", imdbId), ("season", String(season)), ("episode", String(episode))),
      authenticated: true
    )
  }

  public func stopPlayback(sessionId: UUID) async throws {
    _ = try await requestData(
      "playback/sessions/\(sessionId.uuidString.lowercased())", method: "DELETE",
      body: Optional<Data>.none, authenticated: true)
  }
  public func updatePlaybackDevice(_ input: PlaybackDeviceHeartbeatInput) async throws
    -> PlaybackDevice
  {
    try await request("playback/device", method: "PUT", body: input, authenticated: true)
  }

  public func playbackDevices() async throws -> PlaybackDeviceList {
    try await request("playback/devices", authenticated: true)
  }

  public func sendPlaybackCommand(sessionId: UUID, input: PlaybackCommandInput) async throws
    -> PlaybackCommand
  {
    try await request(
      "playback/devices/\(sessionId.uuidString.lowercased())/commands", method: "POST", body: input,
      authenticated: true)
  }

  public func playbackCommands(after operationId: UUID? = nil) async throws -> PlaybackCommandList {
    try await request(
      "playback/commands",
      query: queryItems(("after", operationId?.uuidString.lowercased())), authenticated: true)
  }

  @discardableResult
  public func reportPlaybackCommandResult(
    operationId: UUID, input: PlaybackCommandResultInput
  ) async throws -> PlaybackCommandResult {
    try await request(
      "playback/commands/incoming/\(operationId.uuidString.lowercased())/result", method: "PUT", body: input,
      authenticated: true)
  }

  public func outgoingPlaybackCommand(operationId: UUID) async throws -> OutgoingPlaybackCommand {
    try await request(
      "playback/commands/outgoing/\(operationId.uuidString.lowercased())", authenticated: true)
  }

  public func createPlaybackRoom(_ input: PlaybackRoomCreateInput) async throws -> PlaybackRoom {
    try await request("playback/rooms", method: "POST", body: input, authenticated: true)
  }

  public func joinPlaybackRoom(code: String) async throws -> PlaybackRoom {
    try await request(
      "playback/rooms/join", method: "POST", body: PlaybackRoomJoinInput(code: code),
      authenticated: true)
  }

  public func playbackRoom(id: UUID) async throws -> PlaybackRoom {
    try await request("playback/rooms/\(id.uuidString.lowercased())", authenticated: true)
  }

  public func updatePlaybackRoom(id: UUID, input: PlaybackRoomUpdateInput) async throws
    -> PlaybackRoom
  {
    try await request(
      "playback/rooms/\(id.uuidString.lowercased())", method: "PUT", body: input,
      authenticated: true)
  }

  public func leavePlaybackRoom(id: UUID) async throws {
    _ = try await requestData(
      "playback/rooms/\(id.uuidString.lowercased())", method: "DELETE", body: Optional<Data>.none,
      authenticated: true)
  }

  public func exportProfileArchive(profileId: UUID) async throws -> ProfileArchiveDocument {
    let data = try await requestData(
      "profiles/\(profileId.uuidString.lowercased())/archive", method: "GET", body: nil,
      authenticated: true)
    return try ProfileArchiveDocument(data: data)
  }

  public func mergeProfileArchive(
    profileId: UUID, archive: ProfileArchiveDocument
  ) async throws -> ProfileArchiveImportReport {
    try await request(
      "profiles/\(profileId.uuidString.lowercased())/archive/import", method: "POST",
      body: archive, authenticated: true)
  }

  public func createProfileFromArchive(
    categoryId: UUID, archive: ProfileArchiveDocument
  ) async throws -> ProfileArchiveImportReport {
    try await request(
      "profiles/archive", method: "POST",
      body: ProfileArchiveCreateInput(categoryId: categoryId, archive: archive), authenticated: true)
  }

  public func localRecommendations(limit: Int = 20, artworkShape: RecommendationArtworkShape? = nil)
    async throws -> LocalRecommendationPage
  {
    try await request(
      "recommendations",
      query: queryItems(("limit", String(limit)), ("artworkShape", artworkShape?.rawValue)),
      authenticated: true
    )
  }

  public func playbackActivity() async throws -> PlaybackActivity {
    try await request("playback/activity", authenticated: true)
  }

  public func playbackProgress(titleId: UUID) async throws -> PlaybackProgress? {
    let result = try await requestResult(
      "progress/\(titleId.uuidString.lowercased())",
      method: "GET",
      body: nil,
      authenticated: true
    )
    if result.response.statusCode == 204 { return nil }
    do { return try decoder.decode(PlaybackProgress.self, from: result.data) } catch {
      throw RivuneAPIError.invalidResponse
    }
  }

  public func playbackProgressBatch(titleIds: [UUID]) async throws -> PlaybackProgressBatch {
    try await request(
      "progress/batch",
      method: "POST",
      body: PlaybackProgressBatchRequest(titleIds: titleIds),
      authenticated: true
    )
  }

  public func updatePlaybackProgress(titleId: UUID, input: UpdatePlaybackProgressRequest)
    async throws -> PlaybackProgress
  {
    try await request(
      "progress/\(titleId.uuidString.lowercased())",
      method: "PUT",
      body: input,
      authenticated: true
    )
  }

  public func clearPlaybackProgress(titleId: UUID, expectedVersion: Int64) async throws {
    _ = try await requestData(
      "progress/\(titleId.uuidString.lowercased())",
      method: "DELETE",
      query: queryItems(("expectedVersion", String(expectedVersion))),
      body: nil,
      authenticated: true
    )
  }

  public func setTitlesWatchedBatch(_ items: [SetWatchedBatchItem]) async throws
    -> SetWatchedBatchResult
  {
    try await request(
      "titles/watched/batch",
      method: "PUT",
      body: SetWatchedBatchRequest(items: items),
      authenticated: true
    )
  }

  public func markTitleWatched(titleId: UUID, expectedVersion: Int64) async throws
    -> PlaybackProgress
  {
    try await request(
      "titles/\(titleId.uuidString.lowercased())/watched",
      method: "POST",
      body: CompletionRequest(expectedVersion: expectedVersion),
      authenticated: true
    )
  }

  public func markTitleUnwatched(titleId: UUID, expectedVersion: Int64) async throws
    -> PlaybackProgress
  {
    try await request(
      "titles/\(titleId.uuidString.lowercased())/watched",
      method: "DELETE",
      query: queryItems(("expectedVersion", String(expectedVersion))),
      authenticated: true
    )
  }

  public func continueWatching(limit: Int? = nil) async throws -> ContinueWatchingPage {
    try await request(
      "continue-watching",
      query: queryItems(("limit", limit.map(String.init))),
      authenticated: true
    )
  }

  public func dismissContinueWatchingTitle(titleId: UUID) async throws {
    _ = try await requestData(
      "continue-watching/\(titleId.uuidString.lowercased())",
      method: "DELETE",
      body: nil,
      authenticated: true
    )
  }

  public func collections() async throws -> [Collection] {
    let result: CollectionList = try await request("collections", authenticated: true)
    return result.collections
  }

  public func collection(id: UUID) async throws -> Collection {
    try await request("collections/\(id.uuidString.lowercased())", authenticated: true)
  }

  public func resolveCollectionFolder(
    collectionId: UUID,
    folderId: UUID,
    page: Int? = nil,
    limit: Int? = nil,
    language: String? = nil,
    region: String? = nil
  ) async throws -> ResolvedCollectionFolder {
    try await request(
      "collections/\(collectionId.uuidString.lowercased())/folders/\(folderId.uuidString.lowercased())/items",
      query: queryItems(
        ("page", page.map(String.init)), ("limit", limit.map(String.init)), ("language", language),
        ("region", region)),
      authenticated: true
    )
  }

  public func addonCatalogs() async throws -> [AddonCatalogDescriptor] {
    let result: AddonCatalogDescriptorList = try await request(
      "addons/catalogs", authenticated: true)
    return result.catalogs
  }

  public func semanticSearch(_ input: SemanticSearchRequest) async throws -> SemanticSearchPage {
    try await request("search/semantic", method: "POST", body: input, authenticated: true)
  }

  public func searchAddonCatalogs(
    type: String,
    search: String,
    skip: Int? = nil,
    limit: Int? = nil,
    extras: [AddonExtraValue] = []
  ) async throws -> AddonResourceBatch {
    try await request(
      "addons/catalogs/search/\(pathComponent(type))",
      query: queryItems(
        ("search", search), ("skip", skip.map(String.init)), ("limit", limit.map(String.init)))
        + extraQueryItems(extras),
      authenticated: true
    )
  }

  public func addonResource(
    addonId: UUID,
    resource: String,
    type: String,
    id: String,
    skip: Int? = nil,
    limit: Int? = nil,
    extras: [AddonExtraValue] = []
  ) async throws -> AddonResourceResult {
    try await request(
      "addons/\(addonId.uuidString.lowercased())/resource/\(pathComponent(resource))/\(pathComponent(type))/\(pathComponent(id))",
      query: queryItems(("skip", skip.map(String.init)), ("limit", limit.map(String.init)))
        + extraQueryItems(extras),
      authenticated: true
    )
  }

  public func addonResources(
    resource: String,
    type: String,
    id: String,
    extras: [AddonExtraValue] = []
  ) async throws -> AddonResourceBatch {
    try await request(
      "addons/resources/\(pathComponent(resource))/\(pathComponent(type))/\(pathComponent(id))",
      query: extraQueryItems(extras),
      authenticated: true
    )
  }

  public func resolveTitle(_ input: TitleResolveInput) async throws -> TitleReference {
    try await request("titles/resolve", method: "POST", body: input, authenticated: true)
  }

  public func resolveCustomSeries(_ input: CustomSeriesResolveInput) async throws
    -> CustomSeriesResolveResult
  {
    try await request(
      "titles/custom-series/resolve", method: "POST", body: input, authenticated: true)
  }

  public func library(mediaType: TitleMediaType? = nil, page: Int? = nil, pageSize: Int? = nil)
    async throws -> LibraryPage
  {
    try await request(
      "library",
      query: queryItems(
        ("mediaType", mediaType?.rawValue), ("page", page.map(String.init)),
        ("pageSize", pageSize.map(String.init))),
      authenticated: true
    )
  }

  public func calendar(from: String, to: String, language: String? = nil) async throws
    -> [CalendarEvent]
  {
    let result: CalendarEventList = try await request(
      "calendar",
      query: queryItems(("from", from), ("to", to), ("language", language)),
      authenticated: true
    )
    return result.events
  }

  public func tvLibraryMembership(_ identities: [TVLibraryIdentity]) async throws
    -> TVLibraryMembershipResult
  {
    try await request(
      "library/membership", method: "POST",
      body: TVLibraryMembershipRequest(identities: identities), authenticated: true)
  }

  public func addLibraryTitle(id: UUID) async throws -> LibraryItem {
    try await request("library/\(id.uuidString.lowercased())", method: "PUT", authenticated: true)
  }

  public func removeLibraryTitle(id: UUID) async throws {
    _ = try await requestData(
      "library/\(id.uuidString.lowercased())", method: "DELETE", body: nil, authenticated: true)
  }

  public func sessionNotifications(after: String? = nil) async throws -> [SessionNotification] {
    let result: SessionNotificationList = try await request(
      "auth/notifications", query: queryItems(("after", after)), authenticated: true)
    return result.notifications
  }

  public func acknowledgeSessionNotification(id: String) async throws {
    _ = try await requestData(
      "auth/notifications/\(pathComponent(id))", method: "DELETE", body: nil, authenticated: true)
  }

  public func readingQueue(profileId: UUID) async throws -> ReadingQueue {
    try await request("profiles/\(profileId.uuidString.lowercased())/queue", authenticated: true)
  }

  public func addReadingQueueItem(profileId: UUID, input: ReadingQueueAddInput) async throws
    -> ReadingQueueMutation
  {
    try await request(
      "profiles/\(profileId.uuidString.lowercased())/queue/items", method: "POST", body: input,
      authenticated: true)
  }

  public func reorderReadingQueue(profileId: UUID, input: ReadingQueueReorderInput) async throws
    -> ReadingQueueMutation
  {
    try await request(
      "profiles/\(profileId.uuidString.lowercased())/queue/order", method: "PUT", body: input,
      authenticated: true)
  }

  public func updateReadingQueueItem(
    profileId: UUID, itemId: UUID, input: ReadingQueueUpdateInput
  ) async throws -> ReadingQueueMutation {
    try await request(
      "profiles/\(profileId.uuidString.lowercased())/queue/items/\(itemId.uuidString.lowercased())",
      method: "PATCH", body: input, authenticated: true)
  }

  public func removeReadingQueueItem(
    profileId: UUID, itemId: UUID, input: ReadingQueueMutationInput
  ) async throws -> ReadingQueueMutation {
    try await request(
      "profiles/\(profileId.uuidString.lowercased())/queue/items/\(itemId.uuidString.lowercased())",
      method: "DELETE", body: input, authenticated: true)
  }

  public func consumeReadingQueueItem(
    profileId: UUID, itemId: UUID, input: ReadingQueueMutationInput
  ) async throws -> ReadingQueueMutation {
    try await request(
      "profiles/\(profileId.uuidString.lowercased())/queue/items/\(itemId.uuidString.lowercased())/consume",
      method: "POST", body: input, authenticated: true)
  }

  public func savedSearches() async throws -> [SavedSearch] {
    let result: SavedSearchList = try await request("saved-searches", authenticated: true)
    return result.savedSearches
  }

  public func createSavedSearch(_ input: SavedSearchInput) async throws -> SavedSearch {
    try await request("saved-searches", method: "POST", body: input, authenticated: true)
  }

  public func updateSavedSearch(id: UUID, input: SavedSearchUpdateInput) async throws -> SavedSearch {
    try await request(
      "saved-searches/\(id.uuidString.lowercased())", method: "PUT", body: input,
      authenticated: true)
  }

  public func deleteSavedSearch(id: UUID, expectedRevision: Int64) async throws {
    _ = try await requestData(
      "saved-searches/\(id.uuidString.lowercased())", method: "DELETE",
      query: queryItems(("expectedRevision", String(expectedRevision))), body: nil,
      authenticated: true)
  }

  public func smartCollections() async throws -> [SmartCollection] {
    let result: SmartCollectionList = try await request("smart-collections", authenticated: true)
    return result.smartCollections
  }

  public func createSmartCollection(_ input: SmartCollectionInput) async throws -> SmartCollection {
    try await request("smart-collections", method: "POST", body: input, authenticated: true)
  }

  public func updateSmartCollection(id: UUID, input: SmartCollectionUpdateInput) async throws
    -> SmartCollection
  {
    try await request(
      "smart-collections/\(id.uuidString.lowercased())", method: "PUT", body: input,
      authenticated: true)
  }

  public func deleteSmartCollection(id: UUID, expectedRevision: Int64) async throws {
    _ = try await requestData(
      "smart-collections/\(id.uuidString.lowercased())", method: "DELETE",
      query: queryItems(("expectedRevision", String(expectedRevision))), body: nil,
      authenticated: true)
  }

  public func evaluateSmartCollection(id: UUID, page: Int = 1, pageSize: Int = 24) async throws
    -> SmartCollectionPage
  {
    try await request(
      "smart-collections/\(id.uuidString.lowercased())/items",
      query: queryItems(("page", String(page)), ("pageSize", String(pageSize))), authenticated: true)
  }

  public func extensionIncidents() async throws -> [AddonIncident] {
    let result: AddonIncidentList = try await request(
      "operations/extension-incidents", authenticated: true)
    return result.incidents
  }

  public func extensionIncident(id: UUID) async throws -> AddonIncidentDetail {
    try await request(
      "operations/extension-incidents/\(id.uuidString.lowercased())", authenticated: true)
  }

  public func acknowledgeExtensionIncident(id: UUID) async throws -> AddonIncident {
    try await request(
      "operations/extension-incidents/\(id.uuidString.lowercased())/acknowledgement",
      method: "POST", authenticated: true)
  }

  public func mediaNotificationSubscriptions() async throws -> [MediaNotificationSubscription] {
    let result: MediaNotificationSubscriptions = try await request(
      "media-notification-subscriptions", authenticated: true)
    return result.subscriptions
  }

  public func followMediaNotifications(titleId: UUID, input: MediaNotificationFollowInput)
    async throws -> MediaNotificationSubscription
  {
    try await request(
      "media-notification-subscriptions/\(titleId.uuidString.lowercased())", method: "PUT",
      body: input, authenticated: true)
  }

  public func unfollowMediaNotifications(titleId: UUID) async throws {
    _ = try await requestData(
      "media-notification-subscriptions/\(titleId.uuidString.lowercased())", method: "DELETE",
      body: nil, authenticated: true)
  }

  public func mediaNotifications(cursor: String? = nil, limit: Int = 30) async throws
    -> MediaNotificationPage
  {
    try await request(
      "media-notifications", query: queryItems(("cursor", cursor), ("limit", String(limit))),
      authenticated: true)
  }

  public func acknowledgeMediaNotification(
    id: String, state: MediaNotificationAcknowledgementState
  ) async throws {
    let data = try encoder.encode(MediaNotificationAcknowledgement(state: state))
    _ = try await requestData(
      "media-notifications/\(pathComponent(id))/acknowledgement", method: "POST", body: data,
      authenticated: true)
  }

  public func profileAccessibilityPreferences(profileId: UUID) async throws
    -> AccessibilityPreferencesDocument
  {
    try await request(
      "profiles/\(profileId.uuidString.lowercased())/accessibility-preferences",
      authenticated: true)
  }

  public func updateProfileAccessibilityPreferences(
    profileId: UUID, document: AccessibilityPreferencesDocument
  ) async throws -> AccessibilityPreferencesDocument {
    try await request(
      "profiles/\(profileId.uuidString.lowercased())/accessibility-preferences", method: "PUT",
      body: document, authenticated: true)
  }

  public func createPlaybackFailover(_ input: PlaybackFailoverCreateInput) async throws
    -> PlaybackFailoverState
  {
    try await request("playback/failovers", method: "POST", body: input, authenticated: true)
  }

  public func playbackFailover(id: UUID) async throws -> PlaybackFailoverState {
    try await request("playback/failovers/\(id.uuidString.lowercased())", authenticated: true)
  }

  public func cancelPlaybackFailover(id: UUID) async throws {
    _ = try await requestData(
      "playback/failovers/\(id.uuidString.lowercased())", method: "DELETE", body: nil,
      authenticated: true)
  }

  public func advancePlaybackFailover(id: UUID, input: PlaybackFailoverAdvanceInput) async throws
    -> PlaybackFailoverState
  {
    try await request(
      "playback/failovers/\(id.uuidString.lowercased())/advance", method: "POST", body: input,
      authenticated: true)
  }

  public func resolveResponseResourceURL(_ value: String) throws -> URL {
    guard let components = URLComponents(string: value), components.user == nil,
      components.password == nil,
      let resolved = URL(string: value, relativeTo: serverURL)?.absoluteURL
    else {
      throw RivuneAPIError.invalidServerURL(value)
    }
    let origin = try Self.canonicalServerOrigin(resolved)
    if components.scheme == nil {
      guard components.host == nil, origin == serverURL else {
        throw RivuneAPIError.invalidServerURL(value)
      }
    } else if origin != serverURL && resolved.scheme?.lowercased() != "https" {
      throw RivuneAPIError.invalidServerURL(value)
    }
    return resolved
  }

  private func request<Response: Decodable>(
    _ path: String, method: String = "GET", query: [URLQueryItem] = [], authenticated: Bool
  ) async throws -> Response {
    try await decodedRequest(
      path, method: method, query: query, body: nil, authenticated: authenticated)
  }

  private func request<Response: Decodable, Body: Encodable>(
    _ path: String, method: String = "GET", query: [URLQueryItem] = [], body: Body,
    authenticated: Bool, allowProfileSelectionMutation: Bool = false
  ) async throws -> Response {
    let data = try encoder.encode(body)
    return try await decodedRequest(
      path, method: method, query: query, body: data, authenticated: authenticated,
      allowProfileSelectionMutation: allowProfileSelectionMutation)
  }

  private func decodedRequest<Response: Decodable>(
    _ path: String, method: String, query: [URLQueryItem], body: Data?, authenticated: Bool,
    allowProfileSelectionMutation: Bool = false
  ) async throws -> Response {
    let data = try await requestData(
      path, method: method, query: query, body: body, authenticated: authenticated,
      allowProfileSelectionMutation: allowProfileSelectionMutation)
    do { return try decoder.decode(Response.self, from: data) } catch {
      throw RivuneAPIError.invalidResponse
    }
  }

  private func requestData(
    _ path: String, method: String, query: [URLQueryItem] = [], body: Data?, authenticated: Bool,
    allowProfileSelectionMutation: Bool = false
  ) async throws -> Data {
    try await requestResult(
      path, method: method, query: query, body: body, authenticated: authenticated,
      allowProfileSelectionMutation: allowProfileSelectionMutation
    ).data
  }

  private func requestResult(
    _ path: String, method: String, query: [URLQueryItem] = [], body: Data?, authenticated: Bool,
    allowProfileSelectionMutation: Bool = false
  ) async throws -> HTTPResult {
    if authenticated, profileSelectionMutationInFlight, !allowProfileSelectionMutation {
      throw CancellationError()
    }
    if apiBaseURL == nil { _ = try await discover() }
    if authenticated { try await loadCredentialsIfNeeded() }
    if authenticated, profileSelectionMutationInFlight, !allowProfileSelectionMutation {
      throw CancellationError()
    }
    let authorizationToken: String?
    if authenticated {
      guard let accessToken = credentials?.accessToken else {
        throw RivuneAPIError.notAuthenticated
      }
      authorizationToken = accessToken
    } else {
      authorizationToken = nil
    }
    let requestGeneration = authenticated ? authenticationGeneration : nil
    let requestProfileGeneration = authenticated ? profileSelectionGeneration : nil
    let url = try resolvedAPIURL(path: path, query: query)
    return try await perform(
      url: url,
      method: method,
      body: body,
      authorizationToken: authorizationToken,
      retryAfterRefresh: authenticated,
      expectedAuthenticationGeneration: requestGeneration,
      expectedProfileSelectionGeneration: requestProfileGeneration
    )
  }

  private func requestData(
    _ path: String,
    method: String,
    query: [URLQueryItem] = [],
    body: Data?,
    authorizationToken: String,
    retryAfterRefresh: Bool
  ) async throws -> Data {
    if apiBaseURL == nil { _ = try await discover() }
    let url = try resolvedAPIURL(path: path, query: query)
    return try await perform(
      url: url,
      method: method,
      body: body,
      authorizationToken: authorizationToken,
      retryAfterRefresh: retryAfterRefresh,
      expectedAuthenticationGeneration: nil,
      expectedProfileSelectionGeneration: nil
    ).data
  }

  private func resolvedAPIURL(path: String, query: [URLQueryItem]) throws -> URL {
    guard let base = apiBaseURL,
      var components = URLComponents(url: base, resolvingAgainstBaseURL: false)
    else {
      throw RivuneAPIError.invalidResponse
    }
    var encodedPath = components.percentEncodedPath
    if !encodedPath.hasSuffix("/") { encodedPath += "/" }
    encodedPath += path.split(separator: "/").joined(separator: "/")
    components.percentEncodedPath = encodedPath
    if !query.isEmpty { components.queryItems = query }
    guard let url = components.url else { throw RivuneAPIError.invalidServerURL(path) }
    return url
  }

  private func perform<Response: Decodable>(
    url: URL, method: String, body: Data?, authenticated: Bool, retryAfterRefresh: Bool
  ) async throws -> Response {
    if authenticated { try await loadCredentialsIfNeeded() }
    let authorizationToken: String?
    if authenticated {
      guard let accessToken = credentials?.accessToken else {
        throw RivuneAPIError.notAuthenticated
      }
      authorizationToken = accessToken
    } else {
      authorizationToken = nil
    }
    let requestGeneration = authenticated ? authenticationGeneration : nil
    let requestProfileGeneration = authenticated ? profileSelectionGeneration : nil
    let result = try await perform(
      url: url,
      method: method,
      body: body,
      authorizationToken: authorizationToken,
      retryAfterRefresh: retryAfterRefresh,
      expectedAuthenticationGeneration: requestGeneration,
      expectedProfileSelectionGeneration: requestProfileGeneration
    )
    do { return try decoder.decode(Response.self, from: result.data) } catch {
      throw RivuneAPIError.invalidResponse
    }
  }

  private func perform(
    url: URL,
    method: String,
    body: Data?,
    authorizationToken: String?,
    retryAfterRefresh: Bool,
    expectedAuthenticationGeneration: UInt64?,
    expectedProfileSelectionGeneration: UInt64?,
    profileMutationCancellation: ProfileMutationCancellation? = nil
  ) async throws -> HTTPResult {
    guard try Self.canonicalServerOrigin(url) == serverURL else {
      throw RivuneAPIError.invalidServerURL(url.absoluteString)
    }
    var request = URLRequest(url: url)
    request.httpMethod = method
    request.httpBody = body
    request.setValue("application/json", forHTTPHeaderField: "Accept")
    if body != nil { request.setValue("application/json", forHTTPHeaderField: "Content-Type") }
    if let authorizationToken {
      request.setValue("Bearer \(authorizationToken)", forHTTPHeaderField: "Authorization")
    }
    if authorizationToken != nil, let profileContext, Self.usesProfileContext(url, method: method) {
      request.setValue(profileContext, forHTTPHeaderField: "X-Rivune-Profile-Context")
    }

    let transportRequest = request
    let result: (Data, HTTPURLResponse)
    do {
      if expectedAuthenticationGeneration != nil {
        result = try await runAuthenticationOperation {
          try profileMutationCancellation?.check()
          return try await self.transport.data(for: transportRequest)
        }
      } else {
        try profileMutationCancellation?.check()
        result = try await transport.data(for: request)
      }
    } catch {
      try ensureRequestGenerations(
        authentication: expectedAuthenticationGeneration,
        profile: expectedProfileSelectionGeneration)
      throw error
    }
    try ensureRequestGenerations(
      authentication: expectedAuthenticationGeneration, profile: expectedProfileSelectionGeneration)
    let (data, response) = result
    try Self.enforceResponseLimit(data: data, response: response)
    if response.statusCode == 401, let authorizationToken, retryAfterRefresh {
      _ = try await refreshCredentials(failedAccessToken: authorizationToken)
      try ensureRequestGenerations(
        authentication: expectedAuthenticationGeneration,
        profile: expectedProfileSelectionGeneration)
      guard let refreshedAccessToken = credentials?.accessToken else {
        throw RivuneAPIError.notAuthenticated
      }
      return try await perform(
        url: url,
        method: method,
        body: body,
        authorizationToken: refreshedAccessToken,
        retryAfterRefresh: false,
        expectedAuthenticationGeneration: expectedAuthenticationGeneration,
        expectedProfileSelectionGeneration: expectedProfileSelectionGeneration,
        profileMutationCancellation: profileMutationCancellation
      )
    }
    guard (200..<300).contains(response.statusCode) else {
      throw decodeServerError(response: response, data: data)
    }
    return HTTPResult(data: data, response: response)
  }

  private func ensureRequestGenerations(authentication: UInt64?, profile: UInt64?) throws {
    if let authentication, authentication != authenticationGeneration { throw CancellationError() }
    if let profile, profile != profileSelectionGeneration { throw CancellationError() }
  }

  private static func usesProfileContext(_ url: URL, method: String) -> Bool {
    let path = url.path
    if path.hasSuffix("/auth/logout") || path.hasSuffix("/auth/me") { return false }
    if method == "DELETE", path.hasSuffix("/profiles/selection") { return false }
    if method == "GET", path.hasSuffix("/profiles") { return false }
    if method == "GET", path.contains("/profiles/"), path.hasSuffix("/avatar") { return false }
    if method == "POST", path.contains("/profiles/"), path.hasSuffix("/select") { return false }
    return true
  }

  private func refreshCredentials(failedAccessToken: String? = nil) async throws -> TokenPair {
    if let failedAccessToken, let current = credentials, current.accessToken != failedAccessToken {
      return current
    }
    let generation = authenticationGeneration
    if let refreshOperation, refreshOperation.generation == generation {
      return try await refreshOperation.task.value
    }
    guard let refreshToken = credentials?.refreshToken else {
      throw RivuneAPIError.notAuthenticated
    }
    let task = Task<TokenPair, Error> {
      try await self.issueRefresh(refreshToken: refreshToken, generation: generation)
    }
    refreshOperation = RefreshOperation(
      generation: generation,
      refreshToken: refreshToken,
      task: task
    )
    do {
      let tokens = try await task.value
      if refreshOperation?.generation == generation,
        refreshOperation?.refreshToken == refreshToken
      {
        refreshOperation = nil
      }
      return tokens
    } catch {
      if refreshOperation?.generation == generation,
        refreshOperation?.refreshToken == refreshToken
      {
        refreshOperation = nil
      }
      if case RivuneAPIError.server(let status, let code, _, _) = error,
        status == 401,
        code == "invalid_refresh_token",
        generation == authenticationGeneration,
        credentials?.refreshToken == refreshToken
      {
        credentials = nil
        loadedCredentials = true
        _ = try? await credentialStore.clear(for: serverURL, generation: generation)
      }
      throw error
    }
  }

  private func issueRefresh(refreshToken: String, generation: UInt64) async throws -> TokenPair {
    if apiBaseURL == nil { _ = try await discover() }
    guard var url = apiBaseURL else { throw RivuneAPIError.invalidResponse }
    url.appendPathComponent("auth")
    url.appendPathComponent("refresh")
    let data = try encoder.encode(RefreshRequest(refreshToken: refreshToken))
    let tokens: TokenPair = try await perform(
      url: url,
      method: "POST",
      body: data,
      authenticated: false,
      retryAfterRefresh: false
    )
    try await setCredentials(tokens, generation: generation)
    return tokens
  }

  private func setCredentials(_ value: TokenPair, generation: UInt64) async throws {
    guard generation == authenticationGeneration else { throw CancellationError() }
    let persistedProfileContext: String?
    if let pending = pendingProfileContextPersistence,
      pending.authenticationGeneration == generation
    {
      persistedProfileContext = pending.value
    } else {
      persistedProfileContext = profileContext
    }
    let stored = StoredCredentials(tokens: value, profileContext: persistedProfileContext)
    let saved = try await credentialStore.save(stored, for: serverURL, generation: generation)
    guard saved, generation == authenticationGeneration else { throw CancellationError() }
    credentials = value
    loadedCredentials = true
  }

  private func beginCredentialReplacement(preserveStoredCredentials: Bool) async throws -> UInt64 {
    authenticationGeneration += 1
    let generation = authenticationGeneration
    let capturedCredentials = credentials.map {
      StoredCredentials(tokens: $0, profileContext: profileContext)
    }
    credentials = nil
    profileContext = nil
    loadedCredentials = !preserveStoredCredentials
    refreshOperation?.task.cancel()
    refreshOperation = nil
    profileSelectionGeneration += 1
    for cancel in pendingAuthenticationCancellations.values {
      cancel()
    }
    pendingAuthenticationCancellations.removeAll()

    if preserveStoredCredentials {
      await credentialStore.advance(to: generation)
    } else {
      let cleanup = await credentialStore.invalidateAndClear(
        for: serverURL,
        generation: generation,
        capturedCredentials: capturedCredentials
      )
      guard generation == authenticationGeneration else { throw CancellationError() }
      if let error = cleanup.error { throw error }
    }
    return generation
  }

  private func runAuthenticationOperation<Value: Sendable>(
    _ operation: @escaping @Sendable () async throws -> Value
  ) async throws -> Value {
    let id = UUID()
    let task = Task { try await operation() }
    pendingAuthenticationCancellations[id] = { task.cancel() }
    defer { pendingAuthenticationCancellations.removeValue(forKey: id) }
    return try await withTaskCancellationHandler {
      try await task.value
    } onCancel: {
      task.cancel()
    }
  }

  private func installRestoredCredentials(_ restored: StoredCredentials?) {
    credentials = restored?.tokens
    profileContext = restored?.profileContext
    loadedCredentials = true
  }

  private func loadCredentialsIfNeeded() async throws {
    guard !loadedCredentials else { return }
    let generation = authenticationGeneration
    let restored = try await credentialStore.load(for: serverURL, generation: generation)
    guard generation == authenticationGeneration else { throw CancellationError() }
    credentials = restored?.tokens
    profileContext = restored?.profileContext
    loadedCredentials = true
  }

  private static func enforceResponseLimit(data: Data, response: HTTPURLResponse) throws {
    let maximumBytes = URLSessionTransport.maximumResponseBodyBytes
    if response.expectedContentLength > Int64(maximumBytes) || data.count > maximumBytes {
      throw RivuneAPIError.responseTooLarge(maximumBytes: maximumBytes)
    }
  }
  public static func canonicalServerOrigin(_ value: URL) throws -> URL {
    guard let components = URLComponents(url: value, resolvingAgainstBaseURL: false),
      let rawScheme = components.scheme,
      let rawHost = components.host,
      components.user == nil,
      components.password == nil
    else {
      throw RivuneAPIError.invalidServerURL(value.absoluteString)
    }

    let scheme = rawScheme.lowercased()
    let host = rawHost.lowercased()
    guard scheme == "https" || (scheme == "http" && isLocalNetworkHost(host)) else {
      throw RivuneAPIError.invalidServerURL(value.absoluteString)
    }

    var canonical = URLComponents()
    canonical.scheme = scheme
    canonical.host = host
    if let port = components.port,
      !((scheme == "https" && port == 443) || (scheme == "http" && port == 80))
    {
      canonical.port = port
    }
    guard let origin = canonical.url else {
      throw RivuneAPIError.invalidServerURL(value.absoluteString)
    }
    return origin
  }

  private static func isLocalNetworkHost(_ rawHost: String) -> Bool {
    let host =
      rawHost
      .trimmingCharacters(in: CharacterSet(charactersIn: "[]"))
      .lowercased()
    guard !host.isEmpty else { return false }
    if host == "localhost" || host == "::1" { return true }
    if host.contains(":") {
      guard let firstGroup = host.split(separator: ":", omittingEmptySubsequences: true).first,
        let prefix = UInt16(firstGroup, radix: 16)
      else { return false }
      return prefix & 0xfe00 == 0xfc00
    }
    let octets = host.split(separator: ".", omittingEmptySubsequences: false).compactMap { Int($0) }
    guard octets.count == 4, octets.allSatisfy({ 0...255 ~= $0 }) else { return false }
    return octets[0] == 10 || octets[0] == 127 || (octets[0] == 172 && 16...31 ~= octets[1])
      || (octets[0] == 192 && octets[1] == 168)
  }

  private func decodeServerError(response: HTTPURLResponse, data: Data) -> RivuneAPIError {
    let retryAfterSeconds = response.value(forHTTPHeaderField: "Retry-After")
      .flatMap { Int($0.trimmingCharacters(in: .whitespacesAndNewlines)) }
      .flatMap { $0 > 0 ? $0 : nil }
    if let envelope = try? decoder.decode(ErrorEnvelope.self, from: data) {
      return .server(
        status: response.statusCode,
        code: envelope.error.code,
        message: envelope.error.message,
        retryAfterSeconds: retryAfterSeconds
      )
    }
    return .server(
      status: response.statusCode,
      code: "http_\(response.statusCode)",
      message: HTTPURLResponse.localizedString(forStatusCode: response.statusCode),
      retryAfterSeconds: retryAfterSeconds
    )
  }

  private func pathComponent(_ value: String) -> String {
    value.addingPercentEncoding(
      withAllowedCharacters: .urlPathAllowed.subtracting(CharacterSet(charactersIn: "/"))) ?? value
  }

  private func queryItems(_ values: (String, String?)...) -> [URLQueryItem] {
    values.compactMap { name, value in value.map { URLQueryItem(name: name, value: $0) } }
  }

  private func extraQueryItems(_ values: [AddonExtraValue]) -> [URLQueryItem] {
    values.map { URLQueryItem(name: $0.name, value: $0.value) }
  }
}
