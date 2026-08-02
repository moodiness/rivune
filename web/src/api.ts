import { translate as t } from "./i18n";
import type {
  Account,
  AvatarPreset,
  CalendarResponse,
  Collection,
  CollectionSaveInput,
  CollectionExportDocument,
  CollectionImportResult,
  ContinueWatching,
  Discovery,
  DeviceAuthorization,
  InstalledAddon,
  NotificationBroadcast,
  LibraryPage,
  MaintenanceSettings,
  MetadataRefreshSchedule,
  MetadataRefreshScheduleInput,
  OperationAction,
  OperationRun,
  OperationsOverview,
  MovieMetadata,
  PlaybackCapabilities,
  PlaybackActivity,
  PlaybackMarkerList,
  PlaybackPreparation,
  PlaybackSession,
  PlaybackSourceList,
  Profile,
  ProfileSession,
  ResolvedFolder,
  ResourceBatch,
  SettingsLayer,
  SessionNotification,
  SeasonMetadata,
  SeriesMetadata,
  SettingsValues,
  TitleReference,
  TokenPair,
  TrailerList,
  TrackingDeviceAuthorization,
  TrackingPreferences,
  TrackingProvider,
  TrackingStatus,
} from "./types";

const API_BASE = "/api/v1";
const ACCESS_KEY = "rivune.access";
const REFRESH_KEY = "rivune.refresh";
const DEVICE_KEY = "rivune.device";
let refreshPromise: Promise<boolean> | null = null;
let metadataLanguage = navigator.language;
let trailerLanguage = navigator.language;
let trailerCaptionLanguage = navigator.language;
let metadataRegion = region();
let seriesMappingProvider: "tmdb" | "tvdb" = "tmdb";
export const PROFILE_SELECTION_REQUIRED_EVENT = "rivune:profile-selection-required";
export const MAINTENANCE_MODE_EVENT = "rivune:maintenance-mode";
let currentMaintenanceMessage: string | null = null;

export function maintenanceModeMessage(): string | null {
  return currentMaintenanceMessage;
}

export function clearMaintenanceMode(): void {
  currentMaintenanceMessage = null;
}

export class APIError extends Error {
  constructor(public status: number, public code: string, message: string) {
    super(message);
    this.name = "APIError";
  }
}


function saveTokens(tokens: TokenPair) {
  sessionStorage.setItem(ACCESS_KEY, tokens.accessToken);
  localStorage.setItem(ACCESS_KEY, tokens.accessToken);
  localStorage.setItem(REFRESH_KEY, tokens.refreshToken);
  localStorage.setItem(DEVICE_KEY, tokens.deviceId);
}

export function clearSession() {
  sessionStorage.removeItem(ACCESS_KEY);
  localStorage.removeItem(ACCESS_KEY);
  localStorage.removeItem(REFRESH_KEY);
}

async function refreshSession(): Promise<boolean> {
  const refreshToken = localStorage.getItem(REFRESH_KEY);
  if (!refreshToken) return false;
  const response = await fetch(`${API_BASE}/auth/refresh`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ refreshToken }),
  });
  if (!response.ok) {
    clearSession();
    return false;
  }
  const tokens = (await response.json()) as TokenPair;
  saveTokens(tokens);
  return true;
}

