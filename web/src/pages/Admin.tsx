import { Activity, Bell, Boxes, Captions, Check, ChevronDown, ChevronUp, CircleStop, CircleUserRound, Cpu, Database, ExternalLink, Eye, EyeOff, Film, GripVertical, HardDrive, ImagePlus, Languages, Layers3, LoaderCircle, MonitorSmartphone, Palette, Pencil, Plus, Radio, RefreshCw, Save, Send, Server, Settings2, Shield, Sparkles, Trash2, Upload, Users, WandSparkles, X } from "lucide-react";
import { useEffect, useRef, useState, type ChangeEvent, type FormEvent } from "react";
import { api, APIError } from "../api";
import { useAuth } from "../auth";
import { AddTile, Button, ConfirmDialog, EmptyState, IconButton, Modal, Notice, Skeleton } from "../components";
import { interfaceLanguages, translate, type TranslationKey } from "../i18n";
import { notifyError, notifyErrorMessage, notifySuccess } from "../notifications";
import { TITLE_ID_PROVIDERS, titleProviderURL } from "../titleProviders";
import type { AddonManifest, AvatarPreset, Collection, CollectionFolder, CollectionSaveInput, CollectionSource, InstalledAddon, InterfaceLanguage, MaintenanceSettings, PlaybackActivity, PlaybackActivitySession, Profile, ProfileSession, SettingsValues, TrackingDeviceAuthorization, TrackingProvider, TrackingStatus } from "../types";

type AdminTab = "profiles" | "addons" | "collections" | "activity" | "settings";

const tabs: Array<{ id: AdminTab; labelKey: TranslationKey; descriptionKey: TranslationKey; icon: typeof Users; adminOnly?: boolean }> = [
  { id: "profiles", labelKey: "admin.tabs.profiles.label", descriptionKey: "admin.tabs.profiles.description", icon: Users },
  { id: "addons", labelKey: "admin.tabs.addons.label", descriptionKey: "admin.tabs.addons.description", icon: Boxes },
  { id: "collections", labelKey: "admin.tabs.collections.label", descriptionKey: "admin.tabs.collections.description", icon: Layers3 },
  { id: "activity", labelKey: "admin.tabs.activity.label", descriptionKey: "admin.tabs.activity.description", icon: Activity, adminOnly: true },
  { id: "settings", labelKey: "admin.tabs.settings.label", descriptionKey: "admin.tabs.settings.description", icon: Settings2 },
];

function countCodePoints(value: string) {
  let count = 0;
  for (let offset = 0; offset < value.length; count += 1) {
    const codePoint = value.codePointAt(offset);
    offset += codePoint !== undefined && codePoint > 0xffff ? 2 : 1;
  }
  return count;
}

function newIdempotencyKey() {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  bytes[6] = ((bytes[6] ?? 0) & 0x0f) | 0x40;
  bytes[8] = ((bytes[8] ?? 0) & 0x3f) | 0x80;
  const hex = Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
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
    <header className="admin-page__header">
      <div className="admin-page__heading">
        <span>{translate(canManage ? "admin.header.eyebrowServer" : "admin.header.eyebrowProfile")}</span>
        <h1>{translate(canManage ? "admin.header.titleServer" : "admin.header.titleProfile")}</h1>
        <p>{translate(canManage ? "admin.header.descriptionServer" : "admin.header.descriptionProfile")}</p>
      </div>
      <div className="admin-page__context" aria-label={translate("admin.header.workspaceAccessLabel")}>
        <span><Server size={16} aria-hidden="true" /> {translate(canManage ? "admin.header.workspaceServer" : "admin.header.workspacePersonal")}</span>
        <span><Shield size={16} aria-hidden="true" /> {translate(isAdmin ? "admin.header.accessAdministrator" : canManage ? "admin.header.accessManager" : "admin.header.accessProfile")}</span>
      </div>
    </header>
    <div className={`admin-layout ${canManage ? "" : "admin-layout--preferences"}`}>
      {canManage && <nav className="admin-tabs" aria-label={translate("admin.tabs.navigationLabel")}>{visibleTabs.map((item) => { const Icon = item.icon; return <button type="button" aria-current={tab === item.id ? "page" : undefined} key={item.id} className={tab === item.id ? "is-active" : ""} onClick={() => setTab(item.id)}><span><Icon size={20} aria-hidden="true" /></span><div><strong>{translate(item.labelKey)}</strong><small>{translate(item.descriptionKey)}</small></div><ChevronDown size={17} aria-hidden="true" /></button>; })}</nav>}
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
  const [broadcastOpen, setBroadcastOpen] = useState(false);
  const [broadcastMessage, setBroadcastMessage] = useState("");
  const [broadcastError, setBroadcastError] = useState("");
  const [sendingBroadcast, setSendingBroadcast] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);
  const messageTargetRef = useRef<{ profileId: string; sessionId: string; modalId: number } | null>(null);
  const pendingMessageRequestRef = useRef<{ profileId: string; sessionId: string; modalId: number } | null>(null);
  const nextMessageModalIdRef = useRef(0);
  const broadcastKeyRef = useRef<string | null>(null);
  const messageCharacterCount = countCodePoints(message);
  const broadcastCharacterCount = countCodePoints(broadcastMessage);

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
      notifySuccess(
        translate(creating ? "admin.profiles.notifications.createdMessage" : "admin.profiles.notifications.savedMessage", { name: profile.name }),
        translate(creating ? "admin.profiles.notifications.createdTitle" : "admin.profiles.notifications.savedTitle"),
      );
    } catch (cause) {
      setError(notifyError(cause, translate("admin.profiles.errors.save")));
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
      notifySuccess(translate("admin.profiles.notifications.deletedMessage", { name: profile.name }), translate("admin.profiles.notifications.deletedTitle"));
    } catch (cause) {
      setError(notifyError(cause, translate("admin.profiles.errors.delete")));
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
      setError(notifyError(cause, translate("admin.profiles.sessions.errors.load")));
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
      notifySuccess(translate("admin.profiles.sessions.notifications.revokedMessage", { deviceName: session.deviceName }), translate("admin.profiles.sessions.notifications.revokedTitle"));
    } catch (cause) {
      setError(notifyError(cause, translate("admin.profiles.sessions.errors.revoke")));
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
      notifySuccess(translate("admin.profiles.sessions.notifications.messageSent", { deviceName: targetSession.deviceName }), translate("admin.profiles.sessions.notifications.messageSentTitle"));
      messageTargetRef.current = null;
      setMessagingSession(null);
      setMessage("");
    } catch (cause) {
      if (!isCurrentRequest()) return;
      setMessageError(notifyError(cause, translate("admin.profiles.sessions.errors.messageSend")));
    } finally {
      if (pendingMessageRequestRef.current === request) {
        pendingMessageRequestRef.current = null;
        setSendingMessage(false);
      }
    }
  }

  function openBroadcast() {
    broadcastKeyRef.current = newIdempotencyKey();
    setBroadcastMessage("");
    setBroadcastError("");
    setSendingBroadcast(false);
    setBroadcastOpen(true);
  }

  function closeBroadcast() {
    if (sendingBroadcast) return;
    broadcastKeyRef.current = null;
    setBroadcastOpen(false);
  }

  async function sendBroadcast(event: FormEvent) {
    event.preventDefault();
    const key = broadcastKeyRef.current;
    if (!key || sendingBroadcast || !broadcastMessage.trim() || broadcastCharacterCount > 500) return;
    setSendingBroadcast(true);
    setBroadcastError("");
    try {
      const result = await api.broadcastSessionNotification(key, broadcastMessage);
      notifySuccess(
        translate(result.recipientCount === 1 ? "admin.broadcast.sentOne" : "admin.broadcast.sentMany", { count: result.recipientCount }),
        translate("admin.broadcast.sentTitle"),
      );
      broadcastKeyRef.current = null;
      setBroadcastOpen(false);
      setBroadcastMessage("");
    } catch (cause) {
      setBroadcastError(notifyError(cause, translate("admin.broadcast.error")));
    } finally {
      setSendingBroadcast(false);
    }
  }
  const enabledProfiles = profiles.filter((profile) => profile.enabled).length;
  const protectedProfiles = profiles.filter((profile) => profile.hasPin).length;
  const kidsProfiles = profiles.filter((profile) => profile.isChild).length;

  return <div className="admin-section profiles-admin">
    <div className="admin-section__header">
      <div><span>{translate("admin.profiles.eyebrow")}</span><h2>{translate("admin.profiles.title")}</h2><p>{translate("admin.profiles.description")}</p></div>
      <div className="admin-section__actions">
        {account?.user.role === "admin" && <Button variant="secondary" onClick={openBroadcast}><Radio size={18} /> {translate("admin.broadcast.open")}</Button>}
        <Button onClick={() => openEditor("new")}><Plus size={18} /> {translate("admin.profiles.actions.new")}</Button>
      </div>
    </div>
    <section className="admin-summary" aria-label={translate("admin.profiles.overview.label")}>
      <article><span><Users size={18} aria-hidden="true" /></span><div><strong>{profiles.length}</strong><small>{translate("admin.profiles.overview.total")}</small></div></article>
      <article><span><Check size={18} aria-hidden="true" /></span><div><strong>{enabledProfiles}</strong><small>{translate("admin.profiles.overview.enabled")}</small></div></article>
      <article><span><Shield size={18} aria-hidden="true" /></span><div><strong>{protectedProfiles}</strong><small>{translate("admin.profiles.status.pinProtected")}</small></div></article>
      <article><span><Sparkles size={18} aria-hidden="true" /></span><div><strong>{kidsProfiles}</strong><small>{translate("admin.profiles.overview.kids")}</small></div></article>
    </section>
    {error && <Notice>{error}</Notice>}
    {profiles.length ? <div className="profile-admin-grid">{profiles.map((profile) =>
      <article key={profile.id} className="profile-admin-card">
        <div className="profile-admin-card__visual"><img src={profile.avatar.url} alt="" /><span className={profile.isChild ? "is-child" : ""}>{translate(profile.isChild ? "admin.profiles.roles.kids" : profile.canManage ? "admin.profiles.roles.manager" : "admin.profiles.roles.viewer")}</span></div>
        <div className="profile-admin-card__copy"><h3>{profile.name}</h3><p><i className={`admin-status-dot ${profile.enabled ? "" : "is-disabled"}`} /> {translate(profile.enabled ? "common.status.enabled" : "common.status.disabled")} · {translate(profile.hasPin ? "admin.profiles.status.pinProtected" : "admin.profiles.status.noPin")}</p></div>
        <div className="profile-admin-card__actions">
          <Button variant="secondary" onClick={() => void openSessions(profile)}><MonitorSmartphone size={16} /> {translate("admin.profiles.actions.devices")}</Button>
          <Button variant="ghost" onClick={() => openEditor(profile)}><Pencil size={16} /> {translate("common.actions.edit")}</Button>
          {profile.id !== activeProfile?.id && <Button variant="ghost" className="admin-destructive-action" onClick={() => setDeleting(profile)}><Trash2 size={16} /> {translate("common.actions.delete")}</Button>}
        </div>
      </article>,
    )}</div> : <EmptyState icon={<Users size={44} />} title={translate("admin.profiles.empty.title")} description={translate("admin.profiles.empty.description")} action={<Button onClick={() => openEditor("new")}><Plus size={18} /> {translate("admin.profiles.actions.create")}</Button>} />}
    {editing && <Modal onClose={() => setEditing(null)} className="editor-modal profile-editor">
      <div className="editor-modal__heading">
        <span><CircleUserRound size={18} /> {translate(editing === "new" ? "admin.profiles.editor.newEyebrow" : "admin.profiles.editor.editEyebrow")}</span>
        <h2>{editing === "new" ? translate("admin.profiles.editor.createTitle") : translate("admin.profiles.editor.editTitle", { name: editing.name })}</h2>
      </div>
      <form onSubmit={submit} className="form-stack">
        {error && <Notice>{error}</Notice>}
        <div className="avatar-editor">
          <button type="button" onClick={() => fileRef.current?.click()}>
            {image ? <img src={URL.createObjectURL(image)} alt={translate("admin.profiles.editor.selectedAvatarAlt")} /> : <img src={presets.find((preset) => preset.id === presetId)?.url ?? "/api/v1/profile-avatars/aurora"} alt={translate("admin.profiles.editor.selectedAvatarAlt")} />}
            <span><Upload size={17} /> {translate("common.actions.upload")}</span>
          </button>
          <input ref={fileRef} hidden type="file" accept="image/png,image/jpeg,image/webp" onChange={(event) => setImage(event.target.files?.[0] ?? null)} />
          <div>{presets.map((preset) => <button type="button" key={preset.id} className={presetId === preset.id && !image ? "is-active" : ""} aria-label={preset.name} onClick={() => { setPresetId(preset.id); setImage(null); }}><img src={preset.url} alt="" /></button>)}</div>
        </div>
        <div className="form-grid form-grid--two">
          <label className="field"><span>{translate("admin.profiles.editor.name")}</span><div><CircleUserRound size={18} /><input value={name} onChange={(event) => setName(event.target.value)} required /></div></label>
          <label className="field"><span>{translate("admin.profiles.editor.pin")}</span><div><Shield size={18} /><input type={showPin ? "text" : "password"} inputMode="numeric" value={pin} onChange={(event) => setPin(event.target.value)} placeholder={translate(editing === "new" ? "admin.profiles.editor.pinCreatePlaceholder" : "admin.profiles.editor.pinEditPlaceholder")} /><IconButton type="button" label={translate(showPin ? "admin.profiles.editor.hidePin" : "admin.profiles.editor.showPin")} onClick={() => setShowPin((value) => !value)}>{showPin ? <EyeOff size={17} /> : <Eye size={17} />}</IconButton></div></label>
          <label className="toggle-field"><input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} /><span><i /><div><strong>{translate("common.status.enabled")}</strong><small>{translate("admin.profiles.editor.enabledDescription")}</small></div></span></label>
          <label className="toggle-field"><input type="checkbox" checked={isChild} onChange={(event) => setIsChild(event.target.checked)} /><span><i /><div><strong>{translate("admin.profiles.editor.kids")}</strong><small>{translate("admin.profiles.editor.kidsDescription")}</small></div></span></label>
        </div>
        <section className="profile-access-editor">
          <div><strong>{translate("admin.profiles.editor.availability")}</strong><p>{translate("admin.profiles.editor.availabilityDescription", { timezone: accessTimezone })}</p></div>
          <div className="form-grid form-grid--two">
            <label className="field"><span>{translate("admin.profiles.editor.availableFrom")}</span><div><input type="date" value={availableFrom} max={availableUntil || undefined} onChange={(event) => setAvailableFrom(event.target.value)} /></div></label>
            <label className="field"><span>{translate("admin.profiles.editor.availableUntil")}</span><div><input type="date" value={availableUntil} min={availableFrom || undefined} onChange={(event) => setAvailableUntil(event.target.value)} /></div></label>
          </div>
          <label className="toggle-field"><input type="checkbox" checked={dailyHours} onChange={(event) => setDailyHours(event.target.checked)} /><span><i /><div><strong>{translate("admin.profiles.editor.dailyHours")}</strong><small>{translate("admin.profiles.editor.dailyHoursDescription")}</small></div></span></label>
          {dailyHours && <><div className="form-grid form-grid--two">
            <label className="field"><span>{translate("admin.profiles.editor.accessStart")}</span><div><input type="time" value={accessStartTime} onChange={(event) => setAccessStartTime(event.target.value)} required /></div></label>
            <label className="field"><span>{translate("admin.profiles.editor.accessEnd")}</span><div><input type="time" value={accessEndTime} onChange={(event) => setAccessEndTime(event.target.value)} required /></div></label>
          </div><p className="profile-access-editor__hint">{translate("admin.profiles.editor.dailyHoursHint")}</p></>}
        </section>
        <div className="modal-actions"><Button type="button" variant="ghost" onClick={() => setEditing(null)}>{translate("common.cancel")}</Button><Button type="submit" loading={saving}><Save size={18} /> {translate("admin.profiles.actions.save")}</Button></div>
      </form>
    </Modal>}
    {broadcastOpen && <Modal onClose={closeBroadcast} className="editor-modal session-message-modal">
      <div className="editor-modal__heading">
        <span><Radio size={18} /> {translate("admin.broadcast.eyebrow")}</span>
        <h2>{translate("admin.broadcast.title")}</h2>
        <p>{translate("admin.broadcast.description")}</p>
      </div>
      <form className="form-stack" onSubmit={sendBroadcast}>
        {broadcastError && <Notice>{broadcastError}</Notice>}
        <label className="field">
          <span>{translate("admin.broadcast.message")}</span>
          <textarea autoFocus required disabled={sendingBroadcast} rows={5} value={broadcastMessage} onChange={(event) => setBroadcastMessage(event.target.value)} placeholder={translate("admin.broadcast.placeholder")} />
          <small>{translate("admin.broadcast.characterCount", { count: broadcastCharacterCount })}</small>
        </label>
        <div className="modal-actions modal-actions--sticky">
          <Button type="button" variant="ghost" disabled={sendingBroadcast} onClick={closeBroadcast}>{translate("common.cancel")}</Button>
          <Button type="submit" loading={sendingBroadcast} disabled={sendingBroadcast || !broadcastMessage.trim() || broadcastCharacterCount > 500}><Send size={17} /> {translate("admin.broadcast.send")}</Button>
        </div>
      </form>
    </Modal>}
    {sessionsProfile && <Modal onClose={closeSessions} className="editor-modal profile-sessions-modal">
      <div className="editor-modal__heading">
        <span><MonitorSmartphone size={18} /> {translate("admin.profiles.sessions.title")}</span>
        <h2>{sessionsProfile.name}</h2>
        <p>{translate("admin.profiles.sessions.description")}</p>
      </div>
      {error && <Notice>{error}</Notice>}
      {sessionsLoading ? <div className="profile-session-list"><Skeleton /><Skeleton /></div>
        : profileSessions.length === 0
          ? <EmptyState icon={<MonitorSmartphone size={40} />} title={translate("admin.profiles.sessions.emptyTitle")} description={translate("admin.profiles.sessions.emptyDescription")} />
          : <div className="profile-session-list">{profileSessions.map((session) =>
            <article key={session.id} className="profile-session">
              <span><MonitorSmartphone size={20} /></span>
              <div>
                <strong>{session.deviceName}</strong>
                <small>{session.platform} · {translate("admin.profiles.sessions.lastActive", { date: new Date(session.lastSeenAt).toLocaleString() })}</small>
                <SessionIPAddress session={session} />
              </div>
              <div className="profile-session__actions">
                {session.current && <i>{translate("admin.profiles.sessions.currentDevice")}</i>}
                <Button variant="secondary" disabled={sendingMessage} onClick={() => openSessionMessage(session)}><Bell size={15} /> {translate("admin.profiles.sessions.actions.message")}</Button>
                {!session.current && <Button variant="secondary" onClick={() => setRevokingSession(session)}>{translate("admin.profiles.sessions.actions.revoke")}</Button>}
              </div>
            </article>,
          )}</div>}
    </Modal>}
    {sessionsProfile && messagingSession && <Modal onClose={closeSessionMessage} className="editor-modal session-message-modal">
      <div className="editor-modal__heading">
        <span><Bell size={18} /> {translate("admin.profiles.sessions.message.title")}</span>
        <h2>{messagingSession.deviceName}</h2>
        <p>{translate("admin.profiles.sessions.message.description")}</p>
      </div>
      <form className="form-stack" onSubmit={sendSessionMessage}>
        {messageError && <Notice>{messageError}</Notice>}
        <label className="field">
          <span>{translate("admin.profiles.sessions.message.label")}</span>
          <textarea autoFocus required disabled={sendingMessage} rows={5} value={message} onChange={(event) => setMessage(event.target.value)} placeholder={translate("admin.profiles.sessions.message.placeholder")} />
          <small>{translate("admin.profiles.sessions.message.characterCount", { count: messageCharacterCount })}</small>
        </label>
        <div className="modal-actions modal-actions--sticky">
          <Button type="button" variant="ghost" disabled={sendingMessage} onClick={closeSessionMessage}>{translate("common.cancel")}</Button>
          <Button type="submit" loading={sendingMessage} disabled={sendingMessage || !message.trim() || messageCharacterCount > 500}><Send size={17} /> {translate("admin.profiles.sessions.message.send")}</Button>
        </div>
      </form>
    </Modal>}
    {sessionsProfile && revokingSession && <ConfirmDialog title={translate("admin.profiles.sessions.revoke.title", { deviceName: revokingSession.deviceName })} description={translate("admin.profiles.sessions.revoke.description")} confirmLabel={translate("admin.profiles.sessions.revoke.confirm")} onCancel={() => setRevokingSession(null)} onConfirm={() => void revokeSession(revokingSession)} />}
    {deleting && <ConfirmDialog title={translate("admin.profiles.delete.title", { name: deleting.name })} description={translate("admin.profiles.delete.description")} confirmLabel={translate("admin.profiles.delete.confirm")} onCancel={() => setDeleting(null)} onConfirm={() => void remove(deleting)} />}
  </div>;
}

