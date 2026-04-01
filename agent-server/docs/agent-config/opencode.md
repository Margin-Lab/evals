# Opencode

Repo-owned Opencode uses:

- definition: `configs/agent-definitions/opencode`
- default config: `configs/example-agent-configs/opencode-default`
- unified config: `configs/example-agent-configs/opencode-unified`

## Required Env

- provider-qualified auth selected from config:
  - `openai/*` -> `OPENAI_API_KEY`
  - `anthropic/*` -> `ANTHROPIC_API_KEY`
  - `google/*` -> `GEMINI_API_KEY`
  - `*` -> no required secret env

## Toolchains

- managed Node/npm

## Config Input

The Opencode definition schema expects:

- `opencode_version`
- `startup_args`
- `run_args`
- `provider`
- `config_jsonc`

`provider` is the authoritative auth selector for direct configs. `config_jsonc` must not disagree with the provider encoded in its `model` field.

The default profile writes `config_jsonc` to `~/.opencode/opencode.jsonc` and sets `OPENCODE_CONFIG` before launch.

The unified profile requires `model = "provider/model"` and translates the shared `model` / `reasoning_level` payload into direct Opencode input before install or run hooks execute.

Unknown providers fall through to the wildcard no-auth entry. Use `--agent-env` for any manual runtime variables they need.

If skills are configured, `agent-server` materializes them under `~/.config/opencode/skills/<skill-name>/` before launch.

If `agents_md` is configured, `agent-server` writes `AGENTS.md` into the project root where Opencode starts.

## Capabilities

- install: yes
- snapshot: no
- trajectory hook: yes

Runs persist a validated ATIF trajectory and expose `trajectory_status` plus `GET /v1/run/trajectory` after exit.

## Command Flow

See `../plugins/commands-opencode.md` for the exact install and run command shape.
