import { ArrowLeft, ArrowRight, Eye, EyeOff, LockKeyhole, LogOut, ShieldCheck, Sparkles } from "lucide-react";
import { useEffect, useState, type CSSProperties, type FormEvent } from "react";
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

export function ProfileGate({ maintenanceMessage = null }: { maintenanceMessage?: string | null }) {
  const { account, selectProfile, logout, refreshAccount } = useAuth();
  const [selected, setSelected] = useState<Profile | null>(null);
  const [pin, setPin] = useState("");
  const [showPin, setShowPin] = useState(false);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    const interval = window.setInterval(() => { void refreshAccount(); }, 30_000);
    return () => window.clearInterval(interval);
  }, [refreshAccount]);

  async function choose(profile: Profile) {
    if (maintenanceMessage !== null && !profile.canManage) return;
    if (!profile.accessible) return;
    if (profile.hasPin) {
      setSelected(profile);
      setPin("");
      setShowPin(false);
      setError("");
      return;
    }
    setLoading(true);
    setError("");
    try {
      await selectProfile(profile);
    } catch (cause) {
      setError(notifyError(cause, t("profiles.openFailure"), t("profiles.unavailableTitle")));
    } finally {
      setLoading(false);
    }
  }

  async function submitPin(event: FormEvent) {
    event.preventDefault();
    if (!selected) return;
    setLoading(true);
    setError("");
    try {
      await selectProfile(selected, pin);
    } catch (cause) {
      setError(notifyError(cause, t("profiles.openFailure"), t("profiles.unavailableTitle")));
    } finally {
      setLoading(false);
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
      <div className="profile-grid">
        {account?.profiles.map((profile, index) => {
          const unavailable = unavailableReason(profile, maintenanceMessage !== null);
          return <button key={profile.id} className={`profile-card ${unavailable ? "profile-card--unavailable" : ""}`} onClick={() => void choose(profile)} disabled={loading || unavailable !== null} aria-describedby={unavailable ? `profile-${profile.id}-status` : undefined} style={{ "--delay": `${index * 70}ms` } as CSSProperties}>
            <span className="profile-card__avatar"><span className="profile-card__glow" /><img src={profile.avatar.url} alt="" />{profile.hasPin && <i><LockKeyhole size={14} /></i>}</span>
            <strong>{profile.name}</strong>
            <small id={unavailable ? `profile-${profile.id}-status` : undefined}>{unavailable ?? (profile.isChild ? t("profiles.child") : profile.canManage ? t("profiles.admin") : t("profiles.standard"))}</small>
          </button>;
        })}
      </div>
      <div className="profile-gate__secure"><ShieldCheck size={16} /> {t("profiles.privacy")}</div>
    </section>
    {selected && <Modal onClose={() => setSelected(null)} className="pin-modal">
      <button className="pin-modal__back" onClick={() => setSelected(null)}><ArrowLeft size={17} /> {t("common.back")}</button>
      <img src={selected.avatar.url} alt="" />
      <span>{t("profiles.protected")}</span><h2>{t("profiles.hello", { name: selected.name })}</h2><p>{t("profiles.pinBody")}</p>
      <form onSubmit={submitPin}>
        {error && <Notice>{error}</Notice>}
        <div className="pin-input-wrap"><input className="pin-input" value={pin} onChange={(event) => setPin(event.target.value.replace(/\D/g, "").slice(0, 8))} type={showPin ? "text" : "password"} inputMode="numeric" autoComplete="one-time-code" pattern="[0-9]{4,8}" placeholder="••••" autoFocus required /><IconButton type="button" label={showPin ? t("profiles.hidePin") : t("profiles.showPin")} aria-pressed={showPin} onClick={() => setShowPin((value) => !value)}>{showPin ? <EyeOff size={18} /> : <Eye size={18} />}</IconButton></div>
        <Button type="submit" loading={loading}>{t("profiles.open")} <ArrowRight size={18} /></Button>
      </form>
    </Modal>}
  </main>;
}
