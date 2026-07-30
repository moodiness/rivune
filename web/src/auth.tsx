import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { api, clearSession } from "./api";
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

  const refreshAccount = useCallback(async () => {
    try {
      const next = await api.me();
      setAccount(next);
      return next;
    } catch {
      setAccount(null);
      return null;
    }
  }, []);

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
    await api.login(username, password);
    setProfileConfirmed(false);
    setAccount(await api.me());
  }, []);

  const logout = useCallback(async () => {
    await api.logout();
    setAccount(null);
    setProfileConfirmed(false);
  }, []);

  const selectProfile = useCallback(async (profile: Profile, pin?: string) => {
    await api.selectProfile(profile.id, pin);
    setProfileConfirmed(true);
    setAccount(await api.me());
  }, []);

  const leaveProfile = useCallback(async () => {
    await api.clearProfile();
    setProfileConfirmed(false);
    setAccount(await api.me());
  }, []);

  const completeDeviceAuthorization = useCallback(async (deviceCode: string) => {
    await api.exchangeDeviceAuthorization(deviceCode);
    setProfileConfirmed(false);
    setAccount(await api.me());
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
