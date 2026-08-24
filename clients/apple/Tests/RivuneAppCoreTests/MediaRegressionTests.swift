import Foundation
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif
import XCTest
import RivuneAPI
@testable import RivuneAppCore

@MainActor
final class MediaRegressionTests: XCTestCase {
    func testSeriesProgressHydrationChunksMoreThanOneHundredEpisodes() async throws {
        let episodeIDs = (1...101).map(episodeID)
        let transport = MediaRegressionTransport(episodeIDs: episodeIDs)
        let (model, defaults) = try makeModel(transport: transport)
        defer { defaults.removePersistentDomain(forName: defaultsSuite(defaults)) }

        model.openMedia(seriesTarget())
        try await waitUntil { model.mediaDetail?.target.mediaType == "series" && !model.mediaLoading }

        XCTAssertEqual(model.episodeProgress.count, 101)
        let progressBatchSizes = await transport.progressBatchSizes()
        XCTAssertEqual(progressBatchSizes.sorted(), [1, 100])
        XCTAssertEqual(model.seriesEpisodesWatched, false)
    }

    func testSuccessfulWatchedChunkRemainsVisibleWhenLaterChunkConflicts() async throws {
        let episodeIDs = (1...101).map(episodeID)
        let transport = MediaRegressionTransport(episodeIDs: episodeIDs, failingWatchedBatch: 2)
        let (model, defaults) = try makeModel(transport: transport)
        defer { defaults.removePersistentDomain(forName: defaultsSuite(defaults)) }

        model.openMedia(seriesTarget())
        try await waitUntil { model.mediaDetail?.target.mediaType == "series" && !model.mediaLoading }
        model.toggleWatched()
        try await waitUntil { !model.mediaActionLoading }

        let watchedBatchSizes = await transport.watchedBatchSizes()
        XCTAssertEqual(watchedBatchSizes, [100, 1])
        XCTAssertEqual(model.episodeProgress.values.filter(\.completed).count, 100)
        XCTAssertEqual(model.seriesEpisodesWatched, false)
        XCTAssertNotNil(model.mediaFailure)
    }

    func testClosingEpisodeRestoresSeriesProgressSnapshot() async throws {
        let episodeIDs = [episodeID(1), episodeID(2)]
        let transport = MediaRegressionTransport(
            episodeIDs: episodeIDs,
            initiallyCompleted: [episodeIDs[0]]
        )
        let (model, defaults) = try makeModel(transport: transport)
        defer { defaults.removePersistentDomain(forName: defaultsSuite(defaults)) }

        model.openMedia(seriesTarget())
        try await waitUntil { model.mediaDetail?.target.mediaType == "series" && !model.mediaLoading }
        let progressBeforeEpisode = model.episodeProgress
        let watchedBeforeEpisode = model.seriesEpisodesWatched

        model.openEpisode(try decodeEpisode(id: episodeIDs[0], number: 1))
        try await waitUntil { model.mediaDetail?.target.mediaType == "episode" && !model.mediaLoading }
        XCTAssertTrue(model.canNavigateBackFromMedia)

        model.closeMedia()

        XCTAssertEqual(model.mediaDetail?.target.mediaType, "series")
        XCTAssertEqual(model.episodeProgress, progressBeforeEpisode)
        XCTAssertEqual(model.seriesEpisodesWatched, watchedBeforeEpisode)
    }

    func testExternalPlaybackPreservesEscapedPathAndQuery() async throws {
        let streamURL = "https://cdn.example.test/Video%20Library/Film%20Name.mp4?token=a%2Bb%3D%3D&quality=original"
        let transport = MediaRegressionTransport(episodeIDs: [], streamURL: streamURL)
        let (model, defaults) = try makeModel(transport: transport)
        defer { defaults.removePersistentDomain(forName: defaultsSuite(defaults)) }

        model.openMedia(movieTarget())
        try await waitUntil { model.mediaDetail?.target.mediaType == "movie" && !model.mediaLoading }
        model.loadPlaybackSources()
        try await waitUntil { !model.mediaLoading && model.playbackSources.count == 1 }
        model.play(try XCTUnwrap(model.playbackSources.first), externally: true)
        try await waitUntil { model.externalPlaybackURL != nil }

        let externalURL = try XCTUnwrap(model.externalPlaybackURL)
        let components = try XCTUnwrap(URLComponents(url: externalURL, resolvingAgainstBaseURL: false))
        XCTAssertEqual(components.percentEncodedPath, "/Video%20Library/Film%20Name.mp4")
        XCTAssertEqual(components.queryItems, [
            URLQueryItem(name: "token", value: "a+b=="),
            URLQueryItem(name: "quality", value: "original"),
        ])
    }

