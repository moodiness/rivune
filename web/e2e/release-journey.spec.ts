import { expect, request, test, type APIRequestContext, type APIResponse, type Page } from "@playwright/test";

const baseURL = process.env.RIVUNE_RELEASE_BASE_URL;
const setupToken = process.env.RIVUNE_RELEASE_SETUP_TOKEN;
const addonManifestURL = process.env.RIVUNE_RELEASE_ADDON_MANIFEST_URL;
const adminPassword = process.env.RIVUNE_RELEASE_ADMIN_PASSWORD;
if (!baseURL || !setupToken || !addonManifestURL || !adminPassword) {
  throw new Error("RIVUNE_RELEASE_BASE_URL, RIVUNE_RELEASE_SETUP_TOKEN, RIVUNE_RELEASE_ADDON_MANIFEST_URL, and RIVUNE_RELEASE_ADMIN_PASSWORD are required");
}

const API_PREFIX = "/api/v1";

type SessionCredentials = { accessToken: string; profileContext: string };
type Profile = { id: string; name: string; categoryId: string };
type Account = { profiles: Profile[] };
type AccessCategory = { id: string; name: string };
type DeviceAuthorization = { deviceCode: string; userCode: string; intervalSeconds: number };
type InstalledAddon = { id: string; manifest: { id: string; name: string } };
type Source = { sourceRef: string };
type PlaybackSession = { id: string; selectedSourceId: string; sources: Array<{ id: string; url?: string }> };

async function responseJSON<T>(response: APIResponse, operation: string, expectedStatus = 200): Promise<T> {
  if (response.status() !== expectedStatus) {
    throw new Error(`${operation} returned ${response.status()}: ${await response.text()}`);
  }
  return response.json() as Promise<T>;
}

async function credentials(page: Page): Promise<SessionCredentials> {
  const values = await page.evaluate(() => ({
    accessToken: sessionStorage.getItem("rivune.access") ?? localStorage.getItem("rivune.access") ?? "",
    profileContext: sessionStorage.getItem("rivune.profile.context") ?? "",
  }));
  expect(values.accessToken, "access token").not.toBe("");
  expect(values.profileContext, "profile context").not.toBe("");
  return values;
}

async function authenticatedAPI(values: SessionCredentials): Promise<APIRequestContext> {
  return request.newContext({
    baseURL,
    extraHTTPHeaders: {
      Authorization: `Bearer ${values.accessToken}`,
      "X-Rivune-Profile-Context": values.profileContext,
    },
  });
}

