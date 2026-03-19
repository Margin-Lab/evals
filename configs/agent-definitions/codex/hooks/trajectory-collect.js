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

function parseModelName(configToml) {
  if (typeof configToml !== "string") {
    return null;
  }
  const match = configToml.match(/^\s*model\s*=\s*"([^"]+)"/m);
  return match && match[1] ? match[1].trim() : null;
}

function walkJSONLFiles(root) {
  if (!fs.existsSync(root) || !fs.statSync(root).isDirectory()) {
    return [];
  }
  const results = [];
  const stack = [root];
  while (stack.length > 0) {
    const current = stack.pop();
    for (const entry of fs.readdirSync(current, { withFileTypes: true })) {
      const fullPath = path.join(current, entry.name);
      if (entry.isDirectory()) {
        stack.push(fullPath);
      } else if (entry.isFile() && entry.name.endsWith(".jsonl")) {
        results.push(fullPath);
      }
    }
  }
  return results;
}

function findSessionFile(codexHome) {
  const sessionsDir = path.join(codexHome, "sessions");
  const candidates = walkJSONLFiles(sessionsDir);
  if (candidates.length === 0) {
    return null;
  }
  candidates.sort((left, right) => {
    const leftDepth = left.split(path.sep).length;
    const rightDepth = right.split(path.sep).length;
    if (leftDepth !== rightDepth) {
      return rightDepth - leftDepth;
    }
    return fs.statSync(right).mtimeMs - fs.statSync(left).mtimeMs;
  });
  return candidates[0];
}

function readJSONL(filePath) {
  const events = [];
  for (const line of fs.readFileSync(filePath, "utf8").split(/\r?\n/)) {
    const stripped = line.trim();
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

function extractMessageText(content) {
  if (typeof content === "string") {
    return content;
  }
  if (!Array.isArray(content)) {
    return "";
  }
  return content
    .filter((block) => block && typeof block === "object" && typeof block.text === "string")
    .map((block) => block.text)
    .join("");
}

function parseArguments(raw) {
  if (raw && typeof raw === "object" && !Array.isArray(raw)) {
    return [raw, raw];
  }
  if (typeof raw === "string") {
    try {
      const parsed = JSON.parse(raw);
      if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
        return [parsed, raw];
      }
      return [{ value: parsed }, raw];
    } catch {
      return [{ input: raw }, raw];
    }
  }
  if (raw === null || raw === undefined) {
    return [{}, null];
  }
  return [{ value: raw }, raw];
}

function parseOutputBlob(raw) {
  if (raw === null || raw === undefined) {
    return [null, null];
  }
  let parsed = raw;
  if (typeof raw === "string") {
    try {
      parsed = JSON.parse(raw);
    } catch {
      return [raw, null];
    }
  }
  if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
    let output = parsed.output;
    if (output === undefined && Object.keys(parsed).length > 0) {
      output = JSON.stringify(parsed);
    }
    const metadata = parsed.metadata && typeof parsed.metadata === "object" && !Array.isArray(parsed.metadata)
      ? parsed.metadata
      : null;
    return [
      typeof output === "string" || output === null || output === undefined ? output ?? null : JSON.stringify(output),
      metadata,
    ];
  }
  if (Array.isArray(parsed)) {
    return [JSON.stringify(parsed), null];
  }
  return [String(parsed), null];
}

