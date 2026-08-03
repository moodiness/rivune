import { LoaderCircle, RefreshCw, ServerOff } from "lucide-react";
import { lazy, Suspense, useCallback, useEffect, useRef, useState } from "react";
import { useAuth } from "./auth";
import { api, APIError, clearMaintenanceMode, MAINTENANCE_MODE_EVENT, maintenanceModeMessage } from "./api";
import { Button, RivuneMark } from "./components";
import { setLocale, translate as t } from "./i18n";
import { configureNotificationDuration, notifyInfo } from "./notifications";
import { Shell } from "./Shell";
import type { View } from "./Shell";
import { LoginPage, SetupPage } from "./pages/Onboarding";
import { ProfileGate } from "./pages/ProfileGate";
import { DevicePairingPage, PairApprovalPage } from "./pages/Pairing";
import type { InterfaceLanguage, MediaItem, SettingsValues } from "./types";

const validViews: Record<string, View> = { home: "home", search: "search", library: "library", calendar: "calendar", admin: "admin" };
const AdminPage = lazy(() => import("./pages/Admin").then((module) => ({ default: module.AdminPage })));
const HomePage = lazy(() => import("./pages/Explore").then((module) => ({ default: module.HomePage })));
const SearchPage = lazy(() => import("./pages/Explore").then((module) => ({ default: module.SearchPage })));
const LibraryPage = lazy(() => import("./pages/Explore").then((module) => ({ default: module.LibraryPage })));
const CalendarPage = lazy(() => import("./pages/Calendar").then((module) => ({ default: module.CalendarPage })));
const MediaDetails = lazy(() => import("./media").then((module) => ({ default: module.MediaDetails })));

type MediaRoute = {
  item: MediaItem;
  origin: View;
};
type MediaRouteContext = { seasonID: string; episodeID?: string; seasonNumber: number; episodeNumber?: number };

type AppRoute = { view: View; media: MediaRoute | null; canonicalURL?: string };

const mediaRoutePrefix = "media/";
const mediaRouteFields = ["titleId", "posterUrl", "backgroundUrl", "logoUrl", "releaseInfo", "released"] as const;
const uuidRoutePattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

function decodeRouteSegment(value: string): string {
  try {
    return decodeURIComponent(value);
  } catch {
    return "";
  }
}

function viewURL(view: View): string {
  return `/#${view}`;
}

function storedRouteItem(mediaType: string): MediaItem | undefined {
  const stored = window.history.state?.rivuneMediaItem as MediaItem | undefined;
  return stored?.mediaType === mediaType ? stored : undefined;
}
function encodeRouteID(value: string): string {
  return encodeURIComponent(value).replaceAll("%3A", ":");
}

function seriesRouteID(item: MediaItem): string {
  const configured = item.raw?.routeSeriesResourceId;
  if (typeof configured === "string" && configured.trim()) return configured.trim();
  if (item.mediaType === "series") return item.id;
  const episodeResource = item.id.match(/^(.*):\d+:\d+$/);
  return episodeResource?.[1] || item.id;
}


