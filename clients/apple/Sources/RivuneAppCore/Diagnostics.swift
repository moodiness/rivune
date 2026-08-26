import Foundation
#if canImport(Darwin)
import Darwin
#endif

let rivuneMaximumDiagnosticEventBytes = 64 * 1_024
let rivuneMaximumDiagnosticReportBytes = 64 * 1_024
let rivuneDiagnosticReportFileName = "rivune-diagnostics.txt"

public enum RivuneDiagnosticEventCode: String, Sendable {
    case appStarted = "APP_STARTED"
    case serverConnectionStarted = "SERVER_CONNECTION_STARTED"
    case serverConnectionSucceeded = "SERVER_CONNECTION_SUCCEEDED"
    case serverConnectionFailed = "SERVER_CONNECTION_FAILED"
    case catalogRefreshStarted = "CATALOG_REFRESH_STARTED"
    case catalogRefreshSucceeded = "CATALOG_REFRESH_SUCCEEDED"
    case catalogRefreshFailed = "CATALOG_REFRESH_FAILED"
    case searchStarted = "SEARCH_STARTED"
    case searchSucceeded = "SEARCH_SUCCEEDED"
    case searchPartial = "SEARCH_PARTIAL"
    case searchFailed = "SEARCH_FAILED"
    case searchCanceled = "SEARCH_CANCELED"
    case playbackStarted = "PLAYBACK_STARTED"
    case playbackStopped = "PLAYBACK_STOPPED"
    case playbackFailed = "PLAYBACK_FAILED"
    case updateCheckStarted = "UPDATE_CHECK_STARTED"
    case updateAvailable = "UPDATE_AVAILABLE"
    case updateUpToDate = "UPDATE_UP_TO_DATE"
    case updateCheckFailed = "UPDATE_CHECK_FAILED"
    case diagnosticExportSucceeded = "DIAGNOSTIC_EXPORT_SUCCEEDED"
    case diagnosticExportFailed = "DIAGNOSTIC_EXPORT_FAILED"
}

public struct RivuneDiagnosticEvent: Equatable, Sendable {
    public let timestampMilliseconds: Int64
    public let code: RivuneDiagnosticEventCode
    public let operationId: UUID?

    public init(
        timestampMilliseconds: Int64, code: RivuneDiagnosticEventCode, operationId: UUID? = nil
    ) {
        self.timestampMilliseconds = timestampMilliseconds
        self.code = code
        self.operationId = operationId
    }
}

final class RivuneDiagnosticsBuffer: @unchecked Sendable {
    private struct BufferedEvent {
        let event: RivuneDiagnosticEvent
        let bytes: Int
    }

    private let maximumBytes: Int
    private let now: () -> Int64
    private let lock = NSLock()
    private var events: [BufferedEvent] = []
    private var bytes = 0

    init(
        maximumBytes: Int = rivuneMaximumDiagnosticEventBytes,
        now: @escaping () -> Int64 = { Int64(Date().timeIntervalSince1970 * 1_000) }
    ) {
        precondition((1...rivuneMaximumDiagnosticEventBytes).contains(maximumBytes))
        self.maximumBytes = maximumBytes
        self.now = now
    }

    func record(_ code: RivuneDiagnosticEventCode, operationId: UUID? = nil) {
        record(code, operationId: operationId, timestampMilliseconds: now())
    }

    func record(
        _ code: RivuneDiagnosticEventCode, operationId: UUID? = nil,
        timestampMilliseconds: Int64
    ) {
        let event = RivuneDiagnosticEvent(
            timestampMilliseconds: timestampMilliseconds, code: code, operationId: operationId)
        let buffered = BufferedEvent(
            event: event,
            bytes: RivuneDiagnosticsReport.serializedEventLine(event).utf8.count
        )
        guard buffered.bytes <= maximumBytes else { return }
        lock.lock()
        defer { lock.unlock() }
        while !events.isEmpty && bytes + buffered.bytes > maximumBytes {
            bytes -= events.removeFirst().bytes
        }
        events.append(buffered)
        bytes += buffered.bytes
    }

