import Foundation

public enum RivuneProtocol {
    public static let version = 16
}

public struct Discovery: Codable, Sendable, Equatable {
    public let name: String
    public let serverVersion: String
    public let protocolVersion: Int
    public let apiBaseUrl: String
    public let setupRequired: Bool
    public let timezone: String
}

public struct Device: Codable, Sendable, Equatable {
    public let id: UUID?
    public let name: String
    public let platform: String

    public init(id: UUID? = nil, name: String, platform: String) {
        self.id = id
        self.name = name
        self.platform = platform
    }
}

public struct LoginRequest: Codable, Sendable, Equatable {
    public let username: String
    public let password: String
    public let device: Device

    public init(username: String, password: String, device: Device) {
        self.username = username
        self.password = password
        self.device = device
    }
}

public struct TokenPair: Codable, Sendable, Equatable {
    public let tokenType: String
    public let accessToken: String
    public let accessTokenExpiresAt: String
    public let refreshToken: String
    public let refreshTokenExpiresAt: String
    public let sessionId: UUID
    public let deviceId: UUID
}

public struct Account: Codable, Sendable, Equatable {
    public struct User: Codable, Sendable, Equatable {
        public let id: UUID
        public let username: String
        public let role: String
    }

    public struct Session: Codable, Sendable, Equatable {
        public let id: UUID
        public let deviceId: UUID
        public let activeProfile: ActiveProfileGrant?
    }

    public let user: User
    public let session: Session
    public let profiles: [Profile]
}

public struct ActiveProfileGrant: Codable, Sendable, Equatable {
    public let id: UUID
    public let expiresAt: String
}

public struct ProfileList: Codable, Sendable, Equatable {
    public let profiles: [Profile]
}

public struct Profile: Codable, Sendable, Equatable, Identifiable {
    public let id: UUID
    public let name: String
    public let isChild: Bool
    public let hasPin: Bool
    public let canManage: Bool
    public let enabled: Bool
    public let availableFrom: String?
    public let availableUntil: String?
    public let accessStartTime: String?
    public let accessEndTime: String?
    public let accessTimezone: String
    public let accessible: Bool
    public let avatar: ProfileAvatar
}

public struct ProfileAvatar: Codable, Sendable, Equatable {
    public let kind: String
    public let presetId: String?
    public let url: String
}

public struct ProfileSelection: Codable, Sendable, Equatable {
    public let profile: Profile
    public let expiresAt: String
}

public enum MediaType: String, Codable, Sendable {
    case movie
    case series
    case season
    case episode
}

public enum SeriesMappingProvider: String, Codable, Sendable {
    case tmdb
    case tvdb
}

public struct Genre: Codable, Sendable, Equatable {
    public let id: Int
    public let name: String
}

public struct Movie: Codable, Sendable, Equatable, Identifiable {
    public let id: UUID
    public let mediaType: MediaType
    public let title: String
    public let originalTitle: String
    public let originalLanguage: String
    public let overview: String
    public let releaseDate: String?
    public let posterUrl: String?
    public let backdropUrl: String?
    public let tagline: String?
    public let runtimeMinutes: Int?
    public let genres: [Genre]
    public let voteAverage: Double
    public let voteCount: Int
    public let externalIds: [String: String]
}

public struct Series: Codable, Sendable, Equatable, Identifiable {
    public let id: UUID
    public let mediaType: MediaType
    public let name: String
    public let originalName: String
    public let originalLanguage: String
    public let overview: String
    public let firstAirDate: String?
    public let lastAirDate: String?
    public let posterUrl: String?
    public let backdropUrl: String?
    public let tagline: String?
    public let status: String?
    public let numberOfSeasons: Int?
    public let numberOfEpisodes: Int?
    public let genres: [Genre]
    public let voteAverage: Double
    public let voteCount: Int
    public let seasons: [SeasonSummary]
    public let aliases: [SeriesAlias]
    public let episodeOrders: [EpisodeOrder]
    public let mappingProvider: SeriesMappingProvider
    public let externalIds: [String: String]
}

public struct SeriesAlias: Codable, Sendable, Equatable {
    public let language: String
    public let name: String
}

public struct EpisodeOrder: Codable, Sendable, Equatable, Identifiable {
    public let id: String
    public let name: String
    public let type: String
    public let isDefault: Bool
}

public struct SeasonSummary: Codable, Sendable, Equatable, Identifiable {
    public let id: String
    public let mediaType: MediaType
    public let seriesId: UUID
    public let name: String
    public let overview: String
    public let seasonNumber: Int
    public let episodeCount: Int
    public let airDate: String?
    public let posterUrl: String?
    public let voteAverage: Double
    public let externalIds: [String: String]
}

