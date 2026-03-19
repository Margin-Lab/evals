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

function stringify(value) {
  if (typeof value === "string") {
    return value;
  }
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
}

function readJSONLFiles(root) {
  const events = [];
  if (!fs.existsSync(root)) {
    return events;
  }
  const visit = (dir) => {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))) {
      const fullPath = path.join(dir, entry.name);
      if (entry.isDirectory()) {
        visit(fullPath);
        continue;
      }
      if (!entry.isFile() || !entry.name.endsWith(".jsonl")) {
        continue;
      }
      for (const line of fs.readFileSync(fullPath, "utf8").split(/\r?\n/)) {
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
    }
  };
  visit(root);
  return events;
}

function parseDefaultModelName(configInput) {
  const settingsJSON = configInput.settings_json;
  if (typeof settingsJSON !== "string" || !settingsJSON.trim()) {
    return null;
  }
  try {
    const parsed = JSON.parse(settingsJSON);
    return typeof parsed.model === "string" && parsed.model.trim() ? parsed.model.trim() : null;
  } catch {
    return null;
  }
}

function extractTextReasoningToolUses(content) {
  if (typeof content === "string") {
    return [content.trim(), null, []];
  }
  const textParts = [];
  const reasoningParts = [];
  const toolBlocks = [];
  if (Array.isArray(content)) {
    for (const block of content) {
      if (!block || typeof block !== "object" || Array.isArray(block)) {
        textParts.push(stringify(block));
        continue;
      }
      const blockType = block.type;
      if (blockType === "tool_use") {
        toolBlocks.push(block);
        continue;
      }
      if (blockType === "thinking" || blockType === "reasoning" || blockType === "analysis") {
        const textValue = block.text === undefined ? block.thinking : block.text;
        reasoningParts.push(stringify(textValue).trim());
        continue;
      }
      if (blockType === "code" && typeof block.code === "string") {
        textParts.push(block.code);
        continue;
      }
      textParts.push(stringify(Object.prototype.hasOwnProperty.call(block, "text") ? block.text : block));
    }
  } else if (content !== null && content !== undefined && content !== "") {
    textParts.push(stringify(content));
  }
  const text = textParts.map((part) => part.trim()).filter(Boolean).join("\n\n");
  const reasoning = reasoningParts.map((part) => part.trim()).filter(Boolean).join("\n\n");
  return [text, reasoning || null, toolBlocks];
}

function buildMetrics(usage) {
  if (!usage || typeof usage !== "object" || Array.isArray(usage)) {
    return null;
  }
  const cachedTokens = usage.cache_read_input_tokens;
  let promptTokens = usage.input_tokens;
  if (Number.isInteger(promptTokens) && Number.isInteger(cachedTokens)) {
    promptTokens += cachedTokens;
  } else if (cachedTokens !== undefined && promptTokens === undefined) {
    promptTokens = cachedTokens;
  }
  const completionTokens = usage.output_tokens;
  const extra = {};
  for (const [key, value] of Object.entries(usage)) {
    if (key !== "input_tokens" && key !== "output_tokens") {
      extra[key] = value;
    }
  }
  if (promptTokens === undefined && completionTokens === undefined && cachedTokens === undefined && Object.keys(extra).length === 0) {
    return null;
  }
  return compactDict({
    prompt_tokens: promptTokens,
    completion_tokens: completionTokens,
    cached_tokens: cachedTokens,
    extra: compactDict(extra),
  });
}

