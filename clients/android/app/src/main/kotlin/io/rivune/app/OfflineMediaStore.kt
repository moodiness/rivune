package io.rivune.app

import android.content.Context
import android.net.Uri
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import androidx.media3.common.C
import androidx.media3.datasource.BaseDataSource
import androidx.media3.datasource.DataSource
import androidx.media3.datasource.DataSpec
import androidx.media3.datasource.DefaultDataSource
import androidx.media3.datasource.TransferListener
import java.io.File
import java.io.FileOutputStream
import java.io.RandomAccessFile
import java.net.HttpURLConnection
import java.net.URL
import java.nio.ByteBuffer
import java.nio.ByteOrder
import java.nio.file.Files
import java.nio.file.StandardCopyOption
import java.security.KeyStore
import java.security.MessageDigest
import java.security.SecureRandom
import java.time.Instant
import java.util.UUID
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.SecretKeyFactory
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.PBEKeySpec
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject

enum class OfflineMediaState { QUEUED, DOWNLOADING, READY, EXPIRED, FAILED }

data class OfflineMediaItem(
    val id: UUID,
    val titleId: UUID,
    val title: String,
    val fileName: String,
    val container: String,
    val sizeBytes: Long,
    val createdAtEpochMs: Long,
    val posterUrl: String?,
    val positionMs: Long = 0,
    val durationMs: Long = 0,
    val completed: Boolean = false,
    val state: OfflineMediaState = OfflineMediaState.READY,
    val downloadedBytes: Long = sizeBytes,
    val expiresAtEpochMs: Long? = null,
)

data class OfflineProfileGate(
    val name: String,
    val scope: String,
    val hasPin: Boolean,
)

internal fun offlineProfileScope(normalizedOrigin: String, profileId: UUID): String =
    MessageDigest.getInstance("SHA-256")
        .digest("$normalizedOrigin\u0000$profileId".toByteArray(Charsets.UTF_8))
        .joinToString("") { "%02x".format(it) }

internal class OfflineMediaStore private constructor(private val root: File) {
    internal constructor(context: Context) : this(File(context.applicationContext.filesDir, "offline-media"))
    internal constructor(rootDirectory: File, testing: Boolean) : this(rootDirectory.also { check(testing) })
    private val gatesFile = File(root, "profiles.json")
    private val reservations = mutableMapOf<UUID, Long>()
    private var cachedArchiveBytes: Long? = null
    internal var archiveScanCount: Int = 0
        private set

    init { check(root.exists() || root.mkdirs()) { "Offline media directory is unavailable" } }

    fun profiles(): List<OfflineProfileGate> = synchronized(this) {
        readGates().mapNotNull { gate ->
            gate.takeIf { stored ->
                readManifest(stored.scope).any { item -> File(scopeDirectory(stored.scope), item.fileName).isFile }
            }?.let { OfflineProfileGate(it.name, it.scope, it.hasPin) }
        }
    }
    fun profileGate(scope: String): OfflineProfileGate? = synchronized(this) {
        readGates().firstOrNull { it.scope == scope }?.let { OfflineProfileGate(it.name, it.scope, it.hasPin) }
    }


    fun registerProfile(normalizedOrigin: String, profileId: UUID, name: String, hasPin: Boolean, pin: String?): String = synchronized(this) {
        val scope = offlineProfileScope(normalizedOrigin, profileId)
        val gates = readGates().toMutableList()
        val existing = gates.firstOrNull { it.scope == scope }
        val verifier = when {
            hasPin -> {
                require(pin?.matches(PIN_PATTERN) == true) { "Offline PIN must contain 4 to 8 digits" }
                createPinVerifier(pin)
            }
            existing?.hasPin == true -> existing.verifier
            else -> null
        }
        gates.removeAll { it.scope == scope }
        gates += StoredGate(name.take(120), scope, hasPin || existing?.hasPin == true, verifier)
        writeGates(gates)
        authorizedScope = scope
        scopeDirectory(scope)
        scope
    }

