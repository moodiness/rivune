package io.rivune.app

import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.security.MessageDigest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertIs
import kotlin.test.assertNull
import kotlin.test.assertFalse
import kotlin.test.assertTrue
import kotlinx.coroutines.Job
import okhttp3.Interceptor
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.Response
import okhttp3.ResponseBody.Companion.toResponseBody
import kotlinx.coroutines.runBlocking

class AppUpdateTest {
    @Test
    fun developmentBuildUsesUnavailableUpdateState() {
        assertIs<AppUpdateState.Unavailable>(restingUpdateState(enabled = false))
        assertIs<AppUpdateState.Idle>(restingUpdateState(enabled = true))
    }

    @Test
    fun parsesGlobalManifestAndroidEntryAndIgnoresAdditionalFields() {
        val manifest = AppUpdateManifestParser.parse(validManifest(extra = ",\"future\":true"))

        assertEquals("1.2.3", manifest.version)
        assertEquals(42L, manifest.androidPackage.buildVersion)
        assertEquals("io.rivune.app", manifest.androidPackage.applicationId)
        assertEquals("Rivune-Android.apk", manifest.androidPackage.fileName)
    }

    @Test
    fun rejectsUnknownSchemaMissingAndroidEntryAndUnsafeUrls() {
        assertFailsWith<InvalidUpdateManifest> { AppUpdateManifestParser.parse(validManifest().replace("\"schemaVersion\":3", "\"schemaVersion\":1")) }
        assertFailsWith<InvalidUpdateManifest> { AppUpdateManifestParser.parse(validManifest().replace("\"publishedAt\":\"2026-08-14T10:00:00Z\",", "")) }
        assertFailsWith<InvalidUpdateManifest> { AppUpdateManifestParser.parse(validManifest().replace("\"android\":{", "\"other\":{")) }
        assertFailsWith<InvalidUpdateManifest> { AppUpdateManifestParser.parse(validManifest().replace("https://github.com/moodiness/rivune/releases/download/v1.2.3/Rivune-Android.apk", "https://evil.example/Rivune-Android.apk")) }
        assertFailsWith<InvalidUpdateManifest> { AppUpdateManifestParser.parse(validManifest().replace("https://github.com/moodiness/rivune/releases/tag/v1.2.3", "https://github.com/other/rivune/releases/tag/v1.2.3")) }
        assertFailsWith<InvalidUpdateManifest> { AppUpdateManifestParser.parse(validManifest().replace("\"fileName\":\"Rivune-Android.apk\"", "\"fileName\":\"../rivune.apk\"")) }
        assertFailsWith<InvalidUpdateManifest> { AppUpdateManifestParser.parse(validManifest().replace("\"version\":\"1.2.3\"", "\"version\":\"v1.2.3\"")) }
        assertFailsWith<InvalidUpdateManifest> { AppUpdateManifestParser.parse(validManifest().replace("\"tagName\":\"v1.2.3\"", "\"tagName\":\"v1.2.4\"")) }
        assertFailsWith<InvalidUpdateManifest> { AppUpdateManifestParser.parse(validManifest().replace("\"channel\":\"stable\"", "\"channel\":\"prerelease\"")) }
        assertFailsWith<InvalidUpdateManifest> { AppUpdateManifestParser.parse(validManifest().replace("\"buildVersion\":\"42\"", "\"buildVersion\":\"042\"")) }
        assertFailsWith<InvalidUpdateManifest> { AppUpdateManifestParser.parse(validManifest().replace("\"applicationId\":\"io.rivune.app\"", "\"applicationId\":\"io.other.app\"")) }
        assertFailsWith<InvalidUpdateManifest> { AppUpdateManifestParser.parse(validManifest().replace("${"b".repeat(64)}", "${"B".repeat(64)}")) }
    }

