# Releasing Rivune

Rivune follows [Semantic Versioning 2.0.0](https://semver.org/). The public HTTP/OpenAPI protocol, persisted database behavior, configuration, and published container interface determine compatibility.

- Increment **major** for incompatible API, configuration, or storage changes that cannot be upgraded automatically.
- Increment **minor** for backward-compatible features and additive protocol changes.
- Increment **patch** for backward-compatible fixes and internal hardening.
- Use prerelease tags such as `v2.0.0-rc.1` for builds that are not stable.

Every release tag must be an annotated `vMAJOR.MINOR.PATCH` Semantic Version pointing to the current `main` HEAD. Do not move or reuse a published tag.

## Before tagging

1. Confirm the target commit is the current protected `main` HEAD and all relevant backend, frontend, protocol, native-client, migration, and container checks pass locally.
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

The tag push runs `Release candidate CI`, a complete, read-only release gate: backend tests against PostgreSQL, frontend clean install/build/E2E, pinned OpenAPI lint and complete contract resolution, clean and upgrade migration validation, HTTPS/forwarded-header smoke, native client builds, and container builds for both release architectures. When that run succeeds, GitHub starts `Publish release` from the workflow definition on the default branch. Its unprivileged authorization job verifies the `v*` tag is valid SemVer, the completed run came from `.github/workflows/release-candidate.yml`, and the tag commit, tested SHA, and current `main` HEAD are identical. Only then can the environment-gated jobs receive `packages: write` or `contents: write`; each checks the tag and `main` again immediately before publishing. There is no manual publication path.

If GitHub does not create the release-candidate run for an otherwise valid tag push, dispatch the same read-only gate without moving the tag:

```sh
gh workflow run release-candidate.yml --ref main -f tag=v1.5.0
```

The manual path enforces the same SemVer, current-`main`, and tag-SHA checks. Its successful `workflow_run` is eligible for the same environment-gated publication workflow; it does not weaken artifact authorization or permit a stale tag.

## Required GitHub protection

Repository administrators must configure the controls that live outside the repository:

1. Protect `main` with required pull-request reviews; disallow force pushes and direct bypasses. Repository workflows do not run for ordinary branch pushes or pull requests: the complete verification gate runs only for an authorized `v*` release tag.
2. Add a tag ruleset for `v*` that restricts creation to release maintainers and blocks tag updates and deletion.
3. Create an environment named `release`, add required reviewers who are independent of the tag pusher, enable “Prevent self-review,” and restrict deployments to the protected `main` branch. Do not grant environment secrets to any other deployment branch.

These controls are part of the release trust boundary. The `workflow_run` publication workflow is loaded from the default branch rather than from the tag, and the environment approval is the final external authorization before either write-capable job starts.

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

Verify the release after the workflow completes:

```sh
docker buildx imagetools inspect ghcr.io/moodiness/rivune:1.5.0
docker pull ghcr.io/moodiness/rivune:1.5.0
docker image inspect ghcr.io/moodiness/rivune:1.5.0 --format '{{json .RepoDigests}}'
```

The manifest inspection must list only `linux/amd64` and `linux/arm64`. Confirm the GitHub Release points to the same tag and includes generated notes before announcing it.

## Failure and retry

Fix a gate failure on `main` and create a new version tag. Never force-update a tag that may have been observed or partially published. GitHub Actions jobs may be rerun for transient infrastructure failures on the unchanged tagged commit only while it remains the current `main` HEAD; otherwise create a new version tag after the fix reaches `main`. Image tags are content-addressed and the release-note step is ordered after successful image publication.
