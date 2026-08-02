import { ArrowRight, Eye, EyeOff, KeyRound, LockKeyhole, Server, Sparkles, UserRound } from "lucide-react";
import { useState, type FormEvent } from "react";
import { api } from "../api";
import { useAuth } from "../auth";
import { Button, Notice, RivuneMark } from "../components";
import { notifyError } from "../notifications";
import { translate as t } from "../i18n";

export function AuthBackdrop() {
  return <div className="auth-backdrop" aria-hidden="true"><div className="auth-backdrop__orb auth-backdrop__orb--one" /><div className="auth-backdrop__orb auth-backdrop__orb--two" /><div className="auth-backdrop__grain" /></div>;
}

export function SetupPage() {
  const { discovery, rediscover, login } = useAuth();
  const [step, setStep] = useState<"welcome" | "form" | "done">("welcome");
  const [setupToken, setSetupToken] = useState("");
  const [instanceName, setInstanceName] = useState(() => discovery?.name === "Rivune" ? t("auth.defaultInstanceName") : discovery?.name ?? t("auth.defaultInstanceName"));
  const [profileName, setProfileName] = useState(() => t("auth.defaultProfileName"));
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  async function submit(event: FormEvent) {
    event.preventDefault();
    setLoading(true);
    setError("");
    try {
      await api.setup({ instanceName, admin: { username, password }, profileName }, setupToken);
      await login(username, password);
      await rediscover();
      setStep("done");
    } catch (cause) {
      setError(notifyError(cause, t("auth.setupFailure"), t("auth.setupFailureTitle")));
    } finally {
      setLoading(false);
    }
  }

  return <main className="auth-page"><AuthBackdrop /><div className="auth-shell">
    <RivuneMark />
    {step === "welcome" ? <section className="welcome-card page-enter">
      <span className="welcome-card__badge"><Sparkles size={15} /> {t("auth.welcomeBadge")}</span>
      <h1>{t("auth.welcomeTitleLead")}<br /><em>{t("auth.welcomeTitleAccent")}</em></h1>
      <p>{t("auth.welcomeBody")}</p>
      <Button onClick={() => setStep("form")}>{t("auth.configure")} <ArrowRight size={18} /></Button>
      <div className="welcome-card__trust"><span><LockKeyhole size={16} /> {t("auth.selfHosted")}</span><span><Server size={16} /> {t("auth.noCloud")}</span></div>
    </section> : step === "form" ? <section className="auth-card page-enter">
      <div className="auth-card__header"><span>{t("auth.setupEyebrow")}</span><h1>{t("auth.setupTitle")}</h1><p>{t("auth.setupBody")}</p></div>
      <form onSubmit={submit} className="form-stack">
        {error && <Notice>{error}</Notice>}
        <label className="field"><span>{t("auth.instanceName")}</span><div><Server size={18} /><input value={instanceName} onChange={(event) => setInstanceName(event.target.value)} required minLength={1} maxLength={120} /></div></label>
        <div className="form-grid form-grid--two">
          <label className="field"><span>{t("auth.adminAccount")}</span><div><UserRound size={18} /><input value={username} onChange={(event) => setUsername(event.target.value)} autoComplete="username" required minLength={3} /></div></label>
          <label className="field"><span>{t("auth.firstProfile")}</span><div><Sparkles size={18} /><input value={profileName} onChange={(event) => setProfileName(event.target.value)} required /></div></label>
        </div>
        <label className="field"><span>{t("auth.adminPassword")}</span><div><LockKeyhole size={18} /><input type="password" value={password} onChange={(event) => setPassword(event.target.value)} autoComplete="new-password" required minLength={12} /></div><small>{t("auth.passwordHint")}</small></label>
        <label className="field"><span>{t("auth.setupToken")}</span><div><KeyRound size={18} /><input type="password" value={setupToken} onChange={(event) => setSetupToken(event.target.value)} required autoComplete="off" placeholder={t("auth.setupTokenHint")} /></div></label>
        <Button type="submit" loading={loading}>{t("auth.createSpace")} <ArrowRight size={18} /></Button>
      </form>
    </section> : <section className="welcome-card page-enter"><span className="success-orbit"><Sparkles /></span><h1>{t("auth.readyTitle")}</h1><p>{t("auth.readyBody")}</p></section>}
    <footer className="auth-footer">Rivune · {t("auth.openSource")}</footer>
  </div></main>;
}

export function LoginPage({ onBack }: { onBack?: () => void }) {
  const { discovery, login } = useAuth();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  async function submit(event: FormEvent) {
    event.preventDefault();
    setLoading(true);
    setError("");
    try {
      await login(username, password);
    } catch (cause) {
      setError(notifyError(cause, t("auth.loginFailure"), t("auth.loginFailureTitle")));
    } finally {
      setLoading(false);
    }
  }

  return <main className="auth-page"><AuthBackdrop /><div className="auth-shell auth-shell--login">
    <RivuneMark />
    <section className="auth-card auth-card--login page-enter">
      <div className="auth-card__server"><span className="status-dot" /> {t("auth.connectedTo", { server: discovery?.name ?? "Rivune" })}</div>
      <div className="auth-card__header"><span>{t("auth.ownerAccess")}</span><h1>{t("auth.ownerTitle")}</h1><p>{t("auth.ownerBody")}</p></div>
      <form onSubmit={submit} className="form-stack">
        {error && <Notice>{error}</Notice>}
        <label className="field"><span>{t("auth.username")}</span><div><UserRound size={18} /><input value={username} onChange={(event) => setUsername(event.target.value)} autoComplete="username" autoFocus required /></div></label>
        <label className="field"><span>{t("auth.password")}</span><div><LockKeyhole size={18} /><input type={showPassword ? "text" : "password"} value={password} onChange={(event) => setPassword(event.target.value)} autoComplete="current-password" required /><button type="button" className="field__action" onClick={() => setShowPassword((value) => !value)} aria-label={showPassword ? t("auth.hidePassword") : t("auth.showPassword")}>{showPassword ? <EyeOff size={18} /> : <Eye size={18} />}</button></div></label>
        {onBack && <Button type="button" variant="ghost" onClick={onBack}>{t("pairing.backToPairing")}</Button>}
        <Button type="submit" loading={loading}>{t("auth.signIn")} <ArrowRight size={18} /></Button>
      </form>
    </section>
    <footer className="auth-footer">{t("auth.secureConnection")}</footer>
  </div></main>;
}
