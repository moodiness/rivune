import Foundation
import RivuneAPI
import XCTest

@testable import RivuneAppCore

#if canImport(FoundationNetworking)
  import FoundationNetworking
#endif

@MainActor
final class MediaRegressionTests: XCTestCase {
  func testEachPlatformOwnsItsNavigationShell() {
    XCTAssertEqual(rivuneNavigationPresentation(for: .touch), .stack)
    XCTAssertEqual(rivuneNavigationPresentation(for: .desktop), .desktop)
    XCTAssertEqual(rivuneNavigationPresentation(for: .television), .stack)
    XCTAssertEqual(rivuneNavigationPresentation(for: .spatial), .stack)
  }

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
    let streamURL =
      "https://cdn.example.test/Video%20Library/Film%20Name.mp4?token=a%2Bb%3D%3D&quality=original"
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
    XCTAssertEqual(
      components.queryItems,
      [
        URLQueryItem(name: "token", value: "a+b=="),
        URLQueryItem(name: "quality", value: "original"),
      ])
  }

  func testResolvedMediaDurationSeedsPlaybackTimelineWithoutExistingProgress() async throws {
    let transport = MediaRegressionTransport(episodeIDs: [], resolvedDurationSeconds: 2_700)
    let (model, defaults) = try makeModel(transport: transport)
    defer { defaults.removePersistentDomain(forName: defaultsSuite(defaults)) }

    model.openMedia(movieTarget())
    try await waitUntil { model.mediaDetail?.target.mediaType == "movie" && !model.mediaLoading }
    model.loadPlaybackSources()
    try await waitUntil { !model.mediaLoading && model.playbackSources.count == 1 }
    model.play(try XCTUnwrap(model.playbackSources.first), externally: false)
    try await waitUntil { model.playbackPresentation != nil }

    XCTAssertEqual(model.playbackPresentation?.durationSeconds, 2_700)
  }

  func testMinimizeAndRestorePreserveSessionAspectAndSpeed() async throws {
    let transport = MediaRegressionTransport(episodeIDs: [], resolvedDurationSeconds: 2_700)
    let (model, defaults) = try makeModel(transport: transport)
    defer { defaults.removePersistentDomain(forName: defaultsSuite(defaults)) }

    model.openMedia(movieTarget())
    try await waitUntil { model.mediaDetail?.target.mediaType == "movie" && !model.mediaLoading }
    model.loadPlaybackSources()
    try await waitUntil { !model.mediaLoading && model.playbackSources.count == 1 }
    model.play(try XCTUnwrap(model.playbackSources.first), externally: false)
    try await waitUntil { model.playbackPresentation != nil }

    model.updatePlaybackSession(videoAspect: .zoom, playbackSpeed: 1.5)
    model.minimizePlayback(position: 37, duration: 2_700)

    XCTAssertNil(model.playbackPresentation)
    XCTAssertEqual(model.minimizedPlaybackPresentation?.videoAspect, .zoom)
    XCTAssertEqual(model.minimizedPlaybackPresentation?.playbackSpeed, 1.5)

    model.resumeMinimizedPlayback(position: 42, duration: 2_700)

    XCTAssertNil(model.minimizedPlaybackPresentation)
    XCTAssertEqual(model.playbackPresentation?.startSeconds, 42)
    XCTAssertEqual(model.playbackPresentation?.videoAspect, .zoom)
    XCTAssertEqual(model.playbackPresentation?.playbackSpeed, 1.5)
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

    let playbackResourceIDs = await transport.playbackResourceIDs()
    let markerRequestCount = await transport.markerRequestCount()
    XCTAssertEqual(model.mediaDetail?.titleId, episodeIDs[1])
    XCTAssertNil(model.playbackPresentation?.nextEpisode)
    XCTAssertEqual(playbackResourceIDs, ["tt1234567:1:1", "tt1234567:1:2"])
    XCTAssertEqual(markerRequestCount, 2)
  }

  func testTvdbContinuationRetainsOrderAcrossDetailPlaybackAndNextEpisode() async throws {
    let currentEpisode = episodeID(1)
    let nextEpisode = episodeID(2)
    let persistedSeasonID = UUID(uuidString: "77777777-7777-4777-8777-777777777777")!
    let metadataSeasonID = "tvdb:\(Self.seriesID.uuidString.lowercased()):2112814"
    let transport = MediaRegressionTransport(
      episodeIDs: [currentEpisode, nextEpisode],
      episodeOrderID: "2",
      metadataSeasonID: metadataSeasonID
    )
    let (model, defaults) = try makeModel(transport: transport)
    defer { defaults.removePersistentDomain(forName: defaultsSuite(defaults)) }
    let item = try JSONDecoder().decode(
      ContinueWatchingItem.self,
      from: Data(
        """
        {"titleId":"\(currentEpisode.uuidString.lowercased())","mediaType":"episode","seriesId":"\(Self.seriesID.uuidString.lowercased())","seasonId":"\(persistedSeasonID.uuidString.lowercased())","seasonNumber":1,"episodeNumber":1,"mappingProvider":" TVDB ","episodeOrderId":" 2 ","metadataSeasonId":" \(metadataSeasonID) ","title":"Variant Series","resourceId":"tvdb:10357450","resourceProvider":"tvdb","episodeTitle":"DVD Episode 1","positionSeconds":120,"durationSeconds":1800,"version":3,"reason":"resume","lastWatchedAt":"2026-09-04T12:00:00Z"}
        """.utf8))

    model.openMedia(item)
    try await waitUntil { model.mediaDetail?.titleId == currentEpisode && !model.mediaLoading }

    let target = try XCTUnwrap(model.mediaDetail?.target)
    XCTAssertEqual(target.resourceId, "tvdb:10357450")
    XCTAssertEqual(target.mappingProvider, .tvdb)
    XCTAssertEqual(target.episodeOrderId, "2")
    XCTAssertEqual(target.metadataSeasonId, metadataSeasonID)
    XCTAssertEqual(target.seasonId, persistedSeasonID.uuidString)
    let seriesQueries = await transport.seriesQueries()
    let seasonIDs = await transport.seasonIDs()
    XCTAssertEqual(
      seriesQueries,
      [["mappingProvider": "tvdb", "episodeOrder": "2"]])
    XCTAssertEqual(seasonIDs, [metadataSeasonID])

    model.loadPlaybackSources()
    try await waitUntil { model.playbackSources.count == 1 && !model.mediaLoading }
    let playbackResourceIDs = await transport.playbackResourceIDs()
    XCTAssertEqual(playbackResourceIDs, ["tvdb:10357450"])
    model.play(try XCTUnwrap(model.playbackSources.first), externally: false)
    try await waitUntil { model.playbackPresentation?.titleId == currentEpisode }

    let markerRequestCount = await transport.markerRequestCount()
    XCTAssertEqual(markerRequestCount, 0)
    let next = try XCTUnwrap(model.playbackPresentation?.nextEpisode)
    XCTAssertEqual(next.titleId, nextEpisode)
    XCTAssertEqual(next.resourceId, "tvdb:10357451")
    XCTAssertEqual(next.mappingProvider, .tvdb)
    XCTAssertEqual(next.episodeOrderId, "2")
    XCTAssertEqual(next.metadataSeasonId, metadataSeasonID)
    XCTAssertEqual(next.seasonId, persistedSeasonID.uuidString)
  }

  func testInvalidContinuationContextUsesCanonicalHierarchyAndMarkers() async throws {
    let currentEpisode = episodeID(1)
    let persistedSeasonID = UUID(uuidString: "88888888-8888-4888-8888-888888888888")!
    let opaqueSeasonID = "tvdb:\(Self.seriesID.uuidString.lowercased()):2112814"
    let transport = MediaRegressionTransport(episodeIDs: [currentEpisode])
    let (model, defaults) = try makeModel(transport: transport)
    defer { defaults.removePersistentDomain(forName: defaultsSuite(defaults)) }
    let item = try JSONDecoder().decode(
      ContinueWatchingItem.self,
      from: Data(
        """
        {"titleId":"\(currentEpisode.uuidString.lowercased())","mediaType":"episode","seriesId":"\(Self.seriesID.uuidString.lowercased())","seasonId":"\(persistedSeasonID.uuidString.lowercased())","seasonNumber":1,"episodeNumber":1,"mappingProvider":"unknown","episodeOrderId":"2","metadataSeasonId":"\(opaqueSeasonID)","title":"Canonical Series","resourceId":"tt1234567:1:1","resourceProvider":"imdb","episodeTitle":"Episode 1","positionSeconds":120,"durationSeconds":1800,"version":3,"reason":"resume","lastWatchedAt":"2026-09-04T12:00:00Z"}
        """.utf8))

    model.openMedia(item)
    try await waitUntil { model.mediaDetail?.titleId == currentEpisode && !model.mediaLoading }

    let target = try XCTUnwrap(model.mediaDetail?.target)
    XCTAssertNil(target.mappingProvider)
    XCTAssertNil(target.episodeOrderId)
    XCTAssertNil(target.metadataSeasonId)
    let seriesQueries = await transport.seriesQueries()
    let seasonIDs = await transport.seasonIDs()
    XCTAssertEqual(seriesQueries, [["mappingProvider": "tmdb"]])
    XCTAssertEqual(seasonIDs, ["season-1"])

    model.loadPlaybackSources()
    try await waitUntil { model.playbackSources.count == 1 && !model.mediaLoading }
    model.play(try XCTUnwrap(model.playbackSources.first), externally: false)
    try await waitUntil { model.playbackPresentation?.titleId == currentEpisode }
    let markerRequestCount = await transport.markerRequestCount()
    XCTAssertEqual(markerRequestCount, 1)
  }

  func testSelectedOfficialOrderClearsStaleContextAndKeepsCanonicalIdentity() throws {
    let episode = try JSONDecoder().decode(
      Episode.self,
      from: Data(
        """
        {"id":"\(episodeID(1).uuidString.lowercased())","mediaType":"episode","seasonId":"canonical-season-1","name":"Episode 1","overview":"","seasonNumber":1,"episodeNumber":1,"voteAverage":0,"voteCount":0,"externalIds":{"tvdb":"10357450"}}
        """.utf8))
    let series = try JSONDecoder().decode(
      Series.self,
      from: Data(
        """
        {"id":"\(Self.seriesID.uuidString.lowercased())","mediaType":"series","name":"Series","originalName":"Series","originalLanguage":"en","overview":"","genres":[],"cast":[],"voteAverage":0,"voteCount":0,"seasons":[],"aliases":[],"episodeOrders":[{"id":"official","name":"Official","type":"official","isDefault":true}],"selectedEpisodeOrderId":"official","mappingProvider":"tmdb","externalIds":{"imdb":"tt1234567"}}
        """.utf8))
    let stale = RivuneMediaTarget(
      id: "tvdb:10357450", resourceId: "tvdb:10357450", mediaType: "episode",
      title: "Episode 1", titleId: episode.id, provider: "tvdb", externalId: "10357450",
      externalIds: ["tvdb": "10357450"], sourceAddonId: nil, sourceCatalogId: nil,
      sourceName: nil, posterUrl: nil, backgroundUrl: nil, logoUrl: nil, overview: nil,
      releaseInfo: nil, released: nil, seriesId: Self.seriesID, mappingProvider: .tvdb,
      episodeOrderId: "2", metadataSeasonId: "tvdb:stale:2112814",
      seasonId: UUID().uuidString, seasonNumber: 1, episodeNumber: 1, runtimeMinutes: nil)

    let target = RivuneAppModel.episodeTarget(episode, series: series, source: stale)

    XCTAssertEqual(target.resourceId, "tt1234567:1:1")
    XCTAssertNil(target.mappingProvider)
    XCTAssertNil(target.episodeOrderId)
    XCTAssertNil(target.metadataSeasonId)
    XCTAssertEqual(target.seasonId, "canonical-season-1")
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

  func testSearchTypesComeFromSearchableCatalogsAndFilterRequests() async throws {
    let transport = NavigationParityTransport(searchTypes: [
      "tv", "other", "series", "anime", "movie",
    ])
    let (model, defaults) = try makeModel(transport: transport)
    defer { defaults.removePersistentDomain(forName: defaultsSuite(defaults)) }

    model.selectTab(.search)
    try await waitUntil { model.searchMediaTypes.count == 5 && !model.tabLoading }
    XCTAssertEqual(model.searchMediaTypes, ["movie", "series", "anime", "tv", "other"])

    model.searchQuery = "space opera"
    model.setSearchMediaType("anime")
    try await waitUntil { model.searchItems.count == 50 && !model.tabLoading }

    XCTAssertEqual(model.searchMediaType, "anime")
    XCTAssertEqual(Set(model.searchItems.map(\.mediaType)), ["anime"])
    let requestedTypes = await transport.searchTypes()
    XCTAssertEqual(requestedTypes, ["anime"])
  }

  func testSearchFanoutCapsTypesAndConcurrentRequests() async throws {
    let types = (0..<24).map { String(format: "type-%02d", $0) }
    let delays = Dictionary(uniqueKeysWithValues: types.map { ($0, UInt64(40_000_000)) })
    let transport = NavigationParityTransport(
      searchTypes: types + ["type-00", " TYPE-01 "],
      addonDelayNanosecondsByType: delays)
    let (model, defaults) = try makeModel(transport: transport)
    defer { defaults.removePersistentDomain(forName: defaultsSuite(defaults)) }

    model.selectTab(.search)
    try await waitUntil { model.searchMediaTypes.count == 24 && !model.tabLoading }
    model.searchQuery = "bounded fanout"
    model.search()
    try await waitUntil { !model.tabLoading }

    let requestedTypes = await transport.searchTypes()
    let maximumConcurrency = await transport.maximumConcurrentSearchRequests()
    XCTAssertEqual(requestedTypes.count, 16)
    XCTAssertLessThanOrEqual(maximumConcurrency, 4)
    XCTAssertTrue(model.searchPartial)
  }

  func testSemanticRefinementSharesSixteenRequestBudget() async throws {
    let types = (0..<24).map { String(format: "type-%02d", $0) }
    let delays = Dictionary(uniqueKeysWithValues: types.map { ($0, UInt64(20_000_000)) })
    let transport = NavigationParityTransport(
      searchTypes: types,
      semanticSearch: true,
      semanticDelayNanoseconds: 10_000_000,
      semanticMediaTypes: ["unconfigured"],
      addonDelayNanosecondsByType: delays)
    let (model, defaults) = try makeModel(
      transport: transport, semanticSearchAvailable: true)
    defer { defaults.removePersistentDomain(forName: defaultsSuite(defaults)) }

    model.selectTab(.search)
    try await waitUntil { model.searchMediaTypes.count == 24 && !model.tabLoading }
    model.searchQuery = "semantic budget"
    model.search()
    try await waitUntil { !model.tabLoading }

    let requests = await transport.searchTypes()
    XCTAssertEqual(requests.count, 16)
    XCTAssertTrue(model.searchPartial)
  }

  func testSemanticSearchPrioritizesDirectTitlesDeduplicatesAndRemovesIntents() async throws {
    let transport = NavigationParityTransport(
      searchTypes: ["movie", "series"],
      semanticSearch: true,
      semanticDelayNanoseconds: 50_000_000
    )
    let (model, defaults) = try makeModel(
      transport: transport,
      semanticSearchAvailable: true
    )
    defer { defaults.removePersistentDomain(forName: defaultsSuite(defaults)) }

    model.searchQuery = "film Dune de guerre"
    model.search()
    try await waitUntil { !model.searchItems.isEmpty && !model.tabLoading }

    XCTAssertEqual(model.searchIntents.map(\.id), ["media_type:movie", "genre:war"])
    XCTAssertEqual(model.searchItems.first?.title, "Direct title")
    XCTAssertFalse(model.searchItems.contains { $0.title == "Semantic duplicate" })
    XCTAssertEqual(model.searchItems.last?.title, "Semantic unique")
    XCTAssertTrue(model.searchHasMore)
    XCTAssertFalse(model.searchPartial)
    let searchTerms = await transport.searchTerms()
    let searchTypes = await transport.searchTypes()
    XCTAssertEqual(searchTerms.last, "Dune")
    XCTAssertEqual(searchTypes.last, "movie")
    XCTAssertEqual(Set(model.searchItems.map(\.mediaType)), ["movie", "series"])

    model.removeSearchIntent(id: "genre:war")
    try await waitUntil {
      model.searchIntents.map(\.id) == ["media_type:movie"] && !model.tabLoading
    }

    let exclusions = await transport.semanticExclusions()
    XCTAssertEqual(exclusions, [[], ["genre:war"]])
  }

  func testSearchPublishesFirstAddonBatchWhileOtherSourcesAndSemanticRemainLoading() async throws {
    let transport = NavigationParityTransport(
      searchTypes: ["movie", "series"],
      semanticSearch: true,
      semanticDelayNanoseconds: 180_000_000,
      addonDelayNanosecondsByType: ["movie": 180_000_000]
    )
    let (model, defaults) = try makeModel(
      transport: transport,
      semanticSearchAvailable: true,
      semanticSearchTimeoutNanoseconds: 1_000_000_000
    )
    defer { defaults.removePersistentDomain(forName: defaultsSuite(defaults)) }

    model.searchQuery = "film Dune de guerre"
    model.search()
    try await waitUntil { !model.searchItems.isEmpty }

    XCTAssertTrue(model.tabLoading)
    XCTAssertEqual(model.searchItems.first?.mediaType, "series")

    try await waitUntil { !model.tabLoading }
    XCTAssertFalse(model.searchItems.isEmpty)
    XCTAssertFalse(model.tabLoading)
  }

  func testSemanticTypeWithoutConfiguredMatchFallsBackToAllAddonTypes() async throws {
    let transport = NavigationParityTransport(
      searchTypes: ["series"],
      semanticSearch: true,
      semanticMediaTypes: ["anime"],
      addonDelayNanosecondsByType: ["series": 120_000_000]
    )
    let (model, defaults) = try makeModel(
      transport: transport,
      semanticSearchAvailable: true
    )
    defer { defaults.removePersistentDomain(forName: defaultsSuite(defaults)) }

    model.searchQuery = "unmatched semantic type"
    model.search()
    try await waitUntil { !model.tabLoading }

    XCTAssertTrue(model.searchItems.contains { $0.mediaType == "series" })
    let searchedTypes = await transport.searchTypes()
    XCTAssertEqual(searchedTypes.last, "series")
  }

  func testSemanticFirstDuplicateKeepsPublishedRepresentativePositionAndKey() async throws {
    let transport = NavigationParityTransport(
      searchTypes: ["movie"],
      semanticSearch: true,
      addonDelayNanosecondsByType: ["movie": 120_000_000]
    )
    let (model, defaults) = try makeModel(
      transport: transport,
      semanticSearchAvailable: true
    )
    defer { defaults.removePersistentDomain(forName: defaultsSuite(defaults)) }

    model.searchQuery = "film Dune de guerre"
    model.search()
    try await waitUntil { model.searchItems.first?.title == "Semantic duplicate" }
    let publishedID = try XCTUnwrap(model.searchItems.first?.id)
    let publishedKey = try XCTUnwrap(model.searchItems.first?.searchPresentationID)
    XCTAssertTrue(model.tabLoading)

    try await waitUntil { !model.tabLoading }

    XCTAssertEqual(model.searchItems.first?.id, publishedID)
    XCTAssertEqual(model.searchItems.first?.searchPresentationID, publishedKey)
    XCTAssertEqual(model.searchItems.first?.title, "Semantic duplicate")
    XCTAssertFalse(model.searchItems.contains { $0.title == "Direct title" })
  }

  func testCancelledSearchGenerationCannotPublishStaleResults() async throws {
    let transport = NavigationParityTransport(
      searchDelayNanosecondsByTerm: ["old query": 180_000_000],
      ignoresSearchCancellation: true,
      includesSearchTermInResults: true
    )
    let (model, defaults) = try makeModel(transport: transport)
    defer { defaults.removePersistentDomain(forName: defaultsSuite(defaults)) }

    model.searchQuery = "old query"
    model.search()
    try await waitUntil { model.tabLoading }
    try await Task.sleep(nanoseconds: 20_000_000)
    let startedTerms = await transport.searchTerms()
    XCTAssertTrue(startedTerms.contains("old query"))

    model.searchQuery = "new query"
    model.search()
    try await waitUntil { !model.tabLoading && !model.searchItems.isEmpty }
    let terminalIDs = model.searchItems.map(\.id)
    XCTAssertTrue(terminalIDs.allSatisfy { $0.hasPrefix("new-query-") })

    try await Task.sleep(nanoseconds: 250_000_000)
    XCTAssertEqual(model.searchItems.map(\.id), terminalIDs)
    XCTAssertFalse(model.tabLoading)
    let report = model.diagnosticReport(generatedAtMilliseconds: 1)
    let started = report.split(separator: "\n").filter { $0.contains("SEARCH_STARTED") }
    let terminals = report.split(separator: "\n").filter {
      $0.contains("SEARCH_SUCCEEDED") || $0.contains("SEARCH_PARTIAL")
        || $0.contains("SEARCH_FAILED") || $0.contains("SEARCH_CANCELED")
    }
    XCTAssertEqual(started.count, 2)
    XCTAssertEqual(terminals.count, 2)
    let startedOperations = Set(started.compactMap { $0.split(separator: "operation=").last.map(String.init) })
    let terminalOperations = Set(terminals.compactMap { $0.split(separator: "operation=").last.map(String.init) })
    XCTAssertEqual(startedOperations, terminalOperations)
    XCTAssertFalse(report.contains("old query"))
    XCTAssertFalse(report.contains("new query"))
  }

  func testLeavingSearchRecordsOneCanceledTerminal() async throws {
    let transport = NavigationParityTransport(
      searchDelayNanosecondsByTerm: ["leaving query": 500_000_000])
    let (model, defaults) = try makeModel(transport: transport)
    defer { defaults.removePersistentDomain(forName: defaultsSuite(defaults)) }

    model.searchQuery = "leaving query"
    model.selectTab(.search)
    model.search()
    try await waitUntil { model.tabLoading }
    model.selectTab(.home)
    try await Task.sleep(nanoseconds: 20_000_000)

    let report = model.diagnosticReport(generatedAtMilliseconds: 1)
    XCTAssertEqual(report.components(separatedBy: "SEARCH_STARTED").count - 1, 1)
    XCTAssertEqual(report.components(separatedBy: "SEARCH_CANCELED").count - 1, 1)
    XCTAssertFalse(report.contains("leaving query"))
  }

  func testSemanticSearchTimeoutFallsBackToOriginalAddonSearch() async throws {
    let query = "slow semantic search"
    let transport = NavigationParityTransport(
      searchTypes: ["movie", "series"],
      semanticSearch: true,
      semanticDelayNanoseconds: 10_000_000_000
    )
    let (model, defaults) = try makeModel(
      transport: transport,
      semanticSearchAvailable: true,
      semanticSearchTimeoutNanoseconds: 500_000_000
    )
    defer { defaults.removePersistentDomain(forName: defaultsSuite(defaults)) }

    model.searchQuery = query
    model.search()
    try await waitUntil { !model.searchItems.isEmpty && model.tabLoading }
    XCTAssertTrue(model.tabLoading)
    try await waitUntil { !model.tabLoading }

    XCTAssertEqual(Set(model.searchItems.map(\.mediaType)), ["movie", "series"])
    let searchTerms = await transport.searchTerms()
    let searchTypes = await transport.searchTypes()
    XCTAssertEqual(Set(searchTerms), [query])
    XCTAssertEqual(Set(searchTypes), ["movie", "series"])
    XCTAssertTrue(model.searchIntents.isEmpty)
    XCTAssertTrue(model.searchPartial)
    XCTAssertNil(model.tabFailure)
  }

  func testSearchIdentityMatchesAcrossAddonAndSemanticProviderIDs() {
    let addonItem = searchTarget(id: "tt1234567", externalIds: [:])
    let semanticItem = searchTarget(
      id: "tmdb:42",
      externalIds: ["tmdb": "42", "imdb": "tt1234567"]
    )
    let unrelatedItem = searchTarget(id: "tmdb:43", externalIds: ["tmdb": "43"])

    XCTAssertFalse(
      RivuneAppModel.searchIdentities(addonItem).isDisjoint(
        with: RivuneAppModel.searchIdentities(semanticItem)))
    XCTAssertTrue(
      RivuneAppModel.searchIdentities(addonItem).isDisjoint(
        with: RivuneAppModel.searchIdentities(unrelatedItem)))
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
    try await waitUntil {
      model.libraryMediaType == .movie && model.libraryPage == 1 && !model.tabLoading
    }
    XCTAssertEqual(model.libraryItems.count, 1)
    let libraryTypes = await transport.libraryMediaTypes()
    XCTAssertEqual(libraryTypes, [nil, nil, "movie"])

    model.selectTab(.calendar)
    try await waitUntil { !model.tabLoading && model.selectedTab == .calendar }
    let firstMonth = model.calendarMonth
    model.nextCalendarMonth()
    try await waitUntil {
      !model.tabLoading
        && Calendar.current.compare(model.calendarMonth, to: firstMonth, toGranularity: .month)
          == .orderedDescending
    }

    let ranges = await transport.calendarRanges()
    XCTAssertEqual(ranges.count, 2)
    XCTAssertTrue(ranges.allSatisfy { $0.from.hasSuffix("-01") && $0.to >= $0.from })
  }

  func testNextEpisodeResolverCrossesToTheNextNonemptySeason() async throws {
    let currentEpisode = episodeID(1)
    let nextEpisode = episodeID(2)
    let series = try JSONDecoder().decode(
      Series.self,
      from: Data(
        """
        {"id":"11111111-1111-4111-8111-111111111111","mediaType":"series","name":"Series","originalName":"Series","originalLanguage":"en","overview":"","genres":[],"cast":[],"voteAverage":0,"voteCount":0,"seasons":[{"id":"season-3-empty","mediaType":"season","seriesId":"11111111-1111-4111-8111-111111111111","name":"Season 3","overview":"","seasonNumber":3,"episodeCount":0,"voteAverage":0,"externalIds":{}},{"id":"season-2","mediaType":"season","seriesId":"11111111-1111-4111-8111-111111111111","name":"Season 2","overview":"","seasonNumber":2,"episodeCount":1,"voteAverage":0,"externalIds":{}},{"id":"season-1","mediaType":"season","seriesId":"11111111-1111-4111-8111-111111111111","name":"Season 1","overview":"","seasonNumber":1,"episodeCount":1,"voteAverage":0,"externalIds":{}}],"aliases":[],"episodeOrders":[],"mappingProvider":"tmdb","externalIds":{"imdb":"tt1234567"}}
        """.utf8))
    let currentSeason = try JSONDecoder().decode(
      Season.self,
      from: Data(
        """
        {"id":"season-1","mediaType":"season","seriesId":"11111111-1111-4111-8111-111111111111","name":"Season 1","overview":"","seasonNumber":1,"voteAverage":0,"episodes":[\(MediaRegressionTransport.episodeJSON(id: currentEpisode, number: 1))],"externalIds":{}}
        """.utf8))
    let nextSeason = try JSONDecoder().decode(
      Season.self,
      from: Data(
        """
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
      XCTAssertTrue(
        RivuneExternalApplication.rankedCandidates(
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

  private func makeModel(
    transport: any HTTPTransport,
    semanticSearchAvailable: Bool = false,
    semanticSearchTimeoutNanoseconds: UInt64? = nil
  ) throws -> (RivuneAppModel, UserDefaults) {
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
      serverOrigin: URL(string: "https://server.example.test")!,
      semanticSearchAvailable: semanticSearchAvailable,
      semanticSearchTimeoutNanoseconds: semanticSearchTimeoutNanoseconds
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
      mappingProvider: nil, episodeOrderId: nil, metadataSeasonId: nil, seasonId: nil,
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
      mappingProvider: nil, episodeOrderId: nil, metadataSeasonId: nil, seasonId: nil,
      seasonNumber: nil,
      episodeNumber: nil,
      runtimeMinutes: nil
    )
  }

  private func searchTarget(id: String, externalIds: [String: String]) -> RivuneMediaTarget {
    RivuneMediaTarget(
      id: id, resourceId: id, mediaType: "movie", title: id, titleId: nil,
      provider: nil, externalId: nil, externalIds: externalIds,
      sourceAddonId: UUID(uuidString: "66666666-6666-4666-8666-666666666666"),
      sourceCatalogId: "search-movie", sourceName: nil, posterUrl: nil,
      backgroundUrl: nil, logoUrl: nil, overview: nil, releaseInfo: nil, released: nil,
      seriesId: nil, mappingProvider: nil, episodeOrderId: nil, metadataSeasonId: nil,
      seasonId: nil, seasonNumber: nil, episodeNumber: nil,
      runtimeMinutes: nil
    )
  }

  private func decodeEpisode(id: UUID, number: Int) throws -> Episode {
    try JSONDecoder().decode(
      Episode.self, from: Data(MediaRegressionTransport.episodeJSON(id: id, number: number).utf8))
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
  private let resolvedDurationSeconds: Double?
  private let episodeOrderID: String?
  private let metadataSeasonID: String?
  private var recordedProgressBatchSizes: [Int] = []
  private var recordedWatchedBatchSizes: [Int] = []
  private var recordedSeriesQueries: [[String: String]] = []
  private var recordedSeasonIDs: [String] = []
  private var recordedPlaybackResourceIDs: [String] = []
  private var recordedMarkerRequestCount = 0

  init(
    episodeIDs: [UUID],
    initiallyCompleted: Set<UUID> = [],
    failingWatchedBatch: Int? = nil,
    streamURL: String = "https://cdn.example.test/video.mp4",
    resolvedDurationSeconds: Double? = nil,
    episodeOrderID: String? = nil,
    metadataSeasonID: String? = nil
  ) {
    self.episodeIDs = episodeIDs
    self.initiallyCompleted = initiallyCompleted
    self.failingWatchedBatch = failingWatchedBatch
    self.streamURL = streamURL
    self.resolvedDurationSeconds = resolvedDurationSeconds
    self.episodeOrderID = episodeOrderID
    self.metadataSeasonID = metadataSeasonID
  }

  func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
    let path = request.url?.path ?? ""
    if path == "/.well-known/rivune" {
      return response(
        request,
        body: """
          {"name":"Rivune","serverVersion":"test","protocolVersion":22,"apiBaseUrl":"/api/v1","setupRequired":false,"setupCompleted":true,"demoAvailable":false,"timezone":"UTC","interfaceLanguage":"en-US"}
          """)
    }
    if path.contains("/metadata/series/") {
      let query = Dictionary(
        uniqueKeysWithValues: (URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?
          .queryItems ?? []).compactMap { item in item.value.map { (item.name, $0) } })
      recordedSeriesQueries.append(query)
      return response(request, body: seriesJSON())
    }
    if path.contains("/metadata/seasons/") {
      recordedSeasonIDs.append(
        path.split(separator: "/").last.map(String.init)?
          .removingPercentEncoding ?? "")
      return response(request, body: seasonJSON())
    }
    if path.contains("/metadata/titles/") && path.hasSuffix("/trailers") {
      return response(request, body: "{\"trailers\":[]}")
    }
    if path.contains("/metadata/titles/") {
      return response(request, body: movieJSON())
    }
    if path.hasSuffix("/library") {
      return response(
        request, body: "{\"items\":[],\"page\":1,\"totalPages\":0,\"totalResults\":0}")
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
      guard let id = UUID(uuidString: path.split(separator: "/").last.map(String.init) ?? ""),
        episodeIDs.contains(id)
      else {
        return response(request, status: 204, body: "")
      }
      return response(
        request,
        body: progressJSON(
          id: id, completed: initiallyCompleted.contains(id),
          version: initiallyCompleted.contains(id) ? 1 : 0))
    }
    if path.hasSuffix("/titles/watched/batch") {
      let body = try XCTUnwrap(request.httpBody)
      let input = try JSONDecoder().decode(WatchedBatchRequest.self, from: body)
      recordedWatchedBatchSizes.append(input.items.count)
      if recordedWatchedBatchSizes.count == failingWatchedBatch {
        return response(
          request, status: 409,
          body: "{\"error\":{\"code\":\"conflict\",\"message\":\"Progress version conflict.\"}}")
      }
      let items = input.items.map { item in
        "{\"titleId\":\"\(item.titleId.uuidString.lowercased())\",\"progress\":\(progressJSON(id: item.titleId, completed: item.completed, version: item.expectedVersion + 1))}"
      }.joined(separator: ",")
      return response(request, body: "{\"items\":[\(items)]}")
    }
    if path.hasSuffix("/playback/markers") {
      recordedMarkerRequestCount += 1
      return response(request, body: "{\"markers\":[]}")
    }
    if path.hasSuffix("/playback/sources") {
      let body = try XCTUnwrap(request.httpBody)
      let input = try XCTUnwrap(
        JSONSerialization.jsonObject(with: body) as? [String: Any])
      recordedPlaybackResourceIDs.append(try XCTUnwrap(input["resourceId"] as? String))
      return response(
        request,
        body: """
          {"sources":[{"id":"source-1","sourceRef":"source-ref","addonId":"66666666-6666-4666-8666-666666666666","manifestId":"manifest","streamIndex":0,"name":"Direct","protocol":"http","mode":"direct","container":"mp4","expiresAt":"2099-01-01T00:00:00Z","stableIdentity":"stable"}],"providerErrors":[]}
          """)
    }
    if path.hasSuffix("/playback/prepare") {
      return response(
        request,
        body:
          "{\"sourceRef\":\"source-ref\",\"mode\":\"direct\",\"protocol\":\"http\",\"container\":\"mp4\",\"subtitleCount\":0,\"expiresAt\":\"2099-01-01T00:00:00Z\"}"
      )
    }
    if path.hasSuffix("/playback/resolve") {
      let media =
        resolvedDurationSeconds.map {
          "\"media\":{\"container\":\"mp4\",\"durationSeconds\":\($0),\"videoTracks\":[],\"audioTracks\":[],\"subtitleTracks\":[]},"
        } ?? ""
      return response(
        request, status: 201,
        body: """
          {"id":"77777777-7777-4777-8777-777777777777","selectedSourceId":"resolved-1","sources":[{"id":"resolved-1","addonId":"66666666-6666-4666-8666-666666666666","manifestId":"manifest","mode":"direct","url":"\(streamURL)","protocol":"http","container":"mp4",\(media)"compatible":true}],"subtitles":[],"providerErrors":[],"expiresAt":"2099-01-01T00:00:00Z"}
          """)
    }
    if path.contains("/playback/sessions/") && request.httpMethod == "DELETE" {
      return response(request, status: 204, body: "")
    }
    return response(
      request, status: 404,
      body: "{\"error\":{\"code\":\"not_found\",\"message\":\"Unexpected test route.\"}}")
  }

  func progressBatchSizes() -> [Int] { recordedProgressBatchSizes }
  func watchedBatchSizes() -> [Int] { recordedWatchedBatchSizes }
  func seriesQueries() -> [[String: String]] { recordedSeriesQueries }
  func seasonIDs() -> [String] { recordedSeasonIDs }
  func playbackResourceIDs() -> [String] { recordedPlaybackResourceIDs }
  func markerRequestCount() -> Int { recordedMarkerRequestCount }

  private func seriesJSON() -> String {
    let seasonID = metadataSeasonID ?? "season-1"
    let seasons =
      episodeIDs.isEmpty
      ? "[]"
      : "[{\"id\":\"\(seasonID)\",\"mediaType\":\"season\",\"seriesId\":\"11111111-1111-4111-8111-111111111111\",\"name\":\"Season 1\",\"overview\":\"\",\"seasonNumber\":1,\"episodeCount\":\(episodeIDs.count),\"voteAverage\":0,\"externalIds\":{}}]"
    let orders = episodeOrderID.map {
      "\"episodeOrders\":[{\"id\":\"\($0)\",\"name\":\"DVD\",\"type\":\"dvd\",\"isDefault\":false}],\"selectedEpisodeOrderId\":\"\($0)\",\"mappingProvider\":\"tvdb\""
    } ?? "\"episodeOrders\":[],\"mappingProvider\":\"tmdb\""
    return
      "{\"id\":\"11111111-1111-4111-8111-111111111111\",\"mediaType\":\"series\",\"name\":\"Series\",\"originalName\":\"Series\",\"originalLanguage\":\"en\",\"overview\":\"\",\"genres\":[],\"cast\":[],\"voteAverage\":0,\"voteCount\":0,\"seasons\":\(seasons),\"aliases\":[],\(orders),\"externalIds\":{\"imdb\":\"tt1234567\"}}"
  }

  private func seasonJSON() -> String {
    let seasonID = metadataSeasonID ?? "season-1"
    let episodes = episodeIDs.enumerated().map { index, id in
      let tvdb = episodeOrderID == nil ? "" : "\"tvdb\":\"\(10_357_450 + index)\""
      return
        "{\"id\":\"\(id.uuidString.lowercased())\",\"mediaType\":\"episode\",\"seasonId\":\"\(seasonID)\",\"name\":\"Episode \(index + 1)\",\"overview\":\"\",\"seasonNumber\":1,\"episodeNumber\":\(index + 1),\"voteAverage\":0,\"voteCount\":0,\"externalIds\":{\(tvdb)}}"
    }.joined(separator: ",")
    return
      "{\"id\":\"\(seasonID)\",\"mediaType\":\"season\",\"seriesId\":\"11111111-1111-4111-8111-111111111111\",\"name\":\"Season 1\",\"overview\":\"\",\"seasonNumber\":1,\"voteAverage\":0,\"episodes\":[\(episodes)],\"externalIds\":{}}"
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

  private func response(_ request: URLRequest, status: Int = 200, body: String) -> (
    Data, HTTPURLResponse
  ) {
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
  private let configuredSearchTypes: [String]
  private let semanticSearch: Bool
  private let semanticDelayNanoseconds: UInt64
  private let semanticMediaTypes: [String]
  private let addonDelayNanosecondsByType: [String: UInt64]
  private let searchDelayNanosecondsByTerm: [String: UInt64]
  private let ignoresSearchCancellation: Bool
  private let includesSearchTermInResults: Bool
  private var recordedSearchTypes: [String] = []
  private var recordedSearchTerms: [String] = []
  private var recordedSemanticExclusions: [[String]] = []
  private var recordedLibraryMediaTypes: [String?] = []
  private var recordedCalendarRanges: [(from: String, to: String)] = []
  private var activeSearchRequests = 0
  private var maximumActiveSearchRequests = 0

  init(
    searchTypes: [String] = ["movie"],
    semanticSearch: Bool = false,
    semanticDelayNanoseconds: UInt64 = 0,
    semanticMediaTypes: [String] = ["movie"],
    addonDelayNanosecondsByType: [String: UInt64] = [:],
    searchDelayNanosecondsByTerm: [String: UInt64] = [:],
    ignoresSearchCancellation: Bool = false,
    includesSearchTermInResults: Bool = false
  ) {
    configuredSearchTypes = searchTypes
    self.semanticSearch = semanticSearch
    self.semanticDelayNanoseconds = semanticDelayNanoseconds
    self.semanticMediaTypes = semanticMediaTypes
    self.addonDelayNanosecondsByType = addonDelayNanosecondsByType
    self.searchDelayNanosecondsByTerm = searchDelayNanosecondsByTerm
    self.ignoresSearchCancellation = ignoresSearchCancellation
    self.includesSearchTermInResults = includesSearchTermInResults
  }

  func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
    let path = request.url?.path ?? ""
    let query = Dictionary(
      uniqueKeysWithValues: (URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?
        .queryItems ?? []).map { ($0.name, $0.value ?? "") })
    if path == "/.well-known/rivune" {
      return response(
        request,
        body: """
          {"name":"Rivune","serverVersion":"test","protocolVersion":22,"apiBaseUrl":"/api/v1","setupRequired":false,"setupCompleted":true,"demoAvailable":false,"timezone":"UTC","interfaceLanguage":"en-US"}
          """)
    }
    if path.hasSuffix("/addons/catalogs") {
      let catalogs = configuredSearchTypes.enumerated().map { index, type in
        "{\"addonId\":\"66666666-6666-4666-8666-666666666666\",\"manifestId\":\"manifest\",\"position\":\(index),\"catalog\":{\"type\":\"\(type)\",\"id\":\"search-\(type)\",\"extra\":[{\"name\":\"search\"}]},\"addonCatalog\":true,\"searchable\":true}"
      }.joined(separator: ",")
      return response(request, body: "{\"catalogs\":[\(catalogs)]}")
    }
    if path.hasSuffix("/search/semantic") {
      guard semanticSearch else {
        return response(
          request, status: 404,
          body: "{\"error\":{\"code\":\"not_found\",\"message\":\"Unexpected test route.\"}}")
      }
      if semanticDelayNanoseconds > 0 {
        try await Task.sleep(nanoseconds: semanticDelayNanoseconds)
      }
      let body = try XCTUnwrap(request.httpBody)
      let input = try JSONDecoder().decode(SemanticSearchRequest.self, from: body)
      recordedSemanticExclusions.append(input.excludedIntentIds)
      let removed = !input.excludedIntentIds.isEmpty
      let intents =
        removed
        ? "[{\"id\":\"media_type:movie\",\"kind\":\"media_type\",\"value\":\"movie\",\"label\":\"Movies\"}]"
        : "[{\"id\":\"media_type:movie\",\"kind\":\"media_type\",\"value\":\"movie\",\"label\":\"Movies\"},{\"id\":\"genre:war\",\"kind\":\"genre\",\"value\":\"war\",\"label\":\"War\"}]"
      let items =
        removed
        ? "[]"
        : "[{\"id\":\"tmdb:1\",\"mediaType\":\"movie\",\"title\":\"Semantic duplicate\",\"externalIds\":{\"imdb\":\"tt-duplicate\",\"tmdb\":\"1\"},\"sources\":[]},{\"id\":\"tmdb:2\",\"mediaType\":\"movie\",\"title\":\"Semantic unique\",\"externalIds\":{\"tmdb\":\"2\"},\"sources\":[]}]"
      let titleQuery = removed ? "Dune guerre" : "Dune"
      let mediaTypes = semanticMediaTypes.map { "\"\($0)\"" }.joined(separator: ",")
      return response(
        request,
        body:
          "{\"intents\":\(intents),\"titleQuery\":\"\(titleQuery)\",\"mediaTypes\":[\(mediaTypes)],\"items\":\(items),\"page\":1,\"hasMore\":false,\"partial\":false}"
      )
    }
    if path.contains("/addons/catalogs/search/") {
      let skip = Int(query["skip"] ?? "0") ?? 0
      recordedSearchSkips.append(skip)
      let type = request.url?.lastPathComponent ?? "movie"
      recordedSearchTypes.append(type)
      activeSearchRequests += 1
      maximumActiveSearchRequests = max(maximumActiveSearchRequests, activeSearchRequests)
      defer { activeSearchRequests -= 1 }
      let term = query["search"] ?? ""
      recordedSearchTerms.append(term)
      let delay = max(addonDelayNanosecondsByType[type] ?? 0, searchDelayNanosecondsByTerm[term] ?? 0)
      if delay > 0 {
        if ignoresSearchCancellation {
          _ = try? await Task.sleep(nanoseconds: delay)
        } else {
          try await Task.sleep(nanoseconds: delay)
        }
      }
      let count = skip == 0 ? 50 : 1
      let metas = (0..<count).map { index in
        let number = skip + index
        let prefix = includesSearchTermInResults
          ? term.replacingOccurrences(of: " ", with: "-") : type
        let id = semanticSearch && number == 0 ? "tt-duplicate" : "\(prefix)-\(number)"
        let title = semanticSearch && number == 0 ? "Direct title" : "Title \(number)"
        return "{\"id\":\"\(id)\",\"type\":\"\(type)\",\"name\":\"\(title)\"}"
      }.joined(separator: ",")
      return response(
        request,
        body: """
          {"results":[{"addonId":"66666666-6666-4666-8666-666666666666","manifestId":"manifest","resource":"catalog","type":"\(type)","id":"search-\(type)","payload":{"metas":[\(metas)]},"cache":{}}],"errors":[]}
          """)
    }
    if path.hasSuffix("/library") {
      let mediaType = query["mediaType"]
      recordedLibraryMediaTypes.append(mediaType)
      let page = Int(query["page"] ?? "1") ?? 1
      let totalPages = mediaType == nil ? 2 : 1
      let id =
        page == 1
        ? "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1"
        : "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa2"
      return response(
        request,
        body: """
          {"items":[{"titleId":"\(id)","mediaType":"\(mediaType ?? "movie")","title":"Library \(page)","available":true,"addedAt":"2099-01-01T00:00:00Z","updatedAt":"2099-01-01T00:00:00Z"}],"page":\(page),"totalPages":\(totalPages),"totalResults":\(totalPages)}
          """)
    }
    if path.hasSuffix("/calendar") {
      recordedCalendarRanges.append((query["from"] ?? "", query["to"] ?? ""))
      return response(request, body: "{\"events\":[]}")
    }
    return response(
      request, status: 404,
      body: "{\"error\":{\"code\":\"not_found\",\"message\":\"Unexpected test route.\"}}")
  }

  func searchSkips() -> [Int] { recordedSearchSkips }
  func searchTypes() -> [String] { recordedSearchTypes }
  func searchTerms() -> [String] { recordedSearchTerms }
  func semanticExclusions() -> [[String]] { recordedSemanticExclusions }
  func maximumConcurrentSearchRequests() -> Int { maximumActiveSearchRequests }
  func libraryMediaTypes() -> [String?] { recordedLibraryMediaTypes }
  func calendarRanges() -> [(from: String, to: String)] { recordedCalendarRanges }

  private func response(_ request: URLRequest, status: Int = 200, body: String) -> (
    Data, HTTPURLResponse
  ) {
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
