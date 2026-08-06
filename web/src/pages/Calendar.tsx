import { CalendarPlus, CalendarX2, Check, ChevronLeft, ChevronRight, Copy, Film, KeyRound, LoaderCircle, RefreshCw, Trash2, Tv } from "lucide-react";
import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { api, APIError } from "../api";
import { principalIdentity, useAuth } from "../auth";
import { Button, ConfirmDialog, EmptyState, handleDirectionalFocus, IconButton, Modal, Notice, SectionHeading } from "../components";
import { locale, translate as t } from "../i18n";
import type { CalendarEvent, CalendarResponse, CalendarSubscription, MediaItem } from "../types";

type Month = { year: number; month: number };
type MonthBounds = { from: string; to: string; days: number; firstWeekday: number };

const exactDatePattern = /^(\d{4})-(\d{2})-(\d{2})$/;
const calendarMonthRequests = new Map<string, Promise<CalendarResponse>>();

function loadCalendarMonth(principalScope: string, from: string, to: string): Promise<CalendarResponse> {
  const key = `${principalScope}:${api.metadataScope()}:${from}:${to}`;
  const pending = calendarMonthRequests.get(key);
  if (pending) return pending;

  const request = api.calendar(from, to);
  calendarMonthRequests.set(key, request);
  const clear = () => {
    if (calendarMonthRequests.get(key) === request) calendarMonthRequests.delete(key);
  };
  void request.then(clear, clear);
  return request;
}

function dateKey(year: number, month: number, day: number): string {
  return `${String(year).padStart(4, "0")}-${String(month + 1).padStart(2, "0")}-${String(day).padStart(2, "0")}`;
}

function localDate(value: string): Date | null {
  const match = exactDatePattern.exec(value);
  if (!match) return null;
  const year = Number(match[1]);
  const month = Number(match[2]) - 1;
  const day = Number(match[3]);
  const date = new Date(year, month, day);
  if (date.getFullYear() !== year || date.getMonth() !== month || date.getDate() !== day) return null;
  return date;
}

function boundsFor(month: Month, weekStart: number): MonthBounds {
  const days = new Date(month.year, month.month + 1, 0).getDate();
  const firstDay = new Date(month.year, month.month, 1).getDay();
  return {
    from: dateKey(month.year, month.month, 1),
    to: dateKey(month.year, month.month, days),
    days,
    firstWeekday: (firstDay - weekStart + 7) % 7,
  };
}

function moveMonth(month: Month, offset: number): Month {
  const date = new Date(month.year, month.month + offset, 1);
  return { year: date.getFullYear(), month: date.getMonth() };
}

function firstWeekdayForLocale(value: string): number {
  try {
    const localized = new Intl.Locale(value) as unknown as {
      getWeekInfo?: () => { firstDay: number };
      weekInfo?: { firstDay: number };
    };
    const firstDay = localized.getWeekInfo?.().firstDay ?? localized.weekInfo?.firstDay;
    if (typeof firstDay === "number" && firstDay >= 1 && firstDay <= 7) return firstDay % 7;
  } catch {
    // Intl.Locale can reject malformed runtime locale overrides.
  }
  return 1;
}

function compareText(left: string, right: string, collator: Intl.Collator): number {
  const localized = collator.compare(left, right);
  if (localized !== 0) return localized;
  return left < right ? -1 : left > right ? 1 : 0;
}

function compareCalendarEvents(left: CalendarEvent, right: CalendarEvent, collator: Intl.Collator): number {
  const dateOrder = compareText(left.releaseDate, right.releaseDate, collator);
  if (dateOrder !== 0) return dateOrder;
  const seriesOrder = compareText(left.seriesTitle ?? "", right.seriesTitle ?? "", collator);
  if (seriesOrder !== 0) return seriesOrder;
  const seasonOrder = (left.seasonNumber ?? -1) - (right.seasonNumber ?? -1);
  if (seasonOrder !== 0) return seasonOrder;
  const episodeOrder = (left.episodeNumber ?? -1) - (right.episodeNumber ?? -1);
  if (episodeOrder !== 0) return episodeOrder;
  const titleOrder = compareText(left.title, right.title, collator);
  if (titleOrder !== 0) return titleOrder;
  const typeOrder = compareText(left.mediaType, right.mediaType, collator);
  if (typeOrder !== 0) return typeOrder;
  return compareText(left.id, right.id, collator);
}

