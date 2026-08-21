import CryptoKit
import Foundation
import Network
import Security

public struct RivuneOfflineMediaItem: Codable, Identifiable, Equatable, Sendable {
    public let id: UUID
    public let titleId: UUID
    public let title: String
    public let fileName: String
    public let container: String
    public let sizeBytes: Int64
    public let createdAt: Date
    public let posterURL: String?
}

public struct RivuneOfflineMediaScope: Equatable, Sendable {
    public let identifier: String

    public init?(serverOrigin: URL, profileID: UUID) {
        guard var components = URLComponents(url: serverOrigin, resolvingAgainstBaseURL: true),
              let scheme = components.scheme?.lowercased(),
              let host = components.host?.lowercased(),
              !host.isEmpty else { return nil }
        components.scheme = scheme
        components.host = host
        if (scheme == "https" && components.port == 443) || (scheme == "http" && components.port == 80) {
            components.port = nil
        }
        components.user = nil
        components.password = nil
        components.percentEncodedPath = ""
        components.path = ""
        components.query = nil
        components.fragment = nil
        guard let origin = components.url?.absoluteString else { return nil }
        let material = Data("\(origin)\u{0}\(profileID.uuidString.lowercased())".utf8)
        identifier = SHA256.hash(data: material).map { String(format: "%02x", $0) }.joined()
    }

    public init?(identifier: String) {
        let normalized = identifier.lowercased()
        guard normalized.count == 64,
              normalized.unicodeScalars.allSatisfy({ (48...57).contains($0.value) || (97...102).contains($0.value) }) else { return nil }
        self.identifier = normalized
    }
}

public struct RivuneOfflineProfileAccess: Codable, Identifiable, Equatable, Sendable {
    public let id: String
    public let name: String
    public let requiresPIN: Bool
    fileprivate let pinSalt: Data?
    fileprivate let pinVerifier: Data?

    public init(name: String, scope: RivuneOfflineMediaScope, pin: String?) throws {
        id = scope.identifier
        self.name = name
        requiresPIN = pin != nil
        if let pin {
            let salt = try RivuneOfflinePINVerifier.randomSalt()
            pinSalt = salt
            pinVerifier = RivuneOfflinePINVerifier.derive(pin: pin, salt: salt)
        } else {
            pinSalt = nil
            pinVerifier = nil
        }
    }

    init(id: String, name: String, requiresPIN: Bool, pinSalt: Data?, pinVerifier: Data?) {
        self.id = id
        self.name = name
        self.requiresPIN = requiresPIN
        self.pinSalt = pinSalt
        self.pinVerifier = pinVerifier
    }

    public var scope: RivuneOfflineMediaScope? { RivuneOfflineMediaScope(identifier: id) }

    public func permits(pin: String?) -> Bool {
        guard requiresPIN else { return pin == nil }
        guard let pin, let pinSalt, let pinVerifier else { return false }
        return RivuneOfflinePINVerifier.matches(pin: pin, salt: pinSalt, verifier: pinVerifier)
    }
}

enum RivuneOfflinePINVerifier {
    private static let rounds = 50_000

    static func randomSalt() throws -> Data {
        var bytes = [UInt8](repeating: 0, count: 16)
        let status = bytes.withUnsafeMutableBytes { buffer in
            SecRandomCopyBytes(kSecRandomDefault, buffer.count, buffer.baseAddress!)
        }
        guard status == errSecSuccess else { throw RivuneOfflineMediaError.unavailable }
        return Data(bytes)
    }

    static func derive(pin: String, salt: Data) -> Data {
        var digest = Data(SHA256.hash(data: salt + Data(pin.utf8)))
        for _ in 1..<rounds { digest = Data(SHA256.hash(data: digest + salt)) }
        return digest
    }

    static func matches(pin: String, salt: Data, verifier: Data) -> Bool {
        constantTimeEqual(derive(pin: pin, salt: salt), verifier)
    }

