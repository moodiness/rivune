# Releasing Rivune

Rivune follows [Semantic Versioning 2.0.0](https://semver.org/). The public HTTP/OpenAPI protocol, persisted database behavior, configuration, and published container interface determine compatibility.

- Increment **major** for incompatible API, configuration, or storage changes that cannot be upgraded automatically.
- Increment **minor** for backward-compatible features and additive protocol changes.
- Increment **patch** for backward-compatible fixes and internal hardening.
- Use prerelease tags such as `v2.0.0-rc.1` for builds that are not stable.

Every release tag must be an annotated `vMAJOR.MINOR.PATCH` Semantic Version pointing to the current `main` HEAD. Do not move or reuse a published tag.

## Before tagging

1. Confirm the target commit is the current `main` HEAD and all relevant backend, frontend, protocol, native-client, migration, and container checks pass locally.
2. Review user-visible and operational changes, including required environment changes and migration behavior.
3. For protocol changes, confirm the protocol version and [`protocol/openapi.yaml`](../protocol/openapi.yaml) describe the shipped behavior and supported native clients have been updated.
4. For database changes, run the disposable clean-install and immediately-previous-version upgrade checks documented in [Production operations](operations.md#migration-and-proxy-validation).
5. Configure the protected `release` environment with secrets `ANDROID_KEYSTORE_BASE64`, `ANDROID_KEYSTORE_PASSWORD`, and `ANDROID_KEY_PASSWORD`. Configure repository variables `ANDROID_KEY_ALIAS` and `ANDROID_SIGNING_CERT_SHA256`. The fingerprint is 64 hexadecimal SHA-256 characters; colons and uppercase are accepted and normalized before publication. Windows needs no signing secret because the public asset is intentionally unsigned.
6. Choose the next version according to the rules above.
7. Rewrite [`.github/release-notes.md`](../.github/release-notes.md) as product-facing notes and start it with `# Rivune vMAJOR.MINOR.PATCH` matching the chosen tag. The release gate rejects stale or mismatched notes; GitHub-generated contributor summaries are not used.

Create the Android release identity once, before the first APK is published:

```sh
keytool -genkeypair -v -storetype PKCS12 -keystore rivune-release.p12 -alias rivune-android-release -keyalg RSA -keysize 4096 -validity 10000 -dname "CN=Rivune Android Release, O=Rivune, C=FR"
keytool -list -v -storetype PKCS12 -keystore rivune-release.p12 -alias rivune-android-release
```

Use a generated high-entropy password and keep the same value for the PKCS12 store and key. Store the base64 encoding of the complete PKCS12 file as `ANDROID_KEYSTORE_BASE64`, that password as both password secrets, the alias as `ANDROID_KEY_ALIAS`, and the certificate's normalized SHA-256 fingerprint as `ANDROID_SIGNING_CERT_SHA256`. Keep two separate encrypted offline backups of the PKCS12 file, password, alias, and fingerprint. Never commit them. Losing or replacing this key prevents existing `io.rivune.app` installations from accepting future updates.

The release workflow builds and uploads the unsigned APK before any signing secret is available. A separate protected job on a fresh runner downloads only that immutable APK, decodes the PKCS12 under the runner's private temporary directory, signs with `apksigner`, validates the sole certificate fingerprint, and removes the temporary material on both success and failure. Gradle, dependency-source collection, manifest generation, and container tooling run in other jobs and never receive the keystore or either password.


The global Android/Windows update-manifest contract has no secret dependency and can be checked locally with:

```sh
cd clients/update
go test ./...
go run . --help
go run . generate --help
go run . validate --help
```

Create and push one annotated tag:

```sh
git switch main
git pull --ff-only
git tag -a v1.8.0 -m "Rivune v1.8.0"
git push origin v1.8.0
```

The tag push runs `Release candidate CI`. Its read-only `authorize` job resolves
`git/ref/tags/<tag>`, rejects an absent ref or a lightweight tag, requires the
annotated tag object to target a commit, and requires that commit, the run SHA,
and current `main` HEAD to be identical. The reusable `Release gate` then runs
these exact jobs: `Backend tests`, `Server Windows compile`, `Frontend build and
E2E`, `OpenAPI lint and contract resolution`, `Swift API client`, `Android
client`, `Windows x64 portable client`, `Windows ARM64 portable client`,
`Container, migrations, and HTTPS proxy (amd64)`, and `Container image (arm64)`. The native ARM64 container build runs in parallel with the
AMD64 behavioral smoke tests instead of using QEMU on their critical path. The
AMD64 container job also validates the single supported CPU-only manifest,
`compose.yaml`, plus that manifest combined with the supported
`compose.amd-intel.yaml` GPU overlay, using non-secret placeholders.

Only a successful candidate run is authorized to proceed in `Publish release`, whose top-level permission remains `contents: read`. Its `Authorize tested release candidate` job repeats the annotated-tag and tested-commit checks without write permission. Initial publication also requires that commit to remain current `main`; if exactly one matching Release already exists, authorization permits the immutable tag/run pair to continue so downstream jobs can fully validate it before adoption. `windows-asset` produces self-contained single-file canonical x64 `Rivune-x64.exe` and ARM64 `Rivune-arm64.exe`, plus byte-identical x64 `Rivune.exe` for compatibility with the updater first released in v1.8.0. It verifies each PE architecture, size, version, exact filename, and unsigned status before uploading the immutable inputs. `build-assets` stamps tag-derived Android metadata, produces the unsigned APK, verifies the pinned libmpv artifact, and collects the Windows artifacts. The protected `sign-android` job receives only the unsigned APK, decodes the Android private keystore, signs with `apksigner`, verifies the certificate and versions, then uploads the immutable signed APK. The unprotected `publish` job consumes those immutable inputs, generates the global and legacy manifests, publishes the unlisted OCI image, and uploads one immutable release artifact. Android `versionName` comes from the tag and monotone `versionCode` is `2,000,000,000 + github.run_number`; the workflow run number stays fixed across rerun attempts. A separate `contents: write` job revalidates the artifact and either creates a complete draft GitHub Release or semantically adopts the one matching draft or published Release. Native Windows runners then download the matching executable and global manifest and revalidate PE architecture, version, unsigned status, size, SHA-256, and exact URL before promotion can publish the Release and move container aliases.

To reproduce the Compose policy checks locally with disposable, non-production values, run exactly:

```sh
export RIVUNE_DATABASE_PASSWORD=compose-validation-database-password
export RIVUNE_POSTGRES_SUPERUSER_PASSWORD=compose-validation-superuser-password
export RIVUNE_RESTORE_PASSWORD=compose-validation-restore-password
export RIVUNE_SETUP_TOKEN=compose-validation-setup-token
export RIVUNE_ENCRYPTION_KEYS=1:1212121212121212121212121212121212121212121212121212121212121212
export RIVUNE_VIDEO_GROUP_ID=109
export RIVUNE_VIDEO_DEVICE=/dev/dri/renderD129
docker compose -f compose.yaml config --quiet
docker compose -f compose.yaml config --format json | jq -e '
  (.services.rivune.environment | has("RIVUNE_VIDEO_GROUP_ID") | not) and
  (.services.rivune | has("devices") | not) and
  (.services.rivune | has("group_add") | not)
' >/dev/null

docker compose -f compose.yaml -f compose.amd-intel.yaml config --quiet
docker compose -f compose.yaml -f compose.amd-intel.yaml config --format json | jq -e '
  .services.rivune.environment.RIVUNE_VIDEO_GROUP_ID == "109" and
  .services.rivune.devices == [{"source":"/dev/dri/renderD129","target":"/dev/dri/renderD128","permissions":"rwm"}] and
  .services.rivune.group_add == ["109"]
' >/dev/null
```

If GitHub does not create the release-candidate run for an otherwise valid tag push, dispatch the same read-only gate without moving the tag:

```sh
gh workflow run release-candidate.yml --ref main -f tag=v1.8.0
```

The manual path enforces the same SemVer, annotated-tag, current-`main`, and tag-target checks. Its successful `workflow_run` is eligible for the same external release-environment gate; it does not weaken artifact authorization or permit a stale or lightweight tag.

## Required external GitHub protection

Repository files cannot configure or prove GitHub environment, branch, or ruleset protection. Reverify the controls below through both authenticated API responses and the GitHub Settings UI before creating a release tag.

Rivune is currently maintained by the single repository owner, `moodiness`. The repository therefore uses an explicit solo-maintainer gate instead of pretending that an independent approval is available:

1. `main` requires a pull request, enforces the rules for administrators, requires linear history and resolved conversations, and blocks force pushes and deletion. The required approval count is zero until another maintainer exists.
2. Active `v*` tag rulesets allow only `moodiness` to create release tags and allow nobody, including the owner, to update or delete one after creation.
3. The `release` environment requires approval by `moodiness`, permits self-review for this solo-maintainer workflow, disables administrator bypass, and accepts deployments only from protected branches. Do not grant its secrets to another branch.

This gate provides deliberate approval and immutable release refs, but not independent separation of duties. As soon as another trusted maintainer is added, require one pull-request approval, enable environment self-review prevention, and make that maintainer the release reviewer.

Run the authenticated checks below, inspect the complete JSON rather than relying only on selected fields, and compare it with **Settings → Environments → release**, **Settings → Branches/Rules → main**, and **Settings → Rules → Rulesets → `v*`**:

```sh
gh auth status
gh api repos/moodiness/rivune/environments/release
gh api repos/moodiness/rivune/branches/main/protection
gh api --paginate repos/moodiness/rivune/rulesets
```

The environment response must show `moodiness` as required reviewer, `can_admins_bypass: false`, and a protected-branch deployment policy. Branch protection must show administrator enforcement with force pushes and deletion disabled. The two active tag rulesets must separately restrict creation and reject every update or deletion without bypass. If any endpoint is unauthorized, any field is missing, or API and UI disagree, stop: an approval prompt or successful workflow run alone does not prove the complete policy.

## Published artifacts

After all gates succeed, the workflow publishes one OCI manifest to `ghcr.io/moodiness/rivune` for exactly `linux/amd64` and `linux/arm64`, with provenance and an SBOM. A stable `v1.8.0` release receives:

```text
1.8.0
1.8
1
latest
```

The matching GitHub Release contains exactly `rivune-android-<version>.apk`, canonical x64 `Rivune-x64.exe`, ARM64 `Rivune-arm64.exe`, the byte-identical legacy x64 bridge `Rivune.exe`, `rivune-update.json`, and the compatibility bridge `rivune-android-update.json`. GitHub publishes a SHA-256 digest for every release asset; no separate checksum-list asset is generated. `rivune-update.json` remains schema v2: common release metadata plus required `packages.android`, legacy-compatible x64 `packages.windows`, canonical x64 `packages.windowsX64`, and ARM64 `packages.windowsArm64` entries. The legacy and canonical x64 entries have different filenames but identical size and SHA-256. Older v1.8.0 clients continue to use `packages.windows`; architecture-aware clients select the canonical package for their process architecture.

A prerelease such as `v2.0.0-rc.1` receives its full SemVer tag but does not move the stable major, minor, or `latest` aliases. Its update-manifest channel and GitHub prerelease flag are both `prerelease`. A stable release uses channel `stable`, becomes GitHub's latest release, and provides the global feed `https://github.com/moodiness/rivune/releases/latest/download/rivune-update.json`; the legacy Android URL remains only for installed schema-v1 clients. The workflow first publishes an unlisted OCI digest and records that exact digest in an authenticated HTML comment in the draft Release body. It validates the complete Release, promotes and verifies the exact immutable SemVer tag, publishes the Release once, then moves mutable stable aliases only while that tag is still GitHub's latest stable release. A rerun may publish a fresh unlisted build digest, but it adopts exactly one matching draft or published Release only after revalidating its metadata, curated body, digest marker, exact downloaded asset set, GitHub asset digests, direct trusted-byte hashes, both update manifests, APK identity/certificate/versions, and both Windows executable architectures/versions/hashes/unsigned states. Promotion always uses the originally recorded digest. Zero or multiple matching Releases, a different immutable image tag, or any mismatching Release data fails closed; an adopted draft is never deleted by retry cleanup.

Verify the release and its OCI attestations after the workflow completes:

```sh
image=ghcr.io/moodiness/rivune:1.8.0
docker buildx imagetools inspect "${image}"
docker buildx imagetools inspect "${image}" --raw > rivune-manifest.json
jq -e '[.manifests[] | select((.annotations // {})["vnd.docker.reference.type"] != "attestation-manifest") | [.platform.os, .platform.architecture]] | sort == [["linux", "amd64"], ["linux", "arm64"]]' rivune-manifest.json
jq -e '[.manifests[] | select((.annotations // {})["vnd.docker.reference.type"] == "attestation-manifest")] | length == 2' rivune-manifest.json
docker pull "${image}"
docker image inspect "${image}" --format '{{json .RepoDigests}}'
```

The first policy assertion requires exactly the `linux/amd64` and `linux/arm64` runnable manifests and no others. The second requires one OCI attestation manifest for each runnable platform, as emitted by the workflow's `provenance: mode=max` and `sbom: true` settings. Record the immutable index digest and assertion outputs as the post-release attestation. Confirm in the GitHub UI that the Release points to the same annotated tag, includes the curated notes, and contains exactly the six assets above. Inspect GitHub's asset digests with `gh release view <tag> --json assets --jq '.assets[] | [.name,.digest] | @tsv'`, inspect the APK certificate with Android SDK `apksigner verify --verbose --print-certs`, and confirm that `Rivune.exe` and `Rivune-x64.exe` are byte-identical.

## Failure and retry

Fix a gate failure on `main` and create a new version tag. Never force-update a tag that may have been observed or partially published, and never replace mismatching tags, Releases, or assets. GitHub Actions jobs may be rerun for transient infrastructure failures on the unchanged annotated tag target. Before the first Release is created, authorization still requires that target to be current `main`. Once the matching draft or published Release exists, retries resume from its revalidated assets and recorded OCI digest rather than replacing it; an incomplete matching draft is completed only after every existing asset matches the rebuilt trusted bytes. The update manifests use the immutable source-workflow creation time, so retry attempts reproduce their release metadata. Promotion continues to require the immutable annotated tag target even if `main` has subsequently advanced.
