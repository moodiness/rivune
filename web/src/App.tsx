import { LoaderCircle, RefreshCw, ServerOff } from "lucide-react";
import { lazy, Suspense, useEffect, useState } from "react";
import { useAuth } from "./auth";
import { api, APIError } from "./api";
import { Button, RivuneMark } from "./components";
import { translate as t } from "./i18n";
import { notifyInfo } from "./notifications";
import { Shell } from "./Shell";
import type { View } from "./Shell";
import { LoginPage, SetupPage } from "./pages/Onboarding";
import { ProfileGate } from "./pages/ProfileGate";
import { DevicePairingPage, PairApprovalPage } from "./pages/Pairing";

const validViews: Record<string, View> = { home: "home", search: "search", library: "library", admin: "admin" };
const AdminPage = lazy(() => import("./pages/Admin").then((module) => ({ default: module.AdminPage })));
const HomePage = lazy(() => import("./pages/Explore").then((module) => ({ default: module.HomePage })));
const SearchPage = lazy(() => import("./pages/Explore").then((module) => ({ default: module.SearchPage })));
const LibraryPage = lazy(() => import("./pages/Explore").then((module) => ({ default: module.LibraryPage })));

const notificationPollIntervalMilliseconds = 5_000;
const nonNegativeDecimal = /^[0-9]+$/;

function useSessionNotifications(sessionID: string | undefined, refreshAccount: () => Promise<unknown>) {
  useEffect(() => {
    if (!sessionID) return;
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
    const timer = window.setInterval(() => { void poll(); }, notificationPollIntervalMilliseconds);
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, [refreshAccount, sessionID]);
}

export default function App() {
  const { account, booting, discovery, authenticated, activeProfile, refreshAccount } = useAuth();
  useSessionNotifications(account?.session.id, refreshAccount);
  const pairingApproval = window.location.pathname === "/pair";
  const [view, setViewState] = useState<View>(() => validViews[window.location.hash.slice(1)] ?? "home");
  const [homeResetKey, setHomeResetKey] = useState(0);

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

  return <Shell view={view} onView={setView}><Suspense fallback={<div className="view-loading"><LoaderCircle className="spin" /><span>{t("app.loadingSpace")}</span></div>}>{view === "home" ? <HomePage key={homeResetKey} /> : view === "search" ? <SearchPage /> : view === "library" ? <LibraryPage /> : <AdminPage />}</Suspense></Shell>;
}
