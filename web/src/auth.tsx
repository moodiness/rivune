import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import {
  api,
  broadcastProfileSelectionChange,
  clearDemoClientState,
  clearSession,
  DEMO_UNAVAILABLE_EVENT,
  hasDemoHint,
  profileRequestContext,
  prepareDemoAttempt,
  PROFILE_SELECTION_BROADCAST_KEY,
  PROFILE_SELECTION_REQUIRED_EVENT,
  rejectProfileRequestContext,
  rememberDemoSession,
  setProfileRequestContext,
} from "./api";
import { setLocale, translate as t } from "./i18n";
import type { Account, Discovery, InterfaceLanguage, Profile } from "./types";

type AuthState = {
  discovery: Discovery | null;
  account: Account | null;
  activeProfile: Profile | null;
  booting: boolean;
  mode: "real" | "demo";
  demoRevision: number;
  terminalMessage: string | null;
  authenticated: boolean;
  profileRequestSignal: AbortSignal;
  refreshAccount: (options?: { restoreActiveProfile?: boolean }) => Promise<Account | null>;
  login: (username: string, password: string) => Promise<void>;
  completeDeviceAuthorization: (deviceCode: string) => Promise<void>;
  logout: () => Promise<void>;
  selectProfile: (profile: Profile, pin?: string) => Promise<void>;
  leaveProfile: () => Promise<void>;
  enterDemo: () => Promise<void>;
  resetDemo: () => Promise<void>;
  exitDemo: () => Promise<void>;
  rediscover: () => Promise<void>;
  updateServerInterfaceLanguage: (language: InterfaceLanguage) => Promise<void>;
};

const AuthContext = createContext<AuthState | null>(null);

