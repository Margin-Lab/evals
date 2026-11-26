#!/usr/bin/env node
"use strict";

// Installs agy into the install dir from Google's release archives.
//   agy_version = "latest": ask the release manifest for this platform's newest
//     archive and check its SHA-512.
//   agy_version = "<release>": download that release, and check the SHA-512 in
//     agy_sha512 for this platform when the config has one.

const fs = require("node:fs");
const path = require("node:path");
const {
  ARCHIVE_ENTRY,
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
  writeRootCertificates,
} = require("./lib/agy");

async function main() {
  const ctx = loadContext();
  const input = ((ctx.config || {}).input) || {};
  const installDir = ctx.paths.install_dir;
  const binDir = path.join(installDir, "bin");
  const binPath = path.join(binDir, "agy");
  const stagingDir = path.join(installDir, "staging");
  fs.mkdirSync(binDir, { recursive: true });
  fs.mkdirSync(stagingDir, { recursive: true });

  const platform = detectPlatform();
  const requested = requestedRelease(ctx);
  let url;
  let expectedSHA512 = null;
  if (requested === "latest") {
    const manifest = await fetchManifest(platform);
    url = manifest.url;
    expectedSHA512 = manifest.sha512;
  } else {
    url = releaseArchiveURL(requested, platform);
    expectedSHA512 = ((input.agy_sha512 || {})[platform]) || null;
  }
  const release = releaseFromURL(url);

  const archivePath = path.join(stagingDir, "agy.tar.gz");
  const extractedPath = path.join(stagingDir, ARCHIVE_ENTRY);
  try {
    process.stderr.write(`Downloading agy (${platform}) from ${url}\n`);
    const sha512 = await downloadFile(url, archivePath);
    if (expectedSHA512 && sha512 !== expectedSHA512) {
      throw new Error(`the archive's SHA-512 is ${sha512}, not ${expectedSHA512}`);
    }
    await extractTarGzEntry(archivePath, ARCHIVE_ENTRY, extractedPath);
    fs.chmodSync(extractedPath, 0o755);
    fs.renameSync(extractedPath, binPath);

    const version = probeVersion(binPath);
    const wantedVersion = releaseVersion(requested);
    if (wantedVersion && version !== wantedVersion) {
      throw new Error(`agy --version printed ${version}, not ${wantedVersion}`);
    }
    writeRootCertificates(path.join(installDir, "ca-certificates.pem"));
    const metadata = {
      installed: true,
      bin_path: binPath,
      version,
      install_method: "release_archive",
      release,
      platform,
      url,
      sha512,
    };
    fs.writeFileSync(path.join(installDir, "install.json"), `${JSON.stringify(metadata, null, 2)}\n`);
    process.stdout.write(`${JSON.stringify(metadata)}\n`);
  } finally {
    fs.rmSync(stagingDir, { recursive: true, force: true });
  }
}

main().catch((error) => {
  process.stderr.write(`agy install failed: ${error.message}\n`);
  process.exit(1);
});
