package io.rivune.app

import android.content.Context
import android.content.pm.PackageManager
import android.os.Build
import io.rivune.api.isKnownLocalNetworkServerUrl

internal const val ACCESS_LOCAL_NETWORK_PERMISSION = "android.permission.ACCESS_LOCAL_NETWORK"
internal const val LOCAL_NETWORK_PERMISSION_API = 37

internal fun requiresLocalNetworkPermission(
    normalizedServerUrl: String,
    sdkInt: Int,
    targetSdk: Int,
    permissionGranted: Boolean,
): Boolean = sdkInt >= LOCAL_NETWORK_PERMISSION_API &&
    targetSdk >= LOCAL_NETWORK_PERMISSION_API &&
    !permissionGranted &&
    isKnownLocalNetworkServerUrl(normalizedServerUrl)

internal fun requiresLocalNetworkPermission(context: Context, normalizedServerUrl: String): Boolean =
    requiresLocalNetworkPermission(
        normalizedServerUrl = normalizedServerUrl,
        permissionGranted = context.checkSelfPermission(ACCESS_LOCAL_NETWORK_PERMISSION) == PackageManager.PERMISSION_GRANTED,
        sdkInt = Build.VERSION.SDK_INT,
        targetSdk = context.applicationInfo.targetSdkVersion,
    )

