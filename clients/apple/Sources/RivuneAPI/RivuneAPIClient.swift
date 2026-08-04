import Foundation
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif

public protocol HTTPTransport: Sendable {
    func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse)
}

public struct URLSessionTransport: HTTPTransport {
    private let session: URLSession

    public init(session: URLSession = .shared) {
        self.session = session
    }

    public func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        let (data, response) = try await session.data(for: request)
        guard let response = response as? HTTPURLResponse else {
            throw RivuneAPIError.invalidResponse
        }
        return (data, response)
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
    let timezone: String
    let interfaceLanguage: String?
}

public enum RivuneAPIError: Error, LocalizedError, Sendable {
    case incompatibleProtocol(expected: Int, actual: Int)
    case invalidServerURL(String)
    case invalidResponse
    case notAuthenticated
    case server(status: Int, code: String, message: String)

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
        case .server(_, _, let message):
            return message
        }
    }
}

private struct RefreshRequest: Encodable { let refreshToken: String }
private struct SelectProfileRequest: Encodable { let pin: String? }
private struct PlaybackSourcesRequest: Encodable {
    let mediaType: String
    let resourceId: String
    let capabilities: PlaybackCapabilities
}
private struct PlaybackPrepareRequest: Encodable {
    let sourceRef: String
    let startSeconds: Int?
}
private struct PlaybackResolveRequest: Encodable {
    let sourceRef: String
    let titleId: String?
    let preferredAudioTrack: Int?
    let preferredSubtitleId: String?
    let startSeconds: Int?
}

