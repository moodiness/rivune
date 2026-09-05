import Foundation

public enum RivuneProtocol {
  public static let version = 22
}

public enum DiscoveryCapability: String, Codable, CaseIterable, Sendable, Equatable {
  case boundedAggregateResources = "bounded-aggregate-resources"
  case profileArchivesV2 = "profile-archives-v2"
  case requestCorrelation = "request-correlation"
  case localRecommendations = "local-recommendations"
  case semanticSearch = "semantic-search"
  case playbackCoordination = "playback-coordination"
  case playbackCommandResults = "playback-command-results"
  case addonVerifications = "addon-verifications"
}

public struct Discovery: Codable, Sendable, Equatable {
  public let name: String
  public let serverVersion: String
  public let protocolVersion: Int
  public let apiBaseUrl: String
  public let setupRequired: Bool
  public let setupCompleted: Bool?
  public let demoAvailable: Bool?
  public let timezone: String
  public let interfaceLanguage: String
  public let capabilities: [String]

  public func supports(_ capability: DiscoveryCapability) -> Bool {
    capabilities.contains(capability.rawValue)
  }

  public var supportsProfileArchives: Bool {
    supports(.profileArchivesV2)
  }

  init(
    name: String,
    serverVersion: String,
    protocolVersion: Int,
    apiBaseUrl: String,
    setupRequired: Bool,
    setupCompleted: Bool?,
    demoAvailable: Bool?,
    timezone: String,
    interfaceLanguage: String,
    capabilities: [String]
  ) {
    self.name = name
    self.serverVersion = serverVersion
    self.protocolVersion = protocolVersion
    self.apiBaseUrl = apiBaseUrl
    self.setupRequired = setupRequired
    self.setupCompleted = setupCompleted
    self.demoAvailable = demoAvailable
    self.timezone = timezone
    self.interfaceLanguage = interfaceLanguage
    self.capabilities = Self.normalizeCapabilities(capabilities)
  }

  public init(from decoder: Decoder) throws {
    let values = try decoder.container(keyedBy: CodingKeys.self)
    name = try values.decode(String.self, forKey: .name)
    serverVersion = try values.decode(String.self, forKey: .serverVersion)
    protocolVersion = try values.decode(Int.self, forKey: .protocolVersion)
    apiBaseUrl = try values.decode(String.self, forKey: .apiBaseUrl)
    setupRequired = try values.decode(Bool.self, forKey: .setupRequired)
    setupCompleted = try values.decodeIfPresent(Bool.self, forKey: .setupCompleted)
    demoAvailable = try values.decodeIfPresent(Bool.self, forKey: .demoAvailable)
    timezone = try values.decode(String.self, forKey: .timezone)
    interfaceLanguage = try values.decode(String.self, forKey: .interfaceLanguage)
    capabilities = Self.decodeCapabilities(from: values, forKey: .capabilities)
  }

  static func decodeCapabilities<Key: CodingKey>(
    from values: KeyedDecodingContainer<Key>,
    forKey key: Key
  ) -> [String] {
    guard let rawValues = try? values.decode([JSONValue].self, forKey: key) else {
      return []
    }
    return normalizeCapabilities(
      rawValues.compactMap { value in
        guard case .string(let identifier) = value else { return nil }
        return identifier
      })
  }

  static func normalizeCapabilities(_ capabilities: [String]) -> [String] {
    var normalized: [String] = []
    normalized.reserveCapacity(min(capabilities.count, 64))
    var seen = Set<String>()

    for capability in capabilities where isSafeCapabilityIdentifier(capability) {
      guard seen.insert(capability).inserted else { continue }
      normalized.append(capability)
      if normalized.count == 64 { break }
    }
    return normalized
  }

  private static func isSafeCapabilityIdentifier(_ identifier: String) -> Bool {
    let bytes = identifier.utf8
    guard (1...64).contains(bytes.count) else { return false }

    var previousWasHyphen = true
    for byte in bytes {
      switch byte {
      case 97...122, 48...57:
        previousWasHyphen = false
      case 45 where !previousWasHyphen:
        previousWasHyphen = true
      default:
        return false
      }
    }
    return !previousWasHyphen
  }

  enum CodingKeys: String, CodingKey {
    case name, serverVersion, protocolVersion, apiBaseUrl, setupRequired, setupCompleted
    case demoAvailable, timezone, interfaceLanguage, capabilities
  }
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

