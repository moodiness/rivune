import type { TvPlaybackCapabilities } from "./platform";

export const RIVUNE_PROTOCOL_VERSION = 22 as const;
export const SEMANTIC_SEARCH_CAPABILITY = "semantic-search" as const;
export const PLAYBACK_COORDINATION_CAPABILITY = "playback-command-results" as const;

export type UUID = string;
export type JsonPrimitive = string | number | boolean | null;
export type JsonValue = JsonPrimitive | JsonValue[] | { [key: string]: JsonValue };
export type AuthorizationScope = "global_admin" | "category";
export type MediaType = "movie" | "series" | "season" | "episode";
export type TitleMediaType = "movie" | "series" | "tv";
export type SeriesMappingProvider = "tmdb" | "tvdb";
export type PlaybackProgressMediaType = "movie" | "episode";
export type PlaybackMode = "direct" | "remux" | "transcode_audio" | "transcode" | "youtube" | "external";
export type PlaybackCapabilities = TvPlaybackCapabilities;

export interface Discovery {
  name: string;
  serverVersion: string;
  protocolVersion: typeof RIVUNE_PROTOCOL_VERSION;
  apiBaseUrl: string;
  setupRequired: boolean;
  setupCompleted?: boolean;
  demoAvailable?: boolean;
  timezone: string;
  interfaceLanguage: string;
  capabilities: string[];
}

export interface CategoryRef { id: UUID; name: string; color: string | null; icon: string | null }
export interface TokenPair {
  tokenType: string;
  accessToken: string;
  accessTokenExpiresAt: string;
  refreshToken: string;
  refreshTokenExpiresAt: string;
  sessionId: UUID;
  deviceId: UUID;
  authorizationScope: AuthorizationScope;
  category: CategoryRef | null;
}
export interface DeviceAuthorization {
  deviceCode: string;
  userCode: string;
  verificationUri: string;
  verificationUriComplete: string;
  expiresAt: string;
  intervalSeconds: number;
}

export interface AccountUser { id: UUID; username: string; role: string }
export interface ActiveProfileGrant { id: UUID; expiresAt: string }
export interface AccountSession {
  id: UUID; deviceId: UUID; activeProfile: ActiveProfileGrant | null;
  authorizationScope: AuthorizationScope; category: CategoryRef | null;
}
export interface MaintenanceSettings { enabled: boolean; message: string | null }
export interface Account { user: AccountUser; session: AccountSession; profiles: Profile[]; maintenance: MaintenanceSettings }

export interface ProfileAvatar { kind: string; presetId?: string | null; url: string }
export interface Profile {
  id: UUID; name: string; description?: string | null; categoryId: UUID; category: CategoryRef;
  isChild: boolean; hasPin: boolean; canManage: boolean; enabled: boolean; availableFrom: string | null;
  availableUntil: string | null; accessStartTime: string | null; accessEndTime: string | null;
  accessTimezone: string; accessible: boolean; avatar: ProfileAvatar;
}
export interface ProfileSelection { profile: Profile; expiresAt: string; profileContext: string }
export interface ProfileList { profiles: Profile[] }

export interface SettingsValues {
  allowTranscoding?: boolean | null;
  transcoding?: string | null;
  maximumCastMembers?: number | null;
  maximumResolution?: string | null;
  preferDirectPlay?: boolean | null;
  audioLanguage?: string | null;
  subtitleLanguage?: string | null;
  forcedSubtitleLanguage?: string | null;
  autoplayNextEpisode?: boolean | null;
  skipIntroEnabled?: boolean | null;
  skipRecapEnabled?: boolean | null;
  skipOutroEnabled?: boolean | null;
  metadataLanguage?: string | null;
  interfaceLanguage?: string | null;
  seriesMappingProvider?: SeriesMappingProvider | null;
}
export type EffectiveSettingsSources = { [K in keyof SettingsValues]?: string | null };
export interface SettingsLayer { schemaVersion: number; settings: SettingsValues; updatedAt?: string | null }
export interface EffectiveSettings { schemaVersion: number; settings: SettingsValues; sources: EffectiveSettingsSources }

