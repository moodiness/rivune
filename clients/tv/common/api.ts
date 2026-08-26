import type { TvPlatform, TvPlaybackCapabilities } from "./platform";
import { defaultCredentialStore, type CredentialStore, type StoredCredentials } from "./storage";
import {
  RIVUNE_PROTOCOL_VERSION,
  type Account,
  type AddonCatalogDescriptor,
  type AddonResourceBatch,
  type CalendarEvent,
  type Collection,
  type ContinueWatchingPage,
  type DeviceAuthorization,
  type Discovery,
  type EffectiveSettings,
  type LibraryItem,
  type LibraryPage,
  type Movie,
  type PlaybackDecision,
  type PlaybackCommand,
  type PlaybackCommandInput,
  type PlaybackCommandList,
  type PlaybackCommandResultInput,
  type PlaybackDevice,
  type PlaybackDeviceHeartbeatInput,
  type PlaybackDeviceList,
  type PlaybackPreparation,
  type PlaybackProgress,
  type PlaybackSession,
  type PlaybackSourceList,
  type PollDeviceAuthorizationOptions,
  type Profile,
  type ProfileSelection,
  type ResolvedCollectionFolder,
  type ResolvePlaybackInput,
  type Season,
  type Series,
  type SeriesMappingProvider,
  type SemanticSearchPage,
  type SemanticSearchRequest,
  type TitleMediaType,
  type TitleReference,
  type TitleResolveInput,
  type TokenPair,
  type UpdatePlaybackProgressInput,
} from "./types";
import type {
  AccessibilityPreferencesDocument,
  AddonIncident,
  AddonIncidentDetail,
  AddonIncidentList,
  MediaNotificationFollowInput,
  MediaNotificationPage,
  MediaNotificationSubscription,
  MediaNotificationSubscriptions,
  PlaybackFailoverAdvanceInput,
  PlaybackFailoverCreateInput,
  PlaybackFailoverState,
  ReadingQueue,
  ReadingQueueAddInput,
  ReadingQueueMutation,
  ReadingQueueMutationInput,
  ReadingQueueReorderInput,
  ReadingQueueUpdateInput,
  SavedSearch,
  SavedSearchInput,
  SavedSearchList,
  SavedSearchUpdateInput,
  SmartCollection,
  SmartCollectionInput,
  SmartCollectionList,
  SmartCollectionPage,
  SmartCollectionUpdateInput,
  SmartRule,
} from "./types";

export * from "./types";
export type { CredentialStore, StoredCredentials } from "./storage";

const MAX_JSON_BYTES = 16 * 1024 * 1024;
const SAFE_CAPABILITY = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
const PROFILE_CONTEXT_HEADER = "X-Rivune-Profile-Context";
const PLATFORM_HEADER = "X-Rivune-TV-Platform";
const PLAYBACK_RESULT_STATUS: Record<string, true> = { applied: true, failed: true, expired: true };
const PLAYBACK_RESULT_CODE: Record<string, true> = { applied: true, unsupported: true, invalid_state: true, stale_target: true, expired: true, execution_failed: true };
const PLAYBACK_OUTCOME: Record<string, true> = { direct_supported: true, remux_required: true, audio_transcode_required: true, video_transcode_required: true, subtitle_burn_required: true };
const PLAYBACK_REASON: Record<string, true> = { container_not_supported: true, video_codec_not_supported: true, audio_codec_not_supported: true, resolution_limit: true, bitrate_limit: true, hdr_not_supported: true, subtitle_burn_required: true };

type Fetch = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;
type HttpMethod = "GET" | "POST" | "PUT" | "PATCH" | "DELETE";

export interface RivuneTvClientOptions {
  platform: TvPlatform;
  credentialStore?: CredentialStore;
  fetch?: Fetch;
}

interface RequestOptions {
  method?: HttpMethod;
  body?: unknown;
  authenticated?: boolean;
  profileContext?: boolean;
  retryAfterRefresh?: boolean;
  signal?: AbortSignal;
}

interface AuthenticationSnapshot {
  credentials: TokenPair;
  profileContext: string | null;
  epoch: number;
}

export class APIError extends Error {
  readonly status: number;
  readonly code: string;
  readonly retryAfterSeconds: number | null;

  constructor(status: number, code: string, message: string, retryAfterSeconds: number | null = null) {
    super(message);
    this.name = "APIError";
    this.status = status;
    this.code = code;
    this.retryAfterSeconds = retryAfterSeconds;
  }
}

