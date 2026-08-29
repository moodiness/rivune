import { translate as t } from "./i18n";
import { clearMediaCaches } from "./homeCache";
import { clearMetadataCache } from "./metadataCache";
import { safeLocalStorage, safeSessionStorage } from "./browserStorage";
import type {
  AccessibilityDocument,
  AccessibilityUpdate,
  Account,
  AddonCatalogDescriptor,
  AddonIncident,
  AddonIncidentDetail,
  AddonDiagnosticsResponse,
  AddonVerification,
  AccessCategory,
  AvatarPreset,
  CalendarResponse,
  CalendarSubscription,
  CategoryInput,
  Collection,
  CollectionListResponse,
  CollectionSaveInput,
  CollectionExportDocument,
  CollectionImportResult,
  ContinueWatching,
  ConfigurationAuditPage,
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
  JellyfinCredentialSecret,
  JellyfinCredentialStatus,
  MaintenanceSettings,
  ManagedDevice,
  MetadataRefreshSchedule,
  MediaNotificationFollowInput,
  MediaNotificationPage,
  MediaNotificationSubscription,
  MediaNotificationSubscriptions,
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
  PlaybackCommand,
  PlaybackCommandInput,
  PlaybackCommandResultCode,
  PlaybackDevice,
  PlaybackSourceList,
  PlaybackFailoverError,
  PlaybackFailoverState,
  Profile,
  ProfileSession,
  ProfileArchiveDocument,
  ProfileArchiveImportReport,
  ResolvedFolder,
  ResourceBatch,
  ResourceResult,
  ReadingQueue,
  ReadingQueueAddInput,
  ReadingQueueMutation,
  ReadingQueueMutationInput,
  ReadingQueueReorderInput,
  ReadingQueueUpdateInput,
  SettingsLayer,
  SessionNotification,
  SettingsIntegrations,
  SettingsIntegrationsPatch,
  SeasonMetadata,
  SemanticSearchPage,
  SemanticSearchRequest,
  SavedSearch,
  SavedSearchInput,
  SmartCollection,
  SmartCollectionInput,
  SmartCollectionPage,
  SeriesMetadata,
  SettingsValues,
  TitleReference,
  SetWatchedBatchItem,
  SetWatchedBatchResult,
  WebSessionTokens,
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
const DEVICE_KEY = "rivune.device";
const DEVICE_IDS_KEY = "rivune.devices.v1";
const MAX_REMEMBERED_DEVICE_IDS = 64;
const DEVICE_ID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const INSTALLATION_ID_KEY = "rivune.installation-id.v1";
const REFRESH_LOCK = "rivune.auth.refresh";
const PROFILE_KEY = "rivune.profile";
const PROFILE_CONTEXT_KEY = "rivune.profile.context";
const TAB_SESSION_KEY = "rivune.tab.session";
const LEGACY_SHARED_AUTH_KEYS = ["rivune.access", "rivune.refresh", "rivune.session", "rivune.profile.shared-context", "rivune.profile.selection"] as const;
const AUTH_CHANNEL_NAME = "rivune.auth.v1";
export type AuthCoordinationEvent = "auth-invalidated" | "profile-invalidated";
const authChannel = typeof BroadcastChannel === "undefined" ? null : new BroadcastChannel(AUTH_CHANNEL_NAME);
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
  return safeLocalStorage.getItem(DEMO_HINT_KEY) === "1";
}

export function clearDemoClientState(): void {
  clearSession();
  clearMediaCaches();
  clearMetadataCache();
  currentMaintenanceMessage = null;
  safeLocalStorage.removeItem(DEMO_HINT_KEY);
}

export function prepareDemoAttempt(): void {
  demoUnavailableDispatched = false;
  clearDemoClientState();
}

export function rememberDemoSession(): void {
  demoUnavailableDispatched = false;
  safeLocalStorage.setItem(DEMO_HINT_KEY, "1");
}

