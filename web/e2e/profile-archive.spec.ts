import { expect, test } from "@playwright/test";
import { maximumProfileArchiveBytes, readProfileArchive, validProfileArchive } from "../src/profileArchive";

const archive = {
  version: 2,
  exportedAt: "2026-08-26T00:00:00Z",
  identity: { name: "Alice", description: "Portable profile", isChild: false, avatar: { kind: "preset", presetId: "aurora" } },
  settings: {},
  addons: [],
  collections: [],
  titles: [],
  library: [],
  progress: [],
  favorites: [],
  userData: [],
  continueDismissals: [],
  trackingPreferences: [],
} as const;

test("profile archive v2 validation requires the strict document and avatar", () => {
  expect(validProfileArchive(archive)).toBe(true);
  expect(validProfileArchive({ ...archive, version: 1 })).toBe(false);
  expect(validProfileArchive({ ...archive, identity: { name: "Alice", isChild: false } })).toBe(false);
  expect(validProfileArchive({ ...archive, providerToken: "secret" })).toBe(false);
});

test("profile archive parsing rejects oversized files before retaining contents", async () => {
  const oversized = new File([new Uint8Array(maximumProfileArchiveBytes + 1)], "profile.json", { type: "application/json" });
  await expect(readProfileArchive(oversized)).rejects.toThrow("invalid_profile_archive_size");
});