function authorityContainsUserInfo(value: string): boolean {
  const match = /^[a-z][a-z\d+.-]*:\/\/([^/?#]*)/i.exec(value);
  return match?.[1].includes("@") ?? false;
}
function rawHttpHostAllowed(value: string): boolean {
  const authority = /^[a-z][a-z\d+.-]*:\/\/([^/?#]*)/i.exec(value)?.[1] ?? "";
  const rawHost = authority.startsWith("[")
    ? authority.slice(0, authority.indexOf("]") + 1)
    : authority.split(":", 1)[0];
  return rawHost.toLowerCase() === "localhost" ||
    /^\d{1,3}(?:\.\d{1,3}){3}$/.test(rawHost) ||
    /^\[[\da-f:.]+\]$/i.test(rawHost);
}

function privateHttpHost(hostname: string): boolean {
  const host = hostname.toLowerCase().replace(/^\[|\]$/g, "");
  if (host === "localhost" || host === "::1") return true;
  if (host.startsWith("fc") || host.startsWith("fd")) {
    const first = Number.parseInt(host.slice(0, 2), 16);
    if (Number.isInteger(first) && (first & 0xfe) === 0xfc && host.includes(":")) return true;
  }
  const parts = host.split(".");
  if (parts.length !== 4 || parts.some((part) => !/^\d{1,3}$/.test(part))) return false;
  const octets = parts.map(Number);
  if (octets.some((part) => part > 255)) return false;
  return octets[0] === 127 || octets[0] === 10 ||
    (octets[0] === 172 && octets[1] >= 16 && octets[1] <= 31) ||
    (octets[0] === 192 && octets[1] === 168);
}

function transportAllowed(url: URL): boolean {
  return url.username === "" && url.password === "" &&
    (url.protocol === "https:" || (url.protocol === "http:" && privateHttpHost(url.hostname)));
}

/** Normalizes a server entry and enforces the TV credential transport policy. */
export function normalizeServerUrl(value: string): string {
  const trimmed = value.trim();
  if (trimmed.length === 0 || /\s/.test(trimmed)) {
    throw new APIError(0, "invalid_server_url", "Enter a valid Rivune server URL.");
  }
  let candidate = trimmed;
  if (!/^[a-z][a-z\d+.-]*:\/\//i.test(candidate)) {
    let provisional: URL;
    try {
      provisional = new URL(`http://${candidate}`);
    } catch {
      throw new APIError(0, "invalid_server_url", "Enter a valid Rivune server URL.");
    }
    candidate = `${privateHttpHost(provisional.hostname) ? "http" : "https"}://${candidate}`;
  }
  let parsed: URL;
  try {
    parsed = new URL(candidate);
  } catch {
    throw new APIError(0, "invalid_server_url", "Enter a valid Rivune server URL.");
  }
  if (authorityContainsUserInfo(candidate) || !transportAllowed(parsed) || parsed.search || parsed.hash ||
      (parsed.protocol === "http:" && !rawHttpHostAllowed(candidate))) {
    throw new APIError(0, "invalid_server_url", "The Rivune server URL is not allowed.");
  }
  return parsed.origin;
}

function responseRetryAfter(headers: Headers): number | null {
  const value = headers.get("Retry-After")?.trim();
  if (!value) return null;
  if (/^\d+$/.test(value)) {
    const seconds = Number(value);
    return Number.isSafeInteger(seconds) ? seconds : null;
  }
  const at = Date.parse(value);
  return Number.isFinite(at) ? Math.max(0, Math.ceil((at - Date.now()) / 1000)) : null;
}

function responseObject(value: unknown, context: string): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new APIError(0, "invalid_response", `The Rivune server returned an invalid ${context} response.`);
  }
  return value as Record<string, unknown>;
}

function requiredString(value: unknown, field: string): string {
  if (typeof value !== "string" || value.length === 0) {
    throw new APIError(0, "invalid_response", `The Rivune server response has an invalid ${field}.`);
  }
  return value;
}

function requiredNumber(value: unknown, field: string): number {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    throw new APIError(0, "invalid_response", `The Rivune server response has an invalid ${field}.`);
  }
  return value;
}

function requiredBoolean(value: unknown, field: string): boolean {
  if (typeof value !== "boolean") {
    throw new APIError(0, "invalid_response", `The Rivune server response has an invalid ${field}.`);
  }
  return value;
}
function decodeTokenPair(value: unknown): TokenPair {
  const object = responseObject(value, "authentication");
  const scope = requiredString(object.authorizationScope, "authorizationScope");
  if (scope !== "global_admin" && scope !== "category") {
    throw new APIError(0, "invalid_response", "The Rivune server returned an invalid authorization scope.");
  }
  const tokenType = requiredString(object.tokenType, "tokenType");
  if (tokenType.toLowerCase() !== "bearer") {
    throw new APIError(0, "invalid_response", "The Rivune server returned an unsupported token type.");
  }
  const category = object.category ?? null;
  const categoryIsObject = typeof category === "object" && category !== null && !Array.isArray(category);
  if ((scope === "global_admin" && category !== null) || (scope === "category" && !categoryIsObject)) {
    throw new APIError(0, "invalid_response", "The Rivune server returned an inconsistent authorization category.");
  }
  const accessTokenExpiresAt = requiredString(object.accessTokenExpiresAt, "accessTokenExpiresAt");
  const refreshTokenExpiresAt = requiredString(object.refreshTokenExpiresAt, "refreshTokenExpiresAt");
  if (!Number.isFinite(Date.parse(accessTokenExpiresAt)) || !Number.isFinite(Date.parse(refreshTokenExpiresAt))) {
    throw new APIError(0, "invalid_response", "The Rivune server returned an invalid token expiry.");
  }
  return {
    tokenType: "Bearer",
    accessToken: requiredString(object.accessToken, "accessToken"),
    accessTokenExpiresAt,
    refreshToken: requiredString(object.refreshToken, "refreshToken"),
    refreshTokenExpiresAt,
    sessionId: requiredString(object.sessionId, "sessionId"),
    deviceId: requiredString(object.deviceId, "deviceId"),
    authorizationScope: scope,
    category: category as TokenPair["category"],
  };
}

function decodeObject<T>(value: unknown, context: string): T {
  return responseObject(value, context) as T;
}

function publicPlaybackDecision(value: unknown): PlaybackDecision | null {
  if (value === null || value === undefined) return null;
  const object = responseObject(value, "playback decision");
  const reason = requiredString(object.reason, "decision.reason");
  if (!PLAYBACK_OUTCOME[reason] || !Array.isArray(object.reasons) || object.reasons.length > 7) {
    throw new APIError(0, "invalid_response", "The playback decision outcome is invalid.");
  }
  const reasons: PlaybackDecision["reasons"] = [];
  for (const candidate of object.reasons) {
    if (typeof candidate !== "string" || !PLAYBACK_REASON[candidate] || reasons.includes(candidate as PlaybackDecision["reasons"][number])) {
      throw new APIError(0, "invalid_response", "The playback decision reasons are invalid.");
    }
    reasons.push(candidate as PlaybackDecision["reasons"][number]);
  }
  return {
    reason: reason as PlaybackDecision["reason"], reasons,
    videoAction: requiredString(object.videoAction, "decision.videoAction"),
    audioAction: requiredString(object.audioAction, "decision.audioAction"),
    subtitleAction: requiredString(object.subtitleAction, "decision.subtitleAction"),
    toneMapping: requiredBoolean(object.toneMapping, "decision.toneMapping"),
    ...(object.source && typeof object.source === "object" && !Array.isArray(object.source) ? { source: object.source as PlaybackDecision["source"] } : {}),
    ...(object.target && typeof object.target === "object" && !Array.isArray(object.target) ? { target: object.target as PlaybackDecision["target"] } : {}),
  };
}

function decodeEnvelopeArray<T>(value: unknown, field: string): T[] {
  const object = responseObject(value, field);
  if (!Array.isArray(object[field])) {
    throw new APIError(0, "invalid_response", `The Rivune server response has an invalid ${field} list.`);
  }
  return object[field] as T[];
}
async function readBoundedText(response: Response): Promise<string> {
  const declared = response.headers.get("Content-Length");
  if (declared && /^\d+$/.test(declared) && Number(declared) > MAX_JSON_BYTES) {
    throw new APIError(response.status, "response_too_large", "The Rivune server response exceeds the 16 MiB limit.");
  }
  if (!response.body || typeof response.body.getReader !== "function") {
    const text = await response.text();
    if (new TextEncoder().encode(text).byteLength > MAX_JSON_BYTES) {
      throw new APIError(response.status, "response_too_large", "The Rivune server response exceeds the 16 MiB limit.");
    }
    return text;
  }
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let total = 0;
  let text = "";
  try {
    while (true) {
      const result = await reader.read();
      if (result.done) break;
      total += result.value.byteLength;
      if (total > MAX_JSON_BYTES) {
        await reader.cancel();
        throw new APIError(response.status, "response_too_large", "The Rivune server response exceeds the 16 MiB limit.");
      }
      text += decoder.decode(result.value, { stream: true });
    }
    return text + decoder.decode();
  } finally {
    reader.releaseLock();
  }
}

function parseJson(text: string): unknown {
  try {
    return JSON.parse(text) as unknown;
  } catch {
    throw new APIError(0, "invalid_response", "The Rivune server returned invalid JSON.");
  }
}

function delay(seconds: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(signal.reason ?? new DOMException("Aborted", "AbortError"));
      return;
    }
    const timer = setTimeout(resolve, seconds * 1000);
    signal?.addEventListener("abort", () => {
      clearTimeout(timer);
      reject(signal.reason ?? new DOMException("Aborted", "AbortError"));
    }, { once: true });
  });
}

