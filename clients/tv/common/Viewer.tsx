import { useCallback, useEffect, useState } from "react";
import { RivuneTvClient } from "./api";
import { Brand, EmptyState, ErrorPanel, Icon, Screen, Spinner, TvButton } from "./components";
import { Detail, type PlayerRequest } from "./Detail";
import { focusFirst } from "./focus";
import { locale, setLocale, t } from "./i18n";
import { mediaFromCollection, mediaFromContinue, mediaFromLibrary, mediaFromResourceBatch } from "./media";
import { MediaCard } from "./MediaCard";
import { platformAdapter } from "./platform";
import { Player } from "./Player";
import { checkForTvUpdate, downloadTvUpdate, restartForTvUpdate, useTvUpdateState } from "./updates";
import type { Account, CalendarEvent, Collection, Discovery, MediaItem, Profile, ResolvedCollectionFolder, SettingsValues, TitleMediaType } from "./types";

type View = "home" | "search" | "library" | "calendar" | "settings";
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

const nav: Array<{ view: View; icon: "home" | "search" | "library" | "calendar" | "settings"; label: string }> = [
  { view: "home", icon: "home", label: "nav.home" },
  { view: "search", icon: "search", label: "nav.search" },
  { view: "library", icon: "library", label: "nav.library" },
  { view: "calendar", icon: "calendar", label: "nav.calendar" },
  { view: "settings", icon: "settings", label: "nav.settings" },
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

export function Viewer({ client, discovery, account, profile, setBackHandler, onChangeProfile, onDisconnect }: Props) {
  const [view, setView] = useState<View>("home");
  const [homeRows, setHomeRows] = useState<HomeRow[]>([]);
  const [searchQuery, setSearchQuery] = useState("");
  const [searchItems, setSearchItems] = useState<MediaItem[]>([]);
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
    setLoading(true);
    setError("");
    try {
      const results = await Promise.all(["movie", "series", "tv"].map(async (type) => {
        try { return await client.searchAddonCatalogs(type, query, 0, 30); }
        catch { return null; }
      }));
      const items: MediaItem[] = [];
      const seen = new Set<string>();
      for (const result of results) {
        if (!result) continue;
        for (const item of mediaFromResourceBatch(result)) {
          const key = `${item.mediaType}:${item.sourceAddonId ?? ""}:${item.resourceId ?? item.id}`;
          if (seen.has(key)) continue;
          seen.add(key);
          items.push(item);
        }
      }
      setSearchItems(items);
    } catch (cause) { setError(cause instanceof Error ? cause.message : t("error.network")); }
    finally { setLoading(false); }
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
      {searchItems.length
        ? <div className="tv-grid">{searchItems.map((item, index) => <MediaCard key={`${item.mediaType}:${item.id}:${index}`} item={item} artworkUrl={artwork(item.posterUrl || item.backgroundUrl)} onOpen={open} />)}</div>
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

    const updateAction = update.status === "available"
      ? <TvButton tone="primary" onClick={downloadTvUpdate}>{t("settings.update.download")}</TvButton>
      : update.status === "ready"
        ? <TvButton tone="primary" onClick={restartForTvUpdate}>{t("settings.update.restart")}</TvButton>
        : update.status === "unavailable"
          ? null
          : <TvButton disabled={update.status === "checking" || update.status === "downloading"} onClick={checkForTvUpdate}>{t(update.status === "error" ? "settings.update.retry" : "settings.update.check")}</TvButton>;
    return <div className="tv-settings">
      <section className="tv-settings__section"><h2>{t("settings.server")}</h2><div className="tv-settings__fact"><span>{discovery.name}</span><strong>{discovery.serverVersion}</strong></div><div className="tv-settings__fact"><span>Protocol</span><strong>{discovery.protocolVersion}</strong></div></section>
      <section className="tv-settings__section"><h2>{t("settings.profile")}</h2><div className="tv-settings__fact"><span>{profile.name}</span><strong>{account.user.username}</strong></div><div className="tv-actions" style={{ marginTop: 22 }}><TvButton icon="user" onClick={() => void changeProfile()}>{t("settings.changeProfile")}</TvButton><TvButton tone="danger" onClick={() => void disconnect()}>{t("settings.changeServer")}</TvButton></div></section>
      <section className="tv-settings__section"><h2>{t("settings.platform")}</h2><div className="tv-settings__fact"><span>{platformAdapter().platform}</span><strong>{t("settings.version", { version: update.currentVersion })}</strong></div><div className="tv-settings__fact"><span>{t("settings.language")}</span><strong>{settings.interfaceLanguage || discovery.interfaceLanguage}</strong></div></section>
      <section className="tv-settings__section"><h2>{t("settings.update.title")}</h2><div className="tv-settings__fact"><span>{t(`settings.update.status.${update.status}`)}</span>{update.latestVersion && <strong>{update.latestVersion}</strong>}</div>{updateAction && <div className="tv-actions tv-settings__actions">{updateAction}</div>}</section>
    </div>;
  })();

  return <Screen>
    <div className="tv-shell">
      <aside className="tv-sidebar">
        <Brand compact />
        <nav className="tv-nav">{nav.map((item) => <button type="button" key={item.view} className={`tv-nav__button${view === item.view ? " is-active" : ""}`} onClick={() => setView(item.view)}><Icon name={item.icon} size={22} /><span>{t(item.label)}</span></button>)}</nav>
        <div className="tv-sidebar__profile"><strong>{profile.name}</strong><span>{discovery.name}</span></div>
      </aside>
      <main className="tv-content">
        <header className="tv-content__header"><div><h1>{t(`nav.${view}`)}</h1><p>{profile.name} · {discovery.name}</p></div></header>
        {loading && <Spinner />}
        {!loading && body}
      </main>
    </div>
    {detail && <Detail client={client} item={detail} onClose={() => {
      const previous = detailHistory[detailHistory.length - 1];
      if (previous) {
        setDetail(previous);
        setDetailHistory((current) => current.slice(0, -1));
      } else setDetail(null);
    }} onOpen={open} onPlay={setPlayer} />}
    {player && <Player client={client} {...player} setBackHandler={setBackHandler} onClose={() => { setPlayer(null); setRevision((value) => value + 1); }} />}
    {error && <ErrorPanel message={error} onRetry={() => setRevision((value) => value + 1)} onClose={() => setError("")} />}
  </Screen>;
}
