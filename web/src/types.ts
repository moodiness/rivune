export type InterfaceLanguage = "en" | "fr" | "de" | "es" | "it" | "pt-BR";

export type AccessibilityPreferences = {
  reducedMotion: "system" | "reduce" | "no-preference";
  highContrast: "system" | "more" | "standard";
  textScale: 100 | 115 | 130;
  captions: "system" | "on" | "off";
  audioDescription: boolean;
  focusIndicators: "standard" | "enhanced";
};
export type AccessibilityDocument = AccessibilityPreferences & { revision: number };
export type AccessibilityUpdate = AccessibilityPreferences & { revision: number };

export type Discovery = {
  name: string;
  serverVersion: string;
  protocolVersion: number;
  apiBaseUrl: string;
  setupRequired: boolean;
  setupCompleted?: boolean;
  demoAvailable?: boolean;
  capabilities?: string[];
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

export type WebSessionTokens = {
  tokenType: "Bearer";
  accessToken: string;
  accessTokenExpiresAt: string;
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
export type SemanticSearchMediaType = "movie" | "series" | "anime" | "tv" | "other";
export type SemanticSearchRequest = {
  query: string;
  mediaType?: SemanticSearchMediaType;
  language?: string;
  region?: string;
  page: number;
  limit: number;
  excludedIntentIds: string[];
};
export type SemanticSearchIntent = {
  id: string;
  kind: "media_type" | "genre" | "theme" | "country" | "release_year" | "release_decade" | "release_recency" | "rating_min" | "rating_quality" | "runtime_min" | "runtime_max" | "exclude_genre";
  value: string;
  label: string;
};
export type SemanticSearchPage = {
  intents: SemanticSearchIntent[];
  titleQuery: string;
  mediaTypes: SemanticSearchMediaType[];
  items: MediaItem[];
  page: number;
  hasMore: boolean;
  partial: boolean;
};
export type SavedSearchSort = "relevance" | "title" | "year" | "rating" | "added";
export type SavedSearch = {
  id: string;
  name: string;
  query: string;
  mediaType?: "movie" | "series" | "season" | "episode" | "video" | "tv";
  sort: SavedSearchSort;
  revision: number;
  createdAt: string;
  updatedAt: string;
};
export type SavedSearchInput = Omit<SavedSearch, "id" | "revision" | "createdAt" | "updatedAt"> & { expectedRevision?: number };
export type SmartRuleField = "media_type" | "year" | "genre" | "status" | "rating" | "source";
export type SmartRule =
  | { type: "all" | "any"; rules: SmartRule[] }
  | { type: SmartRuleField; operator: "equals" | "not_equals" | "one_of" | "gte" | "lte"; value?: string; values?: string[]; number?: number };
export type SmartCollection = {
  id: string;
  name: string;
  rules: SmartRule;
  sort: "title" | "year" | "rating" | "added";
  revision: number;
  createdAt: string;
  updatedAt: string;
};
export type SmartCollectionInput = Omit<SmartCollection, "id" | "revision" | "createdAt" | "updatedAt"> & { expectedRevision?: number };
export type SmartCollectionItem = {
  id: string;
  mediaType: string;
  title: string;
  posterUrl?: string;
  backgroundUrl?: string;
  releaseInfo?: string;
  released?: string;
  communityRating?: number;
  genres: string[];
  resourceId?: string;
  resourceProvider?: string;
  sourceAddonId?: string;
  sourceCatalogId?: string;
  sourceName?: string;
};
export type SmartCollectionPage = { items: SmartCollectionItem[]; page: number; pageSize: number; total: number; totalPages: number };
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

export type ProfileArchiveDocument = {
  version: 2;
  exportedAt: string;
  identity: { name: string; description?: string | null; isChild: boolean; avatar: { kind: "preset"; presetId: string } | { kind: "image"; contentType: "image/png"; sha256: string; data: string } };
  settings: Record<string, unknown>;
  addons: unknown[];
  collections: unknown[];
  titles: unknown[];
  library: unknown[];
  progress: unknown[];
  favorites: unknown[];
  userData: unknown[];
  trackingPreferences: unknown[];
  continueDismissals: unknown[];
};
export type ProfileArchiveSectionReport = { section: string; created: number; updated: number; unchanged: number };
export type ProfileArchiveImportReport = { mode: "merge" | "create"; profileId: string; sections: ProfileArchiveSectionReport[]; trackingAccountsUpdated: number };

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
export type AddonVerificationStatus = "passed" | "failed";
export type AddonVerificationSummary = "ready" | "manifest_unavailable" | "manifest_invalid" | "catalog_unavailable";
export type AddonVerification = { id: string; status: AddonVerificationStatus; summary: AddonVerificationSummary; checks: Array<{ code: "manifest_fetch" | "manifest_valid" | "catalog_probe"; status: "passed" | "failed" | "skipped" }>; manifest?: AddonManifest; capabilities?: AddonDiagnosticCapabilities; profileIds: string[]; categoryIds: string[]; createdAt: string; expiresAt: string };
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

export type PlaybackMediaProfile = { container: string; videoCodec: string; audioCodec?: string; maximumVideoBitDepth?: number };
export type PlaybackProcessingMode = "remux" | "transcode_audio" | "transcode";
export type PlaybackSubtitleMode = "external" | "burn";
export type PlaybackDecisionReason = "container_not_supported" | "video_codec_not_supported" | "audio_codec_not_supported" | "resolution_limit" | "bitrate_limit" | "hdr_not_supported" | "subtitle_burn_required";

export type PlaybackDecision = {
  reason: "direct_supported" | "remux_required" | "audio_transcode_required" | "video_transcode_required" | "subtitle_burn_required";
  reasons: PlaybackDecisionReason[];
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
    videoBitDepth?: number;

    videoBitrateKbps?: number;
  };
};
export type NetworkClass = "local" | "remote_wifi" | "mobile";
export type QualityPreset = "automatic" | "economy" | "balanced" | "maximum";
export type QualityPreferences = Record<NetworkClass, QualityPreset>;
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
  stableIdentity: string;
};
export type PlaybackProviderFailure = { addonId: string; manifestId: string; code: string; message: string };
export type PlaybackSourceList = { sources: PlaybackSourceOption[]; providerErrors: PlaybackProviderFailure[] };
export type PlaybackFailoverError = "source_failed" | "source_timeout" | "ended_early" | "decode_failed" | "access_denied" | "user_cancelled";
export type PlaybackFailoverCandidateHealth = { position: number; status: "current" | "available" | "cooling_down"; cooldownUntil?: string };
export type PlaybackFailoverState = {
  id: string;
  currentSourceRef: string;
  currentPosition: number;
  positionSeconds: number;
  attemptCount: number;
  maximumAttempts: number;
  revision: number;
  status: "active" | "exhausted" | "cancelled";
  lastError?: PlaybackFailoverError;
  explanation?: string;
  candidateHealth: PlaybackFailoverCandidateHealth[];
  expiresAt: string;
};

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