function validSmartRule(rule: SmartRule, depth = 0): boolean {
  if (!rule || typeof rule !== "object" || depth > 8) return false;
  if (rule.type === "all" || rule.type === "any") {
    return Array.isArray(rule.rules) && rule.rules.length >= 1 && rule.rules.length <= 16 && rule.rules.every((child) => validSmartRule(child, depth + 1));
  }
  if (rule.type === "media_type") {
    const allowed = new Set(["movie", "series", "season", "episode", "video", "tv"]);
    return rule.operator === "one_of" && Array.isArray(rule.values) && rule.values.length >= 1 && rule.values.length <= 6 && new Set(rule.values).size === rule.values.length && rule.values.every((value) => allowed.has(value));
  }
  if (rule.type === "year" || rule.type === "rating") {
    return ["equals", "gte", "lte"].includes(rule.operator) && Number.isFinite(rule.number) && rule.number >= 0 && rule.number <= 2100;
  }
  if (rule.type === "genre" || rule.type === "status" || rule.type === "source") {
    return ["equals", "not_equals"].includes(rule.operator) && typeof rule.value === "string" && rule.value.length >= 1 && rule.value.length <= 128;
  }
  return false;
}

function assertSmartRule(rule: SmartRule): void {
  if (!validSmartRule(rule)) throw new APIError(0, "invalid_smart_rule", "The smart collection rule is invalid.");
}

export class RivuneTvClient {
  readonly issuer: string;
  readonly platform: TvPlatform;

  private readonly fetcher: Fetch;
  private readonly store: CredentialStore;
  private apiBase: URL | null = null;
  private discoveryPromise: Promise<Discovery> | null = null;
  private credentials: TokenPair | null = null;
  private profileContext: string | null = null;
  private credentialsLoaded = false;
  private authenticationEpoch = 0;
  private refreshPromise: Promise<TokenPair> | null = null;
  private mutationTail: Promise<void> = Promise.resolve();

  constructor(serverUrl: string, platform: TvPlatform, options?: Omit<RivuneTvClientOptions, "platform">);
  constructor(serverUrl: string, options: RivuneTvClientOptions);
  constructor(
    serverUrl: string,
    platformOrOptions: TvPlatform | RivuneTvClientOptions,
    options: Omit<RivuneTvClientOptions, "platform"> = {},
  ) {
    this.issuer = normalizeServerUrl(serverUrl);
    const resolved = typeof platformOrOptions === "string"
      ? { ...options, platform: platformOrOptions }
      : platformOrOptions;
    this.platform = resolved.platform;
    this.store = resolved.credentialStore ?? defaultCredentialStore();
    const availableFetch = resolved.fetch ?? globalThis.fetch;
    if (!availableFetch) throw new APIError(0, "fetch_unavailable", "This TV does not provide Fetch.");
    this.fetcher = availableFetch.bind(globalThis);
  }

  async discover(): Promise<Discovery> {
    if (this.discoveryPromise) return this.discoveryPromise;
    const pending = this.performDiscovery();
    this.discoveryPromise = pending;
    try {
      return await pending;
    } catch (error) {
      if (this.discoveryPromise === pending) this.discoveryPromise = null;
      throw error;
    }
  }

  private async performDiscovery(): Promise<Discovery> {
    const value = await this.requestUrl(new URL("/.well-known/rivune", this.issuer), { profileContext: false });
    const object = responseObject(value, "discovery");
    const protocolVersion = requiredNumber(object.protocolVersion, "protocolVersion");
    if (protocolVersion !== RIVUNE_PROTOCOL_VERSION) {
      throw new APIError(0, "incompatible_protocol", `Rivune protocol ${protocolVersion} is incompatible; this client requires ${RIVUNE_PROTOCOL_VERSION}.`);
    }
    const capabilityValues = Array.isArray(object.capabilities) ? object.capabilities : [];
    const capabilities: string[] = [];
    for (const candidate of capabilityValues) {
      if (typeof candidate === "string" && candidate.length <= 64 && SAFE_CAPABILITY.test(candidate) && !capabilities.includes(candidate)) {
        capabilities.push(candidate);
        if (capabilities.length === 64) break;
      }
    }
    const apiBaseUrl = requiredString(object.apiBaseUrl, "apiBaseUrl");
    let apiBase: URL;
    try {
      apiBase = new URL(apiBaseUrl, this.issuer);
    } catch {
      throw new APIError(0, "invalid_server_url", "The Rivune API base URL is invalid.");
    }
    if (authorityContainsUserInfo(apiBaseUrl) || !transportAllowed(apiBase) ||
        apiBase.origin !== this.issuer || apiBase.search || apiBase.hash) {
      throw new APIError(0, "invalid_server_url", "The Rivune API base must use the selected server origin.");
    }
    apiBase.pathname = `${apiBase.pathname.replace(/\/+$/, "")}/`;
    this.apiBase = apiBase;
    return {
      name: requiredString(object.name, "name"),
      serverVersion: requiredString(object.serverVersion, "serverVersion"),
      protocolVersion: RIVUNE_PROTOCOL_VERSION,
      apiBaseUrl,
      setupRequired: requiredBoolean(object.setupRequired, "setupRequired"),
      ...(typeof object.setupCompleted === "boolean" ? { setupCompleted: object.setupCompleted } : {}),
      ...(typeof object.demoAvailable === "boolean" ? { demoAvailable: object.demoAvailable } : {}),
      timezone: requiredString(object.timezone, "timezone"),
      interfaceLanguage: requiredString(object.interfaceLanguage, "interfaceLanguage"),
      capabilities,
    };
  }