    func testEpisodePlaybackExposesAndStartsTheNextEpisode() async throws {
        let episodeIDs = [episodeID(1), episodeID(2)]
        let transport = MediaRegressionTransport(episodeIDs: episodeIDs)
        let (model, defaults) = try makeModel(transport: transport)
        defer { defaults.removePersistentDomain(forName: defaultsSuite(defaults)) }

        model.openMedia(seriesTarget())
        try await waitUntil { model.mediaDetail?.target.mediaType == "series" && !model.mediaLoading }
        model.openEpisode(try decodeEpisode(id: episodeIDs[0], number: 1))
        try await waitUntil { model.mediaDetail?.titleId == episodeIDs[0] && !model.mediaLoading }
        model.loadPlaybackSources()
        try await waitUntil { model.playbackSources.count == 1 && !model.mediaLoading }
        model.play(try XCTUnwrap(model.playbackSources.first), externally: false)
        try await waitUntil { model.playbackPresentation?.titleId == episodeIDs[0] }

        XCTAssertEqual(model.playbackPresentation?.nextEpisode?.titleId, episodeIDs[1])
        model.playNextEpisode(position: 1_800, duration: 1_800)
        try await waitUntil { model.playbackPresentation?.titleId == episodeIDs[1] }

        XCTAssertEqual(model.mediaDetail?.titleId, episodeIDs[1])
        XCTAssertNil(model.playbackPresentation?.nextEpisode)
    }

    func testSearchPaginationAppendsAndStopsAfterShortPage() async throws {
        let transport = NavigationParityTransport()
        let (model, defaults) = try makeModel(transport: transport)
        defer { defaults.removePersistentDomain(forName: defaultsSuite(defaults)) }

        model.searchQuery = "space opera"
        model.search()
        try await waitUntil { model.searchItems.count == 50 && !model.tabLoading }
        XCTAssertTrue(model.searchHasMore)

        model.loadMoreSearch()
        try await waitUntil { model.searchItems.count == 51 && !model.tabLoading }

        XCTAssertFalse(model.searchHasMore)
        XCTAssertFalse(model.searchPartial)
        let skips = await transport.searchSkips()
        XCTAssertEqual(skips, [0, 50])
    }

    func testLibraryPaginationFiltersAndCalendarMonthNavigation() async throws {
        let transport = NavigationParityTransport()
        let (model, defaults) = try makeModel(transport: transport)
        defer { defaults.removePersistentDomain(forName: defaultsSuite(defaults)) }

        model.selectTab(.library)
        try await waitUntil { model.libraryPage == 1 && !model.tabLoading }
        XCTAssertEqual(model.libraryItems.count, 1)
        XCTAssertEqual(model.libraryTotalPages, 2)

        model.loadMoreLibrary()
        try await waitUntil { model.libraryPage == 2 && !model.tabLoading }
        XCTAssertEqual(model.libraryItems.count, 2)

        model.setLibraryMediaType(.movie)
        try await waitUntil { model.libraryMediaType == .movie && model.libraryPage == 1 && !model.tabLoading }
        XCTAssertEqual(model.libraryItems.count, 1)
        let libraryTypes = await transport.libraryMediaTypes()
        XCTAssertEqual(libraryTypes, [nil, nil, "movie"])

        model.selectTab(.calendar)
        try await waitUntil { !model.tabLoading && model.selectedTab == .calendar }
        let firstMonth = model.calendarMonth
        model.nextCalendarMonth()
        try await waitUntil {
            !model.tabLoading && Calendar.current.compare(model.calendarMonth, to: firstMonth, toGranularity: .month) == .orderedDescending
        }

        let ranges = await transport.calendarRanges()
        XCTAssertEqual(ranges.count, 2)
        XCTAssertTrue(ranges.allSatisfy { $0.from.hasSuffix("-01") && $0.to >= $0.from })
    }