function installationId(): string {
  const persisted = safeLocalStorage.getItem(INSTALLATION_ID_KEY)?.trim();
  if (persisted && persisted.length <= 128) {
    safeLocalStorage.setItem(INSTALLATION_ID_KEY, persisted);
    return persisted;
  }

  const generated = typeof crypto.randomUUID === "function" ? crypto.randomUUID() : (() => {
    const bytes = crypto.getRandomValues(new Uint8Array(16));
    bytes[6] = ((bytes[6] ?? 0) & 0x0f) | 0x40;
    bytes[8] = ((bytes[8] ?? 0) & 0x3f) | 0x80;
    const hex = Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join("");
    return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
  })();
  safeLocalStorage.setItem(INSTALLATION_ID_KEY, generated);
  return generated;
}

export class APIError extends Error {
  constructor(public status: number, public code: string, message: string) {
    super(message);
    this.name = "APIError";
  }
}
export function subscribeAuthCoordination(listener: (event: AuthCoordinationEvent) => void): () => void {
  if (!authChannel) return () => undefined;
  const receive = (message: MessageEvent<unknown>) => {
    if (message.data === "auth-invalidated" || message.data === "profile-invalidated") listener(message.data);
  };
  authChannel.addEventListener("message", receive);
  return () => authChannel.removeEventListener("message", receive);
}

function broadcastAuthCoordination(event: AuthCoordinationEvent): void {
  authChannel?.postMessage(event);
}

function clearLegacySharedAuth(): void {
  for (const key of LEGACY_SHARED_AUTH_KEYS) safeLocalStorage.removeItem(key);
}

clearLegacySharedAuth();

export function activeProfileRequestID(): string | null {
  return safeSessionStorage.getItem(PROFILE_KEY);
}

export function profileRequestContext(profileID: string): string | null {
  return safeSessionStorage.getItem(PROFILE_KEY) === profileID
    ? safeSessionStorage.getItem(PROFILE_CONTEXT_KEY)
    : null;
}

export function setProfileRequestContext(profileID: string | null, profileContext: string | null): void {
  if (profileID && profileContext) {
    safeSessionStorage.setItem(PROFILE_KEY, profileID);
    safeSessionStorage.setItem(PROFILE_CONTEXT_KEY, profileContext);
    return;
  }
  safeSessionStorage.removeItem(PROFILE_KEY);
  safeSessionStorage.removeItem(PROFILE_CONTEXT_KEY);
}

export function rejectProfileRequestContext(): void {
  setProfileRequestContext(null, null);
}

export function broadcastProfileSelectionChange(): void {
  broadcastAuthCoordination("profile-invalidated");
}

function rememberedDeviceIDs(): string[] {
  let stored: unknown = [];
  try {
    stored = JSON.parse(safeLocalStorage.getItem(DEVICE_IDS_KEY) ?? "[]");
  } catch {
    // A malformed non-secret hint is discarded below.
  }
  const values = [safeLocalStorage.getItem(DEVICE_KEY), ...(Array.isArray(stored) ? stored : [])];
  const seen = new Set<string>();
  const result: string[] = [];
  for (const value of values) {
    if (typeof value !== "string") continue;
    const normalized = value.trim().toLowerCase();
    if (!DEVICE_ID_PATTERN.test(normalized) || seen.has(normalized)) continue;
    seen.add(normalized);
    result.push(normalized);
    if (result.length === MAX_REMEMBERED_DEVICE_IDS) break;
  }
  return result;
}

function rememberDeviceID(deviceID: string): void {
  const normalized = deviceID.trim().toLowerCase();
  const previous = rememberedDeviceIDs();
  safeLocalStorage.setItem(DEVICE_KEY, normalized);
  safeLocalStorage.setItem(DEVICE_IDS_KEY, JSON.stringify([normalized, ...previous.filter((candidate) => candidate !== normalized)].slice(0, MAX_REMEMBERED_DEVICE_IDS)));
}

function saveTokens(tokens: WebSessionTokens) {
  safeSessionStorage.setItem(ACCESS_KEY, tokens.accessToken);
  safeSessionStorage.setItem(TAB_SESSION_KEY, tokens.sessionId);
  rememberDeviceID(tokens.deviceId);
  clearLegacySharedAuth();
}

