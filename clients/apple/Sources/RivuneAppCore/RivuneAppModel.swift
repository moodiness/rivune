import Foundation
import Network
import RivuneAPI
#if canImport(UIKit)
import UIKit
#elseif canImport(AppKit)
import AppKit
#endif

public enum RivuneDestination: Equatable {
    case server
    case pairing
    case profiles
    case library
}

public enum RivuneAccent: String, CaseIterable, Identifiable, Sendable {
    case blue
    case coral
    case green
    case violet
    case rose

    public var id: String { rawValue }

    public var displayName: String {
        switch self {
        case .blue: return "Blue"
        case .coral: return "Coral"
        case .green: return "Green"
        case .violet: return "Violet"
        case .rose: return "Rose"
        }
    }
}
public enum RivunePlayerPreference: String, CaseIterable, Identifiable, Sendable {
    case ask, rivune, external
    public var id: String { rawValue }
    public var displayName: String {
        switch self {
        case .ask: return "Ask every time"
        case .rivune: return "Rivune player"
        case .external: return "External app"
        }
    }
}
public enum RivuneEmbeddedPlayerPreference: String, CaseIterable, Identifiable, Sendable {
    case automatic, native, mpv

    public var id: String { rawValue }
    public var displayName: String {
        switch self {
        case .automatic: return "Automatic"
        case .native: return "Apple player"
        case .mpv: return "MPV"
        }
    }
}

public enum RivunePlaybackEngine: String, Sendable {
    case native, mpv
}

public struct RivunePlaybackEngineSelection: Equatable, Sendable {
    public let engine: RivunePlaybackEngine
    public let fallbackAllowed: Bool

    public init(engine: RivunePlaybackEngine, fallbackAllowed: Bool) {
        self.engine = engine
        self.fallbackAllowed = fallbackAllowed
    }
}

public enum RivunePlaybackEnginePolicy {
    public static func selection(
        for preference: RivuneEmbeddedPlayerPreference,
        protocol streamingProtocol: String? = nil,
        container: String? = nil
    ) -> RivunePlaybackEngineSelection {
        switch preference {
        case .native:
            return RivunePlaybackEngineSelection(engine: .native, fallbackAllowed: false)
        case .mpv:
            return RivunePlaybackEngineSelection(engine: .mpv, fallbackAllowed: false)
        case .automatic:
            let protocolName = streamingProtocol?.lowercased()
            let containerName = container?.lowercased()
            let nativeDirect = protocolName == "hls" || containerName.map { ["mp4", "mov", "m4v", "mpegts"].contains($0) } == true
            return RivunePlaybackEngineSelection(engine: nativeDirect ? .native : .mpv, fallbackAllowed: true)
        }
    }

    public static func preservesOriginalSource(
        for selection: RivunePlaybackEngineSelection,
        externally: Bool
    ) -> Bool {
        externally || selection.engine == .mpv || selection.fallbackAllowed
    }
}

public enum RivuneAnimationPreference: String, CaseIterable, Identifiable, Sendable {
    case system, full, reduced
    public var id: String { rawValue }
    public var displayName: String {
        switch self { case .system: return "System"; case .full: return "Full"; case .reduced: return "Reduced" }
    }
}

public enum RivuneFrameRatePreference: String, CaseIterable, Identifiable, Sendable {
    case system, enabled, disabled
    public var id: String { rawValue }
    public var displayName: String {
        switch self { case .system: return "System"; case .enabled: return "On"; case .disabled: return "Off" }
    }
}

public enum RivuneVideoAspect: String, CaseIterable, Identifiable, Sendable {
    case fit, fill, zoom
    public var id: String { rawValue }
    public var displayName: String {
        switch self { case .fit: return "Fit"; case .fill: return "Fill"; case .zoom: return "Zoom" }
    }
}

public enum RivuneNetworkQuality: String, CaseIterable, Identifiable, Sendable {
    case automatic, economy, balanced, maximum
    public var id: String { rawValue }
    public var displayName: String {
        switch self { case .automatic: return "Automatic"; case .economy: return "Economy"; case .balanced: return "Balanced"; case .maximum: return "Maximum" }
    }
}

public struct RivuneMediaTarget: Identifiable, Equatable, Sendable {
    public let id: String
    public let resourceId: String
    public let mediaType: String
    public let title: String
    public let titleId: UUID?
    public let provider: String?
    public let externalId: String?
    public let externalIds: [String: String]
    public let sourceAddonId: UUID?
    public let sourceCatalogId: String?
    public let sourceName: String?
    public let posterUrl: String?
    public let backgroundUrl: String?
    public let logoUrl: String?
    public let overview: String?
    public let releaseInfo: String?
    public let released: String?
    public let seriesId: UUID?
    public let seasonId: String?
    public let seasonNumber: Int?
    public let episodeNumber: Int?
    public let runtimeMinutes: Int?
}

extension RivuneMediaTarget {
    var playbackAddonId: UUID? { mediaType == "tv" ? sourceAddonId : nil }
}

public struct RivuneMediaDetail: Equatable, Sendable {
    public var target: RivuneMediaTarget
    public var titleId: UUID
    public var movie: Movie?
    public var series: Series?
    public var episode: Episode?
    public var season: Season?
    public var parentSeries: Series?
    public var progress: PlaybackProgress?
    public var trailers: [Trailer]
    public var inLibrary: Bool
}

public struct RivunePlaybackPresentation: Identifiable, Equatable, Sendable {
    public let id: UUID
    public let sessionId: UUID
    public let sourceRef: String
    public let titleId: UUID
    public let title: String
    public let url: URL
    public let engine: RivunePlaybackEngine
    public let fallbackAllowed: Bool
    public let startSeconds: Int
    public let markers: [PlaybackMarker]
    public let durationSeconds: Int?
    public let expectedVersion: Int64
    public let audioTracks: [PlaybackMediaTrack]
    public let subtitles: [PlaybackSubtitle]
    public let selectedAudioTrack: Int?
    public let selectedSubtitleId: String?
    public let coordinatedItem: CoordinatedPlaybackItem?
    public let sourceAddonId: UUID?
    public let nextEpisode: RivuneMediaTarget?
}


public struct OpenedCollectionFolder: Identifiable, Equatable {
    public let id: UUID
    public let collectionID: UUID
    public let folder: CollectionFolder
    public let items: [CollectionItem]?
    public let sourcePosterUrls: [String: String]?
    public let page: Int
    public let hasMore: Bool
    public let errors: [CollectionSourceFailure]
}

public enum RivuneViewerTab: String, CaseIterable, Identifiable {
    case home, search, library, calendar

    public var id: String { rawValue }
}

private struct RivuneSearchBatchOutcome: Sendable {
    let batch: AddonResourceBatch?
    let failed: Bool
}

public typealias RivuneSearchItem = RivuneMediaTarget

public struct RivuneHeroItem: Identifiable, Equatable, Sendable {
    public let id: String
    public let title: String
    public let backgroundUrl: String?
    public let logoUrl: String?
    public let releaseInfo: String?
    public let target: RivuneMediaTarget
}
public struct RivuneRecommendationItem: Identifiable, Equatable, Sendable {
    public let id: UUID
    public let reason: String
    public let target: RivuneMediaTarget
}


public enum RivuneAppFailure: Equatable, LocalizedError {
    case invalidServer
    case setupRequired
    case incompatibleServer
    case serverUnreachable
    case pairingExpired
    case pairingCapacity
    case deviceLimit
    case pairingFailed
    case noProfiles
    case invalidPin
    case pinRateLimited
    case contentLoad
    case sessionExpired
    case message(String)

    public var errorDescription: String? {
        switch self {
        case .invalidServer: return "Enter a valid Rivune server address."
        case .setupRequired: return "This server must be configured in a web browser first."
        case .incompatibleServer: return "This server uses an incompatible Rivune protocol."
        case .serverUnreachable: return "The Rivune server could not be reached."
        case .pairingExpired: return "The pairing code expired. Request a new code."
        case .pairingCapacity: return "The server is handling too many pairing requests. Try again later."
        case .deviceLimit: return "This account has reached its device limit."
        case .pairingFailed: return "The device could not be paired."
        case .noProfiles: return "No accessible profile is available for this device."
        case .invalidPin: return "The profile PIN is incorrect."
        case .pinRateLimited: return "Too many PIN attempts. Try again later."
        case .contentLoad: return "The library could not be loaded."
        case .sessionExpired: return "The session expired. Pair this device again."
        case .message(let value): return value
        }
    }
}

public enum ServerAddressNormalizer {
    public static func normalize(_ value: String) -> String {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, !trimmed.contains("://") else { return trimmed }
        // greenlight:ignore http-not-https — trusted LAN literals intentionally default to HTTP.
        let probe = URL(string: "http://\(trimmed)")
        let local = probe?.host.map(isLocalNetworkHost) == true
        return "\(local ? "http" : "https")://\(trimmed)"
    }

    private static func isLocalNetworkHost(_ rawHost: String) -> Bool {
        let host = rawHost
            .trimmingCharacters(in: CharacterSet(charactersIn: "[]"))
            .lowercased()
        guard !host.isEmpty else { return false }
        if host == "localhost" || host == "::1" { return true }
        if host.contains(":") {
            guard let firstGroup = host.split(separator: ":", omittingEmptySubsequences: true).first,
                  let prefix = UInt16(firstGroup, radix: 16) else { return false }
            return prefix & 0xfe00 == 0xfc00
        }
        let octets = host.split(separator: ".", omittingEmptySubsequences: false).compactMap { Int($0) }
        guard octets.count == 4, octets.allSatisfy({ 0...255 ~= $0 }) else { return false }
        return octets[0] == 10 ||
            octets[0] == 127 ||
            (octets[0] == 172 && 16...31 ~= octets[1]) ||
            (octets[0] == 192 && octets[1] == 168)
    }
}

struct RivuneSeriesNavigationState: Equatable, Sendable {
    let episodes: [Episode]
    let progress: [UUID: PlaybackProgress]
    let watched: Bool?
}

@MainActor
public final class RivuneAppModel: ObservableObject {
    @Published public private(set) var destination: RivuneDestination = .server
    @Published public var serverAddress: String
    @Published public private(set) var serverName = "Rivune"
    @Published public private(set) var serverVersion: String?
    @Published public private(set) var serverProtocolVersion: Int?
    @Published public private(set) var pairingCode: String?
    @Published public private(set) var verificationURL: URL?
    @Published public private(set) var pairingAccepted = false
    @Published public private(set) var profiles: [Profile] = []
    @Published public private(set) var profileAvatarData: [UUID: Data] = [:]
    @Published public private(set) var activeProfile: Profile?
    @Published public private(set) var collections: [Collection] = []
    @Published public private(set) var folderArtworkURLs: [UUID: String] = [:]
    @Published public private(set) var continueWatchingItems: [ContinueWatchingItem] = []
    @Published public private(set) var heroItems: [RivuneHeroItem] = []
    @Published public private(set) var recommendationItems: [RivuneRecommendationItem] = []
    @Published public private(set) var offlineItems: [RivuneOfflineMediaItem] = []
    @Published public private(set) var offlineProfiles: [RivuneOfflineProfileAccess] = []
    @Published public private(set) var offlineUnlockFailure: RivuneAppFailure?
    @Published public private(set) var pendingOfflineProfile: RivuneOfflineProfileAccess?
    @Published public private(set) var offlineDownloadBytes: Int64 = 0
    @Published public private(set) var offlineDownloadActive = false
    private struct OfflineDownloadSourceIdentity: Equatable {
        let id: String
        let sourceRef: String

        init(_ source: PlaybackSourceOption) {
            id = source.id
            sourceRef = source.sourceRef
        }
    }

    private var offlineDownloadSourceIdentity: OfflineDownloadSourceIdentity?
    @Published public private(set) var playbackDevices: [PlaybackDevice] = []
    @Published public private(set) var activePlaybackRoom: PlaybackRoom?
    @Published public private(set) var playbackCoordinationAvailable = false
    @Published public private(set) var pendingPlaybackCommands: [PlaybackCommand] = []
    @Published public private(set) var openedFolder: OpenedCollectionFolder?
    @Published public private(set) var selectedTab: RivuneViewerTab = .home
    @Published public var searchQuery = ""
    @Published public private(set) var searchItems: [RivuneSearchItem] = []
    @Published public private(set) var searchHasMore = false
    @Published public private(set) var searchPartial = false
    @Published public private(set) var libraryItems: [RivuneAPI.LibraryItem] = []
    @Published public private(set) var libraryMediaType: TitleMediaType?
    @Published public private(set) var libraryPage = 0
    @Published public private(set) var libraryTotalPages = 0
    @Published public private(set) var libraryTotalResults = 0
    @Published public private(set) var calendarEvents: [CalendarEvent] = []
    @Published public private(set) var calendarMonth = Calendar.current.dateInterval(of: .month, for: Date())?.start ?? Date()
    @Published public private(set) var tabLoading = false
    @Published public private(set) var tabFailure: RivuneAppFailure?
    @Published public private(set) var isBusy = false
    @Published public private(set) var failure: RivuneAppFailure?
    @Published public private(set) var accent: RivuneAccent

