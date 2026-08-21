package io.rivune.app

import java.io.RandomAccessFile
import javax.crypto.KeyGenerator
import kotlin.io.path.createTempFile
import kotlin.io.path.createTempDirectory
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class OfflineMediaStoreTest {
    @Test
    fun encryptedArchiveRoundTripsAcrossChunkBoundaryAndRejectsTamperedHeader() {
        val file = createTempFile("rivune-offline", ".rvn").toFile()
        try {
            val key = KeyGenerator.getInstance("AES").apply { init(256) }.generateKey()
            val plaintext = ByteArray(1024 * 1024 + 257) { (it % 251).toByte() }
            val writer = EncryptedMediaWriter(file, key)
            writer.append(plaintext, 700_000)
            writer.append(plaintext.copyOfRange(700_000, plaintext.size), plaintext.size - 700_000)
            kotlin.test.assertEquals(plaintext.size.toLong(), writer.finish())

            EncryptedMediaReader(file, key).use { reader ->
                val output = ByteArray(300)
                kotlin.test.assertEquals(300, reader.read(1_048_500, output, 0, output.size))
                assertContentEquals(plaintext.copyOfRange(1_048_500, 1_048_800), output)
            }

            val validArchive = file.readBytes()
            RandomAccessFile(file, "rw").use { archive -> archive.setLength(archive.length() - 1) }
            assertFailsWith<Throwable> { EncryptedMediaReader(file, key) }
            file.writeBytes(validArchive)

            listOf(0L, 4L, 12L, 48L).forEach { offset ->
                file.writeBytes(validArchive)
                RandomAccessFile(file, "rw").use { archive ->
                    archive.seek(offset)
                    val value = archive.readByte()
                    archive.seek(offset)
                    archive.writeByte(value.toInt() xor 1)
                }
                assertFailsWith<Throwable> {
                    EncryptedMediaReader(file, key).use { reader ->
                        reader.read(0, ByteArray(1), 0, 1)
                    }
                }
            }
        } finally {
            file.delete()
        }
    }

    @Test
    fun scopeIdentityIsStableAndDoesNotExposeOriginOrProfileId() {
        val profileId = java.util.UUID.fromString("11111111-1111-4111-8111-111111111111")
        val scope = offlineProfileScope("https://media.example", profileId)

        assertEquals(scope, offlineProfileScope("https://media.example", profileId))
        assertEquals(64, scope.length)
        assertFalse(scope.contains("media.example"))
        assertFalse(scope.contains(profileId.toString()))
        assertFalse(scope == offlineProfileScope("https://other.example", profileId))
    }

    @Test
    fun gatesAreClosedByDefaultAndPinUnlockDoesNotDowngradeProtection() {
        val root = createTempDirectory("rivune-offline-store").toFile()
        try {
            val store = OfflineMediaStore(root, testing = true)
            val profileId = java.util.UUID.randomUUID()
            val scope = store.registerProfile("https://media.example", profileId, "Protected", hasPin = true, pin = "1234")
            store.lock()

            assertFalse(store.unlock(scope, "0000"))
            assertTrue(store.unlock(scope, "1234"))
            store.lock()
            assertEquals(null, store.openRestoredProfile("https://media.example", profileId, "Protected", hasPin = false))
            assertFalse(store.unlock(scope, null))
            assertTrue(store.unlock(scope, "1234"))
            val gateJson = java.io.File(root, "profiles.json").readText()
            assertFalse(gateJson.contains("media.example"))
            assertFalse(gateJson.contains(profileId.toString()))
            assertFalse(gateJson.contains("1234"))
        } finally {
            root.deleteRecursively()
        }
    }

    @Test
    fun emptyGateIsNotOfferedAtOfflineStartup() {
        val root = createTempDirectory("rivune-offline-empty-gate").toFile()
        try {
            val store = OfflineMediaStore(root, testing = true)
            store.registerProfile("https://media.example", java.util.UUID.randomUUID(), "Viewer", hasPin = false, pin = null)

            assertTrue(store.profiles().isEmpty())
            val protectedScope = store.registerProfile(
                "https://protected.example",
                java.util.UUID.randomUUID(),
                "Protected",
                hasPin = true,
                pin = "1234",
            )
            store.lock()
            assertTrue(store.profiles().isEmpty())
            assertEquals(protectedScope, store.profileGate(protectedScope)?.scope)
            assertTrue(store.profileGate(protectedScope)?.hasPin == true)
        } finally {
            root.deleteRecursively()
        }
    }

    @Test
    fun startupListsOnlyScopesWithValidMediaAndRejectsCrossScopeReads() {
        val root = createTempDirectory("rivune-offline-scopes").toFile()
        try {
            val store = OfflineMediaStore(root, testing = true)
            val first = store.registerProfile("https://one.example", java.util.UUID.randomUUID(), "One", hasPin = false, pin = null)
            store.lock()
            val second = store.registerProfile("https://two.example", java.util.UUID.randomUUID(), "Two", hasPin = false, pin = null)
            val id = java.util.UUID.randomUUID()
            val titleId = java.util.UUID.randomUUID()
            val secondDirectory = java.io.File(root, second).apply { mkdirs() }
            java.io.File(secondDirectory, "$id.rvn").writeBytes(byteArrayOf(1))
            java.io.File(secondDirectory, "manifest.json").writeText(
                """[{"id":"$id","titleId":"$titleId","title":"Saved","fileName":"$id.rvn","container":"mp4","sizeBytes":1,"createdAtEpochMs":1,"posterUrl":""}]""",
            )
            store.lock()

            assertEquals(listOf(second), store.profiles().map(OfflineProfileGate::scope))
            assertTrue(store.unlock(second, null))
            assertEquals("Saved", store.items(second).single().title)
            assertFailsWith<IllegalArgumentException> { store.items(first) }
        } finally {
            root.deleteRecursively()
        }
    }
}
