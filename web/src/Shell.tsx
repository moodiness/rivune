import { Bookmark, CalendarDays, Home, LogOut, Menu, PanelLeftClose, PanelLeftOpen, Search, Settings, Sparkles, Users, X } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { useAuth } from "./auth";
import { IconButton, RivuneMark } from "./components";
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
  const { activeProfile, leaveProfile, logout, discovery } = useAuth();
  const [menuOpen, setMenuOpen] = useState(false);
  const [isNarrow, setIsNarrow] = useState(() => window.matchMedia("(max-width: 820px) and (orientation: portrait)").matches);
  const [sidebarCompact, setSidebarCompact] = useState(() => localStorage.getItem("rivune.sidebar.compact") === "true");
  const canManage = Boolean(activeProfile?.canManage);
  const settingsLabel = t(canManage ? "nav.administration" : "nav.preferences");
  const serverName = discovery?.name ?? "Rivune";
  const formattedServerVersion = formatServerVersion(discovery?.serverVersion ?? null);
  const serverIdentity = formattedServerVersion
    ? t("shell.serverIdentity", { server: serverName, version: formattedServerVersion })
    : t("auth.connectedTo", { server: serverName });

  useEffect(() => {
    const media = window.matchMedia("(max-width: 820px) and (orientation: portrait)");
    const update = () => {
      setIsNarrow(media.matches);
      if (!media.matches) setMenuOpen(false);
    };
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, []);

  useEffect(() => {
    setMenuOpen(false);
    window.scrollTo({ top: 0, behavior: "smooth" });
  }, [view]);

  useEffect(() => {
    if (!menuOpen) return;
    const previousOverflow = document.body.style.overflow;
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setMenuOpen(false);
    };
    document.body.style.overflow = "hidden";
    window.addEventListener("keydown", closeOnEscape);
    return () => {
      document.body.style.overflow = previousOverflow;
      window.removeEventListener("keydown", closeOnEscape);
    };
  }, [menuOpen]);

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

  function toggleSidebar() {
    if (isNarrow) return;
    setSidebarCompact((current) => {
      const next = !current;
      localStorage.setItem("rivune.sidebar.compact", String(next));
      return next;
    });
  }

  function switchProfile() {
    onView("home");
    void leaveProfile().catch((cause) => notifyError(cause, t("profiles.chooserFailure")));
  }

  return <div className={`app-shell ${sidebarCompact ? "is-sidebar-compact" : ""}`}>
    <aside id="main-sidebar" className={`sidebar ${menuOpen ? "is-open" : ""}`} aria-hidden={isNarrow && !menuOpen} inert={isNarrow && !menuOpen}>
      <div className="sidebar__top"><button type="button" className="sidebar__brand-toggle" onClick={toggleSidebar} aria-label={t(sidebarCompact ? "shell.expandSidebar" : "shell.collapseSidebar")} title={t(sidebarCompact ? "shell.expandSidebar" : "shell.collapseSidebar")}><RivuneMark compact={sidebarCompact && !isNarrow} /><span className="sidebar__collapse-icon">{sidebarCompact ? <PanelLeftOpen size={15} /> : <PanelLeftClose size={15} />}</span></button><IconButton label={t("shell.closeMenu")} className="sidebar__close" onClick={() => setMenuOpen(false)}><X /></IconButton></div>
      <nav aria-label={t("nav.main")}>
        <span className="sidebar__label">{t("nav.browse")}</span>
        {navItems.map((item) => { const Icon = item.icon; const active = view === item.id; return <button key={item.id} type="button" className={active ? "is-active" : ""} aria-current={active ? "page" : undefined} onClick={() => onView(item.id)}><Icon size={20} /><span>{t(item.labelKey)}</span>{active && <i />}</button>; })}
        <><span className="sidebar__label">{t(canManage ? "shell.manage" : "shell.preferences")}</span><button type="button" className={view === "admin" ? "is-active" : ""} aria-current={view === "admin" ? "page" : undefined} onClick={() => onView("admin")}><Settings size={20} /><span>{settingsLabel}</span>{view === "admin" && <i />}</button></>
      </nav>
      <div className="sidebar__footer">
        <div className="server-chip" role="group" aria-label={serverIdentity} title={serverIdentity}>
          <span className="status-dot" aria-hidden="true" />
          <div aria-hidden="true">
            <small>{formattedServerVersion ? t("shell.connectedToVersion", { version: formattedServerVersion }) : t("shell.connectedTo")}</small>
            <strong>{serverName}</strong>
          </div>
        </div>
        <button className="sidebar-profile" onClick={switchProfile}><img src={activeProfile?.avatar.url} alt="" /><div><strong>{activeProfile?.name}</strong><small>{t("shell.switchProfile")}</small></div><Users size={17} /></button>
        <button className="sidebar-signout" onClick={() => void logout().catch((cause) => notifyError(cause, t("profiles.signOutFailure"), t("profiles.signOutFailureTitle")))}><LogOut size={17} /><span>{t("profiles.signOut")}</span></button>
      </div>
    </aside>
    {menuOpen && <button className="sidebar-scrim" onClick={() => setMenuOpen(false)} aria-label={t("shell.closeMenu")} />}
    <main className="app-main">
      <header className="topbar">
        <IconButton label={t("shell.openMenu")} className="topbar__menu" onClick={() => setMenuOpen(true)} aria-expanded={menuOpen} aria-controls="main-sidebar"><Menu /></IconButton>
        <div className="topbar__greeting"><Sparkles size={15} /><span>{t("shell.welcomeBack", { name: activeProfile?.name ?? "" })}</span></div>
        <div className="topbar__actions"><button type="button" className="topbar__search" aria-label={t("nav.search")} title={t("nav.search")} aria-keyshortcuts="/ Control+K Meta+K" onClick={() => onView("search")}><Search size={18} /><span>{t("nav.search")}</span><kbd>/</kbd></button><button type="button" className="topbar__profile" aria-label={t("shell.switchProfile")} onClick={switchProfile}><img src={activeProfile?.avatar.url} alt="" /></button></div>
      </header>
      <div className="view-stage">{children}</div>
    </main>
    <nav className="mobile-nav" aria-label={t("nav.mobile")}>{navItems.map((item) => { const Icon = item.icon; const active = view === item.id; return <button key={item.id} type="button" className={active ? "is-active" : ""} aria-current={active ? "page" : undefined} onClick={() => onView(item.id)}><Icon size={20} /><span>{t(item.labelKey)}</span></button>; })}<button type="button" className={view === "admin" ? "is-active" : ""} aria-current={view === "admin" ? "page" : undefined} onClick={() => onView("admin")}><Settings size={20} /><span>{t(canManage ? "nav.manage" : "nav.preferences")}</span></button></nav>
  </div>;
}
