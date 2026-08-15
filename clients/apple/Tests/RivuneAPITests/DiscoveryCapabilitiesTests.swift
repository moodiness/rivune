import Foundation
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif
import XCTest
@testable import RivuneAPI

final class DiscoveryCapabilitiesTests: XCTestCase {
    func testStableCapabilityIdentifiersAndOmittedCapabilities() throws {
        XCTAssertEqual(
            DiscoveryCapability.allCases.map(\.rawValue),
            ["bounded-aggregate-resources", "profile-archives-v1", "request-correlation"]
        )

        let discovery = try JSONDecoder().decode(Discovery.self, from: Data("""
        {"name":"Rivune","serverVersion":"20.0","protocolVersion":20,"apiBaseUrl":"/api/v1","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en-US"}
        """.utf8))

        XCTAssertEqual(discovery.capabilities, [])
        XCTAssertFalse(discovery.supports(.profileArchivesV1))
        XCTAssertFalse(discovery.supportsProfileArchives)
    }

    func testCapabilitiesRetainSafeUnknownsAndDiscardMalformedEntriesAndDuplicates() throws {
        let tooLong = String(repeating: "a", count: 65)
        let discovery = try JSONDecoder().decode(Discovery.self, from: Data("""
        {"name":"Rivune","serverVersion":"20.0","protocolVersion":20,"apiBaseUrl":"/api/v1","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en-US","capabilities":["profile-archives-v1","future-capability","profile-archives-v1","","Uppercase","-leading","trailing-","two--hyphens","contains/slash","\(tooLong)",17,null,{"identifier":"request-correlation"}]}
        """.utf8))

        XCTAssertEqual(discovery.capabilities, ["profile-archives-v1", "future-capability"])
        XCTAssertTrue(discovery.supports(.profileArchivesV1))
        XCTAssertTrue(discovery.supportsProfileArchives)
        XCTAssertFalse(discovery.supports(.boundedAggregateResources))
        XCTAssertFalse(discovery.supports(.requestCorrelation))
    }

    func testCapabilitiesAreStableDeduplicatedAndCappedAt64() throws {
        let identifiers = (0..<70).map { "future-\($0)" } + ["future-0", DiscoveryCapability.profileArchivesV1.rawValue]
        let data = try JSONSerialization.data(withJSONObject: [
            "name": "Rivune",
            "serverVersion": "20.0",
            "protocolVersion": 20,
            "apiBaseUrl": "/api/v1",
            "setupRequired": false,
            "timezone": "UTC",
            "interfaceLanguage": "en-US",
            "capabilities": identifiers,
        ])

        let discovery = try JSONDecoder().decode(Discovery.self, from: data)
        XCTAssertEqual(discovery.capabilities, Array(identifiers.prefix(64)))
        XCTAssertEqual(discovery.capabilities.count, 64)
        XCTAssertFalse(discovery.supportsProfileArchives)
    }

    func testDiscoveryEnvelopeCarriesNormalizedCapabilities() async throws {
        let client = try RivuneAPIClient(
            serverURL: URL(string: "https://example.com")!,
            transport: DiscoveryTransport(),
            credentialStore: DiscoveryCredentialStore()
        )

        let discovery = try await client.discover()
        XCTAssertEqual(discovery.capabilities, ["profile-archives-v1", "future-capability"])
        XCTAssertTrue(discovery.supportsProfileArchives)
    }
}

private struct DiscoveryCredentialStore: CredentialStore {
    func load(for issuer: URL) async throws -> StoredCredentials? { nil }
    func save(_ credentials: StoredCredentials, for issuer: URL) async throws {}
    func clear(for issuer: URL) async throws {}
}

private struct DiscoveryTransport: HTTPTransport {
    func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        let body = Data("""
        {"name":"Rivune","serverVersion":"20.0","protocolVersion":20,"apiBaseUrl":"/api/v1","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en-US","capabilities":["profile-archives-v1","future-capability","profile-archives-v1","not_safe"]}
        """.utf8)
        let response = HTTPURLResponse(
            url: request.url!,
            statusCode: 200,
            httpVersion: nil,
            headerFields: ["Content-Type": "application/json"]
        )!
        return (body, response)
    }
}
