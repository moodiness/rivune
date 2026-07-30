import { AlertCircle, AlertTriangle, Check, ChevronRight, LoaderCircle, Play, Plus, X } from "lucide-react";
import { useEffect, useRef } from "react";
import { translate as t } from "./i18n";
import type { ButtonHTMLAttributes, HTMLAttributes, ReactNode } from "react";

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

export function EmptyState({ icon, title, description, action }: { icon?: ReactNode; title: string; description: string; action?: ReactNode }) {
  return <div className="empty-state">{icon}<h3>{title}</h3><p>{description}</p>{action}</div>;
}

export function Notice({ tone = "error", children }: { tone?: "error" | "success" | "info"; children: ReactNode }) {
  return <div className={`notice notice--${tone}`}>{tone === "error" ? <AlertCircle size={18} /> : <Check size={18} />}{children}</div>;
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

export function ConfirmDialog({ title, description, confirmLabel = "Confirm", loading = false, onConfirm, onCancel }: { title: string; description: string; confirmLabel?: string; loading?: boolean; onConfirm: () => void; onCancel: () => void }) {
  return <Modal onClose={loading ? () => undefined : onCancel} className="confirm-modal">
    <span className="confirm-modal__icon"><AlertTriangle size={21} /></span>
    <div className="confirm-modal__copy"><h2>{title}</h2><p>{description}</p></div>
    <div className="modal-actions"><Button type="button" variant="secondary" disabled={loading} onClick={onCancel}>Cancel</Button><Button type="button" variant="danger" loading={loading} onClick={onConfirm}>{confirmLabel}</Button></div>
  </Modal>;
}

export function Skeleton({ className = "", ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={`skeleton ${className}`} {...props} />;
}

export function MediaCard({ title, image, backdrop, subtitle, eyebrow, badge, overlay = false, shape = "poster", onClick, progress }: { title: string; image?: string; backdrop?: string; subtitle?: string; eyebrow?: string; badge?: string; overlay?: boolean; shape?: "poster" | "landscape" | "square"; onClick: () => void; progress?: number }) {
  const source = image || backdrop;
  return (
    <button className={`media-card media-card--${shape}${overlay ? " media-card--overlay" : ""}`} onClick={onClick} aria-label={t("media.open", { title })}>
      <span className="media-card__visual">
        {source ? <img src={source} alt="" loading="lazy" draggable={false} /> : <span className="media-card__fallback">{title.slice(0, 2).toUpperCase()}</span>}
        <span className="media-card__veil" />
        {badge && <span className="media-card__badge">{badge}</span>}
        {overlay && <span className="media-card__overlay-copy">{eyebrow && <small>{eyebrow}</small>}<strong>{title}</strong>{subtitle && <span>{subtitle}</span>}</span>}
        <span className="media-card__play"><Play size={20} fill="currentColor" /></span>
        {progress !== undefined && <span className="media-card__progress"><i style={{ width: `${Math.max(0, Math.min(100, progress))}%` }} /></span>}
      </span>
      {!overlay && <span className="media-card__copy"><strong>{title}</strong>{subtitle && <small>{subtitle}</small>}</span>}
    </button>
  );
}

export function SectionHeading({ eyebrow, title, action, description }: { eyebrow?: string; title: string; description?: string; action?: ReactNode }) {
  return <header className="section-heading"><div>{eyebrow && <span>{eyebrow}</span>}<h2>{title}</h2>{description && <p>{description}</p>}</div>{action}</header>;
}

export function AddTile({ label, onClick }: { label: string; onClick: () => void }) {
  return <button className="add-tile" onClick={onClick}><Plus size={24} /><span>{label}</span><ChevronRight size={18} /></button>;
}
