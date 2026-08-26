import Foundation
import CryptoKit
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif
import RivuneAPI

private let rivuneAppleUpdateCheckInterval: TimeInterval = 24 * 60 * 60
let rivuneMaximumAppleUpdateManifestBytes = 256 * 1_024
let rivuneMaximumAppleUpdateSignatureBytes = 4 * 1_024
private let rivuneMaximumAppleUpdatePackageBytes: Int64 = 512 * 1_024 * 1_024

public struct RivuneAppleUpdate: Identifiable, Equatable, Sendable {
    public let currentVersion: String
    public let latestVersion: String
    public let publishedAt: Date
    public let releaseURL: URL
    public let packageURL: URL
    public let packageFileName: String
    public let packageSize: Int64
    public let packageSHA256: String

    public var id: String { latestVersion }
}

public enum RivuneAppleUpdateState: Equatable, Sendable {
    case idle
    case checking
    case upToDate(currentVersion: String, latestVersion: String)
    case available(RivuneAppleUpdate)
    case failed
}

struct RivuneAppleUpdateCache: Codable, Equatable, Sendable {
    let currentVersion: String
    let latestVersion: String
    let publishedAt: String
    let releaseURL: String
    let packageURL: String
    let packageFileName: String
    let packageSize: Int64
    let packageSHA256: String
}

extension RivuneAppleUpdateCache {
    func restoredState(
        installedVersion: String,
        platform: RivuneAppleUpdatePlatform
    ) -> RivuneAppleUpdateState? {
        guard currentVersion == installedVersion,
              let current = RivuneSemanticVersion(currentVersion),
              let latest = RivuneSemanticVersion(latestVersion) else { return nil }
        guard current < latest else {
            return .upToDate(currentVersion: currentVersion, latestVersion: latestVersion)
        }
        let expectedReleaseURL = "https://github.com/moodiness/rivune/releases/tag/v\(latestVersion)"
        let expectedPackageURL = "https://github.com/moodiness/rivune/releases/download/v\(latestVersion)/\(platform.fileName)"
        guard let publishedDate = RivuneAppleUpdateManifestParser.parseRFC3339(publishedAt) else { return nil }
        guard releaseURL == expectedReleaseURL,
              packageURL == expectedPackageURL,
              packageFileName == platform.fileName,
              packageSize > 0,
              packageSize <= rivuneMaximumAppleUpdatePackageBytes,
              packageSHA256.utf8.count == 64,
              packageSHA256.utf8.allSatisfy({ (48...57).contains($0) || (97...102).contains($0) }),
              let releaseURL = URL(string: releaseURL),
              let packageURL = URL(string: packageURL) else { return nil }
        return .available(RivuneAppleUpdate(
            currentVersion: currentVersion,
            latestVersion: latestVersion,
            publishedAt: publishedDate,
            releaseURL: releaseURL,
            packageURL: packageURL,
            packageFileName: packageFileName,
            packageSize: packageSize,
            packageSHA256: packageSHA256
        ))
    }
}

enum RivuneAppleUpdateCheckResult: Equatable, Sendable {
    case upToDate(currentVersion: String, latestVersion: String)
    case available(RivuneAppleUpdate)
}

protocol RivuneAppleUpdateChecking: Sendable {
    func check(currentVersion: String) async throws -> RivuneAppleUpdateCheckResult
}

enum RivuneAppleUpdateError: Error, Equatable, Sendable {
    case invalidCurrentVersion
    case invalidManifest
    case manifestNotFound
    case responseTooLarge
    case tooManyRedirects
    case untrustedRedirect
    case unexpectedResponse
}

enum RivuneAppleUpdatePlatform: String, CaseIterable, Sendable {
    case ios
    case tvos
    case visionos
    case macos

    static var current: Self {
#if os(iOS)
        .ios
#elseif os(tvOS)
        .tvos
#elseif os(visionOS)
        .visionos
#elseif os(macOS)
        .macos
#else
        .macos
#endif
    }

    var format: String {
        switch self {
        case .ios, .tvos, .visionos: return "ipa"
        case .macos: return "dmg"
        }
    }

    var architectures: [String] {
        switch self {
        case .ios, .tvos, .visionos: return ["arm64"]
        case .macos: return ["arm64", "x64"]
        }
    }

    var minimumOSVersion: String {
        switch self {
        case .ios, .tvos: return "15.0"
        case .visionos: return "1.0"
        case .macos: return "12.0"
        }
    }

