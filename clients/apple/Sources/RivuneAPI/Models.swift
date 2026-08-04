import Foundation

public enum RivuneProtocol {
    public static let version = 18
}

public struct Discovery: Codable, Sendable, Equatable {
    public let name: String
    public let serverVersion: String
    public let protocolVersion: Int
    public let apiBaseUrl: String
    public let setupRequired: Bool
    public let timezone: String
    public let interfaceLanguage: String
}

public enum AuthorizationScope: String, Codable, Sendable, Equatable {
    case globalAdministrator = "global_admin"
    case category
}

public struct CategoryRef: Codable, Sendable, Equatable, Identifiable {
    public let id: UUID
    public let name: String
    public let color: String?
    public let icon: String?

    public init(id: UUID, name: String, color: String?, icon: String?) {
        self.id = id
        self.name = name
        self.color = color
        self.icon = icon
    }

    public init(from decoder: Decoder) throws {
        let values = try decoder.container(keyedBy: CodingKeys.self)
        id = try values.decode(UUID.self, forKey: .id)
        name = try values.decode(String.self, forKey: .name)
        color = try values.decodeRequiredNullable(String.self, forKey: .color)
        icon = try values.decodeRequiredNullable(String.self, forKey: .icon)
    }

    private enum CodingKeys: String, CodingKey {
        case id, name, color, icon
    }
}

public struct Category: Codable, Sendable, Equatable, Identifiable {
    public let id: UUID
    public let name: String
    public let description: String?
    public let color: String?
    public let icon: String?
    public let position: Int
    public let isDefault: Bool
    public let profileCount: Int
    public let deviceCount: Int
    public let createdAt: String
    public let updatedAt: String

    public init(
        id: UUID,
        name: String,
        description: String?,
        color: String?,
        icon: String?,
        position: Int,
        isDefault: Bool,
        profileCount: Int,
        deviceCount: Int,
        createdAt: String,
        updatedAt: String
    ) {
        self.id = id
        self.name = name
        self.description = description
        self.color = color
        self.icon = icon
        self.position = position
        self.isDefault = isDefault
        self.profileCount = profileCount
        self.deviceCount = deviceCount
        self.createdAt = createdAt
        self.updatedAt = updatedAt
    }

    public init(from decoder: Decoder) throws {
        let values = try decoder.container(keyedBy: CodingKeys.self)
        id = try values.decode(UUID.self, forKey: .id)
        name = try values.decode(String.self, forKey: .name)
        description = try values.decodeRequiredNullable(String.self, forKey: .description)
        color = try values.decodeRequiredNullable(String.self, forKey: .color)
        icon = try values.decodeRequiredNullable(String.self, forKey: .icon)
        position = try values.decode(Int.self, forKey: .position)
        isDefault = try values.decode(Bool.self, forKey: .isDefault)
        profileCount = try values.decode(Int.self, forKey: .profileCount)
        deviceCount = try values.decode(Int.self, forKey: .deviceCount)
        createdAt = try values.decode(String.self, forKey: .createdAt)
        updatedAt = try values.decode(String.self, forKey: .updatedAt)
    }

    private enum CodingKeys: String, CodingKey {
        case id, name, description, color, icon, position, isDefault, profileCount, deviceCount, createdAt, updatedAt
    }
}

public struct CategoryList: Codable, Sendable, Equatable {
    public let categories: [Category]
}

public enum PatchField<Value: Encodable & Sendable & Equatable>: Sendable, Equatable {
    case omitted
    case null
    case value(Value)
}

public struct CategoryCreateRequest: Codable, Sendable, Equatable {
    public let name: String
    public let description: String?
    public let color: String?
    public let icon: String?

    public init(name: String, description: String? = nil, color: String? = nil, icon: String? = nil) {
        self.name = name
        self.description = description
        self.color = color
        self.icon = icon
    }
}

public struct CategoryUpdateRequest: Encodable, Sendable, Equatable {
    public let name: String?
    public let description: PatchField<String>
    public let color: PatchField<String>
    public let icon: PatchField<String>
    public let isDefault: Bool?

    public init(
        name: String? = nil,
        description: PatchField<String> = .omitted,
        color: PatchField<String> = .omitted,
        icon: PatchField<String> = .omitted,
        isDefault: Bool? = nil
    ) {
        self.name = name
        self.description = description
        self.color = color
        self.icon = icon
        self.isDefault = isDefault
    }

