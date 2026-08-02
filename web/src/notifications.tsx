import { AlertCircle, Bell, Check, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { APIError } from "./api";
import { translate as t } from "./i18n";

type NotificationTone = "error" | "info" | "success";

type AppNotification = {
  id: number;
  tone: NotificationTone;
  title: string;
  message: string;
  markPresented: () => void;
  durationMilliseconds: number;
};

type NotificationListener = (notification: AppNotification) => void;

const listeners = new Set<NotificationListener>();
let nextNotificationID = 1;
const defaultNotificationDurationMilliseconds = 5_000;
let notificationDurationMilliseconds = defaultNotificationDurationMilliseconds;

export function configureNotificationDuration(durationSeconds: number): void {
  notificationDurationMilliseconds = Number.isFinite(durationSeconds)
    ? Math.min(30, Math.max(2, durationSeconds)) * 1_000
    : defaultNotificationDurationMilliseconds;
}

function publish(tone: NotificationTone, title: string, message: string): Promise<void> {
  let markPresented = () => {};
  const presented = new Promise<void>((resolve) => { markPresented = resolve; });
  const notification = { id: nextNotificationID++, tone, title, message, durationMilliseconds: notificationDurationMilliseconds, markPresented };
  for (const listener of listeners) listener(notification);
  return presented;
}

export function notifySuccess(message: string, title = t("notifications.savedTitle")): void {
  void publish("success", title, message);
}

export function notifyInfo(message: string, title = t("notifications.messageTitle")): Promise<void> {
  return publish("info", title, message);
}

export function notifyError(cause: unknown, fallback: string, title = t("notifications.errorTitle")): string {
  const message = cause instanceof APIError ? cause.message : cause instanceof Error && cause.message ? cause.message : fallback;
  void publish("error", title, message);
  return message;
}

export function notifyErrorMessage(message: string, title = t("notifications.errorTitle")): string {
  void publish("error", title, message);
  return message;
}

export function NotificationViewport() {
  const [notifications, setNotifications] = useState<AppNotification[]>([]);
  const notificationTimeouts = useRef(new Map<number, number>());

  useEffect(() => {
    const listener: NotificationListener = (notification) => {
      setNotifications((current) => [...current, notification]);
    };
    listeners.add(listener);
    return () => {
      listeners.delete(listener);
      for (const timeout of notificationTimeouts.current.values()) window.clearTimeout(timeout);
      notificationTimeouts.current.clear();
    };
  }, []);

  useEffect(() => {
    for (const notification of notifications.slice(0, 4)) {
      if (notificationTimeouts.current.has(notification.id)) continue;
      notification.markPresented();
      const timeout = window.setTimeout(() => {
        notificationTimeouts.current.delete(notification.id);
        setNotifications((current) => current.filter((item) => item.id !== notification.id));
      }, notification.durationMilliseconds);
      notificationTimeouts.current.set(notification.id, timeout);
    }
  }, [notifications]);

  if (notifications.length === 0) return null;
  return createPortal(
    <div className="notification-viewport" aria-live="polite" aria-atomic="false">
      {notifications.slice(0, 4).map((notification) => <div key={notification.id} className={`app-notification app-notification--${notification.tone}`} role={notification.tone === "error" ? "alert" : "status"}>
        <span>{notification.tone === "error" ? <AlertCircle size={19} /> : notification.tone === "info" ? <Bell size={19} /> : <Check size={19} />}</span>
        <div><strong>{notification.title}</strong><small>{notification.message}</small></div>
        <button type="button" aria-label={t("notifications.dismiss")} onClick={() => {
          const timeout = notificationTimeouts.current.get(notification.id);
          if (timeout !== undefined) window.clearTimeout(timeout);
          notificationTimeouts.current.delete(notification.id);
          setNotifications((current) => current.filter((item) => item.id !== notification.id));
        }}><X size={16} /></button>
      </div>)}
    </div>,
    document.body,
  );
}