    var bundleIdentifier: String {
        switch self {
        case .ios: return "io.rivune.app"
        case .tvos: return "io.rivune.app.tv"
        case .visionos: return "io.rivune.app.vision"
        case .macos: return "io.rivune.app.mac"
        }
    }

    var fileName: String {
        switch self {
        case .ios: return "Rivune-iOS-unsigned.ipa"
        case .tvos: return "Rivune-tvOS-unsigned.ipa"
        case .visionos: return "Rivune-visionOS-unsigned.ipa"
        case .macos: return "Rivune-macOS.dmg"
        }
    }
}

final class RivuneAppleUpdateChecker: RivuneAppleUpdateChecking {
    static let manifestURL = URL(string: "https://github.com/moodiness/rivune/releases/latest/download/rivune-update.json")!

    private static let maximumRedirects = 5
    private let transport: any HTTPTransport
    private let platform: RivuneAppleUpdatePlatform
    private let verifySignature: @Sendable (Data, Data) throws -> Void

    convenience init(platform: RivuneAppleUpdatePlatform = .current) {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.timeoutIntervalForRequest = 20
        configuration.timeoutIntervalForResource = 20
        configuration.requestCachePolicy = .reloadIgnoringLocalAndRemoteCacheData
        configuration.urlCache = nil
        configuration.httpCookieStorage = nil
        configuration.httpShouldSetCookies = false
        let session = URLSession(configuration: configuration)
        self.init(
            transport: URLSessionTransport(
                session: session,
                maximumResponseBodyBytes: rivuneMaximumAppleUpdateManifestBytes
            ),
            platform: platform
        )
    }

    init(
        transport: any HTTPTransport,
        platform: RivuneAppleUpdatePlatform = .current,
        verifySignature: (@Sendable (Data, Data) throws -> Void)? = nil
    ) {
        self.transport = transport
        self.platform = platform
        self.verifySignature = verifySignature ?? { manifest, sidecar in
            try RivuneAppleUpdateSignatureVerifier.verify(manifest: manifest, sidecar: sidecar)
        }
    }

    func check(currentVersion: String) async throws -> RivuneAppleUpdateCheckResult {
        guard RivuneSemanticVersion(currentVersion) != nil else {
            throw RivuneAppleUpdateError.invalidCurrentVersion
        }
        let manifest = try await fetchAsset(
            from: Self.manifestURL,
            fileName: "rivune-update.json",
            maximumBytes: rivuneMaximumAppleUpdateManifestBytes
        )
        guard let signatureURL = URL(string: Self.manifestURL.absoluteString + ".sig") else {
            throw RivuneAppleUpdateError.invalidManifest
        }
        let signature = try await fetchAsset(
            from: signatureURL,
            fileName: "rivune-update.json.sig",
            maximumBytes: rivuneMaximumAppleUpdateSignatureBytes
        )
        try verifySignature(manifest, signature)
        return try RivuneAppleUpdateManifestParser.parse(
            manifest,
            currentVersion: currentVersion,
            platform: platform
        )
    }

    private func fetchAsset(from initialURL: URL, fileName: String, maximumBytes: Int) async throws -> Data {
        var url = initialURL
        for redirectCount in 0...Self.maximumRedirects {
            var request = URLRequest(url: url)
            request.httpMethod = "GET"
            request.timeoutInterval = 20
            request.setValue("application/json", forHTTPHeaderField: "Accept")
            request.setValue("Rivune-Apple-Update/2.0", forHTTPHeaderField: "User-Agent")
            let (data, response): (Data, HTTPURLResponse)
            do {
                (data, response) = try await transport.data(for: request)
            } catch RivuneAPIError.responseTooLarge {
                throw RivuneAppleUpdateError.responseTooLarge
            }
            guard response.url == url else { throw RivuneAppleUpdateError.untrustedRedirect }
            switch response.statusCode {
            case 200:
                guard Self.isAllowedFinalAssetURL(url, fileName: fileName), data.count <= maximumBytes else {
                    throw data.count > maximumBytes ? RivuneAppleUpdateError.responseTooLarge : RivuneAppleUpdateError.untrustedRedirect
                }
                return data
            case 301, 302, 303, 307, 308:
                guard redirectCount < Self.maximumRedirects else { throw RivuneAppleUpdateError.tooManyRedirects }
                guard let location = response.value(forHTTPHeaderField: "Location"),
                      let redirected = URL(string: location, relativeTo: url)?.absoluteURL,
                      Self.isAllowedAssetRedirectURL(redirected, fileName: fileName) else {
                    throw RivuneAppleUpdateError.untrustedRedirect
                }
                url = redirected
            case 404:
                throw RivuneAppleUpdateError.manifestNotFound
            default:
                throw RivuneAppleUpdateError.unexpectedResponse
            }
        }
        throw RivuneAppleUpdateError.tooManyRedirects
    }

