import { BookmarkPlus, FolderSearch, Pencil, Search, Trash2 } from "lucide-react";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { api, APIError } from "./api";
import { Button, Notice, Select } from "./components";
import { translate as t } from "./i18n";
import { notifyError, notifySuccess } from "./notifications";
import type { MediaItem, SavedSearch, SavedSearchInput, SmartCollection, SmartCollectionItem, SmartCollectionInput, SmartRuleField } from "./types";

type SavedSearchManagerProps = {
  query: string;
  mediaType: string;
  onOpen: (query: string, mediaType: string) => void;
};

const savedMediaTypes: Record<string, true> = { movie: true, series: true, season: true, episode: true, video: true, tv: true };

export function SavedSearchManager({ query, mediaType, onOpen }: SavedSearchManagerProps) {
  const [items, setItems] = useState<SavedSearch[]>([]);
  const [name, setName] = useState("");
  const [editing, setEditing] = useState<SavedSearch | null>(null);
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const sectionRef = useRef<HTMLElement>(null);

  function restoreFocusAfterRemoval(index: number): void {
    window.requestAnimationFrame(() => {
      const rows = sectionRef.current?.querySelectorAll<HTMLElement>(".saved-manager__list > li");
      const target = rows?.[Math.min(index, Math.max(0, rows.length - 1))]?.querySelector<HTMLElement>("button")
        ?? sectionRef.current?.querySelector<HTMLElement>("#saved-searches-heading");
      target?.focus();
    });
  }

  useEffect(() => { void load(); }, []);

  async function load() {
    setLoading(true);
    try {
      setItems((await api.savedSearches()).savedSearches);
      setError("");
    } catch (cause) {
      setError(notifyError(cause, t("savedSearches.error"), t("savedSearches.title")));
    } finally {
      setLoading(false);
    }
  }

  async function save(event: FormEvent) {
    event.preventDefault();
    const normalizedName = name.trim();
    const normalizedQuery = query.trim();
    if (!normalizedName || normalizedQuery.length < 2) return;
    setBusy(true);
    setError("");
    const input: SavedSearchInput = {
      name: normalizedName,
      query: normalizedQuery,
      mediaType: savedMediaTypes[mediaType] ? mediaType as SavedSearchInput["mediaType"] : undefined,
      sort: editing?.sort ?? "relevance",
      expectedRevision: editing?.revision,
    };
    try {
      const saved = editing ? await api.updateSavedSearch(editing.id, input) : await api.createSavedSearch(input);
      setItems((current) => [...current.filter((item) => item.id !== saved.id), saved].sort((left, right) => left.name.localeCompare(right.name)));
      setName("");
      setEditing(null);
      notifySuccess(t(editing ? "savedSearches.updated" : "savedSearches.saved"), t("savedSearches.title"));
    } catch (cause) {
      setError(notifyError(cause, cause instanceof APIError && cause.code === "saved_search_conflict" ? t("smartCollections.conflict") : t("savedSearches.error"), t("savedSearches.title")));
    } finally {
      setBusy(false);
    }
  }

  async function remove(item: SavedSearch) {
    const index = items.findIndex((candidate) => candidate.id === item.id);
    setBusy(true);
    try {
      await api.deleteSavedSearch(item.id, item.revision);
      setItems((current) => current.filter((candidate) => candidate.id !== item.id));
      if (editing?.id === item.id) { setEditing(null); setName(""); }
      notifySuccess(t("savedSearches.deleted"), t("savedSearches.title"));
      restoreFocusAfterRemoval(index);
    } catch (cause) {
      setError(notifyError(cause, t("savedSearches.error"), t("savedSearches.title")));
    } finally {
      setBusy(false);
    }
  }

  return <section ref={sectionRef} className="saved-manager" aria-labelledby="saved-searches-heading" aria-busy={loading || undefined}>
    <header><h2 tabIndex={-1} id="saved-searches-heading">{t("savedSearches.title")}</h2><p>{t("savedSearches.description")}</p></header>
    <form className="saved-manager__form" onSubmit={(event) => void save(event)}>
      <label><span>{t("savedSearches.name")}</span><input value={name} maxLength={120} onChange={(event) => setName(event.target.value)} placeholder={t("savedSearches.namePlaceholder")} /></label>
      <Button type="submit" loading={busy} disabled={!name.trim() || query.trim().length < 2}><BookmarkPlus size={16} /> {editing ? t("common.save") : t("savedSearches.save")}</Button>
      {editing && <Button type="button" variant="ghost" onClick={() => { setEditing(null); setName(""); }}>{t("common.cancel")}</Button>}
    </form>
    {loading && <p className="saved-manager__empty" role="status">{t("common.loading")}</p>}
    {error && <Notice tone="warning"><span>{error}</span><Button type="button" variant="ghost" onClick={() => void load()}>{t("common.actions.retry")}</Button></Notice>}
    {!loading && !error && items.length === 0 ? <p className="saved-manager__empty">{t("savedSearches.empty")}</p> : !loading && items.length > 0 ? <ul className="saved-manager__list">{items.map((item) => <li key={item.id}>
      <button type="button" className="saved-manager__open" onClick={() => onOpen(item.query, item.mediaType ?? "all")} aria-label={t("savedSearches.open", { name: item.name })}><Search size={16} /><span><strong>{item.name}</strong><small>{item.query}</small></span></button>
      <Button type="button" variant="ghost" aria-label={`${t("common.actions.edit")} ${item.name}`} onClick={() => { setEditing(item); setName(item.name); onOpen(item.query, item.mediaType ?? "all"); }}><Pencil size={15} /></Button>
      <Button type="button" variant="ghost" aria-label={`${t("common.actions.delete")} ${item.name}`} onClick={() => void remove(item)}><Trash2 size={15} /></Button>
    </li>)}</ul> : null}
  </section>;
}

