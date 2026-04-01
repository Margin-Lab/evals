# Runtime Execution Notes

This runbook documents the current execution shape used by the repo-owned `agent_definition` packages.

## Baseline Expectations

- each run gets an isolated `HOME`
- each run uses a workspace path under `AGENT_SERVER_WORKSPACES_DIR`
- the agent process is launched in a PTY
- provider credentials declared in `required_env` must already exist in the server/container environment
- raw PTY output is persisted to the run artifacts directory

## Shared Orchestration Pattern

For all definitions, `agent-server` currently does the same high-level sequence:

1. create `run_id`, isolated run home, and artifacts dir
2. resolve definition-declared required env
3. call `run.prepare`
4. launch the returned command in a PTY
5. stream PTY output to clients and `pty.log`
6. after exit, poll `trajectory.collect` until the hook returns valid ATIF or the timeout expires
7. write `trajectory.json` when a valid payload is available

## Current Repo-Owned Definitions

### Codex

- required env: `OPENAI_API_KEY`
- definition path: `configs/agent-definitions/codex/`
- snapshot support: yes
- launch/bootstrap: `hooks/run-prepare.js`
- trajectory hook: `hooks/trajectory-collect.js`

### Claude Code

- required env: `ANTHROPIC_API_KEY`
- definition path: `configs/agent-definitions/claude-code/`
- snapshot support: yes
- launch/bootstrap: `hooks/run-prepare.js`
- trajectory hook: `hooks/trajectory-collect.js`

### Gemini CLI

- required env: `GEMINI_API_KEY`
- definition path: `configs/agent-definitions/gemini-cli/`
- snapshot support: no
- launch/bootstrap: `hooks/run-prepare.js`
- trajectory hook: `hooks/trajectory-collect.js`

### Opencode

- required env: provider-qualified
  - `openai/*` -> `OPENAI_API_KEY`
  - `anthropic/*` -> `ANTHROPIC_API_KEY`
  - `google/*` -> `GEMINI_API_KEY`
- definition path: `configs/agent-definitions/opencode/`
- snapshot support: no
- launch/bootstrap: `hooks/run-prepare.js`
- trajectory hook: `hooks/trajectory-collect.js`

### Pi

- required env: provider-qualified
  - `openai/*` -> `OPENAI_API_KEY`
  - `anthropic/*` -> `ANTHROPIC_API_KEY`
  - `google/*` -> `GEMINI_API_KEY`
- definition path: `configs/agent-definitions/pi/`
- snapshot support: no
- launch/bootstrap: `hooks/run-prepare.js`
- trajectory hook: `hooks/trajectory-collect.js`

See `trajectory-format.md` for the ATIF storage contract and status semantics.
