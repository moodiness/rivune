package io.rivune.app

import android.app.Application
import coil.ImageLoader
import coil.ImageLoaderFactory
import coil.decode.SvgDecoder
import coil.disk.DiskCache
import coil.memory.MemoryCache
import io.rivune.app.ui.theme.RivuneMotion
import okhttp3.OkHttpClient

private const val IMAGE_MEMORY_CACHE_PERCENT = 0.20
private const val IMAGE_DISK_CACHE_BYTES = 64L * 1024L * 1024L

class RivuneApplication : Application(), ImageLoaderFactory {
    internal val appUpdates: AppUpdateCoordinator by lazy {
        AppUpdateCoordinator(
            context = this,
            enabled = BuildConfig.APP_UPDATES_ENABLED,
            manifestUrl = BuildConfig.UPDATE_MANIFEST_URL,
        )
    }

    override fun newImageLoader(): ImageLoader = ImageLoader.Builder(this)
        .crossfade(RivuneMotion.normal)
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
