import { createContext, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useState, type ReactNode } from "react";
import { api, APIError } from "./api";
import { translate as t } from "./i18n";
import type { AccessibilityDocument, AccessibilityPreferences } from "./types";

export const defaultAccessibilityPreferences: AccessibilityPreferences = {
  reducedMotion: "system",
  highContrast: "system",
  textScale: 100,
  captions: "system",
  audioDescription: false,
  focusIndicators: "standard",
};

type AccessibilityStatus = "loading" | "ready" | "saving" | "conflict" | "error";
type AccessibilityContextValue = {
  document: AccessibilityDocument;
  status: AccessibilityStatus;
  error: string;
  save: (preferences: AccessibilityPreferences) => Promise<boolean>;
  reload: () => Promise<void>;
};

const AccessibilityContext = createContext<AccessibilityContextValue | null>(null);

export function applyAccessibilityPreferences(preferences: AccessibilityPreferences): () => void {
  const root = document.documentElement;
  const motion = window.matchMedia("(prefers-reduced-motion: reduce)");
  const contrast = window.matchMedia("(prefers-contrast: more)");
  const applySystemValues = () => {
    root.dataset.reducedMotion = preferences.reducedMotion === "system"
      ? motion.matches ? "reduce" : "no-preference"
      : preferences.reducedMotion;
    root.dataset.highContrast = preferences.highContrast === "system"
      ? contrast.matches ? "more" : "standard"
      : preferences.highContrast;
  };
  applySystemValues();
  root.dataset.textScale = String(preferences.textScale);
  root.dataset.captions = preferences.captions;
  root.dataset.audioDescription = String(preferences.audioDescription);
  root.dataset.focusIndicators = preferences.focusIndicators;
  root.style.setProperty("--text-scale", String(preferences.textScale / 100));
  if (preferences.reducedMotion === "system") motion.addEventListener("change", applySystemValues);
  if (preferences.highContrast === "system") contrast.addEventListener("change", applySystemValues);
  return () => {
    motion.removeEventListener("change", applySystemValues);
    contrast.removeEventListener("change", applySystemValues);
  };
}

export function AccessibilityProvider({ profileId, children }: { profileId?: string; children: ReactNode }) {
  const [state, setState] = useState<{ profileId: string; document: AccessibilityDocument; status: AccessibilityStatus; error: string }>({
    profileId: "",
    document: { revision: 0, ...defaultAccessibilityPreferences },
    status: "ready",
    error: "",
  });

  const load = useCallback(async () => {
    if (!profileId) {
      setState({ profileId: "", document: { revision: 0, ...defaultAccessibilityPreferences }, status: "ready", error: "" });
      return;
    }
    setState((current) => ({
      profileId,
      document: current.profileId === profileId ? current.document : { revision: 0, ...defaultAccessibilityPreferences },
      status: "loading",
      error: "",
    }));
    try {
      const loaded = await api.accessibilityPreferences(profileId);
      setState((current) => current.profileId === profileId ? { profileId, document: loaded, status: "ready", error: "" } : current);
    } catch (cause) {
      const message = cause instanceof Error ? cause.message : t("accessibility.error.load");
      setState((current) => current.profileId === profileId ? { ...current, status: "error", error: message } : current);
    }
  }, [profileId]);

  useEffect(() => { void load(); }, [load]);

  const current = state.profileId === (profileId ?? "")
    ? state
    : { profileId: profileId ?? "", document: { revision: 0, ...defaultAccessibilityPreferences }, status: profileId ? "loading" as const : "ready" as const, error: "" };

  useLayoutEffect(() => applyAccessibilityPreferences(current.document), [current.document]);

  const save = useCallback(async (preferences: AccessibilityPreferences): Promise<boolean> => {
    if (!profileId) return false;
    const revision = state.profileId === profileId ? state.document.revision : 0;
    setState((value) => value.profileId === profileId ? { ...value, status: "saving", error: "" } : value);
    try {
      const updated = await api.updateAccessibilityPreferences(profileId, { revision, ...preferences });
      setState((value) => value.profileId === profileId ? { profileId, document: updated, status: "ready", error: "" } : value);
      return true;
    } catch (cause) {
      const conflict = cause instanceof APIError && cause.status === 409 && cause.code === "accessibility_preferences_conflict";
      const message = conflict
        ? t("accessibility.error.conflict")
        : cause instanceof Error ? cause.message : t("accessibility.error.save");
      setState((value) => value.profileId === profileId ? { ...value, status: conflict ? "conflict" : "error", error: message } : value);
      return false;
    }
  }, [profileId, state.document.revision, state.profileId]);

  const value = useMemo<AccessibilityContextValue>(() => ({
    document: current.document,
    status: current.status,
    error: current.error,
    save,
    reload: load,
  }), [current.document, current.error, current.status, load, save]);

  return <AccessibilityContext.Provider value={value}>{children}</AccessibilityContext.Provider>;
}

export function useAccessibility(): AccessibilityContextValue {
  const value = useContext(AccessibilityContext);
  if (!value) throw new Error("useAccessibility must be used inside AccessibilityProvider");
  return value;
}
