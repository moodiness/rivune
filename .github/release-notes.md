# Rivune v1.11.5

## Highlights

- Rivune's native Android, iPhone, iPad, Apple TV, Apple Vision Pro, Mac, and Windows interfaces are now available in French, Spanish, German, Italian, and Brazilian Portuguese, with English as the fallback.
- Android adds a device-local **App language** setting with **System**, English, French, Spanish, German, Italian, and Portuguese (Brazil) choices. Selecting a language recreates the activity so the complete phone, tablet, and TV interface updates immediately.
- Apple clients use the language selected by iOS, iPadOS, tvOS, visionOS, or macOS. The same localization catalog is embedded in every Apple target, including playback controls, accessibility labels, settings, offline playback, diagnostics presentation, and update status.
- Windows follows the effective profile interface language, then the connected server language, then the Windows UI language. Changing the profile setting refreshes the current native interface without restarting Rivune.
- Server-provided media titles, collection names, profile names, provider data, and error details remain unchanged. Only Rivune-owned interface copy is translated.
- Translation catalogs preserve all formatting placeholders and keep unsupported locales on the complete English interface rather than mixing partial translations.

## Supported interface languages

- English
- French
- Spanish
- German
- Italian
- Portuguese (Brazil)

## Application installation

- Download `Rivune-x64.exe` on x64 Windows or `Rivune-arm64.exe` on ARM64 Windows. Windows 10 build 19041 or newer is required. The unsigned executables may trigger SmartScreen; verify the matching GitHub Release URL and asset SHA-256.
- `Rivune-iOS-unsigned.ipa`, `Rivune-tvOS-unsigned.ipa`, and `Rivune-visionOS-unsigned.ipa` must be re-signed with an identity and provisioning profile authorized for the destination device. Stock devices cannot install them as downloaded.
- `Rivune-macOS.dmg` contains an unsigned universal arm64/x86_64 application. Gatekeeper may require explicit local approval; rebuilding from source with Xcode remains the recommended trusted path.
- Download `Rivune-Android.apk` and complete Android's normal package-installation prompt. The application ID remains `io.rivune.app`, and the APK keeps the established Rivune release-signing identity.

## Upgrade notes

- This patch release adds no database migration. The current schema remains unchanged from v1.11.0.
- Existing operators can set `RIVUNE_VERSION=1.11.5`, pull, and recreate Rivune. Fresh Compose deployments now default to the immutable `1.11.5` image tag.
- The Android app-language choice is stored only in the app's private preferences. Apple uses platform language selection. Windows uses existing effective profile and discovery settings; no new credential or permission is introduced.
- GitHub publishes exactly eight release assets: `Rivune-Android.apk`, the three unsigned IPA files, `Rivune-macOS.dmg`, `rivune-update.json`, `Rivune-x64.exe`, and `Rivune-arm64.exe`.

## Container image

- `ghcr.io/moodiness/rivune:1.11.5`
- `ghcr.io/moodiness/rivune:1.11`
- `ghcr.io/moodiness/rivune:1`
- `ghcr.io/moodiness/rivune:latest`
- Platforms: `linux/amd64`, `linux/arm64`
- Provenance and SBOM attestations are published for both runnable platforms.

**Full changelog:** https://github.com/moodiness/rivune/compare/v1.11.4...v1.11.5