    static func constantTimeEqual(_ lhs: Data, _ rhs: Data) -> Bool {
        let count = max(lhs.count, rhs.count)
        var difference = UInt(lhs.count ^ rhs.count)
        for index in 0..<count {
            let left = index < lhs.count ? lhs[index] : 0
            let right = index < rhs.count ? rhs[index] : 0
            difference |= UInt(left ^ right)
        }
        return difference == 0
    }
}

public enum RivuneOfflineMediaError: LocalizedError {
    case unavailable
    case unsupportedSource
    case invalidArchive
    case serverFailure

    public var errorDescription: String? {
        switch self {
        case .unavailable: return "Offline media storage is unavailable."
        case .unsupportedSource: return "This stream cannot be downloaded as one offline file."
        case .invalidArchive: return "The encrypted offline file is invalid or incomplete."
        case .serverFailure: return "The encrypted offline file could not be played."
        }
    }
}

public actor RivuneOfflineMediaStore {
    public static let shared = RivuneOfflineMediaStore()

    private let fileManager = FileManager.default
    private var playbackServer: RivuneOfflinePlaybackServer?
    private var activePartialPaths = Set<String>()
    private let maximumStoredBytes: Int64 = 20 * 1024 * 1024 * 1024

    public func items(in scope: RivuneOfflineMediaScope) -> [RivuneOfflineMediaItem] {
        (try? reconcileManifest(in: scope))?.sorted { $0.createdAt > $1.createdAt } ?? []
    }

    public func download(
        from url: URL,
        titleId: UUID,
        title: String,
        container: String?,
        posterURL: String?,
        in scope: RivuneOfflineMediaScope,
        configuration: URLSessionConfiguration = .ephemeral,
        progress: @escaping @Sendable (Int64) -> Void
    ) async throws -> RivuneOfflineMediaItem {
        let directory = try storageDirectory(for: scope)
        let currentBytes = try reconcileManifest(in: scope).reduce(Int64(0)) { $0 + $1.sizeBytes }
        guard currentBytes < maximumStoredBytes else { throw RivuneOfflineMediaError.unavailable }
        let identifier = UUID()
        let partial = directory.appendingPathComponent(".\(identifier.uuidString.lowercased()).partial", isDirectory: false)
        let destination = directory.appendingPathComponent("\(identifier.uuidString.lowercased()).rvn", isDirectory: false)
        let key = try offlineKey(for: scope)
        activePartialPaths.insert(partial.path)
        defer { activePartialPaths.remove(partial.path) }
        let writer = try RivuneEncryptedMediaWriter(url: partial, key: key, maximumBytes: maximumStoredBytes - currentBytes)
        do {
            try await RivuneStreamingDownloader.download(url: url, writer: writer, configuration: configuration, progress: progress)
            let bytes = try writer.finish()
            guard currentBytes <= maximumStoredBytes - bytes else {
                try? fileManager.removeItem(at: partial)
                throw RivuneOfflineMediaError.unavailable
            }
            try fileManager.moveItem(at: partial, to: destination)
            var manifest = try loadManifest(in: scope)
            let item = RivuneOfflineMediaItem(
                id: identifier,
                titleId: titleId,
                title: title,
                fileName: destination.lastPathComponent,
                container: container?.lowercased() ?? "mp4",
                sizeBytes: bytes,
                createdAt: Date(),
                posterURL: posterURL
            )
            manifest.removeAll { $0.id == item.id }
            manifest.append(item)
            try saveManifest(manifest, in: scope)
            return item
        } catch {
            try? writer.cancel()
            try? fileManager.removeItem(at: partial)
            try? fileManager.removeItem(at: destination)
            throw error
        }
    }

    public func remove(_ item: RivuneOfflineMediaItem, in scope: RivuneOfflineMediaScope) throws {
        stopPlayback()
        var manifest = try loadManifest(in: scope)
        guard manifest.contains(item), item.fileName == URL(fileURLWithPath: item.fileName).lastPathComponent else {
            throw RivuneOfflineMediaError.invalidArchive
        }
        let directory = try storageDirectory(for: scope)
        try? fileManager.removeItem(at: directory.appendingPathComponent(item.fileName))
        manifest.removeAll { $0.id == item.id }
        try saveManifest(manifest, in: scope)
    }

    public func playbackURL(for item: RivuneOfflineMediaItem, in scope: RivuneOfflineMediaScope) async throws -> URL {
        stopPlayback()
        let manifest = try loadManifest(in: scope)
        guard manifest.contains(item), item.fileName == URL(fileURLWithPath: item.fileName).lastPathComponent else {
            throw RivuneOfflineMediaError.invalidArchive
        }
        let archive = try storageDirectory(for: scope).appendingPathComponent(item.fileName)
        guard fileManager.isReadableFile(atPath: archive.path) else { throw RivuneOfflineMediaError.invalidArchive }
        let server = try await RivuneOfflinePlaybackServer.start(archive: archive, key: offlineKey(for: scope), container: item.container)
        playbackServer = server
        return server.url
    }

    public func stopPlayback() {
        playbackServer?.stop()
        playbackServer = nil
    }

    func storageDirectory(for scope: RivuneOfflineMediaScope) throws -> URL {
        guard let base = fileManager.urls(for: .applicationSupportDirectory, in: .userDomainMask).first else {
            throw RivuneOfflineMediaError.unavailable
        }
        let directory = base
            .appendingPathComponent("Rivune/OfflineMedia", isDirectory: true)
            .appendingPathComponent(scope.identifier, isDirectory: true)
        try fileManager.createDirectory(at: directory, withIntermediateDirectories: true, attributes: [
            .posixPermissions: 0o700,
        ])
        return directory
    }

    private func manifestURL(for scope: RivuneOfflineMediaScope) throws -> URL {
        try storageDirectory(for: scope).appendingPathComponent("manifest.json")
    }

    private func loadManifest(in scope: RivuneOfflineMediaScope) throws -> [RivuneOfflineMediaItem] {
        let url = try manifestURL(for: scope)
        guard fileManager.fileExists(atPath: url.path) else { return [] }
        return try JSONDecoder().decode([RivuneOfflineMediaItem].self, from: Data(contentsOf: url))
    }

    func saveManifest(_ items: [RivuneOfflineMediaItem], in scope: RivuneOfflineMediaScope) throws {
        let data = try JSONEncoder().encode(items)
        try data.write(to: manifestURL(for: scope), options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
    }

    func reconcileManifest(in scope: RivuneOfflineMediaScope) throws -> [RivuneOfflineMediaItem] {
        let directory = try storageDirectory(for: scope)
        let manifest = try loadManifest(in: scope)
        let reconciled = manifest.filter { item in
            guard item.fileName == URL(fileURLWithPath: item.fileName).lastPathComponent,
                  item.fileName.lowercased().hasSuffix(".rvn") else { return false }
            let archive = directory.appendingPathComponent(item.fileName, isDirectory: false)
            guard let attributes = try? fileManager.attributesOfItem(atPath: archive.path),
                  attributes[.type] as? FileAttributeType == .typeRegular,
                  fileManager.isReadableFile(atPath: archive.path) else { return false }
            return true
        }
        if reconciled != manifest { try saveManifest(reconciled, in: scope) }

        let referenced = Set(reconciled.map(\.fileName))
        let contents = try fileManager.contentsOfDirectory(
            at: directory,
            includingPropertiesForKeys: [.isRegularFileKey, .isSymbolicLinkKey],
            options: []
        )
        for candidate in contents {
            let isOrphanFinal = candidate.pathExtension.lowercased() == "rvn" && !referenced.contains(candidate.lastPathComponent)
            let isInactivePartial = candidate.pathExtension.lowercased() == "partial" && !activePartialPaths.contains(candidate.path)
            guard isOrphanFinal || isInactivePartial else { continue }
            let values = try? candidate.resourceValues(forKeys: [.isRegularFileKey, .isSymbolicLinkKey])
            guard values?.isRegularFile == true, values?.isSymbolicLink != true else { continue }
            try? fileManager.removeItem(at: candidate)
        }
        return reconciled
    }

    private func offlineKey(for scope: RivuneOfflineMediaScope) throws -> SymmetricKey {
        let service = "io.rivune.offline-media"
        let account = "scope-\(scope.identifier)-key-v1"
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        if status == errSecSuccess, let data = result as? Data, data.count == 32 {
            return SymmetricKey(data: data)
        }
        guard status == errSecItemNotFound else { throw RivuneOfflineMediaError.unavailable }
        let data = Data((0..<32).map { _ in UInt8.random(in: .min ... .max) })
        let add: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
            kSecValueData as String: data,
        ]
        guard SecItemAdd(add as CFDictionary, nil) == errSecSuccess else { throw RivuneOfflineMediaError.unavailable }
        return SymmetricKey(data: data)
    }
}

