import { useEffect, useState } from "react";
import { APIError, RivuneTvClient } from "./api";
import { Brand, ErrorPanel, Modal, Screen, Spinner, TvButton } from "./components";
import { focusFirst } from "./focus";
import { t } from "./i18n";
import type { Account, Profile } from "./types";

function avatar(client: RivuneTvClient, profile: Profile): string | undefined {
  return client.resolveArtworkUrl(profile.avatar.url) ?? undefined;
}

export function ProfileGate({ client, account, onSelected, onSignedOut }: { client: RivuneTvClient; account: Account; onSelected: (profile: Profile, account: Account) => void; onSignedOut: () => void }) {
  const [pinProfile, setPinProfile] = useState<Profile | null>(null);
  const [pin, setPin] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => { focusFirst(pinProfile ? document.querySelector(".tv-modal") ?? document : document); }, [pinProfile]);

  async function choose(profile: Profile, suppliedPin?: string) {
    if (!profile.accessible) {
      setError(t("profiles.unavailable"));
      return;
    }
    if (profile.hasPin && suppliedPin === undefined) {
      setPinProfile(profile);
      setPin("");
      return;
    }
    setBusy(true);
    setError("");
    try {
      await client.selectProfile(profile.id, suppliedPin);
      const current = await client.currentAccount();
      onSelected(profile, current);
    } catch (cause) {
      setError(cause instanceof APIError ? cause.message : t("error.profile"));
    } finally {
      setBusy(false);
    }
  }

  async function signOut() {
    setBusy(true);
    try { await client.logout(); } finally { onSignedOut(); }
  }

  return <Screen className="tv-centered">
    <section className="tv-onboarding" style={{ width: 1100 }}>
      <Brand />
      <h1>{t("profiles.title")}</h1>
      {busy ? <Spinner /> : <div className="tv-profile-grid">
        {account.profiles.map((profile) => <button key={profile.id} type="button" className="tv-profile" disabled={!profile.accessible} onClick={() => void choose(profile)}>
          <span className="tv-profile__avatar">{avatar(client, profile) ? <img src={avatar(client, profile)} alt="" /> : profile.name.slice(0, 1).toUpperCase()}</span>
          <strong>{profile.name}</strong><small>{profile.hasPin ? "PIN" : ""}</small>
        </button>)}
      </div>}
      <div className="tv-actions" style={{ justifyContent: "center", marginTop: 28 }}><TvButton tone="quiet" onClick={() => void signOut()}>{t("common.signOut")}</TvButton></div>
      {pinProfile && <Modal title={t("pin.title", { name: pinProfile.name })} onClose={() => setPinProfile(null)}>
        <label className="tv-field"><span>{t("pin.label")}</span><input className="tv-input" type="password" inputMode="numeric" value={pin} maxLength={32} autoComplete="off" onChange={(event) => setPin(event.target.value)} onKeyDown={(event) => { if (event.key === "Enter" && pin) void choose(pinProfile, pin); }} /></label>
        <div className="tv-actions" style={{ marginTop: 24 }}><TvButton tone="primary" disabled={!pin} onClick={() => void choose(pinProfile, pin)}>{t("pin.submit")}</TvButton><TvButton onClick={() => setPinProfile(null)}>{t("common.cancel")}</TvButton></div>
      </Modal>}
      {error && <ErrorPanel message={error} onClose={() => setError("")} />}
    </section>
  </Screen>;
}
