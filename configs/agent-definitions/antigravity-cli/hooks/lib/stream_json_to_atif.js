#!/usr/bin/env node
"use strict";

// Converts agy's `--output-format stream-json` events into ATIF.
//
// The stream has one `init` event, `step_update` events for each step of the
// conversation (a step_index, a step_type such as user_input, agent_response or
// tool, and a state that ends at DONE), and one `result` event with the run's
// status and cumulative usage. Text arrives as text_delta pieces; tool steps carry
// tool_info with the tool's name, parameters, output and error.

const fs = require("node:fs");

function isPlainObject(value) {
  return !!value && typeof value === "object" && !Array.isArray(value);
}

function finiteOrUndefined(value) {
  return Number.isFinite(value) ? value : undefined;
}

function compact(object) {
  const out = {};
  for (const [key, value] of Object.entries(object)) {
    if (value === undefined || value === null) {
      continue;
    }
    if (isPlainObject(value) && Object.keys(value).length === 0) {
      continue;
    }
    out[key] = value;
  }
  return out;
}

function readEvents(filePath) {
  if (!fs.existsSync(filePath)) {
    return [];
  }
  const events = [];
  for (const line of fs.readFileSync(filePath, "utf8").split(/\r?\n/)) {
    if (!line.trim()) {
      continue;
    }
    try {
      const event = JSON.parse(line);
      if (isPlainObject(event)) {
        events.push(event);
      }
    } catch {
      // agy writes only events to stdout; skip anything else rather than fail.
    }
  }
  return events;
}

// Events carry their payload under a key named after the event, e.g.
// {"event":"step_update","step_update":{...}}; accept a flat event too.
function payloadOf(event) {
  const name = String(event.event || event.type || "");
  return isPlainObject(event[name]) ? event[name] : event;
}

function textOf(value) {
  if (typeof value === "string") {
    return value;
  }
  if (value === undefined || value === null) {
    return "";
  }
  return JSON.stringify(value);
}

function argumentsOf(parameters) {
  if (isPlainObject(parameters)) {
    return parameters;
  }
  if (typeof parameters === "string" && parameters.trim()) {
    try {
      const parsed = JSON.parse(parameters);
      if (isPlainObject(parsed)) {
        return parsed;
      }
    } catch {
      // fall through
    }
    return { input: parameters };
  }
  return {};
}

function usageMetrics(usage) {
  if (!isPlainObject(usage)) {
    return undefined;
  }
  const output = finiteOrUndefined(usage.output_tokens);
  const thinking = finiteOrUndefined(usage.thinking_tokens);
  const metrics = compact({
    prompt_tokens: finiteOrUndefined(usage.input_tokens),
    completion_tokens: output === undefined && thinking === undefined ? undefined : (output || 0) + (thinking || 0),
    cached_tokens: finiteOrUndefined(usage.cache_read_tokens),
  });
  return Object.keys(metrics).length > 0 ? metrics : undefined;
}

function collectSteps(events) {
  const steps = new Map();
  const order = [];
  let init = null;
  let result = null;
  let conversationID = "";
  for (const event of events) {
    const kind = String(event.event || event.type || "");
    const payload = payloadOf(event);
    if (kind === "init") {
      init = payload;
      continue;
    }
    if (kind === "result") {
      result = payload;
      continue;
    }
    if (kind !== "step_update") {
      continue;
    }
    if (payload.conversation_id && !conversationID) {
      conversationID = String(payload.conversation_id);
    }
    const key = `${payload.subagent_info ? "sub" : "main"}:${payload.step_index}`;
    let step = steps.get(key);
    if (!step) {
      step = { index: payload.step_index, type: "", text: "", done: false };
      steps.set(key, step);
      order.push(key);
    }
    if (payload.step_type) {
      step.type = String(payload.step_type);
    }
    if (payload.tool_name) {
      step.toolName = String(payload.tool_name);
    }
    if (isPlainObject(payload.tool_info)) {
      step.toolInfo = { ...(step.toolInfo || {}), ...payload.tool_info };
    }
    if (isPlainObject(payload.usage)) {
      step.usage = payload.usage;
    }
    if (payload.subagent_info) {
      step.subagent = payload.subagent_info;
    }
    if (Number.isFinite(payload.duration_seconds)) {
      step.durationSeconds = payload.duration_seconds;
    }
    if (typeof payload.text_delta === "string" && payload.text_delta) {
      // A final event may repeat the whole text instead of adding to it.
      if (payload.state === "DONE" && step.text && payload.text_delta.startsWith(step.text)) {
        step.text = payload.text_delta;
      } else {
        step.text += payload.text_delta;
      }
    }
    if (payload.state === "DONE") {
      step.done = true;
    }
  }
  return { init, result, conversationID, steps: order.map((key) => steps.get(key)) };
}

