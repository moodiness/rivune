import { Activity, Bell, Boxes, Captions, Check, ChevronDown, ChevronUp, CircleStop, CircleUserRound, Cpu, Database, Eye, EyeOff, Film, GripVertical, HardDrive, ImagePlus, Languages, Layers3, LoaderCircle, MonitorSmartphone, Palette, Pencil, Plus, Radio, RefreshCw, Save, Send, Server, Settings2, Shield, Sparkles, Trash2, Upload, Users, WandSparkles, X } from "lucide-react";
import { useEffect, useRef, useState, type ChangeEvent, type FormEvent } from "react";
import { api } from "../api";
import { useAuth } from "../auth";
import { AddTile, Button, ConfirmDialog, EmptyState, IconButton, Modal, Notice, SectionHeading, Skeleton } from "../components";
import { notifyError, notifyErrorMessage, notifySuccess } from "../notifications";
import type { AddonManifest, AvatarPreset, Collection, CollectionFolder, CollectionSaveInput, CollectionSource, InstalledAddon, PlaybackActivity, PlaybackActivitySession, Profile, ProfileSession, SettingsValues } from "../types";

type AdminTab = "profiles" | "addons" | "collections" | "activity" | "settings";

const tabs: Array<{ id: AdminTab; label: string; description: string; icon: typeof Users; adminOnly?: boolean }> = [
  { id: "profiles", label: "Profiles", description: "People and access", icon: Users },
  { id: "addons", label: "Addons", description: "Content sources", icon: Boxes },
  { id: "collections", label: "Collections", description: "Curate the home", icon: Layers3 },
  { id: "activity", label: "Activity", description: "Playback and media", icon: Activity, adminOnly: true },
  { id: "settings", label: "Settings", description: "Playback and display", icon: Settings2 },
];

function countCodePoints(value: string) {
  let count = 0;
  for (let offset = 0; offset < value.length; count += 1) {
    const codePoint = value.codePointAt(offset);
    offset += codePoint !== undefined && codePoint > 0xffff ? 2 : 1;
  }
  return count;
}

export function AdminPage() {
  const { account, activeProfile } = useAuth();
  const canManage = Boolean(activeProfile?.canManage);
  const isAdmin = account?.user.role === "admin";
  const visibleTabs = tabs.filter((item) => !item.adminOnly || isAdmin);
  const [tab, setTab] = useState<AdminTab>(() => canManage ? "profiles" : "settings");

  useEffect(() => {
    if (!canManage) setTab("settings");
    else if (tab === "activity" && !isAdmin) setTab("profiles");
  }, [canManage, isAdmin, tab]);

  return <div className="standard-page admin-page page-enter">
    <SectionHeading eyebrow={canManage ? "Control room" : "Your space"} title={canManage ? "Administration." : "Preferences."} description={canManage ? "Shape Rivune for everyone who shares this server." : "Personalize this profile."} />
    <div className={`admin-layout ${canManage ? "" : "admin-layout--preferences"}`}>
      {canManage && <nav className="admin-tabs">{visibleTabs.map((item) => { const Icon = item.icon; return <button key={item.id} className={tab === item.id ? "is-active" : ""} onClick={() => setTab(item.id)}><span><Icon size={20} /></span><div><strong>{item.label}</strong><small>{item.description}</small></div><ChevronDown size={17} /></button>; })}</nav>}
      <section className="admin-panel">{tab === "profiles" ? <ProfilesAdmin /> : tab === "addons" ? <AddonsAdmin /> : tab === "collections" ? <CollectionsAdmin /> : tab === "activity" ? <ActivityAdmin /> : <SettingsAdmin />}</section>
    </div>
  </div>;
}

