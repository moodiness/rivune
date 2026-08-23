import { describe, expect, it, vi } from "vitest";
import { APIError, RivuneTvClient, normalizeServerUrl } from "./api";
import { LocalStorageCredentialStore, MemoryCredentialStore, type CredentialStore, type StoredCredentials } from "./storage";
import type { TokenPair } from "./types";

type CapturedRequest = { url: string; init: RequestInit; headers: Headers };

const DISCOVERY = {
  name: "Rivune",
  serverVersion: "1.12.0",
  protocolVersion: 20,
  apiBaseUrl: "/api/v1",
  setupRequired: false,
  timezone: "UTC",
  interfaceLanguage: "en",
};

function tokens(prefix: string): TokenPair {
  return {
    tokenType: "Bearer",
    accessToken: `${prefix}-access`,
    accessTokenExpiresAt: "2099-01-01T00:00:00Z",
    refreshToken: `${prefix}-refresh`,
    refreshTokenExpiresAt: "2099-02-01T00:00:00Z",
    sessionId: "11111111-1111-4111-8111-111111111111",
    deviceId: "22222222-2222-4222-8222-222222222222",
    authorizationScope: "global_admin",
    category: null,
  };
}

function json(body: unknown, status = 200, headers: HeadersInit = {}): Response {
  const responseHeaders = new Headers(headers);
  responseHeaders.set("Content-Type", "application/json");
  return new Response(JSON.stringify(body), { status, headers: responseHeaders });
}

function fetchQueue(responses: Response[], requests: CapturedRequest[]): typeof fetch {
  return vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    requests.push({ url: String(input), init, headers: new Headers(init.headers) });
    const response = responses.shift();
    if (!response) throw new Error("Unexpected fetch");
    return response;
  }) as typeof fetch;
}

describe("Rivune TV URL policy", () => {
  it("accepts only HTTPS or HTTP on localhost/private literal addresses", () => {
    expect(normalizeServerUrl("media.example.com")).toBe("https://media.example.com");
    expect(normalizeServerUrl("192.168.1.20:8080")).toBe("http://192.168.1.20:8080");
    expect(normalizeServerUrl("http://localhost:8080/")).toBe("http://localhost:8080");
    expect(normalizeServerUrl("http://10.0.0.4")).toBe("http://10.0.0.4");
    expect(normalizeServerUrl("http://172.31.255.254")).toBe("http://172.31.255.254");
    expect(normalizeServerUrl("http://[fd00::20]:8090")).toBe("http://[fd00::20]:8090");

    for (const value of [
      "http://media.example.com",
      "http://192.0.2.20",
      "http://rivune.local",
      "http://169.254.1.2",
      "ftp://192.168.1.20",
      "https://user:password@media.example.com",
      "https://@media.example.com",
      "https://media.example.com?token=secret",
    ]) {
      expect(() => normalizeServerUrl(value), value).toThrow(APIError);
    }
  });

  it("rejects IPv4-mapped HTTP addresses", () => {
    expect(() => normalizeServerUrl("http://[::ffff:192.0.2.20]:8080")).toThrow(APIError);
    expect(() => normalizeServerUrl("http://[::ffff:192.168.1.20]:8080")).toThrow(APIError);
  });

  it("rejects a discovery API base on another origin or protocol version", async () => {
    const requests: CapturedRequest[] = [];
    const crossOrigin = new RivuneTvClient("https://media.example.com", {
      platform: "browser",
      credentialStore: new MemoryCredentialStore(),
      fetch: fetchQueue([json({ ...DISCOVERY, apiBaseUrl: "https://evil.example/api/v1" })], requests),
    });
    await expect(crossOrigin.discover()).rejects.toMatchObject({ code: "invalid_server_url" });

    const incompatible = new RivuneTvClient("https://media.example.com", {
      platform: "browser",
      credentialStore: new MemoryCredentialStore(),
      fetch: fetchQueue([json({ ...DISCOVERY, protocolVersion: 19 })], []),
    });
    await expect(incompatible.discover()).rejects.toMatchObject({ code: "incompatible_protocol" });
  });
});