export function clearSession() {
  safeSessionStorage.removeItem(ACCESS_KEY);
  safeSessionStorage.removeItem(TAB_SESSION_KEY);
  safeSessionStorage.removeItem(PROFILE_KEY);
  safeSessionStorage.removeItem(PROFILE_CONTEXT_KEY);
  clearLegacySharedAuth();
}

async function refreshSession(): Promise<boolean> {
  const refresh = async () => {
    const response = await fetch(`${API_BASE}/auth/web/refresh`, {
      method: "POST",
      headers: { "X-Rivune-CSRF": "1" },
      credentials: "same-origin",
    });
    if (!response.ok) {
      clearSession();
      return false;
    }
    const tokens = (await response.json()) as WebSessionTokens;
    saveTokens(tokens);
    return true;
  };

  if ("locks" in navigator) return navigator.locks.request(REFRESH_LOCK, refresh);
  return refresh();
}

async function request<T>(path: string, init: RequestInit = {}, retry = true, attachSession = true, handleDemoUnavailable = true): Promise<T> {
  const requestSessionId = safeSessionStorage.getItem(TAB_SESSION_KEY);
  const headers = new Headers(init.headers);
  if (init.body && !(init.body instanceof FormData) && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const token = attachSession ? safeSessionStorage.getItem(ACCESS_KEY) : null;
  if (token && !headers.has("Authorization")) headers.set("Authorization", `Bearer ${token}`);
  const profileContext = token ? safeSessionStorage.getItem(PROFILE_CONTEXT_KEY) : null;
  if (profileContext && !headers.has("X-Rivune-Profile-Context")) {
    headers.set("X-Rivune-Profile-Context", profileContext);
  }
  const requestURL = path.startsWith("/.well-known") || path === "/health" ? path : `${API_BASE}${path}`;
  const response = await fetch(requestURL, { ...init, headers, credentials: "same-origin" });
  if (response.status === 401 && retry && attachSession) {
    refreshPromise ??= refreshSession().finally(() => { refreshPromise = null; });
    if (await refreshPromise && requestSessionId !== null && safeSessionStorage.getItem(TAB_SESSION_KEY) === requestSessionId) {
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
  discovery: async () => {
    const discovered = await request<Discovery>("/.well-known/rivune", {}, false, false);
    if (discovered.protocolVersion !== 22) throw new APIError(426, "unsupported_protocol", `Unsupported Rivune protocol ${discovered.protocolVersion}`);
    return discovered;
  },
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
    const deviceIDs = rememberedDeviceIDs();
    const tokens = await request<WebSessionTokens>("/auth/web/login", {
      method: "POST",
      headers: { "X-Rivune-CSRF": "1" },
      body: JSON.stringify({ username, password, device: { id: deviceIDs[0], ids: deviceIDs, name: browserName(), platform: "web" } }),
    }, false, false);
    saveTokens(tokens);
    broadcastAuthCoordination("auth-invalidated");
    return tokens;
  },
  beginDeviceAuthorization: () => request<DeviceAuthorization>("/auth/device-code", {
    method: "POST",
    body: JSON.stringify({ deviceName: browserName(), platform: "web", installationId: installationId() }),
  }, false, false),
  exchangeDeviceAuthorization: async (deviceCode: string) => {
    const tokens = await request<WebSessionTokens>("/auth/web/device-code/token", {
      method: "POST",
      headers: { "X-Rivune-CSRF": "1" },
      body: JSON.stringify({ deviceCode }),
    }, false, false);
    saveTokens(tokens);
    broadcastAuthCoordination("auth-invalidated");
    return tokens;
  },
  approveDeviceAuthorization: (input: { userCode: string; categoryId: string; deviceName?: string; internalNote?: string | null }) => request<void>("/auth/device-code/approve", {
    method: "POST",
    body: JSON.stringify(input),
  }),
  restore: refreshSession,
  logout: async () => {
    try {
      await request<void>("/auth/web/refresh", { method: "DELETE", headers: { "X-Rivune-CSRF": "1" } }, false, false);
    } finally {
      clearSession();
      broadcastAuthCoordination("auth-invalidated");
    }
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
    setProfileRequestContext(null, null);
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
  exportProfileArchive: (profileId: string) => request<ProfileArchiveDocument>(`/profiles/${encodeURIComponent(profileId)}/archive`),
  importProfileArchive: (profileId: string, archive: ProfileArchiveDocument) => request<ProfileArchiveImportReport>(`/profiles/${encodeURIComponent(profileId)}/archive/import`, { method: "POST", body: JSON.stringify(archive) }),
  createProfileFromArchive: (categoryId: string, archive: ProfileArchiveDocument) => request<ProfileArchiveImportReport>("/profiles/archive", { method: "POST", body: JSON.stringify({ categoryId, archive }) }),

  collections: (signal?: AbortSignal) => request<CollectionListResponse>("/collections", { signal }),
  calendar: (from: string, to: string, signal?: AbortSignal) => request<CalendarResponse>(`/calendar${query({ from, to, language: metadataLanguage })}`, { signal }),
  calendarSubscription: (profileId: string) => request<CalendarSubscription>(`/profiles/${profileId}/calendar-link`),
  createCalendarSubscription: (profileId: string) => request<CalendarSubscription>(`/profiles/${profileId}/calendar-link`, { method: "POST" }),
  rotateCalendarSubscription: (profileId: string) => request<CalendarSubscription>(`/profiles/${profileId}/calendar-link/rotate`, { method: "POST" }),
  deleteCalendarSubscription: (profileId: string) => request<void>(`/profiles/${profileId}/calendar-link`, { method: "DELETE" }),
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
  addonIncidents: () => request<{ incidents: AddonIncident[] }>("/operations/extension-incidents"),
  addonIncident: (id: string) => request<AddonIncidentDetail>(`/operations/extension-incidents/${encodeURIComponent(id)}`),
  acknowledgeAddonIncident: (id: string) => request<AddonIncident>(`/operations/extension-incidents/${encodeURIComponent(id)}/acknowledgement`, { method: "POST" }),
  semanticSearch: (input: SemanticSearchRequest, signal?: AbortSignal) => request<SemanticSearchPage>("/search/semantic", { method: "POST", body: JSON.stringify(input), signal }),
  savedSearches: () => request<{ savedSearches: SavedSearch[] }>("/saved-searches"),
  createSavedSearch: (input: SavedSearchInput) => request<SavedSearch>("/saved-searches", { method: "POST", body: JSON.stringify(input) }),
  updateSavedSearch: (id: string, input: SavedSearchInput) => request<SavedSearch>(`/saved-searches/${encodeURIComponent(id)}`, { method: "PUT", body: JSON.stringify(input) }),
  deleteSavedSearch: (id: string, expectedRevision: number) => request<void>(`/saved-searches/${encodeURIComponent(id)}${query({ expectedRevision })}`, { method: "DELETE" }),
  smartCollections: () => request<{ smartCollections: SmartCollection[] }>("/smart-collections"),
  createSmartCollection: (input: SmartCollectionInput) => request<SmartCollection>("/smart-collections", { method: "POST", body: JSON.stringify(input) }),
  updateSmartCollection: (id: string, input: SmartCollectionInput) => request<SmartCollection>(`/smart-collections/${encodeURIComponent(id)}`, { method: "PUT", body: JSON.stringify(input) }),
  deleteSmartCollection: (id: string, expectedRevision: number) => request<void>(`/smart-collections/${encodeURIComponent(id)}${query({ expectedRevision })}`, { method: "DELETE" }),
  smartCollectionItems: (id: string, page = 1, pageSize = 100) => request<SmartCollectionPage>(`/smart-collections/${encodeURIComponent(id)}/items${query({ page, pageSize })}`),
  metadataRegion: () => metadataRegion,

  addons: () => request<{ addons: InstalledAddon[] }>("/addons"),
  addonDiagnostics: () => request<AddonDiagnosticsResponse>("/addons/diagnostics"),
  verifyAddonCandidate: (input: InstallAddonInput, signal?: AbortSignal) => request<AddonVerification>("/addons/verifications", { method: "POST", body: JSON.stringify(input), signal }),
  verifyInstalledAddon: (id: string, signal?: AbortSignal) => request<AddonVerification>(`/addons/${encodeURIComponent(id)}/verifications`, { method: "POST", signal }),
  installAddon: (verificationId: string) => request<ManagedAddon>("/addons", { method: "POST", body: JSON.stringify({ verificationId }) }),
  addonManagement: (id: string) => request<ManagedAddon>(`/addons/${id}/management`),
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
  createPlaybackFailover: (input: { candidateSourceRefs: string[]; selectedSourceRef: string; maximumAttempts?: number }) =>
    request<PlaybackFailoverState>("/playback/failovers", { method: "POST", body: JSON.stringify(input) }),
  playbackFailover: (failoverId: string) => request<PlaybackFailoverState>(`/playback/failovers/${encodeURIComponent(failoverId)}`),
  advancePlaybackFailover: (failoverId: string, input: { error: PlaybackFailoverError; positionSeconds: number; expectedRevision: number }) =>
    request<PlaybackFailoverState>(`/playback/failovers/${encodeURIComponent(failoverId)}/advance`, { method: "POST", body: JSON.stringify(input) }),
  cancelPlaybackFailover: (failoverId: string) => request<void>(`/playback/failovers/${encodeURIComponent(failoverId)}`, { method: "DELETE" }),
  stopPlayback: (sessionId: string) => request<void>(`/playback/sessions/${encodeURIComponent(sessionId)}`, { method: "DELETE", keepalive: true }),
  playbackHeartbeat: (input: { capabilities: PlaybackDevice["capabilities"]; state: PlaybackDevice["state"] }) => request<PlaybackDevice>("/playback/device", { method: "PUT", body: JSON.stringify(input) }),
  playbackDevices: () => request<{ devices: PlaybackDevice[] }>("/playback/devices"),
  sendPlaybackCommand: (sessionId: string, input: PlaybackCommandInput) => request<PlaybackCommand>(`/playback/devices/${encodeURIComponent(sessionId)}/commands`, { method: "POST", body: JSON.stringify(input) }),
  playbackCommands: (after?: string) => request<{ commands: PlaybackCommand[] }>(`/playback/commands${query({ after })}`),
  completePlaybackCommand: (operationId: string, status: Exclude<PlaybackCommand["status"], "pending">, code: PlaybackCommandResultCode) => request<PlaybackCommand>(`/playback/commands/incoming/${encodeURIComponent(operationId)}/result`, { method: "PUT", body: JSON.stringify({ status, code }) }),
  outgoingPlaybackCommand: (operationId: string) => request<PlaybackCommand>(`/playback/commands/outgoing/${encodeURIComponent(operationId)}`),
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
  mediaNotificationSubscriptions: () => request<MediaNotificationSubscriptions>("/media-notification-subscriptions"),
  followMediaNotifications: (titleId: string, input: MediaNotificationFollowInput) =>
    request<MediaNotificationSubscription>(`/media-notification-subscriptions/${encodeURIComponent(titleId)}`, { method: "PUT", body: JSON.stringify(input) }),
  unfollowMediaNotifications: (titleId: string) => request<void>(`/media-notification-subscriptions/${encodeURIComponent(titleId)}`, { method: "DELETE" }),
  mediaNotifications: (cursor = "", limit = 30) => request<MediaNotificationPage>(`/media-notifications${query({ cursor, limit })}`),
  acknowledgeMediaNotification: (notificationId: string, state: "read" | "dismissed") =>
    request<void>(`/media-notifications/${encodeURIComponent(notificationId)}/acknowledgement`, { method: "POST", body: JSON.stringify({ state }) }),
  readingQueue: (profileId: string, signal?: AbortSignal) =>
    request<ReadingQueue>(`/profiles/${encodeURIComponent(profileId)}/queue`, { signal }),
  addReadingQueueItem: (profileId: string, input: ReadingQueueAddInput) =>
    request<ReadingQueueMutation>(`/profiles/${encodeURIComponent(profileId)}/queue/items`, { method: "POST", body: JSON.stringify(input) }),
  updateReadingQueueItem: (profileId: string, itemId: string, input: ReadingQueueUpdateInput) =>
    request<ReadingQueueMutation>(`/profiles/${encodeURIComponent(profileId)}/queue/items/${encodeURIComponent(itemId)}`, { method: "PATCH", body: JSON.stringify(input) }),
  reorderReadingQueue: (profileId: string, input: ReadingQueueReorderInput) =>
    request<ReadingQueueMutation>(`/profiles/${encodeURIComponent(profileId)}/queue/order`, { method: "PUT", body: JSON.stringify(input) }),
  removeReadingQueueItem: (profileId: string, itemId: string, input: ReadingQueueMutationInput) =>
    request<ReadingQueueMutation>(`/profiles/${encodeURIComponent(profileId)}/queue/items/${encodeURIComponent(itemId)}`, { method: "DELETE", body: JSON.stringify(input) }),
  consumeReadingQueueItem: (profileId: string, itemId: string, input: ReadingQueueMutationInput) =>
    request<ReadingQueueMutation>(`/profiles/${encodeURIComponent(profileId)}/queue/items/${encodeURIComponent(itemId)}/consume`, { method: "POST", body: JSON.stringify(input) }),

  instanceSettings: () => request<SettingsLayer>("/settings"),
  maintenanceSettings: () => request<MaintenanceSettings>("/settings/maintenance"),
  updateMaintenanceSettings: (settings: MaintenanceSettings) => request<MaintenanceSettings>("/settings/maintenance", { method: "PUT", body: JSON.stringify(settings) }),
  profileSettings: (id: string) => request<SettingsLayer>(`/profiles/${id}/settings`),
  effectiveSettings: (id: string) => request<{ schemaVersion: number; settings: SettingsValues; sources: Record<string, string> }>(`/profiles/${id}/settings/effective`),
  updateInstanceSettings: (settings: SettingsValues) => request<SettingsLayer>("/settings", { method: "PATCH", body: JSON.stringify(settings) }),
  updateProfileSettings: (id: string, settings: SettingsValues) => request<SettingsLayer>(`/profiles/${id}/settings`, { method: "PATCH", body: JSON.stringify(settings) }),
  accessibilityPreferences: (profileId: string) => request<AccessibilityDocument>(`/profiles/${encodeURIComponent(profileId)}/accessibility-preferences`),
  updateAccessibilityPreferences: (profileId: string, input: AccessibilityUpdate) => request<AccessibilityDocument>(`/profiles/${encodeURIComponent(profileId)}/accessibility-preferences`, { method: "PUT", body: JSON.stringify(input) }),
  settingsIntegrations: () => request<SettingsIntegrations>("/settings/integrations"),
  updateSettingsIntegrations: (settings: SettingsIntegrationsPatch) => request<SettingsIntegrations>("/settings/integrations", { method: "PATCH", body: JSON.stringify(settings) }),
  settingsAudit: (cursor?: number, limit = 50) => request<ConfigurationAuditPage>(`/settings/audit${query({ cursor, limit })}`),
  jellyfinCredential: (profileId: string) =>
    request<JellyfinCredentialStatus>(`/profiles/${encodeURIComponent(profileId)}/jellyfin-credential`),
  createJellyfinCredential: (profileId: string) =>
    request<JellyfinCredentialSecret>(`/profiles/${encodeURIComponent(profileId)}/jellyfin-credential`, { method: "POST" }),
  rotateJellyfinCredential: (profileId: string) =>
    request<JellyfinCredentialSecret>(`/profiles/${encodeURIComponent(profileId)}/jellyfin-credential/rotate`, { method: "POST" }),
  revokeJellyfinCredential: (profileId: string) =>
    request<void>(`/profiles/${encodeURIComponent(profileId)}/jellyfin-credential`, { method: "DELETE" }),
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