type SmartCollectionManagerProps = { onOpenMedia: (item: MediaItem) => void };

const ruleFields: Array<{ value: SmartRuleField; label: string }> = [
  { value: "media_type", label: t("smartCollections.rule.mediaType") },
  { value: "year", label: t("smartCollections.rule.year") },
  { value: "genre", label: t("smartCollections.rule.genre") },
  { value: "status", label: t("smartCollections.rule.status") },
  { value: "rating", label: t("smartCollections.rule.rating") },
  { value: "source", label: t("smartCollections.rule.source") },
];

const smartSortOptions: Array<{ value: SmartCollection["sort"]; label: string }> = [
  { value: "title", label: t("smartCollections.sort.title") },
  { value: "year", label: t("smartCollections.sort.year") },
  { value: "rating", label: t("smartCollections.sort.rating") },
  { value: "added", label: t("smartCollections.sort.added") },
];

function mediaFromSmartItem(item: SmartCollectionItem): MediaItem {
  return {
    id: item.resourceId || item.id,
    titleId: item.id,
    mediaType: item.mediaType,
    title: item.title,
    posterUrl: item.posterUrl,
    backgroundUrl: item.backgroundUrl,
    releaseInfo: item.releaseInfo,
    released: item.released,
    voteAverage: item.communityRating,
    resourceId: item.resourceId,
    sourceAddonId: item.sourceAddonId,
    sourceCatalogId: item.sourceCatalogId,
    sourceName: item.sourceName,
    externalIds: {},
  };
}

