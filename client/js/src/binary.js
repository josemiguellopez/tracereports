import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { createHash } from "node:crypto";
import { gunzipSync, inflateRawSync } from "node:zlib";
import { setTimeout as sleep } from "node:timers/promises";
import { env } from "./env.js";

export const REPORT_VERSION = "0.2.0";
const RELEASES = "https://github.com/josemiguellopez/tracereports/releases/download";
const MAX_BINARY = 256 * 1024 * 1024;
const STALE_LOCK_MS = 300_000;
const sha = data => createHash("sha256").update(data).digest("hex");

// Read only the exact executable member; archive paths are never used as output paths.
function executable(archive, name, windows) {
  let found;
  const accept = data => { if (found || !data.length || data.length > MAX_BINARY) throw Error("invalid release executable"); found = data; };
  if (windows) {
    let end = archive.length - 22;
    while (end >= Math.max(0, archive.length - 65557) && archive.readUInt32LE(end) !== 0x06054b50) end--;
    if (end < 0 || archive.readUInt32LE(end) !== 0x06054b50) throw Error("invalid ZIP");
    let offset = archive.readUInt32LE(end + 16);
    for (let i = 0; i < archive.readUInt16LE(end + 10); i++) {
      if (archive.readUInt32LE(offset) !== 0x02014b50) throw Error("invalid ZIP directory");
      const length = archive.readUInt16LE(offset + 28);
      if (archive.toString("utf8", offset + 46, offset + 46 + length) === name) {
        const method = archive.readUInt16LE(offset + 10), size = archive.readUInt32LE(offset + 20);
        const local = archive.readUInt32LE(offset + 42);
        if (archive.readUInt32LE(local) !== 0x04034b50 || archive.readUInt32LE(offset + 24) > MAX_BINARY) throw Error("invalid ZIP executable");
        const start = local + 30 + archive.readUInt16LE(local + 26) + archive.readUInt16LE(local + 28);
        if (start + size > archive.length) throw Error("truncated ZIP");
        const compressed = archive.subarray(start, start + size);
        if (![0, 8].includes(method)) throw Error("unsupported ZIP compression");
        accept(method === 0 ? compressed : inflateRawSync(compressed, { maxOutputLength: MAX_BINARY }));
      }
      offset += 46 + length + archive.readUInt16LE(offset + 30) + archive.readUInt16LE(offset + 32);
    }
  } else {
    const tar = gunzipSync(archive, { maxOutputLength: MAX_BINARY + 8 * 1024 * 1024 });
    for (let offset = 0; offset + 512 <= tar.length; ) {
      const header = tar.subarray(offset, offset + 512);
      if (header.every(b => b === 0)) break;
      const member = header.toString("utf8", 0, 100).split("\0")[0];
      const size = parseInt(header.toString("ascii", 124, 136).replace(/\0/g, "").trim(), 8);
      if (!Number.isSafeInteger(size) || size < 0 || offset + 512 + size > tar.length) throw Error("invalid TAR member");
      if (member === name && [0, 48].includes(header[156]) && header[345] === 0) accept(tar.subarray(offset + 512, offset + 512 + size));
      offset += 512 + Math.ceil(size / 512) * 512;
    }
  }
  if (!found) throw Error("release executable missing");
  return found;
}