    static func automaticCheckIsDue(lastSuccessfulCheck: Date?, now: Date) -> Bool {
        guard let lastSuccessfulCheck else { return true }
        return lastSuccessfulCheck > now || now.timeIntervalSince(lastSuccessfulCheck) >= rivuneAppleUpdateCheckInterval
    }

    private static func isAllowedAssetRedirectURL(_ url: URL, fileName: String) -> Bool {
        guard isPlainHTTPSURL(url) else { return false }
        if isGitHubAssetURL(url) { return true }
        guard url.host?.lowercased() == "github.com", url.query == nil else { return false }
        let parts = url.path.split(separator: "/", omittingEmptySubsequences: true).map(String.init)
        guard parts.count == 6,
              parts[0] == "moodiness",
              parts[1] == "rivune",
              parts[2] == "releases",
              parts[3] == "download",
              parts[5] == fileName,
              parts[4].hasPrefix("v"),
              RivuneSemanticVersion(String(parts[4].dropFirst())) != nil else { return false }
        return true
    }

    private static func isAllowedFinalAssetURL(_ url: URL, fileName: String) -> Bool {
        let expectedLatest = fileName == "rivune-update.json" ? manifestURL : URL(string: manifestURL.absoluteString + ".sig")
        return url == expectedLatest || isAllowedAssetRedirectURL(url, fileName: fileName)
    }

    private static func isGitHubAssetURL(_ url: URL) -> Bool {
        guard let host = url.host?.lowercased(), !url.path.isEmpty else { return false }
        return host == "release-assets.githubusercontent.com" || host == "objects.githubusercontent.com"
    }

    private static func isPlainHTTPSURL(_ url: URL) -> Bool {
        guard let components = URLComponents(url: url, resolvingAgainstBaseURL: false) else { return false }
        return components.scheme?.lowercased() == "https" &&
            (components.port == nil || components.port == 443) &&
            components.user == nil &&
            components.password == nil &&
            components.fragment == nil
    }
}

private struct RivuneAppleUpdateSignature: Decodable {
    let schemaVersion: Int
    let algorithm: String
    let keyId: String
    let manifestSha256: String
    let signature: String
}

enum RivuneAppleUpdateSignatureVerifier {
    private static let algorithm = "ecdsa-p256-sha256"
    private static let keyId = "4e9b15a0b6aed77908f3686fbf05a0a9c322ad846662eb758f56d4e65c22796f"
    private static let publicKey = "MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEacg8w48bnbKqa/KOJd070if0/100iHsU+o6ecokqIS6p7thhZb1ZR9YawxW7HuoEs5k6dW9sTCOyMjUcsgAQww=="

    static func verify(manifest: Data, sidecar: Data) throws {
        guard !sidecar.isEmpty, sidecar.count <= rivuneMaximumAppleUpdateSignatureBytes else {
            throw RivuneAppleUpdateError.responseTooLarge
        }
        guard let text = String(data: sidecar, encoding: .utf8) else {
            throw RivuneAppleUpdateError.invalidManifest
        }
        let object: Any
        let expectedFields = ["schemaVersion", "algorithm", "keyId", "manifestSha256", "signature"]
        for field in expectedFields where text.components(separatedBy: "\"\(field)\"").count != 2 {
            throw RivuneAppleUpdateError.invalidManifest
        }
        do { object = try JSONSerialization.jsonObject(with: sidecar) }
        catch { throw RivuneAppleUpdateError.invalidManifest }
        guard let dictionary = object as? [String: Any],
              Set(dictionary.keys) == Set(expectedFields),
              let value = try? JSONDecoder().decode(RivuneAppleUpdateSignature.self, from: sidecar),
              value.schemaVersion == 1,
              value.algorithm == algorithm,
              value.keyId == keyId,
              value.manifestSha256 == SHA256.hash(data: manifest).map({ String(format: "%02x", $0) }).joined(),
              let publicKeyData = Data(base64Encoded: publicKey),
              publicKeyData.base64EncodedString() == publicKey,
              let signatureData = Data(base64Encoded: value.signature),
              signatureData.base64EncodedString() == value.signature,
              let verifier = try? P256.Signing.PublicKey(derRepresentation: publicKeyData),
              let signature = try? P256.Signing.ECDSASignature(derRepresentation: signatureData),
              verifier.isValidSignature(signature, for: manifest) else {
            throw RivuneAppleUpdateError.invalidManifest
        }
    }
}

