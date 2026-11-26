"use strict";

// Offline tests for the Antigravity CLI hooks. Run: node --test configs/agent-definitions/antigravity-cli/hooks/test

const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { spawnSync } = require("node:child_process");
const test = require("node:test");

const hooksDir = path.resolve(__dirname, "..");
const agy = require("../lib/agy");
const { toTrajectory } = require("../lib/stream_json_to_atif");

function tempDir(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "agy-hooks-test-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

function step(index, fields) {
  return { event: "step_update", step_update: { conversation_id: "c-1", step_index: index, ...fields } };
}

test("release URLs follow the platform folders of Google's release bucket", () => {
  const url = agy.releaseArchiveURL("1.5.18-6386224290725888", "linux_amd64_musl");
  assert.equal(url, "https://storage.googleapis.com/antigravity-public/antigravity-cli/1.5.18-6386224290725888/linux-x64-musl/cli_linux_x64_musl.tar.gz");
  assert.equal(agy.releaseFromURL(url), "1.5.18-6386224290725888");
  assert.equal(agy.releaseVersion("1.5.18-6386224290725888"), "1.5.18");
  assert.equal(agy.releaseVersion("latest"), null);
  assert.throws(() => agy.releaseArchiveURL("1.5.18-1", "windows_amd64"), /no agy archive/);
});

for (const format of ["gnu", "pax"]) {
  test(`extracts the agy binary from a ${format} tar.gz with other entries`, async (t) => {
    const dir = tempDir(t);
    const src = path.join(dir, "src");
    fs.mkdirSync(path.join(src, "a".repeat(120)), { recursive: true });
    fs.writeFileSync(path.join(src, "a".repeat(120), "notes.txt"), "x".repeat(700));
    const binary = Buffer.alloc(1536 + 37, 7);
    fs.writeFileSync(path.join(src, "antigravity"), binary);
    const archive = path.join(dir, "agy.tar.gz");
    const tar = spawnSync("tar", [`--format=${format}`, "-czf", archive, "-C", src, "a".repeat(120), "antigravity"]);
    assert.equal(tar.status, 0, String(tar.stderr));
    const dest = path.join(dir, "out");
    await agy.extractTarGzEntry(archive, "antigravity", dest);
    assert.deepEqual(fs.readFileSync(dest), binary);
    await assert.rejects(agy.extractTarGzEntry(archive, "missing", path.join(dir, "none")), /no file named missing/);
  });
}

test("converts a stream-json run into ATIF steps, tool calls and metrics", () => {
  const trajectory = toTrajectory([
    { event: "init", init: { model: "gemini-4-neon-high", permission_mode: "always-proceed" } },
    step(0, { state: "DONE", step_type: "user_input", text_delta: "Fix the bug." }),
    step(1, { state: "ACTIVE", step_type: "agent_response", text_delta: "Looking " }),
    step(1, { state: "DONE", step_type: "agent_response", text_delta: "Looking at it.", usage: { input_tokens: 900, output_tokens: 10, thinking_tokens: 5 } }),
    step(2, { state: "ACTIVE", step_type: "tool", tool_name: "view_file" }),
    step(2, { state: "DONE", step_type: "tool", tool_info: { name: "view_file", parameters: { path: "a.py" }, output: "code" } }),
    step(3, { state: "DONE", step_type: "tool", tool_info: { name: "run_command", parameters: "{\"command\":\"pytest\"}", error: { type: "EXIT", message: "exit 1" } } }),
    step(4, { state: "DONE", step_type: "checkpoint" }),
    step(5, { state: "DONE", step_type: "agent_response", text_delta: "Done.", usage: { input_tokens: 1200, output_tokens: 3, cache_read_tokens: 800 } }),
    { event: "result", result: { conversation_id: "c-1", status: "SUCCESS", num_turns: 1, usage: { input_tokens: 2100, output_tokens: 13, thinking_tokens: 5 } } },
  ], { version: "1.5.18" });

  assert.equal(trajectory.schema_version, "ATIF-v1.6");
  assert.equal(trajectory.session_id, "c-1");
  assert.deepEqual(trajectory.agent, { name: "antigravity-cli", version: "1.5.18", model_name: "gemini-4-neon-high" });
  assert.deepEqual(trajectory.steps.map((s) => [s.step_id, s.source, s.message]), [
    [1, "user", "Fix the bug."],
    [2, "agent", "Looking at it."],
    [3, "agent", "Done."],
  ]);
  const toolStep = trajectory.steps[1];
  assert.deepEqual(toolStep.tool_calls.map((c) => [c.function_name, c.arguments]), [
    ["view_file", { path: "a.py" }],
    ["run_command", { command: "pytest" }],
  ]);
  assert.deepEqual(toolStep.observation.results.map((r) => [r.source_call_id, r.content]), [
    [toolStep.tool_calls[0].tool_call_id, "code"],
    [toolStep.tool_calls[1].tool_call_id, "Error: exit 1"],
  ]);
  // The largest prompt counts its cached part; output already includes thinking.
  assert.equal(trajectory.final_metrics.total_prompt_tokens, 2000);
  assert.equal(trajectory.final_metrics.total_completion_tokens, 13);
  assert.equal(trajectory.final_metrics.total_cached_tokens, 800);
  assert.equal(trajectory.final_metrics.total_steps, 3);
  assert.equal(trajectory.final_metrics.extra.tool_calls, 2);
});

test("handles agy 1.5.18's real event shapes", () => {
  // Shapes from a trial run on 2026-11-26: the prompt isn't repeated, tool_info
  // arrives on the ACTIVE event and gains its output at DONE, and some steps are
  // system messages.
  const conversation = "5bc020a3-1519-4681-931a-9a71a825e5f3";
  const trajectory = toTrajectory([
    { event: "init", conversation_id: conversation, init: { model: "gemini-4-neon-high", cwd: "/app" } },
    { event: "step_update", step_update: { conversation_id: conversation, step_index: 0, state: "DONE", step_type: "user_input" } },
    { event: "step_update", step_update: { conversation_id: conversation, step_index: 1, state: "ACTIVE", step_type: "agent_response", text_delta: "Reading the iterator." } },
    { event: "step_update", step_update: { conversation_id: conversation, step_index: 1, state: "DONE", step_type: "agent_response", duration_seconds: 5.5, usage: { input_tokens: 11078, output_tokens: 272, thinking_tokens: 86, cache_read_tokens: 0, total_tokens: 11350 } } },
    { event: "step_update", step_update: { conversation_id: conversation, step_index: 2, state: "ACTIVE", step_type: "tool", tool_name: "read_file", tool_info: { name: "read_file", parameters: { Path: "/app/lib/ansible/executor/play_iterator.py" } } } },
    { event: "step_update", step_update: { conversation_id: conversation, step_index: 2, state: "DONE", step_type: "tool", tool_name: "read_file", duration_seconds: 1.1, tool_info: { name: "read_file", parameters: { Path: "/app/lib/ansible/executor/play_iterator.py" }, output: "560 lines, 26440 bytes" } } },
    { event: "step_update", step_update: { conversation_id: conversation, step_index: 3, state: "DONE", step_type: "system_message" } },
    { event: "step_update", step_update: { conversation_id: conversation, step_index: 4, state: "DONE", step_type: "system_message", text_delta: "Context checkpoint saved." } },
    { event: "result", result: { conversation_id: conversation, status: "SUCCESS", response: "Standardized PlayIterator states.", usage: { input_tokens: 236763, output_tokens: 63649, thinking_tokens: 39499, cache_read_tokens: 2150858, total_tokens: 300412 } } },
  ], { version: "1.5.18", initialPrompt: "Fix the play iterator." });

  assert.equal(trajectory.session_id, conversation);
  assert.deepEqual(trajectory.steps.map((s) => [s.source, s.message]), [
    ["user", "Fix the play iterator."],
    ["agent", "Reading the iterator."],
    ["system", "Context checkpoint saved."],
  ]);
  assert.deepEqual(trajectory.steps[1].tool_calls[0].arguments, { Path: "/app/lib/ansible/executor/play_iterator.py" });
  assert.equal(trajectory.steps[1].observation.results[0].content, "560 lines, 26440 bytes");
  assert.deepEqual(trajectory.steps[1].metrics, { prompt_tokens: 11078, completion_tokens: 272, cached_tokens: 0 });
  assert.equal(trajectory.final_metrics.total_completion_tokens, 63649);
  assert.equal(trajectory.final_metrics.extra.usage_totals.cache_read_tokens, 2150858);
  assert.equal(trajectory.final_metrics.extra.step_types.system_message, 2);
});

test("a run that fails before any step keeps its error; an empty stream has no trajectory", () => {
  const trajectory = toTrajectory([
    { event: "result", result: { conversation_id: "c-2", status: "ERROR", error: "You have exhausted your quota on this model." } },
  ], { version: "1.5.18", model: "gemini-4-neon-high" });
  assert.equal(trajectory.steps.length, 1);
  assert.match(trajectory.steps[0].message, /exhausted your quota/);
  assert.equal(trajectory.final_metrics.extra.status, "ERROR");
  assert.match(trajectory.final_metrics.extra.error, /exhausted your quota/);
  assert.equal(toTrajectory([], {}), null);
});

function prepare(t, { apiKey = false, signedIn = true } = {}) {
  const dir = tempDir(t);
  const runHome = path.join(dir, "home");
  const agyDir = path.join(runHome, ".gemini", "antigravity-cli");
  fs.mkdirSync(agyDir, { recursive: true });
  if (signedIn) {
    fs.writeFileSync(path.join(agyDir, "antigravity-oauth-token"), "{}");
  }
  const env = { HOME: runHome, PATH: "/usr/bin:/bin", DBUS_SESSION_BUS_ADDRESS: "unix:path=/run/user/1000/bus" };
  if (apiKey) {
    env.GEMINI_API_KEY = "test-key";
  }
  const contextPath = path.join(dir, "context.json");
  fs.writeFileSync(contextPath, JSON.stringify({
    paths: { install_dir: path.join(dir, "install"), run_home: runHome, artifacts_dir: path.join(dir, "artifacts") },
    install: { bin_path: "/opt/agy/bin/agy", version: "1.5.18" },
    config: { input: { agy_version: "latest", startup_args: [], run_args: [], model: "gemini-4-neon-high", effort: "high", settings_json: "{\"showThinking\":false}" } },
    run: { cwd: "/app", initial_prompt: "Fix it's bug", env },
  }));
  const result = spawnSync(process.execPath, [path.join(hooksDir, "run-prepare.js")], {
    env: { ...process.env, AGENT_CONTEXT_JSON: contextPath },
    encoding: "utf8",
  });
  return { result, agyDir };
}

test("run-prepare launches agy headless with the saved sign-in", (t) => {
  const { result, agyDir } = prepare(t);
  assert.equal(result.status, 0, result.stderr);
  const spec = JSON.parse(result.stdout);
  assert.equal(spec.path, "bash");
  assert.equal(spec.dir, "/app");
  assert.equal(spec.env.DBUS_SESSION_BUS_ADDRESS, undefined);
  assert.equal(spec.env.GEMINI_API_KEY, undefined);
  assert.match(spec.args[1], /\/opt\/agy\/bin\/agy --model gemini-4-neon-high --effort high --output-format stream-json --dangerously-skip-permissions -p 'Fix it'"'"'s bug' < \/dev\/null/);
  assert.deepEqual(JSON.parse(fs.readFileSync(path.join(agyDir, "settings.json"), "utf8")), { showThinking: false });
  assert.ok(fs.existsSync(path.join(agyDir, "last_check.timestamp")));
});

test("run-prepare selects the Gemini API with an API key, and fails with neither", (t) => {
  const withKey = prepare(t, { apiKey: true, signedIn: false });
  assert.equal(withKey.result.status, 0, withKey.result.stderr);
  assert.equal(JSON.parse(withKey.result.stdout).env.GEMINI_API_KEY, "test-key");
  assert.equal(JSON.parse(fs.readFileSync(path.join(withKey.agyDir, "settings.json"), "utf8")).modelProvider, "gemini");

  const neither = prepare(t, { apiKey: false, signedIn: false });
  assert.notEqual(neither.result.status, 0);
  assert.match(neither.result.stderr, /no GEMINI_API_KEY and no Antigravity sign-in/);
});
