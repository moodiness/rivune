import { translate as t } from "./i18n";
import { clearMediaCaches } from "./homeCache";
import { clearMetadataCache } from "./metadataCache";
import type {
  Account,
  AddonCatalogDescriptor,
  AddonDiagnosticsResponse,
  AddonPreviewResponse,
  AccessCategory,
  AvatarPreset,
  CalendarResponse,
  CategoryInput,
  Collection,
  CollectionListResponse,
  CollectionSaveInput,
  CollectionExportDocument,
  CollectionImportResult,
  ContinueWatching,
  Discovery,
  CustomSeriesResolveInput,
  CustomSeriesResolveResult,
  DeviceAuthorization,
  DeviceUpdateInput,
  InstalledAddon,
  InstallAddonInput,
  UpdateAddonInput,
  ManagedAddon,
  NotificationBroadcast,
  LibraryPage,
  MaintenanceSettings,
  ManagedDevice,
  MetadataRefreshSchedule,
  MetadataRefreshScheduleInput,
  OperationAction,
  OperationRun,
  OperationsOverview,
  MovieMetadata,
  PlaybackCapabilities,
  PlaybackActivity,
  PlaybackProgressBatch,
  PlaybackMarkerList,
  PlaybackPreparation,
  PlaybackSession,
  PlaybackSourceList,
  Profile,
  ProfileSession,
  ResolvedFolder,
  ResourceBatch,
  ResourceResult,
  SettingsLayer,
  SessionNotification,
  SeasonMetadata,
  SeriesMetadata,
  SettingsValues,
  TitleReference,
  SetWatchedBatchItem,
  SetWatchedBatchResult,
  TokenPair,
  TrailerList,
  TrackingDeviceAuthorization,
  TrackingPreferences,
  TrackingProvider,
  TrackingStatus,
  TVLibraryIdentity,
  TVLibraryMembershipResult,
} from "./types";

const API_BASE = "/api/v1";
const ACCESS_KEY = "rivune.access";
const REFRESH_KEY = "rivune.refresh";
const SESSION_KEY = "rivune.session";
const DEVICE_KEY = "rivune.device";
const REFRESH_LOCK = "rivune.auth.refresh";
const PROFILE_KEY = "rivune.profile";
const PROFILE_CONTEXT_KEY = "rivune.profile.context";
export const PROFILE_SELECTION_BROADCAST_KEY = "rivune.profile.selection";
const SHARED_PROFILE_CONTEXT_KEY = "rivune.profile.shared-context";
let refreshPromise: Promise<boolean> | null = null;
let metadataLanguage = navigator.language;
let trailerLanguage = navigator.language;
let trailerCaptionLanguage = navigator.language;
let metadataRegion = region();
let seriesMappingProvider: "tmdb" | "tvdb" = "tmdb";
export const PROFILE_SELECTION_REQUIRED_EVENT = "rivune:profile-selection-required";
export const MAINTENANCE_MODE_EVENT = "rivune:maintenance-mode";
export const DEMO_UNAVAILABLE_EVENT = "rivune:demo-unavailable";
export const DEMO_HINT_KEY = "rivune.demo";
let currentMaintenanceMessage: string | null = null;
let demoUnavailableDispatched = false;
export function maintenanceModeMessage(): string | null {
  return currentMaintenanceMessage;
}

export function clearMaintenanceMode(): void {
  currentMaintenanceMessage = null;
}

export function hasDemoHint(): boolean {
  try {
    return localStorage.getItem(DEMO_HINT_KEY) === "1";
  } catch {
    return false;
  }
}

export function clearDemoClientState(): void {
  clearSession();
  clearMediaCaches();
  clearMetadataCache();
  currentMaintenanceMessage = null;
  try {
    localStorage.removeItem(DEMO_HINT_KEY);
  } catch {
    // The backend cookie remains the only authority when storage is unavailable.
  }
}

export function prepareDemoAttempt(): void {
  demoUnavailableDispatched = false;
  clearDemoClientState();
}

