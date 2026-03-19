<p align="center">
  <img src="assets/logo.png" alt="Margin Eval" width="600">
</p>

<p align="center">
  Open-source eval runtime for coding agents.
  <br /><br />
  <a href="https://marginlab.ai">marginlab.ai</a> · <a href="docs/cli/SUMMARY.md">Documentation</a> · <a href="https://x.com/themarginguy"><img src="https://img.shields.io/twitter/follow/themarginguy?style=social" alt="Follow on X"></a>
</p>

---

Margin Eval runs containerized evaluations against coding agents like **Claude Code**, **Codex**, and **OpenCode**. It measures accuracy, token usage, runtime, and captures full execution traces — all in a standardized, reproducible local format.

- **Test any configuration** — agents, models, MCPs, skills, prompting strategies
- **Compare side-by-side** — unified CLI, config format, and output across all agents
- **Reproduce any run** — every run is compiled into an immutable, self-contained bundle
- **Resume on failure** — automatically retry infra failures without re-running completed cases

## Quickstart

### Prerequisites

- [Docker](https://docs.docker.com/get-docker/) installed and running
- An API key or OAuth credentials for your agent provider

### Install

Build from source:

```bash
git clone https://github.com/marginlab/eval.git
cd eval
scripts/build-cli-agent-server.sh
```

This produces a self-contained binary at `./bin/margin`. Verify it works:

```bash
./bin/margin help
```

### Set credentials

**API key:**

```bash
export ANTHROPIC_API_KEY=<your-key>
```

**OAuth (Claude Code Pro/Max, Codex Pro, etc.):** Margin automatically detects valid OAuth credentials at their standard paths. To use a specific credential file:

```bash
margin run ... --auth-file-path /path/to/credentials.json
```

### Create an eval config

Scaffold a default eval config to control concurrency and timeouts:

```bash
margin init eval-config --eval ./my-eval.toml
```

This creates a TOML file you can customize:

```toml
kind = "eval_config"
name = "my-eval"
max_concurrency = 2
fail_fast = false
retry_count = 1
instance_timeout_seconds = 1800
```

### Run your first eval

```bash
margin run \
  --suite ./suites/swe-minimal-test-suite \
  --agent-config ./configs/example-agent-configs/claude-code-default \
  --eval ./my-eval.toml
```

This runs `swe-minimal-test-suite`, a 3-case subset of SWE-Bench Verified designed for quick local testing. Results are saved to `runs/<run-id>/`.

### More examples

**Run Claude Code on SWE-Bench Pro:**

```bash
margin run \
  --suite ./suites/swe-bench-pro \
  --agent-config ./configs/example-agent-configs/claude-code-default \
  --eval ./my-eval.toml
```

**Run Codex with unified config:**

```bash
margin run \
  --suite ./suites/terminal-bench-2 \
  --agent-config ./configs/example-agent-configs/codex-unified \
  --eval ./my-eval.toml
```

**Resume a failed run:**

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

## Supported agents

| Agent | Config examples |
|-------|----------------|
| **Claude Code** | `claude-code-default`, `claude-code-unified` |
| **Codex** | `codex-default`, `codex-unified` |
| **OpenCode** | `opencode-default`, `opencode-unified` |

Agent configs support two modes: **direct** (full agent-specific control) and **unified** (model + reasoning level for apples-to-apples comparison across agents).

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

- [What is Margin?](docs/cli/welcome/01-what-is-margin.md)
- [Installation](docs/cli/quickstart/01-install.md)
- [Running Your First Eval](docs/cli/quickstart/02-running-your-first-eval.md)
- [Configuring Your Agent](docs/cli/configuration/01-configuring-your-agent.md)
- [Configuring Your Eval](docs/cli/configuration/02-configuring-your-eval.md)
- [Creating Your Own Eval](docs/cli/creating-your-own-eval/01-quickstart.md)
- [Adding a New Agent](docs/cli/add-support-for-a-new-agent/01-overview.md)

## License

See [LICENSE](LICENSE) for details.
