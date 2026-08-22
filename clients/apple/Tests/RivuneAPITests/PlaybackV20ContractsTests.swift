import Foundation
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif
import XCTest
@testable import RivuneAPI

final class PlaybackV20ContractsTests: XCTestCase {
    private let titleId = UUID(uuidString: "11111111-1111-4111-8111-111111111111")!

    func testDiscoveryMetadataAndRequiredContentModelsDecode() throws {
        let discovery = try JSONDecoder().decode(Discovery.self, from: Data("""
        {"name":"Rivune","serverVersion":"20.0","protocolVersion":20,"apiBaseUrl":"/api/v1","setupRequired":false,"setupCompleted":true,"demoAvailable":false,"timezone":"UTC","interfaceLanguage":"en-US"}
        """.utf8))
        XCTAssertEqual(discovery.setupCompleted, true)
        XCTAssertEqual(discovery.demoAvailable, false)

        let movie = try JSONDecoder().decode(Movie.self, from: Data("""
        {"id":"11111111-1111-4111-8111-111111111111","mediaType":"movie","title":"Movie","originalTitle":"Movie","originalLanguage":"en","overview":"Overview","genres":[],"cast":[{"id":"42","name":"Actor","character":"Hero","profileUrl":"/actor.jpg"}],"voteAverage":8.0,"voteCount":10,"externalIds":{}}
        """.utf8))
        XCTAssertEqual(movie.cast.first?.character, "Hero")

        let series = try JSONDecoder().decode(Series.self, from: Data("""
        {"id":"11111111-1111-4111-8111-111111111111","mediaType":"series","name":"Series","originalName":"Series","originalLanguage":"en","overview":"Overview","genres":[],"cast":[],"voteAverage":7.0,"voteCount":5,"seasons":[],"aliases":[],"episodeOrders":[],"selectedEpisodeOrderId":"99","mappingProvider":"tmdb","externalIds":{}}
        """.utf8))
        XCTAssertEqual(series.selectedEpisodeOrderId, "99")
        XCTAssertThrowsError(try JSONDecoder().decode(Movie.self, from: Data("""
        {"id":"11111111-1111-4111-8111-111111111111","mediaType":"movie","title":"Movie","originalTitle":"Movie","originalLanguage":"en","overview":"Overview","genres":[],"voteAverage":8.0,"voteCount":10,"externalIds":{}}
        """.utf8)))
    }

    func testMarkerProgressAndContinueWatchingModelsDecodeClosedEnums() throws {
        let marker = try JSONDecoder().decode(PlaybackMarker.self, from: Data("""
        {"type":"intro","startSeconds":1.25,"endSeconds":42.75,"confidence":0.9,"submissionCount":3}
        """.utf8))
        XCTAssertEqual(marker.type, .intro)
        XCTAssertEqual(marker.startSeconds, 1.25)

        let progress = try JSONDecoder().decode(PlaybackProgress.self, from: progressJSON)
        XCTAssertEqual(progress.mediaType, .movie)
        XCTAssertEqual(progress.version, 9_223_372_036_854_775_000)

        let batch = try JSONDecoder().decode(PlaybackProgressBatch.self, from: Data("""
        {"items":[{"titleId":"11111111-1111-4111-8111-111111111111","progress":null}]}
        """.utf8))
        XCTAssertNil(batch.items.first?.progress)
        XCTAssertThrowsError(try JSONDecoder().decode(PlaybackProgressBatch.self, from: Data("""
        {"items":[{"titleId":"11111111-1111-4111-8111-111111111111"}]}
        """.utf8)))

        let page = try JSONDecoder().decode(ContinueWatchingPage.self, from: Data("""
        {"items":[{"titleId":"11111111-1111-4111-8111-111111111111","mediaType":"episode","seriesId":"22222222-2222-4222-8222-222222222222","seasonId":"33333333-3333-4333-8333-333333333333","seasonNumber":1,"episodeNumber":2,"positionSeconds":0,"durationSeconds":2700,"version":7,"reason":"next_episode","lastWatchedAt":"2026-08-03T10:00:00Z"}]}
        """.utf8))
        XCTAssertEqual(page.items.first?.reason, .nextEpisode)
        XCTAssertThrowsError(try JSONDecoder().decode(PlaybackMarker.self, from: Data("""
        {"type":"credits","startSeconds":1.0,"endSeconds":2.0,"confidence":1.0,"submissionCount":1}
        """.utf8)))
    }

