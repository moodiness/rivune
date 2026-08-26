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

const INSTALLATION_ID_KEY = "rivune.tv.installation.v1";
let volatileInstallationId: string | null = null;

function createInstallationId(): string {
  const bytes = new Uint8Array(16);
  if (globalThis.crypto?.getRandomValues) {
    globalThis.crypto.getRandomValues(bytes);
    bytes[6] = (bytes[6] & 0x0f) | 0x40;
    bytes[8] = (bytes[8] & 0x3f) | 0x80;
    const hex = Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join("");
    return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
  }
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}-${Math.random().toString(36).slice(2)}`;
}

export function installationId(): string {
  if (volatileInstallationId) return volatileInstallationId;
  try {
    const stored = globalThis.localStorage?.getItem(INSTALLATION_ID_KEY)?.trim();
    if (stored && stored.length <= 128) return (volatileInstallationId = stored);
    const generated = createInstallationId();
    globalThis.localStorage?.setItem(INSTALLATION_ID_KEY, generated);
    return (volatileInstallationId = generated);
  } catch {
    return (volatileInstallationId = createInstallationId());
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
    const storage = globalThis.localStorage;
    if (storage) {
      for (let index = storage.length - 1; index >= 0; index -= 1) {
        const key = storage.key(index);
        if (key?.startsWith("rivune.tv.credentials.") || key === "rivune.tv.quality.v1") storage.removeItem(key);
      }
    }
  } catch {
    // Storage cleanup is best effort; credentials still remain memory-only.
  }
  return new MemoryCredentialStore();
}
