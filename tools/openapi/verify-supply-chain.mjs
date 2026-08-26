import { readFileSync } from 'node:fs';

const read = (path) => readFileSync(new URL(`../../${path}`, import.meta.url), 'utf8');
const requireMatch = (value, pattern, message) => {
  if (!pattern.test(value)) throw new Error(message);
};
const count = (value, pattern) => [...value.matchAll(pattern)].length;

const dockerfile = read('server/Dockerfile');
const expectedFrom = [
  'node:24-bookworm-slim@sha256:a9f5f7c91a432850b2a8a7797adf5eadb6c733ceed61167806cee7ea7fbc29df',
  'golang:1.26.6-bookworm@sha256:116d58cbd88c1297624acc6e967a060012422bacf9930927e23fb719189c6f36',
  'debian:trixie-slim@sha256:d7e12182ce18b85b93007c1dedf31f2d29e01ccf3182cc4017c709b6259bc132',
];
const from = [...dockerfile.matchAll(/^FROM\s+(\S+)/gm)].map((match) => match[1]);
if (JSON.stringify(from) !== JSON.stringify(expectedFrom)) {
  throw new Error(`Dockerfile FROM digests differ from the approved set: ${from.join(', ')}`);
}
requireMatch(dockerfile, /DEBIAN_SNAPSHOT=20260825T000000Z/, 'Debian snapshot date is not pinned.');
for (const dependency of ['ca-certificates', 'ffmpeg', 'mesa-va-drivers', 'mesa-vulkan-drivers', 'tzdata', 'intel-media-va-driver']) {
  requireMatch(dockerfile, new RegExp(`${dependency.replaceAll('-', '\\-')}=[^\\s\\\\]+`), `${dependency} is not version-pinned.`);
}

