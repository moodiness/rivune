package io.rivune.app

import android.app.UiModeManager
import android.content.ContentResolver
import android.content.Context
import android.content.res.Configuration
import android.net.Uri
import android.os.Build
import androidx.activity.result.contract.ActivityResultContracts
import java.io.IOException
import java.io.OutputStreamWriter
import java.net.URI
import java.net.URISyntaxException
import java.nio.charset.StandardCharsets
import java.time.Instant
import java.time.format.DateTimeFormatter
import java.util.ArrayDeque
import java.util.Collections
import java.util.Locale
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

internal const val MAX_DIAGNOSTICS_EVENT_BYTES = 64 * 1_024
internal const val MAX_DIAGNOSTICS_REPORT_BYTES = 64 * 1_024
internal const val DIAGNOSTIC_REPORT_FILE_NAME = "rivune-diagnostics.txt"

private const val MAX_DIAGNOSTIC_SCALAR_BYTES = 512
private const val MAX_DIAGNOSTIC_DISPLAY_CODE_POINTS = 120
private const val MAX_SERVER_URL_LENGTH = 4_096
private const val UNAVAILABLE = "unavailable"
private val DIAGNOSTIC_OPERATION_ID = Regex("^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$")

internal enum class DiagnosticEventCode {
    APP_STARTED,
    SERVER_CONNECTION_STARTED,
    SERVER_CONNECTION_SUCCEEDED,
    SERVER_CONNECTION_FAILED,
    CATALOG_REFRESH_STARTED,
    CATALOG_REFRESH_SUCCEEDED,
    CATALOG_REFRESH_FAILED,
    SEARCH_STARTED,
    SEARCH_SUCCEEDED,
    SEARCH_PARTIAL,
    SEARCH_FAILED,
    SEARCH_CANCELED,
    PLAYBACK_STARTED,
    PLAYBACK_STOPPED,
    PLAYBACK_FAILED,
    UPDATE_CHECK_STARTED,
    UPDATE_AVAILABLE,
    UPDATE_UP_TO_DATE,
    UPDATE_CHECK_FAILED,
    UPDATE_DOWNLOAD_FAILED,
    UPDATE_DOWNLOAD_CANCELED,
    UPDATE_INSTALL_FAILED,
    UPDATE_INSTALL_CANCELED,
    UPDATE_PACKAGE_REJECTED,
    DIAGNOSTIC_EXPORT_SUCCEEDED,
    DIAGNOSTIC_EXPORT_FAILED,
}

internal data class DiagnosticEvent(
    val timestampEpochMillis: Long,
    val code: DiagnosticEventCode,
    val operationId: String? = null,
)

internal class DiagnosticsBuffer(
    private val maxBytes: Int = MAX_DIAGNOSTICS_EVENT_BYTES,
    private val now: () -> Long = System::currentTimeMillis,
) {
    private val lock = Any()
    private val events = ArrayDeque<BufferedEvent>()
    private var bytes = 0

    init {
        require(maxBytes in 1..MAX_DIAGNOSTICS_EVENT_BYTES) {
            "maxBytes must be between 1 and $MAX_DIAGNOSTICS_EVENT_BYTES"
        }
    }

    fun record(code: DiagnosticEventCode) {
        record(code, now())
    }

    fun record(code: DiagnosticEventCode, operationId: String) {
        require(DIAGNOSTIC_OPERATION_ID.matches(operationId)) { "Invalid diagnostic operation ID" }
        record(DiagnosticEvent(now(), code, operationId))
    }

    fun record(code: DiagnosticEventCode, timestampEpochMillis: Long) {
        record(DiagnosticEvent(timestampEpochMillis, code))
    }

    private fun record(event: DiagnosticEvent) {
        val buffered = BufferedEvent(event, serializedEventLine(event).utf8Size())
        synchronized(lock) {
            if (buffered.bytes > maxBytes) return
            while (events.isNotEmpty() && bytes + buffered.bytes > maxBytes) {
                bytes -= events.removeFirst().bytes
            }
            events.addLast(buffered)
            bytes += buffered.bytes
        }
    }

    fun snapshot(): List<DiagnosticEvent> = synchronized(lock) {
        Collections.unmodifiableList(events.map(BufferedEvent::event))
    }

    fun clear() {
        synchronized(lock) {
            events.clear()
            bytes = 0
        }
    }

    private data class BufferedEvent(val event: DiagnosticEvent, val bytes: Int)
}

internal class SearchDiagnosticOperation(
    private val diagnostics: DiagnosticsBuffer,
    val id: String = java.util.UUID.randomUUID().toString(),
) {
    private val lock = Any()
    private var terminalRecorded = false

    init {
        diagnostics.record(DiagnosticEventCode.SEARCH_STARTED, id)
    }

    fun finish(code: DiagnosticEventCode) {
        require(code in SEARCH_TERMINAL_CODES) { "Search diagnostic must use a terminal code" }
        val shouldRecord = synchronized(lock) {
            if (terminalRecorded) false else true.also { terminalRecorded = true }
        }
        if (shouldRecord) diagnostics.record(code, id)
    }
}

