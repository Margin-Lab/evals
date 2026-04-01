#!/usr/bin/env node
const fs = require("node:fs");
const vm = require("node:vm");

function parseJSONC(raw) {
  try {
    return vm.runInNewContext(`(${raw})`, Object.create(null), { timeout: 1000 });
  } catch (error) {
    throw new Error(`config_jsonc is not valid JSONC: ${error.message}`);
  }
}

function splitProvider(raw) {
  const text = String(raw || "").trim();
  const slash = text.indexOf("/");
  if (slash <= 0 || slash === text.length - 1) {
    return "";
  }
  return text.slice(0, slash).trim();
}

const ctx = JSON.parse(fs.readFileSync(process.env.AGENT_CONTEXT_JSON, "utf8"));
const input = { ...((ctx.config || {}).input || {}) };
const provider = String(input.provider || "").trim();
if (!provider) {
  throw new Error("config.input.provider is required");
}

const parsed = parseJSONC(String(input.config_jsonc || ""));
const modelProvider = splitProvider(parsed.model);
if (modelProvider && modelProvider !== provider) {
  throw new Error(`config.input.provider ${JSON.stringify(provider)} does not match config_jsonc model provider ${JSON.stringify(modelProvider)}`);
}

process.stdout.write(`${JSON.stringify(input)}\n`);
