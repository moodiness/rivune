import { Activity, Bell, Boxes, Captions, Check, ChevronDown, ChevronUp, CircleStop, CircleUserRound, Clock3, Copy, Cpu, Database, ExternalLink, Eye, EyeOff, Film, GripVertical, HardDrive, ImagePlus, KeyRound, Languages, Layers3, Link, LoaderCircle, MonitorSmartphone, Palette, Pencil, Plus, Radio, RefreshCw, Save, Search, Send, Server, Settings2, Shield, Sparkles, Trash2, Upload, Users, WandSparkles, Wrench, X } from "lucide-react";
import { useCallback, useEffect, useRef, useState, type ChangeEvent, type CSSProperties, type FormEvent } from "react";
import { api, APIError } from "../api";
import { useAuth } from "../auth";
import { AddTile, Button, ConfirmDialog, EmptyState, handleDirectionalFocus, IconButton, Modal, Notice, Select, Skeleton } from "../components";
import { interfaceLanguages, locale, translate, type TranslationKey } from "../i18n";
import { notifyError, notifyErrorMessage, notifySuccess, notifyWarning } from "../notifications";
import { acquireOneShotNavigationGuard } from "../oneShotNavigationGuard";
import { TITLE_ID_PROVIDERS, titleProviderURL } from "../titleProviders";
import { integrationCredentialNames, type AccessCategory, type AddonDiagnostic, type AddonDiagnosticErrorCode, type AddonDiagnosticState, type AddonDiagnosticsResponse, type AddonManifest, type AddonPreviewResponse, type AvatarPreset, type CategoryInput, type Collection, type CollectionFolder, type CollectionSaveInput, type CollectionSource, type ConfigurationAuditPage, type DeviceUpdateInput, type HardwareAccelerationMode, type InstallAddonInput, type InstalledAddon, type IntegrationCredentialName, type InterfaceLanguage, type JellyfinCredentialSecret, type JellyfinCredentialStatus, type MaintenanceSettings, type ManagedAddon, type ManagedDevice, type MetadataRefreshScheduleInput, type OperationAction, type OperationRun, type OperationsOverview, type PlaybackActivity, type PlaybackActivitySession, type Profile, type ProfileSession, type RuntimeApplication, type RuntimeSettingsValues, type SettingsIntegrations, type SettingsIntegrationsPatch, type SettingsValues, type TrackingDeviceAuthorization, type TrackingProvider, type TrackingStatus, type UpdateAddonInput } from "../types";

type AdminTab = "categories" | "profiles" | "devices" | "addons" | "collections" | "activity" | "operations" | "settings";
type AdminTabGroup = "access" | "catalog" | "supervision" | "preferences";

const adminTabGroups: Array<{ id: AdminTabGroup; labelKey: TranslationKey }> = [
  { id: "access", labelKey: "admin.tabs.groups.access" },
  { id: "catalog", labelKey: "admin.tabs.groups.catalog" },
  { id: "supervision", labelKey: "admin.tabs.groups.supervision" },
  { id: "preferences", labelKey: "admin.tabs.groups.preferences" },
];

const tabs: Array<{ id: AdminTab; group: AdminTabGroup; labelKey: TranslationKey; descriptionKey: TranslationKey; icon: typeof Users; globalOnly?: boolean }> = [
  { id: "categories", group: "access", labelKey: "admin.categories.tab.label", descriptionKey: "admin.categories.tab.description", icon: Palette, globalOnly: true },
  { id: "profiles", group: "access", labelKey: "admin.tabs.profiles.label", descriptionKey: "admin.tabs.profiles.description", icon: Users },
  { id: "devices", group: "access", labelKey: "admin.devices.tab.label", descriptionKey: "admin.devices.tab.description", icon: MonitorSmartphone, globalOnly: true },
  { id: "addons", group: "catalog", labelKey: "admin.tabs.addons.label", descriptionKey: "admin.tabs.addons.description", icon: Boxes, globalOnly: true },
  { id: "collections", group: "catalog", labelKey: "admin.tabs.collections.label", descriptionKey: "admin.tabs.collections.description", icon: Layers3, globalOnly: true },
  { id: "activity", group: "supervision", labelKey: "admin.tabs.activity.label", descriptionKey: "admin.tabs.activity.description", icon: Activity, globalOnly: true },
  { id: "operations", group: "supervision", labelKey: "admin.tabs.operations.label", descriptionKey: "admin.tabs.operations.description", icon: Wrench, globalOnly: true },
  { id: "settings", group: "preferences", labelKey: "admin.tabs.settings.label", descriptionKey: "admin.tabs.settings.description", icon: Settings2 },
];

type SettingsSection = "appearance" | "playback" | "runtime" | "transcoding" | "language" | "subtitles" | "connections" | "integrations" | "audit";

const settingsSections: Record<SettingsSection, true> = { appearance: true, playback: true, runtime: true, transcoding: true, language: true, subtitles: true, connections: true, integrations: true, audit: true };
const serverOnlySettingsSections: Partial<Record<SettingsSection, true>> = { runtime: true, transcoding: true, integrations: true, audit: true };

function adminRouteParameters() {
  const [route, query = ""] = window.location.hash.slice(1).split("?", 2);
  return route === "admin" ? new URLSearchParams(query) : new URLSearchParams();
}

function requestedAdminTab() {
  const requested = adminRouteParameters().get("tab");
  return tabs.some((item) => item.id === requested) ? requested as AdminTab : null;
}

function requestedSettingsSection() {
  const requested = adminRouteParameters().get("section");
  return requested !== null && settingsSections[requested as SettingsSection] === true ? requested as SettingsSection : null;
}

function updateAdminRoute(tab: AdminTab, section?: SettingsSection, replace = false) {
  const parameters = new URLSearchParams();
  parameters.set("tab", tab);
  if (tab === "settings" && section) parameters.set("section", section);
  const url = `/#admin?${parameters.toString()}`;
  if (`${window.location.pathname}${window.location.hash}` === url) return;
  window.history[replace ? "replaceState" : "pushState"]({ ...window.history.state, rivuneView: "admin" }, "", url);
}

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
  const canManageGlobally = canManage && account?.session.authorizationScope === "global_admin";
  const visibleTabs = canManage ? tabs.filter((item) => !item.globalOnly || canManageGlobally) : tabs.filter((item) => item.id === "settings");
  const fallbackTab: AdminTab = canManage ? "profiles" : "settings";
  const [tab, setTab] = useState<AdminTab>(() => {
    const requested = requestedAdminTab();
    return requested && visibleTabs.some((item) => item.id === requested) ? requested : fallbackTab;
  });
  const selectedTab = visibleTabs.some((item) => item.id === tab) ? tab : fallbackTab;

  useEffect(() => {
    const requested = requestedAdminTab();
    const requestedIsVisible = requested !== null && visibleTabs.some((item) => item.id === requested);
    if (!visibleTabs.some((item) => item.id === tab)) setTab(fallbackTab);
    if (requested && !requestedIsVisible) updateAdminRoute(fallbackTab, undefined, true);
  }, [canManage, canManageGlobally, fallbackTab, tab]);

  useEffect(() => {
    const syncRoute = () => {
      const requested = requestedAdminTab();
      if (requested && visibleTabs.some((item) => item.id === requested)) {
        setTab(requested);
        return;
      }
      if (requested) {
        setTab(fallbackTab);
        updateAdminRoute(fallbackTab, undefined, true);
      }
    };
    window.addEventListener("hashchange", syncRoute);
    window.addEventListener("popstate", syncRoute);
    return () => {
      window.removeEventListener("hashchange", syncRoute);
      window.removeEventListener("popstate", syncRoute);
    };
  }, [canManage, canManageGlobally, fallbackTab]);

  function navigateTab(next: AdminTab) {
    setTab(next);
    updateAdminRoute(next, next === "settings" ? requestedSettingsSection() ?? undefined : undefined);
  }


  return <div className="standard-page admin-page page-enter">
    <header className="admin-page__header">
      <div className="admin-page__heading">
        <span>{translate(canManage ? "admin.header.eyebrowServer" : "admin.header.eyebrowProfile")}</span>
        <h1>{translate(canManage ? "admin.header.titleServer" : "admin.header.titleProfile")}</h1>
        <p>{translate(canManage ? "admin.header.descriptionServer" : "admin.header.descriptionProfile")}</p>
      </div>
      <div className="admin-page__context" aria-label={translate("admin.header.workspaceAccessLabel")}>
        <span><Server size={16} aria-hidden="true" /> {translate(canManage ? "admin.header.workspaceServer" : "admin.header.workspacePersonal")}</span>
        <span><Shield size={16} aria-hidden="true" /> {canManageGlobally ? translate("admin.workspace.accessGlobal") : translate(canManage ? "admin.header.accessManager" : "admin.header.accessProfile")}</span>
      </div>
    </header>
    <div className={`admin-layout ${canManage ? "" : "admin-layout--preferences"}`}>
      {canManage && <nav className="admin-tabs" aria-label={translate("admin.tabs.navigationLabel")} onKeyDown={(event) => {
        const horizontal = getComputedStyle(event.currentTarget).gridAutoFlow === "column";
        handleDirectionalFocus(event, { orientation: horizontal ? "horizontal" : "vertical", wrap: true });
      }}>{adminTabGroups.map((group) => {
        const groupTabs = visibleTabs.filter((item) => item.group === group.id);
        if (groupTabs.length === 0) return null;
        return <div className="admin-tabs__group" data-admin-tab-group={group.id} key={group.id}>
          <span className="admin-tabs__label">{translate(group.labelKey)}</span>
          <div className="admin-tabs__items">{groupTabs.map((item) => {
            const Icon = item.icon;
            const label = translate(item.labelKey);
            const description = translate(item.descriptionKey);
            return <button type="button" data-admin-tab={item.id} aria-current={selectedTab === item.id ? "page" : undefined} key={item.id} className={selectedTab === item.id ? "is-active" : ""} onClick={() => navigateTab(item.id)}><span><Icon size={20} aria-hidden="true" /></span><div><strong>{label}</strong><small>{description}</small></div><ChevronDown size={17} aria-hidden="true" /></button>;
          })}</div>
        </div>;
      })}</nav>}
      <section className="admin-panel">
        {selectedTab === "categories" ? <CategoriesAdmin />
          : selectedTab === "profiles" ? <ProfilesAdmin />
            : selectedTab === "devices" ? <DevicesAdmin />
              : selectedTab === "addons" ? <AddonsAdmin />
                : selectedTab === "collections" ? <CollectionsAdmin />
                  : selectedTab === "activity" ? <ActivityAdmin />
                    : selectedTab === "operations" ? <OperationsAdmin />
                      : <SettingsAdmin key={activeProfile?.id} />}
      </section>
    </div>
  </div>;
}
function CategoryBadge({ category }: { category: Profile["category"] | ManagedDevice["category"] }) {
  return <span
    className="category-badge"
    aria-label={translate("admin.categories.badge.label", { name: category.name })}
    style={category.color ? { "--category-color": category.color } as React.CSSProperties : undefined}
  >
    <i aria-hidden="true" />
    {category.icon && <span aria-hidden="true">{category.icon}</span>}
    <strong>{category.name}</strong>
  </span>;
}

function CategoryFilter({ categories, value, onChange, disabled = false }: { categories: AccessCategory[]; value: string; onChange: (value: string) => void; disabled?: boolean }) {
  return <label className="field category-filter">
    <span>{translate("admin.categories.filter.label")}</span>
    <div><Layers3 size={17} aria-hidden="true" /><Select value={value} disabled={disabled} onChange={onChange} options={[{ value: "all", label: translate("admin.categories.filter.all") }, ...categories.map((category) => ({ value: category.id, label: category.name }))]} /></div>
  </label>;
}