    func testTitleResolveInputNormalizesReleaseDates() {
        let timestamp = TitleResolveInput(
            mediaType: .movie,
            provider: "addon",
            resourceId: "movie-1",
            title: "Film",
            released: "2026-08-12T15:30:45.123+02:00"
        )
        XCTAssertEqual(timestamp.released, "2026-08-12")

        let dateOnly = TitleResolveInput(mediaType: .movie, provider: "addon", resourceId: "movie-2", title: "Film", released: "2026-08-12")
        XCTAssertEqual(dateOnly.released, "2026-08-12")

        let yearOnly = TitleResolveInput(mediaType: .movie, provider: "addon", resourceId: "movie-3", title: "Film", released: "2026")
        XCTAssertNil(yearOnly.released)

        let impossibleDate = TitleResolveInput(mediaType: .movie, provider: "addon", resourceId: "movie-4", title: "Film", released: "2026-02-30T15:30:00Z")
        XCTAssertNil(impossibleDate.released)
    }

    func testMetadataMarkersAndSourcesRequestsUseV20QueryAndBody() async throws {
        let transport = V20RecordingTransport()
        let client = try makeClient(transport: transport)
        _ = try await client.series(id: titleId, language: "fr-FR", mappingProvider: .tvdb, episodeOrder: "42")
        _ = try await client.playbackMarkers(imdbId: "tt1234567", season: 2, episode: 3)
        let addonId = UUID(uuidString: "44444444-4444-4444-8444-444444444444")!
        let sourceList = try await client.playbackSources(
            mediaType: "series",
            addonId: addonId,
            resourceId: "resource",
            capabilities: PlaybackCapabilities(streamingProtocols: ["hls"], containers: ["mp4"])
        )
        XCTAssertEqual(sourceList.sources.first?.mode, .external)

        let requests = transport.apiRequests()
        XCTAssertEqual(query(requests[0])["episodeOrder"], "42")
        XCTAssertEqual(query(requests[0])["mappingProvider"], "tvdb")
        XCTAssertEqual(query(requests[1]), ["imdbId": "tt1234567", "season": "2", "episode": "3"])
        let body = try XCTUnwrap(JSONSerialization.jsonObject(with: try XCTUnwrap(requests[2].httpBody)) as? [String: Any])
        XCTAssertEqual((body["addonId"] as? String)?.lowercased(), addonId.uuidString.lowercased())
    }

    func testProgress204AndEveryMutationRequestContract() async throws {
        let transport = V20RecordingTransport()
        let client = try makeClient(transport: transport)
        let missingProgress = try await client.playbackProgress(titleId: titleId)
        XCTAssertNil(missingProgress)
        _ = try await client.playbackProgressBatch(titleIds: [titleId])
        _ = try await client.updatePlaybackProgress(
            titleId: titleId,
            input: UpdatePlaybackProgressRequest(positionSeconds: 12, durationSeconds: 100, completed: false, expectedVersion: 5)
        )
        try await client.clearPlaybackProgress(titleId: titleId, expectedVersion: 6)
        _ = try await client.setTitlesWatchedBatch([SetWatchedBatchItem(titleId: titleId, completed: true, expectedVersion: 7)])
        _ = try await client.markTitleWatched(titleId: titleId, expectedVersion: 8)
        _ = try await client.markTitleUnwatched(titleId: titleId, expectedVersion: 9)
        let continuePage = try await client.continueWatching(limit: 25)
        try await client.dismissContinueWatchingTitle(titleId: titleId)

        let requests = transport.apiRequests()
        XCTAssertEqual(requests.map(\.httpMethod), ["GET", "POST", "PUT", "DELETE", "PUT", "POST", "DELETE", "GET", "DELETE"])
        XCTAssertEqual(requests[0].url?.path, "/api/v1/progress/\(titleId.uuidString.lowercased())")
        XCTAssertEqual(requests.map { $0.url?.path }, [
            "/api/v1/progress/\(titleId.uuidString.lowercased())",
            "/api/v1/progress/batch",
            "/api/v1/progress/\(titleId.uuidString.lowercased())",
            "/api/v1/progress/\(titleId.uuidString.lowercased())",
            "/api/v1/titles/watched/batch",
            "/api/v1/titles/\(titleId.uuidString.lowercased())/watched",
            "/api/v1/titles/\(titleId.uuidString.lowercased())/watched",
            "/api/v1/continue-watching",
            "/api/v1/continue-watching/\(titleId.uuidString.lowercased())"
        ])
        XCTAssertEqual(query(requests[3])["expectedVersion"], "6")
        XCTAssertEqual(query(requests[6])["expectedVersion"], "9")
        XCTAssertEqual(query(requests[7])["limit"], "25")
        let continueItem = try XCTUnwrap(continuePage.items.first)
        XCTAssertEqual(continueItem.title, "Signal Horizon")
        XCTAssertEqual(continueItem.posterUrl, "/series-poster")
        XCTAssertEqual(continueItem.backgroundUrl, "/series-background")
        XCTAssertEqual(continueItem.releaseInfo, "2026")
        XCTAssertEqual(continueItem.resourceId, "tt9000:2:3")
        XCTAssertEqual(continueItem.resourceProvider, "imdb")
        XCTAssertEqual(continueItem.episodeTitle, "Moonrise")
        XCTAssertEqual(continueItem.episodeStillUrl, "/episode-still")
        XCTAssertEqual(continueItem.episodeAirDate, "2026-08-15")

        let batchBody = try XCTUnwrap(JSONSerialization.jsonObject(with: try XCTUnwrap(requests[1].httpBody)) as? [String: Any])
        XCTAssertEqual((batchBody["titleIds"] as? [String])?.map { $0.lowercased() }, [titleId.uuidString.lowercased()])
        let updateBody = try XCTUnwrap(JSONSerialization.jsonObject(with: try XCTUnwrap(requests[2].httpBody)) as? [String: Any])
        XCTAssertEqual((updateBody["expectedVersion"] as? NSNumber)?.int64Value, 5)
        let watchedBody = try XCTUnwrap(JSONSerialization.jsonObject(with: try XCTUnwrap(requests[4].httpBody)) as? [String: Any])
        XCTAssertEqual(((watchedBody["items"] as? [[String: Any]])?.first?["expectedVersion"] as? NSNumber)?.int64Value, 7)
        let markBody = try XCTUnwrap(JSONSerialization.jsonObject(with: try XCTUnwrap(requests[5].httpBody)) as? [String: Any])
        XCTAssertEqual((markBody["expectedVersion"] as? NSNumber)?.int64Value, 8)
    }