    @Test
    fun automaticChecksThrottleWhileManualChecksBypassCacheAndSendEtag() = runBlocking {
        var requests = 0
        var ifNoneMatch: String? = null
        val transport = Interceptor { chain ->
            if (chain.request().url.encodedPath.endsWith(".sig")) {
                return@Interceptor Response.Builder()
                    .request(chain.request()).protocol(Protocol.HTTP_1_1).code(200).message("OK")
                    .body(validSignature().toResponseBody("application/json".toMediaType())).build()
            }
            requests += 1
            ifNoneMatch = chain.request().header("If-None-Match")
            Response.Builder()
                .request(chain.request())
                .protocol(Protocol.HTTP_1_1)
                .code(200)
                .message("OK")
                .header("ETag", "manifest-1")
                .body(validManifest().toResponseBody("application/json".toMediaType()))
                .build()
        }
        val cache = MemoryCache()
        var now = 1_000_000L
        val client = AppUpdateManifestClient(
            "https://github.com/moodiness/rivune/releases/latest/download/rivune-update.json",
            cache,
            OkHttpClient.Builder().addInterceptor(transport).build(),
        ) { now }

        assertIs<ManifestFetchResult.Manifest>(client.fetch(manual = false))
        assertEquals(now, cache.lastSuccessfulCheckAt)
        assertIs<ManifestFetchResult.Throttled>(client.fetch(manual = false))
        assertEquals(1, requests)

        assertIs<ManifestFetchResult.Manifest>(client.fetch(manual = true))
        assertEquals(2, requests)
        assertEquals("manifest-1", ifNoneMatch)
        assertEquals("manifest-1", cache.etag)
    }

    @Test
    fun missingOrInvalidManifestDoesNotReplaceCacheOrThrottleRetries() = runBlocking {
        var status = 404
        var body = ""
        val cache = MemoryCache(manifest = validManifest())
        val client = AppUpdateManifestClient(
            "https://github.com/moodiness/rivune/releases/latest/download/rivune-update.json",
            cache,
            responseClient { request ->
                val responseBody = if (request.url.encodedPath.endsWith(".sig")) validSignature() else body
                Response.Builder().request(request).protocol(Protocol.HTTP_1_1).code(if (request.url.encodedPath.endsWith(".sig")) 200 else status)
                    .message("response").body(responseBody.toResponseBody("application/json".toMediaType())).build()
            },
        ) { 2_000_000L }

        assertFailsWith<InvalidUpdateManifest> { client.fetch(manual = true) }
        assertEquals(0L, cache.lastSuccessfulCheckAt)
        body = "{not-json}"
        status = 200
        assertFailsWith<InvalidUpdateManifest> { client.fetch(manual = true) }
        assertEquals(0L, cache.lastSuccessfulCheckAt)
        assertEquals(validManifest(), cache.manifest)
        assertNull(cache.etag)
    }

    @Test
    fun missingOversizedAndRedirectedSidecarsFailClosed() = runBlocking {
        var signatureStatus = 404
        var signatureBody = ""
        var redirectSignature = false
        val client = AppUpdateManifestClient(
            "https://github.com/moodiness/rivune/releases/latest/download/rivune-update.json",
            MemoryCache(),
            responseClient { request ->
                val isSignature = request.url.encodedPath.endsWith(".sig")
                val responseRequest = if (isSignature && redirectSignature) {
                    request.newBuilder().url("https://evil.example/rivune-update.json.sig").build()
                } else request
                Response.Builder().request(responseRequest).protocol(Protocol.HTTP_1_1)
                    .code(if (isSignature) signatureStatus else 200).message("response")
                    .body((if (isSignature) signatureBody else validManifest()).toResponseBody("application/json".toMediaType())).build()
            },
        )
        assertFailsWith<InvalidUpdateManifest> { client.fetch(manual = true) }
        signatureStatus = 200
        signatureBody = " ".repeat((MAX_UPDATE_SIGNATURE_BYTES + 1).toInt())
        assertFailsWith<InvalidUpdateManifest> { client.fetch(manual = true) }
        signatureBody = validSignature()
        redirectSignature = true
        assertFailsWith<InvalidUpdateManifest> { client.fetch(manual = true) }
        Unit
    }