    fun openRestoredProfile(normalizedOrigin: String, profileId: UUID, name: String, hasPin: Boolean): String? = synchronized(this) {
        val scope = offlineProfileScope(normalizedOrigin, profileId)
        val gates = readGates().toMutableList()
        val existing = gates.firstOrNull { it.scope == scope }
        if (hasPin || existing?.hasPin == true) {
            if (existing == null && hasPin) {
                gates += StoredGate(name.take(120), scope, true, null)
                writeGates(gates)
            }
            authorizedScope = null
            return null
        }
        gates.removeAll { it.scope == scope }
        gates += StoredGate(name.take(120), scope, false, null)
        writeGates(gates)
        authorizedScope = scope
        scopeDirectory(scope)
        scope
    }

    fun unlock(scope: String, pin: String?): Boolean = synchronized(this) {
        val gate = readGates().firstOrNull { it.scope == scope } ?: return false
        if (gate.hasPin && !verifyPin(pin, gate.verifier)) return false
        authorizedScope = scope
        true
    }

    fun lock() { authorizedScope = null }

    fun items(
        scope: String,
        expirationDays: Int = 30,
        nowEpochMs: Long = Instant.now().toEpochMilli(),
    ): List<OfflineMediaItem> = synchronized(this) {
        requireOpen(scope)
        val manifest = readManifest(scope)
        val active = mutableListOf<OfflineMediaItem>()
        var changed = false
        manifest.forEach { item ->
            val expiry = if (expirationDays == 0) null else item.expiresAtEpochMs
                ?: item.createdAtEpochMs + expirationDays.toLong() * 24L * 60L * 60L * 1_000L
            if (expiry != null && nowEpochMs >= expiry) {
                val archive = File(scopeDirectory(scope), item.fileName)
                val beforeDelete = committedArchiveBytesLocked()
                val released = archive.length().takeIf { archive.delete() } ?: 0L
                cachedArchiveBytes = (beforeDelete - released).coerceAtLeast(0L)
                changed = true
            } else {
                val normalized = item.copy(state = OfflineMediaState.READY, expiresAtEpochMs = expiry)
                active += normalized
                if (normalized != item) changed = true
            }
        }
        if (changed) writeManifest(scope, active)
        active.sortedByDescending(OfflineMediaItem::createdAtEpochMs)
    }

    suspend fun download(
        scope: String,
        sourceUrl: String,
        titleId: UUID,
        title: String,
        container: String?,
        posterUrl: String?,
        quotaBytes: Long = DEFAULT_MAXIMUM_STORED_BYTES,
        expirationDays: Int = 30,
        progress: (Long) -> Unit,
    ): OfflineMediaItem = withContext(Dispatchers.IO) {
        requireOpen(scope)
        require(quotaBytes > 0) { "Offline storage quota must be positive" }
        require(expirationDays >= 0) { "Offline expiration must not be negative" }
        val directory = scopeDirectory(scope)
        val id = UUID.randomUUID()
        val partial = File(directory, ".${id}.partial")
        val destination = File(directory, "$id.rvn")
        val writer = EncryptedMediaWriter(partial, offlineKey(scope))
        try {
            val connection = (URL(sourceUrl).openConnection() as? HttpURLConnection) ?: error("Unsupported offline source")
            connection.instanceFollowRedirects = false
            connection.connectTimeout = 15_000
            connection.readTimeout = 30_000
            connection.requestMethod = "GET"
            connection.connect()
            if (connection.responseCode !in 200..299) error("Offline source returned HTTP ${connection.responseCode}")
            val announcedLength = connection.contentLengthLong
            if (!reserve(id, announcedLength.coerceAtLeast(0L), quotaBytes)) error("Offline storage quota reached")
            connection.inputStream.use { input ->
                val buffer = ByteArray(256 * 1024)
                var total = 0L
                while (true) {
                    val count = input.read(buffer)
                    if (count < 0) break
                    if (count == 0) continue
                    total += count
                    writer.append(buffer, count)
                    if (announcedLength <= 0 && !updateReservation(id, partial.length(), quotaBytes)) {
                        error("Offline storage quota reached")
                    }
                    progress(total)
                }
            }
            connection.disconnect()
            writer.finish()
            val createdAt = Instant.now().toEpochMilli()
            val expiresAt = expirationDays.takeIf { it > 0 }?.let { createdAt + it.toLong() * 24L * 60L * 60L * 1_000L }
            val item = synchronized(this@OfflineMediaStore) {
                requireOpen(scope)
                check(reservations[id] != null) { "Offline download reservation is missing" }
                val committedBytes = committedArchiveBytesLocked()
                val otherReservations = reservations.filterKeys { it != id }.values.sum()
                if (partial.length() > quotaBytes - committedBytes - otherReservations) {
                    error("Offline storage quota reached")
                }
                check(partial.renameTo(destination)) { "Could not commit offline media" }
                val committed = OfflineMediaItem(
                    id, titleId, title, destination.name,
                    container?.lowercase()?.ifBlank { "mp4" } ?: "mp4",
                    destination.length(), createdAt, posterUrl,
                    state = OfflineMediaState.READY,
                    downloadedBytes = destination.length(),
                    expiresAtEpochMs = expiresAt,
                )
                reservations.remove(id)
                cachedArchiveBytes = committedBytes + destination.length()
                writeManifest(scope, readManifest(scope).filterNot { it.id == committed.id } + committed)
                committed
            }
            item
        } catch (failure: Throwable) {
            releaseReservation(id)
            writer.closeQuietly()
            destination.delete()
            partial.delete()
            throw failure
        }
    }

