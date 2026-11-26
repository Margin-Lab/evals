#!/usr/bin/env node
"use strict";

const fs = require("node:fs");
const path = require("node:path");
const { loadTrajectory } = require("./lib/stream_json_to_atif");

const ctx = JSON.parse(fs.readFileSync(process.env.AGENT_CONTEXT_JSON, "utf8"));
const install = ctx.install || {};
const input = ((ctx.config || {}).input) || {};
const trajectory = loadTrajectory(path.join(ctx.paths.artifacts_dir, "agy-stream.jsonl"), {
  version: install.version,
  model: input.model,
  initialPrompt: (ctx.run || {}).initial_prompt,
});
process.stdout.write(`${JSON.stringify(trajectory)}\n`);