export type PlaybackCoordinationItem = { titleId: string; mediaType: string; resourceId: string; sourceAddonId?: string; title: string; posterUrl?: string };
export type PlaybackDeviceState = { status: "idle" | "playing" | "paused" | "ended"; item?: PlaybackCoordinationItem; positionMilliseconds: number; durationMilliseconds: number; updatedAt: string };
export type PlaybackDevice = { sessionId: string; deviceId: string; name: string; platform: string; capabilities: Array<"playback" | "remote-control" | "load">; state: PlaybackDeviceState; current: boolean; revision: number; lastSeenAt: string };
export type PlaybackLoadMode = "handoff" | "play-copy";
export type PlaybackCommandStatus = "pending" | "applied" | "failed" | "expired";
export type PlaybackCommandResultCode = "applied" | "unsupported" | "invalid_state" | "stale_target" | "expired" | "execution_failed";
export type PlaybackCommand = { operationId: string; command: "play" | "pause" | "seek" | "load" | "stop"; mode?: PlaybackLoadMode; targetRevision?: number; item?: PlaybackCoordinationItem; positionMilliseconds?: number; senderDeviceName: string; status: PlaybackCommandStatus; resultCode?: PlaybackCommandResultCode; createdAt: string; expiresAt: string };
export type PlaybackCommandInput = Pick<PlaybackCommand, "operationId" | "command"> & Partial<Pick<PlaybackCommand, "mode" | "targetRevision" | "item" | "positionMilliseconds">>;

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
  mediaTimeline?: "absolute" | "relative";
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
  errorClass?: "capacity" | "source" | "processing" | "storage" | "timeout" | "cancelled" | "unknown";
  prewarming: boolean;
  createdAt: string;
  lastSeenAt: string;
  progressPercent?: number;
  speed?: number;
  startupDurationSeconds?: number;
};
export type PlaybackMediaPool = { active: number; limit: number };
export type PlaybackMediaProcessTotals = { started: number; succeeded: number; failed: number; softwareFallbacks: number };
export type MediaDiagnostics = {
  ffmpegVersion: string;
  ffprobeVersion: string;
  hardwareAcceleration: "unknown" | HardwareAccelerationMode;
  videoEncoder: string;
  preferredVideoCodec: PreferredTranscodeVideoCodec;
  encodeCodecs: string[];
  decodeCodecs: string[];
  hevcMain10?: boolean;
  qualityPreset: TranscodeQualityPreset;
  hardwareToneMap: boolean;
  toneMapBackend: "vulkan" | "vaapi" | "hybrid" | "software";
  transcodeThreads: number;
  maximumReadRate: number;
  totals: PlaybackMediaProcessTotals;
  pools: { process: PlaybackMediaPool; probe: PlaybackMediaPool; subtitle: PlaybackMediaPool; trickplay: PlaybackMediaPool };
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
  diagnostics: MediaDiagnostics;
  sessions: PlaybackActivitySession[];
  jobs: PlaybackMediaJob[];
  sessionsTruncated: boolean;
  jobsTruncated: boolean;
};
export type PlaybackPurgeResult = { sessionsRemoved: number; jobsStopped: number; storageBytes: number };
export type AddonIncidentCode = "timeout" | "unavailable" | "invalid_response" | "unhealthy";
export type AddonIncidentState = "open" | "recovering" | "resolved";
export type AddonIncident = {
  id: string; profileId: string; addonId: string; addonName: string;
  code: AddonIncidentCode; state: AddonIncidentState;
  impact: "availability" | "response_integrity"; occurrenceCount: number;
  firstOccurredAt: string; lastOccurredAt: string; lastSuccessAt: string | null;
  recoveryStartedAt: string | null; resolvedAt: string | null;
  acknowledgedAt: string | null; acknowledgedByUserId: string | null; updatedAt: string;
};
export type AddonIncidentEvent = { id: number; type: "opened" | "occurred" | "recovering" | "resolved" | "acknowledged"; code: AddonIncidentCode; occurredAt: string };
export type AddonIncidentDetail = { incident: AddonIncident; events: AddonIncidentEvent[] };
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
export type PostgreSQLPoolStatus = {
  acquired: number;
  idle: number;
  total: number;
  max: number;
  waitCount: number;
  waitDurationMilliseconds: number;
};
export type TrackingOutboxStatus = {
  pending: number;
  due: number;
  oldestAgeSeconds: number;
};
export type AddonOperationsStatus = {
  total: number;
  enabled: number;
  latestUpdatedAt: string | null;
};
export type PlaybackOperationsStatus = {
  active: number;
  transcoding: number;
};
export type SemanticExtensionStatus = "disabled" | "pending" | "ready" | "failed";
export type SemanticExtensionOperationsStatus = {
  enabled: boolean;
  warmupStatus: SemanticExtensionStatus;
  persistentStatus: SemanticExtensionStatus;
  memoryEntries: number;
  persistentEntries: number;
  hits: number;
  misses: number;
  coalescedWaiters: number;
  executions: number;
  successes: number;
  timeouts: number;
  failures: number;
  cancellations: number;
  busyFallbacks: number;
  active: number;
  queued: number;
  latencyP50Milliseconds: number;
  latencyP95Milliseconds: number;
};
export type OperationsOverview = {
  metadataCache: MetadataCacheStatus;
  metadataRefresh: MetadataRefreshSchedule;
  postgresqlPool: PostgreSQLPoolStatus;
  trackingOutbox: TrackingOutboxStatus;
  addons: AddonOperationsStatus;
  playback: PlaybackOperationsStatus;
  semanticExtension: SemanticExtensionOperationsStatus;
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
export type MediaNotificationKind = "calendar-event-upcoming" | "season-available" | "episode-available" | "movie-release";
export type MediaNotification = {
  id: string;
  kind: MediaNotificationKind;
  titleId: string;
  subjectTitleId?: string;
  title: string;
  seriesTitle?: string;
  releaseDate?: string;
  seasonNumber?: number;
  episodeNumber?: number;
  availableAt: string;
  readAt?: string;
  createdAt: string;
};
export type MediaNotificationPage = { notifications: MediaNotification[]; nextCursor?: string };
export type MediaNotificationSubscription = {
  titleId: string;
  timezone: string;
  horizonDays: number;
  leadDays: number;
  createdAt: string;
  updatedAt: string;
};
export type MediaNotificationFollowInput = { timezone: string; horizonDays: number; leadDays: number };
export type MediaNotificationSubscriptions = { subscriptions: MediaNotificationSubscription[] };
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
  mappingProvider?: "tvdb" | null;
  episodeOrderId?: string | null;
  metadataSeasonId?: string | null;
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
  episodeTitle?: string;
  episodeStillUrl?: string;
  episodeAirDate?: string;
  lastWatchedAt: string;
};
export type ContinueWatching = { items: ContinueItem[] };