    @Published public private(set) var preferredPlayer: RivunePlayerPreference
    @Published public private(set) var embeddedPlayerPreference: RivuneEmbeddedPlayerPreference
    @Published public private(set) var startupTab: RivuneViewerTab
    @Published public private(set) var animationPreference: RivuneAnimationPreference
    @Published public private(set) var frameRateMatching: RivuneFrameRatePreference
    @Published public private(set) var videoAspect: RivuneVideoAspect
    @Published public private(set) var wifiQuality: RivuneNetworkQuality
    @Published public private(set) var mobileQuality: RivuneNetworkQuality
    @Published public private(set) var automaticallyShowStreams: Bool
    @Published public private(set) var autoSkipIntro: Bool
    @Published public private(set) var autoSkipRecap: Bool
    @Published public private(set) var autoSkipOutro: Bool
    @Published public private(set) var mediaDetail: RivuneMediaDetail?
    @Published public private(set) var seasonTrailers: [Trailer] = []
    @Published public private(set) var episodeProgress: [UUID: PlaybackProgress] = [:]
    @Published public private(set) var seriesEpisodesWatched: Bool?
    @Published public private(set) var mediaActionLoading = false
    @Published public private(set) var selectedSeason: Season?
    @Published public private(set) var mediaLoading = false
    @Published public private(set) var mediaFailure: RivuneAppFailure?
    @Published public private(set) var playbackSources: [PlaybackSourceOption] = []
    @Published public private(set) var showPlaybackSources = false
    @Published public private(set) var playbackPresentation: RivunePlaybackPresentation?
    @Published public private(set) var minimizedPlaybackPresentation: RivunePlaybackPresentation?
    @Published public private(set) var playbackOptionLoading = false
    @Published public private(set) var externalPlaybackURL: URL?
    private var externalPlaybackSessionID: UUID?
    @Published public private(set) var profileSettings: SettingsValues?
    @Published public private(set) var profileSettingsSources: EffectiveSettingsSources?
    @Published public private(set) var settingsLoading = false
    @Published public private(set) var settingsFailure: RivuneAppFailure?
    @Published public private(set) var updateState: RivuneAppleUpdateState = .idle
    @Published public private(set) var updateNotice: RivuneAppleUpdate?
    private let diagnostics = RivuneDiagnosticsBuffer()
    private let installedApplicationVersion: String
    private let updateChecker: any RivuneAppleUpdateChecking
    private let defaults: UserDefaults
    private var serverOrigin: URL?
    private var client: RivuneAPIClient?
    private var offlineScope: RivuneOfflineMediaScope?
    private var currentOfflineAccess: RivuneOfflineProfileAccess?
    private var storedOfflineProfiles: [RivuneOfflineProfileAccess] = []
    private var operation: Task<Void, Never>?
    private var updateOperation: Task<Void, Never>?
    private var tabOperation: Task<Void, Never>?
    private var tabGeneration: UInt64 = 0
    private var searchPage = 0
    private var searchDescriptors: [AddonCatalogDescriptor] = []
    private static let searchPageSize = 50
    private var generation: UInt64 = 0
    private var mediaOperation: Task<Void, Never>?
    private var mediaGeneration: UInt64 = 0
    private var settingsOperation: Task<Void, Never>?
    private var coordinationOperation: Task<Void, Never>?
    private var coordinationPositionMilliseconds: Int64 = 0
    private var coordinationDurationMilliseconds: Int64 = 0
    private var coordinationStatus = "idle"
    private var executedPlaybackCommandID: Int64?
    private var lastPlaybackCommandID: Int64 = 0
    private var localRecommendationsAvailable = false
    private var coordinationEndedPlaybackID: UUID?
    private var settingsGeneration: UInt64 = 0
    private var previousMediaDetail: RivuneMediaDetail?
    private struct PendingEpisodeAutoplay {
        let targetID: String
        let sourceAddonID: UUID?
    }
    private var pendingEpisodeAutoplay: PendingEpisodeAutoplay?
    private struct SeriesWatchState: Sendable {
        let episodes: [Episode]
        let progress: [UUID: PlaybackProgress]
    }
    private var seriesEpisodes: [Episode] = []
    private var previousSeriesState: RivuneSeriesNavigationState?
    private let pathMonitor = NWPathMonitor()
    private let pathMonitorQueue = DispatchQueue(label: "io.rivune.network-path")
    public var offlineAccessUnlocked: Bool { offlineScope != nil }
    public func isDownloading(_ source: PlaybackSourceOption) -> Bool {
        offlineDownloadActive && offlineDownloadSourceIdentity == OfflineDownloadSourceIdentity(source)
    }
    private var usesCellularNetwork = false
    public var canNavigateBackFromMedia: Bool { selectedSeason != nil || previousMediaDetail != nil }
    public var autoplayNextEpisode: Bool { profileSettings?.autoplayNextEpisode != false }
    public var applicationVersion: String { installedApplicationVersion }

    public convenience init(defaults: UserDefaults = .standard) {
        self.init(
            defaults: defaults,
            updateChecker: RivuneAppleUpdateChecker(),
            applicationVersion: RivuneAppleDiagnosticMetadata.current().appVersion
        )
    }

    init(
        defaults: UserDefaults,
        updateChecker: any RivuneAppleUpdateChecking,
        applicationVersion: String,
        client: RivuneAPIClient? = nil,
        serverOrigin: URL? = nil
    ) {
        self.defaults = defaults
        self.updateChecker = updateChecker
        self.installedApplicationVersion = applicationVersion
        self.client = client
        self.serverOrigin = serverOrigin
        self.serverAddress = defaults.string(forKey: Self.serverKey) ?? ""
        self.accent = defaults.string(forKey: Self.accentKey).flatMap(RivuneAccent.init(rawValue:)) ?? .blue
        self.preferredPlayer = defaults.string(forKey: Self.playerKey).flatMap(RivunePlayerPreference.init(rawValue:)) ?? .ask
        self.embeddedPlayerPreference = defaults.string(forKey: Self.embeddedPlayerKey).flatMap(RivuneEmbeddedPlayerPreference.init(rawValue:)) ?? .automatic
        self.startupTab = defaults.string(forKey: Self.startupTabKey).flatMap(RivuneViewerTab.init(rawValue:)) ?? .home
        self.animationPreference = defaults.string(forKey: Self.animationKey).flatMap(RivuneAnimationPreference.init(rawValue:)) ?? .system
        self.frameRateMatching = defaults.string(forKey: Self.frameRateKey).flatMap(RivuneFrameRatePreference.init(rawValue:)) ?? .system
        self.videoAspect = defaults.string(forKey: Self.videoAspectKey).flatMap(RivuneVideoAspect.init(rawValue:)) ?? .fit
        self.wifiQuality = defaults.string(forKey: Self.wifiQualityKey).flatMap(RivuneNetworkQuality.init(rawValue:)) ?? .automatic
        self.mobileQuality = defaults.string(forKey: Self.mobileQualityKey).flatMap(RivuneNetworkQuality.init(rawValue:)) ?? .automatic
        self.automaticallyShowStreams = defaults.object(forKey: Self.showStreamsKey) as? Bool ?? true
        self.autoSkipIntro = defaults.bool(forKey: Self.skipIntroKey)
        self.autoSkipRecap = defaults.bool(forKey: Self.skipRecapKey)
        self.autoSkipOutro = defaults.bool(forKey: Self.skipOutroKey)
        pathMonitor.pathUpdateHandler = { [weak self] path in
            Task { @MainActor [weak self] in
                self?.usesCellularNetwork = path.usesInterfaceType(.cellular)
            }
        }
        pathMonitor.start(queue: pathMonitorQueue)
        if let data = defaults.data(forKey: Self.offlineProfilesKey),
           let profiles = try? JSONDecoder().decode([RivuneOfflineProfileAccess].self, from: data) {
            storedOfflineProfiles = profiles
        }
        defaults.removeObject(forKey: Self.offlineScopeKey)
        diagnostics.record(.appStarted)
        if let data = defaults.data(forKey: Self.updateCacheKey) {
            if let cached = try? JSONDecoder().decode(RivuneAppleUpdateCache.self, from: data),
               let restored = cached.restoredState(
                   installedVersion: applicationVersion,
                   platform: .current
               ) {
                updateState = restored
            } else {
                defaults.removeObject(forKey: Self.updateCacheKey)
                defaults.removeObject(forKey: Self.lastUpdateCheckKey)
                defaults.removeObject(forKey: Self.lastUpdateVersionKey)
            }
        }
    }

    deinit {
        operation?.cancel()
        tabOperation?.cancel()
        mediaOperation?.cancel()
        settingsOperation?.cancel()
        coordinationOperation?.cancel()
        updateOperation?.cancel()
        Task { await RivuneOfflineMediaStore.shared.stopPlayback() }
        pathMonitor.cancel()
    }

    public func start() {
        refreshAvailableOfflineProfiles()
        checkForUpdates(manual: false)
        guard destination == .server, !serverAddress.isEmpty else { return }
        connect(to: serverAddress)
    }

    public func connect(to address: String) {
        diagnostics.record(.serverConnectionStarted)
        lockOffline()
        beginOperation()
        let currentGeneration = generation
        let normalized = ServerAddressNormalizer.normalize(address)
        serverAddress = normalized
        failure = nil
        isBusy = true
        operation = Task { [weak self] in
            await self?.connectNow(normalized, generation: currentGeneration)
        }
    }

    public func restartPairing() {
        guard client != nil else { return }
        lockOffline()
        beginOperation()
        let currentGeneration = generation
        destination = .pairing
        pairingCode = nil
        pairingAccepted = false
        failure = nil
        isBusy = true
        operation = Task { [weak self] in
            await self?.beginPairing(generation: currentGeneration)
        }
    }

    public func selectProfile(_ profile: Profile, pin: String? = nil) {
        guard profile.accessible, let client else { return }
        lockOffline()
        resetProfileSettings()
        beginOperation()
        let currentGeneration = generation
        isBusy = true
        failure = nil
        operation = Task { [weak self] in
            do {
                _ = try await client.selectProfile(id: profile.id, pin: pin)
                guard let self, self.isCurrent(currentGeneration) else { return }
                self.resetTabState()
                self.activeProfile = profile
                self.registerOfflineProfile(profile, serverOrigin: self.serverOrigin, pin: pin)
                self.loadProfileSettings()
                self.destination = .library
                await self.loadCollections(using: client, generation: currentGeneration)
            } catch is CancellationError {
            } catch {
                guard let self, self.isCurrent(currentGeneration) else { return }
                if await self.recoverSessionIfNeeded(error, using: client, generation: currentGeneration) { return }
                self.isBusy = false
                self.failure = self.map(error, fallback: .message(error.localizedDescription))
            }
        }
    }

    public func selectTab(_ tab: RivuneViewerTab) {
        selectedTab = tab
        tabFailure = nil
        switch tab {
        case .home:
            break
        case .search:
            if !searchQuery.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty { search() }
        case .library:
            loadPersonalLibrary()
        case .calendar:
            loadCalendar()
        }
    }

    public func search() { runSearch(reset: true) }

    public func loadMoreSearch() {
        guard searchHasMore, !tabLoading else { return }
        runSearch(reset: false)
    }

    private func runSearch(reset: Bool) {
        guard let client else { return }
        let query = searchQuery.trimmingCharacters(in: .whitespacesAndNewlines)
        guard query.count >= 2 else {
            beginTabOperation()
            searchItems = []
            searchPage = 0
            searchHasMore = false
            searchPartial = false
            tabLoading = false
            tabFailure = nil
            return
        }
        let skip = reset ? 0 : searchPage * Self.searchPageSize
        beginTabOperation()
        let currentGeneration = tabGeneration
        tabLoading = true
        tabFailure = nil
        if reset {
            searchItems = []
            searchPage = 0
            searchHasMore = false
            searchPartial = false
        }
        tabOperation = Task { [weak self] in
            do {
                guard let self else { return }
                let descriptors = self.searchDescriptors.isEmpty ? try await client.addonCatalogs() : self.searchDescriptors
                let types = Array(Set(descriptors.filter(\.searchable).map { $0.catalog.type })).sorted()
                var outcomes: [RivuneSearchBatchOutcome] = []
                try await withThrowingTaskGroup(of: RivuneSearchBatchOutcome.self) { group in
                    for type in types {
                        group.addTask {
                            do {
                                return RivuneSearchBatchOutcome(
                                    batch: try await client.searchAddonCatalogs(
                                        type: type,
                                        search: query,
                                        skip: skip,
                                        limit: Self.searchPageSize
                                    ),
                                    failed: false
                                )
                            } catch is CancellationError {
                                throw CancellationError()
                            } catch {
                                return RivuneSearchBatchOutcome(batch: nil, failed: true)
                            }
                        }
                    }
                    for try await outcome in group { outcomes.append(outcome) }
                }
                let batches = outcomes.compactMap(\.batch)
                if batches.isEmpty, outcomes.contains(where: \.failed) { throw RivuneAPIError.invalidResponse }
                let incoming = batches.flatMap { batch in
                    batch.results.flatMap(Self.searchItems(from:))
                }
                guard self.isCurrentTab(currentGeneration) else { return }
                self.searchDescriptors = descriptors
                var seen = Set((reset ? [] : self.searchItems).map(\.id))
                let additions = incoming.filter { seen.insert($0.id).inserted }
                self.searchItems = (reset ? [] : self.searchItems) + additions
                self.searchPage = reset ? 1 : self.searchPage + 1
                let fullPage = batches.contains { batch in
                    batch.results.contains { Self.searchItems(from: $0).count >= Self.searchPageSize }
                }
                self.searchHasMore = fullPage && !additions.isEmpty
                self.searchPartial = outcomes.contains(where: \.failed) || batches.contains { !$0.errors.isEmpty }
                self.tabLoading = false
            } catch is CancellationError {
            } catch {
                guard let self, self.isCurrentTab(currentGeneration) else { return }
                self.tabLoading = false
                self.tabFailure = self.map(error, fallback: .contentLoad)
            }
        }
    }

    public func setLibraryMediaType(_ mediaType: TitleMediaType?) {
        guard libraryMediaType != mediaType else { return }
        libraryMediaType = mediaType
        loadPersonalLibrary(reset: true)
    }

    public func loadMoreLibrary() {
        guard libraryPage < libraryTotalPages, !tabLoading else { return }
        loadPersonalLibrary(reset: false)
    }

    private func loadPersonalLibrary(reset: Bool = true) {
        guard let client else { return }
        let page = reset ? 1 : libraryPage + 1
        beginTabOperation()
        let currentGeneration = tabGeneration
        tabLoading = true
        tabFailure = nil
        if reset {
            libraryItems = []
            libraryPage = 0
            libraryTotalPages = 0
            libraryTotalResults = 0
        }
        tabOperation = Task { [weak self] in
            do {
                guard let self else { return }
                let response = try await client.library(mediaType: self.libraryMediaType, page: page, pageSize: 100)
                guard self.isCurrentTab(currentGeneration) else { return }
                var seen = Set((reset ? [] : self.libraryItems).map(\.id))
                let additions = response.items.filter { seen.insert($0.id).inserted }
                self.libraryItems = (reset ? [] : self.libraryItems) + additions
                self.libraryPage = response.page
                self.libraryTotalPages = response.totalPages
                self.libraryTotalResults = response.totalResults
                self.tabLoading = false
            } catch is CancellationError {
            } catch {
                guard let self, self.isCurrentTab(currentGeneration) else { return }
                self.tabLoading = false
                self.tabFailure = self.map(error, fallback: .contentLoad)
            }
        }
    }

    public func previousCalendarMonth() { moveCalendarMonth(by: -1) }
    public func nextCalendarMonth() { moveCalendarMonth(by: 1) }

    private func moveCalendarMonth(by offset: Int) {
        calendarMonth = Calendar.current.date(byAdding: .month, value: offset, to: calendarMonth) ?? calendarMonth
        calendarEvents = []
        loadCalendar()
    }

    private func loadCalendar() {
        guard let client else { return }
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = .current
        guard let interval = calendar.dateInterval(of: .month, for: calendarMonth),
              let finalDay = calendar.date(byAdding: .day, value: -1, to: interval.end) else { return }
        let month = interval.start
        beginTabOperation()
        let currentGeneration = tabGeneration
        tabLoading = true
        tabFailure = nil
        tabOperation = Task { [weak self] in
            do {
                let formatter = DateFormatter()
                formatter.calendar = calendar
                formatter.locale = Locale(identifier: "en_US_POSIX")
                formatter.dateFormat = "yyyy-MM-dd"
                let events = try await client.calendar(
                    from: formatter.string(from: month),
                    to: formatter.string(from: finalDay)
                )
                guard let self, self.isCurrentTab(currentGeneration),
                      calendar.isDate(self.calendarMonth, equalTo: month, toGranularity: .month) else { return }
                self.calendarMonth = month
                self.calendarEvents = events
                self.tabLoading = false
            } catch is CancellationError {
            } catch {
                guard let self, self.isCurrentTab(currentGeneration) else { return }
                self.tabLoading = false
                self.tabFailure = self.map(error, fallback: .contentLoad)
            }
        }
    }

