# Required Agent Env

`agent-server` resolves provider auth requirements from the loaded definition manifest:

```toml
[auth]
required_env = ["OPENAI_API_KEY"]

[[auth.local_credentials]]
required_env = "OPENAI_API_KEY"
run_home_rel_path = ".codex/auth.json"

  [[auth.local_credentials.sources]]
  kind = "home_file"
  home_rel_path = ".codex/auth.json"
```

## Contract

At run start, the server:

1. reads `manifest.auth.required_env`
2. rejects request env overrides for those keys
3. resolves each required key from either:
   - the server/container process environment
   - or a matching `auth_files[]` run-start entry
4. copies any `auth_files[]` payloads into `run_home`
5. merges base process env, request env, required env, and server-owned defaults such as `HOME`

The same required env set is also used when preparing snapshots and trajectory hooks for a run.

## Current Repo-Owned Definitions

- `codex`: `OPENAI_API_KEY`
- `claude-code`: `ANTHROPIC_API_KEY`
- `opencode`: `OPENAI_API_KEY`

Custom definitions may declare any env keys they need.

## What Request Env Can Do

`POST /v1/run` may include non-secret runtime `env` overrides, for example feature flags or agent-specific knobs.

Request env may not:

- override any key declared in `required_env`
- use empty keys
- use keys containing `=`

## Error Behavior

Relevant API error codes:

- `INVALID_ENV`
  - malformed request env
  - or request attempts to override a required env key
- `MISSING_REQUIRED_ENV`
  - one or more required env keys are missing or blank in the server process environment and were not satisfied by `auth_files[]`

## Notes

- Required env is resolved only at run time, not when loading a definition or config.
- Managed Node, when enabled, is injected through `PATH`; it is not treated as a secret.
- `auth.local_credentials` is resolved only by the caller before `POST /v1/run`. Repo-owned local CLI runs use it to discover and stage OAuth credential payloads for Codex and Claude Code without requiring provider API keys.