private enum RivuneEncryptedMediaFormat {
    static let magic = Data([0x52, 0x56, 0x4e, 0x31])
    static let headerBytes = 48
    static let chunkBytes = 1_048_576
    static let tagBytes = 16

    static func nonce(prefix: Data, index: UInt32) throws -> AES.GCM.Nonce {
        var data = prefix
        data.append(contentsOf: index.bigEndianBytes)
        return try AES.GCM.Nonce(data: data)
    }

    static func authenticatedData(index: UInt32) -> Data {
        var data = magic
        data.append(contentsOf: index.bigEndianBytes)
        return data
    }

    static func headerNonce(prefix: Data) throws -> AES.GCM.Nonce {
        try nonce(prefix: prefix, index: UInt32.max)
    }
}

final class RivuneEncryptedMediaWriter: @unchecked Sendable {
    private let handle: FileHandle
    private let key: SymmetricKey
    private let noncePrefix: Data
    private let maximumBytes: UInt64
    private var buffer = Data()
    private var chunkIndex: UInt32 = 0
    private var plaintextBytes: UInt64 = 0
    private var finished = false

    init(url: URL, key: SymmetricKey, maximumBytes: Int64) throws {
        guard maximumBytes > 0 else { throw RivuneOfflineMediaError.unavailable }
        FileManager.default.createFile(atPath: url.path, contents: nil, attributes: [.posixPermissions: 0o600])
        handle = try FileHandle(forWritingTo: url)
        self.key = key
        self.maximumBytes = UInt64(maximumBytes)
        noncePrefix = Data((0..<8).map { _ in UInt8.random(in: .min ... .max) })
        try handle.write(contentsOf: Data(repeating: 0, count: RivuneEncryptedMediaFormat.headerBytes))
    }

