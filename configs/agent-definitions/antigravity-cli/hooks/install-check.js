#!/usr/bin/env node
"use strict";

const fs = require("node:fs");
const path = require("node:path");
const { loadContext, probeVersion, releaseVersion, requestedRelease } = require("./lib/agy");

const ctx = loadContext();
const installDir = ctx.paths.install_dir;
const binPath = path.join(installDir, "bin", "agy");
const requested = requestedRelease(ctx);

function report(value) {
  process.stdout.write(`${JSON.stringify(value)}\n`);
}

if (!fs.existsSync(binPath)) {
  report({ installed: false });
  process.exit(0);
}

let version;
try {
  version = probeVersion(binPath);
} catch {
  report({ installed: false });
  process.exit(0);
}

let installed = requested === "latest" || version === releaseVersion(requested);
let metadata = {};
try {
  metadata = JSON.parse(fs.readFileSync(path.join(installDir, "install.json"), "utf8"));
} catch {
  metadata = {};
}
if (requested !== "latest" && metadata.release && metadata.release !== requested) {
  installed = false;
}

report({
  installed,
  bin_path: binPath,
  version,
  install_method: "release_archive",
  release: metadata.release,
  platform: metadata.platform,
  sha512: metadata.sha512,
});
