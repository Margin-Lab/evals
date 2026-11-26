#!/usr/bin/env node
"use strict";

function isPlainObject(value) {
  return !!value && typeof value === "object" && !Array.isArray(value);
}

function parseJSONObject(text, label) {
  let value;
  try {
    value = JSON.parse(String(text || ""));
  } catch (error) {
    throw new Error(`${label} is not valid JSON: ${error.message}`);
  }
  if (!isPlainObject(value)) {
    throw new Error(`${label} must be a JSON object`);
  }
  return value;
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

module.exports = {
  deepMerge,
  isPlainObject,
  parseJSONObject,
};