  async restoreSession(): Promise<boolean> {
    await this.loadCredentials();
    return this.credentials !== null;
  }

  async beginDeviceAuthorization(installationId: string, deviceName: string, platform: TvPlatform = this.platform): Promise<DeviceAuthorization> {
    const value = await this.request("auth/device-code", {
      method: "POST",
      body: { installationId, deviceName, platform },
      profileContext: false,
    });
    const authorization = decodeObject<DeviceAuthorization>(value, "device authorization");
    requiredString(authorization.deviceCode, "deviceCode");
    requiredString(authorization.userCode, "userCode");
    requiredString(authorization.expiresAt, "expiresAt");
    if (!Number.isInteger(authorization.intervalSeconds) || authorization.intervalSeconds < 1) {
      throw new APIError(0, "invalid_response", "The device authorization polling interval is invalid.");
    }
    if (!this.resolveMediaUrl(requiredString(authorization.verificationUri, "verificationUri")) ||
        !this.resolveMediaUrl(requiredString(authorization.verificationUriComplete, "verificationUriComplete"))) {
      throw new APIError(0, "invalid_response", "The device authorization verification URL is not allowed.");
    }
    return authorization;
  }

  async exchangeDeviceAuthorization(deviceCode: string): Promise<TokenPair> {
    const epoch = await this.replaceCredentials();
    const value = await this.request("auth/device-code/token", {
      method: "POST",
      body: { deviceCode },
      profileContext: false,
    });
    const tokens = decodeTokenPair(value);
    await this.commitCredentials(tokens, null, epoch);
    return tokens;
  }

  async pollDeviceAuthorization(
    authorization: DeviceAuthorization,
    options: PollDeviceAuthorizationOptions = {},
  ): Promise<TokenPair> {
    let interval = Math.max(1, authorization.intervalSeconds);
    const expiresAt = Date.parse(authorization.expiresAt);
    while (!Number.isFinite(expiresAt) || Date.now() < expiresAt) {
      await delay(interval, options.signal);
      try {
        return await this.exchangeDeviceAuthorization(authorization.deviceCode);
      } catch (error) {
        if (!(error instanceof APIError)) throw error;
        if (error.code === "authorization_pending") {
          interval = error.retryAfterSeconds ?? interval;
        } else if (error.code === "slow_down") {
          interval = error.retryAfterSeconds ?? interval + 5;
        } else {
          throw error;
        }
        options.onPending?.(interval);
      }
    }
    throw new APIError(400, "expired_device_code", "The device authorization code has expired.");
  }

  async refreshSession(): Promise<TokenPair> {
    const snapshot = await this.authenticationSnapshot();
    return this.refreshCredentials(snapshot.credentials.accessToken, snapshot);
  }

  async logout(): Promise<void> {
    await this.loadCredentials();
    const captured = await this.mutate(async () => {
      const current = this.credentials;
      this.authenticationEpoch += 1;
      this.credentials = null;
      this.profileContext = null;
      this.credentialsLoaded = true;
      await this.store.clear(this.issuer);
      return current;
    });
    if (captured) {
      await this.request("auth/logout", {
        method: "POST",
        profileContext: false,
        retryAfterRefresh: false,
      }, captured.accessToken);
    }
  }

  async currentAccount(): Promise<Account> {
    return decodeObject<Account>(await this.request("auth/me", { authenticated: true, profileContext: false }), "account");
  }

  async profiles(): Promise<Profile[]> {
    return decodeEnvelopeArray<Profile>(await this.request("profiles", { authenticated: true, profileContext: false }), "profiles");
  }

  async selectProfile(id: string, pin?: string): Promise<ProfileSelection> {
    const requestedWith = await this.authenticationSnapshot();
    const value = await this.request(`profiles/${encodeURIComponent(id)}/select`, {
      method: "POST",
      body: pin === undefined ? {} : { pin },
      authenticated: true,
      profileContext: false,
    });
    const selection = decodeObject<ProfileSelection>(value, "profile selection");
    requiredString(selection.profileContext, "profileContext");
    const current = await this.authenticationSnapshot();
    if (current.epoch !== requestedWith.epoch) {
      throw new APIError(0, "authentication_changed", "Authentication state changed while selecting a profile.");
    }
    await this.commitCredentials(current.credentials, selection.profileContext, current.epoch);
    return selection;
  }

  async clearProfileSelection(): Promise<void> {
    const requestedWith = await this.authenticationSnapshot();
    await this.request("profiles/selection", {
      method: "DELETE",
      authenticated: true,
      profileContext: false,
    });
    const current = await this.authenticationSnapshot();
    if (current.epoch !== requestedWith.epoch) {
      throw new APIError(0, "authentication_changed", "Authentication state changed while clearing the profile.");
    }
    await this.commitCredentials(current.credentials, null, current.epoch);
  }

  async effectiveProfileSettings(id: string): Promise<EffectiveSettings> {
    return decodeObject<EffectiveSettings>(await this.request(`profiles/${encodeURIComponent(id)}/settings/effective`, { authenticated: true }), "profile settings");
  }

  async collections(): Promise<Collection[]> {
    return decodeEnvelopeArray<Collection>(await this.request("collections", { authenticated: true }), "collections");
  }

  async resolveCollectionFolder(
    collectionId: string,
    folderId: string,
    page?: number,
    limit?: number,
    language?: string,
    region?: string,
  ): Promise<ResolvedCollectionFolder> {
    const query = new URLSearchParams();
    if (page !== undefined) query.set("page", String(page));
    if (limit !== undefined) query.set("limit", String(limit));
    if (language) query.set("language", language);
    if (region) query.set("region", region);
    const encodedQuery = query.toString();
    return decodeObject<ResolvedCollectionFolder>(await this.request(
      `collections/${encodeURIComponent(collectionId)}/folders/${encodeURIComponent(folderId)}/items${encodedQuery ? `?${encodedQuery}` : ""}`,
      { authenticated: true },
    ), "collection folder");
  }

  async continueWatching(limit?: number): Promise<ContinueWatchingPage> {
    const suffix = limit === undefined ? "" : `?limit=${encodeURIComponent(String(limit))}`;
    return decodeObject<ContinueWatchingPage>(await this.request(`continue-watching${suffix}`, { authenticated: true }), "continue watching");
  }