private struct RivuneAppleUpdateManifest: Decodable {
    let schemaVersion: Int
    let channel: String
    let version: String
    let tagName: String
    let publishedAt: String
    let releaseUrl: String
    let packages: RivuneAppleUpdatePackages
}

private struct RivuneAppleUpdatePackages: Decodable {
    let ios: RivuneAppleUpdatePackage
    let tvos: RivuneAppleUpdatePackage
    let visionos: RivuneAppleUpdatePackage
    let macos: RivuneAppleUpdatePackage

    func package(for platform: RivuneAppleUpdatePlatform) -> RivuneAppleUpdatePackage {
        switch platform {
        case .ios: return ios
        case .tvos: return tvos
        case .visionos: return visionos
        case .macos: return macos
        }
    }
}

private struct RivuneAppleUpdatePackage: Decodable {
    let format: String
    let architectures: [String]
    let minimumOsVersion: String
    let bundleIdentifier: String
    let signature: String
    let fileName: String
    let url: String
    let size: Int64
    let sha256: String
}

enum RivuneAppleUpdateManifestParser {
    static func parse(
        _ data: Data,
        currentVersion: String,
        platform: RivuneAppleUpdatePlatform
    ) throws -> RivuneAppleUpdateCheckResult {
        guard data.count <= rivuneMaximumAppleUpdateManifestBytes,
              let current = RivuneSemanticVersion(currentVersion) else {
            throw data.count > rivuneMaximumAppleUpdateManifestBytes
                ? RivuneAppleUpdateError.responseTooLarge
                : RivuneAppleUpdateError.invalidCurrentVersion
        }

        let manifest: RivuneAppleUpdateManifest
        do {
            let decoder = JSONDecoder()
            manifest = try decoder.decode(RivuneAppleUpdateManifest.self, from: data)
        } catch {
            throw RivuneAppleUpdateError.invalidManifest
        }

        let package = manifest.packages.package(for: platform)
        guard manifest.schemaVersion == 3,
              let latest = RivuneSemanticVersion(manifest.version),
              manifest.channel == (latest.prerelease.isEmpty ? "stable" : "prerelease"),
              manifest.tagName == "v\(manifest.version)",
              let publishedAt = parseRFC3339(manifest.publishedAt),
              let releaseURL = exactHTTPSURL(
                manifest.releaseUrl,
                expected: "https://github.com/moodiness/rivune/releases/tag/\(manifest.tagName)"
              ),
              package.format == platform.format,
              package.architectures == platform.architectures,
              package.minimumOsVersion == platform.minimumOSVersion,
              package.bundleIdentifier == platform.bundleIdentifier,
              package.signature == "unsigned",
              package.fileName == platform.fileName,
              package.size > 0,
              package.size <= rivuneMaximumAppleUpdatePackageBytes,
              isLowercaseSHA256(package.sha256),
              let packageURL = exactHTTPSURL(
                package.url,
                expected: "https://github.com/moodiness/rivune/releases/download/\(manifest.tagName)/\(platform.fileName)"
              ) else {
            throw RivuneAppleUpdateError.invalidManifest
        }

        guard current < latest else {
            return .upToDate(currentVersion: currentVersion, latestVersion: manifest.version)
        }
        return .available(RivuneAppleUpdate(
            currentVersion: currentVersion,
            latestVersion: manifest.version,
            publishedAt: publishedAt,
            releaseURL: releaseURL,
            packageURL: packageURL,
            packageFileName: package.fileName,
            packageSize: package.size,
            packageSHA256: package.sha256
        ))
    }

    private static func exactHTTPSURL(_ value: String, expected: String) -> URL? {
        guard value == expected,
              let url = URL(string: value),
              let components = URLComponents(url: url, resolvingAgainstBaseURL: false),
              components.scheme == "https",
              components.host?.lowercased() == "github.com",
              (components.port == nil || components.port == 443),
              components.user == nil,
              components.password == nil,
              components.query == nil,
              components.fragment == nil else { return nil }
        return url
    }

