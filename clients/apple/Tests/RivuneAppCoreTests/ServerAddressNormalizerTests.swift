import XCTest
@testable import RivuneAppCore

final class ServerAddressNormalizerTests: XCTestCase {
    func testPreservesExplicitSchemesAndTrimsWhitespace() {
        XCTAssertEqual(ServerAddressNormalizer.normalize("  http://media.local:8080/path  "), "http://media.local:8080/path")
        XCTAssertEqual(ServerAddressNormalizer.normalize("https://rivune.example.com"), "https://rivune.example.com")
    }

    func testDefaultsLocalNetworkAddressesToHTTP() {
        XCTAssertEqual(ServerAddressNormalizer.normalize("localhost:8080"), "http://localhost:8080")
        XCTAssertEqual(ServerAddressNormalizer.normalize("127.0.0.1:8080"), "http://127.0.0.1:8080")
        XCTAssertEqual(ServerAddressNormalizer.normalize("[::1]:8080"), "http://[::1]:8080")
        XCTAssertEqual(ServerAddressNormalizer.normalize("192.168.1.20:8080"), "http://192.168.1.20:8080")
        XCTAssertEqual(ServerAddressNormalizer.normalize("10.0.0.20:8080"), "http://10.0.0.20:8080")
    }

    func testDefaultsPublicHostsToHTTPS() {
        XCTAssertEqual(ServerAddressNormalizer.normalize("rivune.example.com"), "https://rivune.example.com")
        XCTAssertEqual(ServerAddressNormalizer.normalize("198.51.100.20:8080"), "https://198.51.100.20:8080")
        XCTAssertEqual(ServerAddressNormalizer.normalize("[2001:db8::20]:8080"), "https://[2001:db8::20]:8080")
        XCTAssertEqual(ServerAddressNormalizer.normalize("rivune.local:8080"), "https://rivune.local:8080")
    }

}

@MainActor
final class AccentPreferenceTests: XCTestCase {
    func testAccentDefaultsToBlueAndPersistsSelection() {
        let suite = "AccentPreferenceTests.\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: suite)!
        defer { defaults.removePersistentDomain(forName: suite) }

        let model = RivuneAppModel(defaults: defaults)
        XCTAssertEqual(model.accent, .blue)

        model.setAccent(.rose)

        XCTAssertEqual(model.accent, .rose)
        XCTAssertEqual(RivuneAppModel(defaults: defaults).accent, .rose)
    }
}
