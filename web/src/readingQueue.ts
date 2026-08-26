import { useSyncExternalStore } from "react";
import { api, APIError } from "./api";
import type { MediaItem, ReadingQueue, ReadingQueueAddInput, ReadingQueueItem } from "./types";

type QueueError = "load" | "mutation" | null;
export type ReadingQueueState = {
  queue: ReadingQueue | null;
  loading: boolean;
  busyItemId: string;
  error: QueueError;
  conflict: boolean;
};

const emptyState: ReadingQueueState = { queue: null, loading: false, busyItemId: "", error: null, conflict: false };
const states = new Map<string, ReadingQueueState>();
const listeners = new Map<string, Set<() => void>>();

function state(profileId: string): ReadingQueueState {
  return states.get(profileId) ?? emptyState;
}

function publish(profileId: string, patch: Partial<ReadingQueueState>): void {
  states.set(profileId, { ...state(profileId), ...patch });
  for (const listener of listeners.get(profileId) ?? []) listener();
}

function subscribe(profileId: string, listener: () => void): () => void {
  const scoped = listeners.get(profileId) ?? new Set<() => void>();
  scoped.add(listener);
  listeners.set(profileId, scoped);
  return () => {
    scoped.delete(listener);
    if (scoped.size === 0) listeners.delete(profileId);
  };
}

export function useReadingQueue(profileId: string): ReadingQueueState {
  return useSyncExternalStore(
    (listener) => profileId ? subscribe(profileId, listener) : () => undefined,
    () => profileId ? state(profileId) : emptyState,
    () => emptyState,
  );
}

export async function refreshReadingQueue(profileId: string, preserveConflict = false): Promise<ReadingQueue> {
  if (!profileId) throw new Error("active profile required");
  publish(profileId, { loading: true, error: null, ...(!preserveConflict ? { conflict: false } : {}) });
  try {
    const queue = await api.readingQueue(profileId);
    publish(profileId, { queue, loading: false });
    return queue;
  } catch (cause) {
    publish(profileId, { loading: false, error: "load" });
    throw cause;
  }
}

async function currentQueue(profileId: string): Promise<ReadingQueue> {
  return state(profileId).queue ?? refreshReadingQueue(profileId);
}

async function mutate(profileId: string, itemId: string, action: (queue: ReadingQueue) => Promise<unknown>): Promise<ReadingQueue> {
  publish(profileId, { busyItemId: itemId, error: null, conflict: false });
  try {
    const queue = await currentQueue(profileId);
    await action(queue);
    return await refreshReadingQueue(profileId);
  } catch (cause) {
    const conflict = cause instanceof APIError && (cause.code === "reading_queue_conflict" || cause.code === "reading_queue_operation_conflict");
    publish(profileId, { busyItemId: "", error: conflict ? null : "mutation", conflict });
    if (conflict) await refreshReadingQueue(profileId, true).catch(() => undefined);
    throw cause;
  } finally {
    publish(profileId, { busyItemId: "" });
  }
}

export async function enqueueReadingQueue(profileId: string, item: Omit<ReadingQueueAddInput, "operationId" | "expectedRevision">): Promise<void> {
  await mutate(profileId, "new", async (queue) => {
    await api.addReadingQueueItem(profileId, { ...item, operationId: crypto.randomUUID(), expectedRevision: queue.revision });
  });
}

export async function removeReadingQueue(profileId: string, itemId: string): Promise<void> {
  await mutate(profileId, itemId, async (queue) => {
    await api.removeReadingQueueItem(profileId, itemId, { operationId: crypto.randomUUID(), expectedRevision: queue.revision });
  });
}

export async function consumeReadingQueue(profileId: string, itemId: string): Promise<void> {
  await mutate(profileId, itemId, async (queue) => {
    await api.consumeReadingQueueItem(profileId, itemId, { operationId: crypto.randomUUID(), expectedRevision: queue.revision });
  });
}

export async function moveReadingQueueItem(profileId: string, itemId: string, offset: -1 | 1): Promise<void> {
  await mutate(profileId, itemId, async (queue) => {
    const index = queue.items.findIndex((item) => item.id === itemId);
    const target = index + offset;
    if (index < 0 || target < 0 || target >= queue.items.length) return;
    const ids = queue.items.map((item) => item.id);
    [ids[index], ids[target]] = [ids[target]!, ids[index]!];
    await api.reorderReadingQueue(profileId, { operationId: crypto.randomUUID(), expectedRevision: queue.revision, itemIds: ids });
  });
}

export function readingQueueMedia(item: ReadingQueueItem): MediaItem {
  return {
    id: item.resourceId,
    resourceId: item.resourceId,
    titleId: item.titleId,
    mediaType: item.mediaType,
    sourceAddonId: item.sourceAddonId,
    title: item.title,
    posterUrl: item.posterUrl,
  };
}
