#!/usr/bin/env node
const fs = require("node:fs");
const path = require("node:path");

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
const runHome = paths.run_home;
const configPath = path.join(runHome, ".opencode", "opencode.jsonc");
fs.mkdirSync(path.dirname(configPath), { recursive: true });
fs.writeFileSync(configPath, cfg.config_jsonc, "utf8");
const binPath = install.bin_path || path.join(paths.install_dir, "bin", "opencode");
const outputPath = path.join(paths.artifacts_dir, "opencode.jsonl");
const stderrPath = path.join(paths.artifacts_dir, "opencode.stderr.log");
const env = { ...run.env, OPENCODE_CONFIG: configPath, OPENCODE_FAKE_VCS: "git" };
const command = [
  binPath,
  "run",
  "--format=json",
  ...(cfg.startup_args || []),
  ...(cfg.run_args || []),
  "--",
  run.initial_prompt,
].map(shellQuote).join(" ");
const shellCommand = [
  "set -euo pipefail",
  `mkdir -p ${shellQuote(path.dirname(outputPath))}`,
  `${command} > >(tee ${shellQuote(outputPath)}) 2> >(tee ${shellQuote(stderrPath)} >&2)`,
].join("\n");
process.stdout.write(JSON.stringify({
  path: "bash",
  args: ["-c", shellCommand],
  env,
  dir: run.cwd,
}) + "\n");
