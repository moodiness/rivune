import { useEffect, useMemo, useState } from "react";
import {
  Check,
  Code2,
  Copy,
  Download,
  ExternalLink,
  Monitor,
  QrCode,
  RefreshCw,
  ShieldAlert,
  ShieldCheck,
  Smartphone,
  Tv,
} from "lucide-react";
import { QRCodeSVG } from "qrcode.react";
import { Button, RivuneMark } from "../components";

const releaseEndpoint = "https://api.github.com/repos/moodiness/rivune/releases/latest";
const releaseAssetPrefix = "https://github.com/moodiness/rivune/releases/download/";
const sha256Pattern = /^sha256:([0-9a-f]{64})$/;
const localSigningGuide = "https://github.com/moodiness/rivune#direct-application-downloads";
const semverTagPattern = /^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/;
const maximumReleaseResponseBytes = 512 * 1024;

type AssetName =
  | "Rivune-Android.apk"
  | "Rivune-iOS-unsigned.ipa"
  | "Rivune-tvOS-unsigned.ipa"
  | "Rivune-visionOS-unsigned.ipa"
  | "Rivune-macOS.dmg"
  | "Rivune-x64.exe"
  | "Rivune-arm64.exe";

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
  platform: "Android" | "iPhone & iPad" | "Apple TV" | "Apple Vision Pro" | "macOS" | "Windows";
  detail: string;
  signed: boolean;
  warning: string;
  icon: typeof Smartphone;
};

type DeviceRecommendation = {
  label: string;
  assets: ReadonlySet<AssetName>;
};

const assetSpecs: AssetSpec[] = [
  {
    name: "Rivune-Android.apk",
    platform: "Android",
    detail: "Phone · tablet · Android TV · universal APK",
    signed: true,
    warning: "Android may ask you to allow app installs from this browser. Keep that permission temporary and install only the verified Rivune APK.",
    icon: Smartphone,
  },
  {
    name: "Rivune-iOS-unsigned.ipa",
    platform: "iPhone & iPad",
    detail: "iOS 15 or later · arm64",
    signed: false,
    warning: "This archive is unsigned and cannot be installed as downloaded. Sign it locally with Xcode and your own Apple Developer team.",
    icon: Smartphone,
  },
  {
    name: "Rivune-tvOS-unsigned.ipa",
    platform: "Apple TV",
    detail: "tvOS 15 or later · arm64",
    signed: false,
    warning: "This archive is unsigned. Download it on a Mac, sign it locally with Xcode, then install it on your Apple TV.",
    icon: Tv,
  },
  {
    name: "Rivune-visionOS-unsigned.ipa",
    platform: "Apple Vision Pro",
    detail: "visionOS 1 or later · arm64",
    signed: false,
    warning: "This archive is unsigned. Download it on a Mac and use your own Apple Developer team to sign and install it with Xcode.",
    icon: Monitor,
  },
  {
    name: "Rivune-macOS.dmg",
    platform: "macOS",
    detail: "macOS 12 or later · Apple silicon + Intel",
    signed: false,
    warning: "The app inside this disk image is unsigned. macOS Gatekeeper will warn before opening it; verify the SHA-256 first.",
    icon: Monitor,
  },
  {
    name: "Rivune-x64.exe",
    platform: "Windows",
    detail: "Windows 10 2004 or later · x64",
    signed: false,
    warning: "This portable executable is unsigned. Microsoft SmartScreen may warn; verify the SHA-256 before running it.",
    icon: Monitor,
  },
  {
    name: "Rivune-arm64.exe",
    platform: "Windows",
    detail: "Windows 10 2004 or later · ARM64",
    signed: false,
    warning: "This portable executable is unsigned. Microsoft SmartScreen may warn; verify the SHA-256 before running it.",
    icon: Monitor,
  },
];

const expectedAssetNames = new Set<AssetName>(assetSpecs.map((asset) => asset.name));
const releaseManifestName = "rivune-update.json";
const expectedReleaseAssetNames = new Set<string>([...expectedAssetNames, releaseManifestName]);

type UserAgentData = {
  platform?: string;
  getHighEntropyValues?: (hints: string[]) => Promise<{ architecture?: string; bitness?: string; platform?: string }>;
};

