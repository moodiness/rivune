import { decodeUtf8, sha256Hex } from "./bytes";

export const maximumUpdateSignatureBytes = 4 * 1024;
const algorithm = "ecdsa-p256-sha256";
const keyId = "4e9b15a0b6aed77908f3686fbf05a0a9c322ad846662eb758f56d4e65c22796f";
const publicKeyBase64 = "MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEacg8w48bnbKqa/KOJd070if0/100iHsU+o6ecokqIS6p7thhZb1ZR9YawxW7HuoEs5k6dW9sTCOyMjUcsgAQww==";

function decodeBase64(value: string): Uint8Array {
  if (!/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(value)) throw new Error("The update signature is not canonical base64.");
  let binary: string;
  try { binary = atob(value); } catch { throw new Error("The update signature is not valid base64."); }
  const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0));
  let canonical = "";
  for (let offset = 0; offset < bytes.length; offset += 0x2000) canonical += String.fromCharCode(...bytes.subarray(offset, offset + 0x2000));
  if (btoa(canonical) !== value) throw new Error("The update signature is not canonical base64.");
  return bytes;
}

function derInteger(bytes: Uint8Array, offset: number): { value: Uint8Array; next: number } {
  if (bytes[offset] !== 0x02 || offset + 2 > bytes.length) throw new Error("The update signature DER is invalid.");
  const length = bytes[offset + 1];
  const start = offset + 2;
  const end = start + length;
  if (length === 0 || length > 33 || end > bytes.length || (bytes[start] & 0x80) !== 0) throw new Error("The update signature DER is invalid.");
  if (length > 1 && bytes[start] === 0 && (bytes[start + 1] & 0x80) === 0) throw new Error("The update signature DER is not canonical.");
  const value = bytes.subarray(start + (bytes[start] === 0 ? 1 : 0), end);
  if (value.length === 0 || value.length > 32 || value.every((byte) => byte === 0)) throw new Error("The update signature DER is invalid.");
  return { value, next: end };
}

function derToP1363(bytes: Uint8Array): Uint8Array {
  if (bytes.length < 8 || bytes.length >= 128 || bytes[0] !== 0x30 || bytes[1] !== bytes.length - 2) throw new Error("The update signature DER is invalid.");
  const r = derInteger(bytes, 2);
  const s = derInteger(bytes, r.next);
  if (s.next !== bytes.length) throw new Error("The update signature DER is invalid.");
  const output = new Uint8Array(64);
  output.set(r.value, 32 - r.value.length);
  output.set(s.value, 64 - s.value.length);
  return output;
}

type Sidecar = {
  schemaVersion: number;
  algorithm: string;
  keyId: string;
  manifestSha256: string;
  signature: string;
};

export async function verifyUpdateManifestSignature(manifest: Uint8Array, sidecarBytes: Uint8Array): Promise<void> {
  if (sidecarBytes.length === 0 || sidecarBytes.length > maximumUpdateSignatureBytes) throw new Error("The update signature size is invalid.");
  const text = decodeUtf8(sidecarBytes);
  const propertyNames: string[] = [];
  const propertyPattern = /"((?:\\.|[^"\\])*)"\s*:/g;
  let propertyMatch: RegExpExecArray | null;
  while ((propertyMatch = propertyPattern.exec(text)) !== null) propertyNames.push(propertyMatch[1]);
  propertyNames.sort();
  const expectedProperties = ["algorithm", "keyId", "manifestSha256", "schemaVersion", "signature"];
  if (propertyNames.length !== expectedProperties.length || propertyNames.some((name, index) => name !== expectedProperties[index])) {
    throw new Error("The update signature shape is invalid.");
  }
  let value: unknown;
  try { value = JSON.parse(text); } catch { throw new Error("The update signature is not valid JSON."); }
  if (typeof value !== "object" || value === null || Array.isArray(value)) throw new Error("The update signature shape is invalid.");
  const sidecar = value as Record<string, unknown>;
  const keys = Object.keys(sidecar).sort();
  if (keys.length !== expectedProperties.length || keys.some((key, index) => key !== expectedProperties[index])) throw new Error("The update signature shape is invalid.");
  const typed = sidecar as Sidecar;
  if (typed.schemaVersion !== 1 || typed.algorithm !== algorithm || typed.keyId !== keyId) throw new Error("The update signature contract is not trusted.");
  if (!/^[0-9a-f]{64}$/.test(typed.manifestSha256) || typed.manifestSha256 !== sha256Hex(manifest)) throw new Error("The update manifest digest does not match its signature.");
  if (typeof typed.signature !== "string") throw new Error("The update signature value is invalid.");
  const signature = derToP1363(decodeBase64(typed.signature));
  if (!globalThis.crypto?.subtle) throw new Error("Native update signature verification is unavailable.");
  const publicKeyBytes = decodeBase64(publicKeyBase64);
  const publicKey = await crypto.subtle.importKey(
    "spki",
    publicKeyBytes.buffer as ArrayBuffer,
    { name: "ECDSA", namedCurve: "P-256" },
    false,
    ["verify"],
  );
  const manifestBuffer = manifest.slice().buffer as ArrayBuffer;
  const verified = await crypto.subtle.verify({ name: "ECDSA", hash: "SHA-256" }, publicKey, signature.buffer as ArrayBuffer, manifestBuffer);
  if (!verified) throw new Error("The update manifest signature is invalid.");
}
