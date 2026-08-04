import { ArrowLeft, ArrowRight, Eye, EyeOff, LockKeyhole, LogOut, ShieldCheck, Sparkles, UserRound } from "lucide-react";
import { useCallback, useEffect, useRef, useState, type CSSProperties, type FormEvent, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { useAuth } from "../auth";
import { Button, IconButton, Modal, Notice, RivuneMark } from "../components";
import { notifyError } from "../notifications";
import { translate as t } from "../i18n";
import type { Profile } from "../types";

function unavailableReason(profile: Profile, maintenanceActive: boolean): string | null {
  if (maintenanceActive && !profile.canManage) return t("profiles.maintenanceBlocked");
  if (profile.accessible) return null;
  if (!profile.enabled) return t("profiles.disabled");
  try {
    const parts = new Intl.DateTimeFormat("en-CA", {
      timeZone: profile.accessTimezone,
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
    }).formatToParts(new Date());
    const values = Object.fromEntries(parts.map((part) => [part.type, part.value]));
    const localDate = `${values.year}-${values.month}-${values.day}`;
    if (profile.availableFrom && localDate < profile.availableFrom) return t("profiles.notActiveYet");
    if (profile.availableUntil && localDate > profile.availableUntil) return t("profiles.expired");
  } catch {
    return t("profiles.unavailable");
  }
  return profile.accessStartTime ? t("profiles.outsideHours") : t("profiles.unavailable");
}
function ProfileImage({ profile }: { profile: Profile }) {
  const [failed, setFailed] = useState(false);

  useEffect(() => setFailed(false), [profile.avatar.url]);

  return failed
    ? <UserRound className="profile-avatar-fallback" aria-hidden="true" />
    : <img src={profile.avatar.url} alt="" onError={() => setFailed(true)} />;
}

function moveProfileFocus(event: ReactKeyboardEvent<HTMLDivElement>) {
  if (!["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Home", "End"].includes(event.key)) return;
  const current = (event.target as HTMLElement).closest<HTMLButtonElement>(".profile-card");
  if (!current) return;
  const buttons = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>(".profile-card:not(:disabled)"));
  if (buttons.length < 2) return;
  if (event.key === "Home" || event.key === "End") {
    event.preventDefault();
    buttons[event.key === "Home" ? 0 : buttons.length - 1]?.focus();
    return;
  }

  const currentRect = current.getBoundingClientRect();
  const currentX = currentRect.left + currentRect.width / 2;
  const currentY = currentRect.top + currentRect.height / 2;
  const candidates = buttons.flatMap((button) => {
    if (button === current) return [];
    const rect = button.getBoundingClientRect();
    const dx = rect.left + rect.width / 2 - currentX;
    const dy = rect.top + rect.height / 2 - currentY;
    const inDirection = event.key === "ArrowLeft" ? dx < 0
      : event.key === "ArrowRight" ? dx > 0
        : event.key === "ArrowUp" ? dy < 0
          : dy > 0;
    return inDirection ? [{ button, distance: dx * dx + dy * dy }] : [];
  }).sort((a, b) => a.distance - b.distance);
  if (!candidates[0]) return;
  event.preventDefault();
  candidates[0].button.focus();
}


