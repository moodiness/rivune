import type { RivuneTvClient } from "./api";
import { mediaFromCollection, mediaFromResourceBatch } from "./media";
import type { MediaItem, SemanticSearchIntent, SemanticSearchPage } from "./types";

export const TV_SEARCH_TYPES: readonly string[] = ["movie", "series", "tv"];
export const SEMANTIC_SEARCH_DEADLINE_MS = 12_000;
export const MAX_ADDON_SEARCH_CALLS = 16;
export const MAX_ADDON_SEARCH_CONCURRENCY = 4;

type ViewerSearchClient = Pick<RivuneTvClient, "searchAddonCatalogs" | "semanticSearch">;

export interface ViewerSearchRequest {
  query: string;
  configuredTypes: readonly string[];
  semanticAvailable: boolean;
  mediaType?: string;
  language?: string;
  region?: string;
  page?: number;
  limit?: number;
  excludedIntentIds?: string[];
  signal?: AbortSignal;
  semanticDeadlineMs?: number;
  onUpdate?: (result: ViewerSearchResult) => void;
}

export interface ViewerSearchResult {
  items: MediaItem[];
  intents: SemanticSearchIntent[];
  mediaTypes: string[];
  page: number;
  hasMore: boolean;
  partial: boolean;
}

function searchIdentities(item: MediaItem): Set<string> {
  const mediaType = normalizedIdentityPart(item.mediaType);
  const identities = new Set<string>();
  const candidates = [item.resourceId, item.id];
  for (const provider of ["imdb", "tmdb", "tvdb"] as const) {
    for (const [key, rawValue] of Object.entries(item.externalIds ?? {})) {
      if (normalizedIdentityPart(key) !== provider || !rawValue.trim()) continue;
      const normalizedValue = normalizedIdentityPart(rawValue);
      const prefix = `${provider}:`;
      const value = normalizedValue.startsWith(prefix) ? normalizedValue.slice(prefix.length) : normalizedValue;
      if (value) identities.add(`${mediaType}:${provider}:${value}`);
    }
    for (const candidate of candidates) {
      const normalized = candidate?.trim();
      if (!normalized) continue;
      const namespaced = /^(imdb|tmdb|tvdb):(.+)$/i.exec(normalized);
      if (namespaced && normalizedIdentityPart(namespaced[1]) === provider) {
        identities.add(`${mediaType}:${provider}:${normalizedIdentityPart(namespaced[2])}`);
      } else if (provider === "imdb" && /^tt\d+$/i.test(normalized)) {
        identities.add(`${mediaType}:imdb:${normalizedIdentityPart(normalized)}`);
      }
    }
  }
  if (identities.size === 0) {
    const addon = normalizedIdentityPart(item.sourceAddonId ?? "unscoped-addon");
    const catalog = normalizedIdentityPart(item.sourceCatalogId ?? "unscoped-catalog");
    const opaqueId = normalizedIdentityPart(item.resourceId?.trim() || item.id);
    identities.add(`${mediaType}:addon:${addon}:catalog:${catalog}:id:${opaqueId}`);
  }
  return identities;
}

function mergeViewerSearchItems(current: readonly MediaItem[], incoming: readonly MediaItem[]): MediaItem[] {
  const seen = new Set<string>();
  for (const item of current) for (const identity of searchIdentities(item)) seen.add(identity);
  let merged: MediaItem[] | undefined;
  for (const item of incoming) {
    const identities = searchIdentities(item);
    let duplicate = false;
    for (const identity of identities) if (seen.has(identity)) duplicate = true;
    for (const identity of identities) seen.add(identity);
    if (duplicate) continue;
    (merged ??= [...current]).push(item);
  }
  return merged ?? current as MediaItem[];
}

function sameViewerSearchResult(left: ViewerSearchResult | null, right: ViewerSearchResult): boolean {
  if (!left || left.page !== right.page || left.hasMore !== right.hasMore || left.partial !== right.partial ||
    left.items.length !== right.items.length || left.intents.length !== right.intents.length || left.mediaTypes.length !== right.mediaTypes.length) return false;
  return left.items.every((item, index) => item === right.items[index]) &&
    left.mediaTypes.every((type, index) => type === right.mediaTypes[index]) &&
    left.intents.every((intent, index) => {
      const other = right.intents[index];
      return intent.id === other.id && intent.kind === other.kind && intent.value === other.value && intent.label === other.label;
    });
}