    func testNextEpisodeResolverCrossesToTheNextNonemptySeason() async throws {
        let currentEpisode = episodeID(1)
        let nextEpisode = episodeID(2)
        let series = try JSONDecoder().decode(Series.self, from: Data("""
        {"id":"11111111-1111-4111-8111-111111111111","mediaType":"series","name":"Series","originalName":"Series","originalLanguage":"en","overview":"","genres":[],"cast":[],"voteAverage":0,"voteCount":0,"seasons":[{"id":"season-3-empty","mediaType":"season","seriesId":"11111111-1111-4111-8111-111111111111","name":"Season 3","overview":"","seasonNumber":3,"episodeCount":0,"voteAverage":0,"externalIds":{}},{"id":"season-2","mediaType":"season","seriesId":"11111111-1111-4111-8111-111111111111","name":"Season 2","overview":"","seasonNumber":2,"episodeCount":1,"voteAverage":0,"externalIds":{}},{"id":"season-1","mediaType":"season","seriesId":"11111111-1111-4111-8111-111111111111","name":"Season 1","overview":"","seasonNumber":1,"episodeCount":1,"voteAverage":0,"externalIds":{}}],"aliases":[],"episodeOrders":[],"mappingProvider":"tmdb","externalIds":{"imdb":"tt1234567"}}
        """.utf8))
        let currentSeason = try JSONDecoder().decode(Season.self, from: Data("""
        {"id":"season-1","mediaType":"season","seriesId":"11111111-1111-4111-8111-111111111111","name":"Season 1","overview":"","seasonNumber":1,"voteAverage":0,"episodes":[\(MediaRegressionTransport.episodeJSON(id: currentEpisode, number: 1))],"externalIds":{}}
        """.utf8))
        let nextSeason = try JSONDecoder().decode(Season.self, from: Data("""
        {"id":"season-2","mediaType":"season","seriesId":"11111111-1111-4111-8111-111111111111","name":"Season 2","overview":"","seasonNumber":2,"voteAverage":0,"episodes":[{"id":"\(nextEpisode.uuidString.lowercased())","mediaType":"episode","seasonId":"season-2","name":"Episode 1","overview":"","seasonNumber":2,"episodeNumber":1,"voteAverage":0,"voteCount":0,"externalIds":{}}],"externalIds":{}}
        """.utf8))

        let resolved = try await RivuneAppModel.resolveNextEpisodeTarget(
            series: series,
            currentSeason: currentSeason,
            currentEpisodeID: currentEpisode,
            source: seriesTarget()
        ) { _ in nextSeason }

        XCTAssertEqual(resolved?.titleId, nextEpisode)
        XCTAssertEqual(resolved?.seasonId, "season-2")
        XCTAssertEqual(resolved?.resourceId, "tt1234567:2:1")
    }

#if os(macOS)
    func testExternalPlayerCandidatesHandleAbsenceAndApplicationPathsWithSpaces() throws {
        let mainBundle = URL(fileURLWithPath: "/Applications/Rivune.app", isDirectory: true)
        XCTAssertTrue(RivuneExternalApplication.rankedCandidates(
            [],
            mainBundleURL: mainBundle,
            mainBundleIdentifier: "io.rivune.app"
        ).isEmpty)

        let vlc = URL(fileURLWithPath: "/Applications/Video Players/VLC.app", isDirectory: true)
        let candidates = RivuneExternalApplication.rankedCandidates(
            [vlc, vlc],
            mainBundleURL: mainBundle,
            mainBundleIdentifier: "io.rivune.app"
        )

        XCTAssertEqual(candidates.count, 1)
        XCTAssertEqual(candidates.first?.name, "VLC")
        XCTAssertEqual(candidates.first?.url.path, "/Applications/Video Players/VLC.app")
    }

    func testHorizontalRailDragSuppressesTheFollowingCardClick() async throws {
        let controller = RivuneHorizontalRailDragController()

        controller.registerDrag(translation: CGSize(width: 40, height: 2))
        XCTAssertTrue(controller.suppressesClicks)

        controller.endDrag(predictedEndTranslation: CGSize(width: 80, height: 2))
        XCTAssertTrue(controller.suppressesClicks)
        try await Task.sleep(nanoseconds: 150_000_000)
        XCTAssertFalse(controller.suppressesClicks)
    }
#endif