    public func retryLibrary() {
        guard let client, destination == .library else { return }
        beginOperation()
        let currentGeneration = generation
        failure = nil
        isBusy = true
        operation = Task { [weak self] in
            await self?.loadCollections(using: client, generation: currentGeneration)
        }
    }

    public func chooseAnotherProfile() {
        guard let client else { return }
        lockOffline()
        beginOperation()
        let currentGeneration = generation
        isBusy = true
        failure = nil
        operation = Task { [weak self] in
            do {
                try await client.clearProfileSelection()
                guard let self, self.isCurrent(currentGeneration) else { return }
                self.resetProfileSettings()
                self.activeProfile = nil
                self.lockOffline()
                self.collections = []
                self.resetTabState()
                self.destination = .profiles
                self.isBusy = false
            } catch is CancellationError {
            } catch {
                guard let self, self.isCurrent(currentGeneration) else { return }
                if await self.recoverSessionIfNeeded(error, using: client, generation: currentGeneration) { return }
                self.isBusy = false
                self.failure = self.map(error, fallback: .message(error.localizedDescription))
            }
        }
    }

    public func openFolder(in collection: Collection, folder: CollectionFolder) {
        guard let folderID = folder.id, let client else { return }
        beginOperation()
        let currentGeneration = generation
        openedFolder = OpenedCollectionFolder(id: folderID, collectionID: collection.id, folder: folder, items: nil, sourcePosterUrls: nil, page: 0, hasMore: false, errors: [])
        isBusy = true
        failure = nil
        operation = Task { [weak self] in
            do {
                let resolved = try await client.resolveCollectionFolder(
                    collectionId: collection.id,
                    folderId: folderID,
                    page: 1,
                    limit: 100
                )
                guard let self, self.isCurrent(currentGeneration), self.openedFolder?.id == folderID else { return }
                self.openedFolder = OpenedCollectionFolder(
                    id: folderID,
                    collectionID: resolved.collectionId,
                    folder: resolved.folder,
                    items: resolved.items,
                    sourcePosterUrls: resolved.sourcePosterUrls,
                    page: resolved.page,
                    hasMore: resolved.hasMore,
                    errors: resolved.errors
                )
                self.isBusy = false
            } catch {
                guard let self, self.isCurrent(currentGeneration) else { return }
                if await self.recoverSessionIfNeeded(error, using: client, generation: currentGeneration) { return }
                self.isBusy = false
                self.failure = self.map(error, fallback: .contentLoad)
            }
        }
    }

    public func loadMoreFolderItems() {
        guard let current = openedFolder, current.hasMore, !isBusy, let client else { return }
        beginOperation()
        let currentGeneration = generation
        isBusy = true
        operation = Task { [weak self] in
            do {
                let resolved = try await client.resolveCollectionFolder(
                    collectionId: current.collectionID,
                    folderId: current.id,
                    page: current.page + 1,
                    limit: 100
                )
                guard let self, self.isCurrent(currentGeneration), self.openedFolder?.id == current.id else { return }
                var seen = Set((current.items ?? []).map { "\($0.mediaType)\u{0}\($0.id)" })
                let additions = resolved.items.filter { seen.insert("\($0.mediaType)\u{0}\($0.id)").inserted }
                self.openedFolder = OpenedCollectionFolder(
                    id: current.id,
                    collectionID: resolved.collectionId,
                    folder: resolved.folder,
                    items: (current.items ?? []) + additions,
                    sourcePosterUrls: current.sourcePosterUrls.map { existing in
                        existing.merging(resolved.sourcePosterUrls ?? [:]) { _, latest in latest }
                    } ?? resolved.sourcePosterUrls,
                    page: resolved.page,
                    hasMore: resolved.hasMore && !additions.isEmpty,
                    errors: current.errors + resolved.errors
                )
                self.isBusy = false
            } catch {
                guard let self, self.isCurrent(currentGeneration) else { return }
                self.isBusy = false
                self.failure = self.map(error, fallback: .contentLoad)
            }
        }
    }

    public func closeFolder() {
        beginOperation()
        openedFolder = nil
        isBusy = false
        failure = nil
    }

    public func disconnect() {
        let currentClient = client
        beginOperation()
        client = nil
        resetSessionState()
        lockOffline()
        defaults.removeObject(forKey: Self.serverKey)
        serverAddress = ""
        destination = .server
        isBusy = false
        guard let currentClient else { return }
        operation = Task { try? await currentClient.logout() }
    }

    public func requestOfflineUnlock(_ profile: RivuneOfflineProfileAccess) {
        guard profile.scope != nil else { return }
        if profile.requiresPIN {
            offlineUnlockFailure = nil
            pendingOfflineProfile = profile
        } else {
            unlockOfflineProfile(profile)
        }
    }

    public func unlockOfflineProfile(_ profile: RivuneOfflineProfileAccess, pin: String? = nil) {
        guard pendingOfflineProfile == nil || pendingOfflineProfile?.id == profile.id,
              let scope = profile.scope, profile.permits(pin: pin) else {
            lockOffline(clearPending: false)
            pendingOfflineProfile = profile.requiresPIN ? profile : nil
            offlineUnlockFailure = .invalidPin
            return
        }
        lockOffline(clearPending: false)
        offlineUnlockFailure = nil
        pendingOfflineProfile = nil
        currentOfflineAccess = profile
        offlineScope = scope
        loadOfflineItems()
    }

    public func dismissOfflineUnlock() {
        pendingOfflineProfile = nil
        offlineUnlockFailure = nil
    }

    public func lockOffline(clearPending: Bool = true) {
        beginMediaOperation()
        if playbackPresentation?.sourceRef.hasPrefix("offline:") == true { playbackPresentation = nil }
        if minimizedPlaybackPresentation?.sourceRef.hasPrefix("offline:") == true { minimizedPlaybackPresentation = nil }
        offlineScope = nil
        offlineItems = []
        offlineUnlockFailure = nil
        if clearPending { pendingOfflineProfile = nil; currentOfflineAccess = nil }
        Task { await RivuneOfflineMediaStore.shared.stopPlayback() }
    }

    public func handleSceneBackground() {
        guard let scope = offlineScope,
              let access = currentOfflineAccess ?? storedOfflineProfiles.first(where: { $0.id == scope.identifier }),
              access.requiresPIN else { return }
        let offlineOnly = client == nil || destination == .server
        let shouldPrompt = !offlineItems.isEmpty
        lockOffline(clearPending: false)
        pendingOfflineProfile = shouldPrompt ? access : nil
        if offlineOnly { destination = .server }
    }

    public func clearFailure() { failure = nil }
    func diagnosticReport(
        metadata: RivuneAppleDiagnosticMetadata = .current(),
        generatedAtMilliseconds: Int64 = Int64(Date().timeIntervalSince1970 * 1_000)
    ) -> String {
        RivuneDiagnosticsReport.build(RivuneDiagnosticReportInput(
            generatedAtMilliseconds: generatedAtMilliseconds,
            appVersion: metadata.appVersion,
            appBuild: metadata.appBuild,
            platform: metadata.platform,
            operatingSystemVersion: metadata.operatingSystemVersion,
            deviceModel: metadata.deviceModel,
            isTelevision: metadata.isTelevision,
            serverAddress: serverAddress,
            serverDisplayName: serverName,
            serverVersion: serverVersion,
            serverProtocolVersion: serverProtocolVersion,
            startupTab: startupTab.rawValue,
            preferredPlayer: preferredPlayer.rawValue,
            embeddedPlayer: embeddedPlayerPreference.rawValue,
            animationPreference: animationPreference.rawValue,
            accentColor: accent.rawValue,
            frameRateMatching: frameRateMatching.rawValue,
            videoAspect: videoAspect.rawValue,
            wifiQuality: wifiQuality.rawValue,
            mobileQuality: mobileQuality.rawValue,
            events: diagnostics.snapshot()
        ))
    }

    func recordDiagnosticExport(succeeded: Bool) {
        diagnostics.record(succeeded ? .diagnosticExportSucceeded : .diagnosticExportFailed)
    }

    public func checkForUpdates(manual: Bool = true) {
        guard updateOperation == nil else { return }
        let now = Date()
        let version = applicationVersion
        if !manual,
           defaults.string(forKey: Self.lastUpdateVersionKey) == version,
           !RivuneAppleUpdateChecker.automaticCheckIsDue(
               lastSuccessfulCheck: defaults.object(forKey: Self.lastUpdateCheckKey) as? Date,
               now: now
           ) { return }

        let checker = updateChecker
        updateState = .checking
        diagnostics.record(.updateCheckStarted)
        updateOperation = Task { [weak self] in
            do {
                let result = try await checker.check(currentVersion: version)
                try Task.checkCancellation()
                guard let self else { return }
                self.defaults.set(Date(), forKey: Self.lastUpdateCheckKey)
                self.defaults.set(version, forKey: Self.lastUpdateVersionKey)
                switch result {
                case .upToDate(let currentVersion, let latestVersion):
                    self.updateState = .upToDate(currentVersion: currentVersion, latestVersion: latestVersion)
                    self.persistUpdateCache(.upToDate(currentVersion: currentVersion, latestVersion: latestVersion))
                    self.updateNotice = nil
                    self.diagnostics.record(.updateUpToDate)
                case .available(let update):
                    self.updateState = .available(update)
                    self.persistUpdateCache(.available(update))
                    if self.defaults.string(forKey: Self.lastNotifiedUpdateKey) != update.latestVersion {
                        self.defaults.set(update.latestVersion, forKey: Self.lastNotifiedUpdateKey)
                        self.updateNotice = update
                    }
                    self.diagnostics.record(.updateAvailable)
                }
            } catch is CancellationError {
                guard let self else { return }
                if case .checking = self.updateState { self.updateState = .idle }
            } catch {
                guard let self else { return }
                self.updateState = .failed
                self.diagnostics.record(.updateCheckFailed)
            }
            guard let self else { return }
            self.updateOperation = nil
        }
    }

    public func dismissUpdateNotice() {
        updateNotice = nil
    }

    private func persistUpdateCache(_ result: RivuneAppleUpdateCheckResult) {
        let cache: RivuneAppleUpdateCache
        switch result {
        case .upToDate(let currentVersion, let latestVersion):
            cache = RivuneAppleUpdateCache(
                currentVersion: currentVersion,
                latestVersion: latestVersion,
                publishedAt: "",
                releaseURL: "",
                packageURL: "",
                packageFileName: "",
                packageSize: 0,
                packageSHA256: ""
            )
        case .available(let update):
            cache = RivuneAppleUpdateCache(
                currentVersion: update.currentVersion,
                latestVersion: update.latestVersion,
                publishedAt: ISO8601DateFormatter().string(from: update.publishedAt),
                releaseURL: update.releaseURL.absoluteString,
                packageURL: update.packageURL.absoluteString,
                packageFileName: update.packageFileName,
                packageSize: update.packageSize,
                packageSHA256: update.packageSHA256
            )
        }
        if let data = try? JSONEncoder().encode(cache) {
            defaults.set(data, forKey: Self.updateCacheKey)
        }
    }

    func recordPlaybackFailure() {
        diagnostics.record(.playbackFailed)
    }

    public func setAccent(_ accent: RivuneAccent) {
        self.accent = accent
        defaults.set(accent.rawValue, forKey: Self.accentKey)
    }
    public func setPreferredPlayer(_ value: RivunePlayerPreference) { preferredPlayer = value; defaults.set(value.rawValue, forKey: Self.playerKey) }
    public func setEmbeddedPlayerPreference(_ value: RivuneEmbeddedPlayerPreference) { embeddedPlayerPreference = value; defaults.set(value.rawValue, forKey: Self.embeddedPlayerKey) }
    public func setStartupTab(_ value: RivuneViewerTab) { startupTab = value; defaults.set(value.rawValue, forKey: Self.startupTabKey) }
    public func setAnimationPreference(_ value: RivuneAnimationPreference) { animationPreference = value; defaults.set(value.rawValue, forKey: Self.animationKey) }
    public func setFrameRateMatching(_ value: RivuneFrameRatePreference) { frameRateMatching = value; defaults.set(value.rawValue, forKey: Self.frameRateKey) }
    public func setVideoAspect(_ value: RivuneVideoAspect) { videoAspect = value; defaults.set(value.rawValue, forKey: Self.videoAspectKey) }
    public func setWifiQuality(_ value: RivuneNetworkQuality) { wifiQuality = value; defaults.set(value.rawValue, forKey: Self.wifiQualityKey) }
    public func setMobileQuality(_ value: RivuneNetworkQuality) { mobileQuality = value; defaults.set(value.rawValue, forKey: Self.mobileQualityKey) }
    public func setAutomaticallyShowStreams(_ value: Bool) { automaticallyShowStreams = value; defaults.set(value, forKey: Self.showStreamsKey) }
    public func setAutoSkipIntro(_ value: Bool) { autoSkipIntro = value; defaults.set(value, forKey: Self.skipIntroKey) }
    public func setAutoSkipRecap(_ value: Bool) { autoSkipRecap = value; defaults.set(value, forKey: Self.skipRecapKey) }
    public func setAutoSkipOutro(_ value: Bool) { autoSkipOutro = value; defaults.set(value, forKey: Self.skipOutroKey) }

    public func loadProfileSettings() {
        guard let client, let profile = activeProfile else {
            resetProfileSettings()
            return
        }
        let requestGeneration = beginSettingsOperation()
        let operationGeneration = generation
        settingsLoading = true
        settingsOperation = Task { [weak self] in
            do {
                let effective = try await client.effectiveProfileSettings(id: profile.id)
                guard let self, self.isCurrentSettings(requestGeneration, profileID: profile.id) else { return }
                self.profileSettings = effective.settings
                self.profileSettingsSources = effective.sources
                self.settingsLoading = false
            } catch is CancellationError {
            } catch {
                guard let self, self.isCurrentSettings(requestGeneration, profileID: profile.id) else { return }
                if await self.recoverSessionIfNeeded(error, using: client, generation: operationGeneration) { return }
                self.settingsLoading = false
                self.settingsFailure = self.map(error, fallback: .message("Profile settings could not be loaded."))
            }
        }
    }

    public func updateProfileSettings(_ patch: ProfileSettingsPatch) {
        guard let client, let profile = activeProfile, profile.canManage else { return }
        let requestGeneration = beginSettingsOperation()
        let operationGeneration = generation
        settingsLoading = true
        settingsOperation = Task { [weak self] in
            do {
                _ = try await client.updateProfileSettings(id: profile.id, patch: patch)
                let effective = try await client.effectiveProfileSettings(id: profile.id)
                guard let self, self.isCurrentSettings(requestGeneration, profileID: profile.id) else { return }
                self.profileSettings = effective.settings
                self.profileSettingsSources = effective.sources
                self.settingsLoading = false
            } catch is CancellationError {
            } catch {
                guard let self, self.isCurrentSettings(requestGeneration, profileID: profile.id) else { return }
                if await self.recoverSessionIfNeeded(error, using: client, generation: operationGeneration) { return }
                self.settingsLoading = false
                self.settingsFailure = self.map(error, fallback: .message("Profile settings could not be saved."))
            }
        }
    }

