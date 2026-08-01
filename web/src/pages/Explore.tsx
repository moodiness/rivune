import { ArrowLeft, ArrowRight, Bookmark, Clapperboard, Compass, Film, LoaderCircle, Play, Search, Sparkles, Star, Tv, WandSparkles } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type PointerEvent as ReactPointerEvent, type ReactNode } from "react";
import { api } from "../api";
import { useAuth } from "../auth";
import { Button, EmptyState, MediaCard, Notice, SectionHeading, Skeleton } from "../components";
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
      const title = typeof meta.name === "string" ? meta.name : typeof meta.title === "string" ? meta.title : "Untitled";
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
  if (minutes < 60) return `${minutes}m left`;
  const hours = Math.floor(minutes / 60);
  const remainder = minutes % 60;
  return `${hours}h${remainder > 0 ? ` ${remainder}m` : ""} left`;
}

function mediaFromContinue(item: EnrichedContinueItem): MediaItem {
  const episodeLabel = item.seasonNumber !== undefined && item.episodeNumber !== undefined
    ? `S${String(item.seasonNumber).padStart(2, "0")}E${String(item.episodeNumber).padStart(2, "0")}`
    : "";
  const progress = item.durationSeconds > 0 ? Math.min(100, Math.round(item.positionSeconds / item.durationSeconds * 100)) : 0;
  const seriesTitle = item.title || "Series";
  return {
    id: item.resourceId || item.titleId,
    titleId: item.titleId,
    mediaType: item.mediaType,
    title: item.mediaType === "episode" ? `${seriesTitle} · ${episodeLabel}${item.episodeTitle ? ` · ${item.episodeTitle}` : ""}` : item.title || "Untitled",
    posterUrl: item.episodeStillUrl || item.posterUrl,
    backgroundUrl: item.episodeStillUrl || item.backgroundUrl || item.posterUrl,
    description: item.episodeOverview || (item.reason === "resume" ? `Resume from ${progress}%.` : "The next episode is ready."),
    releaseInfo: item.reason === "resume" ? `${episodeLabel}${episodeLabel ? " · " : ""}${progress}% watched` : `${episodeLabel} · Up next`,
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
      continueCardSubtitle: item.episodeTitle || item.releaseInfo || (item.reason === "resume" ? `${progress}% watched` : ""),
      continueCardBadge: item.reason === "resume"
        ? remainingLabel(item)
        : item.episodeNumber === 1 && (item.seasonNumber ?? 0) > 1 ? "New season" : "Next up",
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
function HorizontalDragRow({ children, className = "folder-cover-row" }: { children: ReactNode; className?: string }) {
  const drag = useRef({ active: false, moved: false, pointerID: 0, startX: 0, startScrollLeft: 0 });
  const suppressClick = useRef(false);

  function finishDrag(event: ReactPointerEvent<HTMLDivElement>) {
    if (event.pointerType === "touch" || !drag.current.active || drag.current.pointerID !== event.pointerId) return;
    drag.current.active = false;
    event.currentTarget.classList.remove("is-dragging");
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
    if (drag.current.moved) {
      suppressClick.current = true;
      window.setTimeout(() => { suppressClick.current = false; }, 0);
    }
  }

  return <div
    className={className}
    onPointerDown={(event) => {
      if (event.pointerType === "touch" || event.button !== 0) return;
      drag.current = { active: true, moved: false, pointerID: event.pointerId, startX: event.clientX, startScrollLeft: event.currentTarget.scrollLeft };
    }}
    onPointerMove={(event) => {
      if (event.pointerType === "touch" || !drag.current.active || drag.current.pointerID !== event.pointerId) return;
      const distance = event.clientX - drag.current.startX;
      if (Math.abs(distance) < 5 && !drag.current.moved) return;
      if (!drag.current.moved) event.currentTarget.setPointerCapture(event.pointerId);
      drag.current.moved = true;
      event.preventDefault();
      event.currentTarget.classList.add("is-dragging");
      event.currentTarget.scrollLeft = drag.current.startScrollLeft - distance;
    }}
    onPointerUp={finishDrag}
    onPointerCancel={finishDrag}
    onClickCapture={(event) => {
      if (!suppressClick.current) return;
      event.preventDefault();
      event.stopPropagation();
      suppressClick.current = false;
    }}
  >{children}</div>;
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
        if (isCurrent() && succeeded === 0) setError(notifyErrorMessage("Your collection sources are currently unavailable.", "Home unavailable"));
      } catch (cause) {
        if (isCurrent()) setError(notifyError(cause, "Your home could not be loaded.", "Home unavailable"));
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
        <span className="hero__eyebrow"><WandSparkles size={15} /> Featured · {heroSlide.collection.title}</span>
        {hero.logoUrl ? <img src={hero.logoUrl} alt={hero.title} /> : <h1>{hero.title}</h1>}
        <div className="hero__meta">{hero.releaseInfo && <span>{hero.releaseInfo}</span>}{hero.voteAverage !== undefined && <span><Star size={14} fill="currentColor" /> {hero.voteAverage.toFixed(1)}</span>}<span>{mediaTypeLabel(hero.mediaType)}</span></div>
        <p>{hero.description || "A hand-picked title from your personal collections."}</p>
        <div className="hero__actions"><Button onClick={() => onOpenMedia(hero)}><Play size={19} fill="currentColor" /> Play now</Button><Button variant="secondary" onClick={() => onOpenMedia(hero)}>More info <ArrowRight size={18} /></Button></div>
      </div>
      {heroSlides.length > 1 && <div className="hero__navigation" aria-label="Featured titles">
        <button type="button" onClick={() => rotateHero(-1)} aria-label="Previous featured title"><ArrowLeft size={18} /></button>
        <span>{heroSlides.map((slide, index) => <button key={slide.key} type="button" className={index === activeHeroIndex ? "is-active" : ""} onClick={() => setActiveHeroIndex(index)} aria-label={`Show ${slide.item.title}`} aria-current={index === activeHeroIndex ? "true" : undefined} />)}</span>
        <button type="button" onClick={() => rotateHero(1)} aria-label="Next featured title"><ArrowRight size={18} /></button>
      </div>}
    </section> : heroPending ? <Skeleton className="hero-skeleton" /> : <section className="hero hero--empty"><div className="hero__content"><span className="hero__eyebrow"><Sparkles size={15} /> Your Rivune</span><h1>A blank canvas,<br />ready for your stories.</h1><p>Enable Hero section on a collection to feature its titles here.</p></div></section>}
    <div className="content-stack">
      {error && <Notice>{error}</Notice>}
      {continueMedia.length > 0 && <section className="continue-section">
        <SectionHeading title="Continue Watching" />
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
          <SectionHeading title={collection.title} action={<button type="button" className="text-button" onClick={() => setOpenedCollection(collection)}>View all <ArrowRight size={16} /></button>} />
          {showDirectly ? directItems.length > 0 ? <HorizontalDragRow className={landscapeItems ? "media-row media-row--landscape" : "media-row"}>{directItems.map((item) => <MediaCard key={`${item.mediaType}-${item.id}`} shape={item.mediaType === "tv" ? "landscape" : "poster"} title={item.title} image={item.mediaType === "tv" ? item.backgroundUrl || item.posterUrl : item.posterUrl} backdrop={item.backgroundUrl} subtitle={item.releaseInfo} onClick={() => onOpenMedia(item)} />)}</HorizontalDragRow> : collectionPending ? <div className="skeleton-row">{[0, 1, 2, 3, 4, 5].map((card) => <Skeleton key={card} className="card-skeleton" />)}</div> : <EmptyState icon={<Clapperboard size={40} />} title="This collection is empty" description="Its configured sources did not return any titles." /> : <HorizontalDragRow>{collection.folders.map((folder, index) => {
            const row = collectionRows.find((candidate) => candidate.resolved.folder.id === folder.id);
            const artwork = row?.resolved.folder.coverImageUrl || folder.coverImageUrl || row?.resolved.items.find((item) => isAvailable(item, mediaPreferences.hideUnreleased))?.posterUrl || collection.backdropImageUrl;
            return <button key={folder.id ?? index} className="folder-cover-card" disabled={!row} onClick={() => { if (row) setOpened(row); }} aria-label={`Open ${folder.title}`}>
              <span className="folder-cover-card__visual">{artwork ? <img src={artwork} alt="" loading="lazy" draggable={false} /> : <span className="folder-cover-card__fallback">{folder.coverEmoji || folder.title.slice(0, 2).toUpperCase()}</span>}</span>
              {!folder.hideTitle && <span className="folder-cover-card__copy"><strong>{folder.title}</strong></span>}
            </button>;
          })}</HorizontalDragRow>}
        </section>;
      })}
      {collections.length === 0 && <EmptyState icon={<Clapperboard size={46} />} title="Your home is ready to be curated" description="Install an addon, then create a collection from the admin space." />}
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
    if (failed) setError(notifyErrorMessage("Some titles could not be loaded.", "Loading failed"));
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
    return <FolderBrowser key={openedFolderID} row={row} hideUnreleased={hideUnreleased} backLabel={`Back to ${collection.title}`} onBack={() => setOpenedFolderID("")} onOpenMedia={onOpenMedia} />;
  }

  const description = showFolders
    ? `${collection.folders.length} folder${collection.folders.length === 1 ? "" : "s"}.`
    : `${cards.length} title${cards.length === 1 ? "" : "s"}.`;
  return <div className="standard-page folder-page page-enter">
    <button type="button" className="text-button folder-page__back" onClick={onBack}><ArrowLeft size={17} /> Back to home</button>
    <SectionHeading eyebrow="Collection" title={collection.title} description={description} />
    {error && <Notice>{error}</Notice>}
    {showFolders ? <div className={`source-folder-grid source-folder-grid--${collection.folderCoverShape}`}>{collection.folders.map((folder, index) => { const page = pages.find((candidate) => candidate.row.resolved.folder.id === folder.id);
    const resolvedFolder = page?.row.resolved.folder ?? folder;
    const visibleItems = page?.items.filter((item) => isAvailable(item, hideUnreleased)) ?? [];
    const artwork = resolvedFolder.coverImageUrl || visibleItems[0]?.posterUrl || visibleItems[0]?.backgroundUrl || collection.backdropImageUrl;
    return <button key={folder.id ?? index} type="button" className="source-folder-card" disabled={!page} onClick={() => setOpenedFolderID(folder.id ?? "")} aria-label={`Open ${folder.title}`}>
      <span className="source-folder-card__visual">{artwork ? <img src={artwork} alt="" loading="lazy" draggable={false} /> : <span>{folder.coverEmoji || folder.title.slice(0, 2).toUpperCase()}</span>}</span>
      {!folder.hideTitle && <span className="source-folder-card__copy"><strong>{folder.title}</strong><small>{visibleItems.length} title{visibleItems.length === 1 ? "" : "s"}</small></span>}
    </button>; })}</div> : cards.length > 0 ? <div className="media-grid">{cards.map(({ item, shape }) => <MediaCard key={`${item.mediaType}-${item.id}`} title={item.title} image={item.posterUrl} backdrop={item.backgroundUrl} subtitle={item.releaseInfo} shape={shape} onClick={() => onOpenMedia(item)} />)}</div> : <EmptyState icon={<Clapperboard size={46} />} title="This collection is empty" description={hideUnreleased ? "No released titles are available here." : "Its configured sources did not return any titles."} />}
    {!showFolders && hasMore && <div className="load-more"><Button variant="secondary" loading={loading} onClick={() => void loadMore()}>Load more</Button></div>}
  </div>;
}

