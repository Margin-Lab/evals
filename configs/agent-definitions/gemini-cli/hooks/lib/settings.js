#!/usr/bin/env node

function isPlainObject(value) {
  return !!value && typeof value === "object" && !Array.isArray(value);
}

function parseJSON(text, label) {
  try {
    return JSON.parse(String(text || ""));
  } catch (error) {
    throw new Error(`${label} is not valid JSON: ${error.message}`);
  }
}

function clone(value) {
  return JSON.parse(JSON.stringify(value));
}

function deepMerge(base, override) {
  if (!isPlainObject(base)) {
    return clone(override);
  }
  const merged = { ...base };
  for (const [key, value] of Object.entries(override || {})) {
    if (isPlainObject(value) && isPlainObject(merged[key])) {
      merged[key] = deepMerge(merged[key], value);
      continue;
    }
    merged[key] = clone(value);
  }
  return merged;
}

function normalizeContextFileNames(value) {
  const base = [];
  if (typeof value === "string" && value.trim()) {
    base.push(value.trim());
  } else if (Array.isArray(value)) {
    for (const item of value) {
      if (typeof item === "string" && item.trim()) {
        base.push(item.trim());
      }
    }
  }
  const deduped = [];
  for (const item of ["AGENTS.md", "GEMINI.md", ...base]) {
    if (!deduped.includes(item)) {
      deduped.push(item);
    }
  }
  return deduped;
}

function renderGeminiMCPServers(servers) {
  const rendered = {};
  for (const server of servers || []) {
    if (server.enabled === false) {
      continue;
    }
    const entry = {};
    if (server.transport === "stdio") {
      const command = Array.isArray(server.command) ? server.command : [];
      if (command.length === 0) {
        throw new Error(`unified.mcp server ${server.name} is missing command`);
      }
      entry.command = command[0];
      if (command.length > 1) {
        entry.args = command.slice(1);
      }
      if (server.env && Object.keys(server.env).length > 0) {
        entry.env = { ...server.env };
      }
    } else if (server.transport === "sse") {
      entry.url = server.url;
      if (server.headers && Object.keys(server.headers).length > 0) {
        entry.headers = { ...server.headers };
      }
    } else if (server.transport === "http") {
      entry.httpUrl = server.url;
      if (server.headers && Object.keys(server.headers).length > 0) {
        entry.headers = { ...server.headers };
      }
    } else {
      throw new Error(`unsupported MCP transport ${server.transport}`);
    }
    if (server.timeout_ms !== undefined && server.timeout_ms !== null) {
      entry.timeout = server.timeout_ms;
    }
    if (server.oauth) {
      throw new Error(`unified.mcp server ${server.name} uses oauth, which is not supported by the repo-owned Gemini unified translator`);
    }
    rendered[server.name] = entry;
  }
  return rendered;
}

function mergeGeminiSettings(baseSettings, runtimeSettings) {
  const merged = deepMerge(baseSettings, runtimeSettings);
  const context = isPlainObject(merged.context) ? { ...merged.context } : {};
  context.fileName = normalizeContextFileNames(context.fileName);
  merged.context = context;
  return merged;
}

module.exports = {
  deepMerge,
  mergeGeminiSettings,
  parseJSON,
  renderGeminiMCPServers,
};