    public func encode(to encoder: Encoder) throws {
        var values = encoder.container(keyedBy: CodingKeys.self)
        try values.encodeIfPresent(name, forKey: .name)
        try values.encode(description, forKey: .description)
        try values.encode(color, forKey: .color)
        try values.encode(icon, forKey: .icon)
        try values.encodeIfPresent(isDefault, forKey: .isDefault)
    }

    private enum CodingKeys: String, CodingKey {
        case name, description, color, icon, isDefault
    }
}

private extension KeyedEncodingContainer {
    mutating func encode<Value: Encodable & Sendable & Equatable>(_ field: PatchField<Value>, forKey key: Key) throws {
        switch field {
        case .omitted:
            break
        case .null:
            try encodeNil(forKey: key)
        case .value(let value):
            try encode(value, forKey: key)
        }
    }
}

private extension KeyedDecodingContainer {
    func decodeRequiredNullable<Value: Decodable>(_ type: Value.Type, forKey key: Key) throws -> Value? {
        guard contains(key) else {
            throw DecodingError.keyNotFound(
                key,
                DecodingError.Context(codingPath: codingPath, debugDescription: "Required nullable field is missing.")
            )
        }
        return try decodeIfPresent(type, forKey: key)
    }
}

private func validateAuthorizationContext(
    _ scope: AuthorizationScope,
    category: CategoryRef?,
    codingPath: [CodingKey]
) throws {
    let isValid = (scope == .globalAdministrator && category == nil) || (scope == .category && category != nil)
    guard isValid else {
        throw DecodingError.dataCorrupted(
            DecodingError.Context(
                codingPath: codingPath,
                debugDescription: "authorizationScope and category do not form a valid authorization context."
            )
        )
    }
}

public struct LoginDevice: Codable, Sendable, Equatable {
    public let id: UUID?
    public let name: String
    public let platform: String

    public init(id: UUID? = nil, name: String, platform: String) {
        self.id = id
        self.name = name
        self.platform = platform
    }
}

public struct Device: Codable, Sendable, Equatable, Identifiable {
    public let id: UUID
    public let name: String
    public let platform: String
    public let categoryId: UUID
    public let category: CategoryRef
    public let internalNote: String?
    public let approvedAt: String?
    public let lastSeenAt: String?
    public let createdAt: String
    public let updatedAt: String

    public init(
        id: UUID,
        name: String,
        platform: String,
        categoryId: UUID,
        category: CategoryRef,
        internalNote: String?,
        approvedAt: String?,
        lastSeenAt: String?,
        createdAt: String,
        updatedAt: String
    ) {
        self.id = id
        self.name = name
        self.platform = platform
        self.categoryId = categoryId
        self.category = category
        self.internalNote = internalNote
        self.approvedAt = approvedAt
        self.lastSeenAt = lastSeenAt
        self.createdAt = createdAt
        self.updatedAt = updatedAt
    }

    public init(from decoder: Decoder) throws {
        let values = try decoder.container(keyedBy: CodingKeys.self)
        id = try values.decode(UUID.self, forKey: .id)
        name = try values.decode(String.self, forKey: .name)
        platform = try values.decode(String.self, forKey: .platform)
        categoryId = try values.decode(UUID.self, forKey: .categoryId)
        category = try values.decode(CategoryRef.self, forKey: .category)
        internalNote = try values.decodeRequiredNullable(String.self, forKey: .internalNote)
        approvedAt = try values.decodeRequiredNullable(String.self, forKey: .approvedAt)
        lastSeenAt = try values.decodeRequiredNullable(String.self, forKey: .lastSeenAt)
        createdAt = try values.decode(String.self, forKey: .createdAt)
        updatedAt = try values.decode(String.self, forKey: .updatedAt)
    }

    private enum CodingKeys: String, CodingKey {
        case id, name, platform, categoryId, category, internalNote, approvedAt, lastSeenAt, createdAt, updatedAt
    }
}

public struct DeviceList: Codable, Sendable, Equatable {
    public let devices: [Device]
}

public struct DeviceUpdateRequest: Encodable, Sendable, Equatable {
    public let name: String?
    public let categoryId: UUID?
    public let internalNote: PatchField<String>

