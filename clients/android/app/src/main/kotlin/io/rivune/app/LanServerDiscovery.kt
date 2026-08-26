package io.rivune.app

import android.content.Context
import android.net.nsd.NsdManager
import android.net.nsd.NsdServiceInfo
import android.os.Handler
import android.os.Looper
import io.rivune.api.RivuneProtocol
import io.rivune.api.isKnownLocalNetworkServerUrl
import java.net.URI
import java.nio.ByteBuffer
import java.nio.charset.CodingErrorAction
import java.nio.charset.StandardCharsets
import java.util.ArrayDeque

internal const val RIVUNE_DNS_SD_SERVICE_TYPE = "_rivune._tcp."

internal data class DiscoveredRivuneServer(
    val serviceName: String,
    val name: String,
    val address: String,
    val version: String?,
) {
    val usesSecureTransport: Boolean get() = address.startsWith("https://")
}

internal fun discoveredRivuneServer(
    serviceName: String,
    attributes: Map<String, ByteArray>,
): DiscoveredRivuneServer? {
    if (attributes["protocol"]?.decodeStrictUTF8()?.trim() != RivuneProtocol.VERSION.toString()) return null
    val rawAddress = attributes["url"]?.decodeStrictUTF8()?.trim()?.takeIf { it.length <= 255 } ?: return null
    val parsed = runCatching { URI(rawAddress) }.getOrNull() ?: return null
    val scheme = parsed.scheme?.lowercase() ?: return null
    if (
        (scheme != "http" && scheme != "https") ||
        parsed.host.isNullOrBlank() ||
        parsed.rawUserInfo != null ||
        (parsed.rawPath.isNotEmpty() && parsed.rawPath != "/") ||
        parsed.rawQuery != null ||
        parsed.rawFragment != null ||
        parsed.port !in -1..65535 ||
        parsed.port == 0
    ) return null
    val address = runCatching {
        URI(scheme, null, parsed.host, parsed.port, null, null, null).toASCIIString()
    }.getOrNull() ?: return null
    if (scheme == "http" && !isKnownLocalNetworkServerUrl(address)) return null

    val normalizedServiceName = serviceName.trim().take(120).ifBlank { "Rivune" }
    val advertisedName = attributes["name"]?.decodeStrictUTF8()?.trim()?.take(120).orEmpty()
    val version = attributes["version"]?.decodeStrictUTF8()?.trim()?.take(64)?.ifBlank { null }
    return DiscoveredRivuneServer(
        serviceName = normalizedServiceName,
        name = advertisedName.ifBlank { normalizedServiceName },
        address = address,
        version = version,
    )
}

private fun ByteArray.decodeStrictUTF8(): String? = runCatching {
    StandardCharsets.UTF_8.newDecoder()
        .onMalformedInput(CodingErrorAction.REPORT)
        .onUnmappableCharacter(CodingErrorAction.REPORT)
        .decode(ByteBuffer.wrap(this))
        .toString()
}.getOrNull()

@Suppress("DEPRECATION")
internal class LanServerDiscovery(context: Context) : AutoCloseable {
    private val manager = context.applicationContext.getSystemService(NsdManager::class.java)
    private val handler = Handler(Looper.getMainLooper())
    private val serversByService = linkedMapOf<String, DiscoveredRivuneServer>()
    private val pending = ArrayDeque<NsdServiceInfo>()
    private val queuedNames = mutableSetOf<String>()
    private var resolving = false
    private var active = false
    private var callback: ((List<DiscoveredRivuneServer>) -> Unit)? = null
    private var listener: NsdManager.DiscoveryListener? = null

    fun start(onServersChanged: (List<DiscoveredRivuneServer>) -> Unit) {
        check(Looper.myLooper() == Looper.getMainLooper()) { "LAN discovery must start on the main thread" }
        stop()
        callback = onServersChanged
        active = true
        val discoveryListener = object : NsdManager.DiscoveryListener {
            override fun onDiscoveryStarted(serviceType: String) = Unit

            override fun onServiceFound(serviceInfo: NsdServiceInfo) {
                handler.post { enqueue(serviceInfo) }
            }

            override fun onServiceLost(serviceInfo: NsdServiceInfo) {
                handler.post {
                    if (!active) return@post
                    serversByService.remove(serviceInfo.serviceName)
                    pending.removeAll { it.serviceName == serviceInfo.serviceName }
                    queuedNames.remove(serviceInfo.serviceName)
                    publish()
                }
            }

            override fun onDiscoveryStopped(serviceType: String) = Unit

            override fun onStartDiscoveryFailed(serviceType: String, errorCode: Int) {
                handler.post { if (active) stop() }
            }

            override fun onStopDiscoveryFailed(serviceType: String, errorCode: Int) = Unit
        }
        listener = discoveryListener
        try {
            manager.discoverServices(RIVUNE_DNS_SD_SERVICE_TYPE, NsdManager.PROTOCOL_DNS_SD, discoveryListener)
        } catch (_: RuntimeException) {
            stop()
        }
    }

    override fun close() = stop()

    fun stop() {
        check(Looper.myLooper() == Looper.getMainLooper()) { "LAN discovery must stop on the main thread" }
        active = false
        listener?.let { current -> runCatching { manager.stopServiceDiscovery(current) } }
        listener = null
        callback = null
        serversByService.clear()
        pending.clear()
        queuedNames.clear()
        resolving = false
    }

    private fun enqueue(serviceInfo: NsdServiceInfo) {
        if (!active || serviceInfo.serviceType?.trimEnd('.') != RIVUNE_DNS_SD_SERVICE_TYPE.trimEnd('.')) return
        val serviceName = serviceInfo.serviceName
        if (serviceName.isBlank() || !queuedNames.add(serviceName)) return
        pending.addLast(serviceInfo)
        resolveNext()
    }

    private fun resolveNext() {
        if (!active || resolving || pending.isEmpty()) return
        resolving = true
        val service = pending.removeFirst()
        runCatching {
            manager.resolveService(service, object : NsdManager.ResolveListener {
                override fun onResolveFailed(serviceInfo: NsdServiceInfo, errorCode: Int) {
                    handler.post { completeResolution(service.serviceName, null) }
                }

                override fun onServiceResolved(serviceInfo: NsdServiceInfo) {
                    val discovered = discoveredRivuneServer(service.serviceName, serviceInfo.attributes)
                    handler.post { completeResolution(service.serviceName, discovered) }
                }
            })
        }.onFailure { completeResolution(service.serviceName, null) }
    }

    private fun completeResolution(serviceName: String, server: DiscoveredRivuneServer?) {
        if (!active) return
        queuedNames.remove(serviceName)
        resolving = false
        if (server != null) {
            serversByService[serviceName] = server
            publish()
        }
        resolveNext()
    }

    private fun publish() {
        callback?.invoke(
            serversByService.values
                .distinctBy { it.address }
                .sortedWith(compareBy<DiscoveredRivuneServer, String>(String.CASE_INSENSITIVE_ORDER) { it.name }.thenBy { it.address }),
        )
    }
}
