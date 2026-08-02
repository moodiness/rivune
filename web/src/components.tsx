import { AlertCircle, AlertTriangle, Check, ChevronRight, LoaderCircle, Play, Plus, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { translate as t } from "./i18n";
import type { ButtonHTMLAttributes, CSSProperties, HTMLAttributes, PointerEvent as ReactPointerEvent, ReactNode } from "react";

export function RivuneMark({ compact = false }: { compact?: boolean }) {
  return (
    <div className={`brand ${compact ? "brand--compact" : ""}`} aria-label="Rivune">
      <span className="brand__mark" aria-hidden="true"><span>R</span></span>
      {!compact && <span className="brand__word">Rivune</span>}
    </div>
  );
}

export function Button({ className = "", variant = "primary", loading = false, children, disabled, ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: "primary" | "secondary" | "ghost" | "danger"; loading?: boolean }) {
  return (
    <button className={`button button--${variant} ${className}`} disabled={disabled || loading} {...props}>
      {loading ? <LoaderCircle size={18} className="spin" /> : children}
    </button>
  );
}

export function IconButton({ label, children, className = "", ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { label: string; children: ReactNode }) {
  return <button className={`icon-button ${className}`} aria-label={label} title={label} {...props}>{children}</button>;
}

export function HorizontalDragRow({ children, className = "folder-cover-row", ...props }: HTMLAttributes<HTMLDivElement>) {
  const drag = useRef({ active: false, moved: false, pointerID: 0, startX: 0, startScrollLeft: 0, lastX: 0, lastTime: 0, velocity: 0 });
  const suppressClick = useRef(false);
  const momentumFrame = useRef(0);

  function stopMomentum() {
    if (momentumFrame.current) cancelAnimationFrame(momentumFrame.current);
    momentumFrame.current = 0;
  }

  function continueMomentum(element: HTMLDivElement, initialVelocity: number) {
    stopMomentum();
    let velocity = Math.max(-2.5, Math.min(2.5, initialVelocity));
    let previousTime = performance.now();
    const advance = (time: number) => {
      const elapsed = Math.min(32, time - previousTime);
      previousTime = time;
      const previousScrollLeft = element.scrollLeft;
      element.scrollLeft += velocity * elapsed;
      velocity *= Math.pow(0.94, elapsed / (1000 / 60));
      if (Math.abs(velocity) < 0.02 || element.scrollLeft === previousScrollLeft) {
        momentumFrame.current = 0;
        return;
      }
      momentumFrame.current = requestAnimationFrame(advance);
    };
    if (Math.abs(velocity) >= 0.02) momentumFrame.current = requestAnimationFrame(advance);
  }

  function finishDrag(event: ReactPointerEvent<HTMLDivElement>, withMomentum: boolean) {
    if (event.pointerType === "touch" || !drag.current.active || drag.current.pointerID !== event.pointerId) return;
    drag.current.active = false;
    event.currentTarget.classList.remove("is-dragging");
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
    if (drag.current.moved) {
      suppressClick.current = true;
      window.setTimeout(() => { suppressClick.current = false; }, 0);
      if (withMomentum) continueMomentum(event.currentTarget, drag.current.velocity);
    }
  }

  useEffect(() => () => stopMomentum(), []);

  return <div
    {...props}
    className={className}
    onPointerDown={(event) => {
      if (event.pointerType === "touch" || event.button !== 0) return;
      stopMomentum();
      drag.current = {
        active: true,
        moved: false,
        pointerID: event.pointerId,
        startX: event.clientX,
        startScrollLeft: event.currentTarget.scrollLeft,
        lastX: event.clientX,
        lastTime: event.timeStamp,
        velocity: 0,
      };
    }}
    onPointerMove={(event) => {
      if (event.pointerType === "touch" || !drag.current.active || drag.current.pointerID !== event.pointerId) return;
      const distance = event.clientX - drag.current.startX;
      if (Math.abs(distance) < 5 && !drag.current.moved) return;
      if (!drag.current.moved) event.currentTarget.setPointerCapture(event.pointerId);
      const elapsed = Math.max(1, event.timeStamp - drag.current.lastTime);
      const instantaneousVelocity = Math.max(-2.5, Math.min(2.5, -(event.clientX - drag.current.lastX) / elapsed));
      drag.current.velocity = drag.current.moved
        ? drag.current.velocity * 0.6 + instantaneousVelocity * 0.4
        : instantaneousVelocity;
      drag.current.lastX = event.clientX;
      drag.current.lastTime = event.timeStamp;
      drag.current.moved = true;
      event.preventDefault();
      event.currentTarget.classList.add("is-dragging");
      event.currentTarget.scrollLeft = drag.current.startScrollLeft - distance;
    }}
    onPointerUp={(event) => finishDrag(event, true)}
    onPointerCancel={(event) => finishDrag(event, false)}
    onClickCapture={(event) => {
      if (!suppressClick.current) return;
      event.preventDefault();
      event.stopPropagation();
      suppressClick.current = false;
    }}
  >{children}</div>;
}

export function EmptyState({ icon, title, description, action }: { icon?: ReactNode; title: string; description: string; action?: ReactNode }) {
  return <div className="empty-state">{icon}<h3>{title}</h3><p>{description}</p>{action}</div>;
}

export function Notice({ tone = "error", children }: { tone?: "error" | "success" | "info" | "warning"; children: ReactNode }) {
  const icon = tone === "success" ? <Check size={18} /> : tone === "warning" ? <AlertTriangle size={18} /> : <AlertCircle size={18} />;
  return <div className={`notice notice--${tone}`}>{icon}{children}</div>;
}

export function Modal({ children, onClose, className = "" }: { children: ReactNode; onClose: () => void; className?: string }) {
  const dialogRef = useRef<HTMLDialogElement>(null);

  useEffect(() => {
    const dialog = dialogRef.current;
    if (!dialog) return;
    dialog.showModal();
    dialog.querySelector<HTMLElement>("[autofocus]")?.focus();
    return () => dialog.close();
  }, []);

  return (
    <dialog ref={dialogRef} className="modal-layer" onCancel={(event) => { event.preventDefault(); onClose(); }} onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}>
      <section className={`modal ${className}`} role="document">
        <IconButton label={t("common.close")} className="modal__close" onClick={onClose}><X size={20} /></IconButton>
        {children}
      </section>
    </dialog>
  );
}

export function ConfirmDialog({ title, description, confirmLabel = t("common.confirm"), loading = false, onConfirm, onCancel }: { title: string; description: string; confirmLabel?: string; loading?: boolean; onConfirm: () => void; onCancel: () => void }) {
  return <Modal onClose={loading ? () => undefined : onCancel} className="confirm-modal">
    <span className="confirm-modal__icon"><AlertTriangle size={21} /></span>
    <div className="confirm-modal__copy"><h2>{title}</h2><p>{description}</p></div>
    <div className="modal-actions"><Button type="button" variant="secondary" disabled={loading} onClick={onCancel}>{t("common.cancel")}</Button><Button type="button" variant="danger" loading={loading} onClick={onConfirm}>{confirmLabel}</Button></div>
  </Modal>;
}

export type ActionMenuAnchor = { x: number; y: number; touch: boolean };

export function ActionMenu({ label, eyebrow, title, subtitle, anchor, onClose, children }: { label: string; eyebrow: string; title: string; subtitle?: string; anchor: ActionMenuAnchor; onClose: () => void; children: ReactNode }) {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const menuRef = useRef<HTMLElement>(null);
  const [position, setPosition] = useState<CSSProperties>();

  useEffect(() => {
    const dialog = dialogRef.current;
    const menu = menuRef.current;
    if (!dialog || !menu) return;
    dialog.showModal();
    if (!anchor.touch) {
      const bounds = menu.getBoundingClientRect();
      setPosition({
        left: Math.max(12, Math.min(anchor.x, window.innerWidth - bounds.width - 12)),
        top: Math.max(12, Math.min(anchor.y, window.innerHeight - bounds.height - 12)),
      });
    }
    menu.querySelector<HTMLButtonElement>("button:not(:disabled)")?.focus();
    return () => dialog.close();
  }, [anchor.touch, anchor.x, anchor.y]);

  return <dialog
    ref={dialogRef}
    className={`action-menu-layer${anchor.touch ? " action-menu-layer--touch" : ""}`}
    onCancel={(event) => { event.preventDefault(); onClose(); }}
    onPointerDown={(event) => { if (event.target === event.currentTarget) onClose(); }}
  >
    <section
      ref={menuRef}
      className="action-menu"
      role="menu"
      aria-label={label}
      style={position}
      onKeyDown={(event) => {
        if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
        const buttons = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>("[role='menuitem']:not(:disabled)"));
        if (buttons.length === 0) return;
        event.preventDefault();
        const current = buttons.indexOf(document.activeElement as HTMLButtonElement);
        const index = event.key === "Home" ? 0
          : event.key === "End" ? buttons.length - 1
            : event.key === "ArrowDown" ? (current + 1 + buttons.length) % buttons.length
              : (current - 1 + buttons.length) % buttons.length;
        buttons[index]?.focus();
      }}
    >
      <header className="action-menu__header">
        <small>{eyebrow}</small>
        <strong>{title}</strong>
        {subtitle && <span>{subtitle}</span>}
      </header>
      {children}
    </section>
  </dialog>;
}

