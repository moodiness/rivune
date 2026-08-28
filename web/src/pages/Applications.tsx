import { useEffect, useMemo, useState } from "react";
import {
  Check,
  Cable,
  Code2,
  Copy,
  Download,
  ExternalLink,
  Monitor,
  KeyRound,
  Languages,
  QrCode,
  RefreshCw,
  Terminal,
  ShieldAlert,
  ShieldCheck,
  Smartphone,
  Tv,
} from "lucide-react";
import { QRCodeSVG } from "qrcode.react";
import { Button, RivuneMark } from "../components";
import { interfaceLanguages, locale, setLocale, translate as t } from "../i18n";
import type { Locale, TranslationKey } from "../i18n";

const releaseEndpoint = "https://api.github.com/repos/moodiness/rivune/releases/latest";
const releaseAssetPrefix = "https://github.com/moodiness/rivune/releases/download/";
const sha256Pattern = /^sha256:([0-9a-f]{64})$/;
const repositoryURL = "https://github.com/moodiness/rivune";
const semverTagPattern = /^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/;
const maximumReleaseResponseBytes = 512 * 1024;
const installerAssetNames = [
  "Rivune-TV-Installer-Windows.exe",
  "Rivune-TV-Installer-macOS.dmg",
] as const;
type InstallerAssetName = typeof installerAssetNames[number];

type AssetName =
  | "Rivune-Android.apk"
  | "Rivune-webOS.ipk"
  | "Rivune-Tizen.wgt"
  | "Rivune-iOS-unsigned.ipa"
  | "Rivune-tvOS-unsigned.ipa"
  | "Rivune-visionOS-unsigned.ipa"
  | "Rivune-macOS.dmg"
  | "Rivune-Windows.exe";

type ReleaseAsset = {
  name: string;
  size: number;
  digest: string;
  browser_download_url: string;
  state: "uploaded";
};

type Release = {
  tag_name: string;
  name: string;
  html_url: string;
  published_at: string;
  draft: false;
  prerelease: false;
  assets: ReleaseAsset[];
};

type AssetSpec = {
  name: AssetName;
  platform: "Android" | "LG webOS" | "Samsung Tizen" | "iPhone & iPad" | "Apple TV" | "Apple Vision Pro" | "macOS" | "Windows";
  detailKey: Extract<TranslationKey, `applications.asset.${string}.detail`>;
  signature: "signed" | "unsigned";
  warningKey: Extract<TranslationKey, `applications.asset.${string}.warning`>;
  icon: typeof Smartphone;
};

type DeviceRecommendation = {
  labelKey: Extract<TranslationKey, `applications.device.${string}`>;
  assets: readonly AssetName[];
};

const assetSpecs: AssetSpec[] = [
  {
    name: "Rivune-Android.apk",
    platform: "Android",
    detailKey: "applications.asset.android.detail",
    signature: "signed",
    warningKey: "applications.asset.android.warning",
    icon: Smartphone,
  },
  {
    name: "Rivune-webOS.ipk",
    platform: "LG webOS",
    detailKey: "applications.asset.webos.detail",
    signature: "unsigned",
    warningKey: "applications.asset.webos.warning",
    icon: Tv,
  },
  {
    name: "Rivune-Tizen.wgt",
    platform: "Samsung Tizen",
    detailKey: "applications.asset.tizen.detail",
    signature: "unsigned",
    warningKey: "applications.asset.tizen.warning",
    icon: Tv,
  },
  {
    name: "Rivune-iOS-unsigned.ipa",
    platform: "iPhone & iPad",
    detailKey: "applications.asset.ios.detail",
    signature: "unsigned",
    warningKey: "applications.asset.ios.warning",
    icon: Smartphone,
  },
  {
    name: "Rivune-tvOS-unsigned.ipa",
    platform: "Apple TV",
    detailKey: "applications.asset.tvos.detail",
    signature: "unsigned",
    warningKey: "applications.asset.tvos.warning",
    icon: Tv,
  },
  {
    name: "Rivune-visionOS-unsigned.ipa",
    platform: "Apple Vision Pro",
    detailKey: "applications.asset.visionos.detail",
    signature: "unsigned",
    warningKey: "applications.asset.visionos.warning",
    icon: Monitor,
  },
  {
    name: "Rivune-macOS.dmg",
    platform: "macOS",
    detailKey: "applications.asset.macos.detail",
    signature: "unsigned",
    warningKey: "applications.asset.macos.warning",
    icon: Monitor,
  },
  {
    name: "Rivune-Windows.exe",
    platform: "Windows",
    detailKey: "applications.asset.windows.detail",
    signature: "unsigned",
    warningKey: "applications.asset.windows.warning",
    icon: Monitor,
  },
];

