import { ArrowDown, ArrowUp, ListVideo, Play, RefreshCw, Trash2 } from "lucide-react";
import { useEffect, useRef } from "react";
import { Button, EmptyState, Notice } from "./components";
import { translate as t } from "./i18n";
import {
  consumeReadingQueue,
  moveReadingQueueItem,
  readingQueueMedia,
  refreshReadingQueue,
  removeReadingQueue,
  useReadingQueue,
} from "./readingQueue";
import type { MediaItem } from "./types";

export function ReadingQueuePanel({ profileId, onOpenMedia }: { profileId: string; onOpenMedia: (item: MediaItem) => void }) {
  const queueState = useReadingQueue(profileId);
  const queue = queueState.queue;
  const panelRef = useRef<HTMLElement>(null);

  function restoreQueueFocus(removedIndex: number): void {
    window.requestAnimationFrame(() => {
      const rows = panelRef.current?.querySelectorAll<HTMLElement>(".reading-queue__list > li");
      const row = rows?.[Math.min(removedIndex, Math.max(0, rows.length - 1))];
      const target = row?.querySelector<HTMLElement>("button:not(:disabled)") ?? panelRef.current?.querySelector<HTMLElement>("#reading-queue-title");
      if (target?.id === "reading-queue-title") target.setAttribute("tabindex", "-1");
      target?.focus();
    });
  }

  async function removeAndRestore(itemId: string, index: number): Promise<void> {
    try {
      await removeReadingQueue(profileId, itemId);
      restoreQueueFocus(index);
    } catch {
      // The store exposes the mutation error next to the queue and retains the trigger.
    }
  }

  useEffect(() => {
    if (!profileId) return;
    void refreshReadingQueue(profileId).catch(() => undefined);
  }, [profileId]);

  async function consumeAndOpen(itemId: string): Promise<void> {
    const item = queue?.items.find((candidate) => candidate.id === itemId);
    if (!item) return;
    try {
      await consumeReadingQueue(profileId, itemId);
      onOpenMedia(readingQueueMedia(item));
    } catch {
      // The store exposes an actionable error or conflict state next to the queue.
    }
  }

  return <section ref={panelRef} className="reading-queue" aria-labelledby="reading-queue-title" aria-busy={queueState.loading || undefined}>
    <header>
      <div>
        <span>{t("queue.eyebrow")}</span>
        <h2 id="reading-queue-title">{t("queue.title")}</h2>
        <p>{t("queue.description")}</p>
      </div>
      <Button variant="ghost" loading={queueState.loading} onClick={() => void refreshReadingQueue(profileId).catch(() => undefined)}>
        <RefreshCw size={16} /> {t("common.refresh")}
      </Button>
    </header>
    {queueState.conflict && <Notice tone="warning"><span>{t("queue.conflict")}</span><Button variant="ghost" onClick={() => void refreshReadingQueue(profileId).catch(() => undefined)}>{t("common.refresh")}</Button></Notice>}
    {queueState.error && <Notice tone="warning"><span>{t(queueState.error === "load" ? "queue.error.load" : "queue.error.mutation")}</span><Button variant="ghost" onClick={() => void refreshReadingQueue(profileId).catch(() => undefined)}>{t("common.actions.retry")}</Button></Notice>}
    {!queueState.loading && queue?.items.length === 0 && <EmptyState icon={<ListVideo size={38} />} title={t("queue.empty.title")} description={t("queue.empty.description")} />}
    {queue && queue.items.length > 0 && <ol className="reading-queue__list" aria-label={t("queue.title")}>
      {queue.items.map((item, index) => {
        const busy = queueState.busyItemId === item.id;
        return <li key={item.id}>
          <span className="reading-queue__position" aria-hidden="true">{index + 1}</span>
          {item.posterUrl ? <img src={item.posterUrl} alt="" /> : <span className="reading-queue__poster" aria-hidden="true"><ListVideo size={20} /></span>}
          <div className="reading-queue__copy"><strong>{item.title}</strong><small>{item.mediaType}</small></div>
          <div className="reading-queue__actions" role="group" aria-label={`${t("queue.actions.forTitle", { title: item.title })}, ${index + 1} of ${queue.items.length}`}>
            <button type="button" disabled={busy || index === 0} aria-label={`${t("queue.actions.moveUp", { title: item.title })}, position ${index + 1}`} title={t("queue.actions.moveUp", { title: item.title })} onClick={() => void moveReadingQueueItem(profileId, item.id, -1).catch(() => undefined)}><ArrowUp size={16} /></button>
            <button type="button" disabled={busy || index === queue.items.length - 1} aria-label={`${t("queue.actions.moveDown", { title: item.title })}, position ${index + 1}`} title={t("queue.actions.moveDown", { title: item.title })} onClick={() => void moveReadingQueueItem(profileId, item.id, 1).catch(() => undefined)}><ArrowDown size={16} /></button>
            <button type="button" disabled={busy} aria-label={`${t("queue.actions.remove", { title: item.title })}, position ${index + 1}`} title={t("queue.actions.remove", { title: item.title })} onClick={() => void removeAndRestore(item.id, index)}><Trash2 size={16} /></button>
            <Button loading={busy} aria-label={`${t("queue.actions.play", { title: item.title })}, position ${index + 1}`} onClick={() => void consumeAndOpen(item.id)}><Play size={16} fill="currentColor" /> {t("common.play")}</Button>
          </div>
        </li>;
      })}
    </ol>}
  </section>;
}
