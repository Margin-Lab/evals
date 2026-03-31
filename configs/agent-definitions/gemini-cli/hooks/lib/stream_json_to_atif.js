#!/usr/bin/env node
const fs = require("node:fs");
const path = require("node:path");

function readJSONLLines(filePath) {
  if (!fs.existsSync(filePath)) {
    throw new Error(`artifact file not found: ${filePath}`);
  }
  return fs.readFileSync(filePath, "utf8")
    .split(/\r?\n/)
    .filter((line) => line.trim())
    .map((line) => JSON.parse(line));
}

function createPendingStep(timestamp) {
  return {
    timestamp,
    textParts: [],
    toolCalls: [],
    observationResults: [],
  };
}

function buildFinalMetrics(stats, stepCount) {
  if (!stats || typeof stats !== "object") {
    return { total_steps: stepCount };
  }
  const totalPromptTokens = Number.isFinite(stats.input_tokens) ? stats.input_tokens : undefined;
  const totalCompletionTokens = Number.isFinite(stats.output_tokens) ? stats.output_tokens : undefined;
  const totalCachedTokens = Number.isFinite(stats.cached) ? stats.cached : undefined;
  const extra = {};
  if (Number.isFinite(stats.duration_ms)) {
    extra.duration_ms = stats.duration_ms;
  }
  if (Number.isFinite(stats.tool_calls)) {
    extra.tool_calls = stats.tool_calls;
  }
  if (stats.models && typeof stats.models === "object" && Object.keys(stats.models).length > 0) {
    extra.models = stats.models;
  }
  return {
    total_prompt_tokens: totalPromptTokens,
    total_completion_tokens: totalCompletionTokens,
    total_cached_tokens: totalCachedTokens,
    total_steps: stepCount,
    extra: Object.keys(extra).length > 0 ? extra : undefined,
  };
}

function toTrajectory(events, version) {
  let sessionId = "";
  let modelName = "";
  let finalStats = null;
  let pending = null;
  let nextStepID = 1;
  const steps = [];

  function flushPending() {
    if (!pending) {
      return;
    }
    const message = pending.textParts.join("") ||
      (pending.toolCalls.length > 0 ? "(tool use)" : "(empty response)");
    const step = {
      step_id: nextStepID++,
      timestamp: pending.timestamp,
      source: "agent",
      model_name: modelName || undefined,
      message,
    };
    if (pending.toolCalls.length > 0) {
      step.tool_calls = pending.toolCalls;
    }
    if (pending.observationResults.length > 0) {
      step.observation = { results: pending.observationResults };
    }
    steps.push(step);
    pending = null;
  }

  for (const event of events) {
    switch (event.type) {
      case "init":
        sessionId = String(event.session_id || "").trim();
        modelName = String(event.model || "").trim();
        break;
      case "message":
        if (event.role === "user") {
          flushPending();
          steps.push({
            step_id: nextStepID++,
            timestamp: event.timestamp,
            source: "user",
            message: String(event.content || ""),
          });
          break;
        }
        if (pending && pending.observationResults.length > 0) {
          flushPending();
        }
        if (!pending) {
          pending = createPendingStep(event.timestamp);
        }
        pending.textParts.push(String(event.content || ""));
        break;
      case "tool_use":
        if (!pending) {
          pending = createPendingStep(event.timestamp);
        }
        pending.toolCalls.push({
          tool_call_id: String(event.tool_id || ""),
          function_name: String(event.tool_name || ""),
          arguments: event.parameters && typeof event.parameters === "object" ? event.parameters : {},
        });
        break;
      case "tool_result":
        if (!pending) {
          pending = createPendingStep(event.timestamp);
        }
        pending.observationResults.push({
          source_call_id: String(event.tool_id || ""),
          content: event.output || (event.error && event.error.message) || "",
        });
        break;
      case "result":
        finalStats = event.stats || null;
        if (event.status === "error" && event.error && !pending) {
          pending = createPendingStep(event.timestamp);
          pending.textParts.push(`Error: ${String(event.error.message || "unknown error")}`);
        }
        break;
      default:
        break;
    }
  }

  flushPending();

  if (!sessionId || steps.length === 0) {
    return null;
  }

  return {
    schema_version: "ATIF-v1.6",
    session_id: sessionId,
    agent: {
      name: "gemini-cli",
      version,
      model_name: modelName || undefined,
    },
    steps,
    final_metrics: buildFinalMetrics(finalStats, steps.length),
  };
}

function loadTrajectory(inputPath, version) {
  const events = readJSONLLines(inputPath);
  return toTrajectory(events, version);
}

module.exports = {
  loadTrajectory,
};