function ProfilesAdmin() {
  const { account, activeProfile, discovery, refreshAccount } = useAuth();
  const [profiles, setProfiles] = useState<Profile[]>(account?.profiles ?? []);
  const [presets, setPresets] = useState<AvatarPreset[]>([]);
  const [editing, setEditing] = useState<Profile | "new" | null>(null);
  const [name, setName] = useState("");
  const [pin, setPin] = useState("");
  const [showPin, setShowPin] = useState(false);
  const [isChild, setIsChild] = useState(false);
  const [enabled, setEnabled] = useState(true);
  const [availableFrom, setAvailableFrom] = useState("");
  const [availableUntil, setAvailableUntil] = useState("");
  const [dailyHours, setDailyHours] = useState(false);
  const [accessStartTime, setAccessStartTime] = useState("08:00");
  const [accessEndTime, setAccessEndTime] = useState("20:00");
  const accessTimezone = discovery?.timezone ?? "UTC";
  const [presetId, setPresetId] = useState("aurora");
  const [image, setImage] = useState<File | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [deleting, setDeleting] = useState<Profile | null>(null);
  const [sessionsProfile, setSessionsProfile] = useState<Profile | null>(null);
  const [profileSessions, setProfileSessions] = useState<ProfileSession[]>([]);
  const [sessionsLoading, setSessionsLoading] = useState(false);
  const [revokingSession, setRevokingSession] = useState<ProfileSession | null>(null);
  const [messagingSession, setMessagingSession] = useState<ProfileSession | null>(null);
  const [message, setMessage] = useState("");
  const [sendingMessage, setSendingMessage] = useState(false);
  const [messageError, setMessageError] = useState("");
  const fileRef = useRef<HTMLInputElement>(null);
  const messageTargetRef = useRef<{ profileId: string; sessionId: string; modalId: number } | null>(null);
  const pendingMessageRequestRef = useRef<{ profileId: string; sessionId: string; modalId: number } | null>(null);
  const nextMessageModalIdRef = useRef(0);
  const messageCharacterCount = countCodePoints(message);

  useEffect(() => { void api.avatarPresets().then((response) => setPresets(response.presets)).catch(() => undefined); }, []);

  function openEditor(profile: Profile | "new") {
    setEditing(profile);
    setName(profile === "new" ? "" : profile.name);
    setPin("");
    setShowPin(false);
    setIsChild(profile === "new" ? false : profile.isChild);
    setEnabled(profile === "new" ? true : profile.enabled);
    setAvailableFrom(profile === "new" ? "" : profile.availableFrom ?? "");
    setAvailableUntil(profile === "new" ? "" : profile.availableUntil ?? "");
    setDailyHours(profile === "new" ? false : Boolean(profile.accessStartTime && profile.accessEndTime));
    setAccessStartTime(profile === "new" ? "08:00" : profile.accessStartTime ?? "08:00");
    setAccessEndTime(profile === "new" ? "20:00" : profile.accessEndTime ?? "20:00");
    setPresetId(profile === "new" ? "aurora" : profile.avatar.presetId ?? "aurora");
    setImage(null);
    setError("");
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!editing) return;
    setSaving(true);
    setError("");
    const creating = editing === "new";
    try {
      let profile: Profile;
      if (editing === "new") {
        profile = await api.createProfile({
          name, isChild, pin: pin || undefined, enabled,
          availableFrom: availableFrom || undefined,
          availableUntil: availableUntil || undefined,
          accessStartTime: dailyHours ? accessStartTime : undefined,
          accessEndTime: dailyHours ? accessEndTime : undefined,
        });
      } else {
        const accessInput: {
          enabled?: boolean;
          availableFrom?: string | null;
          availableUntil?: string | null;
          accessStartTime?: string | null;
          accessEndTime?: string | null;
        } = {};
        if (enabled !== editing.enabled) accessInput.enabled = enabled;
        if ((availableFrom || null) !== editing.availableFrom) accessInput.availableFrom = availableFrom || null;
        if ((availableUntil || null) !== editing.availableUntil) accessInput.availableUntil = availableUntil || null;
        const nextStart = dailyHours ? accessStartTime : null;
        const nextEnd = dailyHours ? accessEndTime : null;
        if (nextStart !== editing.accessStartTime) accessInput.accessStartTime = nextStart;
        if (nextEnd !== editing.accessEndTime) accessInput.accessEndTime = nextEnd;
        profile = await api.updateProfile(editing.id, { name, isChild, ...(pin ? { pin } : {}), ...accessInput });
      }
      if (image) await api.uploadProfileAvatar(profile.id, image);
      else if (presetId !== profile.avatar.presetId) await api.setProfileAvatar(profile.id, presetId);
      const next = await api.profiles();
      setProfiles(next.profiles);
      await refreshAccount();
      setEditing(null);
      notifySuccess(creating ? `${profile.name} is ready.` : `${profile.name} has been updated.`, creating ? "Profile created" : "Profile saved");
    } catch (cause) {
      setError(notifyError(cause, "The profile could not be saved."));
    } finally {
      setSaving(false);
    }
  }

  async function remove(profile: Profile) {
    try {
      await api.deleteProfile(profile.id);
      setProfiles((values) => values.filter((value) => value.id !== profile.id));
      setDeleting(null);
      await refreshAccount();
      notifySuccess(`${profile.name} has been deleted.`, "Profile deleted");
    } catch (cause) {
      setError(notifyError(cause, "The profile could not be deleted."));
    }
  }

  async function openSessions(profile: Profile) {
    if (pendingMessageRequestRef.current) return;
    setSessionsProfile(profile);
    setProfileSessions([]);
    setSessionsLoading(true);
    setError("");
    try {
      setProfileSessions((await api.profileSessions(profile.id)).sessions);
    } catch (cause) {
      setError(notifyError(cause, "Connected sessions could not be loaded."));
    } finally {
      setSessionsLoading(false);
    }
  }

  function closeSessions() {
    if (pendingMessageRequestRef.current) return;
    messageTargetRef.current = null;
    setMessagingSession(null);
    setSessionsProfile(null);
  }

  function openSessionMessage(session: ProfileSession) {
    if (!sessionsProfile || pendingMessageRequestRef.current) return;
    messageTargetRef.current = {
      profileId: sessionsProfile.id,
      sessionId: session.id,
      modalId: ++nextMessageModalIdRef.current,
    };
    setSendingMessage(false);
    setMessagingSession(session);
    setMessage("");
    setMessageError("");
  }

  function closeSessionMessage() {
    if (pendingMessageRequestRef.current) return;
    messageTargetRef.current = null;
    setMessagingSession(null);
  }

  async function revokeSession(session: ProfileSession) {
    if (!sessionsProfile) return;
    try {
      await api.revokeProfileSession(sessionsProfile.id, session.id);
      setProfileSessions((values) => values.filter((value) => value.id !== session.id));
      setRevokingSession(null);
      notifySuccess(`${session.deviceName} has been signed out.`, "Session revoked");
    } catch (cause) {
      setError(notifyError(cause, "The connected session could not be revoked."));
    }
  }

  async function sendSessionMessage(event: FormEvent) {
    event.preventDefault();
    if (!sessionsProfile || !messagingSession || !message.trim() || messageCharacterCount > 500 || pendingMessageRequestRef.current) return;

    const target = messageTargetRef.current;
    if (!target || target.profileId !== sessionsProfile.id || target.sessionId !== messagingSession.id) return;

    const request = { ...target };
    pendingMessageRequestRef.current = request;
    const targetSession = messagingSession;
    const targetMessage = message;
    setSendingMessage(true);
    setMessageError("");

    const isCurrentRequest = () => {
      const currentTarget = messageTargetRef.current;
      return pendingMessageRequestRef.current === request
        && currentTarget?.profileId === request.profileId
        && currentTarget.sessionId === request.sessionId
        && currentTarget.modalId === request.modalId;
    };

    try {
      await api.sendProfileSessionNotification(request.profileId, request.sessionId, targetMessage);
      if (!isCurrentRequest()) return;
      notifySuccess(`Your message was sent to ${targetSession.deviceName}.`, "Message sent");
      messageTargetRef.current = null;
      setMessagingSession(null);
      setMessage("");
    } catch (cause) {
      if (!isCurrentRequest()) return;
      setMessageError(notifyError(cause, "The session message could not be sent."));
    } finally {
      if (pendingMessageRequestRef.current === request) {
        pendingMessageRequestRef.current = null;
        setSendingMessage(false);
      }
    }
  }

  return <div className="admin-section">
    <div className="admin-section__header"><div><span>Household</span><h2>Profiles</h2><p>Separate spaces, recommendations, and progress for every viewer.</p></div><Button onClick={() => openEditor("new")}><Plus size={18} /> New profile</Button></div>
    {error && <Notice>{error}</Notice>}
    <div className="profile-admin-grid">{profiles.map((profile) => <article key={profile.id} className="profile-admin-card"><div className="profile-admin-card__visual"><img src={profile.avatar.url} alt="" /><span className={profile.isChild ? "is-child" : ""}>{profile.isChild ? "Kids" : profile.canManage ? "Manager" : "Viewer"}</span></div><div><h3>{profile.name}</h3><p>{profile.hasPin ? "PIN protected" : "No PIN"}</p></div><div className="profile-admin-card__actions"><IconButton label={`Connected sessions for ${profile.name}`} onClick={() => void openSessions(profile)}><MonitorSmartphone size={17} /></IconButton><IconButton label={`Edit ${profile.name}`} onClick={() => openEditor(profile)}><Pencil size={17} /></IconButton>{profile.id !== activeProfile?.id && <IconButton label={`Delete ${profile.name}`} onClick={() => setDeleting(profile)}><Trash2 size={17} /></IconButton>}</div></article>)}</div>
    {editing && <Modal onClose={() => setEditing(null)} className="editor-modal profile-editor">
      <div className="editor-modal__heading">
        <span><CircleUserRound size={18} /> {editing === "new" ? "New profile" : "Edit profile"}</span>
        <h2>{editing === "new" ? "Create their space." : `Make ${editing.name} feel at home.`}</h2>
      </div>
      <form onSubmit={submit} className="form-stack">
        {error && <Notice>{error}</Notice>}
        <div className="avatar-editor">
          <button type="button" onClick={() => fileRef.current?.click()}>
            {image ? <img src={URL.createObjectURL(image)} alt="Selected avatar" /> : <img src={presets.find((preset) => preset.id === presetId)?.url ?? "/api/v1/profile-avatars/aurora"} alt="Selected avatar" />}
            <span><Upload size={17} /> Upload</span>
          </button>
          <input ref={fileRef} hidden type="file" accept="image/png,image/jpeg,image/webp" onChange={(event) => setImage(event.target.files?.[0] ?? null)} />
          <div>{presets.map((preset) => <button type="button" key={preset.id} className={presetId === preset.id && !image ? "is-active" : ""} aria-label={preset.name} onClick={() => { setPresetId(preset.id); setImage(null); }}><img src={preset.url} alt="" /></button>)}</div>
        </div>
        <div className="form-grid form-grid--two">
          <label className="field"><span>Name</span><div><CircleUserRound size={18} /><input value={name} onChange={(event) => setName(event.target.value)} required /></div></label>
          <label className="field"><span>PIN (optional)</span><div><Shield size={18} /><input type={showPin ? "text" : "password"} inputMode="numeric" value={pin} onChange={(event) => setPin(event.target.value)} placeholder={editing === "new" ? "4–8 digits" : "Leave blank to keep current"} /><IconButton type="button" label={showPin ? "Hide PIN" : "Show PIN"} onClick={() => setShowPin((value) => !value)}>{showPin ? <EyeOff size={17} /> : <Eye size={17} />}</IconButton></div></label>
          <label className="toggle-field"><input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} /><span><i /><div><strong>Enabled</strong><small>Allow this profile to be selected</small></div></span></label>
          <label className="toggle-field"><input type="checkbox" checked={isChild} onChange={(event) => setIsChild(event.target.checked)} /><span><i /><div><strong>Kids profile</strong><small>Safer defaults and filtered content</small></div></span></label>
        </div>
        <section className="profile-access-editor">
          <div><strong>Availability</strong><p>Dates are inclusive in {accessTimezone}. Leave both blank for no date limit.</p></div>
          <div className="form-grid form-grid--two">
            <label className="field"><span>From (optional)</span><div><input type="date" value={availableFrom} max={availableUntil || undefined} onChange={(event) => setAvailableFrom(event.target.value)} /></div></label>
            <label className="field"><span>Until (optional)</span><div><input type="date" value={availableUntil} min={availableFrom || undefined} onChange={(event) => setAvailableUntil(event.target.value)} /></div></label>
          </div>
          <label className="toggle-field"><input type="checkbox" checked={dailyHours} onChange={(event) => setDailyHours(event.target.checked)} /><span><i /><div><strong>Daily access hours</strong><small>Limit profile access every day</small></div></span></label>
          {dailyHours && <><div className="form-grid form-grid--two">
            <label className="field"><span>Start</span><div><input type="time" value={accessStartTime} onChange={(event) => setAccessStartTime(event.target.value)} required /></div></label>
            <label className="field"><span>End</span><div><input type="time" value={accessEndTime} onChange={(event) => setAccessEndTime(event.target.value)} required /></div></label>
          </div><p className="profile-access-editor__hint">Start is included and end is excluded. An end before the start creates an overnight window.</p></>}
        </section>
        <div className="modal-actions"><Button type="button" variant="ghost" onClick={() => setEditing(null)}>Cancel</Button><Button type="submit" loading={saving}><Save size={18} /> Save profile</Button></div>
      </form>
    </Modal>}
    {sessionsProfile && <Modal onClose={closeSessions} className="editor-modal profile-sessions-modal">
      <div className="editor-modal__heading">
        <span><MonitorSmartphone size={18} /> Connected sessions</span>
        <h2>{sessionsProfile.name}</h2>
        <p>Devices currently using this profile. Revoking one signs that device out of Rivune.</p>
      </div>
      {error && <Notice>{error}</Notice>}
      {sessionsLoading ? <div className="profile-session-list"><Skeleton /><Skeleton /></div>
        : profileSessions.length === 0
          ? <EmptyState icon={<MonitorSmartphone size={40} />} title="No active session" description="No device is currently connected to this profile." />
          : <div className="profile-session-list">{profileSessions.map((session) =>
            <article key={session.id} className="profile-session">
              <span><MonitorSmartphone size={20} /></span>
              <div>
                <strong>{session.deviceName}</strong>
                <small>{session.platform} · Last active {new Date(session.lastSeenAt).toLocaleString()}</small>
                <SessionIPAddress session={session} />
              </div>
              <div className="profile-session__actions">
                {session.current && <i>Current device</i>}
                <Button variant="secondary" disabled={sendingMessage} onClick={() => openSessionMessage(session)}><Bell size={15} /> Message</Button>
                {!session.current && <Button variant="secondary" onClick={() => setRevokingSession(session)}>Revoke</Button>}
              </div>
            </article>,
          )}</div>}
    </Modal>}
    {sessionsProfile && messagingSession && <Modal onClose={closeSessionMessage} className="editor-modal session-message-modal">
      <div className="editor-modal__heading">
        <span><Bell size={18} /> Session message</span>
        <h2>{messagingSession.deviceName}</h2>
        <p>This notification will appear only on this connected device.</p>
      </div>
      <form className="form-stack" onSubmit={sendSessionMessage}>
        {messageError && <Notice>{messageError}</Notice>}
        <label className="field">
          <span>Message</span>
          <textarea autoFocus required disabled={sendingMessage} rows={5} value={message} onChange={(event) => setMessage(event.target.value)} placeholder="Write a message for this device…" />
          <small>{messageCharacterCount}/500 characters</small>
        </label>
        <div className="modal-actions modal-actions--sticky">
          <Button type="button" variant="ghost" disabled={sendingMessage} onClick={closeSessionMessage}>Cancel</Button>
          <Button type="submit" loading={sendingMessage} disabled={sendingMessage || !message.trim() || messageCharacterCount > 500}><Send size={17} /> Send message</Button>
        </div>
      </form>
    </Modal>}
    {sessionsProfile && revokingSession && <ConfirmDialog title={`Revoke ${revokingSession.deviceName}?`} description="This device will be signed out and must authenticate again." confirmLabel="Revoke session" onCancel={() => setRevokingSession(null)} onConfirm={() => void revokeSession(revokingSession)} />}
    {deleting && <ConfirmDialog title={`Delete ${deleting.name}?`} description="Their preferences and watch progress will be permanently removed. This cannot be undone." confirmLabel="Delete profile" onCancel={() => setDeleting(null)} onConfirm={() => void remove(deleting)} />}
  </div>;
}

function SessionIPAddress({ session }: { session: ProfileSession }) {
  const [revealed, setRevealed] = useState(false);
  if (!session.ipAddress) return <span className="profile-session__ip profile-session__ip--empty">IP unavailable</span>;
  return <span className="profile-session__ip">
    <code className={revealed ? "is-visible" : ""}>{session.ipAddress}</code>
    <IconButton
      type="button"
      label={`${revealed ? "Hide" : "Show"} IP address for ${session.deviceName}`}
      aria-pressed={revealed}
      onClick={() => setRevealed((value) => !value)}
    >
      {revealed ? <EyeOff size={14} /> : <Eye size={14} />}
    </IconButton>
  </span>;
}