    fun remove(scope: String, item: OfflineMediaItem): Boolean = synchronized(this) {
        requireOpen(scope)
        check(item in readManifest(scope)) { "Offline media belongs to another scope" }
        val archive = File(scopeDirectory(scope), item.fileName)
        val beforeDelete = committedArchiveBytesLocked()
        val released = archive.length()
        check(archive.delete()) { "Could not delete offline media" }
        cachedArchiveBytes = (beforeDelete - released).coerceAtLeast(0L)
        writeManifest(scope, readManifest(scope).filterNot { it.id == item.id })
        true
    }

    suspend fun updateProgress(scope: String, id: UUID, positionMs: Long, durationMs: Long, completed: Boolean): OfflineMediaItem = withContext(Dispatchers.IO) {
        synchronized(this@OfflineMediaStore) {
            requireOpen(scope)
            val items = readManifest(scope)
            val current = items.firstOrNull { it.id == id } ?: error("Offline media does not exist in this scope")
            val updated = current.copy(positionMs = positionMs.coerceAtLeast(0), durationMs = durationMs.coerceAtLeast(0), completed = completed)
            writeManifest(scope, items.map { if (it.id == id) updated else it })
            updated
        }
    }

    fun mediaUri(scope: String, item: OfflineMediaItem): String = synchronized(this) {
        requireOpen(scope)
        val stored = readManifest(scope).firstOrNull { it.id == item.id }
        check(stored != null) { "Offline media belongs to another scope" }
        check(stored.state != OfflineMediaState.EXPIRED && stored.expiresAtEpochMs?.let { Instant.now().toEpochMilli() < it } != false) {
            "Offline media has expired"
        }
        "$OFFLINE_SCHEME://$scope/${item.id}"
    }

    internal fun openReader(scope: String, id: UUID): EncryptedMediaReader {
        requireOpen(scope)
        val item = synchronized(this) { readManifest(scope).firstOrNull { it.id == id } }
            ?: error("Offline media does not exist in this scope")
        check(item.state != OfflineMediaState.EXPIRED && item.expiresAtEpochMs?.let { Instant.now().toEpochMilli() < it } != false) {
            "Offline media has expired"
        }
        val archive = File(scopeDirectory(scope), item.fileName)
        check(archive.isFile) { "Offline media file is missing" }
        return EncryptedMediaReader(archive, offlineKey(scope))
    }

    private fun requireOpen(scope: String) {
        require(scope.matches(SCOPE_PATTERN) && authorizedScope == scope) { "Offline profile scope is locked" }
    }

    private fun scopeDirectory(scope: String): File {
        require(scope.matches(SCOPE_PATTERN)) { "Invalid offline profile scope" }
        return File(root, scope).also { check(it.exists() || it.mkdirs()) { "Offline scope directory is unavailable" } }
    }

    private fun manifestFile(scope: String) = File(scopeDirectory(scope), "manifest.json")

