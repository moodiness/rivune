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
(cd clients/update && go test ./... && go run . validate --help && go run . sign --help && go run . verify-signature --help)
```

The manifest signature is an ECDSA P-256/SHA-256 JSON sidecar named `rivune-update.json.sig`. It contains schema version 1, algorithm `ecdsa-p256-sha256`, the lowercase SHA-256 `keyId` of the public SPKI DER, the manifest SHA-256, and the ASN.1 signature. Clients pin both the public key and `keyId`, reject extra fields and sidecars over 4 KiB, and verify the sidecar before trusting package metadata.

Store the base64-encoded private PEM only as the protected `release` environment secret `RIVUNE_UPDATE_SIGNING_PRIVATE_KEY_PEM_BASE64`; never put the private key, its encoded value, or a command that prints it in documentation, logs, artifacts, or repository variables. Keep encrypted offline recovery copies. The public key and derived `keyId` are not secrets.

Rotation is a public compatibility cutover: first ship clients that pin the replacement public key and derived `keyId`, update every verifier and the release workflow's public verification input together, and only then replace the protected secret for the next release. Because the current verifier accepts one key, clients that pin only the retired key cannot consume manifests signed only by its replacement; plan a manual client upgrade rather than claiming transparent rotation.

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

The protected signing job receives only the unsigned APK. Apple, Windows, webOS, and Tizen release files remain unsigned. Native `Rivune-Windows.exe` payloads and their setup bootstrapper are built and inspected on GitHub's `windows-latest` runner; do not substitute a cross-compiled non-Windows build. The workflow verifies package identities, versions, architectures, signatures, sizes, names, URLs, and SHA-256 digests before publication.

## Create the release

```sh
tag=v1.13.4
git switch main
git pull --ff-only
git tag -a "${tag}" -m "Rivune ${tag}"
git push origin "${tag}"
```

The tag starts **Release candidate CI**. It rejects lightweight, stale, moved, or non-SemVer tags and runs the complete reusable gate. Candidate jobs build immutable Apple, TV, TV-installer, unsigned Android, native Windows, and OCI inputs; the Windows payloads and setup bootstrapper are built on a Windows host. The publish workflow signs Android, assembles the update manifest, signs that manifest in an isolated protected-environment job, and produces one immutable 13-file release artifact.

After the candidate succeeds, approve the protected **Publish release** environment. Publication independently reauthorizes the tag and source run, verifies every immutable input, generates and signs the update manifest, and emits two artifact attestations over the same 13 SHA-256 subjects: GitHub build provenance plus the Rivune release-identity predicate. The candidate OCI image is built once as a two-platform index, recorded by digest, and revalidated by digest before publication; no publish-stage image rebuild is allowed. The Release is created or adopted only after exact byte and digest checks, and stable aliases move last.

If GitHub misses an otherwise valid tag event, dispatch the same gate without moving the tag:

```sh
gh workflow run release-candidate.yml --ref main -f tag="${tag}"
```

## Required GitHub protection

Before each release, confirm in both the API and Settings UI:

- `main` requires a pull request, the **Continuous integration** gate, linear history, resolved conversations, and administrator enforcement; force pushes and deletion are disabled;
- active `v*` rulesets restrict tag creation and reject tag updates or deletion;
- the `release` environment has no administrator bypass and accepts only protected branches;
- Android and update-manifest signing secrets exist only in that environment.

```sh
gh auth status
gh api repos/moodiness/rivune/environments/release
gh api repos/moodiness/rivune/branches/main/protection
gh api --paginate repos/moodiness/rivune/rulesets
```

The current solo-maintainer policy uses `moodiness` as release reviewer and permits self-review. When another trusted maintainer joins, require one pull-request approval and prevent environment self-review.

## Published artifacts

A stable release contains exactly 13 assets:

- eight application packages: Android, iOS, tvOS, visionOS, macOS, one universal Windows setup, webOS, and Tizen;
- `rivune-update.json`, its `rivune-update.json.sig` signature sidecar, and the shared TV runtime;
- two universal TV-installer companions: one Windows EXE and one macOS DMG.

The OCI index at `ghcr.io/moodiness/rivune` contains only `linux/amd64` and `linux/arm64`. Each runnable manifest has one BuildKit attestation manifest containing provenance and SBOM predicates. Stable releases move the exact version, `major.minor`, `major`, and `latest`; prereleases move only their exact tag. Historical releases retain their original layouts.

Download and verify the published subjects without trusting local build output:

```sh
version="${tag#v}"
image="ghcr.io/moodiness/rivune:${version}"
rm -rf "verify-${tag}"
mkdir "verify-${tag}"
gh release download "${tag}" --dir "verify-${tag}"
gh release view "${tag}" --json assets --jq '.assets[] | [.name,.digest] | @tsv'
(cd "verify-${tag}" && sha256sum -- *)
for asset in "verify-${tag}"/*; do
  gh attestation verify "${asset}" --repo moodiness/rivune
  gh attestation verify "${asset}" --repo moodiness/rivune \
    --predicate-type https://rivune.app/attestations/release/v1
done
(cd clients/update && go run . verify-signature \
  --public-key-base64 'MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEacg8w48bnbKqa/KOJd070if0/100iHsU+o6ecokqIS6p7thhZb1ZR9YawxW7HuoEs5k6dW9sTCOyMjUcsgAQww==' \
  --signature "../../verify-${tag}/rivune-update.json.sig" \
  "../../verify-${tag}/rivune-update.json")
index="$(mktemp)"
docker buildx imagetools inspect "${image}" --raw > "${index}"
jq -e '
  ([.manifests[] | select(.annotations["vnd.docker.reference.type"] != "attestation-manifest") | .platform.os + "/" + .platform.architecture] | sort) == ["linux/amd64", "linux/arm64"] and
  ([.manifests[] | select(.annotations["vnd.docker.reference.type"] == "attestation-manifest")] | length) == 2' "${index}"
jq -r '.manifests[] | select(.annotations["vnd.docker.reference.type"] == "attestation-manifest") | .digest' "${index}" |
while read -r attestation_digest; do
  docker buildx imagetools inspect "${image%@*}@${attestation_digest}" --raw | jq -e '
    ([.layers[].annotations["in-toto.io/predicate-type"]] | any(. == "https://slsa.dev/provenance/v0.2" or . == "https://slsa.dev/provenance/v1")) and
    ([.layers[].annotations["in-toto.io/predicate-type"]] | any(startswith("https://spdx.dev/")))'
done
rm -f "${index}"
```

`gh attestation verify` must succeed for every downloaded asset against this repository. Confirm the annotated tag, curated notes, 13 assets, two runnable OCI platforms, and two OCI attestation manifests. Compare the local hashes with the `sha256:` values from `gh release view`. Verify the APK with `apksigner verify --verbose --print-certs`; on Windows, the universal setup must report `NotSigned` from `Get-AuthenticodeSignature`.

## Failure and retry

Fix a deterministic gate failure on `main` and create a new version tag. Never replace a mismatching tag, Release, asset, or OCI digest. Rerun jobs only for transient infrastructure failure on the unchanged tag target. A matching draft may resume only after every existing asset and the recorded OCI digest revalidate exactly.