const releaseManifestName = "rivune-update.json";
const auxiliaryReleaseAssetNames = [releaseManifestName, "rivune-update.json.sig", "Rivune-TV-runtime.json", ...installerAssetNames] as const;
const expectedReleaseAssetNames: readonly string[] = [...assetSpecs.map((asset) => asset.name), ...auxiliaryReleaseAssetNames];


type UserAgentData = {
  platform?: string;
  getHighEntropyValues?: (hints: string[]) => Promise<{ architecture?: string; bitness?: string; platform?: string }>;
};

async function deviceRecommendation(): Promise<DeviceRecommendation> {
  const navigatorWithHints = navigator as Navigator & { userAgentData?: UserAgentData };
  const userAgent = navigator.userAgent;
  let platform = navigatorWithHints.userAgentData?.platform ?? navigator.platform ?? "";
  try {
    const values = await navigatorWithHints.userAgentData?.getHighEntropyValues?.(["platform"]);
    platform = values?.platform ?? platform;
  } catch {
    // Client hints are optional; the user-agent fallback remains available.
  }

  if (/web0s|webos/i.test(userAgent) || /web0s|webos/i.test(platform)) {
    return { labelKey: "applications.device.webos", assets: ["Rivune-webOS.ipk"] };
  }
  if (/tizen/i.test(userAgent) && /samsung|smart-tv/i.test(userAgent)) {
    return { labelKey: "applications.device.tizen", assets: ["Rivune-Tizen.wgt"] };
  }
  if (/android/i.test(userAgent) || /android/i.test(platform)) {
    return { labelKey: "applications.device.android", assets: ["Rivune-Android.apk"] };
  }
  if (/appletv/i.test(userAgent)) {
    return { labelKey: "applications.device.appleTV", assets: ["Rivune-tvOS-unsigned.ipa"] };
  }
  if (/vision/i.test(userAgent) || /xr/i.test(platform)) {
    return { labelKey: "applications.device.vision", assets: ["Rivune-visionOS-unsigned.ipa"] };
  }
  if (/iphone|ipad|ipod/i.test(userAgent) || (/mac/i.test(platform) && navigator.maxTouchPoints > 1)) {
    return { labelKey: "applications.device.ios", assets: ["Rivune-iOS-unsigned.ipa"] };
  }
  if (/mac/i.test(platform) || /macintosh/i.test(userAgent)) {
    return { labelKey: "applications.device.mac", assets: ["Rivune-macOS.dmg"] };
  }
  if (/win/i.test(platform) || /windows/i.test(userAgent)) {
    return { labelKey: "applications.device.windows", assets: ["Rivune-Windows.exe"] };
  }
  return { labelKey: "applications.device.unknown", assets: [] };
}

function validRelease(value: unknown): value is Release {
  if (typeof value !== "object" || value === null) return false;
  const release = value as Partial<Release>;
  if (
    typeof release.tag_name !== "string" || !semverTagPattern.test(release.tag_name) ||
    release.name !== release.tag_name || release.draft !== false || release.prerelease !== false ||
    typeof release.published_at !== "string" || Number.isNaN(Date.parse(release.published_at)) ||
    release.html_url !== `https://github.com/moodiness/rivune/releases/tag/${release.tag_name}` ||
    !Array.isArray(release.assets)
  ) return false;

  const expectedAssetNames = expectedReleaseAssetNames;
  if (release.assets.length !== expectedAssetNames.length) return false;
  const assets = new Map<string, ReleaseAsset>();
  for (const candidate of release.assets) {
    if (typeof candidate !== "object" || candidate === null) return false;
    const asset = candidate as Partial<ReleaseAsset>;
    if (typeof asset.name !== "string" || !expectedAssetNames.includes(asset.name) || assets.has(asset.name)) return false;
    if (
      asset.state !== "uploaded" ||
      typeof asset.size !== "number" || !Number.isSafeInteger(asset.size) || asset.size <= 0 ||
      typeof asset.digest !== "string" || !sha256Pattern.test(asset.digest) ||
      asset.browser_download_url !== `${releaseAssetPrefix}${release.tag_name}/${asset.name}`
    ) return false;
    assets.set(asset.name, asset as ReleaseAsset);
  }
  return assets.size === expectedAssetNames.length;
}

