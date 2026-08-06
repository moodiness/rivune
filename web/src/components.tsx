import { AlertCircle, AlertTriangle, Check, ChevronDown, ChevronRight, LoaderCircle, Play, Plus, X } from "lucide-react";
import { forwardRef, useEffect, useId, useImperativeHandle, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { translate as t } from "./i18n";
import type { ButtonHTMLAttributes, CSSProperties, HTMLAttributes, KeyboardEvent as ReactKeyboardEvent, PointerEvent as ReactPointerEvent, ReactNode } from "react";

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

export type SelectOption = { value: string; label: string; disabled?: boolean };

export type SelectProps = {
  value: string;
  options: ReadonlyArray<SelectOption>;
  onChange: (value: string) => void;
  disabled?: boolean;
  required?: boolean;
  autoFocus?: boolean;
  className?: string;
  id?: string;
  name?: string;
  title?: string;
  "aria-label"?: string;
  "aria-labelledby"?: string;
  "aria-describedby"?: string;
  "aria-invalid"?: boolean | "false" | "true" | "grammar" | "spelling";
};

type SelectPosition = { left: number; top: number; width: number; maxHeight: number; above: boolean; fontFamily: string; fontSize: string; fontWeight: string; letterSpacing: string; lineHeight: string };

function selectToken(name: string): number {
  return Number.parseFloat(getComputedStyle(document.documentElement).getPropertyValue(name));
}

export const Select = forwardRef<HTMLButtonElement, SelectProps>(function Select({
  value,
  options,
  onChange,
  disabled = false,
  required = false,
  autoFocus = false,
  className = "",
  id,
  name,
  title,
  "aria-label": ariaLabel,
  "aria-labelledby": ariaLabelledBy,
  "aria-describedby": ariaDescribedBy,
  "aria-invalid": ariaInvalid,
}, forwardedRef) {
  const generatedID = useId();
  const triggerID = id ?? `${generatedID}-trigger`;
  const listboxID = `${generatedID}-listbox`;
  const triggerRef = useRef<HTMLButtonElement>(null);
  const listboxRef = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const [activeIndex, setActiveIndex] = useState(-1);
  const [position, setPosition] = useState<SelectPosition | null>(null);
  const selectedIndex = options.findIndex((option) => option.value === value);
  const selectedOption = selectedIndex >= 0 ? options[selectedIndex] : undefined;

  useImperativeHandle(forwardedRef, () => triggerRef.current as HTMLButtonElement);

  useEffect(() => {
    if (disabled && open) setOpen(false);
  }, [disabled, open]);

  useEffect(() => {
    if (!open || activeIndex < 0) return;
    document.getElementById(`${listboxID}-option-${activeIndex}`)?.scrollIntoView({ block: "nearest" });
  }, [activeIndex, listboxID, open]);

  useLayoutEffect(() => {
    if (!open) {
      setPosition(null);
      return;
    }
    const trigger = triggerRef.current;
    if (!trigger) return;

    const updatePosition = () => {
      const rect = trigger.getBoundingClientRect();
      const triggerStyle = getComputedStyle(trigger);
      const viewport = window.visualViewport;
      const viewportLeft = viewport?.offsetLeft ?? 0;
      const viewportTop = viewport?.offsetTop ?? 0;
      const viewportWidth = viewport?.width ?? window.innerWidth;
      const viewportHeight = viewport?.height ?? window.innerHeight;
      const viewportPadding = selectToken("--select-viewport-padding");
      const popupGap = selectToken("--select-popup-gap");
      const minimumWidth = selectToken("--select-popup-min-width");
      const maximumWidth = selectToken("--select-popup-max-width");
      const maximumHeight = selectToken("--select-popup-max-height");
      const viewportRight = viewportLeft + viewportWidth;
      const viewportBottom = viewportTop + viewportHeight;
      const availableBelow = viewportBottom - viewportPadding - rect.bottom - popupGap;
      const availableAbove = rect.top - viewportTop - viewportPadding - popupGap;
      const above = availableBelow < maximumHeight && availableAbove > availableBelow;
      const width = Math.min(Math.max(Math.min(rect.width, maximumWidth), minimumWidth), viewportWidth - viewportPadding * 2);
      const preferredLeft = triggerStyle.direction === "rtl" ? rect.right - width : rect.left;
      const left = Math.max(viewportLeft + viewportPadding, Math.min(preferredLeft, viewportRight - viewportPadding - width));
      setPosition({
        left,
        top: above ? rect.top - popupGap : rect.bottom + popupGap,
        width,
        maxHeight: Math.max(0, Math.min(maximumHeight, above ? availableAbove : availableBelow)),
        above,
        fontFamily: triggerStyle.fontFamily,
        fontSize: triggerStyle.fontSize,
        fontWeight: triggerStyle.fontWeight,
        letterSpacing: triggerStyle.letterSpacing,
        lineHeight: triggerStyle.lineHeight,
      });
    };

    updatePosition();
    window.addEventListener("resize", updatePosition);
    window.addEventListener("scroll", updatePosition, true);
    window.visualViewport?.addEventListener("resize", updatePosition);
    window.visualViewport?.addEventListener("scroll", updatePosition);
    return () => {
      window.removeEventListener("resize", updatePosition);
      window.removeEventListener("scroll", updatePosition, true);
      window.visualViewport?.removeEventListener("resize", updatePosition);
      window.visualViewport?.removeEventListener("scroll", updatePosition);
    };
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const closeOnOutsidePointer = (event: PointerEvent) => {
      const target = event.target as Node | null;
      if (target && !triggerRef.current?.contains(target) && !listboxRef.current?.contains(target)) setOpen(false);
    };
    document.addEventListener("pointerdown", closeOnOutsidePointer, true);
    return () => document.removeEventListener("pointerdown", closeOnOutsidePointer, true);
  }, [open]);

  function enabledIndex(from: number, direction: 1 | -1): number {
    if (options.length === 0) return -1;
    for (let offset = 0; offset < options.length; offset += 1) {
      const index = (from + direction * offset + options.length) % options.length;
      if (!options[index]?.disabled) return index;
    }
    return -1;
  }

  function openList(direction: 1 | -1 = 1) {
    if (disabled) return;
    const initial = selectedIndex >= 0 && !options[selectedIndex]?.disabled
      ? selectedIndex
      : enabledIndex(direction === 1 ? 0 : options.length - 1, direction);
    setActiveIndex(initial);
    setOpen(true);
  }

  function moveActive(direction: 1 | -1) {
    const start = activeIndex >= 0 ? activeIndex + direction : direction === 1 ? 0 : options.length - 1;
    setActiveIndex(enabledIndex(start, direction));
  }

  function choose(index: number) {
    const option = options[index];
    if (!option || option.disabled) return;
    if (option.value !== value) onChange(option.value);
    setActiveIndex(index);
    setOpen(false);
    triggerRef.current?.focus({ preventScroll: true });
  }

  const portalRoot = triggerRef.current?.closest("dialog") ?? (typeof document === "undefined" ? null : document.body);
  return <>
    <button
      ref={triggerRef}
      type="button"
      role="combobox"
      aria-autocomplete="none"
      aria-haspopup="listbox"
      aria-expanded={open}
      aria-controls={listboxID}
      aria-activedescendant={open && activeIndex >= 0 ? `${listboxID}-option-${activeIndex}` : undefined}
      aria-label={ariaLabel}
      aria-labelledby={ariaLabelledBy}
      aria-describedby={ariaDescribedBy}
      aria-invalid={ariaInvalid}
      aria-required={required || undefined}
      className={`select__trigger ${className}`}
      data-value={value}
      id={triggerID}
      name={name}
      title={title}
      disabled={disabled}
      autoFocus={autoFocus}
      data-autofocus={autoFocus || undefined}
      onClick={() => { if (open) setOpen(false); else openList(); }}
      onKeyDown={(event) => {
        if (event.altKey || event.ctrlKey || event.metaKey) return;
        if (event.key === "Escape" && open) {
          event.preventDefault();
          event.stopPropagation();
          setOpen(false);
          return;
        }
        if (event.key === "Tab") {
          setOpen(false);
          return;
        }
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          if (open) choose(activeIndex);
          else openList();
          return;
        }
        if (event.key === "ArrowDown" || event.key === "ArrowUp") {
          event.preventDefault();
          if (!open) openList(event.key === "ArrowDown" ? 1 : -1);
          else moveActive(event.key === "ArrowDown" ? 1 : -1);
          return;
        }
        if (open && (event.key === "Home" || event.key === "End")) {
          event.preventDefault();
          setActiveIndex(enabledIndex(event.key === "Home" ? 0 : options.length - 1, event.key === "Home" ? 1 : -1));
        }
      }}
    >
      <span className="select__value">{selectedOption?.label ?? value}</span>
      <ChevronDown className="select__chevron" size={16} aria-hidden="true" />
    </button>
    {open && portalRoot && createPortal(
      <div
        ref={listboxRef}
        id={listboxID}
        role="listbox"
        aria-label={ariaLabel}
        aria-labelledby={ariaLabelledBy ?? (ariaLabel ? undefined : triggerID)}
        className={`select__listbox ${position?.above ? "select__listbox--above" : ""}`}
        style={position ? { left: position.left, top: position.top, width: position.width, maxHeight: position.maxHeight, fontFamily: position.fontFamily, fontSize: position.fontSize, fontWeight: position.fontWeight, letterSpacing: position.letterSpacing, lineHeight: position.lineHeight } : undefined}
      >
        {options.map((option, index) => option.disabled && option.value === "" ? null : <div
          id={`${listboxID}-option-${index}`}
          key={option.value}
          role="option"
          aria-selected={option.value === value}
          aria-disabled={option.disabled || undefined}
          data-value={option.value}
          className={`${index === activeIndex ? "is-active" : ""} ${option.value === value ? "is-selected" : ""}`}
          onPointerMove={() => { if (!option.disabled) setActiveIndex(index); }}
          onClick={() => choose(index)}
        >
          <span>{option.label}</span>
          {option.value === value && <Check size={15} aria-hidden="true" />}
        </div>)}
      </div>,
      portalRoot,
    )}
  </>;
});