export function Skeleton({ className = "", ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={`skeleton ${className}`} {...props} />;
}

export function MediaCard({ title, image, backdrop, subtitle, badge, shape = "poster", onClick, onContextAction, progress }: { title: string; image?: string; backdrop?: string; subtitle?: string; badge?: string; shape?: "poster" | "landscape" | "square"; onClick: () => void; onContextAction?: (anchor: ActionMenuAnchor) => void; progress?: number }) {
  const source = image || backdrop;
  const [failedSource, setFailedSource] = useState<string>();
  const usableSource = source === failedSource ? undefined : source;
  const longPressTimer = useRef(0);
  const longPressStart = useRef({ pointerID: 0, x: 0, y: 0 });
  const suppressClickUntil = useRef(0);
  const suppressContextUntil = useRef(0);

  function cancelLongPress() {
    if (longPressTimer.current) window.clearTimeout(longPressTimer.current);
    longPressTimer.current = 0;
  }

  useEffect(() => () => cancelLongPress(), []);
  return (
    <button
      type="button"
      className={`media-card media-card--${shape}`}
      onClick={(event) => {
        if (performance.now() < suppressClickUntil.current) {
          event.preventDefault();
          event.stopPropagation();
          return;
        }
        onClick();
      }}
      onContextMenu={onContextAction ? (event) => {
        event.preventDefault();
        if (performance.now() < suppressContextUntil.current) return;
        const bounds = event.currentTarget.getBoundingClientRect();
        onContextAction({
          x: event.clientX || bounds.left + bounds.width / 2,
          y: event.clientY || bounds.top + bounds.height / 2,
          touch: false,
        });
      } : undefined}
      onPointerDown={onContextAction ? (event) => {
        if ((event.pointerType !== "touch" && event.pointerType !== "pen") || event.button !== 0) return;
        cancelLongPress();
        longPressStart.current = { pointerID: event.pointerId, x: event.clientX, y: event.clientY };
        longPressTimer.current = window.setTimeout(() => {
          longPressTimer.current = 0;
          suppressClickUntil.current = performance.now() + 1_000;
          suppressContextUntil.current = performance.now() + 1_000;
          window.navigator.vibrate?.(10);
          onContextAction({ x: event.clientX, y: event.clientY, touch: true });
        }, 550);
      } : undefined}
      onPointerMove={onContextAction ? (event) => {
        if (!longPressTimer.current || longPressStart.current.pointerID !== event.pointerId) return;
        if (Math.hypot(event.clientX - longPressStart.current.x, event.clientY - longPressStart.current.y) > 10) cancelLongPress();
      } : undefined}
      onPointerUp={onContextAction ? cancelLongPress : undefined}
      onPointerCancel={onContextAction ? cancelLongPress : undefined}
      aria-label={t("media.open", { title })}
      aria-haspopup={onContextAction ? "menu" : undefined}
    >
      <span className="media-card__visual">
        {usableSource ? <img src={usableSource} alt="" loading="lazy" draggable={false} onError={() => setFailedSource(usableSource)} /> : <span className="media-card__fallback">{title.slice(0, 2).toUpperCase()}</span>}
        <span className="media-card__veil" />
        {badge && <span className="media-card__badge">{badge}</span>}
        <span className="media-card__play"><Play size={20} fill="currentColor" /></span>
        {progress !== undefined && <span className="media-card__progress"><i style={{ width: `${Math.max(0, Math.min(100, progress))}%` }} /></span>}
      </span>
      <span className="media-card__copy"><strong>{title}</strong>{subtitle && <small>{subtitle}</small>}</span>
    </button>
  );
}

export function SectionHeading({ eyebrow, title, action, description }: { eyebrow?: string; title: string; description?: string; action?: ReactNode }) {
  return <header className="section-heading"><div>{eyebrow && <span>{eyebrow}</span>}<h2>{title}</h2>{description && <p>{description}</p>}</div>{action}</header>;
}

export function AddTile({ label, onClick }: { label: string; onClick: () => void }) {
  return <button className="add-tile" onClick={onClick}><Plus size={24} /><span>{label}</span><ChevronRight size={18} /></button>;
}
