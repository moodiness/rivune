package io.rivune.app

import java.nio.charset.StandardCharsets
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

class DiagnosticsTest {
    @Test
    fun serverUrlIsReducedToItsOrigin() {
        assertEquals(
            "https://media.example:8443",
            sanitizeServerOrigin(
                "https://admin:p%40ss@Media.Example:8443/private/profile" +
                    "?api_key=super-secret#session-token",
            ),
        )
        assertEquals("http://media.example", sanitizeServerOrigin("http://media.example:80/library"))
        assertEquals("https://media.example", sanitizeServerOrigin("https://media.example:443/library"))
        assertEquals("https://[2001:db8::1]:9443", sanitizeServerOrigin("https://[2001:db8::1]:9443/api"))
    }

    @Test
    fun unsafeOrNonServerUrlsAreRejected() {
        assertNull(sanitizeServerOrigin(null))
        assertNull(sanitizeServerOrigin("/api/v1"))
        assertNull(sanitizeServerOrigin("file://media.example/private"))
        assertNull(sanitizeServerOrigin("https://media.example\nAuthorization: Bearer secret"))
        assertNull(sanitizeServerOrigin("https://[fe80::1%25en0]/private"))
        assertNull(sanitizeServerOrigin("https://e${"\u0301".repeat(4_096)}.example"))
        assertNull(sanitizeServerOrigin("https://"))
    }

    @Test
    fun displayFieldsStripUnicodeControlsAndRejectOversizedValues() {
        assertEquals("Rivune Home", sanitizeDiagnosticDisplayField(" Rivune\u202E\u2028 Home "))
        assertNull(sanitizeDiagnosticDisplayField("\u202E\u2028"))
        assertNull(sanitizeDiagnosticDisplayField("x".repeat(121)))
    }

    @Test
    fun reportNeverIncludesServerCredentialsPathQueryOrFragment() {
        val input = reportInput(
            serverUrl = "https://diagnostic-user:diagnostic-password@media.example:8443/" +
                "users/private-profile?access_token=diagnostic-token#diagnostic-fragment",
        )

        val report = buildDiagnosticReport(input)

        assertTrue("Server origin: https://media.example:8443\n" in report)
        listOf(
            "diagnostic-user",
            "diagnostic-password",
            "private-profile",
            "access_token",
            "diagnostic-token",
            "diagnostic-fragment",
        ).forEach { secret -> assertFalse(secret in report, "Report leaked $secret") }
    }

    @Test
    fun reportMetadataHasStableNamesOrderAndFormatting() {
        val input = reportInput(
            events = listOf(DiagnosticEvent(1_000, DiagnosticEventCode.APP_STARTED)),
        )

        val expected = """
            Rivune Android diagnostics
            Report format: 1
            Generated at: 1970-01-01T00:00:00Z
            App version: 1.2.3
            App version code: 42
            Build type: release
            Android API: 35
            Device model: Phone
            TV device: no
            Server origin: https://media.example:8443
            Server name: Living Room
            Server version: 10.2.0
            Server protocol: 20
            Startup tab: library
            Preferred player: external
            Animations: reduced
            Accent color: #FF336699
            Frame-rate matching: on
            Video aspect: zoom
            Wi-Fi quality: balanced
            Mobile quality: economy
            Events:
            1970-01-01T00:00:01Z APP_STARTED
        """.trimIndent() + "\n"

        assertEquals(expected, buildDiagnosticReport(input))
        assertEquals(expected, buildDiagnosticReport(input))
    }

    @Test
    fun reportIsUtf8BoundedAndScalarValuesCannotInjectLines() {
        val oversized = "\uD83D\uDE80".repeat(MAX_DIAGNOSTICS_REPORT_BYTES)
        val events = List(4_000) {
            DiagnosticEvent(it.toLong(), DiagnosticEventCode.SERVER_CONNECTION_FAILED)
        }
        val report = buildDiagnosticReport(
            reportInput(
                appVersionName = "1.2.3\nInjected: secret\u0000" + oversized,
                deviceModel = oversized,
                events = events,
            ),
        )

        assertTrue(report.toByteArray(StandardCharsets.UTF_8).size <= MAX_DIAGNOSTICS_REPORT_BYTES)
        assertFalse("\nInjected:" in report)
        assertFalse('\u0000' in report)
        assertTrue("1970-01-01T00:00:03.999Z SERVER_CONNECTION_FAILED\n" in report)
        assertFalse("1970-01-01T00:00:00Z SERVER_CONNECTION_FAILED\n" in report)
    }

    @Test
    fun eventBufferEvictsOldestEntriesToRemainWithinItsByteBound() {
        val buffer = DiagnosticsBuffer(maxBytes = 74)

        buffer.record(DiagnosticEventCode.APP_STARTED, 0)
        buffer.record(DiagnosticEventCode.APP_STARTED, 1_000)
        buffer.record(DiagnosticEventCode.APP_STARTED, 2_000)

        assertEquals(
            listOf(
                DiagnosticEvent(1_000, DiagnosticEventCode.APP_STARTED),
                DiagnosticEvent(2_000, DiagnosticEventCode.APP_STARTED),
            ),
            buffer.snapshot(),
        )
    }

    @Test
    fun exportPayloadIsExactlyTheStableReport() {
        val input = reportInput()

        assertEquals(buildDiagnosticReport(input), diagnosticExportPayload(input))
    }

    private fun reportInput(
        appVersionName: String = "1.2.3",
        serverUrl: String? = "https://media.example:8443/api/v1?token=discarded",
        deviceModel: String = "Phone",
        events: List<DiagnosticEvent> = emptyList(),
    ) = DiagnosticReportInput(
        generatedAtEpochMillis = 0,
        appVersionName = appVersionName,
        appVersionCode = 42,
        buildType = "release",
        serverUrl = serverUrl,
        serverDisplayName = "Living Room",
        serverVersion = "10.2.0",
        serverProtocolVersion = 20,
        sdkInt = 35,
        deviceModel = deviceModel,
        isTelevision = false,
        startupTab = DiagnosticStartupTab.LIBRARY,
        preferredPlayer = DiagnosticPreferredPlayer.EXTERNAL,
        animationPreference = DiagnosticAnimationPreference.REDUCED,
        accentColor = 0xFF336699.toInt(),
        frameRateMatching = "on",
        videoAspect = "zoom",
        wifiQuality = "balanced",
        mobileQuality = "economy",
        events = events,
    )
}
