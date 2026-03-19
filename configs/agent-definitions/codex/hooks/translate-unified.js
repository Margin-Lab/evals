#!/usr/bin/env node
const fs = require("node:fs");

function writeMapTable(lines, tablePath, values) {
  const entries = Object.entries(values || {});
  if (entries.length === 0) {
    return;
  }
  lines.push("");
  lines.push(`[${tablePath}]`);
  for (const [key, value] of entries.sort(([left], [right]) => left.localeCompare(right))) {
    lines.push(`${JSON.stringify(key)} = ${JSON.stringify(value)}`);
  }
}

function writeOAuthTable(lines, tablePath, oauth) {
  if (!oauth) {
    return;
  }
  lines.push("");
  lines.push(`[${tablePath}]`);
  if (oauth.disabled) {
    lines.push("disabled = true");
    return;
  }
  if (oauth.client_id) {
    lines.push(`client_id = ${JSON.stringify(oauth.client_id)}`);
  }
  if (oauth.client_secret) {
    lines.push(`client_secret = ${JSON.stringify(oauth.client_secret)}`);
  }
  if (oauth.scope) {
    lines.push(`scope = ${JSON.stringify(oauth.scope)}`);
  }
}

function renderConfig(unified) {
  const lines = [
    `model = ${JSON.stringify(unified.model)}`,
    `model_reasoning_effort = ${JSON.stringify(unified.reasoning_level)}`,
    'approval_policy = "never"',
    'sandbox_mode = "workspace-write"',
  ];
  const servers = (((unified || {}).mcp || {}).servers || []);
  for (const server of servers) {
    const serverName = JSON.stringify(server.name);
    const tablePath = `mcp_servers.${serverName}`;
    lines.push("");
    lines.push(`[${tablePath}]`);
    lines.push(`transport = ${JSON.stringify(server.transport)}`);
    if (server.enabled !== undefined && server.enabled !== null) {
      lines.push(`enabled = ${server.enabled ? "true" : "false"}`);
    }
    if (server.timeout_ms !== undefined && server.timeout_ms !== null) {
      lines.push(`timeout_ms = ${server.timeout_ms}`);
    }
    if (server.transport === "stdio") {
      const command = server.command || [];
      lines.push(`command = ${JSON.stringify(command[0])}`);
      if (command.length > 1) {
        lines.push(`args = [${command.slice(1).map((value) => JSON.stringify(value)).join(", ")}]`);
      }
      writeMapTable(lines, `${tablePath}.env`, server.env || {});
    } else {
      lines.push(`url = ${JSON.stringify(server.url)}`);
      writeMapTable(lines, `${tablePath}.headers`, server.headers || {});
      writeOAuthTable(lines, `${tablePath}.oauth`, server.oauth);
    }
  }
  return `${lines.join("\n")}\n`;
}

const ctx = JSON.parse(fs.readFileSync(process.env.AGENT_CONTEXT_JSON, "utf8"));
const unified = ctx.config.unified;
process.stdout.write(JSON.stringify({
  codex_version: "latest",
  startup_args: [],
  run_args: [],
  config_toml: renderConfig(unified),
}) + "\n");
