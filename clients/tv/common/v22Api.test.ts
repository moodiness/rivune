import { describe, expect, it, vi } from "vitest";
import { APIError, RivuneTvClient } from "./api";
import { MemoryCredentialStore } from "./storage";
import type { TokenPair } from "./types";

const discovery = { name: "Rivune", serverVersion: "1", protocolVersion: 22, apiBaseUrl: "/api/v1", setupRequired: false, timezone: "UTC", interfaceLanguage: "en" };
const token: TokenPair = {
  tokenType: "Bearer", accessToken: "access", accessTokenExpiresAt: "2099-01-01T00:00:00Z", refreshToken: "refresh",
  refreshTokenExpiresAt: "2099-01-02T00:00:00Z", sessionId: "s", deviceId: "d", authorizationScope: "global_admin", category: null,
};

async function clientWith(responses: unknown[]) {
  const store = new MemoryCredentialStore();
  await store.save({ issuer: "https://media.example", tokens: token, profileContext: "profile-context" });
  const requests: Array<{ url: string; init: RequestInit }> = [];
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    requests.push({ url: String(input), init: init ?? {} });
    const body = responses.shift();
    if (body instanceof Response) return body;
    return new Response(body === undefined ? null : JSON.stringify(body), { status: body === undefined ? 204 : 200, headers: { "Content-Type": "application/json" } });
  }) as typeof globalThis.fetch;
  return { client: new RivuneTvClient("https://media.example", { platform: "tizen", credentialStore: store, fetch }), requests };
}

function body(request: { init: RequestInit }): unknown {
  return JSON.parse(String(request.init.body));
}

describe("TV protocol v22 APIs", () => {
  it("uses queue CAS bodies and preserves a caller operation id", async () => {
    const { client, requests } = await clientWith([discovery, { revision: 4, affectedItemId: "i" }, { revision: 5 }]);
    await client.addReadingQueueItem("p/1", { operationId: "fixed-op", expectedRevision: 3, mediaType: "movie", resourceId: "r", title: "Title" });
    await client.removeReadingQueueItem("p/1", "i/1", { operationId: "fixed-delete", expectedRevision: 4 });
    expect(requests[1].url).toBe("https://media.example/api/v1/profiles/p%2F1/queue/items");
    expect(requests[1].init.method).toBe("POST");
    expect(body(requests[1])).toMatchObject({ operationId: "fixed-op", expectedRevision: 3 });
    expect(requests[2].url.endsWith("/profiles/p%2F1/queue/items/i%2F1")).toBe(true);
    expect(body(requests[2])).toEqual({ operationId: "fixed-delete", expectedRevision: 4 });
  });

  it("uses saved-search revisions and accepts only the closed smart-rule AST", async () => {
    const { client, requests } = await clientWith([discovery, undefined, { id: "s", name: "Drama", rules: { type: "genre", operator: "equals", value: "Drama" }, sort: "title", revision: 1, createdAt: "x", updatedAt: "x" }]);
    await client.deleteSavedSearch("saved/1", 7);
    await client.createSmartCollection({ name: "Drama", rules: { type: "genre", operator: "equals", value: "Drama" }, sort: "title" });
    expect(requests[1].url.endsWith("/saved-searches/saved%2F1?expectedRevision=7")).toBe(true);
    expect(body(requests[2])).toEqual({ name: "Drama", rules: { type: "genre", operator: "equals", value: "Drama" }, sort: "title" });
    await expect(client.createSmartCollection({ name: "Unsafe", rules: { type: "genre", operator: "equals", value: "" }, sort: "title" })).rejects.toMatchObject({ code: "invalid_smart_rule" } satisfies Partial<APIError>);
  });

  it("calls incident, notification, failover, and profile accessibility routes exactly", async () => {
    const failover = { id: "f", currentPosition: 1, currentSourceRef: "source-ref-000002", positionSeconds: 42, attemptCount: 1, maximumAttempts: 2, revision: 2, status: "active", candidateHealth: [], expiresAt: "x" };
    const preferences = { revision: 2, reducedMotion: "reduce", highContrast: "more", textScale: 130, captions: "on", audioDescription: true, focusIndicators: "enhanced" } as const;
    const { client, requests } = await clientWith([discovery, { id: "incident" }, undefined, failover, undefined, preferences]);
    await client.acknowledgeExtensionIncident("incident/1");
    await client.acknowledgeMediaNotification("12", "dismissed");
    await client.advancePlaybackFailover("fail/1", { error: "source_timeout", positionSeconds: 42, expectedRevision: 1 });
    await client.cancelPlaybackFailover("fail/1");
    await client.updateAccessibilityPreferences("profile/1", preferences);
    expect(requests.slice(1).map((request) => [request.init.method, request.url])).toEqual([
      ["POST", "https://media.example/api/v1/operations/extension-incidents/incident%2F1/acknowledgement"],
      ["POST", "https://media.example/api/v1/media-notifications/12/acknowledgement"],
      ["POST", "https://media.example/api/v1/playback/failovers/fail%2F1/advance"],
      ["DELETE", "https://media.example/api/v1/playback/failovers/fail%2F1"],
      ["PUT", "https://media.example/api/v1/profiles/profile%2F1/accessibility-preferences"],
    ]);
    expect(body(requests[2])).toEqual({ state: "dismissed" });
    expect(body(requests[3])).toEqual({ error: "source_timeout", positionSeconds: 42, expectedRevision: 1 });
    expect(body(requests[5])).toEqual(preferences);
  });
});
