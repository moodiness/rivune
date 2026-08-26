import { useEffect, useRef, useState } from "react";
import { useAccessibility } from "./accessibility";
import { translate as t } from "./i18n";
import type { AccessibilityPreferences } from "./types";
const fields = [
  { key: "reducedMotion", labelKey: "accessibility.motion", descriptionKey: "accessibility.motionDescription", options: [
    { value: "system", labelKey: "accessibility.motion.system" }, { value: "reduce", labelKey: "accessibility.motion.reduce" }, { value: "no-preference", labelKey: "accessibility.motion.allow" },
  ] },
  { key: "highContrast", labelKey: "accessibility.contrast", descriptionKey: "accessibility.contrastDescription", options: [
    { value: "system", labelKey: "accessibility.contrast.system" }, { value: "more", labelKey: "accessibility.contrast.more" }, { value: "standard", labelKey: "accessibility.contrast.standard" },
  ] },
  { key: "textScale", labelKey: "accessibility.textScale", descriptionKey: "accessibility.textScaleDescription", options: [
    { value: 100, label: "100%" }, { value: 115, label: "115%" }, { value: 130, label: "130%" },
  ] },
  { key: "captions", labelKey: "accessibility.captions", descriptionKey: "accessibility.captionsDescription", options: [
    { value: "system", labelKey: "accessibility.captions.system" }, { value: "on", labelKey: "accessibility.captions.on" }, { value: "off", labelKey: "accessibility.captions.off" },
  ] },
  { key: "focusIndicators", labelKey: "accessibility.focus", descriptionKey: "accessibility.focusDescription", options: [
    { value: "standard", labelKey: "accessibility.focus.standard" }, { value: "enhanced", labelKey: "accessibility.focus.enhanced" },
  ] },
] as const;

export function AccessibilitySettings() {
  const { document, status, error, save, reload } = useAccessibility();
  const [draft, setDraft] = useState<AccessibilityPreferences>(document);
  const headingRef = useRef<HTMLHeadingElement>(null);

  async function saveAndRestoreFocus(): Promise<void> {
    if (await save(draft)) {
      window.requestAnimationFrame(() => headingRef.current?.focus());
    }
  }
  const dirty = fields.some(({ key }) => draft[key] !== document[key]) || draft.audioDescription !== document.audioDescription;

  useEffect(() => { setDraft(document); }, [document]);

  function change(key: keyof AccessibilityPreferences, value: string | number | boolean) {
    setDraft((current) => ({ ...current, [key]: value } as AccessibilityPreferences));
  }

  return <section className="accessibility-settings" aria-labelledby="accessibility-settings-title" aria-busy={status === "loading" || status === "saving"}>
    <header>
      <div><h4 ref={headingRef} tabIndex={-1} id="accessibility-settings-title">{t("accessibility.title")}</h4><p>{t("accessibility.description")}</p></div>
      <span className={`accessibility-settings__state is-${status}`} role="status" aria-live="polite">
        {t(status === "loading" ? "accessibility.status.loading" : status === "saving" ? "accessibility.status.saving" : status === "conflict" ? "accessibility.status.conflict" : status === "error" ? "accessibility.status.error" : dirty ? "accessibility.status.dirty" : "accessibility.status.saved")}
      </span>
    </header>
    {(status === "conflict" || status === "error") && <div className="accessibility-settings__error" role="alert"><p>{error}</p><button type="button" onClick={() => void reload()}>{t("accessibility.reload")}</button></div>}
    <fieldset disabled={status === "loading" || status === "saving"}>
      {fields.map((field) => <label className="field" key={field.key}>
        <span>{t(field.labelKey)}</span>
        <select value={draft[field.key]} aria-describedby={`accessibility-${field.key}-description`} onChange={(event) => change(field.key, field.key === "textScale" ? Number(event.target.value) : event.target.value)}>
          {field.options.map((option) => <option value={option.value} key={option.value}>{"label" in option ? option.label : t(option.labelKey)}</option>)}
        </select>
        <small id={`accessibility-${field.key}-description`}>{t(field.descriptionKey)}</small>
      </label>)}
      <label className="toggle-field">
        <input type="checkbox" checked={draft.audioDescription} aria-describedby="accessibility-audio-description" onChange={(event) => change("audioDescription", event.target.checked)} />
        <span><i /><div><strong>{t("accessibility.audioDescription")}</strong><small id="accessibility-audio-description">{t("accessibility.audioDescriptionDescription")}</small></div></span>
      </label>
    </fieldset>
    {dirty && <footer>
      <button type="button" className="button button--secondary" disabled={status === "saving"} onClick={() => setDraft(document)}>{t("common.actions.discardChanges")}</button>
      <button type="button" className="button button--primary" disabled={status === "saving" || status === "conflict"} onClick={() => void saveAndRestoreFocus()}>{t("accessibility.save")}</button>
    </footer>}
  </section>;
}