public actor RivuneAPIClient {
    private let serverURL: URL
    private let transport: any HTTPTransport
    private let credentialStore: any CredentialStore
    private let encoder: JSONEncoder
    private let decoder: JSONDecoder
    private var apiBaseURL: URL?
    private var credentials: TokenPair?
    private var loadedCredentials = false
    private var refreshTask: Task<TokenPair, Error>?

    public init(
        serverURL: URL,
        transport: any HTTPTransport = URLSessionTransport(),
        credentialStore: any CredentialStore
    ) {
        self.serverURL = serverURL
        self.transport = transport
        self.credentialStore = credentialStore
        self.encoder = JSONEncoder()
        self.decoder = JSONDecoder()
    }

#if canImport(Security)
    public init(serverURL: URL, transport: any HTTPTransport = URLSessionTransport()) {
        self.init(serverURL: serverURL, transport: transport, credentialStore: KeychainCredentialStore())
    }
#endif

    @discardableResult
    public func discover() async throws -> Discovery {
        guard let url = URL(string: "/.well-known/rivune", relativeTo: serverURL)?.absoluteURL else {
            throw RivuneAPIError.invalidServerURL(serverURL.absoluteString)
        }
        let response: DiscoveryEnvelope = try await perform(url: url, method: "GET", body: Optional<Data>.none, authenticated: false, retryAfterRefresh: false)
        guard response.protocolVersion == RivuneProtocol.version else {
            throw RivuneAPIError.incompatibleProtocol(expected: RivuneProtocol.version, actual: response.protocolVersion)
        }
        guard let interfaceLanguage = response.interfaceLanguage else {
            throw RivuneAPIError.invalidResponse
        }
        let discovery = Discovery(name: response.name, serverVersion: response.serverVersion, protocolVersion: response.protocolVersion, apiBaseUrl: response.apiBaseUrl, setupRequired: response.setupRequired, timezone: response.timezone, interfaceLanguage: interfaceLanguage)
        guard let resolved = URL(string: discovery.apiBaseUrl, relativeTo: serverURL)?.absoluteURL,
              let scheme = resolved.scheme, scheme == "https" || scheme == "http" else {
            throw RivuneAPIError.invalidServerURL(discovery.apiBaseUrl)
        }
        apiBaseURL = resolved
        return discovery
    }

    @discardableResult
    public func restoreSession() async throws -> Bool {
        credentials = try await credentialStore.load()
        loadedCredentials = true
        return credentials != nil
    }

    @discardableResult
    public func login(username: String, password: String, device: LoginDevice) async throws -> TokenPair {
        let payload = LoginRequest(username: username, password: password, device: device)
        let tokens: TokenPair = try await request("auth/login", method: "POST", body: payload, authenticated: false)
        try await setCredentials(tokens)
        return tokens
    }

    @discardableResult
    public func refreshSession() async throws -> TokenPair {
        try await loadCredentialsIfNeeded()
        return try await refreshCredentials()
    }

    public func logout() async throws {
        try await loadCredentialsIfNeeded()
        if credentials != nil {
            _ = try await requestData("auth/logout", method: "POST", body: Optional<Data>.none, authenticated: true)
        }
        credentials = nil
        try await credentialStore.clear()
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
        try await request("categories/\(id.uuidString.lowercased())", method: "PATCH", body: input, authenticated: true)
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
        try await request("devices/\(id.uuidString.lowercased())", method: "PATCH", body: input, authenticated: true)
    }

    public func moveProfiles(_ profileIds: [UUID], to categoryId: UUID) async throws {
        _ = try await requestData(
            "profiles/category-moves",
            method: "POST",
            body: try encoder.encode(ProfileCategoryMoveRequest(profileIds: profileIds, categoryId: categoryId)),
            authenticated: true
        )
    }

    public func moveDevices(_ deviceIds: [UUID], to categoryId: UUID) async throws {
        _ = try await requestData(
            "devices/category-moves",
            method: "POST",
            body: try encoder.encode(DeviceCategoryMoveRequest(deviceIds: deviceIds, categoryId: categoryId)),
            authenticated: true
        )
    }

    public func beginDeviceAuthorization(deviceName: String, platform: String) async throws -> DeviceAuthorizationResponse {
        try await request(
            "auth/device-code",
            method: "POST",
            body: DeviceAuthorizationRequest(deviceName: deviceName, platform: platform),
            authenticated: false
        )
    }

    @discardableResult
    public func exchangeDeviceAuthorization(deviceCode: String) async throws -> TokenPair {
        let tokens: TokenPair = try await request(
            "auth/device-code/token",
            method: "POST",
            body: DeviceCodeTokenRequest(deviceCode: deviceCode),
            authenticated: false
        )
        try await setCredentials(tokens)
        return tokens
    }

    public func approveDeviceAuthorization(_ input: DeviceCodeApprovalRequest) async throws {
        _ = try await requestData("auth/device-code/approve", method: "POST", body: try encoder.encode(input), authenticated: true)
    }

    public func profiles() async throws -> [Profile] {
        let result: ProfileList = try await request("profiles", authenticated: true)
        return result.profiles
    }

    public func selectProfile(id: UUID, pin: String? = nil) async throws -> ProfileSelection {
        try await request("profiles/\(id.uuidString.lowercased())/select", method: "POST", body: SelectProfileRequest(pin: pin), authenticated: true)
    }

    public func clearProfileSelection() async throws {
        _ = try await requestData("profiles/selection", method: "DELETE", body: Optional<Data>.none, authenticated: true)
    }

    public func instanceSettings() async throws -> SettingsLayer {
        try await request("settings", authenticated: true)
    }

    public func updateInstanceSettings(_ patch: InstanceTranscodingPatch) async throws -> SettingsLayer {
        try await request("settings", method: "PATCH", body: patch, authenticated: true)
    }

    public func profileSettings(id: UUID) async throws -> SettingsLayer {
        try await request("profiles/\(id.uuidString.lowercased())/settings", authenticated: true)
    }

    public func updateProfileSettings(id: UUID, patch: ProfileTranscodingPatch) async throws -> SettingsLayer {
        try await request("profiles/\(id.uuidString.lowercased())/settings", method: "PATCH", body: patch, authenticated: true)
    }

    public func effectiveProfileSettings(id: UUID) async throws -> EffectiveSettings {
        try await request("profiles/\(id.uuidString.lowercased())/settings/effective", authenticated: true)
    }

    public func movie(id: UUID, language: String? = nil) async throws -> Movie {
        try await request("metadata/titles/\(id.uuidString.lowercased())", query: queryItems(("language", language)), authenticated: true)
    }

    public func series(id: UUID, language: String? = nil, mappingProvider: SeriesMappingProvider) async throws -> Series {
        try await request("metadata/series/\(id.uuidString.lowercased())", query: queryItems(("language", language), ("mappingProvider", mappingProvider.rawValue)), authenticated: true)
    }

    public func season(id: String, language: String? = nil, mappingProvider: SeriesMappingProvider) async throws -> Season {
        try await request("metadata/seasons/\(pathComponent(id))", query: queryItems(("language", language), ("mappingProvider", mappingProvider.rawValue)), authenticated: true)
    }

    public func trailers(titleId: UUID, language: String? = nil, captionLanguage: String? = nil, seasonNumber: Int? = nil) async throws -> TrailerList {
        try await request(
            "metadata/titles/\(titleId.uuidString.lowercased())/trailers",
            query: queryItems(("language", language), ("captionLanguage", captionLanguage), ("seasonNumber", seasonNumber.map(String.init))),
            authenticated: true
        )
    }

    public func playbackSources(mediaType: String, resourceId: String, capabilities: PlaybackCapabilities) async throws -> PlaybackSourceList {
        try await request("playback/sources", method: "POST", body: PlaybackSourcesRequest(mediaType: mediaType, resourceId: resourceId, capabilities: capabilities), authenticated: true)
    }

    public func preparePlayback(sourceRef: String, startSeconds: Int? = nil) async throws -> PlaybackPreparation {
        try await request("playback/prepare", method: "POST", body: PlaybackPrepareRequest(sourceRef: sourceRef, startSeconds: startSeconds), authenticated: true)
    }

    public func resolvePlayback(
        sourceRef: String,
        titleId: String? = nil,
        preferredAudioTrack: Int? = nil,
        preferredSubtitleId: String? = nil,
        startSeconds: Int? = nil
    ) async throws -> PlaybackSession {
        try await request(
            "playback/resolve",
            method: "POST",
            body: PlaybackResolveRequest(
                sourceRef: sourceRef,
                titleId: titleId,
                preferredAudioTrack: preferredAudioTrack,
                preferredSubtitleId: preferredSubtitleId,
                startSeconds: startSeconds
            ),
            authenticated: true
        )
    }

    public func stopPlayback(sessionId: UUID) async throws {
        _ = try await requestData("playback/sessions/\(sessionId.uuidString.lowercased())", method: "DELETE", body: Optional<Data>.none, authenticated: true)
    }

    public func playbackActivity() async throws -> PlaybackActivity {
        try await request("playback/activity", authenticated: true)
    }

    private func request<Response: Decodable>(_ path: String, method: String = "GET", query: [URLQueryItem] = [], authenticated: Bool) async throws -> Response {
        try await request(path, method: method, query: query, body: Optional<Data>.none, authenticated: authenticated)
    }

    private func request<Response: Decodable, Body: Encodable>(_ path: String, method: String = "GET", query: [URLQueryItem] = [], body: Body, authenticated: Bool) async throws -> Response {
        let data = try encoder.encode(body)
        return try await request(path, method: method, query: query, body: Optional(data), authenticated: authenticated)
    }

    private func request<Response: Decodable>(_ path: String, method: String, query: [URLQueryItem], body: Data?, authenticated: Bool) async throws -> Response {
        let data = try await requestData(path, method: method, query: query, body: body, authenticated: authenticated)
        do { return try decoder.decode(Response.self, from: data) }
        catch { throw RivuneAPIError.invalidResponse }
    }

    private func requestData(_ path: String, method: String, query: [URLQueryItem] = [], body: Data?, authenticated: Bool) async throws -> Data {
        if apiBaseURL == nil { _ = try await discover() }
        if authenticated { try await loadCredentialsIfNeeded() }
        guard let base = apiBaseURL else { throw RivuneAPIError.invalidResponse }
        var url = base
        for component in path.split(separator: "/") { url.appendPathComponent(String(component)) }
        if !query.isEmpty {
            var parts = URLComponents(url: url, resolvingAgainstBaseURL: true)
            parts?.queryItems = query
            guard let composed = parts?.url else { throw RivuneAPIError.invalidServerURL(url.absoluteString) }
            url = composed
        }
        return try await perform(url: url, method: method, body: body, authenticated: authenticated, retryAfterRefresh: authenticated)
    }

    private func perform<Response: Decodable>(url: URL, method: String, body: Data?, authenticated: Bool, retryAfterRefresh: Bool) async throws -> Response {
        let data = try await perform(url: url, method: method, body: body, authenticated: authenticated, retryAfterRefresh: retryAfterRefresh)
        do { return try decoder.decode(Response.self, from: data) }
        catch { throw RivuneAPIError.invalidResponse }
    }

    private func perform(url: URL, method: String, body: Data?, authenticated: Bool, retryAfterRefresh: Bool) async throws -> Data {
        var request = URLRequest(url: url)
        request.httpMethod = method
        request.httpBody = body
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        if body != nil { request.setValue("application/json", forHTTPHeaderField: "Content-Type") }
        let requestAccessToken: String?
        if authenticated {
            guard let token = credentials?.accessToken else { throw RivuneAPIError.notAuthenticated }
            requestAccessToken = token
            request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        } else {
            requestAccessToken = nil
        }

        let (data, response) = try await transport.data(for: request)
        if response.statusCode == 401, authenticated, retryAfterRefresh {
            _ = try await refreshCredentials(failedAccessToken: requestAccessToken)
            return try await perform(url: url, method: method, body: body, authenticated: true, retryAfterRefresh: false)
        }
        guard (200..<300).contains(response.statusCode) else { throw decodeServerError(status: response.statusCode, data: data) }
        return data
    }

    private func refreshCredentials(failedAccessToken: String? = nil) async throws -> TokenPair {
        if let failedAccessToken, let current = credentials, current.accessToken != failedAccessToken {
            return current
        }
        if let refreshTask {
            return try await refreshTask.value
        }
        guard let refreshToken = credentials?.refreshToken else { throw RivuneAPIError.notAuthenticated }
        let task = Task<TokenPair, Error> {
            try await self.issueRefresh(refreshToken: refreshToken)
        }
        refreshTask = task
        do {
            let tokens = try await task.value
            refreshTask = nil
            return tokens
        } catch {
            refreshTask = nil
            credentials = nil
            try? await credentialStore.clear()
            throw error
        }
    }

    private func issueRefresh(refreshToken: String) async throws -> TokenPair {
        if apiBaseURL == nil { _ = try await discover() }
        guard var url = apiBaseURL else { throw RivuneAPIError.invalidResponse }
        url.appendPathComponent("auth")
        url.appendPathComponent("refresh")
        let data = try encoder.encode(RefreshRequest(refreshToken: refreshToken))
        let tokens: TokenPair = try await perform(url: url, method: "POST", body: data, authenticated: false, retryAfterRefresh: false)
        try await setCredentials(tokens)
        return tokens
    }

    private func setCredentials(_ value: TokenPair) async throws {
        try await credentialStore.save(value)
        credentials = value
        loadedCredentials = true
    }

    private func loadCredentialsIfNeeded() async throws {
        guard !loadedCredentials else { return }
        credentials = try await credentialStore.load()
        loadedCredentials = true
    }

    private func decodeServerError(status: Int, data: Data) -> RivuneAPIError {
        if let envelope = try? decoder.decode(ErrorEnvelope.self, from: data) {
            return .server(status: status, code: envelope.error.code, message: envelope.error.message)
        }
        return .server(status: status, code: "http_\(status)", message: HTTPURLResponse.localizedString(forStatusCode: status))
    }

    private func pathComponent(_ value: String) -> String {
        value.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed.subtracting(CharacterSet(charactersIn: "/"))) ?? value
    }

    private func queryItems(_ values: (String, String?)...) -> [URLQueryItem] {
        values.compactMap { name, value in value.map { URLQueryItem(name: name, value: $0) } }
    }
}