const PROGRESSIVE_UPDATE_WINDOW_MS = 32;

function abortReason(signal: AbortSignal): unknown {
  return signal.reason ?? new DOMException("Aborted", "AbortError");
}

function normalizedIdentityPart(value: string): string {
  return value.trim().toLowerCase();
}

/** Returns the stable representative key used for a published media item. */
export function canonicalSearchIdentity(item: MediaItem): string {
  return searchIdentities(item).values().next().value as string;
}

async function semanticSearchWithDeadline(
  client: ViewerSearchClient,
  request: ViewerSearchRequest,
  page: number,
  limit: number,
): Promise<{ page: SemanticSearchPage | null; failed: boolean }> {
  if (!request.semanticAvailable) return { page: null, failed: false };
  const controller = new AbortController();
  const parentSignal = request.signal;
  if (parentSignal?.aborted) throw abortReason(parentSignal);
  const semanticRequest = {
    query: request.query,
    ...(request.mediaType ? { mediaType: request.mediaType } : {}),
    ...(request.language ? { language: request.language } : {}),
    ...(request.region ? { region: request.region } : {}),
    page,
    limit,
    excludedIntentIds: request.excludedIntentIds ?? [],
  };

  return new Promise<{ page: SemanticSearchPage | null; failed: boolean }>((resolve, reject) => {
    let settled = false;
    const finish = (operation: () => void) => {
      if (settled) return;
      settled = true;
      clearTimeout(timeout);
      parentSignal?.removeEventListener("abort", cancel);
      operation();
    };
    const cancel = () => {
      controller.abort(parentSignal ? abortReason(parentSignal) : undefined);
      finish(() => reject(parentSignal ? abortReason(parentSignal) : new DOMException("Aborted", "AbortError")));
    };
    const timeout = setTimeout(() => {
      controller.abort();
      finish(() => resolve({ page: null, failed: true }));
    }, request.semanticDeadlineMs ?? SEMANTIC_SEARCH_DEADLINE_MS);
    parentSignal?.addEventListener("abort", cancel, { once: true });
    if (parentSignal?.aborted) {
      cancel();
      return;
    }
    void client.semanticSearch(semanticRequest, controller.signal).then(
      (result) => finish(() => resolve({ page: result, failed: false })),
      () => {
        if (parentSignal?.aborted) cancel();
        else finish(() => resolve({ page: null, failed: true }));
      },
    );
  });
}

interface AddonSearchOutcome {
  items: MediaItem[];
  failed: boolean;
  hasErrors: boolean;
}

interface AddonSearchBudget {
  remaining: number;
  truncated: boolean;
}

async function searchAddons(
  client: ViewerSearchClient,
  types: readonly string[],
  query: string,
  skip: number,
  limit: number,
  language: string | undefined,
  signal: AbortSignal,
  budget: AddonSearchBudget,
  onOutcome?: (outcome: AddonSearchOutcome, index: number) => void,
): Promise<AddonSearchOutcome[]> {
  const callCount = Math.min(types.length, budget.remaining);
  if (callCount < types.length) budget.truncated = true;
  budget.remaining -= callCount;
  const outcomes = new Array<AddonSearchOutcome>(callCount);
  let nextIndex = 0;
  const worker = async () => {
    while (nextIndex < callCount && !signal.aborted) {
      const index = nextIndex++;
      let outcome: AddonSearchOutcome;
      try {
        const batch = await client.searchAddonCatalogs(types[index], query, skip, limit, language, [], signal);
        outcome = { items: mediaFromResourceBatch(batch), failed: false, hasErrors: batch.errors.length > 0 };
      } catch {
        outcome = { items: [], failed: true, hasErrors: false };
      }
      outcomes[index] = outcome;
      if (!signal.aborted) onOutcome?.(outcome, index);
    }
  };
  await Promise.all(Array.from({ length: Math.min(MAX_ADDON_SEARCH_CONCURRENCY, callCount) }, worker));
  return outcomes.filter(Boolean);
}

