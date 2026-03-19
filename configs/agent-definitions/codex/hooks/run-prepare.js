#!/usr/bin/env node
const fs = require("node:fs");
const path = require("node:path");

const ctx = JSON.parse(fs.readFileSync(process.env.AGENT_CONTEXT_JSON, "utf8"));
const run = ctx.run;
const cfg = ctx.config.input;
const paths = ctx.paths;
const install = ctx.install || {};
const runHome = paths.run_home;
const codexHome = path.join(runHome, ".codex");
const binPath = install.bin_path || path.join(paths.install_dir, "bin", "codex");

fs.mkdirSync(codexHome, { recursive: true });
fs.writeFileSync(path.join(codexHome, "config.toml"), cfg.config_toml, "utf8");

const env = { ...run.env, CODEX_HOME: codexHome };
const apiKey = String(run.env.OPENAI_API_KEY || "").trim();
const authPath = path.join(codexHome, "auth.json");
if (apiKey && !fs.existsSync(authPath)) {
  fs.writeFileSync(authPath, JSON.stringify({ OPENAI_API_KEY: apiKey }, null, 2) + "\n", "utf8");
}

const args = [
  "exec",
  "--dangerously-bypass-approvals-and-sandbox",
  "--skip-git-repo-check",
  "--enable",
  "unified_exec",
  ...(cfg.startup_args || []),
  ...(cfg.run_args || []),
  run.initial_prompt,
];
process.stdout.write(JSON.stringify({ path: binPath, args, env, dir: run.cwd }) + "\n");
