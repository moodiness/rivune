package io.rivune.app

import java.nio.charset.StandardCharsets
import java.security.KeyFactory
import java.security.MessageDigest
import java.time.OffsetDateTime
import java.time.format.DateTimeParseException
import java.security.Signature
import java.security.spec.X509EncodedKeySpec
import java.util.Base64
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.longOrNull
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.OkHttpClient
import okhttp3.Request

internal const val UPDATE_CHECK_INTERVAL_MILLIS = 24L * 60L * 60L * 1_000L
internal const val MAX_UPDATE_MANIFEST_BYTES = 256L * 1_024L
internal const val MAX_UPDATE_APK_BYTES = 512L * 1_024L * 1_024L
internal const val MAX_UPDATE_SIGNATURE_BYTES = 4L * 1_024L
private const val UPDATE_SIGNATURE_ALGORITHM = "ecdsa-p256-sha256"
private const val UPDATE_SIGNING_KEY_ID = "4e9b15a0b6aed77908f3686fbf05a0a9c322ad846662eb758f56d4e65c22796f"
private const val UPDATE_SIGNING_PUBLIC_KEY = "MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEacg8w48bnbKqa/KOJd070if0/100iHsU+o6ecokqIS6p7thhZb1ZR9YawxW7HuoEs5k6dW9sTCOyMjUcsgAQww=="

internal data class AndroidUpdatePackage(
    val applicationId: String,
    val buildVersion: Long,
    val fileName: String,
    val url: String,
    val size: Long,
    val sha256: String,
    val signingCertificateSha256: String,
)

internal data class AppUpdateManifest(
    val version: String,
    val tagName: String,
    val releaseUrl: String,
    val publishedAt: String,
    val androidPackage: AndroidUpdatePackage,
)

internal class InvalidUpdateManifest(message: String) : Exception(message)

internal object AppUpdateSignatureVerifier {
    private val json = Json { ignoreUnknownKeys = false }
    private val sha256 = Regex("^[0-9a-f]{64}$")
    private val publicKeyBytes = Base64.getDecoder().decode(UPDATE_SIGNING_PUBLIC_KEY)
    private val publicKey = KeyFactory.getInstance("EC").generatePublic(X509EncodedKeySpec(publicKeyBytes))

    fun verify(manifest: ByteArray, sidecar: ByteArray) {
        if (sidecar.isEmpty() || sidecar.size > MAX_UPDATE_SIGNATURE_BYTES) throw InvalidUpdateManifest("The update signature size is invalid")
        val text = sidecar.toString(StandardCharsets.UTF_8)
        val propertyNames = Regex("\\\"((?:\\\\.|[^\\\"\\\\])*)\\\"\\s*:").findAll(text).map { it.groupValues[1] }.sorted().toList()
        val expectedProperties = listOf("algorithm", "keyId", "manifestSha256", "schemaVersion", "signature")
        if (propertyNames != expectedProperties) throw InvalidUpdateManifest("The update signature fields are invalid")
        val root = try { json.parseToJsonElement(text).jsonObject }
        catch (_: Exception) { throw InvalidUpdateManifest("The update signature is not valid JSON") }
        if (root.keys != setOf("schemaVersion", "algorithm", "keyId", "manifestSha256", "signature")) {
            throw InvalidUpdateManifest("The update signature fields are invalid")
        }
        if (root.requiredInt("schemaVersion") != 1 || root.requiredString("algorithm") != UPDATE_SIGNATURE_ALGORITHM) {
            throw InvalidUpdateManifest("The update signature contract is unsupported")
        }
        val keyId = root.requiredString("keyId")
        if (!sha256.matches(keyId) || !MessageDigest.isEqual(keyId.toByteArray(), UPDATE_SIGNING_KEY_ID.toByteArray())) {
            throw InvalidUpdateManifest("The update signature key is not trusted")
        }
        val digest = MessageDigest.getInstance("SHA-256").digest(manifest).joinToString("") { "%02x".format(it) }
        val declaredDigest = root.requiredString("manifestSha256")
        if (!sha256.matches(declaredDigest) || !MessageDigest.isEqual(declaredDigest.toByteArray(), digest.toByteArray())) {
            throw InvalidUpdateManifest("The update manifest digest does not match its signature")
        }
        val encoded = root.requiredString("signature")
        val signatureBytes = try { Base64.getDecoder().decode(encoded) }
        catch (_: IllegalArgumentException) { throw InvalidUpdateManifest("The update signature is not valid base64") }
        if (Base64.getEncoder().encodeToString(signatureBytes) != encoded || signatureBytes.isEmpty()) {
            throw InvalidUpdateManifest("The update signature is not canonical base64")
        }
        val verifier = Signature.getInstance("SHA256withECDSA")
        verifier.initVerify(publicKey)
        verifier.update(manifest)
        val valid = try { verifier.verify(signatureBytes) } catch (_: Exception) { false }
        if (!valid) throw InvalidUpdateManifest("The update manifest signature is invalid")
    }

