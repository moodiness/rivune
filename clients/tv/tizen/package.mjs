import { spawnSync } from "node:child_process";
import { mkdtemp, mkdir, readFile, readdir, rename, rm, stat, utimes, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, dirname, join, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";

const sourceRoot = dirname(fileURLToPath(import.meta.url));
const tvRoot = resolve(sourceRoot, "..");
const commonRoot = join(tvRoot, "dist", "bootstrap");
const stageRoot = join(tvRoot, "dist", "tizen");
const packagesRoot = join(tvRoot, "dist", "packages");
const outputPath = join(packagesRoot, "Rivune-Tizen.wgt");
const packageJsonPath = join(tvRoot, "package.json");
const FIXED_TIME = new Date("1980-01-01T00:00:00.000Z");
const EXPECTED_APPLICATION_ID = "RivuneTV01.Rivune";
const EXPECTED_PACKAGE_ID = "RivuneTV01";

function parseArguments(argv) {
  const result = { version: null, profile: null, tizenCli: "tizen" };
  for (let index = 0; index < argv.length; index += 1) {
    const argument = argv[index];
    if (argument === "--version" || argument === "--profile" || argument === "--tizen-cli") {
      const value = argv[index + 1];
      if (!value || value.startsWith("--")) throw new Error(`${argument} requires a value.`);
      if (argument === "--version") result.version = value;
      if (argument === "--profile") result.profile = value;
      if (argument === "--tizen-cli") result.tizenCli = value;
      index += 1;
    } else {
      throw new Error(`Unknown argument: ${argument}`);
    }
  }
  return result;
}

async function existingDirectory(path, description) {
  let details;
  try { details = await stat(path); }
  catch { throw new Error(`${description} does not exist: ${path}`); }
  if (!details.isDirectory()) throw new Error(`${description} is not a directory: ${path}`);
}

async function sortedEntries(path) {
  return (await readdir(path, { withFileTypes: true })).sort((left, right) => {
    const leftName = Buffer.from(left.name, "utf8");
    const rightName = Buffer.from(right.name, "utf8");
    return Buffer.compare(leftName, rightName);
  });
}

async function copyTree(source, destination) {
  await mkdir(destination, { recursive: true, mode: 0o755 });
  for (const entry of await sortedEntries(source)) {
    const input = join(source, entry.name);
    const output = join(destination, entry.name);
    if (entry.isSymbolicLink()) throw new Error(`Symbolic links are not permitted in the Tizen package: ${input}`);
    if (entry.isDirectory()) await copyTree(input, output);
    else if (entry.isFile()) await writeFile(output, await readFile(input), { mode: 0o644 });
    else throw new Error(`Unsupported package input: ${input}`);
  }
}

async function copyFile(source, destination) {
  await writeFile(destination, await readFile(source), { mode: 0o644 });
}

function attributeValues(html, attribute) {
  const values = [];
  const expression = new RegExp(`\\b${attribute}\\s*=\\s*["']([^"']+)["']`, "gi");
  let match;
  while ((match = expression.exec(html)) !== null) values.push(match[1]);
  return values;
}

function validateLocalApplication(indexHtml) {
  const platformIndex = indexHtml.indexOf("<!-- RIVUNE_PLATFORM_BOOTSTRAP -->");
  const runtimeIndex = indexHtml.indexOf("./Rivune-TV-runtime.js");
  const updaterIndex = indexHtml.indexOf("./updater.js");
  if (platformIndex < 0) throw new Error("The TV updater index must contain the platform bootstrap marker.");
  if (runtimeIndex < 0 || updaterIndex < 0 || runtimeIndex > updaterIndex) throw new Error("The packaged TV runtime must load before the updater bootstrap.");
  const remoteResources = attributeValues(indexHtml, "src")
    .concat(attributeValues(indexHtml, "href"))
    .filter((value) => /^(?:https?:)?\/\//i.test(value));
  if (remoteResources.length > 0) throw new Error(`Remote UI resources are not permitted in the packaged client: ${remoteResources.join(", ")}`);
  if (/<iframe\b/i.test(indexHtml)) throw new Error("Remote or embedded UI frames are not permitted in the packaged client.");
  if (/<meta\b[^>]*http-equiv\s*=\s*["']refresh["']/i.test(indexHtml)) throw new Error("UI redirects are not permitted in the packaged client.");
}

async function validatePackagedApplication(root) {
  for (const file of await packageFiles(root)) {
    const extension = file.name.slice(file.name.lastIndexOf(".")).toLowerCase();
    if (extension !== ".html" && extension !== ".js" && extension !== ".mjs" && extension !== ".css") continue;
    const source = await readFile(file.fullPath, "utf8");
    if (extension === ".html") {
      const remoteResources = attributeValues(source, "src")
        .concat(attributeValues(source, "href"))
        .filter((value) => /^(?:https?:)?\/\//i.test(value));
      if (remoteResources.length > 0 || /<iframe\b/i.test(source) || /<meta\b[^>]*http-equiv\s*=\s*["']refresh["']/i.test(source)) {
        throw new Error(`Remote UI content is not permitted in the packaged client: ${file.name}`);
      }
    }
    if ((extension === ".js" || extension === ".mjs") && (
      /\b(?:import|export)\s+(?:[^;]*?\s+from\s*)?["'](?:https?:)?\/\//i.test(source)
      || /\bimport\s*\(\s*["'](?:https?:)?\/\//i.test(source)
      || /\bimportScripts\s*\(\s*["'](?:https?:)?\/\//i.test(source)
      || /\bcreateElement\s*\(\s*["']iframe["']/i.test(source)
    )) {
      throw new Error(`Remote executable UI code or frames are not permitted in the packaged client: ${file.name}`);
    }
    if (extension === ".css" && (
      /@import\s+(?:url\s*\(\s*)?["']?(?:https?:)?\/\//i.test(source)
      || /url\s*\(\s*["']?(?:https?:)?\/\//i.test(source)
    )) {
      throw new Error(`Remote UI styles or assets are not permitted in the packaged client: ${file.name}`);
    }
  }
}

function addSamsungApiBootstrap(indexHtml) {
  const marker = "<!-- RIVUNE_PLATFORM_BOOTSTRAP -->";
  if (!indexHtml.includes(marker)) throw new Error("The TV updater index must contain the platform bootstrap marker.");
  return indexHtml
    .replace(marker, '<script src="$WEBAPIS/webapis/webapis.js"></script>')
    .replace("__RIVUNE_PLATFORM__", "tizen");
}

function validateManifest(configXml, version) {
  const widget = configXml.match(/<widget\b([^>]*)>/i);
  if (!widget) throw new Error("config.xml does not contain a widget declaration.");
  const id = widget[1].match(/\bid\s*=\s*["']([^"']+)["']/i)?.[1];
  const manifestVersion = widget[1].match(/\bversion\s*=\s*["']([^"']+)["']/i)?.[1];
  const application = configXml.match(/<tizen:application\b([^>]*)\/?\s*>/i);
  const applicationId = application?.[1].match(/\bid\s*=\s*["']([^"']+)["']/i)?.[1];
  const packageId = application?.[1].match(/\bpackage\s*=\s*["']([^"']+)["']/i)?.[1];
  if (!/^https:\/\//i.test(id ?? "") || applicationId !== EXPECTED_APPLICATION_ID || packageId !== EXPECTED_PACKAGE_ID) {
    throw new Error(`config.xml must use application ID ${EXPECTED_APPLICATION_ID} and package ID ${EXPECTED_PACKAGE_ID}.`);
  }
  if (manifestVersion !== version) throw new Error(`config.xml version ${manifestVersion || "(missing)"} does not match package version ${version}.`);
  if (!/<content\b[^>]*\bsrc\s*=\s*["']index\.html["']/i.test(configXml)) throw new Error("config.xml must launch the packaged index.html.");
  if (!/<tizen:profile\b[^>]*\bname\s*=\s*["']tv-samsung["']/i.test(configXml)) throw new Error("config.xml must target the Samsung TV profile.");
}

async function normalizeTimes(path) {
  for (const entry of await sortedEntries(path)) {
    const child = join(path, entry.name);
    if (entry.isDirectory()) await normalizeTimes(child);
    await utimes(child, FIXED_TIME, FIXED_TIME);
  }
  await utimes(path, FIXED_TIME, FIXED_TIME);
}

async function stage(version) {
  await existingDirectory(commonRoot, "Shared TV build output");
  const indexHtml = await readFile(join(commonRoot, "index.html"), "utf8");
  validateLocalApplication(indexHtml);
  const configXml = await readFile(join(sourceRoot, "config.xml"), "utf8");
  validateManifest(configXml, version);

  await mkdir(dirname(stageRoot), { recursive: true });
  const temporaryStage = await mkdtemp(join(dirname(stageRoot), ".tizen-stage-"));
  try {
    await copyTree(commonRoot, temporaryStage);
    await copyFile(join(sourceRoot, "config.xml"), join(temporaryStage, "config.xml"));
    await copyFile(join(sourceRoot, "icon.png"), join(temporaryStage, "icon.png"));
    await copyFile(join(sourceRoot, "icon-512.png"), join(temporaryStage, "icon-512.png"));
    await writeFile(join(temporaryStage, "index.html"), addSamsungApiBootstrap(indexHtml), { mode: 0o644 });
    await validatePackagedApplication(temporaryStage);
    await writeFile(join(temporaryStage, "package-metadata.json"), `${JSON.stringify({
      applicationId: EXPECTED_APPLICATION_ID,
      platform: "tizen",
      signature: "unsigned",
      version
    }, null, 2)}\n`, { mode: 0o644 });
    await normalizeTimes(temporaryStage);
    await rm(stageRoot, { recursive: true, force: true });
    await rename(temporaryStage, stageRoot);
  } catch (error) {
    await rm(temporaryStage, { recursive: true, force: true });
    throw error;
  }
}

const crcTable = new Uint32Array(256);
for (let value = 0; value < 256; value += 1) {
  let remainder = value;
  for (let bit = 0; bit < 8; bit += 1) remainder = (remainder & 1) ? (0xedb88320 ^ (remainder >>> 1)) : (remainder >>> 1);
  crcTable[value] = remainder >>> 0;
}

function crc32(data) {
  let result = 0xffffffff;
  for (const byte of data) result = crcTable[(result ^ byte) & 0xff] ^ (result >>> 8);
  return (result ^ 0xffffffff) >>> 0;
}

async function packageFiles(root, current = root) {
  const files = [];
  for (const entry of await sortedEntries(current)) {
    const fullPath = join(current, entry.name);
    if (entry.isSymbolicLink()) throw new Error(`Symbolic links are not permitted in the Tizen package: ${fullPath}`);
    if (entry.isDirectory()) files.push(...await packageFiles(root, fullPath));
    else if (entry.isFile()) files.push({ fullPath, name: relative(root, fullPath).split(sep).join("/") });
    else throw new Error(`Unsupported package input: ${fullPath}`);
  }
  return files;
}

async function writeUnsignedWgt(root, output) {
  const localParts = [];
  const centralParts = [];
  let offset = 0;
  const files = await packageFiles(root);
  for (const file of files) {
    const name = Buffer.from(file.name, "utf8");
    const data = await readFile(file.fullPath);
    const checksum = crc32(data);
    const local = Buffer.alloc(30);
    local.writeUInt32LE(0x04034b50, 0);
    local.writeUInt16LE(20, 4);
    local.writeUInt16LE(0x0800, 6);
    local.writeUInt16LE(0, 8);
    local.writeUInt16LE(0, 10);
    local.writeUInt16LE(33, 12);
    local.writeUInt32LE(checksum, 14);
    local.writeUInt32LE(data.length, 18);
    local.writeUInt32LE(data.length, 22);
    local.writeUInt16LE(name.length, 26);
    local.writeUInt16LE(0, 28);
    localParts.push(local, name, data);

    const central = Buffer.alloc(46);
    central.writeUInt32LE(0x02014b50, 0);
    central.writeUInt16LE(0x0314, 4);
    central.writeUInt16LE(20, 6);
    central.writeUInt16LE(0x0800, 8);
    central.writeUInt16LE(0, 10);
    central.writeUInt16LE(0, 12);
    central.writeUInt16LE(33, 14);
    central.writeUInt32LE(checksum, 16);
    central.writeUInt32LE(data.length, 20);
    central.writeUInt32LE(data.length, 24);
    central.writeUInt16LE(name.length, 28);
    central.writeUInt16LE(0, 30);
    central.writeUInt16LE(0, 32);
    central.writeUInt16LE(0, 34);
    central.writeUInt16LE(0, 36);
    central.writeUInt32LE((0o100644 << 16) >>> 0, 38);
    central.writeUInt32LE(offset, 42);
    centralParts.push(central, name);
    offset += local.length + name.length + data.length;
  }
  if (files.length > 0xffff) throw new Error("The Tizen package contains too many files for a WGT archive.");
  const centralSize = centralParts.reduce((total, part) => total + part.length, 0);
  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50, 0);
  end.writeUInt16LE(0, 4);
  end.writeUInt16LE(0, 6);
  end.writeUInt16LE(files.length, 8);
  end.writeUInt16LE(files.length, 10);
  end.writeUInt32LE(centralSize, 12);
  end.writeUInt32LE(offset, 16);
  end.writeUInt16LE(0, 20);
  await mkdir(dirname(output), { recursive: true });
  const temporaryOutput = join(dirname(output), `.${basename(output)}.${process.pid}.tmp`);
  await writeFile(temporaryOutput, Buffer.concat([...localParts, ...centralParts, end]), { mode: 0o644 });
  await rename(temporaryOutput, output);
}

async function findPackages(path) {
  const results = [];
  for (const entry of await sortedEntries(path)) {
    const child = join(path, entry.name);
    if (entry.isDirectory()) results.push(...await findPackages(child));
    else if (entry.isFile() && entry.name.toLowerCase().endsWith(".wgt")) results.push(child);
  }
  return results;
}

async function writeSignedWgt(profile, tizenCli, output) {
  const temporaryRoot = await mkdtemp(join(tmpdir(), "rivune-tizen-sign-"));
  const signingStage = join(temporaryRoot, "Rivune-Tizen");
  try {
    await copyTree(stageRoot, signingStage);
    await writeFile(join(signingStage, "package-metadata.json"), `${JSON.stringify({
      applicationId: EXPECTED_APPLICATION_ID,
      platform: "tizen",
      signature: "tizen-certificate-profile",
      version: JSON.parse(await readFile(packageJsonPath, "utf8")).version
    }, null, 2)}\n`, { mode: 0o644 });
    const result = spawnSync(tizenCli, ["package", "-t", "wgt", "-s", profile, "--", signingStage], { cwd: temporaryRoot, stdio: "inherit" });
    if (result.error) throw result.error;
    if (result.status !== 0) throw new Error(`Tizen CLI exited with status ${String(result.status)}.`);
    const packages = await findPackages(temporaryRoot);
    if (packages.length !== 1) throw new Error(`Tizen CLI produced ${packages.length} WGT files; expected exactly one.`);
    await mkdir(dirname(output), { recursive: true });
    const temporaryOutput = join(dirname(output), `.${basename(output)}.${process.pid}.tmp`);
    await copyFile(packages[0], temporaryOutput);
    await rename(temporaryOutput, output);
  } finally {
    await rm(temporaryRoot, { recursive: true, force: true });
  }
}

const options = parseArguments(process.argv.slice(2));
const packageJson = JSON.parse(await readFile(packageJsonPath, "utf8"));
const version = options.version || packageJson.version;
if (!/^\d+\.\d+\.\d+$/.test(version)) throw new Error(`Invalid release version: ${version}`);
if (version !== packageJson.version) throw new Error(`Requested version ${version} does not match ${packageJsonPath} version ${packageJson.version}.`);
await stage(version);
if (options.profile) await writeSignedWgt(options.profile, options.tizenCli, outputPath);
else await writeUnsignedWgt(stageRoot, outputPath);
console.log(`${options.profile ? "Signed" : "Unsigned deterministic"} Tizen package: ${outputPath}`);
