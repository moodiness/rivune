import { Bell, BellOff, Check, Clock3, Eye, Film, LoaderCircle, Trash2, Tv } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { api } from "../api";
import { Button, EmptyState, Notice } from "../components";
import { locale, translate as t } from "../i18n";
import { notifyError, notifySuccess } from "../notifications";
import type { LibraryItem, MediaNotification, MediaNotificationSubscription } from "../types";

function notificationContext(notification: MediaNotification): string {
  const episode = notification.seasonNumber !== undefined && notification.episodeNumber !== undefined
    ? t("notifications.episode", { season: notification.seasonNumber, episode: notification.episodeNumber })
    : notification.seasonNumber !== undefined
      ? t("notifications.season", { season: notification.seasonNumber })
      : "";
  return [notification.seriesTitle, episode, notification.releaseDate].filter(Boolean).join(" · ");
}

function notificationKind(kind: MediaNotification["kind"]): string {
  const keys: Record<MediaNotification["kind"], "notifications.kind.calendar-event-upcoming" | "notifications.kind.season-available" | "notifications.kind.episode-available" | "notifications.kind.movie-release"> = {
    "calendar-event-upcoming": "notifications.kind.calendar-event-upcoming",
    "season-available": "notifications.kind.season-available",
    "episode-available": "notifications.kind.episode-available",
    "movie-release": "notifications.kind.movie-release",
  };
  return t(keys[kind]);
}