    private fun JsonObject.requiredString(name: String): String =
        (get(name) as? JsonPrimitive)?.takeIf { it.isString }?.contentOrNull?.takeIf { it.isNotBlank() }
            ?: throw InvalidUpdateManifest("Missing signature $name")

    private fun JsonObject.requiredInt(name: String): Int =
        (get(name) as? JsonPrimitive)?.takeUnless { it.isString }?.intOrNull
            ?: throw InvalidUpdateManifest("Missing signature $name")
}

internal object AppUpdateManifestParser {
    private val semVer = Regex("^(0|[1-9]\\d*)\\.(0|[1-9]\\d*)\\.(0|[1-9]\\d*)(?:-[0-9A-Za-z-]+(?:\\.[0-9A-Za-z-]+)*)?(?:\\+[0-9A-Za-z-]+(?:\\.[0-9A-Za-z-]+)*)?$")
    private val sha256 = Regex("^[0-9a-f]{64}$")
    private val safeApkFileName = Regex("^[0-9A-Za-z][0-9A-Za-z._+-]*\\.apk$")
    private val json = Json { ignoreUnknownKeys = true }

    fun parse(value: String): AppUpdateManifest {
        val root = try {
            json.parseToJsonElement(value).jsonObject
        } catch (error: Exception) {
            throw InvalidUpdateManifest("The update manifest is not valid JSON")
        }
        if (root.requiredInt("schemaVersion") != 3) throw InvalidUpdateManifest("Unsupported update manifest schema")
        val channel = root.requiredString("channel")
        if (channel != "stable" && channel != "prerelease") throw InvalidUpdateManifest("Invalid release channel")
        val version = root.requiredString("version")
        if (!semVer.matches(version)) throw InvalidUpdateManifest("Invalid semantic version")
        val prerelease = version.substringBefore('+').substringAfter('-', "")
        if (prerelease.isNotEmpty() && prerelease.split('.').any { it.isEmpty() || (it.all(Char::isDigit) && it.length > 1 && it.startsWith('0')) }) {
            throw InvalidUpdateManifest("Invalid semantic version")
        }
        if (channel != if (prerelease.isEmpty()) "stable" else "prerelease") {
            throw InvalidUpdateManifest("Release channel does not match the version")
        }
        val tagName = root.requiredString("tagName")
        if (tagName != "v$version") throw InvalidUpdateManifest("Release tag does not match the version")
        val publishedAt = root.requiredString("publishedAt")
        try {
            OffsetDateTime.parse(publishedAt)
        } catch (_: DateTimeParseException) {
            throw InvalidUpdateManifest("Invalid publication date")
        }
        val releaseUrl = root.requiredString("releaseUrl")
        if (releaseUrl != "https://github.com/moodiness/rivune/releases/tag/$tagName") {
            throw InvalidUpdateManifest("Release URL does not match the Rivune release tag")
        }
        val packages = root.requiredObject("packages")
        val entry = packages.requiredObject("android")
        if (entry.requiredString("format") != "apk") throw InvalidUpdateManifest("Invalid Android package format")
        val architectures = try { entry["architectures"]?.jsonArray } catch (_: Exception) { null }
            ?: throw InvalidUpdateManifest("Missing Android architectures")
        if (architectures.map { it.jsonPrimitive.content } != listOf("universal")) {
            throw InvalidUpdateManifest("Unsupported Android package architectures")
        }
        if (entry.requiredString("minimumOsVersion") != "8.0") throw InvalidUpdateManifest("Unsupported minimum Android version")
        val buildVersionText = entry.requiredString("buildVersion")
        if (!Regex("^[1-9]\\d*$").matches(buildVersionText)) throw InvalidUpdateManifest("Invalid Android build version")
        val size = entry.requiredLong("size")
        if (size <= 0L || size > MAX_UPDATE_APK_BYTES) throw InvalidUpdateManifest("Invalid Android package size")
        val digest = entry.requiredString("sha256")
        val certificate = entry.requiredString("signingCertificateSha256")
        if (!sha256.matches(digest) || !sha256.matches(certificate)) throw InvalidUpdateManifest("Invalid Android package digest")
        val fileName = entry.requiredString("fileName")
        if (!safeApkFileName.matches(fileName)) throw InvalidUpdateManifest("Invalid Android package file name")
        val url = entry.requiredString("url")
        if (url != "https://github.com/moodiness/rivune/releases/download/$tagName/$fileName") {
            throw InvalidUpdateManifest("Android package URL does not match the Rivune release tag and file name")
        }
        val applicationId = entry.requiredString("applicationId")
        if (applicationId != "io.rivune.app") throw InvalidUpdateManifest("Invalid Android application ID")
        val androidPackage = AndroidUpdatePackage(
            applicationId = applicationId,
            buildVersion = buildVersionText.toLongOrNull() ?: throw InvalidUpdateManifest("Invalid Android build version"),
            fileName = fileName,
            url = url,
            size = size,
            sha256 = digest,
            signingCertificateSha256 = certificate,
        )
        return AppUpdateManifest(version, tagName, releaseUrl, publishedAt, androidPackage)
    }