function FolderBrowser({ row, hideUnreleased, onBack, onOpenMedia, backLabel = "Back to home" }: { row: HomeRow; hideUnreleased: boolean; onBack: () => void; onOpenMedia: OpenMedia; backLabel?: string }) {
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
      setError(notifyErrorMessage("This folder has no stable identifier.", "Loading failed"));
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
      setError(notifyError(cause, "More titles could not be loaded.", "Loading failed"));
    } finally {
      setLoading(false);
    }
  }

  const browsingSourceFolders = sourceView === "folders" && !activeSource;
  const showMediaFilters = supportsMediaFilter && !browsingSourceFolders;
  const pageTitle = activeSource?.title ?? `${row.resolved.folder.coverEmoji ?? ""} ${row.resolved.folder.title}`.trim();
  const pageDescription = browsingSourceFolders
    ? `${sources.length} source folder${sources.length === 1 ? "" : "s"}.`
    : `${visibleItems.length} curated title${visibleItems.length === 1 ? "" : "s"}${activeSource ? " from this source" : ""}.`;
  return <div className="standard-page folder-page page-enter">
    <button type="button" className="text-button folder-page__back" onClick={() => { if (activeSource && sourceView === "folders") setActiveSourceID(""); else onBack(); }}><ArrowLeft size={17} /> {activeSource && sourceView === "folders" ? `Back to ${row.resolved.folder.title}` : backLabel}</button>
    <SectionHeading eyebrow={activeSource ? `${row.collection.title} · ${row.resolved.folder.title}` : row.collection.title} title={pageTitle} description={pageDescription} />
    {(sourceView === "categories" || showMediaFilters) && <div className="folder-filter-stack">
      {sourceView === "categories" && <div className="source-category-tabs" role="tablist" aria-label="Source categories">{sources.map((source) => <button key={source.id} type="button" role="tab" aria-selected={activeSourceID === source.id} className={activeSourceID === source.id ? "is-active" : ""} onClick={() => setActiveSourceID(source.id ?? "")}>{source.title}</button>)}</div>}
      {showMediaFilters && <div className="filter-pills folder-media-filters" role="group" aria-label="Media type">{([["all", "All titles"], ["movie", "Movies"], ["series", "Series"]] as const).map(([value, label]) => <button key={value} type="button" className={mediaFilter === value ? "is-active" : ""} onClick={() => setMediaFilter(value)}>{label}</button>)}</div>}
    </div>}
    {error && <Notice>{error}</Notice>}
    {browsingSourceFolders ? <div className="source-folder-grid">{sources.map((source) => {
      const sourceItems = itemsBySource.get(source.id ?? "") ?? [];
      const artwork = sourceItems[0]?.posterUrl || sourceItems[0]?.backgroundUrl;
      return <button key={source.id} type="button" className="source-folder-card" onClick={() => setActiveSourceID(source.id ?? "")} aria-label={`Open ${source.title}`}>
        <span className="source-folder-card__visual">{artwork ? <img src={artwork} alt="" loading="lazy" /> : <span>{source.title.slice(0, 2).toUpperCase()}</span>}</span>
        <span className="source-folder-card__copy"><strong>{source.title}</strong><small>{sourceItems.length} title{sourceItems.length === 1 ? "" : "s"}</small></span>
      </button>;
    })}</div> : visibleItems.length > 0 ? <div className="media-grid">{visibleItems.map((item) => <MediaCard key={`${item.mediaType}-${item.id}`} title={item.title} image={item.posterUrl} backdrop={item.backgroundUrl} subtitle={item.releaseInfo} shape={row.resolved.folder.tileShape} onClick={() => onOpenMedia(item)} />)}</div> : <EmptyState icon={<Clapperboard size={46} />} title={activeSource ? "This source is empty" : "This folder is empty"} description={hideUnreleased ? "No released titles are available here." : "Its configured sources did not return any titles."} />}
    {hasMore && <div className="load-more"><Button variant="secondary" loading={loading} onClick={() => void loadMore()}>Load more</Button></div>}
  </div>;
}

