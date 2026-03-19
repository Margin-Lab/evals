# Claude Code

Repo-owned Claude Code uses:

- definition: `configs/agent-definitions/claude-code`
- default config: `configs/agent-configs/claude-code-default`
- unified config: `configs/agent-configs/claude-code-unified`

## Required Env

- `ANTHROPIC_API_KEY`

## Toolchains

- managed Node/npm

## Config Input

The Claude Code definition schema expects:

- `claude_version`
- `startup_args`
- `run_args`
- `settings_json`
- optional `mcp_json`

The default profile writes `settings_json` to `~/.claude/settings.json`. If `mcp_json` is provided, it is written to `~/.mcp.json` for the run.

The unified profile translates the shared `model` / `reasoning_level` payload into Claude Code startup args and generated settings JSON before the direct validation hook runs.

If skills are configured, `agent-server` materializes them under `~/.claude/skills/<skill-name>/` before launch.

If `agents_md` is configured, `agent-server` writes `CLAUDE.md` into the project root where Claude Code starts.

## Capabilities

- install: yes
- snapshot: yes
- trajectory hook: yes

Runs persist a validated ATIF trajectory and expose `trajectory_status` plus `GET /v1/run/trajectory` after exit.

## Command Flow

See `../plugins/commands-claude-code.md` for the exact install, run, and snapshot command shape.