    func snapshot() -> [RivuneDiagnosticEvent] {
        lock.lock()
        defer { lock.unlock() }
        return events.map(\.event)
    }

    func clear() {
        lock.lock()
        defer { lock.unlock() }
        events.removeAll(keepingCapacity: false)
        bytes = 0
    }
}

struct RivuneAppleDiagnosticMetadata: Equatable {
    let appVersion: String
    let appBuild: String
    let platform: String
    let operatingSystemVersion: String
    let deviceModel: String
    let isTelevision: Bool

    static func current(bundle: Bundle = .main, processInfo: ProcessInfo = .processInfo) -> Self {
        let version = processInfo.operatingSystemVersion
        return Self(
            appVersion: bundle.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "unavailable",
            appBuild: bundle.object(forInfoDictionaryKey: "CFBundleVersion") as? String ?? "unavailable",
            platform: platformName,
            operatingSystemVersion: "\(version.majorVersion).\(version.minorVersion).\(version.patchVersion)",
            deviceModel: machineIdentifier(),
            isTelevision: platformName == "tvos"
        )
    }

    private static var platformName: String {
#if os(iOS)
        "ios"
#elseif os(tvOS)
        "tvos"
#elseif os(macOS)
        "macos"
#elseif os(visionOS)
        "visionos"
#else
        "apple"
#endif
    }

    private static func machineIdentifier() -> String {
#if canImport(Darwin)
        var size = 0
        guard sysctlbyname("hw.machine", nil, &size, nil, 0) == 0, size > 1, size <= 1_024 else {
            return "unavailable"
        }
        var value = [CChar](repeating: 0, count: size)
        guard sysctlbyname("hw.machine", &value, &size, nil, 0) == 0 else { return "unavailable" }
        return String(cString: value)
#else
        return "unavailable"
#endif
    }
}

struct RivuneDiagnosticReportInput {
    let generatedAtMilliseconds: Int64
    let appVersion: String
    let appBuild: String
    let platform: String
    let operatingSystemVersion: String
    let deviceModel: String
    let isTelevision: Bool
    let serverAddress: String?
    let serverDisplayName: String?
    let serverVersion: String?
    let serverProtocolVersion: Int?
    let startupTab: String?
    let preferredPlayer: String?
    let embeddedPlayer: String?
    let animationPreference: String?
    let accentColor: String?
    let frameRateMatching: String?
    let videoAspect: String?
    let localQuality: String?
    let remoteWifiQuality: String?
    let mobileQuality: String?
    let events: [RivuneDiagnosticEvent]
}

enum RivuneDiagnosticsReport {
    private static let maximumScalarBytes = 512
    private static let maximumServerURLLength = 4_096
    private static let unavailable = "unavailable"

    static func build(_ input: RivuneDiagnosticReportInput) -> String {
        var report = "Rivune Apple diagnostics\n"
        report += "Report format: 1\n"
        appendField("Generated at", formatTimestamp(input.generatedAtMilliseconds), to: &report)
        appendField("App version", safeScalar(input.appVersion), to: &report)
        appendField("App build", safeScalar(input.appBuild), to: &report)
        appendField("Platform", safeScalar(input.platform), to: &report)
        appendField("OS version", safeScalar(input.operatingSystemVersion), to: &report)
        appendField("Device model", safeScalar(input.deviceModel), to: &report)
        appendField("TV device", input.isTelevision ? "yes" : "no", to: &report)
        appendField("Server origin", sanitizeServerOrigin(input.serverAddress) ?? unavailable, to: &report)
        appendField("Server name", safeScalar(input.serverDisplayName), to: &report)
        appendField("Server version", safeScalar(input.serverVersion), to: &report)
        appendField("Server protocol", input.serverProtocolVersion.map(String.init) ?? unavailable, to: &report)
        appendField("Startup tab", safeScalar(input.startupTab), to: &report)
        appendField("Preferred player", safeScalar(input.preferredPlayer), to: &report)
        appendField("Embedded player", safeScalar(input.embeddedPlayer), to: &report)
        appendField("Animations", safeScalar(input.animationPreference), to: &report)
        appendField("Accent color", safeScalar(input.accentColor), to: &report)
        appendField("Frame-rate matching", safeScalar(input.frameRateMatching), to: &report)
        appendField("Video aspect", safeScalar(input.videoAspect), to: &report)
        appendField("Local quality", safeScalar(input.localQuality), to: &report)
        appendField("Remote Wi-Fi quality", safeScalar(input.remoteWifiQuality), to: &report)
        appendField("Mobile quality", safeScalar(input.mobileQuality), to: &report)
        report += "Events:\n"

        var remainingBytes = max(rivuneMaximumDiagnosticReportBytes - report.utf8.count, 0)
        var retainedLines: [String] = []
        for event in input.events.reversed() {
            let line = serializedEventLine(event)
            let lineBytes = line.utf8.count
            guard lineBytes <= remainingBytes else { break }
            retainedLines.append(line)
            remainingBytes -= lineBytes
        }
        for line in retainedLines.reversed() { report += line }
        return report
    }