function formatToolResult(block, toolUseResult) {
  const parts = [];
  const content = block.content;
  if (typeof content === "string") {
    if (content.trim()) {
      parts.push(content.trim());
    }
  } else if (Array.isArray(content)) {
    for (const item of content) {
      const textValue = stringify(item).trim();
      if (textValue) {
        parts.push(textValue);
      }
    }
  } else if (content !== null && content !== undefined && content !== "") {
    parts.push(stringify(content));
  }

  let metadata = null;
  if (toolUseResult && typeof toolUseResult === "object" && !Array.isArray(toolUseResult)) {
    metadata = { tool_use_result: toolUseResult };
    const formattedChunks = [];
    const stdout = toolUseResult.stdout;
    const stderr = toolUseResult.stderr;
    const exitCode = toolUseResult.exitCode === undefined ? toolUseResult.exit_code : toolUseResult.exitCode;
    if (stdout) {
      formattedChunks.push(`[stdout]\n${stdout}`.replace(/\s+$/, ""));
    }
    if (stderr) {
      formattedChunks.push(`[stderr]\n${stderr}`.replace(/\s+$/, ""));
    }
    if (exitCode !== undefined && exitCode !== null && exitCode !== 0) {
      formattedChunks.push(`[exit_code] ${exitCode}`);
    }
    if (toolUseResult.interrupted) {
      formattedChunks.push(`[interrupted] ${toolUseResult.interrupted}`);
    }
    if (toolUseResult.isImage) {
      formattedChunks.push(`[is_image] ${toolUseResult.isImage}`);
    }
    const remainingMeta = {};
    for (const [key, value] of Object.entries(toolUseResult)) {
      if (!["stdout", "stderr", "exitCode", "exit_code", "interrupted", "isImage"].includes(key)) {
        remainingMeta[key] = value;
      }
    }
    if (Object.keys(remainingMeta).length > 0) {
      formattedChunks.push(`[metadata] ${JSON.stringify(remainingMeta)}`);
    }
    if (formattedChunks.length > 0) {
      parts.push(formattedChunks.join("\n"));
    }
  }

  if (block.is_error === true) {
    parts.push("[error] tool reported failure");
    metadata = metadata || {};
    metadata.is_error = true;
  }
  if (metadata) {
    metadata.raw_tool_result = block;
  }
  const resultText = parts.filter(Boolean).join("\n\n").trim();
  return [resultText || null, metadata];
}

