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

function stripV(value) {
  return String(value || "").trim().replace(/^[vV]/, "");
}

const ctx = loadContext();
const installDir = ctx.paths.install_dir;
const binPath = path.join(installDir, "bin", "opencode");
const requested = String((((ctx.config || {}).input || {}).opencode_version || "latest")).trim();

if (!fs.existsSync(binPath)) {
  process.stdout.write(JSON.stringify({ installed: false }) + "\n");
  process.exit(0);
}

let version;
try {
  version = detectVersion(binPath);
} catch {
  process.stdout.write(JSON.stringify({ installed: false }) + "\n");
  process.exit(0);
}

const installed = requested === "latest" || version === requested || stripV(version) === stripV(requested);
process.stdout.write(JSON.stringify({
  installed,
  bin_path: binPath,
  version,
  install_method: "npm",
  package: "opencode-ai",
}) + "\n");