    private fun offlineKey(scope: String): SecretKey {
        requireOpen(scope)
        val alias = "$KEY_ALIAS_PREFIX$scope"
        val keyStore = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (keyStore.getKey(alias, null) as? SecretKey)?.let { return it }
        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore")
        generator.init(KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
            .setBlockModes(KeyProperties.BLOCK_MODE_GCM).setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
            .setKeySize(256).setRandomizedEncryptionRequired(true).build())
        return generator.generateKey()
    }

    private fun readManifest(scope: String): List<OfflineMediaItem> {
        val manifest = manifestFile(scope)
        if (!manifest.isFile) return emptyList()
        val array = runCatching { JSONArray(manifest.readText(Charsets.UTF_8)) }.getOrElse { return emptyList() }
        return buildList(array.length()) {
            repeat(array.length()) { index ->
                val value = array.optJSONObject(index) ?: return@repeat
                runCatching { OfflineMediaItem(
                    UUID.fromString(value.getString("id")), UUID.fromString(value.getString("titleId")), value.getString("title"),
                    value.getString("fileName"), value.getString("container"), value.getLong("sizeBytes"), value.getLong("createdAtEpochMs"),
                    value.optString("posterUrl").takeIf(String::isNotBlank), value.optLong("positionMs", 0L).coerceAtLeast(0),
                    value.optLong("durationMs", 0L).coerceAtLeast(0), value.optBoolean("completed", false),
                    state = runCatching { OfflineMediaState.valueOf(value.optString("state", "READY")) }.getOrDefault(OfflineMediaState.READY),
                    downloadedBytes = value.optLong("downloadedBytes", value.getLong("sizeBytes")).coerceAtLeast(0),
                    expiresAtEpochMs = value.optLong("expiresAtEpochMs", -1L).takeIf { it >= 0L },
                ) }.getOrNull()?.let(::add)
            }
        }
    }

    private fun writeManifest(scope: String, items: List<OfflineMediaItem>) {
        val array = JSONArray()
        items.forEach { item -> array.put(JSONObject().put("id", item.id.toString()).put("titleId", item.titleId.toString())
            .put("title", item.title).put("fileName", item.fileName).put("container", item.container).put("sizeBytes", item.sizeBytes)
            .put("createdAtEpochMs", item.createdAtEpochMs).put("posterUrl", item.posterUrl ?: "").put("positionMs", item.positionMs)
            .put("durationMs", item.durationMs).put("completed", item.completed).put("state", item.state.name)
            .put("downloadedBytes", item.downloadedBytes).put("expiresAtEpochMs", item.expiresAtEpochMs ?: -1L)) }
        atomicWrite(manifestFile(scope), array.toString())
    }

    private data class StoredGate(val name: String, val scope: String, val hasPin: Boolean, val verifier: String?)

    private fun readGates(): List<StoredGate> {
        if (!gatesFile.isFile) return emptyList()
        val array = runCatching { JSONArray(gatesFile.readText(Charsets.UTF_8)) }.getOrElse { return emptyList() }
        return buildList(array.length()) { repeat(array.length()) { i ->
            val value = array.optJSONObject(i) ?: return@repeat
            runCatching { StoredGate(value.getString("name"), value.getString("scope"), value.getBoolean("hasPin"), value.optString("verifier").takeIf(String::isNotBlank)) }
                .getOrNull()?.takeIf { it.scope.matches(SCOPE_PATTERN) }?.let(::add)
        } }
    }

    private fun writeGates(gates: List<StoredGate>) {
        val array = JSONArray()
        gates.forEach { array.put(JSONObject().put("name", it.name).put("scope", it.scope).put("hasPin", it.hasPin).put("verifier", it.verifier ?: "")) }
        atomicWrite(gatesFile, array.toString())
    }

    private fun atomicWrite(file: File, value: String) {
        val temporary = File(file.parentFile, ".${file.name}.tmp")
        try {
            FileOutputStream(temporary).use { output -> output.write(value.toByteArray(Charsets.UTF_8)); output.fd.sync() }
            Files.move(
                temporary.toPath(),
                file.toPath(),
                StandardCopyOption.ATOMIC_MOVE,
                StandardCopyOption.REPLACE_EXISTING,
            )
        } catch (failure: Throwable) {
            runCatching { Files.deleteIfExists(temporary.toPath()) }
            throw failure
        }
    }

