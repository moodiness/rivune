import type { AccessibilityPreferencesDocument } from "./types";

export const DEFAULT_ACCESSIBILITY_PREFERENCES: AccessibilityPreferencesDocument = {
  revision: 0,
  reducedMotion: "system",
  highContrast: "system",
  textScale: 100,
  captions: "system",
  audioDescription: false,
  focusIndicators: "standard",
};

function systemMatches(query: string): boolean {
  return globalThis.matchMedia?.(query).matches ?? false;
}

export function applyAccessibilityPreferences(preferences: AccessibilityPreferencesDocument): void {
  const root = document.documentElement;
  const reduced = preferences.reducedMotion === "reduce" ||
    preferences.reducedMotion === "system" && systemMatches("(prefers-reduced-motion: reduce)");
  const contrast = preferences.highContrast === "more" ||
    preferences.highContrast === "system" && systemMatches("(prefers-contrast: more)");
  root.dataset.tvReducedMotion = reduced ? "true" : "false";
  root.dataset.tvHighContrast = contrast ? "true" : "false";
  root.dataset.tvFocusIndicators = preferences.focusIndicators;
  root.style.setProperty("--tv-text-scale", String(preferences.textScale / 100));
}

export function clearAccessibilityPreferences(): void {
  const root = document.documentElement;
  delete root.dataset.tvReducedMotion;
  delete root.dataset.tvHighContrast;
  delete root.dataset.tvFocusIndicators;
  root.style.removeProperty("--tv-text-scale");
}

export function captionsPreferred(preferences: AccessibilityPreferencesDocument): boolean {
  return preferences.captions === "on";
}