  async addonCatalogs(): Promise<AddonCatalogDescriptor[]> {
    return decodeEnvelopeArray<AddonCatalogDescriptor>(await this.request("addons/catalogs", { authenticated: true }), "catalogs");
  }
  async semanticSearch(input: SemanticSearchRequest, signal?: AbortSignal): Promise<SemanticSearchPage> {
    return decodeObject<SemanticSearchPage>(await this.request("search/semantic", {
      method: "POST",
      body: input,
      authenticated: true,
      signal,
    }), "semantic search");
  }


  async searchAddonCatalogs(
    type: string,
    query: string,
    skip?: number,
    limit?: number,
    language?: string,
    extras: ReadonlyArray<readonly [string, string]> = [],
    signal?: AbortSignal,
  ): Promise<AddonResourceBatch> {
    const parameters = new URLSearchParams({ search: query });
    if (skip !== undefined) parameters.append("skip", String(skip));
    if (limit !== undefined) parameters.append("limit", String(limit));
    if (language) parameters.append("language", language);
    for (const [name, value] of extras) parameters.append(name, value);
    return decodeObject<AddonResourceBatch>(await this.request(
      `addons/catalogs/search/${encodeURIComponent(type)}?${parameters}`,
      { authenticated: true, signal },
    ), "catalog search");
  }

  async library(mediaType?: TitleMediaType, page?: number, pageSize?: number): Promise<LibraryPage> {
    const query = new URLSearchParams();
    if (mediaType) query.set("mediaType", mediaType);
    if (page !== undefined) query.set("page", String(page));
    if (pageSize !== undefined) query.set("pageSize", String(pageSize));
    const encodedQuery = query.toString();
    return decodeObject<LibraryPage>(await this.request(`library${encodedQuery ? `?${encodedQuery}` : ""}`, { authenticated: true }), "library");
  }

  async addLibraryTitle(titleId: string): Promise<LibraryItem> {
    return decodeObject<LibraryItem>(await this.request(`library/${encodeURIComponent(titleId)}`, {
      method: "PUT",
      authenticated: true,
    }), "library item");
  }

  async removeLibraryTitle(titleId: string): Promise<void> {
    await this.request(`library/${encodeURIComponent(titleId)}`, {
      method: "DELETE",
      authenticated: true,
    });
  }

  async calendar(from: string, to: string, language?: string): Promise<CalendarEvent[]> {
    const query = new URLSearchParams({ from, to });
    if (language) query.set("language", language);
    return decodeEnvelopeArray<CalendarEvent>(await this.request(`calendar?${query}`, { authenticated: true }), "events");
  }

  async resolveTitle(input: TitleResolveInput): Promise<TitleReference> {
    return decodeObject<TitleReference>(await this.request("titles/resolve", {
      method: "POST", body: input, authenticated: true,
    }), "title");
  }

  async movie(id: string, language?: string): Promise<Movie> {
    const suffix = language ? `?language=${encodeURIComponent(language)}` : "";
    return decodeObject<Movie>(await this.request(`metadata/titles/${encodeURIComponent(id)}${suffix}`, { authenticated: true }), "movie");
  }

  async series(
    id: string,
    mappingProvider: SeriesMappingProvider = "tmdb",
    language?: string,
    episodeOrder?: string,
  ): Promise<Series> {
    const query = new URLSearchParams({ mappingProvider });
    if (language) query.set("language", language);
    if (episodeOrder) query.set("episodeOrder", episodeOrder);
    return decodeObject<Series>(await this.request(`metadata/series/${encodeURIComponent(id)}?${query}`, { authenticated: true }), "series");
  }

  async season(id: string, mappingProvider: SeriesMappingProvider = "tmdb", language?: string): Promise<Season> {
    const query = new URLSearchParams({ mappingProvider });
    if (language) query.set("language", language);
    return decodeObject<Season>(await this.request(`metadata/seasons/${encodeURIComponent(id)}?${query}`, { authenticated: true }), "season");
  }

  async playbackProgress(titleId: string): Promise<PlaybackProgress | null> {
    const value = await this.request(`progress/${encodeURIComponent(titleId)}`, { authenticated: true });
    return value === null ? null : decodeObject<PlaybackProgress>(value, "playback progress");
  }

  async updatePlaybackProgress(titleId: string, input: UpdatePlaybackProgressInput): Promise<PlaybackProgress> {
    return decodeObject<PlaybackProgress>(await this.request(`progress/${encodeURIComponent(titleId)}`, {
      method: "PUT", body: input, authenticated: true,
    }), "playback progress");
  }

  async playbackSources(
    mediaType: string,
    resourceId: string,
    capabilities: TvPlaybackCapabilities,
    addonId?: string,
  ): Promise<PlaybackSourceList> {
    return decodeObject<PlaybackSourceList>(await this.request("playback/sources", {
      method: "POST",
      body: { mediaType, resourceId, capabilities, ...(addonId ? { addonId } : {}) },
      authenticated: true,
    }), "playback sources");
  }

  async preparePlayback(
    sourceRef: string,
    startSeconds?: number,
    externalPlayer = false,
  ): Promise<PlaybackPreparation> {
    const preparation = decodeObject<PlaybackPreparation>(await this.request("playback/prepare", {
      method: "POST",
      body: {
        sourceRef,
        ...(startSeconds !== undefined ? { startSeconds } : {}),
        ...(externalPlayer ? { externalPlayer: true } : {}),
      },
      authenticated: true,
    }), "playback preparation");
    preparation.decision = publicPlaybackDecision(preparation.decision);
    return preparation;
  }

  async resolvePlayback(sourceRef: string, input: ResolvePlaybackInput = {}): Promise<PlaybackSession> {
    const session = decodeObject<PlaybackSession>(await this.request("playback/resolve", {
      method: "POST",
      body: { sourceRef, ...input },
      authenticated: true,
    }), "playback session");
    session.sources ??= [];
    session.subtitles ??= [];
    session.providerErrors ??= [];
    for (const source of session.sources) {
      source.decision = publicPlaybackDecision(source.decision);
      if (source.url && !this.resolveMediaUrl(source.url)) {
        throw new APIError(0, "invalid_response", "The playback source URL is not allowed.");
      }
    }
    for (const subtitle of session.subtitles) {
      if (subtitle.url && !this.resolveMediaUrl(subtitle.url)) {
        throw new APIError(0, "invalid_response", "The playback subtitle URL is not allowed.");
      }
    }
    return session;
  }

  async stopPlayback(sessionId: string): Promise<void> {
    await this.request(`playback/sessions/${encodeURIComponent(sessionId)}`, { method: "DELETE", authenticated: true });
  }

