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

function imageExtension(mimeType) {
  switch (mimeType) {
    case "image/png":
      return "png";
    case "image/jpeg":
      return "jpg";
    case "image/gif":
      return "gif";
    case "image/webp":
      return "webp";
    default:
      return "";
  }
}

function saveImage(artifactsDir, stepID, observationIndex, imageIndex, image) {
  const extension = imageExtension(image.mimeType);
  if (!extension || !image.data) {
    return null;
  }
  const imagesDir = path.join(artifactsDir, "images");
  fs.mkdirSync(imagesDir, { recursive: true });
  const filename = `step_${stepID}_obs_${observationIndex}_img_${imageIndex}.${extension}`;
  const outputPath = path.join(imagesDir, filename);
  fs.writeFileSync(outputPath, Buffer.from(image.data, "base64"));
  return {
    type: "image",
    source: {
      media_type: image.mimeType,
      path: path.posix.join("images", filename),
    },
  };
}

function textPartsToString(parts) {
  const text = [];
  for (const part of parts || []) {
    if (part && part.type === "text" && typeof part.text === "string") {
      text.push(part.text);
    }
  }
  return text.join("");
}

function buildObservationContent(artifactsDir, stepID, observationIndex, parts) {
  const textParts = [];
  const imageParts = [];
  let imageIndex = 0;
  for (const part of parts || []) {
    if (!part || typeof part !== "object") {
      continue;
    }
    if (part.type === "text" && typeof part.text === "string") {
      textParts.push(part.text);
      continue;
    }
    if (part.type === "image") {
      const saved = saveImage(artifactsDir, stepID, observationIndex, imageIndex, part);
      imageIndex += 1;
      if (saved) {
        imageParts.push(saved);
      }
    }
  }
  if (imageParts.length === 0) {
    return textParts.join("");
  }
  const content = [];
  if (textParts.length > 0) {
    content.push({ type: "text", text: textParts.join("") });
  }
  content.push(...imageParts);
  return content;
}

function createAssistantStep(message, stepID) {
  const textBlocks = [];
  const reasoningBlocks = [];
  const toolCalls = [];
  for (const part of message.content || []) {
    if (!part || typeof part !== "object") {
      continue;
    }
    if (part.type === "text" && typeof part.text === "string") {
      textBlocks.push(part.text);
      continue;
    }
    if (part.type === "thinking" && typeof part.thinking === "string" && !part.redacted) {
      reasoningBlocks.push(part.thinking);
      continue;
    }
    if (part.type === "toolCall") {
      toolCalls.push({
        tool_call_id: String(part.id || ""),
        function_name: String(part.name || ""),
        arguments: part.arguments && typeof part.arguments === "object" ? part.arguments : {},
      });
    }
  }

  const promptTokens = Number(message.usage?.input || 0) + Number(message.usage?.cacheRead || 0);
  const completionTokens = Number(message.usage?.output || 0);
  const cachedTokens = Number(message.usage?.cacheRead || 0);
  const costUSD = Number(message.usage?.cost?.total || 0);

  return {
    step_id: stepID,
    timestamp: new Date(Number(message.timestamp || 0)).toISOString(),
    source: "agent",
    model_name: typeof message.model === "string" ? message.model : undefined,
    message: textBlocks.join("") || (toolCalls.length > 0 ? "(tool use)" : reasoningBlocks.join("")),
    reasoning_content: reasoningBlocks.length > 0 ? reasoningBlocks.join("\n") : undefined,
    tool_calls: toolCalls.length > 0 ? toolCalls : undefined,
    observation: toolCalls.length > 0 ? { results: [] } : undefined,
    metrics: {
      prompt_tokens: promptTokens,
      completion_tokens: completionTokens,
      cached_tokens: cachedTokens || undefined,
      cost_usd: costUSD || undefined,
      extra: Number(message.usage?.cacheWrite || 0) > 0 ? { cache_write_tokens: Number(message.usage.cacheWrite) } : undefined,
    },
  };
}

function loadTrajectory(inputPath, artifactsDir, version) {
  const events = readJSONLLines(inputPath);
  const header = events.find((event) => event && event.type === "session");
  const terminal = [...events].reverse().find((event) => event && event.type === "agent_end" && Array.isArray(event.messages));
  if (!header || !header.id || !terminal) {
    return null;
  }

  let nextStepID = 1;
  let totalCompletionTokens = 0;
  let totalCostUSD = 0;
  let agentModelName = "";
  const steps = [];
  let pendingToolStep = null;

  for (const message of terminal.messages) {
    if (!message || typeof message !== "object") {
      continue;
    }
    if (message.role === "user") {
      pendingToolStep = null;
      steps.push({
        step_id: nextStepID++,
        timestamp: new Date(Number(message.timestamp || 0)).toISOString(),
        source: "user",
        message: typeof message.content === "string" ? message.content : textPartsToString(message.content),
      });
      continue;
    }
    if (message.role === "assistant") {
      const step = createAssistantStep(message, nextStepID++);
      steps.push(step);
      if (!agentModelName && typeof message.model === "string") {
        agentModelName = message.model;
      }
      totalCompletionTokens += Number(step.metrics.completion_tokens || 0);
      totalCostUSD += Number(step.metrics.cost_usd || 0);
      pendingToolStep = step.tool_calls && step.tool_calls.length > 0 ? step : null;
      continue;
    }
    if (message.role === "toolResult" && pendingToolStep && pendingToolStep.observation) {
      pendingToolStep.observation.results.push({
        source_call_id: String(message.toolCallId || ""),
        content: buildObservationContent(
          artifactsDir,
          pendingToolStep.step_id,
          pendingToolStep.observation.results.length,
          message.content,
        ),
      });
    }
  }

  if (steps.length === 0) {
    return null;
  }

  return {
    schema_version: "ATIF-v1.6",
    session_id: String(header.id),
    agent: {
      name: "pi",
      version,
      model_name: agentModelName || undefined,
    },
    steps,
    final_metrics: {
      total_completion_tokens: totalCompletionTokens,
      total_cost_usd: totalCostUSD || undefined,
      total_steps: steps.length,
    },
  };
}

module.exports = {
  loadTrajectory,
};
