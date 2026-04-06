# Trajectory Format

`agent-server` persists trajectories in ATIF JSON and does not normalize or synthesize a separate metadata record.

## Storage Contract

- trajectory hooks return full ATIF JSON on stdout or `null`
- `agent-server` validates the payload against the shared `runner-core/trajectory` schema
- valid payloads are written to `<state_dir>/runs/<run_id>/artifacts/trajectory.json`
- run state stores only `trajectory_status`, not the full JSON blob

## Trajectory Status Values

`RunRecord.trajectory_status` uses:

- `pending`
  - run has started and trajectory collection has not begun yet
- `collecting`
  - the process exited and the collector is polling the trajectory hook
- `none`
  - the definition does not expose a trajectory hook
- `complete`
  - the hook returned valid ATIF before timeout
- `failed`
  - the hook returned invalid data, returned nothing before timeout, or persistence failed

## Validation Rules

Validation uses the shared ATIF decoder in `runner-core/trajectory` and currently enforces:

- supported `schema_version`
- non-empty `session_id`
- non-empty `agent.name` and `agent.version`
- sequential `step_id`
- agent-only fields only on agent steps
- tool call / observation reference integrity
- valid text and multimodal content shapes

## Usage Metrics Contract

ATIF usage fields have two different scopes:

- `steps[].metrics.*`
  - step-local metrics for the emitted agent step
  - for prompt-side fields, repo-owned hooks treat these as single-request prompt snapshots
- `final_metrics.*`
  - canonical summary values for the run
  - for prompt-side fields, the canonical summary is the largest observed single-request prompt snapshot so it stays comparable to the model context window
  - for completion-side fields, hooks may still expose whole-run totals when the native source supports them

Rules for repo-owned hooks:

- do not synthesize prompt or cached-token summaries by summing step snapshots when those step values already include prior context
- populate `final_metrics.total_prompt_tokens` with the largest trustworthy prompt snapshot for the run
- populate `final_metrics.total_cached_tokens` with the largest trustworthy cached-token snapshot for the run when available
- if a trustworthy summary is unavailable for a metric, omit that `final_metrics` field
- consumers must read token counts from `final_metrics` only; do not reconstruct them from step metrics

## Repo-Owned Definitions

The repo-owned Codex, Claude Code, Gemini CLI, Opencode, and Pi definitions all emit `ATIF-v1.6`.
