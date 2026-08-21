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
    public var displayName: String { rawValue.capitalized }
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
    public var displayName: String { rawValue.capitalized }
}

public enum RivuneNetworkQuality: String, CaseIterable, Identifiable, Sendable {
    case automatic, economy, balanced, maximum
    public var id: String { rawValue }
    public var displayName: String { rawValue.capitalized }
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
}


public struct OpenedCollectionFolder: Identifiable, Equatable {
    public let id: UUID
    public let collectionID: UUID
    public let folder: CollectionFolder
    public let items: [CollectionItem]?
    public let page: Int
    public let hasMore: Bool
    public let errors: [CollectionSourceFailure]
}

public enum RivuneViewerTab: String, CaseIterable, Identifiable {
    case home, search, library, calendar

    public var id: String { rawValue }
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

@MainActor
public final class RivuneAppModel: ObservableObject {
    @Published public private(set) var destination: RivuneDestination = .server
    @Published public var serverAddress: String
    @Published public private(set) var serverName = "Rivune"
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
    @Published public private(set) var openedFolder: OpenedCollectionFolder?
    @Published public private(set) var selectedTab: RivuneViewerTab = .home
    @Published public var searchQuery = ""
    @Published public private(set) var searchItems: [RivuneSearchItem] = []
    @Published public private(set) var libraryItems: [RivuneAPI.LibraryItem] = []
    @Published public private(set) var calendarEvents: [CalendarEvent] = []
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
    @Published public private(set) var profileSettings: SettingsValues?
    @Published public private(set) var profileSettingsSources: EffectiveSettingsSources?
    @Published public private(set) var settingsLoading = false
    @Published public private(set) var settingsFailure: RivuneAppFailure?
    private let defaults: UserDefaults
    private var serverOrigin: URL?
    private var client: RivuneAPIClient?
    private var operation: Task<Void, Never>?
    private var tabOperation: Task<Void, Never>?
    private var tabGeneration: UInt64 = 0
    private var generation: UInt64 = 0
    private var mediaOperation: Task<Void, Never>?
    private var mediaGeneration: UInt64 = 0
    private var settingsOperation: Task<Void, Never>?
    private var settingsGeneration: UInt64 = 0
    private var previousMediaDetail: RivuneMediaDetail?
    private let pathMonitor = NWPathMonitor()
    private let pathMonitorQueue = DispatchQueue(label: "io.rivune.network-path")
    private var usesCellularNetwork = false
    public var canNavigateBackFromMedia: Bool { selectedSeason != nil || previousMediaDetail != nil }

    public init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
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
    }

    deinit {
        operation?.cancel()
        tabOperation?.cancel()
        mediaOperation?.cancel()
        settingsOperation?.cancel()
        pathMonitor.cancel()
    }

    public func start() {
        guard destination == .server, !serverAddress.isEmpty else { return }
        connect(to: serverAddress)
    }