function SessionIPAddress({ session }: { session: ProfileSession }) {
  const [revealed, setRevealed] = useState(false);
  if (!session.ipAddress) return <span className="profile-session__ip profile-session__ip--empty">{translate("admin.profiles.sessions.ipUnavailable")}</span>;
  return <span className="profile-session__ip">
    <code className={revealed ? "is-visible" : ""}>{session.ipAddress}</code>
    <IconButton
      type="button"
      label={translate(revealed ? "admin.profiles.sessions.hideIpAddress" : "admin.profiles.sessions.showIpAddress", { deviceName: session.deviceName })}
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
    setError("");
    try { setAddons((await api.addons()).addons); } catch (cause) { setError(notifyError(cause, translate("admin.addons.errors.load"), translate("admin.addons.errors.unavailableTitle"))); } finally { setLoading(false); }
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
      notifySuccess(translate("admin.addons.notifications.savedMessage", { name: updated.manifest.name }), translate("admin.addons.notifications.savedTitle"));
    } catch (cause) {
      setError(notifyError(cause, translate("admin.addons.errors.save")));
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
      notifySuccess(translate("admin.addons.notifications.installedMessage", { name: installed.manifest.name }), translate("admin.addons.notifications.installedTitle"));
    } catch (cause) { setError(notifyError(cause, translate("admin.addons.errors.install"))); } finally { setWorking(""); }
  }

  async function refresh(id: string) {
    setWorking(id);
    try {
      const updated = await api.refreshAddon(id);
      await load();
      notifySuccess(translate("admin.addons.notifications.refreshedMessage", { name: updated.manifest.name }), translate("admin.addons.notifications.refreshedTitle"));
    } catch (cause) {
      setError(notifyError(cause, translate("admin.addons.errors.refresh")));
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
      notifySuccess(translate("admin.addons.notifications.removedMessage", { name: addon.manifest.name }), translate("admin.addons.notifications.removedTitle"));
    } catch (cause) {
      setError(notifyError(cause, translate("admin.addons.errors.remove")));
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
      notifySuccess(translate("admin.addons.notifications.orderSavedMessage"), translate("admin.orderSavedTitle"));
    } catch (cause) {
      setError(notifyError(cause, translate("admin.addons.errors.orderSave")));
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
  const assignedProfiles = new Set(addons.flatMap((addon) => addon.profileIds)).size;
  const contentTypes = new Set(addons.flatMap((addon) => addon.manifest.types)).size;
  return <div className="admin-section addons-admin">
    <div className="admin-section__header">
      <div><span>{translate("admin.addons.eyebrow")}</span><h2>{translate("admin.addons.title")}</h2><p>{translate("admin.addons.description")}</p></div>
    </div>
    <section className="admin-summary" aria-label={translate("admin.addons.overview.label")}>
      <article><span><Boxes size={18} aria-hidden="true" /></span><div><strong>{loading ? "—" : addons.length}</strong><small>{translate("admin.addons.overview.installed")}</small></div></article>
      <article><span><CircleUserRound size={18} aria-hidden="true" /></span><div><strong>{loading ? "—" : assignedProfiles}</strong><small>{translate("admin.common.profilesReached")}</small></div></article>
      <article><span><Film size={18} aria-hidden="true" /></span><div><strong>{loading ? "—" : contentTypes}</strong><small>{translate("admin.addons.overview.contentTypes")}</small></div></article>
    </section>
    <section className="admin-tool-card" aria-labelledby="install-addon-title">
      <header><div><span>{translate("admin.addons.install.eyebrow")}</span><h3 id="install-addon-title">{translate("admin.addons.install.title")}</h3><p>{translate("admin.addons.install.description")}</p></div></header>
      <form className="install-addon" onSubmit={install}>
        <label className="field"><span>{translate("admin.addons.install.manifestUrl")}</span><div><WandSparkles size={19} /><input type="url" value={transportUrl} onChange={(event) => setTransportUrl(event.target.value)} placeholder={translate("admin.addons.manifestUrlPlaceholder")} required /></div></label>
        <Button type="submit" loading={working === "install"}><Plus size={18} /> {translate("admin.addons.actions.install")}</Button>
      </form>
      <ProfileAssignmentPicker profiles={profiles} selected={installProfileIds} onChange={setInstallProfileIds} legend={translate("admin.profileAssignment.legend")} />
    </section>
    {error && <Notice>{error}</Notice>}
    {loading
      ? <div className="addon-list" aria-label={translate("admin.addons.loadingLabel")}>{[0, 1].map((value) => <Skeleton key={value} className="addon-skeleton" />)}</div>
      : addons.length
        ? <div className="addon-list">{addons.map((addon, addonIndex) => <AddonCard key={addon.id} addon={addon} index={addonIndex} total={addons.length} working={working === addon.id} reordering={reordering} dragging={draggedAddonIndex === addonIndex} onDragStart={(event) => { setDraggedAddonIndex(addonIndex); event.dataTransfer.effectAllowed = "move"; event.dataTransfer.setData("text/plain", String(addonIndex)); }} onDragEnter={(event) => { event.preventDefault(); if (draggedAddonIndex !== null) stageAddonMove(draggedAddonIndex, addonIndex); }} onDragOver={(event) => { if (draggedAddonIndex !== null) { event.preventDefault(); event.dataTransfer.dropEffect = "move"; } }} onDrop={(event) => { event.preventDefault(); void saveAddonOrder(); }} onDragEnd={() => { if (draggedAddonIndex !== null) void saveAddonOrder(); }} onMove={(toIndex) => void moveAddon(addonIndex, toIndex)} onRefresh={() => void refresh(addon.id)} onEdit={() => openAddonEditor(addon)} onRemove={() => setDeleting(addon)} />)}</div>
        : <EmptyState icon={<Boxes size={44} />} title={translate(error ? "admin.addons.errors.unavailableTitle" : "admin.addons.empty.title")} description={translate(error ? "admin.addons.errors.retryDescription" : "admin.addons.empty.description")} action={error ? <Button variant="secondary" onClick={() => void load()}><RefreshCw size={17} /> {translate("common.actions.tryAgain")}</Button> : undefined} />}
    {editingAddon && <Modal onClose={() => { if (!addonEditSaving) setEditingAddon(null); }} className="editor-modal addon-edit-modal"><form onSubmit={saveAddon}><div className="editor-modal__heading"><span><Pencil size={18} /> {translate("admin.addons.editor.title")}</span><h2>{editingAddon.manifest.name}</h2><p>{translate("admin.addons.editor.description")}</p></div>{error && <Notice>{error}</Notice>}<label className="field"><span>{translate("admin.addons.editor.transportUrl")}</span><div><WandSparkles size={18} /><input type="url" value={editTransportUrl} onChange={(event) => setEditTransportUrl(event.target.value)} placeholder={translate("admin.addons.manifestUrlPlaceholder")} required /></div></label><ProfileAssignmentPicker profiles={profiles} selected={editProfileIds} onChange={setEditProfileIds} legend={translate("admin.profileAssignment.legend")} /><div className="modal-actions"><Button type="button" variant="ghost" disabled={addonEditSaving} onClick={() => setEditingAddon(null)}>{translate("common.cancel")}</Button><Button type="submit" loading={addonEditSaving} disabled={editProfileIds.length === 0}><Save size={18} /> {translate("admin.addons.actions.save")}</Button></div></form></Modal>}
    {deleting && <ConfirmDialog title={translate("admin.addons.remove.title", { name: deleting.manifest.name })} description={translate("admin.addons.remove.description")} confirmLabel={translate("admin.addons.remove.confirm")} loading={working === deleting.id} onCancel={() => setDeleting(null)} onConfirm={() => void remove(deleting)} />}
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
    <button type="button" className="addon-card__drag" draggable={!reordering && !working} disabled={reordering || working} onDragStart={onDragStart} onDragEnd={onDragEnd} aria-label={translate("admin.addons.reorder.dragLabel", { name: manifest.name })}><GripVertical /></button>
    <div className="addon-card__logo">{manifest.logo ? <img src={manifest.logo} alt="" /> : manifest.name.slice(0, 2).toUpperCase()}</div>
    <div className="addon-card__body"><div><h3>{manifest.name}</h3><span>v{manifest.version}</span>{manifest.behaviorHints?.p2p && <span className="addon-badge addon-badge--warn">P2P</span>}</div><p>{manifest.description || translate("admin.addons.card.noDescription")}</p><div>{manifest.types.map((type) => <i key={type}>{type}</i>)}</div></div>
    <div className="addon-card__controls">
      {working ? <span className="admin-working" role="status"><LoaderCircle className="spin" size={18} /> {translate("common.status.working")}</span> : <>
        <div className="addon-card__order" aria-label={translate("admin.addons.reorder.groupLabel", { name: manifest.name })}><IconButton label={translate("admin.addons.reorder.moveUp", { name: manifest.name })} disabled={reordering || index === 0} onClick={() => onMove(index - 1)}><ChevronUp size={17} /></IconButton><IconButton label={translate("admin.addons.reorder.moveDown", { name: manifest.name })} disabled={reordering || index === total - 1} onClick={() => onMove(index + 1)}><ChevronDown size={17} /></IconButton></div>
        <div className="addon-card__actions"><Button variant="ghost" disabled={reordering} onClick={onEdit}><Pencil size={16} /> {translate("common.actions.edit")}</Button><Button variant="ghost" disabled={reordering} onClick={onRefresh}><RefreshCw size={16} /> {translate("common.actions.refresh")}</Button><Button variant="ghost" className="admin-destructive-action" disabled={reordering} onClick={onRemove}><Trash2 size={16} /> {translate("common.actions.remove")}</Button></div>
      </>}
    </div>
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
        <span><strong>{profile.name}</strong><small>{translate(checked ? "admin.profileAssignment.included" : "admin.profileAssignment.notIncluded")}</small></span>
        <i><Check size={14} /></i>
      </label>;
    })}</div>
    <small>{translate("admin.profileAssignment.description")}</small>
  </fieldset>;
}