    private fun JsonObject.requiredString(name: String): String =
        (get(name) as? JsonPrimitive)?.takeIf { it.isString }?.contentOrNull?.takeIf { it.isNotBlank() }
            ?: throw InvalidUpdateManifest("Missing $name")

    private fun JsonObject.requiredInt(name: String): Int =
        (get(name) as? JsonPrimitive)?.takeUnless { it.isString }?.intOrNull
            ?: throw InvalidUpdateManifest("Missing $name")

    private fun JsonObject.requiredLong(name: String): Long =
        (get(name) as? JsonPrimitive)?.takeUnless { it.isString }?.longOrNull
            ?: throw InvalidUpdateManifest("Missing $name")

    private fun JsonObject.requiredObject(name: String): JsonObject =
        get(name) as? JsonObject ?: throw InvalidUpdateManifest("Missing $name")
}


internal fun requireGithubReleaseAssetUrl(value: String) {
    val url = value.toHttpUrlOrNull() ?: throw InvalidUpdateManifest("Invalid package URL")
    if (!url.isHttps || url.host != "github.com" || !Regex("^/moodiness/rivune/releases/download/[^/]+/[^/]+$").matches(url.encodedPath)) {
        throw InvalidUpdateManifest("Package URL must be an HTTPS Rivune GitHub release asset URL")
    }
}

internal interface UpdateCheckCache {
    var etag: String?
    var manifest: String?
    var lastSuccessfulCheckAt: Long
}

internal sealed interface ManifestFetchResult {
    data class Manifest(val value: AppUpdateManifest) : ManifestFetchResult
    data object Throttled : ManifestFetchResult
}