function focusCalendarElement(element?: HTMLElement | null): void {
  if (!element) return;
  element.focus();
  element.scrollIntoView({ block: "nearest", inline: "nearest" });
}

function eventContext(event: CalendarEvent): string {
  if (event.mediaType === "movie") return t("calendar.event.movie");
  const episodeCode = [
    event.seasonNumber !== undefined ? `S${String(event.seasonNumber).padStart(2, "0")}` : "",
    event.episodeNumber !== undefined ? `E${String(event.episodeNumber).padStart(2, "0")}` : "",
  ].filter(Boolean).join(" ");
  return [event.seriesTitle, episodeCode].filter(Boolean).join(" · ") || t("calendar.event.episode");
}

function mediaFromEvent(event: CalendarEvent): MediaItem {
  const episodeCode = event.seasonNumber !== undefined && event.episodeNumber !== undefined
    ? `S${String(event.seasonNumber).padStart(2, "0")}E${String(event.episodeNumber).padStart(2, "0")}`
    : "";
  if (event.mediaType === "movie") {
    return {
      id: event.resourceId || event.titleId,
      titleId: event.titleId,
      mediaType: "movie",
      title: event.title,
      posterUrl: event.posterUrl,
      backgroundUrl: event.posterUrl,
      releaseInfo: event.releaseDate,
      released: event.releaseDate,
      externalIds: event.resourceId && event.resourceProvider ? { [event.resourceProvider]: event.resourceId } : undefined,
    };
  }
  return {
    id: event.resourceId || event.seriesId || event.titleId,
    titleId: event.titleId,
    mediaType: "episode",
    title: [event.seriesTitle, episodeCode, event.title].filter(Boolean).join(" · "),
    posterUrl: event.posterUrl,
    backgroundUrl: event.posterUrl,
    releaseInfo: event.releaseDate,
    released: event.releaseDate,
    raw: {
      continueSeriesId: event.seriesId,
      continueSeasonId: event.seasonId,
      continueSeasonNumber: event.seasonNumber,
      continueEpisodeNumber: event.episodeNumber,
      continueEpisodeId: event.titleId,
    },
  };
}

function CalendarEventCard({ event, onOpen }: { event: CalendarEvent; onOpen: (item: MediaItem) => void }) {
  const Icon = event.mediaType === "movie" ? Film : Tv;
  const context = eventContext(event);
  const openLabel = t("calendar.event.openDetails", { title: event.title });
  return <button
    type="button"
    className={`calendar-event calendar-event--${event.mediaType}`}
    data-calendar-event
    onClick={() => onOpen(mediaFromEvent(event))}
    aria-label={`${openLabel} · ${context}`}
    title={`${event.title} · ${context}`}
  >
    <span className="calendar-event__poster">
      {event.posterUrl ? <img src={event.posterUrl} alt="" loading="lazy" /> : <Icon size={18} aria-hidden="true" />}
    </span>
    <span className="calendar-event__copy">
      <span className="calendar-event__kind"><Icon size={12} aria-hidden="true" />{t(event.mediaType === "movie" ? "calendar.event.movie" : "calendar.event.episode")}</span>
      <strong>{event.title}</strong>
      <small>{context}</small>
    </span>
  </button>;
}

type SubscriptionConfirmation = "rotate" | "disable";
type SubscriptionMutation = "create" | SubscriptionConfirmation;