    public func openMedia(_ target: RivuneMediaTarget) {
        guard let client else { return }
        let autoplayAddonID = pendingEpisodeAutoplay.flatMap { pending in
            pending.targetID == target.id ? pending.sourceAddonID : nil
        }
        pendingEpisodeAutoplay = nil
        beginMediaOperation()
        let current = mediaGeneration
        mediaLoading = true
        mediaFailure = nil
        mediaDetail = nil
        selectedSeason = nil
        seasonTrailers = []
        episodeProgress = [:]
        seriesEpisodes = []
        seriesEpisodesWatched = nil
        previousMediaDetail = nil
        previousSeriesState = nil
        playbackSources = []
        showPlaybackSources = false
        mediaOperation = Task { [weak self] in
            do {
                let titleID = try await Self.resolve(target, using: client)
                async let progressValue = try? client.playbackProgress(titleId: titleID)
                async let libraryValue = try? client.library(page: 1, pageSize: 100)
                async let trailersValue = (target.mediaType == "movie" || target.mediaType == "series") ? (try? client.trailers(titleId: titleID).trailers) : []
                let movie = target.mediaType == "movie" ? try? await client.movie(id: titleID) : nil
                var series: Series?
                if target.mediaType == "series" {
                    series = try? await client.series(id: titleID, mappingProvider: .tmdb)
                    if series == nil { series = try? await client.series(id: titleID, mappingProvider: .tvdb) }
                }
                var seriesWatchState: SeriesWatchState?
                if let series {
                    do {
                        seriesWatchState = try await Self.loadSeriesWatchState(series, using: client)
                    } catch is CancellationError {
                        throw CancellationError()
                    } catch {
                        seriesWatchState = nil
                    }
                }
                var episode: Episode?
                var episodeSeason: Season?
                var parentSeries: Series?
                if target.mediaType == "episode", let seriesID = target.seriesId, let seasonID = target.seasonId {
                    parentSeries = try? await client.series(id: seriesID, mappingProvider: .tmdb)
                    if parentSeries == nil { parentSeries = try? await client.series(id: seriesID, mappingProvider: .tvdb) }
                    let provider = parentSeries?.mappingProvider ?? .tmdb
                    episodeSeason = try? await client.season(id: seasonID, mappingProvider: provider)
                    episode = episodeSeason?.episodes.first { $0.id == titleID }
                }
                let progress = await progressValue
                let library = await libraryValue
                let trailers = await trailersValue ?? []
                guard let self, self.isCurrentMedia(current) else { return }
                self.seriesEpisodes = seriesWatchState?.episodes ?? []
                self.episodeProgress = seriesWatchState?.progress ?? [:]
                self.seriesEpisodesWatched = seriesWatchState.map { state in
                    !state.episodes.isEmpty && state.episodes.allSatisfy { state.progress[$0.id]?.completed == true }
                }
                self.mediaDetail = RivuneMediaDetail(
                    target: target, titleId: titleID, movie: movie, series: series, episode: episode,
                    season: episodeSeason, parentSeries: parentSeries, progress: progress, trailers: trailers,
                    inLibrary: library?.items.contains { $0.titleId == titleID } == true
                )
                self.mediaLoading = false
                if target.mediaType != "series" {
                    if autoplayAddonID != nil {
                        self.loadPlaybackSources(autoplayAddonID: autoplayAddonID)
                    } else if self.automaticallyShowStreams {
                        self.loadPlaybackSources()
                    }
                }
            } catch is CancellationError {
            } catch {
                guard let self, self.isCurrentMedia(current) else { return }
                self.mediaLoading = false
                self.mediaFailure = self.map(error, fallback: .contentLoad)
            }
        }
    }

    public func openMedia(_ item: CollectionItem) { openMedia(Self.target(from: item)) }
    public func openMedia(_ item: RivuneAPI.LibraryItem) { openMedia(Self.target(from: item)) }
    public func openMedia(_ item: ContinueWatchingItem) { openMedia(Self.target(from: item)) }
    public func openMedia(_ item: CalendarEvent) { openMedia(Self.target(from: item)) }

    public func openSeason(_ summary: SeasonSummary) {
        guard let client, let detail = mediaDetail else { return }
        beginMediaOperation()
        let current = mediaGeneration
        mediaLoading = true
        mediaFailure = nil
        mediaOperation = Task { [weak self] in
            do {
                async let trailersValue = try? client.trailers(titleId: detail.titleId, seasonNumber: summary.seasonNumber).trailers
                let season = try await client.season(id: summary.id, mappingProvider: detail.series?.mappingProvider ?? .tmdb)
                let progress = try? await client.playbackProgressBatch(titleIds: season.episodes.map(\.id))
                let trailers = await trailersValue ?? []
                guard let self, self.isCurrentMedia(current) else { return }
                self.selectedSeason = season
                self.seasonTrailers = trailers
                self.episodeProgress = Dictionary(uniqueKeysWithValues: (progress?.items ?? []).compactMap { item in
                    item.progress.map { (item.titleId, $0) }
                })
                self.mediaLoading = false
            } catch is CancellationError {
            } catch {
                guard let self, self.isCurrentMedia(current) else { return }
                self.mediaLoading = false
                self.mediaFailure = self.map(error, fallback: .contentLoad)
            }
        }
    }

    public func toggleLibrary() {
        guard let client, var detail = mediaDetail, detail.target.mediaType != "episode", !mediaActionLoading else { return }
        mediaActionLoading = true
        mediaFailure = nil
        Task { [weak self] in
            do {
                if detail.inLibrary { try await client.removeLibraryTitle(id: detail.titleId) }
                else { _ = try await client.addLibraryTitle(id: detail.titleId) }
                guard let self, self.mediaDetail?.titleId == detail.titleId else { return }
                detail.inLibrary.toggle()
                self.mediaDetail = detail
                self.libraryItems = []
                self.mediaActionLoading = false
            } catch {
                guard let self else { return }
                self.mediaActionLoading = false
                self.mediaFailure = self.map(error, fallback: .message("The library could not be updated."))
            }
        }
    }

    public func toggleWatched() {
        guard let client, var detail = mediaDetail, !mediaActionLoading else { return }
        let titleID = detail.titleId
        let season = selectedSeason
        let isSeriesTarget = season == nil && detail.target.mediaType == "series"
        let series = isSeriesTarget ? detail.series : nil
        let initialEpisodes: [Episode]? = season?.episodes ?? (isSeriesTarget ? seriesEpisodes : nil)
        let initialSeriesWatched = isSeriesTarget ? seriesEpisodesWatched : nil
        guard season?.episodes.isEmpty != true else { return }
        mediaActionLoading = true
        mediaFailure = nil
        Task { [weak self] in
            do {
                if var episodes = initialEpisodes {
                    var previousProgress = self?.episodeProgress ?? [:]
                    if isSeriesTarget, episodes.isEmpty {
                        guard let series else { throw RivuneAPIError.invalidResponse }
                        let state = try await Self.loadSeriesWatchState(series, using: client)
                        episodes = state.episodes
                        previousProgress = state.progress
                    }
                    guard !episodes.isEmpty else {
                        self?.mediaActionLoading = false
                        return
                    }
                    let completed = initialSeriesWatched
                        ?? episodes.allSatisfy { previousProgress[$0.id]?.completed == true }
                    var updatedProgress = previousProgress
                    for start in stride(from: 0, to: episodes.count, by: 100) {
                        let end = min(start + 100, episodes.count)
                        let result = try await client.setTitlesWatchedBatch(episodes[start..<end].map {
                            SetWatchedBatchItem(
                                titleId: $0.id,
                                completed: !completed,
                                expectedVersion: updatedProgress[$0.id]?.version ?? 0
                            )
                        })
                        for item in result.items {
                            updatedProgress[item.titleId] = item.progress
                        }
                        if let self, self.mediaDetail?.titleId == titleID {
                            self.episodeProgress = updatedProgress
                        }
                    }
                    guard let self, self.mediaDetail?.titleId == titleID else { return }
                    self.episodeProgress = updatedProgress
                    if isSeriesTarget {
                        self.seriesEpisodes = episodes
                        self.seriesEpisodesWatched = !completed
                    }
                    self.mediaActionLoading = false
                    return
                }
                let expected = detail.progress?.version ?? 0
                let progress: PlaybackProgress
                if detail.progress?.completed == true {
                    progress = try await client.markTitleUnwatched(titleId: detail.titleId, expectedVersion: expected)
                } else {
                    progress = try await client.markTitleWatched(titleId: detail.titleId, expectedVersion: expected)
                }
                guard let self, self.mediaDetail?.titleId == titleID else { return }
                detail.progress = progress
                self.mediaDetail = detail
                self.mediaActionLoading = false
            } catch {
                guard let self, self.mediaDetail?.titleId == titleID else { return }
                self.mediaActionLoading = false
                self.mediaFailure = self.map(error, fallback: .message("The watched state could not be updated."))
            }
        }
    }

    public func openEpisode(_ episode: Episode) {
        guard let detail = mediaDetail else { return }
        let series = detail.series ?? detail.parentSeries
        let priorSeriesState = RivuneSeriesNavigationState(
            episodes: seriesEpisodes,
            progress: episodeProgress,
            watched: seriesEpisodesWatched
        )
        let target = Self.episodeTarget(episode, series: series, source: detail.target)
        openMedia(target)
        previousMediaDetail = detail
        previousSeriesState = priorSeriesState
    }

    public func closeMedia() {
        beginMediaOperation()
        if let previousMediaDetail {
            mediaDetail = previousMediaDetail
            self.previousMediaDetail = nil
            if let previousSeriesState {
                seriesEpisodes = previousSeriesState.episodes
                episodeProgress = previousSeriesState.progress
                seriesEpisodesWatched = previousSeriesState.watched
            }
            previousSeriesState = nil
        } else {
            mediaDetail = nil
            seriesEpisodes = []
            episodeProgress = [:]
            seriesEpisodesWatched = nil
        }
        selectedSeason = nil
        mediaFailure = nil
        playbackSources = []
        showPlaybackSources = false
    }

    public func closeSeason() { selectedSeason = nil }
    public func loadPlaybackSources() { loadPlaybackSources(autoplayAddonID: nil) }

    private func loadPlaybackSources(autoplayAddonID: UUID?) {
        guard let client, let detail = mediaDetail else { return }
        beginMediaOperation()
        let current = mediaGeneration
        mediaLoading = true
        mediaFailure = nil
        showPlaybackSources = autoplayAddonID == nil
        let capabilities = Self.playbackCapabilities(for: currentQuality, player: preferredPlayer, embedded: embeddedPlayerPreference)
        mediaOperation = Task { [weak self] in
            do {
                let result = try await client.playbackSources(mediaType: detail.target.mediaType, addonId: detail.target.playbackAddonId, resourceId: detail.target.resourceId, capabilities: capabilities)
                guard let self, self.isCurrentMedia(current) else { return }
                self.playbackSources = result.sources
                self.mediaLoading = false
                if let autoplayAddonID {
                    guard let source = result.sources.first(where: { $0.addonId == autoplayAddonID }) ?? result.sources.first else {
                        self.mediaFailure = .message("No compatible playback source is available.")
                        return
                    }
                    self.play(source, externally: false)
                }
            } catch is CancellationError {
            } catch {
                guard let self, self.isCurrentMedia(current) else { return }
                self.mediaLoading = false
                self.mediaFailure = self.map(error, fallback: .message("No compatible playback source is available."))
            }
        }
    }

    public func download(_ source: PlaybackSourceOption) {
        guard let client, let detail = mediaDetail, !offlineDownloadActive,
              let selectedSource = playbackSources.first(where: {
                  $0.id == source.id && $0.sourceRef == source.sourceRef
              }) else { return }
        guard !["hls", "dash"].contains(selectedSource.protocol.lowercased()) else {
            mediaFailure = .message(RivuneOfflineMediaError.unsupportedSource.localizedDescription)
            return
        }
        guard let scope = offlineScope else {
            showPlaybackSources = false
            requestActiveOfflineUnlockIfAvailable()
            return
        }
        beginMediaOperation()
        let current = mediaGeneration
        let sourceIdentity = OfflineDownloadSourceIdentity(selectedSource)
        offlineDownloadSourceIdentity = sourceIdentity
        offlineDownloadActive = true
        offlineDownloadBytes = 0
        mediaFailure = nil
        mediaOperation = Task { [weak self] in
            guard let self else { return }
            var playbackSessionID: UUID?
            defer {
                if let playbackSessionID {
                    Task.detached(priority: .utility) {
                        try? await client.stopPlayback(sessionId: playbackSessionID)
                    }
                }
                if self.mediaGeneration == current,
                   self.offlineDownloadSourceIdentity == sourceIdentity {
                    self.offlineDownloadSourceIdentity = nil
                    self.offlineDownloadActive = false
                }
            }
            do {
                _ = try await client.preparePlayback(sourceRef: selectedSource.sourceRef, externalPlayer: true)
                let session = try await client.resolvePlayback(
                    sourceRef: selectedSource.sourceRef,
                    titleId: detail.titleId.uuidString.lowercased(),
                    externalPlayer: true
                )
                playbackSessionID = session.id
                guard let resolvedSource = session.sources.first(where: { $0.id == session.selectedSourceId }) ?? session.sources.first,
                      let rawURL = resolvedSource.url, self.isCurrentMedia(current),
                      let url = self.resolvedResourceURL(rawURL),
                      ["http", "https"].contains(url.scheme?.lowercased() ?? ""),
                      !["hls", "dash"].contains(resolvedSource.protocol.lowercased()) else {
                    throw RivuneOfflineMediaError.unsupportedSource
                }
                let item = try await RivuneOfflineMediaStore.shared.download(
                    from: url, titleId: detail.titleId, title: detail.target.title,
                    container: resolvedSource.container ?? selectedSource.container,
                    posterURL: detail.target.posterUrl, in: scope
                ) { [weak self] bytes in
                    Task { @MainActor in
                        guard let self, self.mediaGeneration == current,
                              self.offlineDownloadSourceIdentity == sourceIdentity else { return }
                        self.offlineDownloadBytes = bytes
                    }
                }
                if let access = self.currentOfflineAccess, access.id == scope.identifier {
                    self.persistOfflineAccess(access)
                }
                guard self.isCurrentMedia(current), self.offlineScope == scope else { return }
                self.offlineItems.removeAll { $0.id == item.id }
                self.offlineItems.insert(item, at: 0)
            } catch is CancellationError {
            } catch {
                guard self.isCurrentMedia(current) else { return }
                self.mediaFailure = self.map(error, fallback: .message(error.localizedDescription))
            }
        }
    }