    public init(name: String? = nil, categoryId: UUID? = nil, internalNote: PatchField<String> = .omitted) {
        self.name = name
        self.categoryId = categoryId
        self.internalNote = internalNote
    }

    public func encode(to encoder: Encoder) throws {
        var values = encoder.container(keyedBy: CodingKeys.self)
        try values.encodeIfPresent(name, forKey: .name)
        try values.encodeIfPresent(categoryId, forKey: .categoryId)
        try values.encode(internalNote, forKey: .internalNote)
    }

    private enum CodingKeys: String, CodingKey {
        case name, categoryId, internalNote
    }
}

public struct LoginRequest: Codable, Sendable, Equatable {
    public let username: String
    public let password: String
    public let device: LoginDevice

    public init(username: String, password: String, device: LoginDevice) {
        self.username = username
        self.password = password
        self.device = device
    }
}

public struct TokenPair: Codable, Sendable, Equatable {
    public let tokenType: String
    public let accessToken: String
    public let accessTokenExpiresAt: String
    public let refreshToken: String
    public let refreshTokenExpiresAt: String
    public let sessionId: UUID
    public let deviceId: UUID
    public let authorizationScope: AuthorizationScope
    public let category: CategoryRef?

    public init(
        tokenType: String,
        accessToken: String,
        accessTokenExpiresAt: String,
        refreshToken: String,
        refreshTokenExpiresAt: String,
        sessionId: UUID,
        deviceId: UUID,
        authorizationScope: AuthorizationScope,
        category: CategoryRef?
    ) {
        self.tokenType = tokenType
        self.accessToken = accessToken
        self.accessTokenExpiresAt = accessTokenExpiresAt
        self.refreshToken = refreshToken
        self.refreshTokenExpiresAt = refreshTokenExpiresAt
        self.sessionId = sessionId
        self.deviceId = deviceId
        self.authorizationScope = authorizationScope
        self.category = category
    }

    public init(from decoder: Decoder) throws {
        let values = try decoder.container(keyedBy: CodingKeys.self)
        tokenType = try values.decode(String.self, forKey: .tokenType)
        accessToken = try values.decode(String.self, forKey: .accessToken)
        accessTokenExpiresAt = try values.decode(String.self, forKey: .accessTokenExpiresAt)
        refreshToken = try values.decode(String.self, forKey: .refreshToken)
        refreshTokenExpiresAt = try values.decode(String.self, forKey: .refreshTokenExpiresAt)
        sessionId = try values.decode(UUID.self, forKey: .sessionId)
        deviceId = try values.decode(UUID.self, forKey: .deviceId)
        authorizationScope = try values.decode(AuthorizationScope.self, forKey: .authorizationScope)
        category = try values.decodeRequiredNullable(CategoryRef.self, forKey: .category)
        try validateAuthorizationContext(authorizationScope, category: category, codingPath: decoder.codingPath)
    }

    private enum CodingKeys: String, CodingKey {
        case tokenType, accessToken, accessTokenExpiresAt, refreshToken, refreshTokenExpiresAt
        case sessionId, deviceId, authorizationScope, category
    }
}

public struct AccountSession: Codable, Sendable, Equatable {
    public let id: UUID
    public let deviceId: UUID
    public let activeProfile: ActiveProfileGrant?
    public let authorizationScope: AuthorizationScope
    public let category: CategoryRef?

    public init(
        id: UUID,
        deviceId: UUID,
        activeProfile: ActiveProfileGrant?,
        authorizationScope: AuthorizationScope,
        category: CategoryRef?
    ) {
        self.id = id
        self.deviceId = deviceId
        self.activeProfile = activeProfile
        self.authorizationScope = authorizationScope
        self.category = category
    }

    public init(from decoder: Decoder) throws {
        let values = try decoder.container(keyedBy: CodingKeys.self)
        id = try values.decode(UUID.self, forKey: .id)
        deviceId = try values.decode(UUID.self, forKey: .deviceId)
        activeProfile = try values.decodeRequiredNullable(ActiveProfileGrant.self, forKey: .activeProfile)
        authorizationScope = try values.decode(AuthorizationScope.self, forKey: .authorizationScope)
        category = try values.decodeRequiredNullable(CategoryRef.self, forKey: .category)
        try validateAuthorizationContext(authorizationScope, category: category, codingPath: decoder.codingPath)
    }

