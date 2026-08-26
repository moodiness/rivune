import { beforeEach, describe, expect, it, vi } from "vitest";
import { PendingMutationJournal } from "./featureState";

beforeEach(() => {
  localStorage.clear();
  vi.spyOn(globalThis.crypto, "randomUUID").mockReturnValue("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa");
});

describe("TV pending mutation journal", () => {
  it("reuses the operation id and CAS revision for the same retry", () => {
    const journal = new PendingMutationJournal();
    expect(journal.begin("profile", "queue:add:title", 4)).toEqual(journal.begin("profile", "queue:add:title", 99));
    expect(journal.begin("profile", "queue:add:title", 4)).toMatchObject({ operationId: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", expectedRevision: 4 });
  });

  it("persists only bounded non-secret mutation metadata", () => {
    const journal = new PendingMutationJournal();
    for (let index = 0; index < 40; index += 1) journal.begin("profile", `remove:${index}`, 1);
    const raw = localStorage.getItem("rivune.tv.pending-mutations.v1") ?? "";
    expect(JSON.parse(raw)).toHaveLength(32);
    expect(raw).not.toContain("token");
    expect(raw.length).toBeLessThan(16_384);
    journal.clearProfile("profile");
    expect(localStorage.getItem("rivune.tv.pending-mutations.v1")).toBe("[]");
  });
});