async function request<T>(path: string, init: RequestInit = {}, retry = true): Promise<T> {
  const headers = new Headers(init.headers);
  if (init.body && !(init.body instanceof FormData) && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const token = sessionStorage.getItem(ACCESS_KEY) ?? localStorage.getItem(ACCESS_KEY);
  if (token) headers.set("Authorization", `Bearer ${token}`);
  const requestURL = path.startsWith("/.well-known") || path === "/health" ? path : `${API_BASE}${path}`;
  const response = await fetch(requestURL, { ...init, headers });
  if (response.status === 401 && retry && localStorage.getItem(REFRESH_KEY)) {
    refreshPromise ??= refreshSession().finally(() => { refreshPromise = null; });
    if (await refreshPromise) return request<T>(path, init, false);
  }
  if (!response.ok) {
    let code = "request_failed";
    let message = t("api.error.requestFailed", { status: response.status });
    let publicMessage: string | undefined;
    try {
      const body = await response.json() as { error?: { code?: string; message?: string; publicMessage?: unknown } };
      code = body.error?.code ?? code;
      message = body.error?.message ?? message;
      if (typeof body.error?.publicMessage === "string") publicMessage = body.error.publicMessage;
    } catch { /* response without JSON */ }
    if (code === "profile_selection_required") {
      window.dispatchEvent(new Event(PROFILE_SELECTION_REQUIRED_EVENT));
    }
    if (code === "maintenance_mode") {
      currentMaintenanceMessage = publicMessage ?? "";
      window.dispatchEvent(new CustomEvent<{ message: string }>(MAINTENANCE_MODE_EVENT, { detail: { message: currentMaintenanceMessage } }));
    }
    throw new APIError(response.status, code, message);
  }
  if (response.status === 204) return undefined as T;
  return response.json() as Promise<T>;
}

const query = (values: Record<string, string | number | boolean | undefined>) => {
  const params = new URLSearchParams();
  Object.entries(values).forEach(([key, value]) => {
    if (value !== undefined && value !== "") params.set(key, String(value));
  });
  const encoded = params.toString();
  return encoded ? `?${encoded}` : "";
};

type ProfileAccessInput = {
  enabled?: boolean;
  availableFrom?: string | null;
  availableUntil?: string | null;
  accessStartTime?: string | null;
  accessEndTime?: string | null;
};

export const api = {
  discovery: () => request<Discovery>("/.well-known/rivune", {}, false),
  setup: (input: { instanceName: string; admin: { username: string; password: string }; profileName: string }, token: string) =>
    request<{ instance: { id: string }; admin: { id: string }; profile: { id: string } }>("/setup", {
      method: "POST", headers: { Authorization: `Bearer ${token}` }, body: JSON.stringify(input),
    }, false),
  login: async (username: string, password: string) => {
    const tokens = await request<TokenPair>("/auth/login", {
      method: "POST",
      body: JSON.stringify({ username, password, device: { id: localStorage.getItem(DEVICE_KEY) || undefined, name: browserName(), platform: "web" } }),
    }, false);
    saveTokens(tokens);
    return tokens;
  },
  beginDeviceAuthorization: () => request<DeviceAuthorization>("/auth/device-code", {
    method: "POST",
    body: JSON.stringify({ deviceName: browserName(), platform: "web" }),
  }, false),
  exchangeDeviceAuthorization: async (deviceCode: string) => {
    const tokens = await request<TokenPair>("/auth/device-code/token", {
      method: "POST",
      body: JSON.stringify({ deviceCode }),
    }, false);
    saveTokens(tokens);
    return tokens;
  },
  approveDeviceAuthorization: (userCode: string) => request<void>("/auth/device-code/approve", {
    method: "POST",
    body: JSON.stringify({ userCode }),
  }),
  restore: refreshSession,
  logout: async () => {
    try { await request<void>("/auth/logout", { method: "POST" }, false); } finally { clearSession(); }
  },
  me: () => request<Account>("/auth/me"),
  profiles: () => request<{ profiles: Profile[] }>("/profiles"),
  selectProfile: (id: string, pin?: string) => request<{ profile: Profile; expiresAt: string }>(`/profiles/${id}/select`, { method: "POST", body: JSON.stringify(pin ? { pin } : {}) }),
  clearProfile: () => request<void>("/profiles/selection", { method: "DELETE" }),
  avatarPresets: () => request<{ presets: AvatarPreset[] }>("/profile-avatars"),
  createProfile: (input: { name: string; isChild: boolean; pin?: string } & ProfileAccessInput) => request<Profile>("/profiles", { method: "POST", body: JSON.stringify(input) }),
  updateProfile: (id: string, input: { name?: string; isChild?: boolean; pin?: string | null } & ProfileAccessInput) => request<Profile>(`/profiles/${id}`, { method: "PATCH", body: JSON.stringify(input) }),
  deleteProfile: (id: string) => request<void>(`/profiles/${id}`, { method: "DELETE" }),
  setProfileAvatar: (id: string, presetId: string) => request<{ avatar: Profile["avatar"] }>(`/profiles/${id}/avatar/preset`, { method: "PUT", body: JSON.stringify({ presetId }) }),
  uploadProfileAvatar: (id: string, image: File) => request<{ avatar: Profile["avatar"] }>(`/profiles/${id}/avatar`, { method: "PUT", headers: { "Content-Type": image.type }, body: image }),
  profileSessions: (id: string) => request<{ sessions: ProfileSession[] }>(`/profiles/${id}/sessions`),
  revokeProfileSession: (profileId: string, sessionId: string) => request<void>(`/profiles/${profileId}/sessions/${sessionId}`, { method: "DELETE" }),
  sessionNotifications: (after = "0") => request<{ notifications: SessionNotification[] }>(`/auth/notifications${query({ after })}`),
  acknowledgeSessionNotification: (notificationId: string) => request<void>(`/auth/notifications/${notificationId}`, { method: "DELETE" }),
  broadcastSessionNotification: (idempotencyKey: string, message: string) => request<NotificationBroadcast>("/auth/notifications/broadcast", { method: "POST", body: JSON.stringify({ idempotencyKey, message }) }),
  sendProfileSessionNotification: (profileId: string, sessionId: string, message: string) => request<SessionNotification>(`/profiles/${profileId}/sessions/${sessionId}/notifications`, { method: "POST", body: JSON.stringify({ message }) }),

  collections: (signal?: AbortSignal) => request<{ collections: Collection[] }>("/collections", { signal }),
  calendar: (from: string, to: string, signal?: AbortSignal) => request<CalendarResponse>(`/calendar${query({ from, to, language: metadataLanguage })}`, { signal }),
  collection: (id: string) => request<Collection>(`/collections/${id}`),
  createCollection: (input: CollectionSaveInput) => request<Collection>("/collections", { method: "POST", body: JSON.stringify(input) }),
  updateCollection: (id: string, input: CollectionSaveInput) => request<Collection>(`/collections/${id}`, { method: "PUT", body: JSON.stringify(input) }),
  deleteCollection: (id: string) => request<void>(`/collections/${id}`, { method: "DELETE" }),
  reorderCollections: (collectionIds: string[]) => request<{ collections: Collection[] }>("/collections/order", { method: "PUT", body: JSON.stringify({ collectionIds }) }),
  exportCollections: () => request<CollectionExportDocument>("/collections/export"),
  importCollections: (document: unknown) => request<CollectionImportResult>("/collections/import", { method: "POST", body: JSON.stringify(document) }),
  configureMetadataLocale: (language?: string, regionCode?: string, preferredAudioLanguage?: string, mappingProvider?: string, preferredSubtitleLanguage?: string) => {
    const automaticLanguage = preferredAudioLanguage && preferredAudioLanguage !== "auto" ? preferredAudioLanguage : navigator.language;
    metadataLanguage = language && language !== "auto" ? language : automaticLanguage;
    metadataRegion = regionCode && regionCode !== "auto" ? regionCode : region();
    trailerLanguage = automaticLanguage;
    trailerCaptionLanguage = preferredSubtitleLanguage && preferredSubtitleLanguage !== "auto" ? preferredSubtitleLanguage : navigator.language;
    seriesMappingProvider = mappingProvider === "tvdb" ? "tvdb" : "tmdb";
  },
  metadataLocale: () => metadataLanguage,
  resolveFolder: (collectionId: string, folderId: string, page = 1, signal?: AbortSignal) => request<ResolvedFolder>(`/collections/${collectionId}/folders/${folderId}/items${query({ page, limit: 100, language: metadataLanguage, region: metadataRegion })}`, { signal }),
  tmdbLookup: (kind: string, search: string) => request<{ results: { id: number; name: string; imageUrl?: string }[] }>(`/collections/tmdb/lookup${query({ kind, query: search, language: metadataLanguage, page: 1 })}`),
  tmdbGenres: (mediaType: string) => request<{ genres: { id: number; name: string }[] }>(`/collections/tmdb/genres${query({ mediaType, language: metadataLanguage })}`),

  addons: () => request<{ addons: InstalledAddon[] }>("/addons"),
  installAddon: (transportUrl: string, profileIds: string[]) => request<InstalledAddon>("/addons", { method: "POST", body: JSON.stringify({ transportUrl, profileIds }) }),
  refreshAddon: (id: string) => request<InstalledAddon>(`/addons/${id}/refresh`, { method: "POST" }),
  updateAddon: (id: string, transportUrl: string, profileIds: string[]) => request<InstalledAddon>(`/addons/${id}`, { method: "PUT", body: JSON.stringify({ transportUrl, profileIds }) }),
  reorderAddons: (addonIds: string[]) => request<{ addons: InstalledAddon[] }>("/addons/order", { method: "PUT", body: JSON.stringify({ addonIds }) }),
  deleteAddon: (id: string) => request<void>(`/addons/${id}`, { method: "DELETE" }),
  addonCatalogs: () => request<{ catalogs: Array<{ addonId: string; manifestId: string; position: number; catalog: { type: string; id: string; name?: string }; addonCatalog: boolean }> }>("/addons/catalogs"),
  search: (type: string, search: string) => request<ResourceBatch>(`/addons/catalogs/search/${encodeURIComponent(type)}${query({ search })}`),
  resources: (resource: string, type: string, id: string) => request<ResourceBatch>(`/addons/resources/${encodeURIComponent(resource)}/${encodeURIComponent(type)}/${encodeURIComponent(id)}`),

  resolveTitle: (input: {
    mediaType: string;
    provider: string;
    externalId: string;
    resourceId: string;
    title: string;
    posterUrl?: string;
    backgroundUrl?: string;
    releaseInfo?: string;
    released?: string;
  }) => request<TitleReference>("/titles/resolve", { method: "POST", body: JSON.stringify(input) }),
  playbackSources: (input: { mediaType: string; resourceId: string; capabilities: PlaybackCapabilities }, signal?: AbortSignal) =>
    request<PlaybackSourceList>("/playback/sources", { method: "POST", body: JSON.stringify(input), signal }),
  playbackMarkers: (imdbId: string, season: number, episode: number, signal?: AbortSignal) =>
    request<PlaybackMarkerList>(`/playback/markers${query({ imdbId, season, episode })}`, { signal }),
  preparePlayback: (input: { sourceRef: string; startSeconds?: number }, signal?: AbortSignal) =>
    request<PlaybackPreparation>("/playback/prepare", { method: "POST", body: JSON.stringify(input), signal }),
  resolvePlayback: (input: { sourceRef: string; titleId?: string; startSeconds?: number; preferredAudioTrack?: number; preferredSubtitleId?: string }) =>
    request<PlaybackSession>("/playback/resolve", { method: "POST", body: JSON.stringify(input) }),
  stopPlayback: (sessionId: string) => request<void>(`/playback/sessions/${encodeURIComponent(sessionId)}`, { method: "DELETE", keepalive: true }),
  playbackActivity: () => request<PlaybackActivity>("/playback/activity"),
  stopPlaybackActivitySession: (sessionId: string) => request<void>(`/playback/activity/sessions/${encodeURIComponent(sessionId)}`, { method: "DELETE" }),
  operations: () => request<OperationsOverview>("/operations"),
  updateMetadataRefreshSchedule: (input: MetadataRefreshScheduleInput) =>
    request<MetadataRefreshSchedule>("/operations/schedules/metadata-refresh", { method: "PUT", body: JSON.stringify(input) }),
  runOperation: (action: OperationAction) =>
    request<OperationRun>(`/operations/actions/${encodeURIComponent(action)}`, { method: "POST" }),
  movieDetails: (titleId: string) => request<MovieMetadata>(`/metadata/titles/${encodeURIComponent(titleId)}${query({ language: metadataLanguage })}`),
  seriesDetails: (titleId: string, options?: { mappingProvider?: "tmdb" | "tvdb"; episodeOrderId?: string }) => request<SeriesMetadata>(`/metadata/series/${encodeURIComponent(titleId)}${query({ language: metadataLanguage, mappingProvider: options?.mappingProvider ?? seriesMappingProvider, episodeOrder: options?.episodeOrderId })}`),
  seasonDetails: (seasonId: string, signal?: AbortSignal, mappingProvider = seriesMappingProvider) => request<SeasonMetadata>(`/metadata/seasons/${encodeURIComponent(seasonId)}${query({ language: metadataLanguage, mappingProvider })}`, { signal }),
  trailers: (titleId: string, seasonNumber?: number) => request<TrailerList>(`/metadata/titles/${encodeURIComponent(titleId)}/trailers${query({ language: trailerLanguage, captionLanguage: trailerCaptionLanguage, seasonNumber })}`),

  library: (mediaType = "") => request<LibraryPage>(`/library${query({ mediaType, page: 1, pageSize: 100 })}`),
  continueWatching: (signal?: AbortSignal) => request<ContinueWatching>("/continue-watching?limit=30", { signal }),
  progress: (titleId: string) => request<import("./types").PlaybackProgress | undefined>(`/progress/${encodeURIComponent(titleId)}`),
  updateProgress: (titleId: string, input: { positionSeconds: number; durationSeconds: number; completed: boolean; expectedVersion: number }) => request<import("./types").PlaybackProgress>(`/progress/${encodeURIComponent(titleId)}`, { method: "PUT", body: JSON.stringify(input) }),
  setWatched: (titleId: string, watched: boolean, expectedVersion: number) => request<import("./types").PlaybackProgress>(`/titles/${encodeURIComponent(titleId)}/watched${watched ? "" : query({ expectedVersion })}`, { method: watched ? "POST" : "DELETE", body: watched ? JSON.stringify({ expectedVersion }) : undefined }),
  addLibrary: (titleId: string) => request(`/library/${encodeURIComponent(titleId)}`, { method: "PUT" }),
  removeLibrary: (titleId: string) => request<void>(`/library/${encodeURIComponent(titleId)}`, { method: "DELETE" }),

  instanceSettings: () => request<SettingsLayer>("/settings"),
  maintenanceSettings: () => request<MaintenanceSettings>("/settings/maintenance"),
  updateMaintenanceSettings: (settings: MaintenanceSettings) => request<MaintenanceSettings>("/settings/maintenance", { method: "PUT", body: JSON.stringify(settings) }),
  profileSettings: (id: string) => request<SettingsLayer>(`/profiles/${id}/settings`),
  effectiveSettings: (id: string) => request<{ schemaVersion: number; settings: SettingsValues; sources: Record<string, string> }>(`/profiles/${id}/settings/effective`),
  updateInstanceSettings: (settings: SettingsValues) => request<SettingsLayer>("/settings", { method: "PATCH", body: JSON.stringify(settings) }),
  updateProfileSettings: (id: string, settings: SettingsValues) => request<SettingsLayer>(`/profiles/${id}/settings`, { method: "PATCH", body: JSON.stringify(settings) }),
  trackingStatuses: (profileId: string) => request<{ providers: TrackingStatus[] }>(`/profiles/${encodeURIComponent(profileId)}/tracking`),
  beginTrackingAuthorization: (profileId: string, provider: TrackingProvider) =>
    request<TrackingDeviceAuthorization>(`/profiles/${encodeURIComponent(profileId)}/tracking/${provider}/device-code`, { method: "POST" }),
  completeTrackingAuthorization: (profileId: string, provider: TrackingProvider, authorizationId: string) =>
    request<TrackingStatus | { pending: true }>(`/profiles/${encodeURIComponent(profileId)}/tracking/${provider}/device-code/${encodeURIComponent(authorizationId)}/token`, { method: "POST" }),
  updateTrackingPreferences: (profileId: string, provider: TrackingProvider, preferences: TrackingPreferences) =>
    request<TrackingStatus>(`/profiles/${encodeURIComponent(profileId)}/tracking/${provider}`, { method: "PATCH", body: JSON.stringify(preferences) }),
  disconnectTracking: (profileId: string, provider: TrackingProvider) =>
    request<void>(`/profiles/${encodeURIComponent(profileId)}/tracking/${provider}`, { method: "DELETE" }),
};

function browserName() {
  const agent = navigator.userAgent;
  if (agent.includes("Firefox")) return "Rivune · Firefox";
  if (agent.includes("Edg/")) return "Rivune · Edge";
  if (agent.includes("Chrome")) return "Rivune · Chrome";
  if (agent.includes("Safari")) return "Rivune · Safari";
  return "Rivune · Web";
}

function region() {
  const parts = navigator.language.split("-");
  return parts[1]?.toUpperCase() || "US";
}