    private enum CodingKeys: String, CodingKey {
        case id, deviceId, activeProfile, authorizationScope, category
    }
}

public struct Account: Codable, Sendable, Equatable {
    public struct User: Codable, Sendable, Equatable {
        public let id: UUID
        public let username: String
        public let role: String
    }

    public let user: User
    public let session: AccountSession
    public let profiles: [Profile]
}

public struct ActiveProfileGrant: Codable, Sendable, Equatable {
    public let id: UUID
    public let expiresAt: String
}

public struct SessionList: Codable, Sendable, Equatable {
    public let sessions: [Session]
}

public struct Session: Codable, Sendable, Equatable, Identifiable {
    public let id: UUID
    public let deviceId: UUID
    public let deviceName: String
    public let platform: String
    public let ipAddress: String?
    public let createdAt: String
    public let lastSeenAt: String
    public let current: Bool
    public let authorizationScope: AuthorizationScope
    public let category: CategoryRef?

    public init(
        id: UUID,
        deviceId: UUID,
        deviceName: String,
        platform: String,
        ipAddress: String?,
        createdAt: String,
        lastSeenAt: String,
        current: Bool,
        authorizationScope: AuthorizationScope,
        category: CategoryRef?
    ) {
        self.id = id
        self.deviceId = deviceId
        self.deviceName = deviceName
        self.platform = platform
        self.ipAddress = ipAddress
        self.createdAt = createdAt
        self.lastSeenAt = lastSeenAt
        self.current = current
        self.authorizationScope = authorizationScope
        self.category = category
    }

    public init(from decoder: Decoder) throws {
        let values = try decoder.container(keyedBy: CodingKeys.self)
        id = try values.decode(UUID.self, forKey: .id)
        deviceId = try values.decode(UUID.self, forKey: .deviceId)
        deviceName = try values.decode(String.self, forKey: .deviceName)
        platform = try values.decode(String.self, forKey: .platform)
        ipAddress = try values.decodeRequiredNullable(String.self, forKey: .ipAddress)
        createdAt = try values.decode(String.self, forKey: .createdAt)
        lastSeenAt = try values.decode(String.self, forKey: .lastSeenAt)
        current = try values.decode(Bool.self, forKey: .current)
        authorizationScope = try values.decode(AuthorizationScope.self, forKey: .authorizationScope)
        category = try values.decodeRequiredNullable(CategoryRef.self, forKey: .category)
        try validateAuthorizationContext(authorizationScope, category: category, codingPath: decoder.codingPath)
    }

    private enum CodingKeys: String, CodingKey {
        case id, deviceId, deviceName, platform, ipAddress, createdAt, lastSeenAt, current
        case authorizationScope, category
    }
}

public struct ProfileSessionList: Codable, Sendable, Equatable {
    public let sessions: [ProfileSession]
}

public struct ProfileSession: Codable, Sendable, Equatable, Identifiable {
    public let id: UUID
    public let userId: UUID
    public let username: String
    public let deviceId: UUID
    public let deviceName: String
    public let platform: String
    public let ipAddress: String?
    public let createdAt: String
    public let lastSeenAt: String
    public let profileGrantExpiresAt: String
    public let current: Bool
    public let authorizationScope: AuthorizationScope
    public let category: CategoryRef?

    public init(
        id: UUID,
        userId: UUID,
        username: String,
        deviceId: UUID,
        deviceName: String,
        platform: String,
        ipAddress: String?,
        createdAt: String,
        lastSeenAt: String,
        profileGrantExpiresAt: String,
        current: Bool,
        authorizationScope: AuthorizationScope,
        category: CategoryRef?
    ) {
        self.id = id
        self.userId = userId
        self.username = username
        self.deviceId = deviceId
        self.deviceName = deviceName
        self.platform = platform
        self.ipAddress = ipAddress
        self.createdAt = createdAt
        self.lastSeenAt = lastSeenAt
        self.profileGrantExpiresAt = profileGrantExpiresAt
        self.current = current
        self.authorizationScope = authorizationScope
        self.category = category
    }

