# Gemini CLI

Repo-owned Gemini CLI uses:

- definition: `configs/agent-definitions/gemini-cli`
- default config: `configs/example-agent-configs/gemini-cli-default`
- unified config: `configs/example-agent-configs/gemini-cli-unified`

## Required Env

- `GEMINI_API_KEY`

The definition also supports reusable local OAuth credentials from `~/.gemini/oauth_creds.json` through `auth.local_credentials`.

## Toolchains

- managed Node/npm

## Config Input

The Gemini CLI definition schema expects:

- `gemini_version`
- `startup_args`
- `run_args`
- `model`
- `approval_mode`
- `settings_json`

The default profile writes `settings_json` to `~/.gemini/settings.json` inside the run home and then launches Gemini CLI in `--output-format stream-json`.

The unified profile translates the shared `model` payload into the direct Gemini fields above. Unified `reasoning_level` is accepted for cross-agent compatibility and preserved on the config snapshot, but Gemini CLI does not expose a reasoning-effort flag, so the repo-owned translator does not map it to any runtime setting.

If unified MCP servers are configured, the translator renders them into Gemini `settings_json.mcpServers`.

If skills are configured, `agent-server` materializes them under `~/.agents/skills/<skill-name>/` before launch.

If `agents_md` is configured, `agent-server` writes `AGENTS.md` into the project root where Gemini CLI starts. The run hook also forces Gemini `context.fileName` to include both `AGENTS.md` and `GEMINI.md`.

## Capabilities

- install: yes
- snapshot: no
- trajectory hook: yes

Runs persist a validated ATIF trajectory and expose `trajectory_status` plus `GET /v1/run/trajectory` after exit.

## Command Flow

See `../plugins/commands-gemini-cli.md` for the exact install and run command shape.
