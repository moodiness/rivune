package io.rivune.api

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

class TranscodingModelsTest {
    private val json = Json { ignoreUnknownKeys = true; explicitNulls = false }

    @Test
    fun capabilitiesEncodeServerOutputDeclarations() {
        val encoded = json.parseToJsonElement(json.encodeToString(PlaybackCapabilities(
            streamingProtocols = listOf("hls"),
            containers = listOf("mp4"),
            processingModes = listOf("remux", "transcode_audio", "transcode"),
            maximumHeight = 2160,
            maximumVideoBitrateKbps = 12_000,
            maximumAudioChannels = 6,
            subtitleModes = listOf("external", "burn"),
            mediaProfiles = listOf(PlaybackMediaProfile("mp4", "h264", "aac")),
        ))).jsonObject

        assertEquals(listOf("remux", "transcode_audio", "transcode"), encoded.getValue("processingModes").jsonArray.map { it.jsonPrimitive.content })
        assertEquals(2160, encoded.getValue("maximumHeight").jsonPrimitive.content.toInt())
        assertEquals(12_000, encoded.getValue("maximumVideoBitrateKbps").jsonPrimitive.content.toInt())
        assertEquals(6, encoded.getValue("maximumAudioChannels").jsonPrimitive.content.toInt())
        assertEquals(listOf("external", "burn"), encoded.getValue("subtitleModes").jsonArray.map { it.jsonPrimitive.content })
    }

    @Test
    fun sessionDecodesDecisionBurnSubtitleSelectionsAndUnknownProperties() {
        val session = json.decodeFromString<PlaybackSession>("""
            {
              "id":"22222222-2222-4222-8222-222222222222",
              "selectedSourceId":"source-1","selectedAudioTrack":2,"selectedSubtitleId":"subtitle-1",
              "sources":[{"id":"source-1","addonId":"66666666-6666-4666-8666-666666666666","manifestId":"org.test","mode":"transcode","protocol":"hls","compatible":true,"decision":{"reason":"subtitle_burn_required","videoAction":"transcode","audioAction":"copy","subtitleAction":"burn","toneMapping":false,"source":{"container":"matroska","videoCodec":"hevc","height":2160,"videoBitrateKbps":24000,"hdrFormat":"dolby_vision"},"target":{"protocol":"hls","container":"mpegts","videoCodec":"h264","audioCodec":"aac","height":1080,"videoBitrateKbps":12000},"futureDecisionField":true}}],
              "subtitles":[{"id":"subtitle-1","addonId":"66666666-6666-4666-8666-666666666666","manifestId":"org.test","default":true,"delivery":"burn","futureSubtitleField":"ignored"}],
              "providerErrors":[{"addonId":"66666666-6666-4666-8666-666666666666","manifestId":"org.test","code":"future_provider_code","message":"future"}],
              "expiresAt":"2026-08-03T12:00:00Z","futureSessionField":"ignored"
            }
        """.trimIndent())

        assertEquals(2, session.selectedAudioTrack)
        assertEquals("subtitle-1", session.selectedSubtitleId)
        assertEquals("subtitle_burn_required", session.sources.first().decision?.reason)
        assertEquals("dolby_vision", session.sources.first().decision?.source?.hdrFormat)
        assertEquals(12_000, session.sources.first().decision?.target?.videoBitrateKbps)
        assertEquals("burn", session.subtitles.first().delivery)
        assertNull(session.subtitles.first().url)
        assertEquals("future_provider_code", session.providerErrors.first().code)
    }

    @Test
    fun activityDecodesDecisionDetails() {
        val activity = json.decodeFromString<PlaybackActivity>("""
            {
              "summary":{"activeSessions":1,"activeJobs":2,"processingSlots":1,"processingLimit":2,"storageBytes":1024,"storageLimitBytes":1048576},
              "diagnostics":{"videoEncoder":"libx264","hardwareToneMap":false},
              "sessions":[{
                "id":"22222222-2222-4222-8222-222222222222","title":"Contract Movie","mediaType":"movie","mode":"transcode",
                "decision":{"reason":"video_transcode_required","videoAction":"transcode","audioAction":"transcode","subtitleAction":"none","toneMapping":true,"target":{"videoCodec":"h264","height":1080,"videoBitrateKbps":12000}},
                "username":"admin","profileId":"44444444-4444-4444-8444-444444444444","profile":"Admin","device":"Pixel","platform":"android",
                "processing":true,"positionSeconds":120,"durationSeconds":7200,
                "createdAt":"2026-08-03T10:00:00Z","lastSeenAt":"2026-08-03T10:01:00Z","expiresAt":"2026-08-03T12:00:00Z"
              }],
              "jobs":[
                {"sessionId":"22222222-2222-4222-8222-222222222222","assetId":"asset-1","mode":"transcode","state":"running","prewarming":false,"progressPercent":37.5,"speed":1.25,"createdAt":"2026-08-03T10:00:00Z","lastSeenAt":"2026-08-03T10:01:00Z"},
                {"assetId":"asset-2","mode":"transcode","state":"running","prewarming":true,"createdAt":"2026-08-03T10:00:00Z","lastSeenAt":"2026-08-03T10:01:00Z"}
              ],
              "futureActivityField":"ignored"
            }
        """.trimIndent())

        assertEquals(1080, activity.sessions.first().decision?.target?.height)
        assertEquals(true, activity.sessions.first().decision?.toneMapping)
        assertEquals(37.5, activity.jobs.first().progressPercent)
        assertEquals(1.25, activity.jobs.first().speed)
        assertNull(activity.jobs.last().progressPercent)
        assertNull(activity.jobs.last().speed)
    }

    @Test
    fun missingNewSettingsUseTolerantDefaults() {
        val values = json.decodeFromString<SettingsValues>("{\"futureSetting\":true}")
        assertNull(values.allowTranscoding)
        assertNull(values.transcoding)
        assertNull(values.maximumCastMembers)

        val inherited = json.decodeFromString<SettingsValues>("""{"maximumCastMembers":null}""")
        assertNull(inherited.maximumCastMembers)

        val effective = json.decodeFromString<EffectiveSettings>("""{"schemaVersion":1,"settings":{"allowTranscoding":false,"transcoding":"enabled","maximumCastMembers":20,"futureSetting":true},"sources":{"allowTranscoding":"instance","transcoding":"profile","maximumCastMembers":"instance","theme":"default"}}""")
        assertEquals(false, effective.settings.allowTranscoding)
        assertEquals("enabled", effective.settings.transcoding)
        assertEquals(20, effective.settings.maximumCastMembers)
        assertEquals("instance", effective.sources.allowTranscoding)
        assertEquals("instance", effective.sources.maximumCastMembers)
        val error = json.decodeFromString<ServerError>("""{"code":"future_error_code","message":"future","futureField":true}""")
        assertEquals("future_error_code", error.code)
    }
}