    public func playOffline(_ item: RivuneOfflineMediaItem) {
        guard let scope = offlineScope else { return }
        Task { [weak self] in
            do {
                let url = try await RivuneOfflineMediaStore.shared.playbackURL(for: item, in: scope)
                guard let self, self.offlineScope == scope else { return }
                self.playbackPresentation = RivunePlaybackPresentation(
                    id: UUID(), sessionId: UUID(), sourceRef: "offline:\(item.id.uuidString)", titleId: item.titleId,
                    title: item.title, url: url, engine: .native, fallbackAllowed: true, startSeconds: 0,
                    markers: [], durationSeconds: nil, expectedVersion: 0, audioTracks: [], subtitles: [],
                    selectedAudioTrack: nil, selectedSubtitleId: nil, coordinatedItem: nil,
                    sourceAddonId: nil, nextEpisode: nil
                )
                self.diagnostics.record(.playbackStarted)
            } catch {
                self?.diagnostics.record(.playbackFailed)
                self?.mediaFailure = .message(error.localizedDescription)
            }
        }
    }

    public func removeOffline(_ item: RivuneOfflineMediaItem) {
        guard let scope = offlineScope else { return }
        Task { [weak self] in
            do {
                try await RivuneOfflineMediaStore.shared.remove(item, in: scope)
                guard let self, self.offlineScope == scope else { return }
                self.offlineItems.removeAll { $0.id == item.id }
                if self.offlineItems.isEmpty { self.removePersistedOfflineAccess(for: scope) }
            } catch { self?.mediaFailure = .message(error.localizedDescription) }
        }
    }

    public func handoffPlayback(to device: PlaybackDevice) {
        guard let client, let detail = mediaDetail else { return }
        let item = coordinatedItem(for: detail)
        Task { _ = try? await client.sendPlaybackCommand(sessionId: device.sessionId, input: PlaybackCommandInput(command: "load", item: item, positionMilliseconds: Int64((detail.progress?.positionSeconds ?? 0) * 1_000))) }
    }
    public func controlPlayback(on device: PlaybackDevice, command: String) {
        guard let client, ["play", "pause", "seek", "stop"].contains(command) else { return }
        let position = command == "seek" ? coordinationPositionMilliseconds : nil
        Task {
            _ = try? await client.sendPlaybackCommand(
                sessionId: device.sessionId,
                input: PlaybackCommandInput(command: command, positionMilliseconds: position)
            )
        }
    }


    public func createPlaybackRoom() {
        guard playbackCoordinationAvailable else { return }
        guard let client, let detail = mediaDetail else { return }
        Task { [weak self] in self?.activePlaybackRoom = try? await client.createPlaybackRoom(PlaybackRoomCreateInput(item: self?.coordinatedItem(for: detail) ?? CoordinatedPlaybackItem(titleId: detail.titleId), state: "paused", positionMilliseconds: Int64((detail.progress?.positionSeconds ?? 0) * 1_000), durationMilliseconds: Int64((detail.progress?.durationSeconds ?? 0) * 1_000))) }
    }

    public func joinPlaybackRoom(code: String) {
        guard playbackCoordinationAvailable else { return }
        guard let client, !code.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
        Task { [weak self] in
            guard let self, let room = try? await client.joinPlaybackRoom(code: code) else { return }
            self.activePlaybackRoom = room
            await self.startCoordinatedPlayback(room.item, positionMilliseconds: room.positionMilliseconds, using: client)
            if self.playbackPresentation == nil {
                self.activePlaybackRoom = nil
                try? await client.leavePlaybackRoom(id: room.id)
            }
        }
    }

    public func leavePlaybackRoom() {
        guard let client, let room = activePlaybackRoom else { return }
        activePlaybackRoom = nil
        pendingPlaybackCommands = []
        executedPlaybackCommandID = nil
        Task { try? await client.leavePlaybackRoom(id: room.id) }
    }

    public func consumePlaybackCommand() {
        guard !pendingPlaybackCommands.isEmpty else { return }
        let command = pendingPlaybackCommands.removeFirst()
        executedPlaybackCommandID = command.id
    }

    public func updateCoordinationPlayback(position: Double, duration: Double, playing: Bool) {
        coordinationPositionMilliseconds = Int64(max(position, 0) * 1_000)
        coordinationDurationMilliseconds = Int64(max(duration, 0) * 1_000)
        coordinationStatus = playing ? "playing" : "paused"
    }
    public func closePlaybackSources() { showPlaybackSources = false }

    public func play(_ source: PlaybackSourceOption, externally: Bool) {
        guard let client, let detail = mediaDetail else { return }
        let selection = RivunePlaybackEnginePolicy.selection(
            for: embeddedPlayerPreference,
            protocol: source.protocol,
            container: source.container
        )
        let preserveOriginalSource = RivunePlaybackEnginePolicy.preservesOriginalSource(for: selection, externally: externally)
        beginMediaOperation()
        let current = mediaGeneration
        mediaLoading = true
        mediaFailure = nil
        mediaOperation = Task { [weak self] in
            do {
                _ = try await client.preparePlayback(sourceRef: source.sourceRef, startSeconds: detail.progress?.positionSeconds, externalPlayer: preserveOriginalSource)
                let session = try await client.resolvePlayback(sourceRef: source.sourceRef, titleId: detail.titleId.uuidString.lowercased(), startSeconds: detail.progress?.positionSeconds, externalPlayer: preserveOriginalSource)
                guard let selected = session.sources.first(where: { $0.id == session.selectedSourceId }) ?? session.sources.first,
                      let rawURL = selected.url,
                      let self, self.isCurrentMedia(current), let url = self.resolvedResourceURL(rawURL) else { throw RivuneAPIError.invalidResponse }
                var markers: [PlaybackMarker] = []
                if detail.target.mediaType == "episode",
                   let imdb = (detail.parentSeries ?? detail.series)?.externalIds["imdb"],
                   let season = detail.target.seasonNumber,
                   let episode = detail.target.episodeNumber {
                    markers = (try? await client.playbackMarkers(imdbId: imdb, season: season, episode: episode).markers) ?? []
                }
                let nextEpisode = externally ? nil : await Self.nextEpisodeTarget(for: detail, using: client)
                self.mediaLoading = false
                self.showPlaybackSources = false
                if externally {
                    self.externalPlaybackSessionID = session.id
                    self.externalPlaybackURL = url
                } else {
                    self.playbackPresentation = RivunePlaybackPresentation(
                        id: UUID(), sessionId: session.id, sourceRef: source.sourceRef, titleId: detail.titleId,
                        title: detail.target.title, url: url, engine: selection.engine, fallbackAllowed: selection.fallbackAllowed,
                        startSeconds: detail.progress?.positionSeconds ?? 0, markers: markers,
                        durationSeconds: detail.progress?.durationSeconds, expectedVersion: detail.progress?.version ?? 0,
                        audioTracks: selected.media?.audioTracks ?? [], subtitles: session.subtitles,
                        selectedAudioTrack: session.selectedAudioTrack, selectedSubtitleId: session.selectedSubtitleId,
                        coordinatedItem: self.coordinatedItem(for: detail), sourceAddonId: source.addonId,
                        nextEpisode: nextEpisode
                    )
                }
                self.diagnostics.record(.playbackStarted)
            } catch {
                guard let self, self.isCurrentMedia(current) else { return }
                self.diagnostics.record(.playbackFailed)
                self.mediaLoading = false
                self.mediaFailure = self.map(error, fallback: .message("Playback could not be started."))
            }
        }
    }

    public func selectPlaybackOptions(audioTrack: Int?, subtitleId: String?, position: Int) {
        guard let client, let current = playbackPresentation, !playbackOptionLoading else { return }
        if current.selectedAudioTrack == audioTrack && current.selectedSubtitleId == subtitleId { return }
        playbackOptionLoading = true
        Task { [weak self] in
            do {
                let session = try await client.resolvePlayback(
                    sourceRef: current.sourceRef,
                    titleId: current.titleId.uuidString.lowercased(),
                    preferredAudioTrack: audioTrack,
                    preferredSubtitleId: subtitleId,
                    startSeconds: position,
                    externalPlayer: current.engine == .mpv || current.fallbackAllowed
                )
                guard let selected = session.sources.first(where: { $0.id == session.selectedSourceId }) ?? session.sources.first,
                      let rawURL = selected.url,
                      let self,
                      self.playbackPresentation?.id == current.id,
                      let url = self.resolvedResourceURL(rawURL) else { throw RivuneAPIError.invalidResponse }
                self.playbackPresentation = RivunePlaybackPresentation(
                    id: current.id, sessionId: session.id, sourceRef: current.sourceRef, titleId: current.titleId,
                    title: current.title, url: url, engine: current.engine, fallbackAllowed: current.fallbackAllowed,
                    startSeconds: position, markers: current.markers, durationSeconds: current.durationSeconds,
                    expectedVersion: current.expectedVersion, audioTracks: selected.media?.audioTracks ?? current.audioTracks,
                    subtitles: session.subtitles, selectedAudioTrack: session.selectedAudioTrack,
                    selectedSubtitleId: session.selectedSubtitleId, coordinatedItem: current.coordinatedItem,
                    sourceAddonId: current.sourceAddonId, nextEpisode: current.nextEpisode
                )
                self.playbackOptionLoading = false
                try? await client.stopPlayback(sessionId: current.sessionId)
            } catch {
                guard let self else { return }
                self.playbackOptionLoading = false
                self.mediaFailure = self.map(error, fallback: .message("The playback options could not be changed."))
            }
        }
    }

    public func fallbackPlaybackToMPV(position: Int, duration: Int) {
        guard let current = playbackPresentation, current.engine == .native, current.fallbackAllowed else { return }
        playbackPresentation = Self.movedToMPV(current, position: position, duration: duration)
    }

    public func fallbackMinimizedPlaybackToMPV(position: Int, duration: Int) {
        guard let current = minimizedPlaybackPresentation, current.engine == .native, current.fallbackAllowed else { return }
        minimizedPlaybackPresentation = Self.movedToMPV(current, position: position, duration: duration)
    }

    public func minimizePlayback(position: Int, duration: Int) {
        guard let current = playbackPresentation else { return }
        minimizedPlaybackPresentation = Self.resumedPresentation(current, position: position, duration: duration)
        playbackPresentation = nil
        beginMediaOperation()
        mediaDetail = nil
        previousMediaDetail = nil
        selectedSeason = nil
        seriesEpisodes = []
        episodeProgress = [:]
        seriesEpisodesWatched = nil
        previousSeriesState = nil
        playbackSources = []
        showPlaybackSources = false
    }

    public func resumeMinimizedPlayback(position: Int, duration: Int) {
        guard let current = minimizedPlaybackPresentation else { return }
        playbackPresentation = Self.resumedPresentation(current, position: position, duration: duration)
        minimizedPlaybackPresentation = nil
    }

    public func playbackFinished(position: Int, duration: Int, completed: Bool) {
        guard let presentation = playbackPresentation else { return }
        diagnostics.record(.playbackStopped)
        coordinationStatus = completed ? "ended" : "idle"
        coordinationPositionMilliseconds = Int64(max(position, 0) * 1_000)
        coordinationDurationMilliseconds = Int64(max(duration, 0) * 1_000)
        if completed { endActivePlaybackRoom(for: presentation) }
        playbackPresentation = nil
        finishPlayback(presentation, position: position, duration: duration, completed: completed)
        coordinationStatus = "idle"
        coordinationPositionMilliseconds = 0
        coordinationDurationMilliseconds = 0
    }

    public func playNextEpisode(position: Int, duration: Int) {
        guard let presentation = playbackPresentation, let nextEpisode = presentation.nextEpisode else { return }
        pendingEpisodeAutoplay = PendingEpisodeAutoplay(targetID: nextEpisode.id, sourceAddonID: presentation.sourceAddonId)
        playbackFinished(position: position, duration: duration, completed: true)
        openMedia(nextEpisode)
    }

    public func playNextMinimizedEpisode(position: Int, duration: Int) {
        guard let presentation = minimizedPlaybackPresentation, let nextEpisode = presentation.nextEpisode else { return }
        pendingEpisodeAutoplay = PendingEpisodeAutoplay(targetID: nextEpisode.id, sourceAddonID: presentation.sourceAddonId)
        minimizedPlaybackFinished(position: position, duration: duration, completed: true)
        openMedia(nextEpisode)
    }

    private func endActivePlaybackRoom(for presentation: RivunePlaybackPresentation) {
        guard let client, let room = activePlaybackRoom, room.currentMemberIsHost,
              coordinationEndedPlaybackID != presentation.id else { return }
        coordinationEndedPlaybackID = presentation.id
        let input = PlaybackRoomUpdateInput(
            state: "ended",
            positionMilliseconds: coordinationPositionMilliseconds,
            durationMilliseconds: coordinationDurationMilliseconds,
            expectedVersion: room.version
        )
        Task { _ = try? await client.updatePlaybackRoom(id: room.id, input: input) }
    }

    public func minimizedPlaybackFinished(position: Int, duration: Int, completed: Bool) {
        guard let presentation = minimizedPlaybackPresentation else { return }
        diagnostics.record(.playbackStopped)
        minimizedPlaybackPresentation = nil
        finishPlayback(presentation, position: position, duration: duration, completed: completed)
    }

    private func finishPlayback(_ presentation: RivunePlaybackPresentation, position: Int, duration: Int, completed: Bool) {
        guard let client else { return }
        Task {
            _ = try? await client.updatePlaybackProgress(titleId: presentation.titleId, input: UpdatePlaybackProgressRequest(positionSeconds: position, durationSeconds: duration, completed: completed, expectedVersion: presentation.expectedVersion))
            try? await client.stopPlayback(sessionId: presentation.sessionId)
        }
    }

    nonisolated private static func movedToMPV(_ presentation: RivunePlaybackPresentation, position: Int, duration: Int) -> RivunePlaybackPresentation {
        RivunePlaybackPresentation(
            id: presentation.id, sessionId: presentation.sessionId, sourceRef: presentation.sourceRef,
            titleId: presentation.titleId, title: presentation.title, url: presentation.url,
            engine: .mpv, fallbackAllowed: false, startSeconds: max(position, 0),
            markers: presentation.markers, durationSeconds: max(duration, position),
            expectedVersion: presentation.expectedVersion, audioTracks: presentation.audioTracks,
            subtitles: presentation.subtitles, selectedAudioTrack: presentation.selectedAudioTrack,
            selectedSubtitleId: presentation.selectedSubtitleId, coordinatedItem: presentation.coordinatedItem,
            sourceAddonId: presentation.sourceAddonId, nextEpisode: presentation.nextEpisode
        )
    }

    nonisolated private static func resumedPresentation(_ presentation: RivunePlaybackPresentation, position: Int, duration: Int) -> RivunePlaybackPresentation {
        RivunePlaybackPresentation(
            id: presentation.id, sessionId: presentation.sessionId, sourceRef: presentation.sourceRef,
            titleId: presentation.titleId, title: presentation.title, url: presentation.url,
            engine: presentation.engine, fallbackAllowed: presentation.fallbackAllowed,
            startSeconds: position, markers: presentation.markers, durationSeconds: max(duration, position),
            expectedVersion: presentation.expectedVersion, audioTracks: presentation.audioTracks,
            subtitles: presentation.subtitles, selectedAudioTrack: presentation.selectedAudioTrack,
            selectedSubtitleId: presentation.selectedSubtitleId, coordinatedItem: presentation.coordinatedItem,
            sourceAddonId: presentation.sourceAddonId, nextEpisode: presentation.nextEpisode
        )
    }

