export type InterfaceLanguage = "en" | "fr" | "fr-CA" | "es" | "es-MX" | "es-AR" | "es-CL" | "es-CO" | "es-PE" | "it" | "de" | "ru" | "pt-PT" | "pt-BR" | "ar" | "ja" | "ko" | "zh-CN" | "zh-TW" | "pl" | "hy" | "nl" | "sv" | "da" | "fi" | "nb" | "tr" | "uk" | "cs" | "sk" | "ro" | "el" | "he" | "hi" | "id" | "vi" | "th" | "hu" | "bg" | "hr" | "sr" | "ms" | "ca" | "fa" | "fil";

export type Discovery = {
  name: string;
  serverVersion: string;
  protocolVersion: number;
  apiBaseUrl: string;
  setupRequired: boolean;
  timezone: string;
  interfaceLanguage: InterfaceLanguage;
};

export type TokenPair = {
  tokenType: "Bearer";
  accessToken: string;
  accessTokenExpiresAt: string;
  refreshToken: string;
  refreshTokenExpiresAt: string;
  sessionId: string;
  deviceId: string;
};

export type Avatar = { kind: "preset" | "custom"; presetId?: string; url: string };
export type Profile = {
  id: string;
  name: string;
  isChild: boolean;
  hasPin?: boolean;
  canManage: boolean;
  enabled: boolean;
  availableFrom: string | null;
  availableUntil: string | null;
  accessStartTime: string | null;
  accessEndTime: string | null;
  accessTimezone: string;
  accessible: boolean;
  avatar: Avatar;
};
export type Account = {
  user: { id: string; username: string; role: string };
  session: { id: string; deviceId: string; activeProfile: { id: string; expiresAt: string } | null };
  profiles: Profile[];
  maintenance: { enabled: boolean; message: string | null };
};
export type ProfileSession = {
  id: string;
  userId: string;
  username: string;
  deviceId: string;
  deviceName: string;
  platform: string;
  ipAddress: string | null;
  createdAt: string;
  lastSeenAt: string;
  profileGrantExpiresAt: string;
  current: boolean;
};
export type SessionNotification = {
  id: string;
  message: string;
  senderUsername: string;
  createdAt: string;
};
export type NotificationBroadcast = {
  id: string;
  message: string;
  senderUsername: string;
  recipientCount: number;
  createdAt: string;
};
export type DeviceAuthorization = {
  deviceCode: string;
  userCode: string;
  verificationUri: string;
  verificationUriComplete: string;
  expiresAt: string;
  intervalSeconds: number;
};


export type CollectionExtra = { name: string; value: string };
export type AddonCatalogSource = { addonId: string; type: string; catalogId: string; extra?: CollectionExtra[] };
export type TMDBFilters = {
  genres?: number[];
  releaseDateFrom?: string;
  releaseDateTo?: string;
  voteAverageMin?: number;
  voteAverageMax?: number;
  voteCountMin?: number;
  originalLanguage?: string;
  originCountry?: string;
  keywords?: number[];
  companies?: number[];
  networks?: number[];
  year?: number;
  watchRegion?: string;
  watchProviders?: number[];
};
export type TMDBSource = {
  sourceType: "list" | "company" | "network" | "collection" | "person" | "director" | "discover";
  tmdbId?: number;
  mediaType: "movie" | "series" | "both";
  sort: string;
  filters: TMDBFilters;
};
export type TraktSource = { listId: number; mediaType: "movie" | "series"; sortBy: string; sortHow: "asc" | "desc" };
export type MDBListSource = { listId: number; mediaType: "movie" | "series"; sort: string; order: "asc" | "desc" };
export type CollectionSource = {
  id?: string;
  kind: "addon_catalog" | "tmdb" | "trakt" | "mdblist";
  title: string;
  addonCatalog?: AddonCatalogSource;
  tmdb?: TMDBSource;
  trakt?: TraktSource;
  mdblist?: MDBListSource;
};
export type CollectionFolder = {
  id?: string;
  title: string;
  tileShape: "poster" | "landscape" | "square";
  sourceView?: "merged" | "categories" | "folders";
  coverImageUrl?: string;
  coverEmoji?: string;
  titleLogoUrl?: string;
  heroBackdropUrl?: string;
  heroVideoUrl?: string;
  focusGifUrl?: string;
  focusGifEnabled: boolean;
  hideTitle: boolean;
  sources: CollectionSource[];
};
export type Collection = {
  id: string;
  title: string;
  heroEnabled: boolean;
  backdropImageUrl?: string;
  pinToTop: boolean;
  focusGlowEnabled: boolean;
  viewMode: "tabbed_grid" | "rows" | "follow_layout";
  folderCoverShape: "poster" | "landscape" | "square";
  folders: CollectionFolder[];
  profileIds: string[];
  position: number;
  version: number;
  createdAt: string;
  updatedAt: string;
};
export type CollectionSaveInput = Omit<Collection, "id" | "position" | "version" | "createdAt" | "updatedAt"> & { expectedVersion?: number };
export type PortableAddonCatalogSource = Omit<AddonCatalogSource, "addonId"> & {
  addonId?: string;
  manifestId?: string;
};
export type PortableCollectionSource = Omit<CollectionSource, "id" | "addonCatalog"> & {
  addonCatalog?: PortableAddonCatalogSource;
};
export type PortableCollectionFolder = Omit<CollectionFolder, "id" | "sourceView" | "sources"> & {
  sourceView: NonNullable<CollectionFolder["sourceView"]>;
  sources: PortableCollectionSource[];
};
export type PortableCollection = Omit<CollectionSaveInput, "expectedVersion" | "folders" | "profileIds"> & {
  folders: PortableCollectionFolder[];
};
export type CollectionExportDocument = {
  schemaVersion: 1;
  exportedAt?: string;
  collections: PortableCollection[];
};
export type CollectionImportResult = { imported: number; collections: Collection[] };

