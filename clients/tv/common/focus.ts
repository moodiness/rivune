const focusableSelector = [
  "button:not([disabled])",
  "a[href]",
  "input:not([disabled])",
  "select:not([disabled])",
  "[tabindex]:not([tabindex='-1'])",
  "[data-tv-focusable='true']",
].join(",");

type Direction = "left" | "right" | "up" | "down";

function visible(element: HTMLElement): boolean {
  const style = window.getComputedStyle(element);
  const rect = element.getBoundingClientRect();
  return style.visibility !== "hidden" && style.display !== "none" && rect.width > 0 && rect.height > 0;
}

function candidates(): HTMLElement[] {
  return Array.from(document.querySelectorAll<HTMLElement>(focusableSelector)).filter(visible);
}

function center(element: HTMLElement) {
  const rect = element.getBoundingClientRect();
  return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 };
}

function nextFocus(current: HTMLElement, direction: Direction): HTMLElement | null {
  const origin = center(current);
  let result: HTMLElement | null = null;
  let best = Number.POSITIVE_INFINITY;
  for (const candidate of candidates()) {
    if (candidate === current) continue;
    const target = center(candidate);
    const dx = target.x - origin.x;
    const dy = target.y - origin.y;
    const primary = direction === "left" ? -dx : direction === "right" ? dx : direction === "up" ? -dy : dy;
    if (primary <= 4) continue;
    const secondary = direction === "left" || direction === "right" ? Math.abs(dy) : Math.abs(dx);
    const anglePenalty = secondary / primary;
    const distance = Math.hypot(dx, dy);
    const score = anglePenalty * 10_000 + distance;
    if (score < best) {
      best = score;
      result = candidate;
    }
  }
  return result;
}

function editingText(element: HTMLElement, direction: Direction): boolean {
  if (!(element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement)) return false;
  if (direction === "up" || direction === "down") return false;
  const start = element.selectionStart ?? 0;
  const end = element.selectionEnd ?? 0;
  if (start !== end) return true;
  return direction === "left" ? start > 0 : end < element.value.length;
}

export function focusFirst(root: ParentNode = document): void {
  window.requestAnimationFrame(() => {
    const target = Array.from(root.querySelectorAll<HTMLElement>(focusableSelector)).find(visible);
    target?.focus();
    target?.scrollIntoView({ block: "nearest", inline: "nearest" });
  });
}

export function installSpatialNavigation(onBack: () => void): () => void {
  const handler = (event: KeyboardEvent) => {
    const active = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const direction: Direction | null = event.key === "ArrowLeft" ? "left"
      : event.key === "ArrowRight" ? "right"
        : event.key === "ArrowUp" ? "up"
          : event.key === "ArrowDown" ? "down"
            : null;
    if (direction) {
      if (active && editingText(active, direction)) return;
      if (!active || !visible(active)) {
        const first = candidates()[0];
        if (!first) return;
        event.preventDefault();
        first.focus();
        first.scrollIntoView({ block: "nearest", inline: "nearest" });
        return;
      }
      const target = nextFocus(active, direction);
      if (!target) return;
      event.preventDefault();
      target.focus();
      target.scrollIntoView({ block: "nearest", inline: "nearest" });
      return;
    }
    const target = event.target;
    const textEntry = target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement;
    const back = event.key === "Escape" || event.key === "BrowserBack" ||
      (!textEntry && (event.key === "Backspace" || event.keyCode === 8)) ||
      event.keyCode === 461 || event.keyCode === 10009;
    if (back) {
      event.preventDefault();
      onBack();
    }
  };
  document.addEventListener("keydown", handler, true);
  return () => document.removeEventListener("keydown", handler, true);
}