    public func clearExternalPlaybackURL() {
        let sessionID = externalPlaybackSessionID
        externalPlaybackSessionID = nil
        externalPlaybackURL = nil
        if let client, let sessionID {
            Task.detached(priority: .utility) {
                try? await client.stopPlayback(sessionId: sessionID)
            }
        }
    }

    public var playbackQuality: RivuneNetworkQuality { usesCellularNetwork ? mobileQuality : wifiQuality }

    private var currentQuality: RivuneNetworkQuality { playbackQuality }

    public func resolvedResourceURL(_ value: String) -> URL? {
        guard let serverOrigin,
              let components = URLComponents(string: value),
              components.user == nil, components.password == nil,
              let resolved = URL(string: value, relativeTo: serverOrigin)?.absoluteURL else { return nil }
        if components.scheme == nil {
            guard components.host == nil, Self.origin(of: resolved) == serverOrigin else { return nil }
            return resolved
        }
        guard Self.origin(of: resolved) == serverOrigin || resolved.scheme?.lowercased() == "https" else { return nil }
        return resolved
    }

    public func folderArtworkURL(for folder: CollectionFolder) -> URL? {
        let value = folder.id.flatMap { folderArtworkURLs[$0] } ?? folder.coverImageUrl
        return value.flatMap(resolvedResourceURL)
    }

    private func connectNow(_ value: String, generation: UInt64) async {
        guard let url = URL(string: value), url.scheme != nil, url.host != nil else {
            diagnostics.record(.serverConnectionFailed)
            finishFailure(.invalidServer, generation: generation)
            return
        }
        do {
            let candidate = try RivuneAPIClient(serverURL: url)
            let discovery = try await candidate.discover()
            guard isCurrent(generation) else { return }
            guard !discovery.setupRequired else {
                diagnostics.record(.serverConnectionFailed)
                finishFailure(.setupRequired, generation: generation)
                return
            }
            client = candidate
            serverOrigin = Self.origin(of: url)
            serverName = discovery.name
            serverVersion = discovery.serverVersion
            serverProtocolVersion = discovery.protocolVersion
            diagnostics.record(.serverConnectionSucceeded)
            playbackCoordinationAvailable = discovery.supports(.playbackCoordination)
            localRecommendationsAvailable = discovery.supports(.localRecommendations)
            defaults.set(value, forKey: Self.serverKey)
            if try await candidate.restoreSession() {
                try await routeAuthenticated(candidate, generation: generation)
            } else {
                destination = .pairing
                await beginPairing(generation: generation)
            }
        } catch {
            diagnostics.record(.serverConnectionFailed)
            if let client, await recoverSessionIfNeeded(error, using: client, generation: generation) { return }
            finishFailure(map(error, fallback: .serverUnreachable), generation: generation)
        }
    }

    private func beginPairing(generation: UInt64) async {
        guard let client, isCurrent(generation) else { return }
        do {
            let authorization = try await client.beginDeviceAuthorization(
                deviceName: Self.deviceName,
                platform: Self.platformName
            )
            guard isCurrent(generation) else { return }
            pairingCode = authorization.userCode
            verificationURL = URL(string: authorization.verificationUriComplete)
                ?? URL(string: authorization.verificationUri)
            isBusy = false
            await poll(authorization, using: client, generation: generation)
        } catch is CancellationError {
        } catch {
            finishFailure(map(error, fallback: .pairingFailed), generation: generation)
        }
    }

    private func poll(
        _ authorization: DeviceAuthorizationResponse,
        using client: RivuneAPIClient,
        generation: UInt64
    ) async {
        var interval = max(authorization.intervalSeconds, 1)
        let expiration = ISO8601DateFormatter().date(from: authorization.expiresAt)
        while isCurrent(generation), expiration.map({ Date() < $0 }) ?? true {
            do {
                try await Task.sleep(nanoseconds: UInt64(interval) * 1_000_000_000)
                _ = try await client.exchangeDeviceAuthorization(deviceCode: authorization.deviceCode)
                guard isCurrent(generation) else { return }
                pairingAccepted = true
                failure = nil
                try await Task.sleep(nanoseconds: 700_000_000)
                try await routeAuthenticated(client, generation: generation)
                return
            } catch is CancellationError {
                return
            } catch RivuneAPIError.server(_, let code, _) {
                switch code {
                case "authorization_pending": continue
                case "slow_down": interval += 5
                case "expired_device_code":
                    finishFailure(.pairingExpired, generation: generation, clearPairing: true)
                    return
                case "device_quota_reached":
                    finishFailure(.deviceLimit, generation: generation, clearPairing: true)
                    return
                default:
                    finishFailure(.pairingFailed, generation: generation, clearPairing: true)
                    return
                }
            } catch URLError.notConnectedToInternet, URLError.cannotConnectToHost, URLError.networkConnectionLost {
                guard isCurrent(generation) else { return }
                failure = .serverUnreachable
            } catch {
                finishFailure(map(error, fallback: .pairingFailed), generation: generation, clearPairing: true)
                return
            }
        }
        finishFailure(.pairingExpired, generation: generation, clearPairing: true)
    }

    private func routeAuthenticated(_ client: RivuneAPIClient, generation: UInt64) async throws {
        let account = try await client.currentAccount()
        guard isCurrent(generation) else { return }
        guard account.session.authorizationScope == .category else {
            try? await client.logout()
            destination = .pairing
            await beginPairing(generation: generation)
            return
        }
        profiles = account.profiles
        profileAvatarData = [:]
        resetProfileSettings()
        guard !profiles.isEmpty else {
            destination = .profiles
            isBusy = false
            failure = .noProfiles
            return
        }
        if let activeID = account.session.activeProfile?.id,
           let active = profiles.first(where: { $0.id == activeID && $0.accessible }) {
            activeProfile = active
            registerOfflineProfile(active, serverOrigin: serverOrigin, pin: nil)
            loadProfileSettings()
            destination = .library
            await loadCollections(using: client, generation: generation)
        } else {
            activeProfile = nil
            destination = .profiles
            isBusy = false
            failure = nil
            await loadCustomProfileAvatars(using: client, profiles: profiles, generation: generation)
        }
    }

    private func loadCollections(using client: RivuneAPIClient, generation: UInt64) async {
        diagnostics.record(.catalogRefreshStarted)
        do {
            let loaded = try await client.collections()
            guard isCurrent(generation) else { return }
            folderArtworkURLs = [:]
            collections = loaded.sorted { lhs, rhs in
                lhs.position == rhs.position ? lhs.title.localizedStandardCompare(rhs.title) == .orderedAscending : lhs.position < rhs.position
            }
            await loadFolderArtwork(using: client, collections: collections, generation: generation)
            await loadHomeSupplement(using: client, collections: collections, generation: generation)
            if playbackCoordinationAvailable { startCoordination(using: client) }
            guard isCurrent(generation) else { return }
            isBusy = false
            failure = nil
            diagnostics.record(.catalogRefreshSucceeded)
        } catch is CancellationError {
        } catch {
            guard isCurrent(generation) else { return }
            diagnostics.record(.catalogRefreshFailed)
            if await recoverSessionIfNeeded(error, using: client, generation: generation) { return }
            isBusy = false
            failure = map(error, fallback: .contentLoad)
        }
    }

    nonisolated private static func loadSeriesWatchState(
        _ series: Series,
        using client: RivuneAPIClient
    ) async throws -> SeriesWatchState {
        let summaries = series.seasons.filter { $0.episodeCount > 0 }
        var seasonsByIndex: [Int: Season] = [:]
        try await withThrowingTaskGroup(of: (Int, Season).self) { group in
            for (index, summary) in summaries.enumerated() {
                group.addTask {
                    (index, try await client.season(id: summary.id, mappingProvider: series.mappingProvider))
                }
            }
            for try await (index, season) in group {
                seasonsByIndex[index] = season
            }
        }

        var seen = Set<UUID>()
        let episodes = summaries.indices
            .compactMap { seasonsByIndex[$0] }
            .flatMap(\.episodes)
            .filter { seen.insert($0.id).inserted }
        var progress: [UUID: PlaybackProgress] = [:]
        try await withThrowingTaskGroup(of: PlaybackProgressBatch.self) { group in
            for start in stride(from: 0, to: episodes.count, by: 100) {
                let end = min(start + 100, episodes.count)
                let titleIDs = episodes[start..<end].map(\.id)
                group.addTask {
                    try await client.playbackProgressBatch(titleIds: titleIDs)
                }
            }
            for try await batch in group {
                for item in batch.items {
                    if let itemProgress = item.progress {
                        progress[item.titleId] = itemProgress
                    }
                }
            }
        }
        return SeriesWatchState(episodes: episodes, progress: progress)
    }

    private func loadCustomProfileAvatars(
        using client: RivuneAPIClient,
        profiles: [Profile],
        generation: UInt64
    ) async {
        let customProfiles = profiles.filter { $0.avatar.kind == "custom" }
        guard !customProfiles.isEmpty else { return }
        var loaded: [UUID: Data] = [:]
        await withTaskGroup(of: (UUID, Data?).self) { group in
            var nextIndex = 0
            for _ in 0..<min(4, customProfiles.count) {
                let profile = customProfiles[nextIndex]
                nextIndex += 1
                group.addTask {
                    (profile.id, try? await client.profileAvatar(id: profile.id))
                }
            }
            while let (id, data) = await group.next() {
                if let data { loaded[id] = data }
                if nextIndex < customProfiles.count {
                    let profile = customProfiles[nextIndex]
                    nextIndex += 1
                    group.addTask {
                        (profile.id, try? await client.profileAvatar(id: profile.id))
                    }
                }
            }
        }
        guard isCurrent(generation), self.profiles.map(\.id) == profiles.map(\.id) else { return }
        profileAvatarData = loaded
    }

    private func loadFolderArtwork(
        using client: RivuneAPIClient,
        collections: [Collection],
        generation: UInt64
    ) async {
        let pending = collections.flatMap { collection in
            collection.folders.compactMap { folder -> (UUID, UUID)? in
                guard folder.coverImageUrl == nil, let folderID = folder.id else { return nil }
                return (collection.id, folderID)
            }
        }
        guard !pending.isEmpty else { return }
        var loaded: [UUID: String] = [:]
        await withTaskGroup(of: (UUID, String?).self) { group in
            var nextIndex = 0
            for _ in 0..<min(4, pending.count) {
                let (collectionID, folderID) = pending[nextIndex]
                nextIndex += 1
                group.addTask {
                    let resolved = try? await client.resolveCollectionFolder(
                        collectionId: collectionID,
                        folderId: folderID,
                        page: 1,
                        limit: 1
                    )
                    return (folderID, resolved.flatMap(Self.folderArtworkReference))
                }
            }
            while let (folderID, url) = await group.next() {
                if let url { loaded[folderID] = url }
                if nextIndex < pending.count {
                    let (collectionID, nextFolderID) = pending[nextIndex]
                    nextIndex += 1
                    group.addTask {
                        let resolved = try? await client.resolveCollectionFolder(
                            collectionId: collectionID,
                            folderId: nextFolderID,
                            page: 1,
                            limit: 1
                        )
                        return (nextFolderID, resolved.flatMap(Self.folderArtworkReference))
                    }
                }
            }
        }
        guard isCurrent(generation), self.collections.map(\.id) == collections.map(\.id) else { return }
        folderArtworkURLs = loaded
    }

    private func loadHomeSupplement(
        using client: RivuneAPIClient,
        collections: [Collection],
        generation: UInt64
    ) async {
        async let continuePage = try? client.continueWatching(limit: 24)
        async let recommendationPage: LocalRecommendationPage? = localRecommendationsAvailable
            ? (try? await client.localRecommendations(limit: 24))
            : nil
        let scope = offlineScope
        let candidates = Array(collections.filter(\.heroEnabled).flatMap { collection in
            collection.folders.compactMap { folder -> (collectionID: UUID, folderID: UUID, collectionBackdrop: String?)? in
                guard let folderID = folder.id else { return nil }
                return (collection.id, folderID, collection.backdropImageUrl)
            }
        }.prefix(12))
        var resolvedByIndex: [Int: ResolvedCollectionFolder] = [:]
        await withTaskGroup(of: (Int, ResolvedCollectionFolder?).self) { group in
            var nextIndex = 0
            for _ in 0..<min(4, candidates.count) {
                let index = nextIndex
                let candidate = candidates[index]
                nextIndex += 1
                group.addTask {
                    let resolved = try? await client.resolveCollectionFolder(
                        collectionId: candidate.collectionID,
                        folderId: candidate.folderID,
                        page: 1,
                        limit: 12
                    )
                    return (index, resolved)
                }
            }
            while let (index, resolved) = await group.next() {
                if let resolved { resolvedByIndex[index] = resolved }
                if nextIndex < candidates.count {
                    let scheduledIndex = nextIndex
                    let candidate = candidates[scheduledIndex]
                    nextIndex += 1
                    group.addTask {
                        let resolved = try? await client.resolveCollectionFolder(
                            collectionId: candidate.collectionID,
                            folderId: candidate.folderID,
                            page: 1,
                            limit: 12
                        )
                        return (scheduledIndex, resolved)
                    }
                }
            }
        }
        var heroes: [RivuneHeroItem] = []
        for index in candidates.indices {
            guard let resolved = resolvedByIndex[index] else { continue }
            for item in resolved.items where heroes.count < 12 {
                heroes.append(
                    RivuneHeroItem(
                        id: "\(item.mediaType):\(item.id)",
                        title: item.title,
                        backgroundUrl: [
                            item.backgroundUrl,
                            resolved.folder.heroBackdropUrl,
                            candidates[index].collectionBackdrop,
                            item.posterUrl,
                        ]
                        .compactMap { $0 }
                        .first { !$0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty },
                        logoUrl: [item.logoUrl, resolved.folder.titleLogoUrl]
                            .compactMap { $0 }
                            .first { !$0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty },
                        releaseInfo: item.releaseInfo,
                        target: Self.target(from: item)
                    )
                )
            }
            if heroes.count == 12 { break }
        }
        let watching = await continuePage?.items ?? []
        let recommendations = await recommendationPage?.items ?? []
        let stored: [RivuneOfflineMediaItem]
        if let scope { stored = await RivuneOfflineMediaStore.shared.items(in: scope) }
        else { stored = [] }
        guard isCurrent(generation), self.collections.map(\.id) == collections.map(\.id) else { return }
        var seen = Set<String>()
        heroItems = heroes.filter { seen.insert($0.id).inserted }
        continueWatchingItems = watching
        recommendationItems = recommendations.compactMap(Self.recommendationItem)
        offlineItems = stored
    }

