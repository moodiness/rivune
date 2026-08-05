import { expect, test as base } from "@playwright/test";
import type { Page, Route } from "@playwright/test";

export type CapturedRequest = {
  method: string;
  pathname: string;
  search: URLSearchParams;
  body: unknown;
  profileId: string | null;
  authorization: string | null;
  profileContext: string | null;
};
type CollectionFolderFixture = {
  id: string;
  title: string;
  tileShape: "poster" | "landscape" | "square";
  sourceView?: "merged" | "categories" | "folders";
  coverImageUrl?: string;
  titleLogoUrl?: string;
  heroBackdropUrl?: string;
  focusGifEnabled: boolean;
  hideTitle: boolean;
  sources: Array<{ id?: string; kind: string; title: string; [key: string]: unknown }>;
};

type CollectionArtworkFixture = {
  backdropImageUrl: string;
  coverImageUrl: string;
  titleLogoUrl: string;
  heroBackdropUrl: string;
};


type CategoryRef = {
  id: string;
  name: string;
  color: string | null;
  icon: string | null;
};

type AccessCategory = CategoryRef & {
  description: string | null;
  position: number;
  isDefault: boolean;
  profileCount: number;
  deviceCount: number;
  createdAt: string;
  updatedAt: string;
};

type Profile = {
  id: string;
  name: string;
  description: string | null;
  categoryId: string;
  category: CategoryRef;
  isChild: boolean;
  hasPin: boolean;
  canManage: boolean;
  enabled: boolean;
  availableFrom: string | null;
  availableUntil: string | null;
  accessStartTime: string | null;
  accessEndTime: string | null;
  accessTimezone: string;
  accessible: boolean;
  avatar: { kind: "preset"; presetId: string; url: string };
};
type DeviceAuthorizationFixture = {
  deviceCode: string;
  userCode: string;
  verificationUri: string;
  verificationUriComplete: string;
  expiresAt: string;
  intervalSeconds: number;
};


type ManagedDevice = {
  id: string;
  name: string;
  platform: string;
  categoryId: string;
  category: CategoryRef;
  internalNote: string | null;
  approvedAt: string | null;
  lastSeenAt: string | null;
  createdAt: string;
  updatedAt: string;
};

type MetadataOperationResponse = {
  status: "succeeded" | "partial" | "failed";
  metadata: { candidates: number; refreshed: number; failed: number; failedTitles?: string[] };
  delayMilliseconds?: number;
  technicalPayload?: Record<string, unknown>;
};

const expiresAt = "2099-01-01T00:00:00Z";
const createdAt = "2024-01-01T00:00:00Z";
const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="320" height="180"><rect width="100%" height="100%" fill="#241f35"/></svg>`;

export const CATEGORY_IDS = {
  household: "10000000-0000-4000-8000-000000000001",
  kids: "10000000-0000-4000-8000-000000000002",
  guest: "10000000-0000-4000-8000-000000000003",
} as const;

export const DEVICE_IDS = {
  livingRoom: "20000000-0000-4000-8000-000000000001",
  tablet: "20000000-0000-4000-8000-000000000002",
} as const;

const categoryFixtures: AccessCategory[] = [
  { id: CATEGORY_IDS.household, name: "Household", description: "Primary household access.", color: "#6E7FF2", icon: "home", position: 0, isDefault: true, profileCount: 0, deviceCount: 0, createdAt, updatedAt: createdAt },
  { id: CATEGORY_IDS.kids, name: "Kids", description: "Age-appropriate profiles and devices.", color: "#F29A78", icon: "sparkles", position: 1, isDefault: false, profileCount: 0, deviceCount: 0, createdAt, updatedAt: createdAt },
  { id: CATEGORY_IDS.guest, name: "Guest", description: null, color: null, icon: null, position: 2, isDefault: false, profileCount: 0, deviceCount: 0, createdAt, updatedAt: createdAt },
];

const categoryRef = (category: AccessCategory): CategoryRef => ({
  id: category.id,
  name: category.name,
  color: category.color,
  icon: category.icon,
});

const fixtureCategoryRef = (id: string) => categoryRef(categoryFixtures.find((category) => category.id === id)!);

function wait(milliseconds: number): Promise<void> {
  const { promise, resolve } = Promise.withResolvers<void>();
  setTimeout(resolve, milliseconds);
  return promise;
}

const profileFixtures: Profile[] = [
  { id: "alice", name: "Alice", description: "Primary household profile.", categoryId: CATEGORY_IDS.household, category: fixtureCategoryRef(CATEGORY_IDS.household), isChild: false, hasPin: false, canManage: true, enabled: true, availableFrom: null, availableUntil: null, accessStartTime: null, accessEndTime: null, accessTimezone: "UTC", accessible: true, avatar: { kind: "preset", presetId: "alice", url: "https://fixtures.rivune.test/alice.svg" } },
  { id: "bob", name: "Bob", description: null, categoryId: CATEGORY_IDS.kids, category: fixtureCategoryRef(CATEGORY_IDS.kids), isChild: false, hasPin: false, canManage: false, enabled: true, availableFrom: null, availableUntil: null, accessStartTime: null, accessEndTime: null, accessTimezone: "UTC", accessible: true, avatar: { kind: "preset", presetId: "bob", url: "https://fixtures.rivune.test/bob.svg" } },
  { id: "casey", name: "Casey", description: null, categoryId: CATEGORY_IDS.kids, category: fixtureCategoryRef(CATEGORY_IDS.kids), isChild: true, hasPin: true, canManage: false, enabled: true, availableFrom: null, availableUntil: null, accessStartTime: null, accessEndTime: null, accessTimezone: "UTC", accessible: true, avatar: { kind: "preset", presetId: "casey", url: "https://fixtures.rivune.test/casey.svg" } },
];

const demoCategory: CategoryRef = { id: "30000000-0000-4000-8000-000000000001", name: "Demo", color: "#6E7FF2", icon: "sparkles" };
const demoProfiles: Profile[] = [
  { id: "demo-00000000-0000-4000-8000-000000000001", name: "Alex", description: null, categoryId: demoCategory.id, category: demoCategory, isChild: false, hasPin: false, canManage: false, enabled: true, availableFrom: null, availableUntil: null, accessStartTime: null, accessEndTime: null, accessTimezone: "UTC", accessible: true, avatar: { kind: "preset", presetId: "demo-alex", url: "/api/v1/demo/assets/alex.svg" } },
  { id: "demo-00000000-0000-4000-8000-000000000002", name: "Kids", description: null, categoryId: demoCategory.id, category: demoCategory, isChild: true, hasPin: false, canManage: false, enabled: true, availableFrom: null, availableUntil: null, accessStartTime: null, accessEndTime: null, accessTimezone: "UTC", accessible: true, avatar: { kind: "preset", presetId: "demo-kids", url: "/api/v1/demo/assets/kids.svg" } },
];

const deviceFixtures: ManagedDevice[] = [
  { id: DEVICE_IDS.livingRoom, name: "Living room TV", platform: "WebOS", categoryId: CATEGORY_IDS.household, category: fixtureCategoryRef(CATEGORY_IDS.household), internalNote: "Main display", approvedAt: createdAt, lastSeenAt: "2024-01-02T00:00:00Z", createdAt, updatedAt: createdAt },
  { id: DEVICE_IDS.tablet, name: "Kids tablet", platform: "Android", categoryId: CATEGORY_IDS.kids, category: fixtureCategoryRef(CATEGORY_IDS.kids), internalNote: null, approvedAt: createdAt, lastSeenAt: null, createdAt, updatedAt: createdAt },
];

const seasonZero = {
  id: "season-specials", mediaType: "season", seriesId: "series-1", name: "Specials", overview: "Behind the voyage.", seasonNumber: 0, episodeCount: 4, airDate: "2023-05-05", backdropUrl: "https://fixtures.rivune.test/season-specials-backdrop.svg", voteAverage: 0, externalIds: { tvdb: "1928275" },
  episodes: [
    { id: "special-1", mediaType: "episode", seasonId: "season-specials", name: "Building a World", overview: "The world behind the voyage.", seasonNumber: 0, episodeNumber: 1, airDate: "2023-06-30", stillUrl: "https://fixtures.rivune.test/special-1-still.svg", runtimeMinutes: 10, voteAverage: 0, voteCount: 0, externalIds: { tvdb: "9873798" } },
    { id: "special-2", mediaType: "episode", seasonId: "season-specials", name: "Questions of the Silo", overview: "Questions from the audience.", seasonNumber: 0, episodeNumber: 2, airDate: "2023-05-05", stillUrl: "https://fixtures.rivune.test/special-2-still.svg", runtimeMinutes: 8, voteAverage: 0, voteCount: 0, externalIds: { tvdb: "9873799" } },
    { id: "special-3", mediaType: "episode", seasonId: "season-specials", name: "Season 1 Recap", overview: "A recap of season one.", seasonNumber: 0, episodeNumber: 3, airDate: "2024-11-11", stillUrl: "https://fixtures.rivune.test/special-3-still.svg", runtimeMinutes: 5, voteAverage: 0, voteCount: 0, externalIds: { tvdb: "10798335" } },
    { id: "special-4", mediaType: "episode", seasonId: "season-specials", name: "The Rebellion in Season 2", overview: "Inside the second season.", seasonNumber: 0, episodeNumber: 4, airDate: "2024-11-15", runtimeMinutes: 7, voteAverage: 0, voteCount: 0, externalIds: { tvdb: "10806950" } },
  ],
};

const seasonOne = {
  id: "season-1", mediaType: "season", seriesId: "series-1", name: "Season 1", overview: "The first voyage.", seasonNumber: 1, episodeCount: 2, airDate: "2024-01-01", posterUrl: "https://fixtures.rivune.test/season-1-poster.svg", backdropUrl: "https://fixtures.rivune.test/season-1-backdrop.svg", voteAverage: 8.2, externalIds: { tmdb: "101" },
  episodes: [
    { id: "episode-1", mediaType: "episode", seasonId: "season-1", name: "First Light", overview: "The crew follows a mysterious signal.", seasonNumber: 1, episodeNumber: 1, airDate: "2024-01-03", stillUrl: "https://fixtures.rivune.test/episode-1-still.svg", backdropUrl: "https://fixtures.rivune.test/episode-1-backdrop.svg", runtimeMinutes: 30, voteAverage: 8.1, voteCount: 100, externalIds: { imdb: "tt900001" } },
    { id: "episode-2", mediaType: "episode", seasonId: "season-1", name: "Second Orbit", overview: "A new course changes everything.", seasonNumber: 1, episodeNumber: 2, airDate: "2024-01-10", stillUrl: "https://fixtures.rivune.test/episode-2-still.svg", backdropUrl: "https://fixtures.rivune.test/episode-2-backdrop.svg", runtimeMinutes: 31, voteAverage: 8.3, voteCount: 95, externalIds: { imdb: "tt900002" } },
  ],
};

const seasonTwo = {
  id: "season-2", mediaType: "season", seriesId: "series-1", name: "Season 2", overview: "The second voyage.", seasonNumber: 2, episodeCount: 1, airDate: "2024-06-01", posterUrl: "https://fixtures.rivune.test/season-2-poster.svg", voteAverage: 8.6, externalIds: { tmdb: "102" },
  episodes: [
    { id: "episode-3", mediaType: "episode", seasonId: "season-2", name: "Moonrise", overview: "The team reunites on a distant moon.", seasonNumber: 2, episodeNumber: 1, airDate: "2024-06-01", stillUrl: "https://fixtures.rivune.test/episode-3-still.svg", backdropUrl: "https://fixtures.rivune.test/episode-3-backdrop.svg", runtimeMinutes: 34, voteAverage: 8.7, voteCount: 88, externalIds: { imdb: "tt900003" } },
  ],
};

const dvdSeason = {
  id: "dvd-season-1", mediaType: "season", seriesId: "series-1", name: "DVD Season 1", overview: "The disc order.", seasonNumber: 1, episodeCount: 3, airDate: "2024-01-01", posterUrl: "https://fixtures.rivune.test/dvd-season-1-poster.svg", backdropUrl: "https://fixtures.rivune.test/dvd-season-1-backdrop.svg", voteAverage: 8.4, externalIds: { tvdb: "2001" },
  episodes: [
    { id: "dvd-episode-1", mediaType: "episode", seasonId: "dvd-season-1", name: "Disc Opening", overview: "The DVD order begins.", seasonNumber: 1, episodeNumber: 1, airDate: "2024-01-03", runtimeMinutes: 30, voteAverage: 8.1, voteCount: 100, externalIds: { tvdb: "2101" } },
    { id: "dvd-episode-2", mediaType: "episode", seasonId: "dvd-season-1", name: "Disc Middle", overview: "The DVD order continues.", seasonNumber: 1, episodeNumber: 2, airDate: "2024-01-10", runtimeMinutes: 31, voteAverage: 8.3, voteCount: 95, externalIds: { tvdb: "2102" } },
    { id: "dvd-episode-3", mediaType: "episode", seasonId: "dvd-season-1", name: "Disc Finale", overview: "The DVD order concludes.", seasonNumber: 1, episodeNumber: 3, airDate: "2024-01-17", runtimeMinutes: 32, voteAverage: 8.4, voteCount: 90, externalIds: { tvdb: "2103" } },
  ],
};

const seasonSummary = <T extends { episodes: unknown[] }>(season: T) => {
  const { episodes: _episodes, ...summary } = season;
  return summary;
};

const extraSeasonSummaries = Array.from({ length: 10 }, (_, index) => {
  const seasonNumber = index + 3;
  return {
    ...seasonSummary(seasonTwo),
    id: `season-${seasonNumber}`,
    name: `Season ${seasonNumber}`,
    seasonNumber,
    episodeCount: seasonNumber === 4 ? 0 : 1,
    posterUrl: `https://fixtures.rivune.test/season-${seasonNumber}-poster.svg`,
    externalIds: { tmdb: String(100 + seasonNumber) },
  };
});

