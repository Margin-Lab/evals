#!/usr/bin/env node
"use strict";

// Installs and probes the Antigravity CLI (agy), Google's native coding-agent binary.
// Releases come from Google's own installer endpoints: a manifest per platform names
// the latest archive and its SHA-512, and every release keeps its archives under
// storage.googleapis.com/antigravity-public/antigravity-cli/<release>/.

const crypto = require("node:crypto");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const zlib = require("node:zlib");
const { spawnSync } = require("node:child_process");

const MANIFEST_BASE_URL = "https://antigravity-cli-auto-updater-974169037036.us-central1.run.app/manifests";
const RELEASE_BASE_URL = "https://storage.googleapis.com/antigravity-public/antigravity-cli";
const ARCHIVE_ENTRY = "antigravity";
const PLATFORM_ARCHIVES = {
  linux_amd64: "linux-x64/cli_linux_x64.tar.gz",
  linux_amd64_musl: "linux-x64-musl/cli_linux_x64_musl.tar.gz",
  linux_arm64: "linux-arm/cli_linux_arm64.tar.gz",
  linux_arm64_musl: "linux-arm-musl/cli_linux_arm64_musl.tar.gz",
};
const RELEASE_PATTERN = /^(\d+\.\d+\.\d+)-\d+$/;
const REQUEST_TIMEOUT_MS = 10 * 60 * 1000;
const ATTEMPTS = 3;

function loadContext() {
  return JSON.parse(fs.readFileSync(process.env.AGENT_CONTEXT_JSON, "utf8"));
}

function requestedRelease(ctx) {
  return String((((ctx.config || {}).input || {}).agy_version) || "latest").trim() || "latest";
}

// "1.5.18-6386224290725888" -> "1.5.18"; "latest" -> null.
function releaseVersion(release) {
  const match = RELEASE_PATTERN.exec(String(release || "").trim());
  return match ? match[1] : null;
}

function isMusl() {
  if (fs.existsSync("/lib/libc.musl-x86_64.so.1") || fs.existsSync("/lib/libc.musl-aarch64.so.1")) {
    return true;
  }
  const report = process.report && typeof process.report.getReport === "function" ? process.report.getReport() : null;
  const header = report && report.header ? report.header : {};
  return !header.glibcVersionRuntime && process.platform === "linux";
}

function detectPlatform() {
  if (process.platform !== "linux") {
    throw new Error(`agy installs are supported on Linux only, not ${process.platform}`);
  }
  const arch = { x64: "amd64", arm64: "arm64" }[process.arch];
  if (!arch) {
    throw new Error(`agy has no Linux build for ${process.arch}`);
  }
  return `linux_${arch}${isMusl() ? "_musl" : ""}`;
}

function releaseArchiveURL(release, platform) {
  const archive = PLATFORM_ARCHIVES[platform];
  if (!archive) {
    throw new Error(`no agy archive known for platform ${platform}`);
  }
  return `${RELEASE_BASE_URL}/${release}/${archive}`;
}

// The release ID is the URL folder between the base URL and the platform folder.
function releaseFromURL(url) {
  const prefix = `${RELEASE_BASE_URL}/`;
  if (!String(url).startsWith(prefix)) {
    return null;
  }
  const release = String(url).slice(prefix.length).split("/", 1)[0];
  return RELEASE_PATTERN.test(release) ? release : null;
}

async function withAttempts(label, fn) {
  let lastError;
  for (let attempt = 1; attempt <= ATTEMPTS; attempt++) {
    try {
      return await fn();
    } catch (error) {
      lastError = error;
      process.stderr.write(`${label} failed (try ${attempt} of ${ATTEMPTS}): ${error.message}\n`);
      if (attempt < ATTEMPTS) {
        await new Promise((resolve) => setTimeout(resolve, attempt * 5000));
      }
    }
  }
  throw lastError;
}

async function fetchManifest(platform) {
  const url = `${MANIFEST_BASE_URL}/${platform}.json`;
  return withAttempts(`fetching ${url}`, async () => {
    const response = await fetch(url, { signal: AbortSignal.timeout(60 * 1000) });
    if (!response.ok) {
      throw new Error(`HTTP ${response.status}`);
    }
    const manifest = await response.json();
    if (!manifest || typeof manifest.url !== "string" || !/^[0-9a-f]{128}$/.test(String(manifest.sha512 || ""))) {
      throw new Error("the manifest has no archive URL or SHA-512");
    }
    return {
      version: String(manifest.version || "").trim(),
      url: manifest.url,
      sha512: manifest.sha512,
    };
  });
}

// Writes the archive at url to destPath and returns its SHA-512 in hex.
async function downloadFile(url, destPath) {
  return withAttempts(`downloading ${url}`, async () => {
    const response = await fetch(url, { signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS) });
    if (!response.ok || !response.body) {
      throw new Error(`HTTP ${response.status}`);
    }
    const hash = crypto.createHash("sha512");
    const out = fs.createWriteStream(destPath);
    try {
      for await (const chunk of response.body) {
        hash.update(chunk);
        if (!out.write(chunk)) {
          await new Promise((resolve) => out.once("drain", resolve));
        }
      }
    } finally {
      await new Promise((resolve, reject) => out.end((error) => (error ? reject(error) : resolve())));
    }
    return hash.digest("hex");
  });
}

function parseOctal(buffer, start, length) {
  const text = buffer.toString("latin1", start, start + length).replace(/\0.*$/s, "").trim();
  return text ? parseInt(text, 8) : 0;
}

function parseSize(header) {
  // GNU base-256 sizes set the high bit of the first byte.
  if (header[124] & 0x80) {
    let size = 0;
    for (let i = 125; i < 136; i++) {
      size = size * 256 + header[i];
    }
    return size;
  }
  return parseOctal(header, 124, 12);
}

