#!/usr/bin/env node
const fs = require("node:fs");
const path = require("node:path");
const { loadTrajectory } = require("./lib/stream_json_to_atif");

const ctx = JSON.parse(fs.readFileSync(process.env.AGENT_CONTEXT_JSON, "utf8"));
const install = ctx.install || {};
const inputPath = path.join(ctx.paths.artifacts_dir, "gemini-stream.jsonl");
const version = String(install.version || install.resolved_version || "unknown").trim();
const trajectory = loadTrajectory(inputPath, version);
process.stdout.write(`${JSON.stringify(trajectory)}\n`);
