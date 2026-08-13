import Foundation
#if canImport(Security)
import Security
#endif

public struct StoredCredentials: Codable, Sendable, Equatable {
    public let tokens: TokenPair
    public let profileContext: String?

    public init(tokens: TokenPair, profileContext: String?) {
        self.tokens = tokens
        self.profileContext = profileContext
    }
}

public protocol CredentialStore: Sendable {
    func load(for issuer: URL) async throws -> StoredCredentials?
    func save(_ credentials: StoredCredentials, for issuer: URL) async throws
    func clear(for issuer: URL) async throws
}

struct CredentialCleanupResult: @unchecked Sendable {
    let credentials: StoredCredentials?
    let error: Error?
}

actor OrderedCredentialStore {
    private let store: any CredentialStore
    private var generation: UInt64 = 0
    private var mutationTail: Task<Void, Never>?

    init(store: any CredentialStore) {
        self.store = store
    }

    func advance(to newGeneration: UInt64) {
        precondition(newGeneration > generation)
        generation = newGeneration
    }

    func load(for issuer: URL, generation expectedGeneration: UInt64) async throws -> StoredCredentials? {
        guard expectedGeneration == generation else { throw CancellationError() }
        let predecessor = mutationTail
        if let predecessor { await predecessor.value }
        guard expectedGeneration == generation else { throw CancellationError() }
        let value = try await store.load(for: issuer)
        guard expectedGeneration == generation else { throw CancellationError() }
        return value
    }

    func save(
        _ credentials: StoredCredentials,
        for issuer: URL,
        generation expectedGeneration: UInt64
    ) async throws -> Bool {
        guard expectedGeneration == generation else { return false }
        let predecessor = mutationTail
        let operation = Task {
            if let predecessor { await predecessor.value }
            guard self.isCurrent(expectedGeneration) else { return false }
            try await self.store.save(credentials, for: issuer)
            return self.isCurrent(expectedGeneration)
        }
        mutationTail = Task { _ = try? await operation.value }
        return try await operation.value
    }

    func clear(
        for issuer: URL,
        generation expectedGeneration: UInt64
    ) async throws -> Bool {
        guard expectedGeneration == generation else { return false }
        let predecessor = mutationTail
        let operation = Task {
            if let predecessor { await predecessor.value }
            guard self.isCurrent(expectedGeneration) else { return false }
            try await self.store.clear(for: issuer)
            return self.isCurrent(expectedGeneration)
        }
        mutationTail = Task { _ = try? await operation.value }
        return try await operation.value
    }

    func invalidateAndClear(
        for issuer: URL,
        generation newGeneration: UInt64,
        capturedCredentials: StoredCredentials?
    ) async -> CredentialCleanupResult {
        precondition(newGeneration > generation)
        generation = newGeneration

        let predecessor = mutationTail
        let operation = Task { () -> CredentialCleanupResult in
            if let predecessor { await predecessor.value }

            var credentials = capturedCredentials
            var firstError: Error?
            if credentials == nil {
                do {
                    credentials = try await self.store.load(for: issuer)
                } catch {
                    firstError = error
                }
            }
            do {
                try await self.store.clear(for: issuer)
            } catch {
                if firstError == nil { firstError = error }
            }
            return CredentialCleanupResult(credentials: credentials, error: firstError)
        }
        mutationTail = Task { _ = await operation.value }
        return await operation.value
    }

    private func isCurrent(_ expectedGeneration: UInt64) -> Bool {
        expectedGeneration == generation
    }
}

#if canImport(Security)
public enum CredentialStoreError: Error, LocalizedError, Sendable {
    case keychain(OSStatus)

    public var errorDescription: String? {
        switch self {
        case .keychain(let status):
            return SecCopyErrorMessageString(status, nil) as String? ?? "Keychain error \(status)"
        }
    }
}

public struct KeychainCredentialStore: CredentialStore {
    private struct PersistedCredentials: Codable {
        let issuer: String
        let credentials: StoredCredentials
    }
    private struct LegacyPersistedCredentials: Codable {
        let issuer: String
        let credentials: TokenPair
    }


    private let service: String

    public init(service: String = "io.rivune.api") {
        self.service = service
    }

    public func load(for issuer: URL) async throws -> StoredCredentials? {
        try removeLegacyCredential()
        let query = scopedQuery(for: issuer, returningData: true)
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let data = result as? Data else {
            throw CredentialStoreError.keychain(status)
        }
        let decoder = JSONDecoder()
        if let persisted = try? decoder.decode(PersistedCredentials.self, from: data),
           persisted.issuer == issuer.absoluteString {
            return persisted.credentials
        }
        if let legacy = try? decoder.decode(LegacyPersistedCredentials.self, from: data),
           legacy.issuer == issuer.absoluteString {
            let migrated = StoredCredentials(tokens: legacy.credentials, profileContext: nil)
            try await save(migrated, for: issuer)
            return migrated
        }
        try delete(scopedQuery(for: issuer))
        return nil
    }

    public func save(_ credentials: StoredCredentials, for issuer: URL) async throws {
        try removeLegacyCredential()
        let persisted = PersistedCredentials(issuer: issuer.absoluteString, credentials: credentials)
        let data = try JSONEncoder().encode(persisted)
        let baseQuery = scopedQuery(for: issuer)
        let update = [kSecValueData as String: data]
        let status = SecItemUpdate(baseQuery as CFDictionary, update as CFDictionary)
        if status == errSecSuccess { return }
        if status != errSecItemNotFound { throw CredentialStoreError.keychain(status) }

        var insert = baseQuery
        insert[kSecValueData as String] = data
        insert[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        let insertStatus = SecItemAdd(insert as CFDictionary, nil)
        guard insertStatus == errSecSuccess else { throw CredentialStoreError.keychain(insertStatus) }
    }

    public func clear(for issuer: URL) async throws {
        try removeLegacyCredential()
        try delete(scopedQuery(for: issuer))
    }

    private func scopedQuery(for issuer: URL, returningData: Bool = false) -> [String: Any] {
        var query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: "session:\(issuer.absoluteString)",
        ]
        if returningData {
            query[kSecReturnData as String] = true
            query[kSecMatchLimit as String] = kSecMatchLimitOne
        }
        return query
    }

    private func removeLegacyCredential() throws {
        try delete([
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: "session",
        ])
    }

    private func delete(_ query: [String: Any]) throws {
        let status = SecItemDelete(query as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw CredentialStoreError.keychain(status)
        }
    }
}
#endif
