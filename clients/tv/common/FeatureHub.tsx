import { useCallback, useEffect, useRef, useState } from "react";
import { APIError, type RivuneTvClient } from "./api";
import { applyAccessibilityPreferences, DEFAULT_ACCESSIBILITY_PREFERENCES } from "./accessibility";
import { EmptyState, Spinner, TvButton } from "./components";
import { PendingMutationJournal } from "./featureState";
import type {
  AccessibilityPreferencesDocument,
  AddonIncident,
  AddonIncidentEvent,
  MediaItem,
  MediaNotification,
  Profile,
  ReadingQueue,
  ReadingQueueItem,
  SavedSearch,
  SmartCollection,
  SmartCollectionCatalogItem,
} from "./types";
import "./featureStyles.css";

export type FeatureView = "queue" | "smart" | "inbox" | "incidents" | "accessibility";

const journal = new PendingMutationJournal();
const NOTIFICATION_LABELS: Record<MediaNotification["kind"], string> = {
  "calendar-event-upcoming": "Upcoming calendar event",
  "season-available": "Season available",
  "episode-available": "Episode available",
  "movie-release": "Movie release",
};
const INCIDENT_LABELS: Record<AddonIncident["code"], string> = {
  timeout: "Timed out",
  unavailable: "Unavailable",
  invalid_response: "Invalid response",
  unhealthy: "Unhealthy",
};

function queueMedia(item: ReadingQueueItem): MediaItem {
  return {
    id: item.titleId ?? item.resourceId,
    titleId: item.titleId,
    resourceId: item.resourceId,
    sourceAddonId: item.sourceAddonId,
    title: item.title,
    mediaType: item.mediaType,
    posterUrl: item.posterUrl,
  };
}

function smartMedia(item: SmartCollectionCatalogItem): MediaItem {
  return {
    id: item.id,
    titleId: item.id,
    resourceId: item.resourceId,
    sourceAddonId: item.sourceAddonId,
    title: item.title,
    mediaType: item.mediaType === "video" || item.mediaType === "season" ? "series" : item.mediaType,
    posterUrl: item.posterUrl,
    backgroundUrl: item.backgroundUrl,
  };
}

function definitive(error: unknown): boolean {
  return error instanceof APIError && error.status >= 400 && error.status < 500;
}

function preserveFocus(operation: () => void): void {
  const key = document.activeElement instanceof HTMLElement ? document.activeElement.dataset.featureKey : undefined;
  operation();
  if (!key) return;
  window.requestAnimationFrame(() => Array.from(document.querySelectorAll<HTMLElement>("[data-feature-key]")).find((element) => element.dataset.featureKey === key)?.focus());
}

function focusFeature(keys: string[]): void {
  window.requestAnimationFrame(() => {
    const elements = Array.from(document.querySelectorAll<HTMLElement>("[data-feature-key]"));
    for (const key of keys) {
      const target = elements.find((element) => element.dataset.featureKey === key);
      if (target) {
        target.focus();
        return;
      }
    }
  });
}

function adjacentFocusKeys(ids: string[], index: number, prefix: string, heading: string): string[] {
  return [ids[index + 1], ids[index - 1]].filter((id): id is string => Boolean(id)).map((id) => `${prefix}:${id}`).concat(heading);
}