const focusableSelector = [
  "button:not(:disabled)",
  "a[href]",
  "input:not(:disabled)",
  "textarea:not(:disabled)",
  "[tabindex]:not([tabindex='-1'])",
].join(",");

function focusableElements(container: HTMLElement): HTMLElement[] {
  return Array.from(container.querySelectorAll<HTMLElement>(focusableSelector))
    .filter((element) => element.getClientRects().length > 0 && element.getAttribute("aria-disabled") !== "true");
}

export function allowsMotion(): boolean {
  if (typeof document !== "undefined" && document.documentElement.dataset.animationsEnabled === "false") return false;
  return typeof window === "undefined" || !window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

export function focusFirstElement(container: HTMLElement | null): HTMLElement | null {
  const target = container ? focusableElements(container)[0] ?? null : null;
  target?.focus({ preventScroll: true });
  target?.scrollIntoView({ block: "nearest", inline: "nearest", behavior: allowsMotion() ? "smooth" : "auto" });
  return target;
}

export function handleDirectionalFocus<T extends HTMLElement>(
  event: ReactKeyboardEvent<T>,
  { orientation, wrap = false }: { orientation: "horizontal" | "vertical"; wrap?: boolean },
): boolean {
  if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return false;
  const horizontal = orientation === "horizontal";
  const acceptedKeys = horizontal
    ? ["ArrowLeft", "ArrowRight", "Home", "End"]
    : ["ArrowUp", "ArrowDown", "Home", "End"];
  if (!acceptedKeys.includes(event.key)) return false;

  const container = event.currentTarget;
  const items = focusableElements(container);
  if (items.length === 0) return false;
  const eventTarget = event.target instanceof Element ? event.target : null;
  const current = eventTarget?.closest<HTMLElement>(focusableSelector) ?? null;
  const currentIndex = current ? items.indexOf(current) : -1;
  const rtl = horizontal && getComputedStyle(container).direction === "rtl";
  const previousKey = horizontal ? (rtl ? "ArrowRight" : "ArrowLeft") : "ArrowUp";
  let nextIndex: number;

  if (event.key === "Home") nextIndex = 0;
  else if (event.key === "End") nextIndex = items.length - 1;
  else {
    const delta = event.key === previousKey ? -1 : 1;
    nextIndex = currentIndex < 0 ? (delta > 0 ? 0 : items.length - 1) : currentIndex + delta;
    if (wrap) nextIndex = (nextIndex + items.length) % items.length;
    else nextIndex = Math.max(0, Math.min(items.length - 1, nextIndex));
  }

  event.preventDefault();
  const next = items[nextIndex];
  next.focus({ preventScroll: true });
  next.scrollIntoView({ block: "nearest", inline: "nearest", behavior: allowsMotion() ? "smooth" : "auto" });
  return true;
}

export function HorizontalDragRow({
  children,
  className = "folder-cover-row",
  onClickCapture,
  onKeyDown,
  onPointerCancel,
  onPointerDown,
  onPointerMove,
  onPointerUp,
  ...props
}: HTMLAttributes<HTMLDivElement>) {
  const drag = useRef({ active: false, moved: false, pointerID: 0, startX: 0, startTime: 0, startScrollLeft: 0, lastX: 0, lastTime: 0, velocity: 0 });
  const suppressClick = useRef(false);
  const momentumFrame = useRef(0);

  function stopMomentum() {
    if (momentumFrame.current) cancelAnimationFrame(momentumFrame.current);
    momentumFrame.current = 0;
  }

  function continueMomentum(element: HTMLDivElement, initialVelocity: number) {
    stopMomentum();
    if (typeof window !== "undefined" && window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    let velocity = Math.max(-2.5, Math.min(2.5, initialVelocity));
    let previousTime = performance.now();
    const advance = (time: number) => {
      if (typeof window !== "undefined" && window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
        momentumFrame.current = 0;
        return;
      }
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
    if (!drag.current.active || drag.current.pointerID !== event.pointerId) return;
    drag.current.active = false;
    event.currentTarget.classList.remove("is-dragging");
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
    if (drag.current.moved) {
      suppressClick.current = true;
      window.setTimeout(() => { suppressClick.current = false; }, 0);
      if (withMomentum) {
        const elapsed = Math.max(1, event.timeStamp - drag.current.startTime);
        const averageVelocity = -(event.clientX - drag.current.startX) / elapsed;
        const releaseVelocity = Math.abs(drag.current.velocity) >= 0.02
          ? drag.current.velocity * 0.75 + averageVelocity * 0.25
          : averageVelocity;
        continueMomentum(event.currentTarget, releaseVelocity);
      }
    }
  }

  useEffect(() => () => stopMomentum(), []);

  return <div
    {...props}
    className={className}
    data-directional-row=""
    onKeyDown={(event) => {
      onKeyDown?.(event);
      if (!event.defaultPrevented) handleDirectionalFocus(event, { orientation: "horizontal" });
    }}
    onPointerDown={(event) => {
      onPointerDown?.(event);
      if (event.defaultPrevented || !event.isPrimary || event.button !== 0) return;
      stopMomentum();
      drag.current = {
        active: true,
        moved: false,
        pointerID: event.pointerId,
        startX: event.clientX,
        startTime: event.timeStamp,
        startScrollLeft: event.currentTarget.scrollLeft,
        lastX: event.clientX,
        lastTime: event.timeStamp,
        velocity: 0,
      };
    }}
    onPointerMove={(event) => {
      onPointerMove?.(event);
      if (event.defaultPrevented || !drag.current.active || drag.current.pointerID !== event.pointerId) return;
      const distance = event.clientX - drag.current.startX;
      if (Math.abs(distance) < 5 && !drag.current.moved) return;
      if (!drag.current.moved) event.currentTarget.setPointerCapture(event.pointerId);
      const elapsed = Math.max(1, event.timeStamp - drag.current.lastTime);
      const averageVelocity = -(event.clientX - drag.current.startX) / Math.max(1, event.timeStamp - drag.current.startTime);
      const instantaneousVelocity = Math.max(-2.5, Math.min(2.5, -(event.clientX - drag.current.lastX) / elapsed));
      drag.current.velocity = drag.current.moved
        ? drag.current.velocity * 0.55 + instantaneousVelocity * 0.3 + averageVelocity * 0.15
        : instantaneousVelocity;
      drag.current.lastX = event.clientX;
      drag.current.lastTime = event.timeStamp;
      drag.current.moved = true;
      event.preventDefault();
      event.currentTarget.classList.add("is-dragging");
      event.currentTarget.scrollLeft = drag.current.startScrollLeft - distance;
    }}
    onPointerUp={(event) => {
      onPointerUp?.(event);
      finishDrag(event, !event.defaultPrevented);
    }}
    onPointerCancel={(event) => {
      onPointerCancel?.(event);
      finishDrag(event, false);
    }}
    onClickCapture={(event) => {
      onClickCapture?.(event);
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
  return <div className={`notice notice--${tone}`} role={tone === "error" ? "alert" : "status"} aria-live="polite" aria-atomic="true">{icon}{children}</div>;
}

export function Modal({ children, onClose, className = "" }: { children: ReactNode; onClose: () => void; className?: string }) {
  const dialogRef = useRef<HTMLDialogElement>(null);

  useEffect(() => {
    const dialog = dialogRef.current;
    if (!dialog) return;
    dialog.showModal();
    (dialog.querySelector<HTMLElement>("[data-autofocus='true'], [autofocus]") ?? dialog.querySelector<HTMLElement>("input:not(:disabled), textarea:not(:disabled), [contenteditable=true], [role=combobox]:not(:disabled)") ?? dialog.querySelector<HTMLElement>("button:not(:disabled)"))?.focus();
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

export function MediaCard({ title, image, backdrop, subtitle, badge, shape = "poster", accessibleLabel, onClick, onContextAction, progress }: { title: string; image?: string; backdrop?: string; subtitle?: string; badge?: string; shape?: "poster" | "landscape" | "square"; accessibleLabel?: string; onClick: () => void; onContextAction?: (anchor: ActionMenuAnchor) => void; progress?: number }) {
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
      aria-label={accessibleLabel || t("media.open", { title })}
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