    public init(from decoder: Decoder) throws {
        let values = try decoder.container(keyedBy: CodingKeys.self)
        id = try values.decode(UUID.self, forKey: .id)
        userId = try values.decode(UUID.self, forKey: .userId)
        username = try values.decode(String.self, forKey: .username)
        deviceId = try values.decode(UUID.self, forKey: .deviceId)
        deviceName = try values.decode(String.self, forKey: .deviceName)
        platform = try values.decode(String.self, forKey: .platform)
        ipAddress = try values.decodeRequiredNullable(String.self, forKey: .ipAddress)
        createdAt = try values.decode(String.self, forKey: .createdAt)
        lastSeenAt = try values.decode(String.self, forKey: .lastSeenAt)
        profileGrantExpiresAt = try values.decode(String.self, forKey: .profileGrantExpiresAt)
        current = try values.decode(Bool.self, forKey: .current)
        authorizationScope = try values.decode(AuthorizationScope.self, forKey: .authorizationScope)
        category = try values.decodeRequiredNullable(CategoryRef.self, forKey: .category)
        try validateAuthorizationContext(authorizationScope, category: category, codingPath: decoder.codingPath)
    }

    private enum CodingKeys: String, CodingKey {
        case id, userId, username, deviceId, deviceName, platform, ipAddress, createdAt, lastSeenAt
        case profileGrantExpiresAt, current, authorizationScope, category
    }
}

public struct ProfileList: Codable, Sendable, Equatable {
    public let profiles: [Profile]
}

public struct Profile: Codable, Sendable, Equatable, Identifiable {
    public let id: UUID
    public let name: String
    public let description: String?
    public let categoryId: UUID
    public let category: CategoryRef
    public let isChild: Bool
    public let hasPin: Bool
    public let canManage: Bool
    public let enabled: Bool
    public let availableFrom: String?
    public let availableUntil: String?
    public let accessStartTime: String?
    public let accessEndTime: String?
    public let accessTimezone: String
    public let accessible: Bool
    public let avatar: ProfileAvatar
}

public struct ProfileAvatar: Codable, Sendable, Equatable {
    public let kind: String
    public let presetId: String?
    public let url: String
}

public struct ProfileSelection: Codable, Sendable, Equatable {
    public let profile: Profile
    public let expiresAt: String
}

public struct CategoryOrderRequest: Codable, Sendable, Equatable {
    public let categoryIds: [UUID]
    public init(categoryIds: [UUID]) { self.categoryIds = categoryIds }
}

public struct CategoryDeleteRequest: Encodable, Sendable, Equatable {
    public let reassignToCategoryId: UUID?
    public init(reassignToCategoryId: UUID? = nil) { self.reassignToCategoryId = reassignToCategoryId }

    public func encode(to encoder: Encoder) throws {
        var values = encoder.container(keyedBy: CodingKeys.self)
        if let reassignToCategoryId {
            try values.encode(reassignToCategoryId, forKey: .reassignToCategoryId)
        } else {
            try values.encodeNil(forKey: .reassignToCategoryId)
        }
    }

    private enum CodingKeys: String, CodingKey { case reassignToCategoryId }
}

public struct ProfileCategoryMoveRequest: Codable, Sendable, Equatable {
    public let profileIds: [UUID]
    public let categoryId: UUID
    public init(profileIds: [UUID], categoryId: UUID) {
        self.profileIds = profileIds
        self.categoryId = categoryId
    }
}

public struct DeviceCategoryMoveRequest: Codable, Sendable, Equatable {
    public let deviceIds: [UUID]
    public let categoryId: UUID
    public init(deviceIds: [UUID], categoryId: UUID) {
        self.deviceIds = deviceIds
        self.categoryId = categoryId
    }
}

public struct DeviceAuthorizationRequest: Codable, Sendable, Equatable {
    public let deviceName: String
    public let platform: String
}

public struct DeviceAuthorizationResponse: Codable, Sendable, Equatable {
    public let deviceCode: String
    public let userCode: String
    public let verificationUri: String
    public let verificationUriComplete: String
    public let expiresAt: String
    public let intervalSeconds: Int
}

public struct DeviceCodeApprovalRequest: Codable, Sendable, Equatable {
    public let userCode: String
    public let categoryId: UUID
    public let deviceName: String?
    public let internalNote: String?

    public init(userCode: String, categoryId: UUID, deviceName: String? = nil, internalNote: String? = nil) {
        self.userCode = userCode
        self.categoryId = categoryId
        self.deviceName = deviceName
        self.internalNote = internalNote
    }
}

public struct DeviceCodeTokenRequest: Codable, Sendable, Equatable {
    public let deviceCode: String
}