export type CollectionViewMode = "tabbed_grid" | "rows" | "follow_layout";
export type CollectionTileShape = "poster" | "landscape" | "square";
export type CollectionSourceView = "merged" | "categories" | "folders";
export type CollectionSourceKind = "addon_catalog" | "tmdb" | "trakt" | "mdblist";
export interface CollectionExtraValue { name: string; value: string }
export interface CollectionAddonCatalogSource {
  addonId: UUID; manifestId?: string | null; type: string; catalogId: string;
  extra?: CollectionExtraValue[] | null;
}
export interface CollectionList { collections: Collection[] }
export interface CollectionSource {
  id?: UUID | null; kind: CollectionSourceKind; title: string;
  addonCatalog?: CollectionAddonCatalogSource | null; tmdb?: JsonValue | null;
  trakt?: JsonValue | null; mdblist?: JsonValue | null;
}
export interface CollectionFolder {
  id?: UUID | null; title: string; tileShape: CollectionTileShape; sourceView?: CollectionSourceView | null;
  coverImageUrl?: string | null; coverEmoji?: string | null; titleLogoUrl?: string | null;
  heroBackdropUrl?: string | null; heroVideoUrl?: string | null; focusGifUrl?: string | null;
  focusGifEnabled: boolean; hideTitle: boolean; sources: CollectionSource[];
}
export interface Collection {
  id: UUID; title: string; backdropImageUrl?: string | null; heroEnabled: boolean; pinToTop: boolean;
  focusGlowEnabled: boolean; viewMode: CollectionViewMode; folderCoverShape: CollectionTileShape;
  folders: CollectionFolder[]; profileIds: UUID[]; categoryIds: UUID[]; position: number; version: number;
  createdAt: string; updatedAt: string;
}
export interface CollectionSourceReference {
  id: UUID; kind: CollectionSourceKind; title: string; addonId?: UUID | null;
  manifestId?: string | null; catalogId?: string | null;
}
export interface CollectionItem {
  id: string; mediaType: string; title: string; posterUrl?: string | null; backgroundUrl?: string | null;
  logoUrl?: string | null; description?: string | null; releaseInfo?: string | null; released?: string | null;
  voteAverage?: number | null; voteCount?: number | null; popularity?: number | null;
  externalIds: Record<string, string>; sources: CollectionSourceReference[]; raw?: JsonValue | null;
}
export interface CollectionSourceFailure { sourceId: UUID; kind: CollectionSourceKind; code: string; message: string }
export interface ResolvedCollectionFolder {
  collectionId: UUID; folder: CollectionFolder; sourcePosterUrls?: Record<string, string> | null;
  items: CollectionItem[]; page: number; hasMore: boolean; errors: CollectionSourceFailure[];
}

export interface SemanticSearchRequest {
  query: string;
  mediaType?: string;
  language?: string;
  region?: string;
  page: number;
  limit: number;
  excludedIntentIds: string[];
}
export interface SemanticSearchIntent { id: string; kind: string; value: string; label: string }
export interface SemanticSearchPage {
  intents: SemanticSearchIntent[];
  titleQuery: string;
  mediaTypes: string[];
  items: CollectionItem[];
  page: number;
  hasMore: boolean;
  partial: boolean;
}

export interface ContinueWatchingItem {
  titleId: UUID; mediaType: PlaybackProgressMediaType; seriesId?: UUID | null; seasonId?: UUID | null;
  seasonNumber?: number | null; episodeNumber?: number | null; title?: string | null; posterUrl?: string | null;
  mappingProvider?: "tvdb" | null; episodeOrderId?: string | null; metadataSeasonId?: string | null;
  backgroundUrl?: string | null; releaseInfo?: string | null; resourceId?: string | null;
  resourceProvider?: string | null; episodeTitle?: string | null; episodeStillUrl?: string | null;
  episodeAirDate?: string | null; positionSeconds: number; durationSeconds: number; version: number;
  reason: "resume" | "next_episode"; lastWatchedAt: string;
}
export interface ContinueWatchingPage { items: ContinueWatchingItem[] }

type ContinueWatchingContinuationContract<T extends Pick<ContinueWatchingItem, "mappingProvider" | "episodeOrderId" | "metadataSeasonId">> = T;
export type ContinueWatchingContinuationContractFixture = ContinueWatchingContinuationContract<{
  readonly mappingProvider: "tvdb";
  readonly episodeOrderId: "2";
  readonly metadataSeasonId: "tvdb:0392d6ce-02f0-4c75-a73f-13badb1c85ba:2112814";
}>;

export type CurrentProgram = string | {
  title?: string;
  description?: string;
  start?: string;
  end?: string;
};
export interface MediaItem {
  id: string;
  titleId?: string;
  mediaType: string;
  seriesId?: string;
  seasonId?: string;
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
  sources?: CollectionSourceReference[];
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
  resumePositionSeconds?: number;
  durationSeconds?: number;
  progressVersion?: number;
}