export function principalIdentity(discovery: Discovery | null, account: Account | null, profile: Profile | null): string | null {
  if (!discovery || !account || !profile) return null;
  let server = discovery.apiBaseUrl;
  try {
    server = new URL(discovery.apiBaseUrl, window.location.origin).href;
  } catch {
    // The discovery value is still an identity boundary even when it is not a valid URL.
  }
  return JSON.stringify([
    server,
    account.user.id,
    account.session.id,
    account.session.authorizationScope,
    account.session.category?.id ?? "",
    profile.id,
    profile.categoryId,
  ]);
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [discovery, setDiscovery] = useState<Discovery | null>(null);
  const [account, setAccount] = useState<Account | null>(null);
  const [booting, setBooting] = useState(true);
  const [mode, setMode] = useState<"real" | "demo">("real");
  const [demoRevision, setDemoRevision] = useState(0);
  const [terminalMessage, setTerminalMessage] = useState<string | null>(null);
  const [confirmedProfileID, setConfirmedProfileID] = useState<string | null>(null);
  const authGeneration = useRef(0);
  const profileSelectionInFlight = useRef(false);
  const confirmedProfileIDRef = useRef<string | null>(null);
  const profileRequestController = useRef(new AbortController());
  const [profileRequestSignal, setProfileRequestSignal] = useState(profileRequestController.current.signal);

  const suspendProfile = useCallback(() => {
    confirmedProfileIDRef.current = null;
    profileRequestController.current.abort();
    setConfirmedProfileID(null);
  }, []);

  const invalidateProfile = useCallback(() => {
    setProfileRequestContext(null, null);
    suspendProfile();
  }, [suspendProfile]);

  const confirmProfile = useCallback((profileID: string, profileContext: string | null) => {
    profileRequestController.current.abort();
    const controller = new AbortController();
    setProfileRequestContext(profileID, profileContext);
    confirmedProfileIDRef.current = profileID;
    profileRequestController.current = controller;
    setProfileRequestSignal(controller.signal);
    setConfirmedProfileID(profileID);
  }, []);

  const applyDemoAccount = useCallback((next: Account) => {
    if (next.user.role !== "demo") throw new Error("The demo endpoint returned a non-demo account.");
    setMode("demo");
    setTerminalMessage(null);
    setAccount(next);
    const activeProfileID = next.session.activeProfile?.id ?? null;
    if (activeProfileID && confirmedProfileIDRef.current !== activeProfileID) confirmProfile(activeProfileID, null);
    else if (!activeProfileID) invalidateProfile();
  }, [confirmProfile, invalidateProfile]);

  const refreshAccount = useCallback(async (options?: { restoreActiveProfile?: boolean }) => {
    const generation = authGeneration.current;
    try {
      const next = await api.me();
      if (authGeneration.current === generation) {
        setAccount(next);
        const activeProfileID = next.session.activeProfile?.id ?? null;
        if (options?.restoreActiveProfile && activeProfileID !== null) {
          const context = profileRequestContext(activeProfileID);
          if (context) confirmProfile(activeProfileID, context);
          else invalidateProfile();
        }
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
      rejectProfileRequestContext();
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
    const sharedProfileChanged = (event: StorageEvent) => {
      if (event.key === PROFILE_SELECTION_BROADCAST_KEY && event.newValue !== null) {
        requireProfileSelection();
      }
    };
    window.addEventListener(PROFILE_SELECTION_REQUIRED_EVENT, requireProfileSelection);
    window.addEventListener("storage", sharedProfileChanged);
    return () => {
      window.removeEventListener(PROFILE_SELECTION_REQUIRED_EVENT, requireProfileSelection);
      window.removeEventListener("storage", sharedProfileChanged);
    };
  }, [invalidateProfile]);

  const rediscover = useCallback(async () => {
    const next = await api.discovery();
    await setLocale(next.interfaceLanguage);
    setDiscovery(next);
  }, []);

  const updateServerInterfaceLanguage = useCallback(async (interfaceLanguage: InterfaceLanguage) => {
    const loadedLocale = await setLocale(interfaceLanguage);
    if (loadedLocale !== interfaceLanguage) return;
    setDiscovery((current) => current === null ? null : { ...current, interfaceLanguage });
  }, []);


  useEffect(() => {
    const endDemo = () => {
      ++authGeneration.current;
      invalidateProfile();
      setMode("real");
      setAccount(null);
      setTerminalMessage(t("demo.unavailable"));
      setDemoRevision((current) => current + 1);
      window.history.replaceState({}, "", "/");
      void rediscover().catch(() => setDiscovery(null));
    };
    window.addEventListener(DEMO_UNAVAILABLE_EVENT, endDemo);
    return () => window.removeEventListener(DEMO_UNAVAILABLE_EVENT, endDemo);
  }, [invalidateProfile, rediscover]);

  useEffect(() => {
    let active = true;
    void (async () => {
      try {
        const discovered = await api.discovery();
        if (!active) return;
        await setLocale(discovered.interfaceLanguage);
        if (!active) return;
        setDiscovery(discovered);

        if (window.location.pathname === "/demo") {
          window.history.replaceState({}, "", `/${window.location.search}${window.location.hash}`);
        }

        if (discovered.setupRequired && hasDemoHint()) {
          clearSession();
          try {
            const response = await api.demoSession();
            if (!active) return;
            applyDemoAccount(response.account);
            rememberDemoSession();
            return;
          } catch {
            clearDemoClientState();
            if (!active) return;
          }
        }

        if (!discovered.setupRequired && await api.restore()) {
          const current = await api.me();
          if (active) {
            setMode("real");
            setAccount(current);
            const activeProfileID = current.session.activeProfile?.id ?? null;
            const context = activeProfileID ? profileRequestContext(activeProfileID) : null;
            if (activeProfileID && context) confirmProfile(activeProfileID, context);
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
  }, [applyDemoAccount, confirmProfile, invalidateProfile]);

  const login = useCallback(async (username: string, password: string) => {
    const generation = ++authGeneration.current;
    invalidateProfile();
    await api.login(username, password);
    const next = await api.me();
    if (authGeneration.current !== generation) return;
    setMode("real");
    setTerminalMessage(null);
    setAccount(next);
  }, [invalidateProfile]);

  const enterDemo = useCallback(async () => {
    const generation = ++authGeneration.current;
    const response = await api.startDemo();
    if (authGeneration.current !== generation) return;
    prepareDemoAttempt();
    invalidateProfile();
    applyDemoAccount(response.account);
    rememberDemoSession();
    setDemoRevision((current) => current + 1);
  }, [applyDemoAccount, invalidateProfile]);

  const resetDemo = useCallback(async () => {
    const generation = ++authGeneration.current;
    const previousProfileID = confirmedProfileIDRef.current;
    profileRequestController.current.abort();
    let response: { account: Account };
    try {
      response = await api.resetDemo();
    } catch (cause) {
      if (previousProfileID && authGeneration.current === generation) confirmProfile(previousProfileID, null);
      throw cause;
    }
    if (authGeneration.current !== generation) return;
    clearDemoClientState();
    if (previousProfileID && response.account.session.activeProfile?.id === previousProfileID) confirmProfile(previousProfileID, null);
    applyDemoAccount(response.account);
    rememberDemoSession();
    setDemoRevision((current) => current + 1);
  }, [applyDemoAccount, confirmProfile]);

  const exitDemo = useCallback(async () => {
    const generation = ++authGeneration.current;
    await api.exitDemo();
    if (authGeneration.current !== generation) return;
    clearDemoClientState();
    invalidateProfile();
    setMode("real");
    setAccount(null);
    setTerminalMessage(null);
    setDemoRevision((current) => current + 1);
    window.history.replaceState({}, "", "/");
    await rediscover();
  }, [invalidateProfile, rediscover]);

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
      const selection = await api.selectProfile(profile.id, pin);
      const next = await api.me();
      if (authGeneration.current !== generation) return;
      setAccount(next);
      if (next.session.activeProfile?.id === profile.id) confirmProfile(profile.id, selection.profileContext);
      broadcastProfileSelectionChange();
    } finally {
      if (authGeneration.current === generation) profileSelectionInFlight.current = false;
    }
  }, [confirmProfile, invalidateProfile]);

  const leaveProfile = useCallback(async () => {
    const generation = ++authGeneration.current;
    profileSelectionInFlight.current = true;
    suspendProfile();
    try {
      await api.clearProfile();
      invalidateProfile();
      broadcastProfileSelectionChange();
      const next = await api.me();
      if (authGeneration.current !== generation) return;
      setAccount(next);
    } finally {
      if (authGeneration.current === generation) profileSelectionInFlight.current = false;
    }
  }, [invalidateProfile, suspendProfile]);

  const completeDeviceAuthorization = useCallback(async (deviceCode: string) => {
    const generation = ++authGeneration.current;
    invalidateProfile();
    await api.exchangeDeviceAuthorization(deviceCode);
    const next = await api.me();
    if (authGeneration.current !== generation) return;
    setMode("real");
    setTerminalMessage(null);
    setAccount(next);
  }, [invalidateProfile]);

  useEffect(() => {
    if (mode !== "demo") return;
    const poll = () => {
      const generation = authGeneration.current;
      void api.demoSession()
        .then((response) => {
          if (authGeneration.current === generation) applyDemoAccount(response.account);
        })
        .catch(() => undefined);
    };
    const timer = window.setInterval(poll, 5_000);
    return () => window.clearInterval(timer);
  }, [applyDemoAccount, mode]);

  const activeProfile = confirmedProfileID !== null && account?.session.activeProfile?.id === confirmedProfileID
    ? account.profiles.find((profile) => profile.id === confirmedProfileID) ?? null
    : null;
  const value = useMemo<AuthState>(() => ({
    discovery,
    account,
    activeProfile,
    booting,
    mode,
    demoRevision,
    terminalMessage,
    authenticated: account !== null,
    profileRequestSignal,
    refreshAccount,
    completeDeviceAuthorization,
    login,
    logout,
    selectProfile,
    leaveProfile,
    enterDemo,
    resetDemo,
    exitDemo,
    rediscover,
    updateServerInterfaceLanguage,
  }), [account, activeProfile, booting, completeDeviceAuthorization, demoRevision, discovery, enterDemo, exitDemo, leaveProfile, login, logout, mode, profileRequestSignal, rediscover, refreshAccount, resetDemo, selectProfile, terminalMessage, updateServerInterfaceLanguage]);

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
