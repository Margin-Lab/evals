#!/usr/bin/env node
const fs = require("node:fs");

const ctx = JSON.parse(fs.readFileSync(process.env.AGENT_CONTEXT_JSON, "utf8"));
const unified = ctx.config.unified || {};
const modelRef = String(unified.model || "");
const slash = modelRef.indexOf("/");
if (slash <= 0 || slash === modelRef.length - 1) {
  throw new Error(`config.unified.model must be provider-qualified for Pi, got ${JSON.stringify(modelRef)}`);
}

process.stdout.write(JSON.stringify({
  pi_version: "latest",
  startup_args: [],
  run_args: [],
  provider: modelRef.slice(0, slash),
  model: modelRef.slice(slash + 1),
  thinking: unified.reasoning_level,
}) + "\n");
