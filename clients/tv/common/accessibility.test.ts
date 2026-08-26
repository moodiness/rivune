import { afterEach, describe, expect, it, vi } from "vitest";
import { applyAccessibilityPreferences, captionsPreferred, clearAccessibilityPreferences } from "./accessibility";
import type { AccessibilityPreferencesDocument } from "./types";

const base: AccessibilityPreferencesDocument = {
  revision: 1, reducedMotion: "system", highContrast: "system", textScale: 100,
  captions: "system", audioDescription: false, focusIndicators: "standard",
};

afterEach(() => {
  clearAccessibilityPreferences();
  vi.unstubAllGlobals();
});

describe("profile accessibility runtime", () => {
  it("resolves system preferences and applies profile scale/focus without persistence", () => {
    vi.stubGlobal("matchMedia", vi.fn((query: string) => ({ matches: query.includes("reduced-motion"), media: query })));
    applyAccessibilityPreferences({ ...base, textScale: 130, focusIndicators: "enhanced" });
    expect(document.documentElement.dataset.tvReducedMotion).toBe("true");
    expect(document.documentElement.dataset.tvHighContrast).toBe("false");
    expect(document.documentElement.dataset.tvFocusIndicators).toBe("enhanced");
    expect(document.documentElement.style.getPropertyValue("--tv-text-scale")).toBe("1.3");
  });

  it("honors explicit reduce/no-preference and captions on/off", () => {
    vi.stubGlobal("matchMedia", vi.fn(() => ({ matches: true })));
    applyAccessibilityPreferences({ ...base, reducedMotion: "no-preference", highContrast: "standard" });
    expect(document.documentElement.dataset.tvReducedMotion).toBe("false");
    expect(document.documentElement.dataset.tvHighContrast).toBe("false");
    expect(captionsPreferred({ ...base, captions: "on" })).toBe(true);
    expect(captionsPreferred({ ...base, captions: "off" })).toBe(false);
  });
});
