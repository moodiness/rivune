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
5. Configure the protected `release` environment with secrets `ANDROID_KEYSTORE_BASE64`, `ANDROID_KEYSTORE_PASSWORD`, and `ANDROID_KEY_PASSWORD`, plus variables `ANDROID_KEY_ALIAS` and `ANDROID_SIGNING_CERT_SHA256`. The certificate fingerprint is 64 hexadecimal SHA-256 characters; colons and uppercase are accepted by the workflow and normalized before publication.
6. Choose the next version according to the rules above.
7. Rewrite [`.github/release-notes.md`](../.github/release-notes.md) as product-facing notes and start it with `# Rivune vMAJOR.MINOR.PATCH` matching the chosen tag. The release gate rejects stale or mismatched notes; GitHub-generated contributor summaries are not used.

Create the Android release identity once, before the first APK is published:

```sh
keytool -genkeypair -v -storetype PKCS12 -keystore rivune-release.p12 -alias rivune-android-release -keyalg RSA -keysize 4096 -validity 10000 -dname "CN=Rivune Android Release, O=Rivune, C=FR"
keytool -list -v -storetype PKCS12 -keystore rivune-release.p12 -alias rivune-android-release
```

Use a generated high-entropy password and keep the same value for the PKCS12 store and key. Store the base64 encoding of the complete PKCS12 file as `ANDROID_KEYSTORE_BASE64`, that password as both password secrets, the alias as `ANDROID_KEY_ALIAS`, and the certificate's normalized SHA-256 fingerprint as `ANDROID_SIGNING_CERT_SHA256`. Keep two separate encrypted offline backups of the PKCS12 file, password, alias, and fingerprint. Never commit them. Losing or replacing this key prevents existing `io.rivune.app` installations from accepting future updates.

The Android manifest contract has no secret dependency and can be checked locally with:

```sh
cd clients/android/update
go test ./...
go run . --help
go run . generate --help
go run . validate --help
```

Create and push one annotated tag:

```sh
git switch main
git pull --ff-only
git tag -a v1.6.2 -m "Rivune v1.6.2"
git push origin v1.6.2
```

The tag push runs `Release candidate CI`. Its read-only `authorize` job resolves
`git/ref/tags/<tag>`, rejects an absent ref or a lightweight tag, requires the
annotated tag object to target a commit, and requires that commit, the run SHA,
and current `main` HEAD to be identical. The reusable `Release gate` then runs
these exact jobs: `Backend tests`, `Server Windows compile`, `Frontend build and
E2E`, `OpenAPI lint and contract resolution`, `Swift API client`, `Kotlin API
client`, `Windows API client`, `Container, migrations, and HTTPS proxy (amd64)`,
and `Container image (arm64)`. The native ARM64 build runs in parallel with the
AMD64 behavioral smoke tests instead of using QEMU on their critical path. The
AMD64 container job also validates the single supported CPU-only manifest,
`compose.yaml`, plus that manifest combined with the supported
`compose.amd-intel.yaml` GPU overlay, using non-secret placeholders.

Only a successful candidate run is authorized to proceed in `Publish release`, whose top-level permission remains `contents: read`. Its `Authorize tested release candidate` job repeats the annotated-tag and tested-commit checks without write permission. Initial publication also requires that commit to remain current `main`; if exactly one matching Release already exists, authorization permits the immutable tag/run pair to continue so the downstream job can fully validate that Release before adoption. The sole job attached to the protected `release` environment decodes the private keystore into an ephemeral directory, derives Android `versionName` from the tag, and sets the monotone `versionCode` to `2,000,000,000 + github.run_number`. The workflow run number stays fixed across rerun attempts, so a retry rebuilds the same Android version. The job builds and verifies the signed APK and produces the dedicated Android manifest and checksums. Signature, certificate fingerprint, package identity, versions, file size, SHA-256, manifest contract, and exact checksum set must all pass before the OCI image can be pushed. The job then reauthorizes the immutable tag and the same initial-or-resume condition, publishes the multi-architecture image, provenance, and SBOM, and finally uploads the immutable Actions artifact. The separate `contents: write` job downloads and revalidates that artifact, reauthorizes refs, and either creates a complete draft GitHub Release or adopts the one matching draft or published Release. It intentionally has no second `environment: release` declaration, so one deliberate approval protects the ordered publication without exposing signing secrets to the release job.

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
gh workflow run release-candidate.yml --ref main -f tag=v1.6.2
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

