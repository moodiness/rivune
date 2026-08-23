#!/usr/bin/env node

import { spawn } from "node:child_process";
import { gzipSync } from "node:zlib";
import { lstat, mkdir, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const platformRoot = dirname(fileURLToPath(import.meta.url));
const tvRoot = resolve(platformRoot, "..");
const repositoryRoot = resolve(tvRoot, "../..");
const commonRoot = join(tvRoot, "dist/bootstrap");
const stagedRoot = join(tvRoot, "dist/webos");
const packagesRoot = join(tvRoot, "dist/packages");
const finalPackage = join(packagesRoot, "Rivune-webOS.ipk");
const sourceDateEpoch = Number(process.env.SOURCE_DATE_EPOCH || "1787443200");

if (!Number.isInteger(sourceDateEpoch) || sourceDateEpoch < 0) {
  throw new Error("SOURCE_DATE_EPOCH must be a non-negative integer.");
}

function optionValue(name) {
  const index = process.argv.indexOf(name);
  if (index >= 0) {
    if (!process.argv[index + 1]) throw new Error(`${name} requires a value.`);
    return process.argv[index + 1];
  }
  const prefix = `${name}=`;
  const inline = process.argv.find((argument) => argument.startsWith(prefix));
  return inline ? inline.slice(prefix.length) : null;
}

const allowedArguments = new Set(["--stage-only"]);
for (let index = 2; index < process.argv.length; index += 1) {
  const argument = process.argv[index];
  if (argument === "--version") { index += 1; continue; }
  if (argument.startsWith("--version=") || allowedArguments.has(argument)) continue;
  throw new Error(`Unknown argument: ${argument}`);
}

const packageMetadata = JSON.parse(await readFile(join(tvRoot, "package.json"), "utf8"));
const version = optionValue("--version") || packageMetadata.version;
if (!/^\d+\.\d+\.\d+$/.test(version)) throw new Error(`Invalid release version: ${version}`);
const appInfo = JSON.parse(await readFile(join(platformRoot, "appinfo.json"), "utf8"));
if (appInfo.id !== "io.rivune.app.webos") throw new Error(`Unexpected webOS application id: ${appInfo.id}`);
if (appInfo.version !== version) {
  throw new Error(`webOS appinfo version ${appInfo.version} does not match release version ${version}.`);
}

function run(command, args) {
  return new Promise((resolveRun, rejectRun) => {
    const child = spawn(command, args, { cwd: repositoryRoot, env: process.env, stdio: "inherit" });
    child.on("error", rejectRun);
    child.on("exit", (code, signal) => {
      if (code === 0) resolveRun();
      else rejectRun(new Error(`${command} failed${signal ? ` with ${signal}` : ` with exit code ${code}`}.`));
    });
  });
}

function writeString(buffer, offset, length, value) {
  const encoded = Buffer.from(value, "utf8");
  if (encoded.length > length) throw new Error(`Archive field is too long: ${value}`);
  encoded.copy(buffer, offset);
}

function writeOctal(buffer, offset, length, value) {
  const octal = value.toString(8);
  if (octal.length > length - 1) throw new Error(`Archive numeric field is too large: ${value}`);
  writeString(buffer, offset, length, octal.padStart(length - 1, "0") + "\0");
}

function splitTarPath(path) {
  if (Buffer.byteLength(path) <= 100) return { name: path, prefix: "" };
  for (let index = path.lastIndexOf("/"); index > 0; index = path.lastIndexOf("/", index - 1)) {
    const prefix = path.slice(0, index);
    const name = path.slice(index + 1);
    if (Buffer.byteLength(prefix) <= 155 && Buffer.byteLength(name) <= 100) return { name, prefix };
  }
  throw new Error(`Package path exceeds the ustar limit: ${path}`);
}

function tarHeader(path, size, mode, type) {
  const header = Buffer.alloc(512);
  const split = splitTarPath(path);
  writeString(header, 0, 100, split.name);
  writeOctal(header, 100, 8, mode);
  writeOctal(header, 108, 8, 0);
  writeOctal(header, 116, 8, 0);
  writeOctal(header, 124, 12, size);
  writeOctal(header, 136, 12, sourceDateEpoch);
  header.fill(0x20, 148, 156);
  writeString(header, 156, 1, type);
  writeString(header, 257, 6, "ustar\0");
  writeString(header, 263, 2, "00");
  writeString(header, 265, 32, "root");
  writeString(header, 297, 32, "root");
  writeString(header, 345, 155, split.prefix);
  let checksum = 0;
  for (const byte of header) checksum += byte;
  writeString(header, 148, 8, checksum.toString(8).padStart(6, "0") + "\0 ");
  return header;
}

function tarEntry(path, content, mode = 0o644) {
  const body = Buffer.isBuffer(content) ? content : Buffer.from(content);
  const padding = Buffer.alloc((512 - (body.length % 512)) % 512);
  return Buffer.concat([tarHeader(path, body.length, mode, "0"), body, padding]);
}

function tarDirectory(path) {
  return tarHeader(path.endsWith("/") ? path : `${path}/`, 0, 0o755, "5");
}

async function stagedTar() {
  const entries = [];
  const applicationRoot = `./usr/palm/applications/${appInfo.id}`;
  entries.push(tarDirectory("./usr"));
  entries.push(tarDirectory("./usr/palm"));
  entries.push(tarDirectory("./usr/palm/applications"));
  entries.push(tarDirectory(applicationRoot));
  async function visit(directory, relativeDirectory) {
    const children = (await readdir(directory, { withFileTypes: true }))
      .sort((left, right) => left.name.localeCompare(right.name, "en"));
    for (const child of children) {
      const source = join(directory, child.name);
      const relative = relativeDirectory ? `${relativeDirectory}/${child.name}` : child.name;
      const archivePath = `${applicationRoot}/${relative}`;
      const stat = await lstat(source);
      if (stat.isSymbolicLink()) throw new Error(`Refusing to package symbolic link: ${source}`);
      if (stat.isDirectory()) {
        entries.push(tarDirectory(archivePath));
        await visit(source, relative);
      } else if (stat.isFile()) {
        entries.push(tarEntry(archivePath, await readFile(source)));
      } else {
        throw new Error(`Unsupported staged package input: ${source}`);
      }
    }
  }
  await visit(stagedRoot, "");
  entries.push(Buffer.alloc(1024));
  return Buffer.concat(entries);
}

function gzipTar(tar) {
  return gzipSync(tar, { level: 9, mtime: 0 });
}

function arMember(name, content) {
  if (Buffer.byteLength(name) > 15) throw new Error(`ar member name is too long: ${name}`);
  const header = Buffer.from(
    `${(name + "/").padEnd(16, " ")}${String(sourceDateEpoch).padEnd(12, " ")}${"0".padEnd(6, " ")}${"0".padEnd(6, " ")}${"100644".padEnd(8, " ")}${String(content.length).padEnd(10, " ")}\x60\n`,
    "ascii"
  );
  return content.length % 2 === 0 ? Buffer.concat([header, content]) : Buffer.concat([header, content, Buffer.from("\n")]);
}

async function createIpk() {
  const control = [
    `Package: ${appInfo.id}`,
    `Version: ${version}`,
    "Architecture: all",
    "Section: misc",
    "Priority: optional",
    "Maintainer: Rivune",
    "Description: Rivune for LG webOS TV",
    "webOS-Package-Format-Version: 2",
    ""
  ].join("\n");
  const controlTar = Buffer.concat([tarEntry("./control", control), Buffer.alloc(1024)]);
  const members = [
    arMember("debian-binary", Buffer.from("2.0\n")),
    arMember("control.tar.gz", gzipTar(controlTar)),
    arMember("data.tar.gz", gzipTar(await stagedTar()))
  ];
  await mkdir(packagesRoot, { recursive: true });
  await rm(finalPackage, { force: true });
  await writeFile(finalPackage, Buffer.concat([Buffer.from("!<arch>\n", "ascii"), ...members]), { mode: 0o644 });
}

await run(process.execPath, [join(platformRoot, "scripts/stage.mjs"), commonRoot, stagedRoot]);
if (!process.argv.includes("--stage-only")) await createIpk();
