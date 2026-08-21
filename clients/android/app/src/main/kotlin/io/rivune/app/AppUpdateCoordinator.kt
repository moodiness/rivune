package io.rivune.app

import android.app.Activity
import android.content.ActivityNotFoundException
import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.SharedPreferences
import android.content.pm.PackageInstaller
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.provider.Settings
import java.io.File
import java.io.FileOutputStream
import java.security.MessageDigest
import java.lang.ref.WeakReference
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import okhttp3.OkHttpClient
import okhttp3.Request

internal sealed interface AppUpdateState {
    data object Idle : AppUpdateState
    data object Unavailable : AppUpdateState
    data class Checking(val manual: Boolean) : AppUpdateState
    data class UpToDate(val currentVersion: String) : AppUpdateState
    data class Available(val manifest: AppUpdateManifest, val packageInfo: AndroidUpdatePackage) : AppUpdateState
    data class Downloading(val manifest: AppUpdateManifest, val packageInfo: AndroidUpdatePackage) : AppUpdateState
    data class ReadyToInstall(val manifest: AppUpdateManifest, val packageInfo: AndroidUpdatePackage, val file: File) : AppUpdateState
    data class NeedsPermission(val manifest: AppUpdateManifest, val packageInfo: AndroidUpdatePackage, val file: File) : AppUpdateState
    data object Installing : AppUpdateState
    data class Error(val message: String) : AppUpdateState
}

private class SharedPreferencesUpdateCache(private val preferences: SharedPreferences) : UpdateCheckCache {
    override var etag: String?
        get() = preferences.getString("etag", null)
        set(value) { preferences.edit().putString("etag", value).apply() }
    override var manifest: String?
        get() = preferences.getString("manifest", null)
        set(value) { preferences.edit().putString("manifest", value).apply() }
    override var lastSuccessfulCheckAt: Long
        get() = preferences.getLong("last_successful_check_at", 0L)
        set(value) { preferences.edit().putLong("last_successful_check_at", value).apply() }
}

private const val NO_INSTALL_SESSION = -1

private class UpdateInstallState(context: Context) {
    private val preferences = context.getSharedPreferences("app_update_install", Context.MODE_PRIVATE)

    val activeSessionId: Int
        get() = preferences.getInt("active_session_id", NO_INSTALL_SESSION)

    val awaitingConfirmation: Boolean
        get() = preferences.getBoolean("awaiting_confirmation", false)

    fun begin(sessionId: Int): Boolean = preferences.edit()
        .putInt("active_session_id", sessionId)
        .putBoolean("awaiting_confirmation", false)
        .commit()

    fun markAwaitingConfirmation(sessionId: Int): Boolean {
        if (activeSessionId != sessionId) return false
        return preferences.edit().putBoolean("awaiting_confirmation", true).commit()
    }

    fun confirmationStarted(sessionId: Int) {
        if (activeSessionId == sessionId) preferences.edit().putBoolean("awaiting_confirmation", false).commit()
    }

    fun clear(sessionId: Int): Boolean {
        if (activeSessionId != sessionId) return false
        preferences.edit().clear().commit()
        return true
    }
}

internal interface UpdateApkInspector {
    fun inspect(file: File, expected: AndroidUpdatePackage, expectedVersionName: String)
}

