#!/usr/bin/env node
const fs = require("node:fs");

function renderMCP(servers) {
  const entries = {};
  for (const server of servers || []) {
    const payload = { type: server.transport };
    if (server.enabled !== undefined && server.enabled !== null) {
      payload.enabled = server.enabled;
    }
    if (server.timeout_ms !== undefined && server.timeout_ms !== null) {
      payload.timeout_ms = server.timeout_ms;
    }
    if (server.transport === "stdio") {
      payload.command = server.command[0];
      if ((server.command || []).length > 1) {
        payload.args = server.command.slice(1);
      }
      if (server.env) {
        payload.env = server.env;
      }
    } else {
      payload.url = server.url;
      if (server.headers) {
        payload.headers = server.headers;
      }
      if (server.oauth !== undefined) {
        if (server.oauth && server.oauth.disabled) {
          payload.oauth = false;
        } else if (server.oauth) {
          const mapped = {};
          if (server.oauth.client_id) {
            mapped.clientId = server.oauth.client_id;
          }
          if (server.oauth.client_secret) {
            mapped.clientSecret = server.oauth.client_secret;
          }
          if (server.oauth.scope) {
            mapped.scope = server.oauth.scope;
          }
          payload.oauth = mapped;
        }
      }
    }
    entries[server.name] = payload;
  }
  return { mcpServers: entries };
}

const ctx = JSON.parse(fs.readFileSync(process.env.AGENT_CONTEXT_JSON, "utf8"));
const unified = ctx.config.unified;
const payload = {
  claude_version: "latest",
  startup_args: ["--model", unified.model, "--effort", unified.reasoning_level],
  run_args: [],
  settings_json: `${JSON.stringify({ model: unified.model, permissionMode: "acceptEdits" }, null, 2)}\n`,
};
const servers = (((unified || {}).mcp || {}).servers || []);
if (servers.length > 0) {
  payload.mcp_json = `${JSON.stringify(renderMCP(servers), null, 2)}\n`;
}
process.stdout.write(JSON.stringify(payload) + "\n");