  async updatePlaybackDevice(input: PlaybackDeviceHeartbeatInput): Promise<PlaybackDevice> {
    return decodeObject<PlaybackDevice>(await this.request("playback/device", { method: "PUT", body: input, authenticated: true }), "playback device");
  }

  async playbackDevices(): Promise<PlaybackDeviceList> {
    const result = decodeObject<PlaybackDeviceList>(await this.request("playback/devices", { authenticated: true }), "playback devices");
    result.devices ??= [];
    return result;
  }

  async sendPlaybackCommand(sessionId: string, input: PlaybackCommandInput): Promise<PlaybackCommand> {
    return decodeObject<PlaybackCommand>(await this.request(`playback/devices/${encodeURIComponent(sessionId)}/commands`, {
      method: "POST", body: input, authenticated: true,
    }), "playback command");
  }

  async playbackCommands(after?: string): Promise<PlaybackCommandList> {
    const suffix = after ? `?after=${encodeURIComponent(after)}` : "";
    const result = decodeObject<PlaybackCommandList>(await this.request(`playback/commands${suffix}`, { authenticated: true }), "playback commands");
    result.commands ??= [];
    return result;
  }
  async reportPlaybackCommandResult(operationId: string, input: PlaybackCommandResultInput): Promise<PlaybackCommand> {
    if (!PLAYBACK_RESULT_STATUS[input.status] || !PLAYBACK_RESULT_CODE[input.code]) {
      throw new APIError(0, "invalid_playback_result", "The playback command result is invalid.");
    }
    return decodeObject<PlaybackCommand>(await this.request(`playback/commands/incoming/${encodeURIComponent(operationId)}/result`, {
      method: "PUT", body: input, authenticated: true,
    }), "playback command result");
  }

  async outgoingPlaybackCommand(operationId: string): Promise<PlaybackCommand> {
    return decodeObject<PlaybackCommand>(await this.request(`playback/commands/outgoing/${encodeURIComponent(operationId)}`, {
      authenticated: true,
    }), "outgoing playback command");
  }

  async readingQueue(profileId: string): Promise<ReadingQueue> {
    return decodeObject<ReadingQueue>(await this.request(`profiles/${encodeURIComponent(profileId)}/queue`, { authenticated: true }), "reading queue");
  }

  async addReadingQueueItem(profileId: string, input: ReadingQueueAddInput): Promise<ReadingQueueMutation> {
    return decodeObject<ReadingQueueMutation>(await this.request(`profiles/${encodeURIComponent(profileId)}/queue/items`, {
      method: "POST", body: input, authenticated: true,
    }), "reading queue mutation");
  }

  async reorderReadingQueue(profileId: string, input: ReadingQueueReorderInput): Promise<ReadingQueueMutation> {
    return decodeObject<ReadingQueueMutation>(await this.request(`profiles/${encodeURIComponent(profileId)}/queue/order`, {
      method: "PUT", body: input, authenticated: true,
    }), "reading queue mutation");
  }

  async updateReadingQueueItem(profileId: string, itemId: string, input: ReadingQueueUpdateInput): Promise<ReadingQueueMutation> {
    return decodeObject<ReadingQueueMutation>(await this.request(`profiles/${encodeURIComponent(profileId)}/queue/items/${encodeURIComponent(itemId)}`, {
      method: "PATCH", body: input, authenticated: true,
    }), "reading queue mutation");
  }

  async removeReadingQueueItem(profileId: string, itemId: string, input: ReadingQueueMutationInput): Promise<ReadingQueueMutation> {
    return decodeObject<ReadingQueueMutation>(await this.request(`profiles/${encodeURIComponent(profileId)}/queue/items/${encodeURIComponent(itemId)}`, {
      method: "DELETE", body: input, authenticated: true,
    }), "reading queue mutation");
  }

  async consumeReadingQueueItem(profileId: string, itemId: string, input: ReadingQueueMutationInput): Promise<ReadingQueueMutation> {
    return decodeObject<ReadingQueueMutation>(await this.request(`profiles/${encodeURIComponent(profileId)}/queue/items/${encodeURIComponent(itemId)}/consume`, {
      method: "POST", body: input, authenticated: true,
    }), "reading queue mutation");
  }

  async savedSearches(): Promise<SavedSearchList> {
    return decodeObject<SavedSearchList>(await this.request("saved-searches", { authenticated: true }), "saved searches");
  }

  async createSavedSearch(input: SavedSearchInput): Promise<SavedSearch> {
    return decodeObject<SavedSearch>(await this.request("saved-searches", { method: "POST", body: input, authenticated: true }), "saved search");
  }

  async updateSavedSearch(id: string, input: SavedSearchUpdateInput): Promise<SavedSearch> {
    return decodeObject<SavedSearch>(await this.request(`saved-searches/${encodeURIComponent(id)}`, { method: "PUT", body: input, authenticated: true }), "saved search");
  }

  async deleteSavedSearch(id: string, expectedRevision: number): Promise<void> {
    await this.request(`saved-searches/${encodeURIComponent(id)}?expectedRevision=${encodeURIComponent(String(expectedRevision))}`, { method: "DELETE", authenticated: true });
  }

  async smartCollections(): Promise<SmartCollectionList> {
    return decodeObject<SmartCollectionList>(await this.request("smart-collections", { authenticated: true }), "smart collections");
  }

  async createSmartCollection(input: SmartCollectionInput): Promise<SmartCollection> {
    assertSmartRule(input.rules);
    return decodeObject<SmartCollection>(await this.request("smart-collections", { method: "POST", body: input, authenticated: true }), "smart collection");
  }

  async updateSmartCollection(id: string, input: SmartCollectionUpdateInput): Promise<SmartCollection> {
    assertSmartRule(input.rules);
    return decodeObject<SmartCollection>(await this.request(`smart-collections/${encodeURIComponent(id)}`, { method: "PUT", body: input, authenticated: true }), "smart collection");
  }

  async deleteSmartCollection(id: string, expectedRevision: number): Promise<void> {
    await this.request(`smart-collections/${encodeURIComponent(id)}?expectedRevision=${encodeURIComponent(String(expectedRevision))}`, { method: "DELETE", authenticated: true });
  }

  async evaluateSmartCollection(id: string, page = 1, pageSize = 30): Promise<SmartCollectionPage> {
    const query = new URLSearchParams({ page: String(page), pageSize: String(pageSize) });
    return decodeObject<SmartCollectionPage>(await this.request(`smart-collections/${encodeURIComponent(id)}/items?${query}`, { authenticated: true }), "smart collection items");
  }

  async extensionIncidents(): Promise<AddonIncidentList> {
    return decodeObject<AddonIncidentList>(await this.request("operations/extension-incidents", { authenticated: true }), "extension incidents");
  }