    @Test
    fun streamingVerifierEnforcesExactSizeAndChecksum() {
        val bytes = "verified apk bytes".encodeToByteArray()
        val digest = MessageDigest.getInstance("SHA-256").digest(bytes).joinToString("") { "%02x".format(it) }
        val output = ByteArrayOutputStream()

        copyAndVerifyUpdate(ByteArrayInputStream(bytes), output, bytes.size.toLong(), digest)
        assertEquals(bytes.toList(), output.toByteArray().toList())
        assertFailsWith<InvalidUpdateManifest> {
            copyAndVerifyUpdate(ByteArrayInputStream(bytes), ByteArrayOutputStream(), bytes.size.toLong() + 1, digest)
        }
        assertFailsWith<InvalidUpdateManifest> {
            copyAndVerifyUpdate(ByteArrayInputStream(bytes), ByteArrayOutputStream(), bytes.size.toLong(), "0".repeat(64))
        }
    }

    @Test
    fun signatureVerifierRejectsAlteredManifestKeyAndEncoding() {
        val manifest = validManifest().encodeToByteArray()
        AppUpdateSignatureVerifier.verify(manifest, validSignature().encodeToByteArray())
        assertFailsWith<InvalidUpdateManifest> {
            AppUpdateSignatureVerifier.verify(manifest + '!'.code.toByte(), validSignature().encodeToByteArray())
        }
        assertFailsWith<InvalidUpdateManifest> {
            AppUpdateSignatureVerifier.verify(manifest, validSignature().replace(UPDATE_SIGNING_KEY_ID_FOR_TEST, "0".repeat(64)).encodeToByteArray())
        }
        assertFailsWith<InvalidUpdateManifest> {
            AppUpdateSignatureVerifier.verify(manifest, validSignature().replace("MEUCIA", "%%%CIA").encodeToByteArray())
        }
        assertFailsWith<InvalidUpdateManifest> {
            AppUpdateSignatureVerifier.verify(manifest, ByteArray((MAX_UPDATE_SIGNATURE_BYTES + 1).toInt()))
        }
    }

    @Test
    fun manifestTransitionsToAvailableOnlyForNewerMatchingPackage() {
        val manifest = AppUpdateManifestParser.parse(validManifest())
        assertIs<AppUpdateState.Available>(
            resolveUpdateManifest(manifest, "io.rivune.app", 41L, "1.2.2", manual = false),
        )
        assertIs<AppUpdateState.UpToDate>(
            resolveUpdateManifest(manifest, "io.rivune.app", 42L, "1.2.3", manual = true),
        )
        assertIs<AppUpdateState.Idle>(
            resolveUpdateManifest(manifest, "other.application", 1L, "0.1.0", manual = false),
        )
    }

    @Test
    fun semanticVersionComparisonRejectsEqualOrOlderVersions() {
        assertEquals(1, compareSemanticVersions("1.2.0", "1.1.9"))
        assertEquals(0, compareSemanticVersions("1.2.0+build", "1.2.0"))
        assertEquals(-1, compareSemanticVersions("1.2.0-rc.1", "1.2.0"))
    }

    @Test
    fun updateNoticeDedupOnlyAllowsStrictlyNewerVersions() {
        assertEquals(true, shouldPresentUpdateNotice(null, "1.2.3"))
        assertEquals(false, shouldPresentUpdateNotice("1.2.3", "1.2.3"))
        assertEquals(true, shouldPresentUpdateNotice("1.2.3", "1.2.4"))
        assertEquals(false, shouldPresentUpdateNotice("1.2.4", "1.2.3"))
        assertEquals(false, shouldPresentUpdateNotice("invalid", "1.2.4"))
    }