export function rememberDemoSession(): void {
  demoUnavailableDispatched = false;
  try {
    localStorage.setItem(DEMO_HINT_KEY, "1");
  } catch {
    // The non-secret hint is optional; the cookie remains authoritative.
  }
}

export class APIError extends Error {
  constructor(public status: number, public code: string, message: string) {
    super(message);
    this.name = "APIError";
  }
}
type SharedProfileContext = {
  sessionId: string;
  profileId: string;
  profileContext: string;
};

function readSharedProfileContext(): SharedProfileContext | null {
  try {
    const parsed = JSON.parse(localStorage.getItem(SHARED_PROFILE_CONTEXT_KEY) ?? "null") as Partial<SharedProfileContext> | null;
    if (!parsed || typeof parsed.sessionId !== "string" || typeof parsed.profileId !== "string" || typeof parsed.profileContext !== "string") return null;
    if (!parsed.sessionId || parsed.sessionId !== localStorage.getItem(SESSION_KEY) || !parsed.profileId || !parsed.profileContext) return null;
    return parsed as SharedProfileContext;
  } catch {
    return null;
  }
}

function rememberSharedProfileContext(profileID: string, profileContext: string): void {
  const sessionId = localStorage.getItem(SESSION_KEY);
  if (!sessionId) return;
  try {
    localStorage.setItem(SHARED_PROFILE_CONTEXT_KEY, JSON.stringify({ sessionId, profileId: profileID, profileContext } satisfies SharedProfileContext));
  } catch {
    // The active tab remains authorized through its tab-local capability.
  }
}

function clearSharedProfileContext(expectedContext?: string): void {
  try {
    if (expectedContext && readSharedProfileContext()?.profileContext !== expectedContext) return;
    localStorage.removeItem(SHARED_PROFILE_CONTEXT_KEY);
  } catch {
    // The server remains authoritative when browser storage is unavailable.
  }
}

export function profileRequestContext(profileID: string): string | null {
  const tabContext = sessionStorage.getItem(PROFILE_KEY) === profileID
    ? sessionStorage.getItem(PROFILE_CONTEXT_KEY)
    : null;
  if (tabContext) {
    rememberSharedProfileContext(profileID, tabContext);
    return tabContext;
  }
  const shared = readSharedProfileContext();
  if (!shared || shared.profileId !== profileID) return null;
  sessionStorage.setItem(PROFILE_KEY, profileID);
  sessionStorage.setItem(PROFILE_CONTEXT_KEY, shared.profileContext);
  return shared.profileContext;
}

export function setProfileRequestContext(profileID: string | null, profileContext: string | null): void {
  if (profileID && profileContext) {
    sessionStorage.setItem(PROFILE_KEY, profileID);
    sessionStorage.setItem(PROFILE_CONTEXT_KEY, profileContext);
    rememberSharedProfileContext(profileID, profileContext);
    return;
  }
  sessionStorage.removeItem(PROFILE_KEY);
  sessionStorage.removeItem(PROFILE_CONTEXT_KEY);
}

export function rejectProfileRequestContext(): void {
  const rejectedContext = sessionStorage.getItem(PROFILE_CONTEXT_KEY);
  if (rejectedContext) clearSharedProfileContext(rejectedContext);
}

export function broadcastProfileSelectionChange(): void {
  try {
    localStorage.setItem(PROFILE_SELECTION_BROADCAST_KEY, JSON.stringify({
      sessionId: localStorage.getItem(SESSION_KEY),
      nonce: `${Date.now()}-${Math.random()}`,
    }));
  } catch {
    // The per-tab opaque profile capability remains the server-side boundary.
  }
}



function saveTokens(tokens: TokenPair) {
  sessionStorage.setItem(ACCESS_KEY, tokens.accessToken);
  localStorage.setItem(ACCESS_KEY, tokens.accessToken);
  localStorage.setItem(SESSION_KEY, tokens.sessionId);
  localStorage.setItem(DEVICE_KEY, tokens.deviceId);
  localStorage.setItem(REFRESH_KEY, tokens.refreshToken);
}