const blankFolder = (): CollectionFolder => ({ title: translate("admin.collections.defaults.folderTitle"), tileShape: "poster", sourceView: "merged", focusGifEnabled: false, hideTitle: false, sources: [] });
const blankCollection = (profileIds: string[] = []): CollectionSaveInput => ({ title: translate("admin.collections.defaults.collectionTitle"), heroEnabled: false, pinToTop: false, focusGlowEnabled: true, viewMode: "rows", folderCoverShape: "poster", folders: [blankFolder()], profileIds })

function collectionCardSummary(collection: Collection): string {
  const folderCount = collection.folders.length;
  const sourceCount = collection.folders.reduce((total, folder) => total + folder.sources.length, 0);
  const folders = translate(folderCount === 1 ? "admin.collections.card.folderCountOne" : "admin.collections.card.folderCountMany", { folderCount });
  const sources = translate(sourceCount === 1 ? "admin.collections.card.sourceCountOne" : "admin.collections.card.sourceCountMany", { sourceCount });
  return `${folders} · ${sources}`;
}

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
    setError("");
    try { setCollections((await api.collections()).collections); } catch (cause) { setError(notifyError(cause, translate("admin.collections.errors.load"), translate("admin.collections.errors.unavailableTitle"))); } finally { setLoading(false); }
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
      source = { kind, title: catalog?.catalog.name || catalog?.catalog.id || translate("admin.collections.sources.defaults.addonCatalog"), addonCatalog: { addonId: catalog?.addonId ?? "", type: catalog?.catalog.type ?? "movie", catalogId: catalog?.catalog.id ?? "" } };
    } else if (kind === "trakt") source = { kind, title: translate("admin.collections.sources.defaults.traktList"), trakt: { listId: 1, mediaType: "movie", sortBy: "rank", sortHow: "asc" } };
    else if (kind === "mdblist") source = { kind, title: "MDBList", mdblist: { listId: 1, mediaType: "movie", sort: "rank", order: "asc" } };
    else source = { kind, title: translate("admin.collections.sources.defaults.tmdbDiscover"), tmdb: { sourceType: "discover", mediaType: "movie", sort: "popularity.desc", filters: {} } };
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
      notifySuccess(translate("admin.collections.notifications.orderSavedMessage"), translate("admin.orderSavedTitle"));
    } catch (cause) {
      setError(notifyError(cause, translate("admin.collections.errors.orderSave")));
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
      const message = translate(document.collections.length === 1 ? "admin.collections.export.successOne" : "admin.collections.export.successMany", { count: document.collections.length });
      setSuccess(message);
      notifySuccess(message, translate("admin.collections.export.successTitle"));
    } catch (cause) {
      setError(notifyError(cause, translate("admin.collections.errors.export")));
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
      const message = translate(result.imported === 1 ? "admin.collections.import.successOne" : "admin.collections.import.successMany", { count: result.imported });
      setSuccess(message);
      notifySuccess(message, translate("admin.collections.import.successTitle"));
    } catch (cause) {
      setError(cause instanceof SyntaxError
        ? notifyErrorMessage(translate("admin.collections.errors.invalidJson"), translate("admin.collections.import.failedTitle"))
        : notifyError(cause, translate("admin.collections.errors.import"), translate("admin.collections.import.failedTitle")));
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
      notifySuccess(
        translate(creating ? "admin.collections.notifications.createdMessage" : "admin.collections.notifications.savedMessage", { title: draft.title }),
        translate(creating ? "admin.collections.notifications.createdTitle" : "admin.collections.notifications.savedTitle"),
      );
    } catch (cause) { setError(notifyError(cause, translate("admin.collections.errors.save"))); } finally { setSaving(false); }
  }

  async function remove(collection: Collection) {
    try {
      await api.deleteCollection(collection.id);
      setCollections((values) => values.filter((value) => value.id !== collection.id));
      setDeleting(null);
      notifySuccess(translate("admin.collections.notifications.deletedMessage", { title: collection.title }), translate("admin.collections.notifications.deletedTitle"));
    } catch (cause) {
      setError(notifyError(cause, translate("admin.collections.errors.delete")));
    }
  }
  const totalFolders = collections.reduce((total, collection) => total + collection.folders.length, 0);
  const totalSources = collections.reduce((total, collection) => total + collection.folders.reduce((count, folder) => count + folder.sources.length, 0), 0);
  const assignedProfiles = new Set(collections.flatMap((collection) => collection.profileIds)).size;

  return <div className="admin-section collections-admin">
    <div className="admin-section__header">
      <div><span>{translate("admin.collections.eyebrow")}</span><h2>{translate("admin.collections.title")}</h2><p>{translate("admin.collections.description")}</p></div>
      <div className="admin-section__actions">
        <input ref={importInput} type="file" accept="application/json,.json" hidden onChange={(event) => void importCollections(event)} />
        <Button type="button" variant="secondary" loading={transfer === "export"} disabled={Boolean(transfer)} onClick={() => void exportCollections()}><Save size={18} /> {translate("admin.collections.actions.exportJson")}</Button>
        <Button type="button" variant="secondary" loading={transfer === "import"} disabled={Boolean(transfer)} onClick={() => importInput.current?.click()}><Upload size={18} /> {translate("admin.collections.actions.importJson")}</Button>
        <Button type="button" disabled={Boolean(transfer)} onClick={() => openEditor("new")}><Plus size={18} /> {translate("admin.collections.actions.new")}</Button>
      </div>
    </div>
    <section className="admin-summary" aria-label={translate("admin.collections.overview.label")}>
      <article><span><Layers3 size={18} aria-hidden="true" /></span><div><strong>{loading ? "—" : collections.length}</strong><small>{translate("admin.collections.overview.collections")}</small></div></article>
      <article><span><Boxes size={18} aria-hidden="true" /></span><div><strong>{loading ? "—" : totalFolders}</strong><small>{translate("admin.collections.overview.folders")}</small></div></article>
      <article><span><Database size={18} aria-hidden="true" /></span><div><strong>{loading ? "—" : totalSources}</strong><small>{translate("admin.collections.overview.sources")}</small></div></article>
      <article><span><CircleUserRound size={18} aria-hidden="true" /></span><div><strong>{loading ? "—" : assignedProfiles}</strong><small>{translate("admin.common.profilesReached")}</small></div></article>
    </section>
    {success && <Notice tone="success">{success}</Notice>}
    {error && <Notice>{error}</Notice>}
    {loading
      ? <div className="collection-admin-grid" aria-label={translate("admin.collections.loadingLabel")}><Skeleton className="collection-skeleton" /><Skeleton className="collection-skeleton" /></div>
      : collections.length
        ? <div className="collection-admin-grid">{collections.map((collection, collectionIndex) =>
          <article key={collection.id} className={`collection-admin-card ${draggedCollectionIndex === collectionIndex ? "is-dragging" : ""}`} style={collection.backdropImageUrl ? { backgroundImage: `url(${collection.backdropImageUrl})` } : undefined} draggable={!reordering} onDragStart={(event) => { setDraggedCollectionIndex(collectionIndex); event.dataTransfer.effectAllowed = "move"; event.dataTransfer.setData("text/plain", String(collectionIndex)); }} onDragEnter={(event) => { event.preventDefault(); if (draggedCollectionIndex !== null) stageCollectionMove(draggedCollectionIndex, collectionIndex); }} onDragOver={(event) => { if (draggedCollectionIndex !== null) { event.preventDefault(); event.dataTransfer.dropEffect = "move"; } }} onDrop={(event) => { event.preventDefault(); void saveCollectionOrder(); }} onDragEnd={() => { if (draggedCollectionIndex !== null) void saveCollectionOrder(); }}>
            <div className="collection-admin-card__shade" />
            <span>{collection.pinToTop ? <><Sparkles size={14} /> {translate("admin.collections.card.pinned")}</> : translate("admin.collections.card.position", { position: collection.position + 1 })}</span>
            <div><h3>{collection.title}</h3><p>{collectionCardSummary(collection)}</p>
              <div className="collection-admin-card__actions">
                <span className="collection-admin-card__order"><IconButton label={translate("admin.collections.reorder.moveUp", { title: collection.title })} disabled={reordering || collectionIndex === 0 || collections[collectionIndex - 1]?.pinToTop !== collection.pinToTop} onClick={() => void moveCollection(collectionIndex, collectionIndex - 1)}><ChevronUp size={17} /></IconButton><IconButton label={translate("admin.collections.reorder.moveDown", { title: collection.title })} disabled={reordering || collectionIndex === collections.length - 1 || collections[collectionIndex + 1]?.pinToTop !== collection.pinToTop} onClick={() => void moveCollection(collectionIndex, collectionIndex + 1)}><ChevronDown size={17} /></IconButton></span>
                <Button variant="secondary" onClick={() => openEditor(collection)}><Pencil size={17} /> {translate("common.actions.edit")}</Button>
                <Button variant="ghost" className="admin-destructive-action" onClick={() => setDeleting(collection)}><Trash2 size={17} /> {translate("common.actions.delete")}</Button>
              </div>
            </div>
          </article>,
        )}</div>
        : <EmptyState icon={<Layers3 size={44} />} title={translate(error ? "admin.collections.errors.unavailableTitle" : "admin.collections.empty.title")} description={translate(error ? "admin.collections.errors.retryDescription" : "admin.collections.empty.description")} action={error ? <Button variant="secondary" onClick={() => void load()}><RefreshCw size={17} /> {translate("common.actions.tryAgain")}</Button> : <Button onClick={() => openEditor("new")}><Plus size={18} /> {translate("admin.collections.actions.create")}</Button>} />}
    {editing && <Modal onClose={() => setEditing(null)} className="editor-modal collection-editor"><form onSubmit={submit}>
      <div className="editor-modal__heading">
        <span><Layers3 size={18} /> {translate(editing === "new" ? "admin.collections.editor.newEyebrow" : "admin.collections.editor.editEyebrow")}</span>
        <h2>{translate("admin.collections.editor.title")}</h2>
        <p>{translate("admin.collections.editor.description")}</p>
      </div>
      {error && <Notice>{error}</Notice>}
      <section className="editor-group">
        <div className="form-grid form-grid--three">
          <label className="field"><span>{translate("admin.collections.editor.collectionTitle")}</span><div><Layers3 size={18} /><input value={draft.title} onChange={(event) => setDraft((current) => ({ ...current, title: event.target.value }))} required /></div></label>
          <label className="field"><span>{translate("admin.collections.editor.backdropUrl")}</span><div><ImagePlus size={18} /><input type="url" value={draft.backdropImageUrl ?? ""} onChange={(event) => setDraft((current) => ({ ...current, backdropImageUrl: event.target.value || undefined }))} placeholder={translate("admin.collections.editor.imageUrlPlaceholder")} /></div></label>
          <label className="field"><span>{translate("admin.collections.editor.folderCoverShape")}</span><div><Boxes size={18} /><select value={draft.folderCoverShape} onChange={(event) => setDraft((current) => ({ ...current, folderCoverShape: event.target.value as CollectionSaveInput["folderCoverShape"] }))}><option value="poster">{translate("admin.collections.shapes.poster")}</option><option value="landscape">{translate("admin.collections.shapes.landscape")}</option><option value="square">{translate("admin.collections.shapes.square")}</option></select></div></label>
        </div>
        <div className="choice-row choice-row--four">
          <label className="toggle-field"><input type="checkbox" checked={draft.heroEnabled} onChange={(event) => setDraft((current) => ({ ...current, heroEnabled: event.target.checked }))} /><span><i /><div><strong>{translate("admin.collections.editor.hero")}</strong><small>{translate("admin.collections.editor.heroDescription")}</small></div></span></label>
          <label className="toggle-field"><input type="checkbox" checked={draft.pinToTop} onChange={(event) => setDraft((current) => ({ ...current, pinToTop: event.target.checked }))} /><span><i /><div><strong>{translate("admin.collections.editor.pinToTop")}</strong><small>{translate("admin.collections.editor.pinToTopDescription")}</small></div></span></label>
          <label className="toggle-field"><input type="checkbox" checked={draft.focusGlowEnabled} onChange={(event) => setDraft((current) => ({ ...current, focusGlowEnabled: event.target.checked }))} /><span><i /><div><strong>{translate("admin.collections.editor.focusGlow")}</strong><small>{translate("admin.collections.editor.focusGlowDescription")}</small></div></span></label>
          <label className="toggle-field"><input type="checkbox" checked={draft.viewMode === "follow_layout"} onChange={(event) => setDraft((current) => ({ ...current, viewMode: event.target.checked ? "follow_layout" : "rows" }))} /><span><i /><div><strong>{translate("admin.collections.editor.displayTitlesDirectly")}</strong><small>{translate("admin.collections.editor.displayTitlesDirectlyDescription")}</small></div></span></label>
        </div>
      </section>
      <ProfileAssignmentPicker profiles={profiles} selected={draft.profileIds} onChange={(profileIds) => setDraft((current) => ({ ...current, profileIds }))} legend={translate("admin.profileAssignment.legend")} />
      <div className="folder-editor-list">{draft.folders.map((folder, folderIndex) =>
        <section
          className={`folder-editor ${draggedFolderIndex === folderIndex ? "is-dragging" : ""}`}
          key={folder.id ?? folderIndex}
          onDragEnter={(event) => {
            event.preventDefault();
            if (draggedFolderIndex !== null && draggedFolderIndex !== folderIndex) {
              moveFolder(draggedFolderIndex, folderIndex);
              setDraggedFolderIndex(folderIndex);
            }
          }}
          onDragOver={(event) => {
            if (draggedFolderIndex !== null) {
              event.preventDefault();
              event.dataTransfer.dropEffect = "move";
            }
          }}
          onDrop={(event) => {
            event.preventDefault();
            setDraggedFolderIndex(null);
          }}
        >
          <header>
            <button
              type="button"
              className="folder-editor__drag"
              draggable
              onDragStart={(event) => {
                setDraggedFolderIndex(folderIndex);
                event.dataTransfer.effectAllowed = "move";
                event.dataTransfer.setData("text/plain", String(folderIndex));
              }}
              onDragEnd={() => setDraggedFolderIndex(null)}
              aria-label={translate("admin.collections.folders.dragLabel", { position: folderIndex + 1 })}
            >
              <GripVertical />
              <span>{translate("admin.collections.folders.position", { position: folderIndex + 1 })}</span>
            </button>
            <div>
              <IconButton type="button" label={translate("admin.collections.folders.moveUp")} disabled={folderIndex === 0} onClick={() => moveFolder(folderIndex, folderIndex - 1)}><ChevronUp size={17} /></IconButton>
              <IconButton type="button" label={translate("admin.collections.folders.moveDown")} disabled={folderIndex === draft.folders.length - 1} onClick={() => moveFolder(folderIndex, folderIndex + 1)}><ChevronDown size={17} /></IconButton>
              {draft.folders.length > 1 && <IconButton type="button" label={translate("admin.collections.folders.remove")} onClick={() => setDraft((current) => ({ ...current, folders: current.folders.filter((_, index) => index !== folderIndex) }))}><Trash2 size={17} /></IconButton>}
            </div>
          </header>
          <div className="form-grid form-grid--three">
            <label className="field"><span>{translate("admin.collections.folders.title")}</span><div><Film size={18} /><input value={folder.title} onChange={(event) => updateFolder(folderIndex, { title: event.target.value })} required /></div></label>
            <label className="field"><span>{translate("admin.collections.folders.tileShape")}</span><div><select value={folder.tileShape} onChange={(event) => updateFolder(folderIndex, { tileShape: event.target.value as CollectionFolder["tileShape"] })}><option value="poster">{translate("admin.collections.shapes.poster")}</option><option value="landscape">{translate("admin.collections.shapes.landscape")}</option><option value="square">{translate("admin.collections.shapes.square")}</option></select></div></label>
            <label className="field"><span>{translate("admin.collections.folders.multipleSources")}</span><div><select value={folder.sourceView ?? "merged"} onChange={(event) => updateFolder(folderIndex, { sourceView: event.target.value as CollectionFolder["sourceView"] })}><option value="merged">{translate("admin.collections.folders.sourceViewMerged")}</option><option value="categories">{translate("admin.collections.folders.sourceViewCategories")}</option><option value="folders">{translate("admin.collections.folders.sourceViewFolders")}</option></select></div></label>
            <label className="field"><span>{translate("admin.collections.folders.coverEmoji")}</span><div><Sparkles size={18} /><input value={folder.coverEmoji ?? ""} onChange={(event) => updateFolder(folderIndex, { coverEmoji: event.target.value })} placeholder="✨" /></div></label>
            <label className="field"><span>{translate("admin.collections.folders.coverImageUrl")}</span><div><ImagePlus size={18} /><input type="url" value={folder.coverImageUrl ?? ""} onChange={(event) => updateFolder(folderIndex, { coverImageUrl: event.target.value || undefined })} placeholder={translate("admin.collections.editor.imageUrlPlaceholder")} /></div></label>
            <label className="toggle-field folder-title-toggle"><input type="checkbox" checked={!folder.hideTitle} onChange={(event) => updateFolder(folderIndex, { hideTitle: !event.target.checked })} /><span><i /><div><strong>{translate("admin.collections.folders.showTitle")}</strong><small>{translate("admin.collections.folders.showTitleDescription")}</small></div></span></label>
          </div>
          <div className="source-list">
            {folder.sources.map((source, sourceIndex) =>
              <div
                className={`source-editor-shell ${draggedSource?.folderIndex === folderIndex && draggedSource.sourceIndex === sourceIndex ? "is-dragging" : ""}`}
                key={source.id ?? sourceIndex}
                onDragEnter={(event) => {
                  event.preventDefault();
                  event.stopPropagation();
                  if (draggedSource?.folderIndex === folderIndex && draggedSource.sourceIndex !== sourceIndex) {
                    moveSource(folderIndex, draggedSource.sourceIndex, sourceIndex);
                    setDraggedSource({ folderIndex, sourceIndex });
                  }
                }}
                onDragOver={(event) => {
                  if (draggedSource?.folderIndex === folderIndex) {
                    event.preventDefault();
                    event.stopPropagation();
                    event.dataTransfer.dropEffect = "move";
                  }
                }}
                onDrop={(event) => {
                  event.preventDefault();
                  event.stopPropagation();
                  setDraggedSource(null);
                }}
              >
                <header className="source-editor-order">
                  <button
                    type="button"
                    className="source-editor__drag"
                    draggable
                    onDragStart={(event) => {
                      event.stopPropagation();
                      setDraggedSource({ folderIndex, sourceIndex });
                      event.dataTransfer.effectAllowed = "move";
                      event.dataTransfer.setData("text/plain", `${folderIndex}:${sourceIndex}`);
                    }}
                    onDragEnd={() => setDraggedSource(null)}
                    aria-label={translate("admin.collections.sources.dragLabel", { position: sourceIndex + 1 })}
                  >
                    <GripVertical size={16} />
                    <span>{translate("admin.collections.sources.position", { position: sourceIndex + 1 })}</span>
                  </button>
                  <div>
                    <IconButton type="button" label={translate("admin.collections.sources.moveUp")} disabled={sourceIndex === 0} onClick={() => moveSource(folderIndex, sourceIndex, sourceIndex - 1)}><ChevronUp size={16} /></IconButton>
                    <IconButton type="button" label={translate("admin.collections.sources.moveDown")} disabled={sourceIndex === folder.sources.length - 1} onClick={() => moveSource(folderIndex, sourceIndex, sourceIndex + 1)}><ChevronDown size={16} /></IconButton>
                  </div>
                </header>
                <SourceEditor source={source} catalogs={catalogs} onChange={(value) => updateSource(folderIndex, sourceIndex, value)} onRemove={() => updateFolder(folderIndex, { sources: folder.sources.filter((_, index) => index !== sourceIndex) })} />
              </div>,
            )}
            <div className="source-add">
              <span>{translate("admin.collections.sources.add")}</span>
              <Button type="button" variant="secondary" onClick={() => addSource(folderIndex, "addon_catalog")} disabled={catalogs.length === 0}><Boxes size={16} /> {translate("admin.collections.sources.defaults.addonCatalog")}</Button>
              <Button type="button" variant="secondary" onClick={() => addSource(folderIndex, "tmdb")}><Film size={16} /> TMDB</Button>
              <Button type="button" variant="secondary" onClick={() => addSource(folderIndex, "trakt")}><Database size={16} /> Trakt</Button>
              <Button type="button" variant="secondary" onClick={() => addSource(folderIndex, "mdblist")}><Database size={16} /> MDBList</Button>
            </div>
          </div>
        </section>,
      )}</div>
      <AddTile label={translate("admin.collections.folders.addAnother")} onClick={() => setDraft((current) => ({ ...current, folders: [...current.folders, blankFolder()] }))} />
      <div className="modal-actions modal-actions--sticky">
        <Button type="button" variant="ghost" onClick={() => setEditing(null)}>{translate("common.cancel")}</Button>
        <Button type="submit" loading={saving}><Save size={18} /> {translate("admin.collections.actions.save")}</Button>
      </div>
    </form></Modal>}
    {deleting && <ConfirmDialog title={translate("admin.collections.delete.title", { title: deleting.title })} description={translate("admin.collections.delete.description")} confirmLabel={translate("admin.collections.delete.confirm")} onCancel={() => setDeleting(null)} onConfirm={() => void remove(deleting)} />}
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
      <span className={`source-kind source-kind--${source.kind}`}>{source.kind === "tmdb" ? "TMDB" : source.kind === "trakt" ? "Trakt" : source.kind === "mdblist" ? "MDBList" : translate("admin.collections.sources.defaults.addonCatalog")}</span>
      <input value={source.title} onChange={(event) => onChange({ ...source, title: event.target.value })} aria-label={translate("admin.collections.sources.categoryTitle")} placeholder={translate("admin.collections.sources.categoryTitle")} required />
      <IconButton type="button" label={translate("admin.collections.sources.remove")} onClick={onRemove}><X size={16} /></IconButton>
    </header>
    {source.addonCatalog && <label className="field"><span>{translate("admin.collections.sources.catalog")}</span><div><select value={`${source.addonCatalog.addonId}|${source.addonCatalog.type}|${source.addonCatalog.catalogId}`} onChange={(event) => {
      const [addonId, type, catalogId] = event.target.value.split("|");
      const catalog = catalogs.find((value) => value.addonId === addonId && value.catalog.type === type && value.catalog.id === catalogId);
      onChange({ ...source, title: catalog?.catalog.name || catalogId, addonCatalog: { addonId, type, catalogId } });
    }}>{catalogs.map((catalog) => <option key={`${catalog.addonId}-${catalog.catalog.type}-${catalog.catalog.id}`} value={`${catalog.addonId}|${catalog.catalog.type}|${catalog.catalog.id}`}>{catalog.manifestId} · {catalog.catalog.name || catalog.catalog.id}</option>)}</select></div></label>}
    {tmdb && <>
      <div className="form-grid form-grid--three tmdb-source-fields">
        <label className="field"><span>{translate("admin.collections.sources.sourceType")}</span><div><select value={tmdb.sourceType} onChange={(event) => {
          const sourceType = event.target.value as NonNullable<CollectionSource["tmdb"]>["sourceType"];
          const mediaType = fixedTMDBMediaType(sourceType) ?? tmdb.mediaType;
          onChange({ ...source, title: tmdbLabel(sourceType), tmdb: { ...tmdb, sourceType, tmdbId: undefined, mediaType } });
        }}>
          <option value="list">{translate("admin.collections.tmdb.sourceTypes.publicList")}</option>
          <option value="company">{translate("admin.collections.tmdb.sourceTypes.productionCompany")}</option>
          <option value="network">{translate("admin.collections.tmdb.sourceTypes.network")}</option>
          <option value="collection">{translate("admin.collections.tmdb.sourceTypes.movieCollection")}</option>
          <option value="person">{translate("admin.collections.tmdb.sourceTypes.personCredits")}</option>
          <option value="director">{translate("admin.collections.tmdb.sourceTypes.directorCredits")}</option>
          <option value="discover">{translate("admin.collections.tmdb.sourceTypes.customDiscover")}</option>
        </select></div></label>
        {tmdb.sourceType !== "discover" && <TMDBReferenceField key={tmdb.sourceType} sourceType={tmdb.sourceType} tmdbId={tmdb.tmdbId} onChange={(tmdbId) => updateTMDB({ tmdbId })} />}
        <label className="field"><span>{translate("admin.collections.sources.type")}</span><div><select value={fixedMediaType ?? tmdb.mediaType} disabled={fixedMediaType !== undefined} onChange={(event) => updateTMDB({ mediaType: event.target.value as "movie" | "series" | "both" })}>
          {fixedMediaType ? <option value={fixedMediaType}>{translate(fixedMediaType === "movie" ? "admin.collections.mediaTypes.movie" : "admin.collections.mediaTypes.series")}</option> : <><option value="movie">{translate("admin.collections.mediaTypes.movie")}</option><option value="series">{translate("admin.collections.mediaTypes.series")}</option><option value="both">{translate("admin.collections.mediaTypes.both")}</option></>}
        </select></div></label>
        <label className="field"><span>{translate("admin.collections.sources.sortBy")}</span><div><select value={tmdb.sort} onChange={(event) => updateTMDB({ sort: event.target.value })}>
          <option value="popularity.desc">{translate("admin.collections.sort.popularity")}</option>
          <option value="vote_average.desc">{translate("admin.collections.sort.rating")}</option>
          <option value="vote_count.desc">{translate("admin.collections.sort.voteCount")}</option>
          <option value="release_date.desc">{translate("admin.collections.sort.releaseDate")}</option>
          <option value="first_air_date.desc">{translate("admin.collections.sort.firstAirDate")}</option>
          <option value="original">{translate("admin.collections.sort.originalOrder")}</option>
        </select></div></label>
      </div>
      {tmdb.sourceType === "discover" && <details className="tmdb-custom-filters" open>
        <summary><ChevronDown size={15} /> {translate("admin.collections.tmdb.filters.title")}</summary>
        <div className="form-grid form-grid--three">
          <TMDBIDListField label={translate("admin.collections.tmdb.filters.genres")} value={tmdb.filters.genres} placeholder={translate("admin.collections.tmdb.filters.genresPlaceholder")} onChange={(genres) => updateTMDBFilters({ genres })} />
          <label className="field"><span>{translate("admin.collections.tmdb.filters.dateFrom")}</span><div><input type="date" value={tmdb.filters.releaseDateFrom ?? ""} onChange={(event) => updateTMDBFilters({ releaseDateFrom: event.target.value || undefined })} /></div></label>
          <label className="field"><span>{translate("admin.collections.tmdb.filters.dateTo")}</span><div><input type="date" value={tmdb.filters.releaseDateTo ?? ""} onChange={(event) => updateTMDBFilters({ releaseDateTo: event.target.value || undefined })} /></div></label>
          <label className="field"><span>{translate("admin.collections.tmdb.filters.ratingMin")}</span><div><input type="number" min={0} max={10} step={0.1} value={tmdb.filters.voteAverageMin ?? ""} onChange={(event) => updateTMDBFilters({ voteAverageMin: event.target.value ? Number(event.target.value) : undefined })} placeholder={translate("admin.collections.tmdb.filters.ratingMinPlaceholder")} /></div></label>
          <label className="field"><span>{translate("admin.collections.tmdb.filters.ratingMax")}</span><div><input type="number" min={0} max={10} step={0.1} value={tmdb.filters.voteAverageMax ?? ""} onChange={(event) => updateTMDBFilters({ voteAverageMax: event.target.value ? Number(event.target.value) : undefined })} placeholder={translate("admin.collections.tmdb.filters.ratingMaxPlaceholder")} /></div></label>
          <label className="field"><span>{translate("admin.collections.tmdb.filters.votesMin")}</span><div><input type="number" min={0} step={1} value={tmdb.filters.voteCountMin ?? ""} onChange={(event) => updateTMDBFilters({ voteCountMin: event.target.value ? Number(event.target.value) : undefined })} placeholder={translate("admin.collections.tmdb.filters.votesMinPlaceholder")} /></div></label>
          <label className="field"><span>{translate("admin.collections.tmdb.filters.language")}</span><div><input value={tmdb.filters.originalLanguage ?? ""} maxLength={3} onChange={(event) => updateTMDBFilters({ originalLanguage: event.target.value || undefined })} placeholder={translate("admin.collections.tmdb.filters.languagePlaceholder")} /></div></label>
          <label className="field"><span>{translate("admin.collections.tmdb.filters.country")}</span><div><input value={tmdb.filters.originCountry ?? ""} maxLength={2} onChange={(event) => updateTMDBFilters({ originCountry: event.target.value || undefined })} placeholder={translate("admin.collections.tmdb.filters.countryPlaceholder")} /></div></label>
          <TMDBIDListField label={translate("admin.collections.tmdb.filters.keywords")} value={tmdb.filters.keywords} placeholder={translate("admin.collections.tmdb.filters.keywordsPlaceholder")} onChange={(keywords) => updateTMDBFilters({ keywords })} />
          <TMDBIDListField label={translate("admin.collections.tmdb.filters.companies")} value={tmdb.filters.companies} placeholder={translate("admin.collections.tmdb.filters.companiesPlaceholder")} onChange={(companies) => updateTMDBFilters({ companies })} />
          <TMDBIDListField label={translate("admin.collections.tmdb.filters.networks")} value={tmdb.filters.networks} placeholder={translate("admin.collections.tmdb.filters.networksPlaceholder")} onChange={(networks) => updateTMDBFilters({ networks })} />
          <label className="field"><span>{translate("admin.collections.tmdb.filters.year")}</span><div><input type="number" min={1870} max={2200} step={1} value={tmdb.filters.year ?? ""} onChange={(event) => updateTMDBFilters({ year: event.target.value ? Number(event.target.value) : undefined })} placeholder={translate("admin.collections.tmdb.filters.yearPlaceholder")} /></div></label>
        </div>
      </details>}
    </>}
    {source.trakt && <div className="form-grid form-grid--three">
      <label className="field"><span>{translate("admin.collections.trakt.listId")}</span><div><input type="number" min={1} value={source.trakt.listId} onChange={(event) => onChange({ ...source, trakt: { ...source.trakt!, listId: Number(event.target.value) } })} /></div></label>
      <label className="field"><span>{translate("admin.collections.trakt.mediaType")}</span><div><select value={source.trakt.mediaType} onChange={(event) => onChange({ ...source, trakt: { ...source.trakt!, mediaType: event.target.value as "movie" | "series" } })}><option value="movie">{translate("admin.collections.mediaTypes.movies")}</option><option value="series">{translate("admin.collections.mediaTypes.series")}</option></select></div></label>
      <label className="field"><span>{translate("admin.collections.trakt.sort")}</span><div><select value={source.trakt.sortBy} onChange={(event) => onChange({ ...source, trakt: { ...source.trakt!, sortBy: event.target.value } })}><option value="rank">{translate("admin.collections.sort.rank")}</option><option value="added">{translate("admin.collections.sort.added")}</option><option value="title">{translate("admin.collections.sort.title")}</option><option value="released">{translate("admin.collections.sort.released")}</option><option value="popularity">{translate("admin.collections.sort.popularity")}</option><option value="votes">{translate("admin.collections.sort.votes")}</option></select></div></label>
    </div>}
    {source.mdblist && <div className="form-grid form-grid--three">
      <label className="field"><span>MDBList ID</span><div><input type="number" min={1} value={source.mdblist.listId} onChange={(event) => onChange({ ...source, mdblist: { ...source.mdblist!, listId: Number(event.target.value) } })} /></div></label>
      <label className="field"><span>{translate("admin.collections.trakt.mediaType")}</span><div><select value={source.mdblist.mediaType} onChange={(event) => onChange({ ...source, mdblist: { ...source.mdblist!, mediaType: event.target.value as "movie" | "series" } })}><option value="movie">{translate("admin.collections.mediaTypes.movies")}</option><option value="series">{translate("admin.collections.mediaTypes.series")}</option></select></div></label>
      <label className="field"><span>{translate("admin.collections.trakt.sort")}</span><div><select value={source.mdblist.sort} onChange={(event) => onChange({ ...source, mdblist: { ...source.mdblist!, sort: event.target.value } })}><option value="rank">{translate("admin.collections.sort.rank")}</option><option value="added">{translate("admin.collections.sort.added")}</option><option value="title">{translate("admin.collections.sort.title")}</option><option value="released">{translate("admin.collections.sort.released")}</option><option value="tmdbpopular">{translate("admin.collections.sort.popularity")}</option><option value="score">{translate("admin.collections.sort.rating")}</option><option value="imdbvotes">{translate("admin.collections.sort.votes")}</option></select></div></label>
      <label className="field"><span>{translate("media.episodeOrder.label")}</span><div><select value={source.mdblist.order} onChange={(event) => onChange({ ...source, mdblist: { ...source.mdblist!, order: event.target.value as "asc" | "desc" } })}><option value="asc">↑ ASC</option><option value="desc">↓ DESC</option></select></div></label>
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
    {searching && <small className="tmdb-reference__status">{translate("admin.collections.tmdb.reference.searching")}</small>}
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
  return translate(({ list: "admin.collections.tmdb.sourceTypes.publicList", company: "admin.collections.tmdb.sourceTypes.productionCompany", network: "admin.collections.tmdb.reference.networkId", collection: "admin.collections.tmdb.reference.movieCollectionId", person: "admin.collections.tmdb.sourceTypes.personCredits", director: "admin.collections.tmdb.sourceTypes.directorCredits", discover: "admin.collections.tmdb.reference.id" } as const)[sourceType]);
}