    func testExternalPlaybackTargetIsOptionalAndEncodesOnlyWhenTrue() async throws {
        let transport = V20RecordingTransport()
        let client = try makeClient(transport: transport)
        _ = try await client.preparePlayback(sourceRef: "opaque-source-reference")
        _ = try await client.preparePlayback(sourceRef: "opaque-source-reference", externalPlayer: true)
        _ = try await client.resolvePlayback(sourceRef: "opaque-source-reference", externalPlayer: true)

        let requests = transport.apiRequests()
        let defaultBody = try XCTUnwrap(JSONSerialization.jsonObject(with: try XCTUnwrap(requests[0].httpBody)) as? [String: Any])
        let prepareBody = try XCTUnwrap(JSONSerialization.jsonObject(with: try XCTUnwrap(requests[1].httpBody)) as? [String: Any])
        let resolveBody = try XCTUnwrap(JSONSerialization.jsonObject(with: try XCTUnwrap(requests[2].httpBody)) as? [String: Any])
        XCTAssertNil(defaultBody["externalPlayer"])
        XCTAssertEqual(prepareBody["externalPlayer"] as? Bool, true)
        XCTAssertEqual(resolveBody["externalPlayer"] as? Bool, true)
    }

    func testCoordinationAndLocalRecommendationRoutes() async throws {
        let transport = V20RecordingTransport()
        let client = try makeClient(transport: transport)
        let targetSession = UUID(uuidString: "77777777-7777-4777-8777-777777777777")!
        let roomId = UUID(uuidString: "88888888-8888-4888-8888-888888888888")!
        let item = CoordinatedPlaybackItem(titleId: titleId, mediaType: "movie", resourceId: "opaque", title: "Movie")

        _ = try await client.updatePlaybackDevice(PlaybackDeviceHeartbeatInput(
            capabilities: ["remote-control"],
            state: PlaybackDeviceState(status: "paused", item: item, positionMilliseconds: 1_000, durationMilliseconds: 10_000)
        ))
        _ = try await client.playbackDevices()
        _ = try await client.sendPlaybackCommand(sessionId: targetSession, input: PlaybackCommandInput(command: "load", item: item, positionMilliseconds: 1_000))
        _ = try await client.playbackCommands(after: 9)
        try await client.acknowledgePlaybackCommand(10)
        _ = try await client.createPlaybackRoom(PlaybackRoomCreateInput(item: item, state: "paused", positionMilliseconds: 1_000, durationMilliseconds: 10_000))
        _ = try await client.joinPlaybackRoom(code: "23456789AB")
        _ = try await client.playbackRoom(id: roomId)
        _ = try await client.updatePlaybackRoom(id: roomId, input: PlaybackRoomUpdateInput(state: "playing", positionMilliseconds: 2_000, durationMilliseconds: 10_000, expectedVersion: 1))
        try await client.leavePlaybackRoom(id: roomId)
        let recommendations = try await client.localRecommendations(limit: 12)

        XCTAssertEqual(recommendations.items.first?.reason, "Because you like Drama")
        let requests = transport.apiRequests()
        XCTAssertEqual(requests.map { $0.url?.path }, [
            "/api/v1/playback/device", "/api/v1/playback/devices",
            "/api/v1/playback/devices/\(targetSession.uuidString.lowercased())/commands",
            "/api/v1/playback/commands", "/api/v1/playback/commands/10/ack",
            "/api/v1/playback/rooms", "/api/v1/playback/rooms/join",
            "/api/v1/playback/rooms/\(roomId.uuidString.lowercased())",
            "/api/v1/playback/rooms/\(roomId.uuidString.lowercased())",
            "/api/v1/playback/rooms/\(roomId.uuidString.lowercased())", "/api/v1/recommendations",
        ])
        XCTAssertEqual(query(requests[3])["after"], "9")
        XCTAssertEqual(query(requests[10])["limit"], "12")
    }