  public func encode(to encoder: Encoder) throws {
    var values = encoder.container(keyedBy: CodingKeys.self)
    try values.encode(id, forKey: .id)
    try values.encode(name, forKey: .name)
    if let color {
      try values.encode(color, forKey: .color)
    } else {
      try values.encodeNil(forKey: .color)
    }
    if let icon {
      try values.encode(icon, forKey: .icon)
    } else {
      try values.encodeNil(forKey: .icon)
    }
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
    case id, name, description, color, icon, position, isDefault, profileCount, deviceCount,
      createdAt, updatedAt
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

extension KeyedEncodingContainer {
  fileprivate mutating func encode<Value: Encodable & Sendable & Equatable>(
    _ field: PatchField<Value>, forKey key: Key
  ) throws {
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

extension KeyedDecodingContainer {
  fileprivate func decodeRequiredNullable<Value: Decodable>(_ type: Value.Type, forKey key: Key)
    throws -> Value?
  {
    guard contains(key) else {
      throw DecodingError.keyNotFound(
        key,
        DecodingError.Context(
          codingPath: codingPath, debugDescription: "Required nullable field is missing.")
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
  let isValid =
    (scope == .globalAdministrator && category == nil) || (scope == .category && category != nil)
  guard isValid else {
    throw DecodingError.dataCorrupted(
      DecodingError.Context(
        codingPath: codingPath,
        debugDescription:
          "authorizationScope and category do not form a valid authorization context."
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
    case id, name, platform, categoryId, category, internalNote, approvedAt, lastSeenAt, createdAt,
      updatedAt
  }
}

public struct DeviceList: Codable, Sendable, Equatable {
  public let devices: [Device]
}

public struct DeviceUpdateRequest: Encodable, Sendable, Equatable {
  public let name: String?
  public let categoryId: UUID?
  public let internalNote: PatchField<String>

  public init(
    name: String? = nil, categoryId: UUID? = nil, internalNote: PatchField<String> = .omitted
  ) {
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
    try validateAuthorizationContext(
      authorizationScope, category: category, codingPath: decoder.codingPath)
  }

  public func encode(to encoder: Encoder) throws {
    var values = encoder.container(keyedBy: CodingKeys.self)
    try values.encode(tokenType, forKey: .tokenType)
    try values.encode(accessToken, forKey: .accessToken)
    try values.encode(accessTokenExpiresAt, forKey: .accessTokenExpiresAt)
    try values.encode(refreshToken, forKey: .refreshToken)
    try values.encode(refreshTokenExpiresAt, forKey: .refreshTokenExpiresAt)
    try values.encode(sessionId, forKey: .sessionId)
    try values.encode(deviceId, forKey: .deviceId)
    try values.encode(authorizationScope, forKey: .authorizationScope)
    if let category {
      try values.encode(category, forKey: .category)
    } else {
      try values.encodeNil(forKey: .category)
    }
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
    activeProfile = try values.decodeRequiredNullable(
      ActiveProfileGrant.self, forKey: .activeProfile)
    authorizationScope = try values.decode(AuthorizationScope.self, forKey: .authorizationScope)
    category = try values.decodeRequiredNullable(CategoryRef.self, forKey: .category)
    try validateAuthorizationContext(
      authorizationScope, category: category, codingPath: decoder.codingPath)
  }

  private enum CodingKeys: String, CodingKey {
    case id, deviceId, activeProfile, authorizationScope, category
  }
}

public struct MaintenanceSettings: Codable, Sendable, Equatable {
  public let enabled: Bool
  public let message: String?

  public init(enabled: Bool, message: String?) {
    self.enabled = enabled
    self.message = message
  }

  public init(from decoder: Decoder) throws {
    let values = try decoder.container(keyedBy: CodingKeys.self)
    enabled = try values.decode(Bool.self, forKey: .enabled)
    message = try values.decodeRequiredNullable(String.self, forKey: .message)
  }

  private enum CodingKeys: String, CodingKey { case enabled, message }
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
  public let maintenance: MaintenanceSettings
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
    try validateAuthorizationContext(
      authorizationScope, category: category, codingPath: decoder.codingPath)
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
    try validateAuthorizationContext(
      authorizationScope, category: category, codingPath: decoder.codingPath)
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
  public let profileContext: String
}

public struct CategoryOrderRequest: Codable, Sendable, Equatable {
  public let categoryIds: [UUID]
  public init(categoryIds: [UUID]) { self.categoryIds = categoryIds }
}

public struct CategoryDeleteRequest: Encodable, Sendable, Equatable {
  public let reassignToCategoryId: UUID?
  public init(reassignToCategoryId: UUID? = nil) {
    self.reassignToCategoryId = reassignToCategoryId
  }

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
  public let installationId: String
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

  public init(
    userCode: String, categoryId: UUID, deviceName: String? = nil, internalNote: String? = nil
  ) {
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

public enum SettingsPatchField<Value: Encodable & Sendable & Equatable>: Encodable, Sendable,
  Equatable
{
  case omitted
  case null
  case value(Value)

  public func encode(to encoder: Encoder) throws {
    var container = encoder.singleValueContainer()
    switch self {
    case .omitted, .null: try container.encodeNil()
    case .value(let value): try container.encode(value)
    }
  }
}

public struct ProfileSettingsPatch: Encodable, Sendable, Equatable {
  public var maximumResolution: SettingsPatchField<String>
  public var preferDirectPlay: SettingsPatchField<Bool>
  public var audioLanguage: SettingsPatchField<String>
  public var metadataLanguage: SettingsPatchField<String>
  public var subtitleLanguage: SettingsPatchField<String>
  public var forcedSubtitleLanguage: SettingsPatchField<String>
  public var autoplayNextEpisode: SettingsPatchField<Bool>
  public var skipIntroEnabled: SettingsPatchField<Bool>
  public var skipRecapEnabled: SettingsPatchField<Bool>
  public var skipOutroEnabled: SettingsPatchField<Bool>
  public var transcoding: SettingsPatchField<String>

  public init(
    maximumResolution: SettingsPatchField<String> = .omitted,
    preferDirectPlay: SettingsPatchField<Bool> = .omitted,
    audioLanguage: SettingsPatchField<String> = .omitted,
    metadataLanguage: SettingsPatchField<String> = .omitted,
    subtitleLanguage: SettingsPatchField<String> = .omitted,
    forcedSubtitleLanguage: SettingsPatchField<String> = .omitted,
    autoplayNextEpisode: SettingsPatchField<Bool> = .omitted,
    skipIntroEnabled: SettingsPatchField<Bool> = .omitted,
    skipRecapEnabled: SettingsPatchField<Bool> = .omitted,
    skipOutroEnabled: SettingsPatchField<Bool> = .omitted,
    transcoding: SettingsPatchField<String> = .omitted
  ) {
    self.maximumResolution = maximumResolution
    self.preferDirectPlay = preferDirectPlay
    self.audioLanguage = audioLanguage
    self.metadataLanguage = metadataLanguage
    self.subtitleLanguage = subtitleLanguage
    self.forcedSubtitleLanguage = forcedSubtitleLanguage
    self.autoplayNextEpisode = autoplayNextEpisode
    self.skipIntroEnabled = skipIntroEnabled
    self.skipRecapEnabled = skipRecapEnabled
    self.skipOutroEnabled = skipOutroEnabled
    self.transcoding = transcoding
  }

  public func encode(to encoder: Encoder) throws {
    var container = encoder.container(keyedBy: CodingKeys.self)
    try container.encodePatch(maximumResolution, forKey: .maximumResolution)
    try container.encodePatch(preferDirectPlay, forKey: .preferDirectPlay)
    try container.encodePatch(audioLanguage, forKey: .audioLanguage)
    try container.encodePatch(metadataLanguage, forKey: .metadataLanguage)
    try container.encodePatch(subtitleLanguage, forKey: .subtitleLanguage)
    try container.encodePatch(forcedSubtitleLanguage, forKey: .forcedSubtitleLanguage)
    try container.encodePatch(autoplayNextEpisode, forKey: .autoplayNextEpisode)
    try container.encodePatch(skipIntroEnabled, forKey: .skipIntroEnabled)
    try container.encodePatch(skipRecapEnabled, forKey: .skipRecapEnabled)
    try container.encodePatch(skipOutroEnabled, forKey: .skipOutroEnabled)
    try container.encodePatch(transcoding, forKey: .transcoding)
  }

  private enum CodingKeys: String, CodingKey {
    case maximumResolution, preferDirectPlay, audioLanguage, metadataLanguage, subtitleLanguage
    case forcedSubtitleLanguage, autoplayNextEpisode, skipIntroEnabled, skipRecapEnabled,
      skipOutroEnabled, transcoding
  }
}

extension KeyedEncodingContainer {
  fileprivate mutating func encodePatch<Value: Encodable & Sendable & Equatable>(
    _ field: SettingsPatchField<Value>, forKey key: Key
  ) throws {
    switch field {
    case .omitted: break
    case .null: try encodeNil(forKey: key)
    case .value(let value): try encode(value, forKey: key)
    }
  }
}

public struct SettingsValues: Codable, Sendable, Equatable {
  public let allowTranscoding: Bool?
  public let transcoding: String?
  public let maximumCastMembers: Int?
  public let maximumResolution: String?
  public let preferDirectPlay: Bool?
  public let audioLanguage: String?
  public let subtitleLanguage: String?
  public let forcedSubtitleLanguage: String?
  public let autoplayNextEpisode: Bool?
  public let skipIntroEnabled: Bool?
  public let skipRecapEnabled: Bool?
  public let skipOutroEnabled: Bool?
  public let metadataLanguage: String?
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
  public let maximumResolution: String?
  public let preferDirectPlay: String?
  public let audioLanguage: String?
  public let subtitleLanguage: String?
  public let forcedSubtitleLanguage: String?
  public let autoplayNextEpisode: String?
  public let skipIntroEnabled: String?
  public let skipRecapEnabled: String?
  public let skipOutroEnabled: String?
  public let metadataLanguage: String?
}

public struct EffectiveSettings: Codable, Sendable, Equatable {
  public let schemaVersion: Int
  public let settings: SettingsValues
  public let sources: EffectiveSettingsSources
}

public enum MediaType: String, Codable, Sendable, Equatable {
  case movie
  case series
  case season
  case episode
}

public enum SeriesMappingProvider: String, Codable, Sendable, Equatable {
  case tmdb
  case tvdb
}

public struct Genre: Codable, Sendable, Equatable {
  public let id: Int
  public let name: String
}
public struct CastMember: Codable, Sendable, Equatable, Identifiable {
  public let id: String
  public let name: String
  public let character: String?
  public let profileUrl: String?
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
  public let cast: [CastMember]
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
  public let cast: [CastMember]
  public let voteAverage: Double
  public let voteCount: Int
  public let seasons: [SeasonSummary]
  public let aliases: [SeriesAlias]
  public let episodeOrders: [EpisodeOrder]
  public let selectedEpisodeOrderId: String?
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
  public let maximumVideoBitDepth: Int?

  public init(
    container: String, videoCodec: String, audioCodec: String? = nil,
    maximumVideoBitDepth: Int? = nil
  ) {
    self.container = container
    self.videoCodec = videoCodec
    self.audioCodec = audioCodec
    self.maximumVideoBitDepth = maximumVideoBitDepth
  }
}

public enum PlaybackProcessingMode: String, Codable, Sendable, Equatable {
  case remux
  case transcodeAudio = "transcode_audio"
  case transcode
}

public enum PlaybackSubtitleMode: String, Codable, Sendable, Equatable {
  case external
  case burn
}

public struct PlaybackCapabilities: Codable, Sendable, Equatable {
  public let streamingProtocols: [String]
  public let containers: [String]
  public let videoCodecs: [String]?
  public let audioCodecs: [String]?
  public let hdrFormats: [String]?
  public let externalPlayers: [String]?
  public let processingModes: [PlaybackProcessingMode]?
  public let hlsSegmentContainer: String?
  public let maximumHeight: Int?
  public let maximumVideoBitrateKbps: Int?
  public let maximumAudioChannels: Int?
  public let subtitleModes: [PlaybackSubtitleMode]?
  public let mediaProfiles: [PlaybackMediaProfile]?

  public init(
    streamingProtocols: [String],
    containers: [String],
    videoCodecs: [String]? = nil,
    audioCodecs: [String]? = nil,
    hdrFormats: [String]? = nil,
    externalPlayers: [String]? = nil,
    processingModes: [PlaybackProcessingMode]? = nil,
    hlsSegmentContainer: String? = nil,
    maximumHeight: Int? = nil,
    maximumVideoBitrateKbps: Int? = nil,
    maximumAudioChannels: Int? = nil,
    subtitleModes: [PlaybackSubtitleMode]? = nil,
    mediaProfiles: [PlaybackMediaProfile]? = nil
  ) {
    self.streamingProtocols = streamingProtocols
    self.containers = containers
    self.videoCodecs = videoCodecs
    self.audioCodecs = audioCodecs
    self.hdrFormats = hdrFormats
    self.externalPlayers = externalPlayers
    self.processingModes = processingModes
    self.hlsSegmentContainer = hlsSegmentContainer
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
  public let addonName: String?
  public let manifestId: String
  public let streamIndex: Int
  public let name: String
  public let description: String?
  public let filename: String?
  public let `protocol`: String
  public let mode: PlaybackMode?
  public let container: String?
  public let expiresAt: String
  public let stableIdentity: String
}

extension PlaybackSourceOption {
  private enum CodingKeys: String, CodingKey {
    case id, sourceRef, addonId, addonName, manifestId, streamIndex, name, description, filename
    case `protocol`, mode, container, expiresAt, stableIdentity
  }

  public init(from decoder: Decoder) throws {
    let values = try decoder.container(keyedBy: CodingKeys.self)
    id = try values.decode(String.self, forKey: .id)
    sourceRef = try values.decode(String.self, forKey: .sourceRef)
    addonId = try values.decode(UUID.self, forKey: .addonId)
    addonName = try values.decodeIfPresent(String.self, forKey: .addonName)
    manifestId = try values.decode(String.self, forKey: .manifestId)
    streamIndex = try values.decode(Int.self, forKey: .streamIndex)
    name = try values.decode(String.self, forKey: .name)
    description = try values.decodeIfPresent(String.self, forKey: .description)
    filename = try values.decodeIfPresent(String.self, forKey: .filename)
    `protocol` = try values.decode(String.self, forKey: .protocol)
    mode = try values.decodeIfPresent(PlaybackMode.self, forKey: .mode)
    container = try values.decodeIfPresent(String.self, forKey: .container)
    expiresAt = try values.decode(String.self, forKey: .expiresAt)
    stableIdentity = try values.decodeIfPresent(String.self, forKey: .stableIdentity) ?? ""
  }
}

public enum PlaybackMode: String, Codable, Sendable, Equatable {
  case direct
  case remux
  case transcodeAudio = "transcode_audio"
  case transcode
  case youtube
  case external
}
public enum PlaybackMediaTimeline: String, Codable, Sendable, Equatable {
  case absolute
  case relative
}

public enum PlaybackDecisionReason: String, Codable, Sendable, Equatable {
  case directSupported = "direct_supported"
  case remuxRequired = "remux_required"
  case audioTranscodeRequired = "audio_transcode_required"
  case videoTranscodeRequired = "video_transcode_required"
  case subtitleBurnRequired = "subtitle_burn_required"
}

public enum PlaybackDecisionConstraint: String, Codable, Sendable, Equatable, Hashable, CaseIterable {
  case containerNotSupported = "container_not_supported"
  case videoCodecNotSupported = "video_codec_not_supported"
  case audioCodecNotSupported = "audio_codec_not_supported"
  case resolutionLimit = "resolution_limit"
  case bitrateLimit = "bitrate_limit"
  case hdrNotSupported = "hdr_not_supported"
  case subtitleBurnRequired = "subtitle_burn_required"
}

public enum PlaybackDecisionAction: String, Codable, Sendable, Equatable {
  case copy
  case transcode
}

public enum PlaybackSubtitleAction: String, Codable, Sendable, Equatable {
  case none
  case external
  case copy
  case burn
}

public enum PlaybackMediaTrackType: String, Codable, Sendable, Equatable {
  case video
  case audio
  case subtitle
}

public enum PlaybackSubtitleDelivery: String, Codable, Sendable, Equatable {
  case external
  case burn
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
public struct CoordinatedPlaybackItem: Codable, Sendable, Equatable {
  public let titleId: UUID
  public let mediaType: String
  public let resourceId: String
  public let sourceAddonId: UUID?
  public let title: String
  public let posterUrl: String?

  public init(
    titleId: UUID, mediaType: String = "", resourceId: String = "", sourceAddonId: UUID? = nil,
    title: String = "", posterUrl: String? = nil
  ) {
    self.titleId = titleId
    self.mediaType = mediaType
    self.resourceId = resourceId
    self.sourceAddonId = sourceAddonId
    self.title = title
    self.posterUrl = posterUrl
  }
}

public struct PlaybackDeviceState: Codable, Sendable, Equatable {
  public let status: String
  public let item: CoordinatedPlaybackItem?
  public let positionMilliseconds: Int64
  public let durationMilliseconds: Int64
  public let updatedAt: String?

  public init(
    status: String, item: CoordinatedPlaybackItem? = nil, positionMilliseconds: Int64 = 0,
    durationMilliseconds: Int64 = 0
  ) {
    self.status = status
    self.item = item
    self.positionMilliseconds = positionMilliseconds
    self.durationMilliseconds = durationMilliseconds
    self.updatedAt = nil
  }
}

public struct PlaybackDeviceHeartbeatInput: Codable, Sendable, Equatable {
  public let capabilities: [String]
  public let state: PlaybackDeviceState
  public init(capabilities: [String], state: PlaybackDeviceState) {
    self.capabilities = capabilities
    self.state = state
  }
}

public struct PlaybackDevice: Codable, Sendable, Equatable, Identifiable {
  public var id: UUID { sessionId }
  public let sessionId: UUID
  public let deviceId: UUID
  public let name: String
  public let platform: String
  public let capabilities: [String]
  public let state: PlaybackDeviceState
  public let revision: Int64
  public let current: Bool
  public let lastSeenAt: String
}

public struct PlaybackDeviceList: Codable, Sendable, Equatable {
  public let devices: [PlaybackDevice]
}

public enum PlaybackLoadMode: String, Codable, Sendable, Equatable {
  case handoff
  case playCopy = "play-copy"
}

public enum PlaybackCommandKind: String, Codable, Sendable, Equatable, CaseIterable {
  case load, play, pause, seek, stop
}

public struct PlaybackCommandInput: Codable, Sendable, Equatable {
  public let operationId: UUID
  public let command: PlaybackCommandKind
  public let item: CoordinatedPlaybackItem?
  public let positionMilliseconds: Int64?
  public let mode: PlaybackLoadMode?
  public let targetRevision: Int64?

  public init(
    operationId: UUID = UUID(), command: PlaybackCommandKind,
    item: CoordinatedPlaybackItem? = nil,
    positionMilliseconds: Int64? = nil, mode: PlaybackLoadMode? = nil,
    targetRevision: Int64? = nil
  ) {
    self.operationId = operationId
    self.command = command
    self.item = item
    self.positionMilliseconds = positionMilliseconds
    self.mode = mode
    self.targetRevision = targetRevision
  }

  private enum CodingKeys: String, CodingKey {
    case operationId, command, item, positionMilliseconds, mode, targetRevision
  }

  public func encode(to encoder: Encoder) throws {
    guard (command == .load) == (mode != nil) else {
      throw EncodingError.invalidValue(
        command,
        .init(codingPath: encoder.codingPath, debugDescription: "Load commands require a mode and other commands forbid one"))
    }
    var values = encoder.container(keyedBy: CodingKeys.self)
    try values.encode(operationId, forKey: .operationId)
    try values.encode(command, forKey: .command)
    try values.encodeIfPresent(item, forKey: .item)
    try values.encodeIfPresent(positionMilliseconds, forKey: .positionMilliseconds)
    try values.encodeIfPresent(mode, forKey: .mode)
    try values.encodeIfPresent(targetRevision, forKey: .targetRevision)
  }
}

public enum PlaybackCommandStatus: String, Codable, Sendable, Equatable {
  case pending, applied, failed, expired
}

public struct PlaybackCommand: Codable, Sendable, Equatable, Identifiable {
  public var id: UUID { operationId }
  public let operationId: UUID
  public let command: PlaybackCommandKind
  public let mode: PlaybackLoadMode?
  public let targetRevision: Int64?
  public let item: CoordinatedPlaybackItem?
  public let positionMilliseconds: Int64?
  public let senderDeviceName: String
  public let status: PlaybackCommandStatus
  public let resultCode: PlaybackCommandResultCode?
  public let createdAt: String
  public let expiresAt: String

  public init(from decoder: Decoder) throws {
    let values = try decoder.container(keyedBy: CodingKeys.self)
    operationId = try values.decode(UUID.self, forKey: .operationId)
    command = try values.decode(PlaybackCommandKind.self, forKey: .command)
    mode = try values.decodeIfPresent(PlaybackLoadMode.self, forKey: .mode)
    targetRevision = try values.decodeIfPresent(Int64.self, forKey: .targetRevision)
    item = try values.decodeIfPresent(CoordinatedPlaybackItem.self, forKey: .item)
    positionMilliseconds = try values.decodeIfPresent(Int64.self, forKey: .positionMilliseconds)
    senderDeviceName = try values.decode(String.self, forKey: .senderDeviceName)
    status = try values.decode(PlaybackCommandStatus.self, forKey: .status)
    resultCode = try values.decodeIfPresent(PlaybackCommandResultCode.self, forKey: .resultCode)
    createdAt = try values.decode(String.self, forKey: .createdAt)
    expiresAt = try values.decode(String.self, forKey: .expiresAt)
    guard (status == .pending) == (resultCode == nil) else {
      throw DecodingError.dataCorruptedError(
        forKey: .resultCode, in: values,
        debugDescription: "Pending commands omit resultCode and terminal commands require one")
    }
  }

  private enum CodingKeys: String, CodingKey {
    case operationId, command, mode, targetRevision, item, positionMilliseconds
    case senderDeviceName, status, resultCode, createdAt, expiresAt
  }
}

public struct PlaybackCommandList: Codable, Sendable, Equatable {
  public let commands: [PlaybackCommand]
}

public enum PlaybackCommandResultStatus: String, Codable, Sendable, Equatable {
  case applied, failed, expired
}

public enum PlaybackCommandResultCode: String, Codable, Sendable, Equatable {
  case applied, unsupported
  case invalidState = "invalid_state"
  case staleTarget = "stale_target"
  case expired
  case executionFailed = "execution_failed"
}

public struct PlaybackCommandResultInput: Codable, Sendable, Equatable {
  public let status: PlaybackCommandResultStatus
  public let code: PlaybackCommandResultCode
  public init(status: PlaybackCommandResultStatus, code: PlaybackCommandResultCode) {
    self.status = status
    self.code = code
  }

  public func encode(to encoder: Encoder) throws {
    let valid = (status == .applied && code == .applied)
      || (status == .expired && code == .expired)
      || (status == .failed && ![.applied, .expired].contains(code))
    guard valid else {
      throw EncodingError.invalidValue(
        code,
        .init(codingPath: encoder.codingPath, debugDescription: "Command result status and code do not match"))
    }
    var values = encoder.container(keyedBy: CodingKeys.self)
    try values.encode(status, forKey: .status)
    try values.encode(code, forKey: .code)
  }

  private enum CodingKeys: String, CodingKey { case status, code }
}

public typealias PlaybackCommandResult = PlaybackCommand
public typealias OutgoingPlaybackCommand = PlaybackCommand

public struct PlaybackRoomCreateInput: Codable, Sendable, Equatable {
  public let item: CoordinatedPlaybackItem
  public let state: String
  public let positionMilliseconds: Int64
  public let durationMilliseconds: Int64
  public init(
    item: CoordinatedPlaybackItem, state: String, positionMilliseconds: Int64,
    durationMilliseconds: Int64
  ) {
    self.item = item
    self.state = state
    self.positionMilliseconds = positionMilliseconds
    self.durationMilliseconds = durationMilliseconds
  }
}

public struct PlaybackRoomJoinInput: Codable, Sendable, Equatable {
  public let code: String
  public init(code: String) { self.code = code }
}

public struct PlaybackRoomUpdateInput: Codable, Sendable, Equatable {
  public let state: String
  public let positionMilliseconds: Int64
  public let durationMilliseconds: Int64
  public let expectedVersion: Int64
  public init(
    state: String, positionMilliseconds: Int64, durationMilliseconds: Int64, expectedVersion: Int64
  ) {
    self.state = state
    self.positionMilliseconds = positionMilliseconds
    self.durationMilliseconds = durationMilliseconds
    self.expectedVersion = expectedVersion
  }
}

public struct PlaybackRoomMember: Codable, Sendable, Equatable, Identifiable {
  public var id: UUID { memberId }
  public let memberId: UUID
  public let profile: String
  public let deviceName: String
  public let platform: String
  public let role: String
  public let current: Bool
  public let joinedAt: String
  public let lastSeenAt: String
}

public struct PlaybackRoom: Codable, Sendable, Equatable, Identifiable {
  public let id: UUID
  public let joinCode: String?
  public let item: CoordinatedPlaybackItem
  public let state: String
  public let positionMilliseconds: Int64
  public let durationMilliseconds: Int64
  public let version: Int64
  public let updatedAt: String
  public let expiresAt: String
  public let members: [PlaybackRoomMember]

  public var currentMember: PlaybackRoomMember? { members.first(where: \.current) }
  public var currentMemberIsHost: Bool { currentMember?.role == "host" }

  public func preservingJoinCode(from previous: PlaybackRoom) -> PlaybackRoom {
    guard id == previous.id, joinCode == nil, let joinCode = previous.joinCode else { return self }
    return PlaybackRoom(
      id: id,
      joinCode: joinCode,
      item: item,
      state: state,
      positionMilliseconds: positionMilliseconds,
      durationMilliseconds: durationMilliseconds,
      version: version,
      updatedAt: updatedAt,
      expiresAt: expiresAt,
      members: members
    )
  }
}

public enum RecommendationArtworkShape: String, Sendable, Equatable {
  case poster, landscape
}

public struct LocalRecommendationTitle: Codable, Sendable, Equatable, Identifiable {
  public let id: UUID
  public let mediaType: String
  public let title: String?
  public let posterUrl: String?
  public let backgroundUrl: String?
  public let releaseInfo: String?
  public let resourceId: String?
  public let resourceProvider: String?
  public let sourceAddonId: UUID?
  public let providerIds: [String: String]
}

public struct LocalRecommendation: Codable, Sendable, Equatable, Identifiable {
  public var id: UUID { item.id }
  public let item: LocalRecommendationTitle
  public let reason: String
  public let score: Double
}

public struct LocalRecommendationPage: Codable, Sendable, Equatable {
  public let items: [LocalRecommendation]
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
  public let mediaTimeline: PlaybackMediaTimeline?
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
  public let reason: PlaybackDecisionReason
  public let reasons: [PlaybackDecisionConstraint]
  public let videoAction: PlaybackDecisionAction
  public let audioAction: PlaybackDecisionAction
  public let subtitleAction: PlaybackSubtitleAction
  public let toneMapping: Bool
  public let source: PlaybackDecisionSource?
  public let target: PlaybackDecisionTarget?

  private enum CodingKeys: String, CodingKey {
    case reason, reasons, videoAction, audioAction, subtitleAction, toneMapping, source, target
  }

  public init(from decoder: Decoder) throws {
    let values = try decoder.container(keyedBy: CodingKeys.self)
    reason = try values.decode(PlaybackDecisionReason.self, forKey: .reason)
    reasons = try values.decode([PlaybackDecisionConstraint].self, forKey: .reasons)
    guard reasons.count <= 7, Set(reasons).count == reasons.count,
      reason != .directSupported || reasons.isEmpty
    else {
      throw DecodingError.dataCorruptedError(
        forKey: .reasons, in: values,
        debugDescription: "Playback decision reasons must be unique, bounded, and empty for direct play")
    }
    videoAction = try values.decode(PlaybackDecisionAction.self, forKey: .videoAction)
    audioAction = try values.decode(PlaybackDecisionAction.self, forKey: .audioAction)
    subtitleAction = try values.decode(PlaybackSubtitleAction.self, forKey: .subtitleAction)
    toneMapping = try values.decode(Bool.self, forKey: .toneMapping)
    source = try values.decodeIfPresent(PlaybackDecisionSource.self, forKey: .source)
    target = try values.decodeIfPresent(PlaybackDecisionTarget.self, forKey: .target)
  }
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
  public let videoBitDepth: Int?
  public let videoBitrateKbps: Int?
}

public struct PlaybackMediaTrack: Codable, Sendable, Equatable {
  public let index: Int
  public let type: PlaybackMediaTrackType
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
  public let delivery: PlaybackSubtitleDelivery?
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
  public let sessionsTruncated: Bool
  public let jobsTruncated: Bool
}

public struct PlaybackActivitySummary: Codable, Sendable, Equatable {
  public let activeSessions: Int
  public let activeJobs: Int
  public let processingSlots: Int
  public let processingLimit: Int
  public let storageBytes: Int64
  public let storageLimitBytes: Int64
}

public enum PlaybackHardwareAcceleration: String, Codable, Sendable, Equatable {
  case unknown, auto, software, hybrid, vaapi, qsv, nvenc, amf
}

public enum PlaybackVideoCodec: String, Codable, Sendable, Equatable {
  case h264, hevc, av1
}

public enum PlaybackPreferredVideoCodec: String, Codable, Sendable, Equatable {
  case auto, h264, hevc, av1
}

public enum PlaybackQualityPreset: String, Codable, Sendable, Equatable {
  case speed, balanced, quality
}

public enum PlaybackToneMapBackend: String, Codable, Sendable, Equatable {
  case vulkan, vaapi, hybrid, software
}

public struct PlaybackMediaProcessTotals: Codable, Sendable, Equatable {
  public let started: Int64
  public let succeeded: Int64
  public let failed: Int64
  public let softwareFallbacks: Int64
}

public struct PlaybackMediaDiagnosticPool: Codable, Sendable, Equatable {
  public let active: Int
  public let limit: Int
}

public struct PlaybackMediaDiagnosticPools: Codable, Sendable, Equatable {
  public let process: PlaybackMediaDiagnosticPool
  public let probe: PlaybackMediaDiagnosticPool
  public let subtitle: PlaybackMediaDiagnosticPool
  public let trickplay: PlaybackMediaDiagnosticPool
}

public struct PlaybackMediaDiagnostics: Codable, Sendable, Equatable {
  public let ffmpegVersion: String
  public let ffprobeVersion: String
  public let hardwareAcceleration: PlaybackHardwareAcceleration
  public let videoEncoder: String
  public let preferredVideoCodec: PlaybackPreferredVideoCodec
  public let encodeCodecs: [PlaybackVideoCodec]
  public let decodeCodecs: [PlaybackVideoCodec]
  public let hevcMain10: Bool?
  public let qualityPreset: PlaybackQualityPreset
  public let hardwareToneMap: Bool
  public let toneMapBackend: PlaybackToneMapBackend
  public let transcodeThreads: Int
  public let maximumReadRate: Double
  public let totals: PlaybackMediaProcessTotals
  public let pools: PlaybackMediaDiagnosticPools
}

public enum PlaybackActivityMode: String, Codable, Sendable, Equatable {
  case direct, remux
  case transcodeAudio = "transcode_audio"
  case transcode, unknown
}

public struct PlaybackActivityExternalIDs: Codable, Sendable, Equatable {
  public let imdb: String?
  public let tmdb: String?
  public let tvdb: String?
}

public struct PlaybackActivityExternalIDMediaTypes: Codable, Sendable, Equatable {
  public let imdb: MediaType?
  public let tmdb: MediaType?
  public let tvdb: MediaType?
}

public struct PlaybackActivitySession: Codable, Sendable, Equatable, Identifiable {
  public let id: UUID
  public let titleId: String?
  public let artworkUrl: String?
  public let externalIds: PlaybackActivityExternalIDs?
  public let externalIdMediaTypes: PlaybackActivityExternalIDMediaTypes?
  public let title: String
  public let mediaType: String
  public let mode: PlaybackActivityMode
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

public enum PlaybackJobState: String, Codable, Sendable, Equatable {
  case processing, complete, failed
}

public enum PlaybackJobErrorClass: String, Codable, Sendable, Equatable {
  case capacity, source, processing, storage, timeout, cancelled, unknown
}

public struct PlaybackMediaJob: Codable, Sendable, Equatable {
  public let sessionId: UUID?
  public let assetId: String
  public let mode: String
  public let state: PlaybackJobState
  public let errorClass: PlaybackJobErrorClass?
  public let prewarming: Bool
  public let progressPercent: Double?
  public let speed: Double?
  public let startupDurationSeconds: Double?
  public let createdAt: String
  public let lastSeenAt: String
}

public enum PlaybackMarkerType: String, Codable, Sendable, Equatable {
  case intro, recap, outro
}

public struct PlaybackMarker: Codable, Sendable, Equatable {
  public let type: PlaybackMarkerType
  public let startSeconds: Double
  public let endSeconds: Double
  public let confidence: Double
  public let submissionCount: Int
}

public struct PlaybackMarkerList: Codable, Sendable, Equatable {
  public let markers: [PlaybackMarker]
}

public enum PlaybackProgressMediaType: String, Codable, Sendable, Equatable {
  case movie, episode
}

public struct PlaybackProgress: Codable, Sendable, Equatable {
  public let titleId: UUID
  public let mediaType: PlaybackProgressMediaType
  public let positionSeconds: Int
  public let durationSeconds: Int
  public let completed: Bool
  public let version: Int64
  public let lastWatchedAt: String
  public let updatedAt: String
}

public struct PlaybackProgressBatchRequest: Codable, Sendable, Equatable {
  public let titleIds: [UUID]

  public init(titleIds: [UUID]) { self.titleIds = titleIds }
}

public struct PlaybackProgressBatchItem: Codable, Sendable, Equatable {
  public let titleId: UUID
  public let progress: PlaybackProgress?

  public init(from decoder: Decoder) throws {
    let values = try decoder.container(keyedBy: CodingKeys.self)
    titleId = try values.decode(UUID.self, forKey: .titleId)
    progress = try values.decodeRequiredNullable(PlaybackProgress.self, forKey: .progress)
  }

  private enum CodingKeys: String, CodingKey { case titleId, progress }
}

public struct PlaybackProgressBatch: Codable, Sendable, Equatable {
  public let items: [PlaybackProgressBatchItem]
}

public struct UpdatePlaybackProgressRequest: Codable, Sendable, Equatable {
  public let positionSeconds: Int
  public let durationSeconds: Int
  public let completed: Bool
  public let expectedVersion: Int64

  public init(positionSeconds: Int, durationSeconds: Int, completed: Bool, expectedVersion: Int64) {
    self.positionSeconds = positionSeconds
    self.durationSeconds = durationSeconds
    self.completed = completed
    self.expectedVersion = expectedVersion
  }
}

public struct CompletionRequest: Codable, Sendable, Equatable {
  public let expectedVersion: Int64

  public init(expectedVersion: Int64) { self.expectedVersion = expectedVersion }
}

public struct SetWatchedBatchItem: Codable, Sendable, Equatable {
  public let titleId: UUID
  public let completed: Bool
  public let expectedVersion: Int64

  public init(titleId: UUID, completed: Bool, expectedVersion: Int64) {
    self.titleId = titleId
    self.completed = completed
    self.expectedVersion = expectedVersion
  }
}

public struct SetWatchedBatchRequest: Codable, Sendable, Equatable {
  public let items: [SetWatchedBatchItem]

  public init(items: [SetWatchedBatchItem]) { self.items = items }
}

public struct SetWatchedBatchResultItem: Codable, Sendable, Equatable {
  public let titleId: UUID
  public let progress: PlaybackProgress
}

public struct SetWatchedBatchResult: Codable, Sendable, Equatable {
  public let items: [SetWatchedBatchResultItem]
}

public enum ContinueWatchingReason: String, Codable, Sendable, Equatable {
  case resume
  case nextEpisode = "next_episode"
}

public struct ContinueWatchingItem: Codable, Sendable, Equatable, Identifiable {
  public var id: UUID { titleId }
  public let titleId: UUID
  public let mediaType: PlaybackProgressMediaType
  public let seriesId: UUID?
  public let seasonId: UUID?
  public let seasonNumber: Int?
  public let episodeNumber: Int?
  public let mappingProvider: String?
  public let episodeOrderId: String?
  public let metadataSeasonId: String?
  public let title: String?
  public let posterUrl: String?
  public let backgroundUrl: String?
  public let releaseInfo: String?
  public let resourceId: String?
  public let resourceProvider: String?
  public let episodeTitle: String?
  public let episodeStillUrl: String?
  public let episodeAirDate: String?
  public let positionSeconds: Int
  public let durationSeconds: Int
  public let version: Int64
  public let reason: ContinueWatchingReason
  public let lastWatchedAt: String
}

public struct ContinueWatchingPage: Codable, Sendable, Equatable {
  public let items: [ContinueWatchingItem]
}

public enum JSONValue: Codable, Sendable, Equatable {
  case boolean(Bool)
  case signedInteger(Int64)
  case unsignedInteger(UInt64)
  case floatingPoint(Double)
  case string(String)
  case array([JSONValue])
  case object([String: JSONValue])
  case null

  public init(from decoder: Decoder) throws {
    let container = try decoder.singleValueContainer()
    if container.decodeNil() {
      self = .null
    } else if let value = try? container.decode(Bool.self) {
      self = .boolean(value)
    } else if let value = try? container.decode(Int64.self) {
      self = .signedInteger(value)
    } else if let value = try? container.decode(UInt64.self) {
      self = .unsignedInteger(value)
    } else if let value = try? container.decode(Double.self) {
      self = .floatingPoint(value)
    } else if let value = try? container.decode(String.self) {
      self = .string(value)
    } else if let value = try? container.decode([JSONValue].self) {
      self = .array(value)
    } else if let value = try? container.decode([String: JSONValue].self) {
      self = .object(value)
    } else {
      throw DecodingError.typeMismatch(
        JSONValue.self,
        .init(codingPath: decoder.codingPath, debugDescription: "Unsupported JSON value"))
    }
  }

  public func encode(to encoder: Encoder) throws {
    var container = encoder.singleValueContainer()
    switch self {
    case .boolean(let value): try container.encode(value)
    case .signedInteger(let value): try container.encode(value)
    case .unsignedInteger(let value): try container.encode(value)
    case .floatingPoint(let value): try container.encode(value)
    case .string(let value): try container.encode(value)
    case .array(let value): try container.encode(value)
    case .object(let value): try container.encode(value)
    case .null: try container.encodeNil()
    }
  }
}

public enum ProfileArchiveError: Error, LocalizedError, Equatable {
  case tooLarge
  case invalidDocument
  case unsupportedVersion

  public var errorDescription: String? {
    switch self {
    case .tooLarge: return "The profile archive exceeds the 16 MiB limit."
    case .invalidDocument: return "The profile archive is invalid."
    case .unsupportedVersion: return "This profile archive version is not supported."
    }
  }
}

public struct ProfileArchiveDocument: Codable, Sendable, Equatable {
  public static let version = 2
  public static let maximumBytes = 16 * 1_024 * 1_024
  public let value: JSONValue

  public init(data: Data) throws {
    guard data.count <= Self.maximumBytes else { throw ProfileArchiveError.tooLarge }
    let decoded: JSONValue
    do { decoded = try JSONDecoder().decode(JSONValue.self, from: data) }
    catch { throw ProfileArchiveError.invalidDocument }
    guard case .object(let object) = decoded else { throw ProfileArchiveError.invalidDocument }
    guard let versionValue = object["version"] else { throw ProfileArchiveError.invalidDocument }
    guard case .signedInteger(let version) = versionValue, version == Int64(Self.version) else {
      throw ProfileArchiveError.unsupportedVersion
    }
    guard let identityValue = object["identity"], case .object = identityValue else {
      throw ProfileArchiveError.invalidDocument
    }
    value = decoded
  }

  public init(from decoder: Decoder) throws {
    let decoded = try JSONValue(from: decoder)
    let data = try JSONEncoder().encode(decoded)
    try self.init(data: data)
  }

  public func encode(to encoder: Encoder) throws { try value.encode(to: encoder) }

  public func encodedData() throws -> Data {
    let data = try JSONEncoder().encode(value)
    guard data.count <= Self.maximumBytes else { throw ProfileArchiveError.tooLarge }
    return data
  }
}

public enum ProfileArchiveImportMode: String, Codable, Sendable, Equatable {
  case merge, create
}

public struct ProfileArchiveSectionReport: Codable, Sendable, Equatable, Identifiable {
  public var id: String { section }
  public let section: String
  public let created: Int
  public let updated: Int
  public let unchanged: Int
}

public struct ProfileArchiveImportReport: Codable, Sendable, Equatable {
  public let mode: ProfileArchiveImportMode
  public let profileId: UUID
  public let sections: [ProfileArchiveSectionReport]
  public let trackingAccountsUpdated: Int
}

public struct ProfileArchiveCreateInput: Codable, Sendable, Equatable {
  public let categoryId: UUID
  public let archive: ProfileArchiveDocument
  public init(categoryId: UUID, archive: ProfileArchiveDocument) {
    self.categoryId = categoryId
    self.archive = archive
  }
}

public enum CollectionViewMode: String, Codable, Sendable, Equatable {
  case tabbedGrid = "tabbed_grid"
  case rows
  case followLayout = "follow_layout"
}
public enum CollectionTileShape: String, Codable, Sendable, Equatable {
  case poster, landscape, square
}
public enum CollectionSourceView: String, Codable, Sendable, Equatable {
  case merged, categories, folders
}
public enum CollectionSourceKind: String, Codable, Sendable, Equatable {
  case addonCatalog = "addon_catalog"
  case tmdb, trakt, mdblist
}

public struct CollectionList: Codable, Sendable, Equatable { public let collections: [Collection] }
public struct Collection: Codable, Sendable, Equatable, Identifiable {
  public let id: UUID
  public let title: String
  public let backdropImageUrl: String?
  public let heroEnabled: Bool
  public let pinToTop: Bool
  public let focusGlowEnabled: Bool
  public let viewMode: CollectionViewMode
  public let folderCoverShape: CollectionTileShape
  public let folders: [CollectionFolder]
  public let profileIds: [UUID]
  public let categoryIds: [UUID]
  public let position: Int
  public let version: Int
  public let createdAt: String
  public let updatedAt: String
}

public struct CollectionFolder: Codable, Sendable, Equatable, Identifiable {
  public let id: UUID?
  public let title: String
  public let tileShape: CollectionTileShape
  public let sourceView: CollectionSourceView?
  public let coverImageUrl: String?
  public let coverEmoji: String?
  public let titleLogoUrl: String?
  public let heroBackdropUrl: String?
  public let heroVideoUrl: String?
  public let focusGifUrl: String?
  public let focusGifEnabled: Bool
  public let hideTitle: Bool
  public let sources: [CollectionSource]
}

public struct CollectionSource: Codable, Sendable, Equatable, Identifiable {
  public let id: UUID?
  public let kind: CollectionSourceKind
  public let title: String
  public let addonCatalog: CollectionAddonCatalogSource?
  public let tmdb: CollectionTMDBSource?
  public let trakt: CollectionTraktSource?
  public let mdblist: CollectionMDBListSource?
}

public struct CollectionAddonCatalogSource: Codable, Sendable, Equatable {
  public let addonId: UUID
  public let manifestId: String?
  public let type: String
  public let catalogId: String
  public let extra: [CollectionExtraValue]?
}
public struct CollectionExtraValue: Codable, Sendable, Equatable {
  public let name: String
  public let value: String
}
public enum CollectionTMDBSourceType: String, Codable, Sendable, Equatable {
  case list, company, network, collection, person, director, discover
}
public enum CollectionTMDBMediaType: String, Codable, Sendable, Equatable {
  case movie, series, both
}
public enum CollectionTMDBSort: String, Codable, Sendable, Equatable {
  case original
  case popularityDescending = "popularity.desc"
  case voteAverageDescending = "vote_average.desc"
  case voteCountDescending = "vote_count.desc"
  case releaseDateDescending = "release_date.desc"
  case firstAirDateDescending = "first_air_date.desc"
}
public struct CollectionTMDBSource: Codable, Sendable, Equatable {
  public let sourceType: CollectionTMDBSourceType
  public let tmdbId: Int64?
  public let mediaType: CollectionTMDBMediaType
  public let sort: CollectionTMDBSort
  public let filters: CollectionTMDBFilters
}
public struct CollectionTMDBFilters: Codable, Sendable, Equatable {
  public let genres: [Int64]?
  public let releaseDateFrom: String?
  public let releaseDateTo: String?
  public let voteAverageMin: Double?
  public let voteAverageMax: Double?
  public let voteCountMin: Int?
  public let originalLanguage: String?
  public let originCountry: String?
  public let keywords: [Int64]?
  public let companies: [Int64]?
  public let networks: [Int64]?
  public let year: Int?
  public let watchRegion: String?
  public let watchProviders: [Int64]?
}
public enum CollectionTraktMediaType: String, Codable, Sendable, Equatable { case movie, series }
public enum CollectionTraktSort: String, Codable, Sendable, Equatable {
  case rank, added, title, released, runtime, popularity, percentage, votes
}
public enum CollectionSortOrder: String, Codable, Sendable, Equatable { case asc, desc }
public struct CollectionTraktSource: Codable, Sendable, Equatable {
  public let listId: Int64
  public let mediaType: CollectionTraktMediaType
  public let sortBy: CollectionTraktSort
  public let sortHow: CollectionSortOrder
}
public enum CollectionMDBListSort: String, Codable, Sendable, Equatable {
  case added, budget, imdbpopular, imdbrating, imdbvotes
  case lastAirDate = "last_air_date"
  case letterrating, lettervotes, metacritic, myanimelist, random, rank, released, releasedigital,
    revenue, rogerebert, rtaudience, rtomatoes, runtime, score
  case scoreAverage = "score_average"
  case sortTitle = "sort_title"
  case title, tmdbpopular, usort
}
public struct CollectionMDBListSource: Codable, Sendable, Equatable {
  public let listId: Int64
  public let mediaType: CollectionTraktMediaType
  public let sort: CollectionMDBListSort
  public let order: CollectionSortOrder
}

public struct ResolvedCollectionFolder: Codable, Sendable, Equatable {
  public let collectionId: UUID
  public let folder: CollectionFolder
  public let sourcePosterUrls: [String: String]?
  public let items: [CollectionItem]
  public let page: Int
  public let hasMore: Bool
  public let errors: [CollectionSourceFailure]
}
public struct CollectionItem: Codable, Sendable, Equatable, Identifiable {
  public let id: String
  public let mediaType: String
  public let title: String
  public let posterUrl: String?
  public let backgroundUrl: String?
  public let logoUrl: String?
  public let description: String?
  public let releaseInfo: String?
  public let released: String?
  public let voteAverage: Double?
  public let voteCount: Int?
  public let popularity: Double?
  public let externalIds: [String: String]
  public let sources: [CollectionSourceReference]
  public let raw: JSONValue?
}

public struct SemanticSearchRequest: Codable, Sendable, Equatable {
  public let query: String
  public let mediaType: String?
  public let language: String?
  public let region: String?
  public let page: Int
  public let limit: Int
  public let excludedIntentIds: [String]

  public init(
    query: String, mediaType: String? = nil, language: String? = nil, region: String? = nil,
    page: Int = 1, limit: Int = 24, excludedIntentIds: [String] = []
  ) {
    self.query = query
    self.mediaType = mediaType
    self.language = language
    self.region = region
    self.page = page
    self.limit = limit
    self.excludedIntentIds = excludedIntentIds
  }
}

public struct SemanticSearchIntent: Codable, Sendable, Equatable, Identifiable {
  public let id: String
  public let kind: String
  public let value: String
  public let label: String
}

public struct SemanticSearchPage: Codable, Sendable, Equatable {
  public let intents: [SemanticSearchIntent]
  public let titleQuery: String
  public let mediaTypes: [String]
  public let items: [CollectionItem]
  public let page: Int
  public let hasMore: Bool
  public let partial: Bool
}
public struct CollectionSourceReference: Codable, Sendable, Equatable, Identifiable {
  public let id: UUID
  public let kind: CollectionSourceKind
  public let title: String
  public let addonId: UUID?
  public let manifestId: String?
  public let catalogId: String?
}
public enum CollectionSourceFailureCode: String, Codable, Sendable, Equatable {
  case providerUnavailable = "collection_provider_unavailable"
  case addonNotFound = "collection_addon_not_found"
  case sourceUnsupported = "collection_source_unsupported"
  case sourceTimeout = "collection_source_timeout"
  case sourceFailed = "collection_source_failed"
}
public struct CollectionSourceFailure: Codable, Sendable, Equatable {
  public let sourceId: UUID
  public let kind: CollectionSourceKind
  public let code: CollectionSourceFailureCode
  public let message: String
}

public struct StremioExtraProperty: Codable, Sendable, Equatable {
  public let name: String
  public let isRequired: Bool?
  public let `default`: String?
  public let options: [String]?
  public let optionsLimit: Int?
}
public struct StremioManifestCatalog: Codable, Sendable, Equatable, Identifiable {
  public let type: String
  public let id: String
  public let name: String?
  public let genres: [String]?
  public let extra: [StremioExtraProperty]?
  public let extraRequired: [String]?
  public let extraSupported: [String]?
}
public struct AddonCatalogDescriptorList: Codable, Sendable, Equatable {
  public let catalogs: [AddonCatalogDescriptor]
}
public struct AddonCatalogDescriptor: Codable, Sendable, Equatable {
  public let addonId: UUID
  public let addonName: String?
  public let addonLogoUrl: String?
  public let manifestId: String
  public let position: Int
  public let catalog: StremioManifestCatalog
  public let addonCatalog: Bool
  public let searchable: Bool
}
public struct AddonCachePolicy: Codable, Sendable, Equatable {
  public let maxAgeSeconds: Int64?
  public let staleWhileRevalidateSeconds: Int64?
  public let staleIfErrorSeconds: Int64?
}
public struct AddonExtraValue: Codable, Sendable, Equatable {
  public let name: String
  public let value: String
  public init(name: String, value: String) {
    self.name = name
    self.value = value
  }
}
public struct AddonResourceResult: Codable, Sendable, Equatable {
  public let addonId: UUID
  public let manifestId: String
  public let resource: String
  public let type: String
  public let id: String
  public let payload: [String: JSONValue]
  public let cache: AddonCachePolicy
  public let extra: [AddonExtraValue]?
}
public struct AddonResourceFailure: Codable, Sendable, Equatable {
  public let addonId: UUID
  public let manifestId: String
  public let code: String
  public let message: String
}
public struct AddonResourceBatch: Codable, Sendable, Equatable {
  public let results: [AddonResourceResult]
  public let errors: [AddonResourceFailure]
}

public struct CalendarEventList: Codable, Sendable, Equatable {
  public let events: [CalendarEvent]
}

public struct CalendarEvent: Codable, Sendable, Equatable, Identifiable {
  public let id: String
  public let titleId: UUID
  public let mediaType: String
  public let title: String
  public let releaseDate: String
  public let posterUrl: String?
  public let resourceId: String?
  public let resourceProvider: String?
  public let seriesTitle: String?
  public let seriesId: UUID?
  public let seasonId: UUID?
  public let seasonNumber: Int?
  public let episodeNumber: Int?
}

public enum TitleMediaType: String, Codable, Sendable, Equatable { case movie, series, tv }
private func normalizedTitleReleaseDate(_ value: String?) -> String? {
  guard let normalized = value?.trimmingCharacters(in: .whitespacesAndNewlines),
    normalized.count >= 10
  else { return nil }
  let dateOnly = String(normalized.prefix(10))
  let dateFormatter = DateFormatter()
  dateFormatter.calendar = Calendar(identifier: .gregorian)
  dateFormatter.locale = Locale(identifier: "en_US_POSIX")
  dateFormatter.timeZone = TimeZone(secondsFromGMT: 0)
  dateFormatter.dateFormat = "yyyy-MM-dd"
  dateFormatter.isLenient = false
  guard let date = dateFormatter.date(from: dateOnly), dateFormatter.string(from: date) == dateOnly
  else { return nil }
  guard normalized.count > 10 else { return dateOnly }
  guard normalized[normalized.index(normalized.startIndex, offsetBy: 10)] == "T" else { return nil }
  let isoFormatter = ISO8601DateFormatter()
  isoFormatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
  if isoFormatter.date(from: normalized) == nil {
    isoFormatter.formatOptions = [.withInternetDateTime]
    guard isoFormatter.date(from: normalized) != nil else { return nil }
  }
  return dateOnly
}
public struct TitleResolveInput: Codable, Sendable, Equatable {
  public let mediaType: TitleMediaType
  public let provider: String
  public let externalId: String?
  public let resourceId: String
  public let title: String
  public let posterUrl: String?
  public let backgroundUrl: String?
  public let releaseInfo: String?
  public let released: String?
  public let sourceAddonId: UUID?
  public let sourceCatalogId: String?
  public let sourceName: String?
  public let country: String?
  public let language: String?
  public let category: String?
  public init(
    mediaType: TitleMediaType, provider: String, externalId: String? = nil, resourceId: String,
    title: String, posterUrl: String? = nil, backgroundUrl: String? = nil,
    releaseInfo: String? = nil, released: String? = nil, sourceAddonId: UUID? = nil,
    sourceCatalogId: String? = nil, sourceName: String? = nil, country: String? = nil,
    language: String? = nil, category: String? = nil
  ) {
    self.mediaType = mediaType
    self.provider = provider
    self.externalId = externalId
    self.resourceId = resourceId
    self.title = title
    self.posterUrl = posterUrl
    self.backgroundUrl = backgroundUrl
    self.releaseInfo = releaseInfo
    self.released = normalizedTitleReleaseDate(released)
    self.sourceAddonId = sourceAddonId
    self.sourceCatalogId = sourceCatalogId
    self.sourceName = sourceName
    self.country = country
    self.language = language
    self.category = category
  }
}
public struct TitleReference: Codable, Sendable, Equatable, Identifiable {
  public var id: UUID { titleId }
  public let titleId: UUID
  public let mediaType: TitleMediaType
  public let provider: String
  public let externalId: String
  public let resourceId: String
  public let title: String
  public let posterUrl: String?
  public let backgroundUrl: String?
  public let releaseInfo: String?
  public let sourceAddonId: UUID?
  public let sourceCatalogId: String?
  public let sourceName: String?
  public let country: String?
  public let language: String?
  public let category: String?
}

public struct CustomSeriesResolveInput: Codable, Sendable, Equatable {
  public let sourceAddonId: UUID
  public let sourceType: String
  public let series: CustomSeriesSnapshot
  public let videos: [CustomVideoSnapshot]
  public init(
    sourceAddonId: UUID, sourceType: String, series: CustomSeriesSnapshot,
    videos: [CustomVideoSnapshot]
  ) {
    self.sourceAddonId = sourceAddonId
    self.sourceType = sourceType
    self.series = series
    self.videos = videos
  }
}
public struct CustomSeriesSnapshot: Codable, Sendable, Equatable {
  public let resourceId: String
  public let title: String
  public let posterUrl: String?
  public let backgroundUrl: String?
  public let releaseInfo: String?
  public init(
    resourceId: String, title: String, posterUrl: String? = nil, backgroundUrl: String? = nil,
    releaseInfo: String? = nil
  ) {
    self.resourceId = resourceId
    self.title = title
    self.posterUrl = posterUrl
    self.backgroundUrl = backgroundUrl
    self.releaseInfo = releaseInfo
  }
}
public struct CustomVideoSnapshot: Codable, Sendable, Equatable {
  public let resourceId: String
  public let title: String?
  public let seasonNumber: Int
  public let episodeNumber: Int
  public let thumbnailUrl: String?
  public let backgroundUrl: String?
  public let releaseInfo: String?
  public let released: String?
  public init(
    resourceId: String, title: String? = nil, seasonNumber: Int, episodeNumber: Int,
    thumbnailUrl: String? = nil, backgroundUrl: String? = nil, releaseInfo: String? = nil,
    released: String? = nil
  ) {
    self.resourceId = resourceId
    self.title = title
    self.seasonNumber = seasonNumber
    self.episodeNumber = episodeNumber
    self.thumbnailUrl = thumbnailUrl
    self.backgroundUrl = backgroundUrl
    self.releaseInfo = releaseInfo
    self.released = released
  }
}
public struct CustomSeriesResolveResult: Codable, Sendable, Equatable {
  public let series: CustomSeriesReference
  public let seasons: [CustomSeasonReference]
  public let videos: [CustomVideoReference]
}
public struct CustomSeriesReference: Codable, Sendable, Equatable {
  public let titleId: UUID
  public let resourceId: String
}
public struct CustomSeasonReference: Codable, Sendable, Equatable {
  public let titleId: UUID
  public let seasonNumber: Int
}
public struct CustomVideoReference: Codable, Sendable, Equatable {
  public let titleId: UUID
  public let resourceId: String
  public let seasonTitleId: UUID
  public let seasonNumber: Int
  public let episodeNumber: Int
}

public struct LibraryItem: Codable, Sendable, Equatable, Identifiable {
  public var id: UUID { titleId }
  public let titleId: UUID
  public let mediaType: TitleMediaType
  public let provider: String?
  public let externalId: String?
  public let resourceId: String?
  public let title: String?
  public let posterUrl: String?
  public let backgroundUrl: String?
  public let releaseInfo: String?
  public let sourceAddonId: UUID?
  public let sourceCatalogId: String?
  public let sourceName: String?
  public let country: String?
  public let language: String?
  public let category: String?
  public let available: Bool
  public let addedAt: String
  public let updatedAt: String
}
public struct LibraryPage: Codable, Sendable, Equatable {
  public let items: [LibraryItem]
  public let page: Int
  public let totalPages: Int
  public let totalResults: Int
}
public struct TVLibraryIdentity: Codable, Sendable, Equatable {
  public let sourceAddonId: UUID
  public let resourceId: String
  public init(sourceAddonId: UUID, resourceId: String) {
    self.sourceAddonId = sourceAddonId
    self.resourceId = resourceId
  }
}
public struct TVLibraryMembershipRequest: Codable, Sendable, Equatable {
  public let identities: [TVLibraryIdentity]
  public init(identities: [TVLibraryIdentity]) { self.identities = identities }
}
public struct TVLibraryMembership: Codable, Sendable, Equatable {
  public let sourceAddonId: UUID
  public let resourceId: String
  public let titleId: UUID
}
public struct TVLibraryMembershipResult: Codable, Sendable, Equatable {
  public let items: [TVLibraryMembership]
}

public struct SessionNotificationList: Codable, Sendable, Equatable {
  public let notifications: [SessionNotification]
}
public struct SessionNotification: Codable, Sendable, Equatable, Identifiable {
  public let id: String
  public let message: String
  public let senderUsername: String
  public let createdAt: String
}

// MARK: - Protocol v22 profile experiences

public enum ReadingQueueMediaType: String, Codable, Sendable, Equatable, CaseIterable {
  case movie, series, episode, tv
}

public struct ReadingQueue: Codable, Sendable, Equatable {
  public let revision: Int64
  public let items: [ReadingQueueItem]
}

public struct ReadingQueueItem: Codable, Sendable, Equatable, Identifiable {
  public let id: UUID
  public let mediaType: ReadingQueueMediaType
  public let resourceId: String
  public let sourceAddonId: UUID?
  public let titleId: UUID?
  public let title: String
  public let posterUrl: String?
  public let position: Int
  public let createdAt: String
  public let updatedAt: String
}

public struct ReadingQueueMutationInput: Codable, Sendable, Equatable {
  public let operationId: UUID
  public let expectedRevision: Int64

  public init(operationId: UUID, expectedRevision: Int64) {
    self.operationId = operationId
    self.expectedRevision = expectedRevision
  }
}

public struct ReadingQueueAddInput: Codable, Sendable, Equatable {
  public let operationId: UUID
  public let expectedRevision: Int64
  public let mediaType: ReadingQueueMediaType
  public let resourceId: String
  public let sourceAddonId: UUID?
  public let titleId: UUID?
  public let title: String
  public let posterUrl: String?

  public init(
    operationId: UUID, expectedRevision: Int64, mediaType: ReadingQueueMediaType,
    resourceId: String, sourceAddonId: UUID? = nil, titleId: UUID? = nil,
    title: String, posterUrl: String? = nil
  ) {
    self.operationId = operationId
    self.expectedRevision = expectedRevision
    self.mediaType = mediaType
    self.resourceId = resourceId
    self.sourceAddonId = sourceAddonId
    self.titleId = titleId
    self.title = title
    self.posterUrl = posterUrl
  }
}

public struct ReadingQueueUpdateInput: Codable, Sendable, Equatable {
  public let operationId: UUID
  public let expectedRevision: Int64
  public let title: String
  public let posterUrl: String?

  public init(operationId: UUID, expectedRevision: Int64, title: String, posterUrl: String? = nil) {
    self.operationId = operationId
    self.expectedRevision = expectedRevision
    self.title = title
    self.posterUrl = posterUrl
  }
}

public struct ReadingQueueReorderInput: Codable, Sendable, Equatable {
  public let operationId: UUID
  public let expectedRevision: Int64
  public let itemIds: [UUID]

  public init(operationId: UUID, expectedRevision: Int64, itemIds: [UUID]) {
    self.operationId = operationId
    self.expectedRevision = expectedRevision
    self.itemIds = itemIds
  }
}

public struct ReadingQueueMutation: Codable, Sendable, Equatable {
  public let revision: Int64
  public let affectedItemId: UUID?
  public let duplicate: Bool?
}

public enum SavedSearchSort: String, Codable, Sendable, Equatable, CaseIterable {
  case relevance, title, year, rating, added
}
public enum SavedSearchMediaType: String, Codable, Sendable, Equatable, CaseIterable {
  case movie, series, season, episode, video, tv
}


public struct SavedSearch: Codable, Sendable, Equatable, Identifiable {
  public let id: UUID
  public let name: String
  public let query: String
  public let mediaType: SavedSearchMediaType?
  public let sort: SavedSearchSort
  public let revision: Int64
  public let createdAt: String
  public let updatedAt: String
}

public struct SavedSearchList: Codable, Sendable, Equatable {
  public let savedSearches: [SavedSearch]
}

public struct SavedSearchInput: Codable, Sendable, Equatable {
  public let name: String
  public let query: String
  public let mediaType: SavedSearchMediaType?
  public let sort: SavedSearchSort

  public init(name: String, query: String, mediaType: SavedSearchMediaType? = nil, sort: SavedSearchSort) {
    self.name = name
    self.query = query
    self.mediaType = mediaType
    self.sort = sort
  }
}

public struct SavedSearchUpdateInput: Codable, Sendable, Equatable {
  public let name: String
  public let query: String
  public let mediaType: SavedSearchMediaType?
  public let sort: SavedSearchSort
  public let expectedRevision: Int64

  public init(
    name: String, query: String, mediaType: SavedSearchMediaType? = nil, sort: SavedSearchSort,
    expectedRevision: Int64
  ) {
    self.name = name
    self.query = query
    self.mediaType = mediaType
    self.sort = sort
    self.expectedRevision = expectedRevision
  }
}

public enum SmartRule: Codable, Sendable, Equatable {
  case all([SmartRule])
  case any([SmartRule])
  case mediaType([String])
  case numeric(field: SmartNumericField, operator: SmartNumericOperator, number: Double)
  case text(field: SmartTextField, operator: SmartTextOperator, value: String)

  public enum SmartNumericField: String, Codable, Sendable, Equatable { case year, rating }
  public enum SmartNumericOperator: String, Codable, Sendable, Equatable { case equals, gte, lte }
  public enum SmartTextField: String, Codable, Sendable, Equatable { case genre, status, source }
  public enum SmartTextOperator: String, Codable, Sendable, Equatable {
    case equals
    case notEquals = "not_equals"
  }

  private enum CodingKeys: String, CodingKey { case type, rules, `operator`, values, number, value }

  public init(from decoder: Decoder) throws {
    let values = try decoder.container(keyedBy: CodingKeys.self)
    let type = try values.decode(String.self, forKey: .type)
    switch type {
    case "all": self = .all(try values.decode([SmartRule].self, forKey: .rules))
    case "any": self = .any(try values.decode([SmartRule].self, forKey: .rules))
    case "media_type":
      guard try values.decode(String.self, forKey: .operator) == "one_of" else {
        throw DecodingError.dataCorruptedError(forKey: .operator, in: values, debugDescription: "Unknown media type operator")
      }
      self = .mediaType(try values.decode([String].self, forKey: .values))
    case SmartNumericField.year.rawValue, SmartNumericField.rating.rawValue:
      guard let field = SmartNumericField(rawValue: type) else { fatalError("guarded") }
      self = .numeric(
        field: field, operator: try values.decode(SmartNumericOperator.self, forKey: .operator),
        number: try values.decode(Double.self, forKey: .number))
    case SmartTextField.genre.rawValue, SmartTextField.status.rawValue, SmartTextField.source.rawValue:
      guard let field = SmartTextField(rawValue: type) else { fatalError("guarded") }
      self = .text(
        field: field, operator: try values.decode(SmartTextOperator.self, forKey: .operator),
        value: try values.decode(String.self, forKey: .value))
    default:
      throw DecodingError.dataCorruptedError(forKey: .type, in: values, debugDescription: "Unknown smart rule type")
    }
  }

  public func encode(to encoder: Encoder) throws {
    var values = encoder.container(keyedBy: CodingKeys.self)
    switch self {
    case .all(let rules): try values.encode("all", forKey: .type); try values.encode(rules, forKey: .rules)
    case .any(let rules): try values.encode("any", forKey: .type); try values.encode(rules, forKey: .rules)
    case .mediaType(let mediaTypes):
      try values.encode("media_type", forKey: .type)
      try values.encode("one_of", forKey: .operator)
      try values.encode(mediaTypes, forKey: .values)
    case .numeric(let field, let operation, let number):
      try values.encode(field, forKey: .type)
      try values.encode(operation, forKey: .operator)
      try values.encode(number, forKey: .number)
    case .text(let field, let operation, let value):
      try values.encode(field, forKey: .type)
      try values.encode(operation, forKey: .operator)
      try values.encode(value, forKey: .value)
    }
  }
}

public enum SmartCollectionSort: String, Codable, Sendable, Equatable, CaseIterable {
  case title, year, rating, added
}

public struct SmartCollection: Codable, Sendable, Equatable, Identifiable {
  public let id: UUID
  public let name: String
  public let rules: SmartRule
  public let sort: SmartCollectionSort
  public let revision: Int64
  public let createdAt: String
  public let updatedAt: String
}

public struct SmartCollectionInput: Codable, Sendable, Equatable {
  public let name: String
  public let rules: SmartRule
  public let sort: SmartCollectionSort
  public init(name: String, rules: SmartRule, sort: SmartCollectionSort) {
    self.name = name; self.rules = rules; self.sort = sort
  }
}

public struct SmartCollectionUpdateInput: Codable, Sendable, Equatable {
  public let name: String
  public let rules: SmartRule
  public let sort: SmartCollectionSort
  public let expectedRevision: Int64
  public init(name: String, rules: SmartRule, sort: SmartCollectionSort, expectedRevision: Int64) {
    self.name = name; self.rules = rules; self.sort = sort; self.expectedRevision = expectedRevision
  }
}

public struct SmartCollectionList: Codable, Sendable, Equatable {
  public let smartCollections: [SmartCollection]
}

public struct SmartCollectionCatalogItem: Codable, Sendable, Equatable, Identifiable {
  public let id: UUID
  public let mediaType: String
  public let title: String
  public let posterUrl: String?
  public let backgroundUrl: String?
  public let releaseInfo: String?
  public let released: String?
  public let genres: [String]
  public let communityRating: Double?
  public let status: String?
  public let resourceId: String?
  public let resourceProvider: String?
  public let sourceAddonId: UUID?
  public let sourceCatalogId: String?
  public let sourceName: String?
}

public struct SmartCollectionPage: Codable, Sendable, Equatable {
  public let items: [SmartCollectionCatalogItem]
  public let page: Int
  public let pageSize: Int
  public let total: Int
  public let totalPages: Int
}

public enum AddonIncidentCode: String, Codable, Sendable, Equatable, CaseIterable {
  case timeout, unavailable
  case invalidResponse = "invalid_response"
  case unhealthy
}
public enum AddonIncidentState: String, Codable, Sendable, Equatable, CaseIterable { case open, recovering, resolved }
public enum AddonIncidentImpact: String, Codable, Sendable, Equatable { case availability; case responseIntegrity = "response_integrity" }
public enum AddonIncidentEventType: String, Codable, Sendable, Equatable { case opened, occurred, recovering, resolved, acknowledged }

public struct AddonIncident: Codable, Sendable, Equatable, Identifiable {
  public let id: UUID
  public let profileId: UUID
  public let addonId: UUID
  public let addonName: String
  public let code: AddonIncidentCode
  public let state: AddonIncidentState
  public let impact: AddonIncidentImpact
  public let occurrenceCount: Int
  public let firstOccurredAt: String
  public let lastOccurredAt: String
  public let lastSuccessAt: String?
  public let recoveryStartedAt: String?
  public let resolvedAt: String?
  public let acknowledgedAt: String?
  public let acknowledgedByUserId: UUID?
  public let updatedAt: String
}
public struct AddonIncidentList: Codable, Sendable, Equatable { public let incidents: [AddonIncident] }
public struct AddonIncidentEvent: Codable, Sendable, Equatable, Identifiable {
  public let id: Int64
  public let type: AddonIncidentEventType
  public let code: AddonIncidentCode
  public let occurredAt: String
}
public struct AddonIncidentDetail: Codable, Sendable, Equatable {
  public let incident: AddonIncident
  public let events: [AddonIncidentEvent]
}

public enum MediaNotificationKind: String, Codable, Sendable, Equatable, CaseIterable {
  case calendarEventUpcoming = "calendar-event-upcoming"
  case seasonAvailable = "season-available"
  case episodeAvailable = "episode-available"
  case movieRelease = "movie-release"
}
public enum MediaNotificationAcknowledgementState: String, Codable, Sendable, Equatable { case read, dismissed }
public struct MediaNotificationFollowInput: Codable, Sendable, Equatable {
  public let timezone: String
  public let horizonDays: Int
  public let leadDays: Int
  public init(timezone: String, horizonDays: Int, leadDays: Int) {
    self.timezone = timezone; self.horizonDays = horizonDays; self.leadDays = leadDays
  }
}
public struct MediaNotificationSubscription: Codable, Sendable, Equatable, Identifiable {
  public var id: UUID { titleId }
  public let titleId: UUID
  public let timezone: String
  public let horizonDays: Int
  public let leadDays: Int
  public let createdAt: String
  public let updatedAt: String
}
public struct MediaNotificationSubscriptions: Codable, Sendable, Equatable {
  public let subscriptions: [MediaNotificationSubscription]
}
public struct MediaNotification: Codable, Sendable, Equatable, Identifiable {
  public let id: String
  public let kind: MediaNotificationKind
  public let titleId: UUID
  public let subjectTitleId: UUID?
  public let title: String
  public let seriesTitle: String?
  public let releaseDate: String?
  public let seasonNumber: Int?
  public let episodeNumber: Int?
  public let availableAt: String
  public let readAt: String?
  public let createdAt: String
}
public struct MediaNotificationPage: Codable, Sendable, Equatable {
  public let notifications: [MediaNotification]
  public let nextCursor: String?
}
public struct MediaNotificationAcknowledgement: Codable, Sendable, Equatable {
  public let state: MediaNotificationAcknowledgementState
  public init(state: MediaNotificationAcknowledgementState) { self.state = state }
}

public enum AccessibilityReducedMotion: String, Codable, Sendable, Equatable, CaseIterable {
  case system, reduce
  case noPreference = "no-preference"
}
public enum AccessibilityContrast: String, Codable, Sendable, Equatable, CaseIterable { case system, more, standard }
public enum AccessibilityCaptions: String, Codable, Sendable, Equatable, CaseIterable { case system, on, off }
public enum AccessibilityFocusIndicators: String, Codable, Sendable, Equatable, CaseIterable { case standard, enhanced }
public struct AccessibilityPreferencesDocument: Codable, Sendable, Equatable {
  public let revision: Int64
  public let reducedMotion: AccessibilityReducedMotion
  public let highContrast: AccessibilityContrast
  public let textScale: Int
  public let captions: AccessibilityCaptions
  public let audioDescription: Bool
  public let focusIndicators: AccessibilityFocusIndicators

  public init(
    revision: Int64, reducedMotion: AccessibilityReducedMotion, highContrast: AccessibilityContrast,
    textScale: Int, captions: AccessibilityCaptions, audioDescription: Bool,
    focusIndicators: AccessibilityFocusIndicators
  ) {
    self.revision = revision; self.reducedMotion = reducedMotion; self.highContrast = highContrast
    self.textScale = textScale; self.captions = captions; self.audioDescription = audioDescription
    self.focusIndicators = focusIndicators
  }
}

public enum PlaybackFailoverError: String, Codable, Sendable, Equatable, CaseIterable {
  case sourceFailed = "source_failed"
  case sourceTimeout = "source_timeout"
  case endedEarly = "ended_early"
  case decodeFailed = "decode_failed"
  case accessDenied = "access_denied"
  case userCancelled = "user_cancelled"
}
public enum PlaybackFailoverStatus: String, Codable, Sendable, Equatable { case active, exhausted, cancelled }
public enum PlaybackFailoverCandidateStatus: String, Codable, Sendable, Equatable {
  case current, available
  case coolingDown = "cooling_down"
}
public struct PlaybackFailoverCreateInput: Codable, Sendable, Equatable {
  public let candidateSourceRefs: [String]
  public let selectedSourceRef: String
  public let maximumAttempts: Int?
  public init(candidateSourceRefs: [String], selectedSourceRef: String, maximumAttempts: Int? = nil) {
    self.candidateSourceRefs = candidateSourceRefs; self.selectedSourceRef = selectedSourceRef
    self.maximumAttempts = maximumAttempts
  }
}
public struct PlaybackFailoverAdvanceInput: Codable, Sendable, Equatable {
  public let error: PlaybackFailoverError
  public let positionSeconds: Double
  public let expectedRevision: Int64
  public init(error: PlaybackFailoverError, positionSeconds: Double, expectedRevision: Int64) {
    self.error = error; self.positionSeconds = positionSeconds; self.expectedRevision = expectedRevision
  }
}
public struct PlaybackFailoverCandidateHealth: Codable, Sendable, Equatable {
  public let position: Int
  public let status: PlaybackFailoverCandidateStatus
  public let cooldownUntil: String?
}
public struct PlaybackFailoverState: Codable, Sendable, Equatable, Identifiable {
  public let id: UUID
  public let currentSourceRef: String?
  public let currentPosition: Int
  public let positionSeconds: Double
  public let attemptCount: Int
  public let maximumAttempts: Int
  public let revision: Int64
  public let status: PlaybackFailoverStatus
  public let lastError: PlaybackFailoverError?
  public let explanation: String?
  public let candidateHealth: [PlaybackFailoverCandidateHealth]
  public let expiresAt: String
}