    func append(_ data: Data) throws {
        guard !finished else { throw RivuneOfflineMediaError.invalidArchive }
        guard UInt64(data.count) <= maximumBytes - min(plaintextBytes, maximumBytes) else { throw RivuneOfflineMediaError.unavailable }
        buffer.append(data)
        plaintextBytes += UInt64(data.count)
        while buffer.count >= RivuneEncryptedMediaFormat.chunkBytes {
            let chunk = Data(buffer.prefix(RivuneEncryptedMediaFormat.chunkBytes))
            buffer.removeFirst(RivuneEncryptedMediaFormat.chunkBytes)
            try writeChunk(chunk)
        }
    }

    func finish() throws -> Int64 {
        guard !finished, plaintextBytes > 0 else { throw RivuneOfflineMediaError.invalidArchive }
        if !buffer.isEmpty { try writeChunk(buffer); buffer.removeAll(keepingCapacity: false) }
        var authenticatedHeader = RivuneEncryptedMediaFormat.magic
        authenticatedHeader.append(2)
        authenticatedHeader.append(contentsOf: [0, 0, 0])
        authenticatedHeader.append(contentsOf: UInt32(RivuneEncryptedMediaFormat.chunkBytes).bigEndianBytes)
        authenticatedHeader.append(contentsOf: plaintextBytes.bigEndianBytes)
        authenticatedHeader.append(noncePrefix)
        let sealedHeader = try AES.GCM.seal(
            Data(), using: key, nonce: RivuneEncryptedMediaFormat.headerNonce(prefix: noncePrefix),
            authenticating: authenticatedHeader
        )
        try handle.seek(toOffset: 0)
        try handle.write(contentsOf: authenticatedHeader)
        try handle.write(contentsOf: sealedHeader.tag)
        try handle.write(contentsOf: Data(repeating: 0, count: 4))
        try handle.synchronize()
        try handle.close()
        finished = true
        return Int64(plaintextBytes)
    }

