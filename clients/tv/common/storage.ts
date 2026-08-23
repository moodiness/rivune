import type { TokenPair } from "./types";

export interface StoredCredentials {
  issuer: string;
  tokens: TokenPair;
  profileContext: string | null;
}

export interface CredentialStore {
  load(issuer: string): Promise<StoredCredentials | null>;
  save(credentials: StoredCredentials): Promise<void>;
  clear(issuer: string): Promise<void>;
}

export interface KeyValueStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

const STORAGE_PREFIX = "rivune.tv.credentials.v1:";

function keyForIssuer(issuer: string): string {
  return `${STORAGE_PREFIX}${encodeURIComponent(issuer)}`;
}

function isTokenPair(value: unknown): value is TokenPair {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return false;
  const token = value as Partial<TokenPair>;
  const categoryValid = token.category === null ||
    (typeof token.category === "object" && token.category !== null && !Array.isArray(token.category));
  const scopeValid = token.authorizationScope === "global_admin" || token.authorizationScope === "category";
  const scopeCategoryConsistent = token.authorizationScope === "global_admin" ? token.category === null : token.category !== null;
  return token.tokenType === "Bearer" &&
    typeof token.accessToken === "string" && token.accessToken.length > 0 &&
    typeof token.accessTokenExpiresAt === "string" && Number.isFinite(Date.parse(token.accessTokenExpiresAt)) &&
    typeof token.refreshToken === "string" && token.refreshToken.length > 0 &&
    typeof token.refreshTokenExpiresAt === "string" && Number.isFinite(Date.parse(token.refreshTokenExpiresAt)) &&
    typeof token.sessionId === "string" && token.sessionId.length > 0 &&
    typeof token.deviceId === "string" && token.deviceId.length > 0 &&
    scopeValid && categoryValid && scopeCategoryConsistent;
}

function decodeStoredCredentials(raw: string, issuer: string): StoredCredentials | null {
  let value: unknown;
  try {
    value = JSON.parse(raw);
  } catch {
    return null;
  }
  if (typeof value !== "object" || value === null || Array.isArray(value)) return null;
  const persisted = value as { version?: unknown; issuer?: unknown; tokens?: unknown; profileContext?: unknown };
  if (persisted.version !== 1 || persisted.issuer !== issuer || !isTokenPair(persisted.tokens)) return null;
  if (persisted.profileContext !== null && typeof persisted.profileContext !== "string") return null;
  return {
    issuer,
    tokens: persisted.tokens,
    profileContext: persisted.profileContext as string | null,
  };
}

/**
 * Issuer-scoped browser persistence. A token rotation and its profile context
 * are committed in one setItem operation, so a crash cannot expose a mixed
 * access/refresh pair.
 */
export class LocalStorageCredentialStore implements CredentialStore {
  constructor(private readonly storage: KeyValueStorage) {}

  async load(issuer: string): Promise<StoredCredentials | null> {
    const key = keyForIssuer(issuer);
    const raw = this.storage.getItem(key);
    if (raw === null) return null;
    const credentials = decodeStoredCredentials(raw, issuer);
    if (credentials === null) this.storage.removeItem(key);
    return credentials;
  }

  async save(credentials: StoredCredentials): Promise<void> {
    const payload = JSON.stringify({
      version: 1,
      issuer: credentials.issuer,
      tokens: credentials.tokens,
      profileContext: credentials.profileContext,
    });
    this.storage.setItem(keyForIssuer(credentials.issuer), payload);
  }

  async clear(issuer: string): Promise<void> {
    this.storage.removeItem(keyForIssuer(issuer));
  }
}

export class MemoryCredentialStore implements CredentialStore {
  private readonly entries = new Map<string, StoredCredentials>();

  async load(issuer: string): Promise<StoredCredentials | null> {
    const value = this.entries.get(issuer);
    return value ? structuredCloneSafe(value) : null;
  }

  async save(credentials: StoredCredentials): Promise<void> {
    this.entries.set(credentials.issuer, structuredCloneSafe(credentials));
  }

  async clear(issuer: string): Promise<void> {
    this.entries.delete(issuer);
  }
}

function structuredCloneSafe<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}

export function defaultCredentialStore(): CredentialStore {
  try {
    return globalThis.localStorage
      ? new LocalStorageCredentialStore(globalThis.localStorage)
      : new MemoryCredentialStore();
  } catch {
    return new MemoryCredentialStore();
  }
}