async function loadLatestRelease(signal: AbortSignal): Promise<Release> {
  const response = await fetch(releaseEndpoint, {
    headers: { Accept: "application/vnd.github+json" },
    cache: "no-store",
    signal,
  });
  if (!response.ok) throw new Error(`GitHub returned HTTP ${response.status}`);
  const declaredLength = Number(response.headers.get("Content-Length"));
  if (Number.isFinite(declaredLength) && declaredLength > maximumReleaseResponseBytes) throw new Error("Release metadata is too large");
  const contents = await response.text();
  if (new TextEncoder().encode(contents).byteLength > maximumReleaseResponseBytes) throw new Error("Release metadata is too large");
  let parsed: unknown;
  try {
    parsed = JSON.parse(contents);
  } catch {
    throw new Error("Release metadata is not valid JSON");
  }
  if (!validRelease(parsed)) throw new Error("Release metadata failed validation");
  return parsed;
}

function formatSize(bytes: number): string {
  const units = ["B", "KiB", "MiB", "GiB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value >= 10 || unit === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[unit]}`;
}

function supportsLocalSigningGuide(tagName: string): boolean {
  const [major, minor] = tagName.slice(1).split(".").map(Number);
  return major > 1 || major === 1 && minor >= 10;
}

function supportsTVInstaller(tagName: string): boolean {
  const [major, minor] = tagName.slice(1).split(".").map(Number);
  return major > 1 || major === 1 && minor >= 12;
}

const signatureLabelKeys = {
  signed: "applications.card.signed",
  unsigned: "applications.card.unsigned",
} as const satisfies Record<AssetSpec["signature"], TranslationKey>;

function ReleaseCard({ asset, spec, recommended, signingGuideAvailable }: { asset: ReleaseAsset; spec: AssetSpec; recommended: boolean; signingGuideAvailable: boolean }) {
  const [showQR, setShowQR] = useState(false);
  const [copied, setCopied] = useState(false);
  const digest = asset.digest.replace("sha256:", "");
  const Icon = spec.icon;

  async function copyDigest() {
    try {
      await navigator.clipboard.writeText(digest);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1800);
    } catch {
      setCopied(false);
    }
  }

  return <article className={`applications-card ${recommended ? "is-recommended" : ""}`} data-asset={asset.name}>
    <div className="applications-card__topline">
      <span className="applications-card__icon"><Icon size={22} /></span>
      <div>
        <div className="applications-card__title-line">
          <h2>{spec.platform}</h2>
          {recommended && <span className="applications-badge applications-badge--recommended">{t("applications.card.recommended")}</span>}
        </div>
        <p>{t(spec.detailKey)}</p>
      </div>
    </div>

    <div className="applications-card__facts" aria-label={t("applications.card.releaseDetails", { platform: spec.platform })}>
      <span>{formatSize(asset.size)}</span>
      <span className={spec.signature === "signed" ? "is-signed" : "is-unsigned"}>
        {spec.signature === "signed" ? <ShieldCheck size={15} /> : <ShieldAlert size={15} />}
        {t(signatureLabelKeys[spec.signature])}
      </span>
    </div>

    <div className="applications-card__actions">
      <a className="button button--primary" href={asset.browser_download_url}>
        <Download size={17} /> {t("applications.card.download")}
      </a>
      <Button variant="secondary" type="button" onClick={() => setShowQR((current) => !current)} aria-expanded={showQR}>
        <QrCode size={17} /> {t(showQR ? "applications.card.hideQR" : "applications.card.showQR")}
      </Button>
    </div>

    {showQR && <div className="applications-card__qr">
      <div className="applications-card__qr-code">
        <QRCodeSVG value={asset.browser_download_url} size={156} level="M" marginSize={2} title={t("applications.card.qrTitle", { asset: asset.name })} />
      </div>
      <p>{t("applications.card.qrBody")}</p>
    </div>}
    {signingGuideAvailable && spec.signature === "unsigned" && spec.name.endsWith(".ipa") && <a className="applications-card__signing-guide" href="#apple-signing"><KeyRound size={13} /> {t("applications.card.signLocally")}</a>}

    {(spec.name === "Rivune-webOS.ipk" || spec.name === "Rivune-Tizen.wgt") && <a className="applications-card__signing-guide" href="#tv-installer"><Cable size={13} /> {t("applications.card.installWithCompanion")}</a>}
    <div className="applications-card__digest">
      <div>
        <span>SHA-256</span>
        <code>{digest}</code>
      </div>
      <button type="button" onClick={() => void copyDigest()} aria-label={t("applications.card.copyDigest", { asset: asset.name })}>
        {copied ? <Check size={16} /> : <Copy size={16} />}
        {t(copied ? "applications.card.copied" : "applications.card.copy")}
      </button>
    </div>

    <p className="applications-card__warning"><ShieldAlert size={17} /> <span>{t(spec.warningKey)}</span></p>
  </article>;
}