function tmdbReferencePlaceholder(sourceType: NonNullable<CollectionSource["tmdb"]>["sourceType"]): string {
  return translate(sourceType === "company" ? "admin.collections.tmdb.reference.companyPlaceholder" : sourceType === "list" ? "admin.collections.tmdb.reference.listPlaceholder" : sourceType === "person" || sourceType === "director" ? "admin.collections.tmdb.reference.personPlaceholder" : "admin.collections.tmdb.reference.numericIdPlaceholder");
}

function tmdbReferenceHelp(sourceType: NonNullable<CollectionSource["tmdb"]>["sourceType"]): string {
  const key = ({
    list: "admin.collections.tmdb.reference.listHelp",
    company: "admin.collections.tmdb.reference.companyHelp",
    network: "admin.collections.tmdb.reference.networkHelp",
    collection: "admin.collections.tmdb.reference.collectionHelp",
    person: "admin.collections.tmdb.reference.personHelp",
    director: "admin.collections.tmdb.reference.directorHelp",
    discover: null,
  } as const)[sourceType];
  return key ? translate(key) : "";
}

function fixedTMDBMediaType(sourceType: NonNullable<CollectionSource["tmdb"]>["sourceType"]): "movie" | "series" | undefined {
  if (sourceType === "network") return "series";
  if (sourceType === "list" || sourceType === "collection") return "movie";
  return undefined;
}