private val SEARCH_TERMINAL_CODES = setOf(
    DiagnosticEventCode.SEARCH_SUCCEEDED,
    DiagnosticEventCode.SEARCH_PARTIAL,
    DiagnosticEventCode.SEARCH_FAILED,
    DiagnosticEventCode.SEARCH_CANCELED,
)

internal enum class DiagnosticStartupTab {
    HOME,
    LIBRARY,
    SEARCH,
    CALENDAR,
}

internal enum class DiagnosticPreferredPlayer {
    ASK,
    RIVUNE,
    EXTERNAL,
}

internal enum class DiagnosticAnimationPreference {
    SYSTEM,
    FULL,
    REDUCED,
}

internal data class DiagnosticReportInput(
    val generatedAtEpochMillis: Long,
    val appVersionName: String,
    val appVersionCode: Long,
    val buildType: String,
    val serverUrl: String?,
    val serverDisplayName: String?,
    val serverVersion: String?,
    val serverProtocolVersion: Int?,
    val sdkInt: Int,
    val deviceModel: String,
    val isTelevision: Boolean,
    val startupTab: DiagnosticStartupTab?,
    val preferredPlayer: DiagnosticPreferredPlayer?,
    val animationPreference: DiagnosticAnimationPreference?,
    val accentColor: Int?,
    val frameRateMatching: String?,
    val videoAspect: String?,
    val localQuality: String?,
    val remoteWifiQuality: String?,
    val mobileQuality: String?,
    val events: List<DiagnosticEvent> = emptyList(),
)

internal data class AndroidDiagnosticMetadata(
    val appVersionName: String,
    val appVersionCode: Long,
    val buildType: String,
    val sdkInt: Int,
    val deviceModel: String,
    val isTelevision: Boolean,
)

internal fun collectAndroidDiagnosticMetadata(context: Context): AndroidDiagnosticMetadata =
    AndroidDiagnosticMetadata(
        appVersionName = BuildConfig.VERSION_NAME,
        appVersionCode = BuildConfig.VERSION_CODE.toLong(),
        buildType = BuildConfig.BUILD_TYPE,
        sdkInt = Build.VERSION.SDK_INT,
        deviceModel = Build.MODEL.orEmpty(),
        isTelevision = context.resources.configuration.uiMode and Configuration.UI_MODE_TYPE_MASK ==
            Configuration.UI_MODE_TYPE_TELEVISION ||
            context.getSystemService(UiModeManager::class.java)?.currentModeType == Configuration.UI_MODE_TYPE_TELEVISION,
    )

internal fun sanitizeServerOrigin(value: String?): String? {
    val candidate = value?.trim()?.takeIf { it.isNotEmpty() && it.utf8Size() <= MAX_SERVER_URL_LENGTH } ?: return null
    if (candidate.any { isUnsafeScalarCodePoint(it.code) }) return null
    val uri = try {
        URI(candidate)
    } catch (_: URISyntaxException) {
        return null
    }
    if (!uri.isAbsolute) return null
    val scheme = uri.scheme?.lowercase(Locale.ROOT) ?: return null
    if (scheme != "http" && scheme != "https") return null
    val rawHost = uri.host?.takeIf(String::isNotBlank) ?: return null
    val host = rawHost.removePrefix("[").removeSuffix("]").lowercase(Locale.ROOT)
    if (host.contains('%') || host.any { isUnsafeScalarCodePoint(it.code) }) return null
    val renderedHost = if (host.contains(':')) "[$host]" else host
    val port = uri.port
    if (port > 65_535) return null
    val includePort = port >= 0 && !((scheme == "http" && port == 80) || (scheme == "https" && port == 443))
    val origin = buildString {
        append(scheme)
        append("://")
        append(renderedHost)
        if (includePort) {
            append(':')
            append(port)
        }
    }
    return origin.takeIf { it.utf8Size() <= MAX_SERVER_URL_LENGTH }
}

internal fun buildDiagnosticReport(input: DiagnosticReportInput): String {
    val report = StringBuilder(2_048)
    report.append("Rivune Android diagnostics\n")
    report.append("Report format: 1\n")
    report.appendField("Generated at", formatTimestamp(input.generatedAtEpochMillis))
    report.appendField("App version", safeScalar(input.appVersionName))
    report.appendField("App version code", input.appVersionCode.toString())
    report.appendField("Build type", safeScalar(input.buildType))
    report.appendField("Android API", input.sdkInt.toString())
    report.appendField("Device model", safeScalar(input.deviceModel))
    report.appendField("TV device", if (input.isTelevision) "yes" else "no")
    report.appendField("Server origin", sanitizeServerOrigin(input.serverUrl) ?: UNAVAILABLE)
    report.appendField("Server name", safeScalar(input.serverDisplayName))
    report.appendField("Server version", safeScalar(input.serverVersion))
    report.appendField("Server protocol", input.serverProtocolVersion?.toString() ?: UNAVAILABLE)
    report.appendField("Startup tab", input.startupTab?.stableName())
    report.appendField("Preferred player", input.preferredPlayer?.stableName())
    report.appendField("Animations", input.animationPreference?.stableName())
    report.appendField("Accent color", input.accentColor?.let { String.format(Locale.ROOT, "#%08X", it) })
    report.appendField("Frame-rate matching", safeScalar(input.frameRateMatching))
    report.appendField("Video aspect", safeScalar(input.videoAspect))
    report.appendField("Local quality", safeScalar(input.localQuality))
    report.appendField("Remote Wi-Fi quality", safeScalar(input.remoteWifiQuality))
    report.appendField("Mobile quality", safeScalar(input.mobileQuality))
    report.append("Events:\n")

    var remainingBytes = MAX_DIAGNOSTICS_REPORT_BYTES - report.utf8Size()
    val retainedLines = ArrayDeque<String>()
    for (index in input.events.indices.reversed()) {
        val line = serializedEventLine(input.events[index])
        val lineBytes = line.utf8Size()
        if (lineBytes > remainingBytes) break
        retainedLines.addFirst(line)
        remainingBytes -= lineBytes
    }
    retainedLines.forEach(report::append)
    return report.toString()
}

