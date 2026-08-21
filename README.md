<p align="center">
  <img src="templates/unraid/rivune-icon.svg" alt="Rivune" width="120" height="120">
</p>

<h1 align="center">Rivune</h1>

<p align="center"><strong>Your self-hosted media universe, on every screen.</strong></p>

Rivune is an open-source media backend and responsive web app with no predefined catalogue or hosted account. It keeps authentication, profiles, collections, playback state, provider credentials, source URLs, and private request headers on the server selected by the operator.

## Install with Docker Compose

Requirements: Docker Engine with Compose v2, Bash, and OpenSSL. On a Linux host, use the root operator command; it creates a mode-0600 `.env` with five independent secrets and refuses to overwrite an existing path:

```sh
git clone https://github.com/moodiness/rivune.git
cd rivune
./rivune setup --public-url https://media.example.com --version 1.9.1
./rivune up
./rivune doctor
```

Omit `--public-url` for a loopback-only installation. `--version` is required and accepts only an exact stable numeric release such as `1.9.1`; mutable image tags such as `latest` are rejected so a fresh install is reproducible. `./rivune help` lists explicit wrappers for lifecycle, logs, diagnostics, authenticated backup verification, and restore. The command always resolves the repository Compose file and never prints generated secrets.

On Windows PowerShell, run `.\scripts\create-env.ps1`, fill the generated private `.env`, then use `docker compose pull` and `docker compose up -d`. The lower-level `./scripts/create-env.sh` path remains available on Unix hosts that need to customize `.env` before startup.

`RIVUNE_ENCRYPTION_KEYS` uses active-first `version:64-lowercase-hex` pairs with unique positive versions and unique, non-zero keys. Back up the generated keyring separately and securely. A database backup cannot recover encrypted integration credentials or profile tracking tokens without every matching key version.

