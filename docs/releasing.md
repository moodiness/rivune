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
6. Rewrite [`.github/release-notes.md`](../.github/release-notes.md) as product-facing notes and start it with `# Rivune vMAJOR.MINOR.PATCH` matching the chosen tag. The release gate rejects stale or mismatched notes; GitHub-generated contributor summaries are not used.

Create and push one annotated tag:

```sh
git switch main
git pull --ff-only
git tag -a v1.5.2 -m "Rivune v1.5.2"
git push origin v1.5.2
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

Only a successful candidate run is authorized to proceed in `Publish release`, whose top-level permission remains `contents: read`. Its `Authorize tested release candidate` job repeats the annotated-tag and same-commit checks without write permission. The externally approved `Publish multi-architecture image` job alone receives `packages: write`, reauthorizes the refs immediately before publishing the OCI image, and emits its provenance and SBOM. Release creation then runs automatically in a separate `contents: write` job only after image publication succeeds; immediately before writing, it reauthorizes the refs again. The release job intentionally has no second `environment: release` declaration, so one deliberate approval protects the ordered publication without adding a redundant post-publication prompt.

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
gh workflow run release-candidate.yml --ref main -f tag=v1.5.2
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

After all gates succeed, the workflow publishes one OCI manifest to `ghcr.io/moodiness/rivune` for exactly `linux/amd64` and `linux/arm64`, with provenance and an SBOM. A stable `v1.5.2` release receives:

```text
1.5.2
1.5
1
sha-<short-commit>
latest
```

A prerelease such as `v2.0.0-rc.1` receives its full SemVer and SHA tags, but does not move the stable major, minor, or `latest` aliases. The workflow then creates the matching GitHub Release from the curated notes committed at the tested tag. A stable release is explicitly marked as GitHub's latest release; a prerelease is not. If image publication fails, no GitHub Release is created.

Verify the release and its OCI attestations after the workflow completes:

```sh
image=ghcr.io/moodiness/rivune:1.5.2
docker buildx imagetools inspect "${image}"
docker buildx imagetools inspect "${image}" --raw > rivune-manifest.json
jq -e '[.manifests[] | select((.annotations // {})["vnd.docker.reference.type"] != "attestation-manifest") | [.platform.os, .platform.architecture]] | sort == [["linux", "amd64"], ["linux", "arm64"]]' rivune-manifest.json
jq -e '[.manifests[] | select((.annotations // {})["vnd.docker.reference.type"] == "attestation-manifest")] | length == 2' rivune-manifest.json
docker pull "${image}"
docker image inspect "${image}" --format '{{json .RepoDigests}}'
```

The first policy assertion requires exactly the `linux/amd64` and `linux/arm64` runnable manifests and no others. The second requires one OCI attestation manifest for each runnable platform, as emitted by the workflow's `provenance: mode=max` and `sbom: true` settings. Record the immutable index digest and assertion outputs as the post-release attestation. Confirm in the GitHub UI that the Release points to the same annotated tag and includes the curated notes before announcing it.

## Failure and retry

Fix a gate failure on `main` and create a new version tag. Never force-update a tag that may have been observed or partially published. GitHub Actions jobs may be rerun for transient infrastructure failures on the unchanged tagged commit only while it remains the current `main` HEAD; otherwise create a new version tag after the fix reaches `main`. Image tags are content-addressed and the release-note step is ordered after successful image publication.