export function SearchPage({ onOpenMedia }: { onOpenMedia: OpenMedia }) {
  const [query, setQuery] = useState("");
  const [items, setItems] = useState<MediaItem[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [filter, setFilter] = useState<"all" | "movie" | "series">("all");
  const mediaPreferences = useMediaPreferences();

  useEffect(() => {
    if (!mediaPreferences.ready) return;
    if (query.trim().length < 2) {
      setItems([]);
      setError("");
      return;
    }
    let active = true;
    const timer = window.setTimeout(() => {
      setLoading(true);
      setError("");
      void Promise.allSettled([api.search("movie", query.trim()), api.search("series", query.trim())]).then((outcomes) => {
        if (!active) return;
        const values = outcomes.flatMap((outcome) => outcome.status === "fulfilled" ? mediaFromBatch(outcome.value) : []);
        const seen = new Set<string>();
        setItems(values.filter((item) => {
          const key = `${item.mediaType}:${item.id}`;
          if (seen.has(key)) return false;
          seen.add(key);
          return true;
        }));
        if (outcomes.every((outcome) => outcome.status === "rejected")) setError(notifyErrorMessage("Search sources are unavailable.", "Search unavailable"));
      }).finally(() => { if (active) setLoading(false); });
    }, 350);
    return () => { active = false; window.clearTimeout(timer); };
  }, [mediaPreferences.profileID, mediaPreferences.ready, query]);

  const visible = useMemo(() => filter === "all" ? items : items.filter((item) => item.mediaType === filter), [filter, items]);

  return <div className="standard-page search-page page-enter">
    <SectionHeading eyebrow="Explore your universe" title="Search everything." description="One search across every addon connected to this profile." />
    <div className="search-box"><Search size={23} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Movies, series, anime…" autoFocus />{loading && <LoaderCircle className="spin" />}</div>
    <div className="filter-pills"><button className={filter === "all" ? "is-active" : ""} onClick={() => setFilter("all")}><Compass size={16} /> All</button><button className={filter === "movie" ? "is-active" : ""} onClick={() => setFilter("movie")}><Film size={16} /> Movies</button><button className={filter === "series" ? "is-active" : ""} onClick={() => setFilter("series")}><Tv size={16} /> Series</button></div>
    {error && <Notice>{error}</Notice>}
    {query.length < 2 ? <div className="search-prompt"><span><Search /></span><h2>What are you in the mood for?</h2><p>Start typing to search all your configured sources.</p></div> : !loading && visible.length === 0 ? <EmptyState icon={<Search size={42} />} title="Nothing found" description="Try another title, or check that your addons expose searchable catalogs." /> : <div className="media-grid">{visible.map((item) => <MediaCard key={`${item.mediaType}-${item.id}`} title={item.title} image={item.posterUrl} backdrop={item.backgroundUrl} subtitle={item.releaseInfo || mediaTypeLabel(item.mediaType)} onClick={() => onOpenMedia(item)} />)}</div>}
  </div>;
}

export function LibraryPage({ onOpenMedia, mediaRevision }: { onOpenMedia: OpenMedia; mediaRevision: number }) {
  const [library, setLibrary] = useState<LibraryPage | null>(null);
  const [loading, setLoading] = useState(true);
  const [filter, setFilter] = useState<"" | "movie" | "series">("");
  const [error, setError] = useState("");
  const libraryRevisionRef = useRef(0);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError("");
    void api.library(filter).then((value) => {
      if (active) setLibrary(value);
    }).catch((cause) => {
      if (active) setError(notifyError(cause, "Your library could not be loaded.", "Library unavailable"));
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
      if (active) setError(notifyError(cause, "Your library could not be refreshed.", "Library refresh failed"));
    });
    return () => { active = false; };
  }, [filter, mediaRevision]);

  const media = library?.items.map((item): MediaItem => ({
    id: item.resourceId || item.externalId || item.titleId,
    titleId: item.titleId,
    mediaType: item.mediaType,
    title: item.title || "Untitled",
    posterUrl: item.posterUrl,
    backgroundUrl: item.backgroundUrl,
    releaseInfo: item.releaseInfo,
    released: item.released,
    externalIds: item.externalId && item.provider ? { [item.provider]: item.externalId } : undefined,
  })) ?? [];

  return <div className="standard-page library-page page-enter">
    <SectionHeading eyebrow="Saved for later" title="Your library." description="Everything you kept, always close at hand." />
    <div className="filter-pills"><button className={filter === "" ? "is-active" : ""} onClick={() => setFilter("")}><Bookmark size={16} /> All titles</button><button className={filter === "movie" ? "is-active" : ""} onClick={() => setFilter("movie")}><Film size={16} /> Movies</button><button className={filter === "series" ? "is-active" : ""} onClick={() => setFilter("series")}><Tv size={16} /> Series</button></div>
    {error && <Notice>{error}</Notice>}
    {loading ? <div className="media-grid">{[0, 1, 2, 3, 4, 5].map((value) => <Skeleton key={value} className="card-skeleton" />)}</div> : media.length > 0 ? <div className="media-grid">{media.map((item) => <MediaCard key={item.titleId} title={item.title} image={item.posterUrl} backdrop={item.backgroundUrl} subtitle={item.releaseInfo || mediaTypeLabel(item.mediaType)} onClick={() => onOpenMedia(item)} />)}</div> : <EmptyState icon={<Bookmark size={46} />} title="Your library is empty" description="Save a movie or series from its details page and it will appear here." />}
  </div>;
}