    private func makeModel(transport: any HTTPTransport) throws -> (RivuneAppModel, UserDefaults) {
        let suite = "MediaRegressionTests.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defaults.removePersistentDomain(forName: suite)
        let client = try RivuneAPIClient(
            serverURL: URL(string: "https://server.example.test")!,
            transport: transport,
            credentialStore: MediaRegressionCredentialStore()
        )
        let model = RivuneAppModel(
            defaults: defaults,
            updateChecker: MediaRegressionUpdateChecker(),
            applicationVersion: "test",
            client: client,
            serverOrigin: URL(string: "https://server.example.test")!
        )
        defaults.set(suite, forKey: "MediaRegressionTests.suite")
        model.setAutomaticallyShowStreams(false)
        return (model, defaults)
    }

    private func defaultsSuite(_ defaults: UserDefaults) -> String {
        defaults.string(forKey: "MediaRegressionTests.suite") ?? ""
    }

    private func seriesTarget() -> RivuneMediaTarget {
        RivuneMediaTarget(
            id: Self.seriesID.uuidString,
            resourceId: "series-resource",
            mediaType: "series",
            title: "Series",
            titleId: Self.seriesID,
            provider: "tmdb",
            externalId: "42",
            externalIds: ["tmdb": "42"],
            sourceAddonId: nil,
            sourceCatalogId: nil,
            sourceName: nil,
            posterUrl: nil,
            backgroundUrl: nil,
            logoUrl: nil,
            overview: nil,
            releaseInfo: nil,
            released: nil,
            seriesId: nil,
            seasonId: nil,
            seasonNumber: nil,
            episodeNumber: nil,
            runtimeMinutes: nil
        )
    }

    private func movieTarget() -> RivuneMediaTarget {
        RivuneMediaTarget(
            id: Self.movieID.uuidString,
            resourceId: "movie-resource",
            mediaType: "movie",
            title: "Film Name",
            titleId: Self.movieID,
            provider: "tmdb",
            externalId: "84",
            externalIds: ["tmdb": "84"],
            sourceAddonId: nil,
            sourceCatalogId: nil,
            sourceName: nil,
            posterUrl: nil,
            backgroundUrl: nil,
            logoUrl: nil,
            overview: nil,
            releaseInfo: nil,
            released: nil,
            seriesId: nil,
            seasonId: nil,
            seasonNumber: nil,
            episodeNumber: nil,
            runtimeMinutes: nil
        )
    }

    private func decodeEpisode(id: UUID, number: Int) throws -> Episode {
        try JSONDecoder().decode(Episode.self, from: Data(MediaRegressionTransport.episodeJSON(id: id, number: number).utf8))
    }

    private func episodeID(_ number: Int) -> UUID {
        UUID(uuidString: String(format: "22222222-2222-4222-8222-%012d", number))!
    }

    private func waitUntil(_ condition: @escaping @MainActor () -> Bool) async throws {
        for _ in 0..<500 {
            if condition() { return }
            try await Task.sleep(nanoseconds: 2_000_000)
        }
        XCTFail("Timed out waiting for media state")
        throw MediaRegressionTimeout()
    }

    private static let seriesID = UUID(uuidString: "11111111-1111-4111-8111-111111111111")!
    private static let movieID = UUID(uuidString: "33333333-3333-4333-8333-333333333333")!
}

private struct MediaRegressionTimeout: Error {}

private struct MediaRegressionUpdateChecker: RivuneAppleUpdateChecking {
    func check(currentVersion: String) async throws -> RivuneAppleUpdateCheckResult {
        .upToDate(currentVersion: currentVersion, latestVersion: currentVersion)
    }
}