  async extensionIncident(id: string): Promise<AddonIncidentDetail> {
    return decodeObject<AddonIncidentDetail>(await this.request(`operations/extension-incidents/${encodeURIComponent(id)}`, { authenticated: true }), "extension incident");
  }

  async acknowledgeExtensionIncident(id: string): Promise<AddonIncident> {
    return decodeObject<AddonIncident>(await this.request(`operations/extension-incidents/${encodeURIComponent(id)}/acknowledgement`, { method: "POST", authenticated: true }), "extension incident");
  }

  async mediaNotificationSubscriptions(): Promise<MediaNotificationSubscriptions> {
    return decodeObject<MediaNotificationSubscriptions>(await this.request("media-notification-subscriptions", { authenticated: true }), "media notification subscriptions");
  }

  async followMediaNotifications(titleId: string, input: MediaNotificationFollowInput): Promise<MediaNotificationSubscription> {
    return decodeObject<MediaNotificationSubscription>(await this.request(`media-notification-subscriptions/${encodeURIComponent(titleId)}`, {
      method: "PUT", body: input, authenticated: true,
    }), "media notification subscription");
  }

  async unfollowMediaNotifications(titleId: string): Promise<void> {
    await this.request(`media-notification-subscriptions/${encodeURIComponent(titleId)}`, { method: "DELETE", authenticated: true });
  }

  async mediaNotifications(cursor?: string, limit = 30): Promise<MediaNotificationPage> {
    const query = new URLSearchParams({ limit: String(limit) });
    if (cursor) query.set("cursor", cursor);
    return decodeObject<MediaNotificationPage>(await this.request(`media-notifications?${query}`, { authenticated: true }), "media notifications");
  }

  async acknowledgeMediaNotification(id: string, state: "read" | "dismissed"): Promise<void> {
    await this.request(`media-notifications/${encodeURIComponent(id)}/acknowledgement`, { method: "POST", body: { state }, authenticated: true });
  }

  async createPlaybackFailover(input: PlaybackFailoverCreateInput): Promise<PlaybackFailoverState> {
    return decodeObject<PlaybackFailoverState>(await this.request("playback/failovers", { method: "POST", body: input, authenticated: true }), "playback failover");
  }

  async playbackFailover(id: string): Promise<PlaybackFailoverState> {
    return decodeObject<PlaybackFailoverState>(await this.request(`playback/failovers/${encodeURIComponent(id)}`, { authenticated: true }), "playback failover");
  }

  async cancelPlaybackFailover(id: string): Promise<void> {
    await this.request(`playback/failovers/${encodeURIComponent(id)}`, { method: "DELETE", authenticated: true });
  }

  async advancePlaybackFailover(id: string, input: PlaybackFailoverAdvanceInput): Promise<PlaybackFailoverState> {
    return decodeObject<PlaybackFailoverState>(await this.request(`playback/failovers/${encodeURIComponent(id)}/advance`, {
      method: "POST", body: input, authenticated: true,
    }), "playback failover");
  }

  async accessibilityPreferences(profileId: string): Promise<AccessibilityPreferencesDocument> {
    return decodeObject<AccessibilityPreferencesDocument>(await this.request(`profiles/${encodeURIComponent(profileId)}/accessibility-preferences`, { authenticated: true }), "accessibility preferences");
  }

  async updateAccessibilityPreferences(profileId: string, input: AccessibilityPreferencesDocument): Promise<AccessibilityPreferencesDocument> {
    return decodeObject<AccessibilityPreferencesDocument>(await this.request(`profiles/${encodeURIComponent(profileId)}/accessibility-preferences`, {
      method: "PUT", body: input, authenticated: true,
    }), "accessibility preferences");
  }

  resolveMediaUrl(value: string): string | null {
    if (!value.trim() || value.startsWith("//") || authorityContainsUserInfo(value)) return null;
    let resolved: URL;
    try {
      resolved = new URL(value, this.issuer);
    } catch {
      return null;
    }
    if (!transportAllowed(resolved)) return null;
    if (resolved.origin !== this.issuer && (resolved.username || resolved.password || resolved.search || resolved.hash)) return null;
    return resolved.href;
  }

  resolveResourceUrl(value: string): string | null {
    return this.resolveMediaUrl(value);
  }

  resolveArtworkUrl(value: string): string | null {
    if (!value.trim() || value.startsWith("//") || authorityContainsUserInfo(value)) return null;
    let resolved: URL;
    try {
      resolved = new URL(value, this.issuer);
    } catch {
      return null;
    }
    return transportAllowed(resolved) && resolved.origin === this.issuer ? resolved.href : null;
  }

  private async request(
    path: string,
    options: RequestOptions,
    explicitAccessToken?: string,
  ): Promise<unknown> {
    if (!this.apiBase) await this.discover();
    const base = this.apiBase;
    if (options.signal?.aborted) {
      throw options.signal.reason ?? new DOMException("Aborted", "AbortError");
    }
    if (!base) throw new APIError(0, "invalid_response", "The Rivune API base is unavailable.");
    return this.requestUrl(new URL(path, base), options, explicitAccessToken);
  }

