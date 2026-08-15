import { QRCodeSVG } from "qrcode.react";
import { ArrowRight, CheckCircle2, FolderKey, KeyRound, LoaderCircle, NotebookPen, RefreshCw, ShieldCheck, Smartphone, Tag } from "lucide-react";
import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { api, APIError } from "../api";
import { useAuth } from "../auth";
import { Button, Notice, RivuneMark, Select } from "../components";
import { notifyError, notifyErrorMessage } from "../notifications";
import { locale, translate as t } from "../i18n";
import type { AccessCategory, DeviceAuthorization } from "../types";
import { AuthBackdrop, LoginPage } from "./Onboarding";

function formatPairingExpiration(expiresAt: string): string | null {
  const expiration = new Date(expiresAt);
  if (Number.isNaN(expiration.getTime())) return null;
  return new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(expiration);
}


export function DevicePairingPage() {
  const { discovery, completeDeviceAuthorization } = useAuth();
  const [authorization, setAuthorization] = useState<DeviceAuthorization | null>(null);
  const [ownerSignIn, setOwnerSignIn] = useState(false);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const mountedRef = useRef(false);
  const beginGenerationRef = useRef(0);

  const begin = useCallback(async () => {
    const generation = ++beginGenerationRef.current;
    setLoading(true);
    setAuthorization(null);
    setError("");
    try {
      const nextAuthorization = await api.beginDeviceAuthorization();
      if (!mountedRef.current || beginGenerationRef.current !== generation) return;
      setAuthorization(nextAuthorization);
    } catch (cause) {
      if (!mountedRef.current || beginGenerationRef.current !== generation) return;
      setError(notifyError(cause, t("pairing.startFailure"), t("pairing.failureTitle")));
    } finally {
      if (mountedRef.current && beginGenerationRef.current === generation) setLoading(false);
    }
  }, []);

  useEffect(() => {
    mountedRef.current = true;
    const timer = window.setTimeout(() => { void begin(); }, 0);
    return () => {
      mountedRef.current = false;
      beginGenerationRef.current += 1;
      window.clearTimeout(timer);
    };
  }, [begin]);

  useEffect(() => {
    if (!authorization) return;
    let cancelled = false;
    let timer: number | undefined;
    let expirationTimer: number | undefined;
    let pollIntervalSeconds = authorization.intervalSeconds;
    const expire = () => {
      if (cancelled) return;
      setAuthorization(null);
      setError(notifyErrorMessage(t("pairing.codeExpiredBody"), t("pairing.codeExpiredTitle")));
    };
    const expiresAt = new Date(authorization.expiresAt).getTime();
    const remaining = expiresAt - Date.now();
    if (Number.isFinite(expiresAt)) {
      if (remaining <= 0) {
        expire();
        return;
      }
      if (remaining <= 2_147_483_647) expirationTimer = window.setTimeout(expire, remaining);
    }
    const poll = async () => {
      if (Number.isFinite(expiresAt) && Date.now() >= expiresAt) {
        expire();
        return;
      }
      try {
        await completeDeviceAuthorization(authorization.deviceCode);
      } catch (cause) {
        if (cancelled) return;
        if (cause instanceof APIError && cause.code === "authorization_pending") {
          timer = window.setTimeout(poll, pollIntervalSeconds * 1000);
          return;
        }
        if (cause instanceof APIError && cause.code === "slow_down") {
          pollIntervalSeconds += 5;
          timer = window.setTimeout(poll, pollIntervalSeconds * 1000);
          return;
        }
        setAuthorization(null);
        if (cause instanceof APIError && cause.code === "expired_device_code") {
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
      if (expirationTimer !== undefined) window.clearTimeout(expirationTimer);
    };
  }, [authorization, completeDeviceAuthorization]);

  const verificationURL = authorization ? new URL(authorization.verificationUri, window.location.origin).toString() : null;
  const verificationCompleteURL = authorization ? new URL(authorization.verificationUriComplete, window.location.origin).toString() : null;
  const expiration = authorization ? formatPairingExpiration(authorization.expiresAt) : null;

  if (ownerSignIn) return <LoginPage onBack={() => setOwnerSignIn(false)} />;

  return <main className="auth-page"><AuthBackdrop /><div className="auth-shell auth-shell--login">
    <RivuneMark />
    <section className="auth-card auth-card--login pairing-card pairing-card--device page-enter">
      <div className="auth-card__server"><span className="status-dot" /> {t("auth.connectedTo", { server: discovery?.name ?? "Rivune" })}</div>
      <div className="pairing-card__intro">
        <div className="pairing-card__icon"><Smartphone /></div>
        <div className="auth-card__header">
          <span>{t("pairing.deviceEyebrow")}</span>
          <h1>{t("pairing.deviceTitle")}</h1>
          <p>{t("pairing.deviceBody")}</p>
        </div>
      </div>
      {error && <Notice>{error}</Notice>}
      {loading ? <div className="pairing-card__loading" role="status"><LoaderCircle className="spin" /><span>{t("pairing.creatingCode")}</span></div>
        : authorization && verificationURL && verificationCompleteURL ? <div className="pairing-card__code">
          <div className="pairing-card__credential">
            <span>{t("pairing.codeLabel")}</span>
            <strong dir="ltr">{authorization.userCode}</strong>
          </div>
          <div className="pairing-card__qr">
            <QRCodeSVG value={verificationCompleteURL} level="M" marginSize={4} title={t("pairing.codeLabel")} role="img" aria-label={t("pairing.codeLabel")} />
          </div>
          <div className="pairing-card__instructions">
            <p>{t("pairing.openApprovalPage")}</p>
            <a href={verificationURL} dir="ltr">{verificationURL}</a>
            {expiration && <time dateTime={authorization.expiresAt}>{t("pairing.codeExpiresAt", { time: expiration })}</time>}
            <small>{t("pairing.deviceInstructions")}</small>
          </div>
        </div>
          : <Button onClick={() => void begin()}><RefreshCw size={18} /> {t("pairing.generateCode")}</Button>}
      <Button type="button" variant="secondary" onClick={() => window.location.assign("/pair")}>{t("pairing.approveDevice")} <ArrowRight size={18} /></Button>
      <button type="button" className="text-button pairing-card__owner" onClick={() => setOwnerSignIn(true)}><KeyRound size={16} /> {t("pairing.ownerSignIn")}</button>
    </section>
    <footer className="auth-footer"><ShieldCheck size={14} /> {t("pairing.deviceFooter")}</footer>
  </div></main>;
}

export function PairApprovalPage() {
  const { account, activeProfile, leaveProfile } = useAuth();
  const session = account?.session;
  const isGlobalAdmin = session?.authorizationScope === "global_admin";
  const sessionCategory = session?.authorizationScope === "category" ? session.category : null;
  const [userCode, setUserCode] = useState(() => new URLSearchParams(window.location.search).get("code")?.toUpperCase() ?? "");
  const [deviceName, setDeviceName] = useState("");
  const [internalNote, setInternalNote] = useState("");
  const [categories, setCategories] = useState<AccessCategory[]>([]);
  const [categoryID, setCategoryID] = useState("");
  const [categoryState, setCategoryState] = useState<"idle" | "loading" | "ready" | "error" | "forbidden">("idle");
  const [categoryError, setCategoryError] = useState("");
  const [approved, setApproved] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const submittingRef = useRef(false);
  const categorySelectRef = useRef<HTMLButtonElement>(null);
  const successHeadingRef = useRef<HTMLHeadingElement>(null);

  const loadCategories = useCallback(async () => {
    if (!isGlobalAdmin) return;
    setCategoryState("loading");
    setCategoryError("");
    try {
      const nextCategories = await api.categories();
      setCategories(nextCategories);
      setCategoryID((current) => nextCategories.some((category) => category.id === current) ? current : "");
      setCategoryState("ready");
    } catch (cause) {
      setCategories([]);
      setCategoryID("");
      if (cause instanceof APIError && cause.status === 403) {
        setCategoryState("forbidden");
        setCategoryError(t("pairing.categoryForbidden"));
      } else {
        setCategoryState("error");
        setCategoryError(notifyError(cause, t("pairing.categoryLoadFailure"), t("pairing.failureTitle")));
      }
    }
  }, [isGlobalAdmin]);

  useEffect(() => {
    if (isGlobalAdmin) {
      void loadCategories();
      return;
    }
    setCategories([]);
    setCategoryID("");
    setCategoryError("");
    setCategoryState("idle");
  }, [isGlobalAdmin, loadCategories]);

  useEffect(() => {
    if (!approved) return;
    window.requestAnimationFrame(() => successHeadingRef.current?.focus({ preventScroll: true }));
  }, [approved]);

  const normalizedCode = userCode.toUpperCase().replace(/[^A-Z0-9]/g, "").slice(0, 8);
  const formattedCode = normalizedCode.length > 4 ? `${normalizedCode.slice(0, 4)}-${normalizedCode.slice(4)}` : normalizedCode;
  const validCode = /^[A-Z2-9]{8}$/.test(normalizedCode);
  const selectedCategory = isGlobalAdmin
    ? categories.find((category) => category.id === categoryID) ?? null
    : sessionCategory;
  const categoryReady = isGlobalAdmin
    ? categoryState === "ready" && categories.length > 0
    : Boolean(sessionCategory);
  const canApprove = validCode && categoryReady && Boolean(selectedCategory) && !loading;

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (submittingRef.current || loading || !validCode) return;
    if (!selectedCategory) {
      setError(t(isGlobalAdmin ? "pairing.categoryRequired" : "pairing.categoryUnavailable"));
      categorySelectRef.current?.focus();
      return;
    }

    submittingRef.current = true;
    setLoading(true);
    setError("");
    const trimmedDeviceName = deviceName.trim();
    const trimmedInternalNote = internalNote.trim();
    try {
      await api.approveDeviceAuthorization({
        userCode: formattedCode,
        categoryId: selectedCategory.id,
        ...(trimmedDeviceName ? { deviceName: trimmedDeviceName } : {}),
        ...(trimmedInternalNote ? { internalNote: trimmedInternalNote } : {}),
      });
      setApproved(true);
    } catch (cause) {
      setError(notifyError(cause, t("pairing.approvalFailure"), t("pairing.approvalFailureTitle")));
    } finally {
      submittingRef.current = false;
      setLoading(false);
    }
  }

  return <main className="auth-page"><AuthBackdrop /><div className="auth-shell auth-shell--login">
    <RivuneMark />
    <section className="auth-card auth-card--login pairing-card pairing-card--approval page-enter">
      {approved ? <>
        <div className="pairing-card__icon pairing-card__icon--success"><CheckCircle2 /></div>
        <div className="auth-card__header"><span>{t("pairing.approvedEyebrow")}</span><h1 ref={successHeadingRef} tabIndex={-1}>{t("pairing.approvedTitle")}</h1><p>{t("pairing.approvedBody")}</p></div>
        <Button onClick={() => window.location.assign("/")}>{t("pairing.returnToRivune")} <ArrowRight size={18} /></Button>
      </> : !(isGlobalAdmin || activeProfile?.canManage) ? <>
        <div className="pairing-card__icon"><ShieldCheck /></div>
        <div className="auth-card__header"><span>{t("pairing.managerRequiredEyebrow")}</span><h1>{t("pairing.managerRequiredTitle")}</h1><p>{t("pairing.managerRequiredBody")}</p></div>
        {error && <Notice>{error}</Notice>}
        <Button onClick={() => void leaveProfile().catch((cause) => setError(notifyError(cause, t("profiles.chooserFailure"))))}>{t("pairing.chooseAnotherProfile")}</Button>
      </> : <>
        <div className="pairing-card__icon"><Smartphone /></div>
        <div className="auth-card__header"><span>{t("pairing.approvalEyebrow")}</span><h1>{t("pairing.approvalCategoryTitle")}</h1><p>{t("pairing.approvalBody")}</p></div>
        <form className="form-stack" onSubmit={submit}>
          {error && <Notice>{error}</Notice>}
          <label className="field"><span>{t("pairing.codeLabel")}</span><div><KeyRound size={18} /><input value={formattedCode} onChange={(event) => setUserCode(event.target.value)} autoComplete="one-time-code" inputMode="text" placeholder={t("pairing.codePlaceholder")} minLength={9} maxLength={9} pattern="[A-Z2-9]{4}-[A-Z2-9]{4}" dir="ltr" autoFocus required disabled={loading} /></div></label>

          {isGlobalAdmin ? <>
            <label className="field">
              <span>{t("pairing.categoryLabel")}</span>
              <div><FolderKey size={18} /><Select ref={categorySelectRef} value={categoryID} onChange={(value) => { setCategoryID(value); setError(""); }} required disabled={categoryState !== "ready" || categories.length === 0 || loading} aria-describedby="pairing-category-hint" options={[
                { value: "", label: t("pairing.categoryPlaceholder") },
                ...categories.map((category) => ({ value: category.id, label: category.name })),
              ]} /></div>
              <small id="pairing-category-hint">{t("pairing.categoryAssignmentHint")}</small>
            </label>
            {categoryState === "loading" && <div className="pairing-card__category-loading" role="status" aria-live="polite"><LoaderCircle className="spin" size={17} /><span>{t("pairing.categoryLoading")}</span></div>}
            {categoryState === "ready" && categories.length === 0 && <Notice tone="warning">{t("pairing.categoryEmpty")}</Notice>}
            {(categoryState === "error" || categoryState === "forbidden") && <div className="pairing-card__category-state"><Notice>{categoryError}{categoryState === "error" && <Button type="button" variant="ghost" onClick={() => void loadCategories()}><RefreshCw size={16} /> {t("common.actions.tryAgain")}</Button>}</Notice></div>}
          </> : sessionCategory ? <div className="field pairing-card__category" aria-describedby="pairing-category-hint">
            <span>{t("pairing.assignedCategory")}</span>
            <div className="pairing-card__category-badge"><FolderKey size={17} /><strong>{sessionCategory.name}</strong></div>
            <small id="pairing-category-hint">{t("pairing.categoryAssignmentHint")}</small>
          </div> : <Notice>{t("pairing.categoryUnavailable")}</Notice>}

          <label className="field"><span>{t("pairing.deviceNameLabel")}</span><div><Tag size={18} /><input value={deviceName} onChange={(event) => setDeviceName(event.target.value)} maxLength={120} placeholder={t("pairing.deviceNamePlaceholder")} disabled={loading} /></div></label>
          <label className="field pairing-card__note"><span>{t("pairing.internalNoteLabel")}</span><div><NotebookPen size={18} /><textarea value={internalNote} onChange={(event) => setInternalNote(event.target.value)} maxLength={500} rows={3} placeholder={t("pairing.internalNotePlaceholder")} disabled={loading} /></div><small>{t("pairing.internalNoteHint")}</small></label>
          <Button type="submit" loading={loading} disabled={!canApprove} aria-label={loading ? t("pairing.approvingDevice") : undefined}>{t("pairing.approveDevice")} <ArrowRight size={18} /></Button>
          <span className="visually-hidden" role="status" aria-live="polite">{loading ? t("pairing.approvingDevice") : ""}</span>
        </form>
      </>}
    </section>
    <footer className="auth-footer">{t("pairing.approvingAs", { name: activeProfile?.name ?? t("pairing.managerFallback") })}</footer>
  </div></main>;
}