export interface StremioExtraProperty {
  name: string; isRequired?: boolean | null; default?: string | null; options?: string[] | null;
  optionsLimit?: number | null;
}
export interface StremioManifestCatalog {
  type: string; id: string; name?: string | null; genres?: string[] | null;
  extra?: StremioExtraProperty[] | null; extraRequired?: string[] | null; extraSupported?: string[] | null;
}
export interface AddonCatalogDescriptor {
  addonId: UUID; addonName?: string | null; addonLogoUrl?: string | null; manifestId: string;
  position: number; catalog: StremioManifestCatalog; addonCatalog: boolean; searchable: boolean;
}
export interface AddonCatalogDescriptorList { catalogs: AddonCatalogDescriptor[] }
export interface AddonCachePolicy {
  maxAgeSeconds?: number | null; staleWhileRevalidateSeconds?: number | null; staleIfErrorSeconds?: number | null;
}
export interface AddonExtraValue { name: string; value: string }
export interface AddonResourceResult {
  addonId: UUID; manifestId: string; resource: string; type: string; id: string;
  payload: Record<string, JsonValue>; cache: AddonCachePolicy; extra?: AddonExtraValue[] | null;
}
export interface AddonResourceFailure { addonId: UUID; manifestId: string; code: string; message: string }
export interface AddonResourceBatch { results: AddonResourceResult[]; errors: AddonResourceFailure[] }

export interface LibraryItem {
  titleId: UUID; mediaType: TitleMediaType; provider?: string | null; externalId?: string | null;
  resourceId?: string | null; title?: string | null; posterUrl?: string | null; backgroundUrl?: string | null;
  releaseInfo?: string | null; sourceAddonId?: UUID | null; sourceCatalogId?: string | null;
  sourceName?: string | null; country?: string | null; language?: string | null; category?: string | null;
  available: boolean; addedAt: string; updatedAt: string;
}
export interface LibraryPage { items: LibraryItem[]; page: number; totalPages: number; totalResults: number }

export interface CalendarEvent {
  id: string; titleId: UUID; mediaType: "movie" | "episode"; title: string; releaseDate: string;
  posterUrl?: string | null; resourceId?: string | null; resourceProvider?: string | null;
  seriesTitle?: string | null; seriesId?: UUID | null; seasonId?: UUID | null;
  seasonNumber?: number | null; episodeNumber?: number | null;
}
export interface CalendarEventList { events: CalendarEvent[] }

export interface TitleResolveInput {
  mediaType: TitleMediaType; provider: string; externalId?: string | null; resourceId: string; title: string;
  posterUrl?: string | null; backgroundUrl?: string | null; releaseInfo?: string | null; released?: string | null;
  sourceAddonId?: UUID | null; sourceCatalogId?: string | null; sourceName?: string | null;
  country?: string | null; language?: string | null; category?: string | null;
}
export interface TitleReference extends Omit<TitleResolveInput, "externalId"> { titleId: UUID; externalId: string }

export interface Genre { id: number; name: string }
export interface CastMember { id: string; name: string; character?: string | null; profileUrl?: string | null }
export interface Movie {
  id: UUID; mediaType: MediaType; title: string; originalTitle: string; originalLanguage: string;
  overview: string; releaseDate?: string | null; posterUrl?: string | null; backdropUrl?: string | null;
  logoUrl?: string | null; tagline?: string | null; runtimeMinutes?: number | null; genres: Genre[];
  cast: CastMember[]; voteAverage: number; voteCount: number; externalIds: Record<string, string>;
}
export interface SeriesAlias { language: string; name: string }
export interface EpisodeOrder { id: string; name: string; type: string; isDefault: boolean }
export interface SeasonSummary {
  id: string; mediaType: MediaType; seriesId: UUID; name: string; overview: string; seasonNumber: number;
  episodeCount: number; airDate?: string | null; posterUrl?: string | null; backdropUrl?: string | null;
  voteAverage: number; externalIds: Record<string, string>;
}
export interface Series {
  id: UUID; mediaType: MediaType; name: string; originalName: string; originalLanguage: string; overview: string;
  firstAirDate?: string | null; lastAirDate?: string | null; posterUrl?: string | null; backdropUrl?: string | null;
  logoUrl?: string | null; tagline?: string | null; status?: string | null; numberOfSeasons?: number | null;
  numberOfEpisodes?: number | null; genres: Genre[]; cast: CastMember[]; voteAverage: number; voteCount: number;
  seasons: SeasonSummary[]; aliases: SeriesAlias[]; episodeOrders: EpisodeOrder[];
  selectedEpisodeOrderId?: string | null; mappingProvider: SeriesMappingProvider;
  externalIds: Record<string, string>;
}
export interface Episode {
  id: UUID; mediaType: MediaType; seasonId: string; name: string; overview: string; seasonNumber: number;
  episodeNumber: number; airDate?: string | null; stillUrl?: string | null; backdropUrl?: string | null;
  runtimeMinutes?: number | null; voteAverage: number; voteCount: number; externalIds: Record<string, string>;
}
export interface Season {
  id: string; mediaType: MediaType; seriesId: UUID; name: string; overview: string; seasonNumber: number;
  airDate?: string | null; posterUrl?: string | null; backdropUrl?: string | null; voteAverage: number;
  episodes: Episode[]; externalIds: Record<string, string>;
}