export async function downloadBinary(timeoutMs = 60_000) {
  const system = { win32: "windows", linux: "linux", darwin: "darwin" }[process.platform];
  const arch = { x64: "amd64", arm64: "arm64" }[process.arch];
  if (!system || !arch) throw Error("unsupported report platform");
  const base = (system === "windows" ? process.env.LOCALAPPDATA : system === "darwin" ? "" : process.env.XDG_CACHE_HOME) ||
    (system === "darwin" ? path.join(os.homedir(), "Library", "Caches") : path.join(os.homedir(), ".cache"));
  const parent = path.join(base, "tracereports", REPORT_VERSION), target = path.join(parent, `${system}_${arch}`);
  const name = system === "windows" ? "tracereports.exe" : "tracereports", binary = path.join(target, name);
  const cached = () => {
    try { return fs.lstatSync(binary).isFile() && sha(fs.readFileSync(binary)) === fs.readFileSync(path.join(target, "binary.sha256"), "utf8").trim(); }
    catch { return false; }
  };
  if (cached()) return binary;
  if (env("BIN_DOWNLOAD", "1") === "0") throw Error("binary download disabled");
  const deadline = Date.now() + timeoutMs, lock = path.join(parent, `${system}_${arch}.lock`);
  fs.mkdirSync(parent, { recursive: true });
  while (true) {
    if (cached()) return binary;
    if (Date.now() >= deadline) throw Error("waiting for report binary cache timed out");
    try { fs.mkdirSync(lock); break; }
    catch (err) {
      if (err.code !== "EEXIST") throw err;
      // A download holds the lock for at most its timeout: an older one was left by a process that
      // died mid-download and would otherwise block every later run.
      try { if (Date.now() - fs.statSync(lock).mtimeMs > STALE_LOCK_MS) { fs.rmdirSync(lock); continue; } } catch { /* removed meanwhile */ }
      await sleep(50);
    }
  }
  let staging;
  const controller = new AbortController(), timer = setTimeout(() => controller.abort(), Math.max(1, deadline - Date.now()));
  try {
    if (cached()) return binary;
    const baseUrl = env("BIN_BASE_URL") || RELEASES;
    if (baseUrl !== RELEASES) {
      const url = new URL(baseUrl);
      if (url.protocol !== "http:" || !["127.0.0.1", "localhost", "[::1]"].includes(url.hostname) || url.username || url.password) throw Error("BIN_BASE_URL must be a loopback test server");
    }
    const asset = `tracereports_${REPORT_VERSION}_${system}_${arch}.${system === "windows" ? "zip" : "tar.gz"}`;
    const download = async (filename, limit) => {
      const response = await fetch(`${baseUrl.replace(/\/$/, "")}/v${REPORT_VERSION}/${filename}`, { signal: controller.signal });
      if (!response.ok) { await response.body?.cancel(); throw Error(`release download HTTP ${response.status}`); }
      const chunks = []; let size = 0;
      for await (const chunk of response.body) {
        size += chunk.length;
        if (size > limit) throw Error("release download too large");
        chunks.push(chunk);
      }
      return Buffer.concat(chunks);
    };
    const checksums = (await download("checksums.txt", 1024 * 1024)).toString("utf8");
    const hashes = checksums.split(/\r?\n/).map(line => line.trim().split(/\s+/)).filter(p => p.length === 2 && p[1].replace(/^\*/, "") === asset).map(p => p[0].toLowerCase());
    if (hashes.length !== 1 || !/^[a-f0-9]{64}$/.test(hashes[0])) throw Error("release checksum missing or ambiguous");
    const archive = await download(asset, 128 * 1024 * 1024);
    if (sha(archive) !== hashes[0]) throw Error("release checksum mismatch");
    const data = executable(archive, name, system === "windows");
    fs.mkdirSync(target, { recursive: true });
    staging = fs.mkdtempSync(path.join(parent, ".download-"));
    fs.writeFileSync(path.join(staging, name), data, { mode: 0o755 });
    fs.writeFileSync(path.join(staging, "binary.sha256"), sha(data));
    // Other processes wait on the lock while the two cache files are replaced.
    fs.rmSync(binary, { force: true });
    fs.rmSync(path.join(target, "binary.sha256"), { force: true });
    fs.renameSync(path.join(staging, "binary.sha256"), path.join(target, "binary.sha256"));
    fs.renameSync(path.join(staging, name), binary);
    return binary;
  } finally {
    clearTimeout(timer);
    if (staging) fs.rmSync(staging, { recursive: true, force: true });
    fs.rmdirSync(lock);
  }
}
