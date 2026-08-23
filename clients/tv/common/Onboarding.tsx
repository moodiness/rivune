import { useEffect, useRef, useState } from "react";
import { APIError, RivuneTvClient } from "./api";
import { Brand, ErrorPanel, Screen, Spinner, TvButton } from "./components";
import { focusFirst } from "./focus";
import { t } from "./i18n";
import { platformAdapter } from "./platform";
import type { Account, DeviceAuthorization, Discovery } from "./types";

type Connected = { client: RivuneTvClient; discovery: Discovery; account: Account | null };
const serverKey = "rivune.tv.server.v1";

function message(cause: unknown): string {
  if (cause instanceof APIError && cause.code === "incompatible_protocol") return cause.message || t("server.incompatible", { version: "?" });
  return cause instanceof Error && cause.message ? cause.message : t("error.network");
}

export function Onboarding({ rememberedServer, onConnected }: { rememberedServer: string | null; onConnected: (connection: Connected) => void }) {
  const adapter = platformAdapter();
  const [address, setAddress] = useState(rememberedServer ?? "");
  const [client, setClient] = useState<RivuneTvClient | null>(null);
  const [discovery, setDiscovery] = useState<Discovery | null>(null);
  const [authorization, setAuthorization] = useState<DeviceAuthorization | null>(null);
  const [busy, setBusy] = useState(Boolean(rememberedServer));
  const [error, setError] = useState("");
  const generation = useRef(0);

  useEffect(() => { focusFirst(); }, [authorization, discovery]);
  useEffect(() => {
    if (!rememberedServer) return;
    void connect(rememberedServer, true);
  // The remembered value is intentionally consumed once at startup.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (!client || !authorization) return;
    const current = ++generation.current;
    let timer = 0;
    let cancelled = false;
    let delaySeconds = Math.max(1, authorization.intervalSeconds);
    const expiresAt = Date.parse(authorization.expiresAt);
    const poll = async () => {
      if (cancelled || current !== generation.current) return;
      if (Number.isFinite(expiresAt) && Date.now() >= expiresAt) {
        setError(t("error.authorization"));
        return;
      }
      try {
        await client.exchangeDeviceAuthorization(authorization.deviceCode);
        const account = await client.currentAccount();
        if (!cancelled && current === generation.current) onConnected({ client, discovery: discovery!, account });
      } catch (cause) {
        if (cancelled || current !== generation.current) return;
        if (cause instanceof APIError && (cause.code === "authorization_pending" || cause.code === "slow_down")) {
          delaySeconds = cause.retryAfterSeconds ?? (cause.code === "slow_down" ? delaySeconds + 5 : delaySeconds);
          timer = window.setTimeout(poll, Math.max(1, delaySeconds) * 1000);
          return;
        }
        setBusy(false);
        setError(cause instanceof Error ? cause.message : t("error.authorization"));
      }
    };
    timer = window.setTimeout(poll, delaySeconds * 1000);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [authorization, client, discovery, onConnected]);

  async function connect(value = address, silent = false) {
    ++generation.current;
    setBusy(true);
    setError("");
    setAuthorization(null);
    try {
      const next = new RivuneTvClient(value, adapter.platform);
      const found = await next.discover();
      setClient(next);
      setDiscovery(found);
      window.localStorage.setItem(serverKey, next.issuer);
      if (await next.restoreSession()) {
        const account = await next.currentAccount();
        onConnected({ client: next, discovery: found, account });
        return;
      }
      const deviceName = await adapter.deviceName();
      const code = await next.beginDeviceAuthorization(deviceName, adapter.platform);
      setAuthorization(code);
    } catch (cause) {
      if (silent) window.localStorage.removeItem(serverKey);
      setError(message(cause));
    } finally {
      setBusy(false);
    }
  }

  async function restartPairing() {
    if (!client) return;
    setBusy(true);
    setError("");
    try {
      const deviceName = await adapter.deviceName();
      setAuthorization(await client.beginDeviceAuthorization(deviceName, adapter.platform));
    } catch (cause) {
      setError(message(cause));
    } finally {
      setBusy(false);
    }
  }

  if (authorization && discovery) return <Screen className="tv-centered">
    <section className="tv-onboarding">
      <Brand />
      <h1>{t("pair.title")}</h1>
      <p>{t("pair.body", { url: authorization.verificationUri })}</p>
      <span className="tv-code">{authorization.userCode}</span>
      <p className="tv-pair-url">{authorization.verificationUri}</p>
      <Spinner label={t("pair.waiting")} />
      <p className="tv-help">{t("pair.expires", { time: new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit" }).format(new Date(authorization.expiresAt)) })}</p>
      <div className="tv-actions"><TvButton onClick={restartPairing}>{t("pair.restart")}</TvButton><TvButton tone="quiet" onClick={() => { ++generation.current; setAuthorization(null); setDiscovery(null); setClient(null); }}>{t("common.back")}</TvButton></div>
      {error && <ErrorPanel message={error} onClose={() => setError("")} />}
    </section>
  </Screen>;

  return <Screen className="tv-centered">
    <section className="tv-onboarding">
      <Brand />
      <h1>{t("server.title")}</h1>
      <p>{t("server.body")}</p>
      <label className="tv-field"><span>{t("server.address")}</span><input className="tv-input" type="url" inputMode="url" value={address} placeholder={t("server.example")} autoCapitalize="none" autoCorrect="off" spellCheck={false} onChange={(event) => setAddress(event.target.value)} onKeyDown={(event) => { if (event.key === "Enter") void connect(); }} /></label>
      <p className="tv-help">{t("server.security")}</p>
      {busy ? <Spinner /> : <div className="tv-actions"><TvButton tone="primary" disabled={!address.trim()} onClick={() => void connect()}>{t("server.connect")}</TvButton></div>}
      {error && <ErrorPanel message={error} onRetry={() => void connect()} onClose={() => setError("")} />}
    </section>
  </Screen>;
}

export function rememberedTvServer(): string | null {
  try { return window.localStorage.getItem(serverKey); } catch { return null; }
}

export function forgetTvServer(): void {
  try { window.localStorage.removeItem(serverKey); } catch { /* unavailable storage */ }
}
