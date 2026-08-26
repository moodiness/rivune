import { describe, expect, it } from "vitest";
import { maximumUpdateSignatureBytes, verifyUpdateManifestSignature } from "./signature";

const manifest = new TextEncoder().encode(`{
  "schemaVersion":3,
  "channel":"stable",
  "version":"1.2.3",
  "tagName":"v1.2.3",
  "publishedAt":"2026-08-14T10:00:00Z",
  "releaseUrl":"https://github.com/moodiness/rivune/releases/tag/v1.2.3",
  "packages":{
    "android":{
      "format":"apk",
      "architectures":["universal"],
      "applicationId":"io.rivune.app",
      "buildVersion":"42",
      "minimumOsVersion":"8.0",
      "fileName":"Rivune-Android.apk",
      "url":"https://github.com/moodiness/rivune/releases/download/v1.2.3/Rivune-Android.apk",
      "size":18,
      "sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "signingCertificateSha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
      "futureAndroidField":true
    },
    "windows":{"format":"exe"},
    "futurePlatform":{"format":"future"}
  }
}`);
const sidecar = `{"schemaVersion":1,"algorithm":"ecdsa-p256-sha256","keyId":"4e9b15a0b6aed77908f3686fbf05a0a9c322ad846662eb758f56d4e65c22796f","manifestSha256":"c35d2c7cc2944446f26d6d77712b51cb5c184eacaf645a58b91956899884b247","signature":"MEUCIQD3Y1nimFWOa45eeOeBm5Mq8yJ1qKYWk5LY1X3y/ObraQIgJ9w6o9GEgnOWumU9yU8nR2MaNxJl5k/yINR965dZVDo="}`;
const bytes = (value: string) => new TextEncoder().encode(value);

describe("update manifest signature", () => {
  it("verifies raw manifest bytes with the pinned release key", async () => {
    await expect(verifyUpdateManifestSignature(manifest, bytes(sidecar))).resolves.toBeUndefined();
  });

  it("rejects altered bytes, key IDs, malformed signatures, and oversized sidecars", async () => {
    await expect(verifyUpdateManifestSignature(new Uint8Array([...manifest, 0x20]), bytes(sidecar))).rejects.toThrow();
    await expect(verifyUpdateManifestSignature(manifest, bytes(sidecar.replace("4e9b15", "000000")))).rejects.toThrow();
    await expect(verifyUpdateManifestSignature(manifest, bytes(sidecar.replace("MEUCIQ", "%%%CIQ")))).rejects.toThrow();
    await expect(verifyUpdateManifestSignature(manifest, bytes(sidecar.replace(/MEUCIQ[^\"]+/, "AQID")))).rejects.toThrow();
    await expect(verifyUpdateManifestSignature(manifest, new Uint8Array(maximumUpdateSignatureBytes + 1))).rejects.toThrow();
  });
});
