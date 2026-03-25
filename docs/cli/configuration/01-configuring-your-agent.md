# Configuring Your Agent

Create an agent config to choose which agent, model, and agent-specific setup Margin should use during an eval.

Start by scaffolding a config from an existing definition:

```bash
margin init agent-config \
  --agent-config ./configs/my-agent-configs/codex-gpt5 \
  --definition ./configs/agent-definitions/codex
```

## Recommended: unified config

Use `mode = "unified"` by default.

Unified config is the recommended format because it gives you one simple shape that works across supported agents. It is the best choice when you want to compare agents side-by-side or keep your config easy to understand.

Here is a fully-featured unified config:

```toml
kind = "agent_config"
name = "codex-gpt5-unified"
description = "Codex with shared instructions, one skill, and one MCP server"
definition = "../../agent-definitions/codex"
mode = "unified"

[[skills]]
path = "./skills/repo-search"

[agents_md]
path = "./AGENTS.md"

[unified]
model = "gpt-5"
reasoning_level = "medium"

[[unified.mcp.servers]]
name = "filesystem"
transport = "stdio"
enabled = true
timeout_ms = 15000
command = ["npx", "-y", "@modelcontextprotocol/server-filesystem", "/workspace"]

[unified.mcp.servers.env]
LOG_LEVEL = "warn"
```

## What each part does

### Top-level fields

- `kind` must be `"agent_config"`.
- `name` is the label shown in runs and output bundles.
- `description` is optional, but useful when you have several similar configs.
- `definition` points to the agent definition this config uses.
- `mode = "unified"` tells Margin to use the shared config format.

### `[[skills]]`

Skills are optional reusable instruction bundles. Each `path` must point to a directory that contains a `SKILL.md` file.

You can add multiple skills:

```toml
[[skills]]
path = "./skills/repo-search"

[[skills]]
path = "./skills/test-writing"
```

If the selected agent supports skills, Margin packages them and installs them into that agent's skill directory before the run starts.

### `[agents_md]`

This lets you provide a shared instructions file for the repo being evaluated:

```toml
[agents_md]
path = "./AGENTS.md"
```

Margin copies the file contents into the run and writes the correct filename automatically:

- Codex and OpenCode use `AGENTS.md`
- Claude Code uses `CLAUDE.md`

### `[unified]`

The required unified fields are:

```toml
[unified]
model = "gpt-5"
reasoning_level = "medium"
```

- `model` must be allowed by the selected agent definition.
- `reasoning_level` must be allowed by the selected agent definition.

### `[[unified.mcp.servers]]`

Unified config also supports optional MCP servers.

Each server needs:

- `name`
- `transport`

For `transport = "stdio"`, set:

- `command = [...]`
- optional `[unified.mcp.servers.env]`

For `transport = "http"` or `transport = "sse"`, set:

- `url`
- optional `headers`
- optional `oauth`

Optional fields for all transports:

- `enabled`
- `timeout_ms`

Example with a remote MCP server:

```toml
[[unified.mcp.servers]]
name = "company-api"
transport = "http"
url = "https://mcp.example.com"
timeout_ms = 10000
```

## How to modify the unified example

The usual edits are simple:

1. Change `definition` if you want a different agent implementation.
2. Change `unified.model` to the model you want for that agent.
3. Change `unified.reasoning_level` to `low`, `medium`, or `high`.
4. Add or remove `[[skills]]` blocks.
5. Add or remove `[agents_md]` if you want shared repo instructions.
6. Add `[[unified.mcp.servers]]` blocks if the agent should use MCP tools.

For example, to switch the example above from Codex to Claude Code:

```toml
definition = "../../agent-definitions/claude-code"

[unified]
model = "sonnet"
reasoning_level = "medium"
```

Everything else can stay the same.

## Fallback: direct config

If the agent has native configuration that is not exposed through the unified contract, use `mode = "direct"` instead.

Direct mode lets you set the agent config in that agent's native format. The exact `[input]` shape comes from the selected definition's `schema.json`.

Here is a Codex direct config example:

```toml
kind = "agent_config"
name = "codex-direct"
definition = "../../agent-definitions/codex"
mode = "direct"

[[skills]]
path = "./skills/repo-search"

[agents_md]
path = "./AGENTS.md"

[input]
codex_version = "latest"
startup_args = []
run_args = []
config_toml = """
model = "gpt-5.1-codex"
model_reasoning_effort = "high"
approval_policy = "never"
sandbox_mode = "workspace-write"
"""
```

In this example:

- `codex_version`, `startup_args`, and `run_args` are the required wrapper fields for the repo-owned Codex definition.
- `config_toml` is native Codex config content written directly into the run.

Use direct mode when you need settings that the shared unified format does not cover yet.

For the repo-owned definitions in this repo, the native config payload is:

- Codex: `config_toml`
- Claude Code: `settings_json` and optional `mcp_json`
- OpenCode: `config_jsonc`

## Passing env vars and bind mounts at run time

Some run-specific settings belong on the `margin run` command rather than in `config.toml`:

```bash
margin run \
  --suite ./suites/my-suite \
  --agent-config ./configs/my-agent-configs/codex-gpt5 \
  --eval ./my-eval.toml \
  --agent-env MY_VAR=my-value \
  --agent-bind /host/path=/container/path
```

Both flags are repeatable.

## Next steps

- [Configuring Your Eval](./02-configuring-your-eval.md)
- [Creating Your Own Eval](../creating-your-own-eval/01-quickstart.md)
- [Add Support for a New Agent](../add-support-for-a-new-agent/01-overview.md)