    internal fun reserve(operationId: UUID, bytes: Long, quotaBytes: Long): Boolean = synchronized(this) {
        if (bytes < 0 || quotaBytes <= 0) return false
        val used = committedArchiveBytesLocked() + reservations.filterKeys { it != operationId }.values.sum()
        if (bytes > quotaBytes - used) return false
        reservations[operationId] = bytes
        true
    }

    internal fun updateReservation(operationId: UUID, bytes: Long, quotaBytes: Long): Boolean = synchronized(this) {
        if (operationId !in reservations || bytes < 0) return false
        val used = committedArchiveBytesLocked() + reservations.filterKeys { it != operationId }.values.sum()
        if (bytes > quotaBytes - used) return false
        reservations[operationId] = bytes
        true
    }

    internal fun releaseReservation(operationId: UUID) = synchronized(this) {
        reservations.remove(operationId)
        cachedArchiveBytes = null
        Unit
    }

    private fun committedArchiveBytesLocked(): Long = cachedArchiveBytes ?: deviceArchiveBytes().also { cachedArchiveBytes = it }

    internal fun deviceArchiveBytes(): Long {
        archiveScanCount += 1
        return root.walkTopDown()
            .filter { it.isFile && it.extension == "rvn" }
            .sumOf(File::length)
    }

    internal fun cleanupOrphans(scope: String): Unit = synchronized(this) {
        requireOpen(scope)
        val manifest = manifestFile(scope)
        if (!manifest.isFile) return
        val parsed = runCatching { JSONArray(manifest.readText(Charsets.UTF_8)) }.getOrNull() ?: return
        val items = readManifest(scope)
        if (items.size != parsed.length()) return
        val referenced = items.mapTo(mutableSetOf(), OfflineMediaItem::fileName)
        scopeDirectory(scope).listFiles().orEmpty().forEach { file ->
            if (file.extension == "rvn" && file.name !in referenced) file.delete()
            if (file.name.endsWith(".partial") && reservations.keys.none { file.name == ".$it.partial" }) file.delete()
        }
        cachedArchiveBytes = null
    }

    private fun createPinVerifier(pin: String): String {
        val salt = ByteArray(16).also(SecureRandom()::nextBytes)
        return salt.toHex() + ":" + derivePin(pin, salt).toHex()
    }

    private fun verifyPin(pin: String?, verifier: String?): Boolean {
        if (pin?.matches(PIN_PATTERN) != true || verifier == null) return false
        val parts = verifier.split(':', limit = 2)
        if (parts.size != 2) return false
        return runCatching { MessageDigest.isEqual(parts[1].hexBytes(), derivePin(pin, parts[0].hexBytes())) }.getOrDefault(false)
    }

    private fun derivePin(pin: String, salt: ByteArray): ByteArray =
        SecretKeyFactory.getInstance("PBKDF2WithHmacSHA256").generateSecret(PBEKeySpec(pin.toCharArray(), salt, 120_000, 256)).encoded

    private fun ByteArray.toHex() = joinToString("") { "%02x".format(it) }
    private fun String.hexBytes() = chunked(2).map { it.toInt(16).toByte() }.toByteArray()

    companion object {
        const val OFFLINE_SCHEME = "rivune-offline"
        internal const val DEFAULT_MAXIMUM_STORED_BYTES = 20L * 1024L * 1024L * 1024L
        private const val KEY_ALIAS_PREFIX = "io.rivune.offline-media.v2."
        private val SCOPE_PATTERN = Regex("^[0-9a-f]{64}$")
        private val PIN_PATTERN = Regex("^[0-9]{4,8}$")
        @Volatile private var authorizedScope: String? = null
    }
}

private object EncryptedMediaFormat {
    val MAGIC = byteArrayOf(0x52, 0x56, 0x4e, 0x31)
    const val HEADER_BYTES = 48
    const val PREFIX_BYTES = 8
    const val TAG_BYTES = 16
    const val CHUNK_BYTES = 1024 * 1024
    const val PAYLOAD_OFFSET = HEADER_BYTES + PREFIX_BYTES + TAG_BYTES
    fun nonce(prefix: ByteArray, index: Int): ByteArray = ByteBuffer.allocate(12).order(ByteOrder.BIG_ENDIAN).put(prefix).putInt(index).array()
    fun header(size: Long, prefix: ByteArray): ByteArray = ByteBuffer.allocate(HEADER_BYTES + PREFIX_BYTES)
        .order(ByteOrder.BIG_ENDIAN)
        .put(MAGIC)
        .putLong(size)
        .put(ByteArray(HEADER_BYTES - 12))
        .put(prefix)
        .array()
}