public struct InstanceTranscodingPatch: Encodable, Sendable, Equatable {
    public let allowTranscoding: Bool?

    public init(allowTranscoding: Bool?) {
        self.allowTranscoding = allowTranscoding
    }

    public func encode(to encoder: Encoder) throws {
        var values = encoder.container(keyedBy: CodingKeys.self)
        if let allowTranscoding {
            try values.encode(allowTranscoding, forKey: .allowTranscoding)
        } else {
            try values.encodeNil(forKey: .allowTranscoding)
        }
    }

    private enum CodingKeys: String, CodingKey {
        case allowTranscoding
    }
}

public struct ProfileTranscodingPatch: Encodable, Sendable, Equatable {
    public let transcoding: String?

    public init(transcoding: String?) {
        self.transcoding = transcoding
    }

    public func encode(to encoder: Encoder) throws {
        var values = encoder.container(keyedBy: CodingKeys.self)
        if let transcoding {
            try values.encode(transcoding, forKey: .transcoding)
        } else {
            try values.encodeNil(forKey: .transcoding)
        }
    }

    private enum CodingKeys: String, CodingKey {
        case transcoding
    }
}

public struct SettingsValues: Codable, Sendable, Equatable {
    public let allowTranscoding: Bool?
    public let transcoding: String?
    public let maximumCastMembers: Int?
}

public struct SettingsLayer: Codable, Sendable, Equatable {
    public let schemaVersion: Int
    public let settings: SettingsValues
    public let updatedAt: String?
}

public struct EffectiveSettingsSources: Codable, Sendable, Equatable {
    public let allowTranscoding: String?
    public let transcoding: String?
    public let maximumCastMembers: String?
}

public struct EffectiveSettings: Codable, Sendable, Equatable {
    public let schemaVersion: Int
    public let settings: SettingsValues
    public let sources: EffectiveSettingsSources
}

public enum MediaType: String, Codable, Sendable {
    case movie
    case series
    case season
    case episode
}

public enum SeriesMappingProvider: String, Codable, Sendable {
    case tmdb
    case tvdb
}

public struct Genre: Codable, Sendable, Equatable {
    public let id: Int
    public let name: String
}

public struct Movie: Codable, Sendable, Equatable, Identifiable {
    public let id: UUID
    public let mediaType: MediaType
    public let title: String
    public let originalTitle: String
    public let originalLanguage: String
    public let overview: String
    public let releaseDate: String?
    public let posterUrl: String?
    public let backdropUrl: String?
    public let logoUrl: String?
    public let tagline: String?
    public let runtimeMinutes: Int?
    public let genres: [Genre]
    public let voteAverage: Double
    public let voteCount: Int
    public let externalIds: [String: String]
}

public struct Series: Codable, Sendable, Equatable, Identifiable {
    public let id: UUID
    public let mediaType: MediaType
    public let name: String
    public let originalName: String
    public let originalLanguage: String
    public let overview: String
    public let firstAirDate: String?
    public let lastAirDate: String?
    public let posterUrl: String?
    public let backdropUrl: String?
    public let logoUrl: String?
    public let tagline: String?
    public let status: String?
    public let numberOfSeasons: Int?
    public let numberOfEpisodes: Int?
    public let genres: [Genre]
    public let voteAverage: Double
    public let voteCount: Int
    public let seasons: [SeasonSummary]
    public let aliases: [SeriesAlias]
    public let episodeOrders: [EpisodeOrder]
    public let mappingProvider: SeriesMappingProvider
    public let externalIds: [String: String]
}

public struct SeriesAlias: Codable, Sendable, Equatable {
    public let language: String
    public let name: String
}

public struct EpisodeOrder: Codable, Sendable, Equatable, Identifiable {
    public let id: String
    public let name: String
    public let type: String
    public let isDefault: Bool
}

public struct SeasonSummary: Codable, Sendable, Equatable, Identifiable {
    public let id: String
    public let mediaType: MediaType
    public let seriesId: UUID
    public let name: String
    public let overview: String
    public let seasonNumber: Int
    public let episodeCount: Int
    public let airDate: String?
    public let posterUrl: String?
    public let backdropUrl: String?
    public let voteAverage: Double
    public let externalIds: [String: String]
}

