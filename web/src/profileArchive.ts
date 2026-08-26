import type { ProfileArchiveDocument } from "./types";

export const maximumProfileArchiveBytes = 16 * 1024 * 1024;

const requiredKeys = ["version", "exportedAt", "identity", "settings", "addons", "collections", "titles", "library", "progress", "favorites", "userData", "continueDismissals", "trackingPreferences"] as const;
const arrayKeys = ["addons", "collections", "titles", "library", "progress", "favorites", "userData", "trackingPreferences", "continueDismissals"] as const;

function record(value: unknown): Record<string, unknown> | null {
  return typeof value === "object" && value !== null && !Array.isArray(value) ? value as Record<string, unknown> : null;
}
function validAvatar(value: unknown): boolean {
  const avatar = record(value);
  if (!avatar || typeof avatar.kind !== "string") return false;
  if (avatar.kind === "preset") return typeof avatar.presetId === "string" && avatar.presetId.length > 0 && avatar.presetId.length <= 64;
  return avatar.kind === "image" && avatar.contentType === "image/png" && typeof avatar.sha256 === "string" && /^[0-9a-f]{64}$/.test(avatar.sha256) && typeof avatar.data === "string" && avatar.data.length <= 2_796_204;
}


export function validProfileArchive(value: unknown): value is ProfileArchiveDocument {
  const root = record(value);
  if (!root || Object.keys(root).length !== requiredKeys.length || Object.keys(root).some((key) => !requiredKeys.includes(key as typeof requiredKeys[number]))) return false;
  if (root.version !== 2 || typeof root.exportedAt !== "string" || !Number.isFinite(Date.parse(root.exportedAt))) return false;
  const identity = record(root.identity);
  if (!identity || typeof identity.name !== "string" || !identity.name.trim() || identity.name.length > 80 || typeof identity.isChild !== "boolean" || !validAvatar(identity.avatar)) return false;
  if (identity.description !== undefined && identity.description !== null && typeof identity.description !== "string") return false;
  if (!record(root.settings)) return false;
  return arrayKeys.every((key) => Array.isArray(root[key]));
}

export async function readProfileArchive(file: File): Promise<ProfileArchiveDocument> {
  if (file.size <= 0 || file.size > maximumProfileArchiveBytes) throw new Error("invalid_profile_archive_size");
  const contents = await file.text();
  if (new TextEncoder().encode(contents).byteLength > maximumProfileArchiveBytes) throw new Error("invalid_profile_archive_size");
  let parsed: unknown;
  try {
    parsed = JSON.parse(contents);
  } catch {
    throw new Error("invalid_profile_archive_json");
  }
  if (!validProfileArchive(parsed)) throw new Error("invalid_profile_archive_document");
  return parsed;
}

export function downloadProfileArchive(archive: ProfileArchiveDocument, profileName: string): void {
  const blob = new Blob([JSON.stringify(archive, null, 2)], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = `rivune-profile-${profileName.trim().toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "") || "archive"}-v2.json`;
  anchor.rel = "noopener";
  anchor.click();
  URL.revokeObjectURL(url);
}