export function NotificationsPage() {
  const [notifications, setNotifications] = useState<MediaNotification[]>([]);
  const [nextCursor, setNextCursor] = useState("");
  const [library, setLibrary] = useState<LibraryItem[]>([]);
  const [subscriptions, setSubscriptions] = useState<MediaNotificationSubscription[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [mutation, setMutation] = useState("");
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const [page, followed, media] = await Promise.all([
        api.mediaNotifications(),
        api.mediaNotificationSubscriptions(),
        api.library("", 1, 100),
      ]);
      setNotifications(page.notifications);
      setNextCursor(page.nextCursor ?? "");
      setSubscriptions(followed.subscriptions);
      setLibrary(media.items.filter((item) => item.mediaType === "movie" || item.mediaType === "series"));
    } catch (cause) {
      setError(notifyError(cause, t("notifications.error.load")));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  const followed = useMemo(() => new Set(subscriptions.map((subscription) => subscription.titleId)), [subscriptions]);

  async function acknowledge(notification: MediaNotification, state: "read" | "dismissed") {
    setMutation(`notification:${notification.id}`);
    try {
      await api.acknowledgeMediaNotification(notification.id, state);
      if (state === "dismissed") setNotifications((current) => current.filter((item) => item.id !== notification.id));
      else setNotifications((current) => current.map((item) => item.id === notification.id ? { ...item, readAt: item.readAt ?? new Date().toISOString() } : item));
    } catch (cause) {
      setError(notifyError(cause, t("notifications.error.update")));
    } finally {
      setMutation("");
    }
  }

  async function toggleTracking(item: LibraryItem) {
    setMutation(`tracking:${item.titleId}`);
    try {
      if (followed.has(item.titleId)) {
        await api.unfollowMediaNotifications(item.titleId);
        setSubscriptions((current) => current.filter((subscription) => subscription.titleId !== item.titleId));
        notifySuccess(t("notifications.tracking.unfollowed", { title: item.title ?? t("media.untitled") }));
      } else {
        const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
        const subscription = await api.followMediaNotifications(item.titleId, { timezone, horizonDays: 30, leadDays: 1 });
        setSubscriptions((current) => [...current.filter((entry) => entry.titleId !== item.titleId), subscription]);
        notifySuccess(t("notifications.tracking.followed", { title: item.title ?? t("media.untitled") }));
      }
    } catch (cause) {
      setError(notifyError(cause, t("notifications.error.tracking")));
    } finally {
      setMutation("");
    }
  }

  async function loadMore() {
    if (!nextCursor) return;
    setLoadingMore(true);
    try {
      const page = await api.mediaNotifications(nextCursor);
      setNotifications((current) => [...current, ...page.notifications]);
      setNextCursor(page.nextCursor ?? "");
    } catch (cause) {
      setError(notifyError(cause, t("notifications.error.load")));
    } finally {
      setLoadingMore(false);
    }
  }

  return <section className="standard-page notifications-page" aria-labelledby="notifications-title">
    <header className="section-heading"><div><h2 id="notifications-title">{t("notifications.title")}</h2><p>{t("notifications.description")}</p></div></header>
    {error && <Notice tone="error">{error}</Notice>}
    <section className="notifications-inbox" aria-labelledby="notifications-inbox-title" aria-busy={loading}>
      <header><div><h3 id="notifications-inbox-title">{t("notifications.inbox.title")}</h3><p>{t("notifications.inbox.description")}</p></div><Button variant="ghost" onClick={() => void load()} disabled={loading}>{loading ? <LoaderCircle className="spin" size={17} /> : <Bell size={17} />}{t("notifications.refresh")}</Button></header>
      {!loading && notifications.length === 0 ? <EmptyState icon={<BellOff aria-hidden="true" />} title={t("notifications.empty.title")} description={t("notifications.empty.description")} /> : <ol className="notifications-list" aria-live="polite">
        {notifications.map((notification) => {
          const busy = mutation === `notification:${notification.id}`;
          return <li key={notification.id} className={notification.readAt ? "is-read" : "is-unread"}>
            <span className="notifications-list__icon" aria-hidden="true">{notification.kind === "movie-release" ? <Film /> : <Tv />}</span>
            <div><span className="notifications-list__kind">{notificationKind(notification.kind)}</span><h4>{notification.title}</h4><p>{notificationContext(notification)}</p><time dateTime={notification.availableAt}><Clock3 size={13} />{new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(new Date(notification.availableAt))}</time></div>
            <div className="notifications-list__actions">
              {!notification.readAt && <Button variant="ghost" disabled={busy} onClick={() => void acknowledge(notification, "read")}><Eye size={16} />{t("notifications.markRead")}</Button>}
              <Button variant="ghost" disabled={busy} onClick={() => void acknowledge(notification, "dismissed")}><Trash2 size={16} />{t("notifications.dismissItem")}</Button>
            </div>
          </li>;
        })}
      </ol>}
      {nextCursor && <Button className="notifications-load-more" variant="secondary" loading={loadingMore} onClick={() => void loadMore()}>{t("notifications.loadMore")}</Button>}
    </section>

    <section className="notifications-tracking" aria-labelledby="notifications-tracking-title">
      <header><h3 id="notifications-tracking-title">{t("notifications.tracking.title")}</h3><p>{t("notifications.tracking.description")}</p></header>
      {library.length === 0 ? <EmptyState icon={<Bell aria-hidden="true" />} title={t("notifications.tracking.emptyTitle")} description={t("notifications.tracking.emptyDescription")} /> : <ul>
        {library.map((item) => {
          const active = followed.has(item.titleId);
          return <li key={item.titleId}><div>{item.mediaType === "movie" ? <Film aria-hidden="true" /> : <Tv aria-hidden="true" />}<span><strong>{item.title ?? t("media.untitled")}</strong><small>{item.releaseInfo ?? t(item.mediaType === "movie" ? "calendar.event.movie" : "media.type.series")}</small></span></div><Button variant={active ? "secondary" : "primary"} loading={mutation === `tracking:${item.titleId}`} aria-pressed={active} onClick={() => void toggleTracking(item)}>{active ? <Check size={17} /> : <Bell size={17} />}{t(active ? "notifications.tracking.following" : "notifications.tracking.follow")}</Button></li>;
        })}
      </ul>}
    </section>
  </section>;
}