internal class AndroidUpdateApkInspector(private val context: Context) : UpdateApkInspector {
    @Suppress("DEPRECATION")
    override fun inspect(file: File, expected: AndroidUpdatePackage, expectedVersionName: String) {
        val flags = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
            PackageManager.GET_SIGNING_CERTIFICATES
        } else {
            PackageManager.GET_SIGNATURES
        }
        val archive = context.packageManager.getPackageArchiveInfo(file.absolutePath, flags)
            ?: throw InvalidUpdateManifest("The downloaded file is not an Android package")
        if (archive.packageName != expected.applicationId || archive.packageName != context.packageName) {
            throw InvalidUpdateManifest("The Android package identifier does not match")
        }
        val archiveVersion = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) archive.longVersionCode else archive.versionCode.toLong()
        val installed = context.packageManager.getPackageInfo(context.packageName, flags)
        val installedVersion = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) installed.longVersionCode else installed.versionCode.toLong()
        if (archiveVersion != expected.buildVersion || archiveVersion <= installedVersion ||
            archive.versionName != expectedVersionName
        ) {
            throw InvalidUpdateManifest("The Android package version does not match")
        }
        val archiveCertificates = packageCertificates(archive).map(::certificateSha256).toSet()
        val installedCertificates = packageCertificates(installed).map(::certificateSha256).toSet()
        val expectedCertificate = expected.signingCertificateSha256.lowercase()
        if (archiveCertificates.size != 1 || installedCertificates.size != 1 ||
            archiveCertificates.single() != installedCertificates.single() ||
            archiveCertificates.single() != expectedCertificate
        ) {
            throw InvalidUpdateManifest("The Android signing certificate does not match")
        }
    }

    @Suppress("DEPRECATION")
    private fun packageCertificates(info: android.content.pm.PackageInfo): Array<out android.content.pm.Signature> =
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) info.signingInfo?.apkContentsSigners.orEmpty()
        else info.signatures.orEmpty()

    private fun certificateSha256(signature: android.content.pm.Signature): String =
        MessageDigest.getInstance("SHA-256").digest(signature.toByteArray()).toHex()
}

