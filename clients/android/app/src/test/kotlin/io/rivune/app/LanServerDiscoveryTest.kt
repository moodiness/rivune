package io.rivune.app

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

class LanServerDiscoveryTest {
    @Test
    fun parsesSecureAndTrustedLanAnnouncements() {
        val secure = discoveredRivuneServer(
            serviceName = "Living room",
            attributes = mapOf(
                "protocol" to "20".encodeToByteArray(),
                "url" to "https://media.example.com".encodeToByteArray(),
                "version" to "1.10.0".encodeToByteArray(),
            ),
        )
        assertEquals("Living room", secure?.name)
        assertEquals("https://media.example.com", secure?.address)
        assertEquals("1.10.0", secure?.version)
        assertTrue(requireNotNull(secure).usesSecureTransport)

        val local = discoveredRivuneServer(
            serviceName = "Bedroom",
            attributes = mapOf(
                "protocol" to "20".encodeToByteArray(),
                "url" to "http://192.168.1.20:8080/".encodeToByteArray(),
            ),
        )
        assertEquals("http://192.168.1.20:8080", local?.address)
        assertFalse(requireNotNull(local).usesSecureTransport)
    }

    @Test
    fun rejectsIncompatibleUntrustedOrCredentialBearingAnnouncements() {
        listOf(
            "http://media.example.com",
            "http://198.51.100.20:8080",
            "https://user:secret@media.example.com",
            "https://media.example.com/path",
            "https://media.example.com?token=secret",
            "ftp://media.example.com",
        ).forEach { address ->
            assertNull(
                discoveredRivuneServer(
                    serviceName = "Hostile",
                    attributes = mapOf(
                        "protocol" to "20".encodeToByteArray(),
                        "url" to address.encodeToByteArray(),
                    ),
                ),
                address,
            )
        }
        assertNull(
            discoveredRivuneServer(
                serviceName = "Old",
                attributes = mapOf(
                    "protocol" to "19".encodeToByteArray(),
                    "url" to "https://media.example.com".encodeToByteArray(),
                ),
            ),
        )
    }

    @Test
    fun rejectsMissingAndMalformedUtf8Announcements() {
        assertNull(discoveredRivuneServer("Missing", emptyMap()))
        assertNull(
            discoveredRivuneServer(
                "Malformed",
                mapOf(
                    "protocol" to "20".encodeToByteArray(),
                    "url" to byteArrayOf(0xc3.toByte(), 0x28),
                ),
            ),
        )
    }

    @Test
    fun permissionContractCoversDiscoveryAndDirectConnections() {
        assertTrue(requiresLocalNetworkPermission(sdkInt = 37, targetSdk = 37, permissionGranted = false))
        assertFalse(requiresLocalNetworkPermission(sdkInt = 36, targetSdk = 37, permissionGranted = false))
        assertFalse(requiresLocalNetworkPermission(sdkInt = 37, targetSdk = 37, permissionGranted = true))
    }
}