type ApplePlatform = "ios" | "tvos" | "visionos";

const applicationsLanguages = interfaceLanguages.filter(({ value }) => value === "en" || value === "fr");
const applePlatforms: ReadonlyArray<{
  value: ApplePlatform;
  labelKey: Extract<TranslationKey, `applications.guide.platform.${string}`>;
  bundleExample: string;
}> = [
  { value: "ios", labelKey: "applications.guide.platform.ios", bundleExample: "com.example.rivune" },
  { value: "tvos", labelKey: "applications.guide.platform.tvos", bundleExample: "com.example.rivune.tv" },
  { value: "visionos", labelKey: "applications.guide.platform.visionos", bundleExample: "com.example.rivune.vision" },
];

function AppleSigningGuide({ tagName }: { tagName: string }) {
  const [platform, setPlatform] = useState<ApplePlatform>("ios");
  const [teamID, setTeamID] = useState("");
  const [bundleID, setBundleID] = useState("");
  const [deviceID, setDeviceID] = useState("");
  const [copied, setCopied] = useState(false);
  const selectedPlatform = applePlatforms.find(({ value }) => value === platform)!;
  const valid = /^[A-Z0-9]{10}$/.test(teamID) &&
    bundleID.length <= 255 && /^[A-Za-z0-9][A-Za-z0-9-]*(\.[A-Za-z0-9][A-Za-z0-9-]*)+$/.test(bundleID) &&
    /^[A-Za-z0-9-]{8,80}$/.test(deviceID);
  const checkoutDirectory = `rivune-${tagName}`;
  const command = [
    `git clone --depth 1 --branch '${tagName}' '${repositoryURL}.git' '${checkoutDirectory}'`,
    `cd '${checkoutDirectory}'`,
    "./clients/apple/Scripts/sign-and-install.sh \\",
    `  --platform ${platform} \\`,
    `  --team-id ${teamID || t("applications.guide.teamPlaceholder")} \\`,
    `  --bundle-id ${bundleID || selectedPlatform.bundleExample} \\`,
    `  --device-id ${deviceID || t("applications.guide.devicePlaceholder")}`,
  ].join("\n");

  async function copyCommand() {
    if (!valid) return;
    try {
      await navigator.clipboard.writeText(command);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1800);
    } catch {
      setCopied(false);
    }
  }

  return <section className="applications-apple-guide" id="apple-signing" aria-labelledby="apple-signing-title">
    <div className="applications-apple-guide__intro">
      <span className="applications-kicker">{t("applications.guide.eyebrow")}</span>
      <h2 id="apple-signing-title">{t("applications.guide.title")}</h2>
      <p>{t("applications.guide.body")}</p>
    </div>

    <ol className="applications-apple-guide__steps">
      <li><span><Code2 size={21} /></span><div><strong>{t("applications.guide.step.account.title")}</strong><p>{t("applications.guide.step.account.body")}</p></div></li>
      <li><span><Cable size={21} /></span><div><strong>{t("applications.guide.step.device.title")}</strong><p>{t("applications.guide.step.device.body")}</p></div></li>
      <li><span><Terminal size={21} /></span><div><strong>{t("applications.guide.step.install.title")}</strong><p>{t("applications.guide.step.install.body")}</p></div></li>
    </ol>

    <div className="applications-apple-guide__builder">
      <div className="applications-platform-tabs" role="tablist" aria-label={t("applications.guide.platform")}>
        {applePlatforms.map((option) => <button
          key={option.value}
          type="button"
          role="tab"
          aria-selected={platform === option.value}
          className={platform === option.value ? "is-active" : ""}
          onClick={() => {
            setPlatform(option.value);
            setBundleID(option.bundleExample);
            setCopied(false);
          }}
        >{t(option.labelKey)}</button>)}
      </div>

      <div className="applications-signing-fields">
        <label><span>{t("applications.guide.team")}</span><input value={teamID} placeholder={t("applications.guide.teamPlaceholder")} maxLength={10} autoComplete="off" spellCheck={false} onChange={(event) => { setTeamID(event.target.value.toUpperCase()); setCopied(false); }} /></label>
        <label><span>{t("applications.guide.bundle")}</span><input value={bundleID} placeholder={selectedPlatform.bundleExample} maxLength={255} autoComplete="off" spellCheck={false} onChange={(event) => { setBundleID(event.target.value); setCopied(false); }} /></label>
        <label><span>{t("applications.guide.device")}</span><input value={deviceID} placeholder={t("applications.guide.devicePlaceholder")} maxLength={80} autoComplete="off" spellCheck={false} onChange={(event) => { setDeviceID(event.target.value); setCopied(false); }} /></label>
      </div>

      <div className="applications-command">
        <div><span>{t("applications.guide.command")}</span><button type="button" disabled={!valid} onClick={() => void copyCommand()}><Copy size={15} /> {t(copied ? "applications.guide.copied" : "applications.guide.copy")}</button></div>
        <pre><code>{command}</code></pre>
        {!valid && <p>{t("applications.guide.invalid")}</p>}
      </div>
    </div>
    <aside className="applications-apple-guide__privacy"><KeyRound size={22} /><div><strong>{t("applications.guide.privacyTitle")}</strong><p>{t("applications.guide.privacyBody")}</p></div></aside>
  </section>;
}