  private async requestUrl(
    url: URL,
    options: RequestOptions,
    explicitAccessToken?: string,
    expectedAuthentication?: AuthenticationSnapshot,
  ): Promise<unknown> {
    if (!transportAllowed(url) || url.origin !== this.issuer) {
      throw new APIError(0, "invalid_server_url", "A Rivune API request attempted to leave the selected server origin.");
    }
    const authenticated = options.authenticated ?? false;
    const snapshot = expectedAuthentication ?? (authenticated ? await this.authenticationSnapshot() : null);
    const accessToken = explicitAccessToken ?? snapshot?.credentials.accessToken;
    const method = options.method ?? "GET";
    const headers = new Headers({ Accept: "application/json", [PLATFORM_HEADER]: this.platform });
    if (accessToken) headers.set("Authorization", `Bearer ${accessToken}`);
    if (snapshot?.profileContext && options.profileContext !== false && this.usesProfileContext(url, method)) {
      headers.set(PROFILE_CONTEXT_HEADER, snapshot.profileContext);
    }
    let body: string | undefined;
    if (options.body !== undefined) {
      body = JSON.stringify(options.body);
      if (new TextEncoder().encode(body).byteLength > MAX_JSON_BYTES) {
        throw new APIError(0, "request_too_large", "The Rivune request exceeds the 16 MiB limit.");
      }
      headers.set("Content-Type", "application/json; charset=utf-8");
    }
    const response = await this.fetcher(url, {
      method,
      headers,
      body,
      credentials: "omit",
      redirect: "manual",
      cache: "no-store",
      signal: options.signal,
    });
    if (response.redirected) {
      throw new APIError(response.status, "redirect_not_allowed", "Rivune API redirects are not allowed.", responseRetryAfter(response.headers));
    }
    if (response.url) {
      let finalUrl: URL;
      try {
        finalUrl = new URL(response.url);
      } catch {
        throw new APIError(0, "invalid_response", "The Rivune response URL is invalid.");
      }
      if (finalUrl.origin !== this.issuer || !transportAllowed(finalUrl)) {
        throw new APIError(0, "redirect_not_allowed", "Rivune API redirects are not allowed.");
      }
    }
    if (response.status === 0 || (response.status >= 300 && response.status <= 399)) {
      throw new APIError(response.status, "redirect_not_allowed", "Rivune API redirects are not allowed.", responseRetryAfter(response.headers));
    }
    if (options.signal?.aborted) {
      throw options.signal.reason ?? new DOMException("Aborted", "AbortError");
    }
    const text = await readBoundedText(response);
    if (options.signal?.aborted) {
      throw options.signal.reason ?? new DOMException("Aborted", "AbortError");
    }
    if (snapshot && snapshot.epoch !== this.authenticationEpoch) {
      throw new APIError(0, "authentication_changed", "Authentication state changed while the request was running.");
    }
    if (response.status === 401 && snapshot && options.retryAfterRefresh !== false) {
      const refreshed = await this.refreshCredentials(accessToken ?? snapshot.credentials.accessToken, snapshot);
      return this.requestUrl(url, { ...options, retryAfterRefresh: false }, undefined, {
        credentials: refreshed,
        profileContext: snapshot.profileContext,
        epoch: snapshot.epoch,
      });
    }
    const value = text.length === 0 ? null : parseJson(text);
    if (!response.ok) throw this.decodeError(response, value);
    return value;
  }

  private decodeError(response: Response, value: unknown): APIError {
    let code = `http_${response.status}`;
    let message = `Rivune server returned HTTP ${response.status}.`;
    if (typeof value === "object" && value !== null && !Array.isArray(value)) {
      const envelope = value as { error?: unknown };
      if (typeof envelope.error === "object" && envelope.error !== null && !Array.isArray(envelope.error)) {
        const error = envelope.error as { code?: unknown; message?: unknown };
        if (typeof error.code === "string" && error.code) code = error.code;
        if (typeof error.message === "string" && error.message) message = error.message;
      }
    }
    return new APIError(response.status, code, message, responseRetryAfter(response.headers));
  }

  private async loadCredentials(): Promise<void> {
    if (this.credentialsLoaded) return;
    const epoch = this.authenticationEpoch;
    const stored = await this.store.load(this.issuer);
    if (epoch !== this.authenticationEpoch || this.credentialsLoaded) return;
    if (stored && stored.issuer !== this.issuer) {
      await this.store.clear(this.issuer);
    } else {
      this.credentials = stored?.tokens ?? null;
      this.profileContext = stored?.profileContext ?? null;
    }
    this.credentialsLoaded = true;
  }

  private async authenticationSnapshot(): Promise<AuthenticationSnapshot> {
    await this.loadCredentials();
    if (!this.credentials) throw new APIError(401, "not_authenticated", "Authentication is required.");
    return { credentials: this.credentials, profileContext: this.profileContext, epoch: this.authenticationEpoch };
  }

  private async replaceCredentials(): Promise<number> {
    return this.mutate(async () => {
      this.authenticationEpoch += 1;
      this.credentials = null;
      this.profileContext = null;
      this.credentialsLoaded = true;
      await this.store.clear(this.issuer);
      return this.authenticationEpoch;
    });
  }

  private async commitCredentials(tokens: TokenPair, profileContext: string | null, epoch: number): Promise<void> {
    await this.mutate(async () => {
      if (epoch !== this.authenticationEpoch) {
        throw new APIError(0, "authentication_changed", "Authentication state changed while credentials were being saved.");
      }
      const stored: StoredCredentials = { issuer: this.issuer, tokens, profileContext };
      await this.store.save(stored);
      if (epoch !== this.authenticationEpoch) {
        await this.store.clear(this.issuer);
        throw new APIError(0, "authentication_changed", "Authentication state changed while credentials were being saved.");
      }
      this.credentials = tokens;
      this.profileContext = profileContext;
      this.credentialsLoaded = true;
    });
  }

  private async refreshCredentials(failedAccessToken: string, snapshot: AuthenticationSnapshot): Promise<TokenPair> {
    if (this.credentials?.accessToken !== failedAccessToken) {
      if (!this.credentials) throw new APIError(401, "not_authenticated", "Authentication is required.");
      return this.credentials;
    }
    if (this.refreshPromise) return this.refreshPromise;
    const pending = (async () => {
      try {
        const value = await this.request("auth/refresh", {
          method: "POST",
          body: { refreshToken: snapshot.credentials.refreshToken },
          profileContext: false,
          retryAfterRefresh: false,
        });
        const tokens = decodeTokenPair(value);
        await this.commitCredentials(tokens, snapshot.profileContext, snapshot.epoch);
        return tokens;
      } catch (error) {
        await this.mutate(async () => {
          if (snapshot.epoch === this.authenticationEpoch && this.credentials?.refreshToken === snapshot.credentials.refreshToken) {
            this.authenticationEpoch += 1;
            this.credentials = null;
            this.profileContext = null;
            this.credentialsLoaded = true;
            await this.store.clear(this.issuer);
          }
        });
        throw error;
      }
    })();
    this.refreshPromise = pending;
    try {
      return await pending;
    } finally {
      if (this.refreshPromise === pending) this.refreshPromise = null;
    }
  }

  private usesProfileContext(url: URL, method: HttpMethod): boolean {
    const path = url.pathname;
    if (path.endsWith("/auth/logout") || path.endsWith("/auth/me")) return false;
    if (method === "GET" && path.endsWith("/profiles")) return false;
    if (method === "GET" && path.includes("/profiles/") && path.endsWith("/avatar")) return false;
    if (method === "DELETE" && path.endsWith("/profiles/selection")) return false;
    if (method === "POST" && path.includes("/profiles/") && path.endsWith("/select")) return false;
    return true;
  }

  private mutate<T>(operation: () => Promise<T>): Promise<T> {
    const pending = this.mutationTail.then(operation, operation);
    this.mutationTail = pending.then(() => undefined, () => undefined);
    return pending;
  }
}