internal class EncryptedMediaWriter(file: File, private val key: SecretKey) {
    private val output = FileOutputStream(file)
    private val prefix = ByteArray(8).also(SecureRandom()::nextBytes)
    private val buffer = ByteArray(EncryptedMediaFormat.CHUNK_BYTES)
    private var buffered = 0
    private var chunkIndex = 0
    private var plaintextBytes = 0L
    private var closed = false
    init { output.write(ByteArray(EncryptedMediaFormat.PAYLOAD_OFFSET)) }
    fun append(bytes: ByteArray, count: Int) {
        check(!closed); var offset = 0
        while (offset < count) { val copied = minOf(count - offset, buffer.size - buffered); bytes.copyInto(buffer, buffered, offset, offset + copied); buffered += copied; offset += copied; plaintextBytes += copied; if (buffered == buffer.size) flushChunk() }
    }
    fun finish(): Long {
        check(!closed)
        if (buffered > 0) flushChunk()
        output.fd.sync()
        val header = EncryptedMediaFormat.header(plaintextBytes, prefix)
        val headerCipher = Cipher.getInstance("AES/GCM/NoPadding")
        headerCipher.init(Cipher.ENCRYPT_MODE, key, GCMParameterSpec(128, EncryptedMediaFormat.nonce(prefix, -1)))
        headerCipher.updateAAD(header)
        val headerTag = headerCipher.doFinal()
        check(headerTag.size == EncryptedMediaFormat.TAG_BYTES)
        output.channel.position(0)
        output.write(header)
        output.write(headerTag)
        output.fd.sync()
        output.close()
        closed = true
        return plaintextBytes
    }
    fun closeQuietly() { if (!closed) { closed = true; runCatching { output.close() } } }
    private fun flushChunk() { val cipher = Cipher.getInstance("AES/GCM/NoPadding"); cipher.init(Cipher.ENCRYPT_MODE, key, GCMParameterSpec(128, EncryptedMediaFormat.nonce(prefix, chunkIndex))); output.write(cipher.doFinal(buffer, 0, buffered)); buffered = 0; chunkIndex += 1 }
}

internal class EncryptedMediaReader(file: File, private val key: SecretKey) : AutoCloseable {
    private val archive = RandomAccessFile(file, "r")
    private val size: Long
    private val prefix: ByteArray
    private var cachedIndex = -1
    private var cachedPlaintext = ByteArray(0)
    init {
        val header = ByteArray(EncryptedMediaFormat.HEADER_BYTES + EncryptedMediaFormat.PREFIX_BYTES)
        archive.readFully(header)
        val headerBuffer = ByteBuffer.wrap(header).order(ByteOrder.BIG_ENDIAN)
        val magic = ByteArray(4).also(headerBuffer::get)
        check(magic.contentEquals(EncryptedMediaFormat.MAGIC)) { "Invalid offline media" }
        size = headerBuffer.long
        check(size >= 0) { "Invalid offline media length" }
        headerBuffer.position(EncryptedMediaFormat.HEADER_BYTES)
        prefix = ByteArray(EncryptedMediaFormat.PREFIX_BYTES).also(headerBuffer::get)
        val headerTag = ByteArray(EncryptedMediaFormat.TAG_BYTES)
        archive.readFully(headerTag)
        val headerCipher = Cipher.getInstance("AES/GCM/NoPadding")
        headerCipher.init(Cipher.DECRYPT_MODE, key, GCMParameterSpec(128, EncryptedMediaFormat.nonce(prefix, -1)))
        headerCipher.updateAAD(header)
        headerCipher.doFinal(headerTag)
        val chunks = if (size == 0L) 0 else ((size - 1) / EncryptedMediaFormat.CHUNK_BYTES + 1)
        check(archive.length() == EncryptedMediaFormat.PAYLOAD_OFFSET.toLong() + size + chunks * EncryptedMediaFormat.TAG_BYTES) { "Invalid offline archive length" }
    }
    fun length() = size
    fun read(position: Long, target: ByteArray, offset: Int, requested: Int): Int {
        if (position >= size) return C.RESULT_END_OF_INPUT
        var cursor = position; var written = 0; val wanted = minOf(requested.toLong(), size - position).toInt()
        while (written < wanted) { val index = (cursor / EncryptedMediaFormat.CHUNK_BYTES).toInt(); val chunk = chunk(index); val inChunk = (cursor % EncryptedMediaFormat.CHUNK_BYTES).toInt(); val count = minOf(wanted - written, chunk.size - inChunk); chunk.copyInto(target, offset + written, inChunk, inChunk + count); written += count; cursor += count }
        return written
    }
    private fun chunk(index: Int): ByteArray {
        if (cachedIndex == index) return cachedPlaintext
        val plainLength = minOf(EncryptedMediaFormat.CHUNK_BYTES.toLong(), size - index.toLong() * EncryptedMediaFormat.CHUNK_BYTES).toInt()
        val encrypted = ByteArray(plainLength + EncryptedMediaFormat.TAG_BYTES)
        val offset = EncryptedMediaFormat.PAYLOAD_OFFSET.toLong() + index.toLong() * (EncryptedMediaFormat.CHUNK_BYTES + EncryptedMediaFormat.TAG_BYTES)
        archive.seek(offset); archive.readFully(encrypted)
        val cipher = Cipher.getInstance("AES/GCM/NoPadding"); cipher.init(Cipher.DECRYPT_MODE, key, GCMParameterSpec(128, EncryptedMediaFormat.nonce(prefix, index)))
        cachedPlaintext = cipher.doFinal(encrypted); cachedIndex = index; return cachedPlaintext
    }
    override fun close() = archive.close()
}

