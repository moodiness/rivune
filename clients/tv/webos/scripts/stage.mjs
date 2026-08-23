#!/usr/bin/env node

import { chmod, copyFile, lstat, mkdir, readFile, readdir, rename, rm, utimes, writeFile } from "node:fs/promises";
import { dirname, extname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const scriptDirectory = dirname(fileURLToPath(import.meta.url));
const repositoryRoot = resolve(scriptDirectory, "../../../..");
const platformRoot = resolve(scriptDirectory, "..");
const commonRoot = resolve(process.argv[2] || join(repositoryRoot, "clients/tv/dist/common"));
const outputRoot = resolve(process.argv[3] || join(repositoryRoot, "clients/tv/dist/webos"));
const temporaryRoot = `${outputRoot}.staging-${process.pid}`;
const sourceDate = new Date(Number(process.env.SOURCE_DATE_EPOCH || "1787443200") * 1000);

async function copyTree(source, destination) {
  const sourceStat = await lstat(source);
  if (sourceStat.isSymbolicLink()) {
    throw new Error(`Refusing to package symbolic link: ${source}`);
  }
  if (sourceStat.isDirectory()) {
    await mkdir(destination, { recursive: true, mode: 0o755 });
    const entries = (await readdir(source)).sort((left, right) => left.localeCompare(right, "en"));
    for (const entry of entries) await copyTree(join(source, entry), join(destination, entry));
    await chmod(destination, 0o755);
    await utimes(destination, sourceDate, sourceDate);
    return;
  }
  if (!sourceStat.isFile()) throw new Error(`Unsupported package input: ${source}`);
  await mkdir(dirname(destination), { recursive: true, mode: 0o755 });
  await copyFile(source, destination);
  await chmod(destination, 0o644);
  await utimes(destination, sourceDate, sourceDate);
}

async function resourceFiles(root) {
  const result = [];
  const extensions = new Set([".html", ".js", ".mjs", ".css"]);
  async function visit(directory) {
    const entries = (await readdir(directory, { withFileTypes: true }))
      .sort((left, right) => left.name.localeCompare(right.name, "en"));
    for (const entry of entries) {
      const path = join(directory, entry.name);
      if (entry.isDirectory()) await visit(path);
      else if (entry.isFile() && extensions.has(extname(entry.name).toLowerCase())) result.push(path);
    }
  }
  await visit(root);
  return result;
}

async function rejectRemoteUi(root) {
  const remoteUrl = "(?:https?:)?\\/\\/";
  const remoteElement = new RegExp(`<(?:script|iframe|frame|object|embed|base)\\b[^>]*(?:src|data|href)\\s*=\\s*["']\\s*${remoteUrl}`, "i");
  const remoteStylesheet = new RegExp(`<link\\b(?=[^>]*\\brel\\s*=\\s*["'][^"']*stylesheet)(?=[^>]*\\bhref\\s*=\\s*["']\\s*${remoteUrl})[^>]*>`, "i");
  const remoteImport = new RegExp(`(?:\\bimport(?:Scripts)?\\s*\\(\\s*|\\bimport\\s+|\\bfrom\\s+)["']\\s*${remoteUrl}`, "i");
  const remoteWorker = new RegExp(`new\\s+(?:Shared)?Worker\\s*\\(\\s*["']\\s*${remoteUrl}`, "i");
  const remoteNavigation = new RegExp(`(?:location(?:\\.href)?\\s*=|location\\.(?:assign|replace)\\s*\\()\\s*["']\\s*${remoteUrl}`, "i");
  const remoteDynamicUi = new RegExp(`createElement\\s*\\(\\s*["'](?:script|iframe|frame|object|embed)["']\\s*\\)[\\s\\S]{0,256}?(?:src|data|href)\\s*=\\s*["']\\s*${remoteUrl}`, "i");
  const remoteCss = new RegExp(`(?:@import\\s+(?:url\\s*\\()?|url\\s*\\()\\s*["']?\\s*${remoteUrl}`, "i");
  for (const resourcePath of await resourceFiles(root)) {
    const source = await readFile(resourcePath, "utf8");
    const extension = extname(resourcePath).toLowerCase();
    const rejected = remoteElement.test(source) || remoteStylesheet.test(source) ||
      remoteImport.test(source) || remoteWorker.test(source) || remoteNavigation.test(source) ||
      remoteDynamicUi.test(source) || (extension === ".css" && remoteCss.test(source));
    if (rejected) throw new Error(`Remote UI resources are forbidden in packaged TV code: ${resourcePath}`);
  }
}

async function hardenIndex(root) {
  const indexPath = join(root, "index.html");
  const html = await readFile(indexPath, "utf8");
  const platformMarker = "<!-- RIVUNE_PLATFORM_BOOTSTRAP -->";
  const runtimeScript = '<script src="./Rivune-TV-runtime.js"></script>';
  const updaterScript = '<script src="./updater.js"></script>';
  if (!html.includes(platformMarker) || !html.includes(runtimeScript) || !html.includes(updaterScript) || html.indexOf(runtimeScript) > html.indexOf(updaterScript)) {
    throw new Error("The TV updater index must load the packaged runtime before the updater bootstrap.");
  }
  const hardened = html
    .replace(platformMarker, "")
    .replace("__RIVUNE_PLATFORM__", "webos");
  if (!hardened.includes("object-src 'none'") || !hardened.includes("frame-src 'none'")) {
    throw new Error("The TV updater Content Security Policy must explicitly disable object and frame UI.");
  }
  await writeFile(indexPath, hardened, "utf8");
  await chmod(indexPath, 0o644);
  await utimes(indexPath, sourceDate, sourceDate);
}

async function stage() {
  const indexPath = join(commonRoot, "index.html");
  const indexStat = await lstat(indexPath).catch(() => null);
  if (!indexStat?.isFile()) throw new Error(`Shared TV build is missing index.html: ${indexPath}`);

  await rm(temporaryRoot, { recursive: true, force: true });
  await mkdir(temporaryRoot, { recursive: true, mode: 0o755 });
  try {
    await copyTree(commonRoot, temporaryRoot);
    await copyTree(join(platformRoot, "appinfo.json"), join(temporaryRoot, "appinfo.json"));
    await copyTree(join(platformRoot, "assets/icon.png"), join(temporaryRoot, "icon.png"));
    await copyTree(join(platformRoot, "assets/largeIcon.png"), join(temporaryRoot, "largeIcon.png"));
    await copyTree(join(platformRoot, "assets/background.png"), join(temporaryRoot, "background.png"));
    await copyTree(join(platformRoot, "assets/splash.png"), join(temporaryRoot, "splash.png"));
    await hardenIndex(temporaryRoot);
    await rejectRemoteUi(temporaryRoot);
    await rm(outputRoot, { recursive: true, force: true });
    await mkdir(dirname(outputRoot), { recursive: true, mode: 0o755 });
    await rename(temporaryRoot, outputRoot);
  } catch (error) {
    await rm(temporaryRoot, { recursive: true, force: true });
    throw error;
  }
}

await stage();