export async function performViewerSearch(client: ViewerSearchClient, request: ViewerSearchRequest): Promise<ViewerSearchResult> {
  const query = request.query.trim();
  const page = Math.max(1, request.page ?? 1);
  const limit = Math.min(40, Math.max(1, request.limit ?? 30));
  const availableTypes = [...new Set(request.configuredTypes.map(normalizedIdentityPart).filter(Boolean))];
  const requestedType = request.mediaType ? normalizedIdentityPart(request.mediaType) : undefined;
  const configuredTypes = requestedType ? availableTypes.filter((type) => type === requestedType) : availableTypes;
  const addonBudget: AddonSearchBudget = { remaining: MAX_ADDON_SEARCH_CALLS, truncated: false };
  const parentSignal = request.signal;
  if (parentSignal?.aborted) throw abortReason(parentSignal);

  const skip = (page - 1) * limit;
  let publishedItems: MediaItem[] = [];
  let publishedSemantic: SemanticSearchPage | null = null;
  let lastPublished: ViewerSearchResult | null = null;
  let updateTimer: number | undefined;
  let nextBatchSequence = 0;
  const knownIdentities = new Set<string>();
  const pendingBatches: Array<{ order: number; sequence: number; items: readonly MediaItem[]; speculative: boolean }> = [];

  const snapshot = (final: Partial<ViewerSearchResult> = {}): ViewerSearchResult => ({
    items: publishedItems,
    intents: final.intents ?? publishedSemantic?.intents ?? [],
    mediaTypes: final.mediaTypes ?? publishedSemantic?.mediaTypes ?? [],
    page: final.page ?? publishedSemantic?.page ?? page,
    hasMore: final.hasMore ?? publishedSemantic?.hasMore ?? false,
    partial: final.partial ?? publishedSemantic?.partial ?? false,
  });
  const flush = (final: Partial<ViewerSearchResult> = {}) => {
    if (updateTimer !== undefined) {
      clearTimeout(updateTimer);
      updateTimer = undefined;
    }
    pendingBatches.sort((left, right) => left.order - right.order || left.sequence - right.sequence);
    for (const batch of pendingBatches) publishedItems = mergeViewerSearchItems(publishedItems, batch.items);
    pendingBatches.length = 0;
    if (parentSignal?.aborted || publishedItems.length === 0) return;
    const result = snapshot(final);
    if (sameViewerSearchResult(lastPublished, result)) return;
    lastPublished = result;
    request.onUpdate?.(result);
  };
  const enqueue = (items: readonly MediaItem[], order: number, metadataChanged = false, speculative = false) => {
    const uniqueItems = items.filter((item) => {
      const identities = searchIdentities(item);
      let duplicate = false;
      for (const identity of identities) if (knownIdentities.has(identity)) duplicate = true;
      for (const identity of identities) knownIdentities.add(identity);
      return !duplicate;
    });
    if (uniqueItems.length > 0) pendingBatches.push({ order, sequence: nextBatchSequence++, items: uniqueItems, speculative });
    if (uniqueItems.length === 0 && !metadataChanged) return;
    if (!lastPublished && uniqueItems.length > 0) {
      flush();
      return;
    }
    if (lastPublished && updateTimer === undefined) updateTimer = window.setTimeout(flush, PROGRESSIVE_UPDATE_WINDOW_MS);
  };
  const cancelUpdates = () => {
    clearTimeout(updateTimer);
    updateTimer = undefined;
    pendingBatches.length = 0;
  };
  const discardPendingSpeculation = () => {
    for (let index = pendingBatches.length - 1; index >= 0; index -= 1) {
      if (pendingBatches[index].speculative) pendingBatches.splice(index, 1);
    }
    knownIdentities.clear();
    for (const item of publishedItems) for (const identity of searchIdentities(item)) knownIdentities.add(identity);
    for (const batch of pendingBatches) {
      for (const item of batch.items) for (const identity of searchIdentities(item)) knownIdentities.add(identity);
    }
    if (pendingBatches.length === 0) {
      clearTimeout(updateTimer);
      updateTimer = undefined;
    }
  };
  const onSpeculativeOutcome = (outcome: AddonSearchOutcome, index: number) => enqueue(outcome.items, index, false, true);
  const speculativeController = new AbortController();
  const cancelSpeculative = () => {
    speculativeController.abort(parentSignal ? abortReason(parentSignal) : undefined);
    cancelUpdates();
  };
  parentSignal?.addEventListener("abort", cancelSpeculative, { once: true });
  const speculativeAddons = searchAddons(
    client,
    configuredTypes,
    query,
    skip,
    limit,
    request.language,
    speculativeController.signal,
    addonBudget,
    onSpeculativeOutcome,
  );

  let semanticOutcome: { page: SemanticSearchPage | null; failed: boolean };
  try {
    semanticOutcome = await semanticSearchWithDeadline(client, { ...request, query }, page, limit);
  } catch (error) {
    speculativeController.abort(parentSignal?.aborted ? abortReason(parentSignal) : undefined);
    cancelUpdates();
    await speculativeAddons;
    parentSignal?.removeEventListener("abort", cancelSpeculative);
    throw error;
  }
  if (parentSignal?.aborted) {
    speculativeController.abort(abortReason(parentSignal));
    cancelUpdates();
    await speculativeAddons;
    parentSignal.removeEventListener("abort", cancelSpeculative);
    throw abortReason(parentSignal);
  }
  publishedSemantic = semanticOutcome.page;
  const semanticItems = publishedSemantic?.items.map(mediaFromCollection) ?? [];
  if (publishedSemantic) enqueue(semanticItems, configuredTypes.length, true);
  const semantic = semanticOutcome.page;
  const inferredTypes = new Set((semantic?.mediaTypes ?? []).map(normalizedIdentityPart));
  const inferredConfiguredTypes = configuredTypes.filter((type) => inferredTypes.has(type));
  const types = !request.mediaType && inferredConfiguredTypes.length > 0 ? inferredConfiguredTypes : configuredTypes;
  const residualQuery = semantic?.titleQuery.trim();
  const addonQuery = residualQuery && residualQuery.length >= 2 ? residualQuery : query;
  const sameTypes = types.length === configuredTypes.length && types.every((type, index) => type === configuredTypes[index]);
  let addonOutcomes: AddonSearchOutcome[];
  if (addonQuery === query && sameTypes) {
    addonOutcomes = await speculativeAddons;
  } else {
    discardPendingSpeculation();
    speculativeController.abort();
    await speculativeAddons;
    if (parentSignal?.aborted) {
      cancelUpdates();
      throw abortReason(parentSignal);
    }
    const finalController = new AbortController();
    const cancelFinal = () => {
      finalController.abort(parentSignal ? abortReason(parentSignal) : undefined);
      cancelUpdates();
    };
    parentSignal?.addEventListener("abort", cancelFinal, { once: true });
    if (parentSignal?.aborted) cancelFinal();
    try {
      addonOutcomes = await searchAddons(client, types, addonQuery, skip, limit, request.language, finalController.signal, addonBudget, (outcome, index) => enqueue(outcome.items, index));
    } finally {
      parentSignal?.removeEventListener("abort", cancelFinal);
    }
  }
  parentSignal?.removeEventListener("abort", cancelSpeculative);
  if (parentSignal?.aborted) {
    cancelUpdates();
    throw abortReason(parentSignal);
  }

  const addonPartial = addonOutcomes.some((outcome) => outcome.failed || outcome.hasErrors);
  const addonHasMore = addonOutcomes.some((outcome) => outcome.items.length >= limit);
  const final = {
    intents: semantic?.intents ?? [],
    mediaTypes: semantic?.mediaTypes ?? [],
    page: semantic?.page ?? page,
    hasMore: semantic?.hasMore === true || addonHasMore,
    partial: semanticOutcome.failed || semantic?.partial === true || addonPartial || addonBudget.truncated,
  };
  flush(final);
  return snapshot(final);
}