internal fun diagnosticExportPayload(input: DiagnosticReportInput): String = buildDiagnosticReport(input)

internal fun diagnosticReportDocumentContract(): ActivityResultContracts.CreateDocument =
    ActivityResultContracts.CreateDocument("text/plain")

internal suspend fun exportDiagnosticReport(
    contentResolver: ContentResolver,
    uri: Uri,
    input: DiagnosticReportInput,
): Boolean = withContext(Dispatchers.IO) {
    val report = diagnosticExportPayload(input)
    try {
        val stream = contentResolver.openOutputStream(uri, "wt") ?: return@withContext false
        stream.use {
            OutputStreamWriter(it, StandardCharsets.UTF_8).use { writer ->
                writer.write(report)
            }
        }
        true
    } catch (_: IOException) {
        false
    } catch (_: SecurityException) {
        false
    }
}

private fun StringBuilder.appendField(label: String, value: String?) {
    append(label)
    append(": ")
    append(value ?: UNAVAILABLE)
    append('\n')
}

internal fun sanitizeDiagnosticDisplayField(value: String?): String? {
    val candidate = value?.trim()?.takeIf(String::isNotEmpty) ?: return null
    if (candidate.codePointCount(0, candidate.length) > MAX_DIAGNOSTIC_DISPLAY_CODE_POINTS) return null
    val sanitized = StringBuilder(candidate.length)
    var offset = 0
    while (offset < candidate.length) {
        val codePoint = candidate.codePointAt(offset)
        offset += Character.charCount(codePoint)
        if (!isUnsafeScalarCodePoint(codePoint)) sanitized.appendCodePoint(codePoint)
    }
    return sanitized.toString().trim().takeIf(String::isNotEmpty)
}

private fun safeScalar(value: String?): String {
    if (value == null) return UNAVAILABLE
    val sanitized = StringBuilder(minOf(value.length, MAX_DIAGNOSTIC_SCALAR_BYTES))
    var usedBytes = 0
    var offset = 0
    while (offset < value.length) {
        val codePoint = value.codePointAt(offset)
        offset += Character.charCount(codePoint)
        if (isUnsafeScalarCodePoint(codePoint)) continue
        val codePointBytes = codePoint.utf8Size()
        if (usedBytes + codePointBytes > MAX_DIAGNOSTIC_SCALAR_BYTES) break
        sanitized.appendCodePoint(codePoint)
        usedBytes += codePointBytes
    }
    return sanitized.toString().trim().ifEmpty { UNAVAILABLE }
}

private fun isUnsafeScalarCodePoint(codePoint: Int): Boolean = when (Character.getType(codePoint)) {
    Character.CONTROL.toInt(),
    Character.FORMAT.toInt(),
    Character.LINE_SEPARATOR.toInt(),
    Character.PARAGRAPH_SEPARATOR.toInt(),
    Character.SURROGATE.toInt(),
    -> true
    else -> false
}

private fun Int.utf8Size(): Int = when {
    this <= 0x7F -> 1
    this <= 0x7FF -> 2
    this <= 0xFFFF -> 3
    else -> 4
}

private fun CharSequence.utf8Size(): Int {
    var size = 0
    var offset = 0
    while (offset < length) {
        val codePoint = Character.codePointAt(this, offset)
        offset += Character.charCount(codePoint)
        size += codePoint.utf8Size()
    }
    return size
}

private fun serializedEventLine(event: DiagnosticEvent): String = buildString {
    append(formatTimestamp(event.timestampEpochMillis)).append(' ').append(event.code.name)
    event.operationId?.let { append(" operation=").append(it) }
    append('\n')
}

private fun formatTimestamp(timestampEpochMillis: Long): String =
    DateTimeFormatter.ISO_INSTANT.format(Instant.ofEpochMilli(timestampEpochMillis))

private fun DiagnosticStartupTab.stableName(): String = name.lowercase(Locale.ROOT)

private fun DiagnosticPreferredPlayer.stableName(): String = name.lowercase(Locale.ROOT)

private fun DiagnosticAnimationPreference.stableName(): String = name.lowercase(Locale.ROOT)
