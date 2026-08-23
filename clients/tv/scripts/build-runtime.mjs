#!/usr/bin/env node

import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const tvRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const packageMetadata = JSON.parse(await readFile(join(tvRoot, "package.json"), "utf8"));
if (!/^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/.test(packageMetadata.version)) {
  throw new Error(`Invalid TV runtime version: ${String(packageMetadata.version)}`);
}
const partsRoot = join(tvRoot, "dist/runtime-parts");
const bootstrapRoot = join(tvRoot, "dist/bootstrap");
const packagesRoot = join(tvRoot, "dist/packages");
const applicationJavaScript = await readFile(join(partsRoot, "application.iife.js"), "utf8");
const applicationCss = await readFile(join(partsRoot, "application.css"), "utf8");
const webosJavaScript = await readFile(join(tvRoot, "webos/platform.js"), "utf8");
const tizenJavaScript = await readFile(join(tvRoot, "tizen/platform.js"), "utf8");
for (const [name, source] of Object.entries({ applicationJavaScript, applicationCss, webosJavaScript, tizenJavaScript })) {
  if (source.length === 0) throw new Error(`The generated TV runtime part is empty: ${name}`);
}
const runtime = {
  schemaVersion: 1,
  version: packageMetadata.version,
  application: {
    javascript: applicationJavaScript,
    css: applicationCss,
  },
  platforms: {
    webos: { javascript: webosJavaScript },
    tizen: { javascript: tizenJavaScript },
  },
};
const serialized = `${JSON.stringify(runtime)}\n`;
if (Buffer.byteLength(serialized) > 16 * 1024 * 1024) throw new Error("The generated TV runtime exceeds 16 MiB.");
const executable = `window.RivunePackagedRuntime=${JSON.stringify(runtime).replace(/\u2028/g, "\\u2028").replace(/\u2029/g, "\\u2029")};\n`;
await mkdir(bootstrapRoot, { recursive: true });
await mkdir(packagesRoot, { recursive: true });
await writeFile(join(bootstrapRoot, "index.html"), await readFile(join(tvRoot, "updater/index.html")), { mode: 0o644 });
await writeFile(join(bootstrapRoot, "Rivune-TV-runtime.js"), executable, { mode: 0o644 });
await writeFile(join(packagesRoot, "Rivune-TV-runtime.json"), serialized, { mode: 0o644 });