export interface PlaybackProgress {
  titleId: UUID; mediaType: PlaybackProgressMediaType; positionSeconds: number; durationSeconds: number;
  completed: boolean; version: number; lastWatchedAt: string; updatedAt: string;
}
export interface UpdatePlaybackProgressInput {
  positionSeconds: number; durationSeconds: number; completed: boolean; expectedVersion: number;
}
export interface PlaybackSourceOption {
  id: string; sourceRef: string; addonId: UUID; addonName?: string | null; manifestId: string; streamIndex: number;
  name: string; description?: string | null; filename?: string | null; protocol: string; mode?: PlaybackMode | null;
  container?: string | null; expiresAt: string; stableIdentity?: string;
}
export interface PlaybackProviderError { addonId: UUID; manifestId: string; code: string; message: string }
export interface PlaybackSourceList { sources: PlaybackSourceOption[]; providerErrors: PlaybackProviderError[] }
export interface PlaybackMediaTrack {
  index: number; type: "video" | "audio" | "subtitle"; codec: string; profile?: string | null;
  language?: string | null; forced?: boolean | null; title?: string | null; width?: number | null;
  height?: number | null; channels?: number | null;
}
export interface PlaybackMediaInspection {
  container?: string | null; durationSeconds?: number | null; hdrFormat?: string | null;
  videoTracks: PlaybackMediaTrack[]; audioTracks: PlaybackMediaTrack[]; subtitleTracks: PlaybackMediaTrack[];
}
export type PlaybackDecisionOutcome = "direct_supported" | "remux_required" | "audio_transcode_required" | "video_transcode_required" | "subtitle_burn_required";
export type PlaybackDecisionReason = "container_not_supported" | "video_codec_not_supported" | "audio_codec_not_supported" | "resolution_limit" | "bitrate_limit" | "hdr_not_supported" | "subtitle_burn_required";
export interface PlaybackDecision {
  reason: PlaybackDecisionOutcome; reasons: PlaybackDecisionReason[]; videoAction: string; audioAction: string; subtitleAction: string; toneMapping: boolean;
  source?: Record<string, JsonValue | undefined> | null; target?: Record<string, JsonValue | undefined> | null;
}
export interface PlaybackPreparation {
  sourceRef: string; mode: PlaybackMode; protocol: string; container?: string | null;
  media?: PlaybackMediaInspection | null; subtitleCount: number; expiresAt: string; decision?: PlaybackDecision | null;
}
export interface PlaybackSource {
  id: string; addonId: UUID; manifestId: string; name?: string | null; title?: string | null;
  mode: PlaybackMode; url?: string | null; ytId?: string | null; infoHash?: string | null;
  fileIndex?: number | null; protocol: string; container?: string | null; mediaTimeline?: string | null;
  compatible: boolean; media?: PlaybackMediaInspection | null; decision?: PlaybackDecision | null;
}
export interface PlaybackSubtitle {
  id: string; addonId: UUID; manifestId: string; language?: string | null; forced?: boolean | null;
  default?: boolean | null; delivery?: string | null; url?: string | null;
}
export interface PlaybackSession {
  id: UUID; selectedSourceId: string; selectedAudioTrack?: number | null; selectedSubtitleId?: string | null;
  sources: PlaybackSource[]; subtitles: PlaybackSubtitle[]; providerErrors: PlaybackProviderError[]; expiresAt: string;
}

