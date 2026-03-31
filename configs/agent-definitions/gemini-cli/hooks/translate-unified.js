#!/usr/bin/env node
const fs = require("node:fs");
const { renderGeminiMCPServers } = require("./lib/settings");

const ctx = JSON.parse(fs.readFileSync(process.env.AGENT_CONTEXT_JSON, "utf8"));
const unified = ctx.config.unified || {};

const settings = {};
const servers = (((unified || {}).mcp || {}).servers) || [];
const renderedServers = renderGeminiMCPServers(servers);
if (Object.keys(renderedServers).length > 0) {
  settings.mcpServers = renderedServers;
}

process.stdout.write(JSON.stringify({
  gemini_version: "latest",
  startup_args: [],
  run_args: [],
  model: unified.model,
  approval_mode: "yolo",
  settings_json: JSON.stringify(settings, null, 2),
}) + "\n");