function tmdbLabel(sourceType: NonNullable<CollectionSource["tmdb"]>["sourceType"]): string {
  const labels = { list: "admin.collections.tmdb.sourceTypes.publicList", company: "admin.collections.tmdb.sourceTypes.productionCompany", network: "admin.collections.tmdb.sourceTypes.network", collection: "admin.collections.tmdb.sourceTypes.movieCollection", person: "admin.collections.tmdb.sourceTypes.personCredits", director: "admin.collections.tmdb.sourceTypes.directorCredits", discover: "admin.collections.tmdb.sourceTypes.customDiscover" } as const;
  return translate(labels[sourceType]);
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
      setError(notifyError(cause, translate("admin.activity.errors.load"), translate("admin.activity.errors.unavailableTitle")));
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
          setError(notifyError(cause, translate("admin.activity.errors.load"), translate("admin.activity.errors.unavailableTitle")));
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
      const messageKey = result.sessionsRemoved === 1
        ? result.jobsStopped === 1 ? "admin.activity.notifications.purgedMessageOneSessionOneJob" : "admin.activity.notifications.purgedMessageOneSession"
        : result.jobsStopped === 1 ? "admin.activity.notifications.purgedMessageOneJob" : "admin.activity.notifications.purgedMessage";
      notifySuccess(translate(messageKey, { sessionsRemoved: result.sessionsRemoved, jobsStopped: result.jobsStopped }), translate("admin.activity.notifications.purgedTitle"));
      await load(true);
    } catch (cause) {
      setError(notifyError(cause, translate("admin.activity.errors.purge"), translate("admin.activity.errors.purgeTitle")));
    } finally {
      setPurging(false);
    }
  }

  async function stopSession() {
    if (!selectedSession) return;
    setStopping(true);
    try {
      await api.stopPlaybackActivitySession(selectedSession.id);
      notifySuccess(translate("admin.activity.notifications.stoppedMessage", { title: selectedSession.title }), translate("admin.activity.notifications.stoppedTitle"));
      setSelectedSession(null);
      await load(true);
    } catch (cause) {
      setError(notifyError(cause, translate("admin.activity.errors.stop"), translate("admin.activity.errors.stopTitle")));
    } finally {
      setStopping(false);
    }
  }

  if (loading) return <div className="admin-section activity-admin">
    <div className="admin-section__header"><div><span>{translate("admin.activity.eyebrow")}</span><h2>{translate("admin.activity.title")}</h2><p>{translate("admin.activity.description")}</p></div></div>
    <div className="activity-overview" aria-label={translate("admin.activity.loadingOverviewLabel")}>{[0, 1, 2, 3].map((value) => <Skeleton key={value} className="activity-metric activity-metric--loading" />)}</div>
    <div className="admin-loading-state" role="status"><LoaderCircle className="spin" /><strong>{translate("admin.activity.loadingTitle")}</strong><span>{translate("admin.activity.loadingDescription")}</span></div>
  </div>;
  const summary = activity?.summary;
  return <div className="admin-section activity-admin">
    <div className="admin-section__header"><div><span>{translate("admin.activity.eyebrow")}</span><h2>{translate("admin.activity.title")}</h2><p>{translate("admin.activity.description")}</p></div><div className="admin-section__actions"><Button variant="secondary" onClick={() => void load()} loading={refreshing}><RefreshCw size={16} /> {translate("common.actions.refresh")}</Button><Button variant="secondary" className="admin-maintenance-action" onClick={() => void purge()} loading={purging}><HardDrive size={16} /> {translate("admin.activity.actions.purge")}</Button></div></div>
    {error && <Notice>{error}</Notice>}
    <div className="activity-overview" aria-label={translate("admin.activity.overview.label")} aria-live="polite">
      <ActivityMetric icon={<Radio />} label={translate("admin.activity.overview.sessions")} value={String(summary?.activeSessions ?? 0)} detail={translate((summary?.activeJobs ?? 0) === 1 ? "admin.activity.overview.mediaJobsOne" : "admin.activity.overview.mediaJobsMany", { count: summary?.activeJobs ?? 0 })} />
      <ActivityMetric icon={<Cpu />} label={translate("admin.activity.overview.processing")} value={`${summary?.processingSlots ?? 0} / ${summary?.processingLimit ?? 0}`} detail={translate("admin.activity.overview.ffmpegSlots")} />
      <ActivityMetric icon={<HardDrive />} label={translate("admin.activity.overview.temporaryMedia")} value={formatBytes(summary?.storageBytes ?? 0)} detail={translate("admin.activity.overview.storageLimit", { limit: formatBytes(summary?.storageLimitBytes ?? 0) })} />
      <ActivityMetric icon={<Server />} label={translate("admin.activity.overview.encoder")} value={activity?.diagnostics.videoEncoder.toUpperCase() ?? translate("admin.activity.overview.unknownEncoder")} detail={translate(activity?.diagnostics.hardwareToneMap ? "admin.activity.overview.hardwareToneMapping" : "admin.activity.overview.softwareToneMapping")} />
    </div>
    <section className="activity-panel">
      <header><div><span>{translate("admin.activity.sessions.eyebrow")}</span><h3>{translate("admin.activity.sessions.title")}</h3></div><small>{translate((activity?.sessions.length ?? 0) === 1 ? "admin.activity.sessions.activeCountOne" : "admin.activity.sessions.activeCountMany", { count: activity?.sessions.length ?? 0 })}</small></header>
      {activity?.sessions.length
        ? <div className="activity-session-list">{activity.sessions.map((session) => <article className="activity-session" key={session.id}>
          <ActivitySessionArtwork session={session} />
          <div className="activity-session__copy">
            <strong>{session.title}</strong>
            <ActivitySessionProviders session={session} />
            <span>{session.profile} · {session.username}</span>
            <small>{session.device} · {session.platform} · {activityModeLabel(session.mode)}</small>
          </div>
          <div className="activity-session__time">
            <strong>{formatActivityProgress(session.positionSeconds, session.durationSeconds)}</strong>
            <small>{activityAge(session.lastSeenAt)} · {translate("admin.activity.sessions.started", { age: activityAge(session.createdAt) })}</small>
          </div>
          <Button variant="danger" onClick={() => setSelectedSession(session)}><CircleStop size={16} />{translate("common.actions.stop")}</Button>
        </article>)}</div>
        : <EmptyState icon={<Radio />} title={translate("admin.activity.sessions.emptyTitle")} description={translate("admin.activity.sessions.emptyDescription")} />}
    </section>
    <section className="activity-panel">
      <header><div><span>{translate("admin.activity.jobs.eyebrow")}</span><h3>{translate("admin.activity.jobs.title")}</h3></div><small>{translate((activity?.jobs.length ?? 0) === 1 ? "admin.activity.jobs.countOne" : "admin.activity.jobs.countMany", { count: activity?.jobs.length ?? 0 })}</small></header>
      {activity?.jobs.length
        ? <div className="activity-job-list">{activity.jobs.map((job, index) => <article className="activity-job" key={`${job.sessionId ?? "prewarm"}-${job.assetId}-${index}`}><span className={`activity-job__dot is-${job.state}`} /><div><strong>{job.prewarming ? translate("admin.activity.jobs.preparingSource") : activityModeLabel(job.mode)}</strong><small>{job.assetId} · {translate("admin.activity.jobs.lastRequest", { age: activityAge(job.lastSeenAt) })}</small></div><span>{job.state}</span></article>)}</div>
        : <EmptyState icon={<Cpu />} title={translate("admin.activity.jobs.emptyTitle")} description={translate("admin.activity.jobs.emptyDescription")} />}
    </section>
    {selectedSession && <ConfirmDialog title={translate("admin.activity.stop.title", { title: selectedSession.title })} description={translate("admin.activity.stop.description", { device: selectedSession.device })} confirmLabel={translate("admin.activity.stop.confirm")} loading={stopping} onConfirm={() => void stopSession()} onCancel={() => setSelectedSession(null)} />}
  </div>;
}

function ActivitySessionArtwork({ session }: { session: PlaybackActivitySession }) {
  const [failedURL, setFailedURL] = useState("");
  const showArtwork = Boolean(session.artworkUrl && failedURL !== session.artworkUrl);
  return <span className={`activity-session__artwork ${session.processing ? "is-processing" : ""}`}>
    {showArtwork
      ? <img src={session.artworkUrl} alt={session.title} loading="lazy" decoding="async" referrerPolicy="no-referrer" onError={() => setFailedURL(session.artworkUrl ?? "")} />
      : <Activity size={18} aria-hidden="true" />}
  </span>;
}

