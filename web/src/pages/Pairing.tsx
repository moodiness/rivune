import { ArrowRight, CheckCircle2, KeyRound, LoaderCircle, RefreshCw, ShieldCheck, Smartphone } from "lucide-react";
import { useCallback, useEffect, useState, type FormEvent } from "react";
import { api, APIError } from "../api";
import { useAuth } from "../auth";
import { Button, Notice, RivuneMark } from "../components";
import { notifyError, notifyErrorMessage } from "../notifications";
import type { DeviceAuthorization } from "../types";
import { AuthBackdrop, LoginPage } from "./Onboarding";

export function DevicePairingPage() {
  const { discovery, completeDeviceAuthorization } = useAuth();
  const [authorization, setAuthorization] = useState<DeviceAuthorization | null>(null);
  const [ownerSignIn, setOwnerSignIn] = useState(false);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);

  const begin = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      setAuthorization(await api.beginDeviceAuthorization());
    } catch (cause) {
      setError(notifyError(cause, "This device could not start pairing.", "Pairing failed"));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void begin(); }, [begin]);

  useEffect(() => {
    if (!authorization) return;
    let cancelled = false;
    let timer: number | undefined;
    const poll = async () => {
      try {
        await completeDeviceAuthorization(authorization.deviceCode);
      } catch (cause) {
        if (cancelled) return;
        if (cause instanceof APIError && cause.code === "authorization_pending") {
          timer = window.setTimeout(poll, authorization.intervalSeconds * 1000);
          return;
        }
        if (cause instanceof APIError && cause.code === "slow_down") {
          timer = window.setTimeout(poll, (authorization.intervalSeconds + 5) * 1000);
          return;
        }
        if (cause instanceof APIError && cause.code === "expired_device_code") {
          setAuthorization(null);
          setError(notifyErrorMessage("This pairing code expired. Generate a new one to continue.", "Pairing code expired"));
          return;
        }
        setError(notifyError(cause, "This device could not finish pairing.", "Pairing failed"));
      }
    };
    timer = window.setTimeout(poll, authorization.intervalSeconds * 1000);
    return () => {
      cancelled = true;
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [authorization, completeDeviceAuthorization]);

  const pairingURL = new URL("/pair", window.location.origin).toString();

  if (ownerSignIn) return <LoginPage onBack={() => setOwnerSignIn(false)} />;

  return <main className="auth-page"><AuthBackdrop /><div className="auth-shell auth-shell--login">
    <RivuneMark />
    <section className="auth-card auth-card--login pairing-card page-enter">
      <div className="auth-card__server"><span className="status-dot" /> Connected to {discovery?.name ?? "Rivune"}</div>
      <div className="pairing-card__icon"><Smartphone /></div>
      <div className="auth-card__header">
        <span>New device</span>
        <h1>Connect to your Rivune home.</h1>
        <p>No family username or password is needed. Ask a manager to approve this device once.</p>
      </div>
      {error && <Notice>{error}</Notice>}
      {loading ? <div className="pairing-card__loading"><LoaderCircle className="spin" /><span>Creating a secure pairing code…</span></div>
        : authorization ? <div className="pairing-card__code">
          <span>Pairing code</span>
          <strong>{authorization.userCode}</strong>
          <p>On a device already connected to Rivune, open:</p>
          <code>{pairingURL}</code>
          <small>The manager selects a manager profile, confirms this code, and this screen opens the profile picker automatically.</small>
        </div>
          : <Button onClick={() => void begin()}><RefreshCw size={18} /> Generate a new code</Button>}
      <button type="button" className="text-button pairing-card__owner" onClick={() => setOwnerSignIn(true)}><KeyRound size={16} /> Owner sign in</button>
    </section>
    <footer className="auth-footer"><ShieldCheck size={14} /> One approval per device · Revocable at any time</footer>
  </div></main>;
}

export function PairApprovalPage() {
  const { activeProfile, leaveProfile } = useAuth();
  const [userCode, setUserCode] = useState(() => new URLSearchParams(window.location.search).get("code")?.toUpperCase() ?? "");
  const [approved, setApproved] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  async function submit(event: FormEvent) {
    event.preventDefault();
    setLoading(true);
    setError("");
    try {
      await api.approveDeviceAuthorization(userCode);
      setApproved(true);
    } catch (cause) {
      setError(notifyError(cause, "This device could not be approved.", "Approval failed"));
    } finally {
      setLoading(false);
    }
  }

  const normalizedCode = userCode.toUpperCase().replace(/[^A-Z0-9]/g, "").slice(0, 8);
  const formattedCode = normalizedCode.length > 4 ? `${normalizedCode.slice(0, 4)}-${normalizedCode.slice(4)}` : normalizedCode;

  return <main className="auth-page"><AuthBackdrop /><div className="auth-shell auth-shell--login">
    <RivuneMark />
    <section className="auth-card auth-card--login pairing-card pairing-card--approval page-enter">
      {approved ? <>
        <div className="pairing-card__icon pairing-card__icon--success"><CheckCircle2 /></div>
        <div className="auth-card__header"><span>Device approved</span><h1>They can choose a profile now.</h1><p>The new device received its own revocable session. No administrator credentials were shared.</p></div>
        <Button onClick={() => window.location.assign("/")}>Return to Rivune <ArrowRight size={18} /></Button>
      </> : !activeProfile?.canManage ? <>
        <div className="pairing-card__icon"><ShieldCheck /></div>
        <div className="auth-card__header"><span>Manager required</span><h1>Switch to a manager profile.</h1><p>Only a profile allowed to manage this Rivune home can approve a new device.</p></div>
        <Button onClick={() => void leaveProfile().catch((cause) => setError(notifyError(cause, "The profile chooser could not be opened.")))}>Choose another profile</Button>
      </> : <>
        <div className="pairing-card__icon"><Smartphone /></div>
        <div className="auth-card__header"><span>Device pairing</span><h1>Approve a family device.</h1><p>Compare the code shown on the new device before approving it.</p></div>
        <form className="form-stack" onSubmit={submit}>
          {error && <Notice>{error}</Notice>}
          <label className="field"><span>Pairing code</span><div><KeyRound size={18} /><input value={formattedCode} onChange={(event) => setUserCode(event.target.value)} autoComplete="one-time-code" inputMode="text" placeholder="ABCD-EFGH" minLength={9} maxLength={9} autoFocus required /></div></label>
          <Button type="submit" loading={loading}>Approve device <ArrowRight size={18} /></Button>
        </form>
      </>}
    </section>
    <footer className="auth-footer">Approving as {activeProfile?.name ?? "manager"}</footer>
  </div></main>;
}
