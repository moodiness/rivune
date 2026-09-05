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
  #if targetEnvironment(simulator)
    public static let embeddedMPVSupported = false
  #else
    public static let embeddedMPVSupported = true
  #endif

  public static func selection(
    for preference: RivuneEmbeddedPlayerPreference,
    protocol streamingProtocol: String? = nil,
    container: String? = nil,
    embeddedMPVSupported: Bool = embeddedMPVSupported
  ) -> RivunePlaybackEngineSelection {
    guard embeddedMPVSupported else {
      return RivunePlaybackEngineSelection(engine: .native, fallbackAllowed: false)
    }
    switch preference {
    case .native:
      return RivunePlaybackEngineSelection(engine: .native, fallbackAllowed: false)
    case .mpv:
      return RivunePlaybackEngineSelection(engine: .mpv, fallbackAllowed: false)
    case .automatic:
      let protocolName = streamingProtocol?.lowercased()
      let containerName = container?.lowercased()
      let nativeDirect =
        protocolName == "hls"
        || containerName.map { ["mp4", "mov", "m4v", "mpegts"].contains($0) } == true
      return RivunePlaybackEngineSelection(
        engine: nativeDirect ? .native : .mpv, fallbackAllowed: true)
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
    switch self {
    case .system: return "System"
    case .full: return "Full"
    case .reduced: return "Reduced"
    }
  }
}

public enum RivuneFrameRatePreference: String, CaseIterable, Identifiable, Sendable {
  case system, enabled, disabled
  public var id: String { rawValue }
  public var displayName: String {
    switch self {
    case .system: return "System"
    case .enabled: return "On"
    case .disabled: return "Off"
    }
  }
}

public enum RivuneVideoAspect: String, CaseIterable, Identifiable, Sendable {
  case fit, fill, zoom
  public var id: String { rawValue }
  public var displayName: String {
    switch self {
    case .fit: return "Fit"
    case .fill: return "Fill"
    case .zoom: return "Zoom"
    }
  }
}

public enum RivuneNetworkQuality: String, CaseIterable, Identifiable, Sendable {
  case automatic, economy, balanced, maximum
  public var id: String { rawValue }
  public var displayName: String {
    switch self {
    case .automatic: return "Automatic"
    case .economy: return "Economy"
    case .balanced: return "Balanced"
    case .maximum: return "Maximum"
    }
  }
}

public enum RivuneNetworkClass: String, CaseIterable, Sendable, Equatable {
  case local
  case remoteWifi = "remote_wifi"
  case mobile
}

public struct RivuneQualityLimit: Equatable, Sendable {
  public let maximumHeight: Int?
  public let maximumVideoBitrateKbps: Int?
}

public enum RivuneNetworkQualityPolicy {
  public static func limit(
    quality: RivuneNetworkQuality, networkClass: RivuneNetworkClass
  ) -> RivuneQualityLimit {
    switch quality {
    case .economy:
      return RivuneQualityLimit(maximumHeight: 480, maximumVideoBitrateKbps: 2_000)
    case .balanced:
      return RivuneQualityLimit(maximumHeight: 1080, maximumVideoBitrateKbps: 8_000)
    case .maximum:
      return RivuneQualityLimit(maximumHeight: nil, maximumVideoBitrateKbps: nil)
    case .automatic:
      return networkClass == .mobile
        ? RivuneQualityLimit(maximumHeight: 720, maximumVideoBitrateKbps: 5_000)
        : RivuneQualityLimit(maximumHeight: nil, maximumVideoBitrateKbps: nil)
    }
  }
}

public enum RivuneRecommendationLayout: String, CaseIterable, Identifiable, Sendable {
  case portrait, landscape

  public var id: String { rawValue }
  public var displayName: String {
    switch self {
    case .portrait: return "Portrait"
    case .landscape: return "Landscape"
    }
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
  public let mappingProvider: SeriesMappingProvider?
  public let episodeOrderId: String?
  public let metadataSeasonId: String?
  public let seasonId: String?
  public let seasonNumber: Int?
  public let episodeNumber: Int?
  public let runtimeMinutes: Int?
  var searchPresentationID: String? = nil
}

extension RivuneMediaTarget {
  var playbackAddonId: UUID? { mediaType == "tv" ? sourceAddonId : nil }
  var stableSearchPresentationID: String { searchPresentationID ?? id }
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
  public let timelineStartSeconds: Int
  public let mediaTimeline: PlaybackMediaTimeline?
  public var videoAspect: RivuneVideoAspect
  public var playbackSpeed: Double
  public let markers: [PlaybackMarker]
  public let durationSeconds: Int?
  public let expectedVersion: Int64
  public let audioTracks: [PlaybackMediaTrack]
  public let subtitles: [PlaybackSubtitle]
  public let selectedAudioTrack: Int?
  public let selectedSubtitleId: String?
  public let decisionReasons: [PlaybackDecisionConstraint]
  public let coordinatedItem: CoordinatedPlaybackItem?
  public let sourceAddonId: UUID?
  public let nextEpisode: RivuneMediaTarget?
}

extension RivunePlaybackPresentation {
  var timelineOffsetSeconds: Double {
    mediaTimeline == .relative ? Double(max(timelineStartSeconds, 0)) : 0
  }

  func absolutePlaybackPosition(mediaSeconds: Double) -> Double {
    guard mediaSeconds.isFinite else { return timelineOffsetSeconds }
    return max(mediaSeconds, 0) + timelineOffsetSeconds
  }

  func mediaPlaybackPosition(absoluteSeconds: Double) -> Double {
    guard absoluteSeconds.isFinite else { return 0 }
    return max(absoluteSeconds - timelineOffsetSeconds, 0)
  }

  func resolvedPlaybackDuration(mediaDurationSeconds: Double) -> Double {
    if let durationSeconds, durationSeconds > 0 { return Double(durationSeconds) }
    guard mediaDurationSeconds.isFinite, mediaDurationSeconds > 0 else { return 0 }
    return absolutePlaybackPosition(mediaSeconds: mediaDurationSeconds)
  }
}

extension PlaybackDecisionConstraint {
  var safeDisplayName: String {
    switch self {
    case .containerNotSupported: return "Container conversion"
    case .videoCodecNotSupported: return "Video compatibility"
    case .audioCodecNotSupported: return "Audio compatibility"
    case .resolutionLimit: return "Resolution limit"
    case .bitrateLimit: return "Network bitrate limit"
    case .hdrNotSupported: return "HDR compatibility"
    case .subtitleBurnRequired: return "Subtitle rendering"
    }
  }
}

extension RivunePlaybackPresentation {
  var playbackDecisionSummary: String? {
    let labels = decisionReasons.map(\.safeDisplayName)
    return labels.isEmpty ? nil : labels.joined(separator: " · ")
  }
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
  let order: Int
  let batch: AddonResourceBatch?
  let failed: Bool
}

private struct RivuneSemanticSearchOutcome: Sendable {
  let page: SemanticSearchPage?
  let failed: Bool
}

private struct RivuneAsyncTimeoutError: Error {}

private final class RivuneAsyncDeadlineState<Value: Sendable>: @unchecked Sendable {
  private let lock = NSLock()
  private var continuation: CheckedContinuation<Value, Error>?
  private var operationTask: Task<Void, Never>?
  private var timeoutTask: Task<Void, Never>?
  private var cancelled = false
  private var finished = false

  func start(
    continuation: CheckedContinuation<Value, Error>,
    operation: @escaping @Sendable () async throws -> Value,
    timeoutNanoseconds: UInt64
  ) {
    lock.lock()
    if cancelled {
      finished = true
      lock.unlock()
      continuation.resume(throwing: CancellationError())
      return
    }
    self.continuation = continuation
    lock.unlock()

    let operationTask = Task {
      do {
        finish(.success(try await operation()))
      } catch {
        finish(.failure(error))
      }
    }
    let timeoutTask = Task {
      do {
        try await Task.sleep(nanoseconds: timeoutNanoseconds)
        finish(.failure(RivuneAsyncTimeoutError()))
      } catch {
        // The operation completed or the caller cancelled the race.
      }
    }
    register(operationTask: operationTask, timeoutTask: timeoutTask)
  }

  func cancel() {
    lock.lock()
    cancelled = true
    guard !finished, let continuation else {
      lock.unlock()
      return
    }
    finished = true
    self.continuation = nil
    let operationTask = self.operationTask
    let timeoutTask = self.timeoutTask
    self.operationTask = nil
    self.timeoutTask = nil
    lock.unlock()

    operationTask?.cancel()
    timeoutTask?.cancel()
    continuation.resume(throwing: CancellationError())
  }

  private func register(operationTask: Task<Void, Never>, timeoutTask: Task<Void, Never>) {
    lock.lock()
    if finished {
      lock.unlock()
      operationTask.cancel()
      timeoutTask.cancel()
      return
    }
    self.operationTask = operationTask
    self.timeoutTask = timeoutTask
    lock.unlock()
  }

  private func finish(_ result: Result<Value, Error>) {
    lock.lock()
    guard !finished, let continuation else {
      lock.unlock()
      return
    }
    finished = true
    self.continuation = nil
    let operationTask = self.operationTask
    let timeoutTask = self.timeoutTask
    self.operationTask = nil
    self.timeoutTask = nil
    lock.unlock()

    operationTask?.cancel()
    timeoutTask?.cancel()
    continuation.resume(with: result)
  }
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
    case .pairingCapacity:
      return "The server is handling too many pairing requests. Rivune will retry automatically."
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
    let host =
      rawHost
      .trimmingCharacters(in: CharacterSet(charactersIn: "[]"))
      .lowercased()
    guard !host.isEmpty else { return false }
    if host == "localhost" || host == "::1" { return true }
    if host.contains(":") {
      guard let firstGroup = host.split(separator: ":", omittingEmptySubsequences: true).first,
        let prefix = UInt16(firstGroup, radix: 16)
      else { return false }
      return prefix & 0xfe00 == 0xfc00
    }
    let octets = host.split(separator: ".", omittingEmptySubsequences: false).compactMap { Int($0) }
    guard octets.count == 4, octets.allSatisfy({ 0...255 ~= $0 }) else { return false }
    return octets[0] == 10 || octets[0] == 127 || (octets[0] == 172 && 16...31 ~= octets[1])
      || (octets[0] == 192 && octets[1] == 168)
  }
}

struct RivuneSeriesNavigationState: Equatable, Sendable {
  let episodes: [Episode]
  let progress: [UUID: PlaybackProgress]
  let watched: Bool?
}

public enum RivuneProfileExperienceSurface: String, CaseIterable, Sendable, Hashable {
  case queue, savedSearches, smartCollections, notifications, subscriptions, incidents, accessibility
}

public struct RivunePlaybackFailoverGate: Equatable, Sendable {
  public private(set) var switchCount = 0
  public private(set) var renderedFirstFrame = false
  public private(set) var cancelled = false
  public let maximumSwitches: Int

  public init(maximumSwitches: Int = 2) {
    self.maximumSwitches = min(max(maximumSwitches, 1), 3)
  }

  public var canAdvance: Bool {
    !renderedFirstFrame && !cancelled && switchCount < maximumSwitches
  }

  public mutating func beginAdvance() -> Bool {
    guard canAdvance else { return false }
    switchCount += 1
    return true
  }

  public mutating func markFirstFrame() { renderedFirstFrame = true }
  public mutating func cancel() { cancelled = true }
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
  @Published public private(set) var pairingRetrySeconds: Int?
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
  @Published public private(set) var offlineExpirationDays: Int
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
  @Published public private(set) var searchIntents: [SemanticSearchIntent] = []
  @Published public private(set) var searchMediaTypes: [String] = []
  @Published public private(set) var searchMediaType: String?
  @Published public private(set) var libraryItems: [RivuneAPI.LibraryItem] = []
  @Published public private(set) var libraryMediaType: TitleMediaType?
  @Published public private(set) var libraryPage = 0
  @Published public private(set) var libraryTotalPages = 0
  @Published public private(set) var libraryTotalResults = 0
  @Published public private(set) var calendarEvents: [CalendarEvent] = []
  @Published public private(set) var calendarMonth =
    Calendar.current.dateInterval(of: .month, for: Date())?.start ?? Date()
  @Published public private(set) var tabLoading = false
  @Published public private(set) var tabFailure: RivuneAppFailure?
  @Published public private(set) var isBusy = false
  @Published public private(set) var failure: RivuneAppFailure?
  @Published public private(set) var accent: RivuneAccent