function AddonsAdmin() {
  const { account, activeProfile } = useAuth();
  const profiles = account?.profiles.filter((profile) => account.user.role === "admin" || profile.canManage) ?? [];
  const [addons, setAddons] = useState<InstalledAddon[]>([]);
  const [transportUrl, setTransportUrl] = useState("");
  const [installProfileIds, setInstallProfileIds] = useState<string[]>(() => activeProfile ? [activeProfile.id] : []);
  const [loading, setLoading] = useState(true);
  const [working, setWorking] = useState("");
  const [error, setError] = useState("");
  const [deleting, setDeleting] = useState<InstalledAddon | null>(null);
  const [editingAddon, setEditingAddon] = useState<InstalledAddon | null>(null);
  const [editTransportUrl, setEditTransportUrl] = useState("");
  const [editProfileIds, setEditProfileIds] = useState<string[]>([]);
  const [draggedAddonIndex, setDraggedAddonIndex] = useState<number | null>(null);
  const [reordering, setReordering] = useState(false);
  const reorderInFlight = useRef(false);

  async function load() {
    setLoading(true);
    try { setAddons((await api.addons()).addons); } catch (cause) { setError(notifyError(cause, "Addons could not be loaded.", "Addons unavailable")); } finally { setLoading(false); }
  }
  useEffect(() => { void load(); }, []);
  useEffect(() => {
    if (installProfileIds.length === 0 && activeProfile) setInstallProfileIds([activeProfile.id]);
  }, [activeProfile, installProfileIds.length]);

  function openAddonEditor(addon: InstalledAddon) {
    setEditingAddon(addon);
    setEditTransportUrl(addon.transportUrl);
    setEditProfileIds(addon.profileIds);
    setError("");
  }

  async function saveAddon(event: FormEvent) {
    event.preventDefault();
    if (!editingAddon || !activeProfile || editProfileIds.length === 0) return;
    setWorking(editingAddon.id);
    setError("");
    try {
      const updated = await api.updateAddon(editingAddon.id, editTransportUrl, editProfileIds);
      setAddons((values) => updated.profileIds.includes(activeProfile.id)
        ? values.map((value) => value.id === updated.id ? updated : value)
        : values.filter((value) => value.id !== updated.id));
      setEditingAddon(null);
      notifySuccess(`${updated.manifest.name} and its profile access have been updated.`, "Addon saved");
    } catch (cause) {
      setError(notifyError(cause, "The addon could not be saved."));
    } finally {
      setWorking("");
    }
  }

  async function install(event: FormEvent) {
    event.preventDefault();
    setWorking("install");
    setError("");
    try {
      const installed = await api.installAddon(transportUrl, installProfileIds);
      setTransportUrl("");
      await load();
      notifySuccess(`${installed.manifest.name} is ready to use.`, "Addon installed");
    } catch (cause) { setError(notifyError(cause, "The addon could not be installed.")); } finally { setWorking(""); }
  }

  async function refresh(id: string) {
    setWorking(id);
    try {
      const updated = await api.refreshAddon(id);
      await load();
      notifySuccess(`${updated.manifest.name} is up to date.`, "Addon refreshed");
    } catch (cause) {
      setError(notifyError(cause, "The addon could not be refreshed."));
    } finally {
      setWorking("");
    }
  }

  async function remove(addon: InstalledAddon) {
    setWorking(addon.id);
    try {
      await api.deleteAddon(addon.id);
      setAddons((values) => values.filter((value) => value.id !== addon.id));
      setDeleting(null);
      notifySuccess(`${addon.manifest.name} has been removed.`, "Addon removed");
    } catch (cause) {
      setError(notifyError(cause, "The addon could not be removed."));
    } finally {
      setWorking("");
    }
  }

  function reorderedAddons(fromIndex: number, toIndex: number) {
    if (fromIndex === toIndex || fromIndex < 0 || toIndex < 0 || fromIndex >= addons.length || toIndex >= addons.length) return null;
    const next = [...addons];
    const [addon] = next.splice(fromIndex, 1);
    next.splice(toIndex, 0, addon);
    return next;
  }

  function stageAddonMove(fromIndex: number, toIndex: number) {
    const next = reorderedAddons(fromIndex, toIndex);
    if (!next) return;
    setAddons(next);
    setDraggedAddonIndex(toIndex);
  }

  async function saveAddonOrder(next = addons) {
    if (reorderInFlight.current) return;
    reorderInFlight.current = true;
    setDraggedAddonIndex(null);
    setReordering(true);
    setError("");
    try {
      setAddons((await api.reorderAddons(next.map((addon) => addon.id))).addons);
      notifySuccess("The addon order has been saved.", "Order saved");
    } catch (cause) {
      setError(notifyError(cause, "The addon order could not be saved."));
      await load();
    } finally {
      reorderInFlight.current = false;
      setReordering(false);
    }
  }

  async function moveAddon(fromIndex: number, toIndex: number) {
    const next = reorderedAddons(fromIndex, toIndex);
    if (!next) return;
    setAddons(next);
    await saveAddonOrder(next);
  }

  const addonEditSaving = Boolean(editingAddon && working === editingAddon.id);
  return <div className="admin-section"><div className="admin-section__header"><div><span>Sources</span><h2>Addons</h2><p>Connect compatible providers to unlock catalogs, metadata, and streams.</p></div></div>
    <form className="install-addon" onSubmit={install}><div><WandSparkles size={21} /><input type="url" value={transportUrl} onChange={(event) => setTransportUrl(event.target.value)} placeholder="https://addon.example/manifest.json" required /></div><Button type="submit" loading={working === "install"}><Plus size={18} /> Install addon</Button></form>
    <ProfileAssignmentPicker profiles={profiles} selected={installProfileIds} onChange={setInstallProfileIds} legend="Available to" />
    {error && <Notice>{error}</Notice>}
    {loading ? <div className="addon-list">{[0, 1].map((value) => <Skeleton key={value} className="addon-skeleton" />)}</div> : addons.length ? <div className="addon-list">{addons.map((addon, addonIndex) => <AddonCard key={addon.id} addon={addon} index={addonIndex} total={addons.length} working={working === addon.id} reordering={reordering} dragging={draggedAddonIndex === addonIndex} onDragStart={(event) => { setDraggedAddonIndex(addonIndex); event.dataTransfer.effectAllowed = "move"; event.dataTransfer.setData("text/plain", String(addonIndex)); }} onDragEnter={(event) => { event.preventDefault(); if (draggedAddonIndex !== null) stageAddonMove(draggedAddonIndex, addonIndex); }} onDragOver={(event) => { if (draggedAddonIndex !== null) { event.preventDefault(); event.dataTransfer.dropEffect = "move"; } }} onDrop={(event) => { event.preventDefault(); void saveAddonOrder(); }} onDragEnd={() => { if (draggedAddonIndex !== null) void saveAddonOrder(); }} onMove={(toIndex) => void moveAddon(addonIndex, toIndex)} onRefresh={() => void refresh(addon.id)} onEdit={() => openAddonEditor(addon)} onRemove={() => setDeleting(addon)} />)}</div> : <EmptyState icon={<Boxes size={44} />} title="No addons installed" description="Paste a compatible manifest URL above to connect your first source." />}
    {editingAddon && <Modal onClose={() => { if (!addonEditSaving) setEditingAddon(null); }} className="editor-modal addon-edit-modal"><form onSubmit={saveAddon}><div className="editor-modal__heading"><span><Pencil size={18} /> Edit addon</span><h2>{editingAddon.manifest.name}</h2><p>Update the transport and profile access together.</p></div>{error && <Notice>{error}</Notice>}<label className="field"><span>Transport URL</span><div><WandSparkles size={18} /><input type="url" value={editTransportUrl} onChange={(event) => setEditTransportUrl(event.target.value)} placeholder="https://addon.example/manifest.json" required /></div></label><ProfileAssignmentPicker profiles={profiles} selected={editProfileIds} onChange={setEditProfileIds} legend="Available to" /><div className="modal-actions"><Button type="button" variant="ghost" disabled={addonEditSaving} onClick={() => setEditingAddon(null)}>Cancel</Button><Button type="submit" loading={addonEditSaving} disabled={editProfileIds.length === 0}><Save size={18} /> Save addon</Button></div></form></Modal>}
    {deleting && <ConfirmDialog title={`Remove ${deleting.manifest.name}?`} description="This provider and its catalogs will no longer be available to this profile." confirmLabel="Remove addon" loading={working === deleting.id} onCancel={() => setDeleting(null)} onConfirm={() => void remove(deleting)} />}
  </div>;
}

function AddonCard({ addon, index, total, working, reordering, dragging, onDragStart, onDragEnter, onDragOver, onDrop, onDragEnd, onMove, onRefresh, onEdit, onRemove }: {
  addon: InstalledAddon;
  index: number;
  total: number;
  working: boolean;
  reordering: boolean;
  dragging: boolean;
  onDragStart: React.DragEventHandler<HTMLButtonElement>;
  onDragEnter: React.DragEventHandler<HTMLElement>;
  onDragOver: React.DragEventHandler<HTMLElement>;
  onDrop: React.DragEventHandler<HTMLElement>;
  onDragEnd: React.DragEventHandler<HTMLButtonElement>;
  onMove: (toIndex: number) => void;
  onRefresh: () => void;
  onEdit: () => void;
  onRemove: () => void;
}) {
  const manifest: AddonManifest = addon.manifest;
  return <article className={`addon-card ${dragging ? "is-dragging" : ""}`} onDragEnter={onDragEnter} onDragOver={onDragOver} onDrop={onDrop}>
    <button type="button" className="addon-card__drag" draggable={!reordering && !working} disabled={reordering || working} onDragStart={onDragStart} onDragEnd={onDragEnd} aria-label={`Move ${manifest.name}`}><GripVertical /></button>
    <div className="addon-card__logo">{manifest.logo ? <img src={manifest.logo} alt="" /> : manifest.name.slice(0, 2).toUpperCase()}</div>
    <div className="addon-card__body"><div><h3>{manifest.name}</h3><span>v{manifest.version}</span>{manifest.behaviorHints?.p2p && <span className="addon-badge addon-badge--warn">P2P</span>}</div><p>{manifest.description || "No description provided."}</p><div>{manifest.types.map((type) => <i key={type}>{type}</i>)}</div></div>
    <div className="addon-card__actions">{working ? <LoaderCircle className="spin" /> : <><IconButton label={`Move ${manifest.name} up`} disabled={reordering || index === 0} onClick={() => onMove(index - 1)}><ChevronUp size={17} /></IconButton><IconButton label={`Move ${manifest.name} down`} disabled={reordering || index === total - 1} onClick={() => onMove(index + 1)}><ChevronDown size={17} /></IconButton><IconButton label={`Edit ${manifest.name}`} disabled={reordering} onClick={onEdit}><Pencil size={18} /></IconButton><IconButton label={`Refresh ${manifest.name}`} disabled={reordering} onClick={onRefresh}><RefreshCw size={18} /></IconButton><IconButton label={`Remove ${manifest.name}`} disabled={reordering} onClick={onRemove}><Trash2 size={18} /></IconButton></>}</div>
  </article>;
}

function ProfileAssignmentPicker({ profiles, selected, onChange, legend }: {
  profiles: Profile[];
  selected: string[];
  onChange: (profileIds: string[]) => void;
  legend: string;
}) {
  function toggle(profileID: string) {
    if (selected.includes(profileID)) {
      if (selected.length === 1) return;
      onChange(selected.filter((value) => value !== profileID));
      return;
    }
    onChange([...selected, profileID]);
  }

  return <fieldset className="profile-assignment">
    <legend>{legend}</legend>
    <div>{profiles.map((profile) => {
      const checked = selected.includes(profile.id);
      return <label key={profile.id} className={checked ? "is-selected" : ""}>
        <input type="checkbox" checked={checked} onChange={() => toggle(profile.id)} />
        <img src={profile.avatar.url} alt="" />
        <span><strong>{profile.name}</strong><small>{checked ? "Included" : "Not included"}</small></span>
        <i><Check size={14} /></i>
      </label>;
    })}</div>
    <small>Configuration changes stay synchronized across selected profiles.</small>
  </fieldset>;
}

