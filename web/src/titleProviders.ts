import type { EpisodeMetadata } from "./types";

export const TITLE_ID_PROVIDERS = [
  { key: "imdb", label: "IMDb" },
  { key: "tmdb", label: "TMDB" },
  { key: "tvdb", label: "TVDB" },
] as const;

export type TitleIDProvider = typeof TITLE_ID_PROVIDERS[number]["key"];

export function titleProviderURL(
  provider: TitleIDProvider,
  externalID: string,
  mediaType: string,
  episode?: Pick<EpisodeMetadata, "seasonNumber" | "episodeNumber">,
): string | undefined {
  const id = encodeURIComponent(externalID);
  if (provider === "imdb") return `https://www.imdb.com/title/${id}/`;
  if (provider === "tmdb") {
    if (mediaType === "episode") {
      return episode ? `https://www.themoviedb.org/tv/${id}/season/${episode.seasonNumber}/episode/${episode.episodeNumber}` : undefined;
    }
    if (mediaType !== "movie" && mediaType !== "series") return undefined;
    return `https://www.themoviedb.org/${mediaType === "movie" ? "movie" : "tv"}/${id}`;
  }
  if (mediaType !== "movie" && mediaType !== "series" && mediaType !== "season" && mediaType !== "episode") return undefined;
  return `https://thetvdb.com/dereferrer/${mediaType}/${id}`;
}
