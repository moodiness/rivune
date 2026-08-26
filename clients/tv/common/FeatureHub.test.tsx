import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { RivuneTvClient } from "./api";
import { FeatureHub } from "./FeatureHub";
import { installSpatialNavigation } from "./focus";
import type { Profile, ReadingQueue } from "./types";

const profile = { id: "profile", name: "Viewer", accessible: true, hasPin: false, avatar: { kind: "preset", url: "" } } as Profile;
const initial: ReadingQueue = {
  revision: 3,
  items: [
    { id: "one", mediaType: "movie", resourceId: "one", title: "First", position: 0, createdAt: "x", updatedAt: "x" },
    { id: "two", mediaType: "movie", resourceId: "two", title: "Second", position: 1, createdAt: "x", updatedAt: "x" },
  ],
};

describe("TV feature remote focus", () => {
  let container: HTMLDivElement;
  let root: Root;
  let removeNavigation: () => void;

  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    window.localStorage?.clear();
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
      const key = this.dataset.featureKey ?? "";
      const left = key.includes("two") ? 300 : key.includes("one") ? 100 : 0;
      return { x: left, y: 0, left, top: 0, right: left + 20, bottom: 20, width: 20, height: 20, toJSON: () => ({}) };
    });
    Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: vi.fn() });
    vi.spyOn(window, "requestAnimationFrame").mockImplementation((callback) => { callback(0); return 1; });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    removeNavigation = installSpatialNavigation(vi.fn());
  });

  afterEach(async () => {
    removeNavigation?.();
    if (root) await act(async () => root.unmount());
    container?.remove();
  });

  it("moves between queue cards with the TV remote and keeps a stable keyed action after refresh", async () => {
    const readingQueue = vi.fn().mockResolvedValue(initial);
    const reorderReadingQueue = vi.fn().mockResolvedValue({ revision: 4 });
    const client = { readingQueue, reorderReadingQueue } as unknown as RivuneTvClient;
    await act(async () => root.render(<FeatureHub view="queue" client={client} profile={profile} timezone="UTC" admin={false} onOpen={vi.fn()} onAccessibilityChange={vi.fn()} />));
    await act(async () => undefined);

    const first = container.querySelector<HTMLButtonElement>('[data-feature-key="queue:one"]')!;
    const second = container.querySelector<HTMLButtonElement>('[data-feature-key="queue:two"]')!;
    first.focus();
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true }));
    expect(document.activeElement).toBe(second);

    const earlier = container.querySelector<HTMLButtonElement>('[data-feature-key="queue:two:earlier"]')!;
    earlier.focus();
    await act(async () => earlier.click());
    expect(reorderReadingQueue).toHaveBeenCalledWith("profile", expect.objectContaining({ expectedRevision: 3, itemIds: ["two", "one"] }));
    expect(document.activeElement?.getAttribute("data-feature-key")).toBe("queue:two:earlier");
  });

  it("announces and focuses the selected add-on history, then hides stale history on failure", async () => {
    let resolveHistory!: (value: { incident: unknown; events: Array<{ id: number; type: "opened"; code: "timeout"; occurredAt: string }> }) => void;
    const history = new Promise<{ incident: unknown; events: Array<{ id: number; type: "opened"; code: "timeout"; occurredAt: string }> }>((resolve) => { resolveHistory = resolve; });
    const incidents = ["Catalog One", "Catalog Two"].map((addonName, index) => ({
      id: `incident-${index}`, profileId: "profile", addonId: `addon-${index}`, addonName, code: "timeout" as const,
      state: "open" as const, impact: "availability" as const, occurrenceCount: 1, firstOccurredAt: "2026-01-01T00:00:00Z",
      lastOccurredAt: "2026-01-01T00:00:00Z", lastSuccessAt: null, recoveryStartedAt: null, resolvedAt: null,
      acknowledgedAt: null, acknowledgedByUserId: null, updatedAt: "2026-01-01T00:00:00Z",
    }));
    const client = {
      extensionIncidents: vi.fn().mockResolvedValue({ incidents }),
      extensionIncident: vi.fn().mockImplementationOnce(() => history).mockRejectedValueOnce(new Error("History unavailable")),
    } as unknown as RivuneTvClient;
    await act(async () => root.render(<FeatureHub view="incidents" client={client} profile={profile} timezone="UTC" admin onOpen={vi.fn()} onAccessibilityChange={vi.fn()} />));
    await act(async () => undefined);

    await act(async () => container.querySelector<HTMLButtonElement>('[data-feature-key="incident:incident-0"]')?.click());
    let region = container.querySelector<HTMLElement>(".tv-incident-events")!;
    expect(region.getAttribute("aria-busy")).toBe("true");
    expect(region.getAttribute("aria-live")).toBe("polite");
    expect(region.textContent).toContain("Event history for Catalog One");

    await act(async () => resolveHistory({ incident: incidents[0], events: [{ id: 1, type: "opened", code: "timeout", occurredAt: "2026-01-01T00:00:00Z" }] }));
    region = container.querySelector<HTMLElement>(".tv-incident-events")!;
    expect(region.getAttribute("aria-labelledby")).toBe("tv-incident-history-title");
    expect(document.activeElement).toBe(region);

    await act(async () => container.querySelector<HTMLButtonElement>('[data-feature-key="incident:incident-1"]')?.click());
    expect(container.querySelector(".tv-incident-events")).toBeNull();
    expect(container.querySelector('[role="alert"]')?.textContent).toContain("History unavailable");
  });

  it("disambiguates same-title notifications and moves focus to the adjacent item after dismiss", async () => {
    const notifications = [
      { id: "1", kind: "movie-release" as const, titleId: "title-1", title: "Shared title", availableAt: "2026-09-01T00:00:00Z", createdAt: "2026-08-01T00:00:00Z" },
      { id: "2", kind: "episode-available" as const, titleId: "title-2", title: "Shared title", availableAt: "2026-09-02T00:00:00Z", createdAt: "2026-08-02T00:00:00Z" },
    ];
    const mediaNotifications = vi.fn()
      .mockResolvedValueOnce({ notifications })
      .mockResolvedValueOnce({ notifications: [notifications[1]] });
    const acknowledgeMediaNotification = vi.fn().mockResolvedValue(undefined);
    const client = { mediaNotifications, acknowledgeMediaNotification } as unknown as RivuneTvClient;
    await act(async () => root.render(<FeatureHub view="inbox" client={client} profile={profile} timezone="UTC" admin={false} onOpen={vi.fn()} onAccessibilityChange={vi.fn()} />));
    await act(async () => undefined);

    const trackLabels = Array.from(container.querySelectorAll<HTMLButtonElement>('[data-feature-key$=":track"]')).map((button) => button.getAttribute("aria-label"));
    expect(new Set(trackLabels).size).toBe(2);
    expect(trackLabels[0]).toContain("Movie release");
    expect(trackLabels[0]).toContain("notification 1");
    expect(trackLabels[1]).toContain("Episode available");
    expect(container.querySelector('[data-feature-key="notification:1:read"]')).not.toBeNull();
    const dismiss = container.querySelector<HTMLButtonElement>('[data-feature-key="notification:1:dismiss"]')!;
    dismiss.focus();
    await act(async () => dismiss.click());
    expect(acknowledgeMediaNotification).toHaveBeenCalledWith("1", "dismissed");
    expect(document.activeElement?.getAttribute("data-feature-key")).toBe("notification:2");
  });
});