    nonisolated private static func recommendationItem(_ recommendation: LocalRecommendation) -> RivuneRecommendationItem? {
        let item = recommendation.item
        guard let title = item.title, !title.isEmpty else { return nil }
        let target = RivuneMediaTarget(
            id: item.resourceId ?? item.id.uuidString, resourceId: item.resourceId ?? item.id.uuidString,
            mediaType: item.mediaType, title: title, titleId: item.id, provider: item.resourceProvider,
            externalId: nil, externalIds: item.providerIds, sourceAddonId: item.sourceAddonId,
            sourceCatalogId: nil, sourceName: nil, posterUrl: item.posterUrl, backgroundUrl: item.backgroundUrl,
            logoUrl: nil, overview: nil, releaseInfo: item.releaseInfo, released: nil,
            seriesId: nil, seasonId: nil, seasonNumber: nil, episodeNumber: nil, runtimeMinutes: nil
        )
        return RivuneRecommendationItem(id: item.id, reason: recommendation.reason, target: target)
    }

    private func coordinatedItem(for detail: RivuneMediaDetail) -> CoordinatedPlaybackItem {
        CoordinatedPlaybackItem(titleId: detail.titleId, mediaType: detail.target.mediaType, resourceId: detail.target.resourceId, sourceAddonId: detail.target.playbackAddonId, title: detail.target.title, posterUrl: detail.target.posterUrl)
    }

    private func startCoordination(using client: RivuneAPIClient) {
        coordinationOperation?.cancel()
        coordinationOperation = Task { [weak self] in
            while !Task.isCancelled {
                guard let self else { return }
                let activePresentation = self.playbackPresentation ?? self.minimizedPlaybackPresentation
                let item = activePresentation?.coordinatedItem
                let status = item == nil ? "idle" : self.coordinationStatus
                let input = PlaybackDeviceHeartbeatInput(capabilities: ["remote-control", "watch-room"], state: PlaybackDeviceState(status: status, item: item, positionMilliseconds: item == nil ? 0 : self.coordinationPositionMilliseconds, durationMilliseconds: item == nil ? 0 : self.coordinationDurationMilliseconds))
                _ = try? await client.updatePlaybackDevice(input)
                if let devices = try? await client.playbackDevices() { self.playbackDevices = devices.devices.filter { !$0.current } }
                if let commandID = self.executedPlaybackCommandID {
                    do {
                        try await client.acknowledgePlaybackCommand(commandID)
                        self.lastPlaybackCommandID = max(self.lastPlaybackCommandID, commandID)
                        self.executedPlaybackCommandID = nil
                    } catch {
                        // Retry the acknowledgement without executing the command a second time.
                    }
                }
                if self.executedPlaybackCommandID == nil, pendingPlaybackCommands.isEmpty,
                   let commands = try? await client.playbackCommands(after: self.lastPlaybackCommandID) {
                    for command in commands.commands {
                        if command.command == "load", let item = command.item {
                            await self.startCoordinatedPlayback(item, positionMilliseconds: command.positionMilliseconds ?? 0, using: client)
                            guard self.playbackPresentation != nil else { break }
                            do {
                                try await client.acknowledgePlaybackCommand(command.id)
                                self.lastPlaybackCommandID = max(self.lastPlaybackCommandID, command.id)
                            } catch { break }
                        } else if self.playbackPresentation != nil || self.minimizedPlaybackPresentation != nil {
                            self.pendingPlaybackCommands.append(command)
                            break
                        } else {
                            break
                        }
                    }
                }
                if let room = self.activePlaybackRoom {
                    do {
                        let refreshed: PlaybackRoom
                        if room.currentMemberIsHost, room.state != "ended", item?.titleId == room.item.titleId {
                            let update = PlaybackRoomUpdateInput(
                                state: self.coordinationStatus == "playing" ? "playing" : "paused",
                                positionMilliseconds: self.coordinationPositionMilliseconds,
                                durationMilliseconds: self.coordinationDurationMilliseconds,
                                expectedVersion: room.version
                            )
                            do {
                                refreshed = try await client.updatePlaybackRoom(id: room.id, input: update)
                            } catch {
                                refreshed = try await client.playbackRoom(id: room.id)
                            }
                        } else {
                            refreshed = try await client.playbackRoom(id: room.id)
                        }
                        self.activePlaybackRoom = refreshed.preservingJoinCode(from: room)
                    } catch let RivuneAPIError.server(status, _, _) where status == 403 || status == 404 {
                        self.activePlaybackRoom = nil
                    } catch {
                        // Keep the last room snapshot across transient network failures.
                    }
                }
                try? await Task.sleep(nanoseconds: 2_000_000_000)
            }
        }
    }

    private func startCoordinatedPlayback(_ item: CoordinatedPlaybackItem, positionMilliseconds: Int64, using client: RivuneAPIClient) async {
        let capabilities = Self.playbackCapabilities(for: currentQuality, player: preferredPlayer, embedded: embeddedPlayerPreference)
        do {
            let sources = try await client.playbackSources(mediaType: item.mediaType, addonId: item.sourceAddonId, resourceId: item.resourceId, capabilities: capabilities)
            guard let option = sources.sources.first else { throw RivuneAPIError.invalidResponse }
            let start = Int(max(positionMilliseconds, 0) / 1_000)
            let selection = RivunePlaybackEnginePolicy.selection(for: embeddedPlayerPreference, protocol: option.protocol, container: option.container)
            let preserve = selection.engine == .mpv || selection.fallbackAllowed
            _ = try await client.preparePlayback(sourceRef: option.sourceRef, startSeconds: start, externalPlayer: preserve)
            let progress = try? await client.playbackProgress(titleId: item.titleId)
            let session = try await client.resolvePlayback(sourceRef: option.sourceRef, titleId: item.titleId.uuidString.lowercased(), startSeconds: start, externalPlayer: preserve)
            guard let selected = session.sources.first(where: { $0.id == session.selectedSourceId }) ?? session.sources.first,
                  let rawURL = selected.url, let url = resolvedResourceURL(rawURL) else { throw RivuneAPIError.invalidResponse }
            playbackPresentation = RivunePlaybackPresentation(
                id: UUID(), sessionId: session.id, sourceRef: option.sourceRef, titleId: item.titleId,
                title: item.title, url: url, engine: selection.engine, fallbackAllowed: selection.fallbackAllowed,
                startSeconds: start, markers: [], durationSeconds: nil, expectedVersion: progress?.version ?? 0,
                audioTracks: selected.media?.audioTracks ?? [], subtitles: session.subtitles,
                selectedAudioTrack: session.selectedAudioTrack, selectedSubtitleId: session.selectedSubtitleId,
                coordinatedItem: item, sourceAddonId: option.addonId, nextEpisode: nil
            )
            diagnostics.record(.playbackStarted)
        } catch {
            diagnostics.record(.playbackFailed)
            playbackPresentation = nil
            mediaFailure = .message("Playback handoff could not be started.")
        }
    }
    nonisolated private static func folderArtworkReference(_ resolved: ResolvedCollectionFolder) -> String? {
        if let sourcePosters = resolved.sourcePosterUrls {
            for source in resolved.folder.sources {
                if let id = source.id?.uuidString.lowercased(),
                   let value = sourcePosters[id], !value.isEmpty { return value }
            }
            if let value = sourcePosters.values.first(where: { !$0.isEmpty }) { return value }
        }
        if let value = resolved.folder.coverImageUrl, !value.isEmpty { return value }
        if let value = resolved.items.first?.posterUrl, !value.isEmpty { return value }
        if let value = resolved.items.first?.backgroundUrl, !value.isEmpty { return value }
        return nil
    }

    private func recoverSessionIfNeeded(
        _ error: Error,
        using client: RivuneAPIClient,
        generation: UInt64
    ) async -> Bool {
        guard map(error, fallback: .message(error.localizedDescription)) == .sessionExpired,
              isCurrent(generation) else { return false }
        try? await client.logout()
        guard isCurrent(generation) else { return true }
        self.generation &+= 1
        lockOffline()
        let pairingGeneration = self.generation
        operation = nil
        pairingCode = nil
        verificationURL = nil
        pairingAccepted = false
        profiles = []
        profileAvatarData = [:]
        resetProfileSettings()
        activeProfile = nil
        collections = []
        folderArtworkURLs = [:]
        resetTabState()
        destination = .pairing
        failure = nil
        isBusy = true
        operation = Task { [weak self] in
            await self?.beginPairing(generation: pairingGeneration)
        }
        return true
    }

    private func map(_ error: Error, fallback: RivuneAppFailure) -> RivuneAppFailure {
        if let apiError = error as? RivuneAPIError {
            switch apiError {
            case .invalidServerURL: return .invalidServer

            case .incompatibleProtocol: return .incompatibleServer
            case .notAuthenticated: return .sessionExpired
            case .server(_, let code, let message):
                switch code {
                case "device_quota_reached": return .deviceLimit
                case "device_code_capacity", "rate_limited": return .pairingCapacity
                case "invalid_profile_pin": return .invalidPin
                case "profile_pin_rate_limited": return .pinRateLimited
                case "session_expired", "invalid_access_token": return .sessionExpired
                default: return .message(message)
                }
            default: return fallback
            }
        }
        if error is URLError { return .serverUnreachable }
        return fallback
    }
    nonisolated private static func searchItems(from result: AddonResourceResult) -> [RivuneSearchItem] {
        guard case .array(let metas)? = result.payload["metas"] else { return [] }
        return metas.enumerated().compactMap { index, value in
            guard case .object(let object) = value else { return nil }
            let title = jsonString(object["name"]) ?? jsonString(object["title"])
            guard let title, !title.isEmpty else { return nil }
            let resourceID = jsonString(object["id"]) ?? "\(index)"
            let mediaType = jsonString(object["type"]) ?? result.type
            return RivuneSearchItem(
                id: resourceID, resourceId: resourceID, mediaType: mediaType, title: title, titleId: nil,
                provider: nil, externalId: nil, externalIds: [:], sourceAddonId: result.addonId,
                sourceCatalogId: result.id, sourceName: nil,
                posterUrl: jsonString(object["poster"]) ?? jsonString(object["posterUrl"]),
                backgroundUrl: jsonString(object["background"]) ?? jsonString(object["backgroundUrl"]),
                logoUrl: jsonString(object["logo"]) ?? jsonString(object["logoUrl"]),
                overview: jsonString(object["description"]) ?? jsonString(object["overview"]),
                releaseInfo: jsonString(object["releaseInfo"]), released: jsonString(object["released"]),
                seriesId: nil, seasonId: nil, seasonNumber: nil, episodeNumber: nil, runtimeMinutes: nil
            )
        }
    }

    nonisolated private static func jsonString(_ value: JSONValue?) -> String? {
        guard case .string(let result) = value else { return nil }
        return result
    }
    nonisolated private static func target(from item: CollectionItem) -> RivuneMediaTarget {
        let addon = item.sources.first { $0.addonId != nil }
        return RivuneMediaTarget(
            id: item.id, resourceId: item.id, mediaType: item.mediaType, title: item.title, titleId: UUID(uuidString: item.id),
            provider: nil, externalId: nil, externalIds: item.externalIds, sourceAddonId: addon?.addonId,
            sourceCatalogId: addon?.catalogId, sourceName: addon?.title, posterUrl: item.posterUrl,
            backgroundUrl: item.backgroundUrl, logoUrl: item.logoUrl, overview: item.description, releaseInfo: item.releaseInfo,
            released: item.released, seriesId: nil, seasonId: nil, seasonNumber: nil, episodeNumber: nil, runtimeMinutes: nil
        )
    }

    nonisolated private static func target(from item: RivuneAPI.LibraryItem) -> RivuneMediaTarget {
        RivuneMediaTarget(
            id: item.resourceId ?? item.externalId ?? item.titleId.uuidString, resourceId: item.resourceId ?? item.externalId ?? item.titleId.uuidString,
            mediaType: item.mediaType.rawValue, title: item.title ?? "Untitled", titleId: item.titleId,
            provider: item.provider, externalId: item.externalId,
            externalIds: item.provider.flatMap { provider in item.externalId.map { [provider: $0] } } ?? [:],
            sourceAddonId: item.sourceAddonId, sourceCatalogId: item.sourceCatalogId, sourceName: item.sourceName,
            posterUrl: item.posterUrl, backgroundUrl: item.backgroundUrl, logoUrl: nil, overview: nil, releaseInfo: item.releaseInfo,
            released: nil, seriesId: nil, seasonId: nil, seasonNumber: nil, episodeNumber: nil, runtimeMinutes: nil
        )
    }

    nonisolated private static func target(from item: ContinueWatchingItem) -> RivuneMediaTarget {
        RivuneMediaTarget(
            id: item.titleId.uuidString, resourceId: item.resourceId ?? item.titleId.uuidString, mediaType: item.mediaType.rawValue,
            title: item.title ?? item.episodeTitle ?? "Untitled", titleId: item.titleId, provider: item.resourceProvider,
            externalId: nil, externalIds: [:], sourceAddonId: nil, sourceCatalogId: nil, sourceName: nil,
            posterUrl: item.posterUrl, backgroundUrl: item.episodeStillUrl ?? item.backgroundUrl, logoUrl: nil,
            overview: nil, releaseInfo: item.releaseInfo, released: item.episodeAirDate, seriesId: item.seriesId,
            seasonId: item.seasonId?.uuidString, seasonNumber: item.seasonNumber, episodeNumber: item.episodeNumber, runtimeMinutes: nil
        )
    }

    nonisolated private static func target(from item: CalendarEvent) -> RivuneMediaTarget {
        RivuneMediaTarget(
            id: item.id, resourceId: item.resourceId ?? item.titleId.uuidString, mediaType: item.mediaType,
            title: item.title, titleId: item.titleId, provider: item.resourceProvider, externalId: nil, externalIds: [:],
            sourceAddonId: nil, sourceCatalogId: nil, sourceName: nil, posterUrl: item.posterUrl, backgroundUrl: nil,
            logoUrl: nil, overview: nil, releaseInfo: item.releaseDate, released: item.releaseDate, seriesId: item.seriesId,
            seasonId: item.seasonId?.uuidString, seasonNumber: item.seasonNumber, episodeNumber: item.episodeNumber, runtimeMinutes: nil
        )
    }

    nonisolated static func episodeTarget(
        _ episode: Episode,
        series: Series?,
        source: RivuneMediaTarget
    ) -> RivuneMediaTarget {
        RivuneMediaTarget(
            id: episode.id.uuidString,
            resourceId: episodeResourceID(episode, series: series),
            mediaType: "episode",
            title: episode.name,
            titleId: episode.id,
            provider: nil,
            externalId: nil,
            externalIds: episode.externalIds,
            sourceAddonId: source.sourceAddonId,
            sourceCatalogId: source.sourceCatalogId,
            sourceName: source.sourceName,
            posterUrl: episode.stillUrl,
            backgroundUrl: episode.backdropUrl,
            logoUrl: nil,
            overview: episode.overview,
            releaseInfo: "S\(episode.seasonNumber) E\(episode.episodeNumber)",
            released: episode.airDate,
            seriesId: series?.id,
            seasonId: episode.seasonId,
            seasonNumber: episode.seasonNumber,
            episodeNumber: episode.episodeNumber,
            runtimeMinutes: episode.runtimeMinutes
        )
    }