async function tvInstallerRecommendation(): Promise<InstallerAssetName> {
  const navigatorWithHints = navigator as Navigator & { userAgentData?: UserAgentData };
  const userAgent = navigator.userAgent.toLowerCase();
  let platform = (navigatorWithHints.userAgentData?.platform ?? navigator.platform).toLowerCase();
  try {
    const values = await navigatorWithHints.userAgentData?.getHighEntropyValues?.(["platform"]);
    platform = values?.platform?.toLowerCase() ?? platform;
  } catch {
    // Client hints are optional; the user-agent fallback remains available.
  }
  if (/mac/.test(platform) || /macintosh/.test(userAgent)) return "Rivune-TV-Installer-macOS.dmg";
  return "Rivune-TV-Installer-Windows.exe";
}

function TVInstallerGuide({ release }: { release: Release }) {
  const [platform, setPlatform] = useState<"webos" | "tizen">("webos");
  const [recommendedName, setRecommendedName] = useState<InstallerAssetName>("Rivune-TV-Installer-Windows.exe");
  useEffect(() => {
    let active = true;
    void tvInstallerRecommendation().then((name) => { if (active) setRecommendedName(name); });
    return () => { active = false; };
  }, []);
  const byName = new Map(release.assets.map((asset) => [asset.name, asset]));
  const installer = byName.get(recommendedName)!;
  const digest = installer.digest.replace("sha256:", "");
  const [copied, setCopied] = useState(false);

  async function copyDigest() {
    try {
      await navigator.clipboard.writeText(digest);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1800);
    } catch {
      setCopied(false);
    }
  }

  return <section className="applications-tv-installer" id="tv-installer" aria-labelledby="tv-installer-title">
    <div className="applications-tv-installer__intro">
      <span className="applications-kicker">{t("applications.tvInstaller.eyebrow")}</span>
      <h2 id="tv-installer-title">{t("applications.tvInstaller.title")}</h2>
      <p>{t("applications.tvInstaller.body")}</p>
      <div className="applications-platform-tabs" role="tablist" aria-label={t("applications.tvInstaller.platform")}>
        <button type="button" role="tab" aria-selected={platform === "webos"} className={platform === "webos" ? "is-active" : ""} onClick={() => setPlatform("webos")}>LG webOS</button>
        <button type="button" role="tab" aria-selected={platform === "tizen"} className={platform === "tizen" ? "is-active" : ""} onClick={() => setPlatform("tizen")}>Samsung Tizen</button>
      </div>
    </div>
    <ol className="applications-tv-installer__steps">
      <li><span><Download size={21} /></span><div><strong>{t("applications.tvInstaller.step.download.title")}</strong><p>{t("applications.tvInstaller.step.download.body")}</p></div></li>
      <li><span><Cable size={21} /></span><div><strong>{t(`applications.tvInstaller.step.${platform}.title`)}</strong><p>{t(`applications.tvInstaller.step.${platform}.body`)}</p></div></li>
      <li><span><ShieldCheck size={21} /></span><div><strong>{t("applications.tvInstaller.step.install.title")}</strong><p>{t("applications.tvInstaller.step.install.body")}</p></div></li>
    </ol>
    <div className="applications-tv-installer__download">
      <div><span>{t("applications.tvInstaller.recommended")}</span><strong>{recommendedName}</strong><small>{formatSize(installer.size)}</small></div>
      <a className="button button--primary" href={installer.browser_download_url}><Download size={17} /> {t("applications.tvInstaller.download")}</a>
      <button type="button" onClick={() => void copyDigest()}><Copy size={15} /> {t(copied ? "applications.card.copied" : "applications.tvInstaller.copyDigest")}</button>
      <code>{digest}</code>
    </div>
    <details className="applications-tv-installer__alternatives">
      <summary>{t("applications.tvInstaller.otherPlatforms")}</summary>
      <div>{installerAssetNames.map((name) => {
        const asset = byName.get(name)!;
        return <a key={name} href={asset.browser_download_url}><span>{name}</span><small>{formatSize(asset.size)}</small></a>;
      })}</div>
    </details>
    <aside className="applications-tv-installer__privacy"><KeyRound size={22} /><div><strong>{t("applications.tvInstaller.privacyTitle")}</strong><p>{t("applications.tvInstaller.privacyBody")}</p></div></aside>
  </section>;
}