async function deviceRecommendation(): Promise<DeviceRecommendation> {
  const navigatorWithHints = navigator as Navigator & { userAgentData?: UserAgentData };
  const userAgent = navigator.userAgent;
  let platform = navigatorWithHints.userAgentData?.platform ?? navigator.platform ?? "";
  let architecture = "";
  let bitness = "";
  try {
    const values = await navigatorWithHints.userAgentData?.getHighEntropyValues?.(["architecture", "bitness", "platform"]);
    platform = values?.platform ?? platform;
    architecture = values?.architecture?.toLowerCase() ?? "";
    bitness = values?.bitness ?? "";
  } catch {
    // Client hints are optional; the user-agent fallback remains available.
  }

  if (/android/i.test(userAgent) || /android/i.test(platform)) {
    return { label: "Android device", assets: new Set(["Rivune-Android.apk"]) };
  }
  if (/appletv/i.test(userAgent)) {
    return { label: "Apple TV", assets: new Set(["Rivune-tvOS-unsigned.ipa"]) };
  }
  if (/vision/i.test(userAgent) || /xr/i.test(platform)) {
    return { label: "Apple Vision Pro", assets: new Set(["Rivune-visionOS-unsigned.ipa"]) };
  }
  if (/iphone|ipad|ipod/i.test(userAgent) || (/mac/i.test(platform) && navigator.maxTouchPoints > 1)) {
    return { label: "iPhone or iPad", assets: new Set(["Rivune-iOS-unsigned.ipa"]) };
  }
  if (/mac/i.test(platform) || /macintosh/i.test(userAgent)) {
    return { label: "Mac", assets: new Set(["Rivune-macOS.dmg"]) };
  }
  if (/win/i.test(platform) || /windows/i.test(userAgent)) {
    if (architecture.includes("arm") || /arm64/i.test(userAgent)) {
      return { label: "Windows on ARM64", assets: new Set(["Rivune-arm64.exe"]) };
    }
    if (architecture === "x86" && bitness === "64" || /win64|x64|amd64/i.test(userAgent)) {
      return { label: "Windows x64", assets: new Set(["Rivune-x64.exe"]) };
    }
    return { label: "Windows (architecture unavailable)", assets: new Set(["Rivune-x64.exe", "Rivune-arm64.exe"]) };
  }
  return { label: "Platform not identified", assets: new Set() };
}