function buildSteps(rawEvents, defaultModelName) {
  const normalizedEvents = [];
  const pendingCalls = new Map();
  let pendingReasoning = null;

  for (const event of rawEvents) {
    if (event.type !== "response_item") {
      continue;
    }
    const payload = event.payload;
    if (!payload || typeof payload !== "object" || Array.isArray(payload)) {
      continue;
    }
    const payloadType = payload.type;
    const timestamp = event.timestamp;

    if (payloadType === "reasoning") {
      const summary = payload.summary;
      if (Array.isArray(summary)) {
        const parts = summary.filter((item) => typeof item === "string" && item.trim());
        pendingReasoning = parts.length > 0 ? parts.join("\n") : null;
      } else {
        pendingReasoning = null;
      }
      continue;
    }

    if (payloadType === "message") {
      const role = payload.role || "user";
      const source = role === "assistant" ? "agent" : role === "user" ? "user" : "system";
      const step = {
        timestamp,
        source,
        message: extractMessageText(payload.content),
      };
      if (source === "agent" && pendingReasoning) {
        step.reasoning_content = pendingReasoning;
      }
      if (source === "agent" && defaultModelName) {
        step.model_name = defaultModelName;
      }
      normalizedEvents.push(step);
      pendingReasoning = null;
      continue;
    }

    if (payloadType === "function_call" || payloadType === "custom_tool_call") {
      const callId = String(payload.call_id || "").trim();
      if (!callId) {
        continue;
      }
      const rawArguments = payloadType === "function_call" ? payload.arguments : payload.input;
      const [argumentsValue, rawArgumentsValue] = parseArguments(rawArguments);
      pendingCalls.set(callId, compactDict({
        timestamp,
        source: "agent",
        message: null,
        model_name: defaultModelName,
        reasoning_content: pendingReasoning,
        tool_calls: [{
          tool_call_id: callId,
          function_name: String(payload.name || ""),
          arguments: argumentsValue,
        }],
        extra: compactDict({
          raw_arguments: rawArgumentsValue,
          status: payload.status,
        }),
      }) || {});
      pendingReasoning = null;
      continue;
    }

    if (payloadType === "function_call_output" || payloadType === "custom_tool_call_output") {
      const callId = String(payload.call_id || "").trim();
      const [outputText, metadata] = parseOutputBlob(payload.output);
      let step = callId ? pendingCalls.get(callId) : null;
      if (callId) {
        pendingCalls.delete(callId);
      }
      if (!step) {
        step = {
          timestamp,
          source: "agent",
          message: null,
          model_name: defaultModelName,
          tool_calls: [{
            tool_call_id: callId || "tool-call-output",
            function_name: String(payload.name || ""),
            arguments: {},
          }],
        };
      }
      step.timestamp = step.timestamp || timestamp;
      if (outputText !== null) {
        const toolCallId = step.tool_calls[0].tool_call_id;
        step.observation = {
          results: [compactDict({ source_call_id: toolCallId, content: outputText })],
        };
      }
      const extra = { ...(step.extra || {}) };
      if (metadata) {
        extra.tool_metadata = metadata;
      }
      step.extra = compactDict(extra);
      normalizedEvents.push(compactDict(step) || {});
      pendingReasoning = null;
    }
  }

  normalizedEvents.push(...Array.from(pendingCalls.entries())
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([, step]) => step));

  const steps = [];
  normalizedEvents.forEach((event, index) => {
    const step = compactDict({
      step_id: index + 1,
      timestamp: event.timestamp,
      source: event.source,
      model_name: event.model_name,
      message: event.message || "(tool use)",
      reasoning_content: event.reasoning_content,
      tool_calls: event.tool_calls,
      observation: event.observation,
      extra: event.extra,
    });
    if (step) {
      steps.push(step);
    }
  });
  return steps;
}

function buildFinalMetrics(rawEvents, totalSteps) {
  for (let index = rawEvents.length - 1; index >= 0; index -= 1) {
    const event = rawEvents[index];
    if (event.type !== "event_msg") {
      continue;
    }
    const payload = event.payload;
    if (!payload || payload.type !== "token_count") {
      continue;
    }
    const info = payload.info;
    if (!info || typeof info !== "object" || Array.isArray(info)) {
      continue;
    }
    const usage = info.total_token_usage;
    if (!usage || typeof usage !== "object" || Array.isArray(usage)) {
      continue;
    }
    const extra = compactDict({
      reasoning_output_tokens: usage.reasoning_output_tokens,
      total_tokens: usage.total_tokens,
      last_token_usage: info.last_token_usage,
    });
    return compactDict({
      total_prompt_tokens: usage.input_tokens,
      total_completion_tokens: usage.output_tokens,
      total_cached_tokens: usage.cached_input_tokens,
      total_cost_usd: info.total_cost || info.cost_usd,
      total_steps: totalSteps,
      extra,
    });
  }
  return { total_steps: totalSteps };
}

const ctx = loadContext();
const run = ctx.run;
const configInput = ((ctx.config || {}).input) || {};
const install = ctx.install || {};
const codexHome = path.join(ctx.paths.run_home, ".codex");
const sessionFile = findSessionFile(codexHome);

if (!sessionFile) {
  process.stdout.write("null\n");
  process.exit(0);
}

const rawEvents = readJSONL(sessionFile);
if (rawEvents.length === 0) {
  process.stdout.write("null\n");
  process.exit(0);
}

const sessionMeta = rawEvents.find((event) => event.type === "session_meta" && event.payload && typeof event.payload === "object")?.payload || {};
const sessionId = String(sessionMeta.id || run.session_id || path.basename(path.dirname(sessionFile)) || "").trim();
if (!sessionId) {
  process.stdout.write("null\n");
  process.exit(0);
}

const version = String(
  install.resolved_version ||
  install.version ||
  sessionMeta.cli_version ||
  configInput.codex_version ||
  "unknown"
).trim();

let defaultModelName = parseModelName(String(configInput.config_toml || ""));
for (const event of rawEvents) {
  if (event.type !== "turn_context" || !event.payload || typeof event.payload !== "object") {
    continue;
  }
  if (typeof event.payload.model === "string" && event.payload.model.trim()) {
    defaultModelName = event.payload.model.trim();
    break;
  }
}

const agentExtra = compactDict({
  originator: sessionMeta.originator,
  cwd: sessionMeta.cwd,
  git: sessionMeta.git,
  instructions: sessionMeta.instructions,
});
const steps = buildSteps(rawEvents, defaultModelName);
if (steps.length === 0) {
  process.stdout.write("null\n");
  process.exit(0);
}

const trajectory = compactDict({
  schema_version: "ATIF-v1.6",
  session_id: sessionId,
  agent: compactDict({
    name: "codex",
    version: version || "unknown",
    model_name: defaultModelName,
    extra: agentExtra || {},
  }),
  steps,
  final_metrics: buildFinalMetrics(rawEvents, steps.length),
});
process.stdout.write(`${JSON.stringify(trajectory)}\n`);