export function ApplicationsPage() {
  const [activeLocale, setActiveLocale] = useState<Locale>(locale === "fr" ? "fr" : "en");
  const [release, setRelease] = useState<Release | null>(null);
  const [recommendation, setRecommendation] = useState<DeviceRecommendation>({ labelKey: "applications.device.detecting", assets: [] });
  const [failure, setFailure] = useState(false);
  const [generation, setGeneration] = useState(0);

  useEffect(() => {
    let active = true;
    void deviceRecommendation().then((result) => { if (active) setRecommendation(result); });
    return () => { active = false; };
  }, []);

  useEffect(() => {
    let active = true;
    const controller = new AbortController();
    const timeout = window.setTimeout(() => controller.abort(), 12_000);
    setRelease(null);
    setFailure(false);
    void loadLatestRelease(controller.signal)
      .then((next) => { if (active) setRelease(next); })
      .catch(() => { if (active) setFailure(true); })
      .finally(() => window.clearTimeout(timeout));
    return () => {
      active = false;
      window.clearTimeout(timeout);
      controller.abort();
    };
  }, [generation]);

  const assets = useMemo(() => {
    if (!release) return [];
    const byName = new Map(release.assets.map((asset) => [asset.name, asset]));
    return assetSpecs
      .flatMap((spec, index) => {
        const asset = byName.get(spec.name);
        return asset ? [{ spec, asset, index, recommended: recommendation.assets.includes(spec.name) }] : [];
      })
      .sort((left, right) => Number(right.recommended) - Number(left.recommended) || left.index - right.index);
  }, [recommendation.assets, release]);

  async function changeLocale(nextLocale: Locale) {
    const loadedLocale = await setLocale(nextLocale);
    const publicLocale = loadedLocale === "fr" ? "fr" : "en";
    const url = new URL(window.location.href);
    url.searchParams.set("lang", publicLocale);
    window.history.replaceState(null, "", url);
    document.title = t("applications.meta.title");
    document.querySelector('meta[name="description"]')?.setAttribute("content", t("applications.meta.description"));
    setActiveLocale(publicLocale);
  }

  return <main className="applications-page" data-locale={activeLocale}>
    <div className="applications-page__aura applications-page__aura--one" />
    <div className="applications-page__aura applications-page__aura--two" />
    <header className="applications-header">
      <a href={import.meta.env.BASE_URL} aria-label={t("applications.header.open")}><RivuneMark /></a>
      <div className="applications-header__actions">
        <label className="applications-language">
          <Languages size={18} />
          <span>{t("applications.header.language")}</span>
          <select aria-label={t("applications.header.language")} value={activeLocale} onChange={(event) => void changeLocale(event.target.value as Locale)}>
            {applicationsLanguages.map((language) => <option key={language.value} value={language.value}>{language.label}</option>)}
          </select>
        </label>
        <a className="applications-header__github" href={repositoryURL} target="_blank" rel="noreferrer">
          <Code2 size={18} /> {t("applications.header.source")} <ExternalLink size={14} />
        </a>
      </div>
    </header>

    <section className="applications-hero">
      <span className="applications-kicker">{t("applications.hero.kicker")}</span>
      <h1>{t("applications.hero.title")}</h1>
      <p>{t("applications.hero.body")}</p>
      <div className="applications-detection">
        <span className="applications-detection__pulse" />
        <span>{t("applications.hero.detected")} <strong>{t(recommendation.labelKey)}</strong></span>
      </div>
    </section>

    {!release && !failure && <section className="applications-loading" aria-live="polite">
      <RefreshCw className="spin" size={24} />
      <div><strong>{t("applications.release.loadingTitle")}</strong><span>{t("applications.release.loadingBody")}</span></div>
    </section>}

    {failure && <section className="applications-failure" role="alert">
      <ShieldAlert size={24} />
      <div><strong>{t("applications.release.failureTitle")}</strong><span>{t("applications.release.failureBody")}</span></div>
      <Button variant="secondary" onClick={() => setGeneration((current) => current + 1)}><RefreshCw size={17} /> {t("applications.release.retry")}</Button>
    </section>}

    {release && <>
      <section className="applications-release-bar" aria-label={t("applications.release.latest")}>
        <div><span>{t("applications.release.latest")}</span><strong>{release.tag_name}</strong></div>
        <div><span>{t("applications.release.published")}</span><strong>{new Intl.DateTimeFormat(activeLocale, { dateStyle: "medium" }).format(new Date(release.published_at))}</strong></div>
        <a href={release.html_url} target="_blank" rel="noreferrer">{t("applications.release.notes")} <ExternalLink size={14} /></a>
      </section>

      <section className="applications-grid" aria-label={t("applications.downloads.label")}>
        {assets.map(({ asset, spec, recommended }) => <ReleaseCard key={spec.name} asset={asset} spec={spec} recommended={recommended} signingGuideAvailable={supportsLocalSigningGuide(release.tag_name)} />)}
      </section>

      {supportsTVInstaller(release.tag_name) && <TVInstallerGuide release={release} />}
      {supportsLocalSigningGuide(release.tag_name) && <AppleSigningGuide tagName={release.tag_name} />}
    </>}

    <section className="applications-verify">
      <ShieldCheck size={24} />
      <div>
        <h2>{t("applications.verify.title")}</h2>
        <p>{t("applications.verify.body")}</p>
      </div>
      <div className="applications-verify__commands"><code>shasum -a 256 &lt;downloaded-file&gt;</code><code>Get-FileHash &lt;downloaded-file&gt; -Algorithm SHA256</code></div>
    </section>

    <footer className="applications-footer">
      <span>{t("applications.footer.tagline")}</span>
      <a href={`${repositoryURL}/blob/main/LICENSE`} target="_blank" rel="noreferrer">{t("applications.footer.license")} <ExternalLink size={13} /></a>
    </footer>
  </main>;
}