    func testPlaybackRoomMemberUsesOpaqueMemberID() throws {
        let room = try JSONDecoder().decode(PlaybackRoom.self, from: Data(V20RecordingTransport().roomJSON.utf8))
        let member = try XCTUnwrap(room.currentMember)

        XCTAssertEqual(member.id, member.memberId)
        XCTAssertEqual(member.memberId.uuidString.lowercased(), "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
        XCTAssertEqual(member.profile, "Viewer")
        XCTAssertTrue(room.currentMemberIsHost)
        XCTAssertThrowsError(try JSONDecoder().decode(PlaybackRoomMember.self, from: Data("""
        {"sessionId":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","profileId":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","profile":"Viewer","deviceName":"Apple TV","platform":"tvos","role":"host","current":true,"joinedAt":"2099-01-01T00:00:00Z","lastSeenAt":"2099-01-01T00:00:01Z"}
        """.utf8)))
    }

    func testPlaybackRoomRefreshPreservesHostJoinCodeOnlyForSameRoom() throws {
        let initial = try decodeRoom(id: "88888888-8888-4888-8888-888888888888", joinCode: "23456789AB", version: 1)
        let refreshed = try decodeRoom(id: "88888888-8888-4888-8888-888888888888", joinCode: nil, version: 2)
        let replacement = try decodeRoom(id: "99999999-9999-4999-8999-999999999999", joinCode: nil, version: 1)

        XCTAssertEqual(refreshed.preservingJoinCode(from: initial).joinCode, "23456789AB")
        XCTAssertNil(replacement.preservingJoinCode(from: initial).joinCode)
    }

    private func decodeRoom(id: String, joinCode: String?, version: Int64) throws -> PlaybackRoom {
        let code = joinCode.map { "\"joinCode\":\"\($0)\"," } ?? ""
        return try JSONDecoder().decode(PlaybackRoom.self, from: Data("""
        {"id":"\(id)",\(code)"item":{"titleId":"11111111-1111-4111-8111-111111111111","mediaType":"movie","resourceId":"opaque","title":"Movie"},"state":"paused","positionMilliseconds":0,"durationMilliseconds":0,"version":\(version),"updatedAt":"2026-08-21T00:00:00Z","expiresAt":"2026-08-22T00:00:00Z","members":[]}
        """.utf8))
    }
    private var progressJSON: Data {
        Data("""
        {"titleId":"11111111-1111-4111-8111-111111111111","mediaType":"movie","positionSeconds":12,"durationSeconds":100,"completed":false,"version":9223372036854775000,"lastWatchedAt":"2026-08-03T10:00:00Z","updatedAt":"2026-08-03T10:01:00Z"}
        """.utf8)
    }

    private func makeClient(transport: V20RecordingTransport) throws -> RivuneAPIClient {
        try RivuneAPIClient(
            serverURL: URL(string: "https://example.test")!,
            transport: transport,
            credentialStore: V20CredentialStore(token: TokenPair(
                tokenType: "Bearer",
                accessToken: "access",
                accessTokenExpiresAt: "2026-08-03T12:15:00Z",
                refreshToken: "refresh",
                refreshTokenExpiresAt: "2026-09-03T12:00:00Z",
                sessionId: UUID(uuidString: "55555555-5555-4555-8555-555555555555")!,
                deviceId: UUID(uuidString: "66666666-6666-4666-8666-666666666666")!,
                authorizationScope: .globalAdministrator,
                category: nil
            ))
        )
    }

    private func query(_ request: URLRequest) -> [String: String] {
        Dictionary(uniqueKeysWithValues: (URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.queryItems ?? []).map { ($0.name, $0.value ?? "") })
    }
}

private struct V20CredentialStore: CredentialStore {
    let token: TokenPair
    func load(for issuer: URL) async throws -> StoredCredentials? { StoredCredentials(tokens: token, profileContext: nil) }
    func save(_ credentials: StoredCredentials, for issuer: URL) async throws {}
    func clear(for issuer: URL) async throws {}
}

private final class V20RecordingTransport: HTTPTransport, @unchecked Sendable {
    private let lock = NSLock()
    private var requests: [URLRequest] = []