const episodeOrders = [
  { id: "1", name: "Aired Order", type: "official", isDefault: true },
  { id: "2", name: "DVD Order", type: "dvd", isDefault: false },
  { id: "3", name: "Absolute Order", type: "absolute", isDefault: false },
  { id: "4", name: "Story Order", type: "alternate", isDefault: false },
  { id: "7", name: "Streaming Order", type: "alttwo", isDefault: false },
];

const series = {
  id: "series-1",
  mediaType: "series",
  name: "Signal Horizon",
  originalName: "Signal Horizon",
  originalLanguage: "en",
  overview: "Explorers cross the edge of known space.",
  firstAirDate: "2024-01-03",
  posterUrl: "https://fixtures.rivune.test/series-poster.svg",
  backdropUrl: "https://fixtures.rivune.test/series-backdrop.svg",
  logoUrl: "https://fixtures.rivune.test/series-logo.svg",
  tagline: "Beyond the map.",
  status: "Returning Series",
  numberOfSeasons: 12,
  numberOfEpisodes: 13,
  genres: [{ id: 1, name: "Science Fiction" }],
  voteAverage: 8.5,
  voteCount: 500,
  cast: [
    { id: "101", name: "Avery Stone", character: "Commander Ilya Voss", profileUrl: "https://fixtures.rivune.test/cast-1.svg" },
    { id: "102", name: "Mina Park", character: "Dr. Sera Vale", profileUrl: "https://fixtures.rivune.test/cast-2.svg" },
    { id: "103", name: "Omar Reed", character: "Elias Ward", profileUrl: "https://fixtures.rivune.test/cast-3.svg" },
    { id: "104", name: "Lucia Chen", character: "Captain Nia Sol", profileUrl: "https://fixtures.rivune.test/cast-4.svg" },
    { id: "105", name: "Noah Bennett", character: "Theo Quinn", profileUrl: "https://fixtures.rivune.test/cast-5.svg" },
    { id: "106", name: "Priya Shah", character: "Engineer Mara Keene", profileUrl: "https://fixtures.rivune.test/cast-1.svg" },
    { id: "107", name: "Jon Bell", character: "Admiral Corin", profileUrl: "https://fixtures.rivune.test/cast-2.svg" },
    { id: "108", name: "Élodie Martin", character: "Mira Sato", profileUrl: "https://fixtures.rivune.test/cast-3.svg" },
    { id: "109", name: "Sam Okafor", character: "Dr. Ren Cole", profileUrl: "https://fixtures.rivune.test/cast-4.svg" },
  ],
  seasons: [seasonSummary(seasonZero), seasonSummary(seasonOne), seasonSummary(seasonTwo), ...extraSeasonSummaries],
  episodeOrders,
  mappingProvider: "tmdb",
  externalIds: { imdb: "tt9000", tmdb: "9000", tvdb: "9900" },
};

const animeSeries = {
  ...series,
  id: "series-anime",
  name: "Solo Leveling",
  originalName: "俺だけレベルアップな件",
  originalLanguage: "ja",
  overview: "Humanity's weakest hunter discovers a system that lets him level up.",
  firstAirDate: "2024-01-07",
  tagline: "Only he levels up.",
  numberOfSeasons: 2,
  numberOfEpisodes: 25,
  genres: [{ id: 16, name: "Animation" }, { id: 10759, name: "Action & Adventure" }],
  cast: [
    { id: "201", name: "Taito Ban", character: "Sung Jinwoo", profileUrl: "https://fixtures.rivune.test/cast-1.svg" },
    { id: "202", name: "Reina Ueda", character: "Cha Hae-In", profileUrl: "https://fixtures.rivune.test/cast-2.svg" },
    { id: "203", name: "Genta Nakamura", character: "Yoo Jinho", profileUrl: "https://fixtures.rivune.test/cast-3.svg" },
    { id: "204", name: "Daisuke Hirakawa", character: "Choi Jong-In", profileUrl: "https://fixtures.rivune.test/cast-4.svg" },
    { id: "205", name: "Hiroki Touchi", character: "Baek Yoonho", profileUrl: "https://fixtures.rivune.test/cast-5.svg" },
    { id: "206", name: "Haruna Mikawa", character: "Sung Jinah", profileUrl: "https://fixtures.rivune.test/cast-1.svg" },
    { id: "207", name: "Makoto Furukawa", character: "Woo Jinchul", profileUrl: "https://fixtures.rivune.test/cast-2.svg" },
    { id: "208", name: "Banjou Ginga", character: "Go Gunhee", profileUrl: "https://fixtures.rivune.test/cast-3.svg" },
  ],
  externalIds: { imdb: "tt21209876", tmdb: "127532", tvdb: "389597" },
};

const movie = {
  id: "movie-1",
  mediaType: "movie",
  title: "Fight Club",
  originalTitle: "Fight Club",
  originalLanguage: "en",
  overview: "An insomniac and a soap maker form an underground club.",
  releaseDate: "1999-10-15",
  posterUrl: "https://fixtures.rivune.test/poster.svg",
  backdropUrl: "https://fixtures.rivune.test/backdrop.svg",
  runtimeMinutes: 139,
  genres: [{ id: 18, name: "Drama" }],
  voteAverage: 8.4,
  voteCount: 30000,
  cast: [
    { id: "301", name: "Edward Norton", character: "The Narrator", profileUrl: "https://fixtures.rivune.test/cast-1.svg" },
    { id: "302", name: "Brad Pitt", character: "Tyler Durden", profileUrl: "https://fixtures.rivune.test/cast-2.svg" },
    { id: "303", name: "Helena Bonham Carter", character: "Marla Singer", profileUrl: "https://fixtures.rivune.test/cast-3.svg" },
  ],
  externalIds: { imdb: "tt0137523", tmdb: "550" },
};

function json(route: Route, body: unknown, status = 200) {
  return route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
}

function collection(profileId: string) {
  const name = profileId === "bob" ? "Bob's Fresh Picks" : "Alice's Slow Shelf";
  return {
    id: `${profileId}-collection`, title: name, heroEnabled: false, pinToTop: false, focusGlowEnabled: false, viewMode: "follow_layout", folderCoverShape: "poster",
    folders: [{ id: `${profileId}-folder`, title: name, tileShape: "poster", focusGifEnabled: false, hideTitle: false, sources: [] } as CollectionFolderFixture],
    profileIds: [profileId], position: 0, version: 1, createdAt, updatedAt: createdAt,
  };
}

