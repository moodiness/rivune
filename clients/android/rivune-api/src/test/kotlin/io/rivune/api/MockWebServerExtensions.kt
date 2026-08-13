package io.rivune.api

import okhttp3.HttpUrl
import okhttp3.mockwebserver.MockWebServer

internal fun MockWebServer.loopbackUrl(path: String): HttpUrl =
    url(path).newBuilder().host("127.0.0.1").build()
