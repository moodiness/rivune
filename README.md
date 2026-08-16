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
./rivune setup --public-url https://media.example.com --version 1.7.1
./rivune up
./rivune doctor
```

Omit `--public-url` for a loopback-only installation. `--version` is required and accepts only an exact stable numeric release such as `1.7.1`; mutable image tags such as `latest` are rejected so a fresh install is reproducible. `./rivune help` lists explicit wrappers for lifecycle, logs, diagnostics, authenticated backup verification, and restore. The command always resolves the repository Compose file and never prints generated secrets.

On Windows PowerShell, run `.\scripts\create-env.ps1`, fill the generated private `.env`, then use `docker compose pull` and `docker compose up -d`. The lower-level `./scripts/create-env.sh` path remains available on Unix hosts that need to customize `.env` before startup.

`RIVUNE_ENCRYPTION_KEYS` uses active-first `version:64-lowercase-hex` pairs with unique positive versions and unique, non-zero keys. Back up the generated keyring separately and securely. A database backup cannot recover encrypted integration credentials or profile tracking tokens without every matching key version.

Open [http://localhost:8080](http://localhost:8080) for a loopback deployment. For a normal HTTPS installation, keep using `compose.yaml`, set `RIVUNE_PUBLIC_URL` to the public HTTPS origin, and put Rivune behind Pangolin/Newt or an operator-managed reverse proxy. The proxy must terminate TLS and target Rivune over HTTP on port `8080`; then open `RIVUNE_PUBLIC_URL`. Enter `RIVUNE_SETUP_TOKEN` to claim the instance and create the first administrator.

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

The base Compose file is a complete PostgreSQL 18 stack for Pangolin/Newt or another operator-managed proxy. It creates a database-only internal network and the dedicated `rivune-edge` network. Attach the proxy only to `rivune-edge` and target hostname `rivune`, port `8080`, over HTTP. For Pangolin, configure Newt on that network with those target values. The loopback-only host port is for local access and is not used by a container proxy.

For Unraid's XML template with an existing PostgreSQL server, TLS, a host port, and AMD/Intel GPU access are all opt-in. Prefer the same dedicated edge network and `Rivune:8080` target. If Newt cannot share that network, manually add an Unraid TCP Port mapping from container port `8080` to an unused host port such as `18080`; never forward it on the router.

See [Production operations](docs/operations.md) for the complete Pangolin network configuration, optional PostgreSQL TLS, upgrades from legacy environment configuration, encryption-key rotation, GPU activation, and authenticated database backup, restore, and rollback.

## Development

Backend requirements are Go 1.26.6 or newer in the 1.26 line and PostgreSQL 18. Frontend requirements are Node.js 22 and npm. Typed clients live under [`clients/`](clients/); the public contract is [`protocol/openapi.yaml`](protocol/openapi.yaml).

The Android project includes the native Rivune application for phones, tablets, and Android TV plus the reusable `rivune-api` SDK. It supports server discovery, restored sessions, passwordless device pairing, category-scoped profiles and PINs, and paginated collection browsing.

### Android app

Official Android releases provide a universal APK for phones, tablets, and Android TV. Download `rivune-android-<version>.apk` and its `rivune-android-<version>-corresponding-source.tar.gz` from the matching [GitHub Release](https://github.com/moodiness/rivune/releases), verify both with the published `SHA256SUMS`, and complete Android's normal package-installation prompt. The public application ID is `io.rivune.app` and Android 8.0 or newer is required.

The installed app checks the dedicated `rivune-android-update.json` release asset at most once every 24 hours and also offers a manual check in Settings. It never contains a GitHub token. An update is downloaded only after consent, then its size, SHA-256, package identity, version code, and signing certificate are verified before Android shows its own installation confirmation. Silent installation is not supported; if Android blocks installs from this source, grant that system permission and return to Rivune to continue.

Android Settings keeps device-specific startup, preferred-player, motion, language, accent, frame-rate matching, picture-format, and Wi-Fi/Ethernet versus mobile-network quality choices local to the device. Preferred-player choices include asking every time, Rivune automatic (AndroidX Media3 first with an embedded mpv fallback for unsupported media), explicit Media3, explicit mpv, and detected external players. Profile controls display the effective Rivune server value and its provenance, support clearing a profile override to inherit the server policy, and cover resolution, direct play, automatic next episode, audio, subtitles, forced subtitles, and metadata language. The effective transcoding permission remains visible but read-only because only a global administrator may change that server policy. Internal episode playback exposes a one-shot Next action and starts the next episode after a natural end when the effective profile setting allows it; an external player continues only after returning an explicit completed result. About shows the connected server/build details and can copy or export a bounded in-memory diagnostic report through Android's document picker; the report excludes credentials, profile/media data, URL paths, queries, and raw exception text.

```sh
cd web && npm ci && npm run build
cd ../server && go test ./...
cd ../clients/android && ./gradlew :rivune-api:testDebugUnitTest :app:testDebugUnitTest :app:assembleDebug :app:assembleRelease
```

## License

The repository's server, web, Apple, Windows, API, documentation, and other
separately distributed Rivune components are licensed under the
[Apache License 2.0](LICENSE). General third-party notices are in
[`NOTICE`](NOTICE).

The Android application binary includes a GPLv3 native playback stack and is
distributed under different combined-work terms. See
[`clients/android/app/src/main/assets/legal/LICENSE.txt`](clients/android/app/src/main/assets/legal/LICENSE.txt) and
[`clients/android/app/src/main/assets/legal/THIRD_PARTY_NOTICES.txt`](clients/android/app/src/main/assets/legal/THIRD_PARTY_NOTICES.txt)
for the exact terms, Corresponding Source directions, and attributions.