const blankFolder = (): CollectionFolder => ({ title: "Featured", tileShape: "poster", sourceView: "merged", focusGifEnabled: false, hideTitle: false, sources: [] });
const blankCollection = (profileIds: string[] = []): CollectionSaveInput => ({ title: "New collection", heroEnabled: false, pinToTop: false, focusGlowEnabled: true, viewMode: "rows", folderCoverShape: "poster", folders: [blankFolder()], profileIds })

function CollectionsAdmin() {
  const { account, activeProfile } = useAuth();
  const profiles = account?.profiles.filter((profile) => account.user.role === "admin" || profile.canManage) ?? [];
  const [collections, setCollections] = useState<Collection[]>([]);
  const [editing, setEditing] = useState<Collection | "new" | null>(null);
  const [draft, setDraft] = useState<CollectionSaveInput>(blankCollection(activeProfile ? [activeProfile.id] : []));
  const [catalogs, setCatalogs] = useState<Array<{ addonId: string; manifestId: string; catalog: { type: string; id: string; name?: string } }>>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [transfer, setTransfer] = useState<"" | "export" | "import">("");
  const [success, setSuccess] = useState("");
  const [draggedFolderIndex, setDraggedFolderIndex] = useState<number | null>(null);
  const [draggedSource, setDraggedSource] = useState<{ folderIndex: number; sourceIndex: number } | null>(null);
  const [deleting, setDeleting] = useState<Collection | null>(null);
  const [draggedCollectionIndex, setDraggedCollectionIndex] = useState<number | null>(null);
  const [reordering, setReordering] = useState(false);
  const reorderInFlight = useRef(false);
  const importInput = useRef<HTMLInputElement>(null);

  async function load() {
    setLoading(true);
    try { setCollections((await api.collections()).collections); } catch (cause) { setError(notifyError(cause, "Collections could not be loaded.", "Collections unavailable")); } finally { setLoading(false); }
  }
  useEffect(() => { void load(); void api.addonCatalogs().then((response) => setCatalogs(response.catalogs)).catch(() => undefined); }, []);

  function openEditor(collection: Collection | "new") {
    setEditing(collection);
    if (collection === "new") setDraft(blankCollection(activeProfile ? [activeProfile.id] : []));
    else setDraft({ title: collection.title, backdropImageUrl: collection.backdropImageUrl, heroEnabled: collection.heroEnabled, pinToTop: collection.pinToTop, focusGlowEnabled: collection.focusGlowEnabled, viewMode: collection.viewMode, folderCoverShape: collection.folderCoverShape, folders: structuredClone(collection.folders), profileIds: collection.profileIds, expectedVersion: collection.version });
    setError("");
    setDraggedFolderIndex(null);
    setDraggedSource(null);
  }

  function updateFolder(index: number, patch: Partial<CollectionFolder>) {
    setDraft((current) => ({ ...current, folders: current.folders.map((folder, folderIndex) => folderIndex === index ? { ...folder, ...patch } : folder) }));
  }

  function moveFolder(fromIndex: number, toIndex: number) {
    if (fromIndex === toIndex) return;
    setDraft((current) => {
      if (fromIndex < 0 || fromIndex >= current.folders.length || toIndex < 0 || toIndex >= current.folders.length) return current;
      const folders = [...current.folders];
      const [folder] = folders.splice(fromIndex, 1);
      folders.splice(toIndex, 0, folder);
      return { ...current, folders };
    });
  }

  function moveSource(folderIndex: number, fromIndex: number, toIndex: number) {
    if (fromIndex === toIndex) return;
    setDraft((current) => {
      const folder = current.folders[folderIndex];
      if (!folder || fromIndex < 0 || fromIndex >= folder.sources.length || toIndex < 0 || toIndex >= folder.sources.length) return current;
      const sources = [...folder.sources];
      const [source] = sources.splice(fromIndex, 1);
      sources.splice(toIndex, 0, source);
      return {
        ...current,
        folders: current.folders.map((value, index) => index === folderIndex ? { ...value, sources } : value),
      };
    });
  }

  function updateSource(folderIndex: number, sourceIndex: number, source: CollectionSource) {
    const folder = draft.folders[folderIndex];
    updateFolder(folderIndex, { sources: folder.sources.map((current, index) => index === sourceIndex ? source : current) });
  }

  function addSource(folderIndex: number, kind: CollectionSource["kind"]) {
    const folder = draft.folders[folderIndex];
    let source: CollectionSource;
    if (kind === "addon_catalog") {
      const catalog = catalogs[0];
      source = { kind, title: catalog?.catalog.name || catalog?.catalog.id || "Addon catalog", addonCatalog: { addonId: catalog?.addonId ?? "", type: catalog?.catalog.type ?? "movie", catalogId: catalog?.catalog.id ?? "" } };
    } else if (kind === "trakt") source = { kind, title: "Trakt list", trakt: { listId: 1, mediaType: "movie", sortBy: "rank", sortHow: "asc" } };
    else source = { kind, title: "TMDB discover", tmdb: { sourceType: "discover", mediaType: "movie", sort: "popularity.desc", filters: {} } };
    updateFolder(folderIndex, { sources: [...folder.sources, source] });
  }

  function reorderedCollections(fromIndex: number, toIndex: number) {
    if (fromIndex === toIndex || fromIndex < 0 || toIndex < 0 || fromIndex >= collections.length || toIndex >= collections.length) return null;
    if (collections[fromIndex].pinToTop !== collections[toIndex].pinToTop) return null;
    const next = [...collections];
    const [collection] = next.splice(fromIndex, 1);
    next.splice(toIndex, 0, collection);
    return next;
  }

  function stageCollectionMove(fromIndex: number, toIndex: number) {
    const next = reorderedCollections(fromIndex, toIndex);
    if (!next) return;
    setCollections(next);
    setDraggedCollectionIndex(toIndex);
  }

  async function saveCollectionOrder(next = collections) {
    if (reorderInFlight.current) return;
    reorderInFlight.current = true;
    setDraggedCollectionIndex(null);
    setReordering(true);
    setError("");
    try {
      setCollections((await api.reorderCollections(next.map((collection) => collection.id))).collections);
      notifySuccess("The collection order has been saved.", "Order saved");
    } catch (cause) {
      setError(notifyError(cause, "The collection order could not be saved."));
      await load();
    } finally {
      reorderInFlight.current = false;
      setReordering(false);
    }
  }

  async function moveCollection(fromIndex: number, toIndex: number) {
    const next = reorderedCollections(fromIndex, toIndex);
    if (!next) return;
    setCollections(next);
    await saveCollectionOrder(next);
  }

  async function exportCollections() {
    setTransfer("export");
    setError("");
    setSuccess("");
    try {
      const document = await api.exportCollections();
      const url = URL.createObjectURL(new Blob([JSON.stringify(document, null, 2)], { type: "application/json" }));
      const anchor = window.document.createElement("a");
      anchor.href = url;
      anchor.download = `rivune-collections-${new Date().toISOString().slice(0, 10)}.json`;
      window.document.body.append(anchor);
      anchor.click();
      anchor.remove();
      URL.revokeObjectURL(url);
      setSuccess(`${document.collections.length} collection${document.collections.length === 1 ? "" : "s"} exported.`);
      notifySuccess(`${document.collections.length} collection${document.collections.length === 1 ? "" : "s"} exported.`, "Export complete");
    } catch (cause) {
      setError(notifyError(cause, "Collections could not be exported."));
    } finally {
      setTransfer("");
    }
  }

  async function importCollections(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    if (!file) return;
    setTransfer("import");
    setError("");
    setSuccess("");
    try {
      const document: unknown = JSON.parse(await file.text());
      const result = await api.importCollections(document);
      await load();
      setSuccess(`${result.imported} collection${result.imported === 1 ? "" : "s"} imported.`);
      notifySuccess(`${result.imported} collection${result.imported === 1 ? "" : "s"} imported.`, "Import complete");
    } catch (cause) {
      setError(cause instanceof SyntaxError
        ? notifyErrorMessage("The selected file does not contain valid JSON.", "Import failed")
        : notifyError(cause, "Collections could not be imported.", "Import failed"));
    } finally {
      event.target.value = "";
      setTransfer("");
    }
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    setSaving(true);
    setError("");
    const creating = editing === "new";
    try {
      if (editing === "new") await api.createCollection(draft);
      else if (editing) await api.updateCollection(editing.id, draft);
      await load();
      setEditing(null);
      notifySuccess(`${draft.title} has been ${creating ? "created" : "updated"}.`, creating ? "Collection created" : "Collection saved");
    } catch (cause) { setError(notifyError(cause, "The collection could not be saved.")); } finally { setSaving(false); }
  }

  async function remove(collection: Collection) {
    try {
      await api.deleteCollection(collection.id);
      setCollections((values) => values.filter((value) => value.id !== collection.id));
      setDeleting(null);
      notifySuccess(`${collection.title} has been deleted.`, "Collection deleted");
    } catch (cause) {
      setError(notifyError(cause, "The collection could not be deleted."));
    }
  }

  return <div className="admin-section"><><div className="admin-section__header"><div><span>Curation</span><h2>Collections</h2><p>Build the rows and worlds that make every profile's home unique.</p></div><div className="admin-section__actions"><input ref={importInput} type="file" accept="application/json,.json" hidden onChange={(event) => void importCollections(event)} /><Button type="button" variant="secondary" loading={transfer === "export"} disabled={Boolean(transfer)} onClick={() => void exportCollections()}><Save size={18} /> Export JSON</Button><Button type="button" variant="secondary" loading={transfer === "import"} disabled={Boolean(transfer)} onClick={() => importInput.current?.click()}><Upload size={18} /> Import JSON</Button><Button type="button" disabled={Boolean(transfer)} onClick={() => openEditor("new")}><Plus size={18} /> New collection</Button></div></div>{success && <Notice tone="success">{success}</Notice>}</>{error && <Notice>{error}</Notice>}{loading ? <div className="collection-admin-grid"><Skeleton className="collection-skeleton" /><Skeleton className="collection-skeleton" /></div> : collections.length ? <div className="collection-admin-grid">{collections.map((collection, collectionIndex) => <article key={collection.id} className={`collection-admin-card ${draggedCollectionIndex === collectionIndex ? "is-dragging" : ""}`} style={collection.backdropImageUrl ? { backgroundImage: `url(${collection.backdropImageUrl})` } : undefined} draggable={!reordering} onDragStart={(event) => { setDraggedCollectionIndex(collectionIndex); event.dataTransfer.effectAllowed = "move"; event.dataTransfer.setData("text/plain", String(collectionIndex)); }} onDragEnter={(event) => { event.preventDefault(); if (draggedCollectionIndex !== null) stageCollectionMove(draggedCollectionIndex, collectionIndex); }} onDragOver={(event) => { if (draggedCollectionIndex !== null) { event.preventDefault(); event.dataTransfer.dropEffect = "move"; } }} onDrop={(event) => { event.preventDefault(); void saveCollectionOrder(); }} onDragEnd={() => { if (draggedCollectionIndex !== null) void saveCollectionOrder(); }}><div className="collection-admin-card__shade" /><span>{collection.pinToTop ? <><Sparkles size={14} /> Pinned</> : `Position ${collection.position + 1}`}</span><div><h3>{collection.title}</h3><p>{collection.folders.length} folder{collection.folders.length === 1 ? "" : "s"} · {collection.folders.reduce((total, folder) => total + folder.sources.length, 0)} sources</p><div><><IconButton label={`Move ${collection.title} up`} disabled={reordering || collectionIndex === 0 || collections[collectionIndex - 1]?.pinToTop !== collection.pinToTop} onClick={() => void moveCollection(collectionIndex, collectionIndex - 1)}><ChevronUp size={17} /></IconButton><IconButton label={`Move ${collection.title} down`} disabled={reordering || collectionIndex === collections.length - 1 || collections[collectionIndex + 1]?.pinToTop !== collection.pinToTop} onClick={() => void moveCollection(collectionIndex, collectionIndex + 1)}><ChevronDown size={17} /></IconButton><Button variant="secondary" onClick={() => openEditor(collection)}><Pencil size={17} /> Edit</Button></><IconButton label={`Delete ${collection.title}`} onClick={() => setDeleting(collection)}><Trash2 size={17} /></IconButton></div></div></article>)}</div> : <EmptyState icon={<Layers3 size={44} />} title="No collections yet" description="Create the first curated space for this profile." action={<Button onClick={() => openEditor("new")}><Plus size={18} /> Create collection</Button>} />}
    {editing && <Modal onClose={() => setEditing(null)} className="editor-modal collection-editor"><form onSubmit={submit}><div className="editor-modal__heading"><span><Layers3 size={18} /> {editing === "new" ? "New collection" : "Collection editor"}</span><h2>Design a world worth entering.</h2><p>Mix addons, TMDB discovery, people, networks, and Trakt lists in any order.</p></div>{error && <Notice>{error}</Notice>}<section className="editor-group"><div className="form-grid form-grid--three"><label className="field"><span>Collection title</span><div><Layers3 size={18} /><input value={draft.title} onChange={(event) => setDraft((current) => ({ ...current, title: event.target.value }))} required /></div></label><label className="field"><span>Backdrop URL</span><div><ImagePlus size={18} /><input type="url" value={draft.backdropImageUrl ?? ""} onChange={(event) => setDraft((current) => ({ ...current, backdropImageUrl: event.target.value || undefined }))} placeholder="https://…" /></div></label><label className="field"><span>Folder cover shape</span><div><Boxes size={18} /><select value={draft.folderCoverShape} onChange={(event) => setDraft((current) => ({ ...current, folderCoverShape: event.target.value as CollectionSaveInput["folderCoverShape"] }))}><option value="poster">Poster</option><option value="landscape">Landscape</option><option value="square">Square</option></select></div></label></div><div className="choice-row choice-row--four"><label className="toggle-field"><input type="checkbox" checked={draft.heroEnabled} onChange={(event) => setDraft((current) => ({ ...current, heroEnabled: event.target.checked }))} /><span><i /><div><strong>Hero section</strong><small>Feature this collection on Home</small></div></span></label><label className="toggle-field"><input type="checkbox" checked={draft.pinToTop} onChange={(event) => setDraft((current) => ({ ...current, pinToTop: event.target.checked }))} /><span><i /><div><strong>Pin to top</strong><small>Always show first</small></div></span></label><label className="toggle-field"><input type="checkbox" checked={draft.focusGlowEnabled} onChange={(event) => setDraft((current) => ({ ...current, focusGlowEnabled: event.target.checked }))} /><span><i /><div><strong>Focus glow</strong><small>Ambient highlight</small></div></span></label><label className="toggle-field"><input type="checkbox" checked={draft.viewMode === "follow_layout"} onChange={(event) => setDraft((current) => ({ ...current, viewMode: event.target.checked ? "follow_layout" : "rows" }))} /><span><i /><div><strong>Display titles directly</strong><small>Otherwise browse by folder</small></div></span></label></div></section>
      <ProfileAssignmentPicker profiles={profiles} selected={draft.profileIds} onChange={(profileIds) => setDraft((current) => ({ ...current, profileIds }))} legend="Available to" />
      <div className="folder-editor-list">{draft.folders.map((folder, folderIndex) => <section className={`folder-editor ${draggedFolderIndex === folderIndex ? "is-dragging" : ""}`} key={folder.id ?? folderIndex} onDragEnter={(event) => { event.preventDefault(); if (draggedFolderIndex !== null && draggedFolderIndex !== folderIndex) { moveFolder(draggedFolderIndex, folderIndex); setDraggedFolderIndex(folderIndex); } }} onDragOver={(event) => { if (draggedFolderIndex !== null) { event.preventDefault(); event.dataTransfer.dropEffect = "move"; } }} onDrop={(event) => { event.preventDefault(); setDraggedFolderIndex(null); }}><header><button type="button" className="folder-editor__drag" draggable onDragStart={(event) => { setDraggedFolderIndex(folderIndex); event.dataTransfer.effectAllowed = "move"; event.dataTransfer.setData("text/plain", String(folderIndex)); }} onDragEnd={() => setDraggedFolderIndex(null)} aria-label={`Move folder ${folderIndex + 1}`}><GripVertical /><span>Folder {folderIndex + 1}</span></button><div><IconButton type="button" label="Move folder up" disabled={folderIndex === 0} onClick={() => moveFolder(folderIndex, folderIndex - 1)}><ChevronUp size={17} /></IconButton><IconButton type="button" label="Move folder down" disabled={folderIndex === draft.folders.length - 1} onClick={() => moveFolder(folderIndex, folderIndex + 1)}><ChevronDown size={17} /></IconButton>{draft.folders.length > 1 && <IconButton type="button" label="Remove folder" onClick={() => setDraft((current) => ({ ...current, folders: current.folders.filter((_, index) => index !== folderIndex) }))}><Trash2 size={17} /></IconButton>}</div></header><div className="form-grid form-grid--three"><label className="field"><span>Folder title</span><div><Film size={18} /><input value={folder.title} onChange={(event) => updateFolder(folderIndex, { title: event.target.value })} required /></div></label><><label className="field"><span>Tile shape</span><div><select value={folder.tileShape} onChange={(event) => updateFolder(folderIndex, { tileShape: event.target.value as CollectionFolder["tileShape"] })}><option value="poster">Poster</option><option value="landscape">Landscape</option><option value="square">Square</option></select></div></label><label className="field"><span>Multiple sources</span><div><select value={folder.sourceView ?? "merged"} onChange={(event) => updateFolder(folderIndex, { sourceView: event.target.value as CollectionFolder["sourceView"] })}><option value="merged">All together</option><option value="categories">Category tabs</option><option value="folders">Source folders</option></select></div></label></><><label className="field"><span>Cover emoji</span><div><Sparkles size={18} /><input value={folder.coverEmoji ?? ""} onChange={(event) => updateFolder(folderIndex, { coverEmoji: event.target.value })} placeholder="✨" /></div></label><label className="field"><span>Cover image URL</span><div><ImagePlus size={18} /><input type="url" value={folder.coverImageUrl ?? ""} onChange={(event) => updateFolder(folderIndex, { coverImageUrl: event.target.value || undefined })} placeholder="https://…" /></div></label></><label className="toggle-field folder-title-toggle"><input type="checkbox" checked={!folder.hideTitle} onChange={(event) => updateFolder(folderIndex, { hideTitle: !event.target.checked })} /><span><i /><div><strong>Show folder title</strong><small>Display name below cover</small></div></span></label></div><div className="source-list">{folder.sources.map((source, sourceIndex) => <div className={`source-editor-shell ${draggedSource?.folderIndex === folderIndex && draggedSource.sourceIndex === sourceIndex ? "is-dragging" : ""}`} key={source.id ?? sourceIndex} onDragEnter={(event) => { event.preventDefault(); event.stopPropagation(); if (draggedSource?.folderIndex === folderIndex && draggedSource.sourceIndex !== sourceIndex) { moveSource(folderIndex, draggedSource.sourceIndex, sourceIndex); setDraggedSource({ folderIndex, sourceIndex }); } }} onDragOver={(event) => { if (draggedSource?.folderIndex === folderIndex) { event.preventDefault(); event.stopPropagation(); event.dataTransfer.dropEffect = "move"; } }} onDrop={(event) => { event.preventDefault(); event.stopPropagation(); setDraggedSource(null); }}><header className="source-editor-order"><button type="button" className="source-editor__drag" draggable onDragStart={(event) => { event.stopPropagation(); setDraggedSource({ folderIndex, sourceIndex }); event.dataTransfer.effectAllowed = "move"; event.dataTransfer.setData("text/plain", `${folderIndex}:${sourceIndex}`); }} onDragEnd={() => setDraggedSource(null)} aria-label={`Move source ${sourceIndex + 1}`}><GripVertical size={16} /><span>Source {sourceIndex + 1}</span></button><div><IconButton type="button" label="Move source up" disabled={sourceIndex === 0} onClick={() => moveSource(folderIndex, sourceIndex, sourceIndex - 1)}><ChevronUp size={16} /></IconButton><IconButton type="button" label="Move source down" disabled={sourceIndex === folder.sources.length - 1} onClick={() => moveSource(folderIndex, sourceIndex, sourceIndex + 1)}><ChevronDown size={16} /></IconButton></div></header><SourceEditor source={source} catalogs={catalogs} onChange={(value) => updateSource(folderIndex, sourceIndex, value)} onRemove={() => updateFolder(folderIndex, { sources: folder.sources.filter((_, index) => index !== sourceIndex) })} /></div>)}<div className="source-add"><span>Add a source</span><Button type="button" variant="secondary" onClick={() => addSource(folderIndex, "addon_catalog")} disabled={catalogs.length === 0}><Boxes size={16} /> Addon</Button><Button type="button" variant="secondary" onClick={() => addSource(folderIndex, "tmdb")}><Film size={16} /> TMDB</Button><Button type="button" variant="secondary" onClick={() => addSource(folderIndex, "trakt")}><Database size={16} /> Trakt</Button></div></div></section>)}</div><AddTile label="Add another folder" onClick={() => setDraft((current) => ({ ...current, folders: [...current.folders, blankFolder()] }))} /><div className="modal-actions modal-actions--sticky"><Button type="button" variant="ghost" onClick={() => setEditing(null)}>Cancel</Button><Button type="submit" loading={saving}><Save size={18} /> Save collection</Button></div></form></Modal>}
    {deleting && <ConfirmDialog title={`Delete ${deleting.title}?`} description="This collection and its folder configuration will be permanently removed. This cannot be undone." confirmLabel="Delete collection" onCancel={() => setDeleting(null)} onConfirm={() => void remove(deleting)} />}
  </div>;
}