internal class RivuneDataSourceFactory(context: Context) : DataSource.Factory {
    private val defaults = DefaultDataSource.Factory(context.applicationContext)
    private val offline = OfflineMediaStore(context.applicationContext)
    override fun createDataSource(): DataSource = RoutingDataSource(defaults.createDataSource(), OfflineDataSource(offline))
}

private class RoutingDataSource(private val network: DataSource, private val offline: DataSource) : DataSource {
    private var active: DataSource? = null
    override fun addTransferListener(transferListener: TransferListener) { network.addTransferListener(transferListener); offline.addTransferListener(transferListener) }
    override fun open(dataSpec: DataSpec): Long { check(active == null); active = if (dataSpec.uri.scheme == OfflineMediaStore.OFFLINE_SCHEME) offline else network; return checkNotNull(active).open(dataSpec) }
    override fun read(buffer: ByteArray, offset: Int, length: Int) = checkNotNull(active).read(buffer, offset, length)
    override fun getUri(): Uri? = active?.uri
    override fun close() { active?.close(); active = null }
}

private class OfflineDataSource(private val store: OfflineMediaStore) : BaseDataSource(false) {
    private var reader: EncryptedMediaReader? = null
    private var uri: Uri? = null
    private var position = 0L
    private var bytesRemaining = 0L
    override fun open(dataSpec: DataSpec): Long {
        transferInitializing(dataSpec)
        val scope = dataSpec.uri.host ?: error("Invalid offline scope")
        val id = UUID.fromString(dataSpec.uri.lastPathSegment ?: error("Invalid offline media URI"))
        val opened = store.openReader(scope, id); reader = opened; uri = dataSpec.uri; position = dataSpec.position
        check(position <= opened.length()) { "Offline position is outside media" }
        bytesRemaining = if (dataSpec.length == C.LENGTH_UNSET.toLong()) opened.length() - position else minOf(dataSpec.length, opened.length() - position)
        transferStarted(dataSpec); return bytesRemaining
    }
    override fun read(buffer: ByteArray, offset: Int, length: Int): Int { if (bytesRemaining == 0L) return C.RESULT_END_OF_INPUT; val count = checkNotNull(reader).read(position, buffer, offset, minOf(length.toLong(), bytesRemaining).toInt()); if (count > 0) { position += count; bytesRemaining -= count; bytesTransferred(count) }; return count }
    override fun getUri(): Uri? = uri
    override fun close() { reader?.close(); reader = null; uri = null; position = 0; bytesRemaining = 0; transferEnded() }
}
