const roundConstants = new Uint32Array([
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
  0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
  0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
  0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
  0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
  0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
]);

function rotateRight(value: number, bits: number): number {
  return (value >>> bits) | (value << (32 - bits));
}

export function sha256Hex(input: Uint8Array): string {
  const hash = new Uint32Array([
    0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a,
    0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19,
  ]);
  const words = new Uint32Array(64);
  const paddedLength = Math.ceil((input.length + 9) / 64) * 64;
  const bitLengthLow = (input.length << 3) >>> 0;
  const bitLengthHigh = Math.floor(input.length / 0x20000000) >>> 0;

  function paddedByte(index: number): number {
    if (index < input.length) return input[index];
    if (index === input.length) return 0x80;
    if (index >= paddedLength - 8) {
      const shift = (paddedLength - 1 - index) * 8;
      return shift >= 32 ? (bitLengthHigh >>> (shift - 32)) & 0xff : (bitLengthLow >>> shift) & 0xff;
    }
    return 0;
  }

  for (let offset = 0; offset < paddedLength; offset += 64) {
    for (let index = 0; index < 16; index += 1) {
      const byte = offset + index * 4;
      words[index] = (
        (paddedByte(byte) << 24) |
        (paddedByte(byte + 1) << 16) |
        (paddedByte(byte + 2) << 8) |
        paddedByte(byte + 3)
      ) >>> 0;
    }
    for (let index = 16; index < 64; index += 1) {
      const first = words[index - 15];
      const second = words[index - 2];
      const sigma0 = rotateRight(first, 7) ^ rotateRight(first, 18) ^ (first >>> 3);
      const sigma1 = rotateRight(second, 17) ^ rotateRight(second, 19) ^ (second >>> 10);
      words[index] = (words[index - 16] + sigma0 + words[index - 7] + sigma1) >>> 0;
    }

    let a = hash[0];
    let b = hash[1];
    let c = hash[2];
    let d = hash[3];
    let e = hash[4];
    let f = hash[5];
    let g = hash[6];
    let h = hash[7];
    for (let index = 0; index < 64; index += 1) {
      const sum1 = rotateRight(e, 6) ^ rotateRight(e, 11) ^ rotateRight(e, 25);
      const choice = (e & f) ^ (~e & g);
      const temporary1 = (h + sum1 + choice + roundConstants[index] + words[index]) >>> 0;
      const sum0 = rotateRight(a, 2) ^ rotateRight(a, 13) ^ rotateRight(a, 22);
      const majority = (a & b) ^ (a & c) ^ (b & c);
      const temporary2 = (sum0 + majority) >>> 0;
      h = g;
      g = f;
      f = e;
      e = (d + temporary1) >>> 0;
      d = c;
      c = b;
      b = a;
      a = (temporary1 + temporary2) >>> 0;
    }
    hash[0] = (hash[0] + a) >>> 0;
    hash[1] = (hash[1] + b) >>> 0;
    hash[2] = (hash[2] + c) >>> 0;
    hash[3] = (hash[3] + d) >>> 0;
    hash[4] = (hash[4] + e) >>> 0;
    hash[5] = (hash[5] + f) >>> 0;
    hash[6] = (hash[6] + g) >>> 0;
    hash[7] = (hash[7] + h) >>> 0;
  }
  return Array.from(hash, (value) => value.toString(16).padStart(8, "0")).join("");
}

export function decodeUtf8(input: Uint8Array): string {
  const parts: string[] = [];
  const units: number[] = [];
  const flush = () => {
    if (units.length === 0) return;
    parts.push(String.fromCharCode(...units));
    units.length = 0;
  };
  const push = (value: number) => {
    units.push(value);
    if (units.length >= 4096) flush();
  };
  for (let index = 0; index < input.length;) {
    const first = input[index++];
    if (first <= 0x7f) {
      push(first);
      continue;
    }
    let codePoint: number;
    let continuationCount: number;
    let minimum: number;
    if (first >= 0xc2 && first <= 0xdf) {
      codePoint = first & 0x1f;
      continuationCount = 1;
      minimum = 0x80;
    } else if (first >= 0xe0 && first <= 0xef) {
      codePoint = first & 0x0f;
      continuationCount = 2;
      minimum = 0x800;
    } else if (first >= 0xf0 && first <= 0xf4) {
      codePoint = first & 0x07;
      continuationCount = 3;
      minimum = 0x10000;
    } else {
      throw new Error("The downloaded TV runtime is not valid UTF-8.");
    }
    if (index + continuationCount > input.length) throw new Error("The downloaded TV runtime is not valid UTF-8.");
    for (let offset = 0; offset < continuationCount; offset += 1) {
      const continuation = input[index++];
      if ((continuation & 0xc0) !== 0x80) throw new Error("The downloaded TV runtime is not valid UTF-8.");
      codePoint = (codePoint << 6) | (continuation & 0x3f);
    }
    if (codePoint < minimum || codePoint > 0x10ffff || (codePoint >= 0xd800 && codePoint <= 0xdfff)) {
      throw new Error("The downloaded TV runtime is not valid UTF-8.");
    }
    if (codePoint <= 0xffff) push(codePoint);
    else {
      const scalar = codePoint - 0x10000;
      push(0xd800 + (scalar >>> 10));
      push(0xdc00 + (scalar & 0x3ff));
    }
  }
  flush();
  return parts.join("");
}