    func cancel() throws {
        guard !finished else { return }
        try handle.close()
        finished = true
    }

    private func writeChunk(_ plaintext: Data) throws {
        guard chunkIndex < UInt32.max else { throw RivuneOfflineMediaError.invalidArchive }
        let sealed = try AES.GCM.seal(
            plaintext,
            using: key,
            nonce: RivuneEncryptedMediaFormat.nonce(prefix: noncePrefix, index: chunkIndex),
            authenticating: RivuneEncryptedMediaFormat.authenticatedData(index: chunkIndex)
        )
        try handle.seekToEnd()
        try handle.write(contentsOf: sealed.ciphertext)
        try handle.write(contentsOf: sealed.tag)
        chunkIndex += 1
    }
}

final class RivuneEncryptedMediaReader: @unchecked Sendable {
    let plaintextBytes: UInt64
    private let handle: FileHandle
    private let key: SymmetricKey
    private let noncePrefix: Data
    private let chunkBytes: Int
    private let lock = NSLock()

    init(url: URL, key: SymmetricKey) throws {
        handle = try FileHandle(forReadingFrom: url)
        self.key = key
        do {
        let header = try handle.read(upToCount: RivuneEncryptedMediaFormat.headerBytes) ?? Data()
        guard header.count == RivuneEncryptedMediaFormat.headerBytes,
              header.prefix(4) == RivuneEncryptedMediaFormat.magic,
              header[4] == 2 else { throw RivuneOfflineMediaError.invalidArchive }
        chunkBytes = Int(UInt32(bigEndianData: header[8..<12]))
        plaintextBytes = UInt64(bigEndianData: header[12..<20])
        noncePrefix = Data(header[20..<28])
        guard chunkBytes == RivuneEncryptedMediaFormat.chunkBytes, plaintextBytes > 0 else {
            throw RivuneOfflineMediaError.invalidArchive
        }
        let chunkCount = (plaintextBytes + UInt64(RivuneEncryptedMediaFormat.chunkBytes) - 1) / UInt64(RivuneEncryptedMediaFormat.chunkBytes)
        let expectedLength = UInt64(RivuneEncryptedMediaFormat.headerBytes) + plaintextBytes + chunkCount * UInt64(RivuneEncryptedMediaFormat.tagBytes)
        let storedLength = try handle.seekToEnd()
        guard storedLength == expectedLength else { throw RivuneOfflineMediaError.invalidArchive }
        let sealedHeader = try AES.GCM.SealedBox(
            nonce: RivuneEncryptedMediaFormat.headerNonce(prefix: noncePrefix),
            ciphertext: Data(),
            tag: Data(header[28..<44])
        )
            _ = try AES.GCM.open(sealedHeader, using: key, authenticating: Data(header[0..<28]))
        }
        catch {
            try? handle.close()
            throw error
        }
    }

    deinit { try? handle.close() }