type ContinueItemContinuationContract<T extends Pick<ContinueItem, "mappingProvider" | "episodeOrderId" | "metadataSeasonId">> = T;
export type ContinueItemContinuationContractFixture = ContinueItemContinuationContract<{
  readonly mappingProvider: "tvdb";
  readonly episodeOrderId: "2";
  readonly metadataSeasonId: "tvdb:0392d6ce-02f0-4c75-a73f-13badb1c85ba:2112814";
}>;

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

export type HardwareAccelerationMode = "auto" | "software" | "vaapi" | "hybrid" | "qsv" | "nvenc" | "amf";
export type PreferredTranscodeVideoCodec = "auto" | "h264" | "hevc" | "av1";
export type TranscodeQualityPreset = "speed" | "balanced" | "quality";

export type SettingsValues = {
  interfaceLanguage?: InterfaceLanguage | null;
  theme?: string | null;
  maximumResolution?: string | null;
  maximumCastMembers?: number | null;
  maximumDirectTitles?: number | null;
  allowTranscoding?: boolean | null;
  jellyfinEnabled?: boolean | null;
  jellyfinDebug?: boolean | null;
  timezone?: string | null;
  hardwareAcceleration?: HardwareAccelerationMode | null;
  transcodeMaxBitrateKbps?: number | null;
  preferredTranscodeVideoCodec?: PreferredTranscodeVideoCodec | null;
  transcodeQualityPreset?: TranscodeQualityPreset | null;
  transcodeConcurrency?: number | null;
  mediaMaxStorageMB?: number | null;
  artworkMaxStorageMB?: number | null;

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
export type RuntimeSettingsValues = {
  timezone: string;
  jellyfinEnabled: boolean;
  jellyfinDebug: boolean;
  hardwareAcceleration: HardwareAccelerationMode;
  transcodeMaxBitrateKbps: number;
  preferredTranscodeVideoCodec: PreferredTranscodeVideoCodec;
  transcodeQualityPreset: TranscodeQualityPreset;
  transcodeConcurrency: number;
  mediaMaxStorageMB: number;
  artworkMaxStorageMB: number;
  allowTranscoding: boolean;
};
export type RuntimeApplication = {
  active: RuntimeSettingsValues;
  requested: RuntimeSettingsValues;
  pendingRestart: Array<"hardwareAcceleration" | "preferredTranscodeVideoCodec" | "transcodeQualityPreset" | "transcodeConcurrency">;
};
export type SettingsLayer = { schemaVersion: number; revision: number; settings: SettingsValues; runtime?: RuntimeApplication; updatedAt: string | null };

export const integrationCredentialNames = ["tmdbAccessToken", "fanartApiKey", "mdblistApiKey", "tvdbApiKey", "tvdbPin", "traktClientId", "traktClientSecret", "simklClientId"] as const;
export type IntegrationCredentialName = typeof integrationCredentialNames[number];
export type IntegrationCredentialStatus = { configured: boolean; updatedAt: string | null };
export type IntegrationProviderStatuses = {
  tmdb: boolean;
  fanart: boolean;
  mdblist: boolean;
  tvdb: boolean;
  trakt: boolean;
  simkl: boolean;
};
export type SettingsIntegrations = {
  revision: number;
  credentials: Record<IntegrationCredentialName, IntegrationCredentialStatus>;
  providers: IntegrationProviderStatuses;
};
export type SettingsIntegrationsPatch = Partial<Record<IntegrationCredentialName, string | null>>;
type ConfigurationAuditEventBase = {
  id: number;
  revision: number;
  actorUserId: string | null;
  changedKeys: string[];
  createdAt: string;
};
export type ConfigurationAuditEvent = ConfigurationAuditEventBase & (
  | { action: "settings.updated"; snapshot: SettingsValues }
  | { action: "integrations.updated"; snapshot: Record<IntegrationCredentialName, boolean> }
);
export type ConfigurationAuditPage = {
  events: ConfigurationAuditEvent[];
  nextCursor: number | null;
};
export type AvatarPreset = { id: string; name: string; url: string };

export type ReadingQueueMediaType = "movie" | "series" | "episode" | "tv";
export type ReadingQueueItem = {
  id: string;
  mediaType: ReadingQueueMediaType;
  resourceId: string;
  sourceAddonId?: string;
  titleId?: string;
  title: string;
  posterUrl?: string;
  position: number;
  createdAt: string;
  updatedAt: string;
};
export type ReadingQueue = { revision: number; items: ReadingQueueItem[] };
export type ReadingQueueMutation = { revision: number; affectedItemId?: string; duplicate?: boolean };
export type ReadingQueueAddInput = {
  operationId: string;
  expectedRevision: number;
  mediaType: ReadingQueueMediaType;
  resourceId: string;
  sourceAddonId?: string;
  titleId?: string;
  title: string;
  posterUrl?: string;
};
export type ReadingQueueMutationInput = { operationId: string; expectedRevision: number };
export type ReadingQueueReorderInput = ReadingQueueMutationInput & { itemIds: string[] };
export type ReadingQueueUpdateInput = ReadingQueueMutationInput & { title: string; posterUrl?: string };