export type PlaybackDeviceStatus = "idle" | "playing" | "paused" | "ended";
export type PlaybackCommandName = "load" | "play" | "pause" | "seek" | "stop";
export type PlaybackLoadMode = "handoff" | "play-copy";
export type PlaybackCommandStatus = "pending" | "applied" | "failed" | "expired";
export type PlaybackCommandResultCode = "applied" | "unsupported" | "invalid_state" | "stale_target" | "expired" | "execution_failed";
export interface CoordinatedPlaybackItem {
  titleId: UUID; mediaType: TitleMediaType | "episode"; resourceId: string; sourceAddonId?: UUID; title: string; posterUrl?: string;
}
export interface PlaybackDeviceState {
  status: PlaybackDeviceStatus; item?: CoordinatedPlaybackItem; positionMilliseconds: number; durationMilliseconds: number; updatedAt?: string;
}
export interface PlaybackDeviceHeartbeatInput { capabilities: string[]; state: PlaybackDeviceState }
export interface PlaybackDevice {
  sessionId: UUID; deviceId: UUID; name: string; platform: string; capabilities: string[]; state: PlaybackDeviceState;
  current: boolean; lastSeenAt: string; revision: number;
}
export interface PlaybackDeviceList { devices: PlaybackDevice[] }
export interface PlaybackCommandInput {
  operationId: UUID; command: PlaybackCommandName; item?: CoordinatedPlaybackItem; positionMilliseconds?: number;
  mode?: PlaybackLoadMode; targetRevision?: number;
}
export interface PlaybackCommand extends PlaybackCommandInput {
  senderDeviceName: string; status: PlaybackCommandStatus; resultCode?: PlaybackCommandResultCode; createdAt: string; expiresAt: string;
}
export interface PlaybackCommandList { commands: PlaybackCommand[] }
export interface PlaybackCommandResultInput { status: Exclude<PlaybackCommandStatus, "pending">; code: PlaybackCommandResultCode }

export type QueueMediaType = "movie" | "series" | "episode" | "tv";
export interface ReadingQueueItem {
  id: UUID; mediaType: QueueMediaType; resourceId: string; sourceAddonId?: UUID; titleId?: UUID; title: string;
  posterUrl?: string; position: number; createdAt: string; updatedAt: string;
}
export interface ReadingQueue { revision: number; items: ReadingQueueItem[] }
export interface ReadingQueueMutationHeader { operationId: UUID; expectedRevision: number }
export interface ReadingQueueAddInput extends ReadingQueueMutationHeader {
  mediaType: QueueMediaType; resourceId: string; sourceAddonId?: UUID; titleId?: UUID; title: string; posterUrl?: string;
}
export type ReadingQueueMutationInput = ReadingQueueMutationHeader;
export interface ReadingQueueUpdateInput extends ReadingQueueMutationHeader { title: string; posterUrl?: string }
export interface ReadingQueueReorderInput extends ReadingQueueMutationHeader { itemIds: UUID[] }
export interface ReadingQueueMutation { revision: number; affectedItemId?: UUID; duplicate?: boolean }

export type SavedSearchSort = "relevance" | "title" | "year" | "rating" | "added";
export type SavedSearchMediaType = QueueMediaType | "season" | "video";
export interface SavedSearchInput { name: string; query: string; mediaType?: SavedSearchMediaType; sort: SavedSearchSort }
export interface SavedSearch extends SavedSearchInput { id: UUID; revision: number; createdAt: string; updatedAt: string }
export interface SavedSearchUpdateInput extends SavedSearchInput { expectedRevision: number }
export interface SavedSearchList { savedSearches: SavedSearch[] }

export type SmartRule =
  | { type: "all" | "any"; rules: SmartRule[] }
  | { type: "media_type"; operator: "one_of"; values: SavedSearchMediaType[] }
  | { type: "year" | "rating"; operator: "equals" | "gte" | "lte"; number: number }
  | { type: "genre" | "status" | "source"; operator: "equals" | "not_equals"; value: string };