function ActivitySessionProviders({ session }: { session: PlaybackActivitySession }) {
  if (!TITLE_ID_PROVIDERS.some((provider) => session.externalIds?.[provider.key])) return null;
  return <span className="activity-session__providers">
    {TITLE_ID_PROVIDERS.map((provider) => {
      const externalID = session.externalIds?.[provider.key];
      if (!externalID) return null;
      const href = titleProviderURL(provider.key, externalID, session.externalIdMediaTypes?.[provider.key] ?? session.mediaType);
      const label = `${provider.label} · ${externalID}`;
      const contents = <><span>{provider.label}</span>{href && <ExternalLink size={9} aria-hidden="true" />}</>;
      return href
        ? <a key={provider.key} className={`activity-session__provider is-${provider.key}`} href={href} target="_blank" rel="noreferrer" aria-label={label} title={label}>{contents}</a>
        : <span key={provider.key} className={`activity-session__provider is-${provider.key}`} aria-label={label} title={label}>{contents}</span>;
    })}
  </span>;
}

function ActivityMetric({ icon, label, value, detail }: { icon: React.ReactNode; label: string; value: string; detail: string }) {
  return <article className="activity-metric"><span>{icon}</span><div><small>{label}</small><strong>{value}</strong><span>{detail}</span></div></article>;
}

function activityModeLabel(mode: string): string {
  const key = {
    direct: "admin.activity.modes.direct",
    remux: "admin.activity.modes.remux",
    transcode_audio: "admin.activity.modes.audioConversion",
    transcode: "admin.activity.modes.videoTranscode",
  } as const;
  return mode in key ? translate(key[mode as keyof typeof key]) : mode;
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
  if (seconds < 10) return translate("admin.activity.age.justNow");
  if (seconds < 60) return translate("admin.activity.age.secondsAgo", { count: seconds });
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return translate("admin.activity.age.minutesAgo", { count: minutes });
  const hours = Math.floor(minutes / 60);
  return translate("admin.activity.age.hoursAgo", { count: hours });
}

function formatActivityProgress(positionSeconds: number, durationSeconds: number): string {
  if (!Number.isFinite(durationSeconds) || durationSeconds <= 0) return translate("admin.activity.progress.durationUnavailable");
  const positionMinutes = Math.max(0, Math.floor(positionSeconds / 60));
  const durationMinutes = Math.max(1, Math.ceil(durationSeconds / 60));
  return translate("admin.activity.progress.minutes", { positionMinutes: Math.min(positionMinutes, durationMinutes), durationMinutes });
}

const rivuneSettingDefaults = {
  interfaceLanguage: "en",
  theme: "system",
  maximumResolution: "auto",
  preferDirectPlay: true,
  hideUnreleased: false,
  metadataLanguage: "auto",
  metadataRegion: "auto",
  seriesMappingProvider: "tmdb",
  audioLanguage: "auto",
  subtitleLanguage: "auto",
  forcedSubtitleLanguage: "off",
  autoplayNextEpisode: true,
  skipIntroEnabled: true,
  skipRecapEnabled: true,
  skipOutroEnabled: true,
  cardDensity: "comfortable",
  animationsEnabled: true,
  subtitleSizePercent: 100,
  subtitleTextColor: "#FFFFFF",
  subtitleBackgroundOpacityPercent: 60,
  notificationsEnabled: true,
  notificationDurationSeconds: 5,
  notificationPollIntervalSeconds: 5,
} as const;

type SettingOption = { readonly value: string } & (
  | { readonly label: string }
  | { readonly labelKey: TranslationKey }
);

const settingOptions = {
  interfaceLanguage: interfaceLanguages,
  theme: [{ value: "system", labelKey: "settings.theme.system" }, { value: "dark", labelKey: "settings.theme.dark" }, { value: "light", labelKey: "settings.theme.light" }],
  resolution: [{ value: "auto", labelKey: "settings.resolution.auto" }, { value: "2160p", labelKey: "settings.resolution.2160p" }, { value: "1080p", labelKey: "settings.resolution.1080p" }, { value: "720p", labelKey: "settings.resolution.720p" }, { value: "480p", labelKey: "settings.resolution.480p" }],
  density: [{ value: "comfortable", labelKey: "settings.density.comfortable" }, { value: "compact", labelKey: "settings.density.compact" }],
  language: [{ value: "auto", labelKey: "settings.language.auto" }, { value: "fr-FR", label: "Français" }, { value: "en-US", labelKey: "languages.english" }, { value: "es-ES", label: "Español" }, { value: "de-DE", label: "Deutsch" }, { value: "it-IT", label: "Italiano" }, { value: "pt-BR", label: "Português" }, { value: "ja-JP", label: "日本語" }],
  region: [{ value: "auto", labelKey: "settings.region.auto" }, { value: "FR", labelKey: "regions.france" }, { value: "BE", labelKey: "regions.belgium" }, { value: "CA", labelKey: "regions.canada" }, { value: "CH", labelKey: "regions.switzerland" }, { value: "US", labelKey: "regions.unitedStates" }, { value: "GB", labelKey: "regions.unitedKingdom" }, { value: "DE", labelKey: "regions.germany" }, { value: "ES", labelKey: "regions.spain" }, { value: "IT", labelKey: "regions.italy" }, { value: "JP", labelKey: "regions.japan" }],
  mapping: [{ value: "tmdb", labelKey: "settings.seriesMapping.tmdb" }, { value: "tvdb", labelKey: "settings.seriesMapping.tvdb" }],
} as const satisfies Record<string, ReadonlyArray<SettingOption>>;


function SettingsAdmin() {
  const { account, activeProfile, updateServerInterfaceLanguage } = useAuth();
  const [settingsTarget, setSettingsTarget] = useState(activeProfile?.id ?? "");
  const [instance, setInstance] = useState<SettingsValues>({});
  const [savedInstance, setSavedInstance] = useState<SettingsValues>({});
  const [profile, setProfile] = useState<SettingsValues>({});
  const [savedProfile, setSavedProfile] = useState<SettingsValues>({});
  const [inherited, setInherited] = useState<SettingsValues>({});
  const [maintenance, setMaintenance] = useState<MaintenanceSettings>({ enabled: false, message: null });
  const [savedMaintenance, setSavedMaintenance] = useState<MaintenanceSettings>({ enabled: false, message: null });
  const [saving, setSaving] = useState(false);
  const [savingMaintenance, setSavingMaintenance] = useState(false);
  const [error, setError] = useState("");
  const [loaded, setLoaded] = useState(false);
  const settingsTargetRef = useRef(settingsTarget);
  settingsTargetRef.current = settingsTarget;
  const canManageProfiles = Boolean(activeProfile?.canManage);
  const canManageServer = canManageProfiles && account?.user.role === "admin";
  const serverSelected = settingsTarget === "server";
  const targetProfile = account?.profiles.find((candidate) => candidate.id === settingsTarget) ?? activeProfile;
  const settingsDirty = serverSelected ? JSON.stringify(instance) !== JSON.stringify(savedInstance) : JSON.stringify(profile) !== JSON.stringify(savedProfile);
  const maintenanceDirty = maintenance.enabled !== savedMaintenance.enabled || maintenance.message !== savedMaintenance.message;
  const hasUnsavedChanges = settingsDirty || (serverSelected && maintenanceDirty);
  const overrideCount = Object.values(serverSelected ? instance : profile).filter((value) => value !== null && value !== undefined).length;

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
        const [layer, maintenanceSettings] = await Promise.all([api.instanceSettings(), api.maintenanceSettings()]);
        if (!current) return;
        setInstance(layer.settings);
        setSavedInstance(layer.settings);
        setMaintenance(maintenanceSettings);
        setSavedMaintenance(maintenanceSettings);
        setInherited({});
        return;
      }
      const [layer, serverDefaults] = await Promise.all([api.profileSettings(target), api.instanceSettings()]);
      if (!current) return;
      setProfile(layer.settings);
      setSavedProfile(layer.settings);
      setInherited(serverDefaults.settings);
    })()
      .catch((cause) => {
        if (current) setError(notifyError(cause, translate("settings.errors.load"), translate("settings.errors.unavailableTitle")));
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
    const profileName = account?.profiles.find((candidate) => candidate.id === target)?.name ?? translate("settings.scope.profileFallback");
    setSaving(true);
    setError("");
    try {
      if (savingServer) {
        const updated = await api.updateInstanceSettings(instance);
        if (settingsTargetRef.current === target) {
          setInstance(updated.settings);
          setSavedInstance(updated.settings);
        }
        updateServerInterfaceLanguage(updated.settings.interfaceLanguage ?? "en");
      } else {
        const updated = await api.updateProfileSettings(target, profile);
        if (settingsTargetRef.current === target) {
          setProfile(updated.settings);
          setSavedProfile(updated.settings);
        }
      }
      if (!savingServer && target === activeProfile.id) window.dispatchEvent(new Event("rivune:settings-changed"));
      notifySuccess(savingServer ? translate("settings.notifications.serverSavedMessage") : translate("settings.notifications.profileSavedMessage", { profileName }), translate("settings.notifications.savedTitle"));
    } catch (cause) {
      setError(notifyError(cause, translate("settings.errors.save"), translate("settings.errors.saveTitle")));
    } finally {
      setSaving(false);
    }
  }

  async function saveMaintenance() {
    setSavingMaintenance(true);
    setError("");
    try {
      const updated = await api.updateMaintenanceSettings(maintenance);
      setMaintenance(updated);
      setSavedMaintenance(updated);
      notifySuccess(translate("admin.maintenance.saved"), translate("admin.maintenance.savedTitle"));
    } catch (cause) {
      setError(notifyError(cause, translate("admin.maintenance.error"), translate("admin.maintenance.title")));
    } finally {
      setSavingMaintenance(false);
    }
  }

  if (!loaded) return <Skeleton className="settings-skeleton" />;
  const profileName = targetProfile?.name ?? translate("settings.scope.profileFallback");
  const scopeName = serverSelected ? translate("settings.scope.serverDefaults") : translate("settings.scope.profileOverrides", { profileName });
  return <div className="admin-section preferences-admin">
    <div className={`settings-scope settings-scope--${serverSelected ? "server" : "profile"}`}>
      <span className="settings-scope__icon">{serverSelected ? <Server size={22} /> : <CircleUserRound size={22} />}</span>
      <div className="settings-scope__copy">
        <small>{translate("settings.scope.eyebrow")}</small>
        <strong>{scopeName}</strong>
        <p>{serverSelected ? translate("settings.scope.serverDescription") : translate("settings.scope.profileDescription")}</p>
        <span>{serverSelected
          ? translate("settings.scope.serverDefaultCount", { count: overrideCount })
          : translate(overrideCount === 1 ? "settings.scope.profileOverrideCountOne" : "settings.scope.profileOverrideCountMany", { count: overrideCount })}</span>
      </div>
      {canManageProfiles && <label className="field settings-profile-picker"><span>{translate("settings.scope.switch")}</span><div>{serverSelected ? <Server size={18} /> : <CircleUserRound size={18} />}<select value={settingsTarget} disabled={saving || savingMaintenance || hasUnsavedChanges} onChange={(event) => setSettingsTarget(event.target.value)}>{canManageServer && <option value="server">{translate("settings.scope.serverDefaults")}</option>}{account?.profiles.map((candidate) => <option key={candidate.id} value={candidate.id}>{translate("settings.scope.profileOption", { profileName: candidate.name })}</option>)}</select></div>{hasUnsavedChanges && <small>{translate("settings.scope.unsavedSwitchHint")}</small>}</label>}
    </div>
    {error && <Notice>{error}</Notice>}
    {serverSelected
      ? <>
        <SettingsCard title={translate("settings.server.title")} description={translate("settings.server.description")} icon={<Server />} values={instance} defaults={rivuneSettingDefaults} onChange={setInstance} onSave={() => void save()} onReset={() => setInstance(savedInstance)} saving={saving} dirty={settingsDirty} emptyLabel={translate("settings.defaults.rivune")} />
        <MaintenanceCard values={maintenance} onChange={setMaintenance} onSave={() => void saveMaintenance()} onReset={() => setMaintenance(savedMaintenance)} saving={savingMaintenance} dirty={maintenanceDirty} />
      </>
      : <>
        <SettingsCard title={translate("settings.profile.title", { profileName })} description={translate("settings.profile.description")} icon={<CircleUserRound />} values={profile} defaults={{ ...rivuneSettingDefaults, ...inherited }} onChange={setProfile} onSave={() => void save()} onReset={() => setProfile(savedProfile)} saving={saving} dirty={settingsDirty} emptyLabel={translate("settings.defaults.server")} />
        <TrackingSettings profileId={settingsTarget} />
      </>}
  </div>;
}

