package io.rivune.app

import android.content.Context
import android.media.AudioAttributes
import android.media.AudioFocusRequest
import android.media.AudioManager
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import android.view.SurfaceHolder
import android.view.SurfaceView
import dev.jdtech.mpv.MPVLib
import java.io.File
import java.nio.file.Files
import java.nio.file.StandardCopyOption
import java.security.KeyStore
import java.security.cert.X509Certificate
import java.util.Base64
import kotlin.math.roundToLong

internal enum class MpvPlaybackState { PREPARING, BUFFERING, READY }

internal data class MpvTrack(
    val nativeId: Int?,
    val identity: String,
    val type: String,
    val title: String?,
    val language: String?,
    val selected: Boolean,
)

internal data class MpvNativeTrack(
    val id: Int,
    val type: String,
    val title: String?,
    val language: String?,
    val selected: Boolean,
    val externalFilename: String? = null,
)

internal sealed interface MpvSubtitleSelectionAction {
    data object Disable : MpvSubtitleSelectionAction
    data class SelectNative(val id: Int) : MpvSubtitleSelectionAction
    data class AddExternal(val subtitle: PlayerSubtitlePresentation) : MpvSubtitleSelectionAction
    data object AwaitExternal : MpvSubtitleSelectionAction
}

internal fun mpvExternalSubtitleIdentity(subtitle: PlayerSubtitlePresentation): String =
    "server:${subtitle.id}"

internal fun projectMpvSubtitleTracks(
    advertised: List<PlayerSubtitlePresentation>,
    nativeTracks: List<MpvNativeTrack>,
    selectedIdentity: String?,
): List<MpvTrack> {
    val nativeByFilename = nativeTracks
        .filter { !it.externalFilename.isNullOrEmpty() }
        .groupBy { it.externalFilename }
    val matchedNativeIds = HashSet<Int>()
    val projected = ArrayList<MpvTrack>(advertised.size + nativeTracks.size)
    advertised.forEach { subtitle ->
        val native = nativeByFilename[subtitle.url]?.firstOrNull()
        if (native != null) matchedNativeIds += native.id
        val identity = mpvExternalSubtitleIdentity(subtitle)
        projected += MpvTrack(
            nativeId = native?.id,
            identity = identity,
            type = "sub",
            title = subtitle.label,
            language = subtitle.language,
            selected = identity == selectedIdentity,
        )
    }
    nativeTracks.forEach { native ->
        if (native.id in matchedNativeIds) return@forEach
        val identity = "native:sub:${native.id}"
        projected += MpvTrack(
            nativeId = native.id,
            identity = identity,
            type = native.type,
            title = native.title,
            language = native.language,
            selected = identity == selectedIdentity,
        )
    }
    return projected
}

internal fun isMpvNaturalEnd(observedEof: Boolean, currentEof: Boolean?): Boolean =
    observedEof || currentEof == true

internal fun initialMpvExternalSubtitles(
    advertised: List<PlayerSubtitlePresentation>,
): List<PlayerSubtitlePresentation> = listOfNotNull(advertised.firstOrNull(PlayerSubtitlePresentation::selected))

internal fun isMpvSubtitleRequestPending(deadlineMs: Long?, nowMs: Long): Boolean =
    deadlineMs != null && deadlineMs > nowMs

internal fun resolveMpvSubtitleSelection(
    identity: String?,
    tracks: List<MpvTrack>,
    advertised: List<PlayerSubtitlePresentation>,
    pendingExternalIdentities: Set<String>,
): MpvSubtitleSelectionAction {
    if (identity == null) return MpvSubtitleSelectionAction.Disable
    tracks.firstOrNull { it.identity == identity }?.nativeId?.let {
        return MpvSubtitleSelectionAction.SelectNative(it)
    }
    val external = advertised.firstOrNull { mpvExternalSubtitleIdentity(it) == identity }
        ?: return MpvSubtitleSelectionAction.AwaitExternal
    return if (identity in pendingExternalIdentities) {
        MpvSubtitleSelectionAction.AwaitExternal
    } else {
        MpvSubtitleSelectionAction.AddExternal(external)
    }
}