After all gates succeed, the workflow publishes one OCI manifest to `ghcr.io/moodiness/rivune` for exactly `linux/amd64` and `linux/arm64`, with provenance and an SBOM. A stable `v1.6.2` release receives:

```text
1.6.2
1.6
1
latest
```

The matching GitHub Release contains exactly `rivune-android-1.6.2.apk`, `rivune-android-1.6.2-corresponding-source.tar.gz`, `rivune-android-update.json`, and `SHA256SUMS`. The JSON document is an Android-specific schema-v1 manifest with the common release metadata and one `package` object; it does not advertise unbuilt platforms. `SHA256SUMS` covers exactly the APK, corresponding-source archive, and JSON manifest. The APK is universal, uses application ID `io.rivune.app`, requires Android 8.0 or newer, and is signed with the certificate fingerprint recorded in the manifest. The corresponding-source archive contains the exact Rivune revision and complete authenticated native and JVM source graph needed to rebuild the GPL-enabled Android work.

A prerelease such as `v2.0.0-rc.1` receives its full SemVer tag but does not move the stable major, minor, or `latest` aliases. Its update manifest channel and GitHub prerelease flag are both `prerelease`. A stable release uses channel `stable`, becomes GitHub's latest release, and provides the stable URL `https://github.com/moodiness/rivune/releases/latest/download/rivune-android-update.json`. The workflow first publishes an unlisted OCI digest and records that exact digest in an authenticated HTML comment in the draft Release body. It validates the complete Release, promotes and verifies the exact immutable SemVer tag, publishes the Release once, then moves mutable stable aliases only while that tag is still GitHub's latest stable release. A rerun may publish a fresh unlisted build digest, but it adopts exactly one matching draft or published Release only after revalidating its metadata, curated body, digest marker, exact downloaded asset set, checksums, update-manifest references, and APK identity, signing certificate, and versions. Promotion always uses the originally recorded digest. Zero or multiple matching Releases, a different immutable image tag, or any mismatching Release data fails closed; an adopted draft is never deleted by retry cleanup.

Verify the release and its OCI attestations after the workflow completes:

```sh
image=ghcr.io/moodiness/rivune:1.6.2
docker buildx imagetools inspect "${image}"
docker buildx imagetools inspect "${image}" --raw > rivune-manifest.json
jq -e '[.manifests[] | select((.annotations // {})["vnd.docker.reference.type"] != "attestation-manifest") | [.platform.os, .platform.architecture]] | sort == [["linux", "amd64"], ["linux", "arm64"]]' rivune-manifest.json
jq -e '[.manifests[] | select((.annotations // {})["vnd.docker.reference.type"] == "attestation-manifest")] | length == 2' rivune-manifest.json
docker pull "${image}"
docker image inspect "${image}" --format '{{json .RepoDigests}}'
```

The first policy assertion requires exactly the `linux/amd64` and `linux/arm64` runnable manifests and no others. The second requires one OCI attestation manifest for each runnable platform, as emitted by the workflow's `provenance: mode=max` and `sbom: true` settings. Record the immutable index digest and assertion outputs as the post-release attestation. Confirm in the GitHub UI that the Release points to the same annotated tag, includes the curated notes, and contains exactly the four Android release assets above. Download them, run `sha256sum --check SHA256SUMS`, and inspect the APK certificate with Android SDK `apksigner verify --verbose --print-certs` before announcing the release.

## Failure and retry

Fix a gate failure on `main` and create a new version tag. Never force-update a tag that may have been observed or partially published, and never replace mismatching tags, Releases, or assets. GitHub Actions jobs may be rerun for transient infrastructure failures on the unchanged annotated tag target. Before the first Release is created, authorization still requires that target to be current `main`. Once the matching draft or published Release exists, retries resume from its fully revalidated assets and recorded OCI digest rather than replacing it; promotion continues to require the immutable annotated tag target even if `main` has subsequently advanced.
