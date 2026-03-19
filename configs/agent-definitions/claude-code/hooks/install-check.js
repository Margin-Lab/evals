#!/usr/bin/env node
const fs = require("node:fs");
const path = require("node:path");
const { spawnSync } = require("node:child_process");

const versionTokenRegex = /\bv?([0-9]+(?:\.[0-9]+){1,3}(?:[-+][0-9A-Za-z.-]+)?)\b/i;

function loadContext() {
  return JSON.parse(fs.readFileSync(process.env.AGENT_CONTEXT_JSON, "utf8"));
}

function normalizeVersion(raw) {
  const text = String(raw || "").trim();
  if (!text) {
    return "";
  }
  const match = text.match(versionTokenRegex);
  if (match) {
    return match[1];
  }
  return text.replace(/^[vV]/, "");
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
const binPath = path.join(installDir, "bin", "claude");
const requested = normalizeVersion((((ctx.config || {}).input || {}).claude_version || "latest"));

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

const normalizedVersion = normalizeVersion(version);
const installed = requested === "" || requested === "latest" || normalizedVersion === requested;
process.stdout.write(JSON.stringify({
  installed,
  bin_path: binPath,
  version,
  install_method: "npm",
  package: "@anthropic-ai/claude-code",
}) + "\n");