    public func connect(to address: String) {
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

    public func search() {
        guard let client else { return }
        let query = searchQuery.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !query.isEmpty else {
            searchItems = []
            return
        }
        beginTabOperation()
        let currentGeneration = tabGeneration
        tabLoading = true
        tabFailure = nil
        tabOperation = Task { [weak self] in
            do {
                let descriptors = try await client.addonCatalogs()
                let types = Array(Set(descriptors.filter(\.searchable).map { $0.catalog.type })).sorted()
                var items: [RivuneSearchItem] = []
                try await withThrowingTaskGroup(of: AddonResourceBatch.self) { group in
                    for type in types {
                        group.addTask { try await client.searchAddonCatalogs(type: type, search: query, limit: 50) }
                    }
                    for try await batch in group {
                        for result in batch.results {
                            items.append(contentsOf: Self.searchItems(from: result))
                        }
                    }
                }
                guard let self, self.isCurrentTab(currentGeneration) else { return }
                var seen = Set<String>()
                self.searchItems = items.filter { seen.insert($0.id).inserted }
                self.tabLoading = false
            } catch is CancellationError {
            } catch {
                guard let self, self.isCurrentTab(currentGeneration) else { return }
                self.tabLoading = false
                self.tabFailure = self.map(error, fallback: .contentLoad)
            }
        }
    }

    private func loadPersonalLibrary() {
        guard let client else { return }
        beginTabOperation()
        let currentGeneration = tabGeneration
        tabLoading = true
        tabFailure = nil
        tabOperation = Task { [weak self] in
            do {
                var page = 1
                var loaded: [RivuneAPI.LibraryItem] = []
                while true {
                    let response = try await client.library(page: page, pageSize: 100)
                    loaded.append(contentsOf: response.items)
                    guard page < response.totalPages else { break }
                    page += 1
                }
                guard let self, self.isCurrentTab(currentGeneration) else { return }
                self.libraryItems = loaded
                self.tabLoading = false
            } catch is CancellationError {
            } catch {
                guard let self, self.isCurrentTab(currentGeneration) else { return }
                self.tabLoading = false
                self.tabFailure = self.map(error, fallback: .contentLoad)
            }
        }
    }

    private func loadCalendar() {
        guard let client else { return }
        beginTabOperation()
        let currentGeneration = tabGeneration
        tabLoading = true
        tabFailure = nil
        tabOperation = Task { [weak self] in
            do {
                var calendar = Calendar(identifier: .gregorian)
                calendar.timeZone = .current
                let now = Date()
                let from = calendar.date(byAdding: .day, value: -31, to: now) ?? now
                let to = calendar.date(byAdding: .day, value: 61, to: now) ?? now
                let formatter = DateFormatter()
                formatter.calendar = calendar
                formatter.locale = Locale(identifier: "en_US_POSIX")
                formatter.dateFormat = "yyyy-MM-dd"
                let events = try await client.calendar(from: formatter.string(from: from), to: formatter.string(from: to))
                guard let self, self.isCurrentTab(currentGeneration) else { return }
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
        openedFolder = OpenedCollectionFolder(id: folderID, collectionID: collection.id, folder: folder, items: nil, page: 0, hasMore: false, errors: [])
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
        defaults.removeObject(forKey: Self.serverKey)
        serverAddress = ""
        destination = .server
        isBusy = false
        guard let currentClient else { return }
        operation = Task { try? await currentClient.logout() }
    }

    public func clearFailure() { failure = nil }

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
        beginMediaOperation()
        let current = mediaGeneration
        mediaLoading = true
        mediaFailure = nil
        mediaDetail = nil
        selectedSeason = nil
        seasonTrailers = []
        episodeProgress = [:]
        previousMediaDetail = nil
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
                var episode: Episode?
                var parentSeries: Series?
                if target.mediaType == "episode", let seriesID = target.seriesId, let seasonID = target.seasonId {
                    parentSeries = try? await client.series(id: seriesID, mappingProvider: .tmdb)
                    if parentSeries == nil { parentSeries = try? await client.series(id: seriesID, mappingProvider: .tvdb) }
                    let provider = parentSeries?.mappingProvider ?? .tmdb
                    episode = try? await client.season(id: seasonID, mappingProvider: provider).episodes.first { $0.id == titleID }
                }
                let progress = await progressValue
                let library = await libraryValue
                let trailers = await trailersValue ?? []
                guard let self, self.isCurrentMedia(current) else { return }
                self.mediaDetail = RivuneMediaDetail(
                    target: target, titleId: titleID, movie: movie, series: series, episode: episode, parentSeries: parentSeries,
                    progress: progress, trailers: trailers, inLibrary: library?.items.contains { $0.titleId == titleID } == true
                )
                self.mediaLoading = false
                if target.mediaType != "series", self.automaticallyShowStreams { self.loadPlaybackSources() }
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
        mediaActionLoading = true
        mediaFailure = nil
        Task { [weak self] in
            do {
                if let season = self?.selectedSeason, !season.episodes.isEmpty {
                    let completed = season.episodes.allSatisfy { self?.episodeProgress[$0.id]?.completed == true }
                    let result = try await client.setTitlesWatchedBatch(season.episodes.map {
                        SetWatchedBatchItem(titleId: $0.id, completed: !completed, expectedVersion: self?.episodeProgress[$0.id]?.version ?? 0)
                    })
                    guard let self else { return }
                    self.episodeProgress = Dictionary(uniqueKeysWithValues: result.items.map { ($0.titleId, $0.progress) })
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
                guard let self, self.mediaDetail?.titleId == detail.titleId else { return }
                detail.progress = progress
                self.mediaDetail = detail
                self.mediaActionLoading = false
            } catch {
                guard let self else { return }
                self.mediaActionLoading = false
                self.mediaFailure = self.map(error, fallback: .message("The watched state could not be updated."))
            }
        }
    }

    public func openEpisode(_ episode: Episode) {
        guard let detail = mediaDetail else { return }
        let series = detail.series ?? detail.parentSeries
        let target = RivuneMediaTarget(
            id: episode.id.uuidString, resourceId: Self.episodeResourceID(episode, series: series), mediaType: "episode", title: episode.name,
            titleId: episode.id, provider: nil, externalId: nil, externalIds: episode.externalIds, sourceAddonId: detail.target.sourceAddonId,
            sourceCatalogId: detail.target.sourceCatalogId, sourceName: detail.target.sourceName, posterUrl: episode.stillUrl,
            backgroundUrl: episode.backdropUrl, logoUrl: nil, overview: episode.overview, releaseInfo: "S\(episode.seasonNumber) E\(episode.episodeNumber)",
            released: episode.airDate, seriesId: series?.id, seasonId: episode.seasonId, seasonNumber: episode.seasonNumber,
            episodeNumber: episode.episodeNumber, runtimeMinutes: episode.runtimeMinutes
        )
        openMedia(target)
        previousMediaDetail = detail
    }

    public func closeMedia() {
        beginMediaOperation()
        if let previousMediaDetail {
            mediaDetail = previousMediaDetail
            self.previousMediaDetail = nil
        } else {
            mediaDetail = nil
        }
        selectedSeason = nil
        mediaFailure = nil
        playbackSources = []
        showPlaybackSources = false
    }

    public func closeSeason() { selectedSeason = nil }
    public func loadPlaybackSources() {
        guard let client, let detail = mediaDetail else { return }
        beginMediaOperation()
        let current = mediaGeneration
        mediaLoading = true
        mediaFailure = nil
        showPlaybackSources = true
        let capabilities = Self.playbackCapabilities(for: currentQuality, player: preferredPlayer, embedded: embeddedPlayerPreference)
        mediaOperation = Task { [weak self] in
            do {
                let result = try await client.playbackSources(mediaType: detail.target.mediaType, addonId: detail.target.playbackAddonId, resourceId: detail.target.resourceId, capabilities: capabilities)
                guard let self, self.isCurrentMedia(current) else { return }
                self.playbackSources = result.sources
                self.mediaLoading = false
            } catch is CancellationError {
            } catch {
                guard let self, self.isCurrentMedia(current) else { return }
                self.mediaLoading = false
                self.mediaFailure = self.map(error, fallback: .message("No compatible playback source is available."))
            }
        }
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
                self.mediaLoading = false
                self.showPlaybackSources = false
                if externally {
                    self.externalPlaybackURL = url
                } else {
                    self.playbackPresentation = RivunePlaybackPresentation(
                        id: UUID(), sessionId: session.id, sourceRef: source.sourceRef, titleId: detail.titleId,
                        title: detail.target.title, url: url, engine: selection.engine, fallbackAllowed: selection.fallbackAllowed,
                        startSeconds: detail.progress?.positionSeconds ?? 0, markers: markers,
                        durationSeconds: detail.progress?.durationSeconds, expectedVersion: detail.progress?.version ?? 0,
                        audioTracks: selected.media?.audioTracks ?? [], subtitles: session.subtitles,
                        selectedAudioTrack: session.selectedAudioTrack, selectedSubtitleId: session.selectedSubtitleId
                    )
                }
            } catch {
                guard let self, self.isCurrentMedia(current) else { return }
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
                    selectedSubtitleId: session.selectedSubtitleId
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
        playbackPresentation = nil
        finishPlayback(presentation, position: position, duration: duration, completed: completed)
    }

    public func minimizedPlaybackFinished(position: Int, duration: Int, completed: Bool) {
        guard let presentation = minimizedPlaybackPresentation else { return }
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
            selectedSubtitleId: presentation.selectedSubtitleId
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
            selectedSubtitleId: presentation.selectedSubtitleId
        )
    }

    public func clearExternalPlaybackURL() { externalPlaybackURL = nil }

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
            finishFailure(.invalidServer, generation: generation)
            return
        }
        do {
            let candidate = try RivuneAPIClient(serverURL: url)
            let discovery = try await candidate.discover()
            guard isCurrent(generation) else { return }
            guard !discovery.setupRequired else {
                finishFailure(.setupRequired, generation: generation)
                return
            }
            client = candidate
            serverOrigin = Self.origin(of: url)
            serverName = discovery.name
            defaults.set(value, forKey: Self.serverKey)
            if try await candidate.restoreSession() {
                try await routeAuthenticated(candidate, generation: generation)
            } else {
                destination = .pairing
                await beginPairing(generation: generation)
            }
        } catch {
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
        do {
            let loaded = try await client.collections()
            guard isCurrent(generation) else { return }
            folderArtworkURLs = [:]
            collections = loaded.sorted { lhs, rhs in
                lhs.position == rhs.position ? lhs.title.localizedStandardCompare(rhs.title) == .orderedAscending : lhs.position < rhs.position
            }
            await loadFolderArtwork(using: client, collections: collections, generation: generation)
            await loadHomeSupplement(using: client, collections: collections, generation: generation)
            guard isCurrent(generation) else { return }
            isBusy = false
            failure = nil
        } catch is CancellationError {
        } catch {
            guard isCurrent(generation) else { return }
            if await recoverSessionIfNeeded(error, using: client, generation: generation) { return }
            isBusy = false
            failure = map(error, fallback: .contentLoad)
        }
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
        let candidates = Array(collections.filter(\.heroEnabled).flatMap { collection in
            collection.folders.compactMap { folder -> (UUID, UUID)? in
                guard let folderID = folder.id else { return nil }
                return (collection.id, folderID)
            }
        }.prefix(12))
        var heroes: [RivuneHeroItem] = []
        await withTaskGroup(of: ResolvedCollectionFolder?.self) { group in
            var nextIndex = 0
            for _ in 0..<min(4, candidates.count) {
                let candidate = candidates[nextIndex]
                nextIndex += 1
                group.addTask {
                    try? await client.resolveCollectionFolder(collectionId: candidate.0, folderId: candidate.1, page: 1, limit: 12)
                }
            }
            while let resolved = await group.next() {
                if let resolved {
                    for item in resolved.items where heroes.count < 12 {
                        heroes.append(
                            RivuneHeroItem(
                                id: "\(item.mediaType):\(item.id)",
                                title: item.title,
                                backgroundUrl: item.backgroundUrl ?? resolved.folder.heroBackdropUrl ?? item.posterUrl,
                                logoUrl: item.logoUrl ?? resolved.folder.titleLogoUrl,
                                releaseInfo: item.releaseInfo,
                                target: Self.target(from: item)
                            )
                        )
                    }
                }
                if nextIndex < candidates.count {
                    let candidate = candidates[nextIndex]
                    nextIndex += 1
                    group.addTask {
                        try? await client.resolveCollectionFolder(collectionId: candidate.0, folderId: candidate.1, page: 1, limit: 12)
                    }
                }
            }
        }
        let watching = await continuePage?.items ?? []
        guard isCurrent(generation), self.collections.map(\.id) == collections.map(\.id) else { return }
        var seen = Set<String>()
        heroItems = heroes.filter { seen.insert($0.id).inserted }
        continueWatchingItems = watching
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

    private func beginMediaOperation() { mediaGeneration &+= 1; mediaOperation?.cancel(); mediaOperation = nil }
    private func isCurrentMedia(_ value: UInt64) -> Bool { !Task.isCancelled && value == mediaGeneration }


    private func beginTabOperation() {
        tabGeneration &+= 1
        tabOperation?.cancel()
        tabOperation = nil
    }

    private func isCurrentTab(_ value: UInt64) -> Bool {
        !Task.isCancelled && value == tabGeneration
    }

    private func resetTabState() {
        tabGeneration &+= 1
        tabOperation?.cancel()
        tabOperation = nil
        selectedTab = startupTab
        searchQuery = ""
        searchItems = []
        libraryItems = []
        calendarEvents = []
        continueWatchingItems = []
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
        playbackSources = []
        showPlaybackSources = false
        playbackPresentation = nil
        minimizedPlaybackPresentation = nil
        playbackOptionLoading = false
        externalPlaybackURL = nil
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
        playbackSources = []
        showPlaybackSources = false
        playbackPresentation = nil
        minimizedPlaybackPresentation = nil
        playbackOptionLoading = false
        externalPlaybackURL = nil
    }

    private static let serverKey = "rivune.server.origin"
    private static let accentKey = "rivune.appearance.accent"
    private static let playerKey = "rivune.playback.player"
    private static let embeddedPlayerKey = "rivune.playback.embedded-player"
    private static let startupTabKey = "rivune.navigation.startup-tab"
    private static let animationKey = "rivune.appearance.animations"
    private static let frameRateKey = "rivune.playback.frame-rate"
    private static let videoAspectKey = "rivune.playback.aspect"
    private static let wifiQualityKey = "rivune.playback.wifi-quality"
    private static let mobileQualityKey = "rivune.playback.mobile-quality"
    private static let showStreamsKey = "rivune.playback.show-streams"
    private static let skipIntroKey = "rivune.playback.skip-intro"
    private static let skipRecapKey = "rivune.playback.skip-recap"
    private static let skipOutroKey = "rivune.playback.skip-outro"

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