function cleanMediaRoute(): MediaRoute | null {
  const segments = window.location.pathname.split("/").filter(Boolean).map(decodeRouteSegment);
  if (segments[0] !== "media" || segments.length < 3) return null;
  const origin = validViews[window.history.state?.rivuneOrigin ?? ""] ?? "home";

  if (segments[1] === "series" && segments[3] === "season") {
    const seriesResourceID = segments[2] ?? "";
    const seasonNumber = Number(segments[4]);
    const hasEpisode = segments.length === 7 && segments[5] === "episode";
    const episodeNumber = hasEpisode ? Number(segments[6]) : undefined;
    if (
      !seriesResourceID ||
      (segments.length !== 5 && !hasEpisode) ||
      !Number.isInteger(seasonNumber) ||
      (hasEpisode && !Number.isInteger(episodeNumber))
    ) return null;
    const mediaType = hasEpisode ? "episode" : "series";
    const stored = storedRouteItem(mediaType);
    const storedSeasonNumber = typeof stored?.raw?.continueSeasonNumber === "number" ? stored.raw.continueSeasonNumber : stored?.seasonNumber;
    const storedEpisodeNumber = typeof stored?.raw?.continueEpisodeNumber === "number" ? stored.raw.continueEpisodeNumber : stored?.episodeNumber;
    const storedContextMatchesPath = storedSeasonNumber === seasonNumber && storedEpisodeNumber === episodeNumber;
    const item: MediaItem = stored
      ? { ...stored }
      : {
        id: hasEpisode ? `${seriesResourceID}:${seasonNumber}:${episodeNumber}` : seriesResourceID,
        titleId: mediaType === "series" && uuidRoutePattern.test(seriesResourceID) ? seriesResourceID : undefined,
        mediaType,
        title: t("media.untitled"),
      };
    item.seasonNumber = seasonNumber;
    if (episodeNumber !== undefined) item.episodeNumber = episodeNumber;
    item.raw = {
      ...item.raw,
      routeSeriesResourceId: seriesResourceID,
      continueSeasonId: storedContextMatchesPath ? item.raw?.continueSeasonId : undefined,
      continueSeasonNumber: seasonNumber,
      continueEpisodeId: storedContextMatchesPath ? item.raw?.continueEpisodeId : undefined,
      continueEpisodeNumber: episodeNumber,
    };
    return { item, origin };
  }

  if (segments.length !== 3) return null;
  const mediaType = segments[1] ?? "";
  const id = segments[2] ?? "";
  if (!mediaType || !id) return null;
  const stored = storedRouteItem(mediaType);
  const item: MediaItem = stored
    ? { ...stored }
    : {
      id,
      titleId: uuidRoutePattern.test(id) ? id : undefined,
      mediaType,
      title: t("media.untitled"),
    };
  return { item, origin };
}

function legacyMediaRoute(routePath: string, query: string): MediaRoute | null {
  const segments = routePath.slice(mediaRoutePrefix.length).split("/");
  if (segments.length < 2) return null;
  const params = new URLSearchParams(query);
  const origin = validViews[params.get("from") ?? ""] ?? "home";
  const mediaType = decodeRouteSegment(segments[0]);
  const id = decodeRouteSegment(segments.slice(1).join("/"));
  if (!mediaType || !id) return null;
  const stored = storedRouteItem(mediaType);
  const title = params.get("title")?.trim() || stored?.title || t("media.untitled");
  const item: MediaItem = stored?.id === id ? { ...stored, id, mediaType, title } : { id, mediaType, title };
  for (const field of mediaRouteFields) {
    const value = params.get(field);
    if (value) item[field] = value;
  }
  const seasonValue = params.get("season");
  const episodeValue = params.get("episode");
  const seasonNumber = seasonValue === null ? undefined : Number(seasonValue);
  const episodeNumber = episodeValue === null ? undefined : Number(episodeValue);
  const seriesID = params.get("seriesId") ?? "";
  const seasonID = params.get("seasonId") ?? "";
  const episodeID = params.get("episodeId") ?? "";
  if (seasonNumber !== undefined && Number.isInteger(seasonNumber)) item.seasonNumber = seasonNumber;
  if (episodeNumber !== undefined && Number.isInteger(episodeNumber)) item.episodeNumber = episodeNumber;
  for (const [key, value] of params) {
    if (!key.startsWith("external.") || !value) continue;
    item.externalIds = { ...item.externalIds, [key.slice("external.".length)]: value };
  }
  if (seriesID || seasonID || episodeID || seasonNumber !== undefined || episodeNumber !== undefined) {
    item.raw = {
      ...item.raw,
      continueSeriesId: seriesID || undefined,
      continueSeasonId: seasonID || undefined,
      continueSeasonNumber: seasonNumber !== undefined && Number.isInteger(seasonNumber) ? seasonNumber : undefined,
      continueEpisodeNumber: episodeNumber !== undefined && Number.isInteger(episodeNumber) ? episodeNumber : undefined,
      continueEpisodeId: episodeID || item.titleId,
    };
  }
  return { item, origin };
}

