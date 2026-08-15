# Rivune v1.7.0

## Highlights

- The native Android experience has been redesigned for phones, tablets, and Android TV with responsive navigation, centered Search and Library layouts, cleaner collection artwork, polished detail actions, and improved D-pad focus behavior.
- Android Settings now separates app and profile choices for startup destination, preferred player, motion, language, accent color, frame-rate matching, picture format, per-network quality, resolution, audio, subtitles, transcoding, and metadata language.
- Profile metadata language now reaches collections, search, calendars, movie and series details, seasons, and trailers; changing it refreshes the active surface without reconnecting.
- Native playback adds refined touch controls, orientation handling, picture-format controls, Android 12+ frame-rate matching, network-aware quality selection, and persistent internal or external player preferences.
- A bounded, privacy-conscious diagnostic report can be copied or exported from Android Settings without credentials, media history, URL paths, query strings, or raw exception text.
- Rivune now uses one shared mark across Android launchers and TV banners, the web interface, browser favicon, and Unraid assets.

## Android installation

- Download `rivune-android-1.7.0.apk` from this release and complete Android's package-installation prompt. Android 8.0 or newer is required.
- The public application ID is `io.rivune.app`.
- `rivune-android-update.json` is the stable, Android-specific update feed. `SHA256SUMS` covers exactly the APK and that manifest.
- Rivune never performs a silent install and contains no GitHub token. Android may require you to allow installs from the app that opens the APK before returning to Rivune.

## Upgrade notes

- Server operators can pull and recreate Rivune normally. This release adds no database migration and requires no new server environment variable.
- Android app preferences are device-local; profile playback and metadata preferences continue to be stored by the connected Rivune server.
- Existing web and container deployments receive the refreshed shared branding from the normal release image.

## Container image

- `ghcr.io/moodiness/rivune:1.7.0`
- `ghcr.io/moodiness/rivune:1.7`
- `ghcr.io/moodiness/rivune:1`
- `ghcr.io/moodiness/rivune:latest`
- Platforms: `linux/amd64`, `linux/arm64`

**Full changelog:** https://github.com/moodiness/rivune/compare/v1.6.2...v1.7.0