function CalendarSubscriptionModal({ profileId, onClose }: { profileId: string; onClose: () => void }) {
  const [subscription, setSubscription] = useState<CalendarSubscription | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadRevision, setLoadRevision] = useState(0);
  const [mutation, setMutation] = useState<SubscriptionMutation | null>(null);
  const [confirmation, setConfirmation] = useState<SubscriptionConfirmation | null>(null);
  const [error, setError] = useState("");
  const [copyFeedback, setCopyFeedback] = useState<"copied" | "error" | "">("");
  const urlInputId = useId();
  const urlInputRef = useRef<HTMLInputElement>(null);
  const inactiveActionsRef = useRef<HTMLDivElement>(null);
  const headingId = useId();
  const descriptionId = useId();

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError("");
    void api.calendarSubscription(profileId).then((next) => {
      if (active) setSubscription(next);
    }).catch(() => {
      if (active) setError(t("calendar.subscription.error.load"));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [loadRevision, profileId]);

  async function createSubscription(): Promise<void> {
    setMutation("create");
    setError("");
    setCopyFeedback("");
    try {
      const next = await api.createCalendarSubscription(profileId);
      setSubscription(next);
      window.requestAnimationFrame(() => urlInputRef.current?.focus());
    } catch {
      setError(t("calendar.subscription.error.create"));
    } finally {
      setMutation(null);
    }
  }

  async function rotateSubscription(): Promise<void> {
    setMutation("rotate");
    setError("");
    setCopyFeedback("");
    try {
      const next = await api.rotateCalendarSubscription(profileId);
      setSubscription(next);
      window.requestAnimationFrame(() => urlInputRef.current?.focus());
    } catch {
      setError(t("calendar.subscription.error.rotate"));
    } finally {
      setMutation(null);
      setConfirmation(null);
    }
  }

  async function disableSubscription(): Promise<void> {
    setMutation("disable");
    setError("");
    setCopyFeedback("");
    try {
      await api.deleteCalendarSubscription(profileId);
      setSubscription({ active: false });
      window.requestAnimationFrame(() => inactiveActionsRef.current?.querySelector<HTMLButtonElement>("button")?.focus());
    } catch {
      setError(t("calendar.subscription.error.disable"));
    } finally {
      setMutation(null);
      setConfirmation(null);
    }
  }

  async function copySubscriptionURL(): Promise<void> {
    if (!subscription?.url) return;
    setCopyFeedback("");
    try {
      await navigator.clipboard.writeText(subscription.url);
      setCopyFeedback("copied");
    } catch {
      setCopyFeedback("error");
    }
  }

  return <>
    <Modal onClose={mutation ? () => undefined : onClose} className="calendar-subscription-modal" aria-labelledby={headingId} aria-describedby={descriptionId}>
      <header className="calendar-subscription-modal__heading">
        <span><CalendarPlus size={18} aria-hidden="true" /> {t("calendar.subscription.action")}</span>
        <h2 id={headingId} data-autofocus="true" tabIndex={-1}>{t("calendar.subscription.title")}</h2>
        <p id={descriptionId}>{t("calendar.subscription.description")}</p>
      </header>

      {loading ? <div className="calendar-subscription-state" role="status" aria-live="polite"><LoaderCircle className="spin" size={24} aria-hidden="true" /><span>{t("calendar.subscription.loading")}</span></div>
        : subscription === null ? <div className="calendar-subscription-state calendar-subscription-state--error">
          <Notice>{error || t("calendar.subscription.error.load")}</Notice>
          <Button type="button" variant="secondary" onClick={() => setLoadRevision((value) => value + 1)}><RefreshCw size={17} aria-hidden="true" /> {t("common.retry")}</Button>
        </div>
          : <div className="calendar-subscription-content" aria-busy={mutation !== null || undefined}>
            {error && <Notice>{error}</Notice>}
            {subscription.active ? <>
              <div className="calendar-subscription-status"><Check size={18} aria-hidden="true" /><p>{t("calendar.subscription.active")}</p></div>
              {subscription.url ? <>
                <div className="calendar-subscription-url">
                  <label htmlFor={urlInputId}>{t("calendar.subscription.urlLabel")}</label>
                  <div><input ref={urlInputRef} id={urlInputId} value={subscription.url} readOnly dir="ltr" spellCheck={false} autoComplete="off" /><Button type="button" variant="secondary" disabled={mutation !== null} onClick={() => void copySubscriptionURL()}>{copyFeedback === "copied" ? <Check size={17} aria-hidden="true" /> : <Copy size={17} aria-hidden="true" />} {t("calendar.subscription.copy")}</Button></div>
                </div>
                <p className={`calendar-subscription-feedback${copyFeedback ? " is-visible" : ""}${copyFeedback === "error" ? " is-error" : ""}`} role="status" aria-live="polite" aria-atomic="true">{copyFeedback === "copied" ? t("calendar.subscription.copied") : copyFeedback === "error" ? t("calendar.subscription.error.copy") : ""}</p>
                <Notice tone="warning">{t("calendar.subscription.warning")}</Notice>
              </> : <div className="calendar-subscription-hidden"><KeyRound size={18} aria-hidden="true" /><p>{t("calendar.subscription.hidden")}</p></div>}
              <p className="calendar-subscription-window">{t("calendar.subscription.window")}</p>
              <div className="calendar-subscription-actions" onKeyDown={(event) => { handleDirectionalFocus(event, { orientation: getComputedStyle(event.currentTarget).display === "grid" ? "vertical" : "horizontal", wrap: true }); }}>
                <Button type="button" variant="secondary" disabled={mutation !== null} onClick={() => setConfirmation("rotate")}><RefreshCw size={17} aria-hidden="true" /> {t("calendar.subscription.regenerate")}</Button>
                <Button type="button" variant="danger" disabled={mutation !== null} onClick={() => setConfirmation("disable")}><Trash2 size={17} aria-hidden="true" /> {t("calendar.subscription.disable")}</Button>
              </div>
            </> : <>
              <div className="calendar-subscription-hidden"><KeyRound size={18} aria-hidden="true" /><p>{t("calendar.subscription.hidden")}</p></div>
              <p className="calendar-subscription-window">{t("calendar.subscription.window")}</p>
              <div ref={inactiveActionsRef} className="calendar-subscription-actions" onKeyDown={(event) => { handleDirectionalFocus(event, { orientation: "vertical", wrap: true }); }}><Button type="button" loading={mutation === "create"} disabled={mutation !== null} onClick={() => void createSubscription()}><CalendarPlus size={17} aria-hidden="true" /> {t("calendar.subscription.create")}</Button></div>
            </>}
          </div>}
    </Modal>
    {confirmation === "rotate" && <ConfirmDialog
      title={t("calendar.subscription.regenerate.title")}
      description={t("calendar.subscription.regenerate.description")}
      confirmLabel={t("calendar.subscription.regenerate.confirm")}
      loading={mutation === "rotate"}
      onCancel={() => setConfirmation(null)}
      onConfirm={() => void rotateSubscription()}
    />}
    {confirmation === "disable" && <ConfirmDialog
      title={t("calendar.subscription.disable.title")}
      description={t("calendar.subscription.disable.description")}
      confirmLabel={t("calendar.subscription.disable.confirm")}
      loading={mutation === "disable"}
      onCancel={() => setConfirmation(null)}
      onConfirm={() => void disableSubscription()}
    />}
  </>;
}