    @Test
    fun installerConfirmationRequiresMatchingRecordedLiveSessionAndIntent() {
        assertEquals(true, canLaunchInstallationConfirmation(42, 42, sessionExists = true, confirmationPresent = true))
        assertEquals(false, canLaunchInstallationConfirmation(42, 41, sessionExists = true, confirmationPresent = true))
        assertEquals(false, canLaunchInstallationConfirmation(42, 42, sessionExists = false, confirmationPresent = true))
        assertEquals(false, canLaunchInstallationConfirmation(42, 42, sessionExists = true, confirmationPresent = false))
        assertEquals(false, canLaunchInstallationConfirmation(-1, -1, sessionExists = true, confirmationPresent = true))
    }

    @Test
    fun cancellingActiveDownloadCancelsTransportJobAndPartialFiles() {
        val directory = kotlin.io.path.createTempDirectory("rivune-update-cancel").toFile()
        try {
            val partial = directory.resolve("update.apk.part").apply { writeBytes(byteArrayOf(1, 2, 3)) }
            val complete = directory.resolve("update.apk").apply { writeBytes(byteArrayOf(4)) }
            var transportCancelled = false
            val job = Job()

            ActiveUpdateDownload(job, { transportCancelled = true }, listOf(partial, complete)).cancel()

            assertTrue(transportCancelled)
            assertTrue(job.isCancelled)
            assertFalse(partial.exists())
            assertFalse(complete.exists())
        } finally {
            directory.deleteRecursively()
        }
    }

    @Test
    fun cancellingInstallPreparationCancelsCopySessionAndPackage() {
        val directory = kotlin.io.path.createTempDirectory("rivune-install-cancel").toFile()
        try {
            val apk = directory.resolve("update.apk").apply { writeBytes(byteArrayOf(1, 2, 3)) }
            val job = Job()
            var sessionAbandoned = false

            ActiveInstallPreparation(job, { sessionAbandoned = true }, apk).cancel()

            assertTrue(sessionAbandoned)
            assertTrue(job.isCancelled)
            assertFalse(apk.exists())
        } finally {
            directory.deleteRecursively()
        }
    }

    @Test
    fun updateDialogKeepsCancelFocusUntilSystemHandoff() {
        val manifest = AppUpdateManifestParser.parse(validManifest())
        val apk = kotlin.io.path.createTempFile("rivune-focus", ".apk").toFile()
        try {
            assertTrue(shouldFocusUpdateCancel(AppUpdateState.Downloading(manifest, manifest.androidPackage)))
            assertTrue(shouldFocusUpdateCancel(AppUpdateState.PreparingInstallation(apk)))
            assertFalse(shouldFocusUpdateCancel(AppUpdateState.Installing))
            assertFalse(canDismissUpdateDialog(AppUpdateState.PreparingInstallation(apk)))
            assertFalse(canDismissUpdateDialog(AppUpdateState.Installing))
            assertTrue(shouldShowUpdateDialog(AppUpdateState.PreparingInstallation(apk)))
            assertFalse(shouldShowUpdateDialog(AppUpdateState.Installing))
            assertTrue(canDismissUpdateDialog(AppUpdateState.ReadyToInstall(manifest, manifest.androidPackage, apk)))
        } finally {
            apk.delete()
        }
    }
    @Test
    fun transportIOExceptionAfterCancelCannotEmitSecondTerminal() {
        val manifest = AppUpdateManifestParser.parse(validManifest())
        val downloading = AppUpdateState.Downloading(manifest, manifest.androidPackage)
        val transportCancellationError = java.io.IOException("Canceled")

        assertEquals(DiagnosticEventCode.UPDATE_DOWNLOAD_FAILED, updateDownloadFailureCode(transportCancellationError))
        assertFalse(shouldRecordUpdateDownloadFailure(AppUpdateState.Idle, coroutineActive = true))
        assertFalse(shouldRecordUpdateDownloadFailure(downloading, coroutineActive = false))
        assertTrue(shouldRecordUpdateDownloadFailure(downloading, coroutineActive = true))
        assertFalse(shouldRecordUpdateInstallFailure(AppUpdateState.Idle, coroutineActive = true))
    }


