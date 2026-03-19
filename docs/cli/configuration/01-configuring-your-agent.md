# Configuring Your Agent

Create an agent config to control which agent, model, and settings are used during an eval.

```bash
# Scaffold a new agent config from an existing definition
margin init agent-config \
  --agent-config ./configs/my-agent-configs/claude-sonnet \
  --definition ./configs/agent-definitions/claude-code
```

## Two modes: direct vs. unified

### Direct mode — full control

Specify agent-specific settings in `[input]` (shape defined by the agent definition's `schema.json`):

```toml
kind = "agent_config"
name = "claude-code-opus"
definition = "../../agent-definitions/claude-code"
mode = "direct"

[input]
claude_version = "latest"
startup_args = ["--model", "opus", "--effort", "high"]
run_args = []
settings_json = """
{
  "model": "opus",
  "permissionMode": "acceptEdits"
}
"""
```

### Unified mode — compare agents apples-to-apples

Specify just `model` + `reasoning_level`. The agent definition translates these at compile time:

```toml
kind = "agent_config"
name = "claude-code-unified"
definition = "../../agent-definitions/claude-code"
mode = "unified"

[unified]
model = "sonnet"
reasoning_level = "medium"
```

| Agent | Allowed models | Allowed reasoning levels |
|---|---|---|
| Claude Code | `opus`, `sonnet`, `haiku`, `sonnet[1m]` | `low`, `medium`, `high` |
| Codex | `gpt-5-codex`, `gpt-5`, `gpt-5.1-codex`, etc. | `low`, `medium`, `high` |
| OpenCode | `*` (any model string) | `low`, `medium`, `high` |

## More built-in examples

**Codex (direct):**

```toml
kind = "agent_config"
name = "codex-default"
definition = "../../agent-definitions/codex"
mode = "direct"

[input]
codex_version = "latest"
startup_args = []
run_args = []
config_toml = """
model = "gpt-5-codex"
approval_policy = "never"
"""
```

**OpenCode (unified):**

```toml
kind = "agent_config"
name = "opencode-unified"
definition = "../../agent-definitions/opencode"
mode = "unified"

[unified]
model = "openai/gpt-5"
reasoning_level = "medium"
```

## Adding skills

Skills are reusable instruction sets materialized into the agent's skills directory before execution:

```toml
[[skills]]
path = "./skills/code-review"

[[skills]]
path = "./skills/testing-best-practices"
```

Each skill directory must contain a `SKILL.md` file.

## Adding project instructions

Provide a shared instructions file (e.g. `CLAUDE.md`) placed into the test workspace:

```toml
[agents_md]
path = "./CLAUDE.md"
```

The filename used in the container is set by the agent definition (`CLAUDE.md` for Claude Code, `AGENTS.md` for Codex/OpenCode).

## Passing env vars and bind mounts

```bash
margin run \
  --suite ./suites/my-suite \
  --agent-config ./configs/my-agent-configs/claude-sonnet \
  --eval ./my-eval.toml \
  --agent-env MY_VAR=my-value \
  --agent-env ANOTHER_VAR=another-value \
  --agent-bind /host/path=/container/path
```

Both flags are repeatable.

## Next steps

- [Configuring Your Eval](./02-configuring-your-eval.md)
- [Creating Your Own Eval](../creating-your-own-eval/01-quickstart.md)
- [Add Support for a New Agent](../add-support-for-a-new-agent/01-overview.md)
