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
5. Choose the next version according to the rules above.

Create and push one annotated tag:

```sh
git switch main
git pull --ff-only
git tag -a v1.5.0 -m "Rivune v1.5.0"
git push origin v1.5.0
```

The tag push runs `Release candidate CI`. Its read-only `authorize` job resolves `git/ref/tags/<tag>`, rejects an absent ref or a lightweight tag, requires the annotated tag object to target a commit, and requires that commit, the run SHA, and current `main` HEAD to be identical. The reusable `Release gate` then runs these exact jobs: `Backend tests`, `Frontend build and E2E`, `OpenAPI lint and contract resolution`, `Swift API client`, `Kotlin API client`, `Windows API client`, and `Container, migrations, and HTTPS proxy`. The container job also runs `docker compose config --quiet` for `compose.yaml`, its VAAPI and NVIDIA overrides, and `deploy/caddy/compose.yaml`, using non-secret validation placeholders for required interpolation values.

Only a successful candidate run is authorized to proceed in `Publish release`, whose top-level permission remains `contents: read`. Its `Authorize tested release candidate` job repeats the annotated-tag and same-commit checks without write permission. Immediately before each existing write-capable job acts, the workflow resolves the annotated tag object and target commit again. `Publish multi-architecture image` alone receives `packages: write`; after it succeeds, `Create GitHub release notes` alone receives `contents: write`.

To reproduce the four Compose policy checks locally with disposable, non-production values, run exactly:

```sh
export RIVUNE_DATABASE_PASSWORD=compose-validation-database-password
export RIVUNE_POSTGRES_SUPERUSER_PASSWORD=compose-validation-superuser-password
export RIVUNE_RESTORE_PASSWORD=compose-validation-restore-password
export RIVUNE_SETUP_TOKEN=compose-validation-setup-token
export RIVUNE_HOST=rivune.invalid
docker compose -f compose.yaml config --quiet
docker compose -f compose.yaml -f compose.vaapi.yaml config --quiet
docker compose -f compose.yaml -f compose.nvidia.yaml config --quiet
docker compose -f deploy/caddy/compose.yaml config --quiet
```

If GitHub does not create the release-candidate run for an otherwise valid tag push, dispatch the same read-only gate without moving the tag:

```sh
gh workflow run release-candidate.yml --ref main -f tag=v1.5.0
```

The manual path enforces the same SemVer, annotated-tag, current-`main`, and tag-target checks. Its successful `workflow_run` is eligible for the same external release-environment gate; it does not weaken artifact authorization or permit a stale or lightweight tag.

## Required external GitHub protection — publication currently blocked

Repository files cannot configure or prove GitHub environment, branch, or ruleset protection. **Do not publish a release until every control below is configured and reverified through both an authenticated API request and the GitHub Settings UI.**

Observed on 2026-08-09: the public `GET /repos/moodiness/rivune/environments/release` response showed environment ID `19369667540`, `protection_rules: []`, `deployment_branch_policy: null`, and `can_admins_bypass: true`. Therefore the required reviewers, “Prevent self-review,” and branch restriction were not configured in the observed state, and publication is blocked. The protection state of `main` and GHCR package versions/attestations was not anonymously verifiable because the consulted endpoints returned HTTP 401. The `v*` ruleset was not confirmed. Treat those controls as unverified, not as absent or protected.

Before creating or dispatching a candidate, an administrator must configure and a different release maintainer must verify:

1. Protect `main` with required pull-request reviews; disallow force pushes and direct bypasses.
2. Add a tag ruleset for `v*` that restricts creation to release maintainers and blocks updates and deletion.
3. Configure the existing `release` environment with independent required reviewers, “Prevent self-review,” administrator bypass disabled, and deployment restricted to the protected `main` branch. Do not grant its secrets to another branch.

Run the authenticated checks below, inspect the complete JSON rather than relying only on the selected fields, and compare it with **Settings → Environments → release**, **Settings → Branches/Rules → main**, and **Settings → Rules → Rulesets → `v*`**:

```sh
gh auth status
gh api repos/moodiness/rivune/environments/release
gh api repos/moodiness/rivune/branches/main/protection
gh api --paginate repos/moodiness/rivune/rulesets
```

The environment response must show the required reviewers and branch policy, self-review prevention must be enabled, and administrator bypass must be disabled. The branch and ruleset responses must show the controls above. Record the authenticated outputs and the second maintainer's UI verification in the release record. If any endpoint is unauthorized, any field is missing, or API and UI disagree, stop: neither an environment approval prompt nor a successful workflow run is evidence that protection is configured.

## Published artifacts

After all gates succeed, the workflow publishes one OCI manifest to `ghcr.io/moodiness/rivune` for exactly `linux/amd64` and `linux/arm64`, with provenance and an SBOM. A stable `v1.5.0` release receives:

```text
1.5.0
1.5
1
sha-<short-commit>
latest
```

A prerelease such as `v2.0.0-rc.1` receives its full SemVer and SHA tags, but does not move the stable major, minor, or `latest` aliases. The workflow then creates the matching GitHub Release and automatically generated release notes. If image publication fails, no GitHub Release is created.

Verify the release and its OCI attestations after the workflow completes:

```sh
image=ghcr.io/moodiness/rivune:1.5.0
docker buildx imagetools inspect "${image}"
docker buildx imagetools inspect "${image}" --raw > rivune-manifest.json
jq -e '[.manifests[] | select(.annotations["vnd.docker.reference.type"] != "attestation-manifest") | [.platform.os, .platform.architecture]] | sort == [["linux", "amd64"], ["linux", "arm64"]]' rivune-manifest.json
jq -e '[.manifests[] | select(.annotations["vnd.docker.reference.type"] == "attestation-manifest")] | length == 2' rivune-manifest.json
docker pull "${image}"
docker image inspect "${image}" --format '{{json .RepoDigests}}'
```

The first policy assertion requires exactly the `linux/amd64` and `linux/arm64` runnable manifests and no others. The second requires one OCI attestation manifest for each runnable platform, as emitted by the workflow's `provenance: mode=max` and `sbom: true` settings. Record the immutable index digest and assertion outputs as the post-release attestation. Confirm in the GitHub UI that the Release points to the same annotated tag and includes generated notes before announcing it.

## Failure and retry

Fix a gate failure on `main` and create a new version tag. Never force-update a tag that may have been observed or partially published. GitHub Actions jobs may be rerun for transient infrastructure failures on the unchanged tagged commit only while it remains the current `main` HEAD; otherwise create a new version tag after the fix reaches `main`. Image tags are content-addressed and the release-note step is ordered after successful image publication.
