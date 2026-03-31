# Codex

Repo-owned Codex uses:

- definition: `configs/agent-definitions/codex`
- default config: `configs/example-agent-configs/codex-default`
- unified config: `configs/example-agent-configs/codex-unified`

## Required Env

- `OPENAI_API_KEY`

## Toolchains

- managed Node/npm

## Config Input

The Codex definition schema expects:

- `codex_version`
- `startup_args`
- `run_args`
- `config_toml`

The default profile writes `config_toml` to `~/.codex/config.toml` inside the run home and then launches `codex exec ...`.

The unified profile uses the shared `model` / `reasoning_level` contract and translates it into the direct Codex fields above before install or run hooks execute.

If skills are configured, `agent-server` materializes them under `~/.agents/skills/<skill-name>/` before launch.

If `agents_md` is configured, `agent-server` writes `AGENTS.md` into the project root where Codex starts.

## Capabilities

- install: yes
- snapshot: yes
- trajectory hook: yes

Runs persist a validated ATIF trajectory and expose `trajectory_status` plus `GET /v1/run/trajectory` after exit.

## Command Flow

See `../plugins/commands-codex.md` for the exact install, run, and snapshot command shape.