Open [http://localhost:8080](http://localhost:8080) for a loopback deployment. For a normal HTTPS installation, keep using `compose.yaml`, set `RIVUNE_PUBLIC_URL` to the public HTTPS origin, and put Rivune behind Pangolin/Newt or an operator-managed reverse proxy. The proxy must terminate TLS and target Rivune over HTTP on port `8080`; then open `RIVUNE_PUBLIC_URL`. Enter `RIVUNE_SETUP_TOKEN` to claim the instance and create the first administrator.

On a physical phone, tablet, or TV, `localhost` means that device, not the machine running Rivune. For trusted-LAN HTTP access without a reverse proxy, set `RIVUNE_BIND_ADDRESS=0.0.0.0` and `RIVUNE_PUBLIC_URL=http://<server-private-IP>:8080`, restart the stack, then enter that same private-IP URL in the app. Rivune accepts cleartext origins only for loopback and literal private-network addresses; never expose this direct port to the public Internet because credentials and sessions are not encrypted.

Global administrators can export and atomically merge a versioned profile archive through the documented API. It includes profile settings, explicitly assigned add-ons and collections, stable title identities, library/progress/favorite/user-data state, and tracking preferences, but never passwords, PINs, sessions, provider credentials, or assignment policy. Add-on transport URLs are intentionally portable and can contain tokens: store the downloaded JSON with credential-file permissions.

## First-run configuration

A new deployment needs no provider or media-tuning environment variables. In **Administration → Settings**:

1. choose the timezone, Jellyfin compatibility, transcoding policy, storage quotas, bitrate ceiling, and hardware-acceleration mode;
2. add only the provider integrations you use;
3. verify the requested and active settings shown by the application.

Integration responses expose configured status and update time only; Rivune never returns provider secret values. Provider changes are applied live. Hardware acceleration is restart-required: saving it persists the requested value, but the previous active value remains in force and `pending restart` remains visible until the service restarts and reconciles the request. Do not treat the requested mode as active before then.

## Host deployment choices

The provided Compose manifest is CPU-only by default and needs no GPU device. For optional AMD/Intel acceleration, set `RIVUNE_VIDEO_DEVICE` and `RIVUNE_VIDEO_GROUP_ID` in `.env`, then add the single supported overlay:

```sh
docker compose -f compose.yaml -f compose.amd-intel.yaml up -d
```

The base Compose file is a complete PostgreSQL 18 stack for Pangolin/Newt or another operator-managed proxy. It creates a database-only internal network and the dedicated `rivune-edge` network. Attach the proxy only to `rivune-edge` and target hostname `rivune`, port `8080`, over HTTP. For Pangolin, configure Newt on that network with those target values. The host port binds to loopback by default; `RIVUNE_BIND_ADDRESS=0.0.0.0` is the explicit trusted-LAN-only exception described above and is not used by a container proxy.

For Unraid's XML template with an existing PostgreSQL server, TLS, a host port, and AMD/Intel GPU access are all opt-in. Prefer the same dedicated edge network and `Rivune:8080` target. If Newt cannot share that network, manually add an Unraid TCP Port mapping from container port `8080` to an unused host port such as `18080`; never forward it on the router.

See [Production operations](docs/operations.md) for the complete Pangolin network configuration, optional PostgreSQL TLS, upgrades from legacy environment configuration, encryption-key rotation, GPU activation, and authenticated database backup, restore, and rollback.

## Development

Backend requirements are Go 1.26.6 or newer in the 1.26 line and PostgreSQL 18. Frontend requirements are Node.js 22 and npm. Typed clients live under [`clients/`](clients/); the public contract is [`protocol/openapi.yaml`](protocol/openapi.yaml).

The Android project includes the native Rivune application for phones, tablets, and Android TV plus the reusable `rivune-api` SDK. It supports HTTPS or trusted-local HTTP discovery, restored sessions, passwordless device pairing, category-scoped profiles and PINs, and paginated collection browsing.

The Apple project provides native SwiftUI applications for iPhone, iPad, Apple TV, Apple Vision Pro, and macOS plus the reusable `RivuneAPI` SDK. It supports HTTPS or trusted-local HTTP discovery, Keychain-protected issuer-scoped sessions, passwordless device pairing, profile selection and PINs, collection browsing, and adaptive remote, touch, gaze, and pointer layouts. Open `clients/apple/Rivune.xcodeproj`; regenerate it from `clients/apple/project.yml` with XcodeGen after changing target configuration.

The Windows project includes a native responsive WinUI 3 application plus the reusable `Rivune.Windows` protocol-v20 client. It supports HTTPS or trusted-local HTTP discovery, passwordless device pairing, DPAPI-protected issuer-scoped sessions, profile avatars and PINs, Home/Search/Library/Calendar browsing, collection and title details, profile settings with provenance, source filtering, and same-origin guarded native HTTP/HLS playback. The player includes resume/completion synchronization, track selection, chapter markers, configurable intro/recap/outro skipping, next-episode playback, retry recovery, keyboard/gamepad navigation, and compact, desktop, TV, reduced-motion, and high-contrast layouts. Official Windows releases provide self-contained `Rivune-x64.exe` and `Rivune-arm64.exe` executables through GitHub Releases.

The Windows executables are portable, but local state is not stored beside them. The last server address and device-only preferences are kept under `%LOCALAPPDATA%\Rivune\` and may be backed up while Rivune is closed. Session files under `%LOCALAPPDATA%\Rivune\credentials\` are encrypted with Windows DPAPI for the current Windows user; they are not a portable backup and normally cannot be restored under another account or Windows installation. After a migration, pair or sign in again. Profiles, library, progress, and other account data remain on the self-hosted Rivune server. The Windows client has no installer or Store package. Official Windows executables are unsigned. Automatic replacement accepts only the exact GitHub asset URL recorded by the release manifest and verifies the manifest `ProductVersion`, size, and SHA-256 before starting and again after staging; it cannot authenticate a publisher independently of GitHub. SmartScreen may warn. Keep the executable in a writable local folder because automatic replacement cannot update a read-only location such as a protected `Program Files` directory.

### Android app

Official Android releases provide a universal APK for phones, tablets, and Android TV. Beginning with v1.8.3, releases use the stable filename `Rivune-Android.apk`; the already-published v1.8.2 release retains `rivune-android-1.8.2.apk`. Download the APK from the matching [GitHub Release](https://github.com/moodiness/rivune/releases), compare its SHA-256 with the digest GitHub publishes for that asset, and complete Android's normal package-installation prompt. The public application ID is `io.rivune.app` and Android 8.0 or newer is required.

Beginning with the first direct Apple release, each matching GitHub Release contains exactly eight assets: the stable Android `Rivune-Android.apk`, unsigned device archives `Rivune-iOS-unsigned.ipa`, `Rivune-tvOS-unsigned.ipa`, and `Rivune-visionOS-unsigned.ipa`, the unsigned universal macOS disk image `Rivune-macOS.dmg`, the sole schema-v2 `rivune-update.json` feed, and x64/ARM64 Windows executables. The unsigned IPA files contain no Apple account, certificate, provisioning profile, or valid code signature; stock iOS, tvOS, and visionOS will not install them as downloaded. A recipient must sign the app with a provisioning identity authorized for that device, typically by rebuilding this open-source project with Xcode or by using lawful personal sideloading tooling. The unsigned macOS app likewise triggers Gatekeeper unless the recipient explicitly permits it or builds it locally. The installed Android and Windows apps check the update feed at most once every 24 hours and also offer a manual check in Settings. Neither client contains a GitHub token. An Android update is downloaded only after consent, then its size, SHA-256, package identity, version code, and signing certificate are verified before Android's package installer is opened.

Android Settings keeps device-specific startup, preferred-player, motion, language, accent, frame-rate matching, picture-format, and Wi-Fi/Ethernet versus mobile-network quality choices local to the device. Preferred-player choices include asking every time, Rivune automatic (AndroidX Media3 first with an embedded mpv fallback for unsupported media), explicit Media3, explicit mpv, and detected external players. Profile controls display the effective Rivune server value and its provenance, support clearing a profile override to inherit the server policy, and cover resolution, direct play, automatic next episode, audio, subtitles, forced subtitles, and metadata language. The effective transcoding permission remains visible but read-only because only a global administrator may change that server policy. Internal episode playback exposes a one-shot Next action and starts the next episode after a natural end when the effective profile setting allows it; an external player continues only after returning an explicit completed result. About shows the connected server/build details and can copy or export a bounded in-memory diagnostic report through Android's document picker; the report excludes credentials, profile/media data, URL paths, queries, and raw exception text.

```sh
cd web && npm ci && npm run build
cd ../server && go test ./...
cd ../clients/update && go test ./...
cd ../apple && swift test
cd ../android && ./gradlew :rivune-api:testDebugUnitTest :app:testDebugUnitTest :app:assembleDebug :app:assembleRelease
cd ../windows && dotnet test Rivune.Windows.slnx --configuration Release --nologo
```

## Security

Report vulnerabilities through the private process in [`SECURITY.md`](SECURITY.md). Do not publish exploit details, credentials, private URLs, or user data in a public issue.

## License

The repository's server, web, Apple, Windows, API, documentation, and other
separately distributed Rivune components are licensed under the
[Apache License 2.0](LICENSE). General third-party notices are in
[`NOTICE`](NOTICE).

The Android application binary includes a GPLv3 native playback stack and is
distributed under different combined-work terms. See
[`clients/android/app/src/main/assets/legal/LICENSE.txt`](clients/android/app/src/main/assets/legal/LICENSE.txt) and
[`clients/android/app/src/main/assets/legal/THIRD_PARTY_NOTICES.txt`](clients/android/app/src/main/assets/legal/THIRD_PARTY_NOTICES.txt)
for the exact terms and attributions.
