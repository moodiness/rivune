<p align="center">
  <img src="assets/rivune-mark.svg" alt="Rivune" width="120" height="120">
</p>

<h1 align="center">Rivune</h1>

<p align="center"><strong>Your self-hosted media universe, on every screen.</strong></p>

Rivune is an open-source media backend and responsive web app. It ships no catalogue or hosted account: authentication, profiles, playback state, provider credentials, source URLs, and private headers stay on the operator's server.

## Quick start

Requirements: Docker Engine with Compose v2, Bash, and OpenSSL. macOS also needs Homebrew Bash and GNU coreutils.

```sh
git clone https://github.com/moodiness/rivune.git
cd rivune
./rivune setup --public-url https://media.example.com --version 1.12.2
./rivune up
./rivune doctor
```

Omit `--public-url` for loopback-only use, then open [http://localhost:8080](http://localhost:8080). A public installation must use HTTPS through Pangolin/Newt or another reverse proxy targeting `rivune:8080` on the dedicated `rivune-edge` network. Never expose raw port 8080 publicly.

On Windows, create `.env` with `./scripts/create-env.ps1`, then use `./rivune.ps1 up|status|logs|down`. Run `./rivune help` for lifecycle, backup, restore, and diagnostics commands. Keep `.env`, backup signing material, and every version of `RIVUNE_ENCRYPTION_KEYS` private and backed up separately.

## Applications

The [applications page](https://moodiness.github.io/rivune/) provides current downloads and SHA-256 digests.

| Platform | Targets | Artifact |
| --- | --- | --- |
| Android | Phone, tablet, Android TV | Signed APK |
| Apple | iPhone, iPad, Apple TV, Vision Pro, macOS | Unsigned IPA/DMG |
| Windows | x64, ARM64 | Unsigned portable executable |
| TV | LG webOS, Samsung Tizen | Unsigned IPK/WGT |

Apple device archives require local signing. Windows and macOS may show trust warnings. Verify the release URL and digest before running unsigned software.

## Development

Requirements: Go 1.26.6, PostgreSQL 18, Node.js 22, and the SDK for each native target. The HTTP contract is [`protocol/openapi.yaml`](protocol/openapi.yaml).

```sh
(cd server && go test ./...)
(cd web && npm ci && npm run build)
(cd clients/update && go test ./...)
swift test --package-path clients/apple
(cd clients/android && ./gradlew :rivune-api:testDebugUnitTest :app:testDebugUnitTest)
(cd clients/windows && dotnet test Rivune.Windows.slnx --configuration Release --nologo)
(cd clients/tv && npm ci && npm run typecheck && npm test)
(cd clients/tv-installer && go test ./...)
```

## Documentation

- [Operations](docs/operations.md): HTTPS, discovery, upgrades, GPU, backup, and restore.
- [Release process](docs/releasing.md): versioning, signing, publication, and retries.
- [Design system](docs/design-system.md).
- [Jellyfin compatibility evidence](docs/jellyfin-compatibility.md).
- [Protocol compatibility](protocol/COMPATIBILITY.md).
- [Security reporting](SECURITY.md).

## License

Most Rivune code and documentation use the [Apache License 2.0](LICENSE); see [`NOTICE`](NOTICE) for third-party attributions. The Android binary includes a GPLv3 playback stack; its exact combined-work terms are bundled in the application.
