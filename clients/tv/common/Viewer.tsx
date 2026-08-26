import { useCallback, useEffect, useRef, useState } from "react";
import { RivuneTvClient } from "./api";
import { applyAccessibilityPreferences, DEFAULT_ACCESSIBILITY_PREFERENCES } from "./accessibility";
import { Brand, EmptyState, ErrorPanel, Icon, Screen, Spinner, TvButton } from "./components";
import { TvCoordination, type TvCommandResult } from "./coordination";
import { Detail, type PlayerRequest } from "./Detail";
import { FeatureHub, type FeatureView } from "./FeatureHub";
import { focusFirst } from "./focus";
import { locale, setLocale, t } from "./i18n";
import { mediaFromCollection, mediaFromContinue, mediaFromLibrary } from "./media";
import { MediaCard } from "./MediaCard";
import { platformAdapter, tvPlaybackPolicy, type TvQualityPreset } from "./platform";
import { Player } from "./Player";
import { checkForTvUpdate, dismissTvUpdateNotice, downloadTvUpdate, restartForTvUpdate, useTvUpdateState } from "./updates";
import { canonicalSearchIdentity, performViewerSearch, TV_SEARCH_TYPES, type ViewerSearchResult } from "./ViewerSearch";
import { PLAYBACK_COORDINATION_CAPABILITY, SEMANTIC_SEARCH_CAPABILITY, type AccessibilityPreferencesDocument, type Account, type CalendarEvent, type Collection, type CoordinatedPlaybackItem, type Discovery, type MediaItem, type PlaybackCommand, type PlaybackDevice, type PlaybackDeviceState, type PlaybackLoadMode, type Profile, type ResolvedCollectionFolder, type SettingsValues, type TitleMediaType } from "./types";

type View = "home" | "search" | "library" | "calendar" | "queue" | "smart" | "inbox" | "incidents" | "accessibility" | "settings";
type HomeRow = { key: string; title: string; items: MediaItem[]; landscape: boolean };

type Props = {
  client: RivuneTvClient;
  discovery: Discovery;
  account: Account;
  profile: Profile;
  setBackHandler: (handler: () => void) => void;
  onChangeProfile: (account: Account) => void;
  onDisconnect: () => void;
};

const nav: Array<{ view: View; icon: "home" | "search" | "library" | "calendar" | "settings"; label: string; admin?: boolean }> = [
  { view: "home", icon: "home", label: "Home" },
  { view: "search", icon: "search", label: "Search" },
  { view: "library", icon: "library", label: "Library" },
  { view: "calendar", icon: "calendar", label: "Calendar" },
  { view: "queue", icon: "library", label: "Queue" },
  { view: "smart", icon: "search", label: "Smart" },
  { view: "inbox", icon: "calendar", label: "Inbox" },
  { view: "incidents", icon: "settings", label: "Incidents", admin: true },
  { view: "accessibility", icon: "settings", label: "Accessibility" },
  { view: "settings", icon: "settings", label: "Settings" },
];

const libraryFilters: Array<{ value: "" | TitleMediaType; label: string }> = [
  { value: "", label: "library.all" },
  { value: "movie", label: "library.movies" },
  { value: "series", label: "library.series" },
  { value: "tv", label: "library.live" },
];

function formattedDate(value: string): string {
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime())
    ? value
    : new Intl.DateTimeFormat(locale(), { year: "numeric", month: "short", day: "numeric" }).format(parsed);
}

type PlayerController = { apply(command: PlaybackCommand): Promise<TvCommandResult>; stop(): Promise<void> };

function mediaFromCoordinated(item: CoordinatedPlaybackItem): MediaItem {
  return {
    id: item.resourceId || item.titleId,
    titleId: item.titleId,
    resourceId: item.resourceId,
    mediaType: item.mediaType,
    title: item.title,
    posterUrl: item.posterUrl,
    sourceAddonId: item.sourceAddonId,
  };
}