internal class AppUpdateManifestClient(
    private val manifestUrl: String,
    private val cache: UpdateCheckCache,
    private val httpClient: OkHttpClient,
    private val now: () -> Long = System::currentTimeMillis,
) {
    suspend fun fetch(manual: Boolean): ManifestFetchResult = withContext(Dispatchers.IO) {
        val checkedAt = now()
        if (!manual && cache.lastSuccessfulCheckAt > 0L && checkedAt - cache.lastSuccessfulCheckAt in 0 until UPDATE_CHECK_INTERVAL_MILLIS) {
            return@withContext ManifestFetchResult.Throttled
        }
        val url = manifestUrl.toHttpUrlOrNull() ?: throw InvalidUpdateManifest("Invalid update manifest URL")
        requireAllowedManifestUrl(url)
        val request = Request.Builder()
            .url(url)
            .header("Accept", "application/json")
            .apply { cache.etag?.let { header("If-None-Match", it) } }
            .build()
        httpClient.newCall(request).execute().use { response ->
            requireAllowedFinalUrl(response.request.url, "rivune-update.json")
            when (response.code) {
                304 -> {
                    val manifest = cache.manifest ?: throw InvalidUpdateManifest("The update cache is empty")
                    val sidecar = fetchSignature(url)
                    val manifestBytes = manifest.toByteArray(StandardCharsets.UTF_8)
                    AppUpdateSignatureVerifier.verify(manifestBytes, sidecar)
                    val parsed = AppUpdateManifestParser.parse(manifest)
                    cache.lastSuccessfulCheckAt = checkedAt
                    ManifestFetchResult.Manifest(parsed)
                }
                404 -> throw InvalidUpdateManifest("No application update manifest is published")
                200 -> {
                    val body = response.body
                    val declaredLength = body.contentLength()
                    if (declaredLength > MAX_UPDATE_MANIFEST_BYTES) throw InvalidUpdateManifest("The update manifest is too large")
                    val source = body.source()
                    source.request(MAX_UPDATE_MANIFEST_BYTES + 1)
                    if (source.buffer.size > MAX_UPDATE_MANIFEST_BYTES) throw InvalidUpdateManifest("The update manifest is too large")
                    val manifestBytes = source.readByteArray()
                    val sidecar = fetchSignature(url)
                    AppUpdateSignatureVerifier.verify(manifestBytes, sidecar)
                    val text = manifestBytes.toString(StandardCharsets.UTF_8)
                    val parsed = AppUpdateManifestParser.parse(text)
                    cache.manifest = text
                    cache.etag = response.header("ETag")
                    cache.lastSuccessfulCheckAt = checkedAt
                    ManifestFetchResult.Manifest(parsed)
                }
                else -> throw InvalidUpdateManifest("Update server returned HTTP ${response.code}")
            }
        }
    }

    private fun fetchSignature(manifest: HttpUrl): ByteArray {
        val signatureUrl = (manifest.toString() + ".sig").toHttpUrlOrNull()
            ?: throw InvalidUpdateManifest("Invalid update signature URL")
        val request = Request.Builder().url(signatureUrl).header("Accept", "application/json").build()
        httpClient.newCall(request).execute().use { response ->
            requireAllowedFinalUrl(response.request.url, "rivune-update.json.sig")
            if (response.code != 200) throw InvalidUpdateManifest("Update signature server returned HTTP ${response.code}")
            val body = response.body
            if (body.contentLength() > MAX_UPDATE_SIGNATURE_BYTES) throw InvalidUpdateManifest("The update signature is too large")
            val source = body.source()
            source.request(MAX_UPDATE_SIGNATURE_BYTES + 1)
            if (source.buffer.size > MAX_UPDATE_SIGNATURE_BYTES) throw InvalidUpdateManifest("The update signature is too large")
            return source.readByteArray()
        }
    }

    private fun requireAllowedManifestUrl(url: HttpUrl) {
        if (!url.isHttps || url.host != "github.com" || url.encodedPath != "/moodiness/rivune/releases/latest/download/rivune-update.json") {
            throw InvalidUpdateManifest("The update manifest URL is not the HTTPS Rivune global latest-release asset")
        }
    }

    private fun requireAllowedFinalUrl(url: HttpUrl, fileName: String) {
        val assetHost = url.host == "release-assets.githubusercontent.com" || url.host == "objects.githubusercontent.com"
        val githubPath = url.host == "github.com" && url.query == null && (
            url.encodedPath == "/moodiness/rivune/releases/latest/download/$fileName" ||
                Regex("^/moodiness/rivune/releases/download/v[^/]+/${Regex.escape(fileName)}$").matches(url.encodedPath)
            )
        if (!url.isHttps || (!assetHost && !githubPath)) throw InvalidUpdateManifest("The update metadata redirected outside the trusted Rivune GitHub release")
    }
}

internal fun compareSemanticVersions(left: String, right: String): Int {
    fun core(value: String) = value.substringBefore('+').substringBefore('-').split('.').map(String::toLong)
    val leftCore = core(left)
    val rightCore = core(right)
    for (index in 0..2) leftCore[index].compareTo(rightCore[index]).takeIf { it != 0 }?.let { return it }
    val leftPre = left.substringBefore('+').substringAfter('-', "").split('.').filter(String::isNotEmpty)
    val rightPre = right.substringBefore('+').substringAfter('-', "").split('.').filter(String::isNotEmpty)
    if (leftPre.isEmpty() && rightPre.isNotEmpty()) return 1
    if (leftPre.isNotEmpty() && rightPre.isEmpty()) return -1
    for (index in 0 until maxOf(leftPre.size, rightPre.size)) {
        val leftPart = leftPre.getOrNull(index) ?: return -1
        val rightPart = rightPre.getOrNull(index) ?: return 1
        val leftNumber = leftPart.toLongOrNull()
        val rightNumber = rightPart.toLongOrNull()
        val result = when {
            leftNumber != null && rightNumber != null -> leftNumber.compareTo(rightNumber)
            leftNumber != null -> -1
            rightNumber != null -> 1
            else -> leftPart.compareTo(rightPart)
        }
        if (result != 0) return result
    }
    return 0
}