function validRelease(value: unknown): value is Release {
  if (typeof value !== "object" || value === null) return false;
  const release = value as Partial<Release>;
  if (
    typeof release.tag_name !== "string" || !semverTagPattern.test(release.tag_name) ||
    release.name !== release.tag_name || release.draft !== false || release.prerelease !== false ||
    typeof release.published_at !== "string" || Number.isNaN(Date.parse(release.published_at)) ||
    release.html_url !== `https://github.com/moodiness/rivune/releases/tag/${release.tag_name}` ||
    !Array.isArray(release.assets) || release.assets.length !== expectedReleaseAssetNames.size
  ) return false;

  const assets = new Map<string, ReleaseAsset>();
  for (const candidate of release.assets) {
    if (typeof candidate !== "object" || candidate === null) return false;
    const asset = candidate as Partial<ReleaseAsset>;
    if (typeof asset.name !== "string" || !expectedReleaseAssetNames.has(asset.name) || assets.has(asset.name)) return false;
    if (
      asset.state !== "uploaded" ||
      typeof asset.size !== "number" || !Number.isSafeInteger(asset.size) || asset.size <= 0 ||
      typeof asset.digest !== "string" || !sha256Pattern.test(asset.digest) ||
      asset.browser_download_url !== `${releaseAssetPrefix}${release.tag_name}/${asset.name}`
    ) return false;
    assets.set(asset.name, asset as ReleaseAsset);
  }
  return assets.size === expectedReleaseAssetNames.size;
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

function ReleaseCard({ asset, spec, recommended }: { asset: ReleaseAsset; spec: AssetSpec; recommended: boolean }) {
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
          {recommended && <span className="applications-badge applications-badge--recommended">Recommended</span>}
        </div>
        <p>{spec.detail}</p>
      </div>
    </div>

    <div className="applications-card__facts" aria-label={`${spec.platform} release details`}>
      <span>{formatSize(asset.size)}</span>
      <span className={spec.signed ? "is-signed" : "is-unsigned"}>
        {spec.signed ? <ShieldCheck size={15} /> : <ShieldAlert size={15} />}
        {spec.signed ? "Signed" : "Unsigned"}
      </span>
    </div>

    <div className="applications-card__actions">
      <a className="button button--primary" href={asset.browser_download_url}>
        <Download size={17} /> Download
      </a>
      <Button variant="secondary" type="button" onClick={() => setShowQR((current) => !current)} aria-expanded={showQR}>
        <QrCode size={17} /> {showQR ? "Hide QR" : "Show QR"}
      </Button>
    </div>

    {showQR && <div className="applications-card__qr">
      <div className="applications-card__qr-code">
        <QRCodeSVG value={asset.browser_download_url} size={156} level="M" marginSize={2} title={`Download ${asset.name}`} />
      </div>
      <p>Scan to open this exact GitHub release asset on another device.</p>
    </div>}
    {!spec.signed && spec.name.endsWith(".ipa") && <a className="applications-card__signing-guide" href={localSigningGuide} target="_blank" rel="noreferrer">Sign and install locally <ExternalLink size={13} /></a>}

    <div className="applications-card__digest">
      <div>
        <span>SHA-256</span>
        <code>{digest}</code>
      </div>
      <button type="button" onClick={() => void copyDigest()} aria-label={`Copy SHA-256 for ${asset.name}`}>
        {copied ? <Check size={16} /> : <Copy size={16} />}
        {copied ? "Copied" : "Copy"}
      </button>
    </div>

    <p className="applications-card__warning"><ShieldAlert size={17} /> <span>{spec.warning}</span></p>
  </article>;
}

export function ApplicationsPage() {
  const [release, setRelease] = useState<Release | null>(null);
  const [recommendation, setRecommendation] = useState<DeviceRecommendation>({ label: "Detecting this device…", assets: new Set() });
  const [failure, setFailure] = useState<string | null>(null);
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
    setFailure(null);
    void loadLatestRelease(controller.signal)
      .then((next) => { if (active) setRelease(next); })
      .catch((error: unknown) => {
        if (!active) return;
        if (!controller.signal.aborted) setFailure(error instanceof Error ? error.message : "Release metadata is unavailable");
        else setFailure("GitHub did not respond in time");
      })
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
      .map((spec, index) => ({ spec, asset: byName.get(spec.name)!, index, recommended: recommendation.assets.has(spec.name) }))
      .sort((left, right) => Number(right.recommended) - Number(left.recommended) || left.index - right.index);
  }, [recommendation.assets, release]);

  return <main className="applications-page">
    <div className="applications-page__aura applications-page__aura--one" />
    <div className="applications-page__aura applications-page__aura--two" />
    <header className="applications-header">
      <a href={import.meta.env.BASE_URL} aria-label="Open Rivune"><RivuneMark /></a>
      <a className="applications-header__github" href="https://github.com/moodiness/rivune" target="_blank" rel="noreferrer">
        <Code2 size={18} /> Source code <ExternalLink size={14} />
      </a>
    </header>

    <section className="applications-hero">
      <span className="applications-kicker">Official direct downloads</span>
      <h1>Rivune on every screen.</h1>
      <p>Download the latest stable native app directly from its immutable GitHub release. Every file includes its exact size and SHA-256 fingerprint.</p>
      <div className="applications-detection">
        <span className="applications-detection__pulse" />
        <span>Detected: <strong>{recommendation.label}</strong></span>
      </div>
    </section>

    {!release && !failure && <section className="applications-loading" aria-live="polite">
      <RefreshCw className="spin" size={24} />
      <div><strong>Loading the latest stable release…</strong><span>Validating GitHub metadata and fingerprints.</span></div>
    </section>}

    {failure && <section className="applications-failure" role="alert">
      <ShieldAlert size={24} />
      <div><strong>Downloads are temporarily unavailable.</strong><span>{failure}</span></div>
      <Button variant="secondary" onClick={() => setGeneration((current) => current + 1)}><RefreshCw size={17} /> Try again</Button>
    </section>}

    {release && <>
      <section className="applications-release-bar" aria-label="Latest stable release">
        <div><span>Latest stable release</span><strong>{release.tag_name}</strong></div>
        <div><span>Published</span><strong>{new Intl.DateTimeFormat(undefined, { dateStyle: "medium" }).format(new Date(release.published_at))}</strong></div>
        <a href={release.html_url} target="_blank" rel="noreferrer">Release notes <ExternalLink size={14} /></a>
      </section>

      <section className="applications-grid" aria-label="Rivune application downloads">
        {assets.map(({ asset, spec, recommended }) => <ReleaseCard key={spec.name} asset={asset} spec={spec} recommended={recommended} />)}
      </section>
    </>}

    <section className="applications-verify">
      <ShieldCheck size={24} />
      <div>
        <h2>Verify before you install</h2>
        <p>Compare the downloaded file with the full SHA-256 shown above. GitHub publishes the same digest in the release asset metadata.</p>
      </div>
      <div className="applications-verify__commands"><code>shasum -a 256 &lt;downloaded-file&gt;</code><code>Get-FileHash &lt;downloaded-file&gt; -Algorithm SHA256</code></div>
    </section>

    <footer className="applications-footer">
      <span>Open source. Self-hosted. No public catalogue.</span>
      <a href="https://github.com/moodiness/rivune/blob/main/LICENSE" target="_blank" rel="noreferrer">Apache 2.0 license <ExternalLink size={13} /></a>
    </footer>
  </main>;
}