export class RivuneHarness {
  readonly requests: CapturedRequest[] = [];
  readonly collectionResponses: string[] = [];
  readonly deviceResponseCompletions: string[] = [];
  readonly accountRefreshCompletions: number[] = [];
  private activeProfileId: string | null = "alice";
  private activeProfileContext = "fixture-profile-context-alice-0";
  private profileContextSequence = 0;
  private userRole: "admin" | "member" | "demo" = "admin";
  private setupRequired = false;
  private demoAvailable = false;
  private demoSessionActive = false;
  private authorizationScope: "global_admin" | "category" = "global_admin";
  private sessionCategoryId: string | null = null;
  private deviceCategoryId = CATEGORY_IDS.household as string;
  private categories = categoryFixtures.map((category) => ({ ...category }));
  private profiles = profileFixtures.map((profile) => ({ ...profile, category: { ...profile.category }, avatar: { ...profile.avatar } }));
  private devices = deviceFixtures.map((device) => ({ ...device, category: { ...device.category } }));
  private nextCategorySequence = 4;
  private nextProfileSequence = 3;
  private nextDeviceSequence = 3;
  private readonly approvedDeviceCodes = new Map<string, { categoryId: string; deviceName?: string; internalNote?: string }>();
  private deviceAuthorization: DeviceAuthorizationFixture = {
    deviceCode: "fixture-device-code",
    userCode: "BCDF-GHJK",
    verificationUri: "/pair",
    verificationUriComplete: "/pair?code=BCDF-GHJK",
    expiresAt,
    intervalSeconds: 60,
  };
  private deviceAuthorizationFailure: { code: string; status: number } | null = null;
  private maintenance: { enabled: boolean; message: string | null } = { enabled: false, message: null };
  private instanceSettings: Record<string, unknown> = { allowTranscoding: true, maximumCastMembers: 20 };
  private readonly profileSettings = new Map<string, Record<string, unknown>>([
    ["alice", { transcoding: "inherit" }],
    ["bob", { transcoding: "inherit" }],
  ]);
  private readonly effectiveSettingsDelays: number[] = [];
  private readonly playbackStopDelays: number[] = [];
  private readonly collectionDelays = new Map<string, number>();
  private readonly collectionViewModes = new Map<string, "tabbed_grid" | "rows" | "follow_layout">();
  private readonly collectionSourcePosters = new Map<string, boolean>();
  private readonly collectionFolders = new Map<string, Array<Pick<CollectionFolderFixture, "id" | "title"> & Partial<CollectionFolderFixture>>>();
  private readonly collectionArtwork = new Map<string, CollectionArtworkFixture>();
  private readonly folderDelays = new Map<string, number>();
  private readonly seasonOverrides = new Map<string, unknown>();
  private libraryItems: Array<Record<string, unknown>> = [];
  private libraryMembershipDelay = 0;
  private readonly demoProgress = new Map<string, { positionSeconds: number; durationSeconds: number; completed: boolean; version: number }>();
  private readonly searchResponses = new Map<string, { body: unknown; status: number; delay: number }>();
  private readonly deviceResponses = new Map<string, { status: number; delay: number }>();
  private readonly deviceDeletionFailures = new Map<string, number>();
  private readonly accountRefreshResponses: Array<{ status: number; delay: number }> = [];
  private readonly profileRefreshAfterSelection = new Map<string, string>();
  private readonly hiddenCategoryCounts = new Set<string>();
  private readonly resolvedTitles = new Map<string, Record<string, unknown>>();
  private operations = {
    metadataCache: {
      entries: 48,
      freshEntries: 41,
      expiredEntries: 7,
      rootTitles: 32,
      missingTitles: 12,
      artworkSnapshots: 29,
    },
    metadataRefresh: {
      task: "metadata-refresh" as const,
      enabled: false,
      intervalHours: 24,
      language: "en",
      batchSize: 25,
      nextRunAt: null as string | null,
      lastStartedAt: null as string | null,
      lastCompletedAt: null as string | null,
      lastStatus: null as "succeeded" | "partial" | "failed" | null,
      lastResult: null as { candidates: number; refreshed: number; failed: number } | null,
    },
    housekeepingIntervalMinutes: 15,
  };
  private readonly metadataOperationResponses: MetadataOperationResponse[] = [];
  private playbackActivity = {
    summary: {
      activeSessions: 2,
      activeJobs: 1,
      processingSlots: 1,
      processingLimit: 3,
      storageBytes: 12_582_912,
      storageLimitBytes: 1_073_741_824,
    },
    diagnostics: { videoEncoder: "h264", hardwareToneMap: false },
    sessions: [],
    jobs: [],
  };


  async configurePreSetup(page: Page) {
    this.setupRequired = true;
    this.demoAvailable = true;
    this.demoSessionActive = false;
    this.userRole = "demo";
    this.authorizationScope = "category";
    this.sessionCategoryId = demoCategory.id;
    this.activeProfileId = demoProfiles[0].id;
    this.libraryItems = [];
    this.demoProgress.clear();
    this.demoProgress.set("episode-1", { positionSeconds: 321, durationSeconds: 1800, completed: false, version: 4 });
    await page.evaluate(() => {
      localStorage.removeItem("rivune.access");
      localStorage.removeItem("rivune.refresh");
      localStorage.removeItem("rivune.session");
      localStorage.removeItem("rivune.demo");
      sessionStorage.removeItem("rivune.access");
    });
  }
  async configureUnpaired(page: Page) {
    this.setupRequired = false;
    this.demoAvailable = false;
    await page.evaluate(() => {
      localStorage.removeItem("rivune.access");
      localStorage.removeItem("rivune.refresh");
      localStorage.removeItem("rivune.session");
      sessionStorage.removeItem("rivune.access");
    });
  }


  completeSetup() {
    this.setupRequired = false;
    this.demoAvailable = false;
    this.authorizationScope = "global_admin";
    this.sessionCategoryId = null;
    this.deviceCategoryId = CATEGORY_IDS.household;
  }

  private async configureActiveProfile(page: Page, profileId: string | null) {
    this.activeProfileId = profileId;
    this.activeProfileContext = profileId ? `fixture-profile-context-${profileId}-${++this.profileContextSequence}` : "";
    const profileContext = this.activeProfileContext;
    await page.evaluate(({ profileId, profileContext }) => {
      if (profileId && profileContext) {
        sessionStorage.setItem("rivune.profile", profileId);
        sessionStorage.setItem("rivune.profile.context", profileContext);
        return;
      }
      sessionStorage.removeItem("rivune.profile");
      sessionStorage.removeItem("rivune.profile.context");
    }, { profileId, profileContext });
  }

  async configureCategoryScope(page: Page, categoryId = CATEGORY_IDS.household) {
    const category = this.categories.find((candidate) => candidate.id === categoryId);
    if (!category) throw new Error(`Unknown fixture category ${categoryId}`);
    this.userRole = "admin";
    this.authorizationScope = "category";
    this.sessionCategoryId = category.id;
    this.deviceCategoryId = category.id;
    await this.configureActiveProfile(page, this.profiles.find((profile) => profile.categoryId === category.id)?.id ?? null);
  }

  async configureGlobalAdmin(page: Page, activeProfileId = "alice", deviceCategoryId = CATEGORY_IDS.household) {
    const category = this.categories.find((candidate) => candidate.id === deviceCategoryId);
    if (!category) throw new Error(`Unknown fixture category ${deviceCategoryId}`);
    this.userRole = "admin";
    this.authorizationScope = "global_admin";
    this.sessionCategoryId = null;
    this.deviceCategoryId = category.id;
    await this.configureActiveProfile(page, activeProfileId);
  }

  setProfileCategory(profileId: string, categoryId: string) {
    const category = this.categories.find((candidate) => candidate.id === categoryId);
    if (!category) throw new Error(`Unknown fixture category ${categoryId}`);
    const index = this.profiles.findIndex((candidate) => candidate.id === profileId);
    if (index < 0) throw new Error(`Unknown fixture profile ${profileId}`);
    this.profiles[index] = { ...this.profiles[index]!, categoryId, category: categoryRef(category) };
  }
  setProfileAvailability(profileId: string, availability: Partial<Pick<Profile, "enabled" | "accessible" | "availableFrom" | "availableUntil" | "accessStartTime" | "accessEndTime">>) {
    const index = this.profiles.findIndex((candidate) => candidate.id === profileId);
    if (index < 0) throw new Error(`Unknown fixture profile ${profileId}`);
    this.profiles[index] = { ...this.profiles[index]!, ...availability };
  }

  seedProfiles(count: number, categoryId = CATEGORY_IDS.household) {
    const category = this.categoryReference(categoryId);
    if (!category) throw new Error(`Unknown fixture category ${categoryId}`);
    for (let index = 0; index < count; index += 1) {
      const sequence = this.profiles.length + 1;
      this.profiles.push({
        ...profileFixtures[1]!,
        id: `seed-profile-${sequence}`,
        name: `Viewer ${sequence}`,
        categoryId,
        category,
        accessible: true,
        avatar: { ...profileFixtures[1]!.avatar },
      });
    }
  }

  setDeviceAuthorization(authorization: Partial<DeviceAuthorizationFixture>) {
    this.deviceAuthorization = { ...this.deviceAuthorization, ...authorization };
  }

  setDeviceAuthorizationFailure(code: string | null, status = 400) {
    this.deviceAuthorizationFailure = code ? { code, status } : null;
  }


  setInterfaceLanguage(language: string) {
    this.instanceSettings = { ...this.instanceSettings, interfaceLanguage: language };
  }

  seedCategory(name: string) {
    const id = `10000000-0000-4000-8000-${String(this.nextCategorySequence++).padStart(12, "0")}`;
    this.categories.push({ id, name, description: null, color: null, icon: null, position: this.categories.length, isDefault: false, profileCount: 0, deviceCount: 0, createdAt, updatedAt: createdAt });
    return id;
  }

  private currentProfiles() {
    if (this.userRole === "demo") return demoProfiles;
    if (this.authorizationScope === "category") return this.profiles.filter((profile) => profile.categoryId === this.sessionCategoryId);
    return this.profiles;
  }

  private accountProfiles() {
    if (this.userRole === "demo") return demoProfiles;
    const categoryId = this.authorizationScope === "category" ? this.sessionCategoryId : this.deviceCategoryId;
    return this.profiles.filter((profile) => profile.categoryId === categoryId);
  }

  private categoryReference(categoryId: string) {
    const category = this.categories.find((candidate) => candidate.id === categoryId);
    return category ? categoryRef(category) : null;
  }

  private categoryList() {
    return this.categories.map((category, position) => ({
      ...category,
      position,
      profileCount: this.hiddenCategoryCounts.has(category.id) ? 0 : this.profiles.filter((profile) => profile.categoryId === category.id).length,
      deviceCount: this.hiddenCategoryCounts.has(category.id) ? 0 : this.devices.filter((device) => device.categoryId === category.id).length,
    }));
  }
  setDeviceResponse(categoryId: string | undefined, options: { status?: number; delay?: number } = {}) {
    this.deviceResponses.set(categoryId ?? "all", { status: options.status ?? 200, delay: options.delay ?? 0 });
  }

  failNextDeviceDeletion(deviceId: string, status = 503) {
    this.deviceDeletionFailures.set(deviceId, status);
  }

  failNextAccountRefresh(delay = 0) {
    this.accountRefreshResponses.push({ status: 503, delay });
  }

  refreshProfileNameAfterSelection(profileId: string, name: string) {
    this.profileRefreshAfterSelection.set(profileId, name);
  }

  seedHiddenCategoryReference(categoryId: string) {
    const category = this.categoryReference(categoryId);
    const profileIndex = this.profiles.findIndex((profile) => profile.id === "casey");
    if (!category || profileIndex < 0) throw new Error(`Cannot seed a hidden reference for category ${categoryId}`);
    this.profiles[profileIndex] = { ...this.profiles[profileIndex]!, categoryId, category };
    this.hiddenCategoryCounts.add(categoryId);
  }
  setMaintenance(enabled: boolean, message: string | null = null) {
    if (enabled) this.activeProfileId = "bob";
    this.maintenance = { enabled, message };
  }

  setCollectionViewMode(profileId: string, viewMode: "tabbed_grid" | "rows" | "follow_layout") {
    this.collectionViewModes.set(profileId, viewMode);
  }
  setCollectionSourcePosters(profileId: string, enabled: boolean) {
    this.collectionSourcePosters.set(profileId, enabled);
  }


  delayCollections(profileId: string, milliseconds: number) {
    this.collectionDelays.set(profileId, milliseconds);
  }

  setCollectionFolders(profileId: string, folders: Array<Pick<CollectionFolderFixture, "id" | "title"> & Partial<CollectionFolderFixture>>) {
    this.collectionFolders.set(profileId, folders.map((folder) => ({ ...folder })));
  }

  setCollectionArtwork(profileId: string, artwork: CollectionArtworkFixture) {
    this.collectionArtwork.set(profileId, { ...artwork });
  }

  delayFolder(folderId: string, milliseconds: number) {
    this.folderDelays.set(folderId, milliseconds);
  }

