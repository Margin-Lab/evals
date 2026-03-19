#!/usr/bin/env node
const fs = require("node:fs");
const path = require("node:path");

function ensureSkipDangerousModePermissionPrompt(settingsPath) {
  if (!fs.existsSync(settingsPath)) {
    return;
  }
  const settings = JSON.parse(fs.readFileSync(settingsPath, "utf8"));
  settings.skipDangerousModePermissionPrompt = true;
  fs.writeFileSync(settingsPath, JSON.stringify(settings, null, 2) + "\n", "utf8");
}

const ctx = JSON.parse(fs.readFileSync(process.env.AGENT_CONTEXT_JSON, "utf8"));
const run = ctx.run;
const snapshot = ctx.snapshot || {};
const paths = ctx.paths;
const install = ctx.install || {};
const runHome = paths.run_home;
const binPath = install.bin_path || path.join(paths.install_dir, "bin", "claude");
const env = { ...run.env, DISABLE_AUTOUPDATER: "1", CLAUDE_CONFIG_DIR: path.join(runHome, ".claude") };
ensureSkipDangerousModePermissionPrompt(path.join(runHome, ".claude", "settings.json"));
const sessionId = String(snapshot.session_id || run.session_id || "").trim();
const args = ["--dangerously-skip-permissions"];
if (sessionId) {
  args.push("--resume", sessionId);
} else {
  args.push("-c");
}
args.push("-p", "Repeat your last assistant response exactly and nothing else.");
process.stdout.write(JSON.stringify({ path: binPath, args, env, dir: run.cwd }) + "\n");