    func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        let path = request.url!.path
        if path == "/.well-known/rivune" {
            return response(request, status: 200, body: Data("""
            {"name":"Rivune","serverVersion":"20.0","protocolVersion":20,"apiBaseUrl":"/api/v1","setupRequired":false,"setupCompleted":true,"demoAvailable":false,"timezone":"UTC","interfaceLanguage":"en-US"}
            """.utf8))
        }
        lock.withLock {
            requests.append(request)
        }

        if path.hasPrefix("/api/v1/progress/") && request.httpMethod == "GET" {
            return response(request, status: 204, body: Data())
        }
        if path.hasSuffix("/metadata/series/11111111-1111-4111-8111-111111111111") {
            return response(request, body: Data("""
            {"id":"11111111-1111-4111-8111-111111111111","mediaType":"series","name":"Series","originalName":"Series","originalLanguage":"en","overview":"Overview","genres":[],"cast":[],"voteAverage":7,"voteCount":5,"seasons":[],"aliases":[],"episodeOrders":[],"selectedEpisodeOrderId":"42","mappingProvider":"tvdb","externalIds":{}}
            """.utf8))
        }
        if path.hasSuffix("/playback/markers") {
            return response(request, body: Data("{\"markers\":[]}".utf8))
        }
        if path.hasSuffix("/playback/sources") {
            return response(request, body: Data("{\"sources\":[{\"id\":\"stream-1\",\"sourceRef\":\"opaque-source-reference\",\"stableIdentity\":\"stable-external\",\"addonId\":\"44444444-4444-4444-8444-444444444444\",\"manifestId\":\"manifest\",\"streamIndex\":0,\"name\":\"External\",\"protocol\":\"external\",\"mode\":\"external\",\"expiresAt\":\"2099-01-01T00:00:00Z\"}],\"providerErrors\":[]}".utf8))
        }
        if path.hasSuffix("/playback/prepare") {
            return response(request, body: Data("{\"sourceRef\":\"opaque-source-reference\",\"mode\":\"direct\",\"protocol\":\"http\",\"subtitleCount\":0,\"expiresAt\":\"2099-01-01T00:00:00Z\"}".utf8))
        }
        if path.hasSuffix("/playback/resolve") {
            return response(request, status: 201, body: Data("{\"id\":\"44444444-4444-4444-8444-444444444444\",\"selectedSourceId\":\"stream-1\",\"sources\":[],\"subtitles\":[],\"providerErrors\":[],\"expiresAt\":\"2099-01-01T00:00:00Z\"}".utf8))
        }
        if path.hasSuffix("/progress/batch") {
            return response(request, body: Data("{\"items\":[{\"titleId\":\"11111111-1111-4111-8111-111111111111\",\"progress\":null}]}".utf8))
        }
        if path.hasSuffix("/titles/watched/batch") {
            return response(request, body: Data("{\"items\":[{\"titleId\":\"11111111-1111-4111-8111-111111111111\",\"progress\":\(progress)}]}".utf8))
        }
        if path.hasSuffix("/continue-watching") && request.httpMethod == "GET" {
            return response(request, body: Data("""
            {"items":[{"titleId":"11111111-1111-4111-8111-111111111111","mediaType":"episode","seriesId":"22222222-2222-4222-8222-222222222222","seasonId":"33333333-3333-4333-8333-333333333333","seasonNumber":2,"episodeNumber":3,"positionSeconds":120,"durationSeconds":1800,"version":1,"reason":"resume","title":"Signal Horizon","posterUrl":"/series-poster","backgroundUrl":"/series-background","releaseInfo":"2026","resourceId":"tt9000:2:3","resourceProvider":"imdb","episodeTitle":"Moonrise","episodeStillUrl":"/episode-still","episodeAirDate":"2026-08-15","lastWatchedAt":"2026-08-15T00:00:00Z"}]}
            """.utf8))
        }
        if path.hasSuffix("/playback/device") {
            return response(request, body: Data(deviceJSON.utf8))
        }
        if path.hasSuffix("/playback/devices") {
            return response(request, body: Data("{\"devices\":[]}".utf8))
        }
        if path.contains("/playback/devices/") && path.hasSuffix("/commands") {
            return response(request, status: 201, body: Data(commandJSON.utf8))
        }
        if path.hasSuffix("/playback/commands") {
            return response(request, body: Data("{\"commands\":[]}".utf8))
        }
        if path.hasSuffix("/ack") {
            return response(request, status: 204, body: Data())
        }
        if path.hasSuffix("/playback/rooms") || path.hasSuffix("/playback/rooms/join") || path.contains("/playback/rooms/") {
            if request.httpMethod == "DELETE" { return response(request, status: 204, body: Data()) }
            return response(request, status: path.hasSuffix("/playback/rooms") && request.httpMethod == "POST" ? 201 : 200, body: Data(roomJSON.utf8))
        }
        if path.hasSuffix("/recommendations") {
            return response(request, body: Data("{\"items\":[{\"item\":{\"id\":\"11111111-1111-4111-8111-111111111111\",\"mediaType\":\"movie\",\"title\":\"Movie\",\"providerIds\":{}},\"reason\":\"Because you like Drama\",\"score\":4.5}]}".utf8))
        }
        if request.httpMethod == "DELETE" {
            if path.contains("/watched") { return response(request, body: Data(progress.utf8)) }
            return response(request, status: 204, body: Data())
        }
        return response(request, body: Data(progress.utf8))
    }