function appRoute(): AppRoute {
  const cleanMedia = cleanMediaRoute();
  if (cleanMedia) return { view: cleanMedia.origin, media: cleanMedia };
  const hash = window.location.hash.slice(1);
  const [routePath, query = ""] = hash.split("?", 2);
  if (!routePath.startsWith(mediaRoutePrefix)) return { view: validViews[routePath] ?? "home", media: null };
  const legacyMedia = legacyMediaRoute(routePath, query);
  if (!legacyMedia) return { view: "home", media: null };
  return {
    view: legacyMedia.origin,
    media: legacyMedia,
    canonicalURL: mediaRouteURL(legacyMedia.item, legacyMedia.origin),
  };
}

function mediaRouteURL(item: MediaItem, _origin: View, context?: MediaRouteContext): string {
  const raw = item.raw ?? {};
  const seasonNumber = context?.seasonNumber ?? (typeof raw.continueSeasonNumber === "number" ? raw.continueSeasonNumber : item.seasonNumber);
  const episodeNumber = context ? context.episodeNumber : typeof raw.continueEpisodeNumber === "number" ? raw.continueEpisodeNumber : item.episodeNumber;
  if (item.mediaType === "series" || item.mediaType === "episode") {
    const seriesResourceID = seriesRouteID(item);
    let path = `/media/series/${encodeRouteID(seriesResourceID)}`;
    if (seasonNumber !== undefined && Number.isInteger(seasonNumber)) {
      path += `/season/${seasonNumber}`;
      if (episodeNumber !== undefined && Number.isInteger(episodeNumber)) path += `/episode/${episodeNumber}`;
    }
    return path;
  }
  return `/media/${encodeURIComponent(item.mediaType)}/${encodeRouteID(item.id)}`;
}

function restoreScroll(top: number): void {
  window.requestAnimationFrame(() => window.scrollTo({ top, behavior: "auto" }));
}

type RuntimeSettings = {
  interfaceLanguage: InterfaceLanguage;
  autoplayNextEpisode: boolean;
  cardDensity: "comfortable" | "compact";
  animationsEnabled: boolean;
  subtitleSizePercent: number;
  subtitleTextColor: string;
  subtitleBackgroundOpacityPercent: number;
  notificationsEnabled: boolean;
  notificationDurationSeconds: number;
  notificationPollIntervalSeconds: number;
};
const defaultRuntimeSettings: RuntimeSettings = {
  interfaceLanguage: "en",
  autoplayNextEpisode: true,
  cardDensity: "comfortable",
  animationsEnabled: true,
  subtitleSizePercent: 100,
  subtitleTextColor: "#FFFFFF",
  subtitleBackgroundOpacityPercent: 60,
  notificationsEnabled: true,
  notificationDurationSeconds: 5,
  notificationPollIntervalSeconds: 5,
};

function boundedSetting(value: number | null | undefined, fallback: number, minimum: number, maximum: number): number {
  return typeof value === "number" && Number.isInteger(value) && value >= minimum && value <= maximum ? value : fallback;
}

function runtimeSettings(values: SettingsValues): RuntimeSettings {
  return {
    interfaceLanguage: values.interfaceLanguage ?? defaultRuntimeSettings.interfaceLanguage,
    autoplayNextEpisode: typeof values.autoplayNextEpisode === "boolean" ? values.autoplayNextEpisode : defaultRuntimeSettings.autoplayNextEpisode,
    cardDensity: values.cardDensity === "compact" ? "compact" : "comfortable",
    animationsEnabled: typeof values.animationsEnabled === "boolean" ? values.animationsEnabled : defaultRuntimeSettings.animationsEnabled,
    subtitleSizePercent: boundedSetting(values.subtitleSizePercent, defaultRuntimeSettings.subtitleSizePercent, 50, 200),
    subtitleTextColor: typeof values.subtitleTextColor === "string" && /^#[0-9A-Fa-f]{6}$/.test(values.subtitleTextColor) ? values.subtitleTextColor.toUpperCase() : defaultRuntimeSettings.subtitleTextColor,
    subtitleBackgroundOpacityPercent: boundedSetting(values.subtitleBackgroundOpacityPercent, defaultRuntimeSettings.subtitleBackgroundOpacityPercent, 0, 100),
    notificationsEnabled: typeof values.notificationsEnabled === "boolean" ? values.notificationsEnabled : defaultRuntimeSettings.notificationsEnabled,
    notificationDurationSeconds: boundedSetting(values.notificationDurationSeconds, defaultRuntimeSettings.notificationDurationSeconds, 2, 30),
    notificationPollIntervalSeconds: boundedSetting(values.notificationPollIntervalSeconds, defaultRuntimeSettings.notificationPollIntervalSeconds, 5, 300),
  };
}

