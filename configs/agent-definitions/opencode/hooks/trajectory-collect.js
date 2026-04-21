#!/usr/bin/env node
const fs = require("node:fs");
const path = require("node:path");

function loadContext() {
  return JSON.parse(fs.readFileSync(process.env.AGENT_CONTEXT_JSON, "utf8"));
}

function compactDict(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return value ?? null;
  }
  const out = {};
  for (const [key, item] of Object.entries(value)) {
    if (item !== null && item !== undefined) {
      out[key] = item;
    }
  }
  return Object.keys(out).length > 0 ? out : null;
}

function millisToISO(value) {
  if (typeof value !== "number") {
    return null;
  }
  const timestamp = new Date(value);
  if (Number.isNaN(timestamp.getTime())) {
    return null;
  }
  return timestamp.toISOString().replace(".000Z", "Z");
}

function jsonDumps(value) {
  if (value === null) {
    return "null";
  }
  if (typeof value === "string") {
    return JSON.stringify(value);
  }
  if (typeof value === "number" || typeof value === "boolean") {
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) {
    return `[${value.map((item) => jsonDumps(item)).join(", ")}]`;
  }
  if (value && typeof value === "object") {
    return `{${Object.keys(value).map((key) => `${JSON.stringify(key)}: ${jsonDumps(value[key])}`).join(", ")}}`;
  }
  return JSON.stringify(value);
}

function parseModelName(configInput) {
  const configJSON = configInput.config_jsonc;
  if (typeof configJSON !== "string" || !configJSON.trim()) {
    return null;
  }
  try {
    const parsed = JSON.parse(configJSON);
    return typeof parsed.model === "string" && parsed.model.trim() ? parsed.model.trim() : null;
  } catch {
    return null;
  }
}

function stringify(value) {
  if (typeof value === "string") {
    return value;
  }
  try {
    return jsonDumps(value);
  } catch {
    return String(value);
  }
}

function readEvents(filePath) {
  if (!fs.existsSync(filePath)) {
    return [];
  }
  const events = [];
  for (const line of fs.readFileSync(filePath, "utf8").split(/\r?\n/)) {
    const stripped = line.trim().replace(/\r/g, "");
    if (!stripped) {
      continue;
    }
    try {
      const payload = JSON.parse(stripped);
      if (payload && typeof payload === "object" && !Array.isArray(payload)) {
        events.push(payload);
      }
    } catch {
      // ignore malformed lines
    }
  }
  return events;
}