public struct Season: Codable, Sendable, Equatable, Identifiable {
    public let id: String
    public let mediaType: MediaType
    public let seriesId: UUID
    public let name: String
    public let overview: String
    public let seasonNumber: Int
    public let airDate: String?
    public let posterUrl: String?
    public let backdropUrl: String?
    public let voteAverage: Double
    public let episodes: [Episode]
    public let externalIds: [String: String]
}

public struct Episode: Codable, Sendable, Equatable, Identifiable {
    public let id: UUID
    public let mediaType: MediaType
    public let seasonId: String
    public let name: String
    public let overview: String
    public let seasonNumber: Int
    public let episodeNumber: Int
    public let airDate: String?
    public let stillUrl: String?
    public let backdropUrl: String?
    public let runtimeMinutes: Int?
    public let voteAverage: Double
    public let voteCount: Int
    public let externalIds: [String: String]
}

public struct TrailerList: Codable, Sendable, Equatable {
    public let trailers: [Trailer]
}

public struct Trailer: Codable, Sendable, Equatable, Identifiable {
    public var id: String { youtubeId }
    public let youtubeId: String
    public let name: String
    public let language: String
    public let isFallback: Bool
    public let captionPreference: String?
}

public struct PlaybackMediaProfile: Codable, Sendable, Equatable {
    public let container: String
    public let videoCodec: String
    public let audioCodec: String?

    public init(container: String, videoCodec: String, audioCodec: String? = nil) {
        self.container = container
        self.videoCodec = videoCodec
        self.audioCodec = audioCodec
    }
}

public struct PlaybackCapabilities: Codable, Sendable, Equatable {
    public let streamingProtocols: [String]
    public let containers: [String]
    public let videoCodecs: [String]?
    public let audioCodecs: [String]?
    public let hdrFormats: [String]?
    public let externalPlayers: [String]?
    public let processingModes: [String]?
    public let maximumHeight: Int?
    public let maximumVideoBitrateKbps: Int?
    public let maximumAudioChannels: Int?
    public let subtitleModes: [String]?
    public let mediaProfiles: [PlaybackMediaProfile]?

    public init(
        streamingProtocols: [String],
        containers: [String],
        videoCodecs: [String]? = nil,
        audioCodecs: [String]? = nil,
        hdrFormats: [String]? = nil,
        externalPlayers: [String]? = nil,
        processingModes: [String]? = nil,
        maximumHeight: Int? = nil,
        maximumVideoBitrateKbps: Int? = nil,
        maximumAudioChannels: Int? = nil,
        subtitleModes: [String]? = nil,
        mediaProfiles: [PlaybackMediaProfile]? = nil
    ) {
        self.streamingProtocols = streamingProtocols
        self.containers = containers
        self.videoCodecs = videoCodecs
        self.audioCodecs = audioCodecs
        self.hdrFormats = hdrFormats
        self.externalPlayers = externalPlayers
        self.processingModes = processingModes
        self.maximumHeight = maximumHeight
        self.maximumVideoBitrateKbps = maximumVideoBitrateKbps
        self.maximumAudioChannels = maximumAudioChannels
        self.subtitleModes = subtitleModes
        self.mediaProfiles = mediaProfiles
    }
}

public struct PlaybackSourceList: Codable, Sendable, Equatable {
    public let sources: [PlaybackSourceOption]
    public let providerErrors: [PlaybackProviderError]
}

public struct PlaybackSourceOption: Codable, Sendable, Equatable, Identifiable {
    public let id: String
    public let sourceRef: String
    public let addonId: UUID
    public let manifestId: String
    public let streamIndex: Int
    public let name: String
    public let description: String?
    public let filename: String?
    public let `protocol`: String
    public let container: String?
    public let expiresAt: String
}

public enum PlaybackMode: String, Codable, Sendable {
    case direct
    case remux
    case transcodeAudio = "transcode_audio"
    case transcode
    case youtube
    case external
}

public struct PlaybackPreparation: Codable, Sendable, Equatable {
    public let sourceRef: String
    public let mode: PlaybackMode
    public let `protocol`: String
    public let container: String?
    public let media: PlaybackMediaInspection?
    public let subtitleCount: Int
    public let expiresAt: String
    public let decision: PlaybackDecision?
}