describe("Rivune TV credentials", () => {
  it("does not restore or send credentials returned for another issuer", async () => {
    const wrongIssuer: StoredCredentials = {
      issuer: "https://other.example",
      tokens: tokens("other"),
      profileContext: "other-profile",
    };
    const cleared: string[] = [];
    const leakyStore: CredentialStore = {
      async load() { return wrongIssuer; },
      async save() { throw new Error("must not save"); },
      async clear(issuer) { cleared.push(issuer); },
    };
    const requests: CapturedRequest[] = [];
    const client = new RivuneTvClient("https://media.example.com", {
      platform: "tizen",
      credentialStore: leakyStore,
      fetch: fetchQueue([json(DISCOVERY)], requests),
    });

    await expect(client.restoreSession()).resolves.toBe(false);
    await expect(client.currentAccount()).rejects.toMatchObject({ code: "not_authenticated" });
    expect(cleared).toEqual(["https://media.example.com"]);
    expect(requests).toHaveLength(1);
    expect(requests[0].headers.get("Authorization")).toBeNull();
  });

  it("persists and applies profileContext only to profile-scoped APIs", async () => {
    const store = new MemoryCredentialStore();
    await store.save({ issuer: "https://media.example.com", tokens: tokens("initial"), profileContext: null });
    const requests: CapturedRequest[] = [];
    const client = new RivuneTvClient("https://media.example.com", {
      platform: "webos",
      credentialStore: store,
      fetch: fetchQueue([
        json(DISCOVERY),
        json({
          profile: { id: "profile-1", name: "Viewer" },
          expiresAt: "2099-01-01T00:00:00Z",
          profileContext: "profile-context-1",
        }),
        json({ collections: [] }),
        json({ user: {}, session: {}, profiles: [], maintenance: { enabled: false, message: null } }),
      ], requests),
    });

    await client.selectProfile("profile-1");
    await expect(client.collections()).resolves.toEqual([]);
    await client.currentAccount();

    expect(requests[1].headers.get("X-Rivune-Profile-Context")).toBeNull();
    expect(requests[2].headers.get("X-Rivune-Profile-Context")).toBe("profile-context-1");
    expect(requests[3].headers.get("X-Rivune-Profile-Context")).toBeNull();
    expect((await store.load("https://media.example.com"))?.profileContext).toBe("profile-context-1");
  });

  it("accepts protocol category-scoped credential rotation", async () => {
    const store = new MemoryCredentialStore();
    await store.save({ issuer: "https://media.example.com", tokens: tokens("old"), profileContext: null });
    const categoryTokens: TokenPair = {
      ...tokens("category"),
      authorizationScope: "category",
      category: { id: "33333333-3333-4333-8333-333333333333", name: "Family", color: null, icon: null },
    };
    const client = new RivuneTvClient("https://media.example.com", {
      platform: "browser",
      credentialStore: store,
      fetch: fetchQueue([json(DISCOVERY), json(categoryTokens)], []),
    });

    await expect(client.refreshSession()).resolves.toEqual(categoryTokens);
    expect((await store.load("https://media.example.com"))?.tokens.authorizationScope).toBe("category");
  });

  it("rotates refresh credentials atomically and retries once with the profile context", async () => {
    const store = new MemoryCredentialStore();
    await store.save({
      issuer: "https://media.example.com",
      tokens: tokens("old"),
      profileContext: "profile-context",
    });
    const requests: CapturedRequest[] = [];
    const client = new RivuneTvClient("https://media.example.com", {
      platform: "browser",
      credentialStore: store,
      fetch: fetchQueue([
        json(DISCOVERY),
        json({ error: { code: "invalid_access_token", message: "Expired" } }, 401),
        json(tokens("new")),
        json({ collections: [] }),
      ], requests),
    });

    await expect(client.collections()).resolves.toEqual([]);

    expect(requests[1].headers.get("Authorization")).toBe("Bearer old-access");
    expect(requests[1].headers.get("X-Rivune-Profile-Context")).toBe("profile-context");
    expect(requests[2].url).toBe("https://media.example.com/api/v1/auth/refresh");
    expect(requests[2].headers.get("Authorization")).toBeNull();
    expect(requests[2].headers.get("X-Rivune-Profile-Context")).toBeNull();
    expect(JSON.parse(String(requests[2].init.body))).toEqual({ refreshToken: "old-refresh" });
    expect(requests[3].headers.get("Authorization")).toBe("Bearer new-access");
    expect(requests[3].headers.get("X-Rivune-Profile-Context")).toBe("profile-context");
    expect(await store.load("https://media.example.com")).toEqual({
      issuer: "https://media.example.com",
      tokens: tokens("new"),
      profileContext: "profile-context",
    });
  });

  it("drops persisted credentials with inconsistent authorization scope", async () => {
    const storage = new Map<string, string>();
    const store = new LocalStorageCredentialStore({
      getItem(key) { return storage.get(key) ?? null; },
      setItem(key, value) { storage.set(key, value); },
      removeItem(key) { storage.delete(key); },
    });
    await store.save({
      issuer: "https://media.example.com",
      tokens: { ...tokens("bad"), authorizationScope: "category", category: null },
      profileContext: null,
    });

    await expect(store.load("https://media.example.com")).resolves.toBeNull();
  });
});