function buildSteps(initialPrompt, events, modelName) {
  const steps = [{ step_id: 1, source: "user", message: initialPrompt || "" }];
  const turns = [];
  let currentTurn = null;
  let sessionId = null;
  let totalCost = 0;
  let totalCompletionTokens = 0;
  let maxPromptTokens = null;
  let maxCachedTokens = null;

  for (const event of events) {
    if (!sessionId && typeof event.sessionID === "string" && event.sessionID.trim()) {
      sessionId = event.sessionID.trim();
    }
    if (event.type === "step_start") {
      currentTurn = { timestamp: event.timestamp, parts: [], finish: null };
      continue;
    }
    if (event.type === "step_finish") {
      if (currentTurn) {
        currentTurn.finish = event.part && typeof event.part === "object" ? event.part : {};
        turns.push(currentTurn);
        currentTurn = null;
      }
      continue;
    }
    if (currentTurn && (event.type === "text" || event.type === "tool_use")) {
      currentTurn.parts.push(event.part && typeof event.part === "object" ? event.part : {});
    }
  }

  for (const turn of turns) {
    const textParts = [];
    const toolCalls = [];
    const observationResults = [];

    for (const part of turn.parts) {
      if (part.type === "text") {
        if (typeof part.text === "string" && part.text) {
          textParts.push(part.text);
        }
        continue;
      }
      if (part.type !== "tool") {
        continue;
      }
      const state = part.state && typeof part.state === "object" ? part.state : {};
      let argumentsValue;
      if (state.input && typeof state.input === "object" && !Array.isArray(state.input)) {
        argumentsValue = state.input;
      } else if (state.input === null || state.input === undefined) {
        argumentsValue = {};
      } else {
        argumentsValue = { value: state.input };
      }
      const callId = String(part.callID || part.id || "tool-use").trim();
      toolCalls.push({
        tool_call_id: callId,
        function_name: String(part.tool || ""),
        arguments: argumentsValue,
      });
      if (Object.prototype.hasOwnProperty.call(state, "output")) {
        observationResults.push(compactDict({
          source_call_id: callId,
          content: stringify(state.output),
        }));
      }
    }

    const finish = turn.finish || {};
    const tokens = finish.tokens && typeof finish.tokens === "object" ? finish.tokens : {};
    const cache = tokens.cache && typeof tokens.cache === "object" ? tokens.cache : {};
    const promptTokens = tokens.input;
    const completionTokens = tokens.output;
    const cachedTokens = cache.read;
    const costUSD = finish.cost;
    totalCost += costUSD || 0;
    if (Number.isInteger(completionTokens)) {
      totalCompletionTokens += completionTokens;
    }
    if (Number.isInteger(promptTokens) || Number.isInteger(cachedTokens)) {
      const promptSnapshot = (promptTokens || 0) + (cachedTokens || 0);
      maxPromptTokens = maxPromptTokens === null ? promptSnapshot : Math.max(maxPromptTokens, promptSnapshot);
    }
    if (Number.isInteger(cachedTokens)) {
      maxCachedTokens = maxCachedTokens === null ? cachedTokens : Math.max(maxCachedTokens, cachedTokens);
    }

    const metrics = compactDict({
      prompt_tokens: promptTokens !== undefined || cachedTokens !== undefined
        ? (promptTokens || 0) + (cachedTokens || 0)
        : null,
      completion_tokens: completionTokens,
      cached_tokens: cachedTokens,
      cost_usd: costUSD,
      extra: compactDict({
        reasoning_tokens: tokens.reasoning,
        cache_write_tokens: cache.write,
      }),
    });

    steps.push(compactDict({
      step_id: steps.length + 1,
      timestamp: millisToISO(turn.timestamp),
      source: "agent",
      model_name: modelName,
      message: textParts.join("\n") || "(tool use)",
      tool_calls: toolCalls.length > 0 ? toolCalls : null,
      observation: observationResults.length > 0 ? { results: observationResults } : null,
      metrics,
    }) || {});
  }

  const finalMetrics = compactDict({
    total_prompt_tokens: maxPromptTokens,
    total_completion_tokens: totalCompletionTokens || null,
    total_cached_tokens: maxCachedTokens,
    total_cost_usd: totalCost || null,
    total_steps: steps.length,
  }) || { total_steps: steps.length };
  return [steps, { session_id: sessionId, final_metrics: finalMetrics }];
}

const ctx = loadContext();
const run = ctx.run;
const install = ctx.install || {};
const configInput = (ctx.config || {}).input || {};
const artifactsDir = ctx.paths.artifacts_dir;
let events = readEvents(path.join(artifactsDir, "opencode.jsonl"));
if (events.length === 0) {
  events = readEvents(path.join(artifactsDir, "pty.log"));
}
if (events.length === 0) {
  process.stdout.write("null\n");
  process.exit(0);
}

const modelName = parseModelName(configInput);
const [steps, details] = buildSteps(String(run.initial_prompt || ""), events, modelName);
const sessionId = String(details.session_id || run.session_id || "").trim();
if (!sessionId) {
  process.stdout.write("null\n");
  process.exit(0);
}

const version = String(install.version || configInput.opencode_version || "unknown").trim();
const trajectory = compactDict({
  schema_version: "ATIF-v1.6",
  session_id: sessionId,
  agent: compactDict({
    name: "opencode",
    version: version || "unknown",
    model_name: modelName,
  }),
  steps,
  final_metrics: details.final_metrics,
});
process.stdout.write(`${JSON.stringify(trajectory)}\n`);