    func read(offset: UInt64, count: Int) throws -> Data {
        guard offset < plaintextBytes, count > 0 else { return Data() }
        lock.lock()
        defer { lock.unlock() }
        let end = min(plaintextBytes, offset + UInt64(count))
        var cursor = offset
        var output = Data()
        output.reserveCapacity(Int(end - offset))
        while cursor < end {
            let index = UInt32(cursor / UInt64(chunkBytes))
            let chunkStart = UInt64(index) * UInt64(chunkBytes)
            let plainLength = Int(min(UInt64(chunkBytes), plaintextBytes - chunkStart))
            let storedOffset = UInt64(RivuneEncryptedMediaFormat.headerBytes) + UInt64(index) * UInt64(chunkBytes + RivuneEncryptedMediaFormat.tagBytes)
            try handle.seek(toOffset: storedOffset)
            let stored = try handle.read(upToCount: plainLength + RivuneEncryptedMediaFormat.tagBytes) ?? Data()
            guard stored.count == plainLength + RivuneEncryptedMediaFormat.tagBytes else { throw RivuneOfflineMediaError.invalidArchive }
            let box = try AES.GCM.SealedBox(
                nonce: RivuneEncryptedMediaFormat.nonce(prefix: noncePrefix, index: index),
                ciphertext: stored.prefix(plainLength),
                tag: stored.suffix(RivuneEncryptedMediaFormat.tagBytes)
            )
            let plaintext = try AES.GCM.open(box, using: key, authenticating: RivuneEncryptedMediaFormat.authenticatedData(index: index))
            let localStart = Int(cursor - chunkStart)
            let localEnd = min(plainLength, localStart + Int(end - cursor))
            output.append(plaintext[localStart..<localEnd])
            cursor += UInt64(localEnd - localStart)
        }
        return output
    }
}

final class RivuneStreamingDownloader: NSObject, URLSessionDataDelegate, @unchecked Sendable {
    private let writer: RivuneEncryptedMediaWriter
    private let progress: @Sendable (Int64) -> Void
    private let configuration: URLSessionConfiguration
    private var continuation: CheckedContinuation<Void, Error>?
    private var session: URLSession?
    private var dataTask: URLSessionDataTask?
    private let cancellationLock = NSLock()
    private var cancellationRequested = false
    private var received: Int64 = 0
    private var failure: Error?

    private init(writer: RivuneEncryptedMediaWriter, configuration: URLSessionConfiguration, progress: @escaping @Sendable (Int64) -> Void) {
        self.writer = writer
        self.configuration = configuration
        self.progress = progress
    }

    static func download(
        url: URL,
        writer: RivuneEncryptedMediaWriter,
        configuration: URLSessionConfiguration = .ephemeral,
        progress: @escaping @Sendable (Int64) -> Void
    ) async throws {
        let delegate = RivuneStreamingDownloader(writer: writer, configuration: configuration, progress: progress)
        try await delegate.run(url: url)
    }

    private func run(url: URL) async throws {
        try await withTaskCancellationHandler {
            try Task.checkCancellation()
            try await withCheckedThrowingContinuation { continuation in
                self.continuation = continuation
                let queue = OperationQueue()
                queue.maxConcurrentOperationCount = 1
                queue.qualityOfService = .utility
                let session = URLSession(configuration: configuration, delegate: self, delegateQueue: queue)
                let dataTask = session.dataTask(with: url)
                self.session = session
                cancellationLock.lock()
                self.dataTask = dataTask
                let cancelled = cancellationRequested
                cancellationLock.unlock()
                if cancelled { dataTask.cancel() } else { dataTask.resume() }
            }
        } onCancel: {
            self.cancelDownload()
        }
    }

    private func cancelDownload() {
        cancellationLock.lock()
        cancellationRequested = true
        let dataTask = self.dataTask
        cancellationLock.unlock()
        dataTask?.cancel()
    }

    func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive response: URLResponse, completionHandler: @escaping (URLSession.ResponseDisposition) -> Void) {
        guard let response = response as? HTTPURLResponse, (200...299).contains(response.statusCode) else {
            failure = RivuneOfflineMediaError.unsupportedSource
            completionHandler(.cancel)
            return
        }
        completionHandler(.allow)
    }

    func urlSession(
        _ session: URLSession,
        task: URLSessionTask,
        willPerformHTTPRedirection response: HTTPURLResponse,
        newRequest request: URLRequest,
        completionHandler: @escaping (URLRequest?) -> Void
    ) {
        failure = RivuneOfflineMediaError.unsupportedSource
        completionHandler(nil)
    }

    func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
        guard failure == nil else { return }
        do {
            try writer.append(data)
            received += Int64(data.count)
            progress(received)
        } catch {
            failure = error
            dataTask.cancel()
        }
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        cancellationLock.lock()
        let cancelled = cancellationRequested
        dataTask = nil
        cancellationLock.unlock()
        let result: Error? = cancelled ? CancellationError() : failure ?? error
        let continuation = self.continuation
        self.continuation = nil
        self.session?.finishTasksAndInvalidate()
        self.session = nil
        if let result { continuation?.resume(throwing: result) } else { continuation?.resume() }
    }
}