const workflows = ['ci.yml', 'dev.yml', 'pages.yml', 'release-candidate.yml', 'release-gate.yml', 'release.yml'];
for (const workflow of workflows) {
  const body = read(`.github/workflows/${workflow}`);
  for (const match of body.matchAll(/^\s*uses:\s+([^\s#]+)(?:\s+#.*)?$/gm)) {
    const reference = match[1];
    if (reference.startsWith('./')) continue;
    if (!/@[0-9a-f]{40}$/.test(reference)) {
      throw new Error(`${workflow} contains an action that is not pinned to a full commit SHA: ${reference}`);
    }
  }
}

const candidate = read('.github/workflows/release-candidate.yml');
const gate = read('.github/workflows/release-gate.yml');
const release = read('.github/workflows/release.yml');
const candidateGateJob = candidate.match(/\n  ci:[\s\S]*?\n  apple-assets:/)?.[0] ?? '';
if (count(candidate, /uses: docker\/build-push-action@/g) !== 1) {
  throw new Error('Release candidate workflow must build the multi-architecture image exactly once.');
}
if (count(release, /uses: docker\/build-push-action@/g) !== 0) {
  throw new Error('Release publication must never rebuild the tested candidate image.');
}
requireMatch(candidate, /candidate_image: ghcr\.io\/\$\{\{ github\.repository \}\}@\$\{\{ needs\.build-candidate\.outputs\.digest \}\}/, 'Release gate does not receive the candidate digest.');
requireMatch(candidateGateJob, /permissions:\n      contents: read\n      actions: read\n      packages: read[\s\S]*uses: \.\/\.github\/workflows\/release-gate\.yml/, 'Release candidate caller must grant actions: read to the reusable candidate artifact verifier.');
requireMatch(candidate, /candidate-windows:[\s\S]*dotnet publish[\s\S]*candidate-windows-\$\{\{ github\.run_id \}\}-\$\{\{ github\.run_attempt \}\}/, 'Final Windows bytes are not built and described before the candidate succeeds.');
requireMatch(candidate, /candidate-release-assets:[\s\S]*:app:assembleRelease[\s\S]*candidate-unsigned-android-\$\{\{ github\.run_id \}\}-\$\{\{ github\.run_attempt \}\}/, 'Final unsigned Android bytes are not built and described before the candidate succeeds.');
requireMatch(candidate, /candidate-release\.json[\s\S]*candidate_release_inputs:[\s\S]*candidate_unsigned_android:[\s\S]*release_version_name:[\s\S]*release_version_code:/, 'Candidate artifacts, digests, and release identity are not passed into the gate.');
if (/--locked-mode/.test(candidate) || /--locked-mode/.test(gate)) {
  throw new Error('dotnet publish must enable RestoreLockedMode as an MSBuild property.');
}
requireMatch(candidate, /-p:RestoreLockedMode=true/, 'Release candidate Windows publishes do not enforce locked restore mode.');
requireMatch(gate, /runs-on: macos-26[\s\S]*swift test/, 'Swift validation must use the macOS 26 SDK required by the Apple UI.');
requireMatch(gate, /-p:RestoreLockedMode=true/, 'Release gate Windows publishes do not enforce locked restore mode.');
requireMatch(gate, /candidate-release-assets:[\s\S]*candidate-release-inputs\/candidate-release\.json[\s\S]*candidate-update-tool\/candidate-update-tool\.json/, 'Release gate does not consume the exact candidate Windows, Android, and verifier artifacts.');
requireMatch(gate, /RIVUNE_IMAGE: rivune-ci:current[\s\S]*scripts\/ci\/migrations\.sh[\s\S]*RIVUNE_IMAGE: rivune-ci:current[\s\S]*scripts\/ci\/reverse-proxy-smoke\.sh/, 'Migration and proxy smokes do not share the pulled candidate image.');
requireMatch(release, /candidate-assets:[\s\S]*github\.event\.workflow_run\.id[\s\S]*candidate-release\.json/, 'Release publication does not adopt the exact cross-workflow candidate bytes.');
if (/dotnet publish|:app:assembleRelease/.test(release)) throw new Error('Release workflow recompiles Windows or Android after the candidate gate.');
requireMatch(release, /RIVUNE_RELEASE_IMAGE: ghcr\.io\/\$\{\{ github\.repository \}\}@\$\{\{ needs\.publish\.outputs\.image_digest \}\}/, 'Release journey does not use the immutable tested digest.');
requireMatch(release, /actions\/attest-build-provenance@4d101475d8b20a2381f78447822ac1eab6504dd8[\s\S]*subject-checksums: release-asset-subjects\.sha256/, 'Final release assets lack pinned build provenance.');
requireMatch(release, /actions\/attest@1e69f48acb82d1966a394da916b4c1698aa569d6[\s\S]*predicate-type: https:\/\/rivune\.app\/attestations\/release\/v1[\s\S]*predicate-path: release-provenance-context\.json/, 'Release identity predicate does not use the pinned generic attestation action.');
requireMatch(release, /attestations: write[\s\S]*id-token: write/, 'Release asset attestation permissions are absent.');
requireMatch(release, /IMAGE_DIGEST: \$\{\{ needs\.release\.outputs\.image_digest \}\}[\s\S]*imagetools create[\s\S]*"\$\{IMAGE\}@\$\{IMAGE_DIGEST\}"/, 'Promotion does not consume the validated release digest.');
const signingJob = release.match(/\n  sign-manifest:[\s\S]*?\n  publish:/)?.[0] ?? '';
const verificationJob = release.match(/\n  verify-signed-assets:[\s\S]*?\n  publish:/)?.[0] ?? '';
const attestationJob = release.match(/\n  publish:[\s\S]*?\n  release-journey:/)?.[0] ?? '';
requireMatch(signingJob, /permissions:\n      contents: read\n      actions: read[\s\S]*RIVUNE_UPDATE_SIGNING_PRIVATE_KEY_PEM_BASE64: \$\{\{ secrets\.RIVUNE_UPDATE_SIGNING_PRIVATE_KEY_PEM_BASE64 \}\}[\s\S]*unset RIVUNE_UPDATE_SIGNING_PRIVATE_KEY_PEM_BASE64[\s\S]*openssl dgst -sha256 -sign/, 'Update signing is not isolated to a minimal non-OIDC job and bounded secret step.');
if (/actions\/checkout|\bgo run\b|id-token: write|attestations: write/.test(signingJob)) throw new Error('Update signing job executes repository source or receives OIDC permissions.');
requireMatch(verificationJob, /candidate-update-tool\/rivune-update-tool verify-signature[\s\S]*verified-release-assets/, 'Candidate-built verifier does not validate signatures before the OIDC boundary.');
if (/secrets\.|actions\/checkout|candidate-update-tool\/rivune-update-tool/.test(attestationJob)) throw new Error('OIDC attestation job exposes secrets, checks out code, or executes a candidate binary.');
requireMatch(release, /expected=.*rivune-update\.json rivune-update\.json\.sig/, 'The signed manifest sidecar is absent from the exact release asset set.');
requireMatch(release, /exact 13-asset set/, 'The release workflow does not enforce the 13-asset contract.');
requireMatch(release, /subject-checksums: release-asset-subjects\.sha256/, 'Release provenance does not cover the sidecar subject list.');
requireMatch(read('.github/workflows/pages.yml'), /for asset_name in rivune-update\.json rivune-update\.json\.sig Rivune-TV-runtime\.json/, 'The TV mirror does not publish the manifest signature sidecar.');
requireMatch(gate, /postgres:18-trixie@sha256:1957b2ff3137e4ef7f3bc813e74fff50b1e1ffddc85c8b9d6f14ade972be8687/, 'PostgreSQL gate service is not pinned by digest.');
if (/brew install/.test(gate)) throw new Error('Release gate still installs mutable Homebrew formulas.');
for (const digest of ['4d9e34b62172d645eed6457cac13fc222569974098ef4ee9c3368bedf0196806', 'afec26de54fd93084cdf8bae83f8cc436293f0c229064f17601d5f5945aee178', 'a9fe3ea2f86dfc72f6728417521ec9067b343277152b114f4e98d8cb0e263603', 'e80dbe0d2a2597e3c11c404f03337b981d74b4a8504b70586c354b7697a7c27f']) requireMatch(gate, new RegExp(digest), `Checksummed Apple tool digest missing: ${digest}`);
if (/\bnpx\s+(?:--yes\s+)?@redocly\/cli/.test(gate)) {
  throw new Error('Release gate still permits an implicit network Redocly install.');
}
requireMatch(gate, /node-version: 24\.19\.0[\s\S]*working-directory: tools\/openapi[\s\S]*npm ci --ignore-scripts[\s\S]*npm exec --offline -- redocly/, 'OpenAPI tooling is not installed from its lock and executed offline on exact Node.');

requireMatch(read('scripts/macos-discovery.sh'), /<string>protocol=22<\/string>/, 'macOS discovery still advertises an obsolete protocol.');
requireMatch(read('scripts/ci/reverse-proxy-smoke.sh'), /\["protocolVersion"\] == 22/, 'Reverse-proxy smoke does not enforce protocol v22.');

console.log('Supply-chain workflow invariants verified.');