describe("Rivune TV request security", () => {
  it("uses no cookies, rejects redirects, and identifies the packaged platform", async () => {
    const requests: CapturedRequest[] = [];
    const client = new RivuneTvClient("https://media.example.com", {
      platform: "tizen",
      credentialStore: new MemoryCredentialStore(),
      fetch: fetchQueue([json(DISCOVERY)], requests),
    });

    await client.discover();

    expect(requests[0].url).toBe("https://media.example.com/.well-known/rivune");
    expect(requests[0].init.credentials).toBe("omit");
    expect(requests[0].init.redirect).toBe("manual");
    expect(requests[0].headers.get("X-Rivune-TV-Platform")).toBe("tizen");
    expect(requests[0].headers.get("Cookie")).toBeNull();
    expect(requests[0].headers.get("Authorization")).toBeNull();
  });
  it("rejects redirect responses without following them", async () => {
    const client = new RivuneTvClient("https://media.example.com", {
      platform: "browser",
      credentialStore: new MemoryCredentialStore(),
      fetch: fetchQueue([
        new Response(null, { status: 302, headers: { Location: "https://evil.example/api" } }),
      ], []),
    });

    await expect(client.discover()).rejects.toMatchObject({
      status: 302,
      code: "redirect_not_allowed",
    });
  });

  it("rejects cross-origin media URLs that carry URL credentials", () => {
    const client = new RivuneTvClient("https://media.example.com", {
      platform: "browser",
      credentialStore: new MemoryCredentialStore(),
      fetch: fetchQueue([], []),
    });

    expect(client.resolveMediaUrl("https://cdn.example/movie.mp4")).toBe("https://cdn.example/movie.mp4");
    expect(client.resolveMediaUrl("https://cdn.example/movie.mp4?token=secret")).toBeNull();
    expect(client.resolveMediaUrl("https://cdn.example/movie.mp4#token")).toBeNull();
    expect(client.resolveMediaUrl("/api/v1/playback/sessions/session/assets/video?token=server-issued")).toBe("https://media.example.com/api/v1/playback/sessions/session/assets/video?token=server-issued");
  });


  it("uses the v20 library mutation routes", async () => {
    const store = new MemoryCredentialStore();
    await store.save({ issuer: "https://media.example.com", tokens: tokens("library"), profileContext: "viewer" });
    const requests: CapturedRequest[] = [];
    const client = new RivuneTvClient("https://media.example.com", {
      platform: "webos",
      credentialStore: store,
      fetch: fetchQueue([
        json(DISCOVERY),
        json({ titleId: "title-1", mediaType: "movie", available: true, addedAt: "now", updatedAt: "now" }),
        new Response(null, { status: 204 }),
      ], requests),
    });

    await client.addLibraryTitle("title-1");
    await client.removeLibraryTitle("title-1");

    expect(requests[1].url).toBe("https://media.example.com/api/v1/library/title-1");
    expect(requests[1].init.method).toBe("PUT");
    expect(requests[2].url).toBe("https://media.example.com/api/v1/library/title-1");
    expect(requests[2].init.method).toBe("DELETE");
  });

  it("rejects a declared response body above 16 MiB before parsing", async () => {
    const oversized = json(DISCOVERY, 200, { "Content-Length": String(16 * 1024 * 1024 + 1) });
    const client = new RivuneTvClient("https://media.example.com", {
      platform: "browser",
      credentialStore: new MemoryCredentialStore(),
      fetch: fetchQueue([oversized], []),
    });

    await expect(client.discover()).rejects.toMatchObject({ code: "response_too_large" });
  });

  it("exposes bounded error envelope details and Retry-After", async () => {
    const store = new MemoryCredentialStore();
    await store.save({ issuer: "https://media.example.com", tokens: tokens("access"), profileContext: null });
    const client = new RivuneTvClient("https://media.example.com", {
      platform: "browser",
      credentialStore: store,
      fetch: fetchQueue([
        json(DISCOVERY),
        json({ error: { code: "browse_busy", message: "Try later" } }, 429, { "Retry-After": "42" }),
      ], []),
    });

    await expect(client.collections()).rejects.toMatchObject({
      status: 429,
      code: "browse_busy",
      message: "Try later",
      retryAfterSeconds: 42,
    });
  });
});