    nonisolated static func resolveNextEpisodeTarget(
        series: Series,
        currentSeason: Season,
        currentEpisodeID: UUID,
        source: RivuneMediaTarget,
        loadSeason: @Sendable (String) async throws -> Season
    ) async throws -> RivuneMediaTarget? {
        guard let currentEpisodeIndex = currentSeason.episodes.firstIndex(where: { $0.id == currentEpisodeID }) else { return nil }
        if currentEpisodeIndex + 1 < currentSeason.episodes.count {
            return episodeTarget(currentSeason.episodes[currentEpisodeIndex + 1], series: series, source: source)
        }
        let orderedSeasons = series.seasons.sorted {
            $0.seasonNumber == $1.seasonNumber
                ? $0.id < $1.id
                : $0.seasonNumber < $1.seasonNumber
        }
        guard let currentSeasonIndex = orderedSeasons.firstIndex(where: { $0.id == currentSeason.id }) else { return nil }
        for summary in orderedSeasons.dropFirst(currentSeasonIndex + 1) where summary.episodeCount > 0 {
            if let episode = try await loadSeason(summary.id).episodes.first {
                return episodeTarget(episode, series: series, source: source)
            }
        }
        return nil
    }

    nonisolated private static func nextEpisodeTarget(
        for detail: RivuneMediaDetail,
        using client: RivuneAPIClient
    ) async -> RivuneMediaTarget? {
        guard let series = detail.parentSeries ?? detail.series,
              let season = detail.season,
              let episode = detail.episode else { return nil }
        return try? await resolveNextEpisodeTarget(
            series: series,
            currentSeason: season,
            currentEpisodeID: episode.id,
            source: detail.target
        ) { seasonID in
            try await client.season(id: seasonID, mappingProvider: series.mappingProvider)
        }
    }

    nonisolated private static func resolve(_ target: RivuneMediaTarget, using client: RivuneAPIClient) async throws -> UUID {
        if let titleID = target.titleId { return titleID }
        let mediaType: TitleMediaType
        switch target.mediaType { case "movie": mediaType = .movie; case "series": mediaType = .series; case "tv": mediaType = .tv; default: throw RivuneAPIError.invalidResponse }
        let preferredProvider = ["tmdb", "imdb", "tvdb", "trakt"].first { target.externalIds[$0]?.isEmpty == false }
        let namespace = target.id.split(separator: ":", maxSplits: 1).map(String.init)
        let provider = target.provider ?? (target.mediaType == "tv" ? "addon" : preferredProvider ?? (namespace.count == 2 ? namespace[0].lowercased() : nil)) ?? (target.id.hasPrefix("tt") ? "imdb" : "addon")
        let externalID = target.externalId ?? (target.mediaType == "tv" ? target.resourceId : preferredProvider.flatMap { target.externalIds[$0] }) ?? (namespace.count == 2 ? namespace[1] : target.id)
        return try await client.resolveTitle(TitleResolveInput(mediaType: mediaType, provider: provider, externalId: externalID, resourceId: target.resourceId, title: target.title, posterUrl: target.posterUrl, backgroundUrl: target.backgroundUrl, releaseInfo: target.releaseInfo, released: target.released, sourceAddonId: target.sourceAddonId, sourceCatalogId: target.sourceCatalogId, sourceName: target.sourceName)).titleId
    }

    nonisolated private static func episodeResourceID(_ episode: Episode, series: Series?) -> String {
        if let imdb = series?.externalIds["imdb"] { return "\(imdb):\(episode.seasonNumber):\(episode.episodeNumber)" }
        return episode.externalIds["imdb"] ?? episode.externalIds["tvdb"].map { "tvdb:\($0)" } ?? episode.id.uuidString
    }

    nonisolated static func playbackCapabilities(
        for quality: RivuneNetworkQuality,
        player: RivunePlayerPreference,
        embedded: RivuneEmbeddedPlayerPreference
    ) -> PlaybackCapabilities {
        let maximum: (Int?, Int?)
        switch quality { case .economy: maximum = (720, 5_000); case .balanced: maximum = (1080, 12_000); case .maximum, .automatic: maximum = (nil, nil) }
        let useMPV = player == .external || embedded != .native
        return PlaybackCapabilities(
            streamingProtocols: ["http", "hls", "dash"],
            containers: useMPV ? ["mp4", "mkv", "matroska", "avi", "mov", "flv", "ts", "m2ts", "mpegts", "webm", "ogv", "ogg", "3gp", "mpeg"] : ["mp4", "mov", "m4v", "mpegts"],
            videoCodecs: ["h264", "hevc"],
            audioCodecs: useMPV ? ["aac", "mp3", "mp2", "flac", "opus", "vorbis", "ac3", "eac3", "dts", "truehd", "alac", "wma"] : ["aac", "ac3", "eac3"],
            externalPlayers: ["apple_open_url"], processingModes: [.remux, .transcodeAudio, .transcode],
            maximumHeight: maximum.0, maximumVideoBitrateKbps: maximum.1,
            maximumAudioChannels: 8, subtitleModes: [.external, .burn]
        )
    }

    private func beginMediaOperation() {
        mediaGeneration &+= 1
        mediaOperation?.cancel()
        mediaOperation = nil
        offlineDownloadSourceIdentity = nil
        offlineDownloadActive = false
        offlineDownloadBytes = 0
    }

    private func registerOfflineProfile(_ profile: Profile, serverOrigin: URL?, pin: String?) {
        guard let serverOrigin,
              let scope = RivuneOfflineMediaScope(serverOrigin: serverOrigin, profileID: profile.id) else {
            lockOffline()
            return
        }
        if profile.hasPin, pin == nil {
            currentOfflineAccess = storedOfflineProfiles.first { $0.id == scope.identifier && $0.requiresPIN }
            guard let access = currentOfflineAccess else { return }
            Task { [weak self] in
                let items = await RivuneOfflineMediaStore.shared.items(in: scope)
                guard let self, self.activeProfile?.id == profile.id, self.offlineScope == nil,
                      !items.isEmpty else { return }
                self.pendingOfflineProfile = access
            }
            return
        }
        guard let access = try? RivuneOfflineProfileAccess(name: profile.name, scope: scope, pin: profile.hasPin ? pin : nil) else {
            lockOffline()
            return
        }
        currentOfflineAccess = access
        beginMediaOperation()
        offlineScope = scope
        pendingOfflineProfile = nil
        loadOfflineItems()
        Task { [weak self] in
            let items = await RivuneOfflineMediaStore.shared.items(in: scope)
            guard let self, self.offlineScope == scope, !items.isEmpty else { return }
            self.persistOfflineAccess(access)
        }
    }

    private func persistOfflineAccess(_ access: RivuneOfflineProfileAccess) {
        storedOfflineProfiles.removeAll { $0.id == access.id }
        storedOfflineProfiles.append(access)
        storedOfflineProfiles.sort { $0.name.localizedStandardCompare($1.name) == .orderedAscending }
        offlineProfiles.removeAll { $0.id == access.id }
        offlineProfiles.append(access)
        offlineProfiles.sort { $0.name.localizedStandardCompare($1.name) == .orderedAscending }
        if let data = try? JSONEncoder().encode(storedOfflineProfiles) {
            defaults.set(data, forKey: Self.offlineProfilesKey)
        }
    }

    private func removePersistedOfflineAccess(for scope: RivuneOfflineMediaScope) {
        storedOfflineProfiles.removeAll { $0.id == scope.identifier }
        offlineProfiles.removeAll { $0.id == scope.identifier }
        if let data = try? JSONEncoder().encode(storedOfflineProfiles) {
            defaults.set(data, forKey: Self.offlineProfilesKey)
        }
    }

    private func refreshAvailableOfflineProfiles() {
        let candidates = storedOfflineProfiles
        Task { [weak self] in
            var available: [RivuneOfflineProfileAccess] = []
            for access in candidates {
                guard let scope = access.scope else { continue }
                if !(await RivuneOfflineMediaStore.shared.items(in: scope)).isEmpty { available.append(access) }
            }
            guard let self else { return }
            self.offlineProfiles = available
        }
    }

    private func requestActiveOfflineUnlockIfAvailable() {
        guard let serverOrigin, let profile = activeProfile,
              let scope = RivuneOfflineMediaScope(serverOrigin: serverOrigin, profileID: profile.id),
              let access = currentOfflineAccess.flatMap({ $0.id == scope.identifier ? $0 : nil })
                ?? storedOfflineProfiles.first(where: { $0.id == scope.identifier }) else { return }
        requestOfflineUnlock(access)
    }

    private func loadOfflineItems() {
        guard let scope = offlineScope else {
            offlineItems = []
            return
        }
        offlineItems = []
        Task { [weak self] in
            let items = await RivuneOfflineMediaStore.shared.items(in: scope)
            guard let self, self.offlineScope == scope else { return }
            self.offlineItems = items
        }
    }


    private func beginTabOperation() {
        tabGeneration &+= 1
        tabOperation?.cancel()
        tabOperation = nil
    }

    private func isCurrentTab(_ value: UInt64) -> Bool {
        !Task.isCancelled && value == tabGeneration
    }
    private func isCurrentMedia(_ value: UInt64) -> Bool {
        !Task.isCancelled && value == mediaGeneration
    }


    private func resetTabState() {
        tabGeneration &+= 1
        tabOperation?.cancel()
        tabOperation = nil
        coordinationOperation?.cancel()
        coordinationOperation = nil
        playbackCoordinationAvailable = false
        localRecommendationsAvailable = false
        playbackDevices = []
        activePlaybackRoom = nil
        pendingPlaybackCommands = []
        lastPlaybackCommandID = 0
        coordinationStatus = "idle"
        coordinationPositionMilliseconds = 0
        coordinationDurationMilliseconds = 0
        coordinationEndedPlaybackID = nil
        executedPlaybackCommandID = nil
        selectedTab = startupTab
        searchQuery = ""
        searchItems = []
        searchPage = 0
        searchHasMore = false
        searchPartial = false
        searchDescriptors = []
        libraryItems = []
        libraryMediaType = nil
        libraryPage = 0
        libraryTotalPages = 0
        libraryTotalResults = 0
        calendarEvents = []
        calendarMonth = Calendar.current.dateInterval(of: .month, for: Date())?.start ?? Date()
        continueWatchingItems = []
        recommendationItems = []
        heroItems = []
        tabLoading = false
        tabFailure = nil
    }

    private func finishFailure(
        _ value: RivuneAppFailure,
        generation: UInt64,
        clearPairing: Bool = false
    ) {
        guard isCurrent(generation) else { return }
        beginMediaOperation()
        mediaDetail = nil
        previousMediaDetail = nil
        selectedSeason = nil
        seriesEpisodes = []
        episodeProgress = [:]
        seriesEpisodesWatched = nil
        previousSeriesState = nil
        playbackSources = []
        showPlaybackSources = false
        playbackPresentation = nil
        minimizedPlaybackPresentation = nil
        playbackOptionLoading = false
        externalPlaybackURL = nil
        externalPlaybackSessionID = nil
        if clearPairing { pairingCode = nil }
        isBusy = false
        failure = value
    }

    private func beginOperation() {
        generation &+= 1
        operation?.cancel()
        operation = nil
    }

    private func isCurrent(_ value: UInt64) -> Bool {
        !Task.isCancelled && value == generation
    }

    private func beginSettingsOperation() -> UInt64 {
        settingsGeneration &+= 1
        settingsOperation?.cancel()
        settingsOperation = nil
        settingsFailure = nil
        return settingsGeneration
    }

    private func isCurrentSettings(_ value: UInt64, profileID: UUID) -> Bool {
        !Task.isCancelled && value == settingsGeneration && activeProfile?.id == profileID
    }

    private func resetProfileSettings() {
        settingsGeneration &+= 1
        settingsOperation?.cancel()
        settingsOperation = nil
        profileSettings = nil
        profileSettingsSources = nil
        settingsLoading = false
        settingsFailure = nil
    }

    private func resetSessionState() {
        serverOrigin = nil
        serverName = "Rivune"
        serverVersion = nil
        serverProtocolVersion = nil
        pairingCode = nil
        verificationURL = nil
        pairingAccepted = false
        profiles = []
        profileAvatarData = [:]
        resetProfileSettings()
        activeProfile = nil
        collections = []
        folderArtworkURLs = [:]
        resetTabState()
        openedFolder = nil
        failure = nil
        beginMediaOperation()
        mediaDetail = nil
        previousMediaDetail = nil
        selectedSeason = nil
        seriesEpisodes = []
        episodeProgress = [:]
        seriesEpisodesWatched = nil
        previousSeriesState = nil
        playbackSources = []
        showPlaybackSources = false
        playbackPresentation = nil
        minimizedPlaybackPresentation = nil
        playbackOptionLoading = false
        externalPlaybackURL = nil
        externalPlaybackSessionID = nil
    }

    private static let serverKey = "rivune.server.origin"
    private static let offlineScopeKey = "rivune.offline.scope"
    private static let accentKey = "rivune.appearance.accent"
    private static let playerKey = "rivune.playback.player"
    private static let embeddedPlayerKey = "rivune.playback.embedded-player"
    private static let startupTabKey = "rivune.navigation.startup-tab"
    private static let animationKey = "rivune.appearance.animations"
    private static let frameRateKey = "rivune.playback.frame-rate"
    private static let videoAspectKey = "rivune.playback.aspect"
    private static let wifiQualityKey = "rivune.playback.wifi-quality"
    private static let offlineProfilesKey = "rivune.offline.profiles"
    private static let mobileQualityKey = "rivune.playback.mobile-quality"
    private static let showStreamsKey = "rivune.playback.show-streams"
    private static let skipIntroKey = "rivune.playback.skip-intro"
    private static let skipRecapKey = "rivune.playback.skip-recap"
    private static let skipOutroKey = "rivune.playback.skip-outro"
    private static let lastUpdateVersionKey = "rivune.update.last-successful-version"
    private static let lastNotifiedUpdateKey = "rivune.update.last-notified-version"
    private static let lastUpdateCheckKey = "rivune.update.last-successful-check"
    private static let updateCacheKey = "rivune.update.cached-result"

    private static func origin(of url: URL) -> URL? {
        guard var components = URLComponents(url: url, resolvingAgainstBaseURL: true),
              let scheme = components.scheme?.lowercased(), components.host != nil else { return nil }
        components.scheme = scheme
        if (scheme == "https" && components.port == 443) || (scheme == "http" && components.port == 80) {
            components.port = nil
        }
        components.user = nil
        components.password = nil
        components.path = ""
        components.query = nil
        components.fragment = nil
        return components.url
    }
    private static var deviceName: String {
#if canImport(UIKit)

        UIDevice.current.name
#elseif canImport(AppKit)
        Host.current().localizedName ?? "Mac"
#else
        "Apple device"
#endif
    }

    private static var platformName: String {
#if os(visionOS)
        "visionos"
#elseif os(tvOS)
        "tvos"
#elseif os(iOS)
        "ios"
#elseif os(macOS)
        "macos"
#else
        "apple"
#endif
    }
}