function SourceEditor({ source, catalogs, onChange, onRemove }: { source: CollectionSource; catalogs: Array<{ addonId: string; manifestId: string; catalog: { type: string; id: string; name?: string } }>; onChange: (source: CollectionSource) => void; onRemove: () => void }) {
  const tmdb = source.tmdb;
  const fixedMediaType = tmdb ? fixedTMDBMediaType(tmdb.sourceType) : undefined;
  const updateTMDB = (change: Partial<NonNullable<CollectionSource["tmdb"]>>) => {
    if (tmdb) onChange({ ...source, tmdb: { ...tmdb, ...change } });
  };
  const updateTMDBFilters = (change: Partial<NonNullable<CollectionSource["tmdb"]>["filters"]>) => {
    if (tmdb) updateTMDB({ filters: { ...tmdb.filters, ...change } });
  };

  return <article className="source-editor">
    <header>
      <span className={`source-kind source-kind--${source.kind}`}>{source.kind === "tmdb" ? "TMDB" : source.kind === "trakt" ? "Trakt" : "Addon"}</span>
      <input value={source.title} onChange={(event) => onChange({ ...source, title: event.target.value })} aria-label="Category title" placeholder="Category title" required />
      <IconButton type="button" label="Remove source" onClick={onRemove}><X size={16} /></IconButton>
    </header>
    {source.addonCatalog && <label className="field"><span>Catalog</span><div><select value={`${source.addonCatalog.addonId}|${source.addonCatalog.type}|${source.addonCatalog.catalogId}`} onChange={(event) => {
      const [addonId, type, catalogId] = event.target.value.split("|");
      const catalog = catalogs.find((value) => value.addonId === addonId && value.catalog.type === type && value.catalog.id === catalogId);
      onChange({ ...source, title: catalog?.catalog.name || catalogId, addonCatalog: { addonId, type, catalogId } });
    }}>{catalogs.map((catalog) => <option key={`${catalog.addonId}-${catalog.catalog.type}-${catalog.catalog.id}`} value={`${catalog.addonId}|${catalog.catalog.type}|${catalog.catalog.id}`}>{catalog.manifestId} · {catalog.catalog.name || catalog.catalog.id}</option>)}</select></div></label>}
    {tmdb && <>
      <div className="form-grid form-grid--three tmdb-source-fields">
        <label className="field"><span>Source type</span><div><select value={tmdb.sourceType} onChange={(event) => {
          const sourceType = event.target.value as NonNullable<CollectionSource["tmdb"]>["sourceType"];
          const mediaType = fixedTMDBMediaType(sourceType) ?? tmdb.mediaType;
          onChange({ ...source, title: tmdbLabel(sourceType), tmdb: { ...tmdb, sourceType, tmdbId: undefined, mediaType } });
        }}>
          <option value="list">Public list</option>
          <option value="company">Production company</option>
          <option value="network">Network</option>
          <option value="collection">Movie collection</option>
          <option value="person">Person credits</option>
          <option value="director">Director credits</option>
          <option value="discover">Custom discover</option>
        </select></div></label>
        {tmdb.sourceType !== "discover" && <TMDBReferenceField key={tmdb.sourceType} sourceType={tmdb.sourceType} tmdbId={tmdb.tmdbId} onChange={(tmdbId) => updateTMDB({ tmdbId })} />}
        <label className="field"><span>Type</span><div><select value={fixedMediaType ?? tmdb.mediaType} disabled={fixedMediaType !== undefined} onChange={(event) => updateTMDB({ mediaType: event.target.value as "movie" | "series" | "both" })}>
          {fixedMediaType ? <option value={fixedMediaType}>{fixedMediaType === "movie" ? "Movie" : "Series"}</option> : <><option value="movie">Movie</option><option value="series">Series</option><option value="both">Both</option></>}
        </select></div></label>
        <label className="field"><span>Sort by</span><div><select value={tmdb.sort} onChange={(event) => updateTMDB({ sort: event.target.value })}>
          <option value="popularity.desc">Popularity</option>
          <option value="vote_average.desc">Rating</option>
          <option value="vote_count.desc">Vote count</option>
          <option value="release_date.desc">Release date</option>
          <option value="first_air_date.desc">First air date</option>
          <option value="original">Original order</option>
        </select></div></label>
      </div>
      {tmdb.sourceType === "discover" && <details className="tmdb-custom-filters" open>
        <summary><ChevronDown size={15} /> Custom filters</summary>
        <div className="form-grid form-grid--three">
          <TMDBIDListField label="Genres" value={tmdb.filters.genres} placeholder="28, 12" onChange={(genres) => updateTMDBFilters({ genres })} />
          <label className="field"><span>Date from</span><div><input type="date" value={tmdb.filters.releaseDateFrom ?? ""} onChange={(event) => updateTMDBFilters({ releaseDateFrom: event.target.value || undefined })} /></div></label>
          <label className="field"><span>Date to</span><div><input type="date" value={tmdb.filters.releaseDateTo ?? ""} onChange={(event) => updateTMDBFilters({ releaseDateTo: event.target.value || undefined })} /></div></label>
          <label className="field"><span>Rating min</span><div><input type="number" min={0} max={10} step={0.1} value={tmdb.filters.voteAverageMin ?? ""} onChange={(event) => updateTMDBFilters({ voteAverageMin: event.target.value ? Number(event.target.value) : undefined })} placeholder="7.0" /></div></label>
          <label className="field"><span>Rating max</span><div><input type="number" min={0} max={10} step={0.1} value={tmdb.filters.voteAverageMax ?? ""} onChange={(event) => updateTMDBFilters({ voteAverageMax: event.target.value ? Number(event.target.value) : undefined })} placeholder="10" /></div></label>
          <label className="field"><span>Votes min</span><div><input type="number" min={0} step={1} value={tmdb.filters.voteCountMin ?? ""} onChange={(event) => updateTMDBFilters({ voteCountMin: event.target.value ? Number(event.target.value) : undefined })} placeholder="100" /></div></label>
          <label className="field"><span>Language</span><div><input value={tmdb.filters.originalLanguage ?? ""} maxLength={3} onChange={(event) => updateTMDBFilters({ originalLanguage: event.target.value || undefined })} placeholder="en" /></div></label>
          <label className="field"><span>Country</span><div><input value={tmdb.filters.originCountry ?? ""} maxLength={2} onChange={(event) => updateTMDBFilters({ originCountry: event.target.value || undefined })} placeholder="US" /></div></label>
          <TMDBIDListField label="Keywords" value={tmdb.filters.keywords} placeholder="9715" onChange={(keywords) => updateTMDBFilters({ keywords })} />
          <TMDBIDListField label="Companies" value={tmdb.filters.companies} placeholder="420" onChange={(companies) => updateTMDBFilters({ companies })} />
          <TMDBIDListField label="Networks" value={tmdb.filters.networks} placeholder="213" onChange={(networks) => updateTMDBFilters({ networks })} />
          <label className="field"><span>Year</span><div><input type="number" min={1870} max={2200} step={1} value={tmdb.filters.year ?? ""} onChange={(event) => updateTMDBFilters({ year: event.target.value ? Number(event.target.value) : undefined })} placeholder="2024" /></div></label>
        </div>
      </details>}
    </>}
    {source.trakt && <div className="form-grid form-grid--three">
      <label className="field"><span>Trakt list ID</span><div><input type="number" min={1} value={source.trakt.listId} onChange={(event) => onChange({ ...source, trakt: { ...source.trakt!, listId: Number(event.target.value) } })} /></div></label>
      <label className="field"><span>Media type</span><div><select value={source.trakt.mediaType} onChange={(event) => onChange({ ...source, trakt: { ...source.trakt!, mediaType: event.target.value as "movie" | "series" } })}><option value="movie">Movies</option><option value="series">Series</option></select></div></label>
      <label className="field"><span>Sort</span><div><select value={source.trakt.sortBy} onChange={(event) => onChange({ ...source, trakt: { ...source.trakt!, sortBy: event.target.value } })}><option value="rank">Rank</option><option value="added">Added</option><option value="title">Title</option><option value="released">Released</option><option value="popularity">Popularity</option><option value="votes">Votes</option></select></div></label>
    </div>}
  </article>;
}

