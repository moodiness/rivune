import XCTest
@testable import RivuneAppCore

final class DiagnosticsTests: XCTestCase {
    func testServerAddressIsReducedToOrigin() {
        XCTAssertEqual(
            RivuneDiagnosticsReport.sanitizeServerOrigin(
                "https://diagnostic-user:diagnostic-password@Media.Example:8443/private/profile?access_token=secret#fragment"
            ),
            "https://media.example:8443"
        )
        XCTAssertEqual(RivuneDiagnosticsReport.sanitizeServerOrigin("http://media.example:80/library"), "http://media.example")
        XCTAssertEqual(RivuneDiagnosticsReport.sanitizeServerOrigin("https://[2001:db8::1]:9443/api"), "https://[2001:db8::1]:9443")
        XCTAssertNil(RivuneDiagnosticsReport.sanitizeServerOrigin("file://media.example/private"))
        XCTAssertNil(RivuneDiagnosticsReport.sanitizeServerOrigin("https://media.example\nAuthorization: Bearer secret"))
        let oversizedUnicodeHost = "https://e\(String(repeating: "\u{0301}", count: 4_096)).example"
        XCTAssertNil(RivuneDiagnosticsReport.sanitizeServerOrigin(oversizedUnicodeHost))
    }

    func testReportHasStableAllowlistedMetadataAndNoURLSecrets() {
        let report = RivuneDiagnosticsReport.build(input(events: [
            RivuneDiagnosticEvent(timestampMilliseconds: 1_000, code: .appStarted),
        ]))
        let expected = """
        Rivune Apple diagnostics
        Report format: 1
        Generated at: 1970-01-01T00:00:00Z
        App version: 1.2.3
        App build: 42
        Platform: ios
        OS version: 18.0
        Device model: iPhone17,1
        TV device: no
        Server origin: https://media.example:8443
        Server name: Living Room
        Server version: 10.2.0
        Server protocol: 22
        Startup tab: library
        Preferred player: external
        Embedded player: mpv
        Animations: reduced
        Accent color: rose
        Frame-rate matching: enabled
        Video aspect: zoom
        Local quality: balanced
        Remote Wi-Fi quality: balanced
        Mobile quality: economy
        Events:
        1970-01-01T00:00:01Z APP_STARTED
        """ + "\n"

        XCTAssertEqual(report, expected)
        for secret in ["diagnostic-user", "diagnostic-password", "private-profile", "access_token", "diagnostic-token", "diagnostic-fragment"] {
            XCTAssertFalse(report.contains(secret), "Report leaked \(secret)")
        }
    }

    func testReportIsUTF8BoundedAndScalarValuesCannotInjectLines() {
        let oversized = String(repeating: "🚀", count: rivuneMaximumDiagnosticReportBytes)
        let events = (0..<4_000).map {
            RivuneDiagnosticEvent(timestampMilliseconds: Int64($0), code: .serverConnectionFailed)
        }
        let report = RivuneDiagnosticsReport.build(input(
            appVersion: "1.2.3\nInjected: secret\u{0000}\(oversized)",
            deviceModel: oversized,
            events: events
        ))

        XCTAssertLessThanOrEqual(report.utf8.count, rivuneMaximumDiagnosticReportBytes)
        XCTAssertFalse(report.contains("\nInjected:"))
        XCTAssertFalse(report.contains("\u{0000}"))
        XCTAssertTrue(report.contains("1970-01-01T00:00:03.999Z SERVER_CONNECTION_FAILED\n"))
        XCTAssertFalse(report.contains("1970-01-01T00:00:00Z SERVER_CONNECTION_FAILED\n"))
    }

    func testEventBufferEvictsOldestEntriesWithinByteLimit() {
        let buffer = RivuneDiagnosticsBuffer(maximumBytes: 70)
        buffer.record(.appStarted, timestampMilliseconds: 0)
        buffer.record(.appStarted, timestampMilliseconds: 1_000)
        buffer.record(.appStarted, timestampMilliseconds: 2_000)

        XCTAssertEqual(buffer.snapshot(), [
            RivuneDiagnosticEvent(timestampMilliseconds: 1_000, code: .appStarted),
            RivuneDiagnosticEvent(timestampMilliseconds: 2_000, code: .appStarted),
        ])
    }

    func testSearchLifecycleEventsNeverContainQuerySecrets() {
        let report = RivuneDiagnosticsReport.build(input(events: [
            RivuneDiagnosticEvent(timestampMilliseconds: 1_000, code: .searchStarted),
            RivuneDiagnosticEvent(timestampMilliseconds: 2_000, code: .searchPartial),
            RivuneDiagnosticEvent(timestampMilliseconds: 3_000, code: .searchCanceled),
        ]))

        XCTAssertTrue(report.contains("SEARCH_STARTED"))
        XCTAssertTrue(report.contains("SEARCH_PARTIAL"))
        XCTAssertTrue(report.contains("SEARCH_CANCELED"))
        XCTAssertFalse(report.contains("access_token"))
        XCTAssertFalse(report.contains("private-profile"))
        XCTAssertFalse(report.contains("diagnostic-token"))
    }

    private func input(
        appVersion: String = "1.2.3",
        deviceModel: String = "iPhone17,1",
        events: [RivuneDiagnosticEvent] = []
    ) -> RivuneDiagnosticReportInput {
        RivuneDiagnosticReportInput(
            generatedAtMilliseconds: 0,
            appVersion: appVersion,
            appBuild: "42",
            platform: "ios",
            operatingSystemVersion: "18.0",
            deviceModel: deviceModel,
            isTelevision: false,
            serverAddress: "https://diagnostic-user:diagnostic-password@media.example:8443/private-profile?access_token=diagnostic-token#diagnostic-fragment",
            serverDisplayName: "Living Room",
            serverVersion: "10.2.0",
            serverProtocolVersion: 22,
            startupTab: "library",
            preferredPlayer: "external",
            embeddedPlayer: "mpv",
            animationPreference: "reduced",
            accentColor: "rose",
            frameRateMatching: "enabled",
            videoAspect: "zoom",
            localQuality: "balanced",
            remoteWifiQuality: "balanced",
            mobileQuality: "economy",
            events: events
        )
    }
}
