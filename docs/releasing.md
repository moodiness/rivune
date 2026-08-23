# Releasing Rivune

Rivune uses [Semantic Versioning](https://semver.org/). Increment major for an incompatible API, configuration, or storage cutover; minor for backward-compatible features; patch for fixes. Use prerelease tags such as `v2.0.0-rc.1` for unstable builds.

Tags are immutable annotated `vMAJOR.MINOR.PATCH` values pointing to current `main`. Never move or reuse a tag.

## Before tagging

1. Confirm the target is current `main` and the complete CI gate passes.
2. Review user-visible changes, migrations, environment changes, and protocol compatibility.
3. Update [`protocol/openapi.yaml`](../protocol/openapi.yaml), the protocol version, and every bundled client together when required.
4. Run the migration and proxy smoke documented in [Operations](operations.md#local-deployment-smoke) after database or deployment changes.
5. Rewrite [`.github/release-notes.md`](../.github/release-notes.md) with product-facing notes headed `# Rivune vMAJOR.MINOR.PATCH`.
6. Verify the GitHub protections below.

Run the update-manifest contract locally:

```sh
(cd clients/update && go test ./... && go run . validate --help)
```

## Android signing

Create the release identity once:

```sh
keytool -genkeypair -v -storetype PKCS12 \
  -keystore rivune-release.p12 \
  -alias rivune-android-release \
  -keyalg RSA -keysize 4096 -validity 10000 \
  -dname "CN=Rivune Android Release, O=Rivune, C=FR"
```

Store the complete PKCS12 file as `ANDROID_KEYSTORE_BASE64`, its high-entropy password as `ANDROID_KEYSTORE_PASSWORD` and `ANDROID_KEY_PASSWORD`, the alias as `ANDROID_KEY_ALIAS`, and the normalized SHA-256 certificate fingerprint as `ANDROID_SIGNING_CERT_SHA256`. Keep two encrypted offline backups. Never commit signing material.

The protected signing job receives only the unsigned APK. Apple, Windows, webOS, and Tizen release files remain unsigned. The workflow verifies package identities, versions, architectures, signatures, sizes, names, URLs, and SHA-256 digests before publication.

## Create the release

```sh
tag=v1.12.1
git switch main
git pull --ff-only
git tag -a "${tag}" -m "Rivune ${tag}"
git push origin "${tag}"
```

The tag starts **Release candidate CI**. It rejects lightweight, stale, moved, or non-SemVer tags and runs the complete reusable gate. Candidate jobs build immutable Apple, TV, and TV-installer inputs in parallel.

After the candidate succeeds, approve the protected **Publish release** environment. Publication independently reauthorizes the tag and source run, signs Android, verifies every immutable input, creates or adopts one matching Release, publishes the recorded OCI digest, and only then moves stable aliases.

If GitHub misses an otherwise valid tag event, dispatch the same gate without moving the tag:

```sh
gh workflow run release-candidate.yml --ref main -f tag="${tag}"
```

## Required GitHub protection

Before each release, confirm in both the API and Settings UI:

- `main` requires a pull request, the **Continuous integration** gate, linear history, resolved conversations, and administrator enforcement; force pushes and deletion are disabled;
- active `v*` rulesets restrict tag creation and reject tag updates or deletion;
- the `release` environment has no administrator bypass and accepts only protected branches;
- Android signing secrets exist only in that environment.

```sh
gh auth status
gh api repos/moodiness/rivune/environments/release
gh api repos/moodiness/rivune/branches/main/protection
gh api --paginate repos/moodiness/rivune/rulesets
```

The current solo-maintainer policy uses `moodiness` as release reviewer and permits self-review. When another trusted maintainer joins, require one pull-request approval and prevent environment self-review.

## Published artifacts

A stable release contains exactly 12 assets:

- eight application packages: Android, iOS, tvOS, visionOS, macOS, one universal Windows setup, webOS, and Tizen;
- `rivune-update.json` and the shared TV runtime;
- two universal TV-installer companions: one Windows EXE and one macOS DMG.

The OCI index at `ghcr.io/moodiness/rivune` contains only `linux/amd64` and `linux/arm64`, with provenance and SBOM attestations. Stable releases move the exact version, `major.minor`, `major`, and `latest`; prereleases move only their exact tag. Historical releases retain their original layouts.

```sh
version="${tag#v}"
image="ghcr.io/moodiness/rivune:${version}"
docker buildx imagetools inspect "${image}"
gh release view "${tag}" --json assets --jq '.assets[] | [.name,.digest] | @tsv'
```

Confirm the annotated tag, curated notes, 12 assets, two runnable OCI platforms, and two attestation manifests. Verify the APK with `apksigner verify --verbose --print-certs`; the universal Windows setup must report `NotSigned` from `Get-AuthenticodeSignature`.

## Failure and retry

Fix a deterministic gate failure on `main` and create a new version tag. Never replace a mismatching tag, Release, asset, or OCI digest. Rerun jobs only for transient infrastructure failure on the unchanged tag target. A matching draft may resume only after every existing asset and the recorded OCI digest revalidate exactly.
