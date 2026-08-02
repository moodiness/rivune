import { ArrowRight, CheckCircle2, KeyRound, LoaderCircle, RefreshCw, ShieldCheck, Smartphone } from "lucide-react";
import { useCallback, useEffect, useState, type FormEvent } from "react";
import { api, APIError } from "../api";
import { useAuth } from "../auth";
import { Button, Notice, RivuneMark } from "../components";
import { notifyError, notifyErrorMessage } from "../notifications";
import { translate as t } from "../i18n";
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
      setError(notifyError(cause, t("pairing.startFailure"), t("pairing.failureTitle")));
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
          setError(notifyErrorMessage(t("pairing.codeExpiredBody"), t("pairing.codeExpiredTitle")));
          return;
        }
        setError(notifyError(cause, t("pairing.finishFailure"), t("pairing.failureTitle")));
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
      <div className="auth-card__server"><span className="status-dot" /> {t("auth.connectedTo", { server: discovery?.name ?? "Rivune" })}</div>
      <div className="pairing-card__icon"><Smartphone /></div>
      <div className="auth-card__header">
        <span>{t("pairing.deviceEyebrow")}</span>
        <h1>{t("pairing.deviceTitle")}</h1>
        <p>{t("pairing.deviceBody")}</p>
      </div>
      {error && <Notice>{error}</Notice>}
      {loading ? <div className="pairing-card__loading"><LoaderCircle className="spin" /><span>{t("pairing.creatingCode")}</span></div>
        : authorization ? <div className="pairing-card__code">
          <span>{t("pairing.codeLabel")}</span>
          <strong>{authorization.userCode}</strong>
          <p>{t("pairing.openApprovalPage")}</p>
          <code>{pairingURL}</code>
          <small>{t("pairing.deviceInstructions")}</small>
        </div>
          : <Button onClick={() => void begin()}><RefreshCw size={18} /> {t("pairing.generateCode")}</Button>}
      <button type="button" className="text-button pairing-card__owner" onClick={() => setOwnerSignIn(true)}><KeyRound size={16} /> {t("pairing.ownerSignIn")}</button>
    </section>
    <footer className="auth-footer"><ShieldCheck size={14} /> {t("pairing.deviceFooter")}</footer>
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
      setError(notifyError(cause, t("pairing.approvalFailure"), t("pairing.approvalFailureTitle")));
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
        <div className="auth-card__header"><span>{t("pairing.approvedEyebrow")}</span><h1>{t("pairing.approvedTitle")}</h1><p>{t("pairing.approvedBody")}</p></div>
        <Button onClick={() => window.location.assign("/")}>{t("pairing.returnToRivune")} <ArrowRight size={18} /></Button>
      </> : !activeProfile?.canManage ? <>
        <div className="pairing-card__icon"><ShieldCheck /></div>
        <div className="auth-card__header"><span>{t("pairing.managerRequiredEyebrow")}</span><h1>{t("pairing.managerRequiredTitle")}</h1><p>{t("pairing.managerRequiredBody")}</p></div>
        <Button onClick={() => void leaveProfile().catch((cause) => setError(notifyError(cause, t("profiles.chooserFailure"))))}>{t("pairing.chooseAnotherProfile")}</Button>
      </> : <>
        <div className="pairing-card__icon"><Smartphone /></div>
        <div className="auth-card__header"><span>{t("pairing.approvalEyebrow")}</span><h1>{t("pairing.approvalTitle")}</h1><p>{t("pairing.approvalBody")}</p></div>
        <form className="form-stack" onSubmit={submit}>
          {error && <Notice>{error}</Notice>}
          <label className="field"><span>{t("pairing.codeLabel")}</span><div><KeyRound size={18} /><input value={formattedCode} onChange={(event) => setUserCode(event.target.value)} autoComplete="one-time-code" inputMode="text" placeholder={t("pairing.codePlaceholder")} minLength={9} maxLength={9} autoFocus required /></div></label>
          <Button type="submit" loading={loading}>{t("pairing.approveDevice")} <ArrowRight size={18} /></Button>
        </form>
      </>}
    </section>
    <footer className="auth-footer">{t("pairing.approvingAs", { name: activeProfile?.name ?? t("pairing.managerFallback") })}</footer>
  </div></main>;
}
