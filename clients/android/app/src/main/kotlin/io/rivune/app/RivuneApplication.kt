package io.rivune.app

import android.app.Application
import coil.ImageLoader
import coil.ImageLoaderFactory
import coil.decode.SvgDecoder
import coil.disk.DiskCache
import coil.memory.MemoryCache
import okhttp3.OkHttpClient
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob

private const val IMAGE_MEMORY_CACHE_PERCENT = 0.20
private const val IMAGE_DISK_CACHE_BYTES = 64L * 1024L * 1024L

class RivuneApplication : Application(), ImageLoaderFactory {
    internal val applicationScope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    internal val appPreferences: AppPreferencesStore by lazy { AppPreferencesStore(this) }
    internal val diagnostics = DiagnosticsBuffer()
    internal val appUpdates: AppUpdateCoordinator by lazy {
        AppUpdateCoordinator(
            context = this,
            enabled = BuildConfig.APP_UPDATES_ENABLED,
            manifestUrl = BuildConfig.UPDATE_MANIFEST_URL,
            diagnostics = diagnostics,
        )
    }

    override fun onCreate() {
        super.onCreate()
        diagnostics.record(DiagnosticEventCode.APP_STARTED)
    }

    override fun newImageLoader(): ImageLoader = ImageLoader.Builder(this)
        .memoryCache {
            MemoryCache.Builder(this)
                .maxSizePercent(IMAGE_MEMORY_CACHE_PERCENT)
                .build()
        }
        .diskCache {
            DiskCache.Builder()
                .directory(cacheDir.resolve("image_cache"))
                .maxSizeBytes(IMAGE_DISK_CACHE_BYTES)
                .build()
        }
        .components {
            add(SvgDecoder.Factory())
        }
        .okHttpClient {
            OkHttpClient.Builder()
                .followRedirects(false)
                .followSslRedirects(false)
                .build()
        }
        .build()
}
