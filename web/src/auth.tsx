import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { api, clearSession, PROFILE_SELECTION_REQUIRED_EVENT } from "./api";
import type { Account, Discovery, Profile } from "./types";

type AuthState = {
  discovery: Discovery | null;
  account: Account | null;
  activeProfile: Profile | null;
  booting: boolean;
  authenticated: boolean;
  refreshAccount: () => Promise<Account | null>;
  login: (username: string, password: string) => Promise<void>;
  completeDeviceAuthorization: (deviceCode: string) => Promise<void>;
  logout: () => Promise<void>;
  selectProfile: (profile: Profile, pin?: string) => Promise<void>;
  leaveProfile: () => Promise<void>;
  rediscover: () => Promise<void>;
};

const AuthContext = createContext<AuthState | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [discovery, setDiscovery] = useState<Discovery | null>(null);
  const [account, setAccount] = useState<Account | null>(null);
  const [booting, setBooting] = useState(true);
  const [profileConfirmed, setProfileConfirmed] = useState(false);
  const authGeneration = useRef(0);
  const profileSelectionInFlight = useRef(false);

  const refreshAccount = useCallback(async () => {
    const generation = authGeneration.current;
    try {
      const next = await api.me();
      if (authGeneration.current === generation) setAccount(next);
      return next;
    } catch {
      if (authGeneration.current === generation) setAccount(null);
      return null;
    }
  }, []);

  useEffect(() => {
    const requireProfileSelection = () => {
      if (profileSelectionInFlight.current) return;
      const generation = authGeneration.current;
      void api.me()
        .then((next) => {
          if (authGeneration.current !== generation) return;
          setAccount(next);
          if (!next.session.activeProfile) setProfileConfirmed(false);
        })
        .catch(() => {
          if (authGeneration.current !== generation) return;
          setAccount(null);
          setProfileConfirmed(false);
        });
    };
    window.addEventListener(PROFILE_SELECTION_REQUIRED_EVENT, requireProfileSelection);
    return () => window.removeEventListener(PROFILE_SELECTION_REQUIRED_EVENT, requireProfileSelection);
  }, [refreshAccount]);

  const rediscover = useCallback(async () => {
    const next = await api.discovery();
    setDiscovery(next);
  }, []);

  useEffect(() => {
    let active = true;
    void (async () => {
      try {
        const discovered = await api.discovery();
        if (!active) return;
        setDiscovery(discovered);
        if (!discovered.setupRequired && await api.restore()) {
          const current = await api.me();
          if (active) setAccount(current);
        }
      } catch {
        if (active) setAccount(null);
      } finally {
        if (active) setBooting(false);
      }
    })();
    return () => { active = false; };
  }, []);

  const login = useCallback(async (username: string, password: string) => {
    const generation = ++authGeneration.current;
    await api.login(username, password);
    const next = await api.me();
    if (authGeneration.current !== generation) return;
    setProfileConfirmed(false);
    setAccount(next);
  }, []);

  const logout = useCallback(async () => {
    const generation = ++authGeneration.current;
    await api.logout();
    if (authGeneration.current !== generation) return;
    setAccount(null);
    setProfileConfirmed(false);
  }, []);

  const selectProfile = useCallback(async (profile: Profile, pin?: string) => {
    const generation = ++authGeneration.current;
    profileSelectionInFlight.current = true;
    try {
      await api.selectProfile(profile.id, pin);
      const next = await api.me();
      if (authGeneration.current !== generation) return;
      setAccount(next);
      setProfileConfirmed(next.session.activeProfile?.id === profile.id);
    } finally {
      if (authGeneration.current === generation) profileSelectionInFlight.current = false;
    }
  }, []);

  const leaveProfile = useCallback(async () => {
    const generation = ++authGeneration.current;
    await api.clearProfile();
    const next = await api.me();
    if (authGeneration.current !== generation) return;
    setProfileConfirmed(false);
    setAccount(next);
  }, []);

  const completeDeviceAuthorization = useCallback(async (deviceCode: string) => {
    const generation = ++authGeneration.current;
    await api.exchangeDeviceAuthorization(deviceCode);
    const next = await api.me();
    if (authGeneration.current !== generation) return;
    setProfileConfirmed(false);
    setAccount(next);
  }, []);

  const activeProfile = profileConfirmed
    ? account?.profiles.find((profile) => profile.id === account.session.activeProfile?.id) ?? null
    : null;
  const value = useMemo<AuthState>(() => ({
    discovery,
    account,
    activeProfile,
    booting,
    authenticated: account !== null,
    refreshAccount,
    completeDeviceAuthorization,
    login,
    logout,
    selectProfile,
    leaveProfile,
    rediscover,
  }), [account, activeProfile, booting, completeDeviceAuthorization, discovery, leaveProfile, login, logout, rediscover, refreshAccount, selectProfile]);

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
