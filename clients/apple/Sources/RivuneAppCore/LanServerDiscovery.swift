import Foundation
import Network
import RivuneAPI

public struct DiscoveredRivuneServer: Identifiable, Equatable, Sendable {
    public let serviceName: String
    public let name: String
    public let address: URL
    public let version: String?

    public var id: String { address.absoluteString }
    public var usesSecureTransport: Bool { address.scheme == "https" }

    public init(serviceName: String, name: String, address: URL, version: String?) {
        self.serviceName = serviceName
        self.name = name
        self.address = address
        self.version = version
    }
}

public enum RivuneLANService {
    public static let type = "_rivune._tcp"

    public static func parse(serviceName: String, attributes: [String: String]) -> DiscoveredRivuneServer? {
        guard attributes["protocol"]?.trimmingCharacters(in: .whitespacesAndNewlines) == "20",
              let rawAddress = attributes["url"]?.trimmingCharacters(in: .whitespacesAndNewlines),
              rawAddress.utf8.count <= 255,
              let supplied = URL(string: rawAddress),
              let components = URLComponents(url: supplied, resolvingAgainstBaseURL: false),
              components.user == nil,
              components.password == nil,
              (components.path.isEmpty || components.path == "/"),
              components.query == nil,
              components.fragment == nil,
              let port = components.port ?? ((components.scheme?.lowercased() == "https") ? 443 : 80) as Int?,
              (1...65_535).contains(port),
              let address = try? RivuneAPIClient.canonicalServerOrigin(supplied) else { return nil }

        let fallbackName = String(serviceName.trimmingCharacters(in: .whitespacesAndNewlines).prefix(120))
        let advertisedName = String((attributes["name"] ?? "").trimmingCharacters(in: .whitespacesAndNewlines).prefix(120))
        let version = attributes["version"]
            .map { String($0.trimmingCharacters(in: .whitespacesAndNewlines).prefix(64)) }
            .flatMap { $0.isEmpty ? nil : $0 }
        return DiscoveredRivuneServer(
            serviceName: fallbackName.isEmpty ? "Rivune" : fallbackName,
            name: advertisedName.isEmpty ? (fallbackName.isEmpty ? "Rivune" : fallbackName) : advertisedName,
            address: address,
            version: version
        )
    }
}

@MainActor
public final class RivuneLANBrowser: ObservableObject {
    @Published public private(set) var servers: [DiscoveredRivuneServer] = []

    private var browser: NWBrowser?
    private let queue = DispatchQueue(label: "io.rivune.lan-discovery", qos: .userInitiated)

    public init() {}

    public func start() {
        stop()
        let parameters = NWParameters()
        parameters.includePeerToPeer = true
        let browser = NWBrowser(for: .bonjourWithTXTRecord(type: RivuneLANService.type, domain: nil), using: parameters)
        browser.stateUpdateHandler = { [weak self, weak browser] state in
            guard case .failed = state else { return }
            Task { @MainActor [weak self, weak browser] in
                guard self?.browser === browser else { return }
                self?.stop()
            }
        }
        browser.browseResultsChangedHandler = { [weak self, weak browser] results, _ in
            let servers = results.compactMap(Self.server(from:))
            Task { @MainActor [weak self, weak browser] in
                guard self?.browser === browser else { return }
                self?.servers = servers
                    .reduce(into: [String: DiscoveredRivuneServer]()) { found, server in
                        found[server.address.absoluteString] = server
                    }
                    .values
                    .sorted {
                        let comparison = $0.name.localizedCaseInsensitiveCompare($1.name)
                        return comparison == .orderedSame
                            ? $0.address.absoluteString < $1.address.absoluteString
                            : comparison == .orderedAscending
                    }
            }
        }
        self.browser = browser
        browser.start(queue: queue)
    }

    public func stop() {
        browser?.cancel()
        browser = nil
        servers = []
    }

    deinit {
        browser?.cancel()
    }

    nonisolated private static func server(from result: NWBrowser.Result) -> DiscoveredRivuneServer? {
        guard case let .service(name, _, _, _) = result.endpoint,
              case let .bonjour(record) = result.metadata else { return nil }
        return RivuneLANService.parse(serviceName: name, attributes: record.dictionary)
    }
}
