#!/usr/bin/env node
const fs = require("node:fs");

function splitModelId(raw) {
  const trimmed = String(raw || "").trim();
  const slash = trimmed.indexOf("/");
  if (slash < 0) {
    return ["", trimmed];
  }
  return [trimmed.slice(0, slash).trim(), trimmed.slice(slash + 1).trim()];
}

function renderOpenAIProviderConfig(modelId, reasoningLevel) {
  return {
    openai: {
      models: {
        [modelId]: {
          options: {
            reasoningEffort: reasoningLevel,
          },
        },
      },
    },
  };
}

function renderMCP(servers) {
  const entries = {};
  for (const server of servers || []) {
    const payload = {};
    if (server.enabled !== undefined && server.enabled !== null) {
      payload.enabled = server.enabled;
    }
    if (server.timeout_ms !== undefined && server.timeout_ms !== null) {
      payload.timeout = server.timeout_ms;
    }
    if (server.transport === "stdio") {
      payload.type = "local";
      payload.command = server.command;
      if (server.env) {
        payload.environment = server.env;
      }
    } else {
      payload.type = "remote";
      payload.url = server.url;
      if (server.headers) {
        payload.headers = server.headers;
      }
      if (server.oauth !== undefined) {
        if (server.oauth && server.oauth.disabled) {
          payload.oauth = false;
        } else if (server.oauth) {
          const oauth = {};
          if (server.oauth.client_id) {
            oauth.clientId = server.oauth.client_id;
          }
          if (server.oauth.client_secret) {
            oauth.clientSecret = server.oauth.client_secret;
          }
          if (server.oauth.scope) {
            oauth.scope = server.oauth.scope;
          }
          payload.oauth = oauth;
        }
      }
    }
    entries[server.name] = payload;
  }
  return entries;
}

const ctx = JSON.parse(fs.readFileSync(process.env.AGENT_CONTEXT_JSON, "utf8"));
const unified = ctx.config.unified;
const [provider, modelId] = splitModelId(unified.model);
const config = {
  $schema: "https://opencode.ai/config.json",
  model: unified.model,
};
if (provider === "openai" && modelId) {
  config.provider = renderOpenAIProviderConfig(modelId, unified.reasoning_level);
}
const servers = (((unified || {}).mcp || {}).servers || []);
if (servers.length > 0) {
  config.mcp = renderMCP(servers);
}
process.stdout.write(JSON.stringify({
  opencode_version: "latest",
  startup_args: [],
  run_args: [],
  config_jsonc: `${JSON.stringify(config, null, 2)}\n`,
}) + "\n");