export function CalendarPage({ onOpenMedia }: { onOpenMedia: (item: MediaItem) => void }) {
  const { account, activeProfile, discovery } = useAuth();
  const now = new Date();
  const [month, setMonth] = useState<Month>({ year: now.getFullYear(), month: now.getMonth() });
  const [events, setEvents] = useState<CalendarEvent[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [reloadKey, setReloadKey] = useState(0);
  const [subscriptionOpen, setSubscriptionOpen] = useState(false);
  const requestSequence = useRef(0);
  const loadedMonthRef = useRef("");
  const pageRef = useRef<HTMLDivElement>(null);
  const subscriptionOpenerRef = useRef<HTMLButtonElement | null>(null);
  const pendingCalendarFocus = useRef<{ day: number; eventIndex?: number } | null>(null);
  const principalScope = principalIdentity(discovery, account, activeProfile);
  const weekStart = useMemo(() => firstWeekdayForLocale(locale), [locale]);
  const bounds = useMemo(() => boundsFor(month, weekStart), [month, weekStart]);
  const monthDate = useMemo(() => new Date(month.year, month.month, 1), [month]);
  const eventCollator = useMemo(() => new Intl.Collator(locale, { numeric: true, sensitivity: "base" }), [locale]);
  const { monthFormatter, fullDateFormatter, agendaDateFormatter, weekdays } = useMemo(() => {
    const monthFormatter = new Intl.DateTimeFormat(locale, { month: "long", year: "numeric" });
    const fullDateFormatter = new Intl.DateTimeFormat(locale, { weekday: "long", month: "long", day: "numeric" });
    const agendaDateFormatter = new Intl.DateTimeFormat(locale, { weekday: "short", day: "numeric" });
    const weekdayFormatter = new Intl.DateTimeFormat(locale, { weekday: "short" });
    return {
      monthFormatter,
      fullDateFormatter,
      agendaDateFormatter,
      weekdays: Array.from({ length: 7 }, (_, index) => weekdayFormatter.format(new Date(2024, 0, 7 + ((weekStart + index) % 7)))),
    };
  }, [locale, weekStart]);
  const monthLabel = monthFormatter.format(monthDate);
  const todayKey = dateKey(now.getFullYear(), now.getMonth(), now.getDate());

  useEffect(() => {
    setSubscriptionOpen(false);
    subscriptionOpenerRef.current = null;
  }, [activeProfile?.id]);

  useEffect(() => {
    const sequence = ++requestSequence.current;
    let active = true;
    loadedMonthRef.current = "";
    setEvents([]);
    setError("");
    setLoading(principalScope !== null);
    if (principalScope === null) return () => { active = false; };

    void loadCalendarMonth(principalScope, bounds.from, bounds.to).then((response) => {
      if (!active || sequence !== requestSequence.current) return;
      setEvents(response.events.filter((event) => {
        const date = localDate(event.releaseDate);
        return date !== null && event.releaseDate >= bounds.from && event.releaseDate <= bounds.to;
      }));
    }).catch((cause: unknown) => {
      if (!active || sequence !== requestSequence.current) return;
      setError(cause instanceof APIError ? cause.message : t("calendar.error.load"));
    }).finally(() => {
      if (active && sequence === requestSequence.current) {
        loadedMonthRef.current = bounds.from;
        setLoading(false);
      }
    });

    return () => { active = false; };
  }, [bounds.from, bounds.to, principalScope, reloadKey]);

  useEffect(() => {
    if (loading || loadedMonthRef.current !== bounds.from || !pendingCalendarFocus.current) return;
    const pending = pendingCalendarFocus.current;
    pendingCalendarFocus.current = null;
    const frame = window.requestAnimationFrame(() => {
      const page = pageRef.current;
      if (!page) return;
      const day = Math.min(pending.day, bounds.days);
      const agendaDay = page.querySelector<HTMLElement>(`.calendar-agenda__day[data-calendar-day="${day}"]`);
      const agendaEvent = pending.eventIndex === undefined ? null : agendaDay?.querySelectorAll<HTMLElement>("[data-calendar-event]")[pending.eventIndex];
      const gridDay = page.querySelector<HTMLElement>(`.calendar-day[data-calendar-day="${day}"]`);
      const gridEvent = pending.eventIndex === undefined ? null : gridDay?.querySelectorAll<HTMLElement>("[data-calendar-event]")[pending.eventIndex];
      const visibleEvent = [agendaEvent, gridEvent].find((candidate) => candidate && candidate.offsetParent !== null);
      const visibleDay = gridDay?.offsetParent !== null ? gridDay : null;
      focusCalendarElement(visibleEvent ?? visibleDay ?? page.querySelector<HTMLElement>(".calendar-toolbar button"));
    });
    return () => window.cancelAnimationFrame(frame);
  }, [bounds.days, bounds.from, loading, month.month, month.year]);

  const sortedEvents = useMemo(() => [...events].sort((left, right) => compareCalendarEvents(left, right, eventCollator)), [eventCollator, events]);
  const groupedEvents = useMemo(() => {
    const groups = new Map<string, CalendarEvent[]>();
    for (const event of sortedEvents) {
      const group = groups.get(event.releaseDate);
      if (group) group.push(event);
      else groups.set(event.releaseDate, [event]);
    }
    return groups;
  }, [sortedEvents]);

  const datedGroups = useMemo(() => Array.from(groupedEvents, ([date, items]) => ({ date, items })).sort((left, right) => left.date.localeCompare(right.date)), [groupedEvents]);
  const totalCells = Math.ceil((bounds.firstWeekday + bounds.days) / 7) * 7;
  const cells = Array.from({ length: totalCells }, (_, index) => {
    const day = index - bounds.firstWeekday + 1;
    return day >= 1 && day <= bounds.days ? day : null;
  });
  const initialFocusableDay = month.year === now.getFullYear() && month.month === now.getMonth() ? now.getDate() : 1;

  function queueMonthChange(offset: number, day: number, eventIndex?: number): void {
    pendingCalendarFocus.current = { day, eventIndex };
    setMonth((current) => moveMonth(current, offset));
  }

  function handleCalendarGridKeyDown(event: ReactKeyboardEvent<HTMLDivElement>): void {
    if (event.altKey || event.ctrlKey || event.metaKey) return;
    const target = event.target instanceof Element ? event.target : null;
    const eventCard = target?.closest<HTMLElement>("[data-calendar-event]");
    const dayElement = target?.closest<HTMLElement>("[data-calendar-day]");
    if (!dayElement || !event.currentTarget.contains(dayElement)) return;
    const dayElements = Array.from(event.currentTarget.querySelectorAll<HTMLElement>(".calendar-day[data-calendar-day]"));
    const dayIndex = dayElements.indexOf(dayElement);
    if (dayIndex < 0) return;
    const day = Number(dayElement.dataset.calendarDay);
    const rtl = getComputedStyle(event.currentTarget).direction === "rtl";

    if (event.key === "PageUp" || event.key === "PageDown") {
      const eventIndex = eventCard ? Array.from(dayElement.querySelectorAll("[data-calendar-event]")).indexOf(eventCard) : undefined;
      event.preventDefault();
      queueMonthChange(event.key === "PageUp" ? -1 : 1, day, eventIndex);
      return;
    }

    if (!eventCard && event.key === "Enter") {
      const firstEvent = dayElement.querySelector<HTMLElement>("[data-calendar-event]");
      if (!firstEvent) return;
      event.preventDefault();
      focusCalendarElement(firstEvent);
      return;
    }

    let dayOffset = 0;
    if (event.key === "ArrowLeft") dayOffset = rtl ? 1 : -1;
    else if (event.key === "ArrowRight") dayOffset = rtl ? -1 : 1;
    else if (event.key === "ArrowUp") dayOffset = -7;
    else if (event.key === "ArrowDown") dayOffset = 7;
    else if (event.key === "Home") dayOffset = -dayIndex;
    else if (event.key === "End") dayOffset = dayElements.length - dayIndex - 1;
    else return;

    let eventIndex: number | undefined;
    if (eventCard) {
      const dayEvents = Array.from(dayElement.querySelectorAll<HTMLElement>("[data-calendar-event]"));
      eventIndex = dayEvents.indexOf(eventCard);
      if (event.key === "ArrowUp" && eventIndex > 0) {
        event.preventDefault();
        focusCalendarElement(dayEvents[eventIndex - 1]);
        return;
      }
      if (event.key === "ArrowDown" && eventIndex < dayEvents.length - 1) {
        event.preventDefault();
        focusCalendarElement(dayEvents[eventIndex + 1]);
        return;
      }
    }

    const nextDay = dayElements[dayIndex + dayOffset];
    if (!nextDay) return;
    event.preventDefault();
    for (const candidate of dayElements) candidate.tabIndex = candidate === nextDay ? 0 : -1;
    focusCalendarElement(eventIndex === undefined ? nextDay : nextDay.querySelectorAll<HTMLElement>("[data-calendar-event]")[eventIndex] ?? nextDay);
  }

  function handleAgendaKeyDown(event: ReactKeyboardEvent<HTMLDivElement>): void {
    if (event.altKey || event.ctrlKey || event.metaKey) return;
    const target = event.target instanceof Element ? event.target.closest<HTMLElement>("[data-calendar-event]") : null;
    if (!target || !event.currentTarget.contains(target)) return;
    const dayElement = target.closest<HTMLElement>("[data-calendar-day]");
    const day = Number(dayElement?.dataset.calendarDay);
    const dayEvents = Array.from(dayElement?.querySelectorAll<HTMLElement>("[data-calendar-event]") ?? []);
    const eventIndex = dayEvents.indexOf(target);
    if (event.key === "PageUp" || event.key === "PageDown") {
      event.preventDefault();
      queueMonthChange(event.key === "PageUp" ? -1 : 1, day, eventIndex);
      return;
    }
    if (event.key === "ArrowUp" || event.key === "ArrowDown" || event.key === "Home" || event.key === "End") {
      handleDirectionalFocus(event, { orientation: "vertical" });
      return;
    }
    if (event.key === "ArrowLeft" || event.key === "ArrowRight") handleDirectionalFocus(event, { orientation: "horizontal" });
  }

  function closeSubscription(): void {
    setSubscriptionOpen(false);
    window.requestAnimationFrame(() => subscriptionOpenerRef.current?.focus());
  }

  return <div className="standard-page calendar-page page-enter" ref={pageRef}>
    <SectionHeading
      eyebrow={t("calendar.heading.eyebrow")}
      title={t("calendar.heading.title")}
      description={t("calendar.heading.description")}
      action={activeProfile?.canManage ? <Button type="button" variant="secondary" onClick={(event) => { subscriptionOpenerRef.current = event.currentTarget; setSubscriptionOpen(true); }}><CalendarPlus size={17} aria-hidden="true" /> {t("calendar.subscription.action")}</Button> : undefined}
    />

    <div className="calendar-surface">
      <div className="calendar-toolbar" role="group" aria-label={t("calendar.navigation.label")} onKeyDown={(event) => { handleDirectionalFocus(event, { orientation: "horizontal" }); }}>
        <h3 aria-live="polite" title={monthLabel}>{monthLabel}</h3>
        <div className="calendar-toolbar__actions">
          <IconButton label={t("calendar.navigation.previousMonth")} onClick={() => setMonth((current) => moveMonth(current, -1))}><ChevronLeft size={20} /></IconButton>
          <IconButton label={t("calendar.navigation.nextMonth")} onClick={() => setMonth((current) => moveMonth(current, 1))}><ChevronRight size={20} /></IconButton>
          <Button type="button" variant="ghost" onClick={() => setMonth({ year: now.getFullYear(), month: now.getMonth() })}>{t("common.today")}</Button>
        </div>
      </div>
    {loading ? <div className="calendar-state" role="status"><LoaderCircle className="spin" size={28} /><strong>{t("calendar.loading.title", { month: monthLabel })}</strong><span>{t("calendar.loading.description")}</span></div>
      : error ? <div className="calendar-state calendar-state--error"><Notice>{error}</Notice><Button type="button" variant="secondary" onClick={() => setReloadKey((value) => value + 1)}><RefreshCw size={17} /> {t("common.retry")}</Button></div>
        : <>
          <div className="calendar-weekdays" aria-hidden="true">{weekdays.map((weekday, index) => <span key={`${index}-${weekday}`}>{weekday}</span>)}</div>
          <div className="calendar-grid" role="region" aria-label={t("calendar.grid.label", { month: monthLabel })} onKeyDown={handleCalendarGridKeyDown}>
            {cells.map((day, index) => {
              if (day === null) return <span className="calendar-day calendar-day--outside" aria-hidden="true" key={`outside-${index}`} />;
              const key = dateKey(month.year, month.month, day);
              const date = new Date(month.year, month.month, day);
              const dayEvents = groupedEvents.get(key) ?? [];
              return <section
                className={`calendar-day${key === todayKey ? " is-today" : ""}${dayEvents.length > 0 ? " has-events" : ""}`}
                aria-current={key === todayKey ? "date" : undefined}
                aria-label={fullDateFormatter.format(date)}
                data-calendar-day={day}
                key={key}
                tabIndex={day === initialFocusableDay ? 0 : -1}
                onFocus={(focusEvent) => {
                  if (focusEvent.target !== focusEvent.currentTarget) return;
                  for (const candidate of focusEvent.currentTarget.parentElement?.querySelectorAll<HTMLElement>(".calendar-day[data-calendar-day]") ?? []) candidate.tabIndex = candidate === focusEvent.currentTarget ? 0 : -1;
                }}
              >
                <header><time dateTime={key}>{day}</time>{key === todayKey && <span>{t("common.today")}</span>}</header>
                <div className="calendar-day__events">{dayEvents.map((calendarEvent) => <CalendarEventCard event={calendarEvent} onOpen={onOpenMedia} key={calendarEvent.id} />)}</div>
              </section>;
            })}
          </div>
          {sortedEvents.length === 0 && <div className="calendar-empty calendar-empty--grid"><EmptyState icon={<CalendarX2 size={38} />} title={t("calendar.empty.title", { month: monthLabel })} description={t("calendar.empty.description")} /></div>}
          <div className="calendar-agenda" aria-label={t("calendar.agenda.label", { month: monthLabel })} onKeyDown={handleAgendaKeyDown}>
            {datedGroups.length === 0
              ? <EmptyState icon={<CalendarX2 size={38} />} title={t("calendar.empty.title", { month: monthLabel })} description={t("calendar.empty.description")} />
              : datedGroups.map(({ date, items }) => {
                const parsed = localDate(date);
                if (!parsed) return null;
                return <section className="calendar-agenda__day" data-calendar-day={parsed.getDate()} key={date}>
                  <header><time dateTime={date} aria-label={fullDateFormatter.format(parsed)}>{agendaDateFormatter.format(parsed)}</time>{date === todayKey && <span>{t("common.today")}</span>}</header>
                  <div>{items.map((calendarEvent) => <CalendarEventCard event={calendarEvent} onOpen={onOpenMedia} key={calendarEvent.id} />)}</div>
                </section>;
              })}
          </div>
        </>}
    </div>
    {subscriptionOpen && activeProfile?.canManage && <CalendarSubscriptionModal key={activeProfile.id} profileId={activeProfile.id} onClose={closeSubscription} />}
  </div>;
}
