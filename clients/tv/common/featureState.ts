export interface PendingMutation {
  profileId: string;
  key: string;
  operationId: string;
  expectedRevision: number;
  createdAt: number;
}

const STORAGE_KEY = "rivune.tv.pending-mutations.v1";
const MAX_PENDING_MUTATIONS = 32;
const MAX_STATE_BYTES = 16_384;

function newOperationId(): string {
  if (globalThis.crypto?.randomUUID) return globalThis.crypto.randomUUID();
  const bytes = new Uint8Array(16);
  globalThis.crypto?.getRandomValues?.(bytes);
  bytes[6] = (bytes[6] & 0x0f) | 0x40;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

function validRecord(value: unknown): value is PendingMutation {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false;
  const record = value as Partial<PendingMutation>;
  return typeof record.profileId === "string" && record.profileId.length <= 64 &&
    typeof record.key === "string" && record.key.length <= 600 &&
    typeof record.operationId === "string" && /^[0-9a-f]{8}-[0-9a-f-]{27}$/i.test(record.operationId) &&
    Number.isSafeInteger(record.expectedRevision) && (record.expectedRevision ?? 0) >= 1 &&
    Number.isFinite(record.createdAt);
}

function readPending(): PendingMutation[] {
  try {
    const raw = globalThis.localStorage?.getItem(STORAGE_KEY);
    if (!raw || raw.length > MAX_STATE_BYTES) return [];
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed.filter(validRecord).slice(-MAX_PENDING_MUTATIONS);
  } catch {
    return [];
  }
}

function writePending(records: PendingMutation[]): void {
  try {
    globalThis.localStorage?.setItem(STORAGE_KEY, JSON.stringify(records.slice(-MAX_PENDING_MUTATIONS)));
  } catch {
    // The server remains authoritative when bounded local retry metadata cannot be stored.
  }
}

export class PendingMutationJournal {
  begin(profileId: string, key: string, expectedRevision: number): PendingMutation {
    const records = readPending();
    const existing = records.find((record) => record.profileId === profileId && record.key === key);
    if (existing) return existing;
    const record = { profileId, key, expectedRevision, operationId: newOperationId(), createdAt: Date.now() };
    writePending([...records, record]);
    return record;
  }

  complete(operationId: string): void {
    writePending(readPending().filter((record) => record.operationId !== operationId));
  }

  clearProfile(profileId: string): void {
    writePending(readPending().filter((record) => record.profileId !== profileId));
  }
}
