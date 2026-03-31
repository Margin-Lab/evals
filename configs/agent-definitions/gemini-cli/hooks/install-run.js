#!/usr/bin/env node
const fs = require("node:fs");
const path = require("node:path");
const { spawnSync } = require("node:child_process");

function loadContext() {
  return JSON.parse(fs.readFileSync(process.env.AGENT_CONTEXT_JSON, "utf8"));
}

function detectVersion(binPath) {
  const result = spawnSync(binPath, ["--version"], { encoding: "utf8" });
  if (result.status !== 0) {
    throw new Error(result.stderr || result.stdout || "version probe failed");
  }
  return String(result.stdout || "").trim().split(/\r?\n/, 1)[0].trim();
}

const ctx = loadContext();
const installDir = ctx.paths.install_dir;
fs.mkdirSync(installDir, { recursive: true });
const requested = String((((ctx.config || {}).input || {}).gemini_version || "latest")).trim();
let pkg = "@google/gemini-cli";
if (requested && requested !== "latest") {
  pkg += `@${requested}`;
}

const install = spawnSync("npm", ["install", "--global", "--prefix", installDir, pkg], {
  stdio: ["ignore", "ignore", "pipe"],
  encoding: "utf8",
});
if (install.stdout) {
  process.stderr.write(install.stdout);
}
if (install.stderr) {
  process.stderr.write(install.stderr);
}
if (install.status !== 0) {
  process.exit(install.status || 1);
}

const binPath = path.join(installDir, "bin", "gemini");
const version = detectVersion(binPath);
process.stdout.write(JSON.stringify({
  installed: true,
  bin_path: binPath,
  version,
  install_method: "npm",
  package: "@google/gemini-cli",
}) + "\n");
