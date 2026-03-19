# agent-server Design

`agent-server` is a generic runtime for packaged `agent_definition` artifacts. It no longer contains a live built-in plugin registry. Codex, Claude Code, and Opencode are ordinary repo-owned definitions that use the same lifecycle contract as custom agents.

## Core Model

The server persists exactly one selected agent at a time:

- one loaded `agent_definition`
- one validated `agent_config`
- one install result
- one run record

Only one run may be active at a time.

`agent_config` has two explicit modes:

- `direct`: the config already contains definition-owned `input`
- `unified`: the config contains shared `unified` input that the loaded definition translates into direct `input`

Both modes may also include shared packaged `skills`.

## Lifecycle

The runtime flow is:

1. Load a packaged definition snapshot.
2. Materialize the definition files under the server state root.
3. Validate and normalize the submitted config spec.
4. If `mode=unified`, run the definition's unified translate hook to produce direct `input`.
5. Validate any configured skills and ensure the selected definition supports them.
6. Validate the resolved direct `input` against the definition schema and optional validate hook.
7. Run install hooks, if present.
8. Materialize configured skill directories into the run home when the definition declares a skill root.
9. Ask the definition for a launch `ExecSpec` through `run.prepare`.
10. Start the returned process in a PTY.
11. Fan out PTY output to WebSocket clients and accept input and resize messages.
12. After exit, poll the optional trajectory hook until it returns valid ATIF or the collection timeout expires.

There is no per-agent image overlay step. Agent dependencies must be installed by hooks inside the case image.

## Hook Contract

Hooks are executable files referenced by the manifest. They are not restricted to Python.

A hook may be:

- a shell script with a shebang
- a Python script
- a Node script
- a compiled binary
- any other executable available in the container

Shared contract:

- hooks receive `AGENT_CONTEXT_JSON=<path>`
- the context file includes the loaded definition, normalized config, install result, server paths, optional toolchain info, and optional run/snapshot context
- hook `stdout` must contain only the expected JSON payload
- hook `stderr` is treated as logs and surfaced in errors/artifacts

Hook roles:

- `config.unified.translate`: returns direct config input JSON for unified-mode submissions
- `config.validate`: returns normalized config input JSON
- `install.check`: returns install status JSON
- `install.run`: performs installation and returns install result JSON
- `run.prepare`: returns an exec spec JSON `{path,args,env,dir}`
- `snapshot.prepare`: returns an exec spec for short-lived snapshot capture
- `trajectory.collect`: returns full ATIF JSON

Install, run, snapshot, and trajectory hooks always receive the resolved direct `config.input`. Unified input is preserved separately on the persisted config snapshot and is available in hook context only during translation and validation.

Hook context also includes configured skill metadata (`name`, `description`) without embedding the packaged archive payload.

## Toolchains

The only built-in managed toolchain is Node/npm. A definition opts into it with:

```toml
[toolchains.node]
minimum = "20"
preferred = "24"
```

When enabled, `agent-server` ensures managed Node before every hook phase, then prepends the managed bin dir to hook and run `PATH`. On Alpine it installs the image's `nodejs` package with `apk` and verifies the resulting major version is at least `minimum`. Everywhere else it downloads a pinned official Node archive in-process, verifies its checksum, and installs that runtime under the managed toolchain directory. It tries `preferred` first and falls back to `minimum` if needed.

Managed Node bootstrap does not rely on the case image's CA trust store. `agent-server` embeds a public root bundle, materializes a merged PEM file under `state/`, and uses that bundle both for Go HTTPS downloads and for managed Node/npm child processes via `NODE_EXTRA_CA_CERTS` and `NPM_CONFIG_CAFILE`. Operators can append private roots with `AGENT_SERVER_EXTRA_CA_CERTS_FILE`.

There is no built-in Python installer. Python hooks are still supported, but they rely on the case image already containing Python. The repo-owned definitions use JS hooks plus managed Node instead.

## State Machine

Agent states:

- `empty`
- `definition_loaded`
- `installed`
- `configured`

Run states:

- `idle`
- `starting`
- `running`
- `collecting`
- `exited`

Important rules:

- loading a new definition replaces the current definition/config/install selection
- config and install may happen in either order after a definition is loaded
- unified config support is optional and definition-owned
- skill support is optional and definition-owned via `skills.home_rel_dir`
- a run requires loaded definition, validated config, and install result
- an exited run must be cleared before the next run starts

## Repo-Owned Definitions

The repo-owned definitions live outside `agent-server` under `configs/agent-definitions/`. They are useful examples of the contract, but they are not special-cased by the runtime.
