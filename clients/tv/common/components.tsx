import { useEffect, useId, useRef, type ButtonHTMLAttributes, type HTMLAttributes, type ReactNode, type RefObject } from "react";
import { focusFirst } from "./focus";
import { t } from "./i18n";

type InertState = { count: number; initial: boolean; inertAttribute: boolean; ariaHidden: string | null };
const inertStates = new WeakMap<HTMLElement, InertState>();

function inertOutside(scope: HTMLElement): () => void {
  const affected: HTMLElement[] = [];
  let current = scope;
  while (current.parentElement) {
    const parent = current.parentElement;
    for (const sibling of Array.from(parent.children)) {
      if (!(sibling instanceof HTMLElement) || sibling === current) continue;
      const existing = inertStates.get(sibling);
      if (existing) existing.count += 1;
      else inertStates.set(sibling, { count: 1, initial: Boolean(sibling.inert), inertAttribute: sibling.hasAttribute("inert"), ariaHidden: sibling.getAttribute("aria-hidden") });
      sibling.inert = true;
      sibling.setAttribute("inert", "");
      sibling.setAttribute("aria-hidden", "true");
      affected.push(sibling);
    }
    if (parent === document.body) break;
    current = parent;
  }
  return () => {
    for (const element of affected) {
      const state = inertStates.get(element);
      if (!state) continue;
      state.count -= 1;
      if (state.count > 0) continue;
      element.inert = state.initial;
      if (!state.inertAttribute) element.removeAttribute("inert");
      if (state.ariaHidden === null) element.removeAttribute("aria-hidden");
      else element.setAttribute("aria-hidden", state.ariaHidden);
      inertStates.delete(element);
    }
  };
}
export function useOverlayFocus(scopeRef: RefObject<HTMLElement | null>): void {
  const invokerRef = useRef<HTMLElement | null>(null);
  useEffect(() => {
    const scope = scopeRef.current;
    if (!scope) return;
    invokerRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const restoreBackground = inertOutside(scope);
    focusFirst(scope);
    return () => {
      restoreBackground();
      const invoker = invokerRef.current;
      window.requestAnimationFrame(() => {
        if (invoker?.isConnected) invoker.focus();
      });
    };
  }, [scopeRef]);
}
export type IconName = "back" | "calendar" | "check" | "close" | "home" | "library" | "pause" | "play" | "search" | "settings" | "skipBack" | "skipForward" | "source" | "user";

const paths: Record<IconName, string> = {
  back: "M15 18l-6-6 6-6",
  calendar: "M6 2v4m12-4v4M3 9h18M5 4h14a2 2 0 012 2v14H3V6a2 2 0 012-2z",
  check: "M5 12l4 4L19 6",
  close: "M6 6l12 12M18 6L6 18",
  home: "M3 11l9-8 9 8v10h-6v-6H9v6H3z",
  library: "M4 4h5v16H4zM11 4h4v16h-4zM17 4h3v16h-3z",
  pause: "M8 5v14M16 5v14",
  play: "M8 5l11 7-11 7z",
  search: "M11 4a7 7 0 100 14 7 7 0 000-14zm5 12l5 5",
  settings: "M12 8a4 4 0 100 8 4 4 0 000-8zm0-6v3m0 14v3M2 12h3m14 0h3M5 5l2 2m10 10l2 2M19 5l-2 2M7 17l-2 2",
  skipBack: "M11 7l-5 5 5 5V7zm7 0l-5 5 5 5V7z",
  skipForward: "M6 7l5 5-5 5V7zm7 0l5 5-5 5V7z",
  source: "M7 7h10v10H7zM3 3h10v4M21 21H11v-4",
  user: "M12 12a5 5 0 100-10 5 5 0 000 10zm-9 10a9 9 0 0118 0",
};

export function Icon({ name, size = 24 }: { name: IconName; size?: number }) {
  return <svg className="tv-icon" width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d={paths[name]} /></svg>;
}

export function Brand({ compact = false }: { compact?: boolean }) {
  return <div className={`tv-brand${compact ? " tv-brand--compact" : ""}`} aria-label="Rivune">
    <span className="tv-brand__mark"><span /></span>
    <span className="tv-brand__word">Rivune</span>
  </div>;
}

export function TvButton({ children, icon, tone = "default", className = "", ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { icon?: IconName; tone?: "default" | "primary" | "quiet" | "danger" }) {
  return <button type="button" className={`tv-button tv-button--${tone} ${className}`.trim()} {...props}>{icon && <Icon name={icon} size={22} />}<span>{children}</span></button>;
}

export function Screen({ children, className = "", ...props }: HTMLAttributes<HTMLElement>) {
  return <main className={`tv-screen ${className}`.trim()} {...props}>{children}</main>;
}

export function Spinner({ label = t("common.loading") }: { label?: string }) {
  return <div className="tv-spinner" role="status"><span className="tv-spinner__ring" /><span>{label}</span></div>;
}

export function EmptyState({ title, body, action }: { title: string; body?: string; action?: ReactNode }) {
  return <section className="tv-empty"><span className="tv-empty__aura" /><h2>{title}</h2>{body && <p>{body}</p>}{action}</section>;
}

export function ErrorPanel({ message, onRetry, onClose }: { message: string; onRetry?: () => void; onClose?: () => void }) {
  const scopeRef = useRef<HTMLElement>(null);
  const titleId = useId();
  const messageId = useId();
  useOverlayFocus(scopeRef);
  return <section ref={scopeRef} className="tv-error" role="alertdialog" aria-modal="true" aria-labelledby={titleId} aria-describedby={messageId} data-tv-focus-scope="true"><div><strong id={titleId}>{t("error.title")}</strong><p id={messageId}>{message}</p></div><div className="tv-actions">{onRetry && <TvButton tone="primary" data-tv-error-primary="true" onClick={onRetry}>{t("common.retry")}</TvButton>}{onClose && <TvButton data-tv-error-primary={onRetry ? undefined : "true"} data-tv-dismiss-scope="true" onClick={onClose}>{t("common.close")}</TvButton>}</div></section>;
}

export function Modal({ title, children, onClose, className = "" }: { title: string; children: ReactNode; onClose: () => void; className?: string }) {
  const scopeRef = useRef<HTMLDivElement>(null);
  const titleId = useId();
  useOverlayFocus(scopeRef);
  return <div ref={scopeRef} className="tv-modal" role="dialog" aria-modal="true" aria-labelledby={titleId} data-tv-focus-scope="true"><div className={`tv-modal__card ${className}`.trim()}><header><h2 id={titleId}>{title}</h2><TvButton icon="close" tone="quiet" aria-label={t("common.close")} data-tv-dismiss-scope="true" onClick={onClose}>{t("common.close")}</TvButton></header>{children}</div></div>;
}

export function formatTime(seconds: number): string {
  const safe = Math.max(0, Math.floor(Number.isFinite(seconds) ? seconds : 0));
  const hours = Math.floor(safe / 3600);
  const minutes = Math.floor(safe % 3600 / 60);
  const remainder = safe % 60;
  return hours > 0 ? `${hours}:${String(minutes).padStart(2, "0")}:${String(remainder).padStart(2, "0")}` : `${minutes}:${String(remainder).padStart(2, "0")}`;
}