function toTrajectory(events, options = {}) {
  const { init, result, conversationID, steps: rawSteps } = collectSteps(events);
  const version = String(options.version || "unknown").trim() || "unknown";
  const modelName = String((init && init.model) || options.model || "").trim();
  const steps = [];
  const stepTypes = {};
  let pending = null;
  let toolCalls = 0;
  let largestPrompt;
  let largestCached;

  const flush = () => {
    if (!pending) {
      return;
    }
    const step = {
      step_id: steps.length + 1,
      source: "agent",
      model_name: modelName || undefined,
      message: pending.text || (pending.toolCalls.length > 0 ? "(tool use)" : "(empty response)"),
    };
    if (pending.toolCalls.length > 0) {
      step.tool_calls = pending.toolCalls;
    }
    if (pending.results.length > 0) {
      step.observation = { results: pending.results };
    }
    if (pending.metrics) {
      step.metrics = pending.metrics;
    }
    steps.push(step);
    pending = null;
  };
  const ensurePending = () => {
    if (!pending) {
      pending = { text: "", toolCalls: [], results: [], metrics: undefined };
    }
    return pending;
  };

  for (const raw of rawSteps) {
    stepTypes[raw.type || "unknown"] = (stepTypes[raw.type || "unknown"] || 0) + 1;
    const metrics = usageMetrics(raw.usage);
    if (metrics && Number.isFinite(metrics.prompt_tokens)) {
      largestPrompt = Math.max(largestPrompt || 0, metrics.prompt_tokens);
    }
    if (metrics && Number.isFinite(metrics.cached_tokens)) {
      largestCached = Math.max(largestCached || 0, metrics.cached_tokens);
    }
    if (raw.type === "user_input") {
      flush();
      const message = raw.text || (steps.length === 0 ? String(options.initialPrompt || "") : "");
      steps.push({ step_id: steps.length + 1, source: "user", message: message || "(user input)" });
      continue;
    }
    if (raw.type === "agent_response" || (!raw.toolInfo && raw.text && raw.type !== "checkpoint")) {
      if (pending && pending.results.length > 0) {
        flush();
      }
      const target = ensurePending();
      target.text += raw.text;
      if (metrics) {
        target.metrics = metrics;
      }
      continue;
    }
    if (raw.type === "tool" || raw.toolInfo) {
      const info = raw.toolInfo || {};
      const target = ensurePending();
      const callID = `call-${toolCalls + 1}-step-${raw.subagent ? "sub-" : ""}${raw.index}`;
      target.toolCalls.push({
        tool_call_id: callID,
        function_name: String(info.name || raw.toolName || "tool"),
        arguments: argumentsOf(info.parameters),
      });
      const error = isPlainObject(info.error) ? info.error : null;
      const output = textOf(info.output);
      target.results.push({
        source_call_id: callID,
        content: output || (error ? `Error: ${error.message || error.type || "tool failed"}` : ""),
      });
      if (metrics) {
        target.metrics = metrics;
      }
      toolCalls++;
      continue;
    }
  }

  if (result && result.status && result.status !== "SUCCESS" && result.error) {
    const target = ensurePending();
    target.text += `${target.text ? "\n" : ""}Error: ${textOf(result.error)}`;
  }
  flush();

  if (steps.length === 0) {
    return null;
  }
  const sessionID = String((result && result.conversation_id) || conversationID || "").trim() || "unknown";
  const usage = result && isPlainObject(result.usage) ? result.usage : null;
  const totals = usageMetrics(usage) || {};
  return {
    schema_version: "ATIF-v1.6",
    session_id: sessionID,
    agent: compact({
      name: "antigravity-cli",
      version,
      model_name: modelName || undefined,
    }),
    steps,
    final_metrics: compact({
      total_prompt_tokens: largestPrompt,
      total_completion_tokens: totals.completion_tokens,
      total_cached_tokens: largestCached,
      total_steps: steps.length,
      extra: compact({
        status: result ? result.status : undefined,
        finished_reason: result ? result.finished_reason : undefined,
        num_turns: result ? finiteOrUndefined(result.num_turns) : undefined,
        duration_seconds: result ? finiteOrUndefined(result.duration_seconds) : undefined,
        usage_totals: usage || undefined,
        tool_calls: toolCalls,
        step_types: stepTypes,
      }),
    }),
  };
}

function loadTrajectory(inputPath, options = {}) {
  return toTrajectory(readEvents(inputPath), options);
}

module.exports = {
  loadTrajectory,
  readEvents,
  toTrajectory,
};
