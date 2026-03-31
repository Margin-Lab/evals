# Pi

Repo-owned Pi uses:

- definition: `configs/agent-definitions/pi`
- default config: `configs/example-agent-configs/pi-default`
- unified config: `configs/example-agent-configs/pi-unified`

## Required Env

- none in the definition manifest

Pi auth is provider-specific. The repo-owned definition leaves provider credential env handling to the selected runtime environment and example config.

## Toolchains

- managed Node/npm

## Config Input

The Pi definition schema expects:

- `pi_version`
- `startup_args`
- `run_args`
- `provider`
- `model`
- `thinking`

The default profile launches Pi in `--mode json`, sets `PI_CODING_AGENT_DIR`, and uses a run-local `--session-dir`.

The unified profile requires a provider-qualified `model` such as `openai/gpt-5`, splits it into `provider` and `model`, and maps shared `reasoning_level` directly to Pi `thinking`.

If skills are configured, `agent-server` materializes them under `~/.agents/skills/<skill-name>/` before launch.

If `agents_md` is configured, `agent-server` writes `AGENTS.md` into the project root where Pi starts.

## Capabilities

- install: yes
- snapshot: no
- trajectory hook: yes

Runs persist a validated ATIF trajectory and expose `trajectory_status` plus `GET /v1/run/trajectory` after exit.

## Command Flow

See `../plugins/commands-pi.md` for the exact install and run command shape.