export type SmartCollectionSort = "title" | "year" | "rating" | "added";
export interface SmartCollectionInput { name: string; rules: SmartRule; sort: SmartCollectionSort }
export interface SmartCollection extends SmartCollectionInput { id: UUID; revision: number; createdAt: string; updatedAt: string }
export interface SmartCollectionUpdateInput extends SmartCollectionInput { expectedRevision: number }
export interface SmartCollectionList { smartCollections: SmartCollection[] }
export interface SmartCollectionCatalogItem {
  id: UUID; mediaType: SavedSearchMediaType; title: string; genres: string[]; posterUrl?: string; backgroundUrl?: string;
  releaseInfo?: string; released?: string; communityRating?: number; status?: string; resourceId?: string;
  resourceProvider?: string; sourceAddonId?: UUID; sourceCatalogId?: string; sourceName?: string;
}
export interface SmartCollectionPage { items: SmartCollectionCatalogItem[]; page: number; pageSize: number; total: number; totalPages: number }

export type AddonIncidentCode = "timeout" | "unavailable" | "invalid_response" | "unhealthy";
export interface AddonIncident {
  id: UUID; profileId: UUID; addonId: UUID; addonName: string; code: AddonIncidentCode;
  state: "open" | "recovering" | "resolved"; impact: "availability" | "response_integrity"; occurrenceCount: number;
  firstOccurredAt: string; lastOccurredAt: string; lastSuccessAt: string | null; recoveryStartedAt: string | null;
  resolvedAt: string | null; acknowledgedAt: string | null; acknowledgedByUserId: UUID | null; updatedAt: string;
}
export interface AddonIncidentEvent { id: number; type: "opened" | "occurred" | "recovering" | "resolved" | "acknowledged"; code: AddonIncidentCode; occurredAt: string }
export interface AddonIncidentList { incidents: AddonIncident[] }
export interface AddonIncidentDetail { incident: AddonIncident; events: AddonIncidentEvent[] }

export type MediaNotificationKind = "calendar-event-upcoming" | "season-available" | "episode-available" | "movie-release";
export interface MediaNotification {
  id: string; kind: MediaNotificationKind; titleId: UUID; subjectTitleId?: UUID; title: string; seriesTitle?: string;
  releaseDate?: string; seasonNumber?: number; episodeNumber?: number; availableAt: string; readAt?: string; createdAt: string;
}
export interface MediaNotificationPage { notifications: MediaNotification[]; nextCursor?: string }
export interface MediaNotificationFollowInput { timezone: string; horizonDays: number; leadDays: number }
export interface MediaNotificationSubscription extends MediaNotificationFollowInput { titleId: UUID; createdAt: string; updatedAt: string }
export interface MediaNotificationSubscriptions { subscriptions: MediaNotificationSubscription[] }

export type PlaybackFailoverError = "source_failed" | "source_timeout" | "ended_early" | "decode_failed" | "access_denied" | "user_cancelled";
export interface PlaybackFailoverCreateInput { candidateSourceRefs: string[]; selectedSourceRef: string; maximumAttempts?: number }
export interface PlaybackFailoverAdvanceInput { error: PlaybackFailoverError; positionSeconds: number; expectedRevision: number }
export interface PlaybackFailoverCandidateHealth { position: number; status: "current" | "available" | "cooling_down"; cooldownUntil?: string }
export interface PlaybackFailoverState {
  id: UUID; currentSourceRef?: string; currentPosition: number; positionSeconds: number; attemptCount: number; maximumAttempts: number;
  revision: number; status: "active" | "exhausted" | "cancelled"; lastError?: PlaybackFailoverError; explanation?: string;
  candidateHealth: PlaybackFailoverCandidateHealth[]; expiresAt: string;
}

export interface AccessibilityPreferencesDocument {
  revision: number; reducedMotion: "system" | "reduce" | "no-preference"; highContrast: "system" | "more" | "standard";
  textScale: 100 | 115 | 130; captions: "system" | "on" | "off"; audioDescription: boolean; focusIndicators: "standard" | "enhanced";
}

export interface FolderQuery { page?: number; limit?: number; language?: string; region?: string }
export interface SearchCatalogQuery {
  skip?: number; limit?: number; language?: string; extras?: ReadonlyArray<readonly [string, string]>;
}
export interface LibraryQuery { mediaType?: TitleMediaType; page?: number; pageSize?: number }
export interface MetadataQuery { language?: string }
export interface SeriesQuery extends MetadataQuery { mappingProvider?: SeriesMappingProvider; episodeOrder?: string }
export interface ResolvePlaybackInput {
  titleId?: string; preferredAudioTrack?: number; preferredSubtitleId?: string;
  startSeconds?: number; externalPlayer?: boolean;
}
export interface PollDeviceAuthorizationOptions {
  signal?: AbortSignal;
  onPending?: (nextDelaySeconds: number) => void;
}