function useRuntimeSettings(profileID: string | undefined, serverLanguage: InterfaceLanguage = "en"): { settings: RuntimeSettings; ready: boolean } {
  const serverDefaults = { ...defaultRuntimeSettings, interfaceLanguage: serverLanguage };
  const [loaded, setLoaded] = useState<{ profileID: string; settings: RuntimeSettings; ready: boolean }>({
    profileID: "",
    settings: serverDefaults,
    ready: true,
  });

  useEffect(() => {
    if (!profileID) {
      api.configureMetadataLocale("auto", "auto");
      setLocale(serverLanguage);
      setLoaded({ profileID: "", settings: serverDefaults, ready: true });
      return;
    }
    let active = true;
    let requestGeneration = 0;
    const load = () => {
      const generation = ++requestGeneration;
      setLoaded((current) => current.profileID === profileID
        ? current
        : { profileID, settings: serverDefaults, ready: false });
      void api.effectiveSettings(profileID).then((response) => {
        if (!active || generation !== requestGeneration) return;
        api.configureMetadataLocale(
          response.settings.metadataLanguage ?? undefined,
          response.settings.metadataRegion ?? undefined,
          response.settings.audioLanguage ?? undefined,
          response.settings.seriesMappingProvider ?? undefined,
          response.settings.subtitleLanguage ?? undefined,
        );
        const next = runtimeSettings(response.settings);
        setLocale(next.interfaceLanguage);
        setLoaded({ profileID, settings: next, ready: true });
      }).catch(() => {
        if (!active || generation !== requestGeneration) return;
        api.configureMetadataLocale("auto", "auto");
        setLocale(serverLanguage);
        setLoaded({ profileID, settings: serverDefaults, ready: true });
      });
    };
    load();
    window.addEventListener("rivune:settings-changed", load);
    return () => {
      active = false;
      window.removeEventListener("rivune:settings-changed", load);
    };
  }, [profileID, serverLanguage]);

  return loaded.profileID === (profileID ?? "")
    ? { settings: loaded.settings, ready: loaded.ready }
    : { settings: serverDefaults, ready: false };
}

function applyRuntimeSettings(settings: RuntimeSettings): void {
  const root = document.documentElement;
  root.dataset.autoplayNextEpisode = String(settings.autoplayNextEpisode);
  root.dataset.cardDensity = settings.cardDensity;
  root.dataset.animationsEnabled = String(settings.animationsEnabled);
  root.style.setProperty("--player-subtitle-size", `${settings.subtitleSizePercent}%`);
  root.style.setProperty("--player-subtitle-color", settings.subtitleTextColor);
  root.style.setProperty("--player-subtitle-background-opacity", String(settings.subtitleBackgroundOpacityPercent / 100));
  configureNotificationDuration(settings.notificationDurationSeconds);
}
const nonNegativeDecimal = /^[0-9]+$/;


