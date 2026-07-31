import { Bookmark, CalendarDays, Home, LogOut, Menu, PanelLeftClose, PanelLeftOpen, Search, Settings, Sparkles, Users, X } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { useAuth } from "./auth";
import { IconButton, RivuneMark } from "./components";
import { translate as t } from "./i18n";
import { notifyError } from "./notifications";

export type View = "home" | "search" | "library" | "calendar" | "admin";

const navItems: Array<{ id: View; label: string; icon: typeof Home }> = [
  { id: "home", label: t("nav.home"), icon: Home },
  { id: "search", label: t("nav.search"), icon: Search },
  { id: "library", label: t("nav.library"), icon: Bookmark },
  { id: "calendar", label: t("nav.calendar"), icon: CalendarDays },
];

export function Shell({ view, onView, children }: { view: View; onView: (view: View) => void; children: ReactNode }) {
  const { activeProfile, leaveProfile, logout, discovery } = useAuth();
  const [menuOpen, setMenuOpen] = useState(false);
  const [isNarrow, setIsNarrow] = useState(() => window.matchMedia("(max-width: 820px) and (orientation: portrait)").matches);
  const [sidebarCompact, setSidebarCompact] = useState(() => localStorage.getItem("rivune.sidebar.compact") === "true");
  const canManage = Boolean(activeProfile?.canManage);
  const settingsLabel = t(canManage ? "nav.administration" : "nav.preferences");

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
    void leaveProfile().catch((cause) => notifyError(cause, "The profile chooser could not be opened."));
  }

  return <div className={`app-shell ${sidebarCompact ? "is-sidebar-compact" : ""}`}>
    <aside id="main-sidebar" className={`sidebar ${menuOpen ? "is-open" : ""}`} aria-hidden={isNarrow && !menuOpen} inert={isNarrow && !menuOpen}>
      <div className="sidebar__top"><button type="button" className="sidebar__brand-toggle" onClick={toggleSidebar} aria-label={t(sidebarCompact ? "shell.expandSidebar" : "shell.collapseSidebar")} title={t(sidebarCompact ? "shell.expandSidebar" : "shell.collapseSidebar")}><RivuneMark compact={sidebarCompact && !isNarrow} /><span className="sidebar__collapse-icon">{sidebarCompact ? <PanelLeftOpen size={15} /> : <PanelLeftClose size={15} />}</span></button><IconButton label={t("shell.closeMenu")} className="sidebar__close" onClick={() => setMenuOpen(false)}><X /></IconButton></div>
      <nav aria-label={t("nav.main")}>
        <span className="sidebar__label">{t("nav.browse")}</span>
        {navItems.map((item) => { const Icon = item.icon; return <button key={item.id} className={view === item.id ? "is-active" : ""} onClick={() => onView(item.id)}><Icon size={20} /><span>{item.label}</span>{view === item.id && <i />}</button>; })}
        <><span className="sidebar__label">{t(canManage ? "shell.manage" : "shell.preferences")}</span><button className={view === "admin" ? "is-active" : ""} onClick={() => onView("admin")}><Settings size={20} /><span>{settingsLabel}</span>{view === "admin" && <i />}</button></>
      </nav>
      <div className="sidebar__footer">
        <div className="server-chip"><span className="status-dot" /><div><small>{t("shell.connectedTo")}</small><strong>{discovery?.name ?? "Rivune"}</strong></div></div>
        <button className="sidebar-profile" onClick={switchProfile}><img src={activeProfile?.avatar.url} alt="" /><div><strong>{activeProfile?.name}</strong><small>{t("shell.switchProfile")}</small></div><Users size={17} /></button>
        <button className="sidebar-signout" onClick={() => void logout().catch((cause) => notifyError(cause, "This device could not be signed out.", "Sign out failed"))}><LogOut size={17} /><span>{t("profiles.signOut")}</span></button>
      </div>
    </aside>
    {menuOpen && <button className="sidebar-scrim" onClick={() => setMenuOpen(false)} aria-label={t("shell.closeMenu")} />}
    <main className="app-main">
      <header className="topbar">
        <IconButton label={t("shell.openMenu")} className="topbar__menu" onClick={() => setMenuOpen(true)} aria-expanded={menuOpen} aria-controls="main-sidebar"><Menu /></IconButton>
        <div className="topbar__greeting"><Sparkles size={15} /><span>{t("shell.welcomeBack", { name: activeProfile?.name ?? "" })}</span></div>
        <div className="topbar__actions"><IconButton label={t("nav.search")} onClick={() => onView("search")}><Search size={19} /></IconButton><button className="topbar__profile" aria-label={t("shell.switchProfile")} onClick={switchProfile}><img src={activeProfile?.avatar.url} alt="" /></button></div>
      </header>
      <div className="view-stage">{children}</div>
    </main>
    <nav className="mobile-nav" aria-label={t("nav.mobile")}>{navItems.map((item) => { const Icon = item.icon; return <button key={item.id} className={view === item.id ? "is-active" : ""} onClick={() => onView(item.id)}><Icon size={20} /><span>{item.label}</span></button>; })}<button className={view === "admin" ? "is-active" : ""} onClick={() => onView("admin")}><Settings size={20} /><span>{t(canManage ? "nav.manage" : "nav.preferences")}</span></button></nav>
  </div>;
}
