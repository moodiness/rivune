import { AlertCircle, Check, X } from "lucide-react";
import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { APIError } from "./api";

type NotificationTone = "error" | "success";

type AppNotification = {
  id: number;
  tone: NotificationTone;
  title: string;
  message: string;
};

type NotificationListener = (notification: AppNotification) => void;

const listeners = new Set<NotificationListener>();
let nextNotificationID = 1;

function publish(tone: NotificationTone, title: string, message: string): void {
  const notification = { id: nextNotificationID++, tone, title, message };
  for (const listener of listeners) listener(notification);
}

export function notifySuccess(message: string, title = "Saved"): void {
  publish("success", title, message);
}

export function notifyError(cause: unknown, fallback: string, title = "Something went wrong"): string {
  const message = cause instanceof APIError ? cause.message : cause instanceof Error && cause.message ? cause.message : fallback;
  publish("error", title, message);
  return message;
}

export function notifyErrorMessage(message: string, title = "Something went wrong"): string {
  publish("error", title, message);
  return message;
}

export function NotificationViewport() {
  const [notifications, setNotifications] = useState<AppNotification[]>([]);

  useEffect(() => {
    const listener: NotificationListener = (notification) => {
      setNotifications((current) => [...current, notification].slice(-4));
      window.setTimeout(() => {
        setNotifications((current) => current.filter((item) => item.id !== notification.id));
      }, 5000);
    };
    listeners.add(listener);
    return () => { listeners.delete(listener); };
  }, []);

  if (notifications.length === 0) return null;
  return createPortal(
    <div className="notification-viewport" aria-live="polite" aria-atomic="false">
      {notifications.map((notification) => <div key={notification.id} className={`app-notification app-notification--${notification.tone}`} role={notification.tone === "error" ? "alert" : "status"}>
        <span>{notification.tone === "error" ? <AlertCircle size={19} /> : <Check size={19} />}</span>
        <div><strong>{notification.title}</strong><small>{notification.message}</small></div>
        <button type="button" aria-label="Dismiss notification" onClick={() => setNotifications((current) => current.filter((item) => item.id !== notification.id))}><X size={16} /></button>
      </div>)}
    </div>,
    document.body,
  );
}
