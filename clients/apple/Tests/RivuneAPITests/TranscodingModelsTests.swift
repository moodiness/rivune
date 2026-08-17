import Foundation
import XCTest
@testable import RivuneAPI

final class TranscodingModelsTests: XCTestCase {
    func testCapabilitiesEncodeServerOutputDeclarations() throws {
        let capabilities = PlaybackCapabilities(
            streamingProtocols: ["hls"],
            containers: ["mp4"],
            processingModes: [.remux, .transcodeAudio, .transcode],
            maximumHeight: 2160,
            maximumVideoBitrateKbps: 12_000,
            maximumAudioChannels: 6,
            subtitleModes: [.external, .burn],
            mediaProfiles: [PlaybackMediaProfile(container: "mp4", videoCodec: "h265", audioCodec: "aac", maximumVideoBitDepth: 10)]
        )

        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: JSONEncoder().encode(capabilities)) as? [String: Any])
        XCTAssertEqual(object["processingModes"] as? [String], ["remux", "transcode_audio", "transcode"])
        XCTAssertEqual(object["maximumHeight"] as? Int, 2160)
        XCTAssertEqual(object["maximumVideoBitrateKbps"] as? Int, 12_000)
        XCTAssertEqual(object["maximumAudioChannels"] as? Int, 6)
        XCTAssertEqual(object["subtitleModes"] as? [String], ["external", "burn"])
        XCTAssertEqual(((object["mediaProfiles"] as? [[String: Any]])?.first)?["maximumVideoBitDepth"] as? Int, 10)
    }

    func testSourceListDecodesOptionalAddonNameAndSourceIdentity() throws {
        let json = Data("""
        {
          "sources":[
            {"id":"source-1","sourceRef":"ref-1","stableIdentity":"stable-primary","addonId":"66666666-6666-4666-8666-666666666666","addonName":"Test Addon","manifestId":"org.test","streamIndex":0,"name":"Primary","protocol":"hls","expiresAt":"2026-08-03T12:00:00Z"},
            {"id":"source-2","sourceRef":"ref-2","addonId":"77777777-7777-4777-8777-777777777777","manifestId":"org.other","streamIndex":1,"name":"Fallback","protocol":"dash","expiresAt":"2026-08-03T12:00:00Z"}
          ],
          "providerErrors":[]
        }
        """.utf8)

        let sourceList = try JSONDecoder().decode(PlaybackSourceList.self, from: json)
        XCTAssertEqual(sourceList.sources[0].addonName, "Test Addon")
        XCTAssertEqual(sourceList.sources[0].addonId, UUID(uuidString: "66666666-6666-4666-8666-666666666666"))
        XCTAssertEqual(sourceList.sources[0].manifestId, "org.test")
        XCTAssertEqual(sourceList.sources[0].sourceRef, "ref-1")
        XCTAssertEqual(sourceList.sources[0].stableIdentity, "stable-primary")
        XCTAssertNil(sourceList.sources[1].addonName)
        XCTAssertEqual(sourceList.sources[1].addonId, UUID(uuidString: "77777777-7777-4777-8777-777777777777"))
        XCTAssertEqual(sourceList.sources[1].manifestId, "org.other")
        XCTAssertEqual(sourceList.sources[1].sourceRef, "ref-2")
        XCTAssertEqual(sourceList.sources[1].stableIdentity, "")
    }

    func testSessionDecodesDecisionBurnSubtitleSelectionsAndUnknownProperties() throws {
        let json = """
        {
          "id":"22222222-2222-4222-8222-222222222222",
          "selectedSourceId":"source-1",
          "selectedAudioTrack":2,
          "selectedSubtitleId":"subtitle-1",
          "sources":[{
            "id":"source-1","addonId":"66666666-6666-4666-8666-666666666666","manifestId":"org.test",
            "mode":"transcode","protocol":"hls","mediaTimeline":"relative","compatible":true,
            "decision":{"reason":"subtitle_burn_required","videoAction":"transcode","audioAction":"copy","subtitleAction":"burn","toneMapping":false,"source":{"container":"matroska","videoCodec":"hevc","height":2160,"videoBitrateKbps":24000,"hdrFormat":"dolby_vision"},"target":{"protocol":"hls","container":"mpegts","videoCodec":"h264","audioCodec":"aac","height":1080,"videoBitDepth":8,"videoBitrateKbps":12000},"futureDecisionField":true}
          }],
          "subtitles":[{"id":"subtitle-1","addonId":"66666666-6666-4666-8666-666666666666","manifestId":"org.test","default":true,"delivery":"burn","futureSubtitleField":"ignored"}],
          "providerErrors":[{"addonId":"66666666-6666-4666-8666-666666666666","manifestId":"org.test","code":"future_provider_code","message":"future"}],
          "expiresAt":"2026-08-03T12:00:00Z",
          "futureSessionField":"ignored"
        }
        """.data(using: .utf8)!

        let session = try JSONDecoder().decode(PlaybackSession.self, from: json)
        XCTAssertEqual(session.selectedAudioTrack, 2)
        XCTAssertEqual(session.selectedSubtitleId, "subtitle-1")
        XCTAssertEqual(session.sources.first?.decision?.reason, .subtitleBurnRequired)
        XCTAssertEqual(session.sources.first?.mediaTimeline, .relative)
        XCTAssertEqual(session.sources.first?.decision?.videoAction, .transcode)
        XCTAssertEqual(session.sources.first?.decision?.source?.hdrFormat, "dolby_vision")
        XCTAssertEqual(session.sources.first?.decision?.target?.videoBitrateKbps, 12_000)
        XCTAssertEqual(session.sources.first?.decision?.target?.videoBitDepth, 8)
        XCTAssertEqual(session.subtitles.first?.delivery, .burn)
        XCTAssertNil(session.subtitles.first?.url)
        XCTAssertEqual(session.providerErrors.first?.code, "future_provider_code")
    }

    func testActivityDecodesDecisionDetails() throws {
        let json = """
        {
          "summary":{"activeSessions":1,"activeJobs":2,"processingSlots":1,"processingLimit":2,"storageBytes":1024,"storageLimitBytes":1048576},
          "diagnostics":{"ffmpegVersion":"7.1","ffprobeVersion":"7.1","hardwareAcceleration":"software","videoEncoder":"libx264","preferredVideoCodec":"h264","encodeCodecs":["h264"],"decodeCodecs":["h264","hevc"],"hevcMain10":true,"qualityPreset":"balanced","hardwareToneMap":false,"toneMapBackend":"software","transcodeThreads":4,"maximumReadRate":2.0,"totals":{"started":5,"succeeded":3,"failed":1,"softwareFallbacks":1},"pools":{"process":{"active":1,"limit":2},"probe":{"active":0,"limit":2},"subtitle":{"active":0,"limit":2},"trickplay":{"active":0,"limit":1}}},
          "sessions":[{
            "id":"22222222-2222-4222-8222-222222222222","title":"Contract Movie","mediaType":"movie","mode":"transcode",
            "decision":{"reason":"video_transcode_required","videoAction":"transcode","audioAction":"transcode","subtitleAction":"none","toneMapping":true,"target":{"videoCodec":"h264","height":1080,"videoBitrateKbps":12000}},
            "username":"admin","profileId":"44444444-4444-4444-8444-444444444444","profile":"Admin","device":"iPhone","platform":"ios",
            "processing":true,"positionSeconds":120,"durationSeconds":7200,
            "createdAt":"2026-08-03T10:00:00Z","lastSeenAt":"2026-08-03T10:01:00Z","expiresAt":"2026-08-03T12:00:00Z"
          }],
          "jobs":[
            {"sessionId":"22222222-2222-4222-8222-222222222222","assetId":"asset-1","mode":"transcode","state":"processing","prewarming":false,"progressPercent":37.5,"speed":1.25,"startupDurationSeconds":2.5,"createdAt":"2026-08-03T10:00:00Z","lastSeenAt":"2026-08-03T10:01:00Z"},
            {"assetId":"asset-2","mode":"transcode","state":"failed","errorClass":"source","prewarming":true,"createdAt":"2026-08-03T10:00:00Z","lastSeenAt":"2026-08-03T10:01:00Z"}
          ],
          "sessionsTruncated":false,
          "jobsTruncated":true,
          "futureActivityField":"ignored"
        }
        """.data(using: .utf8)!

        let activity = try JSONDecoder().decode(PlaybackActivity.self, from: json)
        XCTAssertEqual(activity.sessions.first?.decision?.target?.height, 1080)
        XCTAssertEqual(activity.sessions.first?.mode, .transcode)
        XCTAssertEqual(activity.jobs.first?.state, .processing)
        XCTAssertEqual(activity.diagnostics.hardwareAcceleration, .software)
        XCTAssertEqual(activity.diagnostics.preferredVideoCodec, .h264)
        XCTAssertEqual(activity.diagnostics.qualityPreset, .balanced)
        XCTAssertEqual(activity.diagnostics.toneMapBackend, .software)
        XCTAssertTrue(activity.sessions.first?.decision?.toneMapping == true)
        XCTAssertEqual(activity.jobs.first?.progressPercent, 37.5)
        XCTAssertEqual(activity.jobs.first?.speed, 1.25)
        XCTAssertNil(activity.jobs.last?.progressPercent)
        XCTAssertNil(activity.jobs.last?.speed)
        XCTAssertEqual(activity.jobs.last?.errorClass, .source)
        XCTAssertEqual(activity.diagnostics.totals.started, 5)
        XCTAssertEqual(activity.diagnostics.pools.process.limit, 2)
        XCTAssertFalse(activity.sessionsTruncated)
        XCTAssertTrue(activity.jobsTruncated)
    }

    func testSettingsDefaultsAndExplicitNullPatchesAreTolerant() throws {
        let values = try JSONDecoder().decode(SettingsValues.self, from: Data("{\"futureSetting\":true}".utf8))
        XCTAssertNil(values.allowTranscoding)
        XCTAssertNil(values.transcoding)
        XCTAssertNil(values.maximumCastMembers)

        let inherited = try JSONDecoder().decode(SettingsValues.self, from: Data("{\"maximumCastMembers\":null}".utf8))
        XCTAssertNil(inherited.maximumCastMembers)

        let effective = try JSONDecoder().decode(
            EffectiveSettings.self,
            from: Data(#"{"schemaVersion":1,"settings":{"allowTranscoding":false,"transcoding":"enabled","maximumCastMembers":20,"futureSetting":true},"sources":{"allowTranscoding":"instance","transcoding":"profile","maximumCastMembers":"instance","theme":"default"}}"#.utf8)
        )
        XCTAssertEqual(effective.settings.allowTranscoding, false)
        XCTAssertEqual(effective.settings.transcoding, "enabled")
        XCTAssertEqual(effective.settings.maximumCastMembers, 20)
        XCTAssertEqual(effective.sources.allowTranscoding, "instance")
        XCTAssertEqual(effective.sources.maximumCastMembers, "instance")

        let instance = try XCTUnwrap(JSONSerialization.jsonObject(with: JSONEncoder().encode(InstanceTranscodingPatch(allowTranscoding: nil))) as? [String: Any])
        XCTAssertTrue(instance["allowTranscoding"] is NSNull)
        let profile = try XCTUnwrap(JSONSerialization.jsonObject(with: JSONEncoder().encode(ProfileTranscodingPatch(transcoding: nil))) as? [String: Any])
        XCTAssertTrue(profile["transcoding"] is NSNull)

        let error = try JSONDecoder().decode(ServerError.self, from: Data(#"{"code":"future_error_code","message":"future","futureField":true}"#.utf8))
        XCTAssertEqual(error.code, "future_error_code")
    }
}