function buildSteps(events, defaultModelName) {
  const normalizedEvents = [];
  const pendingCalls = new Map();
  const seenMessageIds = new Set();

  for (const event of events) {
    const message = event.message;
    if (!message || typeof message !== "object" || Array.isArray(message)) {
      continue;
    }
    const eventType = event.type;
    const timestamp = event.timestamp;

    if (eventType === "assistant") {
      const [text, reasoning, toolBlocks] = extractTextReasoningToolUses(message.content);
      const messageId = message.id;
      let metrics = null;
      if (!(typeof messageId === "string" && seenMessageIds.has(messageId))) {
        metrics = buildMetrics(message.usage);
        if (typeof messageId === "string" && messageId) {
          seenMessageIds.add(messageId);
        }
      }

      const extra = compactDict({
        stop_reason: message.stop_reason,
        stop_sequence: message.stop_sequence,
        request_id: message.requestId,
        id: event.id,
        agent_id: event.agent_id,
        cwd: event.cwd,
        user_type: event.userType !== "external" ? event.userType : null,
        is_sidechain: event.isSidechain || false,
      });
      const modelName = message.model || defaultModelName;

      if (text || reasoning || toolBlocks.length === 0) {
        normalizedEvents.push(compactDict({
          timestamp,
          source: "agent",
          message: text || "",
          reasoning_content: reasoning,
          model_name: modelName,
          metrics,
          extra,
        }) || {});
        metrics = null;
      }

      toolBlocks.forEach((toolBlock, index) => {
        const callId = toolBlock.id || toolBlock.tool_use_id;
        if (typeof callId !== "string" || !callId.trim()) {
          return;
        }
        const rawArguments = toolBlock.input;
        const argumentsValue = rawArguments && typeof rawArguments === "object" && !Array.isArray(rawArguments)
          ? rawArguments
          : { input: rawArguments };
        const callExtra = { ...(extra || {}) };
        if (toolBlock.is_error !== undefined) {
          callExtra.tool_use_is_error = toolBlock.is_error;
        }
        if (toolBlock.name) {
          callExtra.tool_use_name = toolBlock.name;
        }
        pendingCalls.set(callId, compactDict({
          timestamp,
          source: "agent",
          message: null,
          reasoning_content: reasoning,
          model_name: modelName,
          metrics: index === 0 ? metrics : null,
          extra: compactDict(callExtra),
          tool_calls: [{
            tool_call_id: callId,
            function_name: String(toolBlock.name || ""),
            arguments: argumentsValue || {},
          }],
        }) || {});
        if (index === 0) {
          metrics = null;
        }
      });
      continue;
    }

    if (eventType === "user") {
      const content = message.content;
      if (typeof content === "string") {
        const text = content.trim();
        if (text) {
          normalizedEvents.push({
            timestamp,
            source: "user",
            message: text,
            extra: { is_sidechain: event.isSidechain || false },
          });
        }
        continue;
      }

      if (Array.isArray(content)) {
        const textParts = [];
        for (const block of content) {
          if (block && typeof block === "object" && !Array.isArray(block) && block.type === "tool_result") {
            const callId = block.tool_use_id;
            const [formattedOutput, metadata] = formatToolResult(block, event.toolUseResult);
            let step = typeof callId === "string" ? pendingCalls.get(callId) : null;
            if (typeof callId === "string") {
              pendingCalls.delete(callId);
            }
            if (!step) {
              step = {
                timestamp,
                source: "agent",
                message: null,
                model_name: defaultModelName,
                tool_calls: [{
                  tool_call_id: callId || "tool-result",
                  function_name: String(block.name || block.tool_name || ""),
                  arguments: {},
                }],
              };
            }
            const extra = { ...(step.extra || {}) };
            if (metadata) {
              extra.tool_result_metadata = metadata;
            }
            if (block.is_error !== undefined) {
              extra.tool_result_is_error = block.is_error;
            }
            step.extra = compactDict(extra);
            if (formattedOutput !== null) {
              const toolCallId = step.tool_calls[0].tool_call_id;
              step.observation = { results: [compactDict({ source_call_id: toolCallId, content: formattedOutput })] };
            }
            step.timestamp = step.timestamp || timestamp;
            normalizedEvents.push(compactDict(step) || {});
            continue;
          }
          const textValue = stringify(block).trim();
          if (textValue) {
            textParts.push(textValue);
          }
        }
        const textMessage = textParts.join("\n\n");
        if (textMessage) {
          normalizedEvents.push({ timestamp, source: "user", message: textMessage });
        }
        continue;
      }

      if (content !== null && content !== undefined && content !== "") {
        normalizedEvents.push({ timestamp, source: "user", message: stringify(content).trim() });
      }
    }
  }

  normalizedEvents.push(...pendingCalls.values());

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
      metrics: event.metrics,
      extra: event.extra,
    });
    if (step) {
      steps.push(step);
    }
  });
  return steps;
}