function notificationContext(notification: MediaNotification, index: number): string {
  return `${notification.title}, ${NOTIFICATION_LABELS[notification.kind]}, ${new Date(notification.availableAt).toLocaleDateString()}, notification ${index + 1}`;
}
export function FeatureHub({ view, client, profile, timezone, admin, onOpen, onAccessibilityChange }: {
  view: FeatureView;
  client: RivuneTvClient;
  profile: Profile;
  timezone: string;
  admin: boolean;
  onOpen: (item: MediaItem) => void;
  onAccessibilityChange: (preferences: AccessibilityPreferencesDocument) => void;
}) {
  const [loading, setLoading] = useState(true);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const [queue, setQueue] = useState<ReadingQueue | null>(null);
  const [saved, setSaved] = useState<SavedSearch[]>([]);
  const [smart, setSmart] = useState<SmartCollection[]>([]);
  const [smartItems, setSmartItems] = useState<SmartCollectionCatalogItem[]>([]);
  const [activeSmart, setActiveSmart] = useState("");
  const [notifications, setNotifications] = useState<MediaNotification[]>([]);
  const [incidents, setIncidents] = useState<AddonIncident[]>([]);
  const [events, setEvents] = useState<AddonIncidentEvent[]>([]);
  const [selectedIncidentId, setSelectedIncidentId] = useState("");
  const [incidentLoading, setIncidentLoading] = useState(false);
  const [preferences, setPreferences] = useState<AccessibilityPreferencesDocument>(DEFAULT_ACCESSIBILITY_PREFERENCES);
  const [savedName, setSavedName] = useState("");
  const [savedQuery, setSavedQuery] = useState("");
  const [smartName, setSmartName] = useState("");
  const [smartGenre, setSmartGenre] = useState("");
  const generation = useRef(0);
  const incidentHistoryRef = useRef<HTMLElement>(null);
  const incidentRequest = useRef("");

  const load = useCallback(async () => {
    const current = ++generation.current;
    setLoading(true);
    setError("");
    try {
      if (view === "queue") {
        const result = await client.readingQueue(profile.id);
        if (current === generation.current) preserveFocus(() => setQueue(result));
      } else if (view === "smart") {
        const [savedResult, smartResult] = await Promise.all([client.savedSearches(), client.smartCollections()]);
        if (current !== generation.current) return;
        setSaved(savedResult.savedSearches);
        setSmart(smartResult.smartCollections);
        const selected = smartResult.smartCollections.find((entry) => entry.id === activeSmart) ?? smartResult.smartCollections[0];
        setActiveSmart(selected?.id ?? "");
        const result = selected ? await client.evaluateSmartCollection(selected.id) : null;
        if (current === generation.current) setSmartItems(result?.items ?? []);
      } else if (view === "inbox") {
        const result = await client.mediaNotifications(undefined, 100);
        if (current === generation.current) preserveFocus(() => setNotifications(result.notifications));
      } else if (view === "incidents") {
        if (!admin) throw new APIError(403, "management_required", "Administrator access is required.");
        const result = await client.extensionIncidents();
        if (current === generation.current) preserveFocus(() => setIncidents(result.incidents));
      } else {
        const result = await client.accessibilityPreferences(profile.id);
        if (current === generation.current) {
          setPreferences(result);
          applyAccessibilityPreferences(result);
        }
      }
    } catch (cause) {
      if (current === generation.current) setError(cause instanceof Error ? cause.message : "This section could not be loaded.");
    } finally {
      if (current === generation.current) setLoading(false);
    }
  }, [activeSmart, admin, client, profile.id, view]);

  useEffect(() => { void load(); return () => { generation.current += 1; }; }, [load]);

  async function mutateQueue(key: string, action: (operationId: string, expectedRevision: number) => Promise<unknown>, fallbackKeys: string[] = []) {
    if (!queue) return;
    const pending = journal.begin(profile.id, key, queue.revision);
    setError("");
    try {
      await action(pending.operationId, pending.expectedRevision);
      journal.complete(pending.operationId);
      setMessage("Queue updated.");
      await load();
      if (fallbackKeys.length > 0) focusFeature(fallbackKeys);
    } catch (cause) {
      if (definitive(cause)) journal.complete(pending.operationId);
      if (cause instanceof APIError && cause.status === 409) {
        setError("The queue changed on another screen. The latest order has been loaded; try the action again.");
        await load();
      } else setError(cause instanceof Error ? cause.message : "The queue action failed.");
    }
  }

  async function move(item: ReadingQueueItem, delta: number) {
    if (!queue) return;
    const index = queue.items.findIndex((entry) => entry.id === item.id);
    const target = index + delta;
    if (index < 0 || target < 0 || target >= queue.items.length) return;
    const ids = queue.items.map((entry) => entry.id);
    [ids[index], ids[target]] = [ids[target], ids[index]];
    await mutateQueue(`reorder:${ids.join(",")}`, (operationId, expectedRevision) => client.reorderReadingQueue(profile.id, { operationId, expectedRevision, itemIds: ids }));
  }

  async function evaluate(collection: SmartCollection) {
    setActiveSmart(collection.id);
    setLoading(true);
    setError("");
    try {
      const result = await client.evaluateSmartCollection(collection.id);
      preserveFocus(() => setSmartItems(result.items));
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "The smart collection could not be evaluated.");
    } finally { setLoading(false); }
  }

  async function addSavedSearch() {
    const name = savedName.trim();
    const query = savedQuery.trim();
    if (!name || !query) return;
    setError("");
    try {
      await client.createSavedSearch({ name, query, sort: "relevance" });
      setSavedName("");
      setSavedQuery("");
      setMessage("Search saved.");
      await load();
    } catch (cause) { setError(cause instanceof Error ? cause.message : "The search could not be saved."); }
  }

  async function addSmartCollection() {
    const name = smartName.trim();
    const genre = smartGenre.trim();
    if (!name || !genre) return;
    setError("");
    try {
      await client.createSmartCollection({ name, rules: { type: "genre", operator: "equals", value: genre }, sort: "title" });
      setSmartName("");
      setSmartGenre("");
      setMessage("Smart collection created.");
      await load();
    } catch (cause) { setError(cause instanceof Error ? cause.message : "The smart collection could not be created."); }
  }

  async function notificationAction(notification: MediaNotification, state: "read" | "dismissed", index: number) {
    setError("");
    try {
      await client.acknowledgeMediaNotification(notification.id, state);
      setMessage(state === "read" ? "Marked as read." : "Notification dismissed.");
      await load();
      const adjacent = [notifications[index + 1]?.id, notifications[index - 1]?.id].filter((id): id is string => Boolean(id)).map((id) => `notification:${id}`);
      focusFeature(state === "read" ? [`notification:${notification.id}:read`, `notification:${notification.id}`, ...adjacent, "inbox-heading"] : [...adjacent, "inbox-heading"]);
    } catch (cause) { setError(cause instanceof Error ? cause.message : "The notification action failed."); }
  }

  async function trackNotification(notification: MediaNotification) {
    setError("");
    try {
      await client.followMediaNotifications(notification.titleId, { timezone, horizonDays: 90, leadDays: 1 });
      setMessage(`Tracking releases for ${notification.title}.`);
    } catch (cause) { setError(cause instanceof Error ? cause.message : "Release tracking could not be enabled."); }
  }

  async function showIncident(incident: AddonIncident) {
    incidentRequest.current = incident.id;
    setSelectedIncidentId(incident.id);
    setEvents([]);
    setIncidentLoading(true);
    setError("");
    try {
      const detail = await client.extensionIncident(incident.id);
      if (incidentRequest.current !== incident.id) return;
      setEvents(detail.events);
      setIncidentLoading(false);
      window.requestAnimationFrame(() => incidentHistoryRef.current?.focus());
    } catch (cause) {
      if (incidentRequest.current !== incident.id) return;
      setSelectedIncidentId("");
      setEvents([]);
      setIncidentLoading(false);
      setError(cause instanceof Error ? cause.message : "Incident details are unavailable.");
    }
  }

  async function acknowledgeIncident(incident: AddonIncident) {
    setError("");
    try {
      await client.acknowledgeExtensionIncident(incident.id);
      setMessage("Incident acknowledged.");
      await load();
      focusFeature([`incident:${incident.id}`, "incidents-heading"]);
    } catch (cause) { setError(cause instanceof Error ? cause.message : "The incident could not be acknowledged."); }
  }

  async function savePreferences() {
    setError("");
    try {
      const updated = await client.updateAccessibilityPreferences(profile.id, preferences);
      setPreferences(updated);
      applyAccessibilityPreferences(updated);
      onAccessibilityChange(updated);
      setMessage("Accessibility preferences saved for this profile.");
    } catch (cause) {
      if (cause instanceof APIError && cause.status === 409) {
        setError("These preferences changed on another screen. Latest preferences have been loaded; review and save again.");
        const latest = await client.accessibilityPreferences(profile.id);
        setPreferences(latest);
        applyAccessibilityPreferences(latest);
        onAccessibilityChange(latest);
      } else setError(cause instanceof Error ? cause.message : "Preferences could not be saved.");
    }
  }

  const selectedIncident = incidents.find((incident) => incident.id === selectedIncidentId);
  let body;
  if (view === "queue") body = queue?.items.length ? <div className="tv-feature-rail" aria-label="Reading queue" data-feature-key="queue-heading" tabIndex={-1}>
    {queue.items.map((item, index) => <article className="tv-feature-card" key={item.id}>
      <button type="button" aria-label={`Open ${item.title}, queue position ${index + 1}`} data-feature-key={`queue:${item.id}`} onClick={() => onOpen(queueMedia(item))}><strong>{item.title}</strong><span>Position {index + 1}</span></button>
      <div className="tv-feature-card__actions">
        <TvButton aria-label={`Move ${item.title} earlier from queue position ${index + 1}`} data-feature-key={`queue:${item.id}:earlier`} disabled={index === 0} onClick={() => void move(item, -1)}>Earlier</TvButton>
        <TvButton aria-label={`Move ${item.title} later from queue position ${index + 1}`} data-feature-key={`queue:${item.id}:later`} disabled={index === queue.items.length - 1} onClick={() => void move(item, 1)}>Later</TvButton>
        <TvButton aria-label={`Mark ${item.title} consumed at queue position ${index + 1}`} data-feature-key={`queue:${item.id}:consume`} tone="primary" onClick={() => void mutateQueue(`consume:${item.id}`, (operationId, expectedRevision) => client.consumeReadingQueueItem(profile.id, item.id, { operationId, expectedRevision }), adjacentFocusKeys(queue.items.map((entry) => entry.id), index, "queue", "queue-heading"))}>Mark consumed</TvButton>
        <TvButton aria-label={`Remove ${item.title} from queue position ${index + 1}`} data-feature-key={`queue:${item.id}:remove`} tone="danger" onClick={() => void mutateQueue(`remove:${item.id}`, (operationId, expectedRevision) => client.removeReadingQueueItem(profile.id, item.id, { operationId, expectedRevision }), adjacentFocusKeys(queue.items.map((entry) => entry.id), index, "queue", "queue-heading"))}>Remove</TvButton>
      </div>
    </article>)}
  </div> : !loading && <div data-feature-key="queue-heading" tabIndex={-1}><EmptyState title="Your queue is empty." /></div>;
  else if (view === "smart") body = <>
    <section className="tv-feature-editor"><h2>Saved searches</h2><div className="tv-actions"><input className="tv-input" aria-label="Saved search name" placeholder="Name" value={savedName} onChange={(event) => setSavedName(event.target.value)} /><input className="tv-input" aria-label="Search query" placeholder="Search words" value={savedQuery} onChange={(event) => setSavedQuery(event.target.value)} /><TvButton tone="primary" onClick={() => void addSavedSearch()}>Save search</TvButton></div><div className="tv-feature-pills">{saved.map((entry) => <span key={entry.id}>{entry.name}: {entry.query}</span>)}</div></section>
    <section className="tv-feature-editor"><h2>Smart collections</h2><div className="tv-actions"><input className="tv-input" aria-label="Smart collection name" placeholder="Name" value={smartName} onChange={(event) => setSmartName(event.target.value)} /><input className="tv-input" aria-label="Genre rule" placeholder="Genre" value={smartGenre} onChange={(event) => setSmartGenre(event.target.value)} /><TvButton tone="primary" onClick={() => void addSmartCollection()}>Create from genre</TvButton></div><div className="tv-feature-pills">{smart.map((entry) => <button type="button" className={activeSmart === entry.id ? "is-active" : ""} data-feature-key={`smart:${entry.id}`} key={entry.id} onClick={() => void evaluate(entry)}>{entry.name}</button>)}</div></section>
    {smartItems.length ? <div className="tv-feature-rail">{smartItems.map((item) => <button type="button" className="tv-feature-card" data-feature-key={`smart-item:${item.id}`} key={item.id} onClick={() => onOpen(smartMedia(item))}><strong>{item.title}</strong><span>{item.genres.join(" · ")}</span></button>)}</div> : !loading && <EmptyState title="No matching smart collection items." />}
  </>;
  else if (view === "inbox") body = notifications.length ? <div className="tv-feature-list" aria-label="Media notifications" data-feature-key="inbox-heading" tabIndex={-1}>{notifications.map((notification, index) => <article className={`tv-notification${notification.readAt ? " is-read" : ""}`} key={notification.id}>
    <button type="button" aria-label={`Open ${notificationContext(notification, index)}`} data-feature-key={`notification:${notification.id}`} onClick={() => onOpen({ id: notification.titleId, titleId: notification.titleId, title: notification.title, mediaType: notification.kind === "movie-release" ? "movie" : "series" })}><strong>{notification.title}</strong><span>{NOTIFICATION_LABELS[notification.kind]} · {new Date(notification.availableAt).toLocaleDateString()}</span></button>
    <div className="tv-actions"><TvButton aria-label={`Track releases for ${notificationContext(notification, index)}`} data-feature-key={`notification:${notification.id}:track`} onClick={() => void trackNotification(notification)}>Track</TvButton>{!notification.readAt && <TvButton aria-label={`Mark read ${notificationContext(notification, index)}`} data-feature-key={`notification:${notification.id}:read`} tone="primary" onClick={() => void notificationAction(notification, "read", index)}>Read</TvButton>}<TvButton aria-label={`Dismiss ${notificationContext(notification, index)}`} data-feature-key={`notification:${notification.id}:dismiss`} tone="danger" onClick={() => void notificationAction(notification, "dismissed", index)}>Dismiss</TvButton></div>
  </article>)}</div> : !loading && <div data-feature-key="inbox-heading" tabIndex={-1}><EmptyState title="No media notifications." /></div>;
  else if (view === "incidents") body = incidents.length ? <div className="tv-feature-list" aria-label="Extension incidents" data-feature-key="incidents-heading" tabIndex={-1}>{incidents.map((incident, index) => <article className="tv-incident" key={incident.id}>
    <button type="button" aria-label={`Show incident history for ${incident.addonName}, ${INCIDENT_LABELS[incident.code]}, incident ${index + 1}`} data-feature-key={`incident:${incident.id}`} onClick={() => void showIncident(incident)}><strong>{incident.addonName}</strong><span>{INCIDENT_LABELS[incident.code]} · {incident.state} · {incident.occurrenceCount} occurrences</span></button>
    {!incident.acknowledgedAt && <TvButton aria-label={`Acknowledge ${INCIDENT_LABELS[incident.code]} incident for ${incident.addonName}, incident ${index + 1}`} data-feature-key={`incident:${incident.id}:acknowledge`} tone="primary" onClick={() => void acknowledgeIncident(incident)}>Acknowledge</TvButton>}
  </article>)}{selectedIncident && <section ref={incidentHistoryRef} className="tv-incident-events" tabIndex={-1} aria-labelledby="tv-incident-history-title" aria-live="polite" aria-busy={incidentLoading}><h2 id="tv-incident-history-title">Event history for {selectedIncident.addonName}</h2>{incidentLoading ? <p>Loading incident history…</p> : events.map((event) => <p key={event.id}><strong>{event.type}</strong> · {INCIDENT_LABELS[event.code]} · {new Date(event.occurredAt).toLocaleString()}</p>)}</section>}</div> : !loading && <EmptyState title="No extension incidents." />;
  else body = <div className="tv-accessibility-form">
    <label>Reduced motion<select value={preferences.reducedMotion} onChange={(event) => setPreferences({ ...preferences, reducedMotion: event.target.value as AccessibilityPreferencesDocument["reducedMotion"] })}><option value="system">System</option><option value="reduce">Reduce</option><option value="no-preference">No preference</option></select></label>
    <label>Contrast<select value={preferences.highContrast} onChange={(event) => setPreferences({ ...preferences, highContrast: event.target.value as AccessibilityPreferencesDocument["highContrast"] })}><option value="system">System</option><option value="more">More</option><option value="standard">Standard</option></select></label>
    <label>Text scale<select value={preferences.textScale} onChange={(event) => setPreferences({ ...preferences, textScale: Number(event.target.value) as 100 | 115 | 130 })}><option value="100">100%</option><option value="115">115%</option><option value="130">130%</option></select></label>
    <label>Captions<select value={preferences.captions} onChange={(event) => setPreferences({ ...preferences, captions: event.target.value as AccessibilityPreferencesDocument["captions"] })}><option value="system">System</option><option value="on">On</option><option value="off">Off</option></select></label>
    <label>Audio description<select value={preferences.audioDescription ? "on" : "off"} onChange={(event) => setPreferences({ ...preferences, audioDescription: event.target.value === "on" })}><option value="off">Off</option><option value="on">On</option></select></label>
    <label>Focus indicators<select value={preferences.focusIndicators} onChange={(event) => setPreferences({ ...preferences, focusIndicators: event.target.value as AccessibilityPreferencesDocument["focusIndicators"] })}><option value="standard">Standard</option><option value="enhanced">Enhanced</option></select></label>
    <TvButton tone="primary" onClick={() => void savePreferences()}>Save profile accessibility</TvButton>
  </div>;

  return <section className="tv-feature-hub" data-tv-focus-scope="true" aria-busy={loading}>
    <p className="tv-sr-status" role="status" aria-live="polite">{message}</p>
    {loading && <Spinner />}
    {error && <div className="tv-feature-error" role="alert"><span>{error}</span><TvButton onClick={() => void load()}>Retry</TvButton></div>}
    {body}
  </section>;
}