function TrackingSettings({ profileId }: { profileId: string }) {
  const [providers, setProviders] = useState<TrackingStatus[]>([]);
  const [authorization, setAuthorization] = useState<TrackingDeviceAuthorization | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadFailed, setLoadFailed] = useState(false);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");

  async function load() {
    const response = await api.trackingStatuses(profileId);
    setProviders(response.providers);
    setLoadFailed(false);
  }

  useEffect(() => {
    let current = true;
    setLoading(true);
    setLoadFailed(false);
    setAuthorization(null);
    setError("");
    void api.trackingStatuses(profileId)
      .then((response) => {
        if (!current) return;
        setProviders(response.providers);
        setLoadFailed(false);
      })
      .catch((cause) => {
        if (!current) return;
        setLoadFailed(true);
        setError(notifyError(cause, translate("settings.trackingLoadError"), translate("settings.trackingTitle")));
      })
      .finally(() => { if (current) setLoading(false); });
    return () => { current = false; };
  }, [profileId]);

  async function retryLoad() {
    setLoading(true);
    setError("");
    setLoadFailed(false);
    try {
      await load();
    } catch (cause) {
      setLoadFailed(true);
      setError(notifyError(cause, translate("settings.trackingLoadError"), translate("settings.trackingTitle")));
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    if (!authorization) return;
    let cancelled = false;
    let timer = 0;
    const poll = async () => {
      if (cancelled) return;
      if (Date.now() >= new Date(authorization.expiresAt).getTime()) {
        setAuthorization(null);
        setError(translate("settings.trackingCodeExpired"));
        return;
      }
      try {
        const result = await api.completeTrackingAuthorization(profileId, authorization.provider, authorization.id);
        if ("pending" in result) {
          timer = window.setTimeout(() => void poll(), authorization.intervalSeconds * 1000);
          return;
        }
        await load();
        if (!cancelled) {
          setAuthorization(null);
          notifySuccess(translate("settings.trackingConnectedMessage", { provider: providerName(result.provider) }), translate("settings.trackingConnected"));
        }
      } catch (cause) {
        if (cancelled) return;
        if (cause instanceof APIError && cause.status === 429 && cause.code === "tracking_authorization_slow_down") {
          timer = window.setTimeout(() => void poll(), authorization.intervalSeconds * 1000);
          return;
        }
        setAuthorization(null);
        setError(notifyError(cause, translate("settings.trackingConnectError"), translate("settings.trackingTitle")));
      }
    };
    timer = window.setTimeout(() => void poll(), authorization.intervalSeconds * 1000);
    return () => { cancelled = true; window.clearTimeout(timer); };
  }, [authorization, profileId]);

  async function connect(provider: TrackingProvider) {
    setBusy(`${provider}:connect`);
    setError("");
    try {
      const next = await api.beginTrackingAuthorization(profileId, provider);
      setAuthorization(next);
      window.open(next.verificationUrl, "_blank", "noopener,noreferrer");
    } catch (cause) {
      setError(notifyError(cause, translate("settings.trackingConnectError"), translate("settings.trackingTitle")));
    } finally {
      setBusy("");
    }
  }

  async function disconnect(provider: TrackingProvider) {
    setBusy(`${provider}:disconnect`);
    setError("");
    try {
      await api.disconnectTracking(profileId, provider);
      if (authorization?.provider === provider) setAuthorization(null);
      await load();
      notifySuccess(translate("settings.trackingDisconnectedMessage", { provider: providerName(provider) }), translate("settings.trackingDisconnected"));
    } catch (cause) {
      setError(notifyError(cause, translate("settings.trackingDisconnectError"), translate("settings.trackingTitle")));
    } finally {
      setBusy("");
    }
  }

  async function toggle(status: TrackingStatus, key: "syncWatched" | "syncProgress" | "syncLibrary", value: boolean) {
    setBusy(`${status.provider}:${key}`);
    setError("");
    try {
      const updated = await api.updateTrackingPreferences(profileId, status.provider, { [key]: value });
      setProviders((current) => current.map((candidate) => candidate.provider === updated.provider ? updated : candidate));
    } catch (cause) {
      setError(notifyError(cause, translate("settings.trackingSaveError"), translate("settings.trackingTitle")));
    } finally {
      setBusy("");
    }
  }

  if (loading) return <Skeleton className="settings-skeleton" />;
  return <section className="settings-card tracking-settings">
    <header>
      <span><RefreshCw /></span>
      <div><small>{translate("settings.tracking.eyebrow")}</small><h3>{translate("settings.trackingTitle")}</h3><p>{translate("settings.trackingDescription")}</p></div>
      <span className="settings-card__meta"><Check size={14} /> {translate("settings.tracking.autoSave")}</span>
    </header>
    {error && <Notice>{error}</Notice>}
    {authorization && <Notice tone="info"><div className="tracking-authorization" aria-live="polite"><strong>{translate("settings.trackingEnterCode", { provider: providerName(authorization.provider) })}</strong><code>{authorization.userCode}</code><a href={authorization.verificationUrl} target="_blank" rel="noreferrer">{translate("settings.trackingOpenProvider")} <ExternalLink size={14} /></a><small><LoaderCircle size={13} className="spin" /> {translate("settings.tracking.waitingForAuthorization")}</small></div></Notice>}
    {loadFailed
      ? <div className="tracking-load-retry"><Button variant="secondary" onClick={() => void retryLoad()}><RefreshCw size={16} /> {translate("settings.tracking.retryLoading")}</Button></div>
      : providers.length > 0
        ? <div className="settings-groups tracking-provider-grid">{providers.map((status) => {
          const providerBusy = busy.startsWith(`${status.provider}:`);
          return <SettingsGroup key={status.provider} icon={<TrackingProviderIcon provider={status.provider} />} iconClassName={`tracking-provider-tile tracking-provider-tile--${status.provider}`} title={providerName(status.provider)} description={status.connected ? translate("settings.tracking.connectedDescription") : status.configured ? translate("settings.trackingConnectDescription") : translate("settings.trackingAdminRequired")} status={status.connected ? translate("settings.trackingStatusConnected") : status.configured ? translate("settings.trackingStatusDisconnected") : translate("settings.trackingStatusUnavailable")} statusTone={status.connected ? "connected" : status.configured ? "disconnected" : "unavailable"}>
            {status.connected ? <>
              <TrackingToggle label={translate("settings.trackingWatched")} description={translate("settings.trackingWatchedDescription")} checked={status.syncWatched} disabled={providerBusy} saving={busy === `${status.provider}:syncWatched`} onChange={(value) => void toggle(status, "syncWatched", value)} />
              <TrackingToggle label={translate("settings.trackingProgress")} description={translate("settings.trackingProgressDescription")} checked={status.syncProgress} disabled={providerBusy} saving={busy === `${status.provider}:syncProgress`} onChange={(value) => void toggle(status, "syncProgress", value)} />
              <TrackingToggle label={translate("settings.trackingLibrary")} description={translate("settings.trackingLibraryDescription")} checked={status.syncLibrary} disabled={providerBusy} saving={busy === `${status.provider}:syncLibrary`} onChange={(value) => void toggle(status, "syncLibrary", value)} />
              <div className="tracking-provider-action"><small aria-live="polite">{status.pendingItems ? translate("settings.trackingPending", { count: status.pendingItems }) : status.lastError ? translate("settings.trackingRetrying") : status.lastSuccessAt ? translate("settings.trackingLastSuccess", { date: new Date(status.lastSuccessAt).toLocaleString() }) : translate("settings.trackingReady")}</small><Button variant="secondary" loading={busy === `${status.provider}:disconnect`} disabled={providerBusy} onClick={() => void disconnect(status.provider)}>{translate("settings.trackingDisconnect")}</Button></div>
            </> : <div className="tracking-provider-action"><small>{status.configured ? translate("settings.tracking.deviceFlowDescription") : translate("settings.trackingStatusUnavailable")}</small><Button disabled={!status.configured || Boolean(authorization) || providerBusy} loading={busy === `${status.provider}:connect`} onClick={() => void connect(status.provider)}>{translate("settings.trackingConnect")}</Button></div>}
          </SettingsGroup>;
        })}</div>
        : <div className="tracking-empty"><RefreshCw size={26} /><strong>{translate("settings.tracking.emptyTitle")}</strong><p>{translate("settings.tracking.emptyDescription")}</p></div>}
  </section>;
}

function TrackingToggle({ label, description, checked, disabled, saving, onChange }: { label: string; description: string; checked: boolean; disabled: boolean; saving: boolean; onChange: (value: boolean) => void }) {
  return <div className="setting-control setting-control--toggle"><label className="toggle-field"><input type="checkbox" checked={checked} disabled={disabled} onChange={(event) => onChange(event.target.checked)} /><span><i /><div><strong>{label}</strong><small>{description}</small></div>{saving && <LoaderCircle size={15} className="spin tracking-toggle__saving" />}</span></label></div>;
}

function providerName(provider: TrackingProvider): string {
  return provider === "trakt" ? "Trakt" : "Simkl";
}
function TrackingProviderIcon({ provider }: { provider: TrackingProvider }) {
  const path = provider === "trakt"
    ? "m15.082 15.107-.73-.73 9.578-9.583a4.499 4.499 0 0 0-.115-.575L13.662 14.382l1.08 1.08-.73.73-1.81-1.81L23.422 3.144c-.075-.15-.155-.3-.25-.44L11.508 14.377l2.154 2.155-.73.73-7.193-7.199.73-.73 4.309 4.31L22.546 1.86A5.618 5.618 0 0 0 18.362 0H5.635A5.637 5.637 0 0 0 0 5.634V18.37A5.632 5.632 0 0 0 5.635 24h12.732C21.477 24 24 21.48 24 18.37V6.19l-8.913 8.918zm-4.314-2.155L6.814 8.988l.73-.73 3.954 3.96zm1.075-1.084-3.954-3.96.73-.73 3.959 3.96zm9.853 5.688a4.141 4.141 0 0 1-4.14 4.14H6.438a4.144 4.144 0 0 1-4.139-4.14V6.438A4.141 4.141 0 0 1 6.44 2.3h10.387v1.04H6.438c-1.71 0-3.099 1.39-3.099 3.1V17.55c0 1.71 1.39 3.105 3.1 3.105h11.117c1.71 0 3.1-1.395 3.1-3.105v-1.754h1.04v1.754z"
    : "M3.84 0A3.832 3.832 0 0 0 0 3.84v16.32A3.832 3.832 0 0 0 3.84 24h16.32A3.832 3.832 0 0 0 24 20.16V3.84A3.832 3.832 0 0 0 20.16 0zm8.567 4.11c2.074 0 3.538.061 4.393.186 1.127.168 1.94.46 2.438.877.672.578 1.009 1.613 1.009 3.104 0 .161-.004.417-.01.768h-4.234c-.014-.358-.039-.607-.074-.746-.098-.41-.42-.64-.966-.692-.484-.043-1.66-.066-3.53-.066-1.85 0-2.946.056-3.289.165-.385.133-.578.474-.578 1.024 0 .528.203.851.61.969.343.095 1.887.187 4.633.275 2.487.073 4.073.165 4.76.275.693.11 1.244.275 1.654.495.41.22.737.532.983.936.37.595.557 1.552.557 2.873 0 1.475-.182 2.557-.546 3.247-.364.683-.96 1.149-1.785 1.398-.812.25-3.05.374-6.71.374-2.226 0-3.832-.062-4.82-.187-1.204-.147-2.068-.434-2.593-.86-.567-.456-.903-1.1-1.008-1.93a10.522 10.522 0 0 1-.085-1.434v-.789H7.44c-.007.74.136 1.216.43 1.428.154.102.33.167.525.203.196.037.54.063 1.03.077a166.2 166.2 0 0 0 2.405.022c1.862-.007 2.94-.018 3.234-.033.553-.044.917-.12 1.092-.23.245-.161.368-.52.368-1.077 0-.38-.078-.648-.231-.802-.211-.212-.712-.325-1.503-.34-.547 0-1.688-.044-3.425-.132-1.794-.088-2.956-.14-3.488-.154-1.387-.044-2.364-.212-2.932-.505-.728-.373-1.205-1.01-1.429-1.91-.126-.498-.189-1.15-.189-1.956 0-1.698.309-2.895.925-3.59.462-.527 1.163-.875 2.102-1.044.848-.146 2.865-.22 6.053-.22z";

  return <svg className="tracking-provider-icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path fill="currentColor" d={path} /></svg>;
}


function MaintenanceCard({ values, onChange, onSave, onReset, saving, dirty }: { values: MaintenanceSettings; onChange: (values: MaintenanceSettings) => void; onSave: () => void; onReset: () => void; saving: boolean; dirty: boolean }) {
  const message = values.message ?? "";
  return <section className="settings-card settings-card--danger maintenance-settings">
    <header><span><Shield /></span><div><small>{translate("admin.maintenance.eyebrow")}</small><h3>{translate("admin.maintenance.title")}</h3><p>{translate("admin.maintenance.description")}</p></div><span className={`settings-save-state ${dirty ? "is-dirty" : "is-saved"}`} role="status" aria-live="polite">{saving ? <><LoaderCircle size={14} className="spin" /> {translate("common.status.saving")}</> : dirty ? <><Save size={14} /> {translate("common.status.unsavedChanges")}</> : <><Check size={14} /> {translate("common.status.saved")}</>}</span></header>
    <div className="maintenance-settings__body">
      <div className="setting-control setting-control--toggle">
        <label className="toggle-field"><input type="checkbox" checked={values.enabled} onChange={(event) => onChange({ ...values, enabled: event.target.checked })} /><span><i /><div><strong>{translate("admin.maintenance.enabled")}</strong><small>{translate("admin.maintenance.enabledDescription")}</small></div></span></label>
      </div>
      <label className="field"><span>{translate("admin.maintenance.message")}</span><div><textarea value={message} placeholder={translate("admin.maintenance.placeholder")} onChange={(event) => { if (countCodePoints(event.target.value) <= 500) onChange({ ...values, message: event.target.value || null }); }} /></div><small>{translate("admin.maintenance.characterCount", { count: countCodePoints(message) })}</small></label>
    </div>
    <footer><div><strong>{translate(values.enabled ? "admin.maintenance.accessBlocked" : "admin.maintenance.accessAvailable")}</strong><small>{translate("admin.maintenance.separateSaveDescription")}</small></div><Button variant="secondary" disabled={!dirty || saving} onClick={onReset}>{translate("common.actions.discardChanges")}</Button><Button loading={saving} disabled={!dirty} onClick={onSave}><Check size={18} /> {translate("admin.maintenance.save")}</Button></footer>
  </section>;
}