  @Published public private(set) var preferredPlayer: RivunePlayerPreference
  @Published public private(set) var embeddedPlayerPreference: RivuneEmbeddedPlayerPreference
  @Published public private(set) var startupTab: RivuneViewerTab
  @Published public private(set) var animationPreference: RivuneAnimationPreference
  @Published public private(set) var recommendationLayout: RivuneRecommendationLayout
  @Published public private(set) var frameRateMatching: RivuneFrameRatePreference
  @Published public private(set) var videoAspect: RivuneVideoAspect
  @Published public private(set) var localQuality: RivuneNetworkQuality
  @Published public private(set) var remoteWifiQuality: RivuneNetworkQuality
  @Published public private(set) var mobileQuality: RivuneNetworkQuality
  @Published public private(set) var networkClass: RivuneNetworkClass = .remoteWifi
  @Published public private(set) var automaticallyShowStreams: Bool
  @Published public private(set) var autoSkipIntro: Bool
  @Published public private(set) var autoSkipRecap: Bool
  @Published public private(set) var autoSkipOutro: Bool
  @Published public private(set) var mediaDetail: RivuneMediaDetail?
  @Published public private(set) var profileArchiveAvailable = false
  @Published public private(set) var profileArchiveReport: ProfileArchiveImportReport?
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
  @Published public private(set) var readingQueue: ReadingQueue?
  @Published public private(set) var savedSearches: [SavedSearch] = []
  @Published public private(set) var smartCollections: [SmartCollection] = []
  @Published public private(set) var smartCollectionPage: SmartCollectionPage?
  @Published public private(set) var mediaNotifications: [MediaNotification] = []
  @Published public private(set) var mediaNotificationSubscriptions: [MediaNotificationSubscription] = []
  @Published public private(set) var extensionIncidents: [AddonIncident] = []
  @Published public private(set) var accessibilityPreferences: AccessibilityPreferencesDocument?
  @Published public private(set) var profileExperienceLoading = false
  @Published public private(set) var profileExperienceFailure: RivuneAppFailure?
  @Published public private(set) var profileExperienceLoadingSurfaces = Set<RivuneProfileExperienceSurface>()
  @Published public private(set) var profileExperienceFailures: [RivuneProfileExperienceSurface: RivuneAppFailure] = [:]
  @Published public private(set) var profileConflictMessage: String?
  @Published public private(set) var playbackFailoverNotice: String?
  @Published public private(set) var playbackFailoverLoading = false
  private let diagnostics = RivuneDiagnosticsBuffer()
  private let installedApplicationVersion: String
  private let updateChecker: any RivuneAppleUpdateChecking
  private var updateNotifier: (any RivuneAppleUpdateNotifying)?
  private let defaults: UserDefaults
  private let installationID: String
  private var serverOrigin: URL?
  private var client: RivuneAPIClient?
  private var offlineScope: RivuneOfflineMediaScope?
  private var currentOfflineAccess: RivuneOfflineProfileAccess?
  private var storedOfflineProfiles: [RivuneOfflineProfileAccess] = []
  private var operation: Task<Void, Never>?
  private var updateOperation: Task<Void, Never>?
  private var tabOperation: Task<Void, Never>?
  private var recommendationOperation: Task<Void, Never>?
  private var tabGeneration: UInt64 = 0
  private var searchPage = 0
  private var searchDescriptors: [AddonCatalogDescriptor] = []
  private var semanticSearchAvailable = false
  private var excludedSearchIntentIDs: [String] = []
  private let semanticSearchTimeoutNanoseconds: UInt64
  private static let searchPageSize = 50
  private static let defaultSemanticSearchTimeoutNanoseconds: UInt64 = 12_000_000_000
  private static let searchPublicationWindowNanoseconds: UInt64 = 32_000_000
  private var searchPublicationOperation: Task<Void, Never>?
  private var pendingSearchItems: [RivuneSearchItem] = []
  private var pendingSearchGeneration: UInt64?
  private var activeSearchOperationID: UUID?
  private var immediatelyPublishedSearchGeneration: UInt64?
  private var searchIdentityOwners: [String: String] = [:]
  private struct SearchPresentationEntry {
    let order: Int
    let item: RivuneSearchItem
  }
  private var searchPresentations: [String: SearchPresentationEntry] = [:]
  private var generation: UInt64 = 0
  private var mediaOperation: Task<Void, Never>?
  private var mediaGeneration: UInt64 = 0
  private var settingsOperation: Task<Void, Never>?
  private var coordinationOperation: Task<Void, Never>?
  private var profileExperienceOperation: Task<Void, Never>?
  private var playbackFailoverOperation: Task<Void, Never>?
  private var coordinationForeground = true
  private var coordinationLastPresenceNanoseconds: UInt64?
  private var coordinationRecentActivityUntilNanoseconds: UInt64 = 0
  private let coordinationNow: @Sendable () -> UInt64
  private let coordinationSleep: @Sendable (UInt64) async -> Void
  private var activePlaybackFailover: PlaybackFailoverState?
  private var playbackRenderedFirstFrame = false
  private var playbackFailoverGate = RivunePlaybackFailoverGate()
  private var coordinationPositionMilliseconds: Int64 = 0
  private var coordinationDurationMilliseconds: Int64 = 0
  private var coordinationStatus = "idle"
  private var executedPlaybackCommandID: UUID?
  private var lastPlaybackCommandID: UUID?
  private struct CompletedPlaybackCommandRecord: Codable {
    let operationId: UUID
    let status: PlaybackCommandResultStatus
    let code: PlaybackCommandResultCode
  }
  private var completedPlaybackCommandResults: [UUID: PlaybackCommandResultInput] = [:]
  private var completedPlaybackCommandOrder: [UUID] = []
  private static let maximumCompletedPlaybackCommands = 128
  private var localRecommendationsAvailable = false
  private var coordinationEndedPlaybackID: UUID?
  private var settingsGeneration: UInt64 = 0
  private var previousMediaDetail: RivuneMediaDetail?
  private struct PendingEpisodeAutoplay {
    let targetID: String
    let sourceAddonID: UUID?
  }
  public var effectiveAnimationPreference: RivuneAnimationPreference {
    switch accessibilityPreferences?.reducedMotion {
    case .reduce: return .reduced
    case .noPreference: return .full
    case .system, nil: return animationPreference
    }
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
  var coordinationIsForeground: Bool { coordinationForeground }
  var coordinationPollingActive: Bool { coordinationOperation != nil }

  nonisolated static func coordinationPollIntervalNanoseconds(
    hasActiveWork: Bool, recentActivityUntil: UInt64, now: UInt64
  ) -> UInt64 {
    hasActiveWork || recentActivityUntil > now ? 2_000_000_000 : 15_000_000_000
  }
  public var offlineDownloadsRequireWiFi = true

  nonisolated static func coordinationLoopDelayNanoseconds(
    commandInterval: UInt64, lastPresence: UInt64?, now: UInt64
  ) -> UInt64 {
    guard let lastPresence, now >= lastPresence else { return 0 }
    let elapsed = now - lastPresence
    let untilPresence = elapsed >= 15_000_000_000 ? 0 : 15_000_000_000 - elapsed
    return min(commandInterval, untilPresence)
  }
  public var canNavigateBackFromMedia: Bool { selectedSeason != nil || previousMediaDetail != nil }
  public var autoplayNextEpisode: Bool { profileSettings?.autoplayNextEpisode != false }
  public var applicationVersion: String { installedApplicationVersion }

  public convenience init(defaults: UserDefaults = .standard) {
    self.init(
      defaults: defaults,
      updateChecker: RivuneAppleUpdateChecker(),
      applicationVersion: RivuneAppleDiagnosticMetadata.current().appVersion,
      updateNotifier: RivuneAppleLocalUpdateNotifier()
    )
  }

  init(
    defaults: UserDefaults,
    updateChecker: any RivuneAppleUpdateChecking,
    applicationVersion: String,
    updateNotifier: (any RivuneAppleUpdateNotifying)? = nil,
    client: RivuneAPIClient? = nil,
    serverOrigin: URL? = nil,
    semanticSearchAvailable: Bool = false,
    semanticSearchTimeoutNanoseconds: UInt64? = nil,
    coordinationNow: @escaping @Sendable () -> UInt64 = { DispatchTime.now().uptimeNanoseconds },
    coordinationSleep: @escaping @Sendable (UInt64) async -> Void = { try? await Task.sleep(nanoseconds: $0) }
  ) {
    self.defaults = defaults
    self.updateChecker = updateChecker
    self.updateNotifier = updateNotifier
    self.installedApplicationVersion = applicationVersion
    self.client = client
    self.serverOrigin = serverOrigin
    self.semanticSearchAvailable = semanticSearchAvailable
    self.coordinationNow = coordinationNow
    self.coordinationSleep = coordinationSleep
    self.semanticSearchTimeoutNanoseconds =
      semanticSearchTimeoutNanoseconds ?? Self.defaultSemanticSearchTimeoutNanoseconds
    if let storedInstallationID = defaults.string(forKey: Self.installationIDKey),
      let parsedInstallationID = UUID(uuidString: storedInstallationID)
    {
      self.installationID = parsedInstallationID.uuidString.lowercased()
    } else {
      let generatedInstallationID = UUID().uuidString.lowercased()
      defaults.set(generatedInstallationID, forKey: Self.installationIDKey)
      self.installationID = generatedInstallationID
    }
    self.serverAddress = defaults.string(forKey: Self.serverKey) ?? ""
    self.accent =
      defaults.string(forKey: Self.accentKey).flatMap(RivuneAccent.init(rawValue:)) ?? .blue
    self.preferredPlayer =
      defaults.string(forKey: Self.playerKey).flatMap(RivunePlayerPreference.init(rawValue:))
      ?? .ask
    self.embeddedPlayerPreference =
      defaults.string(forKey: Self.embeddedPlayerKey).flatMap(
        RivuneEmbeddedPlayerPreference.init(rawValue:)) ?? .automatic
    self.startupTab =
      defaults.string(forKey: Self.startupTabKey).flatMap(RivuneViewerTab.init(rawValue:)) ?? .home
    self.animationPreference =
      defaults.string(forKey: Self.animationKey).flatMap(RivuneAnimationPreference.init(rawValue:))
      ?? .system
    self.recommendationLayout =
      defaults.string(forKey: Self.recommendationLayoutKey).flatMap(
        RivuneRecommendationLayout.init(rawValue:)) ?? .portrait
    self.frameRateMatching =
      defaults.string(forKey: Self.frameRateKey).flatMap(RivuneFrameRatePreference.init(rawValue:))
      ?? .system
    self.videoAspect =
      defaults.string(forKey: Self.videoAspectKey).flatMap(RivuneVideoAspect.init(rawValue:))
      ?? .fit
    let legacyWiFiQuality =
      defaults.string(forKey: Self.legacyWifiQualityKey).flatMap(RivuneNetworkQuality.init(rawValue:))
    let legacyMobileQuality =
      defaults.string(forKey: Self.legacyMobileQualityKey).flatMap(RivuneNetworkQuality.init(rawValue:))
    self.localQuality =
      defaults.string(forKey: Self.localQualityKey).flatMap(RivuneNetworkQuality.init(rawValue:))
      ?? legacyWiFiQuality ?? .automatic
    self.remoteWifiQuality =
      defaults.string(forKey: Self.remoteWifiQualityKey).flatMap(RivuneNetworkQuality.init(rawValue:))
      ?? legacyWiFiQuality ?? .automatic
    self.mobileQuality =
      defaults.string(forKey: Self.mobileQualityKey).flatMap(RivuneNetworkQuality.init(rawValue:))
      ?? legacyMobileQuality ?? .automatic
    let storedExpirationDays = defaults.object(forKey: Self.offlineExpirationDaysKey) as? Int
    self.offlineExpirationDays = storedExpirationDays.map { min(max($0, 0), 3_650) } ?? 30
    self.automaticallyShowStreams = defaults.object(forKey: Self.showStreamsKey) as? Bool ?? true
    self.autoSkipIntro = defaults.bool(forKey: Self.skipIntroKey)
    self.autoSkipRecap = defaults.bool(forKey: Self.skipRecapKey)
    self.autoSkipOutro = defaults.bool(forKey: Self.skipOutroKey)
    defaults.set(localQuality.rawValue, forKey: Self.localQualityKey)
    defaults.set(remoteWifiQuality.rawValue, forKey: Self.remoteWifiQualityKey)
    defaults.set(mobileQuality.rawValue, forKey: Self.mobileQualityKey)
    defaults.removeObject(forKey: Self.legacyWifiQualityKey)
    defaults.removeObject(forKey: Self.legacyMobileQualityKey)
    pathMonitor.pathUpdateHandler = { [weak self] path in
      Task { @MainActor [weak self] in
        guard let self else { return }
        let updatedClass = Self.classifyNetwork(
          cellular: path.usesInterfaceType(.cellular),
          expensive: path.isExpensive,
          constrained: path.isConstrained,
          serverOrigin: self.serverOrigin)
        self.networkClass = updatedClass
        if updatedClass == .mobile, self.offlineDownloadsRequireWiFi,
          self.offlineDownloadActive
        {
          self.mediaOperation?.cancel()
          self.offlineDownloadSourceIdentity = nil
          self.offlineDownloadActive = false
          self.mediaFailure = .message("Offline downloads require Wi-Fi.")
        }
      }
    }
    pathMonitor.start(queue: pathMonitorQueue)
    if let data = defaults.data(forKey: Self.offlineProfilesKey),
      let profiles = try? JSONDecoder().decode([RivuneOfflineProfileAccess].self, from: data)
    {
      storedOfflineProfiles = profiles
    }
    if let data = defaults.data(forKey: Self.completedPlaybackCommandsKey),
      let stored = try? JSONDecoder().decode([CompletedPlaybackCommandRecord].self, from: data)
    {
      completedPlaybackCommandOrder = stored.map(\.operationId)
      completedPlaybackCommandResults = Dictionary(uniqueKeysWithValues: stored.map {
        ($0.operationId, PlaybackCommandResultInput(status: $0.status, code: $0.code))
      })
    }
    defaults.removeObject(forKey: Self.offlineScopeKey)
    diagnostics.record(.appStarted)
    if let data = defaults.data(forKey: Self.updateCacheKey) {
      if let cached = try? JSONDecoder().decode(RivuneAppleUpdateCache.self, from: data),
        let restored = cached.restoredState(
          installedVersion: applicationVersion,
          platform: .current
        )
      {
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
    recommendationOperation?.cancel()
    mediaOperation?.cancel()
    settingsOperation?.cancel()
    coordinationOperation?.cancel()
    updateOperation?.cancel()
    profileExperienceOperation?.cancel()
    playbackFailoverOperation?.cancel()
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
        self.loadProfileExperiences()
        self.destination = .library
        await self.loadCollections(using: client, generation: currentGeneration)
      } catch is CancellationError {
      } catch {
        guard let self, self.isCurrent(currentGeneration) else { return }
        if await self.recoverSessionIfNeeded(error, using: client, generation: currentGeneration) {
          return
        }
        self.isBusy = false
        self.failure = self.map(error, fallback: .message(error.localizedDescription))
      }
    }
  }

  public func selectTab(_ tab: RivuneViewerTab) {
    if selectedTab == .search, tab != .search {
      beginTabOperation()
      tabLoading = false
    }
    selectedTab = tab
    tabFailure = nil
    switch tab {
    case .home:
      break
    case .search:
      if searchDescriptors.isEmpty {
        loadSearchMediaTypes()
      } else if !searchQuery.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
        search()
      }
    case .library:
      loadPersonalLibrary()
    case .calendar:
      loadCalendar()
    }
  }

  public func search() { runSearch(reset: true) }

  public func removeSearchIntent(id: String) {
    let normalized = id.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
    guard searchIntents.contains(where: { $0.id == normalized }),
      !excludedSearchIntentIDs.contains(normalized)
    else { return }
    excludedSearchIntentIDs.append(normalized)
    runSearch(reset: true, preserveExcludedIntents: true)
  }

  public func setSearchMediaType(_ mediaType: String?) {
    let normalized = mediaType?.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
    if let normalized, !searchMediaTypes.contains(normalized) { return }
    guard searchMediaType != normalized else { return }
    searchMediaType = normalized
    if searchQuery.trimmingCharacters(in: .whitespacesAndNewlines).count >= 2 {
      runSearch(reset: true)
    }
  }

  private func loadSearchMediaTypes() {
    guard let client else { return }
    beginTabOperation()
    let currentGeneration = tabGeneration
    tabLoading = true
    tabFailure = nil
    tabOperation = Task { [weak self] in
      do {
        let descriptors = try await client.addonCatalogs()
        guard let self, self.isCurrentTab(currentGeneration) else { return }
        self.applySearchDescriptors(descriptors)
        self.tabLoading = false
        if self.searchQuery.trimmingCharacters(in: .whitespacesAndNewlines).count >= 2 {
          self.runSearch(reset: true)
        }
      } catch is CancellationError {
      } catch {
        guard let self, self.isCurrentTab(currentGeneration) else { return }
        self.tabLoading = false
        self.tabFailure = self.map(error, fallback: .contentLoad)
      }
    }
  }

  public func loadMoreSearch() {
    guard searchHasMore, !tabLoading else { return }
    runSearch(reset: false)
  }

  private func runSearch(reset: Bool, preserveExcludedIntents: Bool = false) {
    guard let client else { return }
    let query = searchQuery.trimmingCharacters(in: .whitespacesAndNewlines)
    guard query.count >= 2 else {
      beginTabOperation()
      searchItems = []
      searchIdentityOwners = [:]
      searchPresentations = [:]
      searchIntents = []
      excludedSearchIntentIDs = []
      searchPage = 0
      searchHasMore = false
      searchPartial = false
      tabLoading = false
      tabFailure = nil
      return
    }
    if reset, !preserveExcludedIntents { excludedSearchIntentIDs = [] }
    let semanticPage = reset ? 1 : searchPage + 1
    let skip = reset ? 0 : searchPage * Self.searchPageSize
    beginTabOperation()
    let currentGeneration = tabGeneration
    tabLoading = true
    tabFailure = nil
    if reset {
      searchItems = []
      searchIdentityOwners = [:]
      searchPresentations = [:]
      searchIntents = []
      searchPage = 0
      searchHasMore = false
      searchPartial = false
    }
    let searchOperationID = UUID()
    activeSearchOperationID = searchOperationID
    diagnostics.record(.searchStarted, operationId: searchOperationID)
    tabOperation = Task { [weak self] in
      do {
        guard let self else { return }
        async let semanticSearch: RivuneSemanticSearchOutcome = Self.searchSemanticCatalog(
          using: client,
          enabled: self.semanticSearchAvailable,
          request: SemanticSearchRequest(
            query: query,
            mediaType: self.searchMediaType,
            language: Locale.current.languageCode,
            region: Locale.current.regionCode,
            page: semanticPage,
            limit: min(Self.searchPageSize, 40),
            excludedIntentIds: self.excludedSearchIntentIDs
          ),
          timeoutNanoseconds: self.semanticSearchTimeoutNanoseconds
        )
        let descriptors =
          self.searchDescriptors.isEmpty ? try await client.addonCatalogs() : self.searchDescriptors
        guard self.isCurrentTab(currentGeneration) else { return }
        self.applySearchDescriptors(descriptors)
        let availableTypes = Self.searchableMediaTypes(from: descriptors)
        let configuredTypes =
          self.searchMediaType.map { availableTypes.contains($0) ? [$0] : [] } ?? availableTypes
        let initialTypes = self.semanticSearchAvailable
          ? Array(configuredTypes.prefix(4))
          : configuredTypes
        let originalSearch = Task { [weak self] in
          try await Self.searchAddonCatalogs(
            using: client,
            types: initialTypes,
            query: query,
            skip: skip,
            limit: Self.searchPageSize,
            onOutcome: { [weak self] outcome in
              await self?.publishSearchOutcome(outcome, generation: currentGeneration)
            }
          )
        }
        let semanticOutcome: RivuneSemanticSearchOutcome
        do {
          semanticOutcome = try await semanticSearch
        } catch {
          originalSearch.cancel()
          _ = try? await originalSearch.value
          throw error
        }
        let semantic = semanticOutcome.page
        let semanticFailed = semanticOutcome.failed
        if let semantic {
          self.publishSearchItems(
            semantic.items.map(Self.searchItem(from:)), generation: currentGeneration)
          if reset { self.searchIntents = semantic.intents }
        }
        let inferredTypes = Set(semantic?.mediaTypes ?? [])
        let matchedTypes = configuredTypes.filter(inferredTypes.contains)
        let types =
          self.searchMediaType == nil && !matchedTypes.isEmpty
          ? matchedTypes
          : configuredTypes
        let residualQuery = semantic?.titleQuery.trimmingCharacters(in: .whitespacesAndNewlines)
        let titleQuery = residualQuery.map { $0.count >= 2 ? $0 : query } ?? query
        var outcomes = try await withTaskCancellationHandler {
          try await originalSearch.value
        } onCancel: {
          originalSearch.cancel()
        }
        if self.semanticSearchAvailable {
          let refined = semantic != nil && (types != configuredTypes || titleQuery != query)
          let followupTypes = refined
            ? types
            : Array(configuredTypes.dropFirst(initialTypes.count))
          let remainingBudget = max(16 - initialTypes.count, 0)
          if remainingBudget > 0, !followupTypes.isEmpty {
            outcomes += try await Self.searchAddonCatalogs(
              using: client,
              types: followupTypes,
              query: refined ? titleQuery : query,
              skip: skip,
              limit: Self.searchPageSize,
              maximumTypes: remainingBudget,
              onOutcome: { [weak self] outcome in
                await self?.publishSearchOutcome(outcome, generation: currentGeneration)
              })
          }
          if configuredTypes.count > 16, !outcomes.contains(where: { $0.failed && $0.batch == nil }) {
            outcomes.append(RivuneSearchBatchOutcome(order: outcomes.count, batch: nil, failed: true))
          }
        }
        let batches = outcomes.compactMap(\.batch)
        if batches.isEmpty, outcomes.contains(where: \.failed), semantic?.items.isEmpty != false,
          self.searchItems.isEmpty, self.pendingSearchItems.isEmpty
        {
          throw RivuneAPIError.invalidResponse
        }
        var directItems = batches.flatMap { batch in
          batch.results.flatMap(Self.searchItems(from:))
        }
        if self.searchMediaType == nil, !matchedTypes.isEmpty {
          directItems = directItems.filter { inferredTypes.contains($0.mediaType.lowercased()) }
        }
        let incoming = directItems + (semantic?.items.map(Self.searchItem(from:)) ?? [])
        guard self.isCurrentTab(currentGeneration) else { return }
        let pending = self.takePendingSearchItems(generation: currentGeneration)
        self.appendSearchItems(pending + incoming)
        if reset { self.searchIntents = semantic?.intents ?? [] }
        self.searchPage = reset ? 1 : self.searchPage + 1
        let fullPage = batches.contains { batch in
          batch.results.contains { Self.searchItems(from: $0).count >= Self.searchPageSize }
        }
        self.searchHasMore =
          ((semantic?.hasMore == true) || fullPage) && !self.searchItems.isEmpty
        self.searchPartial =
          semanticFailed || semantic?.partial == true || outcomes.contains(where: \.failed)
          || batches.contains { !$0.errors.isEmpty }
        self.tabLoading = false
        self.finishSearchDiagnostic(
          self.searchPartial ? .searchPartial : .searchSucceeded,
          operationId: searchOperationID)
      } catch is CancellationError {
        self?.finishSearchDiagnostic(.searchCanceled, operationId: searchOperationID)
      } catch {
        guard let self else { return }
        if self.isCurrentTab(currentGeneration) {
          self.appendSearchItems(self.takePendingSearchItems(generation: currentGeneration))
          self.tabLoading = false
          self.tabFailure = self.map(error, fallback: .contentLoad)
        }
        self.finishSearchDiagnostic(.searchFailed, operationId: searchOperationID)
      }
    }
  }

