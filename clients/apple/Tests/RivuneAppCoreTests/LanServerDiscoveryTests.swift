import XCTest
@testable import RivuneAppCore

final class LanServerDiscoveryTests: XCTestCase {
    func testParsesSecureAndTrustedLANAnnouncements() throws {
        let secure = try XCTUnwrap(RivuneLANService.parse(
            serviceName: "Living room",
            attributes: ["protocol": "20", "url": "https://media.example.com", "version": "1.10.0"]
        ))
        XCTAssertEqual(secure.name, "Living room")
        XCTAssertEqual(secure.address.absoluteString, "https://media.example.com")
        XCTAssertEqual(secure.version, "1.10.0")
        XCTAssertTrue(secure.usesSecureTransport)

        let local = try XCTUnwrap(RivuneLANService.parse(
            serviceName: "Bedroom",
            attributes: ["protocol": "20", "url": "http://192.168.1.20:8080/"]
        ))
        XCTAssertEqual(local.address.absoluteString, "http://192.168.1.20:8080")
        XCTAssertFalse(local.usesSecureTransport)
    }

    func testRejectsUnsafeAnnouncements() {
        for address in [
            "http://media.example.com",
            "http://198.51.100.20:8080",
            "https://user:secret@media.example.com",
            "https://media.example.com/path",
            "https://media.example.com?token=secret",
            "ftp://media.example.com",
        ] {
            XCTAssertNil(RivuneLANService.parse(serviceName: "Hostile", attributes: ["protocol": "20", "url": address]), address)
        }
        XCTAssertNil(RivuneLANService.parse(serviceName: "Old", attributes: ["protocol": "19", "url": "https://media.example.com"]))
        XCTAssertNil(RivuneLANService.parse(serviceName: "Missing", attributes: [:]))
    }
}
