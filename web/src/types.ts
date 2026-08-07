export type InterfaceLanguage = "en" | "fr" | "fr-CA" | "es" | "es-MX" | "es-AR" | "es-CL" | "es-CO" | "es-PE" | "it" | "de" | "ru" | "pt-PT" | "pt-BR" | "ar" | "ja" | "ko" | "zh-CN" | "zh-TW" | "pl" | "hy" | "nl" | "sv" | "da" | "fi" | "nb" | "tr" | "uk" | "cs" | "sk" | "ro" | "el" | "he" | "hi" | "id" | "vi" | "th" | "hu" | "bg" | "hr" | "sr" | "ms" | "ca" | "fa" | "fil";

export type Discovery = {
  name: string;
  serverVersion: string;
  protocolVersion: number;
  apiBaseUrl: string;
  setupRequired: boolean;
  setupCompleted?: boolean;
  demoAvailable?: boolean;
  timezone: string;
  interfaceLanguage: InterfaceLanguage;
};

export type AuthorizationScope = "global_admin" | "category";

export type CategoryRef = {
  id: string;
  name: string;
  color: string | null;
  icon: string | null;
};

type ScopedAuthorization =
  | { authorizationScope: "global_admin"; category: null }
  | { authorizationScope: "category"; category: CategoryRef };

export type TokenPair = {
  tokenType: "Bearer";
  accessToken: string;
  accessTokenExpiresAt: string;
  refreshToken: string;
  refreshTokenExpiresAt: string;
  sessionId: string;
  deviceId: string;
} & ScopedAuthorization;

export type AccessCategory = {
  id: string;
  name: string;
  description: string | null;
  color: string | null;
  icon: string | null;
  position: number;
  isDefault: boolean;
  profileCount: number;
  deviceCount: number;
  createdAt: string;
  updatedAt: string;
};

export type ManagedDevice = {
  id: string;
  name: string;
  platform: string;
  categoryId: string;
  category: CategoryRef;
  internalNote: string | null;
  approvedAt: string | null;
  lastSeenAt: string | null;
  createdAt: string;
  updatedAt: string;
};

export type CategoryInput = {
  name: string;
  description?: string | null;
  color?: string | null;
  icon?: string | null;
};

export type DeviceUpdateInput = {
  name?: string;
  categoryId?: string;
  internalNote?: string | null;
};

export type Avatar = { kind: "preset" | "custom"; presetId?: string; url: string };
export type Profile = {
  id: string;
  name: string;
  description: string | null;
  categoryId: string;
  category: CategoryRef;
  isChild: boolean;
  hasPin: boolean;
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
  user: { id: string; username: string; role: "admin" | "member" | "demo" };
  session: {
    id: string;
    deviceId: string;
    activeProfile: { id: string; expiresAt: string } | null;
  } & ScopedAuthorization;
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
} & ScopedAuthorization;
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
export type AddonCatalogSource = { addonId: string; manifestId?: string; type: string; catalogId: string; extra?: CollectionExtra[] };
export type AddonCatalogDescriptor = {
  addonId: string;
  addonName?: string;
  addonLogoUrl?: string;
  manifestId: string;
  position: number;
  catalog: {
    type: string;
    id: string;
    name?: string;
    extra?: Array<{
      name: string;
      isRequired?: boolean;
      default?: string;
      options?: string[];
      optionsLimit?: number;
    }>;
    extraRequired?: string[];
    extraSupported?: string[];
  };
  addonCatalog: boolean;
  searchable: boolean;
};
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
  categoryIds: string[];
  position: number;
  version: number;
  createdAt: string;
  updatedAt: string;
};
export type CollectionSaveInput = Omit<Collection, "id" | "position" | "version" | "createdAt" | "updatedAt"> & { expectedVersion?: number };
export type CollectionListResponse = { collections: Collection[] };
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
export type PortableCollection = Omit<CollectionSaveInput, "expectedVersion" | "folders" | "profileIds" | "categoryIds"> & {
  folders: PortableCollectionFolder[];
};
export type CollectionExportDocument = {
  schemaVersion: 1;
  exportedAt?: string;
  collections: PortableCollection[];
};
export type CollectionImportResult = { imported: number; collections: Collection[] };

export type SourceReference = {
  id: string;
  kind: string;
  title: string;
  addonId?: string;
  manifestId?: string;
  catalogId?: string;
};
export type CurrentProgram = string | {
  title?: string;
  name?: string;
  description?: string;
  start?: string;
  end?: string;
};
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
  sourceAddonId?: string;
  sourceCatalogId?: string;
  sourceName?: string;
  resourceId?: string;
  country?: string;
  language?: string;
  category?: string;
  available?: boolean;
  currentProgram?: CurrentProgram;
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
  backdropUrl?: string;
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
  backdropUrl?: string;
  voteAverage: number;
  externalIds: Record<string, string>;
};
export type SeasonMetadata = SeasonSummary & { episodes: EpisodeMetadata[] };
export type CastMember = { id: string; name: string; character?: string; profileUrl?: string };