private struct MediaRegressionCredentialStore: CredentialStore {
    func load(for issuer: URL) async throws -> StoredCredentials? {
        StoredCredentials(
            tokens: TokenPair(
                tokenType: "Bearer",
                accessToken: "access",
                accessTokenExpiresAt: "2099-01-01T01:00:00Z",
                refreshToken: "refresh",
                refreshTokenExpiresAt: "2099-02-01T00:00:00Z",
                sessionId: UUID(uuidString: "44444444-4444-4444-8444-444444444444")!,
                deviceId: UUID(uuidString: "55555555-5555-4555-8555-555555555555")!,
                authorizationScope: .globalAdministrator,
                category: nil
            ),
            profileContext: nil
        )
    }

    func save(_ credentials: StoredCredentials, for issuer: URL) async throws {}
    func clear(for issuer: URL) async throws {}
}

private actor MediaRegressionTransport: HTTPTransport {
    private struct ProgressBatchRequest: Decodable { let titleIds: [UUID] }
    private struct WatchedBatchRequest: Decodable { let items: [SetWatchedBatchItem] }

    private let episodeIDs: [UUID]
    private let initiallyCompleted: Set<UUID>
    private let failingWatchedBatch: Int?
    private let streamURL: String
    private var recordedProgressBatchSizes: [Int] = []
    private var recordedWatchedBatchSizes: [Int] = []

    init(
        episodeIDs: [UUID],
        initiallyCompleted: Set<UUID> = [],
        failingWatchedBatch: Int? = nil,
        streamURL: String = "https://cdn.example.test/video.mp4"
    ) {
        self.episodeIDs = episodeIDs
        self.initiallyCompleted = initiallyCompleted
        self.failingWatchedBatch = failingWatchedBatch
        self.streamURL = streamURL
    }

    func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        let path = request.url?.path ?? ""
        if path == "/.well-known/rivune" {
            return response(request, body: """
            {"name":"Rivune","serverVersion":"test","protocolVersion":20,"apiBaseUrl":"/api/v1","setupRequired":false,"setupCompleted":true,"demoAvailable":false,"timezone":"UTC","interfaceLanguage":"en-US"}
            """)
        }
        if path.contains("/metadata/series/") {
            return response(request, body: seriesJSON())
        }
        if path.contains("/metadata/seasons/") {
            return response(request, body: seasonJSON())
        }
        if path.contains("/metadata/titles/") && path.hasSuffix("/trailers") {
            return response(request, body: "{\"trailers\":[]}")
        }
        if path.contains("/metadata/titles/") {
            return response(request, body: movieJSON())
        }
        if path.hasSuffix("/library") {
            return response(request, body: "{\"items\":[],\"page\":1,\"totalPages\":0,\"totalResults\":0}")
        }
        if path.hasSuffix("/progress/batch") {
            let body = try XCTUnwrap(request.httpBody)
            let input = try JSONDecoder().decode(ProgressBatchRequest.self, from: body)
            recordedProgressBatchSizes.append(input.titleIds.count)
            let items = input.titleIds.map { id in
                "{\"titleId\":\"\(id.uuidString.lowercased())\",\"progress\":\(progressJSON(id: id, completed: initiallyCompleted.contains(id), version: initiallyCompleted.contains(id) ? 1 : 0))}"
            }.joined(separator: ",")
            return response(request, body: "{\"items\":[\(items)]}")
        }
        if path.contains("/progress/") && request.httpMethod == "GET" {
            guard let id = UUID(uuidString: path.split(separator: "/").last.map(String.init) ?? ""), episodeIDs.contains(id) else {
                return response(request, status: 204, body: "")
            }
            return response(request, body: progressJSON(id: id, completed: initiallyCompleted.contains(id), version: initiallyCompleted.contains(id) ? 1 : 0))
        }
        if path.hasSuffix("/titles/watched/batch") {
            let body = try XCTUnwrap(request.httpBody)
            let input = try JSONDecoder().decode(WatchedBatchRequest.self, from: body)
            recordedWatchedBatchSizes.append(input.items.count)
            if recordedWatchedBatchSizes.count == failingWatchedBatch {
                return response(request, status: 409, body: "{\"error\":{\"code\":\"conflict\",\"message\":\"Progress version conflict.\"}}")
            }
            let items = input.items.map { item in
                "{\"titleId\":\"\(item.titleId.uuidString.lowercased())\",\"progress\":\(progressJSON(id: item.titleId, completed: item.completed, version: item.expectedVersion + 1))}"
            }.joined(separator: ",")
            return response(request, body: "{\"items\":[\(items)]}")
        }
        if path.hasSuffix("/playback/sources") {
            return response(request, body: """
            {"sources":[{"id":"source-1","sourceRef":"source-ref","addonId":"66666666-6666-4666-8666-666666666666","manifestId":"manifest","streamIndex":0,"name":"Direct","protocol":"http","mode":"direct","container":"mp4","expiresAt":"2099-01-01T00:00:00Z","stableIdentity":"stable"}],"providerErrors":[]}
            """)
        }
        if path.hasSuffix("/playback/prepare") {
            return response(request, body: "{\"sourceRef\":\"source-ref\",\"mode\":\"direct\",\"protocol\":\"http\",\"container\":\"mp4\",\"subtitleCount\":0,\"expiresAt\":\"2099-01-01T00:00:00Z\"}")
        }
        if path.hasSuffix("/playback/resolve") {
            return response(request, status: 201, body: """
            {"id":"77777777-7777-4777-8777-777777777777","selectedSourceId":"resolved-1","sources":[{"id":"resolved-1","addonId":"66666666-6666-4666-8666-666666666666","manifestId":"manifest","mode":"direct","url":"\(streamURL)","protocol":"http","container":"mp4","compatible":true}],"subtitles":[],"providerErrors":[],"expiresAt":"2099-01-01T00:00:00Z"}
            """)
        }
        if path.contains("/playback/sessions/") && request.httpMethod == "DELETE" {
            return response(request, status: 204, body: "")
        }
        return response(request, status: 404, body: "{\"error\":{\"code\":\"not_found\",\"message\":\"Unexpected test route.\"}}")
    }

    func progressBatchSizes() -> [Int] { recordedProgressBatchSizes }
    func watchedBatchSizes() -> [Int] { recordedWatchedBatchSizes }

    private func seriesJSON() -> String {
        let seasons = episodeIDs.isEmpty ? "[]" : "[{\"id\":\"season-1\",\"mediaType\":\"season\",\"seriesId\":\"11111111-1111-4111-8111-111111111111\",\"name\":\"Season 1\",\"overview\":\"\",\"seasonNumber\":1,\"episodeCount\":\(episodeIDs.count),\"voteAverage\":0,\"externalIds\":{}}]"
        return "{\"id\":\"11111111-1111-4111-8111-111111111111\",\"mediaType\":\"series\",\"name\":\"Series\",\"originalName\":\"Series\",\"originalLanguage\":\"en\",\"overview\":\"\",\"genres\":[],\"cast\":[],\"voteAverage\":0,\"voteCount\":0,\"seasons\":\(seasons),\"aliases\":[],\"episodeOrders\":[],\"mappingProvider\":\"tmdb\",\"externalIds\":{\"imdb\":\"tt1234567\"}}"
    }

    private func seasonJSON() -> String {
        let episodes = episodeIDs.enumerated().map { index, id in
            Self.episodeJSON(id: id, number: index + 1)
        }.joined(separator: ",")
        return "{\"id\":\"season-1\",\"mediaType\":\"season\",\"seriesId\":\"11111111-1111-4111-8111-111111111111\",\"name\":\"Season 1\",\"overview\":\"\",\"seasonNumber\":1,\"voteAverage\":0,\"episodes\":[\(episodes)],\"externalIds\":{}}"
    }

    nonisolated static func episodeJSON(id: UUID, number: Int) -> String {
        "{\"id\":\"\(id.uuidString.lowercased())\",\"mediaType\":\"episode\",\"seasonId\":\"season-1\",\"name\":\"Episode \(number)\",\"overview\":\"\",\"seasonNumber\":1,\"episodeNumber\":\(number),\"voteAverage\":0,\"voteCount\":0,\"externalIds\":{}}"
    }

    private func movieJSON() -> String {
        "{\"id\":\"33333333-3333-4333-8333-333333333333\",\"mediaType\":\"movie\",\"title\":\"Film Name\",\"originalTitle\":\"Film Name\",\"originalLanguage\":\"en\",\"overview\":\"\",\"genres\":[],\"cast\":[],\"voteAverage\":0,\"voteCount\":0,\"externalIds\":{}}"
    }

    private func progressJSON(id: UUID, completed: Bool, version: Int64) -> String {
        "{\"titleId\":\"\(id.uuidString.lowercased())\",\"mediaType\":\"episode\",\"positionSeconds\":0,\"durationSeconds\":1800,\"completed\":\(completed),\"version\":\(version),\"lastWatchedAt\":\"2099-01-01T00:00:00Z\",\"updatedAt\":\"2099-01-01T00:00:00Z\"}"
    }

    private func response(_ request: URLRequest, status: Int = 200, body: String) -> (Data, HTTPURLResponse) {
        (
            Data(body.utf8),
            HTTPURLResponse(
                url: request.url!,
                statusCode: status,
                httpVersion: nil,
                headerFields: ["Content-Type": "application/json"]
            )!
        )
    }
}

