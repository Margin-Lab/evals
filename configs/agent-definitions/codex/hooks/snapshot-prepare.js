#!/usr/bin/env node
const fs = require("node:fs");
const path = require("node:path");

const ctx = JSON.parse(fs.readFileSync(process.env.AGENT_CONTEXT_JSON, "utf8"));
const run = ctx.run;
const paths = ctx.paths;
const install = ctx.install || {};
const binPath = install.bin_path || path.join(paths.install_dir, "bin", "codex");
const env = { ...run.env, CODEX_HOME: path.join(paths.run_home, ".codex") };

const args = [
  "resume",
  "--last",
  "--no-alt-screen",
  "--dangerously-bypass-approvals-and-sandbox",
  "--skip-git-repo-check",
];
process.stdout.write(JSON.stringify({ path: binPath, args, env, dir: run.cwd }) + "\n");