export type SourceReference = { id: string; kind: string; title: string; addonId?: string };
export type MediaItem = {
  id: string;
  titleId?: string;
  mediaType: string;
  seasonNumber?: number;
  episodeNumber?: number;
  title: string;
  posterUrl?: string;
  backgroundUrl?: string;
  logoUrl?: string;
  description?: string;
  releaseInfo?: string;
  released?: string;
  voteAverage?: number;
  voteCount?: number;
  popularity?: number;
  externalIds?: Record<string, string>;
  sources?: SourceReference[];
  raw?: Record<string, unknown>;
};
export type TrailerMetadata = {
  youtubeId: string;
  name: string;
  language: string;
  isFallback: boolean;
  captionPreference?: string;
};
export type TrailerList = {
  trailers: TrailerMetadata[];
};
export type EpisodeMetadata = {
  id: string;
  mediaType: "episode";
  seasonId: string;
  name: string;
  overview: string;
  seasonNumber: number;
  episodeNumber: number;
  airDate?: string;
  stillUrl?: string;
  runtimeMinutes?: number;
  voteAverage: number;
  voteCount: number;
  externalIds: Record<string, string>;
};
export type SeasonSummary = {
  id: string;
  mediaType: "season";
  seriesId: string;
  name: string;
  overview: string;
  seasonNumber: number;
  episodeCount: number;
  airDate?: string;
  posterUrl?: string;
  voteAverage: number;
  externalIds: Record<string, string>;
};
export type SeasonMetadata = SeasonSummary & { episodes: EpisodeMetadata[] };
export type SeriesMetadata = {
  id: string;
  mediaType: "series";
  name: string;
  originalName: string;
  originalLanguage: string;
  overview: string;
  firstAirDate?: string;
  lastAirDate?: string;
  posterUrl?: string;
  backdropUrl?: string;
  logoUrl?: string;
  tagline?: string;
  status?: string;
  numberOfSeasons?: number;
  numberOfEpisodes?: number;
  genres: Array<{ id: number; name: string }>;
  voteAverage: number;
  voteCount: number;
  seasons: SeasonSummary[];
  episodeOrders: Array<{ id: string; name: string; type: string; isDefault: boolean }>;
  selectedEpisodeOrderId?: string;
  mappingProvider: "tmdb" | "tvdb";
  externalIds: Record<string, string>;
};
export type ResolvedFolder = {
  collectionId: string;
  folder: CollectionFolder;
  items: MediaItem[];
  page: number;
  hasMore: boolean;
  errors: { sourceId: string; kind: string; code: string; message: string }[];
};