public struct Season: Codable, Sendable, Equatable, Identifiable {
    public let id: String
    public let mediaType: MediaType
    public let seriesId: UUID
    public let name: String
    public let overview: String
    public let seasonNumber: Int
    public let airDate: String?
    public let posterUrl: String?
    public let voteAverage: Double
    public let episodes: [Episode]
    public let externalIds: [String: String]
}

public struct Episode: Codable, Sendable, Equatable, Identifiable {
    public let id: UUID
    public let mediaType: MediaType
    public let seasonId: String
    public let name: String
    public let overview: String
    public let seasonNumber: Int
    public let episodeNumber: Int
    public let airDate: String?
    public let stillUrl: String?
    public let runtimeMinutes: Int?
    public let voteAverage: Double
    public let voteCount: Int
    public let externalIds: [String: String]
}

public struct TrailerList: Codable, Sendable, Equatable {
    public let trailers: [Trailer]
}

public struct Trailer: Codable, Sendable, Equatable, Identifiable {
    public var id: String { youtubeId }
    public let youtubeId: String
    public let name: String
    public let language: String
    public let isFallback: Bool
    public let captionPreference: String?
}

public struct PlaybackCapabilities: Codable, Sendable, Equatable {
    public let streamingProtocols: [String]
    public let containers: [String]
    public let videoCodecs: [String]?
    public let audioCodecs: [String]?
    public let hdrFormats: [String]?
    public let externalPlayers: [String]?

    public init(streamingProtocols: [String], containers: [String], videoCodecs: [String]? = nil, audioCodecs: [String]? = nil, hdrFormats: [String]? = nil, externalPlayers: [String]? = nil) {
        self.streamingProtocols = streamingProtocols
        self.containers = containers
        self.videoCodecs = videoCodecs
        self.audioCodecs = audioCodecs
        self.hdrFormats = hdrFormats
        self.externalPlayers = externalPlayers
    }
}

public struct PlaybackSourceList: Codable, Sendable, Equatable {
    public let sources: [PlaybackSourceOption]
    public let providerErrors: [PlaybackProviderError]
}

public struct PlaybackSourceOption: Codable, Sendable, Equatable, Identifiable {
    public let id: String
    public let sourceRef: String
    public let addonId: UUID
    public let manifestId: String
    public let streamIndex: Int
    public let name: String
    public let description: String?
    public let filename: String?
    public let `protocol`: String
    public let container: String?
    public let expiresAt: String
}

public enum PlaybackMode: String, Codable, Sendable {
    case direct
    case remux
    case transcodeAudio = "transcode_audio"
    case transcode
    case youtube
    case external
}

public struct PlaybackPreparation: Codable, Sendable, Equatable {
    public let sourceRef: String
    public let mode: PlaybackMode
    public let `protocol`: String
    public let container: String?
    public let media: PlaybackMediaInspection?
    public let subtitleCount: Int
    public let expiresAt: String
}

public struct PlaybackSession: Codable, Sendable, Equatable, Identifiable {
    public let id: UUID
    public let selectedSourceId: String
    public let sources: [PlaybackSource]
    public let subtitles: [PlaybackSubtitle]
    public let providerErrors: [PlaybackProviderError]
    public let expiresAt: String
}

public struct PlaybackSource: Codable, Sendable, Equatable, Identifiable {
    public let id: String
    public let addonId: UUID
    public let manifestId: String
    public let name: String?
    public let title: String?
    public let mode: PlaybackMode
    public let url: String?
    public let ytId: String?
    public let infoHash: String?
    public let fileIndex: Int?
    public let `protocol`: String
    public let container: String?
    public let compatible: Bool
    public let media: PlaybackMediaInspection?
}

public struct PlaybackMediaInspection: Codable, Sendable, Equatable {
    public let container: String?
    public let durationSeconds: Double?
    public let hdrFormat: String?
    public let videoTracks: [PlaybackMediaTrack]
    public let audioTracks: [PlaybackMediaTrack]
    public let subtitleTracks: [PlaybackMediaTrack]
}

public struct PlaybackMediaTrack: Codable, Sendable, Equatable {
    public let index: Int
    public let type: String
    public let codec: String
    public let profile: String?
    public let language: String?
    public let title: String?
    public let width: Int?
    public let height: Int?
    public let channels: Int?
}

public struct PlaybackSubtitle: Codable, Sendable, Equatable, Identifiable {
    public let id: String
    public let addonId: UUID
    public let manifestId: String
    public let language: String?
    public let url: String
}

public struct PlaybackProviderError: Codable, Sendable, Equatable {
    public let addonId: UUID
    public let manifestId: String
    public let code: String
    public let message: String
}