    func apiRequests() -> [URLRequest] {
        lock.withLock { requests }
    }

    private var progress: String {
        "{\"titleId\":\"11111111-1111-4111-8111-111111111111\",\"mediaType\":\"movie\",\"positionSeconds\":12,\"durationSeconds\":100,\"completed\":false,\"version\":10,\"lastWatchedAt\":\"2026-08-03T10:00:00Z\",\"updatedAt\":\"2026-08-03T10:01:00Z\"}"
    }

    private var deviceJSON: String {
        "{\"sessionId\":\"55555555-5555-4555-8555-555555555555\",\"deviceId\":\"66666666-6666-4666-8666-666666666666\",\"name\":\"Device\",\"platform\":\"apple\",\"capabilities\":[\"remote-control\"],\"state\":{\"status\":\"idle\",\"positionMilliseconds\":0,\"durationMilliseconds\":0},\"current\":true,\"lastSeenAt\":\"2099-01-01T00:00:00Z\"}"
    }

    private var commandJSON: String {
        "{\"id\":10,\"command\":\"play\",\"senderDeviceName\":\"Sender\",\"createdAt\":\"2099-01-01T00:00:00Z\",\"expiresAt\":\"2099-01-01T00:02:00Z\"}"
    }

    fileprivate var roomJSON: String {
        "{\"id\":\"88888888-8888-4888-8888-888888888888\",\"joinCode\":\"23456789AB\",\"item\":{\"titleId\":\"11111111-1111-4111-8111-111111111111\",\"mediaType\":\"movie\",\"resourceId\":\"opaque\",\"title\":\"Movie\"},\"state\":\"paused\",\"positionMilliseconds\":1000,\"durationMilliseconds\":10000,\"version\":1,\"updatedAt\":\"2099-01-01T00:00:00Z\",\"expiresAt\":\"2099-01-01T08:00:00Z\",\"members\":[{\"memberId\":\"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa\",\"profile\":\"Viewer\",\"deviceName\":\"Apple TV\",\"platform\":\"tvos\",\"role\":\"host\",\"current\":true,\"joinedAt\":\"2099-01-01T00:00:00Z\",\"lastSeenAt\":\"2099-01-01T00:00:01Z\"}]}"
    }

    private func response(_ request: URLRequest, status: Int = 200, body: Data) -> (Data, HTTPURLResponse) {
        (body, HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!)
    }
}
