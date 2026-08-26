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
  if (element.closest("[hidden], [inert], [aria-hidden='true']")) return false;
  const style = window.getComputedStyle(element);
  const rect = element.getBoundingClientRect();
  return style.visibility !== "hidden" && style.display !== "none" && rect.width > 0 && rect.height > 0;
}

function activeFocusRoot(): HTMLElement | null {
  const scopes = Array.from(document.querySelectorAll<HTMLElement>("[data-tv-focus-scope='true']")).filter(visible);
  return scopes[scopes.length - 1] ?? null;
}

function candidates(root: ParentNode = activeFocusRoot() ?? document): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>(focusableSelector)).filter(visible);
}

function center(element: HTMLElement) {
  const rect = element.getBoundingClientRect();
  return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 };
}

function nextFocus(current: HTMLElement, direction: Direction, root: ParentNode): HTMLElement | null {
  const origin = center(current);
  let result: HTMLElement | null = null;
  let best = Number.POSITIVE_INFINITY;
  for (const candidate of candidates(root)) {
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
    const activeRoot = activeFocusRoot();
    const targetRoot = activeRoot && (root === document || !root.contains(activeRoot)) ? activeRoot : root;
    const target = candidates(targetRoot)[0];
    target?.focus();
    target?.scrollIntoView({ block: "nearest", inline: "nearest" });
  });
}

export function installSpatialNavigation(onBack: () => void): () => void {
  const handler = (event: KeyboardEvent) => {
    const active = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const focusRoot = activeFocusRoot();
    if (event.key === "Tab" && focusRoot) {
      const scopedCandidates = candidates(focusRoot);
      if (scopedCandidates.length === 0) {
        event.preventDefault();
        return;
      }
      const currentIndex = active ? scopedCandidates.indexOf(active) : -1;
      const nextIndex = event.shiftKey
        ? (currentIndex <= 0 ? scopedCandidates.length - 1 : currentIndex - 1)
        : (currentIndex < 0 || currentIndex === scopedCandidates.length - 1 ? 0 : currentIndex + 1);
      if (currentIndex < 0 || (event.shiftKey ? currentIndex === 0 : currentIndex === scopedCandidates.length - 1)) {
        event.preventDefault();
        const target = scopedCandidates[nextIndex];
        target.focus();
        target.scrollIntoView({ block: "nearest", inline: "nearest" });
      }
      return;
    }
    const direction: Direction | null = event.key === "ArrowLeft" ? "left"
      : event.key === "ArrowRight" ? "right"
        : event.key === "ArrowUp" ? "up"
          : event.key === "ArrowDown" ? "down"
            : null;
    if (direction) {
      if (active && editingText(active, direction)) return;
      const scopedCandidates = candidates(focusRoot ?? document);
      if (!active || !visible(active) || (focusRoot && !focusRoot.contains(active))) {
        const first = scopedCandidates[0];
        if (!first) return;
        event.preventDefault();
        first.focus();
        first.scrollIntoView({ block: "nearest", inline: "nearest" });
        return;
      }
      const target = nextFocus(active, direction, focusRoot ?? document);
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
      const dismiss = activeFocusRoot()?.querySelector<HTMLButtonElement>("[data-tv-dismiss-scope='true']");
      if (dismiss) dismiss.click();
      else onBack();
    }
  };
  document.addEventListener("keydown", handler, true);
  return () => document.removeEventListener("keydown", handler, true);
}