internal interface MpvPlaybackListener {
    fun onStateChanged(state: MpvPlaybackState)
    fun onPlayingChanged(isPlaying: Boolean)
    fun onPositionChanged(positionMs: Long, durationMs: Long)
    fun onTracksChanged(audio: List<MpvTrack>, subtitles: List<MpvTrack>)
    fun onPlaybackEnded()
    fun onPlaybackFailed(positionMs: Long)
}

/** Owns one native libmpv instance at a time and confines public operations to the main thread. */
internal class MpvPlaybackController(
    context: Context,
    private val presentation: PlayerPresentation,
    private val listener: MpvPlaybackListener,
) {
    private val appContext = context.applicationContext
    private val mainHandler = Handler(Looper.getMainLooper())
    private val audioManager = appContext.getSystemService(AudioManager::class.java)
    private val audioFocusRequest = AudioFocusRequest.Builder(AudioManager.AUDIOFOCUS_GAIN)
        .setAudioAttributes(
            AudioAttributes.Builder()
                .setUsage(AudioAttributes.USAGE_MEDIA)
                .setContentType(AudioAttributes.CONTENT_TYPE_MOVIE)
                .build(),
        )
        .setOnAudioFocusChangeListener(
            { change ->
                when (change) {
                    AudioManager.AUDIOFOCUS_GAIN -> if (!pauseRequested) {
                        hasAudioFocus = true
                        mpv?.setPropertyBoolean("pause", false)
                    }
                    AudioManager.AUDIOFOCUS_LOSS_TRANSIENT -> {
                        hasAudioFocus = false
                        mpv?.setPropertyBoolean("pause", true)
                        updatePlaying()
                    }
                    AudioManager.AUDIOFOCUS_LOSS -> pause()
                }
            },
            mainHandler,
        )
        .build()
    private var mpv: MPVLib? = null
    private var observer: MPVLib.EventObserver? = null
    private var surfaceView: SurfaceView? = null
    private var generation = 0L
    private var closed = false
    private var tearingDown = false
    private var fileLoaded = false
    private var terminalEventDelivered = false
    private var pausedForCache = false
    private var pauseRequested = false
    private var nativePaused = true
    private var hasAudioFocus = false
    private var eofReached = false
    private var requestedPositionMs = presentation.startPositionMs.coerceAtLeast(0L)
    private var requestedAspect = VideoAspectPreference.FIT
    private var requestedSpeed = 1f
    private var requestedAudioId: Int? = null
    private var selectedSubtitleIdentity = initialMpvExternalSubtitles(presentation.subtitles)
        .firstOrNull()
        ?.let(::mpvExternalSubtitleIdentity)
    private var subtitleTracks = emptyList<MpvTrack>()
    private var pendingExternalSubtitleIdentity: String? = null
    private var pendingExternalSubtitleDeadlineMs: Long? = null
    private val externalSubtitleTimeout = Runnable { expireExternalSubtitleRequest() }

    var positionMs: Long = requestedPositionMs
        private set
    var durationMs: Long = presentation.durationSeconds.coerceAtLeast(0).toLong() * 1_000L
        private set
    var isPlaying: Boolean = false
        private set

    fun createSurfaceView(context: Context): SurfaceView = SurfaceView(context).also { view ->
        check(surfaceView == null || surfaceView === view) { "MPV controller is already attached to a view" }
        surfaceView = view
        view.holder.addCallback(surfaceCallback)
    }

    fun releaseSurfaceView(view: SurfaceView) {
        if (surfaceView !== view) return
        view.holder.removeCallback(surfaceCallback)
        if (view.holder.surface.isValid) destroyNativeForSurfaceLoss()
        surfaceView = null
    }

    fun play() {
        val focusGranted = audioManager.requestAudioFocus(audioFocusRequest) == AudioManager.AUDIOFOCUS_REQUEST_GRANTED
        hasAudioFocus = focusGranted
        if (!focusGranted) {
            mpv?.setPropertyBoolean("pause", true)
            return
        }
        pauseRequested = false
        val currentSurface = surfaceView?.holder
        if (mpv == null && currentSurface?.surface?.isValid == true) surfaceCallback.surfaceCreated(currentSurface)
        mpv?.setPropertyBoolean("pause", false)
    }

    fun pause() {
        pauseRequested = true
        hasAudioFocus = false
        mpv?.setPropertyBoolean("pause", true)
        audioManager.abandonAudioFocusRequest(audioFocusRequest)
    }

    fun seekTo(absolutePositionMs: Long) {
        val target = absolutePositionMs.coerceAtLeast(0L)
        requestedPositionMs = target
        positionMs = target
        terminalEventDelivered = false
        eofReached = false
        mpv?.setPropertyDouble(
            "time-pos",
            mediaPlaybackPositionMs(target, presentation.timelineStartPositionMs, presentation.mediaTimeline) / 1_000.0,
        )
        listener.onPositionChanged(positionMs, durationMs)
    }

    fun setSpeed(speed: Float) {
        requestedSpeed = speed
        mpv?.setPropertyDouble("speed", speed.toDouble())
    }

    fun setAspect(aspect: VideoAspectPreference) {
        requestedAspect = aspect
        val instance = mpv ?: return
        when (aspect) {
            VideoAspectPreference.FIT -> {
                instance.setPropertyString("video-aspect-override", "no")
                instance.setPropertyDouble("panscan", 0.0)
            }
            VideoAspectPreference.FILL -> {
                instance.setPropertyDouble("panscan", 0.0)
                instance.setPropertyString("video-aspect-override", "-1")
            }
            VideoAspectPreference.ZOOM -> {
                instance.setPropertyString("video-aspect-override", "no")
                instance.setPropertyDouble("panscan", 1.0)
            }
        }
    }

    fun selectAudio(id: Int) {
        requestedAudioId = id
        mpv?.setPropertyInt("aid", id)
        refreshTracks()
    }

    fun selectSubtitle(identity: String?) {
        selectedSubtitleIdentity = identity
        mpv?.let { requestSubtitleSelection(it, identity) }
        refreshTracks()
    }

    private fun requestSubtitleSelection(instance: MPVLib, identity: String?) {
        when (val action = resolveMpvSubtitleSelection(
            identity = identity,
            tracks = subtitleTracks,
            advertised = presentation.subtitles,
            pendingExternalIdentities = setOfNotNull(pendingExternalSubtitleIdentity),
        )) {
            MpvSubtitleSelectionAction.Disable -> instance.setPropertyString("sid", "no")
            is MpvSubtitleSelectionAction.SelectNative -> instance.setPropertyInt("sid", action.id)
            is MpvSubtitleSelectionAction.AddExternal -> {
                if (pendingExternalSubtitleIdentity == null) addExternalSubtitle(instance, action.subtitle)
            }
            MpvSubtitleSelectionAction.AwaitExternal -> Unit
        }
    }

    private fun addExternalSubtitle(instance: MPVLib, subtitle: PlayerSubtitlePresentation) {
        val identity = mpvExternalSubtitleIdentity(subtitle)
        val nowMs = SystemClock.uptimeMillis()
        if (identity == pendingExternalSubtitleIdentity &&
            isMpvSubtitleRequestPending(pendingExternalSubtitleDeadlineMs, nowMs)
        ) return
        pendingExternalSubtitleIdentity = identity
        pendingExternalSubtitleDeadlineMs = nowMs + EXTERNAL_SUBTITLE_LOAD_TIMEOUT_MS
        scheduleExternalSubtitleTimeout(nowMs)
        try {
            instance.command(arrayOf("sub-add", subtitle.url, "select", subtitle.label, subtitle.language.orEmpty()))
        } catch (_: RuntimeException) {
            clearPendingExternalSubtitleRequest()
        }
    }

    private fun expireExternalSubtitleRequest() {
        val nowMs = SystemClock.uptimeMillis()
        if (isMpvSubtitleRequestPending(pendingExternalSubtitleDeadlineMs, nowMs)) {
            scheduleExternalSubtitleTimeout(nowMs)
            return
        }
        val expiredIdentity = pendingExternalSubtitleIdentity
        clearPendingExternalSubtitleRequest()
        if (selectedSubtitleIdentity != expiredIdentity) {
            mpv?.let { requestSubtitleSelection(it, selectedSubtitleIdentity) }
        }
    }

    private fun clearPendingExternalSubtitleRequest() {
        pendingExternalSubtitleIdentity = null
        pendingExternalSubtitleDeadlineMs = null
        mainHandler.removeCallbacks(externalSubtitleTimeout)
    }

    private fun scheduleExternalSubtitleTimeout(nowMs: Long) {
        mainHandler.removeCallbacks(externalSubtitleTimeout)
        val deadlineMs = pendingExternalSubtitleDeadlineMs ?: return
        mainHandler.postDelayed(externalSubtitleTimeout, (deadlineMs - nowMs).coerceAtLeast(1L))
    }
    fun release() {
        if (closed) return
        closed = true
        surfaceView?.holder?.removeCallback(surfaceCallback)
        surfaceView = null
        destroyNative()
        audioManager.abandonAudioFocusRequest(audioFocusRequest)
        mainHandler.removeCallbacksAndMessages(null)
    }

    private val surfaceCallback = object : SurfaceHolder.Callback {
        override fun surfaceCreated(holder: SurfaceHolder) {
            if (closed || terminalEventDelivered || !holder.surface.isValid) return
            val instance = ensureNative() ?: return
            instance.attachSurface(holder.surface)
            instance.setOptionString("force-window", "yes")
            instance.setPropertyString("android-surface-size", "${holder.surfaceFrame.width()}x${holder.surfaceFrame.height()}")
            instance.setPropertyBoolean("pause", true)
            instance.command(arrayOf("loadfile", presentation.mediaUrl, "replace"))
        }

        override fun surfaceChanged(holder: SurfaceHolder, format: Int, width: Int, height: Int) {
            if (!closed && holder.surface.isValid) mpv?.setPropertyString("android-surface-size", "${width}x$height")
        }

        override fun surfaceDestroyed(holder: SurfaceHolder) {
            // Native destroy is synchronous: VO and its owned Surface reference are gone before
            // Android may destroy the Surface. A later surfaceCreated recreates and resumes libmpv.
            destroyNativeForSurfaceLoss()
        }
    }

    private fun ensureNative(): MPVLib? {
        mpv?.let { return it }
        if (closed) return null
        val instance = MPVLib.create(appContext)
        if (instance == null) {
            deliverFailure()
            return null
        }
        generation += 1L
        val instanceGeneration = generation
        val eventObserver = createObserver(instanceGeneration)
        observer = eventObserver
        mpv = instance
        tearingDown = false
        fileLoaded = false
        eofReached = false
        try {
            configure(instance)
            instance.addObserver(eventObserver)
            instance.init()
            observe(instance)
            listener.onStateChanged(MpvPlaybackState.PREPARING)
        } catch (failure: Throwable) {
            destroyNative()
            deliverFailure()
            return null
        }
        return instance
    }

    private fun configure(instance: MPVLib) {
        instance.setOptionString("config", "no")
        instance.setOptionString("profile", "fast")
        instance.setOptionString("vo", "gpu")
        instance.setOptionString("gpu-context", "android")
        instance.setOptionString("opengl-es", "yes")
        instance.setOptionString("hwdec", "mediacodec-copy")
        instance.setOptionString("hwdec-codecs", "all")
        instance.setOptionString("ao", "audiotrack,opensles")
        instance.setOptionString("audio-set-media-role", "yes")
        instance.setOptionString("cache", "yes")
        instance.setOptionString("demuxer-max-bytes", (64 * 1024 * 1024).toString())
        instance.setOptionString("demuxer-max-back-bytes", (16 * 1024 * 1024).toString())
        instance.setOptionString("tls-ca-file", androidCaBundle(appContext).absolutePath)
        instance.setOptionString("tls-verify", "yes")
        instance.setOptionString("network-timeout", "30")
        instance.setOptionString("input-default-bindings", "no")
        instance.setOptionString("input-vo-keyboard", "no")
        instance.setOptionString("osc", "no")
        instance.setOptionString("terminal", "no")
        instance.setOptionString("idle", "yes")
        instance.setOptionString("keep-open", "yes")
        instance.setOptionString("force-window", "no")
        instance.setOptionString("save-position-on-quit", "no")
    }

    private fun observe(instance: MPVLib) {
        instance.observeProperty("time-pos/full", MPVLib.MpvFormat.MPV_FORMAT_DOUBLE)
        instance.observeProperty("duration/full", MPVLib.MpvFormat.MPV_FORMAT_DOUBLE)
        instance.observeProperty("pause", MPVLib.MpvFormat.MPV_FORMAT_FLAG)
        instance.observeProperty("paused-for-cache", MPVLib.MpvFormat.MPV_FORMAT_FLAG)
        instance.observeProperty("eof-reached", MPVLib.MpvFormat.MPV_FORMAT_FLAG)
        instance.observeProperty("track-list", MPVLib.MpvFormat.MPV_FORMAT_NONE)
    }

    private fun createObserver(instanceGeneration: Long) = object : MPVLib.EventObserver {
        override fun eventProperty(property: String) {
            post(instanceGeneration) { if (property == "track-list") refreshTracks() }
        }
        override fun eventProperty(property: String, value: Long) {
            post(instanceGeneration) { handleNumericProperty(property, value.toDouble()) }
        }
        override fun eventProperty(property: String, value: Double) {
            post(instanceGeneration) { handleNumericProperty(property, value) }
        }
        override fun eventProperty(property: String, value: Boolean) {
            post(instanceGeneration) {
                when (property) {
                    "pause" -> { nativePaused = value; updatePlaying() }
                    "paused-for-cache" -> {
                        pausedForCache = value
                        listener.onStateChanged(if (value) MpvPlaybackState.BUFFERING else MpvPlaybackState.READY)
                        updatePlaying()
                    }
                    "eof-reached" -> eofReached = value
                }
            }
        }
        override fun eventProperty(property: String, value: String) = Unit
        override fun event(eventId: Int) {
            post(instanceGeneration) {
                when (eventId) {
                    MPVLib.MpvEvent.MPV_EVENT_START_FILE -> listener.onStateChanged(MpvPlaybackState.PREPARING)
                    MPVLib.MpvEvent.MPV_EVENT_FILE_LOADED -> onFileLoaded()
                    MPVLib.MpvEvent.MPV_EVENT_PLAYBACK_RESTART -> listener.onStateChanged(MpvPlaybackState.READY)
                    MPVLib.MpvEvent.MPV_EVENT_END_FILE -> {
                        val endedAtEof = isMpvNaturalEnd(eofReached, mpv?.getPropertyBoolean("eof-reached"))
                        if (endedAtEof) deliverEnd() else deliverFailure()
                    }
                    MPVLib.MpvEvent.MPV_EVENT_SHUTDOWN -> if (!tearingDown) deliverFailure()
                }
            }
        }
    }

    private fun post(instanceGeneration: Long, action: () -> Unit) {
        mainHandler.post { if (!closed && !tearingDown && generation == instanceGeneration) action() }
    }

    private fun handleNumericProperty(property: String, seconds: Double) {
        when (property) {
            "time-pos/full" -> {
                val mediaPosition = (seconds * 1_000.0).roundToLong().coerceAtLeast(0L)
                positionMs = absolutePlaybackPositionMs(mediaPosition, presentation.timelineStartPositionMs, presentation.mediaTimeline)
                    .let { if (durationMs > 0L) it.coerceAtMost(durationMs) else it }
                requestedPositionMs = positionMs
            }
            "duration/full" -> {
                val mediaDuration = (seconds * 1_000.0).roundToLong().coerceAtLeast(0L)
                durationMs = resolvedPlaybackDurationMs(
                    presentation.durationSeconds.coerceAtLeast(0).toLong() * 1_000L,
                    mediaDuration,
                    presentation.timelineStartPositionMs,
                    presentation.mediaTimeline,
                )
            }
        }
        listener.onPositionChanged(positionMs, durationMs)
    }

    private fun onFileLoaded() {
        val instance = mpv ?: return
        fileLoaded = true
        selectedSubtitleIdentity
            ?.let { identity -> presentation.subtitles.firstOrNull { mpvExternalSubtitleIdentity(it) == identity } }
            ?.let { addExternalSubtitle(instance, it) }
        if (selectedSubtitleIdentity == null) instance.setPropertyString("sid", "no")
        val mediaPositionMs = mediaPlaybackPositionMs(requestedPositionMs, presentation.timelineStartPositionMs, presentation.mediaTimeline)
        if (mediaPositionMs > 0L) instance.setPropertyDouble("time-pos", mediaPositionMs / 1_000.0)
        setAspect(requestedAspect)
        setSpeed(requestedSpeed)
        if (pauseRequested) instance.setPropertyBoolean("pause", true) else play()
        refreshTracks()
        listener.onStateChanged(MpvPlaybackState.READY)
        updatePlaying()
    }

    private fun refreshTracks() {
        val instance = mpv ?: return
        val count = instance.getPropertyInt("track-list/count") ?: return
        val audio = ArrayList<MpvTrack>(count)
        val nativeSubtitles = ArrayList<MpvNativeTrack>(count)
        for (index in 0 until count) {
            val type = instance.getPropertyString("track-list/$index/type") ?: continue
            if (type != "audio" && type != "sub") continue
            val id = instance.getPropertyInt("track-list/$index/id") ?: continue
            val title = instance.getPropertyString("track-list/$index/title")
            val language = instance.getPropertyString("track-list/$index/lang")
            val selected = instance.getPropertyBoolean("track-list/$index/selected") == true
            if (type == "audio") {
                audio += MpvTrack(
                    nativeId = id,
                    identity = "native:audio:$id",
                    type = type,
                    title = title,
                    language = language,
                    selected = requestedAudioId?.let { it == id } ?: selected,
                )
            } else {
                nativeSubtitles += MpvNativeTrack(
                    id = id,
                    type = type,
                    title = title,
                    language = language,
                    selected = selected,
                    externalFilename = instance.getPropertyString("track-list/$index/external-filename"),
                )
            }
        }
        requestedAudioId?.let { requestedId ->
            if (audio.any { it.nativeId == requestedId } && audio.none { it.nativeId == requestedId && it.selected }) {
                instance.setPropertyInt("aid", requestedId)
            }
        }
        subtitleTracks = projectMpvSubtitleTracks(
            advertised = presentation.subtitles,
            nativeTracks = nativeSubtitles,
            selectedIdentity = selectedSubtitleIdentity,
        )
        val completedIdentity = pendingExternalSubtitleIdentity?.takeIf { identity ->
            subtitleTracks.any { it.identity == identity && it.nativeId != null }
        }
        if (completedIdentity != null) {
            clearPendingExternalSubtitleRequest()
            if (selectedSubtitleIdentity != completedIdentity) requestSubtitleSelection(instance, selectedSubtitleIdentity)
        }
        val selectedTrack = subtitleTracks.firstOrNull { it.identity == selectedSubtitleIdentity }
        when {
            selectedTrack?.nativeId != null && nativeSubtitles.none { it.id == selectedTrack.nativeId && it.selected } ->
                instance.setPropertyInt("sid", selectedTrack.nativeId)
            selectedTrack?.nativeId == null && nativeSubtitles.any(MpvNativeTrack::selected) ->
                instance.setPropertyString("sid", "no")
        }
        listener.onTracksChanged(audio, subtitleTracks)
    }

    private fun updatePlaying() {
        val updated = fileLoaded && hasAudioFocus && !nativePaused && !pausedForCache && !terminalEventDelivered
        if (updated != isPlaying) {
            isPlaying = updated
            surfaceView?.keepScreenOn = updated
            listener.onPlayingChanged(updated)
        }
    }

    private fun deliverEnd() {
        if (terminalEventDelivered || closed || tearingDown) return
        terminalEventDelivered = true
        isPlaying = false
        surfaceView?.keepScreenOn = false
        audioManager.abandonAudioFocusRequest(audioFocusRequest)
        listener.onPlayingChanged(false)
        listener.onPlaybackEnded()
    }

    private fun deliverFailure() {
        if (terminalEventDelivered || closed || tearingDown) return
        terminalEventDelivered = true
        isPlaying = false
        surfaceView?.keepScreenOn = false
        audioManager.abandonAudioFocusRequest(audioFocusRequest)
        listener.onPlayingChanged(false)
        listener.onPlaybackFailed(positionMs)
    }

    private fun destroyNativeForSurfaceLoss() {
        if (closed || mpv == null) return
        requestedPositionMs = positionMs
        destroyNative()
    }

    private fun destroyNative() {
        val instance = mpv ?: return
        tearingDown = true
        generation += 1L
        observer?.let(instance::removeObserver)
        observer = null
        try {
            instance.destroy()
        } finally {
            mpv = null
            fileLoaded = false
            subtitleTracks = emptyList()
            clearPendingExternalSubtitleRequest()
            pausedForCache = false
            nativePaused = true
            hasAudioFocus = false
            isPlaying = false
            surfaceView?.keepScreenOn = false
            audioManager.abandonAudioFocusRequest(audioFocusRequest)
            tearingDown = false
        }
    }

    private companion object {
        private const val CA_BUNDLE_FILE = "libmpv-android-system-ca-v1.pem"
        private const val EXTERNAL_SUBTITLE_LOAD_TIMEOUT_MS = 35_000L
        private var processCaBundle: File? = null

        @Synchronized
        fun androidCaBundle(context: Context): File {
            processCaBundle?.let { cached ->
                check(cached.isFile && cached.length() > 0L) { "Generated libmpv CA bundle is unavailable" }
                return cached
            }
            val target = File(context.cacheDir, CA_BUNDLE_FILE)
            val temporary = File(context.cacheDir, "$CA_BUNDLE_FILE.tmp")
            try {
                Files.deleteIfExists(temporary.toPath())
                val keyStore = KeyStore.getInstance("AndroidCAStore").apply { load(null) }
                val encoder = Base64.getMimeEncoder(64, byteArrayOf('\n'.code.toByte()))
                var certificateCount = 0
                temporary.outputStream().buffered().use { output ->
                    val aliases = keyStore.aliases()
                    while (aliases.hasMoreElements()) {
                        val alias = aliases.nextElement()
                        if (!alias.startsWith("system:")) continue
                        val certificate = keyStore.getCertificate(alias) as? X509Certificate ?: continue
                        output.write("-----BEGIN CERTIFICATE-----\n".toByteArray())
                        output.write(encoder.encode(certificate.encoded))
                        output.write("\n-----END CERTIFICATE-----\n".toByteArray())
                        certificateCount += 1
                    }
                }
                check(certificateCount > 0 && temporary.length() > 0L) {
                    "Android CA store did not contain any system X.509 certificates"
                }
                Files.move(
                    temporary.toPath(),
                    target.toPath(),
                    StandardCopyOption.ATOMIC_MOVE,
                    StandardCopyOption.REPLACE_EXISTING,
                )
                check(target.isFile && target.length() > 0L) { "Unable to create libmpv CA bundle" }
                return target.also { processCaBundle = it }
            } catch (failure: Throwable) {
                runCatching { Files.deleteIfExists(temporary.toPath()) }
                throw failure
            }
        }
    }
}