  private collectionFor(profileId: string) {
    const value = collection(profileId);
    const configured = this.collectionFolders.get(profileId);
    if (configured) {
      value.folders = configured.map((folder) => ({ ...value.folders[0], ...folder }));
    }
    const artwork = this.collectionArtwork.get(profileId);
    return {
      ...value,
      ...(artwork ? { backdropImageUrl: `/api/v1/artwork/${profileId}-collection-backdrop` } : {}),
      folders: value.folders.map((folder, index) => artwork && index === 0 ? {
        ...folder,
        coverImageUrl: `/api/v1/artwork/${profileId}-folder-cover`,
        titleLogoUrl: `/api/v1/artwork/${profileId}-folder-logo`,
        heroBackdropUrl: `/api/v1/artwork/${profileId}-folder-backdrop`,
      } : folder),
      viewMode: this.collectionViewModes.get(profileId) ?? value.viewMode,
    };
  }

  private collectionManagementFor(profileId: string) {
    const value = this.collectionFor(profileId);
    const artwork = this.collectionArtwork.get(profileId);
    if (!artwork) return value;
    return {
      ...value,
      backdropImageUrl: artwork.backdropImageUrl,
      folders: value.folders.map((folder, index) => index === 0 ? {
        ...folder,
        coverImageUrl: artwork.coverImageUrl,
        titleLogoUrl: artwork.titleLogoUrl,
        heroBackdropUrl: artwork.heroBackdropUrl,
      } : folder),
    };
  }

  delayNextEffectiveSettings(milliseconds: number) {
    this.effectiveSettingsDelays.push(milliseconds);
  }
  delayNextPlaybackStop(milliseconds: number) {
    this.playbackStopDelays.push(milliseconds);
  }


  setSeason(id: string, season: unknown) {
    this.seasonOverrides.set(id, season);
  }

  setLibraryItems(items: Array<Record<string, unknown>>) {
    this.libraryItems = items;
  }
  setLibraryMembershipDelay(milliseconds: number) {
    this.libraryMembershipDelay = milliseconds;
  }
  setSearchResponse(type: string, skip: number, body: unknown, options: { status?: number; delay?: number } = {}) {
    this.searchResponses.set(`${type}:${skip}`, { body, status: options.status ?? 200, delay: options.delay ?? 0 });
  }

  queueMetadataOperationResponses(...responses: MetadataOperationResponse[]) {
    this.metadataOperationResponses.push(...responses);
  }

  matching(pathname: string, method?: string) {
    return this.requests.filter((request) => request.pathname === pathname && (!method || request.method === method));
  }

  async waitForRequest(pathname: string, method?: string) {
    await expect.poll(() => this.matching(pathname, method).length).toBeGreaterThan(0);
    return this.matching(pathname, method).at(-1)!;
  }

  async install(page: Page) {
    await page.route("**/*", (route) => this.handle(route));
    await page.goto("/__e2e_seed__");
    const profileID = this.activeProfileId;
    const profileContext = this.activeProfileContext;
    await page.evaluate(({ profileID, profileContext }) => {
      localStorage.setItem("rivune.refresh", "fixture-refresh");
      localStorage.setItem("rivune.device", "fixture-device");
      if (profileID) sessionStorage.setItem("rivune.profile", profileID);
      if (profileID && profileContext) sessionStorage.setItem("rivune.profile.context", profileContext);
    }, { profileID, profileContext });
  }

  private account() {
    const currentProfiles = this.accountProfiles();
    const sessionCategory = this.userRole === "demo"
      ? demoCategory
      : this.authorizationScope === "category" && this.sessionCategoryId
        ? this.categoryReference(this.sessionCategoryId)
        : null;
    return {
      user: { id: this.userRole === "demo" ? "demo-user" : "user-1", username: this.userRole === "demo" ? "demo" : "fixture-owner", role: this.userRole },
      session: {
        id: this.userRole === "demo" ? "demo-session" : "session-1",
        deviceId: this.userRole === "demo" ? "demo-browser" : "fixture-device",
        activeProfile: this.activeProfileId && currentProfiles.some((profile) => profile.id === this.activeProfileId) ? { id: this.activeProfileId, expiresAt } : null,
        authorizationScope: this.userRole === "demo" ? "category" : this.authorizationScope,
        category: sessionCategory,
      },
      profiles: currentProfiles,
      maintenance: this.maintenance,
    };
  }