private actor NavigationParityTransport: HTTPTransport {
    private var recordedSearchSkips: [Int] = []
    private var recordedLibraryMediaTypes: [String?] = []
    private var recordedCalendarRanges: [(from: String, to: String)] = []

    func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        let path = request.url?.path ?? ""
        let query = Dictionary(uniqueKeysWithValues: (URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.queryItems ?? []).map { ($0.name, $0.value ?? "") })
        if path == "/.well-known/rivune" {
            return response(request, body: """
            {"name":"Rivune","serverVersion":"test","protocolVersion":20,"apiBaseUrl":"/api/v1","setupRequired":false,"setupCompleted":true,"demoAvailable":false,"timezone":"UTC","interfaceLanguage":"en-US"}
            """)
        }
        if path.hasSuffix("/addons/catalogs") {
            return response(request, body: """
            {"catalogs":[{"addonId":"66666666-6666-4666-8666-666666666666","manifestId":"manifest","position":0,"catalog":{"type":"movie","id":"search","extra":[{"name":"search"}]},"addonCatalog":true,"searchable":true}]}
            """)
        }
        if path.contains("/addons/catalogs/search/") {
            let skip = Int(query["skip"] ?? "0") ?? 0
            recordedSearchSkips.append(skip)
            let count = skip == 0 ? 50 : 1
            let metas = (0..<count).map { index in
                let number = skip + index
                return "{\"id\":\"movie-\(number)\",\"type\":\"movie\",\"name\":\"Movie \(number)\"}"
            }.joined(separator: ",")
            return response(request, body: """
            {"results":[{"addonId":"66666666-6666-4666-8666-666666666666","manifestId":"manifest","resource":"catalog","type":"movie","id":"search","payload":{"metas":[\(metas)]},"cache":{}}],"errors":[]}
            """)
        }
        if path.hasSuffix("/library") {
            let mediaType = query["mediaType"]
            recordedLibraryMediaTypes.append(mediaType)
            let page = Int(query["page"] ?? "1") ?? 1
            let totalPages = mediaType == nil ? 2 : 1
            let id = page == 1
                ? "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1"
                : "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa2"
            return response(request, body: """
            {"items":[{"titleId":"\(id)","mediaType":"\(mediaType ?? "movie")","title":"Library \(page)","available":true,"addedAt":"2099-01-01T00:00:00Z","updatedAt":"2099-01-01T00:00:00Z"}],"page":\(page),"totalPages":\(totalPages),"totalResults":\(totalPages)}
            """)
        }
        if path.hasSuffix("/calendar") {
            recordedCalendarRanges.append((query["from"] ?? "", query["to"] ?? ""))
            return response(request, body: "{\"events\":[]}")
        }
        return response(request, status: 404, body: "{\"error\":{\"code\":\"not_found\",\"message\":\"Unexpected test route.\"}}")
    }

    func searchSkips() -> [Int] { recordedSearchSkips }
    func libraryMediaTypes() -> [String?] { recordedLibraryMediaTypes }
    func calendarRanges() -> [(from: String, to: String)] { recordedCalendarRanges }

    private func response(_ request: URLRequest, status: Int = 200, body: String) -> (Data, HTTPURLResponse) {
        (
            Data(body.utf8),
            HTTPURLResponse(
                url: request.url!,
                statusCode: status,
                httpVersion: nil,
                headerFields: ["Content-Type": "application/json"]
            )!
        )
    }
}
