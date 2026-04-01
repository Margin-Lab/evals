# Add Support for a New Agent

Integrate an agent that isn't built-in (Claude Code, Codex, Gemini CLI, OpenCode, Pi) by creating your own agent definition.

```bash
# Scaffold the definition
margin init agent-definition --definition ./configs/agent-definitions/my-agent

# Create a config for it
margin init agent-config \
  --agent-config ./configs/my-agent-configs/my-agent-default \
  --definition ./configs/agent-definitions/my-agent

# Test it
margin run \
  --suite ./suites/my-suite \
  --agent-config ./configs/my-agent-configs/my-agent-default \
  --eval ./my-eval.toml \
  --dry-run
```

## What gets scaffolded

```
configs/agent-definitions/my-agent/
  definition.toml       # Auth, schema, hooks, optional features
  schema.json           # JSON Schema for agent config [input] section
  hooks/
    install-check.sh    # Is the agent already installed?
    install-run.sh      # Install the agent
    run-prepare.js      # Produce the launch command
```

## 1. Configure definition.toml

```toml
kind = "agent_definition"
name = "my-agent"

[auth]
required_env = ["MY_AGENT_API_KEY"]

[config]
schema = "schema.json"

[install]
check = "hooks/install-check.sh"
run = "hooks/install-run.sh"

[toolchains.node]
minimum = "20"
preferred = "24"

[run]
prepare = "hooks/run-prepare.js"
```

For OAuth-style auth discovery, add a `local_credentials` entry with one or more ordered sources:

```toml
[[auth.local_credentials]]
required_env = "MY_AGENT_API_KEY"
run_home_rel_path = ".my-agent/credentials.json"

  [[auth.local_credentials.sources]]
  kind = "home_file"
  home_rel_path = ".my-agent/credentials.json"
```

## 2. Define the config schema

Edit `schema.json` to describe what `[input]` accepts:

```json
{
  "type": "object",
  "required": ["command"],
  "additionalProperties": false,
  "properties": {
    "command": {
      "type": "array",
      "minItems": 1,
      "items": { "type": "string", "minLength": 1 }
    },
    "cwd": { "type": "string", "minLength": 1 },
    "env": {
      "type": "object",
      "additionalProperties": { "type": "string" }
    }
  }
}
```

The runner validates every agent config's `[input]` against this schema at compile time.

## 3. Implement the hooks

**install-check.sh** — print `{"installed":true}` or `{"installed":false}`:

```bash
#!/usr/bin/env bash
set -euo pipefail
if command -v my-agent &> /dev/null; then
  printf '{"installed":true}\n'
else
  printf '{"installed":false}\n'
fi
```

**install-run.sh** — install the agent, print `{"installed":true}`:

```bash
#!/usr/bin/env bash
set -euo pipefail
npm install -g my-agent@latest
printf '{"installed":true}\n'
```

**run-prepare.js** — read context, print launch JSON:

```js
#!/usr/bin/env node
"use strict";

const fs = require("fs");

const ctx = JSON.parse(fs.readFileSync(process.env.AGENT_CONTEXT_JSON, "utf8"));

const inputCfg = ((ctx.config || {}).input) || {};
const command = inputCfg.command || ["my-agent"];
const env = { ...(((ctx.run || {}).env) || {}), ...((inputCfg.env) || {}) };
const cwd = inputCfg.cwd || ((ctx.run || {}).cwd) || "/";

process.stdout.write(`${JSON.stringify({
  path: command[0],
  args: command.slice(1),
  env,
  dir: cwd,
})}\n`);
```

The output must contain: `path` (executable), `args` (arguments list), `env` (environment dict), `dir` (working directory).

For JS hooks, declare `[toolchains.node]` in the definition. `agent-server` provisions managed Node before running config, install, run, snapshot, and trajectory hooks.

## 4. Create an agent config

```toml
kind = "agent_config"
name = "my-agent-default"
definition = "../../agent-definitions/my-agent"
mode = "direct"

[input]
command = ["my-agent", "run", "--auto"]
cwd = "/testbed"
env = { MY_SETTING = "value" }
```

## 5. Test with dry-run, then run for real

```bash
# Validates definition, schema, config, and compiles the bundle — no tokens spent
margin run \
  --suite ./suites/my-suite \
  --agent-config ./configs/my-agent-configs/my-agent-default \
  --eval ./my-eval.toml \
  --dry-run

# Run for real
margin run \
  --suite ./suites/my-suite \
  --agent-config ./configs/my-agent-configs/my-agent-default \
  --eval ./my-eval.toml
```

## Optional features

Add any of these to `definition.toml`:

```toml
# Toolchain requirements
[toolchains.node]
minimum = "18"
preferred = "24"

# Shared skills support
[skills]
home_rel_dir = ".my-agent/skills"

# Shared project instructions
[agents_md]
filename = "AGENTS.md"

# Unified mode (for cross-agent comparison)
[config.unified]
translate = "hooks/translate-unified.js"
allowed_models = ["model-a", "model-b"]
allowed_reasoning_levels = ["low", "medium", "high"]

# Post-run snapshot collection
[snapshot]
prepare = "hooks/snapshot-prepare.js"

# Post-run trajectory collection
[trajectory]
collect = "hooks/trajectory-collect.js"
```

## Built-in definitions for reference

| Definition | Path | Auth |
|---|---|---|
| Claude Code | `configs/agent-definitions/claude-code/` | `ANTHROPIC_API_KEY` |
| Codex | `configs/agent-definitions/codex/` | `OPENAI_API_KEY` |
| Gemini CLI | `configs/agent-definitions/gemini-cli/` | `GEMINI_API_KEY` |
| OpenCode | `configs/agent-definitions/opencode/` | provider-qualified (`openai/*`, `anthropic/*`, `google/*`, fallback `*` with no auth) |
| Pi | `configs/agent-definitions/pi/` | provider-qualified (`openai/*`, `anthropic/*`, `google/*`, fallback `*` with no auth) |

Study these for complete hook implementations and translate-unified examples.

## Next steps

- [Running Your First Eval](../quickstart/02-running-your-first-eval.md) — run a suite with your custom agent
- [Creating Your Own Eval](../creating-your-own-eval/01-quickstart.md) — build test cases tailored to your agent