  private func applySearchDescriptors(_ descriptors: [AddonCatalogDescriptor]) {
    searchDescriptors = descriptors
    searchMediaTypes = Self.searchableMediaTypes(from: descriptors).filter { $0 != "all" }
    if let searchMediaType, !searchMediaTypes.contains(searchMediaType) {
      self.searchMediaType = nil
    }
  }
  nonisolated private static func searchAddonCatalogs(
    using client: RivuneAPIClient,
    types: [String],
    query: String,
    skip: Int,
    limit: Int,
    maximumTypes: Int = 16,
    onOutcome: @escaping @Sendable (RivuneSearchBatchOutcome) async -> Void
  ) async throws -> [RivuneSearchBatchOutcome] {
    let stableTypes = stableSearchTypes(types)
    let normalized = Array(stableTypes.prefix(max(maximumTypes, 0)))
    let truncated = normalized.count < stableTypes.count
    return try await withThrowingTaskGroup(of: RivuneSearchBatchOutcome.self) { group in
      var nextIndex = 0
      while nextIndex < min(4, normalized.count) {
        let order = nextIndex
        let type = normalized[nextIndex]
        group.addTask { try await searchAddonCatalog(using: client, type: type, query: query, skip: skip, limit: limit, order: order) }
        nextIndex += 1
      }
      var outcomes: [RivuneSearchBatchOutcome] = []
      outcomes.reserveCapacity(normalized.count + (truncated ? 1 : 0))
      for try await outcome in group {
        outcomes.append(outcome)
        await onOutcome(outcome)
        if nextIndex < normalized.count {
          let order = nextIndex
          let type = normalized[nextIndex]
          group.addTask { try await searchAddonCatalog(using: client, type: type, query: query, skip: skip, limit: limit, order: order) }
          nextIndex += 1
        }
      }
      if truncated {
        outcomes.append(RivuneSearchBatchOutcome(order: normalized.count, batch: nil, failed: true))
      }
      return outcomes.sorted { $0.order < $1.order }
    }
  }

  nonisolated private static func searchAddonCatalog(
    using client: RivuneAPIClient, type: String, query: String, skip: Int, limit: Int, order: Int
  ) async throws -> RivuneSearchBatchOutcome {
    do {
      return RivuneSearchBatchOutcome(
        order: order,
        batch: try await client.searchAddonCatalogs(
          type: type, search: query, skip: skip, limit: limit),
        failed: false)
    } catch is CancellationError {
      throw CancellationError()
    } catch {
      return RivuneSearchBatchOutcome(order: order, batch: nil, failed: true)
    }
  }

  nonisolated static func stableSearchTypes(_ types: [String]) -> [String] {
    var seen = Set<String>()
    return types.compactMap { value in
      let normalized = value.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
      guard !normalized.isEmpty, seen.insert(normalized).inserted else { return nil }
      return normalized
    }
  }

  nonisolated static func boundedSearchTypes(_ types: [String]) -> [String] {
    Array(stableSearchTypes(types).prefix(16))
  }

  private func publishSearchOutcome(_ outcome: RivuneSearchBatchOutcome, generation: UInt64) {
    guard isCurrentTab(generation), let batch = outcome.batch else { return }
    publishSearchItems(
      batch.results.flatMap(Self.searchItems(from:)), generation: generation)
  }

  private func publishSearchItems(_ incoming: [RivuneSearchItem], generation: UInt64) {
    guard isCurrentTab(generation), !incoming.isEmpty else { return }
    if immediatelyPublishedSearchGeneration != generation {
      if appendSearchItems(incoming) {
        immediatelyPublishedSearchGeneration = generation
      }
      return
    }
    if pendingSearchGeneration != generation {
      cancelPendingSearchPublication()
      pendingSearchGeneration = generation
    }
    pendingSearchItems.append(contentsOf: incoming)
    guard searchPublicationOperation == nil else { return }
    searchPublicationOperation = Task { [weak self] in
      do {
        try await Task.sleep(nanoseconds: Self.searchPublicationWindowNanoseconds)
      } catch {
        return
      }
      guard let self, self.isCurrentTab(generation) else { return }
      self.appendSearchItems(self.takePendingSearchItems(generation: generation))
    }
  }

  @discardableResult
  func appendSearchItems(_ incoming: [RivuneSearchItem]) -> Bool {
    guard !incoming.isEmpty else { return false }
    var additions: [RivuneSearchItem] = []
    additions.reserveCapacity(incoming.count)
    for var item in incoming {
      let identities = Self.searchIdentities(item)
      let owners = Set(identities.compactMap { searchIdentityOwners[$0] })
      if let owner = owners.compactMap({ owner in
        searchPresentations[owner].map { (owner, $0.order) }
      }).min(by: { $0.1 < $1.1 })?.0 {
        for identity in identities where searchIdentityOwners[identity] == nil {
          searchIdentityOwners[identity] = owner
        }
        continue
      }
      let presentationID = Self.canonicalSearchIdentity(in: identities)
      item.searchPresentationID = presentationID
      let order = searchItems.count + additions.count
      additions.append(item)
      searchPresentations[presentationID] = SearchPresentationEntry(order: order, item: item)
      for identity in identities { searchIdentityOwners[identity] = presentationID }
    }
    guard !additions.isEmpty else { return false }
    searchItems.append(contentsOf: additions)
    return true
  }

  private func takePendingSearchItems(generation: UInt64) -> [RivuneSearchItem] {
    guard pendingSearchGeneration == generation else { return [] }
    let pending = pendingSearchItems
    cancelPendingSearchPublication()
    return pending
  }

  private func cancelPendingSearchPublication() {
    searchPublicationOperation?.cancel()
    searchPublicationOperation = nil
    pendingSearchItems = []
    pendingSearchGeneration = nil
  }
  nonisolated private static func searchSemanticCatalog(
    using client: RivuneAPIClient,
    enabled: Bool,
    request: SemanticSearchRequest,
    timeoutNanoseconds: UInt64
  ) async throws -> RivuneSemanticSearchOutcome {
    guard enabled else { return RivuneSemanticSearchOutcome(page: nil, failed: false) }
    do {
      let page = try await withTimeout(nanoseconds: timeoutNanoseconds) {
        try await client.semanticSearch(request)
      }
      return RivuneSemanticSearchOutcome(page: page, failed: false)
    } catch is CancellationError {
      throw CancellationError()
    } catch {
      return RivuneSemanticSearchOutcome(page: nil, failed: true)
    }
  }

  nonisolated private static func withTimeout<Value: Sendable>(
    nanoseconds: UInt64,
    operation: @escaping @Sendable () async throws -> Value
  ) async throws -> Value {
    let state = RivuneAsyncDeadlineState<Value>()
    do {
      let value = try await withTaskCancellationHandler {
        try await withCheckedThrowingContinuation { continuation in
          state.start(
            continuation: continuation,
            operation: operation,
            timeoutNanoseconds: nanoseconds
          )
        }
      } onCancel: {
        state.cancel()
      }
      try Task.checkCancellation()
      return value
    } catch {
      try Task.checkCancellation()
      throw error
    }
  }