export function clearSession() {
  sessionStorage.removeItem(ACCESS_KEY);
  sessionStorage.removeItem(PROFILE_KEY);
  sessionStorage.removeItem(PROFILE_CONTEXT_KEY);
  localStorage.removeItem(ACCESS_KEY);
  localStorage.removeItem(REFRESH_KEY);
  localStorage.removeItem(SESSION_KEY);
  clearSharedProfileContext();
}

function adoptNewerSharedSession(refreshToken: string, sessionId: string | null): boolean {
  const sharedRefreshToken = localStorage.getItem(REFRESH_KEY);
  const sharedAccessToken = localStorage.getItem(ACCESS_KEY);
  const sharedSessionId = localStorage.getItem(SESSION_KEY);
  if (!sessionId || sharedSessionId !== sessionId || !sharedRefreshToken || sharedRefreshToken === refreshToken || !sharedAccessToken) return false;
  sessionStorage.setItem(ACCESS_KEY, sharedAccessToken);
  return true;
}

async function refreshSession(): Promise<boolean> {
  const refreshToken = localStorage.getItem(REFRESH_KEY);
  if (!refreshToken) return false;
  const sessionId = localStorage.getItem(SESSION_KEY);

  const refresh = async () => {
    if (localStorage.getItem(REFRESH_KEY) !== refreshToken) {
      return adoptNewerSharedSession(refreshToken, sessionId);
    }
    const response = await fetch(`${API_BASE}/auth/refresh`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ refreshToken }),
    });
    if (!response.ok) {
      if (adoptNewerSharedSession(refreshToken, sessionId)) return true;
      if (localStorage.getItem(REFRESH_KEY) === refreshToken) clearSession();
      return false;
    }
    const tokens = (await response.json()) as TokenPair;
    if (localStorage.getItem(REFRESH_KEY) !== refreshToken) {
      return adoptNewerSharedSession(refreshToken, sessionId);
    }
    saveTokens(tokens);
    return true;
  };

  if ("locks" in navigator) {
    return navigator.locks.request(REFRESH_LOCK, refresh);
  }
  return refresh();
}