export type AddonManifest = {
  id: string;
  name: string;
  version: string;
  description?: string;
  logo?: string;
  background?: string;
  types: string[];
  behaviorHints?: { adult?: boolean; p2p?: boolean; configurable?: boolean; configurationRequired?: boolean };
};
export type InstalledAddon = {
  id: string;
  transportUrl: string;
  manifest: AddonManifest;
  position: number;
  profileIds: string[];
  installedAt: string;
  updatedAt: string;
};
export type ResourceResult = { addonId: string; manifestId: string; transportUrl: string; resource: string; type: string; id: string; payload: Record<string, unknown> };
export type ResourceBatch = { results: ResourceResult[]; errors: { addonId: string; manifestId: string; code: string; message: string }[] };

export type PlaybackMediaProfile = { container: string; videoCodec: string; audioCodec?: string };
export type PlaybackCapabilities = {
  streamingProtocols: string[];
  containers: string[];
  videoCodecs?: string[];
  audioCodecs?: string[];
  hdrFormats?: string[];
  externalPlayers?: string[];
  processingModes?: ("remux")[];
  mediaProfiles?: PlaybackMediaProfile[];
};
export type PlaybackSourceOption = {
  id: string;
  sourceRef: string;
  addonId: string;
  manifestId: string;
  streamIndex: number;
  name: string;
  description?: string;
  filename?: string;
  protocol: string;
  container?: string;
  expiresAt: string;
};
export type PlaybackProviderFailure = { addonId: string; manifestId: string; code: string; message: string };
export type PlaybackSourceList = { sources: PlaybackSourceOption[]; providerErrors: PlaybackProviderFailure[] };

export type PlaybackMediaTrack = {
  index: number;
  type: string;
  codec: string;
  profile?: string;
  language?: string;
  title?: string;
  forced?: boolean;
  width?: number;
  height?: number;
  channels?: number;
};
export type PlaybackMediaInspection = {
  container?: string;
  durationSeconds?: number;
  hdrFormat?: string;
  videoTracks: PlaybackMediaTrack[];
  audioTracks: PlaybackMediaTrack[];
  subtitleTracks: PlaybackMediaTrack[];
};
export type PlaybackPreparation = {
  sourceRef: string;
  mode: "direct" | "remux" | "transcode_audio" | "transcode" | "youtube" | "external";
  protocol: string;
  container?: string;
  media?: PlaybackMediaInspection;
  subtitleCount: number;
  expiresAt: string;
};