export type MovieMetadata = {
  id: string;
  mediaType: "movie";
  title: string;
  originalTitle: string;
  originalLanguage: string;
  overview: string;
  releaseDate?: string;
  posterUrl?: string;
  backdropUrl?: string;
  logoUrl?: string;
  tagline?: string;
  runtimeMinutes?: number;
  genres: Array<{ id: number; name: string }>;
  cast: CastMember[];
  voteAverage: number;
  voteCount: number;
  externalIds: Record<string, string>;
};
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
  cast: CastMember[];
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
  sourcePosterUrls?: Record<string, string>;
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
  manifest: AddonManifest;
  position: number;
  enabled: boolean;
  profileIds: string[];
  categoryIds: string[];
  installedAt: string;
  updatedAt: string;
};
export type ManagedAddon = InstalledAddon & {
  transportUrl?: string;
};
export type InstallAddonInput = {
  transportUrl: string;
  profileIds: string[];
  categoryIds: string[];
};
export type UpdateAddonInput = {
  profileIds?: string[];
  categoryIds?: string[];
  transportUrl?: string;
  enabled?: boolean;
};
export type AddonDiagnosticState = "unknown" | "available" | "degraded" | "unavailable";
export type AddonDiagnosticErrorCode = "timeout" | "invalid_response" | "unavailable" | "request_failed";
export type AddonDiagnosticCapabilities = {
  resources: string[];
  search: boolean;
  pagination: boolean;
  searchPagination: boolean;
};
export type AddonPreviewResponse = {
  manifest: AddonManifest;
  capabilities: AddonDiagnosticCapabilities;
  profileIds: string[];
  categoryIds: string[];
};
export type AddonDiagnostic = {
  addonId: string;
  state: AddonDiagnosticState;
  lastSuccessAt?: string;
  approximateLatencyMs?: number;
  lastError?: { code: AddonDiagnosticErrorCode; at: string };
  capabilities: AddonDiagnosticCapabilities;
};
export type AddonDiagnosticsResponse = {
  observedSince: string;
  diagnostics: AddonDiagnostic[];
};
export type ResourceResult = { addonId: string; manifestId: string; resource: string; type: string; id: string; extra?: CollectionExtra[]; payload: Record<string, unknown> };
export type ResourceBatch = { results: ResourceResult[]; errors: { addonId: string; manifestId: string; code: string; message: string }[] };
export type CustomSeriesResolveInput = {
  sourceAddonId: string;
  sourceType: string;
  series: {
    resourceId: string;
    title: string;
    posterUrl?: string;
    backgroundUrl?: string;
    releaseInfo?: string;
  };
  videos: Array<{
    resourceId: string;
    title?: string;
    seasonNumber: number;
    episodeNumber: number;
    thumbnailUrl?: string;
    backgroundUrl?: string;
    releaseInfo?: string;
    released?: string;
  }>;
};
export type CustomSeriesResolveResult = {
  series: { titleId: string; resourceId: string };
  seasons: Array<{ titleId: string; seasonNumber: number }>;
  videos: Array<{ titleId: string; resourceId: string; seasonTitleId: string; seasonNumber: number; episodeNumber: number }>;
};