async function request<T>(path: string, init: RequestInit = {}, retry = true, attachSession = true, handleDemoUnavailable = true): Promise<T> {
  const requestSessionId = localStorage.getItem(SESSION_KEY);
  const headers = new Headers(init.headers);
  if (init.body && !(init.body instanceof FormData) && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const token = attachSession ? sessionStorage.getItem(ACCESS_KEY) ?? localStorage.getItem(ACCESS_KEY) : null;
  if (token && !headers.has("Authorization")) headers.set("Authorization", `Bearer ${token}`);
  const profileContext = token ? sessionStorage.getItem(PROFILE_CONTEXT_KEY) : null;
  if (profileContext && !headers.has("X-Rivune-Profile-Context")) {
    headers.set("X-Rivune-Profile-Context", profileContext);
  }
  const requestURL = path.startsWith("/.well-known") || path === "/health" ? path : `${API_BASE}${path}`;
  const response = await fetch(requestURL, { ...init, headers, credentials: "same-origin" });
  if (response.status === 401 && retry && localStorage.getItem(REFRESH_KEY)) {
    refreshPromise ??= refreshSession().finally(() => { refreshPromise = null; });
    if (await refreshPromise && requestSessionId !== null && localStorage.getItem(SESSION_KEY) === requestSessionId) {
      return request<T>(path, init, false, attachSession, handleDemoUnavailable);
    }
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
    if (code === "demo_unavailable" && handleDemoUnavailable) {
      clearDemoClientState();
      if (!demoUnavailableDispatched) {
        demoUnavailableDispatched = true;
        window.dispatchEvent(new Event(DEMO_UNAVAILABLE_EVENT));
      }
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
  discovery: () => request<Discovery>("/.well-known/rivune", {}, false, false),
  setup: async (input: { instanceName: string; admin: { username: string; password: string }; profileName: string }, token: string) => {
    const result = await request<{ instance: { id: string }; admin: { id: string }; profile: { id: string } }>("/setup", {
      method: "POST", headers: { Authorization: `Bearer ${token}` }, body: JSON.stringify(input),
    }, false, false);
    clearDemoClientState();
    return result;
  },
  startDemo: () => request<{ account: Account }>("/demo/sessions", { method: "POST" }, false, false, false),
  demoSession: () => request<{ account: Account }>("/demo/session", {}, false, false),
  resetDemo: () => request<{ account: Account }>("/demo/session/reset", { method: "POST" }, false, false),
  exitDemo: () => request<void>("/demo/session", { method: "DELETE" }, false, false),
  login: async (username: string, password: string) => {
    const tokens = await request<TokenPair>("/auth/login", {
      method: "POST",
      body: JSON.stringify({ username, password, device: { id: localStorage.getItem(DEVICE_KEY) || undefined, name: browserName(), platform: "web" } }),
    }, false, false);
    saveTokens(tokens);
    return tokens;
  },
  beginDeviceAuthorization: () => request<DeviceAuthorization>("/auth/device-code", {
    method: "POST",
    body: JSON.stringify({ deviceName: browserName(), platform: "web" }),
  }, false, false),
  exchangeDeviceAuthorization: async (deviceCode: string) => {
    const tokens = await request<TokenPair>("/auth/device-code/token", {
      method: "POST",
      body: JSON.stringify({ deviceCode }),
    }, false, false);
    saveTokens(tokens);
    return tokens;
  },
  approveDeviceAuthorization: (input: { userCode: string; categoryId: string; deviceName?: string; internalNote?: string | null }) => request<void>("/auth/device-code/approve", {
    method: "POST",
    body: JSON.stringify(input),
  }),
  restore: refreshSession,
  logout: async () => {
    try { await request<void>("/auth/logout", { method: "POST" }, false); } finally { clearSession(); }
  },
  me: () => request<Account>("/auth/me"),
  categories: async () => (await request<{ categories: AccessCategory[] }>("/categories")).categories,
  createCategory: (input: CategoryInput) => request<AccessCategory>("/categories", { method: "POST", body: JSON.stringify(input) }),
  updateCategory: (id: string, input: Partial<CategoryInput> & { isDefault?: true }) => request<AccessCategory>(`/categories/${id}`, { method: "PATCH", body: JSON.stringify(input) }),
  deleteCategory: (id: string, reassignToCategoryId?: string) => request<void>(`/categories/${id}`, { method: "DELETE", body: JSON.stringify({ reassignToCategoryId }) }),
  reorderCategories: async (categoryIds: string[]) => (await request<{ categories: AccessCategory[] }>("/categories/order", { method: "PUT", body: JSON.stringify({ categoryIds }) })).categories,
  devices: async (categoryId?: string) => (await request<{ devices: ManagedDevice[] }>(`/devices${query({ categoryId })}`)).devices,
  updateDevice: (id: string, input: DeviceUpdateInput) => request<ManagedDevice>(`/devices/${id}`, { method: "PATCH", body: JSON.stringify(input) }),
  deleteDevice: (id: string) => request<void>(`/devices/${id}`, { method: "DELETE" }),
  moveProfilesToCategory: (profileIds: string[], categoryId: string) => request<void>("/profiles/category-moves", { method: "POST", body: JSON.stringify({ profileIds, categoryId }) }),
  moveDevicesToCategory: (deviceIds: string[], categoryId: string) => request<void>("/devices/category-moves", { method: "POST", body: JSON.stringify({ deviceIds, categoryId }) }),
  profiles: () => request<{ profiles: Profile[] }>("/profiles"),
  selectProfile: (id: string, pin?: string) => request<{ profile: Profile; expiresAt: string; profileContext: string }>(`/profiles/${id}/select`, { method: "POST", body: JSON.stringify(pin ? { pin } : {}) }),
  clearProfile: async () => {
    await request<void>("/profiles/selection", { method: "DELETE" });
    clearSharedProfileContext();
  },
  avatarPresets: () => request<{ presets: AvatarPreset[] }>("/profile-avatars"),
  createProfile: (input: { name: string; description?: string | null; categoryId: string; isChild?: boolean; pin?: string } & ProfileAccessInput) => request<Profile>("/profiles", { method: "POST", body: JSON.stringify(input) }),
  updateProfile: (id: string, input: { name?: string; description?: string | null; categoryId?: string; isChild?: boolean; pin?: string | null } & ProfileAccessInput) => request<Profile>(`/profiles/${id}`, { method: "PATCH", body: JSON.stringify(input) }),
  deleteProfile: (id: string) => request<void>(`/profiles/${id}`, { method: "DELETE" }),
  setProfileAvatar: (id: string, presetId: string) => request<{ avatar: Profile["avatar"] }>(`/profiles/${id}/avatar/preset`, { method: "PUT", body: JSON.stringify({ presetId }) }),
  uploadProfileAvatar: (id: string, image: File) => request<{ avatar: Profile["avatar"] }>(`/profiles/${id}/avatar`, { method: "PUT", headers: { "Content-Type": image.type }, body: image }),
  profileSessions: (id: string) => request<{ sessions: ProfileSession[] }>(`/profiles/${id}/sessions`),
  revokeProfileSession: (profileId: string, sessionId: string) => request<void>(`/profiles/${profileId}/sessions/${sessionId}`, { method: "DELETE" }),
  sessionNotifications: (after = "0") => request<{ notifications: SessionNotification[] }>(`/auth/notifications${query({ after })}`),
  acknowledgeSessionNotification: (notificationId: string) => request<void>(`/auth/notifications/${notificationId}`, { method: "DELETE" }),
  broadcastSessionNotification: (idempotencyKey: string, message: string) => request<NotificationBroadcast>("/auth/notifications/broadcast", { method: "POST", body: JSON.stringify({ idempotencyKey, message }) }),
  sendProfileSessionNotification: (profileId: string, sessionId: string, message: string) => request<SessionNotification>(`/profiles/${profileId}/sessions/${sessionId}/notifications`, { method: "POST", body: JSON.stringify({ message }) }),

  collections: (signal?: AbortSignal) => request<CollectionListResponse>("/collections", { signal }),
  calendar: (from: string, to: string, signal?: AbortSignal) => request<CalendarResponse>(`/calendar${query({ from, to, language: metadataLanguage })}`, { signal }),
  collection: (id: string) => request<Collection>(`/collections/${id}`),
  collectionManagement: (id: string) => request<Collection>(`/collections/${id}/management`),
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
  metadataScope: () => `${metadataLanguage}|${metadataRegion}`,
  resolveFolder: (collectionId: string, folderId: string, page = 1, signal?: AbortSignal) => request<ResolvedFolder>(`/collections/${collectionId}/folders/${folderId}/items${query({ page, limit: 100, language: metadataLanguage, region: metadataRegion })}`, { signal }),
  tmdbLookup: (kind: string, search: string) => request<{ results: { id: number; name: string; imageUrl?: string }[] }>(`/collections/tmdb/lookup${query({ kind, query: search, language: metadataLanguage, page: 1 })}`),
  tmdbGenres: (mediaType: string) => request<{ genres: { id: number; name: string }[] }>(`/collections/tmdb/genres${query({ mediaType, language: metadataLanguage })}`),

  addons: () => request<{ addons: InstalledAddon[] }>("/addons"),
  addonDiagnostics: () => request<AddonDiagnosticsResponse>("/addons/diagnostics"),
  previewAddon: (input: InstallAddonInput, signal?: AbortSignal) => request<AddonPreviewResponse>("/addons/preview", { method: "POST", body: JSON.stringify(input), signal }),
  installAddon: (transportUrl: string, profileIds: string[]) => request<ManagedAddon>("/addons", { method: "POST", body: JSON.stringify({ transportUrl, profileIds } satisfies InstallAddonInput) }),
  addonManagement: (id: string) => request<ManagedAddon>(`/addons/${id}/management`),
  refreshAddon: (id: string) => request<InstalledAddon>(`/addons/${id}/refresh`, { method: "POST" }),
  updateAddon: (id: string, input: UpdateAddonInput) => request<ManagedAddon>(`/addons/${id}`, { method: "PUT", body: JSON.stringify(input) }),
  reorderAddons: (addonIds: string[]) => request<{ addons: InstalledAddon[] }>("/addons/order", { method: "PUT", body: JSON.stringify({ addonIds }) }),
  deleteAddon: (id: string) => request<void>(`/addons/${id}`, { method: "DELETE" }),
  addonCatalogs: (signal?: AbortSignal) => request<{ catalogs: AddonCatalogDescriptor[] }>("/addons/catalogs", { signal }),
  search: (type: string, search: string, skip: number, limit: number, signal?: AbortSignal) => request<ResourceBatch>(`/addons/catalogs/search/${encodeURIComponent(type)}${query({ search, skip, limit })}`, { signal }),
  addonCatalog: (addonId: string, type: string, catalogId: string, extras: { search?: string; skip?: number; limit?: number }, signal?: AbortSignal) => request<ResourceResult>(`/addons/${encodeURIComponent(addonId)}/resource/catalog/${encodeURIComponent(type)}/${encodeURIComponent(catalogId)}${query(extras)}`, { signal }),
  resources: (resource: "catalog" | "addon_catalog" | "meta", type: string, id: string) => request<ResourceBatch>(`/addons/resources/${encodeURIComponent(resource)}/${encodeURIComponent(type)}/${encodeURIComponent(id)}`),

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
    sourceAddonId?: string;
    sourceCatalogId?: string;
    sourceName?: string;
    country?: string;
    language?: string;
    category?: string;
  }) => request<TitleReference>("/titles/resolve", { method: "POST", body: JSON.stringify(input) }),
  resolveCustomSeries: (input: CustomSeriesResolveInput, signal?: AbortSignal) =>
    request<CustomSeriesResolveResult>("/titles/custom-series/resolve", { method: "POST", body: JSON.stringify(input), signal }),
  playbackSources: (input: { mediaType: string; resourceId: string; addonId?: string; capabilities: PlaybackCapabilities }, signal?: AbortSignal) =>
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

  library: (mediaType = "", page = 1, pageSize = 100) => request<LibraryPage>(`/library${query({ mediaType, page, pageSize })}`),
  tvLibraryMembership: (identities: TVLibraryIdentity[], signal?: AbortSignal) =>
    request<TVLibraryMembershipResult>("/library/membership", { method: "POST", body: JSON.stringify({ identities }), signal }),
  continueWatching: (signal?: AbortSignal) => request<ContinueWatching>("/continue-watching?limit=30", { signal }),
  dismissContinue: (titleId: string) => request<void>(`/continue-watching/${encodeURIComponent(titleId)}`, { method: "DELETE" }),
  progress: (titleId: string) => request<import("./types").PlaybackProgress | undefined>(`/progress/${encodeURIComponent(titleId)}`),
  progressBatch: (titleIds: string[], signal?: AbortSignal) =>
    request<PlaybackProgressBatch>("/progress/batch", { method: "POST", body: JSON.stringify({ titleIds }), signal }),
  updateProgress: (titleId: string, input: { positionSeconds: number; durationSeconds: number; completed: boolean; expectedVersion: number }) => request<import("./types").PlaybackProgress>(`/progress/${encodeURIComponent(titleId)}`, { method: "PUT", body: JSON.stringify(input) }),
  setWatched: (titleId: string, watched: boolean, expectedVersion: number) => request<import("./types").PlaybackProgress>(`/titles/${encodeURIComponent(titleId)}/watched${watched ? "" : query({ expectedVersion })}`, { method: watched ? "POST" : "DELETE", body: watched ? JSON.stringify({ expectedVersion }) : undefined }),
  setWatchedBatch: (items: SetWatchedBatchItem[], signal?: AbortSignal) =>
    request<SetWatchedBatchResult>("/titles/watched/batch", { method: "PUT", body: JSON.stringify({ items }), signal }),
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
