import { AlertCircle, AlertTriangle, Bell, Check, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { APIError } from "./api";
import { translate as t } from "./i18n";

type NotificationTone = "error" | "info" | "success" | "warning";

type AppNotification = {
  id: number;
  tone: NotificationTone;
  title: string;
  message: string;
  markPresented: () => void;
  durationMilliseconds: number;
  restoreFocus: HTMLElement | null;
};

type NotificationListener = (notification: AppNotification) => void;

const listeners = new Set<NotificationListener>();
const resetListeners = new Set<() => void>();
let nextNotificationID = 1;
const defaultNotificationDurationMilliseconds = 5_000;
let notificationDurationMilliseconds = defaultNotificationDurationMilliseconds;

export function configureNotificationDuration(durationSeconds: number): void {
  notificationDurationMilliseconds = Number.isFinite(durationSeconds)
    ? Math.min(30, Math.max(2, durationSeconds)) * 1_000
    : defaultNotificationDurationMilliseconds;
}

export function clearNotifications(): void {
  for (const listener of resetListeners) listener();
}

function publish(tone: NotificationTone, title: string, message: string): Promise<void> {
  let markPresented = () => {};
  const presented = new Promise<void>((resolve) => { markPresented = resolve; });
  const notification = { id: nextNotificationID++, tone, title, message, durationMilliseconds: notificationDurationMilliseconds, markPresented, restoreFocus: document.activeElement instanceof HTMLElement ? document.activeElement : null };
  for (const listener of listeners) listener(notification);
  return presented;
}

export function notifySuccess(message: string, title = t("notifications.savedTitle")): void {
  void publish("success", title, message);
}

export function notifyInfo(message: string, title = t("notifications.messageTitle")): Promise<void> {
  return publish("info", title, message);
}

export function notifyWarning(message: string, title = t("notifications.messageTitle")): void {
  void publish("warning", title, message);
}

export function notifyError(cause: unknown, fallback: string, title = t("notifications.errorTitle")): string {
  if (cause instanceof APIError && cause.code === "demo_unavailable") return t("demo.unavailable");
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
  const notificationTimeouts = useRef(new Map<number, { handle: number; remaining: number; startedAt: number }>());

  function restoreNotificationFocus(notification: AppNotification): void {
    if (notification.restoreFocus?.isConnected) notification.restoreFocus.focus();
  }

  function startTimer(notification: AppNotification, remaining = notification.durationMilliseconds): void {
    const startedAt = window.performance.now();
    const handle = window.setTimeout(() => {
      const active = document.activeElement;
      const toastOwnedFocus = active instanceof HTMLElement && active.closest(`[data-notification-id="${notification.id}"]`) !== null;
      notificationTimeouts.current.delete(notification.id);
      setNotifications((current) => current.filter((item) => item.id !== notification.id));
      if (toastOwnedFocus) restoreNotificationFocus(notification);
    }, remaining);
    notificationTimeouts.current.set(notification.id, { handle, remaining, startedAt });
  }

  function pauseTimer(notification: AppNotification): void {
    const timer = notificationTimeouts.current.get(notification.id);
    if (!timer) return;
    window.clearTimeout(timer.handle);
    notificationTimeouts.current.set(notification.id, {
      handle: 0,
      remaining: Math.max(0, timer.remaining - (window.performance.now() - timer.startedAt)),
      startedAt: window.performance.now(),
    });
  }

  function resumeTimer(notification: AppNotification): void {
    const timer = notificationTimeouts.current.get(notification.id);
    if (!timer || timer.handle !== 0) return;
    startTimer(notification, timer.remaining);
  }

  function dismiss(notification: AppNotification): void {
    const timer = notificationTimeouts.current.get(notification.id);
    if (timer?.handle) window.clearTimeout(timer.handle);
    notificationTimeouts.current.delete(notification.id);
    setNotifications((current) => current.filter((item) => item.id !== notification.id));
    restoreNotificationFocus(notification);
  }

  useEffect(() => {
    const listener: NotificationListener = (notification) => {
      setNotifications((current) => [...current, notification]);
    };
    const reset = () => {
      for (const timer of notificationTimeouts.current.values()) if (timer.handle) window.clearTimeout(timer.handle);
      notificationTimeouts.current.clear();
      setNotifications((current) => {
        for (const notification of current) notification.markPresented();
        return [];
      });
    };
    listeners.add(listener);
    resetListeners.add(reset);
    return () => {
      listeners.delete(listener);
      resetListeners.delete(reset);
      for (const timer of notificationTimeouts.current.values()) if (timer.handle) window.clearTimeout(timer.handle);
      notificationTimeouts.current.clear();
    };
  }, []);

  useEffect(() => {
    for (const notification of notifications.slice(0, 4)) {
      if (notificationTimeouts.current.has(notification.id)) continue;
      notification.markPresented();
      startTimer(notification);
    }
  }, [notifications]);

  if (notifications.length === 0) return null;
  return createPortal(
    <div className="notification-viewport" aria-live="polite" aria-atomic="false">
      {notifications.slice(0, 4).map((notification) => <div key={notification.id} data-notification-id={notification.id} className={`app-notification app-notification--${notification.tone}`} role={notification.tone === "error" ? "alert" : "status"}
        onMouseEnter={() => pauseTimer(notification)}
        onMouseLeave={(event) => { if (!event.currentTarget.contains(document.activeElement)) resumeTimer(notification); }}
        onFocus={(event) => {
          if (event.relatedTarget instanceof HTMLElement && !event.currentTarget.contains(event.relatedTarget)) notification.restoreFocus = event.relatedTarget;
          pauseTimer(notification);
        }}
        onBlur={(event) => { if (!event.currentTarget.contains(event.relatedTarget as Node | null) && !event.currentTarget.matches(":hover")) resumeTimer(notification); }}>
        <span>{notification.tone === "error" ? <AlertCircle size={19} /> : notification.tone === "warning" ? <AlertTriangle size={19} /> : notification.tone === "info" ? <Bell size={19} /> : <Check size={19} />}</span>
        <div><strong>{notification.title}</strong><small>{notification.message}</small></div>
        <button type="button" aria-label={`${t("notifications.dismiss")}: ${notification.title} — ${notification.message}`} onClick={() => dismiss(notification)}><X size={16} /></button>
      </div>)}
    </div>,
    document.body,
  );
}