export type PlaybackMediaProfile = { container: string; videoCodec: string; audioCodec?: string };
export type PlaybackProcessingMode = "remux" | "transcode_audio" | "transcode";
export type PlaybackSubtitleMode = "external" | "burn";
export type PlaybackDecision = {
  reason: "direct_supported" | "remux_required" | "audio_transcode_required" | "video_transcode_required" | "subtitle_burn_required";
  videoAction: "copy" | "transcode";
  audioAction: "copy" | "transcode";
  subtitleAction: "none" | "external" | "copy" | "burn";
  toneMapping: boolean;
  source?: {
    container?: string;
    videoCodec?: string;
    audioCodec?: string;
    height?: number;
    videoBitrateKbps?: number;
    hdrFormat?: string;
  };
  target?: {
    protocol?: string;
    container?: string;
    videoCodec?: string;
    audioCodec?: string;
    height?: number;
    videoBitrateKbps?: number;
  };
};
export type PlaybackCapabilities = {
  streamingProtocols: string[];
  containers: string[];
  videoCodecs?: string[];
  audioCodecs?: string[];
  hdrFormats?: string[];
  externalPlayers?: string[];
  processingModes?: PlaybackProcessingMode[];
  maximumVideoBitrateKbps?: number;
  maximumHeight?: number;
  maximumAudioChannels?: number;
  subtitleModes?: PlaybackSubtitleMode[];
  mediaProfiles?: PlaybackMediaProfile[];
};
export type PlaybackSourceOption = {
  id: string;
  sourceRef: string;
  addonId: string;
  manifestId: string;
  addonName?: string;
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
  decision?: PlaybackDecision;
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
  decision?: PlaybackDecision;
};
export type PlaybackSubtitle = { id: string; addonId: string; manifestId: string; language?: string; delivery?: "external" | "burn"; url?: string; forced?: boolean; default?: boolean };
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
  decision?: PlaybackDecision;
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
  progressPercent?: number;
  speed?: number;
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
export type OperationAction = "fetch-missing-metadata" | "run-housekeeping" | "clear-metadata-cache" | "clear-stream-cache";
export type MetadataRefreshResult = { candidates: number; refreshed: number; failed: number; failedTitles?: string[] };
export type MetadataRefreshScheduleInput = {
  enabled: boolean;
  intervalHours: 6 | 12 | 24 | 168;
  language: string;
  batchSize: number;
};
export type MetadataRefreshSchedule = {
  task: "metadata-refresh";
  enabled: boolean;
  intervalHours: number;
  language: string;
  batchSize: number;
  nextRunAt: string | null;
  lastStartedAt: string | null;
  lastCompletedAt: string | null;
  lastStatus: "succeeded" | "partial" | "failed" | null;
  lastResult: MetadataRefreshResult | null;
};
export type MetadataCacheStatus = {
  entries: number;
  freshEntries: number;
  expiredEntries: number;
  rootTitles: number;
  missingTitles: number;
  artworkSnapshots: number;
};
export type OperationsOverview = {
  metadataCache: MetadataCacheStatus;
  metadataRefresh: MetadataRefreshSchedule;
  housekeepingIntervalMinutes: number;
};
export type OperationRun = {
  action: OperationAction;
  startedAt: string;
  completedAt: string;
  status: "succeeded" | "partial" | "failed";
  result: {
    metadata?: MetadataRefreshResult;
    metadataCache?: { entriesDeleted: number };
    playback?: PlaybackPurgeResult;
  };
};
export type PlaybackProgress = { titleId: string; mediaType?: string; positionSeconds: number; durationSeconds: number; completed: boolean; version: number; updatedAt?: string };
export type PlaybackProgressBatchItem = { titleId: string; progress: PlaybackProgress | null };
export type PlaybackProgressBatch = { items: PlaybackProgressBatchItem[] };
export type SetWatchedBatchItem = { titleId: string; completed: boolean; expectedVersion: number };
export type SetWatchedBatchResult = { items: Array<{ titleId: string; progress: PlaybackProgress }> };
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
  sourceAddonId?: string;
  sourceCatalogId?: string;
  sourceName?: string;
  country?: string;
  language?: string;
  category?: string;
  available?: boolean;
  currentProgram?: CurrentProgram;
  addedAt: string;
  updatedAt: string;
};
export type LibraryPage = { items: LibraryItem[]; page: number; totalPages: number; totalResults: number };
export type TVLibraryIdentity = { sourceAddonId: string; resourceId: string };
export type TVLibraryMembership = TVLibraryIdentity & { titleId: string };
export type TVLibraryMembershipResult = { items: TVLibraryMembership[] };
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
export type CalendarSubscription = { active: boolean; url?: string; createdAt?: string; rotatedAt?: string };
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
  sourceAddonId?: string;
  sourceCatalogId?: string;
  sourceName?: string;
  country?: string;
  language?: string;
  category?: string;
  available?: boolean;
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

export type JellyfinCredentialStatus = {
  active: boolean;
  canIssue: boolean;
  generation: number;
  username?: string;
  createdAt?: string;
  rotatedAt?: string;
  lastUsedAt?: string;
  revokedAt?: string;
};
export type JellyfinCredentialSecret = JellyfinCredentialStatus & {
  username: string;
  password: string;
};

export type SettingsValues = {
  interfaceLanguage?: InterfaceLanguage | null;
  theme?: string | null;
  maximumResolution?: string | null;
  maximumCastMembers?: number | null;
  maximumDirectTitles?: number | null;
  allowTranscoding?: boolean | null;
  jellyfinEnabled?: boolean | null;
  transcoding?: "inherit" | "enabled" | "disabled" | null;
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