    @Test
    fun updateTerminalDiagnosticsUseClosedPhaseCodes() {
        assertEquals(DiagnosticEventCode.UPDATE_DOWNLOAD_FAILED, updateDownloadFailureCode(java.io.IOException("network")))
        assertEquals(DiagnosticEventCode.UPDATE_PACKAGE_REJECTED, updateDownloadFailureCode(InvalidUpdateManifest("invalid package")))
        assertEquals(DiagnosticEventCode.UPDATE_INSTALL_FAILED, installationFailureCode(android.content.pm.PackageInstaller.STATUS_FAILURE))
        assertNull(installationFailureCode(android.content.pm.PackageInstaller.STATUS_SUCCESS))
        assertTrue(DiagnosticEventCode.UPDATE_DOWNLOAD_CANCELED.name.matches(Regex("^UPDATE_[A-Z_]+$")))
        val manifest = AppUpdateManifestParser.parse(validManifest())
        var state: AppUpdateState = AppUpdateState.Downloading(manifest, manifest.androidPackage)
        val recorded = buildList {
            repeat(2) {
                updateCancellationCode(state)?.let(::add)
                state = AppUpdateState.Idle
            }
        }
        assertEquals(listOf(DiagnosticEventCode.UPDATE_DOWNLOAD_CANCELED), recorded)
        assertTrue(recorded.none { it == DiagnosticEventCode.UPDATE_DOWNLOAD_FAILED || it == DiagnosticEventCode.UPDATE_PACKAGE_REJECTED })
        assertTrue(DiagnosticEventCode.UPDATE_INSTALL_CANCELED.name.matches(Regex("^UPDATE_[A-Z_]+$")))
        assertTrue(DiagnosticEventCode.UPDATE_INSTALL_CANCELED != DiagnosticEventCode.UPDATE_INSTALL_FAILED)
    }

    private fun responseClient(response: (okhttp3.Request) -> Response): OkHttpClient =
        OkHttpClient.Builder().addInterceptor { response(it.request()) }.build()

    private data class MemoryCache(
        override var etag: String? = null,
        override var manifest: String? = null,
        override var lastSuccessfulCheckAt: Long = 0L,
    ) : UpdateCheckCache

    private companion object {
        const val UPDATE_SIGNING_KEY_ID_FOR_TEST = "4e9b15a0b6aed77908f3686fbf05a0a9c322ad846662eb758f56d4e65c22796f"
    }
    private fun validSignature() = """{"schemaVersion":1,"algorithm":"ecdsa-p256-sha256","keyId":"4e9b15a0b6aed77908f3686fbf05a0a9c322ad846662eb758f56d4e65c22796f","manifestSha256":"e0404a37ee9fb66fec045f80460a920b83c679c5867f1492c18f65dcfc85799f","signature":"MEUCIAIplcAe0LpTG5S/BpsnKiDu+Ud1BPLFlFcJHYdoTycBAiEAlbjrGRLCGQ/18R26MmHLzTKseWWqzLNJc0zcL7Guy+o="}"""

    private fun validManifest(extra: String = "") = """
        {
          "schemaVersion":3,
          "channel":"stable",
          "version":"1.2.3",
          "tagName":"v1.2.3",
          "publishedAt":"2026-08-14T10:00:00Z",
          "releaseUrl":"https://github.com/moodiness/rivune/releases/tag/v1.2.3",
          "packages":{
            "android":{
              "format":"apk",
              "architectures":["universal"],
              "applicationId":"io.rivune.app",
              "buildVersion":"42",
              "minimumOsVersion":"8.0",
              "fileName":"Rivune-Android.apk",
              "url":"https://github.com/moodiness/rivune/releases/download/v1.2.3/Rivune-Android.apk",
              "size":18,
              "sha256":"${"a".repeat(64)}",
              "signingCertificateSha256":"${"b".repeat(64)}",
              "futureAndroidField":true
            },
            "windows":{"format":"exe"},
            "futurePlatform":{"format":"future"}
          }
          $extra
        }
    """.trimIndent()
}
