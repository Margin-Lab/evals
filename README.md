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

Margin Eval is the most robust orchestrator for running evals against CLI agents like **Claude Code**, **Codex**, and **OpenCode**. It measures accuracy, token usage, runtime, and captures full execution traces, all in a standardized, reproducible local format.

- **Test any configuration**: agents, models, MCPs, skills, prompting strategies
- **Compare side-by-side**: unified CLI, config format, and output across all agents
- **Reproduce any run**: every run is compiled into an immutable, self-contained bundle
- **Resume on failure**: automatically retry infra failures without re-running completed cases

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
margin check
```

Update an installer-managed binary:

```bash
margin update
```

### Run first evals

Dry-run your first eval (no token usage)
```bash
margin run \
  --suite ./suites/swe-minimal-test-suite \
  --agent-config ./configs/example-agent-configs/codex-unified/ \
  --eval ./configs/example-eval-configs/default.toml \
  --dry-run
```

Run your first eval using an API key (minimal test suite, will use small amount of tokens)
```bash
export ANTHROPIC_API_KEY=<API_KEY>
margin run \
  --suite ./suites/swe-minimal-test-suite \
  --agent-config ./configs/example-agent-configs/claude-code-default \
  --eval ./configs/example-eval-configs/default.toml \
```

Run your first eval using your agents OAuth, margin will auto-detect your OAuth file (minimal test suite, will use small amount of tokens)
```bash
margin run \
  --suite ./suites/swe-minimal-test-suite \
  --agent-config ./configs/example-agent-configs/codex-unified/ \
  --eval ./configs/example-eval-configs/default.toml \
```

Or, run with a specific OAuth file (minimal test suite, will use small amount of tokens)
```bash
margin run \
  --suite ./suites/swe-minimal-test-suite \
  --agent-config ./configs/example-agent-configs/codex-unified/ \
  --eval ./configs/example-eval-configs/default.toml \
  --auth-file-path /path/to/credentials.json
```

### More examples

**Run Claude Code on SWE-Bench Pro:**

```bash
margin run \
  --suite ./suites/swe-bench-pro \
  --agent-config ./configs/example-agent-configs/claude-code-default \
  --eval ./configs/example-eval-configs/default.toml \
```

**Run Codex with unified config:**

```bash
margin run \
  --suite ./suites/terminal-bench-2 \
  --agent-config ./configs/example-agent-configs/codex-unified \
  --eval ./configs/example-eval-configs/default.toml \
```

**Resume a run:**

```bash
margin run --resume-from <run-id>
```

**Scaffold a new agent config:**

```bash
margin init agent-config \
  --agent-config ./configs/my-agent-configs/claude-sonnet \
  --definition ./configs/agent-definitions/claude-code
```

## Built-in eval suites

| Suite | Description |
|-------|-------------|
| `swe-minimal-test-suite` | 3-case smoke test (astropy, django, sphinx) |
| `swe-bench-verified` | Full SWE-Bench Verified benchmark |
| `swe-bench-pro` | SWE-Bench Pro benchmark |
| `swe-bench-pro-curated-50` | Curated 50-case subset of SWE-Bench Pro |
| `terminal-bench-2` | 30+ terminal task cases |

Many more evals coming soon.

## Supported agents

| Agent | Config examples |
|-------|----------------|
| **Claude Code** | `claude-code-default`, `claude-code-unified` |
| **Codex** | `codex-default`, `codex-unified` |
| **OpenCode** | `opencode-default`, `opencode-unified` |

Agent configs support two modes: **direct** (full agent-specific control) and **unified** (one config format that works across all supported agents).

Many more agents coming soon.

## Project structure

```
cli/                    CLI tool — compiles configs into run bundles
runner/runner-core/     Shared engine — state machine, worker pool, run store
runner/runner-local/    Local runner — Docker executor, filesystem persistence
agent-server/           In-container runtime — agent lifecycle, trajectory capture
configs/                Agent definitions and example configs
suites/                 Built-in eval suites
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