internal class AppUpdateCoordinator(
    private val context: Context,
    private val enabled: Boolean,
    manifestUrl: String,
    cache: UpdateCheckCache = SharedPreferencesUpdateCache(
        context.getSharedPreferences("app_update_cache", Context.MODE_PRIVATE),
    ),
    private val httpClient: OkHttpClient = OkHttpClient.Builder()
        .connectTimeout(15, TimeUnit.SECONDS)
        .readTimeout(30, TimeUnit.SECONDS)
        .callTimeout(45, TimeUnit.SECONDS)
        .followRedirects(true)
        .followSslRedirects(true)
        .build(),
    private val inspector: UpdateApkInspector = AndroidUpdateApkInspector(context),
    private val scope: CoroutineScope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate),
    private val diagnostics: DiagnosticsBuffer = DiagnosticsBuffer(),
) {
    private val manifestClient = AppUpdateManifestClient(manifestUrl, cache, httpClient)
    private val downloadHttpClient = httpClient.newBuilder()
        .callTimeout(0, TimeUnit.MILLISECONDS)
        .build()
    private val installState = UpdateInstallState(context)
    private var resumedActivity = WeakReference<Activity>(null)
    private val operation = Mutex()
    private val _state = MutableStateFlow<AppUpdateState>(restingUpdateState(enabled))
    val state: StateFlow<AppUpdateState> = _state.asStateFlow()

    init {
        val sessionId = installState.activeSessionId
        if (sessionId != NO_INSTALL_SESSION) {
            val session = context.packageManager.packageInstaller.getSessionInfo(sessionId)
            if (session != null && session.isSealed && !installState.awaitingConfirmation) {
                _state.value = AppUpdateState.Installing
            } else {
                if (session != null) runCatching { context.packageManager.packageInstaller.abandonSession(sessionId) }
                installState.clear(sessionId)
                _state.value = AppUpdateState.Error("The previous Android installation session did not complete")
            }
        }
    }

    fun activityResumed(activity: Activity) {
        resumedActivity = WeakReference(activity)
        val sessionId = installState.activeSessionId
        if (sessionId != NO_INSTALL_SESSION &&
            context.packageManager.packageInstaller.getSessionInfo(sessionId) == null
        ) {
            installState.clear(sessionId)
            _state.value = restingUpdateState(enabled)
        }
    }

    fun activityPaused(activity: Activity) {
        if (resumedActivity.get() === activity) resumedActivity.clear()
    }

    fun checkAutomatically() {
        if (!enabled || _state.value !is AppUpdateState.Idle) return
        scope.launch { check(manual = false) }
    }

    fun checkManually() {
        if (_state.value is AppUpdateState.Checking || _state.value is AppUpdateState.Downloading ||
            _state.value is AppUpdateState.Installing
        ) return
        if (!enabled) {
            _state.value = AppUpdateState.Unavailable
            return
        }
        scope.launch { check(manual = true) }
    }

    private suspend fun check(manual: Boolean) = operation.withLock {
        _state.value = AppUpdateState.Checking(manual)
        diagnostics.record(DiagnosticEventCode.UPDATE_CHECK_STARTED)
        try {
            when (val result = manifestClient.fetch(manual)) {
                ManifestFetchResult.Throttled -> {
                    _state.value = restingUpdateState(enabled)
                    diagnostics.record(DiagnosticEventCode.UPDATE_UP_TO_DATE)
                }
                is ManifestFetchResult.Manifest -> {
                    val resolved = resolveUpdateManifest(
                        manifest = result.value,
                        applicationId = BuildConfig.APPLICATION_ID,
                        currentVersionCode = BuildConfig.VERSION_CODE.toLong(),
                        currentVersionName = BuildConfig.VERSION_NAME,
                        manual = manual,
                    )
                    _state.value = resolved
                    diagnostics.record(
                        if (resolved is AppUpdateState.Available) {
                            DiagnosticEventCode.UPDATE_AVAILABLE
                        } else {
                            DiagnosticEventCode.UPDATE_UP_TO_DATE
                        },
                    )
                }
            }
        } catch (error: Exception) {
            _state.value = if (manual) AppUpdateState.Error(error.safeUpdateMessage()) else restingUpdateState(enabled)
            diagnostics.record(DiagnosticEventCode.UPDATE_CHECK_FAILED)
        }
    }

    /** Called only after the user accepts the download in the update dialog. */
    fun download() {
        val available = _state.value as? AppUpdateState.Available ?: return
        _state.value = AppUpdateState.Downloading(available.manifest, available.packageInfo)
        scope.launch {
            operation.withLock {
                val partial = updateDirectory().resolve("${available.packageInfo.fileName}.part")
                val complete = updateDirectory().resolve(available.packageInfo.fileName)
                try {
                    partial.delete()
                    complete.delete()
                    downloadVerified(available.packageInfo, partial)
                    if (!partial.renameTo(complete)) throw InvalidUpdateManifest("The downloaded update could not be saved")
                    inspector.inspect(complete, available.packageInfo, available.manifest.version)
                    _state.value = AppUpdateState.ReadyToInstall(available.manifest, available.packageInfo, complete)
                } catch (error: Exception) {
                    partial.delete()
                    complete.delete()
                    _state.value = AppUpdateState.Error(error.safeUpdateMessage())
                }
            }
        }
    }

    /** Starts PackageInstaller only after a second explicit user action. */
    fun install(activity: Activity) {
        val ready = when (val current = _state.value) {
            is AppUpdateState.ReadyToInstall -> current
            is AppUpdateState.NeedsPermission -> AppUpdateState.ReadyToInstall(current.manifest, current.packageInfo, current.file)
            else -> return
        }
        if (!context.packageManager.canRequestPackageInstalls()) {
            _state.value = AppUpdateState.NeedsPermission(ready.manifest, ready.packageInfo, ready.file)
            val permissionIntent = Intent(
                Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES,
                Uri.parse("package:${context.packageName}"),
            )
            try {
                if (permissionIntent.resolveActivity(context.packageManager) == null) {
                    throw ActivityNotFoundException("Android does not expose per-app install settings")
                }
                activity.startActivity(permissionIntent)
            } catch (error: ActivityNotFoundException) {
                ready.file.delete()
                _state.value = AppUpdateState.Error(error.safeUpdateMessage())
            } catch (error: SecurityException) {
                ready.file.delete()
                _state.value = AppUpdateState.Error(error.safeUpdateMessage())
            }
            return
        }
        _state.value = AppUpdateState.Installing
        scope.launch {
            operation.withLock {
                try {
                    commitPackage(ready.file)
                } catch (error: Exception) {
                    ready.file.delete()
                    _state.value = AppUpdateState.Error(error.safeUpdateMessage())
                }
            }
        }
    }

    fun resumeAfterPermission(activity: Activity) {
        if (_state.value is AppUpdateState.NeedsPermission && context.packageManager.canRequestPackageInstalls()) install(activity)
    }

    fun dismiss() {
        when (val current = _state.value) {
            is AppUpdateState.Downloading, AppUpdateState.Installing -> Unit
            is AppUpdateState.ReadyToInstall -> { current.file.delete(); _state.value = restingUpdateState(enabled) }
            is AppUpdateState.NeedsPermission -> { current.file.delete(); _state.value = restingUpdateState(enabled) }
            else -> _state.value = restingUpdateState(enabled)
        }
    }

    internal fun installationResult(sessionId: Int, status: Int, message: String?) {
        if (!installState.clear(sessionId)) return
        _state.value = when (status) {
            PackageInstaller.STATUS_SUCCESS -> restingUpdateState(enabled)
            else -> AppUpdateState.Error(message?.takeIf { it.isNotBlank() } ?: "Android could not install the update")
        }
    }

    internal fun requestInstallationConfirmation(sessionId: Int, confirmation: Intent?, launchContext: Context = context) {
        val sessionExists = runCatching {
            context.packageManager.packageInstaller.getSessionInfo(sessionId)
        }.getOrNull() != null
        if (!canLaunchInstallationConfirmation(
                activeSessionId = installState.activeSessionId,
                callbackSessionId = sessionId,
                sessionExists = sessionExists,
                confirmationPresent = confirmation != null,
            ) || !installState.markAwaitingConfirmation(sessionId)
        ) {
            installationResult(sessionId, PackageInstaller.STATUS_FAILURE, "Android did not provide a valid installation confirmation")
            return
        }
        val resumed = resumedActivity.get()
        val targetContext = resumed ?: launchContext
        if (resumed == null) confirmation!!.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        try {
            targetContext.startActivity(confirmation!!)
            installState.confirmationStarted(sessionId)
        } catch (error: Exception) {
            installationResult(sessionId, PackageInstaller.STATUS_FAILURE, error.message)
        }
    }

    private suspend fun downloadVerified(expected: AndroidUpdatePackage, destination: File) = withContext(Dispatchers.IO) {
        requireGithubReleaseAssetUrl(expected.url)
        val request = Request.Builder().url(expected.url).header("Accept", "application/vnd.android.package-archive").build()
        downloadHttpClient.newCall(request).execute().use { response ->
            if (!response.isSuccessful) throw InvalidUpdateManifest("Update download returned HTTP ${response.code}")
            requireAllowedFinalDownloadUrl(response.request.url)
            val body = response.body
            if (body.contentLength() > MAX_UPDATE_APK_BYTES ||
                (body.contentLength() >= 0L && body.contentLength() != expected.size)
            ) throw InvalidUpdateManifest("The update download size does not match")
            body.byteStream().use { input ->
                FileOutputStream(destination).buffered().use { output ->
                    copyAndVerifyUpdate(input, output, expected.size, expected.sha256)
                }
            }
        }
    }

    private fun requireAllowedFinalDownloadUrl(url: okhttp3.HttpUrl) {
        val allowed = url.host == "github.com" || url.host == "release-assets.githubusercontent.com" ||
            url.host.endsWith(".githubusercontent.com")
        if (!url.isHttps || !allowed) throw InvalidUpdateManifest("The update download redirected outside GitHub")
    }

    private suspend fun commitPackage(file: File) = withContext(Dispatchers.IO) {
        val installer = context.packageManager.packageInstaller
        val params = PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL).apply {
            setAppPackageName(context.packageName)
            setSize(file.length())
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) setRequireUserAction(PackageInstaller.SessionParams.USER_ACTION_REQUIRED)
        }
        val sessionId = installer.createSession(params)
        if (!installState.begin(sessionId)) {
            installer.abandonSession(sessionId)
            throw InvalidUpdateManifest("The Android installation session could not be recorded")
        }
        try {
            installer.openSession(sessionId).use { session ->
                file.inputStream().use { input ->
                    session.openWrite("rivune-update.apk", 0L, file.length()).use { output ->
                        input.copyTo(output)
                        session.fsync(output)
                    }
                }
                val callback = Intent(context, AppUpdateInstallReceiver::class.java).apply {
                    action = AppUpdateInstallReceiver.ACTION_INSTALL_RESULT
                    putExtra(PackageInstaller.EXTRA_SESSION_ID, sessionId)
                }
                val pending = PendingIntent.getBroadcast(
                    context,
                    sessionId,
                    callback,
                    PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_MUTABLE,
                )
                session.commit(pending.intentSender)
            }
        } catch (error: Exception) {
            installState.clear(sessionId)
            installer.abandonSession(sessionId)
            throw error
        }
        file.delete()
    }

    private fun updateDirectory(): File = context.cacheDir.resolve("app_updates").also(File::mkdirs)
}

