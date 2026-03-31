#!/usr/bin/env node
const fs = require("node:fs");
const path = require("node:path");
const { mergeGeminiSettings, parseJSON } = require("./lib/settings");

function shellQuote(value) {
  const text = String(value);
  if (text.length === 0) {
    return "''";
  }
  if (/^[A-Za-z0-9_@%+=:,./-]+$/.test(text)) {
    return text;
  }
  return `'${text.replace(/'/g, `'\"'\"'`)}'`;
}

const ctx = JSON.parse(fs.readFileSync(process.env.AGENT_CONTEXT_JSON, "utf8"));
const run = ctx.run;
const cfg = ctx.config.input;
const paths = ctx.paths;
const install = ctx.install || {};
const binPath = install.bin_path || path.join(paths.install_dir, "bin", "gemini");
const geminiDir = path.join(paths.run_home, ".gemini");
const oauthCredsPath = path.join(geminiDir, "oauth_creds.json");
const settingsPath = path.join(geminiDir, "settings.json");
const outputPath = path.join(paths.artifacts_dir, "gemini-stream.jsonl");
const stderrPath = path.join(paths.artifacts_dir, "gemini.stderr.log");

fs.mkdirSync(geminiDir, { recursive: true });
const baseSettings = parseJSON(cfg.settings_json, "config.input.settings_json");
const env = { ...run.env };
const hasAPIKey = typeof env.GEMINI_API_KEY === "string" && env.GEMINI_API_KEY.trim() !== "";
const hasOAuthCreds = fs.existsSync(oauthCredsPath);
let selectedAuthType = null;

if (hasAPIKey) {
  selectedAuthType = "gemini-api-key";
} else if (hasOAuthCreds) {
  selectedAuthType = "oauth-personal";
  env.GOOGLE_GENAI_USE_GCA = "true";
}

const runtimeSettings = selectedAuthType
  ? { security: { auth: { selectedType: selectedAuthType } } }
  : {};
const mergedSettings = mergeGeminiSettings(baseSettings, runtimeSettings);
fs.writeFileSync(settingsPath, JSON.stringify(mergedSettings, null, 2) + "\n", "utf8");

const command = [
  shellQuote(binPath),
  ...(cfg.startup_args || []).map(shellQuote),
  "--model",
  shellQuote(cfg.model),
  "--output-format",
  "stream-json",
  "--approval-mode",
  shellQuote(cfg.approval_mode),
  ...(cfg.run_args || []).map(shellQuote),
  "-p",
  shellQuote(run.initial_prompt),
].join(" ");

const shellCommand = [
  "set -euo pipefail",
  `mkdir -p ${shellQuote(path.dirname(outputPath))}`,
  `${command} > >(tee ${shellQuote(outputPath)}) 2> >(tee ${shellQuote(stderrPath)} >&2)`,
].join("\n");

process.stdout.write(JSON.stringify({
  path: "bash",
  args: ["-lc", shellCommand],
  env,
  dir: run.cwd,
}) + "\n");