private final class RivuneOfflinePlaybackServer: @unchecked Sendable {
    let url: URL
    private let listener: NWListener
    private let archive: URL
    private let key: SymmetricKey
    private let token: String
    private let mimeType: String
    private let queue = DispatchQueue(label: "io.rivune.offline-playback", qos: .userInitiated)

    private init(url: URL, listener: NWListener, archive: URL, key: SymmetricKey, token: String, mimeType: String) {
        self.url = url
        self.listener = listener
        self.archive = archive
        self.key = key
        self.token = token
        self.mimeType = mimeType
    }

    static func start(archive: URL, key: SymmetricKey, container: String) async throws -> RivuneOfflinePlaybackServer {
        let parameters = NWParameters.tcp
        parameters.requiredLocalEndpoint = .hostPort(host: "127.0.0.1", port: .any)
        let listener = try NWListener(using: parameters)
        let token = UUID().uuidString.lowercased().replacingOccurrences(of: "-", with: "")
        let mime = mimeType(for: container)
        return try await withCheckedThrowingContinuation { continuation in
            let gate = RivuneContinuationGate()
            listener.stateUpdateHandler = { state in
                switch state {
                case .ready:
                    guard gate.claim() else { return }
                    guard let port = listener.port,
                          let url = URL(string: "http://127.0.0.1:\(port.rawValue)/\(token)") else {
                        continuation.resume(throwing: RivuneOfflineMediaError.serverFailure)
                        return
                    }
                    let server = RivuneOfflinePlaybackServer(url: url, listener: listener, archive: archive, key: key, token: token, mimeType: mime)
                    listener.newConnectionHandler = { [weak server] connection in server?.accept(connection) }
                    continuation.resume(returning: server)
                case .failed(let error):
                    guard gate.claim() else { return }
                    continuation.resume(throwing: error)
                case .cancelled:
                    guard gate.claim() else { return }
                    continuation.resume(throwing: RivuneOfflineMediaError.serverFailure)
                default: break
                }
            }
            listener.start(queue: DispatchQueue(label: "io.rivune.offline-listener", qos: .userInitiated))
        }
    }

    func stop() { listener.cancel() }

    private func accept(_ connection: NWConnection) {
        connection.start(queue: queue)
        receiveRequest(connection, data: Data())
    }

    private func receiveRequest(_ connection: NWConnection, data: Data) {
        connection.receive(minimumIncompleteLength: 1, maximumLength: 16 * 1024) { [weak self] bytes, _, _, error in
            guard let self else { connection.cancel(); return }
            var request = data
            if let bytes { request.append(bytes) }
            if request.count > 32 * 1024 || error != nil { connection.cancel(); return }
            if request.range(of: Data("\r\n\r\n".utf8)) != nil {
                self.respond(connection, request: request)
            } else {
                self.receiveRequest(connection, data: request)
            }
        }
    }