export type PlaybackSource = {
  id: string;
  addonId: string;
  manifestId: string;
  name?: string;
  title?: string;
  mode: "direct" | "remux" | "transcode_audio" | "transcode" | "youtube" | "external";
  url?: string;
  ytId?: string;
  infoHash?: string;
  fileIndex?: number;
  protocol: string;
  container?: string;
  compatible: boolean;
  media?: PlaybackMediaInspection;
};
export type PlaybackSubtitle = { id: string; addonId: string; manifestId: string; language?: string; url: string; forced?: boolean; default?: boolean };
export type PlaybackMarker = {
  type: "intro" | "recap" | "outro";
  startSeconds: number;
  endSeconds: number;
  confidence: number;
  submissionCount: number;
};
export type PlaybackMarkerList = { markers: PlaybackMarker[] };
export type PlaybackSession = {
  id: string;
  selectedSourceId: string;
  selectedAudioTrack?: number;
  selectedSubtitleId?: string;
  sources: PlaybackSource[];
  subtitles: PlaybackSubtitle[];
  providerErrors: PlaybackProviderFailure[];
  expiresAt: string;
};
export type PlaybackActivitySession = {
  id: string;
  titleId?: string;
  artworkUrl?: string;
  externalIds?: Partial<Record<"imdb" | "tmdb" | "tvdb", string>>;
  externalIdMediaTypes?: Partial<Record<"imdb" | "tmdb" | "tvdb", "movie" | "series" | "season" | "episode">>;
  title: string;
  mediaType: string;
  mode: "direct" | "remux" | "transcode_audio" | "transcode" | "unknown";
  username: string;
  profileId: string;
  profile: string;
  device: string;
  platform: string;
  processing: boolean;
  positionSeconds: number;
  durationSeconds: number;
  createdAt: string;
  lastSeenAt: string;
  expiresAt: string;
};
export type PlaybackMediaJob = {
  sessionId?: string;
  assetId: string;
  mode: string;
  state: "processing" | "complete" | "failed";
  prewarming: boolean;
  createdAt: string;
  lastSeenAt: string;
};
export type PlaybackActivity = {
  summary: {
    activeSessions: number;
    activeJobs: number;
    processingSlots: number;
    processingLimit: number;
    storageBytes: number;
    storageLimitBytes: number;
  };
  diagnostics: { videoEncoder: string; hardwareToneMap: boolean };
  sessions: PlaybackActivitySession[];
  jobs: PlaybackMediaJob[];
};
export type PlaybackPurgeResult = { sessionsRemoved: number; jobsStopped: number; storageBytes: number };
export type PlaybackProgress = { titleId: string; mediaType?: string; positionSeconds: number; durationSeconds: number; completed: boolean; version: number; updatedAt?: string };
export type LibraryItem = {
  titleId: string;
  mediaType: string;
  provider?: string;
  externalId?: string;
  resourceId?: string;
  title?: string;
  posterUrl?: string;
  backgroundUrl?: string;
  releaseInfo?: string;
  released?: string;
  addedAt: string;
  updatedAt: string;
};
export type LibraryPage = { items: LibraryItem[]; page: number; totalPages: number; totalResults: number };
export type CalendarEvent = {
  id: string;
  titleId: string;
  mediaType: "movie" | "episode";
  title: string;
  releaseDate: string;
  posterUrl?: string;
  resourceId?: string;
  resourceProvider?: string;
  seriesTitle?: string;
  seriesId?: string;
  seasonId?: string;
  seasonNumber?: number;
  episodeNumber?: number;
};
export type CalendarResponse = { events: CalendarEvent[] };
export type TitleReference = {
  titleId: string;
  mediaType: string;
  provider: string;
  externalId: string;
  resourceId: string;
  title: string;
  posterUrl?: string;
  backgroundUrl?: string;
  releaseInfo?: string;
  released?: string;
};
export type ContinueItem = {
  titleId: string;
  mediaType: "movie" | "episode";
  seriesId?: string;
  seasonId?: string;
  seasonNumber?: number;
  episodeNumber?: number;
  positionSeconds: number;
  durationSeconds: number;
  version: number;
  reason: "resume" | "next_episode";
  title?: string;
  posterUrl?: string;
  backgroundUrl?: string;
  releaseInfo?: string;
  resourceId?: string;
  resourceProvider?: string;
  lastWatchedAt: string;
};
export type ContinueWatching = { items: ContinueItem[] };

export type TrackingProvider = "trakt" | "simkl";
export type TrackingStatus = {
  provider: TrackingProvider;
  configured: boolean;
  connected: boolean;
  syncWatched: boolean;
  syncProgress: boolean;
  syncLibrary: boolean;
  connectedAt?: string;
  lastSuccessAt?: string;
  lastError?: string;
  pendingItems: number;
};
export type TrackingDeviceAuthorization = {
  id: string;
  provider: TrackingProvider;
  userCode: string;
  verificationUrl: string;
  expiresAt: string;
  intervalSeconds: number;
};
export type TrackingPreferences = Partial<Pick<TrackingStatus, "syncWatched" | "syncProgress" | "syncLibrary">>;

export type SettingsValues = {
  interfaceLanguage?: InterfaceLanguage | null;
  theme?: string | null;
  maximumResolution?: string | null;
  preferDirectPlay?: boolean | null;
  hideUnreleased?: boolean | null;
  metadataLanguage?: string | null;
  metadataRegion?: string | null;
  seriesMappingProvider?: "tmdb" | "tvdb" | null;
  audioLanguage?: string | null;
  subtitleLanguage?: string | null;
  forcedSubtitleLanguage?: string | null;
  autoplayNextEpisode?: boolean | null;
  skipIntroEnabled?: boolean | null;
  skipRecapEnabled?: boolean | null;
  skipOutroEnabled?: boolean | null;
  cardDensity?: "comfortable" | "compact" | null;
  animationsEnabled?: boolean | null;
  subtitleSizePercent?: number | null;
  subtitleTextColor?: string | null;
  subtitleBackgroundOpacityPercent?: number | null;
  notificationsEnabled?: boolean | null;
  notificationDurationSeconds?: number | null;
  notificationPollIntervalSeconds?: number | null;
};
export type MaintenanceSettings = { enabled: boolean; message: string | null };
export type SettingsLayer = { schemaVersion: number; settings: SettingsValues; updatedAt?: string };
export type AvatarPreset = { id: string; name: string; url: string };