  private async handle(route: Route) {
    const request = route.request();
    const url = new URL(request.url());
    if (url.pathname === "/__e2e_seed__") {
      await route.fulfill({ status: 200, contentType: "text/html", body: "<!doctype html><title>Rivune E2E seed</title>" });
      return;
    }
    if (url.hostname === "fixtures.rivune.test") {
      const contentType = url.pathname.endsWith(".vtt") ? "text/vtt" : url.pathname.endsWith(".mp4") ? "video/mp4" : "image/svg+xml";
      await route.fulfill({ status: 200, contentType, body: contentType === "text/vtt" ? "WEBVTT\n\n00:00.000 --> 00:02.000\nFixture caption" : contentType === "video/mp4" ? "" : svg });
      return;
    }
    if (url.hostname === "www.youtube-nocookie.com") {
      await route.fulfill({ status: 200, contentType: "text/html", body: "<!doctype html><title>Fixture trailer</title>" });
      return;
    }
    if (!url.pathname.startsWith("/api/v1") && url.pathname !== "/.well-known/rivune") {
      await route.continue();
      return;
    }

    let body: unknown;
    try { body = request.postData() ? request.postDataJSON() : undefined; } catch { body = request.postData(); }
    const path = url.pathname.startsWith("/api/v1") ? url.pathname.slice("/api/v1".length) : url.pathname;
    const profileAtRequest = this.activeProfileId;
    this.requests.push({ method: request.method(), pathname: url.pathname, search: new URLSearchParams(url.search), body, profileId: profileAtRequest, authorization: request.headers().authorization ?? null, profileContext: request.headers()["x-rivune-profile-context"] ?? null });

    if (url.pathname === "/.well-known/rivune") {
      await json(route, { name: "Rivune E2E", serverVersion: "1.2.3", protocolVersion: 19, apiBaseUrl: "/api/v1", setupRequired: this.setupRequired, setupCompleted: !this.setupRequired, demoAvailable: this.setupRequired && this.demoAvailable, timezone: "UTC", interfaceLanguage: typeof this.instanceSettings.interfaceLanguage === "string" ? this.instanceSettings.interfaceLanguage : "en" });
      return;
    }
    const profileContextExempt =
      path === "/setup" ||
      path === "/auth/logout" ||
      path === "/auth/me" ||
      request.method() === "GET" && (path === "/profiles" || path.startsWith("/profiles/") && path.endsWith("/avatar")) ||
      request.method() === "POST" && path.startsWith("/profiles/") && path.endsWith("/select");
    if (request.headers().authorization && this.activeProfileId && !profileContextExempt &&
      request.headers()["x-rivune-profile-context"] !== this.activeProfileContext) {
      await json(route, { error: { code: "profile_selection_required", message: "Select the active profile again" } }, 409);
      return;
    }
    if (path === "/demo/sessions" && request.method() === "POST") {
      if (!this.setupRequired || !this.demoAvailable) {
        await json(route, { error: { code: "demo_unavailable", message: "The server setup has been completed. Demo mode is no longer available." } }, 410);
        return;
      }
      this.demoSessionActive = true;
      this.userRole = "demo";
      this.authorizationScope = "category";
      this.sessionCategoryId = demoCategory.id;
      this.activeProfileId = demoProfiles[0].id;
      this.libraryItems = [];
      this.demoProgress.clear();
      this.demoProgress.set("episode-1", { positionSeconds: 321, durationSeconds: 1800, completed: false, version: 4 });
      await route.fulfill({
        status: 201,
        contentType: "application/json",
        headers: { "set-cookie": "rivune_demo=fixture-demo-cookie; HttpOnly; SameSite=Strict; Path=/api/v1" },
        body: JSON.stringify({ account: this.account() }),
      });
      return;
    }
    if (path === "/demo/session" && request.method() === "GET") {
      if (!this.setupRequired) {
        this.demoSessionActive = false;
        await json(route, { error: { code: "demo_unavailable", message: "The server setup has been completed. Demo mode is no longer available." } }, 410);
        return;
      }
      if (!this.demoSessionActive) {
        await json(route, { error: { code: "demo_session_not_found", message: "Demo session not found." } }, 401);
        return;
      }
      await json(route, { account: this.account() });
      return;
    }
    if (path === "/demo/session/reset" && request.method() === "POST") {
      if (!this.setupRequired) {
        this.demoSessionActive = false;
        await json(route, { error: { code: "demo_unavailable", message: "The server setup has been completed. Demo mode is no longer available." } }, 410);
        return;
      }
      if (!this.demoSessionActive) {
        await json(route, { error: { code: "demo_session_not_found", message: "Demo session not found." } }, 401);
        return;
      }
      this.activeProfileId = demoProfiles[0].id;
      this.libraryItems = [];
      this.demoProgress.clear();
      this.demoProgress.set("episode-1", { positionSeconds: 321, durationSeconds: 1800, completed: false, version: 4 });
      await json(route, { account: this.account() });
      return;
    }
    if (path === "/demo/session" && request.method() === "DELETE") {
      this.demoSessionActive = false;
      await route.fulfill({ status: 204, headers: { "set-cookie": "rivune_demo=; Max-Age=0; Path=/api/v1" } });
      return;
    }
    if (path.startsWith("/demo/assets/")) {
      if (!this.demoSessionActive || !this.setupRequired) {
        await json(route, { error: { code: this.setupRequired ? "demo_session_not_found" : "demo_unavailable", message: "Demo asset unavailable." } }, this.setupRequired ? 401 : 410);
        return;
      }
      await route.fulfill({ status: 200, contentType: "image/svg+xml", body: svg });
      return;
    }
    if (this.demoSessionActive && (
      path === "/settings" ||
      path.startsWith("/operations") ||
      path === "/auth/logout" ||
      path === "/setup" ||
      /^\/profiles\/[^/]+\/settings$/.test(path)
    )) {
      await json(route, { error: { code: "demo_forbidden", message: "Demo sessions cannot access this endpoint" } }, 403);
      return;
    }
    if (path === "/setup" && request.method() === "POST") {
      if (!this.setupRequired) {
        await json(route, { error: { code: "already_configured", message: "Server setup is already complete." } }, 409);
        return;
      }
      this.setupRequired = false;
      this.demoAvailable = false;
      this.demoSessionActive = false;
      this.userRole = "admin";
      this.authorizationScope = "global_admin";
      this.sessionCategoryId = null;
      this.activeProfileId = "alice";
      await json(route, { instance: { id: "instance-1" }, admin: { id: "user-1" }, profile: { id: "alice" } }, 201);
      return;
    }
    if (path === "/auth/login" && request.method() === "POST") {
      this.authorizationScope = "global_admin";
      this.sessionCategoryId = null;
      await json(route, { tokenType: "Bearer", accessToken: "fixture-access", accessTokenExpiresAt: expiresAt, refreshToken: "fixture-refresh", refreshTokenExpiresAt: expiresAt, sessionId: "session-1", deviceId: "fixture-device", authorizationScope: "global_admin", category: null });
      return;
    }
    const maintenanceSelection = path.match(/^\/profiles\/([^/]+)\/select$/);
    const maintenanceExempt = path === "/auth/refresh" || path === "/auth/me" || path === "/auth/logout" ||
      path === "/profiles" || path === "/profiles/selection" || maintenanceSelection !== null;
    const activeProfile = this.currentProfiles().find((profile) => profile.id === this.activeProfileId);
    if (this.maintenance.enabled && !activeProfile?.canManage && !maintenanceExempt) {
      await json(route, { error: { code: "maintenance_mode", message: "Rivune is temporarily unavailable for maintenance.", ...(this.maintenance.message ? { publicMessage: this.maintenance.message } : {}) } }, 503);
      return;
    }
    if (path === "/auth/refresh" && request.method() === "POST") {
      const sessionCategory = this.authorizationScope === "category" && this.sessionCategoryId ? this.categoryReference(this.sessionCategoryId) : null;
      await json(route, { tokenType: "Bearer", accessToken: "fixture-access", accessTokenExpiresAt: expiresAt, refreshToken: "fixture-refresh", refreshTokenExpiresAt: expiresAt, sessionId: "session-1", deviceId: "fixture-device", authorizationScope: this.authorizationScope, category: sessionCategory });
      return;
    }
    if (path === "/auth/device-code" && request.method() === "POST") {
      await json(route, this.deviceAuthorization);
      return;
    }
    if (path === "/auth/device-code/token" && request.method() === "POST") {
      if (this.deviceAuthorizationFailure) {
        await json(route, { error: { code: this.deviceAuthorizationFailure.code, message: "Device authorization failed" } }, this.deviceAuthorizationFailure.status);
        return;
      }
      const approval = this.approvedDeviceCodes.get(this.deviceAuthorization.userCode);
      if (!approval) {
        await json(route, { error: { code: "authorization_pending", message: "Authorization is pending" } }, 428);
        return;
      }
      const approvedCategory = this.categoryReference(approval.categoryId);
      await json(route, { tokenType: "Bearer", accessToken: "fixture-device-access", accessTokenExpiresAt: expiresAt, refreshToken: "fixture-device-refresh", refreshTokenExpiresAt: expiresAt, sessionId: "session-device-code", deviceId: this.devices.at(-1)?.id ?? DEVICE_IDS.livingRoom, authorizationScope: "category", category: approvedCategory });
      return;
    }
    if (path === "/auth/device-code/approve" && request.method() === "POST") {
      const input = body as { userCode?: string; categoryId?: string; deviceName?: string; internalNote?: string };
      const approvedCategory = input.categoryId ? this.categoryReference(input.categoryId) : null;
      const authorizedCategory = this.authorizationScope === "global_admin" || input.categoryId === this.sessionCategoryId;
      if (!approvedCategory || !authorizedCategory) {
        await json(route, { error: { code: "forbidden", message: "The server session cannot assign this category" } }, 403);
        return;
      }
      if (!input.userCode) {
        await json(route, { error: { code: "validation_failed", message: "A user code is required" } }, 422);
        return;
      }
      this.approvedDeviceCodes.set(input.userCode, { categoryId: approvedCategory.id, ...(input.deviceName ? { deviceName: input.deviceName } : {}), ...(input.internalNote ? { internalNote: input.internalNote } : {}) });
      const id = `20000000-0000-4000-8000-${String(this.nextDeviceSequence++).padStart(12, "0")}`;
      this.devices.push({ id, name: input.deviceName ?? "Approved device", platform: "Unknown", categoryId: approvedCategory.id, category: approvedCategory, internalNote: input.internalNote ?? null, approvedAt: createdAt, lastSeenAt: null, createdAt, updatedAt: createdAt });
      await route.fulfill({ status: 204 });
      return;
    }
    if (path === "/auth/sessions" && request.method() === "GET") {
      const sessionCategory = this.authorizationScope === "category" && this.sessionCategoryId ? this.categoryReference(this.sessionCategoryId) : null;
      await json(route, { sessions: [{ id: "session-1", deviceId: "fixture-device", deviceName: "Fixture browser", platform: "Web", ipAddress: null, createdAt, lastSeenAt: createdAt, current: true, authorizationScope: this.authorizationScope, category: sessionCategory }] });
      return;
    }
    if (path === "/auth/me") {
      const configured = this.accountRefreshResponses.shift();
      if (configured?.delay) await wait(configured.delay);
      if (configured && configured.status !== 200) {
        await json(route, { error: { code: "account_refresh_failed", message: "The refreshed account snapshot is unavailable" } }, configured.status);
        this.accountRefreshCompletions.push(configured.status);
        return;
      }
      await json(route, this.account());
      return;
    }
    if (path === "/settings/maintenance" && request.method() === "GET") { await json(route, this.maintenance); return; }
    if (path === "/settings/maintenance" && request.method() === "PUT") {
      this.maintenance = body as { enabled: boolean; message: string | null };
      await json(route, this.maintenance);
      return;
    }
    if (path === "/operations" && request.method() === "GET") {
      await json(route, this.operations);
      return;
    }
    if (path === "/operations/schedules/metadata-refresh" && request.method() === "PUT") {
      const input = body as { enabled: boolean; intervalHours: number; language: string; batchSize: number };
      this.operations.metadataRefresh = {
        ...this.operations.metadataRefresh,
        ...input,
        nextRunAt: input.enabled ? "2026-08-03T00:00:00Z" : null,
      };
      await json(route, this.operations.metadataRefresh);
      return;
    }
    const operationAction = path.match(/^\/operations\/actions\/(fetch-missing-metadata|run-housekeeping|clear-metadata-cache|clear-stream-cache)$/);
    if (operationAction && request.method() === "POST") {
      const action = operationAction[1];
      let status: "succeeded" | "partial" | "failed" = "succeeded";
      let result: Record<string, unknown> = {};
      if (action === "fetch-missing-metadata") {
        const response = this.metadataOperationResponses.shift() ?? {
          status: "succeeded" as const,
          metadata: { candidates: 12, refreshed: 12, failed: 0 },
        };
        status = response.status;
        if (response.delayMilliseconds) await wait(response.delayMilliseconds);
        result = {
          metadata: response.metadata,
          ...(response.technicalPayload ? { technicalPayload: response.technicalPayload } : {}),
        };
      } else if (action === "clear-metadata-cache") {
        result = { metadataCache: { entriesDeleted: this.operations.metadataCache.entries } };
        this.operations.metadataCache = { ...this.operations.metadataCache, entries: 0, freshEntries: 0, expiredEntries: 0 };
      } else if (action === "clear-stream-cache") {
        const { activeSessions: sessionsRemoved, activeJobs: jobsStopped, storageBytes } = this.playbackActivity.summary;
        result = { playback: { sessionsRemoved, jobsStopped, storageBytes } };
        this.playbackActivity.summary = {
          ...this.playbackActivity.summary,
          activeSessions: 0,
          activeJobs: 0,
          processingSlots: 0,
          storageBytes: 0,
        };
      }
      await json(route, { action, startedAt: createdAt, completedAt: createdAt, status, result });
      return;
    }
    if (path === "/playback/activity" && request.method() === "GET") {
      await json(route, this.playbackActivity);
      return;
    }
    if (path === "/categories" && request.method() === "GET") {
      if (this.authorizationScope !== "global_admin") { await json(route, { error: { code: "forbidden", message: "Global administration is required" } }, 403); return; }
      await json(route, { categories: this.categoryList() });
      return;
    }
    if (path === "/categories" && request.method() === "POST") {
      if (this.authorizationScope !== "global_admin") { await json(route, { error: { code: "forbidden", message: "Global administration is required" } }, 403); return; }
      const input = body as { name?: string; description?: string | null; color?: string | null; icon?: string | null };
      const normalizedName = input.name?.trim().toLocaleLowerCase();
      if (!normalizedName) { await json(route, { error: { code: "validation_failed", message: "A category name is required" } }, 422); return; }
      if (this.categories.some((category) => category.name.trim().toLocaleLowerCase() === normalizedName)) {
        await json(route, { error: { code: "category_name_conflict", message: "A category with this name already exists" } }, 409);
        return;
      }
      const id = `10000000-0000-4000-8000-${String(this.nextCategorySequence++).padStart(12, "0")}`;
      const category: AccessCategory = { id, name: input.name!.trim(), description: input.description ?? null, color: input.color ?? null, icon: input.icon ?? null, position: this.categories.length, isDefault: false, profileCount: 0, deviceCount: 0, createdAt, updatedAt: createdAt };
      this.categories.push(category);
      await json(route, { ...category }, 201);
      return;
    }
    if (path === "/categories/order" && request.method() === "PUT") {
      if (this.authorizationScope !== "global_admin") { await json(route, { error: { code: "forbidden", message: "Global administration is required" } }, 403); return; }
      const reorderInput = body as { categoryIds?: string[] };
      const categoryIds = reorderInput.categoryIds ?? [];
      const completeOrder = categoryIds.length === this.categories.length && new Set(categoryIds).size === categoryIds.length && categoryIds.every((id) => this.categories.some((category) => category.id === id));
      if (!completeOrder) { await json(route, { error: { code: "validation_failed", message: "The complete category order is required" } }, 422); return; }
      this.categories = categoryIds.map((id) => this.categories.find((category) => category.id === id)!);
      await json(route, { categories: this.categoryList() });
      return;
    }
    const categoryResource = path.match(/^\/categories\/([^/]+)$/);
    if (categoryResource && request.method() === "PATCH") {
      if (this.authorizationScope !== "global_admin") { await json(route, { error: { code: "forbidden", message: "Global administration is required" } }, 403); return; }
      const index = this.categories.findIndex((category) => category.id === categoryResource[1]);
      if (index < 0) { await json(route, { error: { code: "not_found", message: "Category not found" } }, 404); return; }
      const input = body as Partial<Pick<AccessCategory, "name" | "description" | "color" | "icon" | "isDefault">>;
      const normalizedName = input.name?.trim().toLocaleLowerCase();
      if (normalizedName && this.categories.some((category, candidateIndex) => candidateIndex !== index && category.name.trim().toLocaleLowerCase() === normalizedName)) {
        await json(route, { error: { code: "category_name_conflict", message: "A category with this name already exists" } }, 409);
        return;
      }
      let updated = { ...this.categories[index]!, ...input, ...(input.name ? { name: input.name.trim() } : {}), updatedAt: createdAt };
      this.categories[index] = updated;
      if (input.isDefault) {
        this.categories = this.categories.map((category) => ({ ...category, isDefault: category.id === updated.id }));
        updated = this.categories[index]!;
      }
      const updatedRef = categoryRef(updated);
      this.profiles = this.profiles.map((profile) => profile.categoryId === updated.id ? { ...profile, category: updatedRef } : profile);
      this.devices = this.devices.map((device) => device.categoryId === updated.id ? { ...device, category: updatedRef } : device);
      await json(route, { ...this.categoryList()[index] });
      return;
    }
    if (categoryResource && request.method() === "DELETE") {
      if (this.authorizationScope !== "global_admin") { await json(route, { error: { code: "forbidden", message: "Global administration is required" } }, 403); return; }
      const source = this.categories.find((category) => category.id === categoryResource[1]);
      if (!source) { await json(route, { error: { code: "not_found", message: "Category not found" } }, 404); return; }
      if (source.isDefault || this.categories.length === 1) { await json(route, { error: { code: "category_delete_conflict", message: "The default or final category cannot be deleted" } }, 409); return; }
      const input = body as { reassignToCategoryId?: string | null };
      const hasAssignments = this.profiles.some((profile) => profile.categoryId === source.id) || this.devices.some((device) => device.categoryId === source.id);
      const destination = input.reassignToCategoryId ? this.categories.find((category) => category.id === input.reassignToCategoryId && category.id !== source.id) : undefined;
      if (hasAssignments && !destination) { await json(route, { error: { code: "category_reassignment_required", message: "Referenced resources require reassignment" } }, 409); return; }
      if (destination) {
        const destinationRef = categoryRef(destination);
        this.profiles = this.profiles.map((profile) => profile.categoryId === source.id ? { ...profile, categoryId: destination.id, category: destinationRef } : profile);
        this.devices = this.devices.map((device) => device.categoryId === source.id ? { ...device, categoryId: destination.id, category: destinationRef } : device);
      }
      this.categories = this.categories.filter((category) => category.id !== source.id);
      await route.fulfill({ status: 204 });
      return;
    }
    if (path === "/devices" && request.method() === "GET") {
      if (this.authorizationScope !== "global_admin") { await json(route, { error: { code: "forbidden", message: "Global administration is required" } }, 403); return; }
      const filter = url.searchParams.get("categoryId");
      if (filter && !this.categoryReference(filter)) { await json(route, { error: { code: "not_found", message: "Category not found" } }, 404); return; }
      const responseKey = filter ?? "all";
      const configured = this.deviceResponses.get(responseKey);
      const devices = filter ? this.devices.filter((device) => device.categoryId === filter) : this.devices;
      if (configured?.delay) await wait(configured.delay);
      if (configured && configured.status !== 200) {
        await json(route, { error: { code: "device_list_failed", message: "The device list is temporarily unavailable" } }, configured.status);
      } else {
        await json(route, { devices });
      }
      this.deviceResponseCompletions.push(responseKey);
      return;
    }
    if (path === "/devices/category-moves" && request.method() === "POST") {
      if (this.authorizationScope !== "global_admin") { await json(route, { error: { code: "forbidden", message: "Global administration is required" } }, 403); return; }
      const input = body as { deviceIds?: string[]; categoryId?: string };
      const destination = input.categoryId ? this.categoryReference(input.categoryId) : null;
      if (!destination || !input.deviceIds?.length || input.deviceIds.some((id) => !this.devices.some((device) => device.id === id))) { await json(route, { error: { code: "not_found", message: "Device or category not found" } }, 404); return; }
      const deviceIds = new Set(input.deviceIds);
      this.devices = this.devices.map((device) => deviceIds.has(device.id) ? { ...device, categoryId: destination.id, category: destination, updatedAt: createdAt } : device);
      await route.fulfill({ status: 204 });
      return;
    }
    const deviceResource = path.match(/^\/devices\/([^/]+)$/);
    if (deviceResource && request.method() === "DELETE") {
      if (this.authorizationScope !== "global_admin") { await json(route, { error: { code: "forbidden", message: "Global administration is required" } }, 403); return; }
      const failureStatus = this.deviceDeletionFailures.get(deviceResource[1]!);
      if (failureStatus !== undefined) {
        this.deviceDeletionFailures.delete(deviceResource[1]!);
        await json(route, { error: { code: "device_delete_failed", message: "The device could not be deleted" } }, failureStatus);
        return;
      }
      const index = this.devices.findIndex((device) => device.id === deviceResource[1]);
      if (index < 0) { await json(route, { error: { code: "not_found", message: "Device not found" } }, 404); return; }
      this.devices.splice(index, 1);
      await route.fulfill({ status: 204 });
      return;
    }
    if (deviceResource && request.method() === "PATCH") {
      if (this.authorizationScope !== "global_admin") { await json(route, { error: { code: "forbidden", message: "Global administration is required" } }, 403); return; }
      const index = this.devices.findIndex((device) => device.id === deviceResource[1]);
      if (index < 0) { await json(route, { error: { code: "not_found", message: "Device not found" } }, 404); return; }
      const input = body as { name?: string; categoryId?: string; internalNote?: string | null };
      const destination = input.categoryId ? this.categoryReference(input.categoryId) : null;
      if (input.categoryId && !destination) { await json(route, { error: { code: "not_found", message: "Category not found" } }, 404); return; }
      const updated = { ...this.devices[index]!, ...input, ...(destination ? { categoryId: destination.id, category: destination } : {}), updatedAt: createdAt };
      this.devices[index] = updated;
      await json(route, updated);
      return;
    }
    if (path === "/profiles/category-moves" && request.method() === "POST") {
      if (this.authorizationScope !== "global_admin") { await json(route, { error: { code: "forbidden", message: "Global administration is required" } }, 403); return; }
      const input = body as { profileIds?: string[]; categoryId?: string };
      const destination = input.categoryId ? this.categoryReference(input.categoryId) : null;
      if (!destination || !input.profileIds?.length || input.profileIds.some((id) => !this.profiles.some((profile) => profile.id === id))) { await json(route, { error: { code: "not_found", message: "Profile or category not found" } }, 404); return; }
      const profileIds = new Set(input.profileIds);
      this.profiles = this.profiles.map((profile) => profileIds.has(profile.id) ? { ...profile, categoryId: destination.id, category: destination } : profile);
      if (this.activeProfileId && profileIds.has(this.activeProfileId)) this.activeProfileId = null;
      await route.fulfill({ status: 204 });
      return;
    }
    if (path === "/profiles" && request.method() === "POST") {
      const input = body as { name?: string; description?: string | null; categoryId?: string; isChild?: boolean; pin?: string; enabled?: boolean; availableFrom?: string; availableUntil?: string; accessStartTime?: string; accessEndTime?: string };
      const destination = input.categoryId ? this.categoryReference(input.categoryId) : null;
      const authorizedCategory = this.authorizationScope === "global_admin" || input.categoryId === this.sessionCategoryId;
      if (!destination || !authorizedCategory) { await json(route, { error: { code: "forbidden", message: "The server session cannot create a profile in this category" } }, 403); return; }
      if (!input.name?.trim()) { await json(route, { error: { code: "validation_failed", message: "A profile name is required" } }, 422); return; }
      const id = `40000000-0000-4000-8000-${String(this.nextProfileSequence++).padStart(12, "0")}`;
      const profile: Profile = { id, name: input.name.trim(), description: input.description ?? null, categoryId: destination.id, category: destination, isChild: input.isChild ?? false, hasPin: Boolean(input.pin), canManage: this.authorizationScope === "category", enabled: input.enabled ?? true, availableFrom: input.availableFrom ?? null, availableUntil: input.availableUntil ?? null, accessStartTime: input.accessStartTime ?? null, accessEndTime: input.accessEndTime ?? null, accessTimezone: "UTC", accessible: true, avatar: { kind: "preset", presetId: "aurora", url: "/api/v1/profile-avatars/aurora" } };
      this.profiles.push(profile);
      await json(route, profile, 201);
      return;
    }
    if (path === "/profiles" && request.method() === "GET") { await json(route, { profiles: this.currentProfiles() }); return; }
    const profileResource = path === "/profiles/selection" ? null : path.match(/^\/profiles\/([^/]+)$/);
    if (profileResource && request.method() === "PATCH") {
      const index = this.profiles.findIndex((profile) => profile.id === profileResource[1]);
      const visible = index >= 0 && (this.authorizationScope === "global_admin" || this.profiles[index]!.categoryId === this.sessionCategoryId);
      if (!visible) { await json(route, { error: { code: "not_found", message: "Profile not found" } }, 404); return; }
      const input = body as { name?: string; description?: string | null; categoryId?: string; isChild?: boolean; pin?: string | null; enabled?: boolean; availableFrom?: string | null; availableUntil?: string | null; accessStartTime?: string | null; accessEndTime?: string | null };
      const destination = input.categoryId ? this.categoryReference(input.categoryId) : null;
      if (input.categoryId && (!destination || this.authorizationScope === "category" && input.categoryId !== this.sessionCategoryId)) { await json(route, { error: { code: "forbidden", message: "The server session cannot move this profile" } }, 403); return; }
      const { pin: _pin, ...profileFields } = input;
      const updated = { ...this.profiles[index]!, ...profileFields, ...(destination ? { categoryId: destination.id, category: destination } : {}), ...(input.pin !== undefined ? { hasPin: input.pin !== null } : {}) };
      this.profiles[index] = updated;
      await json(route, updated);
      return;
    }
    if (profileResource && request.method() === "DELETE") {
      const index = this.profiles.findIndex((profile) => profile.id === profileResource[1]);
      const visible = index >= 0 && (this.authorizationScope === "global_admin" || this.profiles[index]!.categoryId === this.sessionCategoryId);
      if (!visible) { await json(route, { error: { code: "not_found", message: "Profile not found" } }, 404); return; }
      this.profiles.splice(index, 1);
      await route.fulfill({ status: 204 });
      return;
    }
    if (path === "/profile-avatars" && request.method() === "GET") { await json(route, { presets: [{ id: "aurora", name: "Aurora", url: "/api/v1/profile-avatars/aurora" }] }); return; }
    const profileSessionsResource = path.match(/^\/profiles\/([^/]+)\/sessions$/);
    if (profileSessionsResource && request.method() === "GET") {
      const profile = this.currentProfiles().find((candidate) => candidate.id === profileSessionsResource[1]);
      if (!profile) { await json(route, { error: { code: "not_found", message: "Profile not found" } }, 404); return; }
      const sessionCategory = this.authorizationScope === "category" && this.sessionCategoryId ? this.categoryReference(this.sessionCategoryId) : null;
      await json(route, { sessions: [{ id: "profile-session-1", userId: "user-1", username: "fixture-owner", deviceId: "fixture-device", deviceName: "Fixture browser", platform: "Web", ipAddress: null, createdAt, lastSeenAt: createdAt, profileGrantExpiresAt: expiresAt, current: profile.id === this.activeProfileId, authorizationScope: this.authorizationScope, category: sessionCategory }] });
      return;
    }
    if (path === "/settings" && request.method() === "GET") { await json(route, { schemaVersion: 1, settings: this.instanceSettings, updatedAt: createdAt }); return; }
    if (path === "/settings" && request.method() === "PATCH") {
      this.instanceSettings = { ...this.instanceSettings, ...(body as Record<string, unknown>) };
      await json(route, { schemaVersion: 1, settings: this.instanceSettings, updatedAt: createdAt });
      return;
    }
    const profileSettings = path.match(/^\/profiles\/([^/]+)\/settings$/);
    if (profileSettings && request.method() === "GET") { await json(route, { schemaVersion: 1, settings: this.profileSettings.get(profileSettings[1]) ?? {}, updatedAt: createdAt }); return; }
    if (profileSettings && request.method() === "PATCH") {
      const next = { ...(this.profileSettings.get(profileSettings[1]) ?? {}), ...(body as Record<string, unknown>) };
      this.profileSettings.set(profileSettings[1], next);
      await json(route, { schemaVersion: 1, settings: next, updatedAt: createdAt });
      return;
    }
    if (path === "/profiles/selection" && request.method() === "DELETE") {
      this.activeProfileId = null;
      this.activeProfileContext = "";
      await route.fulfill({ status: 204 });
      return;
    }
    const profileSelection = path.match(/^\/profiles\/([^/]+)\/select$/);
    if (profileSelection && request.method() === "POST") {
      const selected = this.accountProfiles().find((profile) => profile.id === profileSelection[1]);
      if (!selected) { await json(route, { error: { code: "not_found", message: "Profile not found" } }, 404); return; }
      if (this.maintenance.enabled && !selected.canManage) {
        await json(route, { error: { code: "maintenance_mode", message: "Rivune is temporarily unavailable for maintenance.", ...(this.maintenance.message ? { publicMessage: this.maintenance.message } : {}) } }, 503);
        return;
      }
      this.activeProfileId = selected.id;
      this.activeProfileContext = `fixture-profile-context-${selected.id}-${++this.profileContextSequence}`;
      const refreshedName = this.profileRefreshAfterSelection.get(selected.id);
      if (refreshedName) {
        this.profiles = this.profiles.map((profile) => profile.id === selected.id ? { ...profile, name: refreshedName } : profile);
        this.profileRefreshAfterSelection.delete(selected.id);
      }
      await json(route, { profile: selected, expiresAt, profileContext: this.activeProfileContext });
      return;
    }
    const effectiveSettings = path.match(/^\/profiles\/([^/]+)\/settings\/effective$/);
    if (effectiveSettings) {
      const profileValues = this.profileSettings.get(effectiveSettings[1]) ?? {};
      const profileLanguage = profileValues.interfaceLanguage;
      const instanceLanguage = this.instanceSettings.interfaceLanguage;
      const instanceAllowsTranscoding = this.instanceSettings.allowTranscoding !== false;
      const transcoding = profileValues.transcoding === "enabled" || profileValues.transcoding === "disabled" ? profileValues.transcoding : "inherit";
      const allowTranscoding = instanceAllowsTranscoding && transcoding !== "disabled";
      const instanceMaximumCastMembers = typeof this.instanceSettings.maximumCastMembers === "number" ? Math.min(100, Math.max(1, this.instanceSettings.maximumCastMembers)) : 20;
      const maximumCastMembers = typeof profileValues.maximumCastMembers === "number" ? Math.min(instanceMaximumCastMembers, Math.max(1, profileValues.maximumCastMembers)) : instanceMaximumCastMembers;
      const notificationsEnabled = typeof this.instanceSettings.notificationsEnabled === "boolean" ? this.instanceSettings.notificationsEnabled : true;
      const notificationDurationSeconds = typeof this.instanceSettings.notificationDurationSeconds === "number" ? this.instanceSettings.notificationDurationSeconds : 5;
      const notificationPollIntervalSeconds = typeof this.instanceSettings.notificationPollIntervalSeconds === "number" ? this.instanceSettings.notificationPollIntervalSeconds : 5;
      const notificationSource = (key: string) => key in this.instanceSettings ? "instance" : "default";
      const interfaceLanguage = typeof profileLanguage === "string"
        ? profileLanguage
        : typeof instanceLanguage === "string" ? instanceLanguage : "en";
      const responseDelay = this.effectiveSettingsDelays.shift() ?? 0;
      if (responseDelay > 0) await wait(responseDelay);
      await json(route, { schemaVersion: 1, settings: { interfaceLanguage, allowTranscoding, transcoding, maximumCastMembers, autoplayNextEpisode: true, animationsEnabled: false, notificationsEnabled, notificationDurationSeconds, notificationPollIntervalSeconds, metadataLanguage: "en-US", metadataRegion: "US", audioLanguage: "en", subtitleLanguage: "en" }, sources: { interfaceLanguage: typeof profileLanguage === "string" ? "profile" : typeof instanceLanguage === "string" ? "instance" : "default", allowTranscoding: instanceAllowsTranscoding ? transcoding === "disabled" ? "profile" : "instance" : "instance", transcoding: "profile", maximumCastMembers: typeof profileValues.maximumCastMembers === "number" ? "profile" : "instance", notificationsEnabled: notificationSource("notificationsEnabled"), notificationDurationSeconds: notificationSource("notificationDurationSeconds"), notificationPollIntervalSeconds: notificationSource("notificationPollIntervalSeconds") } });
      return;
    }
    if (path === "/auth/notifications") { await json(route, { notifications: [] }); return; }
    if (path === "/auth/notifications/broadcast" && request.method() === "POST") {
      const input = body as { idempotencyKey: string; message: string };
      await json(route, { id: input.idempotencyKey, message: input.message, senderUsername: "fixture-owner", recipientCount: 3, createdAt }, 201);
      return;
    }
    const collectionManagement = path.match(/^\/collections\/([^/]+)\/management$/);
    if (collectionManagement && request.method() === "GET") {
      const collectionProfile = collectionManagement[1].replace(/-collection$/, "");
      await json(route, this.collectionManagementFor(collectionProfile));
      return;
    }
    if (path === "/collections") {
      const collectionProfile = profileAtRequest ?? "alice";
      const delay = this.collectionDelays.get(collectionProfile) ?? 0;
      if (delay > 0) await wait(delay);
      await json(route, { collections: [this.collectionFor(collectionProfile)] });
      this.collectionResponses.push(collectionProfile);
      return;
    }
    const folderItems = path.match(/^\/collections\/([^/]+)\/folders\/([^/]+)\/items$/);
    if (folderItems) {
      const collectionProfile = folderItems[1].split("-")[0];
      const folderID = folderItems[2];
      const responseDelay = this.folderDelays.get(folderID) ?? 0;
      if (responseDelay > 0) await wait(responseDelay);
      const resolvedCollection = this.collectionFor(collectionProfile);
      const folder = resolvedCollection.folders.find((candidate) => candidate.id === folderID) ?? resolvedCollection.folders[0];
      const configured = this.collectionFolders.has(collectionProfile);
      const title = configured ? `${folder.title} Exclusive` : collectionProfile === "bob" ? "Bob Exclusive" : "Alice Exclusive";
      const itemID = configured ? `${collectionProfile}-${folderID}-exclusive` : `${collectionProfile}-exclusive`;
      const posterURL = configured ? `/api/v1/artwork/${itemID}` : `https://fixtures.rivune.test/${itemID}.svg`;
      const sourcePosterUrls = this.collectionSourcePosters.get(collectionProfile) === false
        ? {}
        : Object.fromEntries(folder.sources.flatMap((source) =>
          source.id ? [[source.id, `/api/v1/artwork/${source.id}-collection-poster`]] : [],
        ));
      const itemSources = folder.sources.slice(0, 1).flatMap((source) =>
        source.id ? [{ id: source.id, kind: source.kind, title: source.title }] : [],
      );
      await json(route, {
        collectionId: folderItems[1],
        folder,
        items: [{ id: itemID, mediaType: "movie", title, posterUrl: posterURL, description: `${title} fixture`, sources: itemSources }],
        ...(Object.keys(sourcePosterUrls).length > 0 ? { sourcePosterUrls } : {}),
        page: 1,
        hasMore: false,
        errors: [],
      });
      return;
    }
    if (path === "/continue-watching") {
      const items = profileAtRequest === "bob"
        ? [{ titleId: "bob-episode", mediaType: "episode", seriesId: "bob-series", seasonId: "bob-season", seasonNumber: 1, episodeNumber: 1, positionSeconds: 60, durationSeconds: 1200, version: 1, reason: "resume", title: "Bob Queue", resourceId: "bob-resource", resourceProvider: "imdb", lastWatchedAt: createdAt }]
        : [{ titleId: "episode-1", mediaType: "episode", seriesId: "series-1", seasonId: "season-1", seasonNumber: 1, episodeNumber: 1, positionSeconds: 321, durationSeconds: 1800, version: 4, reason: "resume", title: "Signal Horizon", resourceId: "tt9000:1:1", resourceProvider: "imdb", lastWatchedAt: createdAt }];
      await json(route, { items });
      return;
    }
    const seasonOverride = path.match(/^\/metadata\/seasons\/([^/]+)$/);
    if (seasonOverride && this.seasonOverrides.has(seasonOverride[1])) {
      await json(route, this.seasonOverrides.get(seasonOverride[1]));
      return;
    }
    if (path === "/metadata/seasons/season-specials") { await json(route, seasonZero); return; }
    if (path === "/metadata/seasons/season-1") { await json(route, seasonOne); return; }
    if (path === "/metadata/seasons/season-2") { await json(route, seasonTwo); return; }
    if (path === "/metadata/seasons/dvd-season-1") { await json(route, dvdSeason); return; }
    if (path === "/metadata/seasons/bob-season") { await json(route, { ...seasonOne, id: "bob-season", seriesId: "bob-series", episodes: [] }); return; }
    if (path === "/metadata/series/series-1") {
      if (url.searchParams.get("episodeOrder") === "2") {
        await json(route, {
          ...series,
          numberOfSeasons: 1,
          numberOfEpisodes: 3,
          seasons: [seasonSummary(dvdSeason)],
          selectedEpisodeOrderId: "2",
          mappingProvider: "tvdb",
        });
        return;
      }
      await json(route, series);
      return;
    }
    if (path === "/metadata/series/series-anime") { await json(route, animeSeries); return; }
    if (path === "/metadata/titles/movie-1") { await json(route, movie); return; }
    const catalogSearch = path.match(/^\/addons\/catalogs\/search\/([^/]+)$/);
    if (catalogSearch && request.method() === "GET") {
      const type = decodeURIComponent(catalogSearch[1]);
      const skip = Number(url.searchParams.get("skip") ?? "0");
      const configured = this.searchResponses.get(`${type}:${skip}`);
      if (configured?.delay) await wait(configured.delay);
      await json(route, configured?.body ?? { results: [], errors: [] }, configured?.status ?? 200);
      return;
    }
    if (path.startsWith("/addons/resources/meta/")) { await json(route, { results: [], errors: [] }); return; }
    if (path === "/library/membership" && request.method() === "POST") {
      if (this.libraryMembershipDelay) await wait(this.libraryMembershipDelay);
      const rawIdentities = body && typeof body === "object" && "identities" in body ? body.identities : undefined;
      const identities = Array.isArray(rawIdentities) ? rawIdentities.flatMap((identity) =>
        identity && typeof identity === "object" &&
        "sourceAddonId" in identity && typeof identity.sourceAddonId === "string" &&
        "resourceId" in identity && typeof identity.resourceId === "string"
          ? [{ sourceAddonId: identity.sourceAddonId, resourceId: identity.resourceId }]
          : [],
      ) : [];
      const items = identities.flatMap((identity) => {
        const saved = this.libraryItems.find((item) =>
          item.mediaType === "tv" &&
          item.sourceAddonId === identity.sourceAddonId &&
          item.resourceId === identity.resourceId,
        );
        return saved ? [{
          sourceAddonId: identity.sourceAddonId,
          resourceId: identity.resourceId,
          titleId: saved.titleId,
        }] : [];
      });
      await json(route, { items });
      return;
    }
    if (path === "/library") {
      const mediaType = url.searchParams.get("mediaType");
      const items = mediaType ? this.libraryItems.filter((item) => item.mediaType === mediaType) : this.libraryItems;
      await json(route, { items, page: 1, totalPages: items.length > 0 ? 1 : 0, totalResults: items.length });
      return;
    }
    const libraryMutation = path.match(/^\/library\/([^/]+)$/);
    if (libraryMutation && request.method() === "PUT") {
      const titleId = decodeURIComponent(libraryMutation[1]);
      if (!this.libraryItems.some((item) => item.titleId === titleId)) {
        const resolved = this.resolvedTitles.get(titleId) ?? { titleId, mediaType: "movie", resourceId: titleId, title: titleId };
        this.libraryItems.push({ ...resolved, addedAt: createdAt, updatedAt: createdAt, available: resolved.available ?? true });
      }
      await json(route, { titleId });
      return;
    }
    if (libraryMutation && request.method() === "DELETE") {
      const titleId = decodeURIComponent(libraryMutation[1]);
      this.libraryItems = this.libraryItems.filter((item) => item.titleId !== titleId);
      await route.fulfill({ status: 204 });
      return;
    }
    if (path === "/titles/watched/batch" && request.method() === "PUT") {
      const rawItems = body && typeof body === "object" && "items" in body ? body.items : undefined;
      const inputs = Array.isArray(rawItems) ? rawItems.flatMap((input) =>
        input && typeof input === "object" &&
        "titleId" in input && typeof input.titleId === "string" &&
        "completed" in input && typeof input.completed === "boolean" &&
        "expectedVersion" in input && typeof input.expectedVersion === "number"
          ? [{ titleId: input.titleId, completed: input.completed, expectedVersion: input.expectedVersion }]
          : []
      ) : [];
      const items = inputs.map((input) => ({
        titleId: input.titleId,
        progress: {
          titleId: input.titleId,
          mediaType: input.titleId.startsWith("episode-") ? "episode" : "movie",
          positionSeconds: input.completed ? 1800 : 0,
          durationSeconds: 1800,
          completed: input.completed,
          version: input.expectedVersion + 1,
          updatedAt: createdAt,
        },
      }));
      await json(route, { items });
      return;
    }
    const watchedMutation = path.match(/^\/titles\/([^/]+)\/watched$/);
    if (watchedMutation && (request.method() === "POST" || request.method() === "DELETE")) {
      const titleId = decodeURIComponent(watchedMutation[1]);
      const watched = request.method() === "POST";
      const expectedVersion = watched
        ? Number((body as { expectedVersion?: number } | undefined)?.expectedVersion ?? 0)
        : Number(url.searchParams.get("expectedVersion") ?? 0);
      await json(route, {
        titleId,
        mediaType: titleId.startsWith("episode-") ? "episode" : "movie",
        positionSeconds: watched ? 1800 : 0,
        durationSeconds: 1800,
        completed: watched,
        version: expectedVersion + 1,
        updatedAt: createdAt,
      });
      return;
    }
    if (path === "/progress/batch" && request.method() === "POST") {
      const rawTitleIds = body && typeof body === "object" && "titleIds" in body ? body.titleIds : undefined;
      const titleIds = Array.isArray(rawTitleIds) ? rawTitleIds.filter((titleId): titleId is string => typeof titleId === "string") : [];
      const items = titleIds.map((titleId) => {
        const saved = this.userRole === "demo" ? this.demoProgress.get(titleId) : undefined;
        const progress = saved ?? { positionSeconds: titleId === "episode-1" ? 321 : 0, durationSeconds: 1800, completed: false, version: titleId === "episode-1" ? 4 : 0 };
        return { titleId, progress: { titleId, mediaType: titleId.startsWith("episode-") ? "episode" : "movie", ...progress, updatedAt: createdAt } };
      });
      await json(route, { items });
      return;
    }
    if (path.startsWith("/progress/") && request.method() === "GET") {
      const titleId = decodeURIComponent(path.slice("/progress/".length));
      const saved = this.userRole === "demo" ? this.demoProgress.get(titleId) : undefined;
      const progress = saved ?? { positionSeconds: titleId === "episode-1" ? 321 : 0, durationSeconds: 1800, completed: false, version: titleId === "episode-1" ? 4 : 0 };
      await json(route, { titleId, ...progress, updatedAt: createdAt });
      return;
    }
    if (path.startsWith("/progress/") && request.method() === "PUT") {
      const titleId = decodeURIComponent(path.slice("/progress/".length));
      const input = body as { positionSeconds: number; durationSeconds: number; completed: boolean; expectedVersion: number };
      const progress = { positionSeconds: input.positionSeconds, durationSeconds: input.durationSeconds, completed: input.completed, version: input.expectedVersion + 1 };
      if (this.userRole === "demo") this.demoProgress.set(titleId, progress);
      await json(route, { titleId, ...progress, updatedAt: createdAt });
      return;
    }
    if (path === "/playback/markers" && request.method() === "GET") {
      await json(route, { markers: [{ type: "intro", startSeconds: 330, endSeconds: 400, confidence: 1, submissionCount: 2 }] });
      return;
    }
    if (path === "/playback/sources" && request.method() === "POST") {
      const resourceId = String((body as { resourceId?: string })?.resourceId ?? "unknown");
      await json(route, { sources: [{ id: `option-${resourceId}`, sourceRef: `source-${resourceId}`, addonId: "fixture-addon", manifestId: "fixture-manifest", streamIndex: 0, name: "Fixture 1080p", description: "Deterministic direct stream", protocol: "http", container: "mp4", expiresAt }], providerErrors: [] });
      return;
    }
    if (path === "/playback/prepare" && request.method() === "POST") {
      await json(route, { sourceRef: (body as { sourceRef: string }).sourceRef, mode: "direct", protocol: "http", container: "mp4", media: { container: "mp4", durationSeconds: 1800, hdrFormat: "sdr", videoTracks: [{ index: 0, type: "video", codec: "h264", width: 1920, height: 1080 }], audioTracks: [{ index: 0, type: "audio", codec: "aac", language: "en", title: "English", channels: 2 }, { index: 2, type: "audio", codec: "aac", language: "fr", title: "French", channels: 2 }], subtitleTracks: [] }, subtitleCount: 2, expiresAt });
      return;
    }
    if (path === "/playback/resolve" && request.method() === "POST") {
      const input = body as { sourceRef: string; titleId?: string; preferredAudioTrack?: number; preferredSubtitleId?: string };
      const sessionID = `session-${this.matching("/api/v1/playback/resolve", "POST").length}`;
      const selectedSubtitleID = input.preferredSubtitleId ?? "none";
      const burnsSubtitles = selectedSubtitleID === "sub-burn";
      const source = {
        id: "resolved-source",
        addonId: "fixture-addon",
        manifestId: "fixture-manifest",
        name: burnsSubtitles ? "Fixture 1080p with burned subtitles" : "Fixture 1080p",
        mode: burnsSubtitles ? "transcode" : "direct",
        url: burnsSubtitles ? `/api/v1/playback/sessions/${sessionID}/assets/master.m3u8?file=master.m3u8` : "https://fixtures.rivune.test/video.mp4",
        protocol: burnsSubtitles ? "hls" : "http",
        container: "mp4",
        compatible: true,
        media: { container: "mp4", durationSeconds: 1800, hdrFormat: "sdr", videoTracks: [{ index: 0, type: "video", codec: "h264", width: 1920, height: 1080 }], audioTracks: [{ index: 0, type: "audio", codec: "aac", language: "en", title: "English", channels: 2 }, { index: 2, type: "audio", codec: "aac", language: "fr", title: "French", channels: 2 }], subtitleTracks: [] },
      };
      await json(route, {
        id: sessionID,
        selectedSourceId: source.id,
        selectedAudioTrack: input.preferredAudioTrack ?? 0,
        selectedSubtitleId: selectedSubtitleID,
        sources: [source],
        subtitles: [
          { id: "sub-en", addonId: "fixture-addon", manifestId: "fixture-manifest", language: "en", delivery: "external", url: "https://fixtures.rivune.test/subtitles-en.vtt", default: false },
          { id: "sub-fr", addonId: "fixture-addon", manifestId: "fixture-manifest", language: "fr", delivery: "external", url: "https://fixtures.rivune.test/subtitles-fr.vtt", default: false },
          { id: "sub-es-empty", addonId: "fixture-addon", manifestId: "fixture-manifest", language: "es", delivery: "external", url: "", default: false },
          { id: "sub-burn", addonId: "fixture-addon", manifestId: "fixture-manifest", language: "ja", delivery: "burn", default: false },
        ],
        providerErrors: [],
        expiresAt,
      });
      return;
    }
    if (/^\/playback\/sessions\/[^/]+\/assets\/master\.m3u8$/.test(path) && request.method() === "GET") {
      await route.fulfill({
        contentType: "application/vnd.apple.mpegurl",
        body: "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:1\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-ENDLIST\n",
      });
      return;
    }
    if (/^\/playback\/sessions\//.test(path) && request.method() === "DELETE") {
      const responseDelay = this.playbackStopDelays.shift() ?? 0;
      if (responseDelay > 0) await wait(responseDelay);
      await route.fulfill({ status: 204 });
      return;
    }
    const trailers = path.match(/^\/metadata\/titles\/([^/]+)\/trailers$/);
    if (trailers) {
      const titleID = decodeURIComponent(trailers[1]);
      const seasonNumber = url.searchParams.get("seasonNumber");
      const movieTrailer = titleID === "movie-1";
      const label = movieTrailer ? "Fight Club Trailer" : seasonNumber === "2" ? "Season Two Trailer" : "Season One Trailer";
      const youtubeId = movieTrailer ? "fight-club" : seasonNumber === "2" ? "season-two" : "season-one";
      await json(route, { trailers: [{ youtubeId, name: label, language: "en", isFallback: false, captionPreference: "en" }] });
      return;
    }
    if (path === "/calendar") {
      const today = new Date().toISOString().slice(0, 10);
      await json(route, { events: [{ id: "calendar-episode-3", titleId: "episode-3", mediaType: "episode", title: "Moonrise", releaseDate: today, resourceId: "tt9000:2:1", resourceProvider: "imdb", seriesTitle: "Signal Horizon", seriesId: "series-1", seasonId: "season-2", seasonNumber: 2, episodeNumber: 1 }] });
      return;
    }
    if (path === "/titles/resolve" && request.method() === "POST") {
      const input = body as { externalId?: string; mediaType?: string; sourceAddonId?: string; resourceId?: string };
      const titleId = input.mediaType === "tv"
        ? `tv-${input.sourceAddonId ?? "addon"}-${input.resourceId ?? input.externalId ?? "channel"}`
        : input.externalId === "tt9000" || input.externalId === "9000"
          ? "series-1"
          : input.externalId === "tt21209876"
            ? "series-anime"
            : input.externalId === "tt0137523" || input.externalId === "550"
              ? "movie-1"
              : "resolved-title";
      const resolved = { ...(body as object), titleId };
      this.resolvedTitles.set(titleId, resolved as Record<string, unknown>);
      await json(route, resolved);
      return;
    }
    await json(route, { error: { code: "fixture_route_missing", message: `No E2E fixture for ${request.method()} ${path}` } }, 501);
  }
}

export const test = base.extend<{ rivune: RivuneHarness }>({
  rivune: async ({ page }, use) => {
    const rivune = new RivuneHarness();
    await rivune.install(page);
    await use(rivune);
  },
});

export { expect };