internal fun restingUpdateState(enabled: Boolean): AppUpdateState =
    if (enabled) AppUpdateState.Idle else AppUpdateState.Unavailable

internal fun canLaunchInstallationConfirmation(
    activeSessionId: Int,
    callbackSessionId: Int,
    sessionExists: Boolean,
    confirmationPresent: Boolean,
): Boolean = activeSessionId != NO_INSTALL_SESSION &&
    callbackSessionId == activeSessionId &&
    sessionExists &&
    confirmationPresent

internal fun resolveUpdateManifest(
    manifest: AppUpdateManifest,
    applicationId: String,
    currentVersionCode: Long,
    currentVersionName: String,
    manual: Boolean,
): AppUpdateState {
    val updatePackage = manifest.androidPackage
    val available = updatePackage.applicationId == applicationId && updatePackage.buildVersion > currentVersionCode &&
        compareSemanticVersions(manifest.version, currentVersionName) > 0
    return if (available) AppUpdateState.Available(manifest, updatePackage)
    else if (manual) AppUpdateState.UpToDate(currentVersionName) else AppUpdateState.Idle
}

class AppUpdateInstallReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action != ACTION_INSTALL_RESULT) return
        val sessionId = intent.getIntExtra(PackageInstaller.EXTRA_SESSION_ID, NO_INSTALL_SESSION)
        if (sessionId == NO_INSTALL_SESSION) return
        val updates = (context.applicationContext as? RivuneApplication)?.appUpdates ?: return
        val status = intent.getIntExtra(PackageInstaller.EXTRA_STATUS, PackageInstaller.STATUS_FAILURE)
        if (status == PackageInstaller.STATUS_PENDING_USER_ACTION) {
            @Suppress("DEPRECATION")
            val confirmation = intent.getParcelableExtra<Intent>(Intent.EXTRA_INTENT)
            updates.requestInstallationConfirmation(sessionId, confirmation, context)
            return
        }
        updates.installationResult(
            sessionId,
            status,
            intent.getStringExtra(PackageInstaller.EXTRA_STATUS_MESSAGE),
        )
    }

    internal companion object {
        const val ACTION_INSTALL_RESULT = "io.rivune.app.APP_UPDATE_INSTALL_RESULT"
    }
}

private fun ByteArray.toHex(): String = joinToString("") { "%02x".format(it) }

internal fun copyAndVerifyUpdate(
    input: java.io.InputStream,
    output: java.io.OutputStream,
    expectedSize: Long,
    expectedSha256: String,
) {
    if (expectedSize <= 0L || expectedSize > MAX_UPDATE_APK_BYTES) throw InvalidUpdateManifest("Invalid update size")
    val digest = MessageDigest.getInstance("SHA-256")
    var total = 0L
    val buffer = ByteArray(DEFAULT_BUFFER_SIZE)
    while (true) {
        val count = input.read(buffer)
        if (count < 0) break
        total += count
        if (total > expectedSize || total > MAX_UPDATE_APK_BYTES) throw InvalidUpdateManifest("The update download is too large")
        digest.update(buffer, 0, count)
        output.write(buffer, 0, count)
    }
    if (total != expectedSize) throw InvalidUpdateManifest("The update download size does not match")
    if (digest.digest().toHex() != expectedSha256) throw InvalidUpdateManifest("The update download checksum does not match")
}
private fun Throwable.safeUpdateMessage(): String = message?.takeIf { it.isNotBlank() } ?: "The update operation failed"
