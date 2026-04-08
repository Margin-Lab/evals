<p align="center">
  <a href="https://marginlab.ai"><img src="assets/logo.png" alt="Margin Eval" width="600"></a>
</p>

<p align="center">
  Open-source eval runtime for coding agents.
  <br /><br />
  <a href="https://marginlab.ai">marginlab.ai</a> · <a href="https://docs.marginlab.ai">Documentation</a>
  <br />
  <a href="https://x.com/themarginguy"><img src="https://img.shields.io/twitter/follow/themarginguy?style=social" alt="Follow on X"></a>
</p>

---

Margin Eval is the most robust orchestrator for running evals against CLI agents like **Claude Code**, **Codex**, **Gemini CLI**, **OpenCode**, and **Pi**. It measures accuracy, token usage, runtime, and captures full execution traces, all in a standardized, reproducible local format.

- **Test any configuration**: agents, models, MCPs, skills, prompting strategies
- **Compare side-by-side**: unified CLI, config format, and output across all agents
- **Reproduce any run**: every run is compiled into an immutable, self-contained bundle
- **Resume on failure**: automatically retry infra failures without re-running completed cases
- **Extensible**: Define your own agents, test suites, configurations, etc.

## Quickstart

### Prerequisites

- [Docker](https://docs.docker.com/get-docker/) installed and running
- An API key or OAuth credentials for your agent provider

### Install

Install the latest stable release:

```bash
curl -fsSL https://raw.githubusercontent.com/Margin-Lab/evals/main/scripts/install.sh | bash
```

Check your installation is ready to run an eval

```bash
margin --version
margin check
```
Update an installer-managed binary:

```bash
margin update
```

### Run first evals

Dry-run your first eval (no token usage; tests still run)
```bash
margin run \
  --suite git::https://github.com/Margin-Lab/test-suites.git//swe-minimal-test-suite \
  --agent-config ~/.margin/configs/example-agent-configs/codex-unified \
  --eval ~/.margin/configs/example-eval-configs/default.toml \
  --dry-run
```

Run your first eval using an API key
```bash
export ANTHROPIC_API_KEY=<API_KEY>
margin run \
  --suite git::https://github.com/Margin-Lab/test-suites.git//swe-minimal-test-suite \
  --agent-config ~/.margin/configs/example-agent-configs/claude-code-default \
  --eval ~/.margin/configs/example-eval-configs/default.toml \
```

Run Gemini CLI with the unified config
```bash
export GEMINI_API_KEY=<API_KEY>
margin run \
  --suite git::https://github.com/Margin-Lab/test-suites.git//swe-minimal-test-suite \
  --agent-config ~/.margin/configs/example-agent-configs/gemini-cli-unified \
  --eval ~/.margin/configs/example-eval-configs/default.toml \
```

Run your first eval using your agents OAuth, Margin will auto-detect your OAuth file
```bash
margin run \
  --suite git::https://github.com/Margin-Lab/test-suites.git//swe-minimal-test-suite \
  --agent-config ~/.margin/configs/example-agent-configs/codex-unified \
  --eval ~/.margin/configs/example-eval-configs/default.toml \
```

Or, run with a specific OAuth file
```bash
margin run \
  --suite git::https://github.com/Margin-Lab/test-suites.git//swe-minimal-test-suite \
  --agent-config ~/.margin/configs/example-agent-configs/codex-unified \
  --eval ~/.margin/configs/example-eval-configs/default.toml \
  --auth-file-path /path/to/credentials.json
```

### More examples

**Run Claude Code on SWE-Bench Pro:**

```bash
margin run \
  --suite git::https://github.com/Margin-Lab/swe-suites.git//swe-bench-pro \
  --agent-config ~/.margin/configs/example-agent-configs/claude-code-default \
  --eval ~/.margin/configs/example-eval-configs/default.toml \
```

**Run Codex with unified config:**

```bash
margin run \
  --suite git::https://github.com/Margin-Lab/swe-suites.git//terminal-bench-2 \
  --agent-config ~/.margin/configs/example-agent-configs/codex-unified \
  --eval ~/.margin/configs/example-eval-configs/default.toml \
```

**Resume a run:**

```bash
margin run --resume-from <run-id>
```

**Resume with updated suite/config inputs:**

```bash
margin run \
  --resume-from <run-id> \
  --suite ./suites/smoke \
  --agent-config ./configs/my-agent-configs/claude-sonnet \
  --eval ./configs/my-evals/local.toml
```

**Scaffold a new agent config:**

```bash
margin init agent-config \
  --agent-config ./configs/my-agent-configs/claude-sonnet \
  --definition ./configs/agent-definitions/claude-code
```

## Official eval suites

Official SWE eval suites are hosted at `https://github.com/Margin-Lab/swe-suites.git`

| Suite | Example `--suite` value |
|-------|--------------------------|
| `swe-bench-verified` | `git::https://github.com/Margin-Lab/swe-suites.git//swe-bench-verified` |
| `swe-bench-pro` | `git::https://github.com/Margin-Lab/swe-suites.git//swe-bench-pro` |
| `swe-bench-pro-curated-50` | `git::https://github.com/Margin-Lab/swe-suites.git//swe-bench-pro-curated-50` |
| `terminal-bench-2` | `git::https://github.com/Margin-Lab/swe-suites.git//terminal-bench-2` |

Many more built-in evals coming soon.

See Creating [Your Own Eval](https://docs.marginlab.ai/creating-your-own-eval/01-quickstart) for a guide on how to create your own eval suite.

## Supported agents

| Agent | Config examples |
|-------|----------------|
| **Claude Code** | `claude-code-default`, `claude-code-unified` |
| **Codex** | `codex-default`, `codex-unified` |
| **Gemini CLI** | `gemini-cli-default`, `gemini-cli-unified` |
| **OpenCode** | `opencode-default`, `opencode-unified` |
| **Pi** | `pi-default`, `pi-unified` |

Agent configs support two modes: **direct** (full agent-specific control) and **unified** (one config format that works across all supported agents).

Many more built-in agents coming soon. See [Adding a New Agent](https://docs.marginlab.ai/add-support-for-a-new-agent/01-overview) for a guide on how to add a new agent, and [Configuring Your Agent](https://docs.marginlab.ai/configuration/01-configuring-your-agent) for a guide on how to configure an existing agent.

## Project structure

```
cli/                    CLI tool — compiles configs into run bundles
runner/runner-core/     Shared engine — state machine, worker pool, run store
runner/runner-local/    Local runner — Docker executor, filesystem persistence
agent-server/           In-container runtime — agent lifecycle, trajectory capture
configs/                Agent definitions and example configs
suites/                 Local custom suites during development
docs/                   Documentation
```

## Documentation

- [What is Margin?](https://docs.marginlab.ai/welcome/01-what-is-margin)
- [Installation](https://docs.marginlab.ai/quickstart/01-install)
- [Running Your First Eval](https://docs.marginlab.ai/quickstart/02-running-your-first-eval)
- [Configuring Your Agent](https://docs.marginlab.ai/configuration/01-configuring-your-agent)
- [Configuring Your Eval](https://docs.marginlab.ai/configuration/02-configuring-your-eval)
- [Creating Your Own Eval](https://docs.marginlab.ai/creating-your-own-eval/01-quickstart)
- [Adding a New Agent](https://docs.marginlab.ai/add-support-for-a-new-agent/01-overview)

## License

This project is licensed under the [GNU Affero General Public License v3.0](LICENSE).

Please contact us at hello@marginlab.ai for questions