function entryName(header) {
  const name = header.toString("utf8", 0, 100).replace(/\0.*$/s, "");
  const prefix = header.toString("utf8", 345, 500).replace(/\0.*$/s, "");
  return prefix ? `${prefix}/${name}` : name;
}

function closeStream(stream) {
  return new Promise((resolve, reject) => stream.end((error) => (error ? reject(error) : resolve())));
}

// Extracts the regular file whose base name is wanted from a .tar.gz into destPath.
// Handles ustar, GNU long names and pax headers; the agy archive holds one file.
async function extractTarGzEntry(archivePath, wanted, destPath) {
  const source = fs.createReadStream(archivePath);
  const gunzip = zlib.createGunzip();
  source.on("error", (error) => gunzip.destroy(error));
  source.pipe(gunzip);

  let pending = Buffer.alloc(0);
  let entry = null; // { type, remaining, padding, meta, out }
  let nextName = null; // from a preceding GNU long-name or pax header
  let found = false;

  const endEntry = async () => {
    if (entry.out) {
      await closeStream(entry.out);
      found = true;
    }
    if (entry.meta) {
      const text = Buffer.concat(entry.meta).toString("utf8");
      if (entry.type === "L") {
        nextName = text.replace(/\0.*$/s, "");
      } else {
        for (const record of text.split("\n")) {
          const match = /^\d+ path=(.*)$/.exec(record);
          if (match) {
            nextName = match[1];
          }
        }
      }
    }
  };

  try {
    for await (const chunk of gunzip) {
      pending = pending.length ? Buffer.concat([pending, chunk]) : chunk;
      for (;;) {
        if (!entry) {
          if (pending.length < 512) {
            break;
          }
          const header = pending.subarray(0, 512);
          pending = pending.subarray(512);
          if (header.every((byte) => byte === 0)) {
            continue;
          }
          const type = String.fromCharCode(header[156] || 48);
          const size = parseSize(header);
          const name = nextName || entryName(header);
          nextName = null;
          const isMeta = type === "L" || type === "x";
          const isWanted = !found && (type === "0" || type === "\0") &&
            path.posix.basename(name.replace(/\/+$/, "")) === wanted;
          entry = {
            type,
            remaining: size,
            padding: (512 - (size % 512)) % 512,
            meta: isMeta ? [] : null,
            out: isWanted ? fs.createWriteStream(destPath, { mode: 0o755 }) : null,
          };
        }
        if (entry.remaining > 0) {
          if (pending.length === 0) {
            break;
          }
          const take = Math.min(entry.remaining, pending.length);
          const piece = pending.subarray(0, take);
          pending = pending.subarray(take);
          entry.remaining -= take;
          if (entry.meta) {
            entry.meta.push(Buffer.from(piece));
          }
          if (entry.out && !entry.out.write(piece)) {
            await new Promise((resolve) => entry.out.once("drain", resolve));
          }
          if (entry.remaining > 0) {
            continue;
          }
        }
        if (entry.padding > 0) {
          if (pending.length === 0) {
            break;
          }
          const take = Math.min(entry.padding, pending.length);
          pending = pending.subarray(take);
          entry.padding -= take;
          if (entry.padding > 0) {
            continue;
          }
        }
        await endEntry();
        entry = null;
      }
    }
  } finally {
    if (entry && entry.out) {
      entry.out.destroy();
    }
  }
  if (entry && entry.out) {
    throw new Error(`the archive ended inside ${wanted}`);
  }
  if (!found) {
    throw new Error(`the archive has no file named ${wanted}`);
  }
}

// Runs `agy --version` with a throwaway home, so the probe neither reads nor
// writes a real sign-in.
function probeVersion(binPath) {
  const home = fs.mkdtempSync(path.join(os.tmpdir(), "agy-probe-"));
  try {
    const result = spawnSync(binPath, ["--version"], {
      encoding: "utf8",
      env: { PATH: process.env.PATH || "/usr/bin:/bin", HOME: home },
      stdio: ["ignore", "pipe", "pipe"],
      timeout: 120 * 1000,
    });
    if (result.status !== 0) {
      throw new Error((result.stderr || result.stdout || "agy --version failed").trim());
    }
    return String(result.stdout || "").trim().split(/\r?\n/, 1)[0].trim().replace(/^v/i, "");
  } finally {
    fs.rmSync(home, { recursive: true, force: true });
  }
}

// Writes Node's bundled root certificates to destPath. agy reads the system's CA
// bundle, which some task images don't have; run-prepare points SSL_CERT_FILE at
// this file only when no system bundle exists.
function writeRootCertificates(destPath) {
  const tls = require("node:tls");
  fs.writeFileSync(destPath, `${tls.rootCertificates.join("\n")}\n`, { mode: 0o644 });
}

const SYSTEM_CA_BUNDLES = [
  "/etc/ssl/certs/ca-certificates.crt",
  "/etc/pki/tls/certs/ca-bundle.crt",
  "/etc/ssl/ca-bundle.pem",
  "/etc/pki/tls/cacert.pem",
  "/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem",
  "/etc/ssl/cert.pem",
];

function systemCABundleExists() {
  return SYSTEM_CA_BUNDLES.some((candidate) => {
    try {
      return fs.statSync(candidate).size > 0;
    } catch {
      return false;
    }
  });
}

module.exports = {
  ARCHIVE_ENTRY,
  PLATFORM_ARCHIVES,
  detectPlatform,
  downloadFile,
  extractTarGzEntry,
  fetchManifest,
  loadContext,
  probeVersion,
  releaseArchiveURL,
  releaseFromURL,
  releaseVersion,
  requestedRelease,
  systemCABundleExists,
  writeRootCertificates,
};
