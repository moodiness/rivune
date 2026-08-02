import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { api, clearSession, PROFILE_SELECTION_REQUIRED_EVENT } from "./api";
import { setLocale } from "./i18n";
import type { Account, Discovery, InterfaceLanguage, Profile } from "./types";

type AuthState = {
  discovery: Discovery | null;
  account: Account | null;
  activeProfile: Profile | null;
  booting: boolean;
  authenticated: boolean;
  profileRequestSignal: AbortSignal;
  refreshAccount: (options?: { restoreActiveProfile?: boolean }) => Promise<Account | null>;
  login: (username: string, password: string) => Promise<void>;
  completeDeviceAuthorization: (deviceCode: string) => Promise<void>;
  logout: () => Promise<void>;
  selectProfile: (profile: Profile, pin?: string) => Promise<void>;
  leaveProfile: () => Promise<void>;
  rediscover: () => Promise<void>;
  updateServerInterfaceLanguage: (language: InterfaceLanguage) => void;
};

const AuthContext = createContext<AuthState | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [discovery, setDiscovery] = useState<Discovery | null>(null);
  const [account, setAccount] = useState<Account | null>(null);
  const [booting, setBooting] = useState(true);
  const [confirmedProfileID, setConfirmedProfileID] = useState<string | null>(null);
  const authGeneration = useRef(0);
  const profileSelectionInFlight = useRef(false);
  const confirmedProfileIDRef = useRef<string | null>(null);
  const profileRequestController = useRef(new AbortController());
  const [profileRequestSignal, setProfileRequestSignal] = useState(profileRequestController.current.signal);

  const invalidateProfile = useCallback(() => {
    confirmedProfileIDRef.current = null;
    profileRequestController.current.abort();
    setConfirmedProfileID(null);
  }, []);

  const confirmProfile = useCallback((profileID: string) => {
    profileRequestController.current.abort();
    const controller = new AbortController();
    confirmedProfileIDRef.current = profileID;
    profileRequestController.current = controller;
    setProfileRequestSignal(controller.signal);
    setConfirmedProfileID(profileID);
  }, []);

  const refreshAccount = useCallback(async (options?: { restoreActiveProfile?: boolean }) => {
    const generation = authGeneration.current;
    try {
      const next = await api.me();
      if (authGeneration.current === generation) {
        setAccount(next);
        const activeProfileID = next.session.activeProfile?.id ?? null;
        if (options?.restoreActiveProfile && activeProfileID !== null) confirmProfile(activeProfileID);
        else if (confirmedProfileIDRef.current !== activeProfileID) invalidateProfile();
      }
      return next;
    } catch {
      if (authGeneration.current === generation) setAccount(null);
      return null;
    }
  }, [confirmProfile, invalidateProfile]);

  useEffect(() => {
    const requireProfileSelection = () => {
      if (profileSelectionInFlight.current) return;
      const generation = ++authGeneration.current;
      invalidateProfile();
      void api.me()
        .then((next) => {
          if (authGeneration.current !== generation) return;
          setAccount(next);
        })
        .catch(() => {
          if (authGeneration.current !== generation) return;
          setAccount(null);
        });
    };
    window.addEventListener(PROFILE_SELECTION_REQUIRED_EVENT, requireProfileSelection);
    return () => window.removeEventListener(PROFILE_SELECTION_REQUIRED_EVENT, requireProfileSelection);
  }, [invalidateProfile]);

  const rediscover = useCallback(async () => {
    const next = await api.discovery();
    setLocale(next.interfaceLanguage);
    setDiscovery(next);
  }, []);

  const updateServerInterfaceLanguage = useCallback((interfaceLanguage: InterfaceLanguage) => {
    setDiscovery((current) => current === null ? null : { ...current, interfaceLanguage });
  }, []);

  useEffect(() => {
    let active = true;
    void (async () => {
      try {
        const discovered = await api.discovery();
        if (!active) return;
        setLocale(discovered.interfaceLanguage);
        setDiscovery(discovered);
        if (!discovered.setupRequired && await api.restore()) {
          const current = await api.me();
          if (active) {
            setAccount(current);
            if (current.session.activeProfile?.id) confirmProfile(current.session.activeProfile.id);
            else invalidateProfile();
          }
        }
      } catch {
        if (active) setAccount(null);
      } finally {
        if (active) setBooting(false);
      }
    })();
    return () => { active = false; };
  }, [confirmProfile, invalidateProfile]);

  const login = useCallback(async (username: string, password: string) => {
    const generation = ++authGeneration.current;
    invalidateProfile();
    await api.login(username, password);
    const next = await api.me();
    if (authGeneration.current !== generation) return;
    setAccount(next);
  }, [invalidateProfile]);

  const logout = useCallback(async () => {
    const generation = ++authGeneration.current;
    invalidateProfile();
    profileSelectionInFlight.current = false;
    await api.logout();
    if (authGeneration.current !== generation) return;
    setAccount(null);
  }, [invalidateProfile]);

  const selectProfile = useCallback(async (profile: Profile, pin?: string) => {
    const generation = ++authGeneration.current;
    invalidateProfile();
    profileSelectionInFlight.current = true;
    try {
      await api.selectProfile(profile.id, pin);
      const next = await api.me();
      if (authGeneration.current !== generation) return;
      setAccount(next);
      if (next.session.activeProfile?.id === profile.id) confirmProfile(profile.id);
    } finally {
      if (authGeneration.current === generation) profileSelectionInFlight.current = false;
    }
  }, [confirmProfile, invalidateProfile]);

  const leaveProfile = useCallback(async () => {
    const generation = ++authGeneration.current;
    invalidateProfile();
    await api.clearProfile();
    const next = await api.me();
    if (authGeneration.current !== generation) return;
    setAccount(next);
  }, [invalidateProfile]);

  const completeDeviceAuthorization = useCallback(async (deviceCode: string) => {
    const generation = ++authGeneration.current;
    invalidateProfile();
    await api.exchangeDeviceAuthorization(deviceCode);
    const next = await api.me();
    if (authGeneration.current !== generation) return;
    setAccount(next);
  }, [invalidateProfile]);

  const activeProfile = confirmedProfileID !== null && account?.session.activeProfile?.id === confirmedProfileID
    ? account.profiles.find((profile) => profile.id === confirmedProfileID) ?? null
    : null;
  const value = useMemo<AuthState>(() => ({
    discovery,
    account,
    activeProfile,
    booting,
    authenticated: account !== null,
    profileRequestSignal,
    refreshAccount,
    completeDeviceAuthorization,
    login,
    logout,
    selectProfile,
    leaveProfile,
    rediscover,
    updateServerInterfaceLanguage,
  }), [account, activeProfile, booting, completeDeviceAuthorization, discovery, leaveProfile, login, logout, profileRequestSignal, rediscover, refreshAccount, selectProfile, updateServerInterfaceLanguage]);

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth() {
  const value = useContext(AuthContext);
  if (!value) throw new Error("useAuth must be used inside AuthProvider");
  return value;
}

export function resetStoredSession() {
  clearSession();
}
