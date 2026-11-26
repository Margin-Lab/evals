#!/usr/bin/env node
"use strict";

const fs = require("node:fs");
const path = require("node:path");
const { systemCABundleExists } = require("./lib/agy");
const { deepMerge, parseJSONObject } = require("./lib/settings");

function shellQuote(value) {
  const text = String(value);
  if (text.length === 0) {
    return "''";
  }
  if (/^[A-Za-z0-9_@%+=:,./-]+$/.test(text)) {
    return text;
  }
  return `'${text.replace(/'/g, `'"'"'`)}'`;
}

const ctx = JSON.parse(fs.readFileSync(process.env.AGENT_CONTEXT_JSON, "utf8"));
const run = ctx.run;
const cfg = ctx.config.input;
const paths = ctx.paths;
const install = ctx.install || {};
const binPath = install.bin_path || path.join(paths.install_dir, "bin", "agy");
const agyDir = path.join(paths.run_home, ".gemini", "antigravity-cli");
const tokenPath = path.join(agyDir, "antigravity-oauth-token");
const settingsPath = path.join(agyDir, "settings.json");
const streamPath = path.join(paths.artifacts_dir, "agy-stream.jsonl");
const stderrPath = path.join(paths.artifacts_dir, "agy.stderr.log");
const transcriptsDir = path.join(paths.artifacts_dir, "agy-transcripts");
const caBundlePath = path.join(paths.install_dir, "ca-certificates.pem");

fs.mkdirSync(agyDir, { recursive: true });
fs.mkdirSync(paths.artifacts_dir, { recursive: true });

const env = { ...run.env };
// With no D-Bus session bus agy reads its sign-in from tokenPath, not a keyring.
delete env.DBUS_SESSION_BUS_ADDRESS;
const hasAPIKey = typeof env.GEMINI_API_KEY === "string" && env.GEMINI_API_KEY.trim() !== "";
if (!hasAPIKey && !fs.existsSync(tokenPath)) {
  throw new Error(`no GEMINI_API_KEY and no Antigravity sign-in at ${tokenPath}`);
}
if (!hasAPIKey) {
  delete env.GEMINI_API_KEY;
}

// GEMINI_API_KEY only takes effect with modelProvider "gemini".
const settings = deepMerge(
  parseJSONObject(cfg.settings_json, "config.input.settings_json"),
  hasAPIKey ? { modelProvider: "gemini" } : {},
);
fs.writeFileSync(settingsPath, `${JSON.stringify(settings, null, 2)}\n`, "utf8");

// agy checks for updates in the background when this file is missing or old. The
// task's version is fixed by the install, so mark the check as just done.
fs.writeFileSync(path.join(agyDir, "last_check.timestamp"), "");

if (!env.SSL_CERT_FILE && !systemCABundleExists() && fs.existsSync(caBundlePath)) {
  env.SSL_CERT_FILE = caBundlePath;
}

const agyCommand = [
  shellQuote(binPath),
  ...(cfg.startup_args || []).map(shellQuote),
  "--model",
  shellQuote(cfg.model),
  ...(cfg.effort ? ["--effort", shellQuote(cfg.effort)] : []),
  "--output-format",
  "stream-json",
  "--dangerously-skip-permissions",
  ...(cfg.run_args || []).map(shellQuote),
  "-p",
  shellQuote(run.initial_prompt),
].join(" ");

// stdin is closed: agy -p can wait on an open stdin pipe. After the run, copy the
// conversation transcripts agy keeps on disk next to the event stream.
const shellCommand = [
  "set -uo pipefail",
  `${agyCommand} < /dev/null 2> >(tee ${shellQuote(stderrPath)} >&2) | tee ${shellQuote(streamPath)}`,
  "status=${PIPESTATUS[0]}",
  `for transcript in "$HOME"/.gemini/antigravity-cli/brain/*/.system_generated/logs/transcript.jsonl; do`,
  `  [ -f "$transcript" ] || continue`,
  `  conversation="\${transcript%/.system_generated/logs/transcript.jsonl}"`,
  `  mkdir -p ${shellQuote(transcriptsDir)}`,
  `  cp "$transcript" ${shellQuote(transcriptsDir)}/"\${conversation##*/}.jsonl" || true`,
  "done",
  'exit "$status"',
].join("\n");

process.stdout.write(`${JSON.stringify({
  path: "bash",
  args: ["-c", shellCommand],
  env,
  dir: run.cwd,
})}\n`);
