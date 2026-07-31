import { LoaderCircle, RefreshCw, ServerOff } from "lucide-react";
import { lazy, Suspense, useEffect, useState } from "react";
import { useAuth } from "./auth";
import { api, APIError } from "./api";
import { Button, RivuneMark } from "./components";
import { translate as t } from "./i18n";
import { configureNotificationDuration, notifyInfo } from "./notifications";
import { Shell } from "./Shell";
import type { View } from "./Shell";
import { LoginPage, SetupPage } from "./pages/Onboarding";
import { ProfileGate } from "./pages/ProfileGate";
import { DevicePairingPage, PairApprovalPage } from "./pages/Pairing";
import type { SettingsValues } from "./types";

const validViews: Record<string, View> = { home: "home", search: "search", library: "library", calendar: "calendar", admin: "admin" };
const AdminPage = lazy(() => import("./pages/Admin").then((module) => ({ default: module.AdminPage })));
const HomePage = lazy(() => import("./pages/Explore").then((module) => ({ default: module.HomePage })));
const SearchPage = lazy(() => import("./pages/Explore").then((module) => ({ default: module.SearchPage })));
const LibraryPage = lazy(() => import("./pages/Explore").then((module) => ({ default: module.LibraryPage })));
const CalendarPage = lazy(() => import("./pages/Calendar").then((module) => ({ default: module.CalendarPage })));

type RuntimeSettings = {
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

function useRuntimeSettings(profileID: string | undefined): { settings: RuntimeSettings; ready: boolean } {
  const [loaded, setLoaded] = useState<{ profileID: string; settings: RuntimeSettings; ready: boolean }>({
    profileID: "",
    settings: defaultRuntimeSettings,
    ready: true,
  });

  useEffect(() => {
    if (!profileID) {
      api.configureMetadataLocale("auto", "auto");
      setLoaded({ profileID: "", settings: defaultRuntimeSettings, ready: true });
      return;
    }
    let active = true;
    const load = () => {
      setLoaded((current) => ({
        profileID,
        settings: current.profileID === profileID ? current.settings : defaultRuntimeSettings,
        ready: false,
      }));
      void api.effectiveSettings(profileID).then((response) => {
        if (!active) return;
        api.configureMetadataLocale(
          response.settings.metadataLanguage ?? undefined,
          response.settings.metadataRegion ?? undefined,
          response.settings.audioLanguage ?? undefined,
        );
        setLoaded({ profileID, settings: runtimeSettings(response.settings), ready: true });
      }).catch(() => {
        if (!active) return;
        api.configureMetadataLocale("auto", "auto");
        setLoaded({ profileID, settings: defaultRuntimeSettings, ready: true });
      });
    };
    load();
    window.addEventListener("rivune:settings-changed", load);
    return () => {
      active = false;
      window.removeEventListener("rivune:settings-changed", load);
    };
  }, [profileID]);

  return loaded.profileID === profileID
    ? { settings: loaded.settings, ready: loaded.ready }
    : { settings: defaultRuntimeSettings, ready: false };
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
          await notifyInfo(notification.message, `Message from ${notification.senderUsername}`);
          if (!active) return;
          cursor = notification.id;
          localStorage.setItem(cursorKey, cursor);
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
  const { settings, ready: settingsReady } = useRuntimeSettings(activeProfile?.id);
  useSessionNotifications(account?.session.id, refreshAccount, settings.notificationsEnabled, settings.notificationPollIntervalSeconds);
  const pairingApproval = window.location.pathname === "/pair";
  const [view, setViewState] = useState<View>(() => validViews[window.location.hash.slice(1)] ?? "home");
  const [homeResetKey, setHomeResetKey] = useState(0);

  useEffect(() => {
    applyRuntimeSettings(settings);
  }, [settings]);

  useEffect(() => {
    const onHashChange = () => setViewState(validViews[window.location.hash.slice(1)] ?? "home");
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, []);


  function setView(next: View) {
    if (next === "home") {
      setHomeResetKey((current) => current + 1);
      window.scrollTo({ top: 0, behavior: "smooth" });
    }
    setViewState(next);
    window.history.replaceState(null, "", `#${next}`);
  }

  if (booting) return <div className="boot-screen"><div className="boot-screen__aura" /><RivuneMark /><LoaderCircle className="spin" /><p>{t("app.connecting")}</p></div>;
  if (!discovery) return <main className="offline-page"><div className="offline-page__glow" /><RivuneMark /><section><span><ServerOff /></span><h1>{t("app.offlineTitle")}</h1><p>{t("app.offlineBody")}</p><Button onClick={() => window.location.reload()}><RefreshCw size={18} /> {t("app.reconnect")}</Button></section></main>;
  if (discovery.setupRequired) return <SetupPage />;
  if (!authenticated) return pairingApproval ? <LoginPage /> : <DevicePairingPage />;
  if (!activeProfile) return <ProfileGate />;
  if (pairingApproval) return <PairApprovalPage />;
  if (!settingsReady) return <Shell view={view} onView={setView}><div className="view-loading"><LoaderCircle className="spin" /><span>Loading profile settings…</span></div></Shell>;

  return <Shell view={view} onView={setView}><Suspense fallback={<div className="view-loading"><LoaderCircle className="spin" /><span>{t("app.loadingSpace")}</span></div>}>{view === "home" ? <HomePage key={homeResetKey} /> : view === "search" ? <SearchPage /> : view === "library" ? <LibraryPage /> : view === "calendar" ? <CalendarPage /> : <AdminPage />}</Suspense></Shell>;
}
