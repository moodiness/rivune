import type { MediaItem } from "./types";
import { mediaSubtitle } from "./media";

export function MediaCard({ item, artworkUrl, landscape = false, progressPercent = 0, onOpen }: { item: MediaItem; artworkUrl?: string; landscape?: boolean; progressPercent?: number; onOpen: (item: MediaItem) => void }) {
  return <button type="button" className={`tv-card${landscape ? " tv-card--landscape" : ""}`} onClick={() => onOpen(item)} data-tv-focusable="true">
    <span className="tv-card__art">
      {artworkUrl ? <img src={artworkUrl} alt="" loading="lazy" /> : <span className="tv-card__fallback">{item.title.slice(0, 1).toUpperCase()}</span>}
      {progressPercent > 0 && <span className="tv-card__progress"><span style={{ width: `${Math.min(100, Math.max(0, progressPercent))}%` }} /></span>}
    </span>
    <span className="tv-card__copy"><strong>{item.title}</strong><span>{mediaSubtitle(item)}</span></span>
  </button>;
}
