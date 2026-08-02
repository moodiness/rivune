import { CalendarX2, ChevronLeft, ChevronRight, Film, LoaderCircle, RefreshCw, Tv } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { api, APIError } from "../api";
import { useAuth } from "../auth";
import { Button, EmptyState, IconButton, Notice, SectionHeading } from "../components";
import { locale, translate as t } from "../i18n";
import type { CalendarEvent, MediaItem } from "../types";

type Month = { year: number; month: number };
type MonthBounds = { from: string; to: string; days: number; firstWeekday: number };

const exactDatePattern = /^(\d{4})-(\d{2})-(\d{2})$/;

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

function boundsFor(month: Month): MonthBounds {
  const days = new Date(month.year, month.month + 1, 0).getDate();
  return {
    from: dateKey(month.year, month.month, 1),
    to: dateKey(month.year, month.month, days),
    days,
    firstWeekday: new Date(month.year, month.month, 1).getDay(),
  };
}

function moveMonth(month: Month, offset: number): Month {
  const date = new Date(month.year, month.month + offset, 1);
  return { year: date.getFullYear(), month: date.getMonth() };
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
  return <button type="button" className={`calendar-event calendar-event--${event.mediaType}`} onClick={() => onOpen(mediaFromEvent(event))} aria-label={t("calendar.event.openDetails", { title: event.title })}>
    <span className="calendar-event__poster">
      {event.posterUrl ? <img src={event.posterUrl} alt="" loading="lazy" /> : <Icon size={18} aria-hidden="true" />}
    </span>
    <span className="calendar-event__copy">
      <span className="calendar-event__kind"><Icon size={12} aria-hidden="true" />{t(event.mediaType === "movie" ? "calendar.event.movie" : "calendar.event.episode")}</span>
      <strong>{event.title}</strong>
      <small>{eventContext(event)}</small>
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
  const profileID = activeProfile?.id ?? "";
  const bounds = useMemo(() => boundsFor(month), [month]);
  const monthDate = useMemo(() => new Date(month.year, month.month, 1), [month]);
  const { monthFormatter, fullDateFormatter, weekdays } = useMemo(() => {
    const monthFormatter = new Intl.DateTimeFormat(locale, { month: "long", year: "numeric" });
    const fullDateFormatter = new Intl.DateTimeFormat(locale, { weekday: "long", month: "long", day: "numeric" });
    const weekdayFormatter = new Intl.DateTimeFormat(locale, { weekday: "short" });
    return {
      monthFormatter,
      fullDateFormatter,
      weekdays: Array.from({ length: 7 }, (_, index) => weekdayFormatter.format(new Date(2024, 0, 7 + index))),
    };
  }, [locale]);
  const monthLabel = monthFormatter.format(monthDate);
  const todayKey = dateKey(now.getFullYear(), now.getMonth(), now.getDate());

  useEffect(() => {
    const sequence = ++requestSequence.current;
    const controller = new AbortController();
    setEvents([]);
    setError("");
    setLoading(Boolean(profileID));
    if (!profileID) return () => controller.abort();

    void api.calendar(bounds.from, bounds.to, controller.signal).then((response) => {
      if (controller.signal.aborted || sequence !== requestSequence.current) return;
      setEvents(response.events.filter((event) => {
        const date = localDate(event.releaseDate);
        return date !== null && event.releaseDate >= bounds.from && event.releaseDate <= bounds.to;
      }));
    }).catch((cause: unknown) => {
      if (controller.signal.aborted || sequence !== requestSequence.current) return;
      setError(cause instanceof APIError ? cause.message : t("calendar.error.load"));
    }).finally(() => {
      if (!controller.signal.aborted && sequence === requestSequence.current) setLoading(false);
    });

    return () => controller.abort();
  }, [bounds.from, bounds.to, profileID, reloadKey]);

  const groupedEvents = useMemo(() => {
    const groups = new Map<string, CalendarEvent[]>();
    for (const event of events) {
      const group = groups.get(event.releaseDate);
      if (group) group.push(event);
      else groups.set(event.releaseDate, [event]);
    }
    return groups;
  }, [events]);

  const datedGroups = useMemo(() => Array.from(groupedEvents, ([date, items]) => ({ date, items })), [groupedEvents]);
  const totalCells = Math.ceil((bounds.firstWeekday + bounds.days) / 7) * 7;
  const cells = Array.from({ length: totalCells }, (_, index) => {
    const day = index - bounds.firstWeekday + 1;
    return day >= 1 && day <= bounds.days ? day : null;
  });

  return <div className="standard-page calendar-page page-enter">
    <SectionHeading
      eyebrow={t("calendar.heading.eyebrow")}
      title={t("calendar.heading.title")}
      description={t("calendar.heading.description")}
      action={<div className="calendar-toolbar" aria-label={t("calendar.navigation.label")}>
        <IconButton label={t("calendar.navigation.previousMonth")} onClick={() => setMonth((current) => moveMonth(current, -1))}><ChevronLeft size={20} /></IconButton>
        <h3 aria-live="polite">{monthLabel}</h3>
        <IconButton label={t("calendar.navigation.nextMonth")} onClick={() => setMonth((current) => moveMonth(current, 1))}><ChevronRight size={20} /></IconButton>
        <Button type="button" variant="ghost" onClick={() => setMonth({ year: now.getFullYear(), month: now.getMonth() })}>{t("common.today")}</Button>
      </div>}
    />

    {loading ? <div className="calendar-state" role="status"><LoaderCircle className="spin" size={28} /><strong>{t("calendar.loading.title", { month: monthLabel })}</strong><span>{t("calendar.loading.description")}</span></div>
      : error ? <div className="calendar-state calendar-state--error"><Notice>{error}</Notice><Button type="button" variant="secondary" onClick={() => setReloadKey((value) => value + 1)}><RefreshCw size={17} /> {t("common.retry")}</Button></div>
        : events.length === 0 ? <EmptyState icon={<CalendarX2 size={38} />} title={t("calendar.empty.title", { month: monthLabel })} description={t("calendar.empty.description")} />
          : <>
            <div className="calendar-weekdays" aria-hidden="true">{weekdays.map((weekday) => <span key={weekday}>{weekday}</span>)}</div>
            <div className="calendar-grid" role="region" aria-label={t("calendar.grid.label", { month: monthLabel })}>
              {cells.map((day, index) => {
                if (day === null) return <span className="calendar-day calendar-day--outside" aria-hidden="true" key={`outside-${index}`} />;
                const key = dateKey(month.year, month.month, day);
                const date = new Date(month.year, month.month, day);
                const dayEvents = groupedEvents.get(key) ?? [];
                return <section className={`calendar-day${key === todayKey ? " is-today" : ""}${dayEvents.length > 0 ? " has-events" : ""}`} aria-label={fullDateFormatter.format(date)} key={key}>
                  <header><time dateTime={key}>{day}</time>{key === todayKey && <span>{t("common.today")}</span>}</header>
                  <div className="calendar-day__events">{dayEvents.map((event) => <CalendarEventCard event={event} onOpen={onOpenMedia} key={event.id} />)}</div>
                </section>;
              })}
            </div>
            <div className="calendar-agenda" aria-label={t("calendar.agenda.label", { month: monthLabel })}>
              {datedGroups.map(({ date, items }) => {
                const parsed = localDate(date);
                if (!parsed) return null;
                return <section className="calendar-agenda__day" key={date}>
                  <header><time dateTime={date}>{fullDateFormatter.format(parsed)}</time>{date === todayKey && <span>{t("common.today")}</span>}</header>
                  <div>{items.map((event) => <CalendarEventCard event={event} onOpen={onOpenMedia} key={event.id} />)}</div>
                </section>;
              })}
            </div>
          </>}
  </div>;
}
