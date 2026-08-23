import { useCallback, useEffect, useRef, useState } from "react";
import { RivuneTvClient } from "./api";
import { installSpatialNavigation } from "./focus";
import { setLocale } from "./i18n";
import { forgetTvServer, Onboarding, rememberedTvServer } from "./Onboarding";
import { ProfileGate } from "./ProfileGate";
import { Viewer } from "./Viewer";
import type { Account, Discovery, Profile } from "./types";

type Connection = { client: RivuneTvClient; discovery: Discovery; account: Account | null };

export default function App() {
  const [connection, setConnection] = useState<Connection | null>(null);
  const [profile, setProfile] = useState<Profile | null>(null);
  const [generation, setGeneration] = useState(0);
  const backHandler = useRef<() => void>(() => window.RivunePlatformAdapter?.exitApp());

  const setBack = useCallback((handler: () => void) => { backHandler.current = handler; }, []);
  const connected = useCallback((next: Connection) => {
    setLocale(next.discovery.interfaceLanguage);
    setConnection(next);
    const activeId = next.account?.session.activeProfile?.id;
    const active = activeId ? next.account?.profiles.find((candidate) => candidate.id === activeId && candidate.accessible) ?? null : null;
    setProfile(active);
  }, []);

  useEffect(() => installSpatialNavigation(() => backHandler.current()), []);
  useEffect(() => {
    const updater = window.RivuneUpdater;
    if (!updater) return;
    void updater.markHealthy().then(() => updater.checkAutomatically());
  }, []);
  useEffect(() => {
    if (!connection) backHandler.current = () => window.RivunePlatformAdapter?.exitApp();
    else if (!profile) backHandler.current = () => window.RivunePlatformAdapter?.exitApp();
  }, [connection, profile]);

  function disconnect() {
    forgetTvServer();
    setConnection(null);
    setProfile(null);
    setGeneration((value) => value + 1);
  }

  if (!connection) return <Onboarding key={generation} rememberedServer={rememberedTvServer()} onConnected={connected} />;
  if (!connection.account) return <Onboarding key={`auth-${generation}`} rememberedServer={connection.client.issuer} onConnected={connected} />;
  if (!profile) return <ProfileGate client={connection.client} account={connection.account} onSelected={(nextProfile, account) => { setConnection({ ...connection, account }); setProfile(nextProfile); }} onSignedOut={disconnect} />;
  return <Viewer client={connection.client} discovery={connection.discovery} account={connection.account} profile={profile} setBackHandler={setBack} onChangeProfile={(account) => { setConnection({ ...connection, account }); setProfile(null); }} onDisconnect={disconnect} />;
}