    private func respond(_ connection: NWConnection, request: Data) {
        guard let text = String(data: request, encoding: .utf8),
              let first = text.components(separatedBy: "\r\n").first else { connection.cancel(); return }
        let parts = first.split(separator: " ")
        guard parts.count >= 2, (parts[0] == "GET" || parts[0] == "HEAD"), parts[1] == Substring("/\(token)") else {
            send(connection, header: "HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n", body: nil)
            return
        }
        do {
            let reader = try RivuneEncryptedMediaReader(url: archive, key: key)
            let total = reader.plaintextBytes
            var start: UInt64 = 0
            var end = total - 1
            var partial = false
            if let rangeLine = text.components(separatedBy: "\r\n").first(where: { $0.lowercased().hasPrefix("range:") }),
               let range = parseRange(rangeLine, total: total) {
                start = range.0; end = range.1; partial = true
            }
            let length = end - start + 1
            var header = "HTTP/1.1 \(partial ? "206 Partial Content" : "200 OK")\r\n"
            header += "Content-Type: \(mimeType)\r\nAccept-Ranges: bytes\r\nContent-Length: \(length)\r\n"
            if partial { header += "Content-Range: bytes \(start)-\(end)/\(total)\r\n" }
            header += "Connection: close\r\nCache-Control: no-store\r\n\r\n"
            if parts[0] == "HEAD" { send(connection, header: header, body: nil); return }
            connection.send(content: Data(header.utf8), completion: .contentProcessed { [weak self] error in
                guard error == nil, let self else { connection.cancel(); return }
                self.sendRange(connection, reader: reader, offset: start, remaining: length)
            })
        } catch {
            send(connection, header: "HTTP/1.1 500 Internal Server Error\r\nContent-Length: 0\r\nConnection: close\r\n\r\n", body: nil)
        }
    }

    private func sendRange(_ connection: NWConnection, reader: RivuneEncryptedMediaReader, offset: UInt64, remaining: UInt64) {
        guard remaining > 0 else { connection.cancel(); return }
        do {
            let count = Int(min(remaining, 256 * 1024))
            let data = try reader.read(offset: offset, count: count)
            guard !data.isEmpty else { connection.cancel(); return }
            connection.send(content: data, isComplete: UInt64(data.count) == remaining, completion: .contentProcessed { [weak self] error in
                guard error == nil, let self else { connection.cancel(); return }
                if UInt64(data.count) == remaining { connection.cancel() }
                else { self.sendRange(connection, reader: reader, offset: offset + UInt64(data.count), remaining: remaining - UInt64(data.count)) }
            })
        } catch { connection.cancel() }
    }

    private func send(_ connection: NWConnection, header: String, body: Data?) {
        var data = Data(header.utf8)
        if let body { data.append(body) }
        connection.send(content: data, isComplete: true, completion: .contentProcessed { _ in connection.cancel() })
    }

    private func parseRange(_ line: String, total: UInt64) -> (UInt64, UInt64)? {
        let value = line.split(separator: ":", maxSplits: 1).last?.trimmingCharacters(in: .whitespaces) ?? ""
        guard value.lowercased().hasPrefix("bytes=") else { return nil }
        let parts = value.dropFirst(6).split(separator: "-", maxSplits: 1, omittingEmptySubsequences: false)
        guard parts.count == 2, let start = UInt64(parts[0]), start < total else { return nil }
        let end = parts[1].isEmpty ? total - 1 : min(UInt64(parts[1]) ?? total - 1, total - 1)
        return end >= start ? (start, end) : nil
    }

    private static func mimeType(for container: String) -> String {
        switch container.lowercased() {
        case "mp4", "m4v", "mov": return "video/mp4"
        case "mkv": return "video/x-matroska"
        case "webm": return "video/webm"
        case "mp3": return "audio/mpeg"
        case "m4a", "aac": return "audio/mp4"
        default: return "application/octet-stream"
        }
    }
}

private final class RivuneContinuationGate: @unchecked Sendable {
    private let lock = NSLock()
    private var claimed = false

    func claim() -> Bool {
        lock.lock()
        defer { lock.unlock() }
        guard !claimed else { return false }
        claimed = true
        return true
    }
}

private extension FixedWidthInteger {
    var bigEndianBytes: [UInt8] {
        withUnsafeBytes(of: bigEndian) { Array($0) }
    }

    init<D: DataProtocol>(bigEndianData data: D) {
        self = data.reduce(0) { ($0 << 8) | Self($1) }
    }
}