function useSessionNotifications(sessionID: string | undefined, refreshAccount: () => Promise<unknown>, enabled: boolean, pollIntervalSeconds: number) {
  useEffect(() => {
    if (!sessionID || !enabled) return;
    let active = true;
    let polling = false;
    const cursorKey = `rivune.notifications.${sessionID}`;
    const storedCursor = localStorage.getItem(cursorKey);
    let cursor = storedCursor !== null && nonNegativeDecimal.test(storedCursor) ? storedCursor : "0";
    const poll = async () => {
      if (polling) return;
      polling = true;
      try {
        const { notifications } = await api.sessionNotifications(cursor);
        if (!active) return;
        for (const notification of notifications) {
          await notifyInfo(notification.message, t("notifications.from", { sender: notification.senderUsername }));
          if (!active) return;
          cursor = notification.id;
          localStorage.setItem(cursorKey, cursor);
          void api.acknowledgeSessionNotification(notification.id).catch(() => undefined);
        }
      } catch (cause) {
        if (active && cause instanceof APIError && cause.status === 401) {
          await refreshAccount();
          if (!active) return;
        }
      } finally {
        polling = false;
      }
    };
    void poll();
    const timer = window.setInterval(() => { void poll(); }, pollIntervalSeconds * 1_000);
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, [enabled, pollIntervalSeconds, refreshAccount, sessionID]);
}

export default function App() {
  const { account, booting, discovery, authenticated, activeProfile, refreshAccount } = useAuth();
  const { settings, ready: settingsReady } = useRuntimeSettings(activeProfile?.id, discovery?.interfaceLanguage);
  useSessionNotifications(account?.session.id, refreshAccount, settings.notificationsEnabled, settings.notificationPollIntervalSeconds);
  const pairingApproval = window.location.pathname === "/pair";
  const [initialRoute] = useState(appRoute);
  const [view, setViewState] = useState<View>(initialRoute.view);
  const [mediaRoute, setMediaRoute] = useState<MediaRoute | null>(initialRoute.media);
  const [homeResetKey, setHomeResetKey] = useState(0);
  const [mediaDataRevisions, setMediaDataRevisions] = useState({ home: 0, library: 0 });
  const mediaRouteRef = useRef<MediaRoute | null>(initialRoute.media);
  const invokingElementRef = useRef<HTMLElement | null>(null);
  const routeSurfaceRef = useRef<HTMLDivElement>(null);
  const [maintenanceMessage, setMaintenanceMessage] = useState<string | null>(maintenanceModeMessage);
  const [checkingMaintenance, setCheckingMaintenance] = useState(false);

  function restoreOriginFocus() {
    const invokingElement = invokingElementRef.current;
    window.requestAnimationFrame(() => {
      if (invokingElement?.isConnected) {
        invokingElement.focus({ preventScroll: true });
        return;
      }
      const heading = routeSurfaceRef.current?.querySelector<HTMLElement>("h1, h2, h3");
      if (heading) {
        const previousTabIndex = heading.getAttribute("tabindex");
        heading.tabIndex = -1;
        heading.focus({ preventScroll: true });
        if (previousTabIndex === null) heading.addEventListener("blur", () => heading.removeAttribute("tabindex"), { once: true });
        return;
      }
      routeSurfaceRef.current?.focus({ preventScroll: true });
    });
  }
  function invalidateMediaOrigin(origin: View) {
    if (origin !== "home" && origin !== "library") return;
    setMediaDataRevisions((revisions) => ({ ...revisions, [origin]: revisions[origin] + 1 }));
  }

  const retryMaintenance = useCallback(async () => {
    setCheckingMaintenance(true);
    try {
      const next = await refreshAccount({ restoreActiveProfile: true });
      if (!next) return;
      if (next.maintenance.enabled) {
        setMaintenanceMessage(next.maintenance.message ?? "");
        return;
      }
      setMaintenanceMessage(null);
      clearMaintenanceMode();
    } finally {
      setCheckingMaintenance(false);
    }
  }, [refreshAccount]);

  useEffect(() => {
    const showMaintenance = (event: Event) => {
      const detail = (event as CustomEvent<{ message?: string }>).detail;
      setMaintenanceMessage(detail?.message ?? "");
    };
    window.addEventListener(MAINTENANCE_MODE_EVENT, showMaintenance);
    return () => window.removeEventListener(MAINTENANCE_MODE_EVENT, showMaintenance);
  }, []);

  useEffect(() => {
    if (maintenanceMessage === null) return;
    const timer = window.setInterval(() => { void retryMaintenance(); }, 5_000);
    return () => window.clearInterval(timer);
  }, [maintenanceMessage, retryMaintenance]);

  useEffect(() => {
    applyRuntimeSettings(settings);
  }, [settings]);

  useEffect(() => {
    const previousRestoration = window.history.scrollRestoration;
    window.history.scrollRestoration = "manual";
    if (initialRoute.canonicalURL) {
      window.history.replaceState({ ...window.history.state, rivuneOrigin: initialRoute.view }, "", initialRoute.canonicalURL);
    }
    const onRouteChange = () => {
      const next = appRoute();
      if (next.canonicalURL) {
        window.history.replaceState({ ...window.history.state, rivuneOrigin: next.view }, "", next.canonicalURL);
      }
      const previousMediaRoute = mediaRouteRef.current;
      mediaRouteRef.current = next.media;
      setViewState(next.view);
      setMediaRoute(next.media);
      restoreScroll(next.media ? 0 : typeof window.history.state?.rivuneScrollTop === "number" ? window.history.state.rivuneScrollTop : 0);
      if (previousMediaRoute !== null && next.media === null) {
        invalidateMediaOrigin(previousMediaRoute.origin);
        restoreOriginFocus();
      }
    };
    window.addEventListener("hashchange", onRouteChange);
    window.addEventListener("popstate", onRouteChange);
    if (initialRoute.media) restoreScroll(0);
    return () => {
      window.history.scrollRestoration = previousRestoration;
      window.removeEventListener("hashchange", onRouteChange);
      window.removeEventListener("popstate", onRouteChange);
    };
  }, [initialRoute.canonicalURL, initialRoute.media, initialRoute.view]);


  function setView(next: View) {
    const previousMediaRoute = mediaRouteRef.current;
    if (next === "home") setHomeResetKey((current) => current + 1);
    mediaRouteRef.current = null;
    setViewState(next);
    setMediaRoute(null);
    window.history.replaceState({ rivuneView: next, rivuneScrollTop: 0 }, "", viewURL(next));
    window.scrollTo({ top: 0, behavior: next === "home" ? "smooth" : "auto" });
    if (previousMediaRoute !== null) {
      invokingElementRef.current = null;
      invalidateMediaOrigin(previousMediaRoute.origin);
      restoreOriginFocus();
    }
  }

  function openMedia(item: MediaItem) {
    invokingElementRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    window.history.replaceState({ ...window.history.state, rivuneView: view, rivuneScrollTop: window.scrollY }, "", viewURL(view));
    window.history.pushState({ rivuneMedia: true, rivuneMediaItem: item, rivuneOrigin: view }, "", mediaRouteURL(item, view));
    const nextRoute = { item, origin: view };
    mediaRouteRef.current = nextRoute;
    setMediaRoute(nextRoute);
    window.scrollTo({ top: 0, behavior: "auto" });
  }

  function updateMediaRoute(context: MediaRouteContext) {
    if (!mediaRoute) return;
    const storedItem: MediaItem = {
      ...mediaRoute.item,
      seasonNumber: context.seasonNumber,
      episodeNumber: context.episodeNumber,
      raw: {
        ...mediaRoute.item.raw,
        continueSeasonId: context.seasonID,
        continueSeasonNumber: context.seasonNumber,
        continueEpisodeId: context.episodeID,
        continueEpisodeNumber: context.episodeNumber,
      },
    };
    window.history.replaceState({ ...window.history.state, rivuneMediaItem: storedItem }, "", mediaRouteURL(storedItem, mediaRoute.origin, context));
  }

  function openNestedMedia(item: MediaItem) {
    if (!mediaRoute) return;
    const nextRoute = { item, origin: mediaRoute.origin };
    const fromSeriesSeason = mediaRoute.item.mediaType === "series" && item.mediaType === "episode";
    window.history.pushState({ rivuneMedia: true, rivuneMediaItem: item, rivuneOrigin: mediaRoute.origin, rivuneParentSeason: fromSeriesSeason }, "", mediaRouteURL(item, mediaRoute.origin));
    mediaRouteRef.current = nextRoute;
    setMediaRoute(nextRoute);
    window.scrollTo({ top: 0, behavior: "auto" });
  }

  function returnToSeason(item: MediaItem) {
    if (!mediaRoute) return;
    if (window.history.state?.rivuneParentSeason === true) {
      window.history.back();
      return;
    }
    const nextRoute = { item, origin: mediaRoute.origin };
    window.history.replaceState({ ...window.history.state, rivuneMedia: true, rivuneMediaItem: item, rivuneOrigin: mediaRoute.origin, rivuneParentSeason: false }, "", mediaRouteURL(item, mediaRoute.origin));
    mediaRouteRef.current = nextRoute;
    setMediaRoute(nextRoute);
    window.scrollTo({ top: 0, behavior: "auto" });
  }

  function closeMedia() {
    if (!mediaRoute) return;
    if (window.history.state?.rivuneMedia) {
      window.history.back();
      return;
    }
    mediaRouteRef.current = null;
    setMediaRoute(null);
    setViewState(mediaRoute.origin);
    invalidateMediaOrigin(mediaRoute.origin);
    window.history.replaceState({ rivuneView: mediaRoute.origin, rivuneScrollTop: 0 }, "", viewURL(mediaRoute.origin));
    restoreScroll(0);
    restoreOriginFocus();
  }

  const profileMaintenanceMessage = account?.maintenance.enabled
    ? account.maintenance.message ?? ""
    : maintenanceMessage;

  if (maintenanceMessage !== null && !authenticated) return <main className="offline-page maintenance-page"><div className="offline-page__glow" /><RivuneMark /><section><span><ServerOff /></span><h1>{t("app.maintenanceTitle")}</h1><p>{maintenanceMessage || t("app.maintenanceBody")}</p><Button loading={checkingMaintenance} onClick={() => void retryMaintenance()}><RefreshCw size={18} /> {t("app.maintenanceRetry")}</Button></section></main>;
  if (booting) return <div className="boot-screen"><div className="boot-screen__aura" /><RivuneMark /><LoaderCircle className="spin" /><p>{t("app.connecting")}</p></div>;
  if (!discovery) return <main className="offline-page"><div className="offline-page__glow" /><RivuneMark /><section><span><ServerOff /></span><h1>{t("app.offlineTitle")}</h1><p>{t("app.offlineBody")}</p><Button onClick={() => window.location.reload()}><RefreshCw size={18} /> {t("app.reconnect")}</Button></section></main>;
  if (discovery.setupRequired) return <SetupPage />;
  if (!authenticated) return pairingApproval ? <LoginPage /> : <DevicePairingPage />;
  if (!activeProfile || profileMaintenanceMessage !== null && !activeProfile.canManage) return <ProfileGate maintenanceMessage={profileMaintenanceMessage} />;
  if (pairingApproval) return <PairApprovalPage />;
  if (!settingsReady) return <Shell view={view} onView={setView}><div className="view-loading"><LoaderCircle className="spin" /><span>{t("app.loadingProfileSettings")}</span></div></Shell>;

  return <Shell view={view} onView={setView}>
    <div ref={routeSurfaceRef} tabIndex={-1} className={mediaRoute ? "route-surface route-surface--hidden" : "route-surface"}>
      <Suspense fallback={<div className="view-loading"><LoaderCircle className="spin" /><span>{t("app.loadingSpace")}</span></div>}>
        {view === "home" ? <HomePage key={homeResetKey} onOpenMedia={openMedia} mediaRevision={mediaDataRevisions.home} /> : view === "search" ? <SearchPage onOpenMedia={openMedia} /> : view === "library" ? <LibraryPage onOpenMedia={openMedia} mediaRevision={mediaDataRevisions.library} /> : view === "calendar" ? <CalendarPage onOpenMedia={openMedia} /> : <AdminPage />}
      </Suspense>
    </div>
    {mediaRoute && <Suspense fallback={<div className="view-loading"><LoaderCircle className="spin" /><span>{t("app.loadingTitle")}</span></div>}><MediaDetails key={`${mediaRoute.item.mediaType}:${mediaRoute.item.id}:${mediaRoute.item.titleId ?? ""}`} item={mediaRoute.item} onClose={closeMedia} onNavigateContext={updateMediaRoute} onOpenMedia={openNestedMedia} onOpenSeason={returnToSeason} /></Suspense>}
  </Shell>;
}