test("published image completes setup, pairing, add-on search, and playback", async ({ browser, page }) => {
  const unexpectedBrowserOrigins = new Set<string>();
  const expectedOrigin = new URL(baseURL).origin;
  const observe = (observedPage: Page) => observedPage.on("request", (outgoing) => {
    const url = new URL(outgoing.url());
    if (url.protocol === "http:" || url.protocol === "https:") {
      if (url.origin !== expectedOrigin) unexpectedBrowserOrigins.add(url.origin);
    }
  });
  observe(page);

  await page.goto("/");
  await page.getByRole("button", { name: "Set up server", exact: true }).click();
  await page.getByLabel("Space name").fill("Release Journey");
  await page.getByLabel("Administrator account").fill("release-owner");
  await page.getByLabel("First profile").fill("Release Profile");
  await page.getByLabel("Administrator password").fill(adminPassword);
  await page.getByLabel("Setup token").fill(setupToken);
  await page.getByRole("button", { name: /Create my space/ }).click();
  await expect(page.getByRole("heading", { name: "Who's watching?" })).toBeVisible();
  await page.getByRole("button", { name: /Release Profile/ }).click();
  await expect(page.getByRole("navigation", { name: "Main navigation" })).toBeVisible();

  const adminCredentials = await credentials(page);
  const adminAPI = await authenticatedAPI(adminCredentials);
  const account = await responseJSON<Account>(await adminAPI.get(`${API_PREFIX}/auth/me`), "load administrator account");
  const profile = account.profiles.find((candidate) => candidate.name === "Release Profile");
  expect(profile, "created profile").toBeDefined();
  const categories = await responseJSON<{ categories: AccessCategory[] }>(await adminAPI.get(`${API_PREFIX}/categories`), "load access categories");
  const profileCategory = categories.categories.find((category) => category.id === profile!.categoryId);
  expect(profileCategory, "profile access category").toBeDefined();

  const deviceContext = await browser.newContext({ locale: "en-US", timezoneId: "UTC" });
  const devicePage = await deviceContext.newPage();
  observe(devicePage);
  const authorizationResponse = devicePage.waitForResponse((response) => response.url().endsWith(`${API_PREFIX}/auth/device-code`) && response.request().method() === "POST");
  await devicePage.goto("/");
  const authorization = await responseJSON<DeviceAuthorization>(await authorizationResponse, "begin device authorization", 201);
  await expect(devicePage.getByText(authorization.userCode, { exact: true })).toBeVisible();
  expect(authorization.intervalSeconds).toBeGreaterThan(0);
  expect(authorization.intervalSeconds).toBeLessThanOrEqual(10);

  await page.goto(`/pair?code=${encodeURIComponent(authorization.userCode)}`);
  await expect(page.getByRole("heading", { name: "Approve a device." })).toBeVisible();
  await page.getByRole("combobox", { name: "Access category" }).click();
  await page.getByRole("option", { name: profileCategory!.name, exact: true }).click();
  await page.getByLabel("Device name").fill("Release Journey Browser");
  await page.getByRole("button", { name: "Approve device", exact: true }).click();
  await expect(page.getByRole("heading", { name: "They can choose a profile now." })).toBeVisible();
  await expect(devicePage.getByRole("heading", { name: "Who's watching?" })).toBeVisible({ timeout: 20_000 });
  await devicePage.getByRole("button", { name: /Release Profile/ }).click();
  await expect(devicePage.getByRole("navigation", { name: "Main navigation" })).toBeVisible();

  const deviceAPI = await authenticatedAPI(await credentials(devicePage));
  const verification = await responseJSON<{ id: string; status: "passed" | "failed"; summary: string }>(await adminAPI.post(`${API_PREFIX}/addons/verifications`, {
    data: { transportUrl: addonManifestURL, profileIds: [profile!.id], categoryIds: [] },
  }), "verify release add-on", 201);
  expect(verification).toMatchObject({ status: "passed", summary: "ready" });
  expect(verification.id).toMatch(/^[0-9a-f-]{36}$/i);
  const installed = await responseJSON<InstalledAddon>(await adminAPI.post(`${API_PREFIX}/addons`, {
    data: { verificationId: verification.id },
  }), "install verified release add-on", 201);
  expect(installed.manifest).toMatchObject({ id: "org.rivune.release-journey", name: "Release Journey Fixture" });

  const search = await responseJSON<{
    results: Array<{ addonId: string; payload: { metas: Array<{ id: string; name: string }> } }>;
    errors: unknown[];
  }>(await deviceAPI.get(`${API_PREFIX}/addons/catalogs/search/movie?search=release%20journey&skip=0&limit=10`), "search release add-on");
  expect(search.errors).toEqual([]);
  expect(search.results).toHaveLength(1);
  expect(search.results[0]?.addonId).toBe(installed.id);
  expect(search.results[0]?.payload.metas).toContainEqual(expect.objectContaining({ id: "release-demo", name: "Release Journey" }));

  const metadata = await responseJSON<{
    results: Array<{ payload: { meta: { id: string; name: string } } }>;
    errors: unknown[];
  }>(await deviceAPI.get(`${API_PREFIX}/addons/resources/meta/movie/release-demo`), "load release metadata");
  expect(metadata.errors).toEqual([]);
  expect(metadata.results[0]?.payload.meta).toMatchObject({ id: "release-demo", name: "Release Journey" });

  const title = await responseJSON<{ titleId: string }>(await deviceAPI.post(`${API_PREFIX}/titles/resolve`, {
    data: {
      mediaType: "movie",
      provider: "addon",
      externalId: "release-demo",
      resourceId: "release-demo",
      title: "Release Journey",
      releaseInfo: "2026",
      released: "2026-01-01T00:00:00.000Z",
      sourceAddonId: installed.id,
      sourceCatalogId: "release-search",
      sourceName: "Release Journey Fixture",
    },
  }), "resolve release title");
  expect(title.titleId).toMatch(/^[0-9a-f-]{36}$/i);

  const capabilities = {
    streamingProtocols: ["http"], containers: ["mp4"], videoCodecs: ["h264"], audioCodecs: ["aac"],
    processingModes: ["remux", "transcode_audio", "transcode"], subtitleModes: ["external", "burn"], maximumAudioChannels: 2,
    mediaProfiles: [{ container: "mp4", videoCodec: "h264", audioCodec: "aac", maximumVideoBitDepth: 8 }], hdrFormats: ["sdr"], externalPlayers: [],
  };
  const sourceList = await responseJSON<{ sources: Source[]; providerErrors: unknown[] }>(await deviceAPI.post(`${API_PREFIX}/playback/sources`, {
    data: { mediaType: "movie", addonId: installed.id, resourceId: "release-demo", capabilities },
  }), "load playback sources");
  expect(sourceList.providerErrors).toEqual([]);
  expect(sourceList.sources).toHaveLength(1);
  const sourceRef = sourceList.sources[0]!.sourceRef;
  const preparation = await responseJSON<{ sourceRef: string; mode: string; protocol: string }>(await deviceAPI.post(`${API_PREFIX}/playback/prepare`, {
    data: { sourceRef, startSeconds: 0 },
  }), "prepare release playback");
  expect(preparation).toMatchObject({ sourceRef, mode: "direct", protocol: "http" });

  const playback = await responseJSON<PlaybackSession>(await deviceAPI.post(`${API_PREFIX}/playback/resolve`, {
    data: { sourceRef, titleId: title.titleId, startSeconds: 0 },
  }), "resolve release playback", 201);
  const selected = playback.sources.find((source) => source.id === playback.selectedSourceId);
  expect(selected?.url).toMatch(new RegExp(`^${API_PREFIX}/playback/sessions/${playback.id}/assets/`));
  const range = await deviceAPI.get(selected!.url!, { headers: { Range: "bytes=0-1023" } });
  expect(range.status()).toBe(206);
  expect(range.headers()["content-range"]).toMatch(/^bytes 0-1023\/\d+$/);
  expect((await range.body()).byteLength).toBe(1024);

  const activity = await responseJSON<{ summary: { activeSessions: number }; sessions: Array<{ id: string }> }>(await adminAPI.get(`${API_PREFIX}/playback/activity`), "load active playback");
  expect(activity.summary.activeSessions).toBeGreaterThanOrEqual(1);
  expect(activity.sessions).toContainEqual(expect.objectContaining({ id: playback.id }));
  expect((await deviceAPI.delete(`${API_PREFIX}/playback/sessions/${playback.id}`)).status()).toBe(204);
  await expect.poll(async () => {
    const current = await responseJSON<{ sessions: Array<{ id: string }> }>(await adminAPI.get(`${API_PREFIX}/playback/activity`), "confirm playback stopped");
    return current.sessions.some((session) => session.id === playback.id);
  }).toBe(false);

  expect(unexpectedBrowserOrigins).toEqual(new Set());
  await deviceAPI.dispose();
  await adminAPI.dispose();
  await deviceContext.close();
});
