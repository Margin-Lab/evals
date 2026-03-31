# Opencode

Repo-owned Opencode uses:

- definition: `configs/agent-definitions/opencode`
- default config: `configs/example-agent-configs/opencode-default`
- unified config: `configs/example-agent-configs/opencode-unified`

## Required Env

- `OPENAI_API_KEY`

The repo-owned default profile targets an OpenAI-backed model through `config_jsonc`, so it is documented as requiring OpenAI credentials.

## Toolchains

- managed Node/npm

## Config Input

The Opencode definition schema expects:

- `opencode_version`
- `startup_args`
- `run_args`
- `config_jsonc`

The default profile writes `config_jsonc` to `~/.opencode/opencode.jsonc` and sets `OPENCODE_CONFIG` before launch.

The unified profile translates the shared `model` / `reasoning_level` payload into the direct Opencode config JSONC before install or run hooks execute.

If skills are configured, `agent-server` materializes them under `~/.config/opencode/skills/<skill-name>/` before launch.

If `agents_md` is configured, `agent-server` writes `AGENTS.md` into the project root where Opencode starts.

## Capabilities

- install: yes
- snapshot: no
- trajectory hook: yes

Runs persist a validated ATIF trajectory and expose `trajectory_status` plus `GET /v1/run/trajectory` after exit.

## Command Flow

See `../plugins/commands-opencode.md` for the exact install and run command shape.
