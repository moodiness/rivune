import { describe, expect, it } from "vitest";
import { decodeUtf8, sha256Hex } from "./bytes";

describe("TV runtime bytes", () => {
  it("computes SHA-256 without requiring secure-context Web Crypto", () => {
    expect(sha256Hex(new TextEncoder().encode("abc"))).toBe("ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad");
  });

  it("strictly decodes multilingual UTF-8 and rejects malformed input", () => {
    const text = "Rivune · télévision · 日本語";
    expect(decodeUtf8(new TextEncoder().encode(text))).toBe(text);
    expect(() => decodeUtf8(new Uint8Array([0xe2, 0x28, 0xa1]))).toThrow();
    expect(() => decodeUtf8(new Uint8Array([0xc0, 0x80]))).toThrow();
  });
});
