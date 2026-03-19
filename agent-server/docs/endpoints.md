# agent-server Endpoints

All API endpoints use JSON except the PTY WebSocket. Errors use:

```json
{
  "error": {
    "code": "SOME_CODE",
    "message": "human-readable message",
    "details": {}
  }
}
```

Request bodies reject unknown fields.

## Health

### `GET /healthz`

Liveness probe. Returns `200`:

```json
{"status":"ok"}
```

### `GET /readyz`

Readiness probe. Checks:

- shutdown state
- writability of `bin` and `state`
- accessibility of `workspaces`
- readability of the state store
- existence of the materialized definition dir when a definition is loaded

Returns:

- `200` when ready
- `503` with per-check details when not ready

## State

### `GET /v1/state`

Returns the persisted server state:

- `agent`: loaded definition/config/install state
- `run`: current run state plus optional `run_id`
- `paths`: root, bin, state, and workspaces paths
- `capabilities`: inferred from the loaded definition
  - `supports_install`
  - `supports_snapshot`
  - `supports_trajectory`
  - `supports_skills`
  - `supports_agents_md`
  - `supports_unified_config`
  - `required_env`
  - optional `agents_md_filename`
  - optional `allowed_models`
  - optional `allowed_reasoning_levels`
- `shutting_down`

## Agent Lifecycle

### `PUT /v1/agent-definition`

Loads a packaged `DefinitionSnapshot`:

```json
{
  "definition": {
    "manifest": {},
    "package": {}
  }
}
```

Behavior:

- validates the snapshot
- materializes the packaged definition under the state root
- creates the install dir for that definition hash
- replaces any previously loaded definition/config/install selection
- requires no active run

Returns:

- `201` when a new definition is loaded
- `200` when the same definition is already selected

### `PUT /v1/agent-config`

Validates and persists a `ConfigSpec` for the active definition.

Direct mode request:

```json
{
  "config": {
    "name": "codex-default",
    "description": "optional",
    "mode": "direct",
    "input": {}
  }
}
```

Unified mode request:

```json
{
  "config": {
    "name": "codex-unified",
    "description": "optional",
    "mode": "unified",
    "unified": {
      "model": "gpt-5-codex",
      "reasoning_level": "medium"
    }
  }
}
```

Behavior:

- requires a loaded definition
- validates `config.name` and `config.mode`
- validates optional top-level packaged `skills` and rejects them when the selected definition does not declare a skill root
- validates optional top-level `agents_md` content and rejects it when the selected definition does not declare an `agents_md.filename`
- direct mode validates `config.input` against the definition schema
- unified mode validates the shared unified payload and, if supported by the loaded definition, runs `config.unified.translate`
- runs the optional `config.validate` hook against the resolved direct input
- stores a `ConfigSnapshot`
  - `skills` preserves normalized packaged skills for both modes
  - `agents_md` preserves raw markdown content for both modes
  - `input` always contains the resolved direct input used by install/run hooks
  - `unified` is preserved only when the submitted mode was `unified`

Returns:

- `201` on first successful config
- `200` when replacing an existing config for the same loaded definition

### `POST /v1/agent/install`

Runs the active definition install lifecycle.

Behavior:

- requires a loaded definition
- may run with or without a persisted config
- runs `install.check` first, if present
- runs `install.run` only when the check does not report `{"installed": true}`
- persists the returned install result

Returns:

- `200` with `state` and `install`

## Run Lifecycle

### `POST /v1/run`

Starts a PTY-backed run:

```json
{
  "cwd": "/marginlab/workspaces/case",
  "initial_prompt": "Solve the task",
  "args": [],
  "env": {},
  "dry_run": false,
  "pty": {"cols": 120, "rows": 40}
}
```

Validation rules:

- `cwd` is required
- `cwd` must already exist under `AGENT_SERVER_WORKSPACES_DIR`
- `initial_prompt` is required
- `args` may not contain empty strings
- request env keys may not be empty or contain `=`
- request env may not override definition `required_env`
- `dry_run` is optional and defaults to `false`

Behavior:

- requires loaded definition, config, and install result
- creates isolated run home and artifacts dirs under the state root
- resolves required env from the server process environment
- asks `run.prepare` for an `ExecSpec`
- launches the returned command in a PTY unless `dry_run=true`

Run hooks receive the resolved direct config input regardless of whether the submitted config used direct or unified mode.

When the selected definition declares a skill root, configured skills are materialized into the run `HOME` before `run.prepare` executes.

When the selected definition declares `agents_md.filename`, configured `agents_md` content is written into the resolved agent start directory after `run.prepare` returns its final `ExecSpec.Dir`.

When `dry_run=true`, the server still performs the full prelaunch path, including auth file materialization, skills setup, and `agents_md` writing, then records the run as immediately exited with exit code `0` and `trajectory_status=none`.

Returns `201` with:

- `run_id`
- `state`
- optional `pid`
- `started_at`
- optional `attach.ws_path`
- optional `attach.protocol`

### `GET /v1/run`

Returns the persisted run record:

- state
- run id
- pid
- cwd
- request env overrides
- started/ended timestamps
- exit code and signal
- trajectory status

Returns `404` with `RUN_NOT_ACTIVE` when no run exists.

### `GET /v1/run/trajectory`

Returns the persisted ATIF payload for the exited run.

Behavior:

- requires a run to exist
- requires the run state to be `exited`
- requires `trajectory_status=complete`
- reads `<state_dir>/runs/<run_id>/artifacts/trajectory.json`

Returns:

- `200` with `application/json` ATIF content
- `404` with `RUN_NOT_ACTIVE` when no run exists
- `404` with `TRAJECTORY_UNAVAILABLE` when the run has no completed trajectory
- `409` with `INVALID_RUN_STATE` when the run has not exited yet

### `DELETE /v1/run`

Behavior:

- stops an active run, if any
- or clears an exited run back to `idle`
- returns `200` with the resulting run state

## Snapshot

### `POST /v1/run/snapshot`

Requests a short-lived snapshot capture for the current run:

```json
{
  "run_id": "r_...",
  "pty": {"cols": 120, "rows": 40}
}
```

Behavior:

- requires a matching active or recently exited run id
- requires the active definition to expose `snapshot.prepare`
- returns an ANSI snapshot encoded as base64 text

Response fields:

- `run_id`
- `agent`
- `run_state`
- `captured_at`
- `content_type` always `ansi`
- `content_encoding` always `base64`
- `content`
- `truncated`

## PTY WebSocket

### `GET /v1/run/pty?run_id=<id>`

WebSocket attach endpoint. The protocol name returned by `POST /v1/run` is `pty.v1`.

Behavior:

- replays the last buffered PTY output on connect
- fans out live PTY output to all attached clients
- accepts input from any client
- closes all clients with a final exit message when the run ends

Message rules:

- server to client PTY output: binary frames
- client to server PTY input: binary frames
- client resize control: text JSON `{"type":"resize","cols":120,"rows":40}`
- server exit control: text JSON `{"type":"exit","exit_code":0}`

## Removed Endpoints

These old endpoints are gone and have no compatibility alias:

- `PUT /v1/agent`
- `PUT /v1/agent/config`