function buildFinalMetrics(steps) {
  const promptValues = steps
    .filter((step) => step.metrics && Number.isInteger(step.metrics.prompt_tokens))
    .map((step) => step.metrics.prompt_tokens);
  const completionValues = steps
    .filter((step) => step.metrics && Number.isInteger(step.metrics.completion_tokens))
    .map((step) => step.metrics.completion_tokens);
  const cachedValues = steps
    .filter((step) => step.metrics && Number.isInteger(step.metrics.cached_tokens))
    .map((step) => step.metrics.cached_tokens);

  const serviceTiers = new Set();
  let cacheCreationTotal = 0;
  let cacheCreationSeen = false;
  let cacheReadTotal = 0;
  let cacheReadSeen = false;
  for (const step of steps) {
    const metrics = step.metrics;
    if (!metrics || typeof metrics !== "object") {
      continue;
    }
    const extra = metrics.extra;
    if (!extra || typeof extra !== "object" || Array.isArray(extra)) {
      continue;
    }
    if (typeof extra.service_tier === "string" && extra.service_tier) {
      serviceTiers.add(extra.service_tier);
    }
    if (Number.isInteger(extra.cache_creation_input_tokens)) {
      cacheCreationTotal += extra.cache_creation_input_tokens;
      cacheCreationSeen = true;
    }
    if (Number.isInteger(extra.cache_read_input_tokens)) {
      cacheReadTotal += extra.cache_read_input_tokens;
      cacheReadSeen = true;
    }
  }

  const finalExtra = compactDict({
    service_tiers: serviceTiers.size > 0 ? Array.from(serviceTiers).sort() : null,
    total_cache_creation_input_tokens: cacheCreationSeen ? cacheCreationTotal : null,
    total_cache_read_input_tokens: cacheReadSeen ? cacheReadTotal : null,
  });
  return compactDict({
    total_prompt_tokens: promptValues.length > 0 ? promptValues.reduce((sum, value) => sum + value, 0) : null,
    total_completion_tokens: completionValues.length > 0 ? completionValues.reduce((sum, value) => sum + value, 0) : null,
    total_cached_tokens: cachedValues.length > 0 ? cachedValues.reduce((sum, value) => sum + value, 0) : null,
    total_steps: steps.length,
    extra: finalExtra,
  }) || { total_steps: steps.length };
}

const ctx = loadContext();
const run = ctx.run;
const configInput = (ctx.config || {}).input || {};
const install = ctx.install || {};
const claudeDir = path.join(ctx.paths.run_home, ".claude", "projects");
const rawEvents = readJSONLFiles(claudeDir);
if (rawEvents.length === 0) {
  process.stdout.write("null\n");
  process.exit(0);
}

rawEvents.sort((left, right) => String(left.timestamp || "").localeCompare(String(right.timestamp || "")));
const events = [
  ...rawEvents.filter((event) => event.isSidechain),
  ...rawEvents.filter((event) => !event.isSidechain),
];
let sessionId = "";
for (const event of events) {
  if (typeof event.sessionId === "string" && event.sessionId.trim()) {
    sessionId = event.sessionId.trim();
    break;
  }
}
if (!sessionId) {
  sessionId = String(run.session_id || "").trim();
}
if (!sessionId) {
  process.stdout.write("null\n");
  process.exit(0);
}

let version = "";
for (const event of events) {
  if (typeof event.version === "string" && event.version.trim()) {
    version = event.version.trim();
    break;
  }
}
if (!version) {
  version = String(install.resolved_version || install.version || configInput.claude_version || "unknown").trim();
}

let defaultModelName = parseDefaultModelName(configInput);
for (const event of events) {
  if (!event.message || typeof event.message !== "object" || Array.isArray(event.message)) {
    continue;
  }
  if (typeof event.message.model === "string" && event.message.model.trim()) {
    defaultModelName = event.message.model.trim();
    break;
  }
}

const cwds = Array.from(new Set(events.map((event) => event.cwd).filter((value) => typeof value === "string" && value))).sort();
const gitBranches = Array.from(new Set(events.map((event) => event.gitBranch).filter((value) => typeof value === "string" && value))).sort();
const agentIds = Array.from(new Set(events.map((event) => event.agentId).filter((value) => typeof value === "string" && value))).sort();
const agentExtra = compactDict({
  cwds: cwds.length > 0 ? cwds : null,
  git_branches: gitBranches.length > 0 ? gitBranches : null,
  agent_ids: agentIds.length > 0 ? agentIds : null,
});

const steps = buildSteps(events, defaultModelName);
if (steps.length === 0) {
  process.stdout.write("null\n");
  process.exit(0);
}

const trajectory = compactDict({
  schema_version: "ATIF-v1.6",
  session_id: sessionId,
  agent: compactDict({
    name: "claude-code",
    version: version || "unknown",
    model_name: defaultModelName,
    extra: agentExtra,
  }),
  steps,
  final_metrics: buildFinalMetrics(steps),
});
process.stdout.write(`${JSON.stringify(trajectory)}\n`);