function TMDBReferenceField({ sourceType, tmdbId, onChange }: { sourceType: NonNullable<CollectionSource["tmdb"]>["sourceType"]; tmdbId?: number; onChange: (tmdbId?: number) => void }) {
  const [value, setValue] = useState(tmdbId ? String(tmdbId) : "");
  const [resolvedValue, setResolvedValue] = useState("");
  const [matches, setMatches] = useState<Array<{ id: number; name: string }>>([]);
  const [searching, setSearching] = useState(false);
  const lookupByName = sourceType === "company";

  useEffect(() => {
    const query = value.trim();
    if (!lookupByName || parseTMDBReference(query, sourceType) || query.length < 2 || query === resolvedValue) {
      setMatches([]);
      setSearching(false);
      return;
    }
    let active = true;
    const timer = window.setTimeout(() => {
      setSearching(true);
      void api.tmdbLookup("company", query).then(({ results }) => {
        if (!active) return;
        setMatches(results);
        onChange(results[0]?.id);
      }).catch(() => {
        if (active) setMatches([]);
      }).finally(() => {
        if (active) setSearching(false);
      });
    }, 300);
    return () => {
      active = false;
      window.clearTimeout(timer);
    };
  }, [lookupByName, resolvedValue, sourceType, value]);

  const help = tmdbReferenceHelp(sourceType);
  return <div className="tmdb-reference">
    <label className="field">
      <span>{tmdbReferenceLabel(sourceType)}</span>
      <div><input value={value} onChange={(event) => {
        const next = event.target.value;
        setValue(next);
        setResolvedValue("");
        onChange(parseTMDBReference(next, sourceType));
      }} placeholder={tmdbReferencePlaceholder(sourceType)} required /></div>
      <small>{help}</small>
    </label>
    {searching && <small className="tmdb-reference__status">Searching TMDB…</small>}
    {matches.length > 0 && <div className="tmdb-reference__matches">{matches.slice(0, 5).map((match) => <button type="button" key={match.id} onMouseDown={(event) => event.preventDefault()} onClick={() => {
      setValue(match.name);
      setResolvedValue(match.name);
      setMatches([]);
      onChange(match.id);
    }}>{match.name}<small>#{match.id}</small></button>)}</div>}
  </div>;
}

function TMDBIDListField({ label, value, placeholder, onChange }: { label: string; value?: number[]; placeholder: string; onChange: (value?: number[]) => void }) {
  const [text, setText] = useState(value?.join(", ") ?? "");
  return <label className="field"><span>{label}</span><div><input value={text} onChange={(event) => {
    const next = event.target.value;
    setText(next);
    const values = numericList(next);
    onChange(values.length > 0 ? values : undefined);
  }} placeholder={placeholder} /></div></label>;
}

function parseTMDBReference(value: string, sourceType: NonNullable<CollectionSource["tmdb"]>["sourceType"]): number | undefined {
  const trimmed = value.trim();
  if (/^\d+$/.test(trimmed)) {
    const id = Number(trimmed);
    return Number.isSafeInteger(id) && id > 0 ? id : undefined;
  }
  const path = sourceType === "director" ? "person" : sourceType;
  const match = trimmed.match(new RegExp(`(?:themoviedb\\.org/)?${path}/(\\d+)`, "i"));
  if (!match) return undefined;
  const id = Number(match[1]);
  return Number.isSafeInteger(id) && id > 0 ? id : undefined;
}

function tmdbReferenceLabel(sourceType: NonNullable<CollectionSource["tmdb"]>["sourceType"]): string {
  return { list: "Public list", company: "Production company", network: "Network ID", collection: "Movie collection ID", person: "Person credits", director: "Director credits", discover: "TMDB ID" }[sourceType];
}

function tmdbReferencePlaceholder(sourceType: NonNullable<CollectionSource["tmdb"]>["sourceType"]): string {
  return sourceType === "company" ? "Pixar, URL, or 3" : sourceType === "list" ? "List URL or 12345" : sourceType === "person" || sourceType === "director" ? "Person URL or 287" : "Numeric TMDB ID";
}

function tmdbReferenceHelp(sourceType: NonNullable<CollectionSource["tmdb"]>["sourceType"]): string {
  return {
    list: "TMDB public list URL or numeric list ID. Movies only.",
    company: "TMDB production company name, URL, or numeric ID.",
    network: "TMDB network ID. Series only.",
    collection: "TMDB movie collection ID. Movies only.",
    person: "TMDB person ID or URL used for cast credits.",
    director: "TMDB person ID or URL used for director credits.",
    discover: "",

  }[sourceType];
}

function fixedTMDBMediaType(sourceType: NonNullable<CollectionSource["tmdb"]>["sourceType"]): "movie" | "series" | undefined {
  if (sourceType === "network") return "series";
  if (sourceType === "list" || sourceType === "collection") return "movie";
  return undefined;
}

function tmdbLabel(sourceType: NonNullable<CollectionSource["tmdb"]>["sourceType"]): string {
  const labels: Record<NonNullable<CollectionSource["tmdb"]>["sourceType"], string> = { list: "Public list", company: "Production company", network: "Network", collection: "Movie collection", person: "Person credits", director: "Director credits", discover: "Custom discover" };
  return labels[sourceType];
}

function numericList(value: string): number[] {
  return value.split(",").map((part) => Number(part.trim())).filter((number) => Number.isInteger(number) && number > 0);
}

function ActivityAdmin() {
  const [activity, setActivity] = useState<PlaybackActivity | null>(null);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [purging, setPurging] = useState(false);
  const [stopping, setStopping] = useState(false);
  const [selectedSession, setSelectedSession] = useState<PlaybackActivitySession | null>(null);
  const [error, setError] = useState("");

  async function load(silent = false) {
    if (!silent) setRefreshing(true);
    try {
      setActivity(await api.playbackActivity());
      setError("");
    } catch (cause) {
      setError(notifyError(cause, "Playback activity could not be loaded.", "Activity unavailable"));
    } finally {
      setLoading(false);
      if (!silent) setRefreshing(false);
    }
  }

  useEffect(() => {
    let active = true;
    const refresh = async () => {
      try {
        const value = await api.playbackActivity();
        if (active) {
          setActivity(value);
          setError("");
          setLoading(false);
        }
      } catch (cause) {
        if (active) {
          setError(notifyError(cause, "Playback activity could not be loaded.", "Activity unavailable"));
          setLoading(false);
        }
      }
    };
    void refresh();
    const interval = window.setInterval(() => { void refresh(); }, 5_000);
    return () => {
      active = false;
      window.clearInterval(interval);
    };
  }, []);

  async function purge() {
    setPurging(true);
    try {
      const result = await api.purgePlaybackActivity();
      notifySuccess(`${result.sessionsRemoved} inactive sessions and ${result.jobsStopped} orphaned jobs removed.`, "Media cleaned");
      await load(true);
    } catch (cause) {
      setError(notifyError(cause, "Inactive media could not be purged.", "Cleanup failed"));
    } finally {
      setPurging(false);
    }
  }

  async function stopSession() {
    if (!selectedSession) return;
    setStopping(true);
    try {
      await api.stopPlaybackActivitySession(selectedSession.id);
      notifySuccess(`${selectedSession.title} was stopped.`, "Playback stopped");
      setSelectedSession(null);
      await load(true);
    } catch (cause) {
      setError(notifyError(cause, "The playback session could not be stopped.", "Stop failed"));
    } finally {
      setStopping(false);
    }
  }

  if (loading) return <Skeleton className="settings-skeleton" />;
  const summary = activity?.summary;
  return <div className="admin-section activity-admin">
    <div className="admin-section__header"><div><span>Live media</span><h2>Playback activity</h2><p>See active sessions, processing pressure, and temporary media usage.</p></div><div className="admin-section__actions"><Button variant="secondary" onClick={() => void load()} loading={refreshing}><RefreshCw size={16} />Refresh</Button><Button variant="secondary" onClick={() => void purge()} loading={purging}><HardDrive size={16} />Purge expired media</Button></div></div>
    {error && <Notice>{error}</Notice>}
    <div className="activity-overview">
      <ActivityMetric icon={<Radio />} label="Sessions" value={String(summary?.activeSessions ?? 0)} detail={`${summary?.activeJobs ?? 0} media jobs`} />
      <ActivityMetric icon={<Cpu />} label="Processing" value={`${summary?.processingSlots ?? 0} / ${summary?.processingLimit ?? 0}`} detail="FFmpeg slots" />
      <ActivityMetric icon={<HardDrive />} label="Temporary media" value={formatBytes(summary?.storageBytes ?? 0)} detail={`of ${formatBytes(summary?.storageLimitBytes ?? 0)}`} />
      <ActivityMetric icon={<Server />} label="Encoder" value={activity?.diagnostics.videoEncoder.toUpperCase() ?? "UNKNOWN"} detail={activity?.diagnostics.hardwareToneMap ? "Hardware tone mapping" : "Software tone mapping"} />
    </div>
    <section className="activity-panel">
      <header><div><span>Now playing</span><h3>Sessions</h3></div><small>{activity?.sessions.length ?? 0} active</small></header>
      {activity?.sessions.length
        ? <div className="activity-session-list">{activity.sessions.map((session) => <article className="activity-session" key={session.id}><span className={`activity-session__state ${session.processing ? "is-processing" : ""}`}><Activity size={18} /></span><div><strong>{session.title}</strong><span>{session.profile} · {session.username}</span><small>{session.device} · {session.platform} · {activityModeLabel(session.mode)}</small></div><div className="activity-session__time"><strong>{activityAge(session.lastSeenAt)}</strong><small>started {activityAge(session.createdAt)}</small></div><Button variant="danger" onClick={() => setSelectedSession(session)}><CircleStop size={16} />Stop</Button></article>)}</div>
        : <EmptyState icon={<Radio />} title="No active playback" description="Sessions appear here as soon as a device starts playing." />}
    </section>
    <section className="activity-panel">
      <header><div><span>Media workers</span><h3>Processing jobs</h3></div><small>{activity?.jobs.length ?? 0} jobs</small></header>
      {activity?.jobs.length
        ? <div className="activity-job-list">{activity.jobs.map((job, index) => <article className="activity-job" key={`${job.sessionId ?? "prewarm"}-${job.assetId}-${index}`}><span className={`activity-job__dot is-${job.state}`} /><div><strong>{job.prewarming ? "Preparing selected source" : activityModeLabel(job.mode)}</strong><small>{job.assetId} · last request {activityAge(job.lastSeenAt)}</small></div><span>{job.state}</span></article>)}</div>
        : <EmptyState icon={<Cpu />} title="No media processing" description="Direct play does not consume an FFmpeg slot." />}
    </section>
    {selectedSession && <ConfirmDialog title={`Stop ${selectedSession.title}?`} description={`Playback on ${selectedSession.device} will end immediately and its temporary media will be deleted.`} confirmLabel="Stop playback" loading={stopping} onConfirm={() => void stopSession()} onCancel={() => setSelectedSession(null)} />}
  </div>;
}

function ActivityMetric({ icon, label, value, detail }: { icon: React.ReactNode; label: string; value: string; detail: string }) {
  return <article className="activity-metric"><span>{icon}</span><div><small>{label}</small><strong>{value}</strong><span>{detail}</span></div></article>;
}

function activityModeLabel(mode: string): string {
  return { direct: "Direct play", remux: "Remux", transcode_audio: "Audio conversion", transcode: "Video transcode" }[mode] ?? mode;
}

function formatBytes(value: number): string {
  if (value < 1024) return `${value} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let scaled = value;
  let index = -1;
  do {
    scaled /= 1024;
    index++;
  } while (scaled >= 1024 && index < units.length - 1);
  return `${scaled >= 10 ? scaled.toFixed(0) : scaled.toFixed(1)} ${units[index]}`;
}

function activityAge(value: string): string {
  const seconds = Math.max(0, Math.floor((Date.now() - new Date(value).getTime()) / 1000));
  if (seconds < 10) return "just now";
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  return `${hours}h ago`;
}

function SettingsAdmin() {
  const { account, activeProfile } = useAuth();
  const [settingsTarget, setSettingsTarget] = useState(activeProfile?.id ?? "");
  const [instance, setInstance] = useState<SettingsValues>({});
  const [profile, setProfile] = useState<SettingsValues>({});
  const [inherited, setInherited] = useState<SettingsValues>({});
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [loaded, setLoaded] = useState(false);
  const settingsTargetRef = useRef(settingsTarget);
  settingsTargetRef.current = settingsTarget;
  const canManageProfiles = Boolean(activeProfile?.canManage);
  const canManageServer = canManageProfiles && account?.user.role === "admin";
  const serverSelected = settingsTarget === "server";
  const targetProfile = account?.profiles.find((candidate) => candidate.id === settingsTarget) ?? activeProfile;

  useEffect(() => {
    setSettingsTarget(activeProfile?.id ?? "");
  }, [activeProfile?.id]);


  useEffect(() => {
    if (!settingsTarget) return;
    const target = settingsTarget;
    let current = true;
    setLoaded(false);
    setError("");
    void (async () => {
      if (target === "server") {
        const layer = await api.instanceSettings();
        if (!current) return;
        setInstance(layer.settings);
        setInherited({});
        return;
      }
      const [layer, serverDefaults] = await Promise.all([api.profileSettings(target), api.instanceSettings()]);
      if (!current) return;
      setProfile(layer.settings);
      setInherited(serverDefaults.settings);
    })()
      .catch((cause) => {
        if (current) setError(notifyError(cause, "Settings could not be loaded.", "Settings unavailable"));
      })
      .finally(() => {
        if (current) setLoaded(true);
      });
    return () => { current = false; };
  }, [settingsTarget]);

  async function save() {
    if (!activeProfile || !settingsTarget) return;
    const target = settingsTarget;
    const savingServer = target === "server";
    const profileName = account?.profiles.find((candidate) => candidate.id === target)?.name ?? "Profile";
    setSaving(true);
    setError("");
    try {
      if (savingServer) {
        const updated = await api.updateInstanceSettings(instance);
        if (settingsTargetRef.current === target) setInstance(updated.settings);
      } else {
        const updated = await api.updateProfileSettings(target, profile);
        if (settingsTargetRef.current === target) setProfile(updated.settings);
      }
      if (savingServer || target === activeProfile.id) window.dispatchEvent(new Event("rivune:settings-changed"));
      notifySuccess(savingServer ? "Server defaults have been updated." : `${profileName} preferences have been updated.`, "Settings saved");
    } catch (cause) {
      setError(notifyError(cause, "Settings could not be saved.", "Settings not saved"));
    } finally {
      setSaving(false);
    }
  }

  if (!loaded) return <Skeleton className="settings-skeleton" />;
  return <div className="admin-section"><div className="admin-section__header"><div><span>Preferences</span><h2>Settings</h2><p>Choose one scope to configure at a time.</p></div>{canManageProfiles && <label className="field settings-profile-picker"><span>Settings scope</span><div>{serverSelected ? <Server size={18} /> : <CircleUserRound size={18} />}<select value={settingsTarget} disabled={saving} onChange={(event) => setSettingsTarget(event.target.value)}>{canManageServer && <option value="server">Server defaults</option>}{account?.profiles.map((candidate) => <option key={candidate.id} value={candidate.id}>{candidate.name}</option>)}</select></div></label>}</div>{error && <Notice>{error}</Notice>}
    {serverSelected
      ? <SettingsCard title="Server defaults" description="The baseline inherited by every profile." icon={<Server />} values={instance} onChange={setInstance} onSave={() => void save()} saving={saving} emptyLabel="Rivune default" />
      : <SettingsCard title={`${targetProfile?.name ?? "Profile"} preferences`} description="Overrides that follow this profile everywhere." icon={<CircleUserRound />} values={profile} defaults={inherited} onChange={setProfile} onSave={() => void save()} saving={saving} />}
  </div>;
}

function SettingsCard({ title, description, icon, values, defaults = {}, onChange, onSave, saving, emptyLabel = "Inherit" }: { title: string; description: string; icon: React.ReactNode; values: SettingsValues; defaults?: SettingsValues; onChange: (values: SettingsValues) => void; onSave: () => void; saving: boolean; emptyLabel?: string }) {
  function change<K extends keyof SettingsValues>(key: K, value: SettingsValues[K]) {
    onChange({ ...values, [key]: value });
  }

  return <section className="settings-card">
    <header><span>{icon}</span><div><h3>{title}</h3><p>{description}</p></div></header>
    <div className="settings-groups">
      <SettingsGroup icon={<Film />} title="Playback" description="Stream quality and episode flow.">
        <label className="field"><span>Maximum resolution</span><div><select value={values.maximumResolution ?? ""} onChange={(event) => change("maximumResolution", event.target.value || null)}><option value="">{emptyLabel}</option><option value="2160p">4K · 2160p</option><option value="1080p">Full HD · 1080p</option><option value="720p">HD · 720p</option><option value="480p">SD · 480p</option></select></div></label>
        <InheritedToggle label="Prefer direct play" description="Avoid transcoding when supported" value={values.preferDirectPlay} defaultValue={defaults.preferDirectPlay ?? true} onChange={(value) => change("preferDirectPlay", value)} emptyLabel={emptyLabel} />
        <InheritedToggle label="Autoplay next episode" description="Continue a series when an episode finishes" value={values.autoplayNextEpisode} defaultValue={defaults.autoplayNextEpisode ?? true} onChange={(value) => change("autoplayNextEpisode", value)} emptyLabel={emptyLabel} />
      </SettingsGroup>

      <SettingsGroup icon={<Palette />} title="Interface" description="Appearance, motion, and content density.">
        <label className="field"><span>Theme</span><div><select value={values.theme ?? ""} onChange={(event) => change("theme", event.target.value || null)}><option value="">{emptyLabel}</option><option value="dark">Dark</option><option value="light">Light</option><option value="system">System</option></select></div></label>
        <label className="field"><span>Card density</span><div><select value={values.cardDensity ?? ""} onChange={(event) => change("cardDensity", event.target.value ? event.target.value as "comfortable" | "compact" : null)}><option value="">{emptyLabel}</option><option value="comfortable">Comfortable</option><option value="compact">Compact</option></select></div></label>
        <InheritedToggle label="Interface animations" description="Use transitions and automatic hero rotation" value={values.animationsEnabled} defaultValue={defaults.animationsEnabled ?? true} onChange={(value) => change("animationsEnabled", value)} emptyLabel={emptyLabel} />
        <InheritedToggle label="Hide unreleased titles" description="Home only · search still includes upcoming titles" value={values.hideUnreleased} defaultValue={defaults.hideUnreleased ?? false} onChange={(value) => change("hideUnreleased", value)} emptyLabel={emptyLabel} />
      </SettingsGroup>

      <SettingsGroup icon={<Languages />} title="Languages & metadata" description="Keep titles and playback language-aware.">
        <label className="field"><span>Metadata language</span><div><select value={values.metadataLanguage ?? ""} onChange={(event) => change("metadataLanguage", event.target.value || null)}><option value="">{emptyLabel}</option><option value="auto">Automatic · preferred audio or device language</option><option value="fr-FR">Français</option><option value="en-US">English</option><option value="es-ES">Español</option><option value="de-DE">Deutsch</option><option value="it-IT">Italiano</option><option value="pt-BR">Português</option><option value="ja-JP">日本語</option></select></div></label>
        <label className="field"><span>Metadata region</span><div><select value={values.metadataRegion ?? ""} onChange={(event) => change("metadataRegion", event.target.value || null)}><option value="">{emptyLabel}</option><option value="auto">Automatic · device region</option><option value="FR">France</option><option value="BE">Belgium</option><option value="CA">Canada</option><option value="CH">Switzerland</option><option value="US">United States</option><option value="GB">United Kingdom</option><option value="DE">Germany</option><option value="ES">Spain</option><option value="IT">Italy</option><option value="JP">Japan</option></select></div></label>
        <label className="field"><span>Series episode mapping</span><div><select value={values.seriesMappingProvider ?? ""} onChange={(event) => change("seriesMappingProvider", event.target.value ? event.target.value as "tmdb" | "tvdb" : null)}><option value="">{emptyLabel}</option><option value="tmdb">TMDB · provider seasons</option><option value="tvdb">TVDB · official seasons</option></select></div></label>
        <label className="field"><span>Audio language</span><div><input value={values.audioLanguage ?? ""} onChange={(event) => change("audioLanguage", event.target.value || null)} placeholder="en" /></div></label>
      </SettingsGroup>

      <SettingsGroup icon={<Captions />} title="Subtitles" description="Preferred track and readable cue styling.">
        <label className="field"><span>Subtitle language</span><div><input value={values.subtitleLanguage ?? ""} onChange={(event) => change("subtitleLanguage", event.target.value || null)} placeholder="en" /></div></label>
        <RangeSetting label="Subtitle size" value={values.subtitleSizePercent} defaultValue={defaults.subtitleSizePercent ?? 100} min={50} max={200} step={1} suffix="%" emptyLabel={emptyLabel} onChange={(value) => change("subtitleSizePercent", value)} />
        <ColorSetting value={values.subtitleTextColor} defaultValue={defaults.subtitleTextColor ?? "#FFFFFF"} emptyLabel={emptyLabel} onChange={(value) => change("subtitleTextColor", value)} />
        <RangeSetting label="Background opacity" value={values.subtitleBackgroundOpacityPercent} defaultValue={defaults.subtitleBackgroundOpacityPercent ?? 60} min={0} max={100} step={1} suffix="%" emptyLabel={emptyLabel} onChange={(value) => change("subtitleBackgroundOpacityPercent", value)} />
      </SettingsGroup>

      <SettingsGroup icon={<Bell />} title="Notifications" description="Session messages and how long they stay visible.">
        <InheritedToggle label="Session notifications" description="Poll for messages sent to this device" value={values.notificationsEnabled} defaultValue={defaults.notificationsEnabled ?? true} onChange={(value) => change("notificationsEnabled", value)} emptyLabel={emptyLabel} />
        <RangeSetting label="Display duration" value={values.notificationDurationSeconds} defaultValue={defaults.notificationDurationSeconds ?? 5} min={2} max={30} step={1} suffix=" seconds" emptyLabel={emptyLabel} onChange={(value) => change("notificationDurationSeconds", value)} />
        <RangeSetting label="Polling interval" value={values.notificationPollIntervalSeconds} defaultValue={defaults.notificationPollIntervalSeconds ?? 5} min={5} max={300} step={1} suffix=" seconds" emptyLabel={emptyLabel} onChange={(value) => change("notificationPollIntervalSeconds", value)} />
      </SettingsGroup>
    </div>
    <footer><Button loading={saving} onClick={onSave}><Check size={18} /> Save settings</Button></footer>
  </section>;
}

function SettingsGroup({ icon, title, description, children }: { icon: React.ReactNode; title: string; description: string; children: React.ReactNode }) {
  return <section className="settings-group">
    <div className="settings-group__heading"><span>{icon}</span><div><h4>{title}</h4><p>{description}</p></div></div>
    <div className="settings-group__grid">{children}</div>
  </section>;
}

function InheritedToggle({ label, description, value, defaultValue, onChange, emptyLabel }: { label: string; description: string; value: boolean | null | undefined; defaultValue: boolean; onChange: (value: boolean | null) => void; emptyLabel: string }) {
  const inherited = value === null || value === undefined;
  return <div className="setting-control setting-control--toggle">
    <label className="toggle-field"><input type="checkbox" checked={value ?? defaultValue} onChange={(event) => onChange(event.target.checked)} /><span><i /><div><strong>{label}</strong><small>{description}{inherited ? ` · ${emptyLabel}` : ""}</small></div></span></label>
    {!inherited && <button type="button" className="setting-inherit" onClick={() => onChange(null)}>{emptyLabel}</button>}
  </div>;
}

function RangeSetting({ label, value, defaultValue, min, max, step, suffix, emptyLabel, onChange }: { label: string; value: number | null | undefined; defaultValue: number; min: number; max: number; step: number; suffix: string; emptyLabel: string; onChange: (value: number | null) => void }) {
  const inherited = value === null || value === undefined;
  const shown = value ?? defaultValue;
  return <div className="setting-control setting-control--range">
    <label className="field"><span>{label}</span><div><input type="range" min={min} max={max} step={step} value={shown} onChange={(event) => onChange(Number(event.target.value))} /><output>{shown}{suffix}</output></div><small>{inherited ? emptyLabel : "Custom"}</small></label>
    {!inherited && <button type="button" className="setting-inherit" onClick={() => onChange(null)}>{emptyLabel}</button>}
  </div>;
}

function ColorSetting({ value, defaultValue, emptyLabel, onChange }: { value: string | null | undefined; defaultValue: string; emptyLabel: string; onChange: (value: string | null) => void }) {
  const inherited = value === null || value === undefined;
  const shown = value ?? defaultValue;
  return <div className="setting-control setting-control--color">
    <label className="field"><span>Subtitle text color</span><div><input type="color" value={shown} onChange={(event) => onChange(event.target.value.toUpperCase())} /><output>{shown}</output></div><small>{inherited ? emptyLabel : "Custom"}</small></label>
    {!inherited && <button type="button" className="setting-inherit" onClick={() => onChange(null)}>{emptyLabel}</button>}
  </div>;
}
