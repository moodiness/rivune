import { CalendarX2, ChevronLeft, ChevronRight, Film, LoaderCircle, RefreshCw, Tv } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { api, APIError } from "../api";
import { useAuth } from "../auth";
import { Button, EmptyState, handleDirectionalFocus, IconButton, Notice, SectionHeading } from "../components";
import { locale, translate as t } from "../i18n";
import type { CalendarEvent, CalendarResponse, MediaItem } from "../types";

type Month = { year: number; month: number };
type MonthBounds = { from: string; to: string; days: number; firstWeekday: number };

const exactDatePattern = /^(\d{4})-(\d{2})-(\d{2})$/;
const calendarMonthRequests = new Map<string, Promise<CalendarResponse>>();

function loadCalendarMonth(profileID: string, from: string, to: string): Promise<CalendarResponse> {
  const key = `${profileID}:${api.metadataScope()}:${from}:${to}`;
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

export function CalendarPage({ onOpenMedia }: { onOpenMedia: (item: MediaItem) => void }) {
  const { activeProfile } = useAuth();
  const now = new Date();
  const [month, setMonth] = useState<Month>({ year: now.getFullYear(), month: now.getMonth() });
  const [events, setEvents] = useState<CalendarEvent[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [reloadKey, setReloadKey] = useState(0);
  const requestSequence = useRef(0);
  const loadedMonthRef = useRef("");
  const pageRef = useRef<HTMLDivElement>(null);
  const pendingCalendarFocus = useRef<{ day: number; eventIndex?: number } | null>(null);
  const profileID = activeProfile?.id ?? "";
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
    const sequence = ++requestSequence.current;
    let active = true;
    loadedMonthRef.current = "";
    setEvents([]);
    setError("");
    setLoading(Boolean(profileID));
    if (!profileID) return () => { active = false; };

    void loadCalendarMonth(profileID, bounds.from, bounds.to).then((response) => {
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
  }, [bounds.from, bounds.to, profileID, reloadKey]);

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

  return <div className="standard-page calendar-page page-enter" ref={pageRef}>
    <SectionHeading
      eyebrow={t("calendar.heading.eyebrow")}
      title={t("calendar.heading.title")}
      description={t("calendar.heading.description")}
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
  </div>;
}