public struct PlaybackSession: Codable, Sendable, Equatable, Identifiable {
    public let id: UUID
    public let selectedSourceId: String
    public let selectedAudioTrack: Int?
    public let selectedSubtitleId: String?
    public let sources: [PlaybackSource]
    public let subtitles: [PlaybackSubtitle]
    public let providerErrors: [PlaybackProviderError]
    public let expiresAt: String
}

public struct PlaybackSource: Codable, Sendable, Equatable, Identifiable {
    public let id: String
    public let addonId: UUID
    public let manifestId: String
    public let name: String?
    public let title: String?
    public let mode: PlaybackMode
    public let url: String?
    public let ytId: String?
    public let infoHash: String?
    public let fileIndex: Int?
    public let `protocol`: String
    public let container: String?
    public let compatible: Bool
    public let media: PlaybackMediaInspection?
    public let decision: PlaybackDecision?
}

public struct PlaybackMediaInspection: Codable, Sendable, Equatable {
    public let container: String?
    public let durationSeconds: Double?
    public let hdrFormat: String?
    public let videoTracks: [PlaybackMediaTrack]
    public let audioTracks: [PlaybackMediaTrack]
    public let subtitleTracks: [PlaybackMediaTrack]
}

public struct PlaybackDecision: Codable, Sendable, Equatable {
    public let reason: String
    public let videoAction: String
    public let audioAction: String
    public let subtitleAction: String
    public let toneMapping: Bool
    public let source: PlaybackDecisionSource?
    public let target: PlaybackDecisionTarget?
}

public struct PlaybackDecisionSource: Codable, Sendable, Equatable {
    public let container: String?
    public let videoCodec: String?
    public let audioCodec: String?
    public let height: Int?
    public let videoBitrateKbps: Int?
    public let hdrFormat: String?
}

public struct PlaybackDecisionTarget: Codable, Sendable, Equatable {
    public let `protocol`: String?
    public let container: String?
    public let videoCodec: String?
    public let audioCodec: String?
    public let height: Int?
    public let videoBitrateKbps: Int?
}

public struct PlaybackMediaTrack: Codable, Sendable, Equatable {
    public let index: Int
    public let type: String
    public let codec: String
    public let profile: String?
    public let language: String?
    public let title: String?
    public let forced: Bool?
    public let width: Int?
    public let height: Int?
    public let channels: Int?
}

public struct PlaybackSubtitle: Codable, Sendable, Equatable, Identifiable {
    public let id: String
    public let addonId: UUID
    public let manifestId: String
    public let language: String?
    public let forced: Bool?
    public let `default`: Bool?
    public let delivery: String?
    public let url: String?
}

public struct PlaybackProviderError: Codable, Sendable, Equatable {
    public let addonId: UUID
    public let manifestId: String
    public let code: String
    public let message: String
}

public struct PlaybackActivity: Codable, Sendable, Equatable {
    public let summary: PlaybackActivitySummary
    public let diagnostics: PlaybackMediaDiagnostics
    public let sessions: [PlaybackActivitySession]
    public let jobs: [PlaybackMediaJob]
}

public struct PlaybackActivitySummary: Codable, Sendable, Equatable {
    public let activeSessions: Int
    public let activeJobs: Int
    public let processingSlots: Int
    public let processingLimit: Int
    public let storageBytes: Int64
    public let storageLimitBytes: Int64
}

public struct PlaybackMediaDiagnostics: Codable, Sendable, Equatable {
    public let videoEncoder: String
    public let hardwareToneMap: Bool
}

public struct PlaybackActivitySession: Codable, Sendable, Equatable, Identifiable {
    public let id: UUID
    public let titleId: String?
    public let artworkUrl: String?
    public let externalIds: [String: String]?
    public let externalIdMediaTypes: [String: String]?
    public let title: String
    public let mediaType: String
    public let mode: String
    public let decision: PlaybackDecision?
    public let username: String
    public let profileId: UUID
    public let profile: String
    public let device: String
    public let platform: String
    public let processing: Bool
    public let positionSeconds: Int
    public let durationSeconds: Int
    public let createdAt: String
    public let lastSeenAt: String
    public let expiresAt: String
}

public struct PlaybackMediaJob: Codable, Sendable, Equatable {
    public let sessionId: UUID?
    public let assetId: String
    public let mode: String
    public let state: String
    public let prewarming: Bool
    public let progressPercent: Double?
    public let speed: Double?
    public let createdAt: String
    public let lastSeenAt: String
}
