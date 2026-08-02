import { ArrowLeft, ArrowRight, Bookmark, Clapperboard, Compass, Film, LoaderCircle, Play, Search, Sparkles, Star, Tv, WandSparkles, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { api } from "../api";
import { useAuth } from "../auth";
import { Button, EmptyState, HorizontalDragRow, MediaCard, Notice, SectionHeading, Skeleton } from "../components";
import { translate as t } from "../i18n";
import { notifyError, notifyErrorMessage } from "../notifications";
import { mediaTypeLabel } from "../media";
import type { Collection, ContinueItem, LibraryPage, MediaItem, ResolvedFolder, ResourceBatch } from "../types";

type OpenMedia = (item: MediaItem) => void;

function record(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  return Object.fromEntries(Object.entries(value));
}


function mediaFromBatch(batch: ResourceBatch): MediaItem[] {
  const output: MediaItem[] = [];
  for (const result of batch.results) {
    const metas = result.payload.metas;
    if (!Array.isArray(metas)) continue;
    for (const candidate of metas) {
      const meta = record(candidate);
      if (!meta || typeof meta.id !== "string") continue;
      const title = typeof meta.name === "string" ? meta.name : typeof meta.title === "string" ? meta.title : t("media.untitled");
      output.push({
        id: meta.id,
        mediaType: typeof meta.type === "string" ? meta.type : result.type,
        title,
        posterUrl: typeof meta.poster === "string" ? meta.poster : undefined,
        backgroundUrl: typeof meta.background === "string" ? meta.background : undefined,
        logoUrl: typeof meta.logo === "string" ? meta.logo : undefined,
        description: typeof meta.description === "string" ? meta.description : undefined,
        releaseInfo: typeof meta.releaseInfo === "string" ? meta.releaseInfo : undefined,
        released: typeof meta.released === "string" ? meta.released : undefined,
        voteAverage: typeof meta.imdbRating === "number" ? meta.imdbRating : undefined,
        externalIds: {},
        sources: [{ id: result.addonId, kind: "addon_catalog", title: result.manifestId, addonId: result.addonId }],
        raw: meta,
      });
    }
  }
  return output;
}

function isAvailable(item: MediaItem, hideUnreleased: boolean): boolean {
  if (!hideUnreleased || !item.released) return true;
  const releasedAt = Date.parse(item.released);
  return Number.isNaN(releasedAt) || releasedAt <= Date.now();
}

type EnrichedContinueItem = ContinueItem & {
  episodeTitle?: string;
  episodeOverview?: string;
  episodeStillUrl?: string;
  episodeAirDate?: string;
};

function remainingLabel(item: EnrichedContinueItem): string {
  const remainingSeconds = Math.max(0, item.durationSeconds - item.positionSeconds);
  if (remainingSeconds <= 0) return "";
  const minutes = Math.max(1, Math.ceil(remainingSeconds / 60));
  if (minutes < 60) return t("common.time.minutesLeft", { minutes });
  const hours = Math.floor(minutes / 60);
  const remainder = minutes % 60;
  return t("common.time.hoursLeft", { hours, minutes: remainder > 0 ? ` ${remainder}m` : "" });
}

function mediaFromContinue(item: EnrichedContinueItem): MediaItem {
  const episodeLabel = item.seasonNumber !== undefined && item.episodeNumber !== undefined
    ? `S${String(item.seasonNumber).padStart(2, "0")}E${String(item.episodeNumber).padStart(2, "0")}`
    : "";
  const progress = item.durationSeconds > 0 ? Math.min(100, Math.round(item.positionSeconds / item.durationSeconds * 100)) : 0;
  const seriesTitle = item.title || t("media.type.series");
  return {
    id: item.resourceId || item.titleId,
    titleId: item.titleId,
    mediaType: item.mediaType,
    title: item.mediaType === "episode" ? `${seriesTitle} · ${episodeLabel}${item.episodeTitle ? ` · ${item.episodeTitle}` : ""}` : item.title || t("media.untitled"),
    posterUrl: item.episodeStillUrl || item.posterUrl,
    backgroundUrl: item.episodeStillUrl || item.backgroundUrl || item.posterUrl,
    description: item.episodeOverview || (item.reason === "resume" ? t("home.continue.resumeFromPercent", { progress }) : t("home.continue.nextEpisodeReady")),
    releaseInfo: item.reason === "resume"
      ? episodeLabel ? t("home.continue.episodeProgress", { episodeCode: episodeLabel, progress }) : t("home.continue.percentWatched", { progress })
      : t("home.continue.episodeUpNext", { episodeCode: episodeLabel }),
    released: item.episodeAirDate,
    externalIds: item.resourceProvider && item.resourceId ? { [item.resourceProvider]: item.resourceId } : {},
    raw: {
      progress,
      continueReason: item.reason,
      continueSeriesId: item.seriesId,
      continueSeasonId: item.seasonId,
      continueSeasonNumber: item.seasonNumber,
      continueEpisodeNumber: item.episodeNumber,
      continueEpisodeId: item.titleId,
      continueCardTitle: seriesTitle,
      continueCardEyebrow: item.seasonNumber !== undefined && item.episodeNumber !== undefined ? `S${item.seasonNumber} E${item.episodeNumber}` : "",
      continueCardSubtitle: item.episodeTitle || item.releaseInfo || (item.reason === "resume" ? t("home.continue.percentWatched", { progress }) : ""),
      continueCardBadge: item.reason === "resume"
        ? remainingLabel(item)
        : item.episodeNumber === 1 && (item.seasonNumber ?? 0) > 1 ? t("home.continue.newSeason") : t("home.continue.nextUp"),
    },
  };
}

async function loadContinueItems(signal?: AbortSignal): Promise<EnrichedContinueItem[]> {
  const response = await api.continueWatching(signal).catch(() => ({ items: [] as ContinueItem[] }));
  const seasonIDs = Array.from(new Set(response.items.flatMap((item) => item.seasonId ? [item.seasonId] : [])));
  const seasons = new Map((await Promise.all(seasonIDs.map(async (seasonID) => [seasonID, await api.seasonDetails(seasonID, signal).catch(() => undefined)] as const))).filter((entry) => entry[1] !== undefined));
  return response.items.map((item) => {
    const episode = item.seasonId ? seasons.get(item.seasonId)?.episodes.find((candidate) => candidate.id === item.titleId) : undefined;
    return episode ? { ...item, episodeTitle: episode.name, episodeOverview: episode.overview, episodeStillUrl: episode.stillUrl, episodeAirDate: episode.airDate } : item;
  });
}

function useMediaPreferences() {
  const { activeProfile } = useAuth();
  const profileID = activeProfile?.id ?? "";
  const [preferences, setPreferences] = useState({ profileID: "", hideUnreleased: false, animationsEnabled: true });

  useEffect(() => {
    let active = true;
    if (!activeProfile) {
      setPreferences({ profileID: "", hideUnreleased: false, animationsEnabled: true });
      return () => { active = false; };
    }
    void api.effectiveSettings(activeProfile.id)
      .then((settings) => {
        if (!active) return;
        setPreferences({ profileID: activeProfile.id, hideUnreleased: settings.settings.hideUnreleased === true, animationsEnabled: settings.settings.animationsEnabled !== false });
      })
      .catch(() => {
        if (!active) return;
        setPreferences({ profileID: activeProfile.id, hideUnreleased: false, animationsEnabled: true });
      });
    return () => { active = false; };
  }, [activeProfile]);

  return { ...preferences, profileID, ready: profileID !== "" && preferences.profileID === profileID };
}

type HomeRow = { collection: Collection; resolved: ResolvedFolder };
type HeroSlide = { key: string; item: MediaItem; collection: Collection; folder: ResolvedFolder["folder"] };
const homeFolderConcurrency = 6;
const homeFolderTimeoutMilliseconds = 10_000;

function homeFolderKey(collectionID: string, folderID: string): string {
  return `${collectionID}:${folderID}`;
}


export function HomePage({ onOpenMedia, mediaRevision }: { onOpenMedia: OpenMedia; mediaRevision: number }) {
  const [rows, setRows] = useState<HomeRow[]>([]);
  const [collections, setCollections] = useState<Collection[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [opened, setOpened] = useState<HomeRow | null>(null);
  const [openedCollection, setOpenedCollection] = useState<Collection | null>(null);
  const [continueItems, setContinueItems] = useState<EnrichedContinueItem[]>([]);
  const mediaPreferences = useMediaPreferences();
  const { profileRequestSignal } = useAuth();
  const [pendingFolderKeys, setPendingFolderKeys] = useState<Set<string>>(new Set());
  const [activeHeroIndex, setActiveHeroIndex] = useState(0);
  const homeRequestGeneration = useRef(0);
  const continueRevisionRef = useRef(0);

  useEffect(() => {
    if (!mediaPreferences.ready || !mediaPreferences.profileID || profileRequestSignal.aborted) return;
    const generation = ++homeRequestGeneration.current;
    const controller = new AbortController();
    let active = true;
    const isCurrent = () => active && homeRequestGeneration.current === generation;
    const cancelRequest = () => {
      active = false;
      if (homeRequestGeneration.current === generation) homeRequestGeneration.current++;
      controller.abort();
    };
    profileRequestSignal.addEventListener("abort", cancelRequest, { once: true });
    setLoading(true);
    setError("");
    setRows([]);
    setCollections([]);
    setContinueItems([]);
    setPendingFolderKeys(new Set());

    void loadContinueItems(controller.signal).then((items) => {
      if (isCurrent()) setContinueItems(items);
    }).catch(() => undefined);

    void (async () => {
      try {
        const response = await api.collections(controller.signal);
        if (!isCurrent()) return;
        setCollections(response.collections);
        const targets = response.collections.flatMap((collection) => collection.folders.map((folder) => ({
          collection,
          folderID: folder.id ?? "",
          key: homeFolderKey(collection.id, folder.id ?? ""),
        })));
        setPendingFolderKeys(new Set(targets.map((target) => target.key)));
        setLoading(false);
        if (targets.length === 0) return;

        const results: Array<HomeRow | undefined> = new Array(targets.length);
        let cursor = 0;
        let succeeded = 0;
        const worker = async () => {
          while (isCurrent() && !controller.signal.aborted) {
            const index = cursor++;
            if (index >= targets.length) return;
            const target = targets[index];
            const requestController = new AbortController();
            const abortRequest = () => requestController.abort();
            controller.signal.addEventListener("abort", abortRequest, { once: true });
            const timeout = window.setTimeout(abortRequest, homeFolderTimeoutMilliseconds);
            try {
              const resolved = await api.resolveFolder(target.collection.id, target.folderID, 1, requestController.signal);
              if (!isCurrent()) return;
              results[index] = { collection: target.collection, resolved };
              succeeded++;
              setRows(results.filter((row): row is HomeRow => row !== undefined));
            } catch {
              // A missing folder must not prevent independent rows from rendering.
            } finally {
              window.clearTimeout(timeout);
              controller.signal.removeEventListener("abort", abortRequest);
              if (isCurrent()) {
                setPendingFolderKeys((current) => {
                  const next = new Set(current);
                  next.delete(target.key);
                  return next;
                });
              }
            }
          }
        };
        await Promise.all(Array.from({ length: Math.min(homeFolderConcurrency, targets.length) }, worker));
        if (isCurrent() && succeeded === 0) setError(notifyErrorMessage(t("home.error.sourcesUnavailable"), t("home.error.unavailableTitle")));
      } catch (cause) {
        if (isCurrent()) setError(notifyError(cause, t("home.error.loadFailed"), t("home.error.unavailableTitle")));
      } finally {
        if (isCurrent()) setLoading(false);
      }
    })();

    return () => {
      profileRequestSignal.removeEventListener("abort", cancelRequest);
      cancelRequest();
    };
  }, [mediaPreferences.profileID, mediaPreferences.ready, profileRequestSignal]);

  useEffect(() => {
    if (mediaRevision === 0 || continueRevisionRef.current === mediaRevision || !mediaPreferences.ready || !mediaPreferences.profileID || profileRequestSignal.aborted) return;
    continueRevisionRef.current = mediaRevision;
    let active = true;
    void loadContinueItems(profileRequestSignal).then((items) => {
      if (active) setContinueItems(items);
    }).catch(() => undefined);
    return () => { active = false; };
  }, [mediaPreferences.profileID, mediaPreferences.ready, mediaRevision, profileRequestSignal]);

  const heroSlides = useMemo(() => {
    const seen = new Set<string>();
    const slides: HeroSlide[] = [];
    for (const row of rows) {
      if (!row.collection.heroEnabled) continue;
      for (const item of row.resolved.items) {
        const key = `${item.mediaType}:${item.id}`;
        if (seen.has(key) || !isAvailable(item, mediaPreferences.hideUnreleased)) continue;
        seen.add(key);
        slides.push({ key, item, collection: row.collection, folder: row.resolved.folder });
        if (slides.length === 12) return slides;
      }
    }
    return slides;
  }, [rows, mediaPreferences.hideUnreleased]);

  useEffect(() => {
    setActiveHeroIndex(0);
  }, [mediaPreferences.profileID]);

  useEffect(() => {
    if (!mediaPreferences.animationsEnabled || heroSlides.length < 2 || window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    const timer = window.setInterval(() => setActiveHeroIndex((current) => (current + 1) % heroSlides.length), 8_000);
    return () => window.clearInterval(timer);
  }, [heroSlides.length, mediaPreferences.animationsEnabled]);

  useEffect(() => {
    setActiveHeroIndex((current) => heroSlides.length === 0 ? 0 : current % heroSlides.length);
  }, [heroSlides.length]);

  const heroSlide = heroSlides[activeHeroIndex];
  const hero = heroSlide?.item;
  const heroBackdrop = heroSlide && (hero.backgroundUrl || heroSlide.folder.heroBackdropUrl || heroSlide.collection.backdropImageUrl || hero.posterUrl);
  const heroPending = collections.some((collection) => collection.heroEnabled && collection.folders.some((folder) => pendingFolderKeys.has(homeFolderKey(collection.id, folder.id ?? ""))));
  const continueMedia = continueItems.map(mediaFromContinue);

  function rotateHero(direction: -1 | 1) {
    setActiveHeroIndex((current) => (current + direction + heroSlides.length) % heroSlides.length);
  }


  if (loading) return <div className="home-page page-enter"><Skeleton className="hero-skeleton" /><div className="content-stack">{[0, 1, 2].map((row) => <div key={row}><Skeleton className="heading-skeleton" /><div className="skeleton-row">{[0, 1, 2, 3, 4, 5].map((card) => <Skeleton key={card} className="card-skeleton" />)}</div></div>)}</div></div>;

  if (opened) return <FolderBrowser key={`${opened.collection.id}-${opened.resolved.folder.id}`} row={opened} hideUnreleased={mediaPreferences.hideUnreleased} onBack={() => setOpened(null)} onOpenMedia={onOpenMedia} />;
  if (openedCollection) return <CollectionBrowser key={openedCollection.id} collection={openedCollection} rows={rows.filter((row) => row.collection.id === openedCollection.id)} hideUnreleased={mediaPreferences.hideUnreleased} onBack={() => setOpenedCollection(null)} onOpenMedia={onOpenMedia} />;

  return <div className="home-page page-enter">
    {hero && heroSlide ? <section key={heroSlide.key} className="hero hero--featured">
      {heroBackdrop && <div className="hero__backdrop" aria-hidden="true"><img src={heroBackdrop} alt="" /></div>}
      <div className="hero__content">
        <span className="hero__eyebrow"><WandSparkles size={15} /> {t("home.hero.featuredCollection", { collection: heroSlide.collection.title })}</span>
        {hero.logoUrl ? <img src={hero.logoUrl} alt={hero.title} /> : <h1>{hero.title}</h1>}
        <div className="hero__meta">{hero.releaseInfo && <span>{hero.releaseInfo}</span>}{hero.voteAverage !== undefined && <span><Star size={14} fill="currentColor" /> {hero.voteAverage.toFixed(1)}</span>}<span>{mediaTypeLabel(hero.mediaType)}</span></div>
        <p>{hero.description || t("home.hero.descriptionFallback")}</p>
        <div className="hero__actions"><Button onClick={() => onOpenMedia(hero)}><Play size={19} fill="currentColor" /> {t("home.hero.playNow")}</Button><Button variant="secondary" onClick={() => onOpenMedia(hero)}>{t("home.hero.moreInfo")} <ArrowRight size={18} /></Button></div>
      </div>
      {heroSlides.length > 1 && <div className="hero__navigation" aria-label={t("home.hero.carouselLabel")}>
        <button type="button" onClick={() => rotateHero(-1)} aria-label={t("home.hero.previousTitle")}><ArrowLeft size={18} /></button>
        <span>{heroSlides.map((slide, index) => <button key={slide.key} type="button" className={index === activeHeroIndex ? "is-active" : ""} onClick={() => setActiveHeroIndex(index)} aria-label={t("home.hero.showTitle", { title: slide.item.title })} aria-current={index === activeHeroIndex ? "true" : undefined} />)}</span>
        <button type="button" onClick={() => rotateHero(1)} aria-label={t("home.hero.nextTitle")}><ArrowRight size={18} /></button>
      </div>}
    </section> : heroPending ? <Skeleton className="hero-skeleton" /> : <section className="hero hero--empty"><div className="hero__content"><span className="hero__eyebrow"><Sparkles size={15} /> {t("home.emptyHero.eyebrow")}</span><h1>{t("home.emptyHero.title").split("\n").map((line, index) => <span key={line}>{index > 0 && <br />}{line}</span>)}</h1><p>{t("home.emptyHero.description")}</p></div></section>}
    <div className="content-stack">
      {error && <Notice>{error}</Notice>}
      {continueMedia.length > 0 && <section className="continue-section">
        <SectionHeading title={t("home.continue.title")} />
        <HorizontalDragRow className="media-row media-row--landscape media-row--continue">{continueMedia.map((item) => <MediaCard
          key={`${String(item.raw?.continueReason)}-${item.titleId}`}
          shape="landscape"
          overlay
          title={typeof item.raw?.continueCardTitle === "string" ? item.raw.continueCardTitle : item.title}
          eyebrow={typeof item.raw?.continueCardEyebrow === "string" ? item.raw.continueCardEyebrow : undefined}
          image={item.backgroundUrl || item.posterUrl}
          subtitle={typeof item.raw?.continueCardSubtitle === "string" ? item.raw.continueCardSubtitle : item.releaseInfo}
          badge={typeof item.raw?.continueCardBadge === "string" ? item.raw.continueCardBadge : undefined}
          progress={item.raw?.continueReason === "resume" && typeof item.raw.progress === "number" ? item.raw.progress : undefined}
          onClick={() => onOpenMedia(item)}
        />)}</HorizontalDragRow>
      </section>}
      {collections.map((collection) => {
        const collectionRows = rows.filter((candidate) => candidate.collection.id === collection.id);
        const directItems = Array.from(new Map(collectionRows.flatMap((row) => row.resolved.items)
          .filter((item) => isAvailable(item, mediaPreferences.hideUnreleased))
          .map((item) => [`${item.mediaType}:${item.id}`, item])).values());
        const showDirectly = collection.viewMode === "follow_layout";
        const landscapeItems = directItems.length > 0 && directItems.every((item) => item.mediaType === "tv");
        const collectionPending = collection.folders.some((folder) => pendingFolderKeys.has(homeFolderKey(collection.id, folder.id ?? "")));
        return <section className={`folder-collection-section folder-collection-section--${collection.folderCoverShape}`} key={collection.id}>
          <SectionHeading title={collection.title} action={<button type="button" className="text-button" onClick={() => setOpenedCollection(collection)}>{t("common.actions.viewAll")} <ArrowRight size={16} /></button>} />
          {showDirectly ? directItems.length > 0 ? <HorizontalDragRow className={landscapeItems ? "media-row media-row--landscape" : "media-row"}>{directItems.map((item) => <MediaCard key={`${item.mediaType}-${item.id}`} shape={item.mediaType === "tv" ? "landscape" : "poster"} title={item.title} image={item.mediaType === "tv" ? item.backgroundUrl || item.posterUrl : item.posterUrl} backdrop={item.backgroundUrl} subtitle={item.releaseInfo} onClick={() => onOpenMedia(item)} />)}</HorizontalDragRow> : collectionPending ? <div className="skeleton-row">{[0, 1, 2, 3, 4, 5].map((card) => <Skeleton key={card} className="card-skeleton" />)}</div> : <EmptyState icon={<Clapperboard size={40} />} title={t("home.collection.emptyTitle")} description={t("home.collection.emptySourcesDescription")} /> : <HorizontalDragRow>{collection.folders.map((folder, index) => {
            const row = collectionRows.find((candidate) => candidate.resolved.folder.id === folder.id);
            const artwork = row?.resolved.folder.coverImageUrl || folder.coverImageUrl || row?.resolved.items.find((item) => isAvailable(item, mediaPreferences.hideUnreleased))?.posterUrl || collection.backdropImageUrl;
            return <button key={folder.id ?? index} className="folder-cover-card" disabled={!row} onClick={() => { if (row) setOpened(row); }} aria-label={t("home.folder.openNamed", { name: folder.title })}>
              <span className="folder-cover-card__visual">{artwork ? <img src={artwork} alt="" loading="lazy" draggable={false} /> : <span className="folder-cover-card__fallback">{folder.coverEmoji || folder.title.slice(0, 2).toUpperCase()}</span>}</span>
              {!folder.hideTitle && <span className="folder-cover-card__copy"><strong>{folder.title}</strong></span>}
            </button>;
          })}</HorizontalDragRow>}
        </section>;
      })}
      {collections.length === 0 && <EmptyState icon={<Clapperboard size={46} />} title={t("home.empty.title")} description={t("home.empty.description")} />}
    </div>
  </div>;
}



type CollectionBrowserRow = {
  row: HomeRow;
  items: MediaItem[];
  page: number;
  hasMore: boolean;
};

function CollectionBrowser({ collection, rows, hideUnreleased, onBack, onOpenMedia }: { collection: Collection; rows: HomeRow[]; hideUnreleased: boolean; onBack: () => void; onOpenMedia: OpenMedia }) {
  const [pages, setPages] = useState<CollectionBrowserRow[]>(() => rows.map((row) => ({
    row,
    items: row.resolved.items,
    page: row.resolved.page,
    hasMore: row.resolved.hasMore,
  })));
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [openedFolderID, setOpenedFolderID] = useState("");
  const showFolders = collection.viewMode !== "follow_layout";
  const cards = useMemo(() => {
    const seen = new Set<string>();
    return pages.flatMap((page) => page.items.flatMap((item) => {
      if (!isAvailable(item, hideUnreleased)) return [];
      const key = `${item.mediaType}:${item.id}`;
      if (seen.has(key)) return [];
      seen.add(key);
      return [{ item, shape: page.row.resolved.folder.tileShape }];
    }));
  }, [hideUnreleased, pages]);
  const hasMore = pages.some((page) => page.hasMore);

  async function loadMore() {
    const targets = pages.filter((page) => page.hasMore && page.row.resolved.folder.id);
    if (targets.length === 0) return;
    setLoading(true);
    setError("");
    const outcomes = await Promise.allSettled(targets.map(async (target) => ({
      folderID: target.row.resolved.folder.id ?? "",
      resolved: await api.resolveFolder(collection.id, target.row.resolved.folder.id ?? "", target.page + 1),
    })));
    const loaded = new Map<string, ResolvedFolder>();
    let failed = false;
    for (const outcome of outcomes) {
      if (outcome.status === "fulfilled") loaded.set(outcome.value.folderID, outcome.value.resolved);
      else failed = true;
    }
    setPages((current) => current.map((page) => {
      const folderID = page.row.resolved.folder.id ?? "";
      const next = loaded.get(folderID);
      if (!next) return page;
      const seen = new Set(page.items.map((item) => `${item.mediaType}:${item.id}`));
      return {
        ...page,
        items: [...page.items, ...next.items.filter((item) => {
          const key = `${item.mediaType}:${item.id}`;
          if (seen.has(key)) return false;
          seen.add(key);
          return true;
        })],
        page: next.page,
        hasMore: next.hasMore,
      };
    }));
    if (failed) setError(notifyErrorMessage(t("home.browser.someTitlesLoadFailed"), t("common.error.loadingFailedTitle")));
    setLoading(false);
  }

  const openedPage = pages.find((page) => page.row.resolved.folder.id === openedFolderID);
  if (openedPage) {
    const row = {
      ...openedPage.row,
      resolved: {
        ...openedPage.row.resolved,
        items: openedPage.items,
        page: openedPage.page,
        hasMore: openedPage.hasMore,
      },
    };
    return <FolderBrowser key={openedFolderID} row={row} hideUnreleased={hideUnreleased} backLabel={t("common.actions.backToNamed", { name: collection.title })} onBack={() => setOpenedFolderID("")} onOpenMedia={onOpenMedia} />;
  }

  const description = `${showFolders
    ? t(collection.folders.length === 1 ? "home.collection.folderCount.one" : "home.collection.folderCount.many", { count: collection.folders.length })
    : t(cards.length === 1 ? "home.collection.titleCount.one" : "home.collection.titleCount.many", { count: cards.length })}.`;
  return <div className="standard-page folder-page page-enter">
    <button type="button" className="text-button folder-page__back" onClick={onBack}><ArrowLeft size={17} /> {t("common.actions.backToHome")}</button>
    <SectionHeading eyebrow={t("home.collection.eyebrow")} title={collection.title} description={description} />
    {error && <Notice>{error}</Notice>}
    {showFolders ? <div className={`source-folder-grid source-folder-grid--${collection.folderCoverShape}`}>{collection.folders.map((folder, index) => { const page = pages.find((candidate) => candidate.row.resolved.folder.id === folder.id);
    const resolvedFolder = page?.row.resolved.folder ?? folder;
    const visibleItems = page?.items.filter((item) => isAvailable(item, hideUnreleased)) ?? [];
    const artwork = resolvedFolder.coverImageUrl || visibleItems[0]?.posterUrl || visibleItems[0]?.backgroundUrl || collection.backdropImageUrl;
    return <button key={folder.id ?? index} type="button" className="source-folder-card" disabled={!page} onClick={() => setOpenedFolderID(folder.id ?? "")} aria-label={t("home.folder.openNamed", { name: folder.title })}>
      <span className="source-folder-card__visual">{artwork ? <img src={artwork} alt="" loading="lazy" draggable={false} /> : <span>{folder.coverEmoji || folder.title.slice(0, 2).toUpperCase()}</span>}</span>
      {!folder.hideTitle && <span className="source-folder-card__copy"><strong>{folder.title}</strong><small>{t(visibleItems.length === 1 ? "home.collection.titleCount.one" : "home.collection.titleCount.many", { count: visibleItems.length })}</small></span>}
    </button>; })}</div> : cards.length > 0 ? <div className="media-grid">{cards.map(({ item, shape }) => <MediaCard key={`${item.mediaType}-${item.id}`} title={item.title} image={item.posterUrl} backdrop={item.backgroundUrl} subtitle={item.releaseInfo} shape={shape} onClick={() => onOpenMedia(item)} />)}</div> : <EmptyState icon={<Clapperboard size={46} />} title={t("home.collection.emptyTitle")} description={hideUnreleased ? t("home.browser.noReleasedTitles") : t("home.collection.emptySourcesDescription")} />}
    {!showFolders && hasMore && <div className="load-more"><Button variant="secondary" loading={loading} onClick={() => void loadMore()}>{t("common.actions.loadMore")}</Button></div>}
  </div>;
}

function FolderBrowser({ row, hideUnreleased, onBack, onOpenMedia, backLabel }: { row: HomeRow; hideUnreleased: boolean; onBack: () => void; onOpenMedia: OpenMedia; backLabel?: string }) {
  const [items, setItems] = useState(row.resolved.items);
  const [page, setPage] = useState(row.resolved.page);
  const [hasMore, setHasMore] = useState(row.resolved.hasMore);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const sources = useMemo(() => row.resolved.folder.sources.filter((source) => source.id), [row.resolved.folder.sources]);
  const sourceView = sources.length > 1 ? row.resolved.folder.sourceView ?? "merged" : "merged";
  const [activeSourceID, setActiveSourceID] = useState(sourceView === "categories" ? sources[0]?.id ?? "" : "");
  const [mediaFilter, setMediaFilter] = useState<"all" | "movie" | "series">("all");
  const availableItems = useMemo(() => items.filter((item) => isAvailable(item, hideUnreleased)), [hideUnreleased, items]);
  const itemsBySource = useMemo(() => {
    const grouped = new Map(sources.map((source) => [source.id ?? "", [] as MediaItem[]]));
    for (const item of availableItems) {
      for (const reference of item.sources ?? []) grouped.get(reference.id)?.push(item);
    }
    return grouped;
  }, [availableItems, sources]);
  const activeSource = sources.find((source) => source.id === activeSourceID);
  const scopedItems = activeSourceID ? itemsBySource.get(activeSourceID) ?? [] : availableItems;
  const supportsMediaFilter = (activeSource ? [activeSource] : sources).some((source) => source.tmdb?.mediaType === "both");
  const visibleItems = mediaFilter === "all" ? scopedItems : scopedItems.filter((item) => item.mediaType === mediaFilter);

  useEffect(() => {
    setMediaFilter("all");
  }, [activeSourceID]);

  async function loadMore() {
    const folderID = row.resolved.folder.id;
    if (!folderID) {
      setError(notifyErrorMessage(t("home.folder.missingIdentifier"), t("common.error.loadingFailedTitle")));
      return;
    }
    setLoading(true);
    setError("");
    try {
      const next = await api.resolveFolder(row.collection.id, folderID, page + 1);
      setItems((current) => {
        const seen = new Set(current.map((item) => `${item.mediaType}:${item.id}`));
        const additions = next.items.filter((item) => {
          const key = `${item.mediaType}:${item.id}`;
          if (seen.has(key)) return false;
          seen.add(key);
          return true;
        });
        return [...current, ...additions];
      });
      setPage(next.page);
      setHasMore(next.hasMore);
    } catch (cause) {
      setError(notifyError(cause, t("home.browser.moreTitlesLoadFailed"), t("common.error.loadingFailedTitle")));
    } finally {
      setLoading(false);
    }
  }

  const browsingSourceFolders = sourceView === "folders" && !activeSource;
  const showMediaFilters = supportsMediaFilter && !browsingSourceFolders;
  const pageTitle = activeSource?.title ?? `${row.resolved.folder.coverEmoji ?? ""} ${row.resolved.folder.title}`.trim();
  const pageDescription = `${browsingSourceFolders
    ? t(sources.length === 1 ? "home.folder.sourceFolderCount.one" : "home.folder.sourceFolderCount.many", { count: sources.length })
    : activeSource
      ? t(visibleItems.length === 1 ? "home.folder.curatedTitleCount.fromSource.one" : "home.folder.curatedTitleCount.fromSource.many", { count: visibleItems.length })
      : t(visibleItems.length === 1 ? "home.folder.curatedTitleCount.one" : "home.folder.curatedTitleCount.many", { count: visibleItems.length })}.`;
  return <div className="standard-page folder-page page-enter">
    <button type="button" className="text-button folder-page__back" onClick={() => { if (activeSource && sourceView === "folders") setActiveSourceID(""); else onBack(); }}><ArrowLeft size={17} /> {activeSource && sourceView === "folders" ? t("common.actions.backToNamed", { name: row.resolved.folder.title }) : backLabel ?? t("common.actions.backToHome")}</button>
    <SectionHeading eyebrow={activeSource ? `${row.collection.title} · ${row.resolved.folder.title}` : row.collection.title} title={pageTitle} description={pageDescription} />
    {(sourceView === "categories" || showMediaFilters) && <div className="folder-filter-stack">
      {sourceView === "categories" && <div className="source-category-tabs" role="tablist" aria-label={t("home.folder.sourceCategoriesLabel")}>{sources.map((source) => <button key={source.id} type="button" role="tab" aria-selected={activeSourceID === source.id} className={activeSourceID === source.id ? "is-active" : ""} onClick={() => setActiveSourceID(source.id ?? "")}>{source.title}</button>)}</div>}
      {showMediaFilters && <div className="filter-pills folder-media-filters" role="group" aria-label={t("media.filter.groupLabel")}>{([["all", "media.filter.allTitles"], ["movie", "media.filter.movies"], ["series", "media.filter.series"]] as const).map(([value, labelKey]) => <button key={value} type="button" className={mediaFilter === value ? "is-active" : ""} onClick={() => setMediaFilter(value)}>{t(labelKey)}</button>)}</div>}
    </div>}
    {error && <Notice>{error}</Notice>}
    {browsingSourceFolders ? <div className="source-folder-grid">{sources.map((source) => {
      const sourceItems = itemsBySource.get(source.id ?? "") ?? [];
      const artwork = sourceItems[0]?.posterUrl || sourceItems[0]?.backgroundUrl;
      return <button key={source.id} type="button" className="source-folder-card" onClick={() => setActiveSourceID(source.id ?? "")} aria-label={t("home.folder.openNamed", { name: source.title })}>
        <span className="source-folder-card__visual">{artwork ? <img src={artwork} alt="" loading="lazy" /> : <span>{source.title.slice(0, 2).toUpperCase()}</span>}</span>
        <span className="source-folder-card__copy"><strong>{source.title}</strong><small>{t(sourceItems.length === 1 ? "home.collection.titleCount.one" : "home.collection.titleCount.many", { count: sourceItems.length })}</small></span>
      </button>;
    })}</div> : visibleItems.length > 0 ? <div className="media-grid">{visibleItems.map((item) => <MediaCard key={`${item.mediaType}-${item.id}`} title={item.title} image={item.posterUrl} backdrop={item.backgroundUrl} subtitle={item.releaseInfo} shape={row.resolved.folder.tileShape} onClick={() => onOpenMedia(item)} />)}</div> : <EmptyState icon={<Clapperboard size={46} />} title={t(activeSource ? "home.source.emptyTitle" : "home.folder.emptyTitle")} description={hideUnreleased ? t("home.browser.noReleasedTitles") : t("home.collection.emptySourcesDescription")} />}
    {hasMore && <div className="load-more"><Button variant="secondary" loading={loading} onClick={() => void loadMore()}>{t("common.actions.loadMore")}</Button></div>}
  </div>;
}

export function SearchPage({ onOpenMedia }: { onOpenMedia: OpenMedia }) {
  const [query, setQuery] = useState("");
  const [items, setItems] = useState<MediaItem[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [filter, setFilter] = useState<"all" | "movie" | "series">("all");
  const mediaPreferences = useMediaPreferences();
  const loadedProfileRef = useRef("");
  const normalizedQuery = query.trim();

  useEffect(() => {
    if (!mediaPreferences.ready || !mediaPreferences.profileID || loadedProfileRef.current === mediaPreferences.profileID) return;
    loadedProfileRef.current = mediaPreferences.profileID;
    setQuery(sessionStorage.getItem(`rivune.search.${mediaPreferences.profileID}`) ?? "");
    setFilter("all");
  }, [mediaPreferences.profileID, mediaPreferences.ready]);

  useEffect(() => {
    if (!mediaPreferences.ready) return;
    if (normalizedQuery.length < 2) {
      setItems([]);
      setError("");
      setLoading(false);
      return;
    }
    let active = true;
    const timer = window.setTimeout(() => {
      setLoading(true);
      setError("");
      void Promise.allSettled([api.search("movie", normalizedQuery), api.search("series", normalizedQuery)]).then((outcomes) => {
        if (!active) return;
        const values = outcomes.flatMap((outcome) => outcome.status === "fulfilled" ? mediaFromBatch(outcome.value) : []);
        const seen = new Set<string>();
        setItems(values.filter((item) => {
          const key = `${item.mediaType}:${item.id}`;
          if (seen.has(key)) return false;
          seen.add(key);
          return true;
        }));
        if (outcomes.every((outcome) => outcome.status === "rejected")) setError(notifyErrorMessage(t("search.error.sourcesUnavailable"), t("search.error.unavailableTitle")));
      }).finally(() => { if (active) setLoading(false); });
    }, 350);
    return () => { active = false; window.clearTimeout(timer); };
  }, [mediaPreferences.profileID, mediaPreferences.ready, normalizedQuery]);

  const visible = useMemo(() => filter === "all" ? items : items.filter((item) => item.mediaType === filter), [filter, items]);

  function updateQuery(value: string) {
    setQuery(value);
    if (mediaPreferences.profileID) sessionStorage.setItem(`rivune.search.${mediaPreferences.profileID}`, value);
  }

  return <div className="standard-page search-page page-enter">
    <SectionHeading eyebrow={t("search.eyebrow")} title={t("search.title")} description={t("search.description")} />
    <div className="search-box"><Search size={23} /><input type="search" value={query} onChange={(event) => updateQuery(event.target.value)} onKeyDown={(event) => { if (event.key === "Escape" && query) { event.preventDefault(); updateQuery(""); } }} aria-label={t("nav.search")} aria-keyshortcuts="Escape" placeholder={t("search.placeholder")} autoFocus />{query && <button type="button" className="search-box__clear" aria-label={t("common.close")} title={t("common.close")} onClick={() => updateQuery("")}><X size={17} /></button>}{loading && <LoaderCircle className="spin" />}</div>
    <div className="browse-toolbar">
      <div className="filter-pills"><button type="button" className={filter === "all" ? "is-active" : ""} aria-pressed={filter === "all"} onClick={() => setFilter("all")}><Compass size={16} /> {t("media.filter.all")}</button><button type="button" className={filter === "movie" ? "is-active" : ""} aria-pressed={filter === "movie"} onClick={() => setFilter("movie")}><Film size={16} /> {t("media.filter.movies")}</button><button type="button" className={filter === "series" ? "is-active" : ""} aria-pressed={filter === "series"} onClick={() => setFilter("series")}><Tv size={16} /> {t("media.filter.series")}</button></div>
      {normalizedQuery.length >= 2 && !loading && <span className="browse-toolbar__count" role="status">{t(visible.length === 1 ? "common.results.count.one" : "common.results.count.many", { count: visible.length })}</span>}
    </div>
    {error && <Notice>{error}</Notice>}
    {normalizedQuery.length < 2 ? <div className="search-prompt"><span><Search /></span><h2>{t("search.prompt.title")}</h2><p>{t("search.prompt.description")}</p></div> : !loading && visible.length === 0 ? <EmptyState icon={<Search size={42} />} title={t("search.empty.title")} description={t("search.empty.description")} /> : <div className="media-grid">{visible.map((item) => <MediaCard key={`${item.mediaType}-${item.id}`} title={item.title} image={item.posterUrl} backdrop={item.backgroundUrl} subtitle={item.releaseInfo || mediaTypeLabel(item.mediaType)} onClick={() => onOpenMedia(item)} />)}</div>}
  </div>;
}

export function LibraryPage({ onOpenMedia, mediaRevision }: { onOpenMedia: OpenMedia; mediaRevision: number }) {
  const [library, setLibrary] = useState<LibraryPage | null>(null);
  const [loading, setLoading] = useState(true);
  const [filter, setFilter] = useState<"" | "movie" | "series">("");
  const [query, setQuery] = useState("");
  const [sort, setSort] = useState<"added" | "title" | "released">("added");
  const [error, setError] = useState("");
  const libraryRevisionRef = useRef(0);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError("");
    void api.library(filter).then((value) => {
      if (active) setLibrary(value);
    }).catch((cause) => {
      if (active) setError(notifyError(cause, t("library.error.loadFailed"), t("library.error.unavailableTitle")));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [filter]);

  useEffect(() => {
    if (mediaRevision === 0 || libraryRevisionRef.current === mediaRevision) return;
    libraryRevisionRef.current = mediaRevision;
    let active = true;
    void api.library(filter).then((value) => {
      if (active) {
        setLibrary(value);
        setError("");
      }
    }).catch((cause) => {
      if (active) setError(notifyError(cause, t("library.error.refreshFailed"), t("library.error.refreshFailedTitle")));
    });
    return () => { active = false; };
  }, [filter, mediaRevision]);

  const normalizedQuery = query.trim().toLocaleLowerCase();
  const filteredItems = useMemo(() => {
    const items = [...(library?.items ?? [])].filter((item) => !normalizedQuery || `${item.title ?? ""} ${item.releaseInfo ?? ""}`.toLocaleLowerCase().includes(normalizedQuery));
    items.sort((left, right) => {
      if (sort === "title") return (left.title ?? "").localeCompare(right.title ?? "");
      const leftDate = Date.parse(sort === "released" ? left.released ?? "" : left.addedAt);
      const rightDate = Date.parse(sort === "released" ? right.released ?? "" : right.addedAt);
      return (Number.isNaN(rightDate) ? 0 : rightDate) - (Number.isNaN(leftDate) ? 0 : leftDate);
    });
    return items;
  }, [library?.items, normalizedQuery, sort]);
  const media = filteredItems.map((item): MediaItem => ({
    id: item.resourceId || item.externalId || item.titleId,
    titleId: item.titleId,
    mediaType: item.mediaType,
    title: item.title || t("media.untitled"),
    posterUrl: item.posterUrl,
    backgroundUrl: item.backgroundUrl,
    releaseInfo: item.releaseInfo,
    released: item.released,
    externalIds: item.externalId && item.provider ? { [item.provider]: item.externalId } : undefined,
  }));

  return <div className="standard-page library-page page-enter">
    <SectionHeading eyebrow={t("library.eyebrow")} title={t("library.title")} description={t("library.description")} />
    <div className="library-controls">
      <div className="search-box search-box--compact"><Search size={19} /><input type="search" value={query} onChange={(event) => setQuery(event.target.value)} onKeyDown={(event) => { if (event.key === "Escape" && query) { event.preventDefault(); setQuery(""); } }} aria-label={t("nav.search")} aria-keyshortcuts="Escape" placeholder={t("search.placeholder")} />{query && <button type="button" className="search-box__clear" aria-label={t("common.close")} title={t("common.close")} onClick={() => setQuery("")}><X size={16} /></button>}</div>
      <label className="library-sort"><span>{t("admin.collections.sources.sortBy")}</span><select value={sort} onChange={(event) => setSort(event.target.value as typeof sort)}><option value="added">{t("admin.collections.sort.added")}</option><option value="title">{t("admin.collections.sort.title")}</option><option value="released">{t("admin.collections.sort.released")}</option></select></label>
    </div>
    <div className="browse-toolbar">
      <div className="filter-pills"><button type="button" className={filter === "" ? "is-active" : ""} aria-pressed={filter === ""} onClick={() => setFilter("")}><Bookmark size={16} /> {t("media.filter.allTitles")}</button><button type="button" className={filter === "movie" ? "is-active" : ""} aria-pressed={filter === "movie"} onClick={() => setFilter("movie")}><Film size={16} /> {t("media.filter.movies")}</button><button type="button" className={filter === "series" ? "is-active" : ""} aria-pressed={filter === "series"} onClick={() => setFilter("series")}><Tv size={16} /> {t("media.filter.series")}</button></div>
      {!loading && <span className="browse-toolbar__count" role="status">{t(media.length === 1 ? "common.results.count.one" : "common.results.count.many", { count: media.length })}</span>}
    </div>
    {error && <Notice>{error}</Notice>}
    {loading ? <div className="media-grid">{[0, 1, 2, 3, 4, 5].map((value) => <Skeleton key={value} className="card-skeleton" />)}</div> : media.length > 0 ? <div className="media-grid">{media.map((item) => <MediaCard key={item.titleId} title={item.title} image={item.posterUrl} backdrop={item.backgroundUrl} subtitle={item.releaseInfo || mediaTypeLabel(item.mediaType)} onClick={() => onOpenMedia(item)} />)}</div> : query ? <EmptyState icon={<Search size={42} />} title={t("search.empty.title")} description={t("search.empty.description")} /> : <EmptyState icon={<Bookmark size={46} />} title={t("library.empty.title")} description={t("library.empty.description")} />}
  </div>;
}