export function ProfileGate({ maintenanceMessage = null }: { maintenanceMessage?: string | null }) {
  const { account, selectProfile, logout, refreshAccount } = useAuth();
  const [selectedProfileID, setSelectedProfileID] = useState<string | null>(null);
  const [pin, setPin] = useState("");
  const [showPin, setShowPin] = useState(false);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const loadingRef = useRef(false);
  const openerRef = useRef<HTMLButtonElement | null>(null);
  const selected = account?.profiles.find((profile) => profile.id === selectedProfileID) ?? null;

  const closePin = useCallback(() => {
    setSelectedProfileID(null);
    setPin("");
    setShowPin(false);
    setError("");
    const opener = openerRef.current;
    openerRef.current = null;
    window.requestAnimationFrame(() => {
      if (opener?.isConnected) opener.focus({ preventScroll: true });
    });
  }, []);


  useEffect(() => {
    const interval = window.setInterval(() => { void refreshAccount(); }, 30_000);
    return () => window.clearInterval(interval);
  }, [refreshAccount]);
  useEffect(() => {
    if (!selectedProfileID) return;
    const current = account?.profiles.find((profile) => profile.id === selectedProfileID);
    if (!current || unavailableReason(current, maintenanceMessage !== null)) closePin();
  }, [account?.profiles, closePin, maintenanceMessage, selectedProfileID]);


  async function choose(profile: Profile, opener?: HTMLButtonElement) {
    if (unavailableReason(profile, maintenanceMessage !== null) || loadingRef.current) return;
    if (profile.hasPin) {
      openerRef.current = opener ?? null;
      setSelectedProfileID(profile.id);
      setPin("");
      setShowPin(false);
      setError("");
      return;
    }
    loadingRef.current = true;
    setLoading(true);
    setError("");
    try {
      await selectProfile(profile);
    } catch (cause) {
      setError(notifyError(cause, t("profiles.openFailure"), t("profiles.unavailableTitle")));
    } finally {
      loadingRef.current = false;
      setLoading(false);
    }
  }

  async function submitPin(event: FormEvent) {
    event.preventDefault();
    if (!selected || loadingRef.current) return;
    loadingRef.current = true;
    setLoading(true);
    setError("");
    try {
      await selectProfile(selected, pin);
    } catch (cause) {
      setError(notifyError(cause, t("profiles.openFailure"), t("profiles.unavailableTitle")));
    } finally {
      setLoading(false);
      loadingRef.current = false;
    }
  }

  return <main className="profile-gate">
    <div className="profile-gate__atmosphere" aria-hidden="true"><i /><i /><i /></div>
      <header><RivuneMark /><button onClick={() => void logout().catch((cause) => notifyError(cause, t("profiles.signOutFailure"), t("profiles.signOutFailureTitle")))} className="text-button"><LogOut size={17} /> {t("profiles.signOut")}</button></header>
    <section className="profile-gate__content page-enter">
      <span className="eyebrow"><Sparkles size={15} /> {t("profiles.eyebrow")}</span>
      <h1>{t("profiles.title")}</h1>
      <p>{t("profiles.body")}</p>
      {maintenanceMessage !== null && <Notice tone="warning"><span><strong>{t("profiles.maintenanceTitle")}</strong><br />{maintenanceMessage || t("profiles.maintenanceBody")}</span></Notice>}
      {error && !selected && <Notice>{error}</Notice>}
      <div className="profile-grid" onKeyDown={moveProfileFocus}>
        {account?.profiles.map((profile, index) => {
          const unavailable = unavailableReason(profile, maintenanceMessage !== null);
          const status = unavailable ?? (profile.isChild ? t("profiles.child") : profile.canManage ? t("profiles.admin") : t("profiles.standard"));
          const statusID = `profile-${profile.id}-status`;
          const descriptionID = profile.description ? `profile-${profile.id}-description` : undefined;
          const describedBy = [statusID, descriptionID].filter(Boolean).join(" ");
          return <button key={profile.id} className={`profile-card ${unavailable ? "profile-card--unavailable" : ""}`} onClick={(event) => void choose(profile, event.currentTarget)} disabled={loading && !unavailable} aria-disabled={unavailable ? true : undefined} aria-label={`${profile.name} ${status}`} aria-describedby={describedBy} style={{ "--delay": `${index * 70}ms` } as CSSProperties}>
            <span className="profile-card__avatar"><span className="profile-card__glow" /><ProfileImage profile={profile} />{profile.hasPin && <i><LockKeyhole size={14} /></i>}</span>
            <strong>{profile.name}</strong>
            <small id={statusID}>{status}</small>
            {profile.description && <span id={descriptionID} className="profile-card__description">{profile.description}</span>}
          </button>;
        })}
      </div>
      <div className="profile-gate__secure"><ShieldCheck size={16} /> {t("profiles.privacy")}</div>
    </section>
    {selected && <Modal onClose={closePin} className="pin-modal">
      <button className="pin-modal__back" onClick={closePin}><ArrowLeft size={17} /> {t("common.back")}</button>
      <span className="pin-modal__avatar"><ProfileImage profile={selected} /></span>
      <span>{t("profiles.protected")}</span><h2>{t("profiles.hello", { name: selected.name })}</h2><p>{t("profiles.pinBody")}</p>
      <form onSubmit={submitPin}>
        {error && <Notice>{error}</Notice>}
        <div className="pin-input-wrap"><input className="pin-input" value={pin} onChange={(event) => setPin(event.target.value.replace(/\D/g, "").slice(0, 8))} type={showPin ? "text" : "password"} inputMode="numeric" autoComplete="one-time-code" pattern="[0-9]{4,8}" placeholder="••••" dir="ltr" autoFocus required /><IconButton type="button" label={showPin ? t("profiles.hidePin") : t("profiles.showPin")} aria-pressed={showPin} onClick={() => setShowPin((value) => !value)}>{showPin ? <EyeOff size={18} /> : <Eye size={18} />}</IconButton></div>
        <Button type="submit" loading={loading}>{t("profiles.open")} <ArrowRight size={18} /></Button>
      </form>
    </Modal>}
  </main>;
}