    static func sanitizeServerOrigin(_ value: String?) -> String? {
        guard let candidate = value?.trimmingCharacters(in: .whitespacesAndNewlines),
              !candidate.isEmpty,
              candidate.utf8.count <= maximumServerURLLength,
              !candidate.unicodeScalars.contains(where: isUnsafeScalar),
              let components = URLComponents(string: candidate),
              let rawScheme = components.scheme,
              let rawHost = components.host,
              !rawHost.isEmpty else { return nil }
        let scheme = rawScheme.lowercased()
        guard scheme == "http" || scheme == "https" else { return nil }
        let host = rawHost.trimmingCharacters(in: CharacterSet(charactersIn: "[]")).lowercased()
        guard !host.isEmpty, !host.contains("%"), !host.unicodeScalars.contains(where: isUnsafeScalar) else { return nil }
        let renderedHost = host.contains(":") ? "[\(host)]" : host
        let port = components.port
        let includePort = port.map { !((scheme == "http" && $0 == 80) || (scheme == "https" && $0 == 443)) } ?? false
        let origin = "\(scheme)://\(renderedHost)\(includePort ? ":\(port!)" : "")"
        return origin.utf8.count <= maximumServerURLLength ? origin : nil
    }

    static func serializedEventLine(_ event: RivuneDiagnosticEvent) -> String {
        let operation = event.operationId.map { " operation=\($0.uuidString.lowercased())" } ?? ""
        return "\(formatTimestamp(event.timestampMilliseconds)) \(event.code.rawValue)\(operation)\n"
    }

    private static func appendField(_ label: String, _ value: String?, to report: inout String) {
        report += "\(label): \(value ?? unavailable)\n"
    }

    private static func safeScalar(_ value: String?) -> String {
        guard let value else { return unavailable }
        var output = ""
        output.reserveCapacity(min(value.utf8.count, maximumScalarBytes))
        var usedBytes = 0
        for scalar in value.unicodeScalars where !isUnsafeScalar(scalar) {
            let rendered = String(scalar)
            let bytes = rendered.utf8.count
            guard usedBytes + bytes <= maximumScalarBytes else { break }
            output.unicodeScalars.append(scalar)
            usedBytes += bytes
        }
        let trimmed = output.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? unavailable : trimmed
    }

    private static func isUnsafeScalar(_ scalar: Unicode.Scalar) -> Bool {
        switch scalar.properties.generalCategory {
        case .control, .format, .lineSeparator, .paragraphSeparator, .surrogate:
            return true
        default:
            return false
        }
    }

    private static func formatTimestamp(_ milliseconds: Int64) -> String {
        let date = Date(timeIntervalSince1970: Double(milliseconds) / 1_000)
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = milliseconds.isMultiple(of: 1_000)
            ? [.withInternetDateTime]
            : [.withInternetDateTime, .withFractionalSeconds]
        return formatter.string(from: date)
    }
}
