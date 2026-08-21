package io.rivune.app

import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.security.MessageDigest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertIs
import kotlin.test.assertNull
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
        assertFailsWith<InvalidUpdateManifest> { AppUpdateManifestParser.parse(validManifest().replace("\"schemaVersion\":2", "\"schemaVersion\":1")) }
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
                Response.Builder().request(request).protocol(Protocol.HTTP_1_1).code(status)
                    .message("response").body(body.toResponseBody("application/json".toMediaType())).build()
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
    fun installerConfirmationRequiresMatchingRecordedLiveSessionAndIntent() {
        assertEquals(true, canLaunchInstallationConfirmation(42, 42, sessionExists = true, confirmationPresent = true))
        assertEquals(false, canLaunchInstallationConfirmation(42, 41, sessionExists = true, confirmationPresent = true))
        assertEquals(false, canLaunchInstallationConfirmation(42, 42, sessionExists = false, confirmationPresent = true))
        assertEquals(false, canLaunchInstallationConfirmation(42, 42, sessionExists = true, confirmationPresent = false))
        assertEquals(false, canLaunchInstallationConfirmation(-1, -1, sessionExists = true, confirmationPresent = true))
    }

    private fun responseClient(response: (okhttp3.Request) -> Response): OkHttpClient =
        OkHttpClient.Builder().addInterceptor { response(it.request()) }.build()

    private data class MemoryCache(
        override var etag: String? = null,
        override var manifest: String? = null,
        override var lastSuccessfulCheckAt: Long = 0L,
    ) : UpdateCheckCache

    private fun validManifest(extra: String = "") = """
        {
          "schemaVersion":2,
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