  nonisolated private static func searchableMediaTypes(from descriptors: [AddonCatalogDescriptor])
    -> [String]
  {
    let preferredOrder = ["movie": 0, "series": 1, "anime": 2, "tv": 3, "other": 4, "all": 5]
    return Set(
      descriptors.lazy.filter(\.searchable).map {
        $0.catalog.type.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
      }
    )
    .filter { !$0.isEmpty }
    .sorted { left, right in
      let leftRank = preferredOrder[left] ?? Int.max
      let rightRank = preferredOrder[right] ?? Int.max
      return leftRank == rightRank ? left < right : leftRank < rightRank
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
        let response = try await client.library(
          mediaType: self.libraryMediaType, page: page, pageSize: 100)
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
    calendarMonth =
      Calendar.current.date(byAdding: .month, value: offset, to: calendarMonth) ?? calendarMonth
    calendarEvents = []
    loadCalendar()
  }

  private func loadCalendar() {
    guard let client else { return }
    var calendar = Calendar(identifier: .gregorian)
    calendar.timeZone = .current
    guard let interval = calendar.dateInterval(of: .month, for: calendarMonth),
      let finalDay = calendar.date(byAdding: .day, value: -1, to: interval.end)
    else { return }
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
          calendar.isDate(self.calendarMonth, equalTo: month, toGranularity: .month)
        else { return }
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
        if await self.recoverSessionIfNeeded(error, using: client, generation: currentGeneration) {
          return
        }
        self.isBusy = false
        self.failure = self.map(error, fallback: .message(error.localizedDescription))
      }
    }
  }

  public func openFolder(in collection: Collection, folder: CollectionFolder) {
    guard let folderID = folder.id, let client else { return }
    beginOperation()
    let currentGeneration = generation
    openedFolder = OpenedCollectionFolder(
      id: folderID, collectionID: collection.id, folder: folder, items: nil, sourcePosterUrls: nil,
      page: 0, hasMore: false, errors: [])
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
        guard let self, self.isCurrent(currentGeneration), self.openedFolder?.id == folderID else {
          return
        }
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
        if await self.recoverSessionIfNeeded(error, using: client, generation: currentGeneration) {
          return
        }
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
        guard let self, self.isCurrent(currentGeneration), self.openedFolder?.id == current.id
        else { return }
        var seen = Set((current.items ?? []).map { "\($0.mediaType)\u{0}\($0.id)" })
        let additions = resolved.items.filter {
          seen.insert("\($0.mediaType)\u{0}\($0.id)").inserted
        }
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
      let scope = profile.scope, profile.permits(pin: pin)
    else {
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

  public func handleSceneActive() {
    coordinationForeground = true
    coordinationLastPresenceNanoseconds = nil
    if playbackCoordinationAvailable, let client { startCoordination(using: client) }
  }

  public func dismissOfflineUnlock() {
    pendingOfflineProfile = nil
    offlineUnlockFailure = nil
  }

  public func lockOffline(clearPending: Bool = true) {
    beginMediaOperation()
    if playbackPresentation?.sourceRef.hasPrefix("offline:") == true { playbackPresentation = nil }
    if minimizedPlaybackPresentation?.sourceRef.hasPrefix("offline:") == true {
      minimizedPlaybackPresentation = nil
    }
    offlineScope = nil
    offlineItems = []
    offlineUnlockFailure = nil
    if clearPending {
      pendingOfflineProfile = nil
      currentOfflineAccess = nil
    }
    Task { await RivuneOfflineMediaStore.shared.stopPlayback() }
  }

  public func handleSceneBackground() {
    coordinationForeground = false
    coordinationOperation?.cancel()
    coordinationOperation = nil
    guard let scope = offlineScope,
      let access = currentOfflineAccess
        ?? storedOfflineProfiles.first(where: { $0.id == scope.identifier }),
      access.requiresPIN
    else { return }
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
    RivuneDiagnosticsReport.build(
      RivuneDiagnosticReportInput(
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
        localQuality: localQuality.rawValue,
        remoteWifiQuality: remoteWifiQuality.rawValue,
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
      )
    {
      return
    }

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
          self.persistUpdateCache(
            .upToDate(currentVersion: currentVersion, latestVersion: latestVersion))
          self.updateNotice = nil
          self.diagnostics.record(.updateUpToDate)
        case .available(let update):
          self.updateState = .available(update)
          self.persistUpdateCache(.available(update))
          let shouldPresent = rivuneShouldPresentUpdateNotice(
            lastVersion: self.defaults.string(forKey: Self.lastNotifiedUpdateKey),
            candidateVersion: update.latestVersion
          )
          if manual {
            if shouldPresent { self.defaults.set(update.latestVersion, forKey: Self.lastNotifiedUpdateKey) }
            self.updateNotice = update
          } else if shouldPresent {
            let notifier = self.updateNotifier ?? RivuneAppleLocalUpdateNotifier()
            self.updateNotifier = notifier
            let delivered = await notifier.deliver(update)
            self.defaults.set(update.latestVersion, forKey: Self.lastNotifiedUpdateKey)
            self.updateNotice = delivered ? nil : update
          } else {
            self.updateNotice = nil
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

  /** The OS permission prompt is invoked only from a user-selected settings action. */
  public func enableUpdateNotifications() {
    let notifier = updateNotifier ?? RivuneAppleLocalUpdateNotifier()
    updateNotifier = notifier
    Task { await notifier.requestPermission() }
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
  public func setPreferredPlayer(_ value: RivunePlayerPreference) {
    preferredPlayer = value
    defaults.set(value.rawValue, forKey: Self.playerKey)
  }
  public func setEmbeddedPlayerPreference(_ value: RivuneEmbeddedPlayerPreference) {
    embeddedPlayerPreference = value
    defaults.set(value.rawValue, forKey: Self.embeddedPlayerKey)
  }
  public func setStartupTab(_ value: RivuneViewerTab) {
    startupTab = value
    defaults.set(value.rawValue, forKey: Self.startupTabKey)
  }
  public func setAnimationPreference(_ value: RivuneAnimationPreference) {
    animationPreference = value
    defaults.set(value.rawValue, forKey: Self.animationKey)
  }
  public func setRecommendationLayout(_ value: RivuneRecommendationLayout) {
    guard recommendationLayout != value else { return }
    recommendationLayout = value
    defaults.set(value.rawValue, forKey: Self.recommendationLayoutKey)
    recommendationItems = []
    recommendationOperation?.cancel()
    guard localRecommendationsAvailable, let client, destination == .library else { return }
    let currentGeneration = generation
    let artworkShape: RecommendationArtworkShape = value == .landscape ? .landscape : .poster
    recommendationOperation = Task { [weak self] in
      let page = try? await client.localRecommendations(limit: 24, artworkShape: artworkShape)
      guard let self, self.isCurrent(currentGeneration), self.client === client,
        self.recommendationLayout == value
      else { return }
      self.recommendationItems = page?.items.compactMap(Self.recommendationItem) ?? []
    }
  }
  public func setFrameRateMatching(_ value: RivuneFrameRatePreference) {
    frameRateMatching = value
    defaults.set(value.rawValue, forKey: Self.frameRateKey)
  }
  public func setVideoAspect(_ value: RivuneVideoAspect) {
    videoAspect = value
    defaults.set(value.rawValue, forKey: Self.videoAspectKey)
  }
  public func setLocalQuality(_ value: RivuneNetworkQuality) {
    localQuality = value
    defaults.set(value.rawValue, forKey: Self.localQualityKey)
  }
  public func setRemoteWifiQuality(_ value: RivuneNetworkQuality) {
    remoteWifiQuality = value
    defaults.set(value.rawValue, forKey: Self.remoteWifiQualityKey)
  }
  public func setMobileQuality(_ value: RivuneNetworkQuality) {
    mobileQuality = value
    defaults.set(value.rawValue, forKey: Self.mobileQualityKey)
  }
  public func setAutomaticallyShowStreams(_ value: Bool) {
    automaticallyShowStreams = value
    defaults.set(value, forKey: Self.showStreamsKey)
  }
  public func setAutoSkipIntro(_ value: Bool) {
    autoSkipIntro = value
    defaults.set(value, forKey: Self.skipIntroKey)
  }
  public func setOfflineExpirationDays(_ value: Int) {
    offlineExpirationDays = min(max(value, 0), 3_650)
    defaults.set(offlineExpirationDays, forKey: Self.offlineExpirationDaysKey)
  }
  public func setAutoSkipRecap(_ value: Bool) {
    autoSkipRecap = value
    defaults.set(value, forKey: Self.skipRecapKey)
  }
  public func setAutoSkipOutro(_ value: Bool) {
    autoSkipOutro = value
    defaults.set(value, forKey: Self.skipOutroKey)
  }

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
        guard let self, self.isCurrentSettings(requestGeneration, profileID: profile.id) else {
          return
        }
        self.profileSettings = effective.settings
        self.profileSettingsSources = effective.sources
        self.settingsLoading = false
      } catch is CancellationError {
      } catch {
        guard let self, self.isCurrentSettings(requestGeneration, profileID: profile.id) else {
          return
        }
        if await self.recoverSessionIfNeeded(error, using: client, generation: operationGeneration) {
          return
        }
        self.settingsLoading = false
        self.settingsFailure = self.map(
          error, fallback: .message("Profile settings could not be loaded."))
      }
    }
  }

  public func exportActiveProfileArchive() async throws -> ProfileArchiveDocument {
    guard profileArchiveAvailable, let client, let profile = activeProfile else {
      throw RivuneAPIError.invalidResponse
    }
    return try await client.exportProfileArchive(profileId: profile.id)
  }

  public func mergeActiveProfileArchive(
    _ archive: ProfileArchiveDocument
  ) async throws -> ProfileArchiveImportReport {
    guard profileArchiveAvailable, let client, let profile = activeProfile else {
      throw RivuneAPIError.invalidResponse
    }
    let report = try await client.mergeProfileArchive(profileId: profile.id, archive: archive)
    profileArchiveReport = report
    loadProfileSettings()
    return report
  }

  public func createProfileFromArchive(
    _ archive: ProfileArchiveDocument
  ) async throws -> ProfileArchiveImportReport {
    guard profileArchiveAvailable, let client, let categoryId = activeProfile?.categoryId else {
      throw RivuneAPIError.invalidResponse
    }
    let report = try await client.createProfileFromArchive(categoryId: categoryId, archive: archive)
    profileArchiveReport = report
    return report
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
        guard let self, self.isCurrentSettings(requestGeneration, profileID: profile.id) else {
          return
        }
        self.profileSettings = effective.settings
        self.profileSettingsSources = effective.sources
        self.settingsLoading = false
      } catch is CancellationError {
      } catch {
        guard let self, self.isCurrentSettings(requestGeneration, profileID: profile.id) else {
          return
        }
        if await self.recoverSessionIfNeeded(error, using: client, generation: operationGeneration)
        {
          return
        }
        self.settingsLoading = false
        self.settingsFailure = self.map(
          error, fallback: .message("Profile settings could not be saved."))
      }
    }
  }

  public func loadProfileExperiences() {
    guard let client, let profile = activeProfile else { return }
    profileExperienceOperation?.cancel()
    let currentGeneration = generation
    profileExperienceLoadingSurfaces = Set(RivuneProfileExperienceSurface.allCases)
    profileExperienceFailures = [:]
    profileExperienceLoading = true
    profileExperienceFailure = nil
    profileExperienceOperation = Task { [weak self] in
      await withTaskGroup(of: Void.self) { group in
        group.addTask { [weak self] in
          do {
            let value = try await client.readingQueue(profileId: profile.id)
            await self?.completeProfileExperience(
              .queue, profileID: profile.id, generation: currentGeneration) { $0.readingQueue = value }
          } catch { await self?.failProfileExperience(.queue, error: error, profileID: profile.id, generation: currentGeneration) }
        }
        group.addTask { [weak self] in
          do {
            let value = try await client.savedSearches()
            await self?.completeProfileExperience(
              .savedSearches, profileID: profile.id, generation: currentGeneration) { $0.savedSearches = value }
          } catch { await self?.failProfileExperience(.savedSearches, error: error, profileID: profile.id, generation: currentGeneration) }
        }
        group.addTask { [weak self] in
          do {
            let value = try await client.smartCollections()
            await self?.completeProfileExperience(
              .smartCollections, profileID: profile.id, generation: currentGeneration) { $0.smartCollections = value }
          } catch { await self?.failProfileExperience(.smartCollections, error: error, profileID: profile.id, generation: currentGeneration) }
        }
        group.addTask { [weak self] in
          do {
            let value = try await client.mediaNotifications(limit: 30)
            await self?.completeProfileExperience(
              .notifications, profileID: profile.id, generation: currentGeneration) { $0.mediaNotifications = value.notifications }
          } catch { await self?.failProfileExperience(.notifications, error: error, profileID: profile.id, generation: currentGeneration) }
        }
        group.addTask { [weak self] in
          do {
            let value = try await client.mediaNotificationSubscriptions()
            await self?.completeProfileExperience(
              .subscriptions, profileID: profile.id, generation: currentGeneration) { $0.mediaNotificationSubscriptions = value }
          } catch { await self?.failProfileExperience(.subscriptions, error: error, profileID: profile.id, generation: currentGeneration) }
        }
        group.addTask { [weak self] in
          do {
            let value = try await client.profileAccessibilityPreferences(profileId: profile.id)
            await self?.completeProfileExperience(
              .accessibility, profileID: profile.id, generation: currentGeneration) { $0.accessibilityPreferences = value }
          } catch { await self?.failProfileExperience(.accessibility, error: error, profileID: profile.id, generation: currentGeneration) }
        }
        group.addTask { [weak self] in
          guard profile.canManage else {
            await self?.completeProfileExperience(
              .incidents, profileID: profile.id, generation: currentGeneration) { $0.extensionIncidents = [] }
            return
          }
          do {
            let value = try await client.extensionIncidents()
            await self?.completeProfileExperience(
              .incidents, profileID: profile.id, generation: currentGeneration) { $0.extensionIncidents = value }
          } catch { await self?.failProfileExperience(.incidents, error: error, profileID: profile.id, generation: currentGeneration) }
        }
      }
    }
  }

  public func isProfileExperienceLoading(_ surface: RivuneProfileExperienceSurface) -> Bool {
    profileExperienceLoadingSurfaces.contains(surface)
  }

  public func profileExperienceFailure(for surface: RivuneProfileExperienceSurface) -> RivuneAppFailure? {
    profileExperienceFailures[surface]
  }

  private func completeProfileExperience(
    _ surface: RivuneProfileExperienceSurface, profileID: UUID, generation: UInt64,
    apply: @MainActor (RivuneAppModel) -> Void
  ) {
    guard isCurrent(generation), activeProfile?.id == profileID else { return }
    apply(self)
    profileExperienceFailures.removeValue(forKey: surface)
    profileExperienceLoadingSurfaces.remove(surface)
    profileExperienceLoading = !profileExperienceLoadingSurfaces.isEmpty
    profileExperienceFailure = profileExperienceFailures.values.first
  }

  private func failProfileExperience(
    _ surface: RivuneProfileExperienceSurface, error: Error, profileID: UUID, generation: UInt64
  ) {
    guard !(error is CancellationError), isCurrent(generation), activeProfile?.id == profileID else { return }
    let failure = map(error, fallback: .message("This profile section could not be refreshed."))
    profileExperienceFailures[surface] = failure
    profileExperienceLoadingSurfaces.remove(surface)
    profileExperienceLoading = !profileExperienceLoadingSurfaces.isEmpty
    profileExperienceFailure = failure
  }

  public func addCurrentMediaToQueue() {
    guard let detail = mediaDetail,
      let type = ReadingQueueMediaType(rawValue: detail.target.mediaType)
    else { return }
    mutateReadingQueue { client, profileID, operationId, revision in
      try await client.addReadingQueueItem(
        profileId: profileID,
        input: ReadingQueueAddInput(
          operationId: operationId, expectedRevision: revision, mediaType: type,
          resourceId: detail.target.resourceId, sourceAddonId: detail.target.sourceAddonId,
          titleId: detail.titleId, title: detail.target.title, posterUrl: detail.target.posterUrl))
    }
  }

  public func removeQueueItem(_ item: ReadingQueueItem) {
    mutateReadingQueue { client, profileID, operationId, revision in
      try await client.removeReadingQueueItem(
        profileId: profileID, itemId: item.id,
        input: ReadingQueueMutationInput(operationId: operationId, expectedRevision: revision))
    }
  }

  public func moveQueueItem(_ item: ReadingQueueItem, offset: Int) {
    guard let queue = readingQueue, let index = queue.items.firstIndex(where: { $0.id == item.id }) else {
      return
    }
    let destination = index + offset
    guard queue.items.indices.contains(destination) else { return }
    var ids = queue.items.map(\.id)
    ids.swapAt(index, destination)
    let reorderedIDs = ids
    mutateReadingQueue { client, profileID, operationId, revision in
      try await client.reorderReadingQueue(
        profileId: profileID,
        input: ReadingQueueReorderInput(
          operationId: operationId, expectedRevision: revision, itemIds: reorderedIDs))
    }
  }

  private func mutateReadingQueue(
    _ mutation: @escaping @Sendable (RivuneAPIClient, UUID, UUID, Int64) async throws
      -> ReadingQueueMutation
  ) {
    guard let client, let profile = activeProfile, let queue = readingQueue else { return }
    let operationId = UUID()
    profileExperienceLoading = true
    profileConflictMessage = nil
    Task { [weak self] in
      do {
        _ = try await mutation(client, profile.id, operationId, queue.revision)
        let refreshed = try await client.readingQueue(profileId: profile.id)
        guard let self, self.activeProfile?.id == profile.id else { return }
        self.readingQueue = refreshed
        self.profileExperienceLoading = false
      } catch {
        guard let self, self.activeProfile?.id == profile.id else { return }
        self.profileExperienceLoading = false
        if case RivuneAPIError.server(let status, _, _, _) = error, status == 409 {
          self.profileConflictMessage = "The queue changed on another device. Refresh before trying again."
        } else {
          self.profileExperienceFailure = self.map(
            error, fallback: .message("The queue could not be changed."))
        }
      }
    }
  }

  public func saveCurrentSearch(name: String? = nil) {
    guard let client else { return }
    let query = searchQuery.trimmingCharacters(in: .whitespacesAndNewlines)
    guard query.count >= 2 else { return }
    let proposedTitle = name?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
    let title = proposedTitle.isEmpty ? query : proposedTitle
    profileExperienceLoading = true
    profileConflictMessage = nil
    Task { [weak self] in
      do {
        let savedMediaType = self?.searchMediaType.flatMap(SavedSearchMediaType.init(rawValue:))
        let saved = try await client.createSavedSearch(
          SavedSearchInput(name: title, query: query, mediaType: savedMediaType, sort: .relevance))
        guard let self else { return }
        self.savedSearches.append(saved)
        self.savedSearches.sort {
          let order = $0.name.localizedCaseInsensitiveCompare($1.name)
          return order == .orderedSame ? $0.id.uuidString < $1.id.uuidString : order == .orderedAscending
        }
        self.profileExperienceLoading = false
      } catch {
        guard let self else { return }
        self.profileExperienceLoading = false
        self.profileExperienceFailure = self.map(error, fallback: .message("The search could not be saved."))
      }
    }
  }

  public func runSavedSearch(_ search: SavedSearch) {
    searchQuery = search.query
    searchMediaType = search.mediaType?.rawValue
    selectTab(.search)
    runSearch(reset: true)
  }

  public func deleteSavedSearch(_ search: SavedSearch) {
    guard let client else { return }
    Task { [weak self] in
      do {
        try await client.deleteSavedSearch(id: search.id, expectedRevision: search.revision)
        self?.savedSearches.removeAll { $0.id == search.id }
      } catch {
        guard let self else { return }
        if case RivuneAPIError.server(let status, _, _, _) = error, status == 409 {
          self.profileConflictMessage = "This saved search changed on another device."
        } else {
          self.profileExperienceFailure = self.map(error, fallback: .message("The saved search could not be deleted."))
        }
      }
    }
  }

  public func openSmartCollection(_ collection: SmartCollection) {
    guard let client else { return }
    profileExperienceLoading = true
    Task { [weak self] in
      do {
        let page = try await client.evaluateSmartCollection(id: collection.id)
        guard let self else { return }
        self.smartCollectionPage = page
        self.profileExperienceLoading = false
      } catch {
        guard let self else { return }
        self.profileExperienceLoading = false
        self.profileExperienceFailure = self.map(error, fallback: .message("The smart collection could not be evaluated."))
      }
    }
  }

  public func acknowledgeMediaNotification(
    _ notification: MediaNotification, state: MediaNotificationAcknowledgementState
  ) {
    guard let client else { return }
    Task { [weak self] in
      do {
        try await client.acknowledgeMediaNotification(id: notification.id, state: state)
        guard let self else { return }
        if state == .dismissed { self.mediaNotifications.removeAll { $0.id == notification.id } }
        else { self.loadProfileExperiences() }
      } catch {
        self?.profileExperienceFailure = .message("The notification could not be updated.")
      }
    }
  }

  public func followCurrentMediaNotifications() {
    guard let client, let titleId = mediaDetail?.titleId else { return }
    let timezone = TimeZone.current.identifier
    Task { [weak self] in
      do {
        let subscription = try await client.followMediaNotifications(
          titleId: titleId,
          input: MediaNotificationFollowInput(timezone: timezone, horizonDays: 90, leadDays: 1))
        guard let self else { return }
        self.mediaNotificationSubscriptions.removeAll { $0.titleId == titleId }
        self.mediaNotificationSubscriptions.append(subscription)
      } catch {
        self?.profileExperienceFailure = .message("Release notifications could not be enabled.")
      }
    }
  }
  public func setAccessibilityReducedMotion(_ value: AccessibilityReducedMotion) {
    guard let current = accessibilityPreferences else { return }
    updateAccessibilityPreferences(AccessibilityPreferencesDocument(
      revision: current.revision, reducedMotion: value, highContrast: current.highContrast,
      textScale: current.textScale, captions: current.captions,
      audioDescription: current.audioDescription, focusIndicators: current.focusIndicators))
  }

  public func setAccessibilityContrast(_ value: AccessibilityContrast) {
    guard let current = accessibilityPreferences else { return }
    updateAccessibilityPreferences(AccessibilityPreferencesDocument(
      revision: current.revision, reducedMotion: current.reducedMotion, highContrast: value,
      textScale: current.textScale, captions: current.captions,
      audioDescription: current.audioDescription, focusIndicators: current.focusIndicators))
  }

  public func setAccessibilityTextScale(_ value: Int) {
    guard let current = accessibilityPreferences, [100, 115, 130].contains(value) else { return }
    updateAccessibilityPreferences(AccessibilityPreferencesDocument(
      revision: current.revision, reducedMotion: current.reducedMotion,
      highContrast: current.highContrast, textScale: value, captions: current.captions,
      audioDescription: current.audioDescription, focusIndicators: current.focusIndicators))
  }

  public func setAccessibilityCaptions(_ value: AccessibilityCaptions) {
    guard let current = accessibilityPreferences else { return }
    updateAccessibilityPreferences(AccessibilityPreferencesDocument(
      revision: current.revision, reducedMotion: current.reducedMotion,
      highContrast: current.highContrast, textScale: current.textScale, captions: value,
      audioDescription: current.audioDescription, focusIndicators: current.focusIndicators))
  }

  public func setAccessibilityAudioDescription(_ value: Bool) {
    guard let current = accessibilityPreferences else { return }
    updateAccessibilityPreferences(AccessibilityPreferencesDocument(
      revision: current.revision, reducedMotion: current.reducedMotion,
      highContrast: current.highContrast, textScale: current.textScale,
      captions: current.captions, audioDescription: value,
      focusIndicators: current.focusIndicators))
  }

  public func setAccessibilityFocusIndicators(_ value: AccessibilityFocusIndicators) {
    guard let current = accessibilityPreferences else { return }
    updateAccessibilityPreferences(AccessibilityPreferencesDocument(
      revision: current.revision, reducedMotion: current.reducedMotion,
      highContrast: current.highContrast, textScale: current.textScale,
      captions: current.captions, audioDescription: current.audioDescription,
      focusIndicators: value))
  }


  public func acknowledgeIncident(_ incident: AddonIncident) {
    guard let client, activeProfile?.canManage == true else { return }
    Task { [weak self] in
      do {
        let updated = try await client.acknowledgeExtensionIncident(id: incident.id)
        guard let self, let index = self.extensionIncidents.firstIndex(where: { $0.id == updated.id }) else { return }
        self.extensionIncidents[index] = updated
      } catch {
        self?.profileExperienceFailure = .message("The incident could not be acknowledged.")
      }
    }
  }

  public func updateAccessibilityPreferences(_ document: AccessibilityPreferencesDocument) {
    guard let client, let profile = activeProfile, profile.canManage else { return }
    settingsLoading = true
    settingsFailure = nil
    Task { [weak self] in
      do {
        let updated = try await client.updateProfileAccessibilityPreferences(
          profileId: profile.id, document: document)
        guard let self, self.activeProfile?.id == profile.id else { return }
        self.accessibilityPreferences = updated
        self.settingsLoading = false
      } catch {
        guard let self, self.activeProfile?.id == profile.id else { return }
        self.settingsLoading = false
        if case RivuneAPIError.server(let status, _, _, _) = error, status == 409 {
          self.profileConflictMessage = "Accessibility preferences changed on another device. Reload and try again."
        } else {
          self.settingsFailure = self.map(error, fallback: .message("Accessibility preferences could not be saved."))
        }
      }
    }
  }

  private static func loadSeries(
    id: UUID,
    context: RivuneMediaTarget,
    using client: RivuneAPIClient
  ) async -> Series? {
    if let mappingProvider = context.mappingProvider {
      return try? await client.series(
        id: id,
        mappingProvider: mappingProvider,
        episodeOrder: context.episodeOrderId)
    }
    if let canonical = try? await client.series(id: id, mappingProvider: .tmdb) {
      return canonical
    }
    guard let fallback = try? await client.series(id: id, mappingProvider: .tvdb) else {
      return nil
    }
    guard let officialOrderID = officialOrderID(in: fallback) else {
      return nil
    }
    guard let official = try? await client.series(
      id: id,
      mappingProvider: .tvdb,
      episodeOrder: officialOrderID),
      selectedOrderIsOfficial(official, expectedOrderID: officialOrderID)
    else {
      return nil
    }
    return official
  }

  private static func officialOrderID(in series: Series) -> String? {
    guard series.mappingProvider == .tvdb else { return nil }
    let selectedOrderID = series.selectedEpisodeOrderId?
      .trimmingCharacters(in: .whitespacesAndNewlines)
    let selectedOrder = selectedOrderID.flatMap { selectedID in
      series.episodeOrders.first { $0.id == selectedID }
    }
    if selectedOrder?.type.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
      == "official"
    {
      return selectedOrderID.flatMap { positiveDecimalInt64($0) }
    }
    return series.episodeOrders.first(where: {
      $0.type.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() == "official"
    }).flatMap { positiveDecimalInt64($0.id) }
  }

  private static func selectedOrderIsOfficial(
    _ series: Series,
    expectedOrderID: String? = nil
  ) -> Bool {
    guard
      series.mappingProvider == .tvdb,
      let selectedOrderID = series.selectedEpisodeOrderId?
        .trimmingCharacters(in: .whitespacesAndNewlines),
      expectedOrderID == nil || selectedOrderID == expectedOrderID,
      let selectedOrder = series.episodeOrders.first(where: { $0.id == selectedOrderID })
    else {
      return false
    }
    return selectedOrder.type.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
      == "official"
  }

  private static func positiveDecimalInt64(_ value: String) -> String? {
    let normalized = value.trimmingCharacters(in: .whitespacesAndNewlines)
    guard
      let first = normalized.utf8.first,
      first >= 49 && first <= 57,
      normalized.utf8.allSatisfy({ $0 >= 48 && $0 <= 57 }),
      Int64(normalized) != nil
    else {
      return nil
    }
    return normalized
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
        async let trailersValue =
          (target.mediaType == "movie" || target.mediaType == "series")
          ? (try? client.trailers(titleId: titleID).trailers) : []
        let movie = target.mediaType == "movie" ? try? await client.movie(id: titleID) : nil
        let series =
          target.mediaType == "series"
          ? await Self.loadSeries(id: titleID, context: target, using: client) : nil
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
        if target.mediaType == "episode", let seriesID = target.seriesId {
          parentSeries = await Self.loadSeries(id: seriesID, context: target, using: client)
          let provider = target.mappingProvider ?? parentSeries?.mappingProvider ?? .tmdb
          let seasonID =
            target.metadataSeasonId
            ?? parentSeries?.seasons.first { $0.id == target.seasonId }?.id
            ?? parentSeries?.seasons.first { $0.seasonNumber == target.seasonNumber }?.id
          if let seasonID {
            episodeSeason = try? await client.season(id: seasonID, mappingProvider: provider)
            episode =
              episodeSeason?.episodes.first { $0.id == titleID }
              ?? episodeSeason?.episodes.first { $0.episodeNumber == target.episodeNumber }
          }
        }
        let progress = await progressValue
        let library = await libraryValue
        let trailers = await trailersValue ?? []
        guard let self, self.isCurrentMedia(current) else { return }
        self.seriesEpisodes = seriesWatchState?.episodes ?? []
        self.episodeProgress = seriesWatchState?.progress ?? [:]
        self.seriesEpisodesWatched = seriesWatchState.map { state in
          !state.episodes.isEmpty
            && state.episodes.allSatisfy { state.progress[$0.id]?.completed == true }
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
        async let trailersValue = try? client.trailers(
          titleId: detail.titleId, seasonNumber: summary.seasonNumber
        ).trailers
        let season = try await client.season(
          id: summary.id, mappingProvider: detail.series?.mappingProvider ?? .tmdb)
        let progress = try? await client.playbackProgressBatch(titleIds: season.episodes.map(\.id))
        let trailers = await trailersValue ?? []
        guard let self, self.isCurrentMedia(current) else { return }
        self.selectedSeason = season
        self.seasonTrailers = trailers
        self.episodeProgress = Dictionary(
          uniqueKeysWithValues: (progress?.items ?? []).compactMap { item in
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
    guard let client, var detail = mediaDetail, detail.target.mediaType != "episode",
      !mediaActionLoading
    else { return }
    mediaActionLoading = true
    mediaFailure = nil
    Task { [weak self] in
      do {
        if detail.inLibrary {
          try await client.removeLibraryTitle(id: detail.titleId)
        } else {
          _ = try await client.addLibraryTitle(id: detail.titleId)
        }
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
          let completed =
            initialSeriesWatched
            ?? episodes.allSatisfy { previousProgress[$0.id]?.completed == true }
          var updatedProgress = previousProgress
          for start in stride(from: 0, to: episodes.count, by: 100) {
            let end = min(start + 100, episodes.count)
            let result = try await client.setTitlesWatchedBatch(
              episodes[start..<end].map {
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
          progress = try await client.markTitleUnwatched(
            titleId: detail.titleId, expectedVersion: expected)
        } else {
          progress = try await client.markTitleWatched(
            titleId: detail.titleId, expectedVersion: expected)
        }
        guard let self, self.mediaDetail?.titleId == titleID else { return }
        detail.progress = progress
        self.mediaDetail = detail
        self.mediaActionLoading = false
      } catch {
        guard let self, self.mediaDetail?.titleId == titleID else { return }
        self.mediaActionLoading = false
        self.mediaFailure = self.map(
          error, fallback: .message("The watched state could not be updated."))
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
    let capabilities = Self.playbackCapabilities(
      for: currentQuality, networkClass: networkClass,
      player: preferredPlayer, embedded: embeddedPlayerPreference)
    mediaOperation = Task { [weak self] in
      do {
        let result = try await client.playbackSources(
          mediaType: detail.target.mediaType, addonId: detail.target.playbackAddonId,
          resourceId: detail.target.resourceId, capabilities: capabilities)
        guard let self, self.isCurrentMedia(current) else { return }
        self.playbackSources = result.sources
        self.mediaLoading = false
        if let autoplayAddonID {
          guard
            let source = result.sources.first(where: { $0.addonId == autoplayAddonID })
              ?? result.sources.first
          else {
            self.mediaFailure = .message("No compatible playback source is available.")
            return
          }
          self.play(source, externally: false)
        }
      } catch is CancellationError {
      } catch {
        guard let self, self.isCurrentMedia(current) else { return }
        self.mediaLoading = false
        self.mediaFailure = self.map(
          error, fallback: .message("No compatible playback source is available."))
      }
    }
  }

  public func download(_ source: PlaybackSourceOption) {
    guard let client, let detail = mediaDetail, !offlineDownloadActive,
      let selectedSource = playbackSources.first(where: {
        $0.id == source.id && $0.sourceRef == source.sourceRef
      })
    else { return }
    guard !["hls", "dash"].contains(selectedSource.protocol.lowercased()) else {
      mediaFailure = .message(RivuneOfflineMediaError.unsupportedSource.localizedDescription)
      return
    }
    guard let scope = offlineScope else {
      showPlaybackSources = false
      requestActiveOfflineUnlockIfAvailable()
      return
    }
    guard networkClass != .mobile || !offlineDownloadsRequireWiFi else {
      mediaFailure = .message(RivuneOfflineMediaError.mobileNetworkBlocked.localizedDescription)
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
          self.offlineDownloadSourceIdentity == sourceIdentity
        {
          self.offlineDownloadSourceIdentity = nil
          self.offlineDownloadActive = false
        }
      }
      do {
        _ = try await client.preparePlayback(
          sourceRef: selectedSource.sourceRef, externalPlayer: true)
        let session = try await client.resolvePlayback(
          sourceRef: selectedSource.sourceRef,
          titleId: detail.titleId.uuidString.lowercased(),
          externalPlayer: true
        )
        playbackSessionID = session.id
        guard
          let resolvedSource = session.sources.first(where: { $0.id == session.selectedSourceId })
            ?? session.sources.first,
          let rawURL = resolvedSource.url, self.isCurrentMedia(current),
          let url = self.resolvedResourceURL(rawURL),
          ["http", "https"].contains(url.scheme?.lowercased() ?? ""),
          !["hls", "dash"].contains(resolvedSource.protocol.lowercased())
        else {
          throw RivuneOfflineMediaError.unsupportedSource
        }
        let item = try await RivuneOfflineMediaStore.shared.download(
          from: url, titleId: detail.titleId, title: detail.target.title,
          container: resolvedSource.container ?? selectedSource.container,
          posterURL: detail.target.posterUrl, in: scope,
          expirationDays: offlineExpirationDays
        ) { [weak self] bytes in
          Task { @MainActor [weak self] in
            guard let self, self.mediaGeneration == current,
              self.offlineDownloadSourceIdentity == sourceIdentity
            else { return }
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
          id: UUID(), sessionId: UUID(), sourceRef: "offline:\(item.id.uuidString)",
          titleId: item.titleId,
          title: item.title, url: url, engine: .native,
          fallbackAllowed: RivunePlaybackEnginePolicy.embeddedMPVSupported, startSeconds: 0,
          timelineStartSeconds: 0, mediaTimeline: nil, videoAspect: videoAspect, playbackSpeed: 1,
          markers: [], durationSeconds: nil, expectedVersion: 0, audioTracks: [], subtitles: [],
          selectedAudioTrack: nil, selectedSubtitleId: nil, decisionReasons: [],
          coordinatedItem: nil, sourceAddonId: nil, nextEpisode: nil
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
    let operationId = UUID()
    Task { [weak self] in
      do {
        let commandInput = PlaybackCommandInput(
          operationId: operationId, command: .load, item: item,
          positionMilliseconds: Int64((detail.progress?.positionSeconds ?? 0) * 1_000),
          mode: .handoff, targetRevision: device.revision)
        _ = try await Self.sendPlaybackCommandWithRetry(
          using: client, sessionId: device.sessionId, input: commandInput)
        for _ in 0..<30 {
          try Task.checkCancellation()
          let outgoing = try await client.outgoingPlaybackCommand(operationId: operationId)
          if outgoing.status != .pending {
            guard outgoing.status == .applied, outgoing.resultCode == .applied else {
              throw RivuneAPIError.server(
                status: 409, code: outgoing.resultCode?.rawValue ?? outgoing.status.rawValue,
                message: "Playback handoff was not applied.", retryAfterSeconds: nil)
            }
            guard let self else { return }
            if let presentation = self.playbackPresentation {
              self.playbackFinished(
                position: Int(self.coordinationPositionMilliseconds / 1_000),
                duration: Int(self.coordinationDurationMilliseconds / 1_000), completed: false)
              try? await client.stopPlayback(sessionId: presentation.sessionId)
            }
            return
          }
          try await Task.sleep(nanoseconds: 500_000_000)
        }
        throw RivuneAPIError.server(
          status: 408, code: "expired", message: "Playback handoff expired.", retryAfterSeconds: nil)
      } catch {
        self?.mediaFailure = self?.map(
          error, fallback: .message("Playback handoff could not be completed."))
      }
    }
  }
  public func playCopy(to device: PlaybackDevice) {
    guard let client, let detail = mediaDetail else { return }
    let input = PlaybackCommandInput(
      command: .load, item: coordinatedItem(for: detail),
      positionMilliseconds: Int64((detail.progress?.positionSeconds ?? 0) * 1_000),
      mode: .playCopy, targetRevision: device.revision)
    Task { _ = try? await Self.sendPlaybackCommandWithRetry(using: client, sessionId: device.sessionId, input: input) }
  }
  public func controlPlayback(on device: PlaybackDevice, command: PlaybackCommandKind) {
    guard let client, command != .load else { return }
    let position = command == .seek ? coordinationPositionMilliseconds : nil
    let input = PlaybackCommandInput(
      command: command, positionMilliseconds: position, targetRevision: device.revision)
    Task {
      _ = try? await Self.sendPlaybackCommandWithRetry(
        using: client, sessionId: device.sessionId, input: input)
    }
    coordinationRecentActivityUntilNanoseconds = coordinationNow() + 30_000_000_000
  }
  public func createPlaybackRoom() {
    guard playbackCoordinationAvailable else { return }
    guard let client, let detail = mediaDetail else { return }
    Task { [weak self] in
      self?.activePlaybackRoom = try? await client.createPlaybackRoom(
        PlaybackRoomCreateInput(
          item: self?.coordinatedItem(for: detail)
            ?? CoordinatedPlaybackItem(titleId: detail.titleId), state: "paused",
          positionMilliseconds: Int64((detail.progress?.positionSeconds ?? 0) * 1_000),
          durationMilliseconds: Int64((detail.progress?.durationSeconds ?? 0) * 1_000)))
    }
  }

  public func joinPlaybackRoom(code: String) {
    guard playbackCoordinationAvailable else { return }
    guard let client, !code.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
    Task { [weak self] in
      guard let self, let room = try? await client.joinPlaybackRoom(code: code) else { return }
      self.activePlaybackRoom = room
      await self.startCoordinatedPlayback(
        room.item, positionMilliseconds: room.positionMilliseconds, using: client)
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

  public func consumePlaybackCommand(status: PlaybackCommandResultStatus = .applied) {
    guard !pendingPlaybackCommands.isEmpty, let client else { return }
    let command = pendingPlaybackCommands.removeFirst()
    let code: PlaybackCommandResultCode = status == .applied ? .applied : .executionFailed
    rememberCompletedPlaybackCommand(command.operationId, status: status, code: code)
    executedPlaybackCommandID = command.operationId
    coordinationRecentActivityUntilNanoseconds = coordinationNow() + 30_000_000_000
    Task {
      _ = try? await client.reportPlaybackCommandResult(
        operationId: command.operationId,
        input: PlaybackCommandResultInput(status: status, code: code))
    }
  }

  public func updateCoordinationPlayback(position: Double, duration: Double, playing: Bool) {
    coordinationPositionMilliseconds = Int64(max(position, 0) * 1_000)
    coordinationDurationMilliseconds = Int64(max(duration, 0) * 1_000)
    coordinationStatus = playing ? "playing" : "paused"
  }
  public func updatePlaybackSession(
    videoAspect: RivuneVideoAspect? = nil, playbackSpeed: Double? = nil
  ) {
    if var presentation = playbackPresentation {
      if let videoAspect { presentation.videoAspect = videoAspect }
      if let playbackSpeed, playbackSpeed.isFinite, playbackSpeed > 0 {
        presentation.playbackSpeed = playbackSpeed
      }
      playbackPresentation = presentation
    }
    if var presentation = minimizedPlaybackPresentation {
      if let videoAspect { presentation.videoAspect = videoAspect }
      if let playbackSpeed, playbackSpeed.isFinite, playbackSpeed > 0 {
        presentation.playbackSpeed = playbackSpeed
      }
      minimizedPlaybackPresentation = presentation
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
    let preserveOriginalSource = RivunePlaybackEnginePolicy.preservesOriginalSource(
      for: selection, externally: externally)
    var failoverCandidates = [source.sourceRef]
    for candidate in playbackSources where !failoverCandidates.contains(candidate.sourceRef) {
      failoverCandidates.append(candidate.sourceRef)
      if failoverCandidates.count == 8 { break }
    }
    beginMediaOperation()
    let current = mediaGeneration
    mediaLoading = true
    mediaFailure = nil
    mediaOperation = Task { [weak self] in
      do {
        var failoverState: PlaybackFailoverState?
        if !externally && failoverCandidates.count >= 2 {
          do {
            failoverState = try await client.createPlaybackFailover(
              PlaybackFailoverCreateInput(
                candidateSourceRefs: failoverCandidates, selectedSourceRef: source.sourceRef,
                maximumAttempts: 2))
          } catch {
            self?.playbackFailoverNotice = "Automatic source fallback is unavailable."
          }
        }
        _ = try await client.preparePlayback(
          sourceRef: source.sourceRef, startSeconds: detail.progress?.positionSeconds,
          externalPlayer: preserveOriginalSource)
        let session = try await client.resolvePlayback(
          sourceRef: source.sourceRef, titleId: detail.titleId.uuidString.lowercased(),
          startSeconds: detail.progress?.positionSeconds, externalPlayer: preserveOriginalSource)
        guard
          let selected = session.sources.first(where: { $0.id == session.selectedSourceId })
            ?? session.sources.first,
          let rawURL = selected.url,
          let self, self.isCurrentMedia(current), let url = self.resolvedResourceURL(rawURL)
        else { throw RivuneAPIError.invalidResponse }
        var markers: [PlaybackMarker] = []
        if detail.target.mediaType == "episode",
          detail.target.episodeOrderId?.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty != false,
          let imdb = (detail.parentSeries ?? detail.series)?.externalIds["imdb"],
          let season = detail.target.seasonNumber,
          let episode = detail.target.episodeNumber
        {
          markers =
            (try? await client.playbackMarkers(imdbId: imdb, season: season, episode: episode)
              .markers) ?? []
        }
        let nextEpisode =
          externally ? nil : await Self.nextEpisodeTarget(for: detail, using: client)
        self.mediaLoading = false
        self.showPlaybackSources = false
        if externally {
          self.externalPlaybackSessionID = session.id
          self.externalPlaybackURL = url
        } else {
          self.playbackPresentation = RivunePlaybackPresentation(
            id: UUID(), sessionId: session.id, sourceRef: source.sourceRef, titleId: detail.titleId,
            title: detail.target.title, url: url, engine: selection.engine,
            fallbackAllowed: selection.fallbackAllowed,
            startSeconds: detail.progress?.positionSeconds ?? 0,
            timelineStartSeconds: detail.progress?.positionSeconds ?? 0,
            mediaTimeline: selected.mediaTimeline,
            videoAspect: self.videoAspect, playbackSpeed: 1, markers: markers,
            durationSeconds: selected.media?.durationSeconds.map { max(Int($0.rounded()), 0) }
              ?? detail.progress?.durationSeconds, expectedVersion: detail.progress?.version ?? 0,
            audioTracks: selected.media?.audioTracks ?? [], subtitles: session.subtitles,
            selectedAudioTrack: session.selectedAudioTrack,
            selectedSubtitleId: session.selectedSubtitleId,
            decisionReasons: selected.decision?.reasons ?? [],
            coordinatedItem: self.coordinatedItem(for: detail), sourceAddonId: source.addonId,
            nextEpisode: nextEpisode
          )
          self.activePlaybackFailover = failoverState
          self.playbackFailoverGate = RivunePlaybackFailoverGate(
            maximumSwitches: failoverState?.maximumAttempts ?? 2)
          self.playbackRenderedFirstFrame = false
          if failoverState != nil { self.playbackFailoverNotice = nil }
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
    if current.selectedAudioTrack == audioTrack && current.selectedSubtitleId == subtitleId {
      return
    }
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
        guard
          let selected = session.sources.first(where: { $0.id == session.selectedSourceId })
            ?? session.sources.first,
          let rawURL = selected.url,
          let self,
          self.playbackPresentation?.id == current.id,
          let url = self.resolvedResourceURL(rawURL)
        else { throw RivuneAPIError.invalidResponse }
        self.playbackPresentation = RivunePlaybackPresentation(
          id: current.id, sessionId: session.id, sourceRef: current.sourceRef,
          titleId: current.titleId,
          title: current.title, url: url, engine: current.engine,
          fallbackAllowed: current.fallbackAllowed,
          startSeconds: position, timelineStartSeconds: position,
          mediaTimeline: selected.mediaTimeline,
          videoAspect: current.videoAspect, playbackSpeed: current.playbackSpeed,
          markers: current.markers,
          durationSeconds: selected.media?.durationSeconds.map { max(Int($0.rounded()), 0) }
            ?? current.durationSeconds,
          expectedVersion: current.expectedVersion,
          audioTracks: selected.media?.audioTracks ?? current.audioTracks,
          subtitles: session.subtitles, selectedAudioTrack: session.selectedAudioTrack,
          selectedSubtitleId: session.selectedSubtitleId,
          decisionReasons: selected.decision?.reasons ?? [],
          coordinatedItem: current.coordinatedItem,
          sourceAddonId: current.sourceAddonId, nextEpisode: current.nextEpisode
        )
        self.playbackOptionLoading = false
        try? await client.stopPlayback(sessionId: current.sessionId)
      } catch {
        guard let self else { return }
        self.playbackOptionLoading = false
        self.mediaFailure = self.map(
          error, fallback: .message("The playback options could not be changed."))
      }
    }
  }

  public func markPlaybackFirstFrame(presentationID: UUID) {
    guard playbackPresentation?.id == presentationID || minimizedPlaybackPresentation?.id == presentationID,
      !playbackRenderedFirstFrame
    else { return }
    playbackRenderedFirstFrame = true
    playbackFailoverGate.markFirstFrame()
  }

  @discardableResult
  public func attemptPlaybackFailover(
    error: PlaybackFailoverError, position: Int, duration: Int, minimized: Bool = false
  ) -> Bool {
    guard [.sourceFailed, .sourceTimeout, .endedEarly].contains(error),
      let client, var failover = activePlaybackFailover, failover.status == .active,
      playbackFailoverGate.beginAdvance(), !playbackFailoverLoading,
      let current = minimized ? minimizedPlaybackPresentation : playbackPresentation
    else { return false }
    playbackFailoverLoading = true
    playbackFailoverNotice = "Trying another source…"
    playbackFailoverOperation?.cancel()
    playbackFailoverOperation = Task { [weak self] in
      do {
        failover = try await client.advancePlaybackFailover(
          id: failover.id,
          input: PlaybackFailoverAdvanceInput(
            error: error, positionSeconds: Double(max(position, 0)),
            expectedRevision: failover.revision))
        guard failover.status == .active, let sourceRef = failover.currentSourceRef else {
          throw RivuneAPIError.invalidResponse
        }
        let session = try await client.resolvePlayback(
          sourceRef: sourceRef, titleId: current.titleId.uuidString.lowercased(),
          preferredAudioTrack: current.selectedAudioTrack,
          preferredSubtitleId: current.selectedSubtitleId, startSeconds: max(position, 0),
          externalPlayer: current.engine == .mpv || current.fallbackAllowed)
        guard let selected = session.sources.first(where: { $0.id == session.selectedSourceId })
          ?? session.sources.first,
          let rawURL = selected.url, let self, let url = self.resolvedResourceURL(rawURL)
        else { throw RivuneAPIError.invalidResponse }
        let replacement = Self.replacingFailoverSource(
          current, session: session, sourceRef: sourceRef, source: selected, url: url,
          position: max(position, 0), duration: max(duration, position))
        guard (minimized ? self.minimizedPlaybackPresentation : self.playbackPresentation)?.id
          == current.id
        else { return }
        self.activePlaybackFailover = failover
        self.playbackRenderedFirstFrame = false
        self.playbackFailoverLoading = false
        self.playbackFailoverNotice = failover.explanation ?? "Switched to another source."
        if minimized { self.minimizedPlaybackPresentation = replacement }
        else { self.playbackPresentation = replacement }
        // Keep the failed session alive until the replacement has resolved and is published.
        try? await client.stopPlayback(sessionId: current.sessionId)
      } catch is CancellationError {
      } catch {
        guard let self else { return }
        self.activePlaybackFailover = failover
        self.playbackFailoverLoading = false
        self.playbackFailoverNotice = failover.status == .exhausted
          ? (failover.explanation ?? "No unused source remains.")
          : "Source fallback could not continue."
      }
    }
    return true
  }

  public func dismissPlaybackFailoverNotice() { playbackFailoverNotice = nil }

  public func fallbackPlaybackToMPV(position: Int, duration: Int) {
    guard RivunePlaybackEnginePolicy.embeddedMPVSupported,
      let current = playbackPresentation, current.engine == .native, current.fallbackAllowed
    else { return }
    playbackPresentation = Self.movedToMPV(current, position: position, duration: duration)
  }

  public func fallbackMinimizedPlaybackToMPV(position: Int, duration: Int) {
    guard RivunePlaybackEnginePolicy.embeddedMPVSupported,
      let current = minimizedPlaybackPresentation, current.engine == .native,
      current.fallbackAllowed
    else { return }
    minimizedPlaybackPresentation = Self.movedToMPV(current, position: position, duration: duration)
  }

  public func minimizePlayback(position: Int, duration: Int) {
    guard let current = playbackPresentation else { return }
    minimizedPlaybackPresentation = Self.resumedPresentation(
      current, position: position, duration: duration)
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
    guard let presentation = playbackPresentation, let nextEpisode = presentation.nextEpisode else {
      return
    }
    pendingEpisodeAutoplay = PendingEpisodeAutoplay(
      targetID: nextEpisode.id, sourceAddonID: presentation.sourceAddonId)
    playbackFinished(position: position, duration: duration, completed: true)
    openMedia(nextEpisode)
  }

  public func playNextMinimizedEpisode(position: Int, duration: Int) {
    guard let presentation = minimizedPlaybackPresentation,
      let nextEpisode = presentation.nextEpisode
    else { return }
    pendingEpisodeAutoplay = PendingEpisodeAutoplay(
      targetID: nextEpisode.id, sourceAddonID: presentation.sourceAddonId)
    minimizedPlaybackFinished(position: position, duration: duration, completed: true)
    openMedia(nextEpisode)
  }

  private func endActivePlaybackRoom(for presentation: RivunePlaybackPresentation) {
    guard let client, let room = activePlaybackRoom, room.currentMemberIsHost,
      coordinationEndedPlaybackID != presentation.id
    else { return }
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

  private func finishPlayback(
    _ presentation: RivunePlaybackPresentation, position: Int, duration: Int, completed: Bool
  ) {
    guard let client else { return }
    let failover = activePlaybackFailover
    activePlaybackFailover = nil
    playbackFailoverOperation?.cancel()
    playbackFailoverOperation = nil
    playbackFailoverLoading = false
    playbackFailoverGate.cancel()
    Task {
      _ = try? await client.updatePlaybackProgress(
        titleId: presentation.titleId,
        input: UpdatePlaybackProgressRequest(
          positionSeconds: position, durationSeconds: duration, completed: completed,
          expectedVersion: presentation.expectedVersion))
      try? await client.stopPlayback(sessionId: presentation.sessionId)
      if let failover { try? await client.cancelPlaybackFailover(id: failover.id) }
    }
  }

  nonisolated private static func replacingFailoverSource(
    _ presentation: RivunePlaybackPresentation, session: PlaybackSession, sourceRef: String,
    source: PlaybackSource, url: URL, position: Int, duration: Int
  ) -> RivunePlaybackPresentation {
    RivunePlaybackPresentation(
      id: presentation.id, sessionId: session.id, sourceRef: sourceRef,
      titleId: presentation.titleId, title: presentation.title, url: url,
      engine: presentation.engine, fallbackAllowed: presentation.fallbackAllowed,
      startSeconds: position, timelineStartSeconds: position,
      mediaTimeline: source.mediaTimeline, videoAspect: presentation.videoAspect,
      playbackSpeed: presentation.playbackSpeed, markers: presentation.markers,
      durationSeconds: source.media?.durationSeconds.map { max(Int($0.rounded()), 0) } ?? duration,
      expectedVersion: presentation.expectedVersion,
      audioTracks: source.media?.audioTracks ?? presentation.audioTracks,
      subtitles: session.subtitles, selectedAudioTrack: session.selectedAudioTrack,
      selectedSubtitleId: session.selectedSubtitleId,
      decisionReasons: source.decision?.reasons ?? [], coordinatedItem: presentation.coordinatedItem,
      sourceAddonId: source.addonId, nextEpisode: presentation.nextEpisode)
  }

  nonisolated private static func movedToMPV(
    _ presentation: RivunePlaybackPresentation, position: Int, duration: Int
  ) -> RivunePlaybackPresentation {
    RivunePlaybackPresentation(
      id: presentation.id, sessionId: presentation.sessionId, sourceRef: presentation.sourceRef,
      titleId: presentation.titleId, title: presentation.title, url: presentation.url,
      engine: .mpv, fallbackAllowed: false, startSeconds: max(position, 0),
      timelineStartSeconds: presentation.timelineStartSeconds,
      mediaTimeline: presentation.mediaTimeline,
      videoAspect: presentation.videoAspect, playbackSpeed: presentation.playbackSpeed,
      markers: presentation.markers, durationSeconds: max(duration, position),
      expectedVersion: presentation.expectedVersion, audioTracks: presentation.audioTracks,
      subtitles: presentation.subtitles, selectedAudioTrack: presentation.selectedAudioTrack,
      selectedSubtitleId: presentation.selectedSubtitleId,
      decisionReasons: presentation.decisionReasons,
      coordinatedItem: presentation.coordinatedItem,
      sourceAddonId: presentation.sourceAddonId, nextEpisode: presentation.nextEpisode
    )
  }

  nonisolated private static func resumedPresentation(
    _ presentation: RivunePlaybackPresentation, position: Int, duration: Int
  ) -> RivunePlaybackPresentation {
    RivunePlaybackPresentation(
      id: presentation.id, sessionId: presentation.sessionId, sourceRef: presentation.sourceRef,
      titleId: presentation.titleId, title: presentation.title, url: presentation.url,
      engine: presentation.engine, fallbackAllowed: presentation.fallbackAllowed,
      startSeconds: position, timelineStartSeconds: presentation.timelineStartSeconds,
      mediaTimeline: presentation.mediaTimeline, videoAspect: presentation.videoAspect,
      playbackSpeed: presentation.playbackSpeed, markers: presentation.markers,
      durationSeconds: max(duration, position), expectedVersion: presentation.expectedVersion,
      audioTracks: presentation.audioTracks, subtitles: presentation.subtitles,
      selectedAudioTrack: presentation.selectedAudioTrack,
      selectedSubtitleId: presentation.selectedSubtitleId,
      decisionReasons: presentation.decisionReasons,
      coordinatedItem: presentation.coordinatedItem, sourceAddonId: presentation.sourceAddonId,
      nextEpisode: presentation.nextEpisode
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

  public var playbackQuality: RivuneNetworkQuality {
    switch networkClass {
    case .local: return localQuality
    case .remoteWifi: return remoteWifiQuality
    case .mobile: return mobileQuality
    }
  }

  private var currentQuality: RivuneNetworkQuality { playbackQuality }

  public func resolvedResourceURL(_ value: String) -> URL? {
    guard let serverOrigin,
      let components = URLComponents(string: value),
      components.user == nil, components.password == nil,
      let resolved = URL(string: value, relativeTo: serverOrigin)?.absoluteURL
    else { return nil }
    if components.scheme == nil {
      guard components.host == nil, Self.origin(of: resolved) == serverOrigin else { return nil }
      return resolved
    }
    guard Self.origin(of: resolved) == serverOrigin || resolved.scheme?.lowercased() == "https"
    else { return nil }
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
      if discovery.setupRequired {
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
        && discovery.supports(.playbackCommandResults)
      localRecommendationsAvailable = discovery.supports(.localRecommendations)
      semanticSearchAvailable = discovery.supports(.semanticSearch)
      profileArchiveAvailable = discovery.supportsProfileArchives
      let currentPath = pathMonitor.currentPath
      networkClass = Self.classifyNetwork(
        cellular: currentPath.usesInterfaceType(.cellular),
        expensive: currentPath.isExpensive,
        constrained: currentPath.isConstrained,
        serverOrigin: serverOrigin)
      defaults.set(value, forKey: Self.serverKey)
      if try await candidate.restoreSession() {
        try await routeAuthenticated(candidate, generation: generation)
      } else {
        destination = .pairing
        await beginPairing(generation: generation)
      }
    } catch {
      diagnostics.record(.serverConnectionFailed)
      if let client, await recoverSessionIfNeeded(error, using: client, generation: generation) {
        return
      }
      finishFailure(map(error, fallback: .serverUnreachable), generation: generation)
    }
  }

  private func beginPairing(generation: UInt64) async {
    guard let client, isCurrent(generation) else { return }
    pairingRetrySeconds = nil

    while isCurrent(generation), self.client === client {
      do {
        let authorization = try await client.beginDeviceAuthorization(
          installationId: installationID,
          deviceName: Self.deviceName,
          platform: Self.platformName
        )
        guard isCurrent(generation) else { return }
        pairingCode = authorization.userCode
        verificationURL =
          URL(string: authorization.verificationUriComplete)
          ?? URL(string: authorization.verificationUri)
        failure = nil
        isBusy = false
        await poll(authorization, using: client, generation: generation)
        return
      } catch is CancellationError {
        return
      } catch RivuneAPIError.server(_, let code, _, let retryAfterSeconds)
        where code == "device_code_capacity" || code == "rate_limited"
      {
        let shouldRetry = await waitToRetryPairing(
          after: max(retryAfterSeconds ?? 60, 1),
          using: client,
          generation: generation
        )
        guard shouldRetry else { return }
      } catch {
        finishFailure(map(error, fallback: .pairingFailed), generation: generation)
        return
      }
    }
  }

  private func waitToRetryPairing(
    after delaySeconds: Int,
    using client: RivuneAPIClient,
    generation: UInt64
  ) async -> Bool {
    guard isCurrent(generation), self.client === client else { return false }
    pairingCode = nil
    verificationURL = nil
    pairingAccepted = false
    failure = .pairingCapacity
    isBusy = false

    for remaining in stride(from: delaySeconds, through: 1, by: -1) {
      guard isCurrent(generation), self.client === client else { return false }
      pairingRetrySeconds = remaining
      do {
        try await Task.sleep(nanoseconds: 1_000_000_000)
      } catch {
        return false
      }
    }

    guard isCurrent(generation), self.client === client else { return false }
    pairingRetrySeconds = nil
    failure = nil
    isBusy = true
    return true
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
      } catch RivuneAPIError.server(_, let code, _, let retryAfterSeconds) {
        switch code {
        case "authorization_pending": continue
        case "slow_down": interval = max(retryAfterSeconds ?? interval + 5, 1)
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
      } catch URLError.notConnectedToInternet, URLError.cannotConnectToHost, URLError
        .networkConnectionLost
      {
        guard isCurrent(generation) else { return }
        failure = .serverUnreachable
      } catch {
        finishFailure(
          map(error, fallback: .pairingFailed), generation: generation, clearPairing: true)
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
      let active = profiles.first(where: { $0.id == activeID && $0.accessible })
    {
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
        lhs.position == rhs.position
          ? lhs.title.localizedStandardCompare(rhs.title) == .orderedAscending
          : lhs.position < rhs.position
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
    let recommendationArtworkShape: RecommendationArtworkShape =
      recommendationLayout == .landscape ? .landscape : .poster
    async let continuePage = try? client.continueWatching(limit: 24)
    async let recommendationPage: LocalRecommendationPage? =
      localRecommendationsAvailable
      ? (try? await client.localRecommendations(limit: 24, artworkShape: recommendationArtworkShape))
      : nil
    let scope = offlineScope
    let candidates = Array(
      collections.filter(\.heroEnabled).flatMap { collection in
        collection.folders.compactMap {
          folder -> (collectionID: UUID, folderID: UUID, collectionBackdrop: String?)? in
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
    if let scope {
      stored = await RivuneOfflineMediaStore.shared.items(in: scope)
    } else {
      stored = []
    }
    guard isCurrent(generation), self.collections.map(\.id) == collections.map(\.id) else { return }
    var seen = Set<String>()
    heroItems = heroes.filter { seen.insert($0.id).inserted }
    continueWatchingItems = watching
    recommendationItems = recommendations.compactMap(Self.recommendationItem)
    offlineItems = stored
  }

  nonisolated private static func sendPlaybackCommandWithRetry(
    using client: RivuneAPIClient, sessionId: UUID, input: PlaybackCommandInput
  ) async throws -> PlaybackCommand {
    var lastError: Error = RivuneAPIError.invalidResponse
    for attempt in 0..<3 {
      do {
        return try await client.sendPlaybackCommand(sessionId: sessionId, input: input)
      } catch is CancellationError {
        throw CancellationError()
      } catch let error as RivuneAPIError {
        if case .server(let status, _, _, _) = error, status == 409 { throw error }
        lastError = error
      } catch {
        lastError = error
      }
      if attempt < 2 { try await Task.sleep(nanoseconds: 250_000_000) }
    }
    throw lastError
  }

  nonisolated private static func recommendationItem(_ recommendation: LocalRecommendation)
    -> RivuneRecommendationItem?
  {
    let item = recommendation.item
    guard let title = item.title, !title.isEmpty else { return nil }
    let target = RivuneMediaTarget(
      id: item.id.uuidString, resourceId: item.resourceId ?? item.id.uuidString,
      mediaType: item.mediaType, title: title, titleId: item.id,
      provider: item.resourceProvider, externalId: nil, externalIds: item.providerIds,
      sourceAddonId: item.sourceAddonId, sourceCatalogId: nil, sourceName: nil,
      posterUrl: item.posterUrl, backgroundUrl: item.backgroundUrl, logoUrl: nil,
      overview: nil, releaseInfo: item.releaseInfo, released: nil,
      seriesId: nil, mappingProvider: nil, episodeOrderId: nil, metadataSeasonId: nil,
      seasonId: nil, seasonNumber: nil, episodeNumber: nil, runtimeMinutes: nil)
    return RivuneRecommendationItem(id: item.id, reason: recommendation.reason, target: target)
  }
  private func coordinatedItem(for detail: RivuneMediaDetail) -> CoordinatedPlaybackItem {
    CoordinatedPlaybackItem(
      titleId: detail.titleId, mediaType: detail.target.mediaType,
      resourceId: detail.target.resourceId, sourceAddonId: detail.target.playbackAddonId,
      title: detail.target.title, posterUrl: detail.target.posterUrl)
  }


  private func startCoordination(using client: RivuneAPIClient) {
    coordinationOperation?.cancel()
    guard coordinationForeground else { coordinationOperation = nil; return }
    coordinationOperation = Task { [weak self] in
      while !Task.isCancelled {
        guard let self, self.coordinationForeground else { return }
        let now = self.coordinationNow()
        let activePresentation = self.playbackPresentation ?? self.minimizedPlaybackPresentation
        let item = activePresentation?.coordinatedItem
        if self.coordinationLastPresenceNanoseconds.map({ now &- $0 >= 15_000_000_000 }) != false {
          let status = item == nil ? "idle" : self.coordinationStatus
          let input = PlaybackDeviceHeartbeatInput(
            capabilities: ["remote-control", "watch-room", "command-results"],
            state: PlaybackDeviceState(
              status: status, item: item,
              positionMilliseconds: item == nil ? 0 : self.coordinationPositionMilliseconds,
              durationMilliseconds: item == nil ? 0 : self.coordinationDurationMilliseconds))
          _ = try? await client.updatePlaybackDevice(input)
          if let devices = try? await client.playbackDevices() {
            self.playbackDevices = devices.devices.filter { !$0.current }
          }
          self.coordinationLastPresenceNanoseconds = now
        }

        if let operationId = self.executedPlaybackCommandID,
          let completed = self.completedPlaybackCommandResults[operationId]
        {
          do {
            _ = try await client.reportPlaybackCommandResult(operationId: operationId, input: completed)
            self.lastPlaybackCommandID = operationId
            self.executedPlaybackCommandID = nil
          } catch {
            // Re-submit the same terminal result; the endpoint is idempotent.
          }
        }
        if self.executedPlaybackCommandID == nil, self.pendingPlaybackCommands.isEmpty,
          let commands = try? await client.playbackCommands(after: self.lastPlaybackCommandID)
        {
          for command in commands.commands {
            if let completed = self.completedPlaybackCommandResults[command.operationId] {
              _ = try? await client.reportPlaybackCommandResult(
                operationId: command.operationId, input: completed)
              self.lastPlaybackCommandID = command.operationId
              continue
            }
            if command.command == .load, let commandItem = command.item, command.mode != nil {
              await self.startCoordinatedPlayback(
                commandItem, positionMilliseconds: command.positionMilliseconds ?? 0, using: client)
              let applied = self.playbackPresentation != nil
              self.rememberCompletedPlaybackCommand(
                command.operationId, status: applied ? .applied : .failed,
                code: applied ? .applied : .executionFailed)
              _ = try? await client.reportPlaybackCommandResult(
                operationId: command.operationId,
                input: PlaybackCommandResultInput(
                  status: applied ? .applied : .failed,
                  code: applied ? .applied : .executionFailed))
              self.lastPlaybackCommandID = command.operationId
              if !applied { break }
            } else if self.playbackPresentation != nil || self.minimizedPlaybackPresentation != nil {
              self.pendingPlaybackCommands.append(command)
              self.coordinationRecentActivityUntilNanoseconds = self.coordinationNow() + 30_000_000_000
              break
            } else {
              self.rememberCompletedPlaybackCommand(
                command.operationId, status: .failed, code: .invalidState)
              _ = try? await client.reportPlaybackCommandResult(
                operationId: command.operationId,
                input: PlaybackCommandResultInput(status: .failed, code: .invalidState))
              self.lastPlaybackCommandID = command.operationId
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
                expectedVersion: room.version)
              do { refreshed = try await client.updatePlaybackRoom(id: room.id, input: update) }
              catch { refreshed = try await client.playbackRoom(id: room.id) }
            } else {
              refreshed = try await client.playbackRoom(id: room.id)
            }
            self.activePlaybackRoom = refreshed.preservingJoinCode(from: room)
          } catch let RivuneAPIError.server(status, _, _, _) where status == 403 || status == 404 {
            self.activePlaybackRoom = nil
          } catch {
            // Keep the last room snapshot across transient network failures.
          }
        }
        let hasActiveWork = activePresentation != nil || self.activePlaybackRoom != nil
          || !self.pendingPlaybackCommands.isEmpty || self.executedPlaybackCommandID != nil
        let interval = Self.coordinationPollIntervalNanoseconds(
          hasActiveWork: hasActiveWork,
          recentActivityUntil: self.coordinationRecentActivityUntilNanoseconds,
          now: self.coordinationNow())
        let delay = Self.coordinationLoopDelayNanoseconds(
          commandInterval: interval,
          lastPresence: self.coordinationLastPresenceNanoseconds,
          now: self.coordinationNow())
        await self.coordinationSleep(delay)
      }
    }
  }

  private func startCoordinatedPlayback(
    _ item: CoordinatedPlaybackItem, positionMilliseconds: Int64, using client: RivuneAPIClient
  ) async {
    let capabilities = Self.playbackCapabilities(
      for: currentQuality, networkClass: networkClass,
      player: preferredPlayer, embedded: embeddedPlayerPreference)
    do {
      let sources = try await client.playbackSources(
        mediaType: item.mediaType, addonId: item.sourceAddonId, resourceId: item.resourceId,
        capabilities: capabilities)
      guard let option = sources.sources.first else { throw RivuneAPIError.invalidResponse }
      let start = Int(max(positionMilliseconds, 0) / 1_000)
      let selection = RivunePlaybackEnginePolicy.selection(
        for: embeddedPlayerPreference, protocol: option.protocol, container: option.container)
      let preserve = selection.engine == .mpv || selection.fallbackAllowed
      _ = try await client.preparePlayback(
        sourceRef: option.sourceRef, startSeconds: start, externalPlayer: preserve)
      let progress = try? await client.playbackProgress(titleId: item.titleId)
      let session = try await client.resolvePlayback(
        sourceRef: option.sourceRef, titleId: item.titleId.uuidString.lowercased(),
        startSeconds: start, externalPlayer: preserve)
      guard let selected = session.sources.first(where: { $0.id == session.selectedSourceId })
        ?? session.sources.first,
        let rawURL = selected.url, let url = resolvedResourceURL(rawURL)
      else { throw RivuneAPIError.invalidResponse }
      playbackPresentation = RivunePlaybackPresentation(
        id: UUID(), sessionId: session.id, sourceRef: option.sourceRef, titleId: item.titleId,
        title: item.title, url: url, engine: selection.engine,
        fallbackAllowed: selection.fallbackAllowed,
        startSeconds: start, timelineStartSeconds: start, mediaTimeline: selected.mediaTimeline,
        videoAspect: videoAspect, playbackSpeed: 1, markers: [],
        durationSeconds: selected.media?.durationSeconds.map { max(Int($0.rounded()), 0) }
          ?? progress?.durationSeconds,
        expectedVersion: progress?.version ?? 0,
        audioTracks: selected.media?.audioTracks ?? [], subtitles: session.subtitles,
        selectedAudioTrack: session.selectedAudioTrack,
        selectedSubtitleId: session.selectedSubtitleId,
        decisionReasons: selected.decision?.reasons ?? [],
        coordinatedItem: item, sourceAddonId: option.addonId, nextEpisode: nil)
      diagnostics.record(.playbackStarted)
    } catch {
      diagnostics.record(.playbackFailed)
      playbackPresentation = nil
      mediaFailure = .message("Playback handoff could not be started.")
    }
  }
  nonisolated private static func folderArtworkReference(_ resolved: ResolvedCollectionFolder)
    -> String?
  {
    if let sourcePosters = resolved.sourcePosterUrls {
      for source in resolved.folder.sources {
        if let id = source.id?.uuidString.lowercased(),
          let value = sourcePosters[id], !value.isEmpty
        {
          return value
        }
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
      isCurrent(generation)
    else { return false }
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
      case .server(_, let code, let message, _):
        switch code {
        case "device_quota_reached": return .deviceLimit
        case "device_code_capacity", "rate_limited": return .pairingCapacity
        case "invalid_profile_pin": return .invalidPin
        case "profile_pin_rate_limited": return .pinRateLimited
        case "session_expired", "invalid_access_token", "invalid_refresh_token":
          return .sessionExpired
        default: return .message(message)
        }
      default: return fallback
      }
    }
    if error is URLError { return .serverUnreachable }
    return fallback
  }
  nonisolated private static func searchItems(from result: AddonResourceResult)
    -> [RivuneSearchItem]
  {
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
        seriesId: nil, mappingProvider: nil, episodeOrderId: nil, metadataSeasonId: nil,
        seasonId: nil, seasonNumber: nil, episodeNumber: nil, runtimeMinutes: nil
      )
    }
  }

  nonisolated private static func searchItem(from item: CollectionItem) -> RivuneSearchItem {
    let addon = item.sources.first { $0.addonId != nil }
    return RivuneSearchItem(
      id: item.id,
      resourceId: item.id,
      mediaType: item.mediaType,
      title: item.title,
      titleId: UUID(uuidString: item.id),
      provider: semanticProvider(for: item),
      externalId: semanticExternalID(for: item),
      externalIds: item.externalIds,
      sourceAddonId: addon?.addonId,
      sourceCatalogId: addon?.catalogId,
      sourceName: addon?.title,
      posterUrl: item.posterUrl,
      backgroundUrl: item.backgroundUrl,
      logoUrl: item.logoUrl,
      overview: item.description,
      releaseInfo: item.releaseInfo,
      released: item.released,
      seriesId: nil,
      mappingProvider: nil,
      episodeOrderId: nil,
      metadataSeasonId: nil,
      seasonId: nil,
      seasonNumber: nil,
      episodeNumber: nil,
      runtimeMinutes: nil
    )
  }

  nonisolated private static func semanticProvider(for item: CollectionItem) -> String? {
    ["tmdb", "imdb", "tvdb", "trakt"].first { item.externalIds[$0]?.isEmpty == false }
  }

  nonisolated private static func semanticExternalID(for item: CollectionItem) -> String? {
    guard let provider = semanticProvider(for: item) else { return nil }
    return item.externalIds[provider]
  }

  nonisolated static func searchIdentities(_ item: RivuneSearchItem) -> Set<String> {
    var identities = Set(
      item.externalIds.compactMap { provider, value in
        value.isEmpty ? nil : "\(provider.lowercased()):\(value.lowercased())"
      })
    let namespace = item.id.split(separator: ":", maxSplits: 1).map(String.init)
    if namespace.count == 2,
      ["tmdb", "imdb", "tvdb", "trakt"].contains(namespace[0].lowercased())
    {
      identities.insert("\(namespace[0].lowercased()):\(namespace[1].lowercased())")
    } else if item.id.lowercased().hasPrefix("tt") {
      identities.insert("imdb:\(item.id.lowercased())")
    }
    if identities.isEmpty {
      let source = item.sourceAddonId?.uuidString.lowercased() ?? "native"
      let catalog = item.sourceCatalogId?.lowercased() ?? "none"
      identities.insert(
        "\(source):\(catalog):\(item.mediaType.lowercased()):\(item.id.lowercased())")
    }
    return identities
  }

  nonisolated private static func canonicalSearchIdentity(in identities: Set<String>) -> String {
    identities.min() ?? "search:unknown"
  }

  nonisolated private static func jsonString(_ value: JSONValue?) -> String? {
    guard case .string(let result) = value else { return nil }
    return result
  }
  nonisolated private static func target(from item: CollectionItem) -> RivuneMediaTarget {
    let addon = item.sources.first { $0.addonId != nil }
    return RivuneMediaTarget(
      id: item.id, resourceId: item.id, mediaType: item.mediaType, title: item.title,
      titleId: UUID(uuidString: item.id),
      provider: nil, externalId: nil, externalIds: item.externalIds, sourceAddonId: addon?.addonId,
      sourceCatalogId: addon?.catalogId, sourceName: addon?.title, posterUrl: item.posterUrl,
      backgroundUrl: item.backgroundUrl, logoUrl: item.logoUrl, overview: item.description,
      releaseInfo: item.releaseInfo,
      released: item.released, seriesId: nil, mappingProvider: nil, episodeOrderId: nil,
      metadataSeasonId: nil, seasonId: nil, seasonNumber: nil, episodeNumber: nil,
      runtimeMinutes: nil
    )
  }

  nonisolated private static func target(from item: RivuneAPI.LibraryItem) -> RivuneMediaTarget {
    RivuneMediaTarget(
      id: item.resourceId ?? item.externalId ?? item.titleId.uuidString,
      resourceId: item.resourceId ?? item.externalId ?? item.titleId.uuidString,
      mediaType: item.mediaType.rawValue, title: item.title ?? "Untitled", titleId: item.titleId,
      provider: item.provider, externalId: item.externalId,
      externalIds: item.provider.flatMap { provider in item.externalId.map { [provider: $0] } }
        ?? [:],
      sourceAddonId: item.sourceAddonId, sourceCatalogId: item.sourceCatalogId,
      sourceName: item.sourceName,
      posterUrl: item.posterUrl, backgroundUrl: item.backgroundUrl, logoUrl: nil, overview: nil,
      releaseInfo: item.releaseInfo,
      released: nil, seriesId: nil, mappingProvider: nil, episodeOrderId: nil,
      metadataSeasonId: nil, seasonId: nil, seasonNumber: nil, episodeNumber: nil,
      runtimeMinutes: nil
    )
  }

  nonisolated private static func target(from item: ContinueWatchingItem) -> RivuneMediaTarget {
    let episodeOrderID = item.episodeOrderId?.trimmingCharacters(in: .whitespacesAndNewlines)
    let metadataSeasonID = item.metadataSeasonId?.trimmingCharacters(in: .whitespacesAndNewlines)
    let mappingProvider =
      item.mappingProvider?.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() == "tvdb"
        && episodeOrderID?.isEmpty == false
        && metadataSeasonID?.isEmpty == false
      ? SeriesMappingProvider.tvdb : nil
    return RivuneMediaTarget(
      id: item.titleId.uuidString, resourceId: item.resourceId ?? item.titleId.uuidString,
      mediaType: item.mediaType.rawValue,
      title: item.title ?? item.episodeTitle ?? "Untitled", titleId: item.titleId,
      provider: item.resourceProvider,
      externalId: nil, externalIds: [:], sourceAddonId: nil, sourceCatalogId: nil, sourceName: nil,
      posterUrl: item.posterUrl, backgroundUrl: item.episodeStillUrl ?? item.backgroundUrl,
      logoUrl: nil,
      overview: nil, releaseInfo: item.releaseInfo, released: item.episodeAirDate,
      seriesId: item.seriesId, mappingProvider: mappingProvider,
      episodeOrderId: mappingProvider == nil ? nil : episodeOrderID,
      metadataSeasonId: mappingProvider == nil ? nil : metadataSeasonID,
      seasonId: item.seasonId?.uuidString, seasonNumber: item.seasonNumber,
      episodeNumber: item.episodeNumber, runtimeMinutes: nil
    )
  }

  nonisolated private static func target(from item: CalendarEvent) -> RivuneMediaTarget {
    RivuneMediaTarget(
      id: item.id, resourceId: item.resourceId ?? item.titleId.uuidString,
      mediaType: item.mediaType,
      title: item.title, titleId: item.titleId, provider: item.resourceProvider, externalId: nil,
      externalIds: [:],
      sourceAddonId: nil, sourceCatalogId: nil, sourceName: nil, posterUrl: item.posterUrl,
      backgroundUrl: nil,
      logoUrl: nil, overview: nil, releaseInfo: item.releaseDate, released: item.releaseDate,
      seriesId: item.seriesId, mappingProvider: nil, episodeOrderId: nil, metadataSeasonId: nil,
      seasonId: item.seasonId?.uuidString, seasonNumber: item.seasonNumber,
      episodeNumber: item.episodeNumber, runtimeMinutes: nil
    )
  }

  nonisolated static func episodeTarget(
    _ episode: Episode,
    series: Series?,
    source: RivuneMediaTarget
  ) -> RivuneMediaTarget {
    let selectedOrderID = series?.selectedEpisodeOrderId?
      .trimmingCharacters(in: .whitespacesAndNewlines)
    let selectedOrder = selectedOrderID.flatMap { selectedID in
      series?.episodeOrders.first { $0.id == selectedID }
    }
    let inheritedOrderID = source.episodeOrderId?
      .trimmingCharacters(in: .whitespacesAndNewlines)
    let inheritedSeasonID = source.metadataSeasonId?
      .trimmingCharacters(in: .whitespacesAndNewlines)
    let inheritedVariant =
      source.mappingProvider == .tvdb
        && inheritedOrderID?.isEmpty == false
        && inheritedSeasonID?.isEmpty == false
    let selectedVariant =
      selectedOrder != nil
        && selectedOrder?.type.trimmingCharacters(in: .whitespacesAndNewlines)
          .lowercased() != "official"
    let variant = selectedOrder == nil ? inheritedVariant : selectedVariant
    let episodeOrderID =
      selectedVariant ? selectedOrderID : (variant ? inheritedOrderID : nil)
    let metadataSeasonID = variant ? episode.seasonId : nil
    let persistedSeasonID =
      variant && source.metadataSeasonId == episode.seasonId
        ? source.seasonId ?? episode.seasonId : episode.seasonId
    return RivuneMediaTarget(
      id: episode.id.uuidString,
      resourceId: episodeResourceID(
        episode, series: series, episodeOrderId: episodeOrderID),
      mediaType: "episode",
      title: episode.name,
      titleId: episode.id,
      provider: nil,
      externalId: nil,
      externalIds: episode.externalIds,
      sourceAddonId: source.sourceAddonId,
      sourceCatalogId: source.sourceCatalogId,
      sourceName: source.sourceName,
      posterUrl: episode.stillUrl ?? source.posterUrl,
      backgroundUrl: episode.backdropUrl ?? episode.stillUrl ?? source.backgroundUrl,
      logoUrl: nil,
      overview: episode.overview,
      releaseInfo: "S\(episode.seasonNumber) E\(episode.episodeNumber)",
      released: episode.airDate,
      seriesId: series?.id ?? source.seriesId,
      mappingProvider: variant ? .tvdb : nil,
      episodeOrderId: episodeOrderID,
      metadataSeasonId: metadataSeasonID,
      seasonId: persistedSeasonID,
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
    guard
      let currentEpisodeIndex = currentSeason.episodes.firstIndex(where: {
        $0.id == currentEpisodeID
      })
    else { return nil }
    if currentEpisodeIndex + 1 < currentSeason.episodes.count {
      return episodeTarget(
        currentSeason.episodes[currentEpisodeIndex + 1], series: series, source: source)
    }
    let orderedSeasons = series.seasons.sorted {
      $0.seasonNumber == $1.seasonNumber
        ? $0.id < $1.id
        : $0.seasonNumber < $1.seasonNumber
    }
    guard let currentSeasonIndex = orderedSeasons.firstIndex(where: { $0.id == currentSeason.id })
    else { return nil }
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
      let episode = detail.episode
    else { return nil }
    return try? await resolveNextEpisodeTarget(
      series: series,
      currentSeason: season,
      currentEpisodeID: episode.id,
      source: detail.target
    ) { seasonID in
      try await client.season(id: seasonID, mappingProvider: series.mappingProvider)
    }
  }

  nonisolated private static func resolve(
    _ target: RivuneMediaTarget, using client: RivuneAPIClient
  ) async throws -> UUID {
    if let titleID = target.titleId { return titleID }
    let mediaType: TitleMediaType
    switch target.mediaType {
    case "movie": mediaType = .movie
    case "series": mediaType = .series
    case "tv": mediaType = .tv
    default: throw RivuneAPIError.invalidResponse
    }
    let preferredProvider = ["tmdb", "imdb", "tvdb", "trakt"].first {
      target.externalIds[$0]?.isEmpty == false
    }
    let namespace = target.id.split(separator: ":", maxSplits: 1).map(String.init)
    let provider =
      target.provider
      ?? (target.mediaType == "tv"
        ? "addon" : preferredProvider ?? (namespace.count == 2 ? namespace[0].lowercased() : nil))
      ?? (target.id.hasPrefix("tt") ? "imdb" : "addon")
    let externalID =
      target.externalId
      ?? (target.mediaType == "tv"
        ? target.resourceId : preferredProvider.flatMap { target.externalIds[$0] })
      ?? (namespace.count == 2 ? namespace[1] : target.id)
    return try await client.resolveTitle(
      TitleResolveInput(
        mediaType: mediaType, provider: provider, externalId: externalID,
        resourceId: target.resourceId, title: target.title, posterUrl: target.posterUrl,
        backgroundUrl: target.backgroundUrl, releaseInfo: target.releaseInfo,
        released: target.released, sourceAddonId: target.sourceAddonId,
        sourceCatalogId: target.sourceCatalogId, sourceName: target.sourceName)
    ).titleId
  }

  nonisolated private static func episodeResourceID(
    _ episode: Episode,
    series: Series?,
    episodeOrderId: String? = nil
  ) -> String {
    if episodeOrderId?.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty == false,
      let tvdb = episode.externalIds["tvdb"], !tvdb.isEmpty
    {
      return "tvdb:\(tvdb)"
    }
    if let imdb = series?.externalIds["imdb"] {
      return "\(imdb):\(episode.seasonNumber):\(episode.episodeNumber)"
    }
    return episode.externalIds["imdb"] ?? episode.externalIds["tvdb"].map { "tvdb:\($0)" }
      ?? episode.id.uuidString
  }

  nonisolated static func playbackCapabilities(
    for quality: RivuneNetworkQuality,
    networkClass: RivuneNetworkClass = .remoteWifi,
    player: RivunePlayerPreference,
    embedded: RivuneEmbeddedPlayerPreference,
    embeddedMPVSupported: Bool = RivunePlaybackEnginePolicy.embeddedMPVSupported
  ) -> PlaybackCapabilities {
    let maximum = RivuneNetworkQualityPolicy.limit(quality: quality, networkClass: networkClass)
    let useMPV = player == .external || embeddedMPVSupported && embedded != .native
    return PlaybackCapabilities(
      streamingProtocols: useMPV ? ["http", "hls", "dash"] : ["hls"],
      containers: useMPV
        ? [
          "mp4", "mkv", "matroska", "avi", "mov", "flv", "ts", "m2ts", "mpegts", "webm", "ogv",
          "ogg", "3gp", "mpeg",
        ] : ["mp4", "mov", "m4v", "mpegts"],
      videoCodecs: ["h264", "hevc"],
      audioCodecs: useMPV
        ? [
          "aac", "mp3", "mp2", "flac", "opus", "vorbis", "ac3", "eac3", "dts", "truehd", "alac",
          "wma",
        ] : ["aac", "ac3", "eac3"],
      externalPlayers: ["apple_open_url"], processingModes: [.remux, .transcodeAudio, .transcode],
      hlsSegmentContainer: useMPV ? nil : "mp4",
      maximumHeight: maximum.maximumHeight,
      maximumVideoBitrateKbps: maximum.maximumVideoBitrateKbps,
      maximumAudioChannels: 8, subtitleModes: [.external, .burn],
      mediaProfiles: useMPV
        ? nil
        : [
          PlaybackMediaProfile(
            container: "mp4", videoCodec: "h264", audioCodec: "aac", maximumVideoBitDepth: 8),
          PlaybackMediaProfile(
            container: "mp4", videoCodec: "hevc", audioCodec: "aac", maximumVideoBitDepth: 10),
        ]
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
      let scope = RivuneOfflineMediaScope(serverOrigin: serverOrigin, profileID: profile.id)
    else {
      lockOffline()
      return
    }
    if profile.hasPin, pin == nil {
      currentOfflineAccess = storedOfflineProfiles.first {
        $0.id == scope.identifier && $0.requiresPIN
      }
      guard let access = currentOfflineAccess else { return }
      Task { [weak self] in
        let items = await RivuneOfflineMediaStore.shared.items(in: scope)
        guard let self, self.activeProfile?.id == profile.id, self.offlineScope == nil,
          !items.isEmpty
        else { return }
        self.pendingOfflineProfile = access
      }
      return
    }
    guard
      let access = try? RivuneOfflineProfileAccess(
        name: profile.name, scope: scope, pin: profile.hasPin ? pin : nil)
    else {
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
        if !(await RivuneOfflineMediaStore.shared.items(in: scope)).isEmpty {
          available.append(access)
        }
      }
      guard let self else { return }
      self.offlineProfiles = available
    }
  }

  private func requestActiveOfflineUnlockIfAvailable() {
    guard let serverOrigin, let profile = activeProfile,
      let scope = RivuneOfflineMediaScope(serverOrigin: serverOrigin, profileID: profile.id),
      let access = currentOfflineAccess.flatMap({ $0.id == scope.identifier ? $0 : nil })
        ?? storedOfflineProfiles.first(where: { $0.id == scope.identifier })
    else { return }
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
    if let operationId = activeSearchOperationID {
      finishSearchDiagnostic(.searchCanceled, operationId: operationId)
    }
    tabOperation?.cancel()
    tabOperation = nil
    tabGeneration &+= 1
    cancelPendingSearchPublication()
    immediatelyPublishedSearchGeneration = nil
  }

  private func finishSearchDiagnostic(
    _ code: RivuneDiagnosticEventCode, operationId: UUID
  ) {
    guard activeSearchOperationID == operationId else { return }
    activeSearchOperationID = nil
    diagnostics.record(code, operationId: operationId)
  }

  private func isCurrentTab(_ value: UInt64) -> Bool {
    !Task.isCancelled && value == tabGeneration
  }
  private func isCurrentMedia(_ value: UInt64) -> Bool {
    !Task.isCancelled && value == mediaGeneration
  }

  private func resetTabState() {
    if let operationId = activeSearchOperationID {
      finishSearchDiagnostic(.searchCanceled, operationId: operationId)
    }
    tabGeneration &+= 1
    tabOperation?.cancel()
    tabOperation = nil
    cancelPendingSearchPublication()
    immediatelyPublishedSearchGeneration = nil
    coordinationOperation?.cancel()
    coordinationOperation = nil
    recommendationOperation?.cancel()
    recommendationOperation = nil
    playbackCoordinationAvailable = false
    localRecommendationsAvailable = false
    semanticSearchAvailable = false
    playbackDevices = []
    activePlaybackRoom = nil
    pendingPlaybackCommands = []
    lastPlaybackCommandID = nil
    coordinationStatus = "idle"
    coordinationPositionMilliseconds = 0
    coordinationDurationMilliseconds = 0
    coordinationEndedPlaybackID = nil
    executedPlaybackCommandID = nil
    coordinationLastPresenceNanoseconds = nil
    coordinationRecentActivityUntilNanoseconds = 0
    selectedTab = startupTab
    searchQuery = ""
    searchItems = []
    searchIdentityOwners = [:]
    searchPresentations = [:]
    searchPage = 0
    searchHasMore = false
    searchPartial = false
    searchIntents = []
    excludedSearchIntentIDs = []
    searchDescriptors = []
    searchMediaTypes = []
    searchMediaType = nil
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
    tabFailure = nil
    profileArchiveAvailable = false
    profileArchiveReport = nil
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
    pairingRetrySeconds = nil
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
    pairingRetrySeconds = nil
    profiles = []
    profileAvatarData = [:]
    resetProfileSettings()
    activeProfile = nil
    collections = []
    folderArtworkURLs = [:]
    resetTabState()
    openedFolder = nil
    failure = nil
    profileExperienceOperation?.cancel()
    profileExperienceOperation = nil
    playbackFailoverOperation?.cancel()
    playbackFailoverOperation = nil
    activePlaybackFailover = nil
    playbackFailoverGate = RivunePlaybackFailoverGate()
    readingQueue = nil
    savedSearches = []
    smartCollections = []
    smartCollectionPage = nil
    mediaNotifications = []
    mediaNotificationSubscriptions = []
    extensionIncidents = []
    accessibilityPreferences = nil
    profileExperienceLoading = false
    profileExperienceFailure = nil
    profileConflictMessage = nil
    playbackFailoverNotice = nil
    playbackFailoverLoading = false
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
  private static let installationIDKey = "rivune.installation.id"
  private static let offlineScopeKey = "rivune.offline.scope"
  private static let accentKey = "rivune.appearance.accent"
  private static let playerKey = "rivune.playback.player"
  private static let embeddedPlayerKey = "rivune.playback.embedded-player"
  private static let startupTabKey = "rivune.navigation.startup-tab"
  private static let animationKey = "rivune.appearance.animations"
  private static let recommendationLayoutKey = "rivune.appearance.recommendation-layout"
  private static let frameRateKey = "rivune.playback.frame-rate"
  private static let videoAspectKey = "rivune.playback.aspect"
  private static let localQualityKey = "rivune.quality.local"
  private static let remoteWifiQualityKey = "rivune.quality.remote-wifi"
  private static let mobileQualityKey = "rivune.quality.mobile"
  private static let legacyWifiQualityKey = "rivune.playback.wifi-quality"
  private static let legacyMobileQualityKey = "rivune.playback.mobile-quality"
  private static let offlineProfilesKey = "rivune.offline.profiles"
  private static let offlineExpirationDaysKey = "rivune.offline.expiration-days"
  private static let showStreamsKey = "rivune.playback.show-streams"
  private static let skipIntroKey = "rivune.playback.skip-intro"
  private static let skipRecapKey = "rivune.playback.skip-recap"
  private static let skipOutroKey = "rivune.playback.skip-outro"
  private static let lastUpdateVersionKey = "rivune.update.last-successful-version"
  private static let lastNotifiedUpdateKey = "rivune.update.last-notified-version"
  private static let lastUpdateCheckKey = "rivune.update.last-successful-check"
  private static let updateCacheKey = "rivune.update.cached-result"

  nonisolated static func classifyNetwork(
    cellular: Bool, expensive: Bool, constrained: Bool, serverOrigin: URL?
  ) -> RivuneNetworkClass {
    if cellular || expensive || constrained { return .mobile }
    guard let host = serverOrigin?.host?.lowercased() else { return .remoteWifi }
    return isLANHost(host) ? .local : .remoteWifi
  }

  nonisolated private static func isLANHost(_ rawHost: String) -> Bool {
    let host = rawHost.trimmingCharacters(in: CharacterSet(charactersIn: "[]")).lowercased()
    if host == "localhost" || host == "::1" || host.hasSuffix(".local") { return true }
    if host.contains(":") {
      if host.hasPrefix("fe8") || host.hasPrefix("fe9") || host.hasPrefix("fea")
        || host.hasPrefix("feb") { return true }
      guard let first = host.split(separator: ":").first, let prefix = UInt16(first, radix: 16) else {
        return false
      }
      return prefix & 0xfe00 == 0xfc00
    }
    let octets = host.split(separator: ".", omittingEmptySubsequences: false).compactMap { Int($0) }
    guard octets.count == 4, octets.allSatisfy({ 0...255 ~= $0 }) else { return false }
    return octets[0] == 10 || octets[0] == 127
      || (octets[0] == 169 && octets[1] == 254)
      || (octets[0] == 172 && 16...31 ~= octets[1])
      || (octets[0] == 192 && octets[1] == 168)
  }

  private static func origin(of url: URL) -> URL? {
    guard var components = URLComponents(url: url, resolvingAgainstBaseURL: true),
      let scheme = components.scheme?.lowercased(), components.host != nil
    else { return nil }
    components.scheme = scheme
    if (scheme == "https" && components.port == 443) || (scheme == "http" && components.port == 80)
    {
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

  private func rememberCompletedPlaybackCommand(
    _ operationId: UUID,
    status: PlaybackCommandResultStatus = .applied,
    code: PlaybackCommandResultCode = .applied
  ) {
    coordinationRecentActivityUntilNanoseconds = coordinationNow() + 30_000_000_000
    guard completedPlaybackCommandResults[operationId] == nil else { return }
    completedPlaybackCommandResults[operationId] = PlaybackCommandResultInput(status: status, code: code)
    completedPlaybackCommandOrder.append(operationId)
    if completedPlaybackCommandOrder.count > Self.maximumCompletedPlaybackCommands {
      let overflow = completedPlaybackCommandOrder.count - Self.maximumCompletedPlaybackCommands
      let removed = Array(completedPlaybackCommandOrder.prefix(overflow))
      completedPlaybackCommandOrder.removeFirst(overflow)
      for operationId in removed { completedPlaybackCommandResults.removeValue(forKey: operationId) }
    }
    let records = completedPlaybackCommandOrder.compactMap { operationId -> CompletedPlaybackCommandRecord? in
      guard let result = completedPlaybackCommandResults[operationId] else { return nil }
      return CompletedPlaybackCommandRecord(
        operationId: operationId, status: result.status, code: result.code)
    }
    if let data = try? JSONEncoder().encode(records) {
      defaults.set(data, forKey: Self.completedPlaybackCommandsKey)
    }
  }

  private static let completedPlaybackCommandsKey = "rivune.playback.completed-operations-v22"
}
