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
const binPath = install.bin_path || path.join(paths.install_dir, "bin", "pi");
const piAgentDir = path.join(paths.run_home, ".pi", "agent");
const sessionDir = path.join(paths.run_home, ".pi", "sessions");
const outputPath = path.join(paths.artifacts_dir, "pi-events.jsonl");
const stderrPath = path.join(paths.artifacts_dir, "pi.stderr.log");

fs.mkdirSync(piAgentDir, { recursive: true });
fs.mkdirSync(sessionDir, { recursive: true });

const env = { ...run.env, PI_CODING_AGENT_DIR: piAgentDir };
const command = [
  shellQuote(binPath),
  ...(cfg.startup_args || []).map(shellQuote),
  "--mode",
  "json",
  "--session-dir",
  shellQuote(sessionDir),
  "--provider",
  shellQuote(cfg.provider),
  "--model",
  shellQuote(cfg.model),
  "--thinking",
  shellQuote(cfg.thinking),
  ...(cfg.run_args || []).map(shellQuote),
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