function SettingsCard({ title, description, icon, values, defaults = {}, onChange, onSave, onReset, saving, dirty, emptyLabel = translate("settings.defaults.server") }: { title: string; description: string; icon: React.ReactNode; values: SettingsValues; defaults?: SettingsValues; onChange: (values: SettingsValues) => void; onSave: () => void; onReset: () => void; saving: boolean; dirty: boolean; emptyLabel?: string }) {
  const effective = {
    interfaceLanguage: defaults.interfaceLanguage ?? rivuneSettingDefaults.interfaceLanguage,
    theme: defaults.theme ?? rivuneSettingDefaults.theme,
    maximumResolution: defaults.maximumResolution ?? rivuneSettingDefaults.maximumResolution,
    preferDirectPlay: defaults.preferDirectPlay ?? rivuneSettingDefaults.preferDirectPlay,
    hideUnreleased: defaults.hideUnreleased ?? rivuneSettingDefaults.hideUnreleased,
    metadataLanguage: defaults.metadataLanguage ?? rivuneSettingDefaults.metadataLanguage,
    metadataRegion: defaults.metadataRegion ?? rivuneSettingDefaults.metadataRegion,
    seriesMappingProvider: defaults.seriesMappingProvider ?? rivuneSettingDefaults.seriesMappingProvider,
    audioLanguage: defaults.audioLanguage ?? rivuneSettingDefaults.audioLanguage,
    subtitleLanguage: defaults.subtitleLanguage ?? rivuneSettingDefaults.subtitleLanguage,
    forcedSubtitleLanguage: defaults.forcedSubtitleLanguage ?? rivuneSettingDefaults.forcedSubtitleLanguage,
    autoplayNextEpisode: defaults.autoplayNextEpisode ?? rivuneSettingDefaults.autoplayNextEpisode,
    skipIntroEnabled: defaults.skipIntroEnabled ?? rivuneSettingDefaults.skipIntroEnabled,
    skipRecapEnabled: defaults.skipRecapEnabled ?? rivuneSettingDefaults.skipRecapEnabled,
    skipOutroEnabled: defaults.skipOutroEnabled ?? rivuneSettingDefaults.skipOutroEnabled,
    cardDensity: defaults.cardDensity ?? rivuneSettingDefaults.cardDensity,
    animationsEnabled: defaults.animationsEnabled ?? rivuneSettingDefaults.animationsEnabled,
    subtitleSizePercent: defaults.subtitleSizePercent ?? rivuneSettingDefaults.subtitleSizePercent,
    subtitleTextColor: defaults.subtitleTextColor ?? rivuneSettingDefaults.subtitleTextColor,
    subtitleBackgroundOpacityPercent: defaults.subtitleBackgroundOpacityPercent ?? rivuneSettingDefaults.subtitleBackgroundOpacityPercent,
    notificationsEnabled: defaults.notificationsEnabled ?? rivuneSettingDefaults.notificationsEnabled,
    notificationDurationSeconds: defaults.notificationDurationSeconds ?? rivuneSettingDefaults.notificationDurationSeconds,
    notificationPollIntervalSeconds: defaults.notificationPollIntervalSeconds ?? rivuneSettingDefaults.notificationPollIntervalSeconds,
  };
  function change<K extends keyof SettingsValues>(key: K, value: SettingsValues[K]) {
    onChange({ ...values, [key]: value });
  }

  return <section className="settings-card preferences-workspace">
    <header>
      <span>{icon}</span>
      <div><small>{translate("settings.card.eyebrow")}</small><h3>{title}</h3><p>{description}</p></div>
      <div className="settings-card__actions">
        <span className={`settings-save-state ${dirty ? "is-dirty" : "is-saved"}`} role="status" aria-live="polite">{saving ? <><LoaderCircle size={14} className="spin" /> {translate("common.status.saving")}</> : dirty ? <><Save size={14} /> {translate("common.status.unsavedChanges")}</> : <><Check size={14} /> {translate("common.status.allChangesSaved")}</>}</span>
        <Button variant="secondary" disabled={!dirty || saving} onClick={onReset}>{translate("common.actions.discardChanges")}</Button>
        <Button loading={saving} disabled={!dirty} onClick={onSave}><Check size={18} /> {translate("settings.actions.savePreferences")}</Button>
      </div>
    </header>
    <div className="settings-groups settings-groups--preferences">
      <SettingsGroup icon={<Palette />} title={translate("settings.groups.appearance.title")} description={translate("settings.groups.appearance.description")} className="settings-group--wide">
        <SelectSetting label={translate("settings.fields.theme")} value={values.theme} defaultValue={effective.theme} options={settingOptions.theme} emptyLabel={emptyLabel} onChange={(value) => change("theme", value)} />
        <SelectSetting label={translate("settings.fields.cardDensity")} value={values.cardDensity} defaultValue={effective.cardDensity} options={settingOptions.density} emptyLabel={emptyLabel} onChange={(value) => change("cardDensity", value as "comfortable" | "compact" | null)} />
        <InheritedToggle label={translate("settings.fields.animations")} description={translate("settings.fields.animationsDescription")} value={values.animationsEnabled} defaultValue={effective.animationsEnabled} onChange={(value) => change("animationsEnabled", value)} emptyLabel={emptyLabel} />
        <InheritedToggle label={translate("settings.fields.hideUnreleased")} description={translate("settings.fields.hideUnreleasedDescription")} value={values.hideUnreleased} defaultValue={effective.hideUnreleased} onChange={(value) => change("hideUnreleased", value)} emptyLabel={emptyLabel} />
      </SettingsGroup>

      <SettingsGroup icon={<Film />} title={translate("settings.groups.playback.title")} description={translate("settings.groups.playback.description")} className="settings-group--wide">
        <SelectSetting label={translate("settings.fields.maximumResolution")} value={values.maximumResolution} defaultValue={effective.maximumResolution} options={settingOptions.resolution} emptyLabel={emptyLabel} onChange={(value) => change("maximumResolution", value)} />
        <InheritedToggle label={translate("settings.fields.preferDirectPlay")} description={translate("settings.fields.preferDirectPlayDescription")} value={values.preferDirectPlay} defaultValue={effective.preferDirectPlay} onChange={(value) => change("preferDirectPlay", value)} emptyLabel={emptyLabel} />
        <InheritedToggle label={translate("settings.fields.autoplayNextEpisode")} description={translate("settings.fields.autoplayNextEpisodeDescription")} value={values.autoplayNextEpisode} defaultValue={effective.autoplayNextEpisode} onChange={(value) => change("autoplayNextEpisode", value)} emptyLabel={emptyLabel} />
        <InheritedToggle label={translate("settings.skipIntro")} description={translate("settings.skipIntroDescription")} value={values.skipIntroEnabled} defaultValue={effective.skipIntroEnabled} onChange={(value) => change("skipIntroEnabled", value)} emptyLabel={emptyLabel} />
        <InheritedToggle label={translate("settings.skipRecap")} description={translate("settings.skipRecapDescription")} value={values.skipRecapEnabled} defaultValue={effective.skipRecapEnabled} onChange={(value) => change("skipRecapEnabled", value)} emptyLabel={emptyLabel} />
        <InheritedToggle label={translate("settings.skipOutro")} description={translate("settings.skipOutroDescription")} value={values.skipOutroEnabled} defaultValue={effective.skipOutroEnabled} onChange={(value) => change("skipOutroEnabled", value)} emptyLabel={emptyLabel} />
      </SettingsGroup>

      <SettingsGroup icon={<Languages />} title={translate("settings.groups.languageMetadata.title")} description={translate("settings.groups.languageMetadata.description")}>
        <SelectSetting name="interfaceLanguage" label={translate("settings.interfaceLanguage")} description={translate("settings.interfaceLanguageDescription")} value={values.interfaceLanguage} defaultValue={effective.interfaceLanguage} options={settingOptions.interfaceLanguage} emptyLabel={emptyLabel} onChange={(value) => change("interfaceLanguage", value as InterfaceLanguage | null)} />
        <SelectSetting label={translate("settings.fields.metadataLanguage")} value={values.metadataLanguage} defaultValue={effective.metadataLanguage} options={settingOptions.language} emptyLabel={emptyLabel} onChange={(value) => change("metadataLanguage", value)} />
        <SelectSetting label={translate("settings.fields.metadataRegion")} value={values.metadataRegion} defaultValue={effective.metadataRegion} options={settingOptions.region} emptyLabel={emptyLabel} onChange={(value) => change("metadataRegion", value)} />
        <SelectSetting label={translate("settings.fields.seriesMapping")} value={values.seriesMappingProvider} defaultValue={effective.seriesMappingProvider} options={settingOptions.mapping} emptyLabel={emptyLabel} onChange={(value) => change("seriesMappingProvider", value as "tmdb" | "tvdb" | null)} />
        <TextSetting label={translate("settings.fields.audioLanguage")} value={values.audioLanguage} defaultValue={effective.audioLanguage} placeholder={translate("settings.fields.languageCodePlaceholder")} emptyLabel={emptyLabel} onChange={(value) => change("audioLanguage", value)} />
      </SettingsGroup>

      <SettingsGroup icon={<Captions />} title={translate("settings.groups.subtitles.title")} description={translate("settings.groups.subtitles.description")}>
        <TextSetting label={translate("settings.fields.subtitleLanguage")} value={values.subtitleLanguage} defaultValue={effective.subtitleLanguage} placeholder={translate("settings.fields.languageCodePlaceholder")} emptyLabel={emptyLabel} onChange={(value) => change("subtitleLanguage", value)} />
        <TextSetting label={translate("settings.forcedSubtitleLanguage")} value={values.forcedSubtitleLanguage} defaultValue={effective.forcedSubtitleLanguage} placeholder={emptyLabel} emptyLabel={emptyLabel} list="forced-subtitle-languages" description={translate("settings.forcedSubtitleDescription")} onChange={(value) => change("forcedSubtitleLanguage", value)}>
          <datalist id="forced-subtitle-languages"><option value="off">{translate("settings.forcedSubtitleOff")}</option><option value="en">{translate("languages.english")}</option><option value="fr">Français</option><option value="es">Español</option><option value="de">Deutsch</option><option value="it">Italiano</option><option value="pt">Português</option><option value="ja">日本語</option></datalist>
        </TextSetting>
        <RangeSetting label={translate("settings.fields.subtitleSize")} value={values.subtitleSizePercent} defaultValue={effective.subtitleSizePercent} min={50} max={200} step={1} suffix="%" emptyLabel={emptyLabel} onChange={(value) => change("subtitleSizePercent", value)} />
        <ColorSetting value={values.subtitleTextColor} defaultValue={effective.subtitleTextColor} emptyLabel={emptyLabel} onChange={(value) => change("subtitleTextColor", value)} />
        <RangeSetting label={translate("settings.fields.subtitleBackgroundOpacity")} value={values.subtitleBackgroundOpacityPercent} defaultValue={effective.subtitleBackgroundOpacityPercent} min={0} max={100} step={1} suffix="%" emptyLabel={emptyLabel} onChange={(value) => change("subtitleBackgroundOpacityPercent", value)} />
      </SettingsGroup>

      <SettingsGroup icon={<Bell />} title={translate("settings.groups.deviceNotifications.title")} description={translate("settings.groups.deviceNotifications.description")} className="settings-group--wide settings-group--advanced" status={translate("common.labels.advanced")}>
        <InheritedToggle label={translate("settings.fields.sessionNotifications")} description={translate("settings.fields.sessionNotificationsDescription")} value={values.notificationsEnabled} defaultValue={effective.notificationsEnabled} onChange={(value) => change("notificationsEnabled", value)} emptyLabel={emptyLabel} />
        <RangeSetting label={translate("settings.fields.notificationDuration")} value={values.notificationDurationSeconds} defaultValue={effective.notificationDurationSeconds} min={2} max={30} step={1} suffix={translate("settings.units.secondsSuffix")} emptyLabel={emptyLabel} onChange={(value) => change("notificationDurationSeconds", value)} />
        <RangeSetting label={translate("settings.fields.notificationPollingInterval")} value={values.notificationPollIntervalSeconds} defaultValue={effective.notificationPollIntervalSeconds} min={5} max={300} step={1} suffix={translate("settings.units.secondsSuffix")} emptyLabel={emptyLabel} onChange={(value) => change("notificationPollIntervalSeconds", value)} />
      </SettingsGroup>
    </div>
  </section>;
}

function SettingsGroup({ icon, iconClassName = "", title, description, status, statusTone = "", className = "", children }: { icon: React.ReactNode; iconClassName?: string; title: string; description: string; status?: string; statusTone?: "connected" | "disconnected" | "unavailable" | ""; className?: string; children: React.ReactNode }) {
  return <section className={`settings-group ${className}`}>
    <div className="settings-group__heading"><span className={iconClassName}>{icon}</span><div><div className="settings-group__title"><h4>{title}</h4>{status && <span className={`settings-group__status ${statusTone ? `is-${statusTone}` : ""}`}>{status}</span>}</div><p>{description}</p></div></div>
    <div className="settings-group__grid">{children}</div>
  </section>;
}

function InheritedToggle({ label, description, value, defaultValue, onChange, emptyLabel }: { label: string; description: string; value: boolean | null | undefined; defaultValue: boolean; onChange: (value: boolean | null) => void; emptyLabel: string }) {
  const inherited = value === null || value === undefined;
  const shown = value ?? defaultValue;
  return <div className="setting-control setting-control--toggle">
    <label className="toggle-field"><input type="checkbox" checked={shown} onChange={(event) => onChange(event.target.checked)} /><span><i /><div><strong>{label}</strong><small>{description}</small><em className={`setting-value-state ${inherited ? "is-inherited" : "is-override"}`}>{inherited ? translate("settings.value.inheritedBoolean", { source: emptyLabel, value: translate(shown ? "common.status.on" : "common.status.off") }) : translate("settings.value.overrideBoolean", { value: translate(shown ? "common.status.on" : "common.status.off") })}</em></div></span></label>
  </div>;
}

function RangeSetting({ label, value, defaultValue, min, max, step, suffix, emptyLabel, onChange }: { label: string; value: number | null | undefined; defaultValue: number; min: number; max: number; step: number; suffix: string; emptyLabel: string; onChange: (value: number | null) => void }) {
  const inherited = value === null || value === undefined;
  const shown = value ?? defaultValue;
  return <div className="setting-control setting-control--range">
    <label className="field"><span>{label}</span><div><input type="range" min={min} max={max} step={step} value={shown} onChange={(event) => onChange(Number(event.target.value))} /><output>{shown}{suffix}</output></div><small className={`setting-value-state ${inherited ? "is-inherited" : "is-override"}`}>{inherited ? translate("settings.value.inheritedNumber", { source: emptyLabel, value: shown, suffix }) : translate("settings.value.overrideNumber", { value: shown, suffix })}</small></label>
  </div>;
}

function ColorSetting({ value, defaultValue, emptyLabel, onChange }: { value: string | null | undefined; defaultValue: string; emptyLabel: string; onChange: (value: string | null) => void }) {
  const inherited = value === null || value === undefined;
  const shown = value ?? defaultValue;
  return <div className="setting-control setting-control--color">
    <label className="field"><span>{translate("settings.fields.subtitleTextColor")}</span><div><input type="color" value={shown} onChange={(event) => onChange(event.target.value.toUpperCase())} /><output>{shown}</output></div><small className={`setting-value-state ${inherited ? "is-inherited" : "is-override"}`}>{inherited ? translate("settings.value.inheritedText", { source: emptyLabel, value: shown }) : translate("settings.value.overrideText", { value: shown })}</small></label>
  </div>;
}

function SelectSetting({ name, label, description, value, defaultValue, options, emptyLabel, onChange }: { name?: string; label: string; description?: string; value: string | null | undefined; defaultValue: string; options: ReadonlyArray<SettingOption>; emptyLabel: string; onChange: (value: string | null) => void }) {
  const inherited = value === null || value === undefined;
  const shown = value ?? defaultValue;
  const shownOption = options.find((option) => option.value === shown);
  const shownLabel = shownOption ? ("labelKey" in shownOption ? translate(shownOption.labelKey) : shownOption.label) : shown;
  return <div className="setting-control">
    <label className="field"><span>{label}</span><div><select name={name} value={value ?? ""} onChange={(event) => onChange(event.target.value || null)}><option value="">{translate("settings.actions.useInherited", { source: emptyLabel.toLowerCase() })}</option>{options.map((option) => <option key={option.value} value={option.value}>{"labelKey" in option ? translate(option.labelKey) : option.label}</option>)}</select></div>{description && <small>{description}</small>}<small className={`setting-value-state ${inherited ? "is-inherited" : "is-override"}`}>{inherited ? translate("settings.value.inheritedText", { source: emptyLabel, value: shownLabel }) : translate("settings.value.overrideText", { value: shownLabel })}</small></label>
  </div>;
}

function TextSetting({ label, value, defaultValue, placeholder, emptyLabel, list, description, onChange, children }: { label: string; value: string | null | undefined; defaultValue: string; placeholder: string; emptyLabel: string; list?: string; description?: string; onChange: (value: string | null) => void; children?: React.ReactNode }) {
  const inherited = value === null || value === undefined;
  const shown = value ?? defaultValue;
  return <div className="setting-control">
    <label className="field"><span>{label}</span><div><input list={list} value={value ?? ""} onChange={(event) => onChange(event.target.value || null)} placeholder={inherited ? shown : placeholder} />{children}</div>{description && <small>{description}</small>}<small className={`setting-value-state ${inherited ? "is-inherited" : "is-override"}`}>{inherited ? translate("settings.value.inheritedText", { source: emptyLabel, value: shown }) : translate("settings.value.overrideText", { value: shown })}</small></label>
  </div>;
}