function CategoriesAdmin() {
  const { refreshAccount } = useAuth();
  const [categories, setCategories] = useState<AccessCategory[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [editor, setEditor] = useState<AccessCategory | "new" | null>(null);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [color, setColor] = useState("");
  const [icon, setIcon] = useState("");
  const [saving, setSaving] = useState(false);
  const [editorError, setEditorError] = useState("");
  const [reordering, setReordering] = useState(false);
  const [deleting, setDeleting] = useState<AccessCategory | null>(null);
  const [reassignTo, setReassignTo] = useState("");
  const [deleteNeedsReassignment, setDeleteNeedsReassignment] = useState(false);
  const [deleteError, setDeleteError] = useState("");
  const [deleteSaving, setDeleteSaving] = useState(false);
  const [defaultSavingId, setDefaultSavingId] = useState<string | null>(null);

  const loadCategories = useCallback(async () => {
    setLoading(true);
    setLoadError("");
    try {
      setCategories(await api.categories());
    } catch (cause) {
      setLoadError(notifyError(cause, translate("admin.categories.errors.load")));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void loadCategories(); }, [loadCategories]);

  function openEditor(category: AccessCategory | "new") {
    setEditor(category);
    setName(category === "new" ? "" : category.name);
    setDescription(category === "new" ? "" : category.description ?? "");
    setColor(category === "new" ? "" : category.color ?? "");
    setIcon(category === "new" ? "" : category.icon ?? "");
    setEditorError("");
  }

  async function saveCategory(event: FormEvent) {
    event.preventDefault();
    if (!editor || saving) return;
    const trimmedName = name.trim();
    const duplicate = categories.some((category) => category.id !== (editor === "new" ? "" : editor.id) && category.name.trim().toLocaleLowerCase() === trimmedName.toLocaleLowerCase());
    if (duplicate) {
      setEditorError(translate("admin.categories.errors.conflict"));
      return;
    }
    const input: CategoryInput = {
      name: trimmedName,
      description: description.trim() || null,
      color: color.trim() || null,
      icon: icon.trim() || null,
    };
    setSaving(true);
    setEditorError("");
    let saved: AccessCategory;
    try {
      saved = editor === "new" ? await api.createCategory(input) : await api.updateCategory(editor.id, input);
    } catch (cause) {
      const message = cause instanceof APIError && cause.status === 409 ? translate("admin.categories.errors.conflict") : notifyError(cause, translate("admin.categories.errors.save"));
      setEditorError(message);
      setSaving(false);
      return;
    }

    setCategories((current) => editor === "new" ? [...current, saved] : current.map((category) => category.id === saved.id ? saved : category));
    const message = translate(editor === "new" ? "admin.categories.notifications.created" : "admin.categories.notifications.saved", { name: saved.name });
    notifySuccess(message);
    setEditor(null);
    setSaving(false);
    void refreshAccount();
  }

  async function moveCategory(index: number, direction: -1 | 1) {
    const destination = index + direction;
    if (reordering || destination < 0 || destination >= categories.length) return;
    const reordered = [...categories];
    [reordered[index], reordered[destination]] = [reordered[destination]!, reordered[index]!];
    setReordering(true);
    setLoadError("");
    try {
      setCategories(await api.reorderCategories(reordered.map((category) => category.id)));
      notifySuccess(translate("admin.categories.notifications.reordered"));
    } catch (cause) {
      setLoadError(notifyError(cause, translate("admin.categories.errors.reorder")));
    } finally {
      setReordering(false);
    }
  }
  async function makeDefault(category: AccessCategory) {
    if (category.isDefault || defaultSavingId !== null) return;
    setDefaultSavingId(category.id);
    setLoadError("");
    try {
      const saved = await api.updateCategory(category.id, { isDefault: true });
      setCategories((current) => current.map((candidate) =>
        candidate.id === saved.id ? saved : { ...candidate, isDefault: false },
      ));
      const message = translate("admin.categories.notifications.saved", { name: saved.name });
      notifySuccess(message);
    } catch (cause) {
      setLoadError(notifyError(cause, translate("admin.categories.errors.save")));
    } finally {
      setDefaultSavingId(null);
    }
  }


  function openDelete(category: AccessCategory) {
    const destination = categories.find((candidate) => candidate.id !== category.id);
    setDeleting(category);
    setReassignTo(destination?.id ?? "");
    setDeleteError("");
    setDeleteNeedsReassignment(false);
  }

  async function deleteCategory() {
    if (!deleting || deleteSaving) return;
    const hasAssignments = deleting.profileCount + deleting.deviceCount > 0 || deleteNeedsReassignment;
    const validDestination = categories.some((category) => category.id === reassignTo && category.id !== deleting.id);
    if (hasAssignments && !validDestination) {
      setDeleteError(translate("admin.categories.delete.destination"));
      return;
    }
    setDeleteSaving(true);
    setDeleteError("");
    try {
      await api.deleteCategory(deleting.id, hasAssignments ? reassignTo : undefined);
      const message = translate("admin.categories.notifications.deleted", { name: deleting.name });
      setCategories((current) => current.filter((category) => category.id !== deleting.id));
      await refreshAccount();
      setDeleting(null);
      notifySuccess(message);
      void loadCategories();
    } catch (cause) {
      if (cause instanceof APIError && cause.status === 409 && cause.code === "category_reassignment_required") {
        setDeleteNeedsReassignment(true);
      }
      setDeleteError(notifyError(cause, translate("admin.categories.errors.delete")));
    } finally {
      setDeleteSaving(false);
    }
  }

  return <div className="admin-section categories-admin">
    <div className="admin-section__header">
      <div><span>{translate("admin.categories.eyebrow")}</span><h2>{translate("admin.categories.title")}</h2><p>{translate("admin.categories.description")}</p></div>
      <div className="admin-section__actions"><Button onClick={() => openEditor("new")}><Plus size={18} /> {translate("admin.categories.actions.new")}</Button></div>
    </div>
    {loadError && <Notice>{loadError}</Notice>}
    {loading ? <div className="admin-loading-state" aria-live="polite"><LoaderCircle size={28} className="spin" /><strong>{translate("admin.categories.loading.title")}</strong><span>{translate("admin.categories.loading.description")}</span></div>
      : categories.length === 0 ? <EmptyState icon={<Layers3 size={44} />} title={translate("admin.categories.empty.title")} description={translate("admin.categories.empty.description")} action={<Button onClick={() => openEditor("new")}><Plus size={18} /> {translate("admin.categories.actions.create")}</Button>} />
        : <div className="category-list">{categories.map((category, index) => <article key={category.id} className="category-card" style={category.color ? { "--category-color": category.color } as React.CSSProperties : undefined}>
          <div className="category-card__order" aria-label={`${index + 1} / ${categories.length}`}>
            <IconButton type="button" label={translate("admin.categories.actions.moveUp", { name: category.name })} disabled={reordering || index === 0} onClick={() => void moveCategory(index, -1)}><ChevronUp size={17} /></IconButton>
            <strong>{index + 1}</strong>
            <IconButton type="button" label={translate("admin.categories.actions.moveDown", { name: category.name })} disabled={reordering || index === categories.length - 1} onClick={() => void moveCategory(index, 1)}><ChevronDown size={17} /></IconButton>
          </div>
          <span className="category-card__mark" aria-hidden="true">{category.icon ? <small>{category.icon}</small> : <Layers3 size={22} />}</span>
          <div className="category-card__copy">
            <div><h3>{category.name}</h3>{category.isDefault && <span className="category-badge category-badge--default">{translate("admin.categories.badge.default")}</span>}</div>
            {category.description && <p>{category.description}</p>}
            <ul><li><Users size={14} /> {translate("admin.categories.count.profiles", { count: category.profileCount })}</li><li><MonitorSmartphone size={14} /> {translate("admin.categories.count.devices", { count: category.deviceCount })}</li></ul>
          </div>
          <div className="category-card__actions">
            {!category.isDefault && <Button variant="ghost" loading={defaultSavingId === category.id} disabled={defaultSavingId !== null} onClick={() => void makeDefault(category)}><Check size={16} /> {translate("admin.categories.badge.default")}</Button>}
            <Button variant="secondary" onClick={() => openEditor(category)}><Pencil size={16} /> {translate("common.actions.edit")}</Button>
            {!category.isDefault && categories.length > 1 && <Button variant="ghost" className="admin-destructive-action" onClick={() => openDelete(category)}><Trash2 size={16} /> {translate("common.actions.delete")}</Button>}
          </div>
        </article>)}</div>}
    {editor && <Modal onClose={() => { if (!saving) setEditor(null); }} className="editor-modal category-editor">
      <div className="editor-modal__heading">
        <span><Layers3 size={18} /> {translate(editor === "new" ? "admin.categories.editor.newEyebrow" : "admin.categories.editor.editEyebrow")}</span>
        <h2>{translate(editor === "new" ? "admin.categories.editor.createTitle" : "admin.categories.editor.editTitle", editor === "new" ? undefined : { name: editor.name })}</h2>
      </div>
      <form className="form-stack" onSubmit={saveCategory}>
        {editorError && <Notice>{editorError}</Notice>}
        <label className="field"><span>{translate("admin.categories.fields.name")}</span><div><Layers3 size={18} /><input autoFocus required maxLength={80} value={name} disabled={saving} onChange={(event) => setName(event.target.value)} /></div></label>
        <label className="field"><span>{translate("admin.categories.fields.description")}</span><textarea rows={4} maxLength={500} value={description} disabled={saving} placeholder={translate("admin.categories.fields.descriptionPlaceholder")} onChange={(event) => setDescription(event.target.value)} /></label>
        <div className="form-grid form-grid--two">
          <label className="field"><span>{translate("admin.categories.fields.color")}</span><div className="category-color-field"><Palette size={18} /><input type="color" aria-label={translate("admin.categories.fields.color")} value={/^#[0-9A-Fa-f]{6}$/.test(color) ? color : "#F29A78"} disabled={saving} onChange={(event) => setColor(event.target.value.toUpperCase())} /><input aria-label="HEX" value={color} disabled={saving} pattern="^#[0-9A-Fa-f]{6}$" placeholder="#F29A78" onChange={(event) => setColor(event.target.value.toUpperCase())} /></div></label>
          <label className="field"><span>{translate("admin.categories.fields.icon")}</span><div><Sparkles size={18} /><input value={icon} disabled={saving} pattern="^[a-z0-9]+(?:-[a-z0-9]+)*$" placeholder={translate("admin.categories.fields.iconPlaceholder")} onChange={(event) => setIcon(event.target.value)} /></div></label>
        </div>
        <div className="modal-actions modal-actions--sticky"><Button type="button" variant="ghost" disabled={saving} onClick={() => setEditor(null)}>{translate("common.cancel")}</Button><Button type="submit" loading={saving} disabled={!name.trim()}><Save size={18} /> {translate(editor === "new" ? "admin.categories.actions.create" : "admin.categories.actions.save")}</Button></div>
      </form>
    </Modal>}
    {deleting && <Modal onClose={() => { if (!deleteSaving) setDeleting(null); }} className="editor-modal category-delete-modal">
      <div className="editor-modal__heading">
        <span><Trash2 size={18} /> {translate("admin.categories.delete.eyebrow")}</span>
        <h2>{translate("admin.categories.delete.title", { name: deleting.name })}</h2>
        {!deleteNeedsReassignment && <p>{deleting.profileCount + deleting.deviceCount > 0 ? translate("admin.categories.delete.reassignDescription", { profiles: deleting.profileCount, devices: deleting.deviceCount }) : translate("admin.categories.delete.emptyDescription")}</p>}
      </div>
      <div className="form-stack">
        {deleteError && <Notice>{deleteError}</Notice>}
        {(deleting.profileCount + deleting.deviceCount > 0 || deleteNeedsReassignment) && <label className="field"><span>{translate("admin.categories.delete.destination")}</span><div><Layers3 size={18} /><Select autoFocus required disabled={deleteSaving} value={reassignTo} onChange={setReassignTo} options={categories.filter((category) => category.id !== deleting.id).map((category) => ({ value: category.id, label: category.name }))} /></div></label>}
        <div className="modal-actions"><Button type="button" variant="secondary" disabled={deleteSaving} onClick={() => setDeleting(null)}>{translate("common.cancel")}</Button><Button type="button" variant="danger" loading={deleteSaving} disabled={(deleting.profileCount + deleting.deviceCount > 0 || deleteNeedsReassignment) && !reassignTo} onClick={() => void deleteCategory()}>{translate(deleting.profileCount + deleting.deviceCount > 0 || deleteNeedsReassignment ? "admin.categories.delete.confirm" : "admin.categories.delete.confirmEmpty")}</Button></div>
      </div>
    </Modal>}
  </div>;
}

function DevicesAdmin() {
  const [categories, setCategories] = useState<AccessCategory[]>([]);
  const [devices, setDevices] = useState<ManagedDevice[]>([]);
  const [filter, setFilter] = useState("all");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [selected, setSelected] = useState<Set<string>>(() => new Set());
  const [editing, setEditing] = useState<ManagedDevice | null>(null);
  const [deviceName, setDeviceName] = useState("");
  const [internalNote, setInternalNote] = useState("");
  const [saving, setSaving] = useState(false);
  const [moving, setMoving] = useState<ManagedDevice[]>([]);
  const [moveCategoryId, setMoveCategoryId] = useState("");
  const [moveSaving, setMoveSaving] = useState(false);
  const [deleting, setDeleting] = useState<ManagedDevice | null>(null);
  const [deleteSaving, setDeleteSaving] = useState(false);
  const deviceLoadGeneration = useRef(0);

  const loadDevices = useCallback(async (categoryId: string) => {
    const generation = ++deviceLoadGeneration.current;
    setLoading(true);
    setError("");
    try {
      const next = await api.devices(categoryId === "all" ? undefined : categoryId);
      if (deviceLoadGeneration.current === generation) setDevices(next);
    } catch (cause) {
      if (deviceLoadGeneration.current === generation) setError(notifyError(cause, translate("admin.devices.errors.load")));
    } finally {
      if (deviceLoadGeneration.current === generation) setLoading(false);
    }
  }, []);

  useEffect(() => {
    void api.categories().then(setCategories).catch((cause) => setError(notifyError(cause, translate("admin.categories.errors.load"))));
  }, []);
  useEffect(() => {
    setSelected(new Set());
    void loadDevices(filter);
    return () => { deviceLoadGeneration.current += 1; };
  }, [filter, loadDevices]);

  function openEditor(device: ManagedDevice) {
    setEditing(device);
    setDeviceName(device.name);
    setInternalNote(device.internalNote ?? "");
    setError("");
  }

  async function saveDevice(event: FormEvent) {
    event.preventDefault();
    if (!editing || saving) return;
    const input: DeviceUpdateInput = { name: deviceName.trim(), internalNote: internalNote.trim() || null };
    setSaving(true);
    setError("");
    try {
      const saved = await api.updateDevice(editing.id, input);
      setDevices((current) => current.map((device) => device.id === saved.id ? saved : device));
      const message = translate("admin.devices.edit.success", { name: saved.name });
      setEditing(null);
      notifySuccess(message);
    } catch (cause) {
      setError(notifyError(cause, translate("admin.devices.edit.error")));
    } finally {
      setSaving(false);
    }
  }

  function openMove(targets: ManagedDevice[]) {
    if (!targets.length) return;
    const destination = categories.find((category) => targets.some((device) => device.categoryId !== category.id));
    setMoving(targets);
    setMoveCategoryId(destination?.id ?? "");
    setError("");
  }

  async function moveDevices() {
    if (!moving.length || !moveCategoryId || moveSaving) return;
    setMoveSaving(true);
    setError("");
    try {
      await api.moveDevicesToCategory(moving.map((device) => device.id), moveCategoryId);
      const message = translate(moving.length === 1 ? "admin.devices.move.successOne" : "admin.devices.move.successMany", moving.length === 1 ? { name: moving[0]!.name } : { count: moving.length });
      setMoving([]);
      setSelected(new Set());
      notifySuccess(message);
      await loadDevices(filter);
    } catch (cause) {
      setError(notifyError(cause, translate("admin.devices.move.error")));
    } finally {
      setMoveSaving(false);
    }
  }

  async function deleteDevice() {
    if (!deleting || deleteSaving) return;
    setDeleteSaving(true);
    setError("");
    try {
      await api.deleteDevice(deleting.id);
      setDevices((current) => current.filter((device) => device.id !== deleting.id));
      setSelected((current) => {
        if (!current.has(deleting.id)) return current;
        const next = new Set(current);
        next.delete(deleting.id);
        return next;
      });
      const message = translate("admin.devices.delete.success", { name: deleting.name });
      setDeleting(null);
      notifySuccess(message);
    } catch (cause) {
      setError(notifyError(cause, translate("admin.devices.delete.error")));
    } finally {
      setDeleteSaving(false);
    }
  }

  const selectedDevices = devices.filter((device) => selected.has(device.id));

  return <div className="admin-section devices-admin">
    <div className="admin-section__header">
      <div><span>{translate("admin.devices.eyebrow")}</span><h2>{translate("admin.devices.title")}</h2><p>{translate("admin.devices.description")}</p></div>
      <div className="admin-section__actions"><a className="button button--secondary" href="/pair"><Link size={16} /> {translate("pairing.approveDevice")}</a><CategoryFilter categories={categories} value={filter} onChange={setFilter} /></div>
    </div>
    {error && <Notice>{error}</Notice>}
    {selectedDevices.length > 0 && <div className="bulk-move-bar" role="status"><strong>{translate("admin.devices.bulk.selected", { count: selectedDevices.length })}</strong><Button variant="secondary" onClick={() => openMove(selectedDevices)}><Layers3 size={16} /> {translate("admin.devices.bulk.move")}</Button></div>}
    {loading ? <div className="admin-loading-state" aria-live="polite"><LoaderCircle size={28} className="spin" /><strong>{translate("admin.devices.loading.title")}</strong><span>{translate("admin.devices.loading.description")}</span></div>
      : devices.length === 0 ? <EmptyState icon={<MonitorSmartphone size={44} />} title={filter === "all" ? translate("admin.devices.empty.title") : translate("admin.devices.filter.emptyTitle")} description={filter === "all" ? translate("admin.devices.empty.description") : translate("admin.devices.filter.emptyDescription")} />
        : <div className="device-admin-list">{devices.map((device) => <article key={device.id} className="device-admin-card">
          <label className="admin-select-control"><input type="checkbox" checked={selected.has(device.id)} aria-label={translate("admin.devices.bulk.select", { name: device.name })} onChange={(event) => setSelected((current) => { const next = new Set(current); if (event.target.checked) next.add(device.id); else next.delete(device.id); return next; })} /><span /></label>
          <span className="device-admin-card__icon" aria-hidden="true"><MonitorSmartphone size={22} /></span>
          <div className="device-admin-card__copy"><div><h3>{device.name}</h3><CategoryBadge category={device.category} /></div><p>{device.platform} · {translate("admin.devices.approved", { date: device.approvedAt ? new Date(device.approvedAt).toLocaleString() : new Date(device.createdAt).toLocaleString() })} · {device.lastSeenAt ? translate("admin.devices.lastSeen", { date: new Date(device.lastSeenAt).toLocaleString() }) : translate("admin.devices.neverSeen")}</p>{device.internalNote && <small>{device.internalNote}</small>}</div>
          <div className="device-admin-card__actions"><Button variant="secondary" onClick={() => openEditor(device)}><Pencil size={16} /> {translate("common.actions.edit")}</Button><Button variant="ghost" disabled={categories.length < 2} onClick={() => openMove([device])}><Layers3 size={16} /> {translate("admin.devices.move.action")}</Button><Button variant="ghost" className="admin-destructive-action" onClick={() => { setError(""); setDeleting(device); }}><Trash2 size={16} /> {translate("common.actions.delete")}</Button></div>
        </article>)}</div>}
    {editing && <Modal onClose={() => { if (!saving) setEditing(null); }} className="editor-modal device-editor">
      <div className="editor-modal__heading"><span><MonitorSmartphone size={18} /> {translate("admin.devices.edit.eyebrow")}</span><h2>{translate("admin.devices.edit.title", { name: editing.name })}</h2><CategoryBadge category={editing.category} /></div>
      <form className="form-stack" onSubmit={saveDevice}>
        {error && <Notice>{error}</Notice>}
        <label className="field"><span>{translate("admin.devices.fields.name")}</span><div><MonitorSmartphone size={18} /><input autoFocus required maxLength={120} value={deviceName} disabled={saving} onChange={(event) => setDeviceName(event.target.value)} /></div></label>
        <label className="field"><span>{translate("admin.devices.fields.note")}</span><textarea rows={5} maxLength={500} value={internalNote} disabled={saving} placeholder={translate("admin.devices.fields.notePlaceholder")} onChange={(event) => setInternalNote(event.target.value)} /></label>
        <div className="modal-actions modal-actions--sticky"><Button type="button" variant="ghost" disabled={saving} onClick={() => setEditing(null)}>{translate("common.cancel")}</Button><Button type="submit" loading={saving} disabled={!deviceName.trim()}><Save size={18} /> {translate("admin.devices.edit.save")}</Button></div>
      </form>
    </Modal>}
    {moving.length > 0 && <Modal onClose={() => { if (!moveSaving) setMoving([]); }} className="editor-modal move-category-modal">
      <div className="editor-modal__heading"><span><Layers3 size={18} /> {translate("admin.devices.move.eyebrow")}</span><h2>{translate(moving.length === 1 ? "admin.devices.move.titleOne" : "admin.devices.move.titleMany", moving.length === 1 ? { name: moving[0]!.name } : { count: moving.length })}</h2><p>{translate("admin.devices.move.description")}</p></div>
      <div className="form-stack">
        {error && <Notice>{error}</Notice>}
        <label className="field"><span>{translate("admin.devices.move.destination")}</span><div><Layers3 size={18} /><Select autoFocus required value={moveCategoryId} disabled={moveSaving} onChange={setMoveCategoryId} options={[{ value: "", label: translate("admin.devices.move.destination"), disabled: true }, ...categories.filter((category) => moving.some((device) => device.categoryId !== category.id)).map((category) => ({ value: category.id, label: category.name }))]} /></div></label>
        <div className="modal-actions"><Button type="button" variant="secondary" disabled={moveSaving} onClick={() => setMoving([])}>{translate("common.cancel")}</Button><Button type="button" loading={moveSaving} disabled={!moveCategoryId} onClick={() => void moveDevices()}>{translate("admin.devices.move.confirm")}</Button></div>
      </div>
    </Modal>}
    {deleting && <ConfirmDialog title={translate("admin.devices.delete.title", { name: deleting.name })} description={translate("admin.devices.delete.description")} confirmLabel={translate("admin.devices.delete.confirm")} loading={deleteSaving} onCancel={() => setDeleting(null)} onConfirm={() => void deleteDevice()} />}
  </div>;
}


function useAdministrationProfiles() {
  const { account } = useAuth();
  const isGlobalAdmin = account?.session.authorizationScope === "global_admin";
  const [profiles, setProfiles] = useState<Profile[]>(account?.profiles ?? []);

  useEffect(() => {
    if (!isGlobalAdmin) setProfiles(account?.profiles ?? []);
  }, [account?.profiles, isGlobalAdmin]);

  useEffect(() => {
    if (!isGlobalAdmin) return;
    let current = true;
    void api.profiles()
      .then((response) => { if (current) setProfiles(response.profiles); })
      .catch(() => undefined);
    return () => { current = false; };
  }, [account?.user.id, isGlobalAdmin]);

  return profiles;
}


function ProfilesAdmin() {
  const { account, activeProfile, discovery, refreshAccount } = useAuth();
  const [profiles, setProfiles] = useState<Profile[]>(account?.profiles ?? []);
  const [presets, setPresets] = useState<AvatarPreset[]>([]);
  const [editing, setEditing] = useState<Profile | "new" | null>(null);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
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
  const isGlobalAdmin = account?.session.authorizationScope === "global_admin";
  const [categories, setCategories] = useState<AccessCategory[]>([]);
  const [categoriesLoading, setCategoriesLoading] = useState(isGlobalAdmin);
  const [categoryFilter, setCategoryFilter] = useState("all");
  const [categoryId, setCategoryId] = useState(account?.session.category?.id ?? "");
  const [selectedProfiles, setSelectedProfiles] = useState<Set<string>>(() => new Set());
  const [movingProfiles, setMovingProfiles] = useState<Profile[]>([]);
  const [moveCategoryId, setMoveCategoryId] = useState("");
  const [moveSaving, setMoveSaving] = useState(false);
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

  const [imagePreviewURL, setImagePreviewURL] = useState("");
  useEffect(() => {
    if (!image || !editing) {
      setImagePreviewURL("");
      return;
    }
    const url = URL.createObjectURL(image);
    setImagePreviewURL(url);
    return () => URL.revokeObjectURL(url);
  }, [editing, image]);

  useEffect(() => { void api.avatarPresets().then((response) => setPresets(response.presets)).catch(() => undefined); }, []);
  useEffect(() => { if (!isGlobalAdmin) setProfiles(account?.profiles ?? []); }, [account?.profiles, isGlobalAdmin]);
  useEffect(() => {
    if (!isGlobalAdmin) return;
    let current = true;
    void api.profiles()
      .then((response) => { if (current) setProfiles(response.profiles); })
      .catch(() => undefined);
    return () => { current = false; };
  }, [account?.user.id, isGlobalAdmin]);
  useEffect(() => {
    if (!isGlobalAdmin) {
      setCategories([]);
      setCategoriesLoading(false);
      setCategoryId(account?.session.category?.id ?? "");
      return;
    }
    setCategoriesLoading(true);
    void api.categories()
      .then((values) => {
        setCategories(values);
        setCategoryId((current) => current || values.find((category) => category.isDefault)?.id || values[0]?.id || "");
      })
      .catch((cause) => setError(notifyError(cause, translate("admin.categories.errors.load"))))
      .finally(() => setCategoriesLoading(false));
  }, [account?.session.category?.id, isGlobalAdmin]);

  function openEditor(profile: Profile | "new") {
    setEditing(profile);
    const defaultCategoryId = account?.session.category?.id ?? categories.find((category) => category.isDefault)?.id ?? categories[0]?.id ?? "";
    setCategoryId(profile === "new" ? defaultCategoryId : profile.categoryId);
    setName(profile === "new" ? "" : profile.name);
    setDescription(profile === "new" ? "" : profile.description ?? "");
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
    const creating = editing === "new";
    if (creating && !categoryId) {
      setError(translate("admin.profiles.editor.category"));
      return;
    }
    setSaving(true);
    setError("");
    try {
      let profile: Profile;
      if (creating) {
        profile = await api.createProfile({
          name, description: description.trim() || null, categoryId, isChild, pin: pin || undefined, enabled,
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
        profile = await api.updateProfile(editing.id, { name, description: description.trim() || null, isChild, ...(pin ? { pin } : {}), ...accessInput });
      }
      if (image) await api.uploadProfileAvatar(profile.id, image);
      else if (presetId !== profile.avatar.presetId) await api.setProfileAvatar(profile.id, presetId);
      const next = await api.profiles();
      setProfiles(next.profiles);
      if (isGlobalAdmin) void api.categories().then(setCategories).catch(() => undefined);
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
      if (isGlobalAdmin) void api.categories().then(setCategories).catch(() => undefined);
      setDeleting(null);
      await refreshAccount();
      notifySuccess(translate("admin.profiles.notifications.deletedMessage", { name: profile.name }), translate("admin.profiles.notifications.deletedTitle"));
    } catch (cause) {
      setError(notifyError(cause, translate("admin.profiles.errors.delete")));
    }
  }

  function openProfileMove(targets: Profile[]) {
    if (!targets.length) return;
    const destination = categories.find((category) => targets.some((profile) => profile.categoryId !== category.id));
    setMovingProfiles(targets);
    setMoveCategoryId(destination?.id ?? "");
    setError("");
  }

  async function moveProfiles() {
    if (!movingProfiles.length || !moveCategoryId || moveSaving) return;
    setMoveSaving(true);
    setError("");
    try {
      await api.moveProfilesToCategory(movingProfiles.map((profile) => profile.id), moveCategoryId);
      const message = translate(movingProfiles.length === 1 ? "admin.profiles.move.successOne" : "admin.profiles.move.successMany", movingProfiles.length === 1 ? { name: movingProfiles[0]!.name } : { count: movingProfiles.length });
      const next = await api.profiles();
      setProfiles(next.profiles);
      setCategories(await api.categories());
      await refreshAccount();
      setMovingProfiles([]);
      setSelectedProfiles(new Set());
      notifySuccess(message);
    } catch (cause) {
      setError(notifyError(cause, translate("admin.profiles.move.error")));
    } finally {
      setMoveSaving(false);
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
  const visibleProfiles = categoryFilter === "all" ? profiles : profiles.filter((profile) => profile.categoryId === categoryFilter);
  const enabledProfiles = profiles.filter((profile) => profile.enabled).length;
  const protectedProfiles = profiles.filter((profile) => profile.hasPin).length;
  const kidsProfiles = profiles.filter((profile) => profile.isChild).length;
  const selectedProfileValues = profiles.filter((profile) => selectedProfiles.has(profile.id));

  return <div className="admin-section profiles-admin">
    <div className="admin-section__header">
      <div><span>{translate("admin.profiles.eyebrow")}</span><h2>{translate("admin.profiles.title")}</h2><p>{translate("admin.profiles.description")}</p></div>
      <div className="admin-section__actions">
        {isGlobalAdmin && <Button variant="secondary" onClick={openBroadcast}><Radio size={18} /> {translate("admin.broadcast.open")}</Button>}
        <Button disabled={isGlobalAdmin && (categoriesLoading || categories.length === 0)} onClick={() => openEditor("new")}><Plus size={18} /> {translate("admin.profiles.actions.new")}</Button>
      </div>
    </div>
    <section className="admin-summary" aria-label={translate("admin.profiles.overview.label")}>
      <article><span><Users size={18} aria-hidden="true" /></span><div><strong>{profiles.length}</strong><small>{translate("admin.profiles.overview.total")}</small></div></article>
      <article><span><Check size={18} aria-hidden="true" /></span><div><strong>{enabledProfiles}</strong><small>{translate("admin.profiles.overview.enabled")}</small></div></article>
      <article><span><Shield size={18} aria-hidden="true" /></span><div><strong>{protectedProfiles}</strong><small>{translate("admin.profiles.status.pinProtected")}</small></div></article>
      <article><span><Sparkles size={18} aria-hidden="true" /></span><div><strong>{kidsProfiles}</strong><small>{translate("admin.profiles.overview.kids")}</small></div></article>
    </section>
    {isGlobalAdmin && <div className="category-workspace-toolbar">
      <CategoryFilter categories={categories} value={categoryFilter} disabled={categoriesLoading} onChange={(value) => { setCategoryFilter(value); setSelectedProfiles(new Set()); }} />
      {selectedProfileValues.length > 0 && <div className="bulk-move-bar" role="status"><strong>{translate("admin.profiles.bulk.selected", { count: selectedProfileValues.length })}</strong><Button variant="secondary" onClick={() => openProfileMove(selectedProfileValues)}><Layers3 size={16} /> {translate("admin.profiles.bulk.move")}</Button></div>}
    </div>}
    {error && <Notice>{error}</Notice>}
    {visibleProfiles.length ? <div className="profile-admin-grid">{visibleProfiles.map((profile) =>
      <article key={profile.id} className={`profile-admin-card ${isGlobalAdmin ? "profile-admin-card--selectable" : ""}`}>
        {isGlobalAdmin && <label className="admin-select-control profile-admin-card__select"><input type="checkbox" disabled={profile.id === activeProfile?.id} checked={selectedProfiles.has(profile.id)} aria-label={translate("admin.profiles.bulk.select", { name: profile.name })} onChange={(event) => setSelectedProfiles((current) => { const next = new Set(current); if (event.target.checked) next.add(profile.id); else next.delete(profile.id); return next; })} /><span /></label>}
        <div className="profile-admin-card__visual"><img src={profile.avatar.url} alt="" /><span className={profile.isChild ? "is-child" : ""}>{translate(profile.isChild ? "admin.profiles.roles.kids" : profile.canManage ? "admin.profiles.roles.manager" : "admin.profiles.roles.viewer")}</span></div>
        <div className="profile-admin-card__copy"><div><h3>{profile.name}</h3><CategoryBadge category={profile.category} /></div>{profile.description && <p className="profile-admin-card__description">{profile.description}</p>}<p className="profile-admin-card__status"><i className={`admin-status-dot ${profile.enabled ? "" : "is-disabled"}`} /> {translate(profile.enabled ? "common.status.enabled" : "common.status.disabled")} · {translate(profile.hasPin ? "admin.profiles.status.pinProtected" : "admin.profiles.status.noPin")}</p></div>
        <div className="profile-admin-card__actions">
          <Button variant="secondary" onClick={() => void openSessions(profile)}><MonitorSmartphone size={16} /> {translate("admin.profiles.actions.devices")}</Button>
          <Button variant="ghost" onClick={() => openEditor(profile)}><Pencil size={16} /> {translate("common.actions.edit")}</Button>
          {isGlobalAdmin && profile.id !== activeProfile?.id && <Button variant="ghost" disabled={categories.length < 2} onClick={() => openProfileMove([profile])}><Layers3 size={16} /> {translate("admin.profiles.move.action")}</Button>}
          {profile.id !== activeProfile?.id && <Button variant="ghost" className="admin-destructive-action" onClick={() => setDeleting(profile)}><Trash2 size={16} /> {translate("common.actions.delete")}</Button>}
        </div>
      </article>,
    )}</div> : <EmptyState icon={<Users size={44} />} title={profiles.length ? translate("admin.profiles.filter.emptyTitle") : translate("admin.profiles.empty.title")} description={profiles.length ? translate("admin.profiles.filter.emptyDescription") : translate("admin.profiles.empty.description")} action={!profiles.length || categoryFilter !== "all" ? <Button disabled={isGlobalAdmin && categories.length === 0} onClick={() => openEditor("new")}><Plus size={18} /> {translate("admin.profiles.actions.create")}</Button> : undefined} />}
    {editing && <Modal onClose={() => { if (!saving) setEditing(null); }} className="editor-modal profile-editor">
      <div className="editor-modal__heading">
        <span><CircleUserRound size={18} /> {translate(editing === "new" ? "admin.profiles.editor.newEyebrow" : "admin.profiles.editor.editEyebrow")}</span>
        <h2>{editing === "new" ? translate("admin.profiles.editor.createTitle") : translate("admin.profiles.editor.editTitle", { name: editing.name })}</h2>
      </div>
      <form onSubmit={submit} className="form-stack">
        {error && <Notice>{error}</Notice>}
        <div className="avatar-editor">
          <button type="button" disabled={saving} onClick={() => fileRef.current?.click()}>
            {imagePreviewURL ? <img src={imagePreviewURL} alt={translate("admin.profiles.editor.selectedAvatarAlt")} /> : <img src={presets.find((preset) => preset.id === presetId)?.url ?? "/api/v1/profile-avatars/aurora"} alt={translate("admin.profiles.editor.selectedAvatarAlt")} />}
            <span><Upload size={17} /> {translate("common.actions.upload")}</span>
          </button>
          <input ref={fileRef} hidden type="file" accept="image/png,image/jpeg,image/webp" onChange={(event) => setImage(event.target.files?.[0] ?? null)} />
          <div>{presets.map((preset) => <button type="button" disabled={saving} key={preset.id} className={presetId === preset.id && !image ? "is-active" : ""} aria-label={preset.name} onClick={() => { setPresetId(preset.id); setImage(null); }}><img src={preset.url} alt="" /></button>)}</div>
        </div>
        <label className="field"><span>{translate("admin.categories.fields.description")}</span><textarea rows={3} maxLength={500} value={description} disabled={saving} onChange={(event) => setDescription(event.target.value)} /></label>
        <div className="form-grid form-grid--two">
          <label className="field"><span>{translate("admin.profiles.editor.name")}</span><div><CircleUserRound size={18} /><input autoFocus value={name} disabled={saving} onChange={(event) => setName(event.target.value)} required /></div></label>
          <label className="field"><span>{translate("admin.profiles.editor.pin")}</span><div><Shield size={18} /><input type={showPin ? "text" : "password"} inputMode="numeric" disabled={saving} value={pin} onChange={(event) => setPin(event.target.value)} placeholder={translate(editing === "new" ? "admin.profiles.editor.pinCreatePlaceholder" : "admin.profiles.editor.pinEditPlaceholder")} /><IconButton type="button" disabled={saving} label={translate(showPin ? "admin.profiles.editor.hidePin" : "admin.profiles.editor.showPin")} onClick={() => setShowPin((value) => !value)}>{showPin ? <EyeOff size={17} /> : <Eye size={17} />}</IconButton></div></label>
          {editing === "new" && isGlobalAdmin && <label className="field"><span>{translate("admin.profiles.editor.category")}</span><div><Layers3 size={18} /><Select required value={categoryId} disabled={saving || categoriesLoading} onChange={setCategoryId} options={categories.map((category) => ({ value: category.id, label: category.name }))} /></div></label>}
          {editing === "new" && !isGlobalAdmin && account?.session.category && <div className="profile-editor__category"><span>{translate("admin.profiles.editor.category")}</span><CategoryBadge category={account.session.category} /></div>}
          <label className="toggle-field"><input type="checkbox" checked={enabled} disabled={saving} onChange={(event) => setEnabled(event.target.checked)} /><span><i /><div><strong>{translate("common.status.enabled")}</strong><small>{translate("admin.profiles.editor.enabledDescription")}</small></div></span></label>
          <label className="toggle-field"><input type="checkbox" checked={isChild} disabled={saving} onChange={(event) => setIsChild(event.target.checked)} /><span><i /><div><strong>{translate("admin.profiles.editor.kids")}</strong><small>{translate("admin.profiles.editor.kidsDescription")}</small></div></span></label>
        </div>
        <section className="profile-access-editor">
          <div><strong>{translate("admin.profiles.editor.availability")}</strong><p>{translate("admin.profiles.editor.availabilityDescription", { timezone: accessTimezone })}</p></div>
          <div className="form-grid form-grid--two">
            <label className="field"><span>{translate("admin.profiles.editor.availableFrom")}</span><div><input type="date" value={availableFrom} disabled={saving} max={availableUntil || undefined} onChange={(event) => setAvailableFrom(event.target.value)} /></div></label>
            <label className="field"><span>{translate("admin.profiles.editor.availableUntil")}</span><div><input type="date" value={availableUntil} disabled={saving} min={availableFrom || undefined} onChange={(event) => setAvailableUntil(event.target.value)} /></div></label>
          </div>
          <label className="toggle-field"><input type="checkbox" checked={dailyHours} disabled={saving} onChange={(event) => setDailyHours(event.target.checked)} /><span><i /><div><strong>{translate("admin.profiles.editor.dailyHours")}</strong><small>{translate("admin.profiles.editor.dailyHoursDescription")}</small></div></span></label>
          {dailyHours && <><div className="form-grid form-grid--two">
            <label className="field"><span>{translate("admin.profiles.editor.accessStart")}</span><div><input type="time" value={accessStartTime} disabled={saving} onChange={(event) => setAccessStartTime(event.target.value)} required /></div></label>
            <label className="field"><span>{translate("admin.profiles.editor.accessEnd")}</span><div><input type="time" value={accessEndTime} disabled={saving} onChange={(event) => setAccessEndTime(event.target.value)} required /></div></label>
          </div><p className="profile-access-editor__hint">{translate("admin.profiles.editor.dailyHoursHint")}</p></>}
        </section>
        <div className="modal-actions"><Button type="button" variant="ghost" disabled={saving} onClick={() => setEditing(null)}>{translate("common.cancel")}</Button><Button type="submit" loading={saving} disabled={!name.trim() || (editing === "new" && !categoryId)}><Save size={18} /> {translate("admin.profiles.actions.save")}</Button></div>
      </form>
    </Modal>}
    {movingProfiles.length > 0 && <Modal onClose={() => { if (!moveSaving) setMovingProfiles([]); }} className="editor-modal move-category-modal">
      <div className="editor-modal__heading"><span><Layers3 size={18} /> {translate("admin.profiles.move.eyebrow")}</span><h2>{translate(movingProfiles.length === 1 ? "admin.profiles.move.titleOne" : "admin.profiles.move.titleMany", movingProfiles.length === 1 ? { name: movingProfiles[0]!.name } : { count: movingProfiles.length })}</h2><p>{translate("admin.profiles.move.description")}</p></div>
      <div className="form-stack">
        {error && <Notice>{error}</Notice>}
        <label className="field"><span>{translate("admin.profiles.move.destination")}</span><div><Layers3 size={18} /><Select autoFocus required value={moveCategoryId} disabled={moveSaving} onChange={setMoveCategoryId} options={[{ value: "", label: translate("admin.profiles.move.destination"), disabled: true }, ...categories.filter((category) => movingProfiles.some((profile) => profile.categoryId !== category.id)).map((category) => ({ value: category.id, label: category.name }))]} /></div></label>
        <div className="modal-actions"><Button type="button" variant="secondary" disabled={moveSaving} onClick={() => setMovingProfiles([])}>{translate("common.cancel")}</Button><Button type="button" loading={moveSaving} disabled={!moveCategoryId} onClick={() => void moveProfiles()}>{translate("admin.profiles.move.confirm")}</Button></div>
      </div>
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
                <Button variant="secondary" className="profile-session__message-action" disabled={sendingMessage} onClick={() => openSessionMessage(session)}><Bell size={15} /> {translate("admin.profiles.sessions.actions.message")}</Button>
                {!session.current && <Button variant="danger" onClick={() => setRevokingSession(session)}>{translate("admin.profiles.sessions.actions.revoke")}</Button>}
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


function effectiveProfileIds(resource: { profileIds: string[]; categoryIds: string[] }, profiles: Profile[]) {
  const explicit = new Set(resource.profileIds);
  const categories = new Set(resource.categoryIds);
  return profiles.filter((profile) => explicit.has(profile.id) || categories.has(profile.categoryId)).map((profile) => profile.id);
}

function AddonsAdmin() {
  const { account, activeProfile } = useAuth();
  const administrationProfiles = useAdministrationProfiles();
  const profiles = administrationProfiles.filter((profile) => account?.session.authorizationScope === "global_admin" || profile.canManage);
  const [categories, setCategories] = useState<AccessCategory[]>([]);
  const isGlobalAdmin = account?.session.authorizationScope === "global_admin";
  const [addons, setAddons] = useState<InstalledAddon[]>([]);
  const [transportUrl, setTransportUrl] = useState("");
  const [installProfileIds, setInstallProfileIds] = useState<string[]>(() => activeProfile ? [activeProfile.id] : []);
  const [installCategoryIds, setInstallCategoryIds] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [working, setWorking] = useState("");
  const [error, setError] = useState("");
  const [diagnostics, setDiagnostics] = useState<AddonDiagnosticsResponse | null>(null);
  const [diagnosticsError, setDiagnosticsError] = useState("");
  const [addonPreview, setAddonPreview] = useState<(AddonPreviewResponse & { input: InstallAddonInput }) | null>(null);
  const [previewError, setPreviewError] = useState("");
  const [previewing, setPreviewing] = useState(false);
  const [deleting, setDeleting] = useState<InstalledAddon | null>(null);
  const [editingAddon, setEditingAddon] = useState<ManagedAddon | null>(null);
  const [editTransportUrl, setEditTransportUrl] = useState("");
  const [editProfileIds, setEditProfileIds] = useState<string[]>([]);
  const [editCategoryIds, setEditCategoryIds] = useState<string[]>([]);
  const [editEnabled, setEditEnabled] = useState(true);
  const [draggedAddonIndex, setDraggedAddonIndex] = useState<number | null>(null);
  const [reordering, setReordering] = useState(false);
  const reorderInFlight = useRef(false);
  const initialLoadStarted = useRef(false);
  const previewRequestRef = useRef<{ controller: AbortController } | null>(null);
  const installAssignmentInitialized = useRef(Boolean(activeProfile));

  async function load() {
    setLoading(true);
    setError("");
    setDiagnostics(null);
    setDiagnosticsError("");
    const addonListRequest = api.addons()
      .then((response) => setAddons(response.addons))
      .catch((cause) => setError(notifyError(cause, translate("admin.addons.errors.load"), translate("admin.addons.errors.unavailableTitle"))))
      .finally(() => setLoading(false));
    const diagnosticsRequest = isGlobalAdmin
      ? api.addonDiagnostics()
        .then(setDiagnostics)
        .catch(() => setDiagnosticsError(translate("admin.addons.diagnostics.loadFailed")))
      : Promise.resolve();
    const categoriesRequest = isGlobalAdmin
      ? api.categories().then(setCategories).catch((cause) => setError(notifyError(cause, translate("admin.addons.errors.load"))))
      : Promise.resolve();
    await Promise.all([addonListRequest, diagnosticsRequest, categoriesRequest]);
  }
  useEffect(() => {
    if (initialLoadStarted.current) return;
    initialLoadStarted.current = true;
    void load();
  }, []);
  useEffect(() => () => {
    const request = previewRequestRef.current;
    previewRequestRef.current = null;
    request?.controller.abort();
  }, []);
  useEffect(() => {
    if (installAssignmentInitialized.current || !activeProfile) return;
    installAssignmentInitialized.current = true;
    setInstallProfileIds([activeProfile.id]);
  }, [activeProfile]);

  function invalidateAddonPreview() {
    previewRequestRef.current?.controller.abort();
    previewRequestRef.current = null;
    setAddonPreview(null);
    setPreviewError("");
    setPreviewing(false);
  }

  async function previewAddon(event: FormEvent) {
    event.preventDefault();
    if (!isGlobalAdmin || previewRequestRef.current || working === "install" || !transportUrl || installProfileIds.length + installCategoryIds.length === 0) return;
    const input: InstallAddonInput = { transportUrl, profileIds: [...installProfileIds], categoryIds: [...installCategoryIds] };
    const request = { controller: new AbortController() };
    previewRequestRef.current = request;
    setAddonPreview(null);
    setPreviewError("");
    setPreviewing(true);
    setError("");
    try {
      const response = await api.previewAddon(input, request.controller.signal);
      if (previewRequestRef.current !== request) return;
      setAddonPreview({ ...response, input });
    } catch {
      if (previewRequestRef.current !== request) return;
      setPreviewError(translate("admin.addons.errors.preview"));
    } finally {
      if (previewRequestRef.current === request) {
        previewRequestRef.current = null;
        setPreviewing(false);
      }
    }
  }

  async function openAddonEditor(addon: InstalledAddon) {
    setError("");
    if (account?.session.authorizationScope !== "global_admin") {
      setEditingAddon(addon);
      setEditTransportUrl("");
      setEditProfileIds(addon.profileIds);
      setEditCategoryIds(addon.categoryIds);
      setEditEnabled(addon.enabled);
      return;
    }
    setWorking(addon.id);
    try {
      const managed = await api.addonManagement(addon.id);
      if (!managed.transportUrl) throw new Error("Addon management response did not include a transport URL");
      setEditingAddon(managed);
      setEditTransportUrl(managed.transportUrl);
      setEditProfileIds(managed.profileIds);
      setEditCategoryIds(managed.categoryIds);
      setEditEnabled(managed.enabled);
    } catch (cause) {
      setError(notifyError(cause, translate("admin.addons.errors.load"), translate("admin.addons.errors.unavailableTitle")));
    } finally {
      setWorking("");
    }
  }

  function closeAddonEditor() {
    setEditingAddon(null);
    setEditTransportUrl("");
    setEditProfileIds([]);
    setEditCategoryIds([]);
    setEditEnabled(true);
  }

  async function saveAddon(event: FormEvent) {
    event.preventDefault();
    if (!editingAddon || !activeProfile || editProfileIds.length + editCategoryIds.length === 0) return;
    setWorking(editingAddon.id);
    setError("");
    try {
      const existingProfileIds = new Set(editingAddon.profileIds);
      const existingCategoryIds = new Set(editingAddon.categoryIds);
      const profileIdsChanged = existingProfileIds.size !== editProfileIds.length || editProfileIds.some((id) => !existingProfileIds.has(id));
      const categoryIdsChanged = existingCategoryIds.size !== editCategoryIds.length || editCategoryIds.some((id) => !existingCategoryIds.has(id));
      const nextTransportUrl = editTransportUrl.trim();
      const input: UpdateAddonInput = {
        ...(profileIdsChanged ? { profileIds: [...editProfileIds] } : {}),
        ...(categoryIdsChanged ? { categoryIds: [...editCategoryIds] } : {}),
        ...(isGlobalAdmin && editEnabled !== editingAddon.enabled ? { enabled: editEnabled } : {}),
        ...(isGlobalAdmin && nextTransportUrl && nextTransportUrl !== (editingAddon.transportUrl ?? "") ? { transportUrl: nextTransportUrl } : {}),
      };
      const updated = await api.updateAddon(editingAddon.id, input);
      const publicUpdated: InstalledAddon = {
        id: updated.id,
        manifest: updated.manifest,
        position: updated.position,
        enabled: updated.enabled,
        profileIds: updated.profileIds,
        categoryIds: updated.categoryIds,
        installedAt: updated.installedAt,
        updatedAt: updated.updatedAt,
      };
      setAddons((values) => effectiveProfileIds(publicUpdated, profiles).includes(activeProfile.id)
        ? values.map((value) => value.id === publicUpdated.id ? publicUpdated : value)
        : values.filter((value) => value.id !== publicUpdated.id));
      closeAddonEditor();
      notifySuccess(translate("admin.addons.notifications.savedMessage", { name: updated.manifest.name }), translate("admin.addons.notifications.savedTitle"));
    } catch (cause) {
      setError(notifyError(cause, translate("admin.addons.errors.save")));
    } finally {
      setWorking("");
    }
  }

  async function install() {
    if (!isGlobalAdmin || !addonPreview || working === "install") return;
    const profileSelectionMatches = addonPreview.input.profileIds.length === installProfileIds.length && addonPreview.input.profileIds.every((value, index) => value === installProfileIds[index]);
    const categorySelectionMatches = addonPreview.input.categoryIds.length === installCategoryIds.length && addonPreview.input.categoryIds.every((value, index) => value === installCategoryIds[index]);
    if (addonPreview.input.transportUrl !== transportUrl || !profileSelectionMatches || !categorySelectionMatches) {
      invalidateAddonPreview();
      return;
    }
    setWorking("install");
    setError("");
    try {
      const installed = await api.installAddon(addonPreview.input);
      invalidateAddonPreview();
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
  const assignedProfiles = new Set(addons.flatMap((addon) => effectiveProfileIds(addon, profiles))).size;
  const contentTypes = new Set(addons.flatMap((addon) => addon.manifest.types)).size;
  const diagnosticsByAddonId = new Map(diagnostics?.diagnostics.map((diagnostic) => [diagnostic.addonId, diagnostic]) ?? []);
  return <div className="admin-section addons-admin">
    <div className="admin-section__header">
      <div><span>{translate("admin.addons.eyebrow")}</span><h2>{translate("admin.addons.title")}</h2><p>{translate("admin.addons.description")}</p></div>
    </div>
    <section className="admin-summary" aria-label={translate("admin.addons.overview.label")}>
      <article><span><Boxes size={18} aria-hidden="true" /></span><div><strong>{loading ? "—" : addons.length}</strong><small>{translate("admin.addons.overview.installed")}</small></div></article>
      <article><span><CircleUserRound size={18} aria-hidden="true" /></span><div><strong>{loading ? "—" : assignedProfiles}</strong><small>{translate("admin.common.profilesReached")}</small></div></article>
      <article><span><Film size={18} aria-hidden="true" /></span><div><strong>{loading ? "—" : contentTypes}</strong><small>{translate("admin.addons.overview.contentTypes")}</small></div></article>
    </section>
    {isGlobalAdmin && <section className="admin-tool-card" aria-labelledby="install-addon-title">
      <header><div><span>{translate("admin.addons.install.eyebrow")}</span><h3 id="install-addon-title">{translate("admin.addons.install.title")}</h3><p>{translate("admin.addons.install.description")}</p></div></header>
      <form className="install-addon" onSubmit={previewAddon}>
        <label className="field"><span>{translate("admin.addons.install.manifestUrl")}</span><div><WandSparkles size={19} /><input type="url" value={transportUrl} onChange={(event) => { invalidateAddonPreview(); setTransportUrl(event.target.value); }} placeholder={translate("admin.addons.manifestUrlPlaceholder")} required /></div></label>
        <Button type="submit" loading={previewing} disabled={working === "install" || installProfileIds.length + installCategoryIds.length === 0}><Search size={18} /> {translate("admin.addons.actions.preview")}</Button>
      </form>
      <AssignmentPicker categories={categories} profiles={profiles} profileIds={installProfileIds} categoryIds={installCategoryIds} onChange={({ profileIds, categoryIds }) => { installAssignmentInitialized.current = true; invalidateAddonPreview(); setInstallProfileIds(profileIds); setInstallCategoryIds(categoryIds); }} />
      {previewError && <Notice>{previewError}</Notice>}
      {addonPreview && <AddonInstallPreview preview={addonPreview} installing={working === "install"} onInstall={() => void install()} />}
    </section>}
    {error && <Notice>{error}</Notice>}
    {diagnosticsError && <Notice>{diagnosticsError}</Notice>}
    {diagnostics && <p className="addon-diagnostics-observed">{translate("admin.addons.diagnostics.observedSince", { date: new Date(diagnostics.observedSince).toLocaleString() })}</p>}
    {loading
      ? <div className="addon-list" aria-label={translate("admin.addons.loadingLabel")}>{[0, 1].map((value) => <Skeleton key={value} className="addon-skeleton" />)}</div>
      : addons.length
        ? <div className="addon-list">{addons.map((addon, addonIndex) => <AddonCard key={addon.id} addon={addon} reach={effectiveProfileIds(addon, profiles).length} diagnostic={diagnosticsByAddonId.get(addon.id)} index={addonIndex} total={addons.length} working={working === addon.id} reordering={reordering} dragging={draggedAddonIndex === addonIndex} onDragStart={(event) => { setDraggedAddonIndex(addonIndex); event.dataTransfer.effectAllowed = "move"; event.dataTransfer.setData("text/plain", String(addonIndex)); }} onDragEnter={(event) => { event.preventDefault(); if (draggedAddonIndex !== null) stageAddonMove(draggedAddonIndex, addonIndex); }} onDragOver={(event) => { if (draggedAddonIndex !== null) { event.preventDefault(); event.dataTransfer.dropEffect = "move"; } }} onDrop={(event) => { event.preventDefault(); void saveAddonOrder(); }} onDragEnd={() => { if (draggedAddonIndex !== null) void saveAddonOrder(); }} onMove={(toIndex) => void moveAddon(addonIndex, toIndex)} onRefresh={() => void refresh(addon.id)} onEdit={() => void openAddonEditor(addon)} onRemove={() => setDeleting(addon)} />)}</div>
        : <EmptyState icon={<Boxes size={44} />} title={translate(error ? "admin.addons.errors.unavailableTitle" : "admin.addons.empty.title")} description={translate(error ? "admin.addons.errors.retryDescription" : "admin.addons.empty.description")} action={error ? <Button variant="secondary" onClick={() => void load()}><RefreshCw size={17} /> {translate("common.actions.tryAgain")}</Button> : undefined} />}
    {editingAddon && <Modal onClose={() => { if (!addonEditSaving) closeAddonEditor(); }} className="editor-modal addon-edit-modal"><form onSubmit={saveAddon}><div className="editor-modal__heading"><span><Pencil size={18} /> {translate("admin.addons.editor.title")}</span><h2>{editingAddon.manifest.name}</h2><p>{translate("admin.addons.editor.description")}</p></div>{error && <Notice>{error}</Notice>}{account?.session.authorizationScope === "global_admin" && <><label className="field"><span>{translate("admin.addons.editor.transportUrl")}</span><div><WandSparkles size={18} /><input type="url" value={editTransportUrl} disabled={addonEditSaving} onChange={(event) => setEditTransportUrl(event.target.value)} placeholder={translate("admin.addons.manifestUrlPlaceholder")} /></div></label><section className="addon-availability" aria-labelledby="addon-availability-title" aria-describedby="addon-availability-description"><div><strong id="addon-availability-title">{translate("admin.addons.editor.availability")}</strong><p id="addon-availability-description">{translate("admin.addons.editor.availabilityDescription")}</p><span className={`addon-availability__status ${editEnabled ? "is-enabled" : "is-disabled"}`}>{translate(editEnabled ? "common.status.enabled" : "common.status.disabled")}</span></div><Button type="button" variant="secondary" className="addon-availability__toggle" aria-pressed={editEnabled} disabled={addonEditSaving} onClick={() => setEditEnabled((value) => !value)}>{translate(editEnabled ? "admin.addons.actions.disable" : "admin.addons.actions.enable")}</Button></section></>}<AssignmentPicker categories={categories} profiles={profiles} profileIds={editProfileIds} categoryIds={editCategoryIds} disabled={addonEditSaving} onChange={({ profileIds, categoryIds }) => { setEditProfileIds(profileIds); setEditCategoryIds(categoryIds); }} /><div className="modal-actions"><Button type="button" variant="ghost" disabled={addonEditSaving} onClick={closeAddonEditor}>{translate("common.cancel")}</Button><Button type="submit" loading={addonEditSaving} disabled={editProfileIds.length + editCategoryIds.length === 0}><Save size={18} /> {translate("admin.addons.actions.save")}</Button></div></form></Modal>}
    {deleting && <ConfirmDialog title={translate("admin.addons.remove.title", { name: deleting.manifest.name })} description={translate("admin.addons.remove.description")} confirmLabel={translate("admin.addons.remove.confirm")} loading={working === deleting.id} onCancel={() => setDeleting(null)} onConfirm={() => void remove(deleting)} />}
  </div>;
}

function AddonInstallPreview({ preview, installing, onInstall }: { preview: AddonPreviewResponse; installing: boolean; onInstall: () => void }) {
  const { manifest, capabilities } = preview;
  const hasWarnings = Boolean(manifest.behaviorHints?.p2p || manifest.behaviorHints?.adult || manifest.behaviorHints?.configurationRequired);
  return <section className="addon-preview" aria-labelledby="addon-preview-title">
    <header>
      <div><h4 id="addon-preview-title">{translate("admin.addons.preview.title")}</h4><p>{translate("admin.addons.preview.description")}</p></div>
    </header>
    <div className="addon-preview__manifest">
      <div className="addon-card__logo" aria-hidden="true">{manifest.name.slice(0, 2).toUpperCase()}</div>
      <div className="addon-preview__body">
        <div className="addon-preview__identity"><strong>{manifest.name}</strong><span>v{manifest.version}</span></div>
        {manifest.description && <p>{manifest.description}</p>}
        <div className="addon-card__types" role="list" aria-label={translate("admin.addons.overview.contentTypes")}>{manifest.types.map((type, index) => <i role="listitem" key={`${type}-${index}`}>{type}</i>)}</div>
        <div className="addon-preview__capabilities" role="list">
          {(capabilities.search || capabilities.searchPagination) && <span role="listitem">{translate("admin.addons.diagnostics.capability.search")}</span>}
          {(capabilities.pagination || capabilities.searchPagination) && <span role="listitem">{translate("admin.addons.diagnostics.capability.pagination")}</span>}
          {capabilities.resources.map((resource, index) => <span role="listitem" key={`${resource}-${index}`}>{resource}</span>)}
        </div>
        {hasWarnings && <div className="addon-preview__warnings" role="list">
          {manifest.behaviorHints?.p2p && <span role="listitem">P2P</span>}
          {manifest.behaviorHints?.adult && <span role="listitem">{translate("admin.addons.preview.adult")}</span>}
          {manifest.behaviorHints?.configurationRequired && <span role="listitem">{translate("admin.addons.preview.configurationRequired")}</span>}
        </div>}
      </div>
    </div>
    <Button type="button" loading={installing} onClick={onInstall}><Plus size={18} /> {translate("admin.addons.actions.install")}</Button>
  </section>;
}

const diagnosticStateKeys: Record<AddonDiagnosticState, TranslationKey> = {
  unknown: "admin.addons.diagnostics.state.unknown",
  available: "admin.addons.diagnostics.state.available",
  degraded: "admin.addons.diagnostics.state.degraded",
  unavailable: "admin.addons.diagnostics.state.unavailable",
};

const diagnosticErrorKeys: Record<AddonDiagnosticErrorCode, TranslationKey> = {
  timeout: "admin.addons.diagnostics.error.timeout",
  invalid_response: "admin.addons.diagnostics.error.invalidResponse",
  unavailable: "admin.addons.diagnostics.error.unavailable",
  request_failed: "admin.addons.diagnostics.error.requestFailed",
};

function AddonCard({ addon, reach, diagnostic, index, total, working, reordering, dragging, onDragStart, onDragEnter, onDragOver, onDrop, onDragEnd, onMove, onRefresh, onEdit, onRemove }: {
  addon: InstalledAddon;
  reach: number;
  diagnostic?: AddonDiagnostic;
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
  return <article className={`addon-card ${!addon.enabled ? "is-disabled" : ""} ${dragging ? "is-dragging" : ""}`} onDragEnter={onDragEnter} onDragOver={onDragOver} onDrop={onDrop}>
    <button type="button" className="addon-card__drag" draggable={!reordering && !working} disabled={reordering || working} onDragStart={onDragStart} onDragEnd={onDragEnd} aria-label={translate("admin.addons.reorder.dragLabel", { name: manifest.name })}><GripVertical /></button>
    <div className="addon-card__logo">{manifest.logo ? <img src={manifest.logo} alt="" /> : manifest.name.slice(0, 2).toUpperCase()}</div>
    <div className="addon-card__body">
      <div><h3>{manifest.name}</h3><span>v{manifest.version}</span>{!addon.enabled && <span className="addon-badge addon-badge--disabled">{translate("common.status.disabled")}</span>}{manifest.behaviorHints?.p2p && <span className="addon-badge addon-badge--warn">P2P</span>}</div>
      <p>{manifest.description || translate("admin.addons.card.noDescription")}</p>
      <div className="addon-card__types">{manifest.types.map((type) => <i key={type}>{type}</i>)}</div>
      <span className="assignment-reach">{reach} {translate("admin.common.profilesReached")}</span>
      {diagnostic && <div className="addon-diagnostics">
        <div className="addon-diagnostics__summary">
          <span className={`addon-diagnostics__state addon-diagnostics__state--${diagnostic.state}`}>{translate(diagnosticStateKeys[diagnostic.state])}</span>
          <div className="addon-diagnostics__capabilities">
            {(diagnostic.capabilities.search || diagnostic.capabilities.searchPagination) && <span>{translate("admin.addons.diagnostics.capability.search")}</span>}
            {(diagnostic.capabilities.pagination || diagnostic.capabilities.searchPagination) && <span>{translate("admin.addons.diagnostics.capability.pagination")}</span>}
            {diagnostic.capabilities.resources.map((resource) => <span key={resource}>{resource}</span>)}
          </div>
        </div>
        {(diagnostic.lastSuccessAt || diagnostic.approximateLatencyMs !== undefined || diagnostic.lastError) && <div className="addon-diagnostics__history">
          {diagnostic.lastSuccessAt && <span>{translate("admin.addons.diagnostics.lastSuccess", { date: new Date(diagnostic.lastSuccessAt).toLocaleString() })}</span>}
          {diagnostic.approximateLatencyMs !== undefined && <span>{translate("admin.addons.diagnostics.latency", { milliseconds: diagnostic.approximateLatencyMs })}</span>}
          {diagnostic.lastError && <span>{translate("admin.addons.diagnostics.lastError", { error: translate(diagnosticErrorKeys[diagnostic.lastError.code]) })}</span>}
        </div>}
      </div>}
    </div>
    <div className="addon-card__controls">
      {working ? <span className="admin-working" role="status"><LoaderCircle className="spin" size={18} /> {translate("common.status.working")}</span> : <>
        <div className="addon-card__order" aria-label={translate("admin.addons.reorder.groupLabel", { name: manifest.name })}><IconButton label={translate("admin.addons.reorder.moveUp", { name: manifest.name })} disabled={reordering || index === 0} onClick={() => onMove(index - 1)}><ChevronUp size={17} /></IconButton><IconButton label={translate("admin.addons.reorder.moveDown", { name: manifest.name })} disabled={reordering || index === total - 1} onClick={() => onMove(index + 1)}><ChevronDown size={17} /></IconButton></div>
        <div className="addon-card__actions"><Button variant="ghost" disabled={reordering} onClick={onEdit}><Pencil size={16} /> {translate("common.actions.edit")}</Button><Button variant="ghost" disabled={reordering} onClick={onRefresh}><RefreshCw size={16} /> {translate("common.actions.refresh")}</Button><Button variant="ghost" className="admin-destructive-action" disabled={reordering} onClick={onRemove}><Trash2 size={16} /> {translate("common.actions.remove")}</Button></div>
      </>}
    </div>
  </article>;
}

function AssignmentPicker({ categories, profiles, profileIds, categoryIds, disabled = false, onChange }: {
  categories: AccessCategory[];
  profiles: Profile[];
  profileIds: string[];
  categoryIds: string[];
  disabled?: boolean;
  onChange: (assignment: { profileIds: string[]; categoryIds: string[] }) => void;
}) {

  return <fieldset className="assignment-picker">
    <legend>{translate("admin.profileAssignment.legend")}</legend>
    <section className="assignment-picker__section">
      <header><h4>{translate("admin.assignment.categories")}</h4><p>{translate("admin.assignment.categoriesDescription")}</p></header>
      <div className="assignment-picker__options assignment-picker__categories">{categories.map((category) => {
        const checked = categoryIds.includes(category.id);
        return <label key={category.id} className={checked ? "is-selected" : ""} style={category.color ? { "--category-color": category.color } as CSSProperties : undefined}>
          <input type="checkbox" checked={checked} disabled={disabled} onChange={() => onChange({ profileIds, categoryIds: categoryIds.includes(category.id) ? categoryIds.filter((value) => value !== category.id) : [...categoryIds, category.id] })} />
          <span className="assignment-picker__mark" aria-hidden="true">{category.icon || <Layers3 size={16} />}</span>
          <span><strong>{category.name}</strong><small>{translate(checked ? "admin.profileAssignment.included" : "admin.profileAssignment.notIncluded")}</small></span>
        </label>;
      })}</div>
    </section>
    <details className="assignment-picker__profiles">
      <summary><ChevronDown size={17} aria-hidden="true" /><span>{translate("admin.assignment.showProfiles", { count: profileIds.length })}</span></summary>
      <section className="assignment-picker__section">
        <header><h4>{translate("admin.assignment.profiles")}</h4><p>{translate("admin.assignment.profilesDescription")}</p></header>
        <div className="assignment-picker__options">{profiles.map((profile) => {
          const checked = profileIds.includes(profile.id);
          return <label key={profile.id} className={checked ? "is-selected" : ""}>
            <input type="checkbox" checked={checked} disabled={disabled} onChange={() => onChange({ profileIds: profileIds.includes(profile.id) ? profileIds.filter((value) => value !== profile.id) : [...profileIds, profile.id], categoryIds })} />
            <img src={profile.avatar.url} alt="" />
            <span><strong>{profile.name}</strong><small>{translate(checked ? "admin.profileAssignment.included" : "admin.profileAssignment.notIncluded")}</small></span>
          </label>;
        })}</div>
      </section>
    </details>
    <small className="assignment-picker__description">{translate("admin.assignment.description")}</small>
  </fieldset>;
}

const blankFolder = (): CollectionFolder => ({ title: translate("admin.collections.defaults.folderTitle"), tileShape: "poster", sourceView: "merged", focusGifEnabled: false, hideTitle: false, sources: [] });
const blankCollection = (profileIds: string[] = []): CollectionSaveInput => ({ title: translate("admin.collections.defaults.collectionTitle"), heroEnabled: false, pinToTop: false, focusGlowEnabled: true, viewMode: "rows", folderCoverShape: "poster", folders: [blankFolder()], profileIds, categoryIds: [] })

function collectionCardSummary(collection: Collection, reach: number): string {
  const folderCount = collection.folders.length;
  const sourceCount = collection.folders.reduce((total, folder) => total + folder.sources.length, 0);
  const folders = translate(folderCount === 1 ? "admin.collections.card.folderCountOne" : "admin.collections.card.folderCountMany", { folderCount });
  const sources = translate(sourceCount === 1 ? "admin.collections.card.sourceCountOne" : "admin.collections.card.sourceCountMany", { sourceCount });
  return `${folders} · ${sources} · ${reach} ${translate("admin.common.profilesReached")}`;
}

function CollectionsAdmin() {
  const { account, activeProfile } = useAuth();
  const administrationProfiles = useAdministrationProfiles();
  const profiles = administrationProfiles.filter((profile) => account?.session.authorizationScope === "global_admin" || profile.canManage);
  const [categories, setCategories] = useState<AccessCategory[]>([]);
  const [collections, setCollections] = useState<Collection[]>([]);
  const [editing, setEditing] = useState<Collection | "new" | null>(null);
  const [draft, setDraft] = useState<CollectionSaveInput>(blankCollection(activeProfile ? [activeProfile.id] : []));
  const [catalogs, setCatalogs] = useState<Array<{ addonId: string; manifestId: string; catalog: { type: string; id: string; name?: string } }>>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [transfer, setTransfer] = useState<"" | "export" | "import">("");
  const [draggedFolderIndex, setDraggedFolderIndex] = useState<number | null>(null);
  const [draggedSource, setDraggedSource] = useState<{ folderIndex: number; sourceIndex: number } | null>(null);
  const [deleting, setDeleting] = useState<Collection | null>(null);
  const [draggedCollectionIndex, setDraggedCollectionIndex] = useState<number | null>(null);
  const [reordering, setReordering] = useState(false);
  const reorderInFlight = useRef(false);
  const editorRequestSequence = useRef(0);
  const importInput = useRef<HTMLInputElement>(null);

  async function load() {
    setLoading(true);
    setError("");
    try { setCollections((await api.collections()).collections); } catch (cause) { setError(notifyError(cause, translate("admin.collections.errors.load"), translate("admin.collections.errors.unavailableTitle"))); } finally { setLoading(false); }
  }
  useEffect(() => {
    void load();
    void api.addonCatalogs().then((response) => setCatalogs(response.catalogs)).catch(() => undefined);
    void api.categories().then(setCategories).catch((cause) => setError(notifyError(cause, translate("admin.collections.errors.load"), translate("admin.collections.errors.unavailableTitle"))));
  }, []);

  async function openEditor(collection: Collection | "new") {
    const requestSequence = ++editorRequestSequence.current;
    const emptyDraft = blankCollection(activeProfile ? [activeProfile.id] : []);
    setError("");
    setDraggedFolderIndex(null);
    setDraggedSource(null);
    if (collection === "new") {
      setDraft(emptyDraft);
      setEditing("new");
      return;
    }
    setEditing(null);
    setDraft(emptyDraft);
    try {
      const managed = await api.collectionManagement(collection.id);
      if (editorRequestSequence.current !== requestSequence) return;
      setDraft({ title: managed.title, backdropImageUrl: managed.backdropImageUrl, heroEnabled: managed.heroEnabled, pinToTop: managed.pinToTop, focusGlowEnabled: managed.focusGlowEnabled, viewMode: managed.viewMode, folderCoverShape: managed.folderCoverShape, folders: structuredClone(managed.folders), profileIds: managed.profileIds, categoryIds: managed.categoryIds, expectedVersion: managed.version });
      setEditing(managed);
    } catch (cause) {
      if (editorRequestSequence.current !== requestSequence) return;
      setEditing(null);
      setDraft(emptyDraft);
      setError(notifyError(cause, translate("admin.collections.errors.load"), translate("admin.collections.errors.unavailableTitle")));
    }
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
      source = { kind, title: catalog?.catalog.name || catalog?.catalog.id || translate("admin.collections.sources.defaults.addonCatalog"), addonCatalog: { addonId: catalog?.addonId ?? "", manifestId: catalog?.manifestId, type: catalog?.catalog.type ?? "movie", catalogId: catalog?.catalog.id ?? "" } };
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
    try {
      const document: unknown = JSON.parse(await file.text());
      const result = await api.importCollections(document);
      await load();
      const message = translate(result.imported === 1 ? "admin.collections.import.successOne" : "admin.collections.import.successMany", { count: result.imported });
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
    if (draft.profileIds.length + draft.categoryIds.length === 0) return;
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
  const assignedProfiles = new Set(collections.flatMap((collection) => effectiveProfileIds(collection, profiles))).size;

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
    {error && <Notice>{error}</Notice>}
    {loading
      ? <div className="collection-admin-grid" aria-label={translate("admin.collections.loadingLabel")}><Skeleton className="collection-skeleton" /><Skeleton className="collection-skeleton" /></div>
      : collections.length
        ? <div className="collection-admin-grid">{collections.map((collection, collectionIndex) =>
          <article key={collection.id} className={`collection-admin-card ${draggedCollectionIndex === collectionIndex ? "is-dragging" : ""}`} style={collection.backdropImageUrl ? { backgroundImage: `url(${collection.backdropImageUrl})` } : undefined} draggable={!reordering} onDragStart={(event) => { setDraggedCollectionIndex(collectionIndex); event.dataTransfer.effectAllowed = "move"; event.dataTransfer.setData("text/plain", String(collectionIndex)); }} onDragEnter={(event) => { event.preventDefault(); if (draggedCollectionIndex !== null) stageCollectionMove(draggedCollectionIndex, collectionIndex); }} onDragOver={(event) => { if (draggedCollectionIndex !== null) { event.preventDefault(); event.dataTransfer.dropEffect = "move"; } }} onDrop={(event) => { event.preventDefault(); void saveCollectionOrder(); }} onDragEnd={() => { if (draggedCollectionIndex !== null) void saveCollectionOrder(); }}>
            <div className="collection-admin-card__shade" />
            <span>{collection.pinToTop ? <><Sparkles size={14} /> {translate("admin.collections.card.pinned")}</> : translate("admin.collections.card.position", { position: collection.position + 1 })}</span>
            <div><h3>{collection.title}</h3><p>{collectionCardSummary(collection, effectiveProfileIds(collection, profiles).length)}</p>
              <div className="collection-admin-card__actions">
                <span className="collection-admin-card__order"><IconButton label={translate("admin.collections.reorder.moveUp", { title: collection.title })} disabled={reordering || collectionIndex === 0 || collections[collectionIndex - 1]?.pinToTop !== collection.pinToTop} onClick={() => void moveCollection(collectionIndex, collectionIndex - 1)}><ChevronUp size={17} /></IconButton><IconButton label={translate("admin.collections.reorder.moveDown", { title: collection.title })} disabled={reordering || collectionIndex === collections.length - 1 || collections[collectionIndex + 1]?.pinToTop !== collection.pinToTop} onClick={() => void moveCollection(collectionIndex, collectionIndex + 1)}><ChevronDown size={17} /></IconButton></span>
                <Button variant="secondary" onClick={() => void openEditor(collection)}><Pencil size={17} /> {translate("common.actions.edit")}</Button>
                <Button variant="ghost" className="admin-destructive-action" onClick={() => setDeleting(collection)}><Trash2 size={17} /> {translate("common.actions.delete")}</Button>
              </div>
            </div>
          </article>,
        )}</div>
        : <EmptyState icon={<Layers3 size={44} />} title={translate(error ? "admin.collections.errors.unavailableTitle" : "admin.collections.empty.title")} description={translate(error ? "admin.collections.errors.retryDescription" : "admin.collections.empty.description")} action={error ? <Button variant="secondary" onClick={() => void load()}><RefreshCw size={17} /> {translate("common.actions.tryAgain")}</Button> : <Button onClick={() => openEditor("new")}><Plus size={18} /> {translate("admin.collections.actions.create")}</Button>} />}
    {editing && <Modal onClose={() => { if (!saving) setEditing(null); }} className="editor-modal collection-editor"><form onSubmit={submit}>
      <div className="editor-modal__heading">
        <span><Layers3 size={18} /> {translate(editing === "new" ? "admin.collections.editor.newEyebrow" : "admin.collections.editor.editEyebrow")}</span>
        <h2>{translate("admin.collections.editor.title")}</h2>
        <p>{translate("admin.collections.editor.description")}</p>
      </div>
      {error && <Notice>{error}</Notice>}
      <section className="editor-group">
        <div className="form-grid form-grid--three">
          <label className="field"><span>{translate("admin.collections.editor.collectionTitle")}</span><div><Layers3 size={18} /><input value={draft.title} onChange={(event) => setDraft((current) => ({ ...current, title: event.target.value }))} required /></div></label>
          <label className="field"><span>{translate("admin.collections.editor.backdropUrl")}</span><div><ImagePlus size={18} /><input inputMode="url" value={draft.backdropImageUrl ?? ""} onChange={(event) => setDraft((current) => ({ ...current, backdropImageUrl: event.target.value || undefined }))} placeholder={translate("admin.collections.editor.imageUrlPlaceholder")} /></div></label>
          <label className="field"><span>{translate("admin.collections.editor.folderCoverShape")}</span><div><Boxes size={18} /><Select value={draft.folderCoverShape} onChange={(value) => setDraft((current) => ({ ...current, folderCoverShape: value as CollectionSaveInput["folderCoverShape"] }))} options={[{ value: "poster", label: translate("admin.collections.shapes.poster") }, { value: "landscape", label: translate("admin.collections.shapes.landscape") }, { value: "square", label: translate("admin.collections.shapes.square") }]} /></div></label>
        </div>
        <div className="choice-row choice-row--four">
          <label className="toggle-field"><input type="checkbox" checked={draft.heroEnabled} onChange={(event) => setDraft((current) => ({ ...current, heroEnabled: event.target.checked }))} /><span><i /><div><strong>{translate("admin.collections.editor.hero")}</strong><small>{translate("admin.collections.editor.heroDescription")}</small></div></span></label>
          <label className="toggle-field"><input type="checkbox" checked={draft.pinToTop} onChange={(event) => setDraft((current) => ({ ...current, pinToTop: event.target.checked }))} /><span><i /><div><strong>{translate("admin.collections.editor.pinToTop")}</strong><small>{translate("admin.collections.editor.pinToTopDescription")}</small></div></span></label>
          <label className="toggle-field"><input type="checkbox" checked={draft.focusGlowEnabled} onChange={(event) => setDraft((current) => ({ ...current, focusGlowEnabled: event.target.checked }))} /><span><i /><div><strong>{translate("admin.collections.editor.focusGlow")}</strong><small>{translate("admin.collections.editor.focusGlowDescription")}</small></div></span></label>
          <label className="toggle-field"><input type="checkbox" checked={draft.viewMode === "follow_layout"} onChange={(event) => setDraft((current) => ({ ...current, viewMode: event.target.checked ? "follow_layout" : "rows" }))} /><span><i /><div><strong>{translate("admin.collections.editor.displayTitlesDirectly")}</strong><small>{translate("admin.collections.editor.displayTitlesDirectlyDescription")}</small></div></span></label>
        </div>
      </section>
      <AssignmentPicker categories={categories} profiles={profiles} profileIds={draft.profileIds} categoryIds={draft.categoryIds} disabled={saving} onChange={({ profileIds, categoryIds }) => setDraft((current) => ({ ...current, profileIds, categoryIds }))} />
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
            <label className="field"><span>{translate("admin.collections.folders.tileShape")}</span><div><Select value={folder.tileShape} onChange={(value) => updateFolder(folderIndex, { tileShape: value as CollectionFolder["tileShape"] })} options={[{ value: "poster", label: translate("admin.collections.shapes.poster") }, { value: "landscape", label: translate("admin.collections.shapes.landscape") }, { value: "square", label: translate("admin.collections.shapes.square") }]} /></div></label>
            <label className="field"><span>{translate("admin.collections.folders.multipleSources")}</span><div><Select value={folder.sourceView ?? "merged"} onChange={(value) => updateFolder(folderIndex, { sourceView: value as CollectionFolder["sourceView"] })} options={[{ value: "merged", label: translate("admin.collections.folders.sourceViewMerged") }, { value: "categories", label: translate("admin.collections.folders.sourceViewCategories") }, { value: "folders", label: translate("admin.collections.folders.sourceViewFolders") }]} /></div></label>
            <label className="field"><span>{translate("admin.collections.folders.coverEmoji")}</span><div><Sparkles size={18} /><input value={folder.coverEmoji ?? ""} onChange={(event) => updateFolder(folderIndex, { coverEmoji: event.target.value })} placeholder="✨" /></div></label>
            <label className="field"><span>{translate("admin.collections.folders.coverImageUrl")}</span><div><ImagePlus size={18} /><input inputMode="url" value={folder.coverImageUrl ?? ""} onChange={(event) => updateFolder(folderIndex, { coverImageUrl: event.target.value || undefined })} placeholder={translate("admin.collections.editor.imageUrlPlaceholder")} /></div></label>
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
        <Button type="button" variant="ghost" disabled={saving} onClick={() => setEditing(null)}>{translate("common.cancel")}</Button>
        <Button type="submit" loading={saving} disabled={draft.profileIds.length + draft.categoryIds.length === 0}><Save size={18} /> {translate("admin.collections.actions.save")}</Button>
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
    {source.addonCatalog && <label className="field"><span>{translate("admin.collections.sources.catalog")}</span><div><Select value={`${source.addonCatalog.addonId}|${source.addonCatalog.type}|${source.addonCatalog.catalogId}`} onChange={(value) => {
      const [addonId, type, catalogId] = value.split("|");
      const catalog = catalogs.find((candidate) => candidate.addonId === addonId && candidate.catalog.type === type && candidate.catalog.id === catalogId);
      onChange({ ...source, title: catalog?.catalog.name || catalogId, addonCatalog: { addonId, manifestId: catalog?.manifestId, type, catalogId } });
    }} options={catalogs.map((catalog) => ({ value: `${catalog.addonId}|${catalog.catalog.type}|${catalog.catalog.id}`, label: `${catalog.manifestId} · ${catalog.catalog.name || catalog.catalog.id}` }))} /></div></label>}
    {tmdb && <>
      <div className="form-grid form-grid--three tmdb-source-fields">
        <label className="field"><span>{translate("admin.collections.sources.sourceType")}</span><div><Select value={tmdb.sourceType} onChange={(value) => {
          const sourceType = value as NonNullable<CollectionSource["tmdb"]>["sourceType"];
          const mediaType = fixedTMDBMediaType(sourceType) ?? tmdb.mediaType;
          onChange({ ...source, title: tmdbLabel(sourceType), tmdb: { ...tmdb, sourceType, tmdbId: undefined, mediaType } });
        }} options={[
          { value: "list", label: translate("admin.collections.tmdb.sourceTypes.publicList") },
          { value: "company", label: translate("admin.collections.tmdb.sourceTypes.productionCompany") },
          { value: "network", label: translate("admin.collections.tmdb.sourceTypes.network") },
          { value: "collection", label: translate("admin.collections.tmdb.sourceTypes.movieCollection") },
          { value: "person", label: translate("admin.collections.tmdb.sourceTypes.personCredits") },
          { value: "director", label: translate("admin.collections.tmdb.sourceTypes.directorCredits") },
          { value: "discover", label: translate("admin.collections.tmdb.sourceTypes.customDiscover") },
        ]} /></div></label>
        {tmdb.sourceType !== "discover" && <TMDBReferenceField key={tmdb.sourceType} sourceType={tmdb.sourceType} tmdbId={tmdb.tmdbId} onChange={(tmdbId) => updateTMDB({ tmdbId })} />}
        <label className="field"><span>{translate("admin.collections.sources.type")}</span><div><Select value={fixedMediaType ?? tmdb.mediaType} disabled={fixedMediaType !== undefined} onChange={(value) => updateTMDB({ mediaType: value as "movie" | "series" | "both" })} options={fixedMediaType
          ? [{ value: fixedMediaType, label: translate(fixedMediaType === "movie" ? "admin.collections.mediaTypes.movie" : "admin.collections.mediaTypes.series") }]
          : [{ value: "movie", label: translate("admin.collections.mediaTypes.movie") }, { value: "series", label: translate("admin.collections.mediaTypes.series") }, { value: "both", label: translate("admin.collections.mediaTypes.both") }]} /></div></label>
        <label className="field"><span>{translate("admin.collections.sources.sortBy")}</span><div><Select value={tmdb.sort} onChange={(value) => updateTMDB({ sort: value })} options={[
          { value: "popularity.desc", label: translate("admin.collections.sort.popularity") },
          { value: "vote_average.desc", label: translate("admin.collections.sort.rating") },
          { value: "vote_count.desc", label: translate("admin.collections.sort.voteCount") },
          { value: "release_date.desc", label: translate("admin.collections.sort.releaseDate") },
          { value: "first_air_date.desc", label: translate("admin.collections.sort.firstAirDate") },
          { value: "original", label: translate("admin.collections.sort.originalOrder") },
        ]} /></div></label>
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
      <label className="field"><span>{translate("admin.collections.trakt.mediaType")}</span><div><Select value={source.trakt.mediaType} onChange={(value) => onChange({ ...source, trakt: { ...source.trakt!, mediaType: value as "movie" | "series" } })} options={[{ value: "movie", label: translate("admin.collections.mediaTypes.movies") }, { value: "series", label: translate("admin.collections.mediaTypes.series") }]} /></div></label>
      <label className="field"><span>{translate("admin.collections.trakt.sort")}</span><div><Select value={source.trakt.sortBy} onChange={(value) => onChange({ ...source, trakt: { ...source.trakt!, sortBy: value } })} options={[{ value: "rank", label: translate("admin.collections.sort.rank") }, { value: "added", label: translate("admin.collections.sort.added") }, { value: "title", label: translate("admin.collections.sort.title") }, { value: "released", label: translate("admin.collections.sort.released") }, { value: "popularity", label: translate("admin.collections.sort.popularity") }, { value: "votes", label: translate("admin.collections.sort.votes") }]} /></div></label>
    </div>}
    {source.mdblist && <div className="form-grid form-grid--three">
      <label className="field"><span>MDBList ID</span><div><input type="number" min={1} value={source.mdblist.listId} onChange={(event) => onChange({ ...source, mdblist: { ...source.mdblist!, listId: Number(event.target.value) } })} /></div></label>
      <label className="field"><span>{translate("admin.collections.trakt.mediaType")}</span><div><Select value={source.mdblist.mediaType} onChange={(value) => onChange({ ...source, mdblist: { ...source.mdblist!, mediaType: value as "movie" | "series" } })} options={[{ value: "movie", label: translate("admin.collections.mediaTypes.movies") }, { value: "series", label: translate("admin.collections.mediaTypes.series") }]} /></div></label>
      <label className="field"><span>{translate("admin.collections.trakt.sort")}</span><div><Select value={source.mdblist.sort} onChange={(value) => onChange({ ...source, mdblist: { ...source.mdblist!, sort: value } })} options={[{ value: "rank", label: translate("admin.collections.sort.rank") }, { value: "added", label: translate("admin.collections.sort.added") }, { value: "title", label: translate("admin.collections.sort.title") }, { value: "released", label: translate("admin.collections.sort.released") }, { value: "tmdbpopular", label: translate("admin.collections.sort.popularity") }, { value: "score", label: translate("admin.collections.sort.rating") }, { value: "imdbvotes", label: translate("admin.collections.sort.votes") }]} /></div></label>
      <label className="field"><span>{translate("media.episodeOrder.label")}</span><div><Select value={source.mdblist.order} onChange={(value) => onChange({ ...source, mdblist: { ...source.mdblist!, order: value as "asc" | "desc" } })} options={[{ value: "asc", label: "↑ ASC" }, { value: "desc", label: "↓ DESC" }]} /></div></label>
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

const metadataRefreshIntervals: MetadataRefreshScheduleInput["intervalHours"][] = [6, 12, 24, 168];

const operationActionCards: Array<{
  action: OperationAction;
  icon: typeof Database;
  destructive: boolean;
  titleKey: TranslationKey;
  descriptionKey: TranslationKey;
  scopeKey: TranslationKey;
}> = [
  { action: "fetch-missing-metadata", icon: Sparkles, destructive: false, titleKey: "admin.operations.actions.metadata.title", descriptionKey: "admin.operations.actions.metadata.description", scopeKey: "admin.operations.actions.metadata.scope" },
  { action: "run-housekeeping", icon: Wrench, destructive: false, titleKey: "admin.operations.actions.housekeeping.title", descriptionKey: "admin.operations.actions.housekeeping.description", scopeKey: "admin.operations.actions.housekeeping.scope" },
  { action: "clear-metadata-cache", icon: Database, destructive: true, titleKey: "admin.operations.actions.clearMetadata.title", descriptionKey: "admin.operations.actions.clearMetadata.description", scopeKey: "admin.operations.actions.clearMetadata.scope" },
  { action: "clear-stream-cache", icon: HardDrive, destructive: true, titleKey: "admin.operations.actions.clearStream.title", descriptionKey: "admin.operations.actions.clearStream.description", scopeKey: "admin.operations.actions.clearStream.scope" },
];

function metadataRefreshInput(schedule: OperationsOverview["metadataRefresh"]): MetadataRefreshScheduleInput {
  return {
    enabled: schedule.enabled,
    intervalHours: metadataRefreshIntervals.includes(schedule.intervalHours as MetadataRefreshScheduleInput["intervalHours"])
      ? schedule.intervalHours as MetadataRefreshScheduleInput["intervalHours"]
      : 24,
    language: schedule.language,
    batchSize: schedule.batchSize,
  };
}

function failedMetadataTitles(result: OperationRun["result"]["metadata"]): string[] {
  if (!result || !("failedTitles" in result) || !Array.isArray(result.failedTitles)) return [];
  return result.failedTitles.filter((title): title is string => typeof title === "string" && title.trim().length > 0);
}

function metadataFailedTitlesMessage(result: NonNullable<OperationRun["result"]["metadata"]>): string {
  const failedTitles = failedMetadataTitles(result);
  return failedTitles.length > 0 ? ` ${translate("admin.operations.notifications.metadataFailedTitles", { titles: failedTitles.join(", ") })}` : "";
}

function metadataResultMessage(result: NonNullable<OperationRun["result"]["metadata"]>): string {
  return `${translate("admin.operations.notifications.metadataResult", { candidates: result.candidates, refreshed: result.refreshed, failed: result.failed })}${metadataFailedTitlesMessage(result)}`;
}
function OperationsAdmin() {
  const [overview, setOverview] = useState<OperationsOverview | null>(null);
  const [activity, setActivity] = useState<PlaybackActivity | null>(null);
  const [streamAvailable, setStreamAvailable] = useState(false);
  const [schedule, setSchedule] = useState<MetadataRefreshScheduleInput>({ enabled: false, intervalHours: 24, language: "en", batchSize: 25 });
  const [savedSchedule, setSavedSchedule] = useState<MetadataRefreshScheduleInput>({ enabled: false, intervalHours: 24, language: "en", batchSize: 25 });
  const [maintenance, setMaintenance] = useState<MaintenanceSettings>({ enabled: false, message: null });
  const [savedMaintenance, setSavedMaintenance] = useState<MaintenanceSettings>({ enabled: false, message: null });
  const [lastRuns, setLastRuns] = useState<Partial<Record<OperationAction, OperationRun>>>({});
  const [confirmAction, setConfirmAction] = useState<OperationAction | null>(null);
  const [runningAction, setRunningAction] = useState<OperationAction | null>(null);
  const runningActionRef = useRef<OperationAction | null>(null);
  const [metadataRequestFailed, setMetadataRequestFailed] = useState(false);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [savingSchedule, setSavingSchedule] = useState(false);
  const [savingMaintenance, setSavingMaintenance] = useState(false);
  const [error, setError] = useState("");
  const scheduleDirty = JSON.stringify(schedule) !== JSON.stringify(savedSchedule);
  const maintenanceDirty = maintenance.enabled !== savedMaintenance.enabled || maintenance.message !== savedMaintenance.message;
  const scheduleValid = schedule.language.trim().length > 0 && Number.isInteger(schedule.batchSize) && schedule.batchSize >= 1 && schedule.batchSize <= 100;

  async function load(silent = false) {
    if (!silent) setRefreshing(true);
    try {
      const playbackRequest = api.playbackActivity().then((value) => ({ value })).catch(() => ({ value: null }));
      const [nextOverview, nextMaintenance, playback] = await Promise.all([api.operations(), api.maintenanceSettings(), playbackRequest]);
      const nextSchedule = metadataRefreshInput(nextOverview.metadataRefresh);
      setOverview(nextOverview);
      setActivity(playback.value);
      setStreamAvailable(playback.value !== null);
      setSchedule(nextSchedule);
      setSavedSchedule(nextSchedule);
      setMaintenance(nextMaintenance);
      setSavedMaintenance(nextMaintenance);
      setError("");
    } catch (cause) {
      setError(notifyError(cause, translate("admin.operations.errors.load"), translate("admin.operations.errors.loadTitle")));
    } finally {
      setLoading(false);
      if (!silent) setRefreshing(false);
    }
  }

  useEffect(() => {
    void load(true);
    let active = true;
    const interval = window.setInterval(() => {
      void api.playbackActivity().then((value) => {
        if (active) {
          setActivity(value);
          setStreamAvailable(true);
        }
      }).catch(() => {
        if (active) setStreamAvailable(false);
      });
    }, 5_000);
    return () => {
      active = false;
      window.clearInterval(interval);
    };
  }, []);

  async function saveSchedule() {
    if (!scheduleDirty || !scheduleValid) return;
    setSavingSchedule(true);
    setError("");
    try {
      const input = { ...schedule, language: schedule.language.trim() };
      const updated = await api.updateMetadataRefreshSchedule(input);
      const nextSchedule = metadataRefreshInput(updated);
      setSchedule(nextSchedule);
      setSavedSchedule(nextSchedule);
      setOverview((current) => current ? { ...current, metadataRefresh: updated } : current);
      notifySuccess(translate("admin.operations.schedule.savedMessage"), translate("admin.operations.schedule.savedTitle"));
    } catch (cause) {
      setError(notifyError(cause, translate("admin.operations.errors.saveSchedule"), translate("admin.operations.errors.saveScheduleTitle")));
    } finally {
      setSavingSchedule(false);
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

  function operationResultMessage(run: OperationRun): string {
    if (run.result.metadata) return metadataResultMessage(run.result.metadata);
    if (run.result.metadataCache) return translate("admin.operations.notifications.metadataCacheResult", { entriesDeleted: run.result.metadataCache.entriesDeleted });
    if (run.result.playback) return translate("admin.operations.notifications.playbackResult", { sessionsRemoved: run.result.playback.sessionsRemoved, jobsStopped: run.result.playback.jobsStopped, storageBytes: run.result.playback.storageBytes });
    return translate("admin.operations.notifications.completed");
  }

  async function runAction(action: OperationAction) {
    if (runningActionRef.current) return;
    runningActionRef.current = action;
    setRunningAction(action);
    setError("");
    if (action === "fetch-missing-metadata") setMetadataRequestFailed(false);
    try {
      const run = await api.runOperation(action);
      setLastRuns((current) => ({ ...current, [action]: run }));
      const title = translate(run.status === "failed" ? "admin.operations.notifications.failedTitle" : run.status === "partial" ? "admin.operations.notifications.partialTitle" : "admin.operations.notifications.succeededTitle");
      if (run.status === "failed") notifyErrorMessage(operationResultMessage(run), title);
      else if (run.status === "partial") notifyWarning(operationResultMessage(run), title);
      else notifySuccess(operationResultMessage(run), title);
      const [nextOverview, nextActivity] = await Promise.all([
        api.operations().catch(() => null),
        api.playbackActivity().catch(() => null),
      ]);
      if (nextOverview) {
        setOverview(nextOverview);
        const nextSchedule = metadataRefreshInput(nextOverview.metadataRefresh);
        setSchedule(nextSchedule);
        setSavedSchedule(nextSchedule);
      }
      if (nextActivity) {
        setActivity(nextActivity);
        setStreamAvailable(true);
      }
    } catch (cause) {
      if (action === "fetch-missing-metadata") {
        setMetadataRequestFailed(true);
        notifyErrorMessage(translate("admin.operations.notifications.metadataFailedMessage"), translate("admin.operations.notifications.failedTitle"));
      } else {
        setError(notifyError(cause, translate("admin.operations.errors.runAction"), translate("admin.operations.errors.runActionTitle")));
      }
    } finally {
      runningActionRef.current = null;
      setRunningAction(null);
    }
  }

  async function runConfirmedAction() {
    if (!confirmAction) return;
    await runAction(confirmAction);
    setConfirmAction(null);
  }

  if (loading) return <div className="admin-section operations-admin">
    <div className="admin-section__header"><div><span>{translate("admin.operations.eyebrow")}</span><h2>{translate("admin.operations.title")}</h2><p>{translate("admin.operations.description")}</p></div></div>
    <div className="admin-loading-state" role="status"><LoaderCircle className="spin" /><strong>{translate("admin.operations.loadingTitle")}</strong><span>{translate("admin.operations.loadingDescription")}</span></div>
  </div>;

  if (!overview) return <div className="admin-section operations-admin">
    <div className="admin-section__header"><div><span>{translate("admin.operations.eyebrow")}</span><h2>{translate("admin.operations.title")}</h2><p>{translate("admin.operations.description")}</p></div><Button variant="secondary" onClick={() => void load()} loading={refreshing}><RefreshCw size={16} /> {translate("common.actions.tryAgain")}</Button></div>
    {error && <Notice>{error}</Notice>}
  </div>;

  const cache = overview.metadataCache;
  const refresh = overview.metadataRefresh;
  const stream = activity?.summary;
  return <div className="admin-section operations-admin">
    <div className="admin-section__header"><div><span>{translate("admin.operations.eyebrow")}</span><h2>{translate("admin.operations.title")}</h2><p>{translate("admin.operations.description")}</p></div><div className="admin-section__actions"><Button variant="secondary" onClick={() => void load()} loading={refreshing} disabled={scheduleDirty || maintenanceDirty || Boolean(runningAction) || savingSchedule || savingMaintenance}><RefreshCw size={16} /> {translate("common.actions.refresh")}</Button></div></div>
    {error && <Notice>{error}</Notice>}

    <section className="operations-panel" aria-labelledby="operations-metadata-title">
      <header><div><span>{translate("admin.operations.metadata.eyebrow")}</span><h3 id="operations-metadata-title">{translate("admin.operations.metadata.title")}</h3><p>{translate("admin.operations.metadata.description")}</p></div><small>{translate("admin.operations.metadata.housekeepingInterval", { count: overview.housekeepingIntervalMinutes })}</small></header>
      <div className="operations-metrics" aria-label={translate("admin.operations.metadata.metricsLabel")}>
        <OperationMetric icon={<Database />} value={cache.entries} label={translate("admin.operations.metadata.entries")} />
        <OperationMetric icon={<Check />} value={cache.freshEntries} label={translate("admin.operations.metadata.fresh")} tone="success" />
        <OperationMetric icon={<Clock3 />} value={cache.expiredEntries} label={translate("admin.operations.metadata.expired")} tone="warning" />
        <OperationMetric icon={<Film />} value={cache.rootTitles} label={translate("admin.operations.metadata.rootTitles")} />
        <OperationMetric icon={<RefreshCw />} value={cache.missingTitles} label={translate("admin.operations.metadata.missingTitles")} tone="warning" />
        <OperationMetric icon={<ImagePlus />} value={cache.artworkSnapshots} label={translate("admin.operations.metadata.artworkSnapshots")} />
      </div>
    </section>

    <section className="operations-panel operations-resources" aria-labelledby="operations-resources-title">
      <header><div><span>{translate("admin.operations.resources.eyebrow")}</span><h3 id="operations-resources-title">{translate("admin.operations.resources.title")}</h3><p>{translate("admin.operations.resources.description")}</p></div></header>
      <div className="operations-resource-grid" aria-label={translate("admin.operations.resources.label")}>
        <OperationAggregate
          icon={<Database />}
          title={translate("admin.operations.resources.database.title")}
          values={[
            [translate("admin.operations.resources.database.acquired"), overview.postgresqlPool.acquired],
            [translate("admin.operations.resources.database.idle"), overview.postgresqlPool.idle],
            [translate("admin.operations.resources.database.total"), overview.postgresqlPool.total],
            [translate("admin.operations.resources.database.maximum"), overview.postgresqlPool.max],
            [translate("admin.operations.resources.database.waits"), overview.postgresqlPool.waitCount],
            [translate("admin.operations.resources.database.waitTime"), formatOperationsDuration(overview.postgresqlPool.waitDurationMilliseconds)],
          ]}
        />
        <OperationAggregate
          icon={<Send />}
          title={translate("admin.operations.resources.tracking.title")}
          values={[
            [translate("admin.operations.resources.tracking.pending"), overview.trackingOutbox.pending],
            [translate("admin.operations.resources.tracking.due"), overview.trackingOutbox.due],
            [translate("admin.operations.resources.tracking.oldest"), formatOperationsDuration(overview.trackingOutbox.oldestAgeSeconds * 1_000)],
          ]}
        />
        <OperationAggregate
          icon={<Boxes />}
          title={translate("admin.operations.resources.addons.title")}
          values={[
            [translate("admin.operations.resources.addons.total"), overview.addons.total],
            [translate("admin.operations.resources.addons.enabled"), overview.addons.enabled],
            [translate("admin.operations.resources.addons.latestUpdate"), overview.addons.latestUpdatedAt ? formatOperationsAge(overview.addons.latestUpdatedAt) : translate("admin.operations.never")],
          ]}
        />
        <OperationAggregate
          icon={<Radio />}
          title={translate("admin.operations.resources.playback.title")}
          values={[
            [translate("admin.operations.resources.playback.active"), overview.playback.active],
            [translate("admin.operations.resources.playback.transcoding"), overview.playback.transcoding],
          ]}
        />
      </div>
    </section>

    <section className="operations-panel operations-schedule" aria-labelledby="operations-schedule-title">
      <header><div><span>{translate("admin.operations.schedule.eyebrow")}</span><h3 id="operations-schedule-title">{translate("admin.operations.schedule.title")}</h3><p>{translate("admin.operations.schedule.description")}</p></div><span className={`settings-save-state ${scheduleDirty ? "is-dirty" : "is-saved"}`} role="status" aria-live="polite">{savingSchedule ? <><LoaderCircle size={14} className="spin" /> {translate("common.status.saving")}</> : scheduleDirty ? <><Save size={14} /> {translate("common.status.unsavedChanges")}</> : <><Check size={14} /> {translate("common.status.saved")}</>}</span></header>
      <div className="operations-schedule__form">
        <div className="setting-control setting-control--toggle">
          <label className="toggle-field"><input type="checkbox" checked={schedule.enabled} disabled={savingSchedule || Boolean(runningAction)} onChange={(event) => setSchedule((current) => ({ ...current, enabled: event.target.checked }))} /><span><i /><div><strong>{translate("admin.operations.schedule.enabled")}</strong><small>{translate("admin.operations.schedule.enabledDescription")}</small></div></span></label>
        </div>
        <label className="field"><span>{translate("admin.operations.schedule.interval")}</span><div><Select value={String(schedule.intervalHours)} disabled={savingSchedule || Boolean(runningAction)} onChange={(value) => setSchedule((current) => ({ ...current, intervalHours: Number(value) as MetadataRefreshScheduleInput["intervalHours"] }))} options={metadataRefreshIntervals.map((hours) => ({ value: String(hours), label: translate(hours === 6 ? "admin.operations.schedule.interval6" : hours === 12 ? "admin.operations.schedule.interval12" : hours === 24 ? "admin.operations.schedule.interval24" : "admin.operations.schedule.interval168") }))} /></div></label>
        <label className="field"><span>{translate("admin.operations.schedule.language")}</span><div><Languages size={17} aria-hidden="true" /><input value={schedule.language} disabled={savingSchedule || Boolean(runningAction)} maxLength={35} autoComplete="off" spellCheck={false} placeholder="en" aria-invalid={schedule.language.trim().length === 0} aria-describedby="operations-language-help" onChange={(event) => setSchedule((current) => ({ ...current, language: event.target.value }))} /></div><small id="operations-language-help">{translate("admin.operations.schedule.languageHelp")}</small></label>
        <label className="field"><span>{translate("admin.operations.schedule.batchSize")}</span><div><input type="number" value={schedule.batchSize} min={1} max={100} step={1} disabled={savingSchedule || Boolean(runningAction)} aria-invalid={!Number.isInteger(schedule.batchSize) || schedule.batchSize < 1 || schedule.batchSize > 100} aria-describedby="operations-batch-help" onChange={(event) => setSchedule((current) => ({ ...current, batchSize: Number(event.target.value) }))} /></div><small id="operations-batch-help">{translate("admin.operations.schedule.batchSizeHelp")}</small></label>
      </div>
      <div className="operations-schedule__state" aria-label={translate("admin.operations.schedule.stateLabel")}>
        <OperationState label={translate("admin.operations.schedule.nextRun")} value={refresh.nextRunAt ? formatOperationsDate(refresh.nextRunAt) : translate(refresh.enabled ? "admin.operations.schedule.pending" : "admin.operations.schedule.disabled")} />
        <OperationState label={translate("admin.operations.schedule.lastStarted")} value={refresh.lastStartedAt ? formatOperationsDate(refresh.lastStartedAt) : translate("admin.operations.never")} />
        <OperationState label={translate("admin.operations.schedule.lastCompleted")} value={refresh.lastCompletedAt ? formatOperationsDate(refresh.lastCompletedAt) : translate("admin.operations.never")} />
        <OperationState label={translate("admin.operations.schedule.lastStatus")} value={translate(refresh.lastStatus ? `admin.operations.status.${refresh.lastStatus}` as TranslationKey : "admin.operations.never")} tone={refresh.lastStatus ?? ""} />
      </div>
      {refresh.lastResult && <p className="operations-result" role="status">{translate("admin.operations.schedule.lastResult", { candidates: refresh.lastResult.candidates, refreshed: refresh.lastResult.refreshed, failed: refresh.lastResult.failed })}{metadataFailedTitlesMessage(refresh.lastResult)}</p>}
      <footer><div><strong>{translate(schedule.enabled ? "admin.operations.schedule.enabledSummary" : "admin.operations.schedule.disabledSummary")}</strong><small>{translate("admin.operations.schedule.durableDescription")}</small></div><Button variant="secondary" disabled={!scheduleDirty || savingSchedule} onClick={() => setSchedule(savedSchedule)}>{translate("common.actions.discardChanges")}</Button><Button loading={savingSchedule} disabled={!scheduleDirty || !scheduleValid || Boolean(runningAction)} onClick={() => void saveSchedule()}><Save size={17} /> {translate("admin.operations.schedule.save")}</Button></footer>
    </section>

    <section className="operations-panel" aria-labelledby="operations-stream-title">
      <header><div><span>{translate("admin.operations.stream.eyebrow")}</span><h3 id="operations-stream-title">{translate("admin.operations.stream.title")}</h3><p>{translate("admin.operations.stream.description")}</p></div><small>{translate(streamAvailable ? "admin.operations.stream.live" : "admin.operations.stream.unavailable")}</small></header>
      <div className="operations-metrics operations-metrics--stream" aria-label={translate("admin.operations.stream.metricsLabel")} aria-live="polite">
        <OperationMetric icon={<Radio />} value={stream?.activeSessions ?? 0} label={translate("admin.operations.stream.sessions")} tone="success" />
        <OperationMetric icon={<Cpu />} value={stream?.activeJobs ?? 0} label={translate("admin.operations.stream.jobs")} />
        <OperationMetric icon={<HardDrive />} value={formatBytes(stream?.storageBytes ?? 0)} label={translate("admin.operations.stream.storage")} />
        <OperationMetric icon={<Server />} value={`${stream?.processingSlots ?? 0} / ${stream?.processingLimit ?? 0}`} label={translate("admin.operations.stream.processing")} />
      </div>
    </section>


    <section className="operations-actions" aria-labelledby="operations-actions-title">
      <header><span>{translate("admin.operations.actions.eyebrow")}</span><h3 id="operations-actions-title">{translate("admin.operations.actions.title")}</h3><p>{translate("admin.operations.actions.description")}</p></header>
      <div className="operations-action-grid">{operationActionCards.map((card) => {
        const Icon = card.icon;
        const lastRun = lastRuns[card.action];
        return <article className={`operation-action-card ${card.destructive ? "is-destructive" : ""}`} key={card.action}>
          <header><span><Icon aria-hidden="true" /></span><div><h4>{translate(card.titleKey)}</h4><p>{translate(card.descriptionKey)}</p></div></header>
          <div className="operation-action-card__scope"><Shield size={15} aria-hidden="true" /><span>{translate(card.scopeKey)}</span></div>
          <div className="operation-action-card__feedback" aria-live="polite">
            {lastRun && !(card.action === "fetch-missing-metadata" && metadataRequestFailed) && (card.action === "fetch-missing-metadata" && lastRun.result.metadata
              ? lastRun.status !== "succeeded" && <Notice tone={lastRun.status === "failed" ? "error" : "warning"}><span role="status"><strong>{metadataResultMessage(lastRun.result.metadata)}</strong>{lastRun.status === "failed" ? ` ${translate("admin.operations.notifications.metadataFailedMessage")}` : ` ${translate("admin.operations.notifications.metadataPartialMessage")}`}</span>{lastRun.status === "failed" && <Button variant="secondary" aria-label={translate("admin.operations.actions.metadata.retry")} loading={runningAction === card.action} disabled={Boolean(runningAction)} onClick={() => void runAction(card.action)}><RefreshCw size={16} /> {translate("admin.operations.actions.metadata.retry")}</Button>}</Notice>
              : lastRun.status !== "succeeded" && <small className={`operation-action-card__result is-${lastRun.status}`} role="status">{translate(`admin.operations.status.${lastRun.status}` as TranslationKey)} · {formatOperationsDate(lastRun.completedAt)}</small>)}
            {card.action === "fetch-missing-metadata" && metadataRequestFailed && <Notice><span role="alert">{translate("admin.operations.notifications.metadataFailedMessage")}</span><Button variant="secondary" aria-label={translate("admin.operations.actions.metadata.retry")} loading={runningAction === card.action} disabled={Boolean(runningAction)} onClick={() => void runAction(card.action)}><RefreshCw size={16} /> {translate("admin.operations.actions.metadata.retry")}</Button></Notice>}
          </div>
          <Button variant={card.destructive ? "danger" : "secondary"} aria-label={translate("admin.operations.actions.runNamed", { action: translate(card.titleKey) })} loading={runningAction === card.action} disabled={Boolean(runningAction) || savingSchedule || savingMaintenance || (card.action === "fetch-missing-metadata" && scheduleDirty)} onClick={() => card.destructive ? setConfirmAction(card.action) : void runAction(card.action)}>{card.destructive ? <Trash2 size={16} /> : <RefreshCw size={16} />} {translate(runningAction === card.action ? "admin.operations.actions.running" : "admin.operations.actions.run")}</Button>
        </article>;
      })}</div>
    </section>
    <DeviceNotificationsOperationsCard />

    <MaintenanceCard values={maintenance} onChange={setMaintenance} onSave={() => void saveMaintenance()} onReset={() => setMaintenance(savedMaintenance)} saving={savingMaintenance} dirty={maintenanceDirty} />

    {confirmAction && <ConfirmDialog title={translate(confirmAction === "clear-metadata-cache" ? "admin.operations.confirm.metadataTitle" : "admin.operations.confirm.streamTitle")} description={translate(confirmAction === "clear-metadata-cache" ? "admin.operations.confirm.metadataDescription" : "admin.operations.confirm.streamDescription")} confirmLabel={translate(confirmAction === "clear-metadata-cache" ? "admin.operations.confirm.metadataConfirm" : "admin.operations.confirm.streamConfirm")} loading={runningAction === confirmAction} onConfirm={() => void runConfirmedAction()} onCancel={() => setConfirmAction(null)} />}
  </div>;
}

function OperationMetric({ icon, value, label, tone = "" }: { icon: React.ReactNode; value: string | number; label: string; tone?: "success" | "warning" | "" }) {
  return <article className={`operation-metric ${tone ? `is-${tone}` : ""}`}><span aria-hidden="true">{icon}</span><div><strong>{value}</strong><small>{label}</small></div></article>;
}

function OperationAggregate({ icon, title, values }: { icon: React.ReactNode; title: string; values: ReadonlyArray<readonly [string, string | number]> }) {
  return <article className="operation-aggregate" aria-label={title}>
    <header><span aria-hidden="true">{icon}</span><h4>{title}</h4></header>
    <dl>{values.map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}</dl>
  </article>;
}

function OperationState({ label, value, tone = "" }: { label: string; value: string; tone?: "succeeded" | "partial" | "failed" | "" }) {
  return <div className={`operation-state ${tone ? `is-${tone}` : ""}`}><small>{label}</small><strong>{value}</strong></div>;
}

function formatOperationsDate(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(date);
}

function formatOperationsDuration(milliseconds: number): string {
  const duration = Math.max(0, milliseconds);
  const [value, unit] = duration >= 86_400_000
    ? [duration / 86_400_000, "day"] as const
    : duration >= 3_600_000
      ? [duration / 3_600_000, "hour"] as const
      : duration >= 60_000
        ? [duration / 60_000, "minute"] as const
        : duration >= 1_000
          ? [duration / 1_000, "second"] as const
          : [duration, "millisecond"] as const;
  return new Intl.NumberFormat(locale, { style: "unit", unit, unitDisplay: "short", maximumFractionDigits: value < 10 ? 1 : 0 }).format(value);
}

function formatOperationsAge(value: string): string {
  const timestamp = new Date(value).getTime();
  return Number.isNaN(timestamp) ? translate("admin.operations.never") : formatOperationsDuration(Date.now() - timestamp);
}

function ActivityAdmin() {
  const [activity, setActivity] = useState<PlaybackActivity | null>(null);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [stopping, setStopping] = useState(false);
  const [selectedSession, setSelectedSession] = useState<PlaybackActivitySession | null>(null);
  const [error, setError] = useState("");
  const activityRootRef = useRef<HTMLDivElement>(null);
  const focusReturnRef = useRef<HTMLButtonElement>(null);
  const pendingStoppedSessionRef = useRef<string | null>(null);

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
  const firstActivityControl = useCallback((): HTMLButtonElement | null => {
    const root = activityRootRef.current;
    return root?.querySelector<HTMLButtonElement>(".activity-session .button")
      ?? root?.querySelector<HTMLButtonElement>(".admin-section__actions .button")
      ?? null;
  }, []);

  useEffect(() => {
    const stoppedSessionID = pendingStoppedSessionRef.current;
    if (!stoppedSessionID || selectedSession || !activity || activity.sessions.some((session) => session.id === stoppedSessionID)) return;

    const frame = window.requestAnimationFrame(() => {
      if (pendingStoppedSessionRef.current !== stoppedSessionID) return;
      pendingStoppedSessionRef.current = null;
      focusReturnRef.current = null;
      const target = firstActivityControl();
      target?.focus();
      target?.scrollIntoView({ block: "nearest", inline: "nearest" });
    });
    return () => window.cancelAnimationFrame(frame);
  }, [activity, firstActivityControl, selectedSession]);


  function restoreActivityFocus() {
    window.requestAnimationFrame(() => {
      const returnTarget = focusReturnRef.current;
      const fallback = firstActivityControl();
      const target = returnTarget?.isConnected ? returnTarget : fallback;
      target?.focus();
      target?.scrollIntoView({ block: "nearest", inline: "nearest" });
    });
  }

  function closeSessionDialog() {
    setSelectedSession(null);
    restoreActivityFocus();
  }


  async function stopSession() {
    if (!selectedSession) return;
    const stoppedSession = selectedSession;
    setStopping(true);
    try {
      await api.stopPlaybackActivitySession(stoppedSession.id);
      notifySuccess(translate("admin.activity.notifications.stoppedMessage", { title: stoppedSession.title }), translate("admin.activity.notifications.stoppedTitle"));
      pendingStoppedSessionRef.current = stoppedSession.id;
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
  const jobsBySession = new Map<string, PlaybackActivity["jobs"][number]>();
  for (const job of activity?.jobs ?? []) {
    if (!job.prewarming && job.sessionId) jobsBySession.set(job.sessionId, job);
  }
  return <div className="admin-section activity-admin" ref={activityRootRef}>
    <div className="admin-section__header"><div><span>{translate("admin.activity.eyebrow")}</span><h2>{translate("admin.activity.title")}</h2><p>{translate("admin.activity.description")}</p></div><div className="admin-section__actions"><Button variant="secondary" onClick={() => void load()} loading={refreshing}><RefreshCw size={16} /> {translate("common.actions.refresh")}</Button></div></div>
    {error && <Notice>{error}</Notice>}
    <div className="activity-overview" aria-label={translate("admin.activity.overview.label")} aria-live="polite">
      <ActivityMetric icon={<Radio />} label={translate("admin.activity.overview.sessions")} value={String(summary?.activeSessions ?? 0)} detail={translate((summary?.activeJobs ?? 0) === 1 ? "admin.activity.overview.mediaJobsOne" : "admin.activity.overview.mediaJobsMany", { count: summary?.activeJobs ?? 0 })} />
      <ActivityMetric icon={<Cpu />} label={translate("admin.activity.overview.processing")} value={`${summary?.processingSlots ?? 0} / ${summary?.processingLimit ?? 0}`} detail={activity ? `${translate("admin.activity.overview.ffmpegSlots")} · probe ${activity.diagnostics.pools?.probe?.active ?? 0}/${activity.diagnostics.pools?.probe?.limit ?? 0} · subtitle ${activity.diagnostics.pools?.subtitle?.active ?? 0}/${activity.diagnostics.pools?.subtitle?.limit ?? 0} · trickplay ${activity.diagnostics.pools?.trickplay?.active ?? 0}/${activity.diagnostics.pools?.trickplay?.limit ?? 0} · total ${activity.diagnostics.totals.started}/${activity.diagnostics.totals.succeeded}/${activity.diagnostics.totals.failed} · fallback ${activity.diagnostics.totals.softwareFallbacks}` : translate("admin.activity.overview.ffmpegSlots")} />
      <ActivityMetric icon={<HardDrive />} label={translate("admin.activity.overview.temporaryMedia")} value={formatBytes(summary?.storageBytes ?? 0)} detail={translate("admin.activity.overview.storageLimit", { limit: formatBytes(summary?.storageLimitBytes ?? 0) })} />
      <ActivityMetric icon={<Server />} label={translate("admin.activity.overview.encoder")} value={activity?.diagnostics.videoEncoder.toUpperCase() ?? translate("admin.activity.overview.unknownEncoder")} detail={activity ? `FFmpeg ${activity.diagnostics.ffmpegVersion || "unknown"} · ffprobe ${activity.diagnostics.ffprobeVersion || "unknown"} · ${activity.diagnostics.hardwareAcceleration || "unknown"} · ${activity.diagnostics.transcodeThreads || 0} threads · ${activity.diagnostics.maximumReadRate.toFixed(2)}× max · ${translate(activity.diagnostics.hardwareToneMap ? "admin.activity.overview.hardwareToneMapping" : "admin.activity.overview.softwareToneMapping")} (${activity.diagnostics.toneMapBackend.toUpperCase()})` : ""} />
      <ActivityMetric wide icon={<Film />} label={translate("admin.activity.overview.codecs")} value={translate("admin.activity.overview.preferredCodec", { codec: activity?.diagnostics.preferredVideoCodec.toUpperCase() ?? "AUTO" })} detail={activity ? `${translate("admin.activity.overview.encodeCodecs", { codecs: activity.diagnostics.encodeCodecs.length ? activity.diagnostics.encodeCodecs.map((codec) => codec.toUpperCase()).join(", ") : translate("admin.activity.overview.none") })} · ${translate("admin.activity.overview.decodeCodecs", { codecs: activity.diagnostics.decodeCodecs.length ? activity.diagnostics.decodeCodecs.map((codec) => codec.toUpperCase()).join(", ") : translate("admin.activity.overview.none") })} · ${translate("admin.activity.overview.qualityPreset", { preset: activity.diagnostics.qualityPreset.toUpperCase() })}` : ""} />
    </div>
    <section className="activity-panel">
      <header><div><span>{translate("admin.activity.sessions.eyebrow")}</span><h3>{translate("admin.activity.sessions.title")}</h3></div><small>{translate((summary?.activeSessions ?? 0) === 1 ? "admin.activity.sessions.activeCountOne" : "admin.activity.sessions.activeCountMany", { count: summary?.activeSessions ?? 0 })}</small></header>
      {activity?.sessions.length
        ? <div className="activity-session-list">{activity.sessions.map((session) => <article className="activity-session" key={session.id}>
          <ActivitySessionArtwork session={session} />
          <div className="activity-session__copy">
            <strong>{session.title}</strong>
            <ActivitySessionProviders session={session} />
            <span>{session.profile} · {session.username}</span>
            <small>{session.device} · {session.platform} · {activityModeLabel(session.mode)}</small>
            {session.decision && <ActivitySessionDecision decision={session.decision} />}
          </div>
          <div className="activity-session__controls">
            <div className="activity-session__time">
              <ActivitySessionProgress positionSeconds={session.positionSeconds} durationSeconds={session.durationSeconds} />
              <div className="activity-session__meta">
                <small>{activityAge(session.lastSeenAt)} · {translate("admin.activity.sessions.started", { age: activityAge(session.createdAt) })}</small>
                <ActivityJobProgress job={jobsBySession.get(session.id)} />
              </div>
            </div>
            <Button variant="danger" onClick={(event) => { focusReturnRef.current = event.currentTarget; setSelectedSession(session); }}><CircleStop size={16} />{translate("common.actions.stop")}</Button>
          </div>
        </article>)}</div>
        : <EmptyState icon={<Radio />} title={translate("admin.activity.sessions.emptyTitle")} description={translate("admin.activity.sessions.emptyDescription")} />}
    </section>
    <section className="activity-panel">
      <header><div><span>{translate("admin.activity.jobs.eyebrow")}</span><h3>{translate("admin.activity.jobs.title")}</h3></div><small>{translate((summary?.activeJobs ?? 0) === 1 ? "admin.activity.jobs.countOne" : "admin.activity.jobs.countMany", { count: summary?.activeJobs ?? 0 })}</small></header>
      {activity?.jobs.length
        ? <div className="activity-job-list">{activity.jobs.map((job, index) => <article className="activity-job" key={`${job.sessionId ?? "prewarm"}-${job.assetId}-${index}`}><span className={`activity-job__dot is-${job.state}`} aria-hidden="true" /><div><strong>{job.prewarming ? translate("admin.activity.jobs.preparingSource") : activityModeLabel(job.mode)}</strong><small>{job.assetId}{job.errorClass ? ` · ${job.errorClass}` : ""} · {translate("admin.activity.jobs.lastRequest", { age: activityAge(job.lastSeenAt) })}</small></div><ActivityJobProgress job={job} /></article>)}</div>
        : <EmptyState icon={<Cpu />} title={translate("admin.activity.jobs.emptyTitle")} description={translate("admin.activity.jobs.emptyDescription")} />}
    </section>
    {selectedSession && <ConfirmDialog title={translate("admin.activity.stop.title", { title: selectedSession.title })} description={translate("admin.activity.stop.description", { device: selectedSession.device })} confirmLabel={translate("admin.activity.stop.confirm")} loading={stopping} onConfirm={() => void stopSession()} onCancel={closeSessionDialog} />}
  </div>;
}

function ActivitySessionProgress({ positionSeconds, durationSeconds }: { positionSeconds: number; durationSeconds: number }) {
  const label = formatActivityProgress(positionSeconds, durationSeconds);
  if (!Number.isFinite(durationSeconds) || durationSeconds <= 0) {
    return <div className="activity-session__progress is-unavailable" role="status" aria-label={label}><strong>{label}</strong></div>;
  }
  const position = Number.isFinite(positionSeconds) ? Math.max(0, Math.min(positionSeconds, durationSeconds)) : 0;
  const percent = position / durationSeconds;
  const percentLabel = new Intl.NumberFormat(locale, { style: "percent", maximumFractionDigits: 0 }).format(percent);
  return <div className="activity-session__progress" role="status" aria-label={label}>
    <span><strong>{label}</strong><span aria-hidden="true">{percentLabel}</span></span>
    <progress max={durationSeconds} value={position} aria-label={label}>{percentLabel}</progress>
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

function ActivityMetric({ icon, label, value, detail, wide = false }: { icon: React.ReactNode; label: string; value: string; detail: string; wide?: boolean }) {
  return <article className={`activity-metric${wide ? " activity-metric--wide" : ""}`}><span aria-hidden="true">{icon}</span><div><small>{label}</small><strong>{value}</strong><span>{detail}</span></div></article>;
}
function ActivityJobProgress({ job }: { job?: PlaybackActivity["jobs"][number] }) {
  const progressPercent = job?.progressPercent;
  const speed = job?.speed;
  if (typeof progressPercent !== "number" || !Number.isFinite(progressPercent) || progressPercent < 0
    || typeof speed !== "number" || !Number.isFinite(speed) || speed < 0) return null;
  const startup = job?.startupDurationSeconds;
  const startupDetail = typeof startup === "number" && Number.isFinite(startup) && startup >= 0 ? ` · start ${startup.toFixed(2)}s` : "";
  return <span className="activity-progress-status" role="status">{Math.round(progressPercent)}% · {speed.toFixed(2)}×{startupDetail}</span>;
}

function ActivitySessionDecision({ decision }: { decision: NonNullable<PlaybackActivitySession["decision"]> }) {
  const reasonKeys = {
    direct_supported: "admin.activity.reasons.directSupported",
    remux_required: "admin.activity.reasons.remuxRequired",
    audio_transcode_required: "admin.activity.reasons.audioTranscodeRequired",
    video_transcode_required: "admin.activity.reasons.videoTranscodeRequired",
    subtitle_burn_required: "admin.activity.reasons.subtitleBurnRequired",
  } as const;
  const actionKeys = {
    copy: "admin.activity.actions.copy",
    transcode: "admin.activity.actions.transcode",
    external: "admin.activity.actions.external",
    burn: "admin.activity.actions.burn",
    none: "admin.activity.actions.none",
  } as const;
  const sourceVideo = decision.source?.videoCodec?.toUpperCase();
  const targetVideo = decision.target?.videoCodec?.toUpperCase() ?? (decision.videoAction === "copy" ? sourceVideo : undefined);
  const sourceAudio = decision.source?.audioCodec?.toUpperCase();
  const targetAudio = decision.target?.audioCodec?.toUpperCase() ?? (decision.audioAction === "copy" ? sourceAudio : undefined);
  const sourceHeight = decision.source?.height;
  const targetHeight = decision.target?.height ?? (decision.videoAction === "copy" ? sourceHeight : undefined);
  const reasonKey = reasonKeys[decision.reason];
  return <dl className="activity-session__decision">
    {reasonKey && <div className="activity-session__decision-reason"><dt className="visually-hidden">{translate("player.panel.diagnostics")}</dt><dd>{translate(reasonKey)}</dd></div>}
    <div><dt>{sourceVideo && targetVideo ? translate("admin.activity.details.video", { source: sourceVideo, target: targetVideo }) : translate("player.diagnostics.video")}</dt><dd>{translate(actionKeys[decision.videoAction])}</dd></div>
    <div><dt>{sourceAudio && targetAudio ? translate("admin.activity.details.audio", { source: sourceAudio, target: targetAudio }) : translate("player.diagnostics.audio")}</dt><dd>{translate(actionKeys[decision.audioAction])}</dd></div>
    {sourceHeight && targetHeight && <div><dt>{translate("admin.activity.details.resolution", { source: `${sourceHeight}p`, target: `${targetHeight}p` })}</dt></div>}
    {decision.target?.videoBitrateKbps && <div><dt>{translate("admin.activity.details.bitrate", { bitrate: decision.target.videoBitrateKbps.toLocaleString() })}</dt></div>}
    <div><dt>{translate("player.panel.subtitles")}</dt><dd>{translate(actionKeys[decision.subtitleAction])}</dd></div>
    {decision.toneMapping && <div><dt>{translate("admin.activity.details.toneMapping")}</dt></div>}
  </dl>;
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
  const duration = Math.max(1, Math.ceil(durationSeconds));
  const position = Number.isFinite(positionSeconds) ? Math.max(0, Math.min(Math.floor(positionSeconds), duration)) : 0;
  return `${formatActivityTime(position)} / ${formatActivityTime(duration)}`;
}

function formatActivityTime(totalSeconds: number): string {
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  const parts: string[] = [];
  if (hours > 0) parts.push(new Intl.NumberFormat(locale, { style: "unit", unit: "hour", unitDisplay: "narrow" }).format(hours));
  if (minutes > 0) parts.push(new Intl.NumberFormat(locale, { style: "unit", unit: "minute", unitDisplay: "narrow" }).format(minutes));
  if (seconds > 0 || parts.length === 0) parts.push(new Intl.NumberFormat(locale, { style: "unit", unit: "second", unitDisplay: "narrow" }).format(seconds));
  return parts.join(" ");
}

const rivuneSettingDefaults = {
  interfaceLanguage: "en",
  theme: "system",
  maximumResolution: "auto",
  maximumCastMembers: 20,
  maximumDirectTitles: 20,
  preferDirectPlay: true,
  allowTranscoding: true,
  jellyfinEnabled: false,
  jellyfinDebug: false,
  timezone: "UTC",
  hardwareAcceleration: "auto",
  transcodeMaxBitrateKbps: 12000,
  preferredTranscodeVideoCodec: "auto",
  transcodeQualityPreset: "balanced",
  transcodeConcurrency: 4,
  mediaMaxStorageMB: 20480,
  artworkMaxStorageMB: 20480,
  transcoding: "inherit",
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

type DeviceNotificationValues = Pick<SettingsValues, "notificationsEnabled" | "notificationDurationSeconds" | "notificationPollIntervalSeconds">;

function deviceNotificationValues(values: SettingsValues): DeviceNotificationValues {
  return {
    notificationsEnabled: values.notificationsEnabled,
    notificationDurationSeconds: values.notificationDurationSeconds,
    notificationPollIntervalSeconds: values.notificationPollIntervalSeconds,
  };
}

function isDeviceNotificationSetting(key: string): boolean {
  return key === "notificationsEnabled" || key === "notificationDurationSeconds" || key === "notificationPollIntervalSeconds";
}

function preferenceValues(values: SettingsValues, includeAdministratorPolicy = false): SettingsValues {
  const preferences = { ...values };
  delete preferences.notificationsEnabled;
  delete preferences.notificationDurationSeconds;
  delete preferences.notificationPollIntervalSeconds;
  if (!includeAdministratorPolicy) delete preferences.transcoding;
  return preferences;
}
function preferencePatch(values: SettingsValues, saved: SettingsValues, includeAdministratorPolicy = false): SettingsValues {
  const current = preferenceValues(values, includeAdministratorPolicy);
  const baseline = preferenceValues(saved, includeAdministratorPolicy);
  const patch: SettingsValues = {};
  for (const key of Object.keys(current) as Array<keyof SettingsValues>) {
    if (current[key] !== baseline[key]) Object.assign(patch, { [key]: current[key] });
  }
  return patch;
}


function DeviceNotificationsOperationsCard() {
  const [values, setValues] = useState<DeviceNotificationValues>(() => deviceNotificationValues({}));
  const [saved, setSaved] = useState<DeviceNotificationValues>(() => deviceNotificationValues({}));
  const [loading, setLoading] = useState(true);
  const [loaded, setLoaded] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const dirty = JSON.stringify(values) !== JSON.stringify(saved);
  const defaults = {
    notificationsEnabled: rivuneSettingDefaults.notificationsEnabled,
    notificationDurationSeconds: rivuneSettingDefaults.notificationDurationSeconds,
    notificationPollIntervalSeconds: rivuneSettingDefaults.notificationPollIntervalSeconds,
  };

  useEffect(() => {
    let current = true;
    setLoading(true);
    setLoaded(false);
    setError("");
    void api.instanceSettings()
      .then((layer) => {
        if (!current) return;
        const next = deviceNotificationValues(layer.settings);
        setValues(next);
        setSaved(next);
        setLoaded(true);
      })
      .catch((cause) => {
        if (current) setError(notifyError(cause, translate("settings.errors.load"), translate("settings.errors.unavailableTitle")));
      })
      .finally(() => {
        if (current) setLoading(false);
      });
    return () => { current = false; };
  }, []);

  function change<K extends keyof DeviceNotificationValues>(key: K, value: DeviceNotificationValues[K]) {
    setValues((current) => ({ ...current, [key]: value }));
  }

  async function save() {
    if (!dirty) return;
    setSaving(true);
    setError("");
    try {
      const updated = await api.updateInstanceSettings(values);
      const next = deviceNotificationValues(updated.settings);
      setValues(next);
      setSaved(next);
      window.dispatchEvent(new Event("rivune:settings-changed"));
      notifySuccess(translate("settings.notifications.serverSavedMessage"), translate("settings.notifications.savedTitle"));
    } catch (cause) {
      setError(notifyError(cause, translate("settings.errors.save"), translate("settings.errors.saveTitle")));
    } finally {
      setSaving(false);
    }
  }

  return <section className="operations-panel operations-notifications" aria-labelledby="operations-notifications-title">
    <header>
      <div><span>{translate("common.labels.advanced")}</span><h3 id="operations-notifications-title">{translate("settings.groups.deviceNotifications.title")}</h3><p>{translate("settings.groups.deviceNotifications.description")}</p></div>
    </header>
    {error && <Notice>{error}</Notice>}
    {loading && <Skeleton className="settings-skeleton" />}
    {loaded && <>
      <div className="operations-notifications__form">
        <DeviceNotificationFields values={values} defaults={defaults} emptyLabel={translate("settings.defaults.rivune")} onChange={change} />
      </div>
      <footer><div><strong>{translate("settings.scope.serverDefaults")}</strong><small>{translate("settings.scope.serverDescription")}</small></div><Button variant="secondary" disabled={!dirty || saving} onClick={() => setValues(saved)}>{translate("common.actions.discardChanges")}</Button><Button loading={saving} disabled={!dirty} onClick={() => void save()}><Check size={18} /> {translate("settings.actions.savePreferences")}</Button></footer>
    </>}
  </section>;
}

type SettingOption = { readonly value: string } & (
  | { readonly label: string }
  | { readonly labelKey: TranslationKey }
);

const settingOptions = {
  interfaceLanguage: interfaceLanguages,
  theme: [{ value: "system", labelKey: "settings.theme.system" }, { value: "dark", labelKey: "settings.theme.dark" }, { value: "light", labelKey: "settings.theme.light" }],
  resolution: [{ value: "auto", labelKey: "settings.resolution.auto" }, { value: "2160p", labelKey: "settings.resolution.2160p" }, { value: "1080p", labelKey: "settings.resolution.1080p" }, { value: "720p", labelKey: "settings.resolution.720p" }, { value: "480p", labelKey: "settings.resolution.480p" }],
  density: [{ value: "comfortable", labelKey: "settings.density.comfortable" }, { value: "compact", labelKey: "settings.density.compact" }],
  transcoding: [{ value: "inherit", labelKey: "settings.options.transcodingInherit" }, { value: "enabled", labelKey: "settings.options.transcodingEnabled" }, { value: "disabled", labelKey: "settings.options.transcodingDisabled" }],
  hardwareAcceleration: [{ value: "auto", labelKey: "settings.runtime.hardware.auto" }, { value: "software", labelKey: "settings.runtime.hardware.software" }, { value: "vaapi", labelKey: "settings.runtime.hardware.vaapi" }, { value: "hybrid", labelKey: "settings.runtime.hardware.hybrid" }, { value: "qsv", labelKey: "settings.runtime.hardware.qsv" }, { value: "nvenc", labelKey: "settings.runtime.hardware.nvenc" }, { value: "amf", labelKey: "settings.runtime.hardware.amf" }],
  preferredTranscodeVideoCodec: [{ value: "auto", labelKey: "settings.runtime.codec.auto" }, { value: "h264", labelKey: "settings.runtime.codec.h264" }, { value: "hevc", labelKey: "settings.runtime.codec.hevc" }, { value: "av1", labelKey: "settings.runtime.codec.av1" }],
  transcodeQualityPreset: [{ value: "speed", labelKey: "settings.runtime.quality.speed" }, { value: "balanced", labelKey: "settings.runtime.quality.balanced" }, { value: "quality", labelKey: "settings.runtime.quality.quality" }],
  language: [{ value: "auto", labelKey: "settings.language.auto" }, { value: "fr-FR", label: "Français" }, { value: "en-US", labelKey: "languages.english" }, { value: "es-ES", label: "Español" }, { value: "de-DE", label: "Deutsch" }, { value: "it-IT", label: "Italiano" }, { value: "pt-BR", label: "Português" }, { value: "ja-JP", label: "日本語" }],
  region: [{ value: "auto", labelKey: "settings.region.auto" }, { value: "FR", labelKey: "regions.france" }, { value: "BE", labelKey: "regions.belgium" }, { value: "CA", labelKey: "regions.canada" }, { value: "CH", labelKey: "regions.switzerland" }, { value: "US", labelKey: "regions.unitedStates" }, { value: "GB", labelKey: "regions.unitedKingdom" }, { value: "DE", labelKey: "regions.germany" }, { value: "ES", labelKey: "regions.spain" }, { value: "IT", labelKey: "regions.italy" }, { value: "JP", labelKey: "regions.japan" }],
  mapping: [{ value: "tmdb", labelKey: "settings.seriesMapping.tmdb" }, { value: "tvdb", labelKey: "settings.seriesMapping.tvdb" }],
} as const satisfies Record<string, ReadonlyArray<SettingOption>>;

type SettingsSectionDefinition = {
  id: SettingsSection;
  label: string;
  description: string;
  icon: React.ReactNode;
  searchText: string;
};

function settingsSectionDefinitions(serverScope: boolean): SettingsSectionDefinition[] {
  const definition = (id: SettingsSection, label: string, description: string, icon: React.ReactNode, fields: string[]): SettingsSectionDefinition => ({
    id,
    label,
    description,
    icon,
    searchText: [label, description, ...fields].join(" ").toLocaleLowerCase(),
  });
  const appearance = definition("appearance", translate("settings.groups.appearance.title"), translate("settings.groups.appearance.description"), <Palette />, [
    translate("settings.fields.theme"),
    translate("settings.fields.cardDensity"),
    translate("settings.fields.animations"),
    translate("settings.fields.animationsDescription"),
    translate("settings.fields.hideUnreleased"),
    translate("settings.fields.hideUnreleasedDescription"),
    translate("settings.fields.maximumDirectTitles"),
    translate("settings.fields.maximumDirectTitlesDescription"),
    translate("settings.fields.maximumDirectTitlesMode"),
  ]);
  const playbackFields = [
    translate("settings.fields.maximumResolution"),
    translate("settings.fields.maximumCastMembers"),
    translate("settings.fields.preferDirectPlay"),
    translate("settings.fields.preferDirectPlayDescription"),
    translate("settings.fields.autoplayNextEpisode"),
    translate("settings.fields.autoplayNextEpisodeDescription"),
    translate("settings.skipIntro"),
    translate("settings.skipIntroDescription"),
    translate("settings.skipRecap"),
    translate("settings.skipRecapDescription"),
    translate("settings.skipOutro"),
    translate("settings.skipOutroDescription"),
  ];
  if (!serverScope) playbackFields.push(translate("settings.fields.transcoding"), translate("settings.fields.transcodingDescription"));
  const playback = definition("playback", translate("settings.groups.playback.title"), translate("settings.groups.playback.description"), <Film />, playbackFields);
  const language = definition("language", translate("settings.groups.languageMetadata.title"), translate("settings.groups.languageMetadata.description"), <Languages />, [
    translate("settings.interfaceLanguage"),
    translate("settings.interfaceLanguageDescription"),
    translate("settings.fields.metadataLanguage"),
    translate("settings.fields.metadataRegion"),
    translate("settings.fields.seriesMapping"),
    translate("settings.fields.audioLanguage"),
  ]);
  const subtitles = definition("subtitles", translate("settings.groups.subtitles.title"), translate("settings.groups.subtitles.description"), <Captions />, [
    translate("settings.fields.subtitleLanguage"),
    translate("settings.forcedSubtitleLanguage"),
    translate("settings.forcedSubtitleDescription"),
    translate("settings.fields.subtitleSize"),
    translate("settings.fields.subtitleTextColor"),
    translate("settings.fields.subtitleBackgroundOpacity"),
  ]);
  if (serverScope) {
    const runtime = definition("runtime", translate("settings.runtime.title"), translate("settings.runtime.description"), <Server />, [
      translate("settings.runtime.timezone"),
      translate("settings.runtime.jellyfinDebug"),
      translate("settings.runtime.hardwareAcceleration"),
      translate("settings.runtime.transcodeMaxBitrate"),
      translate("settings.runtime.preferredTranscodeVideoCodec"),
      translate("settings.runtime.transcodeQualityPreset"),
      translate("settings.runtime.transcodeConcurrency"),
      translate("settings.runtime.mediaQuota"),
      translate("settings.runtime.artworkQuota"),
    ]);
    const transcoding = definition("transcoding", translate("settings.fields.transcoding"), translate("settings.fields.allowTranscodingDescription"), <Cpu />, [
      translate("settings.fields.allowTranscoding"),
      translate("settings.fields.allowTranscodingDescription"),
    ]);
    const connections = definition("connections", translate("settings.fields.jellyfinApi"), translate("settings.fields.jellyfinEnabledDescription"), <Radio />, [
      translate("settings.fields.jellyfinEnabled"),
      translate("settings.fields.jellyfinEnabledDescription"),
    ]);
    const integrations = definition("integrations", translate("settings.integrations.title"), translate("settings.integrations.description"), <KeyRound />, integrationCredentialNames.map((name) => translate(`settings.integrations.fields.${name}` as TranslationKey)));
    const audit = definition("audit", translate("settings.audit.title"), translate("settings.audit.description"), <Clock3 />, [translate("settings.audit.changedKeys"), translate("settings.audit.revision")]);
    return [appearance, playback, runtime, transcoding, language, subtitles, connections, integrations, audit];
  }
  const connections = definition("connections", translate("settings.connectionsTitle"), translate("settings.trackingDescription"), <Radio />, [
    translate("settings.fields.jellyfinApi"),
    translate("settings.fields.jellyfinEnabledDescription"),
    translate("settings.trackingWatched"),
    translate("settings.trackingProgress"),
    translate("settings.trackingLibrary"),
    translate("settings.trackingConnect"),
  ]);
  return [appearance, playback, language, subtitles, connections];
}



function SettingsAdmin() {
  const { account, activeProfile, updateServerInterfaceLanguage } = useAuth();
  const administrationProfiles = useAdministrationProfiles();
  const [settingsTarget, setSettingsTarget] = useState(activeProfile?.id ?? "");
  const [instance, setInstance] = useState<SettingsValues>({});
  const [savedInstance, setSavedInstance] = useState<SettingsValues>({});
  const [profile, setProfile] = useState<SettingsValues>({});
  const [savedProfile, setSavedProfile] = useState<SettingsValues>({});
  const [inherited, setInherited] = useState<SettingsValues>({});
  const [runtimeApplication, setRuntimeApplication] = useState<RuntimeApplication | undefined>();
  const [integrationDirty, setIntegrationDirty] = useState(false);
  const [saving, setSaving] = useState(false);
  const [checkingTranscodingDisable, setCheckingTranscodingDisable] = useState(false);
  const [transcodingDisableCount, setTranscodingDisableCount] = useState<number | null>(null);
  const [error, setError] = useState("");
  const [loaded, setLoaded] = useState(false);
  const [activeSection, setActiveSection] = useState<SettingsSection>(() => requestedSettingsSection() ?? "appearance");
  const [sectionSearch, setSectionSearch] = useState("");
  const initialSection = requestedSettingsSection() ?? "appearance";
  const lastProfileSection = useRef<SettingsSection>(serverOnlySettingsSections[initialSection] ? "appearance" : initialSection);
  const lastServerSection = useRef<SettingsSection>(initialSection);
  const settingsTargetRef = useRef(settingsTarget);
  settingsTargetRef.current = settingsTarget;
  const canManageProfiles = Boolean(activeProfile?.canManage);
  const canManageServer = canManageProfiles && account?.session.authorizationScope === "global_admin";
  const serverSelected = settingsTarget === "server";
  const targetProfile = administrationProfiles.find((candidate) => candidate.id === settingsTarget) ?? activeProfile;
  const settingsDirty = serverSelected ? JSON.stringify(instance) !== JSON.stringify(savedInstance) : JSON.stringify(profile) !== JSON.stringify(savedProfile);
  const hasUnsavedChanges = settingsDirty || integrationDirty;
  const overrideCount = Object.entries(serverSelected ? instance : profile).filter(([key, value]) => !isDeviceNotificationSetting(key) && value !== null && value !== undefined && !(key === "transcoding" && value === "inherit")).length;
  const sectionDefinitions = settingsSectionDefinitions(serverSelected);
  const normalizedSearch = sectionSearch.trim().toLocaleLowerCase();
  const filteredSections = normalizedSearch ? sectionDefinitions.filter((section) => section.searchText.includes(normalizedSearch)) : sectionDefinitions;

  function sectionAllowed(section: SettingsSection) {
    return serverSelected || !serverOnlySettingsSections[section];
  }
  const visibleSection = sectionAllowed(activeSection) ? activeSection : "appearance";

  function navigateSettingsSection(section: SettingsSection, replace = false) {
    if (!sectionAllowed(section) || integrationDirty) return;
    if (serverSelected) lastServerSection.current = section;
    else lastProfileSection.current = section;
    setActiveSection(section);
    updateAdminRoute("settings", section, replace);
  }

  function changeSettingsTarget(nextTarget: string) {
    if (nextTarget === settingsTarget || hasUnsavedChanges) return;
    const nextSection = nextTarget === "server" ? lastServerSection.current : lastProfileSection.current;
    setSettingsTarget(nextTarget);
    setActiveSection(nextSection);
    updateAdminRoute("settings", nextSection, true);
  }

  function handleSectionKeyDown(event: React.KeyboardEvent<HTMLButtonElement>) {
    if (!["ArrowDown", "ArrowUp", "ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
    const buttons = Array.from(event.currentTarget.parentElement?.querySelectorAll<HTMLButtonElement>("[data-settings-section]:not(:disabled)") ?? []);
    const currentIndex = buttons.indexOf(event.currentTarget);
    if (currentIndex < 0 || buttons.length === 0) return;
    event.preventDefault();
    const rtl = document.documentElement.dir === "rtl";
    const forward = event.key === "ArrowDown" || event.key === (rtl ? "ArrowLeft" : "ArrowRight");
    const nextIndex = event.key === "Home" ? 0
      : event.key === "End" ? buttons.length - 1
        : forward ? (currentIndex + 1) % buttons.length
          : (currentIndex - 1 + buttons.length) % buttons.length;
    buttons[nextIndex]?.focus();
  }

  useEffect(() => {
    if (!normalizedSearch) return;
    const matches = settingsSectionDefinitions(serverSelected).filter((section) => section.searchText.includes(normalizedSearch));
    if (matches.length === 1 && matches[0]?.id !== activeSection) navigateSettingsSection(matches[0]!.id);
  }, [normalizedSearch, serverSelected]);

  useEffect(() => {
    const syncSection = () => {
      if (integrationDirty) {
        updateAdminRoute("settings", "integrations", true);
        return;
      }
      const requestedTab = requestedAdminTab();
      if (requestedTab && requestedTab !== "settings") return;
      const requested = requestedSettingsSection();
      const next = requested && sectionAllowed(requested) ? requested : "appearance";
      if (serverSelected) lastServerSection.current = next;
      else lastProfileSection.current = next;
      setActiveSection(next);
      if (requested !== next || requestedTab !== "settings") updateAdminRoute("settings", next, true);
    };
    syncSection();
    window.addEventListener("hashchange", syncSection);
    window.addEventListener("popstate", syncSection);
    return () => {
      window.removeEventListener("hashchange", syncSection);
      window.removeEventListener("popstate", syncSection);
    };
  }, [integrationDirty, serverSelected]);

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
        setSavedInstance(layer.settings);
        setRuntimeApplication(layer.runtime);
        setInherited({});
        return;
      }
      const [layer, serverDefaults] = await Promise.all([api.profileSettings(target), api.instanceSettings()]);
      if (!current) return;
      setProfile(layer.settings);
      setSavedProfile(layer.settings);
      setInherited(serverDefaults.settings);
      setRuntimeApplication(serverDefaults.runtime);
    })()
      .catch((cause) => {
        if (current) setError(notifyError(cause, translate("settings.errors.load"), translate("settings.errors.unavailableTitle")));
      })
      .finally(() => {
        if (current) setLoaded(true);
      });
    return () => { current = false; };
  }, [settingsTarget]);

  async function requestSave() {
    const disablesServerTranscoding = serverSelected &&
      (savedInstance.allowTranscoding ?? rivuneSettingDefaults.allowTranscoding) &&
      !(instance.allowTranscoding ?? rivuneSettingDefaults.allowTranscoding);
    if (!disablesServerTranscoding) {
      await save();
      return;
    }
    setCheckingTranscodingDisable(true);
    setError("");
    try {
      const activity = await api.playbackActivity();
      setTranscodingDisableCount(activity.sessions.filter((session) => session.mode === "transcode_audio" || session.mode === "transcode").length);
    } catch (cause) {
      setError(notifyError(cause, translate("admin.activity.errors.load"), translate("admin.activity.errors.unavailableTitle")));
    } finally {
      setCheckingTranscodingDisable(false);
    }
  }

  async function save(confirmedTranscodingSessions?: number) {
    if (!activeProfile || !settingsTarget) return;
    const target = settingsTarget;
    const savingServer = target === "server";
    const profileName = account?.profiles.find((candidate) => candidate.id === target)?.name ?? translate("settings.scope.profileFallback");
    setSaving(true);
    setError("");
    try {
      let remainingTranscodingSessions = 0;
      if (savingServer) {
        const updated = await api.updateInstanceSettings(preferencePatch(instance, savedInstance));
        if (settingsTargetRef.current === target) {
          setInstance(updated.settings);
          setSavedInstance(updated.settings);
          setRuntimeApplication(updated.runtime);
        }
        await updateServerInterfaceLanguage(updated.settings.interfaceLanguage ?? "en");
        if (confirmedTranscodingSessions !== undefined) {
          const activity = await api.playbackActivity().catch(() => null);
          remainingTranscodingSessions = activity
            ? activity.sessions.filter((session) => session.mode === "transcode_audio" || session.mode === "transcode").length
            : confirmedTranscodingSessions;
          setTranscodingDisableCount(null);
        }
      } else {
        const updated = await api.updateProfileSettings(target, preferencePatch(profile, savedProfile, canManageServer));
        if (settingsTargetRef.current === target) {
          setProfile(updated.settings);
          setSavedProfile(updated.settings);
        }
      }
      if (savingServer || target === activeProfile.id) window.dispatchEvent(new Event("rivune:settings-changed"));
      notifySuccess(savingServer ? translate("settings.notifications.serverSavedMessage") : translate("settings.notifications.profileSavedMessage", { profileName }), translate("settings.notifications.savedTitle"));
      if (remainingTranscodingSessions > 0) {
        notifyWarning(
          translate(remainingTranscodingSessions === 1 ? "settings.transcoding.activeSessionsOne" : "settings.transcoding.activeSessionsMany", { count: remainingTranscodingSessions }),
          translate("settings.notifications.savedTitle"),
        );
      }
    } catch (cause) {
      setError(notifyError(cause, translate("settings.errors.save"), translate("settings.errors.saveTitle")));
    } finally {
      setSaving(false);
    }
  }


  if (!loaded) return <Skeleton className="settings-skeleton" />;
  const profileName = targetProfile?.name ?? translate("settings.scope.profileFallback");
  const scopeName = serverSelected ? translate("settings.scope.serverDefaults") : translate("settings.scope.profileOverrides", { profileName });
  return <div className="admin-section preferences-admin settings-workspace">
    <aside className="settings-navigation" aria-label={translate("admin.tabs.settings.label")}>
      <div className={`settings-scope settings-scope--${serverSelected ? "server" : "profile"}`}>
        <span className="settings-scope__icon">{serverSelected ? <Server size={20} aria-hidden="true" /> : <CircleUserRound size={20} aria-hidden="true" />}</span>
        <div className="settings-scope__copy">
          <small>{translate("settings.scope.eyebrow")}</small>
          <strong>{scopeName}</strong>
          <span>{serverSelected
            ? translate("settings.scope.serverDefaultCount", { count: overrideCount })
            : translate(overrideCount === 1 ? "settings.scope.profileOverrideCountOne" : "settings.scope.profileOverrideCountMany", { count: overrideCount })}</span>
        </div>
        {canManageProfiles && <label className={`field settings-profile-picker ${hasUnsavedChanges ? "is-locked" : ""}`}>
          <span>{translate("settings.scope.switch")}</span>
          <div>{serverSelected ? <Server size={16} aria-hidden="true" /> : <CircleUserRound size={16} aria-hidden="true" />}
            <Select value={settingsTarget} disabled={saving || checkingTranscodingDisable || hasUnsavedChanges} onChange={changeSettingsTarget} options={[
              ...(canManageServer ? [{ value: "server", label: translate("settings.scope.serverDefaults") }] : []),
              ...administrationProfiles.map((candidate) => ({ value: candidate.id, label: translate("settings.scope.profileOption", { profileName: candidate.name }) })),
            ]} />
          </div>
          {hasUnsavedChanges && <small>{translate("settings.scope.unsavedSwitchHint")}</small>}
        </label>}
      </div>
      <label className="settings-navigation__search">
        <span className="visually-hidden">{translate("common.search")}</span>
        <Search size={17} aria-hidden="true" />
        <input type="search" aria-label={translate("common.search")} placeholder={translate("common.search")} value={sectionSearch} onChange={(event) => setSectionSearch(event.target.value)} />
      </label>
      <nav aria-label={translate("admin.tabs.settings.label")}>
        {filteredSections.map((section) => <button
          type="button"
          key={section.id}
          data-settings-section={section.id}
          className={visibleSection === section.id ? "is-active" : ""}
          disabled={integrationDirty && visibleSection !== section.id}
          aria-current={visibleSection === section.id ? "page" : undefined}
          aria-label={section.label}
          aria-describedby={`settings-navigation-description-${section.id}`}
          aria-controls={`settings-section-${section.id}`}
          onKeyDown={handleSectionKeyDown}
          onClick={() => navigateSettingsSection(section.id)}
        >
          <span>{section.icon}</span>
          <span className="settings-navigation__item-copy">
            <strong className="settings-navigation__item-title">{section.label}</strong>
            <small id={`settings-navigation-description-${section.id}`} className="settings-navigation__item-description">{section.description}</small>
          </span>
        </button>)}
        {filteredSections.length === 0 && <p className="settings-navigation__empty" role="status">{translate("common.noResults")}</p>}
      </nav>
    </aside>
    <main className="settings-content">
      <header className="settings-content__header">
        <span>{serverSelected ? <Server size={22} aria-hidden="true" /> : <CircleUserRound size={22} aria-hidden="true" />}</span>
        <div><small>{translate("settings.scope.eyebrow")}</small><h2>{serverSelected ? translate("settings.server.title") : translate("settings.profile.title", { profileName })}</h2><p>{serverSelected ? translate("settings.scope.serverDescription") : translate("settings.scope.profileDescription")}</p></div>
      </header>
      {error && <Notice>{error}</Notice>}
      {visibleSection === "connections" && !serverSelected
        ? <div id="settings-section-connections" className="settings-connections">
          {Boolean(inherited.jellyfinEnabled) && targetProfile
            ? <JellyfinCredentialPanel key={targetProfile.id} profile={targetProfile} />
            : <div className="jellyfin-access jellyfin-access--disabled" role="note"><div className="jellyfin-access__identity"><span><Radio size={20} aria-hidden="true" /></span><div><small>Jellyfin</small><h3>{translate("settings.fields.jellyfinApi")}</h3><p>{translate("settings.fields.jellyfinEnabledDescription")}</p></div></div><span>{translate("common.status.disabled")}</span></div>}
          <section className="settings-connections__tracking" aria-label={translate("settings.trackingTitle")}>
            <header><span><Radio size={19} aria-hidden="true" /></span><div><h3>{translate("settings.trackingTitle")}</h3><p>{translate("settings.trackingDescription")}</p></div></header>
            <TrackingSettings profileId={settingsTarget} />
          </section>
        </div>
        : serverSelected && visibleSection === "integrations"
          ? <IntegrationSettings onDirtyChange={setIntegrationDirty} />
          : serverSelected && visibleSection === "audit"
            ? <ConfigurationAudit />
            : serverSelected
              ? <SettingsCard activeSection={visibleSection} serverScope runtimeApplication={runtimeApplication} title={translate("settings.server.title")} description={translate("settings.server.description")} icon={<Server />} values={instance} defaults={rivuneSettingDefaults} onChange={setInstance} onSave={() => void requestSave()} onReset={() => setInstance(savedInstance)} saving={saving || checkingTranscodingDisable} dirty={settingsDirty} emptyLabel={translate("settings.defaults.rivune")} />
              : <SettingsCard activeSection={visibleSection} canConfigureTranscoding={canManageServer} title={translate("settings.profile.title", { profileName })} description={translate("settings.profile.description")} icon={<CircleUserRound />} values={profile} defaults={{ ...rivuneSettingDefaults, ...inherited }} onChange={setProfile} onSave={() => void requestSave()} onReset={() => setProfile(savedProfile)} saving={saving} dirty={settingsDirty} emptyLabel={translate("settings.defaults.server")} />}
    </main>
    {transcodingDisableCount !== null && <ConfirmDialog
      title={translate("settings.transcoding.disableConfirmTitle")}
      description={translate(transcodingDisableCount === 1 ? "settings.transcoding.disableConfirmDescriptionOne" : "settings.transcoding.disableConfirmDescriptionMany", { count: transcodingDisableCount })}
      confirmLabel={translate("settings.transcoding.disableConfirm")}
      loading={saving}
      onConfirm={() => void save(transcodingDisableCount)}
      onCancel={() => setTranscodingDisableCount(null)}
    />}
  </div>;
}

function jellyfinServerAddress(apiBaseUrl: string): string {
  try {
    const address = new URL(apiBaseUrl, window.location.origin);
    address.pathname = address.pathname.replace(/\/api\/v1\/?$/, "") || "/";
    address.search = "";
    address.hash = "";
    return address.href.replace(/\/$/, "");
  } catch {
    return window.location.origin;
  }
}

function JellyfinCredentialPanel({ profile }: { profile: Profile }) {
  const { discovery } = useAuth();
  const [credential, setCredential] = useState<JellyfinCredentialStatus | null>(null);
  const [secret, setSecret] = useState<JellyfinCredentialSecret | null>(null);
  const [copyState, setCopyState] = useState<{ field: "server" | "username" | "password"; outcome: "copied" | "error" } | null>(null);
  const [confirmAction, setConfirmAction] = useState<"rotate" | "revoke" | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [creating, setCreating] = useState(false);
  const [loadAttempt, setLoadAttempt] = useState(0);
  const [error, setError] = useState("");
  const releaseNavigationGuardRef = useRef<(() => void) | null>(null);

  function protectSecretIssuance() {
    releaseNavigationGuardRef.current ??= acquireOneShotNavigationGuard();
  }

  useEffect(() => {
    if (busy || creating || secret) return;
    releaseNavigationGuardRef.current?.();
    releaseNavigationGuardRef.current = null;
  }, [busy, creating, secret]);

  useEffect(() => () => {
    releaseNavigationGuardRef.current?.();
    releaseNavigationGuardRef.current = null;
  }, []);

  useEffect(() => {
    let current = true;
    setLoading(true);
    setError("");
    setCredential(null);
    setSecret(null);
    setCopyState(null);
    setConfirmAction(null);
    void api.jellyfinCredential(profile.id)
      .then((status) => { if (current) setCredential(status); })
      .catch((cause) => { if (current) setError(notifyError(cause, translate("settings.errors.load"), translate("settings.fields.jellyfinApi"))); })
      .finally(() => { if (current) setLoading(false); });
    return () => { current = false; };
  }, [loadAttempt, profile.id]);

  if (!discovery) return null;
  const serverAddress = jellyfinServerAddress(discovery.apiBaseUrl);
  const copyLabel = translate("admin.activity.actions.copy");
  const copiedLabel = translate("settings.jellyfinAccess.copied");

  async function copyValue(field: "server" | "username" | "password", value: string) {
    setCopyState(null);
    try {
      await navigator.clipboard.writeText(value);
      setCopyState({ field, outcome: "copied" });
    } catch {
      setCopyState({ field, outcome: "error" });
    }
  }

  function copyButton(field: "server" | "username" | "password", value: string, label: string) {
    const copied = copyState?.field === field && copyState.outcome === "copied";
    return <Button type="button" variant="secondary" className={copied ? "is-copied" : ""} aria-label={`${copyLabel}: ${label}`} onClick={() => void copyValue(field, value)}>{copied ? <Check size={16} aria-hidden="true" /> : <Copy size={16} aria-hidden="true" />} {copied ? copiedLabel : copyLabel}</Button>;
  }

  function reveal(next: JellyfinCredentialSecret) {
    const { password: _password, ...status } = next;
    void _password;
    setCredential(status);
    setSecret(next);
    setCopyState(null);
  }

  async function createCredential() {
    protectSecretIssuance();
    setCreating(true);
    setBusy(true);
    setError("");
    try {
      reveal(await api.createJellyfinCredential(profile.id));
    } catch (cause) {
      const failure = notifyError(cause, translate("settings.errors.save"), translate("settings.fields.jellyfinApi"));
      try {
        const status = await api.jellyfinCredential(profile.id);
        setCredential(status);
        setError(status.active ? translate("settings.jellyfinAccess.createUncertain") : failure);
      } catch {
        setError(failure);
      }
    } finally {
      setBusy(false);
      setCreating(false);
    }
  }

  async function rotateCredential() {
    protectSecretIssuance();
    setBusy(true);
    setError("");
    try {
      reveal(await api.rotateJellyfinCredential(profile.id));
      setConfirmAction(null);
    } catch (cause) {
      setError(notifyError(cause, translate("settings.errors.save"), translate("settings.fields.jellyfinApi")));
      setConfirmAction(null);
    } finally {
      setBusy(false);
    }
  }

  async function revokeCredential() {
    setBusy(true);
    setError("");
    try {
      await api.revokeJellyfinCredential(profile.id);
      setCredential((current) => current ? { ...current, active: false, revokedAt: new Date().toISOString() } : { active: false, canIssue: false, generation: 0 });
      setConfirmAction(null);
    } catch (cause) {
      setError(notifyError(cause, translate("settings.errors.save"), translate("settings.fields.jellyfinApi")));
      setConfirmAction(null);
    } finally {
      setBusy(false);
    }
  }

  function closeSecret() {
    setSecret(null);
    setCopyState(null);
  }

  return <section className="jellyfin-access" aria-label={translate("settings.fields.jellyfinApi")} data-jellyfin-profile={profile.id}>
    <header className="jellyfin-access__header">
      <div className="jellyfin-access__identity"><span><Radio size={20} aria-hidden="true" /></span><div><small>Jellyfin</small><h3>{translate("settings.fields.jellyfinApi")}</h3><p>{translate("settings.fields.jellyfinEnabledDescription")}</p></div></div>
      <div className="jellyfin-access__password" role="note"><KeyRound size={18} aria-hidden="true" /><p>{translate("settings.jellyfinAccess.passwordHint")}</p></div>
    </header>
    <div className="jellyfin-access__server">
      <label><Server size={18} aria-hidden="true" /><span><small>URL</small><input aria-label="URL" value={serverAddress} readOnly dir="ltr" spellCheck={false} autoComplete="off" onFocus={(event) => event.currentTarget.select()} /></span></label>
      {copyButton("server", serverAddress, "URL")}
    </div>
    {error && <Notice>{error}</Notice>}
    {!loading && !credential && error && <Button type="button" variant="secondary" onClick={() => setLoadAttempt((attempt) => attempt + 1)}><RefreshCw size={16} aria-hidden="true" /> {translate("common.retry")}</Button>}
    {loading
      ? <Skeleton className="settings-skeleton" />
      : credential && <article className="jellyfin-access__profile">
        <header><div><CircleUserRound size={17} aria-hidden="true" /><strong>{profile.name}</strong></div><span className={credential.active ? "is-active" : ""}>{translate(credential.active ? "common.status.enabled" : "common.status.disabled")}</span></header>
        {credential.username && <div className="jellyfin-access__credential"><label><span>{translate("auth.username")}</span><input aria-label={translate("auth.username")} value={credential.username} readOnly dir="ltr" spellCheck={false} autoComplete="off" onFocus={(event) => event.currentTarget.select()} /></label>{copyButton("username", credential.username, translate("auth.username"))}</div>}
        {!credential.canIssue && <Notice tone="info">{translate("settings.jellyfinAccess.issuePermission")}</Notice>}
        <div className="jellyfin-access__actions">
          {!credential.active && <Button type="button" loading={busy} disabled={!credential.canIssue} onClick={() => void createCredential()}><Plus size={16} aria-hidden="true" /> {translate("common.add")}</Button>}
          {credential.active && <><Button type="button" variant="secondary" disabled={busy || !credential.canIssue} onClick={() => setConfirmAction("rotate")}><RefreshCw size={16} aria-hidden="true" /> {translate("common.refresh")}</Button><Button type="button" variant="danger" disabled={busy} onClick={() => setConfirmAction("revoke")}><Trash2 size={16} aria-hidden="true" /> {translate("common.actions.remove")}</Button></>}
        </div>
      </article>}
    <span className="visually-hidden" role="status" aria-live="polite" aria-atomic="true">{copyState?.outcome === "copied" ? copiedLabel : copyState?.outcome === "error" ? translate("settings.jellyfinAccess.copyError") : ""}</span>
    {!secret && copyState?.outcome === "error" && <p className="jellyfin-access__copy-error">{translate("settings.jellyfinAccess.copyError")}</p>}
    {(creating || secret) && <Modal dismissible={!creating} onClose={closeSecret} className="editor-modal jellyfin-secret-modal" aria-labelledby="jellyfin-secret-title" aria-describedby="jellyfin-secret-warning">
      <div className="editor-modal__heading"><span><KeyRound size={18} aria-hidden="true" /> Jellyfin</span><h2 id="jellyfin-secret-title">{translate("settings.fields.jellyfinApi")}</h2><p id="jellyfin-secret-warning" className="jellyfin-secret-modal__warning">{translate("settings.jellyfinAccess.pinHint")}</p></div>
      {creating
        ? <Skeleton className="settings-skeleton" />
        : secret && <>
          <div className="jellyfin-secret-modal__fields">
            <div className="jellyfin-access__credential"><label><span>{translate("auth.username")}</span><input aria-label={translate("auth.username")} value={secret.username} readOnly dir="ltr" spellCheck={false} autoComplete="off" onFocus={(event) => event.currentTarget.select()} /></label>{copyButton("username", secret.username, translate("auth.username"))}</div>
            <div className="jellyfin-access__credential"><label><span>{translate("auth.password")}</span><input aria-label={translate("auth.password")} value={secret.password} readOnly dir="ltr" spellCheck={false} autoComplete="off" onFocus={(event) => event.currentTarget.select()} /></label>{copyButton("password", secret.password, translate("auth.password"))}</div>
          </div>
          {copyState?.outcome === "error" && <p className="jellyfin-access__copy-error">{translate("settings.jellyfinAccess.copyError")}</p>}
          <div className="modal-actions"><Button type="button" onClick={closeSecret}>{translate("common.close")}</Button></div>
        </>}
    </Modal>}
    {confirmAction === "rotate" && <ConfirmDialog title={translate("common.refresh")} description={translate("settings.jellyfinAccess.rotateWarning")} confirmLabel={translate("common.refresh")} loading={busy} onCancel={() => setConfirmAction(null)} onConfirm={() => void rotateCredential()} />}
    {confirmAction === "revoke" && <ConfirmDialog title={translate("common.actions.remove")} description={translate("settings.jellyfinAccess.revokeWarning")} confirmLabel={translate("common.actions.remove")} loading={busy} onCancel={() => setConfirmAction(null)} onConfirm={() => void revokeCredential()} />}
  </section>;
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

  if (loading) return <Skeleton className="settings-skeleton tracking-settings__skeleton" />;
  return <section className="tracking-settings" aria-label={translate("settings.trackingTitle")}>
    {error && <Notice>{error}</Notice>}
    {authorization && <section className={`tracking-authorization tracking-authorization--${authorization.provider}`} role="status" aria-live="polite">
      <span className={`tracking-provider-mark tracking-provider-mark--${authorization.provider}`}><TrackingProviderIcon provider={authorization.provider} /></span>
      <div className="tracking-authorization__copy">
        <strong>{translate("settings.trackingEnterCode", { provider: providerName(authorization.provider) })}</strong>
        <small><LoaderCircle size={13} className="spin" aria-hidden="true" /> {translate("settings.tracking.waitingForAuthorization")}</small>
      </div>
      <code>{authorization.userCode}</code>
      <a className="button button--secondary" href={authorization.verificationUrl} target="_blank" rel="noreferrer">{translate("settings.trackingOpenProvider")} <ExternalLink size={14} aria-hidden="true" /></a>
    </section>}
    {loadFailed
      ? <div className="tracking-load-retry"><Button variant="secondary" onClick={() => void retryLoad()}><RefreshCw size={16} /> {translate("settings.tracking.retryLoading")}</Button></div>
      : providers.length > 0
        ? <div className="tracking-provider-grid">{providers.map((status) => {
          const providerBusy = busy.startsWith(`${status.provider}:`);
          const stateLabel = status.lastError
            ? translate("settings.trackingRetrying")
            : status.connected
              ? status.pendingItems
                ? translate("settings.trackingPending", { count: status.pendingItems })
                : status.lastSuccessAt
                  ? translate("settings.trackingLastSuccess", { date: new Date(status.lastSuccessAt).toLocaleString() })
                  : translate("settings.trackingReady")
              : status.configured
                ? translate("settings.tracking.deviceFlowDescription")
                : translate("settings.trackingStatusUnavailable");
          const lastSyncLabel = status.connected && status.lastSuccessAt
            ? translate("settings.trackingLastSuccess", { date: new Date(status.lastSuccessAt).toLocaleString() })
            : "";
          const statusLabel = status.lastError
            ? translate("settings.trackingRetrying")
            : status.connected
              ? translate("settings.trackingStatusConnected")
              : status.configured
                ? translate("settings.trackingStatusDisconnected")
                : translate("settings.trackingStatusUnavailable");
          return <article key={status.provider} aria-labelledby={`tracking-provider-${status.provider}`} className={`tracking-provider-card tracking-provider-card--${status.provider} ${status.connected ? "is-connected" : status.configured ? "is-disconnected" : "is-unavailable"} ${status.lastError ? "has-error" : ""}`}>
            <header className="tracking-provider-card__identity">
              <span className={`tracking-provider-mark tracking-provider-mark--${status.provider}`}><TrackingProviderIcon provider={status.provider} /></span>
              <div>
                <h3 id={`tracking-provider-${status.provider}`}>{providerName(status.provider)}</h3>
                <p>{status.connected ? translate("settings.tracking.connectedDescription") : status.configured ? translate("settings.trackingConnectDescription") : translate("settings.trackingAdminRequired")}</p>
              </div>
              <span className="tracking-provider-status"><i aria-hidden="true" /> {statusLabel}</span>
            </header>
            <div className="tracking-provider-card__activity" aria-live="polite">
              <Clock3 size={16} aria-hidden="true" />
              <div className="tracking-provider-card__activity-copy"><strong>{stateLabel}</strong>{lastSyncLabel && stateLabel !== lastSyncLabel && <small>{lastSyncLabel}</small>}</div>
              {!status.connected && <Button disabled={!status.configured || Boolean(authorization) || providerBusy} loading={busy === `${status.provider}:connect`} onClick={() => void connect(status.provider)}>{translate("settings.trackingConnect")}</Button>}
            </div>
            {status.connected && <details className="tracking-provider-details">
              <summary aria-label={`${translate("admin.tabs.settings.label")} · ${providerName(status.provider)}`}><span><Settings2 size={16} aria-hidden="true" /> {translate("admin.tabs.settings.label")}</span><ChevronDown size={16} aria-hidden="true" /></summary>
              <div className="tracking-provider-details__body">
                <div className="tracking-provider-options">
                  <TrackingToggle id={`tracking-${status.provider}-watched`} label={translate("settings.trackingWatched")} description={translate("settings.trackingWatchedDescription")} checked={status.syncWatched} disabled={providerBusy} saving={busy === `${status.provider}:syncWatched`} onChange={(value) => void toggle(status, "syncWatched", value)} />
                  <TrackingToggle id={`tracking-${status.provider}-progress`} label={translate("settings.trackingProgress")} description={translate("settings.trackingProgressDescription")} checked={status.syncProgress} disabled={providerBusy} saving={busy === `${status.provider}:syncProgress`} onChange={(value) => void toggle(status, "syncProgress", value)} />
                  <TrackingToggle id={`tracking-${status.provider}-library`} label={translate("settings.trackingLibrary")} description={translate("settings.trackingLibraryDescription")} checked={status.syncLibrary} disabled={providerBusy} saving={busy === `${status.provider}:syncLibrary`} onChange={(value) => void toggle(status, "syncLibrary", value)} />
                </div>
                <div className="tracking-provider-action"><small>{translate("settings.tracking.autoSave")}</small><Button variant="secondary" loading={busy === `${status.provider}:disconnect`} disabled={providerBusy} onClick={() => void disconnect(status.provider)}>{translate("settings.trackingDisconnect")}</Button></div>
              </div>
            </details>}
          </article>;
        })}</div>
        : <div className="tracking-empty"><RefreshCw size={26} /><strong>{translate("settings.tracking.emptyTitle")}</strong><p>{translate("settings.tracking.emptyDescription")}</p></div>}
  </section>;
}

function TrackingToggle({ id, label, description, checked, disabled, saving, onChange }: { id: string; label: string; description: string; checked: boolean; disabled: boolean; saving: boolean; onChange: (value: boolean) => void }) {
  return <div className="tracking-toggle"><label className="toggle-field" htmlFor={id}><input id={id} type="checkbox" checked={checked} disabled={disabled} aria-describedby={`${id}-description`} onChange={(event) => onChange(event.target.checked)} /><span aria-busy={saving}><i /><div><strong>{label}</strong><small id={`${id}-description`}>{description}</small></div>{saving && <LoaderCircle size={15} className="spin tracking-toggle__saving" aria-hidden="true" />}</span></label></div>;
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
        <label className="toggle-field"><input type="checkbox" checked={values.enabled} disabled={saving} onChange={(event) => onChange({ ...values, enabled: event.target.checked })} /><span><i /><div><strong>{translate("admin.maintenance.enabled")}</strong><small>{translate("admin.maintenance.enabledDescription")}</small></div></span></label>
      </div>
      <label className="field"><span>{translate("admin.maintenance.message")}</span><div><textarea value={message} disabled={saving} placeholder={translate("admin.maintenance.placeholder")} onChange={(event) => { if (countCodePoints(event.target.value) <= 500) onChange({ ...values, message: event.target.value || null }); }} /></div><small>{translate("admin.maintenance.characterCount", { count: countCodePoints(message) })}</small></label>
      <div className="maintenance-settings__actions"><Button variant="secondary" disabled={!dirty || saving} onClick={onReset}>{translate("common.actions.discardChanges")}</Button><Button loading={saving} disabled={!dirty} onClick={onSave}><Check size={18} /> {translate("admin.maintenance.save")}</Button></div>
    </div>
  </section>;
}

const integrationProviders = ["tmdb", "tvdb", "fanart", "mdblist", "trakt", "simkl"] as const;
const integrationProviderLabels: Record<typeof integrationProviders[number], string> = { tmdb: "TMDB", tvdb: "TVDB", fanart: "Fanart", mdblist: "MDBList", trakt: "Trakt", simkl: "Simkl" };

function formatConfigurationDate(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? translate("settings.audit.unknownDate") : new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(date);
}

function IntegrationSettings({ onDirtyChange }: { onDirtyChange: (dirty: boolean) => void }) {
  const [integrations, setIntegrations] = useState<SettingsIntegrations | null>(null);
  const [draft, setDraft] = useState<Record<IntegrationCredentialName, string>>({ tmdbAccessToken: "", fanartApiKey: "", mdblistApiKey: "", tvdbApiKey: "", tvdbPin: "", traktClientId: "", traktClientSecret: "", simklClientId: "" });
  const [removals, setRemovals] = useState<Partial<Record<IntegrationCredentialName, true>>>({});
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const dirty = integrationCredentialNames.some((name) => draft[name].length > 0 || removals[name]);

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      setIntegrations(await api.settingsIntegrations());
    } catch (cause) {
      setError(notifyError(cause, translate("settings.integrations.errors.load"), translate("settings.integrations.errors.loadTitle")));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);
  useEffect(() => { onDirtyChange(Boolean(dirty)); }, [dirty, onDirtyChange]);
  useEffect(() => () => onDirtyChange(false), [onDirtyChange]);

  function reset() {
    setDraft({ tmdbAccessToken: "", fanartApiKey: "", mdblistApiKey: "", tvdbApiKey: "", tvdbPin: "", traktClientId: "", traktClientSecret: "", simklClientId: "" });
    setRemovals({});
  }

  async function save(event: FormEvent) {
    event.preventDefault();
    if (!dirty) return;
    const patch: SettingsIntegrationsPatch = {};
    for (const name of integrationCredentialNames) {
      if (removals[name]) patch[name] = null;
      else if (draft[name].length > 0) patch[name] = draft[name];
    }
    setSaving(true);
    setError("");
    try {
      const updated = await api.updateSettingsIntegrations(patch);
      setIntegrations(updated);
      reset();
      notifySuccess(translate("settings.integrations.notifications.saved"), translate("settings.integrations.notifications.savedTitle"));
    } catch (cause) {
      setError(notifyError(cause, translate("settings.integrations.errors.save"), translate("settings.integrations.errors.saveTitle")));
    } finally {
      setSaving(false);
    }
  }

  return <section id="settings-section-integrations" className="settings-card configuration-integrations" aria-labelledby="settings-integrations-title">
    <header><span><KeyRound aria-hidden="true" /></span><div><small>{translate("settings.integrations.eyebrow")}</small><h3 id="settings-integrations-title">{translate("settings.integrations.title")}</h3><p>{translate("settings.integrations.description")}</p></div>{integrations && <span className="settings-save-state is-saved">{translate("settings.integrations.revision", { revision: integrations.revision })}</span>}</header>
    {error && <Notice>{error}</Notice>}
    {loading && <Skeleton className="settings-skeleton" />}
    {!loading && !integrations && <EmptyState icon={<KeyRound aria-hidden="true" />} title={translate("settings.integrations.emptyTitle")} description={translate("settings.integrations.emptyDescription")} action={<Button variant="secondary" onClick={() => void load()}>{translate("common.actions.tryAgain")}</Button>} />}
    {integrations && <form className="configuration-integrations__form" onSubmit={(event) => void save(event)}>
      <div className="configuration-provider-status" aria-label={translate("settings.integrations.providerStatus")}>
        {integrationProviders.map((provider) => <span key={provider} className={integrations.providers[provider] ? "is-configured" : ""}><i aria-hidden="true" />{integrationProviderLabels[provider]}<small>{translate(integrations.providers[provider] ? "settings.integrations.configured" : "settings.integrations.notConfigured")}</small></span>)}
      </div>
      <p className="configuration-secret-note" role="note"><Shield size={18} aria-hidden="true" /> <span><strong>{translate("settings.integrations.writeOnlyTitle")}</strong>{translate("settings.integrations.writeOnlyDescription")}</span></p>
      <div className="configuration-credential-list">{integrationCredentialNames.map((name) => {
        const status = integrations.credentials[name];
        const removing = Boolean(removals[name]);
        const label = translate(`settings.integrations.fields.${name}` as TranslationKey);
        return <div className={`configuration-credential ${removing ? "is-removing" : ""}`} key={name}>
          <div className="configuration-credential__copy"><label htmlFor={`integration-${name}`}>{label}</label><span id={`integration-${name}-status`} className={status.configured ? "is-configured" : ""}>{translate(status.configured ? "settings.integrations.configured" : "settings.integrations.notConfigured")}</span>{status.updatedAt && <small>{translate("settings.integrations.updatedAt", { date: formatConfigurationDate(status.updatedAt) })}</small>}</div>
          <div className="configuration-credential__control">
            <input id={`integration-${name}`} name={name} type="password" autoComplete="new-password" spellCheck={false} value={draft[name]} disabled={saving || removing} aria-label={translate("settings.integrations.replaceLabel", { name: label })} aria-describedby={`integration-${name}-status`} placeholder={translate("settings.integrations.replacementPlaceholder")} onChange={(event) => setDraft((current) => ({ ...current, [name]: event.target.value }))} />
            <Button type="button" variant={removing ? "secondary" : "danger"} disabled={saving || (!status.configured && !removing)} aria-label={translate(removing ? "settings.integrations.undoRemoveLabel" : "settings.integrations.removeLabel", { name: label })} onClick={() => {
              setDraft((current) => ({ ...current, [name]: "" }));
              setRemovals((current) => {
                if (!removing) return { ...current, [name]: true };
                const next = { ...current };
                delete next[name];
                return next;
              });
            }}>{removing ? translate("settings.integrations.undoRemove") : translate("common.actions.remove")}</Button>
          </div>
          {removing && <p role="status">{translate("settings.integrations.pendingRemoval")}</p>}
        </div>;
      })}</div>
      {(dirty || saving) && <footer className={`settings-save-bar ${dirty ? "is-dirty" : ""}`}>
        <span className={`settings-save-state ${dirty ? "is-dirty" : "is-saved"}`} role="status" aria-live="polite">{saving ? <><LoaderCircle size={14} className="spin" /> {translate("common.status.saving")}</> : <><Save size={14} /> {translate("common.status.unsavedChanges")}</>}</span>
        <div><Button type="button" variant="secondary" disabled={saving} onClick={reset}>{translate("common.actions.discardChanges")}</Button><Button type="submit" loading={saving} disabled={!dirty}><Check size={18} aria-hidden="true" /> {translate("settings.integrations.save")}</Button></div>
      </footer>}
    </form>}
  </section>;
}

function ConfigurationAudit() {
  const [page, setPage] = useState<ConfigurationAuditPage | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState("");

  const load = useCallback(async (cursor?: number) => {
    cursor === undefined ? setLoading(true) : setLoadingMore(true);
    setError("");
    try {
      const next = await api.settingsAudit(cursor);
      setPage((current) => cursor === undefined || !current ? next : { events: [...current.events, ...next.events], nextCursor: next.nextCursor });
    } catch (cause) {
      setError(notifyError(cause, translate("settings.audit.errors.load"), translate("settings.audit.errors.loadTitle")));
    } finally {
      setLoading(false);
      setLoadingMore(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  return <section id="settings-section-audit" className="settings-card configuration-audit" aria-labelledby="settings-audit-title">
    <header><span><Clock3 aria-hidden="true" /></span><div><small>{translate("settings.audit.eyebrow")}</small><h3 id="settings-audit-title">{translate("settings.audit.title")}</h3><p>{translate("settings.audit.description")}</p></div></header>
    {error && <Notice>{error}</Notice>}
    {loading && <Skeleton className="settings-skeleton" />}
    {!loading && page?.events.length === 0 && <EmptyState icon={<Clock3 aria-hidden="true" />} title={translate("settings.audit.emptyTitle")} description={translate("settings.audit.emptyDescription")} />}
    {page && page.events.length > 0 && <ol className="configuration-audit__events">{page.events.map((event) => <li key={event.id}>
      <span className="configuration-audit__icon" aria-hidden="true">{event.action === "settings.updated" ? <Settings2 /> : <KeyRound />}</span>
      <div>
        <div className="configuration-audit__heading"><strong>{translate(event.action === "settings.updated" ? "settings.audit.actions.settingsUpdated" : "settings.audit.actions.integrationsUpdated")}</strong><time dateTime={event.createdAt}>{formatConfigurationDate(event.createdAt)}</time></div>
        {event.action === "integrations.updated"
          ? <ul className="configuration-audit__changes">{event.changedKeys.map((key) => {
            const name = key as IntegrationCredentialName;
            return <li key={key}><span>{translate(`settings.integrations.fields.${name}` as TranslationKey)}</span><strong className={event.snapshot[name] ? "is-configured" : ""}>{translate(event.snapshot[name] ? "settings.integrations.configured" : "settings.integrations.notConfigured")}</strong></li>;
          })}</ul>
          : <p>{translate("settings.audit.changedKeys")}: {event.changedKeys.join(", ")}</p>}
        <small>{translate("settings.audit.actor", { actor: translate(event.actorUserId ? "settings.audit.administrator" : "settings.audit.system") })} · {translate("settings.audit.revision", { revision: event.revision })}</small>
      </div>
    </li>)}</ol>}
    {page?.nextCursor !== null && page?.nextCursor !== undefined && <Button variant="secondary" loading={loadingMore} disabled={loadingMore} onClick={() => void load(page.nextCursor ?? undefined)}>{translate("settings.audit.loadMore")}</Button>}
  </section>;
}

function RuntimeControl({ id, label, description, requested, active, pending, children }: { id: string; label: string; description: string; requested: string; active: string; pending: boolean; children: React.ReactNode }) {
  return <div className="runtime-setting"><div className="runtime-setting__copy"><strong>{label}</strong><small id={`${id}-description`}>{description}</small><div id={`${id}-state`} className="runtime-setting-state" role="status"><span>{translate("settings.runtime.requested", { value: requested })}</span><span>{translate("settings.runtime.active", { value: active })}</span>{pending && <span className="is-pending"><RefreshCw size={12} aria-hidden="true" />{translate("settings.runtime.restartRequired")}</span>}</div></div><div className="runtime-setting__control">{children}</div></div>;
}

function RuntimeSettingsPanel({ values, application, onChange }: { values: SettingsValues; application?: RuntimeApplication; onChange: <K extends keyof SettingsValues>(key: K, value: SettingsValues[K]) => void }) {
  const requested: RuntimeSettingsValues = {
    timezone: values.timezone ?? application?.requested?.timezone ?? rivuneSettingDefaults.timezone,
    jellyfinEnabled: values.jellyfinEnabled ?? application?.requested?.jellyfinEnabled ?? rivuneSettingDefaults.jellyfinEnabled,
    jellyfinDebug: values.jellyfinDebug ?? application?.requested?.jellyfinDebug ?? rivuneSettingDefaults.jellyfinDebug,
    hardwareAcceleration: values.hardwareAcceleration ?? application?.requested?.hardwareAcceleration ?? rivuneSettingDefaults.hardwareAcceleration,
    transcodeMaxBitrateKbps: values.transcodeMaxBitrateKbps ?? application?.requested?.transcodeMaxBitrateKbps ?? rivuneSettingDefaults.transcodeMaxBitrateKbps,
    preferredTranscodeVideoCodec: values.preferredTranscodeVideoCodec ?? application?.requested?.preferredTranscodeVideoCodec ?? rivuneSettingDefaults.preferredTranscodeVideoCodec,
    transcodeQualityPreset: values.transcodeQualityPreset ?? application?.requested?.transcodeQualityPreset ?? rivuneSettingDefaults.transcodeQualityPreset,
    transcodeConcurrency: values.transcodeConcurrency ?? application?.requested?.transcodeConcurrency ?? rivuneSettingDefaults.transcodeConcurrency,
    mediaMaxStorageMB: values.mediaMaxStorageMB ?? application?.requested?.mediaMaxStorageMB ?? rivuneSettingDefaults.mediaMaxStorageMB,
    artworkMaxStorageMB: values.artworkMaxStorageMB ?? application?.requested?.artworkMaxStorageMB ?? rivuneSettingDefaults.artworkMaxStorageMB,
    allowTranscoding: values.allowTranscoding ?? application?.requested?.allowTranscoding ?? rivuneSettingDefaults.allowTranscoding,
  };
  const active = application?.active ?? requested;
  const pending = application?.pendingRestart ?? [];
  const hardwareLabel = (value: HardwareAccelerationMode) => {
    const option = settingOptions.hardwareAcceleration.find((candidate) => candidate.value === value);
    return option ? translate(option.labelKey) : value;
  };
  const codecLabel = (value: RuntimeSettingsValues["preferredTranscodeVideoCodec"]) => {
    const option = settingOptions.preferredTranscodeVideoCodec.find((candidate) => candidate.value === value);
    return option ? translate(option.labelKey) : value;
  };
  const qualityLabel = (value: RuntimeSettingsValues["transcodeQualityPreset"]) => {
    const option = settingOptions.transcodeQualityPreset.find((candidate) => candidate.value === value);
    return option ? translate(option.labelKey) : value;
  };
  const numberLabel = (value: number, suffix: string) => `${new Intl.NumberFormat(locale).format(value)}${suffix}`;

  return <SettingsGroup sectionId="runtime" icon={<Server />} title={translate("settings.runtime.title")} description={translate("settings.runtime.description")}>
    <RuntimeControl id="runtime-timezone" label={translate("settings.runtime.timezone")} description={translate("settings.runtime.timezoneDescription")} requested={requested.timezone} active={active.timezone} pending={false}><input className="setting-text-input" name="timezone" value={requested.timezone} aria-label={translate("settings.runtime.timezone")} aria-describedby="runtime-timezone-description runtime-timezone-state" list="runtime-timezones" onChange={(event) => onChange("timezone", event.target.value)} /><datalist id="runtime-timezones"><option value="UTC" /><option value="America/New_York" /><option value="Europe/London" /><option value="Europe/Paris" /><option value="Asia/Tokyo" /></datalist></RuntimeControl>
    <RuntimeControl id="runtime-jellyfin-debug" label={translate("settings.runtime.jellyfinDebug")} description={translate("settings.runtime.jellyfinDebugDescription")} requested={translate(requested.jellyfinDebug ? "common.status.on" : "common.status.off")} active={translate(active.jellyfinDebug ? "common.status.on" : "common.status.off")} pending={false}><label className="setting-toggle"><input type="checkbox" aria-label={translate("settings.runtime.jellyfinDebug")} aria-describedby="runtime-jellyfin-debug-description runtime-jellyfin-debug-state" checked={requested.jellyfinDebug} onChange={(event) => onChange("jellyfinDebug", event.target.checked)} /><span aria-hidden="true"><i /></span></label></RuntimeControl>
    <RuntimeControl id="runtime-hardware-acceleration" label={translate("settings.runtime.hardwareAcceleration")} description={translate("settings.runtime.hardwareAccelerationDescription")} requested={hardwareLabel(requested.hardwareAcceleration)} active={hardwareLabel(active.hardwareAcceleration)} pending={pending.includes("hardwareAcceleration")}><Select name="hardwareAcceleration" aria-label={translate("settings.runtime.hardwareAcceleration")} aria-describedby="runtime-hardware-acceleration-description runtime-hardware-acceleration-state" value={requested.hardwareAcceleration} onChange={(value) => onChange("hardwareAcceleration", value as HardwareAccelerationMode)} options={settingOptions.hardwareAcceleration.map((option) => ({ value: option.value, label: translate(option.labelKey) }))} /></RuntimeControl>
    <RuntimeControl id="runtime-preferred-codec" label={translate("settings.runtime.preferredTranscodeVideoCodec")} description={translate("settings.runtime.preferredTranscodeVideoCodecDescription")} requested={codecLabel(requested.preferredTranscodeVideoCodec)} active={codecLabel(active.preferredTranscodeVideoCodec)} pending={pending.includes("preferredTranscodeVideoCodec")}><Select name="preferredTranscodeVideoCodec" aria-label={translate("settings.runtime.preferredTranscodeVideoCodec")} aria-describedby="runtime-preferred-codec-description runtime-preferred-codec-state" value={requested.preferredTranscodeVideoCodec} onChange={(value) => onChange("preferredTranscodeVideoCodec", value as RuntimeSettingsValues["preferredTranscodeVideoCodec"])} options={settingOptions.preferredTranscodeVideoCodec.map((option) => ({ value: option.value, label: translate(option.labelKey) }))} /></RuntimeControl>
    <RuntimeControl id="runtime-quality-preset" label={translate("settings.runtime.transcodeQualityPreset")} description={translate("settings.runtime.transcodeQualityPresetDescription")} requested={qualityLabel(requested.transcodeQualityPreset)} active={qualityLabel(active.transcodeQualityPreset)} pending={pending.includes("transcodeQualityPreset")}><Select name="transcodeQualityPreset" aria-label={translate("settings.runtime.transcodeQualityPreset")} aria-describedby="runtime-quality-preset-description runtime-quality-preset-state" value={requested.transcodeQualityPreset} onChange={(value) => onChange("transcodeQualityPreset", value as RuntimeSettingsValues["transcodeQualityPreset"])} options={settingOptions.transcodeQualityPreset.map((option) => ({ value: option.value, label: translate(option.labelKey) }))} /></RuntimeControl>
    {([
      ["transcodeConcurrency", "settings.runtime.transcodeConcurrency", "settings.runtime.transcodeConcurrencyDescription", 1, 32, ""],
      ["transcodeMaxBitrateKbps", "settings.runtime.transcodeMaxBitrate", "settings.runtime.transcodeMaxBitrateDescription", 64, 200000, translate("settings.units.kbpsSuffix")],
      ["mediaMaxStorageMB", "settings.runtime.mediaQuota", "settings.runtime.mediaQuotaDescription", 512, 102400, translate("settings.units.megabytesSuffix")],
      ["artworkMaxStorageMB", "settings.runtime.artworkQuota", "settings.runtime.artworkQuotaDescription", 256, 102400, translate("settings.units.megabytesSuffix")],
    ] as const).map(([key, labelKey, descriptionKey, minimum, maximum, suffix]) => <RuntimeControl id={`runtime-${key}`} key={key} label={translate(labelKey)} description={translate(descriptionKey)} requested={numberLabel(requested[key], suffix)} active={numberLabel(active[key], suffix)} pending={key === "transcodeConcurrency" && pending.includes("transcodeConcurrency")}><input className="setting-number-input" name={key} type="number" min={minimum} max={maximum} step={1} required aria-label={translate(labelKey)} aria-describedby={`runtime-${key}-description runtime-${key}-state`} value={requested[key]} onChange={(event) => { const next = event.currentTarget.valueAsNumber; if (Number.isInteger(next) && next >= minimum && next <= maximum) onChange(key, next); }} /></RuntimeControl>)}
  </SettingsGroup>;
}

function SettingsCard({ activeSection, serverScope = false, canConfigureTranscoding = false, runtimeApplication, values, defaults = {}, onChange, onSave, onReset, saving, dirty, emptyLabel = translate("settings.defaults.server") }: { activeSection: SettingsSection; serverScope?: boolean; canConfigureTranscoding?: boolean; runtimeApplication?: RuntimeApplication; title: string; description: string; icon: React.ReactNode; values: SettingsValues; defaults?: SettingsValues; onChange: (values: SettingsValues) => void; onSave: () => void; onReset: () => void; saving: boolean; dirty: boolean; emptyLabel?: string }) {
  const effective = {
    interfaceLanguage: defaults.interfaceLanguage ?? rivuneSettingDefaults.interfaceLanguage,
    theme: defaults.theme ?? rivuneSettingDefaults.theme,
    maximumResolution: defaults.maximumResolution ?? rivuneSettingDefaults.maximumResolution,
    maximumCastMembers: defaults.maximumCastMembers ?? rivuneSettingDefaults.maximumCastMembers,
    maximumDirectTitles: defaults.maximumDirectTitles ?? rivuneSettingDefaults.maximumDirectTitles,
    allowTranscoding: defaults.allowTranscoding ?? rivuneSettingDefaults.allowTranscoding,
    jellyfinEnabled: defaults.jellyfinEnabled ?? rivuneSettingDefaults.jellyfinEnabled,
    transcoding: defaults.transcoding ?? rivuneSettingDefaults.transcoding,
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
  };
  const serverAllowsTranscoding = serverScope ? values.allowTranscoding ?? effective.allowTranscoding : effective.allowTranscoding;
  const jellyfinEnabled = serverScope ? values.jellyfinEnabled ?? effective.jellyfinEnabled : effective.jellyfinEnabled;
  const profileTranscoding = values.transcoding ?? rivuneSettingDefaults.transcoding;
  const effectiveTranscoding = serverAllowsTranscoding && profileTranscoding !== "disabled";
  const subtitlePreviewStyle = {
    "--subtitle-preview-scale": values.subtitleSizePercent ?? effective.subtitleSizePercent,
    "--subtitle-preview-color": values.subtitleTextColor ?? effective.subtitleTextColor,
    "--subtitle-preview-opacity": values.subtitleBackgroundOpacityPercent ?? effective.subtitleBackgroundOpacityPercent,
  } as CSSProperties;
  function change<K extends keyof SettingsValues>(key: K, value: SettingsValues[K]) {
    onChange({ ...values, [key]: value });
  }

  return <section className="settings-card preferences-workspace" aria-busy={saving}>
    <fieldset className="settings-groups settings-groups--preferences" disabled={saving}>
      {activeSection === "appearance" && <SettingsGroup sectionId="appearance" icon={<Palette />} title={translate("settings.groups.appearance.title")} description={translate("settings.groups.appearance.description")}>
        <SelectSetting name="theme" presentation="theme" label={translate("settings.fields.theme")} value={values.theme} defaultValue={effective.theme} options={settingOptions.theme} emptyLabel={emptyLabel} onChange={(value) => change("theme", value)} />
        <SelectSetting name="cardDensity" presentation="density" label={translate("settings.fields.cardDensity")} value={values.cardDensity} defaultValue={effective.cardDensity} options={settingOptions.density} emptyLabel={emptyLabel} onChange={(value) => change("cardDensity", value as "comfortable" | "compact" | null)} />
        <InheritedToggle label={translate("settings.fields.animations")} description={translate("settings.fields.animationsDescription")} value={values.animationsEnabled} defaultValue={effective.animationsEnabled} onChange={(value) => change("animationsEnabled", value)} emptyLabel={emptyLabel} />
        <InheritedToggle label={translate("settings.fields.hideUnreleased")} description={translate("settings.fields.hideUnreleasedDescription")} value={values.hideUnreleased} defaultValue={effective.hideUnreleased} onChange={(value) => change("hideUnreleased", value)} emptyLabel={emptyLabel} />
        <BoundedInheritedNumberSetting serverScope={serverScope} value={values.maximumDirectTitles} serverValue={effective.maximumDirectTitles} saving={saving} label={translate("settings.fields.maximumDirectTitles")} description={translate("settings.fields.maximumDirectTitlesDescription")} modeLabel={translate("settings.fields.maximumDirectTitlesMode")} name="maximumDirectTitles" defaultValue={20} minimum={1} absoluteMaximum={100} onChange={(value) => change("maximumDirectTitles", value)} />
      </SettingsGroup>}

      {activeSection === "playback" && <SettingsGroup sectionId="playback" icon={<Film />} title={translate("settings.groups.playback.title")} description={translate("settings.groups.playback.description")}>
        {!serverScope && <div className="setting-control setting-control--transcoding">
          {canConfigureTranscoding && <label className="field"><span>{translate("settings.fields.transcoding")}</span><div><Select value={profileTranscoding} disabled={saving} aria-describedby="profile-transcoding-description" onChange={(value) => change("transcoding", value as "inherit" | "enabled" | "disabled")} options={settingOptions.transcoding.map((option) => ({ value: option.value, label: translate(option.labelKey) }))} /></div><small id="profile-transcoding-description">{translate("settings.fields.transcodingDescription")}</small></label>}
          <div className={`settings-transcoding-state ${effectiveTranscoding ? "is-enabled" : "is-blocked"}`} role="status" aria-live="polite">
            {effectiveTranscoding ? <Check aria-hidden="true" /> : <Shield aria-hidden="true" />}
            <p>{translate(!serverAllowsTranscoding ? "settings.transcoding.blockedByServer" : effectiveTranscoding ? "settings.transcoding.effectiveEnabled" : "settings.transcoding.effectiveDisabled")}</p>
          </div>
        </div>}
        <SelectSetting label={translate("settings.fields.maximumResolution")} value={values.maximumResolution} defaultValue={effective.maximumResolution} options={settingOptions.resolution} emptyLabel={emptyLabel} onChange={(value) => change("maximumResolution", value)} />
        <MaximumCastMembersSetting serverScope={serverScope} value={values.maximumCastMembers} serverValue={effective.maximumCastMembers} saving={saving} onChange={(value) => change("maximumCastMembers", value)} />
        <InheritedToggle label={translate("settings.fields.preferDirectPlay")} description={translate("settings.fields.preferDirectPlayDescription")} value={values.preferDirectPlay} defaultValue={effective.preferDirectPlay} onChange={(value) => change("preferDirectPlay", value)} emptyLabel={emptyLabel} />
        <InheritedToggle label={translate("settings.fields.autoplayNextEpisode")} description={translate("settings.fields.autoplayNextEpisodeDescription")} value={values.autoplayNextEpisode} defaultValue={effective.autoplayNextEpisode} onChange={(value) => change("autoplayNextEpisode", value)} emptyLabel={emptyLabel} />
        <InheritedToggle label={translate("settings.skipIntro")} description={translate("settings.skipIntroDescription")} value={values.skipIntroEnabled} defaultValue={effective.skipIntroEnabled} onChange={(value) => change("skipIntroEnabled", value)} emptyLabel={emptyLabel} />
        <InheritedToggle label={translate("settings.skipRecap")} description={translate("settings.skipRecapDescription")} value={values.skipRecapEnabled} defaultValue={effective.skipRecapEnabled} onChange={(value) => change("skipRecapEnabled", value)} emptyLabel={emptyLabel} />
        <InheritedToggle label={translate("settings.skipOutro")} description={translate("settings.skipOutroDescription")} value={values.skipOutroEnabled} defaultValue={effective.skipOutroEnabled} onChange={(value) => change("skipOutroEnabled", value)} emptyLabel={emptyLabel} />
      </SettingsGroup>}

      {activeSection === "runtime" && serverScope && <RuntimeSettingsPanel values={values} application={runtimeApplication} onChange={change} />}

      {activeSection === "transcoding" && serverScope && <SettingsGroup sectionId="transcoding" icon={<Cpu />} title={translate("settings.fields.transcoding")} description={translate("settings.fields.allowTranscodingDescription")}>
        <div className="setting-control setting-control--toggle settings-transcoding-control">
          <label className="toggle-field"><input type="checkbox" checked={serverAllowsTranscoding} disabled={saving} aria-describedby="allow-transcoding-description" onChange={(event) => change("allowTranscoding", event.target.checked)} /><span><i /><div><strong>{translate("settings.fields.allowTranscoding")}</strong><small id="allow-transcoding-description">{translate("settings.fields.allowTranscodingDescription")}</small></div></span></label>
        </div>
      </SettingsGroup>}

      {activeSection === "connections" && serverScope && <SettingsGroup sectionId="connections" icon={<Radio />} title={translate("settings.fields.jellyfinApi")} description={translate("settings.fields.jellyfinEnabledDescription")}>
        <div className="setting-control setting-control--toggle">
          <label className="toggle-field"><input type="checkbox" checked={jellyfinEnabled} disabled={saving} aria-describedby="jellyfin-enabled-description" onChange={(event) => change("jellyfinEnabled", event.target.checked)} /><span><i /><div><strong>{translate("settings.fields.jellyfinEnabled")}</strong><small id="jellyfin-enabled-description">{translate("settings.fields.jellyfinEnabledDescription")}</small></div></span></label>
        </div>
      </SettingsGroup>}

      {activeSection === "language" && <SettingsGroup sectionId="language" icon={<Languages />} title={translate("settings.groups.languageMetadata.title")} description={translate("settings.groups.languageMetadata.description")}>
        <SelectSetting name="interfaceLanguage" label={translate("settings.interfaceLanguage")} description={translate("settings.interfaceLanguageDescription")} value={values.interfaceLanguage} defaultValue={effective.interfaceLanguage} options={settingOptions.interfaceLanguage} emptyLabel={emptyLabel} onChange={(value) => change("interfaceLanguage", value as InterfaceLanguage | null)} />
        <SelectSetting label={translate("settings.fields.metadataLanguage")} value={values.metadataLanguage} defaultValue={effective.metadataLanguage} options={settingOptions.language} emptyLabel={emptyLabel} onChange={(value) => change("metadataLanguage", value)} />
        <SelectSetting label={translate("settings.fields.metadataRegion")} value={values.metadataRegion} defaultValue={effective.metadataRegion} options={settingOptions.region} emptyLabel={emptyLabel} onChange={(value) => change("metadataRegion", value)} />
        <SelectSetting label={translate("settings.fields.seriesMapping")} value={values.seriesMappingProvider} defaultValue={effective.seriesMappingProvider} options={settingOptions.mapping} emptyLabel={emptyLabel} onChange={(value) => change("seriesMappingProvider", value as "tmdb" | "tvdb" | null)} />
        <TextSetting label={translate("settings.fields.audioLanguage")} value={values.audioLanguage} defaultValue={effective.audioLanguage} placeholder={translate("settings.fields.languageCodePlaceholder")} emptyLabel={emptyLabel} onChange={(value) => change("audioLanguage", value)} />
      </SettingsGroup>}

      {activeSection === "subtitles" && <SettingsGroup sectionId="subtitles" icon={<Captions />} title={translate("settings.groups.subtitles.title")} description={translate("settings.groups.subtitles.description")}>
        <TextSetting label={translate("settings.fields.subtitleLanguage")} value={values.subtitleLanguage} defaultValue={effective.subtitleLanguage} placeholder={translate("settings.fields.languageCodePlaceholder")} emptyLabel={emptyLabel} onChange={(value) => change("subtitleLanguage", value)} />
        <TextSetting label={translate("settings.forcedSubtitleLanguage")} value={values.forcedSubtitleLanguage} defaultValue={effective.forcedSubtitleLanguage} placeholder={emptyLabel} emptyLabel={emptyLabel} list="forced-subtitle-languages" description={translate("settings.forcedSubtitleDescription")} onChange={(value) => change("forcedSubtitleLanguage", value)}>
          <datalist id="forced-subtitle-languages"><option value="off">{translate("settings.forcedSubtitleOff")}</option><option value="en">{translate("languages.english")}</option><option value="fr">Français</option><option value="es">Español</option><option value="de">Deutsch</option><option value="it">Italiano</option><option value="pt">Português</option><option value="ja">日本語</option></datalist>
        </TextSetting>
        <RangeSetting label={translate("settings.fields.subtitleSize")} value={values.subtitleSizePercent} defaultValue={effective.subtitleSizePercent} min={50} max={200} step={1} suffix="%" emptyLabel={emptyLabel} onChange={(value) => change("subtitleSizePercent", value)} />
        <ColorSetting value={values.subtitleTextColor} defaultValue={effective.subtitleTextColor} emptyLabel={emptyLabel} onChange={(value) => change("subtitleTextColor", value)} />
        <RangeSetting label={translate("settings.fields.subtitleBackgroundOpacity")} value={values.subtitleBackgroundOpacityPercent} defaultValue={effective.subtitleBackgroundOpacityPercent} min={0} max={100} step={1} suffix="%" emptyLabel={emptyLabel} onChange={(value) => change("subtitleBackgroundOpacityPercent", value)} />
        <figure className="subtitle-preview" style={subtitlePreviewStyle} aria-label={translate("settings.groups.subtitles.title")}>
          <div className="subtitle-preview__scene" aria-hidden="true"><i /><i /><i /></div>
          <figcaption><span className="subtitle-preview__caption">{translate("settings.groups.subtitles.description")}</span></figcaption>
        </figure>
      </SettingsGroup>}
    </fieldset>
    {(dirty || saving) && <footer className={`settings-save-bar ${dirty ? "is-dirty" : ""}`} aria-label={translate("common.status.unsavedChanges")}>
      <span className={`settings-save-state ${dirty ? "is-dirty" : "is-saved"}`} role="status" aria-live="polite">{saving ? <><LoaderCircle size={14} className="spin" /> {translate("common.status.saving")}</> : <><Save size={14} /> {translate("common.status.unsavedChanges")}</>}</span>
      <div>
        <Button variant="secondary" disabled={saving} onClick={onReset}>{translate("common.actions.discardChanges")}</Button>
        <Button loading={saving} onClick={onSave}><Check size={18} /> {translate("settings.actions.savePreferences")}</Button>
      </div>
    </footer>}
  </section>;
}

function DeviceNotificationFields({ values, defaults, emptyLabel, onChange }: { values: DeviceNotificationValues; defaults: { notificationsEnabled: boolean; notificationDurationSeconds: number; notificationPollIntervalSeconds: number }; emptyLabel: string; onChange: <K extends keyof DeviceNotificationValues>(key: K, value: DeviceNotificationValues[K]) => void }) {
  return <>
    <InheritedToggle label={translate("settings.fields.sessionNotifications")} description={translate("settings.fields.sessionNotificationsDescription")} value={values.notificationsEnabled} defaultValue={defaults.notificationsEnabled} onChange={(value) => onChange("notificationsEnabled", value)} emptyLabel={emptyLabel} />
    <RangeSetting label={translate("settings.fields.notificationDuration")} value={values.notificationDurationSeconds} defaultValue={defaults.notificationDurationSeconds} min={2} max={30} step={1} suffix={translate("settings.units.secondsSuffix")} emptyLabel={emptyLabel} onChange={(value) => onChange("notificationDurationSeconds", value)} />
    <RangeSetting label={translate("settings.fields.notificationPollingInterval")} value={values.notificationPollIntervalSeconds} defaultValue={defaults.notificationPollIntervalSeconds} min={5} max={300} step={1} suffix={translate("settings.units.secondsSuffix")} emptyLabel={emptyLabel} onChange={(value) => onChange("notificationPollIntervalSeconds", value)} />
  </>;
}

function SettingsGroup({ sectionId, icon, iconClassName = "", title, description, status, statusTone = "", className = "", children }: { sectionId?: SettingsSection; icon: React.ReactNode; iconClassName?: string; title: string; description: string; status?: string; statusTone?: "connected" | "disconnected" | "unavailable" | ""; className?: string; children: React.ReactNode }) {
  return <section id={sectionId ? `settings-section-${sectionId}` : undefined} className={`settings-group ${className}`}>
    <div className="settings-group__heading"><span className={iconClassName}>{icon}</span><div><div className="settings-group__title"><h4>{title}</h4>{status && <span className={`settings-group__status ${statusTone ? `is-${statusTone}` : ""}`}>{status}</span>}</div><p>{description}</p></div></div>
    <div className="settings-group__grid">{children}</div>
  </section>;
}

function SettingInheritAction({ source, settingLabel, onClick }: { source: string; settingLabel: string; onClick: () => void }) {
  const label = translate("settings.actions.useInherited", { source: source.toLowerCase() });
  return <button type="button" className="setting-inherit-action" aria-label={`${label} · ${settingLabel}`} onClick={onClick}><RefreshCw size={12} aria-hidden="true" /><span>{label}</span></button>;
}

function InheritedToggle({ label, description, value, defaultValue, onChange, emptyLabel }: { label: string; description: string; value: boolean | null | undefined; defaultValue: boolean; onChange: (value: boolean | null) => void; emptyLabel: string }) {
  const inherited = value === null || value === undefined;
  const shown = value ?? defaultValue;
  const state = inherited
    ? translate("settings.value.inheritedBoolean", { source: emptyLabel, value: translate(shown ? "common.status.on" : "common.status.off") })
    : translate("settings.value.overrideBoolean", { value: translate(shown ? "common.status.on" : "common.status.off") });
  return <div className="setting-control setting-control--toggle">
    <div className="setting-row">
      <div className="setting-row__copy"><strong>{label}</strong><small>{description}</small><em className={`setting-value-state ${inherited ? "is-inherited" : "is-override"}`}>{state}</em></div>
      <div className="setting-row__actions">
        {!inherited && <SettingInheritAction source={emptyLabel} settingLabel={label} onClick={() => onChange(null)} />}
        <label className="setting-toggle">
          <input type="checkbox" aria-label={label} checked={shown} onChange={(event) => onChange(event.target.checked)} />
          <span aria-hidden="true"><i /></span>
        </label>
      </div>
    </div>
  </div>;
}
function MaximumCastMembersSetting({ serverScope, value, serverValue, saving, onChange }: { serverScope: boolean; value: number | null | undefined; serverValue: number; saving: boolean; onChange: (value: number | null) => void }) {
  return <BoundedInheritedNumberSetting
    serverScope={serverScope}
    value={value}
    serverValue={serverValue}
    saving={saving}
    label={translate("settings.fields.maximumCastMembers")}
    modeLabel={translate("settings.fields.maximumCastMembersMode")}
    name="maximumCastMembers"
    defaultValue={20}
    minimum={1}
    absoluteMaximum={100}
    onChange={onChange}
  />;
}

function BoundedInheritedNumberSetting({ serverScope, value, serverValue, saving, label, description, modeLabel, name, defaultValue, minimum, absoluteMaximum, onChange }: { serverScope: boolean; value: number | null | undefined; serverValue: number; saving: boolean; label: string; description?: string; modeLabel: string; name: string; defaultValue: number; minimum: number; absoluteMaximum: number; onChange: (value: number | null) => void }) {
  const inherited = value === null || value === undefined;
  const maximum = serverScope ? absoluteMaximum : boundedInteger(serverValue, defaultValue, minimum, absoluteMaximum);
  const shown = inherited ? boundedInteger(serverValue, defaultValue, minimum, maximum) : boundedInteger(value, serverScope ? defaultValue : maximum, minimum, maximum);
  const source = serverScope ? translate("settings.defaults.rivune") : translate("settings.defaults.server");
  const state = inherited
    ? translate("settings.value.inheritedNumber", { source, value: shown, suffix: "" })
    : translate("settings.value.overrideNumber", { value: shown, suffix: "" });
  return <div className="setting-control setting-control--number">
    <div className="setting-row">
      <div className="setting-row__copy"><strong>{label}</strong>{description && <small>{description}</small>}<em className={`setting-value-state ${inherited ? "is-inherited" : "is-override"}`}>{state}</em></div>
      <div className="setting-row__actions">
        {!serverScope && <Select className="setting-mode-select" name={`${name}Mode`} aria-label={modeLabel} disabled={saving} value={inherited ? "inherit" : "custom"} onChange={(value) => onChange(value === "inherit" ? null : maximum)} options={[{ value: "inherit", label: translate("settings.options.transcodingInherit") }, { value: "custom", label: translate("settings.options.customValue") }]} />}
        <input
          className="setting-number-input"
          aria-label={label}
          name={name}
          type="number"
          min={minimum}
          max={maximum}
          step={1}
          required
          disabled={saving || (!serverScope && inherited)}
          value={shown}
          onChange={(event) => {
            const next = event.currentTarget.valueAsNumber;
            if (Number.isInteger(next) && next >= minimum && next <= maximum) onChange(next);
          }}
        />
        {!inherited && <SettingInheritAction source={source} settingLabel={label} onClick={() => onChange(null)} />}
      </div>
    </div>
  </div>;
}

function boundedInteger(value: number | null | undefined, fallback: number, minimum: number, maximum: number): number {
  return typeof value === "number" && Number.isInteger(value) && value >= minimum && value <= maximum ? value : fallback;
}


function RangeSetting({ label, value, defaultValue, min, max, step, suffix, emptyLabel, onChange }: { label: string; value: number | null | undefined; defaultValue: number; min: number; max: number; step: number; suffix: string; emptyLabel: string; onChange: (value: number | null) => void }) {
  const inherited = value === null || value === undefined;
  const shown = value ?? defaultValue;
  const state = inherited
    ? translate("settings.value.inheritedNumber", { source: emptyLabel, value: shown, suffix })
    : translate("settings.value.overrideNumber", { value: shown, suffix });
  return <div className="setting-control setting-control--range">
    <div className="setting-row">
      <div className="setting-row__copy"><strong>{label}</strong><em className={`setting-value-state ${inherited ? "is-inherited" : "is-override"}`}>{state}</em></div>
      <div className="setting-row__actions">
        <input aria-label={label} type="range" min={min} max={max} step={step} value={shown} onChange={(event) => onChange(Number(event.target.value))} />
        <output>{shown}{suffix}</output>
        {!inherited && <SettingInheritAction source={emptyLabel} settingLabel={label} onClick={() => onChange(null)} />}
      </div>
    </div>
  </div>;
}

function ColorSetting({ value, defaultValue, emptyLabel, onChange }: { value: string | null | undefined; defaultValue: string; emptyLabel: string; onChange: (value: string | null) => void }) {
  const inherited = value === null || value === undefined;
  const shown = value ?? defaultValue;
  const label = translate("settings.fields.subtitleTextColor");
  const state = inherited
    ? translate("settings.value.inheritedText", { source: emptyLabel, value: shown })
    : translate("settings.value.overrideText", { value: shown });
  return <div className="setting-control setting-control--color">
    <div className="setting-row">
      <div className="setting-row__copy"><strong>{label}</strong><em className={`setting-value-state ${inherited ? "is-inherited" : "is-override"}`}>{state}</em></div>
      <div className="setting-row__actions">
        <input aria-label={label} type="color" value={shown} onChange={(event) => onChange(event.target.value.toUpperCase())} />
        <output>{shown}</output>
        {!inherited && <SettingInheritAction source={emptyLabel} settingLabel={label} onClick={() => onChange(null)} />}
      </div>
    </div>
  </div>;
}

function SelectSetting({ name, label, description, value, defaultValue, options, emptyLabel, presentation, onChange }: { name?: string; label: string; description?: string; value: string | null | undefined; defaultValue: string; options: ReadonlyArray<SettingOption>; emptyLabel: string; presentation?: "theme" | "density"; onChange: (value: string | null) => void }) {
  const inherited = value === null || value === undefined;
  const shown = value ?? defaultValue;
  const shownOption = options.find((option) => option.value === shown);
  const shownLabel = shownOption ? ("labelKey" in shownOption ? translate(shownOption.labelKey) : shownOption.label) : shown;
  const state = inherited
    ? translate("settings.value.inheritedText", { source: emptyLabel, value: shownLabel })
    : translate("settings.value.overrideText", { value: shownLabel });
  const copy = <div className="setting-row__copy"><strong>{label}</strong>{description && <small>{description}</small>}<em className={`setting-value-state ${inherited ? "is-inherited" : "is-override"}`}>{state}</em></div>;
  if (presentation) {
    const radioName = name ?? (presentation === "theme" ? "theme" : "cardDensity");
    return <div className={`setting-control setting-control--visual setting-control--${presentation}`}>
      <div className="setting-row">
        {copy}
        <div className="setting-row__actions setting-row__actions--visual">
          <div className={`setting-visual-options setting-visual-options--${presentation}`} role="radiogroup" aria-label={label}>
            {options.map((option) => {
              const optionLabel = "labelKey" in option ? translate(option.labelKey) : option.label;
              const effectiveOption = option.value === shown;
              return <label key={option.value} className={`setting-visual-option ${effectiveOption ? "is-effective" : ""} ${effectiveOption && inherited ? "is-inherited" : ""}`}>
                <input type="radio" name={radioName} value={option.value} checked={effectiveOption} onClick={() => { if (inherited && effectiveOption) onChange(option.value); }} onChange={() => onChange(option.value)} />
                <span className="setting-visual-option__preview" aria-hidden="true"><i /><i /><i /></span>
                <strong>{optionLabel}</strong>
              </label>;
            })}
          </div>
          {!inherited && <SettingInheritAction source={emptyLabel} settingLabel={label} onClick={() => onChange(null)} />}
        </div>
      </div>
    </div>;
  }
  return <div className="setting-control">
    <div className="setting-row">
      {copy}
      <div className="setting-row__actions">
        <Select name={name} aria-label={label} value={value ?? ""} onChange={(nextValue) => onChange(nextValue || null)} options={[{ value: "", label: translate("settings.actions.useInherited", { source: emptyLabel.toLowerCase() }) }, ...options.map((option) => ({ value: option.value, label: "labelKey" in option ? translate(option.labelKey) : option.label }))]} />
        {!inherited && <SettingInheritAction source={emptyLabel} settingLabel={label} onClick={() => onChange(null)} />}
      </div>
    </div>
  </div>;
}

function TextSetting({ label, value, defaultValue, placeholder, emptyLabel, list, description, onChange, children }: { label: string; value: string | null | undefined; defaultValue: string; placeholder: string; emptyLabel: string; list?: string; description?: string; onChange: (value: string | null) => void; children?: React.ReactNode }) {
  const inherited = value === null || value === undefined;
  const shown = value ?? defaultValue;
  const state = inherited
    ? translate("settings.value.inheritedText", { source: emptyLabel, value: shown })
    : translate("settings.value.overrideText", { value: shown });
  return <div className="setting-control setting-control--text">
    <div className="setting-row">
      <div className="setting-row__copy"><strong>{label}</strong>{description && <small>{description}</small>}<em className={`setting-value-state ${inherited ? "is-inherited" : "is-override"}`}>{state}</em></div>
      <div className="setting-row__actions">
        <span className="setting-text-input"><input aria-label={label} list={list} value={value ?? ""} onChange={(event) => onChange(event.target.value || null)} placeholder={inherited ? shown : placeholder} />{children}</span>
        {!inherited && <SettingInheritAction source={emptyLabel} settingLabel={label} onClick={() => onChange(null)} />}
      </div>
    </div>
  </div>;
}