    private static func isLowercaseSHA256(_ value: String) -> Bool {
        value.utf8.count == 64 && value.utf8.allSatisfy {
            (48...57).contains($0) || (97...102).contains($0)
        }
    }

    fileprivate static func parseRFC3339(_ value: String) -> Date? {
        guard value.range(
            of: #"^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?(Z|[+-][0-9]{2}:[0-9]{2})$"#,
            options: .regularExpression
        ) != nil else { return nil }
        let fractional = ISO8601DateFormatter()
        fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let date = fractional.date(from: value) { return date }
        let wholeSeconds = ISO8601DateFormatter()
        wholeSeconds.formatOptions = [.withInternetDateTime]
        return wholeSeconds.date(from: value)
    }
}

struct RivuneSemanticVersion: Comparable, Equatable, Sendable {
    let core: [String]
    let prerelease: [String]

    init?(_ value: String) {
        let buildParts = value.split(separator: "+", omittingEmptySubsequences: false)
        guard (1...2).contains(buildParts.count),
              buildParts.allSatisfy({ !$0.isEmpty }),
              buildParts.dropFirst().allSatisfy({ Self.validIdentifiers(String($0), rejectLeadingZeroNumbers: false) }) else {
            return nil
        }
        let releaseParts = buildParts[0].split(separator: "-", maxSplits: 1, omittingEmptySubsequences: false)
        guard (1...2).contains(releaseParts.count), !releaseParts[0].isEmpty else { return nil }
        let core = releaseParts[0].split(separator: ".", omittingEmptySubsequences: false).map(String.init)
        guard core.count == 3, core.allSatisfy(Self.validCoreIdentifier) else { return nil }
        let prerelease = releaseParts.count == 2 ? String(releaseParts[1]) : ""
        guard releaseParts.count == 1 || (!prerelease.isEmpty && Self.validIdentifiers(prerelease, rejectLeadingZeroNumbers: true)) else { return nil }
        self.core = core
        self.prerelease = prerelease.isEmpty ? [] : prerelease.split(separator: ".").map(String.init)
    }

    static func < (left: Self, right: Self) -> Bool {
        for index in 0..<3 {
            let comparison = compareNumeric(left.core[index], right.core[index])
            if comparison != 0 { return comparison < 0 }
        }
        if left.prerelease.isEmpty { return false }
        if right.prerelease.isEmpty { return true }
        for index in 0..<max(left.prerelease.count, right.prerelease.count) {
            guard index < left.prerelease.count else { return true }
            guard index < right.prerelease.count else { return false }
            let leftPart = left.prerelease[index]
            let rightPart = right.prerelease[index]
            if leftPart == rightPart { continue }
            let leftNumeric = leftPart.utf8.allSatisfy { (48...57).contains($0) }
            let rightNumeric = rightPart.utf8.allSatisfy { (48...57).contains($0) }
            if leftNumeric && rightNumeric { return compareNumeric(leftPart, rightPart) < 0 }
            if leftNumeric { return true }
            if rightNumeric { return false }
            return leftPart < rightPart
        }
        return false
    }

    private static func compareNumeric(_ left: String, _ right: String) -> Int {
        if left.count != right.count { return left.count < right.count ? -1 : 1 }
        if left == right { return 0 }
        return left < right ? -1 : 1
    }

    private static func validCoreIdentifier(_ value: String) -> Bool {
        guard !value.isEmpty,
              value.utf8.allSatisfy({ (48...57).contains($0) }) else { return false }
        return value == "0" || value.first != "0"
    }

    private static func validIdentifiers(_ value: String, rejectLeadingZeroNumbers: Bool) -> Bool {
        let identifiers = value.split(separator: ".", omittingEmptySubsequences: false).map(String.init)
        return !identifiers.isEmpty && identifiers.allSatisfy { identifier in
            guard !identifier.isEmpty,
                  identifier.utf8.allSatisfy({ byte in
                      (48...57).contains(byte) || (65...90).contains(byte) || (97...122).contains(byte) || byte == 45
                  }) else { return false }
            let numeric = identifier.utf8.allSatisfy { (48...57).contains($0) }
            return !rejectLeadingZeroNumbers || !numeric || identifier == "0" || identifier.first != "0"
        }
    }
}