export function SmartCollectionManager({ onOpenMedia }: SmartCollectionManagerProps) {
  const [collections, setCollections] = useState<SmartCollection[]>([]);
  const [active, setActive] = useState<SmartCollection | null>(null);
  const [results, setResults] = useState<SmartCollectionItem[]>([]);
  const [editing, setEditing] = useState<SmartCollection | null>(null);
  const [name, setName] = useState("");
  const [field, setField] = useState<SmartRuleField>("genre");
  const [value, setValue] = useState("");
  const [sort, setSort] = useState<SmartCollection["sort"]>("title");
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const sectionRef = useRef<HTMLElement>(null);

  function restoreFocusAfterRemoval(index: number): void {
    window.requestAnimationFrame(() => {
      const rows = sectionRef.current?.querySelectorAll<HTMLElement>(".saved-manager__list > li");
      const target = rows?.[Math.min(index, Math.max(0, rows.length - 1))]?.querySelector<HTMLElement>("button")
        ?? sectionRef.current?.querySelector<HTMLElement>("#smart-collections-heading");
      target?.focus();
    });
  }

  useEffect(() => { void load(); }, []);

  async function load() {
    setLoading(true);
    try { setCollections((await api.smartCollections()).smartCollections); setError(""); }
    catch (cause) { setError(notifyError(cause, t("smartCollections.error"), t("smartCollections.title"))); }
    finally { setLoading(false); }
  }

  function edit(item: SmartCollection) {
    const rule = "rules" in item.rules ? item.rules.rules[0] : item.rules;
    setEditing(item);
    setName(item.name);
    setSort(item.sort);
    if (rule && !("rules" in rule)) {
      setField(rule.type);
      setValue(rule.type === "media_type" ? rule.values?.[0] ?? "" : rule.number === undefined ? rule.value ?? "" : String(rule.number));
    }
  }

  async function save(event: FormEvent) {
    event.preventDefault();
    const normalizedName = name.trim();
    const normalizedValue = value.trim();
    if (!normalizedName || !normalizedValue) return;
    const numeric = field === "year" || field === "rating" ? Number(normalizedValue) : undefined;
    if (numeric !== undefined && !Number.isFinite(numeric)) return;
    const rules: SmartCollectionInput["rules"] = field === "media_type"
      ? { type: "media_type", operator: "one_of", values: [normalizedValue] }
      : numeric !== undefined
        ? { type: field, operator: "gte", number: numeric }
        : { type: field, operator: "equals", value: normalizedValue };
    setBusy(true);
    setError("");
    try {
      const input: SmartCollectionInput = { name: normalizedName, rules, sort, expectedRevision: editing?.revision };
      const saved = editing ? await api.updateSmartCollection(editing.id, input) : await api.createSmartCollection(input);
      setCollections((current) => [...current.filter((item) => item.id !== saved.id), saved].sort((left, right) => left.name.localeCompare(right.name)));
      setEditing(null); setName(""); setValue("");
      notifySuccess(t("smartCollections.saved"), t("smartCollections.title"));
    } catch (cause) {
      setError(notifyError(cause, cause instanceof APIError && cause.code === "saved_search_conflict" ? t("smartCollections.conflict") : t("smartCollections.error"), t("smartCollections.title")));
    } finally { setBusy(false); }
  }

  async function open(item: SmartCollection) {
    setBusy(true); setActive(item); setError("");
    try { setResults((await api.smartCollectionItems(item.id, 1, 100)).items); }
    catch (cause) { setResults([]); setError(notifyError(cause, t("smartCollections.error"), t("smartCollections.title"))); }
    finally { setBusy(false); }
  }

  async function remove(item: SmartCollection) {
    const index = collections.findIndex((candidate) => candidate.id === item.id);
    setBusy(true);
    try {
      await api.deleteSmartCollection(item.id, item.revision);
      setCollections((current) => current.filter((candidate) => candidate.id !== item.id));
      if (active?.id === item.id) { setActive(null); setResults([]); }
      notifySuccess(t("smartCollections.deleted"), t("smartCollections.title"));
      restoreFocusAfterRemoval(index);
    } catch (cause) { setError(notifyError(cause, t("smartCollections.error"), t("smartCollections.title"))); }
    finally { setBusy(false); }
  }

  return <section ref={sectionRef} className="saved-manager smart-manager" aria-labelledby="smart-collections-heading" aria-busy={loading || busy || undefined}>
    <header><h2 tabIndex={-1} id="smart-collections-heading">{t("smartCollections.title")}</h2><p>{t("smartCollections.description")}</p></header>
    <form className="saved-manager__form smart-manager__form" onSubmit={(event) => void save(event)}>
      <label><span>{t("smartCollections.name")}</span><input value={name} maxLength={120} onChange={(event) => setName(event.target.value)} placeholder={t("smartCollections.namePlaceholder")} /></label>
      <label><span>{t("smartCollections.rule.field")}</span><Select value={field} options={ruleFields} onChange={(next) => { setField(next as SmartRuleField); setValue(""); }} /></label>
      <label><span>{t("smartCollections.rule.value")}</span><input value={value} maxLength={128} inputMode={field === "year" || field === "rating" ? "decimal" : "text"} onChange={(event) => setValue(event.target.value)} /></label>
      <label><span>{t("smartCollections.sort")}</span><Select value={sort} options={smartSortOptions} onChange={(next) => setSort(next as SmartCollection["sort"])} /></label>
      <Button type="submit" loading={busy} disabled={!name.trim() || !value.trim()}><FolderSearch size={16} /> {editing ? t("common.save") : t("smartCollections.create")}</Button>
      {editing && <Button type="button" variant="ghost" onClick={() => { setEditing(null); setName(""); setValue(""); }}>{t("common.cancel")}</Button>}
    </form>
    {loading && <p className="saved-manager__empty" role="status">{t("common.loading")}</p>}
    {error && <Notice tone="warning"><span>{error}</span><Button type="button" variant="ghost" onClick={() => void load()}>{t("common.actions.retry")}</Button></Notice>}
    {!loading && !error && collections.length === 0 ? <p className="saved-manager__empty">{t("smartCollections.empty")}</p> : !loading && collections.length > 0 ? <ul className="saved-manager__list">{collections.map((item) => <li key={item.id}>
      <button type="button" className="saved-manager__open" aria-label={t("smartCollections.open", { name: item.name })} onClick={() => void open(item)}><FolderSearch size={16} /><strong>{item.name}</strong></button>
      <Button type="button" variant="ghost" aria-label={t("smartCollections.edit", { name: item.name })} onClick={() => edit(item)}><Pencil size={15} /></Button>
      <Button type="button" variant="ghost" aria-label={`${t("common.actions.delete")} ${item.name}`} onClick={() => void remove(item)}><Trash2 size={15} /></Button>
    </li>)}</ul> : null}
    {active && <section className="smart-manager__results" aria-labelledby="smart-results-heading"><h3 id="smart-results-heading">{active.name}</h3>
      {results.length === 0 && !busy ? <p>{t("smartCollections.noItems")}</p> : <ul>{results.map((item) => <li key={item.id}><button type="button" onClick={() => onOpenMedia(mediaFromSmartItem(item))}><strong>{item.title}</strong>{item.releaseInfo && <small>{item.releaseInfo}</small>}</button></li>)}</ul>}
    </section>}
  </section>;
}
