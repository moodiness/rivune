import { Bookmark, CalendarDays, Home, LogOut, PanelLeftClose, PanelLeftOpen, RotateCcw, Search, Settings, Sparkles, Users } from "lucide-react";
import { useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react";
import { useAuth } from "./auth";
import { APIError } from "./api";
import { allowsMotion, focusFirstElement, handleDirectionalFocus, RivuneMark } from "./components";
import { translate as t } from "./i18n";
import { notifyError } from "./notifications";

export type View = "home" | "search" | "library" | "calendar" | "admin";

const navItems: Array<{ id: View; labelKey: "nav.home" | "nav.search" | "nav.library" | "nav.calendar"; icon: typeof Home }> = [
  { id: "home", labelKey: "nav.home", icon: Home },
  { id: "search", labelKey: "nav.search", icon: Search },
  { id: "library", labelKey: "nav.library", icon: Bookmark },
  { id: "calendar", labelKey: "nav.calendar", icon: CalendarDays },
];

function formatServerVersion(version: string | null): string | null {
  const normalized = version?.trim();
  if (!normalized) return null;
  if (/^v/i.test(normalized) || !/^\d/.test(normalized)) return normalized;
  return `v${normalized}`;
}

export function Shell({ view, onView, children }: { view: View; onView: (view: View) => void; children: ReactNode }) {
  const { activeProfile, leaveProfile, logout, discovery, exitDemo, mode, resetDemo } = useAuth();
  const [sidebarCompact, setSidebarCompact] = useState(() => localStorage.getItem("rivune.sidebar.compact") === "true");
  const [demoAction, setDemoAction] = useState<"reset" | "exit" | null>(null);
  const [mobileAccountOpen, setMobileAccountOpen] = useState(false);
  const sidebarRef = useRef<HTMLElement>(null);
  const viewStageRef = useRef<HTMLDivElement>(null);
  const mobileAccountButtonRef = useRef<HTMLButtonElement>(null);
  const mobileAccountPanelRef = useRef<HTMLDivElement>(null);
  const lastSidebarFocus = useRef<HTMLElement | null>(null);
  const lastContentFocus = useRef<HTMLElement | null>(null);
  const isDemo = mode === "demo";
  const settingsLabel = t("admin.tabs.settings.label");
  const profileRole = t(activeProfile?.isChild ? "profiles.child" : activeProfile?.canManage ? "profiles.admin" : "profiles.standard");
  const serverName = discovery?.name ?? "Rivune";
  const formattedServerVersion = formatServerVersion(discovery?.serverVersion ?? null);
  const serverIdentity = formattedServerVersion
    ? t("shell.serverIdentity", { server: serverName, version: formattedServerVersion })
    : t("auth.connectedTo", { server: serverName });

  useEffect(() => {
    setMobileAccountOpen(false);
    window.scrollTo({ top: 0, behavior: allowsMotion() ? "smooth" : "auto" });
  }, [view]);

  useEffect(() => {
    const openSearch = (event: KeyboardEvent) => {
      const target = event.target instanceof HTMLElement ? event.target : null;
      if (target?.closest("input, textarea, select, [contenteditable=true]")) return;
      const slash = event.key === "/" && !event.altKey && !event.ctrlKey && !event.metaKey;
      const command = event.key.toLowerCase() === "k" && (event.ctrlKey || event.metaKey) && !event.altKey;
      if (!slash && !command) return;
      event.preventDefault();
      if (view !== "search") onView("search");
      window.requestAnimationFrame(() => document.querySelector<HTMLInputElement>(".search-box input")?.focus());
    };
    window.addEventListener("keydown", openSearch);
    return () => window.removeEventListener("keydown", openSearch);
  }, [onView, view]);

  useEffect(() => {
    if (!mobileAccountOpen) return;
    const dismiss = (event: PointerEvent) => {
      const target = event.target instanceof Node ? event.target : null;
      if (!target || mobileAccountButtonRef.current?.contains(target) || mobileAccountPanelRef.current?.contains(target)) return;
      setMobileAccountOpen(false);
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      setMobileAccountOpen(false);
      mobileAccountButtonRef.current?.focus();
    };
    document.addEventListener("pointerdown", dismiss);
    document.addEventListener("keydown", closeOnEscape);
    return () => {
      document.removeEventListener("pointerdown", dismiss);
      document.removeEventListener("keydown", closeOnEscape);
    };
  }, [mobileAccountOpen]);

  function toggleSidebar() {
    setSidebarCompact((current) => {
      const next = !current;
      localStorage.setItem("rivune.sidebar.compact", String(next));
      return next;
    });
  }

  function switchProfile() {
    setMobileAccountOpen(false);
    onView("home");
    void leaveProfile().catch((cause) => notifyError(cause, t("profiles.chooserFailure")));
  }

  async function runDemoAction(action: "reset" | "exit") {
    setDemoAction(action);
    try {
      if (action === "reset") await resetDemo();
      else await exitDemo();
    } catch (cause) {
      if (!(cause instanceof APIError && cause.code === "demo_unavailable")) {
        notifyError(cause, t(action === "reset" ? "demo.resetFailure" : "demo.exitFailure"));
      }
    } finally {
      setDemoAction(null);
    }
  }

  function focusContent(): boolean {
    const remembered = lastContentFocus.current;
    if (remembered?.isConnected && remembered.getClientRects().length > 0 && viewStageRef.current?.contains(remembered)) {
      remembered.focus({ preventScroll: true });
      return true;
    }
    return Boolean(focusFirstElement(viewStageRef.current));
  }

  function focusSidebar(): boolean {
    const sidebar = sidebarRef.current;
    if (!sidebar || sidebar.getClientRects().length === 0) return false;
    const remembered = lastSidebarFocus.current;
    const target = remembered?.isConnected && remembered.getClientRects().length > 0 && sidebar.contains(remembered)
      ? remembered
      : sidebar.querySelector<HTMLElement>('nav button[aria-current="page"]');
    if (target) target.focus({ preventScroll: true });
    else return Boolean(focusFirstElement(sidebar));
    return true;
  }

  function isInlineArrow(event: ReactKeyboardEvent<HTMLElement>, edge: "forward" | "backward") {
    const rtl = getComputedStyle(event.currentTarget).direction === "rtl";
    const forward = rtl ? "ArrowLeft" : "ArrowRight";
    return event.key === (edge === "forward" ? forward : rtl ? "ArrowRight" : "ArrowLeft");
  }

  function handleSidebarKeyDown(event: ReactKeyboardEvent<HTMLElement>) {
    if (handleDirectionalFocus(event, { orientation: "vertical" })) return;
    if (!event.altKey && !event.ctrlKey && !event.metaKey && !event.shiftKey && isInlineArrow(event, "forward") && focusContent()) {
      event.preventDefault();
    }
  }

  function handleContentKeyDown(event: ReactKeyboardEvent<HTMLElement>) {
    if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
    const target = event.target instanceof HTMLElement ? event.target : null;
    if (target?.closest("input, textarea, select, [contenteditable=true]")) return;
    if (!isInlineArrow(event, "backward")) return;
    if (event.defaultPrevented) {
      const row = target?.closest<HTMLElement>("[data-directional-row]");
      const current = target?.closest<HTMLElement>("button, a[href], [tabindex]");
      const first = row?.querySelector<HTMLElement>(":scope > button:not(:disabled), :scope > a[href], :scope > [tabindex]:not([tabindex='-1'])");
      if (!row || !current || current !== first) return;
    }
    if (!focusSidebar()) return;
    event.preventDefault();
  }

  const signOut = () => {
    setMobileAccountOpen(false);
    void logout().catch((cause) => notifyError(cause, t("profiles.signOutFailure"), t("profiles.signOutFailureTitle")));
  };

  return <div className={`app-shell ${sidebarCompact ? "is-sidebar-compact" : ""}`}>
    <aside
      id="main-sidebar"
      className="sidebar"
      ref={sidebarRef}
      onFocusCapture={(event) => { if (event.target instanceof HTMLElement) lastSidebarFocus.current = event.target; }}
      onKeyDown={handleSidebarKeyDown}
    >
      <div className="sidebar__top">
        <button type="button" className="sidebar__brand-toggle" onClick={toggleSidebar} aria-label={t(sidebarCompact ? "shell.expandSidebar" : "shell.collapseSidebar")} title={t(sidebarCompact ? "shell.expandSidebar" : "shell.collapseSidebar")}>
          <RivuneMark compact={sidebarCompact} />
          <span className="sidebar__collapse-icon">{sidebarCompact ? <PanelLeftOpen size={15} /> : <PanelLeftClose size={15} />}</span>
        </button>
      </div>
      <nav aria-label={t("nav.main")}>
        {navItems.map((item) => {
          const Icon = item.icon;
          const active = view === item.id;
          return <button key={item.id} type="button" className={active ? "is-active" : ""} aria-label={t(item.labelKey)} aria-current={active ? "page" : undefined} title={t(item.labelKey)} onClick={() => onView(item.id)}><Icon size={20} /><span>{t(item.labelKey)}</span></button>;
        })}
        {!isDemo && <button type="button" className={view === "admin" ? "is-active" : ""} aria-label={settingsLabel} aria-current={view === "admin" ? "page" : undefined} title={settingsLabel} onClick={() => onView("admin")}><Settings size={20} /><span>{settingsLabel}</span></button>}
      </nav>
      <div className="sidebar__footer">
        {isDemo && <>
          <div className="demo-badge" title={t("demo.syntheticContent")}><Sparkles size={14} /><span>{t("demo.badge")}</span></div>
          <small className="demo-disclaimer">{t("demo.syntheticContent")}</small>
        </>}
        <div className="server-chip" role="group" aria-label={serverIdentity} title={serverIdentity}>
          <span className="status-dot" aria-hidden="true" />
          <div aria-hidden="true">
            <small>{formattedServerVersion ? t("shell.connectedToVersion", { version: formattedServerVersion }) : t("shell.connectedTo")}</small>
            <strong>{serverName}</strong>
          </div>
        </div>
        <button type="button" className="sidebar-profile" aria-label={t("shell.switchProfile")} title={t("shell.switchProfile")} onClick={switchProfile}><img src={activeProfile?.avatar.url} alt="" /><div><strong>{activeProfile?.name}</strong><small>{t("shell.switchProfile")}</small></div><Users size={17} /></button>
        {isDemo ? <div className="demo-actions">
          <button type="button" disabled={demoAction !== null} aria-label={t("demo.reset")} title={t("demo.reset")} onClick={() => void runDemoAction("reset")}><RotateCcw className={demoAction === "reset" ? "spin" : ""} size={17} /><span>{t("demo.reset")}</span></button>
          <button type="button" disabled={demoAction !== null} aria-label={t("demo.exit")} title={t("demo.exit")} onClick={() => void runDemoAction("exit")}><LogOut size={17} /><span>{t("demo.exit")}</span></button>
        </div> : <button type="button" className="sidebar-signout" aria-label={t("profiles.signOut")} title={t("profiles.signOut")} onClick={signOut}><LogOut size={17} /><span>{t("profiles.signOut")}</span></button>}
      </div>
    </aside>
    <main
      className="app-main"
      onFocusCapture={(event) => { if (event.target instanceof HTMLElement) lastContentFocus.current = event.target; }}
      onKeyDown={handleContentKeyDown}
    >
      <div className="view-stage" ref={viewStageRef}>{children}</div>
    </main>
    <div className="mobile-shell-controls">
      <button
        type="button"
        className="mobile-account-toggle"
        ref={mobileAccountButtonRef}
        aria-expanded={mobileAccountOpen}
        aria-haspopup="true"
        aria-controls="mobile-account-panel"
        onClick={() => setMobileAccountOpen((open) => !open)}
      >
        <img src={activeProfile?.avatar.url} alt="" />
        <span className="mobile-account-identity"><strong>{activeProfile?.name ?? t("shell.switchProfile")}</strong>{" "}<small>{profileRole}</small></span>
      </button>
      {isDemo && <div className="demo-mobile-controls" aria-label={t("demo.badge")} title={t("demo.syntheticContent")}>
        <span><Sparkles size={14} /> {t("demo.badge")}</span>
        <button type="button" disabled={demoAction !== null} onClick={() => void runDemoAction("reset")} aria-label={t("demo.reset")}><RotateCcw className={demoAction === "reset" ? "spin" : ""} size={16} /></button>
        <button type="button" disabled={demoAction !== null} onClick={() => void runDemoAction("exit")} aria-label={t("demo.exit")}><LogOut size={16} /></button>
      </div>}
      {mobileAccountOpen && <div
        id="mobile-account-panel"
        className="mobile-account-panel"
        ref={mobileAccountPanelRef}
        role="group"
        aria-label={activeProfile?.name ?? t("shell.switchProfile")}
        onKeyDown={(event) => { handleDirectionalFocus(event, { orientation: "vertical" }); }}
      >
        <div className="server-chip" role="group" aria-label={serverIdentity} title={serverIdentity}>
          <span className="status-dot" aria-hidden="true" />
          <div aria-hidden="true">
            <small>{formattedServerVersion ? t("shell.connectedToVersion", { version: formattedServerVersion }) : t("shell.connectedTo")}</small>
            <strong>{serverName}</strong>
          </div>
        </div>
        <button type="button" className="sidebar-profile" aria-label={t("shell.switchProfile")} onClick={switchProfile}><img src={activeProfile?.avatar.url} alt="" /><div><strong>{activeProfile?.name}</strong><small>{t("shell.switchProfile")}</small></div><Users size={17} /></button>
        {!isDemo && <button type="button" className="sidebar-signout" aria-label={t("profiles.signOut")} onClick={signOut}><LogOut size={17} /><span>{t("profiles.signOut")}</span></button>}
      </div>}
    </div>
    <nav
      className="mobile-nav"
      aria-label={t("nav.mobile")}
      onKeyDown={(event) => {
        if (handleDirectionalFocus(event, { orientation: "horizontal" })) return;
        if (event.key === "ArrowUp") {
          event.preventDefault();
          focusContent();
        }
      }}
    >
      {navItems.map((item) => {
        const Icon = item.icon;
        const active = view === item.id;
        return <button key={item.id} type="button" className={active ? "is-active" : ""} aria-current={active ? "page" : undefined} onClick={() => onView(item.id)}><Icon size={20} /><span>{t(item.labelKey)}</span></button>;
      })}
      {!isDemo && <button type="button" className={view === "admin" ? "is-active" : ""} aria-current={view === "admin" ? "page" : undefined} onClick={() => onView("admin")}><Settings size={20} /><span>{settingsLabel}</span></button>}
    </nav>
  </div>;
}
