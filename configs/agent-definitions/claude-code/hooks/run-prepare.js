#!/usr/bin/env node
const fs = require("node:fs");
const path = require("node:path");

function ensureSkipDangerousModePermissionPrompt(settingsPath) {
  let settings = {};
  if (fs.existsSync(settingsPath)) {
    settings = JSON.parse(fs.readFileSync(settingsPath, "utf8"));
  }
  settings.skipDangerousModePermissionPrompt = true;
  fs.writeFileSync(settingsPath, JSON.stringify(settings, null, 2) + "\n", "utf8");
}

const ctx = JSON.parse(fs.readFileSync(process.env.AGENT_CONTEXT_JSON, "utf8"));
const run = ctx.run;
const cfg = ctx.config.input;
const paths = ctx.paths;
const install = ctx.install || {};
const runHome = paths.run_home;
const claudeDir = path.join(runHome, ".claude");
fs.mkdirSync(claudeDir, { recursive: true });
const binPath = install.bin_path || path.join(paths.install_dir, "bin", "claude");

const settingsPath = path.join(claudeDir, "settings.json");
fs.writeFileSync(settingsPath, cfg.settings_json, "utf8");
ensureSkipDangerousModePermissionPrompt(settingsPath);

const env = { ...run.env, DISABLE_AUTOUPDATER: "1", CLAUDE_CONFIG_DIR: claudeDir };
const apiKey = String(env.ANTHROPIC_API_KEY || "").trim();
const claudeState = {};
const mcpJSON = String(cfg.mcp_json || "").trim();
if (mcpJSON) {
  Object.assign(claudeState, JSON.parse(mcpJSON));
}
claudeState.hasCompletedOnboarding = true;
if (apiKey) {
  claudeState.customApiKeyResponses = { approved: [apiKey.slice(-20)], rejected: [] };
}
fs.writeFileSync(path.join(claudeDir, ".claude.json"), JSON.stringify(claudeState, null, 2) + "\n", "utf8");

const args = [
  "--dangerously-skip-permissions",
  "--session-id",
  run.session_id,
  ...(cfg.startup_args || []),
  ...(cfg.run_args || []),
  "-p",
  run.initial_prompt,
];
process.stdout.write(JSON.stringify({ path: binPath, args, env, dir: run.cwd }) + "\n");