export function Viewer({ client, discovery, account, profile, setBackHandler, onChangeProfile, onDisconnect }: Props) {
  const [view, setView] = useState<View>("home");
  const [homeRows, setHomeRows] = useState<HomeRow[]>([]);
  const [searchQuery, setSearchQuery] = useState("");
  const [searchResult, setSearchResult] = useState<ViewerSearchResult>({ items: [], intents: [], mediaTypes: [], page: 1, hasMore: false, partial: false });
  const [libraryItems, setLibraryItems] = useState<MediaItem[]>([]);
  const [libraryType, setLibraryType] = useState<"" | TitleMediaType>("");
  const [calendar, setCalendar] = useState<CalendarEvent[]>([]);
  const [settings, setSettings] = useState<SettingsValues>({});
  const [detail, setDetail] = useState<MediaItem | null>(null);
  const [detailHistory, setDetailHistory] = useState<MediaItem[]>([]);
  const [player, setPlayer] = useState<PlayerRequest | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [revision, setRevision] = useState(0);
  const [, setLanguageRevision] = useState(0);
  const update = useTvUpdateState();
  const [devices, setDevices] = useState<PlaybackDevice[]>([]);
  const [remoteCommand, setRemoteCommand] = useState<PlaybackCommand | null>(null);
  const [qualityPreset, setQualityPreset] = useState<TvQualityPreset>("automatic");
  const [accessibility, setAccessibility] = useState<AccessibilityPreferencesDocument>(DEFAULT_ACCESSIBILITY_PREFERENCES);
  const isAdmin = account.user.role === "global_admin" || account.user.role === "admin";

  const searchAbort = useRef<AbortController | null>(null);
  const coordination = useRef<TvCoordination | null>(null);
  const playerController = useRef<PlayerController | null>(null);
  const playbackState = useRef<PlaybackDeviceState>({ status: "idle", positionMilliseconds: 0, durationMilliseconds: 0 });
  const remoteResults = useRef(new Map<string, (result: TvCommandResult) => void>());
  const artwork = useCallback((value?: string) => value ? client.resolveArtworkUrl(value) ?? undefined : undefined, [client]);

  useEffect(() => {
    let active = true;
    void client.effectiveProfileSettings(profile.id).then((effective) => {
      if (!active) return;
      const next = effective.settings ?? {};
      setSettings(next);
      setLocale(next.interfaceLanguage ?? discovery.interfaceLanguage);
      setLanguageRevision((value) => value + 1);
    }).catch(() => {
      setLocale(discovery.interfaceLanguage);
      setLanguageRevision((value) => value + 1);
    });
    return () => { active = false; };
  }, [client, discovery.interfaceLanguage, profile.id]);

  useEffect(() => {
    let active = true;
    void client.accessibilityPreferences(profile.id).then((preferences) => {
      if (!active) return;
      setAccessibility(preferences);
      applyAccessibilityPreferences(preferences);
    }).catch(() => {
      if (active) applyAccessibilityPreferences(DEFAULT_ACCESSIBILITY_PREFERENCES);
    });
    return () => { active = false; };
  }, [client, profile.id]);

  useEffect(() => {
    if (!discovery.capabilities.includes(PLAYBACK_COORDINATION_CAPABILITY)) return;
    const engine = new TvCoordination(client, {
      state: () => playbackState.current,
      devices: setDevices,
      command: async (command) => {
        if (command.command !== "load") {
          return playerController.current?.apply(command) ?? { status: "failed", code: "invalid_state" };
        }
        if (!command.item || !command.mode) return { status: "failed", code: "unsupported" };
        await playerController.current?.stop();
        playbackState.current = {
          status: "paused", item: command.item,
          positionMilliseconds: command.positionMilliseconds ?? 0, durationMilliseconds: 0,
        };
        let resolve!: (result: TvCommandResult) => void;
        const promise = new Promise<TvCommandResult>((complete) => { resolve = complete; });
        remoteResults.current.set(command.operationId, resolve);
        const timeout = Math.max(0, new Date(command.expiresAt).getTime() - Date.now());
        window.setTimeout(() => {
          if (remoteResults.current.get(command.operationId) !== resolve) return;
          remoteResults.current.delete(command.operationId);
          setRemoteCommand((current) => current?.operationId === command.operationId ? null : current);
          resolve({ status: "expired", code: "expired" });
        }, timeout);
        setDetailHistory([]);
        setDetail(mediaFromCoordinated(command.item));
        setRemoteCommand(command);
        return promise;
      },
    });
    coordination.current = engine;
    engine.start();
    return () => {
      engine.stop();
      if (coordination.current === engine) coordination.current = null;
      for (const resolve of remoteResults.current.values()) resolve({ status: "failed", code: "execution_failed" });
      remoteResults.current.clear();
      setDevices([]);
    };
  }, [client, discovery.capabilities]);

  const finishRemote = useCallback((operationId: string, result: TvCommandResult) => {
    const resolve = remoteResults.current.get(operationId);
    if (!resolve) return;
    remoteResults.current.delete(operationId);
    setRemoteCommand((current) => current?.operationId === operationId ? null : current);
    if (result.status !== "applied" && !playerController.current) playbackState.current = { status: "idle", positionMilliseconds: 0, durationMilliseconds: 0 };
    resolve(result);
  }, []);

  const sendToDevice = useCallback(async (device: PlaybackDevice, item: CoordinatedPlaybackItem, mode: PlaybackLoadMode, positionMilliseconds?: number) => {
    const engine = coordination.current;
    if (!engine) throw new Error(t("coordination.unavailable"));
    const id = await engine.sendLoad(device, { item, positionMilliseconds: positionMilliseconds ?? Math.round(playbackState.current.positionMilliseconds || 0) }, mode);
    const result = await engine.waitOutgoing(id);
    if (result.status !== "applied") throw new Error(t(`coordination.result.${result.code}`));
    if (mode === "handoff") await playerController.current?.stop();
  }, []);

  const controlDevice = useCallback(async (device: PlaybackDevice, command: "play" | "pause" | "seek" | "stop", positionMilliseconds?: number) => {
    const engine = coordination.current;
    if (!engine) throw new Error(t("coordination.unavailable"));
    const id = await engine.send(device, command, positionMilliseconds === undefined ? {} : { positionMilliseconds });
    const result = await engine.waitOutgoing(id);
    if (result.status !== "applied") throw new Error(t(`coordination.result.${result.code}`));
  }, []);

  const loadHome = useCallback(async () => {
    const [collectionResponse, continueResponse] = await Promise.all([client.collections(), client.continueWatching().catch(() => ({ items: [] }))]);
    const rows: HomeRow[] = [];
    const continueItems = continueResponse.items.map(mediaFromContinue);
    if (continueItems.length) rows.push({ key: "continue", title: t("home.continue"), items: continueItems, landscape: true });
    const collections: Collection[] = collectionResponse;
    const requests: Array<Promise<{ collection: Collection; folder: ResolvedCollectionFolder } | null>> = [];
    for (const collection of collections) {
      collection.folders.forEach((folder) => {
        if (!folder.id) return;
        requests.push(client.resolveCollectionFolder(collection.id, folder.id, 1, undefined, settings.metadataLanguage ?? undefined).then((resolved) => ({ collection, folder: resolved })).catch(() => null));
      });
    }
    const resolved = await Promise.all(requests);
    for (const row of resolved) {
      if (!row || row.folder.items.length === 0) continue;
      rows.push({ key: `${row.collection.id}:${row.folder.folder.id}`, title: row.folder.folder.title || row.collection.title, items: row.folder.items.map(mediaFromCollection), landscape: row.folder.folder.tileShape === "landscape" });
    }
    setHomeRows(rows);
  }, [client, settings.metadataLanguage]);

  const loadLibrary = useCallback(async () => {
    const result = await client.library(libraryType || undefined, 1, 100);
    setLibraryItems(result.items.map(mediaFromLibrary));
  }, [client, libraryType]);

  const loadCalendar = useCallback(async () => {
    const from = new Date();
    const to = new Date(from.getTime() + 90 * 86400_000);
    const result = await client.calendar(from.toISOString(), to.toISOString(), settings.metadataLanguage ?? undefined);
    setCalendar(result);
  }, [client, settings.metadataLanguage]);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError("");
    const request = view === "home" ? loadHome() : view === "library" ? loadLibrary() : view === "calendar" ? loadCalendar() : Promise.resolve();
    void request.then(() => {
      if (active) setLoading(false);
    }, (cause: unknown) => {
      if (!active) return;
      setError(cause instanceof Error ? cause.message : t("error.network"));
      setLoading(false);
    });
    return () => { active = false; };
  }, [loadCalendar, loadHome, loadLibrary, revision, view]);

  useEffect(() => {
    if (view !== "search") {
      const controller = searchAbort.current;
      searchAbort.current = null;
      controller?.abort();
    }
    return () => {
      if (view === "search") {
        const controller = searchAbort.current;
        searchAbort.current = null;
        controller?.abort();
      }
    };
  }, [view]);

  useEffect(() => { focusFirst(document.querySelector(".tv-content") ?? document); }, [view]);

  useEffect(() => {
    if (player) return;
    setBackHandler(() => {
      if (detail) {
        const previous = detailHistory[detailHistory.length - 1];
        if (previous) {
          setDetail(previous);
          setDetailHistory((current) => current.slice(0, -1));
        } else setDetail(null);
        return;
      }
      if (view !== "home") { setView("home"); return; }
      platformAdapter().exitApp();
    });
  }, [detail, detailHistory, player, setBackHandler, view]);

  async function search() {
    const query = searchQuery.trim();
    if (!query) return;
    searchAbort.current?.abort();
    const controller = new AbortController();
    searchAbort.current = controller;
    setLoading(true);
    setError("");
    setSearchResult({ items: [], intents: [], mediaTypes: [], page: 1, hasMore: false, partial: false });
    try {
      await performViewerSearch(client, {
        query,
        configuredTypes: TV_SEARCH_TYPES,
        semanticAvailable: discovery.capabilities.includes(SEMANTIC_SEARCH_CAPABILITY),
        language: settings.metadataLanguage ?? discovery.interfaceLanguage,
        signal: controller.signal,
        onUpdate: (result) => {
          if (searchAbort.current === controller && !controller.signal.aborted) setSearchResult(result);
        },
      });
    } catch (cause) {
      if (searchAbort.current === controller && !controller.signal.aborted) {
        setError(cause instanceof Error ? cause.message : t("error.network"));
      }
    } finally {
      if (searchAbort.current === controller) {
        searchAbort.current = null;
        setLoading(false);
      }
    }
  }
  function open(item: MediaItem) {
    if (detail) setDetailHistory((current) => [...current, detail]);
    setDetail(item);
  }

  async function changeProfile() {
    setLoading(true);
    try {
      await client.clearProfileSelection();
      onChangeProfile(await client.currentAccount());
    } finally { setLoading(false); }
  }

  async function disconnect() {
    setLoading(true);
    try { await client.logout(); } catch { /* local credentials are cleared by the client */ }
    finally { onDisconnect(); }
  }

  const body = (() => {
    if (view === "home") return <>
      {homeRows.length ? homeRows.map((row) => <section className="tv-section" key={row.key}>
        <div className="tv-section__heading"><h2>{row.title}</h2></div>
        <div className="tv-row">{row.items.map((item, index) => <MediaCard
          key={`${row.key}:${item.id}:${index}`}
          item={item}
          landscape={row.landscape}
          artworkUrl={artwork(row.landscape ? item.backgroundUrl || item.posterUrl : item.posterUrl || item.backgroundUrl)}
          progressPercent={item.durationSeconds ? (item.resumePositionSeconds ?? 0) / item.durationSeconds * 100 : 0}
          onOpen={open}
        />)}</div>
      </section>) : !loading && <EmptyState title={t("home.empty")} />}
    </>;

    if (view === "search") return <>
      <div className="tv-searchbar">
        <input className="tv-input" value={searchQuery} placeholder={t("search.placeholder")} onChange={(event) => setSearchQuery(event.target.value)} onKeyDown={(event) => { if (event.key === "Enter") void search(); }} />
        <TvButton icon="search" tone="primary" onClick={() => void search()}>{t("search.submit")}</TvButton>
      </div>
      {searchResult.items.length
        ? <div className="tv-grid">{searchResult.items.map((item) => <MediaCard key={canonicalSearchIdentity(item)} item={item} artworkUrl={artwork(item.posterUrl || item.backgroundUrl)} onOpen={open} />)}</div>
        : !loading && searchQuery && <EmptyState title={t("search.empty")} />}
    </>;

    if (view === "library") return <>
      <div className="tv-filters">{libraryFilters.map((filter) => <button type="button" className={`tv-filter${libraryType === filter.value ? " is-active" : ""}`} key={filter.value} onClick={() => setLibraryType(filter.value)}>{t(filter.label)}</button>)}</div>
      {libraryItems.length
        ? <div className="tv-grid">{libraryItems.map((item) => <MediaCard key={item.titleId || item.id} item={item} artworkUrl={artwork(item.posterUrl || item.backgroundUrl)} onOpen={open} />)}</div>
        : !loading && <EmptyState title={t("library.empty")} />}
    </>;

    if (view === "calendar") return calendar.length ? <div className="tv-calendar-list">
      {calendar.map((event) => <button type="button" className="tv-calendar-event" key={event.id} onClick={() => open({
        id: event.resourceId || event.titleId,
        titleId: event.titleId,
        resourceId: event.resourceId ?? undefined,
        mediaType: event.mediaType,
        title: event.title,
        posterUrl: event.posterUrl ?? undefined,
        seasonNumber: event.seasonNumber ?? undefined,
        episodeNumber: event.episodeNumber ?? undefined,
        seriesId: event.seriesId ?? undefined,
        seasonId: event.seasonId ?? undefined,
      })}>
        <time>{formattedDate(event.releaseDate)}</time>
        <span><strong>{event.title}</strong><span>{event.seriesTitle || (event.seasonNumber !== undefined ? `S${event.seasonNumber} · E${event.episodeNumber ?? ""}` : "")}</span></span>
      </button>)}
    </div> : !loading && <EmptyState title={t("calendar.empty")} />;

    if (view === "queue" || view === "smart" || view === "inbox" || view === "incidents" || view === "accessibility") {
      return <FeatureHub view={view as FeatureView} client={client} profile={profile} timezone={discovery.timezone} admin={isAdmin} onOpen={open} onAccessibilityChange={setAccessibility} />;
    }

    const updateAction = update.status === "available"
      ? <TvButton tone="primary" onClick={downloadTvUpdate}>{t("settings.update.download")}</TvButton>
      : update.status === "ready"
        ? <TvButton tone="primary" onClick={restartForTvUpdate}>{t("settings.update.restart")}</TvButton>
        : update.status === "unavailable"
          ? null
          : <TvButton disabled={update.status === "checking" || update.status === "downloading"} onClick={checkForTvUpdate}>{t(update.status === "error" ? "settings.update.retry" : "settings.update.check")}</TvButton>;
    return <div className="tv-settings">
      <section className="tv-settings__section"><h2>{t("settings.server")}</h2><div className="tv-settings__fact"><span>{discovery.name}</span><strong>{discovery.serverVersion}</strong></div><div className="tv-settings__fact"><span>{t("settings.protocol")}</span><strong>{discovery.protocolVersion}</strong></div></section>
      <section className="tv-settings__section"><h2>{t("settings.profile")}</h2><div className="tv-settings__fact"><span>{profile.name}</span><strong>{account.user.username}</strong></div><div className="tv-actions" style={{ marginTop: 22 }}><TvButton icon="user" onClick={() => void changeProfile()}>{t("settings.changeProfile")}</TvButton><TvButton tone="danger" onClick={() => void disconnect()}>{t("settings.changeServer")}</TvButton></div></section>
      <section className="tv-settings__section"><h2>{t("settings.platform")}</h2><div className="tv-settings__fact"><span>{platformAdapter().platform}</span><strong>{t("settings.version", { version: update.currentVersion })}</strong></div><div className="tv-settings__fact"><span>{t("settings.language")}</span><strong>{settings.interfaceLanguage || discovery.interfaceLanguage}</strong></div></section>
      <section className="tv-settings__section"><h2>{t("settings.quality")}</h2><div className="tv-filters">{(["automatic", "economy", "balanced", "maximum"] as TvQualityPreset[]).map((preset) => <button type="button" className={`tv-filter${qualityPreset === preset ? " is-active" : ""}`} key={preset} onClick={() => setQualityPreset(preset)}>{t(`quality.${preset}`)}</button>)}</div>{(() => { const policy = tvPlaybackPolicy(platformAdapter(), client.issuer, qualityPreset); return <><div className="tv-settings__fact"><span>{t("settings.network")}</span><strong>{t(`network.${policy.networkClass}`)}</strong></div><div className="tv-settings__fact"><span>{t("settings.qualityLimit")}</span><strong>{policy.maximumHeight}p · {policy.maximumVideoBitrateKbps} kb/s</strong></div><div className="tv-settings__fact"><span>{t("settings.offline")}</span><strong>{t("settings.offlineUnavailable")}</strong></div></>; })()}</section>
      <section className="tv-settings__section"><h2>{t("settings.update.title")}</h2><div className="tv-settings__fact"><span>{t(`settings.update.status.${update.status}`)}</span>{update.latestVersion && <strong>{update.latestVersion}</strong>}</div>{updateAction && <div className="tv-actions tv-settings__actions">{updateAction}</div>}</section>
    </div>;
  })();

  return <Screen>
    <div className="tv-shell">
      <aside className="tv-sidebar">
        <Brand compact />
        <nav className="tv-nav">{nav.filter((item) => !item.admin || isAdmin).map((item) => <button type="button" key={item.view} className={`tv-nav__button${view === item.view ? " is-active" : ""}`} onClick={() => setView(item.view)}><Icon name={item.icon} size={22} /><span>{item.label}</span></button>)}</nav>
        <div className="tv-sidebar__profile"><strong>{profile.name}</strong><span>{discovery.name}</span></div>
      </aside>
      <main className="tv-content">
        {update.status === "available" && update.notice && <section className="tv-update-notice" role="status">
          <div><strong>{t("settings.update.status.available")}</strong><span>{update.latestVersion}</span></div>
          <div className="tv-actions">
            <TvButton tone="primary" onClick={() => { dismissTvUpdateNotice(); setView("settings"); }}>{t("settings.update.title")}</TvButton>
            <TvButton onClick={dismissTvUpdateNotice}>{t("common.close")}</TvButton>
          </div>
        </section>}
        <header className="tv-content__header"><div><h1>{nav.find((entry) => entry.view === view)?.label ?? view}</h1><p>{profile.name} · {discovery.name}</p></div></header>
        {loading && <Spinner />}
        {(!loading || view === "search" && searchResult.items.length > 0) && body}
      </main>
    </div>
    {detail && <Detail client={client} item={detail} profileId={profile.id} timezone={discovery.timezone} accessibility={accessibility} qualityPreset={qualityPreset} devices={devices} remoteCommand={remoteCommand} onClose={() => {
      const previous = detailHistory[detailHistory.length - 1];
      if (previous) {
        setDetail(previous);
        setDetailHistory((current) => current.slice(0, -1));
      } else setDetail(null);
    }} onOpen={open} onPlay={setPlayer} onSendToDevice={sendToDevice} onRemoteResult={finishRemote} />}
    {player && <Player client={client} {...player} devices={devices} qualityPreset={qualityPreset} onSendToDevice={sendToDevice} onControlDevice={controlDevice}
      onController={(controller) => { playerController.current = controller; }}
      onPlaybackState={(state) => { playbackState.current = state; }}
      onRemoteResult={finishRemote}
      setBackHandler={setBackHandler} onClose={() => { playerController.current = null; playbackState.current = { status: "idle", positionMilliseconds: 0, durationMilliseconds: 0 }; setPlayer(null); setRevision((value) => value + 1); }} />}
    {error && <ErrorPanel message={error} onRetry={() => setRevision((value) => value + 1)} onClose={() => setError("")} />}
  </Screen>;
}
